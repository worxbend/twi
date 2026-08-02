package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClickTabBarSwitchesTabs(t *testing.T) {
	model := keymapModel(t)
	if model.activeTab != tabChat {
		t.Fatalf("initial tab = %v, want chat", model.activeTab)
	}

	// "2:Stream Info" starts after " *1:Chat" plus the two-space join.
	model = leftClick(model, 12, 0)
	if model.activeTab != tabStreamInfo {
		t.Fatalf("tab after clicking Stream Info = %v, want stream info", model.activeTab)
	}

	model = leftClick(model, 2, 0)
	if model.activeTab != tabChat {
		t.Fatalf("tab after clicking Chat = %v, want chat", model.activeTab)
	}
}

func TestClickSidebarCloseAffordanceClosesChannel(t *testing.T) {
	model := keymapModel(t)
	layout := model.layout()
	if layout.sidebarWidth <= 0 {
		t.Fatal("test setup: sidebar not visible")
	}
	model.focus = mockFocusSidebar
	model.syncSidebarSelectionToActive()

	row := layout.tabBarHeight + layout.statusHeight + 1
	// Clicking the name switches; only the trailing glyph closes.
	model = leftClick(model, 3, row)
	if got := len(model.channels.channelNames()); got != 2 {
		t.Fatalf("channels after clicking a name = %d, want 2", got)
	}

	model = leftClick(model, layout.sidebarWidth-2, row)
	if got := model.channels.channelNames(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("channels after clicking the close glyph = %#v, want [beta]", got)
	}
}

func TestClickOverlayRowRunsThatEntry(t *testing.T) {
	model := keymapModel(t)
	model.channelPicker = channelPickerState{open: true}
	layout := model.layout()
	if layout.channelPickerHeight <= 0 {
		t.Fatal("test setup: channel picker not laid out")
	}

	entries := model.channelPickerEntries()
	if len(entries) < 2 {
		t.Fatalf("test setup: %d picker entries, want at least 2", len(entries))
	}
	// Row 0 of the overlay content is the header, so the second entry sits
	// two rows below the pane's top border.
	top := layout.tabBarHeight + layout.statusHeight + layout.chatHeight
	model = leftClick(model, 4, top+3)
	if model.channelPicker.open {
		t.Fatal("channelPicker.open after clicking an entry = true, want false")
	}
	if got, want := model.activeChannelName(), entries[1].login; got != want {
		t.Fatalf("active channel after clicking entry 1 = %q, want %q", got, want)
	}
}

func TestWheelStillScrollsChatWithNoOverlayOpen(t *testing.T) {
	model := keymapModel(t)
	model.activeChannelState().messages = numberedMockMessages("alpha", 40)
	layout := model.layout()

	updated, _ := model.Update(tea.MouseMsg(tea.MouseEvent{
		X:      layout.sidebarWidth + 2,
		Y:      layout.tabBarHeight + layout.statusHeight + 1,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}))
	model = updated.(mockShellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("scrollOffset after a wheel-up over chat = 0, want scrolled")
	}
}
