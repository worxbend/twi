package app

import (
	"context"
	"errors"
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
}

func (f *appFakeGameLookup) GetGameByName(_ context.Context, name string) (twitch.Game, bool, error) {
	game, ok := f.games[name]
	return game, ok, nil
}

type appFakeUserLookup struct {
	users []twitch.UserIdentity
	err   error
}

func (f *appFakeUserLookup) GetUsers(context.Context, twitch.UserLookupRequest) ([]twitch.UserIdentity, error) {
	return f.users, f.err
}

func TestTabSwitchAltDigitTogglesBetweenChatAndStreamInfo(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.width, model.height = 88, 20

	if model.activeTab != tabChat {
		t.Fatalf("default activeTab = %v, want tabChat", model.activeTab)
	}
	if !strings.Contains(model.View(), "*1:Chat") {
		t.Fatalf("view missing active chat tab marker:\n%s", model.View())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	model = updated.(mockShellModel)
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
	model = updated.(mockShellModel)
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
	model := newMockShellModel("example", cfg)
	model.width, model.height = 88, 20
	model.channelManager = &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		Title:         "Hello world",
		GameName:      "Just Chatting",
		Language:      "en",
		Tags:          []string{"English", "Chill"},
	}}
	model.selfUserLookup = &appFakeUserLookup{users: []twitch.UserIdentity{{UserID: "123", Login: "streamer"}}}

	updated, cmd := model.switchToTab(tabStreamInfo)
	model = updated.(mockShellModel)
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
	model := newMockShellModel("example", cfg)
	model.width, model.height = 88, 20
	model.activeTab = tabStreamInfo
	model.channelManager = &appFakeChannelManager{getErr: errors.New("twitch says no")}
	model.streamInfo.broadcasterID = "123" // skip user lookup

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
	model := newMockShellModel("example", cfg)
	model.width, model.height = 88, 20
	channelManager := &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		Title:         "Old title",
		GameName:      "Old Game",
		Language:      "en",
		Tags:          []string{"Chill"},
	}}
	model.channelManager = channelManager
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"New Game": {ID: "999", Name: "New Game"},
	}}
	model.streamInfo.broadcasterID = "123"

	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)
	model.activeTab = tabStreamInfo

	// Edit only the title field; category/language/tags stay untouched.
	model.streamInfo.selected = streamInfoFieldTitle
	updated, _ := model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if !model.streamInfo.editing {
		t.Fatal("editing = false after enter on unedited field, want true")
	}
	model.streamInfo.editBuffer = ""
	for _, r := range "New title" {
		updated, _ = model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(mockShellModel)
	}
	updated, _ = model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if model.streamInfo.editing {
		t.Fatal("editing = true after commit, want false")
	}
	if model.streamInfo.title != "New title" {
		t.Fatalf("title = %q, want %q", model.streamInfo.title, "New title")
	}

	updated, cmd := model.handleStreamInfoKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(mockShellModel)
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

func TestStreamInfoCategoryChangeResolvesGameID(t *testing.T) {
	cfg := config.Default()
	model := newMockShellModel("example", cfg)
	channelManager := &appFakeChannelManager{info: twitch.ChannelInfo{
		BroadcasterID: "123",
		GameName:      "Old Game",
	}}
	model.channelManager = channelManager
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{
		"New Game": {ID: "999", Name: "New Game"},
	}}
	model.streamInfo.broadcasterID = "123"
	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)

	model.streamInfo.category = "New Game"
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

func TestStreamInfoCategoryChangeUnknownNameFailsSave(t *testing.T) {
	cfg := config.Default()
	model := newMockShellModel("example", cfg)
	model.channelManager = &appFakeChannelManager{info: twitch.ChannelInfo{BroadcasterID: "123"}}
	model.gameLookup = &appFakeGameLookup{games: map[string]twitch.Game{}}
	model.streamInfo.broadcasterID = "123"
	loaded := model.scheduleStreamInfoLoad()().(streamInfoLoadedMsg)
	model = model.applyStreamInfoLoaded(loaded)

	model.streamInfo.category = "Not A Real Category"
	cmd := model.scheduleStreamInfoSave()
	saved := cmd().(streamInfoSavedMsg)
	if saved.err == nil {
		t.Fatal("save error = nil, want error for unknown category")
	}
}
