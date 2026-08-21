package app

// anyOverlayOpen reports whether a modal overlay (command palette, inspect
// panel, theme settings, emote picker, channel picker, or category picker)
// currently covers the chat/composer view. Widgets use this instead of checking each overlay
// flag individually.
func (m shellModel) anyOverlayOpen() bool {
	return m.palette.open || m.inspectOpen || m.themeSettings.open || m.emotePicker.open ||
		m.channelPicker.open || m.categoryPicker.open
}

// overlayKind names one of the mutually exclusive overlays that can cover the
// chat view.
//
// It is a type rather than a bare string because closeOtherOverlays is driven
// entirely by which one to keep, and the names did not visibly match the
// fields they guarded -- "emotes" kept m.emotePicker, "channels" kept
// m.channelPicker. A typo in an untyped literal compiles happily and closes
// the overlay that was just opened, which looks like the key press being
// ignored. With a named type the compiler catches it.
type overlayKind string

const (
	// overlayNone keeps nothing open: every overlay is closed.
	overlayNone     overlayKind = ""
	overlayPalette  overlayKind = "palette"
	overlayInspect  overlayKind = "inspect"
	overlayTheme    overlayKind = "theme"
	overlayEmotes   overlayKind = "emotes"
	overlayChannels overlayKind = "channels"
	overlayCategory overlayKind = "category"
)

// closeOtherOverlays closes every overlay except keep. Overlays are mutually
// exclusive: opening one always closes the others.
func (m *shellModel) closeOtherOverlays(keep overlayKind) {
	if keep != overlayPalette {
		m.palette = commandPaletteState{}
	}
	if keep != overlayInspect {
		m.inspectOpen = false
	}
	if keep != overlayTheme {
		m.themeSettings = themeSettingsState{}
	}
	if keep != overlayEmotes {
		m.emotePicker = emotePickerState{}
	}
	if keep != overlayChannels {
		m.channelPicker = channelPickerState{}
	}
	if keep != overlayCategory {
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
