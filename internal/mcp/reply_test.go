package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
)

// replyEngine answers like the mail engine for two accounts and records what is asked of it.
type replyEngine struct {
	drafts []backend.ComposeRequest
	lists  []map[string]any
}

func (*replyEngine) Connected() bool { return true }

func (e *replyEngine) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "accounts.list":
		return json.RawMessage(`{"accounts":[
			{"id":"acc1","name":"Private","identities":[{"id":"i1","email":"me@home.example"}]},
			{"id":"acc2","name":"Work","identities":[{"id":"i2","email":"me@work.example"}]}]}`), nil
	case "messages.get":
		p, _ := params.(map[string]any)
		switch p["id"] {
		case "enc":
			return json.RawMessage(`{"id":"enc","accountId":"acc2","subject":"Geheim","from":"Alice <alice@acme.example>","to":["me@work.example"],"date":"2026-11-02T10:00:00Z","bodyText":"CIPHERTEXT","security":{"encrypted":true,"decryption":"skipped"}}`), nil
		case "sent":
			return json.RawMessage(`{"id":"sent","accountId":"acc2","subject":"Re: Plan","from":"me@work.example","to":["bob@acme.example"],"date":"2026-11-02T10:00:00Z","bodyText":"ok","security":{"encrypted":false}}`), nil
		}
		return json.RawMessage(`{"id":"m1","accountId":"acc2","subject":"Kickoff","from":"Alice <alice@acme.example>","to":["me@work.example","bob@acme.example"],"cc":["carol@acme.example"],"date":"2026-11-02T10:00:00Z","bodyText":"Line one\n\n> older","attachments":[{"name":"plan.pdf","partName":"2"}],"security":{"encrypted":false}}`), nil
	case "compose.draft":
		var req backend.ComposeRequest
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &req)
		e.drafts = append(e.drafts, req)
		return json.RawMessage(`{"ok":true}`), nil
	case "messages.list":
		raw, _ := json.Marshal(params)
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		e.lists = append(e.lists, m)
		return json.RawMessage(`{"messages":[]}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func replyServer(eng *replyEngine) *Server {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	load := func() (config.Config, error) { return cfg, nil }
	return New(Options{Backend: backend.New(backend.Options{Engine: eng, LoadConfig: load}), LoadConfig: load, AllowDrafts: true})
}

func draftCall(args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_draft","arguments":` + args + `}}`
}

func resultText(t *testing.T, r rpcResponse) (string, bool) {
	t.Helper()
	raw, _ := json.Marshal(r.Result)
	var res toolCallResult
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("no tool result: %s", raw)
	}
	return res.Content[0].Text, res.IsError
}

func TestReplyDraftIsFilledFromTheOriginal(t *testing.T) {
	eng := &replyEngine{}
	resps := runMCPExchange(t, replyServer(eng), draftCall(`{"replyToMessageId":"m1","body":"Passt."}`))
	if text, isErr := resultText(t, resps[0]); isErr {
		t.Fatalf("reply failed: %s", text)
	}
	d := eng.drafts[0]
	if d.AccountID != "acc2" {
		t.Errorf("account = %q, want the original's acc2", d.AccountID)
	}
	if len(d.To) != 1 || !strings.Contains(d.To[0], "alice@acme.example") || len(d.CC) != 0 {
		t.Errorf("to = %v, cc = %v", d.To, d.CC)
	}
	if d.Subject != "Re: Kickoff" {
		t.Errorf("subject = %q", d.Subject)
	}
	if !strings.HasPrefix(d.Body, "Passt.") || !strings.Contains(d.Body, "> Line one\n>\n>> older") || !strings.Contains(d.Body, "wrote:") {
		t.Errorf("body without quote: %q", d.Body)
	}
}

func TestReplyAllCopiesTheOthersButNotOwnAddresses(t *testing.T) {
	eng := &replyEngine{}
	runMCPExchange(t, replyServer(eng), draftCall(`{"replyToMessageId":"m1","replyType":"replyAll","body":"Hi"}`))
	d := eng.drafts[0]
	if got := strings.Join(d.CC, ","); got != "bob@acme.example,carol@acme.example" {
		t.Errorf("cc = %q", got)
	}
}

func TestReplyToOwnSentMailGoesToItsRecipients(t *testing.T) {
	eng := &replyEngine{}
	runMCPExchange(t, replyServer(eng), draftCall(`{"replyToMessageId":"sent","body":"Nachtrag"}`))
	d := eng.drafts[0]
	if len(d.To) != 1 || d.To[0] != "bob@acme.example" || d.Subject != "Re: Plan" {
		t.Errorf("to = %v, subject = %q", d.To, d.Subject)
	}
}

func TestForwardCarriesTextAndAttachments(t *testing.T) {
	eng := &replyEngine{}
	resps := runMCPExchange(t, replyServer(eng),
		draftCall(`{"replyToMessageId":"m1","replyType":"forward","body":"FYI"}`),
		draftCall(`{"replyToMessageId":"m1","replyType":"forward","to":["dave@acme.example"],"body":"FYI"}`))
	if _, isErr := resultText(t, resps[0]); !isErr {
		t.Fatal("forward without to must fail")
	}
	if len(eng.drafts) != 1 {
		t.Fatalf("drafts = %d", len(eng.drafts))
	}
	d := eng.drafts[0]
	if d.Subject != "Fwd: Kickoff" || !strings.Contains(d.Body, "Forwarded message") || !strings.Contains(d.Body, "Line one") {
		t.Errorf("subject = %q body = %q", d.Subject, d.Body)
	}
	if len(d.SourceAttachments) != 1 || d.SourceAttachments[0].Name != "plan.pdf" || d.SourceAttachments[0].MessageID != "m1" {
		t.Errorf("attachments = %+v", d.SourceAttachments)
	}
}

func TestEncryptedOriginalIsNeverQuotedOrForwarded(t *testing.T) {
	eng := &replyEngine{}
	resps := runMCPExchange(t, replyServer(eng),
		draftCall(`{"replyToMessageId":"enc","body":"Danke"}`),
		draftCall(`{"replyToMessageId":"enc","replyType":"forward","to":["dave@acme.example"],"body":"FYI"}`))
	if _, isErr := resultText(t, resps[1]); !isErr {
		t.Fatal("forwarding an encrypted mail must fail")
	}
	if len(eng.drafts) != 1 {
		t.Fatalf("drafts = %d", len(eng.drafts))
	}
	if d := eng.drafts[0]; d.Body != "Danke" || strings.Contains(d.Body, "CIPHERTEXT") {
		t.Errorf("body = %q", d.Body)
	}
}

func TestNewDraftKeepsAGivenAccount(t *testing.T) {
	eng := &replyEngine{}
	runMCPExchange(t, replyServer(eng), draftCall(`{"accountId":"acc2","to":["bob@acme.example"],"subject":"Hi","body":"x"}`))
	if d := eng.drafts[0]; d.AccountID != "acc2" || d.Subject != "Hi" {
		t.Errorf("draft = %+v", d)
	}
}

func TestSearchHasNoDayLimitUnlessAsked(t *testing.T) {
	eng := &replyEngine{}
	search := func(args string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_messages","arguments":` + args + `}}`
	}
	runMCPExchange(t, replyServer(eng), search(`{"q":"rechnung"}`), search(`{"q":"rechnung","days":900,"limit":500}`))
	if len(eng.lists) != 2 {
		t.Fatalf("lists = %d", len(eng.lists))
	}
	num := func(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }
	if num(eng.lists[0], "days") != 0 || num(eng.lists[0], "limit") != 20 {
		t.Errorf("default search = %v", eng.lists[0])
	}
	if num(eng.lists[1], "days") != 900 || num(eng.lists[1], "limit") != 50 {
		t.Errorf("explicit search = %v", eng.lists[1])
	}
}
