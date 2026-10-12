//go:build unix

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

type profileStats struct {
	mu              sync.Mutex
	views, received int
	renderTimes     []time.Duration
	arrivalTimes    []time.Duration
	first           chan struct{}
	once            sync.Once
}
type profiledShell struct {
	shellModel
	stats *profileStats
}

func (m profiledShell) Init() tea.Cmd { return m.scheduleFrameTick() }
func (m profiledShell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.shellModel.Update(msg)
	if event, ok := msg.(chatClientMessageMsg); ok && event.ok {
		m.stats.mu.Lock()
		m.stats.received++
		m.stats.arrivalTimes = append(m.stats.arrivalTimes, time.Since(event.message.Timestamp))
		m.stats.mu.Unlock()
	}
	return profiledShell{updated.(shellModel), m.stats}, cmd
}
func (m profiledShell) View() tea.View {
	start := time.Now()
	view := m.shellModel.View()
	elapsed := time.Since(start)
	m.stats.mu.Lock()
	m.stats.views++
	m.stats.renderTimes = append(m.stats.renderTimes, elapsed)
	m.stats.mu.Unlock()
	m.stats.once.Do(func() { close(m.stats.first) })
	return view
}

type profileWriter struct {
	delay         time.Duration
	writes, bytes atomic.Uint64
}

func (w *profileWriter) Write(data []byte) (int, error) {
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	w.writes.Add(1)
	w.bytes.Add(uint64(len(data)))
	return len(data), nil
}

var _ io.Writer = (*profileWriter)(nil)

type tuiRuntimeResult struct {
	Workload                      string  `json:"workload"`
	Channels                      int     `json:"channels"`
	DurationSeconds               float64 `json:"duration_seconds"`
	ProcessCPUSeconds             float64 `json:"process_cpu_seconds"`
	CoreUtilizationPercent        float64 `json:"core_utilization_percent"`
	OfferedMessages               int     `json:"offered_messages"`
	ProcessedMessages             int     `json:"processed_messages"`
	DroppedMessages               int     `json:"dropped_messages"`
	QueuedAtShutdown              int     `json:"queued_at_shutdown"`
	ArrivalLatencyP95Milliseconds float64 `json:"arrival_latency_p95_ms"`
	ArrivalLatencyMaxMilliseconds float64 `json:"arrival_latency_max_ms"`
	InputQueueCapacity            int     `json:"input_queue_capacity"`
	InputQueuePeak                int     `json:"input_queue_peak"`
	Views                         int     `json:"views"`
	ViewsPerSecond                float64 `json:"views_per_second"`
	TerminalWritesPerSecond       float64 `json:"terminal_writes_per_second"`
	TerminalBytesPerSecond        float64 `json:"terminal_bytes_per_second"`
	ViewP95Milliseconds           float64 `json:"view_p95_ms"`
	AllocatedBytesPerSecond       float64 `json:"allocated_bytes_per_second"`
	AllocationsPerSecond          float64 `json:"allocations_per_second"`
	LiveHeapBytes                 int64   `json:"post_run_live_heap_delta_bytes"`
}

func processCPU(t *testing.T) float64 {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	return float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
}

// TestTUIRuntimeProfile runs the real Bubble Tea event loop and renderer. The
// writer discards screen bytes, or deliberately delays them. A bounded 128-event
// producer offers 100 messages/s independently of UI processing, making pressure
// visible rather than silently lowering the offered rate to match the renderer.
// This opt-in workload writes only to the requested artifact directory.
func TestTUIRuntimeProfile(t *testing.T) {
	directory := os.Getenv("TWI_TUI_PROFILE_DIR")
	if directory == "" {
		t.Skip("set TWI_TUI_PROFILE_DIR to collect a real TUI workload profile")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	duration := 3 * time.Second
	if value := os.Getenv("TWI_TUI_PROFILE_DURATION"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Second || parsed > time.Minute {
			t.Fatal("profile duration must be 1s to 1m")
		}
		duration = parsed
	}
	type scenario struct {
		workload tuiWorkload
		delay    time.Duration
	}
	scenarios := []scenario{{tuiWorkloads[0], 0}, {tuiWorkloads[1], 0}, {tuiWorkloads[3], 0}, {tuiWorkloads[4], 0}, {tuiWorkloads[3], 25 * time.Millisecond}}
	results := make([]tuiRuntimeResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		name := scenario.workload.name
		if scenario.delay > 0 {
			name += "-slow-terminal"
		}
		t.Run(name, func(t *testing.T) {
			var result tuiRuntimeResult
			pprof.Do(context.Background(), pprof.Labels("workload", name), func(context.Context) {
				result = profileRuntimeWorkload(t, name, scenario.workload, scenario.delay, duration)
			})
			results = append(results, result)
			t.Logf("views %.1f/s writes %.1f/s CPU %.1f%% p95 %.2fms queue %d/128 drops %d", result.ViewsPerSecond, result.TerminalWritesPerSecond, result.CoreUtilizationPercent, result.ViewP95Milliseconds, result.InputQueuePeak, result.DroppedMessages)
		})
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "runtime.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func profileRuntimeWorkload(t *testing.T, name string, workload tuiWorkload, delay, duration time.Duration) tuiRuntimeResult {
	t.Helper()
	model := buildWorkloadModel(workload, nil, time.Now().UTC())
	stats := &profileStats{first: make(chan struct{})}
	writer := &profileWriter{delay: delay}
	program := tea.NewProgram(profiledShell{model, stats}, tea.WithInput(nil), tea.WithOutput(writer), tea.WithWindowSize(120, 40), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithoutSignalHandler())
	finished := make(chan error, 1)
	go func() { _, err := program.Run(); finished <- err }()
	defer program.Kill()
	select {
	case <-stats.first:
	case err := <-finished:
		t.Fatalf("program startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("program startup timed out")
	}
	// Let the startup render flush before measuring terminal writes and CPU.
	time.Sleep(75 * time.Millisecond)
	stats.mu.Lock()
	stats.views = 0
	stats.received = 0
	stats.renderTimes = nil
	stats.arrivalTimes = nil
	stats.mu.Unlock()
	writer.writes.Store(0)
	writer.bytes.Store(0)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuStart := processCPU(t)
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan chatClientMessageMsg, 128)
	var offered, dropped, peak atomic.Int64
	channels := model.channels.channelNames()
	stopped := make(chan struct{})
	generated := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-input:
				program.Send(message)
			}
		}
	}()
	go func() {
		defer close(generated)
		if workload.arrivals == 0 {
			<-ctx.Done()
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		next := workload.backlog
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				index := int(offered.Add(1)) - 1
				message := chatClientMessageMsg{message: workloadMessage(channels[index%len(channels)], next, at), ok: true}
				next++
				select {
				case input <- message:
				default:
					dropped.Add(1)
				}
				depth := int64(len(input))
				for old := peak.Load(); depth > old; old = peak.Load() {
					if peak.CompareAndSwap(old, depth) {
						break
					}
				}
			}
		}
	}()
	timer := time.NewTimer(duration)
	select {
	case <-timer.C:
	case err := <-finished:
		cancel()
		t.Fatalf("program stopped early: %v", err)
	}
	cancel()
	<-generated
	program.Quit()
	select {
	case err := <-finished:
		if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program failed to stop")
	}
	<-stopped
	elapsed := time.Since(start).Seconds()
	cpu := processCPU(t) - cpuStart
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	stats.mu.Lock()
	views, received := stats.views, stats.received
	times := append([]time.Duration(nil), stats.renderTimes...)
	arrivals := append([]time.Duration(nil), stats.arrivalTimes...)
	stats.mu.Unlock()
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i] < arrivals[j] })
	var arrivalP95, arrivalMax time.Duration
	if len(arrivals) > 0 {
		arrivalP95 = arrivals[(len(arrivals)-1)*95/100]
		arrivalMax = arrivals[len(arrivals)-1]
	}
	var p95 time.Duration
	if len(times) > 0 {
		p95 = times[(len(times)-1)*95/100]
	}
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	live := int64(retained.HeapAlloc) - int64(before.HeapAlloc)
	result := tuiRuntimeResult{Workload: name, Channels: workload.channels, DurationSeconds: elapsed, ProcessCPUSeconds: cpu, CoreUtilizationPercent: cpu / elapsed * 100, OfferedMessages: int(offered.Load()), ProcessedMessages: received, DroppedMessages: int(dropped.Load()), QueuedAtShutdown: max(0, int(offered.Load())-received-int(dropped.Load())), ArrivalLatencyP95Milliseconds: float64(arrivalP95) / float64(time.Millisecond), ArrivalLatencyMaxMilliseconds: float64(arrivalMax) / float64(time.Millisecond), InputQueueCapacity: cap(input), InputQueuePeak: int(peak.Load()), Views: views, ViewsPerSecond: float64(views) / elapsed, TerminalWritesPerSecond: float64(writer.writes.Load()) / elapsed, TerminalBytesPerSecond: float64(writer.bytes.Load()) / elapsed, ViewP95Milliseconds: float64(p95) / float64(time.Millisecond), AllocatedBytesPerSecond: float64(after.TotalAlloc-before.TotalAlloc) / elapsed, AllocationsPerSecond: float64(after.Mallocs-before.Mallocs) / elapsed, LiveHeapBytes: live}
	runtime.KeepAlive(&model)
	return result
}
