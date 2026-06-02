package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/session"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	ctx      *app.Context
	session  *session.Session
	input    textinput.Model
	log      viewport.Model
	content  string
	width    int
	height   int
	status   string
	thinking bool
}

type agentMsg struct {
	answer string
	err    error
}

func Run(ctx *app.Context) error {
	sess, err := ctx.Sessions.New("default", ctx.Root.Name)
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
		ctx:      ctx,
		session:  sess,
		input:    input,
		log:      vp,
		content:  content,
		status:   "ready",
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
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
		m.log.Width = msg.Width - sidebar - 2
		m.log.Height = msg.Height - 4
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if value == "" {
				return m, nil
			}
			if strings.HasPrefix(value, "/") {
				next, cmd := m.handleCommand(value)
				return next, cmd
			}
			m.appendLog("\nYou: " + value + "\n")
			m.thinking = true
			m.status = "agent running"
			return m, m.ask(value)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			next := m.handleMouseClick(msg.X, msg.Y)
			return next, nil
		}
	case agentMsg:
		m.thinking = false
		if msg.err != nil {
			m.status = "error"
			m.appendLog("\nAsayn error: " + msg.err.Error() + "\n")
		} else {
			m.status = "ready"
			m.appendLog("\nAsayn: " + msg.answer + "\n")
			_ = m.ctx.Sessions.Save(m.session)
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.log, cmd = m.log.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) handleMouseClick(x, y int) model {
	if m.width < 100 {
		return m
	}
	sidebarLeft := m.width - 30
	if x < sidebarLeft {
		return m
	}
	idx := y - 8
	subs := m.ctx.Tools.SubAgentSnapshots()
	if idx < 0 || idx >= len(subs) {
		return m
	}
	snap := subs[idx]
	m.appendLog("\nSub-agent " + snap.Name + " (" + snap.ID + "):\n" + m.ctx.Tools.SubAgentStatus(snap.ID) + "\n")
	return m
}

func (m model) View() string {
	if m.width == 0 {
		return "Asayn"
	}
	main := lipgloss.NewStyle().Width(m.log.Width).Render(m.log.View()) + "\n" + m.input.View()
	sidebarWidth := 30
	if m.width < 100 {
		return main
	}
	side := m.sidebar(sidebarWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, main, side)
}

func (m *model) appendLog(s string) {
	m.content += s
	m.log.SetContent(m.content)
	m.log.GotoBottom()
}

func (m model) ask(prompt string) tea.Cmd {
	sess := m.session
	agent := m.ctx.Agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		answer, err := agent.Ask(ctx, sess, prompt)
		return agentMsg{answer: answer, err: err}
	}
}

func (m model) handleCommand(raw string) (model, tea.Cmd) {
	parts := strings.Fields(raw)
	cmd := strings.TrimPrefix(parts[0], "/")
	arg := strings.TrimSpace(strings.TrimPrefix(raw, parts[0]))
	switch cmd {
	case "help":
		m.appendLog(helpText())
	case "new":
		name := arg
		if name == "" {
			name = "session"
		}
		sess, err := m.ctx.Sessions.New(name, m.ctx.Root.Name)
		if err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
			return m, nil
		}
		m.session = sess
		m.content = welcome(m.ctx)
		m.log.SetContent(m.content)
	case "resume":
		if arg == "" {
			items, err := m.ctx.Sessions.List()
			if err != nil {
				m.appendLog("\nerror: " + err.Error() + "\n")
				return m, nil
			}
			lines := []string{"\nSessions:"}
			for _, item := range items {
				lines = append(lines, fmt.Sprintf("%s  %s  %s", item.ID, item.Name, item.UpdatedAt.Format(time.RFC3339)))
			}
			m.appendLog(strings.Join(lines, "\n") + "\n")
			return m, nil
		}
		sess, err := m.ctx.Sessions.Load(arg)
		if err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
			return m, nil
		}
		m.session = sess
		m.content = fmt.Sprintf("Resumed %s (%s)\n", sess.Name, sess.ID)
		m.log.SetContent(m.content)
	case "rename":
		if arg == "" {
			m.appendLog("\nusage: /rename [name]\n")
			return m, nil
		}
		if err := m.ctx.Sessions.Rename(m.session, arg); err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
		} else {
			m.appendLog("\nrenamed current session to " + arg + "\n")
		}
	case "fork":
		name := arg
		if name == "" {
			name = m.session.Name + "-fork"
		}
		sess, err := m.ctx.Sessions.Fork(m.session, name)
		if err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
			return m, nil
		}
		m.session = sess
		m.appendLog("\nforked current session: " + sess.ID + "\n")
	case "root_agent":
		if arg == "" {
			names, _ := config.ListAgents(m.ctx.Paths, config.RootAgentKind)
			m.appendLog("\nroot agents: " + strings.Join(names, ", ") + "\nusage: /root_agent [name]\n")
			return m, nil
		}
		root, err := config.LoadAgent(m.ctx.Paths, config.RootAgentKind, arg)
		if err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
			return m, nil
		}
		m.ctx.Root = root
		m.ctx.Agent = llm.NewAgent(m.ctx.API, root, m.ctx.Paths, m.ctx.Tools)
		m.session.RootAgent = root.Name
		_ = m.ctx.Sessions.Save(m.session)
		m.appendLog("\nroot_agent set to " + root.Name + "\n")
	case "skills":
		skills, err := config.ListSkills(m.ctx.Paths)
		if err != nil {
			m.appendLog("\nerror: " + err.Error() + "\n")
			return m, nil
		}
		if len(parts) >= 3 {
			name := parts[1]
			visible := parts[2] == "on" || parts[2] == "true"
			if m.session.VisibleSkills == nil {
				m.session.VisibleSkills = map[string]bool{}
			}
			m.session.VisibleSkills[name] = visible
			_ = m.ctx.Sessions.Save(m.session)
		}
		rows := []string{"\nSkills (/skills [name] on|off):"}
		for _, skill := range skills {
			rows = append(rows, fmt.Sprintf("%s [%s] visible=%v", skill.Name, skill.Source, m.session.VisibleSkills[skill.Name]))
		}
		m.appendLog(strings.Join(rows, "\n") + "\n")
	case "compact", "btw":
		m.appendLog("\n/" + cmd + " is reserved in this MVP; context isolation/compaction is not implemented yet.\n")
	case "exit":
		return m, tea.Quit
	default:
		m.appendLog("\nunknown command: /" + cmd + "\n")
	}
	return m, nil
}

func (m model) sidebar(width int) string {
	lines := []string{
		"Asayn",
		"agent skills are all you need",
		"",
		"session: " + m.session.Name,
		"root: " + m.session.RootAgent,
		"status: " + m.status,
		"",
		"sub agents",
	}
	subs := m.ctx.Tools.SubAgentSummaries()
	if len(subs) == 0 {
		lines = append(lines, "none")
	} else {
		for _, sub := range m.ctx.Tools.SubAgentSnapshots() {
			short := sub.ID
			if len(short) > 8 {
				short = short[:8]
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", short, sub.Status, sub.Name))
		}
	}
	return lipgloss.NewStyle().
		Width(width).
		Height(m.height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func welcome(ctx *app.Context) string {
	return fmt.Sprintf("Asayn - agent skills are all you need\nworkplace: %s\nroot_agent: %s\nType /help for commands.\n", ctx.Paths.Workplace, ctx.Root.Name)
}

func helpText() string {
	return `
Commands:
/help                 show help
/new [name]           start a new session
/resume [session]     list or resume saved sessions
/rename [name]        rename current session
/fork [name]          fork from the current point
/root_agent [name]    list or select root agent
/skills               list skills; /skills [name] on|off toggles visibility
/compact [text]       reserved for future context compression
/btw <question>       reserved for future side-channel question
/exit                 exit CLI
`
}
