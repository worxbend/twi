package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixFollowedChannelsClientGetFollowedChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user_id"); got != "123" {
			t.Fatalf("user_id = %q, want 123", got)
		}
		if got := r.URL.Query().Get("first"); got != "100" {
			t.Fatalf("first = %q, want 100", got)
		}
		fmt.Fprint(w, `{"total":1,"data":[{"broadcaster_id":"55","broadcaster_login":"alpha","broadcaster_name":"Alpha","followed_at":"2026-07-14T22:22:08Z"}],"pagination":{}}`)
	}))
	defer server.Close()

	client := NewFollowedChannelsClient(FollowedChannelsClientConfig{Endpoint: server.URL})
	followed, err := client.GetFollowedChannels(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetFollowedChannels error = %v", err)
	}
	want := twitch.FollowedChannel{
		BroadcasterID:    "55",
		BroadcasterLogin: "alpha",
		BroadcasterName:  "Alpha",
		FollowedAt:       time.Date(2026, 7, 14, 22, 22, 8, 0, time.UTC),
	}
	if len(followed) != 1 || followed[0] != want {
		t.Fatalf("followed = %#v, want [%#v]", followed, want)
	}
}

// Twitch pages the follow list 100 at a time; the picker needs the whole
// list, so the cursor is followed until it runs out.
func TestHelixFollowedChannelsClientFollowsCursorAndDeduplicates(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			if got := r.URL.Query().Get("after"); got != "" {
				t.Fatalf("first page after = %q, want empty", got)
			}
			fmt.Fprint(w, `{"data":[{"broadcaster_login":"alpha"},{"broadcaster_login":"beta"}],"pagination":{"cursor":"page2"}}`)
		default:
			if got := r.URL.Query().Get("after"); got != "page2" {
				t.Fatalf("second page after = %q, want page2", got)
			}
			// "beta" repeats across pages when the follow list shifts
			// mid-walk; the picker must not show it twice.
			fmt.Fprint(w, `{"data":[{"broadcaster_login":"beta"},{"broadcaster_login":"gamma"}],"pagination":{}}`)
		}
	}))
	defer server.Close()

	client := NewFollowedChannelsClient(FollowedChannelsClientConfig{Endpoint: server.URL})
	followed, err := client.GetFollowedChannels(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetFollowedChannels error = %v", err)
	}
	logins := make([]string, 0, len(followed))
	for _, entry := range followed {
		logins = append(logins, entry.BroadcasterLogin)
	}
	if len(logins) != 3 || logins[0] != "alpha" || logins[1] != "beta" || logins[2] != "gamma" {
		t.Fatalf("logins = %#v, want [alpha beta gamma]", logins)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestHelixFollowedChannelsClientMissingUserID(t *testing.T) {
	client := NewFollowedChannelsClient(FollowedChannelsClientConfig{Endpoint: "http://unused.invalid"})
	if _, err := client.GetFollowedChannels(context.Background(), "  "); err == nil {
		t.Fatal("GetFollowedChannels error = nil, want missing user ID error")
	}
}

func TestHelixFollowedChannelsClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: user:read:follows"}`)
	}))
	defer server.Close()

	client := NewFollowedChannelsClient(FollowedChannelsClientConfig{Endpoint: server.URL})
	_, err := client.GetFollowedChannels(context.Background(), "123")
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true", err)
	}
}

func TestHelixFollowedChannelsClientRedactsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	}))
	defer server.Close()

	client := NewFollowedChannelsClient(FollowedChannelsClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:supersecrettoken",
	})
	_, err := client.GetFollowedChannels(context.Background(), "123")
	if err == nil {
		t.Fatal("GetFollowedChannels error = nil, want HTTP 500 error")
	}
	if got := err.Error(); strings.Contains(got, "supersecrettoken") {
		t.Fatalf("error leaked the OAuth token: %s", got)
	}
}
