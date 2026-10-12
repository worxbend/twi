package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
)

func TestHistorySearchBoundedAndModerated(t *testing.T) {
	path, s, _ := historyFixture(t, 3)
	ctx := context.Background()
	var out, diagnostics bytes.Buffer
	if code := runHistorySearch(ctx, []string{"--history", path, "--query", "hello", "--limit", "2", "--channel", "alpha"}, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatal("search limit ignored", out.String())
	}
	var r automation.Record
	if err := json.Unmarshal(lines[0], &r); err != nil || r.ID != "2" || r.Source != "history" {
		t.Fatal(r, err)
	}
	if _, _, err := s.Append(ctx, automation.Record{Type: "chat_cleared", Channel: automation.Channel{Login: "alpha"}, ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := runHistorySearch(ctx, []string{"--history", path, "--query", "hello"}, &out, &diagnostics); code != 0 || out.Len() != 0 {
		t.Fatal(code, out.String(), diagnostics.String())
	}
}

func TestHistorySearchRejectsBeforeOpeningStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "chat.db")
	for _, args := range [][]string{{"--history", path, "--query", "hello", "--limit", "1001"}, {"--history", path}, {"--history", path, "--query", "hello", "extra"}} {
		var out, diagnostics bytes.Buffer
		if code := runHistorySearch(context.Background(), args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatal(code, out.String(), diagnostics.String())
		}
	}
}

type searchErrorSink struct{ err error }

func (s searchErrorSink) Write([]byte) (int, error) { return 0, s.err }

func TestHistorySearchOutputExitContract(t *testing.T) {
	path, _, _ := historyFixture(t, 1)
	for _, test := range []struct {
		err  error
		code int
	}{{syscall.EPIPE, 0}, {io.ErrShortWrite, 7}} {
		var diagnostics bytes.Buffer
		code := runHistorySearch(context.Background(), []string{"--history", path, "--query", "hello"}, searchErrorSink{test.err}, &diagnostics)
		if code != test.code {
			t.Fatal(code, test.code, diagnostics.String())
		}
	}
}
