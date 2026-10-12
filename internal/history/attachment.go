package history

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/twitch"
)

var ErrAttachmentOverflow = errors.New("recorder attachment queue full")

// Attachment is an explicitly opened, bounded recording sink for interactive
// clients. It never starts a background daemon. Errors must be surfaced by the
// caller; queue exhaustion terminates capture with a coverage gap.
type Attachment struct {
	session      string
	transport    string
	ready        map[string]bool
	store        *Store
	queue        chan automation.Record
	errors       chan error
	done         chan struct{}
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	failure      error
	failureSince time.Time
	channels     map[string]bool
}

func OpenAttachment(ctx context.Context, path string, channels []string, transport string) (*Attachment, error) {
	if len(channels) == 0 || len(channels) > 100 || (transport != "irc" && transport != "eventsub") {
		return nil, errors.New("recorder attachment requires explicit channels")
	}
	validated, err := twitch.ParseChannels(channels)
	if err != nil {
		return nil, err
	}
	channels = make([]string, len(validated))
	for i, channel := range validated {
		channels[i] = channel.String()
	}
	store, err := OpenWriter(path)
	if err != nil {
		return nil, err
	}
	session := fmt.Sprintf("tui-%d", time.Now().UnixNano())
	runCtx, cancel := context.WithCancel(ctx)
	a := &Attachment{session: session, transport: transport, ready: map[string]bool{}, store: store, queue: make(chan automation.Record, 100), errors: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
	a.channels = make(map[string]bool, len(channels))
	for _, channel := range channels {
		a.channels[channel] = true
	}
	go a.run(runCtx, store, session)
	return a, nil
}

func (a *Attachment) Errors() <-chan error { return a.errors }

func (a *Attachment) Submit(r automation.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.channels[r.Channel.Login] {
		return errors.New("record channel outside attachment selection")
	}
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	select {
	case <-a.done:
		return errors.New("recorder attachment stopped")
	case a.queue <- r:
		return nil
	default:
		a.closed = true
		a.failure = ErrAttachmentOverflow
		a.failureSince = time.Now().UTC()
		a.errors <- ErrAttachmentOverflow
		a.cancel()
		return ErrAttachmentOverflow
	}
}

func (a *Attachment) Close() error {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	<-a.done
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failure
}

func (a *Attachment) run(ctx context.Context, store *Store, session string) {
	defer close(a.done)
	defer close(a.errors)
	defer store.Close()
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()
	records := make([]automation.Record, 0, 100)
	var failure error
	lastDurable := time.Now().UTC()
	flush := func(flushCtx context.Context) error {
		if len(records) == 0 {
			return nil
		}
		_, err := store.AppendBatch(flushCtx, records)
		if err == nil {
			records = records[:0]
			lastDurable = time.Now().UTC()
		}
		return err
	}
	defer func() {
		if errors.Is(failure, context.Canceled) && ctx.Err() != nil {
			failure = nil
		}
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Submit and Close serialize. Once canceled, no further records enter queue.
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		if len(records) == 100 {
			if err := flush(end); err != nil {
				failure = err
				goto drained
			}
		}
		for {
			select {
			case r := <-a.queue:
				records = append(records, r)
				if len(records) == 100 {
					if err := flush(end); err != nil {
						failure = err
						goto drained
					}
				}
			default:
				goto drained
			}
		}
	drained:
		if err := flush(end); err != nil {
			failure = err
		}
		a.mu.Lock()
		clean := failure == nil && a.failure == nil
		cause := "recorder_storage_failure"
		if errors.Is(a.failure, ErrAttachmentOverflow) {
			cause = "recorder_overflow"
		}
		if !a.failureSince.IsZero() && a.failureSince.Before(lastDurable) {
			lastDurable = a.failureSince
		}
		a.mu.Unlock()
		ended := time.Now().UTC()
		if !clean {
			for channel := range a.channels {
				if err := store.AddGap(end, Gap{Channel: channel, Start: lastDurable, End: ended.Add(time.Nanosecond), Cause: cause}); err != nil {
					failure = err
				}
			}
		}
		if err := store.EndCapture(end, session, ended, clean); err != nil {
			failure = err
		}
		if failure != nil {
			a.mu.Lock()
			if a.failure == nil {
				a.failure = failure
			}
			a.mu.Unlock()
			select {
			case a.errors <- failure:
			default:
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-a.queue:
			records = append(records, r)
			if len(records) == 100 {
				failure = flush(ctx)
			}
		case <-timer.C:
			failure = flush(ctx)
		case <-heartbeat.C:
			failure = store.Heartbeat(ctx, session, time.Now().UTC())
		}
		if failure != nil {
			return
		}
	}
}

// AddGap records transport loss without claiming historical backfill. Callers
// report the actual disconnected interval when reconnect readiness is known.
func (a *Attachment) AddGap(ctx context.Context, g Gap) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	if !a.channels[g.Channel] {
		return errors.New("gap channel outside attachment selection")
	}
	return a.store.AddGap(ctx, g)
}

// Ready begins coverage only after the transport confirms this channel joined.
func (a *Attachment) Ready(ctx context.Context, channel string, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	if !a.channels[channel] {
		return errors.New("channel outside attachment selection")
	}
	if a.ready[channel] {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := a.store.BeginCapture(ctx, channel, a.session, a.transport, at); err != nil {
		return err
	}
	a.ready[channel] = true
	return nil
}

// Lost ends coverage when the transport disconnects. A later Ready starts a
// new interval; the intervening time is never represented as captured history.
func (a *Attachment) Lost(ctx context.Context, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := a.store.EndCapture(ctx, a.session, at, false); err != nil {
		return err
	}
	clear(a.ready)
	return nil
}

// Loss records known upstream loss across ready channels. With multiple channels
// the aggregate lost count cannot honestly be attributed to individual channels.
func (a *Attachment) Loss(ctx context.Context, start, end time.Time, lost uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	if start.IsZero() || end.IsZero() || end.Before(start) || lost > 1<<63-1 {
		return errors.New("invalid recorder loss interval or count")
	}
	for channel := range a.ready {
		gap := Gap{Channel: channel, Start: start, End: end.Add(time.Nanosecond), Cause: "upstream_overflow"}
		if len(a.ready) == 1 && lost > 0 {
			count := int64(lost)
			gap.Lost = &count
		}
		if err := a.store.AddGap(ctx, gap); err != nil {
			return err
		}
	}
	return nil
}

// Fail reports an integration failure through the same terminal recording-error
// channel as storage failures. It stops capture without changing Twitch state.
func (a *Attachment) Fail(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	a.failure = err
	a.failureSince = time.Now().UTC()
	select {
	case a.errors <- err:
	default:
	}
	a.cancel()
}

// ChannelLost ends one explicitly selected channel without changing coverage of
// others. A later transport-ready event may start its next capture interval.
func (a *Attachment) ChannelLost(ctx context.Context, channel string, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("recorder attachment closed")
	}
	if !a.channels[channel] {
		return errors.New("channel outside attachment selection")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := a.store.EndChannelCapture(ctx, channel, a.session, at, false); err != nil {
		return err
	}
	delete(a.ready, channel)
	return nil
}
