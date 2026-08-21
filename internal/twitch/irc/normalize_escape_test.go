package irc

import (
	"reflect"
	"strings"
	"testing"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
)

// escapePayload is text carrying control sequences a terminal would act on:
// clear the screen, move the cursor, set a scroll region, ring the bell.
const escapePayload = "hi\x1b[2J\x1b[10;10H\x1b[1;5r\x07there"

// rawFieldPaths are the field paths deliberately exempt from sanitization.
//
// RawTags and Params are the protocol's own key/value maps, kept verbatim so
// the inspect panel can show a message exactly as Twitch delivered it. They
// are sanitized where they are rendered, not here -- see redactDiagnosticText
// in internal/app.
var rawFieldPaths = map[string]bool{
	"Message.RawTags":    true,
	"Notice.RawTags":     true,
	"UserNotice.RawTags": true,
	"UserNotice.Params":  true,
	"RoomState.RawTags":  true,
	"Moderation.RawTags": true,
	"UserState.RawTags":  true,
	"Raw.RawTags":        true,
}

// TestNormalizersStripEscapesFromEveryDisplayedField enforces the invariant
// internal/textsafe states: every string that reaches the screen from outside
// is passed through Display first.
//
// It is implemented as a reflective walk rather than a list of assertions
// because the invariant is about *every* field, and a per-field test only
// covers the fields somebody remembered. Deleting the textsafe.Display call on
// SystemText, ParentText, a badge's Info, an emote's Name or a moderation
// target's login left the entire suite green before this existed.
func TestNormalizersStripEscapesFromEveryDisplayedField(t *testing.T) {
	user := gempir.User{
		Name:        "chatter" + escapePayload,
		DisplayName: "Chatter" + escapePayload,
		Color:       "#fff",
		Badges:      map[string]int{"moderator": 1},
	}
	tags := map[string]string{"first-msg": "1", "badge-info": "subscriber/" + escapePayload}

	events := map[string]func() any{
		"PrivateMessage": func() any {
			return NormalizePrivateMessage(gempir.PrivateMessage{
				ID: "id", Channel: "example", RoomID: "1", Time: time.Now(),
				User: user, Message: escapePayload, Tags: tags,
				Reply: &gempir.Reply{ParentMsgID: "p", ParentUserLogin: "parent" + escapePayload, ParentMsgBody: escapePayload},
			})
		},
		"NoticeMessage": func() any {
			return NormalizeNoticeMessage(gempir.NoticeMessage{Channel: "example", MsgID: "id", Message: escapePayload})
		},
		"UserNoticeMessage": func() any {
			return NormalizeUserNoticeMessage(gempir.UserNoticeMessage{
				ID: "id", Channel: "example", RoomID: "1", User: user,
				Message: escapePayload, SystemMsg: escapePayload, MsgID: "raid", Tags: tags,
			})
		},
		"ClearChatMessage": func() any {
			return NormalizeClearChatMessage(gempir.ClearChatMessage{
				Channel: "example", TargetUsername: "target" + escapePayload, TargetUserID: "2",
			})
		},
		"ClearMessage": func() any {
			return NormalizeClearMessage(gempir.ClearMessage{
				Channel: "example", Login: "login" + escapePayload, TargetMsgID: "m", Message: escapePayload,
			})
		},
		"UserStateMessage": func() any {
			return NormalizeUserStateMessage(gempir.UserStateMessage{Channel: "example", User: user})
		},
		"RoomStateMessage": func() any {
			return NormalizeRoomStateMessage(gempir.RoomStateMessage{Channel: "example", RoomID: "1"})
		},
		"UserJoinMessage": func() any {
			return NormalizeUserJoinMessage(gempir.UserJoinMessage{Channel: "example", User: "user" + escapePayload}, time.Now())
		},
		"UserPartMessage": func() any {
			return NormalizeUserPartMessage(gempir.UserPartMessage{Channel: "example", User: "user" + escapePayload}, time.Now())
		},
	}

	for name, build := range events {
		t.Run(name, func(t *testing.T) {
			walkStringFields(t, reflect.ValueOf(build()), "")
		})
	}
}

// walkStringFields visits every string reachable from v and fails on any that
// still contains a control character, unless its path is exempt.
func walkStringFields(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		if rawFieldPaths[path] {
			return
		}
		if containsControl(v.String()) {
			t.Errorf("%s = %q still contains a control character; it needs textsafe.Display", path, v.String())
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkStringFields(t, v.Elem(), path)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			name := v.Type().Field(i).Name
			child := name
			if path != "" {
				child = path + "." + name
			}
			if rawFieldPaths[child] {
				continue
			}
			walkStringFields(t, v.Field(i), child)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkStringFields(t, v.Index(i), path)
		}
	case reflect.Map:
		if rawFieldPaths[path] {
			return
		}
		for _, key := range v.MapKeys() {
			walkStringFields(t, key, path)
			walkStringFields(t, v.MapIndex(key), path)
		}
	}
}

// containsControl reports whether value holds a C0 or C1 control character,
// excluding the CTCP delimiter, which is meaningful in a chat message and is
// deliberately preserved.
func containsControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool {
		if r == ctcpDelimiter {
			return false
		}
		return r < 0x20 || (r >= 0x7f && r <= 0x9f)
	})
}
