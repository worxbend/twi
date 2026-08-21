package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
)

func TestActivityLogColumnAppearsAboveMinWidthAndRenders(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockModel("example", cfg)
	model.width, model.height = 140, 20
	model.appendActivity(activityEntry{Kind: activityFollow, Text: "NewViewer followed"})

	layout := model.layout()
	if layout.activityWidth != activityLogWideSize {
		t.Fatalf("activityWidth = %d, want %d at width 140", layout.activityWidth, activityLogWideSize)
	}
	view := model.View()
	if !strings.Contains(view, "Activity") || !strings.Contains(view, "NewViewer followed") {
		t.Fatalf("view missing activity log content:\n%s", view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipglossWidth(line); got > model.width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, model.width, view)
		}
	}
}

func TestActivityLogColumnHiddenBelowMinWidth(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20

	layout := model.layout()
	if layout.activityWidth != 0 {
		t.Fatalf("activityWidth = %d at width 88, want 0 (below activityLogMinWidth)", layout.activityWidth)
	}
	if strings.Contains(model.View(), " Activity") {
		t.Fatalf("narrow view unexpectedly shows the activity log column:\n%s", model.View())
	}
}

func TestActivityLogColumnHiddenOnStreamInfoAndMiscTabs(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 140, 20

	model.activeTab = tabStreamInfo
	if layout := model.layout(); layout.activityWidth != 0 {
		t.Fatalf("activityWidth on Stream Info tab = %d, want 0", layout.activityWidth)
	}

	model.activeTab = tabMisc
	if layout := model.layout(); layout.activityWidth != 0 {
		t.Fatalf("activityWidth on Misc tab = %d, want 0", layout.activityWidth)
	}
}

func TestMouseInChatRegionExcludesActivityLogColumn(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockModel("example", cfg)
	model.width, model.height = 140, 20
	layout := model.layout()
	if layout.activityWidth <= 0 {
		t.Fatal("test setup: expected a visible activity log column")
	}

	insideActivity := tea.MouseEvent{
		X:      layout.sidebarWidth + layout.chatWidth + 1,
		Y:      layout.tabBarHeight + layout.statusHeight + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}
	if model.mouseInChatRegion(insideActivity, layout) {
		t.Fatal("mouseInChatRegion = true for a point inside the activity log column, want false")
	}

	insideChat := tea.MouseEvent{
		X:      layout.sidebarWidth + 1,
		Y:      layout.tabBarHeight + layout.statusHeight + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}
	if !model.mouseInChatRegion(insideChat, layout) {
		t.Fatal("mouseInChatRegion = false for a point inside chat, want true")
	}
}

// TestSidePanesNeverStarveChatBelowFloor sweeps every terminal width at which
// a side pane can be drawn and checks the chat column keeps
// minChatWidthAfterPanes. Turning the activity column on explicitly used to
// draw it at its responsive default (28) on terminals as narrow as 48, where
// the visibility guard had only reserved room for activityMinSize (16),
// squeezing chat to 20 cells.
func TestSidePanesNeverStarveChatBelowFloor(t *testing.T) {
	for width := minChatWidthAfterPanes + activityMinSize; width <= 200; width++ {
		for _, visibility := range []activityVisibility{activityAuto, activityShown} {
			for _, sidebar := range []sidebarVisibility{sidebarAuto, sidebarShown} {
				cfg := config.Default()
				cfg.Features.AnimationMode = "off"
				model := newMockModel("example", cfg)
				model.width, model.height = width, 30
				model.panes.activityVisibility = visibility
				model.panes.sidebarVisibility = sidebar

				layout := model.layout()
				if layout.activityWidth == 0 && layout.sidebarWidth == 0 {
					continue
				}
				if layout.chatWidth < minChatWidthAfterPanes {
					t.Fatalf("width=%d activityVisibility=%d sidebarVisibility=%d: sidebar=%d activity=%d leaves chat=%d, want >= %d",
						width, visibility, sidebar,
						layout.sidebarWidth, layout.activityWidth, layout.chatWidth, minChatWidthAfterPanes)
				}
			}
		}
	}
}
