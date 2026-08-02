package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func TestComposerMentionPrefixOnlyMatchesWordLeadingAt(t *testing.T) {
	for _, test := range []struct {
		draft  string
		prefix string
		ok     bool
	}{
		{"@ali", "ali", true},
		{"hey @ali", "ali", true},
		{"@", "", true},
		{"hey", "", false},
		{"", "", false},
		// An "@" mid-word is an address or a handle already typed out, not a
		// completion request.
		{"mail@example", "", false},
		// A completed mention followed by more text is no longer being typed.
		{"@alice said", "", false},
	} {
		prefix, ok := composerMentionPrefix(test.draft)
		if ok != test.ok || prefix != test.prefix {
			t.Errorf("composerMentionPrefix(%q) = (%q, %v), want (%q, %v)", test.draft, prefix, ok, test.prefix, test.ok)
		}
	}
}

// mentionTestModel returns a composer-focused model whose roster holds three
// chatters sharing a "stream" prefix, most recently seen first.
func mentionTestModel(t *testing.T) mockShellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 90, 22
	model.focus = mockFocusComposer

	now := time.Now()
	roster := model.activeChannelState().roster
	roster.observeMessage(twitch.ChatMessage{
		AuthorLogin: "streamerguy", DisplayName: "StreamerGuy", Timestamp: now,
		Badges: []twitch.Badge{{SetID: "broadcaster"}},
	})
	roster.observeMessage(twitch.ChatMessage{
		AuthorLogin: "streamfan99", DisplayName: "StreamFan99", Timestamp: now.Add(-time.Minute),
	})
	roster.observeMessage(twitch.ChatMessage{
		AuthorLogin: "unrelated", DisplayName: "Unrelated", Timestamp: now,
	})
	return model
}

func typeInComposer(model mockShellModel, text string) mockShellModel {
	for _, r := range text {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(mockShellModel)
	}
	return model
}

func TestMentionAutocompleteSuggestsAndCompletesWithTab(t *testing.T) {
	model := typeInComposer(mentionTestModel(t), "hey @stream")

	matches := model.mentionSuggestions()
	if len(matches) != 2 {
		t.Fatalf("suggestions = %d, want 2 matching the 'stream' prefix", len(matches))
	}
	if matches[0].Login != "streamerguy" {
		t.Fatalf("first suggestion = %q, want the most recently seen chatter", matches[0].Login)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "StreamerGuy") {
		t.Fatalf("composer view missing the suggestion strip:\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelState().composerText, "hey @StreamerGuy "; got != want {
		t.Fatalf("draft after tab = %q, want %q", got, want)
	}
	if got := model.mentionSuggestions(); len(got) != 0 {
		t.Fatalf("suggestions still open after accepting: %d", len(got))
	}
}

func TestMentionAutocompleteArrowsMoveSelection(t *testing.T) {
	model := typeInComposer(mentionTestModel(t), "@stream")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelState().composerText, "@StreamFan99 "; got != want {
		t.Fatalf("draft after down+tab = %q, want %q", got, want)
	}

	// Selection wraps, so up from the first entry reaches the last.
	model = typeInComposer(mentionTestModel(t), "@stream")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelState().composerText, "@StreamFan99 "; got != want {
		t.Fatalf("draft after up+tab = %q, want %q (selection should wrap)", got, want)
	}
}

func TestMentionAutocompleteEscDismissesOnlyCurrentWord(t *testing.T) {
	model := typeInComposer(mentionTestModel(t), "@stream")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if got := model.mentionSuggestions(); len(got) != 0 {
		t.Fatalf("suggestions = %d after esc, want 0", len(got))
	}

	// Typing more changes the prefix, so completion is offered again rather
	// than staying suppressed for the rest of the draft.
	model = typeInComposer(model, "e")
	if got := model.mentionSuggestions(); len(got) == 0 {
		t.Fatal("suggestions stayed dismissed after the prefix changed")
	}
}

func TestMentionAutocompleteLeavesKeysAloneWhenClosed(t *testing.T) {
	model := mentionTestModel(t)
	model.focus = mockFocusChat

	// With no completion in flight, tab must still cycle focus.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if model.focus != mockFocusComposer {
		t.Fatalf("focus after tab = %v, want composer; tab was swallowed", model.focus)
	}

	// A draft with no @ offers nothing, so tab keeps cycling.
	model = typeInComposer(model, "plain text")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if model.focus != mockFocusChat {
		t.Fatalf("focus after second tab = %v, want chat", model.focus)
	}
}

func TestMentionAutocompleteSkipsExactSingleMatch(t *testing.T) {
	model := typeInComposer(mentionTestModel(t), "@unrelated")
	if got := model.mentionSuggestions(); len(got) != 0 {
		t.Fatalf("suggestions = %d for an already-complete mention, want 0", len(got))
	}
}

func TestMentionAutocompleteEnterStillSends(t *testing.T) {
	model := typeInComposer(mentionTestModel(t), "@stream")
	if len(model.mentionSuggestions()) == 0 {
		t.Fatal("test setup failed: expected open suggestions")
	}

	// Enter must remain "send", not "accept completion" - losing the ability
	// to send while a strip happens to be open would be a serious regression.
	// This model has no chat client, so reaching the send path shows up as a
	// send failure rather than a cleared draft.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	state := model.activeChannelState()
	if state.sendState != composerSendFailed {
		t.Fatalf("send state after enter = %v, want the send path to have run", state.sendState)
	}
	if got := state.composerText; got != "@stream" {
		t.Fatalf("draft after enter = %q, want it untouched by the suggestion strip", got)
	}
}
