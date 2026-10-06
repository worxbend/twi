package irc

import (
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

// ctcpDelimiter wraps a CTCP ACTION ("/me"). Send adds it after sanitizing,
// so sanitizeText itself drops it like every other C0 control.
const ctcpDelimiter = '\x01'

// sanitizeText makes text safe to put on the wire as a single IRC message.
//
// IRC frames messages with CRLF, so a carriage return or newline inside the
// text ends the PRIVMSG early and the remainder is parsed as a new command.
// Anything that can reach outbound text -- a pasted message, a clipboard
// bracketed paste, a crafted string from any future automation -- could
// otherwise issue arbitrary IRC commands as the authenticated user. Both
// become spaces so the visible message survives intact.
//
// Other C0 controls are dropped, including the CTCP delimiter: it must not
// survive in user text, or a pasted "\x01VERSION\x01" would go out as a raw
// CTCP command in an ordinary message. The only delimiters on the wire are
// the pair Send wraps around an action after sanitizing.
//
// The result is truncated to Twitch's length limit.
func sanitizeText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// Dropped: not typeable, not meaningful, and interpreted by
			// terminals that render this message later.
		default:
			b.WriteRune(r)
		}
	}
	return truncateChatMessage(b.String())
}

func truncateChatMessage(text string) string {
	runes := []rune(text)
	if len(runes) <= twitch.MaxChatMessageRunes {
		return text
	}
	action := len(runes) > 0 && runes[0] == ctcpDelimiter &&
		runes[len(runes)-1] == ctcpDelimiter
	if !action {
		return string(runes[:twitch.MaxChatMessageRunes])
	}
	// Leave room to close the CTCP wrapper.
	return string(runes[:twitch.MaxChatMessageRunes-1]) + string(ctcpDelimiter)
}
