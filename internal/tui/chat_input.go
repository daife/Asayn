package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxInputRows = 4

type chatInput struct {
	field        textinput.Model
	width        int
	contentWidth int
	height       int
}

type inputLine struct {
	text       string
	start, end int
}

func newChatInput() chatInput {
	field := textinput.New()
	field.Placeholder = "message or /help"
	field.Prompt = ""
	field.CharLimit = 8000
	field.Focus()

	input := chatInput{
		field:  field,
		width:  80,
		height: 1,
	}
	input.SetWidth(80)
	return input
}

func (i *chatInput) SetWidth(width int) {
	if width <= 0 {
		width = 80
	}
	i.width = width
	i.contentWidth = width - lipgloss.Width(inputPrompt)
	if i.contentWidth < 1 {
		i.contentWidth = 1
	}
	i.field.Width = 0
	i.height = inputDisplayHeight(i.field.Value(), i.contentWidth)
}

func (i chatInput) Height() int {
	if i.height < 1 {
		return 1
	}
	return i.height
}

func (i chatInput) Value() string {
	return i.field.Value()
}

func (i *chatInput) SetValue(value string) {
	i.field.SetValue(value)
	i.height = inputDisplayHeight(i.field.Value(), i.contentWidth)
}

func (i *chatInput) CursorEnd() {
	i.field.CursorEnd()
}

func (i chatInput) Blink() tea.Cmd {
	return i.field.Cursor.BlinkCmd()
}

func (i chatInput) Update(msg tea.Msg) (chatInput, tea.Cmd) {
	var cmd tea.Cmd
	i.field, cmd = i.field.Update(msg)
	i.height = inputDisplayHeight(i.field.Value(), i.contentWidth)
	return i, cmd
}

func (i chatInput) View() string {
	if i.Value() == "" {
		return inputPrompt + i.placeholderView()
	}

	lines := wrappedInputLines(i.Value(), i.contentWidth)
	cursorLine := cursorInputLine(lines, i.field.Position())
	start := cursorLine - i.Height() + 1
	if start < 0 {
		start = 0
	}
	end := start + i.Height()
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for lineIdx := start; lineIdx < end; lineIdx++ {
		if lineIdx > start {
			b.WriteByte('\n')
		}
		if lineIdx == cursorLine {
			b.WriteString(inputPrompt)
			b.WriteString(i.renderCursorLine(lines[lineIdx]))
			continue
		}
		b.WriteString(strings.Repeat(" ", lipgloss.Width(inputPrompt)))
		b.WriteString(lines[lineIdx].text)
	}
	return b.String()
}

func (i chatInput) placeholderView() string {
	placeholder := []rune(i.field.Placeholder)
	if len(placeholder) == 0 {
		i.field.Cursor.SetChar(" ")
		return i.field.Cursor.View()
	}
	i.field.Cursor.TextStyle = i.field.PlaceholderStyle
	i.field.Cursor.SetChar(string(placeholder[0]))
	return i.field.Cursor.View() + i.field.PlaceholderStyle.Render(string(placeholder[1:]))
}

func (i chatInput) renderCursorLine(line inputLine) string {
	value := []rune(i.Value())
	pos := i.field.Position()
	lineRunes := []rune(line.text)
	localPos := pos - line.start
	if localPos < 0 {
		localPos = 0
	}
	if localPos > len(lineRunes) {
		localPos = len(lineRunes)
	}

	var b strings.Builder
	b.WriteString(string(lineRunes[:localPos]))
	if pos < len(value) && pos < line.end {
		i.field.Cursor.SetChar(string(value[pos]))
		b.WriteString(i.field.Cursor.View())
		if localPos+1 < len(lineRunes) {
			b.WriteString(string(lineRunes[localPos+1:]))
		}
	} else {
		i.field.Cursor.SetChar(" ")
		b.WriteString(i.field.Cursor.View())
	}
	return b.String()
}

func wrappedInputLines(value string, contentWidth int) []inputLine {
	if contentWidth < 1 {
		contentWidth = 1
	}
	runes := []rune(value)
	if len(runes) == 0 {
		return []inputLine{{}}
	}

	lines := make([]inputLine, 0, inputDisplayHeight(value, contentWidth))
	start := 0
	width := 0
	for idx, r := range runes {
		rw := lipgloss.Width(string(r))
		if rw < 1 {
			rw = 1
		}
		if width > 0 && width+rw > contentWidth {
			lines = append(lines, inputLine{text: string(runes[start:idx]), start: start, end: idx})
			start = idx
			width = 0
		}
		width += rw
	}
	lines = append(lines, inputLine{text: string(runes[start:]), start: start, end: len(runes)})
	return lines
}

func cursorInputLine(lines []inputLine, cursor int) int {
	if len(lines) == 0 {
		return 0
	}
	for idx, line := range lines {
		if cursor == line.end && idx < len(lines)-1 && lines[idx+1].start == cursor {
			continue
		}
		if cursor >= line.start && cursor <= line.end {
			return idx
		}
	}
	return len(lines) - 1
}
