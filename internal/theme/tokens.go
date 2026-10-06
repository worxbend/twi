package theme

// tokens.go derives the semantic design-token layer every component styles
// itself from. A Palette is the user's nine raw colors; Tokens is the larger,
// opinionated vocabulary the UI actually speaks -- raised surfaces, tracks,
// faint separators, readable on-color pairs -- computed from those nine so
// all of the built-in presets (and any custom theme) restyle every component
// at once instead of each widget naming palette fields directly.
//
// Every derivation goes through the palette package's own color math (Darken,
// Mix, ContrastCorrectedForeground), which all share the same failure rule:
// an invalid hex value degrades the decoration, never the UI. Custom themes
// with blank or malformed entries therefore still render, just quieter.

// Kind names the semantic states a badge, chip, or status indicator can
// carry. Components ask for a Kind, never a raw color, so the meaning of a
// surface ("this failed", "this is live") survives a theme switch.
type Kind int

const (
	KindNeutral Kind = iota
	KindAccent
	KindSuccess
	KindWarning
	KindError
	// KindInfo is informational emphasis. It deliberately rides the accent
	// hue: a nine-color palette has no separate info slot, and accent is the
	// color every preset already reserves for "look here".
	KindInfo
)

// Tokens is the semantic vocabulary components style from. Nothing in the
// app package should name a Palette field for a layered or blended surface;
// it should ask for the matching token instead.
type Tokens struct {
	// Canvas is the app's deepest background, behind every pane.
	Canvas string
	// Surface is the standard panel fill (the palette's own Surface).
	Surface string
	// SurfaceHi is the raised fill for overlays, selected rows, and chips:
	// visibly above Surface without introducing a new hue.
	SurfaceHi string
	// Track is the resting fill for meter troughs and quiet chrome strips
	// (tab bar, status line), sitting between Canvas and Surface.
	Track string

	// Text and Muted are the palette's own foreground roles, re-anchored so
	// components need one import.
	Text  string
	Muted string
	// Faint is the tertiary role: separators, track glyphs, hint text that
	// should recede further than Muted.
	Faint string

	// Accent is the palette accent; AccentSoft is that accent washed into
	// Surface for selection rows and soft chips; OnAccent is guaranteed-
	// readable text on Accent itself.
	Accent     string
	AccentSoft string
	OnAccent   string

	// Border is the palette border; BorderSoft is it dimmed toward the
	// background for quiet frames and dividers.
	Border     string
	BorderSoft string

	// Semantic state colors and their readable on-color partners for solid
	// badges. Info mirrors Accent by design (see KindInfo).
	Success   string
	Warning   string
	Error     string
	Info      string
	OnSuccess string
	OnWarning string
	OnError   string
	OnInfo    string

	// Selection is the background an active/highlighted row in a list gets:
	// present and themed, but never louder than the content it sits behind.
	Selection string
}

// For derives the full token set from a palette.
func For(p Palette) Tokens {
	canvas := Darken(p.Background, canvasDarken)
	return Tokens{
		Canvas:    canvas,
		Surface:   p.Surface,
		SurfaceHi: Mix(p.Surface, p.Foreground, 0.07),
		Track:     Mix(canvas, p.Surface, 0.55),

		Text:  p.Foreground,
		Muted: p.Muted,
		Faint: Mix(p.Muted, p.Background, 0.45),

		Accent:     p.Accent,
		AccentSoft: Mix(p.Surface, p.Accent, 0.25),
		OnAccent:   ContrastCorrectedForeground(p.Foreground, p.Accent, p.Background),

		Border:     p.Border,
		BorderSoft: Mix(p.Border, p.Background, 0.40),

		Success:   p.Success,
		Warning:   p.Warning,
		Error:     p.Error,
		Info:      p.Accent,
		OnSuccess: ContrastCorrectedForeground(p.Foreground, p.Success, p.Background),
		OnWarning: ContrastCorrectedForeground(p.Foreground, p.Warning, p.Background),
		OnError:   ContrastCorrectedForeground(p.Foreground, p.Error, p.Background),
		OnInfo:    ContrastCorrectedForeground(p.Foreground, p.Accent, p.Background),

		Selection: Mix(p.Surface, p.Accent, 0.25),
	}
}

// canvasDarken is the single canvas depth, shared with the app package's
// historical canvasBackground() so both compute the same value.
const canvasDarken = 0.14

// Color returns the Kind's hue. KindNeutral and KindInfo resolve to Muted and
// Accent respectively, so a component can map every state through one call.
func (t Tokens) Color(kind Kind) string {
	switch kind {
	case KindAccent, KindInfo:
		return t.Accent
	case KindSuccess:
		return t.Success
	case KindWarning:
		return t.Warning
	case KindError:
		return t.Error
	default:
		return t.Muted
	}
}

// OnColor returns text guaranteed readable on the Kind's hue as a solid
// badge background.
func (t Tokens) OnColor(kind Kind) string {
	switch kind {
	case KindAccent, KindInfo:
		return t.OnAccent
	case KindSuccess:
		return t.OnSuccess
	case KindWarning:
		return t.OnWarning
	case KindError:
		return t.OnError
	default:
		return t.Text
	}
}

// SoftBadge returns the (foreground, background) pair for a quiet badge: the
// kind's hue as text on a surface tinted with that hue. It is the resting
// state of a status indicator; the solid Color/OnColor pair is the emphatic
// one.
func (t Tokens) SoftBadge(kind Kind) (foreground, background string) {
	color := t.Color(kind)
	return color, Mix(t.Surface, color, 0.18)
}
