package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/theme"
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

// themeSwatchCell is one swatch column. Every entry draws the same number of
// cells so the color columns line up row-for-row down the page.
const (
	themeSwatchCell      = "██"
	themeSwatchEmptyCell = "··"
)

// themeSettingsNames lists the selectable entries: every built-in preset in
// stable order, plus a trailing "custom" entry that previews whatever
// cfg.Features.ThemeCustom currently holds.
func themeSettingsNames() []string {
	names := append(append([]string(nil), theme.PresetNames()...), "custom")
	return names
}

func (m *shellModel) toggleThemeSettings() {
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
func (m shellModel) handleThemeSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case tea.KeyHome:
		m.setThemeSettingsSelection(0)
	case tea.KeyEnd:
		m.setThemeSettingsSelection(len(themeSettingsNames()) - 1)
	}
	return m, nil
}

func (m *shellModel) moveThemeSettingsSelection(delta int) {
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
	m.setThemeSettingsSelection(selected)
}

func (m *shellModel) setThemeSettingsSelection(selected int) {
	names := themeSettingsNames()
	if selected < 0 || selected >= len(names) {
		return
	}
	m.themeSettings.selected = selected
	m.theme, _ = theme.ResolvePalette(names[selected], m.effectiveConfig.Features.ThemeCustom)
}

// persistSelectedTheme writes the previewed theme to the effective config
// file, preserving every other setting already in effect (it round-trips
// the full config, not just the theme fields, so nothing else is reset).
func (m shellModel) persistSelectedTheme() (tea.Model, tea.Cmd) {
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

// themeSettingsPageView renders the picker as its own full-screen page rather
// than a strip docked under the chat. Sizing off m.width/m.height (the splash
// pattern) means View() returns this instead of the dashboard, so the palette
// never has to be judged through a chat pane squeezed down to make room for
// it, and the list gets the whole terminal for swatches.
func (m shellModel) themeSettingsPageView() string {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)
	if height < 3 || width < 5 {
		lines := m.themeSettingsLines(width, height, m.canvasBackground())
		return fitBlock(strings.Join(lines, "\n"), width, height)
	}
	contentHeight := height - 2
	lines := m.themeSettingsLines(clampMin(width-4, 1), contentHeight, m.theme.Surface)
	return m.renderPane(paneSpec{
		icon:          "🎨",
		title:         "Themes",
		content:       strings.Join(lines, "\n"),
		width:         width,
		contentHeight: contentHeight,
		padding:       1,
		accent:        m.theme.Accent,
		focused:       true,
	})
}

// themeSettingsLines lays out the page body: a status header, the windowed
// entry list, and a key-hint footer. Lines are pre-styled against background
// because the swatches need per-segment color, which the pane body style
// cannot apply on its own.
func (m shellModel) themeSettingsLines(width, height int, background string) []string {
	if height <= 0 || width <= 0 {
		return nil
	}
	names := themeSettingsNames()
	selected := m.themeSettings.selected
	if selected < 0 || selected >= len(names) {
		selected = 0
	}

	header, headerColor := "Select a theme — the preview applies live", m.theme.Muted
	if m.themeSettings.saveError != "" {
		header, headerColor = m.themeSettings.saveError, m.theme.Error
	}
	lines := []string{paneStyledText(fitLine(header, width), headerColor, background, false)}
	if height == 1 {
		return lines
	}

	blank := paneStyledText(fitLine("", width), m.theme.Muted, background, false)
	footer := ""
	if height >= 4 {
		footer = paneStyledText(
			fitLine("↑/↓ move · home/end jump · enter save · esc cancel", width),
			m.theme.Muted,
			background,
			false,
		)
	}
	if height >= 7 {
		lines = append(lines, blank)
	}

	listHeight := height - len(lines)
	if footer != "" {
		listHeight--
	}
	start := paletteWindowStart(selected, len(names), listHeight)
	for i := start; i < len(names) && listHeight > 0; i++ {
		lines = append(lines, m.themeSettingsEntryLine(names[i], width, i == selected, background))
		listHeight--
	}
	for listHeight > 0 {
		lines = append(lines, blank)
		listHeight--
	}
	if footer != "" {
		lines = append(lines, footer)
	}
	return lines
}

// themeSettingsEntryLine draws one row: selection marker, theme name, and a
// swatch strip rendered in that preset's own colors so the list doubles as a
// palette comparison without having to select each entry in turn.
func (m shellModel) themeSettingsEntryLine(name string, width int, selected bool, background string) string {
	if width <= 0 {
		return ""
	}

	prefix, prefixColor := "  ", m.theme.Muted
	if selected {
		prefix, prefixColor = "❯ ", m.theme.Accent
	}
	label := name
	if strings.EqualFold(name, m.effectiveConfig.Features.ThemeName) {
		label += " (active)"
	}

	var builder strings.Builder
	used := 0
	write := func(text, color string, bold bool) {
		text = fitLine(text, minInt(uniseg.StringWidth(text), clampMin(width-used, 0)))
		if text == "" {
			return
		}
		builder.WriteString(paneStyledText(text, color, background, bold))
		used += uniseg.StringWidth(text)
	}

	write(prefix, prefixColor, selected)
	write(fitLine(label, themeSettingsLabelWidth(m.effectiveConfig.Features.ThemeName)), m.theme.Foreground, selected)
	if width-used >= themeSwatchWidth()+2 {
		write("  ", m.theme.Muted, false)
		palette, _ := theme.ResolvePalette(name, m.effectiveConfig.Features.ThemeCustom)
		for _, color := range themeSwatchColors(palette) {
			// An unset slot (an empty "custom" palette) would otherwise paint a
			// solid block in the terminal's default foreground and read as a
			// real color; dots make "nothing configured here" legible instead.
			if strings.TrimSpace(color) == "" {
				write(themeSwatchEmptyCell, m.theme.Muted, false)
				continue
			}
			write(themeSwatchCell, color, false)
		}
	}
	if used < width {
		builder.WriteString(paneStyledText(strings.Repeat(" ", width-used), m.theme.Muted, background, false))
	}
	return builder.String()
}

// themeSwatchColors samples each palette in a fixed order so the swatch
// columns mean the same thing on every row.
func themeSwatchColors(palette theme.Palette) []string {
	return []string{
		palette.Accent,
		palette.Foreground,
		palette.Muted,
		palette.Success,
		palette.Warning,
		palette.Error,
		palette.Border,
	}
}

func themeSwatchWidth() int {
	return len(themeSwatchColors(theme.Palette{})) * uniseg.StringWidth(themeSwatchCell)
}

// themeSettingsLabelWidth pads every name to a shared column so the swatch
// strips start at the same offset regardless of name length.
func themeSettingsLabelWidth(activeName string) int {
	widest := 0
	for _, name := range themeSettingsNames() {
		width := uniseg.StringWidth(name)
		if strings.EqualFold(name, activeName) {
			width += uniseg.StringWidth(" (active)")
		}
		widest = maxInt(widest, width)
	}
	return widest
}
