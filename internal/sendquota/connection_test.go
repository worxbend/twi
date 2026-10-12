package sendquota

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectionQuotaWindowBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if delay, err := reserveConnectionAt(path, 20, start); err != nil || delay != 0 {
		t.Fatalf("initial delay %s err %v", delay, err)
	}
	if delay, err := reserveConnectionAt(path, 1, start.Add(10*time.Second-time.Nanosecond)); err != nil || delay != time.Nanosecond {
		t.Fatalf("before boundary delay %s err %v", delay, err)
	}
	if delay, err := reserveConnectionAt(path, 20, start.Add(10*time.Second)); err != nil || delay != 0 {
		t.Fatalf("boundary delay %s err %v", delay, err)
	}
}
func TestConnectionQuotaKindsRemainIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	if err := AcquireConnectionAt(context.Background(), dir, "account", "joins", 20); err != nil {
		t.Fatal(err)
	}
	if err := AcquireConnectionAt(context.Background(), dir, "account", "auth", 20); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := AcquireConnectionAt(deadline, dir, "account", "joins", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked join cancellation: %v", err)
	}
}
func TestConnectionQuotaProcessHelper(t *testing.T) {
	dir := os.Getenv("TWI_CONNECTION_QUOTA_TEST_DIR")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := AcquireConnectionAt(ctx, dir, "account", "joins", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("child escaped parent reservations: %v", err)
	}
}
func TestConnectionQuotaSharesProcessReservations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	if err := AcquireConnectionAt(context.Background(), dir, "account", "joins", 20); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestConnectionQuotaProcessHelper$")
	child.Env = append(os.Environ(), "TWI_CONNECTION_QUOTA_TEST_DIR="+dir)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child %v %s", err, output)
	}
}
