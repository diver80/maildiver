package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The reading pane shows the open mail's conversation with the newest mail on top: newer mails
// above the open one, older ones below, folded until clicked; summaries see the whole thread.
func TestConversationInReadingPane(t *testing.T) {
	app, loop, st := keymapApp(t)
	st.rpc.mu.Lock()
	st.rpc.answers = map[string]string{
		"messages.conversation": `{"threadId":"th:a@x","messages":[
			{"id":"m3","messageId":"c@x","subject":"Re: Re: Angebot","from":"Carol <carol@acme.de>","date":"2026-11-20T10:00:00Z","unread":true,"snippet":"Passt, ich bestelle."},
			{"id":"m1","messageId":"m1@x","subject":"Re: Angebot","from":"Alice <alice@acme.de>","date":"2026-11-19T10:00:00Z"},
			{"id":"m0","messageId":"a@x","subject":"Angebot","from":"Bob <bob@acme.de>","date":"2026-11-18T10:00:00Z","folder":"Sent","snippet":"Anbei das Angebot."}]}`,
		"messages.thread": `{"messages":[{"id":"m0","subject":"Angebot","bodyText":"Anbei"},{"id":"m1","subject":"Re: Angebot","bodyText":"Danke"},{"id":"m3","subject":"Re: Re: Angebot","bodyText":"Passt"}]}`,
	}
	st.rpc.getJSON = `{"id":"m1","messageId":"m1@x","accountId":"acc1","folder":"INBOX","subject":"Re: Angebot","from":"Alice <alice@acme.de>","bodyText":"Danke für das Angebot","read":true}`
	st.rpc.mu.Unlock()

	app.SelectMessage(st.ctx, "m1")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.conversationSize("m1") == 3 }) {
		t.Fatal("conversation not loaded")
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1200)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	newer := strings.Index(texts, "Neuere Mails in dieser Unterhaltung (1)")
	body := strings.Index(texts, "Danke für das Angebot")
	older := strings.Index(texts, "Frühere Mails in dieser Unterhaltung (1)")
	if newer < 0 || body < 0 || older < 0 || !(newer < body && body < older) {
		t.Fatalf("expected newer · open mail · older, got positions %d %d %d; texts:\n%s", newer, body, older, texts)
	}
	if !strings.Contains(texts, "Passt, ich bestelle.") || !strings.Contains(texts, "Anbei das Angebot.") {
		t.Fatalf("folded mails show their start; texts:\n%s", texts)
	}

	// Unfolding a mail loads and shows it.
	st.rpc.mu.Lock()
	st.rpc.getJSON = `{"id":"m0","messageId":"a@x","accountId":"acc1","folder":"Sent","subject":"Angebot","from":"Bob <bob@acme.de>","bodyText":"Anbei das vollständige Angebot über 12.000 EUR","read":true}`
	st.rpc.mu.Unlock()
	r, ok := tt.Find("Anbei das Angebot.")
	if !ok {
		t.Fatal("older mail row not found")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if !loop.waitUntil(t, 3*time.Second, func() bool { it := app.conversation.open["m0"]; return it != nil && it.detail != nil }) {
		t.Fatal("unfolded mail not loaded")
	}
	tt.Frame()
	if !tt.HasText("Anbei das vollständige Angebot über 12.000 EUR") {
		t.Fatalf("unfolded body missing; texts: %v", tt.Texts())
	}

	// A summary asks for the whole conversation.
	app.RunInspectorAI(context.Background(), "summarize")
	if !loop.waitUntil(t, 5*time.Second, func() bool { return st.rpc.lastParams("messages.thread") != nil }) {
		t.Fatal("summarize did not load the conversation")
	}
	if got := st.rpc.lastParams("messages.thread"); got["id"] != "m1" {
		t.Fatalf("thread params: %v", got)
	}

	// A mail on its own has no conversation section.
	st.rpc.mu.Lock()
	st.rpc.answers["messages.conversation"] = `{"threadId":"th:z","messages":[{"id":"m2","subject":"Allein"}]}`
	st.rpc.getJSON = `{"id":"m2","accountId":"acc1","subject":"Allein","bodyText":"Nur eine Mail"}`
	st.rpc.mu.Unlock()
	app.SelectMessage(st.ctx, "m2")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.conversationSize("m2") == 1 }) {
		t.Fatal("conversation not reloaded")
	}
	tt.Frame()
	if tt.HasText("Neuere Mails in dieser Unterhaltung (1)") || strings.Contains(strings.Join(tt.Texts(), "\n"), "in dieser Unterhaltung") {
		t.Fatal("a single mail must not show a conversation")
	}
}
