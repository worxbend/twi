package helix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixMarkersClientCreateStreamMarker(t *testing.T) {
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
		fmt.Fprint(w, `{"data":[{"id":"123","created_at":"2026-07-14T20:00:00Z","description":"hype moment","position_seconds":244,"URL":"https://twitch.tv/videos/1?t=4m4s"}]}`)
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{
		Endpoint:   server.URL,
		ClientID:   "client-id",
		OAuthToken: "oauth:access-token",
	})
	marker, err := client.CreateStreamMarker(context.Background(), "123", "hype moment")
	if err != nil {
		t.Fatalf("CreateStreamMarker error = %v", err)
	}
	want := twitch.StreamMarker{
		ID:              "123",
		CreatedAt:       time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC),
		Description:     "hype moment",
		PositionSeconds: 244,
		URL:             "https://twitch.tv/videos/1?t=4m4s",
	}
	if marker != want {
		t.Fatalf("marker = %#v, want %#v", marker, want)
	}
	if gotBody["user_id"] != "123" || gotBody["description"] != "hype moment" {
		t.Fatalf("request body = %#v, want user_id=123 description=hype moment", gotBody)
	}
}

func TestHelixMarkersClientCreateStreamMarkerMissingUserID(t *testing.T) {
	client := NewMarkersClient(MarkersClientConfig{Endpoint: "http://unused.invalid"})
	if _, err := client.CreateStreamMarker(context.Background(), "  ", ""); err == nil {
		t.Fatal("CreateStreamMarker error = nil, want missing broadcaster ID error")
	}
}

func TestHelixMarkersClientGetStreamMarkersReturnsMostRecentVideo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user_id"); got != "123" {
			t.Fatalf("user_id = %q, want 123", got)
		}
		if got := r.URL.Query().Get("first"); got != "20" {
			t.Fatalf("first = %q, want 20", got)
		}
		fmt.Fprint(w, `{"data":[{"user_id":"123","user_name":"streamer","videos":[{"video_id":"v1","markers":[{"id":"m1","created_at":"2026-07-14T20:00:00Z","description":"first","position_seconds":10,"URL":"https://twitch.tv/videos/v1?t=10s"},{"id":"m2","created_at":"2026-07-14T20:05:00Z","description":"","position_seconds":300,"URL":"https://twitch.tv/videos/v1?t=5m"}]}]}]}`)
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{Endpoint: server.URL})
	markers, err := client.GetStreamMarkers(context.Background(), "123", 0)
	if err != nil {
		t.Fatalf("GetStreamMarkers error = %v", err)
	}
	if len(markers) != 2 || markers[0].ID != "m1" || markers[1].ID != "m2" {
		t.Fatalf("markers = %#v, want [m1, m2]", markers)
	}
}

func TestHelixMarkersClientGetStreamMarkersNoVideos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"user_id":"123","user_name":"streamer","videos":[]}]}`)
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{Endpoint: server.URL})
	markers, err := client.GetStreamMarkers(context.Background(), "123", 20)
	if err != nil {
		t.Fatalf("GetStreamMarkers error = %v", err)
	}
	if len(markers) != 0 {
		t.Fatalf("markers = %#v, want empty", markers)
	}
}

func TestHelixMarkersClientMissingScopeIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: channel:manage:broadcast"}`)
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{Endpoint: server.URL, OAuthToken: "oauth:access-token"})
	_, err := client.CreateStreamMarker(context.Background(), "123", "")
	if err == nil {
		t.Fatal("CreateStreamMarker error = nil, want 401 error")
	}
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true", err)
	}

	_, err = client.GetStreamMarkers(context.Background(), "123", 20)
	if !twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = false, want true for GetStreamMarkers 401", err)
	}
}

func TestHelixMarkersClientNoVideoFoundIsDetectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Not Found","status":404,"message":"Unable to find user's most recent Video/VOD ID. Please ensure you are passing the correct user ID!"}`)
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{Endpoint: server.URL})
	_, err := client.GetStreamMarkers(context.Background(), "123", 20)
	if err == nil {
		t.Fatal("GetStreamMarkers error = nil, want 404 error")
	}
	if !twitch.IsNoVideoFound(err) {
		t.Fatalf("IsNoVideoFound(%v) = false, want true", err)
	}
	if twitch.IsMissingScope(err) {
		t.Fatalf("IsMissingScope(%v) = true, want false for a 404", err)
	}
}

func TestHelixMarkersClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewMarkersClient(MarkersClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.CreateStreamMarker(context.Background(), "123", "")
	if err == nil {
		t.Fatal("CreateStreamMarker error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
