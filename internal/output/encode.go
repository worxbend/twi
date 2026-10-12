// Package output encodes stable automation records without lifecycle chatter.
package output

import (
	"encoding/json"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
	"io"
	"strings"
	"time"
)

type checkedWriter struct{ io.Writer }

func (w checkedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func Write(w io.Writer, r automation.Record, format string, textOnly bool) (err error) {
	original := w
	defer func() {
		if err == nil {
			if f, ok := original.(interface{ Flush() error }); ok {
				err = f.Flush()
			}
		}
	}()
	w = checkedWriter{w}
	if format == "jsonl" {
		return json.NewEncoder(w).Encode(r)
	}
	text := SafeText(r.Text)
	if textOnly {
		_, err = fmt.Fprintln(w, text)
		return err
	}
	_, err = fmt.Fprintf(w, "%s #%s %s: %s\n", r.EventAt.Format(time.RFC3339Nano), SafeText(r.Channel.Login), SafeText(r.Author.Login), text)
	return err
}
func SafeText(s string) string {
	s = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
	return textsafe.Display(s)
}

// FromEvent omits raw tags and wire frames. received is the local observation time.
func FromEvent(e twitch.Event, received time.Time, transport string) (automation.Record, bool) {
	r := automation.Record{SchemaVersion: 1, ReceivedAt: received, EventAt: received, TimeSource: "received", Transport: transport, Source: "live"}
	switch e.Kind {
	case twitch.EventMessage:
		m := e.Message
		r.Type = "message"
		r.MessageKind = string(m.Type)
		r.AuthorColor = m.AuthorColor
		r.Badges = publicBadges(m.Badges)
		r.SourceBadges = publicBadges(m.SourceBadges)
		r.Bits = m.Bits
		r.FirstMessage = m.FirstMessage
		r.PlatformMessageType = m.PlatformMessageType
		r.ID = m.ID
		r.DeliveryID = m.DeliveryID
		r.SourceMessageID = m.SourceMessageID
		if m.SourceChannelID != "" || m.SourceChannelLogin != "" {
			r.SourceChannel = &automation.Channel{ID: m.SourceChannelID, Login: m.SourceChannelLogin}
		}
		r.Channel = automation.Channel{ID: m.ChannelID, Login: m.Channel}
		r.Author = automation.Author{ID: m.AuthorID, Login: m.AuthorLogin, DisplayName: m.DisplayName}
		r.Text = m.Text
		r.Deleted = m.Deleted
		if !m.Timestamp.IsZero() {
			r.EventAt = m.Timestamp
			r.TimeSource = "irc_server"
			if transport == "eventsub" {
				r.TimeSource = "eventsub_envelope"
			}
		}
		if m.Reply != nil {
			r.Reply = &automation.Reply{ParentID: m.Reply.ParentMessageID, AuthorLogin: m.Reply.ParentLogin, Text: m.Reply.ParentText}
		}
		for _, f := range m.Fragments {
			r.Fragments = append(r.Fragments, automation.Fragment{Type: string(f.Type), Text: f.Text, ID: f.Ref.ID, Bits: f.Bits, Tier: f.Tier})
		}
	case twitch.EventNotice:
		r.Type = "notice"
		r.Channel.Login = e.Notice.Channel
		r.Reason = e.Notice.ID
		r.Text = e.Notice.Text
	case twitch.EventUserNotice:
		n := e.UserNotice
		r.Type = "user_notice"
		r.Channel = automation.Channel{ID: n.RoomID, Login: n.Channel}
		r.ID = n.ID
		r.Reason = n.MessageID
		r.DeliveryID = n.DeliveryID
		r.SystemText = n.SystemText
		r.Text = n.Text
		if r.Text == "" {
			r.Text = n.SystemText
		}
		r.NoticeParameters = cloneParameters(n.Params)
		r.AuthorColor = n.AuthorColor
		r.Badges = publicBadges(n.Badges)
		r.SourceBadges = publicBadges(n.SourceBadges)
		for _, f := range n.Fragments {
			r.Fragments = append(r.Fragments, automation.Fragment{Type: string(f.Type), Text: f.Text, ID: f.Ref.ID, Bits: f.Bits, Tier: f.Tier})
		}
		r.SourceMessageID = n.SourceMessageID
		if n.SourceChannelID != "" || n.SourceChannelLogin != "" {
			r.SourceChannel = &automation.Channel{ID: n.SourceChannelID, Login: n.SourceChannelLogin}
		}
		r.Author = automation.Author{ID: n.AuthorID, Login: n.AuthorLogin, DisplayName: n.DisplayName}
		if !n.Timestamp.IsZero() {
			r.EventAt = n.Timestamp
			r.TimeSource = "irc_server"
			if transport == "eventsub" {
				r.TimeSource = "eventsub_envelope"
			}
		}
	case twitch.EventModeration:
		m := e.Moderation
		r.Type = string(m.Type)
		r.Channel = automation.Channel{ID: m.RoomID, Login: m.Channel}
		r.DeliveryID = m.DeliveryID
		r.Action = m.Action
		r.ModerationParameters = cloneParameters(m.Parameters)
		r.BanDurationMS = m.BanDuration.Milliseconds()
		if !m.ExpiresAt.IsZero() {
			expires := m.ExpiresAt
			r.TimeoutExpiresAt = &expires
		}
		if m.SourceChannelID != "" || m.SourceChannelLogin != "" {
			r.SourceChannel = &automation.Channel{ID: m.SourceChannelID, Login: m.SourceChannelLogin}
		}
		r.Reason = m.Reason
		if m.ModeratorID != "" || m.ModeratorLogin != "" {
			r.Moderator = &automation.Author{ID: m.ModeratorID, Login: m.ModeratorLogin, DisplayName: m.ModeratorName}
		}
		r.TargetMessageID = m.TargetMessageID
		r.TargetUserID = m.TargetUserID
		r.Author.Login = m.TargetLogin
		r.Author.DisplayName = m.TargetName
		if !m.Timestamp.IsZero() {
			r.EventAt = m.Timestamp
			r.TimeSource = "irc_server"
			if transport == "eventsub" {
				r.TimeSource = "eventsub_envelope"
			}
		}
	default:
		return r, false
	}
	r.Channel.Login = strings.TrimPrefix(strings.ToLower(r.Channel.Login), "#")
	return r, true
}

func cloneParameters(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
func publicBadges(values []twitch.Badge) []automation.Badge {
	if len(values) == 0 {
		return nil
	}
	badges := make([]automation.Badge, len(values))
	for i, badge := range values {
		badges[i] = automation.Badge{SetID: badge.SetID, ID: badge.ID, Info: badge.Info}
	}
	return badges
}
