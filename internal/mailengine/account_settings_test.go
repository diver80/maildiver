package mailengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func newAccountSettingsEngine(t *testing.T) (*Engine, func() config.Config) {
	t.Helper()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng, func() config.Config { return cfg }
}

func TestSaveAccountKeepsPausedAccountPaused(t *testing.T) {
	ctx := context.Background()
	eng, current := newAccountSettingsEngine(t)
	acc, err := eng.SaveAccount(ctx, config.DirectAccount{Email: "me@example.org", IMAPHost: "imap.example.org"}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Enabled {
		t.Fatal("new accounts must start enabled")
	}

	acc.Enabled = false
	if _, err := eng.SaveAccount(ctx, acc, ""); err != nil {
		t.Fatal(err)
	}
	// Editing another field (and even re-entering the password) must not switch syncing back on.
	acc.Name = "Privat"
	if _, err := eng.SaveAccount(ctx, acc, "pw2"); err != nil {
		t.Fatal(err)
	}
	cfg := current()
	if len(cfg.Direct.Accounts) != 1 || cfg.Direct.Accounts[0].Enabled || cfg.Direct.Accounts[0].Name != "Privat" {
		t.Fatalf("paused account was re-enabled or not saved: %+v", cfg.Direct.Accounts)
	}
	st := eng.AccountsStatus(ctx)
	if len(st) != 1 || st[0].State != "stopped" {
		t.Fatalf("paused account must report stopped: %+v", st)
	}
	// Its mails stay visible: the account is still listed for the UI.
	raw, err := eng.rpcAccountsList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Accounts []backend.Account }
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 1 {
		t.Fatalf("paused account missing from accounts.list: %s", raw)
	}
}

func TestSaveAccountEnablesImportedAccountOnFirstPassword(t *testing.T) {
	ctx := context.Background()
	eng, current := newAccountSettingsEngine(t)
	cfg := current()
	cfg.Direct.Accounts = []config.DirectAccount{{ID: "acc-tb", Email: "me@example.org", IMAPHost: "imap.example.org"}}
	if err := eng.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	acc := cfg.Direct.Accounts[0]
	if _, err := eng.SaveAccount(ctx, acc, "pw"); err != nil {
		t.Fatal(err)
	}
	if !current().Direct.Accounts[0].Enabled {
		t.Fatal("imported account should start syncing once its first password is saved")
	}
}

func TestSaveAccountRespectsTriageExclusion(t *testing.T) {
	ctx := context.Background()
	eng, current := newAccountSettingsEngine(t)
	acc, err := eng.SaveAccount(ctx, config.DirectAccount{Email: "me@example.org", IMAPHost: "imap.example.org"}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	cfg := current()
	if !cfg.Mailbox.TriageIncludes(acc.ID) {
		t.Fatal("new accounts must be included in triage")
	}
	cfg.Mailbox.TriageExcludedAccountIDs = []string{acc.ID}
	if err := eng.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SaveAccount(ctx, acc, ""); err != nil {
		t.Fatal(err)
	}
	cfg = current()
	if cfg.Mailbox.TriageIncludes(acc.ID) {
		t.Fatal("saving the account re-included it in triage")
	}
	// Ordinary mail actions (mark read) stay allowed for an excluded account.
	if !cfg.Mailbox.CanWrite(acc.ID) {
		t.Fatal("triage exclusion must not block mail actions")
	}
	if err := eng.DeleteAccount(ctx, acc.ID); err != nil {
		t.Fatal(err)
	}
	if ids := current().Mailbox.TriageExcludedAccountIDs; len(ids) != 0 {
		t.Fatalf("deleted account left in exclusion list: %v", ids)
	}
}

func TestUpdateCalendarSourceSwitchAndColor(t *testing.T) {
	ctx := context.Background()
	eng, current := newAccountSettingsEngine(t)
	cfg := current()
	cfg.Direct.ICalFeeds = []config.ICalFeedConfig{{ID: "f1", Name: "Feiertage", URL: "https://example.org/f.ics", Color: "#73daca", Enabled: true}}
	cfg.Direct.CardDAVAccounts = []config.DAVAccountConfig{{ID: "d1", Name: "Adressen", Enabled: true}}
	if err := eng.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := eng.store.UpsertCalendar(ctx, StoredCalendar{ID: "ical-f1", Name: "Feiertage", Source: "ical", Color: "#73daca"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.UpdateCalendarSource(ctx, "ical", "f1", false, "#f7768e"); err != nil {
		t.Fatal(err)
	}
	if err := eng.UpdateCalendarSource(ctx, "carddav", "d1", false, ""); err != nil {
		t.Fatal(err)
	}
	cfg = current()
	if f := cfg.Direct.ICalFeeds[0]; f.Enabled || f.Color != "#f7768e" {
		t.Fatalf("feed = %+v", f)
	}
	if cfg.Direct.CardDAVAccounts[0].Enabled {
		t.Fatal("address book still enabled")
	}
	if cal, err := eng.store.GetCalendar(ctx, "ical-f1"); err != nil || cal.Color != "#f7768e" {
		t.Fatalf("cached calendar color = %+v err=%v", cal, err)
	}
	if err := eng.UpdateCalendarSource(ctx, "ical", "f1", true, "red; drop"); err == nil {
		t.Fatal("invalid colors must be rejected")
	}
	if err := eng.UpdateCalendarSource(ctx, "caldav", "missing", true, ""); err == nil {
		t.Fatal("unknown sources must be reported")
	}
}
