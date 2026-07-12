package app

import (
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// setBackgroundColorSeq is the OSC 11 escape sequence that overrides the
// terminal emulator's own default background color (not just the cells
// lipgloss paints), so the active theme covers the whole terminal, not only
// the alt-screen viewport content. resetBackgroundColorSeq (OSC 111) restores
// whatever background the user's terminal was configured with before twi
// started. Both are widely supported (xterm, iTerm2, kitty, Alacritty,
// WezTerm, VTE-based terminals); unsupported terminals simply ignore them.
const (
	setBackgroundColorSeqFormat = "\x1b]11;%s\x07"
	resetBackgroundColorSeq     = "\x1b]111\x07"
)

// writeThemeBackground overrides the terminal's background color to match
// the active theme. It is a fire-and-forget tea.Cmd side effect (no
// resulting Msg) rather than part of View()'s returned string, since OSC
// sequences use a BEL/ST terminator that isn't handled the same way as the
// SGR color sequences lipgloss strips when measuring rendered width.
func (m mockShellModel) writeThemeBackground() tea.Cmd {
	w := m.terminalOutput
	background := m.theme.Background
	if w == nil || strings.TrimSpace(background) == "" {
		return nil
	}
	return func() tea.Msg {
		fmt.Fprintf(w, setBackgroundColorSeqFormat, background)
		return nil
	}
}

// resetTerminalBackground restores the terminal's own background color.
// Callers must invoke this once after an interactive program exits so the
// override doesn't leak into the user's shell.
func resetTerminalBackground(w io.Writer) {
	fmt.Fprint(w, resetBackgroundColorSeq)
}
