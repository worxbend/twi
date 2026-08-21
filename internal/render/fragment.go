package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
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

// Atomic reports whether a fragment has to survive wrapping in one piece.
// A mention, an emote, or an emoji is a single object to the reader, so the
// wrapper moves it to the next row rather than splitting it down the middle.
//
// internal/animation applies a broader rule to reveal animations; see
// animation's own isAtomic for why the two are not the same predicate.
func (f Fragment) Atomic() bool {
	switch f.Kind {
	case FragmentMention, FragmentEmojiFallback, FragmentEmoteFallback:
		return true
	default:
		return false
	}
}

// MergesWith reports whether two neighboring fragments can be drawn as one
// run of text, which is true when they are the same kind, styled the same way,
// and point at the same asset.
//
// A fragment with a reserved width never merges: WidthCells is a per-fragment
// promise about how many cells it occupies, and a merged pair would keep only
// one of the two promises.
func (f Fragment) MergesWith(other Fragment) bool {
	if f.WidthCells > 0 || other.WidthCells > 0 {
		return false
	}
	return f.Kind == other.Kind &&
		f.Style == other.Style &&
		f.Ref == other.Ref &&
		f.WidthCells == other.WidthCells
}

// Row is a width-bounded collection of render fragments.
type Row struct {
	Fragments []Fragment
}

// Append adds a fragment to the end of the row, folding it into the previous
// fragment when the two would render identically (see Fragment.MergesWith).
// Folding matters because callers build rows one grapheme cluster at a time:
// without it a forty-character word would emit forty separate terminal escape
// sequences. Fragments with no text are dropped.
//
// It is also what internal/animation uses to rebuild a partially revealed row,
// so a half-typed message and the finished one coalesce by the same rule.
func (r *Row) Append(fragment Fragment) {
	if fragment.Text == "" {
		return
	}
	last := len(r.Fragments) - 1
	if last >= 0 && r.Fragments[last].MergesWith(fragment) {
		r.Fragments[last].Text += fragment.Text
		return
	}
	r.Fragments = append(r.Fragments, fragment)
}

// Plain returns the row fallback text without terminal styling.
func (r Row) Plain() string {
	var builder strings.Builder
	for _, fragment := range r.Fragments {
		builder.WriteString(fragmentFallbackText(fragment))
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

// fragmentWithDefaultBackground fills in a fragment's background only when it
// has none of its own, leaving deliberately colored fragments such as the
// avatar chip untouched.
func fragmentWithDefaultBackground(fragment Fragment, background string) Fragment {
	if fragment.Style.Background == "" {
		fragment.Style.Background = background
	}
	return fragment
}

// coalesceAdjacent merges neighboring fragments that would render identically,
// by the same rule Row.Append uses. Message text is built one grapheme cluster
// or one token at a time, so a plain sentence arrives as dozens of fragments
// and leaves as one.
func coalesceAdjacent(in []Fragment) []Fragment {
	if len(in) == 0 {
		return nil
	}
	out := make([]Fragment, 0, len(in))
	for _, fragment := range in {
		row := Row{Fragments: out}
		row.Append(fragment)
		out = row.Fragments
	}
	return out
}

// renderFragment turns one fragment into the escape-sequence-wrapped string a
// terminal draws, applying its colors and text attributes.
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
