# Terminal v2 migration validation

On 2026-10-12, migrated to `charm.land/bubbletea/v2 v2.1.0` and
`charm.land/lipgloss/v2 v2.0.6`. Versions were checked against the Go module
proxy. API changes follow the upstream [Bubble Tea upgrade guide](https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md)
and [Lip Gloss upgrade guide](https://github.com/charmbracelet/lipgloss/blob/main/UPGRADE_GUIDE_V2.md).

The model now returns a native `tea.View`; its fields declare alternate screen,
mouse reporting, focus reporting, bracketed paste and the terminal background.
Bubble Tea owns terminal mode changes and restoration. Key presses/releases,
paste, mouse clicks/releases/motion/wheels use their native v2 messages. Pasted
text reaches active inputs directly, so pasted shortcut names cannot invoke
commands or send a message. Alt tab shortcuts use key codes, including events
without printable text. Border-inclusive v2 style dimensions preserve the
existing pane sizes and mouse hitboxes.

The keyboard and terminal-event controller lives in `internal/app/keyboard.go`;
view assembly lives in `internal/app/shell_view.go`. Existing render, animation,
Unicode width, picker, composer, resize, mouse and theme tests remain in place.
Text assertions inspect visible text where v2 adds ANSI style segmentation;
color/rail/background tests still inspect styled output. No v1 terminal imports,
compatibility wrappers or `termenv` dependency remain.

Executed checks:

```sh
go test ./internal/app ./internal/render ./internal/theme ./internal/animation
go test -race ./internal/app ./internal/render ./internal/theme ./internal/animation
go build -o /tmp/twi-ui-v2 ./cmd/twi
python3 scripts/test-tui-pty.py /tmp/twi-ui-v2
```

All passed. The Unix PTY smoke exercises keyboard navigation, bracketed Unicode
paste, focus/blur, mouse wheel/click/release, resizing and three exit paths:
`q` at 80x24, `ctrl+c` at 32x12 and SIGTERM at 120x30. It checks alternate-screen,
paste, focus and mouse modes are disabled after exit and the original termios
state is restored. SIGTERM returns 143, matching the process signal contract.
Model tests separately assert that paste cannot send or invoke shortcuts,
key releases and mouse drags do not trigger actions, native alt+2 switches tabs,
and focus/resize preserve Unicode drafts.

These tests use the deterministic mock source. They do not demonstrate live
Twitch credentials, a physical terminal emulator's rendering or controlled-channel
chat behavior. Those checks still need that environment.

A later controlled pass launched the production EventSub TUI against the
user-authorized `worxbend` channel using the private existing read grant. The
same PTY input/restoration matrix passed q at80x24, Ctrl+C at32x12, and SIGTERM
at120x30. No composer sends were performed. The test verifies launch/input and
terminal lifecycle; it does not independently assert subscription readiness.
The separate live recorder/producer suite did verify EventSub readiness and
message delivery. See [automation validation](automation-validation.md).
