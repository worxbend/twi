package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/w0rxbend/twi/internal/twitch"
)

const (
	streamStatusPollInterval = 60 * time.Second
	chatBitrateWindow        = 5 * time.Second
)

type chatByteSample struct {
	at    time.Time
	bytes int
}

type streamStatusTickMsg struct{}

type streamStatusResolvedMsg struct {
	results []twitch.StreamInfo
	err     error
}

// scheduleStreamStatusTick polls Twitch Helix "Get Streams" for every
// configured channel every streamStatusPollInterval. Polling is disabled
// (StreamStatusResolver is nil) without live credentials or when
// stream_status_mode is "off".
func (m *mockShellModel) scheduleStreamStatusTick() tea.Cmd {
	if m.streamStatusResolver == nil || m.streamStatusTickScheduled {
		return nil
	}
	m.streamStatusTickScheduled = true
	return tea.Tick(streamStatusPollInterval, func(time.Time) tea.Msg {
		return streamStatusTickMsg{}
	})
}

func (m mockShellModel) resolveStreamStatusCommand() tea.Cmd {
	resolver := m.streamStatusResolver
	if resolver == nil {
		return nil
	}
	logins := m.channels.channelNames()
	return func() tea.Msg {
		results, err := resolver.GetStreams(context.Background(), logins)
		return streamStatusResolvedMsg{results: results, err: err}
	}
}

func (m *mockShellModel) applyStreamStatusResults(results []twitch.StreamInfo) {
	for _, result := range results {
		state := m.channels.ensure(result.UserLogin)
		if state == nil {
			continue
		}
		state.live = result.Live
		state.viewerCount = result.ViewerCount
		if result.Live {
			state.liveSince = result.StartedAt
		} else {
			state.liveSince = time.Time{}
		}
	}
}

// recordChatBytes tracks incoming chat message size for the derived "chat
// bitrate" status-bar figure. Twitch does not expose stream ingest/encode
// bitrate through any public API, so this reports actual chat-message
// throughput instead of implying a stream encode bitrate.
func (m *mockShellModel) recordChatBytes(message twitch.ChatMessage) {
	m.chatByteSamples = append(m.chatByteSamples, chatByteSample{
		at:    time.Now(),
		bytes: len(message.Text),
	})
}

// sampleResourceUsage records a CPU-time delta and the current Go heap
// allocation once per animation tick. These are sampled here (not read fresh
// inside View()) so View() stays a pure function of already-ticked model
// state instead of reading live, ever-changing runtime stats mid-render.
// Unavailable on platforms without sampleProcessCPUTime support (see
// status_metrics_unix.go / status_metrics_other.go).
func (m *mockShellModel) sampleResourceUsage(now time.Time) {
	cpuTime, ok := sampleProcessCPUTime()
	if !ok {
		m.cpuAvailable = false
	} else {
		if !m.cpuSampleAt.IsZero() {
			wall := now.Sub(m.cpuSampleAt)
			if wall > 0 {
				m.cpuPercent = float64(cpuTime-m.cpuSampleTime) / float64(wall) * 100
				m.cpuAvailable = true
			}
		}
		m.cpuSampleAt = now
		m.cpuSampleTime = cpuTime
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	m.memoryMB = float64(stats.Alloc) / (1024 * 1024)
}

// trimChatByteSamples drops samples outside the rolling bitrate window.
func (m *mockShellModel) trimChatByteSamples(now time.Time) {
	cutoff := now.Add(-chatBitrateWindow)
	trimmed := m.chatByteSamples[:0]
	for _, sample := range m.chatByteSamples {
		if sample.at.After(cutoff) {
			trimmed = append(trimmed, sample)
		}
	}
	m.chatByteSamples = trimmed
}

// chatBitrateBps returns the rolling-window chat-message byte throughput.
func (m mockShellModel) chatBitrateBps() float64 {
	if len(m.chatByteSamples) == 0 {
		return 0
	}
	total := 0
	for _, sample := range m.chatByteSamples {
		total += sample.bytes
	}
	return float64(total) / chatBitrateWindow.Seconds()
}

// fps returns the shared animation clock's achieved frame rate over the last
// second (see advanceFrame's frameTimestamps bookkeeping).
func (m mockShellModel) fps() float64 {
	return float64(len(m.frameTimestamps))
}

// formatStatusMetrics renders the LIVE/REC telemetry segment of the status
// bar. debugRecording is cfg.Debug.Enabled: twi's own debug-log recording,
// the only "recording" concept this app has. now is the zero time before the
// animation clock's first tick (animation disabled, or no Update() cycle has
// run yet), in which case elapsed/pulse render as static, deterministic
// values instead of reading the wall clock directly from View().
func (m mockShellModel) formatStatusMetrics(now time.Time, debugRecording bool) string {
	active := m.activeChannelState()
	pulse := now.IsZero() || (now.UnixMilli()/500)%2 == 0

	parts := make([]string, 0, 6)
	if active.live {
		parts = append(parts, pulseLabel("LIVE", pulse)+" "+formatElapsed(liveElapsed(now, active.liveSince)))
		if active.viewerCount > 0 {
			parts = append(parts, fmt.Sprintf("viewers=%d", active.viewerCount))
		}
	} else {
		parts = append(parts, "OFFLINE")
	}
	if debugRecording {
		parts = append(parts, pulseLabel("REC", pulse))
	}
	if m.cpuAvailable {
		parts = append(parts, fmt.Sprintf("cpu=%.0f%%", m.cpuPercent))
	}
	parts = append(parts, fmt.Sprintf("mem=%.0fMB", m.memoryMB))
	parts = append(parts, fmt.Sprintf("fps=%.0f", m.fps()))
	parts = append(parts, fmt.Sprintf("chat=%.1fKB/s", m.chatBitrateBps()/1024))
	return strings.Join(parts, " ")
}

// metricsNow returns the animation clock's last tick time (the zero time
// before the first tick). Status metrics deliberately never fall back to the
// wall clock: View() must stay a pure function of already-ticked model
// state, matching how the rest of the app (chat reveal, scene flash) is
// tested with an injectable clock rather than free-floating real time.
func (m mockShellModel) metricsNow() time.Time {
	return m.lastFrameAt
}

// compactStatusMetrics renders just the LIVE/OFFLINE badge and elapsed time
// for narrower terminals that don't have room for the full metrics line.
func (m mockShellModel) compactStatusMetrics(now time.Time) string {
	active := m.activeChannelState()
	pulse := now.IsZero() || (now.UnixMilli()/500)%2 == 0
	if !active.live {
		return "OFFLINE"
	}
	return pulseLabel("LIVE", pulse) + " " + formatElapsed(liveElapsed(now, active.liveSince))
}

// liveElapsed returns the on-air duration as of now, or zero when now or
// liveSince hasn't been established yet.
func liveElapsed(now, liveSince time.Time) time.Duration {
	if now.IsZero() || liveSince.IsZero() {
		return 0
	}
	return now.Sub(liveSince)
}

func pulseLabel(label string, on bool) string {
	if on {
		return label
	}
	return "·" + label
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
