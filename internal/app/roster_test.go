package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func TestRosterObservesMessagesAndBadges(t *testing.T) {
	roster := newChatterRoster()
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	roster.observeMessage(twitch.ChatMessage{
		AuthorLogin: "Alice",
		DisplayName: "Alice",
		AuthorColor: "#ff0000",
		Timestamp:   at,
		Badges: []twitch.Badge{
			{SetID: "moderator", ID: "1"},
			{SetID: "subscriber", ID: "12", Info: "26"},
		},
	})

	entry, ok := roster.lookup("alice")
	if !ok {
		t.Fatal("lookup(alice) not found; logins must be matched case-insensitively")
	}
	if entry.DisplayName != "Alice" || entry.Color != "#ff0000" {
		t.Fatalf("entry identity = %q/%q, want Alice/#ff0000", entry.DisplayName, entry.Color)
	}
	if !entry.Moderator || !entry.Subscriber {
		t.Fatalf("roles = mod:%v sub:%v, want both true", entry.Moderator, entry.Subscriber)
	}
	if entry.SubscribedMonths != 26 {
		t.Fatalf("SubscribedMonths = %d, want 26 from badge-info", entry.SubscribedMonths)
	}
	if got := entry.roleLabel(); got != "mod" {
		t.Fatalf("roleLabel = %q, want mod to outrank sub", got)
	}

	// A later badge-less message must not demote a known moderator.
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "alice", Timestamp: at.Add(time.Minute)})
	if entry, _ = roster.lookup("alice"); !entry.Moderator {
		t.Fatal("badge-less message cleared moderator role, want roles to be sticky")
	}
	if entry.Messages != 2 {
		t.Fatalf("Messages = %d, want 2", entry.Messages)
	}
}

func TestRosterActiveCountPrefersMembershipOverRecency(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	roster := newChatterRoster()

	// Without membership, presence falls back to recent speakers.
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "recent", Timestamp: now.Add(-time.Minute)})
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "stale", Timestamp: now.Add(-2 * rosterActiveWindow)})
	if got := roster.activeCount(now); got != 1 {
		t.Fatalf("recency-based activeCount = %d, want 1 (stale speaker outside the window)", got)
	}

	// Once membership arrives it becomes authoritative.
	roster.observeMembership(twitch.MembershipEvent{Type: twitch.MembershipJoin, Login: "lurker", At: now})
	roster.observeMembership(twitch.MembershipEvent{Type: twitch.MembershipJoin, Login: "stale", At: now})
	if got := roster.activeCount(now); got != 3 {
		t.Fatalf("membership activeCount = %d, want 3 (both joiners plus the speaker)", got)
	}
	roster.observeMembership(twitch.MembershipEvent{Type: twitch.MembershipPart, Login: "stale", At: now})
	if got := roster.activeCount(now); got != 2 {
		t.Fatalf("activeCount after part = %d, want 2", got)
	}
}

func TestRosterCompletionsRankRecentChattersFirst(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	roster := newChatterRoster()
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "streamfan", Timestamp: now.Add(-time.Hour)})
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "streamer", Timestamp: now})
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "unrelated", Timestamp: now})

	matches := roster.completions("stream", 10)
	if len(matches) != 2 {
		t.Fatalf("completions(stream) = %d entries, want 2", len(matches))
	}
	if matches[0].Login != "streamer" {
		t.Fatalf("first completion = %q, want streamer (most recently seen)", matches[0].Login)
	}
	if got := roster.completions("@STREAM", 10); len(got) != 2 {
		t.Fatalf("completions ignored @ prefix or case: got %d entries, want 2", len(got))
	}
	if got := roster.completions("stream", 1); len(got) != 1 {
		t.Fatalf("completions did not honor limit: got %d entries, want 1", len(got))
	}
}

func TestRosterApplyFollowersOnlyAnnotatesKnownChatters(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	followedAt := now.Add(-720 * time.Hour)
	roster := newChatterRoster()
	roster.observeMessage(twitch.ChatMessage{AuthorLogin: "chatter", Timestamp: now})
	roster.applyFollowers([]twitch.Follower{
		{UserLogin: "chatter", FollowedAt: followedAt},
		{UserLogin: "never-spoke", FollowedAt: followedAt},
	})

	entry, _ := roster.lookup("chatter")
	if !entry.FollowKnown || !entry.FollowsSince.Equal(followedAt) {
		t.Fatalf("chatter follow state = known:%v since:%v, want true/%v", entry.FollowKnown, entry.FollowsSince, followedAt)
	}
	if _, ok := roster.lookup("never-spoke"); ok {
		t.Fatal("applyFollowers invented a roster entry for a user never seen in chat")
	}
}

func TestMembershipEventsFeedRosterAndActivityLog(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 120, 24
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	updated, _ := model.Update(chatClientMembershipMsg{ok: true, membership: twitch.MembershipEvent{
		Type: twitch.MembershipJoin, Channel: "alpha", Login: "newviewer", At: at,
	}})
	model = updated.(mockShellModel)

	entry, ok := model.activeChannelState().roster.lookup("newviewer")
	if !ok || !entry.Present {
		t.Fatalf("join did not mark chatter present: found=%v", ok)
	}
	if len(model.activityLog) != 1 || !strings.Contains(model.activityLog[0].Text, "newviewer joined") {
		t.Fatalf("activity log = %+v, want a 'newviewer joined' entry", model.activityLog)
	}
	if model.activityLog[0].Kind != activityMembership {
		t.Fatalf("activity kind = %q, want %q", model.activityLog[0].Kind, activityMembership)
	}

	updated, _ = model.Update(chatClientMembershipMsg{ok: true, membership: twitch.MembershipEvent{
		Type: twitch.MembershipPart, Channel: "alpha", Login: "newviewer", At: at.Add(time.Second),
	}})
	model = updated.(mockShellModel)
	if entry, _ = model.activeChannelState().roster.lookup("newviewer"); entry.Present {
		t.Fatal("part did not clear presence")
	}
	if len(model.activityLog) != 2 || !strings.Contains(model.activityLog[1].Text, "newviewer left") {
		t.Fatalf("activity log = %+v, want a 'newviewer left' entry", model.activityLog)
	}
}

func TestMembershipBurstCollapsesIntoOneSummaryRow(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	const joins = 40
	for i := 0; i < joins; i++ {
		model.applyMembershipEvent(twitch.MembershipEvent{
			Type:    twitch.MembershipJoin,
			Channel: "alpha",
			Login:   "viewer" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			At:      at.Add(time.Duration(i) * time.Millisecond),
		})
	}

	// The first few are logged individually, the rest collapse into one row
	// that is rewritten in place - so a reconnect burst costs a bounded
	// number of lines rather than one per join.
	want := membershipActivityBurst + 1
	if got := len(model.activityLog); got != want {
		t.Fatalf("activity log rows after %d joins = %d, want %d", joins, got, want)
	}
	summary := model.activityLog[len(model.activityLog)-1].Text
	if !strings.Contains(summary, "more joined/left") {
		t.Fatalf("last row = %q, want a collapsed burst summary", summary)
	}
	if !strings.Contains(summary, "35") {
		t.Fatalf("summary = %q, want it to report the 35 collapsed events", summary)
	}
}

func TestChatPaneTitleAlwaysShowsActiveChatterCount(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 100, 24

	// Without membership the count is marked approximate.
	if got := ansi.Strip(model.View()); !strings.Contains(got, "👥 ~") {
		t.Fatalf("chat title missing approximate chatter count:\n%s", got)
	}

	updated, _ := model.Update(chatClientMembershipMsg{ok: true, membership: twitch.MembershipEvent{
		Type: twitch.MembershipJoin, Channel: "alpha", Login: "viewer-one", At: time.Now(),
	}})
	model = updated.(mockShellModel)

	// Once membership arrives the count becomes exact and drops the "~".
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "👥 1") || strings.Contains(view, "👥 ~") {
		t.Fatalf("chat title should show an exact count once membership is seen:\n%s", view)
	}
}

func TestClosedMembershipStreamDoesNotReportDisconnect(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	before := model.activeChannelState().status.Status

	// Twitch stops sending membership for busy channels; that must not be
	// mistaken for the chat connection dropping.
	updated, cmd := model.Update(chatClientMembershipMsg{ok: false})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("closed membership stream returned command %#v, want nil", cmd)
	}
	if got := model.activeChannelState().status.Status; got != before {
		t.Fatalf("connection status = %v, want it unchanged at %v", got, before)
	}
}

// Twitch never echoes a user's own PRIVMSG back, so twi renders its own sent
// messages from a local echo. Without USERSTATE badges that echo is the one
// place in chat where the user's own broadcaster/mod badge is missing.
func TestOwnBadgesFromUserStateAppearOnLocalEcho(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Twitch.Username = "streamerguy"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 100, 24

	updated, _ := model.Update(chatClientUserStateMsg{ok: true, state: twitch.UserState{
		Channel:     "alpha",
		AuthorLogin: "streamerguy",
		DisplayName: "StreamerGuy",
		AuthorColor: "#ff0000",
		Badges:      []twitch.Badge{{SetID: "broadcaster", ID: "1"}},
	}})
	model = updated.(mockShellModel)

	echo := model.localEchoMessage(queuedComposerSend{ID: 1, Text: "hello"}, SendResult{}, "alpha")
	if len(echo.Badges) != 1 || echo.Badges[0].SetID != "broadcaster" {
		t.Fatalf("local echo badges = %+v, want the broadcaster badge from USERSTATE", echo.Badges)
	}
	if echo.DisplayName != "StreamerGuy" {
		t.Fatalf("local echo display name = %q, want StreamerGuy from USERSTATE", echo.DisplayName)
	}
	if echo.AuthorColor != "#ff0000" {
		t.Fatalf("local echo color = %q, want #ff0000 from USERSTATE", echo.AuthorColor)
	}
}

func TestUserStateRecordsOwnRoleWithoutInventingMessages(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Twitch.Username = "streamerguy"
	model := newMockShellModel("alpha", cfg)

	updated, _ := model.Update(chatClientUserStateMsg{ok: true, state: twitch.UserState{
		Channel:     "alpha",
		AuthorLogin: "streamerguy",
		DisplayName: "StreamerGuy",
		Badges:      []twitch.Badge{{SetID: "broadcaster", ID: "1"}},
	}})
	model = updated.(mockShellModel)

	entry, ok := model.activeChannelState().roster.lookup("streamerguy")
	if !ok {
		t.Fatal("USERSTATE did not add the user to the roster")
	}
	if got := entry.roleLabel(); got != "broadcaster" {
		t.Fatalf("own role = %q, want broadcaster", got)
	}
	// USERSTATE is not a message; the tally must not claim otherwise.
	if entry.Messages != 0 {
		t.Fatalf("Messages = %d after USERSTATE alone, want 0", entry.Messages)
	}
}

func TestClosedUserStateStreamIsNotAFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	before := model.activeChannelState().status.Status

	updated, cmd := model.Update(chatClientUserStateMsg{ok: false})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("closed USERSTATE stream returned command %#v, want nil", cmd)
	}
	if got := model.activeChannelState().status.Status; got != before {
		t.Fatalf("connection status = %v, want it unchanged at %v", got, before)
	}
}
