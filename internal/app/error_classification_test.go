package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

// TestCredentialSafeDetailDoesNotMisreadTLSErrors is the regression this
// change exists for. Classification used to be strings.Contains(err, "auth"),
// so "x509: certificate signed by unknown authority" matched on "authority"
// and was reported as a rejected OAuth token -- sending anyone behind a
// corporate TLS proxy off to re-run a login that could never have helped.
func TestCredentialSafeDetailDoesNotMisreadTLSErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("x509: certificate signed by unknown authority"),
		x509.UnknownAuthorityError{},
		errors.New("dial tcp: lookup irc.chat.twitch.tv: no such host"),
		errors.New("read tcp: connection reset by peer"),
		errors.New("i/o timeout"),
	} {
		detail := credentialSafeDetail(err)
		if strings.Contains(detail, "verify username, OAuth token") {
			t.Errorf("credentialSafeDetail(%v) = %q, want the real error, not credential guidance", err, detail)
		}
	}
}

func TestCredentialSafeDetailReportsRealAuthFailures(t *testing.T) {
	err := errors.Join(twitch.ErrAuthFailed, errors.New("login authentication failed"))
	detail := credentialSafeDetail(err)
	for _, want := range []string{"OAuth token", "chat:read"} {
		if !strings.Contains(detail, want) {
			t.Errorf("credentialSafeDetail = %q, want it to mention %q", detail, want)
		}
	}
}

// TestCredentialSafeErrorPreservesTheChain covers the second half of the bug:
// replacing an error with errors.New(redacted) kept credentials out of the
// display but discarded the cause, so every errors.Is check downstream
// silently stopped matching. The DeadlineExceeded branch in Reconnect could
// not fire for exactly this reason.
func TestCredentialSafeErrorPreservesTheChain(t *testing.T) {
	for _, sentinel := range []error{context.DeadlineExceeded, context.Canceled, twitch.ErrAuthFailed} {
		wrapped := credentialSafeError(fmt.Errorf("connect: %w", sentinel))
		if !errors.Is(wrapped, sentinel) {
			t.Errorf("credentialSafeError lost %v from the chain", sentinel)
		}
	}
}

// TestCredentialSafeErrorStillRedacts keeps the property the flattening was
// there to provide in the first place.
func TestCredentialSafeErrorStillRedacts(t *testing.T) {
	wrapped := credentialSafeError(errors.New("login failed for oauth:secret-token"))
	if strings.Contains(wrapped.Error(), "oauth:secret-token") {
		t.Fatalf("error leaked the token: %q", wrapped.Error())
	}
}

// TestStateFromNoticeKeepsHealthyConnectionsConnected pins the user-visible
// consequence: an ordinary notice must not turn the status bar red.
//
// Which notices mean "the credentials were rejected" is decided by the
// transport and arrives on Notice.AuthFailed; the tests for that live in
// internal/twitch/irc, where the wire text is known. This asserts what the app
// does with the answer.
func TestStateFromNoticeKeepsHealthyConnectionsConnected(t *testing.T) {
	state := stateFromNotice(twitch.Notice{
		Channel: "example",
		ID:      "no_permission",
		Text:    "You don't have permission to perform that action.",
	})
	if state.Status != ConnectionConnected {
		t.Fatalf("status = %q, want connected for an ordinary notice", state.Status)
	}
	if !strings.Contains(state.Detail, "permission") {
		t.Fatalf("detail = %q, want the notice text preserved", state.Detail)
	}
}

// TestStateFromNoticeReportsAuthFailure is the other half: when the transport
// says the credentials were rejected, the app must surface it as a failure
// with guidance rather than as an ordinary notice.
func TestStateFromNoticeReportsAuthFailure(t *testing.T) {
	state := stateFromNotice(twitch.Notice{
		Channel:    "*",
		Text:       "Login authentication failed",
		AuthFailed: true,
	})
	if state.Status != ConnectionFailed {
		t.Fatalf("status = %q, want failed for a rejected login", state.Status)
	}
	if !strings.Contains(state.Detail, "chat:read") {
		t.Errorf("detail = %q, want actionable scope guidance", state.Detail)
	}
}
