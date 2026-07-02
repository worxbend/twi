// Package animation provides deterministic, grapheme-safe reveal state for
// rendered chat rows.
//
// Reveal operates on internal/render fragments instead of raw strings. Normal
// text, including foreground-colored text, is split by Unicode grapheme
// clusters while preserving style metadata on each unit. Semantic tokens such
// as mentions, emoji fallbacks, emote fallbacks, metadata, and fragments with
// style modifiers or asset references reveal as complete units. The queue is
// bounded; when it overflows, the oldest queued reveal is completed immediately
// and returned to the caller so it can be rendered statically instead of being
// dropped.
package animation

import (
	"time"

	"github.com/rivo/uniseg"
	"github.com/w0rxbend/twi/internal/render"
)

const (
	defaultMaxQueued           = 32
	defaultFastInterval        = 20 * time.Millisecond
	defaultReducedInterval     = 80 * time.Millisecond
	defaultFastUnitsPerTick    = 1
	defaultReducedUnitsPerTick = 4
)

// Mode controls how much reveal motion is applied.
type Mode string

const (
	// ModeOff renders rows fully without queuing or ticking.
	ModeOff Mode = "off"
	// ModeReduced reveals multiple units per slower tick for fewer frames.
	ModeReduced Mode = "reduced"
	// ModeFast reveals one unit per short tick.
	ModeFast Mode = "fast"
)

// Clock provides deterministic time for queues and tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

// Config controls reveal timing and queue bounds.
type Config struct {
	Mode                Mode
	MaxQueued           int
	FastInterval        time.Duration
	ReducedInterval     time.Duration
	FastUnitsPerTick    int
	ReducedUnitsPerTick int
}

// DefaultConfig returns the default fast reveal behavior.
func DefaultConfig() Config {
	return Config{
		Mode:                ModeFast,
		MaxQueued:           defaultMaxQueued,
		FastInterval:        defaultFastInterval,
		ReducedInterval:     defaultReducedInterval,
		FastUnitsPerTick:    defaultFastUnitsPerTick,
		ReducedUnitsPerTick: defaultReducedUnitsPerTick,
	}
}

// RevealUnit is the smallest visible step of a reveal animation.
type RevealUnit struct {
	Row      int
	Fragment render.Fragment
}

// Units converts rendered rows into reveal units.
func Units(rows []render.Row) []RevealUnit {
	units := make([]RevealUnit, 0)
	for rowIndex, row := range rows {
		for _, fragment := range row.Fragments {
			units = append(units, fragmentUnits(rowIndex, fragment)...)
		}
	}
	return units
}

// Sequence tracks deterministic reveal progress for a set of rendered rows.
type Sequence struct {
	rows         []render.Row
	units        []RevealUnit
	visibleUnits int
	lastAdvance  time.Time
	interval     time.Duration
	unitsPerTick int
}

// NewSequence creates reveal state for rows at now.
func NewSequence(rows []render.Row, cfg Config, now time.Time) Sequence {
	cfg = cfg.withDefaults()
	sequence := Sequence{
		rows:         cloneRows(rows),
		units:        Units(rows),
		lastAdvance:  now,
		interval:     cfg.interval(),
		unitsPerTick: cfg.unitsPerTick(),
	}
	if cfg.mode() == ModeOff || len(sequence.units) == 0 {
		sequence.visibleUnits = len(sequence.units)
	}
	return sequence
}

// Done reports whether every reveal unit is visible.
func (s Sequence) Done() bool {
	return s.visibleUnits >= len(s.units)
}

// Advance moves the reveal forward according to now and returns true when the
// visible frame changed.
func (s *Sequence) Advance(now time.Time) bool {
	if s.Done() {
		return false
	}
	if s.interval <= 0 || s.unitsPerTick <= 0 {
		s.Complete()
		return true
	}
	if now.Before(s.lastAdvance.Add(s.interval)) {
		return false
	}

	ticks := int(now.Sub(s.lastAdvance) / s.interval)
	s.lastAdvance = s.lastAdvance.Add(time.Duration(ticks) * s.interval)
	s.visibleUnits += ticks * s.unitsPerTick
	if s.visibleUnits > len(s.units) {
		s.visibleUnits = len(s.units)
	}
	return true
}

// Complete makes every reveal unit visible immediately.
func (s *Sequence) Complete() {
	s.visibleUnits = len(s.units)
}

// Frame returns rows with only the currently visible reveal units populated.
func (s Sequence) Frame() []render.Row {
	if len(s.rows) == 0 {
		return nil
	}
	if s.Done() {
		return cloneRows(s.rows)
	}

	rows := make([]render.Row, len(s.rows))
	for i := 0; i < s.visibleUnits; i++ {
		unit := s.units[i]
		if unit.Row < 0 || unit.Row >= len(rows) {
			continue
		}
		appendFragment(&rows[unit.Row], unit.Fragment)
	}
	return rows
}

// CompletionReason explains why a reveal left the queue.
type CompletionReason string

const (
	CompletionImmediate CompletionReason = "immediate"
	CompletionFinished  CompletionReason = "finished"
	CompletionOverflow  CompletionReason = "overflow"
)

// CompletedReveal is returned when a reveal should be rendered statically.
type CompletedReveal struct {
	ID     string
	Rows   []render.Row
	Reason CompletionReason
}

// EnqueueResult describes the effect of adding a reveal to a queue.
type EnqueueResult struct {
	Queued    bool
	Complete  *CompletedReveal
	Overflow  []CompletedReveal
	QueueSize int
}

// AdvanceResult describes reveals completed during a queue tick.
type AdvanceResult struct {
	Changed   bool
	Completed []CompletedReveal
	QueueSize int
}

type queuedReveal struct {
	id       string
	sequence Sequence
}

// Queue holds a bounded set of active reveals.
type Queue struct {
	cfg       Config
	clock     Clock
	items     []queuedReveal
	overflows int
}

// NewQueue creates a bounded reveal queue. A nil clock uses the system clock.
func NewQueue(cfg Config, clock Clock) *Queue {
	cfg = cfg.withDefaults()
	if clock == nil {
		clock = systemClock{}
	}
	return &Queue{
		cfg:   cfg,
		clock: clock,
	}
}

// Enqueue adds rows to the reveal queue. In off mode, the reveal completes
// immediately and does not occupy queue capacity. If the queue is full, the
// oldest queued reveal is completed with CompletionOverflow and removed before
// the new reveal is accepted.
func (q *Queue) Enqueue(id string, rows []render.Row) EnqueueResult {
	now := q.clock.Now()
	sequence := NewSequence(rows, q.cfg, now)
	if sequence.Done() {
		complete := completedReveal(id, &sequence, CompletionImmediate)
		return EnqueueResult{
			Complete:  &complete,
			QueueSize: len(q.items),
		}
	}

	result := EnqueueResult{Queued: true}
	for len(q.items) >= q.cfg.MaxQueued {
		oldest := q.items[0]
		q.items = q.items[1:]
		oldest.sequence.Complete()
		result.Overflow = append(result.Overflow, completedReveal(oldest.id, &oldest.sequence, CompletionOverflow))
		q.overflows++
	}
	q.items = append(q.items, queuedReveal{id: id, sequence: sequence})
	result.QueueSize = len(q.items)
	return result
}

// Advance moves every queued reveal according to the queue clock and removes
// any reveals that completed.
func (q *Queue) Advance() AdvanceResult {
	now := q.clock.Now()
	result := AdvanceResult{}
	active := q.items[:0]
	for i := range q.items {
		item := q.items[i]
		if item.sequence.Advance(now) {
			result.Changed = true
		}
		if item.sequence.Done() {
			result.Completed = append(result.Completed, completedReveal(item.id, &item.sequence, CompletionFinished))
			continue
		}
		active = append(active, item)
	}
	q.items = active
	result.QueueSize = len(q.items)
	return result
}

// Len returns the number of active queued reveals.
func (q *Queue) Len() int {
	return len(q.items)
}

// OverflowCount returns the total number of oldest reveals completed because
// the bounded queue was full.
func (q *Queue) OverflowCount() int {
	return q.overflows
}

// Frames returns the current partial frames for queued reveals by ID.
func (q *Queue) Frames() map[string][]render.Row {
	frames := make(map[string][]render.Row, len(q.items))
	for _, item := range q.items {
		frames[item.id] = item.sequence.Frame()
	}
	return frames
}

// ReplaceRows updates the rendered rows for an active reveal while preserving
// visible progress. It is intended for layout-stable asset updates, such as
// prepared image cells replacing fixed-width text fallbacks.
func (q *Queue) ReplaceRows(id string, rows []render.Row) bool {
	for i := range q.items {
		if q.items[i].id != id {
			continue
		}
		current := q.items[i].sequence
		next := NewSequence(rows, q.cfg, current.lastAdvance)
		next.visibleUnits = current.visibleUnits
		if next.visibleUnits > len(next.units) {
			next.visibleUnits = len(next.units)
		}
		q.items[i].sequence = next
		return true
	}
	return false
}

func completedReveal(id string, sequence *Sequence, reason CompletionReason) CompletedReveal {
	sequence.Complete()
	return CompletedReveal{
		ID:     id,
		Rows:   sequence.Frame(),
		Reason: reason,
	}
}

func fragmentUnits(row int, fragment render.Fragment) []RevealUnit {
	if fragment.Text == "" {
		return nil
	}
	if isAtomic(fragment) {
		return []RevealUnit{{Row: row, Fragment: cloneFragment(fragment)}}
	}

	graphemes := uniseg.NewGraphemes(fragment.Text)
	units := make([]RevealUnit, 0)
	for graphemes.Next() {
		next := cloneFragment(fragment)
		next.Text = graphemes.Str()
		units = append(units, RevealUnit{Row: row, Fragment: next})
	}
	return units
}

func isAtomic(fragment render.Fragment) bool {
	switch fragment.Kind {
	case render.FragmentAvatar,
		render.FragmentTimestamp,
		render.FragmentBadge,
		render.FragmentUsername,
		render.FragmentMention,
		render.FragmentReply,
		render.FragmentNotice,
		render.FragmentAction,
		render.FragmentDeleted,
		render.FragmentEmojiFallback,
		render.FragmentEmoteFallback:
		return true
	}
	return fragment.Ref != (render.Fragment{}.Ref) ||
		fragment.Style.Background != "" ||
		fragment.Style.Bold ||
		fragment.Style.Italic ||
		fragment.Style.Strikethrough
}

func appendFragment(row *render.Row, fragment render.Fragment) {
	if fragment.Text == "" {
		return
	}
	last := len(row.Fragments) - 1
	if last >= 0 && sameFragment(row.Fragments[last], fragment) {
		row.Fragments[last].Text += fragment.Text
		return
	}
	row.Fragments = append(row.Fragments, cloneFragment(fragment))
}

func sameFragment(a, b render.Fragment) bool {
	if a.WidthCells > 0 || b.WidthCells > 0 {
		return false
	}
	return a.Kind == b.Kind &&
		a.Style == b.Style &&
		a.Ref == b.Ref &&
		a.WidthCells == b.WidthCells
}

func cloneRows(rows []render.Row) []render.Row {
	if len(rows) == 0 {
		return nil
	}
	out := make([]render.Row, len(rows))
	for i, row := range rows {
		out[i].Fragments = cloneFragments(row.Fragments)
	}
	return out
}

func cloneFragments(fragments []render.Fragment) []render.Fragment {
	if len(fragments) == 0 {
		return nil
	}
	out := make([]render.Fragment, len(fragments))
	copy(out, fragments)
	return out
}

func cloneFragment(fragment render.Fragment) render.Fragment {
	return fragment
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.Mode == "" {
		c.Mode = defaults.Mode
	}
	if c.Mode != ModeOff && c.Mode != ModeReduced && c.Mode != ModeFast {
		c.Mode = defaults.Mode
	}
	if c.MaxQueued <= 0 {
		c.MaxQueued = defaults.MaxQueued
	}
	if c.FastInterval <= 0 {
		c.FastInterval = defaults.FastInterval
	}
	if c.ReducedInterval <= 0 {
		c.ReducedInterval = defaults.ReducedInterval
	}
	if c.FastUnitsPerTick <= 0 {
		c.FastUnitsPerTick = defaults.FastUnitsPerTick
	}
	if c.ReducedUnitsPerTick <= 0 {
		c.ReducedUnitsPerTick = defaults.ReducedUnitsPerTick
	}
	return c
}

func (c Config) mode() Mode {
	return c.withDefaults().Mode
}

func (c Config) interval() time.Duration {
	c = c.withDefaults()
	switch c.Mode {
	case ModeReduced:
		return c.ReducedInterval
	case ModeOff:
		return 0
	default:
		return c.FastInterval
	}
}

func (c Config) unitsPerTick() int {
	c = c.withDefaults()
	switch c.Mode {
	case ModeReduced:
		return c.ReducedUnitsPerTick
	case ModeOff:
		return 1
	default:
		return c.FastUnitsPerTick
	}
}
