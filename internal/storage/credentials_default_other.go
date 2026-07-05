//go:build !unix

package storage

import "fmt"

// NewDefaultCredentialStore reports that this platform has no supported
// saved-credential backend.
func NewDefaultCredentialStore() (CredentialStore, error) {
	return nil, fmt.Errorf("%w: saved credential persistence is disabled on this platform; use environment variables or a private flat config file for Twitch credentials", ErrUnsupportedCredentialFilePlatform)
}
