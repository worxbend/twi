package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const splashProgressWidth = 24

// splashActive reports whether the ~2s animated startup splash should still
// cover the normal dashboard. Any keypress sets splashSkipped so users are
// never trapped waiting it out.
func (m mockShellModel) splashActive() bool {
	if m.splashUntil.IsZero() || m.splashSkipped {
		return false
	}
	return time.Now().Before(m.splashUntil)
}

// splashView renders the startup splash: a centered wordmark, tagline, and a
// progress bar that fills as the splash's ~2s window elapses. Every visible
// cell uses the active theme, matching the rest of the dashboard.
func (m mockShellModel) splashView() string {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)

	elapsed := splashDuration - time.Until(m.splashUntil)
	fraction := float64(elapsed) / float64(splashDuration)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction * splashProgressWidth)
	bar := strings.Repeat("#", filled) + strings.Repeat("-", splashProgressWidth-filled)

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted))

	content := lipgloss.JoinVertical(lipgloss.Center,
		accent.Render("twi"),
		muted.Render("terminal Twitch chat"),
		"",
		muted.Render("["+bar+"]"),
	)

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Background(lipgloss.Color(m.theme.Background)).
		Render(content)
}
