package triage

import (
	"testing"

	"github.com/diver80/omathunder/internal/config"
)

func TestDiagnoseFiltersRealWorldCases(t *testing.T) {
	existsTrue := true
	existsFalse := false
	filters := []NativeFilter{
		{
			AccountID: "acc3",
			Name:      "Inbox » REWE (Systemmails)",
			Enabled:   true,
			MatchAll:  true, // classic AND bug!
			Conditions: []NativeCondition{
				{Field: "subject", Op: "contains", Value: "Automated restore completed for Confluence in rewe", BooleanAnd: true},
				{Field: "subject", Op: "contains", Value: "Automaterd restore started for Confluence in rewe", BooleanAnd: true},
			},
			Actions: []NativeAction{
				{Type: "moveToFolder", TargetFolderPath: "INBOX/REWE", TargetFolderExists: &existsTrue},
				{Type: "markRead"},
			},
		},
		{
			AccountID: "acc3",
			Name:      "SPAM",
			Enabled:   true,
			MatchAll:  true,
			Conditions: []NativeCondition{
				{Field: "subject", Op: "is", Value: "Your on-call rotation for axsteam_schedule is starting now", BooleanAnd: true},
				{Field: "subject", Op: "contains", Value: "FEV-Confluence@fev.com", BooleanAnd: true},
			},
			Actions: []NativeAction{{Type: "delete"}},
		},
		{
			AccountID: "acc3",
			Name:      "Atlassian Mails join product",
			Enabled:   false,
			MatchAll:  false,
			Conditions: []NativeCondition{
				{Field: "subject", Op: "contains", Value: "join their team in Jira Product Discovery", BooleanAnd: false},
			},
			Actions: []NativeAction{{Type: "moveToFolder", TargetFolderPath: "Trash", TargetFolderExists: &existsTrue}},
		},
		{
			AccountID: "acc3",
			Name:      "Jira Mails",
			Enabled:   true,
			MatchAll:  true,
			Conditions: []NativeCondition{
				{Field: "header", HeaderName: "x-mailer", Op: "contains", Value: "Atlassian Jira", BooleanAnd: true},
			},
			Actions: []NativeAction{{Type: "moveToFolder", TargetFolderPath: "INBOX/Jira", TargetFolderExists: &existsFalse}},
		},
	}

	// Case 1: REWE mail matches 1 of 2 AND conditions -> and_partial_match + QuickFix set_or
	rep1 := DiagnoseFilters(MessageInput{
		AccountID: "acc3",
		Subject:   "Automated restore completed for Confluence in rewe",
		From:      "noreply@rewe.de",
	}, filters)
	if len(rep1.MatchedFilters) != 0 {
		t.Fatalf("expected 0 matched filters, got %+v", rep1.MatchedFilters)
	}
	if len(rep1.NearMisses) != 1 || rep1.NearMisses[0].ReasonCode != "and_partial_match" || rep1.NearMisses[0].QuickFix != "set_or" {
		t.Fatalf("expected and_partial_match quickfix set_or, got %+v", rep1.NearMisses)
	}

	// Case 2: SPAM mail has sender address stored in subject condition -> wrong_field
	rep2 := DiagnoseFilters(MessageInput{
		AccountID: "acc3",
		Subject:   "Another alert",
		From:      "FEV-Confluence@fev.com",
	}, filters)
	if len(rep2.NearMisses) == 0 || rep2.NearMisses[0].ReasonCode != "wrong_field" {
		t.Fatalf("expected wrong_field near miss, got %+v", rep2.NearMisses)
	}

	// Case 3: Disabled filter that matches -> disabled + QuickFix enable
	rep3 := DiagnoseFilters(MessageInput{
		AccountID: "acc3",
		Subject:   "Max wants to join their team in Jira Product Discovery?",
		From:      "noreply@atlassian.com",
	}, filters)
	if len(rep3.NearMisses) != 1 || !rep3.NearMisses[0].WouldMatchIfEnabled || rep3.NearMisses[0].QuickFix != "enable" {
		t.Fatalf("expected disabled near-miss, got %+v", rep3.NearMisses)
	}

	// Case 4: Custom header filter matches -> matched
	rep4 := DiagnoseFilters(MessageInput{
		AccountID: "acc3",
		Subject:   "[JIRA] Ticket updated",
		From:      "jira@avono.de",
		Headers:   map[string]string{"X-Mailer": "Atlassian Jira 9.12"},
	}, filters)
	if len(rep4.MatchedFilters) != 1 || rep4.MatchedFilters[0].FilterName != "Jira Mails" {
		t.Fatalf("expected Jira Mails to match via x-mailer, got %+v", rep4.MatchedFilters)
	}

	// Structural issues check: should detect disabled_filter, and_mutually_exclusive, email_in_subject, missing_folder
	codes := map[string]bool{}
	for _, iss := range rep1.Issues {
		codes[iss.Code] = true
	}
	for _, want := range []string{"disabled_filter", "and_mutually_exclusive", "email_in_subject", "missing_folder"} {
		if !codes[want] {
			t.Fatalf("missing expected issue %q in %+v", want, rep1.Issues)
		}
	}
}

func TestSuggestSpaceRulesFromFilters(t *testing.T) {
	filters := []NativeFilter{
		{
			AccountID:   "acc3",
			AccountName: "frank.hess@avono.de",
			Name:        "Inbox » Börse",
			Enabled:     true,
			Conditions: []NativeCondition{
				{Field: "from", Op: "endsWith", Value: "@boerse-stuttgart.de"},
				{Field: "from", Op: "endsWith", Value: "@bsdigital.com"},
			},
			Actions: []NativeAction{{Type: "moveToFolder", TargetFolderPath: "INBOX/Boerse"}},
		},
		{
			AccountID:   "acc3",
			AccountName: "frank.hess@avono.de",
			Name:        "frank.hess@avono.de » Inbox => REWE",
			Enabled:     true,
			Conditions: []NativeCondition{
				{Field: "from", Op: "endsWith", Value: "@rewe-group.com"},
				{Field: "from", Op: "endsWith", Value: "@rewe.atlassian.net"},
			},
			Actions: []NativeAction{{Type: "moveToFolder", TargetFolderPath: "INBOX/REWE"}},
		},
		{
			AccountID:  "acc3",
			Name:       "Trash Atlassian",
			Enabled:    true,
			Conditions: []NativeCondition{{Field: "subject", Op: "contains", Value: "join"}},
			Actions:    []NativeAction{{Type: "moveToFolder", TargetFolderPath: "Trash"}},
		},
	}
	existing := []config.RoutingRule{
		{ID: "r1", AccountID: "acc3", Space: "REWE", FolderPath: "INBOX/REWE"},
	}

	cands := SuggestSpaceRulesFromFilters(filters, existing)
	if len(cands) != 2 {
		t.Fatalf("expected 2 space candidates (skipping Trash), got %d: %+v", len(cands), cands)
	}
	if cands[0].Space != "Boerse" || len(cands[0].Domains) != 2 || cands[0].AlreadyImported {
		t.Fatalf("unexpected first candidate: %+v", cands[0])
	}
	if cands[1].Space != "REWE" || !cands[1].AlreadyImported {
		t.Fatalf("expected REWE to be marked alreadyImported=true, got %+v", cands[1])
	}
}
