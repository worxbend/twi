package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func chatModelWithMessages(t *testing.T, count int, mutate func(*config.Config)) mockShellModel {
	t.Helper()
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.ScrollbackLimit = 0
	if mutate != nil {
		mutate(&cfg)
	}
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(mockShellModel)

	state := model.activeChannelState()
	state.messages = nil
	base := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	for i := range count {
		state.messages = append(state.messages, twitch.ChatMessage{
			ID:          fmt.Sprintf("m-%d", i),
			Channel:     "example",
			AuthorLogin: fmt.Sprintf("chatter%d", i%7),
			DisplayName: fmt.Sprintf("Chatter%d", i%7),
			AuthorColor: "#9146ff",
			Text:        fmt.Sprintf("message %d with enough text that it wraps across the column", i),
			Timestamp:   base.Add(time.Duration(i) * time.Second),
			Type:        twitch.MessageTypeChat,
		})
	}
	return model
}

// TestVisibleChatRowsMatchesFullRenderThenSlice pins the windowed viewport
// path to the behavior it replaced. chatView used to style every row and slice
// afterwards; it now styles only the window, and the two must agree at every
// scroll position or the optimization is a rendering bug.
func TestVisibleChatRowsMatchesFullRenderThenSlice(t *testing.T) {
	for _, layoutMode := range []string{"inline", "grouped", "compact"} {
		t.Run(layoutMode, func(t *testing.T) {
			model := chatModelWithMessages(t, 60, func(cfg *config.Config) {
				cfg.Features.MessageLayout = layoutMode
			})
			layout := model.layout()
			state := model.activeChannelState()
			total := model.chatRowCount(layout)

			for _, offset := range []int{0, 1, 5, 17, total, total + 25} {
				state.scrollOffset = offset
				want := visibleRows(model.chatRows(layout), layout.chatContentHeight, offset)
				got := model.visibleChatRows(layout)
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("layout=%s offset=%d: windowed rows differ from full render\n got %d rows\nwant %d rows",
						layoutMode, offset, len(got), len(want))
				}
			}
		})
	}
}

// TestChatRowCountMatchesChatRows guards the count used for scroll clamping,
// which no longer renders the rows it is counting.
func TestChatRowCountMatchesChatRows(t *testing.T) {
	for _, count := range []int{0, 1, 40} {
		model := chatModelWithMessages(t, count, nil)
		layout := model.layout()
		if got, want := model.chatRowCount(layout), len(model.chatRows(layout)); got != want {
			t.Fatalf("messages=%d: chatRowCount = %d, want %d", count, got, want)
		}
	}
}

// TestChatRowCacheInvalidatesOnDeletion covers the one in-place mutation of a
// retained message: a moderator deleting it must not keep serving the cached
// pre-deletion rows.
func TestChatRowCacheInvalidatesOnDeletion(t *testing.T) {
	model := chatModelWithMessages(t, 5, nil)
	layout := model.layout()
	before := strings.Join(model.chatRows(layout), "\n")

	state := model.activeChannelState()
	state.messages[2].Deleted = true
	after := strings.Join(model.chatRows(layout), "\n")

	if before == after {
		t.Fatal("marking a message deleted did not change the rendered rows; the row cache is stale")
	}
}

// TestChatRowCacheInvalidatesOnResize covers the whole-cache invalidation
// path: width is part of the render params, not the per-message key.
func TestChatRowCacheInvalidatesOnResize(t *testing.T) {
	model := chatModelWithMessages(t, 5, nil)
	before := strings.Join(model.chatRows(model.layout()), "\n")

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	model = updated.(mockShellModel)
	after := strings.Join(model.chatRows(model.layout()), "\n")

	if before == after {
		t.Fatal("resizing did not change the rendered rows; the row cache ignored the width change")
	}
}

func TestTrimScrollbackDropsOldestAndKeepsNewest(t *testing.T) {
	state := &channelState{name: "example"}
	for i := range 10 {
		state.messages = append(state.messages, twitch.ChatMessage{ID: fmt.Sprintf("m-%d", i)})
	}
	state.trimScrollback(4)

	if got := len(state.messages); got != 4 {
		t.Fatalf("len(messages) = %d, want 4", got)
	}
	if got, want := state.messages[0].ID, "m-6"; got != want {
		t.Fatalf("oldest retained = %q, want %q", got, want)
	}
	if got, want := state.messages[3].ID, "m-9"; got != want {
		t.Fatalf("newest retained = %q, want %q", got, want)
	}
}

func TestTrimScrollbackIsNoOpWhenUnlimitedOrUnderLimit(t *testing.T) {
	for _, limit := range []int{0, -1, 10} {
		state := &channelState{name: "example"}
		for i := range 10 {
			state.messages = append(state.messages, twitch.ChatMessage{ID: fmt.Sprintf("m-%d", i)})
		}
		state.trimScrollback(limit)
		if got := len(state.messages); got != 10 {
			t.Fatalf("limit=%d: len(messages) = %d, want 10", limit, got)
		}
	}
}

// TestApplyMessageTrimsInactiveChannel covers the buffer that grows without
// anyone looking at it: an inactive channel still accumulates every message.
func TestApplyMessageTrimsInactiveChannel(t *testing.T) {
	set := newChannelStateSet([]string{"first", "second"}, mockAnimationConfig("off"), nil, 3)
	for i := range 12 {
		set.applyMessage(twitch.ChatMessage{
			ID:      fmt.Sprintf("m-%d", i),
			Channel: "second",
			Text:    "hello",
		})
	}
	state := set.ensure("second")
	if got := len(state.messages); got != 3 {
		t.Fatalf("inactive channel retained %d messages, want 3", got)
	}
	if got, want := state.messages[0].ID, "m-9"; got != want {
		t.Fatalf("oldest retained = %q, want %q", got, want)
	}
}
