package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T, n int) (string, *history.Store, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "history.db")
	s, e := history.OpenWriter(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	at := time.Now().UTC()
	if e = s.BeginCapture(context.Background(), "alpha", "session", "irc", at.Add(-time.Minute)); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		_, _, e = s.Append(context.Background(), automation.Record{Type: "message", ID: fmt.Sprint(i), Channel: automation.Channel{ID: "1", Login: "alpha"}, Text: "hello", EventAt: at, ReceivedAt: at})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Heartbeat(context.Background(), "session", time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	return path, s, time.Now().UTC()
}
func historyOptions(path string) consumeOptions {
	return consumeOptions{dataOptions: dataOptions{channels: []string{"alpha"}, historyPath: path, format: "jsonl"}, offset: "beginning", historical: true, timeField: "event", events: "messages"}
}
func TestHistoryReplayPagesAndResume(t *testing.T) {
	path, s, at := historyFixture(t, 700)
	o := historyOptions(path)
	o.after = s.Cursor(1)
	o.offset = ""
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if got := strings.Count(out.String(), "\n"); got != 699 {
		t.Fatalf("got %d replay rows", got)
	}
}
func TestHistoryCountCheckpointResume(t *testing.T) {
	path, _, at := historyFixture(t, 300)
	o := historyOptions(path)
	o.cursorFile = filepath.Join(t.TempDir(), "cursor")
	o.count = 10
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	cp, e := history.ReadCheckpoint(o.cursorFile)
	if e != nil || cp.Position != 10 {
		t.Fatal(cp, e)
	}
	o.count = 0
	o.offset = ""
	out.Reset()
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if got := strings.Count(out.String(), "\n"); got != 290 {
		t.Fatalf("got %d rows", got)
	}
}
func TestHistoryCoverageFailsBeforeOutput(t *testing.T) {
	path, _, at := historyFixture(t, 1)
	o := historyOptions(path)
	o.channels = []string{"missing"}
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 6 || out.Len() != 0 {
		t.Fatal(code, out.String(), diagnostics.String())
	}
	o.allowPartial = true
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
}
func TestHistoryTailDoesNotReplayWholeStore(t *testing.T) {
	path, _, at := historyFixture(t, 700)
	o := historyOptions(path)
	o.last = 3
	o.offset = ""
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if strings.Count(out.String(), "\n") != 3 {
		t.Fatal(out.String())
	}
	var r automation.Record
	if e := json.Unmarshal(bytes.Split(out.Bytes(), []byte("\n"))[0], &r); e != nil || r.ID != "697" {
		t.Fatal(r, e)
	}
}

type appendDuringReplay struct {
	bytes.Buffer
	store    *history.Store
	appended bool
	err      error
}

func (w *appendDuringReplay) Write(p []byte) (int, error) {
	n, e := w.Buffer.Write(p)
	if !w.appended {
		w.appended = true
		_, _, w.err = w.store.Append(context.Background(), automation.Record{Type: "message", ID: "follow-message", Channel: automation.Channel{ID: "1", Login: "alpha"}, Text: "during replay", EventAt: time.Now(), ReceivedAt: time.Now()})
	}
	return n, e
}
func TestHistoryReplayFollowNoHandoverLoss(t *testing.T) {
	path, s, at := historyFixture(t, 2)
	o := historyOptions(path)
	o.follow = true
	o.count = 3
	out := &appendDuringReplay{store: s}
	var diagnostics bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if code := consumeHistory(ctx, o, at, out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if out.err != nil {
		t.Fatal(out.err)
	}
	if got := strings.Count(out.String(), "\n"); got != 3 {
		t.Fatalf("expected three records, got %d: %s diagnostics %s", got, out.String(), diagnostics.String())
	}
	if strings.Count(out.String(), "follow-message") != 1 {
		t.Fatal("follow duplicated or lost appended message", out.String())
	}
}

func TestHistoryTailAllEventsUsesPerChannelBoundaries(t *testing.T) {
	path, s, at := historyFixture(t, 1)
	ctx := context.Background()
	if e := s.BeginCapture(ctx, "beta", "beta-session", "irc", at.Add(-time.Minute)); e != nil {
		t.Fatal(e)
	}
	if e := s.Heartbeat(ctx, "beta-session", time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	appendRecord := func(channel, kind, id string) {
		t.Helper()
		_, _, e := s.Append(ctx, automation.Record{Type: kind, ID: id, Channel: automation.Channel{Login: channel}, Text: id, EventAt: at, ReceivedAt: at})
		if e != nil {
			t.Fatal(e)
		}
	}
	appendRecord("beta", "message", "beta-old")
	appendRecord("alpha", "notice", "alpha-before")
	appendRecord("beta", "message", "beta-tail")
	appendRecord("beta", "notice", "beta-after")
	appendRecord("alpha", "message", "alpha-tail")
	appendRecord("alpha", "notice", "alpha-after")
	o := historyOptions(path)
	o.channels = []string{"alpha", "beta"}
	o.offset = ""
	o.last = 1
	o.events = "all"
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(ctx, o, time.Now().UTC(), &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	var ids []string
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		var r automation.Record
		if e := json.Unmarshal(line, &r); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "beta-tail,beta-after,alpha-tail,alpha-after" {
		t.Fatal(ids)
	}
}
func TestFutureUntilRequiresFollowBeforeOpeningStore(t *testing.T) {
	o := historyOptions("/missing/database")
	o.until = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, time.Now(), &out, &diagnostics); code != 2 {
		t.Fatal(code, diagnostics.String())
	}
}
func TestFutureUntilFollowDrainsFinalInterval(t *testing.T) {
	path, s, at := historyFixture(t, 1)
	o := historyOptions(path)
	o.follow = true
	o.until = at.Add(60 * time.Millisecond).Format(time.RFC3339Nano)
	out := &appendDuringReplay{store: s}
	var diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if got := strings.Count(out.String(), "\n"); got != 2 {
		t.Fatal(got, out.String(), diagnostics.String())
	}
}

func TestHistoryFollowUntilChecksFinalCoverage(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			path, s, start := historyFixture(t, 1)
			o := historyOptions(path)
			o.offset = "end"
			o.follow = true
			o.allowPartial = partial
			o.until = start.Add(150 * time.Millisecond).Format(time.RFC3339Nano)
			// Completion runs on the 200 ms tick. Recording stops between the last
			// ordinary health check and the requested end, so final coverage is required.
			timer := time.AfterFunc(120*time.Millisecond, func() { s.EndCapture(context.Background(), "session", start.Add(120*time.Millisecond), false) })
			defer timer.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var out, diagnostics bytes.Buffer
			code := consumeHistory(ctx, o, start, &out, &diagnostics)
			expected := 6
			if partial {
				expected = 0
			}
			if code != expected || !strings.Contains(diagnostics.String(), "capture_gap") {
				t.Fatal(code, diagnostics.String())
			}
		})
	}
}
