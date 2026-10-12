package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/twitch"
)

func TestNativePasteDoesNotInvokeShortcutsOrSend(t *testing.T) {
	for _, content := range []string{"q", "ctrl+c", "enter", "one\ntwo"} {
		model := composerTestModel(t)
		updated, cmd := model.Update(tea.PasteMsg{Content: content})
		got := updated.(shellModel)
		if cmd != nil {
			t.Fatalf("paste %q returned command", content)
		}
		if want := strings.ReplaceAll(content, "\n", " "); got.activeChannelState().composerText != want {
			t.Fatalf("paste %q draft = %q, want %q", content, got.activeChannelState().composerText, want)
		}
	}
	model := keymapModel(t)
	updated, cmd := model.Update(tea.PasteMsg{Content: "q"})
	if cmd != nil || updated.(shellModel).focus != focusChat {
		t.Fatal("normal-mode paste invoked action")
	}
	model.toggleCommandPalette()
	updated, cmd = model.Update(tea.PasteMsg{Content: "enter\nq"})
	got := updated.(shellModel)
	if cmd != nil || !got.palette.open || got.palette.query != "enter q" {
		t.Fatal("palette paste invoked command")
	}
}

func TestNativeAltShortcutWorksWithoutPrintableText(t *testing.T) {
	model := keymapModel(t)
	updated, _ := model.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
	if updated.(shellModel).activeTab != tabStreamInfo {
		t.Fatal("native alt+2 did not switch tab")
	}
	updated, _ = updated.Update(tea.KeyReleaseMsg{Code: '1', Mod: tea.ModAlt})
	if updated.(shellModel).activeTab != tabStreamInfo {
		t.Fatal("key release invoked tab shortcut")
	}
}

func TestNativeMouseReleaseAndDragDoNotChangeFocus(t *testing.T) {
	model := mouseFocusModel(t)
	y := composerRow(model)
	for _, msg := range []tea.Msg{
		tea.MouseReleaseMsg{X: 4, Y: y, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: 4, Y: y, Button: tea.MouseLeft},
	} {
		updated, cmd := model.Update(msg)
		if cmd != nil || updated.(shellModel).focus != focusChat {
			t.Fatal("release or drag invoked click")
		}
	}
	updated, _ := model.Update(tea.MouseClickMsg{X: 4, Y: y, Button: tea.MouseLeft})
	if updated.(shellModel).focus != focusComposer {
		t.Fatal("native click did not focus composer")
	}
}

func TestNativeFocusAndResizePreserveDraft(t *testing.T) {
	model := composerTestModel(t)
	model.activeChannelState().composerText = "視聴者 😀"
	for _, msg := range []tea.Msg{tea.BlurMsg{}, tea.FocusMsg{}, tea.WindowSizeMsg{Width: 32, Height: 12}, tea.WindowSizeMsg{Width: 120, Height: 30}} {
		updated, _ := model.Update(msg)
		model = updated.(shellModel)
		if model.activeChannelState().composerText != "視聴者 😀" {
			t.Fatal("terminal event changed draft")
		}
	}
	if !model.terminalFocused || model.width != 120 || model.height != 30 {
		t.Fatal("terminal state not updated")
	}
}

type testChatRecorder struct{ failures chan error }

func (r *testChatRecorder) Submit(automation.Record) error                 { return nil }
func (r *testChatRecorder) Errors() <-chan error                           { return r.failures }
func (r *testChatRecorder) Ready(context.Context, string, time.Time) error { return nil }
func (r *testChatRecorder) Lost(context.Context, time.Time) error          { return nil }

func TestRecorderFailureRemainsVisibleAtNarrowWidth(t *testing.T) {
	model := keymapModel(t)
	model.recorder = &testChatRecorder{failures: make(chan error, 1)}
	if got := ansi.Strip(model.statusLine(32)); !strings.Contains(got, "HISTORY REC") {
		t.Fatalf("capture badge hidden: %q", got)
	}
	updated, cmd := model.Update(recorderErrorMsg{err: errors.New("disk full")})
	model = updated.(shellModel)
	if cmd != nil || !strings.Contains(ansi.Strip(model.statusLine(32)), "HISTORY ERROR disk full") {
		t.Fatal("capture failure hidden")
	}
	if model.nextRecorderErrorCommand() != nil {
		t.Fatal("failed recorder kept polling")
	}
}

type readOnlyChatSource struct {
	messages chan twitch.ChatMessage
	states   chan ConnectionState
}

func (r *readOnlyChatSource) Messages() <-chan twitch.ChatMessage      { return r.messages }
func (r *readOnlyChatSource) ConnectionStates() <-chan ConnectionState { return r.states }
func (r *readOnlyChatSource) Close() error                             { return nil }

func TestReadOnlySourceCannotQueueSendAndKeepsDraft(t *testing.T) {
	model := keymapModel(t)
	model.services.client = &readOnlyChatSource{}
	model.focus = focusComposer
	model.activeChannelState().composerText = "draft stays"
	updated, cmd := model.queueComposerSend()
	state := updated.activeChannelState()
	if cmd != nil || state.composerText != "draft stays" || len(state.sendQueue) != 0 || state.activeSend != nil {
		t.Fatal("read-only source queued send or lost draft")
	}
	if state.sendFeedback != "send unavailable for this chat source" {
		t.Fatalf("feedback = %q", state.sendFeedback)
	}
	if !strings.Contains(model.composerPlaceholder(""), "Read-only history") {
		t.Fatal("read-only source not labeled")
	}
}

func TestClosedStreamsPreserveActionableFailure(t *testing.T) {
	model := keymapModel(t)
	failed := ConnectionState{Status: ConnectionFailed, Channel: model.activeChannelName(), Detail: "local history read failed", Err: errors.New("corrupt database")}
	updated, _ := model.Update(chatClientConnectionStateMsg{state: failed, ok: true})
	model = updated.(shellModel)
	for _, closed := range []tea.Msg{chatClientMessageMsg{ok: false}, chatClientConnectionStateMsg{ok: false}} {
		updated, _ = model.Update(closed)
		model = updated.(shellModel)
		if got := model.activeChannelState().status; got.Status != ConnectionFailed || got.Detail != failed.Detail || got.Err != failed.Err {
			t.Fatalf("failure overwritten: %#v", got)
		}
	}
}
