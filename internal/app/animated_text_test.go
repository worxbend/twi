package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/config"
)

func TestSplashTaglineTypesInWithoutMovingSideways(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 88, 22
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.splashUntil = started.Add(splashDuration)
	contentWidth := splashContentWidth(model.width)
	canvas := model.canvasBackground()

	widths := make(map[int]bool)
	lengths := make([]int, 0, 8)
	for _, offset := range []time.Duration{0, 300, 600, 900, 1200, 1500, 1900} {
		line := model.splashTaglineLine(contentWidth, offset*time.Millisecond, canvas)
		widths[lipgloss.Width(line)] = true
		lengths = append(lengths, len(strings.TrimSpace(strings.ReplaceAll(line, animation.DefaultCursor, ""))))
	}
	if len(widths) != 1 {
		t.Fatalf("tagline line widths = %v, want one stable width across the reveal", widths)
	}
	if lengths[0] >= lengths[len(lengths)-1] {
		t.Fatalf("tagline did not grow while typing: %v", lengths)
	}
	settled := model.splashTaglineLine(contentWidth, splashDuration, canvas)
	if !strings.Contains(settled, splashTaglineText) {
		t.Fatalf("settled tagline missing %q:\n%q", splashTaglineText, settled)
	}
}

// The splash lifts on a wall-clock deadline, so a tagline that is still
// typing when it lifts would never be read. Reduced motion must still finish.
func TestSplashTaglineFinishesBeforeTheSplashLifts(t *testing.T) {
	for _, mode := range []animation.Mode{animation.ModeFast, animation.ModeReduced} {
		cfg := config.Default()
		cfg.Features.AnimationMode = string(mode)
		model := newMockModel("alpha", cfg)

		textCfg := model.textEffectConfig(animation.EffectTypewriter)
		textCfg.Step = splashTaglineStep(model.animationMode)
		elapsed := splashDuration - splashTaglineDelay
		if !animation.TextDone(splashTaglineText, textCfg, elapsed) {
			t.Fatalf("%s tagline unfinished after %s of a %s splash", mode, elapsed, splashDuration)
		}
	}
}

func TestSplashLinesAnimateBetweenFrames(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 88, 22
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.splashUntil = started.Add(splashDuration)
	contentWidth := splashContentWidth(model.width)
	canvas := model.canvasBackground()

	first := model.splashDecorativeLine(contentWidth, 0, canvas)
	later := model.splashDecorativeLine(contentWidth, 640*time.Millisecond, canvas)
	if first == later {
		t.Fatal("splash strapline is identical across frames, want the shimmer to sweep")
	}
	if lipgloss.Width(first) != lipgloss.Width(later) {
		t.Fatalf("strapline widths differ across frames: %d vs %d", lipgloss.Width(first), lipgloss.Width(later))
	}
}

func TestEmptyStateAnimatesOnTheSharedFrameClock(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())

	model.lastFrameAt = time.UnixMilli(1000)
	first := strings.Join(model.noChannelRows(60), "\n")
	model.lastFrameAt = time.UnixMilli(1900)
	later := strings.Join(model.noChannelRows(60), "\n")
	if first == later {
		t.Fatal("empty state is identical across frames, want the headline and scanner to move")
	}
	for _, view := range []string{first, later} {
		if !strings.Contains(view, "/channels") || !strings.Contains(view, "ctrl+p") {
			t.Fatalf("empty state lost its guidance:\n%s", view)
		}
		if !strings.Contains(view, emptyStateScannerGlyph) {
			t.Fatalf("empty state missing the idle scanner:\n%s", view)
		}
	}
}

func TestEmptyStateHoldsStillWhenAnimationIsOff(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = string(animation.ModeOff)
	model := newMockModel("alpha", cfg)

	model.lastFrameAt = time.UnixMilli(1000)
	first := strings.Join(model.noChannelRows(60), "\n")
	model.lastFrameAt = time.UnixMilli(9000)
	later := strings.Join(model.noChannelRows(60), "\n")
	if first != later {
		t.Fatalf("empty state animated with animation=off:\n%s\n---\n%s", first, later)
	}
	if !strings.Contains(first, noChannelHeadline) {
		t.Fatalf("static empty state missing %q:\n%s", noChannelHeadline, first)
	}
	if strings.Contains(first, emptyStateScannerGlyph) {
		t.Fatalf("static empty state kept a frozen scanner:\n%s", first)
	}
}

func TestEmptyStateRowsStayInsideTheirWidth(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.lastFrameAt = time.UnixMilli(1400)

	for _, width := range []int{1, 6, 12, 24, 60, 120} {
		for _, row := range model.noChannelRows(width) {
			if got := lipgloss.Width(row); got > width {
				t.Fatalf("empty-state row width = %d at pane width %d: %q", got, width, row)
			}
		}
	}
}

func TestCenteredEffectLinePaintsPaddingWithBackground(t *testing.T) {
	forceColorProfile(t)
	background := newMockModel("alpha", config.Default()).canvasBackground()
	backgroundCode := backgroundOnlySGRCode(t, background)

	cfg := animation.TextConfig{Effect: animation.EffectNone, Base: "#ffffff"}
	line := centeredEffectLine(animatedText("twi", cfg, 0, background), 11, background)
	if got := lipgloss.Width(line); got != 11 {
		t.Fatalf("centered effect line width = %d, want 11: %q", got, line)
	}
	// Both pads plus the label itself must re-establish the background, or
	// the terminal's own background shows through after each ANSI reset.
	if got := strings.Count(line, backgroundCode+"m"); got < 3 {
		t.Fatalf("centered effect line sets the background %d times, want at least 3: %q", got, line)
	}
	for plain := range strings.SplitSeq(line, "\x1b[0m") {
		if strings.HasPrefix(plain, " ") {
			t.Fatalf("unstyled padding follows a reset in %q", line)
		}
	}
}
