package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/theme"
)

func (m mockShellModel) gradientPhase(width int) int {
	if width <= 0 || m.animationMode == string(animation.ModeOff) || m.lastFrameAt.IsZero() {
		return 0
	}
	frameMillis := int64(200)
	if m.animationMode == string(animation.ModeReduced) {
		frameMillis = 400
	}
	return int(m.lastFrameAt.UnixMilli()/frameMillis) % width
}

// gradientEndColor keeps decorative gradients visible when a colorful theme
// intentionally reuses Accent for Success. Monochrome palettes remain solid
// so their absence of hue is preserved rather than silently colorized.
func (m mockShellModel) gradientEndColor() string {
	if !strings.EqualFold(m.theme.Accent, m.theme.Success) {
		return m.theme.Success
	}
	if !strings.EqualFold(m.theme.Accent, m.theme.Foreground) {
		return m.theme.Warning
	}
	return m.theme.Success
}

func gradientBackgroundLine(value string, width int, start, end, preferredForeground, fallbackForeground string, phase int, bold bool) string {
	if width <= 0 {
		return ""
	}
	plain := fitLine(value, width)
	colors := theme.SeamlessGradient(start, end, width)
	if len(colors) == 0 {
		return plain
	}
	var builder strings.Builder
	cell := 0
	graphemes := uniseg.NewGraphemes(plain)
	for graphemes.Next() {
		cluster := graphemes.Str()
		color := colors[(cell+phase)%len(colors)]
		foreground := theme.ContrastCorrectedForeground(preferredForeground, color, fallbackForeground)
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(foreground)).
			Background(lipgloss.Color(color)).
			Bold(bold)
		builder.WriteString(style.Render(cluster))
		cell += uniseg.StringWidth(cluster)
	}
	return builder.String()
}

func gradientForegroundText(value, start, end, background string, phase int, bold bool) string {
	width := uniseg.StringWidth(value)
	if width <= 0 {
		return ""
	}
	colors := theme.SeamlessGradient(start, end, width)
	var builder strings.Builder
	cell := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		cluster := graphemes.Str()
		color := colors[(cell+phase)%len(colors)]
		builder.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Background(lipgloss.Color(background)).
			Bold(bold).
			Render(cluster))
		cell += uniseg.StringWidth(cluster)
	}
	return builder.String()
}
