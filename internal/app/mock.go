package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const (
	defaultMockWidth  = 88
	defaultMockHeight = 22
	mockIncomingDelay = 650 * time.Millisecond
	mockRevealDelay   = 20 * time.Millisecond
	avatarLookupDelay = 50 * time.Millisecond
	assetLookupDelay  = 35 * time.Millisecond
)

// AvatarResolver is the app-facing boundary for batched author avatar
// metadata. Implementations must not perform network work from View.
type AvatarResolver interface {
	ResolveAvatars(context.Context, []assets.AvatarRequest) ([]assets.AvatarResult, error)
}

type ClientOptions struct {
	AvatarResolver AvatarResolver
	AssetResolver  assets.EventResolver
	ImageRenderer  render.ImageRenderer
}

type fdWriter interface {
	Fd() uintptr
}

type mockShellModel struct {
	channels              *channelStateSet
	animationMode         string
	imageMode             string
	avatarMode            string
	emojiMode             string
	emoteMode             string
	imageCapability       render.ImageCapabilityDecision
	sourceDetail          string
	client                ChatClient
	avatarResolver        AvatarResolver
	assetResolver         assets.EventResolver
	imageRenderer         render.ImageRenderer
	incoming              []twitch.ChatMessage
	nextIncoming          int
	nextReveal            int
	width                 int
	height                int
	focus                 mockFocus
	helpExpanded          bool
	composerText          string
	replyTo               *composerReplyContext
	nextSend              int
	activeSend            *queuedComposerSend
	sendQueue             []queuedComposerSend
	sendState             composerSendState
	sendFeedback          string
	revealTickScheduled   bool
	avatarLookupScheduled bool
	avatarLookupInFlight  bool
	avatarRequested       map[string]bool
	assetLookupScheduled  bool
	assetLookupInFlight   bool
	assetRequested        map[string]bool
	imageCells            map[render.ImageCellKey]render.ImageCell
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
	ID               int
	Channel          string
	Text             string
	Draft            string
	ReplyToMessageID string
	Action           bool
	Reply            *composerReplyContext
}

type composerReplyContext struct {
	MessageID string
	Author    string
	Text      string
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

type avatarLookupTickMsg struct{}

type avatarLookupResolvedMsg struct {
	results []assets.AvatarResult
	err     error
}

type assetLookupTickMsg struct{}

type assetPreparedMsg struct {
	event assets.Event
	cell  render.ImageCell
	err   error
}

type assetPreparedBatchMsg struct {
	results []assetPreparedMsg
}

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

	model := newMockShellModelWithCapability(channel, cfg, runtimeImageCapability(cfg))
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
	return RunClientWithOptions(w, cfg, client, ClientOptions{})
}

// RunClientWithOptions starts the Bubble Tea chat shell with optional
// asynchronous app services such as avatar metadata resolution.
func RunClientWithOptions(w io.Writer, cfg config.Config, client ChatClient, opts ClientOptions) error {
	if client == nil {
		return fmt.Errorf("missing chat client")
	}
	defer client.Close()

	channel := "chat"
	if len(cfg.DefaultChannels) > 0 {
		channel = cfg.DefaultChannels[0]
	}

	model := newLiveShellModelWithOptionsAndCapability(channel, cfg, client, opts, runtimeImageCapability(cfg))
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
	return newMockShellModelWithClockAndCapability(channel, cfg, clock, deterministicImageCapability(cfg))
}

func newMockShellModelWithCapability(channel string, cfg config.Config, capability render.ImageCapabilityDecision) mockShellModel {
	return newMockShellModelWithClockAndCapability(channel, cfg, nil, capability)
}

func newMockShellModelWithClockAndCapability(channel string, cfg config.Config, clock animation.Clock, capability render.ImageCapabilityDecision) mockShellModel {
	connectedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	animationConfig := mockAnimationConfig(cfg.Features.AnimationMode)
	channels := newChannelStateSet(configuredChannels(channel, cfg.DefaultChannels), animationConfig, clock)
	for _, channelName := range channels.channelNames() {
		state := channels.ensure(channelName)
		state.status = ConnectionState{
			Status:  ConnectionConnected,
			Channel: channelName,
			Detail:  "mock source ready: no network",
			At:      connectedAt,
		}
		state.messages = seededMockMessages(channelName, connectedAt)
	}
	return mockShellModel{
		channels:        channels,
		animationMode:   string(animationConfig.Mode),
		imageMode:       capability.Mode,
		avatarMode:      capability.Avatar.Mode,
		emojiMode:       capability.Emoji.Mode,
		emoteMode:       capability.Emote.Mode,
		imageCapability: capability,
		sourceDetail:    "mock source: no network",
		incoming:        incomingMockMessages(channels.activeName(), connectedAt),
		width:           defaultMockWidth,
		height:          defaultMockHeight,
		focus:           mockFocusChat,
	}
}

func newLiveShellModelWithClock(channel string, cfg config.Config, client ChatClient, clock animation.Clock) mockShellModel {
	return newLiveShellModelWithClockAndOptions(channel, cfg, client, clock, ClientOptions{})
}

func newLiveShellModelWithOptionsAndCapability(channel string, cfg config.Config, client ChatClient, opts ClientOptions, capability render.ImageCapabilityDecision) mockShellModel {
	return newLiveShellModelWithClockOptionsAndCapability(channel, cfg, client, nil, opts, capability)
}

func newLiveShellModelWithClockAndOptions(channel string, cfg config.Config, client ChatClient, clock animation.Clock, opts ClientOptions) mockShellModel {
	return newLiveShellModelWithClockOptionsAndCapability(channel, cfg, client, clock, opts, deterministicImageCapability(cfg))
}

func newLiveShellModelWithClockOptionsAndCapability(channel string, cfg config.Config, client ChatClient, clock animation.Clock, opts ClientOptions, capability render.ImageCapabilityDecision) mockShellModel {
	animationConfig := mockAnimationConfig(cfg.Features.AnimationMode)
	channels := newChannelStateSet(configuredChannels(channel, cfg.DefaultChannels), animationConfig, clock)
	active := channels.activeState()
	active.status = ConnectionState{
		Status:  ConnectionConnecting,
		Channel: active.name,
		Detail:  "connecting to Twitch IRC",
		At:      time.Now(),
	}
	return mockShellModel{
		channels:        channels,
		animationMode:   string(animationConfig.Mode),
		imageMode:       capability.Mode,
		avatarMode:      capability.Avatar.Mode,
		emojiMode:       capability.Emoji.Mode,
		emoteMode:       capability.Emote.Mode,
		imageCapability: capability,
		sourceDetail:    "live IRC",
		client:          client,
		avatarResolver:  opts.AvatarResolver,
		assetResolver:   opts.AssetResolver,
		imageRenderer:   opts.ImageRenderer,
		avatarRequested: make(map[string]bool),
		assetRequested:  make(map[string]bool),
		imageCells:      make(map[render.ImageCellKey]render.ImageCell),
		width:           defaultMockWidth,
		height:          defaultMockHeight,
		focus:           mockFocusChat,
	}
}

func runtimeImageCapability(cfg config.Config) render.ImageCapabilityDecision {
	cacheWritable := false
	cacheDir, err := config.DefaultCacheDir()
	if err == nil && storage.ProbeWritableDir(cacheDir) == nil {
		cacheWritable = true
	}
	return render.DecideImageCapabilities(cfg.Features, render.DetectTerminalImageSignals(os.Environ()), cacheWritable)
}

func deterministicImageCapability(cfg config.Config) render.ImageCapabilityDecision {
	return render.DecideImageCapabilities(cfg.Features, render.TerminalImageSignals{}, true)
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
		case tea.KeyEsc:
			m.replyTo = nil
		case tea.KeyUp:
			if m.focus == mockFocusChat {
				m.selectReplyMessage(-1)
			}
		case tea.KeyDown:
			if m.focus == mockFocusChat {
				m.selectReplyMessage(1)
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
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == ']' {
				if m.channels.switchBy(1) {
					m.clampScroll()
					return m.withAsyncAssetCommands(nil)
				}
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == '[' {
				if m.channels.switchBy(-1) {
					m.clampScroll()
					return m.withAsyncAssetCommands(nil)
				}
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				return m, tea.Quit
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'r' {
				m.startReplyMode()
				return m, nil
			}
			if m.focus == mockFocusComposer {
				m.composerText += string(msg.Runes)
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
		return m.withAsyncAssetCommands(nil)
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
		return m.withAsyncAssetCommands(cmds...)
	case chatClientMessageMsg:
		if !msg.ok {
			m.channels.applyConnectionState(ConnectionState{
				Status:  ConnectionDisconnected,
				Channel: m.activeChannelName(),
				Detail:  "chat message stream closed",
				At:      time.Now(),
			})
			return m, nil
		}
		var cmds []tea.Cmd
		if revealCmd := m.enqueueMessage(msg.message); revealCmd != nil {
			cmds = append(cmds, revealCmd)
		}
		cmds = append(cmds, m.nextClientMessageCommand())
		m.clampScroll()
		return m.withAsyncAssetCommands(cmds...)
	case chatClientConnectionStateMsg:
		if !msg.ok {
			if m.activeChannelState().status.Status != ConnectionClosed {
				m.channels.applyConnectionState(ConnectionState{
					Status:  ConnectionDisconnected,
					Channel: m.activeChannelName(),
					Detail:  "connection state stream closed",
					At:      time.Now(),
				})
			}
			return m, nil
		}
		m.channels.applyConnectionState(msg.state)
		return m, m.nextConnectionStateCommand()
	case composerSendCompletedMsg:
		return m.completeComposerSend(msg)
	case mockAnimationTickMsg:
		m.revealTickScheduled = false
		active := m.activeChannelState()
		result := active.revealQueue.Advance()
		m.completeReveals(result.Completed)
		m.clampScroll()
		if active.revealQueue.Len() > 0 {
			return m, m.scheduleRevealTick()
		}
		if result.Changed {
			return m.withAsyncAssetCommands(nil)
		}
	case avatarLookupTickMsg:
		m.avatarLookupScheduled = false
		requests := m.pendingAvatarRequests()
		if len(requests) == 0 || m.avatarResolver == nil {
			return m, nil
		}
		m.markAvatarRequests(requests)
		m.avatarLookupInFlight = true
		return m, m.resolveAvatarCommand(requests)
	case avatarLookupResolvedMsg:
		m.avatarLookupInFlight = false
		m.applyAvatarResults(msg.results)
		m.clampScroll()
		if msg.err != nil {
			return m, nil
		}
		return m.withAsyncAssetCommands(nil)
	case assetLookupTickMsg:
		m.assetLookupScheduled = false
		requests := m.pendingAssetRequests()
		if len(requests) == 0 || m.assetResolver == nil || m.imageRenderer == nil {
			return m, nil
		}
		m.markAssetRequests(requests)
		m.assetLookupInFlight = true
		return m, m.resolveAssetsCommand(requests)
	case assetPreparedBatchMsg:
		m.assetLookupInFlight = false
		m.applyAssetResults(msg.results)
		m.refreshActiveRevealRows()
		m.clampScroll()
		return m.withAsyncAssetCommands(nil)
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
	active := m.activeChannelState()
	left := fmt.Sprintf("#%s %s", active.name, active.status.Status)
	if totalUnread := m.channels.totalUnread(); totalUnread > 0 && width >= 48 {
		left += fmt.Sprintf(" | unread=%d", totalUnread)
	}
	if width >= 50 && m.sendFeedback != "" {
		left += " | send: " + m.sendFeedback
	} else if width >= 34 && active.status.Detail != "" {
		left += " - " + active.status.Detail
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
	rows = visibleRows(rows, layout.chatContentHeight, m.activeChannelState().scrollOffset)

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
	active := m.activeChannelState()
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	rows := make([]string, 0, len(active.messages))
	for _, msg := range active.messages {
		for _, row := range render.Rows(msg, m.renderOptions(rowWidth)) {
			rows = append(rows, terminalRowString(row, rowWidth))
		}
	}
	frames := active.revealQueue.Frames()
	for _, id := range active.activeOrder {
		for _, row := range frames[id] {
			rows = append(rows, terminalRowString(row, rowWidth))
		}
	}
	return rows
}

func (m mockShellModel) composerView(layout mockShellLayout) string {
	label := fmt.Sprintf(" Message #%s", m.activeChannelName())
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
		if m.replyTo != nil {
			input = m.replyContextLine(layout.width) + "\n" + input
		}
		return fitBlock(input, layout.width, layout.composerHeight)
	}

	lines := []string{}
	if m.replyTo != nil && layout.composerContentHeight >= 3 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Italic(true).Render(m.replyContextLine(layout.width-4)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color("#8bd5ff")).Render(label),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Render(input),
	)
	box := lipgloss.JoinVertical(lipgloss.Left, lines...)

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

func (m mockShellModel) activeChannelState() *channelState {
	if m.channels == nil {
		channels := newChannelStateSet([]string{"chat"}, mockAnimationConfig(m.animationMode), nil)
		return channels.activeState()
	}
	return m.channels.activeState()
}

func (m mockShellModel) activeChannelName() string {
	if m.channels == nil {
		return "chat"
	}
	return m.channels.activeName()
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

func terminalRowString(row render.Row, width int) string {
	if width <= 0 {
		return ""
	}
	if row.Width() > width {
		return fitLine(row.Plain(), width)
	}
	out := row.TerminalString()
	if padding := width - row.Width(); padding > 0 {
		out += strings.Repeat(" ", padding)
	}
	return out
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
	if m.replyTo != nil {
		layout.composerHeight++
		layout.composerContentHeight++
	}
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
	m.activeChannelState().scrollOffset += delta
	m.clampScroll()
}

func (m *mockShellModel) clampScroll() {
	active := m.activeChannelState()
	maxScroll := m.maxScrollOffset()
	if active.scrollOffset > maxScroll {
		active.scrollOffset = maxScroll
	}
	if active.scrollOffset < 0 {
		active.scrollOffset = 0
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
	state, activeChannel := m.channels.applyMessage(message)
	if state == nil {
		return nil
	}
	if !activeChannel {
		return nil
	}
	message.Channel = state.name

	if state.scrollOffset > 0 {
		m.appendStaticMessage(message, true)
		return nil
	}

	layout := m.layout()
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	revealID := m.nextRevealID(message)
	result := state.revealQueue.Enqueue(revealID, render.Rows(message, m.renderOptions(rowWidth)))
	m.completeReveals(result.Overflow)
	if result.Complete != nil {
		m.appendStaticMessage(message, false)
		return nil
	}
	if result.Queued {
		state.activeOrder = append(state.activeOrder, revealID)
		state.activeMessages[revealID] = message
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
	state := m.activeChannelState()
	for _, reveal := range completed {
		message, ok := state.activeMessages[reveal.ID]
		if !ok {
			continue
		}
		m.appendStaticMessageReplacingRows(message, state.scrollOffset > 0, len(reveal.Rows))
		delete(state.activeMessages, reveal.ID)
		m.removeActiveReveal(reveal.ID)
	}
}

func (m *mockShellModel) appendStaticMessage(message twitch.ChatMessage, preserveScrolledView bool) {
	m.appendStaticMessageReplacingRows(message, preserveScrolledView, 0)
}

func (m *mockShellModel) appendStaticMessageReplacingRows(message twitch.ChatMessage, preserveScrolledView bool, replacedRows int) {
	state := m.channels.ensure(message.Channel)
	if state == nil {
		state = m.activeChannelState()
	}
	rowCount := 0
	if preserveScrolledView {
		rowCount = m.staticMessageRowCount(message) - replacedRows
	}
	if message.Channel == "" {
		message.Channel = state.name
	}
	state.messages = append(state.messages, message)
	if preserveScrolledView {
		state.scrollOffset += rowCount
	}
}

func (m mockShellModel) staticMessageRowCount(message twitch.ChatMessage) int {
	layout := m.layout()
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)
	rows := render.Rows(message, m.renderOptions(rowWidth))
	if len(rows) == 0 {
		return 1
	}
	return len(rows)
}

func (m *mockShellModel) removeActiveReveal(id string) {
	state := m.activeChannelState()
	for i, activeID := range state.activeOrder {
		if activeID != id {
			continue
		}
		copy(state.activeOrder[i:], state.activeOrder[i+1:])
		state.activeOrder = state.activeOrder[:len(state.activeOrder)-1]
		return
	}
}

func (m *mockShellModel) scheduleRevealTick() tea.Cmd {
	if m.revealTickScheduled || m.activeChannelState().revealQueue.Len() == 0 {
		return nil
	}
	m.revealTickScheduled = true
	return tea.Tick(mockRevealDelay, func(time.Time) tea.Msg {
		return mockAnimationTickMsg{}
	})
}

func (m *mockShellModel) withAsyncAssetCommands(cmds ...tea.Cmd) (tea.Model, tea.Cmd) {
	if cmd := m.scheduleAvatarLookup(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.scheduleAssetLookup(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return *m, batchNonNil(cmds...)
}

func batchNonNil(cmds ...tea.Cmd) tea.Cmd {
	nonNil := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			nonNil = append(nonNil, cmd)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	return tea.Batch(nonNil...)
}

func (m *mockShellModel) scheduleAvatarLookup() tea.Cmd {
	if m.avatarResolver == nil || m.avatarLookupScheduled || m.avatarLookupInFlight || !m.imageCapability.Avatar.Active {
		return nil
	}
	if len(m.pendingAvatarRequests()) == 0 {
		return nil
	}
	m.avatarLookupScheduled = true
	return tea.Tick(avatarLookupDelay, func(time.Time) tea.Msg {
		return avatarLookupTickMsg{}
	})
}

func (m mockShellModel) resolveAvatarCommand(requests []assets.AvatarRequest) tea.Cmd {
	resolver := m.avatarResolver
	return func() tea.Msg {
		results, err := resolver.ResolveAvatars(context.Background(), requests)
		return avatarLookupResolvedMsg{results: results, err: err}
	}
}

func (m *mockShellModel) markAvatarRequests(requests []assets.AvatarRequest) {
	if m.avatarRequested == nil {
		m.avatarRequested = make(map[string]bool)
	}
	for _, req := range requests {
		if key := avatarRequestKey(req); key != "" {
			m.avatarRequested[key] = true
		}
	}
}

func (m mockShellModel) pendingAvatarRequests() []assets.AvatarRequest {
	if m.avatarResolver == nil || !m.imageCapability.Avatar.Active {
		return nil
	}
	seen := make(map[string]bool)
	requests := []assets.AvatarRequest{}
	for _, message := range m.visibleAvatarMessages() {
		if strings.TrimSpace(message.AvatarURL) != "" {
			continue
		}
		req := assets.AvatarRequest{
			UserID:      message.AuthorID,
			UserLogin:   message.AuthorLogin,
			DisplayName: message.DisplayName,
		}
		key := avatarRequestKey(req)
		if key == "" || seen[key] || m.avatarRequested[key] {
			continue
		}
		seen[key] = true
		requests = append(requests, req)
	}
	return requests
}

func (m mockShellModel) visibleAvatarMessages() []twitch.ChatMessage {
	active := m.activeChannelState()
	layout := m.layout()
	if layout.chatContentHeight <= 0 {
		return nil
	}
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	rowCounts := make([]int, 0, len(active.messages))
	totalRows := 0
	for _, message := range active.messages {
		count := len(render.Rows(message, m.renderOptions(rowWidth)))
		if count <= 0 {
			count = 1
		}
		rowCounts = append(rowCounts, count)
		totalRows += count
	}
	frames := active.revealQueue.Frames()
	for _, id := range active.activeOrder {
		totalRows += len(frames[id])
	}

	start := totalRows - layout.chatContentHeight - active.scrollOffset
	if start < 0 {
		start = 0
	}
	end := start + layout.chatContentHeight
	messages := make([]twitch.ChatMessage, 0, layout.chatContentHeight)
	cursor := 0
	for i, message := range active.messages {
		next := cursor + rowCounts[i]
		if rangesOverlap(cursor, next, start, end) {
			messages = append(messages, message)
		}
		cursor = next
	}
	for _, id := range active.activeOrder {
		rows := frames[id]
		next := cursor + len(rows)
		if rangesOverlap(cursor, next, start, end) {
			if message, ok := active.activeMessages[id]; ok {
				messages = append(messages, message)
			}
		}
		cursor = next
	}
	return messages
}

func rangesOverlap(startA, endA, startB, endB int) bool {
	return startA < endB && startB < endA
}

func (m *mockShellModel) applyAvatarResults(results []assets.AvatarResult) {
	for _, result := range results {
		if !result.Found || strings.TrimSpace(result.AvatarURL) == "" {
			continue
		}
		for _, state := range m.channels.states {
			for i := range state.messages {
				applyAvatarToMessage(&state.messages[i], result)
			}
			for id, message := range state.activeMessages {
				applyAvatarToMessage(&message, result)
				state.activeMessages[id] = message
			}
		}
	}
}

func applyAvatarToMessage(message *twitch.ChatMessage, result assets.AvatarResult) {
	if message == nil || strings.TrimSpace(message.AvatarURL) != "" {
		return
	}
	if !avatarResultMatchesMessage(result, *message) {
		return
	}
	message.AvatarURL = result.AvatarURL
	if strings.TrimSpace(message.AuthorID) == "" {
		message.AuthorID = result.UserID
	}
}

func avatarResultMatchesMessage(result assets.AvatarResult, message twitch.ChatMessage) bool {
	if result.UserID != "" && message.AuthorID != "" {
		return result.UserID == message.AuthorID
	}
	if result.UserLogin != "" && message.AuthorLogin != "" {
		return strings.EqualFold(result.UserLogin, message.AuthorLogin)
	}
	if result.DisplayName != "" && message.DisplayName != "" {
		return strings.EqualFold(result.DisplayName, message.DisplayName)
	}
	return false
}

func avatarRequestKey(req assets.AvatarRequest) string {
	key := assets.AvatarCacheKey(req)
	if key.ID == "" {
		return ""
	}
	return key.Kind + "\x00" + key.ID
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (m *mockShellModel) scheduleAssetLookup() tea.Cmd {
	if m.assetResolver == nil || m.imageRenderer == nil || m.assetLookupScheduled || m.assetLookupInFlight {
		return nil
	}
	if len(m.pendingAssetRequests()) == 0 {
		return nil
	}
	m.assetLookupScheduled = true
	return tea.Tick(assetLookupDelay, func(time.Time) tea.Msg {
		return assetLookupTickMsg{}
	})
}

func (m mockShellModel) resolveAssetsCommand(requests []assets.Request) tea.Cmd {
	resolver := m.assetResolver
	renderer := m.imageRenderer
	return func() tea.Msg {
		results := make([]assetPreparedMsg, 0, len(requests))
		for _, request := range requests {
			event := resolver.Resolve(context.Background(), request)
			result := assetPreparedMsg{event: event}
			if event.Kind == assets.EventCacheHit || event.Kind == assets.EventDownloaded {
				cell, err := renderer.RenderImage(context.Background(), event.Record, imageSpecFromAssetRequest(request, event))
				result.cell = cell
				result.err = err
			}
			results = append(results, result)
		}
		return assetPreparedBatchMsg{results: results}
	}
}

func imageSpecFromAssetRequest(request assets.Request, event assets.Event) render.ImageSpec {
	ref := event.Ref
	if ref.Kind == "" && event.Record.Key.Kind != "" {
		ref.Kind = event.Record.Key.Kind
		ref.ID = event.Record.Key.ID
	}
	if ref.Kind == "" {
		ref = request.Ref
	}
	return render.ImageSpec{
		Ref:         ref,
		WidthCells:  request.WidthCells,
		HeightCells: request.HeightCells,
		Fallback:    request.Fallback,
	}
}

func (m *mockShellModel) markAssetRequests(requests []assets.Request) {
	if m.assetRequested == nil {
		m.assetRequested = make(map[string]bool)
	}
	for _, request := range requests {
		if request.ID != "" {
			m.assetRequested[request.ID] = true
		}
	}
}

func (m mockShellModel) pendingAssetRequests() []assets.Request {
	if m.assetResolver == nil || m.imageRenderer == nil || !m.imageCapabilityHasActiveAssets() {
		return nil
	}
	layout := m.layout()
	if layout.chatContentHeight <= 0 {
		return nil
	}
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)

	seen := make(map[string]bool)
	requests := []assets.Request{}
	for _, message := range m.visibleAssetMessages() {
		for _, row := range render.Rows(message, m.renderOptions(rowWidth)) {
			for _, fragment := range row.Fragments {
				request, ok := m.assetRequestForFragment(message, fragment)
				if !ok || seen[request.ID] || m.assetRequested[request.ID] {
					continue
				}
				seen[request.ID] = true
				requests = append(requests, request)
			}
		}
	}
	return requests
}

func (m mockShellModel) assetRequestForFragment(message twitch.ChatMessage, fragment render.Fragment) (assets.Request, bool) {
	if !m.assetFragmentEnabled(fragment) {
		return assets.Request{}, false
	}
	key, ok := render.ImageCellKeyForRef(fragment.Ref)
	if !ok {
		return assets.Request{}, false
	}
	if _, ok := m.imageCells[key]; ok {
		return assets.Request{}, false
	}
	request := assets.Request{
		ID:          assetRequestID(fragment.Ref, message.Channel),
		Ref:         fragment.Ref,
		ChannelID:   firstNonEmptyString(message.Channel, m.activeChannelName()),
		UserID:      message.AuthorID,
		UserLogin:   message.AuthorLogin,
		Fallback:    fragment.Text,
		WidthCells:  fragment.Width(),
		HeightCells: 1,
	}
	if request.ID == "" || request.WidthCells <= 0 {
		return assets.Request{}, false
	}
	return request, true
}

func (m mockShellModel) assetFragmentEnabled(fragment render.Fragment) bool {
	switch fragment.Kind {
	case render.FragmentAvatar:
		return m.imageCapability.Avatar.Active
	case render.FragmentBadge:
		return m.imageCapability.Status == render.ImageCapabilityEnabled || m.imageCapability.Status == render.ImageCapabilityDegraded
	case render.FragmentEmojiFallback:
		return m.imageCapability.Emoji.Active
	case render.FragmentEmoteFallback:
		return m.imageCapability.Emote.Active
	default:
		return false
	}
}

func (m mockShellModel) imageCapabilityHasActiveAssets() bool {
	return m.imageCapability.Avatar.Active ||
		m.imageCapability.Emoji.Active ||
		m.imageCapability.Emote.Active ||
		m.imageCapability.Status == render.ImageCapabilityEnabled ||
		m.imageCapability.Status == render.ImageCapabilityDegraded
}

func (m mockShellModel) visibleAssetMessages() []twitch.ChatMessage {
	return m.visibleAvatarMessages()
}

func assetRequestID(ref twitch.AssetRef, channel string) string {
	key, ok := render.ImageCellKeyForRef(ref)
	if !ok {
		return ""
	}
	if key.Kind == assets.KindBadge && strings.TrimSpace(channel) != "" {
		return key.Kind + "\x00" + channel + "\x00" + key.ID
	}
	return key.Kind + "\x00" + key.ID
}

func (m *mockShellModel) applyAssetResults(results []assetPreparedMsg) {
	if len(results) == 0 {
		return
	}
	if m.imageCells == nil {
		m.imageCells = make(map[render.ImageCellKey]render.ImageCell)
	}
	for _, result := range results {
		if result.err != nil || (result.event.Kind != assets.EventCacheHit && result.event.Kind != assets.EventDownloaded) {
			m.forgetAssetRequest(result.event.RequestID)
			continue
		}
		if result.cell.Text == "" {
			continue
		}
		key, ok := imageCellKeyFromAssetEvent(result.event)
		if !ok {
			continue
		}
		m.imageCells[key] = result.cell
	}
}

func (m *mockShellModel) forgetAssetRequest(id string) {
	if id == "" || m.assetRequested == nil {
		return
	}
	delete(m.assetRequested, id)
}

func imageCellKeyFromAssetEvent(event assets.Event) (render.ImageCellKey, bool) {
	if key, ok := render.ImageCellKeyForRef(event.Ref); ok {
		return key, true
	}
	if event.Record.Key.Kind != "" && event.Record.Key.ID != "" {
		return render.ImageCellKey{Kind: event.Record.Key.Kind, ID: event.Record.Key.ID}, true
	}
	return render.ImageCellKey{}, false
}

func (m *mockShellModel) refreshActiveRevealRows() {
	state := m.activeChannelState()
	if state.revealQueue == nil || state.revealQueue.Len() == 0 {
		return
	}
	layout := m.layout()
	rowWidth := layout.width
	if layout.chatFramed {
		rowWidth = layout.width - 4
	}
	rowWidth = clampMin(rowWidth, 1)
	for _, id := range state.activeOrder {
		message, ok := state.activeMessages[id]
		if !ok {
			continue
		}
		state.revealQueue.ReplaceRows(id, render.Rows(message, m.renderOptions(rowWidth)))
	}
}

func (m *mockShellModel) queueComposerSend() (tea.Model, tea.Cmd) {
	draft := strings.TrimSpace(m.composerText)
	text, action := composerSendText(draft)
	if text == "" {
		return *m, nil
	}
	if m.client == nil {
		m.sendState = composerSendFailed
		m.sendFeedback = "send unavailable for this chat source"
		return *m, nil
	}

	m.nextSend++
	channel := m.activeChannelName()
	m.sendQueue = append(m.sendQueue, queuedComposerSend{
		ID:               m.nextSend,
		Channel:          channel,
		Text:             text,
		Draft:            draft,
		ReplyToMessageID: replyMessageID(m.replyTo),
		Action:           action,
		Reply:            cloneComposerReply(m.replyTo),
	})
	m.composerText = ""
	m.replyTo = nil
	m.sendState = composerSendQueued
	m.sendFeedback = fmt.Sprintf("queued for #%s", channel)
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
	if next.ReplyToMessageID != "" {
		m.sendFeedback = "sending reply to " + replyAuthor(next.Reply)
	}
	if next.Action {
		m.sendFeedback = "sending action to #" + next.Channel
	}
	client := m.client
	req := SendRequest{
		Channel:          next.Channel,
		Text:             next.Text,
		ReplyToMessageID: next.ReplyToMessageID,
		Action:           next.Action,
	}
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
		texts, reply := m.drainUnsentComposerSends(sent)
		m.restoreComposerText(texts...)
		m.replyTo = reply
		return m, nil
	}
	if msg.result.RateLimited {
		m.sendState = composerSendRateLimited
		m.sendFeedback = "rate limited: " + sendResultDetail(msg.result)
		texts, reply := m.drainUnsentComposerSends(sent)
		m.restoreComposerText(texts...)
		m.replyTo = reply
		return m, nil
	}

	m.sendState = composerSendSucceeded
	m.sendFeedback = sendResultDetail(msg.result)
	return m, m.startNextComposerSend()
}

func (m *mockShellModel) drainUnsentComposerSends(active queuedComposerSend) ([]string, *composerReplyContext) {
	texts := make([]string, 0, len(m.sendQueue)+1)
	texts = append(texts, active.restoreText())
	for _, queued := range m.sendQueue {
		texts = append(texts, queued.restoreText())
	}
	reply := commonReplyContext(active, m.sendQueue)
	m.sendQueue = nil
	return texts, reply
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

func (s queuedComposerSend) restoreText() string {
	if s.Draft != "" {
		return s.Draft
	}
	if s.Action {
		return "/me " + s.Text
	}
	return s.Text
}

func composerSendText(draft string) (string, bool) {
	text := strings.TrimSpace(draft)
	lower := strings.ToLower(text)
	if lower == "/me" {
		return "", true
	}
	if strings.HasPrefix(lower, "/me ") || strings.HasPrefix(lower, "/me\t") {
		return strings.TrimSpace(text[len("/me"):]), true
	}
	return text, false
}

func replyMessageID(reply *composerReplyContext) string {
	if reply == nil {
		return ""
	}
	return reply.MessageID
}

func cloneComposerReply(reply *composerReplyContext) *composerReplyContext {
	if reply == nil {
		return nil
	}
	copied := *reply
	return &copied
}

func replyAuthor(reply *composerReplyContext) string {
	if reply == nil || reply.Author == "" {
		return "message"
	}
	return reply.Author
}

func commonReplyContext(active queuedComposerSend, queued []queuedComposerSend) *composerReplyContext {
	all := make([]queuedComposerSend, 0, len(queued)+1)
	all = append(all, active)
	all = append(all, queued...)

	var common *composerReplyContext
	for _, send := range all {
		if send.ReplyToMessageID == "" {
			return nil
		}
		if common == nil {
			common = cloneComposerReply(send.Reply)
			continue
		}
		if send.ReplyToMessageID != common.MessageID {
			return nil
		}
	}
	return common
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

func (m mockShellModel) renderOptions(width int) render.Options {
	opts := render.DefaultOptions(width)
	opts.Assets = m.imageCapability.AssetOptions()
	if len(m.imageCells) > 0 {
		opts.Assets.ImageCells = m.imageCells
	}
	return opts
}

func (m *mockShellModel) deleteComposerRune() {
	if m.composerText == "" {
		return
	}
	runes := []rune(m.composerText)
	m.composerText = string(runes[:len(runes)-1])
}

func (m *mockShellModel) selectReplyMessage(delta int) {
	messages := m.replyableMessages()
	if len(messages) == 0 {
		return
	}

	index := -1
	currentID := replyMessageID(m.replyTo)
	for i, message := range messages {
		if message.ID == currentID {
			index = i
			break
		}
	}
	if index == -1 {
		if delta < 0 {
			index = len(messages) - 1
		} else {
			index = 0
		}
	} else {
		index += delta
		if index < 0 {
			index = 0
		}
		if index >= len(messages) {
			index = len(messages) - 1
		}
	}

	m.replyTo = replyContextFromMessage(messages[index])
}

func (m *mockShellModel) startReplyMode() {
	if m.replyTo == nil {
		m.selectReplyMessage(-1)
	}
	if m.replyTo != nil {
		m.focus = mockFocusComposer
	}
}

func (m mockShellModel) replyableMessages() []twitch.ChatMessage {
	active := m.activeChannelState()
	messages := make([]twitch.ChatMessage, 0, len(active.messages))
	for _, message := range active.messages {
		if strings.TrimSpace(message.ID) != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func replyContextFromMessage(message twitch.ChatMessage) *composerReplyContext {
	author := displayReplyAuthor(message)
	text := compactReplyText(message.Text)
	if text == "" && len(message.Fragments) > 0 {
		var builder strings.Builder
		for _, fragment := range message.Fragments {
			builder.WriteString(fragment.Text)
		}
		text = compactReplyText(builder.String())
	}
	return &composerReplyContext{
		MessageID: message.ID,
		Author:    author,
		Text:      text,
	}
}

func displayReplyAuthor(message twitch.ChatMessage) string {
	if message.DisplayName != "" {
		return message.DisplayName
	}
	if message.AuthorLogin != "" {
		return message.AuthorLogin
	}
	return "unknown"
}

func compactReplyText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func (m mockShellModel) replyContextLine(width int) string {
	if m.replyTo == nil {
		return ""
	}
	line := " Replying to " + replyAuthor(m.replyTo)
	if m.replyTo.Text != "" {
		line += ": " + m.replyTo.Text
	}
	return fitLine(line, clampMin(width, 1))
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
		return []string{" tab focus | ? help | pg scroll | r reply | esc cancel | q quit | ctrl+c quit | " + source}
	}

	lines := []string{
		" tab focus: chat/composer",
		" up/down: select reply | r: reply | esc: cancel reply",
		" pgup/pgdn: scroll chat | ?: compact help | q: quit | ctrl+c: quit | " + source,
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
