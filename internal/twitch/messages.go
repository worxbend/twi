package twitch

import (
	"net/url"
	"strings"
	"time"
	"unicode"
)

// NormalizeIRCOAuthToken puts a token into the form Twitch IRC requires.
//
// Twitch's IRC login expects the access token prefixed with "oauth:", while
// every other place a token appears -- Helix headers, the OAuth endpoints,
// what a user pastes from the Twitch developer console -- uses it bare. The
// rule lives here because three packages need it and had a copy each: the
// config loader, the CLI, and the IRC transport writing the token to the wire
// after a refresh. Three copies of a rule about credentials is three chances
// for them to disagree about what gets sent.
//
// A blank token is returned unchanged: "oauth:" on its own is not a credential
// and would only turn a missing-token error into a rejected-login one.
func NormalizeIRCOAuthToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(strings.ToLower(value), "oauth:") {
		return value
	}
	return "oauth:" + value
}

// IsLoginRune reports whether r can appear in a Twitch login name.
//
// Twitch logins are letters, digits and underscore. Four packages needed to
// know this -- the IRC normalizer splitting mentions out of message text, the
// renderer doing the same for display, the message filter, and mention
// autocomplete -- and each had written the predicate out itself. What a login
// is made of is a fact about Twitch, so it belongs with the rest of the model.
//
// Only the predicate is shared. The loops that use it genuinely differ: one
// stops at the first non-login rune, another strips a leading "@", another
// truncates. Sharing those would merge behaviors that are not the same.
func IsLoginRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// MaxChatMessageRunes is Twitch's per-message limit. Longer messages are
// rejected by the server, so twi truncates rather than sending something it
// knows will be dropped, and the composer counts down to it as you type.
const MaxChatMessageRunes = 500

type ChatMessage struct {
	ID          string
	Channel     string
	ChannelID   string
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
	// Bits is the cheer amount from a Twitch PRIVMSG's "bits" tag, or 0 for a
	// message with no cheer. Cheers arrive as ordinary chat messages (not a
	// USERNOTICE), so this is the only signal that a message is a cheer.
	Bits int
	// FirstMessage marks a chatter's very first message in this channel,
	// from Twitch's "first-msg" tag. Twitch computes it server-side, so it
	// is accurate across devices and sessions in a way twi's own roster
	// cannot be.
	FirstMessage bool
	// SystemEventID identifies a normalized non-chat event associated with a
	// notice/system row, such as "raid" from a Twitch USERNOTICE msg-id.
	SystemEventID string
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
	Ref   AssetRef
}

type Emote struct {
	ID    string
	Name  string
	Start int
	End   int
	Ref   AssetRef
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

func StaticEmoteCDNURL(raw string) (cleanURL, id string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return "", "", false
	}
	if strings.ToLower(parsed.Scheme) != "https" || strings.ToLower(parsed.Hostname()) != "static-cdn.jtvnw.net" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 3 || parts[0] != "emoticons" || parts[1] != "v2" {
		return "", "", false
	}
	unescapedID, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", "", false
	}
	id = strings.TrimSpace(unescapedID)
	if id == "" || strings.ContainsAny(id, `/\`) {
		return "", "", false
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), id, true
}
