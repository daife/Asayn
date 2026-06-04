package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
)

func TestDiffFileApplyHistoryShowRevertMany(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1,2 +1,3 @@\n alpha\n+beta\n omega\n"
	out, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":         "apply",
		"unified_diff": diff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "change_id=") {
		t.Fatalf("expected change id, got %s", out)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nbeta\nomega\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}

	history, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode": "history",
		"path": "hello.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history, "hello.txt") {
		t.Fatalf("expected history for file, got %s", history)
	}

	id := sess.Changes[len(sess.Changes)-1].ID
	shown, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":       "history",
		"change_ids": []any{id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, id) || !strings.Contains(shown, "+beta") {
		t.Fatalf("unexpected show output: %s", shown)
	}

	_, err = exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":       "revert_many",
		"change_ids": []any{id},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nomega\n" {
		t.Fatalf("unexpected reverted content: %q", string(data))
	}
}

func TestDiffFileApplyDryRunDoesNotWrite(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":         "apply",
		"dry_run":      true,
		"unified_diff": "--- a/hello.txt\n+++ b/hello.txt\n@@ -1,2 +1,3 @@\n alpha\n+beta\n omega\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "change_id=") || !strings.Contains(out, "+beta") {
		t.Fatalf("dry run should return only diff, got %s", out)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nomega\n" {
		t.Fatalf("dry run wrote file: %q", data)
	}
}

func TestDiffFilePatchWithoutHeaderUsesPath(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":    "apply",
		"path":    "hello.txt",
		"patches": []any{"@@ -1,2 +1,3 @@\n alpha\n+beta\n omega\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nbeta\nomega\n" {
		t.Fatalf("unexpected patch result: %q", data)
	}
}

func TestDiffFilePatchHeaderPathConflictErrors(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "actual.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":         "apply",
		"path":         "expected.txt",
		"unified_diff": "--- a/actual.txt\n+++ b/actual.txt\n@@ -1 +1 @@\n-alpha\n+beta\n",
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with path") {
		t.Fatalf("expected path conflict error, got %v", err)
	}
}

func TestDiffFileDevNullCreateExistingFileErrors(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":         "apply",
		"unified_diff": "--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1 @@\n+new\n",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing create error, got %v", err)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("file changed unexpectedly: %q", data)
	}
}

func TestDiffFileRevertManyDefaultsToReverseOrder(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, item := range []struct {
		old string
		new string
	}{
		{"one", "two"},
		{"two", "three"},
	} {
		if _, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
			"mode":     "replace",
			"path":     "hello.txt",
			"old_text": item.old,
			"new_text": item.new,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := []any{sess.Changes[0].ID, sess.Changes[1].ID}
	if _, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":       "revert_many",
		"change_ids": ids,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\n" {
		t.Fatalf("unexpected reverted content: %q", data)
	}
}

func TestDiffFileRevertManyReverseOrderFalseUsesProvidedOrder(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, item := range []struct {
		old string
		new string
	}{
		{"one", "two"},
		{"two", "three"},
	} {
		if _, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
			"mode":     "replace",
			"path":     "hello.txt",
			"old_text": item.old,
			"new_text": item.new,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := []any{sess.Changes[0].ID, sess.Changes[1].ID}
	if _, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":          "revert_many",
		"change_ids":    ids,
		"reverse_order": false,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two\n" {
		t.Fatalf("expected provided-order revert to leave second before-content, got %q", data)
	}
}

func TestDiffFileReplaceMultiLineBlockReturnsDiff(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	initial := "{\n  \"items\": [\n    {\n      \"name\": \"one\"\n    }\n  ]\n}\n"
	if err := os.WriteFile(filepath.Join(work, "data.json"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode": "replace",
		"path": "data.json",
		"old_text": "    {\n" +
			"      \"name\": \"one\"\n" +
			"    }\n" +
			"  ]",
		"new_text": "    {\n" +
			"      \"name\": \"one\"\n" +
			"    },\n" +
			"    {\n" +
			"      \"name\": \"two\"\n" +
			"    }\n" +
			"  ]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "change_id=") || !strings.Contains(out, "+    },") || !strings.Contains(out, "+      \"name\": \"two\"") {
		t.Fatalf("replace should return a verification diff, got %s", out)
	}
	data, err := os.ReadFile(filepath.Join(work, "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"items\": [\n    {\n      \"name\": \"one\"\n    },\n    {\n      \"name\": \"two\"\n    }\n  ]\n}\n"
	if string(data) != want {
		t.Fatalf("unexpected replace result:\n%s", data)
	}
}

func TestDiffFileReplaceRequiresUniqueMatchByDefault(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "dup.txt"), []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = exec.Run(context.Background(), sess, "diff_file", map[string]any{
		"mode":     "replace",
		"path":     "dup.txt",
		"old_text": "same",
		"new_text": "other",
	})
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("expected duplicate match error, got %v", err)
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
	files := []string{
		"alpha.py",
		"beta.txt",
		"dir/gamma.py",
		"dir/delta.md",
	}
	for _, file := range files {
		path := filepath.Join(work, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err := os.WriteFile(filepath.Join(work, "case.txt"), []byte("Alpha\nalpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Run(context.Background(), sess, "search_grep", map[string]any{
		"query": "alpha",
	})
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
			emit("assistant: 有一天")
			emit("assistant: ，")
			emit("tool result: read_file\ninternal details")
		}
		return "有一天，小白兔讲了一个笑话。"
	})
	start, err := exec.Run(context.Background(), sess, "sub_agent_start_async", map[string]any{
		"name":        "笑话代理人B",
		"instruction": "讲一个简短笑话",
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
	if !strings.Contains(out, "[root_agent]: 讲一个简短笑话") || !strings.Contains(out, "[笑话代理人B]: 有一天，小白兔讲了一个笑话。") {
		t.Fatalf("semantic transcript missing expected dialogue, got %s", out)
	}
	for _, unwanted := range []string{"assistant: 有一天", "assistant: ，", "tool result:", "result:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("semantic transcript leaked %q in %s", unwanted, out)
		}
	}
}

func TestShellSchemasFollowShellConfig(t *testing.T) {
	syncExec := NewExecutor(config.Paths{}, nil, 20000, false, false)
	if !hasToolSchema(syncExec.Schemas(false), "shell_run_sync") {
		t.Fatal("sync mode should expose shell_run_sync")
	}
	if hasToolSchema(syncExec.Schemas(false), "shell_run_async") || hasToolSchema(syncExec.Schemas(false), "shell_async_status") || hasToolSchema(syncExec.Schemas(false), "shell_async_kill") || hasToolSchema(syncExec.Schemas(false), "shell_async_write") {
		t.Fatal("sync mode should expose only shell_run_sync")
	}

	parallelExec := NewExecutor(config.Paths{}, nil, 20000, true, false)
	if !hasToolSchema(parallelExec.Schemas(false), "shell_run_sync") || !hasToolSchema(parallelExec.Schemas(false), "shell_run_async") || !hasToolSchema(parallelExec.Schemas(false), "shell_async_status") || !hasToolSchema(parallelExec.Schemas(false), "shell_async_kill") {
		t.Fatal("parallel mode should expose sync and async shell tools")
	}
	if hasToolSchema(parallelExec.Schemas(false), "shell_async_write") {
		t.Fatal("parallel non-interactive mode should not expose shell async write")
	}

	interactiveExec := NewExecutor(config.Paths{}, nil, 20000, true, true)
	if !hasToolSchema(interactiveExec.Schemas(false), "shell_async_write") {
		t.Fatal("interactive mode should expose shell_async_write")
	}
}

func TestDiffFileSchemaUsesCanonicalParameters(t *testing.T) {
	exec := NewExecutor(config.Paths{}, nil, 20000, false, false)
	props := toolProperties(t, exec.Schemas(false), "diff_file")
	for _, name := range []string{"find", "replace", "reverse"} {
		if _, ok := props[name]; ok {
			t.Fatalf("diff_file schema should not expose legacy parameter %q", name)
		}
	}
	for _, name := range []string{"old_text", "new_text", "reverse_order", "dry_run", "patches"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("diff_file schema should expose %q", name)
		}
	}
}

func TestDiffFileRejectsLegacyAliasesAndModes(t *testing.T) {
	work := t.TempDir()
	store := session.NewStore(filepath.Join(work, ".Asayn", ".sessions", "root_agents"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(config.Paths{Workplace: work}, store, 20000, false, false)
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range []map[string]any{
		{"mode": "replace", "path": "hello.txt", "find": "alpha", "replace": "beta"},
		{"mode": "preview", "path": "hello.txt", "old_text": "alpha", "new_text": "beta"},
		{"mode": "patch", "path": "hello.txt", "old_text": "alpha", "new_text": "beta"},
		{"mode": "show", "change_id": "missing"},
	} {
		if _, err := exec.Run(context.Background(), sess, "diff_file", args); err == nil {
			t.Fatalf("expected legacy args/mode to fail: %#v", args)
		}
	}
}

func TestShellAsyncWaitCheckIsNotExposed(t *testing.T) {
	exec := NewExecutor(config.Paths{}, nil, 20000, true, true)
	if hasToolSchema(exec.Schemas(false), "shell_async_wait_check") {
		t.Fatal("shell_async_wait_check should not be exposed")
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
