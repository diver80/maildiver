package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

func TestSearchScopeFilters(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.cfg.Language = "de"
	app.accounts = []backend.Account{{ID: "acc1", Name: "ich@example.org"}, {ID: "acc2", Name: "mail@example.org"}}
	app.folders = []backend.Folder{{ID: "f1", AccountID: "acc1", Name: "avono", Path: "avono"}}
	app.navID = "folder:f1"
	app.searchQuery = "wolfgang"

	if f := app.viewLoad().filter; f.AccountID != "" || f.FolderID != "" || f.Query != "wolfgang" {
		t.Fatalf("default search must look everywhere: %+v", f)
	}
	if got := app.searchPlaceholder(false); got != "Alle Postfächer durchsuchen" {
		t.Fatalf("placeholder: %q", got)
	}
	everywhereKey := app.serverSearchScopeKey("wolfgang")

	app.searchScope = searchAccount
	if f := app.viewLoad().filter; f.AccountID != "acc1" || f.FolderID != "" {
		t.Fatalf("account scope: %+v", f)
	}
	app.searchScope = searchFolder
	if f := app.viewLoad().filter; f.AccountID != "acc1" || f.FolderID != "f1" {
		t.Fatalf("folder scope: %+v", f)
	}
	if got := app.searchPlaceholder(false); got != "In avono suchen" || app.serverSearchScopeKey("wolfgang") == everywhereKey {
		t.Fatalf("folder scope placeholder %q / key not distinct", got)
	}

	// Outside a folder the folder scope widens: to the toolbar's account, else everywhere.
	app.navID = "recent"
	if app.effectiveSearchScope() != searchEverywhere {
		t.Fatal("no folder and no account: everywhere")
	}
	app.selectedAccount = "acc2"
	if f := app.viewLoad().filter; f.AccountID != "acc2" || f.FolderID != "" {
		t.Fatalf("widened to the account: %+v", f)
	}

	// Without a search the view's own filter applies again.
	app.searchQuery = ""
	app.navID = "folder:f1"
	if f := app.viewLoad().filter; f.FolderID != "f1" {
		t.Fatalf("folder view without search: %+v", f)
	}
}

func TestSearchScopeBarRenders(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.cfg.Language = "de"
	app.accounts = []backend.Account{{ID: "acc1", Name: "ich@example.org"}, {ID: "acc2", Name: "mail@example.org"}}
	app.folders = []backend.Folder{{ID: "f1", AccountID: "acc1", Name: "avono", Path: "avono"}}
	app.navID = "folder:f1"
	app.searchQuery, app.lastSearchQuery = "wolfgang", "wolfgang"
	tt := ui.NewTester(app.RenderMain, 1380, 900)
	tt.Frame()
	for _, want := range []string{"Suchen in:", "Überall", "ich@example.org", "avono", "„wolfgang“ · überall", "Auch auf dem Server suchen"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q; texts=%v", want, tt.Texts())
		}
	}
	app.setSearchScope(searchFolder)
	tt.Frame()
	if !tt.HasText("„wolfgang“ · in avono") {
		t.Fatalf("header should name the folder; texts=%v", tt.Texts())
	}
}

func TestSearchHasASidebarEntryAndAnyOtherEntryEndsIt(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.cfg.Language = "de"
	app.accounts = []backend.Account{{ID: "acc1", Name: "ich@example.org"}}
	app.navID = "focus"
	app.searchQuery, app.lastSearchQuery = "barbara", "barbara"
	tt := ui.NewTester(app.RenderMain, 1380, 900)
	tt.Frame()
	if !tt.HasText("Suche: barbara") {
		t.Fatalf("search entry missing; texts=%v", tt.Texts())
	}
	// Focus is where the search started: one click still ends the search and shows Focus.
	if err := tt.Click("Fokus (Ungelesen & ★)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.searchQuery != "" || app.lastSearchQuery != "" || app.navID != "focus" || tt.HasText("Suche: barbara") {
		t.Fatalf("Focus must end the search: query=%q nav=%q texts=%v", app.searchQuery, app.navID, tt.Texts())
	}

	app.searchQuery, app.lastSearchQuery = "barbara", "barbara"
	tt.Frame()
	if err := tt.Click("Aktuelle Mails"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.searchActive() || app.navID != "recent" {
		t.Fatalf("another entry must end the search and open: nav=%q", app.navID)
	}
}
