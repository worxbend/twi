package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
)

func TestRenderPanePreservesDimensionsAndAddsIconTitle(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	view := model.renderPane(paneSpec{
		icon:          "💬",
		title:         "Chat · #alpha",
		content:       fitBlock("hello", 26, 3),
		width:         30,
		contentHeight: 3,
		padding:       1,
		accent:        model.theme.Accent,
		focused:       true,
	})

	if got, want := lineCount(view), 5; got != want {
		t.Fatalf("pane height = %d, want %d:\n%s", got, want, view)
	}
	for number, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != 30 {
			t.Fatalf("pane line %d width = %d, want 30:\n%s", number+1, got, view)
		}
	}
	plain := ansi.Strip(view)
	for _, want := range []string{"💬", "Chat · #alpha", "hello", "┌", "┐", "└", "┘"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pane missing %q:\n%s", want, view)
		}
	}
}

func TestFocusedPaneChromeAnimatesFromSharedFrame(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	spec := paneSpec{icon: "💬", title: "Chat", width: 24, contentHeight: 1, accent: model.theme.Accent, focused: true}
	model.frames.lastFrameAt = time.UnixMilli(1600)
	first := model.renderPane(spec)
	model.frames.lastFrameAt = time.UnixMilli(1800)
	second := model.renderPane(spec)
	if first == second {
		t.Fatal("focused pane chrome did not animate with the shared frame clock")
	}
	if lipgloss.Width(first) != lipgloss.Width(second) {
		t.Fatal("focused pane animation changed rendered width")
	}
}

func TestChatPaneChromeIsStaticWhenNotFocused(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.focus = focusSidebar
	layout := model.layout()

	model.frames.lastFrameAt = time.UnixMilli(1600)
	first := model.chatView(layout)
	model.frames.lastFrameAt = time.UnixMilli(1800)
	second := model.chatView(layout)
	if first != second {
		t.Fatal("unfocused chat pane chrome changed with the shared frame clock; want a static border and title")
	}
}

// TestChatPaneChromeAnimatesWhenFocused guards the chat pane's own
// paneSpec.focused wiring specifically: chatView used to omit that field
// entirely, so the chat pane's border and title never lit up or shimmered
// on focus at all, unlike every other pane (see
// TestFocusedPaneChromeAnimatesFromSharedFrame).
func TestChatPaneChromeAnimatesWhenFocused(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.focus = focusChat
	layout := model.layout()

	model.frames.lastFrameAt = time.UnixMilli(1600)
	first := model.chatView(layout)
	model.frames.lastFrameAt = time.UnixMilli(1800)
	second := model.chatView(layout)
	if first == second {
		t.Fatal("focused chat pane chrome did not animate with the shared frame clock")
	}
}

func TestCanvasBackgroundIsDarkerThanThemeBackground(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	if got := model.canvasBackground(); got == model.theme.Background {
		t.Fatalf("canvas background = pane base %q, want a darker derived color", got)
	}
}

func TestPaneTitleLinePreservesResponsiveWidth(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	for width := 1; width <= 60; width++ {
		for _, focused := range []bool{false, true} {
			line := model.paneTitleLine(width, "🎮", "A deliberately long Unicode title · #δοκιμή", model.theme.Accent, model.theme.Success, model.theme.Border, focused)
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("title width at width=%d focused=%v = %d, want %d: %q", width, focused, got, width, ansi.Strip(line))
			}
		}
	}
}
