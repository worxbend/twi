package twitch

import (
	"errors"
	"strings"
	"testing"
)

func TestIsAuthErrorIgnoresNil(t *testing.T) {
	if IsAuthError(nil) {
		t.Fatal("IsAuthError(nil) = true, want false")
	}
}

// TestNewSafeErrorKeepsTheCauseReachable pins the three-part contract that
// makes NewSafeError worth having over errors.New(redacted).
//
// Transports must never print raw error text, because tokens appear in it. The
// obvious fix -- errors.New(redact(err.Error())) -- also throws the cause away,
// which leaves callers with nothing but a string to classify by and silently
// breaks every errors.Is check downstream. That was the original bug; these
// tests are what stop it coming back.
func TestNewSafeErrorKeepsTheCauseReachable(t *testing.T) {
	cause := errors.New("login failed for oauth:s3cr3t")
	err := NewSafeError("twitch IRC authentication failed", cause)

	if got := err.Error(); got != "twitch IRC authentication failed" {
		t.Errorf("Error() = %q, want the redacted detail", got)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("Error() = %q, leaked the cause's text", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is could not reach the cause; every downstream classification breaks")
	}
	if unwrapped := errors.Unwrap(err); unwrapped != cause {
		t.Errorf("Unwrap() = %v, want the original cause", unwrapped)
	}
}

// TestNewSafeErrorOnNilCauseReturnsNil keeps `return NewSafeError(detail, err)`
// safe to write on a path where err may be nil, which is how it is used.
func TestNewSafeErrorOnNilCauseReturnsNil(t *testing.T) {
	if err := NewSafeError("detail", nil); err != nil {
		t.Errorf("NewSafeError(detail, nil) = %v, want nil", err)
	}
}

// TestNewSafeErrorCarriesSentinelsThroughJoin covers the shape transports
// actually build: the cause is an errors.Join of a sentinel and the underlying
// failure, and both must stay reachable.
func TestNewSafeErrorCarriesSentinelsThroughJoin(t *testing.T) {
	underlying := errors.New("connection reset")
	err := NewSafeError("twitch IRC authentication failed", errors.Join(ErrAuthFailed, underlying))

	if !IsAuthError(err) {
		t.Error("IsAuthError = false; a joined ErrAuthFailed must survive the wrapper")
	}
	if !errors.Is(err, underlying) {
		t.Error("the underlying cause is no longer reachable through the wrapper")
	}
}

// TestZeroChannelAPIErrorIsPrintable covers the nil-tolerance documented on
// ChannelAPIError: its other fields are exported, so a struct literal can skip
// the constructor, and an error value that panics when printed is far worse
// than one that reports little.
func TestZeroChannelAPIErrorIsPrintable(t *testing.T) {
	var err ChannelAPIError
	if got := err.Error(); got == "" {
		t.Error("Error() on a zero ChannelAPIError returned an empty string")
	}
	if got := err.Unwrap(); got != nil {
		t.Errorf("Unwrap() on a zero ChannelAPIError = %v, want nil", got)
	}
	var nilErr *ChannelAPIError
	if got := nilErr.Error(); got == "" {
		t.Error("Error() on a nil *ChannelAPIError returned an empty string")
	}
}
