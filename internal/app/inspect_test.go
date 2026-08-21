package app

import (
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

// TestInspectPaneStripsTerminalEscapes is a regression test for a control
// sequence reaching the terminal through the inspect panel.
//
// The panel exists to show a message exactly as Twitch delivered it -- raw IRC
// tags included -- which makes it the one view that deliberately renders
// attacker-controlled text that has bypassed the normal rendering path. It was
// passed only through redactSensitive, which looks for credentials and has
// nothing to say about control characters, so a tag value could clear the
// screen, reposition the cursor, or set a scroll region.
//
// Every one of these is real IRC tag content: Twitch forwards unknown tags
// verbatim, so the value is whatever the sender put there.
func TestInspectPaneStripsTerminalEscapes(t *testing.T) {
	const (
		clearScreen  = "\x1b[2J"
		moveCursor   = "\x1b[10;10H"
		bell         = "\x07"
		scrollRegion = "\x1b[1;5r"
	)
	message := twitch.ChatMessage{
		ID:          "abc",
		Channel:     "example",
		AuthorLogin: "chatter",
		Text:        "hello",
		RawTags: map[string]string{
			"custom-tag" + bell:     clearScreen + "value",
			"another":               moveCursor + scrollRegion,
			"reply-parent-msg-body": "quoted" + clearScreen,
		},
	}

	line := inspectRawTagsLine(message.RawTags)
	for name, seq := range map[string]string{
		"clear screen":  clearScreen,
		"cursor move":   moveCursor,
		"bell":          bell,
		"scroll region": scrollRegion,
	} {
		if strings.Contains(line, seq) {
			t.Errorf("inspect raw-tags line leaked a %s escape: %q", name, line)
		}
	}
	if strings.Contains(line, "\x1b") {
		t.Errorf("inspect raw-tags line still contains ESC: %q", line)
	}
}

// TestInspectPaneStripsEscapesFromEveryDiagnosticField covers the other fields
// the panel renders from the wire, not just raw tags.
func TestInspectPaneStripsEscapesFromEveryDiagnosticField(t *testing.T) {
	const esc = "\x1b[2J"
	message := twitch.ChatMessage{
		ID:          "id" + esc,
		Channel:     "chan" + esc,
		AuthorLogin: "login" + esc,
		DisplayName: "display" + esc,
		AuthorColor: "#fff" + esc,
		Badges:      []twitch.Badge{{SetID: "mod" + esc, ID: "1" + esc, Info: "info" + esc}},
		Reply:       &twitch.Reply{ParentMessageID: "pid" + esc, ParentLogin: "plogin" + esc, ParentText: "ptext" + esc},
		Emotes:      []twitch.Emote{{ID: "e" + esc, Name: "Kappa" + esc}},
	}

	for name, line := range map[string]string{
		"message": inspectMessageLine(message),
		"author":  inspectAuthorLine(message),
		"badges":  inspectBadgesLine(message.Badges),
		"reply":   inspectReplyLine(message.Reply),
		"emotes":  inspectEmotesLine(message.Emotes),
	} {
		if strings.Contains(line, "\x1b") {
			t.Errorf("inspect %s line leaked an escape: %q", name, line)
		}
	}
}
