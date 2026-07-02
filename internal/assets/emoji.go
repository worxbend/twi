package assets

import (
	"github.com/w0rxbend/twi/internal/emoji"
	"github.com/w0rxbend/twi/internal/storage"
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

// IsEmojiCluster reports whether cluster is a standard emoji grapheme cluster.
func IsEmojiCluster(cluster string) bool {
	return emoji.IsCluster(cluster)
}
