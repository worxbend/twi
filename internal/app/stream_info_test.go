package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

type appFakeChannelManager struct {
	info       twitch.ChannelInfo
	getErr     error
	modifyErr  error
	lastUpdate twitch.ChannelInfoUpdate
	modified   bool
}

func (f *appFakeChannelManager) GetChannelInformation(context.Context, string) (twitch.ChannelInfo, error) {
	return f.info, f.getErr
}

func (f *appFakeChannelManager) ModifyChannelInformation(_ context.Context, _ string, update twitch.ChannelInfoUpdate) error {
	f.modified = true
	f.lastUpdate = update
	return f.modifyErr
}

type appFakeGameLookup struct {
	games map[string]twitch.Game
	err   error
}

func (f *appFakeGameLookup) SearchCategories(_ context.Context, query string, limit int) ([]twitch.Game, error) {
	if f.err != nil {
		return nil, f.err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var matches []twitch.Game
	for name, game := range f.games {
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			matches = append(matches, game)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

type appFakeUserLookup struct {
	users []twitch.UserIdentity
	err   error
}

func (f *appFakeUserLookup) GetUsers(context.Context, twitch.UserLookupRequest) ([]twitch.UserIdentity, error) {
	return f.users, f.err
}

func TestTabSwitchAltDigitTogglesBetweenChatAndStreamInfo(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.width, model.height = 88, 20

	if model.activeTab != tabChat {
		t.Fatalf("default activeTab = %v, want tabChat", model.activeTab)
	}
	if !strings.Contains(model.View(), "*1:Chat") {
		t.Fatalf("view missing active chat tab marker:\n%s", model.View())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	model = updated.(shellModel)
	if model.activeTab != tabStreamInfo {
		t.Fatalf("activeTab after alt+2 = %v, want tabStreamInfo", model.activeTab)
	}
	view := model.View()
	if !strings.Contains(view, "*2:Stream Info") {
		t.Fatalf("view missing active stream-info tab marker:\n%s", view)
	}
	if !strings.Contains(view, "Unavailable") {
		t.Fatalf("view with no channelManager should show unavailable message:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	model = updated.(shellModel)
	if model.activeTab != tabChat {
		t.Fatalf("activeTab after alt+1 = %v, want tabChat", model.activeTab)
	}
	if !strings.Contains(model.View(), "Message #example") {
		t.Fatalf("view after switching back to chat missing composer:\n%s", model.View())
	}
}

func TestStreamInfoLoadsAndDisplaysChannelInfo(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "streamer"
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.channelManager = &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		Title:         "Hello world",
		GameName:      "Just Chatting",
		Language:      "en",
		Tags:          []string{"English", "Chill"},
	}}
	model.userLookup = &appFakeUserLookup{users: []twitch.UserIdentity{{UserID: "123", Login: "streamer"}}}

	updated, cmd := model.switchToTab(tabStreamInfo)
	model = updated.(shellModel)
	if cmd == nil {
		t.Fatal("switchToTab(tabStreamInfo) returned nil command, want a load command")
	}
	msg := cmd()
	loaded, ok := msg.(streamInfoLoadedMsg)
	if !ok {
		t.Fatalf("command returned %T, want streamInfoLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("streamInfoLoadedMsg.err = %v, want nil", loaded.err)
	}
	if loaded.broadcasterID != "123" {
		t.Fatalf("broadcasterID = %q, want 123", loaded.broadcasterID)
	}

	model = model.applyStreamInfoLoaded(loaded)
	view := model.streamInfoView(model.layout())
	for _, want := range []string{"Title: Hello world", "Category: Just Chatting", "Language: en", "Tags: English, Chill"} {
		if !strings.Contains(view, want) {
			t.Fatalf("stream info view missing %q:\n%s", want, view)
		}
	}
}

func TestStreamInfoLoadFailureSurfacesError(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabStreamInfo
	model.channelManager = &appFakeChannelManager{getErr: errors.New("twitch says no")}
	model.selfBroadcasterID = "123" // skip user lookup

	cmd := model.scheduleStreamInfoLoad()
	if cmd == nil {
		t.Fatal("scheduleStreamInfoLoad returned nil command")
	}
	loaded := cmd().(streamInfoLoadedMsg)
	if loaded.err == nil {
		t.Fatal("streamInfoLoadedMsg.err = nil, want error")
	}
	model = model.applyStreamInfoLoaded(loaded)
	view := model.streamInfoView(model.layout())
	if !strings.Contains(view, "Load failed") || !strings.Contains(view, "twitch says no") {
		t.Fatalf("view missing load error:\n%s", view)
	}
}

func TestStreamInfoEditAndSaveUpdatesOnlyChangedFields(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	channelManager := &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		Title:         "Old title",
		GameName:      "Old Game",
		Language:      "en",
		Tags:          []string{"Chill"},
	}}
	model.channelManager = channelManager
	model.selfBroadcasterID = "123"

	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)
	model.activeTab = tabStreamInfo

	// Edit only the title field; category/language/tags stay untouched.
	model.streamInfo.selected = streamInfoFieldTitle
	updated, _ := model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if !model.streamInfo.editing {
		t.Fatal("editing = false after enter on unedited field, want true")
	}
	model.streamInfo.editBuffer = ""
	for _, r := range "New title" {
		updated, _ = model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(shellModel)
	}
	updated, _ = model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.streamInfo.editing {
		t.Fatal("editing = true after commit, want false")
	}
	if model.streamInfo.title != "New title" {
		t.Fatalf("title = %q, want %q", model.streamInfo.title, "New title")
	}

	updated, cmd := model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(shellModel)
	if cmd == nil {
		t.Fatal("ctrl+s returned nil command, want save command")
	}
	saved := cmd().(streamInfoSavedMsg)
	if saved.err != nil {
		t.Fatalf("streamInfoSavedMsg.err = %v, want nil", saved.err)
	}
	model = model.applyStreamInfoSaved(saved)
	if !model.streamInfo.saveOK {
		t.Fatal("saveOK = false after successful save")
	}
	if !channelManager.modified {
		t.Fatal("ModifyChannelInformation was not called")
	}
	if channelManager.lastUpdate.Title == nil || *channelManager.lastUpdate.Title != "New title" {
		t.Fatalf("update.Title = %v, want \"New title\"", channelManager.lastUpdate.Title)
	}
	if channelManager.lastUpdate.GameID != nil {
		t.Fatalf("update.GameID = %v, want nil (category unchanged)", channelManager.lastUpdate.GameID)
	}
	if channelManager.lastUpdate.Language != nil {
		t.Fatalf("update.Language = %v, want nil (language unchanged)", channelManager.lastUpdate.Language)
	}
}

func TestStreamInfoCategoryChangeSendsPickedGameID(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	channelManager := &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		GameName:      "Old Game",
	}}
	model.channelManager = channelManager
	model.selfBroadcasterID = "123"
	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)

	// The category picker is the only way to change category, and it always
	// sets the name and ID together (see commitCategoryPickerSelection), so
	// save should send the already-known ID without any lookup.
	model.streamInfo.category = "New Game"
	model.streamInfo.categoryGameID = "999"
	cmd := model.scheduleStreamInfoSave()
	saved := cmd().(streamInfoSavedMsg)
	if saved.err != nil {
		t.Fatalf("save error = %v, want nil", saved.err)
	}
	if channelManager.lastUpdate.GameID == nil || *channelManager.lastUpdate.GameID != "999" {
		t.Fatalf("update.GameID = %v, want 999", channelManager.lastUpdate.GameID)
	}
	if saved.info.GameName != "New Game" {
		t.Fatalf("saved.info.GameName = %q, want New Game", saved.info.GameName)
	}
}

func TestCategoryPickerSearchesAndSelectsCategory(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabStreamInfo
	model.channelManager = &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		GameName:      "Old Game",
		GameID:        "1",
	}}
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"Old Game":          {ID: "1", Name: "Old Game"},
		"Fortnite":          {ID: "33214", Name: "Fortnite"},
		"Fortnite Creative": {ID: "509670", Name: "Fortnite Creative"},
		"Just Chatting":     {ID: "509658", Name: "Just Chatting"},
	}}
	model.selfBroadcasterID = "123"
	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)

	model.streamInfo.selected = streamInfoFieldCategory
	updated, cmd := model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if !model.categoryPicker.open {
		t.Fatal("categoryPicker.open = false after enter on category field, want true")
	}
	if cmd == nil {
		t.Fatal("opening category picker returned nil command, want initial search command")
	}
	// The picker seeds its query with the current category, so the initial
	// search (fired without debounce) already reflects it.
	resultsMsg := cmd().(categoryPickerResultsMsg)
	model = model.applyCategoryPickerResults(resultsMsg)
	if len(model.categoryPicker.results) != 1 || model.categoryPicker.results[0].Name != "Old Game" {
		t.Fatalf("initial results = %#v, want [Old Game]", model.categoryPicker.results)
	}

	// Type "fort" to search for Fortnite categories; each keystroke debounces.
	model.categoryPicker.query = ""
	for _, r := range "fort" {
		updated, cmd = model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(shellModel)
		if cmd == nil {
			t.Fatal("typing in category picker returned nil command, want debounce tick")
		}
	}
	debounceMsg := cmd().(categoryPickerDebounceMsg)
	updated, cmd = model.applyCategoryPickerDebounce(debounceMsg)
	model = updated.(shellModel)
	if cmd == nil {
		t.Fatal("debounce tick returned nil command, want search command")
	}
	resultsMsg = cmd().(categoryPickerResultsMsg)
	model = model.applyCategoryPickerResults(resultsMsg)
	if len(model.categoryPicker.results) != 2 {
		t.Fatalf("results = %#v, want 2 Fortnite matches", model.categoryPicker.results)
	}

	// Entry 0 is the pinned "no category" row; move to the first real result
	// (Fortnite, alphabetically before Fortnite Creative) and select it.
	model.moveCategoryPickerSelection(1)
	updated, _ = model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.categoryPicker.open {
		t.Fatal("categoryPicker.open = true after enter, want closed")
	}
	if model.streamInfo.category != "Fortnite" || model.streamInfo.categoryGameID != "33214" {
		t.Fatalf("category = %q id = %q, want Fortnite/33214", model.streamInfo.category, model.streamInfo.categoryGameID)
	}
}

func TestCategoryPickerNoCategoryEntryClearsCategory(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{}}
	model.streamInfo.category = "Old Game"
	model.streamInfo.categoryGameID = "1"
	model.categoryPicker = categoryPickerState{open: true}

	updated, _ := model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.streamInfo.category != "" || model.streamInfo.categoryGameID != "" {
		t.Fatalf("category = %q id = %q, want cleared", model.streamInfo.category, model.streamInfo.categoryGameID)
	}
}

func TestCategoryPickerEscCancelsWithoutChangingCategory(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.streamInfo.category = "Old Game"
	model.streamInfo.categoryGameID = "1"
	model.categoryPicker = categoryPickerState{open: true, query: "fort"}

	updated, _ := model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(shellModel)
	if model.categoryPicker.open {
		t.Fatal("categoryPicker.open = true after esc, want closed")
	}
	if model.streamInfo.category != "Old Game" || model.streamInfo.categoryGameID != "1" {
		t.Fatalf("category = %q id = %q, want unchanged", model.streamInfo.category, model.streamInfo.categoryGameID)
	}
}

func TestCategoryPickerStaleDebounceAndResultsAreDiscarded(t *testing.T) {
	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"Fortnite": {ID: "33214", Name: "Fortnite"},
	}}
	model.categoryPicker = categoryPickerState{open: true}

	updated, cmd1 := model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(shellModel)
	staleDebounce := cmd1().(categoryPickerDebounceMsg)

	updated, _ = model.handleCategoryPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = updated.(shellModel)

	// The first keystroke's debounce tick arrives after the second keystroke
	// already bumped the generation counter; it must be ignored, not trigger
	// a search for the stale "f" query.
	updated, cmd := model.applyCategoryPickerDebounce(staleDebounce)
	model = updated.(shellModel)
	if cmd != nil {
		t.Fatal("stale debounce triggered a search command, want nil")
	}

	staleResults := categoryPickerResultsMsg{generation: staleDebounce.generation, results: []twitch.Game{{ID: "1", Name: "Stale"}}}
	model = model.applyCategoryPickerResults(staleResults)
	if len(model.categoryPicker.results) != 0 {
		t.Fatalf("results = %#v after stale response, want unchanged (empty)", model.categoryPicker.results)
	}
}

func TestStreamInfoMissingScopeErrorIsUserFriendly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized","status":401,"message":"Missing scope: channel:manage:broadcast"}`)
	}))
	defer server.Close()

	cfg := config.Default()
	model := newMockModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabStreamInfo
	model.channelManager = twitch.NewHelixChannelsClient(twitch.HelixChannelsClientConfig{Endpoint: server.URL})
	model.selfBroadcasterID = "123"

	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	if !twitch.IsMissingScope(loaded.err) {
		t.Fatalf("loaded.err = %v, want a missing-scope error", loaded.err)
	}
	model = model.applyStreamInfoLoaded(loaded)
	if !strings.Contains(model.streamInfo.loadErr, "twi login") {
		t.Fatalf("loadErr = %q, want a re-login hint instead of the raw Twitch response", model.streamInfo.loadErr)
	}
	view := model.streamInfoView(model.layout())
	if !strings.Contains(view, "twi login") {
		t.Fatalf("stream info view missing re-login hint:\n%s", view)
	}
}
