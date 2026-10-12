package history

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
)

func TestBatchRollbackAndSearchPrivacy(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	first := message("alpha", "1", at)
	first.Text = `literal OR phrase`
	if _, err := s.AppendBatch(ctx, []automation.Record{first, {Type: "send_result"}}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if high, _ := s.HighWater(ctx); high != 0 {
		t.Fatal("partial commit", high)
	}
	if _, err := s.AppendBatch(ctx, []automation.Record{first, first, message("alpha", "2", at)}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Search(ctx, "literal OR phrase", []string{"alpha"}, 10)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if _, _, err = s.Append(ctx, automation.Record{Type: "message_deleted", Channel: first.Channel, TargetMessageID: "1", ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Search(ctx, "literal", nil, 10)
	if err != nil || len(rows) != 0 {
		t.Fatal("moderated text searchable", rows, err)
	}
	// Inspect the index itself: privacy must hold even without the public filter.
	var indexed int
	if err = s.db.QueryRow("SELECT count(*) FROM event_search WHERE event_search MATCH 'literal'").Scan(&indexed); err != nil || indexed != 0 {
		t.Fatal(indexed, err)
	}
}

func TestSQLiteDiskFullRollsBackBatch(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	r := message("alpha", "full", time.Now().UTC())
	r.Text = strings.Repeat("x", 256*1024)
	_, err := s.AppendBatch(ctx, []automation.Record{r})
	if !errors.Is(err, ErrStorageFull) {
		t.Fatalf("expected real SQLite full: %v", err)
	}
	if high, e := s.HighWater(ctx); e != nil || high != 0 {
		t.Fatal("failed transaction persisted", high, e)
	}
}

func TestCorruptDatabaseRejected(t *testing.T) {
	s := fixture(t)
	path := s.path
	s.Close()
	if err := os.WriteFile(path, []byte(strings.Repeat("corrupt", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := OpenWriter(path); err == nil {
		reopened.Close()
		t.Fatal("corrupt database accepted")
	}
}

// Subprocess holds a real writer or unfinished DDL transaction. Parent kills it
// after readiness, proving kernel lock release and SQLite crash rollback.
func TestStorageCrashHelper(t *testing.T) {
	mode := os.Getenv("TWI_STORAGE_CRASH_MODE")
	if mode == "" {
		return
	}
	s, err := OpenWriter(os.Getenv("TWI_STORAGE_CRASH_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().UTC()
	if mode == "capture" {
		if err = s.BeginCapture(ctx, "alpha", "killed", "irc", at); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.Append(ctx, message("alpha", "committed", at)); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err = s.db.Exec("BEGIN IMMEDIATE; CREATE TABLE interrupted_migration(value TEXT); PRAGMA user_version=99;"); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("ready")
	select {}
}

func TestProcessKillRecoveryAndInterruptedMigration(t *testing.T) {
	for _, mode := range []string{"capture", "migration"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private", "chat.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestStorageCrashHelper$")
			cmd.Env = append(os.Environ(), "TWI_STORAGE_CRASH_MODE="+mode, "TWI_STORAGE_CRASH_PATH="+path)
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
			ready := make(chan bool, 1)
			go func() { scan := bufio.NewScanner(pipe); ready <- scan.Scan() && scan.Text() == "ready" }()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("child never ready")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child timeout")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			cmd.Wait()
			s, err := OpenWriter(path)
			if err != nil {
				t.Fatal("restart failed", err)
			}
			defer s.Close()
			if mode == "capture" {
				status, err := s.Status(context.Background())
				if err != nil || len(status.Captures) != 1 || status.Captures[0].Clean || status.Captures[0].Ended == nil {
					t.Fatal(status, err)
				}
				high, err := s.HighWater(context.Background())
				if err != nil || high != 1 {
					t.Fatal(high, err)
				}
			} else {
				var count int
				if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='interrupted_migration'").Scan(&count); err != nil || count != 0 {
					t.Fatal("DDL survived crash", count, err)
				}
			}
		})
	}
}

func TestPruneBetweenPageAndEmission(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	r, _, err := s.Append(ctx, message("alpha", "old", at))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rows, _, err := reader.ReadAfter(ctx, Query{}, 0, 256)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if _, err = s.Prune(ctx, Retention{Age: time.Minute, MaxBytes: 1 << 30}, false); err != nil {
		t.Fatal(err)
	}
	if _, visible, err := reader.Refresh(ctx, r, false); err != ErrCursorExpired || visible {
		t.Fatal("pruned buffered row emitted", visible, err)
	}
}

func TestSearchMigrationAndPrune(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	at := time.Now().UTC().Add(-time.Hour)
	r := message("alpha", "legacy", at)
	r.Text = "indexed legacy phrase"
	if _, _, err := s.Append(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TRIGGER event_search_insert; DROP TRIGGER event_search_update; DROP TRIGGER event_search_delete; DROP TABLE event_search; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	path := s.path
	uuid := s.UUID()
	s.Close()
	migrated, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if migrated.UUID() != uuid {
		t.Fatal("search migration changed store identity")
	}
	rows, err := migrated.Search(ctx, "legacy phrase", nil, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	rows, err = migrated.Search(ctx, `" OR *`, nil, 100)
	if err != nil || len(rows) != 0 {
		t.Fatal("FTS input was executable", rows, err)
	}
	if _, err = migrated.Prune(ctx, Retention{Age: time.Minute, MaxBytes: 1 << 30}, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = migrated.db.QueryRow("SELECT count(*) FROM event_search").Scan(&count); err != nil || count != 0 {
		t.Fatal("prune retained search body", count, err)
	}
}

func TestBatchReturnedRecordsHonorLaterModeration(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	r := message("alpha", "in-batch", at)
	results, err := s.AppendBatch(ctx, []automation.Record{r, {Type: "message_deleted", Channel: r.Channel, TargetMessageID: r.ID, ReceivedAt: at}})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Record.Deleted || results[0].Record.Text != "" || results[0].Record.Sequence == 0 || results[0].Record.Cursor == nil {
		t.Fatal("batch returned stale moderated body", results)
	}
}

func TestUserNoticeDeletionRedactsAliasesAndDelayedDelivery(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprint(delayed), func(t *testing.T) {
			ctx := context.Background()
			s := fixture(t)
			at := time.Now().UTC()
			notice := message("alpha", "notification-id", at)
			notice.Type = "user_notice"
			notice.Text = "removed subscriber body"
			notice.SystemText = "removed fallback body"
			notice.NoticeParameters = map[string]string{"body-copy": "removed subscriber body"}
			notice.Fragments = []automation.Fragment{{Type: "text", Text: notice.Text}}
			deletion := automation.Record{Type: "message_deleted", Channel: notice.Channel, TargetMessageID: notice.ID, ReceivedAt: at.Add(time.Second)}
			if delayed {
				if _, _, err := s.Append(ctx, deletion); err != nil {
					t.Fatal(err)
				}
			}
			stored, _, err := s.Append(ctx, notice)
			if err != nil {
				t.Fatal(err)
			}
			reply := message("alpha", "reply-to-notice", at)
			reply.Reply = &automation.Reply{ParentID: notice.ID, Text: notice.Text}
			if _, _, err = s.Append(ctx, reply); err != nil {
				t.Fatal(err)
			}
			if !delayed {
				if _, _, err = s.Append(ctx, deletion); err != nil {
					t.Fatal(err)
				}
			}
			if _, inserted, err := s.Append(ctx, notice); err != nil || inserted {
				t.Fatal("duplicate notice resurrected", inserted, err)
			}
			fresh, visible, err := s.Refresh(ctx, stored, true)
			if err != nil || !visible || !fresh.Deleted || fresh.Text != "" || fresh.SystemText != "" || len(fresh.Fragments) != 0 || len(fresh.NoticeParameters) != 0 {
				t.Fatal("buffered notice retained deleted body", fresh, err)
			}
			rows, _, err := s.ReadAfter(ctx, Query{AllEvents: true}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.ID == notice.ID && (!row.Deleted || row.Text != "" || row.SystemText != "" || len(row.Fragments) > 0 || len(row.NoticeParameters) > 0) {
					t.Fatal("replay leaked notice body", row)
				}
				if row.Reply != nil && row.Reply.Text != "" {
					t.Fatal("reply retained removed notice preview", row)
				}
			}
			var raw string
			if err = s.db.QueryRow("SELECT payload FROM events WHERE kind='user_notice'").Scan(&raw); err != nil || strings.Contains(raw, "removed ") {
				t.Fatal("persisted alias was not redacted", raw, err)
			}
		})
	}
}

func TestUserClearAndChatClearRedactNotifications(t *testing.T) {
	for _, target := range []string{"user", ""} {
		t.Run(target, func(t *testing.T) {
			ctx := context.Background()
			s := fixture(t)
			at := time.Now().UTC()
			first := message("alpha", "notice-first", at)
			first.Type = "user_notice"
			first.SystemText = "private fallback"
			first.NoticeParameters = map[string]string{"text": "private alias"}
			second := first
			second.ID = "notice-second"
			second.Author.ID = "other"
			if _, err := s.AppendBatch(ctx, []automation.Record{first, second}); err != nil {
				t.Fatal(err)
			}
			clear := automation.Record{Type: "chat_cleared", Channel: first.Channel, TargetUserID: target, ReceivedAt: at.Add(time.Second)}
			if _, _, err := s.Append(ctx, clear); err != nil {
				t.Fatal(err)
			}
			// A delayed distinct notification for the cleared author must remain redacted.
			delayed := first
			delayed.ID = "late-notice"
			r, _, err := s.Append(ctx, delayed)
			if err != nil || !r.Deleted || r.SystemText != "" || len(r.NoticeParameters) != 0 {
				t.Fatal("clear bypassed delayed notice", r, err)
			}
			rows, _, err := s.ReadAfter(ctx, Query{AllEvents: true}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Type != "user_notice" {
					continue
				}
				mustDelete := target == "" || row.Author.ID == target
				if mustDelete && (!row.Deleted || row.Text != "" || row.SystemText != "" || len(row.NoticeParameters) != 0) {
					t.Fatal("clear leaked notification", row)
				}
				if !mustDelete && row.Deleted {
					t.Fatal("user clear affected other user", row)
				}
			}
		})
	}
}
