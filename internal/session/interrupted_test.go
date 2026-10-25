package session

import (
	"github.com/asayn/asayn/internal/llm/types"
	"path/filepath"
	"testing"
)

func TestRestoreIncompleteToolBatch(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = []types.ChatMessage{
		{Role: "user", Content: "work"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "done"}, {ID: "interrupted"}}},
		{Role: "tool", ToolCallID: "done", Content: "real result"},
		{Role: "user", Content: "continue"},
	}
	sess.CompactedBefore = 3
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	restored, err := store.LoadByID(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Messages) != 5 || restored.Messages[2].Content != "real result" || restored.Messages[3].ToolCallID != "interrupted" || restored.Messages[3].Content != ToolInterruptedUnknown || restored.Messages[4].Role != "user" {
		t.Fatalf("invalid repaired history: %+v", restored.Messages)
	}
	if restored.CompactedBefore != 4 {
		t.Fatalf("compaction boundary shifted incorrectly: %d", restored.CompactedBefore)
	}
	if restored.RepairInterruptedTools() {
		t.Fatal("repair is not idempotent")
	}
	if err := store.Save(restored); err != nil {
		t.Fatal(err)
	}
	again, err := store.LoadByID(sess.ID)
	if err != nil || len(again.Messages) != 5 {
		t.Fatalf("round trip: %v %+v", err, again)
	}
}

func TestRepairFinalIncompleteBatch(t *testing.T) {
	sess := &Session{Messages: []types.ChatMessage{{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "a"}, {ID: "b"}}}}}
	if !sess.RepairInterruptedTools() || len(sess.Messages) != 3 {
		t.Fatalf("missing tool outputs: %+v", sess.Messages)
	}
	if sess.Messages[1].ToolCallID != "a" || sess.Messages[2].ToolCallID != "b" {
		t.Fatal("call ordering lost")
	}
}
