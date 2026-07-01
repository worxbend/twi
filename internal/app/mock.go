package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const (
	defaultMockWidth  = 88
	defaultMockHeight = 22
	mockIncomingDelay = 650 * time.Millisecond
	mockRevealDelay   = 20 * time.Millisecond
)

type fdWriter interface {
	Fd() uintptr
}

type mockShellModel struct {
	channel             string
	animationMode       string
	imageMode           string
	sourceDetail        string
	client              ChatClient
	status              ConnectionState
	messages            []twitch.ChatMessage
	incoming            []twitch.ChatMessage
	nextIncoming        int
	nextReveal          int
	revealQueue         *animation.Queue
	activeOrder         []string
	activeMessages      map[string]twitch.ChatMessage
	width               int
	height              int
	focus               mockFocus
	helpExpanded        bool
	composerText        string
	nextSend            int
	activeSend          *queuedComposerSend
	sendQueue           []queuedComposerSend
	sendState           composerSendState
	sendFeedback        string
	scrollOffset        int
	revealTickScheduled bool
}

var _ tea.Model = mockShellModel{}

type mockFocus int

const (
	mockFocusChat mockFocus = iota
	mockFocusComposer
)

type composerSendState string

const (
	composerSendIdle        composerSendState = ""
	composerSendQueued      composerSendState = "queued"
	composerSendSending     composerSendState = "sending"
	composerSendSucceeded   composerSendState = "sent"
	composerSendFailed      composerSendState = "failed"
	composerSendRateLimited composerSendState = "rate_limited"
)

type queuedComposerSend struct {
	ID      int
	Channel string
	Text    string
}

type mockShellLayout struct {
	width                 int
	statusHeight          int
	chatHeight            int
	chatContentHeight     int
	chatFramed            bool
	composerHeight        int
	composerContentHeight int
	composerFramed        bool
	helpHeight            int
}

type mockIncomingMessageMsg struct {
	message   twitch.ChatMessage
	scheduled bool
	index     int
}

type mockAnimationTickMsg struct{}

type chatClientMessageMsg struct {
	message twitch.ChatMessage
	ok      bool
}

type chatClientConnectionStateMsg struct {
	state ConnectionState
	ok    bool
}

type composerSendCompletedMsg struct {
	id     int
	result SendResult
	err    error
}

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

// RunClient starts the Bubble Tea chat shell against a real app-facing chat
// client. The client is closed when the shell exits.
func RunClient(w io.Writer, cfg config.Config, client ChatClient) error {
	if client == nil {
		return fmt.Errorf("missing chat client")
	}
	defer client.Close()

	channel := "chat"
	if len(cfg.DefaultChannels) > 0 {
		channel = cfg.DefaultChannels[0]
	}

	model := newLiveShellModel(channel, cfg, client)
	if !isInteractiveTerminal(w) {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}

	program := tea.NewProgram(model, tea.WithOutput(w), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newMockShellModel(channel string, cfg config.Config) mockShellModel {
	return newMockShellModelWithClock(channel, cfg, nil)
}

func newMockShellModelWithClock(channel string, cfg config.Config, clock animation.Clock) mockShellModel {
	connectedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	animationConfig := mockAnimationConfig(cfg.Features.AnimationMode)
	return mockShellModel{
		channel:       channel,
		animationMode: string(animationConfig.Mode),
		imageMode:     cfg.Features.ImageMode,
		sourceDetail:  "mock source: no network",
		status: ConnectionState{
			Status:  ConnectionConnected,
			Channel: channel,
			Detail:  "mock source ready",
			At:      connectedAt,
		},
		messages:       seededMockMessages(channel, connectedAt),
		incoming:       incomingMockMessages(channel, connectedAt),
		revealQueue:    animation.NewQueue(animationConfig, clock),
		activeMessages: make(map[string]twitch.ChatMessage),
		width:          defaultMockWidth,
		height:         defaultMockHeight,
		focus:          mockFocusChat,
	}
}

func newLiveShellModel(channel string, cfg config.Config, client ChatClient) mockShellModel {
	return newLiveShellModelWithClock(channel, cfg, client, nil)
}

func newLiveShellModelWithClock(channel string, cfg config.Config, client ChatClient, clock animation.Clock) mockShellModel {
	animationConfig := mockAnimationConfig(cfg.Features.AnimationMode)
	return mockShellModel{
		channel:       channel,
		animationMode: string(animationConfig.Mode),
		imageMode:     cfg.Features.ImageMode,
		sourceDetail:  "live IRC",
		client:        client,
		status: ConnectionState{
			Status:  ConnectionConnecting,
			Channel: channel,
			Detail:  "connecting to Twitch IRC",
			At:      time.Now(),
		},
		revealQueue:    animation.NewQueue(animationConfig, clock),
		activeMessages: make(map[string]twitch.ChatMessage),
		width:          defaultMockWidth,
		height:         defaultMockHeight,
		focus:          mockFocusChat,
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

func incomingMockMessages(channel string, startedAt time.Time) []twitch.ChatMessage {
	return []twitch.ChatMessage{
		{
			ID:          "mock-live-1",
			Channel:     channel,
			Timestamp:   startedAt.Add(4 * time.Second),
			AuthorLogin: "new_viewer",
			DisplayName: "new_viewer",
			AuthorColor: "#ffb86c",
			Text:        "This incoming mock message reveals through animation ticks.",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-live-2",
			Channel:     channel,
			Timestamp:   startedAt.Add(5 * time.Second),
			AuthorLogin: "vip_guest",
			DisplayName: "vip_guest",
			AuthorColor: "#f38ba8",
			Text:        "Scrolling and the composer stay responsive while frames advance.",
			Type:        twitch.MessageTypeChat,
		},
	}
}

func (m mockShellModel) Init() tea.Cmd {
	return tea.Batch(m.nextIncomingCommand(), m.nextClientMessageCommand(), m.nextConnectionStateCommand())
}

func (m mockShellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyTab:
			m.cycleFocus()
		case tea.KeyPgUp:
			m.scrollBy(m.layout().chatContentHeight)
		case tea.KeyPgDown:
			m.scrollBy(-m.layout().chatContentHeight)
		case tea.KeyBackspace:
			if m.focus == mockFocusComposer {
				m.deleteComposerRune()
			}
		case tea.KeyCtrlU:
			if m.focus == mockFocusComposer {
				m.composerText = ""
			}
		case tea.KeyEnter:
			if m.focus == mockFocusComposer {
				return m.queueComposerSend()
			}
		case tea.KeySpace:
			if m.focus == mockFocusComposer {
				m.composerText += " "
			}
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && msg.Runes[0] == '?' {
				m.helpExpanded = !m.helpExpanded
				m.clampScroll()
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				return m, tea.Quit
			}
			if m.focus == mockFocusComposer {
				m.composerText += string(msg.Runes)
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
	case mockIncomingMessageMsg:
		var cmds []tea.Cmd
		if msg.scheduled && msg.index == m.nextIncoming {
			m.nextIncoming++
			cmds = append(cmds, m.nextIncomingCommand())
		}
		if revealCmd := m.enqueueMessage(msg.message); revealCmd != nil {
			cmds = append(cmds, revealCmd)
		}
		m.clampScroll()
		return m, tea.Batch(cmds...)
	case chatClientMessageMsg:
		if !msg.ok {
			m.status = ConnectionState{
				Status:  ConnectionDisconnected,
				Channel: m.channel,
				Detail:  "chat message stream closed",
				At:      time.Now(),
			}
			return m, nil
		}
		var cmds []tea.Cmd
		if revealCmd := m.enqueueMessage(msg.message); revealCmd != nil {
			cmds = append(cmds, revealCmd)
		}
		cmds = append(cmds, m.nextClientMessageCommand())
		m.clampScroll()
		return m, tea.Batch(cmds...)
	case chatClientConnectionStateMsg:
		if !msg.ok {
			if m.status.Status != ConnectionClosed {
				m.status = ConnectionState{
					Status:  ConnectionDisconnected,
					Channel: m.channel,
					Detail:  "connection state stream closed",
					At:      time.Now(),
				}
			}
			return m, nil
		}
		m.status = msg.state
		if m.status.Channel == "" {
			m.status.Channel = m.channel
		}
		return m, m.nextConnectionStateCommand()
	case composerSendCompletedMsg:
		return m.completeComposerSend(msg)
	case mockAnimationTickMsg:
		m.revealTickScheduled = false
		result := m.revealQueue.Advance()
		m.completeReveals(result.Completed)
		m.clampScroll()
		if m.revealQueue.Len() > 0 {
			return m, m.scheduleRevealTick()
		}
		if result.Changed {
			return m, nil
		}
	}
	return m, nil
}

func (m mockShellModel) View() string {
	layout := m.layout()

	regions := make([]string, 0, 4)
	if layout.statusHeight > 0 {
		regions = append(regions, m.statusLine(layout.width))
	}
	if layout.chatHeight > 0 {
		regions = append(regions, m.chatView(layout))
	}
	if layout.composerHeight > 0 {
		regions = append(regions, m.composerView(layout))
	}
	if layout.helpHeight > 0 {
		regions = append(regions, m.helpView(layout.width, layout.helpHeight))
	}

	return lipgloss.JoinVertical(lipgloss.Left, regions...)
}

func (m mockShellModel) statusLine(width int) string {
	left := fmt.Sprintf("#%s %s", m.channel, m.status.Status)
	if width >= 50 && m.sendFeedback != "" {
		left += " | send: " + m.sendFeedback
	} else if width >= 34 && m.status.Detail != "" {
		left += " - " + m.status.Detail
	}
	right := ""
	if width >= 64 {
		right = fmt.Sprintf(" focus=%s animation=%s images=%s", m.focusName(), m.animationMode, m.imageMode)
	} else if width >= 42 {
		right = fmt.Sprintf(" focus=%s", m.focusName())
	}
	line := fitLine(" "+left+right, width)

	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#4b367c")).
		Bold(true).
		Render(line)
}

func (m mockShellModel) chatView(layout mockShellLayout) string {
	rows := m.chatRows(layout)
	rows = visibleRows(rows, layout.chatContentHeight, m.scrollOffset)

	if len(rows) < layout.chatContentHeight {
		for len(rows) < layout.chatContentHeight {
			rows = append(rows, "")
		}
	}

	content := strings.Join(rows, "\n")
	if !layout.chatFramed {
		return fitBlock(content, layout.width, layout.chatHeight)
	}

	borderColor := lipgloss.Color("#5f6c7b")
	if m.focus == mockFocusChat {
		borderColor = lipgloss.Color("#8bd5ff")
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.chatContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) chatRows(layout mockShellLayout) []string {
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	rows := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		for _, row := range render.PlainRows(msg, rowWidth) {
			rows = append(rows, fitLine(row, rowWidth))
		}
	}
	frames := m.revealQueue.Frames()
	for _, id := range m.activeOrder {
		for _, row := range frames[id] {
			rows = append(rows, fitLine(row.Plain(), rowWidth))
		}
	}
	return rows
}

func (m mockShellModel) composerView(layout mockShellLayout) string {
	label := fmt.Sprintf(" Message #%s", m.channel)
	if m.focus == mockFocusComposer {
		label += " [focus]"
	}
	if m.sendState != composerSendIdle && layout.width >= 36 {
		label += " - " + string(m.sendState)
	}
	if layout.width < 28 {
		label = " >"
	}
	input := m.composerText
	if input == "" {
		input = "Type a message..."
	}
	input = " " + fitLine(input, clampMin(layout.width-4, 1))
	if !layout.composerFramed {
		return fitBlock(input, layout.width, layout.composerHeight)
	}

	box := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Foreground(lipgloss.Color("#8bd5ff")).Render(label),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Render(input),
	)

	if layout.composerContentHeight == 1 {
		box = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Render(input)
	}

	borderColor := lipgloss.Color("#2a9d8f")
	if m.focus == mockFocusComposer {
		borderColor = lipgloss.Color("#f9e2af")
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.composerContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(box)
}

func (m mockShellModel) helpView(width, height int) string {
	lines := m.helpLines(width, height)
	for i := range lines {
		lines[i] = fitLine(lines[i], width)
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("#cdd6f4")).
		Background(lipgloss.Color("#1f2430")).
		Render(strings.Join(lines, "\n"))
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

	var builder strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := uniseg.StringWidth(cluster)
		if used+clusterWidth > width {
			break
		}
		builder.WriteString(cluster)
		used += clusterWidth
	}
	if used < width {
		builder.WriteString(strings.Repeat(" ", width-used))
	}
	return builder.String()
}

func (m mockShellModel) layout() mockShellLayout {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)
	layout := mockShellLayout{
		width:        width,
		statusHeight: 1,
		helpHeight:   1,
	}
	if height == 1 {
		layout.helpHeight = 0
		return layout
	}

	if m.helpExpanded {
		switch {
		case height >= 14:
			layout.helpHeight = 3
		case height >= 10:
			layout.helpHeight = 2
		}
	}

	layout.composerHeight = 4
	layout.composerContentHeight = 2
	layout.composerFramed = width >= 5
	if height < 10 {
		layout.composerHeight = 3
		layout.composerContentHeight = 1
	}

	remaining := height - layout.statusHeight - layout.helpHeight - layout.composerHeight
	if remaining < 3 && layout.composerHeight > 3 {
		layout.composerHeight = 3
		layout.composerContentHeight = 1
		remaining = height - layout.statusHeight - layout.helpHeight - layout.composerHeight
	}
	if remaining < 1 && layout.helpHeight > 0 {
		layout.helpHeight = 0
		remaining = height - layout.statusHeight - layout.composerHeight
	}
	if remaining < 1 && layout.composerHeight > 0 {
		layout.composerHeight = clampMin(height-layout.statusHeight, 0)
		layout.composerContentHeight = clampMin(layout.composerHeight-2, 0)
		layout.composerFramed = layout.composerHeight >= 3 && width >= 5
		remaining = height - layout.statusHeight - layout.composerHeight
	}

	layout.chatHeight = clampMin(remaining, 0)
	layout.chatFramed = layout.chatHeight >= 3 && width >= 5
	layout.chatContentHeight = layout.chatHeight
	if layout.chatFramed {
		layout.chatContentHeight = layout.chatHeight - 2
	}
	if layout.chatContentHeight < 0 {
		layout.chatContentHeight = 0
	}

	used := layout.statusHeight + layout.chatHeight + layout.composerHeight + layout.helpHeight
	if used < height {
		layout.chatHeight += height - used
		if layout.chatFramed {
			layout.chatContentHeight = layout.chatHeight - 2
		} else {
			layout.chatContentHeight = layout.chatHeight
		}
	}

	return layout
}

func (m *mockShellModel) cycleFocus() {
	if m.focus == mockFocusChat {
		m.focus = mockFocusComposer
		return
	}
	m.focus = mockFocusChat
}

func (m *mockShellModel) scrollBy(delta int) {
	if delta == 0 {
		delta = 1
	}
	m.scrollOffset += delta
	m.clampScroll()
}

func (m *mockShellModel) clampScroll() {
	maxScroll := m.maxScrollOffset()
	if m.scrollOffset > maxScroll {
		m.scrollOffset = maxScroll
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m mockShellModel) maxScrollOffset() int {
	layout := m.layout()
	visible := layout.chatContentHeight
	rows := m.chatRows(layout)
	if visible <= 0 || len(rows) <= visible {
		return 0
	}
	return len(rows) - visible
}

func (m mockShellModel) nextIncomingCommand() tea.Cmd {
	if m.nextIncoming >= len(m.incoming) {
		return nil
	}

	message := m.incoming[m.nextIncoming]
	index := m.nextIncoming
	return tea.Tick(mockIncomingDelay, func(time.Time) tea.Msg {
		return mockIncomingMessageMsg{
			message:   message,
			scheduled: true,
			index:     index,
		}
	})
}

func (m mockShellModel) nextClientMessageCommand() tea.Cmd {
	if m.client == nil {
		return nil
	}
	messages := m.client.Messages()
	return func() tea.Msg {
		message, ok := <-messages
		return chatClientMessageMsg{message: message, ok: ok}
	}
}

func (m mockShellModel) nextConnectionStateCommand() tea.Cmd {
	if m.client == nil {
		return nil
	}
	states := m.client.ConnectionStates()
	return func() tea.Msg {
		state, ok := <-states
		return chatClientConnectionStateMsg{state: state, ok: ok}
	}
}

func (m *mockShellModel) enqueueMessage(message twitch.ChatMessage) tea.Cmd {
	layout := m.layout()
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	revealID := m.nextRevealID(message)
	result := m.revealQueue.Enqueue(revealID, render.Rows(message, render.DefaultOptions(rowWidth)))
	m.completeReveals(result.Overflow)
	if result.Complete != nil {
		m.messages = append(m.messages, message)
		return nil
	}
	if result.Queued {
		m.activeOrder = append(m.activeOrder, revealID)
		m.activeMessages[revealID] = message
		return m.scheduleRevealTick()
	}
	return nil
}

func (m *mockShellModel) nextRevealID(message twitch.ChatMessage) string {
	m.nextReveal++
	base := message.ID
	if base == "" {
		base = "mock-message"
	}
	return fmt.Sprintf("%s/%d", base, m.nextReveal)
}

func (m *mockShellModel) completeReveals(completed []animation.CompletedReveal) {
	for _, reveal := range completed {
		message, ok := m.activeMessages[reveal.ID]
		if !ok {
			continue
		}
		m.messages = append(m.messages, message)
		delete(m.activeMessages, reveal.ID)
		m.removeActiveReveal(reveal.ID)
	}
}

func (m *mockShellModel) removeActiveReveal(id string) {
	for i, activeID := range m.activeOrder {
		if activeID != id {
			continue
		}
		copy(m.activeOrder[i:], m.activeOrder[i+1:])
		m.activeOrder = m.activeOrder[:len(m.activeOrder)-1]
		return
	}
}

func (m *mockShellModel) scheduleRevealTick() tea.Cmd {
	if m.revealTickScheduled || m.revealQueue.Len() == 0 {
		return nil
	}
	m.revealTickScheduled = true
	return tea.Tick(mockRevealDelay, func(time.Time) tea.Msg {
		return mockAnimationTickMsg{}
	})
}

func (m *mockShellModel) queueComposerSend() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composerText)
	if text == "" {
		return *m, nil
	}
	if m.client == nil {
		m.sendState = composerSendFailed
		m.sendFeedback = "send unavailable for this chat source"
		return *m, nil
	}

	m.nextSend++
	m.sendQueue = append(m.sendQueue, queuedComposerSend{
		ID:      m.nextSend,
		Channel: m.channel,
		Text:    text,
	})
	m.composerText = ""
	m.sendState = composerSendQueued
	m.sendFeedback = fmt.Sprintf("queued for #%s", m.channel)
	return *m, m.startNextComposerSend()
}

func (m *mockShellModel) startNextComposerSend() tea.Cmd {
	if m.activeSend != nil || len(m.sendQueue) == 0 {
		return nil
	}
	next := m.sendQueue[0]
	m.sendQueue = m.sendQueue[1:]
	m.activeSend = &next
	m.sendState = composerSendSending
	m.sendFeedback = fmt.Sprintf("sending to #%s", next.Channel)
	client := m.client
	req := SendRequest{Channel: next.Channel, Text: next.Text}
	return func() tea.Msg {
		result, err := client.Send(context.Background(), req)
		return composerSendCompletedMsg{id: next.ID, result: result, err: err}
	}
}

func (m mockShellModel) completeComposerSend(msg composerSendCompletedMsg) (tea.Model, tea.Cmd) {
	if m.activeSend == nil || m.activeSend.ID != msg.id {
		return m, nil
	}

	sent := *m.activeSend
	m.activeSend = nil
	if msg.err != nil {
		m.sendState = composerSendFailed
		m.sendFeedback = "failed: " + credentialSafeSendDetail(msg.err)
		m.restoreComposerText(m.drainUnsentComposerText(sent)...)
		return m, nil
	}
	if msg.result.RateLimited {
		m.sendState = composerSendRateLimited
		m.sendFeedback = "rate limited: " + sendResultDetail(msg.result)
		m.restoreComposerText(m.drainUnsentComposerText(sent)...)
		return m, nil
	}

	m.sendState = composerSendSucceeded
	m.sendFeedback = sendResultDetail(msg.result)
	return m, m.startNextComposerSend()
}

func (m *mockShellModel) drainUnsentComposerText(active queuedComposerSend) []string {
	texts := make([]string, 0, len(m.sendQueue)+1)
	texts = append(texts, active.Text)
	for _, queued := range m.sendQueue {
		texts = append(texts, queued.Text)
	}
	m.sendQueue = nil
	return texts
}

func (m *mockShellModel) restoreComposerText(texts ...string) {
	text := strings.TrimSpace(strings.Join(texts, " "))
	if text == "" {
		return
	}
	if strings.TrimSpace(m.composerText) == "" {
		m.composerText = text
		return
	}
	m.composerText = text + " " + m.composerText
}

func sendResultDetail(result SendResult) string {
	if result.Detail != "" {
		return result.Detail
	}
	if result.RateLimited {
		if result.RetryAfter > 0 {
			return "retry in " + result.RetryAfter.String()
		}
		return "Twitch is slowing message sends"
	}
	if !result.AcceptedAt.IsZero() {
		return "accepted"
	}
	return "accepted"
}

func mockAnimationConfig(mode string) animation.Config {
	cfg := animation.DefaultConfig()
	switch animation.Mode(mode) {
	case animation.ModeOff:
		cfg.Mode = animation.ModeOff
	case animation.ModeReduced:
		cfg.Mode = animation.ModeReduced
	case animation.ModeFast:
		cfg.Mode = animation.ModeFast
	default:
		cfg.Mode = animation.ModeFast
	}
	return cfg
}

func (m *mockShellModel) deleteComposerRune() {
	if m.composerText == "" {
		return
	}
	runes := []rune(m.composerText)
	m.composerText = string(runes[:len(runes)-1])
}

func (m mockShellModel) focusName() string {
	if m.focus == mockFocusComposer {
		return "composer"
	}
	return "chat"
}

func (m mockShellModel) helpLines(width, height int) []string {
	source := m.sourceDetail
	if source == "" {
		source = "chat source"
	}
	if !m.helpExpanded {
		if width < 20 {
			return []string{" tab | ?"}
		}
		if width < 38 {
			return []string{" tab focus | ? help"}
		}
		return []string{" tab focus | ? help | pg scroll | q quit | ctrl+c quit | " + source}
	}

	lines := []string{
		" tab focus: chat/composer",
		" pgup/pgdn: scroll chat | ?: compact help",
		" q: quit from chat | ctrl+c: quit | " + source,
	}
	if width < 38 {
		lines = []string{
			" tab: focus",
			" pgup/pgdn: scroll",
			" ?: help | ctrl+c: quit",
		}
	}
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func visibleRows(rows []string, height, scrollOffset int) []string {
	if height <= 0 || len(rows) == 0 {
		return nil
	}
	if len(rows) <= height {
		return rows
	}

	maxScroll := len(rows) - height
	if scrollOffset > maxScroll {
		scrollOffset = maxScroll
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	end := len(rows) - scrollOffset
	start := end - height
	if start < 0 {
		start = 0
	}
	return rows[start:end]
}

func fitBlock(value string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, fitLine(line, width))
	}
	return strings.Join(out, "\n")
}
