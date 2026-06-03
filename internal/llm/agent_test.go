package llm

import (
	"testing"

	"github.com/asayn/asayn/internal/llm/types"
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
