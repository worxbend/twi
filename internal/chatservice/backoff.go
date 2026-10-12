package chatservice

import (
	"math/rand/v2"
	"time"
)

// ReconnectDelay supplies shared capped exponential pacing with jitter. The
// caller owns waiting and cancellation; attempts are one-based.
func ReconnectDelay(attempt int, initial, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if initial <= 0 {
		initial = 250 * time.Millisecond
	}
	if maximum < initial {
		maximum = initial
	}
	delay := initial
	for i := 1; i < attempt && delay < maximum; i++ {
		if delay > maximum/2 {
			delay = maximum
		} else {
			delay *= 2
		}
	}
	delay = min(delay, maximum)
	// Keep jitter below the cap, avoiding a synchronized restart after an outage.
	return delay - delay/5 + time.Duration(rand.Int64N(max(int64(delay/5), 1)))
}
