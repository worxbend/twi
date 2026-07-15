package assets

import (
	"context"
	"sort"
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
// autocomplete search. This is distinct from TwitchChatMetadataLookup's
// per-message lazy metadata lookups, which only resolve emotes already seen
// in chat rather than the complete available set.
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
// purely in-memory: AssetRecord/AssetCache are shaped for single image
// records, not name lists, so this doesn't reuse that disk-cache
// abstraction. Safe for concurrent use.
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

	idx.mu.Lock()
	if entry, ok := idx.entries[channelID]; ok && idx.now().Before(entry.fetchedAt.Add(idx.ttl())) {
		idx.mu.Unlock()
		return entry.emotes, nil
	}
	idx.mu.Unlock()

	global, err := idx.Lister.GetGlobalEmotes(ctx)
	if err != nil {
		return nil, err
	}
	var channel []twitch.EmoteMetadata
	if channelID != "" {
		channel, err = idx.Lister.GetChannelEmotes(ctx, channelID)
		if err != nil {
			return nil, err
		}
	}
	emotes := mergeEmoteEntries(channel, global)

	idx.mu.Lock()
	idx.entries[channelID] = emoteIndexEntry{fetchedAt: idx.now(), emotes: emotes}
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

// mergeEmoteEntries deduplicates by name across lists, keeping the first
// occurrence (callers pass channel emotes before global emotes so
// channel-specific emotes win on name collision), then sorts by name.
func mergeEmoteEntries(lists ...[]twitch.EmoteMetadata) []EmoteEntry {
	seen := make(map[string]bool)
	entries := make([]EmoteEntry, 0)
	for _, list := range lists {
		for _, emote := range list {
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
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}
