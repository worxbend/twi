package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func clearTestModel(t *testing.T) mockShellModel {
	t.Helper()
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModelWithClock("example", cfg, &appFakeClock{now: time.Now()})
	state := model.activeChannelState()
	state.messages = []twitch.ChatMessage{
		{ID: "a", Channel: "example", AuthorLogin: "alice", Text: "hello", Type: twitch.MessageTypeChat},
		{ID: "b", Channel: "example", AuthorLogin: "bob", Text: "hi", Type: twitch.MessageTypeChat},
	}
	return model
}

func pressKey(model mockShellModel, key tea.KeyMsg) mockShellModel {
	updated, _ := model.Update(key)
	return updated.(mockShellModel)
}

// TestClearChatAsksBeforeDiscardingHistory guards a one-keystroke,
// irreversible loss of the whole channel backlog, on a tool that is often
// running during a live broadcast.
func TestClearChatAsksBeforeDiscardingHistory(t *testing.T) {
	model := clearTestModel(t)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlL})

	if got := len(model.activeChannelState().messages); got != 2 {
		t.Fatalf("a single ctrl+L discarded %d messages; it must ask first", 2-got)
	}
	if !model.pendingClearChat {
		t.Fatal("ctrl+L did not arm the confirmation")
	}
	if status := ansi.Strip(model.statusLine(120)); !strings.Contains(status, "ctrl+L again") {
		t.Fatalf("status line = %q, want the confirmation prompt; an invisible armed guard is worse than none", status)
	}
}

func TestClearChatProceedsOnSecondPress(t *testing.T) {
	model := clearTestModel(t)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlL})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlL})

	if got := len(model.activeChannelState().messages); got != 0 {
		t.Fatalf("messages remaining = %d, want 0 after a confirmed clear", got)
	}
	if model.pendingClearChat {
		t.Fatal("the confirmation stayed armed after the clear ran")
	}
}

// TestClearChatCancelsOnAnyOtherKey keeps a stray press from leaving a live
// trap that an unrelated keystroke springs later.
func TestClearChatCancelsOnAnyOtherKey(t *testing.T) {
	model := clearTestModel(t)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlL})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	if model.pendingClearChat {
		t.Fatal("an unrelated key left the clear confirmation armed")
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlL})
	if got := len(model.activeChannelState().messages); got != 2 {
		t.Fatalf("messages = %d, want 2; a cancelled confirmation must not carry over", got)
	}
	if status := ansi.Strip(model.statusLine(120)); !strings.Contains(status, "ctrl+L again") {
		t.Fatal("the fresh ctrl+L did not re-arm the confirmation")
	}
}
