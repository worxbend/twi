package app

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// switchToTab activates tab, closing any open overlay so the new screen
// isn't obscured, and kicks off that screen's data load the first time it's
// opened. Switching to the already-active tab is a no-op.
func (m shellModel) switchToTab(tab shellTab) (shellModel, tea.Cmd) {
	if m.activeTab == tab {
		return m, nil
	}
	m.closeOtherOverlays("")
	m.activeTab = tab
	m.clampScroll()
	switch tab {
	case tabStreamInfo:
		return m, m.scheduleStreamInfoLoad()
	case tabMisc:
		return m, m.scheduleMiscLoad()
	}
	return m, nil
}

// tabBarLine renders the fixed one-row tab strip shown above the status
// line: one label per entry in shellTabs, tagged with its Alt+<digit>
// shortcut, active tab marked with a leading "*", and the configured Twitch
// login plus active chat aligned on the right when space permits. Built as
// plain text and fit/padded with fitLine (like every other region) before a
// single style wraps the whole line, since fitLine itself is not ANSI-aware.
func (m shellModel) tabBarLine(width int) string {
	username, channel := m.tabBarContextParts()
	context := strings.Join(nonEmptyStrings(username, channel), "  ")
	tabs := m.tabBarTabs(false)
	if uniseg.StringWidth(tabs)+2+uniseg.StringWidth(context) > width {
		tabs = m.tabBarTabs(true)
	}
	if uniseg.StringWidth(tabs)+2+uniseg.StringWidth(context) > width {
		tabs = m.activeTabLabel()
	}

	line := tabs
	available := width - uniseg.StringWidth(tabs)
	if context != "" && available > 0 {
		contextWidth := available
		if available > 2 {
			contextWidth -= 2
		}
		visibleContext := tabBarContextForWidth(username, channel, contextWidth)
		gap := available - uniseg.StringWidth(visibleContext)
		line += strings.Repeat(" ", gap) + visibleContext
	}
	line = fitLine(line, width)
	return gradientBackgroundLine(
		line,
		width,
		m.theme.Accent,
		m.gradientEndColor(),
		m.theme.Foreground,
		m.theme.Background,
		m.gradientPhase(width),
		true,
	)
}

func (m shellModel) tabBarTabs(compact bool) string {
	parts := make([]string, 0, len(shellTabs))
	for i, entry := range shellTabs {
		marker := ""
		if compact {
			if entry.tab == m.activeTab {
				marker = "*"
			}
		} else {
			marker = " "
			if entry.tab == m.activeTab {
				marker = "*"
			}
		}
		label := fmt.Sprintf("%s%d", marker, i+1)
		if !compact {
			label += ":" + entry.label
		}
		parts = append(parts, label)
	}
	return " " + strings.Join(parts, "  ")
}

func (m shellModel) activeTabLabel() string {
	for i, entry := range shellTabs {
		if entry.tab == m.activeTab {
			return fmt.Sprintf(" *%d", i+1)
		}
	}
	return ""
}

func (m shellModel) tabBarContextParts() (string, string) {
	username := sanitizeTabBarValue(m.effectiveConfig.Twitch.Username)
	if username != "" {
		username = "@" + username
	}
	channel := strings.TrimPrefix(sanitizeTabBarValue(m.activeChannelName()), "#")
	if channel != "" {
		channel = "#" + channel
	}
	return username, channel
}

func tabBarContextForWidth(username, channel string, width int) string {
	if width <= 0 {
		return ""
	}
	channelWidth := uniseg.StringWidth(channel)
	if channelWidth >= width {
		return truncateDisplayWidth(channel, width)
	}
	if username == "" {
		return channel
	}
	usernameWidth := width - channelWidth - 2
	if usernameWidth <= 0 {
		return channel
	}
	return truncateDisplayWidth(username, usernameWidth) + "  " + channel
}

func truncateDisplayWidth(value string, width int) string {
	return strings.TrimRight(fitLine(value, width), " ")
}

func sanitizeTabBarValue(value string) string {
	value = strings.TrimSpace(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, value)
}
