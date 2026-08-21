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

func TestSanitizeIRCTextDropsControlsButKeepsCTCP(t *testing.T) {
	got := sanitizeText("\x01ACTION wa\x07ves\x1b[31m\x01")
	if want := "\x01ACTION waves[31m\x01"; got != want {
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

// TestSanitizeIRCTextTruncationKeepsActionClosed guards a detail that would
// otherwise surface as a broken /me: dropping the closing CTCP delimiter makes
// every client render the raw wrapper as text.
func TestSanitizeIRCTextTruncationKeepsActionClosed(t *testing.T) {
	got := sanitizeText("\x01ACTION " + strings.Repeat("x", 600) + "\x01")
	runes := []rune(got)
	if len(runes) != twitch.MaxChatMessageRunes {
		t.Fatalf("len(runes) = %d, want %d", len(runes), twitch.MaxChatMessageRunes)
	}
	if runes[0] != ctcpDelimiter || runes[len(runes)-1] != ctcpDelimiter {
		t.Fatalf("truncated action lost its CTCP wrapper: %q ... %q",
			string(runes[0]), string(runes[len(runes)-1]))
	}
}

func TestSanitizeIRCTextLeavesShortMessagesAlone(t *testing.T) {
	const text = "gg wp that was a great play"
	if got := sanitizeText(text); got != text {
		t.Fatalf("sanitizeText(%q) = %q, want it unchanged", text, got)
	}
}
