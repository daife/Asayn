package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

type Agent struct {
	client     *Client
	root       config.AgentConfig
	paths      config.Paths
	tools      *tools.Executor
	isSubAgent bool
}

type AgentEvent struct {
	Kind string
	Text string
}

const maxToolCallRounds = 24

func (e AgentEvent) Display() string {
	switch e.Kind {
	case "thinking_start":
		return "thinking..."
	case "thinking_delta":
		return "thinking: " + e.Text
	case "thinking":
		return "thinking: " + e.Text
	case "assistant":
		return "assistant: " + e.Text
	case "tool_start":
		return "tool: " + e.Text
	case "tool_result":
		return "tool result: " + e.Text
	case "tool_error":
		return "tool error: " + e.Text
	default:
		return e.Text
	}
}

func NewAgent(api config.APIConfig, root config.AgentConfig, paths config.Paths, executor *tools.Executor) *Agent {
	return &Agent{
		client: NewClient(api),
		root:   root,
		paths:  paths,
		tools:  executor,
	}
}

func NewSubAgent(api config.APIConfig, root config.AgentConfig, paths config.Paths, executor *tools.Executor) *Agent {
	agent := NewAgent(api, root, paths, executor)
	agent.isSubAgent = true
	return agent
}

func (a *Agent) Ask(ctx context.Context, sess *session.Session, prompt string) (string, error) {
	return a.AskWithEvents(ctx, sess, prompt, nil)
}

func (a *Agent) AskWithEvents(ctx context.Context, sess *session.Session, prompt string, emit func(AgentEvent)) (string, error) {
	a.EnsureSystemPrompt(sess)
	baseLen := len(sess.Messages)
	sess.Messages = append(sess.Messages, types.ChatMessage{Role: "user", Content: prompt})

	toolSchemas := a.tools.Schemas(a.isSubAgent)
	for step := 0; step < maxToolCallRounds; step++ {
		if emit != nil {
			emit(AgentEvent{Kind: "thinking_start"})
		}
		msg, err := a.client.ChatStream(ctx, a.root.Model, messagesForAPI(sess.Messages, a.visibleSkillSet(sess)), toolSchemas, func(delta StreamDelta) {
			if emit != nil && delta.ReasoningContent != "" {
				emit(AgentEvent{Kind: "thinking_delta", Text: delta.ReasoningContent})
			}
		})
		if err != nil {
			if len(sess.Messages) > baseLen {
				sess.Messages = sess.Messages[:baseLen]
			}
			return "", err
		}
		sess.Messages = append(sess.Messages, msg)
		if emit != nil && msg.ReasoningContent != "" {
			emit(AgentEvent{Kind: "thinking", Text: msg.ReasoningContent})
		}
		if emit != nil && msg.Content != "" && len(msg.ToolCalls) > 0 {
			emit(AgentEvent{Kind: "assistant", Text: msg.Content})
		}
		if len(msg.ToolCalls) == 0 {
			return msg.Content, nil
		}
		for _, call := range msg.ToolCalls {
			result := a.runToolCall(sess, call, emit)
			sess.Messages = append(sess.Messages, types.ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
	answer := fmt.Sprintf("I ended this turn after %d tool-call rounds to avoid an infinite tool loop. Some tool or sub-agent work may still be in progress; send a follow-up message to continue from the current session state.", maxToolCallRounds)
	sess.Messages = append(sess.Messages, types.ChatMessage{Role: "assistant", Content: answer})
	return answer, nil
}

func messagesForAPI(messages []types.ChatMessage, visibleSkills map[string]bool) []types.ChatMessage {
	out := make([]types.ChatMessage, len(messages))
	readSkillCalls := map[string]string{}
	for i, msg := range messages {
		out[i] = msg
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if call.Function.Name != "read_skill" {
					continue
				}
				name := skillNameFromArgs(call.Function.Arguments)
				if name != "" {
					readSkillCalls[call.ID] = name
				}
			}
		}
		if msg.Role == "tool" {
			if name := readSkillCalls[msg.ToolCallID]; name != "" && !visibleSkills[name] {
				out[i].Content = fmt.Sprintf("skill %q is no longer visible in the active session; previous read_skill content is hidden", name)
			}
		}
		if msg.Role != "assistant" || msg.ReasoningContent == "" {
			continue
		}
		keepReasoning := len(msg.ToolCalls) > 0
		if !keepReasoning && i > 0 && messages[i-1].Role == "tool" {
			keepReasoning = true
		}
		if !keepReasoning {
			out[i].ReasoningContent = ""
		}
	}
	return out
}

func skillNameFromArgs(raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	name, _ := args["name"].(string)
	return name
}

func (a *Agent) EnsureSystemPrompt(sess *session.Session) {
	if len(sess.Messages) == 0 {
		sess.Messages = append(sess.Messages, types.ChatMessage{
			Role:    "system",
			Content: a.systemPrompt(sess),
		})
	}
}

func (a *Agent) RefreshSystemPrompt(sess *session.Session) {
	next := a.systemPrompt(sess)
	if len(sess.Messages) == 0 {
		sess.Messages = append(sess.Messages, types.ChatMessage{
			Role:    "system",
			Content: next,
		})
		return
	}
	if sess.Messages[0].Role == "system" {
		sess.Messages[0].Content = next
		return
	}
	sess.Messages = append([]types.ChatMessage{{Role: "system", Content: next}}, sess.Messages...)
}

func (a *Agent) systemPrompt(sess *session.Session) string {
	prompt := a.root.SystemPrompt + toolUsePrompt()
	skills, err := config.ListSkills(a.paths)
	if err != nil || len(skills) == 0 {
		return prompt
	}
	visible := a.visibleSkillSet(sess)
	blocks := []string{}
	for _, skill := range skills {
		if !visible[skill.Name] {
			continue
		}
		meta := []string{}
		for k, v := range skill.Metadata {
			if k == "name" || k == "description" || strings.TrimSpace(v) == "" {
				continue
			}
			meta = append(meta, fmt.Sprintf("%s=%q", k, v))
		}
		sort.Strings(meta)
		description := skill.Description
		if description == "" {
			description = "No description."
		}
		blocks = append(blocks, fmt.Sprintf("<skill name=%q source=%q description=%q metadata=%q />", skill.Name, skill.Source, description, strings.Join(meta, " ")))
	}
	if len(blocks) == 0 {
		return prompt + "\n\nNo skills visible."
	}
	return prompt + "\n\nVisible skills (use read_skill before applying):\n" + strings.Join(blocks, "\n")
}

func toolUsePrompt() string {
	return `

Tool rules:
- File tools (read_file, view_dir, search_grep, diff_file) use workplace-relative paths only. Prefer small, reviewable diffs.
- .Asayn/ is Asayn's required runtime/config directory, not project source. Do not read, search, edit, or summarize .Asayn/ unless the user explicitly asks to change Asayn configuration.
- Shell tools are high-privilege and vary by root-agent shell_config. shell_run_sync is synchronous, returns command output only, and kills the command on timeout; it never supports interactive input. If shell_run_async/shell_async_status/shell_async_kill are available, shell_run_async starts commands in parallel and returns shell_id. If shell_async_write is also available, shell_run_async starts an interactive command and shell_async_write sends input. Before non-trivial shell use, verify the execution environment (cwd, PATH, available binaries, Python interpreter). Prefer file tools when sufficient.
- Sub-agents (sub_agent_list, sub_agent_start_async, sub_agent_check, sub_agent_wait_check, sub_agent_resume_async) run in parallel with read_file, view_dir, search_grep, read_skill, and diff_file. Delegate only simple, time-consuming, file-scoped tasks. Do NOT delegate tasks that need shell or sub-agent coordination. Prefer continuing useful work and use sub_agent_check when a sub-agent is ready_for_check. Use sub_agent_wait_check only for a single deliberate wait when the user asked to wait or there is truly no useful work to do; do not poll with it.`
}

func (a *Agent) runToolCall(sess *session.Session, call types.ToolCall, emit func(AgentEvent)) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("tool argument JSON error: %v", err)
	}
	if call.Function.Name == "read_skill" {
		args["_visible_skills"] = a.visibleSkillNames(sess)
	}
	if emit != nil {
		emit(AgentEvent{
			Kind: "tool_start",
			Text: fmt.Sprintf("%s(%s)", call.Function.Name, call.Function.Arguments),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolCallTimeout(call.Function.Name, args))
	defer cancel()
	out, err := a.tools.Run(ctx, sess, call.Function.Name, args)
	if err != nil {
		out = fmt.Sprintf("tool error: %v", err)
		if emit != nil {
			emit(AgentEvent{Kind: "tool_error", Text: out})
		}
		return out
	}
	if emit != nil {
		emit(AgentEvent{Kind: "tool_result", Text: out})
	}
	return out
}

func (a *Agent) visibleSkillNames(sess *session.Session) []string {
	visible := a.visibleSkillSet(sess)
	out := []string{}
	for name, enabled := range visible {
		if enabled {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (a *Agent) visibleSkillSet(sess *session.Session) map[string]bool {
	visible := map[string]bool{}
	for _, name := range a.root.VisibleSkills {
		visible[name] = true
	}
	return visible
}

func toolCallTimeout(name string, args map[string]any) time.Duration {
	seconds := 60
	if name == "shell_run_sync" || name == "sub_agent_wait_check" {
		argName := "timeout_sec"
		if name == "sub_agent_wait_check" {
			argName = "wait_seconds"
		}
		if raw, ok := args[argName]; ok {
			switch value := raw.(type) {
			case float64:
				seconds = int(value)
			case int:
				seconds = value
			case string:
				var parsed int
				if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
					seconds = parsed
				}
			}
		}
		if seconds < 1 {
			seconds = 60
		}
		return time.Duration(seconds+5) * time.Second
	}
	return time.Duration(seconds) * time.Second
}
