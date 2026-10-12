package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
)

// legacyPrivacyFixture writes the exact inconsistent state produced by the old
// message-only moderation predicate, bypassing today's corrected append path.
func legacyPrivacyFixture(t *testing.T, mode string) (string, string, int64, automation.Record) {
	t.Helper()
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	notice := message("alpha", "notice-id", at)
	notice.Type = "user_notice"
	notice.Text = "removed legacy notice"
	notice.SystemText = "removed legacy fallback"
	notice.NoticeParameters = map[string]string{"alias": "removed legacy notice"}
	notice.Fragments = []automation.Fragment{{Type: "text", Text: notice.Text}}
	if mode == "subtype" {
		notice.ID = "sub"
		notice.Reason = "sub"
	}
	stored, _, err := s.Append(ctx, notice)
	if err != nil {
		t.Fatal(err)
	}
	reply := message("alpha", "reply-id", at)
	reply.Reply = &automation.Reply{ParentID: notice.ID, Text: notice.Text}
	if _, _, err = s.Append(ctx, reply); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "tombstone":
		_, err = s.db.Exec("INSERT INTO tombstones VALUES(?,?,?)", "alpha", notice.ID, at.Add(time.Second).UnixNano())
	case "user":
		_, err = s.db.Exec("INSERT INTO moderation_boundaries VALUES(?,?,?,?)", "alpha", "user", "", at.Add(time.Second).UnixNano())
	case "channel":
		_, err = s.db.Exec("INSERT INTO moderation_boundaries VALUES(?,?,?,?)", "alpha", "", "", at.Add(time.Second).UnixNano())
	case "subtype":
		_, err = s.db.Exec("INSERT INTO tombstones VALUES(?,?,?)", "alpha", "original-uuid-lost-by-old-code", at.Add(time.Second).UnixNano())
	case "deleted-alias":
		_, err = s.db.Exec("UPDATE events SET deleted=1 WHERE seq=?", stored.Sequence)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=6"); err != nil {
		t.Fatal(err)
	}
	path, uuid := s.path, s.UUID()
	high, err := s.HighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return path, uuid, high, stored
}

func TestSchema7ReconcilesLegacyNoticePrivacy(t *testing.T) {
	for _, mode := range []string{"tombstone", "user", "channel", "subtype", "deleted-alias"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path, uuid, high, notice := legacyPrivacyFixture(t, mode)
			s, err := OpenWriter(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.UUID() != uuid {
				t.Fatal("migration changed store identity")
			}
			if got, err := s.HighWater(ctx); err != nil || got != high {
				t.Fatal("migration changed cursor positions", got, err)
			}
			status, err := s.Status(ctx)
			if err != nil || status.DatabaseSchema != 7 {
				t.Fatal(status, err)
			}
			row, visible, err := s.Refresh(ctx, notice, true)
			if err != nil || !visible || !row.Deleted || row.Text != "" || row.SystemText != "" || len(row.NoticeParameters) > 0 || len(row.Fragments) > 0 {
				t.Fatal("migration did not redact aliases", row, err)
			}
			var raw string
			if err = s.db.QueryRow("SELECT payload FROM events WHERE seq=?", notice.Sequence).Scan(&raw); err != nil || strings.Contains(raw, "removed legacy") {
				t.Fatal("raw persisted notice content survived migration", raw, err)
			}
			var reply string
			if err = s.db.QueryRow("SELECT payload FROM events WHERE message_id='reply-id'").Scan(&reply); err != nil || strings.Contains(reply, "removed legacy") {
				t.Fatal("legacy reply preview survived migration", reply, err)
			}
		})
	}
}

func TestReadonlySchema6ProtectsLegacyPrivacyWithoutMigration(t *testing.T) {
	for _, mode := range []string{"tombstone", "user", "channel", "subtype", "deleted-alias"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path, _, _, notice := legacyPrivacyFixture(t, mode)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			rows, _, err := s.ReadAfter(ctx, Query{AllEvents: true}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.ID == notice.ID && (!r.Deleted || r.Text != "" || r.SystemText != "" || len(r.NoticeParameters) > 0 || len(r.Fragments) > 0) {
					t.Fatal("read-only replay leaked legacy notice", r)
				}
				if r.Reply != nil && r.Reply.Text != "" {
					t.Fatal("read-only reply leaked removed notice", r)
				}
			}
			r, _, err := s.Refresh(ctx, notice, true)
			if err != nil || !r.Deleted || r.SystemText != "" || r.Text != "" {
				t.Fatal("read-only refresh leaked", r, err)
			}
			search, err := s.Search(ctx, "body", nil, 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range search {
				if r.Reply != nil && r.Reply.Text != "" {
					t.Fatal("read-only search leaked reply", r)
				}
			}
			var version int
			if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
				t.Fatal("read-only open mutated schema", version, err)
			}
			var raw string
			if err = s.db.QueryRow("SELECT payload FROM events WHERE seq=?", notice.Sequence).Scan(&raw); err != nil || !strings.Contains(raw, "removed legacy") {
				t.Fatal("fixture/body mutated by read-only protection", raw, err)
			}
		})
	}
}

func TestSchema7KeepsUnrelatedAndPostDeleteNotifications(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for i, id := range []string{"actual-notice-uuid", "sub"} {
		r := message("alpha", id, at.Add(time.Duration(i)*2*time.Second))
		r.Type = "user_notice"
		r.SystemText = "kept"
		if _, _, err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO tombstones VALUES('alpha','unrelated-uuid',?); PRAGMA user_version=6", at.Add(time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.Close()
	writer, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	rows, _, err := writer.ReadAfter(ctx, Query{AllEvents: true}, 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, r := range rows {
		if r.Deleted || r.SystemText != "kept" {
			t.Fatal("migration over-redacted valid/non-overlapping notice", r)
		}
	}
	// Tail/window projection remains functional for a read-only legacy schema.
	if _, err := writer.db.Exec("PRAGMA user_version=6"); err != nil {
		t.Fatal(err)
	}
	legacy, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	for i := 0; i < 2; i++ {
		if _, _, err = writer.Append(ctx, message("alpha", fmt.Sprint(i), at)); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, _, err := legacy.Snapshot(ctx, Query{Last: 1, AllowPartial: true})
	if err != nil || len(snapshots) != 1 {
		t.Fatal(snapshots, err)
	}
	payload, _ := json.Marshal(snapshots[0])
	if len(payload) == 0 {
		t.Fatal("empty snapshot")
	}
}

func TestLegacySubtypeUnknownParentReplyProtection(t *testing.T) {
	for _, writerMode := range []bool{false, true} {
		t.Run(fmt.Sprint(writerMode), func(t *testing.T) {
			path, _, _, _ := legacyPrivacyFixture(t, "subtype")
			// The old subtype ID cannot identify the original UUID in a later reply.
			patch, err := OpenWriter(path)
			if err != nil {
				t.Fatal(err)
			}
			legacyReply := message("alpha", "unmatched-reply", time.Now().UTC())
			legacyReply.Reply = &automation.Reply{ParentID: "uuid-not-in-retained-events", Text: "removed legacy notice"}
			// Restore pre-upgrade inconsistent payload so both read-only/migration paths see it.
			payload, _ := json.Marshal(legacyReply)
			if _, err = patch.db.Exec("INSERT INTO events(channel,channel_id,kind,message_id,delivery_id,event_at,received_at,deleted,payload) VALUES('alpha','alpha','message','unmatched-reply','',1,1,0,?); PRAGMA user_version=6", payload); err != nil {
				t.Fatal(err)
			}
			patch.Close()
			var s *Store
			if writerMode {
				s, err = OpenWriter(path)
			} else {
				s, err = Open(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			rows, _, err := s.ReadAfter(context.Background(), Query{AllEvents: true}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.Reply != nil && r.Reply.Text != "" {
					t.Fatal("ambiguous legacy notice leaked through preview", r)
				}
			}
			if writerMode {
				late := message("alpha", "late-unmatched-reply", time.Now().UTC())
				late.Reply = &automation.Reply{ParentID: "another-unretained-uuid", Text: "removed legacy notice"}
				r, _, err := s.Append(context.Background(), late)
				if err != nil || r.Reply.Text != "" {
					t.Fatal("late reply restored legacy removed text", r, err)
				}
			}
		})
	}
}

func TestLegacyUserMessagesClearControlReconcilesAllTextAndLateDelivery(t *testing.T) {
	for _, writerMode := range []bool{false, true} {
		t.Run(fmt.Sprint(writerMode), func(t *testing.T) {
			ctx := context.Background()
			s := fixture(t)
			at := time.Now().UTC()
			normal := message("alpha", "cleared-message", at)
			notice := normal
			notice.ID = "cleared-notice"
			notice.Type = "user_notice"
			notice.SystemText = "removed by user clear"
			notice.NoticeParameters = map[string]string{"copy": notice.Text}
			other := normal
			other.ID = "other-author"
			other.Author.ID = "other"
			reply := other
			reply.ID = "reply-to-cleared"
			reply.Reply = &automation.Reply{ParentID: notice.ID, Text: notice.Text}
			if _, err := s.AppendBatch(ctx, []automation.Record{normal, notice, other, reply}); err != nil {
				t.Fatal(err)
			}
			control := automation.Record{Type: "user_messages_cleared", Channel: normal.Channel, TargetUserID: "user", EventAt: at.Add(time.Second), ReceivedAt: at.Add(time.Second)}
			payload, _ := json.Marshal(control)
			if _, err := s.db.Exec("INSERT INTO events(channel,channel_id,kind,message_id,delivery_id,event_at,received_at,deleted,payload) VALUES('alpha','alpha','user_messages_cleared','','',?,?,0,?); PRAGMA user_version=6", control.EventAt.UnixNano(), control.ReceivedAt.UnixNano(), payload); err != nil {
				t.Fatal(err)
			}
			path := s.path
			s.Close()
			var reader *Store
			var err error
			if writerMode {
				reader, err = OpenWriter(path)
			} else {
				reader, err = Open(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			rows, _, err := reader.ReadAfter(ctx, Query{AllEvents: true}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.ID == normal.ID || r.ID == notice.ID {
					if !r.Deleted || r.Text != "" || r.SystemText != "" || len(r.NoticeParameters) > 0 {
						t.Fatal("legacy base clear leaked", r)
					}
				}
				if r.ID == other.ID && r.Deleted {
					t.Fatal("user clear became channel clear", r)
				}
				if r.Reply != nil && r.Reply.Text != "" {
					t.Fatal("legacy base clear leaked preview", r)
				}
			}
			visible, _, err := reader.ReadAfter(ctx, Query{}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range visible {
				if r.Author.ID == "user" {
					t.Fatal("cleared message visible", r)
				}
			}
			if writerMode {
				late := notice
				late.ID = "late-cleared-notice"
				r, _, err := reader.Append(ctx, late)
				if err != nil || !r.Deleted || r.Text != "" || r.SystemText != "" {
					t.Fatal("late notice restored after legacy clear", r, err)
				}
			}
		})
	}
}

func TestCurrentUserMessagesClearAndMissingTargetGuard(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	r := message("alpha", "current", at)
	n := r
	n.ID = "current-notice"
	n.Type = "user_notice"
	if _, err := s.AppendBatch(ctx, []automation.Record{r, n}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Append(ctx, automation.Record{Type: "user_messages_cleared", Channel: r.Channel, ReceivedAt: at}); err == nil {
		t.Fatal("missing target became blanket clear")
	}
	if _, _, err := s.Append(ctx, automation.Record{Type: "user_messages_cleared", Channel: r.Channel, TargetUserID: r.Author.ID, ReceivedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, original := range []automation.Record{r, n} {
		original.ID += "-delayed"
		stored, _, err := s.Append(ctx, original)
		if err != nil || !stored.Deleted || stored.Text != "" {
			t.Fatal(stored, err)
		}
	}
	rows, _, err := s.ReadAfter(ctx, Query{AllEvents: true}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Type == "message" || r.Type == "user_notice" {
			if !r.Deleted || r.Text != "" {
				t.Fatal("read-scoped user clear missed body", r)
			}
		}
	}
}

func TestMigratedEmptyNoticeCannotReappearWithMissingIdentity(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	at := time.Now().UTC()
	notice := message("alpha", "", at)
	notice.Type = "user_notice"
	notice.SystemText = "removed empty-id fallback"
	if _, _, err := s.Append(ctx, notice); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO tombstones VALUES('alpha','lost-original-id',?); PRAGMA user_version=6", at.Add(time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.Close()
	writer, err := OpenExistingWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	late, _, err := writer.Append(ctx, notice)
	if err != nil || !late.Deleted || late.Text != "" || late.SystemText != "" {
		t.Fatal("late identityless notice restored removed content", late, err)
	}
	notice.ID = "newly-recovered-uuid"
	late, _, err = writer.Append(ctx, notice)
	if err != nil || !late.Deleted || late.Text != "" {
		t.Fatal("recovered late UUID bypassed conservative notice suppression", late, err)
	}
	notice.ID = "post-delete-uuid"
	notice.EventAt = at.Add(2 * time.Second)
	notice.ReceivedAt = notice.EventAt
	kept, _, err := writer.Append(ctx, notice)
	if err != nil || kept.Deleted || kept.Text == "" {
		t.Fatal("conservative suppression affected a later notification", kept, err)
	}
}

func TestOpenExistingWriterNeverCreatesMissingOrEmptyStores(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "not-created", "history.db")
	if store, err := OpenExistingWriter(missing); err == nil {
		store.Close()
		t.Fatal("missing store created")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("missing parent created", err)
	}
	private := filepath.Join(parent, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(private, "missing.db")
	if store, err := OpenExistingWriter(path); err == nil {
		store.Close()
		t.Fatal("missing file created")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("database created", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenExistingWriter(path); err == nil {
		store.Close()
		t.Fatal("empty database initialized")
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatal("empty file changed", info, err)
	}
}

func TestCanceledExistingWriterDoesNotTouchFilesystem(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "missing", "history.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if store, err := OpenExistingWriterContext(ctx, path); !errors.Is(err, context.Canceled) {
		if store != nil {
			store.Close()
		}
		t.Fatal("cancellation lost", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("canceled upgrade created parent", err)
	}
}
