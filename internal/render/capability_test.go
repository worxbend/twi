package render

import (
	"strings"
	"testing"

	"github.com/w0rxbend/twi/internal/config"
)

func TestDecideImageCapabilitiesEnvironmentMatrix(t *testing.T) {
	imageFeatures := config.Default().Features
	imageFeatures.AvatarMode = "image"
	imageFeatures.EmojiMode = "image"
	imageFeatures.EmoteMode = "image"

	for _, tt := range []struct {
		name          string
		features      config.FeatureConfig
		environ       []string
		cacheWritable bool
		wantStatus    ImageCapabilityStatus
		wantActive    bool
		wantDetail    string
	}{
		{
			name:          "kitty auto enabled",
			features:      imageFeatures,
			environ:       []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheWritable: true,
			wantStatus:    ImageCapabilityEnabled,
			wantActive:    true,
			wantDetail:    "enabled",
		},
		{
			name:          "ghostty auto enabled",
			features:      imageFeatures,
			environ:       []string{"TERM=xterm-256color", "TERM_PROGRAM=ghostty", "COLORTERM=truecolor"},
			cacheWritable: true,
			wantStatus:    ImageCapabilityEnabled,
			wantActive:    true,
			wantDetail:    "terminal=ghostty",
		},
		{
			name:          "non kitty auto unsupported",
			features:      imageFeatures,
			environ:       []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			cacheWritable: true,
			wantStatus:    ImageCapabilityUnsupported,
			wantActive:    false,
			wantDetail:    "unsupported",
		},
		{
			name:          "kitty cache unwritable degraded",
			features:      imageFeatures,
			environ:       []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheWritable: false,
			wantStatus:    ImageCapabilityDegraded,
			wantActive:    true,
			wantDetail:    "cache=unwritable",
		},
		{
			name: "explicit override degraded but active",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "normal"
				return features
			}(),
			environ:       []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			cacheWritable: true,
			wantStatus:    ImageCapabilityDegraded,
			wantActive:    true,
			wantDetail:    "no Kitty/Ghostty graphics signal",
		},
		{
			name: "image off disabled",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			environ:       []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheWritable: true,
			wantStatus:    ImageCapabilityDisabled,
			wantActive:    false,
			wantDetail:    "disabled",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decision := DecideImageCapabilities(tt.features, DetectTerminalImageSignals(tt.environ), tt.cacheWritable)

			if decision.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; summary=%s", decision.Status, tt.wantStatus, decision.Summary())
			}
			for name, feature := range map[string]ImageFeatureDecision{
				"avatar": decision.Avatar,
				"emoji":  decision.Emoji,
				"emote":  decision.Emote,
			} {
				if feature.Active != tt.wantActive {
					t.Fatalf("%s active = %v, want %v; decision=%#v", name, feature.Active, tt.wantActive, decision)
				}
			}
			if !strings.Contains(decision.Summary(), tt.wantDetail) {
				t.Fatalf("summary = %q, want it to contain %q", decision.Summary(), tt.wantDetail)
			}
		})
	}
}

func TestImageCapabilityAssetOptionsUseResolvedState(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"

	autoUnsupported := DecideImageCapabilities(cfg.Features, DetectTerminalImageSignals([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), true)
	if autoUnsupported.Avatar.Active || autoUnsupported.Emoji.Active || autoUnsupported.Emote.Active {
		t.Fatalf("auto unsupported active features = %#v", autoUnsupported)
	}
	if opts := autoUnsupported.AssetOptions(); opts.EmojiWidthCells != 0 || opts.EmoteWidthCells != 0 || opts.BadgeWidthCells != 0 {
		t.Fatalf("auto unsupported asset options = %#v, want no image widths", opts)
	}

	cfg.Features.ImageMode = "normal"
	explicitDegraded := DecideImageCapabilities(cfg.Features, DetectTerminalImageSignals([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), true)
	if !explicitDegraded.Avatar.Active || !explicitDegraded.Emoji.Active || !explicitDegraded.Emote.Active {
		t.Fatalf("explicit degraded active features = %#v, want active image features", explicitDegraded)
	}
	if opts := explicitDegraded.AssetOptions(); !opts.ShowAvatars || opts.EmojiWidthCells == 0 || opts.EmoteWidthCells == 0 || opts.BadgeWidthCells == 0 {
		t.Fatalf("explicit degraded asset options = %#v, want reserved image fallback widths", opts)
	}
}

func TestUnknownImageModeFallsBackToAutoSupportState(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ImageMode = "surprise"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"

	decision := DecideImageCapabilities(cfg.Features, DetectTerminalImageSignals([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), true)
	if decision.Status != ImageCapabilityUnsupported {
		t.Fatalf("unknown image mode status = %q, want unsupported auto fallback; summary=%s", decision.Status, decision.Summary())
	}
	if decision.Avatar.Active || decision.Emoji.Active || decision.Emote.Active {
		t.Fatalf("unknown image mode active features = %#v, want inactive auto fallback", decision)
	}
	if !strings.Contains(decision.Summary(), "unknown image mode fell back to auto") {
		t.Fatalf("summary = %q, want unknown-mode fallback detail", decision.Summary())
	}
}
