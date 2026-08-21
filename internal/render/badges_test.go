package render

import (
	"testing"

	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

// badgeStyles is the single source for three different views of a badge: the
// glyph, the palette color, and the compact text label. This test walks the
// table and checks each view reports what the table says, so a badge set added
// with a missing field cannot render half-styled.
//
// It also enforces the width-1 glyph invariant. A width-2 glyph (any
// emoji-presentation codepoint) would silently push message text out of
// alignment on every row that carries that badge.
func TestBadgeStylesStayInSync(t *testing.T) {
	palette := theme.DefaultPalette()
	for setID, style := range badgeStyles {
		if style.glyph == "" {
			t.Errorf("badge %q has no glyph", setID)
		} else if got := uniseg.StringWidth(style.glyph); got != 1 {
			t.Errorf("badge %q glyph %q width = %d cells, want 1", setID, style.glyph, got)
		}
		if style.color == nil {
			t.Errorf("badge %q has no color", setID)
			continue
		}

		glyph, ok := BadgeGlyph(setID)
		if !ok || glyph != style.glyph {
			t.Errorf("BadgeGlyph(%q) = %q, %t; want %q, true", setID, glyph, ok, style.glyph)
		}
		if got, want := badgeGlyphColor(setID, palette), style.color(palette); got != want {
			t.Errorf("badgeGlyphColor(%q) = %q, want %q", setID, got, want)
		}

		// An empty shortLabel is deliberate for sets with no accepted
		// abbreviation ("staff", "turbo"): they keep their raw set ID.
		wantLabel := "[" + setID + "]"
		if style.shortLabel != "" {
			wantLabel = "[" + style.shortLabel + "]"
		}
		if got := compactBadgeLabel(twitch.Badge{SetID: setID}); got != wantLabel {
			t.Errorf("compactBadgeLabel(%q) = %q, want %q", setID, got, wantLabel)
		}
	}
}

// An unknown badge set has to degrade in all three views at once: a neutral
// single-cell marker, the muted color, and its raw set ID as the label.
func TestUnknownBadgeSetFallsBack(t *testing.T) {
	const setID = "not-a-real-badge-set"
	palette := theme.DefaultPalette()

	fallback, ok := BadgeGlyph(setID)
	if ok {
		t.Errorf("BadgeGlyph reported an unknown set as known: %q", fallback)
	}
	if uniseg.StringWidth(fallback) != 1 {
		t.Errorf("unknown-badge fallback %q width = %d cells, want 1", fallback, uniseg.StringWidth(fallback))
	}
	if got := badgeGlyphColor(setID, palette); got != palette.Muted {
		t.Errorf("badgeGlyphColor(%q) = %q, want the muted color %q", setID, got, palette.Muted)
	}
	if got, want := compactBadgeLabel(twitch.Badge{SetID: setID}), "["+setID+"]"; got != want {
		t.Errorf("compactBadgeLabel(%q) = %q, want %q", setID, got, want)
	}
	if got, want := compactBadgeLabel(twitch.Badge{}), "[badge]"; got != want {
		t.Errorf("compactBadgeLabel of an empty set ID = %q, want %q", got, want)
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
