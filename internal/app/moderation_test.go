package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func moderationTestModel(t *testing.T) shellModel {
	t.Helper()
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockModelWithClock("example", cfg, clock)

	state := model.activeChannelState()
	state.messages = []twitch.ChatMessage{
		{ID: "a1", Channel: "example", AuthorLogin: "alice", DisplayName: "Alice", Text: "hello everyone", Type: twitch.MessageTypeChat},
		{ID: "b1", Channel: "example", AuthorLogin: "badactor", DisplayName: "BadActor", Text: "an abusive slur goes here", Type: twitch.MessageTypeChat},
		{ID: "b2", Channel: "example", AuthorLogin: "badactor", DisplayName: "BadActor", Text: "and another one", Type: twitch.MessageTypeChat},
		{ID: "a2", Channel: "example", AuthorLogin: "alice", DisplayName: "Alice", Text: "unrelated chatter", Type: twitch.MessageTypeChat},
	}
	return model
}

// TestModerationDeleteDoesNotReprintRemovedText is the regression this whole
// path exists for. Twitch's CLEARMSG carries the deleted message body, and
// the previous implementation rendered it into a fresh visible notice - so
// deleting a message put its text on screen a second time, on a terminal that
// is frequently being streamed.
func TestModerationDeleteDoesNotReprintRemovedText(t *testing.T) {
	model := moderationTestModel(t)
	const removed = "an abusive slur goes here"

	model.applyModeration(twitch.ModerationEvent{
		Type:            twitch.ModerationMessageDeleted,
		Channel:         "example",
		TargetLogin:     "badactor",
		TargetMessageID: "b1",
		Text:            removed,
		Timestamp:       time.Now(),
	})

	state := model.activeChannelState()
	occurrences := 0
	for _, msg := range state.messages {
		if strings.Contains(msg.Text, removed) {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("removed text appears in %d retained messages, want exactly 1 (the original, now flagged deleted)", occurrences)
	}
	if len(state.messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4; a moderation action must not append a message", len(state.messages))
	}
	for _, msg := range state.messages {
		if msg.ID == "b1" && !msg.Deleted {
			t.Fatal("target message b1 was not flagged deleted")
		}
		if msg.ID != "b1" && msg.Deleted {
			t.Fatalf("message %s was flagged deleted but was not the target", msg.ID)
		}
	}
}

// TestModerationDeleteRemovesTextFromRenderedOutput closes the loop: flagging
// the message is only useful if the words actually leave the screen.
func TestModerationDeleteRemovesTextFromRenderedOutput(t *testing.T) {
	model := moderationTestModel(t)
	const removed = "an abusive slur goes here"

	if !strings.Contains(ansi.Strip(strings.Join(model.chatRows(model.layout()), "\n")), removed) {
		t.Fatal("test setup: the message under test is not on screen before deletion")
	}

	model.applyModeration(twitch.ModerationEvent{
		Type:            twitch.ModerationMessageDeleted,
		Channel:         "example",
		TargetMessageID: "b1",
		Text:            removed,
	})

	rendered := ansi.Strip(strings.Join(model.chatRows(model.layout()), "\n"))
	if strings.Contains(rendered, removed) {
		t.Fatal("deleted message text is still rendered on screen")
	}
	if !strings.Contains(rendered, "message deleted") {
		t.Fatal("no deletion marker rendered in place of the removed message")
	}
	if !strings.Contains(rendered, "unrelated chatter") {
		t.Fatal("deleting one message removed unrelated messages from the view")
	}
}

func TestModerationBanRedactsEveryMessageFromTarget(t *testing.T) {
	for _, kind := range []twitch.ModerationType{twitch.ModerationUserBanned, twitch.ModerationUserTimedOut} {
		t.Run(string(kind), func(t *testing.T) {
			model := moderationTestModel(t)
			model.applyModeration(twitch.ModerationEvent{
				Type:        kind,
				Channel:     "example",
				TargetLogin: "BadActor",
			})

			state := model.activeChannelState()
			for _, msg := range state.messages {
				wantDeleted := msg.AuthorLogin == "badactor"
				if msg.Deleted != wantDeleted {
					t.Fatalf("message %s (author %s) Deleted = %v, want %v",
						msg.ID, msg.AuthorLogin, msg.Deleted, wantDeleted)
				}
			}
		})
	}
}

func TestModerationChatClearedEmptiesBuffer(t *testing.T) {
	model := moderationTestModel(t)
	state := model.activeChannelState()
	state.scrollOffset = 12

	model.applyModeration(twitch.ModerationEvent{
		Type:    twitch.ModerationChatCleared,
		Channel: "example",
	})

	if len(state.messages) != 0 {
		t.Fatalf("len(messages) = %d, want 0 after a chat clear", len(state.messages))
	}
	if state.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 after a chat clear", state.scrollOffset)
	}
}

// TestModerationTargetsNamedChannelNotActive guards the multi-channel case: a
// deletion in a background channel must not redact the channel on screen.
func TestModerationTargetsNamedChannelNotActive(t *testing.T) {
	forceColorProfile(t)
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"example", "other"}
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockModelWithClock("example", cfg, clock)

	active := model.channels.ensure("example")
	active.messages = []twitch.ChatMessage{
		{ID: "keep", Channel: "example", AuthorLogin: "badactor", Text: "here"},
	}
	other := model.channels.ensure("other")
	other.messages = []twitch.ChatMessage{
		{ID: "gone", Channel: "other", AuthorLogin: "badactor", Text: "there"},
	}

	model.applyModeration(twitch.ModerationEvent{
		Type:        twitch.ModerationUserBanned,
		Channel:     "other",
		TargetLogin: "badactor",
	})

	if active.messages[0].Deleted {
		t.Fatal("a ban in #other redacted a message in #example")
	}
	if !other.messages[0].Deleted {
		t.Fatal("a ban in #other did not redact that channel's message")
	}
}

// TestLiveChatClientExposesModerationsOffTheMessageStream pins the transport
// contract: moderation events must arrive on their own channel so consumers
// can redact, and must not appear as chat messages.
func TestLiveChatClientExposesModerationsOffTheMessageStream(t *testing.T) {
	var client any = &LiveChatClient{}
	if _, ok := client.(ModerationSource); !ok {
		t.Fatal("*LiveChatClient does not implement ModerationSource")
	}
}
