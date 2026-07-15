package assets

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

func TestAvatarCacheKeyRejectsCredentialBearingIdentifiers(t *testing.T) {
	if key := AvatarCacheKey(AvatarRequest{UserID: "https://cdn.example/avatar.png?access_token=secret"}); key.ID != "" {
		t.Fatalf("unsafe user ID key = %#v, want empty ID", key)
	}
	if key := AvatarCacheKey(AvatarRequest{UserLogin: "client_secret=secret"}); key.ID != "" {
		t.Fatalf("unsafe user login key = %#v, want empty ID", key)
	}
	key := AvatarCacheKey(AvatarRequest{
		UserID:    "https://cdn.example/avatar.png?access_token=secret",
		UserLogin: "Viewer",
	})
	if got, want := key, (storage.AssetKey{Kind: KindAvatar, ID: "login/viewer"}); got != want {
		t.Fatalf("safe fallback login key = %#v, want %#v", got, want)
	}
}

func TestAvatarBatchResolverBatchesUniqueAuthorsAndCachesMetadata(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	lookup := &fakeAvatarLookup{
		users: []twitch.UserIdentity{
			{
				UserID:          "42",
				Login:           "viewer",
				DisplayName:     "Viewer",
				ProfileImageURL: "https://static-cdn.example/viewer.png",
			},
			{
				UserID:          "99",
				Login:           "mod",
				DisplayName:     "Mod",
				ProfileImageURL: "https://static-cdn.example/mod.png",
			},
		},
	}
	resolver := &AvatarBatchResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
		TTL:    time.Hour,
	}

	results, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{
		{UserID: "42", UserLogin: "viewer", DisplayName: "Viewer"},
		{UserID: "42", UserLogin: "viewer", DisplayName: "Viewer"},
		{UserLogin: "Mod", DisplayName: "Mod"},
	})
	if err != nil {
		t.Fatalf("ResolveAvatars error = %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup calls = %d, want 1", lookup.calls)
	}
	if !reflect.DeepEqual(lookup.last.UserIDs, []string{"42"}) {
		t.Fatalf("lookup IDs = %#v, want 42 once", lookup.last.UserIDs)
	}
	if !reflect.DeepEqual(lookup.last.UserLogins, []string{"Mod"}) {
		t.Fatalf("lookup logins = %#v, want Mod once", lookup.last.UserLogins)
	}
	if len(results) != 2 {
		t.Fatalf("results length = %d, want 2: %#v", len(results), results)
	}
	for _, result := range results {
		if !result.Found || result.AvatarURL == "" {
			t.Fatalf("result = %#v, want found avatar URL", result)
		}
	}

	record, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindAvatar, ID: "42"})
	if err != nil || !ok {
		t.Fatalf("cached user-id avatar ok=%v err=%v, want hit nil", ok, err)
	}
	if record.SourceURL != "https://static-cdn.example/viewer.png" {
		t.Fatalf("cached SourceURL = %q, want viewer URL", record.SourceURL)
	}
	if !record.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("cached ExpiresAt = %s, want %s", record.ExpiresAt, now.Add(time.Hour))
	}
	if _, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindAvatar, ID: "login/viewer"}); err != nil || !ok {
		t.Fatalf("cached login avatar ok=%v err=%v, want hit nil", ok, err)
	}
}

func TestAvatarBatchResolverReturnsCacheHitsWithoutLookup(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	if err := cache.PutAsset(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: KindAvatar, ID: "42"},
		SourceURL: "https://static-cdn.example/cached.png",
		FetchedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	lookup := &fakeAvatarLookup{}
	resolver := &AvatarBatchResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
	}

	results, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{{UserID: "42", UserLogin: "viewer"}})
	if err != nil {
		t.Fatalf("ResolveAvatars error = %v", err)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup calls = %d, want 0 for cache hit", lookup.calls)
	}
	if len(results) != 1 || !results[0].FromCache || results[0].AvatarURL != "https://static-cdn.example/cached.png" {
		t.Fatalf("results = %#v, want one cache hit", results)
	}
}

func TestAvatarBatchResolverRefreshesExpiredCache(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	if err := cache.PutAsset(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: KindAvatar, ID: "42"},
		SourceURL: "https://static-cdn.example/old.png",
		ExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	lookup := &fakeAvatarLookup{users: []twitch.UserIdentity{{
		UserID:          "42",
		Login:           "viewer",
		ProfileImageURL: "https://static-cdn.example/new.png",
	}}}
	resolver := &AvatarBatchResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
	}

	results, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{{UserID: "42", UserLogin: "viewer"}})
	if err != nil {
		t.Fatalf("ResolveAvatars error = %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup calls = %d, want refresh", lookup.calls)
	}
	if len(results) != 1 || results[0].AvatarURL != "https://static-cdn.example/new.png" || results[0].FromCache {
		t.Fatalf("results = %#v, want refreshed network metadata", results)
	}
}

func TestAvatarBatchResolverPreservesDownloadedAvatarCacheData(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	if err := cache.PutAsset(context.Background(), storage.AssetRecord{
		Key:             storage.AssetKey{Kind: KindAvatar, ID: "42"},
		Path:            "cache/avatar-42.png",
		SourceURL:       "https://static-cdn.example/viewer.png",
		PayloadIdentity: "sha256:0000000000000000000000000000000000000000000000000000000000000042",
		MediaType:       "image/png",
		WidthCells:      4,
		HeightCells:     2,
		FetchedAt:       now.Add(-2 * time.Hour),
		ExpiresAt:       now.Add(-time.Second),
	}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	lookup := &fakeAvatarLookup{users: []twitch.UserIdentity{{
		UserID:          "42",
		Login:           "viewer",
		ProfileImageURL: "https://static-cdn.example/viewer.png",
	}}}
	resolver := &AvatarBatchResolver{
		Lookup: lookup,
		Cache:  cache,
		Now:    func() time.Time { return now },
		TTL:    time.Hour,
	}

	results, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{{UserID: "42", UserLogin: "viewer"}})
	if err != nil {
		t.Fatalf("ResolveAvatars error = %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup calls = %d, want refresh", lookup.calls)
	}
	if len(results) != 1 || results[0].AvatarURL != "https://static-cdn.example/viewer.png" || results[0].FromCache {
		t.Fatalf("results = %#v, want refreshed network metadata", results)
	}
	record, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindAvatar, ID: "42"})
	if err != nil || !ok {
		t.Fatalf("cached avatar ok=%v err=%v, want hit nil", ok, err)
	}
	if got, want := record.Path, "cache/avatar-42.png"; got != want {
		t.Fatalf("cached Path = %q, want preserved downloaded path %q", got, want)
	}
	if got, want := record.PayloadIdentity, "sha256:0000000000000000000000000000000000000000000000000000000000000042"; got != want {
		t.Fatalf("cached PayloadIdentity = %q, want %q", got, want)
	}
	if record.WidthCells != 4 || record.HeightCells != 2 {
		t.Fatalf("cached dimensions = %dx%d, want 4x2", record.WidthCells, record.HeightCells)
	}
	if !record.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("cached ExpiresAt = %s, want refreshed %s", record.ExpiresAt, now.Add(time.Hour))
	}
}

func TestAvatarBatchResolverReportsMissingUsers(t *testing.T) {
	lookup := &fakeAvatarLookup{users: []twitch.UserIdentity{{
		UserID:          "42",
		Login:           "viewer",
		ProfileImageURL: "https://static-cdn.example/viewer.png",
	}}}
	resolver := &AvatarBatchResolver{Lookup: lookup}

	results, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{
		{UserLogin: "viewer"},
		{UserLogin: "missing"},
	})
	if err != nil {
		t.Fatalf("ResolveAvatars error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results length = %d, want 2: %#v", len(results), results)
	}
	if !results[0].Found || results[1].Found {
		t.Fatalf("results = %#v, want found viewer and missing user result", results)
	}
}

func TestAvatarBatchResolverReturnsAPIFailureAndRateLimitLikeErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "api failure", err: errors.New("Twitch Get Users returned HTTP 500")},
		{name: "rate limit", err: errors.New("Twitch Get Users returned HTTP 429: rate limit exceeded")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &AvatarBatchResolver{Lookup: &fakeAvatarLookup{err: tt.err}}
			_, err := resolver.ResolveAvatars(context.Background(), []AvatarRequest{{UserLogin: "viewer"}})
			if !errors.Is(err, tt.err) {
				t.Fatalf("ResolveAvatars error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestAvatarBatchResolverHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lookup := &fakeAvatarLookup{}
	resolver := &AvatarBatchResolver{
		Lookup: lookup,
		Cache:  storage.NewMemoryAssetCache(),
	}

	_, err := resolver.ResolveAvatars(ctx, []AvatarRequest{{UserID: "42"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveAvatars error = %v, want context.Canceled", err)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup calls = %d, want 0 after canceled context", lookup.calls)
	}
}

type fakeAvatarLookup struct {
	calls int
	last  twitch.UserLookupRequest
	users []twitch.UserIdentity
	err   error
}

func (f *fakeAvatarLookup) GetUsers(_ context.Context, req twitch.UserLookupRequest) ([]twitch.UserIdentity, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return f.users, nil
}
