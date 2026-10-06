package irc

import (
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestSanitizeIRCTextNeutralizesCommandInjection(t *testing.T) {
	// IRC frames messages with CRLF. Without sanitizing, everything after the
	// newline is parsed as a fresh command from the authenticated user.
	tests := []struct {
		name string
		text string
	}{
		{"crlf", "hello\r\nPART #victim"},
		{"lf", "hello\nPRIVMSG #other :spam"},
		{"cr", "hello\rQUIT"},
		{"leading", "\r\nJOIN #attacker"},
		{"repeated", "a\r\nb\r\nc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeText(tc.text)
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("sanitizeText(%q) = %q, still contains a line break", tc.text, got)
			}
		})
	}
}

func TestSanitizeIRCTextKeepsVisibleContent(t *testing.T) {
	got := sanitizeText("hello\r\nworld")
	if want := "hello  world"; got != want {
		t.Fatalf("sanitizeText = %q, want %q", got, want)
	}
}

func TestSanitizeIRCTextDropsAllControls(t *testing.T) {
	got := sanitizeText("\x01ACTION wa\x07ves\x1b[31m\x01")
	if want := "ACTION waves[31m"; got != want {
		t.Fatalf("sanitizeText = %q, want %q", got, want)
	}
}

// TestSanitizeIRCTextDropsPastedCTCPDelimiter guards the wire boundary: the
// CTCP delimiter is framing Send adds for /me after sanitizing, so in user
// text it must be dropped like any other control -- a pasted "\x01VERSION\x01"
// would otherwise reach the channel as a raw CTCP command.
func TestSanitizeIRCTextDropsPastedCTCPDelimiter(t *testing.T) {
	got := sanitizeText("\x01VERSION\x01")
	if want := "VERSION"; got != want {
		t.Fatalf("sanitizeText = %q, want %q", got, want)
	}
}

func TestSanitizeIRCTextPreservesUnicode(t *testing.T) {
	const text = "こんにちは 👋 café"
	if got := sanitizeText(text); got != text {
		t.Fatalf("sanitizeText(%q) = %q, want it unchanged", text, got)
	}
}

func TestSanitizeIRCTextTruncatesToTwitchLimit(t *testing.T) {
	got := sanitizeText(strings.Repeat("か", 600))
	if runes := []rune(got); len(runes) != twitch.MaxChatMessageRunes {
		t.Fatalf("len(runes) = %d, want %d", len(runes), twitch.MaxChatMessageRunes)
	}
}

func TestSanitizeIRCTextLeavesShortMessagesAlone(t *testing.T) {
	const text = "gg wp that was a great play"
	if got := sanitizeText(text); got != text {
		t.Fatalf("sanitizeText(%q) = %q, want it unchanged", text, got)
	}
}

// TestStripControlCharsNeutralizesCommandInjection guards the wire values
// (username, OAuth token, channel name) that reach gempir's raw PASS/NICK/JOIN
// writes: unlike chat text, an embedded CRLF there would let an attacker who
// controls one of these fields smuggle a second IRC command past our own
// framing, not just the user's own message.
func TestStripControlCharsNeutralizesCommandInjection(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"crlf", "user\r\nPART #victim"},
		{"lf", "user\nQUIT"},
		{"cr", "user\rNICK evil"},
		{"tab", "user\tname"},
		{"del", "user\x7fname"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripControlChars(tc.in)
			if strings.ContainsAny(got, "\r\n\t") || strings.ContainsRune(got, 0x7f) {
				t.Fatalf("stripControlChars(%q) = %q, still contains a control character", tc.in, got)
			}
		})
	}
}

func TestStripControlCharsPreservesUnicode(t *testing.T) {
	const text = "naïve_こんにちは_user"
	if got := stripControlChars(text); got != text {
		t.Fatalf("stripControlChars(%q) = %q, want it unchanged", text, got)
	}
}

// TestNormalizeChannelStripsEmbeddedControlChars is a regression test for the
// same CRLF-smuggling class TestStripControlCharsNeutralizesCommandInjection
// covers, through the actual entry point channel names take before they
// reach the wire.
func TestNormalizeChannelStripsEmbeddedControlChars(t *testing.T) {
	got := normalizeChannel("foo\r\nJOIN #attacker")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("normalizeChannel with embedded CRLF = %q, still contains a line break", got)
	}
}

// TestNewClientStripsControlCharsFromCredentials is a regression test for the
// same CRLF-smuggling class, through NewClient's username/token entry point.
func TestNewClientStripsControlCharsFromCredentials(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "user\r\nQUIT",
		Channels:   []string{"demo"},
		OAuthToken: "oauth:abc\r\nPRIVMSG #victim :spam",
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if strings.ContainsAny(client.username, "\r\n") {
		t.Fatalf("client.username = %q, still contains a line break", client.username)
	}
	if strings.ContainsAny(client.token, "\r\n") {
		t.Fatalf("client.token = %q, still contains a line break", client.token)
	}
}
