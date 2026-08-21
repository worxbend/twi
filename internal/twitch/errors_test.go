package twitch

import (
	"testing"
)

func TestIsAuthErrorIgnoresNil(t *testing.T) {
	if IsAuthError(nil) {
		t.Fatal("IsAuthError(nil) = true, want false")
	}
}
