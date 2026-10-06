package app

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	categoryPickerDebounce       = 250 * time.Millisecond
	categoryPickerRequestTimeout = 5 * time.Second
	categoryPickerResultLimit    = 20
)

// categoryPickerState drives the Stream Info tab's category search overlay:
// the user types a query, results come from Twitch Helix Search Categories
// (debounced so fast typing doesn't fire one request per keystroke), and
// selecting an entry commits both its display name and its Twitch game ID -
// there is no free-text category value, only a real Twitch category.
type categoryPickerState struct {
	open bool
	// filterList holds the typed query and the highlighted row, shared with
	// the other searchable overlays so they cannot drift apart.
	filterList
	results    []twitch.Game
	loading    bool
	err        string
	generation int
}

type categoryPickerDebounceMsg struct{ generation int }

type categoryPickerResultsMsg struct {
	generation int
	results    []twitch.Game
	err        error
}

// openCategoryPicker opens the overlay seeded with the currently selected
// category (if any) so the first results are immediately relevant, and
// kicks off that initial search without debouncing (unlike per-keystroke
// typing, opening the picker is one deliberate action).
func (m *shellModel) openCategoryPicker() tea.Cmd {
	m.closeOtherOverlays(overlayCategory)
	query := strings.TrimSpace(m.streamInfo.category)
	// The generation carries over (bumped) rather than resetting to zero: a
	// search still in flight from a previous open must stay stale.
	m.categoryPicker = categoryPickerState{
		open:       true,
		filterList: filterList{query: query},
		generation: m.categoryPicker.generation + 1,
	}
	return m.scheduleCategorySearch()
}

// closeCategoryPicker dismisses the overlay and bumps the generation, so a
// debounce tick or search response from the closing session is discarded
// when it lands instead of mutating a reopened picker.
func (m *shellModel) closeCategoryPicker() {
	m.categoryPicker = categoryPickerState{generation: m.categoryPicker.generation + 1}
}

func (m shellModel) handleCategoryPickerKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.closeCategoryPicker()
		return m, nil
	case tea.KeyEnter:
		return m.commitCategoryPickerSelection()
	case tea.KeyUp:
		m.categoryPicker.move(-1, len(m.categoryPickerEntries()))
		return m, nil
	case tea.KeyDown, tea.KeyTab:
		m.categoryPicker.move(1, len(m.categoryPickerEntries()))
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.categoryPicker.deleteRune()
		return m, m.debounceCategorySearch()
	case tea.KeyCtrlU:
		m.categoryPicker.clearQuery()
		return m, m.debounceCategorySearch()
	case tea.KeySpace:
		m.categoryPicker.insert([]rune{' '})
		return m, m.debounceCategorySearch()
	case tea.KeyRunes:
		m.categoryPicker.insert(msg.Runes)
		return m, m.debounceCategorySearch()
	}
	return m, nil
}

// commitCategoryPickerSelection applies the highlighted entry to the Stream
// Info Category field (both display name and Twitch game ID, so saving never
// needs a separate name->ID resolution step) and closes the picker. The
// pinned first entry clears the category entirely.
func (m shellModel) commitCategoryPickerSelection() (shellModel, tea.Cmd) {
	entries := m.categoryPickerEntries()
	index := m.categoryPicker.selected
	if index < 0 || index >= len(entries) {
		index = 0
	}
	selected := entries[index]
	m.streamInfo.categoryGameID = selected.ID
	if selected.ID == "" {
		m.streamInfo.category = ""
	} else {
		m.streamInfo.category = selected.Name
	}
	m.closeCategoryPicker()
	return m, nil
}

// categoryPickerEntries pins a synthetic "no category" entry first (Twitch
// has no search result for "clear the category", so the picker offers it
// directly) followed by the current search results.
func (m shellModel) categoryPickerEntries() []twitch.Game {
	entries := make([]twitch.Game, 0, len(m.categoryPicker.results)+1)
	entries = append(entries, twitch.Game{Name: "(no category)"})
	entries = append(entries, m.categoryPicker.results...)
	return entries
}

// debounceCategorySearch bumps the request generation and schedules a
// delayed tick carrying it; handleCategoryPickerKey calls this on every
// query-changing key so a burst of keystrokes collapses into one search
// instead of one per keystroke.
func (m *shellModel) debounceCategorySearch() tea.Cmd {
	m.categoryPicker.generation++
	generation := m.categoryPicker.generation
	return tea.Tick(categoryPickerDebounce, func(time.Time) tea.Msg {
		return categoryPickerDebounceMsg{generation: generation}
	})
}

// scheduleCategorySearch issues the actual Helix Search Categories request
// for the current query. Both the debounce tick and the picker's initial
// open call this; the generation captured here is compared against the
// model's current generation when the response (or a superseding debounce
// tick) arrives, so stale results from an old query are discarded.
func (m *shellModel) scheduleCategorySearch() tea.Cmd {
	query := strings.TrimSpace(m.categoryPicker.query)
	generation := m.categoryPicker.generation
	if m.services.gameLookup == nil || query == "" {
		m.categoryPicker.loading = false
		m.categoryPicker.err = ""
		m.categoryPicker.results = nil
		return nil
	}
	m.categoryPicker.loading = true
	m.categoryPicker.err = ""
	lookup := m.services.gameLookup
	lifetime := m.lifetimeContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(lifetime, categoryPickerRequestTimeout)
		defer cancel()
		results, err := lookup.SearchCategories(ctx, query, categoryPickerResultLimit)
		return categoryPickerResultsMsg{generation: generation, results: results, err: err}
	}
}

func (m shellModel) applyCategoryPickerDebounce(msg categoryPickerDebounceMsg) (shellModel, tea.Cmd) {
	if msg.generation != m.categoryPicker.generation {
		return m, nil
	}
	return m, m.scheduleCategorySearch()
}

func (m shellModel) applyCategoryPickerResults(msg categoryPickerResultsMsg) shellModel {
	if msg.generation != m.categoryPicker.generation {
		return m
	}
	m.categoryPicker.loading = false
	if msg.err != nil {
		m.categoryPicker.err = msg.err.Error()
		m.categoryPicker.results = nil
		return m
	}
	m.categoryPicker.err = ""
	m.categoryPicker.results = msg.results
	m.categoryPicker.selected = 0
	return m
}

func (m shellModel) categoryPickerView(layout shellLayout) string {
	return m.renderOverlayPane(overlayPaneSpec{
		icon:          "🎮",
		title:         "Category Search",
		accent:        m.theme.Warning,
		height:        layout.categoryPickerHeight,
		contentHeight: layout.categoryPickerContentHeight,
		framed:        layout.categoryPickerFramed,
		lines: func(width, height int) []string {
			return m.categoryPickerLines(width, height)
		},
	})
}

func (m shellModel) categoryPickerLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	header := " Category search (enter=select, esc=cancel)"
	switch {
	case m.services.gameLookup == nil:
		header = " Category search: unavailable (missing Twitch API credentials)"
	case m.categoryPicker.loading:
		header = " Category search: searching..."
	case m.categoryPicker.err != "":
		header = " Category search: " + m.categoryPicker.err
	case m.categoryPicker.query != "":
		header = " Category search: " + m.categoryPicker.query
	}
	lines := []string{fitLine(header, width)}
	if height == 1 {
		return lines
	}

	entries := m.categoryPickerEntries()
	start, selected := pickerWindow(m.categoryPicker.selected, len(entries), height)
	for i := start; i < len(entries) && len(lines) < height; i++ {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		lines = append(lines, fitLine(prefix+entries[i].Name, width))
	}
	for len(lines) < height {
		lines = append(lines, fitLine("", width))
	}
	return lines[:height]
}
