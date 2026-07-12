package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestHelixStreamsClientReturnsLiveAndOfflineChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		logins := r.URL.Query()["user_login"]
		if !reflect.DeepEqual(logins, []string{"alpha", "beta"}) {
			t.Fatalf("logins = %#v, want lowercase unique logins", logins)
		}
		fmt.Fprint(w, `{"data":[{"user_login":"alpha","type":"live","started_at":"2026-07-12T18:00:00Z","viewer_count":42}]}`)
	}))
	defer server.Close()

	client := NewHelixStreamsClient(HelixStreamsClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})

	results, err := client.GetStreams(context.Background(), []string{"Alpha", "alpha", "Beta"})
	if err != nil {
		t.Fatalf("GetStreams error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want 2 entries (alpha, beta)", results)
	}
	alpha, beta := results[0], results[1]
	if alpha.UserLogin != "alpha" || !alpha.Live || alpha.ViewerCount != 42 {
		t.Fatalf("alpha = %#v, want live with viewer_count 42", alpha)
	}
	wantStarted := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	if !alpha.StartedAt.Equal(wantStarted) {
		t.Fatalf("alpha.StartedAt = %v, want %v", alpha.StartedAt, wantStarted)
	}
	if beta.UserLogin != "beta" || beta.Live || !beta.StartedAt.IsZero() {
		t.Fatalf("beta = %#v, want offline with zero StartedAt", beta)
	}
}

func TestHelixStreamsClientEmptyLoginsSkipNetwork(t *testing.T) {
	client := NewHelixStreamsClient(HelixStreamsClientConfig{Endpoint: "http://unused.invalid"})
	results, err := client.GetStreams(context.Background(), nil)
	if err != nil || results != nil {
		t.Fatalf("GetStreams(nil) = (%#v, %v), want (nil, nil)", results, err)
	}
}

func TestHelixStreamsClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewHelixStreamsClient(HelixStreamsClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.GetStreams(context.Background(), []string{"alpha"})
	if err == nil {
		t.Fatal("GetStreams error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
