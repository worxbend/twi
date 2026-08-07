package app

import (
	"strings"
	"unicode"

	"github.com/worxbend/twi/internal/render"
)

// roleGlyph maps a roster role label onto the same icon the message renderer
// uses for that badge, so a user reads identically in the suggestion strip
// and in chat.
func roleGlyph(role string) string {
	var setID string
	switch role {
	case "broadcaster":
		setID = "broadcaster"
	case "staff":
		setID = "staff"
	case "mod":
		setID = "moderator"
	case "vip":
		setID = "vip"
	case "sub":
		setID = "subscriber"
	default:
		return ""
	}
	glyph, _ := render.BadgeGlyph(setID)
	return glyph
}

// mentionSuggestionLimit bounds how many completions are offered at once. The
// suggestion strip is one composer row, so more than this cannot be shown
// without stealing space from the draft.
const mentionSuggestionLimit = 6

// mentionAutocompleteState tracks the @mention completion popup for the
// active composer draft.
//
// The candidate list itself is never stored: it is derived from the draft and
// the channel roster on every render, so it stays correct as new people talk
// without any invalidation logic. Only the user's own choices - which
// candidate is selected, and whether they dismissed the strip - live here.
type mentionAutocompleteState struct {
	selected int
	// dismissedFor is the prefix the user pressed esc on. The strip stays
	// hidden until the prefix changes, so esc means "not for this word"
	// rather than "never again".
	dismissedFor string
	dismissed    bool
}

// composerMentionPrefix returns the @mention word the caret sits in, without
// its leading "@".
//
// The composer is append-only (the caret is always at the end - see
// tailDisplayCells), so this only has to inspect the trailing word. The "@"
// must start a word, so an email-like "a@b" never triggers completion.
func composerMentionPrefix(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	index := strings.LastIndex(text, "@")
	if index < 0 {
		return "", false
	}
	if index > 0 {
		previous := []rune(text[:index])
		if last := previous[len(previous)-1]; !unicode.IsSpace(last) {
			return "", false
		}
	}
	prefix := text[index+1:]
	for _, r := range prefix {
		if !isMentionRune(r) {
			return "", false
		}
	}
	return prefix, true
}

func isMentionRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// mentionSuggestions returns the completions offered for the current draft,
// or nil when the strip should be hidden.
func (m shellModel) mentionSuggestions() []*chatterEntry {
	if !m.composerFocused() {
		return nil
	}
	state := m.activeChannelState()
	if state == nil {
		return nil
	}
	prefix, ok := composerMentionPrefix(state.composerText)
	if !ok {
		return nil
	}
	if m.mentionAutocomplete.dismissed && m.mentionAutocomplete.dismissedFor == prefix {
		return nil
	}
	matches := state.roster.completions(prefix, mentionSuggestionLimit)
	// Offering a single candidate that already equals what was typed is
	// noise - there is nothing left to complete.
	if len(matches) == 1 && strings.EqualFold(matches[0].Login, prefix) {
		return nil
	}
	return matches
}

// mentionSelectedIndex clamps the stored selection against the live candidate
// list, which can shrink as the user keeps typing.
func (m shellModel) mentionSelectedIndex(matches []*chatterEntry) int {
	if len(matches) == 0 {
		return 0
	}
	selected := m.mentionAutocomplete.selected
	if selected < 0 || selected >= len(matches) {
		return 0
	}
	return selected
}

func (m *shellModel) moveMentionSelection(delta int) {
	matches := m.mentionSuggestions()
	if len(matches) == 0 {
		return
	}
	selected := m.mentionSelectedIndex(matches) + delta
	if selected < 0 {
		selected = len(matches) - 1
	}
	if selected >= len(matches) {
		selected = 0
	}
	m.mentionAutocomplete.selected = selected
}

// acceptMentionSuggestion replaces the trailing @prefix with the selected
// chatter's name and a trailing space, so the user can keep typing.
func (m *shellModel) acceptMentionSuggestion() bool {
	matches := m.mentionSuggestions()
	if len(matches) == 0 {
		return false
	}
	entry := matches[m.mentionSelectedIndex(matches)]
	state := m.activeChannelState()
	index := strings.LastIndex(state.composerText, "@")
	if index < 0 {
		return false
	}
	state.composerText = state.composerText[:index] + "@" + entry.name() + " "
	m.mentionAutocomplete = mentionAutocompleteState{}
	return true
}

// dismissMentionSuggestions hides the strip for the current word only.
func (m *shellModel) dismissMentionSuggestions() bool {
	prefix, ok := composerMentionPrefix(m.activeChannelState().composerText)
	if !ok {
		return false
	}
	if len(m.mentionSuggestions()) == 0 {
		return false
	}
	m.mentionAutocomplete = mentionAutocompleteState{
		dismissedFor: prefix,
		dismissed:    true,
	}
	return true
}

// resetMentionSelection returns the highlight to the first candidate. It runs
// whenever the draft changes so a stale index never carries over to a
// different candidate list.
func (m *shellModel) resetMentionSelection() {
	m.mentionAutocomplete.selected = 0
}

// composerMentionSegments renders the suggestion strip: the selected
// candidate is highlighted, each entry carries its role glyph, and the list
// is truncated to whatever width the composer has.
func (m shellModel) composerMentionSegments(matches []*chatterEntry) []composerSegment {
	if len(matches) == 0 {
		return nil
	}
	selected := m.mentionSelectedIndex(matches)
	segments := []composerSegment{{text: "@", foreground: m.theme.Accent, bold: true}}
	for i, entry := range matches {
		if i > 0 {
			segments = append(segments, composerSegment{text: " ", foreground: m.theme.Muted})
		}
		label := entry.name()
		if glyph := roleGlyph(entry.roleLabel()); glyph != "" {
			label = glyph + label
		}
		if i == selected {
			segments = append(segments, composerSegment{
				text:       "[" + label + "]",
				foreground: m.theme.Accent,
				bold:       true,
			})
			continue
		}
		segments = append(segments, composerSegment{text: label, foreground: m.theme.Muted})
	}
	segments = append(segments, composerSegment{text: "  tab", foreground: m.theme.Muted, italic: true})
	return segments
}
