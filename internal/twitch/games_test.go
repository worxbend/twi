package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelixGamesClientGetGameByName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "Just Chatting" {
			t.Fatalf("name = %q, want Just Chatting", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"509658","name":"Just Chatting"}]}`)
	}))
	defer server.Close()

	client := NewHelixGamesClient(HelixGamesClientConfig{Endpoint: server.URL})
	game, ok, err := client.GetGameByName(context.Background(), "Just Chatting")
	if err != nil {
		t.Fatalf("GetGameByName error = %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if game.ID != "509658" || game.Name != "Just Chatting" {
		t.Fatalf("game = %#v, want id 509658 name Just Chatting", game)
	}
}

func TestHelixGamesClientGetGameByNameNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	client := NewHelixGamesClient(HelixGamesClientConfig{Endpoint: server.URL})
	_, ok, err := client.GetGameByName(context.Background(), "Nonexistent Category")
	if err != nil {
		t.Fatalf("GetGameByName error = %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false for unmatched category")
	}
}

func TestHelixGamesClientEmptyNameSkipsNetwork(t *testing.T) {
	client := NewHelixGamesClient(HelixGamesClientConfig{Endpoint: "http://unused.invalid"})
	_, ok, err := client.GetGameByName(context.Background(), "   ")
	if err != nil || ok {
		t.Fatalf("GetGameByName(empty) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

func TestHelixGamesClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewHelixGamesClient(HelixGamesClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, _, err := client.GetGameByName(context.Background(), "Some Game")
	if err == nil {
		t.Fatal("GetGameByName error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
