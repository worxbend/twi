package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestDesktopNotificationCommandByPlatform(t *testing.T) {
	name, args, ok := desktopNotificationCommand("linux", "Raid in #example", "15 raiders")
	if !ok || name != "notify-send" {
		t.Fatalf("linux command = %q %#v ok=%v, want notify-send", name, args, ok)
	}
	if got, want := args[len(args)-2], "Raid in #example"; got != want {
		t.Fatalf("linux title arg = %q, want %q", got, want)
	}
	if got, want := args[len(args)-1], "15 raiders"; got != want {
		t.Fatalf("linux body arg = %q, want %q", got, want)
	}

	name, args, ok = desktopNotificationCommand("darwin", "Raid in #example", "15 raiders")
	if !ok || name != "osascript" {
		t.Fatalf("darwin command = %q %#v ok=%v, want osascript", name, args, ok)
	}
	if got, want := args[len(args)-2], "Raid in #example"; got != want {
		t.Fatalf("darwin title arg = %q, want %q", got, want)
	}
	if got, want := args[len(args)-1], "15 raiders"; got != want {
		t.Fatalf("darwin body arg = %q, want %q", got, want)
	}

	name, args, ok = desktopNotificationCommand("windows", "Raid in #example", "15 raiders")
	if !ok || name != "powershell.exe" {
		t.Fatalf("windows command = %q %#v ok=%v, want powershell.exe", name, args, ok)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-EncodedCommand") {
		t.Fatalf("windows args missing encoded command: %#v", args)
	}
	if strings.Contains(joined, "Raid in #example") || strings.Contains(joined, "15 raiders") {
		t.Fatalf("windows args include raw notification text, want encoded command: %#v", args)
	}

	if _, _, ok := desktopNotificationCommand("plan9", "Raid", "body"); ok {
		t.Fatal("unsupported platform returned ok=true")
	}
}

func TestWindowsToastCommandEscapesPowerShellInterpolation(t *testing.T) {
	script := decodeWindowsToastScript(t, windowsToastPowerShellCommand(
		"$(whoami) $env:USERNAME",
		"run `$(calc.exe) back`tick",
	))
	start := strings.Index(script, "@\"")
	end := strings.Index(script, "\"@")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("encoded script has no expandable here-string:\n%s", script)
	}
	payload := script[start+2 : end]
	if i := unescapedPowerShellDollar(payload); i >= 0 {
		t.Fatalf("here-string payload has an unescaped $ at offset %d:\n%s", i, payload)
	}
	for _, want := range []string{"`$(whoami)", "`$env:USERNAME", "```$(calc.exe)", "back``tick"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("here-string payload missing escaped form %q:\n%s", want, payload)
		}
	}
}

// unescapedPowerShellDollar returns the offset of the first $ that PowerShell
// would interpolate, i.e. one not introduced by a backtick escape.
func unescapedPowerShellDollar(script string) int {
	for i := 0; i < len(script); i++ {
		switch script[i] {
		case '`':
			i++
		case '$':
			return i
		}
	}
	return -1
}

func decodeWindowsToastScript(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("encoded command is not base64: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("encoded command has odd byte length %d, want UTF-16LE", len(raw))
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func TestDefaultSystemNotifierUsesDesktopNotificationWhenAvailable(t *testing.T) {
	var bell bytes.Buffer
	var called bool
	notifier := defaultSystemNotifier{
		desktop: desktopNotifier{
			goos: "linux",
			lookPath: func(name string) (string, error) {
				if name != "notify-send" {
					t.Fatalf("lookPath name = %q, want notify-send", name)
				}
				return "/usr/bin/notify-send", nil
			},
			runCommand: func(ctx context.Context, path string, args ...string) error {
				called = true
				if path != "/usr/bin/notify-send" {
					t.Fatalf("runCommand path = %q, want /usr/bin/notify-send", path)
				}
				if len(args) == 0 || args[len(args)-1] != "15 raiders" {
					t.Fatalf("runCommand args = %#v, want notification body", args)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("runCommand context has no deadline")
				}
				return nil
			},
		},
		bell: terminalBellNotifier{w: &bell},
	}

	if err := notifier.Notify(context.Background(), SystemNotification{Title: "Raid in #example", Body: "15 raiders"}); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if !called {
		t.Fatal("desktop command was not called")
	}
	if got := bell.String(); got != "" {
		t.Fatalf("bell output = %q, want empty when desktop notification succeeds", got)
	}
}

func TestDefaultSystemNotifierFallsBackToTerminalBell(t *testing.T) {
	var bell bytes.Buffer
	notifier := defaultSystemNotifier{
		desktop: desktopNotifier{
			goos: "linux",
			lookPath: func(string) (string, error) {
				return "", errors.New("notify-send missing")
			},
		},
		bell: terminalBellNotifier{w: &bell},
	}

	if err := notifier.Notify(context.Background(), SystemNotification{Title: "Raid in #example", Body: "15 raiders"}); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if got, want := bell.String(), terminalBell; got != want {
		t.Fatalf("bell output = %q, want %q", got, want)
	}
}

func TestDesktopNotifierSanitizesNotificationText(t *testing.T) {
	var gotArgs []string
	notifier := desktopNotifier{
		goos:    "linux",
		timeout: time.Second,
		lookPath: func(string) (string, error) {
			return "/usr/bin/notify-send", nil
		},
		runCommand: func(_ context.Context, _ string, args ...string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}

	err := notifier.Notify(context.Background(), SystemNotification{
		Title: "Raid\rin\n#example",
		Body:  "token=oauth:secret-token\n15 raiders",
	})
	if err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	joined := strings.Join(gotArgs, " ")
	if strings.Contains(joined, "\n") || strings.Contains(joined, "\r") {
		t.Fatalf("notification args contain control characters: %#v", gotArgs)
	}
	if strings.Contains(joined, "secret-token") || !strings.Contains(joined, "<redacted>") {
		t.Fatalf("notification args not redacted: %#v", gotArgs)
	}
}
