package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// recordingStreamLookup captures the context its call is given so a test can
// inspect the deadline and cancellation the shell attached to it.
type recordingStreamLookup struct {
	got chan context.Context
}

func (r *recordingStreamLookup) GetStreams(ctx context.Context, _ []string) ([]twitch.StreamInfo, error) {
	r.got <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func newRecordingModel(t *testing.T) (shellModel, *recordingStreamLookup) {
	t.Helper()
	model := newLiveModelWithClock("example", config.Default(), NewFakeChatClient(1), nil)
	resolver := &recordingStreamLookup{got: make(chan context.Context, 1)}
	model.services.streamStatusResolver = resolver
	return model, resolver
}

func TestOutboundTwitchCallsCarryADeadline(t *testing.T) {
	// Regression: these calls used to be made with context.Background(), so a
	// stalled connection parked the Bubble Tea command goroutine forever and
	// the message it owed the update loop never arrived.
	model, resolver := newRecordingModel(t)
	cmd := model.resolveStreamStatusCommand()
	if cmd == nil {
		t.Fatal("resolveStreamStatusCommand returned nil")
	}
	go cmd()

	ctx := <-resolver.got
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("outbound call was given a context with no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > twitchRequestTimeout {
		t.Errorf("deadline is %v away, want a positive value no greater than %v", remaining, twitchRequestTimeout)
	}
}

func TestQuittingCancelsAnInFlightTwitchCall(t *testing.T) {
	// The shell's lifetime context is cancelled when the program exits. An
	// in-flight request must observe that immediately rather than holding on
	// until its own timeout expires.
	model, resolver := newRecordingModel(t)
	lifetime, quit := context.WithCancel(context.Background())
	model.lifetime = lifetime

	done := make(chan struct{})
	go func() {
		model.resolveStreamStatusCommand()()
		close(done)
	}()

	ctx := <-resolver.got
	quit()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("call did not return after the shell quit")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("ctx.Err() = %v, want %v", ctx.Err(), context.Canceled)
	}
}

func TestModelWithNoLifetimeStillMakesCalls(t *testing.T) {
	// Most tests build a shellModel directly and never set a lifetime. Those
	// models must still produce a usable context rather than a nil one.
	var model shellModel
	if got := model.lifetimeContext(); got == nil {
		t.Fatal("lifetimeContext returned nil for a model with no lifetime")
	}
	ctx, cancel := model.requestContext(twitchRequestTimeout)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Error("requestContext returned a context with no deadline")
	}
}
