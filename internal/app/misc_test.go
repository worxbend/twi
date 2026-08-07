package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

type appFakeMarkerManager struct {
	markers   []twitch.StreamMarker
	getErr    error
	created   twitch.StreamMarker
	createErr error

	lastCreateDescription string
	createCalls           int
}

func (f *appFakeMarkerManager) GetStreamMarkers(context.Context, string, int) ([]twitch.StreamMarker, error) {
	return f.markers, f.getErr
}

func (f *appFakeMarkerManager) CreateStreamMarker(_ context.Context, _ string, description string) (twitch.StreamMarker, error) {
	f.createCalls++
	f.lastCreateDescription = description
	return f.created, f.createErr
}

func TestTabSwitchAltThreeOpensMiscTab(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.width, model.height = 88, 20

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true})
	model = updated.(shellModel)
	if model.activeTab != tabMisc {
		t.Fatalf("activeTab after alt+3 = %v, want tabMisc", model.activeTab)
	}
	view := model.View()
	if !strings.Contains(view, "*3:Misc") {
		t.Fatalf("view missing active misc tab marker:\n%s", view)
	}
	if !strings.Contains(view, "Unavailable") {
		t.Fatalf("view with no markerManager should show unavailable message:\n%s", view)
	}
}

func TestMiscLoadsAndDisplaysMarkers(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "streamer"
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.markerManager = &appFakeMarkerManager{markers: []twitch.StreamMarker{
		{ID: "1", Description: "hype moment", PositionSeconds: 65},
		{ID: "2", Description: "", PositionSeconds: 3725},
	}}
	model.userLookup = &appFakeUserLookup{users: []twitch.UserIdentity{{UserID: "123", Login: "streamer"}}}

	updated, cmd := model.switchToTab(tabMisc)
	model = updated.(shellModel)
	if cmd == nil {
		t.Fatal("switchToTab(tabMisc) returned nil command, want a load command")
	}
	msg := cmd()
	loaded, ok := msg.(miscMarkersLoadedMsg)
	if !ok {
		t.Fatalf("command returned %T, want miscMarkersLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("miscMarkersLoadedMsg.err = %v, want nil", loaded.err)
	}
	if loaded.broadcasterID != "123" {
		t.Fatalf("broadcasterID = %q, want 123", loaded.broadcasterID)
	}

	model = model.applyMiscLoaded(loaded)
	if model.selfBroadcasterID != "123" {
		t.Fatalf("selfBroadcasterID = %q, want 123 (shared with Stream Info)", model.selfBroadcasterID)
	}
	view := model.miscView(model.layout())
	for _, want := range []string{"1:05", "hype moment", "1:02:05", "(no description)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("misc view missing %q:\n%s", want, view)
		}
	}
}

func TestMiscLoadFailureSurfacesError(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabMisc
	model.markerManager = &appFakeMarkerManager{getErr: errors.New("twitch says no")}
	model.selfBroadcasterID = "123" // skip user lookup

	cmd := model.scheduleMiscLoad()
	if cmd == nil {
		t.Fatal("scheduleMiscLoad returned nil command")
	}
	loaded := cmd().(miscMarkersLoadedMsg)
	if loaded.err == nil {
		t.Fatal("miscMarkersLoadedMsg.err = nil, want error")
	}
	model = model.applyMiscLoaded(loaded)
	view := model.miscView(model.layout())
	if !strings.Contains(view, "Load failed") || !strings.Contains(view, "twitch says no") {
		t.Fatalf("view missing load error:\n%s", view)
	}
}

func TestMiscCreateMarkerFlow(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabMisc
	fake := &appFakeMarkerManager{created: twitch.StreamMarker{ID: "99", Description: "big play", PositionSeconds: 42}}
	model.markerManager = fake
	model.selfBroadcasterID = "123"
	model.misc.loaded = true

	updated, _ := model.handleMiscKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if !model.misc.editing {
		t.Fatal("editing = false after enter, want true")
	}

	for _, r := range "big play" {
		updated, _ = model.handleMiscKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(shellModel)
	}

	updated, cmd := model.handleMiscKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.misc.editing {
		t.Fatal("editing = true after commit, want false")
	}
	if cmd == nil {
		t.Fatal("committing description returned nil command, want create command")
	}
	createdMsg := cmd().(miscMarkerCreatedMsg)
	if createdMsg.err != nil {
		t.Fatalf("miscMarkerCreatedMsg.err = %v, want nil", createdMsg.err)
	}
	if fake.lastCreateDescription != "big play" {
		t.Fatalf("CreateStreamMarker description = %q, want %q", fake.lastCreateDescription, "big play")
	}

	model = model.applyMiscMarkerCreated(createdMsg)
	if !model.misc.createOK {
		t.Fatal("createOK = false after successful create")
	}
	if len(model.misc.markers) != 1 || model.misc.markers[0].ID != "99" {
		t.Fatalf("markers = %#v, want the newly created marker prepended", model.misc.markers)
	}
}

func TestMiscCreateMarkerFailureIsUserFriendlyOnMissingScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: channel:manage:broadcast"}`)
	}))
	defer server.Close()

	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.markerManager = twitch.NewHelixMarkersClient(twitch.HelixMarkersClientConfig{Endpoint: server.URL})
	model.selfBroadcasterID = "123"

	cmd := model.scheduleCreateMarker("")
	msg := cmd().(miscMarkerCreatedMsg)
	if !twitch.IsMissingScope(msg.err) {
		t.Fatalf("err = %v, want a missing-scope error", msg.err)
	}
	model = model.applyMiscMarkerCreated(msg)
	if !strings.Contains(model.misc.createErr, "twi login") {
		t.Fatalf("createErr = %q, want re-login hint", model.misc.createErr)
	}
}

func TestMiscLoadFailureShowsNotLiveHintOnNoVideoFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Not Found","status":404,"message":"Unable to find user's most recent Video/VOD ID. Please ensure you are passing the correct user ID!"}`)
	}))
	defer server.Close()

	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabMisc
	model.markerManager = twitch.NewHelixMarkersClient(twitch.HelixMarkersClientConfig{Endpoint: server.URL})
	model.selfBroadcasterID = "123"

	loaded := model.scheduleMiscLoad()().(miscMarkersLoadedMsg)
	if !twitch.IsNoVideoFound(loaded.err) {
		t.Fatalf("loaded.err = %v, want a no-video-found error", loaded.err)
	}
	model = model.applyMiscLoaded(loaded)
	if !strings.Contains(model.misc.loadErr, "not currently live") {
		t.Fatalf("loadErr = %q, want a not-live hint", model.misc.loadErr)
	}
	view := model.miscView(model.layout())
	if !strings.Contains(view, "not currently live") {
		t.Fatalf("misc view missing not-live hint:\n%s", view)
	}
}

func TestMoveMiscSelectionWrapsAndHandlesEmpty(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.moveMiscSelection(-1)
	if model.misc.selected != 0 {
		t.Fatalf("selected = %d with no markers, want 0", model.misc.selected)
	}
	model.misc.markers = []twitch.StreamMarker{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	model.moveMiscSelection(-1)
	if model.misc.selected != 2 {
		t.Fatalf("selected after moving up from 0 = %d, want 2 (wrap)", model.misc.selected)
	}
	model.moveMiscSelection(1)
	model.moveMiscSelection(1)
	if model.misc.selected != 1 {
		t.Fatalf("selected = %d, want 1 (wrap forward)", model.misc.selected)
	}
}
