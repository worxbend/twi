package theme

import "testing"

// Presets hands out a copy, so a caller that writes into the map it gets back
// cannot change what the next caller sees.
func TestPresetsReturnsACopy(t *testing.T) {
	before := Presets()["claude"]

	tampered := Presets()
	tampered["claude"] = Palette{Background: "#000000"}
	delete(tampered, "nord")

	if got := Presets()["claude"]; got != before {
		t.Fatalf("Presets()[\"claude\"] = %+v after a caller overwrote its copy, want %+v", got, before)
	}
	if _, ok := Presets()["nord"]; !ok {
		t.Fatal("Presets() lost \"nord\" after a caller deleted it from its copy")
	}
}

// DefaultPalette no longer looks its answer up in the preset map, so this
// pins the two to the same colors.
func TestDefaultPaletteMatchesClaudePreset(t *testing.T) {
	if got, want := Presets()["claude"], DefaultPalette(); got != want {
		t.Fatalf("Presets()[\"claude\"] = %+v, want DefaultPalette() = %+v", got, want)
	}
}
