package app

import (
	"strings"
	"unicode"

	"github.com/worxbend/twi/internal/twitch"
)

type messageFilter uint8

const (
	messageFilterMentions messageFilter = 1 << iota
	messageFilterRoles
	messageFilterNotices
	messageFilterErrors
)

type messageFilterSet uint8

type messageFilterDefinition struct {
	filter   messageFilter
	label    string
	shortcut string
	keywords []string
}

var messageFilterDefinitions = []messageFilterDefinition{
	{
		filter:   messageFilterMentions,
		label:    "mentions",
		shortcut: "1",
		keywords: []string{"mention", "mentions", "at"},
	},
	{
		filter:   messageFilterRoles,
		label:    "roles",
		shortcut: "2",
		keywords: []string{"broadcaster", "moderator", "mod", "vip", "role", "roles"},
	},
	{
		filter:   messageFilterNotices,
		label:    "notices",
		shortcut: "3",
		keywords: []string{"notice", "notices", "system", "moderation"},
	},
	{
		filter:   messageFilterErrors,
		label:    "errors",
		shortcut: "4",
		keywords: []string{"error", "errors", "failed", "failure", "disconnect"},
	},
}

func (s messageFilterSet) active() bool {
	return s != 0
}

func (s messageFilterSet) enabled(filter messageFilter) bool {
	return s&messageFilterSet(filter) != 0
}

func (s *messageFilterSet) toggle(filter messageFilter) {
	if s == nil {
		return
	}
	if s.enabled(filter) {
		*s &^= messageFilterSet(filter)
		return
	}
	*s |= messageFilterSet(filter)
}

func (s *messageFilterSet) reset() {
	if s != nil {
		*s = 0
	}
}

func (s messageFilterSet) summary() string {
	if !s.active() {
		return ""
	}
	labels := make([]string, 0, len(messageFilterDefinitions))
	for _, def := range messageFilterDefinitions {
		if s.enabled(def.filter) {
			labels = append(labels, def.label)
		}
	}
	return strings.Join(labels, ",")
}

func (s messageFilterSet) matches(message twitch.ChatMessage, mentionLogin string) bool {
	if !s.active() {
		return true
	}
	for _, def := range messageFilterDefinitions {
		if s.enabled(def.filter) && messageMatchesFilter(message, def.filter, mentionLogin) {
			return true
		}
	}
	return false
}

func messageMatchesFilter(message twitch.ChatMessage, filter messageFilter, mentionLogin string) bool {
	switch filter {
	case messageFilterMentions:
		return messageMentionsLogin(message, mentionLogin)
	case messageFilterRoles:
		return messageHasRoleBadge(message)
	case messageFilterNotices:
		return messageIsSystemNotice(message)
	case messageFilterErrors:
		return messageIsError(message)
	default:
		return false
	}
}

func messageMentionsLogin(message twitch.ChatMessage, mentionLogin string) bool {
	target := normalizeMentionLogin(mentionLogin)
	for _, fragment := range message.Fragments {
		if fragment.Type == twitch.FragmentMention && mentionTextMatches(fragment.Text, target) {
			return true
		}
		if target != "" && textHasMention(fragment.Text, target) {
			return true
		}
	}
	return textHasMention(message.Text, target)
}

func mentionTextMatches(text, target string) bool {
	mention := normalizeMentionLogin(text)
	if mention == "" {
		return false
	}
	return target == "" || mention == target
}

func textHasMention(text, target string) bool {
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' || i+1 >= len(runes) || !isMessageFilterMentionRune(runes[i+1]) {
			continue
		}
		start := i + 1
		end := start + 1
		for end < len(runes) && isMessageFilterMentionRune(runes[end]) {
			end++
		}
		mention := strings.ToLower(string(runes[start:end]))
		if target == "" || mention == target {
			return true
		}
		i = end
	}
	return false
}

func normalizeMentionLogin(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimPrefix(value, "#")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	end := 0
	for end < len(runes) && isMessageFilterMentionRune(runes[end]) {
		end++
	}
	if end == 0 {
		return ""
	}
	return strings.ToLower(string(runes[:end]))
}

func isMessageFilterMentionRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func messageHasRoleBadge(message twitch.ChatMessage) bool {
	for _, badge := range message.Badges {
		switch strings.ToLower(strings.TrimSpace(badge.SetID)) {
		case "broadcaster", "moderator", "vip":
			return true
		}
	}
	return false
}

func messageIsSystemNotice(message twitch.ChatMessage) bool {
	return message.Type == twitch.MessageTypeNotice || message.Type == twitch.MessageTypeSystem
}

func messageIsError(message twitch.ChatMessage) bool {
	for key, value := range message.RawTags {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		if (key == "level" || key == "severity" || key == "twi.kind" || key == "twi_type") && value == "error" {
			return true
		}
		if (key == "msg-id" || key == "notice-id" || key == "system-msg") && hasErrorMarker(value) {
			return true
		}
	}
	if message.Type != twitch.MessageTypeNotice && message.Type != twitch.MessageTypeSystem {
		return false
	}
	return hasErrorMarker(strings.ToLower(strings.TrimSpace(message.ID + " " + message.Text)))
}

func hasErrorMarker(value string) bool {
	markers := []string{
		"error",
		"failed",
		"failure",
		"invalid",
		"denied",
		"timeout",
		"timed out",
		"disconnect",
		"connection closed",
	}
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
