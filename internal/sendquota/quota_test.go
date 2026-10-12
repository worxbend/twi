package sendquota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSharedChannelPermitAndCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	ctx := context.Background()
	if err := AcquireAt(ctx, dir, "user", "alpha", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := AcquireAt(ctx, dir, "user", "alpha", true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("second permit %v", err)
	}
	if err := AcquireAt(ctx, dir, "user", "beta", true, 0); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := AcquireAt(deadline, dir, "user", "alpha", false, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation %v", err)
	}
}
func TestConcurrentReservationsShareLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	var group sync.WaitGroup
	success := make(chan bool, 10)
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			success <- AcquireAt(context.Background(), dir, "same-account", "alpha", true, 0) == nil
		}()
	}
	group.Wait()
	close(success)
	count := 0
	for ok := range success {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reserved %d concurrent permits", count)
	}
}

func TestQuotaGlobalWindowAndIndependentAccounts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	for i := range 20 {
		if err := AcquireAt(context.Background(), dir, "account", fmt.Sprintf("channel%d", i), true, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := AcquireAt(context.Background(), dir, "account", "other", true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("global permit: %v", err)
	}
	if err := AcquireAt(context.Background(), dir, "different-account", "other", true, 0); err != nil {
		t.Fatal(err)
	}
}
func TestQuotaProcessHelper(t *testing.T) {
	path := os.Getenv("TWI_QUOTA_TEST_DIR")
	if path == "" {
		return
	}
	if err := AcquireAt(context.Background(), path, "account", "alpha", true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("child must observe reservation: %v", err)
	}
}
func TestQuotaReservationsSpanProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	if err := AcquireAt(context.Background(), dir, "account", "alpha", true, 0); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestQuotaProcessHelper$")
	command.Env = append(os.Environ(), "TWI_QUOTA_TEST_DIR="+dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child %v: %s", err, output)
	}
}

func TestQuotaDeterministicWindowBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := range 20 {
		if delay, err := reserveAt(path, fmt.Sprintf("channel%d", i), time.Second, start); err != nil || delay != 0 {
			t.Fatalf("reserve %d delay %s err %v", i, delay, err)
		}
	}
	if delay, err := reserveAt(path, "new-channel", time.Second, start.Add(30*time.Second-time.Nanosecond)); err != nil || delay != time.Nanosecond {
		t.Fatalf("window before boundary delay %s err %v", delay, err)
	}
	if delay, err := reserveAt(path, "new-channel", time.Second, start.Add(30*time.Second)); err != nil || delay != 0 {
		t.Fatalf("window boundary delay %s err %v", delay, err)
	}
	if delay, err := reserveAt(path, "new-channel", time.Second, start.Add(31*time.Second-time.Nanosecond)); err != nil || delay != time.Nanosecond {
		t.Fatalf("channel before boundary delay %s err %v", delay, err)
	}
	if delay, err := reserveAt(path, "new-channel", time.Second, start.Add(31*time.Second)); err != nil || delay != 0 {
		t.Fatalf("channel boundary delay %s err %v", delay, err)
	}
}
