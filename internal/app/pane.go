package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
)

const canvasDarkenAmount = 0.14

type paneSpec struct {
	icon          string
	title         string
	content       string
	width         int
	contentHeight int
	padding       int
	accent        string
	focused       bool
}

func (m mockShellModel) canvasBackground() string {
	return theme.Darken(m.theme.Background, canvasDarkenAmount)
}

// renderPane builds an exact-size panel whose title occupies the existing top
// border row. This preserves layout capacity while giving every pane a surface
// fill, icon-bearing title, quiet frame, and independently colored left rail.
func (m mockShellModel) renderPane(spec paneSpec) string {
	if spec.width <= 0 || spec.contentHeight < 0 {
		return ""
	}
	if spec.accent == "" {
		spec.accent = m.theme.Accent
	}
	if spec.padding < 0 {
		spec.padding = 0
	}

	railColor := spec.accent
	if spec.focused {
		colors := theme.SeamlessGradient(spec.accent, m.paneGradientEnd(spec.accent), 12)
		if len(colors) > 0 {
			railColor = colors[m.gradientPhase(len(colors))%len(colors)]
		}
	}
	title := m.paneTitleLine(spec.width, spec.icon, spec.title, spec.accent, railColor, spec.focused)
	body := lipgloss.NewStyle().
		Width(clampMin(spec.width-2, 0)).
		Height(spec.contentHeight).
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Background(lipgloss.Color(m.theme.Surface)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderRight(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderForeground(
			lipgloss.Color(m.theme.Border),
			lipgloss.Color(m.theme.Border),
			lipgloss.Color(m.theme.Border),
			lipgloss.Color(railColor),
		).
		BorderBackground(lipgloss.Color(m.canvasBackground())).
		Padding(0, spec.padding).
		Render(spec.content)
	return title + "\n" + body
}

func (m mockShellModel) paneTitleLine(width int, icon, title, accent, railColor string, focused bool) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return paneStyledText("│", railColor, m.canvasBackground(), true)
	}

	innerWidth := width - 2
	label := strings.TrimSpace(strings.TrimSpace(icon) + " " + strings.TrimSpace(title))
	label = revealDisplayCells(label, clampMin(innerWidth-3, 0))
	prefix := "─ "
	if label == "" || innerWidth < 3 {
		prefix = ""
	}
	labelText := revealDisplayCells(prefix+label+" ", innerWidth)
	labelWidth := uniseg.StringWidth(labelText)
	remainder := strings.Repeat("─", clampMin(innerWidth-labelWidth, 0))

	left := paneStyledText("┌", railColor, m.canvasBackground(), true)
	var styledLabel string
	if focused {
		styledLabel = gradientForegroundText(
			labelText,
			accent,
			m.paneGradientEnd(accent),
			m.canvasBackground(),
			m.gradientPhase(clampMin(labelWidth, 1)),
			true,
		)
	} else {
		styledLabel = paneStyledText(labelText, accent, m.canvasBackground(), true)
	}
	border := paneStyledText(remainder+"┐", m.theme.Border, m.canvasBackground(), false)
	return left + styledLabel + border
}

func (m mockShellModel) paneGradientEnd(start string) string {
	for _, candidate := range []string{
		m.theme.Success,
		m.theme.Warning,
		m.theme.Accent,
		m.theme.Error,
		m.theme.Foreground,
	} {
		if candidate != "" && !strings.EqualFold(candidate, start) {
			return candidate
		}
	}
	return start
}

func paneStyledText(text, foreground, background string, bold bool) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(foreground)).
		Background(lipgloss.Color(background)).
		Bold(bold).
		Render(text)
}
