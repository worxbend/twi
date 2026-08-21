package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ctrlKeyPattern finds the tea.KeyCtrlX constants the model actually handles.
var ctrlKeyPattern = regexp.MustCompile(`tea\.KeyCtrl([A-Z])\b`)

// TestEveryHandledCtrlKeyIsDocumented is the guard that would have caught the
// drift this table exists to prevent.
//
// Key handling, the help footer, the expanded help, and the command palette
// were four separately maintained things. ctrl+e opens the emote picker --
// used dozens of times a session -- and had fallen out of every documented
// surface, discoverable only by reading the README. Scanning the source for
// handled keys and comparing against the table catches the next one.
func TestEveryHandledCtrlKeyIsDocumented(t *testing.T) {
	// Keys handled only inside a modal that documents itself on screen, or
	// that are standard text-editing bindings within an input.
	exempt := map[string]bool{
		"ctrl+h": true, // category picker: backspace alias
		"ctrl+u": true, // input: clear line
		"ctrl+s": true, // stream info: save, labeled on the tab itself
	}

	documented := documentedKeys()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ctrlKeyPattern.FindAllStringSubmatch(string(source), -1) {
			key := "ctrl+" + strings.ToLower(match[1])
			if _, ok := seen[key]; !ok {
				seen[key] = name
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no tea.KeyCtrl* handlers; the scan is broken, not the keymap")
	}

	for key, file := range seen {
		if exempt[key] || documented[key] {
			continue
		}
		t.Errorf("%s is handled in %s but is not in keyBindings, so it appears in no help surface", key, file)
	}
}

// TestCompactFooterOnlyNamesDocumentedKeys keeps the short footer from
// advertising a binding the table no longer has.
func TestCompactFooterOnlyNamesDocumentedKeys(t *testing.T) {
	for _, entry := range compactFooter {
		if _, ok := bindingForKeys(entry.Keys); !ok {
			t.Errorf("compact footer names %q, which is not in keyBindings", entry.Keys)
		}
	}
}

// TestExpandedHelpCoversEveryBinding makes the table the thing that is
// rendered, rather than a list that happens to sit beside the rendering.
func TestExpandedHelpCoversEveryBinding(t *testing.T) {
	rendered := strings.Join([]string{
		helpGroupLine(keyGroupChat),
		helpGroupLine(keyGroupChannels),
		helpGroupLine(keyGroupView),
		helpGroupLine(keyGroupDisplay),
		helpGroupLine(keyGroupSession),
	}, "\n")

	for _, binding := range keyBindings {
		if !strings.Contains(rendered, binding.Keys) {
			t.Errorf("binding %q is in the table but reaches no help group", binding.Keys)
		}
	}
}

// TestEmotePickerKeyIsDiscoverable pins the specific regression by name.
func TestEmotePickerKeyIsDiscoverable(t *testing.T) {
	if !documentedKeys()["ctrl+e"] {
		t.Fatal("ctrl+e (emote picker) is undocumented again")
	}
	if !strings.Contains(compactHelpLine(), "ctrl+e") {
		t.Fatal("ctrl+e is missing from the compact footer")
	}
	if !strings.Contains(helpGroupLine(keyGroupChat), "ctrl+e") {
		t.Fatal("ctrl+e is missing from the expanded help")
	}
}

// TestCommandPaletteReachesEveryDisplayKey covers the discoverability gap the
// palette had: the emote picker, the theme page and all four display toggles
// were reachable only by already knowing the key, which is the one situation
// a command palette exists to fix.
func TestCommandPaletteReachesEveryDisplayKey(t *testing.T) {
	model := chatModelWithMessages(t, 3, nil)
	var shortcuts []string
	for _, command := range model.commandPaletteCommands() {
		shortcuts = append(shortcuts, command.shortcut)
	}
	joined := strings.Join(shortcuts, " ")

	for _, key := range []string{"ctrl+e", "ctrl+t", "ctrl+g", "ctrl+b", "ctrl+y", "ctrl+n"} {
		if !strings.Contains(joined, key) {
			t.Errorf("%s has no command palette entry, so it is discoverable only by already knowing it", key)
		}
	}
}

// TestCommandPaletteShortcutsAreDocumentedKeys keeps the palette's shortcut
// column from naming a binding the keymap no longer has.
func TestCommandPaletteShortcutsAreDocumentedKeys(t *testing.T) {
	model := chatModelWithMessages(t, 1, nil)
	documented := documentedKeys()
	// Shortcuts that are not keymap bindings: filter states, channel names,
	// and the palette's own composite labels.
	skip := func(shortcut string) bool {
		return shortcut == "" ||
			strings.HasPrefix(shortcut, "#") ||
			shortcut == "active" ||
			strings.Contains(shortcut, " active") ||
			strings.Contains(shortcut, " / ")
	}
	for _, command := range model.commandPaletteCommands() {
		if skip(command.shortcut) {
			continue
		}
		if !documented[command.shortcut] {
			t.Errorf("palette entry %q advertises %q, which is not in keyBindings", command.title, command.shortcut)
		}
	}
}

// TestEveryLeaderChordIsDocumented keeps the in-app help honest about the
// space-leader bindings.
//
// The leader chord is the least discoverable input twi has: nothing on screen
// hints that space is a prefix, so a binding missing from the help is a
// feature nobody finds. `space a`, which shows and hides the activity column,
// had fallen out of the developer documentation exactly that way.
func TestEveryLeaderChordIsDocumented(t *testing.T) {
	chords := map[rune]string{
		leaderSidebarRune:       "channel sidebar",
		leaderChannelPickerRune: "open channel",
		leaderCloseChannelRune:  "close channel",
		leaderInspectRune:       "inspect",
		leaderActivityRune:      "activity column",
	}

	documented := documentedKeys()
	for chord, what := range chords {
		key := "space " + string(chord)
		if !documented[key] {
			t.Errorf("leader chord %q (%s) is not in keyBindings, so the help never mentions it", key, what)
		}
	}
}

// TestPaneSizingKeysAreDocumented covers the other half of the pane controls,
// which are ordinary runes rather than a chord but equally easy to omit.
func TestPaneSizingKeysAreDocumented(t *testing.T) {
	documented := documentedKeys()
	for _, key := range []string{"<", ">", "="} {
		if !documented[key] {
			t.Errorf("pane key %q is handled but not documented in keyBindings", key)
		}
	}
}
