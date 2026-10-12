package history

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestExplicitAttachmentFlushesOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(context.Background(), path, []string{"alpha"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Ready(context.Background(), "alpha", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = a.Submit(message("other", "rejected", time.Now().UTC())); err == nil {
		t.Fatal("unselected record accepted")
	}
	if err = a.Submit(message("alpha", "accepted", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.Submit(message("alpha", "late", time.Now().UTC())); err == nil {
		t.Fatal("closed recorder accepted record")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, _, err := s.ReadAfter(context.Background(), Query{}, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "accepted" {
		t.Fatal(rows, err)
	}
	status, err := s.Status(context.Background())
	if err != nil || len(status.Captures) != 1 || !status.Captures[0].Clean {
		t.Fatal(status, err)
	}
}

func TestAttachmentReadinessAndDisconnectCoverage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Submit(message("alpha", "observed-before-ready", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.Status(ctx)
	if err != nil || len(status.Captures) != 0 {
		t.Fatal("opening invented coverage", status, err)
	}
	at := time.Now().UTC()
	if err = a.Ready(ctx, "alpha", at); err != nil {
		t.Fatal(err)
	}
	if err = a.Submit(message("alpha", "ready", at)); err != nil {
		t.Fatal(err)
	}
	if err = a.Lost(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = a.Submit(message("alpha", "observed-late", at)); err != nil {
		t.Fatal(err)
	}
	if err = a.Ready(ctx, "alpha", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckCoverage(ctx, []string{"alpha"}, at, at.Add(3*time.Second)); err == nil {
		t.Fatal("reconnect gap hidden")
	}
}

func TestAttachmentFlushesQuietPartialBatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Ready(ctx, "alpha", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = a.Submit(message("alpha", "quiet-partial", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	deadline := time.After(time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("partial batch never became durable before close")
		case <-poll.C:
			rows, _, err := s.ReadAfter(ctx, Query{}, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 1 {
				return
			}
		}
	}
}

func TestAttachmentOverflowRemainsUncleanAfterErrorRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Ready(ctx, "alpha", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Hold the sole SQLite connection so ingestion cannot drain batches indefinitely.
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	overflow := false
	for i := 0; i < 301; i++ {
		r := message("alpha", fmt.Sprint(i), time.Now().UTC())
		if err = a.Submit(r); err == ErrAttachmentOverflow {
			overflow = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !overflow {
		t.Fatal("bounded queue did not reject overload")
	}
	if err = <-a.Errors(); err != ErrAttachmentOverflow {
		t.Fatal(err)
	}
	tx.Rollback()
	if err = a.Close(); err != ErrAttachmentOverflow {
		t.Fatal("consumed error lost from final result", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.Status(ctx)
	if err != nil || len(status.Captures) != 1 || status.Captures[0].Clean || len(status.Gaps) != 1 {
		t.Fatal("overflow incorrectly recorded clean coverage", status, err)
	}
}

func TestAttachmentLossCountAndBounds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha", "beta"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	at := time.Now().UTC()
	if err = a.Ready(ctx, "alpha", at); err != nil {
		t.Fatal(err)
	}
	if err = a.Loss(ctx, at, at, 3); err != nil {
		t.Fatal(err)
	}
	if err = a.Loss(ctx, at, at, 1<<63); err == nil {
		t.Fatal("loss count overflow accepted")
	}
	if err = a.Loss(ctx, at.Add(time.Second), at, 1); err == nil {
		t.Fatal("reverse interval accepted")
	}
	if err = a.Ready(ctx, "beta", at); err != nil {
		t.Fatal(err)
	}
	if err = a.Loss(ctx, at, at.Add(time.Second), 4); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.Status(ctx)
	if err != nil || len(status.Gaps) != 3 {
		t.Fatal(status, err)
	}
	known := 0
	unknown := 0
	for _, g := range status.Gaps {
		if g.Lost == nil {
			unknown++
		} else {
			known++
			if *g.Lost != 3 {
				t.Fatal(g)
			}
		}
	}
	if known != 1 || unknown != 2 {
		t.Fatal("aggregate loss misattributed", status.Gaps)
	}
}

func TestAttachmentIntegrationFailureEndsCoverage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err = a.Ready(ctx, "alpha", at); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("transport recording callback failed")
	a.Fail(failure)
	if err = <-a.Errors(); err != failure {
		t.Fatal(err)
	}
	if err = a.Close(); err != failure {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.Status(ctx)
	if err != nil || len(status.Gaps) != 1 || len(status.Captures) != 1 || status.Captures[0].Ended == nil {
		t.Fatal(status, err)
	}
	if err = s.CheckCoverage(ctx, []string{"alpha"}, at, *status.Captures[0].Ended); !errors.Is(err, ErrIncomplete) {
		t.Fatal("failed callback claimed complete capture", err)
	}
}

func TestAttachmentChannelLostLeavesOthersHealthy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	a, err := OpenAttachment(ctx, path, []string{"alpha", "beta"}, "irc")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	at := time.Now().UTC()
	for _, c := range []string{"alpha", "beta"} {
		if err = a.Ready(ctx, c, at); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.ChannelLost(ctx, "alpha", at.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Healthy(ctx, []string{"alpha"}, at); !errors.Is(err, ErrIncomplete) {
		t.Fatal("parted channel healthy", err)
	}
	if err = s.Healthy(ctx, []string{"beta"}, at); err != nil {
		t.Fatal("other channel lost", err)
	}
	if err = a.Ready(ctx, "alpha", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx)
	if err != nil || len(status.Captures) != 3 {
		t.Fatal(status, err)
	}
}
