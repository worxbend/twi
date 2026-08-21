package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixGamesClientSearchCategories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("query"); got != "fort" {
			t.Fatalf("query = %q, want fort", got)
		}
		if got := r.URL.Query().Get("first"); got != "20" {
			t.Fatalf("first = %q, want 20", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"33214","name":"Fortnite"},{"id":"509670","name":"Fortnite Creative"}]}`)
	}))
	defer server.Close()

	client := NewGamesClient(GamesClientConfig{Endpoint: server.URL})
	games, err := client.SearchCategories(context.Background(), "fort", 0)
	if err != nil {
		t.Fatalf("SearchCategories error = %v", err)
	}
	want := []twitch.Game{{ID: "33214", Name: "Fortnite"}, {ID: "509670", Name: "Fortnite Creative"}}
	if !reflect.DeepEqual(games, want) {
		t.Fatalf("games = %#v, want %#v", games, want)
	}
}

func TestHelixGamesClientSearchCategoriesClampsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("first"); got != "100" {
			t.Fatalf("first = %q, want 100", got)
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	client := NewGamesClient(GamesClientConfig{Endpoint: server.URL})
	if _, err := client.SearchCategories(context.Background(), "x", 500); err != nil {
		t.Fatalf("SearchCategories error = %v", err)
	}
}

func TestHelixGamesClientSearchCategoriesNoMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	client := NewGamesClient(GamesClientConfig{Endpoint: server.URL})
	games, err := client.SearchCategories(context.Background(), "zzzznonexistent", 20)
	if err != nil {
		t.Fatalf("SearchCategories error = %v", err)
	}
	if len(games) != 0 {
		t.Fatalf("games = %#v, want empty", games)
	}
}

func TestHelixGamesClientEmptyQuerySkipsNetwork(t *testing.T) {
	client := NewGamesClient(GamesClientConfig{Endpoint: "http://unused.invalid"})
	games, err := client.SearchCategories(context.Background(), "   ", 20)
	if err != nil || games != nil {
		t.Fatalf("SearchCategories(empty) = (%#v, %v), want (nil, nil)", games, err)
	}
}

func TestHelixGamesClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "server saw oauth:access-token and Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewGamesClient(GamesClientConfig{
		Endpoint:   server.URL,
		OAuthToken: "oauth:access-token",
	})
	_, err := client.SearchCategories(context.Background(), "Some Game", 20)
	if err == nil {
		t.Fatal("SearchCategories error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
}
