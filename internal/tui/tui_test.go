package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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

func TestTransientToolResultReconcilesBeforeNextThinking(t *testing.T) {
	sess := &session.Session{Messages: []types.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "run a tool"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID: "call-1",
			Function: types.ToolFunction{
				Name:      "view_dir",
				Arguments: `{"relative_path":"."}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "file list"},
	}}
	m := model{
		ctx:               &app.Context{},
		session:           sess,
		log:               testViewport(80),
		pendingToolStart:  -1,
		pendingThinkStart: -1,
	}
	m.initRenderer(80)
	m.content = "\n" + successStyle.Render("● Tool result") + ": " + mutedStyle.Render(`view_dir({"relative_path":"."})`) + minorResult("file list", 8) + "\n"
	m.transientToolLine = m.content

	_ = m.appendAgentEvent(llm.AgentEvent{Kind: "thinking_start"})

	if got := strings.Count(m.content, "Tool result"); got != 1 {
		t.Fatalf("expected one rendered tool result after reconciliation, got %d in %q", got, m.content)
	}
	if m.transientToolLine != "" {
		t.Fatalf("transient tool line should be cleared, got %q", m.transientToolLine)
	}
	if !strings.Contains(m.content, "Thinking") {
		t.Fatalf("next thinking line should still render, got %q", m.content)
	}
}

func TestQueuedMessagesRenderBelowInputAndEscCancelsLast(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	m := model{
		ctx:                 &app.Context{Sessions: store},
		session:             sess,
		input:               newChatInput(),
		log:                 testViewport(80),
		width:               100,
		height:              30,
		thinking:            true,
		activeTurnStartedAt: time.Now(),
		historyIndex:        -1,
	}
	m.syncInputSize()
	m.input.SetValue("first queued")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.input.SetValue("second queued")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(m.queuedMessages) != 2 {
		t.Fatalf("expected two queued messages, got %#v", m.queuedMessages)
	}
	if strings.Contains(m.content, "queued #") {
		t.Fatalf("queued messages should not be appended to conversation content: %q", m.content)
	}
	assist := m.assistView()
	if !strings.Contains(assist, "1. first queued") || !strings.Contains(assist, "2. second queued") {
		t.Fatalf("queued messages should render below input in assist view: %q", assist)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if len(m.queuedMessages) != 1 || m.queuedMessages[0] != "first queued" {
		t.Fatalf("esc should cancel the latest queued message, got %#v", m.queuedMessages)
	}
	if strings.Contains(m.content, "canceled queued message") {
		t.Fatalf("queue cancellation should not be appended to conversation content: %q", m.content)
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

func TestInputPromptOnlyShowsOnFirstVisualLine(t *testing.T) {
	input := newChatInput()
	input.SetWidth(12)
	input.SetValue("12345678901234567890")
	input.CursorEnd()

	out := input.View()
	if got := strings.Count(out, "›"); got != 1 {
		t.Fatalf("wrapped input should render one prompt marker, got %d in %q", got, out)
	}
}

func TestInputShowsPlaceholderWhenEmpty(t *testing.T) {
	input := newChatInput()
	input.SetWidth(40)

	out := input.View()
	if !strings.Contains(out, "message or /help") {
		t.Fatalf("empty input should show placeholder: %q", out)
	}
	if got := strings.Count(out, "›"); got != 1 {
		t.Fatalf("empty input should render one prompt marker, got %d in %q", got, out)
	}
}

func TestInputWrapKeepsFirstLineVisibleOnInitialExpansion(t *testing.T) {
	m := testInputModel(12)
	m = typeIntoInput(t, m, "12345678901")

	if got := m.input.Height(); got != 2 {
		t.Fatalf("input should expand to two rows, got %d", got)
	}
	out := m.input.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("wrapped input should render two visual lines: %q", out)
	}
	if strings.Contains(lines[0], "›") {
		t.Fatalf("wrapped continuation line should not render a prompt marker: %q", out)
	}
	if !strings.Contains(lines[1], "› 1") {
		t.Fatalf("cursor line should stay at the bottom with the newest input: %q", out)
	}
	if got := strings.Count(out, "›"); got != 1 {
		t.Fatalf("wrapped input should render one prompt marker, got %d in %q", got, out)
	}
}

func TestInputWrapBoundaryKeepsCursorLineAtBottom(t *testing.T) {
	m := testInputModel(12)
	m = typeIntoInput(t, m, "12345678901")
	m.input.field.SetCursor(10)

	out := m.input.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("wrapped input should render two visual lines: %q", out)
	}
	if !strings.Contains(lines[1], "›") {
		t.Fatalf("cursor at wrap boundary should stay on the lower line: %q", out)
	}
}

func TestInputBackspaceShrinksWrappedRows(t *testing.T) {
	m := testInputModel(12)
	m = typeIntoInput(t, m, "123456789012345678901")
	if got := m.input.Height(); got != 3 {
		t.Fatalf("input should start at three rows, got %d", got)
	}

	for range 11 {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = next.(model)
	}

	if got := m.input.Height(); got != 1 {
		t.Fatalf("input should shrink back to one row, got %d", got)
	}
	out := m.input.View()
	if !strings.Contains(out, "› 1234567890") {
		t.Fatalf("shrunk input should show remaining leading content: %q", out)
	}
}

func testInputModel(width int) model {
	input := newChatInput()
	m := model{
		input: input,
		log:   viewport.New(width, 20),
	}
	m.syncInputSize()
	return m
}

func typeIntoInput(t *testing.T, m model, value string) model {
	t.Helper()
	for _, r := range value {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
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
