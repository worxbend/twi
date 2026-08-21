package render

// Terminal cell arithmetic. Everything the renderer measures is counted in
// terminal cells rather than bytes or runes, because that is the only unit the
// screen has: one emoji is four bytes, one rune pair, one grapheme cluster --
// and two cells wide.

import (
	"strings"

	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/twitch"
)

// fitCells pads or truncates value so it occupies exactly width cells. It is
// what makes fixed-width fragments (avatars, badges, emotes) keep their column
// no matter how long the text inside them turns out to be.
func fitCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	out := value
	if textWidth(out) > width {
		out = truncateCells(out, width)
	}
	used := textWidth(out)
	if used < width {
		out += strings.Repeat(" ", width-used)
	}
	return out
}

// truncateCells shortens value to at most limit cells, ending it with "..."
// when there is room for the ellipsis to be worth showing.
func truncateCells(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if textWidth(value) <= limit {
		return value
	}
	if limit <= 3 {
		return takeCells(value, limit)
	}
	return takeCells(value, limit-3) + "..."
}

// takeCells returns the longest prefix of value that fits in limit cells,
// never splitting a grapheme cluster. A double-width cluster that would cross
// the limit is dropped rather than half-drawn.
func takeCells(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	for _, cluster := range graphemeStrings(value) {
		width := textWidth(cluster)
		if used+width > limit {
			break
		}
		builder.WriteString(cluster)
		used += width
	}
	return builder.String()
}

// graphemeStrings splits text into grapheme clusters: the pieces a reader
// perceives as single characters, which is what the renderer must not break
// apart. A family emoji or a letter with a combining accent is one element.
func graphemeStrings(value string) []string {
	graphemes := uniseg.NewGraphemes(value)
	out := make([]string, 0, len(value))
	for graphemes.Next() {
		out = append(out, graphemes.Str())
	}
	return out
}

// fragmentsWidth totals the cells a run of fragments reserves.
func fragmentsWidth(fragments []Fragment) int {
	width := 0
	for _, fragment := range fragments {
		width += fragment.Width()
	}
	return width
}

// textWidth is how many terminal cells a string occupies.
func textWidth(value string) int {
	return uniseg.StringWidth(value)
}

// isMentionPart reports whether a cluster can be part of a Twitch login, which
// is how the renderer finds where an "@name" mention ends.
func isMentionPart(cluster string) bool {
	for _, r := range cluster {
		return twitch.IsLoginRune(r)
	}
	return false
}

// emptyFallback substitutes fallback for an empty value.
func emptyFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// compactWhitespace collapses every run of whitespace, newlines included, into
// a single space, so quoted text stays on one line.
func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
