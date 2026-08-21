package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

const maxActivityEntries = 200

// activityKind coarsely categorizes an activity log entry for potential
// future filtering/styling. Twitch's IRC system events (raids, subs, gift
// subs, etc.) map to activityIRCEvent; new followers map to activityFollow
// (detected by polling and diffing Get Channel Followers, since Twitch only
// pushes follow events through EventSub - a separate WebSocket push API -
// not IRC or any plain polling endpoint with real-time semantics); cheers
// map to activityCheer (detected from a PRIVMSG's "bits" tag, since Twitch
// sends cheers as ordinary chat messages, not a USERNOTICE); and stream
// live/offline transitions map to activityStreamStatus (detected by polling
// and diffing Get Streams, the same status the LIVE/OFFLINE status-bar
// badge already uses).
type activityKind string

const (
	activityFollow       activityKind = "follow"
	activityIRCEvent     activityKind = "irc_event"
	activityClip         activityKind = "clip"
	activityCheer        activityKind = "cheer"
	activityStreamStatus activityKind = "stream_status"
	activityMembership   activityKind = "membership"
	activityChannel      activityKind = "channel"
)

// membershipActivityBurst is the number of membership entries twi will log in
// one burst window before collapsing the rest into a single summary row.
// Twitch delivers JOINs in large batches on connect - logging each one would
// bury every other activity kind under hundreds of "x joined" rows.
const membershipActivityBurst = 5

// membershipBurstWindow is how close together membership events must arrive
// to count as one burst.
const membershipBurstWindow = 2 * time.Second

// activityEntry is one row in the activity log column.
type activityEntry struct {
	Kind    activityKind
	Channel string
	Text    string
	At      time.Time
}

// recordActivityFromMessage appends an activity log entry for a Twitch
// system event twi already classifies for desktop notifications (raid, sub,
// resub, gift sub, etc. - see systemNotificationFromMessage/systemEventLabel
// in system_notification.go), or for a cheer (a plain chat message carrying
// a "bits" tag, which systemNotificationFromMessage never classifies since
// it isn't a USERNOTICE). Any other plain chat message produces no activity
// entry.
func (m *shellModel) recordActivityFromMessage(message twitch.ChatMessage) {
	at := message.Timestamp
	if at.IsZero() {
		at = time.Now()
	}

	if message.Bits > 0 {
		m.appendActivity(activityEntry{
			Kind:    activityCheer,
			Channel: normalizeChannelName(message.Channel),
			Text:    cheerActivityText(message),
			At:      at,
		})
	}

	notification, ok := systemNotificationFromMessage(message)
	if !ok {
		return
	}
	// "system" is twi's own catch-all for MessageTypeSystem placeholders
	// (e.g. mock-mode banners); it isn't a real Twitch event worth logging.
	if strings.EqualFold(notification.EventID, "system") {
		return
	}
	m.appendActivity(activityEntry{
		Kind:    activityIRCEvent,
		Channel: notification.Channel,
		Text:    systemNotificationSummary(notification),
		At:      at,
	})
}

func cheerActivityText(message twitch.ChatMessage) string {
	name := strings.TrimSpace(message.DisplayName)
	if name == "" {
		name = strings.TrimSpace(message.AuthorLogin)
	}
	if name == "" {
		name = "someone"
	}
	unit := "bits"
	if message.Bits == 1 {
		unit = "bit"
	}
	return fmt.Sprintf("%s cheered %d %s", name, message.Bits, unit)
}

// applyMembershipEvent folds a JOIN/PART into the target channel's roster and
// logs it in the activity column.
//
// Bursts are collapsed: Twitch sends membership for everyone already in the
// channel immediately after connecting, so the first few are logged
// individually and the remainder of the burst becomes one rolling
// "N more joined" row that is rewritten in place.
func (m *shellModel) applyMembershipEvent(event twitch.MembershipEvent) {
	state := m.channels.applyMembership(event)
	if state == nil {
		return
	}
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	login := strings.TrimSpace(event.Login)
	if login == "" {
		return
	}

	if at.Sub(m.activity.membershipBurstAt) > membershipBurstWindow {
		m.activity.membershipBurstCount = 0
		m.activity.membershipBurstIndex = -1
	}
	m.activity.membershipBurstAt = at
	m.activity.membershipBurstCount++

	if m.activity.membershipBurstCount <= membershipActivityBurst {
		verb := "joined"
		if event.Type == twitch.MembershipPart {
			verb = "left"
		}
		m.appendActivity(activityEntry{
			Kind:    activityMembership,
			Channel: state.name,
			Text:    login + " " + verb,
			At:      at,
		})
		return
	}

	collapsed := m.activity.membershipBurstCount - membershipActivityBurst
	summary := activityEntry{
		Kind:    activityMembership,
		Channel: state.name,
		Text:    fmt.Sprintf("+%d more joined/left", collapsed),
		At:      at,
	}
	// Rewrite the existing summary row in place while the burst continues so
	// one noisy reconnect costs the log a single line, not hundreds.
	if m.activity.membershipBurstIndex >= 0 && m.activity.membershipBurstIndex < len(m.activity.activityLog) &&
		m.activity.activityLog[m.activity.membershipBurstIndex].Kind == activityMembership {
		m.activity.activityLog[m.activity.membershipBurstIndex] = summary
		return
	}
	m.appendActivity(summary)
	m.activity.membershipBurstIndex = len(m.activity.activityLog) - 1
}

func (m *shellModel) appendActivity(entry activityEntry) {
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	m.activity.activityLog = append(m.activity.activityLog, entry)
	if len(m.activity.activityLog) > maxActivityEntries {
		trimmed := len(m.activity.activityLog) - maxActivityEntries
		m.activity.activityLog = m.activity.activityLog[trimmed:]
		// Trimming shifts every index left; keep the in-place membership
		// summary row pointing at the same entry, or drop it if it aged out.
		m.activity.membershipBurstIndex -= trimmed
		if m.activity.membershipBurstIndex < 0 {
			m.activity.membershipBurstIndex = -1
		}
	}
}

// applyNewFollowerActivity diffs the latest Get Channel Followers page
// against the previously seen follower IDs and appends one activity entry
// per newly seen follower, oldest first. The very first poll only
// establishes the seen-set as a baseline; it never floods the log by
// treating every existing follower as "new".
func (m *shellModel) applyNewFollowerActivity(page []twitch.Follower) {
	hadBaseline := m.activity.seenFollowerIDs != nil
	if m.activity.seenFollowerIDs == nil {
		m.activity.seenFollowerIDs = make(map[string]bool, len(page))
	}
	for i := len(page) - 1; i >= 0; i-- {
		follower := page[i]
		if follower.UserID == "" || m.activity.seenFollowerIDs[follower.UserID] {
			continue
		}
		m.activity.seenFollowerIDs[follower.UserID] = true
		if !hadBaseline {
			continue
		}
		name := follower.UserName
		if name == "" {
			name = follower.UserLogin
		}
		m.appendActivity(activityEntry{
			Kind: activityFollow,
			Text: name + " followed",
			At:   follower.FollowedAt,
		})
	}
}
