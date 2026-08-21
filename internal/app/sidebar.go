package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// sidebarVisibility is the user's standing choice for the channel sidebar.
// The default is auto: show it once a second channel is open, which is when
// a channel list starts earning its width. space+e overrides that either
// way for the session.
type sidebarVisibility int

const (
	sidebarAuto sidebarVisibility = iota
	sidebarShown
	sidebarHidden
)

// toggleSidebar flips the sidebar between shown and hidden, starting from
// whatever the current layout actually shows, so the first press always does
// the visible thing regardless of what auto had decided.
func (m *shellModel) toggleSidebar() {
	if m.layout().sidebarWidth > 0 {
		m.panes.sidebarVisibility = sidebarHidden
		if m.focus == focusSidebar {
			m.focus = focusChat
		}
		return
	}
	m.panes.sidebarVisibility = sidebarShown
	m.clampSidebarSelection()
}

// sidebarVisibleFor decides whether the channel sidebar has room and reason
// to be drawn. Width and chat height are hard constraints in every mode: a
// sidebar that would leave no usable chat column helps nobody.
func (m shellModel) sidebarVisibleFor(width, chatHeight int) bool {
	if width < sidebarMinWidth || chatHeight < 3 {
		return false
	}
	switch m.panes.sidebarVisibility {
	case sidebarShown:
		return true
	case sidebarHidden:
		return false
	default:
		return len(m.channels.channelNames()) >= 2
	}
}

// clampSidebarSelection keeps the highlighted sidebar row inside the open
// channel list as channels are opened and closed.
func (m *shellModel) clampSidebarSelection() {
	names := m.channels.channelNames()
	if len(names) == 0 {
		m.panes.sidebarSelected = 0
		return
	}
	if m.panes.sidebarSelected < 0 {
		m.panes.sidebarSelected = 0
	}
	if m.panes.sidebarSelected >= len(names) {
		m.panes.sidebarSelected = len(names) - 1
	}
}

func (m *shellModel) moveSidebarSelection(delta int) {
	names := m.channels.channelNames()
	if len(names) == 0 {
		m.panes.sidebarSelected = 0
		return
	}
	m.panes.sidebarSelected += delta
	if m.panes.sidebarSelected < 0 {
		m.panes.sidebarSelected = len(names) - 1
	}
	if m.panes.sidebarSelected >= len(names) {
		m.panes.sidebarSelected = 0
	}
}

func (m shellModel) selectedSidebarChannel() string {
	names := m.channels.channelNames()
	if len(names) == 0 {
		return ""
	}
	index := m.panes.sidebarSelected
	if index < 0 || index >= len(names) {
		index = 0
	}
	return names[index]
}

// handleSidebarKey implements the sidebar's vim-style navigation: j/k move,
// enter/l switch, x/d close, esc/h leave for the chat pane.
func (m shellModel) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyUp:
		m.moveSidebarSelection(-1)
		return m, nil, true
	case tea.KeyDown:
		m.moveSidebarSelection(1)
		return m, nil, true
	case tea.KeyEsc, tea.KeyLeft:
		m.focus = focusChat
		return m, nil, true
	case tea.KeyEnter, tea.KeyRight:
		model, cmd := m.switchToSelectedSidebarChannel()
		return model, cmd, true
	case tea.KeyDelete:
		model, cmd := m.closeChannel(m.selectedSidebarChannel())
		return model, cmd, true
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return m, nil, false
		}
		switch msg.Runes[0] {
		case 'j':
			m.moveSidebarSelection(1)
			return m, nil, true
		case 'k':
			m.moveSidebarSelection(-1)
			return m, nil, true
		case 'l':
			model, cmd := m.switchToSelectedSidebarChannel()
			return model, cmd, true
		case 'h':
			m.focus = focusChat
			return m, nil, true
		case 'x', 'd':
			model, cmd := m.closeChannel(m.selectedSidebarChannel())
			return model, cmd, true
		}
		// i/o/a mean "start typing" everywhere outside the composer, the
		// sidebar included.
		if isInsertRune(msg.Runes[0]) {
			m.focus = focusComposer
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m shellModel) switchToSelectedSidebarChannel() (tea.Model, tea.Cmd) {
	channel := m.selectedSidebarChannel()
	if channel == "" {
		return m, nil
	}
	if !m.channels.setActive(channel) {
		return m, nil
	}
	m.clampScroll()
	return m.withAsyncAssetCommands(nil)
}

// syncSidebarSelectionToActive lines the highlight up with the channel the
// chat pane is showing, so entering the sidebar starts from where the user
// already is.
func (m *shellModel) syncSidebarSelectionToActive() {
	names := m.channels.channelNames()
	active := m.channels.activeName()
	for i, name := range names {
		if channelKey(name) == channelKey(active) {
			m.panes.sidebarSelected = i
			return
		}
	}
	m.clampSidebarSelection()
}
