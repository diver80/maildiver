package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
)

func writesFor(st *testStack, key string) []map[string]any {
	_, params := st.rpc.snapshot()
	var out []map[string]any
	for _, p := range params {
		if _, ok := p[key]; ok {
			out = append(out, p)
		}
	}
	return out
}

// C1: the right-click menu acts on the row alone, or on the whole selection the row is in.
func TestMailRowContextMenuRowOrSelection(t *testing.T) {
	app, loop, st := keymapApp(t)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()

	if err := tt.RightClick("Zwei"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(tt.Menu(), "|")
	for _, want := range []string{"Antworten", "Allen antworten", "Weiterleiten", "Als ungelesen markieren", "Mit Stern markieren", "Archivieren", "Verschieben nach…", "Löschen", "Quick-Regel…"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("row menu lacks %q: %s", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("Archivieren"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(writesFor(st, "archive")) == 1 }) {
		t.Fatalf("archive from the menu expected, writes=%v", writesFor(st, "archive"))
	}
	if got := writesFor(st, "archive")[0]["id"]; got != "m2" {
		t.Fatalf("the menu must act on its row, archived %v", got)
	}

	// A row in the checked selection: the menu acts on all checked mails, without reply.
	loop.drain()
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", Folder: "INBOX", Subject: "Eins", Unread: true},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", Folder: "INBOX", Subject: "Zwei"},
		{ID: "m3", MessageID: "m3@x", AccountID: "acc1", Folder: "INBOX", Subject: "Drei"},
	})
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(2, true, false)
	tt.Frame()
	if err := tt.RightClick("Drei"); err != nil {
		t.Fatal(err)
	}
	if menu := strings.Join(tt.Menu(), "|"); strings.Contains(menu, "Antworten") {
		t.Fatalf("no reply for a selection: %s", menu)
	}
	if err := tt.ChooseMenuItem("Als gelesen markieren"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(writesFor(st, "read")) == 2 }) {
		t.Fatalf("read for both checked mails expected, got %v", writesFor(st, "read"))
	}
	for i, want := range []string{"m1", "m3"} {
		if p := writesFor(st, "read")[i]; p["id"] != want || p["read"] != true {
			t.Fatalf("batch read %d: %+v", i, p)
		}
	}
}

// C2: a mail row dragged onto a sidebar folder moves there; a plain click still opens it.
func TestDragMailRowOntoFolderMoves(t *testing.T) {
	app, loop, st := keymapApp(t)
	app.folders = []backend.Folder{
		{ID: "acc1::INBOX", AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "acc1::Projekte", AccountID: "acc1", Name: "Projekte", Path: "Projekte"},
	}
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", FolderID: "acc1::Projekte", Folder: "Projekte", Subject: "Eins"},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", FolderID: "acc1::Projekte", Folder: "Projekte", Subject: "Zwei"},
	})
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()

	if err := tt.Click("Eins"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.selectedID != "m1" {
		t.Fatalf("a click must still open the mail, open=%q", app.selectedID)
	}

	src, ok := tt.Find("Zwei")
	dst, ok2 := tt.Find("INBOX")
	if !ok || !ok2 {
		t.Fatalf("row or folder not found: %v", tt.Texts())
	}
	sx, sy := src.X+src.W/2, src.Y+src.H/2
	dx, dy := dst.X+dst.W/2, dst.Y+dst.H/2
	tt.Press(sx, sy)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(sx+(dx-sx)*f, sy+(dy-sy)*f)
		tt.Frame()
	}
	tt.Release(dx, dy)
	tt.Frame()
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(writesFor(st, "moveToFolderId")) == 1 }) {
		w, _ := st.rpc.snapshot()
		t.Fatalf("drop on INBOX must move m2, writes=%v", w)
	}
	if p := writesFor(st, "moveToFolderId")[0]; p["id"] != "m2" || p["moveToFolderId"] != "acc1::INBOX" {
		t.Fatalf("moved %+v", p)
	}
}

// C3: Backspace and Delete trash the open mail like "d", but never while typing.
func TestBackspaceAndDeleteTrashLikeD(t *testing.T) {
	app, loop, st := keymapApp(t)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	press := func(k ui.Key) {
		tt.Key(0, k)
		loop.drain()
		tt.Frame()
	}
	press(ui.KeyJ)
	if app.selectedID != "m1" {
		t.Fatalf("j opens the first mail, got %q", app.selectedID)
	}
	press(ui.KeyBackspace)
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(writesFor(st, "trash")) == 1 }) {
		t.Fatal("Backspace must trash the open mail")
	}
	loop.drain()
	app.selectedID = "m2"
	tt.Frame()
	press(ui.KeyDelete)
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(writesFor(st, "trash")) == 2 }) {
		t.Fatal("Delete must trash the open mail")
	}

	// In the search field Backspace edits the text.
	loop.drain()
	app.selectedID = "m3"
	press(ui.KeySlash)
	tt.TypeKey(0, ui.KeyX, "x")
	tt.Frame()
	press(ui.KeyBackspace)
	press(ui.KeyDelete)
	time.Sleep(50 * time.Millisecond)
	loop.drain()
	if n := len(writesFor(st, "trash")); n != 2 {
		t.Fatalf("Backspace/Delete while typing must not trash, trash writes=%d", n)
	}
}

// C5: the selection summary sends each checked mail's text, but never an encrypted body.
func TestSelectionSummaryInputsSkipEncryptedBodies(t *testing.T) {
	hdrs := []backend.MessageHeader{ // newest first, as listed
		{ID: "n3", Subject: "Neu", From: "Max Muster <max.muster@example.org>"},
		{ID: "n2", Subject: "PGP"},
		{ID: "n1", Subject: "S/MIME"},
		{ID: "n0", Subject: "Ohne Detail"},
	}
	details := map[string]backend.MessageDetail{
		"n3": {BodyText: "Termin am Freitag"},
		"n2": {BodyText: "-----BEGIN PGP MESSAGE-----\nhQEMA\n-----END PGP MESSAGE-----"},
		"n1": {Security: backend.MessageSecurity{Encrypted: true, Decryption: "skipped"}},
	}
	in, enc := selectionSummaryInputs(hdrs, details)
	if enc != 2 || len(in) != 4 {
		t.Fatalf("encrypted=%d inputs=%d", enc, len(in))
	}
	if in[0].ID != "n0" || in[3].ID != "n3" {
		t.Fatalf("the thread must read oldest first: %s … %s", in[0].ID, in[3].ID)
	}
	byID := map[string]int{}
	for i, m := range in {
		byID[m.ID] = i
	}
	if m := in[byID["n2"]]; !m.Encrypted || m.BodyText != "" {
		t.Fatalf("PGP body must not be sent: %+v", m)
	}
	if m := in[byID["n1"]]; !m.Encrypted || m.BodyText != "" {
		t.Fatalf("S/MIME mail must be marked encrypted: %+v", m)
	}
	if m := in[byID["n3"]]; m.Encrypted || m.BodyText != "Termin am Freitag" {
		t.Fatalf("plain body expected: %+v", m)
	}
}

// C4/C5: both selection bars offer Spam and the AI summary; the summary shows where AI results do.
func TestSelectionBarsOfferSpamAndAISummary(t *testing.T) {
	app := newParityApp(t, &eventDirect{}, nil)
	app.engineReady = true
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", Subject: "Eins"},
		{ID: "m2", AccountID: "acc1", Subject: "Zwei"},
	})
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(1, true, false)

	desk := ui.NewTester(app.RenderMain, 1380, 860)
	desk.Frame()
	for _, label := range []string{"Markierte als Spam markieren", "KI: Auswahl zusammenfassen"} {
		if _, ok := desk.Find(label); !ok {
			t.Fatalf("desktop selection bar lacks %q", label)
		}
	}
	app.selSum = selectionSummary{open: true, count: 2, encrypted: 1, result: "## Überblick\n- Angebot Q4"}
	desk.Frame()
	if !desk.HasText("KI: Zusammenfassung von 2 Mails") || !desk.HasText("Angebot Q4") || !strings.Contains(strings.Join(desk.Texts(), " "), "verschlüsselte Mail") {
		t.Fatalf("summary card with encryption hint expected in the reading pane: %v", desk.Texts())
	}
	app.closeSelectionSummary()

	app.phone.router = ui.NewRouter(phoneRouteList)
	app.setNav("recent")
	ph := app.phoneTester()
	ph.Frame()
	mustClick(t, ph, "Mehr")
	for _, want := range []string{"Als Spam markieren", "KI: Auswahl zusammenfassen"} {
		if !ph.HasText(want) {
			t.Fatalf("phone selection sheet lacks %q: %v", want, ph.Texts())
		}
	}
	app.phone.bulkMore = false
	app.selSum = selectionSummary{open: true, count: 2, result: "Kurzfassung der Auswahl"}
	ph = ui.NewTester(func(c *ui.Context) { // the phone's dialogs come after its screen
		app.winW, app.winH = 380, 760
		app.renderPhone(c)
		app.renderDialogs(c)
	}, 380, 760)
	ph.Frame()
	if !ph.HasText("Kurzfassung der Auswahl") {
		t.Fatalf("phone summary dialog expected: %v", ph.Texts())
	}
	assertNoClipping(t, ph, 380, "selection summary")
}

// C6: the preview line shows under each mail with a fetched snippet, on both platforms, and the
// setting hides it.
func TestListPreviewLineFollowsSetting(t *testing.T) {
	app := newParityApp(t, nil, nil)
	app.engineReady = true
	app.setNav("recent")
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", Subject: "Angebot", Snippet: "Hallo Max, anbei das Angebot"},
		{ID: "m2", AccountID: "acc1", Subject: "Ohne Text"},
	})
	desk := ui.NewTester(app.RenderMain, 1380, 860)
	app.phone.router = ui.NewRouter(phoneRouteList)
	ph := app.phoneTester()
	for _, tt := range []*ui.Tester{desk, ph} {
		tt.Frame()
		if !tt.HasText("Hallo Max, anbei das Angebot") {
			t.Fatalf("preview line expected: %v", tt.Texts())
		}
	}
	off := false
	app.cfg.App.ListSnippets = &off
	for _, tt := range []*ui.Tester{desk, ph} {
		tt.Frame()
		if tt.HasText("Hallo Max, anbei das Angebot") {
			t.Fatal("the setting must hide the preview line")
		}
	}
}

// C7: the send-later presets and validity.
func TestSendLaterPresets(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	wed := time.Date(2026, 11, 25, 14, 37, 20, 0, loc)
	inHour, tomorrow, monday := sendLaterPresets(wed)
	if !inHour.Equal(time.Date(2026, 11, 25, 15, 37, 0, 0, loc)) {
		t.Fatalf("in an hour: %v", inHour)
	}
	if !tomorrow.Equal(time.Date(2026, 11, 26, 8, 0, 0, 0, loc)) {
		t.Fatalf("tomorrow: %v", tomorrow)
	}
	if !monday.Equal(time.Date(2026, 11, 30, 8, 0, 0, 0, loc)) {
		t.Fatalf("monday: %v", monday)
	}
	mon := time.Date(2026, 11, 30, 7, 0, 0, 0, loc)
	if _, _, next := sendLaterPresets(mon); !next.Equal(time.Date(2026, 12, 7, 8, 0, 0, 0, loc)) {
		t.Fatalf("on a Monday it is next week's: %v", next)
	}
	if sendLaterValid(wed.Add(30*time.Second), wed) || !sendLaterValid(wed.Add(time.Hour), wed) {
		t.Fatal("only times ahead are valid")
	}
}

// C7: a scheduled send skips the undo countdown and hands the engine its time; the outbox shows
// the time with "send now" and "back to drafts".
func TestScheduledSendAndOutboxEntry(t *testing.T) {
	app, loop, st := keymapApp(t)
	app.OpenComposeNew()
	app.composeTo = "alice@example.org"
	app.composeSubject = "Später"
	app.composeBody = "Bis Montag"
	at := time.Now().Add(3 * time.Hour).Truncate(time.Minute)
	app.ScheduleComposeSend(context.Background(), at)
	if len(app.pendingSends) != 0 {
		t.Fatal("a scheduled send must not start the undo countdown")
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return st.rpc.lastParams("compose.send") != nil }) {
		t.Fatal("compose.send expected")
	}
	if got, _ := st.rpc.lastParams("compose.send")["sendAt"].(float64); int64(got) != at.Unix() {
		t.Fatalf("sendAt = %v, want %d", st.rpc.lastParams("compose.send")["sendAt"], at.Unix())
	}

	app.outboxItems = []backend.OutboxEntry{{ID: "ob-1", Subject: "Später", Recipients: []string{"alice@example.org"}, ScheduledAt: at.Unix()}}
	app.outboxOpen = true
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	for _, want := range []string{"Geplant: ", "Jetzt senden", "Zurück zu Entwürfen"} {
		if !strings.Contains(strings.Join(tt.Texts(), "|"), want) {
			t.Fatalf("outbox lacks %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("Alle jetzt senden") || tt.HasText("Verwerfen") {
		t.Fatalf("a scheduled-only outbox offers no send-all/discard: %v", tt.Texts())
	}
	if lbl, only := app.outboxLabel(); !only || lbl != "Geplant (1)" {
		t.Fatalf("outbox label %q only=%v", lbl, only)
	}
}

// C7: on a phone, the compose buttons with "Send later…", the outbox and the time picker fit.
func TestSendLaterFitsPhone(t *testing.T) {
	app := newParityApp(t, &eventDirect{}, nil)
	app.engineReady = true
	app.OpenComposeNew()
	app.composeAutoSaveSavedAt = time.Now()
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 380, 800
		app.renderPhone(c)
		app.renderDialogs(c)
	}, 380, 800)
	tt.Frame()
	if !tt.HasText("Später senden…") {
		t.Fatalf("phone compose lacks send later: %v", tt.Texts())
	}
	assertNoClipping(t, tt, 380, "phone compose")

	app.sendLater = sendLaterUI{open: true, at: time.Now().Add(-time.Hour)}
	tt.Frame()
	if !tt.HasText("Der Zeitpunkt muss in der Zukunft liegen.") {
		t.Fatalf("a past time must be refused: %v", tt.Texts())
	}
	assertNoClipping(t, tt, 380, "send later picker")
	app.sendLater.open = false
	app.closeCompose()

	app.outboxItems = []backend.OutboxEntry{
		{ID: "ob-1", Subject: "Angebot für das Projekt mit langem Betreff", Recipients: []string{"max.muster@example.org"}, ScheduledAt: time.Now().Add(time.Hour).Unix()},
		{ID: "ob-2", Subject: "Offline", Recipients: []string{"max.muster@example.org"}, Status: "failed", Attempts: 3, LastError: "dial tcp: i/o timeout"},
	}
	app.outboxOpen = true
	tt.Frame()
	if !tt.HasText("Alle jetzt senden") || !tt.HasText("Verwerfen") || !tt.HasText("Zurück zu Entwürfen") {
		t.Fatalf("mixed outbox: %v", tt.Texts())
	}
	assertNoClipping(t, tt, 380, "phone outbox")
}
