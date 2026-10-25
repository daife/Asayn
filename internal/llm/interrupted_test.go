package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

func TestCancelToolBatchIsDurableAndDoesNotStartRemainingTools(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"first","type":"function","function":{"name":"missing_tool","arguments":"{}"}},{"index":1,"id":"second","type":"function","function":{"name":"missing_tool","arguments":"{}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	root := t.TempDir()
	paths := config.Paths{WorkspaceRoot: root, WorkspaceDir: filepath.Join(root, ".Asayn"), HomeDir: filepath.Join(root, "home")}
	store := session.NewStore(filepath.Join(root, "sessions"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	agent := NewAgent(config.APIConfig{Providers: map[string]config.ProviderConfig{"test": {BaseURL: server.URL}}}, config.AgentConfig{Provider: "test", Model: "test"}, paths, tools.NewBasicExecutor(paths, store, 2000, false))
	agent.SetSessionStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := 0
	_, _, err = agent.AskWithEvents(ctx, sess, "start", func(e AgentEvent) {
		if e.Kind == "tool_start" {
			started++
			checkpoint, loadErr := store.LoadByID(sess.ID)
			if loadErr != nil || checkpoint.Messages[len(checkpoint.Messages)-1].ToolCallID != "second" {
				t.Errorf("tool batch not checkpointed before execution: %v", loadErr)
			}
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || requests != 1 || started != 1 {
		t.Fatalf("err=%v requests=%d started=%d", err, requests, started)
	}
	restored, err := store.LoadByID(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := len(restored.Messages)
	if restored.Messages[n-2].ToolCallID != "first" || restored.Messages[n-1].ToolCallID != "second" || restored.Messages[n-1].Content != session.ToolInterruptedBeforeStart {
		t.Fatalf("invalid canceled batch: %+v", restored.Messages)
	}
	if restored.RepairInterruptedTools() {
		t.Fatal("persisted canceled batch was incomplete")
	}
}

func TestRecoveredSubscriptionRequestHasEveryToolOutput(t *testing.T) {
	sess := &session.Session{Messages: []types.ChatMessage{
		{Role: "user", Content: "work"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "completed", Function: types.ToolFunction{Name: "shell", Arguments: "{}"}}, {ID: "lost", Function: types.ToolFunction{Name: "shell", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "completed", Content: "ok"},
	}}
	sess.RepairInterruptedTools()
	sess.Messages = append(sess.Messages, types.ChatMessage{Role: "user", Content: ContinuePrompt})
	c := NewClient(config.ProviderConfig{APIType: "subscription_responses"})
	body := c.buildRequest("test", sess.Messages, nil, false, "", true).(map[string]any)
	pending := map[string]bool{}
	for _, raw := range body["input"].([]any) {
		item := raw.(map[string]any)
		if item["type"] == "function_call" {
			pending[item["call_id"].(string)] = true
		}
		if item["type"] == "function_call_output" {
			delete(pending, item["call_id"].(string))
		}
		if item["role"] == "user" && item["content"] == ContinuePrompt && len(pending) > 0 {
			t.Fatal("continuation was sent before all outputs")
		}
	}
	if len(pending) != 0 {
		t.Fatalf("unmatched calls: %v", pending)
	}
}
