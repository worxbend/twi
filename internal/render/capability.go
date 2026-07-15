package render

import (
	"fmt"
	"strings"

	"github.com/worxbend/twi/internal/config"
)

// ImageCapabilityStatus is the resolved state for terminal image support.
type ImageCapabilityStatus string

const (
	ImageCapabilityEnabled     ImageCapabilityStatus = "enabled"
	ImageCapabilityDisabled    ImageCapabilityStatus = "disabled"
	ImageCapabilityUnsupported ImageCapabilityStatus = "unsupported"
	ImageCapabilityDegraded    ImageCapabilityStatus = "degraded"
)

// TerminalImageSignals are environment-derived hints used for capability
// decisions. Detection is intentionally conservative because users can still
// force an explicit image mode when terminal environment variables are absent.
type TerminalImageSignals struct {
	Term            string
	TermProgram     string
	KittyWindowID   string
	KittyCompatible bool
	Ghostty         bool
	TrueColor       bool
	Color256        bool
}

// ImageFeatureDecision is the resolved state for one image-backed feature.
type ImageFeatureDecision struct {
	Mode   string
	Status ImageCapabilityStatus
	Detail string
	Active bool
}

// ImageCapabilityDecision is the deterministic image capability state shared
// by app startup, rendering fallbacks, and diagnostics.
type ImageCapabilityDecision struct {
	Mode           string
	RequestedMode  string
	Explicit       bool
	Status         ImageCapabilityStatus
	Detail         string
	Signals        TerminalImageSignals
	CacheWritable  bool
	Avatar         ImageFeatureDecision
	Emoji          ImageFeatureDecision
	Emote          ImageFeatureDecision
	EnableKitty    bool
	UnknownModes   []string
	DegradedReason []string
}

// DetectTerminalImageSignals converts an environment slice into terminal image
// hints without probing the terminal or filesystem.
func DetectTerminalImageSignals(environ []string) TerminalImageSignals {
	env := map[string]string{}
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}

	term := env["TERM"]
	termProgram := env["TERM_PROGRAM"]
	colorTerm := strings.ToLower(env["COLORTERM"])
	signals := TerminalImageSignals{
		Term:          term,
		TermProgram:   termProgram,
		KittyWindowID: env["KITTY_WINDOW_ID"],
		Ghostty:       strings.EqualFold(termProgram, "ghostty"),
		TrueColor: strings.Contains(colorTerm, "truecolor") ||
			strings.Contains(colorTerm, "24bit") ||
			strings.Contains(strings.ToLower(term), "truecolor") ||
			strings.Contains(strings.ToLower(term), "24bit") ||
			strings.Contains(strings.ToLower(term), "direct"),
		Color256: strings.Contains(strings.ToLower(term), "256color"),
	}
	signals.KittyCompatible = signals.KittyWindowID != "" ||
		strings.Contains(strings.ToLower(term), "xterm-kitty") ||
		signals.Ghostty
	return signals
}

// DecideImageCapabilities combines config modes, terminal hints, and cache
// writability into stable image capability decisions.
func DecideImageCapabilities(features config.FeatureConfig, signals TerminalImageSignals, cacheWritable bool) ImageCapabilityDecision {
	requestedMode := cleanMode(features.ImageMode)
	imageMode := requestedMode
	if imageMode == "" {
		imageMode = "auto"
	}

	decision := ImageCapabilityDecision{
		Mode:          imageMode,
		RequestedMode: imageMode,
		Signals:       signals,
		CacheWritable: cacheWritable,
		EnableKitty:   features.EnableKittyImages,
	}

	if !knownImageMode(imageMode) {
		decision.UnknownModes = append(decision.UnknownModes, "image="+features.ImageMode)
		imageMode = "auto"
		decision.Mode = imageMode
	}

	switch imageMode {
	case "off":
		decision.Status = ImageCapabilityDisabled
		decision.Detail = "disabled by image_mode=off"
	case "auto":
		if !features.EnableKittyImages {
			decision.Status = ImageCapabilityDisabled
			decision.Detail = "disabled by enable_kitty_images=false"
			break
		}
		if !signals.KittyCompatible {
			decision.Status = ImageCapabilityUnsupported
			decision.Detail = "auto mode found no Kitty/Ghostty graphics signal; text fallbacks active"
			break
		}
		decision.Status = ImageCapabilityEnabled
		decision.Detail = "auto mode enabled Kitty-compatible images"
	default:
		decision.Explicit = true
		if !features.EnableKittyImages {
			decision.Status = ImageCapabilityDisabled
			decision.Detail = "disabled by enable_kitty_images=false"
			break
		}
		decision.Status = ImageCapabilityEnabled
		decision.Detail = "explicit image mode enabled"
		if !signals.KittyCompatible {
			decision.DegradedReason = append(decision.DegradedReason, "no Kitty/Ghostty graphics signal")
		}
	}

	if decision.Status == ImageCapabilityEnabled {
		if !cacheWritable {
			decision.DegradedReason = append(decision.DegradedReason, "asset cache is not writable")
		}
		if !signals.TrueColor {
			decision.DegradedReason = append(decision.DegradedReason, "no true-color hint")
		}
		if len(decision.DegradedReason) > 0 {
			decision.Status = ImageCapabilityDegraded
			decision.Detail = "image mode degraded: " + strings.Join(decision.DegradedReason, "; ")
		}
	}

	if len(decision.UnknownModes) > 0 {
		switch decision.Status {
		case ImageCapabilityEnabled:
			decision.Status = ImageCapabilityDegraded
			decision.Detail = appendCapabilityDetail("unknown image mode fell back to auto", decision.Detail)
		case ImageCapabilityDegraded:
			decision.Detail = appendCapabilityDetail("unknown image mode fell back to auto", decision.Detail)
		default:
			decision.Detail = appendCapabilityDetail(decision.Detail, "unknown image mode fell back to auto")
		}
	}

	decision.Avatar = decideImageFeature("avatar", features.AvatarMode, "initials", "off", decision)
	decision.Emoji = decideImageFeature("emoji", features.EmojiMode, "unicode", "", decision)
	decision.Emote = decideImageFeature("emote", features.EmoteMode, "text", "", decision)
	return decision
}

// AssetOptions returns renderer fallback options for the resolved image state.
func (d ImageCapabilityDecision) AssetOptions() AssetOptions {
	defaults := FallbackAssetOptions()
	opts := AssetOptions{}
	if d.Avatar.Mode != "off" {
		opts.ShowAvatars = true
		opts.AvatarWidthCells = defaults.AvatarWidthCells
	}
	if d.Status == ImageCapabilityEnabled || d.Status == ImageCapabilityDegraded {
		opts.BadgeWidthCells = defaults.BadgeWidthCells
	}
	if d.Emoji.Active {
		opts.EmojiWidthCells = defaults.EmojiWidthCells
	}
	if d.Emote.Active {
		opts.EmoteWidthCells = defaults.EmoteWidthCells
	}
	return opts
}

// Summary returns a compact diagnostic string suitable for status bars and
// doctor output.
func (d ImageCapabilityDecision) Summary() string {
	parts := []string{
		fmt.Sprintf("%s: image=%s", d.Status, d.Mode),
		fmt.Sprintf("avatar=%s/%s", d.Avatar.Mode, d.Avatar.Status),
		fmt.Sprintf("emoji=%s/%s", d.Emoji.Mode, d.Emoji.Status),
		fmt.Sprintf("emote=%s/%s", d.Emote.Mode, d.Emote.Status),
	}
	if d.Signals.KittyCompatible {
		if d.Signals.Ghostty {
			parts = append(parts, "terminal=ghostty")
		} else {
			parts = append(parts, "terminal=kitty")
		}
	} else {
		parts = append(parts, "terminal=no-kitty")
	}
	if d.Signals.TrueColor {
		parts = append(parts, "color=truecolor")
	} else if d.Signals.Color256 {
		parts = append(parts, "color=256")
	} else {
		parts = append(parts, "color=limited")
	}
	if d.CacheWritable {
		parts = append(parts, "cache=writable")
	} else {
		parts = append(parts, "cache=unwritable")
	}
	if d.Detail != "" {
		parts = append(parts, d.Detail)
	}
	if len(d.UnknownModes) > 0 {
		parts = append(parts, "unknown: "+strings.Join(d.UnknownModes, ", "))
	}
	return strings.Join(parts, "; ")
}

func decideImageFeature(name, mode, fallbackMode, offMode string, base ImageCapabilityDecision) ImageFeatureDecision {
	canonical := cleanMode(mode)
	if canonical == "" {
		canonical = fallbackMode
	}
	feature := ImageFeatureDecision{Mode: canonical}
	imageRequested := canonical == "image"
	if offMode != "" && canonical == offMode {
		feature.Status = ImageCapabilityDisabled
		feature.Detail = name + " disabled by config"
		return feature
	}
	if !imageRequested {
		feature.Status = ImageCapabilityDisabled
		feature.Detail = name + " uses " + canonical + " fallback"
		return feature
	}
	feature.Status = base.Status
	feature.Detail = name + " image mode follows global image capability"
	feature.Active = base.Status == ImageCapabilityEnabled || base.Status == ImageCapabilityDegraded
	if base.Status == ImageCapabilityDisabled {
		feature.Active = false
		feature.Detail = name + " image mode disabled by global image setting"
	}
	if base.Status == ImageCapabilityUnsupported {
		feature.Active = false
		feature.Detail = name + " image mode unsupported in this terminal"
	}
	return feature
}

func cleanMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func appendCapabilityDetail(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "; ")
}

func knownImageMode(mode string) bool {
	switch mode {
	case "auto", "off", "small", "normal", "large":
		return true
	default:
		return false
	}
}
