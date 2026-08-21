package helix

import (
	"encoding/json"
	"io"

	"github.com/worxbend/twi/internal/textsafe"
)

// Response bodies are read through an explicit ceiling rather than trusted to
// be small.
//
// `twi` runs unattended for hours: it refreshes its OAuth token on a timer and
// polls Helix while chat is open. `json.NewDecoder(resp.Body)` reads as much
// as the JSON structure asks for, with no upper bound, so a token endpoint or
// an intermediary that answers with a huge (or slowly trickling) body would
// have the process buffer all of it into memory before failing. An
// `io.LimitReader` turns that into a bounded read that fails with "unexpected
// EOF" once the cap is hit.
//
// The caps are deliberately far above any real response: they are a backstop
// against a broken or hostile peer, not a validation rule.
const (
	// maxResponseBodySize bounds a Helix API response. The largest of
	// them (global emote and badge sets) are a few hundred kilobytes, so 4
	// MiB leaves generous headroom.
	maxResponseBodySize = 4 << 20
)

// decodeJSONBody decodes JSON from body, reading at most limit bytes.
func decodeJSONBody(body io.Reader, limit int64, out any) error {
	return json.NewDecoder(io.LimitReader(body, limit)).Decode(out)
}

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
