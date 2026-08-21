package render

import (
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

// LayoutMode selects how a message's author and text are arranged.
type LayoutMode string

const (
	// LayoutInline puts the author and the message on one row:
	// "12:00 [AB] Name: message text". Densest layout; every row is
	// self-describing, which suits fast-moving chat.
	LayoutInline LayoutMode = "inline"
	// LayoutGrouped puts the author on their own header row with the
	// message text indented beneath it, and omits the header entirely for
	// consecutive messages from the same author (see Options.ContinuesGroup).
	// Trades vertical space for a much clearer read of who said what.
	LayoutGrouped LayoutMode = "grouped"
	// LayoutCompact drops avatars, timestamps, and badges, leaving
	// "Name: message". For narrow terminals and side-by-side panes.
	LayoutCompact LayoutMode = "compact"
)

// DefaultLayoutMode is used when a config value is missing or unrecognized.
const DefaultLayoutMode = LayoutInline

// LayoutModes lists every layout a config file may name.
//
// It exists so that whoever validates configuration -- `twi doctor`, `twi
// setup` -- asks this package rather than repeating the strings. Those lists
// had drifted before: a mode added here and not there makes doctor report a
// perfectly valid config as "unknown".
func LayoutModes() []string {
	return []string{string(LayoutInline), string(LayoutGrouped), string(LayoutCompact)}
}

// NormalizeLayoutMode maps a config string onto a known layout, falling back
// to DefaultLayoutMode so an unrecognized value degrades instead of failing.
func NormalizeLayoutMode(value string) LayoutMode {
	switch LayoutMode(strings.ToLower(strings.TrimSpace(value))) {
	case LayoutInline:
		return LayoutInline
	case LayoutGrouped:
		return LayoutGrouped
	case LayoutCompact:
		return LayoutCompact
	default:
		return DefaultLayoutMode
	}
}

// groupedRows renders LayoutGrouped: an author header row followed by the
// message text indented beneath it. Consecutive messages from the same author
// (Options.ContinuesGroup) skip the header, so a run of messages reads as one
// block under a single name.
func groupedRows(msg twitch.ChatMessage, opts Options) []Row {
	indent := groupedIndentWidth(opts.Width)
	var rows []Row
	if !opts.ContinuesGroup {
		header := groupedHeaderFragments(msg, opts)
		headerRows, current, _ := appendWrappedFragments(nil, Row{}, 0, header, opts.Width, indent)
		rows = append(headerRows, current)
	}

	content := messageContent(msg, opts)
	indentFragment := []Fragment{{Kind: FragmentText, Text: strings.Repeat(" ", indent)}}
	bodyRows, current, _ := appendWrappedFragments(nil, Row{}, 0, indentFragment, opts.Width, 0)
	bodyRows, current, _ = appendWrappedFragments(bodyRows, current, indent, content, opts.Width, indent)
	rows = append(rows, append(bodyRows, current)...)
	return rows
}

// groupedIndentWidth is how far grouped message text is inset under its
// author header. It scales down on narrow terminals so the indent never eats
// the message.
func groupedIndentWidth(width int) int {
	switch {
	case width >= 40:
		return 3
	case width >= 20:
		return 2
	default:
		return 0
	}
}

// Responsive breakpoints: the narrowest terminal width at which each optional
// decoration is still drawn. Below a decoration's breakpoint it is dropped so
// the message text keeps its room.
//
// The two layouts drop things in different orders because they spend their
// horizontal space differently, so each keeps its own ladder:
//
//   - Inline (chooseDecorations) puts everything on the message row, so the
//     widest and least essential goes first: badges, then the avatar, then the
//     timestamp. The first-message mark shares the avatar's breakpoint.
//   - Grouped (groupedHeaderFragments) gives the author their own row, which
//     leaves more room for identity and less need for the clock: author
//     metadata goes first, then the timestamp, then the avatar, and badges are
//     kept the longest.
//
// The numbers are paired, not shared: making both layouts drop a decoration at
// the same width would change what is rendered.
const (
	inlineMinWidthForTimestamp    = 16
	inlineMinWidthForAvatar       = 24
	inlineMinWidthForFirstMessage = 24
	inlineMinWidthForBadges       = 28

	groupedMinWidthForBadges     = 24
	groupedMinWidthForAvatar     = 28
	groupedMinWidthForTimestamp  = 30
	groupedMinWidthForAuthorMeta = 46
)

// groupedHeaderFragments builds the author row for LayoutGrouped: avatar
// chip, username, badges, then muted metadata (timestamp and author context).
// The username carries the heading weight here - a terminal cannot vary font
// size, so the visual hierarchy between "who" and "what" comes from putting
// the name on its own bold, colored row above unemphasized body text.
func groupedHeaderFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	var fragments []Fragment
	if opts.Assets.ShowAvatars && opts.Width >= groupedMinWidthForAvatar {
		fragments = append(fragments, avatarFallbackFragment(msg, opts, displayAuthor(msg)))
	}
	fragments = append(fragments, usernameFragment(msg, opts))
	if badges := badgeFragments(msg, opts); len(badges) > 0 && opts.Width >= groupedMinWidthForBadges {
		fragments = append(fragments, Fragment{Kind: FragmentText, Text: " "})
		fragments = append(fragments, badges...)
	}
	if opts.Width >= groupedMinWidthForTimestamp {
		fragments = append(fragments, Fragment{
			Kind:  FragmentTimestamp,
			Text:  " " + timestampText(msg.Timestamp),
			Style: FragmentStyle{Foreground: opts.Palette.Muted},
		})
	}
	if opts.Width >= groupedMinWidthForAuthorMeta {
		fragments = append(fragments, authorMetaFragments(opts)...)
	}
	return fragments
}
