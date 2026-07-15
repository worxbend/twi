package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// switchToTab activates tab, closing any open overlay so the new screen
// isn't obscured, and kicks off that screen's data load the first time it's
// opened. Switching to the already-active tab is a no-op.
func (m mockShellModel) switchToTab(tab shellTab) (tea.Model, tea.Cmd) {
	if m.activeTab == tab {
		return m, nil
	}
	m.closeOtherOverlays("")
	m.activeTab = tab
	m.clampScroll()
	if tab == tabStreamInfo {
		return m, m.scheduleStreamInfoLoad()
	}
	return m, nil
}

// tabBarLine renders the fixed one-row tab strip shown above the status
// line: one label per entry in shellTabs, tagged with its Alt+<digit>
// shortcut, active tab marked with a leading "*". Built as plain text and
// fit/padded with fitLine (like every other region) before a single style
// wraps the whole line, since fitLine itself is not ANSI-aware.
func (m mockShellModel) tabBarLine(width int) string {
	parts := make([]string, 0, len(shellTabs))
	for i, entry := range shellTabs {
		marker := " "
		if entry.tab == m.activeTab {
			marker = "*"
		}
		parts = append(parts, fmt.Sprintf("%s%d:%s", marker, i+1, entry.label))
	}
	line := fitLine(" "+strings.Join(parts, "  "), width)
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color(m.theme.Muted)).
		Background(lipgloss.Color(m.theme.Surface)).
		Render(line)
}
