package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/twitch"
)

// displayTestModel returns a model with a deterministic three-message
// conversation: two consecutive messages from one author (so grouping has
// something to collapse) followed by a different author.
func displayTestModel(t *testing.T) shellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")

	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 20
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.Local)
	messages := []twitch.ChatMessage{
		{
			ID: "1", Channel: "alpha", AuthorLogin: "alice_l", DisplayName: "Alice_L",
			Timestamp: now, Type: twitch.MessageTypeChat, Text: "hey everyone",
			Badges: []twitch.Badge{{SetID: "moderator", ID: "1"}},
		},
		{
			ID: "2", Channel: "alpha", AuthorLogin: "alice_l", DisplayName: "Alice_L",
			Timestamp: now, Type: twitch.MessageTypeChat, Text: "still me talking",
		},
		{
			ID: "3", Channel: "alpha", AuthorLogin: "bobby", DisplayName: "Bobby",
			Timestamp: now, Type: twitch.MessageTypeChat, Text: "hi there",
		},
	}
	state := model.activeChannelState()
	state.messages = messages
	state.activeOrder = nil
	state.activeMessages = map[string]twitch.ChatMessage{}
	for _, message := range messages {
		state.roster.observeMessage(message)
	}
	return model
}

func chatText(model shellModel) string {
	return ansi.Strip(strings.Join(model.chatRows(model.layout()), "\n"))
}

func TestGroupedLayoutNamesAuthorOncePerRun(t *testing.T) {
	model := displayTestModel(t)
	model.display.messageLayout = render.LayoutGrouped

	text := chatText(model)
	// Two consecutive Alice messages share one header; Bobby starts a new one.
	if got := strings.Count(text, "Alice_L"); got != 1 {
		t.Fatalf("grouped chat names Alice_L %d times, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "Bobby") {
		t.Fatalf("grouped chat missing the second author:\n%s", text)
	}
	for _, want := range []string{"hey everyone", "still me talking", "hi there"} {
		if !strings.Contains(text, want) {
			t.Fatalf("grouped chat missing message %q:\n%s", want, text)
		}
	}
}

func TestInlineLayoutNamesAuthorOnEveryMessage(t *testing.T) {
	model := displayTestModel(t)
	model.display.messageLayout = render.LayoutInline

	if got := strings.Count(chatText(model), "Alice_L"); got != 2 {
		t.Fatalf("inline chat names Alice_L %d times, want 2 (one per message)", got)
	}
}

func TestCycleMessageLayoutRotatesAndPersists(t *testing.T) {
	model := displayTestModel(t)
	if model.display.messageLayout != render.LayoutInline {
		t.Fatalf("default layout = %q, want inline", model.display.messageLayout)
	}

	for _, want := range []render.LayoutMode{render.LayoutGrouped, render.LayoutCompact, render.LayoutInline} {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
		model = updated.(shellModel)
		if model.display.messageLayout != want {
			t.Fatalf("layout after ctrl+g = %q, want %q", model.display.messageLayout, want)
		}
	}

	data, err := os.ReadFile(model.effectiveConfig.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", model.effectiveConfig.Path, err)
	}
	if !strings.Contains(string(data), `message_layout = "inline"`) {
		t.Fatalf("config did not persist the layout choice:\n%s", data)
	}
}

func TestCycleBadgeModeRotatesAndChangesRendering(t *testing.T) {
	model := displayTestModel(t)

	// glyph (default) -> text -> off -> glyph
	if got := chatText(model); !strings.Contains(got, "⚔") {
		t.Fatalf("default badge rendering missing glyph:\n%s", got)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	model = updated.(shellModel)
	if model.display.badgeMode != render.BadgeModeText {
		t.Fatalf("badge mode after one ctrl+b = %q, want text", model.display.badgeMode)
	}
	if got := chatText(model); !strings.Contains(got, "[mod") || strings.Contains(got, "⚔") {
		t.Fatalf("text badge mode rendering wrong:\n%s", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	model = updated.(shellModel)
	if model.display.badgeMode != render.BadgeModeOff {
		t.Fatalf("badge mode after two ctrl+b = %q, want off", model.display.badgeMode)
	}
	if got := chatText(model); strings.Contains(got, "[mod") || strings.Contains(got, "⚔") {
		t.Fatalf("badges still rendered when off:\n%s", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	model = updated.(shellModel)
	if model.display.badgeMode != render.BadgeModeGlyph {
		t.Fatalf("badge mode after three ctrl+b = %q, want glyph again", model.display.badgeMode)
	}
}

func TestToggleFullUsernameShowsLogin(t *testing.T) {
	model := displayTestModel(t)
	state := model.activeChannelState()
	state.messages[0].DisplayName = "アリス"
	state.messages[0].AuthorLogin = "alice_l"

	if got := chatText(model); strings.Contains(got, "(alice_l)") {
		t.Fatalf("login shown before enabling full usernames:\n%s", got)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	model = updated.(shellModel)
	if !model.display.fullUsername {
		t.Fatal("ctrl+n did not enable full usernames")
	}
	if got := chatText(model); !strings.Contains(got, "アリス (alice_l)") {
		t.Fatalf("full username not rendered:\n%s", got)
	}
}

func TestToggleEmoteHighlightFlipsRenderOption(t *testing.T) {
	model := displayTestModel(t)
	if !model.display.highlightEmotes {
		t.Fatal("emote highlighting should default on")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(shellModel)
	if model.display.highlightEmotes {
		t.Fatal("ctrl+y did not disable emote highlighting")
	}
	if model.renderOptions(60).HighlightEmotes {
		t.Fatal("render options still request emote highlighting after the toggle")
	}
}

func TestDisplayTogglesSurviveAFailedConfigWrite(t *testing.T) {
	model := displayTestModel(t)
	// Nest the config under a regular file so the write genuinely fails:
	// a merely missing directory is created on demand. An unwritable path
	// must not roll back the change the user just made.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", blocker, err)
	}
	model.effectiveConfig.Path = filepath.Join(blocker, "config.toml")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = updated.(shellModel)
	if model.display.messageLayout != render.LayoutGrouped {
		t.Fatalf("layout = %q after a failed save, want the change applied anyway", model.display.messageLayout)
	}
	if feedback := model.activeChannelState().sendFeedback; !strings.Contains(feedback, "not saved") {
		t.Fatalf("send feedback = %q, want it to report the failed save", feedback)
	}
}

func TestMessageGroupSurfaceAndRailFollowAuthorColor(t *testing.T) {
	forceColorProfile(t)
	model := displayTestModel(t)

	alice := chatRowBlock{message: model.activeChannelState().messages[0]}
	bobby := chatRowBlock{message: model.activeChannelState().messages[2]}

	// Two different authors at the same stripe parity must still be
	// distinguishable, which is the whole point of tinting by identity.
	if model.messageGroupBackground(alice, 0) == model.messageGroupBackground(bobby, 0) {
		t.Fatal("different authors share a group background at the same stripe parity")
	}
	if model.messageRailColor(alice, 0) == model.messageRailColor(bobby, 0) {
		t.Fatal("different authors share a rail color")
	}
	// The same author is stable across frames and stripe parity changes hue
	// only slightly, so a user's block stays recognizable.
	if model.messageRailColor(alice, 0) != model.messageRailColor(alice, 1) {
		t.Fatal("one author's rail color changed with stripe parity")
	}
}

func TestNoticeRowsKeepNeutralAccentTreatment(t *testing.T) {
	forceColorProfile(t)
	model := displayTestModel(t)
	notice := chatRowBlock{message: twitch.ChatMessage{
		ID: "n1", Channel: "alpha", Type: twitch.MessageTypeNotice, Text: "system notice",
	}}

	// A notice has no real author, so it must not be tinted by a fabricated
	// identity color.
	if got, want := model.messageGroupBackground(notice, 0), model.theme.Background; got != want {
		t.Fatalf("notice background = %q, want the untinted theme background %q", got, want)
	}
}

// Reveal frames are rendered when a message arrives, before the grouping
// pass that runs at draw time. Grouped layout must therefore predict
// continuation correctly, or an animating message repeats the author header
// that the message above it already shows.
func TestGroupedLayoutDoesNotRepeatHeaderWhileAnimating(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	cfg.Features.MessageLayout = "grouped"
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")

	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 20
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.Local)
	state := model.activeChannelState()
	state.messages = []twitch.ChatMessage{{
		ID: "1", Channel: "alpha", AuthorLogin: "alice", DisplayName: "Alice",
		Timestamp: now, Type: twitch.MessageTypeChat, Text: "first message",
	}}
	state.activeOrder = nil
	state.activeMessages = map[string]twitch.ChatMessage{}

	// Same author: the arriving message must not re-announce "Alice".
	updated, _ := model.Update(mockIncomingMessageMsg{message: twitch.ChatMessage{
		ID: "2", Channel: "alpha", AuthorLogin: "alice", DisplayName: "Alice",
		Timestamp: now, Type: twitch.MessageTypeChat, Text: "animated continuation",
	}})
	model = updated.(shellModel)
	if len(model.activeChannelState().activeOrder) == 0 {
		t.Fatal("test setup failed: expected an in-flight reveal")
	}
	if got := strings.Count(chatText(model), "Alice"); got != 1 {
		t.Fatalf("animating same-author message repeated the header %d times:\n%s", got, chatText(model))
	}

	// A different author does start a new group, header and all.
	updated, _ = model.Update(mockIncomingMessageMsg{message: twitch.ChatMessage{
		ID: "3", Channel: "alpha", AuthorLogin: "bob", DisplayName: "Bob",
		Timestamp: now, Type: twitch.MessageTypeChat, Text: "new author",
	}})
	model = updated.(shellModel)
	if got := strings.Count(chatText(model), "Alice"); got != 1 {
		t.Fatalf("Alice header count changed after a different author arrived:\n%s", chatText(model))
	}
}
