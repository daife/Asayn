package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

func TestShutdownWaitsForCancellationAndPersistsProgress(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(filepath.Join(root, "sessions"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{WorkspaceRoot: root, WorkspaceDir: filepath.Join(root, ".Asayn"), HomeDir: filepath.Join(root, "home")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	b := &bridge{ctx: &app.Context{Sessions: store, Tools: tools.NewBasicExecutor(paths, store, 2000, false)}, sess: sess, cancel: cancel, running: true, done: done}
	stopped := make(chan error, 1)
	go func() { stopped <- b.shutdown() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel agent")
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned before agent finished")
	default:
	}
	sess.Messages = []types.ChatMessage{{Role: "user", Content: "work"}, {Role: "assistant", Content: "saved before shutdown"}}
	close(done)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
	restored, err := store.LoadByID(sess.ID)
	if err != nil || len(restored.Messages) != 2 {
		t.Fatalf("missing progress: %v", err)
	}
}
