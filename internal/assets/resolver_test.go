package assets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

var errDownloadFailure = errors.New("download failed")

func TestResolverReturnsCacheHitWithoutLookupOrDownload(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	ref := twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}
	record := storage.AssetRecord{
		Key:       CacheKey(ref),
		Path:      "emotes/25.png",
		MediaType: "image/png",
	}
	if err := cache.PutAsset(context.Background(), record); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}

	metadata := &fakeMetadataLookup{}
	downloader := &fakeDownloader{}
	resolver := &Resolver{
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      cache,
	}

	event := resolver.Resolve(context.Background(), Request{
		ID:  "req-1",
		Ref: ref,
	})

	if event.Kind != EventCacheHit {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventCacheHit)
	}
	if !event.FromCache {
		t.Fatal("event.FromCache = false, want true")
	}
	if event.Record != record {
		t.Fatalf("event.Record = %#v, want %#v", event.Record, record)
	}
	if metadata.calls != 0 {
		t.Fatalf("metadata calls = %d, want 0", metadata.calls)
	}
	if downloader.calls != 0 {
		t.Fatalf("downloader calls = %d, want 0", downloader.calls)
	}
}

func TestResolverRefreshesExpiredCacheRecord(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	ref := twitch.AssetRef{Kind: KindTwitchEmote, ID: "25"}
	expired := storage.AssetRecord{
		Key:       CacheKey(ref),
		Path:      "emotes/old-25.png",
		ExpiresAt: now.Add(-time.Minute),
	}
	if err := cache.PutAsset(context.Background(), expired); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}

	resolver := &Resolver{
		Metadata: &fakeMetadataLookup{
			result: Metadata{
				Ref:       ref,
				URL:       "https://cdn.example/emotes/25.png",
				MediaType: "image/png",
			},
		},
		Downloader: &fakeDownloader{result: DownloadResult{Path: "emotes/new-25.png"}},
		Cache:      cache,
		Now:        func() time.Time { return now },
	}

	event := resolver.Resolve(context.Background(), Request{ID: "req-expired", Ref: ref})

	if event.Kind != EventDownloaded {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventDownloaded)
	}
	if event.FromCache {
		t.Fatal("event.FromCache = true, want false")
	}
	if event.Record.Path != "emotes/new-25.png" {
		t.Fatalf("event.Record.Path = %q, want refreshed download", event.Record.Path)
	}
}

func TestResolverDoesNotTreatMetadataOnlyRecordAsPreparedAsset(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	ref := twitch.AssetRef{Kind: KindEmoji, ID: "😀"}
	if err := cache.PutAsset(context.Background(), storage.AssetRecord{
		Key:       CacheKey(ref),
		SourceURL: "https://cdn.example/emoji/1f600.png",
		MediaType: "image/png",
	}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}

	metadata := &fakeMetadataLookup{
		result: Metadata{
			Ref:       twitch.AssetRef{Kind: KindEmoji, ID: "1f600"},
			URL:       "https://cdn.example/emoji/1f600.png",
			MediaType: "image/png",
		},
	}
	downloader := &fakeDownloader{result: DownloadResult{Path: "emoji/1f600.png"}}
	resolver := &Resolver{
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      cache,
	}

	event := resolver.Resolve(context.Background(), Request{ID: "req-metadata-only", Ref: ref})

	if event.Kind != EventDownloaded {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventDownloaded)
	}
	if metadata.calls != 1 || downloader.calls != 1 {
		t.Fatalf("metadata/downloader calls = %d/%d, want 1/1", metadata.calls, downloader.calls)
	}
	if event.Record.Path != "emoji/1f600.png" {
		t.Fatalf("event.Record.Path = %q, want downloaded asset path", event.Record.Path)
	}
}

func TestResolverDownloadsAndCachesCacheMiss(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	ref := twitch.AssetRef{Kind: KindEmoji, ID: "😀"}
	metadata := &fakeMetadataLookup{
		result: Metadata{
			Ref:         ref,
			Name:        "grinning face",
			URL:         "https://cdn.example/emoji/grinning.png",
			MediaType:   "image/png",
			WidthCells:  2,
			HeightCells: 1,
			ExpiresAt:   now.Add(time.Hour),
		},
	}
	downloader := &fakeDownloader{
		result: DownloadResult{
			Path:            "emoji/grinning.png",
			PayloadIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	resolver := &Resolver{
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      cache,
		Now:        func() time.Time { return now },
	}

	event := resolver.Resolve(context.Background(), Request{
		ID:  "req-2",
		Ref: ref,
	})

	if event.Kind != EventDownloaded {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventDownloaded)
	}
	if metadata.calls != 1 {
		t.Fatalf("metadata calls = %d, want 1", metadata.calls)
	}
	if downloader.calls != 1 {
		t.Fatalf("downloader calls = %d, want 1", downloader.calls)
	}
	if downloader.last.URL != metadata.result.URL {
		t.Fatalf("download URL = %q, want %q", downloader.last.URL, metadata.result.URL)
	}
	if event.Record.Path != "emoji/grinning.png" {
		t.Fatalf("event.Record.Path = %q, want cached path", event.Record.Path)
	}
	if event.Record.PayloadIdentity != downloader.result.PayloadIdentity {
		t.Fatalf("event.Record.PayloadIdentity = %q, want %q", event.Record.PayloadIdentity, downloader.result.PayloadIdentity)
	}
	if event.Record.MediaType != "image/png" {
		t.Fatalf("event.Record.MediaType = %q, want image/png", event.Record.MediaType)
	}
	if event.Record.WidthCells != 2 || event.Record.HeightCells != 1 {
		t.Fatalf("event.Record cells = %dx%d, want 2x1", event.Record.WidthCells, event.Record.HeightCells)
	}
	if event.Record.FetchedAt != now {
		t.Fatalf("event.Record.FetchedAt = %s, want %s", event.Record.FetchedAt, now)
	}

	cached, ok, err := cache.GetAsset(context.Background(), CacheKey(ref))
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if !ok {
		t.Fatal("cache miss after download, want hit")
	}
	if cached != event.Record {
		t.Fatalf("cached record = %#v, want %#v", cached, event.Record)
	}
}

func TestResolverScopesBadgeCacheKeyByChannel(t *testing.T) {
	ref := twitch.AssetRef{Kind: KindBadge, ID: "subscriber/12"}
	channelA := Request{Ref: ref, ChannelID: "channel-a"}
	channelB := Request{Ref: ref, ChannelID: "channel-b"}

	if RequestCacheKey(channelA) == RequestCacheKey(channelB) {
		t.Fatalf("badge cache keys collided: %#v", RequestCacheKey(channelA))
	}

	cache := storage.NewMemoryAssetCache()
	channelARecord := storage.AssetRecord{
		Key:  RequestCacheKey(channelA),
		Path: "badges/channel-a/subscriber-12.png",
	}
	if err := cache.PutAsset(context.Background(), channelARecord); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}

	metadata := &fakeMetadataLookup{
		result: Metadata{
			Ref: ref,
			URL: "https://cdn.example/channel-b/subscriber-12.png",
		},
	}
	downloader := &fakeDownloader{
		result: DownloadResult{Path: "badges/channel-b/subscriber-12.png"},
	}
	resolver := &Resolver{
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      cache,
	}

	event := resolver.Resolve(context.Background(), channelB)

	if event.Kind != EventDownloaded {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventDownloaded)
	}
	if metadata.calls != 1 {
		t.Fatalf("metadata calls = %d, want 1", metadata.calls)
	}
	if event.Record.Path != "badges/channel-b/subscriber-12.png" {
		t.Fatalf("event.Record.Path = %q, want channel B record", event.Record.Path)
	}
}

func TestCacheKeyDoesNotUseURLAsIdentifier(t *testing.T) {
	ref := twitch.AssetRef{
		Kind: KindAvatar,
		URL:  "https://cdn.example/avatar.png?signature=secret",
	}

	key := CacheKey(ref)

	if key.ID != "" {
		t.Fatalf("CacheKey ID = %q, want empty ID for URL-only ref", key.ID)
	}
}

func TestCacheKeyRejectsCredentialBearingIdentifiers(t *testing.T) {
	unsafeRefs := []twitch.AssetRef{
		{Kind: KindAvatar, ID: "https://cdn.example/avatar.png?access_token=secret"},
		{Kind: "oauth:secret", ID: "user-1"},
		{Kind: KindEmoji, ID: "client_secret=secret"},
	}
	for _, ref := range unsafeRefs {
		if key := CacheKey(ref); key != (storage.AssetKey{}) {
			t.Fatalf("CacheKey(%#v) = %#v, want zero key", ref, key)
		}
	}

	req := Request{
		Ref:       twitch.AssetRef{Kind: KindBadge, ID: "subscriber/12"},
		ChannelID: "https://cdn.example/channel?access_token=secret",
	}
	if key := RequestCacheKey(req); key != (storage.AssetKey{}) {
		t.Fatalf("RequestCacheKey(%#v) = %#v, want zero key", req, key)
	}
}

func TestResolverReportsDownloadFailure(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	ref := twitch.AssetRef{Kind: KindBadge, ID: "moderator/1"}
	resolver := &Resolver{
		Metadata: &fakeMetadataLookup{
			result: Metadata{
				Ref:       ref,
				URL:       "https://cdn.example/badge/mod.png",
				MediaType: "image/png",
			},
		},
		Downloader: &fakeDownloader{err: errDownloadFailure},
		Cache:      cache,
	}

	event := resolver.Resolve(context.Background(), Request{ID: "req-3", Ref: ref})

	if event.Kind != EventFailed {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventFailed)
	}
	if !errors.Is(event.Err, errDownloadFailure) {
		t.Fatalf("event.Err = %v, want %v", event.Err, errDownloadFailure)
	}
	if _, ok, err := cache.GetAsset(context.Background(), CacheKey(ref)); err != nil || ok {
		t.Fatalf("cache result after failed download = ok %v err %v, want miss nil", ok, err)
	}
}

func TestResolverHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	metadata := &fakeMetadataLookup{}
	downloader := &fakeDownloader{}
	resolver := &Resolver{
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      storage.NewMemoryAssetCache(),
	}

	event := resolver.Resolve(ctx, Request{
		ID:  "req-4",
		Ref: twitch.AssetRef{Kind: KindAvatar, ID: "user-1"},
	})

	if event.Kind != EventCanceled {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventCanceled)
	}
	if !errors.Is(event.Err, context.Canceled) {
		t.Fatalf("event.Err = %v, want context.Canceled", event.Err)
	}
	if metadata.calls != 0 {
		t.Fatalf("metadata calls = %d, want 0", metadata.calls)
	}
	if downloader.calls != 0 {
		t.Fatalf("downloader calls = %d, want 0", downloader.calls)
	}
}

func TestResolverUsesIdentityLookupForAvatarMetadata(t *testing.T) {
	ref := twitch.AssetRef{Kind: KindAvatar}
	identity := &fakeIdentityLookup{
		result: Identity{
			UserID:    "user-1",
			Login:     "viewer42",
			AvatarURL: "https://cdn.example/avatar.png",
		},
	}
	metadata := &fakeMetadataLookup{}
	downloader := &fakeDownloader{
		result: DownloadResult{Path: "avatars/user-1.png", MediaType: "image/png"},
	}
	resolver := &Resolver{
		Identity:   identity,
		Metadata:   metadata,
		Downloader: downloader,
		Cache:      storage.NewMemoryAssetCache(),
	}

	event := resolver.Resolve(context.Background(), Request{
		ID:        "req-5",
		Ref:       ref,
		UserLogin: "viewer42",
	})

	if event.Kind != EventDownloaded {
		t.Fatalf("event.Kind = %s, want %s", event.Kind, EventDownloaded)
	}
	if identity.calls != 1 {
		t.Fatalf("identity calls = %d, want 1", identity.calls)
	}
	if metadata.last.Ref.ID != "user-1" {
		t.Fatalf("metadata ref ID = %q, want user-1", metadata.last.Ref.ID)
	}
	if metadata.last.Ref.URL != identity.result.AvatarURL {
		t.Fatalf("metadata ref URL = %q, want %q", metadata.last.Ref.URL, identity.result.AvatarURL)
	}
}

type fakeIdentityLookup struct {
	calls  int
	last   IdentityRequest
	result Identity
	err    error
}

func (f *fakeIdentityLookup) LookupIdentity(_ context.Context, req IdentityRequest) (Identity, error) {
	f.calls++
	f.last = req
	return f.result, f.err
}

type fakeMetadataLookup struct {
	calls  int
	last   MetadataRequest
	result Metadata
	err    error
}

func (f *fakeMetadataLookup) LookupMetadata(_ context.Context, req MetadataRequest) (Metadata, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return Metadata{}, f.err
	}
	result := f.result
	if result.Ref == (twitch.AssetRef{}) {
		result.Ref = req.Ref
	}
	if result.URL == "" {
		result.URL = req.Ref.URL
	}
	return result, nil
}

type fakeDownloader struct {
	calls  int
	last   DownloadRequest
	result DownloadResult
	err    error
}

func (f *fakeDownloader) Download(_ context.Context, req DownloadRequest) (DownloadResult, error) {
	f.calls++
	f.last = req
	return f.result, f.err
}
