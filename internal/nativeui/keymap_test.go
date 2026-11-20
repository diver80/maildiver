package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
)

func keymapApp(t *testing.T) (*App, *uiLoop, *testStack) {
	t.Helper()
	st := newTestStack(t)
	app := st.newApp()
	loop := attachUILoop(app, nil)
	app.engineReady = true
	app.readOnly = false
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", Folder: "INBOX", From: "Alice <alice@acme.de>", Subject: "Eins", Unread: true},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", Folder: "INBOX", From: "Bob <bob@acme.de>", Subject: "Zwei"},
		{ID: "m3", MessageID: "m3@x", AccountID: "acc1", Folder: "INBOX", From: "Carol <carol@acme.de>", Subject: "Drei", Flagged: true},
	})
	return app, loop, st
}

func hintKeys(h []keyHint) string {
	keys := make([]string, len(h))
	for i, k := range h {
		keys[i] = k.Keys
	}
	return strings.Join(keys, " ")
}

func TestFooterHintsFollowContext(t *testing.T) {
	app, _, _ := keymapApp(t)

	if got := hintKeys(app.footerHints()); got != "j/k x / c h" {
		t.Fatalf("list hints = %q", got)
	}
	app.selectedID = "m1"
	if got := hintKeys(app.footerHints()); !strings.Contains(got, "r") || !strings.Contains(got, "d") || !strings.HasSuffix(got, "esc") {
		t.Fatalf("message hints = %q", got)
	}
	app.SetMailChecked(1, true, false)
	if got := hintKeys(app.footerHints()); !strings.HasPrefix(got, "x shift+x") || !strings.Contains(got, "q") {
		t.Fatalf("selection hints = %q", got)
	}

	// Read-only hides every write key.
	app.readOnly = true
	for _, h := range app.footerHints() {
		if h.Keys == "d" || h.Keys == "s" || h.Keys == "u" {
			t.Fatalf("read-only must not advertise %q", h.Keys)
		}
	}
	app.readOnly = false

	app.navID = "calendar"
	if got := hintKeys(app.footerHints()); !strings.HasPrefix(got, "n ") {
		t.Fatalf("calendar hints = %q", got)
	}
	app.helpOpen = true
	if got := hintKeys(app.footerHints()); got != "esc" {
		t.Fatalf("modal hints = %q", got)
	}
	app.helpOpen = false
	app.engineReady = false
	if got := hintKeys(app.footerHints()); !strings.HasPrefix(got, "⌘R") {
		t.Fatalf("offline hints = %q", got)
	}
}

func TestFitHintsDropsFromTheEnd(t *testing.T) {
	h := []keyHint{{"j/k", "next/prev"}, {"r", "reply"}, {"esc", "back"}}
	if got := fitHints(h, 10000); len(got) != 3 {
		t.Fatalf("all hints must fit, got %d", len(got))
	}
	first := estimateHintWidth(h[0])
	if got := fitHints(h, first+1); len(got) != 1 || got[0].Keys != "j/k" {
		t.Fatalf("only the first hint fits, got %+v", got)
	}
	if got := fitHints(h, 1); len(got) != 0 {
		t.Fatalf("nothing fits, got %+v", got)
	}
}

// TestSingleKeysDriveTheListHeadless presses real keys into the rendered window: j/k/x/esc act
// on the list, but never while the search field has the focus or a modal is open.
func TestSingleKeysDriveTheListHeadless(t *testing.T) {
	app, loop, _ := keymapApp(t)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	press := func(mods ui.Modifiers, k ui.Key) {
		t.Helper()
		tt.Key(mods, k)
		loop.drain()
		tt.Frame()
	}

	if !tt.HasText("Auswählen") || !tt.HasText("Verbunden (IMAP)") {
		t.Fatalf("footer with status and list hints expected, texts=%v", tt.Texts())
	}

	press(0, ui.KeyJ)
	if app.selectedID != "m1" {
		t.Fatalf("j on a fresh list opens the first mail, got %q", app.selectedID)
	}
	press(0, ui.KeyJ)
	press(0, ui.KeyJ)
	press(0, ui.KeyJ) // stays on the last mail
	if app.selectedID != "m3" {
		t.Fatalf("j moves down and stops at the end, got %q", app.selectedID)
	}
	press(0, ui.KeyK)
	if app.selectedID != "m2" {
		t.Fatalf("k moves up, got %q", app.selectedID)
	}
	if !tt.HasText("Antworten") {
		t.Fatal("message hints expected in the footer")
	}

	press(0, ui.KeyX)
	if !app.IsMailChecked("m2") {
		t.Fatal("x checks the open mail")
	}
	press(0, ui.KeyEscape)
	if len(app.checkedIDs) != 0 || app.selectedID != "m2" {
		t.Fatalf("first esc clears the selection only, checked=%v open=%q", app.checkedIDs, app.selectedID)
	}
	press(0, ui.KeyEscape)
	if app.selectedID != "" {
		t.Fatalf("second esc closes the mail, open=%q", app.selectedID)
	}

	// "/" focuses search; letters then type text instead of triggering shortcuts.
	press(0, ui.KeySlash)
	tt.TypeKey(0, ui.KeyJ, "j")
	tt.TypeKey(0, ui.KeyX, "x")
	loop.drain()
	tt.Frame()
	if app.searchQuery != "jx" || app.selectedID != "" || len(app.checkedIDs) != 0 {
		t.Fatalf("typing in search must not move or check: query=%q open=%q checked=%v", app.searchQuery, app.selectedID, app.checkedIDs)
	}

	// Window shortcuts are off behind a modal.
	app.searchQuery = ""
	app.helpOpen = true
	tt.Frame()
	press(0, ui.KeyJ)
	if app.selectedID != "" {
		t.Fatalf("j behind the help modal must do nothing, open=%q", app.selectedID)
	}
}

func TestTrashKeyOpensNeighbourAndSeveralNeedSecondPress(t *testing.T) {
	app, loop, st := keymapApp(t)
	ctx := context.Background()

	// Several checked mails: the first d only arms.
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(1, true, false)
	app.trashKey(ctx)
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 {
		t.Fatalf("first d on several mails must not write, got %v", writes)
	}
	if len(app.toasts) == 0 || !strings.Contains(app.toasts[len(app.toasts)-1].msg, "Nochmal d") {
		t.Fatalf("expected a confirm toast, got %+v", app.toasts)
	}
	// An expired arm does not count.
	app.keyArmAt = time.Now().Add(-2 * batchKeyConfirmWindow)
	app.trashKey(ctx)
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 {
		t.Fatalf("expired confirmation must re-arm, not write: %v", writes)
	}
	app.trashKey(ctx)
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 2 }) {
		w, _ := st.rpc.snapshot()
		t.Fatalf("second d must trash both mails, writes=%v", w)
	}
	_, params := st.rpc.snapshot()
	for i, want := range []string{"m1", "m2"} {
		if params[i]["id"] != want || params[i]["trash"] != true || params[i]["expectedMessageId"] != want+"@x" {
			t.Fatalf("batch trash %d: %+v", i, params[i])
		}
	}

	// One open mail: trashed at once, and its neighbour opens.
	loop.drain()
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", Subject: "Eins"},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", Subject: "Zwei"},
	})
	app.ClearMailSelection()
	app.selectedID = "m1"
	app.trashKey(ctx)
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.selectedID == "m2" }) {
		t.Fatalf("after d the next mail must open, open=%q", app.selectedID)
	}

	// Read-only blocks the key.
	before, _ := st.rpc.snapshot()
	app.readOnly = true
	app.trashKey(ctx)
	loop.drain()
	if after, _ := st.rpc.snapshot(); len(after) != len(before) {
		t.Fatalf("read-only d must not write: %v", after)
	}
}

func TestReadAndStarKeysToggleOpenMail(t *testing.T) {
	app, loop, st := keymapApp(t)
	ctx := context.Background()

	app.selectedID = "m1" // unread → u marks read
	app.readKey(ctx)
	app.selectedID = "m3" // flagged → s removes the star
	app.starKey(ctx)
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 2 }) {
		w, _ := st.rpc.snapshot()
		t.Fatalf("expected two actions, got %v", w)
	}
	_, params := st.rpc.snapshot()
	byID := map[string]map[string]any{}
	for _, p := range params {
		byID[p["id"].(string)] = p
	}
	if byID["m1"]["read"] != true {
		t.Fatalf("u on an unread mail must mark it read: %+v", byID["m1"])
	}
	if byID["m3"]["flagged"] != false {
		t.Fatalf("s on a starred mail must remove the star: %+v", byID["m3"])
	}
}

func moveFixture(app *App) {
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: "f2", AccountID: "acc1", Name: "Archiv", Path: "Archiv", Type: "archives"},
		{ID: "f3", AccountID: "acc1", Name: "2026", Path: "Archiv/2026"},
		{ID: "f4", AccountID: "acc1", Name: "Kunden", Path: "Kunden"},
		{ID: "g1", AccountID: "acc2", Name: "Archiv", Path: "Archiv"},
	}
	for i := range app.messages {
		app.messages[i].FolderID = "f1"
	}
}

func TestArchiveKeyOpensNeighbour(t *testing.T) {
	app, loop, st := keymapApp(t)
	app.selectedID = "m1"
	app.archiveKey(context.Background())
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.selectedID == "m2" }) {
		t.Fatalf("after e the next mail must open, open=%q", app.selectedID)
	}
	_, params := st.rpc.snapshot()
	if len(params) != 1 || params[0]["id"] != "m1" || params[0]["archive"] != true || params[0]["trash"] != nil || params[0]["expectedMessageId"] != "m1@x" {
		t.Fatalf("archive request = %+v", params)
	}
}

func TestMovePickerStaysInOneAccount(t *testing.T) {
	app, loop, st := keymapApp(t)
	moveFixture(app)

	// A selection across accounts is refused.
	app.messages[2].AccountID = "acc2"
	app.SetMailChecked(1, true, false)
	app.SetMailChecked(2, true, false)
	app.OpenMovePicker()
	if app.moveOpen || len(app.toasts) == 0 || !strings.Contains(app.toasts[len(app.toasts)-1].msg, "eines Kontos") {
		t.Fatalf("cross-account selection must not open the picker: open=%v toasts=%+v", app.moveOpen, app.toasts)
	}
	app.ClearMailSelection()
	app.messages[2].AccountID = "acc1"

	app.selectedID = "m1"
	app.OpenMovePicker()
	if !app.moveOpen || app.moveAccount != "acc1" {
		t.Fatalf("picker must open for acc1, open=%v acc=%q", app.moveOpen, app.moveAccount)
	}
	if got := rowSummary(app.moveFolderRows()); got != "INBOX Archiv+ .2026 Kunden" {
		t.Fatalf("picker rows = %q (only acc1, as a tree)", got)
	}
	if app.moveSourceFolderID() != "f1" {
		t.Fatalf("source folder = %q", app.moveSourceFolderID())
	}
	app.moveFilter = "arch"
	rows := app.moveFolderRows()
	if got := rowSummary(rows); got != "Archiv+ 2026" {
		t.Fatalf("filtered rows = %q", got)
	}
	app.MoveTo(context.Background(), rows[1].Folder)
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 1 }) {
		t.Fatal("move did not run")
	}
	_, params := st.rpc.snapshot()
	if params[0]["id"] != "m1" || params[0]["moveToFolderId"] != "f3" {
		t.Fatalf("move request = %+v", params[0])
	}
	if app.moveOpen {
		t.Fatal("picker must close after the move")
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.selectedID == "m2" }) {
		t.Fatalf("after a move the next mail must open, open=%q", app.selectedID)
	}

	app.readOnly = true
	app.OpenMovePicker()
	if app.moveOpen {
		t.Fatal("read-only must not open the picker")
	}
}

// TestMovePickerKeyboardHeadless drives the picker with keys only: v opens it with the filter
// focused, typing filters, Down moves the cursor and Enter moves the mail.
func TestMovePickerKeyboardHeadless(t *testing.T) {
	app, loop, st := keymapApp(t)
	moveFixture(app)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	press := func(mods ui.Modifiers, k ui.Key) {
		t.Helper()
		tt.Key(mods, k)
		loop.drain()
		tt.Frame()
	}
	press(0, ui.KeyJ) // open m1
	press(0, ui.KeyV)
	if !app.moveOpen || !tt.HasText("Verschieben nach…") {
		t.Fatalf("v must open the picker, open=%v", app.moveOpen)
	}
	for _, r := range "arch" {
		tt.TypeKey(0, ui.KeyA+ui.Key(r-'a'), string(r))
	}
	loop.drain()
	tt.Frame()
	if app.moveFilter != "arch" {
		t.Fatalf("typing must go into the filter, got %q", app.moveFilter)
	}
	press(0, ui.KeyDown)
	if app.moveCursor != 1 {
		t.Fatalf("Down must move the cursor, got %d", app.moveCursor)
	}
	press(0, ui.KeyEnter)
	if !loop.waitUntil(t, 3*time.Second, func() bool { w, _ := st.rpc.snapshot(); return len(w) == 1 }) {
		t.Fatalf("Enter must move the mail, open=%v", app.moveOpen)
	}
	_, params := st.rpc.snapshot()
	if params[0]["moveToFolderId"] != "f3" || params[0]["id"] != "m1" {
		t.Fatalf("move request = %+v", params[0])
	}
}

func TestAppMenuTogglesSidebarHeadless(t *testing.T) {
	app, loop, _ := keymapApp(t)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	if err := tt.Click("Menü"); err != nil {
		t.Fatalf("menu button: %v", err)
	}
	loop.drain()
	tt.Frame()
	for _, item := range []string{"Seitenleiste", "Sprache", "Design", "Tastenkürzel…", "Hilfe…"} {
		if !tt.HasText(item) {
			t.Fatalf("menu item %q missing, texts=%v", item, tt.Texts())
		}
	}
	if err := tt.Click("Seitenleiste"); err != nil {
		t.Fatal(err)
	}
	loop.drain()
	tt.Frame()
	if !app.hideSidebar || tt.HasText("Arbeitsbereich") {
		t.Fatalf("choosing Seitenleiste must hide the sidebar, hidden=%v", app.hideSidebar)
	}

	// Language moved from the toolbar into a submenu.
	for _, step := range []string{"Menü", "Sprache", "English"} {
		if err := tt.Click(step); err != nil {
			t.Fatalf("click %q: %v (texts=%v)", step, err, tt.Texts())
		}
		loop.drain()
		tt.Frame()
	}
	if app.uiLang() != "en" || !tt.HasText("Compose") {
		t.Fatalf("choosing English must switch the UI, lang=%q", app.uiLang())
	}
}
