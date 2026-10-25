package tui

import (
	"context"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/llm/usage"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestQuitWaitsForAgentAndSavesBeforeExit(t *testing.T) {
	m := testModel(t)
	m.ctx.UsageTracker = usage.NewTracker(m.ctx.Paths)
	m.session.Messages = []types.ChatMessage{{Role: "user", Content: "work"}}
	ctx, cancel := context.WithCancel(context.Background())
	m.activeCancel = cancel
	m.thinking = true
	m.pendingCompact = true
	m.queuedMessages = []string{"must not run"}
	next, cmd := m.requestQuit()
	if cmd != nil || !next.quitting || ctx.Err() != context.Canceled || next.pendingCompact || len(next.queuedMessages) > 0 {
		t.Fatal("quit did not wait for cancellation")
	}
	next.session.Messages = append(next.session.Messages, types.ChatMessage{Role: "assistant", Content: "saved progress"})
	result, cmd := next.Update(agentMsg{err: context.Canceled})
	if cmd == nil {
		t.Fatal("quit not issued after completion")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected quit command")
	}
	final := result.(model)
	restored, err := final.ctx.Sessions.LoadByID(final.session.ID)
	if err != nil || restored.Messages[len(restored.Messages)-1].Content != "saved progress" {
		t.Fatalf("progress not saved before quit: %v", err)
	}
}

func TestContinueRejectsEmptyOrRunningSession(t *testing.T) {
	m := testModel(t)
	next, cmd := m.handleCommand("/continue")
	if cmd != nil || next.thinking {
		t.Fatal("empty session started work")
	}
	m.thinking = true
	next, cmd = m.handleCommand("/continue")
	if cmd != nil || next.quitting {
		t.Fatal("running session restarted work")
	}
}
