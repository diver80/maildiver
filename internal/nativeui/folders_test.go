package nativeui

import (
	"strings"
	"testing"

	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/triage"
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
	f := func(id, path string) bridgeclient.Folder {
		name := path[strings.LastIndex(path, "/")+1:]
		return bridgeclient.Folder{ID: id, AccountID: "a", Name: name, Path: path}
	}
	folders := []bridgeclient.Folder{
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
	app.folders = []bridgeclient.Folder{
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
