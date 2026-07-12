//go:build unix

package app

import (
	"syscall"
	"time"
)

// sampleProcessCPUTime returns twi's own total (user+system) CPU time via
// getrusage(RUSAGE_SELF), the standard way to self-report process CPU
// consumption on Unix without a third-party dependency.
func sampleProcessCPUTime() (time.Duration, bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	user := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond
	sys := time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	return user + sys, true
}
