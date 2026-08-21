package app

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/twitch"
)

type broadcasterIDResolvedMsg struct {
	channel string
	userID  string
}

type emoteIndexResolvedMsg struct {
	channel string
	entries []assets.EmoteEntry
	err     error
}

// scheduleBroadcasterIDLookup resolves the active channel's broadcaster user
// ID via the shared Twitch user lookup, so channel-specific emote lookup
// (which Twitch Helix keys by broadcaster ID, not login) needs no separate
// credential plumbing. A missing/failed lookup just means emote search falls
// back to global emotes only.
func (m *shellModel) scheduleBroadcasterIDLookup() tea.Cmd {
	if m.services.userLookup == nil {
		return nil
	}
	channel := m.activeChannelName()
	state := m.channels.ensure(channel)
	if state == nil || state.broadcasterID != "" || state.broadcasterIDRequested {
		return nil
	}
	state.broadcasterIDRequested = true
	lookup := m.services.userLookup
	key := channelKey(channel)
	return func() tea.Msg {
		results, err := lookup.GetUsers(context.Background(), twitch.UserLookupRequest{UserLogins: []string{channel}})
		var userID string
		if err == nil {
			for _, result := range results {
				if strings.EqualFold(result.Login, channel) && result.UserID != "" {
					userID = result.UserID
					break
				}
			}
		}
		return broadcasterIDResolvedMsg{channel: key, userID: userID}
	}
}

func (m *shellModel) applyBroadcasterIDResult(msg broadcasterIDResolvedMsg) {
	if msg.userID == "" {
		return
	}
	if state, ok := m.channels.states[msg.channel]; ok && state != nil {
		state.broadcasterID = msg.userID
	}
}

// scheduleEmoteIndexLookup fetches the active channel's searchable emote set
// (global + channel-specific) once per channel; the EmoteIndex itself
// handles TTL-based refresh, so this only needs to guard against re-issuing
// a redundant in-flight request for a channel already resolved this session.
func (m *shellModel) scheduleEmoteIndexLookup() tea.Cmd {
	if m.emotes.emoteIndex == nil {
		return nil
	}
	channel := m.activeChannelName()
	key := channelKey(channel)
	if _, ok := m.emotes.emoteEntries[key]; ok {
		return nil
	}
	if m.emotes.emoteEntriesRequested == nil {
		m.emotes.emoteEntriesRequested = make(map[string]bool)
	}
	if m.emotes.emoteEntriesRequested[key] {
		return nil
	}
	m.emotes.emoteEntriesRequested[key] = true
	broadcasterID := m.activeChannelState().broadcasterID
	index := m.emotes.emoteIndex
	return func() tea.Msg {
		entries, err := index.Load(context.Background(), broadcasterID)
		return emoteIndexResolvedMsg{channel: key, entries: entries, err: err}
	}
}

func (m *shellModel) applyEmoteIndexResult(msg emoteIndexResolvedMsg) {
	if msg.err != nil {
		delete(m.emotes.emoteEntriesRequested, msg.channel)
		return
	}
	activeChannel := msg.channel == channelKey(m.activeChannelName())
	pickerSelection := ""
	if activeChannel {
		pickerSelection = selectedEmoteName(m.visibleEmotePickerEntries(), m.emotePicker.selected)
	}
	if m.emotes.emoteEntries == nil {
		m.emotes.emoteEntries = make(map[string][]assets.EmoteEntry)
	}
	m.emotes.emoteEntries[msg.channel] = msg.entries
	if activeChannel {
		m.emotePicker.selected = emoteIndexByName(m.visibleEmotePickerEntries(), pickerSelection, m.emotePicker.selected)
	}
}

func selectedEmoteName(entries []assets.EmoteEntry, selected int) string {
	if selected < 0 || selected >= len(entries) {
		return ""
	}
	return entries[selected].Name
}

func emoteIndexByName(entries []assets.EmoteEntry, name string, fallback int) int {
	if name != "" {
		for index, entry := range entries {
			if entry.Name == name {
				return index
			}
		}
	}
	if len(entries) == 0 {
		return 0
	}
	if fallback < 0 {
		return 0
	}
	if fallback >= len(entries) {
		return len(entries) - 1
	}
	return fallback
}

// activeEmoteEntries returns the searchable emote list for the active
// channel. Terminal-native emoji remain available before Twitch emotes resolve
// or when that lookup is disabled; resolved entries are merged without
// duplicates and retain their provider ordering.
func (m shellModel) activeEmoteEntries() []assets.EmoteEntry {
	resolved := m.emotes.emoteEntries[channelKey(m.activeChannelName())]
	entries := make([]assets.EmoteEntry, 0, len(resolved)+10)
	seen := make(map[string]bool, len(resolved)+10)
	for _, entry := range resolved {
		if entry.Name == "" || seen[entry.Name] {
			continue
		}
		entries = append(entries, entry)
		seen[entry.Name] = true
	}
	for _, entry := range builtInEmojiEntries() {
		if seen[entry.Name] {
			continue
		}
		entries = append(entries, entry)
		seen[entry.Name] = true
	}
	return entries
}
