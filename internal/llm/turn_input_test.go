package llm

import (
	"context"
	"encoding/json"
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

func TestSteeringAfterEntireToolBatch(t *testing.T) {
	input := &TurnInput{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []types.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"first","type":"function","function":{"name":"missing_tool","arguments":"{}"}},{"index":1,"id":"second","type":"function","function":{"name":"missing_tool","arguments":"{}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			n := len(body.Messages)
			if n < 3 || body.Messages[n-3].ToolCallID != "first" || body.Messages[n-2].ToolCallID != "second" || body.Messages[n-1].Role != "user" || body.Messages[n-1].Content != "change direction" {
				t.Errorf("steering split the tool batch: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"content":"done"}}]}`+"\n\ndata: [DONE]\n\n")
		}
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
	inserted := 0
	_, _, err = agent.AskWithEvents(WithTurnInput(context.Background(), input), sess, "start", func(e AgentEvent) {
		if e.Kind == "tool_start" && e.ToolCallID == "first" {
			if !input.Submit("change direction") {
				t.Error("steering rejected")
			}
		}
		if e.Kind == "user_inserted" {
			inserted++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || inserted != 1 {
		t.Fatalf("requests=%d inserted=%d", requests, inserted)
	}
	if input.Submit("too late") {
		t.Fatal("completed input accepted a message")
	}
}

func TestTurnInputFinishDoesNotLosePendingMessages(t *testing.T) {
	input := &TurnInput{}
	input.Submit("one")
	input.Submit("two")
	got := input.drain(true)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected pending messages: %v", got)
	}
	if !input.Submit("three") {
		t.Fatal("input closed before inserted messages were processed")
	}
	input.drain(false)
	input.drain(true)
	if input.Submit("four") {
		t.Fatal("closed input accepted message")
	}
}

func TestSteeringAfterResponseWithoutTools(t *testing.T) {
	input := &TurnInput{}
	input.Submit("follow up")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []types.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if requests == 2 && body.Messages[len(body.Messages)-1].Content != "follow up" {
			t.Error("follow-up missing from continuation")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"content":"reply"}}]}`+"\n\ndata: [DONE]\n\n")
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
	_, _, err = agent.AskWithEvents(WithTurnInput(context.Background(), input), sess, "start", nil)
	if err != nil || requests != 2 {
		t.Fatalf("requests=%d err=%v", requests, err)
	}
}
