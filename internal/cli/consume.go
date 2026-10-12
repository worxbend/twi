package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/output"
	"io"
	"syscall"
	"time"
)

func (r *runner) runConsume(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	start := time.Now()
	o, code, err := parseConsume(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", err)
		}
		return code
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.duration > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, o.duration)
		defer stop()
	}
	stdout, stderr = dataStreams(ctx, stdout, stderr)
	if o.historical {
		return consumeHistory(ctx, o, start, stdout, stderr)
	}
	service, err := r.makeDataReader(ctx, o.dataOptions, o.transport, o.allowPartial)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return 0
		}
		code = dataErrorCode(err)
		dataDiagnostic(stderr, o.dataOptions, "connection_failed", err)
		return code
	}
	events, err := service.Run(ctx)
	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return 0
	}
	if err != nil {
		dataDiagnostic(stderr, o.dataOptions, "connection_failed", err)
		return dataErrorCode(err)
	}
	return consumeLive(ctx, o, events, stdout, stderr)
}
func consumeLive(ctx context.Context, o consumeOptions, events <-chan chatservice.Event, stdout, stderr io.Writer) int {
	ready := map[string]bool{}
	excluded := map[string]bool{}
	wanted := len(o.channels)
	pending := []automation.Record{}
	emitted := 0
	var idle *time.Timer
	var idleC <-chan time.Time
	defer func() {
		if idle != nil {
			idle.Stop()
		}
	}()
	emit := func(r automation.Record) (int, bool) {
		if !o.eventsAll() && r.Type != "message" {
			return 0, false
		}
		if err := output.Write(stdout, r, o.format, o.textOnly); err != nil {
			if ctx.Err() != nil {
				return 0, true
			}
			if errors.Is(err, syscall.EPIPE) {
				return 0, true
			}
			dataDiagnostic(stderr, o.dataOptions, "output_failed", err)
			return 7, true
		}
		emitted++
		if idle != nil {
			idle.Reset(o.idle)
		}
		return 0, o.count > 0 && emitted >= o.count
	}
	flush := func() (int, bool) {
		if len(ready) < wanted {
			return 0, false
		}

		if o.idle > 0 && idle == nil {
			idle = time.NewTimer(o.idle)
			idleC = idle.C
		}
		for _, r := range pending {
			if code, done := emit(r); done {
				return code, true
			}
		}
		pending = nil

		return 0, false
	}

	for {
		select {
		case <-ctx.Done():
			return 0
		case <-idleC:
			return 0
		case e, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return 0
				}
				dataDiagnostic(stderr, o.dataOptions, "connection_failed", errors.New("reader stopped"))
				return 4
			}
			switch e.Kind {
			case chatservice.EventFailed:
				if ctx.Err() != nil {
					return 0
				}
				dataDiagnostic(stderr, o.dataOptions, "channel_unavailable", e.Err)
				return dataErrorCode(e.Err)
			case chatservice.EventGap:
				if isAuthDataError(e.Err) {
					dataDiagnostic(stderr, o.dataOptions, "auth_failed", e.Err)
					return 3
				}
				dataDiagnostic(stderr, o.dataOptions, "capture_gap", errors.New(e.Reason))
				// Validation failures carry their terminal auth/network class in the next event.
				if e.Reason == "authorization_failed" {
					continue
				}
				if !o.allowPartial {
					return 6
				}
				if e.Reason != "upstream_overflow" {
					clear(ready)
				}
			case chatservice.EventUnavailable:
				if !excluded[e.Channel] {
					excluded[e.Channel] = true
					wanted--
					delete(ready, e.Channel)
				}
				dataDiagnostic(stderr, o.dataOptions, "channel_unavailable", fmt.Errorf("#%s excluded: %s", e.Channel, e.Reason))
				filtered := pending[:0]
				for _, r := range pending {
					if !excluded[r.Channel.Login] {
						filtered = append(filtered, r)
					}
				}
				pending = filtered
				if code, done := flush(); done {
					return code
				}
			case chatservice.EventReady:
				if excluded[e.Channel] {
					continue
				}
				ready[e.Channel] = true
				if code, done := flush(); done {
					return code
				}
			case chatservice.EventReceived:
				r, ok := output.FromEvent(e.Twitch, e.At, o.transport)
				if !ok {
					continue
				}
				found := false
				for _, ch := range o.channels {
					if r.Channel.Login == ch {
						found = true
						break
					}
				}
				if !found || excluded[r.Channel.Login] {
					continue
				}
				if len(ready) < wanted {
					if len(pending) >= 1024 {
						dataDiagnostic(stderr, o.dataOptions, "capture_gap", errors.New("startup buffer full"))
						if !o.allowPartial {
							return 6
						}
						continue
					}
					pending = append(pending, r)
					continue
				}
				if code, done := emit(r); done {
					return code
				}
			}
		}
	}
}
func (o consumeOptions) eventsAll() bool { return o.events == "all" }
