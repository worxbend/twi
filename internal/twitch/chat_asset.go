package twitch

import (
	"strings"
)

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
	return preferredImageURL(m.ImageURL1X, m.ImageURL2X, m.ImageURL4X)
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
	return preferredImageURL(m.ImageURL1X, m.ImageURL2X, m.ImageURL4X)
}

// preferredImageURL picks the medium-size image when present, falling back to
// the small one and then the large one.
func preferredImageURL(url1x, url2x, url4x string) string {
	if strings.TrimSpace(url2x) != "" {
		return strings.TrimSpace(url2x)
	}
	if strings.TrimSpace(url1x) != "" {
		return strings.TrimSpace(url1x)
	}
	return strings.TrimSpace(url4x)
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
