package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/theme"
)

// themeSettingsState drives the Ctrl+T btop-style theme picker: moving the
// selection live-previews a palette immediately (m.theme changes so every
// widget re-renders under it); Enter persists the choice to the effective
// config file; Esc reverts to the palette active before the view opened.
type themeSettingsState struct {
	open            bool
	selected        int
	originalName    string
	originalPalette theme.Palette
	saveError       string
}

// themeSettingsNames lists the selectable entries: every built-in preset in
// stable order, plus a trailing "custom" entry that previews whatever
// cfg.Features.ThemeCustom currently holds.
func themeSettingsNames() []string {
	names := append(append([]string(nil), theme.PresetNames()...), "custom")
	return names
}

func (m *mockShellModel) toggleThemeSettings() {
	if m.themeSettings.open {
		m.themeSettings = themeSettingsState{}
		return
	}
	m.closeOtherOverlays("theme")
	names := themeSettingsNames()
	selected := 0
	for i, name := range names {
		if strings.EqualFold(name, m.effectiveConfig.Features.ThemeName) {
			selected = i
			break
		}
	}
	m.themeSettings = themeSettingsState{
		open:            true,
		selected:        selected,
		originalName:    m.effectiveConfig.Features.ThemeName,
		originalPalette: m.theme,
	}
}

// handleThemeSettingsKey needs no explicit terminal-background side effect:
// View() re-derives the OSC 11 background sequence from m.theme on every
// render (see themeBackgroundSequence), so changing m.theme here is enough
// to keep the terminal background in sync with the live preview.
func (m mockShellModel) handleThemeSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.theme = m.themeSettings.originalPalette
		m.themeSettings = themeSettingsState{}
		return m, nil
	case tea.KeyEnter:
		return m.persistSelectedTheme()
	case tea.KeyUp:
		m.moveThemeSettingsSelection(-1)
	case tea.KeyDown, tea.KeyTab:
		m.moveThemeSettingsSelection(1)
	}
	return m, nil
}

func (m *mockShellModel) moveThemeSettingsSelection(delta int) {
	names := themeSettingsNames()
	if len(names) == 0 {
		return
	}
	selected := m.themeSettings.selected + delta
	if selected < 0 {
		selected = len(names) - 1
	}
	if selected >= len(names) {
		selected = 0
	}
	m.themeSettings.selected = selected
	m.theme, _ = theme.ResolvePalette(names[selected], m.effectiveConfig.Features.ThemeCustom)
}

// persistSelectedTheme writes the previewed theme to the effective config
// file, preserving every other setting already in effect (it round-trips
// the full config, not just the theme fields, so nothing else is reset).
func (m mockShellModel) persistSelectedTheme() (tea.Model, tea.Cmd) {
	names := themeSettingsNames()
	selected := m.themeSettings.selected
	if selected < 0 || selected >= len(names) {
		m.themeSettings = themeSettingsState{}
		return m, nil
	}
	cfg := m.effectiveConfig
	cfg.Features.ThemeName = names[selected]
	if err := config.WriteNonSecretFile(cfg.Path, cfg); err != nil {
		m.themeSettings.saveError = "save failed: " + config.RedactDisplayValue(err.Error())
		return m, nil
	}
	m.effectiveConfig = cfg
	m.themeSettings = themeSettingsState{}
	return m, nil
}

func (m mockShellModel) themeSettingsView(layout mockShellLayout) string {
	contentWidth := layout.width
	if layout.themeSettingsFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.themeSettingsLines(contentWidth, layout.themeSettingsContentHeight)
	content := strings.Join(lines, "\n")
	if !layout.themeSettingsFramed {
		return fitBlock(content, layout.width, layout.themeSettingsHeight)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.themeSettingsContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(m.theme.Accent)).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) themeSettingsLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	header := " Theme (enter=save, esc=cancel)"
	if m.themeSettings.saveError != "" {
		header = " Theme: " + m.themeSettings.saveError
	}
	lines := []string{fitLine(header, width)}
	if height == 1 {
		return lines
	}

	names := themeSettingsNames()
	selected := m.themeSettings.selected
	if selected < 0 || selected >= len(names) {
		selected = 0
	}
	maxNames := height - 1
	start := paletteWindowStart(selected, len(names), maxNames)
	for i := start; i < len(names) && len(lines) < height; i++ {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		label := prefix + names[i]
		if strings.EqualFold(names[i], m.effectiveConfig.Features.ThemeName) {
			label += " (active)"
		}
		lines = append(lines, fitLine(label, width))
	}
	for len(lines) < height {
		lines = append(lines, fitLine("", width))
	}
	return lines[:height]
}
