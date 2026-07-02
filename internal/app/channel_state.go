package app

import (
	"strings"

	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/twitch"
)

type channelStateSet struct {
	order           []string
	active          string
	states          map[string]*channelState
	animationConfig animation.Config
	clock           animation.Clock
}

type channelState struct {
	name           string
	status         ConnectionState
	messages       []twitch.ChatMessage
	scrollOffset   int
	revealQueue    *animation.Queue
	activeOrder    []string
	activeMessages map[string]twitch.ChatMessage
	unread         int
}

func newChannelStateSet(channels []string, animationConfig animation.Config, clock animation.Clock) *channelStateSet {
	set := &channelStateSet{
		states:          make(map[string]*channelState),
		animationConfig: animationConfig,
		clock:           clock,
	}
	for _, channel := range channels {
		set.ensure(channel)
	}
	if len(set.order) == 0 {
		set.ensure("chat")
	}
	set.active = set.order[0]
	return set
}

func (s *channelStateSet) activeState() *channelState {
	if s == nil {
		return nil
	}
	return s.ensure(s.active)
}

func (s *channelStateSet) activeName() string {
	if state := s.activeState(); state != nil {
		return state.name
	}
	return "chat"
}

func (s *channelStateSet) ensure(channel string) *channelState {
	if s == nil {
		return nil
	}
	name := normalizeChannelName(channel)
	if name == "" {
		name = normalizeChannelName(s.active)
	}
	if name == "" {
		name = "chat"
	}
	key := channelKey(name)
	if state, ok := s.states[key]; ok {
		return state
	}
	state := &channelState{
		name:           name,
		status:         ConnectionState{Status: ConnectionDisconnected, Channel: name},
		revealQueue:    animation.NewQueue(s.animationConfig, s.clock),
		activeMessages: make(map[string]twitch.ChatMessage),
	}
	s.states[key] = state
	s.order = append(s.order, key)
	if s.active == "" {
		s.active = key
	}
	return state
}

func (s *channelStateSet) setActive(channel string) bool {
	state := s.ensure(channel)
	if state == nil {
		return false
	}
	key := channelKey(state.name)
	if s.active == key {
		state.unread = 0
		return false
	}
	s.active = key
	state.unread = 0
	return true
}

func (s *channelStateSet) switchBy(delta int) bool {
	if s == nil || len(s.order) <= 1 || delta == 0 {
		return false
	}
	active := 0
	for i, key := range s.order {
		if key == s.active {
			active = i
			break
		}
	}
	next := (active + delta) % len(s.order)
	if next < 0 {
		next += len(s.order)
	}
	return s.setActive(s.states[s.order[next]].name)
}

func (s *channelStateSet) applyMessage(message twitch.ChatMessage) (*channelState, bool) {
	state := s.ensure(message.Channel)
	if state == nil {
		return nil, false
	}
	message.Channel = state.name
	inactive := channelKey(state.name) != s.active
	if inactive {
		state.messages = append(state.messages, message)
		state.unread++
		return state, false
	}
	return state, true
}

func (s *channelStateSet) applyConnectionState(state ConnectionState) *channelState {
	channel := state.Channel
	if channel == "" {
		channel = s.activeName()
	}
	ch := s.ensure(channel)
	if ch == nil {
		return nil
	}
	state.Channel = ch.name
	ch.status = state
	return ch
}

func (s *channelStateSet) totalUnread() int {
	total := 0
	if s == nil {
		return total
	}
	for _, state := range s.states {
		total += state.unread
	}
	return total
}

func (s *channelStateSet) channelNames() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0, len(s.order))
	for _, key := range s.order {
		if state := s.states[key]; state != nil {
			names = append(names, state.name)
		}
	}
	return names
}

func configuredChannels(primary string, configured []string) []string {
	channels := make([]string, 0, len(configured)+1)
	seen := make(map[string]bool)
	add := func(channel string) {
		name := normalizeChannelName(channel)
		if name == "" {
			return
		}
		key := channelKey(name)
		if seen[key] {
			return
		}
		seen[key] = true
		channels = append(channels, name)
	}
	add(primary)
	for _, channel := range configured {
		add(channel)
	}
	if len(channels) == 0 {
		channels = append(channels, "chat")
	}
	return channels
}

func normalizeChannelName(channel string) string {
	channel = strings.TrimSpace(channel)
	channel = strings.TrimPrefix(channel, "#")
	return channel
}

func channelKey(channel string) string {
	return strings.ToLower(normalizeChannelName(channel))
}
