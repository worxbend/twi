package eventsub

import (
	"encoding/json"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
	"testing"
	"time"
)

func controlFrame(kind, payload string) envelope {
	var frame envelope
	frame.Metadata.Subscription = kind
	frame.Metadata.ID = "delivery"
	frame.Metadata.At = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	frame.Payload.Event = json.RawMessage(payload)
	return frame
}
func TestNotificationPreservesSharedIdentityAndSystemText(t *testing.T) {
	event, err := normalize(controlFrame("channel.chat.notification", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","chatter_user_id":"2","chatter_user_login":"viewer","notice_type":"sub_gift","system_message":"Viewer gifted a subscription","message_id":"notice","source_broadcaster_user_id":"3","source_broadcaster_user_login":"beta","source_message_id":"source","sub_gift":{"recipient_user_id":"4","recipient_user_login":"recipient","sub_tier":"1000"}}`))
	if err != nil || event.Kind != twitch.EventUserNotice || event.UserNotice.DeliveryID != "delivery" || event.UserNotice.SourceChannelID != "3" || event.UserNotice.Params["recipient_user_id"] != "4" || event.UserNotice.SystemText == "" {
		t.Fatal(event, err)
	}
}
func TestHeldMessageBodyNeverBecomesObservedChat(t *testing.T) {
	event, err := normalize(controlFrame("channel.chat.user_message_hold", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","user_id":"2","message_id":"held","message":{"text":"private rejected body"}}`))
	if err != nil || event.Kind != twitch.EventModeration || event.Moderation.Type != twitch.ModerationAction || event.Moderation.Text != "" || event.Moderation.Action != "message_held" {
		t.Fatal(event, err)
	}
	event, err = normalize(controlFrame("channel.chat.user_message_update", `{"broadcaster_user_login":"alpha","message_id":"held","status":"denied"}`))
	if err != nil || event.Moderation.Type != twitch.ModerationMessageDeleted || event.Moderation.TargetMessageID != "held" {
		t.Fatal(event, err)
	}
}
func TestModerationV2ScopesConditionsAndPrivacy(t *testing.T) {
	scopes := auth.ModerationScopes()
	client, err := NewClient(Config{AccountLogin: "viewer", ClientID: "client", UserID: "1", Token: func() string { return "token" }, Channels: []Channel{{Login: "alpha", ID: "1"}, {Login: "beta", ID: "2"}}, Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.subscriptions(client.config.Channels[0])) != 8 || len(client.subscriptions(client.config.Channels[1])) != 7 {
		t.Fatal("scope grants incorrectly treated as roles")
	}
	if CanReadModeration(scopes[:len(scopes)-1]) {
		t.Fatal("missing scope accepted")
	}
	scopes[4] = "moderator:manage:chat_messages"
	if !CanReadModeration(scopes) {
		t.Fatal("documented manage alternative rejected")
	}
	event, err := normalize(controlFrame("channel.moderate", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","moderator_user_id":"2","moderator_user_login":"mod","action":"delete","delete":{"user_id":"3","user_login":"viewer","message_id":"deleted","message_body":"removed body"}}`))
	if err != nil || event.Moderation.Type != twitch.ModerationMessageDeleted || event.Moderation.Text != "" || event.Moderation.ModeratorID != "2" {
		t.Fatal(event, err)
	}
	event, err = normalize(controlFrame("channel.moderate", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","source_broadcaster_user_id":"9","action":"clear"}`))
	if err != nil || event.Moderation.Type != twitch.ModerationAction {
		t.Fatal("shared source incorrectly cleared observed room", event, err)
	}
}
func TestNotificationMatchesDomainIdentityConvention(t *testing.T) {
	event, err := normalize(controlFrame("channel.chat.notification", `{"broadcaster_user_login":"alpha","notice_type":"sub","message_id":"unique-notice","system_message":"subscribed"}`))
	if err != nil || event.UserNotice.ID != "unique-notice" || event.UserNotice.MessageID != "sub" {
		t.Fatal(event, err)
	}
}

func TestNotificationFragmentsBadgesAndSharedSourceDecoration(t *testing.T) {
	event, err := normalize(controlFrame("channel.chat.notification", `{"broadcaster_user_login":"alpha","notice_type":"resub","message_id":"notice-unique","color":"#123456","badges":[{"set_id":"subscriber","id":"12","info":"13"}],"source_badges":[{"set_id":"moderator","id":"1"}],"message":{"text":"hello Kappa","fragments":[{"type":"text","text":"hello "},{"type":"emote","text":"Kappa","emote":{"id":"25"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	notice := event.UserNotice
	if notice.ID != "notice-unique" || notice.MessageID != "resub" || len(notice.Badges) != 1 || notice.Badges[0].Info != "13" || len(notice.SourceBadges) != 1 || len(notice.Fragments) != 2 || notice.Fragments[1].Ref.ID != "25" || notice.AuthorColor != "#123456" {
		t.Fatalf("notification metadata=%+v", notice)
	}
}
func TestChatMessageBadgeCategoryBitsAndSplitAction(t *testing.T) {
	event, err := normalize(controlFrame("channel.chat.message", `{"broadcaster_user_login":"alpha","message_id":"chat","message_type":"user_intro","badges":[{"set_id":"vip","id":"1"}],"source_badges":[{"set_id":"subscriber","id":"2"}],"cheer":{"bits":100},"message":{"text":"Cheer100","fragments":[{"type":"cheermote","text":"Cheer100","cheermote":{"prefix":"cheer","bits":100,"tier":100}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	message := event.Message
	if !message.FirstMessage || message.PlatformMessageType != "user_intro" || message.Bits != 100 || len(message.Badges) != 1 || len(message.SourceBadges) != 1 || message.Fragments[0].Bits != 100 || message.Fragments[0].Tier != 100 {
		t.Fatalf("chat metadata=%+v", message)
	}
	event, err = normalize(controlFrame("channel.chat.message", `{"broadcaster_user_login":"alpha","message_id":"action","message":{"text":"\u0001ACTION waves Kappa\u0001","fragments":[{"type":"text","text":"\u0001ACTION waves "},{"type":"emote","text":"Kappa","emote":{"id":"25"}},{"type":"text","text":"\u0001"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Message.Type != twitch.MessageTypeAction || event.Message.Text != "waves Kappa" || event.Message.Fragments[0].Text != "waves " || event.Message.Fragments[2].Text != "" {
		t.Fatalf("split action=%+v", event.Message)
	}
}
func TestModerationActionTargetSettingsAndSharedPrivacyScope(t *testing.T) {
	cases := []struct{ payload, action, target, key, value string }{{`"action":"vip","vip":{"user_id":"target","user_login":"viewer","user_name":"Viewer"}`, "vip", "target", "", ""}, {`"action":"followers","followers":{"follow_duration_minutes":0}`, "followers", "", "follow_duration_minutes", "0"}, {`"action":"slow","slow":{"wait_time_seconds":30}`, "slow", "", "wait_time_seconds", "30"}, {`"action":"warn","warn":{"user_id":"target","reason":"rule","chat_rules_cited":["be kind"]}`, "warn", "target", "cited_rule_0", "be kind"}, {`"action":"add_blocked_term","automod_terms":{"action":"add","list":"blocked","terms":["term"],"from_automod":false}`, "add_blocked_term", "", "term_0", "term"}, {`"action":"approve_unban_request","unban_request":{"user_id":"target","is_approved":true,"moderator_message":"approved"}`, "approve_unban_request", "target", "is_approved", "true"}}
	for _, test := range cases {
		t.Run(test.action, func(t *testing.T) {
			event, err := normalize(controlFrame("channel.moderate", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","moderator_user_id":"mod","moderator_user_name":"Moderator",`+test.payload+`}`))
			if err != nil {
				t.Fatal(err)
			}
			m := event.Moderation
			if m.Type != twitch.ModerationAction || m.Action != test.action || m.TargetUserID != test.target || m.ModeratorName != "Moderator" || test.key != "" && m.Parameters[test.key] != test.value {
				t.Fatalf("action details=%+v", m)
			}
		})
	}
	for _, action := range []string{"timeout", "shared_chat_timeout"} {
		event, err := normalize(controlFrame("channel.moderate", `{"broadcaster_user_id":"1","broadcaster_user_login":"alpha","source_broadcaster_user_id":"other","action":"`+action+`","`+action+`":{"user_id":"target","user_login":"viewer","reason":"reason","expires_at":"2026-10-12T12:00:30Z"}}`))
		if err != nil {
			t.Fatal(err)
		}
		m := event.Moderation
		if m.Type != twitch.ModerationAction || m.TargetUserID != "target" || m.Reason != "reason" || m.BanDuration != 30*time.Second {
			t.Fatalf("shared target scope=%+v", m)
		}
	}
	event, err := normalize(controlFrame("channel.moderate", `{"broadcaster_user_login":"alpha","source_broadcaster_user_id":"other","action":"shared_chat_delete","shared_chat_delete":{"user_id":"target","message_id":"removed","message_body":"private deleted body"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Moderation.Type != twitch.ModerationAction || event.Moderation.TargetMessageID != "removed" || event.Moderation.Text != "" {
		t.Fatalf("shared delete metadata=%+v", event)
	}
}

func TestBasicModerationControlRequiresScopedIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct{ kind, payload string }{{"channel.chat.message_delete", `{"broadcaster_user_login":"alpha"}`}, {"channel.chat.clear_user_messages", `{"broadcaster_user_login":"alpha"}`}, {"channel.chat.message_delete", `{"message_id":"target"}`}, {"channel.chat.clear_user_messages", `{"target_user_id":"target"}`}, {"channel.chat.clear", `{}`}}
	for _, test := range cases {
		if _, err := normalize(controlFrame(test.kind, test.payload)); err == nil {
			t.Fatalf("unscoped control accepted: %s %s", test.kind, test.payload)
		}
	}
	event, err := normalize(controlFrame("channel.chat.clear_user_messages", `{"broadcaster_user_login":"alpha","target_user_id":"target"}`))
	if err != nil || event.Moderation.Type != twitch.ModerationUserMessagesCleared || event.Moderation.TargetUserID != "target" {
		t.Fatalf("valid user-scoped control lost=%+v err=%v", event, err)
	}
}
