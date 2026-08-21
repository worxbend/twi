package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/twitch"
)

type commandPaletteState struct {
	open     bool
	query    string
	selected int
}

type commandPaletteAction string

const (
	commandPaletteFocusChat     commandPaletteAction = "focus_chat"
	commandPaletteFocusComposer commandPaletteAction = "focus_composer"
	commandPaletteToggleHelp    commandPaletteAction = "toggle_help"
	commandPaletteToggleInspect commandPaletteAction = "toggle_inspect"
	commandPaletteStartReply    commandPaletteAction = "start_reply"
	commandPaletteCancelReply   commandPaletteAction = "cancel_reply"
	commandPalettePageUp        commandPaletteAction = "page_up"
	commandPalettePageDown      commandPaletteAction = "page_down"
	commandPalettePrevious      commandPaletteAction = "previous_channel"
	commandPaletteNext          commandPaletteAction = "next_channel"
	commandPaletteSwitch        commandPaletteAction = "switch_channel"
	commandPaletteOpenChannel   commandPaletteAction = "open_channel"
	commandPaletteCloseChannel  commandPaletteAction = "close_channel"
	commandPaletteToggleSidebar commandPaletteAction = "toggle_sidebar"
	commandPaletteReconnect     commandPaletteAction = "reconnect"
	commandPaletteToggleFilter  commandPaletteAction = "toggle_filter"
	commandPaletteResetFilters  commandPaletteAction = "reset_filters"
	commandPaletteClearLocal    commandPaletteAction = "clear_local"
	commandPaletteQuit          commandPaletteAction = "quit"
	// These reach keys that were previously in no discoverable surface: the
	// emote picker, the theme page, and the four display toggles were
	// reachable only by already knowing the key.
	commandPaletteOpenEmotes      commandPaletteAction = "open_emotes"
	commandPaletteOpenTheme       commandPaletteAction = "open_theme"
	commandPaletteCycleLayout     commandPaletteAction = "cycle_layout"
	commandPaletteCycleBadges     commandPaletteAction = "cycle_badges"
	commandPaletteToggleHighlight commandPaletteAction = "toggle_emote_highlight"
	commandPaletteToggleFullNames commandPaletteAction = "toggle_full_names"
	// Pane visibility and sizing. Chat competes with the activity column for
	// horizontal room, so the palette exposes both the toggle and the resize
	// rather than leaving them key-only.
	commandPaletteToggleActivity commandPaletteAction = "toggle_activity"
	commandPaletteWidenChat      commandPaletteAction = "widen_chat"
	commandPaletteNarrowChat     commandPaletteAction = "narrow_chat"
	commandPaletteResetPanes     commandPaletteAction = "reset_panes"
)

type commandPaletteCommand struct {
	action   commandPaletteAction
	title    string
	shortcut string
	keywords []string
	channel  string
	filter   messageFilter
}

type reconnectingChatClient interface {
	Reconnect(context.Context) error
}

func (m *shellModel) toggleCommandPalette() {
	if m.palette.open {
		m.palette = commandPaletteState{}
		return
	}
	m.closeOtherOverlays("palette")
	m.palette = commandPaletteState{open: true}
	m.refreshPaletteReveal(time.Now())
}

// refreshPaletteReveal (re)builds the command-palette typewriter reveal
// whenever the visible result lines change (query edits, selection moves,
// resize) and advances it when the content is unchanged. Reuses the same
// internal/animation Sequence machinery that reveals chat rows.
func (m *shellModel) refreshPaletteReveal(now time.Time) {
	if !m.palette.open || m.animationMode == string(animation.ModeOff) {
		m.frames.paletteRevealSeq = animation.Sequence{}
		m.frames.paletteRevealKey = ""
		return
	}
	layout := m.layout()
	if layout.paletteContentHeight <= 0 {
		return
	}
	contentWidth := layout.width
	if layout.paletteFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.commandPaletteLines(contentWidth, layout.paletteContentHeight)
	key := strings.Join(lines, "\x00")
	if key == m.frames.paletteRevealKey {
		m.frames.paletteRevealSeq.Advance(now)
		return
	}
	m.frames.paletteRevealKey = key
	m.frames.paletteRevealSeq = animation.NewSequence(paletteLinesToRows(lines), animationConfigFor(m.animationMode), now)
}

func paletteLinesToRows(lines []string) []render.Row {
	rows := make([]render.Row, len(lines))
	for i, line := range lines {
		if line == "" {
			continue
		}
		rows[i] = render.Row{Fragments: []render.Fragment{{Kind: render.FragmentText, Text: line}}}
	}
	return rows
}

// paletteRevealedLines substitutes the typewriter-revealed partial text for
// each line when animation is enabled and the reveal state matches the
// current content; otherwise it falls back to the fully rendered lines
// (animation off, or a resize/content change the reveal hasn't caught up to
// yet, which self-heals on the next tick).
func (m shellModel) paletteRevealedLines(lines []string, width int) []string {
	if m.animationMode == string(animation.ModeOff) {
		return lines
	}
	frame := m.frames.paletteRevealSeq.Frame()
	if len(frame) != len(lines) {
		return lines
	}
	out := make([]string, len(lines))
	for i, row := range frame {
		out[i] = fitLine(row.Plain(), width)
	}
	return out
}

func (m shellModel) handleCommandPaletteKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.palette = commandPaletteState{}
		return m, nil
	case tea.KeyEnter:
		return m.executeCommandPaletteSelection()
	case tea.KeyUp:
		m.movePaletteSelection(-1)
	case tea.KeyDown, tea.KeyTab:
		m.movePaletteSelection(1)
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.deletePaletteRune()
	case tea.KeyCtrlU:
		m.palette.query = ""
		m.palette.selected = 0
	case tea.KeySpace:
		m.palette.query += " "
		m.palette.selected = 0
	case tea.KeyRunes:
		m.palette.query += string(msg.Runes)
		m.palette.selected = 0
	}
	m.clampPaletteSelection()
	m.refreshPaletteReveal(time.Now())
	return m, nil
}

func (m *shellModel) movePaletteSelection(delta int) {
	commands := m.visibleCommandPaletteCommands()
	if len(commands) == 0 {
		m.palette.selected = 0
		return
	}
	m.palette.selected += delta
	if m.palette.selected < 0 {
		m.palette.selected = len(commands) - 1
	}
	if m.palette.selected >= len(commands) {
		m.palette.selected = 0
	}
}

func (m *shellModel) deletePaletteRune() {
	if m.palette.query == "" {
		return
	}
	runes := []rune(m.palette.query)
	m.palette.query = string(runes[:len(runes)-1])
	m.palette.selected = 0
}

func (m *shellModel) clampPaletteSelection() {
	commands := m.visibleCommandPaletteCommands()
	if len(commands) == 0 {
		m.palette.selected = 0
		return
	}
	if m.palette.selected < 0 {
		m.palette.selected = 0
	}
	if m.palette.selected >= len(commands) {
		m.palette.selected = len(commands) - 1
	}
}

func (m shellModel) executeCommandPaletteSelection() (shellModel, tea.Cmd) {
	commands := m.visibleCommandPaletteCommands()
	if len(commands) == 0 {
		m.palette = commandPaletteState{}
		return m, nil
	}
	index := m.palette.selected
	if index < 0 {
		index = 0
	}
	if index >= len(commands) {
		index = len(commands) - 1
	}
	command := commands[index]
	m.palette = commandPaletteState{}
	return m.executeCommandPaletteCommand(command)
}

func (m shellModel) executeCommandPaletteCommand(command commandPaletteCommand) (shellModel, tea.Cmd) {
	switch command.action {
	case commandPaletteFocusChat:
		m.focus = focusChat
	case commandPaletteFocusComposer:
		m.focus = focusComposer
	case commandPaletteToggleHelp:
		m.helpExpanded = !m.helpExpanded
		m.clampScroll()
	case commandPaletteToggleInspect:
		m.inspectOpen = !m.inspectOpen
		m.clampScroll()
	case commandPaletteOpenEmotes:
		m.toggleEmotePicker()
	case commandPaletteOpenTheme:
		m.toggleThemeSettings()
	case commandPaletteCycleLayout:
		m.cycleMessageLayout()
	case commandPaletteCycleBadges:
		m.cycleBadgeMode()
	case commandPaletteToggleHighlight:
		m.toggleEmoteHighlight()
	case commandPaletteToggleFullNames:
		m.toggleFullUsername()
	case commandPaletteToggleActivity:
		m.toggleActivity()
		m.clampScroll()
	case commandPaletteWidenChat:
		m.resizeActivity(-paneResizeStep)
		m.clampScroll()
	case commandPaletteNarrowChat:
		m.resizeActivity(paneResizeStep)
		m.clampScroll()
	case commandPaletteResetPanes:
		m.resetPaneWidths()
		m.clampScroll()
	case commandPaletteStartReply:
		m.startReplyMode()
	case commandPaletteCancelReply:
		m.activeChannelState().replyTo = nil
	case commandPalettePageUp:
		m.scrollBy(m.layout().chatContentHeight)
	case commandPalettePageDown:
		m.scrollBy(-m.layout().chatContentHeight)
	case commandPalettePrevious:
		return m, m.switchChannelBy(-1)
	case commandPaletteNext:
		return m, m.switchChannelBy(1)
	case commandPaletteSwitch:
		return m, m.switchChannel(command.channel)
	case commandPaletteOpenChannel:
		return m, m.openChannelPicker()
	case commandPaletteCloseChannel:
		return m.closeChannel(m.activeChannelName())
	case commandPaletteToggleSidebar:
		m.toggleSidebar()
	case commandPaletteReconnect:
		return m, m.requestReconnect()
	case commandPaletteToggleFilter:
		return m, m.toggleActiveMessageFilter(command.filter)
	case commandPaletteResetFilters:
		return m, m.resetActiveMessageFilters()
	case commandPaletteClearLocal:
		m.clearLocalChat()
	case commandPaletteQuit:
		return m, tea.Quit
	}
	return m, nil
}

func (m shellModel) visibleCommandPaletteCommands() []commandPaletteCommand {
	commands := m.commandPaletteCommands()
	query := strings.TrimSpace(strings.ToLower(m.palette.query))
	if query == "" {
		return commands
	}
	tokens := strings.Fields(query)
	filtered := make([]commandPaletteCommand, 0, len(commands))
	for _, command := range commands {
		if commandPaletteCommandMatches(command, tokens) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

func commandPaletteCommandMatches(command commandPaletteCommand, tokens []string) bool {
	fields := []string{
		string(command.action),
		command.title,
		command.shortcut,
		command.channel,
		strings.Join(command.keywords, " "),
	}
	haystack := strings.ToLower(strings.Join(fields, " "))
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func (m shellModel) commandPaletteCommands() []commandPaletteCommand {
	commands := []commandPaletteCommand{
		{action: commandPaletteFocusComposer, title: "Focus composer", shortcut: "tab", keywords: []string{"message", "input", "draft"}},
		{action: commandPaletteFocusChat, title: "Focus chat", shortcut: "tab", keywords: []string{"messages", "timeline"}},
		{action: commandPaletteStartReply, title: "Reply to selected message", shortcut: "r", keywords: []string{"reply", "selected", "latest"}},
		{action: commandPaletteCancelReply, title: "Cancel reply mode", shortcut: "esc", keywords: []string{"reply", "clear", "cancel"}},
		{action: commandPaletteToggleInspect, title: "Toggle inspect panel", shortcut: "K", keywords: []string{"inspect", "panel", "diagnostics"}},
		{action: commandPaletteOpenChannel, title: "Open channel", shortcut: channelPickerKeyHint, keywords: []string{"channel", "channels", "join", "open", "follow", "follows"}},
		{action: commandPaletteCloseChannel, title: "Close current channel", shortcut: "space x", keywords: []string{"channel", "close", "leave", "part"}},
		{action: commandPaletteToggleSidebar, title: "Toggle channel sidebar", shortcut: "space e", keywords: []string{"channel", "sidebar", "list", "panel"}},
		{action: commandPaletteToggleActivity, title: "Toggle activity column", shortcut: "space a", keywords: []string{"activity", "log", "events", "raids", "subs", "panel", "hide", "show"}},
		{action: commandPaletteWidenChat, title: "Widen chat", shortcut: "<", keywords: []string{"pane", "resize", "wider", "chat", "messages", "activity", "narrow"}},
		{action: commandPaletteNarrowChat, title: "Narrow chat", shortcut: ">", keywords: []string{"pane", "resize", "narrower", "chat", "messages", "activity", "wider"}},
		{action: commandPaletteResetPanes, title: "Reset pane sizes", shortcut: "=", keywords: []string{"pane", "resize", "reset", "default", "auto", "layout"}},
		{action: commandPaletteToggleHelp, title: "Toggle help panel", shortcut: "?", keywords: []string{"help", "panel"}},
		{action: commandPaletteNext, title: "Next channel", shortcut: "]", keywords: []string{"channel", "switch"}},
		{action: commandPalettePrevious, title: "Previous channel", shortcut: "[", keywords: []string{"channel", "switch"}},
		{action: commandPaletteReconnect, title: "Reconnect chat", shortcut: "ctrl+r", keywords: []string{"connection", "irc", "retry"}},
		{action: commandPaletteResetFilters, title: "Reset message filters", shortcut: "0", keywords: []string{"filter", "filters", "all", "reset", "clear"}},
		{action: commandPaletteClearLocal, title: "Clear local chat", shortcut: "ctrl+l", keywords: []string{"history", "messages", "local"}},
		{action: commandPalettePageUp, title: "Scroll page up", shortcut: "pgup", keywords: []string{"scroll", "history"}},
		{action: commandPalettePageDown, title: "Scroll page down", shortcut: "pgdn", keywords: []string{"scroll", "latest"}},
		{action: commandPaletteOpenEmotes, title: "Open emote picker", shortcut: "ctrl+e", keywords: []string{"emote", "emotes", "emoji", "picker", "insert"}},
		{action: commandPaletteOpenTheme, title: "Choose a theme", shortcut: "ctrl+t", keywords: []string{"theme", "themes", "colors", "colour", "palette", "appearance"}},
		{action: commandPaletteCycleLayout, title: "Change message layout", shortcut: "ctrl+g", keywords: []string{"layout", "grouped", "inline", "compact", "display"}},
		{action: commandPaletteCycleBadges, title: "Change badge display", shortcut: "ctrl+b", keywords: []string{"badge", "badges", "glyph", "text", "display"}},
		{action: commandPaletteToggleHighlight, title: "Toggle emote highlighting", shortcut: "ctrl+y", keywords: []string{"emote", "highlight", "chip", "display"}},
		{action: commandPaletteToggleFullNames, title: "Toggle full usernames", shortcut: "ctrl+n", keywords: []string{"username", "login", "display", "names"}},
		{action: commandPaletteQuit, title: "Quit", shortcut: "q / ctrl+c", keywords: []string{"exit"}},
	}

	active := m.activeChannelState()
	for _, def := range messageFilterDefinitions {
		title := "Filter " + def.label
		shortcut := def.shortcut
		if active.messageFilters.enabled(def.filter) {
			title = "Stop filtering " + def.label
			shortcut += " active"
		}
		keywords := append([]string{"filter", "filters", "messages", "local"}, def.keywords...)
		commands = append(commands, commandPaletteCommand{
			action:   commandPaletteToggleFilter,
			title:    title,
			shortcut: shortcut,
			keywords: keywords,
			filter:   def.filter,
		})
	}

	for _, channel := range m.channels.channelNames() {
		title := fmt.Sprintf("Switch to #%s", channel)
		shortcut := "#" + channel
		if channelKey(channel) == channelKey(m.activeChannelName()) {
			shortcut = "active"
		}
		commands = append(commands, commandPaletteCommand{
			action:   commandPaletteSwitch,
			title:    title,
			shortcut: shortcut,
			keywords: []string{"channel", "switch", channel},
			channel:  channel,
		})
	}
	return commands
}

func (m shellModel) commandPaletteView(layout shellLayout) string {
	contentWidth := layout.width
	if layout.paletteFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.commandPaletteLines(contentWidth, layout.paletteContentHeight)
	lines = m.paletteRevealedLines(lines, contentWidth)
	content := strings.Join(lines, "\n")
	if !layout.paletteFramed {
		return fitBlock(content, layout.width, layout.paletteHeight)
	}
	return m.renderPane(paneSpec{
		icon:          "⌘",
		title:         "Command Palette",
		content:       content,
		width:         layout.width,
		contentHeight: layout.paletteContentHeight,
		padding:       1,
		accent:        m.theme.Accent,
		focused:       true,
	})
}

func (m shellModel) commandPaletteLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	query := m.palette.query
	header := " Command"
	if query != "" {
		header += ": " + query
	}
	lines := []string{fitLine(header, width)}
	if height == 1 {
		return lines
	}

	commands := m.visibleCommandPaletteCommands()
	if len(commands) == 0 {
		lines = append(lines, fitLine("  no matches", width))
	} else {
		start, selected := pickerWindow(m.palette.selected, len(commands), height)
		for i := start; i < len(commands) && len(lines) < height; i++ {
			prefix := "  "
			if i == selected {
				prefix = "> "
			}
			label := prefix + commands[i].title
			if commands[i].shortcut != "" {
				label += "  " + commands[i].shortcut
			}
			lines = append(lines, fitLine(label, width))
		}
	}
	for len(lines) < height {
		lines = append(lines, fitLine("", width))
	}
	return lines[:height]
}

func paletteWindowStart(selected, total, height int) int {
	if height <= 0 || total <= height {
		return 0
	}
	start := selected - height/2
	if start < 0 {
		return 0
	}
	if start+height > total {
		return total - height
	}
	return start
}

func (m *shellModel) switchChannelBy(delta int) tea.Cmd {
	if m.channels.switchBy(delta) {
		m.clampScroll()
		return m.asyncAssetCommand()
	}
	return nil
}

func (m *shellModel) switchChannel(channel string) tea.Cmd {
	if m.channels.setActive(channel) {
		m.clampScroll()
		return m.asyncAssetCommand()
	}
	return nil
}

func (m *shellModel) asyncAssetCommand() tea.Cmd {
	_, cmd := m.withAsyncAssetCommands(nil)
	return cmd
}

func (m *shellModel) clearLocalChat() {
	state := m.activeChannelState()
	state.messages = nil
	state.scrollOffset = 0
	state.activeOrder = nil
	state.activeMessages = make(map[string]twitch.ChatMessage)
	state.localEchoes = make(map[string]struct{})
	if m.channels != nil {
		state.revealQueue = animation.NewQueue(m.channels.animationConfig, m.channels.clock)
	}
}

func (m *shellModel) requestReconnect() tea.Cmd {
	channel := m.activeChannelName()
	if m.reconnectInFlight {
		state := ConnectionState{
			Status:  ConnectionReconnecting,
			Channel: channel,
			Detail:  "manual reconnect already in progress",
			At:      time.Now(),
		}
		m.channels.applyConnectionState(state)
		m.debugConnectionState("app.reconnect.already_in_progress", state)
		return nil
	}
	client, ok := m.services.client.(reconnectingChatClient)
	if !ok {
		state := m.activeChannelState().status
		state.Channel = channel
		if state.Status == "" {
			state.Status = ConnectionDisconnected
		}
		state.Detail = "manual reconnect unavailable for this chat source"
		state.At = time.Now()
		m.channels.applyConnectionState(state)
		m.debugConnectionState("app.reconnect.unavailable", state)
		return nil
	}
	m.reconnectInFlight = true
	state := ConnectionState{
		Status:  ConnectionReconnecting,
		Channel: channel,
		Detail:  "manual reconnect requested",
		At:      time.Now(),
	}
	m.channels.applyConnectionState(state)
	m.debugConnectionState("app.reconnect.requested", state)
	return func() tea.Msg {
		return reconnectCompletedMsg{
			channel: channel,
			err:     client.Reconnect(context.Background()),
		}
	}
}

func (m *shellModel) completeReconnect(msg reconnectCompletedMsg) {
	m.reconnectInFlight = false
	channel := msg.channel
	if channel == "" {
		channel = m.activeChannelName()
	}
	if msg.err != nil {
		if errors.Is(msg.err, ErrReconnectUnavailable) {
			state := m.channels.ensure(channel).status
			state.Channel = channel
			if state.Status == "" {
				state.Status = ConnectionDisconnected
			}
			state.Detail = "manual reconnect unavailable for this chat source"
			state.Err = msg.err
			state.At = time.Now()
			m.channels.applyConnectionState(state)
			m.debugConnectionState("app.reconnect.completed", state)
			return
		}
		if errors.Is(msg.err, ErrReconnectInProgress) {
			state := ConnectionState{
				Status:  ConnectionReconnecting,
				Channel: channel,
				Detail:  "manual reconnect already in progress",
				Err:     msg.err,
				At:      time.Now(),
			}
			m.channels.applyConnectionState(state)
			m.debugConnectionState("app.reconnect.completed", state)
			return
		}
		if errors.Is(msg.err, context.Canceled) {
			state := ConnectionState{
				Status:  ConnectionDisconnected,
				Channel: channel,
				Detail:  "manual reconnect canceled; retry with ctrl+r",
				Err:     msg.err,
				At:      time.Now(),
			}
			m.channels.applyConnectionState(state)
			m.debugConnectionState("app.reconnect.completed", state)
			return
		}
		state := ConnectionState{
			Status:  ConnectionFailed,
			Channel: channel,
			Detail:  "manual reconnect failed: " + credentialSafeDetail(msg.err) + "; retry with ctrl+r",
			Err:     msg.err,
			At:      time.Now(),
		}
		m.channels.applyConnectionState(state)
		m.debugConnectionState("app.reconnect.completed", state)
		return
	}
	state := ConnectionState{
		Status:  ConnectionConnecting,
		Channel: channel,
		Detail:  "manual reconnect started; waiting for Twitch IRC",
		At:      time.Now(),
	}
	m.channels.applyConnectionState(state)
	m.debugConnectionState("app.reconnect.completed", state)
}
