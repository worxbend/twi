package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/twitch"
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
	commandPaletteReconnect     commandPaletteAction = "reconnect"
	commandPaletteToggleFilter  commandPaletteAction = "toggle_filter"
	commandPaletteResetFilters  commandPaletteAction = "reset_filters"
	commandPaletteClearLocal    commandPaletteAction = "clear_local"
	commandPaletteQuit          commandPaletteAction = "quit"
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

func (m *mockShellModel) toggleCommandPalette() {
	if m.palette.open {
		m.palette = commandPaletteState{}
		return
	}
	m.palette = commandPaletteState{open: true}
}

func (m mockShellModel) handleCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	return m, nil
}

func (m *mockShellModel) movePaletteSelection(delta int) {
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

func (m *mockShellModel) deletePaletteRune() {
	if m.palette.query == "" {
		return
	}
	runes := []rune(m.palette.query)
	m.palette.query = string(runes[:len(runes)-1])
	m.palette.selected = 0
}

func (m *mockShellModel) clampPaletteSelection() {
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

func (m mockShellModel) executeCommandPaletteSelection() (tea.Model, tea.Cmd) {
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

func (m mockShellModel) executeCommandPaletteCommand(command commandPaletteCommand) (tea.Model, tea.Cmd) {
	switch command.action {
	case commandPaletteFocusChat:
		m.focus = mockFocusChat
	case commandPaletteFocusComposer:
		m.focus = mockFocusComposer
	case commandPaletteToggleHelp:
		m.helpExpanded = !m.helpExpanded
		m.clampScroll()
	case commandPaletteToggleInspect:
		m.inspectOpen = !m.inspectOpen
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

func (m mockShellModel) visibleCommandPaletteCommands() []commandPaletteCommand {
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

func (m mockShellModel) commandPaletteCommands() []commandPaletteCommand {
	commands := []commandPaletteCommand{
		{action: commandPaletteFocusComposer, title: "Focus composer", shortcut: "tab", keywords: []string{"message", "input", "draft"}},
		{action: commandPaletteFocusChat, title: "Focus chat", shortcut: "tab", keywords: []string{"messages", "timeline"}},
		{action: commandPaletteStartReply, title: "Reply to selected message", shortcut: "r", keywords: []string{"reply", "selected", "latest"}},
		{action: commandPaletteCancelReply, title: "Cancel reply mode", shortcut: "esc", keywords: []string{"reply", "clear", "cancel"}},
		{action: commandPaletteToggleInspect, title: "Toggle inspect panel", shortcut: "i", keywords: []string{"inspect", "panel", "diagnostics"}},
		{action: commandPaletteToggleHelp, title: "Toggle help panel", shortcut: "?", keywords: []string{"help", "panel"}},
		{action: commandPaletteNext, title: "Next channel", shortcut: "]", keywords: []string{"channel", "switch"}},
		{action: commandPalettePrevious, title: "Previous channel", shortcut: "[", keywords: []string{"channel", "switch"}},
		{action: commandPaletteReconnect, title: "Reconnect chat", shortcut: "ctrl+r", keywords: []string{"connection", "irc", "retry"}},
		{action: commandPaletteResetFilters, title: "Reset message filters", shortcut: "0", keywords: []string{"filter", "filters", "all", "reset", "clear"}},
		{action: commandPaletteClearLocal, title: "Clear local chat", shortcut: "ctrl+l", keywords: []string{"history", "messages", "local"}},
		{action: commandPalettePageUp, title: "Scroll page up", shortcut: "pgup", keywords: []string{"scroll", "history"}},
		{action: commandPalettePageDown, title: "Scroll page down", shortcut: "pgdn", keywords: []string{"scroll", "latest"}},
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

func (m mockShellModel) commandPaletteView(layout mockShellLayout) string {
	contentWidth := layout.width
	if layout.paletteFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.commandPaletteLines(contentWidth, layout.paletteContentHeight)
	content := strings.Join(lines, "\n")
	if !layout.paletteFramed {
		return fitBlock(content, layout.width, layout.paletteHeight)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.paletteContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#f9e2af")).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) commandPaletteLines(width, height int) []string {
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
		selected := m.palette.selected
		if selected < 0 || selected >= len(commands) {
			selected = 0
		}
		maxCommands := height - 1
		start := paletteWindowStart(selected, len(commands), maxCommands)
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

func (m *mockShellModel) switchChannelBy(delta int) tea.Cmd {
	if m.channels.switchBy(delta) {
		m.clampScroll()
		return m.asyncAssetCommand()
	}
	return nil
}

func (m *mockShellModel) switchChannel(channel string) tea.Cmd {
	if m.channels.setActive(channel) {
		m.clampScroll()
		return m.asyncAssetCommand()
	}
	return nil
}

func (m *mockShellModel) asyncAssetCommand() tea.Cmd {
	_, cmd := m.withAsyncAssetCommands(nil)
	return cmd
}

func (m *mockShellModel) clearLocalChat() {
	state := m.activeChannelState()
	state.messages = nil
	state.scrollOffset = 0
	state.activeOrder = nil
	state.activeMessages = make(map[string]twitch.ChatMessage)
	if m.channels != nil {
		state.revealQueue = animation.NewQueue(m.channels.animationConfig, m.channels.clock)
	}
}

func (m *mockShellModel) requestReconnect() tea.Cmd {
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
	client, ok := m.client.(reconnectingChatClient)
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

func (m *mockShellModel) completeReconnect(msg reconnectCompletedMsg) {
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
