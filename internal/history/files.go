package history

import (
	"errors"
	"os"
	"path/filepath"
)

func checkFile(path string, required bool) error {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) && !required {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return errors.New("history path must be a regular file, not a symlink")
	}
	if !owned(info) {
		return errors.New("history file must belong to current user")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("history file permissions must be private (0600)")
	}
	return nil
}
func privateDirectory(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	return validateDirectory(path)
}
func validateDirectory(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 || !i.IsDir() {
			return errors.New("history directory must not contain symlinks")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	i, e := os.Stat(path)
	if e != nil {
		return e
	}
	if !owned(i) {
		return errors.New("history directory must belong to current user")
	}
	if i.Mode().Perm()&0077 != 0 {
		return errors.New("history directory permissions must be private (0700)")
	}
	return nil
}
