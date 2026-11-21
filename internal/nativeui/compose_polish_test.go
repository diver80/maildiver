package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/triage"
)

func TestDetectMissingAttachmentKeyword(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"de_anhang", "Hallo,\nanbei die gewünschte Datei.\nViele Grüße", "anbei"},
		{"de_angehaengt", "Ich habe das PDF angehängt.", "angehängt"},
		{"en_attached", "Hi Alex,\nPlease find the invoice attached.", "attached"},
		{"ignore_quote", "Alles klar, danke!\n\nAm 20.11.2026 schrieb Bob:\n> Please see attached PDF.", ""},
		{"ignore_sig", "Bis morgen!\n\n-- \nMax Muster\nAnhang: Disclaimer", ""},
		{"no_match", "Hallo, wir telefonieren morgen um 10 Uhr.", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectMissingAttachmentKeyword(extractComposeUserTextOnly(tc.body))
			if !strings.EqualFold(got, tc.want) {
				t.Fatalf("got %q, want %q for body %q", got, tc.want, tc.body)
			}
		})
	}
}

func TestSignaturePositionAndCustomHTML(t *testing.T) {
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{
		{
			ID:                "acc-1",
			Email:             "alex@example.com",
			Signature:         "Alex Example\nExample GmbH",
			SignatureHTML:     `<p><b>Alex</b> Example</p><script>alert(1)</script>`,
			SignaturePosition: "below",
		},
	}
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return cfg, nil }})
	plain, customHTML, below := app.composeSignatureParts("acc-1", "alex@example.com")
	if !below || !strings.Contains(plain, "Alex Example") || !strings.Contains(customHTML, "<b>Alex</b>") {
		t.Fatalf("unexpected signature parts: plain=%q html=%q below=%v", plain, customHTML, below)
	}
	sigBlock := composeSignatureBlockHTMLFromParts(plain, customHTML)
	if strings.Contains(sigBlock, "<script>") || !strings.Contains(sigBlock, `data-mdv-sig="1"`) || !strings.Contains(sigBlock, "<b>Alex</b>") {
		t.Fatalf("unexpected sanitized signature block: %s", sigBlock)
	}

	plainComposed := appendAccountSignatureWithQuote("Danke!", plain, "\n\nAm Montag schrieb Bob:\n> Hi", true)
	sigIdx := strings.Index(plainComposed, "-- \nAlex Example")
	quoteIdx := strings.Index(plainComposed, "> Hi")
	if sigIdx < 0 || quoteIdx < 0 || sigIdx < quoteIdx {
		t.Fatalf("expected signature below quote, got:\n%s", plainComposed)
	}

	htmlComposed := buildComposeEditorInitialHTMLWithSig("Danke!", sigBlock, true, "", "", nil, "Am Montag schrieb Bob:\n> Hi")
	hSigIdx := strings.Index(htmlComposed, `data-mdv-sig="1"`)
	hQuoteIdx := strings.Index(htmlComposed, `data-mdv-quote="1"`)
	if hSigIdx < 0 || hQuoteIdx < 0 || hSigIdx < hQuoteIdx {
		t.Fatalf("expected HTML signature after blockquote when belowQuote=true, got:\n%s", htmlComposed)
	}
}

func TestUndoSendRestoreAndCommit(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	delay := 5
	cfg.App.UndoSendSeconds = &delay

	rpc := &composeCaptureRPC{}
	bc := backend.New(backend.Options{
		Engine:     rpc,
		Events:     events.NewHub(),
		Store:      triage.NewStoreAt(t.TempDir()),
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Version:    "test",
	})
	uiQueue := make(chan func(), 32)
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
		Backend:      bc,
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.SetHooks(func(fn func()) { uiQueue <- fn }, nil, nil, nil, nil)
	app.OpenComposeNew()
	app.composeTo = "bob@example.com"
	app.composeSubject = "Projekt-Update"
	app.composeBody = "Hallo Bob,\nhier das Update."

	app.requestComposeSend(context.Background(), app.buildComposeReq(false))
	if len(app.pendingSends) != 1 || app.composeOpen {
		t.Fatalf("expected compose closed and send queued in undo window (pending=%d open=%v)", len(app.pendingSends), app.composeOpen)
	}
	rpc.mu.Lock()
	if len(rpc.sent) != 0 {
		rpc.mu.Unlock()
		t.Fatalf("expected zero SMTP sends during undo window, got %d", len(rpc.sent))
	}
	rpc.mu.Unlock()

	app.cancelPendingSend()
	if len(app.pendingSends) != 0 || !app.composeOpen || app.composeSubject != "Projekt-Update" || app.composeTo != "bob@example.com" {
		t.Fatalf("expected cancelled send to restore compose state, open=%v subj=%q to=%q", app.composeOpen, app.composeSubject, app.composeTo)
	}
	if app.composeBaseline == app.composeSnapshot() {
		t.Fatalf("expected restored compose baseline to keep dirty/unsaved guard active")
	}
	rpc.mu.Lock()
	if len(rpc.sent) != 0 {
		rpc.mu.Unlock()
		t.Fatalf("cancelled send must not transmit over SMTP")
	}
	rpc.mu.Unlock()

	// Queue TWO emails back-to-back and verify neither overwrites the other.
	app.composeMissingAttAck = true
	app.requestComposeSend(context.Background(), app.buildComposeReq(false))
	app.OpenComposeNew()
	app.composeTo = "carol@example.com"
	app.composeSubject = "Zweite Mail"
	app.composeBody = "Hallo Carol!"
	app.requestComposeSend(context.Background(), app.buildComposeReq(false))
	if len(app.pendingSends) != 2 {
		t.Fatalf("expected 2 concurrent pending sends in undo queue, got %d", len(app.pendingSends))
	}
	firstNonce := app.pendingSends[0].Nonce
	secondNonce := app.pendingSends[1].Nonce
	app.commitPendingSend(context.Background(), firstNonce)
	app.commitPendingSend(context.Background(), secondNonce)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case fn := <-uiQueue:
			fn()
		default:
			rpc.mu.Lock()
			n := len(rpc.sent)
			rpc.mu.Unlock()
			if n >= 2 {
				goto done
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
done:
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	if len(rpc.sent) != 2 {
		t.Fatalf("expected both committed pending sends to invoke compose.send, got %+v", rpc.sent)
	}
}

func TestExtractHTMLBodyInnerAndTruncateUTF8(t *testing.T) {
	full := `<!doctype html><html><head><meta charset="utf-8"></head><body style="color:#111;"><p>Hallo Welt</p></body></html>`
	got := extractHTMLBodyInner(full)
	if got != `<p>Hallo Welt</p>` {
		t.Fatalf("extractHTMLBodyInner got %q", got)
	}
	raw := []byte("äöü€€")
	cut, truncated := truncateUTF8Bytes(raw, 7) // lands mid-'€'
	if !truncated || string(cut) != "äöü" {
		t.Fatalf("truncateUTF8Bytes mid-rune failed: truncated=%v got=%q", truncated, string(cut))
	}
}

func TestComposeTemplatesSaveInsertAndDelete(t *testing.T) {
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	if !app.saveNewComposeTemplate("Eingangsbestätigung", "Rückmeldung zu Ihrer Anfrage", "Vielen Dank für Ihre Nachricht.") {
		t.Fatalf("expected saveNewComposeTemplate to succeed")
	}
	if len(app.cfg.App.ComposeTemplates) != 1 {
		t.Fatalf("expected 1 saved template, got %d", len(app.cfg.App.ComposeTemplates))
	}
	tpl := app.cfg.App.ComposeTemplates[0]

	app.OpenComposeNew()
	app.insertComposeTemplate(tpl)
	if app.composeSubject != "Rückmeldung zu Ihrer Anfrage" || !strings.Contains(app.composeBody, "Vielen Dank für Ihre Nachricht.") {
		t.Fatalf("expected inserted template in compose, subj=%q body=%q", app.composeSubject, app.composeBody)
	}

	app.deleteComposeTemplate(tpl.ID)
	if len(app.cfg.App.ComposeTemplates) != 0 {
		t.Fatalf("expected template deleted, got %+v", app.cfg.App.ComposeTemplates)
	}
}
