package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewSessionSerializesCollectionsAsEmpty(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(store.dir, sess.ID, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"messages", "sub_agents", "input_history"} {
		if string(raw[field]) != "[]" {
			t.Fatalf("%s = %s, want []", field, raw[field])
		}
	}
	if string(raw["visible_skills"]) != "{}" {
		t.Fatalf("visible_skills = %s, want {}", raw["visible_skills"])
	}
}

func TestLoadNormalizesLegacyNullCollections(t *testing.T) {
	store := NewStore(t.TempDir())
	id := "legacy-session"
	dir := filepath.Join(store.dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"legacy-session","name":"legacy","root_agent":"default","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","messages":null,"visible_skills":null,"sub_agents":null,"input_history":null}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := store.LoadByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.CacheSessionID != id || sess.PromptCacheKey != id {
		t.Fatal("legacy cache identity was not derived from session ID")
	}
	if sess.Messages == nil || sess.VisibleSkills == nil || sess.SubAgents == nil || sess.InputHistory == nil {
		t.Fatal("legacy null collections were not normalized")
	}
}

func TestCacheIdentitySurvivesRestoreAndForkIsIndependent(t *testing.T) {
	store := NewStore(t.TempDir())
	original, err := store.New("first", "default")
	if err != nil {
		t.Fatal(err)
	}
	if original.CacheSessionID == "" || original.PromptCacheKey == "" {
		t.Fatal("missing cache identity")
	}
	restored, err := store.LoadByID(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.CacheSessionID != original.CacheSessionID || restored.PromptCacheKey != original.PromptCacheKey {
		t.Fatal("restore changed cache identity")
	}
	fork, err := store.Fork(restored, "fork")
	if err != nil {
		t.Fatal(err)
	}
	if fork.CacheSessionID == original.CacheSessionID || fork.PromptCacheKey == original.PromptCacheKey {
		t.Fatal("persistent fork shares routing identity")
	}
	restored.CacheSessionID = "inherited-affinity"
	restored.PromptCacheKey = "inherited-key"
	if err := store.Save(restored); err != nil {
		t.Fatal(err)
	}
	restored, err = store.LoadByID(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.CacheSessionID != "inherited-affinity" || restored.PromptCacheKey != "inherited-key" {
		t.Fatal("explicit affinity was overwritten")
	}
}
