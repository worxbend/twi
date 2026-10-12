package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m shellModel) View() tea.View {
	view := tea.NewView(m.viewContent())
	view.AltScreen = true
	view.ReportFocus = true
	if m.terminalOutput != nil {
		view.BackgroundColor = lipgloss.Color(m.canvasBackground())
	}
	if m.mouseEnabled {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

func (m shellModel) viewContent() string {
	if m.splashActive() {
		return m.splashView()
	}
	// The theme picker is a full-screen page, not a docked strip: it replaces
	// the dashboard the same way the splash does.
	if m.themeSettings.open {
		return m.themeSettingsPageView()
	}
	layout := m.layout()

	regions := make([]string, 0, 4)
	if layout.tabBarHeight > 0 {
		regions = append(regions, m.tabBarLine(layout.width))
	}
	if layout.statusHeight > 0 {
		regions = append(regions, m.statusLine(layout.width))
	}
	if layout.chatHeight > 0 {
		chat := m.chatView(layout)
		if layout.sidebarWidth > 0 {
			chat = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(layout), chat)
		}
		if layout.activityWidth > 0 {
			chat = lipgloss.JoinHorizontal(lipgloss.Top, chat, m.activityLogView(layout))
		}
		regions = append(regions, chat)
	}
	if layout.streamInfoHeight > 0 {
		regions = append(regions, m.streamInfoView(layout))
	}
	if layout.miscHeight > 0 {
		regions = append(regions, m.miscView(layout))
	}
	if layout.paletteHeight > 0 {
		regions = append(regions, m.commandPaletteView(layout))
	}
	if layout.inspectHeight > 0 {
		regions = append(regions, m.inspectView(layout))
	}
	if layout.emotePickerHeight > 0 {
		regions = append(regions, m.emotePickerView(layout))
	}
	if layout.channelPickerHeight > 0 {
		regions = append(regions, m.channelPickerView(layout))
	}
	if layout.categoryPickerHeight > 0 {
		regions = append(regions, m.categoryPickerView(layout))
	}
	if layout.composerHeight > 0 {
		regions = append(regions, m.composerView(layout))
	}
	if layout.helpHeight > 0 {
		regions = append(regions, m.helpView(layout.width, layout.helpHeight))
	}

	joined := lipgloss.JoinVertical(lipgloss.Left, regions...)
	rendered := lipgloss.NewStyle().
		Width(layout.width).
		Height(clampMin(m.height, 1)).
		Background(lipgloss.Color(m.canvasBackground())).
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Render(joined)
	return rendered
}
