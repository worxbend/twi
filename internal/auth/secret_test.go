package auth

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSecretFormattingRedactsByDefault(t *testing.T) {
	secret := NewSecret("oauth:secret-token")
	tokenSet := TokenSet{
		AccessToken:  secret,
		RefreshToken: NewSecret("refresh-secret"),
	}

	for _, formatted := range []string{
		fmt.Sprint(secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%v", tokenSet),
		fmt.Sprintf("%+v", tokenSet),
		fmt.Sprintf("%#v", tokenSet),
		fmt.Sprintf("%q", tokenSet),
	} {
		for _, raw := range []string{"oauth:secret-token", "secret-token", "refresh-secret"} {
			if strings.Contains(formatted, raw) {
				t.Fatalf("formatted value leaked %q: %s", raw, formatted)
			}
		}
		if !strings.Contains(formatted, RedactedSecret) {
			t.Fatalf("formatted value = %q, want redaction marker", formatted)
		}
	}

	if got := secret.Reveal(); got != "oauth:secret-token" {
		t.Fatalf("Reveal = %q, want raw secret for HTTP adapters and tests", got)
	}
}

func TestSecretStructuredEncodingRedactsByDefault(t *testing.T) {
	tokenSet := TokenSet{
		AccessToken:  NewSecret("oauth:access-secret"),
		RefreshToken: NewSecret("refresh-secret"),
	}

	encoded, err := json.Marshal(tokenSet)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(encoded)
	for _, raw := range []string{"oauth:access-secret", "access-secret", "refresh-secret"} {
		if strings.Contains(got, raw) {
			t.Fatalf("encoded token set leaked %q: %s", raw, got)
		}
	}
	if strings.Count(got, "redacted") != 2 {
		t.Fatalf("encoded token set = %s, want two redacted secrets", got)
	}
}

func TestRedactorRedactsPatternsAndExplicitSecrets(t *testing.T) {
	redactor := NewRedactor(
		NewSecret("oauth:explicit-access"),
		NewSecret("refresh-explicit"),
		NewSecret("client-secret"),
		NewSecret("state-secret"),
		NewSecret("https://id.example/authorize?client_id=client-id&state=state-secret"),
	)
	text := strings.Join([]string{
		"oauth:explicit-access",
		"explicit-access",
		"Bearer bearer-secret",
		"access_token=query-access",
		"refresh_token=refresh-explicit",
		"client_secret=client-secret",
		"state=state-secret",
		"code=callback-code",
		"code_verifier=verifier-secret",
		"https://id.example/authorize?client_id=client-id&state=state-secret",
	}, " ")

	got := redactor.Redact(text)
	for _, raw := range []string{
		"oauth:explicit-access",
		"explicit-access",
		"bearer-secret",
		"query-access",
		"refresh-explicit",
		"client-secret",
		"state-secret",
		"callback-code",
		"verifier-secret",
		"https://id.example",
	} {
		if strings.Contains(got, raw) {
			t.Fatalf("redacted text leaked %q: %s", raw, got)
		}
	}
	if strings.Count(got, RedactedSecret) < 8 {
		t.Fatalf("redacted text = %q, want redaction markers", got)
	}
}

// TestRedactorCoversQuotedOAuthTokens pins the quoted form, which the IRC
// transport used to compensate for with a second pattern of its own.
//
// Twitch's IRC errors quote the token. The unquoted pattern deliberately stops
// at a quote so a token embedded in JSON does not swallow its own closing
// delimiter, which means the quoted form needs a rule of its own -- and having
// that rule live only in the transport is how the two drifted apart.
func TestRedactorCoversQuotedOAuthTokens(t *testing.T) {
	const secret = "s3cr3t-token-value"
	for name, input := range map[string]string{
		"double quoted": `login failed for oauth:"` + secret + `"`,
		"single quoted": `login failed for oauth:'` + secret + `'`,
		"unquoted":      `login failed for oauth:` + secret,
		"json embedded": `{"token":"oauth:` + secret + `"}`,
	} {
		if got := NewRedactor().Redact(input); strings.Contains(got, secret) {
			t.Errorf("%s: Redact(%q) = %q, still contains the token", name, input, got)
		}
	}
}

// TestRedactorPlaceholderIsConfigurable covers WithPlaceholder, which exists so
// a surface can adopt these patterns without changing the marker its output
// already prints -- previously the reason such surfaces kept their own copy of
// the patterns.
func TestRedactorPlaceholderIsConfigurable(t *testing.T) {
	got := NewRedactor().WithPlaceholder("[redacted]").Redact("token oauth:abc123 here")
	if strings.Contains(got, "abc123") {
		t.Fatalf("Redact leaked the token: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("Redact = %q, want the configured [redacted] marker", got)
	}
	if strings.Contains(got, RedactedSecret) {
		t.Errorf("Redact = %q, want the configured marker instead of the default", got)
	}
}
