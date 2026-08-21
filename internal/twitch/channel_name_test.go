package twitch_test

import (
	"slices"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestNormalizeChannelTrimsWhitespaceBeforeTheHash(t *testing.T) {
	// " #beta" is not a contrived value: it is the second entry produced by
	// splitting the config line `channels = alpha, #beta` on commas. Trimming
	// the "#" first leaves it in place, which is the bug this pins.
	cases := map[string]string{
		" #beta":    "beta",
		"#beta":     "beta",
		" #delta ":  "delta",
		"\t#gamma ": "gamma",
		"alpha":     "alpha",
		" alpha ":   "alpha",
		"##double":  "#double",
		"":          "",
		"   ":       "",
		"#":         "",
	}
	for in, want := range cases {
		if got := twitch.NormalizeChannel(in); got != want {
			t.Errorf("NormalizeChannel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeChannelKeepsTheUsersCapitals(t *testing.T) {
	// The normalized form is shown on screen, so it must not flatten the
	// capitals someone chose for their own channel name.
	if got := twitch.NormalizeChannel(" #ShroudTV"); got != "ShroudTV" {
		t.Errorf("NormalizeChannel = %q, want %q", got, "ShroudTV")
	}
}

func TestChannelKeyLowerCasesForIdentity(t *testing.T) {
	// IRC channel names are case-insensitive, so these three must collapse to
	// one key or the same channel opens twice in the sidebar.
	for _, in := range []string{"#Foo", " foo ", "FOO"} {
		if got := twitch.ChannelKey(in); got != "foo" {
			t.Errorf("ChannelKey(%q) = %q, want %q", in, got, "foo")
		}
	}
}

func TestNormalizeChannelsDropsEmptyEntries(t *testing.T) {
	// A trailing comma in a config line yields an empty entry; it must not
	// become a channel with an empty name.
	got := twitch.NormalizeChannels([]string{"alpha", " #beta", "", "  ", "#"})
	want := []string{"alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Errorf("NormalizeChannels = %q, want %q", got, want)
	}
}
