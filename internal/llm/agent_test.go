package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
)

func TestMessagesForAPIHidesNoLongerVisibleReadSkillContent(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "read_skill",
				Arguments: `{"name":"hidden-skill"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "secret skill body"},
	}

	out := messagesForAPI(messages, map[string]bool{"hidden-skill": false}, true)
	if out[2].Content == "secret skill body" {
		t.Fatal("hidden skill content was still sent to API")
	}
	if out[2].Content == "" {
		t.Fatal("hidden skill content should be replaced with an explanatory placeholder")
	}
}

func TestMessagesForAPIKeepsVisibleReadSkillContent(t *testing.T) {
	messages := []types.ChatMessage{
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

	out := messagesForAPI(messages, map[string]bool{"visible-skill": true}, true)
	if out[1].Content != "visible skill body" {
		t.Fatalf("visible skill content changed: %q", out[1].Content)
	}
}

func TestMessagesForAPIDropsReasoningWhenThinkingDisabled(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "assistant", ReasoningContent: "old thinking", ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	}

	out := messagesForAPI(messages, nil, false)
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
				Name:      "read_file",
				Arguments: `{"path":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	}

	out := messagesForAPI(messages, nil, true)
	if out[0].ReasoningContent != "tool thinking" {
		t.Fatalf("enabled thinking should keep tool reasoning, got %q", out[0].ReasoningContent)
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
		"Use mode=write only for new/small files",
		"Run in workplace root",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
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

func TestChatRequestKeepsEmptyContentField(t *testing.T) {
	req := buildChatRequest("model", []types.ChatMessage{{
		Role: "assistant",
		ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: types.ToolFunction{
				Name:      "view_dir",
				Arguments: `{"path":"."}`,
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
