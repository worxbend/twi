package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

type appFakeClock struct {
	now time.Time
}

type appFakeSystemNotifier struct {
	notifications []SystemNotification
	err           error
}

func (n *appFakeSystemNotifier) Notify(ctx context.Context, notification SystemNotification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.notifications = append(n.notifications, notification)
	return n.err
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
			wantRows:   []string{"[VF] 20:00 [moderator] viewer_fan: Kappa 😀"},
			wantEmoteW: 5,
		},
		{
			name: "image off keeps text tokens",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			wantRows:   []string{"[VF] 20:00 [moderator] viewer_fan: Kappa 😀"},
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
	if got, want := model.focus, mockFocusEmotes; got != want {
		t.Fatalf("focus after second tab = %v, want %v", got, want)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after third tab = %v, want %v", got, want)
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q from chat focus returned nil command, want tea.Quit")
	}
}

func TestMockShellPageKeysScrollViewport(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = numberedMockMessages("example", 12)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 16})
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

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 88, Height: 16})
	model = updated.(mockShellModel)
	layout := model.layout()
	chatX := layout.sidebarWidth + 2
	contentY := layout.tabBarHeight + layout.statusHeight + 1

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

	betaY := layout.tabBarHeight + layout.statusHeight + 1 + 2
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

	composerY := layout.tabBarHeight + layout.statusHeight + layout.chatHeight + 1
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
	latestY := layout.tabBarHeight + layout.statusHeight + 1 + layout.chatContentHeight - 1
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
		{X: layout.sidebarWidth + 2, Y: layout.tabBarHeight + layout.statusHeight + 1, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress},
		{X: 2, Y: layout.tabBarHeight + layout.statusHeight + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 10, Y: layout.tabBarHeight + layout.statusHeight + layout.chatHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: layout.sidebarWidth + 4, Y: layout.tabBarHeight + layout.statusHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
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
	if err := client.QueueSendResult(SendResult{MessageID: "sent-1", AcceptedAt: acceptedAt, Detail: "accepted by Twitch"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	cfg := config.Default()
	cfg.Twitch.Username = "self_user"
	model := newLiveShellModelWithClock("example", cfg, client, nil)
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
	messages := model.activeChannelState().messages
	if len(messages) != 1 {
		t.Fatalf("messages after success = %d, want local sent message: %#v", len(messages), messages)
	}
	local := messages[0]
	if local.ID != "sent-1" || local.Channel != "example" || local.Text != "hello chat" || local.DisplayName != "self_user" || local.Type != twitch.MessageTypeChat {
		t.Fatalf("local sent message = %#v, want self-authored chat row", local)
	}
	if !local.Timestamp.Equal(acceptedAt) {
		t.Fatalf("local sent timestamp = %v, want %v", local.Timestamp, acceptedAt)
	}
	sent := client.SentRequests()
	if len(sent) != 1 || sent[0].Channel != "example" || sent[0].Text != "hello chat" {
		t.Fatalf("SentRequests = %#v, want one trimmed send to active channel", sent)
	}
	view := model.View()
	for _, want := range []string{"accepted by Twitch", "hello chat", "self_user"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q after successful send:\n%s", want, view)
		}
	}
}

func TestLiveShellIncomingEchoReplacesLocalSentMessage(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Twitch.Username = "self_user"
	client := NewFakeChatClient(1)
	acceptedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := client.QueueSendResult(SendResult{MessageID: "sent-echo", AcceptedAt: acceptedAt}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", cfg, client, nil)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "hello chat"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	updated, _ = model.Update(cmd().(composerSendCompletedMsg))
	model = updated.(mockShellModel)
	if got := len(model.activeChannelState().messages); got != 1 {
		t.Fatalf("messages after local echo = %d, want 1", got)
	}

	serverEcho := twitch.ChatMessage{
		ID:          "sent-echo",
		Channel:     "example",
		Timestamp:   acceptedAt.Add(time.Second),
		AuthorLogin: "self_user",
		DisplayName: "Self User",
		Text:        "hello chat from Twitch",
		Type:        twitch.MessageTypeChat,
	}
	updated, _ = model.Update(chatClientMessageMsg{message: serverEcho, ok: true})
	model = updated.(mockShellModel)

	messages := model.activeChannelState().messages
	if len(messages) != 1 {
		t.Fatalf("messages after server echo = %d, want replacement only: %#v", len(messages), messages)
	}
	if messages[0].ID != "sent-echo" || messages[0].Text != "hello chat from Twitch" || messages[0].DisplayName != "Self User" {
		t.Fatalf("message after server echo = %#v, want authoritative Twitch row", messages[0])
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
	model.height = 15

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

func TestLiveShellNotifiesSystemEventWhenComposerFocused(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	notifier := &appFakeSystemNotifier{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, nil, nil, ClientOptions{SystemNotifier: notifier})
	model.width = 120
	model.focus = mockFocusComposer

	updated, cmd := model.Update(chatClientMessageMsg{message: raidSystemEventMessage("example"), ok: true})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("composer-focused live system event returned nil command, want notification command")
	}
	cmd()
	if got, want := len(notifier.notifications), 1; got != want {
		t.Fatalf("notifications = %d, want %d", got, want)
	}
	if got, want := notifier.notifications[0].EventID, "raid"; got != want {
		t.Fatalf("notification event = %q, want %q", got, want)
	}
	if model.lastSystemNotification == nil || model.lastSystemNotification.Title != "Raid in #example" {
		t.Fatalf("last notification = %#v, want raid summary", model.lastSystemNotification)
	}
	if !strings.Contains(model.View(), "notify: Raid in #example") {
		t.Fatalf("view missing notification summary:\n%s", model.View())
	}
}

func TestLiveShellNotifiesSystemEventWhenTerminalBlurred(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	notifier := &appFakeSystemNotifier{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, nil, nil, ClientOptions{SystemNotifier: notifier})
	model.focus = mockFocusChat

	updated, _ := model.Update(tea.BlurMsg{})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(mockIncomingMessageMsg{message: raidSystemEventMessage("example")})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("blurred system event returned nil command, want notification command")
	}
	cmd()
	if got, want := len(notifier.notifications), 1; got != want {
		t.Fatalf("notifications = %d, want %d", got, want)
	}

	updated, _ = model.Update(tea.FocusMsg{})
	model = updated.(mockShellModel)
	if !model.terminalFocused {
		t.Fatal("terminalFocused = false after FocusMsg, want true")
	}
}

func TestLiveShellNotifiesSystemEventForInactiveChannel(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	notifier := &appFakeSystemNotifier{}
	model := newLiveShellModelWithClockAndOptions("alpha", cfg, nil, nil, ClientOptions{SystemNotifier: notifier})
	model.focus = mockFocusChat

	updated, cmd := model.Update(mockIncomingMessageMsg{message: raidSystemEventMessage("beta")})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("inactive-channel system event returned nil command, want notification command")
	}
	cmd()
	if got, want := len(notifier.notifications), 1; got != want {
		t.Fatalf("notifications = %d, want %d", got, want)
	}
	if got, want := notifier.notifications[0].Channel, "beta"; got != want {
		t.Fatalf("notification channel = %q, want %q", got, want)
	}
	if got, want := model.channels.ensure("beta").unread, 1; got != want {
		t.Fatalf("beta unread = %d, want %d", got, want)
	}
}

func TestLiveShellDoesNotNotifyActiveSystemEventWhenChatFocused(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	notifier := &appFakeSystemNotifier{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, nil, nil, ClientOptions{SystemNotifier: notifier})
	model.focus = mockFocusChat

	updated, cmd := model.Update(mockIncomingMessageMsg{message: raidSystemEventMessage("example")})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("active focused system event returned command %#v, want nil", cmd)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %#v, want none", notifier.notifications)
	}
	if model.lastSystemNotification != nil {
		t.Fatalf("last notification = %#v, want nil", model.lastSystemNotification)
	}
}

func TestLiveShellDoesNotNotifyPlainNoticeWhenComposerFocused(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	notifier := &appFakeSystemNotifier{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, nil, nil, ClientOptions{SystemNotifier: notifier})
	model.focus = mockFocusComposer

	updated, cmd := model.Update(mockIncomingMessageMsg{message: twitch.ChatMessage{
		ID:      "notice-1",
		Channel: "example",
		Text:    "routine Twitch notice",
		Type:    twitch.MessageTypeNotice,
	}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("plain notice returned command %#v, want nil", cmd)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %#v, want none", notifier.notifications)
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
	messages := model.activeChannelState().messages
	if len(messages) != 3 {
		t.Fatalf("messages after reply send = %d, want parents plus local reply: %#v", len(messages), messages)
	}
	local := messages[2]
	if local.Text != "thanks" || local.Reply == nil || local.Reply.ParentMessageID != "parent-2" {
		t.Fatalf("local reply message = %#v, want reply row targeting parent-2", local)
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
	messages := model.activeChannelState().messages
	if len(messages) != 1 {
		t.Fatalf("messages after action send = %d, want local action row: %#v", len(messages), messages)
	}
	if messages[0].Text != "waves at chat" || messages[0].Type != twitch.MessageTypeAction {
		t.Fatalf("local action message = %#v, want action row", messages[0])
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

func TestLiveShellHighThroughputChatStressHarness(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: clock.Now(), Detail: "accepted during burst"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, clock)
	if got, want := model.imageCapability.Status, render.ImageCapabilityUnsupported; got != want {
		t.Fatalf("image capability status = %q, want %q for credential-free fallback stress", got, want)
	}
	if model.avatarResolver != nil || model.assetResolver != nil || model.imageRenderer != nil {
		t.Fatalf("stress model has image/network services installed: avatar=%T asset=%T renderer=%T", model.avatarResolver, model.assetResolver, model.imageRenderer)
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	maxQueued := animation.DefaultConfig().MaxQueued
	burstSize := maxQueued + 8
	burst := highThroughputBurstMessages("example", burstSize, clock.Now())
	assertHighThroughputFallbackRendering(t, model, burst)
	for i := 0; i < burstSize; i++ {
		updated, _ = model.Update(chatClientMessageMsg{
			message: burst[i],
			ok:      true,
		})
		model = updated.(mockShellModel)
		if model.activeChannelState().revealQueue.Len() > maxQueued {
			t.Fatalf("after burst message %02d reveal queue len = %d, want <= %d", i, model.activeChannelState().revealQueue.Len(), maxQueued)
		}
	}

	if got, want := model.activeChannelState().revealQueue.Len(), maxQueued; got != want {
		t.Fatalf("reveal queue len after burst = %d, want %d", got, want)
	}
	if got, want := len(model.activeChannelState().messages), burstSize-maxQueued; got != want {
		t.Fatalf("static overflow messages = %d, want %d", got, want)
	}
	for _, want := range []string{"burst message 00 fallback", "burst message 07 unicode"} {
		if !messagesContainText(model.activeChannelState().messages, want) {
			t.Fatalf("overflowed static messages missing %q: %#v", want, model.activeChannelState().messages)
		}
	}
	if messagesContainText(model.activeChannelState().messages, "burst message 08 fallback") {
		t.Fatalf("non-overflowed burst message rendered statically too early: %#v", model.activeChannelState().messages)
	}
	if got, want := model.activeChannelState().revealQueue.OverflowCount(), burstSize-maxQueued; got != want {
		t.Fatalf("reveal overflow count = %d, want %d", got, want)
	}
	if got, want := len(model.activeChannelState().activeOrder), maxQueued; got != want {
		t.Fatalf("active reveal order length = %d, want %d", got, want)
	}
	if got := len(model.imageCells); got != 0 {
		t.Fatalf("image cells after fallback stress = %d, want 0", got)
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

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
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

func TestLiveShellAssetRequestPreservesFragmentEmoteURL(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	ref := twitch.AssetRef{
		Kind: assets.KindTwitchEmote,
		URL:  "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0#e=0",
	}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: &appFakeAssetResolver{},
		ImageRenderer: &appFakeImageRenderer{},
		AssetKinds:    map[string]bool{assets.KindTwitchEmote: true},
	})
	model.activeChannelState().messages = []twitch.ChatMessage{{
		ID:          "direct-emote-url",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "asset "},
			{Type: twitch.FragmentEmote, Text: "WutFace", Ref: ref},
		},
	}}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)

	requests := model.pendingAssetRequests()
	if len(requests) != 1 {
		t.Fatalf("pending asset requests = %#v, want one emote request", requests)
	}
	wantRef := ref
	wantRef.ID = "emotesv2_299397e0339249f8a1b50f0affb044d8"
	if got := requests[0].Ref; got != wantRef {
		t.Fatalf("asset request ref = %#v, want enriched fragment ref %#v", got, wantRef)
	}
	if got, want := requests[0].Fallback, "WutFace"; got != want {
		t.Fatalf("asset request fallback = %q, want %q", got, want)
	}
}

func TestLiveShellPreparedImageCellsAreScopedByChannelIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"alpha", "beta"}
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"

	ref := twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}
	alphaKey, ok := render.ImageCellKeyForRefInChannel(ref, "100", "alpha")
	if !ok {
		t.Fatal("alpha image cell key rejected unexpectedly")
	}
	betaKey, ok := render.ImageCellKeyForRefInChannel(ref, "200", "beta")
	if !ok {
		t.Fatal("beta image cell key rejected unexpectedly")
	}
	if alphaKey == betaKey || alphaKey.ChannelIdentity == "" || betaKey.ChannelIdentity == "" {
		t.Fatalf("channel-scoped keys not distinct: alpha=%#v beta=%#v", alphaKey, betaKey)
	}

	resolver := &appFakeAssetResolver{}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		alphaKey: "ALPHA ",
		betaKey:  "BETA  ",
	}}
	model := newLiveShellModelWithClockAndOptions("alpha", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: renderer,
	})

	alphaMessage := assetEventMessage("alpha-asset", "25", "😀")
	alphaMessage.Channel = "alpha"
	alphaMessage.ChannelID = "100"
	alphaMessage.Badges = nil
	betaMessage := assetEventMessage("beta-asset", "25", "😀")
	betaMessage.Channel = "beta"
	betaMessage.ChannelID = "200"
	betaMessage.Badges = nil
	model.channels.ensure("alpha").messages = []twitch.ChatMessage{alphaMessage}
	model.channels.ensure("beta").messages = []twitch.ChatMessage{betaMessage}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("alpha assetLookupTick returned nil command, want resolver command")
	}
	alphaBatch := cmd().(assetPreparedBatchMsg)
	if len(resolver.last) != 1 {
		t.Fatalf("alpha asset requests = %d, want 1: %#v", len(resolver.last), resolver.last)
	}
	if got, want := resolver.last[0].ChannelID, "100"; got != want {
		t.Fatalf("alpha request ChannelID = %q, want %q", got, want)
	}
	if got, want := resolver.last[0].Channel, "alpha"; got != want {
		t.Fatalf("alpha request Channel = %q, want %q", got, want)
	}
	if !strings.Contains(resolver.last[0].ID, "room:100") {
		t.Fatalf("alpha request ID = %q, want room identity", resolver.last[0].ID)
	}
	updated, _ = model.Update(alphaBatch)
	model = updated.(mockShellModel)
	if view := model.View(); !strings.Contains(view, "ALPHA") || strings.Contains(view, "BETA") {
		t.Fatalf("alpha view used wrong prepared cell:\n%s", view)
	}

	if !model.channels.setActive("beta") {
		t.Fatal("failed to switch active channel to beta")
	}
	model.clampScroll()
	if view := chatOnlyView(model); strings.Contains(view, "ALPHA") || strings.Contains(view, "BETA") || !strings.Contains(view, "Kappa") {
		t.Fatalf("beta view reused a prepared cell before beta render; want fallback only:\n%s", view)
	}

	resolver.last = nil
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("beta assetLookupTick returned nil command, want resolver command")
	}
	betaBatch := cmd().(assetPreparedBatchMsg)
	if len(resolver.last) != 1 {
		t.Fatalf("beta asset requests = %d, want 1: %#v", len(resolver.last), resolver.last)
	}
	if got, want := resolver.last[0].ChannelID, "200"; got != want {
		t.Fatalf("beta request ChannelID = %q, want %q", got, want)
	}
	if !strings.Contains(resolver.last[0].ID, "room:200") {
		t.Fatalf("beta request ID = %q, want room identity", resolver.last[0].ID)
	}
	updated, _ = model.Update(betaBatch)
	model = updated.(mockShellModel)
	if view := model.View(); !strings.Contains(view, "BETA") || strings.Contains(view, "ALPHA") {
		t.Fatalf("beta view used wrong prepared cell:\n%s", view)
	}

	keyState := fmt.Sprintf("%#v", model.imageCells)
	for _, notWant := range []string{"https://", "access_token", "refresh_token", "client_secret", "/tmp/", "../", `C:\`} {
		if strings.Contains(keyState, notWant) {
			t.Fatalf("prepared image cell keys leaked unsafe text %q: %s", notWant, keyState)
		}
	}
}

func TestLiveShellAssetEventsPrepareDownloadedRecordBeforeRendering(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{path: "downloaded.jpg", mediaType: "image/jpeg"}
	preparer := &appFakeImagePreparer{}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImagePreparer: preparer,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("prepared-emote", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	batch := cmd().(assetPreparedBatchMsg)
	if len(batch.results) != 1 {
		t.Fatalf("prepared batch results = %d, want 1: %#v", len(batch.results), batch.results)
	}
	if batch.results[0].err != nil {
		t.Fatalf("prepared batch error = %v, want nil", batch.results[0].err)
	}
	if preparer.calls != 1 || renderer.calls != 1 {
		t.Fatalf("preparer/renderer calls = %d/%d, want 1/1", preparer.calls, renderer.calls)
	}
	if got := preparer.records[0].MediaType; got != "image/jpeg" {
		t.Fatalf("preparer input media type = %q, want image/jpeg", got)
	}
	if got := renderer.records[0].MediaType; got != "image/png" {
		t.Fatalf("renderer input media type = %q, want prepared image/png", got)
	}
	if got := renderer.records[0].Path; got != "prepared.png" {
		t.Fatalf("renderer input path = %q, want prepared.png", got)
	}
	if got := preparer.specs[0].WidthCells; got != 6 {
		t.Fatalf("preparer spec width = %d, want emote fallback width 6", got)
	}

	updated, _ = model.Update(batch)
	model = updated.(mockShellModel)
	if view := model.View(); !strings.Contains(view, "EM25") {
		t.Fatalf("view missing prepared emote cell after preparation:\n%s", view)
	}
}

func TestLiveShellAssetPreparationFailureKeepsFallbackAndRetries(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	preparer := &appFakeImagePreparer{err: render.ErrImagePreparationFailed}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImagePreparer: preparer,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("prepare-failure", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	before := model.View()
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	failedBatch := cmd().(assetPreparedBatchMsg)
	if len(failedBatch.results) != 1 || failedBatch.results[0].err == nil {
		t.Fatalf("failed batch = %#v, want one preparation error", failedBatch.results)
	}
	if renderer.calls != 0 {
		t.Fatalf("renderer calls = %d, want 0 after preparation failure", renderer.calls)
	}
	failedID := failedBatch.results[0].event.RequestID

	updated, cmd = model.Update(failedBatch)
	model = updated.(mockShellModel)
	if model.assetRequested[failedID] {
		t.Fatalf("failed preparation request %q remained permanently marked requested", failedID)
	}
	if cmd == nil || !model.assetLookupScheduled {
		t.Fatalf("failed visible preparation did not schedule retry; scheduled=%v cmd=%#v", model.assetLookupScheduled, cmd)
	}
	after := chatOnlyView(model)
	if !strings.Contains(after, "Kappa") || strings.Contains(after, "EM25") {
		t.Fatalf("fallback not preserved after preparation failure:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestLiveShellPermanentAssetPreparationFailureBacksOffWithoutSecretState(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{
		path:      "oauth:fixture-token.png",
		sourceURL: "https://cdn.example/emote.png?access_token=secret",
		fetchedAt: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC),
	}
	preparer := &appFakeImagePreparer{err: fmt.Errorf("%w: %w", render.ErrImagePreparationFailed, render.ErrImageUnsafeAsset)}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImagePreparer: preparer,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("permanent-prepare-failure", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	before := model.View()
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	failedBatch := cmd().(assetPreparedBatchMsg)
	failedID := failedBatch.results[0].event.RequestID

	updated, cmd = model.Update(failedBatch)
	model = updated.(mockShellModel)
	if cmd == nil || !model.assetRetryScheduled {
		t.Fatalf("permanent failure did not schedule backoff retry; scheduled=%v cmd=%#v", model.assetRetryScheduled, cmd)
	}
	if !model.assetRequested[failedID] {
		t.Fatalf("permanent failure request %q was not kept requested during backoff", failedID)
	}
	if got := len(model.assetPermanentFailure); got != 1 {
		t.Fatalf("permanent failure entries = %d, want 1", got)
	}
	failureState := fmt.Sprintf("%#v %#v", model.assetPermanentFailure, model.assetRetryAfter)
	for _, notWant := range []string{"https://", "access_token", "oauth:fixture-token", "refresh_token", "client_secret"} {
		if strings.Contains(failureState, notWant) {
			t.Fatalf("permanent failure state leaked %q: %s", notWant, failureState)
		}
	}

	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("immediate retry command = %#v, want nil during backoff", cmd)
	}
	if preparer.calls != 1 || renderer.calls != 0 {
		t.Fatalf("preparer/renderer calls after immediate retry = %d/%d, want 1/0", preparer.calls, renderer.calls)
	}
	after := model.View()
	afterChat := chatOnlyView(model)
	if before != after || !strings.Contains(afterChat, "Kappa") || strings.Contains(afterChat, "EM25") {
		t.Fatalf("fallback changed after permanent failure:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestLiveShellPermanentAssetFailureRetriesChangedRecordAfterBackoff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	initialFetchedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	resolver := &appFakeAssetResolver{fetchedAt: initialFetchedAt}
	preparer := &appFakeImagePreparer{err: fmt.Errorf("%w: %w", render.ErrImagePreparationFailed, render.ErrImageCorruptData)}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImagePreparer: preparer,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("changed-record-retry", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	failedBatch := cmd().(assetPreparedBatchMsg)
	failedID := failedBatch.results[0].event.RequestID
	updated, _ = model.Update(failedBatch)
	model = updated.(mockShellModel)
	if got := len(model.assetPermanentFailure); got != 1 {
		t.Fatalf("permanent failure entries = %d, want 1", got)
	}

	model.assetRetryAfter[failedID] = time.Now().Add(-time.Second)
	preparer.err = nil
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("same-record retry returned nil command, want resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)
	if preparer.calls != 1 || renderer.calls != 0 {
		t.Fatalf("preparer/renderer calls after same-record retry = %d/%d, want 1/0", preparer.calls, renderer.calls)
	}
	if view := model.View(); strings.Contains(view, "EM25") {
		t.Fatalf("same-record retry rendered permanent failure cell unexpectedly:\n%s", view)
	}

	model.assetRetryAfter[failedID] = time.Now().Add(-time.Second)
	resolver.fetchedAt = initialFetchedAt.Add(time.Hour)
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("changed-record retry returned nil command, want resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)

	if preparer.calls != 2 || renderer.calls != 1 {
		t.Fatalf("preparer/renderer calls after changed record = %d/%d, want 2/1", preparer.calls, renderer.calls)
	}
	if _, ok := model.assetRetryAfter[failedID]; ok {
		t.Fatalf("assetRetryAfter still contains %q after successful changed-record render", failedID)
	}
	if view := model.View(); !strings.Contains(view, "EM25") {
		t.Fatalf("view missing prepared cell after changed-record retry:\n%s", view)
	}
}

func TestLiveShellPermanentAssetFailureRetriesChangedPayloadIdentityAfterBackoff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	fetchedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	payloadA := "sha256:" + strings.Repeat("a", 64)
	payloadB := "sha256:" + strings.Repeat("b", 64)
	resolver := &appFakeAssetResolver{
		path:            "downloaded.png",
		sourceURL:       "https://cdn.example/emote.png",
		payloadIdentity: payloadA,
		fetchedAt:       fetchedAt,
	}
	preparer := &appFakeImagePreparer{err: fmt.Errorf("%w: %w", render.ErrImagePreparationFailed, render.ErrImageCorruptData)}
	renderer := &appFakeImageRenderer{cells: map[render.ImageCellKey]string{
		{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
	}}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImagePreparer: preparer,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("changed-payload-retry", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	failedBatch := cmd().(assetPreparedBatchMsg)
	failedID := failedBatch.results[0].event.RequestID
	updated, _ = model.Update(failedBatch)
	model = updated.(mockShellModel)
	if got := len(model.assetPermanentFailure); got != 1 {
		t.Fatalf("permanent failure entries = %d, want 1", got)
	}
	failureState := fmt.Sprintf("%#v", model.assetPermanentFailure)
	if !strings.Contains(failureState, payloadA) {
		t.Fatalf("permanent failure state missing payload identity %q: %s", payloadA, failureState)
	}
	for _, notWant := range []string{"https://", "cdn.example", "downloaded.png", "access_token", "refresh_token", "client_secret", "Authorization", "Cookie"} {
		if strings.Contains(failureState, notWant) {
			t.Fatalf("permanent failure state leaked %q: %s", notWant, failureState)
		}
	}

	model.assetRetryAfter[failedID] = time.Now().Add(-time.Second)
	preparer.err = nil
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("same-payload retry returned nil command, want resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)
	if preparer.calls != 1 || renderer.calls != 0 {
		t.Fatalf("preparer/renderer calls after same-payload retry = %d/%d, want 1/0", preparer.calls, renderer.calls)
	}

	model.assetRetryAfter[failedID] = time.Now().Add(-time.Second)
	resolver.payloadIdentity = payloadB
	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("changed-payload retry returned nil command, want resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)

	if preparer.calls != 2 || renderer.calls != 1 {
		t.Fatalf("preparer/renderer calls after changed payload = %d/%d, want 2/1", preparer.calls, renderer.calls)
	}
	if _, ok := model.assetRetryAfter[failedID]; ok {
		t.Fatalf("assetRetryAfter still contains %q after successful changed-payload render", failedID)
	}
	if view := model.View(); !strings.Contains(view, "EM25") {
		t.Fatalf("view missing prepared cell after changed-payload retry:\n%s", view)
	}
}

func TestAssetRequestIDDeduplicatesBySafeIdentityAndDimensions(t *testing.T) {
	ref := twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}
	id := assetRequestID(ref, "100", "example", 6, 1)
	if id == "" {
		t.Fatal("safe asset request ID is empty")
	}
	if got := assetRequestID(ref, "100", "example", 6, 1); got != id {
		t.Fatalf("same safe identity and dimensions produced %q, want %q", got, id)
	}
	for _, other := range []string{
		assetRequestID(ref, "100", "example", 8, 1),
		assetRequestID(ref, "100", "example", 6, 2),
		assetRequestID(ref, "200", "example", 6, 1),
	} {
		if other == "" {
			t.Fatal("comparison asset request ID is empty")
		}
		if other == id {
			t.Fatalf("different identity or dimensions produced duplicate ID %q", id)
		}
	}
	for _, invalid := range []string{
		assetRequestID(ref, "100", "example", 0, 1),
		assetRequestID(ref, "100", "example", 6, 0),
		assetRequestID(twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "../cache/asset.png"}, "", "example", 6, 1),
	} {
		if invalid != "" {
			t.Fatalf("invalid request produced ID %q, want empty", invalid)
		}
	}
	if !strings.Contains(id, "room:100") || !strings.Contains(id, "cells:6x1") {
		t.Fatalf("request ID %q missing safe room identity or dimensions", id)
	}
}

func TestAssetPermanentFailureKeyRejectsPathShapedState(t *testing.T) {
	for _, ref := range []twitch.AssetRef{
		{Kind: assets.KindTwitchEmote, ID: "/home/user/asset.png"},
		{Kind: assets.KindTwitchEmote, ID: "../cache/asset.png"},
		{Kind: assets.KindTwitchEmote, ID: `C:\Users\me\asset.png`},
	} {
		event := assets.Event{
			Kind: assets.EventDownloaded,
			Ref:  ref,
			Record: storage.AssetRecord{
				Key:        storage.AssetKey{Kind: ref.Kind, ID: ref.ID},
				MediaType:  "image/png",
				WidthCells: 6,
			},
		}
		if key, ok := assetPermanentFailureKeyForEvent(event, render.ImageSpec{Ref: ref, WidthCells: 6, HeightCells: 1, Fallback: "Kappa"}); ok {
			t.Fatalf("path-shaped asset ref %#v produced failure key %#v, want rejected", ref, key)
		}
		if id := assetRequestID(ref, "", "example"); id != "" {
			t.Fatalf("path-shaped asset ref %#v produced request ID %q, want empty", ref, id)
		}
	}

	safeBadge := twitch.AssetRef{Kind: assets.KindBadge, ID: "moderator/1"}
	if id := assetRequestID(safeBadge, "", "example"); id == "" {
		t.Fatalf("safe badge ref %#v produced empty request ID", safeBadge)
	}

	event := assets.Event{
		Kind: assets.EventDownloaded,
		Ref:  twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"},
		Record: storage.AssetRecord{
			Key:        storage.AssetKey{Kind: assets.KindTwitchEmote, ID: "../cache/asset.png"},
			MediaType:  "image/png",
			WidthCells: 6,
		},
	}
	key, ok := assetPermanentFailureKeyForEvent(event, render.ImageSpec{
		Ref:         twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"},
		WidthCells:  6,
		HeightCells: 1,
		Fallback:    "Kappa",
	})
	if !ok {
		t.Fatal("safe asset ref with unsafe record key produced no failure key")
	}
	if !key.RecordUnsafe || key.RecordID != "" || key.RecordKind != "" {
		t.Fatalf("unsafe record key stored identity = %#v, want unsafe marker without record text", key)
	}
	failureState := fmt.Sprintf("%#v", key)
	for _, notWant := range []string{"/home/user", "../cache", `C:\Users`, "asset.png"} {
		if strings.Contains(failureState, notWant) {
			t.Fatalf("failure key leaked path-shaped text %q: %s", notWant, failureState)
		}
	}

	event.Record.Key = storage.AssetKey{Kind: assets.KindTwitchEmote, ID: "25"}
	event.Record.PayloadIdentity = "https://cdn.example/asset.png?access_token=secret"
	key, ok = assetPermanentFailureKeyForEvent(event, render.ImageSpec{
		Ref:         twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"},
		WidthCells:  6,
		HeightCells: 1,
		Fallback:    "Kappa",
	})
	if !ok {
		t.Fatal("unsafe payload identity event produced no failure key")
	}
	if !key.PayloadIdentityUnsafe || key.PayloadIdentity != "" {
		t.Fatalf("unsafe payload identity stored identity = %#v, want unsafe marker without payload text", key)
	}
	failureState = fmt.Sprintf("%#v", key)
	for _, notWant := range []string{"https://", "cdn.example", "asset.png", "access_token", "secret"} {
		if strings.Contains(failureState, notWant) {
			t.Fatalf("failure key leaked payload identity text %q: %s", notWant, failureState)
		}
	}
}

func TestLiveShellPermanentAssetRenderFailureBacksOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{fetchedAt: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	renderer := &appFakeImageRenderer{
		cells: map[render.ImageCellKey]string{
			{Kind: assets.KindTwitchEmote, ID: "25"}: "EM25  ",
		},
		err: fmt.Errorf("%w: %w", render.ErrImageRenderFailed, render.ErrImageUnsupportedMediaType),
	}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: renderer,
	})
	message := assetEventMessage("permanent-render-failure", "25", "😀")
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	failedBatch := cmd().(assetPreparedBatchMsg)
	updated, _ = model.Update(failedBatch)
	model = updated.(mockShellModel)

	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("immediate renderer retry command = %#v, want nil during backoff", cmd)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls after immediate retry = %d, want 1", renderer.calls)
	}
	if view := chatOnlyView(model); !strings.Contains(view, "Kappa") || strings.Contains(view, "EM25") {
		t.Fatalf("fallback not preserved after render failure:\n%s", view)
	}
}

func TestImageCellKeyFromAssetEventRejectsUnsafeRecordKeyFallback(t *testing.T) {
	event := assets.Event{
		Record: storage.AssetRecord{
			Key: storage.AssetKey{Kind: assets.KindAvatar, ID: "https://cdn.example/avatar.png?access_token=secret"},
		},
	}
	if key, ok := imageCellKeyFromAssetEvent(event, render.ImageSpec{}); ok {
		t.Fatalf("unsafe record key fallback = %#v, true; want false", key)
	}

	event.Record.Key = storage.AssetKey{Kind: assets.KindTwitchEmote, ID: "25"}
	key, ok := imageCellKeyFromAssetEvent(event, render.ImageSpec{})
	if !ok || key != (render.ImageCellKey{Kind: assets.KindTwitchEmote, ID: "25"}) {
		t.Fatalf("safe record key fallback = %#v ok=%v, want twitch emote key", key, ok)
	}
}

func TestLiveShellDisabledOrUnsupportedEmojiImagesDoNotScheduleAssetWork(t *testing.T) {
	for _, tt := range []struct {
		name     string
		features config.FeatureConfig
	}{
		{
			name: "image off",
			features: func() config.FeatureConfig {
				features := config.Default().Features
				features.ImageMode = "off"
				features.EmojiMode = "image"
				return features
			}(),
		},
		{
			name: "auto unsupported",
			features: func() config.FeatureConfig {
				features := config.Default().Features
				features.ImageMode = "auto"
				features.EmojiMode = "image"
				return features
			}(),
		},
		{
			name: "unicode mode",
			features: func() config.FeatureConfig {
				features := config.Default().Features
				features.ImageMode = "normal"
				features.EmojiMode = "unicode"
				return features
			}(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features = tt.features
			resolver := &appFakeAssetResolver{}
			renderer := &appFakeImageRenderer{}
			model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
				AssetResolver: resolver,
				ImageRenderer: renderer,
			})
			model.activeChannelState().messages = []twitch.ChatMessage{{
				ID:          "emoji-only",
				Channel:     "example",
				Timestamp:   time.Date(2026, 7, 2, 20, 0, 0, 0, time.Local),
				DisplayName: "emoji_fan",
				Type:        twitch.MessageTypeChat,
				Text:        "native 😀",
			}}

			before := model.View()
			updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
			model = updated.(mockShellModel)
			if requests := model.pendingAssetRequests(); len(requests) != 0 {
				t.Fatalf("pending asset requests = %#v, want none", requests)
			}
			_, cmd := model.Update(assetLookupTickMsg{})
			if cmd != nil {
				t.Fatalf("assetLookupTick command = %#v, want nil", cmd)
			}
			if resolver.calls != 0 || renderer.calls != 0 {
				t.Fatalf("resolver/renderer calls = %d/%d, want 0/0", resolver.calls, renderer.calls)
			}
			after := model.View()
			if !strings.Contains(before, "😀") || !strings.Contains(after, "😀") {
				t.Fatalf("native emoji fallback missing before/after:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestLiveShellAssetKindsGateMissingCredentialFallbacks(t *testing.T) {
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
		AssetKinds:    map[string]bool{assets.KindEmoji: true},
		ImageRenderer: renderer,
	})
	model.activeChannelState().messages = []twitch.ChatMessage{assetEventMessage("emoji-only-live-stack", "25", "😀")}

	before := chatOnlyView(model)
	if !strings.Contains(before, "[V]") || !strings.Contains(before, "Kappa") || !strings.Contains(before, "😀") {
		t.Fatalf("fallbacks missing before asset work:\n%s", before)
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	requests := model.pendingAssetRequests()
	if got, want := requestKinds(requests), []string{assets.KindEmoji}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request kinds = %#v, want %#v; requests=%#v", got, want, requests)
	}

	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want emoji resolver command")
	}
	updated, _ = model.Update(cmd().(assetPreparedBatchMsg))
	model = updated.(mockShellModel)
	after := chatOnlyView(model)
	if !strings.Contains(after, ":)") {
		t.Fatalf("emoji cell missing after allowed asset work:\n%s", after)
	}
	for _, want := range []string{"[V]", "Kappa"} {
		if !strings.Contains(after, want) {
			t.Fatalf("fallback %q missing after missing-credential gated asset work:\n%s", want, after)
		}
	}
	for _, notWant := range []string{"[A42]", "BMOD", "EM25"} {
		if strings.Contains(after, notWant) {
			t.Fatalf("gated asset cell %q rendered unexpectedly:\n%s", notWant, after)
		}
	}
}

func TestLiveShellAssetRequestsPreferChannelIDForMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: &appFakeImageRenderer{},
	})
	message := assetEventMessage("channel-id-metadata", "25", "😀")
	message.Channel = "example"
	message.ChannelID = "141981764"
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	_, cmd := model.Update(assetLookupTickMsg{})
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	_ = cmd()
	if len(resolver.last) != 1 {
		t.Fatalf("asset requests = %d, want 1: %#v", len(resolver.last), resolver.last)
	}
	if got, want := resolver.last[0].ChannelID, "141981764"; got != want {
		t.Fatalf("request ChannelID = %q, want room ID %q", got, want)
	}
}

func TestLiveShellAssetRequestsDoNotUseChannelNameAsChannelID(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: &appFakeImageRenderer{},
	})
	message := assetEventMessage("missing-room-id-metadata", "25", "Kappa")
	message.Channel = "example"
	message.ChannelID = ""
	message.Badges = nil
	model.activeChannelState().messages = []twitch.ChatMessage{message}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 12})
	model = updated.(mockShellModel)
	_, cmd := model.Update(assetLookupTickMsg{})
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command")
	}
	_ = cmd()
	if len(resolver.last) != 1 {
		t.Fatalf("asset requests = %d, want 1: %#v", len(resolver.last), resolver.last)
	}
	if got := resolver.last[0].ChannelID; got != "" {
		t.Fatalf("request ChannelID = %q, want empty when room ID is unavailable", got)
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
	cfg.Features.EmojiMode = "unicode"
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

func TestLiveShellAssetSchedulerDeduplicatesRepeatedVisibleRequestsAndKeepsViewPure(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	renderer := &appFakeImageRenderer{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		ImageRenderer: renderer,
	})
	for i := 0; i < 400; i++ {
		model.activeChannelState().messages = append(model.activeChannelState().messages, assetEventMessage(fmt.Sprintf("repeat-%03d", i), "25", "😀"))
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 30})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want one bounded resolver command")
	}
	if resolver.calls != 0 || renderer.calls != 0 {
		t.Fatalf("update path performed asset work before command execution: resolver=%d renderer=%d", resolver.calls, renderer.calls)
	}
	if got, wantMax := len(model.assetRequested), 4; got > wantMax {
		t.Fatalf("assetRequested entries = %d, want at most %d for repeated visible messages", got, wantMax)
	}
	_ = model.View()
	if resolver.calls != 0 || renderer.calls != 0 {
		t.Fatalf("view path performed asset work: resolver=%d renderer=%d", resolver.calls, renderer.calls)
	}

	batch := cmd().(assetPreparedBatchMsg)
	if got, want := len(batch.results), 4; got != want {
		t.Fatalf("prepared batch results = %d, want %d", got, want)
	}
	if got, want := len(resolver.last), 4; got != want {
		t.Fatalf("resolver requests = %d, want %d: %#v", got, want, resolver.last)
	}
	for _, request := range resolver.last {
		if !strings.Contains(request.ID, "cells:") {
			t.Fatalf("request ID %q does not include cell dimensions", request.ID)
		}
	}
}

func TestLiveShellAssetSchedulerBoundsVisibleQueue(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	renderer := &appFakeImageRenderer{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		AssetKinds:    map[string]bool{assets.KindTwitchEmote: true},
		ImageRenderer: renderer,
	})
	for i := 0; i < assetWorkQueueMax+8; i++ {
		model.activeChannelState().messages = append(model.activeChannelState().messages, emoteOnlyAssetMessage(fmt.Sprintf("queued-%02d", i), fmt.Sprintf("emote-%02d", i)))
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: assetWorkQueueMax + 13})
	model = updated.(mockShellModel)
	requests := model.pendingAssetRequests()
	if got, want := len(requests), assetWorkQueueMax; got != want {
		t.Fatalf("pending asset requests = %d, want queue bound %d", got, want)
	}

	updated, cmd := model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want bounded resolver command")
	}
	if got, want := len(model.assetRequested), assetWorkQueueMax; got != want {
		t.Fatalf("assetRequested entries = %d, want %d", got, want)
	}
	batch := cmd().(assetPreparedBatchMsg)
	if got, want := len(batch.results), assetWorkQueueMax; got != want {
		t.Fatalf("prepared batch results = %d, want %d", got, want)
	}
	if got, want := len(resolver.last), assetWorkQueueMax; got != want {
		t.Fatalf("resolver requests = %d, want %d", got, want)
	}

	updated, cmd = model.Update(batch)
	model = updated.(mockShellModel)
	if cmd == nil || !model.assetLookupScheduled {
		t.Fatalf("remaining visible assets did not schedule the next bounded tick; scheduled=%v cmd=%#v", model.assetLookupScheduled, cmd)
	}
}

func TestResolveAssetsCommandBoundsConcurrencyAndContext(t *testing.T) {
	release := make(chan struct{})
	resolver := &blockingAssetResolver{
		entered: make(chan struct{}, assetWorkParallel+1),
		release: release,
	}
	preparer := &deadlineTrackingImagePreparer{}
	renderer := &deadlineTrackingImageRenderer{}
	model := mockShellModel{
		assetResolver: resolver,
		imagePreparer: preparer,
		imageRenderer: renderer,
	}
	requests := make([]assets.Request, assetWorkParallel+3)
	for i := range requests {
		ref := twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: fmt.Sprintf("parallel-%02d", i)}
		requests[i] = assets.Request{
			ID:          assetRequestID(ref, "", "example", 6, 1),
			Ref:         ref,
			Channel:     "example",
			Fallback:    "Kappa",
			WidthCells:  6,
			HeightCells: 1,
		}
	}

	cmd := model.resolveAssetsCommand(requests)
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	for i := 0; i < assetWorkParallel; i++ {
		select {
		case <-resolver.entered:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for worker %d to enter resolver", i+1)
		}
	}
	select {
	case <-resolver.entered:
		t.Fatalf("more than %d asset workers entered before release", assetWorkParallel)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(time.Second):
		t.Fatal("asset command did not finish after releasing workers")
	}
	batch := msg.(assetPreparedBatchMsg)
	if got, want := len(batch.results), len(requests); got != want {
		t.Fatalf("prepared batch results = %d, want %d", got, want)
	}
	calls, maxActive, deadlines := resolver.snapshot()
	if calls != len(requests) {
		t.Fatalf("resolver calls = %d, want %d", calls, len(requests))
	}
	if maxActive > assetWorkParallel {
		t.Fatalf("resolver max concurrency = %d, want <= %d", maxActive, assetWorkParallel)
	}
	if deadlines != len(requests) {
		t.Fatalf("resolver deadline count = %d, want %d", deadlines, len(requests))
	}
	if preparer.deadlineCount() != len(requests) {
		t.Fatalf("preparer deadline count = %d, want %d", preparer.deadlineCount(), len(requests))
	}
	if renderer.deadlineCount() != len(requests) {
		t.Fatalf("renderer deadline count = %d, want %d", renderer.deadlineCount(), len(requests))
	}
}

func TestResolveAssetsCommandHonorsContextDeadline(t *testing.T) {
	ref := twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "deadline"}
	request := assets.Request{
		ID:          assetRequestID(ref, "", "example", 6, 1),
		Ref:         ref,
		Channel:     "example",
		Fallback:    "Kappa",
		WidthCells:  6,
		HeightCells: 1,
	}
	resolver := &deadlineBlockingAssetResolver{}
	renderer := &appFakeImageRenderer{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	results := resolveAssetRequests(ctx, []assets.Request{request}, resolver, nil, renderer, nil)
	if got, want := len(results), 1; got != want {
		t.Fatalf("asset results = %d, want %d", got, want)
	}
	if got, want := results[0].event.Kind, assets.EventCanceled; got != want {
		t.Fatalf("event kind = %s, want %s", got, want)
	}
	if !errors.Is(results[0].event.Err, context.DeadlineExceeded) {
		t.Fatalf("event err = %v, want deadline exceeded", results[0].event.Err)
	}
	if !resolver.sawDeadline() {
		t.Fatal("resolver context had no deadline")
	}
	if renderer.calls != 0 {
		t.Fatalf("renderer calls = %d, want 0 after canceled resolve", renderer.calls)
	}
}

func TestLiveShellAssetFailureCanRetryVisibleRequest(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
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
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	model.activeChannelState().messages = nil

	chatContent := func(m mockShellModel) string { return m.chatView(m.layout()) }

	updated, _ := model.Update(mockIncomingMessageMsg{message: activeAssetEventMessage()})
	model = updated.(mockShellModel)
	for i := 0; i < 100 && !strings.Contains(chatContent(model), "Kappa"); i++ {
		clock.Add(mockRevealDelay)
		updated, _ = model.Update(mockAnimationTickMsg{})
		model = updated.(mockShellModel)
	}
	if got := model.activeChannelState().revealQueue.Len(); got == 0 {
		t.Fatal("active reveal completed before asset refresh test could run")
	}
	if view := chatContent(model); !strings.Contains(view, "Kappa") {
		t.Fatalf("test setup did not reveal Kappa before asset event:\n%s", view)
	}

	updated, _ = model.Update(assetPreparedBatchMsg{results: []assetPreparedMsg{
		preparedAssetForTest(twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}, "EM25  ", 6),
	}})
	model = updated.(mockShellModel)
	if view := chatContent(model); !strings.Contains(view, "EM25") {
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
		{name: "normal", width: 88, height: 16, wantSidebar: true},
		{name: "wide", width: 124, height: 17, wantSidebar: true, wantWideSize: true},
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

func TestThemeSettingsOpensPreviewsAndPersists(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18
	originalTheme := model.theme

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("open theme settings returned command %#v, want nil", cmd)
	}
	if !model.themeSettings.open {
		t.Fatal("themeSettings.open = false, want true")
	}
	view := model.View()
	if !strings.Contains(view, "Theme") || !strings.Contains(view, "claude (active)") {
		t.Fatalf("theme settings view missing expected content:\n%s", view)
	}

	// Move to a known preset and confirm live preview applies immediately.
	names := themeSettingsNames()
	var nordIndex int
	for i, name := range names {
		if name == "nord" {
			nordIndex = i
			break
		}
	}
	for model.themeSettings.selected != nordIndex {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(mockShellModel)
	}
	if model.theme != theme.Presets()["nord"] {
		t.Fatalf("live preview theme = %+v, want nord preset", model.theme)
	}
	if model.effectiveConfig.Features.ThemeName == "nord" {
		t.Fatal("effectiveConfig.ThemeName changed before persisting, want unchanged until enter")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if model.themeSettings.open {
		t.Fatal("themeSettings.open after enter = true, want false")
	}
	if model.effectiveConfig.Features.ThemeName != "nord" {
		t.Fatalf("effectiveConfig.ThemeName after persist = %q, want nord", model.effectiveConfig.Features.ThemeName)
	}
	data, err := os.ReadFile(cfg.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", cfg.Path, err)
	}
	if !strings.Contains(string(data), `theme_name = "nord"`) {
		t.Fatalf("persisted config missing theme_name = nord:\n%s", data)
	}
	if originalTheme == model.theme {
		t.Fatal("theme unchanged after persisting a different preset")
	}
}

func TestThemeSettingsEscRevertsPreviewWithoutPersisting(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18
	originalTheme := model.theme
	originalThemeName := model.effectiveConfig.Features.ThemeName

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(mockShellModel)
	if model.theme == originalTheme {
		t.Fatal("moving selection did not change live preview theme")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if model.themeSettings.open {
		t.Fatal("themeSettings.open after esc = true, want false")
	}
	if model.theme != originalTheme {
		t.Fatalf("theme after esc = %+v, want reverted original %+v", model.theme, originalTheme)
	}
	if model.effectiveConfig.Features.ThemeName != originalThemeName {
		t.Fatal("effectiveConfig.ThemeName changed after esc, want unchanged")
	}
	if _, err := os.Stat(cfg.Path); err == nil {
		t.Fatal("config file was written after esc, want no persistence")
	}
}

func TestThemeSettingsAndCommandPaletteAreMutuallyExclusive(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	model = updated.(mockShellModel)
	if !model.themeSettings.open || model.palette.open {
		t.Fatalf("expected theme settings open and palette closed; theme=%v palette=%v", model.themeSettings.open, model.palette.open)
	}
}

func TestEmotePickerOpensFiltersExecutesAndCloses(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("open emote picker returned command %#v, want nil", cmd)
	}
	if !model.emotePicker.open {
		t.Fatal("emotePicker.open = false, want true")
	}
	view := model.View()
	for _, want := range []string{"Emote search", "Kappa", "PogChamp"} {
		if !strings.Contains(view, want) {
			t.Fatalf("emote picker view missing %q:\n%s", want, view)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("champ")})
	model = updated.(mockShellModel)
	if got, want := model.emotePicker.query, "champ"; got != want {
		t.Fatalf("emote picker query = %q, want %q", got, want)
	}
	entries := model.visibleEmotePickerEntries()
	if len(entries) != 1 || entries[0].Name != "PogChamp" {
		t.Fatalf("filtered entries = %#v, want only PogChamp", entries)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if model.emotePicker.open {
		t.Fatal("emotePicker.open after enter = true, want false")
	}
	if got, want := model.activeChannelState().composerText, "PogChamp "; got != want {
		t.Fatalf("composer text after emote picker selection = %q, want %q", got, want)
	}
}

func TestEmotePickerEscCancelsWithoutInserting(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if model.emotePicker.open {
		t.Fatal("emotePicker.open after esc = true, want false")
	}
	if model.activeChannelState().composerText != "" {
		t.Fatalf("composerText after esc = %q, want empty", model.activeChannelState().composerText)
	}
}

func TestEmotePickerAndCommandPaletteAreMutuallyExclusive(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	if !model.palette.open {
		t.Fatal("palette.open = false after ctrl+p, want true")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(mockShellModel)
	if !model.emotePicker.open || model.palette.open {
		t.Fatalf("expected emote picker open and palette closed; emotePicker=%v palette=%v", model.emotePicker.open, model.palette.open)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	if !model.palette.open || model.emotePicker.open {
		t.Fatalf("expected palette open and emote picker closed; palette=%v emotePicker=%v", model.palette.open, model.emotePicker.open)
	}
}

func TestCommandPaletteOpensFiltersExecutesAndCloses(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta", "gamma"}
	model := newMockShellModel("alpha", cfg)
	model.width = 88
	model.height = 15

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("open palette returned command %#v, want nil", cmd)
	}
	if !model.palette.open {
		t.Fatal("palette open = false, want true")
	}
	view := model.View()
	for _, want := range []string{"Command", "Focus composer", "focus=palette"} {
		if !strings.Contains(view, want) {
			t.Fatalf("palette view missing %q:\n%s", want, view)
		}
	}
	if got, want := lineCount(view), 15; got != want {
		t.Fatalf("palette view line count = %d, want %d:\n%s", got, want, view)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gamma")})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette filter returned command %#v, want nil", cmd)
	}
	if got, want := model.palette.query, "gamma"; got != want {
		t.Fatalf("palette query = %q, want %q", got, want)
	}
	commands := model.visibleCommandPaletteCommands()
	if len(commands) != 1 || commands[0].channel != "gamma" {
		t.Fatalf("filtered commands = %#v, want only switch to gamma", commands)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette channel switch returned command %#v, want nil without visible assets", cmd)
	}
	if model.palette.open {
		t.Fatal("palette open after execute = true, want false")
	}
	if got, want := model.activeChannelName(), "gamma"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
}

func TestCommandPaletteFilteringDoesNotMutateComposerReplyOrSelection(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = numberedMockMessages("example", 4)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "draft text"
	model.activeChannelState().replyTo = replyContextFromMessage(model.activeChannelState().messages[2])
	beforeReply := *model.activeChannelState().replyTo

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("help")})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette filter returned command %#v, want nil", cmd)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer text after palette filter = %q, want %q", got, want)
	}
	if got, want := model.palette.query, "help"; got != want {
		t.Fatalf("palette query = %q, want %q", got, want)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette escape returned command %#v, want nil", cmd)
	}
	if model.palette.open {
		t.Fatal("palette open after esc = true, want false")
	}
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after palette esc = %v, want %v", got, want)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer text after palette esc = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after palette esc = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("focus chat")})
	model = updated.(mockShellModel)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("focus chat command returned command %#v, want nil", cmd)
	}
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after palette focus command = %v, want %v", got, want)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer text after focus command = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after focus command = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}
}

func TestCommandPaletteAndKeyboardShortcutsClearAndReconnect(t *testing.T) {
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	state := model.activeChannelState()
	state.messages = numberedMockMessages("example", 3)
	state.composerText = "draft text"
	state.replyTo = replyContextFromMessage(state.messages[1])
	beforeReply := *state.replyTo
	model.focus = mockFocusComposer

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("clear local")})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("clear local command returned command %#v, want nil", cmd)
	}
	if got := len(model.activeChannelState().messages); got != 0 {
		t.Fatalf("messages after palette clear = %d, want 0", got)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer text after palette clear = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after palette clear = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}

	model.activeChannelState().messages = numberedMockMessages("example", 2)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("ctrl+l returned command %#v, want nil", cmd)
	}
	if got := len(model.activeChannelState().messages); got != 0 {
		t.Fatalf("messages after ctrl+l = %d, want 0", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("reconnect")})
	model = updated.(mockShellModel)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("palette reconnect returned nil command, want reconnect command")
	}
	if model.palette.open {
		t.Fatal("palette open after reconnect execute = true, want false")
	}
	if got, want := model.activeChannelState().status.Status, ConnectionReconnecting; got != want {
		t.Fatalf("status after reconnect request = %q, want %q", got, want)
	}
	updated, _ = model.Update(cmd().(reconnectCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := client.ReconnectCount(), 1; got != want {
		t.Fatalf("reconnect count = %d, want %d", got, want)
	}
	if got, want := model.activeChannelState().status.Status, ConnectionConnecting; got != want {
		t.Fatalf("status after reconnect completion = %q, want %q", got, want)
	}
	if !strings.Contains(model.activeChannelState().status.Detail, "waiting for Twitch IRC") {
		t.Fatalf("status detail after reconnect completion = %q, want waiting feedback", model.activeChannelState().status.Detail)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("ctrl+r returned nil command, want reconnect command")
	}
	updated, _ = model.Update(cmd().(reconnectCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := client.ReconnectCount(), 2; got != want {
		t.Fatalf("reconnect count after ctrl+r = %d, want %d", got, want)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer text after reconnect = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("replyTo after reconnect = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}

	mockModel := newMockShellModel("example", config.Default())
	updated, cmd = mockModel.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	mockModel = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("unsupported ctrl+r returned command %#v, want nil", cmd)
	}
	if got, want := mockModel.activeChannelState().status.Status, ConnectionConnected; got != want {
		t.Fatalf("unsupported reconnect status = %q, want preserved %q", got, want)
	}
	if !strings.Contains(mockModel.activeChannelState().status.Detail, "unavailable") {
		t.Fatalf("unsupported reconnect detail = %q, want unavailable guidance", mockModel.activeChannelState().status.Detail)
	}
}

func TestMockShellKeyboardMessageFiltersPreserveChannelState(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "twi_bot"
	model := newMockShellModel("example", cfg)
	state := model.activeChannelState()
	state.messages = filterTestMessages("example")
	state.unread = 4
	state.composerText = "draft text"
	state.replyTo = replyContextFromMessage(state.messages[0])
	beforeReply := *state.replyTo

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("mention filter toggle returned command %#v, want nil without asset services", cmd)
	}
	if !model.activeChannelState().messageFilters.enabled(messageFilterMentions) {
		t.Fatal("mention filter is not active after keyboard toggle")
	}
	if got, want := len(model.activeChannelState().messages), 5; got != want {
		t.Fatalf("message history length = %d, want %d", got, want)
	}
	if got, want := model.activeChannelState().unread, 4; got != want {
		t.Fatalf("unread count = %d, want preserved %d", got, want)
	}
	if got, want := model.activeChannelState().composerText, "draft text"; got != want {
		t.Fatalf("composer draft = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("reply selection = %#v, want preserved %#v", model.activeChannelState().replyTo, beforeReply)
	}
	if selected, ok := model.selectedInspectMessage(); !ok || selected.ID != "ordinary-1" {
		t.Fatalf("selected inspect message = %#v, %v; want hidden ordinary selection preserved", selected, ok)
	}

	chatRows := strings.Join(model.chatRows(model.layout()), "\n")
	if !strings.Contains(chatRows, "hello @twi_bot") {
		t.Fatalf("mention-filtered chat missing mention:\n%s", chatRows)
	}
	for _, hidden := range []string{"ordinary viewer chatter", "vip update", "slow mode enabled", "connection failed"} {
		if strings.Contains(chatRows, hidden) {
			t.Fatalf("mention-filtered chat still contains %q:\n%s", hidden, chatRows)
		}
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("mention filter toggle-off returned command %#v, want nil without asset services", cmd)
	}
	if model.activeChannelState().messageFilters.active() {
		t.Fatalf("filters after toggle-off = %q, want none", model.activeChannelState().messageFilters.summary())
	}
	if chatRows := strings.Join(model.chatRows(model.layout()), "\n"); !strings.Contains(chatRows, "ordinary viewer chatter") || !strings.Contains(chatRows, "hello @twi_bot") {
		t.Fatalf("unfiltered chat missing restored history:\n%s", chatRows)
	}
}

func TestMockShellComposerFocusedNumbersDoNotToggleMessageFilters(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.focus = mockFocusComposer

	for _, r := range []rune{'1', '2', '3', '4', '0'} {
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(mockShellModel)
		if cmd != nil {
			t.Fatalf("composer numeric key %q returned command %#v, want nil", r, cmd)
		}
	}

	if got, want := model.activeChannelState().composerText, "12340"; got != want {
		t.Fatalf("composer text after numeric input = %q, want %q", got, want)
	}
	if model.activeChannelState().messageFilters.active() {
		t.Fatalf("message filters after composer numeric input = %q, want none", model.activeChannelState().messageFilters.summary())
	}
}

func TestCommandPaletteTogglesAndResetsMessageFilters(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = filterTestMessages("example")
	model.activeChannelState().composerText = "draft"
	model.activeChannelState().replyTo = replyContextFromMessage(model.activeChannelState().messages[0])
	beforeReply := *model.activeChannelState().replyTo

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("roles")})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette role filter returned command %#v, want nil without asset services", cmd)
	}
	if !model.activeChannelState().messageFilters.enabled(messageFilterRoles) {
		t.Fatal("role filter is not active after palette command")
	}
	chatRows := strings.Join(model.chatRows(model.layout()), "\n")
	if !strings.Contains(chatRows, "vip update") {
		t.Fatalf("role-filtered chat missing role message:\n%s", chatRows)
	}
	for _, hidden := range []string{"ordinary viewer chatter", "hello @twi_bot", "slow mode enabled", "connection failed"} {
		if strings.Contains(chatRows, hidden) {
			t.Fatalf("role-filtered chat still contains %q:\n%s", hidden, chatRows)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("reset filters")})
	model = updated.(mockShellModel)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("palette reset filters returned command %#v, want nil without asset services", cmd)
	}
	if model.activeChannelState().messageFilters.active() {
		t.Fatalf("filters after reset = %q, want none", model.activeChannelState().messageFilters.summary())
	}
	if got, want := model.activeChannelState().composerText, "draft"; got != want {
		t.Fatalf("composer draft after reset = %q, want %q", got, want)
	}
	if model.activeChannelState().replyTo == nil || *model.activeChannelState().replyTo != beforeReply {
		t.Fatalf("reply selection after reset = %#v, want %#v", model.activeChannelState().replyTo, beforeReply)
	}
	chatRows = strings.Join(model.chatRows(model.layout()), "\n")
	for _, want := range []string{"ordinary viewer chatter", "vip update", "slow mode enabled", "connection failed"} {
		if !strings.Contains(chatRows, want) {
			t.Fatalf("reset chat missing %q:\n%s", want, chatRows)
		}
	}
}

func TestMessageFilterTogglesPreserveActiveSendQueueAndFeedback(t *testing.T) {
	client := NewFakeChatClient(2)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.activeChannelState().messages = filterTestMessages("example")
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "first message"

	updated, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if firstCmd == nil {
		t.Fatal("first Enter returned nil command, want active send command")
	}
	model.activeChannelState().composerText = "second message"
	updated, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if secondCmd != nil {
		t.Fatalf("second Enter returned command %#v while first send active, want queued only", secondCmd)
	}

	before := model.activeChannelState()
	if before.activeSend == nil || len(before.sendQueue) != 1 {
		t.Fatalf("send setup active=%#v queue=%#v, want one active and one queued", before.activeSend, before.sendQueue)
	}
	activeID := before.activeSend.ID
	activeText := before.activeSend.Text
	queuedID := before.sendQueue[0].ID
	queuedText := before.sendQueue[0].Text
	sendState := before.sendState
	sendFeedback := before.sendFeedback

	model.focus = mockFocusChat
	for _, r := range []rune{'1', '1'} {
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(mockShellModel)
		if cmd != nil {
			t.Fatalf("filter toggle %q returned command %#v without asset services, want nil", r, cmd)
		}
		after := model.activeChannelState()
		if after.activeSend == nil || after.activeSend.ID != activeID || after.activeSend.Text != activeText {
			t.Fatalf("activeSend after filter toggle %q = %#v, want id=%d text=%q", r, after.activeSend, activeID, activeText)
		}
		if len(after.sendQueue) != 1 || after.sendQueue[0].ID != queuedID || after.sendQueue[0].Text != queuedText {
			t.Fatalf("sendQueue after filter toggle %q = %#v, want id=%d text=%q", r, after.sendQueue, queuedID, queuedText)
		}
		if after.sendState != sendState || after.sendFeedback != sendFeedback {
			t.Fatalf("send status after filter toggle %q = %q/%q, want %q/%q", r, after.sendState, after.sendFeedback, sendState, sendFeedback)
		}
	}
}

func TestMessageFiltersArePerChannelAcrossSwitching(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "twi_bot"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	alpha := model.channels.ensure("alpha")
	beta := model.channels.ensure("beta")
	alpha.messages = filterTestMessages("alpha")
	beta.messages = filterTestMessages("beta")
	beta.messages[0].Text = "beta ordinary chatter"
	beta.messages[1].Text = "beta hello @twi_bot"
	beta.messages[1].Fragments = []twitch.MessageFragment{{Type: twitch.FragmentMention, Text: "@twi_bot"}}
	beta.messages[2].Text = "beta vip update"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = updated.(mockShellModel)
	if !alpha.messageFilters.enabled(messageFilterMentions) {
		t.Fatal("alpha mention filter is not active")
	}
	if beta.messageFilters.active() {
		t.Fatalf("beta filters = %q, want none before switching", beta.messageFilters.summary())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if model.activeChannelState().messageFilters.active() {
		t.Fatalf("beta filters after switch = %q, want none", model.activeChannelState().messageFilters.summary())
	}
	if view := model.View(); !strings.Contains(view, "beta ordinary chatter") || !strings.Contains(view, "beta vip update") {
		t.Fatalf("unfiltered beta view missing messages:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(mockShellModel)
	if !beta.messageFilters.enabled(messageFilterRoles) {
		t.Fatal("beta role filter is not active")
	}
	if view := model.View(); !strings.Contains(view, "beta vip update") || strings.Contains(view, "beta ordinary chatter") {
		t.Fatalf("role-filtered beta view mismatch:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if !model.activeChannelState().messageFilters.enabled(messageFilterMentions) {
		t.Fatalf("alpha filters after return = %q, want mentions", model.activeChannelState().messageFilters.summary())
	}
	if view := model.View(); !strings.Contains(view, "hello @twi_bot") || strings.Contains(view, "ordinary viewer chatter") {
		t.Fatalf("mention-filtered alpha view mismatch:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	model = updated.(mockShellModel)
	if model.activeChannelState().messageFilters.active() {
		t.Fatalf("alpha filters after reset = %q, want none", model.activeChannelState().messageFilters.summary())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if !model.activeChannelState().messageFilters.enabled(messageFilterRoles) {
		t.Fatalf("beta filters after alpha reset = %q, want roles still active", model.activeChannelState().messageFilters.summary())
	}
}

func TestMessageFiltersLimitVisibleAssetScheduling(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "normal"
	cfg.Features.EmojiMode = "unicode"
	cfg.Features.EmoteMode = "image"
	resolver := &appFakeAssetResolver{}
	renderer := &appFakeImageRenderer{}
	model := newLiveShellModelWithClockAndOptions("example", cfg, NewFakeChatClient(1), nil, ClientOptions{
		AssetResolver: resolver,
		AssetKinds:    map[string]bool{assets.KindTwitchEmote: true},
		ImageRenderer: renderer,
	})
	hidden := emoteOnlyAssetMessage("hidden-asset", "111")
	hidden.Text = "ordinary asset message"
	visible := emoteOnlyAssetMessage("visible-role-asset", "222")
	visible.Text = "vip asset message"
	visible.Badges = []twitch.Badge{{SetID: "vip", ID: "1"}}
	model.activeChannelState().messages = []twitch.ChatMessage{hidden, visible}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(mockShellModel)
	if cmd == nil || !model.assetLookupScheduled {
		t.Fatalf("role filter toggle did not schedule visible asset work; scheduled=%v cmd=%#v", model.assetLookupScheduled, cmd)
	}
	requests := model.pendingAssetRequests()
	if len(requests) != 1 || requests[0].Ref.ID != "222" {
		t.Fatalf("filtered pending asset requests = %#v, want only visible role emote 222", requests)
	}

	updated, cmd = model.Update(assetLookupTickMsg{})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("assetLookupTick returned nil command, want resolver command for filtered visible asset")
	}
	_ = cmd().(assetPreparedBatchMsg)
	if len(resolver.last) != 1 || resolver.last[0].Ref.ID != "222" {
		t.Fatalf("resolver requests = %#v, want only visible role emote 222", resolver.last)
	}
}

func TestMessageFiltersAllowKeyboardSelectionOfActiveReveal(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "twi_bot"
	model := newMockShellModel("example", cfg)
	state := model.activeChannelState()
	state.messages = []twitch.ChatMessage{filterTestMessages("example")[0]}
	state.messageFilters.toggle(messageFilterMentions)

	revealing := filterTestMessages("example")[1]
	revealing.ID = "active-mention"
	if cmd := model.enqueueMessage(revealing); cmd == nil {
		t.Fatal("enqueueMessage returned nil, want scheduled reveal for active mention")
	}
	if len(state.activeMessages) == 0 {
		t.Fatal("active reveal message was not queued")
	}
	if rows := strings.Join(model.chatRows(model.layout()), "\n"); strings.Contains(rows, "ordinary viewer chatter") {
		t.Fatalf("filtered active reveal rows include hidden static message:\n%s", rows)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("reply shortcut returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "active-mention" {
		t.Fatalf("replyTo after selecting active reveal = %#v, want active-mention", model.activeChannelState().replyTo)
	}
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after reply shortcut = %v, want %v", got, want)
	}
}

func TestMessageFiltersKeepNarrowAndWideLayoutsCoherent(t *testing.T) {
	for _, tt := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "narrow", width: 36, height: 12},
		{name: "wide", width: 120, height: 24},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := newMockShellModel("example", config.Default())
			model.activeChannelState().messages = filterTestMessages("example")
			model.activeChannelState().messageFilters.toggle(messageFilterMentions)

			updated, _ := model.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			view := updated.View()

			if got, want := lineCount(view), tt.height; got != want {
				t.Fatalf("filtered view line count = %d, want %d:\n%s", got, want, view)
			}
			for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
				if got := lipglossWidth(line); got > tt.width {
					t.Fatalf("filtered line %d width = %d, want <= %d:\n%s", i+1, got, tt.width, view)
				}
			}
		})
	}
}

func TestReconnectPreservesChannelStateAndReportsFailureAndRepeatedRequests(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(1)
	if err := client.QueueReconnectError(errors.New("dial failed for oauth:secret-token")); err != nil {
		t.Fatalf("QueueReconnectError returned error: %v", err)
	}
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)
	alpha := model.channels.ensure("alpha")
	beta := model.channels.ensure("beta")

	alpha.messages = numberedMockMessages("alpha", 3)
	alpha.composerText = "alpha draft"
	alpha.replyTo = replyContextFromMessage(alpha.messages[1])
	alpha.scrollOffset = 1
	beforeReply := *alpha.replyTo
	beta.messages = numberedMockMessages("beta", 2)
	beta.composerText = "beta draft"
	beta.unread = 2

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("ctrl+r returned nil command, want reconnect command")
	}
	if !model.reconnectInFlight {
		t.Fatal("reconnectInFlight = false, want true")
	}
	if got, want := model.activeChannelState().status.Status, ConnectionReconnecting; got != want {
		t.Fatalf("status after first reconnect request = %q, want %q", got, want)
	}

	updated, repeatedCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(mockShellModel)
	if repeatedCmd != nil {
		t.Fatalf("repeated reconnect returned command %#v, want nil", repeatedCmd)
	}
	if !strings.Contains(model.activeChannelState().status.Detail, "already in progress") {
		t.Fatalf("repeated reconnect detail = %q, want in-progress feedback", model.activeChannelState().status.Detail)
	}
	if got, want := client.ReconnectCount(), 0; got != want {
		t.Fatalf("reconnect count before command executes = %d, want %d", got, want)
	}

	updated, _ = model.Update(cmd().(reconnectCompletedMsg))
	model = updated.(mockShellModel)
	if model.reconnectInFlight {
		t.Fatal("reconnectInFlight after completion = true, want false")
	}
	if got, want := model.activeChannelState().status.Status, ConnectionFailed; got != want {
		t.Fatalf("status after failed reconnect = %q, want %q", got, want)
	}
	detail := model.activeChannelState().status.Detail
	if !strings.Contains(detail, "retry with ctrl+r") {
		t.Fatalf("failed reconnect detail = %q, want retry guidance", detail)
	}
	if strings.Contains(detail, "oauth:secret-token") {
		t.Fatalf("failed reconnect detail leaked token: %q", detail)
	}

	assertReconnectPreservedChannelState(t, model, beforeReply)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("second ctrl+r returned nil command, want reconnect command")
	}
	updated, _ = model.Update(cmd().(reconnectCompletedMsg))
	model = updated.(mockShellModel)
	if got, want := client.ReconnectCount(), 2; got != want {
		t.Fatalf("reconnect count after retry = %d, want %d", got, want)
	}
	if got, want := model.activeChannelState().status.Status, ConnectionConnecting; got != want {
		t.Fatalf("status after successful reconnect = %q, want %q", got, want)
	}
	if !strings.Contains(model.activeChannelState().status.Detail, "waiting for Twitch IRC") {
		t.Fatalf("status detail after successful reconnect = %q, want waiting feedback", model.activeChannelState().status.Detail)
	}
	assertReconnectPreservedChannelState(t, model, beforeReply)
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

func raidSystemEventMessage(channel string) twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:            "raid-1",
		Channel:       channel,
		Timestamp:     time.Date(2026, 7, 2, 20, 0, 11, 0, time.UTC),
		DisplayName:   "raider",
		Text:          "15 raiders from raider joined",
		Type:          twitch.MessageTypeNotice,
		SystemEventID: "raid",
	}
}

func highThroughputBurstMessages(channel string, count int, startedAt time.Time) []twitch.ChatMessage {
	messages := make([]twitch.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		text := fmt.Sprintf("burst message %02d", i)
		message := twitch.ChatMessage{
			ID:          fmt.Sprintf("burst-%02d", i),
			Channel:     channel,
			ChannelID:   "room-42",
			Timestamp:   startedAt.Add(time.Duration(i) * time.Millisecond),
			AuthorID:    fmt.Sprintf("user-%02d", i%6),
			AuthorLogin: fmt.Sprintf("viewer_%02d", i%6),
			DisplayName: fmt.Sprintf("viewer_%02d", i%6),
			AuthorColor: "#00d1ff",
			Text:        text,
			Type:        twitch.MessageTypeChat,
		}

		switch i % 8 {
		case 0:
			message.Text = text + " fallback @twi_bot Kappa 😀"
			message.Badges = []twitch.Badge{{SetID: "moderator", ID: "1"}}
			message.Fragments = []twitch.MessageFragment{
				{Type: twitch.FragmentText, Text: text + " fallback "},
				{Type: twitch.FragmentMention, Text: "@twi_bot"},
				{Type: twitch.FragmentText, Text: " "},
				{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: "25"}},
				{Type: twitch.FragmentText, Text: " "},
				{Type: twitch.FragmentEmoji, Text: "😀"},
			}
		case 1:
			message.Text = text + " reply wraps around the viewport while keeping the parent summary visible"
			message.Reply = &twitch.Reply{
				ParentMessageID: "parent-1",
				ParentAuthor:    "origin",
				ParentText:      "parent text also wraps",
			}
		case 2:
			message.Text = text + " action waves at chat"
			message.Type = twitch.MessageTypeAction
		case 3:
			message.Text = text + " notice reconnecting without credentials"
			message.Type = twitch.MessageTypeNotice
		case 4:
			message.Text = text + " deleted fallback"
			message.Deleted = true
		case 5:
			message.Text = text + " Kappa wraps with fallback token"
			message.Emotes = []twitch.Emote{{ID: "25", Name: "Kappa", Start: 17, End: 21}}
		case 6:
			message.Text = text + " system error stays readable"
			message.Type = twitch.MessageTypeSystem
			message.RawTags = map[string]string{"level": "error"}
		default:
			message.Text = text + " unicode keeps 👨‍👩‍👧‍👦 and ありがとう together @viewer"
		}
		messages = append(messages, message)
	}
	return messages
}

func assertHighThroughputFallbackRendering(t *testing.T, model mockShellModel, messages []twitch.ChatMessage) {
	t.Helper()

	seenKinds := map[render.FragmentKind]bool{}
	var fallbackText strings.Builder
	for _, width := range []int{36, 72} {
		options := model.renderOptions(width)
		for _, message := range messages {
			rows := render.Rows(message, options)
			if len(rows) == 0 {
				t.Fatalf("render rows for %s at width %d are empty", message.ID, width)
			}
			for _, row := range rows {
				if got := row.Width(); got > width {
					t.Fatalf("row width for %s at width %d = %d, want <= %d: %#v", message.ID, width, got, width, row.Plain())
				}
				if terminal := row.TerminalString(); strings.Contains(terminal, "\x1b_G") {
					t.Fatalf("fallback render for %s emitted Kitty image command: %q", message.ID, terminal)
				}
				fallbackText.WriteString(row.Plain())
				fallbackText.WriteByte('\n')
				for _, fragment := range row.Fragments {
					seenKinds[fragment.Kind] = true
					if fragment.ImageReady {
						t.Fatalf("fallback render for %s has prepared image fragment: %#v", message.ID, fragment)
					}
				}
			}
		}
	}

	for _, kind := range []render.FragmentKind{
		render.FragmentBadge,
		render.FragmentMention,
		render.FragmentEmoteFallback,
		render.FragmentEmojiFallback,
		render.FragmentReply,
		render.FragmentAction,
		render.FragmentNotice,
		render.FragmentDeleted,
	} {
		if !seenKinds[kind] {
			t.Fatalf("fallback stress did not render fragment kind %q; saw %#v", kind, seenKinds)
		}
	}
	plain := fallbackText.String()
	for _, want := range []string{"Kappa", "😀", "reply to origin", "[notice]", "[message deleted]", "ありがとう"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("fallback stress output missing %q:\n%s", want, plain)
		}
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

func emoteOnlyAssetMessage(id, emoteID string) twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:          id,
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "asset "},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: assets.KindTwitchEmote, ID: emoteID}},
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

func assertReconnectPreservedChannelState(t *testing.T, model mockShellModel, beforeReply composerReplyContext) {
	t.Helper()

	alpha := model.channels.states[channelKey("alpha")]
	beta := model.channels.states[channelKey("beta")]
	if alpha == nil || beta == nil {
		t.Fatalf("channel states = %#v, want alpha and beta", model.channels.channelNames())
	}
	if got, want := len(alpha.messages), 3; got != want {
		t.Fatalf("alpha history length = %d, want %d", got, want)
	}
	if got, want := len(beta.messages), 2; got != want {
		t.Fatalf("beta history length = %d, want %d", got, want)
	}
	if got, want := alpha.composerText, "alpha draft"; got != want {
		t.Fatalf("alpha composerText = %q, want %q", got, want)
	}
	if alpha.replyTo == nil || *alpha.replyTo != beforeReply {
		t.Fatalf("alpha replyTo = %#v, want %#v", alpha.replyTo, beforeReply)
	}
	if got, want := alpha.scrollOffset, 1; got != want {
		t.Fatalf("alpha scrollOffset = %d, want %d", got, want)
	}
	if got, want := beta.composerText, "beta draft"; got != want {
		t.Fatalf("beta composerText = %q, want %q", got, want)
	}
	if got, want := beta.unread, 2; got != want {
		t.Fatalf("beta unread = %d, want %d", got, want)
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

func filterTestMessages(channel string) []twitch.ChatMessage {
	startedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	return []twitch.ChatMessage{
		{
			ID:          "ordinary-1",
			Channel:     channel,
			Timestamp:   startedAt,
			AuthorLogin: "viewer",
			DisplayName: "viewer",
			Text:        "ordinary viewer chatter",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "mention-1",
			Channel:     channel,
			Timestamp:   startedAt.Add(time.Second),
			AuthorLogin: "viewer",
			DisplayName: "viewer",
			Text:        "hello @twi_bot",
			Fragments: []twitch.MessageFragment{
				{Type: twitch.FragmentText, Text: "hello "},
				{Type: twitch.FragmentMention, Text: "@twi_bot"},
			},
			Type: twitch.MessageTypeChat,
		},
		{
			ID:          "role-1",
			Channel:     channel,
			Timestamp:   startedAt.Add(2 * time.Second),
			AuthorLogin: "vip_guest",
			DisplayName: "vip_guest",
			Badges:      []twitch.Badge{{SetID: "vip", ID: "1"}},
			Text:        "vip update",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:        "notice-1",
			Channel:   channel,
			Timestamp: startedAt.Add(3 * time.Second),
			Text:      "slow mode enabled",
			Type:      twitch.MessageTypeNotice,
		},
		{
			ID:        "error-1",
			Channel:   channel,
			Timestamp: startedAt.Add(4 * time.Second),
			Text:      "connection failed",
			Type:      twitch.MessageTypeSystem,
			RawTags:   map[string]string{"level": "error"},
		},
	}
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
	mu              sync.Mutex
	calls           int
	last            []assets.Request
	fail            bool
	path            string
	sourceURL       string
	payloadIdentity string
	mediaType       string
	fetchedAt       time.Time
}

func (f *appFakeAssetResolver) Resolve(_ context.Context, request assets.Request) assets.Event {
	f.mu.Lock()
	f.calls++
	f.last = append(f.last, request)
	fail := f.fail
	path := f.path
	sourceURL := f.sourceURL
	payloadIdentity := f.payloadIdentity
	mediaType := f.mediaType
	fetchedAt := f.fetchedAt
	f.mu.Unlock()

	if fail {
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
			Key:             storage.AssetKey{Kind: request.Ref.Kind, ID: request.Ref.ID},
			Path:            firstNonEmptyString(path, "fake.png"),
			SourceURL:       sourceURL,
			PayloadIdentity: payloadIdentity,
			MediaType:       firstNonEmptyString(mediaType, "image/png"),
			WidthCells:      request.WidthCells,
			HeightCells:     request.HeightCells,
			FetchedAt:       fetchedAt,
		},
		At: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC),
	}
}

type appFakeImageRenderer struct {
	mu      sync.Mutex
	calls   int
	cells   map[render.ImageCellKey]string
	records []storage.AssetRecord
	err     error
}

func (f *appFakeImageRenderer) RenderImage(_ context.Context, asset storage.AssetRecord, spec render.ImageSpec) (render.ImageCell, error) {
	f.mu.Lock()
	f.calls++
	f.records = append(f.records, asset)
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return render.ImageCell{}, err
	}
	key, _ := render.ImageCellKeyForRefInChannel(spec.Ref, spec.ChannelID, spec.Channel)
	text := f.cells[key]
	if text == "" {
		key, _ := render.ImageCellKeyForRef(spec.Ref)
		text = f.cells[key]
	}
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

type appFakeImagePreparer struct {
	mu      sync.Mutex
	calls   int
	records []storage.AssetRecord
	specs   []render.ImageSpec
	err     error
}

func (f *appFakeImagePreparer) PrepareImage(_ context.Context, asset storage.AssetRecord, spec render.ImageSpec) (storage.AssetRecord, error) {
	f.mu.Lock()
	f.calls++
	f.records = append(f.records, asset)
	f.specs = append(f.specs, spec)
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return storage.AssetRecord{}, err
	}
	asset.Path = "prepared.png"
	asset.MediaType = "image/png"
	asset.WidthCells = spec.WidthCells
	asset.HeightCells = spec.HeightCells
	return asset, nil
}

type blockingAssetResolver struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	deadlines int
	entered   chan struct{}
	release   <-chan struct{}
}

func (r *blockingAssetResolver) Resolve(ctx context.Context, request assets.Request) assets.Event {
	r.mu.Lock()
	r.calls++
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	if _, ok := ctx.Deadline(); ok {
		r.deadlines++
	}
	r.mu.Unlock()

	select {
	case r.entered <- struct{}{}:
	case <-ctx.Done():
	}
	select {
	case <-r.release:
	case <-ctx.Done():
	}

	r.mu.Lock()
	r.active--
	r.mu.Unlock()

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

func (r *blockingAssetResolver) snapshot() (calls, maxActive, deadlines int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.maxActive, r.deadlines
}

type deadlineBlockingAssetResolver struct {
	mu       sync.Mutex
	deadline bool
}

func (r *deadlineBlockingAssetResolver) Resolve(ctx context.Context, request assets.Request) assets.Event {
	if _, ok := ctx.Deadline(); ok {
		r.mu.Lock()
		r.deadline = true
		r.mu.Unlock()
	}
	<-ctx.Done()
	return assets.Event{
		Kind:      assets.EventCanceled,
		RequestID: request.ID,
		Ref:       request.Ref,
		Err:       ctx.Err(),
		At:        time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC),
	}
}

func (r *deadlineBlockingAssetResolver) sawDeadline() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadline
}

type deadlineTrackingImagePreparer struct {
	mu        sync.Mutex
	deadlines int
}

func (p *deadlineTrackingImagePreparer) PrepareImage(ctx context.Context, asset storage.AssetRecord, spec render.ImageSpec) (storage.AssetRecord, error) {
	p.mu.Lock()
	if _, ok := ctx.Deadline(); ok {
		p.deadlines++
	}
	p.mu.Unlock()
	asset.Path = "prepared.png"
	asset.MediaType = "image/png"
	asset.WidthCells = spec.WidthCells
	asset.HeightCells = spec.HeightCells
	return asset, nil
}

func (p *deadlineTrackingImagePreparer) deadlineCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deadlines
}

type deadlineTrackingImageRenderer struct {
	mu        sync.Mutex
	deadlines int
}

func (r *deadlineTrackingImageRenderer) RenderImage(ctx context.Context, _ storage.AssetRecord, spec render.ImageSpec) (render.ImageCell, error) {
	r.mu.Lock()
	if _, ok := ctx.Deadline(); ok {
		r.deadlines++
	}
	r.mu.Unlock()
	return render.ImageCell{Text: fitLine(spec.Fallback, spec.WidthCells), WidthCells: spec.WidthCells}, nil
}

func (r *deadlineTrackingImageRenderer) deadlineCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadlines
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
		spec: render.ImageSpec{Ref: ref, Channel: "example", WidthCells: width, HeightCells: 1},
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
	}
	for _, kind := range []string{assets.KindAvatar, assets.KindBadge, assets.KindTwitchEmote, assets.KindEmoji} {
		if seen[kind] {
			kinds = append(kinds, kind)
			delete(seen, kind)
		}
	}
	for kind := range seen {
		kinds = append(kinds, kind)
	}
	return kinds
}

func lipglossWidth(value string) int {
	return lipgloss.Width(value)
}

// chatOnlyView isolates the chat pane from the full dashboard View(). Tests
// asserting on emote/asset fallback text (e.g. "Kappa") use this instead of
// the whole View() so they aren't satisfied by the unrelated persistent
// emotes quick-select row, which also lists well-known emote names.
func chatOnlyView(m mockShellModel) string {
	return m.chatView(m.layout())
}

func TestScheduleFrameTickRunsContinuouslyWhenAnimationEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)

	cmd := model.scheduleFrameTick()
	if cmd == nil {
		t.Fatal("scheduleFrameTick() = nil, want a tea.Cmd")
	}
	if !model.frameTickScheduled {
		t.Fatal("frameTickScheduled = false after scheduling, want true")
	}
	if second := model.scheduleFrameTick(); second != nil {
		t.Fatal("scheduleFrameTick() while already scheduled returned non-nil, want nil")
	}
}

func TestScheduleFrameTickDisabledWhenAnimationOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)

	if cmd := model.scheduleFrameTick(); cmd != nil {
		t.Fatal("scheduleFrameTick() with animation off returned non-nil, want nil")
	}
}

func TestChannelSwitchTriggersSceneFlash(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22

	if model.sceneFlashActive() {
		t.Fatal("sceneFlashActive() = true before any channel switch, want false")
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	model = updated.(mockShellModel)
	if !model.sceneFlashActive() {
		t.Fatal("sceneFlashActive() = false immediately after channel switch, want true")
	}

	model.sceneFlashUntil = time.Now().Add(-time.Millisecond)
	if model.sceneFlashActive() {
		t.Fatal("sceneFlashActive() = true after deadline elapsed, want false")
	}
}

func TestChannelSwitchDoesNotFlashWhenAnimationOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	model = updated.(mockShellModel)
	if model.sceneFlashActive() {
		t.Fatal("sceneFlashActive() = true with animation off, want false")
	}
}

func TestCommandPaletteRevealProgressesThenSettles(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	if !model.palette.open {
		t.Fatal("palette open = false, want true")
	}

	partial := model.View()
	if strings.Contains(partial, "Focus composer") {
		t.Fatalf("palette view fully revealed on first frame, want a partial typewriter reveal:\n%s", partial)
	}

	now := time.Now()
	for i := 0; i < 200 && !model.paletteRevealSeq.Done(); i++ {
		now = now.Add(50 * time.Millisecond)
		updated, _ = model.Update(animation.FrameMsg{At: now})
		model = updated.(mockShellModel)
	}
	settled := model.View()
	if !strings.Contains(settled, "Focus composer") {
		t.Fatalf("palette view never fully revealed after ticking:\n%s", settled)
	}
}

func TestCommandPaletteFullyRevealedImmediatelyWhenAnimationOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 18

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(mockShellModel)
	if !strings.Contains(model.View(), "Focus composer") {
		t.Fatalf("palette view not fully revealed with animation off:\n%s", model.View())
	}
}

func TestSplashCoversViewUntilDeadlineOrKeypress(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.splashUntil = time.Now().Add(splashDuration)

	view := model.View()
	if !strings.Contains(view, "twi") {
		t.Fatalf("splash view missing wordmark:\n%s", view)
	}
	if strings.Contains(view, "focus=chat") {
		t.Fatalf("splash view leaked normal dashboard content:\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updated.(mockShellModel)
	if model.splashActive() {
		t.Fatal("splashActive() = true after keypress, want false")
	}
	if !strings.Contains(model.View(), "focus=chat") {
		t.Fatalf("view after splash skip missing normal dashboard content:\n%s", model.View())
	}
}

func TestSplashClearsAfterDeadlineWithoutKeypress(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.splashUntil = time.Now().Add(-time.Millisecond)

	if model.splashActive() {
		t.Fatal("splashActive() = true after deadline elapsed, want false")
	}
	if !strings.Contains(model.View(), "focus=chat") {
		t.Fatalf("view after splash deadline missing normal dashboard content:\n%s", model.View())
	}
}

func TestCycleFocusIncludesEmotesRow(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	if model.focus != mockFocusChat {
		t.Fatalf("initial focus = %v, want chat", model.focus)
	}
	model.cycleFocus()
	if model.focus != mockFocusComposer {
		t.Fatalf("focus after 1 cycle = %v, want composer", model.focus)
	}
	model.cycleFocus()
	if model.focus != mockFocusEmotes {
		t.Fatalf("focus after 2 cycles = %v, want emotes", model.focus)
	}
	model.cycleFocus()
	if model.focus != mockFocusChat {
		t.Fatalf("focus after 3 cycles = %v, want chat", model.focus)
	}
}

func TestEmotesViewRendersQuickSelectStripAndHighlightsSelection(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22
	model.focus = mockFocusEmotes

	view := model.View()
	if !strings.Contains(view, "Kappa") || !strings.Contains(view, "PogChamp") {
		t.Fatalf("emotes row missing sample entries:\n%s", view)
	}
	if !strings.Contains(view, "[Kappa]") {
		t.Fatalf("emotes row missing highlighted selection for first entry:\n%s", view)
	}
}

func TestEmotesFocusLeftRightMovesSelectionAndWraps(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22
	model.focus = mockFocusEmotes

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(mockShellModel)
	if model.emoteSelected != 1 {
		t.Fatalf("emoteSelected after right = %d, want 1", model.emoteSelected)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(mockShellModel)
	entries := model.activeEmoteEntries()
	if model.emoteSelected != len(entries)-1 {
		t.Fatalf("emoteSelected after wrapping left = %d, want %d", model.emoteSelected, len(entries)-1)
	}
}

func TestEmotesFocusEnterInsertsSelectedEmoteIntoComposer(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22
	model.focus = mockFocusEmotes

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(mockShellModel)
	entries := model.activeEmoteEntries()
	want := entries[1].Name

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if got := model.activeChannelState().composerText; got != want+" " {
		t.Fatalf("composer text after emote insert = %q, want %q", got, want+" ")
	}
}

func TestScheduleBroadcasterIDLookupResolvesAndCaches(t *testing.T) {
	resolver := &appFakeAvatarResolver{results: []assets.AvatarResult{
		{UserID: "100", UserLogin: "alpha", Found: true},
	}}
	model := newLiveShellModelWithClockAndOptions("alpha", config.Default(), NewFakeChatClient(1), nil, ClientOptions{
		AvatarResolver: resolver,
	})

	cmd := model.scheduleBroadcasterIDLookup()
	if cmd == nil {
		t.Fatal("scheduleBroadcasterIDLookup() = nil, want a command")
	}
	if !model.channels.ensure("alpha").broadcasterIDRequested {
		t.Fatal("broadcasterIDRequested = false after scheduling, want true")
	}
	msg := cmd().(broadcasterIDResolvedMsg)
	model.applyBroadcasterIDResult(msg)
	if got := model.channels.ensure("alpha").broadcasterID; got != "100" {
		t.Fatalf("broadcasterID = %q, want 100", got)
	}

	if cmd := model.scheduleBroadcasterIDLookup(); cmd != nil {
		t.Fatal("scheduleBroadcasterIDLookup() after resolution returned non-nil, want nil (already resolved)")
	}
}

func TestScheduleBroadcasterIDLookupNoAvatarResolver(t *testing.T) {
	model := newLiveShellModelWithClockAndOptions("alpha", config.Default(), NewFakeChatClient(1), nil, ClientOptions{})
	if cmd := model.scheduleBroadcasterIDLookup(); cmd != nil {
		t.Fatal("scheduleBroadcasterIDLookup() without AvatarResolver returned non-nil, want nil")
	}
}

type fakeEmoteListerForApp struct {
	global []twitch.EmoteMetadata
}

func (f *fakeEmoteListerForApp) GetGlobalEmotes(context.Context) ([]twitch.EmoteMetadata, error) {
	return f.global, nil
}

func (f *fakeEmoteListerForApp) GetChannelEmotes(context.Context, string) ([]twitch.EmoteMetadata, error) {
	return nil, nil
}

func TestScheduleEmoteIndexLookupResolvesAndSkipsWhenCached(t *testing.T) {
	lister := &fakeEmoteListerForApp{global: []twitch.EmoteMetadata{{ID: "1", Name: "Kappa"}}}
	model := newLiveShellModelWithClockAndOptions("alpha", config.Default(), NewFakeChatClient(1), nil, ClientOptions{
		EmoteIndex: assets.NewEmoteIndex(lister),
	})

	cmd := model.scheduleEmoteIndexLookup()
	if cmd == nil {
		t.Fatal("scheduleEmoteIndexLookup() = nil, want a command")
	}
	msg := cmd().(emoteIndexResolvedMsg)
	model.applyEmoteIndexResult(msg)
	entries := model.activeEmoteEntries()
	if len(entries) != 1 || entries[0].Name != "Kappa" {
		t.Fatalf("activeEmoteEntries() = %#v, want [Kappa]", entries)
	}

	if cmd := model.scheduleEmoteIndexLookup(); cmd != nil {
		t.Fatal("scheduleEmoteIndexLookup() after resolution returned non-nil, want nil (already cached)")
	}
}

func TestScheduleEmoteIndexLookupNoIndex(t *testing.T) {
	model := newLiveShellModelWithClockAndOptions("alpha", config.Default(), NewFakeChatClient(1), nil, ClientOptions{})
	if cmd := model.scheduleEmoteIndexLookup(); cmd != nil {
		t.Fatal("scheduleEmoteIndexLookup() without EmoteIndex returned non-nil, want nil")
	}
	if entries := model.activeEmoteEntries(); entries != nil {
		t.Fatalf("activeEmoteEntries() = %#v, want nil before resolution", entries)
	}
}

func TestApplyStreamStatusResultsUpdatesLiveOfflineAndViewers(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)

	startedAt := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	model.applyStreamStatusResults([]twitch.StreamInfo{
		{UserLogin: "alpha", Live: true, StartedAt: startedAt, ViewerCount: 99},
		{UserLogin: "beta", Live: false},
	})

	alpha := model.channels.ensure("alpha")
	if !alpha.live || alpha.viewerCount != 99 || !alpha.liveSince.Equal(startedAt) {
		t.Fatalf("alpha state = %+v, want live with viewer_count 99 and matching liveSince", alpha)
	}
	beta := model.channels.ensure("beta")
	if beta.live || !beta.liveSince.IsZero() {
		t.Fatalf("beta state = %+v, want offline with zero liveSince", beta)
	}
}

func TestStatusLineShowsLiveOrOfflineAtSufficientWidth(t *testing.T) {
	cfg := config.Default()
	model := newMockShellModel("alpha", cfg)
	model.channels.ensure("alpha").live = false

	offline := model.statusLine(100)
	if !strings.Contains(offline, "OFFLINE") {
		t.Fatalf("status line missing OFFLINE badge:\n%s", offline)
	}

	model.channels.ensure("alpha").live = true
	model.channels.ensure("alpha").liveSince = time.Time{}
	live := model.statusLine(100)
	if !strings.Contains(live, "LIVE") {
		t.Fatalf("status line missing LIVE badge:\n%s", live)
	}
}

func TestStatusLineOmitsMetricsWhenNarrow(t *testing.T) {
	cfg := config.Default()
	model := newMockShellModel("alpha", cfg)
	narrow := model.statusLine(40)
	if strings.Contains(narrow, "OFFLINE") || strings.Contains(narrow, "LIVE") {
		t.Fatalf("narrow status line unexpectedly includes metrics:\n%s", narrow)
	}
}

func TestFormatElapsedRendersHoursOnlyWhenPresent(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{90 * time.Second, "1:30"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{-time.Second, "0:00"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Fatalf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestUpdateFrameMsgAdvancesAndReschedules(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)
	model.frameTickScheduled = true

	now := time.Now()
	updated, cmd := model.Update(animation.FrameMsg{At: now})
	model = updated.(mockShellModel)

	if model.lastFrameAt != now {
		t.Fatalf("lastFrameAt = %v, want %v", model.lastFrameAt, now)
	}
	if cmd == nil {
		t.Fatal("Update(FrameMsg) returned nil cmd, want a re-scheduled frame tick")
	}
	if !model.frameTickScheduled {
		t.Fatal("frameTickScheduled = false after FrameMsg, want true")
	}
}
