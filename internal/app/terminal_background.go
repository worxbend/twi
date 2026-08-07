package app

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// themeBackgroundSequence returns the OSC 11 escape sequence that overrides
// the terminal emulator's own default background color (not just the cells
// lipgloss paints), so the darker application canvas covers the whole terminal, not only
// the cells that carry an explicit background SGR code. It must be part of
// View()'s returned string rather than written directly to the output
// writer: bubbletea's renderer owns that writer from its own goroutine, and
// writing to it independently (e.g. from a tea.Cmd) races that goroutine and
// can interleave or drop the sequence, leaving some regions on the terminal's
// original background. Embedding it in View() is safe because View() runs
// synchronously on the renderer's own goroutine, and the ANSI parser bubbletea
// and lipgloss share (charmbracelet/x/ansi) recognizes OSC sequences as
// zero-width, so it doesn't perturb layout/width calculations.
//
// Returns "" outside an interactive session (terminalOutput is only set by
// RunMockWithOptions/RunClientWithOptions) so piped/test output stays clean.
func (m shellModel) themeBackgroundSequence() string {
	canvas := m.canvasBackground()
	if m.terminalOutput == nil || strings.TrimSpace(canvas) == "" {
		return ""
	}
	return ansi.SetBackgroundColor(canvas)
}

// primeTerminalBackground writes the OSC 11 override once, synchronously,
// before the interactive tea.Program (and its renderer goroutine) starts.
// This is race-free by construction — nothing else is writing to w yet — and
// avoids a brief flash of the terminal's original background before the
// first frame renders. View()'s embedded sequence (themeBackgroundSequence)
// still covers the ongoing session, including live theme-preview changes.
func primeTerminalBackground(w io.Writer, background string) {
	if strings.TrimSpace(background) == "" {
		return
	}
	io.WriteString(w, ansi.SetBackgroundColor(background)) //nolint:errcheck
}

// resetTerminalBackground restores the terminal's own background color.
// Callers must invoke this once after an interactive program exits so the
// override doesn't leak into the user's shell. Called after program.Run()
// returns, when bubbletea's renderer goroutine has already stopped, so a
// direct write here is race-free.
func resetTerminalBackground(w io.Writer) {
	io.WriteString(w, ansi.ResetBackgroundColor) //nolint:errcheck
}
