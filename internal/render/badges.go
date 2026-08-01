package render

import (
	"strings"

	"github.com/worxbend/twi/internal/theme"
)

// BadgeMode selects how Twitch badges are drawn beside a username.
type BadgeMode string

const (
	// BadgeModeText draws bracketed labels such as "[mod]". Widest, but
	// unambiguous on any terminal and any font.
	BadgeModeText BadgeMode = "text"
	// BadgeModeGlyph draws a single icon per badge. Compact enough to sit
	// inline with the username without pushing message text off the row.
	BadgeModeGlyph BadgeMode = "glyph"
	// BadgeModeOff hides badges entirely.
	BadgeModeOff BadgeMode = "off"
)

// DefaultBadgeMode is used when a config value is missing or unrecognized.
const DefaultBadgeMode = BadgeModeGlyph

// NormalizeBadgeMode maps a config string onto a known mode, falling back to
// DefaultBadgeMode so an unrecognized value degrades instead of failing.
func NormalizeBadgeMode(value string) BadgeMode {
	switch BadgeMode(strings.ToLower(strings.TrimSpace(value))) {
	case BadgeModeText:
		return BadgeModeText
	case BadgeModeGlyph:
		return BadgeModeGlyph
	case BadgeModeOff:
		return BadgeModeOff
	default:
		return DefaultBadgeMode
	}
}

// BadgeGlyphWidth is the cell width reserved for one glyph badge: the icon
// plus a single separating space.
const BadgeGlyphWidth = 2

// badgeGlyphs maps Twitch badge set IDs to single-cell icons.
//
// These are plain Unicode symbols, not Nerd Font private-use codepoints, so
// they render in an unpatched terminal font. Every entry is deliberately
// width-1 under uniseg so BadgeGlyphWidth stays accurate and badge columns
// line up between rows - emoji-presentation characters would be width-2 and
// break that alignment.
var badgeGlyphs = map[string]string{
	"broadcaster": "◉",
	"moderator":   "⚔",
	"vip":         "◆",
	"subscriber":  "★",
	"founder":     "♦",
	"staff":       "⚙",
	"admin":       "⚙",
	"global_mod":  "⚙",
	"partner":     "✓",
	"turbo":       "↯",
	"premium":     "♛",
	"bits":        "◈",
	"bits-leader": "◈",
	"sub-gifter":  "♥",
	"artist":      "✎",
	"no_audio":    "♪",
	"no_video":    "▣",
}

// BadgeGlyph returns the icon for a badge set, and whether one is defined.
// Unknown sets fall back to a neutral marker so an unrecognized badge still
// occupies its column rather than silently disappearing.
func BadgeGlyph(setID string) (string, bool) {
	glyph, ok := badgeGlyphs[strings.ToLower(strings.TrimSpace(setID))]
	if !ok {
		return "•", false
	}
	return glyph, true
}

// badgeGlyphColor picks a palette color per badge set so the icons stay
// readable as a group while still distinguishing authority (broadcaster and
// moderator) from status (subscriber, VIP, and everything else).
func badgeGlyphColor(setID string, palette theme.Palette) string {
	switch strings.ToLower(strings.TrimSpace(setID)) {
	case "broadcaster":
		return palette.Error
	case "moderator", "staff", "admin", "global_mod":
		return palette.Success
	case "vip", "artist":
		return palette.Accent
	case "subscriber", "founder", "premium", "sub-gifter":
		return palette.Warning
	default:
		return palette.Muted
	}
}
