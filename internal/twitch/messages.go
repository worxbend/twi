package twitch

import "time"

type ChatMessage struct {
	ID          string
	Channel     string
	Timestamp   time.Time
	AuthorLogin string
	AuthorID    string
	DisplayName string
	AuthorColor string
	AvatarURL   string
	Badges      []Badge
	Text        string
	Fragments   []MessageFragment
	Emotes      []Emote
	Reply       *Reply
	Type        MessageType
	Deleted     bool
	// RawTags is retained only for diagnostics/debug views. UI and renderer
	// behavior should use normalized fields above instead of transport tags.
	RawTags map[string]string
}

type MessageType string

const (
	MessageTypeChat   MessageType = "chat"
	MessageTypeAction MessageType = "action"
	MessageTypeNotice MessageType = "notice"
	MessageTypeSystem MessageType = "system"
)

type MessageFragment struct {
	Type FragmentType
	Text string
	Ref  AssetRef
}

type FragmentType string

const (
	FragmentText    FragmentType = "text"
	FragmentMention FragmentType = "mention"
	FragmentEmote   FragmentType = "emote"
	FragmentEmoji   FragmentType = "emoji"
	FragmentBits    FragmentType = "bits"
)

type Badge struct {
	SetID string
	ID    string
	Info  string
}

type Emote struct {
	ID    string
	Name  string
	Start int
	End   int
}

type Reply struct {
	ParentMessageID string
	ParentAuthorID  string
	ParentLogin     string
	ParentAuthor    string
	ParentText      string
}

type AssetRef struct {
	Kind string
	ID   string
	URL  string
}
