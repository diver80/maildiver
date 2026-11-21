package triage

import "testing"

func TestEnhancedNativeFilterMatchingCCAttachmentsRegexAndHeaders(t *testing.T) {
	msgs := []MessageInput{
		{
			ID:          "m1",
			AccountID:   "acc-1",
			Subject:     "Rechnung #2026-42",
			From:        "billing@vendor.example",
			To:          []string{"alex@example.com"},
			CC:          []string{"finance@example.com"},
			Headers:     map[string]string{"list-id": "<invoices.vendor.example>"},
			BodyText:    "Bitte überweisen Sie den Betrag bis Freitag.",
			Attachments: []string{"Rechnung_2026_42.pdf"},
		},
		{
			ID:       "m2",
			Subject:  "Team Standup",
			From:     "bob@example.com",
			To:       []string{"alex@example.com"},
			BodyText: "Treffen um 10 Uhr.",
		},
	}

	conds := []NativeCondition{
		{Field: "cc", Op: "contains", Value: "finance@example.com", BooleanAnd: true},
		{Field: "body", Op: "contains", Value: "überweisen", BooleanAnd: true},
		{Field: "attachment", Op: "regex", Value: `rechnung_\d+_\d+\.pdf`, BooleanAnd: true},
		{Field: "header", HeaderName: "List-Id", Op: "contains", Value: "invoices.vendor.example", BooleanAnd: true},
	}
	rep := EvaluateQuickRulePreview(conds, true, map[string]bool{"m1": true, "m2": true}, msgs, "")
	if rep.MarkedMatched != 1 || len(rep.MatchedIDs) != 1 || rep.MatchedIDs[0] != "m1" {
		t.Fatalf("expected only m1 to match enhanced conditions, got %+v", rep)
	}

	emptyAtt := EvaluateQuickRulePreview(
		[]NativeCondition{{Field: "attachment", Op: "isEmpty"}},
		true,
		map[string]bool{"m1": true, "m2": true},
		msgs,
		"",
	)
	if emptyAtt.MarkedMatched != 1 || len(emptyAtt.MatchedIDs) != 1 || emptyAtt.MatchedIDs[0] != "m2" {
		t.Fatalf("expected attachment isEmpty to match m2 only, got %+v", emptyAtt)
	}
}
