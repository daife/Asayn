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
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

func TestAgentCacheRoutingAcrossSessionSwitchAndRestore(t *testing.T) {
	var expected *session.Session
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Header.Get("session-id") != expected.CacheSessionID || body["prompt_cache_key"] != expected.PromptCacheKey {
			t.Errorf("wrong routing: header=%q key=%v, session=%+v", r.Header.Get("session-id"), body["prompt_cache_key"], expected)
		}
		if body["store"] != false || body["stream"] != true {
			t.Error("Responses persistence/stream settings changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":80}}}}`+"\n\n")
	}))
	defer server.Close()
	root := t.TempDir()
	paths := config.Paths{WorkspaceRoot: root, WorkspaceDir: filepath.Join(root, ".Asayn"), HomeDir: filepath.Join(root, "home")}
	store := session.NewStore(filepath.Join(root, "sessions"))
	a, err := store.New("a", "default")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.New("b", "default")
	if err != nil {
		t.Fatal(err)
	}
	executor := tools.NewBasicExecutor(paths, store, 2000, false)
	defer executor.Shutdown()
	agent := NewAgent(config.APIConfig{Providers: map[string]config.ProviderConfig{"test": {APIType: "responses", BaseURL: server.URL, PromptCacheKey: "old-shared-key"}}}, config.AgentConfig{Provider: "test", Model: "test"}, paths, executor)
	for _, sess := range []*session.Session{a, a, b, a} {
		expected = sess
		_, use, err := agent.Ask(context.Background(), sess, "hello")
		if err != nil {
			t.Fatal(err)
		}
		if use.PromptCacheHitTokens != 80 {
			t.Fatalf("lost cached usage: %+v", use)
		}
		if err := store.Save(sess); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := store.LoadByID(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected = restored
	if _, _, err := agent.Ask(context.Background(), restored, "continue"); err != nil {
		t.Fatal(err)
	}
	if agent.client.cfg.PromptCacheKey != "old-shared-key" || agent.client.cacheSessionID != "" {
		t.Fatal("shared client was mutated")
	}
}
