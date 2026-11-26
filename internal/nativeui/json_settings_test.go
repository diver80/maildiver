package nativeui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
)

// The account form carries sync limit, the sync switch and per-alias signatures.
func TestDirectAccountFormSyncLimitSwitchAndAliases(t *testing.T) {
	app := NewApp(Options{})
	app.resetDirectAccountForm()
	d := &app.direct
	d.accEmail, d.imapHost = "me@example.org", "imap.example.org"
	d.accAliasRows = aliasRowsFromConfig([]config.AccountAlias{
		{Email: "sales@example.org", DisplayName: "Sales", Signature: "Gruß\nSales", SignatureHTML: "<p>Sales</p>"},
		{Email: "info@example.org", Signature: "Info"},
	})
	d.accAliasRows = append(d.accAliasRows, aliasRow{}) // empty row from "+ Alias": dropped
	d.accAliasRows[1].signature = "Info-Team"           // edited text drops the stale HTML of that alias only

	acc, _, err := app.buildDirectAccountFromForm()
	if err != nil {
		t.Fatal(err)
	}
	if acc.SyncLimit != config.DefaultSyncLimit || !acc.Enabled {
		t.Fatalf("new account defaults: limit=%d enabled=%v", acc.SyncLimit, acc.Enabled)
	}
	if len(acc.Aliases) != 2 || acc.Aliases[0].SignatureHTML != "<p>Sales</p>" || acc.Aliases[1].Signature != "Info-Team" || acc.Aliases[1].SignatureHTML != "" {
		t.Fatalf("aliases = %+v", acc.Aliases)
	}

	d.editingID = "acc1"
	d.accEnabled = false
	d.accSyncLimit = "1200"
	if acc, _, err = app.buildDirectAccountFromForm(); err != nil || acc.Enabled || acc.SyncLimit != 1200 {
		t.Fatalf("edited account: %+v err=%v", acc, err)
	}
	d.accSyncLimit = "5"
	if _, _, err = app.buildDirectAccountFromForm(); err == nil {
		t.Fatal("a sync limit below the minimum must be rejected")
	}

	// The form starts compact and expands advanced settings (sync limit, switch, aliases) on click.
	d.accSyncLimit = ""
	app.opts.Direct = struct{ DirectController }{}
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 380, 760
		app.renderDirectMailTab(c)
	}, 380, 2400)
	tt.Frame()
	if tt.HasText("Emails synced per folder") {
		t.Fatalf("advanced fields should be collapsed by default: %v", tt.Texts())
	}
	tt.Click("▸ Advanced settings (servers, signature, aliases…)")
	for _, want := range []string{"Emails synced per folder", "Sync this account", "+ Add alias", "Paused: no more fetching, emails already loaded stay visible."} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q at phone width: %v", want, tt.Texts())
		}
	}
}

// calendarSourceDirect records UpdateCalendarSource calls.
type calendarSourceDirect struct {
	DirectController
	mu    sync.Mutex
	calls []string
}

func (f *calendarSourceDirect) UpdateCalendarSource(_ context.Context, kind, id string, enabled bool, color string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s/%s/%v/%s", kind, id, enabled, color))
	return nil
}

func (f *calendarSourceDirect) AccountsStatus(context.Context) []mailengine.AccountSyncStatus {
	return nil
}
func (f *calendarSourceDirect) GoogleCalStatus(context.Context) mailengine.GoogleCalStatus {
	return mailengine.GoogleCalStatus{}
}

// iCal feeds and CalDAV calendars get a sync switch and a color palette; address books only the switch.
func TestCalendarSourcesSwitchAndColor(t *testing.T) {
	fd := &calendarSourceDirect{}
	app := newParityApp(t, fd, nil)
	app.direct.icsFeeds = []config.ICalFeedConfig{{ID: "feed1", Name: "Feiertage", URL: "https://example.org/f.ics", Color: "#73daca", Enabled: true}}
	app.direct.carddavList = []config.DAVAccountConfig{{ID: "dav1", Name: "Adressen", URL: "https://example.org/dav", Enabled: false}}
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 380, 760
		app.renderDirectCalendarTab(c)
	}, 380, 2400)
	tt.Frame()
	n := 0
	for _, txt := range tt.Texts() {
		if txt == "Synchronisieren" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want a sync switch per source, got %d: %v", n, tt.Texts())
	}
	app.updateCalendarSource("ical", "feed1", false, "")
	app.updateCalendarSource("ical", "feed1", true, "#f7768e")
	app.waitFor(t, "both updates", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return len(fd.calls) == 2
	})
	if got := strings.Join(fd.calls, " "); !strings.Contains(got, "ical/feed1/false/") || !strings.Contains(got, "ical/feed1/true/#f7768e") {
		t.Fatalf("calls = %s", got)
	}
}

// The archive dialog passes folder scope, subfolders and the mail limit through.
func TestArchiveRequestFolderScope(t *testing.T) {
	app := NewApp(Options{})
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", Path: "INBOX", Type: "inbox"},
		{ID: "f2", AccountID: "acc1", Path: "Projekte/2024"},
		{ID: "f3", AccountID: "acc1", Path: "Trash", Type: "trash"},
		{ID: "f4", AccountID: "acc2", Path: "Andere"},
	}
	app.arch = archiveUI{account: "Work", accountByLabel: map[string]string{"Work": "acc1"}, olderThanDays: "30",
		includeSub: false, maxMessages: "500", scope: 1, folder: "Projekte/2024"}
	if got := strings.Join(app.archiveFolderChoices(), ","); got != "INBOX,Projekte/2024" {
		t.Fatalf("folder choices = %q", got)
	}
	req := app.archiveRequest()
	if req.Scope != "folder" || req.FolderPath != "Projekte/2024" || req.IncludeSubfolders == nil || *req.IncludeSubfolders || req.MaxMessages != 500 {
		t.Fatalf("folder request = %+v", req)
	}
	app.arch.scope, app.arch.maxMessages = 2, "abc"
	if req = app.archiveRequest(); req.Scope != "all" || req.MaxMessages != defaultArchiveMax || req.FolderPath != "" {
		t.Fatalf("all-folders request = %+v", req)
	}
}

// Unticking "In KI-Sortierung einbeziehen" excludes the account from triage without taking
// away ordinary mail actions; ticking it again includes it.
func TestSettingsTriageAccountExclusion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	app, _, st := keymapApp(t)
	st.box.mu.Lock()
	st.box.cfg.Mailbox.ReadOnly = false
	st.box.cfg.Mailbox.TriageAccountIDs = []string{"acc1", "acc2"}
	st.box.cfg.Direct.Accounts = []config.DirectAccount{{ID: "acc1", Name: "Work"}, {ID: "acc2", Name: "Privat"}}
	st.box.mu.Unlock()

	app.OpenSettings()
	if len(app.settings.triageAccounts) != 2 || !app.settings.triageAccounts[1].include {
		t.Fatalf("triage rows = %+v", app.settings.triageAccounts)
	}
	app.settings.triageAccounts[1].include = false
	if !app.SaveSettings() {
		t.Fatal(app.settings.err)
	}
	st.box.mu.Lock()
	mb := st.box.cfg.Mailbox
	st.box.mu.Unlock()
	if mb.TriageIncludes("acc2") || !mb.TriageIncludes("acc1") || !mb.CanWrite("acc2") {
		t.Fatalf("policy after save: %+v", mb)
	}
}

func TestFormatCalendarHintBusySlotsOnly(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, loc)
	end := now.AddDate(0, 0, 14)
	events := []backend.CalendarEvent{
		{CalendarID: "c1", Title: "Planung   mit Team", Start: "2026-11-24T10:00:00+01:00", End: "2026-11-24T11:30:00+01:00", Attendees: []backend.EventAttendee{{Email: "a@example.org"}}, Location: "Raum 1"},
		{CalendarID: "c1", Title: "Urlaub", Start: "2026-11-26", End: "2026-11-28", AllDay: true},
		{CalendarID: "c1", Title: "Gestern", Start: "2026-11-22T10:00:00+01:00", End: "2026-11-22T11:00:00+01:00"},
		{CalendarID: "c1", Title: "Abgesagt", Start: "2026-11-25T10:00:00+01:00", End: "2026-11-25T11:00:00+01:00", ResponseStatus: "declined"},
		{CalendarID: "hidden", Title: "Privat", Start: "2026-11-25T12:00:00+01:00", End: "2026-11-25T13:00:00+01:00"},
		{CalendarID: "c1", Title: strings.Repeat("x", 80), Start: "2026-11-30T08:00:00+01:00", End: "2026-11-30T09:00:00+01:00"},
	}
	hint := formatCalendarHint(events, map[string]bool{"hidden": true}, now, end)
	for _, want := range []string{"2026-11-24 Tue 10:00–11:30 busy", "2026-11-26 Thu – 2026-11-27 Fri all day busy", "2026-11-30 Mon 08:00–09:00 busy"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint misses %q:\n%s", want, hint)
		}
	}
	for _, leak := range []string{"Planung", "Urlaub", "Gestern", "Abgesagt", "Privat", "a@example.org", "Raum 1", "xxx"} {
		if strings.Contains(hint, leak) {
			t.Fatalf("hint must not contain %q:\n%s", leak, hint)
		}
	}
	if formatCalendarHint(nil, nil, now, end) != "" {
		t.Fatal("no events, no hint")
	}
}
