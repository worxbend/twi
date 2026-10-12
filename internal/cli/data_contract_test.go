package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDataUsageBeforeCredentials(t *testing.T) {
	r := newRunner()

	for _, args := range [][]string{{"produce", "--help"}, {"consume", "--help"}, {"record", "--help"}, {"history", "--help"}, {"chat", "--help"}, {"config", "--help"}, {"config", "show", "--help"}, {"doctor", "--help"}, {"login", "--help"}, {"setup", "--help"}, {"profile", "--help"}, {"version", "--help"}, {"completion", "--help"}} {
		var out, err bytes.Buffer
		if code := r.RunContext(context.Background(), args, &bytes.Buffer{}, &out, &err); code != 0 {
			t.Errorf("%v code=%d %s", args, code, err.String())
		}
		if strings.Contains(out.String()+err.String(), "panic") {
			t.Errorf("help panics for %v", args)
		}
	}
}
func TestConsumeInvalid(t *testing.T) {
	for _, args := range [][]string{{"-t", "alpha", "--last", "0"}, {"-t", "alpha", "--source", "live", "--last", "1"}, {"-t", "alpha", "--follow", "--exit"}, {"-t", "alpha", "--since", "10m", "--last", "2"}, {"-t", "alpha", "--text-only"}, {"-t", "alpha", "--channels", "beta,"}, {"-t", "alpha", "extra"}, {"-t", "alpha", "--duration", "0"}} {
		_, code, e := parseConsume(args, &bytes.Buffer{})
		if code != 2 || e == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
func TestRelativeTimeFixed(t *testing.T) {
	start := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	got, err := parseDataTime("10m", start)
	if err != nil || !got.Equal(start.Add(-10*time.Minute)) {
		t.Fatal(got, err)
	}
}
func TestRootAliasesConflict(t *testing.T) {
	r := newRunner()

	var out, err bytes.Buffer
	if code := r.RunContext(context.Background(), []string{"-P", "-C"}, &bytes.Buffer{}, &out, &err); code != 2 {
		t.Fatal(code)
	}
}
func TestAliasTextIsPayload(t *testing.T) {
	r := newRunner()

	var out, err bytes.Buffer
	if code := r.RunContext(context.Background(), []string{"produce", "-t", "alpha", "-m", "-C", "--dry-run"}, &bytes.Buffer{}, &out, &err); code != 0 {
		t.Fatalf("message alias text rejected code %d: %s", code, err.String())
	}
}
func TestChannelOccurrenceOrder(t *testing.T) {
	r := newRunner()

	var out, diagnostics bytes.Buffer
	if code := r.runProduce(context.Background(), []string{"--channels", "alpha,beta", "-t", "gamma", "-t", "alpha", "--broadcast", "--dry-run", "-m", "hello"}, &bytes.Buffer{}, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	value := out.String()
	if !(strings.Index(value, `"login":"alpha"`) < strings.Index(value, `"login":"beta"`) && strings.Index(value, `"login":"beta"`) < strings.Index(value, `"login":"gamma"`)) {
		t.Fatal(value)
	}
}
func TestLegacyCommandOutputFailure(t *testing.T) {
	r := newRunner()

	var diagnostics bytes.Buffer
	if code := r.RunContext(context.Background(), []string{"version"}, &bytes.Buffer{}, shortCLIWriter{}, &diagnostics); code != 7 {
		t.Fatal(code)
	}
}

type shortCLIWriter struct{}

func (shortCLIWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
