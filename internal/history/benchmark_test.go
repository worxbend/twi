package history

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkRecordedWorkload exercises durable three-channel writes followed by
// bounded snapshot pages while the same store receives future records.
func BenchmarkRecordedWorkload(b *testing.B) {
	s, e := OpenWriter(filepath.Join(b.TempDir(), "private", "history.db"))
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := message(fmt.Sprintf("channel%d", i%3), fmt.Sprint(i), at.Add(time.Duration(i)*time.Millisecond))
		if _, _, e = s.Append(ctx, r); e != nil {
			b.Fatal(e)
		}
	}
	b.StopTimer()
	high, e := s.HighWater(ctx)
	if e != nil {
		b.Fatal(e)
	}
	reader, e := Open(s.path)
	if e != nil {
		b.Fatal(e)
	}
	defer reader.Close()
	began := time.Now()
	position := int64(0)
	rowsSeen := 0
	maxPage := 0
	for position < high {
		rows, next, e := reader.ReadThrough(ctx, Query{}, position, high, 256)
		if e != nil {
			b.Fatal(e)
		}
		if len(rows) > maxPage {
			maxPage = len(rows)
		}
		rowsSeen += len(rows)
		position = next
		// Simulate processing a page while ingestion continues; new records must not
		// enter the original snapshot. Pages release their SQLite snapshot promptly.
		if _, _, e = s.Append(ctx, message("channel0", fmt.Sprintf("future-%d", position), at.Add(time.Hour))); e != nil {
			b.Fatal(e)
		}
	}
	if rowsSeen != b.N || maxPage > 256 {
		b.Fatal(rowsSeen, maxPage)
	}
	b.ReportMetric(float64(time.Since(began).Microseconds())/1000, "replay-ms")
	b.ReportMetric(float64(maxPage), "max-page")
	bytes, e := s.storeBytes()
	if e != nil {
		b.Fatal(e)
	}
	b.ReportMetric(float64(bytes), "store-bytes")
}
