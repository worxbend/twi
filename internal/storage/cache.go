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
	MediaType   string    `json:"media_type,omitempty"`
	WidthCells  int       `json:"width_cells,omitempty"`
	HeightCells int       `json:"height_cells,omitempty"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
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
