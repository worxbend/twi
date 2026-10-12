package history

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"
)

func nowUTC() time.Time { return time.Now().UTC() }

type Capture struct {
	Channel   string     `json:"channel"`
	Session   string     `json:"session"`
	Transport string     `json:"transport"`
	Started   time.Time  `json:"started"`
	Ended     *time.Time `json:"ended"`
	Heartbeat time.Time  `json:"heartbeat"`
	Clean     bool       `json:"clean"`
	Healthy   bool       `json:"healthy"`
}
type Gap struct {
	Channel string    `json:"channel"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Cause   string    `json:"cause"`
	Lost    *int64    `json:"lost"`
}
type Status struct {
	Type              string    `json:"type"`
	SchemaVersion     int       `json:"schema_version"`
	DatabaseSchema    int       `json:"database_schema"`
	Path              string    `json:"path"`
	PrivateFiles      bool      `json:"private_files"`
	Bytes             int64     `json:"bytes"`
	RetentionAge      string    `json:"retention_age"`
	MaxBytes          int64     `json:"max_bytes"`
	UUID              string    `json:"store_uuid"`
	HighWater         int64     `json:"high_water"`
	LowWater          int64     `json:"low_water"`
	CapturesTruncated bool      `json:"captures_truncated"`
	GapsTruncated     bool      `json:"gaps_truncated"`
	Captures          []Capture `json:"captures"`
	Gaps              []Gap     `json:"gaps"`
}

func (s *Store) BeginCapture(ctx context.Context, channel, session, transport string, at time.Time) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO capture_intervals(channel,session,transport,started,heartbeat) VALUES(?,?,?,?,?)", channel, session, transport, at.UnixNano(), at.UnixNano())
	return e
}
func (s *Store) Heartbeat(ctx context.Context, session string, at time.Time) error {
	_, e := s.db.ExecContext(ctx, "UPDATE capture_intervals SET heartbeat=? WHERE session=? AND ended IS NULL", at.UnixNano(), session)
	return e
}
func (s *Store) EndCapture(ctx context.Context, session string, at time.Time, clean bool) error {
	_, e := s.db.ExecContext(ctx, "UPDATE capture_intervals SET ended=?,heartbeat=?,clean=? WHERE session=? AND ended IS NULL", at.UnixNano(), at.UnixNano(), clean, session)
	return e
}
func (s *Store) AddGap(ctx context.Context, g Gap) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO capture_gaps(channel,started,ended,cause,lost) VALUES(?,?,?,?,?)", g.Channel, g.Start.UnixNano(), g.End.UnixNano(), g.Cause, g.Lost)
	return e
}
func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{SchemaVersion: 1, Type: "history_status", UUID: s.uuid, Path: s.path, PrivateFiles: true}
	if validateDirectory(filepath.Dir(s.path)) != nil {
		out.PrivateFiles = false
	}
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		if checkFile(s.path+suffix, suffix == "") != nil {
			out.PrivateFiles = false
		}
	}
	var age int64
	if e := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.DatabaseSchema); e != nil {
		return out, e
	}
	if e := s.db.QueryRowContext(ctx, "SELECT retention_age,max_bytes FROM store_policy WHERE singleton=1").Scan(&age, &out.MaxBytes); e != nil {
		return out, e
	}
	out.RetentionAge = time.Duration(age).String()
	var sizeErr error
	out.Bytes, sizeErr = s.storeBytes()
	if sizeErr != nil {
		return out, sizeErr
	}
	e := s.db.QueryRowContext(ctx, "SELECT low_water,COALESCE((SELECT seq FROM sqlite_sequence WHERE name='events'),0) FROM store_metadata").Scan(&out.LowWater, &out.HighWater)
	if e != nil {
		return out, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT channel,session,transport,started,ended,heartbeat,clean FROM capture_intervals ORDER BY started DESC LIMIT 1024")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var c Capture
		var start, heartbeat int64
		var end sql.NullInt64
		if e = rows.Scan(&c.Channel, &c.Session, &c.Transport, &start, &end, &heartbeat, &c.Clean); e != nil {
			rows.Close()
			return out, e
		}
		c.Started = time.Unix(0, start).UTC()
		c.Heartbeat = time.Unix(0, heartbeat).UTC()
		if end.Valid {
			t := time.Unix(0, end.Int64).UTC()
			c.Ended = &t
		}
		c.Healthy = c.Ended == nil && nowUTC().Sub(c.Heartbeat) <= 15*time.Second
		out.Captures = append(out.Captures, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = s.db.QueryContext(ctx, "SELECT channel,started,ended,cause,lost FROM capture_gaps ORDER BY started DESC LIMIT 1024")
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var g Gap
		var start, end int64
		var lost sql.NullInt64
		if e = rows.Scan(&g.Channel, &start, &end, &g.Cause, &lost); e != nil {
			return out, e
		}
		g.Start = time.Unix(0, start).UTC()
		g.End = time.Unix(0, end).UTC()
		if lost.Valid {
			n := lost.Int64
			g.Lost = &n
		}
		out.Gaps = append(out.Gaps, g)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows.Close()
	var captureCount, gapCount int
	if e = s.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM capture_intervals),(SELECT count(*) FROM capture_gaps)").Scan(&captureCount, &gapCount); e != nil {
		return out, e
	}
	out.CapturesTruncated = captureCount > len(out.Captures)
	out.GapsTruncated = gapCount > len(out.Gaps)
	return out, nil
}
func (s *Store) Healthy(ctx context.Context, channels []string, at time.Time) error {
	for _, channel := range channels {
		var n int
		if e := s.db.QueryRowContext(ctx, "SELECT count(*) FROM capture_intervals WHERE channel=? AND ended IS NULL AND heartbeat>=?", channel, at.Add(-15*time.Second).UnixNano()).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			return fmt.Errorf("%w: no healthy recorder for #%s", ErrIncomplete, channel)
		}
	}
	return nil
}
func (s *Store) CheckCoverage(ctx context.Context, channels []string, start, end time.Time) error {
	if end.Before(start) {
		return fmt.Errorf("%w: invalid coverage interval", ErrIncomplete)
	}
	now := nowUTC()
	for _, channel := range channels {
		rows, e := s.db.QueryContext(ctx, "SELECT started,ended,heartbeat FROM capture_intervals WHERE channel=? AND started<=? ORDER BY started", channel, end.UnixNano())
		if e != nil {
			return e
		}
		position := start
		for rows.Next() {
			var begin, beat int64
			var finish sql.NullInt64
			if e = rows.Scan(&begin, &finish, &beat); e != nil {
				rows.Close()
				return e
			}
			last := time.Unix(0, beat)
			if finish.Valid {
				last = time.Unix(0, finish.Int64)
			} else if now.Sub(last) <= 15*time.Second && now.After(last) {
				last = now
			}
			if time.Unix(0, begin).After(position) || last.Before(position) {
				continue
			}
			if last.After(position) {
				position = last
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if position.Before(end) {
			return fmt.Errorf("%w: uncovered interval for #%s", ErrIncomplete, channel)
		}
		var cause string
		e = s.db.QueryRowContext(ctx, "SELECT cause FROM capture_gaps WHERE channel=? AND started<? AND ended>? LIMIT 1", channel, end.UnixNano(), start.UnixNano()).Scan(&cause)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			return fmt.Errorf("%w: capture gap for #%s (%s)", ErrIncomplete, channel, cause)
		}
	}
	return nil
}

// CoverageStarts finds the observation boundary each selected channel must cover.
// Quiet channels use capture start rather than requiring a chat message.
func (s *Store) CoverageStarts(ctx context.Context, channels []string, after int64) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	var resume sql.NullInt64
	if after > 0 {
		if e := s.db.QueryRowContext(ctx, "SELECT received_at FROM events WHERE seq<=? ORDER BY seq DESC LIMIT 1", after).Scan(&resume); e != nil && e != sql.ErrNoRows {
			return nil, e
		}
	}
	for _, channel := range channels {
		var n sql.NullInt64
		if e := s.db.QueryRowContext(ctx, "SELECT MIN(received_at) FROM events WHERE channel=? AND seq>?", channel, after).Scan(&n); e != nil {
			return nil, e
		}
		if resume.Valid {
			n = resume
		}
		if !n.Valid {
			if e := s.db.QueryRowContext(ctx, "SELECT MIN(started) FROM capture_intervals WHERE channel=?", channel).Scan(&n); e != nil {
				return nil, e
			}
		}
		if !n.Valid {
			return nil, fmt.Errorf("%w: #%s was never recorded", ErrIncomplete, channel)
		}
		out[channel] = time.Unix(0, n.Int64).UTC()
	}
	return out, nil
}

// CheckRetention distinguishes pruned content from known capture coverage.
func (s *Store) CheckRetention(ctx context.Context, q Query) error {
	if q.Since.IsZero() {
		return nil
	}
	field := "event_at"
	if q.TimeField == "received" {
		field = "received_at"
	}
	for _, channel := range q.Channels {
		var bound int64
		e := s.db.QueryRowContext(ctx, "SELECT "+field+" FROM retention_state WHERE channel=?", channel).Scan(&bound)
		if e == sql.ErrNoRows {
			continue
		}
		if e != nil {
			return e
		}
		if q.Since.UnixNano() <= bound {
			return fmt.Errorf("%w: requested time includes pruned content for #%s", ErrIncomplete, channel)
		}
	}
	return nil
}

// EndChannelCapture closes only one channel's intervals for a writer session.
func (s *Store) EndChannelCapture(ctx context.Context, channel, session string, at time.Time, clean bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE capture_intervals SET ended=?,heartbeat=?,clean=? WHERE channel=? AND session=? AND ended IS NULL", at.UnixNano(), at.UnixNano(), clean, channel, session)
	return err
}
