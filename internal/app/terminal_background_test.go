package app

import (
	"bytes"
	"testing"

	"github.com/w0rxbend/twi/internal/config"
)

func TestWriteThemeBackgroundEmitsOSC11(t *testing.T) {
	var buf bytes.Buffer
	model := newMockShellModel("alpha", config.Default())
	model.terminalOutput = &buf

	cmd := model.writeThemeBackground()
	if cmd == nil {
		t.Fatal("writeThemeBackground() = nil, want a command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("writeThemeBackground command produced %#v, want nil (fire-and-forget)", msg)
	}
	want := "\x1b]11;" + model.theme.Background + "\x07"
	if got := buf.String(); got != want {
		t.Fatalf("terminal output = %q, want %q", got, want)
	}
}

func TestWriteThemeBackgroundNilWithoutOutput(t *testing.T) {
	model := newMockShellModel("alpha", config.Default())
	if cmd := model.writeThemeBackground(); cmd != nil {
		t.Fatal("writeThemeBackground() with nil terminalOutput = non-nil, want nil")
	}
}

func TestResetTerminalBackgroundEmitsOSC111(t *testing.T) {
	var buf bytes.Buffer
	resetTerminalBackground(&buf)
	if got, want := buf.String(), "\x1b]111\x07"; got != want {
		t.Fatalf("resetTerminalBackground output = %q, want %q", got, want)
	}
}
