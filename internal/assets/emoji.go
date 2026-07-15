package assets

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/worxbend/twi/internal/emoji"
	"github.com/worxbend/twi/internal/storage"
)

// EmojiAssetKey maps a standard emoji grapheme cluster to a provider-neutral
// cache key. The key ID is a lowercase hyphen-separated codepoint sequence with
// emoji presentation selectors removed, so native fallback text can vary while
// image providers can resolve a stable asset identity.
func EmojiAssetKey(cluster string) (storage.AssetKey, bool) {
	id, ok := EmojiAssetID(cluster)
	if !ok {
		return storage.AssetKey{}, false
	}
	return storage.AssetKey{Kind: KindEmoji, ID: id}, true
}

// EmojiAssetID returns the provider-neutral ID for a standard emoji grapheme
// cluster. It preserves modifiers, ZWJ links, regional indicators, and keycap
// marks while normalizing away text/image variation selectors.
func EmojiAssetID(cluster string) (string, bool) {
	return emoji.AssetID(cluster)
}

// NormalizeEmojiAssetID accepts either a native emoji grapheme cluster or an
// existing provider-neutral asset ID and returns the canonical lowercase asset
// ID used by caches and image providers.
func NormalizeEmojiAssetID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if id, ok := EmojiAssetID(value); ok {
		return id, true
	}
	runes, ok := emojiAssetIDRunes(value)
	if !ok {
		return "", false
	}
	return EmojiAssetID(string(runes))
}

// IsEmojiCluster reports whether cluster is a standard emoji grapheme cluster.
func IsEmojiCluster(cluster string) bool {
	return emoji.IsCluster(cluster)
}

func emojiAssetIDRunes(id string) ([]rune, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(id)), "-")
	if len(parts) == 0 {
		return nil, false
	}
	runes := make([]rune, 0, len(parts))
	for _, part := range parts {
		if part == "" || len(part) > 6 {
			return nil, false
		}
		value, err := strconv.ParseInt(part, 16, 32)
		if err != nil || value < 0 || value > utf8.MaxRune {
			return nil, false
		}
		runes = append(runes, rune(value))
	}
	return runes, true
}
