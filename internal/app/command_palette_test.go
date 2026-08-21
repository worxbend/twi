package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
)

// openPaletteModel builds a shell with the command palette open and the
// typewriter reveal switched off, so these tests see the palette's own state
// rather than an animation frame.
func openPaletteModel(t *testing.T) shellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockModel("alpha", cfg)
	model.width, model.height = 96, 24
	model.palette = commandPaletteState{open: true}
	return model
}

func TestCommandPaletteKeyWrapsSelectionAroundTheResultList(t *testing.T) {
	model := openPaletteModel(t)
	last := len(model.visibleCommandPaletteCommands()) - 1
	if last < 1 {
		t.Fatalf("palette shows %d commands, want at least 2 for a wrap test", last+1)
	}

	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyUp})
	if model.palette.selected != last {
		t.Fatalf("up from the first row = %d, want %d (the last row)", model.palette.selected, last)
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyDown})
	if model.palette.selected != 0 {
		t.Fatalf("down from the last row = %d, want 0", model.palette.selected)
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyTab})
	if model.palette.selected != 1 {
		t.Fatalf("tab = %d, want 1 (tab moves like down)", model.palette.selected)
	}
}

func TestCommandPaletteTypingFiltersAndReturnsToTheTopRow(t *testing.T) {
	model := openPaletteModel(t)
	model.palette.selected = 3

	for _, r := range "quit" {
		model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if model.palette.query != "quit" {
		t.Fatalf("query = %q, want %q", model.palette.query, "quit")
	}
	if model.palette.selected != 0 {
		t.Fatalf("selected after typing = %d, want 0", model.palette.selected)
	}
	commands := model.visibleCommandPaletteCommands()
	if len(commands) == 0 || commands[0].action != commandPaletteQuit {
		t.Fatalf("filtered commands = %#v, want the quit command first", commands)
	}
}

func TestCommandPaletteSpaceTypesIntoTheQuery(t *testing.T) {
	model := openPaletteModel(t)
	for _, r := range "open" {
		model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeySpace})
	if model.palette.query != "open " {
		t.Fatalf("query after space = %q, want %q", model.palette.query, "open ")
	}
}

func TestCommandPaletteBackspaceRemovesAWholeRune(t *testing.T) {
	model := openPaletteModel(t)
	model.palette.query = "pog🎉"

	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if model.palette.query != "pog" {
		t.Fatalf("query after backspace = %q, want %q (the whole emoji removed)", model.palette.query, "pog")
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyCtrlH})
	if model.palette.query != "po" {
		t.Fatalf("query after ctrl+h = %q, want %q", model.palette.query, "po")
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyCtrlU})
	if model.palette.query != "" {
		t.Fatalf("query after ctrl+u = %q, want empty", model.palette.query)
	}
}

// TestCommandPaletteClampsAnOutOfRangeSelection covers the palette's extra
// step over the other pickers: whatever key arrives, the highlight is pulled
// back inside the result list afterwards. A click in the palette pane can
// leave the highlight past the end of a list that has since been filtered
// down, and this is what rescues it.
func TestCommandPaletteClampsAnOutOfRangeSelection(t *testing.T) {
	model := openPaletteModel(t)
	model.palette.query = "quit"
	matches := len(model.visibleCommandPaletteCommands())
	if matches == 0 {
		t.Fatal("no commands match \"quit\", want at least one")
	}
	model.palette.selected = matches + 50

	// Left is not a palette key, so nothing but the clamp can move the
	// highlight here.
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyLeft})
	if model.palette.selected != matches-1 {
		t.Fatalf("selected after clamp = %d, want %d (the last matching row)", model.palette.selected, matches-1)
	}
}

func TestCommandPaletteEscAndEnterStillEndTheOverlay(t *testing.T) {
	model := openPaletteModel(t)
	model.palette.query = "not a command that exists"
	updated, _ := model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.palette.open || updated.palette.query != "" {
		t.Fatalf("palette after esc = %#v, want closed and empty", updated.palette)
	}

	model = openPaletteModel(t)
	model.focus = focusChat
	for _, r := range "focus composer" {
		if r == ' ' {
			model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model, _ = model.handleCommandPaletteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if model.palette.open {
		t.Fatal("palette.open after enter = true, want closed")
	}
	if model.focus != focusComposer {
		t.Fatalf("focus after running \"focus composer\" = %v, want the composer", model.focus)
	}
}
