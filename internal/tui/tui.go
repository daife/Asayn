package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/llm/usage"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	ctx                       *app.Context
	session                   *session.Session
	input                     chatInput
	log                       viewport.Model
	renderer                  *glamour.TermRenderer
	content                   string
	commandOutput             string
	width                     int
	height                    int
	status                    string
	thinking                  bool
	activeCancel              context.CancelFunc
	activeRunKind             string
	pendingCompact            bool
	compactBaseLen            int
	queuedMessages            []string
	agentEvents               chan agentRunEvent
	commandSelected           int
	inputHistory              []string
	historyIndex              int
	historyDraft              string
	resumePicker              bool
	resumeItems               []session.Session
	resumeSelected            int
	rootAgentPicker           bool
	rootAgentItems            []config.AgentInfo
	rootAgentSelected         int
	modelConfigPicker         bool
	modelConfigAgentSelected  int
	modelConfigAgents         []config.AgentInfo
	modelConfigAgentKinds     []string
	modelConfigOptionSelected int
	modelConfigModels         []string
	modelConfigEditingPercent bool
	modelConfigPercentDraft   string
	skillItems                []config.Skill
	subViewID                 string
	sidebarHidden             bool
	spinner                   int
	pendingToolLine           string
	pendingToolName           string
	pendingToolStart          int
	transientToolLine         string
	pendingThinkLine          string
	pendingThinkSpin          bool
	streamThinkText           string
	pendingThinkStart         int
	pendingAnswerStart        int
	streamAnswerText          string
	usageStats                usage.Stats
	latestTotalTokens         int
	activeTurnUsage           types.Usage
	activeTurnStartedAt       time.Time
	lastTurnDuration          time.Duration
	activeRetryStatus         string
	activeTimeoutStatus       string
	sidebarCache              string
	sidebarCacheKey           string
	wrappedLines              int
	wrappedContent            string
	wrapWidth                 int
	wrappedLen                int
	rawNL                     int
	wrappedPrefix             string // wrapped content up to last raw newline (stable)
	wrappedPrefixRawLen       int    // byte position of last raw newline + 1
}

type agentMsg struct {
	answer string
	usage  types.Usage
	err    error
	kind   string
	model  string
}

type agentRunEvent struct {
	event llm.AgentEvent
	done  *agentMsg
}

type agentStartedMsg struct {
	events chan agentRunEvent
}

type agentProgressMsg struct {
	event  llm.AgentEvent
	events chan agentRunEvent
}

type agentPollMsg struct{}
type uiTickMsg struct{}

type commandSpec struct {
	Name        string
	Description string
}

const compactRetainedPrompt = "Recall what we worked on before."

const compactInstructionPrompt = `Create a rigorous continuation summary of the visible conversation so the future main agent can continue with this summary as its sole memory after compression.

The summary is for the future main agent, not for the user. It must be complete enough to continue work without rereading hidden history. Do not write a vague narrative. Do not omit earlier user turns just because later turns look more important.

Output exactly these top-level sections, in this order:

## Conversation Ledger
Cover every visible user turn in chronological order. For each turn, create an entry like:
- Turn N:
  - User request: what the user asked for, including exact constraints, corrections, wording requirements, and whether they asked to install, commit, verify, or only explain.
  - Assistant actions: tools/commands/files inspected or edited, important design decisions, and any assumptions made.
  - Tool results: key command outputs, errors, test results, file paths, model/API/config names, and any evidence that affected the work.
  - Outcome: what was completed, what changed, what was installed or not installed, and what remains unresolved.

If the input already contains an earlier compact summary, treat it as the authoritative record of history hidden by a previous compression. Include it as the earliest ledger entry or as prior-state context; do not invent details beyond it.

## Current State
Summarize the exact current project/session state:
- Current user goal and latest intent.
- Files changed or added, including config files outside the repository if mentioned.
- Tests, builds, installs, and their latest known status.
- Running, canceled, queued, or interrupted model/tool chains.
- Active context boundary facts, including whether this summary follows a manual or automatic compression if visible.

## Pending Work
List the next concrete tasks, known bugs, missing verification, risks, and likely next command or file to inspect. Be explicit about anything that did not finish.

## Standing User Preferences And Workflow Habits
Extract recurring requirements and habits from the whole conversation, for example:
- Whether the user expects changes to be committed after completion.
- Whether the user expects go test ./..., a build, reinstall, or specific verification commands after changes.
- Whether the user prefers Chinese or English for user-facing strings, prompts, status messages, or documentation in specific areas.
- Any repeated preferences about tool exposure, skill visibility, binary filtering, retry behavior, context compression, or installation.
Only state a habit if the conversation provides evidence. If there is no evidence for a habit, say it is not established.

## Critical Constraints
Preserve non-negotiable constraints, edge cases, and exact strings that future work must not break.

Rules:
- Be chronological and exhaustive about user turns.
- Preserve exact error messages, command outputs, file paths, function names, config table names, model names, and user-facing strings when they may matter.
- If a model/tool call chain was interrupted, summarize what had already happened and clearly state what did not finish.
- Do not claim tests passed, files were installed, or commits were made unless the history shows that.
- Do not include an introduction, apology, or meta-commentary about compression.
- Output only the continuation summary.`

var commands = []commandSpec{
	{Name: "/help", Description: "show help"},
	{Name: "/new", Description: "start a new session"},
	{Name: "/resume", Description: "resume a saved session"},
	{Name: "/retry", Description: "retry the last request"},
	{Name: "/rename", Description: "rename current session"},
	{Name: "/fork", Description: "fork from the current point"},
	{Name: "/root_agent", Description: "select root agent"},
	{Name: "/model", Description: "select root agent (alias for /root_agent)"},
	{Name: "/model_config", Description: "configure agent settings"},
	{Name: "/compact", Description: "compress conversation context"},
	{Name: "/btw", Description: "reserved side-channel question"},
	{Name: "/exit", Description: "exit CLI"},
}

var (
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	thinkingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	toolRunStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	userStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	agentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	sectionStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

const inputPrompt = "› "

func Run(ctx *app.Context) error {
	sess, err := ctx.Sessions.New("", ctx.Root.Name)
	if err != nil {
		return err
	}
	input := newChatInput()
	vp := viewport.New(80, 20)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		Down:         key.NewBinding(key.WithKeys("down")),
		Up:           key.NewBinding(key.WithKeys("up")),
	}
	content := ""
	vp.SetContent(content)

	m := model{
		ctx:                ctx,
		session:            sess,
		input:              input,
		log:                vp,
		content:            content,
		status:             "ready",
		historyIndex:       -1,
		pendingToolStart:   -1,
		pendingThinkStart:  -1,
		pendingAnswerStart: -1,
	}
	m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)
	m.initRenderer(80)
	m.syncInputSize()
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m *model) initRenderer(width int) {
	if width <= 0 {
		width = 80
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	m.renderer = r
}

func (m *model) recalcLogWidth() {
	sidebar := 30
	if m.width < 100 || m.sidebarHidden {
		sidebar = 0
	}
	m.log.Width = m.width - sidebar - 4
	if m.log.Width < 20 {
		m.log.Width = m.width
	}
}

func (m *model) syncInputSize() {
	width := m.log.Width
	if width <= 0 {
		width = 80
	}
	m.input.SetWidth(width)

	if m.height > 0 {
		prevHeight := m.log.Height
		m.log.Height = m.height - m.input.Height() - m.assistHeight()
		if m.log.Height < 3 {
			m.log.Height = 3
		}
		// Preserve scroll position when input area grows/shrinks
		if prevHeight > 0 && m.log.Height != prevHeight {
			wasAtBottom := m.log.AtBottom()
			total := m.log.TotalLineCount()
			if wasAtBottom {
				m.log.GotoBottom()
			} else if total > prevHeight && total > m.log.Height {
				ratio := float64(m.log.YOffset) / float64(total-prevHeight)
				if ratio < 0 {
					ratio = 0
				}
				if ratio > 1 {
					ratio = 1
				}
				m.log.YOffset = int(ratio * float64(total-m.log.Height))
			}
		}
	}
}

func inputDisplayHeight(value string, contentWidth int) int {
	if contentWidth < 1 {
		contentWidth = 1
	}
	if value == "" {
		return 1
	}
	rows := 0
	for _, line := range strings.Split(value, "\n") {
		width := lipgloss.Width(line)
		lineRows := 1
		if width > 0 {
			lineRows = (width + contentWidth - 1) / contentWidth
		}
		rows += lineRows
		if rows >= 4 {
			return 4
		}
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.input.Blink(), uiTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLogWidth()
		m.syncInputSize()
		m.initRenderer(m.log.Width)
		m.invalidateWrap(); m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
		m.refreshLog(false)
	case tea.KeyMsg:
		msg = sanitizePasteKeyMsg(msg)
		if m.resumePicker {
			next, cmd, handled := m.handleResumePickerKey(msg)
			if handled {
				return next, cmd
			}
		}
		if m.rootAgentPicker {
			next, cmd, handled := m.handleRootAgentPickerKey(msg)
			if handled {
				return next, cmd
			}
		}
		if m.modelConfigPicker {
			next, cmd, handled := m.handleModelConfigPickerKey(msg)
			if handled {
				return next, cmd
			}
		}
		switch msg.String() {
		case "ctrl+c":
			m.shutdownActiveWork()
			_ = m.cleanupEmptySession()
			return m, tea.Quit
		case "esc":
			if m.subViewID != "" {
				m.subViewID = ""
				m.status = "ready"
				m.refreshLog(true)
				return m, nil
			}
			if m.thinking || len(m.queuedMessages) > 0 {
				return m.handleInterrupt(), nil
			}
			if m.historyIndex != -1 {
				m.restoreHistoryDraft()
				return m, nil
			}
			m.commandSelected = 0
			return m, nil
		case "up", "down":
			if m.historyIndex != -1 {
				if m.navigateInputHistory(msg.String()) {
					return m, nil
				}
			}
			if suggestions := m.commandSuggestions(); len(suggestions) > 0 {
				if msg.String() == "up" {
					m.commandSelected--
					if m.commandSelected < 0 {
						m.commandSelected = len(suggestions) - 1
					}
				} else {
					m.commandSelected++
					if m.commandSelected >= len(suggestions) {
						m.commandSelected = 0
					}
				}
				return m, nil
			}
			if m.navigateInputHistory(msg.String()) {
				return m, nil
			}
		case "tab":
			if suggestions := m.commandSuggestions(); len(suggestions) > 0 {
				m.completeCommand(suggestions[m.clampedCommandSelected(len(suggestions))].Name)
				return m, nil
			}
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			m.syncInputSize()
			if value == "" {
				return m, nil
			}
			if strings.HasPrefix(value, "/") {
				if replacement, ok := m.selectedCommandForEnter(value); ok {
					value = replacement
				}
				m.addInputHistory(value)
				next, cmd := m.handleCommand(value)
				return next, cmd
			}
			if m.thinking {
				m.addInputHistory(value)
				m.queuedMessages = append(m.queuedMessages, value)
				m.status = m.agentRunningStatus()
				m.syncInputSize()
				return m, nil
			}
			return m.startAgentTurn(value, true)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			next := m.handleMouseClick(msg.X, msg.Y)
			return next, nil
		}
	case agentMsg:
		m.agentEvents = nil
		m.activeCancel = nil
		m.thinking = false
		turnDuration := time.Duration(0)
		if !m.activeTurnStartedAt.IsZero() {
			turnDuration = time.Since(m.activeTurnStartedAt)
		}
		m.activeTurnStartedAt = time.Time{}
		m.activeRetryStatus = ""
		m.activeTimeoutStatus = ""
		runKind := msg.kind
		m.activeRunKind = ""
		thinkingAlreadyRendered := m.finalizePendingThinking()
		m.streamThinkText = ""
		m.pendingThinkSpin = false
		m.pendingThinkLine = ""
		m.pendingThinkStart = -1
		m.pendingToolLine = ""
		m.pendingToolName = ""
		m.pendingToolStart = -1
		m.transientToolLine = ""

		if msg.err == nil {
			modelName := msg.model
			if modelName == "" {
				modelName = m.ctx.Root.Model
			}
			// Compact runs are billed to the same root session because they are part of
			// maintaining that conversation's usable context.
			_ = m.ctx.UsageTracker.Log(m.session.ID, m.session.Name, modelName, msg.usage)
			m.latestTotalTokens = msg.usage.TotalTokens
			m.session.LastTotalTokens = msg.usage.TotalTokens
			_ = m.ctx.Sessions.Save(m.session)
		}
		m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)

		if msg.err != nil {
			m.pendingAnswerStart = -1
			m.streamAnswerText = ""
			if errors.Is(msg.err, context.Canceled) || strings.Contains(msg.err.Error(), "context canceled") {
				m.status = "ready"
				if !m.pendingCompact {
					m.appendLog("\n" + mutedStyle.Render("interrupted") + "\n")
				}
			} else {
				m.status = "error"
				m.appendLog("\n" + errorStyle.Render("● Asayn error") + ": " + msg.err.Error() + "\n")
			}
			m.appendDivider()
			_ = m.ctx.Sessions.Save(m.session)
		} else {
			m.status = "ready"
			m.lastTurnDuration = turnDuration
			if runKind == "compact" {
				m.session.Messages = append(m.session.Messages,
					types.ChatMessage{Role: "user", Content: compactRetainedPrompt},
					types.ChatMessage{Role: "assistant", Content: msg.answer},
				)
				m.session.CompactedBefore = m.compactBaseLen
				m.compactBaseLen = 0
			}
			if !thinkingAlreadyRendered {
				m.emitFinalThinking()
			}
			m.invalidateWrap(); m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
			m.pendingAnswerStart = -1
			m.streamAnswerText = ""
			m.refreshLog(false)
			m.appendDivider()
			_ = m.ctx.Sessions.Save(m.session)
		}
		if m.pendingCompact {
			return m.startCompactTurn()
		}
		if len(m.queuedMessages) > 0 {
			next := m.queuedMessages[0]
			m.queuedMessages = m.queuedMessages[1:]
			return m.startAgentTurn(next, false)
		}
	case agentStartedMsg:
		m.agentEvents = msg.events
		return m, pollAgentEvents(m.agentEvents)
	case agentProgressMsg:
		if !m.thinking || msg.events == nil || msg.events != m.agentEvents {
			return m, nil
		}
		if m.appendAgentEvent(msg.event) {
			next, cmd := m.requestCompact(true)
			return next, tea.Batch(cmd, pollAgentEvents(msg.events))
		}
		return m, pollAgentEvents(m.agentEvents)
	case agentPollMsg:
		if m.thinking {
			m.spinner++
			m.refreshPendingSpinners()
		}
		if m.agentEvents != nil {
			return m, pollAgentEvents(m.agentEvents)
		}
		return m, nil
	case uiTickMsg:
		if m.thinking {
			m.spinner++
			m.refreshPendingSpinners()
		}
			return m, uiTick()
	}
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok && m.historyIndex != -1 && isInputEditingKey(key.String()) {
		m.resetHistoryNavigation()
	}
	m.input, cmd = m.input.Update(msg)
	m.syncInputSize()
	cmds = append(cmds, cmd)
	m.clampCommandSelection()
	m.log, cmd = m.log.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func sanitizePasteKeyMsg(msg tea.KeyMsg) tea.KeyMsg {
	if !msg.Paste || msg.Type != tea.KeyRunes {
		return msg
	}
	runes := append([]rune(nil), msg.Runes...)
	for idx, r := range runes {
		if r == '\r' || r == '\n' {
			runes[idx] = ' '
		}
	}
	msg.Runes = runes
	return msg
}

func (m model) cleanupEmptySession() error {
	if session.HasContent(m.session) {
		return nil
	}
	return m.ctx.Sessions.Delete(m.session)
}

func (m model) handleMouseClick(x, y int) model {
	// Right edge entire column toggles sidebar.
	if m.width >= 100 && x >= m.width-1 {
		m.sidebarHidden = !m.sidebarHidden
		prevWidth := m.log.Width
		m.recalcLogWidth()
		m.syncInputSize()
		if prevWidth != m.log.Width {
			m.refreshLog(false)
		}
		return m
	}
	if m.width < 100 || m.subViewID != "" {
		return m
	}
	sidebarLeft := m.width - 30
	if x < sidebarLeft {
		return m
	}
	_, subRows := m.rootSidebarLines(30)
	if len(subRows) == 0 {
		return m
	}
	subs := m.ctx.Tools.SubAgentSnapshots()
	idx := -1
	for rowIdx, row := range subRows {
		if y == row {
			idx = rowIdx
			break
		}
	}
	if idx < 0 || idx >= len(subs) {
		return m
	}
	snap := subs[idx]
	m.subViewID = snap.ID
	m.status = "view sub-agent"
	m.log.SetContent(m.wrapContent(m.subAgentView()))
	m.log.GotoTop()
	return m
}

func (m model) View() string {
	if m.width == 0 {
		return "Asayn"
	}
	sidebarWidth := 30
	hasSidebar := m.width >= 100 && !m.sidebarHidden

	mainWidth := m.width
	if hasSidebar {
		mainWidth = m.width - sidebarWidth - 2
	}
	if mainWidth < 20 {
		mainWidth = 20
	}
	// Toggle hint for sidebar
	var headerLine string
	if m.width >= 100 {
		hint := "< sidebar"
		if !m.sidebarHidden {
			hint = "sidebar >"
		}
		headerLine = lipgloss.NewStyle().Width(mainWidth).Align(lipgloss.Right).Render(mutedStyle.Render(hint))
	}

	body := m.log.View()
	main := lipgloss.JoinVertical(lipgloss.Left,
		headerLine,
		lipgloss.NewStyle().Width(mainWidth).Height(m.log.Height).Render(body),
		m.input.View(),
		m.assistView(),
	)

	if !hasSidebar {
		return main
	}

	side := m.sidebar(sidebarWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, main, " ", side)
}

func (m *model) appendLog(s string) {
	m.content += s
	m.refreshLog(false)
}



func (m *model) refreshLog(forceBottom bool) {
	wasBottom := m.log.AtBottom()
	// Preserve scroll position ratio when not at bottom
	prevRatio := float64(0)
	prevTotal := m.log.TotalLineCount()
	prevHeight := m.log.Height
	if !wasBottom && !forceBottom && prevTotal > prevHeight {
		prevRatio = float64(m.log.YOffset) / float64(prevTotal-prevHeight)
	}
	if m.subViewID != "" {
		m.log.SetContent(m.wrapContent(m.subAgentView()))
	} else {
		m.log.SetContent(m.wrapContent(m.content))
	}
	if forceBottom || wasBottom {
		m.log.GotoBottom()
	} else if prevRatio > 0 {
		newTotal := m.log.TotalLineCount()
		if newTotal > m.log.Height && prevRatio < 1 {
			m.log.YOffset = int(prevRatio * float64(newTotal-m.log.Height))
		}
	}
}
func (m *model) invalidateWrap() {
	m.wrappedLines = 0
	m.wrappedContent = ""
	m.wrappedLen = 0
	m.rawNL = 0
	m.wrappedPrefix = ""
	m.wrappedPrefixRawLen = 0
}

func (m *model) invalidateWrapFrom(pos int) {
	if pos <= 0 || pos >= len(m.content) {
		m.invalidateWrap()
		return
	}
	nl := 0
	for i := 0; i < pos; i++ {
		if m.content[i] == '\n' {
			nl++
		}
	}
	if nl < m.rawNL {
		m.rawNL = nl
		m.wrappedLines = nl
		m.wrappedLen = pos
		m.wrappedPrefix = ""
		m.wrappedPrefixRawLen = 0
		if pos > 0 {
			m.wrappedContent = wrapANSI(m.content[:pos], m.wrapWidth)
			m.wrappedLines = linesIn(m.wrappedContent)
		} else {
			m.wrappedContent = ""
			m.wrappedLines = 0
		}
	}
}

func linesIn(s string) int {
	n := 0
	for _, b := range []byte(s) {
		if b == '\n' {
			n++
		}
	}
	return n
}


func (m *model) wrapContent(content string) string {
	width := m.log.Width
	if width <= 0 {
		return content
	}

	// Full re-wrap when width changes or content was truncated.
	if width != m.wrapWidth || len(content) < m.wrappedLen {
		m.wrappedLines = 0
		m.wrappedContent = ""
		m.rawNL = 0
		m.wrapWidth = width
		m.wrappedLen = 0
		m.wrappedPrefix = ""
		m.wrappedPrefixRawLen = 0
	}

	// Count raw newlines in current content.
	totalRawNL := 0
	for _, b := range []byte(content) {
		if b == '\n' {
			totalRawNL++
		}
	}

	// Full wrap if cache is empty.
	if m.wrappedLines == 0 {
		m.wrappedContent = wrapANSI(content, width)
		m.wrappedLines = linesIn(m.wrappedContent)
		m.rawNL = totalRawNL
		m.wrappedLen = len(content)
		m.wrapWidth = width
		// Set stable prefix (up to last raw newline).
		lastNL := strings.LastIndex(content, "\n")
		if lastNL >= 0 {
			m.wrappedPrefixRawLen = lastNL + 1
			m.wrappedPrefix = wrapANSI(content[:m.wrappedPrefixRawLen], width)
		}
		return m.wrappedContent
	}

	if totalRawNL > m.rawNL {
		// New raw lines appeared. Extend the stable prefix to the
		// last raw newline, then wrap only the new tail.
		lastNL := strings.LastIndex(content, "\n")
		newPrefixRawLen := lastNL + 1
		prefixTail := content[m.wrappedPrefixRawLen:newPrefixRawLen]
		m.wrappedPrefix += wrapANSI(prefixTail, width)
		m.wrappedPrefixRawLen = newPrefixRawLen

		newPart := content[m.wrappedLen:]
		wrappedNew := wrapANSI(newPart, width)
		m.wrappedContent = m.wrappedPrefix + wrappedNew
		m.wrappedLines = linesIn(m.wrappedContent)
		m.rawNL = totalRawNL
		m.wrappedLen = len(content)
		return m.wrappedContent
	}

	// Same raw line count (mid-line delta). Prefix is stable.
	tail := content[m.wrappedPrefixRawLen:]
	m.wrappedContent = m.wrappedPrefix + wrapANSI(tail, width)
	m.wrappedLines = linesIn(m.wrappedContent)
	m.wrappedLen = len(content)
	return m.wrappedContent
}


func (m *model) appendDivider() {
	m.appendLog(renderDivider(m.log.Width, "") + "\n")
}

func (m model) startAgentTurn(value string, recordHistory bool) (model, tea.Cmd) {
	m.commandOutput = ""
	m.activeTurnUsage = types.Usage{}
	m.activeRetryStatus = ""
	m.activeTimeoutStatus = ""
	if recordHistory {
		m.addInputHistory(value)
	}
	prompt := m.withActiveWorkContext(value)
	m.appendLog("\n" + userStyle.Render("You") + ":\n" + prompt + "\n")
	m.log.GotoBottom()
	m.thinking = true
	m.activeRunKind = "agent"
	m.activeTurnStartedAt = time.Now()
	m.lastTurnDuration = 0
	m.status = m.agentRunningStatus()
	cmd, cancel, events := m.ask(prompt, m.ctx.Agent, m.session, "agent", m.ctx.Root.Model)
	m.activeCancel = cancel
	m.agentEvents = events
	return m, tea.Batch(cmd, pollAgentEvents(events))
}

func (m model) startRetryTurn() (model, tea.Cmd) {
	m.commandOutput = ""
	m.activeTurnUsage = types.Usage{}
	m.activeRetryStatus = ""
	m.activeTimeoutStatus = ""
	m.appendLog("\n" + mutedStyle.Render("retrying previous request...") + "\n")
	m.log.GotoBottom()
	m.thinking = true
	m.activeRunKind = "agent"
	m.activeTurnStartedAt = time.Now()
	m.lastTurnDuration = 0
	m.status = m.agentRunningStatus()
	cmd, cancel, events := m.retryAsk(m.ctx.Agent, m.session, "agent", m.ctx.Root.Model)
	m.activeCancel = cancel
	m.agentEvents = events
	return m, tea.Batch(cmd, pollAgentEvents(events))
}

func (m model) requestCompact(auto bool) (model, tea.Cmd) {
	m.commandOutput = ""
	if m.thinking {
		m.pendingCompact = true
		if m.activeCancel != nil {
			m.activeCancel()
			m.activeCancel = nil
		}
		m.status = "auto compressing"
		m.appendLog("\n" + mutedStyle.Render("auto compressing...") + "\n")
		return m, nil
	}
	if auto {
		m.appendLog("\n" + mutedStyle.Render("auto compressing...") + "\n")
	}
	return m.startCompactTurn()
}

func (m model) startCompactTurn() (model, tea.Cmd) {
	cfg, err := config.LoadAgent(m.ctx.Paths, config.SpecialAgentKind, "compact_agent")
	if err != nil {
		m.setCommandOutput("error loading compact_agent: " + err.Error())
		return m, nil
	}
	limits := config.ModelLimitsFor(m.ctx.API, cfg.Provider, cfg.Model)
	cfg.ContextWindow = limits.ContextWindow
	cfg.MaxOutputTokens = limits.MaxOutputTokens

	temp := *m.session
	temp.Messages = append([]types.ChatMessage(nil), m.session.Messages...)
	temp.RootAgent = cfg.Name
	temp.VisibleSkills = map[string]bool{}
	for k, v := range m.session.VisibleSkills {
		temp.VisibleSkills[k] = v
	}

	exec := tools.NewBasicExecutor(m.ctx.Paths, m.ctx.Sessions, cfg.MaxOutputLines)
	agent := llm.NewSubAgent(m.ctx.API, cfg, m.ctx.Paths, exec)
	agent.RefreshSystemPrompt(&temp)

	m.activeTurnUsage = types.Usage{}
	m.activeRetryStatus = ""
	m.activeTimeoutStatus = ""
	m.compactBaseLen = len(m.session.Messages)
	m.pendingCompact = false
	m.appendLog("\n" + userStyle.Render("You") + ":\n" + compactRetainedPrompt + "\n")
	m.log.GotoBottom()
	m.thinking = true
	m.activeRunKind = "compact"
	m.activeTurnStartedAt = time.Now()
	m.lastTurnDuration = 0
	m.status = "compressing context"
	cmd, cancel, events := m.ask(compactInstructionPrompt, agent, &temp, "compact", cfg.Model)
	m.activeCancel = cancel
	m.agentEvents = events
	return m, tea.Batch(cmd, pollAgentEvents(events))
}

func (m model) withActiveWorkContext(value string) string {
	status := m.activeWorkContext()
	if status == "" {
		return value
	}
	return value + "\n\n" + status
}

func (m model) activeWorkContext() string {
	rows := []string{}
	subRows := []string{}
	for _, sub := range m.ctx.Tools.SubAgentSnapshots() {
		if sub.Status == "completed" {
			continue
		}
		if sub.Status == "ready_for_check" {
			rows = append(rows, fmt.Sprintf("[%s] is ready for check", sub.ID))
			continue
		}
		subRows = append(subRows, fmt.Sprintf("- sub_agent %s: %s (%s)", sub.ID, sub.Status, sub.Name))
	}
	if len(subRows) > 0 {
		rows = append(rows, "Active sub-agents:")
		rows = append(rows, subRows...)
	}
	shellRows := []string{}
	for _, sh := range m.ctx.Tools.ShellSnapshots() {
		if sh.Status != "running" {
			continue
		}
		shellRows = append(shellRows, fmt.Sprintf("- shell %s: %s (pid %d) %s", sh.ID, sh.Status, sh.PID, sh.Command))
	}
	if len(shellRows) > 0 {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, "Active terminals:")
		rows = append(rows, shellRows...)
	}
	if len(rows) == 0 {
		return ""
	}
	return "[Active Context]\n" + strings.Join(rows, "\n")
}

func (m model) handleInterrupt() model {
	if len(m.queuedMessages) > 0 {
		m.queuedMessages = m.queuedMessages[:len(m.queuedMessages)-1]
		m.status = m.agentRunningStatus()
		m.syncInputSize()
		return m
	}
	if m.activeCancel != nil {
		m.activeCancel()
		m.activeCancel = nil
		m.status = "interrupting agent"
		m.appendLog("\n" + mutedStyle.Render("interrupting current agent turn...") + "\n")
		return m
	}
	return m
}

func (m model) shutdownActiveWork() {
	if m.activeCancel != nil {
		m.activeCancel()
	}
	m.ctx.Tools.Shutdown()
}

func (m model) agentRunningStatus() string {
	if len(m.queuedMessages) > 0 {
		return fmt.Sprintf("agent running; queued %d", len(m.queuedMessages))
	}
	return "agent running"
}

func (m model) ask(prompt string, agent *llm.Agent, sess *session.Session, kind, modelName string) (tea.Cmd, context.CancelFunc, chan agentRunEvent) {
	events := make(chan agentRunEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := func() tea.Msg {
		go func() {
			defer cancel()
			answer, usage, err := agent.AskWithEvents(ctx, sess, prompt, func(event llm.AgentEvent) {
				events <- agentRunEvent{event: event}
			})
			events <- agentRunEvent{done: &agentMsg{answer: answer, usage: usage, err: err, kind: kind, model: modelName}}
			close(events)
		}()
		return agentPollMsg{}
	}
	return cmd, cancel, events
}

func (m model) retryAsk(agent *llm.Agent, sess *session.Session, kind, modelName string) (tea.Cmd, context.CancelFunc, chan agentRunEvent) {
	events := make(chan agentRunEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := func() tea.Msg {
		go func() {
			defer cancel()
			answer, usage, err := agent.RetryWithEvents(ctx, sess, func(event llm.AgentEvent) {
				events <- agentRunEvent{event: event}
			})
			events <- agentRunEvent{done: &agentMsg{answer: answer, usage: usage, err: err, kind: kind, model: modelName}}
			close(events)
		}()
		return agentPollMsg{}
	}
	return cmd, cancel, events
}

func pollAgentEvents(events chan agentRunEvent) tea.Cmd {
	return func() tea.Msg {
		if events == nil {
			return agentPollMsg{}
		}
		select {
		case item, ok := <-events:
			if !ok {
				return agentPollMsg{}
			}
			if item.done != nil {
				return *item.done
			}
			return agentProgressMsg{event: item.event, events: events}
		case <-time.After(120 * time.Millisecond):
			return agentPollMsg{}
		}
	}
}

func uiTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return uiTickMsg{}
	})
}

func (m *model) appendAgentEvent(event llm.AgentEvent) bool {
	if event.Kind == "thinking" && isNoiseThinking(event.Text) {
		return false
	}
	if event.Kind != "retry" && event.Kind != "timeout" && event.Kind != "thinking_start" && m.activeRetryStatus != "" {
		m.activeRetryStatus = ""
		if strings.HasPrefix(m.status, "Retry for ") {
			m.status = m.agentRunningStatus()
		}
	}
	switch event.Kind {
	case "thinking_delta":
		m.reconcileTransientToolResult()
		delta := sanitizeThinkingDelta(m.streamThinkText, event.Text)
		if delta == "" {
			return false
		}
		m.streamThinkText += delta
		m.pendingThinkSpin = false
		m.updatePendingThinking(minorBlock("Thinking", m.streamThinkText, 8) + "\n")
	case "thinking":
		m.reconcileTransientToolResult()
		text := sanitizeThinkingText(event.Text)
		if text == "" {
			return false
		}
		if m.streamThinkText != "" {
			m.updatePendingThinking(minorBlock("Thinking", text, 8) + "\n")
		} else {
			m.replacePendingThinking(minorBlock("Thinking", text, 8) + "\n")
		}
		m.pendingThinkSpin = false
		m.streamThinkText = ""
	case "thinking_start":
		m.reconcileTransientToolResult()
		line := "\n" + mutedStyle.Render(spinnerFrame(m.spinner)+" Thinking...") + "\n"
		m.pendingThinkLine = line
		m.pendingThinkSpin = true
		m.streamThinkText = ""
		m.pendingThinkStart = len(m.content)
		m.appendLog(line)
	case "assistant":
		m.reconcileTransientToolResult()
		thinkingAlreadyRendered := m.finalizePendingThinking()
		m.streamThinkText = ""
		m.pendingThinkSpin = false
		m.pendingThinkLine = ""
		m.pendingThinkStart = -1
		if !thinkingAlreadyRendered {
			m.emitFinalThinking()
		}
		m.invalidateWrap(); m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
		m.refreshLog(false)
		m.pendingAnswerStart = -1
		m.streamAnswerText = ""
	case "assistant_delta":
		m.reconcileTransientToolResult()
		m.appendAnswerDelta(event.Text)
	case "usage":
		if m.applyUsageEvent(event.Usage) {
			return true
		}
	case "retry":
		text := strings.TrimSpace(event.Text)
		if text == "" {
			text = "Retrying"
		}
		m.activeRetryStatus = text
		m.status = text
	case "timeout":
		text := strings.TrimSpace(event.Text)
		if text == "" {
			text = "timeout"
		}
		m.activeTimeoutStatus = "Timeout: " + text
		m.status = m.activeTimeoutStatus
	case "tool_start":
		m.reconcileTransientToolResult()
		m.finalizePendingThinking()
		m.finalizeStreamAnswer("")
		m.streamAnswerText = ""
		line := "\n" + toolRunStyle.Render(spinnerFrame(m.spinner)+" Tool called") + ": " + event.Text + "\n"
		m.pendingToolLine = line
		m.pendingToolName = event.Text
		m.pendingToolStart = len(m.content)
		m.appendLog(line)
	case "tool_result":
		replacement := "\n" + successStyle.Render("● Tool result") + ": " + mutedStyle.Render(m.pendingToolName) + minorResult(event.Text, 8) + "\n"
		m.replacePendingTool(replacement)
		m.transientToolLine = replacement
	case "tool_error":
		replacement := "\n" + errorStyle.Render("● Tool failed") + ": " + mutedStyle.Render(m.pendingToolName) + minorResult(event.Text, 10) + "\n"
		m.replacePendingTool(replacement)
		m.transientToolLine = replacement
	default:
		m.appendLog("\n" + event.Display() + "\n")
	}
	return false
}

func (m *model) applyUsageEvent(next *types.Usage) bool {
	if next == nil {
		return false
	}
	deltaPrompt := next.PromptTokens - m.activeTurnUsage.PromptTokens
	deltaCompletion := next.CompletionTokens - m.activeTurnUsage.CompletionTokens
	deltaCacheHit := next.PromptCacheHitTokens - m.activeTurnUsage.PromptCacheHitTokens
	if deltaPrompt < 0 {
		deltaPrompt = next.PromptTokens
	}
	if deltaCompletion < 0 {
		deltaCompletion = next.CompletionTokens
	}
	if deltaCacheHit < 0 {
		deltaCacheHit = next.PromptCacheHitTokens
	}
	m.usageStats.TotalInput += int64(deltaPrompt)
	m.usageStats.TotalOutput += int64(deltaCompletion)
	m.usageStats.TotalCacheHit += int64(deltaCacheHit)
	m.usageStats.SessionInput += int64(deltaPrompt)
	m.usageStats.SessionOutput += int64(deltaCompletion)
	m.usageStats.SessionCacheHit += int64(deltaCacheHit)
	m.latestTotalTokens = next.TotalTokens
	m.activeTurnUsage = *next
	return m.shouldAutoCompact(next.TotalTokens)
}

func (m *model) shouldAutoCompact(totalTokens int) bool {
	if m.activeRunKind != "agent" || m.pendingCompact || totalTokens <= 0 || m.ctx.Root.ContextWindow <= 0 {
		return false
	}
	threshold := m.ctx.Root.AutoCompactThresholdPercent
	if threshold <= 0 {
		threshold = 80
	}
	return totalTokens*100 >= m.ctx.Root.ContextWindow*threshold
}

func (m *model) replacePendingTool(replacement string) {
	if m.pendingToolStart >= 0 && m.pendingToolStart <= len(m.content) && strings.HasPrefix(m.content[m.pendingToolStart:], m.pendingToolLine) {
		m.invalidateWrapFrom(m.pendingToolStart)
		m.content = m.content[:m.pendingToolStart] + replacement + m.content[m.pendingToolStart+len(m.pendingToolLine):]
		m.pendingToolLine = ""
		m.pendingToolName = ""
		m.pendingToolStart = -1
		m.refreshLog(false)
		return
	}
	if m.pendingToolLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingToolLine); idx >= 0 {
			m.invalidateWrapFrom(idx)
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingToolLine):]
			m.pendingToolLine = ""
			m.pendingToolName = ""
			m.pendingToolStart = -1
			m.refreshLog(false)
			return
		}
	}
	m.appendLog(replacement)
	m.pendingToolLine = ""
	m.pendingToolName = ""
	m.pendingToolStart = -1
}

func (m *model) reconcileTransientToolResult() {
	if m.transientToolLine == "" || m.session == nil {
		return
	}
	if !sessionHasRecentToolResult(m.session) {
		return
	}
	// Tool result already rendered incrementally via replacePendingTool.
	// Just clear the transient marker — no full re-render needed.
	m.transientToolLine = ""
	m.pendingToolLine = ""
	m.pendingToolName = ""
	m.pendingToolStart = -1
}

func sessionHasRecentToolResult(sess *session.Session) bool {
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		switch sess.Messages[i].Role {
		case "tool":
			return true
		case "user":
			return false
		}
	}
	return false
}

func (m *model) appendAnswerDelta(delta string) {
	if delta == "" {
		return
	}
	if m.pendingAnswerStart < 0 {
		m.appendLog("\n" + agentStyle.Render("Asayn") + ":\n")
		m.pendingAnswerStart = len(m.content)
		m.streamAnswerText = ""
	}
	m.streamAnswerText += delta
	m.appendLog(delta)
}

func (m *model) finalizeStreamAnswer(final string) {
	if m.pendingAnswerStart < 0 {
		return
	}
	m.pendingAnswerStart = -1
	m.streamAnswerText = ""
	m.invalidateWrap(); m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
	m.refreshLog(false)
}

func (m *model) replacePendingThinking(replacement string) {
	if m.pendingThinkLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingThinkLine); idx >= 0 {
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingThinkLine):]
			m.pendingThinkLine = replacement
			m.refreshLog(false)
			return
		}
	}
	m.pendingThinkLine = replacement
	m.pendingThinkSpin = false
	m.appendLog(replacement)
}

func (m *model) updatePendingThinking(replacement string) {
	if m.pendingThinkLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingThinkLine); idx >= 0 {
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingThinkLine):]
			m.pendingThinkLine = replacement
			m.refreshLog(false)
			return
		}
	}
	m.pendingThinkLine = replacement
	m.appendLog(replacement)
}

func (m *model) clearPendingThinking() {
	if m.pendingThinkLine == "" {
		m.pendingThinkStart = -1
		return
	}
	if idx := strings.LastIndex(m.content, m.pendingThinkLine); idx >= 0 {
		m.content = m.content[:idx] + m.content[idx+len(m.pendingThinkLine):]
		m.pendingThinkLine = ""
		m.pendingThinkSpin = false
		m.streamThinkText = ""
		m.pendingThinkStart = -1
		m.refreshLog(false)
		return
	}
	m.pendingThinkLine = ""
	m.pendingThinkSpin = false
	m.streamThinkText = ""
	m.pendingThinkStart = -1
}

func (m *model) truncatePendingThinking() {
	if m.pendingThinkStart < 0 {
		return
	}
	m.content = m.content[:m.pendingThinkStart]
	m.pendingThinkStart = -1
	m.refreshLog(false)
}

func (m *model) finalizePendingThinking() bool {
	if m.pendingThinkLine == "" {
		m.pendingThinkStart = -1
		return false
	}
	rendered := !m.pendingThinkSpin && strings.Contains(m.pendingThinkLine, "Thinking:")
	if !rendered {
		m.clearPendingThinking()
		return false
	}
	m.pendingThinkLine = ""
	m.pendingThinkSpin = false
	m.pendingThinkStart = -1
	m.streamThinkText = ""
	return true
}

func (m *model) emitFinalThinking() {
	msgs := m.session.Messages
	if len(msgs) == 0 {
		return
	}
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || last.ReasoningContent == "" || isNoiseThinking(last.ReasoningContent) {
		return
	}
	block := minorBlock("Thinking", last.ReasoningContent, 8) + "\n"
	if strings.HasSuffix(strings.TrimRight(m.content, "\n"), strings.TrimRight(block, "\n")) {
		return
	}
	m.appendLog(block)
}

func (m *model) refreshPendingSpinners() {
	changed := false
	if m.pendingThinkLine != "" && m.pendingThinkSpin {
		next := "\n" + mutedStyle.Render(spinnerFrame(m.spinner)+" Thinking...") + "\n"
		if m.pendingThinkStart >= 0 && m.pendingThinkStart < len(m.content) && strings.HasPrefix(m.content[m.pendingThinkStart:], m.pendingThinkLine) {
			m.content = m.content[:m.pendingThinkStart] + next + m.content[m.pendingThinkStart+len(m.pendingThinkLine):]
			m.pendingThinkLine = next
			changed = true
		}
	}
	if m.pendingToolLine != "" && m.pendingToolName != "" {
		next := "\n" + toolRunStyle.Render(spinnerFrame(m.spinner)+" Tool called") + ": " + m.pendingToolName + "\n"
		if m.pendingToolStart >= 0 && m.pendingToolStart < len(m.content) && strings.HasPrefix(m.content[m.pendingToolStart:], m.pendingToolLine) {
			m.content = m.content[:m.pendingToolStart] + next + m.content[m.pendingToolStart+len(m.pendingToolLine):]
			m.pendingToolLine = next
			changed = true
		}
	}
	if changed {
		m.refreshLog(false)
	}
}

func (m model) handleResumePickerKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.resumePicker = false
		m.resumeItems = nil
		m.resumeSelected = 0
		m.status = "ready"
		return m, nil, true
	case "up":
		if len(m.resumeItems) > 0 {
			m.resumeSelected--
			if m.resumeSelected < 0 {
				m.resumeSelected = len(m.resumeItems) - 1
			}
		}
		return m, nil, true
	case "down":
		if len(m.resumeItems) > 0 {
			m.resumeSelected++
			if m.resumeSelected >= len(m.resumeItems) {
				m.resumeSelected = 0
			}
		}
		return m, nil, true
	case "enter":
		if len(m.resumeItems) == 0 {
			return m, nil, true
		}
		idx := m.resumeSelected
		if idx < 0 || idx >= len(m.resumeItems) {
			idx = 0
		}
		next, cmd := m.resumeSession(m.resumeItems[idx].ID)
		next.resumePicker = false
		next.resumeItems = nil
		next.resumeSelected = 0
		return next, cmd, true
	}
	return m, nil, true
}

func (m model) handleRootAgentPickerKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.rootAgentPicker = false
		m.rootAgentItems = nil
		m.rootAgentSelected = 0
		m.status = "ready"
		return m, nil, true
	case "up":
		if len(m.rootAgentItems) > 0 {
			m.rootAgentSelected--
			if m.rootAgentSelected < 0 {
				m.rootAgentSelected = len(m.rootAgentItems) - 1
			}
		}
		return m, nil, true
	case "down":
		if len(m.rootAgentItems) > 0 {
			m.rootAgentSelected++
			if m.rootAgentSelected >= len(m.rootAgentItems) {
				m.rootAgentSelected = 0
			}
		}
		return m, nil, true
	case "enter":
		if len(m.rootAgentItems) == 0 {
			return m, nil, true
		}
		idx := m.rootAgentSelected
		if idx < 0 || idx >= len(m.rootAgentItems) {
			idx = 0
		}
		next := m.applyRootAgent(m.rootAgentItems[idx].Name)
		next.rootAgentPicker = false
		next.rootAgentItems = nil
		next.rootAgentSelected = 0
		return next, nil, true
	}
	return m, nil, true
}

func (m model) handleCommand(raw string) (model, tea.Cmd) {
	parts := strings.Fields(raw)
	cmd := strings.TrimPrefix(parts[0], "/")
	arg := strings.TrimSpace(strings.TrimPrefix(raw, parts[0]))
	switch cmd {
	case "help":
		m.setCommandOutput(helpText())
	case "new":
		_ = m.cleanupEmptySession()
		name := arg
		sess, err := m.ctx.Sessions.New(name, m.ctx.Root.Name)
		if err != nil {
			m.setCommandOutput("error: " + err.Error())
			return m, nil
		}
		m.session = sess
		m.subViewID = ""
		m.commandOutput = ""
		m.inputHistory = nil
		m.historyIndex = -1
		m.historyDraft = ""
		m.ctx.Tools.RestoreSubAgents(sess, nil, m.ctx.SubSessions)
		m.content = ""
		m.refreshLog(true)
	case "resume":
		_ = m.cleanupEmptySession()
		if arg == "" {
			return m.startResumePicker()
		}
		return m.resumeSession(arg)
	case "retry":
		if m.thinking {
			m.setCommandOutput("cannot retry while an agent turn is running")
			return m, nil
		}
		if !hasRetryableUserRequest(m.session) {
			m.setCommandOutput("no previous user request to retry")
			return m, nil
		}
		return m.startRetryTurn()
	case "rename":
		if arg == "" {
			m.setCommandOutput("usage: /rename [name]")
			return m, nil
		}
		if err := m.ctx.Sessions.Rename(m.session, arg); err != nil {
			m.setCommandOutput("error: " + err.Error())
		} else {
			m.setCommandOutput("renamed current session to " + arg)
		}
	case "fork":
		name := arg
		if name == "" {
			name = m.session.Name + "-fork"
		}
		sess, err := m.ctx.Sessions.Fork(m.session, name)
		if err != nil {
			m.setCommandOutput("error: " + err.Error())
			return m, nil
		}
		m.session = sess
		m.ctx.Tools.RestoreSubAgents(sess, sess.SubAgents, m.ctx.SubSessions)
		m.setCommandOutput("forked current session: " + sess.ID)
	case "root_agent", "model":
		if arg == "" {
			return m.startRootAgentPicker()
		}
		m = m.applyRootAgent(arg)
	case "model_config":
		return m.startModelConfigPicker()
	case "compact":
		return m.requestCompact(false)
	case "btw":
		m.setCommandOutput("/" + cmd + " is reserved in this MVP; side-channel questions are not implemented yet.")
	case "exit":
		m.shutdownActiveWork()
		_ = m.cleanupEmptySession()
		return m, tea.Quit
	default:
		m.setCommandOutput("unknown command: /" + cmd)
	}
	return m, nil
}

func (m model) startRootAgentPicker() (model, tea.Cmd) {
	items, err := config.ListAgentInfos(m.ctx.Paths, config.RootAgentKind)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.rootAgentItems = items
	m.rootAgentSelected = 0
	for i, item := range items {
		if item.Name == m.session.RootAgent {
			m.rootAgentSelected = i
			break
		}
	}
	m.rootAgentPicker = true
	m.status = "select root agent"
	return m, nil
}

func (m model) applyRootAgent(name string) model {
	root, err := config.LoadAgent(m.ctx.Paths, config.RootAgentKind, name)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m
	}
	// resolve context window / max output from api config
	limits := config.ModelLimitsFor(m.ctx.API, root.Provider, root.Model)
	root.ContextWindow = limits.ContextWindow
	root.MaxOutputTokens = limits.MaxOutputTokens

	m.ctx.Root = root
	m.ctx.Tools.SetAgentLimits(root.MaxOutputLines, root.AllowParallelShell, root.AllowInteractiveShell)
	m.ctx.Agent = llm.NewAgent(m.ctx.API, root, m.ctx.Paths, m.ctx.Tools)
	m.session.RootAgent = root.Name
	m.ctx.Agent.RefreshSystemPrompt(m.session)
	_ = m.ctx.Sessions.Save(m.session)
	m.setCommandOutput("root_agent set to " + root.Name)
	return m
}

func (m model) startModelConfigPicker() (model, tea.Cmd) {
	roots, err := config.ListAgentInfos(m.ctx.Paths, config.RootAgentKind)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	subs, err := config.ListAgentInfos(m.ctx.Paths, config.SubAgentKind)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	specials, err := config.ListAgentInfos(m.ctx.Paths, config.SpecialAgentKind)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}

	m.modelConfigAgents = append([]config.AgentInfo(nil), roots...)
	m.modelConfigAgentKinds = make([]string, len(roots))
	for i := range roots {
		m.modelConfigAgentKinds[i] = "root"
	}
	m.modelConfigAgents = append(m.modelConfigAgents, subs...)
	for range subs {
		m.modelConfigAgentKinds = append(m.modelConfigAgentKinds, "sub")
	}
	m.modelConfigAgents = append(m.modelConfigAgents, specials...)
	for range specials {
		m.modelConfigAgentKinds = append(m.modelConfigAgentKinds, "special")
	}

	skills, err := config.ListSkills(m.ctx.Paths)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.skillItems = skills

	m.modelConfigModels = nil
	providers := m.ctx.API.Providers
	pNames := make([]string, 0, len(providers))
	for k := range providers {
		pNames = append(pNames, k)
	}
	sort.Strings(pNames)
	for _, pn := range pNames {
		p := providers[pn]
		for _, mName := range p.AllowedModels {
			m.modelConfigModels = append(m.modelConfigModels, fmt.Sprintf("%s (%s)", mName, pn))
		}
	}

	m.modelConfigPicker = true
	m.modelConfigAgentSelected = 0
	m.modelConfigEditingPercent = false
	m.modelConfigPercentDraft = ""
	for i, agent := range m.modelConfigAgents {
		if m.modelConfigAgentKinds[i] == "root" && agent.Name == m.session.RootAgent {
			m.modelConfigAgentSelected = i
			break
		}
	}
	m.modelConfigOptionSelected = 0
	m.status = "model config"
	return m, nil
}

func (m model) handleModelConfigPickerKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	if m.modelConfigEditingPercent {
		return m.handleModelConfigPercentEditKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.modelConfigPicker = false
		m.modelConfigEditingPercent = false
		m.modelConfigPercentDraft = ""
		m.status = "ready"
		return m, nil, true
	case "left":
		if len(m.modelConfigAgents) > 0 {
			m.modelConfigAgentSelected--
			if m.modelConfigAgentSelected < 0 {
				m.modelConfigAgentSelected = len(m.modelConfigAgents) - 1
			}
			m.modelConfigOptionSelected = 0
		}
		return m, nil, true
	case "right":
		if len(m.modelConfigAgents) > 0 {
			m.modelConfigAgentSelected++
			if m.modelConfigAgentSelected >= len(m.modelConfigAgents) {
				m.modelConfigAgentSelected = 0
			}
			m.modelConfigOptionSelected = 0
		}
		return m, nil, true
	case "up":
		m.modelConfigOptionSelected--
		maxOptions := 7 + len(m.skillItems)
		if m.modelConfigOptionSelected < 0 {
			m.modelConfigOptionSelected = maxOptions - 1
		}
		return m, nil, true
	case "down":
		m.modelConfigOptionSelected++
		maxOptions := 7 + len(m.skillItems)
		if m.modelConfigOptionSelected >= maxOptions {
			m.modelConfigOptionSelected = 0
		}
		return m, nil, true
	case " ", "enter":
		return m.handleModelConfigAction(), nil, true
	}
	return m, nil, true
}

func (m model) handleModelConfigPercentEditKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.modelConfigEditingPercent = false
		m.modelConfigPercentDraft = ""
		return m, nil, true
	case "enter":
		value, err := strconv.Atoi(strings.TrimSpace(m.modelConfigPercentDraft))
		if err != nil || value < 5 || value > 95 {
			m.setCommandOutput("auto compact threshold must be a number from 5 to 95")
			m.modelConfigEditingPercent = false
			m.modelConfigPercentDraft = ""
			return m, nil, true
		}
		m.modelConfigEditingPercent = false
		m.modelConfigPercentDraft = ""
		return m.saveModelConfigThreshold(value), nil, true
	case "backspace", "ctrl+h":
		if len(m.modelConfigPercentDraft) > 0 {
			m.modelConfigPercentDraft = m.modelConfigPercentDraft[:len(m.modelConfigPercentDraft)-1]
		}
		return m, nil, true
	}
	raw := msg.String()
	if len(raw) == 1 && raw[0] >= '0' && raw[0] <= '9' && len(m.modelConfigPercentDraft) < 2 {
		m.modelConfigPercentDraft += raw
	}
	return m, nil, true
}

func (m model) handleModelConfigAction() model {
	agentInfo := m.modelConfigAgents[m.modelConfigAgentSelected]
	kind := m.modelConfigAgentKinds[m.modelConfigAgentSelected]
	configKind := config.RootAgentKind
	if kind == "sub" {
		configKind = config.SubAgentKind
	} else if kind == "special" {
		configKind = config.SpecialAgentKind
	}

	if m.modelConfigOptionSelected == 5 {
		if configKind == config.RootAgentKind {
			cfg, err := config.LoadAgent(m.ctx.Paths, configKind, agentInfo.Name)
			if err != nil {
				m.setCommandOutput("error loading agent: " + err.Error())
				return m
			}
			m.modelConfigEditingPercent = true
			m.modelConfigPercentDraft = strconv.Itoa(cfg.AutoCompactThresholdPercent)
		}
		return m
	}

	update := func(cfg *config.AgentConfig) {
		switch m.modelConfigOptionSelected {
		case 0: // Model
			if len(m.modelConfigModels) > 0 {
				current := fmt.Sprintf("%s (%s)", cfg.Model, cfg.Provider)
				idx := -1
				for i, mod := range m.modelConfigModels {
					if mod == current {
						idx = i
						break
					}
				}
				idx = (idx + 1) % len(m.modelConfigModels)
				next := m.modelConfigModels[idx]
				parts := strings.Split(next, " (")
				cfg.Model = parts[0]
				cfg.Provider = strings.TrimSuffix(parts[1], ")")
				limits := config.ModelLimitsFor(m.ctx.API, cfg.Provider, cfg.Model)
				cfg.ContextWindow = limits.ContextWindow
				cfg.MaxOutputTokens = limits.MaxOutputTokens
			}
		case 1: // Thinking Enabled
			cfg.ThinkingEnabled = !cfg.ThinkingEnabled
		case 2: // Reasoning Effort
			efforts := []string{"low", "medium", "high", "xhigh", "max"}
			idx := -1
			for i, e := range efforts {
				if e == cfg.ReasoningEffort {
					idx = i
					break
				}
			}
			idx = (idx + 1) % len(efforts)
			cfg.ReasoningEffort = efforts[idx]
		case 3: // Parallel Shell
			if configKind == config.RootAgentKind {
				cfg.AllowParallelShell = !cfg.AllowParallelShell
				if !cfg.AllowParallelShell {
					cfg.AllowInteractiveShell = false
				}
			}
		case 4: // Interactive Shell
			if configKind == config.RootAgentKind {
				cfg.AllowInteractiveShell = !cfg.AllowInteractiveShell
				if cfg.AllowInteractiveShell {
					cfg.AllowParallelShell = true
				}
			}
		case 6: // Real-time Context Control
			if configKind == config.RootAgentKind {
				cfg.RealTimeContextControl = !cfg.RealTimeContextControl
			}
		default: // Skills
			skillIdx := m.modelConfigOptionSelected - 7
			if skillIdx >= 0 && skillIdx < len(m.skillItems) {
				skillName := m.skillItems[skillIdx].Name
				found := -1
				for i, s := range cfg.VisibleSkills {
					if s == skillName {
						found = i
						break
					}
				}
				if found >= 0 {
					cfg.VisibleSkills = append(cfg.VisibleSkills[:found], cfg.VisibleSkills[found+1:]...)
				} else {
					cfg.VisibleSkills = append(cfg.VisibleSkills, skillName)
				}
				sort.Strings(cfg.VisibleSkills)
			}
		}
	}

	newCfg, err := config.SaveAgent(m.ctx.Paths, configKind, agentInfo.Name, update)
	if err != nil {
		m.setCommandOutput("error saving agent: " + err.Error())
		return m
	}

	if configKind == config.RootAgentKind && newCfg.Name == m.session.RootAgent {
		m.ctx.Root = newCfg
		m.ctx.Tools.SetAgentLimits(newCfg.MaxOutputLines, newCfg.AllowParallelShell, newCfg.AllowInteractiveShell)
		m.ctx.Agent = llm.NewAgent(m.ctx.API, newCfg, m.ctx.Paths, m.ctx.Tools)
		m.ctx.Agent.RefreshSystemPrompt(m.session)
	}

	return m
}

func (m model) saveModelConfigThreshold(value int) model {
	if len(m.modelConfigAgents) == 0 {
		return m
	}
	agentInfo := m.modelConfigAgents[m.modelConfigAgentSelected]
	kind := m.modelConfigAgentKinds[m.modelConfigAgentSelected]
	if kind != "root" {
		return m
	}
	newCfg, err := config.SaveAgent(m.ctx.Paths, config.RootAgentKind, agentInfo.Name, func(cfg *config.AgentConfig) {
		cfg.AutoCompactThresholdPercent = value
	})
	if err != nil {
		m.setCommandOutput("error saving agent: " + err.Error())
		return m
	}
	if newCfg.Name == m.session.RootAgent {
		m.ctx.Root = newCfg
		m.ctx.Tools.SetAgentLimits(newCfg.MaxOutputLines, newCfg.AllowParallelShell, newCfg.AllowInteractiveShell)
		m.ctx.Agent = llm.NewAgent(m.ctx.API, newCfg, m.ctx.Paths, m.ctx.Tools)
		m.ctx.Agent.RefreshSystemPrompt(m.session)
	}
	return m
}

func (m model) modelConfigPickerView() string {
	if len(m.modelConfigAgents) == 0 {
		return "\n no agents found"
	}

	agentInfo := m.modelConfigAgents[m.modelConfigAgentSelected]
	kind := m.modelConfigAgentKinds[m.modelConfigAgentSelected]

	configKind := config.RootAgentKind
	if kind == "sub" {
		configKind = config.SubAgentKind
	} else if kind == "special" {
		configKind = config.SpecialAgentKind
	}

	cfg, err := config.LoadAgent(m.ctx.Paths, configKind, agentInfo.Name)
	if err != nil {
		return "\n error loading agent: " + err.Error()
	}

	rows := []string{
		fmt.Sprintf("< %s (%s) >", cfg.Name, kind),
		"",
	}

	options := []string{
		fmt.Sprintf("Model: %s (%s)", cfg.Model, cfg.Provider),
		fmt.Sprintf("Thinking: %s", checkbox(cfg.ThinkingEnabled)),
		fmt.Sprintf("Reasoning: %s", cfg.ReasoningEffort),
	}

	if kind == "root" {
		thresholdText := fmt.Sprintf("%d%%", cfg.AutoCompactThresholdPercent)
		if m.modelConfigEditingPercent {
			thresholdText = m.modelConfigPercentDraft
			if thresholdText == "" {
				thresholdText = "_"
			}
		}
		options = append(options,
			fmt.Sprintf("Parallel Shell: %s", checkbox(cfg.AllowParallelShell)),
			fmt.Sprintf("Interactive Shell: %s", checkbox(cfg.AllowInteractiveShell)),
			fmt.Sprintf("Auto Compact Threshold: %s", thresholdText),
			fmt.Sprintf("Real-time Context Control (beta): %s %s", checkbox(cfg.RealTimeContextControl), mutedStyle.Render("may significantly reduce cache hit rates")),
		)
	} else {
		options = append(options,
			mutedStyle.Render("Parallel Shell: n/a"),
			mutedStyle.Render("Interactive Shell: n/a"),
			mutedStyle.Render("Auto Compact Threshold: n/a"),
			mutedStyle.Render("Real-time Context Control (beta): n/a"),
		)
	}

	for i, opt := range options {
		marker := "  "
		if i == m.modelConfigOptionSelected {
			marker = "> "
		}
		rows = append(rows, marker+opt)
	}

	rows = append(rows, "", "Skills:")
	for i, skill := range m.skillItems {
		marker := "  "
		if i+7 == m.modelConfigOptionSelected {
			marker = "> "
		}
		checked := "[ ]"
		for _, s := range cfg.VisibleSkills {
			if s == skill.Name {
				checked = "[x]"
				break
			}
		}
		rows = append(rows, fmt.Sprintf("%s%s %-15s %s", marker, checked, skill.Name, mutedStyle.Render(skill.Description)))
	}

	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("model config", rows), m.log.Width))
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func checkbox(b bool) string {
	if b {
		return "[x]"
	}
	return "[ ]"
}

func (m model) startResumePicker() (model, tea.Cmd) {
	items, err := m.ctx.Sessions.List()
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.resumeItems = items
	m.resumeSelected = 0
	m.resumePicker = true
	m.status = "select session"
	return m, nil
}

func (m model) resumeSession(idOrName string) (model, tea.Cmd) {
	sess, err := m.ctx.Sessions.Load(idOrName)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.session = sess
	m.subViewID = ""
	m.commandOutput = ""
	m.inputHistory = append([]string(nil), sess.InputHistory...)
	m.historyIndex = -1
	m.historyDraft = ""
	m.ctx.Tools.RestoreSubAgents(sess, sess.SubAgents, m.ctx.SubSessions)
	m.invalidateWrap(); m.content = renderSessionContent(m.ctx, sess, m.renderer, m.log.Width)
	m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)
	m.latestTotalTokens = sess.LastTotalTokens
	m.refreshLog(true)
	m.status = "ready"
	return m, nil
}

func hasRetryableUserRequest(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" && strings.TrimSpace(sess.Messages[i].Content) != "" {
			return true
		}
	}
	return false
}

func (m model) assistView() string {
	if m.resumePicker {
		return m.resumePickerView()
	}
	if m.rootAgentPicker {
		return m.rootAgentPickerView()
	}
	if m.modelConfigPicker {
		return m.modelConfigPickerView()
	}
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		if m.thinking {
			return m.runningAssistView()
		}
		if strings.TrimSpace(m.commandOutput) != "" {
			return m.commandOutputView()
		}
		if strings.TrimSpace(m.input.Value()) == "" {
			return m.idleAssistView()
		}
		return ""
	}
	selected := m.clampedCommandSelected(len(suggestions))
	rows := []string{"up/down select, tab complete, enter run; no suggestions: up/down history"}
	start := selected - 5
	if start < 0 {
		start = 0
	}
	end := start + 6
	if end > len(suggestions) {
		end = len(suggestions)
		start = end - 6
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < end; i++ {
		item := suggestions[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		rows = append(rows, fmt.Sprintf("%s %-12s %s", marker, item.Name, item.Description))
	}
	if start > 0 {
		rows = append([]string{rows[0], fmt.Sprintf("  ... %d previous", start)}, rows[1:]...)
	}
	if end < len(suggestions) {
		rows = append(rows, fmt.Sprintf("  ... %d more", len(suggestions)-end))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("commands", rows), m.log.Width))
}

func (m model) assistHeight() int {
	if m.session == nil && !m.thinking && strings.TrimSpace(m.commandOutput) == "" && len(m.commandSuggestions()) == 0 {
		return 6
	}
	view := strings.TrimSuffix(m.assistView(), "\n")
	if view == "" {
		return 1
	}
	return strings.Count(view, "\n") + 1
}

func (m model) commandOutputView() string {
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("command output", strings.Split(m.commandOutput, "\n")), m.log.Width))
}

func (m model) idleAssistView() string {
	rows := []string{"Type /help for commands."}
	if m.width < 100 {
		rows = append(rows,
			"session: "+m.session.Name+" ("+m.session.ID+")",
			"workspace: "+m.ctx.Paths.WorkspaceRoot,
			"root_agent: "+m.session.RootAgent,
		)
	}
	if m.lastTurnDuration > 0 {
		rows = append(rows, "Worked for "+formatTurnDuration(m.lastTurnDuration))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
}

func (m *model) setCommandOutput(text string) {
	m.commandOutput = strings.TrimSpace(text)
}

func (m model) runningAssistView() string {
	rows := []string{fmt.Sprintf("running: enter queues message, esc interrupts current turn; queued=%d", len(m.queuedMessages))}
	if len(m.queuedMessages) > 0 {
		rows[0] = fmt.Sprintf("running: enter queues message, esc cancels last queued message; queued=%d", len(m.queuedMessages))
		rows = append(rows, queuedMessageRows(m.queuedMessages, m.log.Width)...)
	}
	if !m.activeTurnStartedAt.IsZero() {
		rows = append(rows, "Working("+formatTurnDuration(time.Since(m.activeTurnStartedAt))+")")
	}
	if m.activeRetryStatus != "" {
		rows = append(rows, m.activeRetryStatus)
	}
	if m.activeTimeoutStatus != "" {
		rows = append(rows, m.activeTimeoutStatus)
	} else if timeout := m.activeProviderIdleTimeout(); timeout > 0 {
		rows = append(rows, "Timeout if idle for "+formatTurnDuration(timeout))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("status", rows), m.log.Width))
}

func queuedMessageRows(messages []string, width int) []string {
	if len(messages) == 0 {
		return nil
	}
	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}
	last := messages[len(messages)-1]
	line := fmt.Sprintf("queued (%d): %s", len(messages), truncateDisplayLine(oneLine(last), contentWidth-15))
	return []string{line}
}

func (m model) activeProviderIdleTimeout() time.Duration {
	if m.ctx == nil {
		return 0
	}
	providerName := m.ctx.Root.Provider
	if m.activeRunKind == "compact" {
		if cfg, err := config.LoadAgent(m.ctx.Paths, config.SpecialAgentKind, "compact_agent"); err == nil && cfg.Provider != "" {
			providerName = cfg.Provider
		}
	}
	provider, ok := m.ctx.API.Providers[providerName]
	if !ok || provider.TimeoutSeconds <= 0 {
		return 120 * time.Second
	}
	return time.Duration(provider.TimeoutSeconds) * time.Second
}

func formatTurnDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%dm %ds", totalSeconds/60, totalSeconds%60)
}

func (m model) rootAgentPickerView() string {
	rows := []string{"root_agent: up/down select, enter apply, esc cancel"}
	if len(m.rootAgentItems) == 0 {
		rows = append(rows, "  no root agents found")
		return "\n" + lipgloss.NewStyle().
			Width(m.log.Width).
			Foreground(lipgloss.Color("8")).
			Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
	}

	selected := m.rootAgentSelected
	if selected < 0 || selected >= len(m.rootAgentItems) {
		selected = 0
	}
	start := selected - 3
	if start < 0 {
		start = 0
	}
	end := start + 8
	if end > len(m.rootAgentItems) {
		end = len(m.rootAgentItems)
	}
	for i := start; i < end; i++ {
		item := m.rootAgentItems[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		current := " "
		if item.Name == m.session.RootAgent {
			current = "*"
		}
		rows = append(rows, fmt.Sprintf("%s %s %-20s %-9s %s", marker, current, item.Name, item.Source, item.Description))
	}
	if len(m.rootAgentItems) > end {
		rows = append(rows, fmt.Sprintf("  ... %d more", len(m.rootAgentItems)-end))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("root agents", rows), m.log.Width))
}

func (m model) resumePickerView() string {
	rows := []string{"resume: up/down select, enter resume, esc cancel"}
	if len(m.resumeItems) == 0 {
		rows = append(rows, "  no saved sessions")
		return "\n" + lipgloss.NewStyle().
			Width(m.log.Width).
			Foreground(lipgloss.Color("8")).
			Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
	}

	selected := m.resumeSelected
	if selected < 0 || selected >= len(m.resumeItems) {
		selected = 0
	}
	start := selected - 3
	if start < 0 {
		start = 0
	}
	end := start + 8
	if end > len(m.resumeItems) {
		end = len(m.resumeItems)
	}
	for i := start; i < end; i++ {
		item := m.resumeItems[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		id := item.ID
		if len(id) > 8 {
			id = id[:8]
		}
		rows = append(rows, fmt.Sprintf("%s %-8s %-20s %s", marker, id, item.Name, item.UpdatedAt.Format("2006-01-02 15:04")))
	}
	if len(m.resumeItems) > end {
		rows = append(rows, fmt.Sprintf("  ... %d more", len(m.resumeItems)-end))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
}

func (m model) commandSuggestions() []commandSpec {
	return commandSuggestionsFor(m.input.Value())
}

func commandSuggestionsFor(raw string) []commandSpec {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "/") || strings.Contains(value, " ") || strings.Contains(value, "\t") {
		return nil
	}
	type scoredCommand struct {
		item  commandSpec
		score int
	}
	scored := []scoredCommand{}
	for _, item := range commands {
		if score, ok := fuzzyCommandScore(item.Name, value); ok {
			scored = append(scored, scoredCommand{item: item, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score < scored[j].score
	})
	out := make([]commandSpec, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.item)
	}
	return out
}

func fuzzyCommandScore(name, raw string) (int, bool) {
	query := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "/"))
	target := strings.ToLower(strings.TrimPrefix(name, "/"))
	if query == "" {
		return 0, true
	}
	pos := 0
	prev := -1
	score := len(target) - len(query)
	for _, q := range query {
		found := -1
		for i, r := range target[pos:] {
			if r == q {
				found = pos + i
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		if prev < 0 {
			score += found * 10
		} else {
			gap := found - prev - 1
			score += gap * 5
			if gap == 0 {
				score -= 3
			}
		}
		if found == 0 || target[found-1] == '_' {
			score -= 2
		}
		prev = found
		pos = found + 1
	}
	return score, true
}

func (m *model) clampCommandSelection() {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		m.commandSelected = 0
		return
	}
	if m.commandSelected >= len(suggestions) {
		m.commandSelected = len(suggestions) - 1
	}
	if m.commandSelected < 0 {
		m.commandSelected = 0
	}
}

func (m model) clampedCommandSelected(length int) int {
	if length <= 0 {
		return 0
	}
	if m.commandSelected < 0 {
		return 0
	}
	if m.commandSelected >= length {
		return length - 1
	}
	return m.commandSelected
}

func (m *model) completeCommand(name string) {
	m.input.SetValue(name)
	m.input.CursorEnd()
	m.syncInputSize()
	m.commandSelected = 0
}

func (m *model) addInputHistory(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != value {
		m.inputHistory = append(m.inputHistory, value)
		m.session.InputHistory = append([]string(nil), m.inputHistory...)
		_ = m.ctx.Sessions.Save(m.session)
	}
	m.historyIndex = -1
	m.historyDraft = ""
}

func (m *model) navigateInputHistory(direction string) bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	switch direction {
	case "up":
		if m.historyIndex == -1 {
			m.historyDraft = m.input.Value()
			m.historyIndex = len(m.inputHistory) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		}
	case "down":
		if m.historyIndex == -1 {
			return false
		}
		m.historyIndex++
		if m.historyIndex >= len(m.inputHistory) {
			m.historyIndex = -1
			m.input.SetValue(m.historyDraft)
			m.input.CursorEnd()
			m.syncInputSize()
			m.historyDraft = ""
			return true
		}
	default:
		return false
	}
	m.input.SetValue(m.inputHistory[m.historyIndex])
	m.input.CursorEnd()
	m.syncInputSize()
	return true
}

func (m *model) restoreHistoryDraft() {
	if m.historyIndex == -1 {
		return
	}
	m.input.SetValue(m.historyDraft)
	m.input.CursorEnd()
	m.syncInputSize()
	m.resetHistoryNavigation()
}

func (m *model) resetHistoryNavigation() {
	m.historyIndex = -1
	m.historyDraft = ""
}

func isInputEditingKey(key string) bool {
	switch key {
	case "up", "down", "enter", "tab", "esc", "ctrl+c":
		return false
	default:
		return true
	}
}

func (m model) selectedCommandForEnter(value string) (string, bool) {
	if strings.Contains(value, " ") || strings.Contains(value, "\t") || exactCommand(value) {
		return "", false
	}
	suggestions := commandSuggestionsFor(value)
	if len(suggestions) == 0 {
		return "", false
	}
	return suggestions[m.clampedCommandSelected(len(suggestions))].Name, true
}

func exactCommand(value string) bool {
	for _, item := range commands {
		if item.Name == value {
			return true
		}
	}
	return false
}

func (m *model) sidebar(width int) string {
	if m.subViewID != "" {
		return m.subAgentSidebar(width)
	}
	key := m.computeSidebarKey()
	if key != m.sidebarCacheKey || m.sidebarCache == "" {
		lines, _ := m.rootSidebarLines(width)
		m.sidebarCache = lipgloss.NewStyle().
			Width(width).
			Height(m.height).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			PaddingLeft(1).
			Render(strings.Join(lines, "\n"))
		m.sidebarCacheKey = key
	}
	return m.sidebarCache
}

func (m *model) computeSidebarKey() string {
	status := m.status
	if m.thinking {
		status = "thinking"
	}
	subs := m.ctx.Tools.SubAgentSnapshots()
	shells := m.ctx.Tools.ShellSnapshots()
	// Build a compact fingerprint of mutable sidebar state
	var b strings.Builder
	b.WriteString(status)
	b.WriteString(fmt.Sprint(m.spinner % 4))
	b.WriteString(fmt.Sprint(len(m.queuedMessages)))
	b.WriteString(fmt.Sprint(m.latestTotalTokens))
	for _, s := range subs {
		b.WriteString(s.ID)
		b.WriteString(s.Status)
	}
	for _, sh := range shells {
		b.WriteString(sh.ID)
		b.WriteString(sh.Status)
	}
	return b.String()
}

func (m model) rootSidebarLines(width int) ([]string, []int) {
	contentWidth := width - 3
	if contentWidth < 10 {
		contentWidth = width
	}
	status := m.status
	if m.thinking {
		status = spinnerFrame(m.spinner) + " " + status
	}
	rawLines := []string{
		"",
		"Asayn",
		"agent skills are all you need",
		"",
		sectionStyle.Render("Session"),
		"name: " + m.session.Name,
		"session id: " + m.session.ID,
		"",
		sectionStyle.Render("Root Agent"),
		"name: " + m.session.RootAgent,
		"model: " + m.ctx.Root.Model + " (" + m.ctx.Root.Provider + ")",
		"status: " + status,
	}
	if len(m.queuedMessages) > 0 {
		rawLines = append(rawLines, "queued: " + fmt.Sprint(len(m.queuedMessages)))
	}
	rawLines = append(rawLines, "", sectionStyle.Render("Root Terminals"))
	shells := m.ctx.Tools.ShellSnapshots()
	if len(shells) == 0 {
		rawLines = append(rawLines, "none")
	} else {
		for _, sh := range shells {
			short := shortID(sh.ID)
			rawLines = append(rawLines, fmt.Sprintf("%s %s %s", statusDot(sh.Status, m.spinner), short, sidebarSingleLine(sh.Command)))
		}
	}
	rawLines = append(rawLines, "", sectionStyle.Render("Sub-agents"))
	subs := m.ctx.Tools.SubAgentSnapshots()
	subAgentRawIndexes := make([]int, 0, len(subs))
	if len(subs) == 0 {
		rawLines = append(rawLines, "none")
	} else {
		for _, sub := range subs {
			short := sub.ID
			if len(short) > 8 {
				short = short[:8]
			}
			label := sub.Name
			if sub.Agent != "" && sub.Agent != sub.Name {
				label = sub.Agent + "/" + sub.Name
			}
			if reason := subAgentFailureReason(sub); reason != "" {
				label += " failed: " + reason
			}
			row := fmt.Sprintf("%s %s %s", statusDot(sub.Status, m.spinner), short, label)
			rawLines = append(rawLines, row)
			subAgentRawIndexes = append(subAgentRawIndexes, len(rawLines)-1)
		}
	}

	rawLines = append(rawLines, "", sectionStyle.Render("Usage Statistics"))
	rawLines = append(rawLines, "Global:")
	rawLines = append(rawLines, fmt.Sprintf("  In: %s  Out: %s", usage.FormatTokens(m.usageStats.TotalInput), usage.FormatTokens(m.usageStats.TotalOutput)))
	hitRate := 0.0
	if m.usageStats.TotalInput > 0 {
		hitRate = float64(m.usageStats.TotalCacheHit) / float64(m.usageStats.TotalInput) * 100
	}
	rawLines = append(rawLines, fmt.Sprintf("  Hit: %s (%.1f%%)", usage.FormatTokens(m.usageStats.TotalCacheHit), hitRate))

	rawLines = append(rawLines, "Current Session:")
	rawLines = append(rawLines, fmt.Sprintf("  In: %s  Out: %s", usage.FormatTokens(m.usageStats.SessionInput), usage.FormatTokens(m.usageStats.SessionOutput)))
	sessHitRate := 0.0
	if m.usageStats.SessionInput > 0 {
		sessHitRate = float64(m.usageStats.SessionCacheHit) / float64(m.usageStats.SessionInput) * 100
	}
	rawLines = append(rawLines, fmt.Sprintf("  Hit: %s (%.1f%%)", usage.FormatTokens(m.usageStats.SessionCacheHit), sessHitRate))

	rawLines = append(rawLines, "", sectionStyle.Render("Context Window"))
	rawLines = append(rawLines, renderProgressBar(m.latestTotalTokens, m.ctx.Root.ContextWindow, m.ctx.Root.MaxOutputTokens, contentWidth))
	rawLines = append(rawLines, fmt.Sprintf("%s / %s", usage.FormatTokens(int64(m.latestTotalTokens)), usage.FormatTokens(int64(m.ctx.Root.ContextWindow))))

	lines := make([]string, 0, len(rawLines))
	subRows := make([]int, 0, len(subAgentRawIndexes))
	nextSubAgent := 0
	for rawIdx, line := range rawLines {
		if nextSubAgent < len(subAgentRawIndexes) && rawIdx == subAgentRawIndexes[nextSubAgent] {
			subRows = append(subRows, len(lines))
			nextSubAgent++
		}
		lines = appendWrappedSidebarLine(lines, line, contentWidth)
	}
	return lines, subRows
}

func appendWrappedSidebarLine(lines []string, line string, width int) []string {
	wrapped := wrapANSI(line, width)
	parts := strings.Split(wrapped, "\n")
	if len(parts) == 0 {
		return append(lines, "")
	}
	return append(lines, parts...)
}

func sidebarSingleLine(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return "-"
	}
	return text
}

func renderProgressBar(current, max, reserve, width int) string {
	if max <= 0 {
		max = 1
	}
	if width < 10 {
		width = 10
	}
	barWidth := width - 2
	filled := int(float64(current) / float64(max) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	empty := barWidth - filled

	isWarning := current >= (max - reserve)

	barStyle := successStyle
	if isWarning {
		barStyle = errorStyle
	}

	bar := barStyle.Render(strings.Repeat("█", filled)) + mutedStyle.Render(strings.Repeat("░", empty))
	return "[" + bar + "]"
}

func (m model) subAgentSidebar(width int) string {
	snap, ok := m.subAgentSnapshot(m.subViewID)
	if !ok {
		return lipgloss.NewStyle().Width(width).Height(m.height).Border(lipgloss.NormalBorder(), false, false, false, true).PaddingLeft(1).Render("sub-agent not found")
	}
	cfg, _ := config.LoadAgent(m.ctx.Paths, config.SubAgentKind, snap.Agent)
	if cfg.Name == "" {
		cfg.Name = snap.Agent
	}
	lines := []string{
		sectionStyle.Render("Sub-agent"),
		"basic tool view",
		"",
		"status: " + snap.Status,
	}
	if reason := subAgentFailureReason(snap); reason != "" {
		lines = append(lines, "reason: "+reason)
	}
	lines = append(lines,
		"session: "+snap.Name,
		"session id: "+snap.SessionID,
		"agent: "+snap.Agent,
		"description: "+oneLine(cfg.Description),
		"system prompt:",
	)
	lines = append(lines, indentedSummary(cfg.SystemPrompt, 3)...)
	lines = append(lines,
		"",
		sectionStyle.Render("Note"),
		"user cannot directly chat with sub-agent",
		"Esc returns to root conversation",
	)
	return lipgloss.NewStyle().
		Width(width).
		Height(m.height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func (m model) subAgentView() string {
	for _, snap := range m.ctx.Tools.SubAgentSnapshots() {
		if snap.ID != m.subViewID {
			continue
		}
		if snap.Status == "completed" || snap.Status == "failed" {
			if snap.SessionID != "" {
				if sess, err := m.ctx.SubSessions.LoadByID(snap.SessionID); err == nil && len(sess.Messages) > 0 {
					body := mutedStyle.Render("Sub-agent conversation. User cannot directly chat with this sub-agent; root agent controls follow-ups.")
					body += "\n" + mutedStyle.Render("Esc returns to root conversation.") + "\n"
					body += renderSessionContent(m.ctx, sess, m.renderer, m.log.Width)
					return lipgloss.NewStyle().
						Width(m.log.Width).
						Height(m.log.Height).
						Render(wrapANSI(body, m.log.Width))
				}
			}
		}
		lines := []string{
			fmt.Sprintf("Sub-agent: %s", snap.Name),
			fmt.Sprintf("id: %s", snap.ID),
			fmt.Sprintf("status: %s", snap.Status),
		}
		if reason := subAgentFailureReason(snap); reason != "" {
			lines = append(lines, fmt.Sprintf("reason: %s", reason))
		}
		lines = append(lines,
			"Read-only. User cannot directly chat with this sub-agent.",
			"Esc returns to root conversation.",
			"",
		)
		contentStart := len(lines)
		lines = append(lines, snap.Transcript...)
		if len(snap.Transcript) == 0 && snap.Result != "" {
			lines = append(lines, snap.Result)
		}
		for i := contentStart; i < len(lines); i++ {
			lines[i] = styleTranscriptLine(lines[i])
		}
		return lipgloss.NewStyle().
			Width(m.log.Width).
			Height(m.log.Height).
			Render(wrapANSI(strings.Join(lines, "\n"), m.log.Width))
	}
	return "sub-agent not found\nEsc returns to root conversation."
}

func (m model) subAgentSnapshot(id string) (tools.SubAgentSnapshot, bool) {
	for _, snap := range m.ctx.Tools.SubAgentSnapshots() {
		if snap.ID == id {
			return snap, true
		}
	}
	return tools.SubAgentSnapshot{}, false
}

func subAgentFailureReason(snap tools.SubAgentSnapshot) string {
	if snap.Status != "failed" {
		return ""
	}
	reason := strings.TrimSpace(snap.Result)
	reason = strings.TrimPrefix(reason, "sub-agent error:")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "unknown"
	}
	return oneLine(reason)
}

func (m model) effectiveSkillVisible(name string) bool {
	return containsString(m.ctx.Root.VisibleSkills, name)
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func renderSessionContent(ctx *app.Context, sess *session.Session, renderer *glamour.TermRenderer, width int) string {
	var b strings.Builder
	toolLabels := map[string]string{}
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "system":
			continue
		case "user":
			b.WriteString("\n")
			b.WriteString(userStyle.Render("You"))
			b.WriteString(":\n")
			b.WriteString(msg.Content)
			b.WriteString("\n")
		case "assistant":
			if msg.ReasoningContent != "" && !isNoiseThinking(msg.ReasoningContent) {
				if thinking := sanitizeThinkingText(msg.ReasoningContent); thinking != "" {
					b.WriteString(minorBlock("Thinking", thinking, 8))
					b.WriteString("\n")
				}
			}
			if msg.Content != "" {
				b.WriteString("\n")
				b.WriteString(agentStyle.Render("Asayn"))
				b.WriteString(":\n")
				if renderer != nil {
					out, _ := renderer.Render(msg.Content)
					b.WriteString(strings.TrimRight(out, "\n"))
				} else {
					b.WriteString(msg.Content)
				}
				b.WriteString("\n")
				b.WriteString(renderDivider(width, ""))
				b.WriteString("\n")
			}
			for _, call := range msg.ToolCalls {
				label := call.Function.Name
				if call.Function.Arguments != "" {
					label += "(" + call.Function.Arguments + ")"
				}
				toolLabels[call.ID] = label
			}
		case "tool":
			label := toolLabels[msg.ToolCallID]
			if label == "" {
				label = msg.ToolCallID
			}
			b.WriteString("\n")
			if strings.HasPrefix(strings.TrimSpace(msg.Content), "tool error:") {
				b.WriteString(errorStyle.Render("● Tool failed"))
			} else {
				b.WriteString(successStyle.Render("● Tool result"))
			}
			if label != "" {
				b.WriteString(": ")
				b.WriteString(mutedStyle.Render(label))
			}
			b.WriteString("\n")
			b.WriteString(minorResult(msg.Content, 8))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func isNoiseThinking(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "requesting model response")
}

func sanitizeThinkingDelta(existing, delta string) string {
	if strings.TrimSpace(delta) == "" {
		return ""
	}
	var out strings.Builder
	lastSpace := endsWithSpace(existing)
	for _, r := range delta {
		if unicode.IsSpace(r) {
			if !lastSpace {
				out.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		out.WriteRune(r)
		lastSpace = false
	}
	return out.String()
}

func sanitizeThinkingText(text string) string {
	return strings.TrimSpace(sanitizeThinkingDelta("", text))
}

func endsWithSpace(text string) bool {
	for i := len(text); i > 0; {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		if r == utf8.RuneError && size == 0 {
			return false
		}
		if unicode.IsSpace(r) {
			return true
		}
		return false
	}
	return false
}

func spinnerFrame(n int) string {
	frames := []string{"◐", "◓", "◑", "◒"}
	return frames[n%len(frames)]
}

func statusDot(status string, spinner int) string {
	switch status {
	case "running":
		return toolRunStyle.Render(spinnerFrame(spinner))
	case "completed":
		return successStyle.Render("●")
	default:
		return errorStyle.Render("●")
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "-"
	}
	return id
}

func oneLine(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return "-"
	}
	if len(text) > 80 {
		return text[:77] + "..."
	}
	return text
}

func truncateDisplayLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	var out strings.Builder
	used := 0
	limit := width - 1
	if limit < 1 {
		limit = width
	}
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			j := i + 1
			if j < len(line) && line[j] == '[' { // CSI
				j++
				for j < len(line) && (line[j] < 0x40 || line[j] > 0x7E) {
					j++
				}
				if j < len(line) {
					j++
				}
			} else {
				for j < len(line) {
					b := line[j]
					j++
					if b >= 0x40 && b <= 0x7E {
						break
					}
				}
			}
			out.WriteString(line[i:j])
			i = j
			continue
		}
		r, size := rune(line[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(line[i:])
		}
		token := line[i : i+size]
		w := lipgloss.Width(token)
		if used+w > limit {
			break
		}
		out.WriteString(token)
		used += w
		i += size
	}
	out.WriteString("…")
	out.WriteString("\x1b[0m")
	return out.String()
}

func indentedSummary(text string, maxLines int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"  -"}
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "...")
	}
	for i := range lines {
		lines[i] = "  " + oneLine(lines[i])
	}
	return lines
}

func panelBlock(title string, rows []string) string {
	rows = append([]string{sectionStyle.Render(title)}, rows...)
	return strings.Join(rows, "\n")
}

func styleTranscriptLine(line string) string {
	switch {
	case strings.HasPrefix(line, "thinking:"):
		if isNoiseThinking(strings.TrimPrefix(line, "thinking:")) {
			return ""
		}
		return thinkingStyle.Render(line)
	case strings.HasPrefix(line, "tool:"):
		return toolRunStyle.Render(line)
	case strings.HasPrefix(line, "tool result:"):
		return successStyle.Render(line)
	case strings.HasPrefix(line, "tool error:"):
		return errorStyle.Render(line)
	case strings.HasPrefix(line, "assistant:"):
		return agentStyle.Render(line)
	default:
		return line
	}
}

func minorBlock(title, text string, maxLines int) string {
	return mutedStyle.Render(title+":") + "\n" + mutedStyle.Render(summarizeIndented(compactDisplayText(text), maxLines))
}

func answerBlock(text string) string {
	return "\n" + agentStyle.Render("Asayn") + ":\n" + text + "\n"
}

func minorResult(text string, maxLines int) string {
	if strings.TrimSpace(text) == "" {
		return mutedStyle.Render("  (no output)")
	}
	return "\n" + mutedStyle.Render(summarizeIndented(text, maxLines))
}

func summarizeIndented(text string, maxLines int) string {
	lines := compactLines(text)
	omitted := 0
	if maxLines > 0 && len(lines) > maxLines {
		omitted = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("  ... %d more lines omitted", omitted))
	}
	return strings.Join(lines, "\n")
}

func compactDisplayText(text string) string {
	lines := compactLines(text)
	return strings.Join(lines, "\n")
}

func compactLines(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n")
	lines := []string{}
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}
	return lines
}

func renderDivider(width int, label string) string {
	if width < 40 {
		width = 80
	}
	if label != "" {
		label = " " + label + " "
	}
	lineLen := width - len(label)
	if lineLen < 20 {
		lineLen = 20
	}
	left := lineLen / 2
	right := lineLen - left
	return mutedStyle.Render(strings.Repeat("─", left) + label + strings.Repeat("─", right))
}

func wrapANSI(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	lineWidth := 0
	lineIndent := ""
	findingIndent := true

	for i := 0; i < len(s); {
		if s[i] == '\n' {
			out.WriteByte('\n')
			lineWidth = 0
			lineIndent = ""
			findingIndent = true
			i++
			continue
		}
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' { // CSI
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
					j++
				}
				if j < len(s) {
					j++
				}
			} else {
				for j < len(s) {
					b := s[j]
					j++
					if b >= 0x40 && b <= 0x7E {
						break
					}
				}
			}
			out.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := rune(s[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		token := s[i : i+size]
		tokenWidth := lipgloss.Width(token)
		if tokenWidth <= 0 {
			out.WriteString(token)
			i += size
			continue
		}

		if findingIndent {
			if token == " " || token == "\t" {
				lineIndent += token
			} else {
				findingIndent = false
			}
		}

		if lineWidth > 0 && lineWidth+tokenWidth > width {
			out.WriteByte('\n')
			out.WriteString(lineIndent)
			lineWidth = lipgloss.Width(lineIndent)
		}
		out.WriteString(token)
		lineWidth += tokenWidth
		i += size
	}
	return out.String()
}

func helpText() string {
	return `
Commands:
/help                 show help
/new [name]           start a new session
/resume [session]     pick or resume saved sessions
/rename [name]        rename current session
/fork [name]          fork from the current point
/root_agent [name]    pick or set root agent
/model_config         pick model, thinking, shell, and skills with left/right + space
/compact              compress prior context with compact_agent
/btw <question>       reserved for future side-channel question
/exit                 exit CLI

Input:
type / then use up/down to select commands; tab completes
with no command suggestions, up/down recalls previous inputs
/resume, /root_agent, and /model_config open interactive pickers
/model_config uses left/right to switch targets such as default(root) and default(sub)
while Asayn is working, enter queues the typed message
while Asayn is working, esc cancels the last queued message, or interrupts the current turn if the queue is empty
`
}
