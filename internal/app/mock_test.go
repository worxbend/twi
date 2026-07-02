package app

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

type appFakeClock struct {
	now time.Time
}

func (c *appFakeClock) Now() time.Time {
	return c.now
}

func (c *appFakeClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestRunMockRendersInitialShellForNonInteractiveOutput(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"example"}

	var out bytes.Buffer
	if err := RunMock(&out, cfg); err != nil {
		t.Fatalf("RunMock returned error: %v", err)
	}

	view := out.String()
	for _, want := range []string{
		"#example",
		"connected",
		"Mock chat is ready in the Bubble Tea shell.",
		"[TB]",
		"Message #example",
		"q quit",
		"ctrl+c quit",
		"no network",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("initial view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Run without --mock") {
		t.Fatalf("initial view still contains old static snapshot text:\n%s", view)
	}
}

func TestShellImageCapabilityStateIsDeterministic(t *testing.T) {
	imageFeatures := config.Default().Features
	imageFeatures.AvatarMode = "image"
	imageFeatures.EmojiMode = "image"
	imageFeatures.EmoteMode = "image"

	for _, tt := range []struct {
		name       string
		features   config.FeatureConfig
		wantStatus render.ImageCapabilityStatus
		wantActive bool
	}{
		{
			name:       "auto without terminal signal is unsupported",
			features:   imageFeatures,
			wantStatus: render.ImageCapabilityUnsupported,
			wantActive: false,
		},
		{
			name: "off disables image features",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			wantStatus: render.ImageCapabilityDisabled,
			wantActive: false,
		},
		{
			name: "explicit image mode is degraded but active",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "normal"
				return features
			}(),
			wantStatus: render.ImageCapabilityDegraded,
			wantActive: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features = tt.features
			model := newMockShellModel("example", cfg)

			if model.imageCapability.Status != tt.wantStatus {
				t.Fatalf("image capability status = %q, want %q; summary=%s", model.imageCapability.Status, tt.wantStatus, model.imageCapability.Summary())
			}
			if model.imageCapability.Avatar.Active != tt.wantActive ||
				model.imageCapability.Emoji.Active != tt.wantActive ||
				model.imageCapability.Emote.Active != tt.wantActive {
				t.Fatalf("image feature activity = avatar:%v emoji:%v emote:%v, want %v",
					model.imageCapability.Avatar.Active,
					model.imageCapability.Emoji.Active,
					model.imageCapability.Emote.Active,
					tt.wantActive)
			}

			opts := model.renderOptions(80).Assets
			if tt.wantActive {
				if opts.EmojiWidthCells == 0 || opts.EmoteWidthCells == 0 || opts.BadgeWidthCells == 0 {
					t.Fatalf("active image asset options = %#v, want reserved image widths", opts)
				}
			} else if opts.EmojiWidthCells != 0 || opts.EmoteWidthCells != 0 || opts.BadgeWidthCells != 0 {
				t.Fatalf("inactive image asset options = %#v, want no image-only widths", opts)
			}
		})
	}
}

func TestShellImageFallbackRowsStayStableByCapabilityState(t *testing.T) {
	message := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 0, 0, time.Local),
		AuthorID:    "user-1",
		AuthorLogin: "viewer_fan",
		DisplayName: "viewer_fan",
		Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: "😀"},
		},
	}
	imageFeatures := config.Default().Features
	imageFeatures.AvatarMode = "image"
	imageFeatures.EmojiMode = "image"
	imageFeatures.EmoteMode = "image"

	tests := []struct {
		name       string
		features   config.FeatureConfig
		wantRows   []string
		wantEmoteW int
	}{
		{
			name:       "auto unsupported keeps text tokens",
			features:   imageFeatures,
			wantRows:   []string{"[VF] 20:00 [moderator] view...: Kappa 😀"},
			wantEmoteW: 5,
		},
		{
			name: "image off keeps text tokens",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			wantRows:   []string{"[VF] 20:00 [moderator] view...: Kappa 😀"},
			wantEmoteW: 5,
		},
		{
			name: "enabled mode reserves placeholders",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "normal"
				return features
			}(),
			wantRows:   []string{"[VF] 20:00 [mod] viewer_fan: Kappa  😀"},
			wantEmoteW: 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features = tt.features
			model := newMockShellModel("example", cfg)
			rows := render.Rows(message, model.renderOptions(80))
			if got := renderRowsToPlain(rows); !reflect.DeepEqual(got, tt.wantRows) {
				t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, tt.wantRows)
			}
			emote, ok := firstRenderKind(rows, render.FragmentEmoteFallback)
			if !ok {
				t.Fatalf("missing emote fallback fragment: %#v", rows)
			}
			if got := emote.Width(); got != tt.wantEmoteW {
				t.Fatalf("emote width = %d, want %d: %#v", got, tt.wantEmoteW, emote)
			}
		})
	}
}

func TestMockShellQuitsOnQAndCtrlC(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	for name, msg := range map[string]tea.KeyMsg{
		"q":      {Type: tea.KeyRunes, Runes: []rune{'q'}},
		"ctrl+c": {Type: tea.KeyCtrlC},
	} {
		t.Run(name, func(t *testing.T) {
			_, cmd := model.Update(msg)
			if cmd == nil {
				t.Fatal("Update returned nil command, want tea.Quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("Update command produced %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

func TestMockShellWindowSizeKeepsViewWithinHeight(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 64, Height: 12})
	view := updated.View()

	if got, want := lineCount(view), 12; got != want {
		t.Fatalf("view line count = %d, want %d:\n%s", got, want, view)
	}
}

func TestMockShellFocusHelpAndComposerInput(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after tab = %v, want %v", got, want)
	}
	if !strings.Contains(model.View(), "focus=composer") {
		t.Fatalf("composer focus marker missing:\n%s", model.View())
	}

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		updated, cmd := model.Update(msg)
		if cmd != nil {
			t.Fatalf("composer input returned command for %#v", msg)
		}
		model = updated.(mockShellModel)
	}
	if got, want := model.activeChannelState().composerText, "hi q"; got != want {
		t.Fatalf("composer text = %q, want %q", got, want)
	}
	if !strings.Contains(model.View(), "hi q") {
		t.Fatalf("composer view missing typed text:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(mockShellModel)
	if !model.helpExpanded {
		t.Fatal("helpExpanded = false, want true")
	}
	if !strings.Contains(model.View(), "pgup/pgdn") {
		t.Fatalf("expanded help missing page key hint:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after second tab = %v, want %v", got, want)
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q from chat focus returned nil command, want tea.Quit")
	}
}

func TestMockShellPageKeysScrollViewport(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = numberedMockMessages("example", 12)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	bottom := model.View()
	if !strings.Contains(bottom, "message-11") {
		t.Fatalf("bottom viewport missing latest message:\n%s", bottom)
	}
	if strings.Contains(bottom, "message-00") {
		t.Fatalf("bottom viewport unexpectedly contains oldest message:\n%s", bottom)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	scrolled := model.View()
	if !strings.Contains(scrolled, "message-04") {
		t.Fatalf("page up viewport missing previous page message:\n%s", scrolled)
	}
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("scrollOffset = 0 after page up, want non-zero")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if !strings.Contains(model.View(), "message-00") {
		t.Fatalf("second page up viewport missing oldest message:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("scrollOffset after one page down = 0, want still scrolled")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset != 0 {
		t.Fatalf("scrollOffset after second page down = %d, want 0", model.activeChannelState().scrollOffset)
	}
	if !strings.Contains(model.View(), "message-11") {
		t.Fatalf("second page down viewport missing latest message:\n%s", model.View())
	}
}

func TestMockShellMouseEventsWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	model.channels.ensure("alpha").messages = numberedMockMessages("alpha", 12)
	model.channels.ensure("beta").messages = numberedMockMessages("beta", 3)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 88, Height: 12})
	model = updated.(mockShellModel)
	layout := model.layout()
	chatX := layout.sidebarWidth + 2
	contentY := layout.statusHeight + 1

	updated, cmd := model.Update(tea.MouseMsg{
		X:      chatX,
		Y:      contentY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("mouse wheel returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("mouse wheel up left scrollOffset at 0")
	}
	if !strings.Contains(model.View(), "message-08") {
		t.Fatalf("mouse-scrolled viewport missing older row:\n%s", model.View())
	}

	betaY := layout.statusHeight + 1 + 2
	updated, cmd = model.Update(tea.MouseMsg{
		X:      2,
		Y:      betaY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(mockShellModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("channel click command produced %T, want nil or no-op", msg)
		}
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel after sidebar click = %q, want %q", got, want)
	}
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after sidebar click = %v, want %v", got, want)
	}

	composerY := layout.statusHeight + layout.chatHeight + 1
	updated, _ = model.Update(tea.MouseMsg{
		X:      10,
		Y:      composerY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after composer click = %v, want %v", got, want)
	}

	if !model.channels.setActive("alpha") {
		t.Fatal("test setup failed to switch back to alpha")
	}
	model.activeChannelState().scrollOffset = 0
	layout = model.layout()
	latestY := layout.statusHeight + 1 + layout.chatContentHeight - 1
	updated, _ = model.Update(tea.MouseMsg{
		X:      layout.sidebarWidth + 4,
		Y:      latestY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(mockShellModel)
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "mock-11" {
		t.Fatalf("replyTo after message click = %#v, want mock-11", model.activeChannelState().replyTo)
	}
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after message click = %v, want %v", got, want)
	}
}

func TestMockShellMouseEventsIgnoredWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.EnableMouse = false
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	model.channels.ensure("alpha").messages = numberedMockMessages("alpha", 12)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 88, Height: 12})
	model = updated.(mockShellModel)
	layout := model.layout()

	events := []tea.MouseMsg{
		{X: layout.sidebarWidth + 2, Y: layout.statusHeight + 1, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress},
		{X: 2, Y: layout.statusHeight + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 10, Y: layout.statusHeight + layout.chatHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: layout.sidebarWidth + 4, Y: layout.statusHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
	}
	for _, event := range events {
		updated, cmd := model.Update(event)
		if cmd != nil {
			t.Fatalf("disabled mouse event returned command %#v, want nil", cmd)
		}
		model = updated.(mockShellModel)
	}

	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel after disabled mouse events = %q, want %q", got, want)
	}
	if got := model.activeChannelState().scrollOffset; got != 0 {
		t.Fatalf("scrollOffset after disabled mouse events = %d, want 0", got)
	}
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after disabled mouse events = %v, want %v", got, want)
	}
	if model.activeChannelState().replyTo != nil {
		t.Fatalf("replyTo after disabled mouse events = %#v, want nil", model.activeChannelState().replyTo)
	}
}

func TestInspectPanelShowsSelectedMessageMetadataAndRedactsDiagnostics(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	secretToken := "oauth" + ":" + "secret-token"
	clientSecretKey := strings.Join([]string{"client", "secret"}, "_")
	clientSecretValue := "client" + "SecretValue"
	bearerSecret := "bearer" + "SecretValue"
	querySecret := "query" + "SecretValue"
	message := twitch.ChatMessage{
		ID:          "inspect-1",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		AuthorLogin: "viewer_login",
		AuthorID:    "user-123",
		DisplayName: "Viewer",
		AuthorColor: "#00d1ff",
		Badges: []twitch.Badge{
			{SetID: "moderator", ID: "1", Info: "primary", Ref: twitch.AssetRef{Kind: "badge", ID: "moderator/1", URL: "https://cdn.example.invalid/badge.png"}},
		},
		Text: "please do not show " + secretToken,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "hello"},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25", URL: "https://cdn.example.invalid/emote.png"}},
		},
		Emotes: []twitch.Emote{{ID: "25", Name: "Kappa", Start: 0, End: 4, Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}}},
		Type:   twitch.MessageTypeChat,
		RawTags: map[string]string{
			"id":             "inspect-1",
			clientSecretKey:  clientSecretValue,
			"notice":         "Bearer " + bearerSecret,
			"redirect-query": "https://example.invalid/callback?access_token=" + querySecret,
		},
	}
	model.activeChannelState().messages = []twitch.ChatMessage{message}
	model.activeChannelState().replyTo = replyContextFromMessage(message)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("inspect toggle returned command %#v, want nil", cmd)
	}
	if !model.inspectOpen {
		t.Fatal("inspectOpen = false, want true")
	}

	view := model.View()
	for _, want := range []string{
		"Inspect: selected message",
		"id=inspect-1",
		"author:",
		"display=Viewer",
		"badges: moderator/1(primary) ref=badge:moderator/1",
		"raw tags:",
		clientSecretKey + "=[redacted]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("inspect view missing %q:\n%s", want, view)
		}
	}
	for _, secret := range []string{secretToken, clientSecretValue, bearerSecret, querySecret} {
		if strings.Contains(view, secret) {
			t.Fatalf("inspect view leaked %q:\n%s", secret, view)
		}
	}
	if strings.Contains(view, "https://cdn.example.invalid") {
		t.Fatalf("inspect view exposed asset URL:\n%s", view)
	}
	if !strings.Contains(view, "[redacted]") {
		t.Fatalf("inspect view missing redaction marker:\n%s", view)
	}
}

func TestInspectPanelOpenClosePreservesComposerSelectionReplyAndScroll(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = numberedMockMessages("example", 18)
	model.width = 88
	model.height = 18
	state := model.activeChannelState()
	state.composerText = "draft text"
	state.replyTo = replyContextFromMessage(state.messages[12])
	state.scrollOffset = 3
	model.focus = mockFocusChat

	beforeReply := *state.replyTo
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("open inspect returned command %#v, want nil", cmd)
	}
	if !model.inspectOpen {
		t.Fatal("inspectOpen after open = false, want true")
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composerText after open = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().scrollOffset, 3; got != want {
		t.Fatalf("scrollOffset after open = %d, want %d", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after open = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("close inspect returned command %#v, want nil", cmd)
	}
	if model.inspectOpen {
		t.Fatal("inspectOpen after esc = true, want false")
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composerText after close = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().scrollOffset, 3; got != want {
		t.Fatalf("scrollOffset after close = %d, want %d", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after close = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after close = %v, want %v", got, want)
	}
}

func TestLiveShellEnterQueuesComposerSendAndSuccessKeepsComposerCleared(t *testing.T) {
	client := NewFakeChatClient(1)
	acceptedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := client.QueueSendResult(SendResult{AcceptedAt: acceptedAt, Detail: "accepted by Twitch"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = " hello chat "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("Enter returned nil command, want send command")
	}
	if got := model.activeChannelState().composerText; got != "" {
		t.Fatalf("composerText after queue = %q, want cleared", got)
	}
	if model.activeChannelState().activeSend == nil || model.activeChannelState().activeSend.Channel != "example" || model.activeChannelState().activeSend.Text != "hello chat" {
		t.Fatalf("activeSend = %#v, want queued trimmed send for #example", model.activeChannelState().activeSend)
	}
	if got, want := model.activeChannelState().sendState, composerSendSending; got != want {
		t.Fatalf("sendState after queue = %q, want %q", got, want)
	}

	sendMsg := cmd()
	completed, ok := sendMsg.(composerSendCompletedMsg)
	if !ok {
		t.Fatalf("send command returned %T, want composerSendCompletedMsg", sendMsg)
	}
	updated, cmd = model.Update(completed)
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("successful single send returned extra command %#v", cmd)
	}
	if got := model.activeChannelState().composerText; got != "" {
		t.Fatalf("composerText after success = %q, want cleared", got)
	}
	if got, want := model.activeChannelState().sendState, composerSendSucceeded; got != want {
		t.Fatalf("sendState after success = %q, want %q", got, want)
	}
	sent := client.SentRequests()
	if len(sent) != 1 || sent[0].Channel != "example" || sent[0].Text != "hello chat" {
		t.Fatalf("SentRequests = %#v, want one trimmed send to active channel", sent)
	}
	if !strings.Contains(model.View(), "accepted by Twitch") {
		t.Fatalf("view missing success detail:\n%s", model.View())
	}
}

func TestLiveShellEnterIgnoresEmptyComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "   "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("empty composer returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().activeSend != nil || len(model.activeChannelState().sendQueue) != 0 {
		t.Fatalf("empty composer queued send: active=%#v queue=%#v", model.activeChannelState().activeSend, model.activeChannelState().sendQueue)
	}
	if got := client.SentRequests(); len(got) != 0 {
		t.Fatalf("SentRequests length = %d, want 0", len(got))
	}
}

func TestLiveShellFailedSendShowsReasonAndRestoresComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "please send"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.activeChannelState().composerText, "please send"; got != want {
		t.Fatalf("composerText after failed send = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().sendState, composerSendFailed; got != want {
		t.Fatalf("sendState after failure = %q, want %q", got, want)
	}
	for _, want := range []string{"failed", "network unavailable"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after failed send:\n%s", want, model.View())
		}
	}
}

func TestLiveShellSendFailureUsesSendScopeGuidance(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("missing scope for oauth:secret-token")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "please send"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if strings.Contains(model.activeChannelState().sendFeedback, "oauth:secret-token") {
		t.Fatalf("sendFeedback leaked token: %q", model.activeChannelState().sendFeedback)
	}
	for _, want := range []string{"chat:edit", "send failed"} {
		if !strings.Contains(model.activeChannelState().sendFeedback, want) {
			t.Fatalf("sendFeedback = %q, want %q", model.activeChannelState().sendFeedback, want)
		}
	}
	if strings.Contains(model.activeChannelState().sendFeedback, "chat:read") {
		t.Fatalf("sendFeedback points at read scope instead of edit scope: %q", model.activeChannelState().sendFeedback)
	}
}

func TestLiveShellFailedSendRestoresQueuedFollowupText(t *testing.T) {
	client := NewFakeChatClient(2)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "first message"

	updated, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if firstCmd == nil {
		t.Fatal("first Enter returned nil command, want send command")
	}

	model.activeChannelState().composerText = "second message"
	updated, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if secondCmd != nil {
		t.Fatalf("queued follow-up returned command %#v while first send active, want nil", secondCmd)
	}
	if got := model.activeChannelState().composerText; got != "" {
		t.Fatalf("composerText after queued follow-up = %q, want cleared", got)
	}
	if got := len(model.activeChannelState().sendQueue); got != 1 {
		t.Fatalf("sendQueue length = %d, want 1 queued follow-up", got)
	}

	completed := firstCmd().(composerSendCompletedMsg)
	updated, cmd := model.Update(completed)
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("failed send returned next queued command %#v, want queue stopped", cmd)
	}
	if got := len(model.activeChannelState().sendQueue); got != 0 {
		t.Fatalf("sendQueue length after failure = %d, want drained into composer", got)
	}
	for _, want := range []string{"first message", "second message"} {
		if !strings.Contains(model.activeChannelState().composerText, want) {
			t.Fatalf("composerText after failed queued send = %q, want it to contain %q", model.activeChannelState().composerText, want)
		}
	}
	if got := len(client.SentRequests()); got != 1 {
		t.Fatalf("SentRequests length = %d, want only first send attempted", got)
	}
	for _, want := range []string{"failed", "network unavailable"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after queued failure:\n%s", want, model.View())
		}
	}
}

func TestLiveShellRateLimitedSendShowsReasonAndRestoresComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{
		RateLimited: true,
		RetryAfter:  30 * time.Second,
		Detail:      "sending messages too quickly",
	}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "slow down?"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.activeChannelState().composerText, "slow down?"; got != want {
		t.Fatalf("composerText after rate limit = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().sendState, composerSendRateLimited; got != want {
		t.Fatalf("sendState after rate limit = %q, want %q", got, want)
	}
	for _, want := range []string{"rate limited", "sending messages too quickly"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after rate limit:\n%s", want, model.View())
		}
	}
}

func TestLiveShellKeepsComposerSendStatePerChannel(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(3)
	for _, queued := range []struct {
		result SendResult
		err    error
	}{
		{result: SendResult{AcceptedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), Detail: "beta action accepted"}},
		{result: SendResult{RateLimited: true, RetryAfter: 10 * time.Second, Detail: "alpha cooldown"}},
		{err: fmt.Errorf("network unavailable")},
	} {
		if err := client.QueueSendResult(queued.result, queued.err); err != nil {
			t.Fatalf("QueueSendResult returned error: %v", err)
		}
	}
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)
	alpha := model.channels.ensure("alpha")
	beta := model.channels.ensure("beta")
	alpha.messages = []twitch.ChatMessage{
		{ID: "alpha-parent", Channel: "alpha", Timestamp: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), DisplayName: "alpha_viewer", Text: "alpha question", Type: twitch.MessageTypeChat},
	}
	beta.messages = []twitch.ChatMessage{
		{ID: "beta-parent", Channel: "beta", Timestamp: time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC), DisplayName: "beta_viewer", Text: "beta question", Type: twitch.MessageTypeChat},
	}

	alpha.composerText = "alpha draft"
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("alpha reply selection returned command %#v, want nil", cmd)
	}
	if alpha.replyTo == nil || alpha.replyTo.MessageID != "alpha-parent" {
		t.Fatalf("alpha replyTo = %#v, want alpha-parent", alpha.replyTo)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("switch to beta returned command %#v, want nil", cmd)
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if got, want := alpha.composerText, "alpha draft"; got != want {
		t.Fatalf("alpha draft after switching = %q, want %q", got, want)
	}
	if got := beta.composerText; got != "" {
		t.Fatalf("beta draft after switching = %q, want empty", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(mockShellModel)
	if beta.replyTo == nil || beta.replyTo.MessageID != "beta-parent" {
		t.Fatalf("beta replyTo = %#v, want beta-parent", beta.replyTo)
	}
	model.focus = mockFocusComposer
	beta.composerText = " /me beta waves "
	updated, betaActionCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if betaActionCmd == nil {
		t.Fatal("beta /me Enter returned nil command, want send command")
	}
	if beta.replyTo != nil || beta.composerText != "" {
		t.Fatalf("beta composer after queue = text %q reply %#v, want cleared", beta.composerText, beta.replyTo)
	}

	model.focus = mockFocusChat
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if alpha.replyTo == nil || alpha.replyTo.MessageID != "alpha-parent" || alpha.composerText != "alpha draft" {
		t.Fatalf("alpha state after beta queue = text %q reply %#v, want preserved", alpha.composerText, alpha.replyTo)
	}
	updated, _ = model.Update(betaActionCmd().(composerSendCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := alpha.composerText, "alpha draft"; got != want {
		t.Fatalf("alpha draft after off-channel beta completion = %q, want %q", got, want)
	}
	if got, want := beta.sendState, composerSendSucceeded; got != want {
		t.Fatalf("beta sendState after action success = %q, want %q", got, want)
	}

	model.focus = mockFocusComposer
	alpha.composerText = "alpha reply body"
	updated, alphaRateLimitCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if alphaRateLimitCmd == nil {
		t.Fatal("alpha reply Enter returned nil command, want send command")
	}
	updated, _ = model.Update(alphaRateLimitCmd().(composerSendCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := alpha.composerText, "alpha reply body"; got != want {
		t.Fatalf("alpha draft after rate limit = %q, want %q", got, want)
	}
	if alpha.replyTo == nil || alpha.replyTo.MessageID != "alpha-parent" {
		t.Fatalf("alpha replyTo after rate limit = %#v, want alpha-parent", alpha.replyTo)
	}
	if got, want := alpha.sendState, composerSendRateLimited; got != want {
		t.Fatalf("alpha sendState after rate limit = %q, want %q", got, want)
	}
	if !strings.Contains(alpha.sendFeedback, "alpha cooldown") {
		t.Fatalf("alpha sendFeedback = %q, want rate-limit detail", alpha.sendFeedback)
	}

	model.focus = mockFocusChat
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if strings.Contains(model.View(), "alpha cooldown") {
		t.Fatalf("beta view leaked alpha rate-limit feedback:\n%s", model.View())
	}
	model.focus = mockFocusComposer
	beta.composerText = "beta failed send"
	updated, betaFailureCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if betaFailureCmd == nil {
		t.Fatal("beta failed-send Enter returned nil command, want send command")
	}

	model.focus = mockFocusChat
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(mockShellModel)
	alpha.composerText = "alpha fresh draft"
	updated, _ = model.Update(betaFailureCmd().(composerSendCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := beta.composerText, "beta failed send"; got != want {
		t.Fatalf("beta draft after off-channel failure = %q, want %q", got, want)
	}
	if got, want := alpha.composerText, "alpha fresh draft"; got != want {
		t.Fatalf("alpha draft after off-channel beta failure = %q, want %q", got, want)
	}
	if got, want := beta.sendState, composerSendFailed; got != want {
		t.Fatalf("beta sendState after failure = %q, want %q", got, want)
	}
	if !strings.Contains(beta.sendFeedback, "network unavailable") {
		t.Fatalf("beta sendFeedback = %q, want failure detail", beta.sendFeedback)
	}

	sent := client.SentRequests()
	if got, want := len(sent), 3; got != want {
		t.Fatalf("SentRequests length = %d, want %d: %#v", got, want, sent)
	}
	if sent[0].Channel != "beta" || !sent[0].Action || sent[0].Text != "beta waves" {
		t.Fatalf("first send = %#v, want beta /me action", sent[0])
	}
	if sent[1].Channel != "alpha" || sent[1].ReplyToMessageID != "alpha-parent" || sent[1].Text != "alpha reply body" {
		t.Fatalf("second send = %#v, want alpha reply", sent[1])
	}
	if sent[2].Channel != "beta" || sent[2].Text != "beta failed send" {
		t.Fatalf("third send = %#v, want beta failed send", sent[2])
	}
}

func TestLiveShellSidebarSwitchesChannelsAndPreservesDrafts(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)
	model.width = 88
	model.height = 14

	alpha := model.channels.ensure("alpha")
	beta := model.channels.ensure("beta")
	alpha.status = ConnectionState{Status: ConnectionConnected, Channel: "alpha"}
	beta.status = ConnectionState{Status: ConnectionDisconnected, Channel: "beta"}
	alpha.composerText = "alpha draft"
	beta.composerText = "beta draft"
	beta.unread = 2
	model.focus = mockFocusChat

	view := model.View()
	for _, want := range []string{"Channels", "> * #alpha", "! #beta 2", "alpha draft"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sidebar view missing %q:\n%s", want, view)
		}
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("channel switch returned command %#v, want nil", cmd)
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if got, want := alpha.composerText, "alpha draft"; got != want {
		t.Fatalf("alpha draft after switch = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().composerText, "beta draft"; got != want {
		t.Fatalf("active beta draft = %q, want %q", got, want)
	}
	if got := beta.unread; got != 0 {
		t.Fatalf("beta unread after activation = %d, want 0", got)
	}
	view = model.View()
	for _, want := range []string{"> ! #beta", "beta draft"} {
		if !strings.Contains(view, want) {
			t.Fatalf("active beta view missing %q:\n%s", want, view)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel after switch back = %q, want %q", got, want)
	}
	if got, want := model.activeChannelState().composerText, "alpha draft"; got != want {
		t.Fatalf("restored alpha draft = %q, want %q", got, want)
	}
}

func TestLiveShellSelectsReplyContextAndEscCancelsWithoutLosingDraft(t *testing.T) {
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.activeChannelState().messages = []twitch.ChatMessage{
		{
			ID:          "",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
			DisplayName: "system",
			Text:        "no ID",
			Type:        twitch.MessageTypeNotice,
		},
		{
			ID:          "parent-1",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
			DisplayName: "viewer",
			Text:        "question for chat",
			Type:        twitch.MessageTypeChat,
		},
	}
	model.activeChannelState().composerText = "draft reply"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("select reply returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "parent-1" {
		t.Fatalf("replyTo = %#v, want parent-1 context", model.activeChannelState().replyTo)
	}
	for _, want := range []string{"Replying to viewer", "question for chat"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("reply context view missing %q:\n%s", want, model.View())
		}
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("esc returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().replyTo != nil {
		t.Fatalf("replyTo after esc = %#v, want nil", model.activeChannelState().replyTo)
	}
	if got, want := model.activeChannelState().composerText, "draft reply"; got != want {
		t.Fatalf("composerText after esc = %q, want %q", got, want)
	}
	if strings.Contains(model.View(), "Replying to viewer") {
		t.Fatalf("reply context still visible after esc:\n%s", model.View())
	}
}

func TestLiveShellRStartsReplyModeAndReplySendUsesParentID(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.activeChannelState().messages = []twitch.ChatMessage{
		{
			ID:          "parent-1",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
			DisplayName: "older",
			Text:        "older message",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "parent-2",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 2, 0, time.UTC),
			DisplayName: "latest",
			Text:        "latest message",
			Type:        twitch.MessageTypeChat,
		},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("r returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "parent-2" || model.focus != mockFocusComposer {
		t.Fatalf("reply mode = replyTo %#v focus %v, want latest parent and composer focus", model.activeChannelState().replyTo, model.focus)
	}

	model.activeChannelState().composerText = " thanks "
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("reply Enter returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	sent := client.SentRequests()
	if len(sent) != 1 {
		t.Fatalf("SentRequests length = %d, want 1", len(sent))
	}
	if got, want := sent[0].ReplyToMessageID, "parent-2"; got != want {
		t.Fatalf("ReplyToMessageID = %q, want %q", got, want)
	}
	if got, want := sent[0].Text, "thanks"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
	if sent[0].Action {
		t.Fatalf("Action = true, want false for normal reply")
	}
	if model.activeChannelState().replyTo != nil {
		t.Fatalf("replyTo after successful reply send = %#v, want nil", model.activeChannelState().replyTo)
	}
}

func TestLiveShellMeInputQueuesActionSend(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = " /me waves at chat "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("/me Enter returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	sent := client.SentRequests()
	if len(sent) != 1 {
		t.Fatalf("SentRequests length = %d, want 1", len(sent))
	}
	if got, want := sent[0].Text, "waves at chat"; got != want {
		t.Fatalf("Text = %q, want stripped action body %q", got, want)
	}
	if !sent[0].Action {
		t.Fatal("Action = false, want true")
	}
}

func TestLiveShellFailedReplyRestoresReplyContext(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().replyTo = &composerReplyContext{MessageID: "parent-1", Author: "viewer", Text: "original"}
	model.activeChannelState().composerText = "reply body"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.activeChannelState().composerText, "reply body"; got != want {
		t.Fatalf("composerText after failed reply = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "parent-1" {
		t.Fatalf("replyTo after failed reply = %#v, want parent-1", model.activeChannelState().replyTo)
	}
}

func TestLiveShellFailedMixedQueueDoesNotMisapplyReplyContext(t *testing.T) {
	client := NewFakeChatClient(2)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().replyTo = &composerReplyContext{MessageID: "parent-1", Author: "viewer", Text: "original"}
	model.activeChannelState().composerText = "reply body"

	updated, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if firstCmd == nil {
		t.Fatal("first reply Enter returned nil command, want send command")
	}

	model.activeChannelState().composerText = "plain followup"
	updated, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if secondCmd != nil {
		t.Fatalf("queued follow-up returned command %#v while first send active, want nil", secondCmd)
	}

	completed := firstCmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	for _, want := range []string{"reply body", "plain followup"} {
		if !strings.Contains(model.activeChannelState().composerText, want) {
			t.Fatalf("composerText after failed mixed queue = %q, want it to contain %q", model.activeChannelState().composerText, want)
		}
	}
	if model.activeChannelState().replyTo != nil {
		t.Fatalf("replyTo after failed mixed queue = %#v, want nil to avoid wrong parent", model.activeChannelState().replyTo)
	}
}

func TestMockShellFastModeRevealsIncomingMessage(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	message := mockIncomingMessage("example", "animated-fast", "animated text arrives")

	updated, cmd := model.Update(mockIncomingMessageMsg{message: message})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("incoming fast message returned nil command, want reveal tick")
	}
	if got, want := model.activeChannelState().revealQueue.Len(), 1; got != want {
		t.Fatalf("reveal queue len = %d, want %d", got, want)
	}
	if strings.Contains(model.View(), "animated text arrives") {
		t.Fatalf("incoming message rendered fully before ticks:\n%s", model.View())
	}

	initial := model.View()
	clock.Add(mockRevealDelay)
	updated, _ = model.Update(mockAnimationTickMsg{})
	model = updated.(mockShellModel)
	if got := model.View(); got == initial {
		t.Fatalf("first animation tick did not change view:\n%s", got)
	}

	driveRevealToCompletion(t, &model, clock)
	if got := model.activeChannelState().revealQueue.Len(); got != 0 {
		t.Fatalf("reveal queue len after completion = %d, want 0", got)
	}
	if !strings.Contains(model.View(), "animated text arrives") {
		t.Fatalf("completed reveal missing full message:\n%s", model.View())
	}
}

func TestMockShellOffModeRendersIncomingMessageWithoutRevealTick(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	message := mockIncomingMessage("example", "animated-off", "off mode is immediate")

	updated, cmd := model.Update(mockIncomingMessageMsg{message: message})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("off mode incoming message returned command %#v, want nil reveal tick", cmd)
	}
	if got := model.activeChannelState().revealQueue.Len(); got != 0 {
		t.Fatalf("off mode reveal queue len = %d, want 0", got)
	}
	if !strings.Contains(model.View(), "off mode is immediate") {
		t.Fatalf("off mode view missing full message:\n%s", model.View())
	}
}

func TestMockShellReducedModeUsesFewerChangedFramesThanFastMode(t *testing.T) {
	text := "same animated message takes fewer visible frames in reduced mode"
	fastFrames := changedRevealFrames(t, "fast", text)
	reducedFrames := changedRevealFrames(t, "reduced", text)

	if reducedFrames >= fastFrames {
		t.Fatalf("reduced changed frames = %d, want fewer than fast frames %d", reducedFrames, fastFrames)
	}
}

func TestMockShellInputAndScrollRemainResponsiveDuringAnimation(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.activeChannelState().messages = numberedMockMessages("example", 12)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)

	updated, _ = model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "active-reveal", "animation keeps running"),
	})
	model = updated.(mockShellModel)
	if got := model.activeChannelState().revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len = %d, want 1", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("page up during animation left scrollOffset at 0")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o', 'k'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("composer input during animation returned command %#v, want nil", cmd)
	}
	if got, want := model.activeChannelState().composerText, "ok"; got != want {
		t.Fatalf("composer text during animation = %q, want %q", got, want)
	}
	if got := model.activeChannelState().revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len after input/scroll = %d, want 1", got)
	}
}

func TestLiveShellBurstKeepsRevealQueueBoundedAndControlsResponsive(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: clock.Now(), Detail: "accepted during burst"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, clock)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	for i := 0; i < 40; i++ {
		updated, _ = model.Update(chatClientMessageMsg{
			message: mockIncomingMessage("example", fmt.Sprintf("burst-%02d", i), fmt.Sprintf("burst message %02d", i)),
			ok:      true,
		})
		model = updated.(mockShellModel)
		if model.activeChannelState().revealQueue.Len() > animation.DefaultConfig().MaxQueued {
			t.Fatalf("after burst message %02d reveal queue len = %d, want <= %d", i, model.activeChannelState().revealQueue.Len(), animation.DefaultConfig().MaxQueued)
		}
	}

	if got, want := model.activeChannelState().revealQueue.Len(), animation.DefaultConfig().MaxQueued; got != want {
		t.Fatalf("reveal queue len after burst = %d, want %d", got, want)
	}
	if got, want := len(model.activeChannelState().messages), 40-animation.DefaultConfig().MaxQueued; got != want {
		t.Fatalf("static overflow messages = %d, want %d", got, want)
	}
	for _, want := range []string{"burst message 00", "burst message 07"} {
		if !messagesContainText(model.activeChannelState().messages, want) {
			t.Fatalf("overflowed static messages missing %q: %#v", want, model.activeChannelState().messages)
		}
	}
	if messagesContainText(model.activeChannelState().messages, "burst message 08") {
		t.Fatalf("non-overflowed burst message rendered statically too early: %#v", model.activeChannelState().messages)
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 36, Height: 9})
	model = updated.(mockShellModel)
	view := model.View()
	if got, want := lineCount(view), 9; got != want {
		t.Fatalf("burst resized view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipglossWidth(line); got > 36 {
			t.Fatalf("burst resized line %d width = %d, want <= 36:\n%s", i+1, got, view)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("page up during burst left scrollOffset at 0")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("still responsive")})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("composer input during burst returned command %#v, want nil", cmd)
	}
	if got, want := model.activeChannelState().composerText, "still responsive"; got != want {
		t.Fatalf("composer text during burst = %q, want %q", got, want)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("Enter during burst returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)
	if got, want := model.activeChannelState().sendState, composerSendSucceeded; got != want {
		t.Fatalf("sendState after burst send = %q, want %q", got, want)
	}
	if !strings.Contains(model.activeChannelState().sendFeedback, "accepted during burst") {
		t.Fatalf("sendFeedback after burst = %q, want accepted detail", model.activeChannelState().sendFeedback)
	}
}

func TestLiveShellBatchesVisibleAvatarLookups(t *testing.T) {
	client := NewFakeChatClient(1)
	resolver := &appFakeAvatarResolver{
		results: []assets.AvatarResult{
			{UserID: "42", UserLogin: "viewer", AvatarURL: "https://static-cdn.example/viewer.png", Found: true},
			{UserID: "99", UserLogin: "mod", AvatarURL: "https://static-cdn.example/mod.png", Found: true},
		},
	}
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	model := newLiveShellModelWithClockAndOptions("example", cfg, client, nil, ClientOptions{AvatarResolver: resolver})
	model.activeChannelState().messages = []twitch.ChatMessage{
		{ID: "m1", AuthorID: "42", AuthorLogin: "viewer", DisplayName: "Viewer", Text: "first", Type: twitch.MessageTypeChat},
		{ID: "m2", AuthorID: "42", AuthorLogin: "viewer", DisplayName: "Viewer", Text: "second", Type: twitch.MessageTypeChat},
		{ID: "m3", AuthorID: "99", AuthorLogin: "mod", DisplayName: "Mod", Text: "third", Type: twitch.MessageTypeChat},
	}

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("WindowSize returned nil command, want debounced avatar lookup")
	}
	if !model.avatarLookupScheduled {
		t.Fatal("avatarLookupScheduled = false, want true")
	}

	updated, _ = model.Update(chatClientMessageMsg{
		message: twitch.ChatMessage{ID: "m4", AuthorID: "99", AuthorLogin: "mod", DisplayName: "Mod", Text: "fourth", Type: twitch.MessageTypeChat},
		ok:      true,
	})
	model = updated.(mockShellModel)

	updated, cmd = model.Update(avatarLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("avatarLookupTick returned nil command, want resolver command")
	}
	resolved := cmd().(avatarLookupResolvedMsg)
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if got, want := len(resolver.last), 2; got != want {
		t.Fatalf("batched request count = %d, want %d: %#v", got, want, resolver.last)
	}

	updated, _ = model.Update(resolved)
	model = updated.(mockShellModel)
	for _, message := range model.activeChannelState().messages {
		if message.AuthorID != "42" && message.AuthorID != "99" {
			continue
		}
		if message.AvatarURL == "" {
			t.Fatalf("message missing resolved AvatarURL: %#v", message)
		}
	}
}

func TestLiveShellAvatarResolutionKeepsFallbackRowsStable(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AvatarResolver: &appFakeAvatarResolver{},
	})
	message := twitch.ChatMessage{
		ID:          "m1",
		AuthorID:    "42",
		AuthorLogin: "viewer",
		DisplayName: "Viewer",
		Text:        "avatar metadata arrives later",
		Type:        twitch.MessageTypeChat,
	}
	beforeRows := renderRowsToPlain(render.Rows(message, model.renderOptions(80)))
	model.activeChannelState().messages = []twitch.ChatMessage{message}
	model.applyAvatarResults([]assets.AvatarResult{{
		UserID:    "42",
		UserLogin: "viewer",
		AvatarURL: "https://static-cdn.example/viewer.png",
		Found:     true,
	}})
	afterRows := renderRowsToPlain(render.Rows(model.activeChannelState().messages[0], model.renderOptions(80)))

	if !reflect.DeepEqual(afterRows, beforeRows) {
		t.Fatalf("fallback rows changed after avatar resolution\nbefore: %#v\nafter:  %#v", beforeRows, afterRows)
	}
	fragment, ok := firstRenderKind(render.Rows(model.activeChannelState().messages[0], model.renderOptions(80)), render.FragmentAvatar)
	if !ok {
		t.Fatal("avatar fragment missing after resolution")
	}
	if fragment.Ref.URL != "https://static-cdn.example/viewer.png" {
		t.Fatalf("avatar ref URL = %q, want resolved URL", fragment.Ref.URL)
	}
}

func TestLiveShellAssetEventsRefreshVisibleRows(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindAvatar, ID: "42"}:         "[A42]",
		{Kind: assets.KindBadge, ID: "moderator/1"}: "BMOD  ",
		{Kind: assets.KindTwitchEmote, ID: "25"}:    "EM25  ",
		{Kind: assets.KindEmoji, ID: "1f600"}:       ":)",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: renderer,
	})
	model.activeChannelState().messages = []twitch.ChatMessage{assetEventMessage("visible-assets", "25", "😀")}

	before := model.View()
	for _, notWant := range []string{"[A42]", "BMOD", "EM25", ":)"} {
		if strings.Contains(before, notWant) {
			t.Fatalf("view already contains prepared cell %q before asset events:\n%s", notWant, before)
		}
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	batch, ok := cmd().(assetPreparedBatchMsg)
	if !ok {
		t.Fatalf("asset resolver command returned %T, want assetPreparedBatchMsg", cmd())
	}
	if got, want := requestKinds(resolver.last), []string{assets.KindAvatar, assets.KindBadge, assets.KindTwitchEmote, assets.KindEmoji}; !reflect.DeepEqual(got, want) {
		t.Fatalf("asset request kinds = %#v, want %#v; requests=%#v", got, want, resolver.last)
	}
	if renderer.calls != 4 {
		t.Fatalf("image renderer calls = %d, want 4", renderer.calls)
	}

	updated, _ = model.Update(batch)
	model = updated.(mockShellModel)
	after := model.View()
	for _, want := range []string{"[A42]", "BMOD", "EM25", ":)"} {
		if !strings.Contains(after, want) {
			t.Fatalf("view missing prepared asset cell %q after event:\n%s", want, after)
		}
	}
}

func TestLiveShellAssetEventsPreserveViewportReplyAndComposer(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: &appFakeAssetResolver{},
		ImageRenderer: &appFakeImageRenderer{},
	})
	model.activeChannelState().messages = numberedMockMessages("example", 40)
	model.activeChannelState().messages = append(model.activeChannelState().messages, assetEventMessage("asset-target", "25", "😀"))

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "draft reply"
	model.activeChannelState().replyTo = &composerReplyContext{MessageID: "mock-35", Author: "viewer", Text: "message-35"}
	beforeOffset := model.activeChannelState().scrollOffset
	beforeReply := *model.activeChannelState().replyTo
	beforeView := model.View()

	updated, _ = model.Update(assetPreparedBatchMsg{results: []assetPreparedMsg{
		preparedAssetForTest(twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}, "EM25  ", 6),
		preparedAssetForTest(twitch.AssetRef{Kind: assets.KindEmoji, ID: "1f600"}, ":)", 2),
	}})
	model = updated.(mockShellModel)

	if got := model.activeChannelState().scrollOffset; got != beforeOffset {
		t.Fatalf("scrollOffset after asset update = %d, want %d", got, beforeOffset)
	}
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after asset update = %v, want %v", got, want)
	}
	if got, want := model.activeChannelState().composerText, "draft reply"; got != want {
		t.Fatalf("composerText after asset update = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after asset update = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}
	if after := model.View(); after != beforeView {
		t.Fatalf("off-screen asset update changed scrolled viewport:\nbefore:\n%s\nafter:\n%s", beforeView, after)
	}
}

func TestLiveShellAssetResolverOnlyRequestsVisibleHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: &appFakeImageRenderer{},
	})
	for i := 0; i < 80; i++ {
		model.activeChannelState().messages = append(model.activeChannelState().messages, assetEventMessage(fmt.Sprintf("hidden-%02d", i), fmt.Sprintf("hidden-%02d", i), "😀"))
	}
	for i := 0; i < 20; i++ {
		model.activeChannelState().messages = append(model.activeChannelState().messages, assetEventMessage(fmt.Sprintf("visible-%02d", i), fmt.Sprintf("visible-%02d", i), "😀"))
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	_, cmd := model.Update(assetLookupTickMsg{})
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	_ = cmd()
	if len(resolver.last) == 0 {
		t.Fatal("resolver received no visible asset requests")
	}
	for _, request := range resolver.last {
		if request.Ref.Kind != assets.KindTwitchEmote {
			continue
		}
		if strings.HasPrefix(request.Ref.ID, "hidden-") {
			t.Fatalf("resolver requested hidden history asset %q; requests=%#v", request.Ref.ID, resolver.last)
		}
	}
}

func TestLiveShellAssetFailureCanRetryVisibleRequest(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{fail: true}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: renderer,
	})
	model.activeChannelState().messages = []twitch.ChatMessage{assetEventMessage("retry-assets", "25", "😀")}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("first assetLookupTick returned nil command, want failing resolver command")
	}
	failedBatch := cmd().(assetPreparedBatchMsg)
	if len(failedBatch.results) == 0 {
		t.Fatal("failing resolver produced no batch results")
	}
	failedID := failedBatch.results[0].event.RequestID
	updated, cmd = model.Update(failedBatch)
	model = updated.(mockShellModel)
	if model.assetRequested[failedID] {
		t.Fatalf("failed asset request %q remained permanently marked requested", failedID)
	}
	if cmd == nil || !model.assetLookupScheduled {
		t.Fatalf("failed visible asset did not schedule retry; scheduled=%v cmd=%#v", model.assetLookupScheduled, cmd)
	}

	resolver.fail = false
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("retry assetLookupTick returned nil command, want resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)
	if view := model.View(); !strings.Contains(view, "EM25") {
		t.Fatalf("view missing prepared cell after retry:\n%s", view)
	}
}

func TestMockShellAssetEventRefreshesActiveRevealRows(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmoteMode = "image"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	model.activeChannelState().messages = nil

	updated, _ := model.Update(mockIncomingMessageMsg{message: activeAssetEventMessage()})
	model = updated.(mockShellModel)
	for i := 0; i < 100 && !strings.Contains(model.View(), "Kappa"); i++ {
		clock.Add(mockRevealDelay)
		updated, _ = model.Update(mockAnimationTickMsg{})
		model = updated.(mockShellModel)
	}
	if got := model.activeChannelState().revealQueue.Len(); got == 0 {
		t.Fatal("active reveal completed before asset refresh test could run")
	}
	if view := model.View(); !strings.Contains(view, "Kappa") {
		t.Fatalf("test setup did not reveal Kappa before asset event:\n%s", view)
	}

	updated, _ = model.Update(assetPreparedBatchMsg{results: []assetPreparedMsg{
		preparedAssetForTest(twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}, "EM25  ", 6),
	}})
	model = updated.(mockShellModel)
	if view := model.View(); !strings.Contains(view, "EM25") {
		t.Fatalf("active reveal view missing prepared emote cell after asset event:\n%s", view)
	}
}

func TestMockShellScrolledBurstRendersStaticallyWithoutRevealBacklog(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.activeChannelState().messages = numberedMockMessages("example", 30)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("test setup failed: scrollOffset = 0 after page up")
	}
	beforeView := model.View()
	beforeOffset := model.activeChannelState().scrollOffset

	for i := 0; i < 12; i++ {
		updated, cmd := model.Update(mockIncomingMessageMsg{
			message: mockIncomingMessage("example", fmt.Sprintf("offscreen-%02d", i), fmt.Sprintf("offscreen burst %02d", i)),
		})
		model = updated.(mockShellModel)
		if cmd != nil {
			t.Fatalf("off-screen burst message %02d returned command %#v, want no reveal tick", i, cmd)
		}
	}

	if got := model.activeChannelState().revealQueue.Len(); got != 0 {
		t.Fatalf("off-screen burst reveal queue len = %d, want 0", got)
	}
	if got := len(model.activeChannelState().activeOrder); got != 0 {
		t.Fatalf("off-screen active reveal count = %d, want 0", got)
	}
	if got, want := len(model.activeChannelState().messages), 42; got != want {
		t.Fatalf("messages after off-screen burst = %d, want %d", got, want)
	}
	if model.activeChannelState().scrollOffset <= beforeOffset {
		t.Fatalf("scrollOffset after off-screen burst = %d, want > %d to preserve visible page", model.activeChannelState().scrollOffset, beforeOffset)
	}
	afterView := model.View()
	if afterView != beforeView {
		t.Fatalf("off-screen static burst changed visible scrolled page:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
	if strings.Contains(afterView, "offscreen burst") {
		t.Fatalf("off-screen burst appeared in current scrolled viewport:\n%s", afterView)
	}
}

func TestMockShellCompletingActiveRevealPreservesScrolledViewport(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.activeChannelState().messages = numberedMockMessages("example", 30)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "active-while-scrolled", "active reveal finishes while scrolled"),
	})
	model = updated.(mockShellModel)
	if got := model.activeChannelState().revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len = %d, want 1", got)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("test setup failed: scrollOffset = 0 after page up")
	}
	beforeView := model.View()
	beforeOffset := model.activeChannelState().scrollOffset

	driveRevealToCompletion(t, &model, clock)

	if got := model.activeChannelState().revealQueue.Len(); got != 0 {
		t.Fatalf("reveal queue len after completion = %d, want 0", got)
	}
	if !messagesContainText(model.activeChannelState().messages, "active reveal finishes while scrolled") {
		t.Fatalf("completed reveal missing from static messages: %#v", model.activeChannelState().messages)
	}
	if got := model.activeChannelState().scrollOffset; got != beforeOffset {
		t.Fatalf("scrollOffset after active completion = %d, want %d", got, beforeOffset)
	}
	if afterView := model.View(); afterView != beforeView {
		t.Fatalf("active reveal completion changed visible scrolled page:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
}

func TestMockShellDuplicateIncomingMessageIDsCompleteIndependently(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)

	for _, text := range []string{"first duplicate", "second duplicate"} {
		updated, _ := model.Update(mockIncomingMessageMsg{
			message: mockIncomingMessage("example", "duplicate-id", text),
		})
		model = updated.(mockShellModel)
	}
	if got := model.activeChannelState().revealQueue.Len(); got != 2 {
		t.Fatalf("reveal queue len = %d, want 2", got)
	}

	driveRevealToCompletion(t, &model, clock)
	view := model.View()
	for _, want := range []string{"first duplicate", "second duplicate"} {
		if !strings.Contains(view, want) {
			t.Fatalf("completed duplicate-ID reveal missing %q:\n%s", want, view)
		}
	}
}

func TestMockShellResizeDuringAnimationStaysWithinBounds(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	updated, _ := model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "resize-active", "active reveal survives a narrow resize without overflowing"),
	})
	model = updated.(mockShellModel)

	clock.Add(mockRevealDelay)
	updated, _ = model.Update(mockAnimationTickMsg{})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	view := updated.View()

	if got, want := lineCount(view), 8; got != want {
		t.Fatalf("resized active view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipglossWidth(line); got > 24 {
			t.Fatalf("line %d width = %d, want <= 24:\n%s", i+1, got, view)
		}
	}
}

func TestMockShellNarrowLayoutStaysWithinBounds(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().composerText = "hello 😀 表"
	model.activeChannelState().messages = append(model.activeChannelState().messages, twitch.ChatMessage{
		ID:          "wide",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "wide",
		DisplayName: "wide",
		Text:        "emoji 😀 and CJK 表 stay inside",
		Type:        twitch.MessageTypeChat,
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	view := updated.View()

	if got, want := lineCount(view), 8; got != want {
		t.Fatalf("narrow view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got, want := lipglossWidth(line), 24; got > want {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, want, view)
		}
	}
	for _, notWant := range []string{"animation=", "images=", "mock source ready"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("narrow view contains nonessential status text %q:\n%s", notWant, view)
		}
	}
	for _, want := range []string{"#example connected", "hello"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
}

func TestMockShellChannelSidebarResponsiveLayouts(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta", "gamma"}
	model := newMockShellModel("alpha", cfg)
	model.channels.ensure("alpha").status = ConnectionState{Status: ConnectionConnected, Channel: "alpha"}
	model.channels.ensure("beta").status = ConnectionState{Status: ConnectionDisconnected, Channel: "beta"}
	model.channels.ensure("gamma").status = ConnectionState{Status: ConnectionReconnecting, Channel: "gamma"}
	model.channels.ensure("beta").unread = 3
	model.channels.ensure("gamma").unread = 1

	for _, tt := range []struct {
		name         string
		width        int
		height       int
		wantSidebar  bool
		wantWideSize bool
	}{
		{name: "narrow", width: 48, height: 10},
		{name: "normal", width: 88, height: 12, wantSidebar: true},
		{name: "wide", width: 124, height: 14, wantSidebar: true, wantWideSize: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updated, _ := model.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			rendered := updated.(mockShellModel)
			layout := rendered.layout()
			view := rendered.View()

			if got, want := lineCount(view), tt.height; got != want {
				t.Fatalf("line count = %d, want %d:\n%s", got, want, view)
			}
			for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
				if got := lipglossWidth(line); got > tt.width {
					t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, tt.width, view)
				}
			}
			if (layout.sidebarWidth > 0) != tt.wantSidebar {
				t.Fatalf("sidebarWidth = %d, want sidebar visible %v:\n%s", layout.sidebarWidth, tt.wantSidebar, view)
			}
			if tt.wantWideSize && layout.sidebarWidth != sidebarWideSize {
				t.Fatalf("wide sidebar width = %d, want %d", layout.sidebarWidth, sidebarWideSize)
			}
			if !tt.wantSidebar {
				if strings.Contains(view, "Channels") {
					t.Fatalf("narrow view rendered full sidebar:\n%s", view)
				}
				for _, want := range []string{"#alpha connected", "channels=3", "unread=4"} {
					if !strings.Contains(view, want) {
						t.Fatalf("narrow view missing collapsed channel state %q:\n%s", want, view)
					}
				}
				return
			}
			for _, want := range []string{"Channels", "> * #alpha", "! #beta 3", "~ #gamma 1"} {
				if !strings.Contains(view, want) {
					t.Fatalf("%s sidebar view missing %q:\n%s", tt.name, want, view)
				}
			}
		})
	}
}

func TestMockShellTinyWidthsDoNotExceedWindowWidth(t *testing.T) {
	for width := 1; width <= 5; width++ {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			model := newMockShellModel("example", config.Default())
			model.activeChannelState().composerText = "😀表"

			updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 8})
			view := updated.View()

			if got, want := lineCount(view), 8; got != want {
				t.Fatalf("tiny view line count = %d, want %d:\n%s", got, want, view)
			}
			for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
				if got := lipglossWidth(line); got > width {
					t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, width, view)
				}
			}
		})
	}
}

func mockIncomingMessage(channel, id, text string) twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:          id,
		Channel:     channel,
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Text:        text,
		Type:        twitch.MessageTypeChat,
	}
}

func assetEventMessage(id, emoteID, emojiText string) twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:          id,
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorID:    "42",
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "asset "},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: emoteID}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: emojiText},
		},
	}
}

func activeAssetEventMessage() twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:          "active-asset",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}},
			{Type: twitch.FragmentText, Text: strings.Repeat(" trailing text", 20)},
		},
	}
}

func changedRevealFrames(t *testing.T, mode, text string) int {
	t.Helper()

	cfg := config.Default()
	cfg.Features.AnimationMode = mode
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	updated, _ := model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "animated-"+mode, text),
	})
	model = updated.(mockShellModel)

	changed := 0
	for model.activeChannelState().revealQueue.Len() > 0 {
		before := model.View()
		clock.Add(mockRevealDelay)
		updated, _ = model.Update(mockAnimationTickMsg{})
		model = updated.(mockShellModel)
		if after := model.View(); after != before {
			changed++
		}
		if changed > 1000 {
			t.Fatal("reveal did not complete")
		}
	}
	return changed
}

func driveRevealToCompletion(t *testing.T, model *mockShellModel, clock *appFakeClock) {
	t.Helper()

	for i := 0; model.activeChannelState().revealQueue.Len() > 0; i++ {
		if i > 1000 {
			t.Fatal("reveal did not complete")
		}
		clock.Add(mockRevealDelay)
		updated, _ := model.Update(mockAnimationTickMsg{})
		*model = updated.(mockShellModel)
	}
}

func lineCount(value string) int {
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func numberedMockMessages(channel string, count int) []twitch.ChatMessage {
	startedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	messages := make([]twitch.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, twitch.ChatMessage{
			ID:          fmt.Sprintf("mock-%02d", i),
			Channel:     channel,
			Timestamp:   startedAt.Add(time.Duration(i) * time.Second),
			AuthorLogin: "viewer",
			DisplayName: "viewer",
			Text:        fmt.Sprintf("message-%02d", i),
			Type:        twitch.MessageTypeChat,
		})
	}
	return messages
}

func messagesContainText(messages []twitch.ChatMessage, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Text, text) {
			return true
		}
	}
	return false
}

func firstRenderKind(rows []render.Row, kind render.FragmentKind) (render.Fragment, bool) {
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind == kind {
				return fragment, true
			}
		}
	}
	return render.Fragment{}, false
}

func renderRowsToPlain(rows []render.Row) []string {
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain())
	}
	return plain
}

type appFakeAvatarResolver struct {
	calls   int
	last    []assets.AvatarRequest
	results []assets.AvatarResult
	err     error
}

func (f *appFakeAvatarResolver) ResolveAvatars(_ context.Context, requests []assets.AvatarRequest) ([]assets.AvatarResult, error) {
	f.calls++
	f.last = append([]assets.AvatarRequest(nil), requests...)
	return f.results, f.err
}

type appFakeAssetResolver struct {
	calls int
	last  []assets.Request
	fail  bool
}

func (f *appFakeAssetResolver) Resolve(_ context.Context, request assets.Request) assets.Event {
	f.calls++
	f.last = append(f.last, request)
	if f.fail {
		return assets.Event{
			Kind:      assets.EventFailed,
			RequestID: request.ID,
			Ref:       request.Ref,
			At:        time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC),
		}
	}
	return assets.Event{
		Kind:      assets.EventDownloaded,
		RequestID: request.ID,
		Ref:       request.Ref,
		Record: storage.AssetRecord{
			Key:         storage.AssetKey{Kind: request.Ref.Kind, ID: request.Ref.ID},
			Path:        "fake.png",
			MediaType:   "image/png",
			WidthCells:  request.WidthCells,
			HeightCells: request.HeightCells,
		},
		At: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC),
	}
}

type appFakeImageRenderer struct {
	calls int
	cells map[render.ImageCellKey]string
}

func (f *appFakeImageRenderer) RenderImage(_ context.Context, _ storage.AssetRecord, spec render.ImageSpec) (render.ImageCell, error) {
	f.calls++
	key, _ := render.ImageCellKeyForRef(spec.Ref)
	text := f.cells[key]
	if text == "" {
		text = spec.Fallback
	}
	width := spec.WidthCells
	if width <= 0 {
		width = lipglossWidth(spec.Fallback)
	}
	return render.ImageCell{
		Text:       fitLine(text, width),
		WidthCells: width,
	}, nil
}

func preparedAssetForTest(ref twitch.AssetRef, text string, width int) assetPreparedMsg {
	return assetPreparedMsg{
		event: assets.Event{
			Kind: assets.EventDownloaded,
			Ref:  ref,
			Record: storage.AssetRecord{
				Key:        storage.AssetKey{Kind: ref.Kind, ID: ref.ID},
				MediaType:  "image/png",
				WidthCells: width,
			},
		},
		cell: render.ImageCell{Text: fitLine(text, width), WidthCells: width},
	}
}

func requestKinds(requests []assets.Request) []string {
	seen := make(map[string]bool)
	kinds := make([]string, 0, len(requests))
	for _, request := range requests {
		if seen[request.Ref.Kind] {
			continue
		}
		seen[request.Ref.Kind] = true
		kinds = append(kinds, request.Ref.Kind)
	}
	return kinds
}

func lipglossWidth(value string) int {
	return lipgloss.Width(value)
}
