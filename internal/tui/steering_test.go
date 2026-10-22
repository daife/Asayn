package tui

import (
	"testing"

	"github.com/asayn/asayn/internal/llm"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRunningMessageKeys(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyTab} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			m := testModel(t)
			m.thinking = true
			m.turnInput = &llm.TurnInput{}
			m.input.SetValue("new direction")
			next, _ := m.Update(tea.KeyMsg{Type: key})
			got := next.(model)
			wantQueued := 0
			if key == tea.KeyTab {
				wantQueued = 1
			}
			if len(got.queuedMessages) != wantQueued {
				t.Fatalf("queued=%v", got.queuedMessages)
			}
			if key == tea.KeyEnter {
				pending := got.turnInput.Pending()
				if len(pending) != 1 || pending[0] != "new direction" {
					t.Fatalf("steering missing: %v", pending)
				}
			}
			if got.input.Value() != "" {
				t.Fatal("input was not cleared")
			}
			if len(got.session.Messages) != len(m.session.Messages) {
				t.Fatal("UI modified running session")
			}
		})
	}
}

func TestSteeringFallsBackToQueueAfterRunClosed(t *testing.T) {
	m := testModel(t)
	m.thinking = true
	m.submitSteering("next turn")
	if len(m.queuedMessages) != 1 || m.queuedMessages[0] != "next turn" {
		t.Fatalf("message lost: %v", m.queuedMessages)
	}
}
