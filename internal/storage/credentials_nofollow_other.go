//go:build !unix

package storage

import "os"

func credentialFilePlatformPolicy() credentialFilePlatform {
	return credentialFilePlatform{
		Supported: false,
		Reason:    "credential-file fallback is disabled on non-Unix builds because exact owner-only ACL semantics and reparse-point/no-follow protections are not implemented",
		Action:    "use environment variables or a private flat config file for Twitch credentials on this platform",
	}
}

func openCredentialFileNoFollow(path string) (*os.File, error) {
	return nil, credentialFilePlatformPolicy().validate()
}
