package mailengine

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testMail(id, folder, msgID, inReplyTo, refs, subject string, date int64) StoredMessage {
	return StoredMessage{
		ID: id, AccountID: "acc", FolderPath: folder, UID: uint32(date), MessageID: msgID,
		InReplyTo: inReplyTo, References: refs, Subject: subject, From: "a@b.de",
		DateUnix: date, BodyText: "Text von " + id + "\n> zitiert", BodyFetched: true,
	}
}

func threadOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	m, err := s.GetMessage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m.ThreadID
}

// Replies arriving before their parent, and a missing middle mail arriving last, still end up
// in one conversation.
func TestThreadingIsOrderIndependent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// The third mail (reply to the reply) arrives first, then the root, then the middle one.
	third := testMail("m3", "INBOX", "c@x", "b@x", "a@x b@x", "Re: Re: Angebot", 300)
	root := testMail("m1", "INBOX", "a@x", "", "", "Angebot", 100)
	middle := testMail("m2", "Sent", "b@x", "a@x", "a@x", "Re: Angebot", 200)
	for _, m := range []StoredMessage{third, root, middle} {
		if err := s.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	tid := threadOf(t, s, "m1")
	if threadOf(t, s, "m2") != tid || threadOf(t, s, "m3") != tid {
		t.Fatalf("threads: %s %s %s", tid, threadOf(t, s, "m2"), threadOf(t, s, "m3"))
	}

	// A reply that only names its parent (no References), arriving before the parent.
	s2 := openTestStore(t)
	child := testMail("c", "INBOX", "child@x", "parent@x", "", "Re: Frage", 200)
	parent := testMail("p", "INBOX", "parent@x", "", "", "Frage", 100)
	for _, m := range []StoredMessage{child, parent} {
		if err := s2.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if threadOf(t, s2, "c") != threadOf(t, s2, "p") {
		t.Fatal("a reply that arrived first must pull its parent into its conversation")
	}

	// Unrelated mails with the same subject stay apart.
	other := testMail("o", "INBOX", "other@x", "", "", "Angebot", 400)
	if err := s.UpsertMessage(ctx, other); err != nil {
		t.Fatal(err)
	}
	if threadOf(t, s, "o") == tid {
		t.Fatal("same subject alone must not join a conversation")
	}
}

// A conversation lists each mail once (the copy outside the trash), newest first; the AI
// thread keeps reading order; lists count distinct mails and carry no bodies.
func TestConversationAndThreadSizes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.UpsertFolder(ctx, StoredFolder{ID: FolderID("acc", "Trash"), AccountID: "acc", Path: "Trash", Name: "Trash", Type: "trash"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []StoredMessage{
		testMail("m1", "INBOX", "a@x", "", "", "Angebot", 100),
		testMail("m2", "Sent", "b@x", "a@x", "a@x", "Re: Angebot", 200),
		testMail("m2-copy", "Trash", "b@x", "a@x", "a@x", "Re: Angebot", 200),
		testMail("m3", "INBOX", "c@x", "b@x", "a@x b@x", "Re: Re: Angebot", 300),
	} {
		if err := s.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	tid := threadOf(t, s, "m1")
	conv, err := s.ConversationMessages(ctx, tid, 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range conv {
		ids = append(ids, m.ID)
	}
	if len(ids) != 3 || ids[0] != "m3" || ids[1] != "m2" || ids[2] != "m1" {
		t.Fatalf("conversation = %v, want newest first, the Sent copy of b@x", ids)
	}
	if conv[0].BodyHTML != "" {
		t.Error("conversations carry no full bodies")
	}
	if got := conversationSnippet(conv[0].BodyText); got != "Text von m3" {
		t.Errorf("snippet = %q (quoted lines left out)", got)
	}

	sizes, err := s.ThreadSizes(ctx, []string{tid, tid, ""})
	if err != nil || sizes[tid] != 3 {
		t.Fatalf("thread size = %v, err=%v", sizes, err)
	}

	thread, err := s.ListThreadMessages(ctx, tid, 2)
	if err != nil || len(thread) != 2 || thread[len(thread)-1].ID != "m3" {
		t.Fatalf("AI thread: the newest mails in reading order, got %v err=%v", thread, err)
	}

	list, _, err := s.ListMessages(ctx, MessageQuery{AccountID: "acc", HeadersOnly: true})
	if err != nil || len(list) == 0 {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.BodyText != "" || m.BodyHTML != "" {
			t.Fatalf("headers-only list loaded a body: %+v", m)
		}
	}
}

// Mails stored with the old grouping are grouped again once.
func TestRethreadOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Simulate the old grouping: the reply got its parent's ID, the root its own, both stored apart.
	reply := testMail("r", "INBOX", "r@x", "p@x", "root@x p@x", "Re: Plan", 300)
	reply.ThreadID = "th:p@x"
	root := testMail("root", "INBOX", "root@x", "", "", "Plan", 100)
	root.ThreadID = "th:root@x"
	for _, m := range []StoredMessage{reply, root} {
		if err := s.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM kv_meta WHERE k='thread_version'`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if threadOf(t, s, "r") != threadOf(t, s, "root") {
		t.Fatal("reopening must regroup the stored mails")
	}
}
