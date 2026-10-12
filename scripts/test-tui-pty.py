#!/usr/bin/env python3
"""Exercise native terminal input and verify restoration on normal/signal exits.

Usage: python3 scripts/test-tui-pty.py /path/to/twi
Requires Python's standard library and a Unix PTY. Model-level tests verify
input behavior; this smoke verifies the actual terminal protocol and lifecycle.
"""
import errno
import fcntl
import os
import pty
import select
import signal
import struct
import sys
import tempfile
import termios
import time


def run(binary, exit_mode, columns, rows):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
    before = termios.tcgetattr(slave)
    with tempfile.TemporaryDirectory(prefix="twi-pty-") as isolated:
        pid = os.fork()
        if pid == 0:
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
            for fd in (0, 1, 2):
                os.dup2(slave, fd)
            os.close(master)
            os.close(slave)
            environment = os.environ.copy()
            environment.update(TERM="xterm-256color", COLORTERM="truecolor",
                               XDG_CONFIG_HOME=isolated, XDG_CACHE_HOME=isolated)
            os.execve(binary, [binary, "chat", "--mock", "--channel", "alpha"], environment)
        output = bytearray()

        def drain(seconds):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                if not select.select([master], [], [], 0.05)[0]:
                    continue
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        return
                    raise
                output.extend(chunk)
                # Answer startup terminal queries without claiming capabilities
                # unavailable to a plain xterm-compatible PTY.
                for query, answer in ((b"\x1b[6n", b"\x1b[1;1R"),
                                      (b"\x1b[c", b"\x1b[?1;2c"),
                                      (b"\x1b[>c", b"\x1b[>0;1;0c")):
                    if query in chunk:
                        os.write(master, answer)

        try:
            drain(1.0)
            # Skip splash, enter composer, paste printable text and line breaks.
            os.write(master, b"i")
            drain(0.2)
            os.write(master, b"i\x1b[200~v2 paste\nUnicode "+"視聴者 😀".encode()+b"\x1b[201~")
            drain(0.2)
            # Blur/focus, exit insert, tabs, wheel/click/release, and picker keys.
            for data in (b"\x1b[O\x1b[I", b"\x1b", b"\x1b2", b"\x1b1",
                         b"\x1b[<64;30;5M", b"\x1b[<65;30;5M",
                         b"\x1b[<0;5;5M\x1b[<0;5;5m", b"\x10", b"\x1b[B\x1b[A\x1b"):
                os.write(master, data)
                drain(0.12)
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
            os.kill(pid, signal.SIGWINCH)
            drain(0.3)
            if exit_mode == "ctrl+c":
                os.write(master, b"\x03")
            elif exit_mode == "q":
                os.write(master, b"q")
            else:
                os.kill(pid, signal.SIGTERM)
            drain(0.8)
            deadline = time.monotonic()+5
            while True:
                found, status = os.waitpid(pid, os.WNOHANG)
                if found:
                    break
                if time.monotonic() > deadline:
                    raise AssertionError("TUI failed to exit")
                drain(0.1)
            code = os.waitstatus_to_exitcode(status)
            expected = 143 if exit_mode == "SIGTERM" else 0
            assert code == expected, f"{exit_mode}: exit code {code}, want {expected}"
            for mode in (1049, 2004, 1004, 1002, 1006):
                enabled = f"\x1b[?{mode}h".encode()
                disabled = f"\x1b[?{mode}l".encode()
                assert enabled in output, f"{exit_mode}: mode {mode} was not enabled"
                assert output.rfind(disabled) > output.rfind(enabled), f"{exit_mode}: mode {mode} was not restored"
            assert b"\x1b]111" in output, f"{exit_mode}: terminal background not restored"
            assert output.rfind(b"\x1b[?25h") > output.rfind(b"\x1b[?25l"), f"{exit_mode}: cursor not restored"
            assert termios.tcgetattr(slave) == before, f"{exit_mode}: termios not restored"
            assert b"panic:" not in output, "TUI panicked"
            print(f"PASS {exit_mode} {columns}x{rows}: keyboard, paste, focus, mouse, resize, terminal restoration")
        finally:
            try:
                os.kill(pid, signal.SIGKILL)
                os.waitpid(pid, 0)
            except (ProcessLookupError, ChildProcessError):
                pass
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    executable = os.path.abspath(sys.argv[1])
    for mode, width, height in (("q", 80, 24), ("ctrl+c", 32, 12), ("SIGTERM", 120, 30)):
        run(executable, mode, width, height)
