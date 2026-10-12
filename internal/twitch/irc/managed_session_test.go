package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/twitch"
)

func managedPipe(t *testing.T, channels []string) (*managedSession, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	session := newManagedSession("viewer", "oauth:test-token", channels)
	session.dial = func(context.Context) (net.Conn, error) { return client, nil }
	session.reserve = func(context.Context, string, int) error { return nil }
	return session, server
}
func initialSetup(t *testing.T, server net.Conn) *bufio.Reader {
	t.Helper()
	reader := bufio.NewReader(server)
	for _, prefix := range []string{"CAP REQ :twitch.tv/tags twitch.tv/commands twitch.tv/membership", "PASS oauth:test-token", "NICK viewer"} {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Error(err)
			return reader
		}
		if strings.TrimSpace(line) != prefix {
			t.Errorf("setup=%q want=%q", line, prefix)
		}
	}
	return reader
}
func sessionResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("session did not stop")
		return nil
	}
}
func TestManagedSessionDispatchAndReplies(t *testing.T) {
	session, server := managedPipe(t, []string{"alpha"})
	seen := make(chan string, 16)
	session.OnConnect(func() { seen <- "connected" })
	session.OnPrivateMessage(func(m gempir.PrivateMessage) {
		if m.Message != "hello" || m.ID != "message-id" {
			t.Errorf("message=%+v", m)
		}
		seen <- "private"
	})
	session.OnNoticeMessage(func(gempir.NoticeMessage) { seen <- "notice" })
	session.OnUserNoticeMessage(func(gempir.UserNoticeMessage) { seen <- "usernotice" })
	session.OnRoomStateMessage(func(gempir.RoomStateMessage) { seen <- "room" })
	session.OnClearChatMessage(func(gempir.ClearChatMessage) { seen <- "clearchat" })
	session.OnClearMessage(func(gempir.ClearMessage) { seen <- "clearmsg" })
	session.OnUserStateMessage(func(gempir.UserStateMessage) { seen <- "userstate" })
	session.OnUserJoinMessage(func(gempir.UserJoinMessage) { seen <- "join" })
	session.OnUserPartMessage(func(gempir.UserPartMessage) { seen <- "part" })
	session.OnUnsetMessage(func(gempir.RawMessage) { seen <- "raw" })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- session.ConnectContext(ctx) }()
	reader := initialSetup(t, server)
	fmt.Fprint(server, ":tmi.twitch.tv 001 viewer :Welcome\r\n")
	line, err := reader.ReadString('\n')
	if err != nil || line != "JOIN #alpha\r\n" {
		t.Fatalf("join=%q err=%v", line, err)
	}
	for _, wire := range []string{
		"@id=message-id;user-id=2;room-id=1 :person!person@person.tmi.twitch.tv PRIVMSG #alpha :hello",
		"@msg-id=slow_on :tmi.twitch.tv NOTICE #alpha :Slow mode enabled",
		"@msg-id=sub;room-id=1 :tmi.twitch.tv USERNOTICE #alpha :subscribed",
		"@room-id=1;slow=0 :tmi.twitch.tv ROOMSTATE #alpha",
		"@room-id=1;target-user-id=2 :tmi.twitch.tv CLEARCHAT #alpha :person",
		"@target-msg-id=message-id :tmi.twitch.tv CLEARMSG #alpha :hello",
		"@user-id=2 :tmi.twitch.tv USERSTATE #alpha",
		":person!person@person.tmi.twitch.tv JOIN #alpha",
		":person!person@person.tmi.twitch.tv PART #alpha",
		":tmi.twitch.tv 372 viewer :motd",
	} {
		fmt.Fprint(server, wire+"\r\n")
	}
	session.Say("alpha", actionWireText("waves"))
	session.Reply("alpha", "parent-id", "reply")
	action, _ := reader.ReadString('\n')
	reply, _ := reader.ReadString('\n')
	if action != "PRIVMSG #alpha :\x01ACTION waves\x01\r\n" || reply != "@reply-parent-msg-id=parent-id PRIVMSG #alpha :reply\r\n" {
		t.Fatalf("action=%q reply=%q", action, reply)
	}
	for _, want := range []string{"connected", "private", "notice", "usernotice", "room", "clearchat", "clearmsg", "userstate", "join", "part", "raw"} {
		select {
		case got := <-seen:
			if got != want {
				t.Errorf("got %s want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("missing callback %s", want)
		}
	}
	cancel()
	if !errors.Is(sessionResult(t, result), context.Canceled) {
		t.Fatal("session did not honor context")
	}
}
func TestManagedQuotaWaitDoesNotStarveKeepalives(t *testing.T) {
	session, server := managedPipe(t, []string{"alpha"})
	waiting := make(chan struct{})
	session.reserve = func(ctx context.Context, kind string, count int) error {
		if kind == "joins" {
			close(waiting)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- session.ConnectContext(ctx) }()
	reader := initialSetup(t, server)
	fmt.Fprint(server, ":tmi.twitch.tv 001 viewer :Welcome\r\n")
	<-waiting
	fmt.Fprint(server, "PING :tmi.twitch.tv\r\n")
	server.SetReadDeadline(time.Now().Add(time.Second))
	line, err := reader.ReadString('\n')
	if err != nil || line != "PONG :tmi.twitch.tv\r\n" {
		t.Fatalf("keepalive=%q err=%v", line, err)
	}
	cancel()
	if !errors.Is(sessionResult(t, result), context.Canceled) {
		t.Fatal("quota wait did not cancel")
	}
}
func TestManagedAuthAndDisconnectNeverReconnectInternally(t *testing.T) {
	for _, test := range []struct {
		name, wire string
		auth       bool
	}{{"rejected", ":tmi.twitch.tv NOTICE * :Login authentication failed\r\n", true}, {"directed", ":tmi.twitch.tv RECONNECT\r\n", false}, {"eof", "", false}} {
		t.Run(test.name, func(t *testing.T) {
			session, server := managedPipe(t, nil)
			var dials, auth atomic.Int32
			dial := session.dial
			session.dial = func(ctx context.Context) (net.Conn, error) { dials.Add(1); return dial(ctx) }
			session.reserve = func(context.Context, string, int) error { auth.Add(1); return nil }
			result := make(chan error, 1)
			go func() { result <- session.ConnectContext(context.Background()) }()
			initialSetup(t, server)
			if test.wire != "" {
				fmt.Fprint(server, test.wire)
			} else {
				server.Close()
			}
			err := sessionResult(t, result)
			if err == nil {
				t.Fatal("disconnect accepted as success")
			}
			if test.auth && !errors.Is(err, gempir.ErrLoginAuthenticationFailed) {
				t.Fatalf("authentication=%v", err)
			}
			if dials.Load() != 1 || auth.Load() != 1 {
				t.Fatalf("hidden reconnect: dials=%d auth=%d", dials.Load(), auth.Load())
			}
		})
	}
}
func TestManagedTLSAndAuthPermitsPerReplacement(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	tlsListener := tls.NewListener(listener, fixture.TLS.Clone())
	var serverWorkers sync.WaitGroup
	serverWorkers.Add(1)
	go func() {
		defer serverWorkers.Done()
		for i := 0; i < 2; i++ {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			reader := bufio.NewReader(conn)
			for n := 0; n < 3; n++ {
				if _, err = reader.ReadString('\n'); err != nil {
					conn.Close()
					return
				}
			}
			conn.Close()
		}
	}()
	var permits atomic.Int32
	for i := 0; i < 2; i++ {
		session := newManagedSession("viewer", "oauth:test-token", nil)
		session.reserve = func(context.Context, string, int) error { permits.Add(1); return nil }
		session.dial = func(ctx context.Context) (net.Conn, error) {
			dialer := tls.Dialer{Config: fixture.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
			return dialer.DialContext(ctx, "tcp", listener.Addr().String())
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := session.ConnectContext(ctx)
		cancel()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("TLS session result=%v", err)
		}
	}
	serverWorkers.Wait()
	if permits.Load() != 2 {
		t.Fatalf("replacement auth permits=%d", permits.Load())
	}
}

func TestManagedAdapterRefreshesBeforeReportingAuthFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	previousFactory := newSession
	t.Cleanup(func() { newSession = previousFactory })
	var sessions, refreshes atomic.Int32
	var servers sync.WaitGroup
	newSession = func(username, token string, channels []string) chatSession {
		session, server := managedPipe(t, channels)
		session.username = username
		session.token = token
		generation := sessions.Add(1)
		servers.Add(1)
		go func() {
			defer servers.Done()
			defer server.Close()
			reader := bufio.NewReader(server)
			for i := 0; i < 3; i++ {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if i == 1 {
					expected := "PASS oauth:first\r\n"
					if generation == 2 {
						expected = "PASS oauth:second\r\n"
					}
					if line != expected {
						t.Errorf("generation%d token mismatch", generation)
					}
				}
			}
			if generation == 1 {
				fmt.Fprint(server, ":tmi.twitch.tv NOTICE * :Login authentication failed\r\n")
				return
			}
			fmt.Fprint(server, ":tmi.twitch.tv 001 viewer :Welcome\r\n")
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			fmt.Fprint(server, "@room-id=1 :tmi.twitch.tv ROOMSTATE #alpha\r\n")
			_, _ = reader.ReadString('\n')
		}()
		return session
	}
	client, err := NewClient(Config{Username: "viewer", OAuthToken: "oauth:first", RefreshToken: "refresh", ClientID: "client", ClientSecret: "secret", Channels: []string{"alpha"}, RefreshOAuth: func(context.Context, time.Time) (OAuthRefresh, error) {
		refreshes.Add(1)
		return OAuthRefresh{AccessToken: auth.NewSecret("oauth:second"), RefreshToken: auth.NewSecret("refresh")}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	service, err := chatservice.New(chatservice.Config{Factory: func(context.Context) (chatservice.Reader, error) { return client, nil }, Channels: []string{"alpha"}, ConnectTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ready := false
	for !ready {
		select {
		case event := <-events:
			if event.Kind == chatservice.EventGap || event.Kind == chatservice.EventFailed {
				t.Fatalf("strict startup recovery failed: %+v", event)
			}
			if event.Kind == chatservice.EventReceived && event.Twitch.Kind == twitch.EventNotice && event.Twitch.Notice.AuthFailed {
				t.Fatal("recoverable rejection reported as terminal auth failure")
			}
			if event.Kind == chatservice.EventReady {
				ready = true
			}
		case <-ctx.Done():
			t.Fatal("refreshed adapter never became ready")
		}
	}
	if sessions.Load() != 2 || refreshes.Load() != 1 {
		t.Fatalf("sessions=%d refreshes=%d", sessions.Load(), refreshes.Load())
	}
	cancel()
	client.Close()
	servers.Wait()
}
