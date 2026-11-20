package nativeui

import (
	"context"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
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
