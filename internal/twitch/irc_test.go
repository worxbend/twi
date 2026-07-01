package twitch

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	irc "github.com/gempir/go-twitch-irc/v4"
)

func TestNormalizeIRCPrivateMessage(t *testing.T) {
	raw := `@badge-info=subscriber/24;badges=moderator/1,subscriber/24,game-developer/alpha;color=#1FD2FF;display-name=Karl_Kons;emotes=28087:0-6;flags=;id=7c95beea-a7ac-4c10-9e0a-d7dbf163c038;mod=1;reply-parent-msg-id=parent-1;reply-parent-user-id=parent-user-1;reply-parent-user-login=parent_login;reply-parent-display-name=ParentDisplay;reply-parent-msg-body=hello\sworld;room-id=11148817;subscriber=1;tmi-sent-ts=1540140252828;turbo=0;user-id=68706331;user-type=mod :karl_kons!karl_kons@karl_kons.tmi.twitch.tv PRIVMSG #pajlada :WutFace hello @friend`
	parsed, ok := irc.ParseMessage(raw).(*irc.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizeIRCPrivateMessage(*parsed)

	if event.Kind != EventMessage {
		t.Fatalf("kind = %q, want %q", event.Kind, EventMessage)
	}
	msg := event.Message
	if got, want := msg.ID, "7c95beea-a7ac-4c10-9e0a-d7dbf163c038"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := msg.Channel, "pajlada"; got != want {
		t.Fatalf("Channel = %q, want %q", got, want)
	}
	if got, want := msg.Timestamp, time.Unix(0, 1540140252828*int64(time.Millisecond)); !got.Equal(want) {
		t.Fatalf("Timestamp = %s, want %s", got, want)
	}
	if got, want := msg.AuthorLogin, "karl_kons"; got != want {
		t.Fatalf("AuthorLogin = %q, want %q", got, want)
	}
	if got, want := msg.AuthorID, "68706331"; got != want {
		t.Fatalf("AuthorID = %q, want %q", got, want)
	}
	if got, want := msg.DisplayName, "Karl_Kons"; got != want {
		t.Fatalf("DisplayName = %q, want %q", got, want)
	}
	if got, want := msg.AuthorColor, "#1FD2FF"; got != want {
		t.Fatalf("AuthorColor = %q, want %q", got, want)
	}
	wantBadges := []Badge{
		{SetID: "moderator", ID: "1"},
		{SetID: "subscriber", ID: "24", Info: "24"},
		{SetID: "game-developer", ID: "alpha"},
	}
	if !reflect.DeepEqual(msg.Badges, wantBadges) {
		t.Fatalf("Badges = %#v, want %#v", msg.Badges, wantBadges)
	}
	if got, want := msg.Text, "WutFace hello @friend"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
	if got, want := msg.Type, MessageTypeChat; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	wantEmotes := []Emote{{ID: "28087", Name: "WutFace", Start: 0, End: 6}}
	if !reflect.DeepEqual(msg.Emotes, wantEmotes) {
		t.Fatalf("Emotes = %#v, want %#v", msg.Emotes, wantEmotes)
	}
	wantFragments := []MessageFragment{
		{Type: FragmentEmote, Text: "WutFace", Ref: AssetRef{Kind: "twitch_emote", ID: "28087"}},
		{Type: FragmentText, Text: " hello "},
		{Type: FragmentMention, Text: "@friend"},
	}
	if !reflect.DeepEqual(msg.Fragments, wantFragments) {
		t.Fatalf("Fragments = %#v, want %#v", msg.Fragments, wantFragments)
	}
	if msg.Reply == nil {
		t.Fatal("Reply is nil")
	}
	if got, want := *msg.Reply, (Reply{
		ParentMessageID: "parent-1",
		ParentAuthorID:  "parent-user-1",
		ParentLogin:     "parent_login",
		ParentAuthor:    "ParentDisplay",
		ParentText:      "hello world",
	}); got != want {
		t.Fatalf("Reply = %#v, want %#v", got, want)
	}
	if got, want := msg.RawTags["id"], msg.ID; got != want {
		t.Fatalf("RawTags[id] = %q, want %q", got, want)
	}

	parsed.Tags["id"] = "mutated"
	if got := msg.RawTags["id"]; got == "mutated" {
		t.Fatalf("RawTags shares the callback tag map")
	}
}

func TestNormalizeIRCPrivateActionMessage(t *testing.T) {
	raw := "@badges=;color=#008000;display-name=Zugren;emotes=;id=action-1;room-id=11148817;tmi-sent-ts=1490382456776;user-id=65897106 :zugren!zugren@zugren.tmi.twitch.tv PRIVMSG #pajlada :\x01ACTION waves at chat\x01"
	parsed, ok := irc.ParseMessage(raw).(*irc.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizeIRCPrivateMessage(*parsed)

	if got, want := event.Message.Type, MessageTypeAction; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	if got, want := event.Message.Text, "waves at chat"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
}

func TestNormalizeIRCProtocolEvents(t *testing.T) {
	at := time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC)
	disconnectErr := errors.New("socket closed")

	tests := []struct {
		name  string
		event Event
		check func(*testing.T, Event)
	}{
		{
			name:  "notice",
			event: normalizeParsedFixture(t, `@msg-id=subs_on :tmi.twitch.tv NOTICE #pajlada :This room is now in subscribers-only mode.`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventNotice || event.Notice.ID != "subs_on" || event.Notice.Channel != "pajlada" {
					t.Fatalf("notice event = %#v", event)
				}
			},
		},
		{
			name:  "usernotice",
			event: normalizeParsedFixture(t, `@badge-info=subscriber/3;badges=subscriber/3;color=#00FF7F;display-name=FletcherCodes;emotes=64138:0-8;flags=;id=e4090aa9-8079-41ff-904d-64c7a2193ee0;login=fletchercodes;mod=0;msg-id=ritual;msg-param-ritual-name=new_chatter;room-id=408892348;system-msg=@FletcherCodes\sis\snew\shere.\sSay\shello!;tmi-sent-ts=1551487438943;user-id=412636239 :tmi.twitch.tv USERNOTICE #clippyassistant :SeemsGood`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventUserNotice {
					t.Fatalf("kind = %q, want %q", event.Kind, EventUserNotice)
				}
				if got, want := event.UserNotice.MessageID, "ritual"; got != want {
					t.Fatalf("MessageID = %q, want %q", got, want)
				}
				if got, want := event.UserNotice.AuthorLogin, "fletchercodes"; got != want {
					t.Fatalf("AuthorLogin = %q, want %q", got, want)
				}
				if got, want := event.UserNotice.SystemText, "@FletcherCodes is new here. Say hello!"; got != want {
					t.Fatalf("SystemText = %q, want %q", got, want)
				}
				if got, want := event.UserNotice.Params["msg-param-ritual-name"], "new_chatter"; got != want {
					t.Fatalf("ritual param = %q, want %q", got, want)
				}
				if got, want := event.UserNotice.Emotes, []Emote{{ID: "64138", Name: "SeemsGood", Start: 0, End: 8}}; !reflect.DeepEqual(got, want) {
					t.Fatalf("Emotes = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "roomstate",
			event: normalizeParsedFixture(t, `@emote-only=0;followers-only=-1;r9k=0;rituals=0;room-id=11148817;slow=10;subs-only=1 :tmi.twitch.tv ROOMSTATE #pajlada`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				want := map[string]int{"emote-only": 0, "followers-only": -1, "r9k": 0, "rituals": 0, "slow": 10, "subs-only": 1}
				if event.Kind != EventRoomState || event.RoomState.Channel != "pajlada" || !reflect.DeepEqual(event.RoomState.State, want) {
					t.Fatalf("room state event = %#v, want state %#v", event, want)
				}
			},
		},
		{
			name:  "clearchat timeout",
			event: normalizeParsedFixture(t, `@ban-duration=600;room-id=11148817;target-user-id=123;tmi-sent-ts=1540140252828 :tmi.twitch.tv CLEARCHAT #pajlada :badviewer`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventModeration || event.Moderation.Type != ModerationUserTimedOut || event.Moderation.BanDuration != 10*time.Minute {
					t.Fatalf("clearchat event = %#v", event)
				}
			},
		},
		{
			name:  "clearmsg",
			event: normalizeParsedFixture(t, `@login=badviewer;target-msg-id=target-1 :tmi.twitch.tv CLEARMSG #pajlada :removed text`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventModeration || event.Moderation.Type != ModerationMessageDeleted || event.Moderation.TargetMessageID != "target-1" {
					t.Fatalf("clearmsg event = %#v", event)
				}
			},
		},
		{
			name:  "userstate",
			event: normalizeParsedFixture(t, `@badge-info=;badges=moderator/1;color=#1FD2FF;display-name=Karl_Kons;emote-sets=1,2;user-id=68706331 :tmi.twitch.tv USERSTATE #pajlada`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventUserState || event.UserState.Channel != "pajlada" || event.UserState.DisplayName != "Karl_Kons" {
					t.Fatalf("userstate event = %#v", event)
				}
				if got, want := event.UserState.EmoteSets, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("EmoteSets = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "reconnect",
			event: NormalizeIRCReconnectMessage(irc.ReconnectMessage{Raw: ":tmi.twitch.tv RECONNECT"}, at),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventConnection || event.Connection.Type != ConnectionEventReconnect || !event.Connection.At.Equal(at) {
					t.Fatalf("reconnect event = %#v", event)
				}
			},
		},
		{
			name:  "connect",
			event: NormalizeIRCConnect(at),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventConnection || event.Connection.Type != ConnectionEventConnect || !event.Connection.At.Equal(at) {
					t.Fatalf("connect event = %#v", event)
				}
			},
		},
		{
			name:  "disconnect",
			event: NormalizeIRCDisconnect(disconnectErr, at),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventConnection || event.Connection.Type != ConnectionEventDisconnect || !errors.Is(event.Err, disconnectErr) {
					t.Fatalf("disconnect event = %#v", event)
				}
			},
		},
		{
			name:  "raw fallback",
			event: normalizeParsedFixture(t, `@debug=1 :tmi.twitch.tv UNSUPPORTED #pajlada :payload text`),
			check: func(t *testing.T, event Event) {
				t.Helper()
				if event.Kind != EventRaw || !strings.Contains(event.Raw.TODO, "TODO") || event.Raw.RawType != "UNSUPPORTED" {
					t.Fatalf("raw fallback event = %#v", event)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.event)
		})
	}
}

func normalizeParsedFixture(t *testing.T, raw string) Event {
	t.Helper()
	return NormalizeIRCMessage(irc.ParseMessage(raw))
}
