package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixUsersClientSuccessBatchesIDsAndLogins(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		ids := r.URL.Query()["id"]
		logins := r.URL.Query()["login"]
		if !reflect.DeepEqual(ids, []string{"42", "99"}) {
			t.Fatalf("ids = %#v, want 42 and 99 once", ids)
		}
		if !reflect.DeepEqual(logins, []string{"viewer", "mod"}) {
			t.Fatalf("logins = %#v, want lowercase unique logins", logins)
		}
		fmt.Fprint(w, `{"data":[{"id":"42","login":"viewer","display_name":"Viewer","profile_image_url":"https://static-cdn.example/viewer.png"},{"id":"99","login":"mod","display_name":"Mod","profile_image_url":"https://static-cdn.example/mod.png"}]}`)
	}))
	defer server.Close()

	client := NewUsersClient(UsersClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})

	users, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{
		UserIDs:    []string{"42", "42", "99"},
		UserLogins: []string{"Viewer", "viewer", "Mod"},
	})
	if err != nil {
		t.Fatalf("GetUsers error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", calls.Load())
	}
	if len(users) != 2 {
		t.Fatalf("users length = %d, want 2: %#v", len(users), users)
	}
	if users[0].UserID != "42" || users[0].Login != "viewer" || users[0].ProfileImageURL == "" {
		t.Fatalf("first user = %#v, want viewer avatar metadata", users[0])
	}
}

func TestHelixUsersClientMissingUsersReturnPartialData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logins := r.URL.Query()["login"]
		if !slices.Contains(logins, "missing") {
			t.Fatalf("logins = %#v, want missing lookup included", logins)
		}
		fmt.Fprint(w, `{"data":[{"id":"42","login":"viewer","display_name":"Viewer","profile_image_url":"https://static-cdn.example/viewer.png"}]}`)
	}))
	defer server.Close()

	client := NewUsersClient(UsersClientConfig{Endpoint: server.URL})
	users, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{
		UserLogins: []string{"viewer", "missing"},
	})
	if err != nil {
		t.Fatalf("GetUsers error = %v", err)
	}
	if len(users) != 1 || users[0].Login != "viewer" {
		t.Fatalf("users = %#v, want only returned Twitch user", users)
	}
}

func TestHelixUsersClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewUsersClient(UsersClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{UserIDs: []string{"42"}})
	if err == nil {
		t.Fatal("GetUsers error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %q, want HTTP 500 detail", err.Error())
	}
}

func TestHelixUsersClientRateLimitLikeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Ratelimit-Reset", "1780000000")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"status":429,"message":"rate limit exceeded"}`)
	}))
	defer server.Close()

	client := NewUsersClient(UsersClientConfig{Endpoint: server.URL})
	_, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{UserLogins: []string{"viewer"}})
	if err == nil {
		t.Fatal("GetUsers error = nil, want rate-limit-like error")
	}
	for _, want := range []string{"HTTP 429", "rate limit"} {
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestHelixUsersClientHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	client := NewUsersClient(UsersClientConfig{Endpoint: server.URL})
	_, err := client.GetUsers(ctx, twitch.UserLookupRequest{UserIDs: []string{"42"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetUsers error = %v, want context.Canceled", err)
	}
}

func TestHelixUsersClientSkipsEmptyLookupWithoutHTTP(t *testing.T) {
	client := NewUsersClient(UsersClientConfig{
		Endpoint: "://bad-url",
	})
	users, err := client.GetUsers(context.Background(), twitch.UserLookupRequest{})
	if err != nil {
		t.Fatalf("GetUsers error = %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %#v, want none", users)
	}
}
