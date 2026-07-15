package render

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/emoji"
	"github.com/worxbend/twi/internal/storage"
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
	ImageCell  ImageCell
	ImageReady bool
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

// TerminalString returns the row with prepared image cells substituted for
// matching image fragments. Fragments without prepared cells use styled text
// fallbacks so callers can render stable rows before assets are ready.
func (r Row) TerminalString() string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		if fragment.ImageReady {
			builder.WriteString(fragment.ImageCell.Text)
			continue
		}
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
		if fragment.ImageReady {
			builder.WriteString(fragment.ImageCell.Text)
			continue
		}
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

// Options controls message rendering and wrapping.
type Options struct {
	Width   int
	Palette theme.Palette
	Assets  AssetOptions
}

// DefaultOptions returns renderer options using the default theme palette.
func DefaultOptions(width int) Options {
	return Options{
		Width:   width,
		Palette: theme.DefaultPalette(),
	}
}

// ImageSpec describes a fixed-cell inline image request. It is intended for
// asynchronous callers; View paths should render the fallback fragments already
// produced by this package.
type ImageSpec struct {
	Ref         twitch.AssetRef
	Channel     string
	ChannelID   string
	WidthCells  int
	HeightCells int
	Fallback    string
}

// ImageCell is the terminal output returned by an image renderer.
type ImageCell struct {
	Text       string
	WidthCells int
}

// ImageRenderer is the minimal terminal image rendering boundary. Callers are
// expected to invoke it from Bubble Tea commands or other asynchronous flows,
// not from View methods.
type ImageRenderer interface {
	RenderImage(ctx context.Context, asset storage.AssetRecord, spec ImageSpec) (ImageCell, error)
}

// ImagePreparer normalizes a downloaded image asset into a renderer-ready
// local record. It must be called from asynchronous paths, not View methods.
type ImagePreparer interface {
	PrepareImage(ctx context.Context, asset storage.AssetRecord, spec ImageSpec) (storage.AssetRecord, error)
}

// ImageCellKey identifies a prepared terminal image cell by stable asset
// identity. URLs are intentionally excluded so credential-bearing request data
// cannot become part of chat row state.
type ImageCellKey struct {
	Kind            string
	ID              string
	ChannelIdentity string
}

// ImageCellKeyForRef returns the row-generation key for an asset ref.
func ImageCellKeyForRef(ref twitch.AssetRef) (ImageCellKey, bool) {
	kind := strings.TrimSpace(ref.Kind)
	id := strings.TrimSpace(ref.ID)
	if kind == "" || id == "" {
		return ImageCellKey{}, false
	}
	if containsUnsafeImageIdentity(kind) || containsUnsafeImageIdentity(id) ||
		looksLikeImageIdentityPath(kind) || looksLikeImageIdentityPath(id) {
		return ImageCellKey{}, false
	}
	return ImageCellKey{Kind: kind, ID: id}, true
}

// ImageCellKeyForRefInChannel returns a prepared-cell key scoped to a safe
// channel identity when room or channel context is available.
func ImageCellKeyForRefInChannel(ref twitch.AssetRef, channelID, channel string) (ImageCellKey, bool) {
	key, ok := ImageCellKeyForRef(ref)
	if !ok {
		return ImageCellKey{}, false
	}
	identity, scoped, safe := ImageCellChannelIdentity(channelID, channel)
	if !safe {
		return ImageCellKey{}, false
	}
	if scoped {
		key.ChannelIdentity = identity
	}
	return key, true
}

// ImageCellChannelIdentity returns the safe render-only channel identity used
// for prepared image cells. Room IDs are preferred over channel names.
func ImageCellChannelIdentity(channelID, channel string) (identity string, scoped bool, safe bool) {
	hasContext := strings.TrimSpace(channelID) != "" || strings.TrimSpace(channel) != ""
	if id := strings.TrimSpace(channelID); id != "" && safeImageChannelToken(id) {
		return "room:" + strings.ToLower(id), true, true
	}
	if name := normalizeImageChannelName(channel); name != "" && safeImageChannelToken(name) {
		return "channel:" + name, true, true
	}
	if hasContext {
		return "", false, false
	}
	return "", false, true
}

func containsUnsafeImageIdentity(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") {
		return true
	}
	markers := []string{
		"oauth:",
		"oauth_token=",
		"access_token=",
		"refresh_token=",
		"client_secret=",
		"client-secret=",
		"authorization=",
		"authorization: bearer",
		"bearer ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func normalizeImageChannelName(channel string) string {
	channel = strings.TrimSpace(channel)
	channel = strings.TrimPrefix(channel, "#")
	return strings.ToLower(channel)
}

func safeImageChannelToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || containsUnsafeImageIdentity(value) || looksLikeImageIdentityPath(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func looksLikeImageIdentityPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "./") || value == "." || value == ".." {
		return true
	}
	if strings.Contains(value, "\\") {
		return true
	}
	if len(value) >= 3 && value[1] == ':' && ((value[2] == '/') || (value[2] == '\\')) && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return true
	}
	if strings.HasPrefix(value, "../") || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") {
		return true
	}
	if strings.Contains(value, "/") {
		last := value[strings.LastIndex(value, "/")+1:]
		for _, suffix := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".bin", ".tmp"} {
			if strings.HasSuffix(lower, suffix) || strings.HasSuffix(strings.ToLower(last), suffix) {
				return true
			}
		}
	}
	return false
}

// AssetOptions controls no-image fallbacks and fixed placeholder widths.
type AssetOptions struct {
	ShowAvatars      bool
	AvatarWidthCells int
	BadgeWidthCells  int
	EmoteWidthCells  int
	EmojiWidthCells  int
	ImageCells       map[ImageCellKey]ImageCell
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

	prefix := messagePrefix(msg, opts)
	content := messageContent(msg, opts)
	prefix = attachPreparedImageCells(prefix, opts.Assets, msg.ChannelID, msg.Channel)
	content = attachPreparedImageCells(content, opts.Assets, msg.ChannelID, msg.Channel)
	rows := wrap(prefix, content, opts.Width)
	if len(rows) == 0 {
		return []Row{{Fragments: prefix}}
	}
	return rows
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

func messagePrefix(msg twitch.ChatMessage, opts Options) []Fragment {
	foreground := opts.Palette.Foreground
	muted := opts.Palette.Muted
	accent := opts.Palette.Accent
	authorColor := usernameColor(msg, opts.Palette)

	author := displayAuthor(msg)
	avatarAuthor := author
	includeTimestamp := opts.Width >= 16
	includeBadges := opts.Width >= 28 && len(msg.Badges) > 0
	includeAvatar := opts.Assets.ShowAvatars && opts.Width >= 24

	for {
		fixedWidth := 2
		if msg.Type == twitch.MessageTypeAction {
			fixedWidth = 3
		}
		if includeTimestamp {
			fixedWidth += 6
		}
		if includeBadges {
			for _, badge := range msg.Badges {
				fixedWidth += badgeFallbackWidth(badge, opts.Assets)
			}
		}
		if includeAvatar {
			fixedWidth += opts.Assets.AvatarWidthCells
		}

		if fixedWidth+textWidth(author) <= opts.Width || (!includeAvatar && !includeBadges && !includeTimestamp) {
			break
		}
		if includeBadges {
			includeBadges = false
			continue
		}
		if includeAvatar {
			includeAvatar = false
			continue
		}
		includeTimestamp = false
	}

	var fragments []Fragment
	if includeAvatar {
		fragments = append(fragments, avatarFallbackFragment(msg, opts, avatarAuthor))
	}
	if includeTimestamp {
		fragments = append(fragments, Fragment{
			Kind: FragmentTimestamp,
			Text: timestampText(msg.Timestamp) + " ",
			Style: FragmentStyle{
				Foreground: muted,
			},
		})
	}
	if includeBadges {
		for _, badge := range msg.Badges {
			fragments = append(fragments, badgeFallbackFragment(badge, opts))
		}
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

	fragments = append(fragments, Fragment{
		Kind: FragmentUsername,
		Text: author,
		Style: FragmentStyle{
			Foreground: authorColor,
			Bold:       true,
		},
	})

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
	rows, current, used = appendWrappedFragments(rows, current, used, content, width, indentWidth)
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
				current = Row{}
				used = 0
			}
			if used+fragmentWidth <= width {
				appendFragment(&current, fragment)
				used += fragmentWidth
				continue
			}
		}

		for _, cluster := range graphemeStrings(fragment.Text) {
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

func attachPreparedImageCells(fragments []Fragment, opts AssetOptions, channelID, channel string) []Fragment {
	if len(fragments) == 0 || len(opts.ImageCells) == 0 {
		return fragments
	}
	out := make([]Fragment, len(fragments))
	copy(out, fragments)
	for i := range out {
		key, ok := ImageCellKeyForRefInChannel(out[i].Ref, channelID, channel)
		if !ok {
			continue
		}
		cell, ok := opts.ImageCells[key]
		if !ok || cell.Text == "" || cell.WidthCells != out[i].Width() {
			continue
		}
		out[i].ImageCell = cell
		out[i].ImageReady = true
	}
	return out
}

func usernameColor(msg twitch.ChatMessage, palette theme.Palette) string {
	if msg.AuthorColor == "" {
		return palette.Accent
	}
	return theme.ContrastCorrectedForeground(msg.AuthorColor, palette.Background, palette.Foreground)
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

func fragmentFallbackText(fragment Fragment) string {
	if fragment.WidthCells <= 0 {
		return fragment.Text
	}
	return fitCells(fragment.Text, fragment.WidthCells)
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

func cloneFragments(in []Fragment) []Fragment {
	out := make([]Fragment, len(in))
	copy(out, in)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
