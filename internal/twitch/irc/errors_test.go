package irc

import (
	"errors"
	"strings"
	"testing"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/twitch"
)

// TestCredentialSafeIRCErrorAttachesSentinel connects the two halves of error
// classification: the transport is the only place that knows Twitch rejected
// the credentials, so it must mark the error, or the app is back to guessing
// from message text.
func TestCredentialSafeIRCErrorAttachesSentinel(t *testing.T) {
	err := credentialSafeError(gempir.ErrLoginAuthenticationFailed)
	if !twitch.IsAuthError(err) {
		t.Fatal("credentialSafeError did not mark a login failure as an auth error")
	}
	if !errors.Is(err, gempir.ErrLoginAuthenticationFailed) {
		t.Fatal("credentialSafeError discarded the underlying cause")
	}
	if !strings.Contains(err.Error(), "chat:read") {
		t.Fatalf("error = %q, want actionable scope guidance", err.Error())
	}
}

func TestCredentialSafeIRCErrorLeavesOtherFailuresUnclassified(t *testing.T) {
	err := credentialSafeError(errors.New("x509: certificate signed by unknown authority"))
	if twitch.IsAuthError(err) {
		t.Fatal("a TLS trust failure was classified as an auth error")
	}
	if !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error = %q, want the real cause preserved in the message", err.Error())
	}
}

func TestCredentialSafeIRCErrorRedactsTokens(t *testing.T) {
	err := credentialSafeError(errors.New("dial failed with oauth:secret-token"))
	if strings.Contains(err.Error(), "oauth:secret-token") {
		t.Fatalf("error leaked the token: %q", err.Error())
	}
}
