package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/w0rxbend/twi/internal/animation"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/twitch"
)

type appFakeClock struct {
	now time.Time
}

func (c *appFakeClock) Now() time.Time {
	return c.now
}

func (c *appFakeClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestRunMockRendersInitialShellForNonInteractiveOutput(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"example"}

	var out bytes.Buffer
	if err := RunMock(&out, cfg); err != nil {
		t.Fatalf("RunMock returned error: %v", err)
	}

	view := out.String()
	for _, want := range []string{
		"#example",
		"connected",
		"Mock chat is ready in the Bubble Tea shell.",
		"[TB]",
		"Message #example",
		"q quit",
		"ctrl+c quit",
		"no network",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("initial view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Run without --mock") {
		t.Fatalf("initial view still contains old static snapshot text:\n%s", view)
	}
}

func TestMockShellQuitsOnQAndCtrlC(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	for name, msg := range map[string]tea.KeyMsg{
		"q":      {Type: tea.KeyRunes, Runes: []rune{'q'}},
		"ctrl+c": {Type: tea.KeyCtrlC},
	} {
		t.Run(name, func(t *testing.T) {
			_, cmd := model.Update(msg)
			if cmd == nil {
				t.Fatal("Update returned nil command, want tea.Quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("Update command produced %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

func TestMockShellWindowSizeKeepsViewWithinHeight(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 64, Height: 12})
	view := updated.View()

	if got, want := lineCount(view), 12; got != want {
		t.Fatalf("view line count = %d, want %d:\n%s", got, want, view)
	}
}

func TestMockShellFocusHelpAndComposerInput(t *testing.T) {
	model := newMockShellModel("example", config.Default())

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus after tab = %v, want %v", got, want)
	}
	if !strings.Contains(model.View(), "focus=composer") {
		t.Fatalf("composer focus marker missing:\n%s", model.View())
	}

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		updated, cmd := model.Update(msg)
		if cmd != nil {
			t.Fatalf("composer input returned command for %#v", msg)
		}
		model = updated.(mockShellModel)
	}
	if got, want := model.composerText, "hi q"; got != want {
		t.Fatalf("composer text = %q, want %q", got, want)
	}
	if !strings.Contains(model.View(), "hi q") {
		t.Fatalf("composer view missing typed text:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(mockShellModel)
	if !model.helpExpanded {
		t.Fatal("helpExpanded = false, want true")
	}
	if !strings.Contains(model.View(), "pgup/pgdn") {
		t.Fatalf("expanded help missing page key hint:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	if got, want := model.focus, mockFocusChat; got != want {
		t.Fatalf("focus after second tab = %v, want %v", got, want)
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q from chat focus returned nil command, want tea.Quit")
	}
}

func TestMockShellPageKeysScrollViewport(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.messages = numberedMockMessages("example", 12)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	bottom := model.View()
	if !strings.Contains(bottom, "message-11") {
		t.Fatalf("bottom viewport missing latest message:\n%s", bottom)
	}
	if strings.Contains(bottom, "message-00") {
		t.Fatalf("bottom viewport unexpectedly contains oldest message:\n%s", bottom)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	scrolled := model.View()
	if !strings.Contains(scrolled, "message-04") {
		t.Fatalf("page up viewport missing previous page message:\n%s", scrolled)
	}
	if model.scrollOffset == 0 {
		t.Fatal("scrollOffset = 0 after page up, want non-zero")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if !strings.Contains(model.View(), "message-00") {
		t.Fatalf("second page up viewport missing oldest message:\n%s", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(mockShellModel)
	if model.scrollOffset == 0 {
		t.Fatal("scrollOffset after one page down = 0, want still scrolled")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(mockShellModel)
	if model.scrollOffset != 0 {
		t.Fatalf("scrollOffset after second page down = %d, want 0", model.scrollOffset)
	}
	if !strings.Contains(model.View(), "message-11") {
		t.Fatalf("second page down viewport missing latest message:\n%s", model.View())
	}
}

func TestLiveShellEnterQueuesComposerSendAndSuccessKeepsComposerCleared(t *testing.T) {
	client := NewFakeChatClient(1)
	acceptedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := client.QueueSendResult(SendResult{AcceptedAt: acceptedAt, Detail: "accepted by Twitch"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = " hello chat "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("Enter returned nil command, want send command")
	}
	if got := model.composerText; got != "" {
		t.Fatalf("composerText after queue = %q, want cleared", got)
	}
	if model.activeSend == nil || model.activeSend.Channel != "example" || model.activeSend.Text != "hello chat" {
		t.Fatalf("activeSend = %#v, want queued trimmed send for #example", model.activeSend)
	}
	if got, want := model.sendState, composerSendSending; got != want {
		t.Fatalf("sendState after queue = %q, want %q", got, want)
	}

	sendMsg := cmd()
	completed, ok := sendMsg.(composerSendCompletedMsg)
	if !ok {
		t.Fatalf("send command returned %T, want composerSendCompletedMsg", sendMsg)
	}
	updated, cmd = model.Update(completed)
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("successful single send returned extra command %#v", cmd)
	}
	if got := model.composerText; got != "" {
		t.Fatalf("composerText after success = %q, want cleared", got)
	}
	if got, want := model.sendState, composerSendSucceeded; got != want {
		t.Fatalf("sendState after success = %q, want %q", got, want)
	}
	sent := client.SentRequests()
	if len(sent) != 1 || sent[0].Channel != "example" || sent[0].Text != "hello chat" {
		t.Fatalf("SentRequests = %#v, want one trimmed send to active channel", sent)
	}
	if !strings.Contains(model.View(), "accepted by Twitch") {
		t.Fatalf("view missing success detail:\n%s", model.View())
	}
}

func TestLiveShellEnterIgnoresEmptyComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = "   "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("empty composer returned command %#v, want nil", cmd)
	}
	if model.activeSend != nil || len(model.sendQueue) != 0 {
		t.Fatalf("empty composer queued send: active=%#v queue=%#v", model.activeSend, model.sendQueue)
	}
	if got := client.SentRequests(); len(got) != 0 {
		t.Fatalf("SentRequests length = %d, want 0", len(got))
	}
}

func TestLiveShellFailedSendShowsReasonAndRestoresComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = "please send"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.composerText, "please send"; got != want {
		t.Fatalf("composerText after failed send = %q, want %q", got, want)
	}
	if got, want := model.sendState, composerSendFailed; got != want {
		t.Fatalf("sendState after failure = %q, want %q", got, want)
	}
	for _, want := range []string{"failed", "network unavailable"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after failed send:\n%s", want, model.View())
		}
	}
}

func TestLiveShellSendFailureUsesSendScopeGuidance(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("missing scope for oauth:secret-token")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = "please send"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if strings.Contains(model.sendFeedback, "oauth:secret-token") {
		t.Fatalf("sendFeedback leaked token: %q", model.sendFeedback)
	}
	for _, want := range []string{"chat:edit", "send failed"} {
		if !strings.Contains(model.sendFeedback, want) {
			t.Fatalf("sendFeedback = %q, want %q", model.sendFeedback, want)
		}
	}
	if strings.Contains(model.sendFeedback, "chat:read") {
		t.Fatalf("sendFeedback points at read scope instead of edit scope: %q", model.sendFeedback)
	}
}

func TestLiveShellFailedSendRestoresQueuedFollowupText(t *testing.T) {
	client := NewFakeChatClient(2)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = "first message"

	updated, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if firstCmd == nil {
		t.Fatal("first Enter returned nil command, want send command")
	}

	model.composerText = "second message"
	updated, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if secondCmd != nil {
		t.Fatalf("queued follow-up returned command %#v while first send active, want nil", secondCmd)
	}
	if got := model.composerText; got != "" {
		t.Fatalf("composerText after queued follow-up = %q, want cleared", got)
	}
	if got := len(model.sendQueue); got != 1 {
		t.Fatalf("sendQueue length = %d, want 1 queued follow-up", got)
	}

	completed := firstCmd().(composerSendCompletedMsg)
	updated, cmd := model.Update(completed)
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("failed send returned next queued command %#v, want queue stopped", cmd)
	}
	if got := len(model.sendQueue); got != 0 {
		t.Fatalf("sendQueue length after failure = %d, want drained into composer", got)
	}
	for _, want := range []string{"first message", "second message"} {
		if !strings.Contains(model.composerText, want) {
			t.Fatalf("composerText after failed queued send = %q, want it to contain %q", model.composerText, want)
		}
	}
	if got := len(client.SentRequests()); got != 1 {
		t.Fatalf("SentRequests length = %d, want only first send attempted", got)
	}
	for _, want := range []string{"failed", "network unavailable"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after queued failure:\n%s", want, model.View())
		}
	}
}

func TestLiveShellRateLimitedSendShowsReasonAndRestoresComposer(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{
		RateLimited: true,
		RetryAfter:  30 * time.Second,
		Detail:      "sending messages too quickly",
	}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = "slow down?"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.composerText, "slow down?"; got != want {
		t.Fatalf("composerText after rate limit = %q, want %q", got, want)
	}
	if got, want := model.sendState, composerSendRateLimited; got != want {
		t.Fatalf("sendState after rate limit = %q, want %q", got, want)
	}
	for _, want := range []string{"rate limited", "sending messages too quickly"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("view missing %q after rate limit:\n%s", want, model.View())
		}
	}
}

func TestLiveShellSelectsReplyContextAndEscCancelsWithoutLosingDraft(t *testing.T) {
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.messages = []twitch.ChatMessage{
		{
			ID:          "",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
			DisplayName: "system",
			Text:        "no ID",
			Type:        twitch.MessageTypeNotice,
		},
		{
			ID:          "parent-1",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
			DisplayName: "viewer",
			Text:        "question for chat",
			Type:        twitch.MessageTypeChat,
		},
	}
	model.composerText = "draft reply"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("select reply returned command %#v, want nil", cmd)
	}
	if model.replyTo == nil || model.replyTo.MessageID != "parent-1" {
		t.Fatalf("replyTo = %#v, want parent-1 context", model.replyTo)
	}
	for _, want := range []string{"Replying to viewer", "question for chat"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("reply context view missing %q:\n%s", want, model.View())
		}
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("esc returned command %#v, want nil", cmd)
	}
	if model.replyTo != nil {
		t.Fatalf("replyTo after esc = %#v, want nil", model.replyTo)
	}
	if got, want := model.composerText, "draft reply"; got != want {
		t.Fatalf("composerText after esc = %q, want %q", got, want)
	}
	if strings.Contains(model.View(), "Replying to viewer") {
		t.Fatalf("reply context still visible after esc:\n%s", model.View())
	}
}

func TestLiveShellRStartsReplyModeAndReplySendUsesParentID(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.messages = []twitch.ChatMessage{
		{
			ID:          "parent-1",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
			DisplayName: "older",
			Text:        "older message",
			Type:        twitch.MessageTypeChat,
		},
		{
			ID:          "parent-2",
			Channel:     "example",
			Timestamp:   time.Date(2026, 7, 2, 12, 0, 2, 0, time.UTC),
			DisplayName: "latest",
			Text:        "latest message",
			Type:        twitch.MessageTypeChat,
		},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("r returned command %#v, want nil", cmd)
	}
	if model.replyTo == nil || model.replyTo.MessageID != "parent-2" || model.focus != mockFocusComposer {
		t.Fatalf("reply mode = replyTo %#v focus %v, want latest parent and composer focus", model.replyTo, model.focus)
	}

	model.composerText = " thanks "
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("reply Enter returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	sent := client.SentRequests()
	if len(sent) != 1 {
		t.Fatalf("SentRequests length = %d, want 1", len(sent))
	}
	if got, want := sent[0].ReplyToMessageID, "parent-2"; got != want {
		t.Fatalf("ReplyToMessageID = %q, want %q", got, want)
	}
	if got, want := sent[0].Text, "thanks"; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
	if sent[0].Action {
		t.Fatalf("Action = true, want false for normal reply")
	}
	if model.replyTo != nil {
		t.Fatalf("replyTo after successful reply send = %#v, want nil", model.replyTo)
	}
}

func TestLiveShellMeInputQueuesActionSend(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.composerText = " /me waves at chat "

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("/me Enter returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	sent := client.SentRequests()
	if len(sent) != 1 {
		t.Fatalf("SentRequests length = %d, want 1", len(sent))
	}
	if got, want := sent[0].Text, "waves at chat"; got != want {
		t.Fatalf("Text = %q, want stripped action body %q", got, want)
	}
	if !sent[0].Action {
		t.Fatal("Action = false, want true")
	}
}

func TestLiveShellFailedReplyRestoresReplyContext(t *testing.T) {
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.replyTo = &composerReplyContext{MessageID: "parent-1", Author: "viewer", Text: "original"}
	model.composerText = "reply body"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	if got, want := model.composerText, "reply body"; got != want {
		t.Fatalf("composerText after failed reply = %q, want %q", got, want)
	}
	if model.replyTo == nil || model.replyTo.MessageID != "parent-1" {
		t.Fatalf("replyTo after failed reply = %#v, want parent-1", model.replyTo)
	}
}

func TestLiveShellFailedMixedQueueDoesNotMisapplyReplyContext(t *testing.T) {
	client := NewFakeChatClient(2)
	if err := client.QueueSendResult(SendResult{}, fmt.Errorf("network unavailable")); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, nil)
	model.focus = mockFocusComposer
	model.replyTo = &composerReplyContext{MessageID: "parent-1", Author: "viewer", Text: "original"}
	model.composerText = "reply body"

	updated, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if firstCmd == nil {
		t.Fatal("first reply Enter returned nil command, want send command")
	}

	model.composerText = "plain followup"
	updated, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if secondCmd != nil {
		t.Fatalf("queued follow-up returned command %#v while first send active, want nil", secondCmd)
	}

	completed := firstCmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)

	for _, want := range []string{"reply body", "plain followup"} {
		if !strings.Contains(model.composerText, want) {
			t.Fatalf("composerText after failed mixed queue = %q, want it to contain %q", model.composerText, want)
		}
	}
	if model.replyTo != nil {
		t.Fatalf("replyTo after failed mixed queue = %#v, want nil to avoid wrong parent", model.replyTo)
	}
}

func TestMockShellFastModeRevealsIncomingMessage(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	message := mockIncomingMessage("example", "animated-fast", "animated text arrives")

	updated, cmd := model.Update(mockIncomingMessageMsg{message: message})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("incoming fast message returned nil command, want reveal tick")
	}
	if got, want := model.revealQueue.Len(), 1; got != want {
		t.Fatalf("reveal queue len = %d, want %d", got, want)
	}
	if strings.Contains(model.View(), "animated text arrives") {
		t.Fatalf("incoming message rendered fully before ticks:\n%s", model.View())
	}

	initial := model.View()
	clock.Add(mockRevealDelay)
	updated, _ = model.Update(mockAnimationTickMsg{})
	model = updated.(mockShellModel)
	if got := model.View(); got == initial {
		t.Fatalf("first animation tick did not change view:\n%s", got)
	}

	driveRevealToCompletion(t, &model, clock)
	if got := model.revealQueue.Len(); got != 0 {
		t.Fatalf("reveal queue len after completion = %d, want 0", got)
	}
	if !strings.Contains(model.View(), "animated text arrives") {
		t.Fatalf("completed reveal missing full message:\n%s", model.View())
	}
}

func TestMockShellOffModeRendersIncomingMessageWithoutRevealTick(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	message := mockIncomingMessage("example", "animated-off", "off mode is immediate")

	updated, cmd := model.Update(mockIncomingMessageMsg{message: message})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("off mode incoming message returned command %#v, want nil reveal tick", cmd)
	}
	if got := model.revealQueue.Len(); got != 0 {
		t.Fatalf("off mode reveal queue len = %d, want 0", got)
	}
	if !strings.Contains(model.View(), "off mode is immediate") {
		t.Fatalf("off mode view missing full message:\n%s", model.View())
	}
}

func TestMockShellReducedModeUsesFewerChangedFramesThanFastMode(t *testing.T) {
	text := "same animated message takes fewer visible frames in reduced mode"
	fastFrames := changedRevealFrames(t, "fast", text)
	reducedFrames := changedRevealFrames(t, "reduced", text)

	if reducedFrames >= fastFrames {
		t.Fatalf("reduced changed frames = %d, want fewer than fast frames %d", reducedFrames, fastFrames)
	}
}

func TestMockShellInputAndScrollRemainResponsiveDuringAnimation(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.messages = numberedMockMessages("example", 12)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)

	updated, _ = model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "active-reveal", "animation keeps running"),
	})
	model = updated.(mockShellModel)
	if got := model.revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len = %d, want 1", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.scrollOffset == 0 {
		t.Fatal("page up during animation left scrollOffset at 0")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o', 'k'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("composer input during animation returned command %#v, want nil", cmd)
	}
	if got, want := model.composerText, "ok"; got != want {
		t.Fatalf("composer text during animation = %q, want %q", got, want)
	}
	if got := model.revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len after input/scroll = %d, want 1", got)
	}
}

func TestLiveShellBurstKeepsRevealQueueBoundedAndControlsResponsive(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	client := NewFakeChatClient(1)
	if err := client.QueueSendResult(SendResult{AcceptedAt: clock.Now(), Detail: "accepted during burst"}, nil); err != nil {
		t.Fatalf("QueueSendResult returned error: %v", err)
	}
	model := newLiveShellModelWithClock("example", config.Default(), client, clock)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	for i := 0; i < 40; i++ {
		updated, _ = model.Update(chatClientMessageMsg{
			message: mockIncomingMessage("example", fmt.Sprintf("burst-%02d", i), fmt.Sprintf("burst message %02d", i)),
			ok:      true,
		})
		model = updated.(mockShellModel)
		if model.revealQueue.Len() > animation.DefaultConfig().MaxQueued {
			t.Fatalf("after burst message %02d reveal queue len = %d, want <= %d", i, model.revealQueue.Len(), animation.DefaultConfig().MaxQueued)
		}
	}

	if got, want := model.revealQueue.Len(), animation.DefaultConfig().MaxQueued; got != want {
		t.Fatalf("reveal queue len after burst = %d, want %d", got, want)
	}
	if got, want := len(model.messages), 40-animation.DefaultConfig().MaxQueued; got != want {
		t.Fatalf("static overflow messages = %d, want %d", got, want)
	}
	for _, want := range []string{"burst message 00", "burst message 07"} {
		if !messagesContainText(model.messages, want) {
			t.Fatalf("overflowed static messages missing %q: %#v", want, model.messages)
		}
	}
	if messagesContainText(model.messages, "burst message 08") {
		t.Fatalf("non-overflowed burst message rendered statically too early: %#v", model.messages)
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 36, Height: 9})
	model = updated.(mockShellModel)
	view := model.View()
	if got, want := lineCount(view), 9; got != want {
		t.Fatalf("burst resized view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipglossWidth(line); got > 36 {
			t.Fatalf("burst resized line %d width = %d, want <= 36:\n%s", i+1, got, view)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.scrollOffset == 0 {
		t.Fatal("page up during burst left scrollOffset at 0")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(mockShellModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("still responsive")})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("composer input during burst returned command %#v, want nil", cmd)
	}
	if got, want := model.composerText, "still responsive"; got != want {
		t.Fatalf("composer text during burst = %q, want %q", got, want)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("Enter during burst returned nil command, want send command")
	}
	completed := cmd().(composerSendCompletedMsg)
	updated, _ = model.Update(completed)
	model = updated.(mockShellModel)
	if got, want := model.sendState, composerSendSucceeded; got != want {
		t.Fatalf("sendState after burst send = %q, want %q", got, want)
	}
	if !strings.Contains(model.sendFeedback, "accepted during burst") {
		t.Fatalf("sendFeedback after burst = %q, want accepted detail", model.sendFeedback)
	}
}

func TestMockShellScrolledBurstRendersStaticallyWithoutRevealBacklog(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.messages = numberedMockMessages("example", 30)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.scrollOffset == 0 {
		t.Fatal("test setup failed: scrollOffset = 0 after page up")
	}
	beforeView := model.View()
	beforeOffset := model.scrollOffset

	for i := 0; i < 12; i++ {
		updated, cmd := model.Update(mockIncomingMessageMsg{
			message: mockIncomingMessage("example", fmt.Sprintf("offscreen-%02d", i), fmt.Sprintf("offscreen burst %02d", i)),
		})
		model = updated.(mockShellModel)
		if cmd != nil {
			t.Fatalf("off-screen burst message %02d returned command %#v, want no reveal tick", i, cmd)
		}
	}

	if got := model.revealQueue.Len(); got != 0 {
		t.Fatalf("off-screen burst reveal queue len = %d, want 0", got)
	}
	if got := len(model.activeOrder); got != 0 {
		t.Fatalf("off-screen active reveal count = %d, want 0", got)
	}
	if got, want := len(model.messages), 42; got != want {
		t.Fatalf("messages after off-screen burst = %d, want %d", got, want)
	}
	if model.scrollOffset <= beforeOffset {
		t.Fatalf("scrollOffset after off-screen burst = %d, want > %d to preserve visible page", model.scrollOffset, beforeOffset)
	}
	afterView := model.View()
	if afterView != beforeView {
		t.Fatalf("off-screen static burst changed visible scrolled page:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
	if strings.Contains(afterView, "offscreen burst") {
		t.Fatalf("off-screen burst appeared in current scrolled viewport:\n%s", afterView)
	}
}

func TestMockShellCompletingActiveRevealPreservesScrolledViewport(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	model.messages = numberedMockMessages("example", 30)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "active-while-scrolled", "active reveal finishes while scrolled"),
	})
	model = updated.(mockShellModel)
	if got := model.revealQueue.Len(); got != 1 {
		t.Fatalf("reveal queue len = %d, want 1", got)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	if model.scrollOffset == 0 {
		t.Fatal("test setup failed: scrollOffset = 0 after page up")
	}
	beforeView := model.View()
	beforeOffset := model.scrollOffset

	driveRevealToCompletion(t, &model, clock)

	if got := model.revealQueue.Len(); got != 0 {
		t.Fatalf("reveal queue len after completion = %d, want 0", got)
	}
	if !messagesContainText(model.messages, "active reveal finishes while scrolled") {
		t.Fatalf("completed reveal missing from static messages: %#v", model.messages)
	}
	if got := model.scrollOffset; got != beforeOffset {
		t.Fatalf("scrollOffset after active completion = %d, want %d", got, beforeOffset)
	}
	if afterView := model.View(); afterView != beforeView {
		t.Fatalf("active reveal completion changed visible scrolled page:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
}

func TestMockShellDuplicateIncomingMessageIDsCompleteIndependently(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)

	for _, text := range []string{"first duplicate", "second duplicate"} {
		updated, _ := model.Update(mockIncomingMessageMsg{
			message: mockIncomingMessage("example", "duplicate-id", text),
		})
		model = updated.(mockShellModel)
	}
	if got := model.revealQueue.Len(); got != 2 {
		t.Fatalf("reveal queue len = %d, want 2", got)
	}

	driveRevealToCompletion(t, &model, clock)
	view := model.View()
	for _, want := range []string{"first duplicate", "second duplicate"} {
		if !strings.Contains(view, want) {
			t.Fatalf("completed duplicate-ID reveal missing %q:\n%s", want, view)
		}
	}
}

func TestMockShellResizeDuringAnimationStaysWithinBounds(t *testing.T) {
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", config.Default(), clock)
	updated, _ := model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "resize-active", "active reveal survives a narrow resize without overflowing"),
	})
	model = updated.(mockShellModel)

	clock.Add(mockRevealDelay)
	updated, _ = model.Update(mockAnimationTickMsg{})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	view := updated.View()

	if got, want := lineCount(view), 8; got != want {
		t.Fatalf("resized active view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipglossWidth(line); got > 24 {
			t.Fatalf("line %d width = %d, want <= 24:\n%s", i+1, got, view)
		}
	}
}

func TestMockShellNarrowLayoutStaysWithinBounds(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.composerText = "hello 😀 表"
	model.messages = append(model.messages, twitch.ChatMessage{
		ID:          "wide",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "wide",
		DisplayName: "wide",
		Text:        "emoji 😀 and CJK 表 stay inside",
		Type:        twitch.MessageTypeChat,
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	view := updated.View()

	if got, want := lineCount(view), 8; got != want {
		t.Fatalf("narrow view line count = %d, want %d:\n%s", got, want, view)
	}
	for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got, want := lipglossWidth(line), 24; got > want {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, want, view)
		}
	}
	for _, notWant := range []string{"animation=", "images=", "mock source ready"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("narrow view contains nonessential status text %q:\n%s", notWant, view)
		}
	}
	for _, want := range []string{"#example connected", "hello"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
}

func TestMockShellTinyWidthsDoNotExceedWindowWidth(t *testing.T) {
	for width := 1; width <= 5; width++ {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			model := newMockShellModel("example", config.Default())
			model.composerText = "😀表"

			updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 8})
			view := updated.View()

			if got, want := lineCount(view), 8; got != want {
				t.Fatalf("tiny view line count = %d, want %d:\n%s", got, want, view)
			}
			for i, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
				if got := lipglossWidth(line); got > width {
					t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, width, view)
				}
			}
		})
	}
}

func mockIncomingMessage(channel, id, text string) twitch.ChatMessage {
	return twitch.ChatMessage{
		ID:          id,
		Channel:     channel,
		Timestamp:   time.Date(2026, 7, 2, 20, 0, 10, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Text:        text,
		Type:        twitch.MessageTypeChat,
	}
}

func changedRevealFrames(t *testing.T, mode, text string) int {
	t.Helper()

	cfg := config.Default()
	cfg.Features.AnimationMode = mode
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)
	updated, _ := model.Update(mockIncomingMessageMsg{
		message: mockIncomingMessage("example", "animated-"+mode, text),
	})
	model = updated.(mockShellModel)

	changed := 0
	for model.revealQueue.Len() > 0 {
		before := model.View()
		clock.Add(mockRevealDelay)
		updated, _ = model.Update(mockAnimationTickMsg{})
		model = updated.(mockShellModel)
		if after := model.View(); after != before {
			changed++
		}
		if changed > 1000 {
			t.Fatal("reveal did not complete")
		}
	}
	return changed
}

func driveRevealToCompletion(t *testing.T, model *mockShellModel, clock *appFakeClock) {
	t.Helper()

	for i := 0; model.revealQueue.Len() > 0; i++ {
		if i > 1000 {
			t.Fatal("reveal did not complete")
		}
		clock.Add(mockRevealDelay)
		updated, _ := model.Update(mockAnimationTickMsg{})
		*model = updated.(mockShellModel)
	}
}

func lineCount(value string) int {
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func numberedMockMessages(channel string, count int) []twitch.ChatMessage {
	startedAt := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	messages := make([]twitch.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, twitch.ChatMessage{
			ID:          fmt.Sprintf("mock-%02d", i),
			Channel:     channel,
			Timestamp:   startedAt.Add(time.Duration(i) * time.Second),
			AuthorLogin: "viewer",
			DisplayName: "viewer",
			Text:        fmt.Sprintf("message-%02d", i),
			Type:        twitch.MessageTypeChat,
		})
	}
	return messages
}

func messagesContainText(messages []twitch.ChatMessage, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Text, text) {
			return true
		}
	}
	return false
}

func lipglossWidth(value string) int {
	return lipgloss.Width(value)
}
