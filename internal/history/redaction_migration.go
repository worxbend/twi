package history

import (
	"context"
	"strings"

	"github.com/worxbend/twi/internal/automation"
)

// Some early schema6 notification rows used the notification subtype as ID.
// A channel deletion cannot be matched back to those rows reliably. Prefer a
// conservative tombstone over replaying content that may have been removed.
const legacyNoticeIdentity = `(e.message_id='' OR e.message_id=json_extract(e.payload,'$.reason') OR e.message_id IN ('sub','resub','subgift','anonsubgift','submysterygift','anonsubmysterygift','giftpaidupgrade','anongiftpaidupgrade','rewardgift','raid','unraid','ritual','bitsbadgetier','primepaidupgrade','standardpayforward','communitypayforward','announcement','charity_donation','pay_it_forward'))`

func effectiveDeletedSQL() string {
	return `(e.deleted=1 OR (e.kind IN ('message','user_notice') AND (
 EXISTS(SELECT 1 FROM tombstones t WHERE t.channel=e.channel AND t.message_id=e.message_id)
 OR EXISTS(SELECT 1 FROM moderation_boundaries b WHERE b.channel=e.channel AND b.before_at>=e.event_at AND (b.user_id='' AND b.user_login='' OR b.user_id=json_extract(e.payload,'$.author.id') AND b.user_id<>'' OR b.user_login=json_extract(e.payload,'$.author.login') AND b.user_login<>''))
 OR EXISTS(SELECT 1 FROM events cleared WHERE cleared.channel=e.channel AND cleared.kind='user_messages_cleared' AND cleared.event_at>=e.event_at AND (json_extract(cleared.payload,'$.target_user_id')<>'' AND json_extract(cleared.payload,'$.target_user_id')=json_extract(e.payload,'$.author.id') OR COALESCE(json_extract(cleared.payload,'$.target_user_id'),'')='' AND json_extract(cleared.payload,'$.author.login')<>'' AND json_extract(cleared.payload,'$.author.login')=json_extract(e.payload,'$.author.login')))
 OR (e.kind='user_notice' AND ` + legacyNoticeIdentity + ` AND EXISTS(SELECT 1 FROM tombstones t WHERE t.channel=e.channel AND (t.deleted_at=0 OR t.deleted_at>=e.event_at)))
 )))`
}

func replyRedactionSQL() string {
	return `(json_type(e.payload,'$.reply')='object' AND (
 EXISTS(SELECT 1 FROM tombstones t WHERE t.channel=e.channel AND t.message_id=json_extract(e.payload,'$.reply.parent_id'))
 OR EXISTS(SELECT 1 FROM events parent WHERE parent.channel=e.channel AND parent.message_id=json_extract(e.payload,'$.reply.parent_id') AND ` + strings.ReplaceAll(effectiveDeletedSQL(), "e.", "parent.") + `)
 OR (NOT EXISTS(SELECT 1 FROM events parent WHERE parent.channel=e.channel AND parent.message_id=json_extract(e.payload,'$.reply.parent_id')) AND (EXISTS(SELECT 1 FROM moderation_boundaries b WHERE b.channel=e.channel) OR EXISTS(SELECT 1 FROM events cleared WHERE cleared.channel=e.channel AND cleared.kind='user_messages_cleared') OR EXISTS(SELECT 1 FROM events erased WHERE erased.channel=e.channel AND erased.kind='user_notice' AND ` + strings.ReplaceAll(legacyNoticeIdentity, "e.", "erased.") + ` AND ` + strings.ReplaceAll(effectiveDeletedSQL(), "e.", "erased.") + `)))
 ))`
}

// ensureRedaction reconciles old persisted bodies atomically. It preserves store
// identity, sequence numbers and cursor positions; FTS triggers run transactionally.
func (s *Store) ensureRedaction(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 7 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO moderation_boundaries(channel,user_id,user_login,before_at)
 SELECT channel,COALESCE(json_extract(payload,'$.target_user_id'),''),CASE WHEN COALESCE(json_extract(payload,'$.target_user_id'),'')='' THEN COALESCE(json_extract(payload,'$.author.login'),'') ELSE '' END,MAX(event_at)
 FROM events WHERE kind='user_messages_cleared' AND (COALESCE(json_extract(payload,'$.target_user_id'),'')<>'' OR COALESCE(json_extract(payload,'$.author.login'),'')<>'') GROUP BY 1,2,3
 ON CONFLICT(channel,user_id,user_login) DO UPDATE SET before_at=MAX(before_at,excluded.before_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE events AS e SET deleted=1,payload=json_set(payload,'$.deleted',json('true'),'$.text','','$.fragments',json('null'),'$.system_text','','$.notice_parameters',json('null')) WHERE `+effectiveDeletedSQL())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE events AS e SET payload=json_set(payload,'$.reply.text','') WHERE `+replyRedactionSQL()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=7"); err != nil {
		return err
	}
	return tx.Commit()
}

// protectDecoded enforces logical redaction in read-only legacy stores too.
func protectDecoded(r *automation.Record, deleted, replyDeleted bool) {
	r.Deleted = deleted
	if deleted {
		redact(r)
	}
	if replyDeleted && r.Reply != nil {
		r.Reply.Text = ""
	}
}

func (s *Store) protectedPayloadColumns() string {
	if s.schema >= 7 {
		return "e.seq,e.payload,e.deleted,0 AS reply_deleted"
	}
	return "e.seq,e.payload," + effectiveDeletedSQL() + " AS deleted," + replyRedactionSQL() + " AS reply_deleted"
}

func (s *Store) visibleMessageSQL() string {
	if s.schema >= 7 {
		return "kind='message' AND deleted=0"
	}
	return "kind='message' AND NOT " + effectiveDeletedSQL()
}
