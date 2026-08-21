package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/worxbend/twi/internal/config"
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
	model.focus = focusSidebar
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
	model = updated.(shellModel)
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("scrollOffset after a wheel-up over chat = 0, want scrolled")
	}
}

func TestMockShellMouseEventsWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockModel("alpha", cfg)
	model.channels.ensure("alpha").messages = numberedMockMessages("alpha", 12)
	model.channels.ensure("beta").messages = numberedMockMessages("beta", 3)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 88, Height: 16})
	model = updated.(shellModel)
	layout := model.layout()
	chatX := layout.sidebarWidth + 2
	contentY := layout.tabBarHeight + layout.statusHeight + 1

	updated, cmd := model.Update(tea.MouseMsg{
		X:      chatX,
		Y:      contentY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = updated.(shellModel)
	if cmd != nil {
		t.Fatalf("mouse wheel returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().scrollOffset == 0 {
		t.Fatal("mouse wheel up left scrollOffset at 0")
	}
	if !strings.Contains(model.View(), "message-08") {
		t.Fatalf("mouse-scrolled viewport missing older row:\n%s", model.View())
	}

	sidebarContentY := layout.tabBarHeight + layout.statusHeight + 1
	betaY := sidebarContentY + 1
	updated, cmd = model.Update(tea.MouseMsg{
		X:      2,
		Y:      betaY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(shellModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("channel click command produced %T, want nil or no-op", msg)
		}
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel after sidebar click = %q, want %q", got, want)
	}
	// Clicking a channel also focuses the channel list, so the close
	// affordance appears and j/k keep working on the list.
	if got, want := model.focus, focusSidebar; got != want {
		t.Fatalf("focus after sidebar click = %v, want %v", got, want)
	}
	if got, want := model.panes.sidebarSelected, 1; got != want {
		t.Fatalf("sidebar selection after click = %d, want %d", got, want)
	}

	composerY := layout.tabBarHeight + layout.statusHeight + layout.chatHeight + 1
	updated, _ = model.Update(tea.MouseMsg{
		X:      10,
		Y:      composerY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(shellModel)
	if got, want := model.focus, focusComposer; got != want {
		t.Fatalf("focus after composer click = %v, want %v", got, want)
	}

	if !model.channels.setActive("alpha") {
		t.Fatal("test setup failed to switch back to alpha")
	}
	model.activeChannelState().scrollOffset = 0
	layout = model.layout()
	latestY := layout.tabBarHeight + layout.statusHeight + 1 + layout.chatContentHeight - 1
	updated, _ = model.Update(tea.MouseMsg{
		X:      layout.sidebarWidth + 4,
		Y:      latestY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(shellModel)
	if model.activeChannelState().replyTo == nil || model.activeChannelState().replyTo.MessageID != "mock-11" {
		t.Fatalf("replyTo after message click = %#v, want mock-11", model.activeChannelState().replyTo)
	}
	if got, want := model.focus, focusChat; got != want {
		t.Fatalf("focus after message click = %v, want %v", got, want)
	}
}

func TestMockShellMouseEventsIgnoredWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.EnableMouse = false
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockModel("alpha", cfg)
	model.channels.ensure("alpha").messages = numberedMockMessages("alpha", 12)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 88, Height: 12})
	model = updated.(shellModel)
	layout := model.layout()

	events := []tea.MouseMsg{
		{X: layout.sidebarWidth + 2, Y: layout.tabBarHeight + layout.statusHeight + 1, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress},
		{X: 2, Y: layout.tabBarHeight + layout.statusHeight + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 10, Y: layout.tabBarHeight + layout.statusHeight + layout.chatHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: layout.sidebarWidth + 4, Y: layout.tabBarHeight + layout.statusHeight + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
	}
	for _, event := range events {
		updated, cmd := model.Update(event)
		if cmd != nil {
			t.Fatalf("disabled mouse event returned command %#v, want nil", cmd)
		}
		model = updated.(shellModel)
	}

	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel after disabled mouse events = %q, want %q", got, want)
	}
	if got := model.activeChannelState().scrollOffset; got != 0 {
		t.Fatalf("scrollOffset after disabled mouse events = %d, want 0", got)
	}
	if got, want := model.focus, focusChat; got != want {
		t.Fatalf("focus after disabled mouse events = %v, want %v", got, want)
	}
	if model.activeChannelState().replyTo != nil {
		t.Fatalf("replyTo after disabled mouse events = %#v, want nil", model.activeChannelState().replyTo)
	}
}
