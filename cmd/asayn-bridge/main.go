package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	llmtypes "github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

const compactRetainedPrompt = "Recall what we worked on before."

const compactInstructionPrompt = `Create a rigorous continuation summary of the visible conversation so the future main agent can continue with this summary as its sole memory after compression.

The summary is for the future main agent, not for the user. It must be complete enough to continue work without rereading hidden history. Do not write a vague narrative. Do not omit earlier user turns just because later turns look more important.

Output exactly these top-level sections, in this order:

## Conversation Ledger
Cover every visible user turn in chronological order. For each turn, include the user request, assistant actions, key tool results, and outcome. Preserve exact constraints and unresolved work.

## Current State
Summarize the current goal, changed files, tests/builds/installs, running or interrupted work, and active context boundary.

## Pending Work
List concrete next tasks, known bugs, missing verification, risks, and the likely next command or file to inspect.

## Standing User Preferences And Workflow Habits
Extract only preferences supported by the conversation; otherwise say they are not established.

## Critical Constraints
Preserve non-negotiable constraints, edge cases, and exact strings that future work must not break.

Be chronological and exhaustive. Do not claim work completed without evidence. Output only the continuation summary.`

type request struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type envelope struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	OK        bool   `json:"ok,omitempty"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
}

type bridge struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	ctx     *app.Context
	sess    *session.Session
	cancel  context.CancelFunc
	running bool
}

func main() {
	b := &bridge{}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			b.write(envelope{Type: "response", OK: false, Error: err.Error()})
			continue
		}
		go b.handle(req)
	}
	if b.ctx != nil {
		if !session.HasContent(b.sess) {
			_ = b.ctx.Sessions.Delete(b.sess)
		}
		b.ctx.Tools.Shutdown()
	}
}

func (b *bridge) handle(req request) {
	data, err := b.dispatch(req)
	if err != nil {
		b.write(envelope{Type: "response", ID: req.ID, OK: false, Error: err.Error()})
		return
	}
	b.write(envelope{Type: "response", ID: req.ID, OK: true, Data: data})
}

func (b *bridge) dispatch(req request) (any, error) {
	switch req.Action {
	case "initialize":
		var p struct {
			Workspace string `json:"workspace"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if strings.TrimSpace(p.Workspace) == "" {
			p.Workspace, _ = os.Getwd()
		}
		ctx, err := app.Bootstrap(p.Workspace)
		if err != nil {
			return nil, err
		}
		sess, err := ctx.Sessions.New("", ctx.Root.Name)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.ctx, b.sess = ctx, sess
		b.mu.Unlock()
		return b.snapshot()
	case "snapshot":
		return b.snapshot()
	case "list_sessions":
		ctx, _, err := b.ready()
		if err != nil {
			return nil, err
		}
		return ctx.Sessions.List()
	case "new_session":
		ctx, old, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if !session.HasContent(old) {
			_ = ctx.Sessions.Delete(old)
		}
		sess, err := ctx.Sessions.New(p.Name, ctx.Root.Name)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.sess = sess
		b.mu.Unlock()
		return b.snapshot()
	case "load_session":
		ctx, old, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		sess, err := ctx.Sessions.Load(p.ID)
		if err != nil {
			return nil, err
		}
		if !session.HasContent(old) {
			_ = ctx.Sessions.Delete(old)
		}
		ctx.Tools.RestoreSubAgents(sess, sess.SubAgents, ctx.SubSessions)
		if err := b.applyRoot(sess.RootAgent); err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.sess = sess
		b.mu.Unlock()
		return b.snapshot()
	case "rename_session":
		ctx, sess, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if strings.TrimSpace(p.Name) == "" {
			return nil, errors.New("name is required")
		}
		if err := ctx.Sessions.Rename(sess, strings.TrimSpace(p.Name)); err != nil {
			return nil, err
		}
		return b.snapshot()
	case "fork_session":
		ctx, sess, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		fork, err := ctx.Sessions.Fork(sess, strings.TrimSpace(p.Name))
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.sess = fork
		b.mu.Unlock()
		return b.snapshot()
	case "select_agent":
		_, sess, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if err := b.applyRoot(p.Name); err != nil {
			return nil, err
		}
		b.mu.Lock()
		sess.RootAgent = p.Name
		b.mu.Unlock()
		_ = b.ctx.Sessions.Save(sess)
		return b.snapshot()
	case "catalog":
		ctx, _, err := b.ready()
		if err != nil {
			return nil, err
		}
		roots, _ := config.ListAgentInfos(ctx.Paths, config.RootAgentKind)
		skills, _ := config.ListSkills(ctx.Paths)
		mcps, _ := config.ListMCPServerInfos(ctx.Paths)
		return map[string]any{"agents": roots, "skills": skills, "mcp": mcps, "providers": ctx.API.Providers, "config": ctx.Root}, nil
	case "save_agent_config":
		ctx, _, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p config.AgentConfig
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return nil, err
		}
		name := p.Name
		if name == "" {
			name = ctx.Root.Name
		}
		_, err = config.SaveAgent(ctx.Paths, config.RootAgentKind, name, func(c *config.AgentConfig) { *c = p; c.Name = name })
		if err != nil {
			return nil, err
		}
		if name == ctx.Root.Name {
			if err := b.applyRoot(name); err != nil {
				return nil, err
			}
		}
		return b.snapshot()
	case "ask", "retry", "compact":
		_, _, err := b.readyIdle()
		if err != nil {
			return nil, err
		}
		var p struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if req.Action == "ask" && strings.TrimSpace(p.Prompt) != "" {
			b.mu.Lock()
			if len(b.sess.InputHistory) == 0 || b.sess.InputHistory[len(b.sess.InputHistory)-1] != strings.TrimSpace(p.Prompt) {
				b.sess.InputHistory = append(b.sess.InputHistory, strings.TrimSpace(p.Prompt))
				_ = b.ctx.Sessions.Save(b.sess)
			}
			b.mu.Unlock()
		}
		b.mu.Lock()
		b.running = true
		runCtx, cancel := context.WithCancel(context.Background())
		b.cancel = cancel
		b.mu.Unlock()
		go b.runAgent(req.ID, req.Action, p.Prompt, runCtx)
		return map[string]bool{"started": true}, nil
	case "cancel":
		b.mu.Lock()
		if b.cancel != nil {
			b.cancel()
		}
		b.mu.Unlock()
		return map[string]bool{"cancelled": true}, nil
	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

func (b *bridge) runAgent(requestID, action, prompt string, runCtx context.Context) {
	b.mu.Lock()
	ctx, sess := b.ctx, b.sess
	b.mu.Unlock()
	emit := func(event llm.AgentEvent) {
		b.write(envelope{Type: "event", RequestID: requestID, Data: map[string]any{"kind": event.Kind, "text": event.Text, "usage": event.Usage}})
	}
	var answer string
	var err error
	var use llmtypes.Usage
	if action == "compact" {
		answer, use, err = b.compact(runCtx, sess, emit)
	} else if action == "retry" {
		answer, use, err = ctx.Agent.RetryWithEvents(runCtx, sess, emit)
	} else {
		answer, use, err = ctx.Agent.AskWithEvents(runCtx, sess, prompt, emit)
	}
	if err == nil {
		model := ctx.Root.Model
		if action == "compact" {
			if cfg, loadErr := config.LoadAgent(ctx.Paths, config.SpecialAgentKind, "compact_agent"); loadErr == nil {
				model = cfg.Model
			}
			sess.LastTotalTokens = use.CompletionTokens
		} else {
			sess.LastTotalTokens = use.TotalTokens
		}
		_ = ctx.UsageTracker.Log(sess.ID, sess.Name, model, use)
		_ = ctx.Sessions.Save(sess)
		threshold := ctx.Root.AutoCompactThresholdPercent
		if threshold <= 0 {
			threshold = 80
		}
		if action != "compact" && use.TotalTokens > 0 && ctx.Root.ContextWindow > 0 && use.TotalTokens*100 >= ctx.Root.ContextWindow*threshold {
			emit(llm.AgentEvent{Kind: "auto_compact", Text: "Auto-compacting context"})
			_, compactUse, compactErr := b.compact(runCtx, sess, emit)
			if compactErr != nil {
				err = compactErr
			} else {
				compactModel := ctx.Root.Model
				if cfg, loadErr := config.LoadAgent(ctx.Paths, config.SpecialAgentKind, "compact_agent"); loadErr == nil {
					compactModel = cfg.Model
				}
				_ = ctx.UsageTracker.Log(sess.ID, sess.Name, compactModel, compactUse)
				sess.LastTotalTokens = compactUse.CompletionTokens
				_ = ctx.Sessions.Save(sess)
			}
		}
	}
	b.mu.Lock()
	b.running = false
	b.cancel = nil
	b.mu.Unlock()
	payload := map[string]any{"kind": "done", "answer": answer}
	if err != nil {
		payload["error"] = err.Error()
	}
	b.write(envelope{Type: "event", RequestID: requestID, Data: payload})
}

func (b *bridge) compact(runCtx context.Context, sess *session.Session, emit func(llm.AgentEvent)) (string, llmtypes.Usage, error) {
	cfg, err := config.LoadAgent(b.ctx.Paths, config.SpecialAgentKind, "compact_agent")
	if err != nil {
		return "", llmtypes.Usage{}, fmt.Errorf("load compact_agent: %w", err)
	}
	limits := config.ModelLimitsFor(b.ctx.API, cfg.Provider, cfg.Model)
	cfg.ContextWindow, cfg.MaxOutputTokens = limits.ContextWindow, limits.MaxOutputTokens
	temp := *sess
	temp.Messages = append([]llmtypes.ChatMessage(nil), sess.Messages...)
	temp.RootAgent = cfg.Name
	temp.VisibleSkills = map[string]bool{}
	for name, visible := range sess.VisibleSkills {
		temp.VisibleSkills[name] = visible
	}
	exec := tools.NewBasicExecutor(b.ctx.Paths, b.ctx.Sessions, cfg.MaxOutputLines)
	defer exec.Shutdown()
	agent := llm.NewSubAgent(b.ctx.API, cfg, b.ctx.Paths, exec)
	agent.RefreshSystemPrompt(&temp)
	base := len(sess.Messages)
	answer, use, err := agent.AskWithEvents(runCtx, &temp, compactInstructionPrompt, emit)
	if err != nil {
		return answer, use, err
	}
	sess.Messages = append(sess.Messages,
		llmtypes.ChatMessage{Role: "user", Content: compactRetainedPrompt},
		llmtypes.ChatMessage{Role: "assistant", Content: answer},
	)
	sess.CompactedBefore = base
	return answer, use, nil
}

func (b *bridge) applyRoot(name string) error {
	if name == "" {
		name = "default"
	}
	root, err := config.LoadAgent(b.ctx.Paths, config.RootAgentKind, name)
	if err != nil {
		return err
	}
	limits := config.ModelLimitsFor(b.ctx.API, root.Provider, root.Model)
	root.ContextWindow, root.MaxOutputTokens = limits.ContextWindow, limits.MaxOutputTokens
	b.ctx.Root = root
	b.ctx.Tools.SetAgentLimits(root.MaxOutputLines, root.AllowParallelShell, root.AllowInteractiveShell)
	b.ctx.Tools.SetVisibleMCP(root.VisibleMCP)
	b.ctx.Agent = llm.NewAgent(b.ctx.API, root, b.ctx.Paths, b.ctx.Tools)
	return nil
}

func (b *bridge) ready() (*app.Context, *session.Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx == nil || b.sess == nil {
		return nil, nil, errors.New("bridge is not initialized")
	}
	return b.ctx, b.sess, nil
}

func (b *bridge) readyIdle() (*app.Context, *session.Session, error) {
	ctx, sess, err := b.ready()
	if err != nil {
		return nil, nil, err
	}
	b.mu.Lock()
	running := b.running
	b.mu.Unlock()
	if running {
		return nil, nil, errors.New("agent is running")
	}
	return ctx, sess, nil
}

func (b *bridge) snapshot() (map[string]any, error) {
	ctx, sess, err := b.ready()
	if err != nil {
		return nil, err
	}
	stats, _ := ctx.UsageTracker.GetStats(sess.ID)
	items, _ := ctx.Sessions.List()
	return map[string]any{"session": sess, "sessions": items, "agent": ctx.Root, "stats": stats, "workspace": ctx.Paths.WorkspaceRoot}, nil
}

func (b *bridge) write(v envelope) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}
