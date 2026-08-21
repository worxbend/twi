package helix

import (
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

// TestRedactCredentialsCoversEveryCredentialKey pins the set of key names that
// must never have their value printed.
//
// Helix error responses are echoed into user-facing errors and the debug log
// through credentialSafeUserError, so anything this scanner misses is printed.
// The keys below are the ones internal/auth -- the package that owns this
// policy -- redacts.
func TestRedactCredentialsCoversEveryCredentialKey(t *testing.T) {
	keys := []string{
		"access_token", "access-token",
		"oauth_token", "oauth-token",
		"refresh_token", "refresh-token",
		"client_secret", "client-secret",
		"authorization_code", "code_verifier", "code_challenge",
		"state", "code",
	}
	const secret = "s3cr3t-value-that-must-not-be-printed"

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			body := "twitch returned HTTP 400: {" + key + "=" + secret + "}"
			got := redactCredentials(body, twitch.TokenCredentials{})
			if strings.Contains(got, secret) {
				t.Errorf("redactCredentials leaked the value of %s: %q", key, got)
			}
		})
	}
}

// TestRedactCredentialsCoversBareTokens covers the two prefixed forms that
// carry a credential without a key name in front of them.
func TestRedactCredentialsCoversBareTokens(t *testing.T) {
	for name, body := range map[string]string{
		"oauth prefix":  "connect failed for oauth:s3cr3t-token-value",
		"bearer header": "Authorization: Bearer s3cr3t-token-value",
	} {
		if got := redactCredentials(body, twitch.TokenCredentials{}); strings.Contains(got, "s3cr3t-token-value") {
			t.Errorf("redactCredentials leaked a %s: %q", name, got)
		}
	}
}
