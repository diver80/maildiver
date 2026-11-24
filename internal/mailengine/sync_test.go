package mailengine

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/secretstore"
)

func countFolder(t *testing.T, e *Engine, accID, path string) int {
	t.Helper()
	f, ok := e.store.FindFolderByIDOrPath(context.Background(), accID, path)
	if !ok {
		t.Fatalf("folder %s not stored", path)
	}
	uids, err := e.store.FolderUIDFlags(context.Background(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(uids)
}

// A mailbox larger than the sync window becomes complete over a few full syncs, newest first;
// mails deleted on the server below the window disappear locally; only mails above the last
// UIDNEXT count as new.
func TestSyncBackfillsOlderMails(t *testing.T) {
	ctx := context.Background()
	addr, memUser, stop := startTestIMAPServer(t, "me@example.org", "pw")
	defer stop()
	base := time.Now().Add(-48 * time.Hour)
	for i := 1; i <= 12; i++ {
		appendMemMsg(t, memUser, "INBOX", fmt.Sprintf("From: a@b.de\nTo: me@example.org\nSubject: Mail %d\nMessage-ID: <m%d@x>\nDate: %s\n\nText %d\n",
			i, i, base.Add(time.Duration(i)*time.Hour).Format(time.RFC1123Z), i))
	}
	prev := backfillBatch
	backfillBatch = 4
	defer func() { backfillBatch = prev }()

	cfg := config.Default()
	var mu sync.Mutex
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "m.db"),
		Secrets:      secretstore.NewMemoryStore(),
		Events:       events.NewHub(),
		ConfigLoader: func() (config.Config, error) { mu.Lock(); defer mu.Unlock(); return cfg, nil },
		ConfigSaver:  func(c config.Config) error { mu.Lock(); cfg = c; mu.Unlock(); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	host, port, _ := net.SplitHostPort(addr)
	var p int
	_, _ = fmt.Sscanf(port, "%d", &p)
	acc, err := eng.SaveAccount(ctx, config.DirectAccount{Name: "Test", Email: "me@example.org", IMAPHost: host, IMAPPort: p, IMAPTLS: "plain"}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	for i := range cfg.Direct.Accounts {
		cfg.Direct.Accounts[i].SyncLimit = 5
	}
	mu.Unlock()

	res, err := eng.SyncAccount(ctx, acc.ID, false)
	if err != nil || !res.BackfillPending || len(res.NewMessages) != 0 {
		t.Fatalf("first sync: pending=%v new=%d err=%v", res.BackfillPending, len(res.NewMessages), err)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 9 {
		t.Fatalf("after the first sync: %d mails, want the 5 newest plus 4 backfilled", n)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 9 {
		t.Fatalf("an INBOX-only sync must not backfill: %d", n)
	}
	res, err = eng.SyncAccount(ctx, acc.ID, false)
	if err != nil || res.BackfillPending {
		t.Fatalf("second full sync: pending=%v err=%v", res.BackfillPending, err)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 12 {
		t.Fatalf("after the second sync: %d mails, want all 12", n)
	}

	// Delete the second-oldest mail on the server: it disappears locally on the next full sync.
	c, err := dialIMAP(ctx, acc, "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.SeqSetNum(2), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 11 {
		t.Fatalf("a mail deleted on the server below the window stays: %d", n)
	}

	// A new mail is reported once.
	appendMemMsg(t, memUser, "INBOX", "From: a@b.de\nTo: me@example.org\nSubject: Neu\nMessage-ID: <new@x>\n\nNeu\n")
	res, err = eng.SyncAccount(ctx, acc.ID, true)
	if err != nil || len(res.NewMessages) != 1 || res.NewMessages[0].Subject != "Neu" {
		t.Fatalf("new mail: %+v err=%v", res.NewMessages, err)
	}

	// Phones: the first wake-up only sets the watermark; then each new mail is reported once,
	// and mails the user saw in the app are not reported.
	if got, err := eng.TakeNewInboxMail(ctx); err != nil || len(got) != 0 {
		t.Fatalf("first wake-up must not report the mailbox: %d %v", len(got), err)
	}
	appendMemMsg(t, memUser, "INBOX", "From: b@c.de\nTo: me@example.org\nSubject: Im Hintergrund\nMessage-ID: <bg@x>\n\nText\n")
	if err := eng.SyncInboxes(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := eng.TakeNewInboxMail(ctx)
	if err != nil || len(got) != 1 || got[0].Subject != "Im Hintergrund" {
		t.Fatalf("background mail: %+v %v", got, err)
	}
	if got, _ := eng.TakeNewInboxMail(ctx); len(got) != 0 {
		t.Fatal("a mail is reported once")
	}
	appendMemMsg(t, memUser, "INBOX", "From: b@c.de\nTo: me@example.org\nSubject: Gesehen\nMessage-ID: <seen@x>\n\nText\n")
	if err := eng.SyncInboxes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.MarkInboxSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := eng.TakeNewInboxMail(ctx); len(got) != 0 {
		t.Fatal("mail seen in the open app must not be reported again")
	}
}

// A caller that stops waiting (a refresh timeout, the system ending a background task) is no
// account error, and a manual sync it started still runs to its end.
func TestSyncCallerTimeoutIsNoAccountError(t *testing.T) {
	addr, memUser, stop := startTestIMAPServer(t, "me@example.org", "pw")
	defer stop()
	appendMemMsg(t, memUser, "INBOX", "From: a@b.de\nTo: me@example.org\nSubject: Hallo\nMessage-ID: <h@x>\n\nText\n")
	cfg := config.Default()
	var mu sync.Mutex
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "m.db"),
		Secrets:      secretstore.NewMemoryStore(),
		Events:       events.NewHub(),
		ConfigLoader: func() (config.Config, error) { mu.Lock(); defer mu.Unlock(); return cfg, nil },
		ConfigSaver:  func(c config.Config) error { mu.Lock(); cfg = c; mu.Unlock(); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	host, port, _ := net.SplitHostPort(addr)
	var p int
	_, _ = fmt.Sscanf(port, "%d", &p)
	acc, err := eng.SaveAccount(context.Background(), config.DirectAccount{Name: "Test", Email: "me@example.org", IMAPHost: host, IMAPPort: p, IMAPTLS: "plain"}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	state := func() AccountSyncStatus {
		for _, st := range eng.AccountsStatus(context.Background()) {
			if st.AccountID == acc.ID {
				return st
			}
		}
		t.Fatal("account has no status")
		return AccountSyncStatus{}
	}

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.SyncAccount(gone, acc.ID, false); err == nil {
		t.Fatal("a cancelled sync reports its end")
	}
	if st := state(); st.State == "error" || st.LastError != "" {
		t.Fatalf("a cancelled caller marked the account: %+v", st)
	}

	// Another sync holds the account; the caller gives up waiting, the sync follows later.
	lk := eng.accountSyncLock(acc.ID)
	lk.Lock()
	short, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()
	if err := eng.SyncNow(short, acc.ID); err != context.DeadlineExceeded {
		t.Fatalf("SyncNow past the caller's deadline: %v", err)
	}
	lk.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for eng.store.CountAccountMessages(context.Background(), acc.ID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the manual sync did not finish after its caller gave up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := state(); st.State == "error" || st.LastError != "" {
		t.Fatalf("account after the sync: %+v", st)
	}
}
