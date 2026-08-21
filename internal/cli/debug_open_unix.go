//go:build unix

package cli

import (
	"errors"
	"syscall"
)

const (
	debugLogOpenUsesNoFollow = true
	debugLogOpenPlatformNote = "Unix debug log files are opened with O_NOFOLLOW on the final path and validated through the opened file descriptor."
)

// debugLogNoFollowFlags makes the kernel refuse the open outright if the final
// path component is a symlink, which is the guarantee this platform can give.
func debugLogNoFollowFlags() int { return syscall.O_NOFOLLOW }

// debugLogReopenFlags adds O_NONBLOCK on top, so reopening a path that turned
// out to be a FIFO returns rather than blocking forever waiting for a reader.
func debugLogReopenFlags() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }

// checkDebugLogPathBeforeReopen does nothing here: O_NOFOLLOW already refuses
// a symlink at open time, which is stronger than any check this could make
// beforehand, because nothing can change between the check and the open.
func checkDebugLogPathBeforeReopen(string) error { return nil }

func debugLogOpenErrorIsSymlink(err error) bool {
	return errors.Is(err, syscall.ELOOP)
}
