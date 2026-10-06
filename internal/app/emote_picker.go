package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/assets"
)

// emotePickerState mirrors commandPaletteState's shape: a searchable,
// keyboard-navigable overlay list, opened with Ctrl+E for finding and
// inserting an emote without leaving the composer.
type emotePickerState struct {
	open bool
	// filterList holds the typed query and the highlighted row, shared with
	// the other searchable overlays so they cannot drift apart.
	filterList
}

func (m *shellModel) toggleEmotePicker() {
	if m.emotePicker.open {
		m.emotePicker = emotePickerState{}
		return
	}
	m.closeOtherOverlays(overlayEmotes)
	m.emotePicker = emotePickerState{open: true}
}

func (m shellModel) handleEmotePickerKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.emotePicker = emotePickerState{}
		return m, nil
	case tea.KeyEnter:
		return m.executeEmotePickerSelection()
	}
	handleFilterListKey(msg, &m.emotePicker.filterList, len(m.visibleEmotePickerEntries()))
	m.emotePicker.clamp(len(m.visibleEmotePickerEntries()))
	return m, nil
}

// visibleEmotePickerEntries filters the active channel's resolved emote set
// by substring match on name (case-insensitive), same filtering style as
// the command palette.
func (m shellModel) visibleEmotePickerEntries() []assets.EmoteEntry {
	all := m.activeEmoteEntries()
	query := strings.TrimSpace(strings.ToLower(m.emotePicker.query))
	if query == "" {
		return all
	}
	filtered := make([]assets.EmoteEntry, 0, len(all))
	for _, entry := range all {
		if strings.Contains(strings.ToLower(entry.Name), query) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// executeEmotePickerSelection appends the selected emote's name plus a
// trailing space to the composer (matching the composer's append-only text
// model) and closes the picker.
func (m shellModel) executeEmotePickerSelection() (shellModel, tea.Cmd) {
	entries := m.visibleEmotePickerEntries()
	if len(entries) == 0 {
		m.emotePicker = emotePickerState{}
		return m, nil
	}
	m.emotePicker.clamp(len(entries))
	m.activeChannelState().composerText += entries[m.emotePicker.selected].Name + " "
	m.emotePicker = emotePickerState{}
	return m, nil
}

func (m shellModel) emotePickerView(layout shellLayout) string {
	return m.renderOverlayPane(overlayPaneSpec{
		icon:          "😀",
		title:         "Emote Search",
		accent:        m.theme.Error,
		height:        layout.emotePickerHeight,
		contentHeight: layout.emotePickerContentHeight,
		framed:        layout.emotePickerFramed,
		lines: func(width, height int) []string {
			lines := m.emotePickerLines(width, height)
			selectedLine := pickerSelectedLine(m.emotePicker.selected, len(m.visibleEmotePickerEntries()), height)
			return m.stylePickerLines(lines, width, selectedLine)
		},
	})
}

func (m shellModel) emotePickerLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	query := m.emotePicker.query
	header := " Emote search"
	if query != "" {
		header += ": " + query
	}
	lines := []string{fitLine(header, width)}
	if height == 1 {
		return lines
	}

	entries := m.visibleEmotePickerEntries()
	if len(entries) == 0 {
		lines = append(lines, fitLine("  no matches", width))
	} else {
		start, selected := pickerWindow(m.emotePicker.selected, len(entries), height)
		for i := start; i < len(entries) && len(lines) < height; i++ {
			prefix := "  "
			if i == selected {
				prefix = "> "
			}
			lines = append(lines, fitLine(prefix+entries[i].Name, width))
		}
	}
	for len(lines) < height {
		lines = append(lines, fitLine("", width))
	}
	return lines[:height]
}
