package mailengine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

func TestPhase4ArchiveCandidatesFindsOldMailsPast1000Limit(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{{
		ID: "acc-arch", Name: "Work", Email: "alice@example.com", Enabled: true,
	}}
	cfg.Mailbox.TriageAccountIDs = []string{"acc-arch"}
	eng, err := New(Options{
		DBPath:       dbPath,
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()

	inboxID := FolderID("acc-arch", "INBOX")
	if err := eng.store.UpsertFolder(ctx, StoredFolder{
		ID: inboxID, AccountID: "acc-arch", Name: "INBOX", Path: "INBOX", Type: "inbox",
	}); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}

	now := time.Now().UTC()
	oldTime := now.AddDate(-2, 0, 0)
	// Insert 3 old read messages (>1 year old), 1 old starred, 1 old unread, 1 zero-date, and 1050 recent messages.
	for i := 0; i < 3; i++ {
		ts := oldTime.Add(time.Duration(i) * time.Hour)
		_ = eng.store.UpsertMessage(ctx, StoredMessage{
			ID: fmt.Sprintf("old-%d", i), AccountID: "acc-arch", FolderID: inboxID, FolderPath: "INBOX",
			UID: uint32(10 + i), Subject: fmt.Sprintf("Old Mail %d", i), From: "bob@example.com",
			DateISO: ts.Format(time.RFC3339), DateUnix: ts.Unix(), Read: true, Size: 1024,
		})
	}
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "old-star", AccountID: "acc-arch", FolderID: inboxID, FolderPath: "INBOX",
		UID: 20, Subject: "Starred Old", From: "bob@example.com",
		DateISO: oldTime.Format(time.RFC3339), DateUnix: oldTime.Unix(), Read: true, Flagged: true, Size: 1024,
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "old-unread", AccountID: "acc-arch", FolderID: inboxID, FolderPath: "INBOX",
		UID: 21, Subject: "Unread Old", From: "bob@example.com",
		DateISO: oldTime.Format(time.RFC3339), DateUnix: oldTime.Unix(), Read: false, Size: 1024,
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "zero-date", AccountID: "acc-arch", FolderID: inboxID, FolderPath: "INBOX",
		UID: 22, Subject: "No Date", From: "bob@example.com",
		DateISO: "", DateUnix: 0, Read: true, Size: 1024,
	})
	// Insert 1050 newer messages directly via SQL so the test runs in milliseconds.
	tx, err := eng.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1050; i++ {
		ts := now.Add(-time.Duration(i) * time.Minute)
		_, err := tx.ExecContext(ctx, `INSERT INTO messages(id, account_id, folder_id, folder_path, uid, message_id, thread_id, subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, tags_json, size, body_text, body_html, body_fetched, attachments_json, headers_json, has_ics, ics_body)
			VALUES(?,?,?,?,?,?,?,?,?,'[]','[]',?,?,1,0,'[]',512,'','',0,'[]','{}',0,'')`,
			fmt.Sprintf("new-%d", i), "acc-arch", inboxID, "INBOX", 100+i, fmt.Sprintf("m-%d", i), fmt.Sprintf("t-%d", i),
			fmt.Sprintf("Recent %d", i), "news@example.com", ts.Format(time.RFC3339), ts.Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	days := 365
	raw, err := eng.CallHost(ctx, "host.archive.candidates", map[string]any{
		"accountId":     "acc-arch",
		"scope":         "inbox",
		"olderThanDays": days,
	})
	if err != nil {
		t.Fatalf("host.archive.candidates: %v", err)
	}
	var resp struct {
		Candidates []struct {
			MessageID string `json:"messageId"`
		} `json:"candidates"`
		TotalMatched int   `json:"totalMatched"`
		TotalBytes   int64 `json:"totalBytesAll"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalMatched != 3 || len(resp.Candidates) != 3 {
		t.Fatalf("expected 3 old read unstarred candidates (ignoring 1050 newer + starred + unread + zero-date), got totalMatched=%d len=%d", resp.TotalMatched, len(resp.Candidates))
	}
	if resp.TotalBytes != 3072 {
		t.Fatalf("expected totalBytesAll=3072, got %d", resp.TotalBytes)
	}
}

func TestPhase4TriageCollectProtectsEncryptedAndPassesHeadersUnreadFirst(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{{
		ID: "acc-tri", Name: "Work", Email: "alice@example.com", Enabled: true,
	}}
	cfg.Mailbox.TriageAccountIDs = []string{"acc-tri"}
	eng, err := New(Options{
		DBPath:       dbPath,
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()

	inboxID := FolderID("acc-tri", "INBOX")
	_ = eng.store.UpsertFolder(ctx, StoredFolder{
		ID: inboxID, AccountID: "acc-tri", Name: "INBOX", Path: "INBOX", Type: "inbox",
	})

	now := time.Now().UTC()
	// 1. Encrypted S/MIME message (must have Encrypted=true, BodyText="", Attachments=nil)
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "msg-enc", AccountID: "acc-tri", FolderID: inboxID, FolderPath: "INBOX", UID: 1,
		Subject: "Confidential Contract", From: "legal@example.com",
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(), Read: false, BodyFetched: true,
		BodyText:    "TOP SECRET LEAK TEST",
		Headers:     map[string]string{"content-type": `application/pkcs7-mime; smime-type=enveloped-data; name="smime.p7m"`},
		Attachments: []backend.Attachment{{Name: "smime.p7m", ContentType: "application/pkcs7-mime"}},
	})
	// 2. Newsletter with List-Unsubscribe header (must preserve Headers for Stage-0 skip!)
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "msg-news", AccountID: "acc-tri", FolderID: inboxID, FolderPath: "INBOX", UID: 2,
		Subject: "Weekly Digest", From: "digest@example.org",
		DateISO: now.Add(-time.Hour).Format(time.RFC3339), DateUnix: now.Add(-time.Hour).Unix(),
		Read: true, BodyFetched: true, BodyText: "Latest news",
		Headers: map[string]string{"list-unsubscribe": "<mailto:unsub@example.org>"},
	})
	// 3. Old unread mail (>30 days old — still collected because unread-first!)
	oldUnread := now.AddDate(0, -3, 0)
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "msg-old-unread", AccountID: "acc-tri", FolderID: inboxID, FolderPath: "INBOX", UID: 3,
		Subject: "Still Waiting", From: "partner@example.com",
		DateISO: oldUnread.Format(time.RFC3339), DateUnix: oldUnread.Unix(), Read: false, BodyFetched: true,
		BodyText: "Please reply",
	})

	raw, err := eng.CallHost(ctx, "host.triage.collect", map[string]any{
		"accountId": "acc-tri",
		"limit":     10,
		"days":      30,
		"max":       50,
	})
	if err != nil {
		t.Fatalf("host.triage.collect: %v", err)
	}
	var resp struct {
		Messages []triage.MessageInput `json:"messages"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(resp.Messages))
	}
	byID := map[string]triage.MessageInput{}
	for _, m := range resp.Messages {
		byID[m.ID] = m
	}
	enc := byID["msg-enc"]
	if !enc.Encrypted || enc.BodyText != "" || len(enc.Attachments) != 0 {
		t.Fatalf("encrypted mail leaked to triage: encrypted=%v body=%q atts=%v", enc.Encrypted, enc.BodyText, enc.Attachments)
	}
	news := byID["msg-news"]
	if news.Headers["list-unsubscribe"] == "" {
		t.Fatalf("List-Unsubscribe header missing from triage input: %+v", news.Headers)
	}
	if _, matched := triage.EvaluateStage0(news, true); !matched {
		t.Fatalf("Stage-0 newsletter rule should match with preserved List-Unsubscribe header")
	}
	if _, ok := byID["msg-old-unread"]; !ok {
		t.Fatalf("old unread mail should be collected unread-first")
	}
}
