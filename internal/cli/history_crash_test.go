package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
)

type crashAfterOutput struct{}

func (crashAfterOutput) Write(b []byte) (int, error) {
	n, err := os.Stdout.Write(b)
	if err == nil {
		os.Exit(23)
	}
	return n, err
}

func TestHistoryOutputCrashHelper(t *testing.T) {
	path := os.Getenv("TWI_HISTORY_CRASH_PATH")
	if path == "" {
		return
	}
	o := historyOptions(path)
	o.cursorFile = os.Getenv("TWI_HISTORY_CRASH_CURSOR")
	o.allowPartial = true
	consumeHistory(context.Background(), o, time.Now().UTC(), crashAfterOutput{}, os.Stderr)
	t.Fatal("consumer failed to emit")
}

func TestOutputBeforeCheckpointCrashReplaysDuplicate(t *testing.T) {
	path, _, at := historyFixture(t, 2)
	cursor := filepath.Join(t.TempDir(), "cursor")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHistoryOutputCrashHelper$")
	cmd.Env = append(os.Environ(), "TWI_HISTORY_CRASH_PATH="+path, "TWI_HISTORY_CRASH_CURSOR="+cursor)
	emitted, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("child exit %v, output %s", err, emitted)
	}
	var first automation.Record
	if err = json.Unmarshal(bytes.TrimSpace(emitted), &first); err != nil {
		t.Fatal("stdout record invalid", err, string(emitted))
	}
	if _, err = history.ReadCheckpoint(cursor); !os.IsNotExist(err) {
		t.Fatal("checkpoint written before output completed", err)
	}
	o := historyOptions(path)
	o.cursorFile = cursor
	o.allowPartial = true
	var out, diagnostics bytes.Buffer
	if code := consumeHistory(context.Background(), o, at, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	var replay automation.Record
	if err = json.Unmarshal(bytes.Split(out.Bytes(), []byte{'\n'})[0], &replay); err != nil {
		t.Fatal(err)
	}
	if replay.ID != first.ID || replay.Sequence != first.Sequence {
		t.Fatal("expected duplicate after output/checkpoint crash", first, replay)
	}
	cp, err := history.ReadCheckpoint(cursor)
	if err != nil || cp.Position != 2 {
		t.Fatal(cp, err)
	}
}
