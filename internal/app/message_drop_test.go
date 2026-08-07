package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// TestEmitMessageDropsInsteadOfBlocking is the regression this exists for.
// emitMessage was the one emitter on LiveChatClient without a default branch,
// so a UI that fell behind blocked the bridge goroutine, which back-pressures
// into go-twitch-irc's parser goroutine -- the one that answers PING. Twitch
// then disconnects for missed keepalives. Losing the oldest unread message is
// a far smaller failure than losing the session.
func TestEmitMessageDropsInsteadOfBlocking(t *testing.T) {
	client := &LiveChatClient{
		messages: make(chan twitch.ChatMessage, 2),
		done:     make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			client.emitMessage(context.Background(), twitch.ChatMessage{
				ID:   fmt.Sprintf("m-%d", i),
				Text: "hello",
			})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitMessage blocked on a full buffer; the parser goroutine would stall and Twitch would drop the connection")
	}

	if dropped := client.DroppedMessages(); dropped == 0 {
		t.Fatal("messages were discarded but the drop counter stayed at zero")
	}
}

// TestEmitMessageKeepsTheNewest pins which end is discarded. The queue holds
// messages not yet displayed, so dropping the oldest keeps chat current and
// costs nothing already on screen.
func TestEmitMessageKeepsTheNewest(t *testing.T) {
	client := &LiveChatClient{
		messages: make(chan twitch.ChatMessage, 2),
		done:     make(chan struct{}),
	}
	for i := range 5 {
		client.emitMessage(context.Background(), twitch.ChatMessage{ID: fmt.Sprintf("m-%d", i)})
	}

	var got []string
	for len(client.messages) > 0 {
		got = append(got, (<-client.messages).ID)
	}
	if len(got) == 0 {
		t.Fatal("buffer is empty after emitting")
	}
	if last := got[len(got)-1]; last != "m-4" {
		t.Fatalf("newest retained message = %q, want %q", last, "m-4")
	}
}

func TestEmitMessageDeliversWhenConsumerKeepsUp(t *testing.T) {
	client := &LiveChatClient{
		messages: make(chan twitch.ChatMessage, 4),
		done:     make(chan struct{}),
	}
	for i := range 4 {
		client.emitMessage(context.Background(), twitch.ChatMessage{ID: fmt.Sprintf("m-%d", i)})
	}
	if dropped := client.DroppedMessages(); dropped != 0 {
		t.Fatalf("dropped %d messages with room to spare, want 0", dropped)
	}
	if got := len(client.messages); got != 4 {
		t.Fatalf("buffered %d messages, want 4", got)
	}
}

type fakeDropCounter struct {
	ChatClient
	dropped uint64
}

func (f *fakeDropCounter) DroppedMessages() uint64 { return f.dropped }

// TestStatusLineShowsDroppedMessages keeps the loss visible. Dropping is only
// an acceptable trade if the user finds out from the UI rather than from a
// viewer asking why their message was ignored.
func TestStatusLineShowsDroppedMessages(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModelWithClock("example", cfg, &appFakeClock{now: time.Now()})
	model.client = &fakeDropCounter{dropped: 143}

	status := ansi.Strip(model.statusLine(120))
	if !strings.Contains(status, "dropped=143") {
		t.Fatalf("status line = %q, want it to report dropped=143", status)
	}
}

func TestStatusLineOmitsDropCounterWhenNothingDropped(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModelWithClock("example", cfg, &appFakeClock{now: time.Now()})
	model.client = &fakeDropCounter{dropped: 0}

	if status := ansi.Strip(model.statusLine(120)); strings.Contains(status, "dropped=") {
		t.Fatalf("status line = %q, want no drop counter when nothing was dropped", status)
	}
}

// TestStatusLineHandlesSourcesThatCannotDrop covers mock mode and fakes, which
// do not implement the capability at all.
func TestStatusLineHandlesSourcesThatCannotDrop(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModelWithClock("example", cfg, &appFakeClock{now: time.Now()})

	if got := model.droppedMessageCount(); got != 0 {
		t.Fatalf("droppedMessageCount = %d for a source with no counter, want 0", got)
	}
	if status := ansi.Strip(model.statusLine(120)); strings.Contains(status, "dropped=") {
		t.Fatalf("status line = %q, want no drop counter in mock mode", status)
	}
}
