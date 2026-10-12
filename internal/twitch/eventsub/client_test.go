package eventsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
)

func TestWebSocketSubscribeDeduplicateRevocation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/subscriptions" {
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("wrong token")
			}
			subscriptions.Add(1)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"total_cost":0,"max_total_cost":10}`)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		conn.Write(ctx, websocket.MessageText, []byte(`{"metadata":{"message_type":"session_welcome"},"payload":{"session":{"id":"session","keepalive_timeout_seconds":10}}}`))
		for subscriptions.Load() < int32(len(readSubscriptions())) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
		message := `{"metadata":{"message_type":"notification","message_id":"delivery","message_timestamp":"2026-01-01T00:00:00Z","subscription_type":"channel.chat.message"},"payload":{"event":{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","chatter_user_id":"2","chatter_user_login":"viewer","message_id":"chat-id","message":{"text":"hi"}}}}`
		conn.Write(ctx, websocket.MessageText, []byte(message))
		conn.Write(ctx, websocket.MessageText, []byte(message))
		conn.Write(ctx, websocket.MessageText, []byte(`{"metadata":{"message_type":"revocation"},"payload":{"subscription":{"status":"authorization_revoked"}}}`))
		<-ctx.Done()
	}))
	defer server.Close()
	client, err := NewClient(Config{AccountLogin: "fixture", ClientID: "client", UserID: "user", Token: func() string { return "secret" }, Channels: []Channel{{"alpha", "1"}}, URL: "ws" + strings.TrimPrefix(server.URL, "http"), APIURL: server.URL + "/subscriptions"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var messages, ready, revoked int
	for event := range events {
		switch event.Kind {
		case twitch.EventMessage:
			messages++
			if event.Message.ID != "chat-id" || event.Message.DeliveryID != "delivery" {
				t.Fatalf("IDs mixed: %+v", event.Message)
			}
		case twitch.EventChannelReady:
			ready++
		case twitch.EventError:
			revoked++
		}
	}
	if messages != 1 || ready != 1 || revoked != 1 || subscriptions.Load() != int32(len(readSubscriptions())) {
		t.Fatalf("messages=%d ready=%d revoked=%d subscriptions=%d", messages, ready, revoked, subscriptions.Load())
	}
}
func TestWelcomeTimeout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	client, _ := NewClient(Config{AccountLogin: "fixture", ClientID: "client", UserID: "user", Token: func() string { return "secret" }, Channels: []Channel{{"alpha", "1"}}, URL: "ws" + strings.TrimPrefix(server.URL, "http"), ConnectTimeout: 10 * time.Millisecond})
	if _, err := client.Connect(context.Background()); err == nil {
		t.Fatal("missing welcome accepted")
	}
}
func TestModerationAndSharedSource(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var frame envelope
	json.Unmarshal([]byte(`{"metadata":{"message_type":"notification","message_id":"delivery","subscription_type":"channel.chat.message_delete"},"payload":{"event":{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","target_user_id":"2","message_id":"removed"}}}`), &frame)
	event, err := normalize(frame)
	if err != nil {
		t.Fatal(err)
	}
	if event.Moderation.TargetMessageID != "removed" || event.Moderation.TargetUserID != "2" {
		t.Fatalf("delete: %+v", event)
	}
}

func TestDirectedHandoverRetainsOldNotifications(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fixtureCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var subscriptions atomic.Int32
	oldDelivered := make(chan struct{})
	subscribed := make(chan struct{})
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/subscriptions" {
			if subscriptions.Add(1) == int32(len(readSubscriptions())) {
				close(subscribed)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"total_cost":0,"max_total_cost":10}`)
			return
		}
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer socket.CloseNow()
		// Fixture sockets stay live until explicit client cleanup.
		ctx := fixtureCtx
		if r.URL.Path == "/next" {
			select {
			case <-oldDelivered:
			case <-ctx.Done():
				return
			}
		}
		socket.Write(ctx, websocket.MessageText, []byte(`{"metadata":{"message_type":"session_welcome"},"payload":{"session":{"id":"session","keepalive_timeout_seconds":10}}}`))
		if r.URL.Path == "/ws" {
			select {
			case <-subscribed:
			case <-ctx.Done():
				return
			}
			data := fmt.Sprintf(`{"metadata":{"message_type":"session_reconnect"},"payload":{"session":{"reconnect_url":%q}}}`, endpoint+"/next")
			socket.Write(ctx, websocket.MessageText, []byte(data))
			socket.Write(ctx, websocket.MessageText, []byte(`{"metadata":{"message_type":"notification","message_id":"during-handover","subscription_type":"channel.chat.message"},"payload":{"event":{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","message_id":"chat-id","message":{"text":"handover"}}}}`))
			close(oldDelivered)
			_, _, _ = socket.Read(ctx)
		} else {
			socket.Write(ctx, websocket.MessageText, []byte(`{"metadata":{"message_type":"notification","message_id":"new","subscription_type":"channel.chat.message"},"payload":{"event":{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","message_id":"new-id","message":{"text":"new"}}}}`))
			_, _, _ = socket.Read(ctx)
		}
	}))
	defer func() { cancel(); server.Close() }()
	endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	client, _ := NewClient(Config{AccountLogin: "fixture", ClientID: "client", UserID: "user", Token: func() string { return "secret" }, Channels: []Channel{{"alpha", "1"}}, URL: endpoint + "/ws", APIURL: server.URL + "/subscriptions"})
	ctx := fixtureCtx
	defer client.Close()
	events, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	messages := 0
	for messages < 2 {
		select {
		case e := <-events:
			if e.Kind == twitch.EventMessage {
				messages++
			}
			if e.Kind == twitch.EventConnection || e.Kind == twitch.EventError {
				t.Fatalf("false handover gap: %+v", e)
			}
		case <-ctx.Done():
			t.Fatal("handover lost notification")
		}
	}
	if subscriptions.Load() != int32(len(readSubscriptions())) {
		t.Fatal("handover resubscribed")
	}
}

func TestEventSubSharesAccountChannelLimitBeforeOpeningSocket(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ircChannels := make([]string, 75)
	for i := range ircChannels {
		ircChannels[i] = fmt.Sprintf("irc%d", i)
	}
	ircLease, err := sendquota.ReserveChannels(context.Background(), "fixture", ircChannels)
	if err != nil {
		t.Fatal(err)
	}
	defer ircLease.Close()
	channels := make([]Channel, 26)
	for i := range channels {
		channels[i] = Channel{Login: fmt.Sprintf("eventsub%d", i), ID: fmt.Sprint(i + 1)}
	}
	client, err := NewClient(Config{AccountLogin: "fixture", ClientID: "client", UserID: "user", Token: func() string { return "secret" }, Channels: channels, URL: "ws://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Connect(context.Background()); !errors.Is(err, sendquota.ErrChannelLimit) {
		t.Fatalf("account union exceeded without veto: %v", err)
	}
}
