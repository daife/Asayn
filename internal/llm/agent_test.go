package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

func TestMessagesForAPIHidesPreviousTurnReadSkillContent(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "read_skill",
				Arguments: `{"name":"hidden-skill"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "secret skill body"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next"},
	}

	out := prepareMessagesForAPI(messages, true)
	if out[3].Content == "secret skill body" {
		t.Fatal("previous-turn skill content was still sent to API")
	}
	if !strings.Contains(out[3].Content, "Use the read_skill tool again") {
		t.Fatalf("hidden skill content should be replaced with an explanatory placeholder, got %q", out[3].Content)
	}
}

func TestMessagesForAPIKeepsCurrentTurnReadSkillContent(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "user", Content: "current"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "read_skill",
				Arguments: `{"name":"visible-skill"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "visible skill body"},
	}

	out := prepareMessagesForAPI(messages, true)
	if out[2].Content != "visible skill body" {
		t.Fatalf("current-turn skill content changed: %q", out[2].Content)
	}
}

func TestMessagesForAPIDropsReasoningWhenThinkingDisabled(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "assistant", ReasoningContent: "old thinking", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "file_read",
				Arguments: `{"relative_path":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	}

	out := prepareMessagesForAPI(messages, false)
	if out[0].ReasoningContent != "" {
		t.Fatalf("disabled thinking should not send reasoning_content, got %q", out[0].ReasoningContent)
	}
}

func TestMessagesForAPIKeepsToolReasoningWhenThinkingEnabled(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "assistant", ReasoningContent: "tool thinking", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "file_read",
				Arguments: `{"relative_path":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	}

	out := prepareMessagesForAPI(messages, true)
	if out[0].ReasoningContent != "tool thinking" {
		t.Fatalf("enabled thinking should keep tool reasoning, got %q", out[0].ReasoningContent)
	}
}

func TestMessagesForAPIUsesCompactedBoundary(t *testing.T) {
	sess := &session.Session{
		Messages: []types.ChatMessage{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "old request"},
			{Role: "assistant", Content: "old answer"},
			{Role: "user", Content: "Recall what we worked on before."},
			{Role: "assistant", Content: "compressed summary"},
		},
		CompactedBefore: 3,
	}

	out := messagesForAPI(sess, true)
	if len(out) != 3 {
		t.Fatalf("expected system plus compact round, got %d messages", len(out))
	}
	if out[1].Content != "Recall what we worked on before." || out[2].Content != "compressed summary" {
		t.Fatalf("unexpected compacted messages: %#v", out)
	}
	for _, msg := range out {
		if strings.Contains(msg.Content, "old") {
			t.Fatalf("old pre-compaction content leaked into API messages: %#v", out)
		}
	}
}

func TestMessagesForAPIRepeatedCompressionDoesNotExposeOlderHistory(t *testing.T) {
	sess := &session.Session{
		Messages: []types.ChatMessage{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "original request"},
			{Role: "assistant", Content: "original work"},
			{Role: "user", Content: "Recall what we worked on before."},
			{Role: "assistant", Content: "first compact summary"},
			{Role: "user", Content: "new request after compact"},
			{Role: "assistant", Content: "new work after compact"},
		},
		CompactedBefore: 3,
	}

	out := messagesForAPI(sess, true)
	got := []string{}
	for _, msg := range out {
		got = append(got, msg.Content)
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "original request") || strings.Contains(joined, "original work") {
		t.Fatalf("pre-first-compact history leaked into repeated compact context: %#v", got)
	}
	for _, want := range []string{"system", "first compact summary", "new request after compact", "new work after compact"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("repeated compact context missing %q: %#v", want, got)
		}
	}
}

func TestSystemPromptIncludesConcreteWorkplaceRules(t *testing.T) {
	agent := NewAgent(config.APIConfig{}, config.AgentConfig{
		Name:         "default",
		SystemPrompt: "base prompt",
	}, config.Paths{Workplace: "/tmp/asayn-workplace"}, nil)
	prompt := agent.systemPrompt(&session.Session{})
	for _, want := range []string{
		`Workplace: "/tmp/asayn-workplace"`,
		"delete_lines",
		"search_grep-style regex",
		"view_history",
		"Terminal environment is " + tools.ShellEnvironmentName(),
		"Commands run in workplace root",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptDoesNotGuideShellFileOperations(t *testing.T) {
	agent := NewAgent(config.APIConfig{}, config.AgentConfig{
		Name:         "default",
		SystemPrompt: "base prompt",
	}, config.Paths{Workplace: "/tmp/asayn-workplace"}, nil)
	prompt := strings.ToLower(agent.systemPrompt(&session.Session{}))
	for _, unwanted := range []string{
		"use shell",
		"using shell",
		"shell to read",
		"shell to edit",
		"shell for file",
		"do not use shell",
		"don't use shell",
		"avoid shell",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("system prompt should not guide shell file operations with %q:\n%s", unwanted, prompt)
		}
	}
}

func TestChatRequestUsesPerAgentThinkingConfig(t *testing.T) {
	disabled := buildChatRequest("model", []types.ChatMessage{{Role: "user", Content: "hi"}}, nil, false, "max", false)
	if disabled.Thinking["type"] != "disabled" {
		t.Fatalf("expected disabled thinking, got %#v", disabled.Thinking)
	}
	if disabled.ReasoningEffort != "" {
		t.Fatalf("disabled thinking should omit reasoning_effort, got %q", disabled.ReasoningEffort)
	}

	enabled := buildChatRequest("model", []types.ChatMessage{{Role: "user", Content: "hi"}}, nil, true, "max", true)
	if enabled.Thinking["type"] != "enabled" || enabled.ReasoningEffort != "max" || !enabled.Stream {
		t.Fatalf("expected enabled/max streaming thinking, got thinking=%#v effort=%q stream=%t", enabled.Thinking, enabled.ReasoningEffort, enabled.Stream)
	}
}

func TestFormatRetryAndTimeoutEvents(t *testing.T) {
	retry := formatRetryEvent(StreamDelta{
		RetryAttempt: 2,
		MaxAttempts:  10,
		Wait:         3 * time.Second,
		Message:      "API rate limit",
	})
	if retry != "Retry for 2/10 after 3s (API rate limit)" {
		t.Fatalf("unexpected retry event text: %q", retry)
	}

	timeout := formatTimeoutEvent(StreamDelta{
		Timeout: 120 * time.Second,
		Message: "API stream idle timeout",
	})
	if timeout != "API stream idle timeout after 2m0s" {
		t.Fatalf("unexpected timeout event text: %q", timeout)
	}
}

func TestChatStreamResetsProviderTimeoutOnKeepAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"role":"assistant","content":"first"}}]}`)
		flusher.Flush()
		time.Sleep(600 * time.Millisecond)
		fmt.Fprintln(w, ": keep-alive")
		flusher.Flush()
		time.Sleep(600 * time.Millisecond)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":" second"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()

	client := NewClient(config.ProviderConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})

	msg, usage, err := client.ChatStream(context.Background(), "model", []types.ChatMessage{{Role: "user", Content: "hi"}}, nil, false, "", nil)
	if err != nil {
		t.Fatalf("stream should not fail when keep-alive arrives before provider timeout: %v", err)
	}
	if msg.Content != "first second" {
		t.Fatalf("unexpected streamed content: %q", msg.Content)
	}
	if usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestChatStreamFailsAfterProviderIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"role":"assistant","content":"first"}}]}`)
		flusher.Flush()
		time.Sleep(1100 * time.Millisecond)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":" second"}}]}`)
	}))
	defer server.Close()

	client := NewClient(config.ProviderConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})

	msg, _, err := client.ChatStream(context.Background(), "model", []types.ChatMessage{{Role: "user", Content: "hi"}}, nil, false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("expected stream idle timeout, got %v", err)
	}
	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("expected StreamError, got %T", err)
	}
	if msg.Content != "first" || streamErr.Partial.Content != "first" {
		t.Fatalf("timeout should return partial assistant content, got msg=%q partial=%q", msg.Content, streamErr.Partial.Content)
	}
}

func TestAgentPreservesSessionProgressAfterProviderIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"role":"assistant","content":"partial answer"}}]}`)
		flusher.Flush()
		time.Sleep(1100 * time.Millisecond)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":" after timeout"}}]}`)
	}))
	defer server.Close()

	work := t.TempDir()
	store := session.NewStore(t.TempDir())
	exec := tools.NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	agent := NewAgent(config.APIConfig{
		Providers: map[string]config.ProviderConfig{
			"test": {BaseURL: server.URL, TimeoutSeconds: 1},
		},
	}, config.AgentConfig{
		Name:     "default",
		Provider: "test",
		Model:    "model",
	}, config.Paths{Workplace: work}, exec)
	sess := &session.Session{}

	_, _, err := agent.AskWithEvents(context.Background(), sess, "keep this request", nil)
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("expected idle timeout, got %v", err)
	}
	if len(sess.Messages) != 3 {
		t.Fatalf("expected system, user, and partial assistant messages, got %#v", sess.Messages)
	}
	if sess.Messages[1].Role != "user" || sess.Messages[1].Content != "keep this request" {
		t.Fatalf("timeout should preserve user request, got %#v", sess.Messages[1])
	}
	if sess.Messages[2].Role != "assistant" || sess.Messages[2].Content != "partial answer" {
		t.Fatalf("timeout should preserve partial assistant, got %#v", sess.Messages[2])
	}
}

func TestChatRequestKeepsEmptyContentField(t *testing.T) {
	req := buildChatRequest("model", []types.ChatMessage{{
		Role: "assistant",
		ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "view_dir",
				Arguments: `{"relative_path":"."}`,
			},
		}},
	}}, nil, false, "", false)
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":""`) {
		t.Fatalf("assistant tool-call messages must keep empty content field, got %s", data)
	}
}
