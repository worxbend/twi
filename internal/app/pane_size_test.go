package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
)

func paneTestModel(t *testing.T, width, height int) shellModel {
	t.Helper()
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockModelWithClock("alpha", cfg, &appFakeClock{now: time.Now()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(shellModel)
}

func paneKey(model shellModel, r rune) shellModel {
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(shellModel)
}

func leaderPress(model shellModel, r rune) shellModel {
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(shellModel)
	return paneKey(model, r)
}

// TestResizeMovesWidthBetweenChatAndActivity is the core of the feature: the
// two panes share a fixed budget, so every cell one gains the other loses.
func TestResizeMovesWidthBetweenChatAndActivity(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	before := model.layout()
	if before.activityWidth == 0 {
		t.Fatal("test setup: activity column is not visible at 150 columns")
	}

	widened := paneKey(model, '>')
	got := widened.layout()
	if got.activityWidth != before.activityWidth+paneResizeStep {
		t.Fatalf("activity width = %d, want %d", got.activityWidth, before.activityWidth+paneResizeStep)
	}
	if got.chatWidth != before.chatWidth-paneResizeStep {
		t.Fatalf("chat width = %d, want %d; chat must absorb the difference", got.chatWidth, before.chatWidth-paneResizeStep)
	}

	narrowed := paneKey(widened, '<')
	if got := narrowed.layout(); got.activityWidth != before.activityWidth || got.chatWidth != before.chatWidth {
		t.Fatalf("after > then <, layout = (chat %d, activity %d), want the original (chat %d, activity %d)",
			got.chatWidth, got.activityWidth, before.chatWidth, before.activityWidth)
	}
}

// TestResizeTotalWidthIsConserved guards the invariant that matters most: no
// sequence of resizes may lose or invent terminal columns.
func TestResizeTotalWidthIsConserved(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	for _, key := range []rune{'>', '>', '>', '<', '>', '<', '<', '<', '<', '>', '='} {
		model = paneKey(model, key)
		layout := model.layout()
		total := layout.sidebarWidth + layout.chatWidth + layout.activityWidth
		if total != layout.width {
			t.Fatalf("after %q: sidebar %d + chat %d + activity %d = %d, want %d",
				key, layout.sidebarWidth, layout.chatWidth, layout.activityWidth, total, layout.width)
		}
	}
}

// TestResetRestoresAutomaticWidths keeps "=" meaningful: it must return the
// panes to sizing themselves, not to whatever they happened to be.
func TestResetRestoresAutomaticWidths(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	original := model.layout()

	for range 3 {
		model = paneKey(model, '>')
	}
	if model.layout().activityWidth == original.activityWidth {
		t.Fatal("test setup: resizing did not change the activity width")
	}

	model = paneKey(model, '=')
	if got := model.layout(); got.activityWidth != original.activityWidth || got.chatWidth != original.chatWidth {
		t.Fatalf("after =, layout = (chat %d, activity %d), want the automatic (chat %d, activity %d)",
			got.chatWidth, got.activityWidth, original.chatWidth, original.activityWidth)
	}
	if model.activityWidthOverride != 0 || model.sidebarWidthOverride != 0 {
		t.Fatal("= left an override in place, so the panes would not resize with the terminal")
	}
}

// TestResizeStopsBeforeStarvingChat is the guard that keeps the feature from
// producing an unusable layout: chat is the pane the app exists to show.
func TestResizeStopsBeforeStarvingChat(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	for range 60 {
		model = paneKey(model, '>')
	}
	layout := model.layout()
	if layout.chatWidth < minChatWidthAfterPanes {
		t.Fatalf("chat width = %d after repeated widening of activity, want >= %d",
			layout.chatWidth, minChatWidthAfterPanes)
	}
	if layout.activityWidth > activityMaxSize {
		t.Fatalf("activity width = %d, want <= %d", layout.activityWidth, activityMaxSize)
	}
}

func TestResizeStopsAtTheActivityMinimum(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	for range 60 {
		model = paneKey(model, '<')
	}
	if got := model.layout().activityWidth; got != activityMinSize {
		t.Fatalf("activity width = %d after repeated narrowing, want the floor %d", got, activityMinSize)
	}
}

// TestSidebarResizesOnlyWhenFocused pins the routing rule: the same two keys
// act on whichever side pane the user is looking at.
func TestSidebarResizesOnlyWhenFocused(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	before := model.layout()

	chatFocused := paneKey(model, '>')
	if got := chatFocused.layout(); got.sidebarWidth != before.sidebarWidth {
		t.Fatalf("sidebar width changed to %d from chat focus, want %d untouched", got.sidebarWidth, before.sidebarWidth)
	}

	model.focus = focusSidebar
	sidebarFocused := paneKey(model, '>')
	got := sidebarFocused.layout()
	if got.sidebarWidth != before.sidebarWidth+paneResizeStep {
		t.Fatalf("sidebar width = %d with the sidebar focused, want %d", got.sidebarWidth, before.sidebarWidth+paneResizeStep)
	}
	if got.activityWidth != before.activityWidth {
		t.Fatalf("activity width changed to %d while resizing the sidebar, want %d", got.activityWidth, before.activityWidth)
	}
}

func TestToggleActivityHidesAndRestoresTheColumn(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	original := model.layout()
	if original.activityWidth == 0 {
		t.Fatal("test setup: activity column is not visible")
	}

	hidden := leaderPress(model, leaderActivityRune)
	got := hidden.layout()
	if got.activityWidth != 0 {
		t.Fatalf("activity width = %d after space+a, want 0", got.activityWidth)
	}
	if got.chatWidth != original.chatWidth+original.activityWidth {
		t.Fatalf("chat width = %d, want %d; chat should reclaim the whole column",
			got.chatWidth, original.chatWidth+original.activityWidth)
	}

	shown := leaderPress(hidden, leaderActivityRune)
	if got := shown.layout(); got.activityWidth != original.activityWidth {
		t.Fatalf("activity width = %d after toggling back, want %d", got.activityWidth, original.activityWidth)
	}
}

// TestToggleActivityShowsItBelowTheAutoThreshold covers the point of an
// explicit toggle: asking for the column on a terminal that would not have
// offered it should work.
func TestToggleActivityShowsItBelowTheAutoThreshold(t *testing.T) {
	model := paneTestModel(t, 90, 40)
	if got := model.layout().activityWidth; got != 0 {
		t.Fatalf("test setup: activity width = %d at 90 columns, want it hidden by default", got)
	}

	shown := leaderPress(model, leaderActivityRune)
	if got := shown.layout().activityWidth; got == 0 {
		t.Fatal("space+a did not show the activity column on a terminal wide enough to afford it")
	}
	if got := shown.layout().chatWidth; got < minChatWidthAfterPanes {
		t.Fatalf("chat width = %d after showing activity, want >= %d", got, minChatWidthAfterPanes)
	}
}

// TestToggleActivityCannotShowItOnANarrowTerminal keeps an explicit request
// from producing a layout with no room for chat.
func TestToggleActivityCannotShowItOnANarrowTerminal(t *testing.T) {
	model := paneTestModel(t, 44, 20)
	shown := leaderPress(model, leaderActivityRune)
	if got := shown.layout().activityWidth; got != 0 {
		t.Fatalf("activity width = %d at 44 columns, want 0; there is no room for it", got)
	}
}

// TestHiddenActivityStaysHiddenAcrossResize pins the toggle as a standing
// choice rather than a one-off, matching how the sidebar behaves.
func TestHiddenActivityStaysHiddenAcrossResize(t *testing.T) {
	model := leaderPress(paneTestModel(t, 150, 40), leaderActivityRune)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	model = updated.(shellModel)
	if got := model.layout().activityWidth; got != 0 {
		t.Fatalf("activity width = %d after widening the terminal, want it to stay hidden", got)
	}
}

func TestConfiguredPaneWidthsApply(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	cfg.Features.SidebarWidth = 30
	cfg.Features.ActivityWidth = 40
	model := newMockModelWithClock("alpha", cfg, &appFakeClock{now: time.Now()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	model = updated.(shellModel)

	layout := model.layout()
	if layout.sidebarWidth != 30 {
		t.Errorf("sidebar width = %d, want the configured 30", layout.sidebarWidth)
	}
	if layout.activityWidth != 40 {
		t.Errorf("activity width = %d, want the configured 40", layout.activityWidth)
	}
	if total := layout.sidebarWidth + layout.chatWidth + layout.activityWidth; total != layout.width {
		t.Errorf("widths sum to %d, want %d", total, layout.width)
	}
}

// TestConfiguredPaneWidthsAreClampedNotHonoredBlindly covers a config asking
// for more than the terminal can give: it must degrade, not produce a layout
// with no chat in it.
func TestConfiguredPaneWidthsAreClampedNotHonoredBlindly(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.ActivityWidth = 500
	model := newMockModelWithClock("alpha", cfg, &appFakeClock{now: time.Now()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(shellModel)

	layout := model.layout()
	if layout.chatWidth < minChatWidthAfterPanes {
		t.Fatalf("chat width = %d with an oversized configured activity column, want >= %d",
			layout.chatWidth, minChatWidthAfterPanes)
	}
	if layout.activityWidth > activityMaxSize {
		t.Fatalf("activity width = %d, want <= %d", layout.activityWidth, activityMaxSize)
	}
}

// TestResizedPaneRendersAtItsWidth closes the loop from layout to output.
func TestResizedPaneRendersAtItsWidth(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	for range 3 {
		model = paneKey(model, '>')
	}
	layout := model.layout()
	rendered := model.activityLogView(layout)
	if rendered == "" {
		t.Fatal("activity pane rendered empty after a resize")
	}
	// Measured in display cells, not runes: the pane title carries a wide
	// emoji that occupies two columns.
	for i, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got != layout.activityWidth {
			t.Fatalf("activity row %d is %d cells wide, want %d: %q",
				i, got, layout.activityWidth, ansi.Strip(line))
		}
	}
}

// TestHiddenActivityRendersNothing keeps the hidden pane from leaving a gap.
func TestHiddenActivityRendersNothing(t *testing.T) {
	model := leaderPress(paneTestModel(t, 150, 40), leaderActivityRune)
	layout := model.layout()
	if got := model.activityLogView(layout); got != "" {
		t.Fatalf("hidden activity pane rendered %q, want empty", got)
	}
	view := ansi.Strip(model.View())
	if strings.Contains(view, "Activity ·") {
		t.Fatal("hidden activity pane still shows its title in the full view")
	}
}

// TestResizeKeysDoNothingWithoutAPane keeps the keys inert rather than
// stashing an override that would surprise the user when the pane appears.
func TestResizeKeysDoNothingWithoutAPane(t *testing.T) {
	model := paneTestModel(t, 60, 20)
	if model.layout().activityWidth != 0 {
		t.Fatal("test setup: expected no activity column at 60 columns")
	}
	resized := paneKey(model, '>')
	if resized.activityWidthOverride != 0 {
		t.Fatalf("activityWidthOverride = %d with no visible pane, want 0", resized.activityWidthOverride)
	}
}

// TestResizeKeysReachTheComposerAsText keeps the new bindings from stealing
// characters people actually type.
func TestResizeKeysReachTheComposerAsText(t *testing.T) {
	model := paneTestModel(t, 150, 40)
	model.focus = focusComposer
	for _, r := range "a < b > c = d" {
		model = paneKey(model, r)
	}
	if got, want := model.activeChannelState().composerText, "a < b > c = d"; got != want {
		t.Fatalf("composerText = %q, want %q; resize keys must not be swallowed while composing", got, want)
	}
}
