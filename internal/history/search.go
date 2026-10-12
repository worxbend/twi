package history

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/worxbend/twi/internal/automation"
)

// ensureSearch adds an external-content FTS index in one restart-safe migration.
// Triggers remove moderated and retained-away content in the same transaction.
func (s *Store) ensureSearch(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 6 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
 CREATE VIRTUAL TABLE event_search USING fts5(text,channel UNINDEXED,tokenize='unicode61');
 INSERT INTO event_search(rowid,text,channel) SELECT seq,json_extract(payload,'$.text'),channel FROM events WHERE kind='message' AND deleted=0;
 CREATE TRIGGER event_search_insert AFTER INSERT ON events WHEN NEW.kind='message' AND NEW.deleted=0 BEGIN INSERT INTO event_search(rowid,text,channel) VALUES(NEW.seq,json_extract(NEW.payload,'$.text'),NEW.channel); END;
 CREATE TRIGGER event_search_delete AFTER DELETE ON events BEGIN DELETE FROM event_search WHERE rowid=OLD.seq; END;
 CREATE TRIGGER event_search_update AFTER UPDATE OF payload,deleted ON events BEGIN DELETE FROM event_search WHERE rowid=OLD.seq; INSERT INTO event_search(rowid,text,channel) SELECT NEW.seq,json_extract(NEW.payload,'$.text'),NEW.channel WHERE NEW.kind='message' AND NEW.deleted=0; END;
 PRAGMA user_version=6;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Search matches a literal phrase. FTS operators and SQL are never evaluated
// from user input. Results contain only retained, unmoderated messages.
func (s *Store) Search(ctx context.Context, phrase string, channels []string, limit int) ([]automation.Record, error) {
	if strings.TrimSpace(phrase) == "" || len(phrase) > 4096 || limit < 1 || limit > 1000 {
		return nil, errors.New("search requires a phrase up to 4096 bytes and limit between 1 and 1000")
	}
	match := `"` + strings.ReplaceAll(phrase, `"`, `""`) + `"`
	query := `SELECT ` + s.protectedPayloadColumns() + ` FROM event_search f JOIN events e ON e.seq=f.rowid WHERE event_search MATCH ? AND ` + s.visibleMessageSQL()
	args := []any{match}
	if len(channels) > 0 {
		marks := make([]string, len(channels))
		for i, c := range channels {
			marks[i] = "?"
			args = append(args, c)
		}
		query += " AND e.channel IN (" + strings.Join(marks, ",") + ")"
	}
	query += " ORDER BY e.seq DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []automation.Record
	for rows.Next() {
		var seq int64
		var data []byte
		var deleted, replyDeleted bool
		if err = rows.Scan(&seq, &data, &deleted, &replyDeleted); err != nil {
			return nil, err
		}
		var r automation.Record
		if err = json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		protectDecoded(&r, deleted, replyDeleted)
		r.Sequence = seq
		r.Source = "history"
		c := s.Cursor(seq)
		r.Cursor = &c
		out = append(out, r)
	}
	return out, rows.Err()
}
