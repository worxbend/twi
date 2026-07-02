package assets

import (
	"context"
	"errors"
	"time"

	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

const (
	KindAvatar      = "avatar"
	KindBadge       = "badge"
	KindTwitchEmote = "twitch_emote"
	KindEmoji       = "emoji"
)

// Request describes one asset that should be resolved outside render/view
// paths. Callers can pass the returned Event through Bubble Tea messages.
type Request struct {
	ID          string
	Ref         twitch.AssetRef
	ChannelID   string
	UserID      string
	UserLogin   string
	Fallback    string
	WidthCells  int
	HeightCells int
}

// EventKind identifies an app-facing asset resolution result.
type EventKind string

const (
	EventCacheHit   EventKind = "cache_hit"
	EventDownloaded EventKind = "downloaded"
	EventFailed     EventKind = "failed"
	EventCanceled   EventKind = "canceled"
)

// Event is the app-facing result emitted after an asynchronous asset command
// resolves one Request. It never includes OAuth tokens or request headers.
type Event struct {
	Kind      EventKind
	RequestID string
	Ref       twitch.AssetRef
	Record    storage.AssetRecord
	Metadata  Metadata
	FromCache bool
	Err       error
	At        time.Time
}

// IdentityLookup resolves user identity and avatar URLs for avatar requests.
type IdentityLookup interface {
	LookupIdentity(context.Context, IdentityRequest) (Identity, error)
}

// MetadataLookup resolves image metadata and download sources for asset refs.
type MetadataLookup interface {
	LookupMetadata(context.Context, MetadataRequest) (Metadata, error)
}

// Downloader fetches an already-authorized public asset source into local
// storage. Implementations must respect context cancellation.
type Downloader interface {
	Download(context.Context, DownloadRequest) (DownloadResult, error)
}

// EventResolver is the app-facing boundary used by Bubble Tea commands.
type EventResolver interface {
	Resolve(context.Context, Request) Event
}

// IdentityRequest identifies a Twitch user without requiring transport types.
type IdentityRequest struct {
	Ref       twitch.AssetRef
	UserID    string
	UserLogin string
}

// Identity contains the identity fields needed by avatar resolution.
type Identity struct {
	UserID      string
	Login       string
	DisplayName string
	AvatarURL   string
}

// MetadataRequest identifies avatar, emote, emoji, or badge metadata.
type MetadataRequest struct {
	Ref       twitch.AssetRef
	ChannelID string
}

// Metadata describes a public image source and stable terminal dimensions.
type Metadata struct {
	Ref         twitch.AssetRef
	Name        string
	URL         string
	MediaType   string
	WidthCells  int
	HeightCells int
	ExpiresAt   time.Time
}

// DownloadRequest is the downloader input after identity/metadata lookup.
type DownloadRequest struct {
	Ref       twitch.AssetRef
	URL       string
	MediaType string
}

// DownloadResult is the local cached representation produced by Downloader.
type DownloadResult struct {
	Path        string
	MediaType   string
	WidthCells  int
	HeightCells int
	FetchedAt   time.Time
	ExpiresAt   time.Time
}

// Resolver composes lookup, download, and cache boundaries for one asset.
type Resolver struct {
	Identity   IdentityLookup
	Metadata   MetadataLookup
	Downloader Downloader
	Cache      storage.AssetCache
	Now        func() time.Time
}

var _ EventResolver = (*Resolver)(nil)

var (
	ErrMissingSource = errors.New("missing asset download source")
	ErrNoDownloader  = errors.New("missing asset downloader")
)

// Resolve returns a single app-facing event. It is safe for Bubble Tea commands
// and other background workers; callers should not invoke it from View methods.
func (r *Resolver) Resolve(ctx context.Context, req Request) Event {
	if ctx == nil {
		ctx = context.Background()
	}
	now := r.now()
	ref := normalizeRequestRef(req)
	event := Event{
		RequestID: req.ID,
		Ref:       ref,
		At:        now,
	}
	if err := ctx.Err(); err != nil {
		event.Kind = EventCanceled
		event.Err = err
		return event
	}

	key := RequestCacheKey(req)
	if r != nil && r.Cache != nil && key.ID != "" {
		record, ok, err := r.Cache.GetAsset(ctx, key)
		if err != nil {
			return r.errorEvent(event, err)
		}
		if ok && cacheRecordFresh(record, now) {
			event.Kind = EventCacheHit
			event.Record = record
			event.FromCache = true
			return event
		}
	}

	metadata, err := r.lookupMetadata(ctx, ref, req)
	if err != nil {
		return r.errorEvent(event, err)
	}
	if metadata.Ref == (twitch.AssetRef{}) {
		metadata.Ref = ref
	}
	if metadata.URL == "" {
		metadata.URL = metadata.Ref.URL
	}
	event.Metadata = metadata
	event.Ref = metadata.Ref

	if metadata.URL == "" {
		return r.errorEvent(event, ErrMissingSource)
	}
	if r == nil || r.Downloader == nil {
		return r.errorEvent(event, ErrNoDownloader)
	}

	download, err := r.Downloader.Download(ctx, DownloadRequest{
		Ref:       metadata.Ref,
		URL:       metadata.URL,
		MediaType: metadata.MediaType,
	})
	if err != nil {
		return r.errorEvent(event, err)
	}

	record := recordFromDownload(cacheKey(metadata.Ref, req.ChannelID), metadata, download, req, now)
	if r.Cache != nil && record.Key.ID != "" {
		if err := r.Cache.PutAsset(ctx, record); err != nil {
			return r.errorEvent(event, err)
		}
	}

	event.Kind = EventDownloaded
	event.Record = record
	return event
}

func (r *Resolver) lookupMetadata(ctx context.Context, ref twitch.AssetRef, req Request) (Metadata, error) {
	if r != nil && ref.Kind == KindAvatar && (ref.URL == "" || ref.ID == "") && r.Identity != nil {
		identity, err := r.Identity.LookupIdentity(ctx, IdentityRequest{
			Ref:       ref,
			UserID:    firstNonEmpty(req.UserID, ref.ID),
			UserLogin: req.UserLogin,
		})
		if err != nil {
			return Metadata{}, err
		}
		if ref.ID == "" {
			ref.ID = firstNonEmpty(identity.UserID, identity.Login, req.UserLogin)
		}
		if ref.URL == "" {
			ref.URL = identity.AvatarURL
		}
	}

	if r != nil && r.Metadata != nil {
		return r.Metadata.LookupMetadata(ctx, MetadataRequest{
			Ref:       ref,
			ChannelID: req.ChannelID,
		})
	}

	return Metadata{
		Ref:         ref,
		URL:         ref.URL,
		WidthCells:  req.WidthCells,
		HeightCells: req.HeightCells,
	}, nil
}

func (r *Resolver) errorEvent(event Event, err error) Event {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		event.Kind = EventCanceled
	} else {
		event.Kind = EventFailed
	}
	event.Err = err
	return event
}

func (r *Resolver) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// CacheKey maps a public asset ref onto the shared storage cache key.
func CacheKey(ref twitch.AssetRef) storage.AssetKey {
	return cacheKey(ref, "")
}

// RequestCacheKey maps an asset request onto the shared storage cache key.
// Channel-scoped assets, such as badges, include the channel ID when present.
func RequestCacheKey(req Request) storage.AssetKey {
	return cacheKey(normalizeRequestRef(req), req.ChannelID)
}

func cacheKey(ref twitch.AssetRef, channelID string) storage.AssetKey {
	ref = normalizeRef(ref)
	id := ref.ID
	if ref.Kind == KindEmoji {
		if emojiID, ok := EmojiAssetID(id); ok {
			id = emojiID
		}
	}
	if ref.Kind == KindBadge && channelID != "" && id != "" {
		id = channelID + "/" + id
	}
	return storage.AssetKey{Kind: ref.Kind, ID: id}
}

func normalizeRef(ref twitch.AssetRef) twitch.AssetRef {
	if ref.Kind == "" {
		ref.Kind = KindEmoji
	}
	return ref
}

func normalizeRequestRef(req Request) twitch.AssetRef {
	ref := normalizeRef(req.Ref)
	if ref.Kind == KindAvatar && ref.ID == "" {
		ref.ID = req.UserID
	}
	return ref
}

func recordFromDownload(key storage.AssetKey, metadata Metadata, download DownloadResult, req Request, now time.Time) storage.AssetRecord {
	record := storage.AssetRecord{
		Key:         key,
		Path:        download.Path,
		SourceURL:   metadata.URL,
		MediaType:   firstNonEmpty(download.MediaType, metadata.MediaType),
		WidthCells:  firstPositive(download.WidthCells, metadata.WidthCells, req.WidthCells),
		HeightCells: firstPositive(download.HeightCells, metadata.HeightCells, req.HeightCells),
		FetchedAt:   download.FetchedAt,
		ExpiresAt:   download.ExpiresAt,
	}
	if record.FetchedAt.IsZero() {
		record.FetchedAt = now
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = metadata.ExpiresAt
	}
	return record
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func cacheRecordFresh(record storage.AssetRecord, now time.Time) bool {
	return record.ExpiresAt.IsZero() || record.ExpiresAt.After(now)
}
