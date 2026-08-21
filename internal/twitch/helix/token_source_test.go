package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientReadsTokenPerRequest is the regression for the other half of
// the stale-credential problem. Helix clients copied the OAuth token into a
// field at construction, so after the IRC transport refreshed mid-session,
// chat kept working while the LIVE indicator, follower and subscriber counts,
// /clip, stream markers and Stream Info all began returning 401 -- looking
// like several unrelated features breaking at once.
func TestClientReadsTokenPerRequest(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	token := "first-token"
	client := NewStreamsClient(StreamsClientConfig{
		Endpoint:         server.URL,
		HTTPClient:       server.Client(),
		ClientID:         "client-id",
		OAuthTokenSource: func() string { return token },
	})

	if _, err := client.GetStreams(context.Background(), []string{"example"}); err != nil {
		t.Fatalf("first request: %v", err)
	}

	token = "second-token"
	if _, err := client.GetStreams(context.Background(), []string{"example"}); err != nil {
		t.Fatalf("second request: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(seen))
	}
	if seen[0] != "Bearer first-token" {
		t.Errorf("first request auth = %q, want the original token", seen[0])
	}
	if seen[1] != "Bearer second-token" {
		t.Errorf("second request auth = %q, want the refreshed token; the client froze it at construction", seen[1])
	}
}

// TestClientFallsBackToStaticToken keeps the plain configuration working
// for callers that have no refresh story.
func TestClientFallsBackToStaticToken(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	client := NewStreamsClient(StreamsClientConfig{
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
		ClientID:   "client-id",
		OAuthToken: "static-token",
	})
	if _, err := client.GetStreams(context.Background(), []string{"example"}); err != nil {
		t.Fatalf("request: %v", err)
	}
	if seen != "Bearer static-token" {
		t.Fatalf("auth = %q, want the statically configured token", seen)
	}
}
