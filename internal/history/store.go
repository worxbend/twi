// Package history stores observed chat and capture coverage in private SQLite state.
package history

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/twitch"
	"modernc.org/sqlite"
)

var ErrStorageFull = errors.New("storage_full")
var ErrIncomplete = errors.New("history_incomplete")
var ErrCursorExpired = errors.New("cursor_expired")
var ErrWriterBusy = errors.New("history writer already active")

type Store struct {
	db         *sql.DB
	path, uuid string
	lock       *os.File
	mu         sync.Mutex
	schema     int
}
type Query struct {
	Channels                []string
	Last                    int
	Since, Until            time.Time
	TimeField               string
	AllEvents, AllowPartial bool
	ChannelStarts           map[string]int64
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "twi", "history.sqlite3"), nil
}
func Open(path string) (*Store, error)       { return open(context.Background(), path, false, false) }
func OpenWriter(path string) (*Store, error) { return open(context.Background(), path, true, false) }

// OpenExistingWriter upgrades/maintains an existing private store without creating one.
func OpenExistingWriter(path string) (*Store, error) {
	return OpenExistingWriterContext(context.Background(), path)
}

// OpenExistingWriterContext bounds migrations by both command cancellation and startup timeout.
func OpenExistingWriterContext(ctx context.Context, path string) (*Store, error) {
	return open(ctx, path, true, true)
}
func open(parent context.Context, path string, writer, existing bool) (s *Store, err error) {
	if parent == nil {
		parent = context.Background()
	}
	if err = parent.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if writer && !existing {
		if err = privateDirectory(filepath.Dir(path)); err != nil {
			return nil, err
		}
	}
	if !writer || existing {
		if err = validateDirectory(filepath.Dir(path)); err != nil {
			return nil, err
		}
	}
	if err = checkFile(path, !writer || existing); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm", ".lock"} {
		if err = checkFile(path+suffix, false); err != nil {
			return nil, err
		}
	}
	s = &Store{path: path}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if writer {
		s.lock, err = lockWriter(path + ".lock")
		if err != nil {
			return nil, err
		}
		flags := os.O_RDWR
		if !existing {
			flags |= os.O_CREATE
		}
		f, e := os.OpenFile(path, flags, 0600)
		if e != nil {
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	if !writer {
		q.Set("mode", "ro")
	} else if existing {
		q.Set("mode", "rw")
	}
	q.Add("_pragma", "busy_timeout(3000)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	s.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if err = s.db.PingContext(ctx); err != nil {
		return nil, err
	}
	var version int
	err = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if err != nil {
		return nil, err
	}
	if version > 7 {
		return nil, fmt.Errorf("unsupported newer history schema %d", version)
	}
	if !writer && version > 0 && version < 6 {
		return nil, fmt.Errorf("history schema %d requires writer upgrade; run twi history upgrade --history PATH before reading", version)
	}
	if version == 0 {
		if !writer || existing {
			return nil, errors.New("history schema missing")
		}
		if err = s.initialize(ctx); err != nil {
			return nil, err
		}
	}
	if writer && version == 1 {
		if _, err = s.db.ExecContext(ctx, `BEGIN IMMEDIATE; CREATE TABLE moderation_boundaries(channel TEXT NOT NULL,user_id TEXT NOT NULL,user_login TEXT NOT NULL,before_at INTEGER NOT NULL,PRIMARY KEY(channel,user_id,user_login)); PRAGMA user_version=2; COMMIT;`); err != nil {
			return nil, err
		}
	}
	if writer && (version == 1 || version == 2) {
		if _, err = s.db.ExecContext(ctx, `BEGIN IMMEDIATE; ALTER TABLE tombstones ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0; PRAGMA user_version=3; COMMIT;`); err != nil {
			return nil, err
		}
	}
	if writer && version > 0 && version < 4 {
		if _, err = s.db.ExecContext(ctx, `BEGIN IMMEDIATE; CREATE TABLE retention_state(channel TEXT PRIMARY KEY,event_at INTEGER NOT NULL,received_at INTEGER NOT NULL); PRAGMA user_version=4; COMMIT;`); err != nil {
			return nil, err
		}
	}
	if writer && version > 0 && version < 5 {
		if _, err = s.db.ExecContext(ctx, `BEGIN IMMEDIATE; CREATE TABLE store_policy(singleton INTEGER PRIMARY KEY CHECK(singleton=1),retention_age INTEGER NOT NULL,max_bytes INTEGER NOT NULL); INSERT INTO store_policy VALUES(1,604800000000000,1073741824); PRAGMA user_version=5; COMMIT;`); err != nil {
			return nil, err
		}
	}
	if writer {
		if err = s.ensureSearch(ctx); err != nil {
			return nil, err
		}
		if err = s.ensureRedaction(ctx); err != nil {
			return nil, err
		}
	}
	if writer {
		if _, err = s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=256; PRAGMA journal_size_limit=4194304;"); err != nil {
			return nil, err
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE capture_intervals SET ended=heartbeat, clean=0 WHERE ended IS NULL"); err != nil {
			return nil, err
		}
	}
	if err = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&s.schema); err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT uuid FROM store_metadata WHERE singleton=1").Scan(&s.uuid)
	if err != nil {
		return nil, err
	}
	if writer {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if e := os.Chmod(path+suffix, 0600); e != nil && !os.IsNotExist(e) {
				return nil, e
			}
		}
	}
	return s, nil
}
func (s *Store) initialize(ctx context.Context) error {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return e
	}
	id := hex.EncodeToString(b)
	_, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL;
 BEGIN IMMEDIATE;
 CREATE TABLE store_metadata(singleton INTEGER PRIMARY KEY CHECK(singleton=1),uuid TEXT NOT NULL,low_water INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT,channel TEXT NOT NULL,channel_id TEXT NOT NULL,kind TEXT NOT NULL,message_id TEXT NOT NULL,delivery_id TEXT NOT NULL,event_at INTEGER NOT NULL,received_at INTEGER NOT NULL,deleted INTEGER NOT NULL DEFAULT 0,payload BLOB NOT NULL);
 CREATE UNIQUE INDEX event_message_identity ON events(channel,message_id,kind) WHERE message_id<>'';
 CREATE UNIQUE INDEX event_delivery_identity ON events(channel,delivery_id,kind) WHERE delivery_id<>'';
 CREATE INDEX event_channel_seq ON events(channel,seq);
 CREATE INDEX event_channel_time ON events(channel,event_at,seq);
 CREATE INDEX event_channel_received ON events(channel,received_at,seq);
 CREATE TABLE capture_intervals(id INTEGER PRIMARY KEY,channel TEXT NOT NULL,session TEXT NOT NULL,transport TEXT NOT NULL,started INTEGER NOT NULL,ended INTEGER,heartbeat INTEGER NOT NULL,clean INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX capture_channel_time ON capture_intervals(channel,started);
 CREATE TABLE capture_gaps(id INTEGER PRIMARY KEY,channel TEXT NOT NULL,started INTEGER NOT NULL,ended INTEGER NOT NULL,cause TEXT NOT NULL,lost INTEGER);
 CREATE TABLE tombstones(channel TEXT NOT NULL,message_id TEXT NOT NULL,deleted_at INTEGER NOT NULL,PRIMARY KEY(channel,message_id));
 CREATE TABLE moderation_boundaries(channel TEXT NOT NULL,user_id TEXT NOT NULL,user_login TEXT NOT NULL,before_at INTEGER NOT NULL,PRIMARY KEY(channel,user_id,user_login));
 CREATE TABLE retention_state(channel TEXT PRIMARY KEY,event_at INTEGER NOT NULL,received_at INTEGER NOT NULL);
 CREATE TABLE store_policy(singleton INTEGER PRIMARY KEY CHECK(singleton=1),retention_age INTEGER NOT NULL,max_bytes INTEGER NOT NULL); INSERT INTO store_policy VALUES(1,604800000000000,1073741824);
 PRAGMA user_version=5;`+"INSERT INTO store_metadata(singleton,uuid) VALUES(1,'"+id+"'); COMMIT;")
	if err != nil {
		return err
	}
	return nil
}
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		e := unlockWriter(s.lock)
		s.lock = nil
		if err == nil {
			err = e
		}
	}
	return err
}
func (s *Store) UUID() string { return s.uuid }
func (s *Store) Cursor(seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s.uuid + ":" + strconv.FormatInt(seq, 10)))
}
func (s *Store) ParseCursor(ctx context.Context, cursor string) (int64, error) {
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	if e != nil {
		return 0, ErrCursorExpired
	}
	p := strings.Split(string(b), ":")
	if len(p) != 2 || p[0] != s.uuid {
		return 0, ErrCursorExpired
	}
	seq, e := strconv.ParseInt(p[1], 10, 64)
	if e != nil || seq < 0 {
		return 0, ErrCursorExpired
	}
	var low, high int64
	if e = s.db.QueryRowContext(ctx, "SELECT low_water,COALESCE((SELECT seq FROM sqlite_sequence WHERE name='events'),0) FROM store_metadata").Scan(&low, &high); e != nil {
		return 0, e
	}
	if (low > 0 && seq <= low) || seq > high {
		return 0, ErrCursorExpired
	}
	return seq, nil
}
func (s *Store) HighWater(ctx context.Context) (int64, error) {
	var n int64
	e := s.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='events'),0)").Scan(&n)
	return n, e
}

// AppendResult describes one input record after its entire batch commits.
type AppendResult struct {
	Record   automation.Record
	Inserted bool
}

func (s *Store) Append(ctx context.Context, r automation.Record) (automation.Record, bool, error) {
	results, err := s.AppendBatch(ctx, []automation.Record{r})
	if err != nil {
		return r, false, err
	}
	return results[0].Record, results[0].Inserted, nil
}

// AppendBatch atomically commits at most 100 observed records, in input order.
// Moderation in a batch affects earlier records and prevents later resurrection.
func (s *Store) AppendBatch(ctx context.Context, records []automation.Record) ([]AppendResult, error) {
	if s.lock == nil {
		return nil, errors.New("history opened read-only")
	}
	if len(records) == 0 || len(records) > 100 {
		return nil, errors.New("history batch requires 1 to 100 records")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := reserveDisk(s.path); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	results := make([]AppendResult, 0, len(records))
	hasModeration := false
	for _, record := range records {
		if record.Type != "message" {
			hasModeration = true
		}
		r, inserted, err := s.appendRecord(ctx, tx, record)
		if err != nil {
			return nil, storageError(err)
		}
		results = append(results, AppendResult{r, inserted})
	}
	if hasModeration {
		for i := range results {
			if !results[i].Inserted {
				continue
			}
			seq := results[i].Record.Sequence
			var payload []byte
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM events WHERE seq=?", seq).Scan(&payload); err != nil {
				return nil, storageError(err)
			}
			if err := json.Unmarshal(payload, &results[i].Record); err != nil {
				return nil, err
			}
			results[i].Record.Sequence = seq
			cursor := s.Cursor(seq)
			results[i].Record.Cursor = &cursor
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, storageError(err)
	}
	return results, nil
}

func (s *Store) appendRecord(ctx context.Context, tx *sql.Tx, r automation.Record) (automation.Record, bool, error) {
	if r.Type == "send_result" || r.Source == "local" {
		return r, false, errors.New("only observed records may enter history")
	}
	r.SchemaVersion = 1
	channel, err := twitch.ParseChannel(r.Channel.Login)
	if err != nil {
		return r, false, err
	}
	r.Channel.Login = channel.String()
	if r.ReceivedAt.IsZero() {
		r.ReceivedAt = time.Now().UTC()
	}
	if r.EventAt.IsZero() {
		r.EventAt = r.ReceivedAt
		r.TimeSource = "received"
	}
	r.Source = "history"
	r.Cursor = nil
	var e error
	if r.Type == "message" || r.Type == "user_notice" {
		var n int
		e = tx.QueryRowContext(ctx, "SELECT count(*) FROM tombstones WHERE channel=? AND message_id=?", r.Channel.Login, r.ID).Scan(&n)
		if e != nil {
			return r, false, e
		}
		r.Deleted = n > 0
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM moderation_boundaries WHERE channel=? AND before_at>=? AND (user_id='' AND user_login='' OR user_id=? AND user_id<>'' OR user_login=? AND user_login<>'')", r.Channel.Login, r.EventAt.UnixNano(), r.Author.ID, r.Author.Login).Scan(&n); e != nil {
			return r, false, e
		}
		r.Deleted = r.Deleted || n > 0
		if r.Type == "user_notice" && !r.Deleted {
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM events e WHERE channel=? AND kind='user_notice' AND deleted=1 AND "+legacyNoticeIdentity+" AND event_at>=? AND (json_extract(payload,'$.author.id')=? AND json_extract(payload,'$.author.id')<>'' OR json_extract(payload,'$.author.login')=? AND json_extract(payload,'$.author.login')<>'' OR COALESCE(json_extract(payload,'$.author.id'),'')='' AND COALESCE(json_extract(payload,'$.author.login'),'')='')", r.Channel.Login, r.EventAt.UnixNano(), r.Author.ID, r.Author.Login).Scan(&n); e != nil {
				return r, false, e
			}
			r.Deleted = n > 0
		}
		if r.Reply != nil {
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT message_id FROM tombstones WHERE channel=? AND message_id=? UNION SELECT message_id FROM events WHERE channel=? AND message_id=? AND deleted=1)", r.Channel.Login, r.Reply.ParentID, r.Channel.Login, r.Reply.ParentID).Scan(&n); e != nil {
				return r, false, e
			}
			if n > 0 {
				r.Reply.Text = ""
			} else {
				if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM moderation_boundaries WHERE channel=? AND NOT EXISTS(SELECT 1 FROM events WHERE channel=? AND message_id=?)", r.Channel.Login, r.Channel.Login, r.Reply.ParentID).Scan(&n); e != nil {
					return r, false, e
				}
				if n > 0 {
					r.Reply.Text = ""
				} else {
					if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM events e WHERE channel=? AND kind='user_notice' AND deleted=1 AND "+legacyNoticeIdentity, r.Channel.Login).Scan(&n); e != nil {
						return r, false, e
					}
					if n > 0 {
						r.Reply.Text = ""
					}
				}
			}
		}
		if r.Deleted {
			redact(&r)
		}
	}
	if r.Type == "delete" || r.Type == "clear" || r.Type == "message_deleted" || r.Type == "chat_cleared" || r.Type == "user_banned" || r.Type == "user_timed_out" || r.Type == "user_messages_cleared" {
		if e = s.moderate(ctx, tx, r); e != nil {
			return r, false, e
		}
	}
	payload, e := json.Marshal(r)
	if len(payload) > 1024*1024 {
		return r, false, errors.New("normalized history record exceeds 1 MiB")
	}
	if e != nil {
		return r, false, e
	}
	res, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO events(channel,channel_id,kind,message_id,delivery_id,event_at,received_at,deleted,payload) VALUES(?,?,?,?,?,?,?,?,?)", r.Channel.Login, r.Channel.ID, r.Type, r.ID, r.DeliveryID, r.EventAt.UnixNano(), r.ReceivedAt.UnixNano(), r.Deleted, payload)
	if e != nil {
		return r, false, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return r, false, e
	}
	if n == 0 {
		return r, false, nil
	}
	r.Sequence, e = res.LastInsertId()
	if e != nil {
		return r, false, e
	}
	c := s.Cursor(r.Sequence)
	r.Cursor = &c
	return r, true, nil
}
func redact(r *automation.Record) {
	r.Text = ""
	r.Fragments = nil
	r.SystemText = ""
	r.NoticeParameters = nil
	if r.Reply != nil {
		r.Reply.Text = ""
	}
}

// moderate applies deletion and reply redaction entirely in SQLite, bounding Go memory.
func (s *Store) moderate(ctx context.Context, tx *sql.Tx, r automation.Record) error {
	predicate := "channel=? AND kind IN ('message','user_notice')"
	args := []any{r.Channel.Login}
	if r.Type == "delete" || r.Type == "message_deleted" {
		if r.TargetMessageID == "" {
			return errors.New("message deletion lacks target identity")
		}
		predicate += " AND message_id=?"
		args = append(args, r.TargetMessageID)
		if _, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO tombstones VALUES(?,?,?)", r.Channel.Login, r.TargetMessageID, r.EventAt.UnixNano()); e != nil {
			return e
		}
	} else if r.TargetUserID != "" {
		predicate += " AND json_extract(payload,'$.author.id')=?"
		args = append(args, r.TargetUserID)
	} else if r.Type == "user_banned" || r.Type == "user_timed_out" || r.Type == "user_messages_cleared" {
		if r.Author.Login == "" {
			return errors.New("user moderation lacks target identity")
		}
		predicate += " AND json_extract(payload,'$.author.login')=?"
		args = append(args, r.Author.Login)
	}
	if r.Type != "delete" && r.Type != "message_deleted" {
		userID, userLogin := r.TargetUserID, ""
		if userID == "" && (r.Type == "user_banned" || r.Type == "user_timed_out" || r.Type == "user_messages_cleared") {
			userLogin = r.Author.Login
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO moderation_boundaries VALUES(?,?,?,?) ON CONFLICT(channel,user_id,user_login) DO UPDATE SET before_at=MAX(before_at,excluded.before_at)", r.Channel.Login, userID, userLogin, r.EventAt.UnixNano()); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO tombstones(channel,message_id,deleted_at) SELECT channel,message_id,"+strconv.FormatInt(r.EventAt.UnixNano(), 10)+" FROM events WHERE "+predicate+" AND message_id<>''", args...); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "UPDATE events SET deleted=1,payload=json_set(payload,'$.deleted',json('true'),'$.text','','$.fragments',json('null'),'$.system_text','','$.notice_parameters',json('null')) WHERE "+predicate, args...); e != nil {
		return e
	}
	// Retain a null reply as null; redact only populated previews.
	_, e := tx.ExecContext(ctx, `UPDATE events SET payload=json_set(payload,'$.reply.text','') WHERE channel=? AND kind IN ('message','user_notice') AND json_type(payload,'$.reply')='object' AND (deleted=1 OR json_extract(payload,'$.reply.parent_id') IN (SELECT message_id FROM tombstones WHERE channel=?))`, r.Channel.Login, r.Channel.Login)
	return e
}

func storageError(err error) error {
	var databaseError *sqlite.Error
	if errors.As(err, &databaseError) && databaseError.Code()&255 == 13 {
		return fmt.Errorf("%w: SQLite write exhausted capacity", ErrStorageFull)
	}
	return err
}
