package app

import (
	"os"
	"strings"

	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/storage"
)

// ImageStackStatus describes whether live startup should install async image
// asset services.
type ImageStackStatus string

const (
	ImageStackEnabled           ImageStackStatus = "enabled"
	ImageStackDisabled          ImageStackStatus = "disabled"
	ImageStackUnsupported       ImageStackStatus = "unsupported"
	ImageStackDegraded          ImageStackStatus = "degraded"
	ImageStackMissingDependency ImageStackStatus = "missing_dependency"
)

// ImageStackDecision is the live startup decision for asset resolution,
// downloading, preparation, and terminal rendering. It performs no network I/O.
type ImageStackDecision struct {
	Status         ImageStackStatus
	Detail         string
	Ready          bool
	Capability     render.ImageCapabilityDecision
	CacheDir       string
	SupportedKinds []string
	Missing        []string
}

// Supports reports whether the live image stack should schedule requests for
// one asset kind.
func (d ImageStackDecision) Supports(kind string) bool {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return false
	}
	for _, supported := range d.SupportedKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

// SupportedKindSet returns a map suitable for app.ClientOptions.
func (d ImageStackDecision) SupportedKindSet() map[string]bool {
	if len(d.SupportedKinds) == 0 {
		return nil
	}
	out := make(map[string]bool, len(d.SupportedKinds))
	for _, kind := range d.SupportedKinds {
		out[kind] = true
	}
	return out
}

// DecideLiveImageStack resolves the non-network gates for live image services:
// config modes, Twitch API credentials, emoji provider config, cache
// writability, and terminal image capability.
func DecideLiveImageStack(cfg config.Config, environ []string, cacheDir string) ImageStackDecision {
	if environ == nil {
		environ = os.Environ()
	}
	signals := render.DetectTerminalImageSignals(environ)
	capability := render.DecideImageCapabilities(cfg.Features, signals, true)
	decision := ImageStackDecision{Capability: capability}

	switch capability.Status {
	case render.ImageCapabilityDisabled:
		decision.Status = ImageStackDisabled
		decision.Detail = imageStackDetail("disabled", capability.Detail, "text fallbacks active")
		return decision
	case render.ImageCapabilityUnsupported:
		decision.Status = ImageStackUnsupported
		decision.Detail = imageStackDetail("unsupported", capability.Detail)
		return decision
	}

	if !imageStackRendererAllowed(capability) {
		decision.Status = ImageStackDegraded
		decision.Detail = imageStackDetail("degraded", capability.Detail, "image stack not installed until a Kitty/Ghostty graphics signal is present", "text fallbacks active")
		return decision
	}

	assetDir, err := assetCacheDir(cacheDir)
	if err != nil {
		return missingImageStackDependency(decision, "asset cache path unavailable: "+err.Error())
	}
	if err := storage.ProbeWritableDir(assetDir); err != nil {
		return missingImageStackDependency(decision, "asset cache not writable: "+err.Error())
	}
	decision.CacheDir = assetDir
	decision.Capability = render.DecideImageCapabilities(cfg.Features, signals, true)

	supportedKinds, missing := imageStackSupportedKinds(cfg, decision.Capability)
	decision.SupportedKinds = supportedKinds
	decision.Missing = missing
	switch {
	case len(supportedKinds) == 0 && len(missing) > 0:
		decision.Status = ImageStackMissingDependency
		decision.Detail = imageStackDetail("missing-dependency", "missing "+strings.Join(missing, ", "), "text fallbacks active")
	case len(supportedKinds) == 0:
		decision.Status = ImageStackDisabled
		decision.Detail = imageStackDetail("disabled", "no image-backed asset kinds are enabled", "text fallbacks active")
	case len(missing) > 0:
		decision.Status = ImageStackDegraded
		decision.Ready = true
		decision.Detail = imageStackDetail("degraded", "supported="+strings.Join(supportedKinds, ","), "missing "+strings.Join(missing, ", "), "fallbacks active for unavailable image assets")
	case decision.Capability.Status == render.ImageCapabilityDegraded:
		decision.Status = ImageStackDegraded
		decision.Ready = true
		decision.Detail = imageStackDetail("degraded", "supported="+strings.Join(supportedKinds, ","), decision.Capability.Detail, "fallbacks remain available")
	default:
		decision.Status = ImageStackEnabled
		decision.Ready = true
		decision.Detail = imageStackDetail("enabled", "supported="+strings.Join(supportedKinds, ","), "cache=writable")
	}
	return decision
}

func imageStackRendererAllowed(capability render.ImageCapabilityDecision) bool {
	if !capability.EnableKitty || !capability.Signals.KittyCompatible {
		return false
	}
	return capability.Status == render.ImageCapabilityEnabled || capability.Status == render.ImageCapabilityDegraded
}

func missingImageStackDependency(decision ImageStackDecision, detail string) ImageStackDecision {
	decision.Status = ImageStackMissingDependency
	decision.Missing = []string{detail}
	decision.Detail = imageStackDetail("missing-dependency", detail, "text fallbacks active")
	return decision
}

func imageStackSupportedKinds(cfg config.Config, capability render.ImageCapabilityDecision) ([]string, []string) {
	var supported []string
	var missing []string

	twitchRequested := imageStackTwitchAPIAssetsRequested(capability)
	twitchCredentialsReady, missingCredentials := imageStackTwitchCredentialsReady(cfg)
	if twitchRequested && !twitchCredentialsReady {
		missing = append(missing, missingCredentials...)
	}
	if twitchCredentialsReady {
		if capability.Avatar.Active {
			supported = append(supported, assets.KindAvatar)
		}
		if imageStackGlobalAssetsActive(capability) {
			supported = append(supported, assets.KindBadge)
		}
	}
	if capability.Emote.Active {
		supported = append(supported, assets.KindTwitchEmote)
	}

	if capability.Emoji.Active {
		err := assets.ValidateEmojiProviderConfig(assets.EmojiProviderConfig{
			Provider:    cfg.Features.EmojiProvider,
			URLTemplate: cfg.Features.EmojiURLTemplate,
		})
		if err != nil {
			missing = append(missing, "valid emoji provider config: "+err.Error())
		} else {
			supported = append(supported, assets.KindEmoji)
		}
	}

	return supported, missing
}

func imageStackTwitchAPIAssetsRequested(capability render.ImageCapabilityDecision) bool {
	return capability.Avatar.Active || imageStackGlobalAssetsActive(capability)
}

func imageStackGlobalAssetsActive(capability render.ImageCapabilityDecision) bool {
	return capability.Status == render.ImageCapabilityEnabled || capability.Status == render.ImageCapabilityDegraded
}

func imageStackTwitchCredentialsReady(cfg config.Config) (bool, []string) {
	var missing []string
	if strings.TrimSpace(cfg.Twitch.ClientID) == "" {
		missing = append(missing, "TWI_TWITCH_CLIENT_ID or TWITCH_CLIENT_ID")
	}
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		missing = append(missing, "TWI_TWITCH_OAUTH_TOKEN or TWITCH_ACCESS_TOKEN")
	}
	return len(missing) == 0, missing
}

func imageStackDetail(state string, parts ...string) string {
	kept := make([]string, 0, len(parts)+1)
	if state != "" {
		kept = append(kept, state)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ": ")
}

func (s ImageStackStatus) String() string {
	return string(s)
}

func (d ImageStackDecision) String() string {
	if d.Detail != "" {
		return d.Detail
	}
	return d.Status.String()
}
