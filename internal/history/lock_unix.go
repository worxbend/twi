//go:build unix

package history

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
)

func lockWriter(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, ErrWriterBusy
	}
	return f, nil
}
func unlockWriter(f *os.File) error {
	e := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	c := f.Close()
	if e != nil {
		return e
	}
	return c
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(stat.Uid) == os.Geteuid()
}

func reserveDisk(path string) error {
	var stat unix.Statfs_t
	if e := unix.Statfs(filepath.Dir(path), &stat); e != nil {
		return e
	}
	if uint64(stat.Bavail)*uint64(stat.Bsize) < 4*1024*1024 {
		return fmt.Errorf("%w: less than 4 MiB free maintenance reserve", ErrStorageFull)
	}
	return nil
}
