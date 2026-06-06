package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSanitizePasteKeyMsgReplacesNewlinesOnlyForPaste(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\r\ntwo\nthree"), Paste: true}
	got := sanitizePasteKeyMsg(msg)
	if string(got.Runes) != "one  two three" {
		t.Fatalf("unexpected pasted runes: %q", string(got.Runes))
	}
	if !got.Paste {
		t.Fatal("paste flag was not preserved")
	}
}

func TestSanitizePasteKeyMsgLeavesEnterUntouched(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	got := sanitizePasteKeyMsg(msg)
	if got.Type != tea.KeyEnter || got.String() != "enter" {
		t.Fatalf("enter was rewritten: %#v (%q)", got, got.String())
	}
}
