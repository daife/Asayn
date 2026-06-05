package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
)

func TestFileEditWriteCreate(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "write",
		"relative_path": "new.txt",
		"content":       "hello\nworld\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "change_id=") || !strings.Contains(out, "+hello") {
		t.Fatalf("expected creation diff, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "new.txt"))
	if string(data) != "hello\nworld\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestFileEditDeleteLines(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("a\nb\nc\nd\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "delete_lines",
		"relative_path": "f.txt",
		"start_line":    2,
		"end_line":      3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "change_id=") || !strings.Contains(out, "-b") {
		t.Fatalf("expected delete diff, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "f.txt"))
	if string(data) != "a\nd\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestFileEditInsert(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("a\nc\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":              "insert",
		"relative_path":     "f.txt",
		"insert_after_line": 1,
		"text":              "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+b") {
		t.Fatalf("expected insert diff with +b, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "f.txt"))
	if string(data) != "a\nb\nc\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestFileEditReplaceLines(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("a\nb\nc\nd\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "replace_lines",
		"relative_path": "f.txt",
		"start_line":    2,
		"end_line":      3,
		"text":          "X\nY\nZ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+X") || !strings.Contains(out, "-b") {
		t.Fatalf("expected replace diff, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "f.txt"))
	if string(data) != "a\nX\nY\nZ\nd\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestFileEditFindReplace(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("alpha\nomega\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "f.txt",
		"old_text":      "alpha",
		"new_text":      "beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+beta") || !strings.Contains(out, "-alpha") {
		t.Fatalf("expected replace diff, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "f.txt"))
	if string(data) != "beta\nomega\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestFileEditFindReplaceDuplicateError(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "dup.txt"), []byte("same\nsame\n"), 0o644)

	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "dup.txt",
		"old_text":      "same",
		"new_text":      "other",
	})
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}

func TestFileEditFindReplaceMultiLine(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	initial := "{\n  \"items\": [\n    {\n      \"name\": \"one\"\n    }\n  ]\n}\n"
	os.WriteFile(filepath.Join(work, "data.json"), []byte(initial), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "data.json",
		"old_text":      "    {\n      \"name\": \"one\"\n    }\n  ]",
		"new_text":      "    {\n      \"name\": \"one\"\n    },\n    {\n      \"name\": \"two\"\n    }\n  ]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "change_id=") || !strings.Contains(out, "+      \"name\": \"two\"") {
		t.Fatalf("expected verification diff, got: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(work, "data.json"))
	want := "{\n  \"items\": [\n    {\n      \"name\": \"one\"\n    },\n    {\n      \"name\": \"two\"\n    }\n  ]\n}\n"
	if string(data) != want {
		t.Fatalf("unexpected:\n%s", data)
	}
}

func TestFileEditViewHistory(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)

	for i := 0; i < 12; i++ {
		_, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
			"mode":          "write",
			"relative_path": fmt.Sprintf("files/%02d.txt", i),
			"content":       fmt.Sprintf("%d\n", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode": "view",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 10 {
		t.Fatalf("view should default to 10 summaries, got %d:\n%s", len(lines), out)
	}
	if strings.Contains(out, "00.txt") || !strings.Contains(out, "11.txt") {
		t.Fatalf("view should show recent changes only:\n%s", out)
	}
}

func TestFileEditViewDetail(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644)

	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "hello.txt",
		"old_text":      "omega",
		"new_text":      "beta",
	})
	if err != nil {
		t.Fatal(err)
	}

	id := sess.Changes[len(sess.Changes)-1].ID
	viewed, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":      "view",
		"change_id": id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(viewed, id) || !strings.Contains(viewed, "+beta") {
		t.Fatalf("view detail missing expected content: %s", viewed)
	}
}

func TestFileEditRollback(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644)

	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "hello.txt",
		"old_text":      "omega",
		"new_text":      "beta",
	})
	if err != nil {
		t.Fatal(err)
	}

	id := sess.Changes[len(sess.Changes)-1].ID
	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":       "rollback",
		"change_ids": []any{id},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(work, "hello.txt"))
	if string(data) != "alpha\nomega\n" {
		t.Fatalf("rollback failed, got: %q", data)
	}
	if len(sess.Changes) != 0 {
		t.Fatalf("rollback should remove change record, got %d", len(sess.Changes))
	}
}

func TestFileEditRollbackDeleteRestoresFile(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	target := filepath.Join(work, "gone.txt")
	os.WriteFile(target, []byte("original\n"), 0o644)

	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "delete_lines",
		"relative_path": "gone.txt",
		"start_line":    1,
		"end_line":      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// After deleting the only line, the file should be removed
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("delete_lines on single-line file should remove it")
	}

	id := sess.Changes[len(sess.Changes)-1].ID
	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":       "rollback",
		"change_ids": []any{id},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(target)
	if string(data) != "original\n" {
		t.Fatalf("rollback should restore deleted file, got: %q", data)
	}
}

func TestFileEditRollbackLaterChangeConflict(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("one\n"), 0o644)

	for _, item := range []struct{ old, new string }{
		{"one", "two"},
		{"two", "three"},
	} {
		_, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
			"mode":          "find_replace",
			"relative_path": "hello.txt",
			"old_text":      item.old,
			"new_text":      item.new,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Try to rollback first change only - should fail
	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":       "rollback",
		"change_ids": []any{sess.Changes[0].ID},
	})
	if err == nil || !strings.Contains(err.Error(), "later change") {
		t.Fatalf("expected later change conflict, got %v", err)
	}
}

func TestFileEditRollbackBothChangesWorks(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("one\n"), 0o644)

	for _, item := range []struct{ old, new string }{
		{"one", "two"},
		{"two", "three"},
	} {
		_, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
			"mode":          "find_replace",
			"relative_path": "hello.txt",
			"old_text":      item.old,
			"new_text":      item.new,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	ids := []any{sess.Changes[0].ID, sess.Changes[1].ID}
	_, err = exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":       "rollback",
		"change_ids": ids,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(work, "hello.txt"))
	if string(data) != "one\n" {
		t.Fatalf("expected rollback to original, got: %q", data)
	}
	if len(sess.Changes) != 0 {
		t.Fatalf("all changes should be removed after rollback, got %d", len(sess.Changes))
	}
}

func TestFileEditDryRun(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode":          "find_replace",
		"relative_path": "hello.txt",
		"old_text":      "omega",
		"new_text":      "beta",
		"dry_run":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "change_id=") {
		t.Fatalf("dry_run should not include change_id: %s", out)
	}
	if !strings.Contains(out, "-omega") || !strings.Contains(out, "+beta") {
		t.Fatalf("dry_run should show the diff: %s", out)
	}

	data, _ := os.ReadFile(filepath.Join(work, "hello.txt"))
	if string(data) != "alpha\nomega\n" {
		t.Fatalf("dry_run should not write: %q", data)
	}
}

func TestFileEditRejectsUnsupportedModes(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)

	for _, mode := range []string{"apply", "revert", "patch", "show", "history"} {
		_, err := exec.Run(context.Background(), sess, "file_edit", map[string]any{
			"mode":          mode,
			"relative_path": "x.txt",
		})
		if err == nil {
			t.Fatalf("expected unsupported mode %q to fail", mode)
		}
	}
}

func TestFileEditSchemaHasCorrectParameters(t *testing.T) {
	exec := NewExecutor(config.Paths{}, nil, 20000, false, false)
	props := toolProperties(t, exec.Schemas(false), "file_edit")
	for _, name := range []string{"mode", "dry_run", "relative_path", "start_line", "end_line", "insert_after_line", "text", "old_text", "new_text", "replace_all", "change_id", "change_ids", "limit"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("file_edit schema should expose %q", name)
		}
	}
	for _, name := range []string{"unified_diff", "patches", "expected_content", "find", "replace", "reverse"} {
		if _, ok := props[name]; ok {
			t.Fatalf("file_edit schema should not expose legacy parameter %q", name)
		}
	}
}

func TestSearchGrepFilenameModeUsesRegex(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	files := []string{"alpha.py", "beta.txt", "dir/gamma.py", "dir/delta.md"}
	for _, file := range files {
		path := filepath.Join(work, filepath.FromSlash(file))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte("x"), 0o644)
	}

	out, err := exec.Run(context.Background(), sess, "search_grep", map[string]any{
		"query": `(^|/).*\.py$`,
		"mode":  "filename",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha.py") || !strings.Contains(out, "dir/gamma.py") {
		t.Fatalf("expected regex filename matches, got %s", out)
	}
	if strings.Contains(out, "beta.txt") || strings.Contains(out, "dir/delta.md") {
		t.Fatalf("unexpected filename matches, got %s", out)
	}
}

func TestSearchGrepDefaultsToCaseSensitive(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "case.txt"), []byte("Alpha\nalpha\n"), 0o644)

	out, err := exec.Run(context.Background(), sess, "search_grep", map[string]any{"query": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Alpha") || !strings.Contains(out, "alpha") {
		t.Fatalf("default search should be case-sensitive, got %s", out)
	}

	out, err = exec.Run(context.Background(), sess, "search_grep", map[string]any{
		"query":          "alpha",
		"case_sensitive": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "alpha") {
		t.Fatalf("case-insensitive search should match both cases, got %s", out)
	}
}

func TestReadFileDetectsBinaryByContent(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "binary.txt"), []byte{'o', 'k', 0, 'h', 'i'}, 0o644)

	out, err := exec.Run(context.Background(), sess, "read_file", map[string]any{"relative_path": "binary.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "likely a useless binary file") || !strings.Contains(out, "force_binary=true") {
		t.Fatalf("expected binary preview, got %s", out)
	}

	out, err = exec.Run(context.Background(), sess, "read_file", map[string]any{
		"relative_path": "binary.txt",
		"force_binary":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "likely a useless binary file") {
		t.Fatalf("force_binary should read content, got %s", out)
	}
}

func TestSearchGrepSkipsBinaryFiles(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	os.WriteFile(filepath.Join(work, "text.txt"), []byte("needle\n"), 0o644)
	os.WriteFile(filepath.Join(work, "image.png"), []byte("needle\n"), 0o644)
	os.WriteFile(filepath.Join(work, "payload.dat"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644)

	out, err := exec.Run(context.Background(), sess, "search_grep", map[string]any{"query": "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "text.txt") {
		t.Fatalf("expected text match, got %s", out)
	}
	if strings.Contains(out, "image.png") || strings.Contains(out, "payload.dat") {
		t.Fatalf("search should skip binary files, got %s", out)
	}
}

func TestSubAgentWaitCheckSchemaIsRootOnly(t *testing.T) {
	exec := NewExecutor(config.Paths{}, nil, 20000, false, false)
	if !hasToolSchema(exec.Schemas(false), "sub_agent_wait_check") {
		t.Fatal("root agent schemas should include sub_agent_wait_check")
	}
	if hasToolSchema(exec.Schemas(true), "sub_agent_wait_check") {
		t.Fatal("sub-agent schemas should not include sub_agent_wait_check")
	}
}

func TestSubAgentWaitCheckReturnsStatusAfterDelay(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	start, err := exec.Run(context.Background(), sess, "sub_agent_start_async", map[string]any{
		"instruction": "inspect a file",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exec.Shutdown()
	id := strings.TrimPrefix(strings.SplitN(start, "\n", 2)[0], "sub_agent_id=")
	out, err := exec.Run(context.Background(), sess, "sub_agent_wait_check", map[string]any{
		"sub_agent_id": id,
		"wait_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "id: "+id) {
		t.Fatalf("wait did not return sub-agent status, got %s", out)
	}
}

func TestSubAgentCheckReturnsSemanticTranscript(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	exec.SetSubAgentRunner(func(ctx context.Context, parentSessionID, taskID, sessionID, agentName, name, instruction string, emit func(string), bind func(string)) string {
		if emit != nil {
			emit("assistant: hello")
		}
		return "the answer"
	})
	start, err := exec.Run(context.Background(), sess, "sub_agent_start_async", map[string]any{
		"name":        "test",
		"instruction": "do something",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exec.Shutdown()
	id := strings.TrimPrefix(strings.SplitN(start, "\n", 2)[0], "sub_agent_id=")
	out, err := exec.Run(context.Background(), sess, "sub_agent_wait_check", map[string]any{
		"sub_agent_id": id,
		"wait_seconds": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[root_agent]: do something") || !strings.Contains(out, "[test]: the answer") {
		t.Fatalf("transcript missing expected dialogue, got %s", out)
	}
	for _, unwanted := range []string{"assistant: hello", "result:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("transcript leaked %q in %s", unwanted, out)
		}
	}
}

func TestShellSchemasFollowShellConfig(t *testing.T) {
	syncExec := NewExecutor(config.Paths{}, nil, 20000, false, false)
	if !hasToolSchema(syncExec.Schemas(false), "shell_run_sync") {
		t.Fatal("sync mode should expose shell_run_sync")
	}
	if hasToolSchema(syncExec.Schemas(false), "shell_run_async") || hasToolSchema(syncExec.Schemas(false), "shell_async_status") || hasToolSchema(syncExec.Schemas(false), "shell_async_kill") || hasToolSchema(syncExec.Schemas(false), "shell_async_stdin") {
		t.Fatal("sync mode should expose only shell_run_sync")
	}

	parallelExec := NewExecutor(config.Paths{}, nil, 20000, true, false)
	if !hasToolSchema(parallelExec.Schemas(false), "shell_run_sync") || !hasToolSchema(parallelExec.Schemas(false), "shell_run_async") || !hasToolSchema(parallelExec.Schemas(false), "shell_async_status") || !hasToolSchema(parallelExec.Schemas(false), "shell_async_kill") {
		t.Fatal("parallel mode should expose sync and async shell tools")
	}
	if hasToolSchema(parallelExec.Schemas(false), "shell_async_stdin") {
		t.Fatal("parallel non-interactive mode should not expose shell async stdin")
	}

	interactiveExec := NewExecutor(config.Paths{}, nil, 20000, true, true)
	if !hasToolSchema(interactiveExec.Schemas(false), "shell_async_stdin") {
		t.Fatal("interactive mode should expose shell_async_stdin")
	}
}

func TestRelativePathRejectsAbsolutePath(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	_, err = exec.Run(context.Background(), sess, "read_file", map[string]any{
		"relative_path": filepath.Join(work, "hello.txt"),
	})
	if err == nil || !strings.Contains(err.Error(), "only relative paths are supported") {
		t.Fatalf("expected relative path error, got %v", err)
	}
}

func TestShellRunModes(t *testing.T) {
	work := t.TempDir()
	syncExec := NewExecutor(config.Paths{Workplace: work}, nil, 20000, false, false)
	out, err := syncExec.Run(context.Background(), nil, "shell_run_sync", map[string]any{
		"command":     "printf sync-ok",
		"timeout_sec": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "shell_id=") || out != "sync-ok" {
		t.Fatalf("sync shell_run should return only output, got %q", out)
	}

	parallelExec := NewExecutor(config.Paths{Workplace: work}, nil, 20000, true, false)
	started, err := parallelExec.Run(context.Background(), nil, "shell_run_async", map[string]any{
		"command": "printf parallel-ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started, "shell_id=") {
		t.Fatalf("parallel shell_run should return shell_id, got %q", started)
	}
	id := strings.TrimPrefix(strings.SplitN(started, "\n", 2)[0], "shell_id=")
	status, err := parallelExec.Run(context.Background(), nil, "shell_async_status", map[string]any{
		"shell_id": id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "parallel-ok") {
		t.Fatalf("shell_status should include output, got %q", status)
	}
}

func TestShellRunSyncReportsTimeout(t *testing.T) {
	work := t.TempDir()
	exec := NewExecutor(config.Paths{Workplace: work}, nil, 20000, false, false)
	out, err := exec.Run(context.Background(), nil, "shell_run_sync", map[string]any{
		"command":     "sleep 2",
		"timeout_sec": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<TIMEOUT after 1 seconds>") {
		t.Fatalf("timeout marker missing, got %q", out)
	}
}

func TestShellAsyncStatusAfterKillReportsTerminated(t *testing.T) {
	work := t.TempDir()
	exec := NewExecutor(config.Paths{Workplace: work}, nil, 20000, true, false)
	started, err := exec.Run(context.Background(), nil, "shell_run_async", map[string]any{
		"command": "sleep 10",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(strings.SplitN(started, "\n", 2)[0], "shell_id=")
	if _, err := exec.Run(context.Background(), nil, "shell_async_kill", map[string]any{
		"shell_id": id,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := exec.Run(context.Background(), nil, "shell_async_status", map[string]any{
		"shell_id": id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "terminated") || strings.Contains(status, "shell not found") {
		t.Fatalf("killed shell status should report termination, got %q", status)
	}
}

func hasToolSchema(schemas []types.ToolSchema, name string) bool {
	for _, item := range schemas {
		if item.Function.Name == name {
			return true
		}
	}
	return false
}

func toolProperties(t *testing.T, schemas []types.ToolSchema, name string) map[string]any {
	t.Helper()
	for _, item := range schemas {
		if item.Function.Name != name {
			continue
		}
		props, ok := item.Function.Parameters["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema properties missing", name)
		}
		return props
	}
	t.Fatalf("tool schema %q not found", name)
	return nil
}
