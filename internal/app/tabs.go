package app

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
)

// switchToTab activates tab, closing any open overlay so the new screen
// isn't obscured, and kicks off that screen's data load the first time it's
// opened. Switching to the already-active tab is a no-op.
func (m shellModel) switchToTab(tab shellTab) (shellModel, tea.Cmd) {
	if m.activeTab == tab {
		return m, nil
	}
	m.closeOtherOverlays(overlayNone)
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
// line: one pill per entry in shellTabs, tagged with its Alt+<digit>
// shortcut, the active tab lit as a solid accent pill that shimmers with the
// shared frame clock, and the configured Twitch login plus active chat
// aligned on the right when space permits.
//
// The line is assembled as plain text first -- the same run tabAtMouse
// measures -- and then colored by cell spans via styleLineSpans, so the drawn
// layout and the hit-tested layout cannot drift apart. The active pill
// extends one cell into the surrounding separator spaces, which changes only
// background color, never glyphs.
func (m shellModel) tabBarLine(width int) string {
	if width <= 0 {
		return ""
	}
	tokens := m.tokens()
	username, channel := m.tabBarContextParts()
	context := strings.Join(nonEmptyStrings(username, channel), "  ")
	labels := m.tabBarLabelsForWidth(context, width)

	plain := " " + strings.Join(labels, "  ")
	available := width - uniseg.StringWidth(plain)
	contextStart := -1
	visibleContext := ""
	if context != "" && available > 0 {
		contextWidth := available
		if available > 2 {
			contextWidth -= 2
		}
		visibleContext = tabBarContextForWidth(username, channel, contextWidth)
		gap := available - uniseg.StringWidth(visibleContext)
		contextStart = uniseg.StringWidth(plain) + gap
		plain += strings.Repeat(" ", gap) + visibleContext
	}

	pillColor := tokens.Accent
	if colors := theme.SeamlessGradient(tokens.Accent, m.gradientEndColor(), 12); len(colors) > 0 {
		pillColor = colors[m.gradientPhase(len(colors))%len(colors)]
	}
	pill := run{
		foreground: theme.ContrastCorrectedForeground(tokens.OnAccent, pillColor, tokens.Text),
		background: pillColor,
		bold:       true,
	}

	var spans []lineSpan
	position := 1 // the run's leading space
	for _, label := range labels {
		labelWidth := uniseg.StringWidth(label)
		if strings.HasPrefix(label, "*") {
			start := max(position-1, 0)
			end := min(position+labelWidth+1, uniseg.StringWidth(plain))
			spans = append(spans, lineSpan{start: start, end: end, piece: pill})
		}
		position += labelWidth + 2
	}
	if contextStart >= 0 {
		contextWidth := uniseg.StringWidth(visibleContext)
		channelWidth := uniseg.StringWidth(channel)
		if channel != "" && contextWidth >= channelWidth {
			spans = append(spans, lineSpan{
				start: contextStart + contextWidth - channelWidth,
				end:   contextStart + contextWidth,
				piece: run{foreground: tokens.Text, background: tokens.Track, bold: true},
			})
		}
	}

	return styleLineSpans(plain, width, run{foreground: tokens.Muted, background: tokens.Track}, spans...)
}

// tabBarLabelsForWidth picks the tab label set tabBarLine draws at width,
// falling back from full labels to compact numbers to just the active tab as
// the row runs out of room. tabAtMouse measures the same run so its hit boxes
// cannot drift from what is drawn.
func (m shellModel) tabBarLabelsForWidth(context string, width int) []string {
	labels := m.tabBarLabels(false)
	if uniseg.StringWidth(" "+strings.Join(labels, "  "))+2+uniseg.StringWidth(context) > width {
		labels = m.tabBarLabels(true)
	}
	if uniseg.StringWidth(" "+strings.Join(labels, "  "))+2+uniseg.StringWidth(context) > width {
		labels = []string{m.activeTabMarker()}
	}
	return labels
}

// tabBarTabsForWidth is the plain-text projection of tabBarLabelsForWidth
// kept for tabAtMouse, which measures the run as a single string.
func (m shellModel) tabBarTabsForWidth(context string, width int) string {
	return " " + strings.Join(m.tabBarLabelsForWidth(context, width), "  ")
}

func (m shellModel) tabBarLabels(compact bool) []string {
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
	return parts
}

func (m shellModel) activeTabMarker() string {
	for i, entry := range shellTabs {
		if entry.tab == m.activeTab {
			return fmt.Sprintf("*%d", i+1)
		}
	}
	return ""
}

func (m shellModel) activeTabLabel() string {
	return " " + m.activeTabMarker()
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
