package helix

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModeratedChannelProofPaginatesAndRestrictsSelection(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/" || r.URL.Query().Get("user_id") != "actor" || r.URL.Query().Get("first") != "100" || r.Header.Get("Authorization") != "Bearer fake-token" || r.Header.Get("Client-Id") != "client" {
			t.Errorf("wrong role lookup request")
		}
		if r.URL.Query().Get("after") == "" {
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"unselected","broadcaster_login":"other"},{"broadcaster_id":"selected","broadcaster_login":"Alpha","broadcaster_name":"Alpha"}],"pagination":{"cursor":"next"}}`))
		} else {
			if r.URL.Query().Get("after") != "next" {
				t.Errorf("cursor lost")
			}
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"selected","broadcaster_login":"Alpha"},{"broadcaster_id":"second","broadcaster_login":"Beta"}],"pagination":{"cursor":"more-unneeded"}}`))
		}
	}))
	defer server.Close()
	client := NewModeratedChannelsClient(ClientConfig{Endpoint: server.URL, ClientID: "client", OAuthToken: "fake-token"})
	channels, err := client.GetModeratedChannels(context.Background(), "actor", []string{"selected", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(channels) != 2 || channels[0].BroadcasterID != "selected" || channels[0].BroadcasterLogin != "alpha" || channels[1].BroadcasterID != "second" {
		t.Fatalf("calls=%d proof=%+v", calls, channels)
	}
}
func TestModeratorProofCannotTreatPaginationCycleAsComplete(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"other"}],"pagination":{"cursor":"cycle"}}`))
	}))
	defer server.Close()
	_, err := NewModeratedChannelsClient(ClientConfig{Endpoint: server.URL}).GetModeratedChannels(context.Background(), "actor", []string{"selected"})
	if !errors.Is(err, ErrModeratorProofIncomplete) || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestModeratorProofBoundsPaginationAndRedactsScopeFailure(t *testing.T) {
	t.Parallel()
	t.Run("page budget", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"other"}],"pagination":{"cursor":"` + strings.Repeat("a", calls) + `"}}`))
		}))
		defer server.Close()
		_, err := NewModeratedChannelsClient(ClientConfig{Endpoint: server.URL}).GetModeratedChannels(context.Background(), "actor", []string{"selected"})
		if !errors.Is(err, ErrModeratorProofIncomplete) || calls != maxModeratedChannelPages {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("scope failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"missing scope fake-role-secret"}`))
		}))
		defer server.Close()
		_, err := NewModeratedChannelsClient(ClientConfig{Endpoint: server.URL, OAuthToken: "fake-role-secret"}).GetModeratedChannels(context.Background(), "actor", []string{"selected"})
		if err == nil || strings.Contains(err.Error(), "fake-role-secret") {
			t.Fatalf("unsafe error=%v", err)
		}
		var failure *HTTPError
		if !errors.As(err, &failure) || failure.StatusCode != 401 {
			t.Fatalf("typed status lost=%v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewModeratedChannelsClient(ClientConfig{Endpoint: "http://unreachable.invalid"}).GetModeratedChannels(ctx, "actor", []string{"selected"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	})
}
