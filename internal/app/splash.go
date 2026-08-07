package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/animation"
)

const (
	splashProgressWidth  = 28
	splashTaglineText    = "twi // terminal Twitch chat"
	splashDecorativeText = "✦  expressive chat, zero browser chrome  ✦"
	// splashTaglineDelay holds the tagline blank long enough for the logo
	// to register before the typing starts.
	splashTaglineDelay = 240 * time.Millisecond
)

var splashLogo = []string{
	"████████╗██╗    ██╗██╗",
	"╚══██╔══╝██║    ██║██║",
	"   ██║   ██║ █╗ ██║██║",
	"   ██║   ██║███╗██║██║",
	"   ╚═╝   ╚══╝╚══╝╚═╝╚═╝",
}

// splashActive reports whether the animated startup splash should still cover
// the normal dashboard. Any keypress sets splashSkipped so users are never
// trapped waiting it out.
func (m mockShellModel) splashActive() bool {
	if m.splashUntil.IsZero() || m.splashSkipped {
		return false
	}
	return time.Now().Before(m.splashUntil)
}

func (m mockShellModel) splashView() string {
	return m.splashViewAt(time.Now())
}

// splashViewAt renders a staged, terminal-native boot sequence. The logo's
// accent gradient shimmers with the shared frame clock while the tagline types
// in and the progress head moves through named initialization phases. Keeping
// now explicit makes every animation state deterministic in tests while
// splashView remains the production wall-clock adapter.
func (m mockShellModel) splashViewAt(now time.Time) string {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)
	fraction := m.splashFraction(now)
	canvas := m.canvasBackground()

	contentWidth := splashContentWidth(width)
	logo := splashLogo
	if width < 38 || height < 13 {
		logo = []string{"╭── ✦ twi ✦ ──╮"}
	}
	logoWidth := widestLine(logo)
	phase := m.gradientPhase(clampMin(logoWidth, 1))

	logoLines := make([]string, 0, len(logo))
	for row, line := range logo {
		centered := centeredFittedLine(line, contentWidth)
		logoLines = append(logoLines, gradientForegroundText(
			centered,
			m.theme.Accent,
			m.gradientEndColor(),
			canvas,
			phase+row*2,
			true,
		))
	}

	elapsed := m.splashElapsed(now)
	taglineLine := m.splashTaglineLine(contentWidth, elapsed, canvas)
	decorativeLine := m.splashDecorativeLine(contentWidth, elapsed, canvas)
	blankLine := splashStyledLine(centeredFittedLine("", contentWidth), m.theme.Muted, canvas, false)
	progressWidth := minInt(splashProgressWidth, clampMin(contentWidth-2, 0))
	progressLine := gradientForegroundText(
		centeredFittedLine(splashProgressBar(fraction, progressWidth), contentWidth),
		m.theme.Accent,
		m.gradientEndColor(),
		canvas,
		phase,
		true,
	)
	phaseLine := splashStyledLine(centeredFittedLine(splashPhaseLabel(fraction, m.activeChannelName()), contentWidth), m.theme.Muted, canvas, false)
	lines := splashLinesForHeight(height, logoLines, taglineLine, decorativeLine, blankLine, progressLine, phaseLine)

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Background(lipgloss.Color(canvas)).
		Render(content)
}

func splashLinesForHeight(height int, logo []string, tagline, decorative, blank, progress, phase string) []string {
	if height <= 1 {
		return logo[:1]
	}
	if height == 2 {
		return []string{logo[0], phase}
	}
	if height == 3 {
		return []string{logo[0], progress, phase}
	}
	if height == 4 {
		return []string{logo[0], tagline, progress, phase}
	}
	if height == 5 {
		return []string{logo[0], tagline, decorative, progress, phase}
	}
	lines := make([]string, 0, len(logo)+5)
	lines = append(lines, logo...)
	lines = append(lines, tagline, decorative, blank, progress, phase)
	return lines
}

// splashTaglineLine types the tagline in behind a caret. The label is
// animated at its own width and centered afterward, so the words settle into
// their final position instead of sliding left as characters land.
//
// The step is set explicitly rather than taken from the effect default
// because the tagline has to finish inside splashDuration in every animation
// mode; a reduced-motion default would still be typing when the splash lifts.
func (m mockShellModel) splashTaglineLine(contentWidth int, elapsed time.Duration, canvas string) string {
	cfg := m.textEffectConfig(animation.EffectTypewriter)
	cfg.Bold = true
	cfg.Step = splashTaglineStep(m.animationMode)
	frame := animatedText(revealDisplayCells(splashTaglineText, contentWidth), cfg, elapsed-splashTaglineDelay, canvas)
	return centeredEffectLine(frame, contentWidth, canvas)
}

// splashDecorativeLine sweeps a highlight across the strapline while the
// tagline types, so the boot sequence has motion on both lines without two
// competing reveals.
func (m mockShellModel) splashDecorativeLine(contentWidth int, elapsed time.Duration, canvas string) string {
	cfg := m.textEffectConfig(animation.EffectShimmer)
	cfg.Base = m.theme.Muted
	frame := animatedText(revealDisplayCells(splashDecorativeText, contentWidth), cfg, elapsed, canvas)
	return centeredEffectLine(frame, contentWidth, canvas)
}

func splashTaglineStep(animationMode string) time.Duration {
	if animationMode == string(animation.ModeReduced) {
		return 55 * time.Millisecond
	}
	return 30 * time.Millisecond
}

// splashElapsed returns how long the splash has been on screen at now.
func (m mockShellModel) splashElapsed(now time.Time) time.Duration {
	if m.splashUntil.IsZero() {
		return 0
	}
	return splashDuration - m.splashUntil.Sub(now)
}

func (m mockShellModel) splashFraction(now time.Time) float64 {
	return clampFraction(float64(m.splashElapsed(now)) / float64(splashDuration))
}

func splashContentWidth(width int) int {
	if width <= 2 {
		return width
	}
	return minInt(width-2, 54)
}

func splashProgressBar(fraction float64, width int) string {
	if width <= 0 {
		return "◆"
	}
	filled := int(clampFraction(fraction) * float64(width))
	if filled >= width {
		return "[" + strings.Repeat("━", width) + "]"
	}
	return "[" + strings.Repeat("━", filled) + "◆" + strings.Repeat("·", width-filled-1) + "]"
}

func splashPhaseLabel(fraction float64, channel string) string {
	switch {
	case fraction < 0.22:
		return "◌ twi · loading palette"
	case fraction < 0.48:
		return "◐ warming animation clock"
	case fraction < 0.76:
		return "◓ composing chat surfaces"
	default:
		return "● ready for #" + normalizeChannelName(channel)
	}
}

func revealDisplayCells(value string, cells int) string {
	if cells <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		cluster := graphemes.Str()
		width := uniseg.StringWidth(cluster)
		if used+width > cells {
			break
		}
		builder.WriteString(cluster)
		used += width
	}
	return builder.String()
}

func widestLine(lines []string) int {
	widest := 0
	for _, line := range lines {
		widest = maxInt(widest, uniseg.StringWidth(line))
	}
	return widest
}

func splashStyledLine(text, foreground, background string, bold bool) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(foreground)).
		Background(lipgloss.Color(background)).
		Bold(bold).
		Render(text)
}

func clampFraction(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// centeredPlainLine center-pads plain (non-ANSI) text to width with spaces.
// Callers style the complete padded result so every cell carries the active
// terminal background instead of exposing transparent gaps after ANSI resets.
func centeredPlainLine(text string, width int) string {
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	total := width - textWidth
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func centeredFittedLine(text string, width int) string {
	return centeredPlainLine(strings.TrimRight(fitLine(text, width), " "), width)
}
