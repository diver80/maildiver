package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/triage"
)

func swipeSettle(tt *ui.Tester, d time.Duration) {
	for end := tt.Now().Add(d); tt.Now().Before(end); {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
	tt.Frame()
}

func swipe(tt *ui.Tester, y, from, to float32) {
	tt.FingerDown(from, y)
	swipeSettle(tt, 16*time.Millisecond)
	step := float32(20)
	if to < from {
		step = -20
	}
	for x := from + step; (step > 0 && x <= to) || (step < 0 && x >= to); x += step {
		tt.FingerMove(x, y)
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
	swipeSettle(tt, 200*time.Millisecond)
	tt.FingerUp(to, y)
	swipeSettle(tt, 2*time.Second)
}

// Swiped right and let go, a mail row runs its read action and slides back; swiped left it
// opens on its actions, and opening another row closes it.
func TestPhoneSwipeRow(t *testing.T) {
	a := &App{}
	var read []string
	archived := ""
	var ls ui.ListState
	ids := []string{"m0", "m1", "m2"}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.List(c, &ls, len(ids), func(i int) {
			id := ids[i]
			a.phoneSwipeRow(c.Key(id), id, &phoneSwipeAction{label: "Gelesen", run: func() { read = append(read, id) }},
				[]phoneSwipeAction{{label: "Löschen", destructive: true, run: func() {}}, {label: "Archiv", run: func() { archived = id }}},
				func() { ui.Column(c).FillWidth().Height(60).Children(func() { ui.Text(c, fmt.Sprint("Mail ", i)) }) })
		}).Fill()
	}, 360, 400)
	tt.Frame()

	swipe(tt, 30, 20, 220)
	if fmt.Sprint(read) != "[m0]" {
		t.Fatalf("a right swipe ran %v", read)
	}
	if p := a.phone.swipes.offset("m0"); p.X != 0 {
		t.Fatalf("the row stayed aside: %+v", *p)
	}
	swipe(tt, 90, 20, 220)
	if fmt.Sprint(read) != "[m0 m1]" {
		t.Fatalf("a second right swipe ran %v", read)
	}

	swipe(tt, 30, 340, 120)
	if a.phone.swipes.open != "m0" || a.phone.swipes.offset("m0").X >= 0 {
		t.Fatalf("a left swipe did not open the row: open=%q %+v", a.phone.swipes.open, *a.phone.swipes.offset("m0"))
	}
	swipe(tt, 150, 340, 120)
	swipeSettle(tt, 500*time.Millisecond)
	if a.phone.swipes.open != "m2" || a.phone.swipes.offset("m0").X != 0 {
		t.Fatalf("opening another row left the first open: open=%q m0=%+v", a.phone.swipes.open, *a.phone.swipes.offset("m0"))
	}

	tt.FingerDown(320, 150)
	tt.FingerUp(320, 150)
	swipeSettle(tt, 100*time.Millisecond)
	if archived != "m2" || a.phone.swipes.open != "" {
		t.Fatalf("tapping Archiv: archived=%q open=%q", archived, a.phone.swipes.open)
	}
}

type phoneTestRPC struct {
	mu      sync.Mutex
	actions []map[string]any
	hidden  map[string]bool
	read    map[string]bool
}

func (*phoneTestRPC) Connected() bool { return true }

func (s *phoneTestRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "messages.actions":
		raw, _ := json.Marshal(params)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		s.mu.Lock()
		if s.hidden == nil {
			s.hidden = map[string]bool{}
		}
		if s.read == nil {
			s.read = map[string]bool{}
		}
		s.actions = append(s.actions, req)
		id, _ := req["id"].(string)
		if arch, _ := req["archive"].(bool); arch && id != "" {
			s.hidden[id] = true
		}
		if trash, _ := req["trash"].(bool); trash && id != "" {
			s.hidden[id] = true
		}
		if r, ok := req["read"].(bool); ok && id != "" {
			s.read[id] = r
		}
		s.mu.Unlock()
		return json.RawMessage(`{"ok":true}`), nil
	case "messages.list":
		today := time.Now().UTC()
		all := []struct {
			id, subj, from string
			unread         bool
			dt             time.Time
		}{
			{"m1", "Erste Mail", "Alice <alice@example.com>", true, today},
			{"m2", "Zweite Mail", "Bob <bob@example.com>", true, today.Add(-time.Minute)},
			{"m3", "Dritte Mail", "Carol <carol@example.com>", false, today.Add(-2 * time.Minute)},
		}
		s.mu.Lock()
		var msgs []map[string]any
		for _, m := range all {
			if s.hidden[m.id] {
				continue
			}
			unread := m.unread
			if r, ok := s.read[m.id]; ok {
				unread = !r
			}
			msgs = append(msgs, map[string]any{
				"id":        m.id,
				"accountId": "acc1",
				"folderId":  "f1",
				"folder":    "INBOX",
				"subject":   m.subj,
				"from":      m.from,
				"date":      m.dt.Format(time.RFC3339),
				"unread":    unread,
			})
		}
		s.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"messages": msgs, "total": len(msgs)})
		return b, nil
	}
	return json.RawMessage(`{}`), nil
}

func (s *phoneTestRPC) actionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.actions)
}

func (s *phoneTestRPC) resetVisible() {
	s.mu.Lock()
	s.hidden = map[string]bool{}
	s.mu.Unlock()
}

func (s *phoneTestRPC) takeActions() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]map[string]any(nil), s.actions...)
	s.actions = nil
	return out
}

func newPhoneTestApp(t *testing.T) (*App, *phoneTestRPC, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	rpc := &phoneTestRPC{}
	bc := backend.New(backend.Options{
		Engine:     rpc,
		Events:     events.NewHub(),
		Store:      triage.NewStoreAt(t.TempDir()),
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Version:    "test",
	})
	uiQueue := make(chan func(), 64)
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		Backend:      bc,
	})
	app.SetHooks(func(fn func()) { uiQueue <- fn }, nil, nil, nil, nil)
	drain := func() {
		for {
			select {
			case fn := <-uiQueue:
				if fn != nil {
					fn()
				}
			default:
				return
			}
		}
	}
	app.winW = 360
	app.winH = 740
	return app, rpc, drain
}

func TestPhoneMultiSelectAndBulkActions(t *testing.T) {
	app, rpc, drain := newPhoneTestApp(t)
	today := time.Now().UTC()
	app.messages = []backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Erste Mail", From: "Alice <alice@example.com>", Date: today.Format(time.RFC3339), Unread: true},
		{ID: "m2", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Zweite Mail", From: "Bob <bob@example.com>", Date: today.Add(-time.Minute).Format(time.RFC3339), Unread: true},
		{ID: "m3", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Dritte Mail", From: "Carol <carol@example.com>", Date: today.Add(-2 * time.Minute).Format(time.RFC3339), Unread: false},
	}
	app.phoneRouter().Push(phoneRouteList)

	tt := ui.NewTester(func(c *ui.Context) {
		app.winW = 360
		app.winH = 740
		app.renderPhone(c)
	}, 360, 740)
	tt.Frame()

	r1, ok := tt.Find("Erste Mail")
	if !ok {
		t.Fatal("first mail row not rendered")
	}
	cx, cy := r1.X+r1.W/2, r1.Y+r1.H/2
	tt.FingerDown(cx, cy)
	swipeSettle(tt, 600*time.Millisecond)
	tt.FingerUp(cx, cy)
	tt.Frame()

	if ids := app.CheckedMessageIDs(); fmt.Sprint(ids) != "[m1]" {
		t.Fatalf("long-press checked %v, want [m1]", ids)
	}
	if !tt.HasText("1 ausgewählt") {
		t.Fatalf("missing selection title in %v", tt.Texts())
	}
	for _, want := range []string{"Stern", "Gelesen", "Archiv", "Löschen"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("missing bulk bar button %q", want)
		}
	}

	// Tap second row to add to selection (must NOT push mail detail route).
	r2, ok := tt.Find("Zweite Mail")
	if !ok {
		t.Fatal("second mail row not rendered")
	}
	cx2, cy2 := r2.X+r2.W/2, r2.Y+r2.H/2
	tt.FingerDown(cx2, cy2)
	swipeSettle(tt, 20*time.Millisecond)
	tt.FingerUp(cx2, cy2)
	tt.Frame()

	if ids := app.CheckedMessageIDs(); len(ids) != 2 {
		t.Fatalf("after tapping 2nd row checked=%v, want 2", ids)
	}
	if !tt.HasText("2 ausgewählt") {
		t.Fatalf("tapping row in selection mode did not stay in list: %v", tt.Texts())
	}

	// Tap "Gelesen" -> dispatches read batch action and exits selection mode.
	readRect, ok := tt.Find("Gelesen")
	if !ok {
		t.Fatal("Gelesen button not found")
	}
	tt.FingerDown(readRect.X+readRect.W/2, readRect.Y+readRect.H/2)
	swipeSettle(tt, 20*time.Millisecond)
	tt.FingerUp(readRect.X+readRect.W/2, readRect.Y+readRect.H/2)
	tt.Frame()

	deadline := time.Now().Add(2 * time.Second)
	for rpc.actionCount() < 2 && time.Now().Before(deadline) {
		drain()
		time.Sleep(10 * time.Millisecond)
	}
	drain()
	tt.Frame()
	if acts := rpc.takeActions(); len(acts) != 2 {
		t.Fatalf("Gelesen dispatched %d actions, want 2: %+v", len(acts), acts)
	}
	if len(app.CheckedMessageIDs()) != 0 {
		t.Fatalf("selection still active after Gelesen: %v", app.CheckedMessageIDs())
	}

	// Re-enter via long-press + Select All ("Alle") -> tap "Archiv" -> exits selection & sends Archive.
	r1, _ = tt.Find("Erste Mail")
	tt.FingerDown(r1.X+r1.W/2, r1.Y+r1.H/2)
	swipeSettle(tt, 600*time.Millisecond)
	tt.FingerUp(r1.X+r1.W/2, r1.Y+r1.H/2)
	tt.Frame()
	if err := tt.Click("Alle"); err != nil {
		t.Fatalf("Click Alle: %v", err)
	}
	tt.Frame()
	if len(app.CheckedMessageIDs()) != 3 {
		t.Fatalf("Select All checked %d, want 3", len(app.CheckedMessageIDs()))
	}
	archRect, ok := tt.Find("Archiv")
	if !ok {
		t.Fatal("Archiv button not found")
	}
	tt.FingerDown(archRect.X+archRect.W/2, archRect.Y+archRect.H/2)
	swipeSettle(tt, 20*time.Millisecond)
	tt.FingerUp(archRect.X+archRect.W/2, archRect.Y+archRect.H/2)
	tt.Frame()
	deadline = time.Now().Add(2 * time.Second)
	for rpc.actionCount() < 3 && time.Now().Before(deadline) {
		drain()
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	drain()
	acts := rpc.takeActions()
	if len(acts) != 3 {
		t.Fatalf("Archiv dispatched %d actions, want 3", len(acts))
	}
	for _, act := range acts {
		if arch, _ := act["archive"].(bool); !arch {
			t.Fatalf("expected archive=true, got %+v", act)
		}
	}

	// Reset mocked visibility and verify 2-tap confirmation for "Löschen" when 2 rows are checked.
	rpc.resetVisible()
	app.messages = []backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Erste Mail", From: "Alice <alice@example.com>", Date: today.Format(time.RFC3339), Unread: true},
		{ID: "m2", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Zweite Mail", From: "Bob <bob@example.com>", Date: today.Add(-time.Minute).Format(time.RFC3339), Unread: true},
	}
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(1, true, false)
	tt.Frame()
	delRect, ok := tt.Find("Löschen")
	if !ok {
		t.Fatal("Löschen button not found")
	}
	tt.FingerDown(delRect.X+delRect.W/2, delRect.Y+delRect.H/2)
	swipeSettle(tt, 20*time.Millisecond)
	tt.FingerUp(delRect.X+delRect.W/2, delRect.Y+delRect.H/2)
	tt.Frame()
	drain()
	if got := rpc.takeActions(); len(got) != 0 {
		t.Fatalf("first tap on Löschen should only arm confirmation, ran %+v", got)
	}
	tt.FingerDown(delRect.X+delRect.W/2, delRect.Y+delRect.H/2)
	swipeSettle(tt, 20*time.Millisecond)
	tt.FingerUp(delRect.X+delRect.W/2, delRect.Y+delRect.H/2)
	tt.Frame()
	deadline = time.Now().Add(2 * time.Second)
	for rpc.actionCount() < 2 && time.Now().Before(deadline) {
		drain()
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	drain()
	if got := rpc.takeActions(); len(got) != 2 {
		t.Fatalf("second tap on Löschen dispatched %d actions, want 2", len(got))
	}

	// Verify clear selection button resets selection mode cleanly.
	rpc.resetVisible()
	app.messages = []backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", FolderID: "f1", Folder: "INBOX", Subject: "Erste Mail", From: "Alice <alice@example.com>", Date: today.Format(time.RFC3339), Unread: true},
	}
	app.SetMailChecked(0, true, false)
	tt.Frame()
	if err := tt.Click("Auswahl aufheben"); err != nil {
		t.Fatalf("Click clear selection: %v", err)
	}
	tt.Frame()
	if len(app.CheckedMessageIDs()) != 0 {
		t.Fatalf("clear selection left %v", app.CheckedMessageIDs())
	}
}

func TestPhoneContactsAndCalendarRender(t *testing.T) {
	app, _, _ := newPhoneTestApp(t)
	app.contacts.loaded = true
	app.contacts.list = []backend.ContactRecord{
		{ID: "ct1", DisplayName: "Alice Example", Email: "alice@example.com", Company: "Example Org"},
	}
	app.navID = "contacts"
	app.phoneRouter().Push(phoneRouteList)

	tt := ui.NewTester(func(c *ui.Context) {
		app.winW = 360
		app.winH = 740
		app.renderPhone(c)
	}, 360, 740)
	tt.Frame()
	if !tt.HasText("Alice Example") {
		t.Fatalf("phone contacts list did not render Alice Example")
	}
	if tt.HasText("Kontakt bearbeiten") {
		t.Fatalf("phone contacts should hide detail editor before selection")
	}
	if err := tt.Click("Alice Example"); err != nil {
		t.Fatalf("Click contact: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Kontakt bearbeiten") {
		t.Fatalf("tapping contact on phone did not open detail editor")
	}
	if err := tt.Click("← Alle Kontakte"); err != nil {
		t.Fatalf("Click back to contacts: %v", err)
	}
	tt.Frame()
	if app.contacts.selectedID != "" {
		t.Fatalf("back button did not clear contact selection: %q", app.contacts.selectedID)
	}

	// Render Agenda, Week, and Month calendar modes at phone width without layout panic.
	now := time.Now()
	app.navID = "calendar"
	app.calCursor = now
	app.events = []backend.CalendarEvent{
		{
			ID:       "ev1",
			Title:    "Team Sync",
			Calendar: "Work",
			Start:    now.Format(time.RFC3339),
			End:      now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	for _, mode := range []int{0, 1, 2} {
		app.calViewMode = mode
		tt.Frame()
	}
}
