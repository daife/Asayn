package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

func TestAPITimeoutDoesNotLimitShellExecution(t *testing.T) {
	paths := config.Paths{WorkspaceRoot: t.TempDir()}
	store := session.NewStore(t.TempDir())
	agent := NewAgent(config.APIConfig{Providers: map[string]config.ProviderConfig{"test": {TimeoutSeconds: 1}}}, config.AgentConfig{Provider: "test", Model: "test"}, paths, tools.NewBasicExecutor(paths, store, 2000, false))
	for _, args := range []string{
		`{"command":"sleep 2; echo completed","timeout_sec":0}`,
		`{"command":"sleep 2; echo completed","timeout_sec":-1}`,
	} {
		out, _ := agent.runToolCall(context.Background(), &session.Session{}, types.ToolCall{ID: "shell", Function: types.ToolFunction{Name: "shell_run_sync", Arguments: args}}, nil)
		if !strings.Contains(out, "completed") || strings.Contains(out, "TIMEOUT") || strings.Contains(out, "tool error") {
			t.Fatalf("API timeout interrupted shell: %q", out)
		}
	}
}
