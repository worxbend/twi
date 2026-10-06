package theme

import (
	"strings"
	"testing"
)

// TestForDerivesEveryTokenFromThePalette pins the derivation contract: a
// fully populated preset produces a fully populated token set, so components
// never draw a blank cell because a derived role came up empty.
func TestForDerivesEveryTokenFromThePalette(t *testing.T) {
	for _, name := range []string{"claude", "nord", "mono", "codex"} {
		palette, ok := ResolvePalette(name, Palette{})
		if !ok {
			t.Fatalf("ResolvePalette(%q) was not found", name)
		}
		tokens := For(palette)
		for field, value := range map[string]string{
			"Canvas": tokens.Canvas, "Surface": tokens.Surface, "SurfaceHi": tokens.SurfaceHi,
			"Track": tokens.Track, "Text": tokens.Text, "Muted": tokens.Muted, "Faint": tokens.Faint,
			"Accent": tokens.Accent, "AccentSoft": tokens.AccentSoft, "OnAccent": tokens.OnAccent,
			"Border": tokens.Border, "BorderSoft": tokens.BorderSoft,
			"Success": tokens.Success, "Warning": tokens.Warning, "Error": tokens.Error, "Info": tokens.Info,
			"OnSuccess": tokens.OnSuccess, "OnWarning": tokens.OnWarning, "OnError": tokens.OnError,
			"Selection": tokens.Selection,
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("For(%q).%s is empty", name, field)
			}
		}
	}
}

// TestForOnColorsAreReadable guarantees the on-color paired with each solid
// badge hue meets the same 4.5:1 text contrast the rest of the UI enforces,
// for every built-in preset.
func TestForOnColorsAreReadable(t *testing.T) {
	for _, name := range PresetNames() {
		palette, _ := ResolvePalette(name, Palette{})
		tokens := For(palette)
		pairs := map[string][2]string{
			"OnAccent":  {tokens.OnAccent, tokens.Accent},
			"OnSuccess": {tokens.OnSuccess, tokens.Success},
			"OnWarning": {tokens.OnWarning, tokens.Warning},
			"OnError":   {tokens.OnError, tokens.Error},
		}
		for field, pair := range pairs {
			foreground, foregroundOK := parseHexColor(pair[0])
			background, backgroundOK := parseHexColor(pair[1])
			if !foregroundOK || !backgroundOK {
				t.Errorf("%s %s has an unparseable pair %v", name, field, pair)
				continue
			}
			if ratio := contrastRatio(foreground, background); ratio < minimumTextContrast {
				t.Errorf("%s %s contrast = %.2f, want >= %.1f (%s on %s)", name, field, ratio, minimumTextContrast, pair[0], pair[1])
			}
		}
	}
}

// TestForDegradesInvalidHexQuietly: a custom theme with blank or malformed
// colors must still produce tokens; the derivation helpers pass broken values
// through rather than inventing colors or panicking.
func TestForDegradesInvalidHexQuietly(t *testing.T) {
	broken := Palette{Background: "not-a-color", Accent: "#9146ff"}
	tokens := For(broken)
	if tokens.Accent != "#9146ff" {
		t.Fatalf("For(broken).Accent = %q, want the valid accent passed through", tokens.Accent)
	}
	if tokens.Canvas != "not-a-color" {
		t.Fatalf("For(broken).Canvas = %q, want the broken background passed through unchanged", tokens.Canvas)
	}
}

// TestKindColorMapping pins which token each semantic kind resolves to, so a
// component asking for KindInfo can never silently drift off the accent hue.
func TestKindColorMapping(t *testing.T) {
	tokens := For(DefaultPalette())
	if got := tokens.Color(KindInfo); got != tokens.Accent {
		t.Fatalf("Color(KindInfo) = %q, want accent %q", got, tokens.Accent)
	}
	if got := tokens.Color(KindNeutral); got != tokens.Muted {
		t.Fatalf("Color(KindNeutral) = %q, want muted %q", got, tokens.Muted)
	}
	if got := tokens.OnColor(KindError); got != tokens.OnError {
		t.Fatalf("OnColor(KindError) = %q, want %q", got, tokens.OnError)
	}
}
