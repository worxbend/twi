// Package animation turns rendered chat rows into bounded reveal state.
//
// The package works on grapheme-safe render units instead of raw bytes or
// runes, which keeps emoji, combining characters, ANSI styling, and image
// placeholders intact during typed-in message animation.
package animation
