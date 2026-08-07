package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// openChannel adds a channel to the open set, makes it active, and asks the
// transport to join it. A transport that cannot join on a live connection
// (the mock source, or one that is momentarily disconnected) is not an
// error: the channel is still tracked locally and the next reconnect picks
// it up, so opening never fails in a way the user has to recover from.
func (m shellModel) openChannel(channel string) (tea.Model, tea.Cmd) {
	name := normalizeChannelName(channel)
	if name == "" {
		return m, nil
	}
	alreadyOpen := m.channelIsOpen(name)
	if !m.channels.open(name) {
		return m, nil
	}
	m.clampScroll()
	if !alreadyOpen {
		m.joinChannelOnTransport(name)
		m.appendActivity(activityEntry{
			Kind: activityChannel,
			Text: fmt.Sprintf("opened #%s", name),
		})
	}
	m.debugChannelOpened(name)
	return m.withAsyncAssetCommands(nil)
}

// closeChannel leaves a channel and drops its buffered messages. Closing the
// last open channel is allowed and lands on the empty state rather than
// quitting: the session still has a connection, a composer, and a picker.
func (m shellModel) closeChannel(channel string) (tea.Model, tea.Cmd) {
	name := normalizeChannelName(channel)
	if name == "" || !m.channelIsOpen(name) {
		return m, nil
	}
	if !m.channels.close(name) {
		return m, nil
	}
	m.partChannelOnTransport(name)
	m.appendActivity(activityEntry{
		Kind: activityChannel,
		Text: fmt.Sprintf("closed #%s", name),
	})
	m.clampSidebarSelection()
	m.clampScroll()
	m.debugChannelClosed(name)
	return m.withAsyncAssetCommands(nil)
}

func (m shellModel) channelIsOpen(channel string) bool {
	if m.channels == nil {
		return false
	}
	_, ok := m.channels.states[channelKey(channel)]
	return ok
}

func (m shellModel) joinChannelOnTransport(channel string) {
	joiner, ok := m.client.(ChannelJoiner)
	if !ok {
		return
	}
	if err := joiner.JoinChannel(channel); err != nil {
		m.debugChannelJoinFailed(channel, err)
	}
}

func (m shellModel) partChannelOnTransport(channel string) {
	joiner, ok := m.client.(ChannelJoiner)
	if !ok {
		return
	}
	if err := joiner.PartChannel(channel); err != nil {
		m.debugChannelPartFailed(channel, err)
	}
}

// composerChannelCommand recognizes the composer's own "/channels" command
// (and its "/channel" singular form). Unlike /clip it never reaches Twitch:
// it opens a local overlay, or opens a named channel directly when one is
// given, so "/channels alpha" is a one-line way in.
func composerChannelCommand(text string) (channel string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return "", false
	}
	if !strings.EqualFold(fields[0], "/channels") && !strings.EqualFold(fields[0], "/channel") {
		return "", false
	}
	if len(fields) == 1 {
		return "", true
	}
	return normalizeChannelName(fields[1]), true
}
