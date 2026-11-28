package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

func TestAnalyzeTranslateAndQuickRepliesModes(t *testing.T) {
	srv, rec := fakeAnthropic(t, "ant-test", `{"answer":"Hallo Team,\n\nder Start erfolgt am Montag."}`)
	defer rec.check(t)

	t.Setenv("ANTHROPIC_API_KEY", "ant-test")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{
		Backend:  "claude-api",
		Model:    "claude-sonnet-4-5",
		Endpoint: srv.URL,
	}

	resp, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "translate",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:       "m-1",
			Subject:  "Launch date",
			From:     "alex@example.com",
			BodyText: "Hi team,\n\nWe launch on Monday.",
		},
	})
	if err != nil {
		t.Fatalf("Analyze translate failed: %v", err)
	}
	if resp.Mode != "translate" || !strings.Contains(resp.Answer, "Montag") || len(resp.Proposals) != 0 {
		t.Fatalf("unexpected translate response: %+v", resp)
	}

	// Quick-replies structured JSON + fallback bullet extraction
	srv2, rec2 := fakeAnthropic(t, "ant-test", `{"answer":"3 short replies","quickReplies":["Passt, Montag 10 Uhr!","Können wir auf Dienstag verschieben?","Schickst du mir die Agenda vorab?","Extrawurst"]}`)
	defer rec2.check(t)
	cfg.Agent.Endpoint = srv2.URL

	qr, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "quick_replies",
		Language: "de",
		PrimaryMessage: triage.MessageInput{
			ID:       "m-2",
			Subject:  "Meeting Montag",
			From:     "alice@example.com",
			BodyText: "Wollen wir uns Montag um 10 Uhr abstimmen?",
		},
	})
	if err != nil {
		t.Fatalf("Analyze quick_replies failed: %v", err)
	}
	if len(qr.QuickReplies) != 3 || qr.QuickReplies[0] != "Passt, Montag 10 Uhr!" {
		t.Fatalf("expected 3 cleaned quick replies, got %+v", qr.QuickReplies)
	}

	srv3, rec3 := fakeAnthropic(t, "ant-test", `{"answer":"- Sounds good!\n- Can we do Tuesday instead?\n- Thanks, I will check."}`)
	defer rec3.check(t)
	cfg.Agent.Endpoint = srv3.URL

	qrFallback, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "quick_replies",
		Language: "en",
		PrimaryMessage: triage.MessageInput{
			ID:       "m-3",
			Subject:  "Call?",
			From:     "bob@example.com",
			BodyText: "Free for a quick call?",
		},
	})
	if err != nil {
		t.Fatalf("Analyze quick_replies fallback failed: %v", err)
	}
	if len(qrFallback.QuickReplies) != 3 || qrFallback.QuickReplies[1] != "Can we do Tuesday instead?" {
		t.Fatalf("unexpected fallback quick replies: %+v", qrFallback.QuickReplies)
	}

	srv4, rec4 := fakeAnthropic(t, "ant-test", "- First bullet reply\n- Second bullet reply\n- Third bullet reply")
	defer rec4.check(t)
	cfg.Agent.Endpoint = srv4.URL

	qrRaw, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Mode:     "quick_replies",
		Language: "en",
		PrimaryMessage: triage.MessageInput{
			ID:       "m-4",
			Subject:  "Sync?",
			From:     "bob@example.com",
			BodyText: "When can we sync?",
		},
	})
	if err != nil {
		t.Fatalf("Analyze quick_replies non-JSON fallback failed: %v", err)
	}
	if len(qrRaw.QuickReplies) != 3 || qrRaw.QuickReplies[0] != "First bullet reply" || qrRaw.QuickReplies[2] != "Third bullet reply" {
		t.Fatalf("unexpected non-JSON bullet fallback quick replies: %+v", qrRaw.QuickReplies)
	}
}
