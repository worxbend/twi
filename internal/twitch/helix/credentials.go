package helix

import (
	"context"
	"errors"
	"strings"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
)

// This file holds one policy: what twi is allowed to say about a failed Twitch
// call. A Helix or OAuth error body echoes back the request that produced it,
// and that request carries the credentials, so every error this package returns
// passes through here before anyone can see it.

// credentialSafeError wraps err with a message safe to display and to log.
//
// A cancelled or timed-out context is returned unchanged: that is the caller's
// own doing, and wrapping it would hide the sentinel value the caller checks
// for. Every other error keeps its text with the credential values in
// credentials replaced by a placeholder.
func credentialSafeError(action string, err error, credentials twitch.TokenCredentials) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}

// credentialSafeUserError is credentialSafeError for the Helix adapters, which
// hold an OAuth token and nothing else -- no refresh token, no client secret.
//
// The two used to be separate functions with byte-identical bodies. Only the
// call site differs, so the shorter one is now an adapter over the other and
// the redaction rules have a single implementation.
func credentialSafeUserError(action string, err error, oauthToken string) error {
	return credentialSafeError(action, err, twitch.TokenCredentials{OAuthToken: oauthToken})
}

// redactCredentials makes a Helix response or error safe to show and to log.
//
// The rules live in internal/auth, which owns this policy for the whole
// program. This package used to carry its own copy of the patterns, and it had
// drifted: it never matched access_token, oauth_token, code_verifier,
// code_challenge or state, so a Helix error body echoing any of those printed
// the value.
func redactCredentials(value string, credentials twitch.TokenCredentials) string {
	secrets := credentialSecrets(credentials)
	values := make([]auth.Secret, 0, len(secrets))
	for _, secret := range secrets {
		values = append(values, auth.NewSecret(secret))
	}
	return auth.NewRedactor(values...).Redact(value)
}

// credentialSecrets lists the distinct, non-empty secret values in
// credentials, so each of them can be redacted out of a message.
func credentialSecrets(credentials twitch.TokenCredentials) []string {
	values := []string{
		strings.TrimSpace(credentials.OAuthToken),
		strings.TrimSpace(accessTokenForValidation(credentials.OAuthToken)),
		strings.TrimSpace(credentials.RefreshToken),
		strings.TrimSpace(credentials.ClientSecret),
	}
	seen := make(map[string]bool, len(values))
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		secrets = append(secrets, value)
	}
	return secrets
}

// accessTokenForValidation strips the "oauth:" prefix an IRC-style token
// carries, since the HTTP APIs want the bare access token.
func accessTokenForValidation(value string) string {
	value = strings.TrimSpace(value)
	if prefix, body, ok := strings.Cut(value, ":"); ok && strings.EqualFold(prefix, "oauth") {
		return strings.TrimSpace(body)
	}
	return value
}
