package sendquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// AcquireConnection coordinates normal-account IRC joins and managed authentication attempts.
// Join permits are reserved at the upstream writer boundary, including automatic rejoin.
func AcquireConnection(ctx context.Context, account, kind string, count int) error {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return AcquireConnectionAt(ctx, filepath.Join(base, "twi", "connection-quota"), account, kind, count)
}
func AcquireConnectionAt(ctx context.Context, dir, account, kind string, count int) error {
	if count < 1 || count > 20 || (kind != "joins" && kind != "auth") {
		return errors.New("invalid IRC connection permit request")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return errors.New("connection quota directory must be private")
	}
	if err = checkAncestors(dir); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(account))
	path := filepath.Join(dir, hex.EncodeToString(hash[:])+"-"+kind)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		release, err := lock(ctx, path+".lock")
		if err != nil {
			return err
		}
		delay, err := reserveConnectionAt(path, count, time.Now().UTC())
		release()
		if err != nil {
			return err
		}
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func reserveConnectionAt(path string, count int, now time.Time) (time.Duration, error) {
	var permits []time.Time
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
			return 0, errors.New("invalid private connection quota journal")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		if err = json.Unmarshal(data, &permits); err != nil {
			return 0, errors.New("invalid connection quota journal")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	retained := permits[:0]
	for _, at := range permits {
		if now.Sub(at) < 10*time.Second {
			retained = append(retained, at)
		}
	}
	if len(retained)+count > 20 {
		return retained[len(retained)+count-21].Add(10 * time.Second).Sub(now), nil
	}
	for range count {
		retained = append(retained, now)
	}
	data, err := json.Marshal(retained)
	if err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "connection-")
	if err != nil {
		return 0, err
	}
	name := file.Name()
	defer os.Remove(name)
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return 0, err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	return 0, dir.Sync()
}
