package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/output"
	"github.com/worxbend/twi/internal/twitch/eventsub"
	"io"
	"strconv"
	"strings"
	"time"
)

func parseByteBudget(s string) (int64, error) {
	for _, unit := range []struct {
		name string
		n    int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1000000000}, {"MB", 1000000}, {"KB", 1000}} {
		if strings.HasSuffix(s, unit.name) {
			n, e := strconv.ParseInt(strings.TrimSuffix(s, unit.name), 10, 64)
			if e != nil || n <= 0 || n > (1<<63-1)/unit.n {
				return 0, errors.New("invalid byte budget")
			}
			return n * unit.n, nil
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n <= 0 {
		return 0, errors.New("invalid byte budget")
	}
	return n, nil
}
func (r *runner) runRecord(ctx context.Context, args []string, stdout, stderr io.Writer) (result int) {
	o := dataOptions{}
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addDataFlags(fs, &o, true)
	retention := fs.Duration("retention", 168*time.Hour, "retained age")
	budget := fs.String("max-bytes", "1GiB", "target total store budget including WAL")
	transport := fs.String("transport", "irc", "irc or eventsub")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := validateDataOptions(&o); err != nil {
		dataDiagnostic(stderr, o, "invalid_arguments", err)
		return 2
	}
	maxBytes, err := parseByteBudget(*budget)
	if err != nil || *retention <= 0 || len(o.channels) == 0 || len(o.channels) > 100 || (*transport == "eventsub" && len(o.channels) > eventsub.MaxReadChannels) || fs.NArg() > 0 || (*transport != "irc" && *transport != "eventsub") {
		dataDiagnostic(stderr, o, "invalid_arguments", errors.New("explicit channels, valid transport, positive retention and byte budget required"))
		return 2
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, stderr = dataStreams(ctx, stdout, stderr)
	service, err := r.recordReader(ctx, o, *transport)
	if err != nil {
		dataDiagnostic(stderr, o, "setup_failed", err)
		return dataErrorCode(err)
	}
	events, err := service.Run(ctx)
	if err != nil {
		dataDiagnostic(stderr, o, "connection_failed", err)
		return dataErrorCode(err)
	}
	store, err := history.OpenWriter(o.historyPath)
	if err != nil {
		dataDiagnostic(stderr, o, "storage_failed", err)
		return 7
	}
	defer store.Close()
	if _, err = store.Prune(ctx, history.Retention{Age: *retention, MaxBytes: maxBytes, Now: time.Now().UTC()}, false); err != nil {
		dataDiagnostic(stderr, o, "storage_failed", err)
		return 7
	}
	session := fmt.Sprintf("%d", time.Now().UnixNano())
	ready := map[string]bool{}
	selected := map[string]bool{}
	for _, channel := range o.channels {
		selected[channel] = true
	}
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := store.EndCapture(end, session, time.Now().UTC(), result == 0); err != nil && result == 0 {
			dataDiagnostic(stderr, o, "storage_failed", err)
			result = 7
		}
	}()
	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()
	maintenance := time.NewTicker(5 * time.Second)
	defer maintenance.Stop()
	pending := make([]automation.Record, 0, 100)
	lastDurable := time.Now().UTC()
	captureFailure := func(failure error) {
		bounded, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		since := lastDurable
		for _, r := range pending {
			if !r.ReceivedAt.IsZero() && r.ReceivedAt.Before(since) {
				since = r.ReceivedAt
			}
		}
		ended := time.Now().UTC().Add(time.Nanosecond)
		for channel := range ready {
			if gapErr := store.AddGap(bounded, history.Gap{Channel: channel, Start: since, End: ended, Cause: "recorder_storage_failure"}); gapErr != nil {
				dataDiagnostic(stderr, o, "capture_gap_failed", gapErr)
			}
		}
		dataDiagnostic(stderr, o, "storage_failed", failure)
	}
	flush := func(flushCtx context.Context) error {
		if len(pending) == 0 {
			return nil
		}
		if _, err := store.AppendBatch(flushCtx, pending); err != nil {
			return err
		}
		pending = pending[:0]
		lastDurable = time.Now().UTC()
		return nil
	}
	batchTimer := time.NewTicker(100 * time.Millisecond)
	defer batchTimer.Stop()
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := flush(end); err != nil {
			captureFailure(err)
			result = 7
		}
	}()
	storageFail := func(err error) int {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return 0
		}
		captureFailure(err)
		return 7
	}
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-batchTimer.C:
			if err = flush(ctx); err != nil {
				return storageFail(err)
			}
		case <-heartbeat.C:
			if err = store.Heartbeat(ctx, session, time.Now().UTC()); err != nil {
				return storageFail(err)
			}
		case <-maintenance.C:
			if _, err = store.Prune(ctx, history.Retention{Age: *retention, MaxBytes: maxBytes, Now: time.Now().UTC()}, false); err != nil {
				return storageFail(err)
			}
		case e, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return 0
				}
				dataDiagnostic(stderr, o, "connection_failed", errors.New("reader stopped"))
				return 4
			}
			switch e.Kind {
			case chatservice.EventReady:
				if !ready[e.Channel] {
					if err = store.BeginCapture(ctx, e.Channel, session, *transport, e.At); err != nil {
						return storageFail(err)
					}
					ready[e.Channel] = true
				}
			case chatservice.EventFailed:
				if err = flush(ctx); err != nil {
					return storageFail(err)
				}
				dataDiagnostic(stderr, o, "connection_failed", e.Err)
				if isAuthDataError(e.Err) {
					return 3
				}
				return 4
			case chatservice.EventGap:
				if err = flush(ctx); err != nil {
					return storageFail(err)
				}
				if err = store.EndCapture(ctx, session, e.At, false); err != nil {
					return storageFail(err)
				}
				for _, ch := range o.channels {
					lost := int64(e.Lost)
					start := e.Since
					if start.IsZero() {
						start = e.At
					}
					g := history.Gap{Channel: ch, Start: start, End: e.At.Add(time.Nanosecond), Cause: e.Reason}
					if e.Lost > 0 && len(o.channels) == 1 {
						g.Lost = &lost
					}
					if err = store.AddGap(ctx, g); err != nil {
						return storageFail(err)
					}
				}
				if e.Reason == "upstream_overflow" {
					for ch := range ready {
						if err = store.BeginCapture(ctx, ch, session, *transport, e.At); err != nil {
							return storageFail(err)
						}
					}
				} else {
					clear(ready)
				}
				dataDiagnostic(stderr, o, "capture_gap", errors.New(e.Reason))
			case chatservice.EventReceived:
				r, ok := output.FromEvent(e.Twitch, e.At, *transport)
				if !ok || !selected[r.Channel.Login] {
					continue
				}
				pending = append(pending, r)
				if len(pending) == cap(pending) {
					if err = flush(ctx); err != nil {
						return storageFail(err)
					}
				}
			}
		}
	}
}
