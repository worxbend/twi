package assets

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	DefaultEmojiProvider      = "twemoji"
	DefaultEmojiURLTemplate   = "https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.3/assets/72x72/{id}.png"
	defaultEmojiMetadataTTL   = 30 * 24 * time.Hour
	defaultEmojiWidthCells    = 2
	defaultEmojiHeightCells   = 1
	emojiTemplatePlaceholder  = "{id}"
	emojiTemplatePlaceholder2 = "{{id}}"
)

var ErrInvalidEmojiProviderConfig = errors.New("invalid emoji provider configuration")

// EmojiProviderConfig configures standard emoji metadata resolution. The
// provider returns public image URLs only; downloading remains a separate
// asynchronous resolver step.
type EmojiProviderConfig struct {
	Provider    string
	URLTemplate string
	MediaType   string
	WidthCells  int
	HeightCells int
	Cache       storage.AssetCache
	Now         func() time.Time
	TTL         time.Duration
}

// EmojiMetadataProvider maps standard emoji asset IDs to public image metadata
// and caches metadata-only records under URL-free emoji keys.
type EmojiMetadataProvider struct {
	config EmojiProviderConfig
}

var _ MetadataLookup = (*EmojiMetadataProvider)(nil)

// NewEmojiMetadataProvider creates a configurable standard emoji metadata
// provider. Empty provider settings default to the pinned Twemoji PNG template.
func NewEmojiMetadataProvider(config EmojiProviderConfig) *EmojiMetadataProvider {
	return &EmojiMetadataProvider{config: config}
}

// ValidateEmojiProviderConfig checks provider settings without touching the
// cache or network. It is intended for startup gating before async asset work
// is installed.
func ValidateEmojiProviderConfig(config EmojiProviderConfig) error {
	provider := NewEmojiMetadataProvider(config)
	template, err := provider.urlTemplate()
	if err != nil {
		return err
	}
	sourceURL := strings.ReplaceAll(template, emojiTemplatePlaceholder2, "1f600")
	sourceURL = strings.ReplaceAll(sourceURL, emojiTemplatePlaceholder, "1f600")
	return validateEmojiSourceURL(sourceURL)
}

// LookupMetadata resolves standard emoji refs. Unknown emoji refs return
// metadata without a URL so callers can keep native Unicode fallback text.
func (p *EmojiMetadataProvider) LookupMetadata(ctx context.Context, req MetadataRequest) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}

	ref := normalizeRef(req.Ref)
	if ref.Kind != KindEmoji {
		return Metadata{Ref: ref, URL: ref.URL}, nil
	}
	id, ok := NormalizeEmojiAssetID(ref.ID)
	if !ok {
		return Metadata{Ref: ref}, nil
	}
	ref.ID = id

	if metadata, ok, err := p.cachedMetadata(ctx, ref); err != nil || ok {
		return metadata, err
	}

	sourceURL, err := p.sourceURL(id)
	if err != nil {
		return Metadata{}, err
	}
	metadata := Metadata{
		Ref:         twitch.AssetRef{Kind: KindEmoji, ID: id, URL: sourceURL},
		URL:         sourceURL,
		MediaType:   p.mediaType(sourceURL),
		WidthCells:  p.widthCells(),
		HeightCells: p.heightCells(),
		ExpiresAt:   p.now().Add(p.ttl()),
	}
	if err := p.putMetadata(ctx, metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, ctx.Err()
}

func (p *EmojiMetadataProvider) cachedMetadata(ctx context.Context, ref twitch.AssetRef) (Metadata, bool, error) {
	if p == nil || p.config.Cache == nil || ref.ID == "" {
		return Metadata{}, false, nil
	}
	key := storage.AssetKey{Kind: KindEmoji, ID: ref.ID}
	record, ok, err := p.config.Cache.GetAsset(ctx, key)
	if err != nil || !ok {
		return Metadata{}, false, err
	}
	if !cacheRecordFresh(record, p.now()) || strings.TrimSpace(record.SourceURL) == "" {
		return Metadata{}, false, nil
	}
	if err := validateEmojiSourceURL(record.SourceURL); err != nil {
		return Metadata{}, false, nil
	}
	return Metadata{
		Ref:         twitch.AssetRef{Kind: KindEmoji, ID: ref.ID, URL: record.SourceURL},
		URL:         record.SourceURL,
		MediaType:   record.MediaType,
		WidthCells:  record.WidthCells,
		HeightCells: record.HeightCells,
		ExpiresAt:   record.ExpiresAt,
	}, true, nil
}

func (p *EmojiMetadataProvider) putMetadata(ctx context.Context, metadata Metadata) error {
	if p == nil || p.config.Cache == nil || metadata.Ref.ID == "" || metadata.URL == "" {
		return nil
	}
	now := p.now()
	return p.config.Cache.PutAsset(ctx, storage.AssetRecord{
		Key:         storage.AssetKey{Kind: KindEmoji, ID: metadata.Ref.ID},
		SourceURL:   metadata.URL,
		MediaType:   metadata.MediaType,
		WidthCells:  metadata.WidthCells,
		HeightCells: metadata.HeightCells,
		FetchedAt:   now,
		ExpiresAt:   metadata.ExpiresAt,
	})
}

func (p *EmojiMetadataProvider) sourceURL(id string) (string, error) {
	template, err := p.urlTemplate()
	if err != nil {
		return "", err
	}
	sourceURL := strings.ReplaceAll(template, emojiTemplatePlaceholder2, id)
	sourceURL = strings.ReplaceAll(sourceURL, emojiTemplatePlaceholder, id)
	if err := validateEmojiSourceURL(sourceURL); err != nil {
		return "", err
	}
	return sourceURL, nil
}

func (p *EmojiMetadataProvider) urlTemplate() (string, error) {
	provider := DefaultEmojiProvider
	template := ""
	if p != nil {
		provider = strings.ToLower(strings.TrimSpace(p.config.Provider))
		template = strings.TrimSpace(p.config.URLTemplate)
	}
	if provider == "" {
		provider = DefaultEmojiProvider
	}
	switch provider {
	case DefaultEmojiProvider:
		if template == "" {
			template = DefaultEmojiURLTemplate
		}
	case "custom":
		if template == "" {
			return "", fmt.Errorf("%w: custom emoji provider requires a URL template", ErrInvalidEmojiProviderConfig)
		}
	default:
		return "", fmt.Errorf("%w: unknown emoji provider", ErrInvalidEmojiProviderConfig)
	}
	if !strings.Contains(template, emojiTemplatePlaceholder) && !strings.Contains(template, emojiTemplatePlaceholder2) {
		return "", fmt.Errorf("%w: emoji URL template must include {id}", ErrInvalidEmojiProviderConfig)
	}
	return template, nil
}

func validateEmojiSourceURL(sourceURL string) error {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return fmt.Errorf("%w: emoji source URL is malformed", ErrInvalidEmojiProviderConfig)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%w: emoji source URL must be HTTP or HTTPS", ErrInvalidEmojiProviderConfig)
	}
	if parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%w: emoji source URL must be public", ErrInvalidEmojiProviderConfig)
	}
	if containsCredentialMarker(sourceURL) {
		return fmt.Errorf("%w: emoji source URL must not contain credentials", ErrInvalidEmojiProviderConfig)
	}
	return nil
}

func (p *EmojiMetadataProvider) mediaType(sourceURL string) string {
	if p != nil && strings.TrimSpace(p.config.MediaType) != "" {
		return strings.TrimSpace(p.config.MediaType)
	}
	return mediaTypeForURL(sourceURL, "image/png")
}

func (p *EmojiMetadataProvider) widthCells() int {
	if p != nil && p.config.WidthCells > 0 {
		return p.config.WidthCells
	}
	return defaultEmojiWidthCells
}

func (p *EmojiMetadataProvider) heightCells() int {
	if p != nil && p.config.HeightCells > 0 {
		return p.config.HeightCells
	}
	return defaultEmojiHeightCells
}

func (p *EmojiMetadataProvider) now() time.Time {
	if p != nil && p.config.Now != nil {
		return p.config.Now()
	}
	return time.Now()
}

func (p *EmojiMetadataProvider) ttl() time.Duration {
	if p != nil && p.config.TTL > 0 {
		return p.config.TTL
	}
	return defaultEmojiMetadataTTL
}

func containsCredentialMarker(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"oauth:",
		"oauth_token=",
		"access_token=",
		"refresh_token=",
		"token=",
		"_token=",
		"-token=",
		"client_secret=",
		"client-secret=",
		"secret=",
		"_secret=",
		"-secret=",
		"authorization=",
		"authorization: bearer",
		"auth=",
		"bearer ",
		"bearer%20",
		"cookie=",
		"set-cookie=",
		"password=",
		"passwd=",
		"session=",
		"api_key=",
		"apikey=",
		"device_code=",
		"authorization_code=",
		"code_verifier=",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
