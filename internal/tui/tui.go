package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	ctx                 *app.Context
	session             *session.Session
	input               textinput.Model
	log                 viewport.Model
	content             string
	commandOutput       string
	width               int
	height              int
	status              string
	thinking            bool
	activeCancel        context.CancelFunc
	queuedMessages      []string
	agentEvents         chan agentRunEvent
	commandSelected     int
	inputHistory        []string
	historyIndex        int
	historyDraft        string
	resumePicker        bool
	resumeItems         []session.Session
	resumeSelected      int
	rootAgentPicker     bool
	rootAgentItems      []config.AgentInfo
	rootAgentSelected   int
	skillsPicker        bool
	skillTargets        []skillTarget
	skillTargetSelected int
	skillItems          []config.Skill
	skillSelected       int
	shellConfigPicker   bool
	shellConfigItems    []config.AgentInfo
	shellConfigSelected int
	shellConfigOption   int
	subViewID           string
	spinner             int
	pendingToolLine     string
	pendingToolName     string
	pendingThinkLine    string
	pendingThinkSpin    bool
	streamThinkText     string
	pendingThinkStart   int
}

type agentMsg struct {
	answer string
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

type skillTarget struct {
	Kind        string
	Name        string
	Description string
	Source      string
}

var commands = []commandSpec{
	{Name: "/help", Description: "show help"},
	{Name: "/new", Description: "start a new session"},
	{Name: "/resume", Description: "resume a saved session"},
	{Name: "/rename", Description: "rename current session"},
	{Name: "/fork", Description: "fork from the current point"},
	{Name: "/root_agent", Description: "select root agent"},
	{Name: "/skills", Description: "toggle visible skills"},
	{Name: "/shell_config", Description: "configure root-agent shell tools"},
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
	content := welcome(ctx)
	vp.SetContent(content)

	m := model{
		ctx:               ctx,
		session:           sess,
		input:             input,
		log:               vp,
		content:           content,
		status:            "ready",
		historyIndex:      -1,
		pendingThinkStart: -1,
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
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
		m.input.Width = m.log.Width
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
		if m.skillsPicker {
			next, cmd, handled := m.handleSkillsPickerKey(msg)
			if handled {
				return next, cmd
			}
		}
		if m.shellConfigPicker {
			next, cmd, handled := m.handleShellConfigPickerKey(msg)
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
				m.log.SetContent(m.wrapContent(m.content))
				m.log.GotoBottom()
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
		if msg.err != nil {
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
			m.appendLog("\n" + agentStyle.Render("Asayn") + ":\n" + msg.answer + "\n")
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
	if m.subViewID != "" {
		m.log.SetContent(m.wrapContent(m.subAgentView()))
	}
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
	body := m.log.View()
	if m.subViewID != "" {
		vp := m.log
		vp.SetContent(m.wrapContent(m.subAgentView()))
		body = vp.View()
	}
	main := lipgloss.NewStyle().Width(m.log.Width).Height(m.log.Height).Render(body) + "\n" + m.input.View() + m.assistView()
	sidebarWidth := 30
	if m.width < 100 {
		return main
	}
	side := m.sidebar(sidebarWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, main, "  ", side)
}

func (m *model) appendLog(s string) {
	m.content += s
	m.log.SetContent(m.wrapContent(m.content))
	m.log.GotoBottom()
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
	if recordHistory {
		m.addInputHistory(value)
	}
	prompt := m.withActiveWorkContext(value)
	m.appendLog("\n" + userStyle.Render("You") + ":\n" + prompt + "\n")
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
		if sub.Status == "completed" || sub.Status == "stopped" {
			continue
		}
		subRows = append(subRows, fmt.Sprintf("- id=%s status=%s agent=%s name=%s session_id=%s", sub.ID, sub.Status, sub.Agent, sub.Name, sub.SessionID))
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
		shellRows = append(shellRows, fmt.Sprintf("- id=%s status=%s pid=%d age=%s command=%s", sh.ID, sh.Status, sh.PID, sh.Age, sh.Command))
	}
	if len(shellRows) > 0 {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, "Active root-agent terminals:")
		rows = append(rows, shellRows...)
	}
	if len(rows) == 0 {
		return ""
	}
	return "[Asayn active work context]\n" + strings.Join(rows, "\n")
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
			answer, err := agent.AskWithEvents(ctx, sess, prompt, func(event llm.AgentEvent) {
				events <- agentRunEvent{event: event}
			})
			events <- agentRunEvent{done: &agentMsg{answer: answer, err: err}}
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
		m.appendLog("\n" + agentStyle.Render("Asayn") + ":\n" + event.Text + "\n")
	case "tool_start":
		m.clearPendingThinking()
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

func (m *model) replacePendingTool(replacement string) {
	if m.pendingToolLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingToolLine); idx >= 0 {
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingToolLine):]
			m.pendingToolLine = ""
			m.pendingToolName = ""
			m.log.SetContent(m.wrapContent(m.content))
			m.log.GotoBottom()
			return
		}
	}
	m.appendLog(replacement)
	m.pendingToolName = ""
}

func (m *model) replacePendingThinking(replacement string) {
	if m.pendingThinkLine != "" {
		if idx := strings.LastIndex(m.content, m.pendingThinkLine); idx >= 0 {
			m.content = m.content[:idx] + replacement + m.content[idx+len(m.pendingThinkLine):]
			m.pendingThinkLine = replacement
			m.log.SetContent(m.wrapContent(m.content))
			m.log.GotoBottom()
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
			m.log.SetContent(m.wrapContent(m.content))
			m.log.GotoBottom()
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
		m.log.SetContent(m.wrapContent(m.content))
		m.log.GotoBottom()
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
	m.log.SetContent(m.wrapContent(m.content))
	m.log.GotoBottom()
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
		m.log.SetContent(m.wrapContent(m.content))
		m.log.GotoBottom()
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

func (m model) handleSkillsPickerKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.skillsPicker = false
		m.skillItems = nil
		m.skillSelected = 0
		m.status = "ready"
		return m, nil, true
	case "up":
		if len(m.skillItems) > 0 {
			m.skillSelected--
			if m.skillSelected < 0 {
				m.skillSelected = len(m.skillItems) - 1
			}
		}
		return m, nil, true
	case "down":
		if len(m.skillItems) > 0 {
			m.skillSelected++
			if m.skillSelected >= len(m.skillItems) {
				m.skillSelected = 0
			}
		}
		return m, nil, true
	case "left", "right":
		if len(m.skillTargets) > 0 {
			if msg.String() == "left" {
				m.skillTargetSelected--
				if m.skillTargetSelected < 0 {
					m.skillTargetSelected = len(m.skillTargets) - 1
				}
			} else {
				m.skillTargetSelected++
				if m.skillTargetSelected >= len(m.skillTargets) {
					m.skillTargetSelected = 0
				}
			}
			m.status = "select skills for " + m.currentSkillTargetLabel()
		} else {
			m.status = "select skills"
		}
		return m, nil, true
	case " ", "space":
		if len(m.skillItems) == 0 {
			return m, nil, true
		}
		idx := m.skillSelected
		if idx < 0 || idx >= len(m.skillItems) {
			idx = 0
		}
		name := m.skillItems[idx].Name
		m.toggleSkillForCurrentTarget(name)
		return m, nil, true
	}
	return m, nil, true
}

func (m model) handleShellConfigPickerKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		_ = m.cleanupEmptySession()
		return m, tea.Quit, true
	case "esc":
		m.shellConfigPicker = false
		m.shellConfigItems = nil
		m.shellConfigSelected = 0
		m.shellConfigOption = 0
		m.status = "ready"
		return m, nil, true
	case "left":
		if len(m.shellConfigItems) > 0 {
			m.shellConfigSelected--
			if m.shellConfigSelected < 0 {
				m.shellConfigSelected = len(m.shellConfigItems) - 1
			}
		}
		return m, nil, true
	case "right":
		if len(m.shellConfigItems) > 0 {
			m.shellConfigSelected++
			if m.shellConfigSelected >= len(m.shellConfigItems) {
				m.shellConfigSelected = 0
			}
		}
		return m, nil, true
	case "up", "down":
		if msg.String() == "up" {
			m.shellConfigOption--
			if m.shellConfigOption < 0 {
				m.shellConfigOption = 1
			}
		} else {
			m.shellConfigOption++
			if m.shellConfigOption > 1 {
				m.shellConfigOption = 0
			}
		}
		return m, nil, true
	case " ", "space":
		if len(m.shellConfigItems) == 0 {
			return m, nil, true
		}
		return m.toggleShellConfig(), nil, true
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
		m.content = welcome(m.ctx)
		m.log.SetContent(m.wrapContent(m.content))
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
	case "root_agent":
		if arg == "" {
			return m.startRootAgentPicker()
		}
		m = m.applyRootAgent(arg)
	case "skills":
		skills, err := config.ListSkills(m.ctx.Paths)
		if err != nil {
			m.setCommandOutput("error: " + err.Error())
			return m, nil
		}
		if len(parts) == 1 {
			return m.startSkillsPicker(skills)
		}
		if len(parts) >= 3 {
			kind := config.RootAgentKind
			agentName := m.session.RootAgent
			skillIndex := 1
			if parts[1] == "root" || parts[1] == "root_agent" {
				kind = config.RootAgentKind
				skillIndex = 3
			} else if parts[1] == "sub" || parts[1] == "sub_agent" {
				kind = config.SubAgentKind
				skillIndex = 3
			}
			if skillIndex == 3 {
				if len(parts) < 5 {
					m.setCommandOutput("usage: /skills [root|sub] [agent] [skill] on|off")
					return m, nil
				}
				agentName = parts[2]
			}
			if len(parts) <= skillIndex+1 {
				m.setCommandOutput("usage: /skills [root|sub] [agent] [skill] on|off")
				return m, nil
			}
			skillName := parts[skillIndex]
			visible := parts[skillIndex+1] == "on" || parts[skillIndex+1] == "true"
			if err := m.setAgentSkillVisible(kind, agentName, skillName, visible); err != nil {
				m.setCommandOutput("error: " + err.Error())
				return m, nil
			}
		}
		rows := []string{"Skills (/skills opens picker; /skills [root|sub] [agent] [skill] on|off also works):"}
		for _, skill := range skills {
			rows = append(rows, fmt.Sprintf("%s [%s] description=%s", skill.Name, skill.Source, skill.Description))
		}
		m.setCommandOutput(strings.Join(rows, "\n"))
	case "shell_config":
		if len(parts) == 1 {
			return m.startShellConfigPicker()
		}
		if len(parts) != 4 {
			m.setCommandOutput("usage: /shell_config [root_agent] parallel|interactive on|off")
			return m, nil
		}
		enabled := parts[3] == "on" || parts[3] == "true"
		if err := m.setRootAgentShellOption(parts[1], parts[2], enabled); err != nil {
			m.setCommandOutput("error: " + err.Error())
		}
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
	m.ctx.Tools.SetAgentLimits(root.MaxOutputChars, root.AllowParallelShell, root.AllowInteractiveShell)
	m.ctx.Agent = llm.NewAgent(m.ctx.API, root, m.ctx.Paths, m.ctx.Tools)
	m.session.RootAgent = root.Name
	m.ctx.Agent.RefreshSystemPrompt(m.session)
	_ = m.ctx.Sessions.Save(m.session)
	m.setCommandOutput("root_agent set to " + root.Name)
	return m
}

func (m model) startShellConfigPicker() (model, tea.Cmd) {
	items, err := config.ListAgentInfos(m.ctx.Paths, config.RootAgentKind)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.shellConfigItems = items
	m.shellConfigSelected = 0
	for i, item := range items {
		if item.Name == m.session.RootAgent {
			m.shellConfigSelected = i
			break
		}
	}
	m.shellConfigOption = 0
	m.shellConfigPicker = true
	m.status = "configure shell tools"
	return m, nil
}

func (m model) toggleShellConfig() model {
	item, ok := m.currentShellConfigAgent()
	if !ok {
		return m
	}
	cfg, err := config.LoadAgent(m.ctx.Paths, config.RootAgentKind, item.Name)
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m
	}
	switch m.shellConfigOption {
	case 0:
		cfg.AllowParallelShell = !cfg.AllowParallelShell
		if !cfg.AllowParallelShell {
			cfg.AllowInteractiveShell = false
		}
	case 1:
		if cfg.AllowParallelShell {
			cfg.AllowInteractiveShell = !cfg.AllowInteractiveShell
		} else {
			cfg.AllowParallelShell = true
			cfg.AllowInteractiveShell = true
		}
	}
	if err := m.saveRootShellConfig(cfg.Name, cfg.AllowParallelShell, cfg.AllowInteractiveShell); err != nil {
		m.setCommandOutput("error: " + err.Error())
	}
	return m
}

func (m *model) setRootAgentShellOption(agentName, option string, enabled bool) error {
	cfg, err := config.LoadAgent(m.ctx.Paths, config.RootAgentKind, agentName)
	if err != nil {
		return err
	}
	switch option {
	case "parallel", "parallel_shell", "allow_parallel_shell":
		cfg.AllowParallelShell = enabled
		if !enabled {
			cfg.AllowInteractiveShell = false
		}
	case "interactive", "interactive_shell", "allow_interactive_shell":
		cfg.AllowInteractiveShell = enabled
		if enabled {
			cfg.AllowParallelShell = true
		}
	default:
		return fmt.Errorf("unknown shell option %q", option)
	}
	return m.saveRootShellConfig(cfg.Name, cfg.AllowParallelShell, cfg.AllowInteractiveShell)
}

func (m *model) saveRootShellConfig(agentName string, allowParallel, allowInteractive bool) error {
	cfg, err := config.SaveRootAgentShellConfig(m.ctx.Paths, agentName, allowParallel, allowInteractive)
	if err != nil {
		return err
	}
	if cfg.Name == m.session.RootAgent {
		m.ctx.Root = cfg
		m.ctx.Tools.SetAgentLimits(cfg.MaxOutputChars, cfg.AllowParallelShell, cfg.AllowInteractiveShell)
		m.ctx.Agent = llm.NewAgent(m.ctx.API, cfg, m.ctx.Paths, m.ctx.Tools)
		m.ctx.Agent.RefreshSystemPrompt(m.session)
	}
	m.setCommandOutput(fmt.Sprintf("shell_config %s: parallel=%t interactive=%t", cfg.Name, cfg.AllowParallelShell, cfg.AllowInteractiveShell))
	return nil
}

func (m model) startSkillsPicker(skills []config.Skill) (model, tea.Cmd) {
	targets, err := m.listSkillTargets()
	if err != nil {
		m.setCommandOutput("error: " + err.Error())
		return m, nil
	}
	m.skillItems = skills
	m.skillTargets = targets
	m.skillSelected = 0
	m.skillTargetSelected = 0
	for i, target := range targets {
		if target.Kind == config.RootAgentKind && target.Name == m.session.RootAgent {
			m.skillTargetSelected = i
			break
		}
	}
	m.skillsPicker = true
	m.status = "select skills for " + m.currentSkillTargetLabel()
	return m, nil
}

func (m model) listSkillTargets() ([]skillTarget, error) {
	targets := []skillTarget{}
	roots, err := config.ListAgentInfos(m.ctx.Paths, config.RootAgentKind)
	if err != nil {
		return nil, err
	}
	for _, info := range roots {
		targets = append(targets, skillTarget{
			Kind:        config.RootAgentKind,
			Name:        info.Name,
			Description: info.Description,
			Source:      info.Source,
		})
	}
	subs, err := config.ListAgentInfos(m.ctx.Paths, config.SubAgentKind)
	if err != nil {
		return nil, err
	}
	for _, info := range subs {
		targets = append(targets, skillTarget{
			Kind:        config.SubAgentKind,
			Name:        info.Name,
			Description: info.Description,
			Source:      info.Source,
		})
	}
	return targets, nil
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
	m.content = renderSessionContent(m.ctx, sess)
	m.log.SetContent(m.wrapContent(m.content))
	m.log.GotoBottom()
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
	if m.skillsPicker {
		return m.skillsPickerView()
	}
	if m.shellConfigPicker {
		return m.shellConfigPickerView()
	}
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		if m.thinking {
			return m.runningAssistView()
		}
		if strings.TrimSpace(m.commandOutput) != "" {
			return m.commandOutputView()
		}
		return ""
	}
	selected := m.clampedCommandSelected(len(suggestions))
	rows := []string{"up/down select, tab complete, enter run; no suggestions: up/down history"}
	for i, item := range suggestions {
		marker := " "
		if i == selected {
			marker = ">"
		}
		rows = append(rows, fmt.Sprintf("%s %-12s %s", marker, item.Name, item.Description))
		if len(rows) >= 7 {
			break
		}
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

func (m model) skillsPickerView() string {
	rows := []string{fmt.Sprintf("skills: left/right target, up/down select, space toggle, esc close")}
	rows = append(rows, "target: "+m.skillTargetsLine())
	if len(m.skillItems) == 0 {
		rows = append(rows, "  no skills found")
		return "\n" + lipgloss.NewStyle().
			Width(m.log.Width).
			Foreground(lipgloss.Color("8")).
			Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
	}

	selected := m.skillSelected
	if selected < 0 || selected >= len(m.skillItems) {
		selected = 0
	}
	start := selected - 3
	if start < 0 {
		start = 0
	}
	end := start + 8
	if end > len(m.skillItems) {
		end = len(m.skillItems)
	}
	for i := start; i < end; i++ {
		item := m.skillItems[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		checked := "[ ]"
		if m.currentTargetSkillVisible(item.Name) {
			checked = "[x]"
		}
		rows = append(rows, fmt.Sprintf("%s %s %-20s %-28s %s", marker, checked, item.Name, item.Source, item.Description))
	}
	if len(m.skillItems) > end {
		rows = append(rows, fmt.Sprintf("  ... %d more", len(m.skillItems)-end))
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("skills", rows), m.log.Width))
}

func (m model) shellConfigPickerView() string {
	rows := []string{"shell_config: left/right root agent, up/down option, space toggle, esc close"}
	item, ok := m.currentShellConfigAgent()
	if !ok {
		rows = append(rows, "  no root agents found")
		return "\n" + lipgloss.NewStyle().
			Width(m.log.Width).
			Foreground(lipgloss.Color("8")).
			Render(wrapANSI(strings.Join(rows, "\n"), m.log.Width))
	}
	cfg, err := config.LoadAgent(m.ctx.Paths, config.RootAgentKind, item.Name)
	if err != nil {
		rows = append(rows, "  error: "+err.Error())
	} else {
		rows = append(rows, "agent: "+cfg.Name)
		options := []struct {
			Name    string
			Enabled bool
			Note    string
		}{
			{Name: "allow parallel shell", Enabled: cfg.AllowParallelShell, Note: "adds shell_run_async + shell_async_status + shell_async_kill"},
			{Name: "allow interactive shell", Enabled: cfg.AllowInteractiveShell, Note: "requires parallel; adds shell_async_write"},
		}
		for i, option := range options {
			marker := " "
			if i == m.shellConfigOption {
				marker = ">"
			}
			checked := "[ ]"
			if option.Enabled {
				checked = "[x]"
			}
			rows = append(rows, fmt.Sprintf("%s %s %-24s %s", marker, checked, option.Name, option.Note))
		}
	}
	return "\n" + lipgloss.NewStyle().
		Width(m.log.Width).
		Foreground(lipgloss.Color("8")).
		Render(wrapANSI(panelBlock("shell config", rows), m.log.Width))
}

func (m model) currentShellConfigAgent() (config.AgentInfo, bool) {
	if len(m.shellConfigItems) == 0 {
		return config.AgentInfo{}, false
	}
	idx := m.shellConfigSelected
	if idx < 0 || idx >= len(m.shellConfigItems) {
		idx = 0
	}
	return m.shellConfigItems[idx], true
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
	for i := range lines {
		lines[i] = truncateDisplayLine(lines[i], contentWidth)
	}
	return lines, subStart, len(subs)
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
		if snap.Status == "completed" || snap.Status == "failed" || snap.Status == "stopped" {
			if snap.SessionID != "" {
				if sess, err := m.ctx.SubSessions.LoadByID(snap.SessionID); err == nil && len(sess.Messages) > 0 {
					body := mutedStyle.Render("Sub-agent conversation (read-only). User cannot directly chat with this sub-agent; root agent controls follow-ups.")
					body += "\n" + mutedStyle.Render("Esc returns to root conversation.") + "\n"
					body += renderSessionContent(m.ctx, sess)
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

func (m model) currentSkillTarget() (skillTarget, bool) {
	if len(m.skillTargets) == 0 {
		return skillTarget{}, false
	}
	idx := m.skillTargetSelected
	if idx < 0 || idx >= len(m.skillTargets) {
		idx = 0
	}
	return m.skillTargets[idx], true
}

func (m model) currentSkillTargetLabel() string {
	target, ok := m.currentSkillTarget()
	if !ok {
		return "no agent"
	}
	return target.Name + " (" + skillTargetKindLabel(target.Kind) + ")"
}

func (m model) skillTargetsLine() string {
	if len(m.skillTargets) == 0 {
		return "none"
	}
	parts := []string{}
	for i, target := range m.skillTargets {
		label := target.Name + "(" + skillTargetKindLabel(target.Kind) + ")"
		if i == m.skillTargetSelected {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func skillTargetKindLabel(kind string) string {
	if kind == config.SubAgentKind {
		return "sub"
	}
	return "root"
}

func (m model) currentTargetSkillVisible(name string) bool {
	target, ok := m.currentSkillTarget()
	if !ok {
		return false
	}
	cfg, err := config.LoadAgent(m.ctx.Paths, target.Kind, target.Name)
	if err != nil {
		return false
	}
	return containsString(cfg.VisibleSkills, name)
}

func (m *model) toggleSkillForCurrentTarget(skillName string) {
	target, ok := m.currentSkillTarget()
	if !ok {
		return
	}
	visible := !m.currentTargetSkillVisible(skillName)
	if err := m.setAgentSkillVisible(target.Kind, target.Name, skillName, visible); err != nil {
		m.setCommandOutput("error: " + err.Error())
	}
}

func (m *model) setAgentSkillVisible(kind, agentName, skillName string, visible bool) error {
	cfg, err := config.LoadAgent(m.ctx.Paths, kind, agentName)
	if err != nil {
		return err
	}
	next := append([]string(nil), cfg.VisibleSkills...)
	if visible {
		if !containsString(next, skillName) {
			next = append(next, skillName)
		}
	} else {
		filtered := []string{}
		for _, item := range next {
			if item != skillName {
				filtered = append(filtered, item)
			}
		}
		next = filtered
	}
	cfg, err = config.SaveAgentVisibleSkills(m.ctx.Paths, kind, agentName, next)
	if err != nil {
		return err
	}
	if kind == config.RootAgentKind && agentName == m.session.RootAgent {
		m.ctx.Root = cfg
		m.ctx.Tools.SetAgentLimits(cfg.MaxOutputChars, cfg.AllowParallelShell, cfg.AllowInteractiveShell)
		m.ctx.Agent = llm.NewAgent(m.ctx.API, cfg, m.ctx.Paths, m.ctx.Tools)
		m.ctx.Agent.RefreshSystemPrompt(m.session)
	}
	return nil
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func welcome(ctx *app.Context) string {
	return fmt.Sprintf("Asayn - agent skills are all you need\nworkplace: %s\nroot_agent: %s\nType /help for commands.\n", ctx.Paths.Workplace, ctx.Root.Name)
}

func renderSessionContent(ctx *app.Context, sess *session.Session) string {
	var b strings.Builder
	b.WriteString(mutedStyle.Render(fmt.Sprintf("Resumed %s (%s)", sess.Name, sess.ID)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("workplace: %s", ctx.Paths.Workplace)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("root_agent: %s", sess.RootAgent)))
	b.WriteString("\n")
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
				b.WriteString(msg.Content)
				b.WriteString("\n")
				b.WriteString(renderDivider(80, ""))
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
	case "stopped":
		return mutedStyle.Render("●")
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
			for j < len(line) {
				b := line[j]
				j++
				if b >= '@' && b <= '~' {
					break
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
	return mutedStyle.Render(title + ":\n" + summarizeIndented(compactDisplayText(text), maxLines))
}

func minorResult(text string, maxLines int) string {
	if strings.TrimSpace(text) == "" {
		return mutedStyle.Render("  (no output)")
	}
	return mutedStyle.Render("\n" + summarizeIndented(text, maxLines))
}

func summarizeIndented(text string, maxLines int) string {
	lines := strings.Split(strings.Trim(text, "\n"), "\n")
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
	raw := strings.Split(strings.TrimSpace(text), "\n")
	lines := []string{}
	for _, line := range raw {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
	for i := 0; i < len(s); {
		if s[i] == '\n' {
			out.WriteByte('\n')
			lineWidth = 0
			i++
			continue
		}
		if s[i] == '\x1b' {
			j := i + 1
			for j < len(s) {
				b := s[j]
				j++
				if b >= '@' && b <= '~' {
					break
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
		if lineWidth > 0 && lineWidth+tokenWidth > width {
			out.WriteByte('\n')
			lineWidth = 0
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
/skills               pick per-agent visible skills with left/right + space
/shell_config         pick root-agent shell tool mode with left/right + space
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
