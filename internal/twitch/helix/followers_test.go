package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixFollowersClientGetChannelFollowers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("broadcaster_id"); got != "123" {
			t.Fatalf("broadcaster_id = %q, want 123", got)
		}
		if got := r.URL.Query().Get("moderator_id"); got != "123" {
			t.Fatalf("moderator_id = %q, want 123 (self)", got)
		}
		if got := r.URL.Query().Get("first"); got != "20" {
			t.Fatalf("first = %q, want 20", got)
		}
		fmt.Fprint(w, `{"total":8,"data":[{"user_id":"11111","user_login":"user1","user_name":"User1","followed_at":"2026-07-14T22:22:08Z"}]}`)
	}))
	defer server.Close()

	client := NewFollowersClient(FollowersClientConfig{Endpoint: server.URL})
	page, err := client.GetChannelFollowers(context.Background(), "123", 0)
	if err != nil {
		t.Fatalf("GetChannelFollowers error = %v", err)
	}
	if page.Total != 8 {
		t.Fatalf("total = %d, want 8", page.Total)
	}
	want := twitch.Follower{UserID: "11111", UserLogin: "user1", UserName: "User1", FollowedAt: time.Date(2026, 7, 14, 22, 22, 8, 0, time.UTC)}
	if len(page.Followers) != 1 || page.Followers[0] != want {
		t.Fatalf("followers = %#v, want [%#v]", page.Followers, want)
	}
}

func TestHelixFollowersClientMissingBroadcasterID(t *testing.T) {
	client := NewFollowersClient(FollowersClientConfig{Endpoint: "http://unused.invalid"})
	if _, err := client.GetChannelFollowers(context.Background(), "  ", 20); err == nil {
		t.Fatal("GetChannelFollowers error = nil, want missing broadcaster ID error")
	}
}

func TestHelixFollowersClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: moderator:read:followers"}`)
	}))
	defer server.Close()

	client := NewFollowersClient(FollowersClientConfig{Endpoint: server.URL})
	_, err := client.GetChannelFollowers(context.Background(), "123", 20)
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true", err)
	}
}

func TestHelixFollowersClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewFollowersClient(FollowersClientConfig{Endpoint: server.URL, OAuthToken: "oauth:access-token"})
	_, err := client.GetChannelFollowers(context.Background(), "123", 20)
	if err == nil {
		t.Fatal("GetChannelFollowers error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
