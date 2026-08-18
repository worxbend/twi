package textsafe

import "testing"

func TestDisplayStripsEscapeSequencesAndControls(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "hello chat", "hello chat"},
		{"csi cursor move", "hi \x1b[2Jgone", "hi [2Jgone"},
		{"osc window title", "hi \x1b]0;pwned\x07 there", "hi ]0;pwned there"},
		{"carriage return and newline", "line one\r\nline two", "line oneline two"},
		{"del and nul", "a\x7fb\x00c", "abc"},
		{"c1 control", "a\u0085b", "ab"},
		{"bidi override", "safe\u202egnp.exe", "safegnp.exe"},
		{"emoji with zero width joiner survives", "\U0001F469\u200D\U0001F4BB\uFE0F", "\U0001F469\u200D\U0001F4BB\uFE0F"},
		{"non-latin script survives", "привет 日本語", "привет 日本語"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Display(tc.in); got != tc.want {
				t.Fatalf("Display(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if NeedsSanitizing(tc.in) != (tc.in != tc.want) {
				t.Fatalf("NeedsSanitizing(%q) disagrees with Display", tc.in)
			}
		})
	}
}

func TestDisplayReturnsInputUnchangedWhenNothingToStrip(t *testing.T) {
	in := "an ordinary message with emoji \U0001F600"
	if got := Display(in); got != in {
		t.Fatalf("Display changed clean text: %q", got)
	}
}
