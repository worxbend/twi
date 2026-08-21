package app

// anyOverlayOpen reports whether a modal overlay (command palette, inspect
// panel, theme settings, emote picker, channel picker, or category picker)
// currently covers the chat/composer view. Widgets use this instead of checking each overlay
// flag individually.
func (m shellModel) anyOverlayOpen() bool {
	return m.palette.open || m.inspectOpen || m.themeSettings.open || m.emotePicker.open ||
		m.channelPicker.open || m.categoryPicker.open
}

// closeOtherOverlays closes every overlay except the one named by keep
// ("palette", "inspect", "theme", "emotes", "channels", "category", or ""
// to close all).
// Overlays are mutually exclusive: opening one always closes the others.
func (m *shellModel) closeOtherOverlays(keep string) {
	if keep != "palette" {
		m.palette = commandPaletteState{}
	}
	if keep != "inspect" {
		m.inspectOpen = false
	}
	if keep != "theme" {
		m.themeSettings = themeSettingsState{}
	}
	if keep != "emotes" {
		m.emotePicker = emotePickerState{}
	}
	if keep != "channels" {
		m.channelPicker = channelPickerState{}
	}
	if keep != "category" {
		m.categoryPicker = categoryPickerState{}
	}
}

// padPaneLines fits lines to a pane exactly height rows tall and width cells
// wide, padding with blanks and truncating any overflow.
//
// Panes are drawn into a fixed rectangle, so a short list has to be filled out
// rather than left ragged -- otherwise whatever was on screen before shows
// through the gap.
func padPaneLines(lines []string, width, height int) []string {
	out := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, fitLine(line, width))
	}
	return out
}

// pickerWindow decides which slice of a selectable list a pane should draw and
// which entry in it is highlighted.
//
// It returns the index to start drawing from and the selection to highlight,
// with an out-of-range selection folded back to the first entry: the lists
// behind these panes are refiltered as you type, so a selection can outlive
// the entry it pointed at. One row of the pane goes to the filter line above
// the list, which is why the window is one shorter than the pane.
func pickerWindow(selected, total, paneHeight int) (start, active int) {
	if selected < 0 || selected >= total {
		selected = 0
	}
	return paletteWindowStart(selected, total, paneHeight-1), selected
}
