// Package sendquota coordinates conservative Twitch permits across local processes.
package sendquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var ErrLimited = errors.New("local Twitch send quota exhausted")

// Role is a destination-specific privilege proven by Twitch identity or own
// USERSTATE. Callers must never derive it from a message author or user flag.
type Role string

const (
	RoleNormal      Role = "normal"
	RoleBroadcaster Role = "broadcaster"
	RoleModerator   Role = "moderator"
	RoleVIP         Role = "vip"
)

func (r Role) Privileged() bool { return r == RoleBroadcaster || r == RoleModerator || r == RoleVIP }
func (r Role) valid() bool      { return r == RoleNormal || r.Privileged() }

type permit struct {
	Role          Role      `json:"role,omitempty"`
	ExplicitUntil time.Time `json:"explicit_until,omitempty"`
	At            time.Time `json:"at"`
	Channel       string    `json:"channel"`
	Until         time.Time `json:"until"`
}
type journal struct {
	Permits []permit `json:"permits"`
}

// Acquire reserves before sending; crashes consume a permit until its window expires.
func Acquire(ctx context.Context, account, channel string, fail bool, interval time.Duration) error {
	return AcquireForRole(ctx, account, channel, RoleNormal, fail, interval)
}

// AcquireForRole applies both shared account buckets. Every reservation counts
// against the normal 20/30s bucket; proven privileged destinations may continue
// up to 100/30s and are exempt from the platform's per-channel one-second pace.
// Explicit caller intervals are retained across processes and never relaxed.
func AcquireForRole(ctx context.Context, account, channel string, role Role, fail bool, interval time.Duration) error {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return AcquireRoleAt(ctx, filepath.Join(base, "twi", "send-quota"), account, channel, role, fail, interval)
}
func AcquireAt(ctx context.Context, dir, account, channel string, fail bool, interval time.Duration) error {
	return AcquireRoleAt(ctx, dir, account, channel, RoleNormal, fail, interval)
}
func AcquireRoleAt(ctx context.Context, dir, account, channel string, role Role, fail bool, interval time.Duration) error {
	if !role.valid() {
		return errors.New("invalid Twitch send role")
	}
	if interval < 0 {
		return errors.New("send interval must not be negative")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return errors.New("quota directory must be private and not a symlink")
	}
	if err = checkAncestors(dir); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(account))
	path := filepath.Join(dir, hex.EncodeToString(hash[:]))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		release, err := lock(ctx, path+".lock")
		if err != nil {
			return err
		}
		wait, err := reserveRoleAt(path, channel, role, interval, time.Now().UTC())
		release()
		if err != nil {
			return err
		}
		if wait <= 0 {
			return nil
		}
		if fail {
			return ErrLimited
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func reserveAt(path, channel string, interval time.Duration, now time.Time) (time.Duration, error) {
	return reserveRoleAt(path, channel, RoleNormal, interval, now)
}
func reserveRoleAt(path, channel string, role Role, interval time.Duration, now time.Time) (time.Duration, error) {
	if !role.valid() {
		return 0, errors.New("invalid Twitch send role")
	}
	if interval < 0 {
		return 0, errors.New("send interval must not be negative")
	}
	effectiveInterval := interval
	if !role.Privileged() && effectiveInterval < time.Second {
		effectiveInterval = time.Second
	}
	var data journal
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0077 != 0 {
			return 0, errors.New("quota journal must be private regular file")
		}
		if info.Size() > 65536 {
			return 0, errors.New("quota journal oversized")
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		if err = json.Unmarshal(bytes, &data); err != nil {
			return 0, errors.New("invalid quota journal")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	retained := data.Permits[:0]
	var wait time.Duration
	global := make([]time.Time, 0, 100)
	for _, p := range data.Permits {
		age := now.Sub(p.At)
		if age < 30*time.Second {
			global = append(global, p.At)
		}
		if age < 30*time.Second || p.Until.After(now) {
			retained = append(retained, p)
		}
		if p.Channel == channel {
			if effectiveInterval-age > wait {
				wait = effectiveInterval - age
			}
			until := p.ExplicitUntil
			// Legacy journals lack role/explicit interval fields: retain their recorded
			// cooldown conservatively rather than discarding an unknown user interval.
			if p.Role == "" || !role.Privileged() {
				if p.Until.After(until) {
					until = p.Until
				}
			}
			if delay := until.Sub(now); delay > wait {
				wait = delay
			}
		}
	}
	data.Permits = retained
	limit := 20
	if role.Privileged() {
		limit = 100
	}
	if len(global) >= limit {
		sort.Slice(global, func(i, j int) bool { return global[i].Before(global[j]) })
		if delay := 30*time.Second - now.Sub(global[len(global)-limit]); delay > wait {
			wait = delay
		}
	}
	if wait > 0 {
		return wait, nil
	}
	explicitUntil := time.Time{}
	if interval > 0 {
		explicitUntil = now.Add(interval)
	}
	data.Permits = append(data.Permits, permit{At: now, Channel: channel, Until: now.Add(effectiveInterval), Role: role, ExplicitUntil: explicitUntil})
	bytes, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	if len(bytes) > 65536 {
		return 0, errors.New("quota journal capacity exceeded")
	}
	file, err := os.CreateTemp(filepath.Dir(path), "permit-")
	if err != nil {
		return 0, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(bytes); err == nil {
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

// WithAccountLock serializes a short account operation across local processes.
// Callers use a separate namespace from permits for credential refresh coordination.
func WithAccountLock(ctx context.Context, namespace, account string, action func() error) error {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "twi", namespace)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return errors.New("account lock directory must be private and not a symlink")
	}
	if err = checkAncestors(dir); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(account))
	release, err := lock(ctx, filepath.Join(dir, hex.EncodeToString(hash[:])+".lock"))
	if err != nil {
		return err
	}
	defer release()
	return action()
}

func checkAncestors(path string) error {
	for directory := path; ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("account state directory must not contain symlinks")
		}
		if directory == filepath.Dir(directory) {
			break
		}
	}
	return nil
}
