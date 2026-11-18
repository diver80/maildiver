package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/omathunder/internal/bridgeclient"
)

// The reading pane decrypts the mail it opens and says so; a mail it cannot decrypt shows why and
// offers Thunderbird; a signature is named but never presented as checked.
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
	for _, want := range []string{"Geheimer Text", "KI-Aktionen auf diese Mail senden den entschlüsselten Text", "Ob die Signatur gültig ist, prüft OmaThunder nicht", "In Thunderbird prüfen"} {
		if !strings.Contains(texts, want) {
			t.Errorf("reader lacks %q; texts:\n%s", want, texts)
		}
	}

	// Not decrypted: no "(Kein Textinhalt)", a reason and the way to Thunderbird.
	app.selectedMsg = &bridgeclient.MessageDetail{MessageHeader: app.messages[0], Security: bridgeclient.MessageSecurity{Encrypted: true, Decryption: "fail"}}
	app.readerBody = readerBody{} // the converted body is cached per mail ID
	tt.Frame()
	texts = strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"(Inhalt verschlüsselt)", "konnte die Mail nicht entschlüsseln", "In Thunderbird öffnen"} {
		if !strings.Contains(texts, want) {
			t.Errorf("undecrypted mail lacks %q; texts:\n%s", want, texts)
		}
	}
	if strings.Contains(texts, "(Kein Textinhalt)") || strings.Contains(texts, "Signiert") {
		t.Errorf("wrong notes for an undecrypted, unsigned mail:\n%s", texts)
	}
	if err := tt.Click("In Thunderbird öffnen"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return st.rpc.lastParams("messages.open") != nil }) {
		t.Fatal("open in Thunderbird did not reach the add-on")
	}

	// A plain mail has no note.
	app.selectedMsg = &bridgeclient.MessageDetail{MessageHeader: app.messages[0], BodyText: "Hallo"}
	app.readerBody = readerBody{}
	tt.Frame()
	if tt.HasText("In Thunderbird öffnen") || tt.HasText("In Thunderbird prüfen") {
		t.Error("a plain mail must not show a security note")
	}
}

// The compose dialog sends the signature choice; a reply to an encrypted mail says it stays
// encrypted, and a silent draft of it reports the window Thunderbird opened instead.
func TestComposeSignAndEncryptedReply(t *testing.T) {
	app, loop, st := keymapApp(t)
	msg := &bridgeclient.MessageDetail{MessageHeader: app.messages[0], BodyText: "x", Security: bridgeclient.MessageSecurity{Encrypted: true, Decryption: "success"}}
	app.OpenComposeReply(msg, "Danke!")
	tt := ui.NewTester(app.RenderMain, 1380, 1000)
	tt.Frame()
	if !tt.HasText("Antwort auf eine verschlüsselte Mail: wird verschlüsselt gesendet. „Entwurf speichern“ öffnet sie in Thunderbird.") {
		t.Fatalf("encrypted reply note missing; texts: %v", tt.Texts())
	}
	if got := app.buildComposeReq(false).Sign; got != "" {
		t.Fatalf("default sign = %q, want the identity's default", got)
	}
	if err := tt.Click("Signieren"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.buildComposeReq(false).Sign; got != "on" {
		t.Fatalf("sign = %q after choosing Signieren", got)
	}
	if err := tt.Click("Entwurf speichern"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 1 }) {
		t.Fatal("draft not sent to the add-on")
	}
	if _, params := st.rpc.snapshot(); params[0]["sign"] != "on" || params[0]["replyToMessageId"] != "m1" {
		t.Errorf("compose.draft params: %v", params[0])
	}

	// A new mail starts from the identity's default again.
	app.OpenComposeNew()
	if app.composeSign != 0 || app.composeReplyEncrypted {
		t.Errorf("new compose kept sign=%d encrypted=%v", app.composeSign, app.composeReplyEncrypted)
	}
	tt.Frame()
	if tt.HasText("Antwort auf eine verschlüsselte Mail: wird verschlüsselt gesendet. „Entwurf speichern“ öffnet sie in Thunderbird.") {
		t.Error("a new mail must not show the encrypted reply note")
	}
	if got := app.composeSecuritySuffix(&bridgeclient.ComposeSecurity{Technology: "OpenPGP", Signed: true, Encrypted: true}); got != " (signiert, verschlüsselt)" {
		t.Errorf("security suffix = %q", got)
	}
	if got := app.composeSecuritySuffix(&bridgeclient.ComposeSecurity{}); got != "" {
		t.Errorf("empty security suffix = %q", got)
	}
}
