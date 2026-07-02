package assets

import (
	"testing"

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
}
