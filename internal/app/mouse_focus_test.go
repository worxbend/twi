package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
)

func mouseFocusModel(t *testing.T) shellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.EnableMouse = true
	model := newMockModel("alpha", cfg)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	return updated.(shellModel)
}

func leftClick(model shellModel, x, y int) shellModel {
	updated, _ := model.Update(tea.MouseMsg(tea.MouseEvent{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}))
	return updated.(shellModel)
}

// composerRow returns a Y coordinate inside the composer for the current
// layout, so the test follows the real geometry instead of a hard-coded row
// that silently drifts when the layout changes.
func composerRow(model shellModel) int {
	layout := model.layout()
	return layout.tabBarHeight + layout.statusHeight + layout.chatHeight
}

func TestClickFocusesComposerAndChat(t *testing.T) {
	model := mouseFocusModel(t)
	layout := model.layout()

	model.focus = focusChat
	model = leftClick(model, 4, composerRow(model))
	if model.focus != focusComposer {
		t.Fatalf("focus after clicking the composer = %v, want composer", model.focus)
	}

	chatRow := layout.tabBarHeight + layout.statusHeight + 1
	model = leftClick(model, layout.sidebarWidth+2, chatRow)
	if model.focus != focusChat {
		t.Fatalf("focus after clicking chat = %v, want chat", model.focus)
	}
}

func TestMouseFocusIgnoredWhenMouseDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.EnableMouse = false
	model := newMockModel("alpha", cfg)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	model = updated.(shellModel)
	model.focus = focusChat

	model = leftClick(model, 4, composerRow(model))
	if model.focus != focusChat {
		t.Fatalf("focus changed to %v with mouse support disabled", model.focus)
	}
}
