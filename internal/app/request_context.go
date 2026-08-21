package app

import (
	"context"
	"time"
)

// twitchRequestTimeout bounds a single Twitch API call made from the shell.
//
// Every such call happens inside a Bubble Tea command, which is a goroutine
// whose only job is to deliver one message back to the update loop. Without a
// deadline a stalled connection leaves that goroutine parked forever: the
// message never arrives, the feature it backs stays blank with no error, and
// the goroutine survives until the process exits.
const twitchRequestTimeout = 5 * time.Second

// lifetimeContext returns the context that is cancelled when the shell exits.
//
// It falls back to context.Background() because most tests build a shellModel
// directly rather than going through RunClientWithOptions, and those models
// have no lifetime to inherit. Returning a usable context keeps every such test
// working without having to thread a context through their constructors.
func (m shellModel) lifetimeContext() context.Context {
	if m.lifetime == nil {
		return context.Background()
	}
	return m.lifetime
}

// requestContext derives the context for one outbound Twitch call: bounded by
// timeout, and cancelled early if the user quits before the call finishes.
//
// Call it inside the Bubble Tea command closure, not outside, so the deadline
// starts when the request starts rather than when the command was created. The
// caller must defer the returned cancel.
func (m shellModel) requestContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(m.lifetimeContext(), timeout)
}
