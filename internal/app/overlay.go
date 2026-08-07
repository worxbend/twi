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
