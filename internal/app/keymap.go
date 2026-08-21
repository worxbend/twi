package app

import "strings"

// keyBinding is one documented key and what it does.
//
// The help footer, the expanded help overlay, and the command palette were
// three separately maintained lists, and they had already drifted: ctrl+e
// opens the emote picker dozens of times a session and appeared in none of
// them, discoverable only by reading the README. This table is the single
// source the generated surfaces read from, and keymapCoverage_test.go fails
// when a key is handled in Update without appearing here.
type keyBinding struct {
	// Keys is the binding as a user would type it ("ctrl+e", "space c").
	Keys string
	// Description is the sentence used in expanded help and the palette.
	Description string
	// Group orders bindings within the expanded help.
	Group keyGroup
}

type keyGroup int

const (
	keyGroupChat keyGroup = iota
	keyGroupChannels
	keyGroupView
	keyGroupDisplay
	keyGroupSession
)

// keyBindings is the whole documented keymap, in the order help presents it.
var keyBindings = []keyBinding{
	{Keys: "i/o", Description: "compose", Group: keyGroupChat},
	{Keys: "esc", Description: "back to chat", Group: keyGroupChat},
	{Keys: "j/k", Description: "select message", Group: keyGroupChat},
	{Keys: "pgup/pgdn", Description: "scroll", Group: keyGroupChat},
	{Keys: "r", Description: "reply", Group: keyGroupChat},
	{Keys: "K", Description: "inspect", Group: keyGroupChat},
	{Keys: "@+tab", Description: "complete name", Group: keyGroupChat},
	{Keys: "ctrl+e", Description: "emotes", Group: keyGroupChat},

	{Keys: "space e", Description: "channel sidebar", Group: keyGroupChannels},
	{Keys: "space c", Description: "open channel", Group: keyGroupChannels},
	{Keys: "space x", Description: "close channel", Group: keyGroupChannels},
	{Keys: "space a", Description: "activity column", Group: keyGroupChannels},
	{Keys: "space i", Description: "inspect", Group: keyGroupChannels},
	{Keys: "[/]", Description: "switch", Group: keyGroupChannels},

	{Keys: "1-4", Description: "filters", Group: keyGroupView},
	{Keys: "0", Description: "reset filters", Group: keyGroupView},
	{Keys: "alt+1/2/3", Description: "tabs", Group: keyGroupView},
	{Keys: "tab", Description: "focus chat/composer/channels", Group: keyGroupView},
	{Keys: "?", Description: "help", Group: keyGroupView},
	{Keys: "</>", Description: "resize pane", Group: keyGroupView},
	{Keys: "=", Description: "reset pane sizes", Group: keyGroupView},

	{Keys: "ctrl+t", Description: "theme", Group: keyGroupDisplay},
	{Keys: "ctrl+g", Description: "layout", Group: keyGroupDisplay},
	{Keys: "ctrl+b", Description: "badges", Group: keyGroupDisplay},
	{Keys: "ctrl+y", Description: "emote highlight", Group: keyGroupDisplay},
	{Keys: "ctrl+n", Description: "full names", Group: keyGroupDisplay},

	{Keys: "ctrl+p", Description: "commands", Group: keyGroupSession},
	{Keys: "ctrl+r", Description: "reconnect", Group: keyGroupSession},
	{Keys: "ctrl+l", Description: "clear (asks first)", Group: keyGroupSession},
	{Keys: "q", Description: "quit", Group: keyGroupSession},
	{Keys: "ctrl+c", Description: "quit", Group: keyGroupSession},
}

// keyBindingsInGroup returns the bindings for one help section.
func keyBindingsInGroup(group keyGroup) []keyBinding {
	var out []keyBinding
	for _, binding := range keyBindings {
		if binding.Group == group {
			out = append(out, binding)
		}
	}
	return out
}

// helpGroupLine renders one expanded-help row from the table.
func helpGroupLine(group keyGroup) string {
	bindings := keyBindingsInGroup(group)
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		parts = append(parts, binding.Keys+": "+binding.Description)
	}
	return " " + strings.Join(parts, " | ")
}

// compactFooter is the one-line footer, in the order it reads best rather
// than the order the table declares. Each entry names a binding by its Keys
// value, and TestCompactFooterOnlyNamesDocumentedKeys fails if one of them
// stops existing, so the short form cannot outlive the key it advertises.
var compactFooter = []struct {
	Keys  string
	Label string
}{
	{Keys: "ctrl+p", Label: "ctrl+p"},
	{Keys: "i/o", Label: "i/esc"},
	{Keys: "j/k", Label: "jk"},
	{Keys: "space e", Label: "space e/c"},
	{Keys: "[/]", Label: "[]"},
	{Keys: "1-4", Label: "1-4/0"},
	{Keys: "?", Label: "?"},
	{Keys: "r", Label: "r/K"},
	{Keys: "ctrl+e", Label: "ctrl+e"},
	{Keys: "q", Label: "q quit/ctrl+c quit"},
}

func compactHelpLine() string {
	parts := make([]string, 0, len(compactFooter))
	for _, entry := range compactFooter {
		parts = append(parts, entry.Label)
	}
	return " " + strings.Join(parts, " | ")
}

// documentedKeys reports every key form named in the table, for the coverage
// test that compares it against what Update actually handles.
func documentedKeys() map[string]bool {
	keys := make(map[string]bool, len(keyBindings)*2)
	for _, binding := range keyBindings {
		for _, key := range strings.Split(binding.Keys, "/") {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			keys[key] = true
			// A digit range such as "1-4" documents each key in it; other
			// surfaces name them individually.
			if len(key) == 3 && key[1] == '-' && key[0] >= '0' && key[0] <= '9' && key[2] >= '0' && key[2] <= '9' {
				for digit := key[0]; digit <= key[2]; digit++ {
					keys[string(digit)] = true
				}
			}
		}
	}
	return keys
}
