package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/assets"
)

// emotePickerState mirrors commandPaletteState's shape: a searchable,
// keyboard-navigable overlay list, opened with Ctrl+E for finding an emote
// not in the always-visible quick-select row (see emotesView in mock.go).
type emotePickerState struct {
	open     bool
	query    string
	selected int
}

func (m *mockShellModel) toggleEmotePicker() {
	if m.emotePicker.open {
		m.emotePicker = emotePickerState{}
		return
	}
	m.closeOtherOverlays("emotes")
	m.emotePicker = emotePickerState{open: true}
}

func (m mockShellModel) handleEmotePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.emotePicker = emotePickerState{}
		return m, nil
	case tea.KeyEnter:
		return m.executeEmotePickerSelection()
	case tea.KeyUp:
		m.moveEmotePickerSelection(-1)
	case tea.KeyDown, tea.KeyTab:
		m.moveEmotePickerSelection(1)
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.deleteEmotePickerRune()
	case tea.KeyCtrlU:
		m.emotePicker.query = ""
		m.emotePicker.selected = 0
	case tea.KeySpace:
		m.emotePicker.query += " "
		m.emotePicker.selected = 0
	case tea.KeyRunes:
		m.emotePicker.query += string(msg.Runes)
		m.emotePicker.selected = 0
	}
	m.clampEmotePickerSelection()
	return m, nil
}

func (m *mockShellModel) moveEmotePickerSelection(delta int) {
	entries := m.visibleEmotePickerEntries()
	if len(entries) == 0 {
		m.emotePicker.selected = 0
		return
	}
	m.emotePicker.selected += delta
	if m.emotePicker.selected < 0 {
		m.emotePicker.selected = len(entries) - 1
	}
	if m.emotePicker.selected >= len(entries) {
		m.emotePicker.selected = 0
	}
}

func (m *mockShellModel) deleteEmotePickerRune() {
	if m.emotePicker.query == "" {
		return
	}
	runes := []rune(m.emotePicker.query)
	m.emotePicker.query = string(runes[:len(runes)-1])
	m.emotePicker.selected = 0
}

func (m *mockShellModel) clampEmotePickerSelection() {
	entries := m.visibleEmotePickerEntries()
	if len(entries) == 0 {
		m.emotePicker.selected = 0
		return
	}
	if m.emotePicker.selected < 0 {
		m.emotePicker.selected = 0
	}
	if m.emotePicker.selected >= len(entries) {
		m.emotePicker.selected = len(entries) - 1
	}
}

// visibleEmotePickerEntries filters the active channel's resolved emote set
// by substring match on name (case-insensitive), same filtering style as
// the command palette.
func (m mockShellModel) visibleEmotePickerEntries() []assets.EmoteEntry {
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
func (m mockShellModel) executeEmotePickerSelection() (tea.Model, tea.Cmd) {
	entries := m.visibleEmotePickerEntries()
	if len(entries) == 0 {
		m.emotePicker = emotePickerState{}
		return m, nil
	}
	index := m.emotePicker.selected
	if index < 0 {
		index = 0
	}
	if index >= len(entries) {
		index = len(entries) - 1
	}
	m.activeChannelState().composerText += entries[index].Name + " "
	m.emotePicker = emotePickerState{}
	return m, nil
}

func (m mockShellModel) emotePickerView(layout mockShellLayout) string {
	contentWidth := layout.width
	if layout.emotePickerFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.emotePickerLines(contentWidth, layout.emotePickerContentHeight)
	content := strings.Join(lines, "\n")
	if !layout.emotePickerFramed {
		return fitBlock(content, layout.width, layout.emotePickerHeight)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.emotePickerContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(m.theme.Accent)).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) emotePickerLines(width, height int) []string {
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
		selected := m.emotePicker.selected
		if selected < 0 || selected >= len(entries) {
			selected = 0
		}
		maxEntries := height - 1
		start := paletteWindowStart(selected, len(entries), maxEntries)
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
