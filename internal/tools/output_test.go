package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
)

func TestReadFilePreservesOutputBeyondConfiguredLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("complete output\n", 2500)
	if err := os.WriteFile(filepath.Join(root, "output.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{paths: config.Paths{WorkspaceRoot: root}, maxOutputLines: 10}
	out, err := executor.readFile(map[string]any{"path": "output.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != content {
		t.Fatalf("file content was shortened: got %d bytes, want %d", len(out), len(content))
	}
}

func TestSearchPreservesAllMatchesAndLongLines(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("match\n", 250) + "match" + strings.Repeat("x", 1024*1024+1) + "\nmatch"
	path := filepath.Join(root, "output.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	matches, err := grepTextFile(path, "output.txt", regexp.MustCompile("match"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 252 {
		t.Fatalf("got %d matches, want 252", len(matches))
	}
	executor := &Executor{paths: config.Paths{WorkspaceRoot: root}, maxOutputLines: 10}
	out, err := executor.searchGrep(map[string]any{"query": "match"})
	if err != nil {
		t.Fatal(err)
	}
	if out != strings.Join(matches, "\n") {
		t.Fatal("search output was shortened")
	}
}
