package app

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
)

// unstyledGapAfterReset matches an ANSI reset immediately followed by two or
// more plain (unescaped) whitespace characters and then another escape
// sequence — the exact signature of centering an already-styled span by
// padding it afterward instead of padding the plain text before styling it.
var unstyledGapAfterReset = regexp.MustCompile(`\x1b\[0m\s{2,}\x1b\[`)

// forceColorProfile pins lipgloss's default renderer to TrueColor for the
// duration of the test and restores whatever profile was active before.
// Setting env vars alone (CLICOLOR_FORCE, COLORTERM) isn't reliable here:
// lipgloss/termenv detect and cache the profile once per process, so
// whichever test in this package first touches lipgloss rendering can lock
// in "no color" for every test that runs after it in the same `go test`
// binary, regardless of env vars set later.
func forceColorProfile(t *testing.T) {
	t.Helper()
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(original)
	})
}

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
	forceColorProfile(t)
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
	forceColorProfile(t)
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
	forceColorProfile(t)
	cfg := config.Default()
	background := cfg.ResolveTheme().Background
	backgroundCode := backgroundOnlySGRCode(t, background)

	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.splashUntil = time.Now().Add(splashDuration)

	view := model.View()
	// The wordmark/tagline/progress-bar lines are each rendered independently
	// (their own lipgloss.Style.Render call, each ending in its own ANSI
	// reset) before being joined; the outer splash Background() wrap alone
	// only colors up to the first such reset. Regression test for that gap.
	if got := strings.Count(view, backgroundCode+"m"); got < 2 {
		t.Fatalf("splash view applies background %d times, want at least 2 (outer wrap + inner lines):\n%q", got, view)
	}
}

func TestSplashViewWordmarkLineHasNoUnstyledGapBetweenResets(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.splashUntil = time.Now().Add(splashDuration)

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
	forceColorProfile(t)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#d97757")).Background(lipgloss.Color("#1a1523")).Bold(true)

	// Fixed correct usage: pad the plain text first, then style the result
	// in one Render() call.
	fixed := style.Render(centeredPlainLine("twi", 26))
	if lipgloss.Width(fixed) != 26 {
		t.Fatalf("centeredPlainLine visible width = %d, want 26", lipgloss.Width(fixed))
	}
	if strings.Count(fixed, "\x1b[0m") != 1 || !strings.HasSuffix(fixed, "\x1b[0m") {
		t.Fatalf("styling centered plain text left unstyled padding outside the span: %q", fixed)
	}

	// The bug this guards against: centering an *already-styled* string by
	// padding it afterward (e.g. via lipgloss.JoinVertical(lipgloss.Center,
	// ...)) leaves the padding, added after the inner ANSI reset, fully
	// unstyled — demonstrated here for contrast.
	buggy := lipgloss.JoinVertical(lipgloss.Center, style.Render("twi"), strings.Repeat("x", 26))
	wordmarkLine := strings.SplitN(buggy, "\n", 2)[0]
	if strings.HasSuffix(wordmarkLine, "\x1b[0m") {
		t.Fatal("expected the pad-after-styling approach to leave trailing unstyled padding, but it didn't — centeredPlainLine's fix may no longer be necessary, or this test's premise is stale")
	}
}

func TestBorderedFramesApplyBackgroundToBorderCharacters(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	background := cfg.ResolveTheme().Background
	backgroundCode := backgroundOnlySGRCode(t, background)

	model := newMockShellModel("alpha", cfg)
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

func TestThemeBackgroundSequenceOnlyWhenInteractive(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	if got := model.themeBackgroundSequence(); got != "" {
		t.Fatalf("themeBackgroundSequence() without terminalOutput = %q, want empty", got)
	}

	var buf bytes.Buffer
	model.terminalOutput = &buf
	want := "\x1b]11;" + model.theme.Background + "\x07"
	if got := model.themeBackgroundSequence(); got != want {
		t.Fatalf("themeBackgroundSequence() = %q, want %q", got, want)
	}
}

func TestViewEmbedsThemeBackgroundSequenceWhenInteractive(t *testing.T) {
	var buf bytes.Buffer
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22
	model.terminalOutput = &buf

	view := model.View()
	want := "\x1b]11;" + model.theme.Background + "\x07"
	if !strings.HasPrefix(view, want) {
		t.Fatalf("View() does not start with theme background sequence %q:\n%s", want, view)
	}
}

func TestViewOmitsThemeBackgroundSequenceWhenNotInteractive(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22

	view := model.View()
	if strings.Contains(view, "\x1b]11;") {
		t.Fatalf("non-interactive View() unexpectedly includes OSC 11 sequence:\n%s", view)
	}
}

func TestViewEmbedsThemeBackgroundSequenceDuringSplash(t *testing.T) {
	var buf bytes.Buffer
	model := newMockShellModel("alpha", config.Default())
	model.width, model.height = 88, 22
	model.terminalOutput = &buf
	model.splashUntil = time.Now().Add(splashDuration)

	view := model.View()
	want := "\x1b]11;" + model.theme.Background + "\x07"
	if !strings.HasPrefix(view, want) {
		t.Fatalf("splash View() does not start with theme background sequence %q:\n%s", want, view)
	}
}

func TestPrimeTerminalBackgroundEmitsOSC11(t *testing.T) {
	var buf bytes.Buffer
	primeTerminalBackground(&buf, "#111018")
	if got, want := buf.String(), "\x1b]11;#111018\x07"; got != want {
		t.Fatalf("primeTerminalBackground output = %q, want %q", got, want)
	}
}

func TestPrimeTerminalBackgroundNoopWithEmptyColor(t *testing.T) {
	var buf bytes.Buffer
	primeTerminalBackground(&buf, "")
	if buf.Len() != 0 {
		t.Fatalf("primeTerminalBackground with empty color wrote %q, want nothing", buf.String())
	}
}

func TestResetTerminalBackgroundEmitsOSC111(t *testing.T) {
	var buf bytes.Buffer
	resetTerminalBackground(&buf)
	if got, want := buf.String(), "\x1b]111\x07"; got != want {
		t.Fatalf("resetTerminalBackground output = %q, want %q", got, want)
	}
}
