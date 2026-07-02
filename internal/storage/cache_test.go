package storage

import (
	"context"
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
		Key:         AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:        "emotes/25.png",
		MediaType:   "image/png",
		WidthCells:  6,
		HeightCells: 1,
		FetchedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
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

func TestDiskAssetCachePersistsAcrossInstances(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	source := filepath.Join(t.TempDir(), "avatar.png")
	content := []byte("image bytes")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}

	record := AssetRecord{
		Key:         AssetKey{Kind: "avatar", ID: "user-1"},
		Path:        source,
		MediaType:   "image/png",
		WidthCells:  4,
		HeightCells: 2,
		FetchedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		ExpiresAt:   time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
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
	if got.MediaType != record.MediaType || got.WidthCells != record.WidthCells || got.HeightCells != record.HeightCells {
		t.Fatalf("metadata = %#v, want %#v", got, record)
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
	if runtime.GOOS == "windows" {
		t.Skip("chmod write denial is not portable on Windows")
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

func TestDiskAssetCacheKeepsPathsAndMetadataCredentialSafe(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(source, []byte("public image bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile asset returned error: %v", err)
	}
	key := AssetKey{Kind: "badge", ID: "channel-1/subscriber/12"}
	cache := NewDiskAssetCache(root)

	if err := cache.PutAsset(context.Background(), AssetRecord{Key: key, Path: source, MediaType: "image/png"}); err != nil {
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
		for _, unsafe := range []string{key.ID, "access_token", "client_secret", "oauth:"} {
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
	if cacheBytes := readCacheFixtureBytes(t, root); cacheBytes != "" {
		t.Fatalf("cache wrote files for rejected records: %q", cacheBytes)
	}
}

func TestDefaultAssetCacheDirUsesPlatformCacheDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_CACHE_HOME does not define UserCacheDir on Windows")
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
