package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsAreSafe(t *testing.T) {
	cfg := Default()
	if !cfg.Features.Theme || !cfg.Features.Assistant {
		t.Fatalf("theme and assistant should default on: %+v", cfg.Features)
	}
	if cfg.Features.Triage {
		t.Fatal("triage must default off")
	}
	if !cfg.Mailbox.ReadOnly {
		t.Fatal("mailbox must default to read-only")
	}
	if len(cfg.Mailbox.TriageAccountIDs) != 0 {
		t.Fatal("no account may be enabled by default")
	}
	if cfg.AI.ClassifyWithAI || cfg.AI.AllowUnrestrictedCLI {
		t.Fatal("scans must not use AI and unrestricted CLIs must stay blocked by default")
	}
	if cfg.Theme.StyleBody {
		t.Fatal("mail bodies must not be recolored by default")
	}
}

func TestLegacyConfigGetsSafeDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	legacy := `{"language":"de","incomingFolder":"INBOX","rules":[{"id":"r1","space":"Acme","folderPath":"Spaces/Acme","domains":["acme.de"],"autoMove":true,"includeInScan":true}]}`
	if err := os.MkdirAll(filepath.Join(dir, AppID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AppID, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Features.Triage || !cfg.Mailbox.ReadOnly || len(cfg.Mailbox.TriageAccountIDs) != 0 {
		t.Fatalf("legacy config must load with triage off and read-only on: %+v %+v", cfg.Features, cfg.Mailbox)
	}
	// The Kanban board's Spaces rules are gone; such a file still loads, and saving drops them.
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, AppID, "config.json"))
	if strings.Contains(string(data), "Spaces/Acme") || strings.Contains(string(data), `"autoMove"`) || strings.Contains(string(data), "kanban") {
		t.Fatalf("saved config keeps board fields: %s", data)
	}
}

func TestApplyPatchKeepsAbsentFields(t *testing.T) {
	base := Default()
	base.Mailbox.TriageAccountIDs = []string{"account1"}

	next, err := ApplyPatch(base, []byte(`{"language":"en","features":{"triage":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if next.Language != "en" || !next.Features.Triage {
		t.Fatalf("patch not applied: %+v", next)
	}
	if !next.Features.Theme || !next.Features.Assistant {
		t.Fatalf("nested fields absent from patch must be kept: %+v", next.Features)
	}
	if len(next.Mailbox.TriageAccountIDs) != 1 || !next.Mailbox.ReadOnly {
		t.Fatalf("mailbox absent from patch must be kept: %+v", next.Mailbox)
	}
}

func TestApplyPatchReplacesAccountList(t *testing.T) {
	base := Default()
	base.Mailbox.TriageAccountIDs = []string{"a1", "a2"}

	next, err := ApplyPatch(base, []byte(`{"mailbox":{"triageAccountIds":["a2"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Mailbox.TriageAccountIDs) != 1 || next.Mailbox.TriageAccountIDs[0] != "a2" {
		t.Fatalf("account list not replaced: %+v", next.Mailbox.TriageAccountIDs)
	}
	if !next.Mailbox.ReadOnly {
		t.Fatal("readOnly absent from mailbox patch must be kept")
	}
}

func TestMailboxPolicy(t *testing.T) {
	p := MailboxPolicy{ReadOnly: false, TriageAccountIDs: []string{"a1", "a2"}}
	if !p.CanWrite("a1") || !p.CanWrite("a2") || p.CanWrite("a3") || p.CanWrite("") {
		t.Fatal("CanWrite must only allow enabled accounts")
	}
	p.ReadOnly = true
	if p.CanWrite("a1") {
		t.Fatal("read-only must block writes")
	}
}

func TestLegacyOmaThunderConfigFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	legacyDir := filepath.Join(dir, LegacyAppID)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(`{"language":"en","direct":{"accounts":[{"id":"acc-1","email":"test@example.com","imapHost":"imap.example.com","smtpHost":"smtp.example.com","enabled":true}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "en" || len(cfg.Direct.Accounts) != 1 || cfg.Direct.Accounts[0].Email != "test@example.com" {
		t.Fatalf("expected legacy omathunder config fallback, got %+v", cfg)
	}
}

func TestEmptyLinkPreviewRulesPersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := Default()
	if len(cfg.App.LinkPreview.Rules) == 0 {
		t.Fatal("default config should seed built-in link preview rules")
	}
	cfg.App.LinkPreview.Rules = []LinkPreviewRule{}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.App.LinkPreview.Rules == nil || len(loaded.App.LinkPreview.Rules) != 0 {
		t.Fatalf("explicitly empty LinkPreview.Rules must not be re-seeded on Load, got %+v", loaded.App.LinkPreview.Rules)
	}
}

func TestDirectDBPathUsesDataDirAndNormalizesAliases(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_STATE_HOME", stateDir)

	dbPath, err := DirectDBPath()
	if err != nil {
		t.Fatal(err)
	}
	legacyPath, err := LegacyDirectDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if dbPath != filepath.Join(dataDir, AppID, "maildiver.db") {
		t.Fatalf("DirectDBPath = %q, want under XDG_DATA_HOME", dbPath)
	}
	if legacyPath != filepath.Join(stateDir, AppID, "maildiver.db") {
		t.Fatalf("LegacyDirectDBPath = %q, want under XDG_STATE_HOME", legacyPath)
	}

	cfg := Default()
	cfg.Direct.Accounts = []DirectAccount{{
		ID:       "acc-1",
		Email:    "main@example.de",
		IMAPHost: "imap.example.de",
		SMTPHost: "smtp.example.de",
		Aliases: []AccountAlias{
			{Email: " MAIN@example.de ", DisplayName: "Dup Primary"},
			{Email: " info@example.de ", DisplayName: " Info Team "},
			{Email: "INFO@EXAMPLE.DE", DisplayName: "Dup Alias"},
			{Email: "invalid-no-at", DisplayName: "Bad"},
			{Email: "sales@example.de"},
		},
		Enabled: true,
	}}
	norm := Normalize(cfg)
	aliases := norm.Direct.Accounts[0].Aliases
	if len(aliases) != 2 {
		t.Fatalf("expected 2 unique valid aliases, got %+v", aliases)
	}
	if aliases[0].Email != "info@example.de" || aliases[0].DisplayName != "Info Team" || aliases[1].Email != "sales@example.de" {
		t.Fatalf("unexpected normalized aliases: %+v", aliases)
	}
}
