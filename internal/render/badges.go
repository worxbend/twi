package render

import (
	"strings"

	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
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

// badgeStyle is everything the renderer knows about drawing one Twitch badge
// set. The three ways a badge can appear -- as a glyph, as a short bracketed
// label, and in whichever palette color both use -- live in one record so that
// adding a badge set means filling in one row rather than remembering to touch
// three lookups scattered across the package.
type badgeStyle struct {
	// glyph is the single-cell icon drawn in BadgeModeGlyph.
	glyph string
	// shortLabel is the abbreviation drawn in fixed-width text badges, such
	// as "mod" for "moderator". An empty shortLabel means the set has no
	// accepted abbreviation, and its raw set ID is drawn instead.
	shortLabel string
	// color picks the badge's foreground out of the active theme, so the
	// badge follows the user's palette instead of a hard-coded color.
	color func(theme.Palette) string
}

// The palette accessors the badge table selects colors with. They exist so the
// table reads as data rather than as a column of identical closures.
func paletteError(p theme.Palette) string   { return p.Error }
func paletteSuccess(p theme.Palette) string { return p.Success }
func paletteAccent(p theme.Palette) string  { return p.Accent }
func paletteWarning(p theme.Palette) string { return p.Warning }
func paletteMuted(p theme.Palette) string   { return p.Muted }

// unknownBadgeGlyph stands in for a badge set twi has never heard of, so an
// unrecognized badge still occupies its column instead of silently vanishing.
const unknownBadgeGlyph = "•"

// badgeStyles maps Twitch badge set IDs onto how twi draws them. Keys are
// lower-case; lookupBadgeStyle normalizes before matching.
//
// The glyphs are plain Unicode symbols, not Nerd Font private-use codepoints,
// so they render in an unpatched terminal font. Every one is deliberately
// width-1 under uniseg so BadgeGlyphWidth stays accurate and badge columns line
// up between rows - emoji-presentation characters would be width-2 and break
// that alignment. TestBadgeStylesStayInSync enforces both properties.
//
// The colors separate authority (broadcaster, moderator) from status
// (subscriber, VIP) while keeping the icons readable as a group.
var badgeStyles = map[string]badgeStyle{
	"broadcaster": {glyph: "◉", shortLabel: "cast", color: paletteError},
	"moderator":   {glyph: "⚔", shortLabel: "mod", color: paletteSuccess},
	"vip":         {glyph: "◆", shortLabel: "vip", color: paletteAccent},
	"subscriber":  {glyph: "★", shortLabel: "sub", color: paletteWarning},
	"founder":     {glyph: "♦", shortLabel: "found", color: paletteWarning},
	"staff":       {glyph: "⚙", color: paletteSuccess},
	"admin":       {glyph: "⚙", color: paletteSuccess},
	"global_mod":  {glyph: "⚙", color: paletteSuccess},
	"partner":     {glyph: "✓", color: paletteMuted},
	"turbo":       {glyph: "↯", color: paletteMuted},
	"premium":     {glyph: "♛", color: paletteWarning},
	"bits":        {glyph: "◈", color: paletteMuted},
	"bits-leader": {glyph: "◈", color: paletteMuted},
	"sub-gifter":  {glyph: "♥", color: paletteWarning},
	"artist":      {glyph: "✎", color: paletteAccent},
	"no_audio":    {glyph: "♪", color: paletteMuted},
	"no_video":    {glyph: "▣", color: paletteMuted},
}

// lookupBadgeStyle finds how a badge set is drawn, reporting whether twi knows
// the set at all. Set IDs arrive from Twitch tags, so the key is trimmed and
// lower-cased rather than trusted to be exactly as written in the table.
func lookupBadgeStyle(setID string) (badgeStyle, bool) {
	style, ok := badgeStyles[strings.ToLower(strings.TrimSpace(setID))]
	return style, ok
}

// BadgeGlyph returns the icon for a badge set, and whether one is defined.
// Unknown sets fall back to a neutral marker so an unrecognized badge still
// occupies its column rather than silently disappearing.
func BadgeGlyph(setID string) (string, bool) {
	style, ok := lookupBadgeStyle(setID)
	if !ok || style.glyph == "" {
		return unknownBadgeGlyph, false
	}
	return style.glyph, true
}

// badgeGlyphColor picks the palette color for a badge set. Unknown sets render
// muted, which keeps them legible without implying they carry any authority.
func badgeGlyphColor(setID string, palette theme.Palette) string {
	style, ok := lookupBadgeStyle(setID)
	if !ok || style.color == nil {
		return palette.Muted
	}
	return style.color(palette)
}

// BadgeModes lists every badge mode a config file may name. See LayoutModes
// for why the set lives with the type rather than with its validators.
func BadgeModes() []string {
	return []string{string(BadgeModeGlyph), string(BadgeModeText), string(BadgeModeOff)}
}

// badgeFragments renders a message's badges according to the active badge
// mode: bracketed text labels, single-cell glyphs, or nothing.
func badgeFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	mode := opts.badgeMode()
	if mode == BadgeModeOff || len(msg.Badges) == 0 {
		return nil
	}
	// Each glyph fragment reserves BadgeGlyphWidth (icon plus one trailing
	// pad cell), so callers supply any leading separator themselves rather
	// than getting a doubled space after an element that already ends in one.
	fragments := make([]Fragment, 0, len(msg.Badges))
	if mode == BadgeModeGlyph {
		for _, badge := range msg.Badges {
			fragments = append(fragments, badgeGlyphFragment(badge, opts))
		}
		return fragments
	}
	for _, badge := range msg.Badges {
		fragments = append(fragments, badgeFallbackFragment(badge, opts))
	}
	return fragments
}

// badgeGlyphFragment renders one badge as a colored icon. The fixed
// BadgeGlyphWidth keeps badge columns aligned between rows even when a badge
// set has no glyph mapping and falls back to a neutral marker.
func badgeGlyphFragment(badge twitch.Badge, opts Options) Fragment {
	glyph, _ := BadgeGlyph(badge.SetID)
	return Fragment{
		Kind:       FragmentBadge,
		Text:       glyph,
		WidthCells: BadgeGlyphWidth,
		Style: FragmentStyle{
			Foreground: badgeGlyphColor(badge.SetID, opts.Palette),
			Bold:       true,
		},
		Ref: badgeAssetRef(badge),
	}
}

func badgeLabel(badge twitch.Badge) string {
	name := emptyFallback(badge.SetID, "badge")
	if badge.ID != "" && badge.ID != "1" {
		name += "/" + badge.ID
	}
	return "[" + name + "]"
}

func badgeFallbackFragment(badge twitch.Badge, opts Options) Fragment {
	width := badgeFallbackWidth(badge, opts.Assets)
	text := badgeLabel(badge) + " "
	if opts.Assets.BadgeWidthCells > 0 {
		text = compactBadgeLabel(badge)
	}
	return Fragment{
		Kind:       FragmentBadge,
		Text:       text,
		WidthCells: width,
		Style: FragmentStyle{
			Foreground: opts.Palette.Accent,
			Bold:       true,
		},
		Ref: badgeAssetRef(badge),
	}
}

// badgeSetWidth is the total width a message's badges will occupy under the
// active badge mode, used to budget the message prefix before rendering.
func badgeSetWidth(badges []twitch.Badge, opts Options) int {
	switch opts.badgeMode() {
	case BadgeModeOff:
		return 0
	case BadgeModeGlyph:
		return len(badges) * BadgeGlyphWidth
	default:
		width := 0
		for _, badge := range badges {
			width += badgeFallbackWidth(badge, opts.Assets)
		}
		return width
	}
}

func badgeFallbackWidth(badge twitch.Badge, assets AssetOptions) int {
	if assets.BadgeWidthCells > 0 {
		return assets.BadgeWidthCells
	}
	return textWidth(badgeLabel(badge) + " ")
}

// compactBadgeLabel names a badge in the few cells a fixed-width text badge
// gets. Sets with an accepted abbreviation in badgeStyles use it; anything else
// keeps its raw set ID, which fitCells later truncates if it does not fit.
//
// A badge version other than "1" is appended ("sub/12" for a 12-month
// subscriber) when the name is short enough to leave room for it.
func compactBadgeLabel(badge twitch.Badge) string {
	name := badge.SetID
	if style, ok := lookupBadgeStyle(badge.SetID); ok && style.shortLabel != "" {
		name = style.shortLabel
	}
	if name == "" {
		name = "badge"
	}
	if badge.ID != "" && badge.ID != "1" && textWidth(name) <= 3 {
		name += "/" + badge.ID
	}
	return "[" + name + "]"
}

func badgeAssetID(badge twitch.Badge) string {
	if badge.ID == "" {
		return badge.SetID
	}
	return badge.SetID + "/" + badge.ID
}

func badgeAssetRef(badge twitch.Badge) twitch.AssetRef {
	ref := badge.Ref
	if ref.Kind == "" {
		ref.Kind = "badge"
	}
	if ref.ID == "" {
		ref.ID = badgeAssetID(badge)
	}
	return ref
}
