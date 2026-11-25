package mailengine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

func newActivityTestEngine(t *testing.T, source string) *Engine {
	t.Helper()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:         filepath.Join(t.TempDir(), "test.db"),
		ConfigLoader:   func() (config.Config, error) { return cfg, nil },
		ConfigSaver:    func(c config.Config) error { cfg = c; return nil },
		ActivitySource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	ctx := context.Background()
	for _, f := range []StoredFolder{
		{ID: FolderID("acc", "INBOX"), AccountID: "acc", Name: "INBOX", Path: "INBOX", Type: "inbox"},
		{ID: FolderID("acc", "Trash"), AccountID: "acc", Name: "Trash", Path: "Trash", Type: "trash"},
	} {
		if err := eng.store.UpsertFolder(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	return eng
}

func addActivityTestMail(t *testing.T, eng *Engine, uid uint32, folder, msgID, subject string) StoredMessage {
	t.Helper()
	m := StoredMessage{
		ID: MessageStoreID("acc", folder, uid), AccountID: "acc", FolderID: FolderID("acc", folder), FolderPath: folder,
		UID: uid, MessageID: msgID, Subject: subject, From: "Anna <anna@example.com>",
		DateISO: time.Now().Format(time.RFC3339), DateUnix: time.Now().Unix(),
	}
	if err := eng.store.UpsertMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func kindsOf(list []ActivityEntry) string {
	var k []string
	for _, a := range list {
		k = append(k, a.Kind+"/"+a.Source)
	}
	return strings.Join(k, ",")
}

func TestActivityLogsActionsAndFindsTheMailAgain(t *testing.T) {
	ctx := context.Background()
	eng := newActivityTestEngine(t, "")
	m := addActivityTestMail(t, eng, 1, "INBOX", "<m1@example.com>", "Angebot")
	tr, fl := true, true
	if _, err := eng.Call(ctx, "messages.actions", map[string]any{"id": m.ID, "read": &tr}); err != nil {
		t.Fatal(err)
	}
	// Marking an already read mail read again is no activity.
	if _, err := eng.Call(ctx, "messages.actions", map[string]any{"id": m.ID, "read": &tr, "flagged": &fl}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Call(ctx, "messages.actions", map[string]any{"id": m.ID, "trash": true}); err != nil {
		t.Fatal(err)
	}
	list, err := eng.ListActivity(ctx, ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(list); got != "trash/user,flag/user,read/user" {
		t.Fatalf("activity = %s", got)
	}
	tr0 := list[0]
	if tr0.Subject != "Angebot" || tr0.Folder != "INBOX" || tr0.Target != "Trash" || tr0.MessageID != "m1@example.com" {
		t.Fatalf("trash entry = %+v", tr0)
	}
	// The trashed mail is gone from the inbox; once the trash is synced, the entry finds it there.
	if _, ok := eng.LocateActivityMessage(ctx, tr0); ok {
		t.Fatal("located a mail that is not stored anywhere")
	}
	inTrash := addActivityTestMail(t, eng, 7, "Trash", "<m1@example.com>", "Angebot")
	if got, ok := eng.LocateActivityMessage(ctx, tr0); !ok || got.ID != inTrash.ID || got.FolderPath != "Trash" {
		t.Fatalf("located %+v, %v", got, ok)
	}
	// Filter by kind and search.
	if l, _ := eng.ListActivity(ctx, ActivityQuery{Kinds: []string{ActivityRead}}); kindsOf(l) != "read/user" {
		t.Fatalf("kinds filter: %s", kindsOf(l))
	}
	if l, _ := eng.ListActivity(ctx, ActivityQuery{Search: "nothing like it"}); len(l) != 0 {
		t.Fatalf("search: %s", kindsOf(l))
	}
}

func TestActivityNamesWhoActed(t *testing.T) {
	ctx := context.Background()
	eng := newActivityTestEngine(t, ActivityByMCP)
	a := addActivityTestMail(t, eng, 1, "INBOX", "<a@example.com>", "A")
	b := addActivityTestMail(t, eng, 2, "INBOX", "<b@example.com>", "B")
	tr := true
	if _, err := eng.Call(ctx, "messages.batchActions", backend.BatchActionsRequest{
		Items:  []backend.BatchActionItem{{ID: a.ID}, {ID: b.ID}},
		Action: backend.MessageActionsRequest{Read: &tr},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Call(WithActivitySource(ctx, ActivityByRule, "Newsletter"), "messages.markAllRead", map[string]any{"accountId": "acc", "folderId": FolderID("acc", "INBOX")}); err != nil {
		t.Fatal(err)
	}
	list, _ := eng.ListActivity(ctx, ActivityQuery{})
	if got := kindsOf(list); got != "read/mcp,read/mcp" {
		t.Fatalf("activity = %s", got)
	}
	if list[0].Batch == "" || list[0].Batch != list[1].Batch {
		t.Fatalf("one batch action, batches %q %q", list[0].Batch, list[1].Batch)
	}
	// Both mails were read already, so marking the folder read changed nothing and left no
	// entry; with a new unread mail it does.
	addActivityTestMail(t, eng, 3, "INBOX", "<c@example.com>", "C")
	if _, err := eng.Call(WithActivitySource(ctx, ActivityByRule, "Newsletter"), "messages.markAllRead", map[string]any{"accountId": "acc", "folderId": FolderID("acc", "INBOX")}); err != nil {
		t.Fatal(err)
	}
	list, _ = eng.ListActivity(ctx, ActivityQuery{})
	if list[0].Kind != ActivityReadAll || list[0].Source != ActivityByRule || list[0].SourceName != "Newsletter" || list[0].Count != 1 || list[0].Folder != "INBOX" {
		t.Fatalf("read all entry = %+v", list[0])
	}
}

func TestActivityOlderThanRetentionIsDropped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-activityRetention - time.Hour).UnixMilli()
	if _, err := st.db.Exec(`INSERT INTO activity(at, kind) VALUES(?, 'read'), (?, 'trash')`, old, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	_ = st.db.QueryRowContext(ctx, `SELECT count(*) FROM activity`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d entries left, want 1", n)
	}
}
