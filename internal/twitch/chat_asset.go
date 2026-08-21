package twitch

import (
	"context"
	"strings"
)

// ChatAssetLookup resolves Twitch emote and badge metadata through a Twitch API
// boundary. Implementations return public image URLs or templates only; callers
// are responsible for caching and downloading image bytes.
type ChatAssetLookup interface {
	GetGlobalEmotes(context.Context) ([]EmoteMetadata, error)
	GetChannelEmotes(context.Context, string) ([]EmoteMetadata, error)
	GetGlobalBadges(context.Context) ([]BadgeMetadata, error)
	GetChannelBadges(context.Context, string) ([]BadgeMetadata, error)
}

// EmoteMetadata contains the Helix fields needed to build a CDN image URL.
type EmoteMetadata struct {
	ID          string
	Name        string
	TemplateURL string
	ImageURL1X  string
	ImageURL2X  string
	ImageURL4X  string
	Formats     []string
	Scales      []string
	ThemeModes  []string
}

// ImageURL returns a deterministic static/light URL when Twitch's template is
// available, falling back to the static URLs Helix includes in each item.
func (m EmoteMetadata) ImageURL() string {
	format := preferredValue(m.Formats, "static")
	theme := preferredValue(m.ThemeModes, "light")
	scale := preferredValue(m.Scales, "2.0")
	if strings.TrimSpace(m.TemplateURL) != "" && strings.TrimSpace(m.ID) != "" && format != "" && theme != "" && scale != "" {
		out := m.TemplateURL
		out = strings.ReplaceAll(out, "{{id}}", m.ID)
		out = strings.ReplaceAll(out, "{{format}}", format)
		out = strings.ReplaceAll(out, "{{theme_mode}}", theme)
		out = strings.ReplaceAll(out, "{{scale}}", scale)
		return out
	}
	if strings.TrimSpace(m.ImageURL2X) != "" {
		return strings.TrimSpace(m.ImageURL2X)
	}
	if strings.TrimSpace(m.ImageURL1X) != "" {
		return strings.TrimSpace(m.ImageURL1X)
	}
	return strings.TrimSpace(m.ImageURL4X)
}

// BadgeMetadata contains one Twitch badge version image.
type BadgeMetadata struct {
	SetID       string
	ID          string
	Title       string
	Description string
	ImageURL1X  string
	ImageURL2X  string
	ImageURL4X  string
}

// ImageURL returns a deterministic medium-size badge URL when present.
func (m BadgeMetadata) ImageURL() string {
	if strings.TrimSpace(m.ImageURL2X) != "" {
		return strings.TrimSpace(m.ImageURL2X)
	}
	if strings.TrimSpace(m.ImageURL1X) != "" {
		return strings.TrimSpace(m.ImageURL1X)
	}
	return strings.TrimSpace(m.ImageURL4X)
}

func preferredValue(values []string, preferred string) string {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), preferred) {
			return preferred
		}
	}
	if len(values) == 0 {
		return preferred
	}
	return strings.TrimSpace(values[0])
}
