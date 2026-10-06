package app

// components.go is twi's reusable design-system kit: the small, exact-width
// renderers every surface is built from. They exist so a tab pill, a status
// chip, a picker selection row, and a splash meter all make the same visual
// decisions in one place -- semantic tokens instead of raw palette fields,
// every cell carrying an explicit background (an inner ANSI reset must never
// expose the terminal's default background mid-line), and grapheme-safe width
// math throughout.
//
// Everything here is a pure function of its arguments plus the model's theme
// and frame clock, so the existing deterministic screenshot and polish tests
// keep working unchanged.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
)

// run is one styled piece of a rendered line. An empty background means
// "inherit the line's background", which renderRuns substitutes in.
type run struct {
	text       string
	foreground string
	background string
	bold       bool
	italic     bool
}

// tokens derives the semantic token set for the model's active theme.
func (m shellModel) tokens() theme.Tokens {
	return theme.For(m.theme)
}

// renderRuns draws runs left to right on background, padded or truncated to
// exactly width cells. Every cell leaves with an explicit background: the
// panes embed these lines inside an outer lipgloss style, and the first inner
// ANSI reset would otherwise drop everything after it to the terminal's
// default background.
func renderRuns(width int, background string, runs ...run) string {
	if width <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	for _, piece := range runs {
		if used >= width {
			break
		}
		text := revealDisplayCells(piece.text, width-used)
		if text == "" {
			continue
		}
		fg := piece.foreground
		if fg == "" {
			fg = background
		}
		bg := piece.background
		if bg == "" {
			bg = background
		}
		builder.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(fg)).
			Background(lipgloss.Color(bg)).
			Bold(piece.bold).
			Italic(piece.italic).
			Render(text))
		used += uniseg.StringWidth(text)
	}
	if used < width {
		builder.WriteString(lipgloss.NewStyle().
			Background(lipgloss.Color(background)).
			Render(strings.Repeat(" ", width-used)))
	}
	return builder.String()
}

// keycap styles one keyboard hint the way help and footer surfaces draw it:
// raised fill, accent text, bold -- the keyboard-equivalent of a button.
func keycap(label string, tokens theme.Tokens) run {
	return run{
		text:       label,
		foreground: tokens.Accent,
		background: tokens.SurfaceHi,
		bold:       true,
	}
}

// separatorRun is the faint vertical divider between segments on chrome
// strips. It takes the exact text the line already carries (" | ") so the
// plain-text run a hit-test or assertion measures never changes.
func separatorRun(text string, tokens theme.Tokens) run {
	return run{text: text, foreground: tokens.Faint}
}

// progressBarGlyphs are the sub-cell fill steps, from one eighth to full.
// They are Block Elements (BMP, single width), so column math stays exact.
var progressBarGlyphs = []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}

const progressFullGlyph = "█"

// progressBar renders a width-cell meter on background: a gradient fill that
// advances in eighth-cell steps over a dim track. phase rotates the gradient
// with the shared frame clock; callers pass 0 when animation is off, which
// renders the solid accent end of the gradient.
func (m shellModel) progressBar(width int, fraction float64, background string, phase int) string {
	if width <= 0 {
		return ""
	}
	tokens := m.tokens()
	fraction = clampFraction(fraction)
	colors := theme.SeamlessGradient(tokens.Accent, m.gradientEndColor(), width*2)
	if len(colors) == 0 {
		colors = []string{tokens.Accent}
	}

	eighths := int(fraction * float64(width*8))
	fullCells := eighths / 8
	partial := eighths % 8

	var builder strings.Builder
	for cell := 0; cell < width; cell++ {
		glyph := " "
		foreground := tokens.Faint
		cellBackground := tokens.Track
		switch {
		case cell < fullCells:
			glyph = progressFullGlyph
			foreground = colors[(cell+phase)%len(colors)]
			cellBackground = background
		case cell == fullCells && partial > 0:
			glyph = progressBarGlyphs[partial-1]
			foreground = colors[(cell+phase)%len(colors)]
			cellBackground = tokens.Track
		}
		builder.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(foreground)).
			Background(lipgloss.Color(cellBackground)).
			Bold(glyph != " ").
			Render(glyph))
	}
	return builder.String()
}

// styleLineSpans re-styles an already-assembled plain line by cell spans
// without changing its text: each span colors [start, end) of the line's
// cells, everything outside the spans gets base. Chrome whose plain text is
// mirrored by mouse hit-testing (the tab bar) styles this way so the drawn
// run and the measured run cannot drift apart.
func styleLineSpans(line string, width int, base run, spans ...lineSpan) string {
	if width <= 0 {
		return ""
	}
	plain := fitLine(line, width)
	cells := splitCells(plain)
	// Resolve each cell's piece, then emit maximal same-style chunks: ANSI
	// must never land inside a label, because rendering tests (and users'
	// terminal search) match substrings like "*1:Chat" on the styled line.
	pieces := make([]run, len(cells))
	for index := range cells {
		piece := base
		for _, span := range spans {
			if index >= span.start && index < span.end {
				piece = span.piece
				break
			}
		}
		pieces[index] = piece
	}
	var builder strings.Builder
	for index := 0; index < len(cells); {
		end := index + 1
		for end < len(cells) && pieces[end] == pieces[index] {
			end++
		}
		chunk := ""
		for _, cell := range cells[index:end] {
			chunk += cell
		}
		piece := pieces[index]
		builder.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(piece.foreground)).
			Background(lipgloss.Color(piece.background)).
			Bold(piece.bold).
			Italic(piece.italic).
			Render(chunk))
		index = end
	}
	return builder.String()
}

// lineSpan colors the half-open cell range [start, end) of a line with piece.
type lineSpan struct {
	start, end int
	piece      run
}

// pickerSelectedPrefix is the plain-text marker every searchable overlay
// draws in front of its highlighted row.
const pickerSelectedPrefix = "> "

// stylePickerLines styles the plain rows every searchable overlay builds
// (command palette, emote/channel/category pickers): the header row raised
// with an accent name, the highlighted row on the Selection token with an
// accent marker, "no matches" quiet. selectedLine is the index into lines of
// the highlighted row, or -1 when there is nothing to highlight. Text is
// never altered, only the spans it is drawn with.
func (m shellModel) stylePickerLines(lines []string, width, selectedLine int) []string {
	tokens := m.tokens()
	styled := make([]string, len(lines))
	for index, line := range lines {
		switch {
		case index == 0:
			styled[index] = renderRuns(width, tokens.SurfaceHi, pickerHeaderRuns(line, tokens)...)
		case index == selectedLine:
			rest := strings.TrimPrefix(line, pickerSelectedPrefix)
			prefix := line[:len(line)-len(rest)]
			styled[index] = renderRuns(width, tokens.Selection,
				run{text: prefix, foreground: tokens.Accent, background: tokens.Selection, bold: true},
				run{text: rest, foreground: tokens.Text, background: tokens.Selection, bold: true},
			)
		case strings.TrimSpace(line) == "no matches":
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Muted, italic: true})
		default:
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Text})
		}
	}
	return styled
}

// pickerHeaderRuns styles an overlay's query row: the prompt name in bold
// accent, the typed query (everything from the first ": ") in primary text.
func pickerHeaderRuns(line string, tokens theme.Tokens) []run {
	name, query, found := strings.Cut(line, ": ")
	runs := []run{{text: name, foreground: tokens.Accent, background: tokens.SurfaceHi, bold: true}}
	if found {
		runs = append(runs, run{text: ": " + query, foreground: tokens.Text, background: tokens.SurfaceHi})
	}
	return runs
}

// styleFormLines styles the rows of the form-like tab screens (Stream Info,
// Stream Markers): the header row gets an accent name with a muted hint, the
// "> "-marked row is the selection, failure rows announce themselves in the
// error token, confirmations in success, and in-progress or placeholder rows
// recede to mute. Each row is styled in as few chunks as possible so the
// strings tests match ("Title: Hello world", "Load failed: ...") stay
// contiguous.
func (m shellModel) styleFormLines(lines []string, width int) []string {
	tokens := m.tokens()
	styled := make([]string, len(lines))
	for index, line := range lines {
		switch {
		case index == 0:
			name, hint, found := strings.Cut(line, " (")
			runs := []run{{text: name, foreground: tokens.Accent, background: tokens.SurfaceHi, bold: true}}
			if found {
				runs = append(runs, run{text: " (" + hint, foreground: tokens.Muted, background: tokens.SurfaceHi})
			}
			styled[index] = renderRuns(width, tokens.SurfaceHi, runs...)
		case strings.HasPrefix(line, pickerSelectedPrefix):
			styled[index] = renderRuns(width, tokens.Selection,
				run{text: pickerSelectedPrefix, foreground: tokens.Accent, background: tokens.Selection, bold: true},
				run{text: strings.TrimPrefix(line, pickerSelectedPrefix), foreground: tokens.Text, background: tokens.Selection, bold: true},
			)
		case strings.Contains(line, "failed") || strings.Contains(line, "Unavailable"):
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Error})
		case strings.Contains(line, "saved") || strings.Contains(line, "created!"):
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Success, bold: true})
		case isQuietFormLine(line):
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Muted})
		default:
			styled[index] = renderRuns(width, tokens.Surface, run{text: line, foreground: tokens.Text})
		}
	}
	return styled
}

// isQuietFormLine reports the placeholder and in-progress rows that should
// recede rather than compete with the form's actual fields.
func isQuietFormLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{"Loading", "no markers", "no messages", "Run `", "Reopen"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// pickerSelectedLine computes which row index of a picker's drawn lines
// holds the highlight: one header row plus the selection's offset inside the
// window pickerWindow draws. It mirrors the Lines builders' own windowing so
// the styled row is always the one carrying the "> " marker.
func pickerSelectedLine(selected, total, height int) int {
	if total <= 0 || height <= 1 {
		return -1
	}
	start, active := pickerWindow(selected, total, height)
	return 1 + active - start
}

// splitCells breaks a single-line string into its grapheme clusters, one per
// display cell in order. Double-width clusters occupy two cells, so the
// returned slice holds the cluster followed by an empty string for the
// continuation cell; styling the continuation identically keeps spans aligned
// with what hit-testing measures.
func splitCells(line string) []string {
	cells := make([]string, 0, len(line))
	graphemes := uniseg.NewGraphemes(line)
	for graphemes.Next() {
		cluster := graphemes.Str()
		cells = append(cells, cluster)
		for extra := uniseg.StringWidth(cluster) - 1; extra > 0; extra-- {
			cells = append(cells, "")
		}
	}
	return cells
}
