package helix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixClipsClientCreateClip(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		fmt.Fprint(w, `{"data":[{"id":"clip123","edit_url":"https://clips.twitch.tv/clip123/edit"}]}`)
	}))
	defer server.Close()

	client := NewClipsClient(ClipsClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})
	clip, err := client.CreateClip(context.Background(), "123")
	if err != nil {
		t.Fatalf("CreateClip error = %v", err)
	}
	want := twitch.Clip{ID: "clip123", EditURL: "https://clips.twitch.tv/clip123/edit"}
	if clip != want {
		t.Fatalf("clip = %#v, want %#v", clip, want)
	}
	if gotBody["broadcaster_id"] != "123" {
		t.Fatalf("request body = %#v, want broadcaster_id=123", gotBody)
	}
}

func TestHelixClipsClientCreateClipMissingBroadcasterID(t *testing.T) {
	client := NewClipsClient(ClipsClientConfig{Endpoint: "http://unused.invalid"})
	if _, err := client.CreateClip(context.Background(), "  "); err == nil {
		t.Fatal("CreateClip error = nil, want missing broadcaster ID error")
	}
}

func TestHelixClipsClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: clips:edit"}`)
	}))
	defer server.Close()

	client := NewClipsClient(ClipsClientConfig{Endpoint: server.URL, OAuthToken: "oauth:access-token"})
	_, err := client.CreateClip(context.Background(), "123")
	if err == nil {
		t.Fatal("CreateClip error = nil, want 401 error")
	}
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true", err)
	}
}

func TestHelixClipsClientNotLiveIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Not Found","status":404,"message":"broadcaster not streaming"}`)
	}))
	defer server.Close()

	client := NewClipsClient(ClipsClientConfig{Endpoint: server.URL})
	_, err := client.CreateClip(context.Background(), "123")
	if err == nil {
		t.Fatal("CreateClip error = nil, want 404 error")
	}
	if !twitch.IsClipCreationUnavailable(err) {
		t.Fatalf("IsClipCreationUnavailable(%v) = false, want true", err)
	}
	if twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = true, want false for a 404", err)
	}
}

func TestHelixClipsClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewClipsClient(ClipsClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.CreateClip(context.Background(), "123")
	if err == nil {
		t.Fatal("CreateClip error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
