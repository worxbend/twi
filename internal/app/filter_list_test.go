package app

import (
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFilterListMoveWrapsAroundBothEnds(t *testing.T) {
	list := filterList{selected: 2}
	list.move(1, 3)
	if list.selected != 0 {
		t.Fatalf("move(1) off the end = %d, want 0", list.selected)
	}
	list.move(-1, 3)
	if list.selected != 2 {
		t.Fatalf("move(-1) off the start = %d, want 2 (the last row)", list.selected)
	}
	list.move(-1, 3)
	if list.selected != 1 {
		t.Fatalf("move(-1) = %d, want 1", list.selected)
	}
}

func TestFilterListMoveWithNoRowsParksAtZero(t *testing.T) {
	list := filterList{selected: 7}
	list.move(1, 0)
	if list.selected != 0 {
		t.Fatalf("move on an empty list = %d, want 0", list.selected)
	}
}

func TestFilterListClampDoesNotWrap(t *testing.T) {
	// Past the end lands on the last row rather than jumping back to the
	// first, which is what separates clamp from move.
	list := filterList{selected: 9}
	list.clamp(3)
	if list.selected != 2 {
		t.Fatalf("clamp(3) with selected=9 = %d, want 2", list.selected)
	}

	list = filterList{selected: -4}
	list.clamp(3)
	if list.selected != 0 {
		t.Fatalf("clamp(3) with selected=-4 = %d, want 0", list.selected)
	}

	list = filterList{selected: 1}
	list.clamp(0)
	if list.selected != 0 {
		t.Fatalf("clamp on an empty list = %d, want 0", list.selected)
	}

	list = filterList{selected: 1}
	list.clamp(5)
	if list.selected != 1 {
		t.Fatalf("clamp left an in-range selection at %d, want 1", list.selected)
	}
}

func TestFilterListEditsResetTheHighlight(t *testing.T) {
	list := filterList{query: "ka", selected: 4}
	list.insert([]rune("pp"))
	if list.query != "kapp" || list.selected != 0 {
		t.Fatalf("insert = %q/%d, want \"kapp\"/0", list.query, list.selected)
	}

	list.selected = 3
	list.deleteRune()
	if list.query != "kap" || list.selected != 0 {
		t.Fatalf("deleteRune = %q/%d, want \"kap\"/0", list.query, list.selected)
	}

	list.selected = 2
	list.clearQuery()
	if list.query != "" || list.selected != 0 {
		t.Fatalf("clearQuery = %q/%d, want \"\"/0", list.query, list.selected)
	}
}

func TestFilterListDeleteRuneOnEmptyQueryKeepsTheHighlight(t *testing.T) {
	// Nothing was edited, so there is no reason to move the highlight.
	list := filterList{selected: 3}
	list.deleteRune()
	if list.query != "" || list.selected != 3 {
		t.Fatalf("deleteRune on an empty query = %q/%d, want \"\"/3", list.query, list.selected)
	}
}

// TestFilterListDeleteRuneMatchesByteSlicing pins down the one place where the
// four overlays had drifted before they shared this code: two of them trimmed
// the query by converting it to a []rune and dropping the last element, the
// other two decoded the final rune with utf8.DecodeLastRuneInString and sliced
// that many bytes off the end. The shared helper uses the []rune form, so this
// test checks the two agree on the text a query can actually contain - the
// query is only ever built from keystrokes, which are always valid UTF-8.
func TestFilterListDeleteRuneMatchesByteSlicing(t *testing.T) {
	byteSlicing := func(query string) string {
		if n := len(query); n > 0 {
			_, size := utf8.DecodeLastRuneInString(query)
			return query[:n-size]
		}
		return query
	}

	queries := []string{
		"",
		"a",
		"kappa",
		"café",  // trailing 2-byte rune
		"日本語",   // 3-byte runes
		"pog🎉",  // 4-byte rune outside the basic plane
		"🎉🎉",    // nothing but 4-byte runes
		"é",    // combining acute accent as its own rune
		"👨‍👩‍👧", // zero-width joiner family sequence
		" ",
		"two words ",
	}
	for _, query := range queries {
		list := filterList{query: query}
		list.deleteRune()
		if want := byteSlicing(query); list.query != want {
			t.Fatalf("deleteRune(%q) = %q, byte slicing gives %q", query, list.query, want)
		}
	}
}

func TestHandleFilterListKeyCoversTheSharedKeys(t *testing.T) {
	tests := []struct {
		name         string
		msg          tea.KeyMsg
		start        filterList
		count        int
		wantQuery    string
		wantSelected int
	}{
		{
			name:         "up wraps to the last row",
			msg:          tea.KeyMsg{Type: tea.KeyUp},
			start:        filterList{selected: 0},
			count:        4,
			wantSelected: 3,
		},
		{
			name:         "down advances",
			msg:          tea.KeyMsg{Type: tea.KeyDown},
			start:        filterList{selected: 0},
			count:        4,
			wantSelected: 1,
		},
		{
			name:         "tab advances like down",
			msg:          tea.KeyMsg{Type: tea.KeyTab},
			start:        filterList{selected: 0},
			count:        4,
			wantSelected: 1,
		},
		{
			name:      "runes are appended",
			msg:       tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ok")},
			start:     filterList{query: "so", selected: 2},
			count:     4,
			wantQuery: "sook",
		},
		{
			name:      "space is typed, not swallowed",
			msg:       tea.KeyMsg{Type: tea.KeySpace},
			start:     filterList{query: "two", selected: 2},
			count:     4,
			wantQuery: "two ",
		},
		{
			name:      "backspace trims a rune",
			msg:       tea.KeyMsg{Type: tea.KeyBackspace},
			start:     filterList{query: "pog🎉", selected: 2},
			count:     4,
			wantQuery: "pog",
		},
		{
			name:      "ctrl+h is backspace on terminals that send it",
			msg:       tea.KeyMsg{Type: tea.KeyCtrlH},
			start:     filterList{query: "pog", selected: 2},
			count:     4,
			wantQuery: "po",
		},
		{
			name:      "ctrl+u clears the query",
			msg:       tea.KeyMsg{Type: tea.KeyCtrlU},
			start:     filterList{query: "pog", selected: 2},
			count:     4,
			wantQuery: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			list := test.start
			if !handleFilterListKey(test.msg, &list, test.count) {
				t.Fatalf("handleFilterListKey(%v) reported the key as unhandled", test.msg.Type)
			}
			if list.query != test.wantQuery {
				t.Fatalf("query = %q, want %q", list.query, test.wantQuery)
			}
			if list.selected != test.wantSelected {
				t.Fatalf("selected = %d, want %d", list.selected, test.wantSelected)
			}
		})
	}
}

func TestHandleFilterListKeyLeavesOtherKeysAlone(t *testing.T) {
	// Escape and enter mean something different in every overlay, so the
	// shared helper must report them as unhandled and touch nothing.
	for _, keyType := range []tea.KeyType{tea.KeyEsc, tea.KeyEnter, tea.KeyLeft, tea.KeyHome} {
		list := filterList{query: "pog", selected: 2}
		if handleFilterListKey(tea.KeyMsg{Type: keyType}, &list, 4) {
			t.Fatalf("handleFilterListKey(%v) reported the key as handled, want unhandled", keyType)
		}
		if list.query != "pog" || list.selected != 2 {
			t.Fatalf("unhandled key changed the list to %q/%d", list.query, list.selected)
		}
	}
}
