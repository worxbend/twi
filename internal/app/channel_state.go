package app

import (
	"strings"
	"time"

	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/twitch"
)

type channelStateSet struct {
	order           []string
	active          string
	states          map[string]*channelState
	animationConfig animation.Config
	clock           animation.Clock
	// scrollbackLimit caps retained messages per channel. Zero or negative
	// means unbounded. See config.FeatureConfig.ScrollbackLimit.
	scrollbackLimit int
	// placeholder backs the no-channels-open empty state. It is never in
	// order or states, so it cannot be joined, switched to, or shown in the
	// sidebar; it exists only so every view and key handler still has a
	// *channelState to read from while nothing is open.
	placeholder *channelState
}

type channelState struct {
	name           string
	status         ConnectionState
	messages       []twitch.ChatMessage
	scrollOffset   int
	messageFilters messageFilterSet
	revealQueue    *animation.Queue
	roster         *chatterRoster
	// selfBadges/selfDisplayName/selfColor mirror the latest USERSTATE for
	// this channel: the authenticated user's own identity, which their local
	// echo has no other source for.
	selfBadges             []twitch.Badge
	selfDisplayName        string
	selfColor              string
	activeOrder            []string
	activeMessages         map[string]twitch.ChatMessage
	localEchoes            map[string]struct{}
	unread                 int
	composerText           string
	replyTo                *composerReplyContext
	activeSend             *queuedComposerSend
	sendQueue              []queuedComposerSend
	sendState              composerSendState
	sendFeedback           string
	broadcasterID          string
	broadcasterIDRequested bool
	live                   bool
	liveStatusKnown        bool
	liveSince              time.Time
	viewerCount            int
}

func newChannelStateSet(channels []string, animationConfig animation.Config, clock animation.Clock, scrollbackLimit int) *channelStateSet {
	set := &channelStateSet{
		states:          make(map[string]*channelState),
		animationConfig: animationConfig,
		clock:           clock,
		scrollbackLimit: scrollbackLimit,
	}
	for _, channel := range channels {
		set.ensure(channel)
	}
	if len(set.order) > 0 {
		set.active = set.order[0]
	}
	return set
}

// empty reports whether no channel is open, which is a normal startup state:
// `twi chat` without --channel/--channels launches here and waits for the
// /channels picker.
func (s *channelStateSet) empty() bool {
	return s == nil || len(s.order) == 0
}

func (s *channelStateSet) activeState() *channelState {
	if s == nil {
		return nil
	}
	if s.empty() {
		return s.emptyState()
	}
	return s.ensure(s.active)
}

// emptyState lazily builds the placeholder used while nothing is open. It is
// rebuilt on demand rather than at construction so the common case (channels
// configured up front) allocates nothing extra.
func (s *channelStateSet) emptyState() *channelState {
	if s.placeholder == nil {
		s.placeholder = s.newState("")
	}
	return s.placeholder
}

func (s *channelStateSet) activeName() string {
	if s.empty() {
		return ""
	}
	if state := s.activeState(); state != nil {
		return state.name
	}
	return ""
}

// open joins a channel and makes it active, returning false when it was
// already open and active. Reopening an already-open channel just switches
// to it, so the picker never creates duplicates.
func (s *channelStateSet) open(channel string) bool {
	if s == nil || normalizeChannelName(channel) == "" {
		return false
	}
	state := s.ensure(channel)
	if state == nil {
		return false
	}
	// ensure() already made it active if this was the first channel, in
	// which case setActive reports no change but the set did change.
	if s.active == channelKey(state.name) {
		state.unread = 0
		return true
	}
	return s.setActive(state.name)
}

// close removes a channel from the set. When the closed channel was active,
// focus moves to its neighbor so the sidebar selection stays where the eye
// already is; closing the last channel leaves the set empty.
func (s *channelStateSet) close(channel string) bool {
	if s == nil {
		return false
	}
	key := channelKey(channel)
	index := -1
	for i, existing := range s.order {
		if existing == key {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	s.order = append(s.order[:index], s.order[index+1:]...)
	delete(s.states, key)
	if s.active != key {
		return true
	}
	if len(s.order) == 0 {
		s.active = ""
		return true
	}
	if index >= len(s.order) {
		index = len(s.order) - 1
	}
	s.active = s.order[index]
	if state := s.states[s.active]; state != nil {
		state.unread = 0
	}
	return true
}

func (s *channelStateSet) ensure(channel string) *channelState {
	if s == nil {
		return nil
	}
	name := normalizeChannelName(channel)
	if name == "" {
		name = normalizeChannelName(s.active)
	}
	// An unnamed channel with nothing active is the empty state, not a
	// channel to invent a name for.
	if name == "" {
		return s.emptyState()
	}
	key := channelKey(name)
	if state, ok := s.states[key]; ok {
		return state
	}
	state := s.newState(name)
	s.states[key] = state
	s.order = append(s.order, key)
	if s.active == "" {
		s.active = key
	}
	return state
}

func (s *channelStateSet) newState(name string) *channelState {
	return &channelState{
		name:           name,
		status:         ConnectionState{Status: ConnectionDisconnected, Channel: name},
		revealQueue:    animation.NewQueue(s.animationConfig, s.clock),
		roster:         newChatterRoster(),
		activeMessages: make(map[string]twitch.ChatMessage),
		localEchoes:    make(map[string]struct{}),
	}
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
	state.roster.observeMessage(message)
	inactive := channelKey(state.name) != s.active
	if inactive {
		state.messages = append(state.messages, message)
		state.trimScrollback(s.scrollbackLimit)
		state.unread++
		return state, false
	}
	return state, true
}

// trimScrollback drops the oldest messages once the channel exceeds limit.
//
// Retained messages are re-rendered on every repaint, so an untrimmed buffer
// makes frame time grow without bound over a long session. Trimming from the
// head keeps the newest history, which is the part anyone is reading.
//
// A non-zero scrollOffset is measured from the bottom of the buffer, so
// dropping from the head does not shift what the viewer is looking at and the
// offset is deliberately left alone. It is only clamped, by the caller's
// clampScroll, once the buffer is short enough that the old offset would
// scroll past the top.
func (s *channelState) trimScrollback(limit int) {
	if s == nil || limit <= 0 || len(s.messages) <= limit {
		return
	}
	drop := len(s.messages) - limit
	// Reslice into a fresh backing array rather than sliding in place: the
	// old array keeps every dropped message alive otherwise, which defeats
	// the point of trimming on a long stream.
	kept := make([]twitch.ChatMessage, limit)
	copy(kept, s.messages[drop:])
	s.messages = kept
}

// applyMembership folds a JOIN/PART into the target channel's roster and
// returns the affected state so the caller can log the event.
func (s *channelStateSet) applyMembership(event twitch.MembershipEvent) *channelState {
	if s == nil {
		return nil
	}
	state := s.ensure(event.Channel)
	if state == nil {
		return nil
	}
	state.roster.observeMembership(event)
	return state
}

func (s *channelStateSet) applyConnectionState(state ConnectionState) *channelState {
	channel := state.Channel
	if channel == "" {
		var first *channelState
		for _, key := range s.order {
			ch := s.states[key]
			if ch == nil {
				continue
			}
			next := state
			next.Channel = ch.name
			ch.status = next
			if first == nil {
				first = ch
			}
		}
		if first != nil {
			return first
		}
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
