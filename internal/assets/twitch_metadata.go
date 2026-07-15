package assets

import (
	"context"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

const defaultTwitchMetadataTTL = 24 * time.Hour

// TwitchChatMetadataLookup is the provider boundary used by TwitchMetadataResolver.
type TwitchChatMetadataLookup interface {
	GetGlobalEmotes(context.Context) ([]twitch.EmoteMetadata, error)
	GetChannelEmotes(context.Context, string) ([]twitch.EmoteMetadata, error)
	GetGlobalBadges(context.Context) ([]twitch.BadgeMetadata, error)
	GetChannelBadges(context.Context, string) ([]twitch.BadgeMetadata, error)
}

// TwitchMetadataResolver resolves Twitch emote and badge metadata into public
// image URLs and caches metadata-only asset records with URL-free keys.
type TwitchMetadataResolver struct {
	Lookup TwitchChatMetadataLookup
	Cache  storage.AssetCache
	Now    func() time.Time
	TTL    time.Duration
}

var _ MetadataLookup = (*TwitchMetadataResolver)(nil)

// LookupMetadata resolves one Twitch emote or badge asset ref. Unknown refs
// return metadata with no URL so callers can keep readable fallback text.
func (r *TwitchMetadataResolver) LookupMetadata(ctx context.Context, req MetadataRequest) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	ref := normalizeRef(req.Ref)
	switch ref.Kind {
	case KindTwitchEmote:
		return r.lookupEmote(ctx, ref, req.ChannelID)
	case KindBadge:
		return r.lookupBadge(ctx, ref, req.ChannelID)
	default:
		return Metadata{
			Ref: ref,
			URL: ref.URL,
		}, nil
	}
}

// ResolveMessageRefs returns a copy of msg with resolved badge and emote refs.
// Fallback text fields are not changed.
func (r *TwitchMetadataResolver) ResolveMessageRefs(ctx context.Context, msg twitch.ChatMessage, channelID string) (twitch.ChatMessage, error) {
	out := msg
	out.Badges = append([]twitch.Badge(nil), msg.Badges...)
	out.Fragments = append([]twitch.MessageFragment(nil), msg.Fragments...)
	out.Emotes = append([]twitch.Emote(nil), msg.Emotes...)
	for i, badge := range out.Badges {
		if badge.Ref.Kind == "" {
			badge.Ref.Kind = KindBadge
		}
		if badge.Ref.ID == "" {
			badge.Ref.ID = badgeAssetID(badge)
		}
		metadata, err := r.LookupMetadata(ctx, MetadataRequest{Ref: badge.Ref, ChannelID: channelID})
		if err != nil {
			return out, err
		}
		if metadata.URL != "" {
			badge.Ref = metadata.Ref
			badge.Ref.URL = metadata.URL
		}
		out.Badges[i] = badge
	}
	for i, fragment := range out.Fragments {
		if fragment.Type != twitch.FragmentEmote {
			continue
		}
		ref := fragment.Ref
		if ref.Kind == "" {
			ref.Kind = KindTwitchEmote
		}
		if ref.ID == "" && strings.TrimSpace(ref.URL) == "" {
			ref.ID = strings.TrimSpace(fragment.Text)
		}
		metadata, err := r.LookupMetadata(ctx, MetadataRequest{Ref: ref, ChannelID: channelID})
		if err != nil {
			return out, err
		}
		if metadata.URL != "" {
			fragment.Ref = metadata.Ref
			fragment.Ref.URL = metadata.URL
			out.Fragments[i] = fragment
		}
	}
	for i, emote := range out.Emotes {
		ref := emote.Ref
		if ref.Kind == "" {
			ref.Kind = KindTwitchEmote
		}
		if ref.ID == "" && strings.TrimSpace(ref.URL) == "" {
			ref.ID = emote.ID
		}
		metadata, err := r.LookupMetadata(ctx, MetadataRequest{Ref: ref, ChannelID: channelID})
		if err != nil {
			return out, err
		}
		if metadata.URL != "" {
			emote.Ref = metadata.Ref
			emote.Ref.URL = metadata.URL
			out.Emotes[i] = emote
		}
	}
	return out, ctx.Err()
}

func (r *TwitchMetadataResolver) lookupEmote(ctx context.Context, ref twitch.AssetRef, channelID string) (Metadata, error) {
	if strings.TrimSpace(ref.URL) != "" {
		metadata := directEmoteMetadata(ref)
		if metadata.URL == "" {
			return Metadata{Ref: metadata.Ref}, nil
		}
		key := CacheKey(metadata.Ref)
		if key.ID == "" {
			return metadata, ctx.Err()
		}
		if err := r.putMetadata(ctx, key, metadata); err != nil {
			return Metadata{}, err
		}
		return metadata, ctx.Err()
	}
	key := CacheKey(ref)
	if key.ID == "" {
		return Metadata{Ref: ref}, nil
	}
	if metadata, ok, err := r.cachedMetadata(ctx, key, ref); err != nil || ok {
		return metadata, err
	}
	if r == nil || r.Lookup == nil {
		return Metadata{Ref: ref}, nil
	}

	if strings.TrimSpace(channelID) != "" {
		channel, err := r.Lookup.GetChannelEmotes(ctx, channelID)
		if err != nil {
			return Metadata{}, err
		}
		if metadata, ok, err := r.cacheEmotes(ctx, channel, ref.ID); err != nil || ok {
			return metadataWithRef(metadata, ref), err
		}
	}
	global, err := r.Lookup.GetGlobalEmotes(ctx)
	if err != nil {
		return Metadata{}, err
	}
	if metadata, ok, err := r.cacheEmotes(ctx, global, ref.ID); err != nil || ok {
		return metadataWithRef(metadata, ref), err
	}
	return Metadata{Ref: ref}, ctx.Err()
}

func (r *TwitchMetadataResolver) lookupBadge(ctx context.Context, ref twitch.AssetRef, channelID string) (Metadata, error) {
	ref.ID = strings.TrimSpace(ref.ID)
	if ref.ID == "" {
		return Metadata{Ref: ref}, nil
	}
	if strings.TrimSpace(channelID) != "" {
		if metadata, ok, err := r.cachedMetadata(ctx, cacheKey(ref, channelID), ref); err != nil || ok {
			return metadata, err
		}
		if r == nil || r.Lookup == nil {
			return Metadata{Ref: ref}, nil
		}
		channel, err := r.Lookup.GetChannelBadges(ctx, channelID)
		if err != nil {
			return Metadata{}, err
		}
		if metadata, ok, err := r.cacheBadges(ctx, channel, channelID, ref.ID); err != nil || ok {
			return metadataWithRef(metadata, ref), err
		}
	}
	if metadata, ok, err := r.cachedMetadata(ctx, cacheKey(ref, ""), ref); err != nil || ok {
		return metadata, err
	}
	if r == nil || r.Lookup == nil {
		return Metadata{Ref: ref}, nil
	}
	global, err := r.Lookup.GetGlobalBadges(ctx)
	if err != nil {
		return Metadata{}, err
	}
	if metadata, ok, err := r.cacheBadges(ctx, global, "", ref.ID); err != nil || ok {
		return metadataWithRef(metadata, ref), err
	}
	return Metadata{Ref: ref}, ctx.Err()
}

func (r *TwitchMetadataResolver) cachedMetadata(ctx context.Context, key storage.AssetKey, ref twitch.AssetRef) (Metadata, bool, error) {
	if r == nil || r.Cache == nil || key.ID == "" {
		return Metadata{}, false, nil
	}
	record, ok, err := r.Cache.GetAsset(ctx, key)
	if err != nil || !ok {
		return Metadata{}, false, err
	}
	if !cacheRecordFresh(record, r.now()) || strings.TrimSpace(record.SourceURL) == "" {
		return Metadata{}, false, nil
	}
	return Metadata{
		Ref:         twitch.AssetRef{Kind: ref.Kind, ID: ref.ID, URL: record.SourceURL},
		URL:         record.SourceURL,
		MediaType:   record.MediaType,
		WidthCells:  record.WidthCells,
		HeightCells: record.HeightCells,
		ExpiresAt:   record.ExpiresAt,
	}, true, nil
}

func (r *TwitchMetadataResolver) cacheEmotes(ctx context.Context, emotes []twitch.EmoteMetadata, targetID string) (Metadata, bool, error) {
	var match Metadata
	for _, emote := range emotes {
		metadata := emoteMetadata(emote)
		if metadata.URL == "" || metadata.Ref.ID == "" {
			continue
		}
		if err := r.putMetadata(ctx, CacheKey(metadata.Ref), metadata); err != nil {
			return Metadata{}, false, err
		}
		if metadata.Ref.ID == targetID {
			match = metadata
		}
	}
	return match, match.Ref.ID != "", ctx.Err()
}

func (r *TwitchMetadataResolver) cacheBadges(ctx context.Context, badges []twitch.BadgeMetadata, channelID, targetID string) (Metadata, bool, error) {
	var match Metadata
	for _, badge := range badges {
		metadata := badgeMetadata(badge)
		if metadata.URL == "" || metadata.Ref.ID == "" {
			continue
		}
		if err := r.putMetadata(ctx, cacheKey(metadata.Ref, channelID), metadata); err != nil {
			return Metadata{}, false, err
		}
		if metadata.Ref.ID == targetID {
			match = metadata
		}
	}
	return match, match.Ref.ID != "", ctx.Err()
}

func (r *TwitchMetadataResolver) putMetadata(ctx context.Context, key storage.AssetKey, metadata Metadata) error {
	if r == nil || r.Cache == nil || key.ID == "" {
		return nil
	}
	now := r.now()
	expiresAt := metadata.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(r.ttl())
	}
	return r.Cache.PutAsset(ctx, storage.AssetRecord{
		Key:         key,
		SourceURL:   metadata.URL,
		MediaType:   metadata.MediaType,
		WidthCells:  metadata.WidthCells,
		HeightCells: metadata.HeightCells,
		FetchedAt:   now,
		ExpiresAt:   expiresAt,
	})
}

func (r *TwitchMetadataResolver) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *TwitchMetadataResolver) ttl() time.Duration {
	if r != nil && r.TTL > 0 {
		return r.TTL
	}
	return defaultTwitchMetadataTTL
}

func emoteMetadata(emote twitch.EmoteMetadata) Metadata {
	url := emote.ImageURL()
	return Metadata{
		Ref:         twitch.AssetRef{Kind: KindTwitchEmote, ID: strings.TrimSpace(emote.ID), URL: url},
		Name:        strings.TrimSpace(emote.Name),
		URL:         url,
		MediaType:   mediaTypeForURL(url, "image/png"),
		WidthCells:  2,
		HeightCells: 1,
	}
}

func directEmoteMetadata(ref twitch.AssetRef) Metadata {
	cleanURL, id, ok := normalizeDirectTwitchEmoteURL(ref.URL)
	if !ok {
		ref.URL = ""
		return Metadata{Ref: ref}
	}
	if strings.TrimSpace(ref.ID) == "" {
		ref.ID = id
	}
	ref.URL = cleanURL
	return Metadata{
		Ref:         ref,
		URL:         cleanURL,
		MediaType:   mediaTypeForURL(cleanURL, "image/png"),
		WidthCells:  2,
		HeightCells: 1,
	}
}

func normalizeDirectTwitchEmoteURL(raw string) (cleanURL, id string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || containsCredentialMarker(raw) {
		return "", "", false
	}
	cleanURL, id, ok = twitch.StaticEmoteCDNURL(raw)
	if !ok || unsafeAssetKeyPart(id) {
		return "", "", false
	}
	return cleanURL, id, true
}

func badgeMetadata(badge twitch.BadgeMetadata) Metadata {
	url := badge.ImageURL()
	return Metadata{
		Ref:         twitch.AssetRef{Kind: KindBadge, ID: badgeAssetID(twitch.Badge{SetID: badge.SetID, ID: badge.ID}), URL: url},
		Name:        strings.TrimSpace(firstNonEmpty(badge.Title, badge.Description, badge.SetID)),
		URL:         url,
		MediaType:   mediaTypeForURL(url, "image/png"),
		WidthCells:  2,
		HeightCells: 1,
	}
}

func metadataWithRef(metadata Metadata, ref twitch.AssetRef) Metadata {
	if metadata.Ref.Kind == "" {
		metadata.Ref.Kind = ref.Kind
	}
	if metadata.Ref.ID == "" {
		metadata.Ref.ID = ref.ID
	}
	if metadata.Ref.URL == "" {
		metadata.Ref.URL = metadata.URL
	}
	return metadata
}

func badgeAssetID(badge twitch.Badge) string {
	setID := strings.TrimSpace(badge.SetID)
	id := strings.TrimSpace(badge.ID)
	if id == "" {
		return setID
	}
	return setID + "/" + id
}

func mediaTypeForURL(value, fallback string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasSuffix(lower, ".gif") || strings.Contains(lower, "/animated/"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasPrefix(lower, "https://static-cdn.jtvnw.net/emoticons/"),
		strings.HasPrefix(lower, "https://static-cdn.jtvnw.net/badges/"),
		strings.Contains(lower, "/static/"):
		return "image/png"
	default:
		return fallback
	}
}
