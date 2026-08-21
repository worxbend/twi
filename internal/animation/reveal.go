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
	"github.com/worxbend/twi/internal/render"
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

// Modes lists every animation mode a config file may name, so that the code
// which validates configuration does not repeat the strings.
func Modes() []string {
	return []string{string(ModeOff), string(ModeReduced), string(ModeFast)}
}

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
	return newSequence(rows, cfg.resolve(), now)
}

// newSequence builds reveal state from an already-resolved config. Queue holds
// its config in that form, so enqueuing a message does not re-derive the same
// timings for every message that arrives.
func newSequence(rows []render.Row, cfg resolvedConfig, now time.Time) Sequence {
	sequence := Sequence{
		rows:         cloneRows(rows),
		units:        Units(rows),
		lastAdvance:  now,
		interval:     cfg.interval,
		unitsPerTick: cfg.unitsPerTick,
	}
	if cfg.mode == ModeOff || len(sequence.units) == 0 {
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
		rows[unit.Row].Append(unit.Fragment)
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
	cfg       resolvedConfig
	clock     Clock
	items     []queuedReveal
	overflows int
}

// NewQueue creates a bounded reveal queue. A nil clock uses the system clock.
func NewQueue(cfg Config, clock Clock) *Queue {
	if clock == nil {
		clock = systemClock{}
	}
	return &Queue{
		cfg:   cfg.resolve(),
		clock: clock,
	}
}

// Enqueue adds rows to the reveal queue. In off mode, the reveal completes
// immediately and does not occupy queue capacity. If the queue is full, the
// oldest queued reveal is completed with CompletionOverflow and removed before
// the new reveal is accepted.
func (q *Queue) Enqueue(id string, rows []render.Row) EnqueueResult {
	now := q.clock.Now()
	sequence := newSequence(rows, q.cfg, now)
	if sequence.Done() {
		complete := completedReveal(id, &sequence, CompletionImmediate)
		return EnqueueResult{
			Complete:  &complete,
			QueueSize: len(q.items),
		}
	}

	result := EnqueueResult{Queued: true}
	for len(q.items) >= q.cfg.maxQueued {
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
		next := newSequence(rows, q.cfg, current.lastAdvance)
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

// isAtomic reports whether a fragment is revealed as one unit rather than one
// grapheme cluster at a time.
//
// It is deliberately broader than render.Fragment.Atomic, which it builds on.
// The renderer only has to keep a fragment whole while wrapping a row, so its
// rule covers mentions, emotes, and emoji. A reveal has a second concern: chat
// chrome that reads as a single object -- the clock, a badge, an avatar chip,
// the author's name -- looks wrong typed out letter by letter, and so does any
// fragment carrying an asset reference or a style beyond a plain foreground
// color. Those all appear at once here.
func isAtomic(fragment render.Fragment) bool {
	if fragment.Atomic() {
		return true
	}
	switch fragment.Kind {
	case render.FragmentAvatar,
		render.FragmentTimestamp,
		render.FragmentBadge,
		render.FragmentUsername,
		render.FragmentReply,
		render.FragmentNotice,
		render.FragmentAction,
		render.FragmentDeleted:
		return true
	}
	return fragment.Ref != (render.Fragment{}.Ref) ||
		fragment.Style.Background != "" ||
		fragment.Style.Bold ||
		fragment.Style.Italic ||
		fragment.Style.Strikethrough
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

// resolvedConfig is a Config with every missing value filled in and the timing
// for the chosen mode already selected. It is produced once, when a Sequence or
// a Queue is built, so that reading the interval on every animation tick is a
// field access rather than a fresh round of normalization.
type resolvedConfig struct {
	mode      Mode
	maxQueued int
	// interval is how long one reveal step lasts, and unitsPerTick how many
	// units that step makes visible. Together they set the reveal speed.
	interval     time.Duration
	unitsPerTick int
}

// resolve normalizes a Config and picks the timings its mode calls for.
func (c Config) resolve() resolvedConfig {
	c = c.withDefaults()
	resolved := resolvedConfig{mode: c.Mode, maxQueued: c.MaxQueued}
	switch c.Mode {
	case ModeReduced:
		resolved.interval = c.ReducedInterval
		resolved.unitsPerTick = c.ReducedUnitsPerTick
	case ModeOff:
		// Off has no timing to speak of: a sequence built from it starts
		// fully revealed and never advances.
		resolved.interval = 0
		resolved.unitsPerTick = 1
	default:
		resolved.interval = c.FastInterval
		resolved.unitsPerTick = c.FastUnitsPerTick
	}
	return resolved
}
