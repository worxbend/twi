package animation

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// DefaultFrameInterval is the shared animation clock's tick rate. ~10fps is
// enough for pulsing status indicators, flashes, the startup splash, and
// typewriter reveals without meaningful CPU overhead.
const DefaultFrameInterval = 100 * time.Millisecond

// FrameMsg is emitted by the shared animation clock. Unlike the reveal-queue
// tick (which only runs while chat rows are being typed in), the frame clock
// ticks continuously so every chrome animation effect can share one driver.
type FrameMsg struct {
	At time.Time
}

// ScheduleFrame returns a tea.Cmd that emits a FrameMsg after interval.
func ScheduleFrame(interval time.Duration) tea.Cmd {
	if interval <= 0 {
		interval = DefaultFrameInterval
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return FrameMsg{At: t}
	})
}
