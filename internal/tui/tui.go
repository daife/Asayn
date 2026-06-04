package tui

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/llm/usage"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	ctx                       *app.Context
	session                   *session.Session
	input                     textinput.Model
	log                       viewport.Model
	renderer                  *glamour.TermRenderer
	content                   string
	commandOutput             string
	width                     int
	height                    int
	status                    string
	thinking                  bool
	activeCancel              context.CancelFunc
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
	skillItems                []config.Skill
	subViewID                 string
	spinner                   int
	pendingToolLine           string
	pendingToolName           string
	pendingThinkLine          string
	pendingThinkSpin          bool
	streamThinkText           string
	pendingThinkStart         int
	pendingAnswerStart        int
	streamAnswerText          string
	usageStats                usage.Stats
	latestTotalTokens         int
	activeTurnUsage           types.Usage
}

type agentMsg struct {
	answer string
	usage  types.Usage
	err    error
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

var commands = []commandSpec{
	{Name: "/help", Description: "show help"},
	{Name: "/new", Description: "start a new session"},
	{Name: "/resume", Description: "resume a saved session"},
	{Name: "/rename", Description: "rename current session"},
	{Name: "/fork", Description: "fork from the current point"},
	{Name: "/copy_answer", Description: "export and copy latest answer"},
	{Name: "/root_agent", Description: "select root agent"},
	{Name: "/model", Description: "select root agent (alias for /root_agent)"},
	{Name: "/model_config", Description: "configure agent settings"},
	{Name: "/compact", Description: "reserved context compression"},
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

func Run(ctx *app.Context) error {
	sess, err := ctx.Sessions.New("", ctx.Root.Name)
	if err != nil {
		return err
	}
	input := textinput.New()
	input.Placeholder = "message or /help"
	input.Focus()
	input.CharLimit = 8000
	input.Prompt = "› "
	vp := viewport.New(80, 20)
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
		pendingThinkStart:  -1,
		pendingAnswerStart: -1,
	}
	m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)
	m.initRenderer(80)
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

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, uiTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		sidebar := 30
		if msg.Width < 100 {
			sidebar = 0
		}
		m.log.Width = msg.Width - sidebar - 4
		if m.log.Width < 20 {
			m.log.Width = msg.Width
		}
		m.log.Height = msg.Height - 7
		if m.log.Height < 3 {
			m.log.Height = 3
		}
		m.input.Width = m.log.Width - 1
		m.initRenderer(m.log.Width)
		m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
		m.log.SetContent(m.wrapContent(m.content))
	case tea.KeyMsg:
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
				m.appendLog("\n" + mutedStyle.Render(fmt.Sprintf("queued #%d: %s", len(m.queuedMessages), value)) + "\n")
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
		thinkingAlreadyRendered := m.finalizePendingThinking()
		m.streamThinkText = ""
		m.pendingThinkSpin = false
		m.pendingThinkLine = ""
		m.pendingThinkStart = -1

		if msg.err == nil {
			_ = m.ctx.UsageTracker.Log(m.session.ID, m.session.Name, m.ctx.Root.Model, msg.usage)
			m.latestTotalTokens = msg.usage.TotalTokens
		}
		m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)

		if msg.err != nil {
			m.pendingAnswerStart = -1
			m.streamAnswerText = ""
			if errors.Is(msg.err, context.Canceled) || strings.Contains(msg.err.Error(), "context canceled") {
				m.status = "ready"
				m.appendLog("\n" + mutedStyle.Render("interrupted") + "\n")
			} else {
				m.status = "error"
				m.appendLog("\n" + errorStyle.Render("● Asayn error") + ": " + msg.err.Error() + "\n")
			}
			m.appendDivider()
		} else {
			m.status = "ready"
			if !thinkingAlreadyRendered {
				m.emitFinalThinking()
			}
			m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
			m.pendingAnswerStart = -1
			m.streamAnswerText = ""
			m.refreshLog(false)
			m.appendDivider()
			_ = m.ctx.Sessions.Save(m.session)
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
		m.appendAgentEvent(msg.event)
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
	cmds = append(cmds, cmd)
	m.clampCommandSelection()
	m.log, cmd = m.log.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) cleanupEmptySession() error {
	if session.HasContent(m.session) {
		return nil
	}
	return m.ctx.Sessions.Delete(m.session)
}

func (m model) handleMouseClick(x, y int) model {
	if m.width < 100 || m.subViewID != "" {
		return m
	}
	sidebarLeft := m.width - 30
	if x < sidebarLeft {
		return m
	}
	_, subStart, subCount := m.rootSidebarLines(30)
	if subCount == 0 {
		return m
	}
	idx := y - subStart
	subs := m.ctx.Tools.SubAgentSnapshots()
	if idx < 0 || idx >= subCount || idx >= len(subs) {
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
	hasSidebar := m.width >= 100

	mainWidth := m.width
	if hasSidebar {
		mainWidth = m.width - sidebarWidth - 2
	}
	if mainWidth < 20 {
		mainWidth = 20
	}

	body := m.log.View()
	main := lipgloss.JoinVertical(lipgloss.Left,
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
	if m.subViewID != "" {
		m.log.SetContent(m.wrapContent(m.subAgentView()))
	} else {
		m.log.SetContent(m.wrapContent(m.content))
	}
	if forceBottom || wasBottom {
		m.log.GotoBottom()
	}
}

func (m model) wrapContent(content string) string {
	width := m.log.Width
	if width <= 0 {
		return content
	}
	return wrapANSI(content, width)
}

func (m *model) appendDivider() {
	m.appendLog(renderDivider(m.log.Width, "") + "\n")
}

func (m model) startAgentTurn(value string, recordHistory bool) (model, tea.Cmd) {
	m.commandOutput = ""
	m.activeTurnUsage = types.Usage{}
	if recordHistory {
		m.addInputHistory(value)
	}
	prompt := m.withActiveWorkContext(value)
	m.appendLog("\n" + userStyle.Render("You") + ":\n" + prompt + "\n")
	m.log.GotoBottom()
	m.thinking = true
	m.status = m.agentRunningStatus()
	cmd, cancel, events := m.ask(prompt)
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
		removed := m.queuedMessages[len(m.queuedMessages)-1]
		m.queuedMessages = m.queuedMessages[:len(m.queuedMessages)-1]
		m.status = m.agentRunningStatus()
		m.appendLog("\n" + mutedStyle.Render("canceled queued message: "+removed) + "\n")
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

func (m model) ask(prompt string) (tea.Cmd, context.CancelFunc, chan agentRunEvent) {
	sess := m.session
	agent := m.ctx.Agent
	events := make(chan agentRunEvent, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	cmd := func() tea.Msg {
		go func() {
			defer cancel()
			answer, usage, err := agent.AskWithEvents(ctx, sess, prompt, func(event llm.AgentEvent) {
				events <- agentRunEvent{event: event}
			})
			events <- agentRunEvent{done: &agentMsg{answer: answer, usage: usage, err: err}}
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

func (m *model) appendAgentEvent(event llm.AgentEvent) {
	if event.Kind == "thinking" && isNoiseThinking(event.Text) {
		return
	}
	switch event.Kind {
	case "thinking_delta":
		m.streamThinkText += event.Text
		m.pendingThinkSpin = false
		m.updatePendingThinking(minorBlock("Thinking", m.streamThinkText, 8) + "\n")
	case "thinking":
		if m.streamThinkText != "" {
			m.updatePendingThinking(minorBlock("Thinking", event.Text, 8) + "\n")
		} else {
			m.replacePendingThinking(minorBlock("Thinking", event.Text, 8) + "\n")
		}
		m.pendingThinkSpin = false
		m.streamThinkText = ""
	case "thinking_start":
		line := "\n" + mutedStyle.Render(spinnerFrame(m.spinner)+" Thinking...") + "\n"
		m.pendingThinkLine = line
		m.pendingThinkSpin = true
		m.streamThinkText = ""
		m.pendingThinkStart = len(m.content)
		m.appendLog(line)
	case "assistant":
		thinkingAlreadyRendered := m.finalizePendingThinking()
		m.streamThinkText = ""
		m.pendingThinkSpin = false
		m.pendingThinkLine = ""
		m.pendingThinkStart = -1
		if !thinkingAlreadyRendered {
			m.emitFinalThinking()
		}
		m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
		m.refreshLog(false)
		m.pendingAnswerStart = -1
		m.streamAnswerText = ""
	case "assistant_delta":
		m.appendAnswerDelta(event.Text)
	case "usage":
		m.applyUsageEvent(event.Usage)
	case "tool_start":
		m.finalizePendingThinking()
		m.finalizeStreamAnswer("")
		m.streamAnswerText = ""
		line := "\n" + toolRunStyle.Render(spinnerFrame(m.spinner)+" Tool called") + ": " + event.Text + "\n"
		m.pendingToolLine = line
		m.pendingToolName = event.Text
		m.appendLog(line)
	case "tool_result":
		m.replacePendingTool("\n" + successStyle.Render("● Tool result") + ": " + mutedStyle.Render(m.pendingToolName) + minorResult(event.Text, 8) + "\n")
	case "tool_error":
		m.replacePendingTool("\n" + errorStyle.Render("● Tool failed") + ": " + mutedStyle.Render(m.pendingToolName) + minorResult(event.Text, 10) + "\n")
	default:
		m.appendLog("\n" + event.Display() + "\n")
	}
}

func (m *model) applyUsageEvent(next *types.Usage) {
	if next == nil {
		return
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
}

func (m *model) replacePendingTool(replacement string) {
	if m.pendingToolLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingToolLine); idx >= 0 {
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingToolLine):]
			m.pendingToolLine = ""
			m.pendingToolName = ""
			m.refreshLog(false)
			return
		}
	}
	m.appendLog(replacement)
	m.pendingToolName = ""
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
	m.content = renderSessionContent(m.ctx, m.session, m.renderer, m.log.Width)
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
		if idx := strings.LastIndex(m.content, m.pendingThinkLine); idx >= 0 {
			m.content = m.content[:idx] + next + m.content[idx+len(m.pendingThinkLine):]
			m.pendingThinkLine = next
			changed = true
		}
	}
	if m.pendingToolLine != "" && m.pendingToolName != "" {
		next := "\n" + toolRunStyle.Render(spinnerFrame(m.spinner)+" Tool called") + ": " + m.pendingToolName + "\n"
		if idx := strings.LastIndex(m.content, m.pendingToolLine); idx >= 0 {
			m.content = m.content[:idx] + next + m.content[idx+len(m.pendingToolLine):]
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
	case "copy_answer":
		out, err := m.exportLatestAnswer()
		if err != nil {
			m.setCommandOutput("error: " + err.Error())
		} else {
			m.setCommandOutput(out)
		}
	case "root_agent", "model":
		if arg == "" {
			return m.startRootAgentPicker()
		}
		m = m.applyRootAgent(arg)
	case "model_config":
		return m.startModelConfigPicker()
	case "compact", "btw":
		m.setCommandOutput("/" + cmd + " is reserved in this MVP; context isolation/compaction is not implemented yet.")
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

	m.modelConfigAgents = append([]config.AgentInfo(nil), roots...)
	m.modelConfigAgentKinds = make([]string, len(roots))
	for i := range roots {
		m.modelConfigAgentKinds[i] = "root"
	}
	m.modelConfigAgents = append(m.modelConfigAgents, subs...)
	for range subs {
		m.modelConfigAgentKinds = append(m.modelConfigAgentKinds, "sub")
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
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.modelConfigPicker = false
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
		maxOptions := 5 + len(m.skillItems)
		if m.modelConfigOptionSelected < 0 {
			m.modelConfigOptionSelected = maxOptions - 1
		}
		return m, nil, true
	case "down":
		m.modelConfigOptionSelected++
		maxOptions := 5 + len(m.skillItems)
		if m.modelConfigOptionSelected >= maxOptions {
			m.modelConfigOptionSelected = 0
		}
		return m, nil, true
	case " ", "enter":
		return m.handleModelConfigAction(), nil, true
	}
	return m, nil, true
}

func (m model) handleModelConfigAction() model {
	agentInfo := m.modelConfigAgents[m.modelConfigAgentSelected]
	kind := m.modelConfigAgentKinds[m.modelConfigAgentSelected]
	configKind := config.RootAgentKind
	if kind == "sub" {
		configKind = config.SubAgentKind
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
		default: // Skills
			skillIdx := m.modelConfigOptionSelected - 5
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

func (m model) modelConfigPickerView() string {
	if len(m.modelConfigAgents) == 0 {
		return "\n no agents found"
	}

	agentInfo := m.modelConfigAgents[m.modelConfigAgentSelected]
	kind := m.modelConfigAgentKinds[m.modelConfigAgentSelected]

	configKind := config.RootAgentKind
	if kind == "sub" {
		configKind = config.SubAgentKind
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
		options = append(options,
			fmt.Sprintf("Parallel Shell: %s", checkbox(cfg.AllowParallelShell)),
			fmt.Sprintf("Interactive Shell: %s", checkbox(cfg.AllowInteractiveShell)),
		)
	} else {
		options = append(options,
			mutedStyle.Render("Parallel Shell: n/a"),
			mutedStyle.Render("Interactive Shell: n/a"),
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
		if i+5 == m.modelConfigOptionSelected {
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
	m.content = renderSessionContent(m.ctx, sess, m.renderer, m.log.Width)
	m.usageStats, _ = m.ctx.UsageTracker.GetStats(m.session.ID)
	m.refreshLog(true)
	m.status = "ready"
	return m, nil
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
			"workplace: "+m.ctx.Paths.Workplace,
			"root_agent: "+m.session.RootAgent,
		)
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
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("status", rows), m.log.Width))
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
	out := []commandSpec{}
	for _, item := range commands {
		if strings.HasPrefix(item.Name, value) {
			out = append(out, item)
		}
	}
	return out
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
			m.historyDraft = ""
			return true
		}
	default:
		return false
	}
	m.input.SetValue(m.inputHistory[m.historyIndex])
	m.input.CursorEnd()
	return true
}

func (m *model) restoreHistoryDraft() {
	if m.historyIndex == -1 {
		return
	}
	m.input.SetValue(m.historyDraft)
	m.input.CursorEnd()
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

func (m model) sidebar(width int) string {
	if m.subViewID != "" {
		return m.subAgentSidebar(width)
	}
	lines, _, _ := m.rootSidebarLines(width)
	return lipgloss.NewStyle().
		Width(width).
		Height(m.height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func (m model) rootSidebarLines(width int) ([]string, int, int) {
	contentWidth := width - 3
	if contentWidth < 10 {
		contentWidth = width
	}
	status := m.status
	if m.thinking {
		status = spinnerFrame(m.spinner) + " " + status
	}
	lines := []string{
		"Asayn",
		"agent skills are all you need",
		"",
		sectionStyle.Render("Session"),
		"name: " + m.session.Name,
		"session id: " + m.session.ID,
		"",
		sectionStyle.Render("Root Agent"),
		"root agent: " + m.session.RootAgent,
		"description: " + oneLine(m.ctx.Root.Description),
		"system prompt:",
	}
	lines = append(lines, indentedSummary(m.ctx.Root.SystemPrompt, 3)...)
	lines = append(lines,
		fmt.Sprintf("thinking: %s effort=%s", onOff(m.ctx.Root.ThinkingEnabled), m.ctx.Root.ReasoningEffort),
		"status: "+status,
	)
	if len(m.queuedMessages) > 0 {
		lines = append(lines, "queued: "+fmt.Sprint(len(m.queuedMessages)))
	}
	lines = append(lines, "", sectionStyle.Render("Root Terminals"))
	shells := m.ctx.Tools.ShellSnapshots()
	if len(shells) == 0 {
		lines = append(lines, "none")
	} else {
		for _, sh := range shells {
			short := shortID(sh.ID)
			lines = append(lines, fmt.Sprintf("%s %s %s", statusDot(sh.Status, m.spinner), short, oneLine(sh.Command)))
		}
	}
	lines = append(lines, "", sectionStyle.Render("Sub-agents"))
	subStart := len(lines)
	subs := m.ctx.Tools.SubAgentSnapshots()
	if len(subs) == 0 {
		lines = append(lines, "none")
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
			lines = append(lines, fmt.Sprintf("%s %s %s", statusDot(sub.Status, m.spinner), short, label))
		}
	}

	lines = append(lines, "", sectionStyle.Render("Usage Statistics"))
	lines = append(lines, "Global:")
	lines = append(lines, fmt.Sprintf("  In: %s  Out: %s", usage.FormatTokens(m.usageStats.TotalInput), usage.FormatTokens(m.usageStats.TotalOutput)))
	hitRate := 0.0
	if m.usageStats.TotalInput > 0 {
		hitRate = float64(m.usageStats.TotalCacheHit) / float64(m.usageStats.TotalInput) * 100
	}
	lines = append(lines, fmt.Sprintf("  Hit: %s (%.1f%%)", usage.FormatTokens(m.usageStats.TotalCacheHit), hitRate))

	lines = append(lines, "Current Session:")
	lines = append(lines, fmt.Sprintf("  In: %s  Out: %s", usage.FormatTokens(m.usageStats.SessionInput), usage.FormatTokens(m.usageStats.SessionOutput)))
	sessHitRate := 0.0
	if m.usageStats.SessionInput > 0 {
		sessHitRate = float64(m.usageStats.SessionCacheHit) / float64(m.usageStats.SessionInput) * 100
	}
	lines = append(lines, fmt.Sprintf("  Hit: %s (%.1f%%)", usage.FormatTokens(m.usageStats.SessionCacheHit), sessHitRate))

	lines = append(lines, "", sectionStyle.Render("Context Window"))
	lines = append(lines, renderProgressBar(m.latestTotalTokens, m.ctx.Root.ContextWindow, m.ctx.Root.MaxOutputTokens, contentWidth))
	lines = append(lines, fmt.Sprintf("%s / %s", usage.FormatTokens(int64(m.latestTotalTokens)), usage.FormatTokens(int64(m.ctx.Root.ContextWindow))))

	for i := range lines {
		lines[i] = truncateDisplayLine(lines[i], contentWidth)
	}
	return lines, subStart, len(subs)
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
		"read-only view",
		"",
		"status: " + snap.Status,
		"session: " + snap.Name,
		"session id: " + snap.SessionID,
		"agent: " + snap.Agent,
		"description: " + oneLine(cfg.Description),
		"system prompt:",
	}
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
					body := mutedStyle.Render("Sub-agent conversation (read-only). User cannot directly chat with this sub-agent; root agent controls follow-ups.")
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
			"Read-only. User cannot directly chat with this sub-agent.",
			"Esc returns to root conversation.",
			"",
		}
		lines = append(lines, snap.Transcript...)
		if len(snap.Transcript) == 0 && snap.Result != "" {
			lines = append(lines, snap.Result)
		}
		for i := 5; i < len(lines); i++ {
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
				b.WriteString(minorBlock("Thinking", msg.ReasoningContent, 8))
				b.WriteString("\n")
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
/copy_answer          copy latest Asayn answer and write preview files
/root_agent [name]    pick or set root agent
/skills               pick per-agent visible skills with left/right + space
/shell_config         pick root-agent shell tool mode with left/right + space
/think_config         pick per-agent thinking mode/effort with left/right + space
/compact [text]       reserved for future context compression
/btw <question>       reserved for future side-channel question
/exit                 exit CLI

Input:
type / then use up/down to select commands; tab completes
with no command suggestions, up/down recalls previous inputs
/resume, /root_agent, and /skills open interactive pickers
/skills uses left/right to switch targets such as default(root) and default(sub)
while Asayn is working, enter queues the typed message
while Asayn is working, esc cancels the last queued message, or interrupts the current turn if the queue is empty
`
}

func (m model) exportLatestAnswer() (string, error) {
	answer := latestAssistantAnswer(m.session)
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("no assistant answer to copy")
	}
	dir := filepath.Join(m.ctx.Paths.Workplace, ".Asayn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	mdPath := filepath.Join(dir, "latest_answer.md")
	htmlPath := filepath.Join(dir, "latest_answer.html")
	if err := os.WriteFile(mdPath, []byte(answer), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(htmlPath, []byte(answerPreviewHTML(answer)), 0o644); err != nil {
		return "", err
	}
	clipboard := "clipboard: unavailable"
	if err := copyToClipboard(answer); err == nil {
		clipboard = "clipboard: copied"
	}
	return fmt.Sprintf("%s\nmarkdown: %s\npreview: %s", clipboard, mdPath, htmlPath), nil
}

func latestAssistantAnswer(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) != "" {
			return msg.Content
		}
	}
	return ""
}

func copyToClipboard(text string) error {
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
	}
	var lastErr error
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(candidate[0], candidate[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no clipboard command found")
}

func answerPreviewHTML(markdown string) string {
	escaped := html.EscapeString(markdown)
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Asayn Latest Answer</title>
<style>
body{margin:0;font:15px/1.55 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#1f2933;background:#f7f8fa}
header{position:sticky;top:0;display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 20px;border-bottom:1px solid #d9dee7;background:#fff}
main{max-width:920px;margin:0 auto;padding:24px 20px 56px}
button{border:1px solid #1f2933;background:#1f2933;color:#fff;border-radius:6px;padding:8px 12px;font:inherit;cursor:pointer}
pre{white-space:pre-wrap;word-break:break-word;background:#fff;border:1px solid #d9dee7;border-radius:8px;padding:18px;box-shadow:0 1px 2px rgba(0,0,0,.04)}
</style>
</head>
<body>
<header><strong>Asayn latest answer</strong><button id="copy">Copy answer</button></header>
<main><pre id="answer">` + escaped + `</pre></main>
<script>
document.getElementById('copy').addEventListener('click', async () => {
  await navigator.clipboard.writeText(document.getElementById('answer').textContent);
  document.getElementById('copy').textContent = 'Copied';
});
</script>
</body>
</html>
`
}
