package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/asayn/asayn/internal/config"
)

func TestDiscoverAndMigrateClaudeAssets(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	claudeConfig := filepath.Join(root, "claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	if err := os.MkdirAll(filepath.Join(claudeConfig, "skills", "reviewer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfig, "skills", "reviewer", "SKILL.md"), []byte("---\nname: code-reviewer\ndescription: Review code\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfig, "settings.json"), []byte(`{"mcpServers":{"zz-test-server":{"command":"zz-test-server","args":["serve"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := config.Bootstrap(workspace)
	if err != nil {
		t.Fatal(err)
	}

	items, err := DiscoverClaude(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %#v", len(items), items)
	}
	ids := []string{items[0].ID, items[1].ID}
	result, err := MigrateClaude(paths, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Migrated) != 2 || len(result.Skipped) != 0 {
		t.Fatalf("result = %#v, want two migrated", result)
	}
	if _, err := os.Stat(paths.HomePath("skills", "reviewer", "SKILL.md")); err != nil {
		t.Fatalf("skill not migrated: %v", err)
	}
	data, err := os.ReadFile(paths.HomePath(config.MCPConfigKind, "zz-test-server.json"))
	if err != nil {
		t.Fatalf("mcp not migrated: %v", err)
	}
	var parsed mcpConfigFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.MCPServers["zz-test-server"]; !ok {
		t.Fatalf("mcp server missing in %s", data)
	}

	items, err = DiscoverClaude(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if !item.Duplicate {
			t.Fatalf("item should be duplicate after migration: %#v", item)
		}
	}
}
