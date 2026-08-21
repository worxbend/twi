package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/config"
)

func TestComposerUsesOpenCodeInspiredSurfaceAnatomy(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 88, 20
	model.focus = focusComposer
	model.activeChannelState().composerText = "hello chat"
	layout := model.layout()

	view := model.composerView(layout)
	if got, want := lineCount(view), 4; got != want {
		t.Fatalf("composer height = %d, want %d:\n%s", got, want, view)
	}
	for number, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != layout.width {
			t.Fatalf("composer line %d width = %d, want %d:\n%s", number+1, got, layout.width, view)
		}
	}
	for _, want := range []string{"▌", "hello chat", "█", "Chat", "#alpha", "ready"} {
		if !strings.Contains(view, want) {
			t.Fatalf("composer missing %q:\n%s", want, view)
		}
	}
	for _, boxBorder := range []string{"┌", "┐", "└", "┘", "─"} {
		if strings.Contains(view, boxBorder) {
			t.Fatalf("composer retained full box border %q:\n%s", boxBorder, view)
		}
	}
}

func TestComposerFocusSwapsPlaceholderForBlockCursor(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 72, 18
	layout := model.layout()

	unfocused := model.composerView(layout)
	if !strings.Contains(unfocused, "Message #alpha…") || strings.Contains(unfocused, "█") {
		t.Fatalf("unfocused composer should show placeholder without cursor:\n%s", unfocused)
	}

	model.focus = focusComposer
	focused := model.composerView(layout)
	if strings.Contains(focused, "Message #alpha…") || !strings.Contains(focused, "█") {
		t.Fatalf("focused empty composer should show only the block cursor:\n%s", focused)
	}
}

func TestComposerCursorBlinksFromSharedFrameClock(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 72, 18
	model.focus = focusComposer
	layout := model.layout()

	model.frames.lastFrameAt = time.UnixMilli(1000)
	visible := model.composerView(layout)
	model.frames.lastFrameAt = time.UnixMilli(1500)
	hidden := model.composerView(layout)
	if !strings.Contains(visible, "█") || strings.Contains(hidden, "█") {
		t.Fatalf("cursor blink states incorrect:\nvisible:\n%s\nhidden:\n%s", visible, hidden)
	}
	if lipgloss.Width(strings.Split(visible, "\n")[0]) != lipgloss.Width(strings.Split(hidden, "\n")[0]) {
		t.Fatal("cursor blink changed composer line width")
	}
}

func TestComposerKeepsLongUnicodeDraftTailBesideCursor(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 32, 18
	model.focus = focusComposer
	model.activeChannelState().composerText = strings.Repeat("old-", 12) + "latest 😀"

	view := model.composerView(model.layout())
	if !strings.Contains(view, "latest 😀█") {
		t.Fatalf("composer did not keep the draft tail beside its cursor:\n%s", view)
	}
}

func TestTailDisplayCellsPreservesWideGrapheme(t *testing.T) {
	const family = "👨‍👩‍👧‍👦"
	if got := tailDisplayCells("0123456789tail", 4); got != "tail" {
		t.Fatalf("ASCII tail = %q, want tail", got)
	}
	if got := tailDisplayCells("prefix "+family, 2); got != family {
		t.Fatalf("ZWJ tail = %q, want %q", got, family)
	}
}

func TestComposerShowsReplyAndSendStateInSurface(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 88, 20
	model.focus = focusComposer
	state := model.activeChannelState()
	state.composerText = "thanks!"
	state.replyTo = &composerReplyContext{MessageID: "parent", Author: "viewer", Text: "question for chat"}
	state.sendState = composerSendRateLimited

	view := model.composerView(model.layout())
	for _, want := range []string{"Replying to viewer", "question for chat", "thanks!", "rate limited"} {
		if !strings.Contains(view, want) {
			t.Fatalf("reply composer missing %q:\n%s", want, view)
		}
	}
	if got, want := lineCount(view), 5; got != want {
		t.Fatalf("reply composer height = %d, want %d:\n%s", got, want, view)
	}
}

func TestComposerCompactsWithoutOverflow(t *testing.T) {
	for _, width := range []int{24, 8, 7, 5, 4, 1} {
		model := newMockModel("alpha", config.Default())
		model.width, model.height = width, 8
		model.focus = focusComposer
		model.activeChannelState().composerText = "hello 😀 表"
		layout := model.layout()
		view := model.composerView(layout)

		if got := lineCount(view); got != layout.composerHeight {
			t.Fatalf("width %d composer height = %d, want %d:\n%s", width, got, layout.composerHeight, view)
		}
		for number, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d line %d rendered width = %d:\n%s", width, number+1, got, view)
			}
		}
	}
}

func TestComposerUsesReadableFallbackAtBoundaryWidths(t *testing.T) {
	for _, width := range []int{5, 6, 7} {
		model := newMockModel("alpha", config.Default())
		model.width, model.height = width, 8
		model.focus = focusComposer
		model.activeChannelState().composerText = "hello"
		layout := model.layout()
		if layout.composerFramed {
			t.Fatalf("width %d composerFramed = true, want readable plain fallback", width)
		}
		if view := model.composerView(layout); !strings.Contains(view, "ello") || !strings.Contains(view, "█") {
			t.Fatalf("width %d fallback hid the draft:\n%s", width, view)
		}
	}

	model := newMockModel("alpha", config.Default())
	model.width, model.height = 8, 8
	model.focus = focusComposer
	model.activeChannelState().composerText = "hello"
	if layout := model.layout(); !layout.composerFramed || !strings.Contains(model.composerView(layout), "█") {
		t.Fatalf("width 8 should use the compact surface with a cursor:\n%s", model.composerView(layout))
	}
}

func TestPlainComposerKeepsLongDraftTailVisible(t *testing.T) {
	for _, width := range []int{5, 6, 7} {
		model := newMockModel("alpha", config.Default())
		model.width, model.height = width, 8
		model.focus = focusComposer
		model.activeChannelState().composerText = "old-old-newest"
		view := model.composerView(model.layout())
		if !strings.Contains(view, "est█") || strings.Contains(view, "old-") {
			t.Fatalf("width %d plain fallback did not keep the draft tail visible:\n%s", width, view)
		}
	}
}

func TestCompactComposerKeepsReplyContextVisible(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 48, 8
	model.focus = focusComposer
	state := model.activeChannelState()
	state.composerText = "reply draft"
	state.replyTo = &composerReplyContext{MessageID: "parent", Author: "viewer", Text: "original message"}
	layout := model.layout()
	if layout.composerHeight != 3 {
		t.Fatalf("compact composer height = %d, want 3", layout.composerHeight)
	}

	view := model.composerView(layout)
	for _, want := range []string{"Replying to viewer", "reply draft", "Chat", "#alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact reply composer missing %q:\n%s", want, view)
		}
	}
}
