package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMemoryAssetCacheStoresRecordsWithoutNetwork(t *testing.T) {
	cache := NewMemoryAssetCache()
	record := AssetRecord{
		Key:             AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:            "emotes/25.png",
		PayloadIdentity: "sha256:" + strings.Repeat("1", 64),
		MediaType:       "image/png",
		WidthCells:      6,
		HeightCells:     1,
		FetchedAt:       time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
	}

	if err := cache.PutAsset(context.Background(), record); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	got, ok, err := cache.GetAsset(context.Background(), record.Key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if !ok {
		t.Fatal("GetAsset ok = false, want true")
	}
	if got != record {
		t.Fatalf("record = %#v, want %#v", got, record)
	}
}

func TestMemoryAssetCacheHonorsContextCancellation(t *testing.T) {
	cache := NewMemoryAssetCache()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	key := AssetKey{Kind: "avatar", ID: "user-1"}
	if _, _, err := cache.GetAsset(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAsset error = %v, want context.Canceled", err)
	}
	if err := cache.PutAsset(ctx, AssetRecord{Key: key}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutAsset error = %v, want context.Canceled", err)
	}
}

func TestMemoryAssetCacheRejectsCredentialBearingKeysAndPaths(t *testing.T) {
	cache := NewMemoryAssetCache()
	unsafeKey := AssetKey{Kind: "avatar", ID: "https://cdn.example/avatar.png?access_token=secret"}
	if err := cache.PutAsset(context.Background(), AssetRecord{Key: unsafeKey}); !errors.Is(err, ErrUnsafeAssetKey) {
		t.Fatalf("PutAsset unsafe key error = %v, want ErrUnsafeAssetKey", err)
	}
	if _, _, err := cache.GetAsset(context.Background(), unsafeKey); !errors.Is(err, ErrUnsafeAssetKey) {
		t.Fatalf("GetAsset unsafe key error = %v, want ErrUnsafeAssetKey", err)
	}

	err := cache.PutAsset(context.Background(), AssetRecord{
		Key:  AssetKey{Kind: "emoji", ID: "1f600"},
		Path: filepath.Join(t.TempDir(), "asset-client_secret=secret.png"),
	})
	if !errors.Is(err, ErrUnsafeAssetPath) {
		t.Fatalf("PutAsset unsafe path error = %v, want ErrUnsafeAssetPath", err)
	}

	err = cache.PutAsset(context.Background(), AssetRecord{
		Key:       AssetKey{Kind: "emoji", ID: "1f600"},
		SourceURL: "https://cdn.example/emoji.png?access_token=secret",
	})
	if !errors.Is(err, ErrUnsafeAssetSourceURL) {
		t.Fatalf("PutAsset unsafe source URL error = %v, want ErrUnsafeAssetSourceURL", err)
	}

	err = cache.PutAsset(context.Background(), AssetRecord{
		Key:             AssetKey{Kind: "emoji", ID: "1f600"},
		PayloadIdentity: "https://cdn.example/emoji.png?access_token=secret",
	})
	if !errors.Is(err, ErrUnsafeAssetPayloadIdentity) {
		t.Fatalf("PutAsset unsafe payload identity error = %v, want ErrUnsafeAssetPayloadIdentity", err)
	}
}

func TestDiskAssetCachePersistsAcrossInstances(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	source := filepath.Join(t.TempDir(), "avatar.png")
	content := []byte("image bytes")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}

	record := AssetRecord{
		Key:             AssetKey{Kind: "avatar", ID: "user-1"},
		Path:            source,
		SourceURL:       "https://static-cdn.example/avatar/user-1.png",
		PayloadIdentity: "sha256:" + strings.Repeat("a", 64),
		MediaType:       "image/png",
		WidthCells:      4,
		HeightCells:     2,
		FetchedAt:       time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		ExpiresAt:       time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}

	if err := NewDiskAssetCache(root).PutAsset(context.Background(), record); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	got, ok, err := NewDiskAssetCache(root).GetAsset(context.Background(), record.Key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if !ok {
		t.Fatal("GetAsset ok = false, want true")
	}
	if got.Key != record.Key {
		t.Fatalf("key = %#v, want %#v", got.Key, record.Key)
	}
	if got.Path == "" || got.Path == source {
		t.Fatalf("cached path = %q, want cache-owned data path", got.Path)
	}
	if got.SourceURL != record.SourceURL || got.PayloadIdentity != record.PayloadIdentity || got.MediaType != record.MediaType || got.WidthCells != record.WidthCells || got.HeightCells != record.HeightCells {
		t.Fatalf("metadata = %#v, want %#v", got, record)
	}
	if strings.Contains(got.Path, record.PayloadIdentity) || strings.Contains(filepath.Dir(got.Path), record.PayloadIdentity) {
		t.Fatalf("cache path exposed payload identity %q in %q", record.PayloadIdentity, got.Path)
	}
	if !got.FetchedAt.Equal(record.FetchedAt) || !got.ExpiresAt.Equal(record.ExpiresAt) {
		t.Fatalf("times = %s/%s, want %s/%s", got.FetchedAt, got.ExpiresAt, record.FetchedAt, record.ExpiresAt)
	}
	gotContent, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("ReadFile cached data returned error: %v", err)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("cached bytes = %q, want %q", gotContent, content)
	}
}

func TestDiskAssetCacheMissesUnknownKey(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	_, ok, err := cache.GetAsset(context.Background(), AssetKey{Kind: "emoji", ID: "😀"})
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if ok {
		t.Fatal("GetAsset ok = true, want false")
	}
}

func TestDiskAssetCacheTreatsCorruptMetadataAsMiss(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	key := AssetKey{Kind: "twitch_emote", ID: "25"}
	paths, err := cache.paths(key)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(paths.metadata, []byte("{not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt metadata returned error: %v", err)
	}

	_, ok, err := cache.GetAsset(context.Background(), key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if ok {
		t.Fatal("GetAsset ok = true, want false for corrupt metadata")
	}
}

func TestDiskAssetCacheTreatsUnsafeSourceURLMetadataAsMiss(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	key := AssetKey{Kind: "avatar", ID: "user-1"}
	paths, err := cache.paths(key)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	metadata := diskAssetMetadata{
		Version:   diskAssetMetadataVersion,
		Key:       key,
		SourceURL: "https://cdn.example/avatar.png?access_token=secret",
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if err := os.WriteFile(paths.metadata, data, 0o600); err != nil {
		t.Fatalf("WriteFile unsafe metadata returned error: %v", err)
	}

	_, ok, err := cache.GetAsset(context.Background(), key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if ok {
		t.Fatal("GetAsset ok = true, want miss for unsafe source URL")
	}
}

func TestDiskAssetCacheTreatsUnsafePayloadIdentityMetadataAsMiss(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	key := AssetKey{Kind: "avatar", ID: "user-1"}
	paths, err := cache.paths(key)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	metadata := diskAssetMetadata{
		Version:         diskAssetMetadataVersion,
		Key:             key,
		PayloadIdentity: "oauth:secret-token",
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if err := os.WriteFile(paths.metadata, data, 0o600); err != nil {
		t.Fatalf("WriteFile unsafe metadata returned error: %v", err)
	}

	_, ok, err := cache.GetAsset(context.Background(), key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if ok {
		t.Fatal("GetAsset ok = true, want miss for unsafe payload identity")
	}
}

func TestDiskAssetCacheReportsFilesystemFailures(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache-root-file")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}
	source := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile asset returned error: %v", err)
	}

	cache := NewDiskAssetCache(root)
	key := AssetKey{Kind: "badge", ID: "channel/subscriber/12"}
	if err := cache.PutAsset(context.Background(), AssetRecord{Key: key, Path: source}); err == nil {
		t.Fatal("PutAsset returned nil, want filesystem error")
	}
	if _, _, err := cache.GetAsset(context.Background(), key); err == nil {
		t.Fatal("GetAsset returned nil error, want filesystem error")
	}
}

func TestDiskAssetCacheReportsPermissionFailures(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("chmod write-denial checks are Unix-specific")
	}
	root := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir fixture returned error: %v", err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("Chmod fixture returned error: %v", err)
	}
	defer func() {
		_ = os.Chmod(root, 0o700)
	}()
	source := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile asset returned error: %v", err)
	}

	err := NewDiskAssetCache(root).PutAsset(context.Background(), AssetRecord{
		Key:  AssetKey{Kind: "avatar", ID: "user-1"},
		Path: source,
	})
	if err == nil {
		t.Skip("cache root remained writable; permission failure not observable")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("PutAsset error = %v, want permission error", err)
	}
}

func TestDiskAssetCacheHonorsContextCancellation(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	key := AssetKey{Kind: "avatar", ID: "user-1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := cache.GetAsset(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAsset error = %v, want context.Canceled", err)
	}
	if err := cache.PutAsset(ctx, AssetRecord{Key: key}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutAsset error = %v, want context.Canceled", err)
	}
}

func TestDiskAssetCacheHonorsCancellationBeforePublishingMetadata(t *testing.T) {
	dir := t.TempDir()
	ctx := &cancelAfterErrContext{remaining: 2}

	err := writeFileAtomicContext(ctx, filepath.Join(dir, "metadata.json"), []byte("{}\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("writeFileAtomicContext error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata stat error = %v, want os.ErrNotExist", err)
	}
}

func TestDiskAssetCachePrunesExpiredRecordsDeterministically(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	expired := AssetKey{Kind: "avatar", ID: "expired"}
	fresh := AssetKey{Kind: "avatar", ID: "fresh"}
	providerFresh := AssetKey{Kind: "avatar", ID: "provider-fresh"}

	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       expired,
		Path:      writeAssetFixture(t, "expired"),
		FetchedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Second),
	})
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       fresh,
		Path:      writeAssetFixture(t, "fresh"),
		FetchedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(time.Hour),
	})
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       providerFresh,
		Path:      writeAssetFixture(t, "provider-fresh"),
		FetchedAt: now.Add(-90 * 24 * time.Hour),
		ExpiresAt: now.Add(time.Hour),
	})

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: -1,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.EntriesScanned != 3 || report.EntriesPruned != 1 || report.ExpiredPruned != 1 || report.SizePruned != 0 {
		t.Fatalf("prune report = %#v, want one expired prune", report)
	}
	if _, ok, err := cache.GetAsset(context.Background(), expired); err != nil || ok {
		t.Fatalf("expired GetAsset ok=%v err=%v, want miss nil", ok, err)
	}
	if _, ok, err := cache.GetAsset(context.Background(), fresh); err != nil || !ok {
		t.Fatalf("fresh GetAsset ok=%v err=%v, want hit nil", ok, err)
	}
	if _, ok, err := cache.GetAsset(context.Background(), providerFresh); err != nil || !ok {
		t.Fatalf("providerFresh GetAsset ok=%v err=%v, want hit nil", ok, err)
	}
}

func TestDiskAssetCachePrunesOldRecordsByMaxAge(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := AssetKey{Kind: "emoji", ID: "old"}
	recent := AssetKey{Kind: "emoji", ID: "recent"}

	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       old,
		Path:      writeAssetFixture(t, "old"),
		FetchedAt: now.Add(-49 * time.Hour),
	})
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       recent,
		Path:      writeAssetFixture(t, "recent"),
		FetchedAt: now.Add(-47 * time.Hour),
	})

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   48 * time.Hour,
		MaxBytes: -1,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.EntriesPruned != 1 || report.ExpiredPruned != 1 {
		t.Fatalf("prune report = %#v, want one max-age prune", report)
	}
	if _, ok, err := cache.GetAsset(context.Background(), old); err != nil || ok {
		t.Fatalf("old GetAsset ok=%v err=%v, want miss nil", ok, err)
	}
	if _, ok, err := cache.GetAsset(context.Background(), recent); err != nil || !ok {
		t.Fatalf("recent GetAsset ok=%v err=%v, want hit nil", ok, err)
	}
}

func TestDiskAssetCachePrunesOldestRecordsBySize(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	oldest := AssetKey{Kind: "badge", ID: "oldest"}
	middle := AssetKey{Kind: "badge", ID: "middle"}
	newest := AssetKey{Kind: "badge", ID: "newest"}

	putDiskCacheFixture(t, cache, AssetRecord{Key: oldest, Path: writeAssetFixture(t, "aaaa"), FetchedAt: now.Add(-3 * time.Hour)})
	putDiskCacheFixture(t, cache, AssetRecord{Key: middle, Path: writeAssetFixture(t, "bbbb"), FetchedAt: now.Add(-2 * time.Hour)})
	putDiskCacheFixture(t, cache, AssetRecord{Key: newest, Path: writeAssetFixture(t, "cccc"), FetchedAt: now.Add(-1 * time.Hour)})

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: 8,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.BytesBefore != 12 || report.BytesAfter != 8 || report.SizePruned != 1 || report.EntriesPruned != 1 {
		t.Fatalf("prune report = %#v, want one size prune from 12 to 8 bytes", report)
	}
	if _, ok, err := cache.GetAsset(context.Background(), oldest); err != nil || ok {
		t.Fatalf("oldest GetAsset ok=%v err=%v, want miss nil", ok, err)
	}
	for _, key := range []AssetKey{middle, newest} {
		if _, ok, err := cache.GetAsset(context.Background(), key); err != nil || !ok {
			t.Fatalf("%#v GetAsset ok=%v err=%v, want hit nil", key, ok, err)
		}
	}
}

func TestDiskAssetCachePrunesCacheOwnedDownloadArtifactsWithoutDeletingUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	stale := writeCacheOwnedArtifactFixture(t, root, "downloads", "asset-stale.bin", "stale")
	kept := writeCacheOwnedArtifactFixture(t, root, "downloads", "notes.bin", "keep")
	old := now.Add(-49 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("Chtimes stale download returned error: %v", err)
	}
	if err := os.Chtimes(kept, old, old); err != nil {
		t.Fatalf("Chtimes unrelated download returned error: %v", err)
	}

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   48 * time.Hour,
		MaxBytes: -1,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.EntriesScanned != 1 || report.EntriesPruned != 1 || report.ExpiredPruned != 1 || report.BytesPruned != 5 {
		t.Fatalf("prune report = %#v, want one stale download artifact pruned", report)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale download stat error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("unrelated download stat error = %v, want kept file", err)
	}
}

func TestDiskAssetCachePrunesPreparedRendererArtifactsBySizeWithoutDeletingUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	oldPrepared := writeCacheOwnedArtifactFixture(t, root, "prepared", "prepared-old.png", "aaaaaaaa")
	newPrepared := writeCacheOwnedArtifactFixture(t, root, "prepared", "prepared-new.png", "bbbbbbbb")
	kept := writeCacheOwnedArtifactFixture(t, root, "prepared", "prepared-note.txt", "cccccccc")
	old := now.Add(-2 * time.Hour)
	newer := now.Add(-time.Hour)
	if err := os.Chtimes(oldPrepared, old, old); err != nil {
		t.Fatalf("Chtimes old prepared returned error: %v", err)
	}
	if err := os.Chtimes(newPrepared, newer, newer); err != nil {
		t.Fatalf("Chtimes new prepared returned error: %v", err)
	}
	if err := os.Chtimes(kept, old, old); err != nil {
		t.Fatalf("Chtimes unrelated prepared returned error: %v", err)
	}

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: 8,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.BytesBefore != 16 || report.BytesAfter != 8 || report.SizePruned != 1 || report.EntriesPruned != 1 {
		t.Fatalf("prune report = %#v, want one old prepared artifact pruned by size", report)
	}
	if _, err := os.Stat(oldPrepared); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old prepared stat error = %v, want os.ErrNotExist", err)
	}
	for _, path := range []string{newPrepared, kept} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept artifact %q stat error = %v", path, err)
		}
	}
}

func TestDiskAssetCachePrunesPreparedTemporaryArtifactsWithoutDeletingUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	stale := writeCacheOwnedArtifactFixture(t, root, filepath.Join("emoji", "aa", strings.Repeat("a", 64)), ".prepared-cache-stale.tmp", "stale-prepared")
	kept := writeCacheOwnedArtifactFixture(t, root, filepath.Join("emoji", "aa", strings.Repeat("a", 64)), ".custom-cache-stale.tmp", "keep-prepared")
	old := now.Add(-49 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("Chtimes stale prepared temp returned error: %v", err)
	}
	if err := os.Chtimes(kept, old, old); err != nil {
		t.Fatalf("Chtimes unrelated temp returned error: %v", err)
	}

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   48 * time.Hour,
		MaxBytes: -1,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.EntriesScanned != 1 || report.EntriesPruned != 1 || report.ExpiredPruned != 1 {
		t.Fatalf("prune report = %#v, want one stale prepared temp pruned", report)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale prepared temp stat error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("unrelated temp stat error = %v, want kept file", err)
	}
}

func TestDiskAssetCachePrunesExpiredMetadataOnlyRecords(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	expired := AssetKey{Kind: "emoji_metadata", ID: "1f600"}
	fresh := AssetKey{Kind: "emoji_metadata", ID: "1f601"}

	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       expired,
		FetchedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Second),
	})
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       fresh,
		FetchedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(time.Hour),
	})

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: -1,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.BytesBefore != 0 || report.BytesAfter != 0 || report.EntriesPruned != 1 || report.ExpiredPruned != 1 {
		t.Fatalf("prune report = %#v, want one zero-byte metadata-only prune", report)
	}
	if _, ok, err := cache.GetAsset(context.Background(), expired); err != nil || ok {
		t.Fatalf("expired metadata GetAsset ok=%v err=%v, want miss nil", ok, err)
	}
	if _, ok, err := cache.GetAsset(context.Background(), fresh); err != nil || !ok {
		t.Fatalf("fresh metadata GetAsset ok=%v err=%v, want hit nil", ok, err)
	}
}

func TestDiskAssetCacheCountsCorruptPayloadsDuringSizePruning(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	corruptKey := AssetKey{Kind: "avatar", ID: "corrupt"}
	freshKey := AssetKey{Kind: "avatar", ID: "fresh"}

	corruptPaths, err := cache.paths(corruptKey)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	if err := os.MkdirAll(corruptPaths.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll corrupt fixture returned error: %v", err)
	}
	if err := os.WriteFile(corruptPaths.metadata, []byte("{not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt metadata returned error: %v", err)
	}
	if err := os.WriteFile(corruptPaths.data, []byte("xxxx"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt payload returned error: %v", err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(corruptPaths.metadata, old, old); err != nil {
		t.Fatalf("Chtimes corrupt metadata returned error: %v", err)
	}
	if err := os.Chtimes(corruptPaths.data, old, old); err != nil {
		t.Fatalf("Chtimes corrupt payload returned error: %v", err)
	}
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       freshKey,
		Path:      writeAssetFixture(t, "yyyy"),
		FetchedAt: now.Add(-time.Hour),
	})

	report, err := cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: 4,
	})
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if report.BytesBefore != 8 || report.BytesAfter != 4 || report.SizePruned != 1 {
		t.Fatalf("prune report = %#v, want corrupt payload counted and pruned by size", report)
	}
	if _, err := os.Stat(corruptPaths.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt cache dir stat error = %v, want os.ErrNotExist", err)
	}
	if _, ok, err := cache.GetAsset(context.Background(), freshKey); err != nil || !ok {
		t.Fatalf("fresh GetAsset ok=%v err=%v, want hit nil", ok, err)
	}
}

func TestDiskAssetCachePruneHonorsContextCancellation(t *testing.T) {
	cache := NewDiskAssetCache(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := cache.Prune(ctx, PruneOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prune error = %v, want context.Canceled", err)
	}
}

func TestDiskAssetCachePruneReportsCleanupFailures(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("chmod cleanup-denial checks are Unix-specific")
	}
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	key := AssetKey{Kind: "avatar", ID: "permission-check"}
	putDiskCacheFixture(t, cache, AssetRecord{
		Key:       key,
		Path:      writeAssetFixture(t, "data"),
		FetchedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(-time.Second),
	})
	paths, err := cache.paths(key)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	parent := filepath.Dir(paths.dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("Chmod fixture returned error: %v", err)
	}
	defer func() {
		_ = os.Chmod(parent, 0o700)
	}()

	_, err = cache.Prune(context.Background(), PruneOptions{
		Now:      now,
		MaxAge:   -1,
		MaxBytes: -1,
	})
	if err == nil {
		t.Skip("cache entry remained removable; cleanup failure not observable")
	}
	if strings.Contains(err.Error(), key.ID) {
		t.Fatalf("cleanup error leaked cache key %q in %q", key.ID, err)
	}
}

func TestDiskAssetCacheKeepsPathsAndMetadataCredentialSafe(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(source, []byte("public image bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile asset returned error: %v", err)
	}
	key := AssetKey{Kind: "badge", ID: "channel-1/subscriber/12"}
	cache := NewDiskAssetCache(root)

	payloadIdentity := "sha256:" + strings.Repeat("b", 64)
	if err := cache.PutAsset(context.Background(), AssetRecord{Key: key, Path: source, PayloadIdentity: payloadIdentity, MediaType: "image/png"}); err != nil {
		t.Fatalf("PutAsset returned error: %v", err)
	}
	got, ok, err := cache.GetAsset(context.Background(), key)
	if err != nil {
		t.Fatalf("GetAsset returned error: %v", err)
	}
	if !ok {
		t.Fatal("GetAsset ok = false, want true")
	}
	for _, path := range []string{got.Path, filepath.Dir(got.Path)} {
		for _, unsafe := range []string{key.ID, payloadIdentity, "access_token", "client_secret", "oauth:"} {
			if strings.Contains(path, unsafe) {
				t.Fatalf("cache path %q contains unsafe text %q", path, unsafe)
			}
		}
	}
	cacheBytes := readCacheFixtureBytes(t, root)
	for _, unsafe := range []string{
		"https://cdn.example/asset.png?access_token=secret",
		"oauth:secret-token",
		"client_secret=secret",
	} {
		if strings.Contains(cacheBytes, unsafe) {
			t.Fatalf("cache files leaked unsafe text %q in %q", unsafe, cacheBytes)
		}
	}
}

func TestDiskAssetCacheSanitizesKindWithoutEscapingRoot(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	key := AssetKey{Kind: "..", ID: "escape-attempt"}

	paths, err := cache.paths(key)
	if err != nil {
		t.Fatalf("paths returned error: %v", err)
	}
	relative, err := filepath.Rel(root, paths.dir)
	if err != nil {
		t.Fatalf("Rel returned error: %v", err)
	}
	if relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || relative == ".." || filepath.IsAbs(relative) {
		t.Fatalf("cache dir %q escaped root %q", paths.dir, root)
	}
	if strings.Contains(relative, "..") {
		t.Fatalf("cache dir relative path %q contains unsafe dot traversal", relative)
	}
}

func TestDiskAssetCacheRejectsCredentialBearingKeysAndPaths(t *testing.T) {
	root := t.TempDir()
	cache := NewDiskAssetCache(root)
	source := filepath.Join(t.TempDir(), "asset-access_token=secret.bin")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile asset returned error: %v", err)
	}

	unsafeKeys := []AssetKey{
		{Kind: "avatar", ID: "https://cdn.example/avatar.png?access_token=secret"},
		{Kind: "oauth:secret", ID: "user-1"},
		{Kind: "emoji", ID: "client_secret=secret"},
	}
	for _, key := range unsafeKeys {
		if err := cache.PutAsset(context.Background(), AssetRecord{Key: key}); !errors.Is(err, ErrUnsafeAssetKey) {
			t.Fatalf("PutAsset(%#v) error = %v, want ErrUnsafeAssetKey", key, err)
		}
		if _, _, err := cache.GetAsset(context.Background(), key); !errors.Is(err, ErrUnsafeAssetKey) {
			t.Fatalf("GetAsset(%#v) error = %v, want ErrUnsafeAssetKey", key, err)
		}
	}

	err := cache.PutAsset(context.Background(), AssetRecord{
		Key:  AssetKey{Kind: "avatar", ID: "user-1"},
		Path: source,
	})
	if !errors.Is(err, ErrUnsafeAssetPath) {
		t.Fatalf("PutAsset unsafe path error = %v, want ErrUnsafeAssetPath", err)
	}
	err = cache.PutAsset(context.Background(), AssetRecord{
		Key:       AssetKey{Kind: "avatar", ID: "user-1"},
		SourceURL: "https://cdn.example/avatar.png?client_secret=secret",
	})
	if !errors.Is(err, ErrUnsafeAssetSourceURL) {
		t.Fatalf("PutAsset unsafe source URL error = %v, want ErrUnsafeAssetSourceURL", err)
	}
	err = cache.PutAsset(context.Background(), AssetRecord{
		Key:             AssetKey{Kind: "avatar", ID: "user-1"},
		PayloadIdentity: "asset.png",
	})
	if !errors.Is(err, ErrUnsafeAssetPayloadIdentity) {
		t.Fatalf("PutAsset unsafe payload identity error = %v, want ErrUnsafeAssetPayloadIdentity", err)
	}
	if cacheBytes := readCacheFixtureBytes(t, root); cacheBytes != "" {
		t.Fatalf("cache wrote files for rejected records: %q", cacheBytes)
	}
}

func TestDefaultAssetCacheDirUsesPlatformCacheDir(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("XDG cache-dir checks are Unix-specific")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	got, err := DefaultAssetCacheDir()
	if err != nil {
		t.Fatalf("DefaultAssetCacheDir returned error: %v", err)
	}
	want := filepath.Join(dir, "twi", "assets")
	if got != want {
		t.Fatalf("DefaultAssetCacheDir = %q, want %q", got, want)
	}
}

func TestCheckReadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("ok\n"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}

	if err := CheckReadableFile(path); err != nil {
		t.Fatalf("CheckReadableFile returned error: %v", err)
	}
	if err := CheckReadableFile(dir); !errors.Is(err, ErrPathIsDirectory) {
		t.Fatalf("CheckReadableFile directory error = %v, want ErrPathIsDirectory", err)
	}
}

func readCacheFixtureBytes(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString(path)
		b.WriteByte('\n')
		b.Write(data)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir returned error: %v", err)
	}
	return b.String()
}

func putDiskCacheFixture(t *testing.T, cache *DiskAssetCache, record AssetRecord) {
	t.Helper()
	if err := cache.PutAsset(context.Background(), record); err != nil {
		t.Fatalf("PutAsset fixture returned error: %v", err)
	}
}

func writeAssetFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}
	return path
}

func writeCacheOwnedArtifactFixture(t *testing.T, root, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(root, dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll artifact fixture returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile artifact fixture returned error: %v", err)
	}
	return path
}

type cancelAfterErrContext struct {
	context.Context
	remaining int
}

func (c *cancelAfterErrContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *cancelAfterErrContext) Done() <-chan struct{} {
	return nil
}

func (c *cancelAfterErrContext) Err() error {
	if c.remaining > 0 {
		c.remaining--
		return nil
	}
	return context.Canceled
}

func (c *cancelAfterErrContext) Value(key any) any {
	return nil
}

func TestProbeWritableDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")

	if err := ProbeWritableDir(dir); err != nil {
		t.Fatalf("ProbeWritableDir returned error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left entries behind: %#v", entries)
	}
}
