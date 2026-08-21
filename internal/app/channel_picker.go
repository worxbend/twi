package app

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	channelPickerRequestTimeout = 5 * time.Second
	// channelPickerKeyHint names the key that opens the picker. It appears in
	// the empty state, the help strip, and the picker's own header, so the
	// three can never drift apart.
	channelPickerKeyHint = "space c"
)

// channelPickerState drives the /channels overlay: a searchable list of
// channels to open. Unlike the category picker there is no per-keystroke
// Twitch search - the follow list is fetched once per session and filtered
// locally, and any name can be typed even if it is not followed.
type channelPickerState struct {
	open     bool
	query    string
	selected int
	loading  bool
	err      string
}

// channelPickerEntry is one row in the picker. Entries come from three
// sources, in this order: channels already open, the user's follow list, and
// a synthetic "open exactly what was typed" row for anything else.
type channelPickerEntry struct {
	login    string
	display  string
	open     bool
	followed bool
	// literal marks the synthetic row offering the typed name verbatim, so
	// an unfollowed channel is still one keystroke away.
	literal bool
}

type followedChannelsResolvedMsg struct {
	channels []twitch.FollowedChannel
	err      error
}

// openChannelPicker opens the overlay and, the first time it is used in a
// session, kicks off the follow-list fetch. The list is cached for the rest
// of the session: follows change rarely, and re-fetching on every open would
// make the picker feel slow for no benefit.
func (m *shellModel) openChannelPicker() tea.Cmd {
	if m.channelPicker.open {
		m.channelPicker = channelPickerState{}
		return nil
	}
	m.closeOtherOverlays(overlayChannels)
	m.channelPicker = channelPickerState{open: true}
	return m.scheduleFollowedChannelsLookup()
}

// scheduleFollowedChannelsLookup fetches the follow list once per session.
// A missing lookup (no credentials) or a failed request is not an error the
// user has to act on: the picker still lists open and configured channels
// and accepts any typed name, so the failure is reported inline and the
// request is not retried on every open.
func (m *shellModel) scheduleFollowedChannelsLookup() tea.Cmd {
	if m.services.followedChannels == nil || m.followedChannelsRequested {
		return nil
	}
	m.followedChannelsRequested = true
	m.channelPicker.loading = true
	lookup := m.services.followedChannels
	userLookup := m.services.userLookup
	username := m.effectiveConfig.Twitch.Username
	knownID := m.selfBroadcasterID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), channelPickerRequestTimeout)
		defer cancel()
		id := knownID
		if id == "" {
			resolved, err := resolveSelfBroadcasterID(ctx, userLookup, username)
			if err != nil {
				return followedChannelsResolvedMsg{err: err}
			}
			id = resolved
		}
		channels, err := lookup.GetFollowedChannels(ctx, id)
		return followedChannelsResolvedMsg{channels: channels, err: err}
	}
}

func (m *shellModel) applyFollowedChannels(msg followedChannelsResolvedMsg) {
	m.channelPicker.loading = false
	if msg.err != nil {
		// Tokens issued before user:read:follows existed land here; say so
		// rather than leaving an unexplained short list.
		if twitch.IsMissingScope(msg.err) {
			m.channelPicker.err = "follow list unavailable (run `twi login` to grant user:read:follows)"
		} else {
			m.channelPicker.err = msg.err.Error()
		}
		return
	}
	m.channelPicker.err = ""
	m.followedChannelList = msg.channels
	m.clampChannelPickerSelection()
}

func (m shellModel) handleChannelPickerKey(msg tea.KeyMsg) (shellModel, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.channelPicker = channelPickerState{}
		return m, nil
	case tea.KeyEnter:
		return m.commitChannelPickerSelection()
	case tea.KeyUp:
		m.moveChannelPickerSelection(-1)
		return m, nil
	case tea.KeyDown, tea.KeyTab:
		m.moveChannelPickerSelection(1)
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if n := len(m.channelPicker.query); n > 0 {
			_, size := utf8.DecodeLastRuneInString(m.channelPicker.query)
			m.channelPicker.query = m.channelPicker.query[:n-size]
		}
		m.channelPicker.selected = 0
		return m, nil
	case tea.KeyCtrlU:
		m.channelPicker.query = ""
		m.channelPicker.selected = 0
		return m, nil
	case tea.KeySpace:
		// Display names can contain spaces even though logins cannot, so the
		// query accepts them rather than treating space as a leader here.
		m.channelPicker.query += " "
		m.channelPicker.selected = 0
		return m, nil
	case tea.KeyRunes:
		m.channelPicker.query += string(msg.Runes)
		m.channelPicker.selected = 0
		return m, nil
	}
	return m, nil
}

func (m *shellModel) moveChannelPickerSelection(delta int) {
	entries := m.channelPickerEntries()
	if len(entries) == 0 {
		m.channelPicker.selected = 0
		return
	}
	m.channelPicker.selected += delta
	if m.channelPicker.selected < 0 {
		m.channelPicker.selected = len(entries) - 1
	}
	if m.channelPicker.selected >= len(entries) {
		m.channelPicker.selected = 0
	}
}

func (m *shellModel) clampChannelPickerSelection() {
	entries := m.channelPickerEntries()
	if len(entries) == 0 || m.channelPicker.selected < 0 {
		m.channelPicker.selected = 0
		return
	}
	if m.channelPicker.selected >= len(entries) {
		m.channelPicker.selected = len(entries) - 1
	}
}

// commitChannelPickerSelection opens the highlighted channel (or closes an
// already-open one, so the picker doubles as a switcher) and dismisses the
// overlay.
func (m shellModel) commitChannelPickerSelection() (shellModel, tea.Cmd) {
	entries := m.channelPickerEntries()
	index := m.channelPicker.selected
	if index < 0 || index >= len(entries) {
		m.channelPicker = channelPickerState{}
		return m, nil
	}
	login := entries[index].login
	m.channelPicker = channelPickerState{}
	return m.openChannel(login)
}

// channelPickerEntries builds the filtered, ordered row list: open channels
// first (they are the switch targets), then followed channels, then the
// literal typed name when it matches nothing already listed.
func (m shellModel) channelPickerEntries() []channelPickerEntry {
	query := strings.ToLower(strings.TrimSpace(m.channelPicker.query))
	query = strings.TrimPrefix(query, "#")

	entries := make([]channelPickerEntry, 0, len(m.followedChannelList)+len(m.channels.channelNames())+1)
	seen := make(map[string]bool)
	add := func(entry channelPickerEntry) {
		key := channelKey(entry.login)
		if key == "" || seen[key] {
			return
		}
		if query != "" && !strings.Contains(strings.ToLower(entry.login), query) &&
			!strings.Contains(strings.ToLower(entry.display), query) {
			return
		}
		seen[key] = true
		entries = append(entries, entry)
	}

	for _, name := range m.channels.channelNames() {
		add(channelPickerEntry{login: name, display: name, open: true})
	}
	followed := append([]twitch.FollowedChannel(nil), m.followedChannelList...)
	sort.SliceStable(followed, func(i, j int) bool {
		return strings.ToLower(followed[i].BroadcasterLogin) < strings.ToLower(followed[j].BroadcasterLogin)
	})
	for _, channel := range followed {
		add(channelPickerEntry{
			login:    channel.BroadcasterLogin,
			display:  channel.BroadcasterName,
			followed: true,
		})
	}
	// The configured defaults are worth offering even after being closed:
	// they are the channels this install is set up for.
	for _, name := range m.effectiveConfig.DefaultChannels {
		add(channelPickerEntry{login: name, display: name})
	}

	// The literal row goes last and only when the typed name is not already
	// listed: enter should default to the best real match, not to a partial
	// name that happens to be a prefix of one.
	if query != "" && !seen[channelKey(query)] {
		entries = append(entries, channelPickerEntry{login: query, display: query, literal: true})
	}
	return entries
}

func (m shellModel) channelPickerView(layout shellLayout) string {
	return m.renderOverlayPane(overlayPaneSpec{
		icon:          "📡",
		title:         "Open Channel",
		accent:        m.theme.Success,
		height:        layout.channelPickerHeight,
		contentHeight: layout.channelPickerContentHeight,
		framed:        layout.channelPickerFramed,
		lines: func(width, height int) []string {
			return m.channelPickerLines(width, height)
		},
	})
}

func (m shellModel) channelPickerLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	header := " Open channel (enter=open, esc=cancel)"
	switch {
	case m.channelPicker.loading:
		header = " Open channel: loading follows..."
	case m.channelPicker.err != "":
		header = " Open channel: " + m.channelPicker.err
	case m.channelPicker.query != "":
		header = " Open channel: " + m.channelPicker.query
	}
	lines := []string{fitLine(header, width)}
	if height == 1 {
		return lines
	}

	entries := m.channelPickerEntries()
	start, selected := pickerWindow(m.channelPicker.selected, len(entries), height)
	for i := start; i < len(entries) && len(lines) < height; i++ {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		lines = append(lines, fitLine(prefix+channelPickerEntryLabel(entries[i]), width))
	}
	for len(lines) < height {
		lines = append(lines, fitLine("", width))
	}
	return lines[:height]
}

func channelPickerEntryLabel(entry channelPickerEntry) string {
	label := "#" + entry.login
	if entry.display != "" && !strings.EqualFold(entry.display, entry.login) {
		label += " (" + entry.display + ")"
	}
	switch {
	case entry.open:
		label += " · open"
	case entry.followed:
		label += " · followed"
	case entry.literal:
		label += " · open by name"
	}
	return label
}
