package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapCopiesEmbeddedDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := Bootstrap(work)
	if err != nil {
		t.Fatal(err)
	}
	checks := []string{
		filepath.Join(paths.HomeDir, "api_config.toml"),
		filepath.Join(paths.HomeDir, RootAgentKind, "default.toml"),
		filepath.Join(paths.HomeDir, SubAgentKind, "default.toml"),
		filepath.Join(paths.HomeDir, SpecialAgentKind, "default.toml"),
		filepath.Join(paths.HomeDir, "skills", "skill-creator", "SKILL.md"),
	}
	for _, path := range checks {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected embedded default %s: %v", path, err)
		}
	}
}

func TestLoadAgentModel(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := Bootstrap(work)
	if err != nil {
		t.Fatal(err)
	}

	// Test default root agent model
	root, err := LoadAgent(paths, RootAgentKind, "default")
	if err != nil {
		t.Fatal(err)
	}
	if root.Model != "deepseek-v4-pro" {
		t.Errorf("expected root agent model deepseek-v4-pro, got %s", root.Model)
	}

	// Test default sub agent model
	sub, err := LoadAgent(paths, SubAgentKind, "default")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Model != "deepseek-v4-flash" {
		t.Errorf("expected sub agent model deepseek-v4-flash, got %s", sub.Model)
	}

	// Test custom model in TOML
	customPath := filepath.Join(paths.WorkspaceDir, RootAgentKind, "custom.toml")
	content := `name = "custom"
model = "custom-model"
`
	if err := os.WriteFile(customPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	custom, err := LoadAgent(paths, RootAgentKind, "custom")
	if err != nil {
		t.Fatal(err)
	}
	if custom.Model != "custom-model" {
		t.Errorf("expected custom model custom-model, got %s", custom.Model)
	}
}

func TestSaveAgentThinkingConfig(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := Bootstrap(work)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := SaveAgentThinkingConfig(paths, SubAgentKind, "default", false, "max")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingEnabled || cfg.ReasoningEffort != "max" {
		t.Fatalf("unexpected saved thinking config: enabled=%t effort=%s", cfg.ThinkingEnabled, cfg.ReasoningEffort)
	}
	loaded, err := LoadAgent(paths, SubAgentKind, "default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ThinkingEnabled || loaded.ReasoningEffort != "max" {
		t.Fatalf("unexpected loaded thinking config: enabled=%t effort=%s", loaded.ThinkingEnabled, loaded.ReasoningEffort)
	}
}
