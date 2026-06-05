package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/viewport"
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

func TestReplacePendingToolUsesStableStartIndex(t *testing.T) {
	m := model{pendingToolStart: -1}
	m.content = "before\n"
	m.pendingToolName = "view_dir({})"
	m.pendingToolLine = "\nold spinner line\n"
	m.pendingToolStart = len(m.content)
	m.content += m.pendingToolLine
	m.content += "after\n"

	m.replacePendingTool("\nresult block\n")

	if strings.Count(m.content, "result block") != 1 {
		t.Fatalf("expected one result block, got %q", m.content)
	}
	if strings.Contains(m.content, "old spinner line") {
		t.Fatalf("spinner line should have been replaced, got %q", m.content)
	}
	if m.pendingToolStart != -1 || m.pendingToolLine != "" || m.pendingToolName != "" {
		t.Fatalf("pending tool state not cleared: start=%d line=%q name=%q", m.pendingToolStart, m.pendingToolLine, m.pendingToolName)
	}
}

func TestRunningAssistViewShowsWorkingDuration(t *testing.T) {
	m := model{
		log:                 testViewport(80),
		width:               100,
		activeTurnStartedAt: time.Now().Add(-65 * time.Second),
		ctx:                 &app.Context{},
	}
	out := m.runningAssistView()
	if !strings.Contains(out, "Working(1m 5s)") {
		t.Fatalf("running assist view missing working duration: %q", out)
	}
}

func TestRunningAssistViewShowsRetryAndTimeoutStatus(t *testing.T) {
	m := model{
		log:                 testViewport(80),
		width:               100,
		activeTurnStartedAt: time.Now(),
		ctx: &app.Context{
			Root: config.AgentConfig{Provider: "test"},
			API:  config.APIConfig{Providers: map[string]config.ProviderConfig{"test": {TimeoutSeconds: 3}}},
		},
	}

	_ = m.appendAgentEvent(llm.AgentEvent{Kind: "retry", Text: "Retry for 1/10 after 1s (API rate limit)"})
	out := m.runningAssistView()
	if !strings.Contains(out, "Retry for 1/10") || !strings.Contains(out, "Timeout if idle for 0m 3s") {
		t.Fatalf("running assist view missing retry or timeout status: %q", out)
	}

	_ = m.appendAgentEvent(llm.AgentEvent{Kind: "timeout", Text: "API stream idle timeout after 3s"})
	out = m.runningAssistView()
	if !strings.Contains(out, "Timeout: API stream idle timeout after 3s") {
		t.Fatalf("running assist view missing timeout event: %q", out)
	}
}

func TestIdleAssistViewShowsLastWorkedDuration(t *testing.T) {
	m := model{
		log:              testViewport(80),
		width:            100,
		session:          &session.Session{Name: "test", ID: "sess"},
		lastTurnDuration: 65 * time.Second,
	}
	out := m.idleAssistView()
	if !strings.Contains(out, "Worked for 1m 5s") {
		t.Fatalf("idle assist view missing worked duration: %q", out)
	}
}

func TestInputDisplayHeightExpandsUpToFourRows(t *testing.T) {
	if got := inputDisplayHeight("short", 20); got != 1 {
		t.Fatalf("short input should use one row, got %d", got)
	}
	if got := inputDisplayHeight("123456789012345678901", 10); got != 3 {
		t.Fatalf("wrapped input should use three rows, got %d", got)
	}
	if got := inputDisplayHeight(strings.Repeat("x", 200), 10); got != 4 {
		t.Fatalf("input height should cap at four rows, got %d", got)
	}
}

func TestSubAgentFailureReasonIsRendered(t *testing.T) {
	reason := subAgentFailureReason(tools.SubAgentSnapshot{
		Status: "failed",
		Result: "sub-agent error: API stream idle timeout after 120s",
	})
	if reason != "API stream idle timeout after 120s" {
		t.Fatalf("unexpected sub-agent failure reason: %q", reason)
	}
}

func testViewport(width int) viewport.Model {
	return viewport.New(width, 20)
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
