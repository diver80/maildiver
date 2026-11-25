package mailengine

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The activity log records what was done to mails on this device: read, flagged, moved,
// deleted, sent, answered invitations – by the user, a filter rule, an automation or an agent
// over MCP. It lives in the local database only, is kept for activityRetention and never
// leaves the device. What another mail client does to the same mailbox is not in it.

const activityRetention = 90 * 24 * time.Hour

// Activity kinds.
const (
	ActivityRead     = "read"
	ActivityUnread   = "unread"
	ActivityFlag     = "flag"
	ActivityUnflag   = "unflag"
	ActivityMove     = "move"
	ActivityArchive  = "archive"
	ActivityTrash    = "trash"
	ActivityDelete   = "delete" // for good
	ActivitySpam     = "spam"
	ActivityRestore  = "restore" // an undone move
	ActivitySend     = "send"
	ActivitySchedule = "schedule"
	ActivityReadAll  = "readall"
	ActivityRSVP     = "rsvp"
)

// Who did it.
const (
	ActivityByUser = "user"
	ActivityByRule = "rule"
	ActivityByMCP  = "mcp"
	ActivityByAuto = "auto"
)

// ActivityEntry is one line of the activity log.
type ActivityEntry struct {
	ID         int64
	At         time.Time
	Kind       string
	Source     string // ActivityBy…
	SourceName string // the rule's name
	AccountID  string
	MessageID  string // RFC Message-ID: finds the mail wherever it is now
	LocalID    string // the stored mail at the time
	Subject    string
	Peer       string // sender, or the recipients of a sent mail
	Folder     string // where the mail was
	Target     string // where it went; the answer of an RSVP
	Count      int    // mails covered (mark all read)
	Batch      string // entries of one action on several mails share it
}

// ActivityQuery selects log entries, newest first.
type ActivityQuery struct {
	Kinds  []string // empty: all
	Search string   // in subject and peer
	Before int64    // ID to page from; 0: newest
	Limit  int
}

type activitySourceKey struct{}

type activitySource struct{ source, name string }

// WithActivitySource marks what ctx does as done by source (a rule, an automation), so the log
// does not attribute it to the user.
func WithActivitySource(ctx context.Context, source, name string) context.Context {
	return context.WithValue(ctx, activitySourceKey{}, activitySource{source, name})
}

func (e *Engine) activitySourceOf(ctx context.Context) (string, string) {
	if s, ok := ctx.Value(activitySourceKey{}).(activitySource); ok && s.source != "" {
		return s.source, s.name
	}
	if e.activitySource != "" {
		return e.activitySource, ""
	}
	return ActivityByUser, ""
}

// logActivity stores entries for what ctx did. It never fails the action: a lost log line is
// better than a failed delete.
func (e *Engine) logActivity(ctx context.Context, entries ...ActivityEntry) {
	if len(entries) == 0 || e.store == nil {
		return
	}
	src, name := e.activitySourceOf(ctx)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	now := time.Now()
	for _, a := range entries {
		if a.At.IsZero() {
			a.At = now
		}
		if a.Source == "" {
			a.Source, a.SourceName = src, name
		}
		if a.Count <= 0 {
			a.Count = 1
		}
		_, _ = tx.ExecContext(ctx, `INSERT INTO activity(at, kind, source, source_name, account_id, message_id, local_id, subject, peer, folder, target, count, batch)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.At.UnixMilli(), a.Kind, a.Source, a.SourceName, a.AccountID, strings.Trim(a.MessageID, "<> "), a.LocalID,
			a.Subject, a.Peer, a.Folder, a.Target, a.Count, a.Batch)
	}
	_ = tx.Commit()
}

// activityFor is the entry of kind for a stored mail.
func activityFor(kind string, m StoredMessage) ActivityEntry {
	return ActivityEntry{
		Kind: kind, AccountID: m.AccountID, MessageID: m.MessageID, LocalID: m.ID,
		Subject: m.Subject, Peer: m.From, Folder: m.FolderPath,
	}
}

// actionActivities are the log entries of one action on m (as it was before): a move, trash,
// archive or delete is one entry; otherwise each read or flag change that changes something.
func actionActivities(m StoredMessage, read, flagged *bool, trash, archive, moved, permDel bool, target, batch string) []ActivityEntry {
	kind := ""
	switch {
	case permDel:
		kind = ActivityDelete
	case moved && trash:
		kind = ActivityTrash
	case moved && archive:
		kind = ActivityArchive
	case moved:
		kind = ActivityMove
	}
	var out []ActivityEntry
	add := func(k, t string) {
		a := activityFor(k, m)
		a.Target, a.Batch = t, batch
		out = append(out, a)
	}
	if kind != "" {
		add(kind, target)
		return out
	}
	if read != nil && *read != m.Read {
		if *read {
			add(ActivityRead, "")
		} else {
			add(ActivityUnread, "")
		}
	}
	if flagged != nil && *flagged != m.Flagged {
		if *flagged {
			add(ActivityFlag, "")
		} else {
			add(ActivityUnflag, "")
		}
	}
	return out
}

// ListActivity returns log entries, newest first.
func (e *Engine) ListActivity(ctx context.Context, q ActivityQuery) ([]ActivityEntry, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 200
	}
	sql := `SELECT id, at, kind, source, source_name, account_id, message_id, local_id, subject, peer, folder, target, count, batch FROM activity WHERE 1=1`
	var args []any
	if q.Before > 0 {
		sql += ` AND id < ?`
		args = append(args, q.Before)
	}
	if len(q.Kinds) > 0 {
		sql += ` AND kind IN (?` + strings.Repeat(`,?`, len(q.Kinds)-1) + `)`
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if s := strings.TrimSpace(q.Search); s != "" {
		sql += ` AND (subject LIKE ? OR peer LIKE ? OR source_name LIKE ?)`
		like := "%" + s + "%"
		args = append(args, like, like, like)
	}
	sql += fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, q.Limit)
	rows, err := e.store.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActivityEntry
	for rows.Next() {
		var a ActivityEntry
		var at int64
		if err := rows.Scan(&a.ID, &at, &a.Kind, &a.Source, &a.SourceName, &a.AccountID, &a.MessageID, &a.LocalID,
			&a.Subject, &a.Peer, &a.Folder, &a.Target, &a.Count, &a.Batch); err != nil {
			return nil, err
		}
		a.At = time.UnixMilli(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityMessage is where the mail of a log entry is now.
type ActivityMessage struct {
	ID         string
	AccountID  string
	FolderID   string
	FolderPath string
}

// LocateActivityMessage finds the mail of a log entry: the stored mail itself if it is still
// there, else the mail with its Message-ID in the same account (moved, in the trash). ok is
// false when the mail is gone.
func (e *Engine) LocateActivityMessage(ctx context.Context, a ActivityEntry) (ActivityMessage, bool) {
	if a.LocalID != "" {
		if m, err := e.store.GetMessage(ctx, a.LocalID); err == nil {
			return ActivityMessage{m.ID, m.AccountID, m.FolderID, m.FolderPath}, true
		}
	}
	mid := strings.Trim(a.MessageID, "<> ")
	if mid == "" {
		return ActivityMessage{}, false
	}
	row := e.store.db.QueryRowContext(ctx, `SELECT id, account_id, folder_id, folder_path FROM messages
		WHERE trim(message_id, '<> ') = ? AND (? = '' OR account_id = ?) ORDER BY date_unix DESC LIMIT 1`, mid, a.AccountID, a.AccountID)
	var m ActivityMessage
	if err := row.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.FolderPath); err != nil {
		return ActivityMessage{}, false
	}
	return m, true
}

func (s *Store) migrateActivity() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS activity (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at INTEGER NOT NULL,
		kind TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'user',
		source_name TEXT NOT NULL DEFAULT '',
		account_id TEXT NOT NULL DEFAULT '',
		message_id TEXT NOT NULL DEFAULT '',
		local_id TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		peer TEXT NOT NULL DEFAULT '',
		folder TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		count INTEGER NOT NULL DEFAULT 1,
		batch TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_activity_kind ON activity(kind, id DESC)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM activity WHERE at < ?`, time.Now().Add(-activityRetention).UnixMilli())
	return err
}
