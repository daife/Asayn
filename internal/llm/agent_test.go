package llm

import (
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

	out := messagesForAPI(messages, map[string]bool{"hidden-skill": false})
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

	out := messagesForAPI(messages, map[string]bool{"visible-skill": true})
	if out[1].Content != "visible skill body" {
		t.Fatalf("visible skill content changed: %q", out[1].Content)
	}
}

func TestSystemPromptIncludesConcreteWorkplaceRules(t *testing.T) {
	agent := NewAgent(config.APIConfig{}, config.AgentConfig{
		Name:         "default",
		SystemPrompt: "base prompt",
	}, config.Paths{Workplace: "/tmp/asayn-workplace"}, nil)
	prompt := agent.systemPrompt(&session.Session{})
	for _, want := range []string{
		`Current workplace: "/tmp/asayn-workplace"`,
		"Do not invent or assume paths such as /root/workplace",
		"Shell commands run with cwd set to the current workplace above",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}
