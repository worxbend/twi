package app

import (
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func TestFormatStatusMetrics(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(model *shellModel, now time.Time)
		now         func(liveSince time.Time) time.Time
		recording   bool
		contains    []string
		notContains []string
	}{
		{
			name: "offline without optional metrics",
			setup: func(model *shellModel, _ time.Time) {
				state := model.channels.activeState()
				state.live = false
				state.viewerCount = 0
			},
			now:         func(time.Time) time.Time { return time.Time{} },
			contains:    []string{"OFFLINE", "mem=0MB", "fps=0", "chat=0.0KB/s"},
			notContains: []string{"LIVE", "viewers=", "followers=", "subs=", "REC", "cpu="},
		},
		{
			name: "live with all metrics",
			setup: func(model *shellModel, now time.Time) {
				model.metrics.followerCount = 42
				model.metrics.followerCountKnown = true
				model.metrics.subscriberCount = 7
				model.metrics.subscriberCountKnown = true
				model.runtime.cpuAvailable = true
				model.runtime.cpuPercent = 25
				model.runtime.memoryMB = 64
				model.frames.frameTimestamps = make([]time.Time, 30)
				model.runtime.chatByteSamples = []chatByteSample{
					{at: now, bytes: 512},
					{at: now, bytes: 512},
				}
			},
			now:         func(liveSince time.Time) time.Time { return liveSince.Add(90 * time.Second) },
			recording:   true,
			contains:    []string{"LIVE 1:30", "viewers=128", "followers=42", "subs=7", "REC", "cpu=25%", "mem=64MB", "fps=30", "chat=0.2KB/s"},
			notContains: []string{"OFFLINE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newMockModel("example", config.Default())
			liveSince := model.channels.activeState().liveSince
			now := tt.now(liveSince)
			if tt.setup != nil {
				tt.setup(&model, now)
			}
			line := model.formatStatusMetrics(now, tt.recording)
			for _, want := range tt.contains {
				if !strings.Contains(line, want) {
					t.Errorf("formatStatusMetrics = %q, missing %q", line, want)
				}
			}
			for _, unwanted := range tt.notContains {
				if strings.Contains(line, unwanted) {
					t.Errorf("formatStatusMetrics = %q, unexpectedly contains %q", line, unwanted)
				}
			}
		})
	}
}

func TestStatusPulse(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "zero time reads as on", now: time.Time{}, want: true},
		{name: "even half-second", now: time.UnixMilli(0), want: true},
		{name: "odd half-second", now: time.UnixMilli(500), want: false},
		{name: "next even half-second", now: time.UnixMilli(1000), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusPulse(tt.now); got != tt.want {
				t.Fatalf("statusPulse(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestTrimChatByteSamples(t *testing.T) {
	now := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		samples []chatByteSample
		want    int
	}{
		{name: "empty stays empty", samples: nil, want: 0},
		{
			name: "all inside window kept",
			samples: []chatByteSample{
				{at: now.Add(-time.Second), bytes: 10},
				{at: now, bytes: 20},
			},
			want: 2,
		},
		{
			name: "expired and boundary samples dropped",
			samples: []chatByteSample{
				{at: now.Add(-chatBitrateWindow - time.Second), bytes: 10},
				{at: now.Add(-chatBitrateWindow), bytes: 20},
				{at: now.Add(-chatBitrateWindow + time.Millisecond), bytes: 30},
				{at: now, bytes: 40},
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := shellModel{}
			model.runtime.chatByteSamples = tt.samples
			model.trimChatByteSamples(now)
			if len(model.runtime.chatByteSamples) != tt.want {
				t.Fatalf("trimChatByteSamples kept %d samples, want %d", len(model.runtime.chatByteSamples), tt.want)
			}
		})
	}
}

func TestRecordChatBytesTrimsWithoutFrameTick(t *testing.T) {
	model := shellModel{}
	model.runtime.chatByteSamples = []chatByteSample{
		{at: time.Now().Add(-time.Minute), bytes: 100},
	}
	model.recordChatBytes(twitch.ChatMessage{Text: "hello"})
	if len(model.runtime.chatByteSamples) != 1 {
		t.Fatalf("chatByteSamples = %d entries, want 1 (stale sample trimmed without a frame tick)", len(model.runtime.chatByteSamples))
	}
	if model.runtime.chatByteSamples[0].bytes != len("hello") {
		t.Fatalf("sample bytes = %d, want %d", model.runtime.chatByteSamples[0].bytes, len("hello"))
	}
}
