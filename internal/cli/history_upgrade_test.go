package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
)

func TestHistoryUpgradeRepairsLegacyNoticeWithoutRetentionChanges(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "history.sqlite3")
	store, err := history.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	_, _, err = store.Append(ctx, automation.Record{SchemaVersion: 1, Type: "user_notice", ID: "unique-notice", Channel: automation.Channel{Login: "alpha"}, Author: automation.Author{ID: "123"}, Text: "removed subscriber body", EventAt: at, ReceivedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, "INSERT INTO tombstones(channel,message_id,deleted_at) VALUES(?,?,?); PRAGMA user_version=6", "alpha", "unique-notice", at.Add(time.Second).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runHistory(ctx, []string{"upgrade", "--history", path}, &stdout, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var after history.Status
	if err := json.Unmarshal(stdout.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.DatabaseSchema != 7 || after.UUID != before.UUID || after.HighWater != before.HighWater || after.RetentionAge != before.RetentionAge || after.MaxBytes != before.MaxBytes {
		t.Fatal("upgrade changed history identity/sequence/retention", after)
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var text string
	var deleted int
	if err := database.QueryRowContext(ctx, "SELECT deleted,json_extract(payload,'$.text') FROM events WHERE message_id='unique-notice'").Scan(&deleted, &text); err != nil || deleted != 1 || text != "" {
		t.Fatal("legacy notice retained removed text", deleted, err)
	}
}

func TestHistoryUpgradeAndPruneNeverCreateMissingStore(t *testing.T) {
	for _, command := range [][]string{{"upgrade"}, {"prune"}, {"prune", "--dry-run"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing-parent", "history.sqlite3")
			var stdout, stderr bytes.Buffer
			arguments := append(append([]string{}, command...), "--history", path)
			if code := runHistory(context.Background(), arguments, &stdout, &stderr); code == 0 {
				t.Fatal("missing store accepted")
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("maintenance created missing directory", err)
			}
		})
	}
}

func TestHistoryUpgradeCanceledBeforeFilesystemAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "missing-parent", "history.sqlite3")
	var stdout, stderr bytes.Buffer
	if code := runHistory(ctx, []string{"upgrade", "--history", path}, &stdout, &stderr); code == 0 {
		t.Fatal("canceled upgrade succeeded")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("canceled upgrade created a directory", err)
	}
}
