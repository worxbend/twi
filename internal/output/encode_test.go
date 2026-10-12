package output

import (
	"bytes"
	"encoding/json"
	"github.com/worxbend/twi/internal/twitch"
	"strings"
	"testing"
	"time"
)

func TestOutputContract(t *testing.T) {
	e := twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: "m", Channel: "#Alpha", AuthorLogin: "user", Text: "hello\n\x1b[2J\u202eend", RawTags: map[string]string{"oauth": "secret"}, Timestamp: time.Now()}}
	r, ok := FromEvent(e, time.Now(), "irc")
	if !ok {
		t.Fatal("missing message")
	}
	var b bytes.Buffer
	if err := Write(&b, r, "jsonl", false); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["cursor"] != nil || got["schema_version"] != float64(1) || strings.Contains(b.String(), "secret") {
		t.Fatalf("bad DTO: %s", b.String())
	}
	b.Reset()
	if err := Write(&b, r, "text", true); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\x1b\u202e") || strings.Count(b.String(), "\n") != 1 || !strings.Contains(b.String(), `\n`) {
		t.Fatalf("unsafe text %q", b.String())
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestShortOutputWrite(t *testing.T) {
	r, _ := FromEvent(twitch.Event{Kind: twitch.EventMessage}, time.Now(), "irc")
	for _, format := range []string{"jsonl", "text"} {
		if err := Write(shortWriter{}, r, format, false); err == nil {
			t.Fatal("short write accepted", format)
		}
	}
}
func TestNoticeCodesAreNotDeliveryIdentity(t *testing.T) {
	r, ok := FromEvent(twitch.Event{Kind: twitch.EventNotice, Notice: twitch.Notice{ID: "msg_ratelimit", Channel: "alpha", Text: "Slow down"}}, time.Now(), "irc")
	if !ok || r.ID != "" || r.Reason != "msg_ratelimit" {
		t.Fatal("notice code incorrectly used as stable message identity", r)
	}
}
func TestUserNoticeIdentityMatchesIRCAndEventSub(t *testing.T) {
	event := twitch.Event{Kind: twitch.EventUserNotice, UserNotice: twitch.UserNotice{ID: "unique-notice", MessageID: "sub", Channel: "alpha", SystemText: "subscription notice"}}
	r, ok := FromEvent(event, time.Now(), "eventsub")
	if !ok || r.ID != "unique-notice" || r.Reason != "sub" || r.Text != "subscription notice" {
		t.Fatal(r)
	}
}

func TestMappedMetadataOwnsCopies(t *testing.T) {
	params := map[string]string{"months": "12"}
	badges := []twitch.Badge{{SetID: "subscriber", ID: "12"}}
	fragments := []twitch.MessageFragment{{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{ID: "25"}}}
	event := twitch.Event{Kind: twitch.EventUserNotice, UserNotice: twitch.UserNotice{ID: "unique", MessageID: "resub", Channel: "alpha", Params: params, Badges: badges, Fragments: fragments}}
	record, ok := FromEvent(event, time.Now(), "eventsub")
	if !ok {
		t.Fatal("notice absent")
	}
	params["months"] = "changed"
	badges[0].ID = "changed"
	fragments[0].Text = "changed"
	if record.NoticeParameters["months"] != "12" || record.Badges[0].ID != "12" || record.Fragments[0].Text != "Kappa" {
		t.Fatalf("mapper aliases input metadata=%+v", record)
	}
	moderatorParams := map[string]string{"wait_time_seconds": "30"}
	event = twitch.Event{Kind: twitch.EventModeration, Moderation: twitch.ModerationEvent{Type: twitch.ModerationAction, Channel: "alpha", Parameters: moderatorParams, TargetName: "Viewer", ModeratorName: "Moderator", ModeratorID: "mod"}}
	record, ok = FromEvent(event, time.Now(), "eventsub")
	if !ok {
		t.Fatal("moderation absent")
	}
	moderatorParams["wait_time_seconds"] = "changed"
	if record.ModerationParameters["wait_time_seconds"] != "30" || record.Author.DisplayName != "Viewer" || record.Moderator.DisplayName != "Moderator" {
		t.Fatalf("moderation metadata=%+v", record)
	}
}
