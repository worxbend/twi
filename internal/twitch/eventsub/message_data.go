package eventsub

import (
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

// Message and notification payloads use the same documented fragments/badges.
// Wire structures remain here; no arbitrary transport JSON reaches public DTOs.
type chatBody struct {
	Text      string         `json:"text"`
	Fragments []chatFragment `json:"fragments"`
}
type chatFragment struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emote *struct {
		ID string `json:"id"`
	} `json:"emote"`
	Mention *struct {
		ID string `json:"user_id"`
	} `json:"mention"`
	Cheermote *struct {
		Prefix string `json:"prefix"`
		Bits   int    `json:"bits"`
		Tier   int    `json:"tier"`
	} `json:"cheermote"`
	GIF *struct {
		ID string `json:"id"`
	} `json:"gif"`
}
type chatBadge struct {
	SetID string `json:"set_id"`
	ID    string `json:"id"`
	Info  string `json:"info"`
}

func normalizeFragments(raw []chatFragment) []twitch.MessageFragment {
	fragments := make([]twitch.MessageFragment, 0, len(raw))
	for _, fragment := range raw {
		value := twitch.MessageFragment{Type: twitch.FragmentText, Text: fragment.Text}
		switch fragment.Type {
		case "emote":
			value.Type = twitch.FragmentEmote
			if fragment.Emote != nil {
				value.Ref = twitch.AssetRef{Kind: "emote", ID: fragment.Emote.ID}
			}
		case "mention":
			value.Type = twitch.FragmentMention
			if fragment.Mention != nil {
				value.Ref = twitch.AssetRef{Kind: "user", ID: fragment.Mention.ID}
			}
		case "cheermote":
			value.Type = twitch.FragmentBits
			if fragment.Cheermote != nil {
				value.Ref = twitch.AssetRef{Kind: "cheermote", ID: fragment.Cheermote.Prefix}
				value.Bits = max(0, fragment.Cheermote.Bits)
				value.Tier = max(0, fragment.Cheermote.Tier)
			}
		case "gif":
			value.Type = twitch.FragmentGIF
			if fragment.GIF != nil {
				value.Ref = twitch.AssetRef{Kind: "gif", ID: fragment.GIF.ID}
			}
		}
		fragments = append(fragments, value)
	}
	return fragments
}
func normalizeBadges(raw []chatBadge) []twitch.Badge {
	badges := make([]twitch.Badge, 0, len(raw))
	for _, badge := range raw {
		badges = append(badges, twitch.Badge{SetID: badge.SetID, ID: badge.ID, Info: badge.Info, Ref: twitch.AssetRef{Kind: "badge", ID: badge.ID}})
	}
	return badges
}
func normalizeAction(text string) (string, bool) {
	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		return strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01"), true
	}
	return text, false
}

func stripActionFragmentBoundaries(fragments []twitch.MessageFragment) {
	if len(fragments) == 0 {
		return
	}
	// Twitch may include CTCP framing in boundary text fragments while emotes
	// remain separate. Remove wrappers only where the fragments actually contain them.
	fragments[0].Text = strings.TrimPrefix(fragments[0].Text, "\x01ACTION ")
	last := len(fragments) - 1
	fragments[last].Text = strings.TrimSuffix(fragments[last].Text, "\x01")
}
