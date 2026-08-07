package animation

import (
	"strings"
	"time"

	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
)

// TextEffect names an animated treatment for a short chrome label such as the
// splash tagline or an empty-state headline. Effects are deliberately not
// available for chat content: chat rows animate through Sequence, which keeps
// message text readable and scroll-stable.
type TextEffect string

const (
	// EffectNone renders the label statically in its base color.
	EffectNone TextEffect = "none"
	// EffectTypewriter reveals one grapheme cluster per step behind a caret.
	EffectTypewriter TextEffect = "typewriter"
	// EffectGradientWave rotates a seamless base-to-accent gradient through
	// the label.
	EffectGradientWave TextEffect = "gradient-wave"
	// EffectShimmer sweeps a narrow accent highlight across the label and
	// then rests before repeating.
	EffectShimmer TextEffect = "shimmer"
	// EffectBounce moves the label back and forth across a fixed track,
	// leaving a fading trail behind it.
	EffectBounce TextEffect = "bounce"
)

// DefaultCursor is the typewriter caret. It must be a single-cell glyph so
// the caret can stand in for an unrevealed cluster without changing width.
const DefaultCursor = "▌"

const (
	shimmerBand = 3
	shimmerRest = 6
	bounceTrail = 3
)

// TextCell is one styled run of an animated label. Cells carry resolved
// colors rather than terminal escapes so this package stays free of terminal
// I/O; internal/app owns lipgloss styling and background composition.
type TextCell struct {
	Text       string
	Foreground string
	Bold       bool
}

// TextConfig controls one animated label.
//
// Every effect preserves the source label's display width on every frame,
// including before a typewriter has revealed anything, so an animated label
// never reflows the surface around it.
type TextConfig struct {
	Effect TextEffect
	// Mode follows the app-wide animation setting. ModeOff renders the
	// static frame, ModeReduced slows the default step down.
	Mode Mode
	// Base is the resting foreground and the near end of gradients.
	Base string
	// Accent is the far gradient end, the shimmer highlight, and the caret
	// color.
	Accent string
	// Trail is the faded color bounce ghosts blend toward. It defaults to
	// Base, which renders the trail as a plain motion blur.
	Trail string
	// Cursor overrides DefaultCursor. It should be one cell wide.
	Cursor string
	// Step is the per-frame advance: one revealed cluster, one gradient
	// column, or one bounce column. Zero picks the effect and mode default.
	Step time.Duration
	// Width is the bounce track. Widths at or below the label's own width
	// leave the label stationary.
	Width int
	// Offset shifts the starting phase of the continuous effects by whole
	// columns, so callers can stagger several labels or art rows without
	// giving each one its own clock.
	Offset int
	Bold   bool
}

// FrameElapsed converts a shared frame-clock timestamp into the elapsed value
// the continuous effects expect. Wave, shimmer, and bounce are periodic, so
// any fixed epoch yields a stable phase; deriving it from the tick keeps View
// a pure function of already-ticked model state instead of the wall clock.
// The zero time (animation off, or before the first tick) yields the static
// first frame.
func FrameElapsed(now time.Time) time.Duration {
	if now.IsZero() {
		return 0
	}
	return time.Duration(now.UnixNano())
}

// TextFrame renders text under cfg at elapsed since the effect started.
// Negative elapsed is a not-yet-started typewriter, which renders as blank
// cells of the right width; the continuous effects treat it as zero.
func TextFrame(text string, cfg TextConfig, elapsed time.Duration) []TextCell {
	cfg = cfg.withTextDefaults()
	units := textClusters(text)
	if len(units) == 0 {
		return nil
	}
	if cfg.Mode == ModeOff || cfg.Step <= 0 {
		return mergeTextCells(staticTextCells(units, cfg))
	}

	switch cfg.Effect {
	case EffectTypewriter:
		return mergeTextCells(typewriterCells(units, cfg, elapsed))
	case EffectGradientWave:
		return mergeTextCells(gradientWaveCells(units, cfg, elapsed))
	case EffectShimmer:
		return mergeTextCells(shimmerCells(units, cfg, elapsed))
	case EffectBounce:
		return mergeTextCells(bounceCells(units, cfg, elapsed))
	default:
		return mergeTextCells(staticTextCells(units, cfg))
	}
}

// TextDone reports whether the effect has settled into its final frame. Only
// the typewriter settles; the continuous effects run for as long as animation
// is enabled, so callers that stop work when an effect finishes should only
// do so for one-shot effects.
func TextDone(text string, cfg TextConfig, elapsed time.Duration) bool {
	cfg = cfg.withTextDefaults()
	if cfg.Mode == ModeOff || cfg.Step <= 0 || cfg.Effect == EffectNone {
		return true
	}
	if cfg.Effect != EffectTypewriter {
		return false
	}
	total := len(textClusters(text))
	return revealedClusters(total, cfg, elapsed) >= total
}

// TextPlain returns the unstyled text of a frame, which is what the label
// occupies on screen.
func TextPlain(cells []TextCell) string {
	var builder strings.Builder
	for _, cell := range cells {
		builder.WriteString(cell.Text)
	}
	return builder.String()
}

// TextWidth returns the display width of a frame in terminal cells.
func TextWidth(cells []TextCell) int {
	return uniseg.StringWidth(TextPlain(cells))
}

type textCluster struct {
	text  string
	width int
}

func textClusters(text string) []textCluster {
	if text == "" {
		return nil
	}
	units := make([]textCluster, 0, len(text))
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		cluster := graphemes.Str()
		units = append(units, textCluster{text: cluster, width: uniseg.StringWidth(cluster)})
	}
	return units
}

func clustersWidth(units []textCluster) int {
	width := 0
	for _, unit := range units {
		width += unit.width
	}
	return width
}

func staticTextCells(units []textCluster, cfg TextConfig) []TextCell {
	cells := make([]TextCell, 0, len(units))
	for _, unit := range units {
		cells = append(cells, TextCell{Text: unit.text, Foreground: cfg.Base, Bold: cfg.Bold})
	}
	return cells
}

func revealedClusters(total int, cfg TextConfig, elapsed time.Duration) int {
	if elapsed <= 0 || cfg.Step <= 0 {
		return 0
	}
	return min(int(elapsed/cfg.Step), total)
}

// typewriterCells reveals the label from the left and pads the rest with
// blanks. Padding rather than truncating is what keeps a centered tagline
// from sliding sideways as it types.
func typewriterCells(units []textCluster, cfg TextConfig, elapsed time.Duration) []TextCell {
	revealed := revealedClusters(len(units), cfg, elapsed)
	caret := elapsed >= 0 && revealed < len(units) && cursorVisible(cfg, elapsed)
	cells := make([]TextCell, 0, len(units))
	for index, unit := range units {
		switch {
		case index < revealed:
			cells = append(cells, TextCell{Text: unit.text, Foreground: cfg.Base, Bold: cfg.Bold})
		case index == revealed && caret:
			cells = append(cells, cursorCell(unit.width, cfg))
		default:
			cells = append(cells, TextCell{Text: strings.Repeat(" ", unit.width), Foreground: cfg.Base})
		}
	}
	return cells
}

// cursorVisible blinks the caret on a multiple of the reveal step so the
// blink rate scales with typing speed instead of needing its own timer.
func cursorVisible(cfg TextConfig, elapsed time.Duration) bool {
	return (elapsed/(cfg.Step*4))%2 == 0
}

func cursorCell(width int, cfg TextConfig) TextCell {
	text := cfg.Cursor
	if pad := width - uniseg.StringWidth(cfg.Cursor); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return TextCell{Text: text, Foreground: cfg.Accent, Bold: true}
}

func gradientWaveCells(units []textCluster, cfg TextConfig, elapsed time.Duration) []TextCell {
	colors := theme.SeamlessGradient(cfg.Base, cfg.Accent, clustersWidth(units))
	if len(colors) == 0 {
		return staticTextCells(units, cfg)
	}
	phase := phaseColumns(elapsed, cfg)
	cells := make([]TextCell, 0, len(units))
	column := 0
	for _, unit := range units {
		cells = append(cells, TextCell{
			Text:       unit.text,
			Foreground: colors[wrapIndex(column+phase, len(colors))],
			Bold:       cfg.Bold,
		})
		column += unit.width
	}
	return cells
}

// shimmerCells sweeps a highlight head across the label and keeps travelling
// past the end for shimmerRest columns, which reads as a pause between passes
// rather than a strobe.
func shimmerCells(units []textCluster, cfg TextConfig, elapsed time.Duration) []TextCell {
	width := clustersWidth(units)
	head := wrapIndex(phaseColumns(elapsed, cfg), width+shimmerRest)
	cells := make([]TextCell, 0, len(units))
	column := 0
	for _, unit := range units {
		distance := column - head
		if distance < 0 {
			distance = -distance
		}
		cell := TextCell{Text: unit.text, Foreground: cfg.Base, Bold: cfg.Bold}
		if distance <= shimmerBand {
			intensity := 1 - float64(distance)/float64(shimmerBand+1)
			cell.Foreground = theme.Mix(cfg.Base, cfg.Accent, intensity)
			cell.Bold = cfg.Bold || distance == 0
		}
		cells = append(cells, cell)
		column += unit.width
	}
	return cells
}

// bounceCells draws the label at its current position on the track, preceded
// by progressively fainter ghosts of the positions it just left. Ghosts are
// drawn before the label so the label always wins any overlap.
func bounceCells(units []textCluster, cfg TextConfig, elapsed time.Duration) []TextCell {
	width := clustersWidth(units)
	track := max(cfg.Width, width)
	travel := track - width
	if travel <= 0 {
		return staticTextCells(units, cfg)
	}

	position, direction := pingPong(phaseColumns(elapsed, cfg), travel)
	row := newColumnTrack(track, cfg.Base)
	for ghost := bounceTrail; ghost >= 1; ghost-- {
		at := position - direction*ghost
		if at < 0 || at > travel {
			continue
		}
		intensity := float64(bounceTrail-ghost+1) / float64(bounceTrail+1)
		row.draw(at, units, theme.Mix(cfg.Trail, cfg.Base, intensity), false)
	}
	row.draw(position, units, cfg.Base, cfg.Bold)
	return row.cells()
}

// pingPong maps a monotonic step count onto a position in [0,travel] that
// reverses at each end, plus the direction it is currently moving.
func pingPong(steps, travel int) (int, int) {
	if travel <= 0 {
		return 0, 1
	}
	position := wrapIndex(steps, travel*2)
	if position <= travel {
		return position, 1
	}
	return travel*2 - position, -1
}

// columnTrack is a fixed-width row of terminal cells. Wide clusters occupy
// their first column and mark the rest as continuations, so overlapping draws
// can never widen or narrow the rendered track.
type columnTrack struct {
	columns []TextCell
	filled  []bool
	base    string
}

func newColumnTrack(width int, base string) *columnTrack {
	track := &columnTrack{
		columns: make([]TextCell, width),
		filled:  make([]bool, width),
		base:    base,
	}
	for i := range track.columns {
		track.columns[i] = TextCell{Text: " ", Foreground: base}
	}
	return track
}

func (t *columnTrack) draw(column int, units []textCluster, foreground string, bold bool) {
	for _, unit := range units {
		if column >= len(t.columns) {
			return
		}
		if column >= 0 {
			t.clearContinuation(column)
			t.columns[column] = TextCell{Text: unit.text, Foreground: foreground, Bold: bold}
			t.filled[column] = false
			for offset := 1; offset < unit.width && column+offset < len(t.columns); offset++ {
				t.clearContinuation(column + offset)
				t.columns[column+offset] = TextCell{}
				t.filled[column+offset] = true
			}
		}
		column += unit.width
	}
}

// clearContinuation replaces the wide cluster that owns column with blanks so
// a partially overwritten cluster cannot leave a dangling extra cell.
func (t *columnTrack) clearContinuation(column int) {
	if !t.filled[column] {
		return
	}
	for owner := column - 1; owner >= 0; owner-- {
		if t.filled[owner] {
			continue
		}
		width := uniseg.StringWidth(t.columns[owner].Text)
		for offset := 0; offset < width && owner+offset < len(t.columns); offset++ {
			t.columns[owner+offset] = TextCell{Text: " ", Foreground: t.base}
			t.filled[owner+offset] = false
		}
		return
	}
}

func (t *columnTrack) cells() []TextCell {
	cells := make([]TextCell, 0, len(t.columns))
	for index, cell := range t.columns {
		if t.filled[index] {
			continue
		}
		cells = append(cells, cell)
	}
	return cells
}

func phaseColumns(elapsed time.Duration, cfg TextConfig) int {
	if cfg.Step <= 0 {
		return cfg.Offset
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return int(elapsed/cfg.Step) + cfg.Offset
}

func wrapIndex(value, length int) int {
	if length <= 0 {
		return 0
	}
	value %= length
	if value < 0 {
		value += length
	}
	return value
}

// mergeTextCells joins neighboring cells that share styling so a frame emits
// one escape sequence per visual run instead of one per grapheme cluster.
func mergeTextCells(cells []TextCell) []TextCell {
	merged := make([]TextCell, 0, len(cells))
	for _, cell := range cells {
		if cell.Text == "" {
			continue
		}
		last := len(merged) - 1
		if last >= 0 && merged[last].Foreground == cell.Foreground && merged[last].Bold == cell.Bold {
			merged[last].Text += cell.Text
			continue
		}
		merged = append(merged, cell)
	}
	return merged
}

func (c TextConfig) withTextDefaults() TextConfig {
	if c.Effect == "" {
		c.Effect = EffectNone
	}
	switch c.Mode {
	case ModeOff, ModeReduced, ModeFast:
	default:
		c.Mode = ModeFast
	}
	if c.Cursor == "" {
		c.Cursor = DefaultCursor
	}
	if c.Trail == "" {
		c.Trail = c.Base
	}
	if c.Step <= 0 {
		c.Step = defaultTextStep(c.Effect, c.Mode)
	}
	return c
}

// defaultTextStep keeps every effect readable at the shared frame clock's
// rate. Reduced mode roughly halves the motion instead of disabling it, which
// matches how the reveal queue treats the same setting.
func defaultTextStep(effect TextEffect, mode Mode) time.Duration {
	reduced := mode == ModeReduced
	switch effect {
	case EffectTypewriter:
		if reduced {
			return 90 * time.Millisecond
		}
		return 45 * time.Millisecond
	case EffectGradientWave:
		if reduced {
			return 200 * time.Millisecond
		}
		return 100 * time.Millisecond
	case EffectShimmer:
		if reduced {
			return 160 * time.Millisecond
		}
		return 80 * time.Millisecond
	case EffectBounce:
		if reduced {
			return 180 * time.Millisecond
		}
		return 90 * time.Millisecond
	default:
		return 0
	}
}
