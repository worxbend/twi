package irc

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/twitch"
)

func TestNormalizeIRCPrivateMessage(t *testing.T) {
	raw := `@badge-info=subscriber/24;badges=moderator/1,subscriber/24,game-developer/alpha;color=#1FD2FF;display-name=Karl_Kons;emotes=28087:0-6;flags=;id=7c95beea-a7ac-4c10-9e0a-d7dbf163c038;mod=1;reply-parent-msg-id=parent-1;reply-parent-user-id=parent-user-1;reply-parent-user-login=parent_login;reply-parent-display-name=ParentDisplay;reply-parent-msg-body=hello\sworld;room-id=11148817;subscriber=1;tmi-sent-ts=1540140252828;turbo=0;user-id=68706331;user-type=mod :karl_kons!karl_kons@karl_kons.tmi.twitch.tv PRIVMSG #pajlada :WutFace hello @friend`
	parsed, ok := gempir.ParseMessage(raw).(*gempir.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizePrivateMessage(*parsed)

	if event.Kind != twitch.EventMessage {
		t.Fatalf("kind = %q, want %q", event.Kind, twitch.EventMessage)
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
	wantBadges := []twitch.Badge{
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
	if got, want := msg.Type, twitch.MessageTypeChat; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	wantEmoteRef := twitch.AssetRef{Kind: "twitch_emote", ID: "28087", URL: "https://static-cdn.jtvnw.net/emoticons/v2/28087/static/light/2.0"}
	wantEmotes := []twitch.Emote{{ID: "28087", Name: "WutFace", Start: 0, End: 6, Ref: wantEmoteRef}}
	if !reflect.DeepEqual(msg.Emotes, wantEmotes) {
		t.Fatalf("Emotes = %#v, want %#v", msg.Emotes, wantEmotes)
	}
	wantFragments := []twitch.MessageFragment{
		{Type: twitch.FragmentEmote, Text: "WutFace", Ref: wantEmoteRef},
		{Type: twitch.FragmentText, Text: " hello "},
		{Type: twitch.FragmentMention, Text: "@friend"},
	}
	if !reflect.DeepEqual(msg.Fragments, wantFragments) {
		t.Fatalf("Fragments = %#v, want %#v", msg.Fragments, wantFragments)
	}
	if msg.Reply == nil {
		t.Fatal("Reply is nil")
	}
	if got, want := *msg.Reply, (twitch.Reply{
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
	parsed, ok := gempir.ParseMessage(raw).(*gempir.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizePrivateMessage(*parsed)

	if got, want := event.Message.Type, twitch.MessageTypeAction; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	if got, want := event.Message.Text, "waves at chat"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
}

func TestNormalizeIRCPrivateMessageCheerParsesBits(t *testing.T) {
	raw := `@badges=;bits=100;color=#008000;display-name=Zugren;emotes=;id=cheer-1;room-id=11148817;tmi-sent-ts=1490382456776;user-id=65897106 :zugren!zugren@zugren.tmi.twitch.tv PRIVMSG #pajlada :Cheer100 nice stream!`
	parsed, ok := gempir.ParseMessage(raw).(*gempir.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizePrivateMessage(*parsed)
	if got, want := event.Message.Bits, 100; got != want {
		t.Fatalf("Bits = %d, want %d", got, want)
	}
}

func TestNormalizeIRCPrivateMessageWithoutBitsTagIsZero(t *testing.T) {
	raw := `@badges=;color=#008000;display-name=Zugren;emotes=;id=plain-1;room-id=11148817;tmi-sent-ts=1490382456776;user-id=65897106 :zugren!zugren@zugren.tmi.twitch.tv PRIVMSG #pajlada :just chatting`
	parsed, ok := gempir.ParseMessage(raw).(*gempir.PrivateMessage)
	if !ok {
		t.Fatalf("fixture did not parse as PrivateMessage")
	}

	event := NormalizePrivateMessage(*parsed)
	if got := event.Message.Bits; got != 0 {
		t.Fatalf("Bits = %d, want 0", got)
	}
}

func TestNormalizeIRCProtocolEvents(t *testing.T) {
	at := time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC)
	disconnectErr := errors.New("socket closed")

	tests := []struct {
		name  string
		event twitch.Event
		check func(*testing.T, twitch.Event)
	}{
		{
			name:  "notice",
			event: normalizeParsedFixture(t, `@msg-id=subs_on :tmi.twitch.tv NOTICE #pajlada :This room is now in subscribers-only mode.`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventNotice || event.Notice.ID != "subs_on" || event.Notice.Channel != "pajlada" {
					t.Fatalf("notice event = %#v", event)
				}
			},
		},
		{
			name:  "usernotice",
			event: normalizeParsedFixture(t, `@badge-info=subscriber/3;badges=subscriber/3;color=#00FF7F;display-name=FletcherCodes;emotes=64138:0-8;flags=;id=e4090aa9-8079-41ff-904d-64c7a2193ee0;login=fletchercodes;mod=0;msg-id=ritual;msg-param-ritual-name=new_chatter;room-id=408892348;system-msg=@FletcherCodes\sis\snew\shere.\sSay\shello!;tmi-sent-ts=1551487438943;user-id=412636239 :tmi.twitch.tv USERNOTICE #clippyassistant :SeemsGood`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventUserNotice {
					t.Fatalf("kind = %q, want %q", event.Kind, twitch.EventUserNotice)
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
				wantRef := twitch.AssetRef{Kind: "twitch_emote", ID: "64138", URL: "https://static-cdn.jtvnw.net/emoticons/v2/64138/static/light/2.0"}
				if got, want := event.UserNotice.Emotes, []twitch.Emote{{ID: "64138", Name: "SeemsGood", Start: 0, End: 8, Ref: wantRef}}; !reflect.DeepEqual(got, want) {
					t.Fatalf("Emotes = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "roomstate",
			event: normalizeParsedFixture(t, `@emote-only=0;followers-only=-1;r9k=0;rituals=0;room-id=11148817;slow=10;subs-only=1 :tmi.twitch.tv ROOMSTATE #pajlada`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				want := map[string]int{"emote-only": 0, "followers-only": -1, "r9k": 0, "rituals": 0, "slow": 10, "subs-only": 1}
				if event.Kind != twitch.EventRoomState || event.RoomState.Channel != "pajlada" || !reflect.DeepEqual(event.RoomState.State, want) {
					t.Fatalf("room state event = %#v, want state %#v", event, want)
				}
			},
		},
		{
			name:  "clearchat timeout",
			event: normalizeParsedFixture(t, `@ban-duration=600;room-id=11148817;target-user-id=123;tmi-sent-ts=1540140252828 :tmi.twitch.tv CLEARCHAT #pajlada :badviewer`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventModeration || event.Moderation.Type != twitch.ModerationUserTimedOut || event.Moderation.BanDuration != 10*time.Minute {
					t.Fatalf("clearchat event = %#v", event)
				}
			},
		},
		{
			name:  "clearmsg",
			event: normalizeParsedFixture(t, `@login=badviewer;target-msg-id=target-1 :tmi.twitch.tv CLEARMSG #pajlada :removed text`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventModeration || event.Moderation.Type != twitch.ModerationMessageDeleted || event.Moderation.TargetMessageID != "target-1" {
					t.Fatalf("clearmsg event = %#v", event)
				}
			},
		},
		{
			name:  "userstate",
			event: normalizeParsedFixture(t, `@badge-info=;badges=moderator/1;color=#1FD2FF;display-name=Karl_Kons;emote-sets=1,2;user-id=68706331 :tmi.twitch.tv USERSTATE #pajlada`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventUserState || event.UserState.Channel != "pajlada" || event.UserState.DisplayName != "Karl_Kons" {
					t.Fatalf("userstate event = %#v", event)
				}
				if got, want := event.UserState.EmoteSets, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("EmoteSets = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "reconnect",
			event: NormalizeReconnectMessage(gempir.ReconnectMessage{Raw: ":tmi.twitch.tv RECONNECT"}, at),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventConnection || event.Connection.Type != twitch.ConnectionEventReconnect || !event.Connection.At.Equal(at) {
					t.Fatalf("reconnect event = %#v", event)
				}
			},
		},
		{
			name:  "connect",
			event: NormalizeConnect(at),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventConnection || event.Connection.Type != twitch.ConnectionEventConnect || !event.Connection.At.Equal(at) {
					t.Fatalf("connect event = %#v", event)
				}
			},
		},
		{
			name:  "disconnect",
			event: NormalizeDisconnect(disconnectErr, at),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventConnection || event.Connection.Type != twitch.ConnectionEventDisconnect || !errors.Is(event.Err, disconnectErr) {
					t.Fatalf("disconnect event = %#v", event)
				}
			},
		},
		{
			name:  "raw fallback",
			event: normalizeParsedFixture(t, `@debug=1 :tmi.twitch.tv UNSUPPORTED #pajlada :payload text`),
			check: func(t *testing.T, event twitch.Event) {
				t.Helper()
				if event.Kind != twitch.EventRaw || !strings.Contains(event.Raw.TODO, "TODO") || event.Raw.RawType != "UNSUPPORTED" {
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

func normalizeParsedFixture(t *testing.T, raw string) twitch.Event {
	t.Helper()
	return NormalizeMessage(gempir.ParseMessage(raw))
}

func TestParseFirstMessageTag(t *testing.T) {
	for _, tt := range []struct {
		name string
		tags map[string]string
		want bool
	}{
		{"present", map[string]string{"first-msg": "1"}, true},
		{"explicitly zero", map[string]string{"first-msg": "0"}, false},
		{"absent", map[string]string{}, false},
		{"nil tags", nil, false},
		// Anything twi does not recognize means "not first": over-marking
		// regulars is worse than missing the occasional newcomer.
		{"unexpected value", map[string]string{"first-msg": "true"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseFirstMessageTag(tt.tags); got != tt.want {
				t.Fatalf("parseFirstMessageTag(%v) = %v, want %v", tt.tags, got, tt.want)
			}
		})
	}
}

// TestNormalizePrivateMessageCarriesFirstMessage pins the tag through
// normalization, which is where a field silently stops being populated.
func TestNormalizePrivateMessageCarriesFirstMessage(t *testing.T) {
	event := NormalizePrivateMessage(gempir.PrivateMessage{
		ID:      "m1",
		Channel: "example",
		Message: "hi",
		User:    gempir.User{Name: "newcomer", DisplayName: "Newcomer"},
		Tags:    map[string]string{"first-msg": "1"},
	})
	if !event.Message.FirstMessage {
		t.Fatal("first-msg tag did not reach ChatMessage.FirstMessage")
	}
}
