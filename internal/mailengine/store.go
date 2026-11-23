package mailengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/triage"
)

const (
	LocalCalendarID      = "local-default"
	LocalAddressBookID   = "local-ab"
	LocalAddressBookName = "MailDiver Kontakte"
)

// Store wraps the local SQLite database (+ FTS5) for instant reads, search,
// offline access, local calendars, contacts, and filter rules.
type Store struct {
	db   *sql.DB
	path string
}

// StoredFolder represents a cached IMAP folder.
type StoredFolder struct {
	ID          string
	AccountID   string
	Name        string
	Path        string
	Delimiter   string
	Type        string
	SpecialUse  []string
	UIDValidity uint32
	UIDNext     uint32
	LastSync    string
	UnreadCount int
}

// StoredMessage represents a cached email header + optional fetched body.
type StoredMessage struct {
	ID          string
	AccountID   string
	FolderID    string
	FolderPath  string
	UID         uint32
	MessageID   string
	ThreadID    string
	InReplyTo   string
	References  string
	Subject     string
	From        string
	To          []string
	CC          []string
	DateISO     string
	DateUnix    int64
	Read        bool
	Flagged     bool
	Tags        []string
	Size        int64
	BodyText    string
	BodyHTML    string
	BodyFetched bool
	Attachments []backend.Attachment
	Headers     map[string]string
	RawEML      []byte
	HasICS      bool
	ICSBody     string
}

// StoredCalendar represents a local, Google, or iCal calendar.
type StoredCalendar struct {
	ID         string
	Name       string
	Source     string // "local", "google", "ical"
	ReadOnly   bool
	Color      string
	SyncToken  string
	ETag       string
	ExternalID string
	LastSync   string
}

// StoredEvent represents a calendar event (single or recurring master).
type StoredEvent struct {
	ID           string
	CalendarID   string
	CalendarName string
	ExternalID   string
	UID          string
	Title        string
	StartISO     string
	EndISO       string
	StartUnix    int64
	EndUnix      int64
	AllDay       bool
	Location     string
	Description  string
	RRule        string
	ExDates      []string
	RecurrenceID string
	IsRecurring  bool
	ReadOnly     bool
	ETag         string
	RawICS       string
	Status       string
	SelfResponse string
}

// StoredFilter represents a local OmaThunder rule executed directly on IMAP messages.
type StoredFilter struct {
	ID         string
	AccountID  string
	Name       string
	Enabled    bool
	MatchAll   bool
	Conditions []triage.NativeCondition
	Actions    []backend.FilterActionSpec
	Position   int
}

// OpenStore opens (or creates) the SQLite database at path and initializes the schema.
func OpenStore(path string) (*Store, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer pool eliminates SQLITE_BUSY under concurrent IDLE/sync/UI traffic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA busy_timeout=5000;`,
		`PRAGMA foreign_keys=ON;`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite pragma %s: %w", p, err)
		}
	}
	st := &Store{db: db, path: path}
	if err := st.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			delimiter TEXT NOT NULL DEFAULT '/',
			type TEXT NOT NULL DEFAULT '',
			special_use_json TEXT NOT NULL DEFAULT '[]',
			uid_validity INTEGER NOT NULL DEFAULT 0,
			uid_next INTEGER NOT NULL DEFAULT 0,
			last_sync TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_folders_account ON folders(account_id);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_folders_acc_path ON folders(account_id, path);`,

		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			folder_id TEXT NOT NULL,
			folder_path TEXT NOT NULL,
			uid INTEGER NOT NULL,
			message_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			in_reply_to TEXT NOT NULL DEFAULT '',
			references_hdr TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			from_addr TEXT NOT NULL DEFAULT '',
			to_json TEXT NOT NULL DEFAULT '[]',
			cc_json TEXT NOT NULL DEFAULT '[]',
			date_iso TEXT NOT NULL DEFAULT '',
			date_unix INTEGER NOT NULL DEFAULT 0,
			read INTEGER NOT NULL DEFAULT 0,
			flagged INTEGER NOT NULL DEFAULT 0,
			tags_json TEXT NOT NULL DEFAULT '[]',
			size INTEGER NOT NULL DEFAULT 0,
			body_text TEXT NOT NULL DEFAULT '',
			body_html TEXT NOT NULL DEFAULT '',
			body_fetched INTEGER NOT NULL DEFAULT 0,
			attachments_json TEXT NOT NULL DEFAULT '[]',
			headers_json TEXT NOT NULL DEFAULT '{}',
			raw_eml BLOB,
			has_ics INTEGER NOT NULL DEFAULT 0,
			ics_body TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_acc_folder_uid ON messages(account_id, folder_id, uid);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_folder_read ON messages(folder_id, read);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date_unix DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_unread ON messages(account_id, read, date_unix DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_msgid ON messages(message_id);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_inreplyto ON messages(in_reply_to);`,

		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			id UNINDEXED,
			subject,
			from_addr,
			to_text,
			body_text
		);`,

		`CREATE TABLE IF NOT EXISTS calendars (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'local',
			read_only INTEGER NOT NULL DEFAULT 0,
			color TEXT NOT NULL DEFAULT '#7aa2f7',
			sync_token TEXT NOT NULL DEFAULT '',
			etag TEXT NOT NULL DEFAULT '',
			external_id TEXT NOT NULL DEFAULT '',
			last_sync TEXT NOT NULL DEFAULT ''
		);`,

		`CREATE TABLE IF NOT EXISTS calendar_events (
			id TEXT PRIMARY KEY,
			calendar_id TEXT NOT NULL,
			external_id TEXT NOT NULL DEFAULT '',
			uid TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			start_iso TEXT NOT NULL DEFAULT '',
			end_iso TEXT NOT NULL DEFAULT '',
			start_unix INTEGER NOT NULL DEFAULT 0,
			end_unix INTEGER NOT NULL DEFAULT 0,
			all_day INTEGER NOT NULL DEFAULT 0,
			location TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			rrule TEXT NOT NULL DEFAULT '',
			exdates_json TEXT NOT NULL DEFAULT '[]',
			recurrence_id TEXT NOT NULL DEFAULT '',
			is_recurring INTEGER NOT NULL DEFAULT 0,
			read_only INTEGER NOT NULL DEFAULT 0,
			etag TEXT NOT NULL DEFAULT '',
			raw_ics TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'confirmed'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_cal_range ON calendar_events(calendar_id, start_unix, end_unix);`,
		`CREATE INDEX IF NOT EXISTS idx_events_ext ON calendar_events(calendar_id, external_id);`,
		`CREATE INDEX IF NOT EXISTS idx_events_uid ON calendar_events(lower(uid));`,

		`CREATE TABLE IF NOT EXISTS contacts (
			id TEXT PRIMARY KEY,
			address_book_id TEXT NOT NULL DEFAULT 'local-ab',
			display_name TEXT NOT NULL DEFAULT '',
			first_name TEXT NOT NULL DEFAULT '',
			last_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			second_email TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			mobile TEXT NOT NULL DEFAULT '',
			company TEXT NOT NULL DEFAULT '',
			job_title TEXT NOT NULL DEFAULT '',
			website TEXT NOT NULL DEFAULT '',
			address TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_contacts_email ON contacts(lower(email));`,

		`CREATE TABLE IF NOT EXISTS filters (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			match_all INTEGER NOT NULL DEFAULT 0,
			conditions_json TEXT NOT NULL DEFAULT '[]',
			actions_json TEXT NOT NULL DEFAULT '[]',
			position INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_filters_acc_name ON filters(account_id, lower(name));`,

		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			key TEXT PRIMARY KEY,
			result_json TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS kv_meta (
			k TEXT PRIMARY KEY,
			v TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	// Idempotent column addition for existing databases.
	if _, err := s.db.Exec(`ALTER TABLE calendar_events ADD COLUMN self_response TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("migrate calendar_events.self_response: %w", err)
		}
	} else {
		// Newly added column on an existing DB: trigger one full Google Calendar sync to backfill self_response.
		_, _ = s.db.Exec(`UPDATE calendars SET sync_token = '' WHERE source = 'google'`)
	}
	if err := s.rethreadOnce(); err != nil {
		return fmt.Errorf("rethread messages: %w", err)
	}
	// Ensure default local writable calendar always exists.
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO calendars(id, name, source, read_only, color) VALUES(?, ?, 'local', 0, '#7aa2f7')`,
		LocalCalendarID, "Persönlich",
	)
	return err
}

// --- Folders ---

func FolderID(accountID, path string) string {
	return accountID + "::" + path
}

func MessageStoreID(accountID, folderPath string, uid uint32) string {
	return fmt.Sprintf("%s::%s::%d", accountID, folderPath, uid)
}

func (s *Store) UpsertFolder(ctx context.Context, f StoredFolder) error {
	if f.ID == "" {
		f.ID = FolderID(f.AccountID, f.Path)
	}
	su, _ := json.Marshal(nonNilStrings(f.SpecialUse))
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folders(id, account_id, name, path, delimiter, type, special_use_json, uid_validity, uid_next, last_sync)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			delimiter=excluded.delimiter,
			type=excluded.type,
			special_use_json=excluded.special_use_json,
			uid_validity=CASE WHEN excluded.uid_validity > 0 THEN excluded.uid_validity ELSE folders.uid_validity END,
			uid_next=CASE WHEN excluded.uid_next > 0 THEN excluded.uid_next ELSE folders.uid_next END,
			last_sync=CASE WHEN excluded.last_sync != '' THEN excluded.last_sync ELSE folders.last_sync END`,
		f.ID, f.AccountID, f.Name, f.Path, f.Delimiter, f.Type, string(su), f.UIDValidity, f.UIDNext, f.LastSync,
	)
	return err
}

func (s *Store) UpdateFolderSyncState(ctx context.Context, folderID string, uidValidity, uidNext uint32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE folders SET uid_validity=?, uid_next=?, last_sync=? WHERE id=?`,
		uidValidity, uidNext, time.Now().UTC().Format(time.RFC3339), folderID,
	)
	return err
}

func (s *Store) ListFolders(ctx context.Context, accountID string) ([]StoredFolder, error) {
	var q string
	var args []any
	if accountID != "" {
		q = `SELECT f.id, f.account_id, f.name, f.path, f.delimiter, f.type, f.special_use_json, f.uid_validity, f.uid_next, f.last_sync,
		            COALESCE(u.cnt, 0)
		     FROM folders f
		     LEFT JOIN (SELECT folder_id, COUNT(*) AS cnt FROM messages WHERE account_id = ? AND read = 0 GROUP BY folder_id) u ON u.folder_id = f.id
		     WHERE f.account_id = ?`
		args = append(args, accountID, accountID)
	} else {
		q = `SELECT f.id, f.account_id, f.name, f.path, f.delimiter, f.type, f.special_use_json, f.uid_validity, f.uid_next, f.last_sync,
		            COALESCE(u.cnt, 0)
		     FROM folders f
		     LEFT JOIN (SELECT folder_id, COUNT(*) AS cnt FROM messages WHERE read = 0 GROUP BY folder_id) u ON u.folder_id = f.id`
	}
	q += ` ORDER BY CASE WHEN lower(f.type)='inbox' OR upper(f.path)='INBOX' THEN 0 ELSE 1 END, lower(f.path)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredFolder
	for rows.Next() {
		var f StoredFolder
		var su string
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Delimiter, &f.Type, &su, &f.UIDValidity, &f.UIDNext, &f.LastSync, &f.UnreadCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(su), &f.SpecialUse)
		out = append(out, f)
	}
	return out, rows.Err()
}

// listFoldersMeta returns folders without computing per-folder unread counts (fast path for lookups).
func (s *Store) listFoldersMeta(ctx context.Context, accountID string) ([]StoredFolder, error) {
	q := `SELECT id, account_id, name, path, delimiter, type, special_use_json, uid_validity, uid_next, last_sync FROM folders`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY CASE WHEN lower(type)='inbox' OR upper(path)='INBOX' THEN 0 ELSE 1 END, lower(path)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredFolder
	for rows.Next() {
		var f StoredFolder
		var su string
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Delimiter, &f.Type, &su, &f.UIDValidity, &f.UIDNext, &f.LastSync); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(su), &f.SpecialUse)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) FindFolderByType(ctx context.Context, accountID, folderType string) (StoredFolder, bool) {
	folders, err := s.listFoldersMeta(ctx, accountID)
	if err != nil {
		return StoredFolder{}, false
	}
	want := strings.ToLower(strings.TrimSpace(folderType))
	isSelectable := func(f StoredFolder) bool {
		for _, su := range f.SpecialUse {
			if strings.EqualFold(su, "noselect") || strings.EqualFold(su, "nonexistent") {
				return false
			}
		}
		return true
	}
	// Pass 1: prefer RFC 6154 server-declared Special-Use (\Sent, \Drafts, \Trash, \Junk, \Archive)
	// over name-heuristic legacy folders (e.g. "Gesendet" sorting before "Sent" / "Gesendete Objekte").
	for _, f := range folders {
		if !isSelectable(f) {
			continue
		}
		for _, su := range f.SpecialUse {
			if strings.EqualFold(su, want) {
				return f, true
			}
		}
	}
	// Pass 2: fall back to name-based folder type classification.
	for _, f := range folders {
		if !isSelectable(f) {
			continue
		}
		if strings.ToLower(f.Type) == want {
			return f, true
		}
	}
	return StoredFolder{}, false
}

func (s *Store) FindFolderByIDOrPath(ctx context.Context, accountID, idOrPath string) (StoredFolder, bool) {
	folders, err := s.listFoldersMeta(ctx, accountID)
	if err != nil {
		return StoredFolder{}, false
	}
	clean := strings.TrimPrefix(strings.TrimSpace(idOrPath), "/")
	for _, f := range folders {
		if f.ID == idOrPath || strings.EqualFold(f.Path, clean) || strings.EqualFold(strings.TrimPrefix(f.Path, "/"), clean) {
			return f, true
		}
	}
	return StoredFolder{}, false
}

// DeleteMissingAccountFolders removes locally cached folders (and their cached messages)
// for accountID whose path is not in keepPaths.
func (s *Store) DeleteMissingAccountFolders(ctx context.Context, accountID string, keepPaths map[string]bool) error {
	if accountID == "" || len(keepPaths) == 0 {
		return nil
	}
	existing, err := s.listFoldersMeta(ctx, accountID)
	if err != nil {
		return err
	}
	var staleIDs []string
	for _, f := range existing {
		if !keepPaths[f.Path] {
			staleIDs = append(staleIDs, f.ID)
		}
	}
	if len(staleIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, fid := range staleIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id IN (SELECT id FROM messages WHERE folder_id=?)`, fid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE folder_id=?`, fid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id=?`, fid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteAccountData(ctx context.Context, accountID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id IN (SELECT id FROM messages WHERE account_id=?)`, accountID); err != nil {
		return err
	}
	for _, tbl := range []string{"messages", "folders", "filters"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+tbl+` WHERE account_id=?`, accountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Messages ---

func (s *Store) ClearFolderMessages(ctx context.Context, folderID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id IN (SELECT id FROM messages WHERE folder_id=?)`, folderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE folder_id=?`, folderID); err != nil {
		return err
	}
	return tx.Commit()
}

// FolderUIDFlags returns cached UID -> (read, flagged, bodyFetched) for incremental diffing.
func (s *Store) FolderUIDFlags(ctx context.Context, folderID string) (map[uint32][3]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uid, read, flagged, body_fetched FROM messages WHERE folder_id=?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32][3]bool{}
	for rows.Next() {
		var uid uint32
		var rd, fl, bf int
		if err := rows.Scan(&uid, &rd, &fl, &bf); err != nil {
			return nil, err
		}
		out[uid] = [3]bool{rd != 0, fl != 0, bf != 0}
	}
	return out, rows.Err()
}

func (s *Store) DeleteFolderUIDs(ctx context.Context, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, uid := range uids {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE folder_id=? AND uid=?`, folderID, uid).Scan(&id); err == nil {
			_, _ = tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id=?`, id)
			_, _ = tx.ExecContext(ctx, `DELETE FROM messages WHERE id=?`, id)
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateMessageFlagsByUID(ctx context.Context, folderID string, uid uint32, read, flagged bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET read=?, flagged=? WHERE folder_id=? AND uid=?`, boolInt(read), boolInt(flagged), folderID, uid)
	return err
}

func (s *Store) UpsertMessage(ctx context.Context, m StoredMessage) error {
	if m.ID == "" {
		m.ID = MessageStoreID(m.AccountID, m.FolderPath, m.UID)
	}
	if m.FolderID == "" {
		m.FolderID = FolderID(m.AccountID, m.FolderPath)
	}
	if m.ThreadID == "" {
		m.ThreadID = resolveThreadID(ctx, s.db, m)
	}
	toJSON, _ := json.Marshal(nonNilStrings(m.To))
	ccJSON, _ := json.Marshal(nonNilStrings(m.CC))
	tagsJSON, _ := json.Marshal(nonNilStrings(m.Tags))
	attJSON, _ := json.Marshal(m.Attachments)
	if m.Attachments == nil {
		attJSON = []byte("[]")
	}
	hdrJSON, _ := json.Marshal(m.Headers)
	if m.Headers == nil {
		hdrJSON = []byte("{}")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, account_id, folder_id, folder_path, uid, message_id, thread_id, in_reply_to, references_hdr,
			subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, tags_json, size,
			body_text, body_html, body_fetched, attachments_json, headers_json, raw_eml, has_ics, ics_body
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			read=excluded.read,
			flagged=excluded.flagged,
			tags_json=excluded.tags_json,
			subject=CASE WHEN excluded.subject != '' THEN excluded.subject ELSE messages.subject END,
			from_addr=CASE WHEN excluded.from_addr != '' THEN excluded.from_addr ELSE messages.from_addr END,
			to_json=CASE WHEN excluded.to_json != '[]' THEN excluded.to_json ELSE messages.to_json END,
			cc_json=CASE WHEN excluded.cc_json != '[]' THEN excluded.cc_json ELSE messages.cc_json END,
			message_id=CASE WHEN excluded.message_id != '' THEN excluded.message_id ELSE messages.message_id END,
			in_reply_to=CASE WHEN excluded.in_reply_to != '' THEN excluded.in_reply_to ELSE messages.in_reply_to END,
			references_hdr=CASE WHEN excluded.references_hdr != '' THEN excluded.references_hdr ELSE messages.references_hdr END,
			body_text=CASE WHEN excluded.body_fetched=1 THEN excluded.body_text ELSE messages.body_text END,
			body_html=CASE WHEN excluded.body_fetched=1 THEN excluded.body_html ELSE messages.body_html END,
			body_fetched=CASE WHEN excluded.body_fetched=1 THEN 1 ELSE messages.body_fetched END,
			attachments_json=CASE WHEN excluded.body_fetched=1 THEN excluded.attachments_json ELSE messages.attachments_json END,
			headers_json=CASE WHEN excluded.body_fetched=1 THEN excluded.headers_json ELSE messages.headers_json END,
			raw_eml=CASE WHEN length(excluded.raw_eml) > 0 THEN excluded.raw_eml ELSE messages.raw_eml END,
			has_ics=CASE WHEN excluded.body_fetched=1 THEN excluded.has_ics ELSE messages.has_ics END,
			ics_body=CASE WHEN excluded.body_fetched=1 THEN excluded.ics_body ELSE messages.ics_body END`,
		m.ID, m.AccountID, m.FolderID, m.FolderPath, m.UID, m.MessageID, m.ThreadID, m.InReplyTo, m.References,
		m.Subject, m.From, string(toJSON), string(ccJSON), m.DateISO, m.DateUnix, boolInt(m.Read), boolInt(m.Flagged),
		string(tagsJSON), m.Size, m.BodyText, m.BodyHTML, boolInt(m.BodyFetched), string(attJSON), string(hdrJSON),
		m.RawEML, boolInt(m.HasICS), m.ICSBody,
	)
	if err != nil {
		return err
	}

	// Keep FTS5 index synchronized.
	_, _ = tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id=?`, m.ID)
	bodyForFTS := m.BodyText
	if !m.BodyFetched {
		// Preserve existing fetched body in FTS if this was a header-only refresh.
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT body_text FROM messages WHERE id=?`, m.ID).Scan(&existing); err == nil && existing != "" {
			bodyForFTS = existing
		}
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO messages_fts(id, subject, from_addr, to_text, body_text) VALUES(?, ?, ?, ?, ?)`,
		m.ID, m.Subject, m.From, strings.Join(m.To, " "), bodyForFTS,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// querier is *sql.DB or *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// resolveThreadID finds the conversation of m. References names the root first, In-Reply-To
// the direct parent; a reply that arrived before m (its In-Reply-To is m) counts too. When m
// links conversations that were apart so far (a missing middle mail arrived), they are merged.
// Without any match the root is the ID, so mails arriving in any order agree on it.
func resolveThreadID(ctx context.Context, q querier, m StoredMessage) string {
	candidates := extractMessageIDs(m.References + " " + m.InReplyTo)
	var found []string
	seen := map[string]bool{}
	add := func(tid string) {
		if tid != "" && !seen[tid] {
			seen[tid] = true
			found = append(found, tid)
		}
	}
	for _, cid := range candidates {
		var tid string
		if err := q.QueryRowContext(ctx, `SELECT thread_id FROM messages WHERE message_id=? AND thread_id!='' LIMIT 1`, cid).Scan(&tid); err == nil {
			add(tid)
		}
	}
	if m.MessageID != "" {
		var tid string
		if err := q.QueryRowContext(ctx, `SELECT thread_id FROM messages WHERE in_reply_to=? AND thread_id!='' LIMIT 1`, m.MessageID).Scan(&tid); err == nil {
			add(tid)
		}
	}
	if len(found) > 0 {
		for _, other := range found[1:] {
			_, _ = q.ExecContext(ctx, `UPDATE messages SET thread_id=? WHERE thread_id=?`, found[0], other)
		}
		return found[0]
	}
	switch {
	case len(candidates) > 0:
		return "th:" + candidates[0]
	case m.MessageID != "":
		return "th:" + m.MessageID
	}
	if norm := normalizeThreadSubject(m.Subject); norm != "" {
		return "th:subj:" + norm
	}
	return "th:" + m.ID
}

// threadVersion changes when resolveThreadID groups differently; stored mails are then grouped
// again once.
const threadVersion = "2"

// rethreadOnce regroups all stored mails, oldest first, after the thread logic changed.
func (s *Store) rethreadOnce() error {
	ctx := context.Background()
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k='thread_version'`).Scan(&v)
	if v == threadVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, message_id, in_reply_to, references_hdr, subject FROM messages ORDER BY date_unix ASC, id ASC`)
	if err != nil {
		return err
	}
	var all []StoredMessage
	for rows.Next() {
		var m StoredMessage
		if err := rows.Scan(&m.ID, &m.MessageID, &m.InReplyTo, &m.References, &m.Subject); err != nil {
			rows.Close()
			return err
		}
		all = append(all, m)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET thread_id=''`); err != nil {
		return err
	}
	for _, m := range all {
		tid := resolveThreadID(ctx, tx, m)
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET thread_id=? WHERE id=?`, tid, m.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO kv_meta(k, v) VALUES('thread_version', ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, threadVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func extractMessageIDs(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(raw) {
		clean := strings.Trim(strings.TrimSpace(tok), "<>,;")
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}

func normalizeThreadSubject(subj string) string {
	s := strings.TrimSpace(strings.ToLower(subj))
	for {
		trimmed := s
		for _, p := range []string{"re:", "aw:", "fwd:", "fw:", "wg:", "sv:"} {
			if strings.HasPrefix(trimmed, p) {
				trimmed = strings.TrimSpace(trimmed[len(p):])
			}
		}
		if trimmed == s {
			break
		}
		s = trimmed
	}
	return s
}

func (s *Store) GetMessage(ctx context.Context, id string) (StoredMessage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, account_id, folder_id, folder_path, uid, message_id, thread_id, in_reply_to, references_hdr,
		       subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, tags_json, size,
		       body_text, body_html, body_fetched, attachments_json, headers_json, raw_eml, has_ics, ics_body
		FROM messages WHERE id=?`, id)
	return scanStoredMessage(row)
}

func scanStoredMessage(row interface{ Scan(dest ...any) error }) (StoredMessage, error) {
	var m StoredMessage
	var toJSON, ccJSON, tagsJSON, attJSON, hdrJSON string
	var rd, fl, bf, hasICS int
	err := row.Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.FolderPath, &m.UID, &m.MessageID, &m.ThreadID, &m.InReplyTo, &m.References,
		&m.Subject, &m.From, &toJSON, &ccJSON, &m.DateISO, &m.DateUnix, &rd, &fl, &tagsJSON, &m.Size,
		&m.BodyText, &m.BodyHTML, &bf, &attJSON, &hdrJSON, &m.RawEML, &hasICS, &m.ICSBody,
	)
	if err != nil {
		return StoredMessage{}, err
	}
	m.Read = rd != 0
	m.Flagged = fl != 0
	m.BodyFetched = bf != 0
	m.HasICS = hasICS != 0
	_ = json.Unmarshal([]byte(toJSON), &m.To)
	_ = json.Unmarshal([]byte(ccJSON), &m.CC)
	_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
	_ = json.Unmarshal([]byte(attJSON), &m.Attachments)
	_ = json.Unmarshal([]byte(hdrJSON), &m.Headers)
	return m, nil
}

// ListMessages queries local SQLite (+ FTS5 when Query is set) in milliseconds.
type MessageQuery struct {
	AccountID       string
	FolderID        string
	Query           string
	Unread          *bool
	UnreadOrFlagged bool
	Days            int
	BeforeUnix      int64 // when > 0, matches date_unix > 0 AND date_unix <= BeforeUnix
	OnlyRead        bool
	ExcludeFlagged  bool
	OldestFirst     bool
	Limit           int
	Offset          int
	// HeadersOnly leaves bodies, attachments and headers out: lists don't show them, and
	// loading every body of a page was the slowest part of opening a folder.
	HeadersOnly bool
}

func (s *Store) ListMessages(ctx context.Context, q MessageQuery) ([]StoredMessage, int, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 80
	} else if limit > 1000 {
		limit = 1000
	}
	var where []string
	var args []any

	if q.AccountID != "" {
		where = append(where, "m.account_id = ?")
		args = append(args, q.AccountID)
	}
	if q.FolderID != "" {
		where = append(where, "(m.folder_id = ? OR lower(m.folder_path) = lower(?))")
		args = append(args, q.FolderID, strings.TrimPrefix(q.FolderID, "/"))
	} else {
		// Exclude Trash/Junk from general Inbox/Recent views unless searching or explicitly viewing them.
		if strings.TrimSpace(q.Query) == "" {
			where = append(where, "COALESCE(f.type, '') NOT IN ('trash', 'junk')")
		}
	}
	if q.UnreadOrFlagged {
		if q.Days > 0 {
			cutoff := time.Now().AddDate(0, 0, -q.Days).Unix()
			where = append(where, "((m.read = 0 AND m.date_unix >= ?) OR m.flagged = 1)")
			args = append(args, cutoff)
		} else {
			where = append(where, "(m.read = 0 OR m.flagged = 1)")
		}
	} else {
		if q.Unread != nil {
			if *q.Unread {
				where = append(where, "m.read = 0")
			} else {
				where = append(where, "m.read = 1")
			}
		}
		if q.Days > 0 {
			cutoff := time.Now().AddDate(0, 0, -q.Days).Unix()
			where = append(where, "m.date_unix >= ?")
			args = append(args, cutoff)
		}
	}
	if q.OnlyRead {
		where = append(where, "m.read = 1")
	}
	if q.ExcludeFlagged {
		where = append(where, "m.flagged = 0")
	}
	if q.BeforeUnix > 0 {
		where = append(where, "m.date_unix > 0 AND m.date_unix <= ?")
		args = append(args, q.BeforeUnix)
	}

	fromClause := "FROM messages m LEFT JOIN folders f ON f.id = m.folder_id"
	if trimmed := strings.TrimSpace(q.Query); trimmed != "" {
		ftsQuery := buildFTSQuery(trimmed)
		if ftsQuery != "" {
			fromClause += " JOIN messages_fts fts ON fts.id = m.id"
			where = append(where, "messages_fts MATCH ?")
			args = append(args, ftsQuery)
		} else {
			like := "%" + trimmed + "%"
			where = append(where, "(m.subject LIKE ? OR m.from_addr LIKE ? OR m.body_text LIKE ?)")
			args = append(args, like, like, like)
		}
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) "+fromClause+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	bodyCols := `m.body_text, m.body_html, m.body_fetched, m.attachments_json, m.headers_json, NULL, m.has_ics, m.ics_body`
	if q.HeadersOnly {
		bodyCols = `'', '', m.body_fetched, CASE WHEN m.attachments_json IS NOT NULL AND m.attachments_json != '' AND m.attachments_json != '[]' THEN '[{"name":""}]' ELSE '[]' END, '{}', NULL, m.has_ics, ''`
	}
	orderDir := "DESC"
	if q.OldestFirst {
		orderDir = "ASC"
	}
	selectSQL := `
		SELECT m.id, m.account_id, m.folder_id, m.folder_path, m.uid, m.message_id, m.thread_id, m.in_reply_to, m.references_hdr,
		       m.subject, m.from_addr, m.to_json, m.cc_json, m.date_iso, m.date_unix, m.read, m.flagged, m.tags_json, m.size,
		       ` + bodyCols + `
		` + fromClause + whereSQL + ` ORDER BY m.date_unix ` + orderDir + ` LIMIT ? OFFSET ?`
	queryArgs := append(append([]any{}, args...), limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, selectSQL, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		m, err := scanStoredMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func buildFTSQuery(raw string) string {
	fields := strings.Fields(raw)
	var parts []string
	for _, tok := range fields {
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '@' || r == '.' || r == '_' || r == '-' || r > 127 {
				return r
			}
			return -1
		}, tok)
		if clean == "" {
			continue
		}
		// Quote token with prefix wildcard so partial email/domain/word queries match smoothly.
		parts = append(parts, `"`+strings.ReplaceAll(clean, `"`, `""`)+`"*`)
	}
	return strings.Join(parts, " ")
}

// ThreadSizes returns how many distinct mails each of the given conversations holds (a mail in
// several folders counts once).
func (s *Store) ThreadSizes(ctx context.Context, threadIDs []string) (map[string]int, error) {
	out := map[string]int{}
	var ids []any
	seen := map[string]bool{}
	for _, id := range threadIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.thread_id, COUNT(DISTINCT CASE WHEN m.message_id != '' THEN m.message_id ELSE m.id END)
		FROM messages m LEFT JOIN folders f ON f.id = m.folder_id
		WHERE COALESCE(f.type, '') NOT IN ('trash', 'junk') AND m.thread_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		GROUP BY m.thread_id`, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid string
		var n int
		if err := rows.Scan(&tid, &n); err != nil {
			return nil, err
		}
		out[tid] = n
	}
	return out, rows.Err()
}

// ConversationMessages returns the mails of a conversation newest first, each mail once (a
// copy outside trash and junk preferred), without bodies.
func (s *Store) ConversationMessages(ctx context.Context, threadID string, limit int) ([]StoredMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.account_id, m.folder_id, m.folder_path, m.uid, m.message_id, m.thread_id, m.in_reply_to, m.references_hdr,
		       m.subject, m.from_addr, m.to_json, m.cc_json, m.date_iso, m.date_unix, m.read, m.flagged, m.tags_json, m.size,
		       substr(m.body_text, 1, 400), '', m.body_fetched, '[]', '{}', NULL, m.has_ics, '',
		       COALESCE(f.type, '')
		FROM messages m LEFT JOIN folders f ON f.id = m.folder_id
		WHERE m.thread_id=? ORDER BY m.date_unix DESC, m.id ASC LIMIT ?`, threadID, limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type entry struct {
		m      StoredMessage
		binned bool
	}
	var order []string
	best := map[string]entry{}
	for rows.Next() {
		var m StoredMessage
		var toJSON, ccJSON, tagsJSON, attJSON, hdrJSON, ftype string
		var rd, fl, bf, hasICS int
		var raw []byte
		if err := rows.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.FolderPath, &m.UID, &m.MessageID, &m.ThreadID, &m.InReplyTo, &m.References,
			&m.Subject, &m.From, &toJSON, &ccJSON, &m.DateISO, &m.DateUnix, &rd, &fl, &tagsJSON, &m.Size,
			&m.BodyText, &m.BodyHTML, &bf, &attJSON, &hdrJSON, &raw, &hasICS, &m.ICSBody, &ftype); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toJSON), &m.To)
		_ = json.Unmarshal([]byte(ccJSON), &m.CC)
		_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
		m.Read, m.Flagged, m.BodyFetched, m.HasICS = rd != 0, fl != 0, bf != 0, hasICS != 0
		key := m.MessageID
		if key == "" {
			key = m.ID
		}
		binned := ftype == "trash" || ftype == "junk"
		prev, ok := best[key]
		switch {
		case !ok:
			order = append(order, key)
			best[key] = entry{m, binned}
		case prev.binned && !binned:
			best[key] = entry{m, binned}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]StoredMessage, 0, min(len(order), limit))
	for _, k := range order {
		if len(out) == limit {
			break
		}
		out = append(out, best[k].m)
	}
	return out, nil
}

func (s *Store) ListThreadMessages(ctx context.Context, threadID string, limit int) ([]StoredMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, account_id, folder_id, folder_path, uid, message_id, thread_id, in_reply_to, references_hdr,
		       subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, tags_json, size,
		       body_text, body_html, body_fetched, attachments_json, headers_json, NULL, has_ics, ics_body
		FROM messages WHERE thread_id=? ORDER BY date_unix DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		m, err := scanStoredMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	// The newest `limit` mails, returned oldest first (the order a conversation is read in).
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (s *Store) UpdateMessageLocal(ctx context.Context, id string, read, flagged *bool, addTags, removeTags []string) error {
	m, err := s.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	if read != nil {
		m.Read = *read
	}
	if flagged != nil {
		m.Flagged = *flagged
	}
	if len(addTags) > 0 || len(removeTags) > 0 {
		tagSet := map[string]bool{}
		for _, t := range m.Tags {
			tagSet[t] = true
		}
		for _, t := range addTags {
			if strings.TrimSpace(t) != "" {
				tagSet[strings.TrimSpace(t)] = true
			}
		}
		for _, t := range removeTags {
			delete(tagSet, strings.TrimSpace(t))
		}
		m.Tags = m.Tags[:0]
		for t := range tagSet {
			m.Tags = append(m.Tags, t)
		}
		sort.Strings(m.Tags)
	}
	tagsJSON, _ := json.Marshal(nonNilStrings(m.Tags))
	_, err = s.db.ExecContext(ctx, `UPDATE messages SET read=?, flagged=?, tags_json=? WHERE id=?`,
		boolInt(m.Read), boolInt(m.Flagged), string(tagsJSON), id)
	return err
}

func (s *Store) DeleteMessage(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE id=?`, id)
	_, err = tx.ExecContext(ctx, `DELETE FROM messages WHERE id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MarkFolderReadLocal marks all unread messages in folderID as read locally.
func (s *Store) MarkFolderReadLocal(ctx context.Context, folderID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE messages SET read=1 WHERE folder_id=? AND read=0`, folderID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnreadFolders returns distinct folders that currently have at least one unread local message.
func (s *Store) UnreadFolders(ctx context.Context, accountID string) ([]StoredFolder, error) {
	all, err := s.ListFolders(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var out []StoredFolder
	for _, f := range all {
		if f.UnreadCount > 0 && f.Type != "trash" && f.Type != "junk" {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) CountAccountMessages(ctx context.Context, accountID string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE account_id=?`, accountID).Scan(&n)
	return n
}

func (s *Store) UnfetchedRecentUIDs(ctx context.Context, folderID string, limit int) ([]uint32, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid FROM messages WHERE folder_id=? AND body_fetched=0 ORDER BY date_unix DESC LIMIT ?`, folderID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var u uint32
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// --- Calendars & Events ---

func (s *Store) UpsertCalendar(ctx context.Context, c StoredCalendar) error {
	if c.Color == "" {
		c.Color = "#7aa2f7"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO calendars(id, name, source, read_only, color, sync_token, etag, external_id, last_sync)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			source=excluded.source,
			read_only=excluded.read_only,
			color=excluded.color,
			sync_token=CASE WHEN excluded.sync_token != '' THEN excluded.sync_token ELSE calendars.sync_token END,
			etag=CASE WHEN excluded.etag != '' THEN excluded.etag ELSE calendars.etag END,
			external_id=CASE WHEN excluded.external_id != '' THEN excluded.external_id ELSE calendars.external_id END,
			last_sync=CASE WHEN excluded.last_sync != '' THEN excluded.last_sync ELSE calendars.last_sync END`,
		c.ID, c.Name, c.Source, boolInt(c.ReadOnly), c.Color, c.SyncToken, c.ETag, c.ExternalID, c.LastSync,
	)
	return err
}

func (s *Store) SetCalendarSyncToken(ctx context.Context, calID, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calendars SET sync_token=?, last_sync=? WHERE id=?`,
		token, time.Now().UTC().Format(time.RFC3339), calID)
	return err
}

func (s *Store) ListCalendars(ctx context.Context) ([]StoredCalendar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, source, read_only, color, sync_token, etag, external_id, last_sync FROM calendars ORDER BY CASE WHEN source='local' THEN 0 WHEN source='google' THEN 1 ELSE 2 END, lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredCalendar
	for rows.Next() {
		var c StoredCalendar
		var ro int
		if err := rows.Scan(&c.ID, &c.Name, &c.Source, &ro, &c.Color, &c.SyncToken, &c.ETag, &c.ExternalID, &c.LastSync); err != nil {
			return nil, err
		}
		c.ReadOnly = ro != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCalendar(ctx context.Context, id string) (StoredCalendar, error) {
	var c StoredCalendar
	var ro int
	err := s.db.QueryRowContext(ctx, `SELECT id, name, source, read_only, color, sync_token, etag, external_id, last_sync FROM calendars WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Source, &ro, &c.Color, &c.SyncToken, &c.ETag, &c.ExternalID, &c.LastSync)
	c.ReadOnly = ro != 0
	return c, err
}

func (s *Store) DeleteCalendar(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id=?`, id)
	_, err = tx.ExecContext(ctx, `DELETE FROM calendars WHERE id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteCalendarsBySource(ctx context.Context, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id IN (SELECT id FROM calendars WHERE source=?)`, source)
	_, err = tx.ExecContext(ctx, `DELETE FROM calendars WHERE source=?`, source)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertEvent(ctx context.Context, e StoredEvent) error {
	exJSON, _ := json.Marshal(nonNilStrings(e.ExDates))
	if e.Status == "" {
		e.Status = "confirmed"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO calendar_events(
			id, calendar_id, external_id, uid, title, start_iso, end_iso, start_unix, end_unix,
			all_day, location, description, rrule, exdates_json, recurrence_id, is_recurring,
			read_only, etag, raw_ics, status, self_response
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title,
			start_iso=excluded.start_iso,
			end_iso=excluded.end_iso,
			start_unix=excluded.start_unix,
			end_unix=excluded.end_unix,
			all_day=excluded.all_day,
			location=excluded.location,
			description=excluded.description,
			rrule=excluded.rrule,
			exdates_json=excluded.exdates_json,
			recurrence_id=excluded.recurrence_id,
			is_recurring=excluded.is_recurring,
			read_only=excluded.read_only,
			etag=excluded.etag,
			raw_ics=excluded.raw_ics,
			status=excluded.status,
			self_response=excluded.self_response`,
		e.ID, e.CalendarID, e.ExternalID, e.UID, e.Title, e.StartISO, e.EndISO, e.StartUnix, e.EndUnix,
		boolInt(e.AllDay), e.Location, e.Description, e.RRule, string(exJSON), e.RecurrenceID,
		boolInt(e.IsRecurring), boolInt(e.ReadOnly), e.ETag, e.RawICS, e.Status, e.SelfResponse,
	)
	return err
}

func (s *Store) ReplaceCalendarEvents(ctx context.Context, calendarID string, events []StoredEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id=?`, calendarID); err != nil {
		return err
	}
	for _, e := range events {
		e.CalendarID = calendarID
		exJSON, _ := json.Marshal(nonNilStrings(e.ExDates))
		if e.Status == "" {
			e.Status = "confirmed"
		}
		_, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO calendar_events(
				id, calendar_id, external_id, uid, title, start_iso, end_iso, start_unix, end_unix,
				all_day, location, description, rrule, exdates_json, recurrence_id, is_recurring,
				read_only, etag, raw_ics, status, self_response
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.ID, e.CalendarID, e.ExternalID, e.UID, e.Title, e.StartISO, e.EndISO, e.StartUnix, e.EndUnix,
			boolInt(e.AllDay), e.Location, e.Description, e.RRule, string(exJSON), e.RecurrenceID,
			boolInt(e.IsRecurring), boolInt(e.ReadOnly), e.ETag, e.RawICS, e.Status, e.SelfResponse,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetEvent(ctx context.Context, calendarID, eventID string) (StoredEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT e.id, e.calendar_id, COALESCE(c.name,''), e.external_id, e.uid, e.title,
		       e.start_iso, e.end_iso, e.start_unix, e.end_unix, e.all_day, e.location, e.description,
		       e.rrule, e.exdates_json, e.recurrence_id, e.is_recurring, e.read_only, e.etag, e.raw_ics, e.status, e.self_response
		FROM calendar_events e LEFT JOIN calendars c ON c.id = e.calendar_id
		WHERE e.calendar_id=? AND (e.id=? OR e.external_id=?)`, calendarID, eventID, eventID)
	return scanStoredEvent(row)
}

func (s *Store) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id=? AND (id=? OR external_id=?)`, calendarID, eventID, eventID)
	return err
}

func (s *Store) ListRawEvents(ctx context.Context, calendarIDs []string) ([]StoredEvent, error) {
	q := `
		SELECT e.id, e.calendar_id, COALESCE(c.name,''), e.external_id, e.uid, e.title,
		       e.start_iso, e.end_iso, e.start_unix, e.end_unix, e.all_day, e.location, e.description,
		       e.rrule, e.exdates_json, e.recurrence_id, e.is_recurring, e.read_only, e.etag, e.raw_ics, e.status, e.self_response
		FROM calendar_events e LEFT JOIN calendars c ON c.id = e.calendar_id
		WHERE e.status != 'cancelled'`
	var args []any
	if len(calendarIDs) > 0 {
		ph := make([]string, len(calendarIDs))
		for i, id := range calendarIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		q += ` AND e.calendar_id IN (` + strings.Join(ph, ",") + `)`
	}
	q += ` ORDER BY e.start_unix ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		ev, err := scanStoredEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// FindEventsByUIDOrMatch finds non-cancelled calendar events matching an invitation's iCal UID,
// or falling back to exact title + start timestamp (±2 min) when UIDs differ across providers.
func (s *Store) FindEventsByUIDOrMatch(ctx context.Context, uid, title string, startUnix int64) ([]StoredEvent, error) {
	uid = strings.TrimSpace(uid)
	if uid != "" {
		rows, err := s.db.QueryContext(ctx, `
			SELECT e.id, e.calendar_id, COALESCE(c.name,''), e.external_id, e.uid, e.title,
			       e.start_iso, e.end_iso, e.start_unix, e.end_unix, e.all_day, e.location, e.description,
			       e.rrule, e.exdates_json, e.recurrence_id, e.is_recurring, e.read_only, e.etag, e.raw_ics, e.status, e.self_response
			FROM calendar_events e LEFT JOIN calendars c ON c.id = e.calendar_id
			WHERE e.status != 'cancelled' AND e.uid != '' AND lower(e.uid) = lower(?)`, uid)
		if err != nil {
			return nil, err
		}
		var out []StoredEvent
		for rows.Next() {
			if ev, serr := scanStoredEvent(rows); serr == nil {
				out = append(out, ev)
			}
		}
		_ = rows.Close()
		if len(out) > 0 {
			return out, nil
		}
	}
	title = strings.TrimSpace(title)
	if title == "" || startUnix <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.calendar_id, COALESCE(c.name,''), e.external_id, e.uid, e.title,
		       e.start_iso, e.end_iso, e.start_unix, e.end_unix, e.all_day, e.location, e.description,
		       e.rrule, e.exdates_json, e.recurrence_id, e.is_recurring, e.read_only, e.etag, e.raw_ics, e.status, e.self_response
		FROM calendar_events e LEFT JOIN calendars c ON c.id = e.calendar_id
		WHERE e.status != 'cancelled' AND lower(trim(e.title)) = lower(trim(?)) AND abs(e.start_unix - ?) <= 120`, title, startUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		if ev, serr := scanStoredEvent(rows); serr == nil {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}

// SetEventSelfResponse updates the cached attendee response status for a stored calendar event.
func (s *Store) SetEventSelfResponse(ctx context.Context, eventID, status string) {
	eventID = strings.TrimSpace(eventID)
	status = strings.TrimSpace(status)
	if eventID == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE calendar_events SET self_response=? WHERE id=?`, status, eventID)
}

// SetICSRSVP records the user's RSVP choice ("accepted", "tentative", "declined") for an invite.
func (s *Store) SetICSRSVP(ctx context.Context, uid, msgID, status string) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return
	}
	if u := strings.ToLower(strings.TrimSpace(uid)); u != "" {
		_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO kv_meta(k, v) VALUES(?, ?)`, "ics_rsvp:"+u, status)
	}
	if m := strings.TrimSpace(msgID); m != "" {
		_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO kv_meta(k, v) VALUES(?, ?)`, "ics_rsvp_msg:"+m, status)
	}
}

// GetICSRSVP returns a previously recorded RSVP status ("accepted", "tentative", "declined") or "".
func (s *Store) GetICSRSVP(ctx context.Context, uid, msgID string) string {
	var v string
	if u := strings.ToLower(strings.TrimSpace(uid)); u != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k=?`, "ics_rsvp:"+u).Scan(&v); err == nil && v != "" {
			return v
		}
	}
	if m := strings.TrimSpace(msgID); m != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k=?`, "ics_rsvp_msg:"+m).Scan(&v); err == nil && v != "" {
			return v
		}
	}
	return ""
}

func scanStoredEvent(row interface{ Scan(dest ...any) error }) (StoredEvent, error) {
	var e StoredEvent
	var exJSON string
	var allDay, isRec, ro int
	err := row.Scan(
		&e.ID, &e.CalendarID, &e.CalendarName, &e.ExternalID, &e.UID, &e.Title,
		&e.StartISO, &e.EndISO, &e.StartUnix, &e.EndUnix, &allDay, &e.Location, &e.Description,
		&e.RRule, &exJSON, &e.RecurrenceID, &isRec, &ro, &e.ETag, &e.RawICS, &e.Status, &e.SelfResponse,
	)
	if err != nil {
		return StoredEvent{}, err
	}
	e.AllDay = allDay != 0
	e.IsRecurring = isRec != 0
	e.ReadOnly = ro != 0
	_ = json.Unmarshal([]byte(exJSON), &e.ExDates)
	return e, nil
}

// --- Contacts ---

func (s *Store) SearchContacts(ctx context.Context, query string) ([]backend.ContactRecord, error) {
	return s.listContacts(ctx, query, 500)
}

func (s *Store) GetContact(ctx context.Context, id string) (backend.ContactRecord, error) {
	var c backend.ContactRecord
	row := s.db.QueryRowContext(ctx, `SELECT id, address_book_id, display_name, first_name, last_name, email, second_email, phone, mobile, company, job_title, website, address, notes FROM contacts WHERE id=?`, id)
	err := row.Scan(&c.ID, &c.AddressBookID, &c.DisplayName, &c.FirstName, &c.LastName, &c.Email, &c.SecondEmail, &c.Phone, &c.Mobile, &c.Company, &c.JobTitle, &c.Website, &c.Address, &c.Notes)
	return c, err
}

func (s *Store) DeleteContactsByAddressBook(ctx context.Context, bookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE address_book_id=?`, bookID)
	return err
}

func (s *Store) DeleteMissingAddressBookContacts(ctx context.Context, bookID string, keepIDs map[string]bool) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM contacts WHERE address_book_id=?`, bookID)
	if err != nil {
		return err
	}
	var toDelete []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !keepIDs[id] {
			toDelete = append(toDelete, id)
		}
	}
	_ = rows.Close()
	for _, id := range toDelete {
		if _, derr := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE id=?`, id); derr != nil {
			return derr
		}
	}
	return nil
}

// ListAllContacts returns all locally stored contacts without a row cap (used for VCF export).
func (s *Store) ListAllContacts(ctx context.Context) ([]backend.ContactRecord, error) {
	return s.listContacts(ctx, "", 0)
}

func (s *Store) listContacts(ctx context.Context, query string, limit int) ([]backend.ContactRecord, error) {
	q := `SELECT id, address_book_id, display_name, first_name, last_name, email, second_email, phone, mobile, company, job_title, website, address, notes FROM contacts`
	var args []any
	if t := strings.TrimSpace(query); t != "" {
		like := "%" + t + "%"
		q += ` WHERE display_name LIKE ? OR email LIKE ? OR company LIKE ? OR first_name LIKE ? OR last_name LIKE ?`
		args = append(args, like, like, like, like, like)
	}
	q += ` ORDER BY lower(display_name), lower(email)`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backend.ContactRecord
	for rows.Next() {
		var c backend.ContactRecord
		if err := rows.Scan(&c.ID, &c.AddressBookID, &c.DisplayName, &c.FirstName, &c.LastName, &c.Email, &c.SecondEmail, &c.Phone, &c.Mobile, &c.Company, &c.JobTitle, &c.Website, &c.Address, &c.Notes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpsertContact(ctx context.Context, req backend.ContactUpsertRequest) (backend.ContactRecord, string, error) {
	c := req.Contact
	if c.AddressBookID == "" {
		c.AddressBookID = req.AddressBookID
	}
	if c.AddressBookID == "" {
		c.AddressBookID = LocalAddressBookID
	}
	action := "imported"
	if req.ContactID != "" {
		c.ID = req.ContactID
	}
	// If enriching or matching existing email, merge missing fields without clobbering existing values.
	var existing backend.ContactRecord
	var found bool
	if c.ID != "" {
		row := s.db.QueryRowContext(ctx, `SELECT id, address_book_id, display_name, first_name, last_name, email, second_email, phone, mobile, company, job_title, website, address, notes FROM contacts WHERE id=?`, c.ID)
		if err := row.Scan(&existing.ID, &existing.AddressBookID, &existing.DisplayName, &existing.FirstName, &existing.LastName, &existing.Email, &existing.SecondEmail, &existing.Phone, &existing.Mobile, &existing.Company, &existing.JobTitle, &existing.Website, &existing.Address, &existing.Notes); err == nil {
			found = true
		}
	} else if c.Email != "" {
		row := s.db.QueryRowContext(ctx, `SELECT id, address_book_id, display_name, first_name, last_name, email, second_email, phone, mobile, company, job_title, website, address, notes FROM contacts WHERE lower(email)=lower(?) LIMIT 1`, c.Email)
		if err := row.Scan(&existing.ID, &existing.AddressBookID, &existing.DisplayName, &existing.FirstName, &existing.LastName, &existing.Email, &existing.SecondEmail, &existing.Phone, &existing.Mobile, &existing.Company, &existing.JobTitle, &existing.Website, &existing.Address, &existing.Notes); err == nil {
			found = true
		}
	}
	if found {
		action = "enriched"
		c.ID = existing.ID
		c.DisplayName = keepOrFill(existing.DisplayName, c.DisplayName)
		c.FirstName = keepOrFill(existing.FirstName, c.FirstName)
		c.LastName = keepOrFill(existing.LastName, c.LastName)
		c.Email = keepOrFill(existing.Email, c.Email)
		c.SecondEmail = keepOrFill(existing.SecondEmail, c.SecondEmail)
		c.Phone = keepOrFill(existing.Phone, c.Phone)
		c.Mobile = keepOrFill(existing.Mobile, c.Mobile)
		c.Company = keepOrFill(existing.Company, c.Company)
		c.JobTitle = keepOrFill(existing.JobTitle, c.JobTitle)
		c.Website = keepOrFill(existing.Website, c.Website)
		c.Address = keepOrFill(existing.Address, c.Address)
		c.Notes = keepOrFill(existing.Notes, c.Notes)
	} else if c.ID == "" {
		c.ID = fmt.Sprintf("ct-%d", time.Now().UnixNano())
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO contacts(
			id, address_book_id, display_name, first_name, last_name, email, second_email,
			phone, mobile, company, job_title, website, address, notes, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.AddressBookID, c.DisplayName, c.FirstName, c.LastName, c.Email, c.SecondEmail,
		c.Phone, c.Mobile, c.Company, c.JobTitle, c.Website, c.Address, c.Notes, time.Now().UTC().Format(time.RFC3339),
	)
	return c, action, err
}

func keepOrFill(existing, incoming string) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return strings.TrimSpace(incoming)
}

// --- Filters ---

func (s *Store) ListFilters(ctx context.Context, accountID string) ([]StoredFilter, error) {
	q := `SELECT id, account_id, name, enabled, match_all, conditions_json, actions_json, position FROM filters`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY position ASC, lower(name) ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredFilter
	for rows.Next() {
		var f StoredFilter
		var en, ma int
		var cj, aj string
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &en, &ma, &cj, &aj, &f.Position); err != nil {
			return nil, err
		}
		f.Enabled = en != 0
		f.MatchAll = ma != 0
		_ = json.Unmarshal([]byte(cj), &f.Conditions)
		_ = json.Unmarshal([]byte(aj), &f.Actions)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) UpsertFilter(ctx context.Context, req backend.FilterUpsertRequest) (StoredFilter, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StoredFilter{}, err
	}
	defer func() { _ = tx.Rollback() }()

	listInTx := func() ([]StoredFilter, error) {
		rows, qerr := tx.QueryContext(ctx, `
			SELECT id, account_id, name, enabled, match_all, conditions_json, actions_json, position
			FROM filters WHERE account_id=? ORDER BY position ASC, name ASC`, req.AccountID)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		var out []StoredFilter
		for rows.Next() {
			var f StoredFilter
			var en, ma int
			var cj, aj string
			if serr := rows.Scan(&f.ID, &f.AccountID, &f.Name, &en, &ma, &cj, &aj, &f.Position); serr != nil {
				return nil, serr
			}
			f.Enabled = en != 0
			f.MatchAll = ma != 0
			_ = json.Unmarshal([]byte(cj), &f.Conditions)
			_ = json.Unmarshal([]byte(aj), &f.Actions)
			out = append(out, f)
		}
		return out, rows.Err()
	}

	existing, err := listInTx()
	if err != nil {
		return StoredFilter{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "MailDiver Filter"
	}
	switch req.Mode {
	case "toggle":
		target := strings.TrimSpace(req.ExistingName)
		if target == "" {
			target = name
		}
		for _, f := range existing {
			if strings.EqualFold(f.Name, target) {
				f.Enabled = req.Enabled
				if _, err := tx.ExecContext(ctx, `UPDATE filters SET enabled=? WHERE id=?`, boolInt(f.Enabled), f.ID); err != nil {
					return StoredFilter{}, err
				}
				return f, tx.Commit()
			}
		}
		return StoredFilter{}, fmt.Errorf("not_found: filter %q", target)
	case "replace", "update":
		target := strings.TrimSpace(req.ExistingName)
		if target == "" {
			target = name
		}
		for _, f := range existing {
			if strings.EqualFold(f.Name, target) {
				// Prevent colliding with another filter's name on rename.
				if !strings.EqualFold(f.Name, name) {
					for _, other := range existing {
						if other.ID != f.ID && strings.EqualFold(other.Name, name) {
							return StoredFilter{}, fmt.Errorf("eine Filterregel mit dem Namen %q existiert bereits in diesem Konto", name)
						}
					}
				}
				f.Name = name
				f.Enabled = req.Enabled
				f.MatchAll = req.MatchAll
				f.Conditions = req.Conditions
				f.Actions = req.Actions
				cj, _ := json.Marshal(f.Conditions)
				aj, _ := json.Marshal(f.Actions)
				if _, err := tx.ExecContext(ctx, `UPDATE filters SET name=?, enabled=?, match_all=?, conditions_json=?, actions_json=? WHERE id=?`,
					f.Name, boolInt(f.Enabled), boolInt(f.MatchAll), string(cj), string(aj), f.ID); err != nil {
					return StoredFilter{}, err
				}
				return f, tx.Commit()
			}
		}
	case "append_conditions":
		target := strings.TrimSpace(req.ExistingName)
		if target == "" {
			target = name
		}
		for _, f := range existing {
			if strings.EqualFold(f.Name, target) {
				for _, c := range req.Conditions {
					if !slices.Contains(f.Conditions, c) {
						f.Conditions = append(f.Conditions, c)
					}
				}
				cj, _ := json.Marshal(f.Conditions)
				if _, err := tx.ExecContext(ctx, `UPDATE filters SET conditions_json=? WHERE id=?`, string(cj), f.ID); err != nil {
					return StoredFilter{}, err
				}
				return f, tx.Commit()
			}
		}
	}

	// Default "create": ensure unique name per account.
	used := map[string]bool{}
	maxPos := 0
	for _, f := range existing {
		used[strings.ToLower(f.Name)] = true
		if f.Position >= maxPos {
			maxPos = f.Position + 1
		}
	}
	base := name
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	f := StoredFilter{
		ID:         fmt.Sprintf("flt-%d", time.Now().UnixNano()),
		AccountID:  req.AccountID,
		Name:       name,
		Enabled:    req.Enabled,
		MatchAll:   req.MatchAll,
		Conditions: req.Conditions,
		Actions:    req.Actions,
		Position:   maxPos,
	}
	cj, _ := json.Marshal(f.Conditions)
	aj, _ := json.Marshal(f.Actions)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO filters(id, account_id, name, enabled, match_all, conditions_json, actions_json, position)
		VALUES(?,?,?,?,?,?,?,?)`,
		f.ID, f.AccountID, f.Name, boolInt(f.Enabled), boolInt(f.MatchAll), string(cj), string(aj), f.Position,
	); err != nil {
		return StoredFilter{}, err
	}
	return f, tx.Commit()
}

func (s *Store) DeleteFilter(ctx context.Context, accountID, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM filters WHERE account_id=? AND lower(name)=lower(?)`, accountID, name)
	return err
}

// ReorderFilter moves the named rule one step earlier (delta < 0) or later (delta > 0) for its account.
func (s *Store) ReorderFilter(ctx context.Context, accountID, name string, delta int) error {
	if delta == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, name FROM filters WHERE account_id=? ORDER BY position ASC, name ASC`, accountID)
	if err != nil {
		return err
	}
	type item struct{ id, name string }
	var list []item
	for rows.Next() {
		var it item
		if serr := rows.Scan(&it.id, &it.name); serr != nil {
			_ = rows.Close()
			return serr
		}
		list = append(list, it)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	idx := -1
	for i, f := range list {
		if strings.EqualFold(f.name, name) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("not_found: filter %q", name)
	}
	target := idx - 1
	if delta > 0 {
		target = idx + 1
	}
	if target < 0 || target >= len(list) {
		return nil
	}
	list[idx], list[target] = list[target], list[idx]
	for pos, f := range list {
		if _, uerr := tx.ExecContext(ctx, `UPDATE filters SET position=? WHERE id=?`, pos, f.id); uerr != nil {
			return uerr
		}
	}
	return tx.Commit()
}

// --- Idempotency Keys ---

func (s *Store) GetIdempotency(ctx context.Context, key string) (json.RawMessage, bool) {
	if key == "" {
		return nil, false
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT result_json FROM idempotency_keys WHERE key=?`, key).Scan(&raw); err != nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

func (s *Store) PutIdempotency(ctx context.Context, key string, result json.RawMessage) {
	if key == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO idempotency_keys(key, result_json, created_at) VALUES(?,?,?)`,
		key, string(result), time.Now().Unix(),
	)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
