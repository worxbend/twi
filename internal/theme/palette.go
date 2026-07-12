package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Palette struct {
	Background string
	Foreground string
	Accent     string
	Muted      string
	Border     string
	Surface    string
	Warning    string
	Error      string
	Success    string
}

// DefaultPalette returns the "claude" preset, twi's default theme.
func DefaultPalette() Palette {
	palette, ok := Presets()["claude"]
	if !ok {
		panic("theme: claude preset missing")
	}
	return palette
}

// ResolvePalette returns the named preset, or custom when name is "custom".
// Unknown names fall back to DefaultPalette with ok=false so callers can warn
// without failing startup, matching the config package's lenient-fallback
// style for unrecognized flat-config values.
func ResolvePalette(name string, custom Palette) (Palette, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "custom" {
		return custom, true
	}
	if palette, ok := Presets()[name]; ok {
		return palette, true
	}
	return DefaultPalette(), false
}

const minimumTextContrast = 4.5

// ContrastCorrectedForeground returns a foreground color that is readable on
// background. Invalid foreground values return fallback unchanged.
func ContrastCorrectedForeground(foreground, background, fallback string) string {
	fg, ok := parseHexColor(foreground)
	if !ok {
		return fallback
	}
	bg, ok := parseHexColor(background)
	if !ok {
		return canonicalHex(fg)
	}
	if contrastRatio(fg, bg) >= minimumTextContrast {
		return canonicalHex(fg)
	}

	if candidate, ok := parseHexColor(fallback); ok && contrastRatio(candidate, bg) >= minimumTextContrast {
		return canonicalHex(candidate)
	}

	white := rgb{r: 255, g: 255, b: 255}
	black := rgb{}
	if contrastRatio(black, bg) > contrastRatio(white, bg) {
		return canonicalHex(black)
	}
	return canonicalHex(white)
}

type rgb struct {
	r uint8
	g uint8
	b uint8
}

func parseHexColor(value string) (rgb, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "#")
	if len(value) == 3 {
		value = string([]byte{
			value[0], value[0],
			value[1], value[1],
			value[2], value[2],
		})
	}
	if len(value) != 6 {
		return rgb{}, false
	}

	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{
		r: uint8(parsed >> 16),
		g: uint8(parsed >> 8),
		b: uint8(parsed),
	}, true
}

func canonicalHex(color rgb) string {
	return fmt.Sprintf("#%02x%02x%02x", color.r, color.g, color.b)
}

func contrastRatio(a, b rgb) float64 {
	la := relativeLuminance(a)
	lb := relativeLuminance(b)
	light := math.Max(la, lb)
	dark := math.Min(la, lb)
	return (light + 0.05) / (dark + 0.05)
}

func relativeLuminance(color rgb) float64 {
	return 0.2126*linearized(color.r) + 0.7152*linearized(color.g) + 0.0722*linearized(color.b)
}

func linearized(component uint8) float64 {
	value := float64(component) / 255
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}
