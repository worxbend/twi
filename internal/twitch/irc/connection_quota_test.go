package irc

import (
	"context"
	"fmt"
	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/twitch"
	"testing"
	"time"
)

func TestJoinChannelLimitIsAtomic(t *testing.T) {
	original := newSession
	t.Cleanup(func() { newSession = original })
	session := &fakeIRCSession{}
	newSession = func(string, string, []string) chatSession { return session }
	channels := make([]string, 100)
	for i := range channels {
		channels[i] = fmt.Sprintf("channel%d", i)
	}
	client, err := NewClient(Config{Username: "reader", OAuthToken: "oauth:token", Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Join(context.Background(), "new-channel"); err == nil {
		t.Fatal("101st channel joined")
	}
	if len(client.channels) != 100 || len(session.joined) != 0 {
		t.Fatal("failed join changed membership")
	}
	if _, err = NewClient(Config{Username: "reader", OAuthToken: "oauth:token", Channels: append(channels, "another")}); err == nil {
		t.Fatal("oversized initial subscription accepted")
	}
	duplicates := make([]string, 101)
	for i := range duplicates {
		duplicates[i] = "channel0"
	}
	if duplicateClient, err := NewClient(Config{Username: "reader", OAuthToken: "oauth:token", Channels: duplicates}); err != nil || len(duplicateClient.channels) != 1 {
		t.Fatalf("duplicate joins count as distinct: %v", err)
	}

}

func TestIRCAuthNoticeDefersToConfiguredRecovery(t *testing.T) {
	for _, refreshAvailable := range []bool{false, true} {
		t.Run(fmt.Sprint(refreshAvailable), func(t *testing.T) {
			session := newManagedSession("reader", "oauth:old", nil)
			client := &Client{now: time.Now}
			if refreshAvailable {
				client.refresh = oauthRefreshConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"}
			}
			var events []twitch.Event
			client.registerHandlers(session, func(event twitch.Event) { events = append(events, event) })
			session.onNoticeMessage(gempir.NoticeMessage{Message: "Login authentication failed"})
			if refreshAvailable && len(events) != 0 {
				t.Fatal("recoverable auth notice escaped adapter")
			}
			if !refreshAvailable && (len(events) != 1 || !events[0].Notice.AuthFailed) {
				t.Fatal("unrecoverable auth notice suppressed")
			}
		})
	}
}
