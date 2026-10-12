// Package automation defines versioned public records shared by CLI adapters.
package automation

import "time"

type Channel struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}
type Author struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
}
type Reply struct {
	ParentID    string `json:"parent_id"`
	AuthorLogin string `json:"author_login,omitempty"`
	Text        string `json:"text,omitempty"`
}
type Fragment struct {
	Bits int    `json:"bits,omitempty"`
	Tier int    `json:"tier,omitempty"`
	Type string `json:"type"`
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
}
type Badge struct {
	SetID string `json:"set_id"`
	ID    string `json:"id"`
	Info  string `json:"info,omitempty"`
}
type Record struct {
	AuthorColor          string            `json:"author_color,omitempty"`
	Badges               []Badge           `json:"badges,omitempty"`
	SourceBadges         []Badge           `json:"source_badges,omitempty"`
	Bits                 int               `json:"bits,omitempty"`
	FirstMessage         bool              `json:"first_message,omitempty"`
	PlatformMessageType  string            `json:"platform_message_type,omitempty"`
	ModerationParameters map[string]string `json:"moderation_parameters,omitempty"`
	MessageKind          string            `json:"message_kind,omitempty"`
	BanDurationMS        int64             `json:"ban_duration_ms,omitempty"`
	TimeoutExpiresAt     *time.Time        `json:"timeout_expires_at,omitempty"`
	Action               string            `json:"action,omitempty"`
	Moderator            *Author           `json:"moderator,omitempty"`
	SystemText           string            `json:"system_text,omitempty"`
	NoticeParameters     map[string]string `json:"notice_parameters,omitempty"`
	SchemaVersion        int               `json:"schema_version"`
	Type                 string            `json:"type"`
	ID                   string            `json:"id"`
	Channel              Channel           `json:"channel"`
	Author               Author            `json:"author"`
	Text                 string            `json:"text"`
	EventAt              time.Time         `json:"event_at"`
	ReceivedAt           time.Time         `json:"received_at"`
	TimeSource           string            `json:"time_source"`
	Transport            string            `json:"transport"`
	Source               string            `json:"source"`
	Cursor               *string           `json:"cursor"`
	Reply                *Reply            `json:"reply"`
	Deleted              bool              `json:"deleted"`
	DeliveryID           string            `json:"delivery_id,omitempty"`
	SourceChannel        *Channel          `json:"source_channel,omitempty"`
	SourceMessageID      string            `json:"source_message_id,omitempty"`
	Fragments            []Fragment        `json:"fragments,omitempty"`
	TargetMessageID      string            `json:"target_message_id,omitempty"`
	TargetUserID         string            `json:"target_user_id,omitempty"`
	Reason               string            `json:"reason,omitempty"`
	Sequence             int64             `json:"-"`
}
