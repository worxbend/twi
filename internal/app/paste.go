package app

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// handlePaste routes clipboard text directly to the active input. Pasted
// shortcut names and single letters must never invoke commands or send chat.
func (m shellModel) handlePaste(content string) (shellModel, tea.Cmd) {
	content = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, content)
	switch {
	case m.splashActive():
		m.frames.splashSkipped = true
	case m.palette.open:
		m.palette.insert([]rune(content))
		m.palette.clamp(len(m.visibleCommandPaletteCommands()))
		m.refreshPaletteReveal(time.Now())
	case m.emotePicker.open:
		m.emotePicker.insert([]rune(content))
		m.emotePicker.clamp(len(m.visibleEmotePickerEntries()))
	case m.channelPicker.open:
		m.channelPicker.insert([]rune(content))
	case m.categoryPicker.open:
		m.categoryPicker.insert([]rune(content))
		return m, m.debounceCategorySearch()
	case m.themeSettings.open:
	case m.activeTab == tabStreamInfo && m.streamInfo.editing:
		m.streamInfo.editBuffer += content
	case m.activeTab == tabMisc && m.misc.editing:
		m.misc.editBuffer += content
	case m.activeTab == tabChat && m.focus == focusComposer:
		m.insertComposerText(content)
		m.resetMentionSelection()
	}
	return m, nil
}
