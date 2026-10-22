package tui

import (
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/llm"
)

func TestToolArgumentsVisibleDuringExecution(t *testing.T) {
	for _, resultKind := range []string{"tool_result", "tool_error"} {
		t.Run(resultKind, func(t *testing.T) {
			m := testModel(t)
			args := `{"command":"git status"}`
			label := "shell(" + args + ")"
			m.appendAgentEvent(llm.AgentEvent{Kind: "tool_start", Text: "shell", ToolName: "shell", ToolArgs: args})
			if !strings.Contains(m.content, label) {
				t.Fatalf("running tool arguments missing: %q", m.content)
			}
			m.spinner++
			m.refreshPendingSpinners()
			if !strings.Contains(m.content, label) {
				t.Fatal("spinner update removed tool arguments")
			}
			m.appendAgentEvent(llm.AgentEvent{Kind: resultKind, Text: "done"})
			if !strings.Contains(m.content, label) {
				t.Fatal("tool completion removed tool arguments")
			}
		})
	}
}
