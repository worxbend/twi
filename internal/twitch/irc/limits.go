package irc

import (
	"encoding/json"
	"io"
)

// maxOAuthRefreshBodySize bounds a Twitch OAuth token refresh response, which
// is a handful of short fields.
//
// twi runs unattended for hours and refreshes its token on a timer, so this
// decode happens without anyone watching. json.NewDecoder reads as much as
// the JSON structure asks for with no upper bound, so a token endpoint (or an
// intermediary) answering with a huge or slowly trickling body would have the
// process buffer all of it before failing. The cap is far above any real
// response: it is a backstop against a broken or hostile peer, not a
// validation rule.
const maxOAuthRefreshBodySize = 4096

// decodeJSONBody decodes JSON from body, reading at most limit bytes.
func decodeJSONBody(body io.Reader, limit int64, out any) error {
	return json.NewDecoder(io.LimitReader(body, limit)).Decode(out)
}
