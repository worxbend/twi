package auth

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// RedactedSecret is the placeholder used when a sensitive auth value is
	// formatted or redacted for user-facing output.
	RedactedSecret = "<redacted>"
)

var (
	oauthTokenPattern    = regexp.MustCompile(`(?i)oauth:[^\s"'&?]+`)
	bearerTokenPattern   = regexp.MustCompile(`(?i)(bearer\s+)[^\s"'&?]+`)
	credentialKeyPattern = regexp.MustCompile(`(?i)((?:access[_-]token|oauth[_-]token|refresh[_-]token|client[_-]secret|authorization_code|code_verifier|code_challenge|state|code)(?:["']?\s*[:=]\s*["']?))[^"'\s&?]+`)
)

// Secret wraps a sensitive auth value. Its default string formatting is
// redacted; callers must use Reveal when they intentionally need the raw value
// for an OAuth HTTP request, token validation request, or test assertion.
type Secret string

// NewSecret returns a Secret containing value.
func NewSecret(value string) Secret {
	return Secret(value)
}

// Present reports whether the secret contains a non-empty value after trimming
// surrounding whitespace.
func (s Secret) Present() bool {
	return strings.TrimSpace(s.Reveal()) != ""
}

// Reveal returns the raw secret value. Do not include this value in logs,
// formatted errors, diagnostics, snapshots, or persisted records before the
// credential storage boundary explicitly owns that behavior.
func (s Secret) Reveal() string {
	return string(s)
}

// Redacted returns the printable representation for this secret.
func (s Secret) Redacted() string {
	if !s.Present() {
		return ""
	}
	return RedactedSecret
}

// String returns a redacted representation of the secret.
func (s Secret) String() string {
	return s.Redacted()
}

// GoString returns a redacted representation used by %#v formatting.
func (s Secret) GoString() string {
	if !s.Present() {
		return "auth.Secret(\"\")"
	}
	return "auth.Secret(" + RedactedSecret + ")"
}

// MarshalJSON encodes the redacted representation so accidental structured
// output does not persist raw secrets.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(s.Redacted())), nil
}

// MarshalText encodes the redacted representation for text encoders.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(s.Redacted()), nil
}

// Redactor redacts auth secrets and common OAuth credential patterns from text
// that may become user-facing output.
type Redactor struct {
	secrets []string
}

// NewRedactor creates a redactor for explicit secret values. OAuth-prefixed
// access tokens are redacted both with and without the oauth: prefix.
func NewRedactor(secrets ...Secret) Redactor {
	return Redactor{secrets: secretStrings(secrets)}
}

// With returns a copy of r that also redacts secrets.
//
// A multi-step flow learns its secrets as it goes: an OAuth login knows the
// configured credentials up front, then the authorization URL and state, then
// the callback code, then the tokens. Every step has to redact everything
// learned so far, and extending the redactor says that directly. Building a
// fresh redactor at each step instead means restating the earlier secrets
// every time, which is where one eventually gets left out.
func (r Redactor) With(secrets ...Secret) Redactor {
	if len(secrets) == 0 {
		return r
	}
	return Redactor{secrets: mergeSecretStrings(r.secrets, secrets)}
}

// Redact removes known OAuth credential patterns and the explicit secrets
// configured on the redactor.
func (r Redactor) Redact(value string) string {
	if value == "" {
		return ""
	}

	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, RedactedSecret)
	}
	value = oauthTokenPattern.ReplaceAllString(value, RedactedSecret)
	value = bearerTokenPattern.ReplaceAllString(value, "${1}"+RedactedSecret)
	value = credentialKeyPattern.ReplaceAllString(value, "${1}"+RedactedSecret)
	return value
}

func secretStrings(secrets []Secret) []string {
	return mergeSecretStrings(nil, secrets)
}

// mergeSecretStrings folds secrets into the already-known strings, dropping
// blanks and duplicates, and returns them longest-first.
//
// The ordering matters: redaction is plain string replacement, so if one
// secret contains another (an "oauth:abc" token contains "abc"), replacing
// the shorter one first would leave the "oauth:" prefix and a redaction
// marker behind instead of removing the whole token.
func mergeSecretStrings(known []string, secrets []Secret) []string {
	seen := map[string]bool{}
	values := make([]string, 0, len(known)+len(secrets))
	for _, value := range known {
		appendSecretString(&values, seen, value)
	}
	for _, secret := range secrets {
		value := strings.TrimSpace(secret.Reveal())
		if value == "" {
			continue
		}
		appendSecretString(&values, seen, value)
		if prefix, body, ok := strings.Cut(value, ":"); ok && strings.EqualFold(prefix, "oauth") {
			appendSecretString(&values, seen, strings.TrimSpace(body))
		}
	}

	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})
	return values
}

func appendSecretString(values *[]string, seen map[string]bool, value string) {
	if value == "" || seen[value] {
		return
	}
	seen[value] = true
	*values = append(*values, value)
}
