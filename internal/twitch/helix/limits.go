package helix

import (
	"github.com/worxbend/twi/internal/textsafe"
)

// maxResponseBodySize bounds a Helix API response. The largest of them
// (global emote and badge sets) are a few hundred kilobytes, so 4 MiB leaves
// generous headroom. The cap is far above any real response: it is a backstop
// against a broken or hostile peer, not a validation rule. See
// internal/twitch/jsonbody for why the read is bounded at all.
const maxResponseBodySize = 4 << 20

// sanitizeDisplayList applies textsafe.Display to every entry of a list of
// free-text values (stream tags, for instance) that will be drawn.
func sanitizeDisplayList(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, value := range in {
		out = append(out, textsafe.Display(value))
	}
	return out
}
