package sendquota

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrChannelLimit = errors.New("account concurrent channel limit exceeded")
var ErrLeaseClosed = errors.New("channel lease closed")

const leaseStaleAfter = 30 * time.Second

type channelLeaseRecord struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Channels  []string  `json:"channels"`
	Heartbeat time.Time `json:"heartbeat"`
}
type channelLeaseJournal struct {
	Leases []channelLeaseRecord `json:"leases"`
}

// ChannelLease reserves distinct account rooms across both IRC and EventSub readers.
// Dead process reservations survive for 30s. Live OS locks prevent false expiry
// during a paused process or a slow heartbeat, even when its timestamp is stale.
type ChannelLease struct {
	mu        sync.Mutex
	path, id  string
	channels  []string
	release   func()
	cancel    context.CancelFunc
	done      chan struct{}
	errors    chan error
	closed    bool
	reclaimed int
}

func ReserveChannels(ctx context.Context, account string, channels []string) (*ChannelLease, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return ReserveChannelsAt(ctx, filepath.Join(base, "twi", "channel-leases"), account, channels)
}
func ReserveChannelsAt(ctx context.Context, dir, account string, channels []string) (*ChannelLease, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("channel lease directory must be private")
	}
	if err = checkAncestors(dir); err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(nonce)
	hash := sha256.Sum256([]byte(strings.ToLower(account)))
	path := filepath.Join(dir, hex.EncodeToString(hash[:]))
	release, err := lock(ctx, path+"-"+id+".lease")
	if err != nil {
		return nil, err
	}
	lease := &ChannelLease{path: path, id: id, release: release, errors: make(chan error, 1), done: make(chan struct{})}
	if err = lease.update(ctx, channels, false); err != nil {
		release()
		_ = os.Remove(path + "-" + id + ".lease")
		return nil, err
	}
	heartbeat, cancel := context.WithCancel(context.Background())
	lease.cancel = cancel
	go lease.heartbeat(heartbeat)
	return lease, nil
}
func (l *ChannelLease) Errors() <-chan error { return l.errors }
func (l *ChannelLease) Add(ctx context.Context, channels []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrLeaseClosed
	}
	next := append([]string(nil), l.channels...)
	next = append(next, channels...)
	return l.update(ctx, next, false)
}
func (l *ChannelLease) Remove(ctx context.Context, channel string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrLeaseClosed
	}
	next := make([]string, 0, len(l.channels))
	for _, existing := range l.channels {
		if existing != channel {
			next = append(next, existing)
		}
	}
	return l.update(ctx, next, false)
}
func (l *ChannelLease) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	cancel := l.cancel
	l.mu.Unlock()
	if cancel != nil {
		cancel()
		<-l.done
	}
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	l.mu.Lock()
	err := l.update(ctx, nil, true)
	l.release()
	_ = os.Remove(l.path + "-" + l.id + ".lease")
	l.mu.Unlock()
	return err
}
func (l *ChannelLease) heartbeat(ctx context.Context) {
	defer close(l.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			if l.closed {
				l.mu.Unlock()
				return
			}
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := l.update(bounded, l.channels, false)
			cancel()
			l.mu.Unlock()
			if err != nil {
				select {
				case l.errors <- err:
				default:
				}
				return
			}
		}
	}
}
func (l *ChannelLease) update(ctx context.Context, channels []string, remove bool) error {
	release, err := lock(ctx, l.path+".lock")
	if err != nil {
		return err
	}
	defer release()
	journal, err := readLeaseJournal(l.path)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	kept := journal.Leases[:0]
	for _, record := range journal.Leases {
		if record.ID == l.id {
			continue
		}
		if now.Sub(record.Heartbeat) >= leaseStaleAfter {
			held, err := leaseLockHeld(l.path + "-" + record.ID + ".lease")
			if err != nil {
				return err
			}
			if !held {
				l.reclaimed++
				_ = os.Remove(l.path + "-" + record.ID + ".lease")
				continue
			}
		}
		kept = append(kept, record)
	}
	canonical := make([]string, 0, len(channels))
	seen := map[string]bool{}
	for _, channel := range channels {
		channel = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(channel), "#"))
		if channel != "" && !seen[channel] {
			seen[channel] = true
			canonical = append(canonical, channel)
		}
	}
	if !remove {
		kept = append(kept, channelLeaseRecord{ID: l.id, PID: os.Getpid(), Channels: canonical, Heartbeat: now})
	}
	rooms := map[string]bool{}
	for _, record := range kept {
		for _, channel := range record.Channels {
			rooms[channel] = true
		}
	}
	if len(rooms) > 100 {
		return ErrChannelLimit
	}
	if len(kept) > 1000 {
		return errors.New("channel lease instance capacity exceeded")
	}
	if err = writeLeaseJournal(l.path, channelLeaseJournal{Leases: kept}); err != nil {
		return err
	}
	l.channels = canonical
	return nil
}
func readLeaseJournal(path string) (channelLeaseJournal, error) {
	var journal channelLeaseJournal
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return journal, err
	}
	if !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return journal, errors.New("invalid private channel lease journal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return journal, err
	}
	if err = json.Unmarshal(data, &journal); err != nil {
		return journal, errors.New("invalid channel lease journal")
	}
	if len(journal.Leases) > 1000 {
		return journal, errors.New("channel lease journal capacity exceeded")
	}
	for _, record := range journal.Leases {
		if len(record.ID) != 32 || len(record.Channels) > 100 {
			return journal, errors.New("invalid channel lease record")
		}
		if _, err := hex.DecodeString(record.ID); err != nil {
			return journal, errors.New("invalid channel lease identity")
		}
	}
	return journal, nil
}
func writeLeaseJournal(path string, journal channelLeaseJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("channel lease journal exceeds 1 MiB")
	}
	file, err := os.CreateTemp(filepath.Dir(path), "lease-")
	if err != nil {
		return err
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
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (l *ChannelLease) ReclaimedCount() int { l.mu.Lock(); defer l.mu.Unlock(); return l.reclaimed }
