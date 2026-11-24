package nativeui

import (
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/mailengine"
	"github.com/diver80/maildiver/internal/triage"
)

// parityRPC is an engine with three mails in pages of two: messages.get answers any id,
// archive/trash hide the mail.
type parityRPC struct {
	mu     sync.Mutex
	hidden map[string]bool
	calls  []string
}

func (*parityRPC) Connected() bool { return true }

func (s *parityRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method)
	if s.hidden == nil {
		s.hidden = map[string]bool{}
	}
	now := time.Now().UTC()
	all := []string{"m1", "m2", "m3"}
	header := func(i int, id string) map[string]any {
		return map[string]any{
			"id": id, "accountId": "acc1", "folderId": "f1", "folder": "INBOX",
			"subject": "Mail " + id, "from": "Max Muster <max.muster@example.org>",
			"date": now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
		}
	}
	switch method {
	case "messages.list":
		var visible []map[string]any
		for i, id := range all {
			if !s.hidden[id] {
				visible = append(visible, header(i, id))
			}
		}
		page, next := visible, ""
		if cur, _ := p["cursor"].(string); cur == "p2" {
			page = visible[min(2, len(visible)):]
		} else if len(visible) > 2 {
			page, next = visible[:2], "p2"
		}
		b, _ := json.Marshal(map[string]any{"messages": page, "total": len(visible), "nextCursor": next})
		return b, nil
	case "messages.get":
		id, _ := p["id"].(string)
		b, _ := json.Marshal(header(0, id))
		return b, nil
	case "messages.actions":
		id, _ := p["id"].(string)
		if arch, _ := p["archive"].(bool); arch {
			s.hidden[id] = true
		}
		if tr, _ := p["trash"].(bool); tr {
			s.hidden[id] = true
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(`{}`), nil
}

type parityApp struct {
	*App
	rpc *parityRPC
	box *cfgBox
	ui  chan func()
	ai  []AlertItem
}

func newParityApp(t *testing.T, direct DirectController, edit func(*config.Config)) *parityApp {
	t.Helper()
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	if edit != nil {
		edit(&cfg)
	}
	box := newCfgBox(cfg)
	rpc := &parityRPC{}
	bc := backend.New(backend.Options{Engine: rpc, Events: events.NewHub(), Store: triage.NewStoreAt(t.TempDir()), LoadConfig: box.load, Version: "test"})
	app := NewApp(Options{ConfigLoader: box.load, ConfigSaver: box.save, Backend: bc, Direct: direct, Store: triage.NewStoreAt(t.TempDir())})
	pa := &parityApp{App: app, rpc: rpc, box: box, ui: make(chan func(), 256)}
	app.SetHooks(func(fn func()) { pa.ui <- fn }, nil, nil, nil, func(al AlertItem) { pa.ai = append(pa.ai, al) })
	app.winW, app.winH = 380, 760
	return pa
}

func (a *parityApp) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for drained := false; !drained; {
			select {
			case fn := <-a.ui:
				fn()
			default:
				drained = true
			}
		}
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (a *parityApp) phoneTester() *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		a.winW, a.winH = 380, 760
		a.renderPhone(c)
	}, 380, 760)
}

func mustClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (%v)", label, err, tt.Texts())
	}
	tt.Frame()
}

func longPress(tt *ui.Tester, r ui.Rect) {
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.FingerDown(x, y)
	swipeSettle(tt, 600*time.Millisecond)
	tt.FingerUp(x, y)
	swipeSettle(tt, 400*time.Millisecond)
}

func listed(a *App, id string) bool {
	for _, m := range a.messages {
		if m.ID == id {
			return true
		}
	}
	return false
}

// B1: the mail screen steps through the list (loading its next page at the end) and, after
// archiving, opens the next mail – the list once there is none.
func TestPhoneMailStepsAndOpensNextAfterArchive(t *testing.T) {
	app := newParityApp(t, nil, nil)
	ctx := context.Background()
	app.engineReady = true
	app.setNav("recent")
	app.LoadMessages(ctx)
	app.waitFor(t, "first page", func() bool { return len(app.messages) == 2 && app.nextCursor != "" })
	r := app.phoneRouter()
	r.Push(phoneRouteList)
	app.SelectMessage(ctx, "m1")
	r.Push(phoneRouteMail)
	tt := app.phoneTester()
	opened := func(id string) func() bool {
		return func() bool {
			tt.Frame()
			return app.selectedMsg != nil && app.selectedMsg.ID == id && r.Path() == phoneRouteMail
		}
	}
	app.waitFor(t, "m1 open", opened("m1"))

	mustClick(t, tt, "Nächste Mail")
	app.waitFor(t, "m2 open", opened("m2"))
	mustClick(t, tt, "Nächste Mail") // the end of the first page: loads the next one, then steps
	app.waitFor(t, "m3 open from the second page", opened("m3"))
	mustClick(t, tt, "Vorherige Mail")
	app.waitFor(t, "back to m2", opened("m2"))

	mustClick(t, tt, "Archivieren")
	app.waitFor(t, "m3 open after archiving m2", opened("m3"))
	app.waitFor(t, "m2 gone from the list", func() bool { return !listed(app.App, "m2") && !app.loading })
	mustClick(t, tt, "Archivieren")
	app.waitFor(t, "m1 open after archiving the last mail", opened("m1"))
	app.waitFor(t, "m3 gone from the list", func() bool { return !listed(app.App, "m3") && !app.loading })
	mustClick(t, tt, "Archivieren")
	app.waitFor(t, "the list after archiving the only mail", func() bool {
		tt.Frame()
		return r.Path() == phoneRouteList
	})
}

// B1: a mail swiped away in the list never opens its neighbour behind the list.
func TestRemovedMailOpensNeighbourOnlyOnMailScreen(t *testing.T) {
	app := newParityApp(t, nil, nil)
	app.messages = []backend.MessageHeader{{ID: "m1"}, {ID: "m2"}}
	app.phoneRouter().Push(phoneRouteList)
	app.afterOpenMailRemoved(context.Background(), "m2")
	if app.selectedID != "" {
		t.Fatalf("opened %q behind the list", app.selectedID)
	}
	app.phoneRouter().Push(phoneRouteMail)
	app.afterOpenMailRemoved(context.Background(), "m2")
	if app.selectedID != "m2" || app.phoneRouter().Path() != phoneRouteMail {
		t.Fatalf("mail screen: selected %q path %q", app.selectedID, app.phoneRouter().Path())
	}
}

// B3: the phone's mail sheet offers the contact extraction.
func TestPhoneMoreSheetExtractsContacts(t *testing.T) {
	app := newParityApp(t, nil, nil)
	app.selectedMsg = &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "m1", Subject: "Hallo", From: "Max Muster <max.muster@example.org>"}}
	app.selectedID = "m1"
	app.phone.router = ui.NewRouter(phoneRouteMail)
	app.phone.more = true
	tt := app.phoneTester()
	tt.Frame()
	if !tt.HasText("KI: Kontakte extrahieren") {
		t.Fatalf("no contact extraction in the sheet: %v", tt.Texts())
	}
}

// B2: the mailboxes lead to the AI assistant, and the daily briefing starts it.
func TestPhoneAssistantAndBriefing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmp-1", "object": "chat.completion", "model": "gpt-4o-mini",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "## Briefing\n\n- Nichts Dringendes."}}},
		})
	}))
	defer srv.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newParityApp(t, nil, func(cfg *config.Config) {
		cfg.AI.Agent = config.ProviderConfig{Backend: "openai", Model: "gpt-4o-mini", Endpoint: srv.URL + "/v1"}
	})
	app.engineReady = true
	tt := app.phoneTester()
	tt.Frame()
	mustClick(t, tt, "KI-Assistent")
	swipeSettle(tt, 300*time.Millisecond)
	if app.phoneRouter().Path() != phoneRouteAssistant || !tt.HasText("Action-Mails") || !tt.HasText("Tagesbriefing") {
		t.Fatalf("assistant screen: path %q, texts %v", app.phoneRouter().Path(), tt.Texts())
	}
	app.phoneRouter().Back()
	swipeSettle(tt, 300*time.Millisecond)
	mustClick(t, tt, "Tagesbriefing")
	app.waitFor(t, "the briefing's answer", func() bool {
		tt.Frame()
		return len(app.chatEntries) == 2 && !app.chatStreaming
	})
	if app.phoneRouter().Path() != phoneRouteAssistant || !strings.Contains(app.chatEntries[0].Content, "Tages-Briefing") {
		t.Fatalf("briefing: path %q entries %+v", app.phoneRouter().Path(), app.chatEntries)
	}
	tt.Frame()
	if !tt.HasText("Nichts Dringendes.") {
		t.Fatalf("answer not shown: %v", tt.Texts())
	}
}

// folderDirect records the folder calls of the direct engine.
type folderDirect struct {
	DirectController
	mu      sync.Mutex
	renamed []string
	deleted []string
}

func (f *folderDirect) RenameFolder(_ context.Context, _, folderID, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renamed = append(f.renamed, folderID+"→"+newName)
	return nil
}

func (f *folderDirect) DeleteFolder(_ context.Context, _, folderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, folderID)
	return nil
}

func (f *folderDirect) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.renamed), len(f.deleted)
}

// B4: a long press on a user folder renames it (in a dialog) or deletes it (after confirming).
func TestPhoneFolderLongPressRenamesAndDeletes(t *testing.T) {
	fd := &folderDirect{}
	app := newParityApp(t, fd, nil)
	app.engineReady = true
	app.accounts = []backend.Account{{ID: "acc1", Name: "Arbeit"}}
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", AccountName: "Arbeit", Name: "Posteingang", Path: "INBOX", Type: "inbox"},
		{ID: "f9", AccountID: "acc1", AccountName: "Arbeit", Name: "Projekte", Path: "Projekte"},
	}
	tt := app.phoneTester()
	tt.Frame()

	inbox, ok := tt.Find("Posteingang")
	if !ok {
		t.Fatalf("no inbox row: %v", tt.Texts())
	}
	longPress(tt, inbox)
	if app.phone.folderSheet {
		t.Fatal("the inbox has no rename/delete")
	}

	mustClick(t, tt, "Weitere Ordner (1)")
	row, ok := tt.Find("Projekte")
	if !ok {
		t.Fatalf("no folder row: %v", tt.Texts())
	}
	longPress(tt, row)
	if !tt.HasText("Umbenennen…") {
		t.Fatalf("no folder sheet: %v", tt.Texts())
	}
	mustClick(t, tt, "Umbenennen…")
	swipeSettle(tt, 400*time.Millisecond)
	if !app.phone.folderRename || app.phone.folderNewName != "Projekte" {
		t.Fatalf("rename dialog: open %v name %q", app.phone.folderRename, app.phone.folderNewName)
	}
	assertNoClipping(t, tt, 380, "rename dialog")
	app.phone.folderNewName = "Projekte 2026"
	tt.Frame()
	mustClick(t, tt, "Umbenennen")
	app.waitFor(t, "rename", func() bool { n, _ := fd.counts(); return n == 1 })
	if fd.renamed[0] != "f9→Projekte 2026" {
		t.Fatalf("renamed %v", fd.renamed)
	}
	app.waitFor(t, "rename done", func() bool { return !app.direct.newFolderBusy })

	swipeSettle(tt, 400*time.Millisecond)
	row, _ = tt.Find("Projekte")
	longPress(tt, row)
	mustClick(t, tt, "Löschen…")
	swipeSettle(tt, 400*time.Millisecond)
	if _, n := fd.counts(); n != 0 {
		t.Fatal("deleted without confirmation")
	}
	if !tt.HasText("Ordner „Projekte“ löschen?") {
		t.Fatalf("no confirmation: %v", tt.Texts())
	}
	mustClick(t, tt, "Ordner löschen")
	app.waitFor(t, "delete", func() bool { _, n := fd.counts(); return n == 1 })
	if fd.deleted[0] != "f9" {
		t.Fatalf("deleted %v", fd.deleted)
	}
}

// B7: back from a contact goes to the contact list, back with mails checked clears them – the
// list screen stays in both, and the next back leaves it.
func TestPhoneBackLeavesContactAndSelectionFirst(t *testing.T) {
	app := newParityApp(t, nil, nil)
	app.engineReady = true
	app.setNav("contacts")
	app.contacts.loaded = true
	app.contacts.list = []backend.ContactRecord{{ID: "c1", DisplayName: "Max Muster", Email: "max.muster@example.org"}}
	r := app.phoneRouter()
	r.Push(phoneRouteList)
	tt := app.phoneTester()
	tt.Frame()
	mustClick(t, tt, "Max Muster")
	tt.Frame()
	if app.contacts.selectedID != "c1" || r.Query(phoneDetailQuery) != "contact" {
		t.Fatalf("contact open: %q, query %q", app.contacts.selectedID, r.Location())
	}
	tt.Key(0, ui.KeyBack)
	swipeSettle(tt, 300*time.Millisecond)
	if app.contacts.selectedID != "" || r.Path() != phoneRouteList {
		t.Fatalf("back from the contact: selected %q at %q", app.contacts.selectedID, r.Location())
	}
	tt.Key(0, ui.KeyBack)
	swipeSettle(tt, 300*time.Millisecond)
	if r.Path() != phoneRouteMailboxes {
		t.Fatalf("second back: %q", r.Location())
	}

	// Mails checked: back clears them first.
	app.setNav("recent")
	app.messages = []backend.MessageHeader{{ID: "m1", Subject: "Mail m1", From: "Max Muster <max.muster@example.org>"}, {ID: "m2", Subject: "Mail m2"}}
	r.Push(phoneRouteList)
	swipeSettle(tt, 300*time.Millisecond)
	row, ok := tt.Find("Mail m1")
	if !ok {
		t.Fatalf("no mail row: %v", tt.Texts())
	}
	longPress(tt, row)
	if len(app.CheckedMessageIDs()) != 1 || r.Query(phoneDetailQuery) != "select" {
		t.Fatalf("checked %v at %q", app.CheckedMessageIDs(), r.Location())
	}
	tt.Key(0, ui.KeyBack)
	swipeSettle(tt, 300*time.Millisecond)
	if len(app.CheckedMessageIDs()) != 0 || r.Path() != phoneRouteList {
		t.Fatalf("back with mails checked: %v at %q", app.CheckedMessageIDs(), r.Location())
	}

	// Cleared by the bar's ✕, the selection's history entry goes too.
	longPress(tt, row)
	mustClick(t, tt, "Auswahl aufheben")
	tt.Frame()
	if r.Location() != phoneRouteList || len(app.CheckedMessageIDs()) != 0 {
		t.Fatalf("after ✕: %q, checked %v", r.Location(), app.CheckedMessageIDs())
	}
	r.Back()
	tt.Frame()
	if r.Path() != phoneRouteMailboxes {
		t.Fatalf("back after ✕ leaves the list, got %q", r.Location())
	}
}

// B5: reminders follow each event's setting; the background's notifications respect the switch.
func TestEventRemindersPerEvent(t *testing.T) {
	app := newParityApp(t, nil, nil)
	app.notifyEnabled = true
	now := time.Now()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	app.events = []backend.CalendarEvent{
		{ID: "e1", Title: "Default bald", Start: at(8 * time.Minute)},
		{ID: "e2", Title: "Default später", Start: at(25 * time.Minute)},
		{ID: "e3", Title: "Halbe Stunde", Start: at(25 * time.Minute), ReminderMinutes: 30},
		{ID: "e4", Title: "Keine", Start: at(3 * time.Minute), ReminderMinutes: backend.ReminderNone},
	}
	app.checkUpcomingMeetingReminders()
	var got []string
	for _, al := range app.ai {
		got = append(got, al.Title)
	}
	if strings.Join(got, ",") != "Default bald,Halbe Stunde" {
		t.Fatalf("reminded %v", got)
	}
	app.checkUpcomingMeetingReminders()
	if len(app.ai) != 2 {
		t.Fatalf("reminded twice: %d", len(app.ai))
	}

	notes := app.backgroundEventNotifications(app.events[:1], now)
	if len(notes) != 1 || notes[0].ID != alertCalPrefix+"e1" || !strings.HasPrefix(notes[0].Subtitle, "In 8 Min") {
		t.Fatalf("notification %+v", notes)
	}
	cfg, _ := app.box.load()
	off := false
	cfg.App.NotifyEnabled = &off
	_ = app.box.save(cfg)
	if _, on := app.backgroundConfig(); on {
		t.Fatal("the background must see notifications switched off")
	}
}

// eventDirect is a direct engine whose calendars keep reminders.
type eventDirect struct{ DirectController }

// B5/B6: the event form picks dates and times, offers the reminder, chooses the repeat from a
// drop-down on phones and refuses an end before the start.
func TestPhoneEventFormPickersReminderAndValidation(t *testing.T) {
	app := newParityApp(t, &eventDirect{}, nil)
	app.calendars = []backend.Calendar{{ID: mailengine.LocalCalendarID, Name: "Persönlich", Type: "local"}}
	app.eventCalLabels = []string{"Persönlich"}
	app.eventCalByLabel = map[string]string{"Persönlich": mailengine.LocalCalendarID}
	app.eventCalendar = "Persönlich"
	app.OpenNewEventModal(nil)
	app.eventTitle = "Planung"
	app.eventRecurrence = 4
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 380, 760
		app.renderEventModal(c)
	}, 380, 760)
	tt.Frame()
	for _, want := range []string{"Jährlich", "Beginn", "Ende", "10 Minuten vorher"} {
		if !tt.HasText(want) {
			t.Fatalf("event form lacks %q: %v", want, tt.Texts())
		}
	}
	assertNoClipping(t, tt, 380, "event form")

	app.eventStart, app.eventEnd = "2026-11-24T10:00", "2026-11-24T09:30"
	tt.Frame()
	if !tt.HasText("Das Ende muss nach dem Beginn liegen.") {
		t.Fatalf("no inline error: %v", tt.Texts())
	}
	app.SaveCalendarEvent(context.Background())
	if app.eventBusy {
		t.Fatal("an end before the start must not be saved")
	}
	app.eventEnd = "2026-11-24T11:00"
	tt.Frame()
	if tt.HasText("Das Ende muss nach dem Beginn liegen.") {
		t.Fatal("the error stays after fixing the end")
	}
}

// assertNoClipping fails for any text reaching past the window's sides.
func assertNoClipping(t *testing.T, tt *ui.Tester, w float32, what string) {
	t.Helper()
	for _, s := range tt.Texts() {
		r, ok := tt.Find(s)
		if !ok || r.W <= 0 {
			continue
		}
		if r.X < -0.5 || r.X+r.W > w+0.5 {
			t.Errorf("%s: %q spans x=%.0f…%.0f, outside 0…%.0f", what, s, r.X, r.X+r.W, w)
		}
	}
}

// B6: the accounts dialog, the rule editor and the event form fit a 380-point phone; with
// MAILDIVER_RENDER set, their screenshots are written there.
func TestPhoneDialogsFitAt380(t *testing.T) {
	app := newParityApp(t, &eventDirect{}, nil)
	app.engineReady = true
	d := &app.direct
	d.open = true
	d.accounts = []mailengine.AccountSyncStatus{
		{AccountID: "acc1", Name: "Arbeit", Email: "max.muster@example.org", State: "idle", MessageCount: 1234},
		{AccountID: "acc2", Name: "Privat mit langem Kontonamen", Email: "max.muster.privat@example.org", State: "error", LastError: "Anmeldung fehlgeschlagen"},
	}
	shots := os.Getenv("MAILDIVER_RENDER")
	shot := func(name string, view func(*ui.Context)) {
		t.Helper()
		tt := ui.NewTester(func(c *ui.Context) {
			app.winW, app.winH = 380, 800
			view(c)
		}, 380, 800)
		tt.Frame()
		tt.Advance(120 * time.Millisecond)
		tt.Frame()
		assertNoClipping(t, tt, 380, name)
		if shots == "" {
			return
		}
		tt.SetScale(2)
		tt.Frame()
		f, err := os.Create(filepath.Join(shots, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	d.tab = 0
	shot("maildiver-phone-accounts.png", app.renderDirectModal)

	d.tab = 2
	d.ruleEditOpen = true
	d.ruleEditName = "Rechnungen"
	d.ruleEditMatchMode = 1
	d.ruleEditConds = []triage.NativeCondition{
		{Field: "from", Op: "contains", Value: "rechnung@example.org"},
		{Field: "header", HeaderName: "List-Id", Op: "contains", Value: "billing.example.org"},
	}
	d.ruleEditMove = true
	d.ruleEditMoveFolder = "INBOX/Rechnungen"
	shot("maildiver-phone-rule-editor.png", app.renderDirectModal)
	app.widgets.directScroll.Y = 900 // the editor's actions further down
	shot("maildiver-phone-rule-editor-actions.png", app.renderDirectModal)
	d.open = false

	app.calendars = []backend.Calendar{{ID: mailengine.LocalCalendarID, Name: "Persönlich", Type: "local"}}
	app.eventCalLabels = []string{"Persönlich"}
	app.eventCalByLabel = map[string]string{"Persönlich": mailengine.LocalCalendarID}
	app.eventCalendar = "Persönlich"
	app.OpenNewEventModal(nil)
	app.eventTitle = "Quartalsplanung"
	app.eventRecurrence = 4
	shot("maildiver-phone-event-form.png", app.renderEventModal)
}
