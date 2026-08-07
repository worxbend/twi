package render

import (
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func renderedText(rows []Row) string {
	var b strings.Builder
	for _, row := range rows {
		for _, f := range row.Fragments {
			b.WriteString(f.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestMessageOpeningWithLongMentionKeepsItsAuthor is the regression for a
// wrapping branch that discarded the row it was abandoning.
//
// On the content pass indentWidth is exactly the prefix width, so
// `used == indentWidth` is true on the very first row too -- the one holding
// the timestamp, badges and author name. When a message opened with a mention
// or emote too wide to sit beside the prefix, that row was thrown away
// instead of emitted, and the message rendered with no author at all. An
// unattributed line in chat is not a cosmetic problem: you cannot tell who
// said it.
func TestMessageOpeningWithLongMentionKeepsItsAuthor(t *testing.T) {
	msg := twitch.ChatMessage{
		ID:          "m1",
		AuthorLogin: "streamerguy",
		DisplayName: "StreamerGuy",
		Text:        "@averylongmentionname hello",
		Type:        twitch.MessageTypeChat,
	}
	for _, layout := range []LayoutMode{LayoutInline, LayoutGrouped, LayoutCompact} {
		for width := 14; width <= 60; width++ {
			opts := DefaultOptions(width)
			opts.Layout = layout
			out := renderedText(Rows(msg, opts))
			if !strings.Contains(out, "StreamerGuy") {
				t.Fatalf("layout=%s width=%d: author missing from rendered message:\n%s", layout, width, out)
			}
		}
	}
}

// TestMessageOpeningWithLongEmoteKeepsItsAuthor covers the other atomic
// fragment kind that can trigger the same branch.
func TestMessageOpeningWithLongEmoteKeepsItsAuthor(t *testing.T) {
	msg := twitch.ChatMessage{
		ID:          "m2",
		AuthorLogin: "viewer",
		DisplayName: "Viewer",
		Text:        "ResidentSleeperResidentSleeper gg",
		Type:        twitch.MessageTypeChat,
		Emotes: []twitch.Emote{{
			Name:  "ResidentSleeperResidentSleeper",
			Start: 0,
			End:   29,
		}},
	}
	for width := 14; width <= 60; width++ {
		out := renderedText(Rows(msg, DefaultOptions(width)))
		if !strings.Contains(out, "Viewer") {
			t.Fatalf("width=%d: author missing from rendered message:\n%s", width, out)
		}
	}
}

// TestWrappingNeverDropsContent sweeps a range of shapes and widths and
// asserts every non-space character survives, so a future wrapping change
// cannot quietly lose text.
func TestWrappingNeverDropsContent(t *testing.T) {
	squash := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\t' {
				return -1
			}
			return r
		}, s)
	}

	texts := []string{
		"@a @b @c @d @e",
		"@averylongmentionname hello",
		"hi @averylongmentionname there",
		"short",
		strings.Repeat("word ", 20),
		"@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"日本語のテキスト @someone です",
	}
	for _, text := range texts {
		for _, layout := range []LayoutMode{LayoutInline, LayoutGrouped, LayoutCompact} {
			for width := 10; width <= 60; width++ {
				msg := twitch.ChatMessage{
					AuthorLogin: "u", DisplayName: "u",
					Text: text, Type: twitch.MessageTypeChat,
				}
				opts := DefaultOptions(width)
				opts.Layout = layout
				out := squash(renderedText(Rows(msg, opts)))
				if !strings.Contains(out, squash(text)) {
					t.Fatalf("layout=%s width=%d text=%q: content lost, got %q", layout, width, text, out)
				}
			}
		}
	}
}

// TestWrappedRowsStayWithinWidth guards the other direction: emitting the
// abandoned row must not push any row past the requested width.
func TestWrappedRowsStayWithinWidth(t *testing.T) {
	msg := twitch.ChatMessage{
		AuthorLogin: "streamerguy",
		DisplayName: "StreamerGuy",
		Text:        "@averylongmentionname hello there everyone",
		Type:        twitch.MessageTypeChat,
	}
	for _, layout := range []LayoutMode{LayoutInline, LayoutGrouped, LayoutCompact} {
		for width := 14; width <= 60; width++ {
			opts := DefaultOptions(width)
			opts.Layout = layout
			for i, row := range Rows(msg, opts) {
				if got := row.Width(); got > width {
					t.Fatalf("layout=%s width=%d: row %d is %d cells wide", layout, width, i, got)
				}
			}
		}
	}
}

// TestWrappingBreaksBetweenWords covers the readability change. Chat is
// prose, and mid-word breaks are materially harder to read at the speed a
// busy channel moves.
func TestWrappingBreaksBetweenWords(t *testing.T) {
	msg := twitch.ChatMessage{
		AuthorLogin: "u",
		DisplayName: "u",
		Text:        "the quick brown fox jumps over the lazy dog",
		Type:        twitch.MessageTypeChat,
	}
	for width := 20; width <= 60; width++ {
		rows := Rows(msg, DefaultOptions(width))
		for i, row := range rows {
			if i == len(rows)-1 {
				continue
			}
			plain := strings.TrimRight(row.Plain(), " ")
			next := strings.TrimLeft(rows[i+1].Plain(), " ")
			if plain == "" || next == "" {
				continue
			}
			// A break through a word leaves a non-space at the end of one row
			// and a non-space at the start of the next, with no space between
			// them in the original text.
			lastWord := plain[strings.LastIndex(plain, " ")+1:]
			firstWord := next
			if idx := strings.Index(next, " "); idx >= 0 {
				firstWord = next[:idx]
			}
			if lastWord != "" && firstWord != "" &&
				strings.Contains(msg.Text, lastWord+firstWord) &&
				!strings.Contains(msg.Text, lastWord+" "+firstWord) {
				t.Fatalf("width=%d: %q was split across rows %d and %d", width, lastWord+firstWord, i, i+1)
			}
		}
	}
}

// TestOverlongWordStillWraps keeps a word wider than the line renderable
// rather than overflowing or vanishing.
func TestOverlongWordStillWraps(t *testing.T) {
	long := strings.Repeat("x", 120)
	msg := twitch.ChatMessage{
		AuthorLogin: "u", DisplayName: "u",
		Text: long, Type: twitch.MessageTypeChat,
	}
	for _, width := range []int{20, 30, 45} {
		rows := Rows(msg, DefaultOptions(width))
		var b strings.Builder
		for _, row := range rows {
			b.WriteString(strings.TrimSpace(row.Plain()))
			if row.Width() > width {
				t.Fatalf("width=%d: row exceeds width at %d cells", width, row.Width())
			}
		}
		if !strings.Contains(b.String(), long) {
			t.Fatalf("width=%d: an over-long word lost characters", width)
		}
	}
}
