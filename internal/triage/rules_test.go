package triage

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diver80/omathunder/internal/config"
)

func TestStage0RulesAndSpaceDomains(t *testing.T) {
	rules := []config.RoutingRule{
		{
			ID:         "r-acme",
			Space:      "Acme GmbH",
			FolderPath: "Spaces/Acme/Incoming",
			Domains:    []string{"acme.de", "acme-cloud.io"},
			AutoMove:   true,
		},
	}

	nl, matched := EvaluateStage0(MessageInput{
		ID:      "m1",
		Subject: "Weekly Digest",
		From:    "news@example.com",
		Headers: map[string]string{"List-Unsubscribe": "<mailto:unsub@example.com>"},
		Unread:  true,
	}, true, rules)
	if !matched || nl.Bucket != BucketNewsletter || nl.NeedsAttention {
		t.Fatalf("expected newsletter match, got matched=%v %+v", matched, nl)
	}

	inv, matched := EvaluateStage0(MessageInput{
		ID:          "m2",
		Subject:     "Ihre Rechnung Nr. 2026-42",
		From:        "Laura Weber <laura@mail.acme.de>",
		Attachments: []string{"Rechnung_2026_42.pdf"},
		Unread:      true,
	}, true, rules)
	if !matched || inv.Bucket != BucketInvoice || inv.Space != "Acme GmbH" || inv.TargetFolder != "Spaces/Acme/Incoming" {
		t.Fatalf("expected invoice + space rule match, got matched=%v %+v", matched, inv)
	}

	base, matched := EvaluateStage0(MessageInput{
		ID:       "m3",
		Subject:  "Können wir morgen um 10 telefonieren?",
		From:     "alex@acme.de",
		BodyText: "Hi, passt morgen 10 Uhr?",
		Unread:   true,
	}, true, rules)
	if matched || base.Space != "Acme GmbH" || base.MatchedRuleID != "r-acme" {
		t.Fatalf("expected personal space email to carry space metadata to Stage 1, got matched=%v %+v", matched, base)
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

func TestStoreKanbanMoveAndRuleRollback(t *testing.T) {
	stStore := NewStoreAt(t.TempDir())
	_, err := stStore.SaveClassifications([]Classification{
		{MessageID: "a1", ThreadID: "<hdr-a1@acme.de>", Subject: "Vertrag unterschreiben", Bucket: BucketAction, NeedsAttention: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	st, err := stStore.MoveKanban("a1", "waiting")
	if err != nil {
		t.Fatal(err)
	}
	if st.Classifications["a1"].KanbanColumn != "waiting" || st.Classifications["a1"].Bucket != BucketWaiting {
		t.Fatalf("expected waiting kanban column, got %+v", st.Classifications["a1"])
	}

	_, err = stStore.SaveClassifications([]Classification{
		{MessageID: "a1", ThreadID: "<hdr-a1@acme.de>", Subject: "Vertrag unterschreiben", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := stStore.Load(); err != nil || st.Classifications["a1"].KanbanColumn != "waiting" || !st.Classifications["a1"].PinnedColumn {
		t.Fatalf("expected pinned KanbanColumn 'waiting' to survive re-scan, got %+v err=%v", st.Classifications["a1"], err)
	}

	_, err = stStore.RecordMoves([]MovedMessage{
		{MessageID: "a99", OldMessageID: "a1", HeaderMessageID: "<hdr-a1@acme.de>", RuleID: "r-acme", OriginalFolder: "INBOX", TargetFolder: "Spaces/Acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := stStore.Load(); err != nil || st.Classifications["a99"].Folder != "Spaces/Acme" || st.Classifications["a1"].MessageID != "" {
		t.Fatalf("expected IMAP move ID migration a1 -> a99, got %+v", st.Classifications)
	}

	popped, st, err := stStore.PopMovedByRule("r-acme")
	if err != nil || len(popped) != 1 || len(st.MovedMessages) != 0 || st.Classifications["a99"].Folder != "INBOX" {
		t.Fatalf("expected clean rollback pop & folder restore, got popped=%+v cls=%+v err=%v", popped, st.Classifications["a99"], err)
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
			c, matched := EvaluateStage0(tc.msg, true, nil)
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

func TestStoreValidationPopMovedByScanAndBarSummary(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if _, err := s.SaveClassifications([]Classification{
		{MessageID: "1", ThreadID: "<1@x>", Subject: "Kickoff", Bucket: BucketEvent, KanbanColumn: "invoice", NeedsAttention: true, Date: "2026-11-10T10:00:00Z"},
		{MessageID: "2", ThreadID: "<2@x>", Subject: "Vertrag A", Bucket: BucketAction, Urgency: "high", NeedsAttention: true, Date: "2026-11-11T10:00:00Z"},
		{MessageID: "3", ThreadID: "<3@x>", Subject: "Vertrag B", Bucket: BucketAction, Urgency: "high", NeedsAttention: true, Date: "2026-11-11T10:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProposal(Proposal{ID: "p1", Status: "pending"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.MoveKanban("1", "invalid-col"); err == nil {
		t.Fatal("expected invalid kanban column to fail")
	}
	if _, err := s.UpdateProposalStatus("p1", "invalid-status"); err == nil {
		t.Fatal("expected invalid proposal status to fail")
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

	// Verify PopMovedByScan restores Folder & FolderID and keeps MatchedRuleID/Space.
	if _, err := s.SaveClassifications([]Classification{
		{MessageID: "50", ThreadID: "<50@x>", AccountID: "work", Space: "Acme", MatchedRuleID: "r1", Folder: "INBOX", FolderID: "f-in"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordMoves([]MovedMessage{
		{MessageID: "51", OldMessageID: "50", HeaderMessageID: "<50@x>", AccountID: "work", RuleID: "r1", ScanID: "scan-1", OriginalFolder: "INBOX", OriginalFolderID: "f-in", TargetFolder: "Spaces/Acme", TargetFolderID: "f-acme"},
	}); err != nil {
		t.Fatal(err)
	}
	popped, err := s.PopMovedByScan("scan-1")
	if err != nil || len(popped) != 1 {
		t.Fatalf("PopMovedByScan failed: popped=%+v err=%v", popped, err)
	}
	st2, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Classifications["51"]; got.Folder != "INBOX" || got.FolderID != "f-in" || got.MatchedRuleID != "r1" {
		t.Fatalf("expected PopMovedByScan to restore folder while keeping rule attribution, got %+v", got)
	}
}

func TestStoreDismissProposals(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	for _, p := range []Proposal{
		{ID: "p1", AccountID: "acc-a", Status: "pending"},
		{ID: "p2", AccountID: "acc-b", Status: "pending"},
		{ID: "p3", AccountID: "acc-b", Status: "applied"},
	} {
		if _, err := s.AddProposal(p); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.DismissProposals([]string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	if sum := ComputeBarSummary(st); sum.PendingProposals != 1 {
		t.Fatalf("expected 1 pending proposal after dismissing p1, got %d", sum.PendingProposals)
	}
	stAll, err := s.DismissProposals(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum := ComputeBarSummary(stAll); sum.PendingProposals != 0 {
		t.Fatalf("expected 0 pending proposals after dismiss all, got %d", sum.PendingProposals)
	}
	// Applied status must remain preserved.
	var p3Status string
	for _, p := range stAll.Proposals {
		if p.ID == "p3" {
			p3Status = p.Status
		}
	}
	if p3Status != "applied" {
		t.Fatalf("expected applied proposal p3 to stay 'applied', got %q", p3Status)
	}
}

func TestStoreSaveScanBatchDropsReadDoneAndRemoveClassifications(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	// Seed an unread newsletter in Done and a pinned read newsletter in Action.
	_, err := s.SaveScanBatch([]Classification{
		{MessageID: "nl-unread", ThreadID: "<nl1@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "nl-pinned", ThreadID: "<nl2@x>", Bucket: BucketNewsletter, KanbanColumn: "done"},
		{MessageID: "act-read", ThreadID: "<act@x>", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
	}, map[string]bool{
		"nl-unread": false,
		"nl-pinned": false,
		"act-read":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveKanban("nl-pinned", "action"); err != nil {
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

func TestStoreMoveKanbanBatch(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_, err := s.SaveClassifications([]Classification{
		{MessageID: "m1", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
		{MessageID: "m2", Bucket: BucketAction, KanbanColumn: "action", NeedsAttention: true},
		{MessageID: "m3", Bucket: BucketIncoming, KanbanColumn: "incoming", NeedsAttention: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.MoveKanbanBatch([]string{"m1", "m2", "missing", ""}, "done")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		c := st.Classifications[id]
		if c.KanbanColumn != "done" || !c.PinnedColumn || c.Bucket != BucketDone || c.NeedsAttention {
			t.Fatalf("expected %s moved & pinned to done, got %+v", id, c)
		}
	}
	if got := st.Classifications["m3"].KanbanColumn; got != "incoming" {
		t.Fatalf("expected m3 untouched in incoming, got %q", got)
	}
	if _, err := s.MoveKanbanBatch([]string{"m1"}, "invalid"); err == nil {
		t.Fatalf("expected invalid column to fail")
	}
}

