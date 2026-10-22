package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/llm"
	tea "github.com/charmbracelet/bubbletea"
)

func updateInput(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func TestUnmarkedMultilinePasteDoesNotSubmit(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "running"}[running], func(t *testing.T) {
			m := testModel(t)
			m.thinking = running
			m.turnInput = &llm.TurnInput{}
			before := len(m.session.Messages)
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")})
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
			stale := submitInputMsg{at: m.pendingEnter, value: m.input.Value()}
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("third")})
			m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateInput(m, stale)
			if strings.Join(strings.Fields(m.input.Value()), " ") != "first second third" {
				t.Fatalf("paste lost text: %q", m.input.Value())
			}
			if !m.pendingEnter.IsZero() || len(m.turnInput.Pending()) != 0 || len(m.queuedMessages) != 0 || len(m.session.Messages) != before {
				t.Fatal("paste triggered submission")
			}
			if running {
				// A deliberate Enter after the burst can submit the complete draft once.
				m.lastEnter = time.Now().Add(-enterQuietPeriod - time.Millisecond)
				m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
				confirmation := submitInputMsg{at: m.pendingEnter, value: m.input.Value()}
				m = updateInput(m, confirmation)
				m = updateInput(m, confirmation)
				pending := m.turnInput.Pending()
				if len(pending) != 1 || strings.Join(strings.Fields(pending[0]), " ") != "first second third" {
					t.Fatalf("bad steering: %v", pending)
				}
			}
		})
	}
}

func TestBracketedPasteNeverSubmits(t *testing.T) {
	m := testModel(t)
	m.thinking = true
	m.turnInput = &llm.TurnInput{}
	m = updateInput(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first\r\nsecond\n"), Paste: true})
	if len(m.turnInput.Pending()) != 0 || !m.pendingEnter.IsZero() || !strings.Contains(m.input.Value(), "second") {
		t.Fatal("bracketed paste triggered submission or lost text")
	}
}

func TestEnterQuietPeriod(t *testing.T) {
	m := testModel(t)
	m.input.SetValue("draft")
	now := time.Now()
	m, cmd := m.handleInputEnter(now)
	if cmd == nil || m.pendingEnter != now {
		t.Fatal("missing deferred Enter")
	}
	started := time.Now()
	msg := cmd().(submitInputMsg)
	if time.Since(started) < enterQuietPeriod-10*time.Millisecond || msg.at != now || msg.value != "draft" {
		t.Fatal("incorrect Enter timer")
	}
	for _, gap := range []time.Duration{enterQuietPeriod - time.Millisecond, enterQuietPeriod, enterQuietPeriod + time.Millisecond} {
		candidate := m
		candidate.pendingEnter = time.Time{}
		candidate, cmd = candidate.handleInputEnter(now.Add(gap))
		if (cmd != nil) != (gap > enterQuietPeriod) {
			t.Fatalf("incorrect preceding Enter boundary: %s", gap)
		}
	}
}

func TestPendingEnterCanceledByEditingOrEscape(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyBackspace}, {Type: tea.KeyLeft}, {Type: tea.KeyRunes, Runes: []rune("x")}} {
		m := testModel(t)
		m.thinking = true
		m.turnInput = &llm.TurnInput{}
		m.input.SetValue("draft")
		m = updateInput(m, tea.KeyMsg{Type: tea.KeyEnter})
		stale := submitInputMsg{at: m.pendingEnter, value: m.input.Value()}
		m = updateInput(m, key)
		m = updateInput(m, stale)
		if len(m.turnInput.Pending()) != 0 || !m.pendingEnter.IsZero() {
			t.Fatalf("key %s did not cancel pending submission", key)
		}
	}
}
