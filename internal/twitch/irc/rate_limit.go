package irc

import (
	"sync"
	"time"

	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
)

// All destinations share the normal account bucket; proven privileged
// destinations may continue into the documented larger 100/30s bucket.
const (
	chatSendLimit  = 20
	chatSendWindow = 30 * time.Second
	// duplicateWindow is how long Twitch remembers a message well enough to
	// reject an identical repeat with msg_duplicate.
	duplicateWindow = 30 * time.Second
)

// sendLimiter enforces the outbound message allowance and Twitch's
// duplicate-message rule.
//
// SendResult.RateLimited and the composer's rate-limited state both existed
// before this did, with nothing to set them: hitting the ceiling looked
// exactly like a successful send, right up until Twitch closed the
// connection.
type sendLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	sent  []time.Time
	last  map[string]time.Time
	limit int
}

func newSendLimiter(now func() time.Time) *sendLimiter {
	if now == nil {
		now = time.Now
	}
	return &sendLimiter{now: now, last: make(map[string]time.Time), limit: chatSendLimit}
}

// allow reports whether a message may be sent now, and records it when so.
// A rejection names which rule was hit, because "slow down" and "Twitch will
// reject this as a duplicate" call for different responses from the sender.
func (l *sendLimiter) allow(channel, text string) error {
	return l.allowForRole(channel, text, sendquota.RoleNormal)
}
func (l *sendLimiter) allowForRole(channel, text string, role sendquota.Role) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)

	limit := l.limit
	if role.Privileged() {
		limit = 100
	}
	if len(l.sent) >= limit {
		return twitch.ErrRateLimited
	}
	key := channel + "\x00" + text
	if at, ok := l.last[key]; ok && now.Sub(at) < duplicateWindow {
		return twitch.ErrDuplicateMessage
	}

	l.sent = append(l.sent, now)
	l.last[key] = now
	return nil
}

func (l *sendLimiter) prune(now time.Time) {
	cutoff := now.Add(-chatSendWindow)
	kept := l.sent[:0]
	for _, at := range l.sent {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	l.sent = kept

	dupCutoff := now.Add(-duplicateWindow)
	for key, at := range l.last {
		if !at.After(dupCutoff) {
			delete(l.last, key)
		}
	}
}
