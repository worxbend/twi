package app

import tea "charm.land/bubbletea/v2"

// updateTerminalEvent handles what the terminal itself reports: keys, mouse
// activity, resizes, and the window gaining or losing focus.
func (m *shellModel) updateTerminalEvent(msg tea.Msg) (shellModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		model, cmd := m.handlePaste(msg.Content)
		return model, cmd, true
	case tea.KeyPressMsg:
		model, cmd := m.handleKey(msg)
		return model, cmd, true
	case tea.MouseMsg:
		// The theme page and category picker own the whole screen, so a
		// click has nothing to hit outside them.
		if m.themeSettings.open || m.categoryPicker.open {
			return *m, nil, true
		}
		model, cmd := m.handleMouse(msg)
		return model, cmd, true
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.refreshActiveRevealRows()
		m.clampScroll()
		model, cmd := m.withAsyncAssetCommands(nil)
		return model, cmd, true
	case tea.FocusMsg:
		m.terminalFocused = true
		return *m, nil, true
	case tea.BlurMsg:
		m.terminalFocused = false
		return *m, nil, true
	}
	return *m, nil, false
}

// handleRuneShortcut handles the single-character shortcuts of the chat view.
//
// Each is gated on where the focus is, because inside the composer these are
// ordinary characters in the message being typed. handled is false when the
// rune means nothing in the current focus, leaving the caller to treat it as
// text.
//
// This used to be nine consecutive `if` statements that each re-tested
// `m.focus == focusChat && len([]rune(msg.Text)) == 1` before comparing one rune.
// Testing the focus once and switching on the rune says the same thing, and
// adding a shortcut is now one case rather than another copy of the guard.
func (m *shellModel) handleRuneShortcut(r rune) (shellModel, tea.Cmd, bool) {
	// "?" toggles help everywhere except in the composer, where it is an
	// ordinary character in the message being typed.
	if r == '?' && m.focus != focusComposer {
		m.helpExpanded = !m.helpExpanded
		m.clampScroll()
		return *m, nil, true
	}
	// Pane resizing works from the chat view and from the sidebar, because
	// the sidebar is the one pane whose own width these keys adjust;
	// everywhere else they act on the activity column.
	if m.focus == focusChat || m.focus == focusSidebar {
		switch r {
		case '<':
			m.resizeFocusedPane(-paneResizeStep)
			m.clampScroll()
			return *m, nil, true
		case '>':
			m.resizeFocusedPane(paneResizeStep)
			m.clampScroll()
			return *m, nil, true
		case '=':
			m.resetPaneWidths()
			m.clampScroll()
			return *m, nil, true
		}
	}
	// Everything below belongs to the chat view alone.
	if m.focus != focusChat {
		return *m, nil, false
	}
	// The message-filter shortcuts are 1-4, and are looked up rather than
	// listed here so the filters stay defined in one place.
	if filter, ok := messageFilterForShortcutRune(r); ok {
		return *m, m.toggleActiveMessageFilter(filter), true
	}
	// i/o/a all enter the composer, matching vim's insert keys; the composer
	// has no cursor to position, so they differ only in muscle memory.
	if isInsertRune(r) {
		m.focus = focusComposer
		return *m, nil, true
	}
	switch r {
	case ']':
		if m.channels.switchBy(1) {
			m.clampScroll()
			model, cmd := m.withAsyncAssetCommands(nil)
			return model, cmd, true
		}
		return *m, nil, true
	case '[':
		if m.channels.switchBy(-1) {
			m.clampScroll()
			model, cmd := m.withAsyncAssetCommands(nil)
			return model, cmd, true
		}
		return *m, nil, true
	case '0':
		return *m, m.resetActiveMessageFilters(), true
	case 'q':
		return *m, tea.Quit, true
	case 'r':
		m.startReplyMode()
		return *m, nil, true
	case 'j':
		m.selectReplyMessage(1)
		return *m, nil, true
	case 'k':
		m.selectReplyMessage(-1)
		return *m, nil, true
	case 'K':
		// K is vim's "show me more about this", which is exactly what the
		// inspect panel does. It replaces the old bare i, now an insert key.
		m.toggleInspect()
		return *m, nil, true
	}
	return *m, nil, false
}

// handleAlwaysOnKey consumes the keys that must work no matter what is on
// screen: quitting, skipping the splash, switching tabs, and the three panel
// toggles. handled is false when the key was none of those, and the caller
// carries on with it.
//
// Its first few lines still run for every keypress, because disarming a
// pending ctrl+L confirmation is something any other key does.
func (m *shellModel) handleAlwaysOnKey(msg tea.KeyPressMsg) (shellModel, tea.Cmd, bool) {
	// Any key other than a second ctrl+L abandons a pending clear, so a
	// stray press cannot arm the confirmation and sit waiting for an
	// unrelated keystroke to trigger it later.
	if m.pendingClearChat && msg.String() != "ctrl+l" {
		m.pendingClearChat = false
	}
	switch {
	case msg.String() == "ctrl+c":
		return *m, tea.Quit, true
	case m.splashActive():
		m.frames.splashSkipped = true
		return *m, nil, true
	}
	if msg.Mod.Contains(tea.ModAlt) {
		if tab, ok := tabForShortcutRune(msg.Code); ok {
			model, cmd := m.switchToTab(tab)
			return model, cmd, true
		}
	}
	switch msg.String() {
	case "ctrl+p":
		m.toggleCommandPalette()
		return *m, nil, true
	case "ctrl+e":
		m.toggleEmotePicker()
		return *m, nil, true
	case "ctrl+t":
		m.toggleThemeSettings()
		return *m, nil, true
	}
	if m.handleDisplayToggleKey(msg) {
		return *m, nil, true
	}
	return *m, nil, false
}

// routeKeyToOpenPanel hands the key to whichever overlay or full-screen tab
// currently owns the keyboard. At most one of them is ever active, and the
// case order decides which wins if that were somehow not true.
func (m shellModel) routeKeyToOpenPanel(msg tea.KeyPressMsg) (shellModel, tea.Cmd, bool) {
	var (
		model shellModel
		cmd   tea.Cmd
	)
	switch {
	case m.palette.open:
		model, cmd = m.handleCommandPaletteKey(msg)
	case m.emotePicker.open:
		model, cmd = m.handleEmotePickerKey(msg)
	case m.themeSettings.open:
		model, cmd = m.handleThemeSettingsKey(msg)
	case m.channelPicker.open:
		model, cmd = m.handleChannelPickerKey(msg)
	case m.categoryPicker.open:
		model, cmd = m.handleCategoryPickerKey(msg)
	case m.activeTab == tabStreamInfo:
		model, cmd = m.handleStreamInfoKey(msg)
	case m.activeTab == tabMisc:
		model, cmd = m.handleMiscKey(msg)
	default:
		return m, nil, false
	}
	return model, cmd, true
}

// handleMentionKey lets the @mention strip claim tab, the arrows and esc, but
// only while it is actually offering completions, so those keys keep their
// normal meaning the rest of the time.
func (m *shellModel) handleMentionKey(msg tea.KeyPressMsg) (shellModel, tea.Cmd, bool) {
	if len(m.mentionSuggestions()) == 0 {
		return *m, nil, false
	}
	switch msg.String() {
	case "tab":
		if m.acceptMentionSuggestion() {
			return *m, nil, true
		}
	case "down":
		m.moveMentionSelection(1)
		return *m, nil, true
	case "up":
		m.moveMentionSelection(-1)
		return *m, nil, true
	case "esc":
		if m.dismissMentionSuggestions() {
			return *m, nil, true
		}
	}
	return *m, nil, false
}

func (m shellModel) handleKey(msg tea.KeyPressMsg) (shellModel, tea.Cmd) {
	if model, cmd, handled := m.handleAlwaysOnKey(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.routeKeyToOpenPanel(msg); handled {
		return model, cmd
	}
	if model, cmd, handled := m.handleMentionKey(msg); handled {
		return model, cmd
	}
	// The space leader chord is consumed before every other binding so a
	// pending leader can never be mistaken for a normal-mode key. It is
	// only ever armed outside the composer, where space is literal text.
	if m.leaderPending {
		return m.handleLeaderKey(msg)
	}
	if msg.String() == "space" && m.focus != focusComposer {
		m.leaderPending = true
		return m, nil
	}
	if m.focus == focusSidebar {
		if model, cmd, handled := m.handleSidebarKey(msg); handled {
			return model, cmd
		}
	}

	switch msg.String() {
	case "tab":
		m.cycleFocus()
	case "pgup":
		m.scrollBy(m.layout().chatContentHeight)
	case "pgdown":
		m.scrollBy(-m.layout().chatContentHeight)
	case "ctrl+l":
		// Clearing discards the whole retained backlog for the channel
		// and cannot be undone. It is one keystroke, next to keys used
		// constantly, on a tool that is often running during a live
		// broadcast -- so it asks once. The leader chord for closing a
		// channel and the sidebar's explicit x are already deliberate
		// enough not to need this.
		if m.pendingClearChat {
			m.pendingClearChat = false
			m.clearLocalChat()
			break
		}
		m.pendingClearChat = true
	case "ctrl+r":
		return m, m.requestReconnect()
	case "backspace":
		if m.focus == focusComposer {
			m.deleteComposerRune()
			m.resetMentionSelection()
		}
	case "esc":
		// esc is "leave insert mode" first: from the composer it always
		// returns to the chat view, keeping the draft intact.
		if m.focus == focusComposer {
			m.focus = focusChat
			return m, nil
		}
		if m.inspectOpen {
			m.inspectOpen = false
			m.clampScroll()
			return m, nil
		}
		m.activeChannelState().replyTo = nil
	case "up":
		if m.focus == focusChat {
			m.selectReplyMessage(-1)
		}
	case "down":
		if m.focus == focusChat {
			m.selectReplyMessage(1)
		}
	case "ctrl+u":
		if m.focus == focusComposer {
			m.activeChannelState().composerText = ""
		}
	case "enter":
		if m.focus == focusComposer {
			return m.queueComposerSend()
		}
	case "space":
		if m.focus == focusComposer {
			m.activeChannelState().composerText += " "
		}
	default:
		if msg.Text == "" {
			break
		}
		if len([]rune(msg.Text)) == 1 {
			if model, cmd, handled := m.handleRuneShortcut([]rune(msg.Text)[0]); handled {
				return model, cmd
			}
		}
		if m.focus == focusComposer {
			m.insertComposerText(msg.Text)
			m.resetMentionSelection()
		}
	}
	return m, nil
}
