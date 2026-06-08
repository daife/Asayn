package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

const staleToolResultsHiddenMessage = "Previous tool results are hidden as they may be outdated."

type Agent struct {
	client     *Client
	root       config.AgentConfig
	paths      config.Paths
	tools      *tools.Executor
	isSubAgent bool
}

type AgentEvent struct {
	Kind  string
	Text  string
	Usage *types.Usage
}

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
	case "assistant_delta":
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
	prov, ok := api.Providers[root.Provider]
	if !ok {
		// fallback to DeepSeek if missing, or maybe SiliconFlow
		if p, exists := api.Providers["DeepSeek"]; exists {
			prov = p
		} else {
			// pick first
			for _, p := range api.Providers {
				prov = p
				break
			}
		}
	}
	return &Agent{
		client: NewClient(prov),
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

func (a *Agent) Ask(ctx context.Context, sess *session.Session, prompt string) (string, types.Usage, error) {
	return a.AskWithEvents(ctx, sess, prompt, nil)
}

func (a *Agent) AskWithEvents(ctx context.Context, sess *session.Session, prompt string, emit func(AgentEvent)) (string, types.Usage, error) {
	return a.askWithEvents(ctx, sess, prompt, emit, true)
}

func (a *Agent) RetryWithEvents(ctx context.Context, sess *session.Session, emit func(AgentEvent)) (string, types.Usage, error) {
	return a.askWithEvents(ctx, sess, "", emit, false)
}

func (a *Agent) askWithEvents(ctx context.Context, sess *session.Session, prompt string, emit func(AgentEvent), appendPrompt bool) (string, types.Usage, error) {
	a.EnsureSystemPrompt(sess)
	baseLen := len(sess.Messages)
	if appendPrompt {
		sess.Messages = append(sess.Messages, types.ChatMessage{Role: "user", Content: prompt})
	}

	var totalUsage types.Usage
	toolSchemas := a.tools.Schemas(a.isSubAgent)
	for {
		if emit != nil {
			emit(AgentEvent{Kind: "thinking_start"})
		}
		contentStreamed := false
		msg, usage, err := a.client.ChatStream(ctx, a.root.Model, messagesForAPI(sess, a.root.ThinkingEnabled, a.root.RealTimeContextControl), toolSchemas, a.root.ThinkingEnabled, a.root.ReasoningEffort, func(delta StreamDelta) {
			if emit == nil {
				return
			}
			if delta.ReasoningContent != "" {
				emit(AgentEvent{Kind: "thinking_delta", Text: delta.ReasoningContent})
			}
			if delta.Content != "" {
				contentStreamed = true
				emit(AgentEvent{Kind: "assistant_delta", Text: delta.Content})
			}
			switch delta.Event {
			case "retry":
				emit(AgentEvent{Kind: "retry", Text: formatRetryEvent(delta)})
			case "timeout":
				emit(AgentEvent{Kind: "timeout", Text: formatTimeoutEvent(delta)})
			}
		})
		if err != nil {
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.TotalTokens = usage.TotalTokens
			totalUsage.PromptCacheHitTokens += usage.PromptCacheHitTokens
			totalUsage.PromptCacheMissTokens += usage.PromptCacheMissTokens
			if streamErr := streamError(err); streamErr != nil && hasAssistantTextProgress(streamErr.Partial) {
				sess.Messages = append(sess.Messages, streamErr.Partial)
			} else if !isContextCanceled(err) && !isStreamTimeout(err) && len(sess.Messages) > baseLen {
				sess.Messages = sess.Messages[:baseLen]
			}
			return "", totalUsage, err
		}
		totalUsage.PromptTokens += usage.PromptTokens // This is total tokens consumed across all turns.
		totalUsage.CompletionTokens += usage.CompletionTokens
		totalUsage.TotalTokens = usage.TotalTokens // Represents the context window size of the latest call.
		totalUsage.PromptCacheHitTokens += usage.PromptCacheHitTokens
		totalUsage.PromptCacheMissTokens += usage.PromptCacheMissTokens
		sess.Messages = append(sess.Messages, msg)
		if emit != nil {
			snapshot := totalUsage
			emit(AgentEvent{Kind: "usage", Usage: &snapshot})
		}

		if emit != nil && msg.ReasoningContent != "" {
			emit(AgentEvent{Kind: "thinking", Text: msg.ReasoningContent})
		}
		if emit != nil && msg.Content != "" && len(msg.ToolCalls) > 0 && !contentStreamed {
			emit(AgentEvent{Kind: "assistant", Text: msg.Content})
		}
		if len(msg.ToolCalls) == 0 {
			return msg.Content, totalUsage, nil
		}
		for _, call := range msg.ToolCalls {
			result := a.runToolCall(ctx, sess, call, emit)
			sess.Messages = append(sess.Messages, types.ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
}

func formatRetryEvent(delta StreamDelta) string {
	if delta.RetryAttempt <= 0 || delta.MaxAttempts <= 0 {
		return ""
	}
	reason := strings.TrimSpace(delta.Message)
	if reason == "" {
		reason = "retrying"
	}
	if delta.Wait > 0 {
		return fmt.Sprintf("Retry for %d/%d after %s (%s)", delta.RetryAttempt, delta.MaxAttempts, delta.Wait.Truncate(time.Second), reason)
	}
	return fmt.Sprintf("Retry for %d/%d (%s)", delta.RetryAttempt, delta.MaxAttempts, reason)
}

func formatTimeoutEvent(delta StreamDelta) string {
	message := strings.TrimSpace(delta.Message)
	if message == "" {
		message = "timeout"
	}
	if delta.Timeout > 0 {
		return fmt.Sprintf("%s after %s", message, delta.Timeout.Truncate(time.Second))
	}
	return message
}

func isContextCanceled(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled"))
}

func isStreamTimeout(err error) bool {
	return streamError(err) != nil || strings.Contains(err.Error(), "idle timeout")
}

func streamError(err error) *StreamError {
	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		return streamErr
	}
	return nil
}

func hasAssistantTextProgress(msg types.ChatMessage) bool {
	return strings.TrimSpace(msg.Content) != "" || strings.TrimSpace(msg.ReasoningContent) != ""
}

func messagesForAPI(sess *session.Session, thinkingEnabled, realTimeContextControl bool) []types.ChatMessage {
	if sess == nil {
		return nil
	}
	return prepareMessagesForAPI(activeMessagesForAPI(sess), thinkingEnabled, realTimeContextControl)
}

func activeMessagesForAPI(sess *session.Session) []types.ChatMessage {
	if sess == nil {
		return nil
	}
	messages := sess.Messages
	if sess.CompactedBefore <= 0 || sess.CompactedBefore >= len(messages) {
		return messages
	}
	out := []types.ChatMessage{}
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
	}
	out = append(out, messages[sess.CompactedBefore:]...)
	return out
}

func prepareMessagesForAPI(messages []types.ChatMessage, thinkingEnabled, realTimeContextControl bool) []types.ChatMessage {
	out := make([]types.ChatMessage, len(messages))
	readSkillCalls := map[string]string{}
	latestUser := latestUserMessageIndex(messages)
	latestTurn := latestUserTurn(messages)
	currentTurn := 0
	staleToolResultIndexes := []int{}
	for i, msg := range messages {
		out[i] = msg
		if msg.Role == "user" {
			currentTurn++
		}
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if call.Function.Name != "skill_read" {
					continue
				}
				name := skillNameFromArgs(call.Function.Arguments)
				if name != "" {
					readSkillCalls[call.ID] = name
				}
			}
		}
		if msg.Role == "tool" {
			if name := readSkillCalls[msg.ToolCallID]; name != "" && i < latestUser {
				out[i].Content = fmt.Sprintf("Skill %q content from a previous skill_read call is hidden. Use the skill_read tool again if you need to view it.", name)
			}
			if realTimeContextControl && currentTurn > 0 && latestTurn-currentTurn >= 3 {
				staleToolResultIndexes = append(staleToolResultIndexes, i)
			}
		}
		if msg.Role == "assistant" && msg.ReasoningContent != "" && (!thinkingEnabled || len(msg.ToolCalls) == 0) {
			out[i].ReasoningContent = ""
		}
	}
	for i, idx := range staleToolResultIndexes {
		out[idx].Content = "hidden"
		if i == len(staleToolResultIndexes)-1 {
			out[idx].Content = staleToolResultsHiddenMessage
		}
	}
	return out
}

func latestUserMessageIndex(messages []types.ChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return len(messages)
}

func latestUserTurn(messages []types.ChatMessage) int {
	turn := 0
	for _, msg := range messages {
		if msg.Role == "user" {
			turn++
		}
	}
	return turn
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
	prompt := a.root.SystemPrompt
	skills, err := config.ListSkills(a.paths)
	if err != nil || len(skills) == 0 {
		return prompt + "\n\nWorkspace rules:\n- Avoid modifying .Asayn/ unless explicitly asked to change Asayn configurations.\n- Write or modify files via shell tools with Python heredocs, sed, cat, etc. Multiple tool calls per response are recommended."
	}
	visible := a.visibleSkillSet(sess)
	blocks := []string{}
	for _, skill := range skills {
		if !visible[skill.Name] {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("<skill folder=%q metadata=%q />", skill.Folder, formatSkillMetadata(skill.Metadata)))
	}
	if len(blocks) == 0 {
		return prompt + "\n\nNo skills visible.\n\nWorkspace rules:\n- Avoid modifying .Asayn/ unless explicitly asked to change Asayn configurations.\n- Write or modify files via shell tools with Python heredocs, sed, cat, etc. Multiple tool calls per response are recommended."
	}
	return prompt + "\n\nVisible skills (use skill_read before applying):\n" + strings.Join(blocks, "\n") + "\n\nWorkspace rules:\n- Avoid modifying .Asayn/ unless explicitly asked to change Asayn configurations.\n- Write or modify files via shell tools with Python heredocs, sed, cat, etc. Multiple tool calls per response are recommended."
}

func formatSkillMetadata(metadata map[string]string) string {
	items := []string{}
	for k, v := range metadata {
		if strings.TrimSpace(v) == "" {
			continue
		}
		items = append(items, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(items)
	return strings.Join(items, " ")
}

func (a *Agent) runToolCall(parent context.Context, sess *session.Session, call types.ToolCall, emit func(AgentEvent)) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("tool argument JSON error: %v", err)
	}
	if call.Function.Name == "skill_read" {
		args["_visible_skills"] = a.visibleSkillNames(sess)
	}
	if emit != nil {
		emit(AgentEvent{
			Kind: "tool_start",
			Text: fmt.Sprintf("%s(%s)", call.Function.Name, call.Function.Arguments),
		})
	}
	ctx, cancel := context.WithTimeout(parent, toolCallTimeout(call.Function.Name, args))
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
	if name == "shell_run_sync" || name == "delay" {
		argName := "timeout_sec"
		if name == "delay" {
			argName = "seconds"
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