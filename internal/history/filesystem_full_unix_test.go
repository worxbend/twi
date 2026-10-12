//go:build unix

package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Run only inside an isolated, size-limited filesystem. Never fill a host path.
// scripts/history-disk-full-smoke.sh supplies a private 8 MiB container tmpfs.
func TestIsolatedFilesystemFull(t *testing.T) {
	base := os.Getenv("TWI_ISOLATED_FULL_FILESYSTEM")
	if base == "" {
		t.Skip("requires isolated limited filesystem")
	}
	if base != "/state" {
		t.Fatal("disk-full fixture must use isolated /state mount")
	}
	path := filepath.Join(base, "private", "history.db")
	s, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC()
	if err = s.BeginCapture(ctx, "alpha", "full-filesystem", "irc", at); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Append(ctx, message("alpha", "before-full", at)); err != nil {
		t.Fatal(err)
	}
	filler := filepath.Join(base, "filler")
	f, err := os.Create(filler)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filler)
	chunk := make([]byte, 256*1024)
	var wrote int
	for wrote < 16*1024*1024 {
		n, e := f.Write(chunk)
		wrote += n
		if e != nil {
			err = e
			break
		}
	}
	f.Close()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("isolated fixture did not produce ENOSPC: bytes=%d error=%v", wrote, err)
	}
	if _, _, err = s.Append(ctx, message("alpha", "during-full", at)); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("full filesystem write: %v", err)
	}
	if err = os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	high, err := s.HighWater(ctx)
	if err != nil || high != 1 {
		t.Fatal("exhausted write persisted", high, err)
	}
	if err = s.EndCapture(ctx, "full-filesystem", time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated tmpfs reached ENOSPC after %d filler bytes; capture write stopped, committed history preserved", wrote)
}
