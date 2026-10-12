package helix

import (
	"context"
	"errors"
	"fmt"
	"github.com/worxbend/twi/internal/twitch"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPErrorPreservesQuotaAndRedactsOldRotatedToken(t *testing.T) {
	now := time.Unix(2000000000, 0)
	var token atomic.Value
	token.Store("oauth:OLD_FIXTURE_SECRET")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token.Store("oauth:NEW_FIXTURE_SECRET")
		w.Header().Set("Retry-After", "3")
		w.Header().Set("Ratelimit-Reset", fmt.Sprint(now.Add(5*time.Second).Unix()))
		w.Header().Set("Ratelimit-Remaining", "0")
		w.Header().Set("Ratelimit-Limit", "800")
		w.WriteHeader(403)
		fmt.Fprint(w, `{"message":"OLD_FIXTURE_SECRET NEW_FIXTURE_SECRET"}`)
	}))
	defer server.Close()
	client := NewUsersClient(ClientConfig{Endpoint: server.URL, OAuthTokenSource: func() string { return token.Load().(string) }, RetryPolicy: &RetryPolicy{Now: func() time.Time { return now }}})
	_, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{UserLogins: []string{"alpha"}})
	var failure *HTTPError
	if !errors.As(err, &failure) || failure.StatusCode != 403 || failure.RetryAfter != 3*time.Second || failure.RateLimitRemaining != 0 || failure.RateLimitLimit != 800 || !failure.RateLimitReset.Equal(now.Add(5*time.Second)) {
		t.Fatalf("typed HTTP error lost facts: %v", err)
	}
	if strings.Contains(err.Error(), "FIXTURE_SECRET") {
		t.Fatalf("error exposes credentials: %v", err)
	}
}
func TestExplicit429WaitsForHeadersAndRetriesAcceptedSend(t *testing.T) {
	now := time.Unix(2000000000, 0)
	calls := 0
	waits := []time.Duration{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("Ratelimit-Reset", fmt.Sprint(now.Add(4*time.Second).Unix()))
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"data":[{"is_sent":true,"message_id":"accepted"}]}`)
	}))
	defer server.Close()
	client := NewSendChatClient(ClientConfig{Endpoint: server.URL, RetryPolicy: &RetryPolicy{Wait: true, MaxRetries: 2, Now: func() time.Time { return now }, WaitFunc: func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		now = now.Add(d)
		return nil
	}}})
	result, status, err := client.Send(context.Background(), SendChatRequest{Message: "hello"})
	if err != nil || status != 200 || !result.IsSent || calls != 2 || len(waits) != 1 || waits[0] != 4*time.Second {
		t.Fatal(result, status, err, calls, waits)
	}
}
func Test429FailPolicyDoesNotWaitOrRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(429) }))
	defer server.Close()
	client := NewSendChatClient(ClientConfig{Endpoint: server.URL, RetryPolicy: &RetryPolicy{Wait: false, MaxRetries: 2}})
	_, status, err := client.Send(context.Background(), SendChatRequest{})
	var failure *HTTPError
	if !errors.As(err, &failure) || status != 429 || calls != 1 {
		t.Fatal(status, err, calls)
	}
}
func Test429WaitCancellationCannotResend(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client := NewSendChatClient(ClientConfig{Endpoint: server.URL, RetryPolicy: &RetryPolicy{Wait: true, MaxRetries: 2, WaitFunc: func(context.Context, time.Duration) error { cancel(); return ctx.Err() }}})
	_, status, err := client.Send(ctx, SendChatRequest{})
	if !errors.Is(err, context.Canceled) || status != 429 || calls != 1 {
		t.Fatal(status, err, calls)
	}
}
func TestReadRetriesExplicit503ButWriteNeverDoes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"1","login":"alpha"}]}`)
	}))
	defer server.Close()
	client := NewUsersClient(ClientConfig{Endpoint: server.URL, RetryPolicy: &RetryPolicy{Wait: true, MaxRetries: 1, WaitFunc: func(context.Context, time.Duration) error { return nil }}})
	users, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{UserLogins: []string{"alpha"}})
	if err != nil || calls != 2 || len(users) != 1 {
		t.Fatal(users, err, calls)
	}
}
