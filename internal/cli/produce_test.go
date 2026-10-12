package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
)

func TestProduceDryRunRoutesAndPreservesText(t *testing.T) {
	r := newRunner()

	var out, diagnostics bytes.Buffer
	code := r.runProduce(context.Background(), []string{"--dry-run", "-t", "#ALPHA", "-t", "alpha", "-t", "beta", "--broadcast"}, strings.NewReader("  hello  \r\nworld"), &out, &diagnostics)
	if code != 0 {
		t.Fatalf("code %d: %s", code, diagnostics.String())
	}
	decoder := json.NewDecoder(&out)
	for i, channel := range []string{"alpha", "beta", "alpha", "beta"} {
		var receipt automation.SendResult
		if err := decoder.Decode(&receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Channel.Login != channel || receipt.Status != "validated" || receipt.InputIndex != i/2+1 {
			t.Fatalf("receipt %+v", receipt)
		}
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("unexpected trailing output: %v", err)
	}
}
func TestProduceStrictJSONRouting(t *testing.T) {
	r := newRunner()

	for _, test := range []struct {
		name, input string
		code        int
	}{
		{"valid", `{"channel":"Alpha","text":"Hello","client_id":"job-1"}`, 0},
		{"unknown", `{"channel":"alpha","text":"Hello","typo":true}`, 2},
		{"allowlist", `{"channel":"beta","text":"Hello"}`, 2},
		{"control", `{"channel":"alpha","text":"Hello\nworld"}`, 2},
		{"missing", `{"text":"Hello"}`, 0},
		{"trailing", `{"text":"Hello"} {}`, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, err bytes.Buffer
			got := r.runProduce(context.Background(), []string{"--dry-run", "--input-format", "jsonl", "-t", "alpha"}, strings.NewReader(test.input), &out, &err)
			if got != test.code {
				t.Fatalf("code %d want %d output %s", got, test.code, out.String())
			}
		})
	}
}
func TestProduceUnicodeAndNUL(t *testing.T) {
	r := newRunner()

	var out, err bytes.Buffer
	got := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "--delimiter", "nul", "--skip-empty"}, strings.NewReader("\x00"+strings.Repeat("界", 500)+"\x00"), &out, &err)
	if got != 0 {
		t.Fatalf("code %d %s", got, err.String())
	}
	out.Reset()
	got = r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "-m", strings.Repeat("界", 501)}, strings.NewReader(""), &out, &err)
	if got != 2 || out.Len() != 0 {
		t.Fatalf("oversized finite input: %d %s", got, out.String())
	}
}
func TestProduceMixedAndOptionErrors(t *testing.T) {
	r := newRunner()

	var out, err bytes.Buffer
	got := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "--on-error", "continue"}, strings.NewReader("ok\n\nlast"), &out, &err)
	if got != 8 || strings.Count(out.String(), "\n") != 3 {
		t.Fatalf("mixed: %d %s", got, out.String())
	}
	for _, args := range [][]string{{"--dry-run", "-t", "alpha", "-t", "beta"}, {"--dry-run", "-t", "alpha", "--broadcast", "--input-format", "jsonl"}, {"--dry-run", "-t", "alpha", "--file", "-", "--file", "-"}, {"--dry-run", "-t", "alpha", "--format", "text", "-J"}, {"--dry-run", "-t", "alpha", "--channels", "beta,"}} {
		out.Reset()
		if got = r.runProduce(context.Background(), args, strings.NewReader("ok"), &out, &err); got != 2 {
			t.Fatalf("args %v code %d", args, got)
		}
	}
}

type brokenProducerWriter struct{}

func (brokenProducerWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestProduceOutputFailureAndBlockedInputDeadline(t *testing.T) {
	r := newRunner()

	var err bytes.Buffer
	if got := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "-m", "hello"}, strings.NewReader(""), brokenProducerWriter{}, &err); got != 7 {
		t.Fatalf("output code %d", got)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	started := time.Now()
	if got := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "--duration", "20ms"}, reader, io.Discard, &err); got != 4 {
		t.Fatalf("deadline code %d", got)
	}
	if time.Since(started) > time.Second {
		t.Fatal("deadline did not cancel blocked stdin")
	}
}

type shortProducerWriter struct{}

func (shortProducerWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestProduceShortOutputFails(t *testing.T) {
	r := newRunner()

	for _, format := range []string{"jsonl", "text"} {
		if code := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "-m", "hello", "--format", format}, strings.NewReader(""), shortProducerWriter{}, io.Discard); code != 7 {
			t.Fatalf("format %s code %d", format, code)
		}
	}
}
func TestProduceHelpOnlyStdout(t *testing.T) {
	r := newRunner()

	var out, err bytes.Buffer
	if code := r.runProduce(context.Background(), []string{"--help"}, strings.NewReader(""), &out, &err); code != 0 || out.Len() == 0 || err.Len() != 0 {
		t.Fatalf("code %d stdout %s stderr %s", code, out.String(), err.String())
	}
}

func TestProduceHelpFlagTextRemainsPayload(t *testing.T) {
	r := newRunner()

	for _, value := range []string{"--help", "-h", "-help"} {
		var out, err bytes.Buffer
		if code := r.runProduce(context.Background(), []string{"--dry-run", "-t", "alpha", "-m", value}, strings.NewReader(""), &out, &err); code != 0 || !strings.Contains(out.String(), `"status":"validated"`) || strings.Contains(out.String(), "Usage:") {
			t.Fatalf("value %s code %d stdout %s stderr %s", value, code, out.String(), err.String())
		}
	}
}

func TestProduceInputErrorUsesMixedExitAfterSuccess(t *testing.T) {
	r := newRunner()

	dir := t.TempDir()
	file := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		files []string
		code  int
	}{{"missing_first", []string{filepath.Join(dir, "missing.txt")}, 7}, {"missing_after_success", []string{file, filepath.Join(dir, "missing.txt")}, 8}} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"--dry-run", "-t", "alpha"}
			for _, file := range test.files {
				args = append(args, "--file", file)
			}
			var out, err bytes.Buffer
			if code := r.runProduce(context.Background(), args, strings.NewReader(""), &out, &err); code != test.code {
				t.Fatalf("code %d want%d stdout %s stderr%s", code, test.code, out.String(), err.String())
			}
		})
	}
}
