package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

const clipRequestTimeout = 5 * time.Second

// clipOffsets carries the optional "T-<offset>" arguments from a /clip
// command. They are never sent to Twitch - Create Clip has no parameter for
// a start time, end time, or duration, and only ever captures approximately
// the last 30-60 seconds at the moment of the call. They're kept purely to
// echo the requested range back to the user alongside the clip's edit URL,
// as a reminder of what to trim to in Twitch's own clip editor.
type clipOffsets struct {
	HasStart   bool
	StartLabel string
	StartAgo   time.Duration
	HasEnd     bool
	EndLabel   string
	EndAgo     time.Duration
}

// parseClipCommand recognizes a "/clip [T-<offset>] [T-<offset>]" chat
// command, e.g. "/clip", "/clip T-5m" (5 minutes ago until now), or
// "/clip T-4m T-2m" (between two past offsets). ok reports whether draft is
// a /clip command at all, so callers know to intercept it instead of
// sending it as chat text; when ok is true and err is non-nil, draft was a
// /clip command with invalid arguments.
func parseClipCommand(draft string) (offsets clipOffsets, ok bool, err error) {
	fields := strings.Fields(strings.TrimSpace(draft))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/clip") {
		return clipOffsets{}, false, nil
	}
	const usage = "usage: /clip [T-<offset>] [T-<offset>], e.g. /clip T-5m or /clip T-4m T-2m"

	args := fields[1:]
	if len(args) > 2 {
		return clipOffsets{}, true, errors.New(usage)
	}
	if len(args) >= 1 {
		ago, label, parseErr := parseClipOffset(args[0])
		if parseErr != nil {
			return clipOffsets{}, true, parseErr
		}
		offsets.HasStart = true
		offsets.StartAgo = ago
		offsets.StartLabel = label
	}
	if len(args) == 2 {
		ago, label, parseErr := parseClipOffset(args[1])
		if parseErr != nil {
			return clipOffsets{}, true, parseErr
		}
		if ago >= offsets.StartAgo {
			return clipOffsets{}, true, fmt.Errorf("start offset must be further in the past than end offset (e.g. /clip T-4m T-2m)")
		}
		offsets.HasEnd = true
		offsets.EndAgo = ago
		offsets.EndLabel = label
	}
	return offsets, true, nil
}

// parseClipOffset parses one "T-<number><unit>" token (unit is s, m, or h),
// returning how long ago it refers to and a normalized label for display.
func parseClipOffset(token string) (time.Duration, string, error) {
	invalid := fmt.Errorf("invalid offset %q, expected T-<number><s|m|h>, e.g. T-5m", token)
	lower := strings.ToLower(strings.TrimSpace(token))
	if !strings.HasPrefix(lower, "t-") || len(lower) < 3 {
		return 0, "", invalid
	}
	body := lower[2:]
	unit := body[len(body)-1]
	amount, err := strconv.Atoi(body[:len(body)-1])
	if err != nil || amount <= 0 {
		return 0, "", invalid
	}
	var unitDuration time.Duration
	switch unit {
	case 's':
		unitDuration = time.Second
	case 'm':
		unitDuration = time.Minute
	case 'h':
		unitDuration = time.Hour
	default:
		return 0, "", invalid
	}
	return time.Duration(amount) * unitDuration, body, nil
}

// formatClipOffsets renders the requested range for display, or "" when the
// command had no offsets at all (a plain "/clip").
func formatClipOffsets(offsets clipOffsets) string {
	switch {
	case offsets.HasStart && offsets.HasEnd:
		return "requested " + offsets.StartLabel + " ago -> " + offsets.EndLabel + " ago"
	case offsets.HasStart:
		return "requested " + offsets.StartLabel + " ago -> now"
	default:
		return ""
	}
}

// clipCreatedMsg is the result of a /clip command's Twitch Helix "Create
// Clip" call.
type clipCreatedMsg struct {
	channel       string
	broadcasterID string
	offsets       clipOffsets
	clip          twitch.Clip
	err           error
}

// scheduleClipCreate creates a clip of the active channel's current stream.
// state.sendState/sendFeedback (the same composer-feedback fields the chat
// send flow uses) carry progress and the result back to the status line,
// since a clip isn't itself a chat message.
func (m *shellModel) scheduleClipCreate(state *channelState, offsets clipOffsets) tea.Cmd {
	if m.clipManager == nil {
		state.sendState = composerSendFailed
		state.sendFeedback = "clip: unavailable (requires Twitch API credentials; run `twi login`)"
		return nil
	}

	clipManager := m.clipManager
	userLookup := m.userLookup
	username := m.effectiveConfig.Twitch.Username
	knownID := m.selfBroadcasterID
	channel := state.name

	state.sendState = composerSendSending
	state.sendFeedback = "clip: creating..."

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clipRequestTimeout)
		defer cancel()

		id := knownID
		if id == "" {
			resolved, err := resolveSelfBroadcasterID(ctx, userLookup, username)
			if err != nil {
				return clipCreatedMsg{channel: channel, offsets: offsets, err: err}
			}
			id = resolved
		}
		clip, err := clipManager.CreateClip(ctx, id)
		if err != nil {
			return clipCreatedMsg{channel: channel, broadcasterID: id, offsets: offsets, err: err}
		}
		return clipCreatedMsg{channel: channel, broadcasterID: id, offsets: offsets, clip: clip}
	}
}

func (m shellModel) applyClipCreated(msg clipCreatedMsg) shellModel {
	if msg.broadcasterID != "" {
		m.selfBroadcasterID = msg.broadcasterID
	}
	state := m.channels.ensure(msg.channel)
	if state == nil {
		return m
	}
	if msg.err != nil {
		state.sendState = composerSendFailed
		state.sendFeedback = "clip: " + clipErrorMessage(msg.err)
		return m
	}

	state.sendState = composerSendSucceeded
	feedback := "clip created: " + msg.clip.EditURL
	if rangeLabel := formatClipOffsets(msg.offsets); rangeLabel != "" {
		feedback = "clip created (" + rangeLabel + ", trim in editor): " + msg.clip.EditURL
	}
	state.sendFeedback = feedback

	activityText := "Clip created: " + msg.clip.EditURL
	if rangeLabel := formatClipOffsets(msg.offsets); rangeLabel != "" {
		activityText = "Clip created (" + rangeLabel + "): " + msg.clip.EditURL
	}
	m.appendActivity(activityEntry{
		Kind:    activityClip,
		Channel: msg.channel,
		Text:    activityText,
	})
	return m
}

// clipErrorMessage mirrors miscErrorMessage/streamInfoErrorMessage: a 401
// from Create Clip means the current token predates (or otherwise lacks)
// clips:edit, and a 404 means Twitch has nothing to clip because the
// broadcaster isn't currently live.
func clipErrorMessage(err error) string {
	switch {
	case twitch.IsMissingScope(err):
		return "your Twitch login is missing the clips:edit scope (or the token expired); run `twi login` to re-authenticate"
	case twitch.IsClipCreationUnavailable(err):
		return "clips aren't available: you are not currently live"
	default:
		return err.Error()
	}
}
