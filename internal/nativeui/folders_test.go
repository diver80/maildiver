package nativeui

import (
	"strings"
	"testing"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/triage"
)

func rowSummary(rows []folderRow) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		mark := ""
		if r.HasKids {
			mark = "+"
			if r.Collapsed {
				mark = ">"
			}
		}
		parts[i] = strings.Repeat(".", r.Depth) + r.Folder.Name + mark
	}
	return strings.Join(parts, " ")
}

func TestBuildFolderRowsNestsByPath(t *testing.T) {
	f := func(id, path string) backend.Folder {
		name := path[strings.LastIndex(path, "/")+1:]
		return backend.Folder{ID: id, AccountID: "a", Name: name, Path: path}
	}
	folders := []backend.Folder{
		f("1", "INBOX"),
		f("2", "Archiv/2021"), // child listed before its parent
		f("3", "Archiv"),
		f("4", "Archiv/2022"),
		f("5", "/Mailspring/Snoozed/Old"), // leading slash; middle folder missing
		f("6", "Mailspring"),
		f("7", "INBOX"), // duplicate path
	}
	got := rowSummary(buildFolderRows(folders, nil))
	want := "INBOX Archiv+ .2021 .2022 Mailspring+ .Old"
	if got != want {
		t.Fatalf("tree = %q, want %q", got, want)
	}

	collapsed := map[string]bool{folderKey(f("3", "Archiv")): true}
	got = rowSummary(buildFolderRows(folders, collapsed))
	want = "INBOX Archiv> Mailspring+ .Old"
	if got != want {
		t.Fatalf("collapsed tree = %q, want %q", got, want)
	}
}

func TestFolderTreeKeepsAccountsApart(t *testing.T) {
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.folders = []backend.Folder{
		{ID: "w1", AccountID: "work", AccountName: "Work", Name: "INBOX", Path: "INBOX"},
		{ID: "p1", AccountID: "home", AccountName: "Home", Name: "INBOX", Path: "INBOX"},
		{ID: "w2", AccountID: "work", AccountName: "Work", Name: "Sub", Path: "INBOX/Sub"},
	}
	groups := app.groupFoldersByAccount()
	if len(groups) != 2 || groups[0].Name != "Work" || groups[1].Name != "Home" {
		t.Fatalf("groups = %+v", groups)
	}
	if got := rowSummary(groups[0].Rows); got != "INBOX+ .Sub" {
		t.Fatalf("work tree = %q", got)
	}
	if got := rowSummary(groups[1].Rows); got != "INBOX" {
		t.Fatalf("home tree = %q (a same-named folder of another account must not nest)", got)
	}

	app.toggleFolderCollapsed(groups[0].Rows[0].Key)
	if got := rowSummary(app.groupFoldersByAccount()[0].Rows); got != "INBOX>" {
		t.Fatalf("after collapse = %q", got)
	}
	app.toggleFolderCollapsed(groups[0].Rows[0].Key)
	if got := rowSummary(app.groupFoldersByAccount()[0].Rows); got != "INBOX+ .Sub" {
		t.Fatalf("after expand = %q", got)
	}
}

func TestDefaultFolderCollapseDepthAndArchives(t *testing.T) {
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.folders = []backend.Folder{
		{ID: "1", AccountID: "acc", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "2", AccountID: "acc", Name: "atlassian", Path: "INBOX/atlassian"},
		{ID: "3", AccountID: "acc", Name: "done", Path: "INBOX/atlassian/done"},
		{ID: "4", AccountID: "acc", Name: "todo", Path: "INBOX/atlassian/todo"},
		{ID: "5", AccountID: "acc", Name: "ops", Path: "INBOX/ops"},
		{ID: "6", AccountID: "acc", Name: "done", Path: "INBOX/ops/done"},
		{ID: "7", AccountID: "acc", Name: "Archives", Path: "Archives", Type: "archive"},
		{ID: "8", AccountID: "acc", Name: "2024", Path: "Archives/2024"},
		{ID: "9", AccountID: "acc", Name: "2025", Path: "Archives/2025"},
		{ID: "10", AccountID: "acc", Name: "Sent", Path: "Sent", Type: "sent"},
	}

	// Startup: INBOX expanded (shows level 2 atlassian/ops), level 2 folders & Archives collapsed.
	got := rowSummary(app.groupFoldersByAccount()[0].Rows)
	want := "INBOX+ .atlassian> .ops> Archives> Sent"
	if got != want {
		t.Fatalf("default tree = %q, want %q", got, want)
	}

	// User expands "atlassian": must stay expanded on subsequent renders/reloads.
	app.toggleFolderCollapsed("acc\x00INBOX/atlassian")
	got = rowSummary(app.groupFoldersByAccount()[0].Rows)
	want = "INBOX+ .atlassian+ ..done ..todo .ops> Archives> Sent"
	if got != want {
		t.Fatalf("after expanding atlassian = %q, want %q", got, want)
	}
}

func TestCustomExpandedFoldersPerAccountAndPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.App.ExpandedFolders = map[string][]string{
		"acc1": {"INBOX/ops"}, // ancestor INBOX must auto-expand too; atlassian & Archives stay collapsed
		"acc2": {},            // explicit empty list -> even INBOX starts collapsed
	}
	cfg.App.HiddenCalendars = []string{"cal-hidden"}

	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.applyConfig(cfg)
	app.accounts = []backend.Account{
		{ID: "acc1", Name: "Work"},
		{ID: "acc2", Name: "Privat"},
	}
	app.folders = []backend.Folder{
		{ID: "1", AccountID: "acc1", AccountName: "Work", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "2", AccountID: "acc1", AccountName: "Work", Name: "atlassian", Path: "INBOX/atlassian"},
		{ID: "3", AccountID: "acc1", AccountName: "Work", Name: "done", Path: "INBOX/atlassian/done"},
		{ID: "4", AccountID: "acc1", AccountName: "Work", Name: "ops", Path: "INBOX/ops"},
		{ID: "5", AccountID: "acc1", AccountName: "Work", Name: "done", Path: "INBOX/ops/done"},
		{ID: "6", AccountID: "acc1", AccountName: "Work", Name: "Archives", Path: "Archives", Type: "archive"},
		{ID: "7", AccountID: "acc1", AccountName: "Work", Name: "2025", Path: "Archives/2025"},
		{ID: "8", AccountID: "acc2", AccountName: "Privat", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "9", AccountID: "acc2", AccountName: "Privat", Name: "Reisen", Path: "INBOX/Reisen"},
	}

	groups := app.groupFoldersByAccount()
	if got, want := rowSummary(groups[0].Rows), "INBOX+ .atlassian> .ops+ ..done Archives>"; got != want {
		t.Fatalf("acc1 custom tree = %q, want %q", got, want)
	}
	if got, want := rowSummary(groups[1].Rows), "INBOX>"; got != want {
		t.Fatalf("acc2 empty-custom tree = %q, want %q", got, want)
	}
	if !app.isCalendarHidden("cal-hidden") {
		t.Fatal("expected HiddenCalendars from config.json to be restored by applyConfig")
	}

	// OpenSettings -> switch acc1 to expand Archives only -> SaveSettings applies immediately & persists.
	app.OpenSettings()
	pref := app.settings.folderPrefs["acc1"]
	if pref == nil || !pref.custom {
		t.Fatalf("expected acc1 custom pref loaded in Settings, got %+v", pref)
	}
	pref.expanded = map[string]bool{"archives": true}
	if !app.SaveSettings() {
		t.Fatalf("SaveSettings failed: %s", app.settings.err)
	}
	groups = app.groupFoldersByAccount()
	if got, want := rowSummary(groups[0].Rows), "INBOX> Archives+ .2025"; got != want {
		t.Fatalf("after SaveSettings acc1 tree = %q, want %q", got, want)
	}
	if len(cfg.App.ExpandedFolders["acc1"]) != 1 || cfg.App.ExpandedFolders["acc1"][0] != "Archives" {
		t.Fatalf("expected persisted ExpandedFolders[acc1]=[Archives], got %+v", cfg.App.ExpandedFolders["acc1"])
	}
}

func TestAccountOrderingAndReorderPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{
		{ID: "acc-work", Name: "Work", Email: "alice@acme.de", Enabled: true},
		{ID: "acc-home", Name: "Home", Email: "alice@example.org", Enabled: true},
		{ID: "acc-list", Name: "Lists", Email: "lists@example.org", Enabled: true},
	}
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.applyConfig(cfg)
	app.accounts = []backend.Account{
		{ID: "acc-work", Name: "Work"},
		{ID: "acc-home", Name: "Home"},
		{ID: "acc-list", Name: "Lists"},
	}
	// Folders arrive in a different order than configured accounts: sidebar must still follow a.accounts.
	app.folders = []backend.Folder{
		{ID: "f-l", AccountID: "acc-list", AccountName: "Lists", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "f-h", AccountID: "acc-home", AccountName: "Home", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "f-w", AccountID: "acc-work", AccountName: "Work", Name: "INBOX", Path: "INBOX", Type: "inbox"},
	}

	groups := app.groupFoldersByAccount()
	if len(groups) != 3 || groups[0].AccountID != "acc-work" || groups[1].AccountID != "acc-home" || groups[2].AccountID != "acc-list" {
		t.Fatalf("initial sidebar order = %+v, want [acc-work, acc-home, acc-list]", groups)
	}

	// Drag "acc-list" onto "acc-work" (top).
	app.ReorderAccounts("acc-list", "acc-work")
	groups = app.groupFoldersByAccount()
	if groups[0].AccountID != "acc-list" || groups[1].AccountID != "acc-work" || groups[2].AccountID != "acc-home" {
		t.Fatalf("after drag to top = [%s, %s, %s]", groups[0].AccountID, groups[1].AccountID, groups[2].AccountID)
	}
	if cfg.Direct.Accounts[0].ID != "acc-list" || cfg.Direct.Accounts[1].ID != "acc-work" || cfg.Direct.Accounts[2].ID != "acc-home" {
		t.Fatalf("persisted config order = %+v", cfg.Direct.Accounts)
	}

	// Drag "acc-list" onto "acc-home" (bottom).
	app.ReorderAccounts("acc-list", "acc-home")
	groups = app.groupFoldersByAccount()
	if groups[0].AccountID != "acc-work" || groups[1].AccountID != "acc-home" || groups[2].AccountID != "acc-list" {
		t.Fatalf("after drag to bottom = [%s, %s, %s]", groups[0].AccountID, groups[1].AccountID, groups[2].AccountID)
	}
}

func TestSidebarCollapseAutoPersistsAcrossRestart(t *testing.T) {
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	folders := []backend.Folder{
		{ID: "1", AccountID: "acc", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "2", AccountID: "acc", Name: "Work", Path: "INBOX/Work"},
		{ID: "3", AccountID: "acc", Name: "Projects", Path: "INBOX/Work/Projects"},
	}
	app.folders = folders
	_ = app.groupFoldersByAccount()

	// Expanding INBOX/Work in the sidebar should immediately persist to cfg.App.ExpandedFolders.
	app.toggleFolderCollapsed("acc\x00INBOX/Work")
	if got := cfg.App.ExpandedFolders["acc"]; len(got) != 2 {
		t.Fatalf("expected persisted ExpandedFolders [INBOX INBOX/Work], got %v", got)
	}

	// Collapsing account section should persist in cfg.App.CollapsedAccounts and survive restart.
	open := app.accountSectionOpen("acc")
	*open = false
	app.persistCollapsedAccounts()
	if len(cfg.App.CollapsedAccounts) != 1 || cfg.App.CollapsedAccounts[0] != "acc" {
		t.Fatalf("expected CollapsedAccounts=[acc], got %v", cfg.App.CollapsedAccounts)
	}

	restarted := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	restarted.applyConfig(cfg)
	restarted.folders = folders
	if got := rowSummary(restarted.groupFoldersByAccount()[0].Rows); got != "INBOX+ .Work+ ..Projects" {
		t.Fatalf("restarted folder tree = %q, want INBOX/Work still expanded", got)
	}
	if *restarted.accountSectionOpen("acc") {
		t.Fatal("expected account section acc to stay collapsed after restart")
	}
}
