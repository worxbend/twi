package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const (
	defaultMockWidth  = 88
	defaultMockHeight = 22
)

type fdWriter interface {
	Fd() uintptr
}

type mockShellModel struct {
	channel       string
	animationMode string
	imageMode     string
	status        ConnectionState
	messages      []twitch.ChatMessage
	width         int
	height        int
}

var _ tea.Model = mockShellModel{}

// RunMock starts the deterministic non-network mock chat shell. When stdout is
// not an interactive terminal, it writes the initial Bubble Tea view and exits
// so tests and redirected commands do not block waiting for keyboard input.
func RunMock(w io.Writer, cfg config.Config) error {
	channel := "mock"
	if len(cfg.DefaultChannels) > 0 {
		channel = cfg.DefaultChannels[0]
	}

	model := newMockShellModel(channel, cfg)
	if !isInteractiveTerminal(w) {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}

	program := tea.NewProgram(model, tea.WithOutput(w), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newMockShellModel(channel string, cfg config.Config) mockShellModel {
	connectedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	return mockShellModel{
		channel:       channel,
		animationMode: cfg.Features.AnimationMode,
		imageMode:     cfg.Features.ImageMode,
		status: ConnectionState{
			Status:  ConnectionConnected,
			Channel: channel,
			Detail:  "mock source ready",
			At:      connectedAt,
		},
		messages: seededMockMessages(channel, connectedAt),
		width:    defaultMockWidth,
		height:   defaultMockHeight,
	}
}

func seededMockMessages(channel string, startedAt time.Time) []twitch.ChatMessage {
	return []twitch.ChatMessage{
		{
			ID:          "mock-1",
			Channel:     channel,
			Timestamp:   startedAt.Add(time.Second),
			AuthorLogin: "twi_bot",
			DisplayName: "twi_bot",
			AuthorColor: "#9146ff",
			Text:        "Mock chat is ready in the Bubble Tea shell.",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-2",
			Channel:     channel,
			Timestamp:   startedAt.Add(2 * time.Second),
			AuthorLogin: "viewer42",
			DisplayName: "viewer42",
			AuthorColor: "#00d1ff",
			Text:        "@twi_bot composer, help, and status regions are visible.",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-3",
			Channel:     channel,
			Timestamp:   startedAt.Add(3 * time.Second),
			AuthorLogin: "moderator",
			DisplayName: "moderator",
			AuthorColor: "#00f593",
			Text:        "No Twitch credentials or network calls are used for --mock.",
			Type:        twitch.MessageTypeNotice,
		},
	}
}

func (m mockShellModel) Init() tea.Cmd {
	return nil
}

func (m mockShellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m mockShellModel) View() string {
	width := clampMin(m.width, 48)
	height := clampMin(m.height, 12)
	chatHeight := clampMin(height-8, 4)

	status := m.statusLine(width)
	chat := m.chatView(width, chatHeight)
	composer := m.composerView(width)
	help := m.helpView(width)

	return lipgloss.JoinVertical(lipgloss.Left, status, chat, composer, help)
}

func (m mockShellModel) statusLine(width int) string {
	left := fmt.Sprintf(" #%s  %s", m.channel, m.status.Status)
	if m.status.Detail != "" {
		left += " - " + m.status.Detail
	}
	right := fmt.Sprintf(" animation=%s images=%s", m.animationMode, m.imageMode)
	line := fitLine(left+right, width)

	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#4b367c")).
		Bold(true).
		Render(line)
}

func (m mockShellModel) chatView(width, height int) string {
	rowWidth := clampMin(width-4, 24)
	rows := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		rows = append(rows, render.TextRow(msg, rowWidth))
	}
	if len(rows) > height {
		rows = rows[len(rows)-height:]
	}

	content := strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Width(width-2).
		Height(height).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#5f6c7b")).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) composerView(width int) string {
	label := fmt.Sprintf(" Message #%s", m.channel)
	input := " Type a message..."
	box := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Foreground(lipgloss.Color("#8bd5ff")).Render(label),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Render(input),
	)

	return lipgloss.NewStyle().
		Width(width-2).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a9d8f")).
		Padding(0, 1).
		Render(box)
}

func (m mockShellModel) helpView(width int) string {
	help := " q quit | ctrl+c quit | mock source | no network "
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("#cdd6f4")).
		Background(lipgloss.Color("#1f2430")).
		Render(fitLine(help, width))
}

func isInteractiveTerminal(w io.Writer) bool {
	file, ok := w.(fdWriter)
	return ok && term.IsTerminal(int(file.Fd()))
}

func clampMin(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) > width {
		runes = runes[:width]
	}
	if len(runes) < width {
		return string(runes) + strings.Repeat(" ", width-len(runes))
	}
	return string(runes)
}
