package animation

import (
	"fmt"
	"reflect"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/twitch"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestUnitsRevealGraphemesWithoutInvalidUTF8(t *testing.T) {
	now := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.FastInterval = time.Millisecond
	row := render.Row{Fragments: []render.Fragment{{
		Kind: render.FragmentText,
		Text: "e\u0301a😀",
	}}}
	sequence := NewSequence([]render.Row{row}, cfg, now)

	wantFrames := [][]string{
		{""},
		{"e\u0301"},
		{"e\u0301a"},
		{"e\u0301a😀"},
	}
	if got := plainFrame(sequence.Frame()); !reflect.DeepEqual(got, wantFrames[0]) {
		t.Fatalf("initial frame = %#v, want %#v", got, wantFrames[0])
	}
	for i := 1; i < len(wantFrames); i++ {
		if !sequence.Advance(now.Add(time.Duration(i) * time.Millisecond)) {
			t.Fatalf("advance %d did not change frame", i)
		}
		got := plainFrame(sequence.Frame())
		if !reflect.DeepEqual(got, wantFrames[i]) {
			t.Fatalf("frame %d = %#v, want %#v", i, got, wantFrames[i])
		}
		for _, line := range got {
			if !utf8.ValidString(line) {
				t.Fatalf("frame %d contains invalid UTF-8: %q", i, line)
			}
		}
	}
}

func TestSemanticAndStyledFragmentsRevealAsCompleteUnits(t *testing.T) {
	now := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.FastInterval = time.Millisecond
	rows := []render.Row{{Fragments: []render.Fragment{
		{Kind: render.FragmentMention, Text: "@viewer"},
		{Kind: render.FragmentText, Text: " "},
		{Kind: render.FragmentEmoteFallback, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
		{Kind: render.FragmentEmojiFallback, Text: "👨‍👩‍👧‍👦"},
		{Kind: render.FragmentText, Text: "VIP", Style: render.FragmentStyle{Bold: true}},
	}}}
	sequence := NewSequence(rows, cfg, now)

	wantFrames := [][]string{
		{"@viewer"},
		{"@viewer "},
		{"@viewer Kappa"},
		{"@viewer Kappa👨‍👩‍👧‍👦"},
		{"@viewer Kappa👨‍👩‍👧‍👦VIP"},
	}
	for i, want := range wantFrames {
		if !sequence.Advance(now.Add(time.Duration(i+1) * time.Millisecond)) {
			t.Fatalf("advance %d did not change frame", i+1)
		}
		if got := plainFrame(sequence.Frame()); !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %d = %#v, want %#v", i+1, got, want)
		}
	}
}

func TestForegroundOnlyTextPreservesStyleWhileRevealingGraphemes(t *testing.T) {
	units := Units([]render.Row{{Fragments: []render.Fragment{{
		Kind:  render.FragmentText,
		Text:  "ab",
		Style: render.FragmentStyle{Foreground: "#ffffff"},
	}}}})

	if len(units) != 2 {
		t.Fatalf("unit count = %d, want 2 grapheme units", len(units))
	}
	for _, unit := range units {
		if got, want := unit.Fragment.Style.Foreground, "#ffffff"; got != want {
			t.Fatalf("unit foreground = %q, want %q", got, want)
		}
	}
	if got, want := units[0].Fragment.Text, "a"; got != want {
		t.Fatalf("first unit text = %q, want %q", got, want)
	}
	if got, want := units[1].Fragment.Text, "b"; got != want {
		t.Fatalf("second unit text = %q, want %q", got, want)
	}
}

func TestFixedWidthFallbackFramesDoNotCoalesce(t *testing.T) {
	now := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.FastInterval = time.Millisecond
	rows := []render.Row{{Fragments: []render.Fragment{
		{Kind: render.FragmentEmojiFallback, Text: "😀", WidthCells: 2, Ref: twitch.AssetRef{Kind: "emoji", ID: "😀"}},
		{Kind: render.FragmentEmojiFallback, Text: "😀", WidthCells: 2, Ref: twitch.AssetRef{Kind: "emoji", ID: "😀"}},
	}}}
	sequence := NewSequence(rows, cfg, now)

	sequence.Advance(now.Add(time.Millisecond))
	if got, want := plainFrame(sequence.Frame()), []string{"😀"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first fixed-width frame = %#v, want %#v", got, want)
	}
	sequence.Advance(now.Add(2 * time.Millisecond))
	frame := sequence.Frame()
	if got, want := plainFrame(frame), []string{"😀😀"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second fixed-width frame = %#v, want %#v", got, want)
	}
	if got, want := len(frame[0].Fragments), 2; got != want {
		t.Fatalf("frame fragment count = %d, want %d: %#v", got, want, frame[0].Fragments)
	}
}

func TestModesProduceDeterministicFrames(t *testing.T) {
	now := time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)
	rows := []render.Row{{Fragments: []render.Fragment{{
		Kind: render.FragmentText,
		Text: "abcd",
	}}}}
	cfg := DefaultConfig()
	cfg.FastInterval = time.Millisecond
	cfg.ReducedInterval = 2 * time.Millisecond
	cfg.ReducedUnitsPerTick = 3

	off := NewSequence(rows, Config{Mode: ModeOff}, now)
	if !off.Done() {
		t.Fatal("off mode should complete immediately")
	}
	if got, want := plainFrame(off.Frame()), []string{"abcd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("off frame = %#v, want %#v", got, want)
	}

	fast := NewSequence(rows, cfg, now)
	fast.Advance(now.Add(time.Millisecond))
	if got, want := plainFrame(fast.Frame()), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fast frame 1 = %#v, want %#v", got, want)
	}
	fast.Advance(now.Add(2 * time.Millisecond))
	if got, want := plainFrame(fast.Frame()), []string{"ab"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fast frame 2 = %#v, want %#v", got, want)
	}

	cfg.Mode = ModeReduced
	reduced := NewSequence(rows, cfg, now)
	if changed := reduced.Advance(now.Add(time.Millisecond)); changed {
		t.Fatal("reduced mode advanced before its interval")
	}
	reduced.Advance(now.Add(2 * time.Millisecond))
	if got, want := plainFrame(reduced.Frame()), []string{"abc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reduced frame = %#v, want %#v", got, want)
	}
}

func TestQueueCompletesOldestRevealOnOverflow(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	cfg := DefaultConfig()
	cfg.MaxQueued = 2
	cfg.FastInterval = time.Second
	queue := NewQueue(cfg, clock)

	if result := queue.Enqueue("one", textRows("one")); !result.Queued || result.QueueSize != 1 {
		t.Fatalf("enqueue one = %#v", result)
	}
	if result := queue.Enqueue("two", textRows("two")); !result.Queued || result.QueueSize != 2 {
		t.Fatalf("enqueue two = %#v", result)
	}
	result := queue.Enqueue("three", textRows("three"))
	if !result.Queued || result.QueueSize != 2 {
		t.Fatalf("enqueue three = %#v, want queued size 2", result)
	}
	if queue.Len() != 2 {
		t.Fatalf("queue len = %d, want 2", queue.Len())
	}
	if queue.OverflowCount() != 1 {
		t.Fatalf("overflow count = %d, want 1", queue.OverflowCount())
	}
	if len(result.Overflow) != 1 {
		t.Fatalf("overflowed reveals = %d, want 1", len(result.Overflow))
	}
	overflowed := result.Overflow[0]
	if overflowed.ID != "one" || overflowed.Reason != CompletionOverflow {
		t.Fatalf("overflow = %#v, want oldest reveal completed by overflow", overflowed)
	}
	if got, want := plainFrame(overflowed.Rows), []string{"one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overflow frame = %#v, want %#v", got, want)
	}
	if _, ok := queue.Frames()["one"]; ok {
		t.Fatal("overflowed reveal should not remain queued")
	}
}

func TestQueueBurstOverflowIsDeterministicAndBounded(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	cfg := DefaultConfig()
	cfg.MaxQueued = 3
	cfg.FastInterval = time.Hour
	queue := NewQueue(cfg, clock)

	overflowed := make([]string, 0)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("message-%02d", i)
		result := queue.Enqueue(id, textRows(id))
		if !result.Queued {
			t.Fatalf("enqueue %s was not queued: %#v", id, result)
		}
		if result.QueueSize > cfg.MaxQueued {
			t.Fatalf("enqueue %s queue size = %d, want <= %d", id, result.QueueSize, cfg.MaxQueued)
		}
		for _, reveal := range result.Overflow {
			overflowed = append(overflowed, reveal.ID)
			if reveal.Reason != CompletionOverflow {
				t.Fatalf("overflow reason for %s = %q, want %q", reveal.ID, reveal.Reason, CompletionOverflow)
			}
			if got, want := plainFrame(reveal.Rows), []string{reveal.ID}; !reflect.DeepEqual(got, want) {
				t.Fatalf("overflow rows for %s = %#v, want %#v", reveal.ID, got, want)
			}
		}
	}

	wantOverflowed := []string{
		"message-00",
		"message-01",
		"message-02",
		"message-03",
		"message-04",
		"message-05",
		"message-06",
	}
	if !reflect.DeepEqual(overflowed, wantOverflowed) {
		t.Fatalf("overflowed IDs = %#v, want %#v", overflowed, wantOverflowed)
	}
	if got, want := queue.Len(), cfg.MaxQueued; got != want {
		t.Fatalf("queue len = %d, want %d", got, want)
	}
	if got, want := queue.OverflowCount(), len(wantOverflowed); got != want {
		t.Fatalf("overflow count = %d, want %d", got, want)
	}
	frames := queue.Frames()
	for _, id := range []string{"message-07", "message-08", "message-09"} {
		if _, ok := frames[id]; !ok {
			t.Fatalf("active frames missing %s; got %#v", id, frames)
		}
	}
}

func TestQueueUsesFakeClockForCompletion(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)}
	cfg := DefaultConfig()
	cfg.FastInterval = time.Millisecond
	queue := NewQueue(cfg, clock)
	queue.Enqueue("message", textRows("ab"))

	if result := queue.Advance(); result.Changed {
		t.Fatalf("advance without time changed frame: %#v", result)
	}
	clock.Add(time.Millisecond)
	if result := queue.Advance(); !result.Changed || len(result.Completed) != 0 {
		t.Fatalf("first advance = %#v, want changed incomplete", result)
	}
	clock.Add(time.Millisecond)
	result := queue.Advance()
	if !result.Changed || len(result.Completed) != 1 || result.Completed[0].Reason != CompletionFinished {
		t.Fatalf("second advance = %#v, want completed reveal", result)
	}
	if got, want := plainFrame(result.Completed[0].Rows), []string{"ab"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completed frame = %#v, want %#v", got, want)
	}
}

func textRows(text string) []render.Row {
	return []render.Row{{Fragments: []render.Fragment{{
		Kind: render.FragmentText,
		Text: text,
	}}}}
}

func plainFrame(rows []render.Row) []string {
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain())
	}
	return plain
}
