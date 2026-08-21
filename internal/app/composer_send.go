package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

// This file holds the composer's send queue. Sending a chat message is not
// instant: the request goes out to Twitch and the answer comes back later as
// a composerSendCompletedMsg. Each channel therefore keeps a queue of what
// the user has pressed enter on, sends one at a time, and - when a send fails
// - hands the text back to the composer instead of dropping it.

// queueComposerSend turns whatever is in the composer into a queued send. It
// first gives the slash commands that never reach Twitch (/channels, /clip)
// their chance to claim the draft, then appends the rest to this channel's
// queue and starts it if nothing else is in flight.
func (m *shellModel) queueComposerSend() (shellModel, tea.Cmd) {
	state := m.activeChannelState()
	draft := strings.TrimSpace(state.composerText)
	// /channels never reaches Twitch: bare, it opens the picker; with a name,
	// it opens that channel directly.
	if channel, isChannelCommand := composerChannelCommand(draft); isChannelCommand {
		state.composerText = ""
		if channel == "" {
			return *m, m.openChannelPicker()
		}
		return m.openChannel(channel)
	}
	if m.channels.empty() {
		state.sendState = composerSendFailed
		state.sendFeedback = "no channel open: /channels or " + channelPickerKeyHint
		return *m, nil
	}
	if offsets, isClip, parseErr := parseClipCommand(draft); isClip {
		if parseErr != nil {
			state.sendState = composerSendFailed
			state.sendFeedback = "clip: " + parseErr.Error()
			return *m, nil
		}
		state.composerText = ""
		state.replyTo = nil
		return *m, m.scheduleClipCreate(state, offsets)
	}
	text, action := composerSendText(draft)
	if text == "" {
		return *m, nil
	}
	if m.services.client == nil {
		state.sendState = composerSendFailed
		state.sendFeedback = "send unavailable for this chat source"
		return *m, nil
	}

	m.nextSend++
	channel := state.name
	state.sendQueue = append(state.sendQueue, queuedComposerSend{
		ID:               m.nextSend,
		Channel:          channel,
		Text:             text,
		Draft:            draft,
		ReplyToMessageID: replyMessageID(state.replyTo),
		Action:           action,
		Reply:            cloneComposerReply(state.replyTo),
	})
	state.composerText = ""
	state.replyTo = nil
	state.sendState = composerSendQueued
	state.sendFeedback = fmt.Sprintf("queued for #%s", channel)
	m.debugSendQueued(state.sendQueue[len(state.sendQueue)-1])
	return *m, m.startNextComposerSend(state)
}

// startNextComposerSend takes the head of the queue and asks the client to
// send it, returning the command that waits for the answer. It does nothing
// when a send is already in flight, so one channel never has two requests
// outstanding and replies keep their order.
func (m *shellModel) startNextComposerSend(state *channelState) tea.Cmd {
	if state == nil || state.activeSend != nil || len(state.sendQueue) == 0 {
		return nil
	}
	next := state.sendQueue[0]
	state.sendQueue = state.sendQueue[1:]
	state.activeSend = &next
	state.sendState = composerSendSending
	state.sendFeedback = fmt.Sprintf("sending to #%s", next.Channel)
	if next.ReplyToMessageID != "" {
		state.sendFeedback = "sending reply to " + replyAuthor(next.Reply)
	}
	if next.Action {
		state.sendFeedback = "sending action to #" + next.Channel
	}
	m.debugSendStart(next)
	client := m.services.client
	req := SendRequest{
		Channel:          next.Channel,
		Text:             next.Text,
		ReplyToMessageID: next.ReplyToMessageID,
		Action:           next.Action,
	}
	return func() tea.Msg {
		result, err := client.Send(context.Background(), req)
		return composerSendCompletedMsg{id: next.ID, result: result, err: err}
	}
}

// completeComposerSend applies the answer to a send that was in flight. On
// success it shows the message locally, because Twitch does not echo a user's
// own message back; on failure it restores the draft and reply context so
// nothing the user typed is lost.
func (m shellModel) completeComposerSend(msg composerSendCompletedMsg) (shellModel, tea.Cmd) {
	state := m.channelStateForActiveSend(msg.id)
	if state == nil || state.activeSend == nil {
		return m, nil
	}

	sent := *state.activeSend
	state.activeSend = nil
	m.debugSendComplete(sent, msg.result, msg.err)
	if msg.err != nil {
		state.failWith(composerSendFailed, "failed: "+credentialSafeSendDetail(msg.err), sent)
		return m, nil
	}
	if msg.result.RateLimited {
		state.failWith(composerSendRateLimited, "rate limited: "+sendResultDetail(msg.result), sent)
		return m, nil
	}

	state.sendState = composerSendSucceeded
	state.sendFeedback = sendResultDetail(msg.result)
	m.appendLocalEcho(sent, msg.result)
	return m, m.startNextComposerSend(state)
}

// failWith records why a send did not go through and undoes the queue. The
// message that failed and everything still waiting behind it go back into the
// composer, along with the reply they were aimed at, so the user can fix the
// problem and press enter again rather than retyping.
func (s *channelState) failWith(state composerSendState, feedback string, sent queuedComposerSend) {
	s.sendState = state
	s.sendFeedback = feedback
	texts, reply := s.drainUnsentComposerSends(sent)
	s.restoreComposerText(texts...)
	s.replyTo = reply
}

// appendLocalEcho renders the user's own just-sent message into their chat.
// Twitch does not send a client its own PRIVMSG back, so without this the
// message the user typed would appear to vanish.
func (m *shellModel) appendLocalEcho(sent queuedComposerSend, result SendResult) {
	state := m.channels.ensure(sent.Channel)
	if state == nil {
		return
	}
	message := m.localEchoMessage(sent, result, state.name)
	if message.ID != "" {
		if state.localEchoes == nil {
			state.localEchoes = make(map[string]struct{})
		}
		state.localEchoes[message.ID] = struct{}{}
	}
	m.appendStaticMessage(message, channelKey(state.name) == m.channels.active && state.scrollOffset > 0)
}

func (m shellModel) localEchoMessage(sent queuedComposerSend, result SendResult, channel string) twitch.ChatMessage {
	at := result.AcceptedAt
	if at.IsZero() && m.channels != nil && m.channels.clock != nil {
		at = m.channels.clock.Now()
	}
	if at.IsZero() {
		at = time.Now()
	}
	id := strings.TrimSpace(result.MessageID)
	if id == "" {
		id = fmt.Sprintf("local-send-%d", sent.ID)
	}
	author := strings.TrimSpace(m.mentionLogin)
	if author == "" {
		author = "me"
	}
	messageType := twitch.MessageTypeChat
	if sent.Action {
		messageType = twitch.MessageTypeAction
	}
	// Twitch never echoes the user's own PRIVMSG back, so this local echo is
	// the only render of it - it has to carry the badges and identity that
	// USERSTATE reported, or the user is the one person in chat whose own
	// broadcaster/mod badge never appears.
	display := author
	color := "#9146ff"
	var badges []twitch.Badge
	if state := m.channels.ensure(channel); state != nil {
		badges = state.selfBadges
		if name := strings.TrimSpace(state.selfDisplayName); name != "" {
			display = name
		}
		if value := strings.TrimSpace(state.selfColor); value != "" {
			color = value
		}
	}
	return twitch.ChatMessage{
		ID:          id,
		Channel:     channel,
		Timestamp:   at,
		AuthorLogin: author,
		DisplayName: display,
		AuthorColor: color,
		Badges:      badges,
		Text:        sent.Text,
		Type:        messageType,
		Reply:       replyFromComposerContext(sent.Reply),
	}
}

func (m shellModel) channelStateForActiveSend(id int) *channelState {
	if m.channels == nil {
		return nil
	}
	for _, state := range m.channels.states {
		if state != nil && state.activeSend != nil && state.activeSend.ID == id {
			return state
		}
	}
	return nil
}

func sendResultDetail(result SendResult) string {
	if result.Detail != "" {
		return result.Detail
	}
	if result.RateLimited {
		if result.RetryAfter > 0 {
			return "retry in " + result.RetryAfter.String()
		}
		return "Twitch is slowing message sends"
	}
	if !result.AcceptedAt.IsZero() {
		return "accepted"
	}
	return "accepted"
}

func (s queuedComposerSend) restoreText() string {
	if s.Draft != "" {
		return s.Draft
	}
	if s.Action {
		return "/me " + s.Text
	}
	return s.Text
}

func composerSendText(draft string) (string, bool) {
	text := strings.TrimSpace(draft)
	lower := strings.ToLower(text)
	if lower == "/me" {
		return "", true
	}
	if strings.HasPrefix(lower, "/me ") || strings.HasPrefix(lower, "/me\t") {
		return strings.TrimSpace(text[len("/me"):]), true
	}
	return text, false
}

func replyMessageID(reply *composerReplyContext) string {
	if reply == nil {
		return ""
	}
	return reply.MessageID
}

func cloneComposerReply(reply *composerReplyContext) *composerReplyContext {
	if reply == nil {
		return nil
	}
	copied := *reply
	return &copied
}

func replyFromComposerContext(reply *composerReplyContext) *twitch.Reply {
	if reply == nil || reply.MessageID == "" {
		return nil
	}
	return &twitch.Reply{
		ParentMessageID: reply.MessageID,
		ParentLogin:     reply.Author,
		ParentAuthor:    reply.Author,
		ParentText:      reply.Text,
	}
}

func replyAuthor(reply *composerReplyContext) string {
	if reply == nil || reply.Author == "" {
		return "message"
	}
	return reply.Author
}

func commonReplyContext(active queuedComposerSend, queued []queuedComposerSend) *composerReplyContext {
	all := make([]queuedComposerSend, 0, len(queued)+1)
	all = append(all, active)
	all = append(all, queued...)

	var common *composerReplyContext
	for _, send := range all {
		if send.ReplyToMessageID == "" {
			return nil
		}
		if common == nil {
			common = cloneComposerReply(send.Reply)
			continue
		}
		if send.ReplyToMessageID != common.MessageID {
			return nil
		}
	}
	return common
}
