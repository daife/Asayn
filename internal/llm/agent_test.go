package llm

import (
	"testing"

	"github.com/asayn/asayn/internal/llm/types"
)

func TestPrepareMessagesForAPIStripsReasoningForPreviousNoToolTurn(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first"},
		{Role: "assistant", ReasoningContent: "hidden reasoning", Content: "answer"},
		{Role: "user", Content: "second"},
	}

	got := prepareMessagesForAPI(messages, true, false)

	if got[2].ReasoningContent != "" {
		t.Fatalf("expected no-tool assistant reasoning to be stripped, got %q", got[2].ReasoningContent)
	}
	if got[2].Content != "answer" {
		t.Fatalf("expected assistant content to be preserved, got %q", got[2].Content)
	}
}

func TestPrepareMessagesForAPIKeepsReasoningForToolTurnWhenThinkingEnabled(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first"},
		{
			Role:             "assistant",
			ReasoningContent: "needed reasoning",
			ToolCalls: []types.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: types.ToolFunction{
					Name:      "search",
					Arguments: "{}",
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "second"},
	}

	got := prepareMessagesForAPI(messages, true, false)

	if got[2].ReasoningContent != "needed reasoning" {
		t.Fatalf("expected tool-turn reasoning to be preserved, got %q", got[2].ReasoningContent)
	}
}

func TestPrepareMessagesForAPIStripsAllReasoningWhenThinkingDisabled(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "user", Content: "first"},
		{
			Role:             "assistant",
			ReasoningContent: "hidden reasoning",
			ToolCalls: []types.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: types.ToolFunction{Name: "search", Arguments: "{}"},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
		{Role: "user", Content: "second"},
	}

	got := prepareMessagesForAPI(messages, false, false)

	if got[1].ReasoningContent != "" {
		t.Fatalf("expected reasoning to be stripped when thinking disabled, got %q", got[1].ReasoningContent)
	}
}

func TestPrepareMessagesForAPIRealTimeContextControlHidesOldToolResults(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "call_1", Type: "function", Function: types.ToolFunction{Name: "search", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "turn 1 result"},
		{Role: "assistant", Content: "done 1"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "call_2", Type: "function", Function: types.ToolFunction{Name: "search", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_2", Content: "turn 2 result"},
		{Role: "assistant", Content: "done 2"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "call_3", Type: "function", Function: types.ToolFunction{Name: "search", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_3", Content: "turn 3 result"},
		{Role: "assistant", Content: "done 3"},
		{Role: "user", Content: "turn 4"},
	}

	got := prepareMessagesForAPI(messages, true, true)

	if got[2].Content != "A long time has passed; hidden." {
		t.Fatalf("expected turn 1 tool result to be hidden, got %q", got[2].Content)
	}
	if got[6].Content != "turn 2 result" {
		t.Fatalf("expected turn 2 tool result to remain, got %q", got[6].Content)
	}
	if got[10].Content != "turn 3 result" {
		t.Fatalf("expected turn 3 tool result to remain, got %q", got[10].Content)
	}
}

func TestPrepareMessagesForAPIRealTimeContextControlDisabledKeepsOldToolResults(t *testing.T) {
	messages := []types.ChatMessage{
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "call_1", Type: "function", Function: types.ToolFunction{Name: "search", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "turn 1 result"},
		{Role: "assistant", Content: "done 1"},
		{Role: "user", Content: "turn 2"},
		{Role: "user", Content: "turn 3"},
		{Role: "user", Content: "turn 4"},
	}

	got := prepareMessagesForAPI(messages, true, false)

	if got[2].Content != "turn 1 result" {
		t.Fatalf("expected old tool result to remain when disabled, got %q", got[2].Content)
	}
}
