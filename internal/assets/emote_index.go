package assets

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

const defaultEmoteIndexTTL = 24 * time.Hour

// EmoteEntry is one autocomplete-searchable emote name.
type EmoteEntry struct {
	Name string
	Ref  twitch.AssetRef
}

// EmoteLister resolves the full available emote set for a channel, for
// autocomplete search.
type EmoteLister interface {
	GetGlobalEmotes(context.Context) ([]twitch.EmoteMetadata, error)
	GetChannelEmotes(context.Context, string) ([]twitch.EmoteMetadata, error)
}

type emoteIndexEntry struct {
	fetchedAt time.Time
	emotes    []EmoteEntry
}

// EmoteIndex caches a name-sorted, deduplicated emote list per channel for
// Ctrl+E autocomplete search and the composer's quick-select row. It is
// purely in-memory: the storage asset cache was shaped for single image
// records, not name lists, and it went away with the image renderer (ADR
// 0003, superseded), so there is no disk-cache abstraction to reuse. Safe
// for concurrent use.
type EmoteIndex struct {
	Lister EmoteLister
	TTL    time.Duration
	Now    func() time.Time

	mu      sync.Mutex
	entries map[string]emoteIndexEntry
}

// NewEmoteIndex creates an EmoteIndex backed by lister. The returned index
// performs no network I/O until Load is called.
func NewEmoteIndex(lister EmoteLister) *EmoteIndex {
	return &EmoteIndex{Lister: lister, entries: make(map[string]emoteIndexEntry)}
}

// Load returns the cached (or freshly fetched) name-sorted emote list
// combining global and channel emotes for channelID. An empty channelID
// returns global emotes only. A nil index or Lister returns (nil, nil).
func (idx *EmoteIndex) Load(ctx context.Context, channelID string) ([]EmoteEntry, error) {
	if idx == nil || idx.Lister == nil {
		return nil, nil
	}
	channelID = strings.TrimSpace(channelID)

	// The global list is identical for every channel, so it is cached under
	// its own key and merged at read time; caching it once per channel used
	// to refetch the same list for every channel that was opened.
	global, err := idx.cached(ctx, "", idx.Lister.GetGlobalEmotes)
	if err != nil {
		return nil, err
	}
	if channelID == "" {
		// Clone: the caller shares nothing with the cache, so mutating the
		// returned slice cannot corrupt the entries every later Load returns.
		return slices.Clone(global), nil
	}
	channel, err := idx.cached(ctx, channelID, func(ctx context.Context) ([]twitch.EmoteMetadata, error) {
		return idx.Lister.GetChannelEmotes(ctx, channelID)
	})
	if err != nil {
		return nil, err
	}
	return mergeEmoteEntries(channel, global), nil
}

// cached returns the emote list stored under key, fetching and caching it on
// a miss or once the TTL has expired. The returned slice is the cache's own;
// callers must not mutate it.
func (idx *EmoteIndex) cached(ctx context.Context, key string, fetch func(context.Context) ([]twitch.EmoteMetadata, error)) ([]EmoteEntry, error) {
	idx.mu.Lock()
	if entry, ok := idx.entries[key]; ok && idx.now().Before(entry.fetchedAt.Add(idx.ttl())) {
		emotes := entry.emotes
		idx.mu.Unlock()
		return emotes, nil
	}
	idx.mu.Unlock()

	metadata, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	emotes := emoteEntries(metadata)

	idx.mu.Lock()
	// Initialize here rather than requiring a constructor. EmoteIndex is
	// otherwise usable as &EmoteIndex{Lister: l} -- every other field has a
	// working zero value -- and a struct that looks constructible but panics
	// on first write is a trap for the next caller.
	if idx.entries == nil {
		idx.entries = make(map[string]emoteIndexEntry)
	}
	idx.entries[key] = emoteIndexEntry{fetchedAt: idx.now(), emotes: emotes}
	idx.mu.Unlock()
	return emotes, nil
}

func (idx *EmoteIndex) ttl() time.Duration {
	if idx.TTL > 0 {
		return idx.TTL
	}
	return defaultEmoteIndexTTL
}

func (idx *EmoteIndex) now() time.Time {
	if idx.Now != nil {
		return idx.Now()
	}
	return time.Now()
}

// emoteEntries converts one fetched metadata list into name-sorted,
// deduplicated entries.
func emoteEntries(metadata []twitch.EmoteMetadata) []EmoteEntry {
	seen := make(map[string]bool, len(metadata))
	entries := make([]EmoteEntry, 0, len(metadata))
	for _, emote := range metadata {
		name := strings.TrimSpace(emote.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, EmoteEntry{
			Name: name,
			Ref:  twitch.AssetRef{Kind: KindTwitchEmote, ID: strings.TrimSpace(emote.ID), URL: emote.ImageURL()},
		})
	}
	sortEmoteEntries(entries)
	return entries
}

// mergeEmoteEntries deduplicates by name across cached lists, keeping the
// first occurrence (callers pass channel emotes before global emotes so
// channel-specific emotes win on name collision), then sorts by name. The
// result is a fresh slice that shares nothing with the cached inputs, so the
// caller cannot corrupt the cache through it.
func mergeEmoteEntries(lists ...[]EmoteEntry) []EmoteEntry {
	seen := make(map[string]bool)
	entries := make([]EmoteEntry, 0)
	for _, list := range lists {
		for _, entry := range list {
			if entry.Name == "" || seen[entry.Name] {
				continue
			}
			seen[entry.Name] = true
			entries = append(entries, entry)
		}
	}
	sortEmoteEntries(entries)
	return entries
}

// sortEmoteEntries orders entries by name, the order autocomplete search and
// the composer's quick-select row present them in.
func sortEmoteEntries(entries []EmoteEntry) {
	slices.SortFunc(entries, func(a, b EmoteEntry) int { return strings.Compare(a.Name, b.Name) })
}
