package cli

import (
	"context"
	"errors"
	"testing"
	"time"
)

type blockedWriter struct{ started, release chan struct{} }

func (w blockedWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}
func TestCancellationReleasesBlockedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := blockedWriter{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := (contextWriter{ctx, sink}).Write([]byte("record")); done <- err }()
	<-sink.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked output ignored cancellation")
	}
	close(sink.release)
}
