package irc

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
)

func TestProducerWritesActionBeforeReturningAndClosing(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	written := make(chan string, 1)
	completed := make(chan struct{})
	go func() {
		defer server.Close()
		defer close(completed)
		reader := bufio.NewReader(server)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if strings.HasPrefix(line, "NICK ") {
				_, _ = server.Write([]byte(":tmi.twitch.tv 001 bot :Welcome\r\n"))
			}
			if strings.HasPrefix(line, "JOIN ") {
				_, _ = server.Write([]byte("@room-id=123 :tmi.twitch.tv ROOMSTATE #alpha\r\n"))
			}
			if strings.Contains(line, "PRIVMSG") {
				written <- line
			}
		}
	}()
	producer, err := NewProducer(context.Background(), ProducerConfig{Login: "bot", Token: func() string { return "fake-token" }, Dial: func(context.Context) (net.Conn, error) { return client, nil }, ReserveAuth: func(context.Context, string) error { return nil }, ReserveJoin: func(context.Context, string) error { return nil }, Lease: func(context.Context, string) (*sendquota.ChannelLease, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := producer.Send(ctx, twitch.OutboundMessage{Channel: "alpha", Text: " waves ", Action: true, ReplyToMessageID: "parent"}); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-written:
		if line != "@reply-parent-msg-id=parent PRIVMSG #alpha :\x01ACTION  waves \x01" {
			t.Fatalf("wire=%q", line)
		}
	case <-ctx.Done():
		t.Fatal("Send returned before synchronous write")
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("close leaked IRC reader")
	}
}
func TestProducerDoesNotTrustRoomstateInsideChatText(t *testing.T) {
	t.Parallel()
	command, _ := producerCommand(":user!u@h PRIVMSG #alpha :pretend ROOMSTATE #beta")
	if command != "PRIVMSG" {
		t.Fatalf("command=%s", command)
	}
}

func TestProducerWireBoundaryRejectsInjectionBeforeDial(t *testing.T) {
	t.Parallel()
	dialed := false
	producer, err := NewProducer(context.Background(), ProducerConfig{Login: "bot", Token: func() string { return "fake" }, Dial: func(context.Context) (net.Conn, error) { dialed = true; return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	cases := []twitch.OutboundMessage{{Channel: "alpha", Text: "hello\r\nPRIVMSG #other :injected"}, {Channel: "alpha", Text: "nul\x00body"}, {Channel: "alpha", Text: string([]byte{0xff})}, {Channel: "alpha", Text: strings.Repeat("a", 501)}, {Channel: "alpha", Text: strings.Repeat("a", 492), Action: true}, {Channel: "alpha", Text: "hello", ReplyToMessageID: "parent;other=tag"}}
	for _, message := range cases {
		if err := producer.Send(context.Background(), message); err == nil {
			t.Fatalf("unsafe wire accepted=%+v", message)
		}
	}
	if dialed {
		t.Fatal("invalid wire input opened connection")
	}
}
