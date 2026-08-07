package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/animation"
)

// textEffectConfig builds a palette-aware config for one animated chrome
// label. Callers override Step, Width, Offset, and Bold for their surface;
// the colors and the app-wide animation mode come from the model so a theme
// change or animation=off applies everywhere without per-call plumbing.
func (m shellModel) textEffectConfig(effect animation.TextEffect) animation.TextConfig {
	return animation.TextConfig{
		Effect: effect,
		Mode:   animation.Mode(m.animationMode),
		Base:   m.theme.Foreground,
		Accent: m.theme.Accent,
		Trail:  m.theme.Muted,
	}
}

// frameElapsed is the elapsed value the continuous text effects read. It
// comes from the shared animation clock's last tick, never the wall clock, so
// View() stays pure and every frame is reproducible in tests.
func (m shellModel) frameElapsed() time.Duration {
	return animation.FrameElapsed(m.lastFrameAt)
}

// animatedText renders one frame of an effect as a styled string on
// background. Each cell carries the background explicitly because a styled
// span ends in an ANSI reset: without it, the terminal's own background would
// show through the gaps between colored runs.
//
// Pass the label itself, truncated with revealDisplayCells rather than padded
// with fitLine, and pad the result with centeredEffectLine or
// paddedEffectLine. Animating a padded row would spread the effect across the
// blank cells instead of the words.
func animatedText(text string, cfg animation.TextConfig, elapsed time.Duration, background string) string {
	return renderTextCells(animation.TextFrame(text, cfg, elapsed), background)
}

func renderTextCells(cells []animation.TextCell, background string) string {
	var builder strings.Builder
	for _, cell := range cells {
		style := lipgloss.NewStyle().Background(lipgloss.Color(background)).Bold(cell.Bold)
		if cell.Foreground != "" {
			style = style.Foreground(lipgloss.Color(cell.Foreground))
		}
		builder.WriteString(style.Render(cell.Text))
	}
	return builder.String()
}

// centeredEffectLine centers already-styled effect output inside width and
// paints the padding with background. Padding a styled span with plain spaces
// would leave unstyled cells between the span's reset and the next escape
// sequence, the same gap terminal_background_test guards against elsewhere.
func centeredEffectLine(styled string, width int, background string) string {
	pad := width - lipgloss.Width(styled)
	if pad <= 0 {
		return styled
	}
	left := pad / 2
	return backgroundSpaces(left, background) + styled + backgroundSpaces(pad-left, background)
}

// paddedEffectLine extends styled effect output to width with background
// cells, the left-aligned counterpart to centeredEffectLine. Rows that share
// a pane with fitLine-padded static rows need it so the surface color reaches
// the pane edge on every row.
func paddedEffectLine(styled string, width int, background string) string {
	return styled + backgroundSpaces(width-lipgloss.Width(styled), background)
}

func backgroundSpaces(count int, background string) string {
	if count <= 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(background)).
		Render(strings.Repeat(" ", count))
}
