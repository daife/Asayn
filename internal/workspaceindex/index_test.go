package workspaceindex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/asayn/asayn/internal/session"
)

func TestRegisterAndListWorkspaceSessions(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	store := session.NewStore(filepath.Join(workspace, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("indexed session", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(home, workspace, sess.ID); err != nil {
		t.Fatal(err)
	}

	items, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Sessions) != 1 {
		t.Fatalf("got %#v, want one workspace with one session", items)
	}
	if items[0].Sessions[0].ID != sess.ID || items[0].LastSessionID != sess.ID {
		t.Fatalf("indexed session mismatch: %#v", items[0])
	}
	if _, err := os.Stat(filepath.Join(home, filename)); err != nil {
		t.Fatalf("index file was not written: %v", err)
	}
}

func TestRegisterDeduplicatesWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	if err := Register(home, workspace, "first"); err != nil {
		t.Fatal(err)
	}
	if err := Register(home, workspace, "second"); err != nil {
		t.Fatal(err)
	}

	items, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].LastSessionID != "second" {
		t.Fatalf("got %#v, want one updated workspace", items)
	}
}
