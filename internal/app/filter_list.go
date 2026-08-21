package app

import tea "github.com/charmbracelet/bubbletea"

// filterList is the "type to narrow, arrow to pick" state that every
// searchable overlay in the shell needs: the command palette, the emote
// picker, the channel picker and the category picker all work this way.
//
// The rows themselves are deliberately not stored here. Each overlay builds
// its own rows on demand (from the command table, from Twitch search results,
// from the follow list), so the methods below take the current row count as an
// argument instead of holding the rows.
type filterList struct {
	// query is the raw text typed into the overlay's search field.
	query string
	// selected is the index of the highlighted row within the rows the
	// overlay is currently showing, which is the already-filtered list.
	selected int
}

// move steps the highlight by delta rows, wrapping around both ends: pressing
// up on the first row lands on the last one and down on the last row lands
// back on the first. count is how many rows the overlay is showing right now;
// when there are none the highlight is parked at 0.
func (l *filterList) move(delta, count int) {
	if count <= 0 {
		l.selected = 0
		return
	}
	l.selected += delta
	if l.selected < 0 {
		l.selected = count - 1
	}
	if l.selected >= count {
		l.selected = 0
	}
}

// clamp pulls the highlight back inside the row list after the rows changed
// underneath it - a new query filtered some away, a search returned fewer
// results. Unlike move it does not wrap: an index past the end lands on the
// last row rather than jumping to the first.
func (l *filterList) clamp(count int) {
	if count <= 0 {
		l.selected = 0
		return
	}
	if l.selected < 0 {
		l.selected = 0
	}
	if l.selected >= count {
		l.selected = count - 1
	}
}

// insert appends typed characters to the query and sends the highlight back to
// the top row. The highlight is reset because the row that was highlighted
// before the edit is unlikely to still sit at the same index once the narrower
// query is applied.
func (l *filterList) insert(runes []rune) {
	l.query += string(runes)
	l.selected = 0
}

// deleteRune removes the last character of the query and, like insert, sends
// the highlight back to the top row. It removes one whole rune rather than one
// byte, so a multi-byte character such as an emoji disappears on a single
// backspace instead of leaving half of it behind.
func (l *filterList) deleteRune() {
	if l.query == "" {
		return
	}
	runes := []rune(l.query)
	l.query = string(runes[:len(runes)-1])
	l.selected = 0
}

// clearQuery empties the search field (ctrl+u) and sends the highlight back to
// the top row.
func (l *filterList) clearQuery() {
	l.query = ""
	l.selected = 0
}

// handleFilterListKey applies the keys that every searchable overlay treats
// identically: up/down/tab move the highlight, backspace and ctrl+u edit the
// query, and space or any other printable text is appended to it.
//
// count is how many rows the overlay is showing, which the highlight needs in
// order to wrap around the ends of the list.
//
// The return value reports whether the key was one of the shared ones, leaving
// each overlay's own handler to deal with the keys that are specific to it:
// escape, enter, and any command it has to run after the query changed.
func handleFilterListKey(msg tea.KeyMsg, list *filterList, count int) (handled bool) {
	switch msg.Type {
	case tea.KeyUp:
		list.move(-1, count)
	case tea.KeyDown, tea.KeyTab:
		list.move(1, count)
	case tea.KeyBackspace, tea.KeyCtrlH:
		list.deleteRune()
	case tea.KeyCtrlU:
		list.clearQuery()
	case tea.KeySpace:
		list.insert([]rune{' '})
	case tea.KeyRunes:
		list.insert(msg.Runes)
	default:
		return false
	}
	return true
}
