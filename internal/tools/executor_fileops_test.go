package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/session"
)

func TestComputeDiffDeleteLinesDoesNotMarkShiftedTail(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff(
		"example.txt",
		"l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n",
		"l1\nl2\nl3\nl5\nl6\nl7\nl8\nl9\n",
		"delete_lines",
	)
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,7 +1,6 @@\n" +
		" l1\n" +
		" l2\n" +
		" l3\n" +
		"-l4\n" +
		" l5\n" +
		" l6\n" +
		" l7\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffDeleteLinesIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nfive\n", "delete_lines")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,5 +1,3 @@\n" +
		" one\n" +
		" two\n" +
		"-three\n" +
		"-four\n" +
		" five\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffInsertIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\ntwo\nthree\n", "one\ntwo\ninserted\nthree\n", "insert")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,3 +1,4 @@\n" +
		" one\n" +
		" two\n" +
		"+inserted\n" +
		" three\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffReplaceLinesIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\nold\nthree\n", "one\nnew\nthree\n", "replace_lines")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" one\n" +
		"-old\n" +
		"+new\n" +
		" three\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestFileEditBatchUsesOriginalLineNumbersRegardlessOrder(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(filepath.Join(root, ".sessions"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "example.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(config.Paths{WorkspaceRoot: root}, store, 2000, false, false)
	_, err = e.Run(context.Background(), sess, "file_edit", map[string]any{
		"mode": "batch",
		"batch": []any{
			map[string]any{"mode": "replace_lines", "path": "example.txt", "start_line": 2, "end_line": 2, "text": "TWO"},
			map[string]any{"mode": "delete_lines", "path": "example.txt", "start_line": 4, "end_line": 4},
			map[string]any{"mode": "insert", "path": "example.txt", "insert_after_line": 1, "text": "one-point-five"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "one\none-point-five\nTWO\nthree\nfive\n"
	if string(got) != want {
		t.Fatalf("unexpected content\nwant:\n%q\ngot:\n%q", want, string(got))
	}
	if len(sess.Changes) != 1 {
		t.Fatalf("expected one recorded change, got %d", len(sess.Changes))
	}
}

func TestFileEditBatchRejectsMultiplePaths(t *testing.T) {
	_, _, err := parseBatchOps([]any{
		map[string]any{"mode": "insert", "path": "a.txt", "insert_after_line": 1, "text": "x"},
		map[string]any{"mode": "insert", "path": "b.txt", "insert_after_line": 1, "text": "y"},
	})
	if err == nil {
		t.Fatal("expected path mismatch error")
	}
}
