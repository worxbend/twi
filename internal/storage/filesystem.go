package storage

import (
	"errors"
	"os"
)

// ErrPathIsDirectory reports that a path expected to be a file is a directory.
var ErrPathIsDirectory = errors.New("path is a directory")

// CheckReadableFile verifies that path names an existing readable file.
func CheckReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ErrPathIsDirectory
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

// ProbeWritableDir verifies that dir exists and accepts a short private file
// write. The probe file is removed before the function returns.
func ProbeWritableDir(dir string) error {
	if err := os.MkdirAll(dir, CredentialDirectoryMode); err != nil {
		return err
	}

	file, err := os.CreateTemp(dir, ".twi-doctor-write-test-*")
	if err != nil {
		return err
	}
	probePath := file.Name()
	if _, err := file.Write([]byte("ok\n")); err != nil {
		_ = file.Close()
		_ = os.Remove(probePath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	return os.Remove(probePath)
}
