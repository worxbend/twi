package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/output"
	"io"
	"os"
	"sort"
	"syscall"
	"time"
)

func historyCode(err error) int {
	if errors.Is(err, history.ErrIncomplete) || errors.Is(err, history.ErrCursorExpired) || errors.Is(err, os.ErrNotExist) {
		return 6
	}
	return 7
}
func consumeHistory(ctx context.Context, o consumeOptions, start time.Time, stdout, stderr io.Writer) int {
	fail := func(code string, err error) int {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return 0
		}
		dataDiagnostic(stderr, o.dataOptions, code, err)
		return historyCode(err)
	}
	q := history.Query{Channels: []string(o.channels), Last: o.last, TimeField: o.timeField, AllEvents: o.eventsAll(), AllowPartial: o.allowPartial}
	var err error
	if o.since != "" {
		q.Since, err = parseDataTime(o.since, start)
		if err != nil {
			dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", err)
			return 2
		}
	}
	if o.until != "" {
		q.Until, err = parseDataTime(o.until, start)
		if err != nil || (!q.Since.IsZero() && !q.Until.After(q.Since)) {
			if err == nil {
				err = fmt.Errorf("until must be after since")
			}
			dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", err)
			return 2
		}
	}
	if q.Until.After(start) && !o.follow {
		dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", fmt.Errorf("future until requires follow"))
		return 2
	}
	store, err := history.Open(o.historyPath)
	if err != nil {
		return fail("history_unavailable", err)
	}
	defer store.Close()
	channels := append([]string(nil), q.Channels...)
	sort.Strings(channels)
	selection := struct {
		Channels          []string
		TimeField, Events string
	}{channels, o.timeField, o.events}
	b, _ := json.Marshal(selection)
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	selectorBytes, _ := json.Marshal(struct {
		Since, Until, Offset string
		Last                 int
	}{o.since, o.until, o.offset, o.last})
	selector := string(selectorBytes)
	position := int64(0)
	resumed := false
	if o.after != "" {
		position, err = store.ParseCursor(ctx, o.after)
		if err != nil {
			return fail("cursor_expired", err)
		}
		resumed = true
	}
	if o.cursorFile != "" {
		cp, e := history.ReadCheckpoint(o.cursorFile)
		if e == nil {
			if cp.UUID != store.UUID() || cp.Fingerprint != fingerprint {
				return fail("cursor_expired", history.ErrCursorExpired)
			}
			position, err = store.ParseCursor(ctx, store.Cursor(cp.Position))
			if err != nil {
				return fail("cursor_expired", err)
			}
			if (o.since != "" || o.until != "" || o.offset != "" || o.last > 0) && cp.Selection != "" && cp.Selection != selector {
				return fail("cursor_expired", history.ErrCursorExpired)
			}
			selector = cp.Selection
			q.Since = cp.Since
			q.Until = cp.Until
			resumed = true
		} else if !errors.Is(e, os.ErrNotExist) {
			return fail("checkpoint_failed", e)
		} else if o.offset == "" && o.last == 0 && o.since == "" {
			dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", errors.New("first cursor-file use requires initial selector"))
			return 2
		}
	}
	checkpoint := func(pos int64) error {
		if o.cursorFile == "" {
			return nil
		}
		return history.WriteCheckpoint(o.cursorFile, history.Checkpoint{SchemaVersion: 1, UUID: store.UUID(), Position: pos, Fingerprint: fingerprint, Selection: selector, Since: q.Since, Until: q.Until})
	}
	if o.follow {
		if err = store.Healthy(ctx, q.Channels, start); err != nil {
			return fail("history_incomplete", err)
		}
	}
	if e := store.CheckRetention(ctx, q); e != nil {
		if !o.allowPartial {
			return fail("history_incomplete", e)
		}
		dataDiagnostic(stderr, o.dataOptions, "history_incomplete", e)
	}
	snapshotEnd, err := store.HighWater(ctx)
	if err != nil {
		return fail("history_failed", err)
	}
	var records []automation.Record
	tailReceived := map[string]time.Time{}
	if !resumed && o.offset == "end" {
		position = snapshotEnd
	}
	if q.Last > 0 && !resumed {
		selection, e := store.TailBoundary(ctx, q)
		if e != nil {
			return fail("history_incomplete", e)
		}
		snapshotEnd = selection.HighWater
		q.ChannelStarts = selection.Starts
		tailReceived = selection.Received
		for _, warning := range selection.Warnings {
			dataDiagnostic(stderr, o.dataOptions, "history_incomplete", warning)
		}
	}
	if o.offset != "end" || resumed {
		starts, e := store.CoverageStarts(ctx, q.Channels, position)
		if e != nil {
			if !o.allowPartial {
				return fail("history_incomplete", e)
			}
			dataDiagnostic(stderr, o.dataOptions, "history_incomplete", e)
		}
		for channel, bound := range starts {
			if !q.Since.IsZero() {
				bound = q.Since
			}
			if received, ok := tailReceived[channel]; ok {
				bound = received
			}
			end := q.Until
			if end.IsZero() || (o.follow && end.After(start)) {
				end = start
			}
			if o.follow && bound.After(start) {
				bound = start
			}
			if e = store.CheckCoverage(ctx, []string{channel}, bound, end); e != nil {
				if !o.allowPartial {
					return fail("history_incomplete", e)
				}
				dataDiagnostic(stderr, o.dataOptions, "history_incomplete", e)
			}
		}
	}
	if q.TimeField == "event" && !q.Since.IsZero() && !o.quiet {
		dataDiagnostic(stderr, o.dataOptions, "event_time_coverage", errors.New("capture coverage describes observation time; late events may arrive later"))
	}
	emitted := 0
	lastOutput := time.Now()
	emit := func(rows []automation.Record) (int, bool) {
		for _, r := range rows {
			if ctx.Err() != nil {
				return 0, true
			}
			fresh, visible, e := store.Refresh(ctx, r, q.AllEvents)
			if e != nil {
				return fail("history_failed", e), true
			}
			if !visible {
				if e = checkpoint(r.Sequence); e != nil {
					return fail("checkpoint_failed", e), true
				}
				continue
			}
			r = fresh
			if err = output.Write(stdout, r, o.format, o.textOnly); err != nil {
				if ctx.Err() != nil {
					return 0, true
				}
				if errors.Is(err, syscall.EPIPE) {
					return 0, true
				}
				return fail("output_failed", err), true
			}
			emitted++
			lastOutput = time.Now()
			if err = checkpoint(r.Sequence); err != nil {
				return fail("checkpoint_failed", err), true
			}
			if o.count > 0 && emitted >= o.count {
				return 0, true
			}
		}
		return 0, false
	}
	q.Last = 0
	for position < snapshotEnd {
		if ctx.Err() != nil {
			return 0
		}
		records, next, e := store.ReadThrough(ctx, q, position, snapshotEnd, 256)
		if e != nil {
			return fail("history_failed", e)
		}
		if code, done := emit(records); done {
			return code
		}
		position = next
		if e = checkpoint(position); e != nil {
			return fail("checkpoint_failed", e)
		}
	}
	if err = checkpoint(position); err != nil {
		return fail("checkpoint_failed", err)
	}
	if !o.follow {
		return 0
	}
	q.Last = 0
	q.ChannelStarts = nil
	lastWarning := ""
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
			if o.idle > 0 && time.Since(lastOutput) >= o.idle {
				return 0
			}
			if !q.Until.IsZero() && !time.Now().Before(q.Until) {
				if e := store.CheckCoverage(ctx, q.Channels, start, q.Until); e != nil {
					if !errors.Is(e, history.ErrIncomplete) {
						return fail("history_failed", e)
					}
					dataDiagnostic(stderr, o.dataOptions, "capture_gap", e)
					if !o.allowPartial {
						return 6
					}
				}
				high, e := store.HighWater(ctx)
				if e != nil {
					return fail("history_failed", e)
				}
				for position < high {
					rows, next, e := store.ReadThrough(ctx, q, position, high, 256)
					if e != nil {
						return fail("history_failed", e)
					}
					if code, done := emit(rows); done {
						return code
					}
					position = next
					if e = checkpoint(position); e != nil {
						return fail("checkpoint_failed", e)
					}
				}
				return 0
			}
			if _, err = store.ParseCursor(ctx, store.Cursor(position)); err != nil {
				return fail("cursor_expired", err)
			}
			healthErr := store.Healthy(ctx, q.Channels, time.Now())
			coverageErr := store.CheckCoverage(ctx, q.Channels, start, time.Now())
			for _, issue := range []error{healthErr, coverageErr} {
				if issue == nil {
					continue
				}
				if !errors.Is(issue, history.ErrIncomplete) {
					return fail("history_failed", issue)
				}
				if issue.Error() != lastWarning {
					dataDiagnostic(stderr, o.dataOptions, "capture_gap", issue)
					lastWarning = issue.Error()
				}
				if !o.allowPartial {
					return 6
				}
			}
			records, position, err = store.ReadAfter(ctx, q, position, 256)
			if err != nil {
				return fail("history_failed", err)
			}
			if code, done := emit(records); done {
				return code
			}
			if err = checkpoint(position); err != nil {
				return fail("checkpoint_failed", err)
			}
		}
	}
}
