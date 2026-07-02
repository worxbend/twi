package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

type Cache struct {
	Root string
}

func NewCache(root string) Cache {
	return Cache{Root: root}
}

func (c Cache) Path(parts ...string) string {
	items := append([]string{c.Root}, parts...)
	return filepath.Join(items...)
}

// DefaultAssetCacheDir returns the platform cache location used for persisted
// asset metadata and bytes.
func DefaultAssetCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "twi", "assets"), nil
}

// AssetKey identifies a cached image or metadata asset without embedding a
// remote URL or any credential-bearing request data.
type AssetKey struct {
	Kind string
	ID   string
}

// AssetRecord describes a cached asset that can be handed to an image
// renderer by an asynchronous caller. Path may be empty for metadata-only
// entries or tests.
type AssetRecord struct {
	Key         AssetKey
	Path        string
	SourceURL   string
	MediaType   string
	WidthCells  int
	HeightCells int
	FetchedAt   time.Time
	ExpiresAt   time.Time
}

// AssetCache is the minimal context-aware cache boundary for image assets.
// Implementations must not require network access for reads or writes.
type AssetCache interface {
	GetAsset(ctx context.Context, key AssetKey) (AssetRecord, bool, error)
	PutAsset(ctx context.Context, record AssetRecord) error
}

var (
	// ErrUnsafeAssetKey reports a cache key that may contain a URL or secret.
	ErrUnsafeAssetKey = errors.New("unsafe asset cache key")
	// ErrUnsafeAssetPath reports an input path that may contain a URL or secret.
	ErrUnsafeAssetPath = errors.New("unsafe asset cache path")
)

// MemoryAssetCache is a deterministic in-memory AssetCache for tests and
// early fallback-only wiring. It performs no file or network I/O.
type MemoryAssetCache struct {
	mu      sync.RWMutex
	records map[AssetKey]AssetRecord
}

var _ AssetCache = (*MemoryAssetCache)(nil)

// NewMemoryAssetCache returns an empty context-aware in-memory asset cache.
func NewMemoryAssetCache() *MemoryAssetCache {
	return &MemoryAssetCache{
		records: make(map[AssetKey]AssetRecord),
	}
}

func (c *MemoryAssetCache) GetAsset(ctx context.Context, key AssetKey) (AssetRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return AssetRecord{}, false, err
	}
	if c == nil {
		return AssetRecord{}, false, nil
	}
	if err := validateAssetKey(key); err != nil {
		return AssetRecord{}, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	record, ok := c.records[key]
	return record, ok, nil
}

func (c *MemoryAssetCache) PutAsset(ctx context.Context, record AssetRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	if err := validateAssetKey(record.Key); err != nil {
		return err
	}
	if record.Path != "" && containsUnsafeCacheText(record.Path) {
		return ErrUnsafeAssetPath
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.records == nil {
		c.records = make(map[AssetKey]AssetRecord)
	}
	c.records[record.Key] = record
	return nil
}

// DiskAssetCache stores asset metadata and optional local bytes on disk. Cache
// paths are derived from a hash of AssetKey so keys never appear in filenames.
type DiskAssetCache struct {
	Root string
}

var _ AssetCache = (*DiskAssetCache)(nil)

const (
	// DefaultAssetCacheMaxAge is the fallback TTL for cache records that do
	// not carry a provider-specific ExpiresAt value.
	DefaultAssetCacheMaxAge = 30 * 24 * time.Hour
	// DefaultAssetCacheMaxBytes is the default on-disk byte budget for cached
	// asset payloads.
	DefaultAssetCacheMaxBytes int64 = 512 * 1024 * 1024
)

// NewDiskAssetCache returns a disk-backed asset cache rooted at root. If root
// is empty, the platform cache directory is used.
func NewDiskAssetCache(root string) *DiskAssetCache {
	return &DiskAssetCache{Root: root}
}

func (c *DiskAssetCache) GetAsset(ctx context.Context, key AssetKey) (AssetRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return AssetRecord{}, false, err
	}
	if c == nil {
		return AssetRecord{}, false, nil
	}
	paths, err := c.paths(key)
	if err != nil {
		return AssetRecord{}, false, err
	}

	data, err := readFileContext(ctx, paths.metadata)
	if errors.Is(err, os.ErrNotExist) {
		return AssetRecord{}, false, nil
	}
	if err != nil {
		return AssetRecord{}, false, err
	}

	var metadata diskAssetMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return AssetRecord{}, false, nil
	}
	if metadata.Version != diskAssetMetadataVersion || metadata.Key != key {
		return AssetRecord{}, false, nil
	}

	record := AssetRecord{
		Key:         key,
		SourceURL:   metadata.SourceURL,
		MediaType:   metadata.MediaType,
		WidthCells:  metadata.WidthCells,
		HeightCells: metadata.HeightCells,
		FetchedAt:   metadata.FetchedAt,
		ExpiresAt:   metadata.ExpiresAt,
	}
	if metadata.HasData {
		info, err := os.Stat(paths.data)
		if errors.Is(err, os.ErrNotExist) {
			return AssetRecord{}, false, nil
		}
		if err != nil {
			return AssetRecord{}, false, err
		}
		if info.IsDir() {
			return AssetRecord{}, false, fmt.Errorf("%s: asset data path is a directory", paths.data)
		}
		record.Path = paths.data
	}

	if err := ctx.Err(); err != nil {
		return AssetRecord{}, false, err
	}
	return record, true, nil
}

func (c *DiskAssetCache) PutAsset(ctx context.Context, record AssetRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	if record.Path != "" && containsUnsafeCacheText(record.Path) {
		return ErrUnsafeAssetPath
	}
	paths, err := c.paths(record.Key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.dir, 0o700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	hasData := record.Path != ""
	if hasData {
		if err := copyFileAtomicContext(ctx, record.Path, paths.data); err != nil {
			return err
		}
	}

	metadata := diskAssetMetadata{
		Version:     diskAssetMetadataVersion,
		Key:         record.Key,
		HasData:     hasData,
		SourceURL:   record.SourceURL,
		MediaType:   record.MediaType,
		WidthCells:  record.WidthCells,
		HeightCells: record.HeightCells,
		FetchedAt:   record.FetchedAt,
		ExpiresAt:   record.ExpiresAt,
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomicContext(ctx, paths.metadata, data)
}

// Prune removes expired records and then removes the oldest remaining records
// until the cache is within the configured byte budget. Size accounting covers
// cache-owned asset payload bytes; metadata-only records count as zero bytes.
func (c *DiskAssetCache) Prune(ctx context.Context, opts PruneOptions) (PruneReport, error) {
	if err := ctx.Err(); err != nil {
		return PruneReport{}, err
	}
	if c == nil {
		return PruneReport{}, nil
	}
	opts = opts.normalized()
	root, err := c.root()
	report := PruneReport{Root: root}
	if err != nil {
		return report, err
	}

	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, fmt.Errorf("%s: cache root is not a directory", root)
	}

	entries, err := c.pruneEntries(ctx, root)
	if err != nil {
		return report, err
	}
	report.EntriesScanned = len(entries)
	for _, entry := range entries {
		report.BytesBefore += entry.dataBytes
	}

	prune := make(map[string]pruneReason)
	bytesAfterExpiration := report.BytesBefore
	for _, entry := range entries {
		if entry.expired(opts) {
			prune[entry.dir] = pruneExpired
			bytesAfterExpiration -= entry.dataBytes
		}
	}

	if opts.MaxBytes >= 0 && bytesAfterExpiration > opts.MaxBytes {
		remaining := make([]pruneEntry, 0, len(entries))
		for _, entry := range entries {
			if _, ok := prune[entry.dir]; !ok {
				remaining = append(remaining, entry)
			}
		}
		slices.SortFunc(remaining, func(a, b pruneEntry) int {
			if cmp := a.fetchedAt.Compare(b.fetchedAt); cmp != 0 {
				return cmp
			}
			return strings.Compare(a.dir, b.dir)
		})
		for _, entry := range remaining {
			if bytesAfterExpiration <= opts.MaxBytes {
				break
			}
			prune[entry.dir] = pruneSize
			bytesAfterExpiration -= entry.dataBytes
		}
	}

	for _, entry := range entries {
		reason, ok := prune[entry.dir]
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			report.BytesAfter = report.BytesBefore - report.BytesPruned
			return report, err
		}
		if err := os.RemoveAll(entry.dir); err != nil {
			report.BytesAfter = report.BytesBefore - report.BytesPruned
			return report, fmt.Errorf("remove cached asset: %w", err)
		}
		report.EntriesPruned++
		report.BytesPruned += entry.dataBytes
		switch reason {
		case pruneExpired:
			report.ExpiredPruned++
		case pruneSize:
			report.SizePruned++
		}
	}
	report.BytesAfter = report.BytesBefore - report.BytesPruned
	return report, nil
}

type PruneOptions struct {
	Now      time.Time
	MaxAge   time.Duration
	MaxBytes int64
}

type PruneReport struct {
	Root           string
	EntriesScanned int
	EntriesPruned  int
	ExpiredPruned  int
	SizePruned     int
	BytesBefore    int64
	BytesPruned    int64
	BytesAfter     int64
}

type diskCachePaths struct {
	dir      string
	metadata string
	data     string
}

const diskAssetMetadataVersion = 1

type diskAssetMetadata struct {
	Version     int       `json:"version"`
	Key         AssetKey  `json:"key"`
	HasData     bool      `json:"has_data"`
	SourceURL   string    `json:"source_url,omitempty"`
	MediaType   string    `json:"media_type,omitempty"`
	WidthCells  int       `json:"width_cells,omitempty"`
	HeightCells int       `json:"height_cells,omitempty"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type pruneEntry struct {
	dir           string
	dataBytes     int64
	fetchedAt     time.Time
	expiresAt     time.Time
	metadataValid bool
}

type pruneReason int

const (
	pruneExpired pruneReason = iota + 1
	pruneSize
)

func (o PruneOptions) normalized() PruneOptions {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.MaxAge == 0 {
		o.MaxAge = DefaultAssetCacheMaxAge
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = DefaultAssetCacheMaxBytes
	}
	return o
}

func (c *DiskAssetCache) pruneEntries(ctx context.Context, root string) ([]pruneEntry, error) {
	entriesByDir := make(map[string]pruneEntry)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		switch filepath.Base(path) {
		case "metadata.json":
			metadataEntry, ok, err := c.pruneEntryFromMetadata(ctx, path)
			if err != nil {
				return err
			}
			if ok {
				entriesByDir[metadataEntry.dir] = metadataEntry
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			addOrphanPruneEntry(entriesByDir, filepath.Dir(path), 0, info.ModTime())
		case "asset.bin":
			info, err := entry.Info()
			if err != nil {
				return err
			}
			addOrphanPruneEntry(entriesByDir, filepath.Dir(path), info.Size(), info.ModTime())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	entries := make([]pruneEntry, 0, len(entriesByDir))
	for _, entry := range entriesByDir {
		entries = append(entries, entry)
	}
	return entries, ctx.Err()
}

func (c *DiskAssetCache) pruneEntryFromMetadata(ctx context.Context, path string) (pruneEntry, bool, error) {
	data, err := readFileContext(ctx, path)
	if err != nil {
		return pruneEntry{}, false, err
	}
	var metadata diskAssetMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return pruneEntry{}, false, nil
	}
	if metadata.Version != diskAssetMetadataVersion {
		return pruneEntry{}, false, nil
	}
	paths, err := c.paths(metadata.Key)
	if err != nil {
		return pruneEntry{}, false, nil
	}
	if filepath.Clean(paths.metadata) != filepath.Clean(path) {
		return pruneEntry{}, false, nil
	}

	var dataBytes int64
	if metadata.HasData {
		info, err := os.Stat(paths.data)
		if errors.Is(err, os.ErrNotExist) {
			dataBytes = 0
		} else if err != nil {
			return pruneEntry{}, false, err
		} else if !info.IsDir() {
			dataBytes = info.Size()
		}
	}
	return pruneEntry{
		dir:           paths.dir,
		dataBytes:     dataBytes,
		fetchedAt:     metadata.FetchedAt,
		expiresAt:     metadata.ExpiresAt,
		metadataValid: true,
	}, true, nil
}

func addOrphanPruneEntry(entries map[string]pruneEntry, dir string, dataBytes int64, modTime time.Time) {
	entry := entries[dir]
	if entry.dir == "" {
		entry.dir = dir
	}
	if dataBytes > entry.dataBytes {
		entry.dataBytes = dataBytes
	}
	if !entry.metadataValid && (entry.fetchedAt.IsZero() || modTime.Before(entry.fetchedAt)) {
		entry.fetchedAt = modTime
	}
	entries[dir] = entry
}

func (e pruneEntry) expired(opts PruneOptions) bool {
	if !e.expiresAt.IsZero() {
		return !e.expiresAt.After(opts.Now)
	}
	if opts.MaxAge > 0 && !e.fetchedAt.IsZero() && e.fetchedAt.Add(opts.MaxAge).Before(opts.Now) {
		return true
	}
	return false
}

func (c *DiskAssetCache) paths(key AssetKey) (diskCachePaths, error) {
	if err := validateAssetKey(key); err != nil {
		return diskCachePaths{}, err
	}
	root, err := c.root()
	if err != nil {
		return diskCachePaths{}, err
	}
	sum := sha256.Sum256([]byte(key.Kind + "\x00" + key.ID))
	digest := hex.EncodeToString(sum[:])
	dir := filepath.Join(root, sanitizePathPart(key.Kind), digest[:2], digest)
	return diskCachePaths{
		dir:      dir,
		metadata: filepath.Join(dir, "metadata.json"),
		data:     filepath.Join(dir, "asset.bin"),
	}, nil
}

func (c *DiskAssetCache) root() (string, error) {
	if c.Root != "" {
		return c.Root, nil
	}
	return DefaultAssetCacheDir()
}

func validateAssetKey(key AssetKey) error {
	if strings.TrimSpace(key.Kind) == "" || strings.TrimSpace(key.ID) == "" {
		return ErrUnsafeAssetKey
	}
	if containsUnsafeCacheText(key.Kind) || containsUnsafeCacheText(key.ID) {
		return ErrUnsafeAssetKey
	}
	return nil
}

func sanitizePathPart(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "asset"
	}
	return b.String()
}

func containsUnsafeCacheText(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") {
		return true
	}
	markers := []string{
		"oauth:",
		"oauth_token=",
		"access_token=",
		"refresh_token=",
		"client_secret=",
		"client-secret=",
		"authorization=",
		"authorization: bearer",
		"bearer ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if errors.Is(readErr, io.EOF) {
			return []byte(b.String()), nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func copyFileAtomicContext(ctx context.Context, srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".asset-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := copyContext(ctx, tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameReplace(tmpPath, dstPath); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

func writeFileAtomicContext(ctx context.Context, path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeContext(ctx, tmp, data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameReplace(tmpPath, path); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if err := writeContext(ctx, dst, buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func writeContext(ctx context.Context, dst io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := dst.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return ctx.Err()
}

func renameReplace(oldPath, newPath string) error {
	if runtime.GOOS == "windows" {
		_ = os.Remove(newPath)
	}
	return os.Rename(oldPath, newPath)
}
