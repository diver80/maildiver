package mailengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
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
		`INSERT OR REPLACE INTO outbox (id, account_id, from_email, recipients_json, subject, draft_message_id, status, raw_eml, attempts, last_error, created_at, next_retry_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.AccountID, item.FromEmail, string(rcptJSON), item.Subject, item.DraftMessageID, item.Status, item.RawEML,
		item.Attempts, item.LastError, item.CreatedAt, item.NextRetryAt,
	)
	return err
}

func (s *Store) ListOutbox(ctx context.Context) ([]OutboxItem, error) {
	if err := s.ensureOutboxTable(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, from_email, recipients_json, subject, draft_message_id, status, raw_eml, attempts, last_error, created_at, next_retry_at FROM outbox ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var rcptJSON string
		if err := rows.Scan(&it.ID, &it.AccountID, &it.FromEmail, &rcptJSON, &it.Subject, &it.DraftMessageID, &it.Status, &it.RawEML, &it.Attempts, &it.LastError, &it.CreatedAt, &it.NextRetryAt); err != nil {
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
	return e.flushOutbox(ctx, false)
}

// FlushOutboxNow immediately attempts transmission of all queued/failed items in the Offline Outbox.
func (e *Engine) FlushOutboxNow(ctx context.Context) (sent int, remaining int) {
	return e.flushOutbox(ctx, true)
}

// ListOutbox returns all queued and failed outgoing messages in the local Outbox.
func (e *Engine) ListOutbox(ctx context.Context) ([]OutboxItem, error) {
	return e.store.ListOutbox(ctx)
}

// DeleteOutbox deletes a pending or failed entry from the local Outbox.
func (e *Engine) DeleteOutbox(ctx context.Context, id string) error {
	err := e.store.DeleteOutbox(ctx, id)
	if err == nil {
		e.emitOutboxChanged()
	}
	return err
}

func (e *Engine) flushOutbox(ctx context.Context, force bool) (sent int, remaining int) {
	items, err := e.store.ListOutbox(ctx)
	if err != nil || len(items) == 0 {
		return 0, 0
	}
	now := time.Now().Unix()
	changed := false
	for _, it := range items {
		if !force && (it.Status == "failed" || (it.NextRetryAt > now && it.Attempts > 0)) {
			remaining++
			continue
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
