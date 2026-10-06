package app

import "testing"

func TestQueueComposerSendBareMeKeepsDraftAndExplains(t *testing.T) {
	model := composerTestModel(t)
	model.activeChannelState().composerText = "/me"

	updated, cmd := model.queueComposerSend()

	if cmd != nil {
		t.Fatal("bare /me started a send command; want feedback instead")
	}
	state := updated.activeChannelState()
	if state.composerText != "/me" {
		t.Fatalf("composerText = %q, want the bare /me draft kept", state.composerText)
	}
	if state.sendState != composerSendFailed {
		t.Fatalf("sendState = %q, want %q", state.sendState, composerSendFailed)
	}
	if state.sendFeedback != "usage: /me <action>" {
		t.Fatalf("sendFeedback = %q, want a usage hint", state.sendFeedback)
	}
}

func TestQueueComposerSendMeActionStillQueues(t *testing.T) {
	model := composerTestModel(t)
	model.services.client = NewFakeChatClient(1)
	model.activeChannelState().composerText = "/me waves"

	updated, cmd := model.queueComposerSend()

	if cmd == nil {
		t.Fatal("no send command started for /me waves")
	}
	state := updated.activeChannelState()
	if state.composerText != "" {
		t.Fatalf("composerText = %q, want cleared after queueing", state.composerText)
	}
	if state.activeSend == nil || !state.activeSend.Action || state.activeSend.Text != "waves" {
		t.Fatalf("activeSend = %#v, want action send with text %q", state.activeSend, "waves")
	}
}
