package render

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/worxbend/twi/internal/emoji"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultWidth       = 80
	minimumRenderWidth = 8
)

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

// The fixed punctuation of a message prefix. These are the exact strings
// messagePrefix draws, and prefixDecorations.width measures them to budget the
// prefix, so the space reserved for the punctuation cannot drift from the
// punctuation that ends up on screen.
const (
	// firstMessageMark precedes the author's name on a viewer's first-ever
	// message in the channel.
	firstMessageMark = "✦ "
	// messageSeparator sits between the author's name and what they said.
	messageSeparator = ": "
	// actionSeparator replaces messageSeparator for a /me action, where the
	// name reads as the subject of a sentence rather than as a label.
	actionSeparator = " "
	// actionMark precedes the author's name on a /me action.
	actionMark = "* "
)

// timestampWidth is the width of a drawn clock plus the space after it. It is
// measured from the renderer's own formatter rather than written out as a
// number, so changing the clock format keeps the prefix budget correct.
var timestampWidth = textWidth(timestampText(time.Time{}) + " ")

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
		timestamp:    !compact && opts.Width >= inlineMinWidthForTimestamp,
		badges:       !compact && opts.Width >= inlineMinWidthForBadges && len(msg.Badges) > 0 && opts.badgeMode() != BadgeModeOff,
		avatar:       !compact && opts.Assets.ShowAvatars && opts.Width >= inlineMinWidthForAvatar,
		firstMessage: msg.FirstMessage && !compact && opts.Width >= inlineMinWidthForFirstMessage,
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
	width := textWidth(messageSeparator)
	if msg.Type == twitch.MessageTypeAction {
		width = textWidth(actionMark) + textWidth(actionSeparator)
	}
	if d.firstMessage {
		width += textWidth(firstMessageMark)
	}
	if d.timestamp {
		width += timestampWidth
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
			Text: firstMessageMark,
			Style: FragmentStyle{
				Foreground: opts.Palette.Success,
				Bold:       true,
			},
		})
	}
	if msg.Type == twitch.MessageTypeAction {
		fragments = append(fragments, Fragment{
			Kind: FragmentAction,
			Text: actionMark,
			Style: FragmentStyle{
				Foreground: accent,
				Bold:       true,
			},
		})
	}

	fragments = append(fragments, usernameFragment(msg, opts))

	separator := messageSeparator
	if msg.Type == twitch.MessageTypeAction {
		separator = actionSeparator
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

// messageContent builds everything that follows the prefix: the optional
// reply and notice lead-ins, then the message body itself.
func messageContent(msg twitch.ChatMessage, opts Options) []Fragment {
	if msg.Deleted {
		return []Fragment{deletedFragment(opts)}
	}

	var fragments []Fragment
	fragments = append(fragments, replyFragments(msg, opts)...)
	fragments = append(fragments, noticeFragments(msg, opts)...)
	fragments = append(fragments, bodyFragments(msg, opts)...)
	return fragments
}

// deletedFragment stands in for a message a moderator removed. The text is
// kept in place, struck through and muted, rather than erased: a reader who
// saw the original needs to know that what they read was taken down.
func deletedFragment(opts Options) Fragment {
	return Fragment{
		Kind: FragmentDeleted,
		Text: "[message deleted]",
		Style: FragmentStyle{
			Foreground:    opts.Palette.Muted,
			Italic:        true,
			Strikethrough: true,
		},
	}
}

// replyFragments renders the "reply to <author>: <quoted text>" lead-in that
// precedes a threaded reply, or nothing when the message is not a reply.
func replyFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	if msg.Reply == nil {
		return nil
	}
	reply := "reply to " + emptyFallback(msg.Reply.ParentAuthor, "unknown")
	if msg.Reply.ParentText != "" {
		reply += ": " + compactWhitespace(msg.Reply.ParentText)
	}
	return []Fragment{{
		Kind: FragmentReply,
		Text: reply + " ",
		Style: FragmentStyle{
			Foreground: opts.Palette.Muted,
			Italic:     true,
		},
	}}
}

// noticeFragments marks server notices and twi's own status lines, so a line
// the client wrote is never mistaken for something a viewer said.
func noticeFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	if msg.Type != twitch.MessageTypeNotice && msg.Type != twitch.MessageTypeSystem {
		return nil
	}
	return []Fragment{{
		Kind: FragmentNotice,
		Text: "[notice] ",
		Style: FragmentStyle{
			Foreground: opts.Palette.Warning,
			Bold:       true,
		},
	}}
}

// bodyFragments renders what the author actually typed, from the richest
// description of it the message carries.
//
// Twitch describes a message's emotes in one of two ways depending on where it
// came from: the EventSub/Helix path hands over pre-split fragments, while the
// IRC path hands over the raw text plus index ranges naming which parts of it
// are emotes. Messages from neither path (twi's own status lines, for example)
// have only text, which is scanned for mentions and emoji.
func bodyFragments(msg twitch.ChatMessage, opts Options) []Fragment {
	if len(msg.Fragments) > 0 {
		return normalizedFragments(msg.Fragments, opts)
	}
	if len(msg.Emotes) > 0 {
		return emoteFallbackFragments(msg, opts)
	}
	return splitTextFragments(msg.Text, opts)
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
			out = append(out, emoteFragment(text, emoteFragmentRef(fragment), opts))
		case twitch.FragmentEmoji:
			out = append(out, emojiFragment(text, emojiAssetRef(text, fragment.Ref), opts))
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

// emoteFragment builds the text stand-in for one channel emote. Emotes are
// never drawn as images, so the token keeps its own text but reserves a fixed
// number of cells (Options.Assets.EmoteWidthCells): every emote then occupies
// the same width, which stops a row of emotes from reflowing the text around
// it. ref is the already-resolved asset reference for the emote; the two call
// sites resolve it differently (rich message fragments carry one, the legacy
// emote ranges do not) and hand in the result.
func emoteFragment(text string, ref twitch.AssetRef, opts Options) Fragment {
	return Fragment{
		Kind:       FragmentEmoteFallback,
		Text:       text,
		WidthCells: opts.Assets.EmoteWidthCells,
		Style: FragmentStyle{
			Foreground: opts.Palette.Success,
			Background: opts.emoteHighlight(),
			Bold:       true,
		},
		Ref: ref,
	}
}

// emojiFragment builds the stand-in for one emoji grapheme cluster. It mirrors
// emoteFragment but uses the emoji width and highlight, so emoji and channel
// emotes stay visually distinguishable. cluster is a single grapheme cluster
// (a flag or a skin-toned face is several codepoints but one cluster).
func emojiFragment(cluster string, ref twitch.AssetRef, opts Options) Fragment {
	return Fragment{
		Kind:       FragmentEmojiFallback,
		Text:       cluster,
		WidthCells: opts.Assets.EmojiWidthCells,
		Style: FragmentStyle{
			Foreground: opts.Palette.Foreground,
			Background: opts.emojiHighlight(),
		},
		Ref: ref,
	}
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
		fragments = append(fragments, emoteFragment(token, emoteAssetRef(emote), opts))
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
			fragments = append(fragments, emojiFragment(cluster, emojiAssetRef(cluster, twitch.AssetRef{}), opts))
			i++
			continue
		}
		textBuffer.WriteString(cluster)
		i++
	}
	flushText()
	return coalesceAdjacent(fragments)
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
