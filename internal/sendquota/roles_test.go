package sendquota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRoleBucketsCountAllMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := range 20 {
		if wait, err := reserveRoleAt(path, fmt.Sprintf("normal%d", i), RoleNormal, 0, now); err != nil || wait != 0 {
			t.Fatalf("normal%d wait=%s err=%v", i, wait, err)
		}
	}
	if wait, err := reserveRoleAt(path, "other", RoleNormal, 0, now); err != nil || wait != 30*time.Second {
		t.Fatalf("normal bucket wait=%s err=%v", wait, err)
	}
	for i := range 80 {
		if wait, err := reserveRoleAt(path, "own", RoleBroadcaster, 0, now); err != nil || wait != 0 {
			t.Fatalf("privileged%d wait=%s err=%v", i, wait, err)
		}
	}
	for _, role := range []Role{RoleNormal, RoleModerator, RoleVIP, RoleBroadcaster} {
		if wait, err := reserveRoleAt(path, "new", role, 0, now); err != nil || wait != 30*time.Second {
			t.Fatalf("role=%s wait=%s err=%v", role, wait, err)
		}
	}
	if wait, err := reserveRoleAt(path, "own", RoleVIP, 0, now.Add(30*time.Second-time.Nanosecond)); err != nil || wait != time.Nanosecond {
		t.Fatalf("before reset wait=%s err=%v", wait, err)
	}
	if wait, err := reserveRoleAt(path, "own", RoleVIP, 0, now.Add(30*time.Second)); err != nil || wait != 0 {
		t.Fatalf("reset wait=%s err=%v", wait, err)
	}
}

func TestNormalBucketAlsoCountsPrivilegedPermits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	now := time.Now().UTC()
	for range 20 {
		if wait, err := reserveRoleAt(path, "own", RoleModerator, 0, now); err != nil || wait != 0 {
			t.Fatal(wait, err)
		}
	}
	if wait, err := reserveRoleAt(path, "normal", RoleNormal, 0, now); err != nil || wait != 30*time.Second {
		t.Fatalf("normal ignored privilege permits: %s %v", wait, err)
	}
	if wait, err := reserveRoleAt(path, "own", RoleModerator, 0, now); err != nil || wait != 0 {
		t.Fatal(wait, err)
	}
}

func TestRoleChannelPacingAndExplicitIntervals(t *testing.T) {
	now := time.Now().UTC()
	for _, role := range []Role{RoleBroadcaster, RoleModerator, RoleVIP} {
		t.Run(string(role), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "permits")
			if wait, err := reserveRoleAt(path, "alpha", RoleNormal, 0, now); err != nil || wait != 0 {
				t.Fatal(wait, err)
			}
			if wait, err := reserveRoleAt(path, "alpha", role, 0, now); err != nil || wait != 0 {
				t.Fatalf("privilege not exempt: %s %v", wait, err)
			}
			if wait, err := reserveRoleAt(path, "alpha", RoleNormal, 0, now.Add(time.Second-time.Nanosecond)); err != nil || wait != time.Nanosecond {
				t.Fatalf("normal exemption leaked: %s %v", wait, err)
			}
			if wait, err := reserveRoleAt(path, "alpha", role, 5*time.Second, now.Add(time.Second)); err != nil || wait != 4*time.Second {
				t.Fatalf("caller interval relaxed: %s %v", wait, err)
			}
			if wait, err := reserveRoleAt(path, "alpha", role, 2*time.Second, now.Add(2*time.Second)); err != nil || wait != 0 {
				t.Fatal(wait, err)
			}
			if wait, err := reserveRoleAt(path, "alpha", RoleBroadcaster, 0, now.Add(3*time.Second)); err != nil || wait != time.Second {
				t.Fatalf("prior caller interval lost: %s %v", wait, err)
			}
		})
	}
}

func TestLegacyJournalCooldownAndUnknownRoleFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	now := time.Now().UTC()
	data, err := json.Marshal(journal{Permits: []permit{{At: now, Channel: "alpha", Until: now.Add(5 * time.Second)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if wait, err := reserveRoleAt(path, "alpha", RoleVIP, 0, now); err != nil || wait != 5*time.Second {
		t.Fatalf("legacy cooldown lost %s %v", wait, err)
	}
	if _, err := reserveRoleAt(path, "beta", Role("claimed-admin"), 0, now); err == nil {
		t.Fatal("unknown role accepted")
	}
	if _, err := reserveRoleAt(path, "beta", RoleVIP, -time.Second, now); err == nil {
		t.Fatal("negative interval accepted")
	}
}

func TestMixedRoleProcessHelper(t *testing.T) {
	dir := os.Getenv("TWI_ROLE_QUOTA_DIR")
	if dir == "" {
		return
	}
	for i := range 80 {
		if err := AcquireRoleAt(context.Background(), dir, "mixed-account", "own", RoleVIP, true, 0); err != nil {
			t.Fatalf("privileged%d: %v", i, err)
		}
	}
	if err := AcquireRoleAt(context.Background(), dir, "mixed-account", "own", RoleVIP, true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("privileged overflow: %v", err)
	}
	if err := AcquireAt(context.Background(), dir, "mixed-account", "normal", true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("normal overflow: %v", err)
	}
}
func TestMixedRoleReservationsRemainSharedAfterProcessExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota")
	for i := range 20 {
		if err := AcquireAt(context.Background(), dir, "mixed-account", fmt.Sprintf("channel%d", i), true, 0); err != nil {
			t.Fatal(err)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestMixedRoleProcessHelper$")
	child.Env = append(os.Environ(), "TWI_ROLE_QUOTA_DIR="+dir)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child %v: %s", err, output)
	}
	if err := AcquireRoleAt(context.Background(), dir, "mixed-account", "own", RoleBroadcaster, true, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("exited child permits lost: %v", err)
	}
	if err := AcquireAt(context.Background(), dir, "separate-account", "normal", true, 0); err != nil {
		t.Fatal(err)
	}
}

func TestMixedBucketWaitUsesRequiredExpiryCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permits")
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		if wait, err := reserveRoleAt(path, "own", RoleBroadcaster, 0, start.Add(time.Duration(i)*100*time.Millisecond)); err != nil || wait != 0 {
			t.Fatal(wait, err)
		}
	}
	now := start.Add(10 * time.Second)
	if wait, err := reserveRoleAt(path, "normal", RoleNormal, 0, now); err != nil || wait != 28*time.Second {
		t.Fatalf("normal must await81 expiries: %s %v", wait, err)
	}
	if wait, err := reserveRoleAt(path, "own", RoleVIP, 0, now); err != nil || wait != 20*time.Second {
		t.Fatalf("privileged must await1 expiry: %s %v", wait, err)
	}
}
