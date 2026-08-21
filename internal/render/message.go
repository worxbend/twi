package render

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/emoji"
	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultWidth       = 80
	minimumRenderWidth = 8
)

// FragmentKind identifies the semantic role of a render fragment.
type FragmentKind string

const (
	FragmentAvatar        FragmentKind = "avatar"
	FragmentTimestamp     FragmentKind = "timestamp"
	FragmentBadge         FragmentKind = "badge"
	FragmentUsername      FragmentKind = "username"
	FragmentText          FragmentKind = "text"
	FragmentMention       FragmentKind = "mention"
	FragmentReply         FragmentKind = "reply"
	FragmentNotice        FragmentKind = "notice"
	FragmentAction        FragmentKind = "action"
	FragmentDeleted       FragmentKind = "deleted"
	FragmentEmojiFallback FragmentKind = "emoji_fallback"
	FragmentEmoteFallback FragmentKind = "emote_fallback"
	FragmentFirstMessage  FragmentKind = "first_message"
)

// FragmentStyle describes terminal styling that can be applied without
// changing a fragment's layout width.
type FragmentStyle struct {
	Foreground    string
	Background    string
	Bold          bool
	Italic        bool
	Strikethrough bool
}

// Fragment is a normalized, styled segment of a rendered chat message.
type Fragment struct {
	Kind       FragmentKind
	Text       string
	Style      FragmentStyle
	Ref        twitch.AssetRef
	WidthCells int
}

// Width returns the terminal cell width reserved by the fragment.
func (f Fragment) Width() int {
	if f.WidthCells > 0 {
		return f.WidthCells
	}
	return textWidth(f.Text)
}

// Row is a width-bounded collection of render fragments.
type Row struct {
	Fragments []Fragment
}

// Plain returns the row fallback text without terminal styling.
func (r Row) Plain() string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		builder.WriteString(fragmentFallbackText(fragment))
	}
	return builder.String()
}

// String returns the row with ANSI styling applied.
func (r Row) String() string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		builder.WriteString(renderFragment(fragment))
	}
	return builder.String()
}

// TerminalString returns the row with styled text fallbacks (avatars,
// badges, emotes, and emoji always render as text - there is no image
// rendering path).
func (r Row) TerminalString() string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		builder.WriteString(renderFragment(fragment))
	}
	return builder.String()
}

// Width returns the terminal cell width reserved by the row.
func (r Row) Width() int {
	return fragmentsWidth(r.Fragments)
}

// TerminalStringWithBackground behaves like TerminalString, but fills every
// fragment's unset background with background instead of leaving it empty.
//
// Each fragment renders through its own independent lipgloss.Style.Render
// call and ends in its own ANSI reset. Wrapping the fully-assembled row in an
// outer Background() style afterward (as View() does for the whole screen)
// only colors text up to that row's first embedded reset — verified
// empirically against lipgloss v1.1.0 — so every fragment after the first
// falls back to the terminal's own default background, which many terminals
// render with the user's configured transparency/blur even after an OSC 11
// default-background override. Setting an explicit background on every
// fragment sidesteps that: explicit SGR backgrounds are always opaque,
// regardless of terminal transparency settings. Fragments that already carry
// their own background (e.g. the avatar-initials chip) are left untouched.
func (r Row) TerminalStringWithBackground(background string) string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		builder.WriteString(renderFragment(fragmentWithDefaultBackground(fragment, background)))
	}
	return builder.String()
}

func fragmentWithDefaultBackground(fragment Fragment, background string) Fragment {
	if fragment.Style.Background == "" {
		fragment.Style.Background = background
	}
	return fragment
}

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

// AuthorMeta is per-user context the renderer cannot derive from a message on
// its own. It is resolved by the app from its channel roster (badges seen so
// far, membership, polled follower data) and handed in per message.
//
// Every field is optional: a zero AuthorMeta renders no metadata rather than
// claiming a user has no roles or does not follow.
type AuthorMeta struct {
	// Role is the highest-precedence role label ("mod", "vip", ...) or "".
	Role string
	// SubscribedMonths is subscriber tenure in months, or 0 when unknown.
	SubscribedMonths int
	// FollowsSince is when the author followed the channel. It is only
	// meaningful when FollowKnown is set; twi usually has follower data for
	// just the most recent page of followers.
	FollowsSince time.Time
	FollowKnown  bool
	// FirstSeen is when twi first saw this author in this session. It is not
	// when they joined the channel - that is not knowable from IRC.
	FirstSeen time.Time
	// Now anchors relative durations. Zero means "use wall clock", which
	// tests override for determinism.
	Now time.Time
}

func (a AuthorMeta) empty() bool {
	return a.Role == "" && a.SubscribedMonths == 0 && !a.FollowKnown && a.FirstSeen.IsZero()
}

func (a AuthorMeta) now() time.Time {
	if a.Now.IsZero() {
		return time.Now()
	}
	return a.Now
}

// Options controls message rendering and wrapping.
type Options struct {
	Width   int
	Palette theme.Palette
	Assets  AssetOptions
	Layout  LayoutMode
	Badges  BadgeMode
	// ContinuesGroup marks a message as following another from the same
	// author. LayoutGrouped uses it to suppress the repeated author header.
	ContinuesGroup bool
	// FullUsername renders "DisplayName (login)" when Twitch's display name
	// differs from the login by more than capitalization, so localized or
	// stylized display names stay traceable to a real account.
	FullUsername bool
	// HighlightEmotes draws emotes and emoji on a tinted background so they
	// separate visually from surrounding text.
	HighlightEmotes bool
	// Meta is optional author context rendered beside the username.
	Meta AuthorMeta
}

// DefaultOptions returns renderer options using the default theme palette.
func DefaultOptions(width int) Options {
	return Options{
		Width:   width,
		Palette: theme.DefaultPalette(),
	}
}

func (o Options) layout() LayoutMode {
	return NormalizeLayoutMode(string(o.Layout))
}

func (o Options) badgeMode() BadgeMode {
	return NormalizeBadgeMode(string(o.Badges))
}

// emoteHighlightBlend is how far the emote/emoji chip background is pulled
// from the message surface toward the accent color. It is deliberately small:
// the chip must read as a raised surface behind the token, not as a block of
// solid color that overwhelms the text drawn on it.
const emoteHighlightBlend = 0.22

// emoteHighlight returns the background for emote tokens, or "" when
// highlighting is off. Blending from the theme's own surface keeps the chip
// legible in every preset instead of hard-coding a color that only works on
// dark themes.
func (o Options) emoteHighlight() string {
	if !o.HighlightEmotes {
		return ""
	}
	return theme.Mix(o.Palette.Surface, o.Palette.Accent, emoteHighlightBlend)
}

// emojiHighlight mirrors emoteHighlight for emoji, tinted toward the warning
// hue so emoji and channel emotes stay distinguishable from each other.
func (o Options) emojiHighlight() string {
	if !o.HighlightEmotes {
		return ""
	}
	return theme.Mix(o.Palette.Surface, o.Palette.Warning, emoteHighlightBlend)
}

// AssetOptions controls fixed placeholder widths for the text fallbacks
// (avatars, badges, emotes, and emoji always render as text).
type AssetOptions struct {
	ShowAvatars      bool
	AvatarWidthCells int
	BadgeWidthCells  int
	EmoteWidthCells  int
	EmojiWidthCells  int
}

// FallbackAssetOptions returns visually intentional text fallbacks that
// reserve stable widths for future avatar, badge, emote, and emoji images.
func FallbackAssetOptions() AssetOptions {
	return AssetOptions{
		ShowAvatars:      true,
		AvatarWidthCells: 5,
		BadgeWidthCells:  6,
		EmoteWidthCells:  6,
		EmojiWidthCells:  2,
	}
}

func (o AssetOptions) withFallbackWidths() AssetOptions {
	defaults := FallbackAssetOptions()
	if o.AvatarWidthCells <= 0 {
		o.AvatarWidthCells = defaults.AvatarWidthCells
	}
	if o.BadgeWidthCells < 0 {
		o.BadgeWidthCells = 0
	}
	if o.EmoteWidthCells < 0 {
		o.EmoteWidthCells = 0
	}
	if o.EmojiWidthCells < 0 {
		o.EmojiWidthCells = 0
	}
	return o
}

// Rows renders a normalized Twitch chat message into width-bounded rows.
func Rows(msg twitch.ChatMessage, opts Options) []Row {
	if opts.Width <= 0 {
		opts.Width = defaultWidth
	}
	if opts.Width < minimumRenderWidth {
		opts.Width = minimumRenderWidth
	}
	if opts.Palette == (theme.Palette{}) {
		opts.Palette = theme.DefaultPalette()
	}
	opts.Assets = opts.Assets.withFallbackWidths()

	if opts.layout() == LayoutGrouped {
		return groupedRows(msg, opts)
	}

	prefix := messagePrefix(msg, opts)
	content := messageContent(msg, opts)
	rows := wrap(prefix, content, opts.Width)
	if len(rows) == 0 {
		return []Row{{Fragments: prefix}}
	}
	return rows
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

// groupedHeaderFragments builds the author row for LayoutGrouped: avatar
// chip, username, badges, then muted metadata (timestamp and author context).
// The username carries the heading weight here - a terminal cannot vary font
// size, so the visual hierarchy between "who" and "what" comes from putting
// the name on its own bold, colored row above unemphasized body text.
func groupedHeaderFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	var fragments []Fragment
	if opts.Assets.ShowAvatars && opts.Width >= 28 {
		fragments = append(fragments, avatarFallbackFragment(msg, opts, displayAuthor(msg)))
	}
	fragments = append(fragments, usernameFragment(msg, opts))
	if badges := badgeFragments(msg, opts); len(badges) > 0 && opts.Width >= 24 {
		fragments = append(fragments, Fragment{Kind: FragmentText, Text: " "})
		fragments = append(fragments, badges...)
	}
	if opts.Width >= 30 {
		fragments = append(fragments, Fragment{
			Kind:  FragmentTimestamp,
			Text:  " " + timestampText(msg.Timestamp),
			Style: FragmentStyle{Foreground: opts.Palette.Muted},
		})
	}
	if opts.Width >= 46 {
		fragments = append(fragments, authorMetaFragments(opts)...)
	}
	return fragments
}

// PlainRows renders fallback text rows for callers that do their own styling.
func PlainRows(msg twitch.ChatMessage, width int) []string {
	rows := Rows(msg, DefaultOptions(width))
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain())
	}
	return plain
}

// StringRows renders ANSI-styled rows.
func StringRows(msg twitch.ChatMessage, width int) []string {
	rows := Rows(msg, DefaultOptions(width))
	rendered := make([]string, 0, len(rows))
	for _, row := range rows {
		rendered = append(rendered, row.String())
	}
	return rendered
}

// TextRow returns the first plain row for older single-row callers.
func TextRow(msg twitch.ChatMessage, width int) string {
	rows := PlainRows(msg, width)
	if len(rows) == 0 {
		return ""
	}
	return rows[0]
}

// usernameFragment renders the author name in their stable identity color.
//
// FullUsername appends the login when the display name is not simply a
// recapitalization of it - Twitch allows CJK and stylized display names that
// are otherwise impossible to tie back to an account you can mention.
func usernameFragment(msg twitch.ChatMessage, opts Options) Fragment {
	author := displayAuthor(msg)
	if opts.FullUsername {
		login := strings.TrimSpace(msg.AuthorLogin)
		if login != "" && !strings.EqualFold(login, author) {
			author += " (" + login + ")"
		}
	}
	return Fragment{
		Kind: FragmentUsername,
		Text: author,
		Style: FragmentStyle{
			Foreground: usernameColor(msg, opts.Palette),
			Bold:       true,
		},
	}
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

// authorMetaFragments renders the muted author context: role, subscriber
// tenure, whether they follow the channel, and how long twi has seen them.
//
// Anything twi does not actually know is omitted rather than guessed, so an
// absent "follows" note means "no follower data", never "does not follow".
func authorMetaFragments(opts Options) []Fragment {
	meta := opts.Meta
	if meta.empty() {
		return nil
	}
	parts := make([]string, 0, 4)
	if meta.Role != "" {
		parts = append(parts, meta.Role)
	}
	if meta.SubscribedMonths > 0 {
		parts = append(parts, fmt.Sprintf("sub %dmo", meta.SubscribedMonths))
	}
	if meta.FollowKnown {
		if meta.FollowsSince.IsZero() {
			parts = append(parts, "follows")
		} else {
			parts = append(parts, "follows "+humanizeDuration(meta.now().Sub(meta.FollowsSince)))
		}
	}
	if !meta.FirstSeen.IsZero() {
		parts = append(parts, "seen "+humanizeDuration(meta.now().Sub(meta.FirstSeen)))
	}
	if len(parts) == 0 {
		return nil
	}
	return []Fragment{{
		Kind: FragmentText,
		Text: " · " + strings.Join(parts, " · "),
		Style: FragmentStyle{
			Foreground: opts.Palette.Muted,
			Italic:     true,
		},
	}}
}

// humanizeDuration renders an age at one significant unit ("3mo", "5d",
// "2h"). Chat metadata only needs a sense of scale, and a single short unit
// keeps the header from crowding out the message.
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// prefixDecorations records which of the optional parts of a message prefix
// -- the avatar, the timestamp, the badges, the first-message mark -- are
// being drawn for one message.
type prefixDecorations struct {
	avatar       bool
	timestamp    bool
	badges       bool
	firstMessage bool
}

// chooseDecorations decides which decorations a message's prefix can afford.
//
// It starts from what the layout and the message allow, then drops
// decorations until the prefix plus the author's name fits the available
// width. The drop order is deliberate: badges go first because they are the
// widest and the most redundant (a moderator is usually recognisable by name
// to the person reading), then the avatar, then the timestamp, which is the
// one people scan for. If nothing is left to drop, the prefix is allowed to
// overflow rather than losing the author's name.
func chooseDecorations(msg twitch.ChatMessage, opts Options, author string) prefixDecorations {
	compact := opts.layout() == LayoutCompact
	d := prefixDecorations{
		// LayoutCompact trades every decoration for message text.
		timestamp:    !compact && opts.Width >= 16,
		badges:       !compact && opts.Width >= 28 && len(msg.Badges) > 0 && opts.badgeMode() != BadgeModeOff,
		avatar:       !compact && opts.Assets.ShowAvatars && opts.Width >= 24,
		firstMessage: msg.FirstMessage && !compact && opts.Width >= 24,
	}

	for {
		if d.width(msg, opts)+textWidth(author) <= opts.Width {
			return d
		}
		switch {
		case d.badges:
			d.badges = false
		case d.avatar:
			d.avatar = false
		case d.timestamp:
			d.timestamp = false
		default:
			// Nothing left to give up; the name matters more than the fit.
			return d
		}
	}
}

// width is the number of cells the decorations and the fixed punctuation of
// the prefix occupy, excluding the author's name.
func (d prefixDecorations) width(msg twitch.ChatMessage, opts Options) int {
	// The trailing separator is ": ", or " " plus the "* " action marker.
	width := 2
	if msg.Type == twitch.MessageTypeAction {
		width = 3
	}
	if d.firstMessage {
		width += 2
	}
	if d.timestamp {
		width += 6
	}
	if d.badges {
		width += badgeSetWidth(msg.Badges, opts)
	}
	if d.avatar {
		width += opts.Assets.AvatarWidthCells
	}
	return width
}

func messagePrefix(msg twitch.ChatMessage, opts Options) []Fragment {
	foreground := opts.Palette.Foreground
	muted := opts.Palette.Muted
	accent := opts.Palette.Accent

	author := displayAuthor(msg)
	decorations := chooseDecorations(msg, opts, author)

	var fragments []Fragment
	if decorations.avatar {
		fragments = append(fragments, avatarFallbackFragment(msg, opts, author))
	}
	if decorations.timestamp {
		fragments = append(fragments, Fragment{
			Kind: FragmentTimestamp,
			Text: timestampText(msg.Timestamp) + " ",
			Style: FragmentStyle{
				Foreground: muted,
			},
		})
	}
	if decorations.badges {
		fragments = append(fragments, badgeFragments(msg, opts)...)
	}
	// A first-ever message in the channel is marked before the name, where it
	// reads as a property of the person rather than of what they said.
	// Greeting a newcomer is one of the few things a streamer must do while
	// it is still on screen, and Twitch's own tag is the only reliable
	// source: a local roster cannot know about a viewer's first visit.
	if decorations.firstMessage {
		fragments = append(fragments, Fragment{
			Kind: FragmentFirstMessage,
			Text: "✦ ",
			Style: FragmentStyle{
				Foreground: opts.Palette.Success,
				Bold:       true,
			},
		})
	}
	if msg.Type == twitch.MessageTypeAction {
		fragments = append(fragments, Fragment{
			Kind: FragmentAction,
			Text: "* ",
			Style: FragmentStyle{
				Foreground: accent,
				Bold:       true,
			},
		})
	}

	fragments = append(fragments, usernameFragment(msg, opts))

	separator := ": "
	if msg.Type == twitch.MessageTypeAction {
		separator = " "
	}
	fragments = append(fragments, Fragment{
		Kind: FragmentText,
		Text: separator,
		Style: FragmentStyle{
			Foreground: foreground,
		},
	})
	return fragments
}

func messageContent(msg twitch.ChatMessage, opts Options) []Fragment {
	if msg.Deleted {
		return []Fragment{{
			Kind: FragmentDeleted,
			Text: "[message deleted]",
			Style: FragmentStyle{
				Foreground:    opts.Palette.Muted,
				Italic:        true,
				Strikethrough: true,
			},
		}}
	}

	var fragments []Fragment
	if msg.Reply != nil {
		reply := "reply to " + emptyFallback(msg.Reply.ParentAuthor, "unknown")
		if msg.Reply.ParentText != "" {
			reply += ": " + compactWhitespace(msg.Reply.ParentText)
		}
		fragments = append(fragments, Fragment{
			Kind: FragmentReply,
			Text: reply + " ",
			Style: FragmentStyle{
				Foreground: opts.Palette.Muted,
				Italic:     true,
			},
		})
	}

	if msg.Type == twitch.MessageTypeNotice || msg.Type == twitch.MessageTypeSystem {
		fragments = append(fragments, Fragment{
			Kind: FragmentNotice,
			Text: "[notice] ",
			Style: FragmentStyle{
				Foreground: opts.Palette.Warning,
				Bold:       true,
			},
		})
	}

	if len(msg.Fragments) > 0 {
		fragments = append(fragments, normalizedFragments(msg.Fragments, opts)...)
		return fragments
	}
	if len(msg.Emotes) > 0 {
		fragments = append(fragments, emoteFallbackFragments(msg, opts)...)
		return fragments
	}
	fragments = append(fragments, splitTextFragments(msg.Text, opts)...)
	return fragments
}

func normalizedFragments(in []twitch.MessageFragment, opts Options) []Fragment {
	var out []Fragment
	for _, fragment := range in {
		text := fragment.Text
		if text == "" && fragment.Ref.ID != "" {
			text = ":" + fragment.Ref.ID + ":"
		}
		switch fragment.Type {
		case twitch.FragmentMention:
			out = append(out, Fragment{
				Kind: FragmentMention,
				Text: text,
				Style: FragmentStyle{
					Foreground: opts.Palette.Accent,
					Bold:       true,
				},
				Ref: fragment.Ref,
			})
		case twitch.FragmentEmote:
			out = append(out, Fragment{
				Kind:       FragmentEmoteFallback,
				Text:       text,
				WidthCells: opts.Assets.EmoteWidthCells,
				Style: FragmentStyle{
					Foreground: opts.Palette.Success,
					Background: opts.emoteHighlight(),
					Bold:       true,
				},
				Ref: emoteFragmentRef(fragment),
			})
		case twitch.FragmentEmoji:
			out = append(out, Fragment{
				Kind:       FragmentEmojiFallback,
				Text:       text,
				WidthCells: opts.Assets.EmojiWidthCells,
				Style: FragmentStyle{
					Foreground: opts.Palette.Foreground,
					Background: opts.emojiHighlight(),
				},
				Ref: emojiAssetRef(text, fragment.Ref),
			})
		case twitch.FragmentBits:
			out = append(out, Fragment{
				Kind: FragmentText,
				Text: text,
				Style: FragmentStyle{
					Foreground: opts.Palette.Warning,
					Bold:       true,
				},
				Ref: fragment.Ref,
			})
		default:
			out = append(out, splitTextFragments(text, opts)...)
		}
	}
	return coalesceAdjacent(out)
}

func emoteFragmentRef(fragment twitch.MessageFragment) twitch.AssetRef {
	ref := fragment.Ref
	if ref.Kind == "" {
		ref.Kind = "twitch_emote"
	}
	if ref.ID == "" {
		_, id, ok := twitch.StaticEmoteCDNURL(ref.URL)
		if ok {
			ref.ID = id
		}
	}
	return ref
}

func emoteFallbackFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	textRunes := []rune(msg.Text)
	if len(textRunes) == 0 {
		return nil
	}

	emotes := make([]twitch.Emote, len(msg.Emotes))
	copy(emotes, msg.Emotes)
	sort.SliceStable(emotes, func(i, j int) bool {
		if emotes[i].Start == emotes[j].Start {
			return emotes[i].End < emotes[j].End
		}
		return emotes[i].Start < emotes[j].Start
	})

	fragments := make([]Fragment, 0, len(emotes)*2+1)
	cursor := 0
	for _, emote := range emotes {
		start := emote.Start
		end := emote.End
		if start < cursor || start < 0 || end < start || end >= len(textRunes) {
			continue
		}
		if cursor < start {
			fragments = append(fragments, splitTextFragments(string(textRunes[cursor:start]), opts)...)
		}
		token := string(textRunes[start : end+1])
		if token == "" {
			token = emptyFallback(emote.Name, ":"+emote.ID+":")
		}
		fragments = append(fragments, Fragment{
			Kind:       FragmentEmoteFallback,
			Text:       token,
			WidthCells: opts.Assets.EmoteWidthCells,
			Style: FragmentStyle{
				Foreground: opts.Palette.Success,
				Background: opts.emoteHighlight(),
				Bold:       true,
			},
			Ref: emoteAssetRef(emote),
		})
		cursor = end + 1
	}
	if cursor < len(textRunes) {
		fragments = append(fragments, splitTextFragments(string(textRunes[cursor:]), opts)...)
	}
	return coalesceAdjacent(fragments)
}

func splitTextFragments(text string, opts Options) []Fragment {
	if text == "" {
		return nil
	}

	var fragments []Fragment
	var textBuffer strings.Builder
	flushText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		fragments = append(fragments, Fragment{
			Kind: FragmentText,
			Text: textBuffer.String(),
			Style: FragmentStyle{
				Foreground: opts.Palette.Foreground,
			},
		})
		textBuffer.Reset()
	}

	graphemes := graphemeStrings(text)
	for i := 0; i < len(graphemes); {
		cluster := graphemes[i]
		if cluster == "@" && i+1 < len(graphemes) && isMentionPart(graphemes[i+1]) {
			flushText()
			start := i
			i += 2
			for i < len(graphemes) && isMentionPart(graphemes[i]) {
				i++
			}
			fragments = append(fragments, Fragment{
				Kind: FragmentMention,
				Text: strings.Join(graphemes[start:i], ""),
				Style: FragmentStyle{
					Foreground: opts.Palette.Accent,
					Bold:       true,
				},
			})
			continue
		}
		if emoji.IsCluster(cluster) {
			flushText()
			fragments = append(fragments, Fragment{
				Kind:       FragmentEmojiFallback,
				Text:       cluster,
				WidthCells: opts.Assets.EmojiWidthCells,
				Style: FragmentStyle{
					Foreground: opts.Palette.Foreground,
					Background: opts.emojiHighlight(),
				},
				Ref: emojiAssetRef(cluster, twitch.AssetRef{}),
			})
			i++
			continue
		}
		textBuffer.WriteString(cluster)
		i++
	}
	flushText()
	return coalesceAdjacent(fragments)
}

func wrap(prefix, content []Fragment, width int) []Row {
	if width <= 0 {
		return nil
	}

	prefixWidth := fragmentsWidth(prefix)
	indentWidth := prefixWidth
	if indentWidth >= width {
		indentWidth = width / 2
	}

	rows := make([]Row, 0, 2)
	current := Row{}
	used := 0
	rows, current, used = appendWrappedFragments(rows, current, used, prefix, width, 0)
	// The final width is not needed: nothing is appended after content, so
	// the trailing row is emitted as-is.
	rows, current, _ = appendWrappedFragments(rows, current, used, content, width, indentWidth)
	rows = append(rows, current)
	return rows
}

func appendWrappedFragments(rows []Row, current Row, used int, fragments []Fragment, width, indentWidth int) ([]Row, Row, int) {
	for _, fragment := range fragments {
		if fragment.WidthCells > 0 || isAtomicFragment(fragment) {
			fragmentWidth := fragment.Width()
			if fragmentWidth == 0 {
				continue
			}
			if used+fragmentWidth > width && used > indentWidth {
				rows = append(rows, current)
				current = continuationRow(indentWidth)
				used = indentWidth
			}
			if used+fragmentWidth > width && used == indentWidth && used > 0 && fragmentWidth <= width {
				// The fragment cannot fit beside the indent but would fit on
				// a full-width row, so give up the indent.
				//
				// The row being abandoned must be emitted first if it holds
				// anything real. On the content pass, indentWidth is exactly
				// the prefix width, so `used == indentWidth` is also true on
				// the very first row -- where `current` holds the timestamp,
				// badges and author name. Discarding it unconditionally
				// dropped the whole prefix whenever a message opened with a
				// long mention or emote, leaving an unattributed line in
				// chat at ordinary terminal widths.
				if rowHasContent(current) {
					rows = append(rows, current)
				}
				current = Row{}
				used = 0
			}
			if used+fragmentWidth <= width {
				appendFragment(&current, fragment)
				used += fragmentWidth
				continue
			}
		}

		// Prefer a break between words. Chat is prose, and breaking mid-word
		// makes it materially harder to read at the speed a busy channel
		// moves. A word wider than the line still falls through to
		// cluster-by-cluster wrapping below, so nothing becomes unrenderable.
		for _, chunk := range wrapChunks(fragment.Text) {
			chunkWidth := textWidth(chunk)
			if chunkWidth > 0 && chunkWidth <= width-indentWidth &&
				used+chunkWidth > width && used > indentWidth &&
				strings.TrimSpace(chunk) != "" {
				rows = append(rows, current)
				current = continuationRow(indentWidth)
				used = indentWidth
			}
			rows, current, used = appendWrappedClusters(rows, current, used, fragment, chunk, width, indentWidth)
		}
	}
	return rows, current, used
}

// wrapChunks splits text into word-sized pieces, each a run of non-space
// characters together with the spaces that follow it. Keeping the trailing
// spaces attached means a break taken before a chunk lands between words
// rather than stranding a space at the start of the next row.
func wrapChunks(text string) []string {
	if text == "" {
		return nil
	}
	var chunks []string
	var current strings.Builder
	inTrailingSpace := false
	for _, cluster := range graphemeStrings(text) {
		if cluster == "\n" {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, cluster)
			inTrailingSpace = false
			continue
		}
		isSpace := strings.TrimSpace(cluster) == ""
		if !isSpace && inTrailingSpace {
			chunks = append(chunks, current.String())
			current.Reset()
			inTrailingSpace = false
		}
		current.WriteString(cluster)
		if isSpace {
			inTrailingSpace = true
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func appendWrappedClusters(rows []Row, current Row, used int, fragment Fragment, text string, width, indentWidth int) ([]Row, Row, int) {
	{
		for _, cluster := range graphemeStrings(text) {
			if cluster == "\n" {
				rows = append(rows, current)
				current = continuationRow(indentWidth)
				used = indentWidth
				continue
			}

			clusterWidth := textWidth(cluster)
			if used+clusterWidth > width && used > indentWidth {
				rows = append(rows, current)
				current = continuationRow(indentWidth)
				used = indentWidth
				if strings.TrimSpace(cluster) == "" {
					continue
				}
			}
			if used+clusterWidth > width && used == indentWidth && used > 0 {
				rows = append(rows, current)
				current = continuationRow(0)
				used = 0
			}

			next := fragment
			next.Text = cluster
			next.WidthCells = 0
			appendFragment(&current, next)
			used += clusterWidth
		}
	}
	return rows, current, used
}

func isAtomicFragment(fragment Fragment) bool {
	switch fragment.Kind {
	case FragmentMention, FragmentEmojiFallback, FragmentEmoteFallback:
		return true
	default:
		return false
	}
}

func continuationRow(indentWidth int) Row {
	if indentWidth <= 0 {
		return Row{}
	}
	return Row{Fragments: []Fragment{{
		Kind: FragmentText,
		Text: strings.Repeat(" ", indentWidth),
	}}}
}

func appendFragment(row *Row, fragment Fragment) {
	if fragment.Text == "" {
		return
	}
	lastIndex := len(row.Fragments) - 1
	if lastIndex >= 0 && sameFragmentStyle(row.Fragments[lastIndex], fragment) {
		row.Fragments[lastIndex].Text += fragment.Text
		return
	}
	row.Fragments = append(row.Fragments, fragment)
}

func coalesceAdjacent(in []Fragment) []Fragment {
	if len(in) == 0 {
		return nil
	}
	out := make([]Fragment, 0, len(in))
	for _, fragment := range in {
		row := Row{Fragments: out}
		appendFragment(&row, fragment)
		out = row.Fragments
	}
	return out
}

func sameFragmentStyle(a, b Fragment) bool {
	if a.WidthCells > 0 || b.WidthCells > 0 {
		return false
	}
	return a.Kind == b.Kind &&
		a.Style == b.Style &&
		a.Ref == b.Ref &&
		a.WidthCells == b.WidthCells
}

func renderFragment(fragment Fragment) string {
	style := lipgloss.NewStyle()
	if fragment.Style.Foreground != "" {
		style = style.Foreground(lipgloss.Color(fragment.Style.Foreground))
	}
	if fragment.Style.Background != "" {
		style = style.Background(lipgloss.Color(fragment.Style.Background))
	}
	if fragment.Style.Bold {
		style = style.Bold(true)
	}
	if fragment.Style.Italic {
		style = style.Italic(true)
	}
	if fragment.Style.Strikethrough {
		style = style.Strikethrough(true)
	}
	return style.Render(fragmentFallbackText(fragment))
}

func usernameColor(msg twitch.ChatMessage, palette theme.Palette) string {
	identity := msg.AuthorLogin
	if identity == "" {
		identity = msg.DisplayName
	}
	if identity == "" {
		identity = msg.AuthorID
	}
	return theme.IdentityColor(identity, []string{palette.Background, palette.Surface}, palette.Foreground)
}

func displayAuthor(msg twitch.ChatMessage) string {
	if msg.DisplayName != "" {
		return msg.DisplayName
	}
	if msg.AuthorLogin != "" {
		return msg.AuthorLogin
	}
	if msg.Type == twitch.MessageTypeNotice {
		return "notice"
	}
	if msg.Type == twitch.MessageTypeSystem {
		return "system"
	}
	return "unknown"
}

func avatarFallbackFragment(msg twitch.ChatMessage, opts Options, author string) Fragment {
	ref := twitch.AssetRef{
		Kind: "avatar",
		ID:   msg.AuthorID,
		URL:  msg.AvatarURL,
	}
	if ref.ID == "" {
		ref.ID = msg.AuthorLogin
	}
	if ref.ID == "" {
		ref.ID = author
	}
	return Fragment{
		Kind:       FragmentAvatar,
		Text:       avatarFallbackText(msg, author),
		WidthCells: opts.Assets.AvatarWidthCells,
		Style: FragmentStyle{
			Foreground: opts.Palette.Background,
			Background: usernameColor(msg, opts.Palette),
			Bold:       true,
		},
		Ref: ref,
	}
}

func avatarFallbackText(msg twitch.ChatMessage, author string) string {
	source := author
	if source == "" {
		source = displayAuthor(msg)
	}
	initials := initials(source)
	if initials == "" {
		initials = "?"
	}
	return "[" + initials + "]"
}

func initials(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	words := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || unicode.IsSpace(r)
	})
	if len(words) == 0 {
		words = []string{value}
	}
	var builder strings.Builder
	for _, word := range words {
		if word == "" {
			continue
		}
		for _, cluster := range graphemeStrings(word) {
			builder.WriteString(strings.ToUpper(cluster))
			break
		}
		if textWidth(builder.String()) >= 2 {
			break
		}
	}
	return takeCells(builder.String(), 2)
}

func timestampText(timestamp time.Time) string {
	if timestamp.IsZero() {
		return "--:--"
	}
	return timestamp.Local().Format("15:04")
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

func compactBadgeLabel(badge twitch.Badge) string {
	name := badge.SetID
	switch strings.ToLower(name) {
	case "broadcaster":
		name = "cast"
	case "moderator":
		name = "mod"
	case "subscriber":
		name = "sub"
	case "vip":
		name = "vip"
	case "founder":
		name = "found"
	case "":
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

func emoteAssetRef(emote twitch.Emote) twitch.AssetRef {
	ref := emote.Ref
	if ref.Kind == "" {
		ref.Kind = "twitch_emote"
	}
	if ref.ID == "" {
		ref.ID = emote.ID
	}
	return ref
}

func emojiAssetRef(text string, ref twitch.AssetRef) twitch.AssetRef {
	if ref.Kind == "" {
		ref.Kind = "emoji"
	}
	if ref.ID == "" || ref.ID == text {
		if id, ok := emoji.AssetID(text); ok {
			ref.ID = id
		} else if ref.ID == "" {
			ref.ID = text
		}
	}
	return ref
}

// fragmentFallbackText is the single funnel every visible fragment passes
// through on its way to the terminal, whatever built it -- a chat message, a
// Helix stream title, a badge label -- so it is where the escape-sequence
// strip belongs, in addition to the one at IRC ingestion. Stripping before
// the width fit also keeps the cell arithmetic honest: it measures the text
// that is actually printed.
//
// textsafe.Display returns the string untouched when there is nothing to
// strip, which is the case for every ordinary message.
func fragmentFallbackText(fragment Fragment) string {
	text := textsafe.Display(fragment.Text)
	if fragment.WidthCells <= 0 {
		return text
	}
	return fitCells(text, fragment.WidthCells)
}

func fitCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	out := value
	if textWidth(out) > width {
		out = truncateCells(out, width)
	}
	used := textWidth(out)
	if used < width {
		out += strings.Repeat(" ", width-used)
	}
	return out
}

func truncateCells(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if textWidth(value) <= limit {
		return value
	}
	if limit <= 3 {
		return takeCells(value, limit)
	}
	return takeCells(value, limit-3) + "..."
}

func takeCells(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	for _, cluster := range graphemeStrings(value) {
		width := textWidth(cluster)
		if used+width > limit {
			break
		}
		builder.WriteString(cluster)
		used += width
	}
	return builder.String()
}

func graphemeStrings(value string) []string {
	graphemes := uniseg.NewGraphemes(value)
	out := make([]string, 0, len(value))
	for graphemes.Next() {
		out = append(out, graphemes.Str())
	}
	return out
}

func fragmentsWidth(fragments []Fragment) int {
	width := 0
	for _, fragment := range fragments {
		width += fragment.Width()
	}
	return width
}

func textWidth(value string) int {
	return uniseg.StringWidth(value)
}

func isMentionPart(cluster string) bool {
	for _, r := range cluster {
		return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	return false
}

func emptyFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// rowHasContent reports whether a row holds anything other than indent
// padding, so an abandoned continuation row can be discarded while a row
// carrying real fragments is emitted.
func rowHasContent(row Row) bool {
	for _, fragment := range row.Fragments {
		if strings.TrimSpace(fragment.Text) != "" {
			return true
		}
	}
	return false
}
