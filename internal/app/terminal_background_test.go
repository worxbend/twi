package app

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
)

// unstyledGapAfterReset matches an ANSI reset immediately followed by two or
// more plain (unescaped) whitespace characters and then another escape
// sequence — the exact signature of centering an already-styled span by
// padding it afterward instead of padding the plain text before styling it.
var unstyledGapAfterReset = regexp.MustCompile(`\x1b\[(?:0)?m\s{2,}\x1b\[`)

// backgroundOnlySGRCode learns which SGR code this environment's detected
// color profile actually renders hex as (truecolor, 256-color, or ANSI16),
// so assertions don't hardcode one profile's downsampled code.
func backgroundOnlySGRCode(t *testing.T, hex string) string {
	t.Helper()
	out := lipgloss.NewStyle().Background(lipgloss.Color(hex)).Render("x")
	start := strings.Index(out, "\x1b[")
	end := strings.Index(out, "m")
	if start < 0 || end < 0 || end <= start+2 {
		t.Fatalf("could not parse an SGR sequence out of %q", out)
	}
	return out[start+2 : end]
}

func TestTerminalRowStringPaddingCarriesExplicitBackground(t *testing.T) {
	background := "#111018"
	backgroundCode := backgroundOnlySGRCode(t, background)

	row := render.Row{Fragments: []render.Fragment{{Kind: render.FragmentText, Text: "hi"}}}
	out := terminalRowString(row, 10, background)
	if got := strings.Count(out, backgroundCode+"m"); got < 1 {
		t.Fatalf("terminalRowString padding missing background code %q: %q", backgroundCode, out)
	}

	// Without a background, no styling should be added at all (padding stays
	// plain, matching pre-fix behavior for callers that don't want it).
	unstyled := terminalRowString(row, 10, "")
	if strings.Contains(unstyled, "\x1b[") {
		t.Fatalf("terminalRowString with empty background unexpectedly styled output: %q", unstyled)
	}
}

func TestBackgroundStyledLineWrapsPlainTextOnly(t *testing.T) {
	background := "#111018"
	backgroundCode := backgroundOnlySGRCode(t, background)

	styled := backgroundStyledLine("hello", background)
	if !strings.Contains(styled, backgroundCode+"m") {
		t.Fatalf("backgroundStyledLine missing background code %q: %q", backgroundCode, styled)
	}
	if got := backgroundStyledLine("", background); got != "" {
		t.Fatalf("backgroundStyledLine(\"\", ...) = %q, want empty", got)
	}
	if got := backgroundStyledLine("hello", ""); got != "hello" {
		t.Fatalf("backgroundStyledLine(..., \"\") = %q, want unchanged plain text", got)
	}
}

func TestSplashViewWordmarkAndTaglineCarryExplicitBackground(t *testing.T) {
	cfg := config.Default()
	background := newMockModel("alpha", cfg).canvasBackground()
	backgroundCode := backgroundOnlySGRCode(t, background)

	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.frames.splashUntil = time.Now().Add(splashDuration)

	view := model.View().Content
	// The wordmark/tagline/progress-bar lines are each rendered independently
	// (their own lipgloss.Style.Render call, each ending in its own ANSI
	// reset) before being joined; the outer splash Background() wrap alone
	// only colors up to the first such reset. Regression test for that gap.
	if got := strings.Count(view, backgroundCode+"m"); got < 2 {
		t.Fatalf("splash view applies background %d times, want at least 2 (outer wrap + inner lines):\n%q", got, view)
	}
}

func TestSplashViewWordmarkLineHasNoUnstyledGapBetweenResets(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.frames.splashUntil = time.Now().Add(splashDuration)

	view := model.splashView()
	var wordmarkLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "twi") {
			wordmarkLine = line
			break
		}
	}
	if wordmarkLine == "" {
		t.Fatalf("could not find the wordmark line in splash view:\n%q", view)
	}

	// Signature of the bug: centering an already-styled, already-reset
	// span by padding it afterward (lipgloss.JoinVertical(lipgloss.Center,
	// ...)) leaves a run of plain, unstyled spaces sandwiched between that
	// inner reset and the next escape sequence — nothing re-establishes a
	// background for those cells. Centering the plain text before styling
	// it (centeredPlainLine) keeps the padding inside the styled span, so
	// this pattern should never appear.
	if unstyledGapAfterReset.MatchString(wordmarkLine) {
		t.Fatalf("wordmark line has an unstyled gap between two resets (unpadded-before-styling regression): %q", wordmarkLine)
	}
}

func TestCenteredPlainLineStyledAsOneUnstyledFreeSpan(t *testing.T) {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#d97757")).Background(lipgloss.Color("#1a1523")).Bold(true)

	// Fixed correct usage: pad the plain text first, then style the result
	// in one Render() call.
	fixed := style.Render(centeredPlainLine("twi", 26))
	if lipgloss.Width(fixed) != 26 {
		t.Fatalf("centeredPlainLine visible width = %d, want 26", lipgloss.Width(fixed))
	}
	if strings.Count(fixed, "\x1b[m") != 1 || !strings.HasSuffix(fixed, "\x1b[m") {
		t.Fatalf("styling centered plain text left unstyled padding outside the span: %q", fixed)
	}

	// The bug this guards against: centering an *already-styled* string by
	// padding it afterward (e.g. via lipgloss.JoinVertical(lipgloss.Center,
	// ...)) leaves the padding, added after the inner ANSI reset, fully
	// unstyled — demonstrated here for contrast.
	buggy := lipgloss.JoinVertical(lipgloss.Center, style.Render("twi"), strings.Repeat("x", 26))
	wordmarkLine := strings.SplitN(buggy, "\n", 2)[0]
	if strings.HasSuffix(wordmarkLine, "\x1b[m") {
		t.Fatal("expected the pad-after-styling approach to leave trailing unstyled padding, but it didn't — centeredPlainLine's fix may no longer be necessary, or this test's premise is stale")
	}
}

func TestBorderedFramesApplyBackgroundToBorderCharacters(t *testing.T) {
	cfg := config.Default()
	background := newMockModel("alpha", cfg).canvasBackground()
	backgroundCode := backgroundOnlySGRCode(t, background)

	model := newMockModel("alpha", cfg)
	model.width, model.height = 88, 22
	layout := model.layout()

	// lipgloss.Style.Background() only colors the content area; border
	// characters (┌─┐│└┘) are governed by the separate BorderBackground()
	// property and render with no background at all when that's unset —
	// even if Background() is set on the same style. Regression test for
	// that gap: check the actual top-border LINE, not just the content.
	chat := model.chatView(layout)
	topBorderLine := strings.SplitN(chat, "\n", 2)[0]
	if !strings.Contains(topBorderLine, backgroundCode+"m") {
		t.Fatalf("chatView top border line missing background code %q: %q", backgroundCode, topBorderLine)
	}
}

func TestViewDeclaresTerminalModesAndThemeBackground(t *testing.T) {
	for _, splash := range []bool{false, true} {
		model := newMockModel("alpha", config.Default())
		model.width, model.height = 88, 22
		var output bytes.Buffer
		model.terminalOutput = &output
		if splash {
			model.frames.splashUntil = time.Now().Add(splashDuration)
		}
		view := model.View()
		if !view.AltScreen || !view.ReportFocus || view.MouseMode != tea.MouseModeCellMotion || view.DisableBracketedPasteMode {
			t.Fatalf("terminal modes = %#v", view)
		}
		want := lipgloss.Color(model.canvasBackground())
		if view.BackgroundColor != want {
			t.Fatalf("background = %v, want %v", view.BackgroundColor, want)
		}
		if strings.Contains(view.Content, "\x1b]11;") {
			t.Fatal("terminal background escaped into content")
		}
	}
}

func TestNoninteractiveViewDoesNotChangeTerminalBackground(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.mouseEnabled = false
	view := model.View()
	if view.BackgroundColor != nil || view.MouseMode != tea.MouseModeNone {
		t.Fatalf("terminal modes = %#v", view)
	}
}
