package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func TestChatRowsGroupConsecutiveAuthorsAndSeparateChanges(t *testing.T) {
	model := groupedChatTestModel()
	layout := model.layout()
	blocks := model.visibleChatRowBlocks(layout)

	wantGroups := []int{0, 0, 1, 1, 2}
	wantSeparators := []bool{false, false, true, false, true}
	if len(blocks) != len(wantGroups) {
		t.Fatalf("visible block count = %d, want %d", len(blocks), len(wantGroups))
	}
	for index, block := range blocks {
		if block.groupIndex != wantGroups[index] || block.separatorBefore != wantSeparators[index] {
			t.Errorf(
				"block %d group/separator = (%d, %v), want (%d, %v)",
				index,
				block.groupIndex,
				block.separatorBefore,
				wantGroups[index],
				wantSeparators[index],
			)
		}
		if got := chatRowBlockRowCount(block); got != 1 {
			t.Fatalf("test message block %d row count = %d, want 1", index, got)
		}
	}

	rows := model.chatRows(layout)
	if got, want := len(rows), 7; got != want {
		t.Fatalf("rendered row count = %d, want %d (5 messages + 2 author separators)", got, want)
	}
	separator := strings.TrimSpace(ansi.Strip(rows[2]))
	if separator == "" || strings.Trim(separator, "─") != "" {
		t.Fatalf("first author separator = %q, want a visible horizontal rule", separator)
	}
	if got := strings.TrimSpace(ansi.Strip(rows[5])); got != separator {
		t.Fatalf("second author separator = %q, want %q", got, separator)
	}
}

func TestAuthorSeparatorRowsAreNotMouseSelectable(t *testing.T) {
	model := groupedChatTestModel()
	layout := model.layout()

	for _, test := range []struct {
		row    int
		wantID string
		wantOK bool
	}{
		{row: 1, wantID: "alice-2", wantOK: true},
		{row: 2, wantOK: false},
		{row: 3, wantID: "bob-1", wantOK: true},
		{row: 5, wantOK: false},
		{row: 6, wantID: "alice-3", wantOK: true},
	} {
		message, ok := model.messageAtVisibleChatRow(layout, test.row)
		if ok != test.wantOK || message.ID != test.wantID {
			t.Errorf("message at visible row %d = (%q, %v), want (%q, %v)", test.row, message.ID, ok, test.wantID, test.wantOK)
		}
	}
}

func TestAuthorGroupingPrefersStableUserIDOverSharedDisplayName(t *testing.T) {
	blocks := []chatRowBlock{
		{message: twitch.ChatMessage{AuthorID: "user-1", DisplayName: "Viewer", Type: twitch.MessageTypeChat}},
		{message: twitch.ChatMessage{AuthorID: "user-2", DisplayName: "Viewer", Type: twitch.MessageTypeChat}},
	}
	assignChatAuthorGroups(blocks)
	if !blocks[1].separatorBefore || blocks[0].groupIndex == blocks[1].groupIndex {
		t.Fatalf("different author IDs with the same display name were grouped together: %#v", blocks)
	}
}

func TestAuthorlessEventsRemainSeparateGroups(t *testing.T) {
	blocks := []chatRowBlock{
		{message: twitch.ChatMessage{ID: "notice-1", Type: twitch.MessageTypeNotice, SystemEventID: "raid"}},
		{message: twitch.ChatMessage{ID: "notice-2", Type: twitch.MessageTypeNotice, SystemEventID: "raid"}},
		{message: twitch.ChatMessage{Type: twitch.MessageTypeSystem, SystemEventID: "status"}},
		{message: twitch.ChatMessage{Type: twitch.MessageTypeSystem, SystemEventID: "status"}},
	}
	assignChatAuthorGroups(blocks)
	for index, block := range blocks {
		if block.groupIndex != index {
			t.Errorf("authorless block %d group = %d, want its own group %d", index, block.groupIndex, index)
		}
		if index > 0 && !block.separatorBefore {
			t.Errorf("authorless block %d has no separator", index)
		}
	}
}

func TestAuthorSeparatorsParticipateInScrollAccounting(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 12
	state := model.activeChannelState()
	state.messages = make([]twitch.ChatMessage, 10)
	for index := range state.messages {
		author := fmt.Sprintf("viewer-%d", index%2)
		state.messages[index] = twitch.ChatMessage{
			ID:          fmt.Sprintf("message-%d", index),
			Channel:     "alpha",
			AuthorLogin: author,
			DisplayName: author,
			Type:        twitch.MessageTypeChat,
			Text:        "short",
		}
	}
	state.activeOrder = nil
	state.activeMessages = map[string]twitch.ChatMessage{}

	layout := model.layout()
	rows := model.chatRows(layout)
	want := len(rows) - layout.chatContentHeight
	if want <= 0 {
		t.Fatalf("test setup produced max scroll %d from %d rows and %d visible rows", want, len(rows), layout.chatContentHeight)
	}
	if got := model.maxScrollOffset(); got != want {
		t.Fatalf("max scroll offset = %d, want %d including author separators", got, want)
	}
	model.scrollBy(want + 10)
	if got := state.scrollOffset; got != want {
		t.Fatalf("clamped scroll offset = %d, want %d", got, want)
	}
}

func TestDifferentAuthorAppendPreservesScrolledViewport(t *testing.T) {
	model := newMockShellModel("example", config.Default())
	model.activeChannelState().messages = numberedMockMessages("example", 30)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 12})
	model = updated.(mockShellModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(mockShellModel)
	state := model.activeChannelState()
	if state.scrollOffset == 0 {
		t.Fatal("test setup failed: expected a scrolled viewport")
	}
	beforeView := model.View()
	beforeRows := len(model.chatRows(model.layout()))
	beforeOffset := state.scrollOffset

	message := mockIncomingMessage("example", "different-author", "off-screen different author")
	message.AuthorLogin = "another-viewer"
	message.DisplayName = "another-viewer"
	updated, cmd := model.Update(mockIncomingMessageMsg{message: message})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("scrolled append returned command %#v, want no reveal tick", cmd)
	}
	afterRows := len(model.chatRows(model.layout()))
	wantOffset := beforeOffset + afterRows - beforeRows
	if got := model.activeChannelState().scrollOffset; got != wantOffset {
		t.Fatalf("scroll offset after different-author append = %d, want %d including separator delta", got, wantOffset)
	}
	if afterView := model.View(); afterView != beforeView {
		t.Fatalf("different-author append changed the visible scrolled page:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
}

func TestMessageGroupSeparatorPreservesResponsiveWidth(t *testing.T) {
	forceColorProfile(t)
	model := newMockShellModel("alpha", config.Default())
	for width := 1; width <= 60; width++ {
		line := model.messageGroupSeparatorString(width)
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("separator width at %d cells = %d: %q", width, got, ansi.Strip(line))
		}
		if !strings.Contains(ansi.Strip(line), "─") {
			t.Fatalf("separator at %d cells has no visible rule: %q", width, ansi.Strip(line))
		}
	}
}

func groupedChatTestModel() mockShellModel {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockShellModel("alpha", cfg)
	model.width, model.height = 88, 22
	model.activeChannelState().messages = []twitch.ChatMessage{
		{ID: "alice-1", Channel: "alpha", AuthorLogin: "Alice", DisplayName: "Alice", Type: twitch.MessageTypeChat, Text: "first"},
		{ID: "alice-2", Channel: "alpha", AuthorLogin: "alice", DisplayName: "Alice", Type: twitch.MessageTypeChat, Text: "second"},
		{ID: "bob-1", Channel: "alpha", AuthorLogin: "bob", DisplayName: "Bob", Type: twitch.MessageTypeChat, Text: "third"},
		{ID: "bob-2", Channel: "alpha", AuthorLogin: "BOB", DisplayName: "Bob", Type: twitch.MessageTypeChat, Text: "fourth"},
		{ID: "alice-3", Channel: "alpha", AuthorLogin: "alice", DisplayName: "Alice", Type: twitch.MessageTypeChat, Text: "fifth"},
	}
	active := model.activeChannelState()
	active.activeOrder = nil
	active.activeMessages = map[string]twitch.ChatMessage{}
	return model
}
