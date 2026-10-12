package chatservice_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/eventsub"
	"github.com/worxbend/twi/internal/twitch/helix"
)

// This suite is opt-in and read-only. It never logs chat text or credentials.
// It closes one real subscribed socket and requires fresh Twitch subscriptions,
// observable capture loss, recovered readiness, and bounded cancellation.
func TestLiveControlledEventSubReconnect(t *testing.T) {
	channel := os.Getenv("TWI_LIVE_VALIDATION_CHANNEL")
	if channel == "" {
		t.Skip("explicit controlled-channel live validation is not enabled")
	}
	if channel != "worxbend" {
		t.Fatal("live fixture restricted to the authorized controlled channel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	clientID := os.Getenv("TWITCH_CLIENT_ID")
	token := strings.TrimPrefix(os.Getenv("TWITCH_ACCESS_TOKEN"), "oauth:")
	identity, err := helix.NewOAuthTokenValidator(helix.OAuthTokenValidatorConfig{}).ValidateToken(ctx, twitch.TokenCredentials{Username: channel, OAuthToken: token, ClientID: clientID})
	if err != nil || identity.Identity.Login != channel || identity.ClientID != clientID || identity.Identity.UserID == "" {
		t.Fatal("controlled live identity validation failed")
	}
	if len(auth.MissingScopes(identity.Scopes, []auth.Scope{auth.ScopeUserReadChat})) > 0 {
		t.Fatal("EventSub read grant missing")
	}
	created := make(chan *eventsub.Client, 3)
	service, err := chatservice.New(chatservice.Config{Channels: []string{channel}, ConnectTimeout: 35 * time.Second, MaxReconnects: 2, Factory: func(context.Context) (chatservice.Reader, error) {
		reader, err := eventsub.NewClient(eventsub.Config{AccountLogin: channel, ClientID: clientID, UserID: identity.Identity.UserID, Token: func() string { return token }, Channels: []eventsub.Channel{{Login: channel, ID: identity.Identity.UserID}}, Scopes: []auth.Scope{auth.ScopeUserReadChat}})
		if err == nil {
			created <- reader
		}
		return reader, err
	}})
	if err != nil {
		t.Fatal("live service configuration failed")
	}
	stream, err := service.Run(ctx)
	if err != nil {
		t.Fatal("live service startup failed")
	}
	first := <-created
	ready, gaps := 0, 0
	for ready < 2 {
		select {
		case event, ok := <-stream:
			if !ok {
				t.Fatal("live service closed before recovery")
			}
			switch event.Kind {
			case chatservice.EventReady:
				ready++
				if ready == 1 {
					if err := first.Close(); err != nil {
						t.Fatal("controlled live socket close failed")
					}
				}
			case chatservice.EventGap:
				gaps++
			case chatservice.EventFailed:
				t.Fatal("live reconnection failed")
			}
		case <-ctx.Done():
			t.Fatal("live recovery deadline exceeded")
		}
	}
	if gaps == 0 || len(created) != 1 {
		t.Fatal("live recovery did not create exactly one replacement and capture gap")
	}
	cancel()
	closed := make(chan struct{})
	go func() {
		for range stream {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("live cancellation did not close stream")
	}
	if report := os.Getenv("TWI_LIVE_REPORT"); report != "" {
		data, _ := json.MarshalIndent(map[string]any{"schema_version": 1, "channel": channel, "checks": map[string]string{"actual_eventsub_subscriptions": "passed", "forced_socket_close_recovery": "passed", "capture_gap": "passed", "bounded_cancellation": "passed"}, "readiness_generations": ready, "gaps": gaps}, "", "  ")
		file, err := os.OpenFile(report, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal("cannot create private live evidence report")
		}
		_, writeErr := file.Write(append(data, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot save private live evidence report")
		}
	}
}
