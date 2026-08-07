package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func composerTestModel(t *testing.T) shellModel {
	t.Helper()
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockModelWithClock("example", cfg, clock)
	model.focus = focusComposer
	return model
}

// TestComposerPasteCollapsesLineBreaks keeps a multi-line paste visible as the
// single line it will actually be sent as, rather than letting the transport
// silently rewrite it at send time.
func TestComposerPasteCollapsesLineBreaks(t *testing.T) {
	model := composerTestModel(t)
	model.insertComposerText("first line\nsecond line\r\nthird")

	got := model.activeChannelState().composerText
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("composerText = %q, still contains a line break", got)
	}
	if want := "first line second line  third"; got != want {
		t.Fatalf("composerText = %q, want %q", got, want)
	}
}

func TestComposerPasteCapsAtTwitchLimit(t *testing.T) {
	model := composerTestModel(t)
	model.insertComposerText(strings.Repeat("x", 700))

	state := model.activeChannelState()
	if runes := []rune(state.composerText); len(runes) != twitch.MaxChatMessageRunes {
		t.Fatalf("composer holds %d runes, want %d", len(runes), twitch.MaxChatMessageRunes)
	}
	if state.sendFeedback == "" {
		t.Fatal("no feedback shown after the composer capped a paste")
	}
}

func TestComposerPasteDropsControlCharacters(t *testing.T) {
	model := composerTestModel(t)
	model.insertComposerText("hel\x07lo\x1b[31m")

	if got, want := model.activeChannelState().composerText, "hello[31m"; got != want {
		t.Fatalf("composerText = %q, want %q", got, want)
	}
}

func TestComposerTypingStillWorks(t *testing.T) {
	model := composerTestModel(t)
	for _, r := range "gg wp" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(shellModel)
	}
	if got, want := model.activeChannelState().composerText, "gg wp"; got != want {
		t.Fatalf("composerText = %q, want %q", got, want)
	}
}

// TestComposerPasteArrivesThroughUpdate covers the real path: bracketed paste
// reaches the model as one KeyRunes burst, not as individual keystrokes.
func TestComposerPasteArrivesThroughUpdate(t *testing.T) {
	model := composerTestModel(t)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo")})
	model = updated.(shellModel)

	if got, want := model.activeChannelState().composerText, "one two"; got != want {
		t.Fatalf("composerText = %q, want %q", got, want)
	}
}
