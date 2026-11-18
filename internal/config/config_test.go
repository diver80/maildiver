package config

import (
	"os"
	"path/filepath"
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
	if !cfg.Mailbox.AutoMoveOnlyUnread {
		t.Fatal("auto-move must default to unread mails only")
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
	if cfg.Features.Triage || !cfg.Mailbox.ReadOnly || len(cfg.Mailbox.TriageAccountIDs) != 0 || len(cfg.Mailbox.KanbanAccountIDs) != 0 {
		t.Fatalf("legacy config must load with triage off and read-only on: %+v %+v", cfg.Features, cfg.Mailbox)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "r1" || cfg.Rules[0].Space != "Acme" {
		t.Fatalf("rules lost: %+v", cfg.Rules)
	}
}

func TestApplyPatchKeepsAbsentFields(t *testing.T) {
	base := Default()
	base.Rules = []RoutingRule{{ID: "keep", Space: "Keep", FolderPath: "Spaces/Keep", Domains: []string{"keep.de"}}}
	base.Mailbox.TriageAccountIDs = []string{"account1"}
	base.Mailbox.KanbanAccountIDs = []string{"account1"}

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
	if len(next.Rules) != 1 || next.Rules[0].ID != "keep" {
		t.Fatalf("rules absent from patch must be kept: %+v", next.Rules)
	}
	if len(next.Mailbox.TriageAccountIDs) != 1 || len(next.Mailbox.KanbanAccountIDs) != 1 || !next.Mailbox.ReadOnly {
		t.Fatalf("mailbox absent from patch must be kept: %+v", next.Mailbox)
	}
}

func TestApplyPatchReplacesRulesWithoutStaleFields(t *testing.T) {
	base := Default()
	base.Rules = []RoutingRule{{ID: "old", FolderPath: "Spaces/Old", AutoMove: true, ColumnFolders: map[string]string{"done": "Spaces/Old/Done"}}}
	base.Mailbox.TriageAccountIDs = []string{"a1", "a2"}
	base.Mailbox.KanbanAccountIDs = []string{"a1", "a2"}

	next, err := ApplyPatch(base, []byte(`{"rules":[{"id":"new","folderPath":"Spaces/New","domains":["new.de"]}],"mailbox":{"triageAccountIds":["a2"],"kanbanAccountIds":["a2"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Rules) != 1 || next.Rules[0].ID != "new" {
		t.Fatalf("rules not replaced: %+v", next.Rules)
	}
	if next.Rules[0].AutoMove || len(next.Rules[0].ColumnFolders) != 0 {
		t.Fatalf("fields of the replaced rule leaked into the new one: %+v", next.Rules[0])
	}
	if len(next.Mailbox.TriageAccountIDs) != 1 || next.Mailbox.TriageAccountIDs[0] != "a2" {
		t.Fatalf("account list not replaced: %+v", next.Mailbox.TriageAccountIDs)
	}
	if len(next.Mailbox.KanbanAccountIDs) != 1 || next.Mailbox.KanbanAccountIDs[0] != "a2" {
		t.Fatalf("kanban account list not replaced: %+v", next.Mailbox.KanbanAccountIDs)
	}
	if !next.Mailbox.ReadOnly {
		t.Fatal("readOnly absent from mailbox patch must be kept")
	}
}

func TestMailboxPolicy(t *testing.T) {
	p := MailboxPolicy{ReadOnly: false, TriageAccountIDs: []string{"a1", "a2"}, KanbanAccountIDs: []string{"a1"}}
	if !p.CanWrite("a1") || !p.CanWrite("a2") || p.CanWrite("a3") || p.CanWrite("") {
		t.Fatal("CanWrite must only allow enabled accounts")
	}
	if !p.KanbanEnabled("a1") || p.KanbanEnabled("a2") || p.KanbanEnabled("a3") {
		t.Fatal("KanbanEnabled must require both AccountEnabled and KanbanAccountIDs membership")
	}
	p.ReadOnly = true
	if p.CanWrite("a1") {
		t.Fatal("read-only must block writes")
	}
}

func TestKanbanFoldersPatchAndPolicy(t *testing.T) {
	base := Default()
	base.Mailbox.KanbanFolders = map[string][]string{
		"a1": {"INBOX", "INBOX/atlassian", "INBOX/avono"},
		"a2": {"INBOX"},
	}
	if !base.Mailbox.HasKanbanFolders("a1") || base.Mailbox.HasKanbanFolders("missing") {
		t.Fatalf("unexpected HasKanbanFolders result: %+v", base.Mailbox.KanbanFolders)
	}

	// Patching an unrelated mailbox field preserves KanbanFolders.
	kept, err := ApplyPatch(base, []byte(`{"mailbox":{"scanOnNewMail":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Mailbox.KanbanFolders["a1"]) != 3 || len(kept.Mailbox.KanbanFolders["a2"]) != 1 {
		t.Fatalf("kanbanFolders should be preserved when absent from patch: %+v", kept.Mailbox.KanbanFolders)
	}

	// Patching kanbanFolders replaces the map cleanly instead of keeping deleted entries.
	replaced, err := ApplyPatch(base, []byte(`{"mailbox":{"kanbanFolders":{"a1":["INBOX/avono"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Mailbox.KanbanFolders) != 1 || len(replaced.Mailbox.KanbanFolders["a1"]) != 1 || replaced.Mailbox.KanbanFolders["a1"][0] != "INBOX/avono" {
		t.Fatalf("kanbanFolders not cleanly replaced: %+v", replaced.Mailbox.KanbanFolders)
	}
}

func TestBridgeConfigDefaultsAndPatch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := Default()
	if cfg.Bridge.Enabled || cfg.Bridge.Port != 0 || cfg.Bridge.AllowSend || len(cfg.Bridge.AccountIDs) != 0 {
		t.Fatalf("bridge must be off by default: %+v", cfg.Bridge)
	}
	path, err := BridgeInfoPath()
	if err != nil || filepath.Base(path) != "bridge.json" {
		t.Fatalf("unexpected BridgeInfoPath: %q err=%v", path, err)
	}
	norm := Normalize(Config{Bridge: BridgeConfig{Enabled: true, Port: 70000}})
	if norm.Bridge.Port != 0 {
		t.Fatalf("out-of-range bridge port must reset to 0, got %d", norm.Bridge.Port)
	}
	patched, err := ApplyPatch(cfg, []byte(`{"bridge":{"enabled":true,"port":19876,"allowSend":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !patched.Bridge.Enabled || patched.Bridge.Port != 19876 || !patched.Bridge.AllowSend {
		t.Fatalf("unexpected patched bridge config: %+v", patched.Bridge)
	}
}
