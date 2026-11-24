package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// The reading pane notes encrypted/signed mail clearly without Thunderbird dependencies.
func TestReaderSecurityNotes(t *testing.T) {
	app, loop, st := keymapApp(t)
	st.rpc.mu.Lock()
	st.rpc.getJSON = `{"id":"m1","messageId":"m1@x","accountId":"acc1","subject":"Eins","from":"Alice <alice@acme.de>","bodyText":"Geheimer Text","security":{"encrypted":true,"decryption":"success","signed":true}}`
	st.rpc.mu.Unlock()

	app.SelectMessage(st.ctx, "m1")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.selectedMsg != nil }) {
		t.Fatal("mail did not load")
	}
	if got := st.rpc.lastParams("messages.get"); got["decrypt"] != true {
		t.Fatalf("the reading pane must ask for decryption: %v", got)
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1000)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"Geheimer Text", "KI-Aktionen auf diese Mail senden den entschlüsselten Text", "Signiert (PGP/S/MIME)."} {
		if !strings.Contains(texts, want) {
			t.Errorf("reader lacks %q; texts:\n%s", want, texts)
		}
	}

	// Verified S/MIME signature displays signer & issuer clearly.
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Signed Body",
		Security: backend.MessageSecurity{
			Signed:           true,
			SMIME:            true,
			SignatureValid:   true,
			SignatureTrusted: true,
			SignerEmail:      "alice@acme.de",
			SignerIssuer:     "Acme CA",
		},
	}
	app.readerBody = readerBody{}
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "S/MIME-Signatur gültig & vertrauenswürdig") {
		t.Fatalf("expected verified S/MIME badge in reader")
	}

	// Not decrypted: no "(Kein Textinhalt)", clear PostDive message.
	app.selectedMsg = &backend.MessageDetail{MessageHeader: app.messages[0], Security: backend.MessageSecurity{Encrypted: true, Decryption: "fail"}}
	app.readerBody = readerBody{} // the converted body is cached per mail ID
	tt.Frame()
	texts = strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"(Inhalt verschlüsselt)", "PostDive konnte den Inhalt nicht entschlüsseln"} {
		if !strings.Contains(texts, want) {
			t.Errorf("undecrypted mail lacks %q; texts:\n%s", want, texts)
		}
	}
	if strings.Contains(texts, "(Kein Textinhalt)") || strings.Contains(texts, "Signiert") {
		t.Errorf("wrong notes for an undecrypted, unsigned mail:\n%s", texts)
	}

	// A plain mail has no security note.
	app.selectedMsg = &backend.MessageDetail{MessageHeader: app.messages[0], BodyText: "Hallo"}
	app.readerBody = readerBody{}
	tt.Frame()
	if tt.HasText("PostDive konnte den Inhalt nicht entschlüsseln") || tt.HasText("Signiert (PGP/S/MIME)") {
		t.Error("a plain mail must not show a security note")
	}
}

// Replying to an encrypted email shows an explicit PostDive SMTP notice and saves drafts cleanly.
func TestComposeSignAndEncryptedReply(t *testing.T) {
	app, loop, st := keymapApp(t)
	msg := &backend.MessageDetail{MessageHeader: app.messages[0], BodyText: "x", Security: backend.MessageSecurity{Encrypted: true, Decryption: "success"}}
	app.OpenComposeReply(msg, "Danke!")
	tt := ui.NewTester(app.RenderMain, 1380, 1000)
	tt.Frame()
	if !tt.HasText("Hinweis: Die Original-Mail war verschlüsselt. Aktiviere unten „S/MIME verschlüsseln“, falls das Empfänger-Zertifikat vorhanden ist.") {
		t.Fatalf("encrypted reply note missing; texts: %v", tt.Texts())
	}
	if got := app.buildComposeReq(false).Sign; got != "" {
		t.Fatalf("default sign = %q, want empty", got)
	}
	if err := tt.Click("Entwurf speichern"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 1 }) {
		t.Fatal("draft not sent to backend")
	}
	if _, params := st.rpc.snapshot(); params[0]["replyToMessageId"] != "m1" {
		t.Errorf("compose.draft params: %v", params[0])
	}

	// A new mail clears the encrypted reply flag.
	app.OpenComposeNew()
	if app.composeSign != 0 || app.composeReplyEncrypted {
		t.Errorf("new compose kept sign=%d encrypted=%v", app.composeSign, app.composeReplyEncrypted)
	}
	tt.Frame()
	if tt.HasText("Hinweis: Die Original-Mail war verschlüsselt") {
		t.Error("a new mail must not show the encrypted reply note")
	}
	if got := app.composeSecuritySuffix(&backend.ComposeSecurity{Technology: "OpenPGP", Signed: true, Encrypted: true}); got != " (signiert, verschlüsselt)" {
		t.Errorf("security suffix = %q", got)
	}
	if got := app.composeSecuritySuffix(&backend.ComposeSecurity{}); got != "" {
		t.Errorf("empty security suffix = %q", got)
	}
}
