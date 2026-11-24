package mailengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/backend"
)

func TestOutboxDueRespectsScheduleRetryAndForce(t *testing.T) {
	now := int64(1_000_000)
	cases := []struct {
		name  string
		it    OutboxItem
		force bool
		want  bool
	}{
		{"fresh queued", OutboxItem{NextRetryAt: now + 30}, false, true},
		{"retry not yet", OutboxItem{Attempts: 1, NextRetryAt: now + 60}, false, false},
		{"retry due", OutboxItem{Attempts: 1, NextRetryAt: now}, false, true},
		{"failed waits", OutboxItem{Status: "failed", Attempts: 8}, false, false},
		{"failed forced", OutboxItem{Status: "failed", Attempts: 8}, true, true},
		{"scheduled later", OutboxItem{ScheduledAt: now + 3600, NextRetryAt: now + 3600}, false, false},
		{"scheduled never forced early", OutboxItem{ScheduledAt: now + 3600}, true, false},
		{"scheduled time come", OutboxItem{ScheduledAt: now, NextRetryAt: now}, false, true},
	}
	for _, c := range cases {
		if got := outboxDue(c.it, now, c.force); got != c.want {
			t.Errorf("%s: outboxDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReplaceHeaderDateAndBcc(t *testing.T) {
	raw := []byte("From: a@example.org\r\nDate: Mon, 02 Jan 2006 15:04:05\r\n +0000\r\nTo: b@example.org\r\nSubject: Hi\r\n\r\nDate: stays in the body\r\n")
	when := time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)
	out := string(withDateHeader(raw, when))
	if !strings.Contains(out, "Date: Mon, 30 Nov 2026 08:00:00 +0000\r\nTo: b@example.org") {
		t.Fatalf("date header not replaced with its folded line:\n%s", out)
	}
	if strings.Count(out, "Date:") != 2 || !strings.HasSuffix(out, "\r\n\r\nDate: stays in the body\r\n") {
		t.Fatalf("body must stay untouched:\n%s", out)
	}
	bcc := string(withBccHeader(raw, []string{"b@example.org", "hidden@example.org"}))
	if !strings.HasPrefix(bcc, "Bcc: hidden@example.org\r\n") {
		t.Fatalf("bcc header expected for the hidden recipient only:\n%s", bcc)
	}
	if got := withBccHeader(raw, []string{"b@example.org"}); string(got) != string(raw) {
		t.Fatalf("no bcc recipients must leave the mail as is:\n%s", got)
	}
}

func TestComposeSendLaterWaitsInOutboxThenSendsOrGoesBackToDrafts(t *testing.T) {
	ctx := context.Background()
	eng, acc, _, _, smtpBody, cleanup := setupTestEngineWithIMAPAndSMTP(t, nil)
	defer cleanup()

	at := time.Now().Add(2 * time.Hour).Unix()
	raw, err := eng.Call(ctx, "compose.send", backend.ComposeRequest{
		AccountID: acc.ID,
		To:        []string{"alice@example.org"},
		BCC:       []string{"hidden@example.org"},
		Subject:   "Später",
		Body:      "Kommt morgen",
		SendAt:    at,
	})
	if err != nil {
		t.Fatalf("compose.send with sendAt: %v", err)
	}
	res := backend.ParseComposeResult(raw)
	if !res.Scheduled || res.ScheduledAt != at {
		t.Fatalf("expected a scheduled result, got %s", raw)
	}
	if *smtpBody != "" {
		t.Fatalf("a mail sent later must not go out now: %s", *smtpBody)
	}
	items, _ := eng.ListOutbox(ctx)
	if len(items) != 1 || items[0].ScheduledAt != at || items[0].Subject != "Später" {
		t.Fatalf("expected one scheduled outbox entry, got %+v", items)
	}
	// Neither the periodic flush nor "send all now" sends it before its time.
	eng.FlushOutbox(ctx)
	eng.FlushOutboxNow(ctx)
	if items, _ = eng.ListOutbox(ctx); len(items) != 1 || *smtpBody != "" {
		t.Fatalf("scheduled mail went out early: items=%d body=%q", len(items), *smtpBody)
	}

	// "Send now" sends it with the time it went out.
	if err := eng.SendOutboxNow(ctx, items[0].ID); err != nil {
		t.Fatalf("SendOutboxNow: %v", err)
	}
	if !strings.Contains(*smtpBody, "Kommt morgen") || strings.Contains(*smtpBody, "hidden@example.org") {
		t.Fatalf("sent mail unexpected (no bcc header allowed): %s", *smtpBody)
	}
	if items, _ = eng.ListOutbox(ctx); len(items) != 0 {
		t.Fatalf("outbox must be empty after send now, got %+v", items)
	}

	// A second one goes back into the drafts, with its Bcc.
	if _, err := eng.Call(ctx, "compose.send", backend.ComposeRequest{
		AccountID: acc.ID, To: []string{"alice@example.org"}, BCC: []string{"hidden@example.org"},
		Subject: "Doch nicht", Body: "Entwurf", SendAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	items, _ = eng.ListOutbox(ctx)
	if len(items) != 1 {
		t.Fatalf("expected one entry, got %d", len(items))
	}
	if err := eng.UnscheduleOutbox(ctx, items[0].ID); err != nil {
		t.Fatalf("UnscheduleOutbox: %v", err)
	}
	if items, _ = eng.ListOutbox(ctx); len(items) != 0 {
		t.Fatalf("outbox must be empty after back to drafts, got %+v", items)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 1 {
		t.Fatalf("expected the mail back in Drafts, got %d", n)
	}
	drafts, _, _ := eng.store.ListMessages(ctx, MessageQuery{AccountID: acc.ID, FolderID: FolderID(acc.ID, "Drafts")})
	if len(drafts) != 1 {
		t.Fatalf("expected one draft, got %d", len(drafts))
	}
	dRaw, err := eng.Call(ctx, "messages.get", map[string]any{"id": drafts[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var detail backend.MessageDetail
	_ = json.Unmarshal(dRaw, &detail)
	if detail.Subject != "Doch nicht" || strings.Join(detail.BCC, ",") != "hidden@example.org" {
		t.Fatalf("draft must keep subject and Bcc, got subject=%q bcc=%v", detail.Subject, detail.BCC)
	}
}
