package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// TestCategoryPickerReopenInvalidatesInFlightSearch guards the generation
// bump on open/close: both used to reset the generation to zero, so a search
// started by a previous open (still generation 0, no typing involved) matched
// the reopened picker's generation and overwrote its state.
func TestCategoryPickerReopenInvalidatesInFlightSearch(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.services.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"Old Game": {ID: "1", Name: "Old Game"},
		"Fortnite": {ID: "33214", Name: "Fortnite"},
	}}
	model.streamInfo.category = "Old Game"

	stale := model.openCategoryPicker()().(categoryPickerResultsMsg)

	// Close, change the category, reopen: the first open's search is still in
	// flight and must not overwrite the new session's results.
	updated, _ := model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated
	model.streamInfo.category = "Fortnite"
	fresh := model.openCategoryPicker()().(categoryPickerResultsMsg)

	model = model.applyCategoryPickerResults(stale)
	if len(model.categoryPicker.results) != 0 {
		t.Fatalf("results = %#v after stale response from a previous open, want empty", model.categoryPicker.results)
	}
	model = model.applyCategoryPickerResults(fresh)
	if len(model.categoryPicker.results) != 1 || model.categoryPicker.results[0].Name != "Fortnite" {
		t.Fatalf("results = %#v, want [Fortnite] from the current open", model.categoryPicker.results)
	}
}

// TestCategoryPickerEscInvalidatesInFlightWork covers the same bump on the
// close path alone: a debounce tick and a search response scheduled while the
// picker was open must both be discarded once esc closes it.
func TestCategoryPickerEscInvalidatesInFlightWork(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.services.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"Fortnite": {ID: "33214", Name: "Fortnite"},
	}}
	model.streamInfo.category = "Fortnite"

	inFlight := model.openCategoryPicker()().(categoryPickerResultsMsg)

	// Type to schedule a debounce, then close before either lands.
	updated, debounceCmd := model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated
	pendingDebounce := debounceCmd().(categoryPickerDebounceMsg)
	updated, _ = model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated

	updated, cmd := model.applyCategoryPickerDebounce(pendingDebounce)
	model = updated
	if cmd != nil {
		t.Fatal("debounce from a closed picker triggered a search, want nil")
	}
	model = model.applyCategoryPickerResults(inFlight)
	if len(model.categoryPicker.results) != 0 {
		t.Fatalf("results = %#v after response from a closed picker, want empty", model.categoryPicker.results)
	}
}
