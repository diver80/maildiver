package triage

import "testing"

func TestSuggestQuickRuleAndPreview(t *testing.T) {
	// Case 1: Multiple mails from same sender with shared subject keyword -> AND(from, subject)
	draft := SuggestQuickRule([]MessageInput{
		{ID: "1", AccountID: "acc1", From: "Jira Bot <noreply@acme.de>", Subject: "[JIRA] Issue-101 updated"},
		{ID: "2", AccountID: "acc1", From: "noreply@acme.de", Subject: "[JIRA] Issue-204 closed"},
	}, "de")
	if len(draft.FromValues) != 1 || draft.FromValues[0] != "noreply@acme.de" {
		t.Fatalf("expected sender noreply@acme.de, got %+v", draft.FromValues)
	}
	if !draft.MatchAll || len(draft.SubjectValues) != 1 || draft.SubjectValues[0] != "[JIRA]" {
		t.Fatalf("expected MatchAll=true with [JIRA] subject token, got matchAll=%v subj=%+v", draft.MatchAll, draft.SubjectValues)
	}
	if draft.Action != "trash" {
		t.Fatalf("expected safe default action trash, got %q", draft.Action)
	}

	conds := BuildQuickRuleConditions(draft.FromValues, draft.SubjectValues, draft.MatchAll)
	marked := map[string]bool{"1": true, "2": true}
	candidates := []MessageInput{
		{ID: "1", AccountID: "acc1", From: "Jira Bot <noreply@acme.de>", Subject: "[JIRA] Issue-101 updated", Folder: "INBOX"},
		{ID: "2", AccountID: "acc1", From: "noreply@acme.de", Subject: "[JIRA] Issue-204 closed", Folder: "INBOX"},
		{ID: "3", AccountID: "acc1", From: "noreply@acme.de", Subject: "Security Audit Notice", Folder: "INBOX"},
		{ID: "4", AccountID: "acc1", From: "noreply@acme.de", Subject: "[JIRA] Issue-309 assigned", Folder: "INBOX"},
	}
	rep := EvaluateQuickRulePreview(conds, draft.MatchAll, marked, candidates, draft.Action)
	if rep.MarkedTotal != 2 || rep.MarkedMatched != 2 {
		t.Fatalf("expected 2/2 marked matched, got %d/%d", rep.MarkedMatched, rep.MarkedTotal)
	}
	if rep.ExtraTotal != 1 || rep.ExtraMatches[0].ID != "4" {
		t.Fatalf("expected only extra [JIRA] mail #4 matched (not #3), got %+v", rep.ExtraMatches)
	}

	// Case 2: Different senders, different domains -> OR list of senders
	multi := SuggestQuickRule([]MessageInput{
		{ID: "a", AccountID: "acc1", From: "Deals <deals@shop1.de>", Subject: "Sale heute"},
		{ID: "b", AccountID: "acc1", From: "Promo <news@shop2.com>", Subject: "Rabattaktion"},
	}, "en")
	if multi.MatchAll || len(multi.FromValues) != 2 {
		t.Fatalf("expected OR with 2 unique senders, got matchAll=%v from=%+v", multi.MatchAll, multi.FromValues)
	}

	// Case 3: Bare domain + trash triggers broad-rule warning
	domConds := BuildQuickRuleConditions([]string{"@shop1.de"}, nil, false)
	domRep := EvaluateQuickRulePreview(domConds, false, map[string]bool{"a": true}, []MessageInput{
		{ID: "a", From: "deals@shop1.de", Subject: "Sale"},
		{ID: "x", From: "billing@shop1.de", Subject: "Rechnung"},
	}, "trash")
	if domRep.BroadWarningDE == "" || domRep.BroadWarningEN == "" {
		t.Fatalf("expected broad-rule warning for bare domain trash rule")
	}
}
