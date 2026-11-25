package nativeui

import (
	"context"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

const unreadGet = `{"id":"m1","messageId":"m1@x","accountId":"acc1","folder":"INBOX","subject":"Eins","from":"Alice <alice@acme.de>","bodyText":"Hallo","read":false,"unread":true}`

func markReadApp(t *testing.T, mode string, readOnly bool) (*App, *uiLoop, *testStack) {
	t.Helper()
	app, loop, st := keymapApp(t)
	st.box.mu.Lock()
	st.box.cfg.Mailbox.ReadOnly = readOnly
	st.box.cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	st.box.cfg.App.MarkRead = mode
	cfg := st.box.cfg
	st.box.mu.Unlock()
	app.applyConfig(cfg)
	st.rpc.mu.Lock()
	st.rpc.getJSON = unreadGet
	st.rpc.mu.Unlock()
	return app, loop, st
}

func readWrites(st *testStack) []map[string]any {
	writes, params := st.rpc.snapshot()
	var out []map[string]any
	for i, w := range writes {
		if w == "messages.actions" && params[i]["read"] != nil {
			out = append(out, params[i])
		}
	}
	return out
}

// Opening an unread mail marks it read at once, like Thunderbird; "mark as unread" sticks.
func TestOpeningMarksRead(t *testing.T) {
	app, loop, st := markReadApp(t, config.MarkReadOnOpen, false)
	app.SelectMessage(st.ctx, "m1")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(readWrites(st)) == 1 }) {
		t.Fatal("the opened mail was not marked read")
	}
	w := readWrites(st)[0]
	if w["read"] != true || w["id"] != "m1" || w["expectedMessageId"] != "m1@x" {
		t.Fatalf("mark read params: %v", w)
	}
	if app.messages[0].Unread || app.selectedMsg.Unread {
		t.Fatal("the list should show the mail read at once")
	}

	// "u" on the open mail marks it unread; reloading it afterwards must not mark it read again.
	st.rpc.mu.Lock()
	st.rpc.getJSON = unreadGet
	st.rpc.mu.Unlock()
	app.readKey(context.Background())
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(readWrites(st)) == 2 }) {
		t.Fatal("u did not reach the add-on")
	}
	if w := readWrites(st)[1]; w["read"] != false {
		t.Fatalf("u should mark unread: %v", w)
	}
	time.Sleep(300 * time.Millisecond)
	loop.drain()
	if n := len(readWrites(st)); n != 2 {
		t.Fatalf("reloading after u marked the mail read again (%d writes)", n)
	}
}

func TestMarkReadSettings(t *testing.T) {
	// Never: nothing changes.
	app, loop, st := markReadApp(t, config.MarkReadNever, false)
	app.SelectMessage(st.ctx, "m1")
	loop.waitUntil(t, time.Second, func() bool { return app.selectedMsg != nil })
	time.Sleep(100 * time.Millisecond)
	loop.drain()
	if n := len(readWrites(st)); n != 0 {
		t.Fatalf("never: %d writes", n)
	}

	// Read-only: nothing changes, no error toast either.
	app, loop, st = markReadApp(t, config.MarkReadOnOpen, true)
	app.readOnly = true
	app.SelectMessage(st.ctx, "m1")
	loop.waitUntil(t, time.Second, func() bool { return app.selectedMsg != nil })
	time.Sleep(100 * time.Millisecond)
	loop.drain()
	if n := len(readWrites(st)); n != 0 || len(app.toasts) != 0 {
		t.Fatalf("read-only: %d writes, toasts %v", n, app.toasts)
	}

	// Delay: only if the mail is still open after the delay.
	old := markReadDelay
	markReadDelay = 150 * time.Millisecond
	defer func() { markReadDelay = old }()
	app, loop, st = markReadApp(t, config.MarkReadDelay, false)
	app.SelectMessage(st.ctx, "m1")
	loop.waitUntil(t, time.Second, func() bool { return app.selectedMsg != nil })
	if n := len(readWrites(st)); n != 0 {
		t.Fatal("delay: marked at once")
	}
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(readWrites(st)) == 1 }) {
		t.Fatal("delay: not marked after the delay")
	}
	app, loop, st = markReadApp(t, config.MarkReadDelay, false)
	app.SelectMessage(st.ctx, "m1")
	loop.waitUntil(t, time.Second, func() bool { return app.selectedMsg != nil })
	app.selectedID = "m2" // moved on before the delay ran out
	time.Sleep(300 * time.Millisecond)
	loop.drain()
	if n := len(readWrites(st)); n != 0 {
		t.Fatal("delay: a mail left before the delay must stay unread")
	}
}

// In the unread view the open mail stays in place after it was marked read, so j/k go on.
func TestKeepOpenMail(t *testing.T) {
	app, _, _ := keymapApp(t)
	h := func(id string) backend.MessageHeader { return backend.MessageHeader{ID: id} }
	app.messages = []backend.MessageHeader{h("a"), h("b"), h("c"), h("d")}
	app.selectedID = "c"
	ids := func(ms []backend.MessageHeader) string {
		s := ""
		for _, m := range ms {
			s += m.ID
		}
		return s
	}
	if got := ids(app.keepOpenMail([]backend.MessageHeader{h("a"), h("b"), h("d")})); got != "abcd" {
		t.Errorf("got %s", got)
	}
	if got := ids(app.keepOpenMail([]backend.MessageHeader{h("d")})); got != "cd" {
		t.Errorf("earlier read mails go, the open one stays: got %s", got)
	}
	if got := ids(app.keepOpenMail([]backend.MessageHeader{h("a"), h("c")})); got != "ac" {
		t.Errorf("listed anyway: got %s", got)
	}
	app.selectedID = ""
	if got := ids(app.keepOpenMail([]backend.MessageHeader{h("a")})); got != "a" {
		t.Errorf("nothing open: got %s", got)
	}
}

func TestFocusMarkReadUpdatesCountersAndPrunesOnSwitch(t *testing.T) {
	app, loop, st := markReadApp(t, config.MarkReadOnOpen, false)
	app.navID = "focus"
	app.folders = []backend.Folder{
		{ID: "acc1::INBOX", AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: "inbox", UnreadCount: 60},
	}
	app.messages = []backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Subject: "Confluence", Unread: true, Read: false},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Subject: "Solman", Unread: false, Read: true, Flagged: true},
		{ID: "m3", MessageID: "m3@x", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Subject: "Jira", Unread: false, Read: true, Flagged: true},
	}
	app.totalMessages = 3
	app.focusTotal = 3
	app.focusUnread = 1

	app.SelectMessage(st.ctx, "m1")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return len(readWrites(st)) == 1 }) {
		t.Fatal("m1 was not marked read")
	}

	// While m1 is still open in Focus, it remains visible so j/k works,
	// but its unread flag, Focus badge, and INBOX badge are all updated immediately.
	if len(app.messages) != 3 {
		t.Fatalf("expected open read mail to stay in Focus while selected, got %d", len(app.messages))
	}
	if app.messages[0].Unread {
		t.Fatal("m1 should be marked read in the list")
	}
	if got := app.focusCount(); got != 2 {
		t.Fatalf("focusCount() while read mail open = %d, want 2", got)
	}
	if got := app.unreadCount(); got != 0 {
		t.Fatalf("unreadCount() = %d, want 0", got)
	}
	if got := app.folders[0].UnreadCount; got != 59 {
		t.Fatalf("INBOX UnreadCount = %d, want 59", got)
	}

	// Switching to the next mail immediately prunes the read unflagged mail from Focus.
	st.rpc.mu.Lock()
	st.rpc.getJSON = `{"id":"m2","messageId":"m2@x","accountId":"acc1","folder":"INBOX","subject":"Solman","from":"Martina <m@acme.de>","bodyText":"Hi","read":true,"unread":false,"flagged":true}`
	st.rpc.mu.Unlock()
	app.SelectMessage(st.ctx, "m2")
	if len(app.messages) != 2 {
		t.Fatalf("expected read unflagged m1 to be pruned from Focus on switch, got %d messages", len(app.messages))
	}
	if app.messages[0].ID != "m2" || app.messages[1].ID != "m3" {
		t.Fatalf("unexpected Focus order after prune: %+v", app.messages)
	}
	if app.mailList.selected != 0 {
		t.Fatalf("mailList.selected = %d, want 0", app.mailList.selected)
	}
	if got := app.focusCount(); got != 2 {
		t.Fatalf("focusCount() after switch = %d, want 2", got)
	}
}

func TestPendingReadSurvivesStaleMessageLoadAndEscapePrunes(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.navID = "focus"
	app.messages = []backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Unread: true},
		{ID: "m2", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Read: true, Flagged: true},
	}
	app.selectedID = "m1"
	app.totalMessages = 2
	app.focusTotal = 2
	app.focusUnread = 1

	seq := app.beginPendingRead("m1", true)
	app.setLocalRead("m1", true)

	// Simulate a stale list load arriving from SQLite before MessageActions committed.
	req := app.newMessageLoad()
	stale := []backend.MessageHeader{
		{ID: "m1", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Unread: true},
		{ID: "m2", AccountID: "acc1", FolderID: "acc1::INBOX", Folder: "INBOX", Read: true, Flagged: true},
	}
	if !app.applyMessageLoad(req, stale, 2) {
		t.Fatal("applyMessageLoad unexpectedly rejected current seq")
	}
	if app.messages[0].Unread {
		t.Fatal("stale message load must not flip in-flight pending read mail back to unread")
	}
	if got := app.focusCount(); got != 1 {
		t.Fatalf("focusCount() after stale load = %d, want 1", got)
	}
	if got := app.unreadCount(); got != 0 {
		t.Fatalf("unreadCount() after stale load = %d, want 0", got)
	}

	// Closing the open mail via Escape immediately prunes it from Focus.
	app.escapeKey()
	if len(app.messages) != 1 || app.messages[0].ID != "m2" {
		t.Fatalf("Escape should prune read unflagged mail from Focus, got %+v", app.messages)
	}

	// A second stale load while m1 is no longer selected drops m1 as well.
	req2 := app.newMessageLoad()
	if !app.applyMessageLoad(req2, stale, 2) {
		t.Fatal("applyMessageLoad 2 rejected")
	}
	if len(app.messages) != 1 || app.messages[0].ID != "m2" {
		t.Fatalf("stale load while unselected must not resurrect pending-read mail in Focus, got %+v", app.messages)
	}
	if !app.endPendingRead("m1", seq) {
		t.Fatal("endPendingRead should match seq")
	}
}
