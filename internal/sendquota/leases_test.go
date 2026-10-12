package sendquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func leaseChannels(prefix string, count int) []string {
	channels := make([]string, count)
	for i := range channels {
		channels[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return channels
}
func TestChannelLeasesShareDistinctAccountRooms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leases")
	ctx := context.Background()
	first, err := ReserveChannelsAt(ctx, dir, "account", leaseChannels("alpha", 70))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := ReserveChannelsAt(ctx, dir, "account", leaseChannels("beta", 30))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	duplicate, err := ReserveChannelsAt(ctx, dir, "account", []string{"alpha0"})
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	if err = first.Add(ctx, []string{"new"}); !errors.Is(err, ErrChannelLimit) {
		t.Fatalf("101st room: %v", err)
	}
	if err = second.Remove(ctx, "beta29"); err != nil {
		t.Fatal(err)
	}
	if err = first.Add(ctx, []string{"new"}); err != nil {
		t.Fatalf("depart did not release room: %v", err)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Add(ctx, leaseChannels("gamma", 29)); err != nil {
		t.Fatalf("closed reader did not release rooms: %v", err)
	}
	if err = first.Add(ctx, []string{"over"}); !errors.Is(err, ErrChannelLimit) {
		t.Fatalf("account room cap %v", err)
	}
}
func TestLiveLeaseCannotExpireWhenHeartbeatIsStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leases")
	ctx := context.Background()
	lease, err := ReserveChannelsAt(ctx, dir, "account", leaseChannels("room", 100))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	journal, err := readLeaseJournal(lease.path)
	if err != nil {
		t.Fatal(err)
	}
	journal.Leases[0].Heartbeat = time.Now().Add(-time.Minute)
	if err = writeLeaseJournal(lease.path, journal); err != nil {
		t.Fatal(err)
	}
	if extra, err := ReserveChannelsAt(ctx, dir, "account", []string{"extra"}); !errors.Is(err, ErrChannelLimit) {
		if extra != nil {
			extra.Close()
		}
		t.Fatalf("live stale heartbeat released account slots: %v", err)
	}
}
func TestDeadChannelLeaseKeepsConservativeGrace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leases")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("account"))
	path := filepath.Join(dir, hex.EncodeToString(hash[:]))
	id := strings.Repeat("a", 32)
	for _, test := range []struct {
		age  time.Duration
		want error
	}{{29 * time.Second, ErrChannelLimit}, {31 * time.Second, nil}} {
		journal := channelLeaseJournal{Leases: []channelLeaseRecord{{ID: id, PID: 999999, Channels: leaseChannels("room", 100), Heartbeat: time.Now().Add(-test.age)}}}
		if err := writeLeaseJournal(path, journal); err != nil {
			t.Fatal(err)
		}
		lease, err := ReserveChannelsAt(context.Background(), dir, "account", []string{"extra"})
		if !errors.Is(err, test.want) {
			t.Fatalf("age %s error %v", test.age, err)
		}
		if lease != nil {
			if lease.ReclaimedCount() != 1 {
				t.Fatal("stale reclaim not reported")
			}
			lease.Close()
		}
	}
}
func TestChannelLeaseProcessHelper(t *testing.T) {
	dir := os.Getenv("TWI_CHANNEL_LEASE_TEST_DIR")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if lease, err := ReserveChannelsAt(ctx, dir, "account", []string{"extra"}); !errors.Is(err, ErrChannelLimit) {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("child ignored active parent rooms: %v", err)
	}
}
func TestChannelLeasesSpanProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leases")
	lease, err := ReserveChannelsAt(context.Background(), dir, "account", leaseChannels("room", 100))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestChannelLeaseProcessHelper$")
	child.Env = append(os.Environ(), "TWI_CHANNEL_LEASE_TEST_DIR="+dir)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child %v: %s", err, output)
	}
}
