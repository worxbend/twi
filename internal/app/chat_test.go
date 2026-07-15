package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

func TestFakeChatClientStreamsMessagesAndConnectionStates(t *testing.T) {
	client := NewFakeChatClient(2)
	connectedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	state := ConnectionState{
		Status:  ConnectionConnected,
		Channel: "example",
		Detail:  "joined",
		At:      connectedAt,
	}
	msg := twitch.ChatMessage{
		ID:          "msg-1",
		Channel:     "example",
		Timestamp:   connectedAt.Add(time.Second),
		AuthorLogin: "viewer",
		Text:        "hello chat",
		Type:        twitch.MessageTypeChat,
	}

	if err := client.FeedConnectionState(state); err != nil {
		t.Fatalf("FeedConnectionState returned error: %v", err)
	}
	if err := client.FeedMessage(msg); err != nil {
		t.Fatalf("FeedMessage returned error: %v", err)
	}

	if got := <-client.ConnectionStates(); !reflect.DeepEqual(got, state) {
		t.Fatalf("ConnectionStates() = %#v, want %#v", got, state)
	}
	if got := <-client.Messages(); !reflect.DeepEqual(got, msg) {
		t.Fatalf("Messages() = %#v, want %#v", got, msg)
	}
}

func TestFakeChatClientSendRecordsRequestsAndReturnsQueuedResults(t *testing.T) {
	client := NewFakeChatClient(1)
	acceptedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := client.QueueSendResult(SendResult{MessageID: "out-1", AcceptedAt: acceptedAt}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	sendErr := errors.New("rate limited")
	if err := client.QueueSendResult(SendResult{}, sendErr); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}

	req := SendRequest{Channel: "example", Text: "hello", ReplyToMessageID: "parent-1"}
	result, err := client.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if result.MessageID != "out-1" || !result.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("Send result = %#v, want queued success", result)
	}

	failedReq := SendRequest{Channel: "example", Text: "/me waves", Action: true}
	_, err = client.Send(context.Background(), failedReq)
	if !errors.Is(err, sendErr) {
		t.Fatalf("Send error = %v, want %v", err, sendErr)
	}

	sent := client.SentRequests()
	if len(sent) != 2 {
		t.Fatalf("SentRequests length = %d, want 2", len(sent))
	}
	if sent[0] != req || sent[1] != failedReq {
		t.Fatalf("SentRequests = %#v, want queued requests", sent)
	}

	sent[0].Text = "mutated"
	if got := client.SentRequests()[0].Text; got != "hello" {
		t.Fatalf("SentRequests returned mutable backing storage; got %q", got)
	}
}

func TestFakeChatClientSendHonorsCanceledContext(t *testing.T) {
	client := NewFakeChatClient(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Send(ctx, SendRequest{Channel: "example", Text: "hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want %v", err, context.Canceled)
	}
	if got := client.SentRequests(); len(got) != 0 {
		t.Fatalf("SentRequests length = %d, want 0 for canceled context", len(got))
	}
}

func TestFakeChatClientSendReturnsRateLimitLikeResult(t *testing.T) {
	client := NewFakeChatClient(1)
	result := SendResult{
		RateLimited: true,
		RetryAfter:  30 * time.Second,
		Detail:      "sending messages too quickly",
	}
	if err := client.QueueSendResult(result, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}

	got, err := client.Send(context.Background(), SendRequest{Channel: "example", Text: "hello"})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if !got.RateLimited || got.RetryAfter != 30*time.Second || got.Detail != result.Detail {
		t.Fatalf("Send result = %#v, want rate-limit-like result %#v", got, result)
	}
}

func TestFakeChatClientClose(t *testing.T) {
	client := NewFakeChatClient(1)

	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := client.FeedMessage(twitch.ChatMessage{}); !errors.Is(err, ErrFakeChatClientClosed) {
		t.Fatalf("FeedMessage error = %v, want %v", err, ErrFakeChatClientClosed)
	}
	if _, err := client.Send(context.Background(), SendRequest{Channel: "example", Text: "hello"}); !errors.Is(err, ErrFakeChatClientClosed) {
		t.Fatalf("Send error = %v, want %v", err, ErrFakeChatClientClosed)
	}
}
