//go:build unix

package storage

import (
	"os"
	"syscall"
)

func credentialFilePlatformPolicy() credentialFilePlatform {
	return credentialFilePlatform{Supported: true}
}

func openCredentialFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
