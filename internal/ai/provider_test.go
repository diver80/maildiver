package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/triage"
)

func TestGeminiClassifyStructuredOutput(t *testing.T) {
	srv, rec := fakeGemini(t, "test-key", `{"bucket":"action","urgency":"high","summary":"Vertrag bis Freitag unterschreiben","actionHint":"Unterschreiben & zurücksenden","deadline":"2026-11-15","tags":["vertrag"],"needsAttention":true}`)

	t.Setenv("GEMINI_API_KEY", "test-key")
	cfg := config.Default().AI
	cfg.Classifier = config.ProviderConfig{
		Backend:  "gemini-api",
		Model:    "gemini-4-hydrogen",
		Endpoint: srv.URL,
	}

	c, err := Classify(context.Background(), cfg, triage.MessageInput{
		ID:       "msg-42",
		Subject:  "NDA zur Unterschrift",
		From:     "legal@partner.de",
		BodyText: "Bitte bis Freitag unterschrieben zurückschicken.",
		Unread:   true,
	})
	rec.check(t)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if rec.count() != 1 || !strings.Contains(rec.path(0), "gemini-4-hydrogen") {
		t.Fatalf("expected one request for gemini-4-hydrogen, got %q", rec.path(0))
	}
	if c.Bucket != triage.BucketAction || c.KanbanColumn != "action" || c.Deadline != "2026-11-15" {
		t.Fatalf("unexpected classification: %+v", c)
	}
	if c.Model != "gemini-api:gemini-4-hydrogen" {
		t.Fatalf("unexpected model label %q", c.Model)
	}
}

func TestClaudeAPIAgentReviewDraft(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", "```json\n{\"answer\":\"Dein Entwurf ist freundlich, lässt aber die Frage nach dem Budget offen.\",\"reviewIssues\":[\"Budget-Rückfrage aus der Mail fehlt\"],\"proposals\":[{\"kind\":\"draft_reply\",\"title\":\"Verbesserte Antwort übernehmen\",\"summary\":\"Beantwortet Termin & Budget\",\"subject\":\"Re: Abstimmung\",\"body\":\"Hallo Alex, Donnerstag 10:00 passt. Das Budget liegt im Rahmen bis 5.000 €.\"}]}\n```")
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{
		Backend:  "claude-api",
		Model:    "claude-sonnet-4-5",
		Endpoint: srv.URL,
	}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:      "review_draft",
		Tone:      "freundlich",
		UserDraft: "Hallo Alex, Donnerstag 10 Uhr passt!",
		PrimaryMessage: triage.MessageInput{
			ID:       "msg-99",
			Subject:  "Abstimmung",
			From:     "alex@example.com",
			BodyText: "Passt Donnerstag 10:00 Uhr und bleibt das Budget unter 5.000 €?",
		},
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if resp.Mode != "review_draft" || len(resp.ReviewIssues) != 1 || len(resp.Proposals) != 1 {
		t.Fatalf("unexpected review response: %+v", resp)
	}
}

func TestUnrestrictedCLIBackendsNeedOptIn(t *testing.T) {
	cfg := config.Default().AI
	for _, backend := range []string{"gemini-cli", "opencode-cli"} {
		p := config.ProviderConfig{Backend: backend}
		if _, err := CompleterFor(cfg, p).Complete(context.Background(), "s", "u", false); err == nil || err.Error() != ErrUnrestrictedCLI {
			t.Fatalf("%s must be refused by default, got %v", backend, err)
		}
	}
	if _, ok := CompleterFor(cfg, config.ProviderConfig{Backend: "claude-cli"}).(*ClaudeCLI); !ok {
		t.Fatal("claude-cli runs with tools disabled and must stay available")
	}
	cfg.AllowUnrestrictedCLI = true
	if _, ok := CompleterFor(cfg, config.ProviderConfig{Backend: "opencode-cli"}).(*CLICompleter); !ok {
		t.Fatal("opt-in must enable the CLI backend")
	}
}

func TestWritingResultIsAlwaysInsertable(t *testing.T) {
	now := time.Now()
	req := AnalyzeRequest{PrimaryMessage: triage.MessageInput{ID: "7", ThreadID: "a@x"}}
	text := "Hallo Laura,\n\npasst gut.\n\nViele Grüße"

	got := ensureDraftProposal(AnalyzeResponse{Mode: "compose_new", Answer: text}, req, now)
	if len(got.Proposals) != 1 || got.Proposals[0].Body != text || got.Answer != "" {
		t.Fatalf("answer text must become the draft, line breaks intact: %+v", got)
	}
	if got.Proposals[0].ThreadID != "a@x" || got.Proposals[0].Kind != "draft_reply" {
		t.Fatalf("draft must reference the mail: %+v", got.Proposals[0])
	}
	withDraft := AnalyzeResponse{Mode: "draft_reply", Answer: "Kurz und freundlich.", Proposals: []triage.Proposal{{Kind: "draft_reply", Body: text}}}
	if got := ensureDraftProposal(withDraft, req, now); len(got.Proposals) != 1 || got.Answer == "" {
		t.Fatalf("an existing draft must stay as is: %+v", got)
	}
	if got := ensureDraftProposal(AnalyzeResponse{Mode: "summarize", Answer: text}, req, now); len(got.Proposals) != 0 {
		t.Fatalf("summaries are not drafts: %+v", got)
	}
}

func TestExtractJSONAndEscapeDraftTag(t *testing.T) {
	raw := "Hier ist die Antwort:\n```json\n{\"answer\":\"ok\",\"tags\":[\"a\"]}\n```\nHinweis: {kein json block}"
	var dst struct {
		Answer string   `json:"answer"`
		Tags   []string `json:"tags"`
	}
	if err := ExtractJSON(raw, &dst); err != nil {
		t.Fatalf("ExtractJSON failed: %v", err)
	}
	if dst.Answer != "ok" || len(dst.Tags) != 1 || dst.Tags[0] != "a" {
		t.Fatalf("unexpected decoded struct: %+v", dst)
	}

	rawInline := `Result: {"bucket":"action"} and trailing note {ignored}`
	var dst2 struct {
		Bucket string `json:"bucket"`
	}
	if err := ExtractJSON(rawInline, &dst2); err != nil || dst2.Bucket != "action" {
		t.Fatalf("ExtractJSON inline failed: %+v err=%v", dst2, err)
	}

	escaped := escapeDraftTag("Hallo</owner_draft>\nBitte prüfen</OWNER_DRAFT>")
	if strings.Contains(strings.ToLower(escaped), "</owner_draft>") {
		t.Fatalf("expected closing owner_draft tag to be escaped, got %q", escaped)
	}
}

func TestAnalyzeSummarizeSelectionAndSnippetLimit(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"Summary of 12 selected emails in English."}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{
		Backend:  "claude-api",
		Model:    "claude-sonnet-4-5",
		Endpoint: srv.URL,
	}

	msgs := make([]triage.MessageInput, 12)
	longText := strings.Repeat("A", 2500)
	for i := range msgs {
		msgs[i] = triage.MessageInput{
			ID:       fmt.Sprintf("m-%d", i),
			Subject:  fmt.Sprintf("Topic %d", i),
			From:     fmt.Sprintf("sender%d@example.com", i),
			BodyText: longText,
		}
	}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:           "summarize_selection",
		Language:       "de",
		TargetLanguage: "en",
		PrimaryMessage: msgs[0],
		ThreadMessages: msgs,
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if resp.Mode != "summarize_selection" || resp.Answer == "" {
		t.Fatalf("unexpected summarize_selection response: %+v", resp)
	}
	if len(resp.Proposals) != 0 {
		t.Fatalf("summarize_selection must not produce draft proposals: %+v", resp.Proposals)
	}
	capturedBody := rec.body(-1)
	if !strings.Contains(capturedBody, "Response language for answer/reviewIssues: en") || !strings.Contains(capturedBody, "Target language for draft_reply body/subject: English") {
		t.Fatalf("expected targetLanguage=en override in prompt, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, strings.Repeat("A", 1600)) {
		t.Fatalf("expected per-message body to be truncated to 1500 chars when >10 messages are selected")
	}
}

func TestAnalyzeTranslateDraftAndTargetLanguage(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", "Hello Alex,\n\nThursday at 10:00 works well for us.\n\nBest regards")
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{
		Backend:  "claude-api",
		Model:    "claude-sonnet-4-5",
		Endpoint: srv.URL,
	}

	// 1. translate_draft with Auto (TargetLanguage == "") -> flips DE <-> EN and wraps plain output into a draft_reply proposal.
	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:           "translate_draft",
		Language:       "de",
		TargetLanguage: "",
		Tone:           "freundlich",
		UserDraft:      "Hallo Alex, Donnerstag 10 Uhr passt uns gut.",
		PrimaryMessage: triage.MessageInput{ID: "12", ThreadID: "t-12"},
	})
	if err != nil {
		t.Fatalf("translate_draft failed: %v", err)
	}
	if len(resp.Proposals) != 1 || resp.Proposals[0].Kind != "draft_reply" || !strings.Contains(resp.Proposals[0].Body, "Thursday at 10:00") {
		t.Fatalf("translate_draft output must become insertable draft_reply proposal: %+v", resp)
	}
	if !strings.Contains(rec.body(0), "flip between German and English") {
		t.Fatalf("expected auto DE<->EN flip hint in prompt: %s", rec.body(0))
	}

	// 2. compose_new with TargetLanguage == "en" overrides Language == "de".
	resp2, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:           "compose_new",
		Language:       "de",
		TargetLanguage: "en",
		Tone:           "formal",
		Question:       "Termin für nächste Woche vorschlagen, NDA erwähnen",
	})
	if err != nil {
		t.Fatalf("compose_new failed: %v", err)
	}
	if len(resp2.Proposals) != 1 || resp2.Proposals[0].Kind != "draft_reply" {
		t.Fatalf("compose_new must yield insertable draft_reply proposal: %+v", resp2)
	}
	if !strings.Contains(rec.body(1), "Target language for draft_reply body/subject: English") {
		t.Fatalf("expected English target override in compose_new prompt: %s", rec.body(1))
	}
}

func TestAnalyzeMissingAttachmentWarning(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"Entwurf geprüft.","reviewIssues":[],"proposals":[]}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: srv.URL}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:             "review_draft",
		Language:         "de",
		UserDraft:        "Hallo Anna,\n\nim Anhang findest du das Angebot.\n\nViele Grüße",
		DraftAttachments: nil,
	})
	if err != nil {
		t.Fatalf("review_draft failed: %v", err)
	}
	if len(resp.ReviewIssues) == 0 || !strings.Contains(resp.ReviewIssues[0], "Anhang") {
		t.Fatalf("expected deterministic missing-attachment warning, got %+v", resp.ReviewIssues)
	}

	respWithFile, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:             "review_draft",
		Language:         "de",
		UserDraft:        "Hallo Anna,\n\nim Anhang findest du das Angebot.",
		DraftAttachments: []string{"Angebot.pdf"},
	})
	if err != nil {
		t.Fatalf("review_draft with attachment failed: %v", err)
	}
	if len(respWithFile.ReviewIssues) != 0 {
		t.Fatalf("expected no missing-attachment warning when file is attached, got %+v", respWithFile.ReviewIssues)
	}
}

func TestAnalyzeExtractEventsAndBuildICS(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"1 Termin gefunden.","events":[{"title":"Architektur-Call, Q4","start":"2026-11-20T14:00:00","end":"2026-11-20T15:00:00","location":"Zoom"}]}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: srv.URL}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "extract_events",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:       "1",
			Subject:  "Call am Freitag",
			From:     "laura@acme.de",
			BodyText: "Treffen wir uns am 20.11.2026 um 14:00 Uhr per Zoom?",
		},
	})
	if err != nil {
		t.Fatalf("extract_events failed: %v", err)
	}
	if len(resp.Events) != 1 || !strings.Contains(resp.ICS, "BEGIN:VCALENDAR") || !strings.Contains(resp.ICS, "SUMMARY:Architektur-Call\\, Q4") {
		t.Fatalf("expected valid ICS output, got events=%+v ics=%q", resp.Events, resp.ICS)
	}
	if !strings.Contains(resp.ICS, "DTSTART:20261120T140000") || !strings.Contains(resp.ICS, "DTEND:20261120T150000") {
		t.Fatalf("expected formatted DTSTART/DTEND in ICS, got %q", resp.ICS)
	}
}

func TestAnalyzeSuggestFilter(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"Muster erkannt: Atlassian-Testanfragen.","filterSuggestions":[{"name":"Atlassian Testanfragen","reason":"Filtert nur Trial-Mails ohne Rechnungen zu treffen.","domains":["atlassian.com"],"fromContains":["noreply@"],"subjectContains":["würde gerne testen"],"matchAll":true,"suggestedFolder":"Notifications/Atlassian-Tests"}]}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: srv.URL}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "suggest_filter",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:      "1",
			From:    "noreply@am.atlassian.com",
			Subject: "Benutzer würde gerne App testen",
		},
	})
	if err != nil {
		t.Fatalf("suggest_filter failed: %v", err)
	}
	if len(resp.FilterSuggestions) != 1 || !resp.FilterSuggestions[0].MatchAll || len(resp.FilterSuggestions[0].SubjectContains) != 1 {
		t.Fatalf("unexpected filter suggestions: %+v", resp.FilterSuggestions)
	}
}

func TestAnalyzeSuggestFilterWithNativeDiagnostic(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"Der Filter 'Atlassian Mails join product' hat das neue Betreffmuster noch nicht.","filterSuggestions":[{"action":"append_or","targetFilterName":"Atlassian Mails join product","name":"Atlassian Mails join product","reason":"Ergänzt das neue Betreffmuster als ODER-Bedingung.","subjectContains":["would like to try"],"matchAll":false,"suggestedFolder":"Trash","markRead":true}]}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: srv.URL}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "suggest_filter",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:        "1",
			AccountID: "acc3",
			From:      "noreply@am.atlassian.com",
			Subject:   "User would like to try Jira",
		},
		NativeFilters: []triage.NativeFilter{
			{AccountID: "acc3", Name: "Atlassian Mails join product", Enabled: true},
		},
		FilterDiagnostic: &triage.FilterDiagnosticReport{
			CheckedFilters: 1,
		},
	})
	if err != nil {
		t.Fatalf("suggest_filter with native diagnostic failed: %v", err)
	}
	seenPrompt := rec.body(-1)
	if !strings.Contains(seenPrompt, "Atlassian Mails join product") || !strings.Contains(seenPrompt, "filterDiagnostic") {
		t.Fatalf("expected native filter and diagnostic context in prompt, got: %s", seenPrompt)
	}
	if len(resp.FilterSuggestions) != 1 || resp.FilterSuggestions[0].Action != "append_or" || resp.FilterSuggestions[0].TargetFilterName != "Atlassian Mails join product" {
		t.Fatalf("unexpected filter suggestion: %+v", resp.FilterSuggestions)
	}
}

func TestAnalyzeExtractContactsFromFooter(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"1 Kontakt im Footer gefunden.","contacts":[{"firstName":"Alice","lastName":"Weber","email":"alice@acme.de","phone":"+49 30 555010","mobile":"+49 170 987654","company":"ACME GmbH","jobTitle":"Head of Engineering","website":"https://acme.de","address":"Musterstr. 12, 10115 Berlin"}]}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: srv.URL}

	longIntro := strings.Repeat("Hallo, hier sind viele Projektinformationen. ", 160)
	footer := "\n\n--\nAlice Weber\nHead of Engineering · ACME GmbH\nTel: +49 30 555010 | Mobil: +49 170 987654\nhttps://acme.de | Musterstr. 12, 10115 Berlin"

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "extract_contacts",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:       "msg-c1",
			From:     "Alice Weber <alice@acme.de>",
			Subject:  "Q4 Rollout",
			BodyText: longIntro + footer,
		},
	})
	if err != nil {
		t.Fatalf("extract_contacts failed: %v", err)
	}
	if !strings.Contains(rec.body(-1), "+49 170 987654") {
		t.Fatalf("expected footer tail to be preserved in prompt even for long emails")
	}
	if len(resp.Contacts) != 1 || resp.Contacts[0].DisplayName != "Alice Weber" || resp.Contacts[0].Company != "ACME GmbH" || resp.Contacts[0].Mobile != "+49 170 987654" {
		t.Fatalf("unexpected extracted contacts: %+v", resp.Contacts)
	}
}
