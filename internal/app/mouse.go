package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// tabAtMouse resolves which tab-bar entry sits under the cursor by rebuilding
// the same label run tabBarTabs renders, so the hit boxes cannot drift from
// what is drawn.
func (m shellModel) tabAtMouse(event tea.MouseEvent, layout shellLayout) (shellTab, bool) {
	if layout.tabBarHeight <= 0 || event.Y != 0 {
		return 0, false
	}
	compact := !strings.Contains(m.tabBarTabs(false), ":")
	// tabBarTabs starts with one leading space and joins labels with two.
	cursor := 1
	for i, entry := range shellTabs {
		marker := " "
		if compact {
			marker = ""
		}
		if entry.tab == m.activeTab {
			marker = "*"
		}
		label := fmt.Sprintf("%s%d", marker, i+1)
		if !compact {
			label += ":" + entry.label
		}
		width := uniseg.StringWidth(label)
		if event.X >= cursor && event.X < cursor+width {
			return entry.tab, true
		}
		cursor += width + 2
	}
	return 0, false
}

// sidebarRowAtMouse resolves the sidebar row under the cursor and reports
// whether the click landed on that row's close affordance rather than its
// name. The affordance is only drawn on the focused, highlighted row, so a
// click can only close what the user can see is closable.
func (m shellModel) sidebarRowAtMouse(event tea.MouseEvent, layout shellLayout) (index int, closeHit bool, ok bool) {
	if layout.sidebarWidth <= 0 || event.X < 0 || event.X >= layout.sidebarWidth {
		return 0, false, false
	}
	chatTop := layout.tabBarHeight + layout.statusHeight
	if event.Y < chatTop+1 || event.Y >= chatTop+layout.chatHeight-1 {
		return 0, false, false
	}
	index = event.Y - chatTop - 1
	if index < 0 || index >= len(m.channels.order) {
		return 0, false, false
	}
	// The affordance occupies the last two content cells of the row, inside
	// the pane border.
	closeStart := layout.sidebarWidth - 1 - uniseg.StringWidth(sidebarCloseAffordance)
	focused := m.focus == focusSidebar && !m.anyOverlayOpen()
	closeHit = focused && index == m.sidebarSelected && event.X >= closeStart
	return index, closeHit, true
}

// overlayRowAtMouse resolves which row of the bottom overlay (command
// palette, emote picker, or channel picker) sits under the cursor. All three
// render a one-line header followed by their entries, so one hit test covers
// them.
func overlayRowAtMouse(event tea.MouseEvent, top, height, contentHeight int, framed bool) (int, bool) {
	if height <= 0 || contentHeight <= 1 {
		return 0, false
	}
	contentTop := top
	if framed {
		contentTop++
	}
	// Row 0 of the content is the header; entries start below it.
	row := event.Y - contentTop - 1
	if row < 0 || row >= contentHeight-1 {
		return 0, false
	}
	return row, true
}

// overlayTop returns the first screen row of the bottom overlay stack. Only
// one overlay is ever open at a time (closeOtherOverlays enforces it), so
// they all start directly below the chat pane.
func (m shellModel) overlayTop(layout shellLayout) int {
	return layout.tabBarHeight + layout.statusHeight + layout.chatHeight
}

// handleOverlayMouse routes a click inside an open bottom overlay to that
// overlay's selection, then runs it - matching the keyboard flow where
// moving the selection and pressing enter are one gesture with a mouse.
func (m shellModel) handleOverlayMouse(event tea.MouseEvent, layout shellLayout) (tea.Model, tea.Cmd, bool) {
	top := m.overlayTop(layout)
	switch {
	case m.palette.open && layout.paletteHeight > 0:
		row, ok := overlayRowAtMouse(event, top, layout.paletteHeight, layout.paletteContentHeight, layout.paletteFramed)
		if !ok {
			return m, nil, false
		}
		commands := m.visibleCommandPaletteCommands()
		index := paletteWindowStart(m.palette.selected, len(commands), layout.paletteContentHeight-1) + row
		if index < 0 || index >= len(commands) {
			return m, nil, true
		}
		m.palette.selected = index
		model, cmd := m.executeCommandPaletteSelection()
		return model, cmd, true
	case m.emotePicker.open && layout.emotePickerHeight > 0:
		row, ok := overlayRowAtMouse(event, top, layout.emotePickerHeight, layout.emotePickerContentHeight, layout.emotePickerFramed)
		if !ok {
			return m, nil, false
		}
		entries := m.visibleEmotePickerEntries()
		index := paletteWindowStart(m.emotePicker.selected, len(entries), layout.emotePickerContentHeight-1) + row
		if index < 0 || index >= len(entries) {
			return m, nil, true
		}
		m.emotePicker.selected = index
		model, cmd := m.executeEmotePickerSelection()
		return model, cmd, true
	case m.channelPicker.open && layout.channelPickerHeight > 0:
		row, ok := overlayRowAtMouse(event, top, layout.channelPickerHeight, layout.channelPickerContentHeight, layout.channelPickerFramed)
		if !ok {
			return m, nil, false
		}
		entries := m.channelPickerEntries()
		index := paletteWindowStart(m.channelPicker.selected, len(entries), layout.channelPickerContentHeight-1) + row
		if index < 0 || index >= len(entries) {
			return m, nil, true
		}
		m.channelPicker.selected = index
		model, cmd := m.commitChannelPickerSelection()
		return model, cmd, true
	}
	return m, nil, false
}
