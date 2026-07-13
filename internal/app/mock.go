package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/debuglog"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/theme"
	"github.com/w0rxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const (
	defaultMockWidth   = 88
	defaultMockHeight  = 22
	mockIncomingDelay  = 650 * time.Millisecond
	mockRevealDelay    = 20 * time.Millisecond
	avatarLookupDelay  = 50 * time.Millisecond
	assetLookupDelay   = 35 * time.Millisecond
	assetWorkTimeout   = 10 * time.Second
	assetWorkQueueMax  = 24
	assetWorkParallel  = 4
	assetFailureRetry  = 5 * time.Minute
	sidebarMinWidth    = 72
	sidebarNormalSize  = 18
	sidebarWideSize    = 24
	sceneFlashDuration = 180 * time.Millisecond
	splashDuration     = 2 * time.Second
)

// AvatarResolver is the app-facing boundary for batched author avatar
// metadata. Implementations must not perform network work from View.
type AvatarResolver interface {
	ResolveAvatars(context.Context, []assets.AvatarRequest) ([]assets.AvatarResult, error)
}

// StreamStatusResolver is the app-facing boundary for real Twitch broadcast
// status (Twitch Helix "Get Streams"). Implementations must not perform
// network work from View.
type StreamStatusResolver interface {
	GetStreams(ctx context.Context, logins []string) ([]twitch.StreamInfo, error)
}

type ClientOptions struct {
	AvatarResolver       AvatarResolver
	AssetResolver        assets.EventResolver
	AssetKinds           map[string]bool
	ImagePreparer        render.ImagePreparer
	ImageRenderer        render.ImageRenderer
	SystemNotifier       SystemNotifier
	StreamStatusResolver StreamStatusResolver
	EmoteIndex           *assets.EmoteIndex
	DebugLogger          debuglog.Logger
}

type fdWriter interface {
	Fd() uintptr
}

type mockShellModel struct {
	channels                  *channelStateSet
	theme                     theme.Palette
	effectiveConfig           config.Config
	terminalOutput            io.Writer
	mentionLogin              string
	animationMode             string
	mouseEnabled              bool
	imageMode                 string
	avatarMode                string
	emojiMode                 string
	emoteMode                 string
	imageCapability           render.ImageCapabilityDecision
	sourceDetail              string
	client                    ChatClient
	avatarResolver            AvatarResolver
	assetResolver             assets.EventResolver
	assetKinds                map[string]bool
	imagePreparer             render.ImagePreparer
	imageRenderer             render.ImageRenderer
	systemNotifier            SystemNotifier
	debugLogger               debuglog.Logger
	incoming                  []twitch.ChatMessage
	nextIncoming              int
	nextReveal                int
	width                     int
	height                    int
	focus                     mockFocus
	terminalFocused           bool
	lastSystemNotification    *SystemNotification
	helpExpanded              bool
	inspectOpen               bool
	palette                   commandPaletteState
	themeSettings             themeSettingsState
	emotePicker               emotePickerState
	reconnectInFlight         bool
	nextSend                  int
	frameTickScheduled        bool
	lastFrameAt               time.Time
	sceneFlashUntil           time.Time
	splashUntil               time.Time
	splashSkipped             bool
	frameTimestamps           []time.Time
	paletteRevealSeq          animation.Sequence
	paletteRevealKey          string
	streamStatusResolver      StreamStatusResolver
	streamStatusTickScheduled bool
	debugRecording            bool
	cpuSampleAt               time.Time
	cpuSampleTime             time.Duration
	cpuPercent                float64
	cpuAvailable              bool
	memoryMB                  float64
	chatByteSamples           []chatByteSample
	revealTickScheduled       bool
	avatarLookupScheduled     bool
	avatarLookupInFlight      bool
	avatarRequested           map[string]bool
	assetLookupScheduled      bool
	assetLookupInFlight       bool
	assetRetryScheduled       bool
	assetRequested            map[string]bool
	assetRetryAfter           map[string]time.Time
	assetPermanentFailure     map[assetPermanentFailureKey]struct{}
	imageCells                map[render.ImageCellKey]render.ImageCell
	emoteIndex                *assets.EmoteIndex
	emoteEntries              map[string][]assets.EmoteEntry
	emoteEntriesRequested     map[string]bool
	emoteSelected             int
}

var _ tea.Model = mockShellModel{}

type mockFocus int

const (
	mockFocusChat mockFocus = iota
	mockFocusComposer
	mockFocusEmotes
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
	width                      int
	statusHeight               int
	chatHeight                 int
	chatContentHeight          int
	chatFramed                 bool
	chatWidth                  int
	sidebarWidth               int
	sidebarContentHeight       int
	inspectHeight              int
	inspectContentHeight       int
	inspectFramed              bool
	paletteHeight              int
	paletteContentHeight       int
	paletteFramed              bool
	emotePickerHeight          int
	emotePickerContentHeight   int
	emotePickerFramed          bool
	themeSettingsHeight        int
	themeSettingsContentHeight int
	themeSettingsFramed        bool
	composerHeight             int
	composerContentHeight      int
	composerFramed             bool
	emotesHeight               int
	emotesContentHeight        int
	emotesFramed               bool
	helpHeight                 int
}

type chatRowBlock struct {
	message twitch.ChatMessage
	rows    []render.Row
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
	event      assets.Event
	spec       render.ImageSpec
	cell       render.ImageCell
	err        error
	permanent  bool
	failureKey assetPermanentFailureKey
}

type assetPreparedBatchMsg struct {
	results []assetPreparedMsg
}

type assetPermanentFailureKey struct {
	AssetKind             string
	AssetID               string
	ChannelIdentity       string
	RecordKind            string
	RecordID              string
	RecordUnsafe          bool
	PayloadIdentity       string
	PayloadIdentityUnsafe bool
	MediaType             string
	MediaTypeUnsafe       bool
	RecordWidthCells      int
	RecordHeightCells     int
	FetchedAtUnixNano     int64
	RequestedWidthCells   int
	RequestedHeightCells  int
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

type reconnectCompletedMsg struct {
	channel string
	err     error
}

// RunMock starts the deterministic non-network mock chat shell. When stdout is
// not an interactive terminal, it writes the initial Bubble Tea view and exits
// so tests and redirected commands do not block waiting for keyboard input.
func RunMock(w io.Writer, cfg config.Config) error {
	return RunMockWithOptions(w, cfg, ClientOptions{})
}

// RunMockWithOptions starts the deterministic non-network mock chat shell with
// optional app services and diagnostics. Non-interactive behavior matches
// RunMock.
func RunMockWithOptions(w io.Writer, cfg config.Config, opts ClientOptions) error {
	channel := "mock"
	if len(cfg.DefaultChannels) > 0 {
		channel = cfg.DefaultChannels[0]
	}

	model := newMockShellModelWithCapability(channel, cfg, runtimeImageCapability(cfg))
	model.debugLogger = opts.DebugLogger
	model.debugAppStart("mock", len(configuredChannels(channel, cfg.DefaultChannels)))
	if !isInteractiveTerminal(w) {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}
	if opts.SystemNotifier == nil {
		opts.SystemNotifier = newDefaultSystemNotifier(w)
	}
	model.systemNotifier = opts.SystemNotifier
	model.splashUntil = splashDeadline(model.animationMode)
	model.terminalOutput = w
	primeTerminalBackground(w, model.theme.Background)

	program := tea.NewProgram(model, programOptions(w, cfg)...)
	_, err := program.Run()
	resetTerminalBackground(w)
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

	interactive := isInteractiveTerminal(w)
	if interactive && opts.SystemNotifier == nil {
		opts.SystemNotifier = newDefaultSystemNotifier(w)
	}
	model := newLiveShellModelWithOptionsAndCapability(channel, cfg, client, opts, runtimeImageCapability(cfg))
	model.debugAppStart("live", len(configuredChannels(channel, cfg.DefaultChannels)))
	if !interactive {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}
	model.splashUntil = splashDeadline(model.animationMode)
	model.terminalOutput = w
	primeTerminalBackground(w, model.theme.Background)

	program := tea.NewProgram(model, programOptions(w, cfg)...)
	_, err := program.Run()
	resetTerminalBackground(w)
	return err
}

// splashDeadline returns when the startup splash should end, or the zero
// time when animation is disabled (splashActive treats a zero deadline as
// "no splash").
func splashDeadline(animationMode string) time.Time {
	if animationMode == string(animation.ModeOff) {
		return time.Time{}
	}
	return time.Now().Add(splashDuration)
}

func programOptions(w io.Writer, cfg config.Config) []tea.ProgramOption {
	options := []tea.ProgramOption{tea.WithOutput(w), tea.WithAltScreen(), tea.WithReportFocus()}
	if cfg.Features.EnableMouse {
		options = append(options, tea.WithMouseCellMotion())
	}
	return options
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
		state.live = true
		state.liveSince = connectedAt
		state.viewerCount = 128
	}
	emoteEntries := make(map[string][]assets.EmoteEntry, len(channels.channelNames()))
	for _, channelName := range channels.channelNames() {
		emoteEntries[channelKey(channelName)] = sampleEmoteEntries()
	}
	return mockShellModel{
		channels:        channels,
		theme:           cfg.ResolveTheme(),
		mentionLogin:    cfg.Twitch.Username,
		animationMode:   string(animationConfig.Mode),
		mouseEnabled:    cfg.Features.EnableMouse,
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
		terminalFocused: true,
		debugRecording:  cfg.Debug.Enabled,
		emoteEntries:    emoteEntries,
		effectiveConfig: cfg,
	}
}

// sampleEmoteEntries seeds Ctrl+E search and the quick-select row in
// --mock mode with well-known Twitch global emote names, so both are
// demoable without credentials or network access.
func sampleEmoteEntries() []assets.EmoteEntry {
	names := []string{
		"Kappa", "PogChamp", "LUL", "monkaS", "KEKW", "5Head", "EZ", "PagMan",
		"OMEGALUL", "Pog", "BibleThump", "TriHard", "VoHiYo", "ResidentSleeper",
		"NotLikeThis", "SeemsGood", "HeyGuys", "DansGame",
	}
	entries := make([]assets.EmoteEntry, len(names))
	for i, name := range names {
		entries[i] = assets.EmoteEntry{Name: name}
	}
	return entries
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
		channels:              channels,
		theme:                 cfg.ResolveTheme(),
		mentionLogin:          cfg.Twitch.Username,
		animationMode:         string(animationConfig.Mode),
		mouseEnabled:          cfg.Features.EnableMouse,
		imageMode:             capability.Mode,
		avatarMode:            capability.Avatar.Mode,
		emojiMode:             capability.Emoji.Mode,
		emoteMode:             capability.Emote.Mode,
		imageCapability:       capability,
		sourceDetail:          "live IRC",
		client:                client,
		avatarResolver:        opts.AvatarResolver,
		assetResolver:         opts.AssetResolver,
		assetKinds:            cloneAssetKinds(opts.AssetKinds),
		imagePreparer:         opts.ImagePreparer,
		imageRenderer:         opts.ImageRenderer,
		systemNotifier:        opts.SystemNotifier,
		streamStatusResolver:  opts.StreamStatusResolver,
		emoteIndex:            opts.EmoteIndex,
		emoteEntries:          make(map[string][]assets.EmoteEntry),
		emoteEntriesRequested: make(map[string]bool),
		debugLogger:           opts.DebugLogger,
		avatarRequested:       make(map[string]bool),
		assetRequested:        make(map[string]bool),
		assetRetryAfter:       make(map[string]time.Time),
		assetPermanentFailure: make(map[assetPermanentFailureKey]struct{}),
		imageCells:            make(map[render.ImageCellKey]render.ImageCell),
		width:                 defaultMockWidth,
		height:                defaultMockHeight,
		focus:                 mockFocusChat,
		terminalFocused:       true,
		debugRecording:        cfg.Debug.Enabled,
		effectiveConfig:       cfg,
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
	return tea.Batch(
		m.nextIncomingCommand(),
		m.nextClientMessageCommand(),
		m.nextConnectionStateCommand(),
		m.scheduleFrameTick(),
		m.resolveStreamStatusCommand(),
		m.scheduleStreamStatusTick(),
	)
}

func (m mockShellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if m.splashActive() {
			m.splashSkipped = true
			return m, nil
		}
		if msg.Type == tea.KeyCtrlP {
			m.toggleCommandPalette()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlE {
			m.toggleEmotePicker()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlT {
			m.toggleThemeSettings()
			return m, nil
		}
		if m.palette.open {
			return m.handleCommandPaletteKey(msg)
		}
		if m.emotePicker.open {
			return m.handleEmotePickerKey(msg)
		}
		if m.themeSettings.open {
			return m.handleThemeSettingsKey(msg)
		}
		switch msg.Type {
		case tea.KeyTab:
			m.cycleFocus()
		case tea.KeyPgUp:
			m.scrollBy(m.layout().chatContentHeight)
		case tea.KeyPgDown:
			m.scrollBy(-m.layout().chatContentHeight)
		case tea.KeyCtrlL:
			m.clearLocalChat()
		case tea.KeyCtrlR:
			return m, m.requestReconnect()
		case tea.KeyBackspace:
			if m.focus == mockFocusComposer {
				m.deleteComposerRune()
			}
		case tea.KeyEsc:
			if m.inspectOpen {
				m.inspectOpen = false
				m.clampScroll()
				return m, nil
			}
			m.activeChannelState().replyTo = nil
		case tea.KeyUp:
			if m.focus == mockFocusChat {
				m.selectReplyMessage(-1)
			}
		case tea.KeyDown:
			if m.focus == mockFocusChat {
				m.selectReplyMessage(1)
			}
		case tea.KeyLeft:
			if m.focus == mockFocusEmotes {
				m.moveEmoteSelection(-1)
			}
		case tea.KeyRight:
			if m.focus == mockFocusEmotes {
				m.moveEmoteSelection(1)
			}
		case tea.KeyCtrlU:
			if m.focus == mockFocusComposer {
				m.activeChannelState().composerText = ""
			}
		case tea.KeyEnter:
			if m.focus == mockFocusComposer {
				return m.queueComposerSend()
			}
			if m.focus == mockFocusEmotes {
				m.insertSelectedEmote()
			}
		case tea.KeySpace:
			if m.focus == mockFocusComposer {
				m.activeChannelState().composerText += " "
			}
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && msg.Runes[0] == '?' {
				m.helpExpanded = !m.helpExpanded
				m.clampScroll()
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == ']' {
				if m.channels.switchBy(1) {
					m.triggerSceneFlash()
					m.clampScroll()
					return m.withAsyncAssetCommands(nil)
				}
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == '[' {
				if m.channels.switchBy(-1) {
					m.triggerSceneFlash()
					m.clampScroll()
					return m.withAsyncAssetCommands(nil)
				}
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 {
				if filter, ok := messageFilterForShortcutRune(msg.Runes[0]); ok {
					return m, m.toggleActiveMessageFilter(filter)
				}
				if msg.Runes[0] == '0' {
					return m, m.resetActiveMessageFilters()
				}
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				return m, tea.Quit
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'r' {
				m.startReplyMode()
				return m, nil
			}
			if m.focus == mockFocusChat && len(msg.Runes) == 1 && msg.Runes[0] == 'i' {
				m.inspectOpen = !m.inspectOpen
				if m.inspectOpen {
					m.closeOtherOverlays("inspect")
				}
				m.clampScroll()
				return m, nil
			}
			if m.focus == mockFocusComposer {
				m.activeChannelState().composerText += string(msg.Runes)
			}
		}
	case tea.MouseMsg:
		if m.palette.open || m.emotePicker.open || m.themeSettings.open {
			return m, nil
		}
		return m.handleMouse(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
		return m.withAsyncAssetCommands(nil)
	case tea.FocusMsg:
		m.terminalFocused = true
	case tea.BlurMsg:
		m.terminalFocused = false
	case mockIncomingMessageMsg:
		var cmds []tea.Cmd
		if msg.scheduled && msg.index == m.nextIncoming {
			m.nextIncoming++
			cmds = append(cmds, m.nextIncomingCommand())
		}
		if revealCmd := m.enqueueMessage(msg.message); revealCmd != nil {
			cmds = append(cmds, revealCmd)
		}
		if notificationCmd := m.maybeNotifyForSystemEvent(msg.message); notificationCmd != nil {
			cmds = append(cmds, notificationCmd)
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
			m.debugConnectionState("app.message_stream.closed", m.activeChannelState().status)
			return m, nil
		}
		m.debugChatMessage("app.message.received", msg.message)
		var cmds []tea.Cmd
		if revealCmd := m.enqueueMessage(msg.message); revealCmd != nil {
			cmds = append(cmds, revealCmd)
		}
		if notificationCmd := m.maybeNotifyForSystemEvent(msg.message); notificationCmd != nil {
			cmds = append(cmds, notificationCmd)
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
				m.debugConnectionState("app.connection_stream.closed", m.activeChannelState().status)
			}
			return m, nil
		}
		m.channels.applyConnectionState(msg.state)
		m.debugConnectionState("app.connection_state.received", msg.state)
		return m, m.nextConnectionStateCommand()
	case composerSendCompletedMsg:
		return m.completeComposerSend(msg)
	case reconnectCompletedMsg:
		m.completeReconnect(msg)
	case animation.FrameMsg:
		m.frameTickScheduled = false
		m.advanceFrame(msg.At)
		return m, m.scheduleFrameTick()
	case streamStatusTickMsg:
		m.streamStatusTickScheduled = false
		return m, tea.Batch(m.resolveStreamStatusCommand(), m.scheduleStreamStatusTick())
	case streamStatusResolvedMsg:
		if msg.err == nil {
			m.applyStreamStatusResults(msg.results)
		}
		return m, nil
	case broadcasterIDResolvedMsg:
		m.applyBroadcasterIDResult(msg)
		return m.withAsyncAssetCommands(nil)
	case emoteIndexResolvedMsg:
		m.applyEmoteIndexResult(msg)
		return m, nil
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
		m.debugAvatarLookupStart(len(requests))
		m.markAvatarRequests(requests)
		m.avatarLookupInFlight = true
		return m, m.resolveAvatarCommand(requests)
	case avatarLookupResolvedMsg:
		m.avatarLookupInFlight = false
		m.applyAvatarResults(msg.results)
		m.debugAvatarLookupComplete(msg.results, msg.err)
		m.clampScroll()
		if msg.err != nil {
			return m, nil
		}
		return m.withAsyncAssetCommands(nil)
	case assetLookupTickMsg:
		m.assetLookupScheduled = false
		m.assetRetryScheduled = false
		requests := m.pendingAssetRequests()
		if len(requests) == 0 || m.assetResolver == nil || m.imageRenderer == nil {
			return m, nil
		}
		m.debugAssetBatchStart(len(requests))
		m.markAssetRequests(requests)
		m.assetLookupInFlight = true
		return m, m.resolveAssetsCommand(requests)
	case assetPreparedBatchMsg:
		m.assetLookupInFlight = false
		m.applyAssetResults(msg.results)
		m.debugAssetBatchComplete(msg.results)
		m.refreshActiveRevealRows()
		m.clampScroll()
		return m.withAsyncAssetCommands(nil)
	}
	return m, nil
}

func (m mockShellModel) View() string {
	backgroundOverride := m.themeBackgroundSequence()
	if m.splashActive() {
		return backgroundOverride + m.splashView()
	}
	layout := m.layout()

	regions := make([]string, 0, 4)
	if layout.statusHeight > 0 {
		regions = append(regions, m.statusLine(layout.width))
	}
	if layout.chatHeight > 0 {
		chat := m.chatView(layout)
		if layout.sidebarWidth > 0 {
			chat = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(layout), chat)
		}
		regions = append(regions, chat)
	}
	if layout.paletteHeight > 0 {
		regions = append(regions, m.commandPaletteView(layout))
	}
	if layout.inspectHeight > 0 {
		regions = append(regions, m.inspectView(layout))
	}
	if layout.emotePickerHeight > 0 {
		regions = append(regions, m.emotePickerView(layout))
	}
	if layout.themeSettingsHeight > 0 {
		regions = append(regions, m.themeSettingsView(layout))
	}
	if layout.composerHeight > 0 {
		regions = append(regions, m.composerView(layout))
	}
	if layout.emotesHeight > 0 {
		regions = append(regions, m.emotesView(layout))
	}
	if layout.helpHeight > 0 {
		regions = append(regions, m.helpView(layout.width, layout.helpHeight))
	}

	joined := lipgloss.JoinVertical(lipgloss.Left, regions...)
	rendered := lipgloss.NewStyle().
		Width(layout.width).
		Height(clampMin(m.height, 1)).
		Background(lipgloss.Color(m.theme.Background)).
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Render(joined)
	return backgroundOverride + rendered
}

func (m mockShellModel) statusLine(width int) string {
	active := m.activeChannelState()
	channelCount := len(m.channels.channelNames())
	left := fmt.Sprintf("#%s %s", active.name, active.status.Status)
	if width >= 96 {
		left = m.formatStatusMetrics(m.metricsNow(), m.debugRecording) + " | " + left
	} else if width >= 60 {
		left = m.compactStatusMetrics(m.metricsNow()) + " | " + left
	}
	if channelCount > 1 && width >= 26 {
		left += fmt.Sprintf(" | channels=%d", channelCount)
	}
	if totalUnread := m.channels.totalUnread(); totalUnread > 0 && width >= 34 {
		left += fmt.Sprintf(" | unread=%d", totalUnread)
	}
	if m.lastSystemNotification != nil && width >= 58 {
		left += " | notify: " + systemNotificationSummary(*m.lastSystemNotification)
	}
	if summary := active.messageFilters.summary(); summary != "" && width >= 46 {
		left += " | filter=" + summary
	}
	right := ""
	if width >= 64 {
		right = fmt.Sprintf(" focus=%s animation=%s images=%s", m.focusName(), m.animationMode, m.imageMode)
	} else if width >= 42 {
		right = fmt.Sprintf(" focus=%s", m.focusName())
	}
	if width >= 50 && active.sendFeedback != "" {
		left += " | send: " + active.sendFeedback
	} else if width >= 34 && active.status.Detail != "" && (channelCount == 1 || width >= 112) {
		left += " - " + active.status.Detail
	}
	line := fitLine(" "+left+right, width)

	statusBackground := m.theme.Accent
	statusForeground := theme.ContrastCorrectedForeground(m.theme.Foreground, statusBackground, m.theme.Background)
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color(statusForeground)).
		Background(lipgloss.Color(statusBackground)).
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
		return fitBlock(content, layout.chatWidth, layout.chatHeight)
	}

	borderColor := lipgloss.Color(m.theme.Border)
	if m.focus == mockFocusChat && !m.anyOverlayOpen() {
		borderColor = lipgloss.Color(m.theme.Accent)
	}
	if m.sceneFlashActive() {
		borderColor = lipgloss.Color(m.theme.Foreground)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.chatWidth-2, 0)).
		Height(layout.chatContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) sidebarView(layout mockShellLayout) string {
	if layout.sidebarWidth <= 0 {
		return ""
	}
	contentWidth := clampMin(layout.sidebarWidth-2, 1)
	lines := make([]string, 0, layout.sidebarContentHeight)
	lines = append(lines, fitLine(" Channels", contentWidth))
	for _, key := range m.channels.order {
		state := m.channels.states[key]
		if state == nil {
			continue
		}
		marker := " "
		if key == m.channels.active {
			marker = ">"
		}
		status := channelStatusIndicator(state.status.Status)
		name := "#" + state.name
		line := fmt.Sprintf("%s %s %s", marker, status, name)
		if state.unread > 0 {
			line += fmt.Sprintf(" %d", state.unread)
		}
		if state.messageFilters.active() {
			line += " f"
		}
		lines = append(lines, fitLine(line, contentWidth))
	}
	for len(lines) < layout.sidebarContentHeight {
		lines = append(lines, fitLine("", contentWidth))
	}
	if len(lines) > layout.sidebarContentHeight {
		lines = lines[:layout.sidebarContentHeight]
	}

	borderColor := lipgloss.Color(m.theme.Border)
	if m.focus == mockFocusChat && !m.anyOverlayOpen() {
		borderColor = lipgloss.Color(m.theme.Accent)
	}
	return lipgloss.NewStyle().
		Width(contentWidth).
		Height(layout.sidebarContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Render(strings.Join(lines, "\n"))
}

func channelStatusIndicator(status ConnectionStatus) string {
	switch status {
	case ConnectionConnected:
		return "*"
	case ConnectionConnecting, ConnectionReconnecting:
		return "~"
	case ConnectionFailed, ConnectionDisconnected, ConnectionClosed:
		return "!"
	default:
		return "-"
	}
}

func (m mockShellModel) chatRows(layout mockShellLayout) []string {
	active := m.activeChannelState()
	rowWidth := m.chatRowWidth(layout)
	blocks := m.visibleChatRowBlocks(layout)

	rows := make([]string, 0, chatRowBlockCount(blocks))
	for _, block := range blocks {
		if len(block.rows) == 0 {
			rows = append(rows, backgroundStyledLine(fitLine("", rowWidth), m.theme.Background))
			continue
		}
		for _, row := range block.rows {
			rows = append(rows, terminalRowString(row, rowWidth, m.theme.Background))
		}
	}
	if len(rows) == 0 && active.messageFilters.active() {
		rows = append(rows, backgroundStyledLine(m.emptyFilterRow(rowWidth), m.theme.Background))
	}
	return rows
}

func (m mockShellModel) visibleChatRowBlocks(layout mockShellLayout) []chatRowBlock {
	active := m.activeChannelState()
	rowWidth := m.chatRowWidth(layout)
	options := m.renderOptions(rowWidth)

	blocks := make([]chatRowBlock, 0, len(active.messages)+len(active.activeOrder))
	for _, message := range active.messages {
		if !m.messageVisibleForState(active, message) {
			continue
		}
		blocks = append(blocks, chatRowBlock{
			message: message,
			rows:    render.Rows(message, options),
		})
	}
	frames := active.revealQueue.Frames()
	for _, id := range active.activeOrder {
		message, ok := active.activeMessages[id]
		if !ok || !m.messageVisibleForState(active, message) {
			continue
		}
		blocks = append(blocks, chatRowBlock{
			message: message,
			rows:    frames[id],
		})
	}
	return blocks
}

func (m mockShellModel) messageVisibleForState(state *channelState, message twitch.ChatMessage) bool {
	if state == nil {
		return true
	}
	return state.messageFilters.matches(message, m.mentionLogin)
}

func chatRowBlockCount(blocks []chatRowBlock) int {
	total := 0
	for _, block := range blocks {
		total += chatRowBlockRowCount(block)
	}
	return total
}

func chatRowBlockRowCount(block chatRowBlock) int {
	if len(block.rows) == 0 {
		return 1
	}
	return len(block.rows)
}

func (m mockShellModel) emptyFilterRow(width int) string {
	active := m.activeChannelState()
	summary := active.messageFilters.summary()
	hidden := len(active.messages) + len(active.activeOrder)
	detail := "no messages yet"
	if hidden > 0 {
		detail = fmt.Sprintf("no matching messages (%d hidden)", hidden)
	}
	return fitLine(" filter: "+summary+" - "+detail, width)
}

func (m mockShellModel) composerView(layout mockShellLayout) string {
	active := m.activeChannelState()
	label := fmt.Sprintf(" Message #%s", m.activeChannelName())
	if m.focus == mockFocusComposer && !m.palette.open {
		label += " [focus]"
	}
	if active.sendState != composerSendIdle && layout.width >= 36 {
		label += " - " + string(active.sendState)
	}
	if layout.width < 28 {
		label = " >"
	}
	input := active.composerText
	if input == "" {
		input = "Type a message..."
	}
	input = " " + fitLine(input, clampMin(layout.width-4, 1))
	if !layout.composerFramed {
		if active.replyTo != nil {
			input = m.replyContextLine(layout.width) + "\n" + input
		}
		return fitBlock(input, layout.width, layout.composerHeight)
	}

	lines := []string{}
	if active.replyTo != nil && layout.composerContentHeight >= 3 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted)).Background(lipgloss.Color(m.theme.Background)).Italic(true).Render(m.replyContextLine(layout.width-4)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Background(lipgloss.Color(m.theme.Background)).Render(label),
		lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Foreground)).Background(lipgloss.Color(m.theme.Background)).Render(input),
	)
	box := lipgloss.JoinVertical(lipgloss.Left, lines...)

	if layout.composerContentHeight == 1 {
		box = lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Foreground)).Background(lipgloss.Color(m.theme.Background)).Render(input)
	}

	borderColor := lipgloss.Color(m.theme.Border)
	if m.focus == mockFocusComposer && !m.anyOverlayOpen() {
		borderColor = lipgloss.Color(m.theme.Accent)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.composerContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(box)
}

// emotesView renders the third dashboard row: a horizontal quick-select
// strip of available emotes. Left/right move the selection when focused;
// enter/tab appends the selected emote name to the composer (see
// insertSelectedEmote). Ctrl+E opens the larger searchable modal for emotes
// not in this glanceable strip.
func (m mockShellModel) emotesView(layout mockShellLayout) string {
	entries := m.activeEmoteEntries()
	contentWidth := layout.width
	if layout.emotesFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}

	label := " Emotes"
	var line string
	switch {
	case len(entries) == 0:
		line = label + ": (resolving...)"
	default:
		selected := m.clampedEmoteSelected(entries)
		parts := make([]string, 0, len(entries))
		for i, entry := range entries {
			name := entry.Name
			if i == selected && m.focus == mockFocusEmotes {
				name = "[" + name + "]"
			}
			parts = append(parts, name)
		}
		line = label + ": " + strings.Join(parts, " ")
	}
	content := fitLine(line, contentWidth)

	if !layout.emotesFramed {
		return fitBlock(content, layout.width, layout.emotesHeight)
	}

	borderColor := lipgloss.Color(m.theme.Border)
	if m.focus == mockFocusEmotes && !m.anyOverlayOpen() {
		borderColor = lipgloss.Color(m.theme.Accent)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.emotesContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(content)
}

// clampedEmoteSelected keeps emoteSelected in range as the entry list
// changes size (e.g. resolves from empty to populated).
func (m mockShellModel) clampedEmoteSelected(entries []assets.EmoteEntry) int {
	if len(entries) == 0 {
		return 0
	}
	selected := m.emoteSelected
	if selected < 0 {
		selected = 0
	}
	if selected >= len(entries) {
		selected = len(entries) - 1
	}
	return selected
}

func (m *mockShellModel) moveEmoteSelection(delta int) {
	entries := m.activeEmoteEntries()
	if len(entries) == 0 {
		m.emoteSelected = 0
		return
	}
	selected := m.clampedEmoteSelected(entries) + delta
	if selected < 0 {
		selected = len(entries) - 1
	}
	if selected >= len(entries) {
		selected = 0
	}
	m.emoteSelected = selected
}

// insertSelectedEmote appends the selected quick-select emote's name plus a
// trailing space to the composer, matching the composer's existing
// append-only text model (no cursor-position tracking).
func (m *mockShellModel) insertSelectedEmote() {
	entries := m.activeEmoteEntries()
	if len(entries) == 0 {
		return
	}
	name := entries[m.clampedEmoteSelected(entries)].Name
	m.activeChannelState().composerText += name + " "
}

func (m mockShellModel) helpView(width, height int) string {
	lines := m.helpLines(width, height)
	for i := range lines {
		lines[i] = fitLine(lines[i], width)
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color(m.theme.Muted)).
		Background(lipgloss.Color(m.theme.Surface)).
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

// terminalRowString renders row to an exact-width terminal line. background
// (the active theme's background) is applied explicitly to every fragment
// and to the trailing width-padding, rather than relying on an outer
// lipgloss wrap: fragments each end in their own ANSI reset, so an outer
// Background() applied after the fact only colors text up to the row's
// first reset (see Row.TerminalStringWithBackground) — real terminals then
// show their own default background, which some render as transparent, for
// everything after that.
func terminalRowString(row render.Row, width int, background string) string {
	if width <= 0 {
		return ""
	}
	if row.Width() > width {
		return backgroundStyledLine(fitLine(row.Plain(), width), background)
	}
	out := row.TerminalStringWithBackground(background)
	if padding := width - row.Width(); padding > 0 {
		out += backgroundStyledLine(strings.Repeat(" ", padding), background)
	}
	return out
}

// backgroundStyledLine wraps plain (non-ANSI) text in an explicit background
// style so it renders opaque instead of falling through to the terminal's
// own default/transparent background. Safe to call with already-plain text
// only (never pre-styled ANSI content — see terminalRowString's doc comment
// for why wrapping already-styled content doesn't work).
func backgroundStyledLine(text string, background string) string {
	if text == "" || strings.TrimSpace(background) == "" {
		return text
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(background)).Render(text)
}

func (m mockShellModel) layout() mockShellLayout {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)
	layout := mockShellLayout{
		width:        width,
		chatWidth:    width,
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
	if m.activeChannelState().replyTo != nil {
		layout.composerHeight++
		layout.composerContentHeight++
	}
	layout.composerFramed = width >= 5
	if height < 10 {
		layout.composerHeight = 3
		layout.composerContentHeight = 1
	}

	if height >= 12 {
		layout.emotesHeight = 3
		layout.emotesContentHeight = 1
		layout.emotesFramed = width >= 5
	}

	remaining := height - layout.statusHeight - layout.helpHeight - layout.composerHeight - layout.emotesHeight
	if remaining < 3 && layout.emotesHeight > 0 {
		layout.emotesHeight = 0
		layout.emotesContentHeight = 0
		layout.emotesFramed = false
		remaining = height - layout.statusHeight - layout.helpHeight - layout.composerHeight
	}
	if remaining < 3 && layout.composerHeight > 3 {
		layout.composerHeight = 3
		layout.composerContentHeight = 1
		remaining = height - layout.statusHeight - layout.helpHeight - layout.composerHeight - layout.emotesHeight
	}
	if remaining < 1 && layout.helpHeight > 0 {
		layout.helpHeight = 0
		remaining = height - layout.statusHeight - layout.composerHeight - layout.emotesHeight
	}
	if remaining < 1 && layout.composerHeight > 0 {
		layout.composerHeight = clampMin(height-layout.statusHeight-layout.emotesHeight, 0)
		layout.composerContentHeight = clampMin(layout.composerHeight-2, 0)
		layout.composerFramed = layout.composerHeight >= 3 && width >= 5
		remaining = height - layout.statusHeight - layout.composerHeight - layout.emotesHeight
	}

	if m.palette.open && remaining >= 4 {
		layout.paletteHeight = 5
		if height >= 18 {
			layout.paletteHeight = 7
		}
		if layout.paletteHeight > remaining-1 {
			layout.paletteHeight = remaining - 1
		}
		if layout.paletteHeight < 3 {
			layout.paletteHeight = 0
		}
		layout.paletteFramed = layout.paletteHeight >= 3 && width >= 5
		layout.paletteContentHeight = layout.paletteHeight
		if layout.paletteFramed {
			layout.paletteContentHeight = layout.paletteHeight - 2
		}
		remaining -= layout.paletteHeight
	}

	if !m.palette.open && m.inspectOpen && remaining >= 4 {
		layout.inspectHeight = 5
		if height >= 18 {
			layout.inspectHeight = 7
		}
		if layout.inspectHeight > remaining-1 {
			layout.inspectHeight = remaining - 1
		}
		if layout.inspectHeight < 3 {
			layout.inspectHeight = 0
		}
		layout.inspectFramed = layout.inspectHeight >= 3 && width >= 5
		layout.inspectContentHeight = layout.inspectHeight
		if layout.inspectFramed {
			layout.inspectContentHeight = layout.inspectHeight - 2
		}
		remaining -= layout.inspectHeight
	}

	if !m.palette.open && !m.inspectOpen && m.emotePicker.open && remaining >= 4 {
		layout.emotePickerHeight = 5
		if height >= 18 {
			layout.emotePickerHeight = 7
		}
		if layout.emotePickerHeight > remaining-1 {
			layout.emotePickerHeight = remaining - 1
		}
		if layout.emotePickerHeight < 3 {
			layout.emotePickerHeight = 0
		}
		layout.emotePickerFramed = layout.emotePickerHeight >= 3 && width >= 5
		layout.emotePickerContentHeight = layout.emotePickerHeight
		if layout.emotePickerFramed {
			layout.emotePickerContentHeight = layout.emotePickerHeight - 2
		}
		remaining -= layout.emotePickerHeight
	}

	if !m.palette.open && !m.inspectOpen && !m.emotePicker.open && m.themeSettings.open && remaining >= 4 {
		layout.themeSettingsHeight = 5
		if height >= 18 {
			layout.themeSettingsHeight = 7
		}
		if layout.themeSettingsHeight > remaining-1 {
			layout.themeSettingsHeight = remaining - 1
		}
		if layout.themeSettingsHeight < 3 {
			layout.themeSettingsHeight = 0
		}
		layout.themeSettingsFramed = layout.themeSettingsHeight >= 3 && width >= 5
		layout.themeSettingsContentHeight = layout.themeSettingsHeight
		if layout.themeSettingsFramed {
			layout.themeSettingsContentHeight = layout.themeSettingsHeight - 2
		}
		remaining -= layout.themeSettingsHeight
	}

	layout.chatHeight = clampMin(remaining, 0)
	layout.sidebarWidth = m.sidebarWidth(width, layout.chatHeight)
	layout.chatWidth = clampMin(width-layout.sidebarWidth, 1)
	layout.chatFramed = layout.chatHeight >= 3 && width >= 5
	layout.chatContentHeight = layout.chatHeight
	if layout.chatFramed {
		layout.chatContentHeight = layout.chatHeight - 2
	}
	layout.sidebarContentHeight = layout.chatHeight - 2
	if layout.sidebarContentHeight < 0 {
		layout.sidebarContentHeight = 0
	}
	if layout.chatContentHeight < 0 {
		layout.chatContentHeight = 0
	}

	used := layout.statusHeight + layout.chatHeight + layout.paletteHeight + layout.inspectHeight + layout.emotePickerHeight + layout.themeSettingsHeight + layout.composerHeight + layout.emotesHeight + layout.helpHeight
	if used < height {
		layout.chatHeight += height - used
		if layout.chatFramed {
			layout.chatContentHeight = layout.chatHeight - 2
		} else {
			layout.chatContentHeight = layout.chatHeight
		}
		layout.sidebarContentHeight = layout.chatHeight - 2
		if layout.sidebarContentHeight < 0 {
			layout.sidebarContentHeight = 0
		}
	}

	return layout
}

func (m mockShellModel) sidebarWidth(width, chatHeight int) int {
	if width < sidebarMinWidth || chatHeight < 3 || len(m.channels.channelNames()) < 2 {
		return 0
	}
	if width >= 112 {
		return sidebarWideSize
	}
	return sidebarNormalSize
}

func (m mockShellModel) chatRowWidth(layout mockShellLayout) int {
	rowWidth := layout.chatWidth
	if layout.chatFramed {
		rowWidth = layout.chatWidth - 4
	}
	return clampMin(rowWidth, 1)
}

func (m *mockShellModel) cycleFocus() {
	switch m.focus {
	case mockFocusChat:
		m.focus = mockFocusComposer
	case mockFocusComposer:
		m.focus = mockFocusEmotes
	default:
		m.focus = mockFocusChat
	}
}

func messageFilterForShortcutRune(r rune) (messageFilter, bool) {
	for _, def := range messageFilterDefinitions {
		if def.shortcut == string(r) {
			return def.filter, true
		}
	}
	return 0, false
}

func (m *mockShellModel) toggleActiveMessageFilter(filter messageFilter) tea.Cmd {
	m.activeChannelState().messageFilters.toggle(filter)
	m.clampScroll()
	return m.asyncAssetCommand()
}

func (m *mockShellModel) resetActiveMessageFilters() tea.Cmd {
	state := m.activeChannelState()
	if !state.messageFilters.active() {
		return nil
	}
	state.messageFilters.reset()
	m.clampScroll()
	return m.asyncAssetCommand()
}

func (m *mockShellModel) scrollBy(delta int) {
	if delta == 0 {
		delta = 1
	}
	m.activeChannelState().scrollOffset += delta
	m.clampScroll()
}

func (m *mockShellModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouseEnabled {
		return *m, nil
	}

	layout := m.layout()
	event := tea.MouseEvent(msg)
	if m.mouseInChatRegion(event, layout) {
		switch {
		case isMouseWheelUp(event):
			m.scrollBy(3)
			return *m, nil
		case isMouseWheelDown(event):
			m.scrollBy(-3)
			return *m, nil
		}
	}

	if !isMouseLeftPress(event) {
		return *m, nil
	}

	if channel, ok := m.channelAtMouse(event, layout); ok {
		m.focus = mockFocusChat
		if m.channels.setActive(channel) {
			m.clampScroll()
			return m.withAsyncAssetCommands(nil)
		}
		return *m, nil
	}
	if m.mouseInComposer(event, layout) {
		m.focus = mockFocusComposer
		return *m, nil
	}
	if message, ok := m.messageAtMouse(event, layout); ok {
		m.focus = mockFocusChat
		m.activeChannelState().replyTo = replyContextFromMessage(message)
		return *m, nil
	}
	if m.mouseInChatRegion(event, layout) {
		m.focus = mockFocusChat
	}
	return *m, nil
}

func isMouseWheelUp(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonWheelUp
}

func isMouseWheelDown(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonWheelDown
}

func isMouseLeftPress(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonLeft && event.Action == tea.MouseActionPress
}

func (m mockShellModel) mouseInChatRegion(event tea.MouseEvent, layout mockShellLayout) bool {
	chatTop := layout.statusHeight
	chatLeft := layout.sidebarWidth
	return event.X >= chatLeft &&
		event.X < layout.width &&
		event.Y >= chatTop &&
		event.Y < chatTop+layout.chatHeight
}

func (m mockShellModel) mouseInComposer(event tea.MouseEvent, layout mockShellLayout) bool {
	composerTop := layout.statusHeight + layout.chatHeight
	return event.X >= 0 &&
		event.X < layout.width &&
		event.Y >= composerTop &&
		event.Y < composerTop+layout.composerHeight
}

func (m mockShellModel) channelAtMouse(event tea.MouseEvent, layout mockShellLayout) (string, bool) {
	if layout.sidebarWidth <= 0 || event.X < 0 || event.X >= layout.sidebarWidth {
		return "", false
	}
	chatTop := layout.statusHeight
	if event.Y < chatTop+1 || event.Y >= chatTop+layout.chatHeight-1 {
		return "", false
	}
	contentRow := event.Y - chatTop - 1
	channelIndex := contentRow - 1
	if channelIndex < 0 || channelIndex >= len(m.channels.order) {
		return "", false
	}
	state := m.channels.states[m.channels.order[channelIndex]]
	if state == nil {
		return "", false
	}
	return state.name, true
}

func (m mockShellModel) messageAtMouse(event tea.MouseEvent, layout mockShellLayout) (twitch.ChatMessage, bool) {
	if !m.mouseInChatRegion(event, layout) || layout.chatContentHeight <= 0 {
		return twitch.ChatMessage{}, false
	}
	chatTop := layout.statusHeight
	contentTop := chatTop
	if layout.chatFramed {
		contentTop++
	}
	contentRow := event.Y - contentTop
	if contentRow < 0 || contentRow >= layout.chatContentHeight {
		return twitch.ChatMessage{}, false
	}
	return m.messageAtVisibleChatRow(layout, contentRow)
}

func (m mockShellModel) messageAtVisibleChatRow(layout mockShellLayout, contentRow int) (twitch.ChatMessage, bool) {
	active := m.activeChannelState()
	blocks := m.visibleChatRowBlocks(layout)
	totalRows := chatRowBlockCount(blocks)

	start := totalRows - layout.chatContentHeight - active.scrollOffset
	if start < 0 {
		start = 0
	}
	target := start + contentRow
	if target < 0 || target >= totalRows {
		return twitch.ChatMessage{}, false
	}

	cursor := 0
	for _, block := range blocks {
		next := cursor + chatRowBlockRowCount(block)
		if target >= cursor && target < next {
			return selectableMessage(block.message)
		}
		cursor = next
	}
	return twitch.ChatMessage{}, false
}

func selectableMessage(message twitch.ChatMessage) (twitch.ChatMessage, bool) {
	if strings.TrimSpace(message.ID) == "" {
		return twitch.ChatMessage{}, false
	}
	return message, true
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
	if state := m.channels.ensure(message.Channel); state != nil {
		state.removeLocalEcho(message.ID)
	}
	m.recordChatBytes(message)
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
	rowWidth := m.chatRowWidth(layout)

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

func (m *mockShellModel) maybeNotifyForSystemEvent(message twitch.ChatMessage) tea.Cmd {
	if !m.shouldNotifyForSystemEvent(message) {
		return nil
	}
	if message.Channel == "" {
		message.Channel = m.activeChannelName()
	}
	notification, ok := systemNotificationFromMessage(message)
	if !ok {
		return nil
	}
	m.lastSystemNotification = &notification
	if m.systemNotifier == nil {
		return nil
	}
	notifier := m.systemNotifier
	return func() tea.Msg {
		_ = notifier.Notify(context.Background(), notification)
		return nil
	}
}

func (m mockShellModel) shouldNotifyForSystemEvent(message twitch.ChatMessage) bool {
	if _, ok := systemNotificationFromMessage(message); !ok {
		return false
	}
	if !m.messageTargetsActiveChannel(message) {
		return true
	}
	if !m.terminalFocused {
		return true
	}
	return m.focus != mockFocusChat || m.anyOverlayOpen()
}

func (m mockShellModel) messageTargetsActiveChannel(message twitch.ChatMessage) bool {
	channel := normalizeChannelName(message.Channel)
	if channel == "" {
		channel = m.activeChannelName()
	}
	if m.channels == nil {
		return channelKey(channel) == channelKey(m.activeChannelName())
	}
	return channelKey(channel) == m.channels.active
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
	if preserveScrolledView && m.messageVisibleForState(state, message) {
		rowCount = m.staticMessageRowCount(message) - replacedRows
		if rowCount < 0 {
			rowCount = 0
		}
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
	rowWidth := m.chatRowWidth(layout)
	rows := render.Rows(message, m.renderOptions(rowWidth))
	if len(rows) == 0 {
		return 1
	}
	return len(rows)
}

func (m *mockShellModel) removeActiveReveal(id string) {
	state := m.activeChannelState()
	state.removeActiveRevealID(id)
}

func (s *channelState) removeActiveRevealID(id string) {
	if s == nil {
		return
	}
	for i, activeID := range s.activeOrder {
		if activeID != id {
			continue
		}
		copy(s.activeOrder[i:], s.activeOrder[i+1:])
		s.activeOrder = s.activeOrder[:len(s.activeOrder)-1]
		return
	}
}

// scheduleFrameTick starts the shared animation clock. It runs continuously
// (not just while something is mid-animation) whenever animation is enabled,
// driving the pulsing status indicators, scene-switch flash, startup splash,
// and command-palette typewriter reveal from one ticker.
func (m *mockShellModel) scheduleFrameTick() tea.Cmd {
	if m.frameTickScheduled || m.animationMode == string(animation.ModeOff) {
		return nil
	}
	m.frameTickScheduled = true
	return animation.ScheduleFrame(animation.DefaultFrameInterval)
}

// advanceFrame runs once per animation-clock tick. It records the frame for
// FPS measurement and advances the command-palette typewriter reveal; scene
// flash and splash simply expire based on wall-clock deadlines checked at
// render time, so they need no per-tick bookkeeping here.
func (m *mockShellModel) advanceFrame(now time.Time) {
	m.lastFrameAt = now
	m.frameTimestamps = append(m.frameTimestamps, now)
	cutoff := now.Add(-time.Second)
	trimmed := m.frameTimestamps[:0]
	for _, ts := range m.frameTimestamps {
		if ts.After(cutoff) {
			trimmed = append(trimmed, ts)
		}
	}
	m.frameTimestamps = trimmed
	m.sampleResourceUsage(now)
	m.trimChatByteSamples(now)
	if m.palette.open {
		m.refreshPaletteReveal(now)
	}
}

// triggerSceneFlash briefly highlights the chat border on channel switch,
// the TUI's closest analog to an OBS "scene switch" transition. It is a
// no-op when animation is disabled.
func (m *mockShellModel) triggerSceneFlash() {
	if m.animationMode == string(animation.ModeOff) {
		return
	}
	m.sceneFlashUntil = time.Now().Add(sceneFlashDuration)
}

func (m mockShellModel) sceneFlashActive() bool {
	return !m.sceneFlashUntil.IsZero() && time.Now().Before(m.sceneFlashUntil)
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
	if cmd := m.scheduleBroadcasterIDLookup(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.scheduleEmoteIndexLookup(); cmd != nil {
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
	blocks := m.visibleChatRowBlocks(layout)
	totalRows := chatRowBlockCount(blocks)

	start := totalRows - layout.chatContentHeight - active.scrollOffset
	if start < 0 {
		start = 0
	}
	end := start + layout.chatContentHeight
	messages := make([]twitch.ChatMessage, 0, layout.chatContentHeight)
	cursor := 0
	for _, block := range blocks {
		next := cursor + chatRowBlockRowCount(block)
		if rangesOverlap(cursor, next, start, end) {
			messages = append(messages, block.message)
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
	requests, retryAt := m.pendingAssetRequestsAt(time.Now())
	if len(requests) == 0 {
		if !retryAt.IsZero() && !m.assetRetryScheduled {
			delay := time.Until(retryAt)
			if delay < assetLookupDelay {
				delay = assetLookupDelay
			}
			m.assetRetryScheduled = true
			return tea.Tick(delay, func(time.Time) tea.Msg {
				return assetLookupTickMsg{}
			})
		}
		return nil
	}
	m.assetLookupScheduled = true
	return tea.Tick(assetLookupDelay, func(time.Time) tea.Msg {
		return assetLookupTickMsg{}
	})
}

func (m mockShellModel) resolveAssetsCommand(requests []assets.Request) tea.Cmd {
	resolver := m.assetResolver
	preparer := m.imagePreparer
	renderer := m.imageRenderer
	permanentFailures := cloneAssetPermanentFailures(m.assetPermanentFailure)
	requests = boundedAssetRequests(requests, assetWorkQueueMax)
	return func() tea.Msg {
		results := resolveAssetRequests(context.Background(), requests, resolver, preparer, renderer, permanentFailures)
		return assetPreparedBatchMsg{results: results}
	}
}

func resolveAssetRequests(ctx context.Context, requests []assets.Request, resolver assets.EventResolver, preparer render.ImagePreparer, renderer render.ImageRenderer, permanentFailures map[assetPermanentFailureKey]struct{}) []assetPreparedMsg {
	if ctx == nil {
		ctx = context.Background()
	}
	requests = boundedAssetRequests(requests, assetWorkQueueMax)
	if len(requests) == 0 {
		return nil
	}
	results := make([]assetPreparedMsg, len(requests))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < assetWorkerCount(len(requests)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = resolveAssetRequest(ctx, requests[index], resolver, preparer, renderer, permanentFailures)
			}
		}()
	}
	for i := range requests {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func resolveAssetRequest(ctx context.Context, request assets.Request, resolver assets.EventResolver, preparer render.ImagePreparer, renderer render.ImageRenderer, permanentFailures map[assetPermanentFailureKey]struct{}) assetPreparedMsg {
	workCtx, cancel := context.WithTimeout(ctx, assetWorkTimeout)
	defer cancel()

	event := resolver.Resolve(workCtx, request)
	spec := imageSpecFromAssetRequest(request, event)
	result := assetPreparedMsg{event: event, spec: spec}
	if event.Kind != assets.EventCacheHit && event.Kind != assets.EventDownloaded {
		return result
	}
	if key, ok := assetPermanentFailureKeyForEvent(event, spec); ok {
		if _, failed := permanentFailures[key]; failed {
			result.permanent = true
			result.failureKey = key
			return result
		}
	}
	record := event.Record
	if preparer != nil {
		prepared, err := preparer.PrepareImage(workCtx, record, spec)
		if err != nil {
			result.err = err
			return result
		}
		record = prepared
	}
	cell, err := renderer.RenderImage(workCtx, record, spec)
	result.cell = cell
	result.err = err
	return result
}

func boundedAssetRequests(requests []assets.Request, limit int) []assets.Request {
	if limit <= 0 || len(requests) <= limit {
		return requests
	}
	return requests[:limit]
}

func assetWorkerCount(requestCount int) int {
	if requestCount <= 0 {
		return 0
	}
	if requestCount < assetWorkParallel {
		return requestCount
	}
	return assetWorkParallel
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
		Channel:     request.Channel,
		ChannelID:   request.ChannelID,
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
	requests, _ := m.pendingAssetRequestsAt(time.Now())
	return requests
}

func (m mockShellModel) pendingAssetRequestsAt(now time.Time) ([]assets.Request, time.Time) {
	if m.assetResolver == nil || m.imageRenderer == nil || !m.imageCapabilityHasActiveAssets() {
		return nil, time.Time{}
	}
	layout := m.layout()
	if layout.chatContentHeight <= 0 {
		return nil, time.Time{}
	}
	rowWidth := m.chatRowWidth(layout)

	seen := make(map[string]bool)
	requests := []assets.Request{}
	var nextRetry time.Time
visibleMessages:
	for _, message := range m.visibleAssetMessages() {
		for _, row := range render.Rows(message, m.renderOptions(rowWidth)) {
			for _, fragment := range row.Fragments {
				request, ok := m.assetRequestForFragment(message, fragment)
				if !ok || seen[request.ID] {
					continue
				}
				if m.assetRequested[request.ID] {
					retryAt, hasRetry := m.assetRetryAfter[request.ID]
					if !hasRetry || now.Before(retryAt) {
						if hasRetry {
							nextRetry = earliestNonZeroTime(nextRetry, retryAt)
						}
						continue
					}
				}
				seen[request.ID] = true
				requests = append(requests, request)
				if len(requests) >= assetWorkQueueMax {
					break visibleMessages
				}
			}
		}
	}
	return requests, nextRetry
}

func (m mockShellModel) assetRequestForFragment(message twitch.ChatMessage, fragment render.Fragment) (assets.Request, bool) {
	if !m.assetFragmentEnabled(fragment) {
		return assets.Request{}, false
	}
	key, ok := render.ImageCellKeyForRefInChannel(fragment.Ref, message.ChannelID, message.Channel)
	if !ok {
		return assets.Request{}, false
	}
	if _, ok := m.imageCells[key]; ok {
		if cell := m.imageCells[key]; cell.Text != "" && cell.WidthCells == fragment.Width() {
			return assets.Request{}, false
		}
	}
	widthCells := fragment.Width()
	request := assets.Request{
		ID:          assetRequestID(fragment.Ref, message.ChannelID, message.Channel, widthCells, 1),
		Ref:         fragment.Ref,
		Channel:     message.Channel,
		ChannelID:   strings.TrimSpace(message.ChannelID),
		UserID:      message.AuthorID,
		UserLogin:   message.AuthorLogin,
		Fallback:    fragment.Text,
		WidthCells:  widthCells,
		HeightCells: 1,
	}
	if request.ID == "" || request.WidthCells <= 0 {
		return assets.Request{}, false
	}
	return request, true
}

func (m mockShellModel) assetFragmentEnabled(fragment render.Fragment) bool {
	if !m.assetKindEnabled(fragment.Ref.Kind) {
		return false
	}
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

func (m mockShellModel) assetKindEnabled(kind string) bool {
	if len(m.assetKinds) == 0 {
		return true
	}
	return m.assetKinds[kind]
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

func assetRequestID(ref twitch.AssetRef, channelID, channel string, dimensions ...int) string {
	key, ok := render.ImageCellKeyForRefInChannel(ref, channelID, channel)
	if !ok {
		return ""
	}
	if unsafeAssetStateIdentity(key.Kind) || unsafeAssetStateIdentity(key.ID) || unsafeAssetStateIdentity(key.ChannelIdentity) {
		return ""
	}
	id := key.Kind + "\x00" + key.ID
	if key.ChannelIdentity != "" {
		id = key.Kind + "\x00" + key.ChannelIdentity + "\x00" + key.ID
	}
	if len(dimensions) == 0 {
		return id
	}
	width := dimensions[0]
	height := 1
	if len(dimensions) > 1 {
		height = dimensions[1]
	}
	if width <= 0 || height <= 0 {
		return ""
	}
	return fmt.Sprintf("%s\x00cells:%dx%d", id, width, height)
}

func (m *mockShellModel) applyAssetResults(results []assetPreparedMsg) {
	if len(results) == 0 {
		return
	}
	if m.imageCells == nil {
		m.imageCells = make(map[render.ImageCellKey]render.ImageCell)
	}
	for _, result := range results {
		if result.permanent || result.err != nil || (result.event.Kind != assets.EventCacheHit && result.event.Kind != assets.EventDownloaded) {
			if m.recordPermanentAssetFailure(result) {
				continue
			}
			m.forgetAssetRequest(result.event.RequestID)
			continue
		}
		if result.cell.Text == "" {
			continue
		}
		key, ok := imageCellKeyFromAssetEvent(result.event, result.spec)
		if !ok {
			continue
		}
		m.imageCells[key] = result.cell
		m.clearAssetRequestRetry(result.event.RequestID)
	}
}

func (m *mockShellModel) recordPermanentAssetFailure(result assetPreparedMsg) bool {
	if !result.permanent && !isPermanentAssetFailure(result) {
		return false
	}
	key := result.failureKey
	if key == (assetPermanentFailureKey{}) {
		var ok bool
		key, ok = assetPermanentFailureKeyForEvent(result.event, result.spec)
		if !ok {
			return false
		}
	}
	if m.assetPermanentFailure == nil {
		m.assetPermanentFailure = make(map[assetPermanentFailureKey]struct{})
	}
	m.assetPermanentFailure[key] = struct{}{}
	if result.event.RequestID != "" {
		if m.assetRetryAfter == nil {
			m.assetRetryAfter = make(map[string]time.Time)
		}
		m.assetRetryAfter[result.event.RequestID] = time.Now().Add(assetFailureRetry)
	}
	return true
}

func isPermanentAssetFailure(result assetPreparedMsg) bool {
	err := result.err
	if err == nil {
		err = result.event.Err
	}
	return render.IsPermanentImageFailure(err) ||
		errors.Is(err, storage.ErrUnsafeAssetKey) ||
		errors.Is(err, storage.ErrUnsafeAssetPath)
}

func assetPermanentFailureKeyForEvent(event assets.Event, spec render.ImageSpec) (assetPermanentFailureKey, bool) {
	assetKey, ok := render.ImageCellKeyForRefInChannel(spec.Ref, spec.ChannelID, spec.Channel)
	if !ok {
		assetKey, ok = imageCellKeyFromAssetEvent(event, spec)
	}
	if !ok {
		return assetPermanentFailureKey{}, false
	}
	if unsafeAssetStateIdentity(assetKey.Kind) || unsafeAssetStateIdentity(assetKey.ID) || unsafeAssetStateIdentity(assetKey.ChannelIdentity) {
		return assetPermanentFailureKey{}, false
	}

	key := assetPermanentFailureKey{
		AssetKind:            assetKey.Kind,
		AssetID:              assetKey.ID,
		ChannelIdentity:      assetKey.ChannelIdentity,
		RecordWidthCells:     event.Record.WidthCells,
		RecordHeightCells:    event.Record.HeightCells,
		FetchedAtUnixNano:    unixNanoOrZero(event.Record.FetchedAt),
		RequestedWidthCells:  firstPositiveInt(spec.WidthCells, event.Record.WidthCells, 1),
		RequestedHeightCells: firstPositiveInt(spec.HeightCells, event.Record.HeightCells, 1),
	}
	if kind, id, unsafe, ok := safeRecordIdentity(event.Record.Key); ok {
		key.RecordKind = kind
		key.RecordID = id
		key.RecordUnsafe = unsafe
	}
	if identity, unsafe, ok := safePayloadIdentity(event.Record.PayloadIdentity); ok {
		key.PayloadIdentity = identity
		key.PayloadIdentityUnsafe = unsafe
	}
	if mediaType, ok := safeAssetFailureText(event.Record.MediaType); ok {
		key.MediaType = mediaType
	} else {
		key.MediaTypeUnsafe = true
	}
	return key, true
}

func safeRecordIdentity(recordKey storage.AssetKey) (kind, id string, unsafe, ok bool) {
	kind = strings.TrimSpace(recordKey.Kind)
	id = strings.TrimSpace(recordKey.ID)
	if kind == "" && id == "" {
		return "", "", false, false
	}
	if unsafeAssetStateIdentity(kind) || unsafeAssetStateIdentity(id) {
		return "", "", true, true
	}
	return kind, id, false, true
}

func safePayloadIdentity(value string) (identity string, unsafe, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, false
	}
	if !safePayloadDigestIdentity(value) {
		return "", true, true
	}
	return value, false, true
}

func safePayloadDigestIdentity(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, r := range value[len(prefix):] {
		if r < '0' || r > '9' {
			if r < 'a' || r > 'f' {
				return false
			}
		}
	}
	return true
}

func safeAssetFailureText(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", true
	}
	if semicolon := strings.IndexByte(value, ';'); semicolon >= 0 {
		value = strings.TrimSpace(value[:semicolon])
	}
	if containsUnsafeAssetStateText(value) {
		return "", false
	}
	return value, true
}

func unsafeAssetStateIdentity(value string) bool {
	return containsUnsafeAssetStateText(value) || looksLikeLocalPath(value)
}

func containsUnsafeAssetStateText(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "://") {
		return true
	}
	markers := []string{
		"oauth:",
		"oauth_token=",
		"access_token=",
		"refresh_token=",
		"client_secret=",
		"client-secret=",
		"authorization=",
		"authorization: bearer",
		"bearer ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func looksLikeLocalPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "./") || value == "." || value == ".." {
		return true
	}
	if strings.Contains(value, "\\") {
		return true
	}
	if len(value) >= 3 && value[1] == ':' && ((value[2] == '/') || (value[2] == '\\')) && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return true
	}
	if strings.HasPrefix(value, "../") || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") {
		return true
	}
	if strings.Contains(value, "/") {
		last := value[strings.LastIndex(value, "/")+1:]
		for _, suffix := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".bin", ".tmp"} {
			if strings.HasSuffix(lower, suffix) || strings.HasSuffix(strings.ToLower(last), suffix) {
				return true
			}
		}
	}
	return false
}

func cloneAssetPermanentFailures(src map[assetPermanentFailureKey]struct{}) map[assetPermanentFailureKey]struct{} {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[assetPermanentFailureKey]struct{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneAssetKinds(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (m *mockShellModel) clearAssetRequestRetry(id string) {
	if id == "" || m.assetRetryAfter == nil {
		return
	}
	delete(m.assetRetryAfter, id)
}

func earliestNonZeroTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func unixNanoOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (m *mockShellModel) forgetAssetRequest(id string) {
	if id == "" || m.assetRequested == nil {
		return
	}
	delete(m.assetRequested, id)
	m.clearAssetRequestRetry(id)
}

func imageCellKeyFromAssetEvent(event assets.Event, spec render.ImageSpec) (render.ImageCellKey, bool) {
	if key, ok := render.ImageCellKeyForRefInChannel(event.Ref, spec.ChannelID, spec.Channel); ok {
		return key, true
	}
	if event.Record.Key.Kind != "" && event.Record.Key.ID != "" {
		return render.ImageCellKeyForRefInChannel(twitch.AssetRef{Kind: event.Record.Key.Kind, ID: event.Record.Key.ID}, spec.ChannelID, spec.Channel)
	}
	return render.ImageCellKey{}, false
}

func (m *mockShellModel) refreshActiveRevealRows() {
	state := m.activeChannelState()
	if state.revealQueue == nil || state.revealQueue.Len() == 0 {
		return
	}
	layout := m.layout()
	rowWidth := m.chatRowWidth(layout)
	for _, id := range state.activeOrder {
		message, ok := state.activeMessages[id]
		if !ok {
			continue
		}
		state.revealQueue.ReplaceRows(id, render.Rows(message, m.renderOptions(rowWidth)))
	}
}

func (m *mockShellModel) queueComposerSend() (tea.Model, tea.Cmd) {
	state := m.activeChannelState()
	draft := strings.TrimSpace(state.composerText)
	text, action := composerSendText(draft)
	if text == "" {
		return *m, nil
	}
	if m.client == nil {
		state.sendState = composerSendFailed
		state.sendFeedback = "send unavailable for this chat source"
		return *m, nil
	}

	m.nextSend++
	channel := state.name
	state.sendQueue = append(state.sendQueue, queuedComposerSend{
		ID:               m.nextSend,
		Channel:          channel,
		Text:             text,
		Draft:            draft,
		ReplyToMessageID: replyMessageID(state.replyTo),
		Action:           action,
		Reply:            cloneComposerReply(state.replyTo),
	})
	state.composerText = ""
	state.replyTo = nil
	state.sendState = composerSendQueued
	state.sendFeedback = fmt.Sprintf("queued for #%s", channel)
	m.debugSendQueued(state.sendQueue[len(state.sendQueue)-1])
	return *m, m.startNextComposerSend(state)
}

func (m *mockShellModel) startNextComposerSend(state *channelState) tea.Cmd {
	if state == nil || state.activeSend != nil || len(state.sendQueue) == 0 {
		return nil
	}
	next := state.sendQueue[0]
	state.sendQueue = state.sendQueue[1:]
	state.activeSend = &next
	state.sendState = composerSendSending
	state.sendFeedback = fmt.Sprintf("sending to #%s", next.Channel)
	if next.ReplyToMessageID != "" {
		state.sendFeedback = "sending reply to " + replyAuthor(next.Reply)
	}
	if next.Action {
		state.sendFeedback = "sending action to #" + next.Channel
	}
	m.debugSendStart(next)
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
	state := m.channelStateForActiveSend(msg.id)
	if state == nil || state.activeSend == nil {
		return m, nil
	}

	sent := *state.activeSend
	state.activeSend = nil
	m.debugSendComplete(sent, msg.result, msg.err)
	if msg.err != nil {
		state.sendState = composerSendFailed
		state.sendFeedback = "failed: " + credentialSafeSendDetail(msg.err)
		texts, reply := state.drainUnsentComposerSends(sent)
		state.restoreComposerText(texts...)
		state.replyTo = reply
		return m, nil
	}
	if msg.result.RateLimited {
		state.sendState = composerSendRateLimited
		state.sendFeedback = "rate limited: " + sendResultDetail(msg.result)
		texts, reply := state.drainUnsentComposerSends(sent)
		state.restoreComposerText(texts...)
		state.replyTo = reply
		return m, nil
	}

	state.sendState = composerSendSucceeded
	state.sendFeedback = sendResultDetail(msg.result)
	m.appendLocalEcho(sent, msg.result)
	return m, m.startNextComposerSend(state)
}

func (m *mockShellModel) appendLocalEcho(sent queuedComposerSend, result SendResult) {
	state := m.channels.ensure(sent.Channel)
	if state == nil {
		return
	}
	message := m.localEchoMessage(sent, result, state.name)
	if message.ID != "" {
		if state.localEchoes == nil {
			state.localEchoes = make(map[string]struct{})
		}
		state.localEchoes[message.ID] = struct{}{}
	}
	m.appendStaticMessage(message, channelKey(state.name) == m.channels.active && state.scrollOffset > 0)
}

func (m mockShellModel) localEchoMessage(sent queuedComposerSend, result SendResult, channel string) twitch.ChatMessage {
	at := result.AcceptedAt
	if at.IsZero() && m.channels != nil && m.channels.clock != nil {
		at = m.channels.clock.Now()
	}
	if at.IsZero() {
		at = time.Now()
	}
	id := strings.TrimSpace(result.MessageID)
	if id == "" {
		id = fmt.Sprintf("local-send-%d", sent.ID)
	}
	author := strings.TrimSpace(m.mentionLogin)
	if author == "" {
		author = "me"
	}
	messageType := twitch.MessageTypeChat
	if sent.Action {
		messageType = twitch.MessageTypeAction
	}
	return twitch.ChatMessage{
		ID:          id,
		Channel:     channel,
		Timestamp:   at,
		AuthorLogin: author,
		DisplayName: author,
		AuthorColor: "#9146ff",
		Text:        sent.Text,
		Type:        messageType,
		Reply:       replyFromComposerContext(sent.Reply),
	}
}

func (m mockShellModel) channelStateForActiveSend(id int) *channelState {
	if m.channels == nil {
		return nil
	}
	for _, state := range m.channels.states {
		if state != nil && state.activeSend != nil && state.activeSend.ID == id {
			return state
		}
	}
	return nil
}

func (s *channelState) removeLocalEcho(id string) bool {
	if s == nil || id == "" {
		return false
	}
	if _, ok := s.localEchoes[id]; !ok {
		return false
	}
	delete(s.localEchoes, id)
	for i, message := range s.messages {
		if message.ID == id {
			copy(s.messages[i:], s.messages[i+1:])
			s.messages = s.messages[:len(s.messages)-1]
			return true
		}
	}
	for _, activeID := range s.activeOrder {
		message, ok := s.activeMessages[activeID]
		if ok && message.ID == id {
			delete(s.activeMessages, activeID)
			s.removeActiveRevealID(activeID)
			return true
		}
	}
	return false
}

func (s *channelState) drainUnsentComposerSends(active queuedComposerSend) ([]string, *composerReplyContext) {
	if s == nil {
		return nil, nil
	}
	texts := make([]string, 0, len(s.sendQueue)+1)
	texts = append(texts, active.restoreText())
	for _, queued := range s.sendQueue {
		texts = append(texts, queued.restoreText())
	}
	reply := commonReplyContext(active, s.sendQueue)
	s.sendQueue = nil
	return texts, reply
}

func (s *channelState) restoreComposerText(texts ...string) {
	if s == nil {
		return
	}
	text := strings.TrimSpace(strings.Join(texts, " "))
	if text == "" {
		return
	}
	if strings.TrimSpace(s.composerText) == "" {
		s.composerText = text
		return
	}
	s.composerText = text + " " + s.composerText
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

func replyFromComposerContext(reply *composerReplyContext) *twitch.Reply {
	if reply == nil || reply.MessageID == "" {
		return nil
	}
	return &twitch.Reply{
		ParentMessageID: reply.MessageID,
		ParentLogin:     reply.Author,
		ParentAuthor:    reply.Author,
		ParentText:      reply.Text,
	}
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
	opts.Palette = m.theme
	opts.Assets = m.imageCapability.AssetOptions()
	if len(m.imageCells) > 0 {
		opts.Assets.ImageCells = m.imageCells
	}
	return opts
}

func (m *mockShellModel) deleteComposerRune() {
	state := m.activeChannelState()
	if state.composerText == "" {
		return
	}
	runes := []rune(state.composerText)
	state.composerText = string(runes[:len(runes)-1])
}

func (m *mockShellModel) selectReplyMessage(delta int) {
	messages := m.replyableMessages()
	if len(messages) == 0 {
		return
	}

	index := -1
	state := m.activeChannelState()
	currentID := replyMessageID(state.replyTo)
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

	state.replyTo = replyContextFromMessage(messages[index])
}

func (m *mockShellModel) startReplyMode() {
	state := m.activeChannelState()
	if state.replyTo == nil {
		m.selectReplyMessage(-1)
	}
	if state.replyTo != nil {
		m.focus = mockFocusComposer
	}
}

func (m mockShellModel) replyableMessages() []twitch.ChatMessage {
	active := m.activeChannelState()
	messages := make([]twitch.ChatMessage, 0, len(active.messages)+len(active.activeOrder))
	for _, message := range active.messages {
		if strings.TrimSpace(message.ID) != "" && m.messageVisibleForState(active, message) {
			messages = append(messages, message)
		}
	}
	for _, id := range active.activeOrder {
		message, ok := active.activeMessages[id]
		if !ok || strings.TrimSpace(message.ID) == "" || !m.messageVisibleForState(active, message) {
			continue
		}
		messages = append(messages, message)
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
	reply := m.activeChannelState().replyTo
	if reply == nil {
		return ""
	}
	line := " Replying to " + redactDiagnosticText(replyAuthor(reply))
	if reply.Text != "" {
		line += ": " + redactDiagnosticText(reply.Text)
	}
	return fitLine(line, clampMin(width, 1))
}

func (m mockShellModel) focusName() string {
	if m.palette.open {
		return "palette"
	}
	if m.focus == mockFocusComposer {
		return "composer"
	}
	if m.focus == mockFocusEmotes {
		return "emotes"
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
			return []string{" ^p | tab"}
		}
		if width < 38 {
			return []string{" ctrl+p palette | tab focus"}
		}
		line := " ctrl+p | tab | [] | filt 1-4/0 | ? | pg | r/i | ^l | ^r | q quit/ctrl+c quit"
		if width >= 112 {
			line += " | " + source
		}
		return []string{line}
	}

	lines := []string{
		" tab focus: chat/composer",
		" ctrl+p: commands | [/]: switch channel | 1-4 filters, 0 reset | up/down: select message",
		" r: reply | i: inspect | pgup/pgdn: scroll | ctrl+l: clear | ctrl+r: reconnect | ?: compact help | q: quit | " + source,
	}
	if width < 38 {
		lines = []string{
			" ctrl+p: commands",
			" tab | pgup/pgdn",
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
