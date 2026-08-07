package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// joiningChatClient is a FakeChatClient that also implements ChannelJoiner,
// standing in for the live IRC transport so tests can assert that opening
// and closing a channel reaches the wire.
type joiningChatClient struct {
	*FakeChatClient
	joined   []string
	departed []string
}

func newJoiningChatClient() *joiningChatClient {
	return &joiningChatClient{FakeChatClient: NewFakeChatClient(1)}
}

func (c *joiningChatClient) JoinChannel(channel string) error {
	c.joined = append(c.joined, channel)
	return nil
}

func (c *joiningChatClient) PartChannel(channel string) error {
	c.departed = append(c.departed, channel)
	return nil
}

func emptyChannelModel(t *testing.T, client ChatClient) shellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = nil
	model := newLiveModelWithClockAndOptions("", cfg, client, nil, ClientOptions{})
	model.width, model.height = 96, 24
	return model
}

func TestEmptyChannelSetRendersEmptyStateAndRefusesSends(t *testing.T) {
	model := emptyChannelModel(t, NewFakeChatClient(1))
	if !model.channels.empty() {
		t.Fatal("channels.empty() = false for a channel-less start, want true")
	}

	view := model.View()
	for _, want := range []string{"No channels open.", "/channels", "no channel open"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty-state view missing %q:\n%s", want, view)
		}
	}

	// Sending with nothing open must fail locally rather than reaching a
	// transport with an empty channel name.
	model.activeChannelState().composerText = "hello"
	model.focus = focusComposer
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if cmd != nil {
		t.Fatalf("send with no channel returned command %#v, want nil", cmd)
	}
	if model.activeChannelState().sendState != composerSendFailed {
		t.Fatalf("sendState = %q, want failed", model.activeChannelState().sendState)
	}
}

func TestOpenChannelJoinsTransportAndClosingParts(t *testing.T) {
	client := newJoiningChatClient()
	model := emptyChannelModel(t, client)

	updated, _ := model.openChannel("#Alpha")
	model = updated.(shellModel)
	if got, want := model.activeChannelName(), "Alpha"; got != want {
		t.Fatalf("active channel after open = %q, want %q", got, want)
	}
	if got := client.joined; len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("joined = %#v, want [Alpha]", got)
	}

	// Reopening an open channel switches to it without a second join.
	updated, _ = model.openChannel("beta")
	model = updated.(shellModel)
	updated, _ = model.openChannel("alpha")
	model = updated.(shellModel)
	if got := len(client.joined); got != 2 {
		t.Fatalf("join count after reopening = %d, want 2", got)
	}
	if got, want := model.activeChannelName(), "Alpha"; got != want {
		t.Fatalf("active channel after reopen = %q, want %q", got, want)
	}

	updated, _ = model.closeChannel("alpha")
	model = updated.(shellModel)
	if got := client.departed; len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("departed = %#v, want [alpha]", got)
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel after close = %q, want %q", got, want)
	}

	// Closing the last channel lands back on the empty state instead of
	// leaving a phantom channel behind.
	updated, _ = model.closeChannel("beta")
	model = updated.(shellModel)
	if !model.channels.empty() {
		t.Fatalf("channels after closing the last one = %#v, want empty", model.channels.channelNames())
	}
}

func TestComposerChannelsCommandOpensPickerOrNamedChannel(t *testing.T) {
	model := emptyChannelModel(t, NewFakeChatClient(1))
	model.focus = focusComposer

	model.activeChannelState().composerText = "/channels"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if !model.channelPicker.open {
		t.Fatal("channelPicker.open after /channels = false, want true")
	}
	if got := model.activeChannelState().composerText; got != "" {
		t.Fatalf("composer text after /channels = %q, want empty", got)
	}

	model.channelPicker = channelPickerState{}
	model.activeChannelState().composerText = "/channels #alpha"
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.channelPicker.open {
		t.Fatal("channelPicker.open after /channels <name> = true, want false")
	}
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel after /channels alpha = %q, want %q", got, want)
	}
}

func TestChannelPickerEntriesListOpenFollowedAndTypedNames(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha"}
	model := newMockModel("alpha", cfg)
	model.followedChannelList = []twitch.FollowedChannel{
		{BroadcasterLogin: "gamma", BroadcasterName: "GammaTV"},
		{BroadcasterLogin: "alpha", BroadcasterName: "Alpha"},
	}

	entries := model.channelPickerEntries()
	if len(entries) < 2 || entries[0].login != "alpha" || !entries[0].open {
		t.Fatalf("entries = %#v, want the open channel first", entries)
	}
	// An already-open channel must not also appear as a follow suggestion.
	seen := 0
	for _, entry := range entries {
		if channelKey(entry.login) == "alpha" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("alpha appeared %d times, want 1", seen)
	}

	// A partial query keeps the real match first so enter opens #gamma, with
	// the typed name offered after it.
	model.channelPicker.query = "gam"
	entries = model.channelPickerEntries()
	if len(entries) != 2 || entries[0].login != "gamma" || !entries[0].followed {
		t.Fatalf("filtered entries = %#v, want followed gamma first", entries)
	}
	if !entries[1].literal || entries[1].login != "gam" {
		t.Fatalf("second entry = %#v, want the literal typed name", entries[1])
	}

	// A name matching nothing is still offered verbatim, so unfollowed
	// channels are reachable.
	model.channelPicker.query = "somebodyelse"
	entries = model.channelPickerEntries()
	if len(entries) != 1 || entries[0].login != "somebodyelse" || !entries[0].literal {
		t.Fatalf("entries for an unknown name = %#v, want a literal entry", entries)
	}
}

func TestChannelPickerEnterOpensSelection(t *testing.T) {
	client := newJoiningChatClient()
	model := emptyChannelModel(t, client)
	model.followedChannelList = []twitch.FollowedChannel{{BroadcasterLogin: "gamma"}}
	model.channelPicker = channelPickerState{open: true}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(shellModel)
	if model.channelPicker.open {
		t.Fatal("channelPicker.open after enter = true, want false")
	}
	if got, want := model.activeChannelName(), "gamma"; got != want {
		t.Fatalf("active channel after picker enter = %q, want %q", got, want)
	}
	if got := client.joined; len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("joined = %#v, want [gamma]", got)
	}
}

func TestFollowedChannelsMissingScopeReportsRecoveryHint(t *testing.T) {
	model := emptyChannelModel(t, NewFakeChatClient(1))
	model.channelPicker = channelPickerState{open: true, loading: true}

	model.applyFollowedChannels(followedChannelsResolvedMsg{
		err: &twitch.ChannelAPIError{StatusCode: 401},
	})
	if model.channelPicker.loading {
		t.Fatal("loading = true after a failed lookup, want false")
	}
	if !strings.Contains(model.channelPicker.err, "user:read:follows") {
		t.Fatalf("err = %q, want it to name the missing scope", model.channelPicker.err)
	}
}
