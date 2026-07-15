package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func (m mockShellModel) inspectView(layout mockShellLayout) string {
	contentWidth := layout.width
	if layout.inspectFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.inspectLines(contentWidth, layout.inspectContentHeight)
	for len(lines) < layout.inspectContentHeight {
		lines = append(lines, fitLine("", contentWidth))
	}
	content := strings.Join(lines, "\n")
	if !layout.inspectFramed {
		return fitBlock(content, layout.width, layout.inspectHeight)
	}

	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.inspectContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(m.theme.Accent)).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) inspectLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, 0, height)
	message, ok := m.selectedInspectMessage()
	if !ok {
		lines = append(lines, " Inspect: no selected message")
		lines = append(lines, " Select with up/down or click a message, then press i.")
		return fitInspectLines(lines, width, height)
	}

	lines = append(lines,
		" Inspect: selected message",
		inspectMessageLine(message),
		inspectAuthorLine(message),
		inspectBadgesLine(message.Badges),
		inspectRawTagsLine(message.RawTags),
	)
	if message.Reply != nil {
		lines = append(lines, inspectReplyLine(message.Reply))
	}
	if len(message.Fragments) > 0 {
		lines = append(lines, inspectFragmentsLine(message.Fragments))
	}
	if len(message.Emotes) > 0 {
		lines = append(lines, inspectEmotesLine(message.Emotes))
	}
	if strings.TrimSpace(message.Text) != "" {
		lines = append(lines, "text: "+redactDiagnosticText(compactDiagnosticText(message.Text)))
	}
	return fitInspectLines(lines, width, height)
}

func fitInspectLines(lines []string, width, height int) []string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = fitLine(redactDiagnosticText(lines[i]), width)
	}
	return lines
}

func (m mockShellModel) selectedInspectMessage() (twitch.ChatMessage, bool) {
	selectedID := replyMessageID(m.activeChannelState().replyTo)
	if strings.TrimSpace(selectedID) == "" {
		return twitch.ChatMessage{}, false
	}
	return m.messageByID(selectedID)
}

func (m mockShellModel) messageByID(id string) (twitch.ChatMessage, bool) {
	active := m.activeChannelState()
	for _, message := range active.messages {
		if message.ID == id {
			return message, true
		}
	}
	for _, message := range active.activeMessages {
		if message.ID == id {
			return message, true
		}
	}
	return twitch.ChatMessage{}, false
}

func inspectMessageLine(message twitch.ChatMessage) string {
	parts := []string{
		"id=" + safeDiagnosticValue(message.ID),
		"channel=#" + safeDiagnosticValue(message.Channel),
		"type=" + safeDiagnosticValue(string(message.Type)),
	}
	if !message.Timestamp.IsZero() {
		parts = append(parts, "time="+message.Timestamp.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if message.Deleted {
		parts = append(parts, "deleted=true")
	}
	return "message: " + strings.Join(parts, " ")
}

func inspectAuthorLine(message twitch.ChatMessage) string {
	parts := []string{
		"display=" + safeDiagnosticValue(displayReplyAuthor(message)),
	}
	if message.AuthorLogin != "" {
		parts = append(parts, "login="+safeDiagnosticValue(message.AuthorLogin))
	}
	if message.AuthorID != "" {
		parts = append(parts, "id="+safeDiagnosticValue(message.AuthorID))
	}
	if message.AuthorColor != "" {
		parts = append(parts, "color="+safeDiagnosticValue(message.AuthorColor))
	}
	if message.AvatarURL != "" {
		parts = append(parts, "avatar=resolved")
	}
	return "author: " + strings.Join(parts, " ")
}

func inspectBadgesLine(badges []twitch.Badge) string {
	if len(badges) == 0 {
		return "badges: none"
	}
	parts := make([]string, 0, len(badges))
	for _, badge := range badges {
		label := emptyFallback(badge.SetID, "unknown") + "/" + emptyFallback(badge.ID, "unknown")
		if badge.Info != "" {
			label += "(" + safeDiagnosticValue(badge.Info) + ")"
		}
		if badge.Ref.Kind != "" || badge.Ref.ID != "" {
			label += " ref=" + inspectAssetRef(badge.Ref)
		}
		parts = append(parts, label)
	}
	return "badges: " + strings.Join(parts, ", ")
}

func inspectRawTagsLine(tags map[string]string) string {
	if len(tags) == 0 {
		return "raw tags: none"
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, safeDiagnosticKey(key)+"="+redactDiagnosticValue(key, tags[key]))
	}
	return "raw tags: " + strings.Join(parts, "; ")
}

func inspectReplyLine(reply *twitch.Reply) string {
	if reply == nil {
		return "reply: none"
	}
	parts := []string{
		"id=" + safeDiagnosticValue(reply.ParentMessageID),
		"author=" + safeDiagnosticValue(emptyFallback(reply.ParentAuthor, reply.ParentLogin)),
	}
	if reply.ParentAuthorID != "" {
		parts = append(parts, "author_id="+safeDiagnosticValue(reply.ParentAuthorID))
	}
	if reply.ParentText != "" {
		parts = append(parts, "text="+safeDiagnosticValue(compactDiagnosticText(reply.ParentText)))
	}
	return "reply: " + strings.Join(parts, " ")
}

func inspectFragmentsLine(fragments []twitch.MessageFragment) string {
	parts := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		label := string(fragment.Type)
		if fragment.Text != "" {
			label += "=" + safeDiagnosticValue(compactDiagnosticText(fragment.Text))
		}
		if fragment.Ref.Kind != "" || fragment.Ref.ID != "" {
			label += " ref=" + inspectAssetRef(fragment.Ref)
		}
		parts = append(parts, label)
	}
	return "fragments: " + strings.Join(parts, ", ")
}

func inspectEmotesLine(emotes []twitch.Emote) string {
	parts := make([]string, 0, len(emotes))
	for _, emote := range emotes {
		label := fmt.Sprintf("%s/%s[%d:%d]", safeDiagnosticValue(emote.Name), safeDiagnosticValue(emote.ID), emote.Start, emote.End)
		if emote.Ref.Kind != "" || emote.Ref.ID != "" {
			label += " ref=" + inspectAssetRef(emote.Ref)
		}
		parts = append(parts, label)
	}
	return "emotes: " + strings.Join(parts, ", ")
}

func inspectAssetRef(ref twitch.AssetRef) string {
	kind := emptyFallback(ref.Kind, "unknown")
	id := emptyFallback(ref.ID, "unknown")
	return safeDiagnosticValue(kind + ":" + id)
}

func redactDiagnosticValue(key, value string) string {
	if sensitiveDiagnosticKey(key) {
		return "[redacted]"
	}
	return redactDiagnosticText(value)
}

func safeDiagnosticKey(key string) string {
	return redactDiagnosticText(key)
}

func safeDiagnosticValue(value string) string {
	return redactDiagnosticText(emptyFallback(value, "unknown"))
}

func redactDiagnosticText(value string) string {
	return redactSensitive(value, config.Config{})
}

func sensitiveDiagnosticKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("-", "", "_", "", ".", "").Replace(normalized)
	return strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token") ||
		normalized == "code" ||
		strings.Contains(normalized, "authorizationcode")
}

func compactDiagnosticText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
