package tui

import (
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
)

func TestCommandSuggestionsForFuzzyMatch(t *testing.T) {
	suggestions := commandSuggestionsFor("/mc")
	if len(suggestions) == 0 {
		t.Fatal("expected fuzzy command suggestions")
	}
	if suggestions[0].Name != "/model_config" {
		t.Fatalf("expected /model_config first, got %s", suggestions[0].Name)
	}
}

func TestShouldAutoCompactAtEightyPercent(t *testing.T) {
	m := model{
		ctx:           &app.Context{Root: config.AgentConfig{ContextWindow: 1000}},
		activeRunKind: "agent",
	}
	if m.shouldAutoCompact(799) {
		t.Fatal("should not auto compact before 80 percent")
	}
	if !m.shouldAutoCompact(800) {
		t.Fatal("should auto compact at 80 percent")
	}
	m.activeRunKind = "compact"
	if m.shouldAutoCompact(900) {
		t.Fatal("compact run should not recursively auto compact")
	}
}

func TestCompactPromptsSeparateInstructionFromRetainedContext(t *testing.T) {
	if compactRetainedPrompt != "Recall what we worked on before." {
		t.Fatalf("unexpected retained compact prompt: %q", compactRetainedPrompt)
	}
	for _, want := range []string{
		"## Conversation Ledger",
		"Cover every visible user turn in chronological order",
		"## Standing User Preferences And Workflow Habits",
	} {
		if !strings.Contains(compactInstructionPrompt, want) {
			t.Fatalf("compact instruction prompt missing %q", want)
		}
	}
	if strings.Contains(compactRetainedPrompt, "Conversation Ledger") {
		t.Fatal("retained compact prompt should not contain detailed compression instructions")
	}
}

func TestSanitizeThinkingDeltaRejectsWhitespaceOnly(t *testing.T) {
	if got := sanitizeThinkingDelta("已有内容", "\n\n \t\r"); got != "" {
		t.Fatalf("expected whitespace-only thinking delta to be rejected, got %q", got)
	}
}

func TestSanitizeThinkingDeltaCollapsesWhitespace(t *testing.T) {
	got := sanitizeThinkingDelta("", "先想一下\n\n\t  再继续\u00a0\u2003输出")
	want := "先想一下 再继续 输出"
	if got != want {
		t.Fatalf("unexpected sanitized thinking delta: got %q want %q", got, want)
	}
}

func TestSanitizeThinkingDeltaDoesNotDuplicateBoundarySpace(t *testing.T) {
	got := sanitizeThinkingDelta("前面 ", "\n\n 后面")
	if got != "后面" {
		t.Fatalf("unexpected boundary whitespace handling: %q", got)
	}
}

func TestCommandSuggestionsForKeepsPrefixMatch(t *testing.T) {
	suggestions := commandSuggestionsFor("/mo")
	if len(suggestions) < 2 {
		t.Fatalf("expected model suggestions, got %d", len(suggestions))
	}
	if suggestions[0].Name != "/model" {
		t.Fatalf("expected /model first, got %s", suggestions[0].Name)
	}
	if suggestions[1].Name != "/model_config" {
		t.Fatalf("expected /model_config second, got %s", suggestions[1].Name)
	}
}

func TestCommandSuggestionsForSlashShowsAllCommands(t *testing.T) {
	suggestions := commandSuggestionsFor("/")
	if len(suggestions) != len(commands) {
		t.Fatalf("expected all commands, got %d want %d", len(suggestions), len(commands))
	}
}
