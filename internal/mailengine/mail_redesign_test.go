package mailengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/secretstore"
	"github.com/diver80/postdive/internal/triage"
)

func TestStoreAndEngineSnoozeWaitingAndSubscriptions(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{
		{
			ID:    "acc-1",
			Name:  "Work",
			Email: "me@example.org",
			Aliases: []config.AccountAlias{
				{Email: "alias@example.org"},
			},
		},
	}

	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		Events:       events.NewHub(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(config.Config) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	now := time.Now()
	fourDaysAgo := now.Add(-4 * 24 * time.Hour)
	twoDaysLater := now.Add(48 * time.Hour)

	if err := eng.store.UpsertFolder(ctx, StoredFolder{
		ID: "acc-1:INBOX", AccountID: "acc-1", Path: "INBOX", Name: "Posteingang", Type: "inbox",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.store.UpsertFolder(ctx, StoredFolder{
		ID: "acc-1:Sent", AccountID: "acc-1", Path: "Sent", Name: "Gesendet", Type: "sent",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.store.UpsertFolder(ctx, StoredFolder{
		ID: "acc-1:Archive", AccountID: "acc-1", Path: "Archive", Name: "Archiv", Type: "archive",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Snooze test messages
	if err := eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-focus", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 1, MessageID: "<focus@example.org>", ThreadID: "th-focus",
		Subject: "Urgent Offer", From: "Alice <alice@client.de>", To: []string{"me@example.org"},
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(), Read: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.SnoozeMessage(ctx, "m-focus", twoDaysLater.Unix()); err != nil {
		t.Fatalf("SnoozeMessage failed: %v", err)
	}
	if got := eng.CountSnoozed(ctx, "acc-1"); got != 1 {
		t.Fatalf("CountSnoozed = %d, want 1", got)
	}
	notSnoozed, _, err := eng.store.ListMessages(ctx, MessageQuery{
		AccountID: "acc-1", ExcludeSnoozed: true, UnreadOrFlagged: true,
	})
	if err != nil || len(notSnoozed) != 0 {
		t.Fatalf("ExcludeSnoozed should exclude snoozed mail: len=%d err=%v", len(notSnoozed), err)
	}
	onlySnoozed, _, err := eng.store.ListMessages(ctx, MessageQuery{
		AccountID: "acc-1", SnoozedOnly: true,
	})
	if err != nil || len(onlySnoozed) != 1 || onlySnoozed[0].ID != "m-focus" {
		t.Fatalf("SnoozedOnly unexpected result: %+v err=%v", onlySnoozed, err)
	}
	// Clear snooze via RPC
	if _, err := eng.Call(ctx, "messages.snooze", map[string]any{"id": "m-focus", "untilUnix": 0}); err != nil {
		t.Fatalf("RPC messages.snooze clear failed: %v", err)
	}
	if got := eng.CountSnoozed(ctx, "acc-1"); got != 0 {
		t.Fatalf("expected CountSnoozed=0 after unsnooze, got %d", got)
	}

	// 2. Waiting-for-reply messages (local SQLite only)
	// Sent 4d ago with NO reply -> should appear
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-wait", AccountID: "acc-1", FolderID: "acc-1:Sent", FolderPath: "Sent",
		UID: 10, MessageID: "<wait-1@example.org>", ThreadID: "th-wait",
		Subject: "Angebot Q4", From: "me@example.org", To: []string{"bob@partner.de"},
		DateISO: fourDaysAgo.Format(time.RFC3339), DateUnix: fourDaysAgo.Unix(), Read: true,
	})
	// Sent 4d ago WITH a reply yesterday -> should NOT appear
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-replied-sent", AccountID: "acc-1", FolderID: "acc-1:Sent", FolderPath: "Sent",
		UID: 11, MessageID: "<rep-1@example.org>", ThreadID: "th-replied",
		Subject: "Termin", From: "me@example.org", To: []string{"carol@partner.de"},
		DateISO: fourDaysAgo.Format(time.RFC3339), DateUnix: fourDaysAgo.Unix(), Read: true,
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-replied-in", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 2, MessageID: "<rep-2@partner.de>", ThreadID: "th-replied", InReplyTo: "<rep-1@example.org>",
		Subject: "Re: Termin", From: "carol@partner.de", To: []string{"me@example.org"},
		DateISO: now.Add(-24 * time.Hour).Format(time.RFC3339), DateUnix: now.Add(-24 * time.Hour).Unix(), Read: false,
	})
	// Sent self-note to own alias -> should NOT appear
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-self", AccountID: "acc-1", FolderID: "acc-1:Sent", FolderPath: "Sent",
		UID: 12, MessageID: "<self-1@example.org>", ThreadID: "th-self",
		Subject: "Notiz an mich", From: "me@example.org", To: []string{"alias@example.org"},
		DateISO: fourDaysAgo.Format(time.RFC3339), DateUnix: fourDaysAgo.Unix(), Read: true,
	})

	waiting, err := eng.ListWaitingReplyMessages(ctx, "acc-1")
	if err != nil {
		t.Fatalf("ListWaitingReplyMessages failed: %v", err)
	}
	if len(waiting) != 1 || waiting[0].ID != "m-wait" {
		t.Fatalf("expected only m-wait in waiting list, got %+v", waiting)
	}

	// 3. Subscriptions aggregation (Inbox only, Archive ignored, One-Click detected)
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-sub-1", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 3, MessageID: "<news-1@digest.io>", Subject: "Issue #1",
		From: "Daily Digest <news@digest.io>", To: []string{"me@example.org"},
		DateISO: fourDaysAgo.Format(time.RFC3339), DateUnix: fourDaysAgo.Unix(),
		Read: false, BodyFetched: true,
		Headers: map[string]string{
			"list-unsubscribe":      "<https://digest.io/unsub/1>, <mailto:unsub@digest.io>",
			"list-unsubscribe-post": "List-Unsubscribe=One-Click",
		},
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-sub-2", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 4, MessageID: "<news-2@digest.io>", Subject: "Issue #2",
		From: "Daily Digest <news@digest.io>", To: []string{"me@example.org"},
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(),
		Read: true, BodyFetched: true,
		Headers: map[string]string{
			"list-unsubscribe": "<https://digest.io/unsub/2>",
		},
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-sub-arch", AccountID: "acc-1", FolderID: "acc-1:Archive", FolderPath: "Archive",
		UID: 1, MessageID: "<arch@old.io>", Subject: "Archived Newsletter",
		From: "Old News <news@old.io>", To: []string{"me@example.org"},
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(),
		Read: true, BodyFetched: true,
		Headers: map[string]string{"list-unsubscribe": "<https://old.io/unsub>"},
	})

	subs, err := eng.ListSubscriptions(ctx, "acc-1")
	if err != nil {
		t.Fatalf("ListSubscriptions failed: %v", err)
	}
	if len(subs) != 1 || subs[0].AccountID != "acc-1" || subs[0].SenderEmail != "news@digest.io" || subs[0].Count != 2 || subs[0].UnreadCount != 1 || !subs[0].OneClick || subs[0].UnsubscribeHTTPS != "https://digest.io/unsub/1" {
		t.Fatalf("unexpected subscriptions result: %+v", subs)
	}
	var _ backend.SubscriptionSummary = subs[0]
}

func TestWakeExpiredSnoozedAndEncryptedThreadRedaction(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{
		{ID: "acc-1", Name: "Work", Email: "me@example.org"},
	}
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		Events:       events.NewHub(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(config.Config) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	now := time.Now()
	_ = eng.store.UpsertFolder(ctx, StoredFolder{ID: "acc-1:INBOX", AccountID: "acc-1", Path: "INBOX", Name: "INBOX", Type: "inbox"})
	_ = eng.store.UpsertFolder(ctx, StoredFolder{ID: "acc-1:Archiv", AccountID: "acc-1", Path: "Archiv", Name: "Archiv", Type: "archives"})
	_ = eng.store.UpsertFolder(ctx, StoredFolder{ID: "acc-1:Archive/2025", AccountID: "acc-1", Path: "Archive/2025", Name: "2025", Type: ""})

	// 1. Expired snoozed mail (was read) wakes up unread with snoozed_until=0; future stays snoozed.
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-wake", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 1, MessageID: "<wake@example.org>", ThreadID: "th-1",
		Subject: "Expired snooze", From: "alice@example.org", To: []string{"me@example.org"},
		DateISO: now.Add(-2 * time.Hour).Format(time.RFC3339), DateUnix: now.Add(-2 * time.Hour).Unix(),
		Read: true,
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-keep", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 2, MessageID: "<keep@example.org>", ThreadID: "th-1", InReplyTo: "<wake@example.org>",
		Subject: "Re: Expired snooze", From: "bob@example.org", To: []string{"me@example.org"},
		DateISO: now.Add(-1 * time.Hour).Format(time.RFC3339), DateUnix: now.Add(-1 * time.Hour).Unix(),
		Read: false,
	})
	if err := eng.SnoozeMessage(ctx, "m-wake", now.Add(-60*time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := eng.SnoozeMessage(ctx, "m-keep", now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	woke, err := eng.WakeExpiredSnoozed(ctx)
	if err != nil {
		t.Fatalf("WakeExpiredSnoozed: %v", err)
	}
	if len(woke) != 1 || woke[0].ID != "m-wake" || !woke[0].Unread || woke[0].SnoozedUntil != 0 {
		t.Fatalf("unexpected woke messages: %+v", woke)
	}
	if got := eng.CountSnoozed(ctx, "acc-1"); got != 1 {
		t.Fatalf("expected 1 future snoozed mail remaining, got %d", got)
	}

	// 2. messages.thread redacts encrypted/inline-PGP content and preserves plaintext siblings.
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-pgp", AccountID: "acc-1", FolderID: "acc-1:INBOX", FolderPath: "INBOX",
		UID: 3, MessageID: "<pgp@example.org>", ThreadID: "th-1", InReplyTo: "<wake@example.org>",
		Subject: "Re: Secret", From: "alice@example.org", To: []string{"me@example.org"},
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(),
		BodyText: "-----BEGIN PGP MESSAGE-----\nwcBMA...\n-----END PGP MESSAGE-----",
		BodyFetched: true,
	})
	rawThread, err := eng.Call(ctx, "messages.thread", map[string]any{"id": "m-wake", "limit": 10})
	if err != nil {
		t.Fatalf("messages.thread: %v", err)
	}
	var parsed struct {
		Messages []triage.MessageInput `json:"messages"`
	}
	if err := json.Unmarshal(rawThread, &parsed); err != nil {
		t.Fatal(err)
	}
	var sawPlain, sawPGP bool
	for _, tm := range parsed.Messages {
		switch tm.ID {
		case "m-wake":
			sawPlain = true
			if tm.Encrypted {
				t.Fatalf("expected plaintext m-wake not marked encrypted: %+v", tm)
			}
		case "m-pgp":
			sawPGP = true
			if !tm.Encrypted || tm.BodyText != "" {
				t.Fatalf("expected inline PGP message redacted in messages.thread: %+v", tm)
			}
		}
	}
	if !sawPlain || !sawPGP {
		t.Fatalf("missing thread messages: plain=%v pgp=%v (%+v)", sawPlain, sawPGP, parsed.Messages)
	}

	// 3. ListSubscriptions excludes 'archives' type and localized Archiv / Archive/2025 folders.
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-sub-archiv", AccountID: "acc-1", FolderID: "acc-1:Archiv", FolderPath: "Archiv",
		UID: 1, MessageID: "<a1@news.io>", Subject: "Archiv News", From: "A <a@news.io>",
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(), BodyFetched: true,
		Headers: map[string]string{"list-unsubscribe": "<https://news.io/u>"},
	})
	_ = eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-sub-arch25", AccountID: "acc-1", FolderID: "acc-1:Archive/2025", FolderPath: "Archive/2025",
		UID: 1, MessageID: "<a2@news.io>", Subject: "2025 News", From: "B <b@news.io>",
		DateISO: now.Format(time.RFC3339), DateUnix: now.Unix(), BodyFetched: true,
		Headers: map[string]string{"list-unsubscribe": "<https://news.io/u2>"},
	})
	subs, err := eng.ListSubscriptions(ctx, "acc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected archived subscriptions excluded, got %+v", subs)
	}
}
