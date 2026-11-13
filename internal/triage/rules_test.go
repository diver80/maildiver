package triage

import (
	"strings"
	"testing"

	"github.com/fhess/omathunder/internal/config"
)

func TestStage0RulesAndCustomerDomains(t *testing.T) {
	rules := []config.RoutingRule{
		{
			ID:         "r-acme",
			Customer:   "Acme GmbH",
			FolderPath: "Kunden/Acme/Incoming",
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
	if !matched || inv.Bucket != BucketInvoice || inv.Customer != "Acme GmbH" || inv.TargetFolder != "Kunden/Acme/Incoming" {
		t.Fatalf("expected invoice + customer rule match, got matched=%v %+v", matched, inv)
	}

	base, matched := EvaluateStage0(MessageInput{
		ID:       "m3",
		Subject:  "Können wir morgen um 10 telefonieren?",
		From:     "alex@acme.de",
		BodyText: "Hi, passt morgen 10 Uhr?",
		Unread:   true,
	}, true, rules)
	if matched || base.Customer != "Acme GmbH" || base.MatchedRuleID != "r-acme" {
		t.Fatalf("expected personal customer email to carry customer metadata to Stage 1, got matched=%v %+v", matched, base)
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
		{MessageID: "a1", Subject: "Vertrag unterschreiben", Bucket: BucketAction, NeedsAttention: true},
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
		{MessageID: "a99", OldMessageID: "a1", HeaderMessageID: "<hdr-a1@acme.de>", RuleID: "r-acme", OriginalFolder: "INBOX", TargetFolder: "Kunden/Acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := stStore.Load(); err != nil || st.Classifications["a99"].Folder != "Kunden/Acme" || st.Classifications["a1"].MessageID != "" {
		t.Fatalf("expected IMAP move ID migration a1 -> a99, got %+v", st.Classifications)
	}

	popped, st, err := stStore.PopMovedByRule("r-acme")
	if err != nil || len(popped) != 1 || len(st.MovedMessages) != 0 || st.Classifications["a99"].Folder != "INBOX" {
		t.Fatalf("expected clean rollback pop & folder restore, got popped=%+v cls=%+v err=%v", popped, st.Classifications["a99"], err)
	}
}
