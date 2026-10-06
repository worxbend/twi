# TUI design system

`twi`'s terminal UI is styled from a small, semantic design system rather
than ad-hoc color choices. This document is the map: what the tokens are,
what components exist, and the rules every surface follows.

## Token layer (`internal/theme`)

A `Palette` is the user's nine raw colors (built-in preset or `custom` in
`config.toml`). `theme.For(palette)` derives the larger `Tokens` vocabulary
every component styles from:

| Token | Role |
| --- | --- |
| `Canvas` | Deepest app background, behind every pane. |
| `Surface` | Standard panel fill. |
| `SurfaceHi` | Raised fill: overlay headers, keycaps, quiet badges. |
| `Track` | Resting chrome: tab-strip background, meter troughs. |
| `Text` / `Muted` / `Faint` | Primary, secondary, and tertiary text. |
| `Accent` / `AccentSoft` / `OnAccent` | Accent hue, its washed-into-surface selection fill, and guaranteed-readable text on the accent itself. |
| `Border` / `BorderSoft` | Frames and quiet dividers. |
| `Success` / `Warning` / `Error` / `Info` | Semantic state hues, each with an `On*` readable partner for solid badges. `Info` intentionally rides the accent hue. |
| `Selection` | Background of the highlighted row in any list. |

Rules:

- Components ask for a token (or a `theme.Kind`), never a raw palette field
  or literal color. A new preset restyles every component at once.
- Every derivation passes broken custom-theme hex through unchanged instead
  of inventing colors, so a malformed theme degrades quietly, never fatally.
- On-colors meet the same 4.5:1 contrast floor the rest of the UI enforces
  (`TestForOnColorsAreReadable` checks every built-in preset).

## Component kit (`internal/app/components.go`)

The reusable renderers the chrome is built from. All of them return
exact-width, grapheme-safe strings with an explicit background on every cell
(an inner ANSI reset must never expose the terminal's default background):

- `renderRuns` — draw styled runs on a background, padded/truncated to width.
- `badgeTextRun` / `liveBadgeRuns` — solid semantic badges (LIVE, REC,
  dropped) and their dimmed pulse-beat variants.
- `keycap` — keyboard hints as raised, accent "buttons" (help, footers).
- `separatorRun` — faint `|` dividers that keep the line's exact text.
- `progressBar` — gradient meter advancing in eighth-cell steps over a track.
- `styleLineSpans` — recolor spans of an already-assembled plain line without
  changing its text; used wherever mouse hit-testing measures the same run
  (the tab bar), so drawn and measured layouts cannot drift.
- `stylePickerLines` / `styleFormLines` — the shared row language of every
  overlay and form tab: accent header, selection-token highlight, semantic
  failure/success rows.

## Surfaces

- **Tab bar** — pill strip on the track token; the active tab is a solid
  accent pill shimmering with the shared frame clock.
- **Status line** — segmented semantic runs on the surface token: badges for
  what demands attention, connection-state color on the channel, telemetry
  receding muted → faint.
- **Panes** — icon-bearing titles in the top border, identity-colored left
  rail, full frame lighting up on focus (`renderPane`).
- **Composer** — quiet inset panel; the focus rail shimmers with the pane
  gradient; block cursor on the shared clock.
- **Overlays** (command palette, pickers, inspector) and **form tabs**
  (Stream Info, Markers) — the shared row language above.
- **Splash** — gradient logo, typewriter tagline, sub-cell boot meter.

## States and degradation

- Visual states map to `theme.Kind`: neutral, accent, success, warning,
  error, info — plus focused (animated gradient), selected (accent-soft
  fill), and disabled/quiet (muted/faint).
- Animation is frame-counter driven and honors `animation = off|reduced`;
  every animated element has a static deterministic frame.
- Small terminals degrade by dropping decoration, never function: panes lose
  frames, pickers lose borders, the tab bar falls back to compact labels,
  status segments drop narrowest-first. Text content is identical in every
  mode, which is what keeps the rendering tests green across restyles.
