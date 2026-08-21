package twitch

import (
	"context"
)

// Clip is the result of Twitch Helix "Create Clip". Twitch captures
// approximately the last 30-60 seconds of the broadcaster's stream at the
// moment of the call - there is no API parameter for a start time, end time,
// or duration. EditURL opens Twitch's own clip editor, where a human can
// trim the clip within whatever window Twitch still allows.
type Clip struct {
	ID      string
	EditURL string
}

// ClipManager creates a Twitch clip of the broadcaster's current stream.
// Twitch only accepts this call while the broadcaster is live.
type ClipManager interface {
	CreateClip(ctx context.Context, broadcasterID string) (Clip, error)
}

// IsClipCreationUnavailable reports whether err is a 404 from Create Clip,
// Twitch's response when the broadcaster isn't currently live (clips can
// only be cut from a live broadcast).
func IsClipCreationUnavailable(err error) bool {
	return IsNoVideoFound(err)
}
