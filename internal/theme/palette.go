package theme

import (
	"fmt"
	"hash/fnv"
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

// Gradient returns steps colors interpolated between start and end. Invalid
// endpoints degrade to the original start value so visual enhancements never
// make a custom theme unusable.
func Gradient(start, end string, steps int) []string {
	if steps <= 0 {
		return nil
	}
	from, fromOK := parseHexColor(start)
	to, toOK := parseHexColor(end)
	colors := make([]string, steps)
	if !fromOK || !toOK {
		for i := range colors {
			colors[i] = start
		}
		return colors
	}
	if steps == 1 {
		colors[0] = canonicalHex(from)
		return colors
	}
	for i := range colors {
		fraction := float64(i) / float64(steps-1)
		colors[i] = canonicalHex(rgb{
			r: interpolateComponent(from.r, to.r, fraction),
			g: interpolateComponent(from.g, to.g, fraction),
			b: interpolateComponent(from.b, to.b, fraction),
		})
	}
	return colors
}

// SeamlessGradient returns a mirrored start-to-end-to-start palette. The
// first and last entries match, so consumers can rotate through the palette
// without exposing a hard end-to-start seam at the wrap point.
func SeamlessGradient(start, end string, steps int) []string {
	if steps <= 0 {
		return nil
	}
	forwardLength := steps/2 + steps%2
	forward := Gradient(start, end, forwardLength)
	colors := make([]string, 0, steps)
	colors = append(colors, forward...)

	reverseStart := len(forward) - 1
	if steps%2 == 1 {
		reverseStart--
	}
	for index := reverseStart; index >= 0; index-- {
		colors = append(colors, forward[index])
	}
	return colors
}

// Darken reduces a valid hex color's RGB components by amount. Amount is
// clamped to [0,1], and invalid custom-theme values are returned unchanged so
// decorative canvas treatment cannot make a theme unusable.
func Darken(color string, amount float64) string {
	parsed, ok := parseHexColor(color)
	if !ok {
		return color
	}
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}
	factor := 1 - amount
	return canonicalHex(rgb{
		r: uint8(math.Round(float64(parsed.r) * factor)),
		g: uint8(math.Round(float64(parsed.g) * factor)),
		b: uint8(math.Round(float64(parsed.b) * factor)),
	})
}

// IdentityColor returns a deterministic, random-looking color for identity
// that meets text contrast against every valid background when possible. It
// keeps nickname colors stable across frames and sessions without mutable UI
// state. Empty identities and impossible custom palettes use fallback.
func IdentityColor(identity string, backgrounds []string, fallback string) string {
	identity = strings.ToLower(strings.TrimSpace(identity))
	if identity == "" {
		return fallback
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(identity))
	hash := hasher.Sum64()
	hue := float64(hash%360) / 360
	saturation := 0.62 + float64((hash>>12)%18)/100

	parsedBackgrounds := make([]rgb, 0, len(backgrounds))
	lightCanvas := false
	for _, value := range backgrounds {
		background, ok := parseHexColor(value)
		if !ok {
			continue
		}
		parsedBackgrounds = append(parsedBackgrounds, background)
		if relativeLuminance(background) > 0.45 {
			lightCanvas = true
		}
	}
	if len(parsedBackgrounds) == 0 {
		return fallback
	}

	lightnesses := []float64{0.68, 0.74, 0.80, 0.86}
	if lightCanvas {
		lightnesses = []float64{0.34, 0.28, 0.22, 0.16}
	}
	for _, lightness := range lightnesses {
		candidate := hslColor(hue, saturation, lightness)
		readable := true
		for _, background := range parsedBackgrounds {
			if contrastRatio(candidate, background) < minimumTextContrast {
				readable = false
				break
			}
		}
		if readable {
			return canonicalHex(candidate)
		}
	}
	return fallback
}

func hslColor(hue, saturation, lightness float64) rgb {
	c := (1 - math.Abs(2*lightness-1)) * saturation
	h := hue * 6
	x := c * (1 - math.Abs(math.Mod(h, 2)-1))
	var r, g, b float64
	switch {
	case h < 1:
		r, g = c, x
	case h < 2:
		r, g = x, c
	case h < 3:
		g, b = c, x
	case h < 4:
		g, b = x, c
	case h < 5:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := lightness - c/2
	return rgb{
		r: uint8(math.Round((r + m) * 255)),
		g: uint8(math.Round((g + m) * 255)),
		b: uint8(math.Round((b + m) * 255)),
	}
}

func interpolateComponent(start, end uint8, fraction float64) uint8 {
	return uint8(math.Round(float64(start) + (float64(end)-float64(start))*fraction))
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
