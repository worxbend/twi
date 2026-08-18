// Package textsafe strips the characters that let remote text take over the
// terminal it is displayed in.
//
// twi draws text it did not write: chat messages, display names, notices,
// stream titles. A terminal does not distinguish "text to show" from
// "commands to obey" -- an ESC (0x1b) in the middle of a message starts an
// escape sequence, and everything after it is interpreted rather than
// printed. A chatter who types the right bytes could move the cursor, clear
// the screen, rewrite the window title, or, on some terminal emulators, worse.
// Nothing about the message looks unusual to the person sending it.
//
// So every string that reaches the screen from outside is passed through
// Display first. This is a display concern, not a parsing one: the wire text
// is kept intact wherever twi stores or forwards it, and cleaned on the way
// to the terminal.
package textsafe

import (
	"strings"
	"unicode"
)

// bidiControls are the Unicode formatting characters that reorder the text
// around them. They are not control characters in the C0/C1 sense and they
// print as nothing, but they let one string display as another -- the trick
// behind lookalike usernames and "trojan source" -- so display text does
// without them.
//
// Zero-width joiners (U+200D), zero-width non-joiners (U+200C) and variation
// selectors are deliberately absent from this list: emoji are built out of
// them and twi renders emoji on purpose.
func isBidiControl(r rune) bool {
	switch r {
	case '\u200e', '\u200f': // LEFT-TO-RIGHT MARK, RIGHT-TO-LEFT MARK
		return true
	case '\u202a', '\u202b', '\u202c', '\u202d', '\u202e': // embeddings and overrides
		return true
	case '\u2066', '\u2067', '\u2068', '\u2069': // isolates
		return true
	}
	return false
}

func unsafeForDisplay(r rune) bool {
	// unicode.IsControl covers C0 (including ESC), DEL, and C1.
	return unicode.IsControl(r) || isBidiControl(r)
}

// Display returns value with everything a terminal would act on removed,
// leaving the printable text -- including emoji and non-Latin scripts --
// exactly as it was.
//
// Characters are dropped rather than replaced. A replacement glyph would put
// visible noise into ordinary messages that merely contain a stray control
// byte, and there is nothing useful to show in its place.
func Display(value string) string {
	if !NeedsSanitizing(value) {
		// The overwhelmingly common case: nothing to do, no allocation.
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if unsafeForDisplay(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NeedsSanitizing reports whether Display would change value. It exists so
// hot paths can skip the copy, and so tests can assert on the condition
// directly.
func NeedsSanitizing(value string) bool {
	for _, r := range value {
		if unsafeForDisplay(r) {
			return true
		}
	}
	return false
}
