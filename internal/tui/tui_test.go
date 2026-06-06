package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/viewport"
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

func TestRootSidebarLinesWrapLongValues(t *testing.T) {
	m := testModel(t)
	m.session.ID = "6922f5c1-feea-long-session-id"
	m.ctx.Root.Model = "very-long-model-name-that-should-wrap"
	m.ctx.Root.Provider = "very-long-provider-name"

	lines, _ := m.rootSidebarLines(30)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "…") {
		t.Fatalf("sidebar should wrap instead of truncating with ellipsis:\n%s", joined)
	}
	if !strings.Contains(joined, "session id: 6922f5c1-feea-") {
		t.Fatalf("wrapped sidebar did not preserve long session id:\n%s", joined)
	}
	compact := strings.ReplaceAll(joined, "\n", "")
	if !strings.Contains(compact, "long-session-id") {
		t.Fatalf("wrapped sidebar lost session id tail:\n%s", joined)
	}
	if !strings.Contains(compact, "very-long-model-name") || !strings.Contains(compact, "very-long-provider-name") {
		t.Fatalf("wrapped sidebar lost model/provider text:\n%s", joined)
	}
}
func TestSidebarToggleUsesAsciiVisibleGlyphs(t *testing.T) {
	m := testModel(t)
	m.width = 120
	m.height = 30
	m.log = viewport.New(80, 20)
	m.input = newChatInput()
	m.syncInputSize()

	visible := m.View()
	if !strings.Contains(visible, "sidebar >") {
		t.Fatalf("visible sidebar view missing collapse hint:\n%s", visible)
	}
	m.sidebarHidden = true
	hidden := m.View()
	if !strings.Contains(hidden, "< sidebar") {
		t.Fatalf("hidden sidebar view missing expand hint:\n%s", hidden)
	}
}

func testModel(t *testing.T) model {
	t.Helper()
	root := t.TempDir()
	store := session.NewStore(filepath.Join(root, ".sessions"))
	sess, err := store.New("test-session", "default")
	if err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{
		HomeDir:       filepath.Join(root, "home"),
		WorkspaceDir:  filepath.Join(root, ".Asayn"),
		WorkspaceRoot: root,
	}
	ctx := &app.Context{
		Paths: paths,
		Root: config.AgentConfig{
			Name:            "default",
			Description:     "General-purpose root agent",
			SystemPrompt:    "You are a highly capable agent.",
			ContextWindow:   1024000,
			MaxOutputTokens: 384000,
			ReasoningEffort: "max",
		},
		Sessions: store,
		Tools:    tools.NewExecutor(paths, store, 2000, false, false),
	}
	return model{
		ctx:                ctx,
		session:            sess,
		input:              newChatInput(),
		log:                viewport.New(80, 20),
		status:             "ready",
		pendingToolStart:   -1,
		pendingThinkStart:  -1,
		pendingAnswerStart: -1,
	}
}
