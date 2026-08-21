package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The space leader chord follows the AstroNvim convention: space starts a
// chord outside the composer, and the next key picks the action. Anything
// unbound simply cancels, so a stray space never leaves the shell in a mode
// the user has to escape from.
const (
	leaderSidebarRune       = 'e'
	leaderChannelPickerRune = 'c'
	leaderCloseChannelRune  = 'x'
	leaderInspectRune       = 'i'
	leaderActivityRune      = 'a'
)

// handleLeaderKey consumes the key following a pending space leader. It
// always clears the pending flag: a chord is exactly two keystrokes.
func (m shellModel) handleLeaderKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	m.leaderPending = false
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, nil
	}
	switch msg.Runes[0] {
	case leaderSidebarRune:
		m.toggleSidebar()
		return m, nil
	case leaderChannelPickerRune:
		return m, m.openChannelPicker()
	case leaderCloseChannelRune:
		return m.closeChannel(m.activeChannelName())
	case leaderInspectRune:
		m.toggleInspect()
		return m, nil
	case leaderActivityRune:
		m.toggleActivity()
		m.clampScroll()
		return m, nil
	}
	return m, nil
}

// isInsertRune reports whether a normal-mode key should move focus to the
// composer. vim's i/a/o all begin inserting text; the composer appends, so
// they are equivalent here.
func isInsertRune(r rune) bool {
	return r == 'i' || r == 'o' || r == 'a'
}

func (m *shellModel) toggleInspect() {
	m.inspectOpen = !m.inspectOpen
	if m.inspectOpen {
		m.closeOtherOverlays("inspect")
	}
	m.clampScroll()
}
