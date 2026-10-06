package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/theme"
)

func TestRenderRunsPreservesWidthAndText(t *testing.T) {
	forceColorProfile(t)
	tokens := theme.For(theme.DefaultPalette())
	line := renderRuns(24, tokens.Surface,
		run{text: "one", foreground: tokens.Accent, bold: true},
		run{text: " | ", foreground: tokens.Faint},
		run{text: "two 視聴", foreground: tokens.Text},
	)
	if got := lipgloss.Width(line); got != 24 {
		t.Fatalf("renderRuns width = %d, want 24: %q", got, line)
	}
	if plain := ansi.Strip(line); plain != "one | two 視聴          " {
		t.Fatalf("renderRuns text = %q, want the runs joined and padded", plain)
	}
}

func TestRenderRunsTruncatesOverflow(t *testing.T) {
	forceColorProfile(t)
	tokens := theme.For(theme.DefaultPalette())
	line := renderRuns(5, tokens.Surface, run{text: "abcdefgh", foreground: tokens.Text})
	if got := lipgloss.Width(line); got != 5 {
		t.Fatalf("renderRuns overflow width = %d, want 5: %q", got, line)
	}
	if plain := ansi.Strip(line); plain != "abcde" {
		t.Fatalf("renderRuns overflow text = %q, want %q", plain, "abcde")
	}
}

func TestStyleLineSpansKeepsTextAndColorsOnlyTheSpan(t *testing.T) {
	forceColorProfile(t)
	tokens := theme.For(theme.DefaultPalette())
	plain := " *1:Chat  2:Info"
	line := styleLineSpans(plain, 20,
		run{foreground: tokens.Muted, background: tokens.Track},
		lineSpan{start: 0, end: 8, piece: run{foreground: tokens.OnAccent, background: tokens.Accent, bold: true}},
	)
	if got := lipgloss.Width(line); got != 20 {
		t.Fatalf("styleLineSpans width = %d, want 20: %q", got, line)
	}
	if stripped := ansi.Strip(line); stripped != fitLine(plain, 20) {
		t.Fatalf("styleLineSpans changed the text: %q, want %q", stripped, fitLine(plain, 20))
	}
	// The label must survive as one contiguous styled chunk: substring
	// matching on styled output is how the tab bar tests (and terminal
	// search) find it.
	if !strings.Contains(line, "*1:Chat") {
		t.Fatalf("styleLineSpans broke the label into per-cell chunks: %q", line)
	}
}

func TestProgressBarWidthAndFillStates(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	for _, fraction := range []float64{0, 0.37, 0.5, 1} {
		bar := model.progressBar(16, fraction, model.canvasBackground(), 0)
		if got := lipgloss.Width(bar); got != 16 {
			t.Fatalf("progressBar(%v) width = %d, want 16: %q", fraction, got, bar)
		}
	}
	empty := model.progressBar(16, 0, model.canvasBackground(), 0)
	full := model.progressBar(16, 1, model.canvasBackground(), 0)
	if strings.Contains(empty, progressFullGlyph) {
		t.Fatalf("empty progress bar drew a fill cell: %q", empty)
	}
	if count := strings.Count(full, progressFullGlyph); count != 16 {
		t.Fatalf("full progress bar fill cells = %d, want 16: %q", count, full)
	}
}

func TestProgressBarAdvancesInSubCellSteps(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	// 1/32 of a 16-cell bar is half a cell: no full glyph, one partial.
	bar := model.progressBar(16, 1.0/32, model.canvasBackground(), 0)
	if strings.Contains(bar, progressFullGlyph) {
		t.Fatalf("sub-cell progress drew a full cell: %q", bar)
	}
	partial := false
	for _, glyph := range progressBarGlyphs {
		if strings.Contains(bar, glyph) {
			partial = true
			break
		}
	}
	if !partial {
		t.Fatalf("sub-cell progress drew no partial glyph: %q", bar)
	}
}

func TestPickerSelectedLineMirrorsTheDrawnWindow(t *testing.T) {
	// No results, or a pane too short for a list under its header, has no
	// highlighted row at all.
	if got := pickerSelectedLine(0, 0, 6); got != -1 {
		t.Fatalf("pickerSelectedLine with no rows = %d, want -1", got)
	}
	if got := pickerSelectedLine(0, 5, 1); got != -1 {
		t.Fatalf("pickerSelectedLine with a header-only pane = %d, want -1", got)
	}
	// With everything visible the highlight is one row past the header.
	if got := pickerSelectedLine(2, 5, 6); got != 3 {
		t.Fatalf("pickerSelectedLine(2, 5, 6) = %d, want 3", got)
	}
	// A scrolled window keeps the highlighted row inside it.
	got := pickerSelectedLine(9, 20, 6)
	if got < 1 || got > 5 {
		t.Fatalf("pickerSelectedLine(9, 20, 6) = %d, want a row inside the 5-line window", got)
	}
}

func TestStyledPickerLinesKeepTextContiguous(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	lines := []string{" Command: quit", "> Quit  q / ctrl+c", "  Focus composer  tab"}
	styled := model.stylePickerLines(lines, 30, 1)
	if len(styled) != len(lines) {
		t.Fatalf("stylePickerLines returned %d lines, want %d", len(styled), len(lines))
	}
	for index, line := range styled {
		if got := lipgloss.Width(line); got != 30 {
			t.Fatalf("styled line %d width = %d, want 30: %q", index, got, line)
		}
	}
	joined := strings.Join(styled, "\n")
	for _, want := range []string{"Command", "Quit", "Focus composer"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("styled picker lines lost %q:\n%s", want, joined)
		}
	}
}

func TestStatusLineSemanticStates(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 120, 24

	model.channels.ensure("alpha").live = true
	model.channels.ensure("alpha").liveSince = time.Time{}
	live := model.statusLine(120)
	if !strings.Contains(live, "LIVE") {
		t.Fatalf("live status line missing the LIVE badge:\n%s", live)
	}

	model.channels.ensure("alpha").live = false
	offline := model.statusLine(120)
	if !strings.Contains(offline, "OFFLINE") {
		t.Fatalf("offline status line missing the OFFLINE badge:\n%s", offline)
	}
	if live == offline {
		t.Fatal("live and offline status lines rendered identically; the badge state did not change")
	}
	if got := lipgloss.Width(offline); got != 120 {
		t.Fatalf("status line width = %d, want 120", got)
	}
}

func TestHelpViewStylesKeycapsWithoutChangingText(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.helpExpanded = true
	view := model.helpView(100, 5)
	plain := ansi.Strip(view)
	for _, want := range []string{"ctrl+p", "?", "quit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded help missing %q:\n%s", want, plain)
		}
	}
	for number, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != 100 {
			t.Fatalf("help line %d width = %d, want 100: %q", number+1, got, line)
		}
	}
}
