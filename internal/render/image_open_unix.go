//go:build unix

package render

import (
	"os"
	"syscall"
)

func openKittyImageFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
