package app

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/worxbend/twi/internal/animation"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

type tuiWorkload struct {
	name                        string
	channels, backlog, arrivals int
	animation                   string
}

var tuiWorkloads = []tuiWorkload{
	{"quiet-one", 1, 0, 0, "off"},
	{"quiet-many", 100, 0, 0, "fast"},
	{"busy-one", 1, config.DefaultScrollbackLimit, 10, "off"},
	{"busy-many", 10, config.DefaultScrollbackLimit, 10, "off"},
	{"busy-animated", 10, config.DefaultScrollbackLimit, 10, "fast"},
}

func workloadMessage(channel string, index int, at time.Time) twitch.ChatMessage {
	return twitch.ChatMessage{ID: fmt.Sprintf("workload-%s-%d", channel, index), Channel: channel, AuthorID: fmt.Sprintf("user%d", index%64), AuthorLogin: fmt.Sprintf("viewer%d", index%64), DisplayName: fmt.Sprintf("Viewer%d", index%64), AuthorColor: "#9146ff", Timestamp: at, Type: twitch.MessageTypeChat, Text: fmt.Sprintf("chat line %d with Unicode 視聴者 😀 and representative words wrapping in a narrow pane", index)}
}
func newWorkloadModel(workload tuiWorkload) (shellModel, *appFakeClock) {
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	clock := &appFakeClock{now: at}
	return buildWorkloadModel(workload, clock, at), clock
}

func buildWorkloadModel(workload tuiWorkload, clock animation.Clock, at time.Time) shellModel {
	cfg := config.Default()
	cfg.Features.AnimationMode = workload.animation
	cfg.DefaultChannels = make([]string, workload.channels)
	for i := range workload.channels {
		cfg.DefaultChannels[i] = fmt.Sprintf("channel%d", i)
	}
	model := newShellModel(cfg.DefaultChannels[0], cfg, clock)
	model.width, model.height = 120, 40
	model.frames.lastFrameAt = at
	for _, channel := range cfg.DefaultChannels {
		state := model.channels.ensure(channel)
		state.status = ConnectionState{Status: ConnectionConnected, Channel: channel, At: at}
		state.messages = make([]twitch.ChatMessage, workload.backlog)
		for index := range workload.backlog {
			state.messages[index] = workloadMessage(channel, index, at.Add(-time.Duration(workload.backlog-index)*time.Second))
		}
	}
	_ = model.View()
	return model
}

// BenchmarkTUIWorkload measures complete model updates and a redraw. A busy
// frame supplies ten messages across the selected channels, then advances the
// shared 100ms chrome clock and the 20ms reveal clock five times. No commands
// open network connections or system notification processes in this fixture.
func BenchmarkTUIWorkload(b *testing.B) {
	for _, workload := range tuiWorkloads {
		b.Run(workload.name, func(b *testing.B) {
			model, clock := newWorkloadModel(workload)
			channels := model.channels.channelNames()
			next := workload.backlog
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for arrival := range workload.arrivals {
					channel := channels[arrival%len(channels)]
					updated, _ := model.Update(chatClientMessageMsg{message: workloadMessage(channel, next, clock.now), ok: true})
					model = updated.(shellModel)
					next++
				}
				clock.now = clock.now.Add(animation.DefaultFrameInterval)
				updated, _ := model.Update(animation.FrameMsg{At: clock.now})
				model = updated.(shellModel)
				if workload.animation != "off" {
					for range 5 {
						updated, _ = model.Update(mockAnimationTickMsg{})
						model = updated.(shellModel)
					}
				}
				_ = model.View()
			}
			b.StopTimer()
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "frames/s")
			b.ReportMetric(float64(workload.arrivals)*float64(b.N)/b.Elapsed().Seconds(), "messages/s")
			b.ReportMetric(float64(len(model.activeChannelState().messages)), "backlog/channel")
			runtime.KeepAlive(&model)
		})
	}
}

// BenchmarkTUIRetainedMemory measures GC-live heap, not cumulative allocations.
// A single fixture is held alive across both samples. Repeated process samples
// absorb runtime/cache noise; timing for this benchmark is not an acceptance metric.
func BenchmarkTUIRetainedMemory(b *testing.B) {
	for _, workload := range tuiWorkloads {
		b.Run(workload.name, func(b *testing.B) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			model, _ := newWorkloadModel(workload)
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			live := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if live < 0 {
				live = 0
			}
			b.ResetTimer()
			for b.Loop() {
			}
			b.StopTimer()
			b.ReportMetric(float64(live), "retained-B")
			b.ReportMetric(float64(live)/float64(workload.channels), "retained-B/channel")
			b.ReportMetric(float64(workload.channels), "channels")
			runtime.KeepAlive(&model)
		})
	}
}

func TestQuietOffDoesNotScheduleRedraw(t *testing.T) {
	model, _ := newWorkloadModel(tuiWorkloads[0])
	if model.scheduleFrameTick() != nil {
		t.Fatal("quiet animation-off model schedules continuous redraw")
	}
}

var _ tea.Model = shellModel{}
