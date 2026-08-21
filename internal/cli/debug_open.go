package cli

import (
	"errors"
	"os"
)

// openDebugLogFile creates the debug log, or reopens it for appending if it
// already exists, refusing to write through a symlink at the final path.
//
// The sequence is the same everywhere and is the security-relevant part, so
// it lives here once rather than being written out per platform: create
// exclusively so an attacker cannot have pre-placed the target, tighten the
// mode, and validate through the opened descriptor -- and if the file already
// existed, reopen for append and validate that descriptor too. Validating the
// open file rather than the path is what closes the gap between checking and
// opening.
//
// What differs between platforms is only how much of that a platform can
// enforce, which is supplied by debugLogNoFollowFlags and
// checkDebugLogPathBeforeReopen in the build-tagged files beside this one.
func openDebugLogFile(path string) (*os.File, error) {
	createFlags := os.O_CREATE | os.O_EXCL | os.O_APPEND | os.O_WRONLY
	file, err := os.OpenFile(path, createFlags|debugLogNoFollowFlags(), debugLogFileMode)
	if err == nil {
		if err := file.Chmod(debugLogFileMode); err != nil {
			return closeDebugLogFileWithError(file, debugLogOperationError("set permissions on", path, err))
		}
		if err := validateOpenedDebugLogFile(path, file); err != nil {
			return closeDebugLogFileWithError(file, err)
		}
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, debugLogOpenFileError(path, err)
	}

	// The log already exists, so reopen it for appending. On a platform
	// without a no-follow open flag this is where the path is inspected
	// first; where the flag exists the kernel enforces it and this is a
	// no-op.
	if err := checkDebugLogPathBeforeReopen(path); err != nil {
		return nil, err
	}
	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY|debugLogReopenFlags(), 0)
	if err != nil {
		return nil, debugLogOpenFileError(path, err)
	}
	if err := validateOpenedDebugLogFile(path, file); err != nil {
		return closeDebugLogFileWithError(file, err)
	}
	return file, nil
}
