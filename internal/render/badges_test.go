package render

import (
	"testing"

	"github.com/rivo/uniseg"
)

// Glyph badges reserve BadgeGlyphWidth cells each. A width-2 glyph (any
// emoji-presentation codepoint) would silently push message text out of
// alignment on every row that carries that badge, so the invariant is
// enforced rather than assumed.
func TestBadgeGlyphsAreSingleCell(t *testing.T) {
	for setID, glyph := range badgeGlyphs {
		if got := uniseg.StringWidth(glyph); got != 1 {
			t.Errorf("badge %q glyph %q width = %d cells, want 1", setID, glyph, got)
		}
	}
	if fallback, ok := BadgeGlyph("not-a-real-badge-set"); ok {
		t.Errorf("BadgeGlyph reported an unknown set as known: %q", fallback)
	} else if uniseg.StringWidth(fallback) != 1 {
		t.Errorf("unknown-badge fallback %q width = %d cells, want 1", fallback, uniseg.StringWidth(fallback))
	}
}

func TestBadgeGlyphLookupIsCaseInsensitive(t *testing.T) {
	glyph, ok := BadgeGlyph("  MODERATOR ")
	if !ok {
		t.Fatal("BadgeGlyph did not match a padded, upper-case badge set")
	}
	if want, _ := BadgeGlyph("moderator"); glyph != want {
		t.Fatalf("BadgeGlyph case mismatch: got %q, want %q", glyph, want)
	}
}

func TestNormalizeBadgeModeFallsBackToDefault(t *testing.T) {
	for _, test := range []struct {
		in   string
		want BadgeMode
	}{
		{"text", BadgeModeText},
		{"GLYPH", BadgeModeGlyph},
		{" off ", BadgeModeOff},
		{"", DefaultBadgeMode},
		{"nonsense", DefaultBadgeMode},
	} {
		if got := NormalizeBadgeMode(test.in); got != test.want {
			t.Errorf("NormalizeBadgeMode(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
