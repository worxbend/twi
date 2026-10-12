package main

import (
	"bufio"
	"context"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("TWI_TEST_MAIN") != "1" {
		return
	}
	os.Args = []string{"twi", "produce", "-t", "alpha", "--dry-run"}
	if os.Getenv("TWI_TEST_MODE") == "consume" {
		os.Args = []string{"twi", "consume", "-t", "alpha", "--last", "1", "--history", os.Getenv("TWI_TEST_HISTORY")}
	}
	main()
}
func TestSignalExitWithBlockedInput(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Unix process signals")
	}
	for _, test := range []struct {
		name   string
		signal os.Signal
		code   int
	}{{"interrupt", os.Interrupt, 130}, {"terminate", syscall.SIGTERM, 143}} {
		t.Run(test.name, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(exe, "-test.run=^TestProcessHelper$")
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "TWI_") && !strings.HasPrefix(v, "TWITCH_") && !strings.HasPrefix(v, "XDG_") {
					command.Env = append(command.Env, v)
				}
			}
			dir := t.TempDir()
			command.Env = append(command.Env, "TWI_TEST_MAIN=1", "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"), "XDG_STATE_HOME="+filepath.Join(dir, "state"))
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			if _, err = input.Write([]byte("first record\n")); err != nil {
				t.Fatal(err)
			}
			received := make(chan bool, 1)
			go func() { received <- bufio.NewScanner(output).Scan() }()
			select {
			case ok := <-received:
				if !ok {
					t.Fatal("producer did not emit receipt")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("receipt timeout")
			}
			if err = command.Process.Signal(test.signal); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- command.Wait() }()
			select {
			case err = <-exited:
			case <-time.After(5 * time.Second):
				t.Fatal("signal shutdown timed out")
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != test.code {
				t.Fatalf("signal exit got %v, want %d", err, test.code)
			}
		})
	}
}
func TestBrokenPipeExitContracts(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Unix pipes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "history.db")
	store, err := history.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Now()
	ctx := context.Background()
	if err = store.BeginCapture(ctx, "alpha", "test", "irc", at.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Append(ctx, automation.Record{Type: "message", ID: "1", Channel: automation.Channel{Login: "alpha"}, Text: "hello", EventAt: at, ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err = store.Heartbeat(ctx, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode string
		code int
	}{{"produce", 7}, {"consume", 0}} {
		t.Run(test.mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestProcessHelper$")
			cmd.Env = append(os.Environ(), "TWI_TEST_MAIN=1", "TWI_TEST_MODE="+test.mode, "TWI_TEST_HISTORY="+path)
			cmd.Stdin = strings.NewReader("hello\n")
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
			defer writer.Close()
			cmd.Stdout = writer
			err = cmd.Run()
			if test.code == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				e, ok := err.(*exec.ExitError)
				if !ok || e.ExitCode() != test.code {
					t.Fatalf("got %v want %d", err, test.code)
				}
			}
		})
	}
}
