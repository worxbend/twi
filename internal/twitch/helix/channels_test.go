package helix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixChannelsClientGetChannelInformation(t *testing.T) {
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
		if got := r.URL.Query().Get("broadcaster_id"); got != "123" {
			t.Fatalf("broadcaster_id = %q, want 123", got)
		}
		fmt.Fprint(w, `{"data":[{"broadcaster_id":"123","broadcaster_login":"alpha","broadcaster_name":"Alpha","game_id":"509658","game_name":"Just Chatting","title":"Hello","broadcaster_language":"en","tags":["English","Chill"]}]}`)
	}))
	defer server.Close()

	client := NewChannelsClient(ChannelsClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})

	info, err := client.GetChannelInformation(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetChannelInformation error = %v", err)
	}
	want := twitch.ChannelInfo{
		BroadcasterID:    "123",
		BroadcasterLogin: "alpha",
		BroadcasterName:  "Alpha",
		GameID:           "509658",
		GameName:         "Just Chatting",
		Title:            "Hello",
		Language:         "en",
		Tags:             []string{"English", "Chill"},
	}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %#v, want %#v", info, want)
	}
}

func TestHelixChannelsClientGetChannelInformationMissingBroadcasterID(t *testing.T) {
	client := NewChannelsClient(ChannelsClientConfig{Endpoint: "http://unused.invalid"})
	_, err := client.GetChannelInformation(context.Background(), "  ")
	if err == nil {
		t.Fatal("GetChannelInformation error = nil, want missing broadcaster ID error")
	}
}

func TestHelixChannelsClientModifyChannelInformationSendsOnlySetFields(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if got := r.URL.Query().Get("broadcaster_id"); got != "123" {
			t.Fatalf("broadcaster_id = %q, want 123", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewChannelsClient(ChannelsClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})

	title := "New title"
	err := client.ModifyChannelInformation(context.Background(), "123", twitch.ChannelInfoUpdate{Title: &title})
	if err != nil {
		t.Fatalf("ModifyChannelInformation error = %v", err)
	}
	want := map[string]any{"title": "New title"}
	if !reflect.DeepEqual(gotBody, want) {
		t.Fatalf("body = %#v, want %#v", gotBody, want)
	}
}

func TestHelixChannelsClientModifyChannelInformationEmptyUpdateSkipsNetwork(t *testing.T) {
	client := NewChannelsClient(ChannelsClientConfig{Endpoint: "http://unused.invalid"})
	if err := client.ModifyChannelInformation(context.Background(), "123", twitch.ChannelInfoUpdate{}); err != nil {
		t.Fatalf("ModifyChannelInformation error = %v, want nil for empty update", err)
	}
}

func TestHelixChannelsClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: user:edit:broadcast or channel:manage:broadcast or channel_editor"}`)
	}))
	defer server.Close()

	client := NewChannelsClient(ChannelsClientConfig{Endpoint: server.URL, OAuthToken: "oauth:access-token"})
	_, err := client.GetChannelInformation(context.Background(), "123")
	if err == nil {
		t.Fatal("GetChannelInformation error = nil, want 401 error")
	}
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true for a 401 response", err)
	}

	modifyErr := client.ModifyChannelInformation(context.Background(), "123", twitch.ChannelInfoUpdate{Title: strPtr("x")})
	if !twitch.IsMissingScope(modifyErr) {
		t.Fatalf("IsMissingScope(%v) = false, want true for a 401 response", modifyErr)
	}
}

func TestIsMissingScopeFalseForOtherErrors(t *testing.T) {
	if twitch.IsMissingScope(nil) {
		t.Fatal("IsMissingScope(nil) = true, want false")
	}
	if twitch.IsMissingScope(fmt.Errorf("some other error")) {
		t.Fatal("IsMissingScope(generic error) = true, want false")
	}
}

func strPtr(s string) *string { return &s }

func TestHelixChannelsClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewChannelsClient(ChannelsClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.GetChannelInformation(context.Background(), "123")
	if err == nil {
		t.Fatal("GetChannelInformation error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
