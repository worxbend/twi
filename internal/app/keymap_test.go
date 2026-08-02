package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
)

func keymapModel(t *testing.T) mockShellModel {
	t.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.EnableMouse = true
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockShellModel("alpha", cfg)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 26})
	return updated.(mockShellModel)
}

func press(model mockShellModel, msg tea.KeyMsg) (mockShellModel, tea.Cmd) {
	updated, cmd := model.Update(msg)
	return updated.(mockShellModel), cmd
}

func pressRune(model mockShellModel, r rune) (mockShellModel, tea.Cmd) {
	return press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func TestInsertKeysFocusComposerAndEscReturnsToChat(t *testing.T) {
	for _, key := range []rune{'i', 'o', 'a'} {
		model := keymapModel(t)
		model, _ = pressRune(model, key)
		if model.focus != mockFocusComposer {
			t.Fatalf("focus after %q = %v, want composer", string(key), model.focus)
		}
		if got := model.activeChannelState().composerText; got != "" {
			t.Fatalf("%q leaked into the draft: %q", string(key), got)
		}

		// Once in the composer the same rune is ordinary text again.
		model, _ = pressRune(model, key)
		if got, want := model.activeChannelState().composerText, string(key); got != want {
			t.Fatalf("composer text = %q, want %q", got, want)
		}

		model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
		if model.focus != mockFocusChat {
			t.Fatalf("focus after esc = %v, want chat", model.focus)
		}
		// esc leaves insert mode without discarding the draft.
		if got, want := model.activeChannelState().composerText, string(key); got != want {
			t.Fatalf("draft after esc = %q, want %q", got, want)
		}
	}
}

func TestVimSelectionAndInspectKeys(t *testing.T) {
	model := keymapModel(t)
	state := model.activeChannelState()
	if len(state.messages) < 2 {
		t.Fatalf("test setup: %d seeded messages, want at least 2", len(state.messages))
	}

	// The first k selects the newest message; the second moves up from it.
	model, _ = pressRune(model, 'k')
	if model.activeChannelState().replyTo == nil {
		t.Fatal("replyTo after k = nil, want a selected message")
	}
	newest := *model.activeChannelState().replyTo
	model, _ = pressRune(model, 'k')
	older := model.activeChannelState().replyTo
	if older == nil || *older == newest {
		t.Fatalf("replyTo after a second k = %#v, want an older message than %#v", older, newest)
	}
	model, _ = pressRune(model, 'j')
	if got := model.activeChannelState().replyTo; got == nil || *got != newest {
		t.Fatalf("replyTo after j = %#v, want back to %#v", got, newest)
	}

	model, _ = pressRune(model, 'K')
	if !model.inspectOpen {
		t.Fatal("inspectOpen after K = false, want true")
	}
}

func TestSpaceLeaderTogglesSidebarOpensPickerAndClosesChannel(t *testing.T) {
	model := keymapModel(t)
	if model.layout().sidebarWidth <= 0 {
		t.Fatal("test setup: sidebar not visible with two channels")
	}

	// space is only a leader outside the composer.
	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	if !model.leaderPending {
		t.Fatal("leaderPending after space = false, want true")
	}
	model, _ = pressRune(model, 'e')
	if model.leaderPending {
		t.Fatal("leaderPending after the chord completed = true, want false")
	}
	if model.layout().sidebarWidth != 0 {
		t.Fatal("sidebar still visible after space+e, want hidden")
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	model, _ = pressRune(model, 'e')
	if model.layout().sidebarWidth <= 0 {
		t.Fatal("sidebar hidden after a second space+e, want visible")
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	model, _ = pressRune(model, 'c')
	if !model.channelPicker.open {
		t.Fatal("channelPicker.open after space+c = false, want true")
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})

	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	model, _ = pressRune(model, 'x')
	if got := model.channels.channelNames(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("channels after space+x = %#v, want [beta]", got)
	}
}

func TestSpaceIsLiteralInComposerAndUnboundLeaderKeysCancel(t *testing.T) {
	model := keymapModel(t)
	model.focus = mockFocusComposer
	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	if model.leaderPending {
		t.Fatal("space armed the leader from the composer, want a literal space")
	}
	if got, want := model.activeChannelState().composerText, " "; got != want {
		t.Fatalf("composer text = %q, want %q", got, want)
	}

	model.focus = mockFocusChat
	model, _ = press(model, tea.KeyMsg{Type: tea.KeySpace})
	model, _ = pressRune(model, 'z')
	if model.leaderPending {
		t.Fatal("leaderPending after an unbound chord key = true, want false")
	}
	if model.anyOverlayOpen() {
		t.Fatal("an unbound chord key opened an overlay")
	}
}

func TestSidebarFocusNavigatesSwitchesAndCloses(t *testing.T) {
	model := keymapModel(t)
	model.focus = mockFocusSidebar
	model.syncSidebarSelectionToActive()

	model, _ = pressRune(model, 'j')
	if got, want := model.sidebarSelected, 1; got != want {
		t.Fatalf("selection after j = %d, want %d", got, want)
	}
	model, _ = pressRune(model, 'l')
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel after l = %q, want %q", got, want)
	}

	// The close affordance only appears on the focused, highlighted row.
	if !strings.Contains(model.View(), sidebarCloseAffordance) {
		t.Fatalf("focused sidebar missing the close affordance:\n%s", model.View())
	}

	model, _ = pressRune(model, 'x')
	if got := model.channels.channelNames(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("channels after x = %#v, want [alpha]", got)
	}

	model.focus = mockFocusSidebar
	model, _ = pressRune(model, 'h')
	if model.focus != mockFocusChat {
		t.Fatalf("focus after h = %v, want chat", model.focus)
	}
}

func TestTabCycleIncludesTheSidebarOnlyWhenVisible(t *testing.T) {
	model := keymapModel(t)
	model.focus = mockFocusComposer
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.focus != mockFocusSidebar {
		t.Fatalf("focus after tab from composer = %v, want the visible sidebar", model.focus)
	}

	model.sidebarVisibility = sidebarHidden
	model.focus = mockFocusComposer
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.focus != mockFocusChat {
		t.Fatalf("focus after tab with a hidden sidebar = %v, want chat", model.focus)
	}
}
