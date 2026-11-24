package nativeui

import (
	"strings"
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
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

func TestCompactFoldersAutoRuleTargetsAndCustomPins(t *testing.T) {
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.applyConfig(cfg)
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work"}}
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: "inbox", UnreadCount: 5},
		{ID: "f2", AccountID: "acc1", Name: "Jira", Path: "INBOX/Jira", UnreadCount: 2},
		{ID: "f3", AccountID: "acc1", Name: "Bestellungen", Path: "Bestellungen", UnreadCount: 1},
		{ID: "f4", AccountID: "acc1", Name: "Archives", Path: "Archives", Type: "archive"},
		{ID: "f5", AccountID: "acc1", Name: "Drafts", Path: "Drafts", Type: "drafts", UnreadCount: 2},
		{ID: "f6", AccountID: "acc1", Name: "Sent", Path: "Sent", Type: "sent"},
		{ID: "f7", AccountID: "acc1", Name: "Trash", Path: "Trash", Type: "trash", UnreadCount: 9},
		{ID: "f8", AccountID: "acc1", Name: "Spam", Path: "Spam", Type: "junk", UnreadCount: 4},
	}
	// Enabled rule moves incoming orders into top-level "Bestellungen"; terminal rule moves to Trash.
	app.direct.rules = []triage.NativeFilter{
		{
			AccountID: "acc1",
			Name:      "Orders",
			Enabled:   true,
			Actions:   []triage.NativeAction{{Type: "moveToFolder", TargetFolderPath: "Bestellungen"}},
		},
		{
			AccountID: "acc1",
			Name:      "Noise",
			Enabled:   true,
			Actions:   []triage.NativeAction{{Type: "moveToFolder", TargetFolderPath: "Trash"}},
		},
	}

	g := app.groupFoldersByAccount()[0]
	// Primary must contain INBOX (+ its child Jira) and rule target Bestellungen.
	if got, want := rowSummary(g.PrimaryRows), "INBOX+ .Jira Bestellungen"; got != want {
		t.Fatalf("PrimaryRows = %q, want %q", got, want)
	}
	// Secondary ("More folders") must hold Archives, Drafts, Sent, Trash, Spam (5 top-level folders).
	if g.OtherTopCount != 5 {
		t.Fatalf("OtherTopCount = %d, want 5", g.OtherTopCount)
	}
	if got, want := rowSummary(g.OtherRows), "Archives Drafts Sent Trash Spam"; got != want {
		t.Fatalf("OtherRows = %q, want %q", got, want)
	}
	// Unread badge on collapsed "More folders" must exclude Trash (9) and Spam (4), leaving Drafts (2).
	if g.OtherUnreadCount != 2 {
		t.Fatalf("OtherUnreadCount = %d, want 2 (excluding Trash/Spam)", g.OtherUnreadCount)
	}

	// Custom pinned folders via Settings: pin Sent only (in addition to INBOX).
	app.OpenSettings()
	pref := app.settings.folderPrefs["acc1"]
	if pref == nil {
		t.Fatalf("expected acc1 folderPref in settings")
	}
	pref.customPinned = true
	pref.pinned = map[string]bool{"sent": true}
	if !app.SaveSettings() {
		t.Fatalf("SaveSettings failed: %s", app.settings.err)
	}
	g = app.groupFoldersByAccount()[0]
	if got, want := rowSummary(g.PrimaryRows), "INBOX+ .Jira Sent"; got != want {
		t.Fatalf("after custom pin PrimaryRows = %q, want %q", got, want)
	}

	// Disabling CompactFolders puts all folders back into PrimaryRows with zero OtherRows.
	off := false
	cfg.App.CompactFolders = &off
	app.applyConfig(cfg)
	g = app.groupFoldersByAccount()[0]
	if len(g.OtherRows) != 0 || len(g.PrimaryRows) != len(g.Rows) {
		t.Fatalf("expected flat folders when CompactFolders=false, primary=%d other=%d", len(g.PrimaryRows), len(g.OtherRows))
	}
}

func TestFocusUnstarClearsStaleOpenMailAndUpdatesStateImmediately(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.navID = "focus"
	app.messages = []backend.MessageHeader{
		{ID: "m1", Subject: "Starred old mail", Unread: false, Flagged: true},
		{ID: "m2", Subject: "Unread mail", Unread: true, Flagged: false},
	}
	app.selectedID = "m1"
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
	}

	// Simulate unstarring m1 in Focus: applying the flag mutation + Focus departure must prevent
	// keepOpenMail from re-injecting the stale Flagged:true row back into the Focus list.
	app.messages[0].Flagged = false
	app.selectedMsg.Flagged = false
	if app.navID == "focus" && !app.selectedMsg.Unread && !app.selectedMsg.Flagged {
		app.selectedID = ""
		app.selectedMsg = nil
	}
	reloaded := []backend.MessageHeader{app.messages[1]}
	afterKeep := app.keepOpenMail(reloaded)
	if len(afterKeep) != 1 || afterKeep[0].ID != "m2" {
		t.Fatalf("expected unstarred read mail to leave Focus list, got %+v", afterKeep)
	}
}
