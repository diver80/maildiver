package triage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStage0Rules(t *testing.T) {
	nl, matched := EvaluateStage0(MessageInput{
		ID:      "m1",
		Subject: "Weekly Digest",
		From:    "news@example.com",
		Headers: map[string]string{"List-Unsubscribe": "<mailto:unsub@example.com>"},
		Unread:  true,
	}, true)
	if !matched || nl.Bucket != BucketNewsletter || nl.NeedsAttention {
		t.Fatalf("expected newsletter match, got matched=%v %+v", matched, nl)
	}

	inv, matched := EvaluateStage0(MessageInput{
		ID:          "m2",
		Subject:     "Ihre Rechnung Nr. 2026-42",
		From:        "Laura Weber <laura@mail.acme.de>",
		Attachments: []string{"Rechnung_2026_42.pdf"},
		Unread:      true,
	}, true)
	if !matched || inv.Bucket != BucketInvoice {
		t.Fatalf("expected invoice match, got matched=%v %+v", matched, inv)
	}

	base, matched := EvaluateStage0(MessageInput{
		ID:       "m3",
		Subject:  "Können wir morgen um 10 telefonieren?",
		From:     "alex@acme.de",
		BodyText: "Hi, passt morgen 10 Uhr?",
		Unread:   true,
	}, true)
	if matched || base.Stage != "stage0-rule" || base.MessageID != "m3" {
		t.Fatalf("expected a personal email to go on to Stage 1, got matched=%v %+v", matched, base)
	}
}

func TestPrepareSnippetStripsQuotesAndSignatures(t *testing.T) {
	raw := "Hallo,\nkannst du das bitte freigeben?\n\n-- \nViele Grüße\nMax\n> Alter Text\n> noch mehr"
	got := PrepareSnippet(raw, 500)
	if strings.Contains(got, "Viele Grüße") || strings.Contains(got, "Alter Text") {
		t.Fatalf("expected signature/quotes stripped, got %q", got)
	}
	if !strings.Contains(got, "freigeben?") {
		t.Fatalf("expected main body preserved, got %q", got)
	}
}

func TestStage0DoesNotHideTransactionalMail(t *testing.T) {
	cases := []struct {
		name    string
		msg     MessageInput
		matched bool
	}{
		{"bare noreply sender goes to Stage 1", MessageInput{From: "noreply@shop.example", Subject: "Ihr Paket ist unterwegs"}, false},
		{"password reset is not downgraded", MessageInput{From: "noreply@bank.example", Subject: "Passwort zurücksetzen", Headers: map[string]string{"Auto-Submitted": "auto-generated"}}, false},
		{"verification code is not downgraded", MessageInput{From: "no-reply@svc.example", Subject: "Your verification code", Headers: map[string]string{"Auto-Submitted": "auto-generated"}}, false},
		{"CI notification is FYI", MessageInput{From: "notifications@github.com", Subject: "[repo] Run failed"}, true},
		{"out-of-office is FYI", MessageInput{From: "max@acme.de", Subject: "Abwesend bis Montag", Headers: map[string]string{"Auto-Submitted": "auto-replied"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, matched := EvaluateStage0(tc.msg, true)
			if matched != tc.matched {
				t.Fatalf("matched=%v, want %v (%+v)", matched, tc.matched, c)
			}
			if matched && c.Bucket != BucketFYI {
				t.Fatalf("expected FYI bucket, got %s", c.Bucket)
			}
		})
	}
}

func TestPrepareSnippetUTF8AndExactSignature(t *testing.T) {
	// "--" without trailing space should NOT cut off the body; "-- " (RFC 3676) should.
	raw := "Punkt 1: Prüfung läuft\n--\nPunkt 2: Überweisung 500 €\n\n-- \nSignaturzeile"
	got := PrepareSnippet(raw, 200)
	if !strings.Contains(got, "Punkt 2: Überweisung 500 €") {
		t.Fatalf("inline '--' must not cut body, got %q", got)
	}
	if strings.Contains(got, "Signaturzeile") {
		t.Fatalf("RFC 3676 '-- ' must strip signature, got %q", got)
	}

	// Truncating multi-byte UTF-8 runes must preserve valid UTF-8 and fit maxRunes (+ ellipsis).
	longUTF8 := strings.Repeat("äöü€—", 30)
	trunc := PrepareSnippet(longUTF8, 12)
	if !utf8.ValidString(trunc) {
		t.Fatalf("truncated snippet produced invalid UTF-8: %q", trunc)
	}
	if utf8.RuneCountInString(trunc) != 13 || !strings.HasSuffix(trunc, "…") {
		t.Fatalf("expected 12 runes + ellipsis, got %d runes (%q)", utf8.RuneCountInString(trunc), trunc)
	}
}

func TestStoreBarSummary(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if _, err := s.SaveClassifications([]Classification{
		{MessageID: "1", ThreadID: "<1@x>", Subject: "Kickoff", Bucket: BucketEvent, KanbanColumn: "invoice", NeedsAttention: true, Date: "2026-11-10T10:00:00Z"},
		{MessageID: "2", ThreadID: "<2@x>", Subject: "Vertrag A", Bucket: BucketAction, Urgency: "high", NeedsAttention: true, Date: "2026-11-11T10:00:00Z"},
		{MessageID: "3", ThreadID: "<3@x>", Subject: "Vertrag B", Bucket: BucketAction, Urgency: "high", NeedsAttention: true, Date: "2026-11-11T10:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	// Verify BarSummary counts EventCount for BucketEvent even in invoice column, and is deterministic.
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	sum := ComputeBarSummary(st)
	if sum.EventCount != 1 {
		t.Fatalf("expected EventCount=1, got %d", sum.EventCount)
	}
	if sum.TopHeadline != "Vertrag A" {
		t.Fatalf("expected deterministic tie-break by MessageID ('Vertrag A'), got %q", sum.TopHeadline)
	}
}

func TestStoreSaveScanBatchDropsReadDoneAndRemoveClassifications(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	// Seed an unread newsletter in Done and a pinned read newsletter in Action.
	_, err := s.SaveScanBatch([]Classification{
		{MessageID: "nl-unread", ThreadID: "<nl1@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "nl-pinned", ThreadID: "<nl2@x>", Bucket: BucketAction, KanbanColumn: "action", PinnedColumn: true, NeedsAttention: true},
		{MessageID: "act-read", ThreadID: "<act@x>", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
	}, map[string]bool{
		"nl-unread": false,
		"nl-pinned": false,
		"act-read":  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-scan: nl-unread is now read -> removed from Done; nl-pinned stays in Action even though read;
	// brand new already-read nl-new-read is not added at all.
	st, err := s.SaveScanBatch([]Classification{
		{MessageID: "nl-unread-newid", ThreadID: "<nl1@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "nl-pinned", ThreadID: "<nl2@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "nl-new-read", ThreadID: "<nl3@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "act-read", ThreadID: "<act@x>", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
	}, map[string]bool{
		"nl-unread-newid": true,
		"nl-pinned":       true,
		"nl-new-read":     true,
		"act-read":        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := st.Classifications["nl-unread"]; exists {
		t.Fatalf("expected previously unread Done card to be removed once read")
	}
	if _, exists := st.Classifications["nl-unread-newid"]; exists {
		t.Fatalf("expected newly scanned read Done card not to be saved")
	}
	if _, exists := st.Classifications["nl-new-read"]; exists {
		t.Fatalf("expected already-read Done card to be omitted")
	}
	if got := st.Classifications["nl-pinned"]; got.KanbanColumn != "action" {
		t.Fatalf("expected pinned card in action to survive even when read, got %+v", got)
	}
	if _, exists := st.Classifications["act-read"]; !exists {
		t.Fatalf("expected read card in action column to stay on board")
	}

	st2, err := s.RemoveClassifications([]string{"act-read", "non-existent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := st2.Classifications["act-read"]; exists {
		t.Fatalf("expected RemoveClassifications to delete act-read")
	}
}

func TestFolderKanbanColumnMapping(t *testing.T) {
	cases := []struct {
		folder string
		col    string
		ok     bool
	}{
		{"INBOX/work/todo", "action", true},
		{"INBOX/work/To-Do", "action", true},
		{"INBOX/ClientA/waiting", "waiting", true},
		{"INBOX/ClientA/Wartet", "waiting", true},
		{"INBOX/ClientB/done", "done", true},
		{"INBOX/ClientB/Erledigt", "done", true},
		{"INBOX/work/Reise", "", false},
		{"INBOX/work/Teams 26", "", false},
		{"INBOX/atlassian", "", false},
		{"INBOX", "", false},
	}
	for _, tc := range cases {
		col, ok := FolderKanbanColumn(tc.folder)
		if ok != tc.ok || col != tc.col {
			t.Fatalf("FolderKanbanColumn(%q) = (%q, %v), want (%q, %v)", tc.folder, col, ok, tc.col, tc.ok)
		}
	}

	cTodo, matched := EvaluateStage0(MessageInput{
		ID:      "todo-1",
		Folder:  "INBOX/work/todo",
		Subject: "Bitte kurz pruefen",
		From:    "max@example.org",
		Unread:  true,
	}, true)
	if matched || cTodo.KanbanColumn != "action" || cTodo.Bucket != BucketAction || !cTodo.NeedsAttention {
		t.Fatalf("expected todo subfolder mail to pre-set action column on base, got matched=%v %+v", matched, cTodo)
	}

	cWait, matched := EvaluateStage0(MessageInput{
		ID:      "wait-1",
		Folder:  "INBOX/ClientA/waiting",
		Subject: "Angebot versendet",
		From:    "ich@example.org",
		Unread:  false,
	}, true)
	if matched || cWait.KanbanColumn != "waiting" || cWait.Bucket != BucketWaiting || cWait.NeedsAttention {
		t.Fatalf("expected waiting subfolder mail to pre-set waiting column on base, got matched=%v %+v", matched, cWait)
	}

	// Neutral subfolders like "Reise" must leave KanbanColumn empty for Stage 1/fallback.
	cOther, matchedOther := EvaluateStage0(MessageInput{
		ID:      "reise-1",
		Folder:  "INBOX/work/Reise",
		Subject: "Hotelbuchung Rueckfrage",
		From:    "kollege@example.org",
		Unread:  true,
	}, true)
	if matchedOther || cOther.KanbanColumn != "" {
		t.Fatalf("expected neutral subfolder to continue to Stage 1 without column override, got matched=%v %+v", matchedOther, cOther)
	}

	// Already classified invoice gets ApplyFolderKanbanColumn applied when placed into todo.
	inv := Classification{MessageID: "i1", Bucket: BucketInvoice, KanbanColumn: "invoice"}
	if !ApplyFolderKanbanColumn(&inv, "INBOX/work/todo") || inv.KanbanColumn != "action" {
		t.Fatalf("expected ApplyFolderKanbanColumn to map todo to action, got %+v", inv)
	}
}

func TestBodyForAIMarksEncryptedMails(t *testing.T) {
	if got := (MessageInput{BodyText: "Hallo"}).BodyForAI(); got != "Hallo" {
		t.Errorf("plain = %q", got)
	}
	if got := (MessageInput{Encrypted: true}).BodyForAI(); got != EncryptedPlaceholder {
		t.Errorf("encrypted without text = %q", got)
	}
	if got := (MessageInput{Encrypted: true, BodyText: "decrypted on request"}).BodyForAI(); got != "decrypted on request" {
		t.Errorf("decrypted = %q", got)
	}
}
