package render

import "strings"

// wrap lays a message out across as many rows of width cells as it needs.
//
// The prefix (timestamp, badges, name) is placed first and its width becomes
// the indent for every continuation row, so a wrapped message forms a block
// under its author instead of running back to the left margin. A prefix that
// is already as wide as the terminal would leave no room for text, so the
// indent falls back to half the width.
func wrap(prefix, content []Fragment, width int) []Row {
	if width <= 0 {
		return nil
	}

	prefixWidth := fragmentsWidth(prefix)
	indentWidth := prefixWidth
	if indentWidth >= width {
		indentWidth = width / 2
	}

	rows := make([]Row, 0, 2)
	current := Row{}
	used := 0
	rows, current, used = appendWrappedFragments(rows, current, used, prefix, width, 0)
	// The final width is not needed: nothing is appended after content, so
	// the trailing row is emitted as-is.
	rows, current, _ = appendWrappedFragments(rows, current, used, content, width, indentWidth)
	rows = append(rows, current)
	return rows
}

// appendWrappedFragments places fragments onto the row being built, breaking
// to a new row whenever the next piece would overflow width. It takes and
// returns the work in progress -- the rows finished so far, the row still
// being filled, and how many cells that row has used -- so callers can lay
// down several runs of fragments in sequence.
//
// Fragments that must stay whole (see Fragment.Atomic) and fragments with a
// reserved width are placed as units; anything else is broken between words,
// and then between grapheme clusters if a single word is wider than the row.
func appendWrappedFragments(rows []Row, current Row, used int, fragments []Fragment, width, indentWidth int) ([]Row, Row, int) {
	for _, fragment := range fragments {
		if fragment.WidthCells > 0 || fragment.Atomic() {
			fragmentWidth := fragment.Width()
			if fragmentWidth == 0 {
				continue
			}
			if used+fragmentWidth > width && used > indentWidth {
				rows, current, used = breakToContinuation(rows, current, indentWidth)
			}
			if indentIsTheObstacle(used, fragmentWidth, width, indentWidth) && fragmentWidth <= width {
				// The fragment cannot fit beside the indent but would fit on
				// a full-width row, so give up the indent.
				//
				// The row being abandoned must be emitted first if it holds
				// anything real. On the content pass, indentWidth is exactly
				// the prefix width, so `used == indentWidth` is also true on
				// the very first row -- where `current` holds the timestamp,
				// badges and author name. Discarding it unconditionally
				// dropped the whole prefix whenever a message opened with a
				// long mention or emote, leaving an unattributed line in
				// chat at ordinary terminal widths.
				if rowHasContent(current) {
					rows = append(rows, current)
				}
				current = Row{}
				used = 0
			}
			if used+fragmentWidth <= width {
				current.Append(fragment)
				used += fragmentWidth
				continue
			}
		}

		// Prefer a break between words. Chat is prose, and breaking mid-word
		// makes it materially harder to read at the speed a busy channel
		// moves. A word wider than the line still falls through to
		// cluster-by-cluster wrapping below, so nothing becomes unrenderable.
		for _, chunk := range wrapChunks(fragment.Text) {
			chunkWidth := textWidth(chunk)
			if chunkWidth > 0 && chunkWidth <= width-indentWidth &&
				used+chunkWidth > width && used > indentWidth &&
				strings.TrimSpace(chunk) != "" {
				rows, current, used = breakToContinuation(rows, current, indentWidth)
			}
			rows, current, used = appendWrappedClusters(rows, current, used, fragment, chunk, width, indentWidth)
		}
	}
	return rows, current, used
}

// wrapChunks splits text into word-sized pieces, each a run of non-space
// characters together with the spaces that follow it. Keeping the trailing
// spaces attached means a break taken before a chunk lands between words
// rather than stranding a space at the start of the next row.
func wrapChunks(text string) []string {
	if text == "" {
		return nil
	}
	var chunks []string
	var current strings.Builder
	inTrailingSpace := false
	for _, cluster := range graphemeStrings(text) {
		if cluster == "\n" {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, cluster)
			inTrailingSpace = false
			continue
		}
		isSpace := strings.TrimSpace(cluster) == ""
		if !isSpace && inTrailingSpace {
			chunks = append(chunks, current.String())
			current.Reset()
			inTrailingSpace = false
		}
		current.WriteString(cluster)
		if isSpace {
			inTrailingSpace = true
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// appendWrappedClusters places text one grapheme cluster at a time, keeping
// the styling of the fragment it came from. This is the last resort of the
// wrapper: a word too wide for the row, or an explicit newline inside a
// fragment, is handled here. Working in grapheme clusters rather than bytes or
// runes is what keeps an emoji or an accented letter from being cut in half.
func appendWrappedClusters(rows []Row, current Row, used int, fragment Fragment, text string, width, indentWidth int) ([]Row, Row, int) {
	for _, cluster := range graphemeStrings(text) {
		if cluster == "\n" {
			rows, current, used = breakToContinuation(rows, current, indentWidth)
			continue
		}

		clusterWidth := textWidth(cluster)
		if used+clusterWidth > width && used > indentWidth {
			rows, current, used = breakToContinuation(rows, current, indentWidth)
			if strings.TrimSpace(cluster) == "" {
				continue
			}
		}
		if indentIsTheObstacle(used, clusterWidth, width, indentWidth) {
			// The cluster does not fit beside the indent, so give up the
			// indent and let it start a full-width row.
			rows, current, used = breakToContinuation(rows, current, 0)
		}

		next := fragment
		next.Text = cluster
		next.WidthCells = 0
		current.Append(next)
		used += clusterWidth
	}
	return rows, current, used
}

// breakToContinuation ends the row being built and opens the next one. The
// finished row is handed back for emission, and the new row starts with
// indentWidth cells of padding so wrapped text lines up under the first row's
// message text instead of under its timestamp and name. An indentWidth of zero
// starts a plain full-width row.
func breakToContinuation(rows []Row, current Row, indentWidth int) ([]Row, Row, int) {
	return append(rows, current), continuationRow(indentWidth), indentWidth
}

// indentIsTheObstacle reports whether the row being built holds nothing but
// its indent and the next piece of pieceWidth cells has already overflowed the
// line. Giving up the indent is then the only remaining way to make room.
//
// Callers add their own extra conditions: appendWrappedFragments also requires
// that the fragment would fit on a full-width row, because an over-long
// fragment gains nothing from the extra cells and is wrapped cluster by
// cluster instead.
func indentIsTheObstacle(used, pieceWidth, width, indentWidth int) bool {
	return used+pieceWidth > width && used == indentWidth && used > 0
}

// continuationRow starts a row pre-filled with indentWidth spaces, which is
// how wrapped text is lined up under the first row's message text.
func continuationRow(indentWidth int) Row {
	if indentWidth <= 0 {
		return Row{}
	}
	return Row{Fragments: []Fragment{{
		Kind: FragmentText,
		Text: strings.Repeat(" ", indentWidth),
	}}}
}

// rowHasContent reports whether a row holds anything other than indent
// padding, so an abandoned continuation row can be discarded while a row
// carrying real fragments is emitted.
func rowHasContent(row Row) bool {
	for _, fragment := range row.Fragments {
		if strings.TrimSpace(fragment.Text) != "" {
			return true
		}
	}
	return false
}
