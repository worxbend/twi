//go:build !unix

package sendquota

import (
	"context"
	"errors"
	"os"
)

func lock(context.Context, string) (func(), error) {
	return nil, errors.New("cross-process Twitch quota locks unsupported on this platform")
}

func owned(os.FileInfo) bool { return true }

func leaseLockHeld(string) (bool, error) {
	return false, errors.New("channel lease locks unsupported on this platform")
}
