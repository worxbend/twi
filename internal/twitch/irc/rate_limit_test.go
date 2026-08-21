package irc

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time      { return c.now }
func (c *stepClock) add(d time.Duration) { c.now = c.now.Add(d) }

// TestSendLimiterRefusesPastTwitchAllowance is the regression for a limit
// that existed only in the type system. SendResult.RateLimited and the
// composer's rate-limited state were both present with nothing setting them,
// so exceeding Twitch's 20-per-30s ceiling looked exactly like a successful
// send -- right up until Twitch closed the connection.
func TestSendLimiterRefusesPastTwitchAllowance(t *testing.T) {
	clock := &stepClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	limiter := newSendLimiter(clock.Now)

	for i := range chatSendLimit {
		if err := limiter.allow("example", fmt.Sprintf("message %d", i)); err != nil {
			t.Fatalf("message %d refused within the allowance: %v", i, err)
		}
		clock.add(100 * time.Millisecond)
	}
	if err := limiter.allow("example", "one too many"); !errors.Is(err, twitch.ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited past the allowance", err)
	}
}

func TestSendLimiterRecoversAfterTheWindow(t *testing.T) {
	clock := &stepClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	limiter := newSendLimiter(clock.Now)

	for i := range chatSendLimit {
		if err := limiter.allow("example", fmt.Sprintf("message %d", i)); err != nil {
			t.Fatalf("message %d refused: %v", i, err)
		}
	}
	clock.add(chatSendWindow + time.Second)
	if err := limiter.allow("example", "after the window"); err != nil {
		t.Fatalf("send after the window returned %v, want nil", err)
	}
}

// TestSendLimiterRejectsDuplicates covers Twitch's msg_duplicate rule, which
// silently drops an identical repeat.
func TestSendLimiterRejectsDuplicates(t *testing.T) {
	clock := &stepClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	limiter := newSendLimiter(clock.Now)

	if err := limiter.allow("example", "gg"); err != nil {
		t.Fatalf("first send returned %v, want nil", err)
	}
	if err := limiter.allow("example", "gg"); !errors.Is(err, twitch.ErrDuplicateMessage) {
		t.Fatalf("error = %v, want ErrDuplicateMessage", err)
	}
	// A different channel is not a duplicate.
	if err := limiter.allow("other", "gg"); err != nil {
		t.Fatalf("same text in another channel returned %v, want nil", err)
	}
	// Neither is the same text once Twitch has forgotten it.
	clock.add(duplicateWindow + time.Second)
	if err := limiter.allow("example", "gg"); err != nil {
		t.Fatalf("repeat after the duplicate window returned %v, want nil", err)
	}
}

func TestSendLimiterAllowsDistinctMessages(t *testing.T) {
	clock := &stepClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	limiter := newSendLimiter(clock.Now)
	for _, text := range []string{"gg", "wp", "nice play", "gg"} {
		clock.add(duplicateWindow + time.Second)
		if err := limiter.allow("example", text); err != nil {
			t.Fatalf("send %q returned %v, want nil", text, err)
		}
	}
}

func TestSendLimiterIsSafeForConcurrentUse(t *testing.T) {
	limiter := newSendLimiter(time.Now)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			_ = limiter.allow("example", fmt.Sprintf("a%d", i))
		}
	}()
	for i := range 100 {
		_ = limiter.allow("other", fmt.Sprintf("b%d", i))
	}
	<-done
}
