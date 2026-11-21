package mailengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/secretstore"
	"github.com/diver80/maildiver/internal/triage"
)

func TestStoreFilterReorderAndBodyHeaderMatching(t *testing.T) {
	ctx := context.Background()
	st, err := OpenStore(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		if _, err := st.UpsertFilter(ctx, backend.FilterUpsertRequest{
			AccountID:  "acc-1",
			Name:       name,
			Enabled:    true,
			MatchAll:   true,
			Conditions: []triage.NativeCondition{{Field: "subject", Op: "contains", Value: name}},
			Actions:    []backend.FilterActionSpec{{Type: "markRead"}},
		}); err != nil {
			t.Fatalf("UpsertFilter(%s): %v", name, err)
		}
	}
	if err := st.ReorderFilter(ctx, "acc-1", "Gamma", -1); err != nil {
		t.Fatalf("ReorderFilter: %v", err)
	}
	list, err := st.ListFilters(ctx, "acc-1")
	if err != nil || len(list) != 3 {
		t.Fatalf("ListFilters: err=%v len=%d", err, len(list))
	}
	if list[0].Name != "Alpha" || list[1].Name != "Gamma" || list[2].Name != "Beta" {
		t.Fatalf("unexpected filter order after move up: %s, %s, %s", list[0].Name, list[1].Name, list[2].Name)
	}

	msg := StoredMessage{
		ID:          "m-body-1",
		AccountID:   "acc-1",
		Subject:     "Statusbericht",
		From:        "ci@example.com",
		CC:          []string{"qa@example.com"},
		BodyText:    "Build pipeline failed in module pgp.",
		Headers:     map[string]string{"x-pipeline": "nightly"},
		Attachments: []backend.Attachment{{Name: "build.log"}},
	}
	flt := StoredFilter{
		Name:     "Body + Header + Attachment",
		Enabled:  true,
		MatchAll: true,
		Conditions: []triage.NativeCondition{
			{Field: "body", Op: "contains", Value: "module pgp", BooleanAnd: true},
			{Field: "cc", Op: "contains", Value: "qa@example.com", BooleanAnd: true},
			{Field: "header", HeaderName: "X-Pipeline", Op: "is", Value: "nightly", BooleanAnd: true},
			{Field: "attachment", Op: "endsWith", Value: ".log", BooleanAnd: true},
		},
	}
	if !matchFilterAgainstMessage(flt, msg) {
		t.Fatalf("expected matchFilterAgainstMessage to evaluate body, cc, headers, and attachments")
	}
}

func TestEnginePGPComposeAndReadRoundTrip(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	cfg.Direct.Accounts = []config.DirectAccount{
		{
			ID:       "acc-alice",
			Name:     "Alice",
			Email:    "alice@example.org",
			IMAPHost: "imap.example.org",
			IMAPPort: 993,
			IMAPTLS:  "tls",
			SMTPHost: "smtp.example.org",
			SMTPPort: 465,
			SMTPTLS:  "tls",
			Enabled:  true,
		},
	}

	dir := t.TempDir()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "mail.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatalf("New Engine: %v", err)
	}
	defer eng.Close()

	if _, err := eng.GeneratePGPKey("Alice", "alice@example.org", ""); err != nil {
		t.Fatalf("GeneratePGPKey(alice): %v", err)
	}
	if _, err := eng.GeneratePGPKey("Bob", "bob@example.org", ""); err != nil {
		t.Fatalf("GeneratePGPKey(bob): %v", err)
	}

	acc := cfg.Direct.Accounts[0]
	_, _, rawEncSigned, compSec, err := eng.buildComposeRaw(context.Background(), acc, backend.ComposeRequest{
		AccountID:  acc.ID,
		FromEmail:  "alice@example.org",
		To:         []string{"bob@example.org"},
		Subject:    "PGP Testnachricht",
		Body:       "Vertraulicher Klartext über PGP/MIME.",
		Sign:       "on",
		Encrypt:    true,
		CryptoTech: "pgp",
	}, false)
	if err != nil {
		t.Fatalf("buildComposeRaw PGP: %v", err)
	}
	if compSec == nil || compSec.Technology != "OpenPGP" || !compSec.Signed || !compSec.Encrypted {
		t.Fatalf("expected OpenPGP ComposeSecurity signed+encrypted, got %+v", compSec)
	}

	// Store as if received by Bob, then read via messages.get (both un-decrypted AI safety check & decrypted).
	ctx := context.Background()
	sm := StoredMessage{
		ID:        "msg-pgp-1",
		AccountID: acc.ID,
		From:      "Alice <alice@example.org>",
		To:        []string{"bob@example.org"},
		Subject:   "PGP Testnachricht",
		RawEML:    rawEncSigned,
	}
	mergeParsedIntoStored(&sm, rawEncSigned)
	if err := eng.store.UpsertMessage(ctx, sm); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	// With decrypt=false: must stay redacted
	rawNoDec, err := eng.Call(ctx, "messages.get", map[string]any{"id": "msg-pgp-1", "decrypt": false, "format": "both"})
	if err != nil {
		t.Fatalf("messages.get(decrypt=false): %v", err)
	}
	var detNoDec backend.MessageDetail
	_ = json.Unmarshal(rawNoDec, &detNoDec)
	if detNoDec.Security.Readable() || detNoDec.BodyText != "" {
		t.Fatalf("expected encrypted PGP mail redacted when decrypt=false, got readable=%v body=%q", detNoDec.Security.Readable(), detNoDec.BodyText)
	}

	// With decrypt=true: decrypted plaintext + verified PGP signature!
	rawDec, err := eng.Call(ctx, "messages.get", map[string]any{"id": "msg-pgp-1", "decrypt": true, "format": "both"})
	if err != nil {
		t.Fatalf("messages.get(decrypt=true): %v", err)
	}
	var detDec backend.MessageDetail
	_ = json.Unmarshal(rawDec, &detDec)
	if !detDec.Security.PGP || !detDec.Security.Readable() || !detDec.Security.Signed || !detDec.Security.SignatureValid || detDec.Security.SignerAddressMismatch {
		t.Fatalf("expected valid decrypted & signed OpenPGP message security, got %+v", detDec.Security)
	}
	if !strings.Contains(detDec.BodyText, "Vertraulicher Klartext über PGP/MIME.") {
		t.Fatalf("expected decrypted body text, got %q", detDec.BodyText)
	}
}
