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
