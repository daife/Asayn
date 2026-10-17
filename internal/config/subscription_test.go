package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubscriptionProviderConfig(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{"default endpoint", "", true},
		{"explicit endpoint", `url = "https://api.openai.com/v1/responses"`, true},
		{"untrusted endpoint", `url = "https://example.com/v1"`, false},
		{"API key", `api_key = "must-not-be-used"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			content := "[providers.ChatGPT]\napi_type = \"subscription_responses\"\nsubscription_account = \"personal\"\n" + tc.extra + "\n"
			if err := os.WriteFile(filepath.Join(home, "api_config.toml"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadAPIConfig(Paths{HomeDir: home})
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected config result: %v", err)
			}
			if err == nil && (cfg.Providers["ChatGPT"].BaseURL != "https://api.openai.com/v1" || cfg.Providers["ChatGPT"].SubscriptionAccount != "personal") {
				t.Fatal("invalid normalized subscription config")
			}
		})
	}
}
