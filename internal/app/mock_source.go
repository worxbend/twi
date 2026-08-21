package app

import (
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// Mock mode: the seeded chat source behind `twi chat --mock`, and the
// entry points that run the shell against it. Everything here exists to
// demonstrate and test the UI without credentials or network; the shell
// itself lives in shell.go and knows nothing about any of it.

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

	model := newMockModelWithClock(channel, cfg, nil)
	model.debugLogger = opts.DebugLogger
	model.debugAppStart("mock", len(configuredChannels(channel, cfg.DefaultChannels)))
	if !isInteractiveTerminal(w) {
		_, err := fmt.Fprintln(w, model.View())
		return err
	}
	if opts.SystemNotifier == nil {
		opts.SystemNotifier = newDefaultSystemNotifier(w)
	}
	model.services.systemNotifier = opts.SystemNotifier
	model.frames.splashUntil = splashDeadline(model.animationMode)
	model.terminalOutput = w
	primeTerminalBackground(w, model.canvasBackground())

	program := tea.NewProgram(model, programOptions(w, cfg)...)
	_, err := program.Run()
	resetTerminalBackground(w)
	return err
}

func newMockModel(channel string, cfg config.Config) shellModel {
	return newMockModelWithClock(channel, cfg, nil)
}

func newMockModelWithClock(channel string, cfg config.Config, clock animation.Clock) shellModel {
	connectedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	model := newShellModel(channel, cfg, clock)
	channels := model.channels
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
	model.sourceDetail = "mock source: no network"
	model.incoming = incomingMockMessages(channels.activeName(), connectedAt)
	model.emotes.emoteEntries = emoteEntries
	return model
}

// sampleEmoteEntries seeds the Ctrl+E emote picker in --mock mode with
// well-known Twitch global emote names, so it is demoable without
// credentials or network access.
func sampleEmoteEntries() []assets.EmoteEntry {
	names := []string{
		"Kappa", "✨", "💜", "🔥", "😂", "🎉", "👀", "🚀", "💬", "🌈", "⚡",
		"PogChamp", "LUL", "monkaS", "KEKW", "5Head", "EZ", "PagMan",
		"OMEGALUL", "Pog", "BibleThump", "TriHard", "VoHiYo", "ResidentSleeper",
		"NotLikeThis", "SeemsGood", "HeyGuys", "DansGame",
	}
	entries := make([]assets.EmoteEntry, len(names))
	for i, name := range names {
		entries[i] = assets.EmoteEntry{Name: name}
	}
	return entries
}

func builtInEmojiEntries() []assets.EmoteEntry {
	names := []string{"✨", "💜", "🔥", "😂", "🎉", "👀", "🚀", "💬", "🌈", "⚡"}
	entries := make([]assets.EmoteEntry, len(names))
	for i, name := range names {
		entries[i] = assets.EmoteEntry{Name: name}
	}
	return entries
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
			Text:        "✨ Mock chat is ready in the Bubble Tea shell. 💜",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-2",
			Channel:     channel,
			Timestamp:   startedAt.Add(2 * time.Second),
			AuthorLogin: "viewer42",
			DisplayName: "viewer42",
			AuthorColor: "#00d1ff",
			Text:        "@twi_bot composer, help, and status regions are visible. 👀",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-3",
			Channel:     channel,
			Timestamp:   startedAt.Add(3 * time.Second),
			AuthorLogin: "moderator",
			DisplayName: "moderator",
			AuthorColor: "#00f593",
			Text:        "🔔 No Twitch credentials or network calls are used for --mock.",
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
			Text:        "This incoming message reveals through animated chat frames. 🚀",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mock-live-2",
			Channel:     channel,
			Timestamp:   startedAt.Add(5 * time.Second),
			AuthorLogin: "vip_guest",
			DisplayName: "vip_guest",
			AuthorColor: "#f38ba8",
			Text:        "Scrolling and the composer stay responsive while frames advance. 🎉",
			Type:        twitch.MessageTypeChat,
		},
	}
}
