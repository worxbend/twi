package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/theme"
)

func TestGradientBackgroundLinePreservesRequestedWidth(t *testing.T) {
	forceColorProfile(t)
	line := gradientBackgroundLine(" ✦ twi 視聴者", 24, "#ff8000", "#00c0ff", "#ffffff", "#000000", 3, true)
	if got := lipgloss.Width(line); got != 24 {
		t.Fatalf("gradient line width = %d, want 24: %q", got, line)
	}
}

func TestGradientEndpointStaysDistinctForColorPresets(t *testing.T) {
	for _, name := range []string{"codex", "btop"} {
		palette, ok := theme.ResolvePalette(name, theme.Palette{})
		if !ok {
			t.Fatalf("theme.ResolvePalette(%q) was not found", name)
		}
		model := shellModel{theme: palette}
		if strings.EqualFold(model.theme.Accent, model.gradientEndColor()) {
			t.Fatalf("%s gradient endpoint = accent %q, want a distinct theme color", name, model.theme.Accent)
		}
	}

	mono, ok := theme.ResolvePalette("mono", theme.Palette{})
	if !ok {
		t.Fatal("theme.ResolvePalette(\"mono\") was not found")
	}
	model := shellModel{theme: mono}
	if !strings.EqualFold(model.theme.Accent, model.gradientEndColor()) {
		t.Fatalf("mono gradient endpoint = %q, want solid accent %q", model.gradientEndColor(), model.theme.Accent)
	}
}

func TestSplashViewHasAnimatedLogoAndNamedBootPhases(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.frames.splashUntil = started.Add(splashDuration)

	loading := model.splashViewAt(started)
	if !strings.Contains(loading, "████████") || !strings.Contains(loading, "twi · loading palette") {
		t.Fatalf("initial splash missing logo or loading phase:\n%s", loading)
	}
	// Expressed as a share of splashDuration rather than a fixed number of
	// milliseconds, so changing the splash length does not silently move
	// this assertion into a different phase.
	ready := model.splashViewAt(started.Add(splashDuration * 9 / 10))
	if !strings.Contains(ready, "ready for #alpha") || !strings.Contains(ready, "━") {
		t.Fatalf("late splash missing ready phase or progress:\n%s", ready)
	}
	if loading == ready {
		t.Fatal("splash frames are identical across animation phases")
	}
}

func TestSplashViewRemainsResponsiveOnCompactTerminal(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 30, 8
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.frames.splashUntil = now.Add(splashDuration)

	view := model.splashViewAt(now.Add(time.Second))
	if got := lineCount(view); got != 8 {
		t.Fatalf("compact splash height = %d, want 8:\n%s", got, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 30 {
			t.Fatalf("compact splash line width = %d, want <= 30: %q", got, line)
		}
	}
}

func TestSplashViewFitsVeryShortTerminals(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.frames.splashUntil = now.Add(splashDuration)

	for _, height := range []int{1, 3, 5} {
		model.width, model.height = 30, height
		view := model.splashViewAt(now.Add(time.Second))
		if got := lineCount(view); got != height {
			t.Fatalf("short splash height = %d, want %d:\n%s", got, height, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > model.width {
				t.Fatalf("short splash line width = %d, want <= %d: %q", got, model.width, line)
			}
		}
	}
}

func TestChatRowsUseMessageRailsMailIconsAndEmoji(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 88, 22
	rows := strings.Join(model.chatRows(model.layout()), "\n")
	for _, want := range []string{"│ ✉ ", "✨", "💜", "👀", "🔔"} {
		if !strings.Contains(rows, want) {
			t.Fatalf("decorated chat rows missing %q:\n%s", want, rows)
		}
	}
}

func TestAnimatingMessageRailChangesWithSharedFrame(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	block := chatRowBlock{animating: true}
	row := render.Row{Fragments: []render.Fragment{{Kind: render.FragmentText, Text: "hello"}}}
	model.frames.lastFrameAt = time.UnixMilli(1600)
	first := model.messageRowString(block, 0, 0, row, 40)
	model.frames.lastFrameAt = time.UnixMilli(1800)
	second := model.messageRowString(block, 0, 0, row, 40)
	if first == second {
		t.Fatal("animated message rail did not change with the shared frame clock")
	}
	if !strings.Contains(first, "✉") || !strings.Contains(second, "✉") {
		t.Fatal("animated message rail dropped the mail icon")
	}
}

func TestMockEmotePickerIncludesExpandedEmojiSet(t *testing.T) {
	entries := sampleEmoteEntries()
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name] = true
	}
	for _, want := range []string{"✨", "💜", "🔥", "😂", "🎉", "👀", "🚀", "💬", "🌈", "⚡"} {
		if !names[want] {
			t.Fatalf("emote picker entries missing emoji %q", want)
		}
	}
}
