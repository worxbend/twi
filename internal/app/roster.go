package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

const (
	// rosterMaxEntries bounds per-channel memory. Large channels can cycle
	// through far more logins than anyone will ever mention or inspect, so
	// the least recently seen entries are evicted past this point.
	rosterMaxEntries = 4096

	// rosterActiveWindow is how long a chatter counts as "active" after their
	// last message when Twitch is not sending membership for the channel.
	rosterActiveWindow = 10 * time.Minute
)

// chatterRoster is a best-effort per-channel view of who is in chat and what
// twi knows about them.
//
// Two independent signals feed it, and neither is complete on its own:
// membership JOIN/PART (accurate presence, but Twitch batches it, delays it,
// and stops sending it entirely for large channels) and chat messages
// (authoritative identity and badges, but only for people who actually
// speak). Presence therefore falls back to a recency window when membership
// is silent - see activeCount.
type chatterRoster struct {
	entries map[string]*chatterEntry
	// membershipSeen records whether Twitch has ever sent membership for this
	// channel. Until it has, presence is inferred from message recency alone.
	membershipSeen bool
}

// chatterEntry is everything twi knows about one chatter in one channel.
type chatterEntry struct {
	Login       string
	DisplayName string
	Color       string

	// Present is JOIN/PART state. It is only meaningful once the roster has
	// seen any membership traffic at all (see chatterRoster.membershipSeen).
	Present bool
	// FirstSeen is when twi first observed this chatter in this session, not
	// when they first appeared in the channel - twi has no way to know that.
	FirstSeen time.Time
	LastSeen  time.Time
	Messages  int

	Broadcaster bool
	Moderator   bool
	VIP         bool
	Subscriber  bool
	Staff       bool
	// SubscribedMonths is the cumulative tenure Twitch reports in the
	// subscriber badge's badge-info tag, or 0 when unknown.
	SubscribedMonths int

	// FollowsSince is set only for chatters twi has seen in a Get Channel
	// Followers page. FollowKnown distinguishes "confirmed follower" from
	// "twi has no follower data for this user", which is the common case:
	// twi only polls the most recent page of followers.
	FollowsSince time.Time
	FollowKnown  bool
}

func newChatterRoster() *chatterRoster {
	return &chatterRoster{entries: make(map[string]*chatterEntry)}
}

func (r *chatterRoster) ensure(login string, at time.Time) *chatterEntry {
	key := strings.ToLower(strings.TrimSpace(login))
	if r == nil || key == "" {
		return nil
	}
	if r.entries == nil {
		r.entries = make(map[string]*chatterEntry)
	}
	entry, ok := r.entries[key]
	if !ok {
		entry = &chatterEntry{Login: key, FirstSeen: at}
		r.entries[key] = entry
		r.evictOldest()
	}
	if entry.FirstSeen.IsZero() || (!at.IsZero() && at.Before(entry.FirstSeen)) {
		entry.FirstSeen = at
	}
	if at.After(entry.LastSeen) {
		entry.LastSeen = at
	}
	return entry
}

// evictOldest drops least-recently-seen entries once the roster exceeds its
// bound, keeping long-running sessions in busy channels from growing without
// limit.
func (r *chatterRoster) evictOldest() {
	if len(r.entries) <= rosterMaxEntries {
		return
	}
	keys := make([]string, 0, len(r.entries))
	for key := range r.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return r.entries[keys[i]].LastSeen.Before(r.entries[keys[j]].LastSeen)
	})
	for _, key := range keys[:len(r.entries)-rosterMaxEntries] {
		delete(r.entries, key)
	}
}

// observeMessage folds a chat message into the roster. Messages are the only
// source of display names, colors, and badges, so this is what populates the
// metadata that membership events cannot carry.
func (r *chatterRoster) observeMessage(message twitch.ChatMessage) {
	if r == nil {
		return
	}
	login := strings.TrimSpace(message.AuthorLogin)
	if login == "" {
		login = strings.TrimSpace(message.DisplayName)
	}
	at := message.Timestamp
	if at.IsZero() {
		at = time.Now()
	}
	entry := r.ensure(login, at)
	if entry == nil {
		return
	}
	if name := strings.TrimSpace(message.DisplayName); name != "" {
		entry.DisplayName = name
	}
	if color := strings.TrimSpace(message.AuthorColor); color != "" {
		entry.Color = color
	}
	// Anyone sending a message is in the channel, whatever membership last
	// said - a PART can arrive late or be missed entirely.
	entry.Present = true
	entry.Messages++
	applyBadgeRoles(entry, message.Badges)
}

// observeMembership folds a JOIN/PART into the roster.
func (r *chatterRoster) observeMembership(event twitch.MembershipEvent) {
	if r == nil {
		return
	}
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	r.membershipSeen = true
	entry := r.ensure(event.Login, at)
	if entry == nil {
		return
	}
	entry.Present = event.Type == twitch.MembershipJoin
}

// applyBadgeRoles maps Twitch badge sets onto role flags. Roles are sticky
// within a session: a message that arrives without badges (Twitch omits them
// in some contexts) must not silently demote a known moderator.
func applyBadgeRoles(entry *chatterEntry, badges []twitch.Badge) {
	for _, badge := range badges {
		switch strings.ToLower(strings.TrimSpace(badge.SetID)) {
		case "broadcaster":
			entry.Broadcaster = true
		case "moderator":
			entry.Moderator = true
		case "vip":
			entry.VIP = true
		case "subscriber", "founder":
			entry.Subscriber = true
			if months, err := strconv.Atoi(strings.TrimSpace(badge.Info)); err == nil && months > entry.SubscribedMonths {
				entry.SubscribedMonths = months
			}
		case "staff", "admin", "global_mod":
			entry.Staff = true
		}
	}
}

// applyFollowers marks chatters that appear in a Get Channel Followers page.
// Only logins already known to the roster are recorded, so polling followers
// never invents chatters who have not been seen in this channel.
func (r *chatterRoster) applyFollowers(followers []twitch.Follower) {
	if r == nil {
		return
	}
	for _, follower := range followers {
		key := strings.ToLower(strings.TrimSpace(follower.UserLogin))
		if key == "" {
			continue
		}
		entry, ok := r.entries[key]
		if !ok {
			continue
		}
		entry.FollowKnown = true
		entry.FollowsSince = follower.FollowedAt
	}
}

func (r *chatterRoster) lookup(login string) (*chatterEntry, bool) {
	if r == nil || r.entries == nil {
		return nil, false
	}
	entry, ok := r.entries[strings.ToLower(strings.TrimSpace(login))]
	return entry, ok
}

// activeCount reports how many chatters are currently considered active.
//
// When Twitch is sending membership for this channel, that is JOIN/PART
// presence. When it is not - large channels, or before the first batch
// arrives - it falls back to counting chatters who spoke within
// rosterActiveWindow, which is the only presence signal available.
func (r *chatterRoster) activeCount(now time.Time) int {
	if r == nil {
		return 0
	}
	count := 0
	for _, entry := range r.entries {
		if r.membershipSeen {
			if entry.Present {
				count++
			}
			continue
		}
		if entry.Messages > 0 && now.Sub(entry.LastSeen) <= rosterActiveWindow {
			count++
		}
	}
	return count
}

// completions returns logins matching prefix for @mention autocomplete,
// ranked by how recently each chatter was seen so the people currently
// talking come first. An empty prefix lists everyone, most recent first.
func (r *chatterRoster) completions(prefix string, limit int) []*chatterEntry {
	if r == nil || limit <= 0 {
		return nil
	}
	prefix = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(prefix, "@")))
	matches := make([]*chatterEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if prefix != "" &&
			!strings.HasPrefix(entry.Login, prefix) &&
			!strings.HasPrefix(strings.ToLower(entry.DisplayName), prefix) {
			continue
		}
		matches = append(matches, entry)
	}
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].LastSeen.Equal(matches[j].LastSeen) {
			return matches[i].LastSeen.After(matches[j].LastSeen)
		}
		return matches[i].Login < matches[j].Login
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// name returns the best display form for a chatter: the Twitch display name
// when known, otherwise the login.
func (e *chatterEntry) name() string {
	if e == nil {
		return ""
	}
	if name := strings.TrimSpace(e.DisplayName); name != "" {
		return name
	}
	return e.Login
}

// roleLabel returns the highest-precedence role for a chatter, or "" when
// they hold none. Precedence matches Twitch's own badge ordering.
func (e *chatterEntry) roleLabel() string {
	switch {
	case e == nil:
		return ""
	case e.Broadcaster:
		return "broadcaster"
	case e.Staff:
		return "staff"
	case e.Moderator:
		return "mod"
	case e.VIP:
		return "vip"
	case e.Subscriber:
		return "sub"
	default:
		return ""
	}
}
