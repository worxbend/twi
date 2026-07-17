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
	model := newMockShellModel("alpha", config.Default())
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
	model := newMockShellModel("alpha", config.Default())
	spec := paneSpec{icon: "💬", title: "Chat", width: 24, contentHeight: 1, accent: model.theme.Accent, focused: true}
	model.lastFrameAt = time.UnixMilli(1600)
	first := model.renderPane(spec)
	model.lastFrameAt = time.UnixMilli(1800)
	second := model.renderPane(spec)
	if first == second {
		t.Fatal("focused pane chrome did not animate with the shared frame clock")
	}
	if lipgloss.Width(first) != lipgloss.Width(second) {
		t.Fatal("focused pane animation changed rendered width")
	}
}

func TestChatPaneChromeIsStaticAcrossSharedFrames(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "fast"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	layout := model.layout()

	model.lastFrameAt = time.UnixMilli(1600)
	first := model.chatView(layout)
	model.lastFrameAt = time.UnixMilli(1800)
	second := model.chatView(layout)
	if first != second {
		t.Fatal("chat pane chrome changed with the shared frame clock; want a static border and title")
	}
}

func TestCanvasBackgroundIsDarkerThanThemeBackground(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	if got := model.canvasBackground(); got == model.theme.Background {
		t.Fatalf("canvas background = pane base %q, want a darker derived color", got)
	}
}

func TestPaneTitleLinePreservesResponsiveWidth(t *testing.T) {
	forceColorProfile(t)
	model := newMockShellModel("alpha", config.Default())
	for width := 1; width <= 60; width++ {
		for _, focused := range []bool{false, true} {
			line := model.paneTitleLine(width, "🎮", "A deliberately long Unicode title · #δοκιμή", model.theme.Accent, model.theme.Success, focused)
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("title width at width=%d focused=%v = %d, want %d: %q", width, focused, got, width, ansi.Strip(line))
			}
		}
	}
}
