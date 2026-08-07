package app

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// benchModel builds a sized model holding count backlog messages in the
// active channel, with animation off so the benchmark measures the static
// render path rather than reveal progress.
func benchModel(b *testing.B, count int) mockShellModel {
	b.Helper()
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	// Trimming is what the cap change adds; disable it here so the benchmark
	// can still measure the cost of a large backlog directly.
	cfg.Features.ScrollbackLimit = 0
	clock := &appFakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	model := newMockShellModelWithClock("example", cfg, clock)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(mockShellModel)

	state := model.activeChannelState()
	state.messages = make([]twitch.ChatMessage, 0, count)
	base := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	for i := range count {
		state.messages = append(state.messages, twitch.ChatMessage{
			ID:          fmt.Sprintf("bench-%d", i),
			Channel:     "example",
			AuthorLogin: fmt.Sprintf("chatter%d", i%64),
			DisplayName: fmt.Sprintf("Chatter%d", i%64),
			AuthorColor: "#9146ff",
			Text:        "this is a representative chat line with enough text to wrap on a narrow column",
			Timestamp:   base.Add(time.Duration(i) * time.Second),
			Type:        twitch.MessageTypeChat,
		})
	}
	return model
}

func BenchmarkView(b *testing.B) {
	for _, count := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("messages=%d", count), func(b *testing.B) {
			model := benchModel(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

// BenchmarkMaxScrollOffset isolates the scroll-clamp path, which several
// Update branches reach on every arriving message.
func BenchmarkMaxScrollOffset(b *testing.B) {
	for _, count := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("messages=%d", count), func(b *testing.B) {
			model := benchModel(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = model.maxScrollOffset()
			}
		})
	}
}
