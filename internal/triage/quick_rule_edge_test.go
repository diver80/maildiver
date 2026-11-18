package triage

import (
	"fmt"
	"strings"
	"testing"
)

func hasWarning(rep QuickRulePreviewReport, code string) bool {
	for _, w := range rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestSuggestQuickRuleEmptyInput(t *testing.T) {
	d := SuggestQuickRule(nil, "en")
	if len(d.FromValues) != 0 || len(d.SubjectValues) != 0 {
		t.Fatalf("expected empty draft, got %+v", d)
	}
	if d.Name == "" || d.Action != "trash" {
		t.Fatalf("expected default name and trash action, got %+v", d)
	}
	if de := SuggestQuickRule(nil, "de"); de.Name == d.Name {
		t.Fatalf("expected localized default names to differ, both %q", d.Name)
	}
}

func TestSuggestQuickRuleFreeMailNeverBareDomain(t *testing.T) {
	// Two different gmail senders sharing a subject must NOT become "@gmail.com AND Hello".
	d := SuggestQuickRule([]MessageInput{
		{ID: "1", AccountID: "a", From: "Anna <anna.k@gmail.com>", Subject: "Hello there"},
		{ID: "2", AccountID: "a", From: "bert99@gmail.com", Subject: "Hello again"},
	}, "de")
	for _, v := range d.FromValues {
		if strings.HasPrefix(v, "@") || strings.EqualFold(v, "gmail.com") {
			t.Fatalf("free-mail domain used as bare domain: %+v", d.FromValues)
		}
	}
	if len(d.FromValues) != 2 || d.MatchAll {
		t.Fatalf("expected OR over the 2 full addresses, got matchAll=%v from=%+v", d.MatchAll, d.FromValues)
	}
	for _, dom := range []string{"gmail.com", "googlemail.com", "gmx.de", "gmx.net", "web.de", "outlook.com", "hotmail.de", "hotmail.co.uk", "live.com", "yahoo.fr", "de.yahoo.com", "icloud.com", "me.com", "t-online.de", "posteo.de", "mailbox.org", "proton.me", "protonmail.com", "aol.com", "freenet.de"} {
		if !IsSharedMailDomain(dom) {
			t.Errorf("expected %s to be a shared mail domain", dom)
		}
	}
	for _, dom := range []string{"acme.de", "paypal.de", "livestream.com", "deliver.yahooapis.example"} {
		if IsSharedMailDomain(dom) {
			t.Errorf("did not expect %s to be a shared mail domain", dom)
		}
	}

	// A user-entered "@gmail.com" rule is flagged and needs confirmation for trash.
	conds := BuildQuickRuleConditions([]string{"@gmail.com"}, []string{"Hello there"}, true)
	rep := EvaluateQuickRulePreview(conds, true, map[string]bool{"1": true}, []MessageInput{
		{ID: "1", From: "anna.k@gmail.com", Subject: "Hello there"},
	}, "trash")
	if !hasWarning(rep, "shared_domain") || !rep.RequiresConfirm {
		t.Fatalf("expected shared_domain warning + confirmation, got %+v", rep.Warnings)
	}
}

func TestSuggestQuickRuleSingleSenderSingleMail(t *testing.T) {
	d := SuggestQuickRule([]MessageInput{{ID: "p1", AccountID: "a", From: "PayPal <service@paypal.de>", Subject: "Ihre Zahlung an Shop"}}, "de")
	if len(d.FromValues) != 1 || d.FromValues[0] != "service@paypal.de" || len(d.SubjectValues) != 0 {
		t.Fatalf("expected sender-only draft for one mail, got %+v", d)
	}
	conds := BuildQuickRuleConditions(d.FromValues, d.SubjectValues, d.MatchAll)
	rep := EvaluateQuickRulePreview(conds, d.MatchAll, map[string]bool{"p1": true}, []MessageInput{
		{ID: "p1", From: "PayPal <service@paypal.de>", Subject: "Ihre Zahlung an Shop"},
	}, "trash")
	if !hasWarning(rep, "sender_only_single") || !rep.RequiresConfirm {
		t.Fatalf("single mail + sender-only + trash must require confirmation, got %+v", rep.Warnings)
	}
	// Non-destructive action: warning-free save.
	rep = EvaluateQuickRulePreview(conds, d.MatchAll, map[string]bool{"p1": true}, []MessageInput{
		{ID: "p1", From: "PayPal <service@paypal.de>", Subject: "Ihre Zahlung an Shop"},
	}, "mark_read")
	if rep.RequiresConfirm {
		t.Fatalf("mark_read must not require confirmation, got %+v", rep.Warnings)
	}
}

func TestSuggestQuickRuleMoreThanEightSenders(t *testing.T) {
	var msgs []MessageInput
	for i := 0; i < 11; i++ {
		msgs = append(msgs, MessageInput{ID: fmt.Sprint(i), AccountID: "a", From: fmt.Sprintf("news%d@shop%d.example", i, i), Subject: fmt.Sprintf("Angebot %d", i)})
	}
	d := SuggestQuickRule(msgs, "en")
	if len(d.FromValues) != 8 || d.MatchAll {
		t.Fatalf("expected 8 OR'ed senders, got %d matchAll=%v", len(d.FromValues), d.MatchAll)
	}
	if !strings.Contains(d.Name, "+ 7 more") {
		t.Fatalf("expected English multi-sender name, got %q", d.Name)
	}
	if de := SuggestQuickRule(msgs, "de"); !strings.Contains(de.Name, "+ 7 weitere") {
		t.Fatalf("expected German multi-sender name, got %q", de.Name)
	}
	marked := map[string]bool{}
	for _, m := range msgs {
		marked[m.ID] = true
	}
	rep := EvaluateQuickRulePreview(BuildQuickRuleConditions(d.FromValues, nil, false), false, marked, msgs, "trash")
	if rep.MarkedMatched != 8 || !hasWarning(rep, "not_all_marked") {
		t.Fatalf("expected 8/11 hits with not_all_marked, got %d/%d %+v", rep.MarkedMatched, rep.MarkedTotal, rep.Warnings)
	}
}

func TestSuggestQuickRuleUmlautsEncodedNamesAndPrefixes(t *testing.T) {
	d := SuggestQuickRule([]MessageInput{
		{ID: "1", AccountID: "a", From: "=?UTF-8?Q?J=C3=BCrgen_M=C3=BCller?= <j.mueller@verein.de>", Subject: "AW: Mitgliederversammlung – Einladung"},
		{ID: "2", AccountID: "a", From: "Jürgen Müller <J.Mueller@verein.de>", Subject: "Re: Fwd: Mitgliederversammlung „Einladung“ 2026"},
	}, "de")
	if len(d.FromValues) != 1 || d.FromValues[0] != "j.mueller@verein.de" {
		t.Fatalf("expected one normalized address, got %+v", d.FromValues)
	}
	if !d.MatchAll || len(d.SubjectValues) != 1 || d.SubjectValues[0] != "Mitgliederversammlung Einladung" && d.SubjectValues[0] != "Mitgliederversammlung" {
		t.Fatalf("unexpected subject pattern %+v", d.SubjectValues)
	}
	for _, v := range d.SubjectValues {
		if strings.ContainsAny(v, "„“–") || strings.HasPrefix(strings.ToLower(v), "re") || strings.HasPrefix(strings.ToLower(v), "aw") {
			t.Fatalf("subject pattern kept punctuation/prefix: %q", v)
		}
	}
}

func TestExtractCommonSubjectPhraseRules(t *testing.T) {
	cases := []struct {
		name     string
		subjects []string
		want     string
	}{
		{"word order differs -> single token", []string{"Weekly Report Digest", "Report Weekly: Digest"}, "Weekly"},
		{"phrase in all subjects", []string{"[Daily Digest] Build OK #412", "[Daily Digest] Sync OK #411", "Re: [Daily Digest] Backup #410"}, "[Daily Digest]"},
		{"digits and short tokens ignored", []string{"Rechnung 2026-11 Nr 4711", "Ihre Rechnung 2026-12 Nr 4712"}, "Rechnung"},
		{"non-ascii punctuation is not a letter", []string{"Angebot – „neu“", "Angebot – alt"}, "Angebot"},
		{"stop words never match", []string{"Re: Hello from the team", "Fwd: Hello from the board"}, ""},
		{"three subjects, joined phrase must be in all", []string{"Server Alert CPU", "Server Alert Disk", "Alert: Server down"}, "Server"},
	}
	for _, tc := range cases {
		got := extractCommonSubjectPhrase(tc.subjects)
		if tc.name == "phrase in all subjects" {
			// "[Daily" / "Digest]" are separate tokens; the joined phrase must be in all three.
			if !strings.Contains(strings.ToLower(tc.subjects[2]), strings.ToLower(got)) || got == "" {
				t.Errorf("%s: got %q", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestQuickRulePreviewWarningsAndNoHits(t *testing.T) {
	marked := map[string]bool{"a": true}
	cands := []MessageInput{{ID: "a", From: "deals@shop1.de", Subject: "Sale"}}

	// No condition hits the marked mail.
	rep := EvaluateQuickRulePreview(BuildQuickRuleConditions([]string{"other@shop2.de"}, nil, false), false, marked, cands, "trash")
	if len(rep.MarkedHitIDs) != 0 || !hasWarning(rep, "no_marked_hits") {
		t.Fatalf("expected no_marked_hits, got %+v", rep)
	}

	// Short subject token and unspecific sender part are flagged.
	rep = EvaluateQuickRulePreview(BuildQuickRuleConditions([]string{"shop"}, []string{"Sa"}, false), false, marked, cands, "move")
	if !hasWarning(rep, "short_value") || !hasWarning(rep, "short_subject") || !rep.RequiresConfirm {
		t.Fatalf("expected short_value + short_subject + confirm for move, got %+v", rep.Warnings)
	}

	// Domain + subject with AND is specific: no bare_domain warning.
	rep = EvaluateQuickRulePreview(BuildQuickRuleConditions([]string{"@shop1.de"}, []string{"Sale"}, true), true, marked, cands, "trash")
	if hasWarning(rep, "bare_domain") {
		t.Fatalf("did not expect bare_domain for domain AND subject, got %+v", rep.Warnings)
	}
}
