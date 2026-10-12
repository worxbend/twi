package history

import (
	"context"
	"errors"
	"github.com/worxbend/twi/internal/automation"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "chat.db")
	s, e := OpenWriter(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func message(channel, id string, at time.Time) automation.Record {
	return automation.Record{Type: "message", ID: id, Channel: automation.Channel{ID: channel, Login: channel}, Author: automation.Author{ID: "user"}, Text: "body", EventAt: at, ReceivedAt: at, Transport: "irc"}
}
func TestStoreDedupTailAndCursor(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for i, id := range []string{"1", "2", "3"} {
		r := message("alpha", id, at.Add(time.Duration(i)*time.Second))
		if _, ok, e := s.Append(ctx, r); e != nil || !ok {
			t.Fatalf("append %v %v", ok, e)
		}
	}
	if _, ok, e := s.Append(ctx, message("alpha", "1", at)); e != nil || ok {
		t.Fatalf("dedup %v %v", ok, e)
	}
	if _, ok, e := s.Append(ctx, message("beta", "1", at)); e != nil || !ok {
		t.Fatal("shared observation collapsed")
	}
	records, high, e := s.Snapshot(ctx, Query{Channels: []string{"alpha", "beta"}, Last: 1, AllowPartial: true})
	if e != nil || len(records) != 2 || records[0].ID != "3" {
		t.Fatalf("tail %#v %v", records, e)
	}
	seq, e := s.ParseCursor(ctx, s.Cursor(high))
	if e != nil || seq != high {
		t.Fatal(seq, e)
	}
	reader, e := Open(s.path)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	more, scan, e := reader.ReadAfter(ctx, Query{Channels: []string{"missing"}}, 0, 10)
	if e != nil || len(more) != 0 || scan != high {
		t.Fatalf("scan %d %v", scan, e)
	}
	if _, e = s.ParseCursor(ctx, s.Cursor(high+1)); !errors.Is(e, ErrCursorExpired) {
		t.Fatal(e)
	}
}
func TestModerationCannotRestoreBody(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.Append(ctx, message("alpha", "1", at))
	r := message("alpha", "2", at)
	r.Reply = &automation.Reply{ParentID: "1", Text: "body"}
	s.Append(ctx, r)
	_, _, e := s.Append(ctx, automation.Record{Type: "message_deleted", Channel: r.Channel, TargetMessageID: "1", ReceivedAt: at})
	if e != nil {
		t.Fatal(e)
	}
	s.Append(ctx, message("alpha", "1", at))
	records, _, e := s.Snapshot(ctx, Query{Channels: []string{"alpha"}})
	if e != nil || len(records) != 1 || records[0].Reply.Text != "" {
		t.Fatalf("moderation %#v %v", records, e)
	}
	all, _, e := s.Snapshot(ctx, Query{Channels: []string{"alpha"}, AllEvents: true})
	if e != nil || all[0].Text != "" || !all[0].Deleted {
		t.Fatal(all, e)
	}
}
func TestCoverageAndRestart(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if e := s.BeginCapture(ctx, "alpha", "session", "irc", at); e != nil {
		t.Fatal(e)
	}
	end := at.Add(time.Minute)
	s.Heartbeat(ctx, "session", end)
	if e := s.CheckCoverage(ctx, []string{"alpha"}, at, end); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckCoverage(ctx, []string{"alpha"}, at.Add(-time.Second), end); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	if e := s.Healthy(ctx, []string{"alpha"}, end.Add(16*time.Second)); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	s.AddGap(ctx, Gap{Channel: "alpha", Start: at.Add(time.Second), End: at.Add(2 * time.Second), Cause: "overflow"})
	if e := s.CheckCoverage(ctx, []string{"alpha"}, at, end); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	path := s.path
	s.Close()
	s2, e := OpenWriter(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	status, e := s2.Status(ctx)
	if e != nil || status.Captures[0].Ended == nil || status.Captures[0].Clean {
		t.Fatal(status, e)
	}
}
func TestRetentionMonotonicAndLocks(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	r, _, e := s.Append(ctx, message("alpha", "1", at.Add(-2*time.Hour)))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = OpenWriter(s.path); !errors.Is(e, ErrWriterBusy) {
		t.Fatal(e)
	}
	plan, e := s.Prune(ctx, Retention{Age: time.Hour, MaxBytes: 1 << 30, Now: at}, true)
	if e != nil || plan.Records != 1 {
		t.Fatal(plan, e)
	}
	result, e := s.Prune(ctx, Retention{Age: time.Hour, MaxBytes: 1 << 30, Now: at}, false)
	if e != nil || result.Records != 1 {
		t.Fatal(result, e)
	}
	r2, _, e := s.Append(ctx, message("alpha", "2", at))
	if e != nil || r2.Sequence <= r.Sequence {
		t.Fatal(r2, e)
	}
	if _, e = s.ParseCursor(ctx, s.Cursor(0)); !errors.Is(e, ErrCursorExpired) {
		t.Fatal(e)
	}
}
func TestPrivateFilesAndCheckpoint(t *testing.T) {
	s := fixture(t)
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		info, e := os.Stat(s.path + suffix)
		if e == nil && info.Mode().Perm() != 0600 {
			t.Fatal(suffix, info.Mode())
		}
	}
	path := filepath.Join(t.TempDir(), "cursor")
	c := Checkpoint{SchemaVersion: 1, UUID: s.uuid, Position: 3, Fingerprint: "x"}
	if e := WriteCheckpoint(path, c); e != nil {
		t.Fatal(e)
	}
	got, e := ReadCheckpoint(path)
	if e != nil || got.Position != 3 {
		t.Fatal(got, e)
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(s.path, link)
	if _, e = Open(link); e == nil {
		t.Fatal("accepted symlink")
	}
	if _, e = Open(filepath.Join(t.TempDir(), "missing")); e == nil {
		t.Fatal("created reader store")
	}
}

func TestSQLiteVersionAndCancellation(t *testing.T) {
	s := fixture(t)
	var v string
	if e := s.db.QueryRow("select sqlite_version()").Scan(&v); e != nil {
		t.Fatal(e)
	}
	t.Log("embedded SQLite", v)
	if v < "3.51.3" {
		t.Fatal("SQLite lacks WAL-reset fix", v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.HighWater(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestPageBoundaryDoesNotCaptureFuture(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.Append(ctx, message("alpha", "1", at))
	high, e := s.HighWater(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s.Append(ctx, message("alpha", "2", at))
	rows, scan, e := s.ReadThrough(ctx, Query{Channels: []string{"alpha"}}, 0, high, 1)
	if e != nil || len(rows) != 1 || rows[0].ID != "1" || scan != high {
		t.Fatal(rows, scan, e)
	}
}

func TestClearRedactsOtherAuthorsReply(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.Append(ctx, message("alpha", "1", at))
	r := message("alpha", "2", at)
	r.Author.ID = "other"
	r.Reply = &automation.Reply{ParentID: "1", Text: "body"}
	s.Append(ctx, r)
	if _, _, e := s.Append(ctx, automation.Record{Type: "user_banned", Channel: r.Channel, TargetUserID: "user", ReceivedAt: at}); e != nil {
		t.Fatal(e)
	}
	rows, _, e := s.Snapshot(ctx, Query{Channels: []string{"alpha"}})
	if e != nil || len(rows) != 1 || rows[0].Reply.Text != "" {
		t.Fatal(rows, e)
	}
}
func TestRefreshBufferedModeration(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	r, _, e := s.Append(ctx, message("alpha", "1", at))
	if e != nil {
		t.Fatal(e)
	}
	s.Append(ctx, automation.Record{Type: "message_deleted", Channel: r.Channel, TargetMessageID: r.ID, ReceivedAt: at})
	got, visible, e := s.Refresh(ctx, r, false)
	if e != nil || visible || got.Text != "" {
		t.Fatal(got, visible, e)
	}
}

func TestDelayedMessagesRespectModerationWatermarks(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, _, e := s.Append(ctx, automation.Record{Type: "chat_cleared", Channel: automation.Channel{Login: "alpha"}, EventAt: at, ReceivedAt: at}); e != nil {
		t.Fatal(e)
	}
	r, _, e := s.Append(ctx, message("alpha", "late", at.Add(-time.Second)))
	if e != nil || !r.Deleted || r.Text != "" {
		t.Fatal(r, e)
	}
	future := message("alpha", "future", at.Add(time.Second))
	future.Reply = &automation.Reply{ParentID: "late", Text: "deleted preview"}
	got, _, e := s.Append(ctx, future)
	if e != nil || got.Reply.Text != "" {
		t.Fatal(got, e)
	}
}
func TestDelayedReplyRespectsDeletion(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.Append(ctx, automation.Record{Type: "message_deleted", Channel: automation.Channel{Login: "alpha"}, TargetMessageID: "parent", EventAt: at, ReceivedAt: at})
	r := message("alpha", "reply", at.Add(time.Second))
	r.Reply = &automation.Reply{ParentID: "parent", Text: "removed body"}
	got, _, e := s.Append(ctx, r)
	if e != nil || got.Reply.Text != "" {
		t.Fatal(got, e)
	}
}
func TestQuietCaptureMetadataPruned(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.BeginCapture(ctx, "alpha", "old", "irc", at.Add(-3*time.Hour))
	s.EndCapture(ctx, "old", at.Add(-2*time.Hour), true)
	s.AddGap(ctx, Gap{Channel: "alpha", Start: at.Add(-3 * time.Hour), End: at.Add(-2 * time.Hour), Cause: "network"})
	out, e := s.Prune(ctx, Retention{Age: time.Hour, MaxBytes: 1 << 30, Now: at}, false)
	if e != nil || out.Records != 0 {
		t.Fatal(out, e)
	}
	status, e := s.Status(ctx)
	if e != nil || len(status.Captures) != 0 || len(status.Gaps) != 0 {
		t.Fatal(status, e)
	}
}
func TestMigrationPreservesIdentity(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	r, _, e := s.Append(ctx, message("alpha", "1", time.Now().UTC()))
	if e != nil {
		t.Fatal(e)
	}
	id := s.UUID()
	cursor := s.Cursor(r.Sequence)
	if _, e = s.db.Exec("DROP TRIGGER event_search_insert; DROP TRIGGER event_search_update; DROP TRIGGER event_search_delete; DROP TABLE event_search; DROP TABLE moderation_boundaries; DROP TABLE retention_state; DROP TABLE store_policy; ALTER TABLE tombstones DROP COLUMN deleted_at; PRAGMA user_version=1"); e != nil {
		t.Fatal(e)
	}
	path := s.path
	s.Close()
	reopened, e := OpenWriter(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if reopened.UUID() != id {
		t.Fatal("migration changed identity")
	}
	if seq, e := reopened.ParseCursor(ctx, cursor); e != nil || seq != r.Sequence {
		t.Fatal(seq, e)
	}
}

func TestRetainedCoverageDoesNotHidePrunedWindow(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.BeginCapture(ctx, "alpha", "session", "irc", at.Add(-3*time.Hour))
	s.Heartbeat(ctx, "session", at)
	s.Append(ctx, message("alpha", "old", at.Add(-2*time.Hour)))
	s.Append(ctx, message("alpha", "new", at))
	if _, e := s.Prune(ctx, Retention{Age: time.Hour, MaxBytes: 1 << 30, Now: at}, false); e != nil {
		t.Fatal(e)
	}
	q := Query{Channels: []string{"alpha"}, Since: at.Add(-3 * time.Hour), TimeField: "received"}
	if e := s.CheckRetention(ctx, q); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	if _, _, e := s.Snapshot(ctx, q); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	q.AllowPartial = true
	if rows, _, e := s.Snapshot(ctx, q); e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}

func TestStatusReportsRetentionHealthAndPrivacy(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.BeginCapture(ctx, "alpha", "session", "irc", at)
	s.Prune(ctx, Retention{Age: 2 * time.Hour, MaxBytes: 16 << 20, Now: at}, false)
	status, e := s.Status(ctx)
	if e != nil || status.DatabaseSchema != 7 || !status.PrivateFiles || status.RetentionAge != "2h0m0s" || status.MaxBytes != 16<<20 || len(status.Captures) != 1 || !status.Captures[0].Healthy {
		t.Fatal(status, e)
	}
	if e = os.Chmod(s.path, 0644); e != nil {
		t.Fatal(e)
	}
	status, e = s.Status(ctx)
	if e != nil || status.PrivateFiles {
		t.Fatal(status, e)
	}
	os.Chmod(s.path, 0600)
}

func TestTailBoundaryAvoidsPayloadsAndSelectsPerChannel(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	s.BeginCapture(ctx, "alpha", "alpha-session", "irc", at.Add(-time.Minute))
	s.BeginCapture(ctx, "beta", "beta-session", "irc", at.Add(-time.Minute))
	s.Heartbeat(ctx, "alpha-session", at)
	s.Heartbeat(ctx, "beta-session", at)
	for _, r := range []automation.Record{message("alpha", "a1", at), message("beta", "b1", at), message("alpha", "a2", at), {Type: "notice", ID: "control", Channel: automation.Channel{Login: "alpha"}, ReceivedAt: at}, message("beta", "b2", at)} {
		if _, _, e := s.Append(ctx, r); e != nil {
			t.Fatal(e)
		}
	}
	selection, e := s.TailBoundary(ctx, Query{Channels: []string{"alpha", "beta"}, Last: 1})
	if e != nil || selection.Starts["alpha"] != 3 || selection.Starts["beta"] != 5 {
		t.Fatal(selection, e)
	}
	rows, _, e := s.ReadThrough(ctx, Query{Channels: []string{"alpha", "beta"}, AllEvents: true, ChannelStarts: selection.Starts}, 0, selection.HighWater, 256)
	if e != nil || len(rows) != 3 || rows[1].Type != "notice" {
		t.Fatal(rows, e)
	}
	selection, e = s.TailBoundary(ctx, Query{Channels: []string{"alpha", "missing"}, Last: 1000000, AllowPartial: true})
	if e != nil || len(selection.Warnings) < 2 || selection.Starts["alpha"] != 1 || selection.Starts["missing"] != selection.HighWater+1 {
		t.Fatal(selection, e)
	}
	if _, e = s.TailBoundary(ctx, Query{Channels: []string{"alpha"}, Last: 3}); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
}
