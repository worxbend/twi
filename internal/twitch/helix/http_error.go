package helix

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/textsafe"
)

// HTTPError retains safe server status and quota information through error wrapping.
// Ambiguous means a write may have reached Twitch without a reliable acceptance response.
type HTTPError struct {
	Operation          string
	Endpoint           string
	StatusCode         int
	Detail             string
	RetryAfter         time.Duration
	RateLimitReset     time.Time
	RateLimitRemaining int
	RateLimitLimit     int
	Ambiguous          bool
}

func (e *HTTPError) Error() string {
	if e.StatusCode == 0 {
		return e.Operation + ": " + e.Detail
	}
	detail := ""
	if e.Detail != "" {
		detail = ": " + e.Detail
	}
	return fmt.Sprintf("%s: twitch %s returned HTTP %d%s", e.Operation, e.Endpoint, e.StatusCode, detail)
}
func (e *HTTPError) RetryableRead() bool {
	return e.StatusCode == 429 || e.StatusCode == 502 || e.StatusCode == 503 || e.StatusCode == 504
}
func (e *HTTPError) RetryableSend() bool {
	return e.StatusCode == http.StatusTooManyRequests && !e.Ambiguous
}

// RetryPolicy only permits retries when the response explicitly rejected the request.
// Unknown write outcomes never enter this policy. WaitFunc and Now make pacing testable.
type RetryPolicy struct {
	MaxRetries int
	Wait       bool
	MaxDelay   time.Duration
	Now        func() time.Time
	WaitFunc   func(context.Context, time.Duration) error
}

func normalizeRetryPolicy(p *RetryPolicy) RetryPolicy {
	value := RetryPolicy{MaxRetries: 2, Wait: true, MaxDelay: 2 * time.Minute, Now: time.Now, WaitFunc: waitForRetry}
	if p != nil {
		value = *p
		if value.Now == nil {
			value.Now = time.Now
		}
		if value.WaitFunc == nil {
			value.WaitFunc = waitForRetry
		}
		if value.MaxDelay <= 0 {
			value.MaxDelay = 2 * time.Minute
		}
	}
	if value.MaxRetries < 0 {
		value.MaxRetries = 0
	}
	if value.MaxRetries > 5 {
		value.MaxRetries = 5
	}
	return value
}
func waitForRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func rateDelay(e *HTTPError, now time.Time) time.Duration {
	delay := e.RetryAfter
	if remaining := e.RateLimitReset.Sub(now); remaining > delay {
		delay = remaining
	}
	if delay <= 0 {
		delay = time.Second
	}
	return delay
}
func quotaHeaders(header http.Header, now time.Time) (time.Duration, time.Time, int, int) {
	var delay time.Duration
	var reset time.Time
	if raw := header.Get("Retry-After"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 && n <= 86400 {
			delay = time.Duration(n) * time.Second
		} else if at, err := http.ParseTime(raw); err == nil && at.After(now) {
			delay = at.Sub(now)
		}
	}
	if n, err := strconv.ParseInt(header.Get("Ratelimit-Reset"), 10, 64); err == nil && n > 0 {
		reset = time.Unix(n, 0).UTC()
	}
	readCount := func(name string) int {
		n, err := strconv.Atoi(header.Get(name))
		if err != nil || n < 0 {
			return -1
		}
		return n
	}
	return delay, reset, readCount("Ratelimit-Remaining"), readCount("Ratelimit-Limit")
}
func (t transport) httpError(resp *http.Response, labels errorLabels) *HTTPError {
	detail, readErr := readSmallBody(resp.Body)
	if readErr != nil {
		detail = "could not read bounded error response"
	}
	// Both the request's token and the current live token are private: rotation may
	// have happened while this request was in flight.
	secrets := []auth.Secret{auth.NewSecret(t.token()), auth.NewSecret(accessTokenForValidation(t.token()))}
	if resp.Request != nil {
		old := strings.TrimPrefix(resp.Request.Header.Get("Authorization"), "Bearer ")
		secrets = append(secrets, auth.NewSecret(old))
	}
	detail = textsafe.Display(auth.NewRedactor(secrets...).Redact(detail))
	delay, reset, remaining, limit := quotaHeaders(resp.Header, t.retry.Now())
	ambiguous := resp.StatusCode >= 500 && resp.Request != nil && resp.Request.Method != http.MethodGet && resp.Request.Method != http.MethodHead
	return &HTTPError{Operation: labels.action, Endpoint: labels.endpoint, StatusCode: resp.StatusCode, Detail: detail, RetryAfter: delay, RateLimitReset: reset, RateLimitRemaining: remaining, RateLimitLimit: limit, Ambiguous: ambiguous}
}
