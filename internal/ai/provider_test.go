package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fhess/omathunder/internal/config"
	"github.com/fhess/omathunder/internal/triage"
)

func TestGeminiClassifyStructuredOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Fatalf("missing x-goog-api-key")
		}
		if !strings.Contains(r.URL.Path, "gemini-4-hydrogen") {
			t.Fatalf("expected gemini-4-hydrogen in path, got %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]string{
							{"text": `{"bucket":"action","urgency":"high","summary":"Vertrag bis Freitag unterschreiben","actionHint":"Unterschreiben & zurücksenden","deadline":"2026-11-15","tags":["vertrag"],"needsAttention":true}`},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	cfg := config.Default().AI
	cfg.Classifier = config.ProviderConfig{
		Backend:  "gemini-api",
		Model:    "gemini-4-hydrogen",
		Endpoint: srv.URL,
	}

	rules := []config.RoutingRule{{
		ID: "r-partner", Customer: "Partner AG", FolderPath: "Kunden/Partner", Domains: []string{"partner.de"},
	}}

	c, err := Classify(context.Background(), cfg, triage.MessageInput{
		ID:       "msg-42",
		Subject:  "NDA zur Unterschrift",
		From:     "legal@partner.de",
		BodyText: "Bitte bis Freitag unterschrieben zurückschicken.",
		Unread:   true,
	}, rules)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if c.Bucket != triage.BucketAction || c.KanbanColumn != "action" || c.Customer != "Partner AG" || c.Deadline != "2026-11-15" {
		t.Fatalf("unexpected classification: %+v", c)
	}
}

func TestClaudeAPIAgentReviewDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ant-test" {
			t.Fatalf("missing x-api-key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{
					"type": "text",
					"text": "```json\n{\"answer\":\"Dein Entwurf ist freundlich, lässt aber die Frage nach dem Budget offen.\",\"reviewIssues\":[\"Budget-Rückfrage aus der Mail fehlt\"],\"proposals\":[{\"kind\":\"draft_reply\",\"title\":\"Verbesserte Antwort übernehmen\",\"summary\":\"Beantwortet Termin & Budget\",\"subject\":\"Re: Abstimmung\",\"body\":\"Hallo Alex, Donnerstag 10:00 passt. Das Budget liegt im Rahmen bis 5.000 €.\"}]}\n```",
				},
			},
		})
	}))
	defer srv.Close()

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
