package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/twitch"
)

func TestHistorySourceReplaysFollowsAndCannotSend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "history.sqlite3")
	writer, err := history.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	record := automation.Record{SchemaVersion: 1, Type: "message", ID: "first", Channel: automation.Channel{Login: "alpha"}, Author: automation.Author{Login: "viewer"}, Text: "first observed message", EventAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Transport: "irc"}
	if _, _, err := writer.Append(ctx, record); err != nil {
		t.Fatal(err)
	}
	source, err := NewHistoryChatSource(ctx, path, []string{"#Alpha"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, ok := any(source).(ChatSender); ok {
		t.Fatal("history reader unexpectedly implements sends")
	}
	receive := func(want string) {
		t.Helper()
		select {
		case message := <-source.Messages():
			if message.ID != want {
				t.Fatalf("message=%+v wantID=%s", message, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("history message timed out")
		}
	}
	receive("first")
	// This append crosses the frozen replay boundary; following must emit it once.
	record.ID = "second"
	record.Text = "second observed message"
	record.ReceivedAt = time.Now().UTC()
	record.EventAt = record.ReceivedAt
	if _, _, err := writer.Append(ctx, record); err != nil {
		t.Fatal(err)
	}
	receive("second")
	moderation := automation.Record{SchemaVersion: 1, Type: string(twitch.ModerationMessageDeleted), Channel: record.Channel, TargetMessageID: "second", ReceivedAt: time.Now().UTC(), EventAt: time.Now().UTC()}
	if _, _, err := writer.Append(ctx, moderation); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-source.Moderations():
		if event.TargetMessageID != "second" {
			t.Fatalf("moderation=%+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("follow moderation timed out")
	}
	select {
	case duplicate := <-source.Messages():
		t.Fatalf("duplicate message=%+v", duplicate)
	default:
	}
}

func TestHistoryMapperRetainsActionAndModerationMetadata(t *testing.T) {
	t.Parallel()
	expires := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC)
	record := automation.Record{Type: "message", MessageKind: "action", Text: "waves", Channel: automation.Channel{ID: "observed-id", Login: "observed"}, SourceChannel: &automation.Channel{ID: "source-id", Login: "source"}}
	message := historyChatMessage(record)
	if message.Type != twitch.MessageTypeAction || message.SourceChannelLogin != "source" {
		t.Fatalf("action metadata lost: %+v", message)
	}
	record.Type = string(twitch.ModerationUserTimedOut)
	record.BanDurationMS = 30000
	record.TimeoutExpiresAt = &expires
	record.TargetUserID = "target"
	record.DeliveryID = "delivery"
	event := historyModerationEvent(record)
	if event.BanDuration != 30*time.Second || !event.ExpiresAt.Equal(expires) || event.SourceChannelID != "source-id" || event.TargetUserID != "target" || event.DeliveryID != "delivery" {
		t.Fatalf("moderation metadata lost: %+v", event)
	}
	record.BanDurationMS = 1<<63 - 1
	if event := historyModerationEvent(record); event.BanDuration != 0 {
		t.Fatalf("invalid duration overflow: %v", event.BanDuration)
	}
}

func TestHistoryMapperRetainsNoticeSystemTextAndActionActor(t *testing.T) {
	t.Parallel()
	notice := automation.Record{Type: "user_notice", Reason: "sub", ID: "unique", SystemText: "viewer subscribed", Text: "hello"}
	message := historyChatMessage(notice)
	if message.Text != "viewer subscribed hello" || message.Type != twitch.MessageTypeNotice || message.SystemEventID != "sub" || message.ID != "unique" {
		t.Fatalf("notice mapping=%+v", message)
	}
	notice.Text = notice.SystemText
	if message := historyChatMessage(notice); message.Text != "viewer subscribed" {
		t.Fatalf("duplicated notice body=%+v", message)
	}
	action := historyModerationEvent(automation.Record{Type: string(twitch.ModerationAction), Action: "vip", Moderator: &automation.Author{ID: "moderator-id", Login: "moderator"}, Reason: "reason"})
	if action.Type != twitch.ModerationAction || action.Action != "vip" || action.ModeratorID != "moderator-id" || action.ModeratorLogin != "moderator" || action.Reason != "reason" {
		t.Fatalf("action mapping=%+v", action)
	}
}
