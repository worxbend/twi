package history

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
)

// TestSlowConsumerWorkload measures one quiet and two busy channels against a
// 1 ms/message sink. The reader releases its SQL page before sink delays. These
// broad budgets catch runaway buffering or long reader transactions, not host
// speed differences. Run with -v to record the baseline.
func TestSlowConsumerWorkload(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, c := range []string{"quiet", "busy_a", "busy_b"} {
		if err := s.BeginCapture(ctx, c, "workload", "irc", at); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const total = 600
	queue := make(chan []automation.Record, 2)
	writerDone := make(chan error, 1)
	maxQueue := 0
	started := time.Now()
	go func() {
		for batch := range queue {
			if _, err := s.AppendBatch(ctx, batch); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()
	for start := 0; start < total; start += 100 {
		batch := make([]automation.Record, 100)
		for j := range batch {
			i := start + j
			channel := "busy_a"
			if i%3 == 0 {
				channel = "busy_b"
			}
			batch[j] = message(channel, fmt.Sprint(i), at)
		}
		queue <- batch
		if n := len(queue); n > maxQueue {
			maxQueue = n
		}
	}
	close(queue)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	ingest := time.Since(started)
	reader, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	high, err := reader.HighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var maxLatency time.Duration
	position := int64(0)
	read := 0
	maxPage := 0
	for position < high {
		pageStarted := time.Now()
		rows, next, err := reader.ReadThrough(ctx, Query{}, position, high, 64)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > maxPage {
			maxPage = len(rows)
		}
		for range rows {
			time.Sleep(time.Millisecond)
			read++
			latency := time.Since(pageStarted)
			if latency > maxLatency {
				maxLatency = latency
			}
		}
		position = next
		// Ingest during stalled output; no held reader transaction may pin WAL.
		if _, _, err = s.Append(ctx, message("busy_a", fmt.Sprintf("follow-%d", position), at.Add(time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	wal := int64(0)
	if info, err := os.Stat(s.path + "-wal"); err == nil {
		wal = info.Size()
	}
	t.Logf("records=%d throughput=%.0f records/s heap_growth=%d bytes max_queue_batches=%d max_page=%d WAL=%d bytes max_page_output_latency=%s", read, float64(total)/ingest.Seconds(), growth, maxQueue, maxPage, wal, maxLatency)
	if read != total || maxQueue > 2 || maxPage > 64 || growth > 64<<20 || wal > 16<<20 || maxLatency > 2*time.Second || ingest > 10*time.Second {
		t.Fatal("workload regression budget exceeded")
	}
}
