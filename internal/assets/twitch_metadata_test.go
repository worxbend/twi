package assets

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

func TestTwitchMetadataResolverResolvesGlobalAndChannelMetadata(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	lookup := &fakeTwitchChatMetadataLookup{
		globalEmotes: []twitch.EmoteMetadata{{
			ID:          "25",
			Name:        "Kappa",
			TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}",
			Formats:     []string{"static"},
			Scales:      []string{"1.0", "2.0"},
			ThemeModes:  []string{"light"},
		}},
		channelEmotes: []twitch.EmoteMetadata{{
			ID:          "304456832",
			Name:        "twitchdevPitchfork",
			TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}",
			Formats:     []string{"static"},
			Scales:      []string{"2.0"},
			ThemeModes:  []string{"light"},
		}},
		globalBadges: []twitch.BadgeMetadata{{
			SetID:      "vip",
			ID:         "1",
			Title:      "VIP",
			ImageURL2X: "https://static-cdn.jtvnw.net/badges/v1/vip/2",
		}},
		channelBadges: []twitch.BadgeMetadata{{
			SetID:      "subscriber",
			ID:         "12",
			Title:      "Subscriber",
			ImageURL2X: "https://static-cdn.jtvnw.net/badges/v1/subscriber-12/2",
		}},
	}
	resolver := &TwitchMetadataResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
		TTL:    time.Hour,
	}

	channelEmote, err := resolver.LookupMetadata(context.Background(), MetadataRequest{
		Ref:       twitch.AssetRef{Kind: KindTwitchEmote, ID: "304456832"},
		ChannelID: "141981764",
	})
	if err != nil {
		t.Fatalf("LookupMetadata channel emote error = %v", err)
	}
	if got, want := channelEmote.URL, "https://static-cdn.jtvnw.net/emoticons/v2/304456832/static/light/2.0"; got != want {
		t.Fatalf("channel emote URL = %q, want %q", got, want)
	}
	globalEmote, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}})
	if err != nil {
		t.Fatalf("LookupMetadata global emote error = %v", err)
	}
	if !strings.Contains(globalEmote.URL, "/25/static/light/2.0") {
		t.Fatalf("global emote URL = %q, want Kappa CDN URL", globalEmote.URL)
	}
	channelBadge, err := resolver.LookupMetadata(context.Background(), MetadataRequest{
		Ref:       twitch.AssetRef{Kind: KindBadge, ID: "subscriber/12"},
		ChannelID: "141981764",
	})
	if err != nil {
		t.Fatalf("LookupMetadata channel badge error = %v", err)
	}
	if got, want := channelBadge.URL, "https://static-cdn.jtvnw.net/badges/v1/subscriber-12/2"; got != want {
		t.Fatalf("channel badge URL = %q, want %q", got, want)
	}
	globalBadge, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindBadge, ID: "vip/1"}})
	if err != nil {
		t.Fatalf("LookupMetadata global badge error = %v", err)
	}
	if got, want := globalBadge.URL, "https://static-cdn.jtvnw.net/badges/v1/vip/2"; got != want {
		t.Fatalf("global badge URL = %q, want %q", got, want)
	}

	if lookup.channelEmoteCalls != 1 {
		t.Fatalf("channel emote calls = %d, want 1", lookup.channelEmoteCalls)
	}
	if lookup.globalEmoteCalls != 1 {
		t.Fatalf("global emote calls = %d, want 1", lookup.globalEmoteCalls)
	}
	if lookup.channelBadgeCalls != 1 {
		t.Fatalf("channel badge calls = %d, want 1", lookup.channelBadgeCalls)
	}
	if lookup.globalBadgeCalls != 1 {
		t.Fatalf("global badge calls = %d, want 1", lookup.globalBadgeCalls)
	}

	cachedBadge, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindBadge, ID: "141981764/subscriber/12"})
	if err != nil || !ok {
		t.Fatalf("cached channel badge ok=%v err=%v, want hit nil", ok, err)
	}
	if cachedBadge.SourceURL != "https://static-cdn.jtvnw.net/badges/v1/subscriber-12/2" {
		t.Fatalf("cached badge SourceURL = %q, want channel badge URL", cachedBadge.SourceURL)
	}
	if !cachedBadge.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("cached badge ExpiresAt = %s, want %s", cachedBadge.ExpiresAt, now.Add(time.Hour))
	}
}

func TestTwitchMetadataResolverUsesCacheHitWithoutProviderLookup(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	if err := cache.PutAsset(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: KindTwitchEmote, ID: "25"},
		SourceURL: "https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0",
		MediaType: "image/png",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	lookup := &fakeTwitchChatMetadataLookup{}
	resolver := &TwitchMetadataResolver{Lookup: lookup, Cache: cache}

	metadata, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}})
	if err != nil {
		t.Fatalf("LookupMetadata error = %v", err)
	}
	if metadata.URL == "" || !metadata.ExpiresAt.After(time.Now()) {
		t.Fatalf("metadata = %#v, want cached URL with expiry", metadata)
	}
	if lookup.totalCalls() != 0 {
		t.Fatalf("provider calls = %d, want 0", lookup.totalCalls())
	}
}

func TestTwitchMetadataResolverUsesDirectEmoteURLWithoutProviderLookup(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	lookup := &fakeTwitchChatMetadataLookup{}
	resolver := &TwitchMetadataResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
		TTL:    time.Hour,
	}
	ref := twitch.AssetRef{
		Kind: KindTwitchEmote,
		ID:   "28087",
		URL:  "https://static-cdn.jtvnw.net/emoticons/v2/28087/static/light/2.0",
	}

	metadata, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: ref, ChannelID: "11148817"})
	if err != nil {
		t.Fatalf("LookupMetadata error = %v", err)
	}
	if metadata.Ref != ref {
		t.Fatalf("metadata Ref = %#v, want direct ref %#v", metadata.Ref, ref)
	}
	if got, want := metadata.URL, ref.URL; got != want {
		t.Fatalf("metadata URL = %q, want %q", got, want)
	}
	if got, want := metadata.MediaType, "image/png"; got != want {
		t.Fatalf("metadata MediaType = %q, want %q", got, want)
	}
	if got, want := metadata.WidthCells, 2; got != want {
		t.Fatalf("metadata WidthCells = %d, want %d", got, want)
	}
	if lookup.totalCalls() != 0 {
		t.Fatalf("provider calls = %d, want 0", lookup.totalCalls())
	}
	cached, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindTwitchEmote, ID: "28087"})
	if err != nil || !ok {
		t.Fatalf("cached direct emote ok=%v err=%v, want hit nil", ok, err)
	}
	if got, want := cached.SourceURL, ref.URL; got != want {
		t.Fatalf("cached SourceURL = %q, want %q", got, want)
	}
	if !cached.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("cached ExpiresAt = %s, want %s", cached.ExpiresAt, now.Add(time.Hour))
	}
}

func TestTwitchMetadataResolverUsesDirectEmoteCDNURLWithFragment(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	lookup := &fakeTwitchChatMetadataLookup{}
	resolver := &TwitchMetadataResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
		TTL:    time.Hour,
	}
	rawURL := "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0#e=0"
	cleanURL := "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0"
	ref := twitch.AssetRef{
		Kind: KindTwitchEmote,
		URL:  rawURL,
	}

	metadata, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: ref, ChannelID: "11148817"})
	if err != nil {
		t.Fatalf("LookupMetadata error = %v", err)
	}
	if got, want := metadata.Ref.ID, "emotesv2_299397e0339249f8a1b50f0affb044d8"; got != want {
		t.Fatalf("metadata Ref.ID = %q, want %q", got, want)
	}
	if got, want := metadata.Ref.URL, cleanURL; got != want {
		t.Fatalf("metadata Ref.URL = %q, want cleaned URL %q", got, want)
	}
	if got, want := metadata.URL, cleanURL; got != want {
		t.Fatalf("metadata URL = %q, want cleaned URL %q", got, want)
	}
	if got, want := metadata.MediaType, "image/png"; got != want {
		t.Fatalf("metadata MediaType = %q, want %q", got, want)
	}
	if lookup.totalCalls() != 0 {
		t.Fatalf("provider calls = %d, want 0", lookup.totalCalls())
	}
	cached, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindTwitchEmote, ID: "emotesv2_299397e0339249f8a1b50f0affb044d8"})
	if err != nil || !ok {
		t.Fatalf("cached direct emote ok=%v err=%v, want hit nil", ok, err)
	}
	if got, want := cached.SourceURL, cleanURL; got != want {
		t.Fatalf("cached SourceURL = %q, want cleaned URL %q", got, want)
	}
}

func TestTwitchMetadataResolverRejectsUnsafeEmoteCacheKey(t *testing.T) {
	resolver := &TwitchMetadataResolver{Cache: storage.NewMemoryAssetCache()}

	metadata, err := resolver.LookupMetadata(context.Background(), MetadataRequest{
		Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "https://cdn.example/emote.png?access_token=secret"},
	})
	if err != nil {
		t.Fatalf("LookupMetadata returned error: %v", err)
	}
	if metadata.URL != "" {
		t.Fatalf("metadata.URL = %q, want fallback without source URL", metadata.URL)
	}
}

func TestTwitchMetadataResolverCacheFailures(t *testing.T) {
	errCacheGet := errors.New("cache get failed")
	errCachePut := errors.New("cache put failed")

	t.Run("get failure", func(t *testing.T) {
		resolver := &TwitchMetadataResolver{
			Lookup: &fakeTwitchChatMetadataLookup{},
			Cache:  &failingAssetCache{getErr: errCacheGet},
		}
		_, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}})
		if !errors.Is(err, errCacheGet) {
			t.Fatalf("LookupMetadata error = %v, want %v", err, errCacheGet)
		}
	})

	t.Run("put failure", func(t *testing.T) {
		resolver := &TwitchMetadataResolver{
			Lookup: &fakeTwitchChatMetadataLookup{
				globalEmotes: []twitch.EmoteMetadata{{
					ID:         "25",
					Name:       "Kappa",
					ImageURL2X: "https://static-cdn.jtvnw.net/emoticons/v2/25/static/light/2.0",
				}},
			},
			Cache: &failingAssetCache{putErr: errCachePut},
		}
		_, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}})
		if !errors.Is(err, errCachePut) {
			t.Fatalf("LookupMetadata error = %v, want %v", err, errCachePut)
		}
	})
}

func TestTwitchMetadataResolverMalformedMetadataKeepsFallback(t *testing.T) {
	resolver := &TwitchMetadataResolver{
		Lookup: &fakeTwitchChatMetadataLookup{
			globalEmotes: []twitch.EmoteMetadata{{ID: "25", Name: "Kappa"}},
			globalBadges: []twitch.BadgeMetadata{{SetID: "vip", ID: "1"}},
		},
		Cache: storage.NewMemoryAssetCache(),
	}

	emote, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}})
	if err != nil {
		t.Fatalf("LookupMetadata emote error = %v", err)
	}
	badge, err := resolver.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindBadge, ID: "vip/1"}})
	if err != nil {
		t.Fatalf("LookupMetadata badge error = %v", err)
	}
	if emote.URL != "" || badge.URL != "" {
		t.Fatalf("metadata URLs = %q %q, want empty malformed metadata fallbacks", emote.URL, badge.URL)
	}
}

func TestResolveMessageRefsPreservesGoldenFallbackRows(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 2, 12, 34, 0, 0, time.Local),
		DisplayName: "unicode_fan",
		Badges:      []twitch.Badge{{SetID: "vip", ID: "1"}},
		Text:        "café 😀 Kappa!",
		Type:        twitch.MessageTypeChat,
		Emotes:      []twitch.Emote{{ID: "25", Name: "Kappa", Start: 7, End: 11}},
	}
	resolver := &TwitchMetadataResolver{
		Lookup: &fakeTwitchChatMetadataLookup{
			globalEmotes: []twitch.EmoteMetadata{{
				ID:          "25",
				Name:        "Kappa",
				TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}",
				Formats:     []string{"static"},
				Scales:      []string{"2.0"},
				ThemeModes:  []string{"light"},
			}},
			globalBadges: []twitch.BadgeMetadata{{
				SetID:      "vip",
				ID:         "1",
				Title:      "VIP",
				ImageURL2X: "https://static-cdn.jtvnw.net/badges/v1/vip/2",
			}},
		},
		Cache: storage.NewMemoryAssetCache(),
	}

	before := render.PlainRows(msg, 80)
	resolved, err := resolver.ResolveMessageRefs(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("ResolveMessageRefs error = %v", err)
	}
	after := render.PlainRows(resolved, 80)
	want := []string{"12:34 [vip] unicode_fan: café 😀 Kappa!"}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("before rows mismatch\n got: %#v\nwant: %#v", before, want)
	}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("after rows mismatch\n got: %#v\nwant: %#v", after, want)
	}

	rows := render.Rows(resolved, render.DefaultOptions(80))
	emote, ok := firstRenderKind(rows, render.FragmentEmoteFallback)
	if !ok {
		t.Fatal("resolved rows missing emote fragment")
	}
	if got, want := emote.Text, "Kappa"; got != want {
		t.Fatalf("emote fallback token = %q, want exact %q", got, want)
	}
	if got := emote.Ref.URL; !strings.Contains(got, "/25/static/light/2.0") {
		t.Fatalf("emote ref URL = %q, want resolved image URL", got)
	}
	badge, ok := firstRenderKind(rows, render.FragmentBadge)
	if !ok {
		t.Fatal("resolved rows missing badge fragment")
	}
	if got, want := badge.Ref.URL, "https://static-cdn.jtvnw.net/badges/v1/vip/2"; got != want {
		t.Fatalf("badge ref URL = %q, want %q", got, want)
	}
}

func TestResolveMessageRefsPreservesFragmentText(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 2, 12, 34, 0, 0, time.Local),
		DisplayName: "fragment_fan",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "exact token "},
			{Type: twitch.FragmentEmote, Text: "Keepo", Ref: twitch.AssetRef{Kind: KindTwitchEmote, ID: "1902"}},
			{Type: twitch.FragmentText, Text: " stays"},
		},
	}
	resolver := &TwitchMetadataResolver{
		Lookup: &fakeTwitchChatMetadataLookup{
			globalEmotes: []twitch.EmoteMetadata{{
				ID:         "1902",
				Name:       "DifferentProviderName",
				ImageURL2X: "https://static-cdn.jtvnw.net/emoticons/v2/1902/static/light/2.0",
			}},
		},
		Cache: storage.NewMemoryAssetCache(),
	}

	resolved, err := resolver.ResolveMessageRefs(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("ResolveMessageRefs error = %v", err)
	}
	if got, want := render.PlainRows(resolved, 80), []string{"12:34 fragment_fan: exact token Keepo stays"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if got := resolved.Fragments[1].Text; got != "Keepo" {
		t.Fatalf("resolved fragment text = %q, want original Keepo token", got)
	}
	if resolved.Fragments[1].Ref.URL == "" {
		t.Fatalf("resolved fragment ref = %#v, want image URL", resolved.Fragments[1].Ref)
	}
}

func TestResolveMessageRefsPreservesDirectFragmentEmoteCDNURL(t *testing.T) {
	rawURL := "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0#e=0"
	cleanURL := "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0"
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 2, 12, 34, 0, 0, time.Local),
		DisplayName: "fragment_fan",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "sent "},
			{Type: twitch.FragmentEmote, Text: "Party", Ref: twitch.AssetRef{Kind: KindTwitchEmote, URL: rawURL}},
		},
	}
	resolver := &TwitchMetadataResolver{
		Lookup: &fakeTwitchChatMetadataLookup{},
		Cache:  storage.NewMemoryAssetCache(),
	}

	resolved, err := resolver.ResolveMessageRefs(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("ResolveMessageRefs error = %v", err)
	}
	if got, want := render.PlainRows(resolved, 80), []string{"12:34 fragment_fan: sent Party"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
	ref := resolved.Fragments[1].Ref
	if got, want := ref.ID, "emotesv2_299397e0339249f8a1b50f0affb044d8"; got != want {
		t.Fatalf("fragment ref ID = %q, want %q", got, want)
	}
	if got := ref.URL; got != cleanURL {
		t.Fatalf("fragment ref URL = %q, want cleaned URL %q", got, cleanURL)
	}
}

func TestResolveMessageRefsDefaultsBadgeKindWhenIDAlreadySet(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 2, 12, 34, 0, 0, time.Local),
		DisplayName: "badge_fan",
		Type:        twitch.MessageTypeChat,
		Text:        "badge ref already has an id",
		Badges: []twitch.Badge{{
			SetID: "vip",
			ID:    "1",
			Ref:   twitch.AssetRef{ID: "vip/1"},
		}},
	}
	resolver := &TwitchMetadataResolver{
		Lookup: &fakeTwitchChatMetadataLookup{
			globalBadges: []twitch.BadgeMetadata{{
				SetID:      "vip",
				ID:         "1",
				Title:      "VIP",
				ImageURL2X: "https://static-cdn.jtvnw.net/badges/v1/vip/2",
			}},
		},
		Cache: storage.NewMemoryAssetCache(),
	}

	resolved, err := resolver.ResolveMessageRefs(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("ResolveMessageRefs error = %v", err)
	}
	if got, want := resolved.Badges[0].Ref.Kind, KindBadge; got != want {
		t.Fatalf("badge ref kind = %q, want %q", got, want)
	}
	if got, want := resolved.Badges[0].Ref.URL, "https://static-cdn.jtvnw.net/badges/v1/vip/2"; got != want {
		t.Fatalf("badge ref URL = %q, want %q", got, want)
	}
}

type fakeTwitchChatMetadataLookup struct {
	globalEmoteCalls  int
	channelEmoteCalls int
	globalBadgeCalls  int
	channelBadgeCalls int
	lastChannelID     string
	globalEmotes      []twitch.EmoteMetadata
	channelEmotes     []twitch.EmoteMetadata
	globalBadges      []twitch.BadgeMetadata
	channelBadges     []twitch.BadgeMetadata
	err               error
}

func (f *fakeTwitchChatMetadataLookup) GetGlobalEmotes(context.Context) ([]twitch.EmoteMetadata, error) {
	f.globalEmoteCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.globalEmotes, nil
}

func (f *fakeTwitchChatMetadataLookup) GetChannelEmotes(_ context.Context, channelID string) ([]twitch.EmoteMetadata, error) {
	f.channelEmoteCalls++
	f.lastChannelID = channelID
	if f.err != nil {
		return nil, f.err
	}
	return f.channelEmotes, nil
}

func (f *fakeTwitchChatMetadataLookup) GetGlobalBadges(context.Context) ([]twitch.BadgeMetadata, error) {
	f.globalBadgeCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.globalBadges, nil
}

func (f *fakeTwitchChatMetadataLookup) GetChannelBadges(_ context.Context, channelID string) ([]twitch.BadgeMetadata, error) {
	f.channelBadgeCalls++
	f.lastChannelID = channelID
	if f.err != nil {
		return nil, f.err
	}
	return f.channelBadges, nil
}

func (f *fakeTwitchChatMetadataLookup) totalCalls() int {
	return f.globalEmoteCalls + f.channelEmoteCalls + f.globalBadgeCalls + f.channelBadgeCalls
}

type failingAssetCache struct {
	getErr error
	putErr error
}

func (c *failingAssetCache) GetAsset(context.Context, storage.AssetKey) (storage.AssetRecord, bool, error) {
	return storage.AssetRecord{}, false, c.getErr
}

func (c *failingAssetCache) PutAsset(context.Context, storage.AssetRecord) error {
	return c.putErr
}

func firstRenderKind(rows []render.Row, kind render.FragmentKind) (render.Fragment, bool) {
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind == kind {
				return fragment, true
			}
		}
	}
	return render.Fragment{}, false
}
