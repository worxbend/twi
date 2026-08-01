package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/config"
)

func mouseFocusModel(t *testing.T) mockShellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.EnableMouse = true
	model := newMockShellModel("alpha", cfg)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	return updated.(mockShellModel)
}

func leftClick(model mockShellModel, x, y int) mockShellModel {
	updated, _ := model.Update(tea.MouseMsg(tea.MouseEvent{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}))
	return updated.(mockShellModel)
}

// emotesStripRow returns a Y coordinate inside the emotes strip for the
// current layout, so the test follows the real geometry instead of a
// hard-coded row that silently drifts when the layout changes.
func emotesStripRow(model mockShellModel) int {
	layout := model.layout()
	top := layout.tabBarHeight + layout.statusHeight + layout.chatHeight + layout.composerHeight
	if layout.emotesFramed {
		return top + 1
	}
	return top
}

func TestClickFocusesEmotesStrip(t *testing.T) {
	model := mouseFocusModel(t)
	if model.layout().emotesHeight <= 0 {
		t.Skip("emotes strip not visible at this size")
	}
	model.focus = mockFocusChat

	model = leftClick(model, 2, emotesStripRow(model))
	if model.focus != mockFocusEmotes {
		t.Fatalf("focus after clicking the emotes strip = %v, want emotes", model.focus)
	}
}

func TestClickSelectsEmoteUnderCursor(t *testing.T) {
	model := mouseFocusModel(t)
	entries := model.activeEmoteEntries()
	if model.layout().emotesHeight <= 0 || len(entries) < 3 {
		t.Skip("emotes strip not visible or too short at this size")
	}
	model.focus = mockFocusChat

	// Walk the rendered run to find where the third entry starts. The strip
	// renders unbracketed names while unfocused, which is the state here.
	// Offsets are in display cells, not runes - the emote list contains
	// double-width emoji.
	x := 2
	for _, entry := range entries[:2] {
		x += uniseg.StringWidth(entry.Name) + 1
	}

	model = leftClick(model, x, emotesStripRow(model))
	if model.focus != mockFocusEmotes {
		t.Fatalf("focus = %v after clicking an emote, want emotes", model.focus)
	}
	if got := model.clampedEmoteSelected(entries); got != 2 {
		t.Fatalf("selected emote = %d (%q), want index 2 (%q)", got, entries[got].Name, entries[2].Name)
	}
}

func TestClickFocusesComposerAndChat(t *testing.T) {
	model := mouseFocusModel(t)
	layout := model.layout()

	composerRow := layout.tabBarHeight + layout.statusHeight + layout.chatHeight
	model.focus = mockFocusChat
	model = leftClick(model, 4, composerRow)
	if model.focus != mockFocusComposer {
		t.Fatalf("focus after clicking the composer = %v, want composer", model.focus)
	}

	chatRow := layout.tabBarHeight + layout.statusHeight + 1
	model = leftClick(model, layout.sidebarWidth+2, chatRow)
	if model.focus != mockFocusChat {
		t.Fatalf("focus after clicking chat = %v, want chat", model.focus)
	}
}

func TestMouseFocusIgnoredWhenMouseDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.EnableMouse = false
	model := newMockShellModel("alpha", cfg)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	model = updated.(mockShellModel)
	model.focus = mockFocusChat

	model = leftClick(model, 2, emotesStripRow(model))
	if model.focus != mockFocusChat {
		t.Fatalf("focus changed to %v with mouse support disabled", model.focus)
	}
}
