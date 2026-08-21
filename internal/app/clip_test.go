package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
)

type appFakeClipManager struct {
	clip   twitch.Clip
	err    error
	calls  int
	lastID string
}

func (f *appFakeClipManager) CreateClip(_ context.Context, broadcasterID string) (twitch.Clip, error) {
	f.calls++
	f.lastID = broadcasterID
	return f.clip, f.err
}

func TestParseClipCommandRecognizesVariants(t *testing.T) {
	cases := []struct {
		name       string
		draft      string
		wantOK     bool
		wantErr    bool
		wantStart  string
		wantEnd    string
		wantHasEnd bool
	}{
		{name: "not a clip command", draft: "hello there", wantOK: false},
		{name: "bare clip", draft: "/clip", wantOK: true},
		{name: "single offset", draft: "/clip T-5m", wantOK: true, wantStart: "5m"},
		{name: "two offsets", draft: "/CLIP T-4m T-2m", wantOK: true, wantStart: "4m", wantHasEnd: true, wantEnd: "2m"},
		{name: "too many args", draft: "/clip T-4m T-2m T-1m", wantOK: true, wantErr: true},
		{name: "bad unit", draft: "/clip T-5x", wantOK: true, wantErr: true},
		{name: "malformed token", draft: "/clip 5m", wantOK: true, wantErr: true},
		{name: "start not before end", draft: "/clip T-2m T-4m", wantOK: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offsets, ok, err := parseClipCommand(tc.draft)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if tc.wantStart != "" && (!offsets.HasStart || offsets.StartLabel != tc.wantStart) {
				t.Fatalf("offsets = %#v, want start label %q", offsets, tc.wantStart)
			}
			if offsets.HasEnd != tc.wantHasEnd {
				t.Fatalf("HasEnd = %v, want %v", offsets.HasEnd, tc.wantHasEnd)
			}
			if tc.wantHasEnd && offsets.EndLabel != tc.wantEnd {
				t.Fatalf("EndLabel = %q, want %q", offsets.EndLabel, tc.wantEnd)
			}
		})
	}
}

func TestLiveShellClipInputCreatesClip(t *testing.T) {
	fake := &appFakeClipManager{clip: twitch.Clip{ID: "abc", EditURL: "https://clips.twitch.tv/abc/edit"}}
	model := newLiveModelWithClock("example", config.Default(), NewFakeChatClient(1), nil)
	model.focus = focusComposer
	model.services.clipManager = fake
	model.selfBroadcasterID = "123"
	model.activeChannelState().composerText = "/clip T-5m"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if cmd == nil {
		t.Fatal("/clip Enter returned nil command, want clip-create command")
	}
	if got := model.activeChannelState().composerText; got != "" {
		t.Fatalf("composerText after /clip = %q, want cleared", got)
	}

	msg := cmd().(clipCreatedMsg)
	if msg.err != nil {
		t.Fatalf("clipCreatedMsg.err = %v, want nil", msg.err)
	}
	updated, _ = model.Update(msg)
	model = updated.(shellModel)

	if fake.calls != 1 || fake.lastID != "123" {
		t.Fatalf("CreateClip calls = %d lastID = %q, want 1 call for broadcaster 123", fake.calls, fake.lastID)
	}
	feedback := model.activeChannelState().sendFeedback
	if !strings.Contains(feedback, "https://clips.twitch.tv/abc/edit") || !strings.Contains(feedback, "5m ago") {
		t.Fatalf("sendFeedback = %q, want edit URL and requested range", feedback)
	}
	if len(model.activity.activityLog) != 1 || model.activity.activityLog[0].Kind != activityClip {
		t.Fatalf("activityLog = %#v, want one activityClip entry", model.activity.activityLog)
	}
	if !strings.Contains(model.activity.activityLog[0].Text, "https://clips.twitch.tv/abc/edit") {
		t.Fatalf("activity entry text = %q, want edit URL", model.activity.activityLog[0].Text)
	}
}

func TestLiveShellClipInputSurfacesParseError(t *testing.T) {
	model := newLiveModelWithClock("example", config.Default(), NewFakeChatClient(1), nil)
	model.focus = focusComposer
	model.services.clipManager = &appFakeClipManager{}
	model.activeChannelState().composerText = "/clip T-2m T-4m"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if cmd != nil {
		t.Fatal("/clip with invalid offsets returned a command, want nil (no network call)")
	}
	if got := model.activeChannelState().sendFeedback; !strings.Contains(got, "further in the past") {
		t.Fatalf("sendFeedback = %q, want offset-order error", got)
	}
}

func TestLiveShellClipInputUnavailableWithoutClipManager(t *testing.T) {
	model := newLiveModelWithClock("example", config.Default(), NewFakeChatClient(1), nil)
	model.focus = focusComposer
	model.activeChannelState().composerText = "/clip"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if cmd != nil {
		t.Fatal("/clip with no clipManager returned a command, want nil")
	}
	if got := model.activeChannelState().sendFeedback; !strings.Contains(got, "twi login") {
		t.Fatalf("sendFeedback = %q, want login hint", got)
	}
}

func TestClipCreateFailureIsUserFriendlyOnMissingScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: clips:edit"}`)
	}))
	defer server.Close()

	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.services.clipManager = helix.NewClipsClient(helix.ClipsClientConfig{Endpoint: server.URL})
	model.selfBroadcasterID = "123"
	state := model.channels.ensure("example")
	model.channels.active = "example"

	cmd := model.scheduleClipCreate(state, clipOffsets{})
	msg := cmd().(clipCreatedMsg)
	if !twitch.IsMissingScope(msg.err) {
		t.Fatalf("err = %v, want a missing-scope error", msg.err)
	}
	model = model.applyClipCreated(msg)
	if !strings.Contains(state.sendFeedback, "twi login") {
		t.Fatalf("sendFeedback = %q, want re-login hint", state.sendFeedback)
	}
}

func TestClipCreateFailureIsUserFriendlyOnNotLive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Not Found","status":404,"message":"broadcaster not streaming"}`)
	}))
	defer server.Close()

	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.services.clipManager = helix.NewClipsClient(helix.ClipsClientConfig{Endpoint: server.URL})
	model.selfBroadcasterID = "123"
	state := model.channels.ensure("example")
	model.channels.active = "example"

	cmd := model.scheduleClipCreate(state, clipOffsets{})
	msg := cmd().(clipCreatedMsg)
	if !twitch.IsClipCreationUnavailable(msg.err) {
		t.Fatalf("err = %v, want a not-live error", msg.err)
	}
	model = model.applyClipCreated(msg)
	if !strings.Contains(state.sendFeedback, "not currently live") {
		t.Fatalf("sendFeedback = %q, want not-live hint", state.sendFeedback)
	}
}
