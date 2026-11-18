package triage

import (
	"testing"

	"github.com/diver80/omathunder/internal/config"
)

func TestPlanAutoMovesSafetyRules(t *testing.T) {
	rules := []config.RoutingRule{
		{ID: "acme", Space: "Acme", AccountID: "work", FolderPath: "Kunden/Acme", FolderID: "f-acme", Domains: []string{"acme.de"}, AutoMove: true},
		{ID: "beta", Space: "Beta", AccountID: "work", FolderPath: "Kunden/Beta", Domains: []string{"beta.com"}, AutoMove: false},
		{ID: "free", Space: "Free", FolderPath: "Kunden/Free", Domains: []string{"free.org"}, AutoMove: true},
	}
	policy := config.MailboxPolicy{TriageAccountIDs: []string{"work"}, AutoMoveOnlyUnread: true}
	inbox := func(mut func(*MessageInput)) MessageInput {
		m := MessageInput{ID: "1", ThreadID: "<a@acme.de>", AccountID: "work", Folder: "INBOX", FolderID: "f-inbox", InInbox: true, From: "Laura <laura@acme.de>", Unread: true}
		if mut != nil {
			mut(&m)
		}
		return m
	}

	cases := []struct {
		name string
		msg  MessageInput
		want bool
	}{
		{"unread inbox mail of enabled account", inbox(nil), true},
		{"subdomain matches", inbox(func(m *MessageInput) { m.From = "x@mail.acme.de" }), true},
		{"rule without account moves within the mail's account", inbox(func(m *MessageInput) { m.From = "x@free.org" }), true},
		{"read mail is skipped when only unread", inbox(func(m *MessageInput) { m.Unread = false }), false},
		{"not in inbox", inbox(func(m *MessageInput) { m.InInbox = false; m.Folder = "Archiv" }), false},
		{"subfolder of customer folder is never flattened", inbox(func(m *MessageInput) { m.InInbox = false; m.Folder = "Kunden/Acme/Projekt" }), false},
		{"account not enabled", inbox(func(m *MessageInput) { m.AccountID = "private" }), false},
		{"rule bound to another account", inbox(func(m *MessageInput) { m.AccountID = "work2" }), false},
		{"autoMove off", inbox(func(m *MessageInput) { m.From = "x@beta.com" }), false},
		{"no rule", inbox(func(m *MessageInput) { m.From = "x@unknown.net" }), false},
		{"lookalike domain", inbox(func(m *MessageInput) { m.From = "x@notacme.de" }), false},
		{"missing Message-ID", inbox(func(m *MessageInput) { m.ThreadID = "" }), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanAutoMoves([]MessageInput{tc.msg}, rules, policy)
			if (len(got) == 1) != tc.want {
				t.Fatalf("want move=%v, got %+v", tc.want, got)
			}
		})
	}

	policy.AutoMoveOnlyUnread = false
	got := PlanAutoMoves([]MessageInput{inbox(func(m *MessageInput) { m.Unread = false })}, rules, policy)
	if len(got) != 1 {
		t.Fatal("read inbox mail should move when only-unread is off")
	}
	if got[0].TargetFolder != "Kunden/Acme" || got[0].TargetFolderID != "f-acme" || got[0].SourceFolderID != "f-inbox" {
		t.Fatalf("unexpected plan: %+v", got[0])
	}
}

func TestPlanAutoMovesFolderMatchNeverMoves(t *testing.T) {
	rules := []config.RoutingRule{{ID: "acme", AccountID: "work", FolderPath: "Kunden/Acme", Domains: []string{"acme.de"}, AutoMove: true}}
	policy := config.MailboxPolicy{TriageAccountIDs: []string{"work"}}
	// Own reply filed in the customer's subfolder: matches by folder, never by domain.
	m := MessageInput{ID: "9", ThreadID: "<b@me>", AccountID: "work", Folder: "Kunden/Acme/Erledigt", From: "me@mycompany.de"}
	if got := PlanAutoMoves([]MessageInput{m}, rules, policy); len(got) != 0 {
		t.Fatalf("folder-based match must not move: %+v", got)
	}
}

func TestStateIsPrunedToNewestMails(t *testing.T) {
	st := State{Classifications: map[string]Classification{}}
	for i := 0; i < 30; i++ {
		id := string(rune('a' + i))
		st.Classifications[id] = Classification{MessageID: id, Date: "2026-11-" + string(rune('0'+i/10)) + string(rune('0'+i%10))}
	}
	st.Classifications["old-pinned"] = Classification{MessageID: "old-pinned", Date: "2020-01-01", PinnedColumn: true}
	pruneClassifications(&st, 10)
	if len(st.Classifications) != 10 {
		t.Fatalf("expected 10 cards, got %d", len(st.Classifications))
	}
	if _, ok := st.Classifications["old-pinned"]; !ok {
		t.Fatal("cards the user placed manually must survive pruning")
	}
	if _, ok := st.Classifications["a"]; ok {
		t.Fatal("oldest unpinned card should be dropped")
	}
}

func TestPlanAutoMovesSkipsFailedSenderAuth(t *testing.T) {
	rules := []config.RoutingRule{{ID: "acme", AccountID: "work", FolderPath: "Kunden/Acme", Domains: []string{"acme.de"}, AutoMove: true}}
	policy := config.MailboxPolicy{TriageAccountIDs: []string{"work"}}
	m := MessageInput{ID: "1", ThreadID: "<x@acme.de>", AccountID: "work", Folder: "INBOX", InInbox: true, From: "ceo@acme.de", Unread: true}
	m.Headers = map[string]string{"authentication-results": "mx.example; dkim=pass; spf=pass; dmarc=pass"}
	if len(PlanAutoMoves([]MessageInput{m}, rules, policy)) != 1 {
		t.Fatal("authenticated mail should move")
	}
	m.Headers = map[string]string{"Authentication-Results": "mx.example; spf=softfail; dmarc=fail (p=none)"}
	if got := PlanAutoMoves([]MessageInput{m}, rules, policy); len(got) != 0 {
		t.Fatalf("spoofed sender must not be filed into the customer folder: %+v", got)
	}
}

func TestStoreDoesNotMergeDifferentMailsWithSameNumericID(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	old := Classification{MessageID: "7", ThreadID: "y@x", Subject: "Re: Angebot", KanbanColumn: "done", PinnedColumn: true, Bucket: BucketDone}
	if _, err := s.SaveClassifications([]Classification{old}); err != nil {
		t.Fatal(err)
	}
	// After a restart, a new mail X gets the same numeric ID 7.
	fresh := Classification{MessageID: "7", ThreadID: "x@x", Subject: "Re: Angebot", Bucket: BucketAction, NeedsAttention: true}
	st, err := s.SaveClassifications([]Classification{fresh})
	if err != nil {
		t.Fatal(err)
	}
	got := st.Classifications["7"]
	if got.ThreadID != "x@x" || got.PinnedColumn || got.KanbanColumn != "action" {
		t.Fatalf("new mail must not inherit the old card: %+v", got)
	}
}

func TestPlanAutoMovesSubjectAndFromContains(t *testing.T) {
	rules := []config.RoutingRule{{
		ID:              "atlassian-trials",
		Space:           "Atlassian Testanfragen",
		AccountID:       "work",
		FolderPath:      "Notifications/Atlassian-Tests",
		Domains:         []string{"atlassian.com"},
		FromContains:    []string{"noreply@"},
		SubjectContains: []string{"würde gerne", "would like to try"},
		AutoMove:        true,
	}}
	policy := config.MailboxPolicy{TriageAccountIDs: []string{"work"}}

	trialMail := MessageInput{
		ID:        "10",
		ThreadID:  "<trial@atlassian.com>",
		AccountID: "work",
		Folder:    "INBOX",
		InInbox:   true,
		From:      "Atlassian <noreply@am.atlassian.com>",
		Subject:   "Max Mustermann würde gerne Jira Service Management testen",
	}
	billingMail := MessageInput{
		ID:        "11",
		ThreadID:  "<bill@atlassian.com>",
		AccountID: "work",
		Folder:    "INBOX",
		InInbox:   true,
		From:      "Atlassian Billing <billing@atlassian.com>",
		Subject:   "Invoice #AT-9912 due",
	}
	supportMail := MessageInput{
		ID:        "12",
		ThreadID:  "<sup@atlassian.com>",
		AccountID: "work",
		Folder:    "INBOX",
		InInbox:   true,
		From:      "Atlassian <noreply@am.atlassian.com>",
		Subject:   "Security alert: API token created",
	}

	got := PlanAutoMoves([]MessageInput{trialMail, billingMail, supportMail}, rules, policy)
	if len(got) != 1 || got[0].MessageID != "10" || got[0].TargetFolder != "Notifications/Atlassian-Tests" {
		t.Fatalf("expected only trial mail to match fine-grained rule, got %+v", got)
	}
}

func TestPlanAutoMovesMultiAccountSameDomain(t *testing.T) {
	rules := []config.RoutingRule{
		{ID: "r-priv", Space: "Acme Priv", AccountID: "priv", FolderPath: "Privat/Acme", FolderID: "f-priv", Domains: []string{"acme.de"}, AutoMove: true},
		{ID: "r-work", Space: "Acme Work", AccountID: "work", FolderPath: "Kunden/Acme", FolderID: "f-work", Domains: []string{"acme.de"}, AutoMove: true},
	}
	policy := config.MailboxPolicy{TriageAccountIDs: []string{"priv", "work"}}
	msg := MessageInput{
		ID:        "10",
		ThreadID:  "<m10@acme.de>",
		AccountID: "work",
		Folder:    "INBOX",
		FolderID:  "work-inbox",
		InInbox:   true,
		From:      "laura@acme.de",
		Unread:    true,
	}
	got := PlanAutoMoves([]MessageInput{msg}, rules, policy)
	if len(got) != 1 || got[0].RuleID != "r-work" || got[0].TargetFolderID != "f-work" {
		t.Fatalf("expected work rule to match without being shadowed by priv rule, got %+v", got)
	}
}
