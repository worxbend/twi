package app

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/w0rxbend/twi/internal/assets"
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
// ID via the same AvatarResolver already wired for author avatars, so
// channel-specific emote lookup (which Twitch Helix keys by broadcaster ID,
// not login) needs no separate credential plumbing. A missing/failed lookup
// just means emote search falls back to global emotes only.
func (m *mockShellModel) scheduleBroadcasterIDLookup() tea.Cmd {
	if m.avatarResolver == nil {
		return nil
	}
	channel := m.activeChannelName()
	state := m.channels.ensure(channel)
	if state == nil || state.broadcasterID != "" || state.broadcasterIDRequested {
		return nil
	}
	state.broadcasterIDRequested = true
	resolver := m.avatarResolver
	key := channelKey(channel)
	return func() tea.Msg {
		results, err := resolver.ResolveAvatars(context.Background(), []assets.AvatarRequest{{UserLogin: channel}})
		var userID string
		if err == nil {
			for _, result := range results {
				if strings.EqualFold(result.UserLogin, channel) && result.UserID != "" {
					userID = result.UserID
					break
				}
			}
		}
		return broadcasterIDResolvedMsg{channel: key, userID: userID}
	}
}

func (m *mockShellModel) applyBroadcasterIDResult(msg broadcasterIDResolvedMsg) {
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
func (m *mockShellModel) scheduleEmoteIndexLookup() tea.Cmd {
	if m.emoteIndex == nil {
		return nil
	}
	channel := m.activeChannelName()
	key := channelKey(channel)
	if _, ok := m.emoteEntries[key]; ok {
		return nil
	}
	if m.emoteEntriesRequested == nil {
		m.emoteEntriesRequested = make(map[string]bool)
	}
	if m.emoteEntriesRequested[key] {
		return nil
	}
	m.emoteEntriesRequested[key] = true
	broadcasterID := m.activeChannelState().broadcasterID
	index := m.emoteIndex
	return func() tea.Msg {
		entries, err := index.Load(context.Background(), broadcasterID)
		return emoteIndexResolvedMsg{channel: key, entries: entries, err: err}
	}
}

func (m *mockShellModel) applyEmoteIndexResult(msg emoteIndexResolvedMsg) {
	if msg.err != nil {
		delete(m.emoteEntriesRequested, msg.channel)
		return
	}
	if m.emoteEntries == nil {
		m.emoteEntries = make(map[string][]assets.EmoteEntry)
	}
	m.emoteEntries[msg.channel] = msg.entries
}

// activeEmoteEntries returns the searchable/quick-select emote list for the
// active channel, or nil while it hasn't resolved yet.
func (m mockShellModel) activeEmoteEntries() []assets.EmoteEntry {
	return m.emoteEntries[channelKey(m.activeChannelName())]
}
