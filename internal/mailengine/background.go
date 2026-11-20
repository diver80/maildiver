package mailengine

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Mail while the app is not open (phones): the system wakes the app now and then, it syncs the
// inboxes and reports what arrived since the user last had the app open. "Arrived" is tracked
// per inbox by IMAP UID (a watermark in kv_meta): UIDs grow with every mail the server takes in,
// so older mails fetched later (backfill) never count as new, while mails moved into the inbox
// do.

// SyncInboxes syncs the INBOX of every enabled account.
func (e *Engine) SyncInboxes(ctx context.Context) error {
	var firstErr error
	for _, a := range e.loadConfig().Direct.Accounts {
		if !a.Enabled {
			continue
		}
		if _, err := e.SyncAccount(ctx, a.ID, true); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// maxNewInboxMail bounds the mails one wake-up reports.
const maxNewInboxMail = 50

// TakeNewInboxMail returns the unread INBOX mails stored since the last call or MarkInboxSeen,
// newest first, and moves the watermark past them. An inbox without a watermark gets one and
// reports nothing, so a first run does not announce the whole mailbox.
func (e *Engine) TakeNewInboxMail(ctx context.Context) ([]StoredMessage, error) {
	return e.store.takeNewInboxMail(ctx, true)
}

// MarkInboxSeen moves every inbox's watermark past what is stored: the user has the app open
// and saw new mail there.
func (e *Engine) MarkInboxSeen(ctx context.Context) error {
	_, err := e.store.takeNewInboxMail(ctx, false)
	return err
}

func (s *Store) takeNewInboxMail(ctx context.Context, collect bool) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, COALESCE(MAX(m.uid), 0) FROM folders f
		LEFT JOIN messages m ON m.folder_id = f.id WHERE f.type = 'inbox' GROUP BY f.id`)
	if err != nil {
		return nil, err
	}
	type inbox struct {
		id     string
		maxUID int64
	}
	var inboxes []inbox
	for rows.Next() {
		var in inbox
		if err := rows.Scan(&in.id, &in.maxUID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		inboxes = append(inboxes, in)
	}
	_ = rows.Close()

	var out []StoredMessage
	for _, in := range inboxes {
		key := "notified_uid:" + in.id
		var raw string
		err := s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k=?`, key).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		mark, _ := strconv.ParseInt(raw, 10, 64)
		if collect && raw != "" && in.maxUID > mark && len(out) < maxNewInboxMail {
			msgRows, err := s.db.QueryContext(ctx, `
				SELECT id, account_id, folder_id, folder_path, uid, message_id, thread_id, in_reply_to, references_hdr,
				       subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, tags_json, size,
				       '', '', body_fetched, '[]', '{}', NULL, has_ics, ''
				FROM messages WHERE folder_id=? AND uid>? AND read=0 ORDER BY uid DESC LIMIT ?`,
				in.id, mark, maxNewInboxMail-len(out))
			if err != nil {
				return nil, err
			}
			for msgRows.Next() {
				m, err := scanStoredMessage(msgRows)
				if err != nil {
					_ = msgRows.Close()
					return nil, err
				}
				out = append(out, m)
			}
			_ = msgRows.Close()
		}
		if raw == "" || in.maxUID > mark {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO kv_meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`,
				key, strconv.FormatInt(in.maxUID, 10)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
