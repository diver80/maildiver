package mailengine

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

// OutboxItem represents an email queued locally while offline or during transient SMTP outages.
type OutboxItem struct {
	ID          string   `json:"id"`
	AccountID   string   `json:"accountId"`
	FromEmail   string   `json:"fromEmail"`
	Recipients  []string `json:"recipients"`
	Subject     string   `json:"subject"`
	RawEML      []byte   `json:"-"`
	Attempts    int      `json:"attempts"`
	LastError   string   `json:"lastError,omitempty"`
	CreatedAt   int64    `json:"createdAt"`
	NextRetryAt int64    `json:"nextRetryAt"`
}

func (s *Store) ensureOutboxTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS outbox (
		id TEXT PRIMARY KEY,
		account_id TEXT NOT NULL,
		from_email TEXT NOT NULL,
		recipients_json TEXT NOT NULL,
		subject TEXT NOT NULL,
		raw_eml BLOB NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		next_retry_at INTEGER NOT NULL
	)`)
	return err
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
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO outbox (id, account_id, from_email, recipients_json, subject, raw_eml, attempts, last_error, created_at, next_retry_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.AccountID, item.FromEmail, string(rcptJSON), item.Subject, item.RawEML,
		item.Attempts, item.LastError, item.CreatedAt, item.NextRetryAt,
	)
	return err
}

func (s *Store) ListOutbox(ctx context.Context) ([]OutboxItem, error) {
	if err := s.ensureOutboxTable(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, from_email, recipients_json, subject, raw_eml, attempts, last_error, created_at, next_retry_at FROM outbox ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var rcptJSON string
		if err := rows.Scan(&it.ID, &it.AccountID, &it.FromEmail, &rcptJSON, &it.Subject, &it.RawEML, &it.Attempts, &it.LastError, &it.CreatedAt, &it.NextRetryAt); err != nil {
			continue
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
	if err := s.ensureOutboxTable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET attempts = ?, last_error = ?, next_retry_at = ? WHERE id = ?`, attempts, lastErr, nextRetryAt, id)
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

// FlushOutbox attempts to transmit all due queued emails in the local Offline Outbox.
func (e *Engine) FlushOutbox(ctx context.Context) (sent int, remaining int) {
	items, err := e.store.ListOutbox(ctx)
	if err != nil || len(items) == 0 {
		return 0, 0
	}
	now := time.Now().Unix()
	for _, it := range items {
		if it.NextRetryAt > now && it.Attempts > 0 {
			remaining++
			continue
		}
		acc, ok := e.findAccount(it.AccountID)
		if !ok {
			remaining++
			continue
		}
		pass, perr := e.accountPassword(acc.ID)
		if perr != nil {
			remaining++
			continue
		}
		if serr := SendSMTP(ctx, acc, pass, it.FromEmail, it.Recipients, it.RawEML); serr != nil {
			attempts := it.Attempts + 1
			backoff := int64(min(3600, 30*(1<<min(attempts, 7))))
			_ = e.store.UpdateOutboxRetry(ctx, it.ID, attempts, serr.Error(), now+backoff)
			remaining++
			continue
		}
		sentPath := "Sent"
		if f, ok := e.store.FindFolderByType(ctx, acc.ID, "sent"); ok {
			sentPath = f.Path
		}
		_, _ = e.appendMessageIMAP(ctx, acc, sentPath, it.RawEML, []imap.Flag{imap.FlagSeen}, "")
		_ = e.store.DeleteOutbox(ctx, it.ID)
		sent++
		e.emitMailSync(acc.ID)
	}
	return sent, remaining
}
