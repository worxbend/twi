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
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const (
	defaultShellWidth     = 88
	defaultShellHeight    = 22
	mockIncomingDelay     = 650 * time.Millisecond
	mockRevealDelay       = 20 * time.Millisecond
	sidebarMinWidth       = 72
	sidebarNormalSize     = 18
	sidebarWideSize       = 24
	activityLogMinWidth   = 100
	activityLogNormalSize = 28
	activityLogWideSize   = 34
	// splashDuration is long enough for the startup chat to play through.
	// Any keypress skips it, so the wait is never something a user is stuck
	// with once they know it is there.
	splashDuration = 10 * time.Second
	// sidebarCloseAffordance marks the highlighted sidebar row as closable
	// with x (keyboard) or a click on the glyph itself (mouse).
	sidebarCloseAffordance = " ✕"

	noChannelHeadline      = "No channels open."
	emptyStateIndent       = "  "
	emptyStateScannerGlyph = "◆"
	emptyStateScannerWidth = 16
)

type ClientOptions struct {
	SystemNotifier       SystemNotifier
	StreamStatusResolver twitch.StreamLookup
	EmoteIndex           *assets.EmoteIndex
	DebugLogger          debuglog.Logger
	ChannelManager       twitch.ChannelManager
	GameLookup           twitch.GameLookup
	UserLookup           twitch.UserLookup
	MarkerManager        twitch.MarkerManager
	FollowerLookup       twitch.FollowerLookup
	SubscriptionLookup   twitch.SubscriptionLookup
	ClipManager          twitch.ClipManager
	FollowedChannels     twitch.FollowedChannelLookup
}

type fdWriter interface {
	Fd() uintptr
}

type shellModel struct {
	// --- collaborators and configuration, set once when the shell is built
	//
	// services holds everything this shell reaches Twitch through.
	services        twitchServices
	effectiveConfig config.Config
	debugLogger     debuglog.Logger
	terminalOutput  io.Writer
	theme           theme.Palette
	// mentionLogin is the login whose mentions are highlighted -- whoever
	// the OAuth token belongs to.
	mentionLogin   string
	animationMode  string
	avatarMode     string
	mouseEnabled   bool
	debugRecording bool
	// sourceDetail names where chat is coming from ("live IRC", "mock") for
	// the status line.
	sourceDetail string

	// --- chat content
	//
	// channels holds the per-channel state: backlog, scroll position,
	// composer text, connection status.
	channels *channelStateSet
	// rowCache memoizes per-message rendered rows across repaints. It is a
	// pointer so the copies Bubble Tea makes on every Update share one cache.
	rowCache *chatRowCache
	// incoming, nextIncoming and nextReveal drive the mock chat source only.
	incoming     []twitch.ChatMessage
	nextIncoming int
	nextReveal   int

	// --- terminal and focus
	width           int
	height          int
	focus           shellFocus
	terminalFocused bool
	activeTab       shellTab

	// --- grouped state, each covered by its own type below
	display  displayState
	panes    paneState
	frames   frameState
	activity activityState
	metrics  channelMetricsState
	runtime  runtimeMetricsState
	emotes   emoteIndexState

	// --- overlays and tabs
	palette             commandPaletteState
	themeSettings       themeSettingsState
	emotePicker         emotePickerState
	channelPicker       channelPickerState
	categoryPicker      categoryPickerState
	mentionAutocomplete mentionAutocompleteState
	streamInfo          streamInfoState
	misc                miscState
	helpExpanded        bool
	inspectOpen         bool
	// leaderPending marks an armed space-leader chord.
	leaderPending bool
	// pendingClearChat marks a ctrl+L awaiting confirmation.
	pendingClearChat bool

	// --- in-flight work and one-shot results
	reconnectInFlight         bool
	nextSend                  int
	streamStatusTickScheduled bool
	lastSystemNotification    *SystemNotification
	followedChannelList       []twitch.FollowedChannel
	followedChannelsRequested bool
	// selfBroadcasterID is the token owner's Twitch user ID, resolved once
	// and needed by every broadcaster-scoped Helix call.
	selfBroadcasterID string
}

// twitchServices are the collaborators the shell reaches Twitch through.
//
// They are grouped rather than left loose among the model's other fields
// because they are wiring, not state: each is injected once when the shell is
// built and never reassigned, and any of them may be nil when the credentials
// or configuration that would enable it are missing -- which is how twi keeps
// working, with those features quietly off, for someone who has only ever run
// `twi chat --mock`. Separating them makes the fields that remain on
// shellModel recognisably the things that actually change as you use twi.
//
// The set mirrors ClientOptions, which is how they arrive.
type twitchServices struct {
	// client is the chat transport events are read from and sends go to.
	client ChatClient
	// notifier raises desktop notifications; nil disables them.
	systemNotifier SystemNotifier
	// streamStatusResolver backs the LIVE indicator.
	streamStatusResolver twitch.StreamLookup
	// channelManager reads and writes the Stream Info tab's fields.
	channelManager twitch.ChannelManager
	// gameLookup resolves a category name to a Twitch game ID.
	gameLookup twitch.GameLookup
	// userLookup resolves logins to user IDs, for the broadcaster ID the
	// Helix calls need and for channel-specific emote autocomplete.
	userLookup twitch.UserLookup
	// markerManager creates and lists the Misc tab's stream markers.
	markerManager twitch.MarkerManager
	// clipManager backs the /clip command.
	clipManager twitch.ClipManager
	// followerLookup and subscriptionLookup back the status line's counts.
	followerLookup     twitch.FollowerLookup
	subscriptionLookup twitch.SubscriptionLookup
	// followedChannels backs the channel picker's autocomplete.
	followedChannels twitch.FollowedChannelLookup
}

// channelMetricsState is the follower and subscriber counts shown in the
// status line, and the tick that refreshes them.
//
// The "known" flags exist because zero is a real count: a channel genuinely
// can have no subscribers, and that must read as "0" rather than as "not
// loaded yet", which is what a bare int cannot express.
type channelMetricsState struct {
	followerCount               int
	followerCountKnown          bool
	subscriberCount             int
	subscriberCountKnown        bool
	channelMetricsTickScheduled bool
}

// runtimeMetricsState is what the status line reports about twi itself --
// processor share, resident memory, and how much chat is arriving.
//
// cpuAvailable is separate from cpuPercent because the sampling is
// platform-specific and simply absent on some systems, where the status line
// must omit the figure rather than claim zero.
type runtimeMetricsState struct {
	cpuSampleAt     time.Time
	cpuSampleTime   time.Duration
	cpuPercent      float64
	cpuAvailable    bool
	memoryMB        float64
	chatByteSamples []chatByteSample
}

// activityState backs the activity column: the entries it lists, and the
// bookkeeping that keeps them meaningful.
//
// seenFollowerIDs suppresses repeats, because the followers endpoint returns
// the same recent followers on every poll. The membershipBurst fields collapse
// the flood of JOIN/PART lines Twitch delivers in batches into a single
// summarised entry instead of scrolling everything else away.
type activityState struct {
	activityLog          []activityEntry
	seenFollowerIDs      map[string]bool
	membershipBurstAt    time.Time
	membershipBurstCount int
	membershipBurstIndex int
}

// emoteIndexState is the emote metadata behind autocomplete and the emote
// picker.
//
// emoteEntriesRequested is tracked separately from emoteEntries so a channel
// whose lookup legitimately returned nothing is not re-fetched on every
// keystroke.
type emoteIndexState struct {
	emoteIndex            *assets.EmoteIndex
	emoteEntries          map[string][]assets.EmoteEntry
	emoteEntriesRequested map[string]bool
}

// displayState is the set of rendering choices the display-toggle keys flip.
// They start from the config file and are changed live from the keyboard.
type displayState struct {
	messageLayout   render.LayoutMode
	badgeMode       render.BadgeMode
	highlightEmotes bool
	fullUsername    bool
}

// frameState is the animation clock: what is currently being animated, and
// which timers are already in flight.
//
// The "scheduled" flags matter more than they look. Bubble Tea timers are
// commands, not a running loop, so scheduling one twice makes animation run
// at double speed and burn processor twice over; these flags are what make
// scheduling idempotent. frameTimestamps is the sliding window the status
// line's frame rate is measured over.
type frameState struct {
	frameTickScheduled  bool
	lastFrameAt         time.Time
	splashUntil         time.Time
	splashSkipped       bool
	frameTimestamps     []time.Time
	revealTickScheduled bool
	paletteRevealSeq    animation.Sequence
	paletteRevealKey    string
}

// paneState is the standing show/hide and width choices for the columns
// beside chat.
//
// A zero width override means "size this pane from the terminal width"; a
// configured or resized value is clamped at layout time rather than when it
// is set, because what a terminal can afford is not knowable until it reports
// its size.
type paneState struct {
	activityVisibility    activityVisibility
	sidebarWidthOverride  int
	activityWidthOverride int
	sidebarVisibility     sidebarVisibility
	sidebarSelected       int
}

var _ tea.Model = shellModel{}

// shellTab is a top-level screen selectable from the tab bar. tabChat is the
// zero value so a freshly constructed model always starts on Chat.
type shellTab int

const (
	tabChat shellTab = iota
	tabStreamInfo
	tabMisc
)

// shellTabs lists every tab in display/shortcut order: tab N is switched to
// with Alt+N (1-indexed to match the visible labels).
var shellTabs = []struct {
	tab   shellTab
	label string
}{
	{tabChat, "Chat"},
	{tabStreamInfo, "Stream Info"},
	{tabMisc, "Misc"},
}

// tabForShortcutRune maps an Alt+<digit> keypress to the tab it selects.
// Bubble Tea (and most terminals) cannot distinguish Ctrl+1/Ctrl+2 from a
// plain "1"/"2" keypress, so tab switching uses Alt+<digit> instead - the
// combination terminals reliably report as a distinct, non-conflicting key.
func tabForShortcutRune(r rune) (shellTab, bool) {
	if r < '1' || r > '9' {
		return 0, false
	}
	index := int(r - '1')
	if index >= len(shellTabs) {
		return 0, false
	}
	return shellTabs[index].tab, true
}

type shellFocus int

const (
	focusChat shellFocus = iota
	focusComposer
	focusSidebar
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

type chatRowBlock struct {
	message         twitch.ChatMessage
	rows            []render.Row
	groupIndex      int
	separatorBefore bool
	// continuesGroup marks a message that follows another from the same
	// author, so LayoutGrouped can omit the repeated author header.
	continuesGroup bool
	animating      bool
	// revealed marks a block whose rows are an in-progress animation frame
	// and must not be re-rendered from the message.
	revealed bool
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

type chatClientMembershipMsg struct {
	membership twitch.MembershipEvent
	ok         bool
}

type chatClientUserStateMsg struct {
	state twitch.UserState
	ok    bool
}

type chatClientModerationMsg struct {
	event twitch.ModerationEvent
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

	// No configured channel is a supported start: the shell opens on the
	// empty state and waits for the /channels picker.
	channel := ""
	if len(cfg.DefaultChannels) > 0 {
		channel = cfg.DefaultChannels[0]
	}

	interactive := isInteractiveTerminal(w)
	if interactive && opts.SystemNotifier == nil {
		opts.SystemNotifier = newDefaultSystemNotifier(w)
	}
	model := newLiveModelWithClockAndOptions(channel, cfg, client, nil, opts)
	model.debugAppStart("live", len(configuredChannels(channel, cfg.DefaultChannels)))
	if !interactive {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}
	model.frames.splashUntil = splashDeadline(model.animationMode)
	model.terminalOutput = w
	primeTerminalBackground(w, model.canvasBackground())

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

// newShellModel builds the parts of the model that come from configuration,
// which is everything both the mock and live sources share.
//
// It exists because the two constructors were maintained by hand and drifted:
// the live one silently never read cfg.Features.MessageLayout, BadgeMode,
// HighlightEmotes or FullUsername, so four settings that config parses,
// doctor validates and the display toggles persist to disk did nothing in the
// only mode that talks to Twitch. Callers add only their source-specific
// tail, so a new feature flag cannot be wired into one path and not the other.
func newShellModel(channel string, cfg config.Config, clock animation.Clock) shellModel {
	animationConfig := animationConfigFor(cfg.Features.AnimationMode)
	channels := newChannelStateSet(
		configuredChannels(channel, cfg.DefaultChannels),
		animationConfig,
		clock,
		cfg.Features.ScrollbackLimit,
	)
	return shellModel{
		channels:      channels,
		rowCache:      newChatRowCache(),
		theme:         cfg.ResolveTheme(),
		mentionLogin:  cfg.Twitch.Username,
		animationMode: string(animationConfig.Mode),
		mouseEnabled:  cfg.Features.EnableMouse,
		avatarMode:    cfg.Features.AvatarMode,
		display: displayState{
			messageLayout:   render.NormalizeLayoutMode(cfg.Features.MessageLayout),
			badgeMode:       render.NormalizeBadgeMode(cfg.Features.BadgeMode),
			highlightEmotes: cfg.Features.HighlightEmotes,
			fullUsername:    cfg.Features.FullUsername,
		},
		panes: paneState{
			sidebarWidthOverride:  cfg.Features.SidebarWidth,
			activityWidthOverride: cfg.Features.ActivityWidth,
		},
		debugRecording:  cfg.Debug.Enabled,
		effectiveConfig: cfg,
		activity: activityState{
			// -1 means "no membership burst is being coalesced". Zero would
			// name the first activity row as an open burst before one exists.
			membershipBurstIndex: -1,
		},
		width:           defaultShellWidth,
		height:          defaultShellHeight,
		focus:           focusChat,
		terminalFocused: true,
	}
}

func newLiveModelWithClock(channel string, cfg config.Config, client ChatClient, clock animation.Clock) shellModel {
	return newLiveModelWithClockAndOptions(channel, cfg, client, clock, ClientOptions{})
}

func newLiveModelWithClockAndOptions(channel string, cfg config.Config, client ChatClient, clock animation.Clock, opts ClientOptions) shellModel {
	model := newShellModel(channel, cfg, clock)
	active := model.channels.activeState()
	active.status = ConnectionState{
		Status:  ConnectionConnecting,
		Channel: active.name,
		Detail:  "connecting to Twitch IRC",
		At:      time.Now(),
	}

	model.sourceDetail = "live IRC"
	model.services = twitchServices{
		client:               client,
		systemNotifier:       opts.SystemNotifier,
		streamStatusResolver: opts.StreamStatusResolver,
		channelManager:       opts.ChannelManager,
		gameLookup:           opts.GameLookup,
		userLookup:           opts.UserLookup,
		markerManager:        opts.MarkerManager,
		followerLookup:       opts.FollowerLookup,
		subscriptionLookup:   opts.SubscriptionLookup,
		clipManager:          opts.ClipManager,
		followedChannels:     opts.FollowedChannels,
	}
	model.emotes.emoteIndex = opts.EmoteIndex
	model.emotes.emoteEntries = make(map[string][]assets.EmoteEntry)
	model.emotes.emoteEntriesRequested = make(map[string]bool)
	model.debugLogger = opts.DebugLogger
	return model
}

func (m shellModel) Init() tea.Cmd {
	return tea.Batch(
		m.nextIncomingCommand(),
		m.nextClientMessageCommand(),
		m.nextConnectionStateCommand(),
		m.nextClientMembershipCommand(),
		m.nextClientUserStateCommand(),
		m.nextClientModerationCommand(),
		m.scheduleFrameTick(),
		m.resolveStreamStatusCommand(),
		m.scheduleStreamStatusTick(),
		m.resolveChannelMetricsCommand(),
		m.scheduleChannelMetricsTick(),
	)
}

// Update is Bubble Tea's single entry point for everything that happens to
// the shell: a keypress, a mouse click, a resize, chat arriving, a timer
// firing, or a background request finishing.
//
// Each family of messages has its own handler below, and each handler reports
// whether it recognised the message. No two of them claim the same concrete
// message type, so the order they are tried in is only about readability -
// it cannot change which handler runs.
func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if model, cmd, handled := m.updateTerminalEvent(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.updateChatStream(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.updateTimers(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.updateAsyncResults(msg); handled {
		return model, cmd
	}
	return m, nil
}

// updateTerminalEvent handles what the terminal itself reports: keys, mouse
// activity, resizes, and the window gaining or losing focus.
func (m *shellModel) updateTerminalEvent(msg tea.Msg) (shellModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		model, cmd := m.handleKey(msg)
		return model, cmd, true
	case tea.MouseMsg:
		// The theme page and category picker own the whole screen, so a
		// click has nothing to hit outside them.
		if m.themeSettings.open || m.categoryPicker.open {
			return *m, nil, true
		}
		model, cmd := m.handleMouse(msg)
		return model, cmd, true
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.refreshActiveRevealRows()
		m.clampScroll()
		model, cmd := m.withAsyncAssetCommands(nil)
		return model, cmd, true
	case tea.FocusMsg:
		m.terminalFocused = true
		return *m, nil, true
	case tea.BlurMsg:
		m.terminalFocused = false
		return *m, nil, true
	}
	return *m, nil, false
}

// updateChatStream handles chat arriving from the connected client - or from
// the mock source the demo and the tests use - together with the connection,
// membership, user-state, and moderation events that travel beside it.
//
// Every one of these messages is delivered by a command that read a single
// value off a channel, so each handler asks for the next read and the stream
// keeps flowing. An ok of false means that channel closed and there is
// nothing further to listen for.
func (m *shellModel) updateChatStream(msg tea.Msg) (shellModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case mockIncomingMessageMsg:
		var cmds []tea.Cmd
		if msg.scheduled && msg.index == m.nextIncoming {
			m.nextIncoming++
			cmds = append(cmds, m.nextIncomingCommand())
		}
		cmds = append(cmds, m.ingestMessage(msg.message)...)
		m.clampScroll()
		model, cmd := m.withAsyncAssetCommands(cmds...)
		return model, cmd, true
	case chatClientMessageMsg:
		if !msg.ok {
			m.markStreamClosed("app.message_stream.closed", "chat message stream closed")
			return *m, nil, true
		}
		m.debugChatMessage("app.message.received", msg.message)
		cmds := m.ingestMessage(msg.message)
		cmds = append(cmds, m.nextClientMessageCommand())
		m.clampScroll()
		model, cmd := m.withAsyncAssetCommands(cmds...)
		return model, cmd, true
	case chatClientConnectionStateMsg:
		if !msg.ok {
			// A stream that closes after a deliberate disconnect is
			// expected, and the status already says so, so only an
			// unexpected close is worth reporting.
			if m.activeChannelState().status.Status != ConnectionClosed {
				m.markStreamClosed("app.connection_stream.closed", "connection state stream closed")
			}
			return *m, nil, true
		}
		m.channels.applyConnectionState(msg.state)
		m.debugConnectionState("app.connection_state.received", msg.state)
		cmd := m.nextConnectionStateCommand()
		return *m, cmd, true
	case chatClientMembershipMsg:
		// A closed membership stream is not a chat failure: Twitch simply
		// stops sending membership for busy channels. Stop listening and
		// leave the roster on its message-recency fallback.
		if !msg.ok {
			return *m, nil, true
		}
		m.applyMembershipEvent(msg.membership)
		cmd := m.nextClientMembershipCommand()
		return *m, cmd, true
	case chatClientUserStateMsg:
		if !msg.ok {
			return *m, nil, true
		}
		m.applyUserState(msg.state)
		cmd := m.nextClientUserStateCommand()
		return *m, cmd, true
	case chatClientModerationMsg:
		if !msg.ok {
			return *m, nil, true
		}
		m.applyModeration(msg.event)
		cmd := m.nextClientModerationCommand()
		return *m, cmd, true
	}
	return *m, nil, false
}

// markStreamClosed records that one of the background streams feeding the
// shell has ended. Nothing can be reconnected from here, so it updates the
// connection status the header shows and writes one debug line naming which
// stream stopped: event is the name that line is filed under, detail is the
// wording the user sees.
func (m *shellModel) markStreamClosed(event, detail string) {
	m.channels.applyConnectionState(ConnectionState{
		Status:  ConnectionDisconnected,
		Channel: m.activeChannelName(),
		Detail:  detail,
		At:      time.Now(),
	})
	m.debugConnectionState(event, m.activeChannelState().status)
}

// updateTimers handles the repeating ticks that move the shell forward on
// their own: the animation clock, the per-character message reveal, and the
// polls that refresh stream status and channel metrics. Each tick clears the
// "already scheduled" flag it was armed with and asks for the next one, so
// only ever one timer of each kind is in flight.
func (m *shellModel) updateTimers(msg tea.Msg) (shellModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case animation.FrameMsg:
		m.frames.frameTickScheduled = false
		m.advanceFrame(msg.At)
		cmd := m.scheduleFrameTick()
		return *m, cmd, true
	case streamStatusTickMsg:
		m.streamStatusTickScheduled = false
		cmd := tea.Batch(m.resolveStreamStatusCommand(), m.scheduleStreamStatusTick())
		return *m, cmd, true
	case channelMetricsTickMsg:
		m.metrics.channelMetricsTickScheduled = false
		cmd := tea.Batch(m.resolveChannelMetricsCommand(), m.scheduleChannelMetricsTick())
		return *m, cmd, true
	case mockAnimationTickMsg:
		m.frames.revealTickScheduled = false
		active := m.activeChannelState()
		result := active.revealQueue.Advance()
		m.completeReveals(result.Completed)
		m.clampScroll()
		if active.revealQueue.Len() > 0 {
			cmd := m.scheduleRevealTick()
			return *m, cmd, true
		}
		if result.Changed {
			model, cmd := m.withAsyncAssetCommands(nil)
			return model, cmd, true
		}
		return *m, nil, true
	}
	return *m, nil, false
}

// updateAsyncResults handles the answers to work the shell started earlier
// and did not wait for: a chat message finishing its trip to Twitch, a
// reconnect completing, and the API lookups behind stream status, channel
// metrics, emotes, clips, followed channels, and the stream-info and
// category editors.
func (m *shellModel) updateAsyncResults(msg tea.Msg) (shellModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case composerSendCompletedMsg:
		model, cmd := m.completeComposerSend(msg)
		return model, cmd, true
	case reconnectCompletedMsg:
		m.completeReconnect(msg)
		return *m, nil, true
	case streamStatusResolvedMsg:
		if msg.err == nil {
			m.applyStreamStatusResults(msg.results)
		}
		return *m, nil, true
	case channelMetricsResolvedMsg:
		return m.applyChannelMetrics(msg), nil, true
	case broadcasterIDResolvedMsg:
		m.applyBroadcasterIDResult(msg)
		model, cmd := m.withAsyncAssetCommands(nil)
		return model, cmd, true
	case emoteIndexResolvedMsg:
		m.applyEmoteIndexResult(msg)
		return *m, nil, true
	case followedChannelsResolvedMsg:
		m.applyFollowedChannels(msg)
		return *m, nil, true
	case streamInfoLoadedMsg:
		return m.applyStreamInfoLoaded(msg), nil, true
	case streamInfoSavedMsg:
		return m.applyStreamInfoSaved(msg), nil, true
	case categoryPickerDebounceMsg:
		model, cmd := m.applyCategoryPickerDebounce(msg)
		return model, cmd, true
	case categoryPickerResultsMsg:
		return m.applyCategoryPickerResults(msg), nil, true
	case miscMarkersLoadedMsg:
		return m.applyMiscLoaded(msg), nil, true
	case miscMarkerCreatedMsg:
		return m.applyMiscMarkerCreated(msg), nil, true
	case clipCreatedMsg:
		return m.applyClipCreated(msg), nil, true
	}
	return *m, nil, false
}

// handleKey routes one keypress.
//
// It is separated from Update because keyboard handling is by far the largest
// part of it -- the other twenty-odd message types are a handful of lines
// each -- and mixing the two made Update impossible to read as a whole.
//
// The order below is the precedence of the bindings, and it matters: keys
// that must work everywhere (quit, tab switching, the panel toggles) are
// consumed first, then an open overlay or a non-chat tab gets first refusal
// on what is left, and only then do the chat-view bindings apply.
// handleRuneShortcut handles the single-character shortcuts of the chat view.
//
// Each is gated on where the focus is, because inside the composer these are
// ordinary characters in the message being typed. handled is false when the
// rune means nothing in the current focus, leaving the caller to treat it as
// text.
//
// This used to be nine consecutive `if` statements that each re-tested
// `m.focus == focusChat && len(msg.Runes) == 1` before comparing one rune.
// Testing the focus once and switching on the rune says the same thing, and
// adding a shortcut is now one case rather than another copy of the guard.
func (m *shellModel) handleRuneShortcut(r rune) (shellModel, tea.Cmd, bool) {
	// "?" toggles help everywhere except in the composer, where it is an
	// ordinary character in the message being typed.
	if r == '?' && m.focus != focusComposer {
		m.helpExpanded = !m.helpExpanded
		m.clampScroll()
		return *m, nil, true
	}
	// Pane resizing works from the chat view and from the sidebar, because
	// the sidebar is the one pane whose own width these keys adjust;
	// everywhere else they act on the activity column.
	if m.focus == focusChat || m.focus == focusSidebar {
		switch r {
		case '<':
			m.resizeFocusedPane(-paneResizeStep)
			m.clampScroll()
			return *m, nil, true
		case '>':
			m.resizeFocusedPane(paneResizeStep)
			m.clampScroll()
			return *m, nil, true
		case '=':
			m.resetPaneWidths()
			m.clampScroll()
			return *m, nil, true
		}
	}
	// Everything below belongs to the chat view alone.
	if m.focus != focusChat {
		return *m, nil, false
	}
	// The message-filter shortcuts are 1-4, and are looked up rather than
	// listed here so the filters stay defined in one place.
	if filter, ok := messageFilterForShortcutRune(r); ok {
		return *m, m.toggleActiveMessageFilter(filter), true
	}
	// i/o/a all enter the composer, matching vim's insert keys; the composer
	// has no cursor to position, so they differ only in muscle memory.
	if isInsertRune(r) {
		m.focus = focusComposer
		return *m, nil, true
	}
	switch r {
	case ']':
		if m.channels.switchBy(1) {
			m.clampScroll()
			model, cmd := m.withAsyncAssetCommands(nil)
			return model, cmd, true
		}
		return *m, nil, true
	case '[':
		if m.channels.switchBy(-1) {
			m.clampScroll()
			model, cmd := m.withAsyncAssetCommands(nil)
			return model, cmd, true
		}
		return *m, nil, true
	case '0':
		return *m, m.resetActiveMessageFilters(), true
	case 'q':
		return *m, tea.Quit, true
	case 'r':
		m.startReplyMode()
		return *m, nil, true
	case 'j':
		m.selectReplyMessage(1)
		return *m, nil, true
	case 'k':
		m.selectReplyMessage(-1)
		return *m, nil, true
	case 'K':
		// K is vim's "show me more about this", which is exactly what the
		// inspect panel does. It replaces the old bare i, now an insert key.
		m.toggleInspect()
		return *m, nil, true
	}
	return *m, nil, false
}

// handleAlwaysOnKey consumes the keys that must work no matter what is on
// screen: quitting, skipping the splash, switching tabs, and the three panel
// toggles. handled is false when the key was none of those, and the caller
// carries on with it.
//
// Its first few lines still run for every keypress, because disarming a
// pending ctrl+L confirmation is something any other key does.
func (m *shellModel) handleAlwaysOnKey(msg tea.KeyMsg) (shellModel, tea.Cmd, bool) {
	// Any key other than a second ctrl+L abandons a pending clear, so a
	// stray press cannot arm the confirmation and sit waiting for an
	// unrelated keystroke to trigger it later.
	if m.pendingClearChat && msg.Type != tea.KeyCtrlL {
		m.pendingClearChat = false
	}
	switch {
	case msg.Type == tea.KeyCtrlC:
		return *m, tea.Quit, true
	case m.splashActive():
		m.frames.splashSkipped = true
		return *m, nil, true
	}
	if msg.Type == tea.KeyRunes && msg.Alt && len(msg.Runes) == 1 {
		if tab, ok := tabForShortcutRune(msg.Runes[0]); ok {
			model, cmd := m.switchToTab(tab)
			return model, cmd, true
		}
	}
	switch msg.Type {
	case tea.KeyCtrlP:
		m.toggleCommandPalette()
		return *m, nil, true
	case tea.KeyCtrlE:
		m.toggleEmotePicker()
		return *m, nil, true
	case tea.KeyCtrlT:
		m.toggleThemeSettings()
		return *m, nil, true
	}
	if m.handleDisplayToggleKey(msg) {
		return *m, nil, true
	}
	return *m, nil, false
}

// routeKeyToOpenPanel hands the key to whichever overlay or full-screen tab
// currently owns the keyboard. At most one of them is ever active, and the
// case order decides which wins if that were somehow not true.
func (m shellModel) routeKeyToOpenPanel(msg tea.KeyMsg) (shellModel, tea.Cmd, bool) {
	var (
		model shellModel
		cmd   tea.Cmd
	)
	switch {
	case m.palette.open:
		model, cmd = m.handleCommandPaletteKey(msg)
	case m.emotePicker.open:
		model, cmd = m.handleEmotePickerKey(msg)
	case m.themeSettings.open:
		model, cmd = m.handleThemeSettingsKey(msg)
	case m.channelPicker.open:
		model, cmd = m.handleChannelPickerKey(msg)
	case m.categoryPicker.open:
		model, cmd = m.handleCategoryPickerKey(msg)
	case m.activeTab == tabStreamInfo:
		model, cmd = m.handleStreamInfoKey(msg)
	case m.activeTab == tabMisc:
		model, cmd = m.handleMiscKey(msg)
	default:
		return m, nil, false
	}
	return model, cmd, true
}

// handleMentionKey lets the @mention strip claim tab, the arrows and esc, but
// only while it is actually offering completions, so those keys keep their
// normal meaning the rest of the time.
func (m *shellModel) handleMentionKey(msg tea.KeyMsg) (shellModel, tea.Cmd, bool) {
	if len(m.mentionSuggestions()) == 0 {
		return *m, nil, false
	}
	switch msg.Type {
	case tea.KeyTab:
		if m.acceptMentionSuggestion() {
			return *m, nil, true
		}
	case tea.KeyDown:
		m.moveMentionSelection(1)
		return *m, nil, true
	case tea.KeyUp:
		m.moveMentionSelection(-1)
		return *m, nil, true
	case tea.KeyEsc:
		if m.dismissMentionSuggestions() {
			return *m, nil, true
		}
	}
	return *m, nil, false
}

func (m shellModel) handleKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	if model, cmd, handled := m.handleAlwaysOnKey(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.routeKeyToOpenPanel(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.handleMentionKey(msg); handled {
		return model, cmd
	}
	// The space leader chord is consumed before every other binding so a
	// pending leader can never be mistaken for a normal-mode key. It is
	// only ever armed outside the composer, where space is literal text.
	if m.leaderPending {
		return m.handleLeaderKey(msg)
	}
	if msg.Type == tea.KeySpace && m.focus != focusComposer {
		m.leaderPending = true
		return m, nil
	}
	if m.focus == focusSidebar {
		if model, cmd, handled := m.handleSidebarKey(msg); handled {
			return model, cmd
		}
	}

	switch msg.Type {
	case tea.KeyTab:
		m.cycleFocus()
	case tea.KeyPgUp:
		m.scrollBy(m.layout().chatContentHeight)
	case tea.KeyPgDown:
		m.scrollBy(-m.layout().chatContentHeight)
	case tea.KeyCtrlL:
		// Clearing discards the whole retained backlog for the channel
		// and cannot be undone. It is one keystroke, next to keys used
		// constantly, on a tool that is often running during a live
		// broadcast -- so it asks once. The leader chord for closing a
		// channel and the sidebar's explicit x are already deliberate
		// enough not to need this.
		if m.pendingClearChat {
			m.pendingClearChat = false
			m.clearLocalChat()
			break
		}
		m.pendingClearChat = true
	case tea.KeyCtrlR:
		return m, m.requestReconnect()
	case tea.KeyBackspace:
		if m.focus == focusComposer {
			m.deleteComposerRune()
			m.resetMentionSelection()
		}
	case tea.KeyEsc:
		// esc is "leave insert mode" first: from the composer it always
		// returns to the chat view, keeping the draft intact.
		if m.focus == focusComposer {
			m.focus = focusChat
			return m, nil
		}
		if m.inspectOpen {
			m.inspectOpen = false
			m.clampScroll()
			return m, nil
		}
		m.activeChannelState().replyTo = nil
	case tea.KeyUp:
		if m.focus == focusChat {
			m.selectReplyMessage(-1)
		}
	case tea.KeyDown:
		if m.focus == focusChat {
			m.selectReplyMessage(1)
		}
	case tea.KeyCtrlU:
		if m.focus == focusComposer {
			m.activeChannelState().composerText = ""
		}
	case tea.KeyEnter:
		if m.focus == focusComposer {
			return m.queueComposerSend()
		}
	case tea.KeySpace:
		if m.focus == focusComposer {
			m.activeChannelState().composerText += " "
		}
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			if model, cmd, handled := m.handleRuneShortcut(msg.Runes[0]); handled {
				return model, cmd
			}
		}
		if m.focus == focusComposer {
			m.insertComposerText(string(msg.Runes))
			m.resetMentionSelection()
		}
	}
	return m, nil
}

func (m shellModel) View() string {
	backgroundOverride := m.themeBackgroundSequence()
	if m.splashActive() {
		return backgroundOverride + m.splashView()
	}
	// The theme picker is a full-screen page, not a docked strip: it replaces
	// the dashboard the same way the splash does.
	if m.themeSettings.open {
		return backgroundOverride + m.themeSettingsPageView()
	}
	layout := m.layout()

	regions := make([]string, 0, 4)
	if layout.tabBarHeight > 0 {
		regions = append(regions, m.tabBarLine(layout.width))
	}
	if layout.statusHeight > 0 {
		regions = append(regions, m.statusLine(layout.width))
	}
	if layout.chatHeight > 0 {
		chat := m.chatView(layout)
		if layout.sidebarWidth > 0 {
			chat = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(layout), chat)
		}
		if layout.activityWidth > 0 {
			chat = lipgloss.JoinHorizontal(lipgloss.Top, chat, m.activityLogView(layout))
		}
		regions = append(regions, chat)
	}
	if layout.streamInfoHeight > 0 {
		regions = append(regions, m.streamInfoView(layout))
	}
	if layout.miscHeight > 0 {
		regions = append(regions, m.miscView(layout))
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
	if layout.channelPickerHeight > 0 {
		regions = append(regions, m.channelPickerView(layout))
	}
	if layout.categoryPickerHeight > 0 {
		regions = append(regions, m.categoryPickerView(layout))
	}
	if layout.composerHeight > 0 {
		regions = append(regions, m.composerView(layout))
	}
	if layout.helpHeight > 0 {
		regions = append(regions, m.helpView(layout.width, layout.helpHeight))
	}

	joined := lipgloss.JoinVertical(lipgloss.Left, regions...)
	rendered := lipgloss.NewStyle().
		Width(layout.width).
		Height(clampMin(m.height, 1)).
		Background(lipgloss.Color(m.canvasBackground())).
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Render(joined)
	return backgroundOverride + rendered
}

// droppedMessageCount reports messages lost to a full buffer, or zero for a
// source that cannot drop (mock mode, test fakes).
func (m shellModel) droppedMessageCount() uint64 {
	counter, ok := m.services.client.(MessageDropCounter)
	if !ok {
		return 0
	}
	return counter.DroppedMessages()
}

func (m shellModel) statusLine(width int) string {
	active := m.activeChannelState()
	channelCount := len(m.channels.channelNames())
	left := fmt.Sprintf("#%s %s", active.name, active.status.Status)
	if m.channels.empty() {
		left = "no channel open"
	}
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
	// Dropped messages are shown unconditionally once any exist, at any width.
	// Chat quietly losing messages is exactly the thing a moderator must not
	// have to discover for themselves, so this outranks the decorations above
	// it in the same line.
	if dropped := m.droppedMessageCount(); dropped > 0 {
		left += fmt.Sprintf(" | dropped=%d", dropped)
	}
	// The prompt is the whole point of the guard: an armed confirmation the
	// user cannot see is worse than no guard at all.
	if m.pendingClearChat {
		left += " | clear chat? ctrl+L again to confirm"
	}
	if m.lastSystemNotification != nil && width >= 58 {
		left += " | notify: " + systemNotificationSummary(*m.lastSystemNotification)
	}
	if summary := active.messageFilters.summary(); summary != "" && width >= 46 {
		left += " | filter=" + summary
	}
	right := ""
	if width >= 64 {
		right = fmt.Sprintf(" focus=%s animation=%s", m.focusName(), m.animationMode)
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
	statusForeground := theme.ContrastCorrectedForeground(m.theme.Foreground, statusBackground, m.canvasBackground())
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color(statusForeground)).
		Background(lipgloss.Color(statusBackground)).
		Bold(true).
		Render(line)
}

// visibleChatRows returns exactly the styled rows the chat pane will draw,
// styling only the window rather than the whole backlog. It is equivalent to
// visibleRows(chatRows(layout), height, scrollOffset) but does not pay to
// style rows that are scrolled off screen.
func (m shellModel) visibleChatRows(layout shellLayout) []string {
	height := layout.chatContentHeight
	if height <= 0 {
		return nil
	}
	active := m.activeChannelState()
	rowWidth := m.chatRowWidth(layout)
	if m.channels.empty() {
		return visibleRows(m.noChannelRows(rowWidth), height, active.scrollOffset)
	}

	blocks := m.visibleChatRowBlocks(layout)
	total := chatRowBlockCount(blocks)
	if total == 0 && active.messageFilters.active() {
		return []string{backgroundStyledLine(m.emptyFilterRow(rowWidth), m.theme.Background)}
	}
	if total <= height {
		return m.styleChatRowWindow(blocks, rowWidth, 0, -1)
	}

	// Mirrors visibleRows: scrollOffset counts rows hidden below the
	// viewport, so the window ends that far from the bottom.
	scrollOffset := min(clampMin(active.scrollOffset, 0), total-height)
	start := total - scrollOffset - height
	return m.styleChatRowWindow(blocks, rowWidth, clampMin(start, 0), height)
}

func (m shellModel) chatView(layout shellLayout) string {
	rows := m.visibleChatRows(layout)

	if len(rows) < layout.chatContentHeight {
		for len(rows) < layout.chatContentHeight {
			rows = append(rows, "")
		}
	}

	content := strings.Join(rows, "\n")
	if !layout.chatFramed {
		return fitBlock(content, layout.chatWidth, layout.chatHeight)
	}

	title := "Chat · #" + m.activeChannelName() + " · " + m.activeChatterLabel()
	if m.channels.empty() {
		title = "Chat"
	}
	return m.renderPane(paneSpec{
		icon:          "💬",
		title:         title,
		content:       content,
		width:         layout.chatWidth,
		contentHeight: layout.chatContentHeight,
		padding:       1,
		accent:        m.theme.Accent,
	})
}

// noChannelRows renders the empty state shown when nothing is open, which is
// where `twi chat` lands without --channel/--channels. It names the two ways
// out rather than leaving a blank pane.
//
// The headline and the scanner run on the shared frame clock so an idle pane
// still reads as a live app. Both degrade to their static frame under
// animation=off, leaving the wording and layout untouched.
func (m shellModel) noChannelRows(width int) []string {
	width = clampMin(width, 1)
	elapsed := m.frameElapsed()
	rows := []string{
		m.emptyStateLine("", width),
		m.emptyStateHeadline(noChannelHeadline, width, elapsed),
		m.emptyStateLine("", width),
		m.emptyStateLine("/channels or "+channelPickerKeyHint+" to open one", width),
		m.emptyStateLine("ctrl+p for the command palette", width),
	}
	if scanner := m.emptyStateScanner(width, elapsed); scanner != "" {
		rows = append(rows, m.emptyStateLine("", width), scanner)
	}
	return rows
}

func (m shellModel) emptyStateLine(text string, width int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.Muted)).
		Background(lipgloss.Color(m.theme.Surface)).
		Render(fitLine(emptyStateIndent+text, width))
}

// emptyStateHeadline drifts an accent gradient through the headline. Muted is
// the near end so the resting color stays the empty state's own muted tone
// instead of turning the pane into an accent banner.
func (m shellModel) emptyStateHeadline(text string, width int, elapsed time.Duration) string {
	cfg := m.textEffectConfig(animation.EffectGradientWave)
	cfg.Base = m.theme.Muted
	cfg.Accent = m.theme.Accent
	cfg.Bold = true
	frame := animatedText(revealDisplayCells(emptyStateIndent+text, width), cfg, elapsed, m.theme.Surface)
	return paddedEffectLine(frame, width, m.theme.Surface)
}

// emptyStateScanner bounces a marker across the pane as an idle indicator:
// nothing is arriving, but the app is still ticking. The track is narrow so
// the motion stays in the corner of the eye rather than crossing the pane.
//
// The row is dropped entirely rather than frozen when animation is off: a
// stationary marker carries no meaning, unlike the headline, whose wording
// still has to be there.
func (m shellModel) emptyStateScanner(width int, elapsed time.Duration) string {
	indentWidth := uniseg.StringWidth(emptyStateIndent)
	track := min(width-indentWidth-2, emptyStateScannerWidth)
	if track < 4 || m.animationMode == string(animation.ModeOff) {
		return ""
	}
	cfg := m.textEffectConfig(animation.EffectBounce)
	cfg.Base = m.theme.Accent
	cfg.Trail = m.theme.Muted
	cfg.Width = track
	indent := backgroundSpaces(indentWidth, m.theme.Surface)
	frame := indent + animatedText(emptyStateScannerGlyph, cfg, elapsed, m.theme.Surface)
	return paddedEffectLine(frame, width, m.theme.Surface)
}

// activeChatterLabel renders the live count of chatters twi considers active
// in the current channel. It lives in the chat pane title so it is visible on
// every frame rather than being dropped by the status bar's width budget.
//
// The count is best-effort by construction (see chatterRoster): with
// membership it is JOIN/PART presence, without it, recent speakers. The "~"
// marks the second case so a small number does not read as an authoritative
// viewer count.
func (m shellModel) activeChatterLabel() string {
	state := m.activeChannelState()
	if state == nil {
		return "👥 0"
	}
	count := state.roster.activeCount(m.metricsNow())
	if state.roster != nil && state.roster.membershipSeen {
		return fmt.Sprintf("👥 %d", count)
	}
	return fmt.Sprintf("👥 ~%d", count)
}

func (m shellModel) sidebarView(layout shellLayout) string {
	if layout.sidebarWidth <= 0 {
		return ""
	}
	contentWidth := clampMin(layout.sidebarWidth-2, 1)
	focused := m.focus == focusSidebar && !m.anyOverlayOpen()
	selected := m.panes.sidebarSelected
	lines := make([]string, 0, layout.sidebarContentHeight)
	for index, key := range m.channels.order {
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
		// The close affordance is only drawn on the highlighted row while
		// the sidebar has focus, so it reads as "x closes this" rather than
		// as decoration on every channel.
		if focused && index == selected {
			line = fitLine(line, clampMin(contentWidth-2, 1))
			line += sidebarCloseAffordance
		}
		lines = append(lines, fitLine(line, contentWidth))
	}
	if len(m.channels.order) == 0 {
		lines = append(lines, fitLine(" (none open)", contentWidth))
	}
	for len(lines) < layout.sidebarContentHeight {
		lines = append(lines, fitLine("", contentWidth))
	}
	if len(lines) > layout.sidebarContentHeight {
		lines = lines[:layout.sidebarContentHeight]
	}

	return m.renderPane(paneSpec{
		icon:          "📡",
		title:         fmt.Sprintf("Channels · %02d", len(m.channels.channelNames())),
		content:       strings.Join(lines, "\n"),
		width:         layout.sidebarWidth,
		contentHeight: layout.sidebarContentHeight,
		accent:        m.theme.Success,
		focused:       focused,
	})
}

// activityLogView renders the right-hand activity log column: raids,
// subs/resubs/gift subs (from IRC, via recordActivityFromMessage), and new
// followers (from polling and diffing Get Channel Followers, via
// applyNewFollowerActivity) - most recent entries at the bottom, matching
// chat's own bottom-anchored scroll convention.
func (m shellModel) activityLogView(layout shellLayout) string {
	if layout.activityWidth <= 0 {
		return ""
	}
	contentWidth := clampMin(layout.activityWidth-2, 1)
	lines := make([]string, 0, layout.activityContentHeight)

	maxRows := clampMin(layout.activityContentHeight, 0)
	entries := m.activity.activityLog
	if len(entries) > maxRows {
		entries = entries[len(entries)-maxRows:]
	}
	for _, entry := range entries {
		lines = append(lines, m.activityLogLine(entry, contentWidth))
	}
	for len(lines) < layout.activityContentHeight {
		lines = append(lines, fitLine("", contentWidth))
	}
	if len(lines) > layout.activityContentHeight {
		lines = lines[:layout.activityContentHeight]
	}

	return m.renderPane(paneSpec{
		icon:          "⚡",
		title:         fmt.Sprintf("Activity · %02d", len(m.activity.activityLog)),
		content:       strings.Join(lines, "\n"),
		width:         layout.activityWidth,
		contentHeight: layout.activityContentHeight,
		accent:        m.theme.Warning,
	})
}

// activityLogLine renders one activity row as "HH:MM ◆ text", where the
// glyph and its color identify the event kind. The pane is narrow, so the
// timestamp is dropped before the glyph and the glyph before the text as
// width shrinks - the text is the part that always survives.
func (m shellModel) activityLogLine(entry activityEntry, width int) string {
	if width <= 0 {
		return ""
	}
	glyph, color := activityKindGlyph(entry.Kind, m.theme)
	text := entry.Text
	// The channel prefix only disambiguates when more than one channel is
	// joined; with a single channel it just eats width in a narrow pane.
	if entry.Channel != "" && len(m.channels.channelNames()) > 1 {
		text = "#" + entry.Channel + " " + text
	}

	var builder strings.Builder
	used := 0
	write := func(value, foreground string, bold bool) {
		value = fitLine(value, min(uniseg.StringWidth(value), clampMin(width-used, 0)))
		if value == "" {
			return
		}
		builder.WriteString(paneStyledText(value, foreground, m.theme.Surface, bold))
		used += uniseg.StringWidth(value)
	}

	if width >= 18 && !entry.At.IsZero() {
		write(" "+entry.At.Local().Format("15:04"), m.theme.Muted, false)
	}
	if width >= 12 {
		write(" "+glyph, color, true)
	}
	write(" "+text, m.theme.Foreground, false)
	if used < width {
		builder.WriteString(paneStyledText(strings.Repeat(" ", width-used), m.theme.Muted, m.theme.Surface, false))
	}
	return builder.String()
}

func activityKindGlyph(kind activityKind, palette theme.Palette) (string, string) {
	switch kind {
	case activityFollow:
		return "♥", palette.Success
	case activityIRCEvent:
		return "★", palette.Accent
	case activityClip:
		return "✂", palette.Warning
	case activityCheer:
		return "◈", palette.Warning
	case activityStreamStatus:
		return "●", palette.Success
	case activityMembership:
		return "⇄", palette.Muted
	default:
		return "·", palette.Muted
	}
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

// styleChatRowWindow converts blocks into styled terminal rows, producing
// only the rows in [start, start+count). A negative count means "to the end".
//
// Styling a row is not free, and only a screenful is ever displayed, so the
// viewport path asks for just the window it will draw rather than styling the
// whole backlog and slicing afterwards.
func (m shellModel) styleChatRowWindow(blocks []chatRowBlock, rowWidth, start, count int) []string {
	if start < 0 {
		start = 0
	}
	capacity := count
	if capacity < 0 {
		capacity = chatRowBlockCount(blocks)
	}
	rows := make([]string, 0, clampMin(capacity, 0))
	index := 0
	// want reports whether the row at the current global index falls inside
	// the requested window, and stops the walk once it is past the end.
	want := func() (keep, done bool) {
		if index < start {
			return false, false
		}
		if count >= 0 && len(rows) >= count {
			return false, true
		}
		return true, false
	}
	for _, block := range blocks {
		if block.separatorBefore {
			keep, done := want()
			if done {
				return rows
			}
			if keep {
				rows = append(rows, m.messageGroupSeparatorString(rowWidth))
			}
			index++
		}
		if len(block.rows) == 0 {
			keep, done := want()
			if done {
				return rows
			}
			if keep {
				rows = append(rows, m.messageRowString(block, block.groupIndex, 0, render.Row{}, rowWidth))
			}
			index++
			continue
		}
		for rowIndex, row := range block.rows {
			keep, done := want()
			if done {
				return rows
			}
			if keep {
				rows = append(rows, m.messageRowString(block, block.groupIndex, rowIndex, row, rowWidth))
			}
			index++
		}
	}
	return rows
}

// chatRowCount reports how many rows chatRows would produce, without paying
// for the string styling of every row. Scroll clamping needs only the count,
// and it runs from several Update paths per arriving message.
func (m shellModel) chatRowCount(layout shellLayout) int {
	if m.channels.empty() {
		return len(m.noChannelRows(m.chatRowWidth(layout)))
	}
	blocks := m.visibleChatRowBlocks(layout)
	count := chatRowBlockCount(blocks)
	if count == 0 && m.activeChannelState().messageFilters.active() {
		return 1
	}
	return count
}

func (m shellModel) chatRows(layout shellLayout) []string {
	active := m.activeChannelState()
	rowWidth := m.chatRowWidth(layout)
	if m.channels.empty() {
		return m.noChannelRows(rowWidth)
	}
	blocks := m.visibleChatRowBlocks(layout)

	rows := m.styleChatRowWindow(blocks, rowWidth, 0, -1)
	if len(rows) == 0 && active.messageFilters.active() {
		rows = append(rows, backgroundStyledLine(m.emptyFilterRow(rowWidth), m.theme.Background))
	}
	return rows
}

func (m shellModel) visibleChatRowBlocks(layout shellLayout) []chatRowBlock {
	active := m.activeChannelState()
	rowWidth := m.chatMessageContentWidth(layout)

	blocks := make([]chatRowBlock, 0, len(active.messages)+len(active.activeOrder))
	for _, message := range active.messages {
		if !m.messageVisibleForState(active, message) {
			continue
		}
		blocks = append(blocks, chatRowBlock{message: message})
	}
	frames := active.revealQueue.Frames()
	for _, id := range active.activeOrder {
		message, ok := active.activeMessages[id]
		if !ok || !m.messageVisibleForState(active, message) {
			continue
		}
		blocks = append(blocks, chatRowBlock{
			message:   message,
			rows:      frames[id],
			animating: true,
			revealed:  true,
		})
	}
	assignChatAuthorGroups(blocks)
	// Rendering happens after grouping because LayoutGrouped needs to know
	// whether a message continues the previous author's block. Reveal frames
	// are already-rendered partial rows, so they are left as-is.
	params := m.chatRenderParams(rowWidth)
	for index := range blocks {
		if blocks[index].revealed {
			continue
		}
		message := blocks[index].message
		continuesGroup := blocks[index].continuesGroup
		meta := m.authorMeta(message)
		blocks[index].rows = m.rowCache.rows(message, continuesGroup, meta, params, func() []render.Row {
			opts := m.renderOptions(rowWidth)
			opts.ContinuesGroup = continuesGroup
			opts.Meta = meta
			return render.Rows(message, opts)
		})
	}
	return blocks
}

// chatRenderParams mirrors the non-message inputs of renderOptions so the row
// cache can detect a change that invalidates every cached message.
func (m shellModel) chatRenderParams(width int) chatRenderParams {
	opts := m.renderOptions(width)
	return chatRenderParams{
		width:        opts.Width,
		layout:       opts.Layout,
		badges:       opts.Badges,
		highlight:    opts.HighlightEmotes,
		fullUsername: opts.FullUsername,
		showAvatars:  opts.Assets.ShowAvatars,
		palette:      opts.Palette,
	}
}

// assignChatAuthorGroups joins adjacent visible messages from the same author
// into one visual group. Filters are applied before this pass, so grouping
// reflects exactly what the user can see rather than hidden history.
func assignChatAuthorGroups(blocks []chatRowBlock) {
	previousKey := ""
	groupIndex := -1
	for index := range blocks {
		key := chatAuthorGroupKey(blocks[index].message, index)
		if index == 0 || key != previousKey {
			groupIndex++
			blocks[index].separatorBefore = index > 0
		} else {
			blocks[index].continuesGroup = true
		}
		blocks[index].groupIndex = groupIndex
		previousKey = key
	}
}

func chatAuthorGroupKey(message twitch.ChatMessage, blockIndex int) string {
	for _, identity := range []string{message.AuthorLogin, message.AuthorID, message.DisplayName} {
		if identity = strings.TrimSpace(identity); identity != "" {
			return "author:" + strings.ToLower(identity)
		}
	}
	if messageID := strings.TrimSpace(message.ID); messageID != "" {
		return "message:" + strings.ToLower(messageID)
	}
	eventID := strings.ToLower(strings.TrimSpace(message.SystemEventID))
	return fmt.Sprintf("anonymous:%s:%d", eventID, blockIndex)
}

func (m shellModel) messageVisibleForState(state *channelState, message twitch.ChatMessage) bool {
	if state == nil {
		return true
	}
	return state.messageFilters.matches(message, m.mentionLogin)
}

func chatRowBlockCount(blocks []chatRowBlock) int {
	total := 0
	for _, block := range blocks {
		if block.separatorBefore {
			total++
		}
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

func (m shellModel) emptyFilterRow(width int) string {
	active := m.activeChannelState()
	summary := active.messageFilters.summary()
	hidden := len(active.messages) + len(active.activeOrder)
	detail := "no messages yet"
	if hidden > 0 {
		detail = fmt.Sprintf("no matching messages (%d hidden)", hidden)
	}
	return fitLine(" filter: "+summary+" - "+detail, width)
}

func (m shellModel) helpView(width, height int) string {
	lines := m.helpLines(width, height)
	if len(lines) > 0 && width >= 6 {
		lines[0] = "⌨ " + strings.TrimLeft(lines[0], " ")
	}
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

func (m shellModel) activeChannelState() *channelState {
	if m.channels == nil {
		channels := newChannelStateSet([]string{"chat"}, animationConfigFor(m.animationMode), nil, config.DefaultScrollbackLimit)
		return channels.activeState()
	}
	return m.channels.activeState()
}

func (m shellModel) activeChannelName() string {
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

func (m shellModel) messageRowString(block chatRowBlock, blockIndex, rowIndex int, row render.Row, rowWidth int) string {
	gutterWidth := messageGutterWidth(rowWidth)
	background := m.messageGroupBackground(block, blockIndex)
	contentWidth := clampMin(rowWidth-gutterWidth, 1)
	content := terminalRowString(row, contentWidth, background)
	if gutterWidth == 0 {
		return content
	}

	railColor := m.messageRailColor(block, blockIndex)
	rail := lipgloss.NewStyle().
		Foreground(lipgloss.Color(railColor)).
		Background(lipgloss.Color(background)).
		Bold(block.animating).
		Render("│ ")
	if gutterWidth == 2 {
		return rail + content
	}
	icon := "  "
	if rowIndex == 0 {
		icon = "✉ "
	}
	iconColor := railColor
	if !block.animating {
		iconColor = m.theme.Muted
	}
	return rail + lipgloss.NewStyle().
		Foreground(lipgloss.Color(iconColor)).
		Background(lipgloss.Color(background)).
		Bold(block.animating).
		Render(icon) + content
}

// messageGroupTint is how far a message group's surface is pulled toward its
// author's identity color. It is small on purpose: the tint has to stay a
// background that message text remains readable on, while still making a
// user's block recognizable at a glance.
const messageGroupTint = 0.10

// messageRailTint is stronger than the surface tint because the rail is a
// solid two-cell glyph rather than a text background, so it can carry much
// more of the author's color without hurting legibility.
const messageRailTint = 0.65

// messageGroupBackground returns the surface a message group is drawn on:
// the alternating base/surface stripe, tinted toward the author's stable
// identity color so consecutive messages from one person read as one block.
func (m shellModel) messageGroupBackground(block chatRowBlock, blockIndex int) string {
	background := m.theme.Background
	if blockIndex%2 == 1 {
		background = m.theme.Surface
	}
	color := m.messageAuthorColor(block.message)
	if color == "" {
		return background
	}
	return theme.Mix(background, color, messageGroupTint)
}

// messageRailColor returns the left rail color for a message group. The rail
// carries the author's identity color so the same person is recognizable
// down the gutter; animating messages keep the shared-clock gradient shimmer
// that marks a message as still arriving.
func (m shellModel) messageRailColor(block chatRowBlock, blockIndex int) string {
	if block.animating {
		railColors := theme.SeamlessGradient(m.theme.Accent, m.gradientEndColor(), 8)
		index := (blockIndex + m.gradientPhase(len(railColors))) % len(railColors)
		return railColors[index]
	}
	color := m.messageAuthorColor(block.message)
	if color == "" {
		railColors := theme.Gradient(m.theme.Accent, m.gradientEndColor(), 8)
		return railColors[blockIndex%len(railColors)]
	}
	return theme.Mix(m.theme.Border, color, messageRailTint)
}

// messageAuthorColor returns a message author's stable identity color, or ""
// for rows with no real author (notices, system placeholders) so those keep
// the neutral accent treatment instead of being tinted by a fake identity.
func (m shellModel) messageAuthorColor(message twitch.ChatMessage) string {
	switch message.Type {
	case twitch.MessageTypeNotice, twitch.MessageTypeSystem:
		return ""
	}
	identity := strings.TrimSpace(message.AuthorLogin)
	if identity == "" {
		identity = strings.TrimSpace(message.DisplayName)
	}
	if identity == "" {
		return ""
	}
	return theme.IdentityColor(identity, []string{m.theme.Background, m.theme.Surface}, m.theme.Foreground)
}

func (m shellModel) messageGroupSeparatorString(rowWidth int) string {
	if rowWidth <= 0 {
		return ""
	}
	line := strings.Repeat("─", rowWidth)
	if rowWidth >= 5 {
		line = "  " + strings.Repeat("─", rowWidth-4) + "  "
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.Border)).
		Background(lipgloss.Color(m.theme.Surface)).
		Render(line)
}

func (m *shellModel) cycleFocus() {
	switch m.focus {
	case focusChat:
		m.focus = focusComposer
	case focusComposer:
		// The sidebar only joins the cycle while it is on screen, so tab
		// never stops on something invisible.
		if m.layout().sidebarWidth > 0 {
			m.syncSidebarSelectionToActive()
			m.focus = focusSidebar
			return
		}
		m.focus = focusChat
	default:
		m.focus = focusChat
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

func (m *shellModel) toggleActiveMessageFilter(filter messageFilter) tea.Cmd {
	m.activeChannelState().messageFilters.toggle(filter)
	m.clampScroll()
	return m.asyncAssetCommand()
}

func (m *shellModel) resetActiveMessageFilters() tea.Cmd {
	state := m.activeChannelState()
	if !state.messageFilters.active() {
		return nil
	}
	state.messageFilters.reset()
	m.clampScroll()
	return m.asyncAssetCommand()
}

func (m *shellModel) scrollBy(delta int) {
	if delta == 0 {
		delta = 1
	}
	m.activeChannelState().scrollOffset += delta
	m.clampScroll()
}

func (m *shellModel) clampScroll() {
	active := m.activeChannelState()
	maxScroll := m.maxScrollOffset()
	if active.scrollOffset > maxScroll {
		active.scrollOffset = maxScroll
	}
	if active.scrollOffset < 0 {
		active.scrollOffset = 0
	}
}

func (m shellModel) maxScrollOffset() int {
	layout := m.layout()
	visible := layout.chatContentHeight
	total := m.chatRowCount(layout)
	if visible <= 0 || total <= visible {
		return 0
	}
	return total - visible
}

func (m shellModel) nextIncomingCommand() tea.Cmd {
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

// receiveCommand turns one of the channels the chat transport publishes on
// into a Bubble Tea command that reads a single value from it.
//
// Bubble Tea has no notion of a lasting subscription: a command runs once,
// returns one message, and the handler for that message asks for the next
// one. wrap builds that message out of the value received and out of ok,
// which is false once the channel has been closed and drained - that is how a
// handler learns the stream has ended. A nil channel produces no command,
// because there is nothing there to listen to.
func receiveCommand[T any](ch <-chan T, wrap func(value T, ok bool) tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		value, ok := <-ch
		return wrap(value, ok)
	}
}

// nextClientMessageCommand waits for the next chat message from the
// transport.
func (m shellModel) nextClientMessageCommand() tea.Cmd {
	if m.services.client == nil {
		return nil
	}
	return receiveCommand(m.services.client.Messages(), func(message twitch.ChatMessage, ok bool) tea.Msg {
		return chatClientMessageMsg{message: message, ok: ok}
	})
}

// nextConnectionStateCommand waits for the transport's next report of whether
// it is connected.
func (m shellModel) nextConnectionStateCommand() tea.Cmd {
	if m.services.client == nil {
		return nil
	}
	return receiveCommand(m.services.client.ConnectionStates(), func(state ConnectionState, ok bool) tea.Msg {
		return chatClientConnectionStateMsg{state: state, ok: ok}
	})
}

// nextClientMembershipCommand subscribes to JOIN/PART when the transport
// supports it. Transports that do not implement MembershipSource (mock mode,
// test fakes) simply never produce membership messages.
func (m shellModel) nextClientMembershipCommand() tea.Cmd {
	if m.services.client == nil {
		return nil
	}
	source, ok := m.services.client.(MembershipSource)
	if !ok {
		return nil
	}
	return receiveCommand(source.Memberships(), func(membership twitch.MembershipEvent, ok bool) tea.Msg {
		return chatClientMembershipMsg{membership: membership, ok: ok}
	})
}

// nextClientUserStateCommand subscribes to USERSTATE when the transport
// supports it. Transports that do not implement UserStateSource (mock mode,
// test fakes) simply never produce these messages.
func (m shellModel) nextClientUserStateCommand() tea.Cmd {
	if m.services.client == nil {
		return nil
	}
	source, ok := m.services.client.(UserStateSource)
	if !ok {
		return nil
	}
	return receiveCommand(source.UserStates(), func(state twitch.UserState, ok bool) tea.Msg {
		return chatClientUserStateMsg{state: state, ok: ok}
	})
}

// nextClientModerationCommand subscribes to moderation actions when the
// transport supports it. Transports that do not implement ModerationSource
// (mock mode, test fakes) simply never produce these messages.
func (m shellModel) nextClientModerationCommand() tea.Cmd {
	if m.services.client == nil {
		return nil
	}
	source, ok := m.services.client.(ModerationSource)
	if !ok {
		return nil
	}
	return receiveCommand(source.Moderations(), func(event twitch.ModerationEvent, ok bool) tea.Msg {
		return chatClientModerationMsg{event: event, ok: ok}
	})
}

// applyModeration redacts messages a moderator removed, rather than echoing
// the removal as a new chat line.
//
// Twitch's CLEARMSG carries the deleted message's text, and reprinting it
// would put the removed words back on screen - the opposite of what the
// moderator asked for, on a terminal that is often being streamed. The text
// stays in the debug log for after-the-fact review and nowhere else.
func (m *shellModel) applyModeration(event twitch.ModerationEvent) {
	channel := event.Channel
	if strings.TrimSpace(channel) == "" {
		channel = m.activeChannelName()
	}
	state := m.channels.ensure(channel)
	if state == nil {
		return
	}

	switch event.Type {
	case twitch.ModerationChatCleared:
		state.messages = nil
		state.activeOrder = nil
		state.activeMessages = make(map[string]twitch.ChatMessage)
		state.scrollOffset = 0
	case twitch.ModerationMessageDeleted:
		state.markMessageDeleted(func(msg twitch.ChatMessage) bool {
			return event.TargetMessageID != "" && msg.ID == event.TargetMessageID
		})
	case twitch.ModerationUserBanned, twitch.ModerationUserTimedOut:
		// A ban or timeout retroactively removes everything that user said,
		// which is what Twitch's own clients show and what a moderator means
		// by the action.
		target := strings.ToLower(strings.TrimSpace(event.TargetLogin))
		if target == "" {
			return
		}
		state.markMessageDeleted(func(msg twitch.ChatMessage) bool {
			return strings.ToLower(strings.TrimSpace(msg.AuthorLogin)) == target
		})
	}
}

// markMessageDeleted flags every retained message matching pred, in both the
// settled backlog and any message still animating in.
// applyUserState records the authenticated user's own badges and identity for
// the target channel. Twitch re-sends USERSTATE after every message that user
// sends, so this stays current as roles change mid-session.
func (m *shellModel) applyUserState(state twitch.UserState) {
	channel := state.Channel
	if strings.TrimSpace(channel) == "" {
		channel = m.activeChannelName()
	}
	target := m.channels.ensure(channel)
	if target == nil {
		return
	}
	target.selfBadges = state.Badges
	target.selfDisplayName = state.DisplayName
	target.selfColor = state.AuthorColor

	// Feed the roster too, so the user's own role shows up in @mention
	// completion and author metadata like anyone else's.
	login := strings.TrimSpace(state.AuthorLogin)
	if login == "" {
		login = strings.TrimSpace(m.mentionLogin)
	}
	if login == "" {
		return
	}
	target.roster.observeMessage(twitch.ChatMessage{
		AuthorLogin: login,
		DisplayName: state.DisplayName,
		AuthorColor: state.AuthorColor,
		Badges:      state.Badges,
		Timestamp:   m.metricsNow(),
	})
	// observeMessage counts a message that was never sent; undo that so the
	// roster's message tally stays truthful.
	if entry, ok := target.roster.lookup(login); ok && entry.Messages > 0 {
		entry.Messages--
	}
}

// ingestMessage folds one arriving chat message into the shell: it queues the
// message for display, records it in the activity column, and raises a desktop
// notification if it is one of the system events worth interrupting for.
//
// Both message sources -- the mock chat used by `twi chat --mock` and the live
// transport -- ran these three steps inline in their own Update arm. Having
// them written twice meant a fourth step added to the live arm would silently
// not happen under --mock, which is the path least likely to be hand-tested.
//
// It returns the commands to batch rather than running them, because the
// caller has its own commands to add and the order they are batched in is the
// caller's business.
func (m *shellModel) ingestMessage(message twitch.ChatMessage) []tea.Cmd {
	var cmds []tea.Cmd
	if revealCmd := m.enqueueMessage(message); revealCmd != nil {
		cmds = append(cmds, revealCmd)
	}
	m.recordActivityFromMessage(message)
	if notificationCmd := m.maybeNotifyForSystemEvent(message); notificationCmd != nil {
		cmds = append(cmds, notificationCmd)
	}
	return cmds
}

func (m *shellModel) enqueueMessage(message twitch.ChatMessage) tea.Cmd {
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
	rowWidth := m.chatMessageContentWidth(layout)

	revealID := m.nextRevealID(message)
	options := m.messageRenderOptions(rowWidth, message, m.continuesActiveGroup(message))
	result := state.revealQueue.Enqueue(revealID, render.Rows(message, options))
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

func (m *shellModel) maybeNotifyForSystemEvent(message twitch.ChatMessage) tea.Cmd {
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
	if m.services.systemNotifier == nil {
		return nil
	}
	notifier := m.services.systemNotifier
	return func() tea.Msg {
		_ = notifier.Notify(context.Background(), notification)
		return nil
	}
}

func (m shellModel) shouldNotifyForSystemEvent(message twitch.ChatMessage) bool {
	if _, ok := systemNotificationFromMessage(message); !ok {
		return false
	}
	if !m.messageTargetsActiveChannel(message) {
		return true
	}
	if !m.terminalFocused {
		return true
	}
	return m.focus != focusChat || m.anyOverlayOpen()
}

func (m shellModel) messageTargetsActiveChannel(message twitch.ChatMessage) bool {
	channel := normalizeChannelName(message.Channel)
	if channel == "" {
		channel = m.activeChannelName()
	}
	if m.channels == nil {
		return channelKey(channel) == channelKey(m.activeChannelName())
	}
	return channelKey(channel) == m.channels.active
}

func (m *shellModel) nextRevealID(message twitch.ChatMessage) string {
	m.nextReveal++
	base := message.ID
	if base == "" {
		base = "mock-message"
	}
	return fmt.Sprintf("%s/%d", base, m.nextReveal)
}

func (m *shellModel) completeReveals(completed []animation.CompletedReveal) {
	state := m.activeChannelState()
	for _, reveal := range completed {
		message, ok := state.activeMessages[reveal.ID]
		if !ok {
			continue
		}
		preserveScrolledView := state.scrollOffset > 0
		beforeRows := 0
		if preserveScrolledView {
			beforeRows = len(m.chatRows(m.layout()))
		}
		delete(state.activeMessages, reveal.ID)
		m.removeActiveReveal(reveal.ID)
		m.appendStaticMessage(message, false)
		if preserveScrolledView {
			state.scrollOffset = clampMin(state.scrollOffset+len(m.chatRows(m.layout()))-beforeRows, 0)
		}
	}
}

func (m *shellModel) appendStaticMessage(message twitch.ChatMessage, preserveScrolledView bool) {
	state := m.channels.ensure(message.Channel)
	if state == nil {
		state = m.activeChannelState()
	}
	beforeRows := 0
	if preserveScrolledView {
		beforeRows = m.chatRowCount(m.layout())
	}
	if message.Channel == "" {
		message.Channel = state.name
	}
	state.messages = append(state.messages, message)
	state.trimScrollback(m.channels.scrollbackLimit)
	if preserveScrolledView {
		state.scrollOffset = clampMin(state.scrollOffset+m.chatRowCount(m.layout())-beforeRows, 0)
	}
}

func (m *shellModel) removeActiveReveal(id string) {
	state := m.activeChannelState()
	state.removeActiveRevealID(id)
}

// scheduleFrameTick starts the shared animation clock. It runs continuously
// (not just while something is mid-animation) whenever animation is enabled,
// driving the pulsing status indicators, startup splash,
// and command-palette typewriter reveal from one ticker.
func (m *shellModel) scheduleFrameTick() tea.Cmd {
	if m.frames.frameTickScheduled || m.animationMode == string(animation.ModeOff) {
		return nil
	}
	m.frames.frameTickScheduled = true
	return animation.ScheduleFrame(animation.DefaultFrameInterval)
}

// advanceFrame runs once per animation-clock tick. It records the frame for
// FPS measurement and advances the command-palette typewriter reveal. The
// splash expires based on a wall-clock deadline checked at render time, so it
// needs no per-tick bookkeeping here.
func (m *shellModel) advanceFrame(now time.Time) {
	m.frames.lastFrameAt = now
	m.frames.frameTimestamps = append(m.frames.frameTimestamps, now)
	cutoff := now.Add(-time.Second)
	trimmed := m.frames.frameTimestamps[:0]
	for _, ts := range m.frames.frameTimestamps {
		if ts.After(cutoff) {
			trimmed = append(trimmed, ts)
		}
	}
	m.frames.frameTimestamps = trimmed
	m.sampleResourceUsage(now)
	m.trimChatByteSamples(now)
	if m.palette.open {
		m.refreshPaletteReveal(now)
	}
}

func (m *shellModel) scheduleRevealTick() tea.Cmd {
	if m.frames.revealTickScheduled || m.activeChannelState().revealQueue.Len() == 0 {
		return nil
	}
	m.frames.revealTickScheduled = true
	return tea.Tick(mockRevealDelay, func(time.Time) tea.Msg {
		return mockAnimationTickMsg{}
	})
}

// withAsyncAssetCommands schedules the async lookups that keep channel
// emote autocomplete current (broadcaster ID, then that channel's emote
// index) alongside whatever other commands the caller already produced.
func (m *shellModel) withAsyncAssetCommands(cmds ...tea.Cmd) (shellModel, tea.Cmd) {
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

func (m *shellModel) refreshActiveRevealRows() {
	state := m.activeChannelState()
	if state.revealQueue == nil || state.revealQueue.Len() == 0 {
		return
	}
	layout := m.layout()
	rowWidth := m.chatMessageContentWidth(layout)
	for _, id := range state.activeOrder {
		message, ok := state.activeMessages[id]
		if !ok {
			continue
		}
		state.revealQueue.ReplaceRows(id, render.Rows(message, m.messageRenderOptions(rowWidth, message, m.continuesActiveGroup(message))))
	}
}

func animationConfigFor(mode string) animation.Config {
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

func (m shellModel) renderOptions(width int) render.Options {
	opts := render.DefaultOptions(width)
	opts.Palette = m.theme
	opts.Assets = render.FallbackAssetOptions()
	opts.Assets.ShowAvatars = m.avatarMode != "off"
	opts.Layout = m.display.messageLayout
	opts.Badges = m.display.badgeMode
	opts.HighlightEmotes = m.display.highlightEmotes
	opts.FullUsername = m.display.fullUsername
	return opts
}

// messageRenderOptions returns render options for one message, adding the
// per-message context Rows cannot infer: whether it continues the previous
// author's group, and what twi knows about the author.
func (m shellModel) messageRenderOptions(width int, message twitch.ChatMessage, continuesGroup bool) render.Options {
	opts := m.renderOptions(width)
	opts.ContinuesGroup = continuesGroup
	opts.Meta = m.authorMeta(message)
	return opts
}

// continuesActiveGroup reports whether message follows another from the same
// author at the tail of the active channel, matching what
// assignChatAuthorGroups would decide once the message is on screen. Reveal
// rows are rendered before that pass runs, so grouping has to be predicted
// here or an animating message would draw a duplicate author header.
func (m shellModel) continuesActiveGroup(message twitch.ChatMessage) bool {
	state := m.activeChannelState()
	if state == nil {
		return false
	}
	var previous twitch.ChatMessage
	switch {
	case len(state.activeOrder) > 0:
		previous = state.activeMessages[state.activeOrder[len(state.activeOrder)-1]]
	case len(state.messages) > 0:
		previous = state.messages[len(state.messages)-1]
	default:
		return false
	}
	if !m.messageVisibleForState(state, previous) {
		return false
	}
	// The block indices only matter for anonymous messages, which never
	// group; any distinct pair of indices gives the same answer here.
	return chatAuthorGroupKey(previous, 0) == chatAuthorGroupKey(message, 1)
}

// authorMeta resolves roster context for a message's author. An author twi
// has never recorded yields a zero AuthorMeta, which renders no claims at all.
func (m shellModel) authorMeta(message twitch.ChatMessage) render.AuthorMeta {
	state := m.activeChannelState()
	if state == nil {
		return render.AuthorMeta{}
	}
	login := message.AuthorLogin
	if strings.TrimSpace(login) == "" {
		login = message.DisplayName
	}
	entry, ok := state.roster.lookup(login)
	if !ok {
		return render.AuthorMeta{}
	}
	return render.AuthorMeta{
		Role:             entry.roleLabel(),
		SubscribedMonths: entry.SubscribedMonths,
		FollowsSince:     entry.FollowsSince,
		FollowKnown:      entry.FollowKnown,
		FirstSeen:        entry.FirstSeen,
		// Truncated to the minute because render.humanizeDuration is
		// minute-granular at its finest: a raw clock would produce identical
		// output while changing on every frame, which defeats row caching for
		// every message whose author twi knows anything about.
		Now: m.metricsNow().Truncate(time.Minute),
	}
}

// insertComposerText appends typed or pasted text to the composer.
//
// A bracketed paste arrives as one burst of runes and can carry line breaks
// and more text than Twitch will accept. The transport already refuses to put
// a line break on the wire, but silently sending only part of what the
// composer shows is its own surprise, so both are resolved here where the
// user can still see and edit the result: breaks collapse to spaces, and the
// text stops at Twitch's limit rather than being trimmed later.
func (m *shellModel) insertComposerText(text string) {
	if text == "" {
		return
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// Control characters have no meaning in a chat message and are
			// interpreted by terminals that render it later.
		default:
			b.WriteRune(r)
		}
	}

	state := m.activeChannelState()
	combined := []rune(state.composerText + b.String())
	if len(combined) > twitch.MaxChatMessageRunes {
		combined = combined[:twitch.MaxChatMessageRunes]
		state.sendFeedback = fmt.Sprintf("message capped at %d characters", twitch.MaxChatMessageRunes)
	}
	state.composerText = string(combined)
}

func (m *shellModel) deleteComposerRune() {
	state := m.activeChannelState()
	if state.composerText == "" {
		return
	}
	runes := []rune(state.composerText)
	state.composerText = string(runes[:len(runes)-1])
}

func (m *shellModel) selectReplyMessage(delta int) {
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

func (m *shellModel) startReplyMode() {
	state := m.activeChannelState()
	if state.replyTo == nil {
		m.selectReplyMessage(-1)
	}
	if state.replyTo != nil {
		m.focus = focusComposer
	}
}

func (m shellModel) replyableMessages() []twitch.ChatMessage {
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

func (m shellModel) replyContextLine(width int) string {
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

func (m shellModel) focusName() string {
	if m.palette.open {
		return "palette"
	}
	if m.focus == focusComposer {
		return "composer"
	}
	if m.focus == focusSidebar {
		return "channels"
	}
	return "chat"
}

func (m shellModel) helpLines(width, height int) []string {
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
		line := compactHelpLine()
		if width >= 112 {
			line += " | " + source
		}
		return []string{line}
	}

	// Below 38 columns there is no room for the generated groups, so the help
	// falls back to a hand-picked set of the five keys that matter most on a
	// very narrow terminal. This is a deliberate editorial choice rather than
	// a projection of the keymap, which is why it is written out; checking the
	// width first keeps it from looking like the generated lines are being
	// discarded.
	if width < 38 {
		return []string{
			" ctrl+p: commands",
			" i/esc | tab | jk",
			" ?: help | ctrl+c: quit",
		}
	}

	// The wide help is generated from keyBindings so it cannot drift from the
	// keymap. The three surfaces that document keys used to be three
	// hand-maintained lists, and ctrl+e had already fallen out of all of them.
	lines := []string{
		helpGroupLine(keyGroupChat),
		helpGroupLine(keyGroupChannels),
		helpGroupLine(keyGroupView),
		helpGroupLine(keyGroupSession) + " | " + source,
		// Display toggles go last: when a short terminal truncates the help,
		// the navigation keys are the ones that must survive.
		helpGroupLine(keyGroupDisplay),
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
	out := padPaneLines(lines, width, height)
	return strings.Join(out, "\n")
}
