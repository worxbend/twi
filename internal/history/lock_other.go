//go:build !unix

package history

import (
	"errors"
	"os"
)

func lockWriter(string) (*os.File, error) {
	return nil, errors.New("history persistence requires supported Unix locking")
}
func unlockWriter(f *os.File) error { return f.Close() }

func owned(os.FileInfo) bool { return false }

func reserveDisk(string) error {
	return errors.New("history persistence requires supported Unix filesystem")
}
