package assets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/twitch"
)

type fakeEmoteLister struct {
	global       []twitch.EmoteMetadata
	channel      map[string][]twitch.EmoteMetadata
	globalCalls  int
	channelCalls int
	err          error
}

func (f *fakeEmoteLister) GetGlobalEmotes(context.Context) ([]twitch.EmoteMetadata, error) {
	f.globalCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.global, nil
}

func (f *fakeEmoteLister) GetChannelEmotes(_ context.Context, channelID string) ([]twitch.EmoteMetadata, error) {
	f.channelCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.channel[channelID], nil
}

func TestEmoteIndexMergesDedupesAndSortsByName(t *testing.T) {
	lister := &fakeEmoteLister{
		global: []twitch.EmoteMetadata{
			{ID: "1", Name: "Kappa", TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"},
			{ID: "2", Name: "PogChamp", TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"},
		},
		channel: map[string][]twitch.EmoteMetadata{
			"123": {
				{ID: "3", Name: "channelPog", TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"},
				// Same name as a global emote: channel entry should win.
				{ID: "4", Name: "Kappa", TemplateURL: "https://static-cdn.jtvnw.net/emoticons/v2/{{id}}/{{format}}/{{theme_mode}}/{{scale}}"},
			},
		},
	}
	idx := NewEmoteIndex(lister)

	entries, err := idx.Load(context.Background(), "123")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %#v, want 3 deduplicated entries", entries)
	}
	names := []string{entries[0].Name, entries[1].Name, entries[2].Name}
	if names[0] != "Kappa" || names[1] != "PogChamp" || names[2] != "channelPog" {
		t.Fatalf("names = %#v, want sorted [Kappa PogChamp channelPog]", names)
	}
	if entries[0].Ref.ID != "4" {
		t.Fatalf("Kappa ref id = %q, want channel emote id 4 (channel wins on collision)", entries[0].Ref.ID)
	}
}

func TestEmoteIndexCachesWithinTTL(t *testing.T) {
	lister := &fakeEmoteLister{global: []twitch.EmoteMetadata{{ID: "1", Name: "Kappa"}}}
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	idx := &EmoteIndex{Lister: lister, entries: make(map[string]emoteIndexEntry), Now: func() time.Time { return now }}

	if _, err := idx.Load(context.Background(), ""); err != nil {
		t.Fatalf("first Load error = %v", err)
	}
	if _, err := idx.Load(context.Background(), ""); err != nil {
		t.Fatalf("second Load error = %v", err)
	}
	if lister.globalCalls != 1 {
		t.Fatalf("globalCalls = %d, want 1 (second Load should hit cache)", lister.globalCalls)
	}

	now = now.Add(25 * time.Hour)
	if _, err := idx.Load(context.Background(), ""); err != nil {
		t.Fatalf("third Load error = %v", err)
	}
	if lister.globalCalls != 2 {
		t.Fatalf("globalCalls = %d, want 2 (expired TTL should refetch)", lister.globalCalls)
	}
}

func TestEmoteIndexNilAndErrorHandling(t *testing.T) {
	var nilIdx *EmoteIndex
	if entries, err := nilIdx.Load(context.Background(), ""); entries != nil || err != nil {
		t.Fatalf("nil index Load = (%#v, %v), want (nil, nil)", entries, err)
	}

	idx := NewEmoteIndex(nil)
	if entries, err := idx.Load(context.Background(), ""); entries != nil || err != nil {
		t.Fatalf("nil lister Load = (%#v, %v), want (nil, nil)", entries, err)
	}

	failing := &fakeEmoteLister{err: errors.New("boom")}
	idx = NewEmoteIndex(failing)
	if _, err := idx.Load(context.Background(), "123"); err == nil {
		t.Fatal("Load error = nil, want propagated lister error")
	}
}

func TestEmoteIndexEmptyChannelIDSkipsChannelLookup(t *testing.T) {
	lister := &fakeEmoteLister{global: []twitch.EmoteMetadata{{ID: "1", Name: "Kappa"}}}
	idx := NewEmoteIndex(lister)
	if _, err := idx.Load(context.Background(), ""); err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if lister.channelCalls != 0 {
		t.Fatalf("channelCalls = %d, want 0 for empty channel ID", lister.channelCalls)
	}
}
