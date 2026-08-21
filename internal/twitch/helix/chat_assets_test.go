package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestHelixChatAssetsClientGlobalAndChannelMetadata(t *testing.T) {
	var globalEmotes atomic.Int32
	var channelEmotes atomic.Int32
	var globalBadges atomic.Int32
	var channelBadges atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/emotes/global", func(w http.ResponseWriter, r *http.Request) {
		globalEmotes.Add(1)
		assertChatAssetHeaders(t, r)
		fmt.Fprint(w, `{"data":[{"id":"25","name":"Kappa","images":{"url_1x":"https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/1.0","url_2x":"https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0","url_4x":"https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/3.0"},"format":["static"],"scale":["1.0","2.0","3.0"],"theme_mode":["light","dark"]}],"template":"https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"}`)
	})
	mux.HandleFunc("/emotes/channel", func(w http.ResponseWriter, r *http.Request) {
		channelEmotes.Add(1)
		assertChatAssetHeaders(t, r)
		if got := r.URL.Query().Get("broadcaster_id"); got != "141981764" {
			t.Fatalf("broadcaster_id = %q, want 141981764", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"304456832","name":"twitchdevPitchfork","images":{"url_2x":"https://static-cdn.jtvnw.net/emoticons/v2/304456832/static/light/2.0"},"format":["static"],"scale":["2.0"],"theme_mode":["light"]}],"template":"https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"}`)
	})
	mux.HandleFunc("/badges/global", func(w http.ResponseWriter, r *http.Request) {
		globalBadges.Add(1)
		assertChatAssetHeaders(t, r)
		fmt.Fprint(w, `{"data":[{"set_id":"vip","versions":[{"id":"1","image_url_1x":"https://static-cdn.jtvnw.net/badges/v1/vip/1","image_url_2x":"https://static-cdn.jtvnw.net/badges/v1/vip/2","image_url_4x":"https://static-cdn.jtvnw.net/badges/v1/vip/3","title":"VIP","description":"VIP"}]}]}`)
	})
	mux.HandleFunc("/badges/channel", func(w http.ResponseWriter, r *http.Request) {
		channelBadges.Add(1)
		assertChatAssetHeaders(t, r)
		if got := r.URL.Query().Get("broadcaster_id"); got != "141981764" {
			t.Fatalf("broadcaster_id = %q, want 141981764", got)
		}
		fmt.Fprint(w, `{"data":[{"set_id":"subscriber","versions":[{"id":"12","image_url_2x":"https://static-cdn.jtvnw.net/badges/v1/subscriber-12/2","title":"Subscriber"}]}]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewChatAssetsClient(ChatAssetsClientConfig{
		GlobalEmotesEndpoint:  server.URL + "/emotes/global",
		ChannelEmotesEndpoint: server.URL + "/emotes/channel",
		GlobalBadgesEndpoint:  server.URL + "/badges/global",
		ChannelBadgesEndpoint: server.URL + "/badges/channel",
		ClientID:              "client-id",
		OAuthToken:            "oauth:access-token",
	})

	global, err := client.GetGlobalEmotes(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalEmotes error = %v", err)
	}
	if len(global) != 1 || global[0].ID != "25" || global[0].ImageURL() != "https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0" {
		t.Fatalf("global emotes = %#v, want Kappa templated URL", global)
	}
	channel, err := client.GetChannelEmotes(context.Background(), "141981764")
	if err != nil {
		t.Fatalf("GetChannelEmotes error = %v", err)
	}
	if len(channel) != 1 || channel[0].ID != "304456832" {
		t.Fatalf("channel emotes = %#v, want twitchdevPitchfork", channel)
	}
	badges, err := client.GetGlobalBadges(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalBadges error = %v", err)
	}
	if len(badges) != 1 || badges[0].SetID != "vip" || badges[0].ImageURL() != "https://static-cdn.jtvnw.net/badges/v1/vip/2" {
		t.Fatalf("global badges = %#v, want vip medium URL", badges)
	}
	channelBadgesResult, err := client.GetChannelBadges(context.Background(), "141981764")
	if err != nil {
		t.Fatalf("GetChannelBadges error = %v", err)
	}
	if len(channelBadgesResult) != 1 || channelBadgesResult[0].SetID != "subscriber" || channelBadgesResult[0].ID != "12" {
		t.Fatalf("channel badges = %#v, want subscriber/12", channelBadgesResult)
	}
	for name, got := range map[string]int32{
		"global emotes":  globalEmotes.Load(),
		"channel emotes": channelEmotes.Load(),
		"global badges":  globalBadges.Load(),
		"channel badges": channelBadges.Load(),
	} {
		if got != 1 {
			t.Fatalf("%s calls = %d, want 1", name, got)
		}
	}
}

func TestHelixChatAssetsClientSkipsEmptyChannelLookup(t *testing.T) {
	client := NewChatAssetsClient(ChatAssetsClientConfig{
		ChannelEmotesEndpoint: "://bad-url",
		ChannelBadgesEndpoint: "://bad-url",
	})

	emotes, err := client.GetChannelEmotes(context.Background(), "")
	if err != nil {
		t.Fatalf("GetChannelEmotes error = %v", err)
	}
	badges, err := client.GetChannelBadges(context.Background(), " ")
	if err != nil {
		t.Fatalf("GetChannelBadges error = %v", err)
	}
	if len(emotes) != 0 || len(badges) != 0 {
		t.Fatalf("empty channel lookup returned emotes=%#v badges=%#v, want none", emotes, badges)
	}
}

func TestHelixChatAssetsClientMalformedMetadata(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bad-json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[`)
	})
	mux.HandleFunc("/missing-fields", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"","name":"missing id","images":{"url_2x":"https://static-cdn.example/missing.png"}},{"id":"ok","name":"fallback","images":{}}],"template":""}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewChatAssetsClient(ChatAssetsClientConfig{
		GlobalEmotesEndpoint: server.URL + "/bad-json",
		GlobalBadgesEndpoint: server.URL + "/missing-fields",
	})
	if _, err := client.GetGlobalEmotes(context.Background()); err == nil {
		t.Fatal("GetGlobalEmotes error = nil, want malformed JSON error")
	}
	badges, err := client.GetGlobalBadges(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalBadges error = %v", err)
	}
	if len(badges) != 0 {
		t.Fatalf("badges = %#v, want malformed badge metadata skipped", badges)
	}
}

func TestHelixChatAssetsClientAPIErrorsAreCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "bad token oauth:access-token Bearer bearer-secret")
	}))
	defer server.Close()

	client := NewChatAssetsClient(ChatAssetsClientConfig{
		GlobalEmotesEndpoint: server.URL,
		OAuthToken:           "oauth:access-token",
	})
	_, err := client.GetGlobalEmotes(context.Background())
	if err == nil {
		t.Fatal("GetGlobalEmotes error = nil, want API error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret")
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %q, want HTTP 401 detail", err.Error())
	}
}

func TestHelixChatAssetsClientHonorsContextCancellation(t *testing.T) {
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

	client := NewChatAssetsClient(ChatAssetsClientConfig{GlobalEmotesEndpoint: server.URL})
	_, err := client.GetGlobalEmotes(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetGlobalEmotes error = %v, want context.Canceled", err)
	}
}

func TestEmoteMetadataImageURLPreference(t *testing.T) {
	metadata := twitch.EmoteMetadata{
		ID:          "25",
		TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}",
		Formats:     []string{"animated", "static"},
		Scales:      []string{"1.0", "2.0"},
		ThemeModes:  []string{"dark", "light"},
	}
	if got, want := metadata.ImageURL(), "https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0"; got != want {
		t.Fatalf("ImageURL = %q, want %q", got, want)
	}
	fallback := twitch.EmoteMetadata{ImageURL1X: "1x", ImageURL2X: "2x", ImageURL4X: "4x"}
	if got, want := fallback.ImageURL(), "2x"; got != want {
		t.Fatalf("fallback ImageURL = %q, want %q", got, want)
	}
}

func assertChatAssetHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", r.Method)
	}
	if got := r.Header.Get("Client-Id"); got != "client-id" {
		t.Fatalf("Client-Id = %q, want client-id", got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want Bearer access-token", got)
	}
}

func TestHelixChatAssetsClientDeduplicatesMetadataOptions(t *testing.T) {
	metadata := helixEmotesResponse{
		Template: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}",
		Data: []helixEmote{{
			ID:        "25",
			Images:    helixEmoteImages{URL2X: "https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0"},
			Formats:   []string{"static", "static"},
			Scales:    []string{"2.0", "2.0"},
			ThemeMode: []string{"light", "light"},
		}},
	}.emotes()

	if len(metadata) != 1 {
		t.Fatalf("metadata length = %d, want 1", len(metadata))
	}
	if !reflect.DeepEqual(metadata[0].Formats, []string{"static"}) {
		t.Fatalf("formats = %#v, want deduplicated static", metadata[0].Formats)
	}
}
