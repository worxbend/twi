package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	irc "github.com/gempir/go-twitch-irc/v4"
)

// TestCredentialSafeIRCErrorAttachesSentinel connects the two halves of error
// classification: the transport is the only place that knows Twitch rejected
// the credentials, so it must mark the error, or the app is back to guessing
// from message text.
func TestCredentialSafeIRCErrorAttachesSentinel(t *testing.T) {
	err := credentialSafeIRCError(irc.ErrLoginAuthenticationFailed)
	if !IsAuthError(err) {
		t.Fatal("credentialSafeIRCError did not mark a login failure as an auth error")
	}
	if !errors.Is(err, irc.ErrLoginAuthenticationFailed) {
		t.Fatal("credentialSafeIRCError discarded the underlying cause")
	}
	if !strings.Contains(err.Error(), "chat:read") {
		t.Fatalf("error = %q, want actionable scope guidance", err.Error())
	}
}

func TestCredentialSafeIRCErrorLeavesOtherFailuresUnclassified(t *testing.T) {
	err := credentialSafeIRCError(errors.New("x509: certificate signed by unknown authority"))
	if IsAuthError(err) {
		t.Fatal("a TLS trust failure was classified as an auth error")
	}
	if !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error = %q, want the real cause preserved in the message", err.Error())
	}
}

func TestCredentialSafeIRCErrorRedactsTokens(t *testing.T) {
	err := credentialSafeIRCError(errors.New("dial failed with oauth:secret-token"))
	if strings.Contains(err.Error(), "oauth:secret-token") {
		t.Fatalf("error leaked the token: %q", err.Error())
	}
}

func TestIsAuthErrorIgnoresNil(t *testing.T) {
	if IsAuthError(nil) {
		t.Fatal("IsAuthError(nil) = true, want false")
	}
}

// TestHelixClientReadsTokenPerRequest is the regression for the other half of
// the stale-credential problem. Helix clients copied the OAuth token into a
// field at construction, so after the IRC transport refreshed mid-session,
// chat kept working while the LIVE indicator, follower and subscriber counts,
// /clip, stream markers and Stream Info all began returning 401 -- looking
// like several unrelated features breaking at once.
func TestHelixClientReadsTokenPerRequest(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	token := "first-token"
	client := NewHelixStreamsClient(HelixStreamsClientConfig{
		Endpoint:         server.URL,
		HTTPClient:       server.Client(),
		ClientID:         "client-id",
		OAuthTokenSource: func() string { return token },
	})

	if _, err := client.GetStreams(context.Background(), []string{"example"}); err != nil {
		t.Fatalf("first request: %v", err)
	}
	// A refresh lands between requests.
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

// TestHelixClientFallsBackToStaticToken keeps the plain configuration working
// for callers that have no refresh story.
func TestHelixClientFallsBackToStaticToken(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	client := NewHelixStreamsClient(HelixStreamsClientConfig{
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
