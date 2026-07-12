//go:build !unix

package app

import "time"

// sampleProcessCPUTime has no portable stdlib-only implementation on
// non-Unix platforms (e.g. Windows); CPU% is shown as unavailable there.
func sampleProcessCPUTime() (time.Duration, bool) {
	return 0, false
}
