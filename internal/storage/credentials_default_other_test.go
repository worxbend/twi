//go:build !unix

package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestNewDefaultCredentialStoreUnsupportedOnOtherNonUnixBuilds(t *testing.T) {
	store, err := NewDefaultCredentialStore()
	if store != nil {
		t.Fatalf("NewDefaultCredentialStore returned store %T, want nil", store)
	}
	if !errors.Is(err, ErrUnsupportedCredentialFilePlatform) {
		t.Fatalf("NewDefaultCredentialStore error = %v, want ErrUnsupportedCredentialFilePlatform", err)
	}
	for _, want := range []string{"saved credential persistence is disabled", "environment variables", "private flat config file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unsupported default-store error missing %q: %v", want, err)
		}
	}
}
