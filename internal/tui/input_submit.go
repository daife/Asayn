package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const enterQuietPeriod = 500 * time.Millisecond

type submitInputMsg struct {
	at    time.Time
	value string
}

// Unmarked terminal pastes are indistinguishable from typing. Require an
// isolated Enter and no subsequent editing before submitting the input.
func (m model) handleInputEnter(now time.Time) (model, tea.Cmd) {
	previous := m.lastEnter
	m.lastEnter = now
	if !previous.IsZero() && now.Sub(previous) <= enterQuietPeriod {
		m.insertInputSeparator()
		return m, nil
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		return m, nil
	}
	m.pendingEnter = now
	value := m.input.Value()
	return m, tea.Tick(enterQuietPeriod, func(time.Time) tea.Msg {
		return submitInputMsg{at: now, value: value}
	})
}

func (m *model) insertInputSeparator() {
	m.resetHistoryNavigation()
	m.input, _ = m.input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}, Paste: true})
	m.syncInputSize()
}

func (m model) submitInput() (model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	m.syncInputSize()
	if value == "" {
		return m, nil
	}
	if strings.HasPrefix(value, "/") {
		// Check if it's a skill/MCP slash command first
		prompt, isSkillMCP := m.parseSkillMCPCommand(value)
		// Only try handleCommand for real commands (no arguments or known commands)
		parts := strings.Fields(value)
		cmdName := strings.TrimPrefix(parts[0], "/")
		isKnownCommand := false
		for _, c := range commands {
			if strings.TrimPrefix(c.Name, "/") == cmdName {
				isKnownCommand = true
				break
			}
		}
		if isSkillMCP && !isKnownCommand {
			// Treat as agent turn with skill/MCP recommendation
			if m.thinking {
				m.addInputHistory(value)
				m.submitSteering(prompt)
				m.status = m.agentRunningStatus()
				m.syncInputSize()
				return m, nil
			}
			return m.startAgentTurn(prompt, true)
		}
		if replacement, ok := m.selectedCommandForEnter(value); ok {
			value = replacement
		}
		m.addInputHistory(value)
		next, cmd := m.handleCommand(value)
		return next, cmd
	}
	if m.thinking {
		m.addInputHistory(value)
		m.submitSteering(value)
		m.status = m.agentRunningStatus()
		m.syncInputSize()
		return m, nil
	}
	return m.startAgentTurn(value, true)
}
