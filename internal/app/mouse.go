package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/twitch"
)

// handleMouse is the entry point for every mouse event the terminal reports.
// It scrolls chat on a wheel event and otherwise routes a left-button press
// through the regions of the screen in the order they are stacked, from the
// overlay on top down to the chat pane behind it.
func (m *shellModel) handleMouse(msg tea.MouseMsg) (shellModel, tea.Cmd) {
	if !m.mouseEnabled {
		return *m, nil
	}

	layout := m.layout()
	event := tea.MouseEvent(msg)
	if m.mouseInChatRegion(event, layout) && !m.anyOverlayOpen() {
		switch {
		case isMouseWheelUp(event):
			m.scrollBy(3)
			return *m, nil
		case isMouseWheelDown(event):
			m.scrollBy(-3)
			return *m, nil
		}
	}

	if !isMouseLeftPress(event) {
		return *m, nil
	}

	// An open overlay covers the composer and part of chat, so it gets first
	// refusal on every click; anything landing outside it dismisses nothing
	// and falls through to the regions still visible above.
	if model, cmd, handled := m.handleOverlayMouse(event, layout); handled {
		return model, cmd
	}
	if tab, ok := m.tabAtMouse(event, layout); ok {
		return m.switchToTab(tab)
	}
	if index, closeHit, ok := m.sidebarRowAtMouse(event, layout); ok {
		state := m.channels.states[m.channels.order[index]]
		if state == nil {
			return *m, nil
		}
		if closeHit {
			return m.closeChannel(state.name)
		}
		m.focus = focusSidebar
		m.panes.sidebarSelected = index
		if m.channels.setActive(state.name) {
			m.clampScroll()
			return m.withAsyncAssetCommands(nil)
		}
		return *m, nil
	}
	if m.mouseInComposer(event, layout) {
		m.focus = focusComposer
		return *m, nil
	}
	if message, ok := m.messageAtMouse(event, layout); ok {
		m.focus = focusChat
		m.activeChannelState().replyTo = replyContextFromMessage(message)
		return *m, nil
	}
	if m.mouseInChatRegion(event, layout) {
		m.focus = focusChat
	}
	return *m, nil
}

// isMouseWheelUp reports whether the event is one notch of the wheel away
// from the newest message.
func isMouseWheelUp(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonWheelUp
}

// isMouseWheelDown reports whether the event is one notch of the wheel back
// towards the newest message.
func isMouseWheelDown(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonWheelDown
}

// isMouseLeftPress reports whether the event is the primary button going
// down. Releases and drags are ignored so that one click acts once.
func isMouseLeftPress(event tea.MouseEvent) bool {
	return event.Button == tea.MouseButtonLeft && event.Action == tea.MouseActionPress
}

// mouseInChatRegion reports whether the cursor is inside the chat pane,
// borders included.
func (m shellModel) mouseInChatRegion(event tea.MouseEvent, layout shellLayout) bool {
	chatTop := layout.tabBarHeight + layout.statusHeight
	chatLeft := layout.sidebarWidth
	chatRight := layout.sidebarWidth + layout.chatWidth
	return event.X >= chatLeft &&
		event.X < chatRight &&
		event.Y >= chatTop &&
		event.Y < chatTop+layout.chatHeight
}

// mouseInComposer reports whether the cursor is inside the composer strip at
// the bottom of the window. The composer spans the full width, so only the
// row really decides.
func (m shellModel) mouseInComposer(event tea.MouseEvent, layout shellLayout) bool {
	composerTop := layout.tabBarHeight + layout.statusHeight + layout.chatHeight
	return event.X >= 0 &&
		event.X < layout.width &&
		event.Y >= composerTop &&
		event.Y < composerTop+layout.composerHeight
}

// messageAtMouse resolves the chat message drawn under the cursor, if any.
// Clicks on the pane border, on a group separator, or below the last message
// resolve to nothing.
func (m shellModel) messageAtMouse(event tea.MouseEvent, layout shellLayout) (twitch.ChatMessage, bool) {
	if !m.mouseInChatRegion(event, layout) || layout.chatContentHeight <= 0 {
		return twitch.ChatMessage{}, false
	}
	chatTop := layout.tabBarHeight + layout.statusHeight
	contentTop := chatTop
	if layout.chatFramed {
		contentTop++
	}
	contentRow := event.Y - contentTop
	if contentRow < 0 || contentRow >= layout.chatContentHeight {
		return twitch.ChatMessage{}, false
	}
	return m.messageAtVisibleChatRow(layout, contentRow)
}

// messageAtVisibleChatRow maps a row of chat content - counted from the top
// of the visible area, not from the top of the history - back to the message
// that produced it. It replays the same windowing arithmetic the renderer
// uses, so the hit boxes cannot drift from what is drawn.
func (m shellModel) messageAtVisibleChatRow(layout shellLayout, contentRow int) (twitch.ChatMessage, bool) {
	active := m.activeChannelState()
	blocks := m.visibleChatRowBlocks(layout)
	totalRows := chatRowBlockCount(blocks)

	start := totalRows - layout.chatContentHeight - active.scrollOffset
	if start < 0 {
		start = 0
	}
	target := start + contentRow
	if target < 0 || target >= totalRows {
		return twitch.ChatMessage{}, false
	}

	cursor := 0
	for _, block := range blocks {
		if block.separatorBefore {
			if target == cursor {
				return twitch.ChatMessage{}, false
			}
			cursor++
		}
		next := cursor + chatRowBlockRowCount(block)
		if target >= cursor && target < next {
			return selectableMessage(block.message)
		}
		cursor = next
	}
	return twitch.ChatMessage{}, false
}

// selectableMessage filters out messages that cannot be acted on. A message
// with no ID (a locally generated notice, for example) can be neither replied
// to nor inspected, so a click on it selects nothing.
func selectableMessage(message twitch.ChatMessage) (twitch.ChatMessage, bool) {
	if strings.TrimSpace(message.ID) == "" {
		return twitch.ChatMessage{}, false
	}
	return message, true
}

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
	closeHit = focused && index == m.panes.sidebarSelected && event.X >= closeStart
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
func (m shellModel) handleOverlayMouse(event tea.MouseEvent, layout shellLayout) (shellModel, tea.Cmd, bool) {
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
