package app

import (
	"testing"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func TestRecordActivityFromMessageClassifiesRaidsAndSubs(t *testing.T) {
	model := newMockModel("example", config.Default())

	model.recordActivityFromMessage(twitch.ChatMessage{
		Channel:       "example",
		Type:          twitch.MessageTypeNotice,
		SystemEventID: "raid",
		Text:          "RaiderName is raiding with 42 viewers!",
		Timestamp:     time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC),
	})
	model.recordActivityFromMessage(twitch.ChatMessage{
		Channel:       "example",
		Type:          twitch.MessageTypeNotice,
		SystemEventID: "resub",
		Text:          "ViewerName subscribed for 6 months!",
	})
	// Plain chat and twi's own generic "system" banners are not activity.
	model.recordActivityFromMessage(twitch.ChatMessage{Channel: "example", Type: twitch.MessageTypeChat, Text: "hello"})
	model.recordActivityFromMessage(twitch.ChatMessage{Channel: "example", Type: twitch.MessageTypeSystem, Text: "Mock chat is ready."})

	if len(model.activity.activityLog) != 2 {
		t.Fatalf("activityLog = %#v, want 2 entries (raid, resub)", model.activity.activityLog)
	}
	if model.activity.activityLog[0].Kind != activityIRCEvent || model.activity.activityLog[0].Channel != "example" {
		t.Fatalf("entry[0] = %#v, want irc_event in #example", model.activity.activityLog[0])
	}
	if model.activity.activityLog[0].At != time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC) {
		t.Fatalf("entry[0].At = %v, want message timestamp preserved", model.activity.activityLog[0].At)
	}
}

func TestRecordActivityFromMessageClassifiesCheers(t *testing.T) {
	model := newMockModel("example", config.Default())

	model.recordActivityFromMessage(twitch.ChatMessage{
		Channel:     "example",
		Type:        twitch.MessageTypeChat,
		DisplayName: "Cheerer",
		Text:        "Cheer100 nice stream!",
		Bits:        100,
		Timestamp:   time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC),
	})
	// A plain chat message with no bits is not a cheer.
	model.recordActivityFromMessage(twitch.ChatMessage{Channel: "example", Type: twitch.MessageTypeChat, Text: "hello"})

	if len(model.activity.activityLog) != 1 {
		t.Fatalf("activityLog = %#v, want 1 cheer entry", model.activity.activityLog)
	}
	entry := model.activity.activityLog[0]
	if entry.Kind != activityCheer || entry.Channel != "example" {
		t.Fatalf("entry = %#v, want Kind=cheer in #example", entry)
	}
	if entry.Text != "Cheerer cheered 100 bits" {
		t.Fatalf("entry.Text = %q, want %q", entry.Text, "Cheerer cheered 100 bits")
	}
}

func TestRecordActivityFromMessageCheerUsesSingularBit(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.recordActivityFromMessage(twitch.ChatMessage{Channel: "example", Type: twitch.MessageTypeChat, DisplayName: "Cheerer", Bits: 1})
	if len(model.activity.activityLog) != 1 || model.activity.activityLog[0].Text != "Cheerer cheered 1 bit" {
		t.Fatalf("activityLog = %#v, want singular \"1 bit\"", model.activity.activityLog)
	}
}

func TestApplyNewFollowerActivityEstablishesBaselineSilently(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.applyNewFollowerActivity([]twitch.Follower{
		{UserID: "1", UserName: "First"},
		{UserID: "2", UserName: "Second"},
	})
	if len(model.activity.activityLog) != 0 {
		t.Fatalf("activityLog after first poll = %#v, want empty (baseline only)", model.activity.activityLog)
	}
	if len(model.activity.seenFollowerIDs) != 2 {
		t.Fatalf("seenFollowerIDs = %#v, want 2 entries", model.activity.seenFollowerIDs)
	}
}

func TestApplyNewFollowerActivityDetectsNewFollowersAfterBaseline(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.applyNewFollowerActivity([]twitch.Follower{{UserID: "1", UserName: "First"}})

	model.applyNewFollowerActivity([]twitch.Follower{
		{UserID: "2", UserName: "Second", FollowedAt: time.Date(2026, 7, 14, 21, 0, 0, 0, time.UTC)},
		{UserID: "1", UserName: "First"},
	})
	if len(model.activity.activityLog) != 1 {
		t.Fatalf("activityLog = %#v, want 1 new-follower entry", model.activity.activityLog)
	}
	if model.activity.activityLog[0].Kind != activityFollow || model.activity.activityLog[0].Text != "Second followed" {
		t.Fatalf("entry = %#v, want Kind=follow Text=\"Second followed\"", model.activity.activityLog[0])
	}

	// Polling again with the same data must not re-report the same follower.
	model.applyNewFollowerActivity([]twitch.Follower{
		{UserID: "2", UserName: "Second"},
		{UserID: "1", UserName: "First"},
	})
	if len(model.activity.activityLog) != 1 {
		t.Fatalf("activityLog after repeat poll = %#v, want still 1 entry", model.activity.activityLog)
	}
}

func TestAppendActivityBoundsLogSize(t *testing.T) {
	model := newMockModel("example", config.Default())
	for i := 0; i < maxActivityEntries+10; i++ {
		model.appendActivity(activityEntry{Kind: activityIRCEvent, Text: "entry"})
	}
	if len(model.activity.activityLog) != maxActivityEntries {
		t.Fatalf("activityLog length = %d, want bounded to %d", len(model.activity.activityLog), maxActivityEntries)
	}
}
