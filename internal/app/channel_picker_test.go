package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// openChannelPickerModel builds a shell with the channel picker open over a
// known row list: one open channel plus two follows.
func openChannelPickerModel(t *testing.T) shellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha"}
	model := newMockModel("alpha", cfg)
	model.width, model.height = 96, 24
	model.followedChannelList = []twitch.FollowedChannel{
		{BroadcasterLogin: "gamma", BroadcasterName: "GammaTV"},
		{BroadcasterLogin: "delta", BroadcasterName: "Delta Streams"},
	}
	model.channelPicker = channelPickerState{open: true}
	return model
}

func TestChannelPickerKeyWrapsSelectionAroundTheEntryList(t *testing.T) {
	model := openChannelPickerModel(t)
	last := len(model.channelPickerEntries()) - 1
	if last < 1 {
		t.Fatalf("picker shows %d entries, want at least 2 for a wrap test", last+1)
	}

	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyUp})
	if model.channelPicker.selected != last {
		t.Fatalf("up from the first row = %d, want %d (the last row)", model.channelPicker.selected, last)
	}
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyDown})
	if model.channelPicker.selected != 0 {
		t.Fatalf("down from the last row = %d, want 0", model.channelPicker.selected)
	}
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyTab})
	if model.channelPicker.selected != 1 {
		t.Fatalf("tab = %d, want 1 (tab moves like down)", model.channelPicker.selected)
	}
}

func TestChannelPickerTypingFiltersAndReturnsToTheTopRow(t *testing.T) {
	model := openChannelPickerModel(t)
	model.channelPicker.selected = 2

	for _, r := range "gam" {
		model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if model.channelPicker.query != "gam" {
		t.Fatalf("query = %q, want %q", model.channelPicker.query, "gam")
	}
	if model.channelPicker.selected != 0 {
		t.Fatalf("selected after typing = %d, want 0", model.channelPicker.selected)
	}
	entries := model.channelPickerEntries()
	if len(entries) == 0 || entries[0].login != "gamma" {
		t.Fatalf("filtered entries = %#v, want gamma first", entries)
	}
}

// TestChannelPickerBackspaceRemovesAWholeRune guards the switch this picker
// made from trimming the query by bytes (utf8.DecodeLastRuneInString) to
// trimming it by runes: for any text a query can hold, the two agree.
func TestChannelPickerBackspaceRemovesAWholeRune(t *testing.T) {
	model := openChannelPickerModel(t)
	model.channelPicker.query = "日本語"

	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if model.channelPicker.query != "日本" {
		t.Fatalf("query after backspace = %q, want %q", model.channelPicker.query, "日本")
	}
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyCtrlH})
	if model.channelPicker.query != "日" {
		t.Fatalf("query after ctrl+h = %q, want %q", model.channelPicker.query, "日")
	}
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyCtrlU})
	if model.channelPicker.query != "" {
		t.Fatalf("query after ctrl+u = %q, want empty", model.channelPicker.query)
	}
}

// TestChannelPickerSpaceTypesIntoTheQuery matters because space is the shell's
// leader key everywhere else: inside this picker it has to reach the query,
// since Twitch display names may contain spaces.
func TestChannelPickerSpaceTypesIntoTheQuery(t *testing.T) {
	model := openChannelPickerModel(t)
	for _, r := range "delta" {
		model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeySpace})
	if model.channelPicker.query != "delta " {
		t.Fatalf("query after space = %q, want %q", model.channelPicker.query, "delta ")
	}
	entries := model.channelPickerEntries()
	if len(entries) == 0 || entries[0].display != "Delta Streams" {
		t.Fatalf("entries for %q = %#v, want the display name match first", model.channelPicker.query, entries)
	}
}

// TestChannelPickerLeavesAnOutOfRangeSelectionAlone records a difference from
// the command palette rather than endorsing it: the palette re-clamps the
// highlight on every key, this picker never does, so a highlight left past the
// end of the list (a click in the pane, then a shorter list) stays there and
// enter cancels instead of opening a channel. Changing that is a behaviour
// change and belongs in its own commit.
func TestChannelPickerLeavesAnOutOfRangeSelectionAlone(t *testing.T) {
	model := openChannelPickerModel(t)
	beyond := len(model.channelPickerEntries()) + 50
	model.channelPicker.selected = beyond

	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyLeft})
	if model.channelPicker.selected != beyond {
		t.Fatalf("selected after an ignored key = %d, want %d (unchanged)", model.channelPicker.selected, beyond)
	}

	before := model.activeChannelName()
	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if model.channelPicker.open {
		t.Fatal("channelPicker.open after enter = true, want closed")
	}
	if got := model.activeChannelName(); got != before {
		t.Fatalf("active channel after enter on an out-of-range row = %q, want %q", got, before)
	}
}

func TestChannelPickerEscClosesAndClearsTheQuery(t *testing.T) {
	model := openChannelPickerModel(t)
	model.channelPicker.query = "gam"

	model, _ = model.handleChannelPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if model.channelPicker.open || model.channelPicker.query != "" {
		t.Fatalf("picker after esc = %#v, want closed and empty", model.channelPicker)
	}
}

// TestClampChannelPickerSelectionPullsTheHighlightBackIn covers the one place
// this picker does clamp: when the follow list arrives and changes the rows
// underneath a highlight that was set before it.
func TestClampChannelPickerSelectionPullsTheHighlightBackIn(t *testing.T) {
	model := openChannelPickerModel(t)
	model.channelPicker.selected = 99
	model.clampChannelPickerSelection()
	if want := len(model.channelPickerEntries()) - 1; model.channelPicker.selected != want {
		t.Fatalf("selected after clamp = %d, want %d", model.channelPicker.selected, want)
	}

	model.channelPicker.selected = -3
	model.clampChannelPickerSelection()
	if model.channelPicker.selected != 0 {
		t.Fatalf("selected after clamping a negative index = %d, want 0", model.channelPicker.selected)
	}
}
