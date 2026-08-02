package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

// composerRow returns a Y coordinate inside the composer for the current
// layout, so the test follows the real geometry instead of a hard-coded row
// that silently drifts when the layout changes.
func composerRow(model mockShellModel) int {
	layout := model.layout()
	return layout.tabBarHeight + layout.statusHeight + layout.chatHeight
}

func TestClickFocusesComposerAndChat(t *testing.T) {
	model := mouseFocusModel(t)
	layout := model.layout()

	model.focus = mockFocusChat
	model = leftClick(model, 4, composerRow(model))
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

	model = leftClick(model, 4, composerRow(model))
	if model.focus != mockFocusChat {
		t.Fatalf("focus changed to %v with mouse support disabled", model.focus)
	}
}
