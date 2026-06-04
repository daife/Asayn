package config

import (
	"os"
	"path/filepath"
	"strings"
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
		filepath.Join(paths.HomeDir, SpecialAgentKind, "compact_agent.toml"),
		filepath.Join(paths.HomeDir, "skills", "skill-creator", "SKILL.md"),
	}
	for _, path := range checks {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected embedded default %s: %v", path, err)
		}
	}
}

func TestBootstrapOnlyUpdatesExistingGitIgnore(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := Bootstrap(work); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, ".git")); !os.IsNotExist(err) {
		t.Fatalf("bootstrap should not initialize git, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(work, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("bootstrap should not create .gitignore when it does not exist, stat err=%v", err)
	}

	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(work); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(work); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "node_modules/\n.Asayn/\n") {
		t.Fatalf(".gitignore missing .Asayn entry:\n%s", content)
	}
	if strings.Count(content, ".Asayn/") != 1 {
		t.Fatalf(".Asayn should be added once, got:\n%s", content)
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
	customDir := filepath.Join(paths.WorkspaceDir, RootAgentKind)
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(customDir, "custom.toml")
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

func TestCompactAgentDefaultPromptStaysSmall(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := Bootstrap(work)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgent(paths, SpecialAgentKind, "compact_agent")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.SystemPrompt, "## Conversation Ledger") {
		t.Fatalf("compact_agent system prompt should stay small; detailed instructions belong in the compact user message:\n%s", cfg.SystemPrompt)
	}
	if !strings.Contains(cfg.SystemPrompt, "context compression agent") {
		t.Fatalf("compact_agent system prompt missing role:\n%s", cfg.SystemPrompt)
	}
}

func TestLoadAPIConfigModelLimitsWithSlashModelName(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := Bootstrap(work)
	if err != nil {
		t.Fatal(err)
	}

	api, err := LoadAPIConfig(paths)
	if err != nil {
		t.Fatal(err)
	}
	limits := ModelLimitsFor(api, "SiliconFlow", "nex-agi/Nex-N2-Pro")
	if limits.ContextWindow != 384000 || limits.MaxOutputTokens != 32768 {
		t.Fatalf("unexpected model limits: context=%d output=%d", limits.ContextWindow, limits.MaxOutputTokens)
	}
}

func TestModelLimitsForFillsMissingFields(t *testing.T) {
	api := APIConfig{Providers: map[string]ProviderConfig{
		"p": {
			ModelLimits: map[string]ModelLimits{
				"nex-agi/Nex-N2-Pro": {MaxOutputTokens: 12000},
			},
		},
	}}

	limits := ModelLimitsFor(api, "p", "nex-agi/Nex-N2-Pro")
	if limits.ContextWindow != 384000 || limits.MaxOutputTokens != 12000 {
		t.Fatalf("unexpected filled limits: context=%d output=%d", limits.ContextWindow, limits.MaxOutputTokens)
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
