package theme

import (
	"reflect"
	"testing"
)

func TestPresetNamesListsEveryBuiltInInStableOrder(t *testing.T) {
	want := []string{
		"ayu-dark", "ayu-mirage", "btop", "catppuccin-frappe",
		"catppuccin-latte", "catppuccin-macchiato", "catppuccin-mocha",
		"claude", "cobalt2", "codex", "dracula", "everforest", "github-light",
		"gruvbox", "gruvbox-light", "horizon", "kanagawa", "mono", "monokai",
		"night-owl", "nightfox", "nord", "oceanic-next", "one-dark",
		"palenight", "rose-pine", "rose-pine-dawn", "rose-pine-moon",
		"solarized-dark", "solarized-light", "synthwave-84", "tokyo-night",
		"zenburn",
	}
	got := PresetNames()
	if len(got) != len(want) {
		t.Fatalf("PresetNames() = %v (%d), want %v (%d)", got, len(got), want, len(want))
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("PresetNames()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// Every role must be a parseable hex color: an unset or malformed field would
// reach lipgloss as an unknown color and render as the terminal's default,
// silently dropping that role's meaning instead of failing loudly.
func TestPresetPalettesFillEveryRoleWithValidHex(t *testing.T) {
	for _, name := range PresetNames() {
		palette := Presets()[name]
		if palette == (Palette{}) {
			t.Fatalf("preset %q has zero-value palette", name)
		}
		for role, color := range paletteRoles(palette) {
			if _, ok := parseHexColor(color); !ok {
				t.Fatalf("preset %q has invalid %s %q", name, role, color)
			}
		}
	}
}

// publishedSurfaceExceptions records presets whose own upstream scheme pairs
// its body text with its panel color below the text bar. Solarized Dark's
// base0 on base02 measures 4.11 by the scheme's definition, so twi keeps the
// published pairing rather than inventing a different Solarized. New presets
// must not join this list.
var publishedSurfaceExceptions = map[string]float64{
	"solarized-dark": 4.0,
}

// Body text has to clear the same contrast bar ContrastCorrectedForeground
// enforces for derived colors, on the canvas and on raised panes alike.
// Muted is deliberately excluded: several upstream schemes publish a
// low-contrast comment color, and twi keeps their identity rather than
// substituting its own.
func TestPresetForegroundsAreReadableOnBackgroundAndSurface(t *testing.T) {
	for _, name := range PresetNames() {
		palette := Presets()[name]
		foreground, _ := parseHexColor(palette.Foreground)
		for role, color := range map[string]string{
			"background": palette.Background,
			"surface":    palette.Surface,
		} {
			want := minimumTextContrast
			if floor, ok := publishedSurfaceExceptions[name]; ok && role == "surface" {
				want = floor
			}
			behind, _ := parseHexColor(color)
			if got := contrastRatio(foreground, behind); got < want {
				t.Fatalf("preset %q foreground contrast on %s = %.2f, want >= %.2f", name, role, got, want)
			}
		}
	}
}

// The accent paints the status bar and every focus rail, so it has to be
// visible against the canvas rather than blending into it. 3.0 is the
// large-text/graphical-object bar, which is what an accent fill is.
func TestPresetAccentsStandOutFromTheirBackground(t *testing.T) {
	const minimumAccentContrast = 3.0
	for _, name := range PresetNames() {
		palette := Presets()[name]
		accent, _ := parseHexColor(palette.Accent)
		background, _ := parseHexColor(palette.Background)
		if got := contrastRatio(accent, background); got < minimumAccentContrast {
			t.Fatalf("preset %q accent contrast = %.2f, want >= %.2f", name, got, minimumAccentContrast)
		}
	}
}

func TestPresetPalettesAreDistinct(t *testing.T) {
	seen := make(map[Palette]string, len(presets))
	for _, name := range PresetNames() {
		palette := Presets()[name]
		if other, ok := seen[palette]; ok {
			t.Fatalf("presets %q and %q are the same palette", other, name)
		}
		seen[palette] = name
	}
}

func paletteRoles(palette Palette) map[string]string {
	return map[string]string{
		"background": palette.Background,
		"foreground": palette.Foreground,
		"accent":     palette.Accent,
		"muted":      palette.Muted,
		"border":     palette.Border,
		"surface":    palette.Surface,
		"warning":    palette.Warning,
		"error":      palette.Error,
		"success":    palette.Success,
	}
}

func TestDefaultPaletteIsClaudePreset(t *testing.T) {
	if got, want := DefaultPalette(), Presets()["claude"]; got != want {
		t.Fatalf("DefaultPalette() = %+v, want %+v", got, want)
	}
}

func TestResolvePaletteKnownPreset(t *testing.T) {
	got, ok := ResolvePalette("Nord", Palette{})
	if !ok {
		t.Fatal("ResolvePalette(\"Nord\", ...) ok = false, want true")
	}
	if want := Presets()["nord"]; got != want {
		t.Fatalf("ResolvePalette(\"Nord\", ...) = %+v, want %+v", got, want)
	}
}

func TestResolvePaletteCustom(t *testing.T) {
	custom := Palette{Background: "#010101", Foreground: "#fefefe"}
	got, ok := ResolvePalette("custom", custom)
	if !ok || got != custom {
		t.Fatalf("ResolvePalette(\"custom\", %+v) = (%+v, %v), want (%+v, true)", custom, got, ok, custom)
	}
}

func TestResolvePaletteUnknownFallsBackToDefault(t *testing.T) {
	got, ok := ResolvePalette("not-a-theme", Palette{})
	if ok {
		t.Fatal("ResolvePalette(\"not-a-theme\", ...) ok = true, want false")
	}
	if want := DefaultPalette(); got != want {
		t.Fatalf("ResolvePalette(\"not-a-theme\", ...) = %+v, want %+v", got, want)
	}
}

func TestContrastCorrectedForegroundKeepsReadableColor(t *testing.T) {
	got := ContrastCorrectedForeground("#00d1ff", "#111018", "#f6f2ff")
	if want := "#00d1ff"; got != want {
		t.Fatalf("color = %q, want %q", got, want)
	}
}

func TestContrastCorrectedForegroundUsesFallbackForLowContrastColor(t *testing.T) {
	got := ContrastCorrectedForeground("#111111", "#111018", "#f6f2ff")
	if want := "#f6f2ff"; got != want {
		t.Fatalf("color = %q, want %q", got, want)
	}
}

func TestContrastCorrectedForegroundHandlesInvalidInput(t *testing.T) {
	got := ContrastCorrectedForeground("not-a-color", "#111018", "#f6f2ff")
	if want := "#f6f2ff"; got != want {
		t.Fatalf("color = %q, want %q", got, want)
	}
}

func TestGradientInterpolatesEndpointsAndMidpoint(t *testing.T) {
	got := Gradient("#ff8000", "#00c0ff", 3)
	want := []string{"#ff8000", "#80a080", "#00c0ff"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Gradient() = %v, want %v", got, want)
	}
}

func TestGradientDegradesSafelyForInvalidCustomColors(t *testing.T) {
	got := Gradient("accent", "#ffffff", 2)
	want := []string{"accent", "accent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Gradient() = %v, want %v", got, want)
	}
}

func TestSeamlessGradientMirrorsBothSidesAndClosesItsLoop(t *testing.T) {
	for _, steps := range []int{1, 2, 3, 8, 9} {
		colors := SeamlessGradient("#ff0000", "#0000ff", steps)
		if len(colors) != steps {
			t.Fatalf("SeamlessGradient(..., %d) length = %d, want %d", steps, len(colors), steps)
		}
		if colors[0] != colors[len(colors)-1] {
			t.Fatalf("SeamlessGradient(..., %d) endpoints = %q and %q, want a closed loop", steps, colors[0], colors[len(colors)-1])
		}
		for left, right := 0, len(colors)-1; left < right; left, right = left+1, right-1 {
			if colors[left] != colors[right] {
				t.Fatalf("SeamlessGradient(..., %d) is not mirrored at %d/%d: %q != %q", steps, left, right, colors[left], colors[right])
			}
		}
	}

	colors := SeamlessGradient("#ff0000", "#0000ff", 8)
	if colors[3] != "#0000ff" || colors[4] != "#0000ff" {
		t.Fatalf("even seamless gradient center = %q/%q, want the end color at both center cells", colors[3], colors[4])
	}
}

func TestDarkenAdjustsValidColorsAndPreservesInvalidValues(t *testing.T) {
	if got, want := Darken("#204060", 0.25), "#183048"; got != want {
		t.Fatalf("Darken() = %q, want %q", got, want)
	}
	if got, want := Darken("#204060", -1), "#204060"; got != want {
		t.Fatalf("Darken() with negative amount = %q, want %q", got, want)
	}
	if got, want := Darken("custom-background", 0.25), "custom-background"; got != want {
		t.Fatalf("Darken() invalid value = %q, want %q", got, want)
	}
}

func TestIdentityColorIsStableDistinctAndReadable(t *testing.T) {
	palette := DefaultPalette()
	backgrounds := []string{palette.Background, palette.Surface}
	alice := IdentityColor("Alice", backgrounds, palette.Foreground)
	if got := IdentityColor("alice", backgrounds, palette.Foreground); got != alice {
		t.Fatalf("case-normalized identity color = %q, want stable %q", got, alice)
	}
	bob := IdentityColor("bob", backgrounds, palette.Foreground)
	if bob == alice {
		t.Fatalf("different identities shared color %q", alice)
	}
	color, ok := parseHexColor(alice)
	if !ok {
		t.Fatalf("identity color %q is not valid hex", alice)
	}
	for _, value := range backgrounds {
		background, ok := parseHexColor(value)
		if !ok {
			t.Fatalf("test background %q is not valid hex", value)
		}
		if ratio := contrastRatio(color, background); ratio < minimumTextContrast {
			t.Fatalf("identity color contrast against %q = %.2f, want >= %.2f", value, ratio, minimumTextContrast)
		}
	}
	if got := IdentityColor("", backgrounds, palette.Foreground); got != palette.Foreground {
		t.Fatalf("empty identity color = %q, want fallback %q", got, palette.Foreground)
	}
}
