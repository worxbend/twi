package app

import (
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

func TestMessageFilterMatching(t *testing.T) {
	tests := []struct {
		name         string
		message      twitch.ChatMessage
		filter       messageFilter
		mentionLogin string
		want         bool
	}{
		{
			name: "mention matches configured user",
			message: twitch.ChatMessage{
				Text: "hello @twi_bot",
			},
			filter:       messageFilterMentions,
			mentionLogin: "twi_bot",
			want:         true,
		},
		{
			name: "mention rejects other users when configured",
			message: twitch.ChatMessage{
				Fragments: []twitch.MessageFragment{{Type: twitch.FragmentMention, Text: "@someone_else"}},
			},
			filter:       messageFilterMentions,
			mentionLogin: "twi_bot",
			want:         false,
		},
		{
			name: "mention matches any mention without configured user",
			message: twitch.ChatMessage{
				Fragments: []twitch.MessageFragment{{Type: twitch.FragmentMention, Text: "@viewer"}},
			},
			filter: messageFilterMentions,
			want:   true,
		},
		{
			name: "role matches broadcaster badge",
			message: twitch.ChatMessage{
				Badges: []twitch.Badge{{SetID: "broadcaster", ID: "1"}},
			},
			filter: messageFilterRoles,
			want:   true,
		},
		{
			name: "role matches moderator badge",
			message: twitch.ChatMessage{
				Badges: []twitch.Badge{{SetID: "moderator", ID: "1"}},
			},
			filter: messageFilterRoles,
			want:   true,
		},
		{
			name: "role matches vip badge",
			message: twitch.ChatMessage{
				Badges: []twitch.Badge{{SetID: "vip", ID: "1"}},
			},
			filter: messageFilterRoles,
			want:   true,
		},
		{
			name: "role rejects ordinary subscriber badge",
			message: twitch.ChatMessage{
				Badges: []twitch.Badge{{SetID: "subscriber", ID: "12"}},
			},
			filter: messageFilterRoles,
			want:   false,
		},
		{
			name: "notice matches notice rows",
			message: twitch.ChatMessage{
				Type: twitch.MessageTypeNotice,
				Text: "slow mode enabled",
			},
			filter: messageFilterNotices,
			want:   true,
		},
		{
			name: "notice matches system rows",
			message: twitch.ChatMessage{
				Type: twitch.MessageTypeSystem,
				Text: "system event",
			},
			filter: messageFilterNotices,
			want:   true,
		},
		{
			name: "error matches error-tagged system row",
			message: twitch.ChatMessage{
				Type:    twitch.MessageTypeSystem,
				Text:    "transport event",
				RawTags: map[string]string{"level": "error"},
			},
			filter: messageFilterErrors,
			want:   true,
		},
		{
			name: "error matches notice failure text",
			message: twitch.ChatMessage{
				Type: twitch.MessageTypeNotice,
				Text: "Twitch IRC authentication failed",
			},
			filter: messageFilterErrors,
			want:   true,
		},
		{
			name: "error matches normalized notice id without raw tags",
			message: twitch.ChatMessage{
				ID:   "login_failed",
				Type: twitch.MessageTypeNotice,
				Text: "Twitch IRC notice",
			},
			filter: messageFilterErrors,
			want:   true,
		},
		{
			name: "error rejects routine notice",
			message: twitch.ChatMessage{
				Type: twitch.MessageTypeNotice,
				Text: "slow mode enabled",
			},
			filter: messageFilterErrors,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := messageMatchesFilter(tt.message, tt.filter, tt.mentionLogin); got != tt.want {
				t.Fatalf("messageMatchesFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessageFilterSetMatchesAnyEnabledFilter(t *testing.T) {
	filters := messageFilterSet(messageFilterMentions | messageFilterRoles)
	message := twitch.ChatMessage{
		ID:     "role-1",
		Badges: []twitch.Badge{{SetID: "vip", ID: "1"}},
		Text:   "plain role message",
	}

	if !filters.matches(message, "twi_bot") {
		t.Fatal("combined filter rejected role message, want OR matching")
	}

	filters = messageFilterSet(messageFilterMentions)
	if filters.matches(message, "twi_bot") {
		t.Fatal("mention-only filter matched role-only message")
	}
}
