package app

import (
	"testing"

	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/config"
)

func TestDefaultLiveImageStackRequestsEmojiAndEmoteImagesWhenCapable(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.OAuthToken = "oauth:token"

	decision := DecideLiveImageStack(cfg, []string{
		"TERM=xterm-kitty",
		"COLORTERM=truecolor",
		"KITTY_WINDOW_ID=42",
	}, t.TempDir())

	if !decision.Ready {
		t.Fatalf("image stack ready = false; status=%s detail=%q", decision.Status, decision.Detail)
	}
	for _, kind := range []string{assets.KindTwitchEmote, assets.KindEmoji} {
		if !decision.Supports(kind) {
			t.Fatalf("default image stack does not support %q; supported=%#v detail=%q", kind, decision.SupportedKinds, decision.Detail)
		}
	}
}

func TestLiveImageStackSupportsFragmentEmotesWithoutTwitchAPICredentials(t *testing.T) {
	cfg := config.Default()

	decision := DecideLiveImageStack(cfg, []string{
		"TERM=xterm-kitty",
		"COLORTERM=truecolor",
		"KITTY_WINDOW_ID=42",
	}, t.TempDir())

	if !decision.Ready {
		t.Fatalf("image stack ready = false; status=%s detail=%q", decision.Status, decision.Detail)
	}
	if !decision.Supports(assets.KindTwitchEmote) {
		t.Fatalf("image stack does not support emotes without API credentials; supported=%#v detail=%q", decision.SupportedKinds, decision.Detail)
	}
	if decision.Supports(assets.KindAvatar) || decision.Supports(assets.KindBadge) {
		t.Fatalf("image stack supports API-backed Twitch assets without credentials; supported=%#v", decision.SupportedKinds)
	}
}
