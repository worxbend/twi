// Package jsonbody decodes JSON response bodies through an explicit size
// ceiling rather than trusting them to be small.
//
// twi runs unattended for hours: it refreshes its OAuth token on a timer and
// polls Helix while chat is open. json.NewDecoder(resp.Body) reads as much as
// the JSON structure asks for, with no upper bound, so an endpoint (or an
// intermediary) answering with a huge or slowly trickling body would have the
// process buffer all of it into memory before failing. An io.LimitReader turns
// that into a bounded read that fails with "unexpected EOF" once the cap is
// hit.
//
// The helper lives in its own package because the boundary test in
// internal/twitch keeps encoding/json out of the domain package, and both the
// helix and the irc adapters need the same bounded decode.
package jsonbody

import (
	"encoding/json"
	"io"
)

// Decode decodes JSON from body into out, reading at most limit bytes. The
// limit is a backstop against a broken or hostile peer, not a validation
// rule: set it far above any real response.
func Decode(body io.Reader, limit int64, out any) error {
	return json.NewDecoder(io.LimitReader(body, limit)).Decode(out)
}
