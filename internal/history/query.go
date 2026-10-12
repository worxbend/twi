package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"strings"
	"time"
)

func (s *Store) Snapshot(ctx context.Context, q Query) ([]automation.Record, int64, error) {
	if !q.AllowPartial {
		if e := s.CheckRetention(ctx, q); e != nil {
			return nil, 0, e
		}
	}
	high, e := s.HighWater(ctx)
	if e != nil {
		return nil, 0, e
	}
	records, _, e := s.read(ctx, q, 0, high, 0)
	if e != nil {
		return nil, high, e
	}
	if q.Last > 0 {
		counts := map[string]int{}
		keep := make([]automation.Record, 0, len(records))
		for i := len(records) - 1; i >= 0; i-- {
			r := records[i]
			if r.Type != "message" || counts[r.Channel.Login] < q.Last {
				keep = append(keep, r)
				if r.Type == "message" {
					counts[r.Channel.Login]++
				}
			}
		}
		for _, c := range q.Channels {
			if counts[c] < q.Last && !q.AllowPartial {
				return nil, high, fmt.Errorf("%w: #%s has %d of %d requested messages", ErrIncomplete, c, counts[c], q.Last)
			}
		}
		if !q.AllowPartial {
			for _, channel := range q.Channels {
				var oldest time.Time
				for _, r := range keep {
					if r.Channel.Login == channel && (oldest.IsZero() || r.ReceivedAt.Before(oldest)) {
						oldest = r.ReceivedAt
					}
				}
				if !oldest.IsZero() {
					if e = s.CheckCoverage(ctx, []string{channel}, oldest, nowUTC()); e != nil {
						return nil, high, e
					}
				}
			}
		}
		records = records[:0]
		for i := len(keep) - 1; i >= 0; i-- {
			records = append(records, keep[i])
		}
	}
	if !q.Since.IsZero() && !q.AllowPartial {
		end := q.Until
		if end.IsZero() {
			end = nowUTC()
		}
		if e = s.CheckCoverage(ctx, q.Channels, q.Since, end); e != nil {
			return nil, high, e
		}
	}
	return records, high, nil
}
func (s *Store) ReadAfter(ctx context.Context, q Query, after int64, limit int) ([]automation.Record, int64, error) {
	high, e := s.HighWater(ctx)
	if e != nil {
		return nil, after, e
	}
	return s.read(ctx, q, after, high, limit)
}
func (s *Store) read(ctx context.Context, q Query, after, high int64, limit int) ([]automation.Record, int64, error) {
	field := "event_at"
	if q.TimeField == "received" {
		field = "received_at"
	} else if q.TimeField != "" && q.TimeField != "event" {
		return nil, after, fmt.Errorf("invalid time field")
	}
	where := []string{"seq>?", "seq<=?"}
	args := []any{after, high}
	if len(q.Channels) > 0 {
		marks := make([]string, len(q.Channels))
		for i, c := range q.Channels {
			marks[i] = "?"
			args = append(args, c)
		}
		where = append(where, "channel IN ("+strings.Join(marks, ",")+")")
	}
	if len(q.ChannelStarts) > 0 {
		var ranges []string
		for _, channel := range q.Channels {
			ranges = append(ranges, "(channel=? AND seq>=?)")
			args = append(args, channel, q.ChannelStarts[channel])
		}
		where = append(where, "("+strings.Join(ranges, " OR ")+")")
	}
	if !q.AllEvents {
		where = append(where, s.visibleMessageSQL())
	}
	if !q.Since.IsZero() {
		where = append(where, field+">=?")
		args = append(args, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		where = append(where, field+"<?")
		args = append(args, q.Until.UnixNano())
	}
	query := "SELECT " + s.protectedPayloadColumns() + " FROM events e WHERE " + strings.Join(where, " AND ") + " ORDER BY seq"
	if q.Last > 0 {
		where = append(where, s.visibleMessageSQL())
		query = "SELECT seq,payload,deleted,reply_deleted FROM (SELECT " + s.protectedPayloadColumns() + ",row_number() OVER (PARTITION BY channel ORDER BY seq DESC) AS rank FROM events e WHERE " + strings.Join(where, " AND ") + ") WHERE rank<=? ORDER BY seq"
		args = append(args, q.Last)
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, e := s.db.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, after, e
	}
	defer rows.Close()
	var result []automation.Record
	scan := high
	for rows.Next() {
		var r automation.Record
		var data []byte
		var deleted, replyDeleted bool
		if e = rows.Scan(&r.Sequence, &data, &deleted, &replyDeleted); e != nil {
			return nil, after, e
		}
		seq := r.Sequence
		if e = json.Unmarshal(data, &r); e != nil {
			return nil, after, e
		}
		r.Sequence = seq
		r.Source = "history"
		protectDecoded(&r, deleted, replyDeleted)
		c := s.Cursor(seq)
		r.Cursor = &c
		result = append(result, r)
	}
	if e = rows.Err(); e != nil {
		return nil, after, e
	}
	if limit > 0 && len(result) == limit {
		scan = result[len(result)-1].Sequence
	}
	return result, scan, nil
}

// ReadThrough returns one bounded page within a previously captured high-water mark.
// The completed scan position advances over filtered rows, including empty pages.
func (s *Store) ReadThrough(ctx context.Context, q Query, after, high int64, limit int) ([]automation.Record, int64, error) {
	if limit <= 0 || limit > 1024 {
		return nil, after, fmt.Errorf("page size must be between 1 and 1024")
	}
	return s.read(ctx, q, after, high, limit)
}

// Refresh enforces current moderation state before a buffered record is emitted.
func (s *Store) Refresh(ctx context.Context, r automation.Record, all bool) (automation.Record, bool, error) {
	var data []byte
	var deleted, replyDeleted bool
	columns := "e.payload,e.deleted,0"
	if s.schema < 7 {
		columns = "e.payload," + effectiveDeletedSQL() + "," + replyRedactionSQL()
	}
	e := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM events e WHERE seq=?", r.Sequence).Scan(&data, &deleted, &replyDeleted)
	if e == sql.ErrNoRows {
		return r, false, ErrCursorExpired
	}
	if e != nil {
		return r, false, e
	}
	seq := r.Sequence
	if e = json.Unmarshal(data, &r); e != nil {
		return r, false, e
	}
	r.Sequence = seq
	protectDecoded(&r, deleted, replyDeleted)
	r.Source = "history"
	cursor := s.Cursor(seq)
	r.Cursor = &cursor
	if deleted {
		redact(&r)
	}
	return r, all || !deleted, nil
}

// TailSelection describes a fixed snapshot's per-channel message boundary without
// loading payloads. Controls are selected later by the caller's AllEvents setting.
type TailSelection struct {
	HighWater int64
	Starts    map[string]int64
	Received  map[string]time.Time
	Counts    map[string]int64
	Warnings  []error
}

// TailBoundary finds the Nth most recent retained, visible message using the
// channel/sequence index. Its Go memory grows only with selected channel count.
func (s *Store) TailBoundary(ctx context.Context, q Query) (TailSelection, error) {
	out := TailSelection{Starts: map[string]int64{}, Received: map[string]time.Time{}, Counts: map[string]int64{}}
	if q.Last <= 0 || len(q.Channels) == 0 {
		return out, fmt.Errorf("tail requires positive count and explicit channels")
	}
	high, e := s.HighWater(ctx)
	if e != nil {
		return out, e
	}
	out.HighWater = high
	for _, channel := range q.Channels {
		var seq, received int64
		e = s.db.QueryRowContext(ctx, "SELECT seq,received_at FROM events e WHERE channel=? AND seq<=? AND "+s.visibleMessageSQL()+" ORDER BY seq DESC LIMIT 1 OFFSET ?", channel, high, q.Last-1).Scan(&seq, &received)
		if e != nil && e != sql.ErrNoRows {
			return out, e
		}
		if e == sql.ErrNoRows {
			var count int64
			if e = s.db.QueryRowContext(ctx, "SELECT count(*) FROM events e WHERE channel=? AND seq<=? AND "+s.visibleMessageSQL(), channel, high).Scan(&count); e != nil {
				return out, e
			}
			out.Counts[channel] = count
			warning := fmt.Errorf("%w: #%s has %d of %d requested messages", ErrIncomplete, channel, count, q.Last)
			if !q.AllowPartial {
				return out, warning
			}
			out.Warnings = append(out.Warnings, warning)
			if count == 0 {
				out.Starts[channel] = high + 1
				continue
			}
			if e = s.db.QueryRowContext(ctx, "SELECT seq,received_at FROM events e WHERE channel=? AND seq<=? AND "+s.visibleMessageSQL()+" ORDER BY seq LIMIT 1", channel, high).Scan(&seq, &received); e != nil {
				return out, e
			}
		} else {
			out.Counts[channel] = int64(q.Last)
		}
		out.Starts[channel] = seq
		if e = s.db.QueryRowContext(ctx, "SELECT MIN(received_at) FROM events e WHERE channel=? AND seq>=? AND seq<=? AND "+s.visibleMessageSQL(), channel, seq, high).Scan(&received); e != nil {
			return out, e
		}
		out.Received[channel] = time.Unix(0, received).UTC()
		end := q.Until
		if end.IsZero() {
			end = nowUTC()
		}
		if e = s.CheckCoverage(ctx, []string{channel}, out.Received[channel], end); e != nil {
			if !q.AllowPartial {
				return out, e
			}
			out.Warnings = append(out.Warnings, e)
		}
	}
	return out, nil
}
