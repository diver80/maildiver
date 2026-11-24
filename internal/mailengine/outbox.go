package mailengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

// OutboxItem represents an email queued locally while offline or during transient SMTP outages.
type OutboxItem struct {
	ID             string   `json:"id"`
	AccountID      string   `json:"accountId"`
	FromEmail      string   `json:"fromEmail"`
	Recipients     []string `json:"recipients"`
	Subject        string   `json:"subject"`
	DraftMessageID string   `json:"draftMessageId,omitempty"`
	Status         string   `json:"status"` // "queued" | "failed"
	RawEML         []byte   `json:"-"`
	Attempts       int      `json:"attempts"`
	LastError      string   `json:"lastError,omitempty"`
	CreatedAt      int64    `json:"createdAt"`
	NextRetryAt    int64    `json:"nextRetryAt"`
	// ScheduledAt (unix seconds, 0 = at once) is when a "send later" mail goes out: never before,
	// not even with "send all now".
	ScheduledAt int64 `json:"scheduledAt,omitempty"`
}

func (s *Store) ensureOutboxTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS outbox (
		id TEXT PRIMARY KEY,
		account_id TEXT NOT NULL,
		from_email TEXT NOT NULL,
		recipients_json TEXT NOT NULL,
		subject TEXT NOT NULL,
		draft_message_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'queued',
		raw_eml BLOB NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		next_retry_at INTEGER NOT NULL
	)`)
	if err != nil {
		return err
	}
	// Migrate older databases created before draft_message_id / status existed.
	for _, ddl := range []string{
		`ALTER TABLE outbox ADD COLUMN draft_message_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE outbox ADD COLUMN status TEXT NOT NULL DEFAULT 'queued'`,
		`ALTER TABLE outbox ADD COLUMN scheduled_at INTEGER NOT NULL DEFAULT 0`,
	} {
		_, _ = s.db.Exec(ddl)
	}
	return nil
}

func (s *Store) EnqueueOutbox(ctx context.Context, item OutboxItem) error {
	if err := s.ensureOutboxTable(); err != nil {
		return err
	}
	rcptJSON, _ := json.Marshal(item.Recipients)
	now := time.Now().Unix()
	if item.CreatedAt == 0 {
		item.CreatedAt = now
	}
	if item.NextRetryAt == 0 {
		item.NextRetryAt = now + 30
	}
	if item.Status == "" {
		item.Status = "queued"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO outbox (id, account_id, from_email, recipients_json, subject, draft_message_id, status, raw_eml, attempts, last_error, created_at, next_retry_at, scheduled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.AccountID, item.FromEmail, string(rcptJSON), item.Subject, item.DraftMessageID, item.Status, item.RawEML,
		item.Attempts, item.LastError, item.CreatedAt, item.NextRetryAt, item.ScheduledAt,
	)
	return err
}

func (s *Store) ListOutbox(ctx context.Context) ([]OutboxItem, error) {
	if err := s.ensureOutboxTable(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, from_email, recipients_json, subject, draft_message_id, status, raw_eml, attempts, last_error, created_at, next_retry_at, scheduled_at FROM outbox ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var rcptJSON string
		if err := rows.Scan(&it.ID, &it.AccountID, &it.FromEmail, &rcptJSON, &it.Subject, &it.DraftMessageID, &it.Status, &it.RawEML, &it.Attempts, &it.LastError, &it.CreatedAt, &it.NextRetryAt, &it.ScheduledAt); err != nil {
			continue
		}
		if it.Status == "" {
			it.Status = "queued"
		}
		_ = json.Unmarshal([]byte(rcptJSON), &it.Recipients)
		out = append(out, it)
	}
	return out, nil
}

// GetOutbox returns one outbox entry.
func (s *Store) GetOutbox(ctx context.Context, id string) (OutboxItem, error) {
	items, err := s.ListOutbox(ctx)
	if err != nil {
		return OutboxItem{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return OutboxItem{}, fmt.Errorf("not_found: Postausgang-Eintrag %q", id)
}

// RescheduleOutbox moves a scheduled entry's time to at (send now: at = now) and lets it be
// tried at once.
func (s *Store) RescheduleOutbox(ctx context.Context, id string, at int64) error {
	if err := s.ensureOutboxTable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET scheduled_at = ?, next_retry_at = ?, status = 'queued' WHERE id = ? AND scheduled_at > 0`, at, at, id)
	return err
}

func (s *Store) DeleteOutbox(ctx context.Context, id string) error {
	if err := s.ensureOutboxTable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM outbox WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateOutboxRetry(ctx context.Context, id string, attempts int, lastErr string, nextRetryAt int64) error {
	return s.UpdateOutboxStatus(ctx, id, attempts, "queued", lastErr, nextRetryAt)
}

func (s *Store) UpdateOutboxStatus(ctx context.Context, id string, attempts int, status, lastErr string, nextRetryAt int64) error {
	if err := s.ensureOutboxTable(); err != nil {
		return err
	}
	if status == "" {
		status = "queued"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET attempts = ?, status = ?, last_error = ?, next_retry_at = ? WHERE id = ?`, attempts, status, lastErr, nextRetryAt, id)
	return err
}

// isTransientNetworkError distinguishes offline/DNS/timeout dial errors (safe to queue in Outbox)
// from permanent auth/recipient rejections (which must be surfaced immediately).
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	low := strings.ToLower(err.Error())
	for _, kw := range []string{"no such host", "network is unreachable", "connection refused", "i/o timeout", "dial tcp", "temporary failure", "eof"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func isPermanentSMTPError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	for _, kw := range []string{
		"535 ", "535-", "550 ", "550-", "551 ", "552 ", "553 ", "554 ",
		"authentication failed", "auth failed", "invalid address", "recipient rejected",
		"relay access denied", "sender rejected",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// FlushOutbox attempts to transmit all due queued emails in the local Offline Outbox.
func (e *Engine) FlushOutbox(ctx context.Context) (sent int, remaining int) {
	return e.flushOutbox(ctx, false, "")
}

// FlushOutboxNow immediately attempts transmission of all queued/failed items in the Offline
// Outbox. Mails scheduled for later keep waiting for their time.
func (e *Engine) FlushOutboxNow(ctx context.Context) (sent int, remaining int) {
	return e.flushOutbox(ctx, true, "")
}

// SendOutboxNow sends one outbox entry at once – a scheduled one before its time, too.
func (e *Engine) SendOutboxNow(ctx context.Context, id string) error {
	it, err := e.store.GetOutbox(ctx, id)
	if err != nil {
		return err
	}
	if it.ScheduledAt > 0 {
		if err := e.store.RescheduleOutbox(ctx, id, time.Now().Unix()); err != nil {
			return err
		}
	}
	e.flushOutbox(ctx, true, id)
	if after, gerr := e.store.GetOutbox(ctx, id); gerr == nil {
		if after.LastError != "" {
			return errors.New(after.LastError)
		}
		return fmt.Errorf("nicht gesendet")
	}
	return nil
}

// UnscheduleOutbox takes a mail out of the outbox and puts it back into the account's Drafts
// folder (with its Bcc recipients, which the mail as sent leaves out).
func (e *Engine) UnscheduleOutbox(ctx context.Context, id string) error {
	// Under the flush lock: a flush sending this very mail finishes first and removes it (the
	// lookup below then fails), and no flush can send it while it moves to Drafts.
	e.outboxMu.Lock()
	defer e.outboxMu.Unlock()
	it, err := e.store.GetOutbox(ctx, id)
	if err != nil {
		return err
	}
	acc, ok := e.findAccount(it.AccountID)
	if !ok {
		return fmt.Errorf("not_found: Konto %q", it.AccountID)
	}
	draftsPath := "Drafts"
	draftsFolderID := FolderID(acc.ID, draftsPath)
	if f, ok := e.store.FindFolderByType(ctx, acc.ID, "drafts"); ok {
		draftsPath, draftsFolderID = f.Path, f.ID
	}
	raw := withBccHeader(it.RawEML, it.Recipients)
	parsed, _ := ParseRawMessage(raw)
	uid, err := e.appendMessageIMAP(ctx, acc, draftsPath, raw, []imap.Flag{imap.FlagDraft, imap.FlagSeen}, parsed.MessageID)
	if err != nil {
		return err
	}
	if uid > 0 {
		now := time.Now().UTC()
		sm := StoredMessage{
			ID: MessageStoreID(acc.ID, draftsPath, uid), AccountID: acc.ID, FolderID: draftsFolderID, FolderPath: draftsPath,
			UID: uid, MessageID: parsed.MessageID, DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(), Read: true, Size: int64(len(raw)),
		}
		mergeParsedIntoStored(&sm, raw)
		_ = e.store.UpsertMessage(ctx, sm)
	}
	if err := e.store.DeleteOutbox(ctx, id); err != nil {
		return err
	}
	e.emitOutboxChanged()
	e.emitMailSync(acc.ID)
	return nil
}

// outboxDue reports whether flushing (force: "send all now") tries item now.
func outboxDue(it OutboxItem, now int64, force bool) bool {
	if it.ScheduledAt > now {
		return false // "send later" waits for its time
	}
	if force {
		return true
	}
	return it.Status != "failed" && (it.NextRetryAt <= now || it.Attempts == 0)
}

// withDateHeader sets the Date header of raw to t: a mail sent later carries the time it went
// out, not the time it was written. The header is outside any signed or encrypted part.
func withDateHeader(raw []byte, t time.Time) []byte {
	return replaceHeader(raw, "Date", t.Format(time.RFC1123Z))
}

// withBccHeader adds a Bcc header naming the recipients that are neither in To nor Cc (raw as
// sent has none), so a draft made from it keeps them.
func withBccHeader(raw []byte, recipients []string) []byte {
	parsed, err := ParseRawMessage(raw)
	if err != nil {
		return raw
	}
	shown := map[string]bool{}
	for _, a := range ExtractAddresses(parsed.To, parsed.CC) {
		shown[strings.ToLower(a)] = true
	}
	var bcc []string
	for _, r := range recipients {
		if !shown[strings.ToLower(strings.TrimSpace(r))] {
			bcc = append(bcc, strings.TrimSpace(r))
		}
	}
	if len(bcc) == 0 {
		return raw
	}
	return replaceHeader(raw, "Bcc", strings.Join(bcc, ", "))
}

// replaceHeader sets header name in raw's header block to value (added in front when absent);
// folded continuation lines of the old value go with it.
func replaceHeader(raw []byte, name, value string) []byte {
	s := string(raw)
	end := strings.Index(s, "\r\n\r\n")
	nl := "\r\n"
	if end < 0 {
		end = strings.Index(s, "\n\n")
		nl = "\n"
	}
	if end < 0 {
		return raw
	}
	lines := strings.Split(s[:end], nl)
	out := make([]string, 0, len(lines)+1)
	prefix := strings.ToLower(name) + ":"
	replaced, skipping := false, false
	for _, ln := range lines {
		if skipping && (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) {
			continue
		}
		skipping = false
		if strings.HasPrefix(strings.ToLower(ln), prefix) {
			skipping = true
			if !replaced {
				out = append(out, name+": "+value)
				replaced = true
			}
			continue
		}
		out = append(out, ln)
	}
	if !replaced {
		out = append([]string{name + ": " + value}, out...)
	}
	return []byte(strings.Join(out, nl) + s[end:])
}

// ListOutbox returns all queued and failed outgoing messages in the local Outbox.
func (e *Engine) ListOutbox(ctx context.Context) ([]OutboxItem, error) {
	return e.store.ListOutbox(ctx)
}

// DeleteOutbox deletes a pending or failed entry from the local Outbox.
func (e *Engine) DeleteOutbox(ctx context.Context, id string) error {
	e.outboxMu.Lock() // not while a flush is sending it
	defer e.outboxMu.Unlock()
	err := e.store.DeleteOutbox(ctx, id)
	if err == nil {
		e.emitOutboxChanged()
	}
	return err
}

// flushOutbox sends the due entries (only: just that one). One flush at a time, so the
// periodic loop and "send now" never send the same mail twice.
func (e *Engine) flushOutbox(ctx context.Context, force bool, only string) (sent int, remaining int) {
	e.outboxMu.Lock()
	defer e.outboxMu.Unlock()
	items, err := e.store.ListOutbox(ctx)
	if err != nil || len(items) == 0 {
		return 0, 0
	}
	now := time.Now().Unix()
	changed := false
	for _, it := range items {
		if only != "" && it.ID != only {
			continue
		}
		if !outboxDue(it, now, force) {
			remaining++
			continue
		}
		if it.ScheduledAt > 0 {
			it.RawEML = withDateHeader(it.RawEML, time.Now())
		}
		acc, ok := e.findAccount(it.AccountID)
		if !ok {
			_ = e.store.UpdateOutboxStatus(ctx, it.ID, it.Attempts+1, "failed", "Konto nicht mehr vorhanden", now+3600)
			changed = true
			remaining++
			continue
		}
		pass, perr := e.accountPassword(acc.ID)
		if perr != nil {
			_ = e.store.UpdateOutboxStatus(ctx, it.ID, it.Attempts+1, "failed", fmt.Sprintf("Kein Passwort für %s", acc.Email), now+3600)
			changed = true
			remaining++
			continue
		}
		if serr := SendSMTP(ctx, acc, pass, it.FromEmail, it.Recipients, it.RawEML); serr != nil {
			attempts := it.Attempts + 1
			status := "queued"
			if isPermanentSMTPError(serr) || attempts >= 8 {
				status = "failed"
			}
			backoff := int64(min(3600, 30*(1<<min(attempts, 7))))
			_ = e.store.UpdateOutboxStatus(ctx, it.ID, attempts, status, serr.Error(), now+backoff)
			changed = true
			remaining++
			continue
		}
		_ = e.appendToSentFolder(ctx, acc, it.RawEML, "")
		if it.DraftMessageID != "" {
			e.deleteDraftIfExists(ctx, acc.ID, it.DraftMessageID)
		}
		_ = e.store.DeleteOutbox(ctx, it.ID)
		sent++
		changed = true
		e.emitMailSync(acc.ID)
	}
	if changed {
		e.emitOutboxChanged()
	}
	return sent, remaining
}
