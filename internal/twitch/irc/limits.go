package irc

// maxOAuthRefreshBodySize bounds a Twitch OAuth token refresh response, which
// is a handful of short fields. The cap is far above any real response: it is
// a backstop against a broken or hostile peer, not a validation rule. See
// internal/twitch/jsonbody for why the read is bounded at all.
const maxOAuthRefreshBodySize = 4096
