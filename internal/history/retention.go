package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Retention struct {
	Age      time.Duration
	MaxBytes int64
	Now      time.Time
}
type PruneResult struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Records       int64  `json:"records"`
	Through       int64  `json:"through"`
	Bytes         int64  `json:"bytes"`
	DryRun        bool   `json:"dry_run"`
}

func (s *Store) Prune(ctx context.Context, r Retention, dry bool) (PruneResult, error) {
	out := PruneResult{SchemaVersion: 1, Type: "history_prune", DryRun: dry}
	if r.Age <= 0 || r.MaxBytes <= 0 {
		return out, errors.New("retention age and byte budget must be positive")
	}
	if r.Now.IsZero() {
		r.Now = nowUTC()
	}
	bytes, e := s.storeBytes()
	if e != nil {
		return out, e
	}
	out.Bytes = bytes
	cutoff := r.Now.Add(-r.Age).UnixNano()
	if e = s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events WHERE received_at<?", cutoff).Scan(&out.Through); e != nil {
		return out, e
	}
	if bytes > r.MaxBytes {
		var n, seq int64
		if e = s.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&n); e != nil {
			return out, e
		}
		remove := n*(bytes-r.MaxBytes)/bytes + 1
		if e = s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM (SELECT seq FROM events ORDER BY seq LIMIT ?)", remove).Scan(&seq); e != nil {
			return out, e
		}
		if seq > out.Through {
			out.Through = seq
		}
	}
	if e = s.db.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE seq<=?", out.Through).Scan(&out.Records); e != nil {
		return out, e
	}
	if dry {
		return out, nil
	}
	if s.lock == nil {
		return out, errors.New("prune requires writer lock")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE store_policy SET retention_age=?,max_bytes=? WHERE singleton=1", int64(r.Age), r.MaxBytes); e != nil {
		return out, e
	}
	// Persist compact channel watermarks before retiring detailed deletion identities.
	if _, e = tx.ExecContext(ctx, `INSERT INTO moderation_boundaries(channel,user_id,user_login,before_at) SELECT channel,'','',? FROM tombstones WHERE deleted_at<? GROUP BY channel ON CONFLICT(channel,user_id,user_login) DO UPDATE SET before_at=MAX(before_at,excluded.before_at)`, cutoff, cutoff); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM tombstones WHERE deleted_at<?", cutoff); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM capture_intervals WHERE ended<?", cutoff); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM capture_gaps WHERE ended<?", cutoff); e != nil {
		return out, e
	}
	if e = s.pruneRows(ctx, tx, out.Through); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	// Bounded maintenance headroom includes the configured 4 MiB WAL allowance.
	for pass := 0; pass < 64; pass++ {
		if _, e = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE); PRAGMA incremental_vacuum(1024); PRAGMA wal_checkpoint(TRUNCATE)"); e != nil {
			return out, e
		}
		bytes, e = s.storeBytes()
		if e != nil {
			return out, e
		}
		out.Bytes = bytes
		if bytes-r.MaxBytes <= 4*1024*1024 {
			return out, nil
		}
		var high, count int64
		if e = s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0),COUNT(*) FROM (SELECT seq FROM events ORDER BY seq LIMIT 1024)").Scan(&high, &count); e != nil {
			return out, e
		}
		if count == 0 {
			return out, fmt.Errorf("%w: history budget below required metadata size or checkpoint blocked", ErrStorageFull)
		}
		tx, e = s.db.BeginTx(ctx, nil)
		if e != nil {
			return out, e
		}
		if e = s.pruneRows(ctx, tx, high); e != nil {
			tx.Rollback()
			return out, e
		}
		if e = tx.Commit(); e != nil {
			return out, e
		}
		out.Records += count
		out.Through = high
	}
	return out, fmt.Errorf("%w: retention maintenance exhausted bounded work; retry prune or increase budget", ErrStorageFull)
}
func (s *Store) pruneRows(ctx context.Context, tx *sql.Tx, through int64) error {
	if through == 0 {
		return nil
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO retention_state(channel,event_at,received_at) SELECT channel,MAX(event_at),MAX(received_at) FROM events WHERE seq<=? GROUP BY channel ON CONFLICT(channel) DO UPDATE SET event_at=MAX(event_at,excluded.event_at),received_at=MAX(received_at,excluded.received_at)`, through); e != nil {
		return e
	}
	// Old delayed records cannot resurrect content whose identity detail was retired.
	if _, e := tx.ExecContext(ctx, `INSERT INTO moderation_boundaries(channel,user_id,user_login,before_at) SELECT channel,'','',MAX(event_at) FROM events WHERE seq<=? GROUP BY channel ON CONFLICT(channel,user_id,user_login) DO UPDATE SET before_at=MAX(before_at,excluded.before_at)`, through); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM events WHERE seq<=?", through); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "UPDATE store_metadata SET low_water=MAX(low_water,?)", through)
	return e
}
func (s *Store) storeBytes() (int64, error) {
	var bytes int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		i, e := os.Stat(s.path + suffix)
		if e == nil {
			bytes += i.Size()
		} else if !os.IsNotExist(e) {
			return 0, e
		}
	}
	return bytes, nil
}

type Checkpoint struct {
	SchemaVersion int       `json:"schema_version"`
	UUID          string    `json:"store_uuid"`
	Position      int64     `json:"position"`
	Fingerprint   string    `json:"fingerprint"`
	Selection     string    `json:"selection,omitempty"`
	Since         time.Time `json:"since"`
	Until         time.Time `json:"until"`
}

func ReadCheckpoint(path string) (Checkpoint, error) {
	var c Checkpoint
	if e := checkFile(path, true); e != nil {
		return c, e
	}
	info, e := os.Stat(path)
	if e != nil {
		return c, e
	}
	if info.Size() > 65536 {
		return c, errors.New("cursor file exceeds 64 KiB")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e == nil && c.SchemaVersion != 1 {
		e = errors.New("unsupported cursor file version")
	}
	return c, e
}
func WriteCheckpoint(path string, c Checkpoint) error {
	if e := checkFile(path, false); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".twi-cursor-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	b, e := json.Marshal(c)
	if e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(append(b, '\n')); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
