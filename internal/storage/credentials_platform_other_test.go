//go:build !unix

package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialFileFallbackDisabledOnNonUnixBuilds(t *testing.T) {
	plan, err := NewCredentialFilePlan(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatalf("NewCredentialFilePlan returned error: %v", err)
	}

	if _, err := NewCredentialFileStore(plan); !errors.Is(err, ErrUnsupportedCredentialFilePlatform) {
		t.Fatalf("NewCredentialFileStore non-Unix error = %v, want ErrUnsupportedCredentialFilePlatform", err)
	} else {
		for _, want := range []string{"disabled on non-Unix builds", "environment variables", "private flat config file"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("non-Unix error missing %q: %v", want, err)
			}
		}
	}

	if _, err := openCredentialFileNoFollow(plan.Path); !errors.Is(err, ErrUnsupportedCredentialFilePlatform) {
		t.Fatalf("openCredentialFileNoFollow non-Unix error = %v, want ErrUnsupportedCredentialFilePlatform", err)
	}
}
