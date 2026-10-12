package app

import (
	"context"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/twitch"
)

type nativeRecorder struct {
	records chan automation.Record
	ready   chan string
	lost    chan struct{}
}

func (r *nativeRecorder) Submit(record automation.Record) error { r.records <- record; return nil }
func (r *nativeRecorder) Errors() <-chan error                  { return nil }
func (r *nativeRecorder) Ready(_ context.Context, channel string, _ time.Time) error {
	r.ready <- channel
	return nil
}
func (r *nativeRecorder) Lost(context.Context, time.Time) error { r.lost <- struct{}{}; return nil }
func TestNativeRecordingPrecedesDisplayOverflow(t *testing.T) {
	t.Parallel()
	transport := newFakeTwitchTransport(16)
	recorder := &nativeRecorder{records: make(chan automation.Record, 16), ready: make(chan string, 16), lost: make(chan struct{}, 16)}
	client, err := NewLiveChatClientWithOptions(context.Background(), transport, 1, LiveChatClientOptions{Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	transport.emit(twitch.Event{Kind: twitch.EventChannelReady, RoomState: twitch.RoomState{Channel: "alpha"}})
	select {
	case channel := <-recorder.ready:
		if channel != "alpha" {
			t.Fatal(channel)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness not captured")
	}
	for i := range 10 {
		transport.emit(twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: string(rune('a' + i)), Channel: "alpha", Text: "observed"}})
	}
	for range 10 {
		select {
		case record := <-recorder.records:
			if record.Text != "observed" {
				t.Fatal(record)
			}
		case <-time.After(time.Second):
			t.Fatal("capture lost message to UI pressure")
		}
	}
	if client.DroppedMessages() == 0 {
		t.Fatal("test did not exercise display overflow")
	}
	if _, err := client.Send(context.Background(), SendRequest{Channel: "alpha", Text: "outgoing"}); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-recorder.records:
		t.Fatalf("captured local send as received=%+v", record)
	default:
	}
}

func TestNoticeDisplayFragmentsPreserveSystemAndObservedBody(t *testing.T) {
	t.Parallel()
	notice := twitch.UserNotice{SystemText: "viewer resubscribed", Text: "hello Kappa", Fragments: []twitch.MessageFragment{{Type: twitch.FragmentText, Text: "hello "}, {Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{ID: "25"}}}}
	displayed := messageFromUserNotice(notice)
	joined := ""
	for _, fragment := range displayed.Fragments {
		joined += fragment.Text
	}
	if displayed.Text != "viewer resubscribed hello Kappa" || joined != displayed.Text {
		t.Fatalf("notice text/fragments disagree: text=%q fragments=%q", displayed.Text, joined)
	}
	if len(notice.Fragments) != 2 || notice.Fragments[0].Text != "hello " {
		t.Fatal("display mapping mutated observed fragments")
	}
	record := automation.Record{Type: "user_notice", SystemText: notice.SystemText, Text: notice.Text, Fragments: []automation.Fragment{{Type: "text", Text: "hello "}, {Type: "emote", Text: "Kappa", ID: "25"}}}
	replay := historyChatMessage(record)
	joined = ""
	for _, fragment := range replay.Fragments {
		joined += fragment.Text
	}
	if joined != replay.Text {
		t.Fatalf("history notice text/fragments disagree: %q / %q", replay.Text, joined)
	}
}
