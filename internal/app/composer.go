package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/animation"
)

type composerSegment struct {
	text       string
	foreground string
	bold       bool
	italic     bool
}

// composerView renders an OpenCode-inspired input surface: a quiet inset
// panel, one focus rail, a block cursor, and a compact metadata footer. The
// borderless shape keeps the chat hierarchy light while send/reply state stays
// visible. Small terminals collapse to a compact surface or plain text.
func (m mockShellModel) composerView(layout mockShellLayout) string {
	if layout.composerHeight <= 0 {
		return ""
	}
	if !layout.composerFramed {
		return m.plainComposerView(layout)
	}

	panelHeight := layout.composerHeight
	panelWidth := clampMin(layout.width-2, 1)
	bodyWidth := clampMin(panelWidth-1, 0)
	bodyPadding := 1
	if bodyWidth >= 12 {
		bodyPadding = 2
	}
	contentWidth := clampMin(bodyWidth-bodyPadding*2, 0)

	lines := make([]string, 0, layout.composerHeight)
	panelLines := m.composerPanelLines(panelHeight, contentWidth)
	railColor := m.theme.Border
	if m.composerFocused() {
		railColor = m.theme.Accent
	}
	for _, segments := range panelLines {
		body := renderComposerSegments(segments, contentWidth, m.theme.Surface)
		body = composerSurfaceSpaces(bodyPadding, m.theme.Surface) + body
		body += composerSurfaceSpaces(bodyWidth-lipgloss.Width(body), m.theme.Surface)
		panel := lipgloss.NewStyle().
			Foreground(lipgloss.Color(railColor)).
			Background(lipgloss.Color(m.theme.Surface)).
			Bold(m.composerFocused()).
			Render("▌") + body
		line := composerBackgroundLine(1, m.canvasBackground()) + panel
		line += composerBackgroundLine(layout.width-lipgloss.Width(line), m.canvasBackground())
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func (m mockShellModel) plainComposerView(layout mockShellLayout) string {
	active := m.activeChannelState()
	input := active.composerText
	if input == "" && !m.composerFocused() {
		input = m.composerPlaceholder("")
	} else if m.composerFocused() {
		cursorWidth := 0
		if layout.width >= 2 {
			cursorWidth = 1
		}
		input = tailDisplayCells(input, clampMin(layout.width-cursorWidth, 0))
		if cursorWidth > 0 {
			cursor := "█"
			if !m.composerCursorVisible() {
				cursor = " "
			}
			input += cursor
		}
	}
	if active.replyTo != nil && layout.composerHeight >= 2 {
		input = m.replyContextLine(layout.width) + "\n" + input
	}
	return fitBlock(input, layout.width, layout.composerHeight)
}

func (m mockShellModel) composerPanelLines(height, width int) [][]composerSegment {
	if height <= 0 {
		return nil
	}
	lines := make([][]composerSegment, 0, height)
	if m.activeChannelState().replyTo != nil && height >= 3 {
		lines = append(lines, m.composerReplySegments())
	}
	// The @mention strip sits directly above the draft so the candidate list
	// is adjacent to the word being completed. It is skipped rather than
	// truncated when the composer is too short to hold it without evicting
	// the input row itself.
	if mentions := m.composerMentionSegments(m.mentionSuggestions()); len(mentions) > 0 && height >= len(lines)+3 {
		lines = append(lines, mentions)
	}
	lines = append(lines, m.composerInputSegments(width))
	for len(lines) < height-1 {
		lines = append(lines, nil)
	}
	if len(lines) < height {
		lines = append(lines, m.composerMetadataSegments())
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m mockShellModel) composerInputSegments(width int) []composerSegment {
	focused := m.composerFocused()
	cursorWidth := 0
	if focused && width > 0 {
		cursorWidth = 1
	}
	available := clampMin(width-cursorWidth, 0)
	input := m.activeChannelState().composerText
	placeholder := false
	if input == "" && !focused {
		input = m.composerPlaceholder("…")
		placeholder = true
	}
	if focused {
		input = tailDisplayCells(input, available)
	} else {
		input = revealDisplayCells(input, available)
	}
	foreground := m.theme.Foreground
	if placeholder {
		foreground = m.theme.Muted
	}
	segments := []composerSegment{{text: input, foreground: foreground}}
	if focused && width > 0 {
		cursor := "█"
		if !m.composerCursorVisible() {
			cursor = " "
		}
		segments = append(segments, composerSegment{text: cursor, foreground: m.theme.Foreground, bold: true})
	}
	return segments
}

// tailDisplayCells keeps the append-only composer's caret end visible without
// splitting grapheme clusters when a draft grows wider than the input surface.
func tailDisplayCells(value string, cells int) string {
	if cells <= 0 || value == "" {
		return ""
	}
	graphemes := uniseg.NewGraphemes(value)
	clusters := make([]string, 0, len(value))
	widths := make([]int, 0, len(value))
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusters = append(clusters, cluster)
		widths = append(widths, uniseg.StringWidth(cluster))
	}
	used := 0
	start := len(clusters)
	for index := len(clusters) - 1; index >= 0; index-- {
		if used+widths[index] > cells {
			break
		}
		used += widths[index]
		start = index
	}
	return strings.Join(clusters[start:], "")
}

func (m mockShellModel) composerReplySegments() []composerSegment {
	reply := m.activeChannelState().replyTo
	if reply == nil {
		return nil
	}
	author := redactDiagnosticText(replyAuthor(reply))
	segments := []composerSegment{
		{text: "↳ Replying to", foreground: m.theme.Accent, bold: true},
		{text: " " + author, foreground: m.theme.Foreground},
	}
	if reply.Text != "" {
		segments = append(segments,
			composerSegment{text: " · ", foreground: m.theme.Muted},
			composerSegment{text: redactDiagnosticText(compactReplyText(reply.Text)), foreground: m.theme.Muted, italic: true},
		)
	}
	return segments
}

func (m mockShellModel) composerMetadataSegments() []composerSegment {
	state, color := m.composerStateLabel()
	return []composerSegment{
		{text: "✉ Chat", foreground: m.theme.Accent, bold: true},
		{text: " · ", foreground: m.theme.Muted},
		{text: m.composerChannelLabel(), foreground: m.theme.Foreground, bold: true},
		{text: " · ", foreground: m.theme.Muted},
		{text: state, foreground: color, bold: state != "ready"},
	}
}

// composerPlaceholder names what typing will do. With nothing open the
// composer is still usable - /channels is typed into it - so the prompt
// points there instead of naming a channel that does not exist.
func (m mockShellModel) composerPlaceholder(suffix string) string {
	if m.channels.empty() {
		return "/channels to open a channel" + suffix
	}
	return "Message #" + m.activeChannelName() + suffix
}

func (m mockShellModel) composerChannelLabel() string {
	if m.channels.empty() {
		return "no channel"
	}
	return "#" + m.activeChannelName()
}

func (m mockShellModel) composerStateLabel() (string, string) {
	switch m.activeChannelState().sendState {
	case composerSendQueued:
		return "queued", m.theme.Warning
	case composerSendSending:
		return "sending", m.theme.Warning
	case composerSendSucceeded:
		return "sent", m.theme.Success
	case composerSendFailed:
		return "failed", m.theme.Error
	case composerSendRateLimited:
		return "rate limited", m.theme.Error
	default:
		return "ready", m.theme.Success
	}
}

func (m mockShellModel) composerFocused() bool {
	return m.focus == mockFocusComposer && !m.anyOverlayOpen()
}

func (m mockShellModel) composerCursorVisible() bool {
	if !m.composerFocused() {
		return false
	}
	if m.animationMode == string(animation.ModeOff) || m.lastFrameAt.IsZero() {
		return true
	}
	interval := 500 * time.Millisecond
	if m.animationMode == string(animation.ModeReduced) {
		interval = time.Second
	}
	return (m.lastFrameAt.UnixNano()/int64(interval))%2 == 0
}

func renderComposerSegments(segments []composerSegment, width int, background string) string {
	if width <= 0 {
		return ""
	}
	var builder strings.Builder
	remaining := width
	for _, segment := range segments {
		if remaining <= 0 {
			break
		}
		text := revealDisplayCells(segment.text, remaining)
		if text == "" {
			continue
		}
		builder.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(segment.foreground)).
			Background(lipgloss.Color(background)).
			Bold(segment.bold).
			Italic(segment.italic).
			Render(text))
		remaining -= lipgloss.Width(text)
	}
	return builder.String()
}

func composerSurfaceSpaces(width int, background string) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(background)).
		Render(strings.Repeat(" ", width))
}

func composerBackgroundLine(width int, background string) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(background)).
		Render(strings.Repeat(" ", width))
}
