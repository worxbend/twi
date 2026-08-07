package render

import (
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func rowsText(rows []Row) string {
	var b strings.Builder
	for _, row := range rows {
		for _, f := range row.Fragments {
			b.WriteString(f.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func hasFragmentKind(rows []Row, kind FragmentKind) bool {
	for _, row := range rows {
		for _, f := range row.Fragments {
			if f.Kind == kind {
				return true
			}
		}
	}
	return false
}

// TestFirstMessageIsMarked covers the one thing a streamer has to act on
// while it is still on screen. Twitch's first-msg tag is the only reliable
// source: a local roster cannot know whether someone has visited before.
func TestFirstMessageIsMarked(t *testing.T) {
	msg := twitch.ChatMessage{
		ID:           "m1",
		Channel:      "example",
		AuthorLogin:  "newcomer",
		DisplayName:  "Newcomer",
		Text:         "hi, first time here",
		Type:         twitch.MessageTypeChat,
		FirstMessage: true,
	}
	rows := Rows(msg, DefaultOptions(80))
	if !hasFragmentKind(rows, FragmentFirstMessage) {
		t.Fatalf("no first-message marker rendered:\n%s", rowsText(rows))
	}
	if !strings.Contains(rowsText(rows), "hi, first time here") {
		t.Fatal("the message text was lost")
	}
}

func TestOrdinaryMessageIsNotMarked(t *testing.T) {
	msg := twitch.ChatMessage{
		ID:          "m2",
		AuthorLogin: "regular",
		DisplayName: "Regular",
		Text:        "hello again",
		Type:        twitch.MessageTypeChat,
	}
	rows := Rows(msg, DefaultOptions(80))
	if hasFragmentKind(rows, FragmentFirstMessage) {
		t.Fatal("an ordinary message was marked as a first message")
	}
}

// TestFirstMessageMarkerYieldsToNarrowWidths keeps the marker from eating the
// space the message text needs.
func TestFirstMessageMarkerYieldsToNarrowWidths(t *testing.T) {
	msg := twitch.ChatMessage{
		AuthorLogin:  "newcomer",
		DisplayName:  "Newcomer",
		Text:         "hi",
		Type:         twitch.MessageTypeChat,
		FirstMessage: true,
	}
	rows := Rows(msg, DefaultOptions(20))
	if hasFragmentKind(rows, FragmentFirstMessage) {
		t.Fatal("the marker was rendered at a width too narrow to afford it")
	}
	if !strings.Contains(rowsText(rows), "hi") {
		t.Fatal("the message text was lost at a narrow width")
	}
}

// TestFirstMessageMarkerAbsentInCompactLayout: compact trades every
// decoration for text, and this is a decoration.
func TestFirstMessageMarkerAbsentInCompactLayout(t *testing.T) {
	msg := twitch.ChatMessage{
		AuthorLogin:  "newcomer",
		DisplayName:  "Newcomer",
		Text:         "hi there",
		Type:         twitch.MessageTypeChat,
		FirstMessage: true,
	}
	opts := DefaultOptions(80)
	opts.Layout = LayoutCompact
	if hasFragmentKind(Rows(msg, opts), FragmentFirstMessage) {
		t.Fatal("compact layout rendered the first-message marker")
	}
}

func TestFirstMessageRowsStayWithinWidth(t *testing.T) {
	msg := twitch.ChatMessage{
		AuthorLogin:  "newcomer",
		DisplayName:  "Newcomer",
		Text:         strings.Repeat("word ", 40),
		Type:         twitch.MessageTypeChat,
		FirstMessage: true,
	}
	for _, width := range []int{24, 40, 80, 120} {
		for _, row := range Rows(msg, DefaultOptions(width)) {
			total := 0
			for _, f := range row.Fragments {
				total += textWidth(f.Text)
			}
			if total > width {
				t.Fatalf("width=%d: row is %d cells wide, want <= %d", width, total, width)
			}
		}
	}
}
