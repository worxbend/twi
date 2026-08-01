package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// TestWriteDocsScreenshots regenerates the SVG terminal screenshots used by
// the README and the microsite.
//
// It is a generator, not an assertion, so it is skipped unless
// TWI_WRITE_SCREENSHOTS=1 - CI must never write into the working tree. Run it
// with:
//
//	TWI_WRITE_SCREENSHOTS=1 go test ./internal/app -run TestWriteDocsScreenshots
//
// Rendering the real View() rather than hand-drawing mockups means the
// screenshots cannot drift away from what twi actually prints.
func TestWriteDocsScreenshots(t *testing.T) {
	if os.Getenv("TWI_WRITE_SCREENSHOTS") != "1" {
		t.Skip("set TWI_WRITE_SCREENSHOTS=1 to regenerate docs screenshots")
	}
	forceColorProfile(t)
	lipgloss.SetColorProfile(termenv.TrueColor)

	outDir := filepath.Join("..", "..", "docs", "assets", "screenshots")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", outDir, err)
	}

	for _, shot := range screenshotScenes() {
		svg := ansiToSVG(shot.render(t), shot.title)
		path := filepath.Join(outDir, shot.name+".svg")
		if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		t.Logf("wrote %s", path)
	}
}

type screenshotScene struct {
	name   string
	title  string
	render func(t *testing.T) string
}

func screenshotScenes() []screenshotScene {
	return []screenshotScene{
		{name: "chat-grouped", title: "twi — grouped layout", render: sceneGrouped},
		{name: "chat-inline", title: "twi — inline layout", render: sceneInline},
		{name: "mention-autocomplete", title: "twi — @mention autocomplete", render: sceneMentions},
		{name: "theme-picker", title: "twi — theme picker", render: sceneThemePicker},
	}
}

// screenshotModel builds a deterministic, populated chat so every screenshot
// shows the same conversation under different settings.
func screenshotModel(t *testing.T, theme string, layout string, width, height int) mockShellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.ThemeName = theme
	cfg.Features.MessageLayout = layout
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")
	cfg.DefaultChannels = []string{"pixelforge", "synthwave", "codegolf"}

	model := newMockShellModel("pixelforge", cfg)
	model.width, model.height = width, height
	model.splashSkipped = true

	at := time.Date(2026, 8, 1, 20, 14, 0, 0, time.Local)
	messages := []twitch.ChatMessage{
		{
			ID: "m1", Channel: "pixelforge", AuthorLogin: "pixelforge", DisplayName: "PixelForge",
			Timestamp: at, Type: twitch.MessageTypeChat, Text: "welcome in, we're building the render pipeline today 🔥",
			Badges: []twitch.Badge{{SetID: "broadcaster", ID: "1"}},
		},
		{
			ID: "m2", Channel: "pixelforge", AuthorLogin: "nova_dev", DisplayName: "nova_dev",
			Timestamp: at.Add(time.Minute), Type: twitch.MessageTypeChat, Text: "finally a chat client that lives in my terminal Kappa",
			Badges: []twitch.Badge{{SetID: "moderator", ID: "1"}, {SetID: "subscriber", ID: "12", Info: "26"}},
		},
		{
			ID: "m3", Channel: "pixelforge", AuthorLogin: "nova_dev", DisplayName: "nova_dev",
			Timestamp: at.Add(2 * time.Minute), Type: twitch.MessageTypeChat, Text: "grouped layout is really nice for reading back",
		},
		{
			ID: "m4", Channel: "pixelforge", AuthorLogin: "syntaxerror", DisplayName: "SyntaxError",
			Timestamp: at.Add(3 * time.Minute), Type: twitch.MessageTypeChat, Text: "@nova_dev agreed 💜 the swatches sold me",
			Badges: []twitch.Badge{{SetID: "vip", ID: "1"}},
		},
		{
			ID: "m5", Channel: "pixelforge", AuthorLogin: "lurkbot", DisplayName: "lurkbot",
			Timestamp: at.Add(4 * time.Minute), Type: twitch.MessageTypeChat, Text: "no browser tab, no problem ✨",
		},
	}

	state := model.activeChannelState()
	state.messages = messages
	state.activeOrder = nil
	state.activeMessages = map[string]twitch.ChatMessage{}
	state.live = true
	state.liveSince = at.Add(-97 * time.Minute)
	state.viewerCount = 1284
	for _, message := range messages {
		state.roster.observeMessage(message)
	}
	state.roster.applyFollowers([]twitch.Follower{
		{UserLogin: "nova_dev", FollowedAt: at.Add(-420 * 24 * time.Hour)},
		{UserLogin: "syntaxerror", FollowedAt: at.Add(-63 * 24 * time.Hour)},
	})
	for _, login := range []string{"nova_dev", "syntaxerror", "lurkbot", "quietviewer", "chatfan22"} {
		model.applyMembershipEvent(twitch.MembershipEvent{
			Type: twitch.MembershipJoin, Channel: "pixelforge", Login: login, At: at,
		})
	}
	model.appendActivity(activityEntry{Kind: activityFollow, Channel: "pixelforge", Text: "chatfan22 followed", At: at.Add(5 * time.Minute)})
	model.appendActivity(activityEntry{Kind: activityIRCEvent, Channel: "pixelforge", Text: "raid from synthwave", At: at.Add(6 * time.Minute)})
	model.appendActivity(activityEntry{Kind: activityCheer, Channel: "pixelforge", Text: "nova_dev cheered 500 bits", At: at.Add(7 * time.Minute)})
	return model
}

func sceneGrouped(t *testing.T) string {
	model := screenshotModel(t, "claude", "grouped", 132, 30)
	return model.View()
}

func sceneInline(t *testing.T) string {
	model := screenshotModel(t, "tokyo-night", "inline", 132, 30)
	return model.View()
}

func sceneMentions(t *testing.T) string {
	model := screenshotModel(t, "catppuccin-mocha", "grouped", 132, 30)
	model.focus = mockFocusComposer
	model.activeChannelState().composerText = "great point @nov"
	return model.View()
}

func sceneThemePicker(t *testing.T) string {
	model := screenshotModel(t, "claude", "grouped", 132, 30)
	model.toggleThemeSettings()
	for i := 0; i < 9; i++ {
		model.moveThemeSettingsSelection(1)
	}
	return model.View()
}

// --- ANSI to SVG ---------------------------------------------------------
//
// A minimal SGR interpreter: enough to reproduce what lipgloss emits (24-bit
// foreground/background, bold, italic, strikethrough, reset). Cells are laid
// out on a fixed grid using display width, so double-width emoji stay aligned
// exactly as they do in a terminal.

const (
	svgCellWidth  = 8.4
	svgLineHeight = 18.0
	svgFontSize   = 14.0
	svgPadding    = 18.0
	svgTitleBar   = 34.0
)

type svgCell struct {
	text          string
	width         int
	fg            string
	bg            string
	bold          bool
	italic        bool
	strikethrough bool
}

func ansiToSVG(rendered, title string) string {
	lines := strings.Split(strings.TrimRight(stripOSC(rendered), "\n"), "\n")
	grid := make([][]svgCell, 0, len(lines))
	columns := 0
	for _, line := range lines {
		cells := parseANSILine(line)
		width := 0
		for _, cell := range cells {
			width += cell.width
		}
		if width > columns {
			columns = width
		}
		grid = append(grid, cells)
	}
	if columns == 0 {
		columns = 80
	}

	bodyWidth := float64(columns)*svgCellWidth + svgPadding*2
	bodyHeight := float64(len(grid))*svgLineHeight + svgPadding*2 + svgTitleBar

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`+"\n",
		bodyWidth, bodyHeight, bodyWidth, bodyHeight, escapeXML(title))
	b.WriteString(`<defs><filter id="s" x="-4%" y="-4%" width="108%" height="112%"><feDropShadow dx="0" dy="10" stdDeviation="14" flood-color="#000" flood-opacity="0.45"/></filter></defs>` + "\n")

	// Window chrome: rounded frame plus the three traffic-light dots, so the
	// screenshot reads as a terminal window rather than a wall of text.
	frame := backgroundOf(grid)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%.0f" height="%.0f" rx="12" fill="%s" filter="url(#s)"/>`+"\n", bodyWidth, bodyHeight, frame)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%.0f" height="%.0f" rx="12" fill="#000" fill-opacity="0.22"/>`+"\n", bodyWidth, svgTitleBar)
	for i, color := range []string{"#ff5f57", "#febc2e", "#28c840"} {
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="6" fill="%s"/>`+"\n", 20+float64(i)*20, svgTitleBar/2, color)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-family="ui-monospace,SFMono-Regular,Menlo,Consolas,monospace" font-size="12" fill="#ffffff" fill-opacity="0.55" text-anchor="middle">%s</text>`+"\n",
		bodyWidth/2, svgTitleBar/2+4, escapeXML(title))

	fmt.Fprintf(&b, `<g font-family="ui-monospace,SFMono-Regular,Menlo,Consolas,'DejaVu Sans Mono',monospace" font-size="%.0f">`+"\n", svgFontSize)
	for row, cells := range grid {
		y := svgTitleBar + svgPadding + float64(row)*svgLineHeight
		column := 0
		// Backgrounds first so text always paints on top of its own chip.
		for _, cell := range cells {
			if cell.bg != "" && cell.width > 0 {
				fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s"/>`,
					svgPadding+float64(column)*svgCellWidth, y, float64(cell.width)*svgCellWidth, svgLineHeight, cell.bg)
			}
			column += cell.width
		}
		column = 0
		for _, cell := range cells {
			// Whitespace-only runs contribute nothing beyond the background
			// rect already drawn above.
			if strings.TrimSpace(cell.text) == "" {
				column += cell.width
				continue
			}
			attrs := ""
			if cell.bold {
				attrs += ` font-weight="600"`
			}
			if cell.italic {
				attrs += ` font-style="italic"`
			}
			if cell.strikethrough {
				attrs += ` text-decoration="line-through"`
			}
			fill := cell.fg
			if fill == "" {
				fill = "#e6e6e6"
			}
			// textLength pins each run to an exact cell count. Without it the
			// font's natural advance drifts from the grid over a long line and
			// the right-hand columns walk off the edge.
			fmt.Fprintf(&b, `<text x="%.2f" y="%.2f" fill="%s"%s textLength="%.2f" lengthAdjust="spacingAndGlyphs" xml:space="preserve">%s</text>`,
				svgPadding+float64(column)*svgCellWidth, y+svgFontSize, fill, attrs,
				float64(cell.width)*svgCellWidth, escapeXML(cell.text))
			column += cell.width
		}
		b.WriteString("\n")
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

// backgroundOf picks the most common background color as the window fill, so
// the frame matches whatever theme the scene was rendered in.
func backgroundOf(grid [][]svgCell) string {
	counts := map[string]int{}
	for _, row := range grid {
		for _, cell := range row {
			if cell.bg != "" {
				counts[cell.bg] += cell.width
			}
		}
	}
	best, bestCount := "#12131a", 0
	for color, count := range counts {
		if count > bestCount {
			best, bestCount = color, count
		}
	}
	return best
}

// stripOSC removes OSC sequences (twi emits OSC 11 to set the terminal
// background), which carry no glyphs and would otherwise leak into the text.
func stripOSC(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == ']' {
			j := i + 2
			for j < len(value) {
				if value[j] == 0x07 {
					j++
					break
				}
				if value[j] == 0x1b && j+1 < len(value) && value[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func parseANSILine(line string) []svgCell {
	var cells []svgCell
	var current svgCell
	flush := func() {
		if current.text != "" {
			cells = append(cells, current)
		}
		current.text = ""
		current.width = 0
	}

	state := svgCell{}
	runes := []rune(line)
	for i := 0; i < len(runes); {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && runes[j] != 'm' && !isANSIFinal(runes[j]) {
				j++
			}
			if j < len(runes) && runes[j] == 'm' {
				flush()
				applySGR(&state, string(runes[i+2:j]))
				current.fg, current.bg = state.fg, state.bg
				current.bold, current.italic, current.strikethrough = state.bold, state.italic, state.strikethrough
			}
			i = j + 1
			continue
		}
		// Consume one grapheme cluster so emoji and combining marks stay whole.
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(string(runes[i:]), -1)
		_ = rest
		if cluster == "" {
			cluster = string(runes[i])
		}
		current.fg, current.bg = state.fg, state.bg
		current.bold, current.italic, current.strikethrough = state.bold, state.italic, state.strikethrough
		current.text += cluster
		current.width += uniseg.StringWidth(cluster)
		i += len([]rune(cluster))
	}
	flush()
	return cells
}

func isANSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7e && r != '[' && r != ';'
}

func applySGR(state *svgCell, params string) {
	if params == "" {
		params = "0"
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "", "0":
			state.fg, state.bg = "", ""
			state.bold, state.italic, state.strikethrough = false, false, false
		case "1":
			state.bold = true
		case "3":
			state.italic = true
		case "9":
			state.strikethrough = true
		case "22":
			state.bold = false
		case "23":
			state.italic = false
		case "29":
			state.strikethrough = false
		case "39":
			state.fg = ""
		case "49":
			state.bg = ""
		case "38", "48":
			if i+4 < len(parts) && parts[i+1] == "2" {
				color := rgbHex(parts[i+2], parts[i+3], parts[i+4])
				if parts[i] == "38" {
					state.fg = color
				} else {
					state.bg = color
				}
				i += 4
			}
		}
	}
}

func rgbHex(r, g, b string) string {
	value := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}
	return fmt.Sprintf("#%02x%02x%02x", value(r), value(g), value(b))
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
