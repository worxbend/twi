package assets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

func TestEmojiAssetIDFixtures(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		want    string
	}{
		{name: "single", cluster: "😀", want: "1f600"},
		{name: "modifier", cluster: "👍🏽", want: "1f44d-1f3fd"},
		{name: "variation selector normalized", cluster: "☕️", want: "2615"},
		{name: "zwj profession", cluster: "👩‍💻", want: "1f469-200d-1f4bb"},
		{name: "zwj family", cluster: "👨‍👩‍👧‍👦", want: "1f468-200d-1f469-200d-1f467-200d-1f466"},
		{name: "regional flag", cluster: "🇺🇸", want: "1f1fa-1f1f8"},
		{name: "keycap", cluster: "1️⃣", want: "31-20e3"},
		{name: "symbol variation", cluster: "♥️", want: "2665"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EmojiAssetID(tt.cluster)
			if !ok {
				t.Fatalf("EmojiAssetID(%q) ok = false, want true", tt.cluster)
			}
			if got != tt.want {
				t.Fatalf("EmojiAssetID(%q) = %q, want %q", tt.cluster, got, tt.want)
			}
			normalized, ok := NormalizeEmojiAssetID(strings.ToUpper(tt.want))
			if !ok {
				t.Fatalf("NormalizeEmojiAssetID(%q) ok = false, want true", strings.ToUpper(tt.want))
			}
			if normalized != tt.want {
				t.Fatalf("NormalizeEmojiAssetID(%q) = %q, want %q", strings.ToUpper(tt.want), normalized, tt.want)
			}
			key, ok := EmojiAssetKey(tt.cluster)
			if !ok {
				t.Fatalf("EmojiAssetKey(%q) ok = false, want true", tt.cluster)
			}
			if key != (storage.AssetKey{Kind: KindEmoji, ID: tt.want}) {
				t.Fatalf("EmojiAssetKey(%q) = %#v, want emoji key %q", tt.cluster, key, tt.want)
			}
		})
	}
}

func TestEmojiAssetIDRejectsNonEmojiClusters(t *testing.T) {
	for _, cluster := range []string{"A", "@", "hello", "\ufe0f", "🏽", "😀‍", "😀😀", "🇺"} {
		if got, ok := EmojiAssetID(cluster); ok {
			t.Fatalf("EmojiAssetID(%q) = %q, true; want false", cluster, got)
		}
	}
}

func TestEmojiCacheKeyNormalizesRawUnicodeRef(t *testing.T) {
	key := CacheKey(twitch.AssetRef{Kind: KindEmoji, ID: "👍🏽"})

	if key != (storage.AssetKey{Kind: KindEmoji, ID: "1f44d-1f3fd"}) {
		t.Fatalf("CacheKey raw emoji = %#v, want normalized emoji key", key)
	}

	key = CacheKey(twitch.AssetRef{Kind: KindEmoji, ID: "1F44D-FE0F-1F3FD"})
	if key != (storage.AssetKey{Kind: KindEmoji, ID: "1f44d-1f3fd"}) {
		t.Fatalf("CacheKey existing emoji ID = %#v, want normalized emoji key", key)
	}
}

func TestEmojiMetadataProviderResolvesRepresentativeMetadataAndCaches(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cache := storage.NewMemoryAssetCache()
	provider := NewEmojiMetadataProvider(EmojiProviderConfig{
		Provider:    "custom",
		URLTemplate: "https://emoji.example/assets/{id}.png",
		Cache:       cache,
		Now:         func() time.Time { return now },
		TTL:         time.Hour,
	})

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "flag", ref: "🇺🇸", want: "1f1fa-1f1f8"},
		{name: "modifier key", ref: "1f44d-1f3fd", want: "1f44d-1f3fd"},
		{name: "zwj", ref: "👩‍💻", want: "1f469-200d-1f4bb"},
		{name: "keycap key", ref: "31-fe0f-20e3", want: "31-20e3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, err := provider.LookupMetadata(context.Background(), MetadataRequest{
				Ref: twitch.AssetRef{Kind: KindEmoji, ID: tt.ref},
			})
			if err != nil {
				t.Fatalf("LookupMetadata returned error: %v", err)
			}
			if got, want := metadata.Ref.ID, tt.want; got != want {
				t.Fatalf("metadata ref ID = %q, want %q", got, want)
			}
			if got, want := metadata.URL, "https://emoji.example/assets/"+tt.want+".png"; got != want {
				t.Fatalf("metadata URL = %q, want %q", got, want)
			}
			if metadata.MediaType != "image/png" || metadata.WidthCells != 2 || metadata.HeightCells != 1 {
				t.Fatalf("metadata shape = %#v, want png 2x1", metadata)
			}
			if !metadata.ExpiresAt.Equal(now.Add(time.Hour)) {
				t.Fatalf("metadata ExpiresAt = %s, want %s", metadata.ExpiresAt, now.Add(time.Hour))
			}
			record, ok, err := cache.GetAsset(context.Background(), storage.AssetKey{Kind: KindEmoji, ID: tt.want})
			if err != nil || !ok {
				t.Fatalf("cached metadata ok=%v err=%v, want hit nil", ok, err)
			}
			if record.Key.ID != tt.want || record.SourceURL != metadata.URL || record.Path != "" {
				t.Fatalf("cached metadata record = %#v, want URL-free key and metadata-only source URL", record)
			}
		})
	}
}

func TestEmojiMetadataProviderDefaultTwemojiTemplate(t *testing.T) {
	metadata, err := NewEmojiMetadataProvider(EmojiProviderConfig{}).LookupMetadata(context.Background(), MetadataRequest{
		Ref: twitch.AssetRef{Kind: KindEmoji, ID: "😀"},
	})
	if err != nil {
		t.Fatalf("LookupMetadata returned error: %v", err)
	}
	if got, want := metadata.URL, "https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.3/assets/72x72/1f600.png"; got != want {
		t.Fatalf("metadata URL = %q, want %q", got, want)
	}
	if metadata.MediaType != "image/png" {
		t.Fatalf("metadata MediaType = %q, want image/png", metadata.MediaType)
	}
}

func TestEmojiMetadataProviderUsesCacheHitWithoutTemplateValidation(t *testing.T) {
	cache := storage.NewMemoryAssetCache()
	key := storage.AssetKey{Kind: KindEmoji, ID: "1f600"}
	record := storage.AssetRecord{
		Key:         key,
		SourceURL:   "https://emoji.example/cached/1f600.png",
		MediaType:   "image/png",
		WidthCells:  2,
		HeightCells: 1,
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := cache.PutAsset(context.Background(), record); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	provider := NewEmojiMetadataProvider(EmojiProviderConfig{
		Provider: "custom",
		Cache:    cache,
	})

	metadata, err := provider.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindEmoji, ID: "😀"}})
	if err != nil {
		t.Fatalf("LookupMetadata returned error: %v", err)
	}
	if metadata.URL != record.SourceURL {
		t.Fatalf("metadata URL = %q, want cached %q", metadata.URL, record.SourceURL)
	}
}

func TestEmojiMetadataProviderIgnoresUnsafeCachedSourceURL(t *testing.T) {
	key := storage.AssetKey{Kind: KindEmoji, ID: "1f600"}
	cache := &unsafeSourceURLCache{
		key: key,
		record: storage.AssetRecord{
			Key:       key,
			SourceURL: "https://emoji.example/1f600.png?access_token=secret",
			MediaType: "image/png",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	provider := NewEmojiMetadataProvider(EmojiProviderConfig{
		Provider:    "custom",
		URLTemplate: "https://emoji.example/public/{id}.png",
		Cache:       cache,
	})

	metadata, err := provider.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindEmoji, ID: "😀"}})
	if err != nil {
		t.Fatalf("LookupMetadata returned error: %v", err)
	}
	if got, want := metadata.URL, "https://emoji.example/public/1f600.png"; got != want {
		t.Fatalf("metadata URL = %q, want safe provider URL %q", got, want)
	}
	if cache.put.SourceURL != metadata.URL {
		t.Fatalf("cached replacement SourceURL = %q, want %q", cache.put.SourceURL, metadata.URL)
	}
}

func TestEmojiMetadataProviderMissAndMalformedConfiguration(t *testing.T) {
	provider := NewEmojiMetadataProvider(EmojiProviderConfig{
		Provider:    "custom",
		URLTemplate: "https://emoji.example/assets/{id}.png",
	})
	miss, err := provider.LookupMetadata(context.Background(), MetadataRequest{Ref: twitch.AssetRef{Kind: KindEmoji, ID: "not emoji"}})
	if err != nil {
		t.Fatalf("LookupMetadata miss returned error: %v", err)
	}
	if miss.URL != "" {
		t.Fatalf("miss URL = %q, want empty fallback metadata", miss.URL)
	}

	for _, tt := range []struct {
		name     string
		provider EmojiProviderConfig
	}{
		{name: "custom missing template", provider: EmojiProviderConfig{Provider: "custom"}},
		{name: "missing placeholder", provider: EmojiProviderConfig{Provider: "custom", URLTemplate: "https://emoji.example/assets/static.png"}},
		{name: "non public scheme", provider: EmojiProviderConfig{Provider: "custom", URLTemplate: "file:///emoji/{id}.png"}},
		{name: "url userinfo", provider: EmojiProviderConfig{Provider: "custom", URLTemplate: "https://user:secret@emoji.example/{id}.png"}},
		{name: "credential marker", provider: EmojiProviderConfig{Provider: "custom", URLTemplate: "https://emoji.example/{id}.png?access_token=secret"}},
		{name: "unknown provider", provider: EmojiProviderConfig{Provider: "surprise"}},
		{name: "unknown provider with template", provider: EmojiProviderConfig{Provider: "surprise", URLTemplate: "https://emoji.example/{id}.png"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEmojiMetadataProvider(tt.provider).LookupMetadata(context.Background(), MetadataRequest{
				Ref: twitch.AssetRef{Kind: KindEmoji, ID: "😀"},
			})
			if !errors.Is(err, ErrInvalidEmojiProviderConfig) {
				t.Fatalf("LookupMetadata error = %v, want ErrInvalidEmojiProviderConfig", err)
			}
			for _, unsafe := range []string{"access_token=secret", "client_secret", "oauth:", "user:secret"} {
				if err != nil && strings.Contains(err.Error(), unsafe) {
					t.Fatalf("provider error leaked unsafe text %q in %q", unsafe, err)
				}
			}
		})
	}
}

func TestEmojiMetadataProviderDiskCachePathsStayCredentialSafe(t *testing.T) {
	root := t.TempDir()
	provider := NewEmojiMetadataProvider(EmojiProviderConfig{
		Provider:    "custom",
		URLTemplate: "https://emoji.example/assets/{id}.png?variant=public",
		Cache:       storage.NewDiskAssetCache(root),
	})
	metadata, err := provider.LookupMetadata(context.Background(), MetadataRequest{
		Ref: twitch.AssetRef{Kind: KindEmoji, ID: "👨‍👩‍👧‍👦"},
	})
	if err != nil {
		t.Fatalf("LookupMetadata returned error: %v", err)
	}
	if metadata.URL == "" || strings.Contains(metadata.URL, "access_token") {
		t.Fatalf("metadata URL = %q, want public source URL", metadata.URL)
	}

	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatalf("WalkDir returned error: %v", err)
	}
	for _, path := range paths {
		for _, unsafe := range []string{
			"1f468-200d-1f469-200d-1f467-200d-1f466",
			"emoji.example",
			"access_token",
			"client_secret",
			"oauth:",
		} {
			if strings.Contains(path, unsafe) {
				t.Fatalf("cache path %q contains unsafe text %q", path, unsafe)
			}
		}
	}
}

type unsafeSourceURLCache struct {
	key    storage.AssetKey
	record storage.AssetRecord
	put    storage.AssetRecord
}

func (c *unsafeSourceURLCache) GetAsset(ctx context.Context, key storage.AssetKey) (storage.AssetRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return storage.AssetRecord{}, false, err
	}
	if c == nil || key != c.key {
		return storage.AssetRecord{}, false, nil
	}
	return c.record, true, nil
}

func (c *unsafeSourceURLCache) PutAsset(ctx context.Context, record storage.AssetRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c != nil {
		c.put = record
	}
	return nil
}
