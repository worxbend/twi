package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixSubscriptionsClientGetBroadcasterSubscriptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("broadcaster_id"); got != "123" {
			t.Fatalf("broadcaster_id = %q, want 123", got)
		}
		if got := r.URL.Query().Get("first"); got != "1" {
			t.Fatalf("first = %q, want 1", got)
		}
		fmt.Fprint(w, `{"data":[],"pagination":{},"total":12,"points":13}`)
	}))
	defer server.Close()

	client := NewSubscriptionsClient(SubscriptionsClientConfig{Endpoint: server.URL})
	page, err := client.GetBroadcasterSubscriptions(context.Background(), "123", 0)
	if err != nil {
		t.Fatalf("GetBroadcasterSubscriptions error = %v", err)
	}
	if page.Total != 12 || page.Points != 13 {
		t.Fatalf("page = %#v, want total=12 points=13", page)
	}
}

func TestHelixSubscriptionsClientMissingBroadcasterID(t *testing.T) {
	client := NewSubscriptionsClient(SubscriptionsClientConfig{Endpoint: "http://unused.invalid"})
	if _, err := client.GetBroadcasterSubscriptions(context.Background(), "  ", 1); err == nil {
		t.Fatal("GetBroadcasterSubscriptions error = nil, want missing broadcaster ID error")
	}
}

func TestHelixSubscriptionsClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: channel:read:subscriptions"}`)
	}))
	defer server.Close()

	client := NewSubscriptionsClient(SubscriptionsClientConfig{Endpoint: server.URL})
	_, err := client.GetBroadcasterSubscriptions(context.Background(), "123", 1)
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true", err)
	}
}

func TestHelixSubscriptionsClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewSubscriptionsClient(SubscriptionsClientConfig{Endpoint: server.URL, OAuthToken: "oauth:access-token"})
	_, err := client.GetBroadcasterSubscriptions(context.Background(), "123", 1)
	if err == nil {
		t.Fatal("GetBroadcasterSubscriptions error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
