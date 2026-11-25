package mailengine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

// The AI completes a contact from that person's own mails: never encrypted ones, never mails of
// someone whose address merely contains the contact's.
func TestMailsFromSenderSkipsEncryptedAndOtherSenders(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	inbox := FolderID("acc", "INBOX")
	now := time.Now().UTC()
	add := func(uid uint32, from, body string, age time.Duration, encrypted bool) {
		m := StoredMessage{
			ID: from + body, AccountID: "acc", FolderID: inbox, FolderPath: "INBOX", UID: uid,
			Subject: "s", From: from, DateISO: now.Add(-age).Format(time.RFC3339), DateUnix: now.Add(-age).Unix(),
			BodyFetched: true, BodyText: body,
		}
		if encrypted {
			m.Headers = map[string]string{"content-type": "application/pkcs7-mime; smime-type=enveloped-data"}
			m.Attachments = []backend.Attachment{{Name: "smime.p7m", ContentType: "application/pkcs7-mime"}}
		}
		if err := eng.store.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "Lukas Carlé <lukas@example.com>", "newest, encrypted", time.Hour, true)
	add(2, "Lukas Carlé <lukas@example.com>", "second", 2*time.Hour, false)
	add(3, "Someone <xlukas@example.com>", "other sender", 3*time.Hour, false)
	add(4, "lukas@example.com", "third", 4*time.Hour, false)
	add(5, "Lukas <LUKAS@example.com>", "fourth", 5*time.Hour, false)

	got, err := eng.MailsFromSender(ctx, []string{"Lukas@Example.com"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BodyText != "second" || got[1].BodyText != "third" {
		t.Fatalf("got %+v", got)
	}
}

// The address book list and the Thunderbird import see every contact, not the first 500.
func TestListContactsHasNoLimit(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for i := 0; i < 620; i++ {
		if _, err := eng.store.SaveContactFull(ctx, backend.ContactRecord{ID: fmt.Sprintf("c%d", i), DisplayName: fmt.Sprintf("Person %03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := eng.ListContacts(ctx, ""); len(got) != 620 {
		t.Fatalf("ListContacts = %d, want 620", len(got))
	}
	if got, _ := eng.ListContacts(ctx, "Person 6"); len(got) != 20 {
		t.Fatalf("ListContacts(query) = %d, want 20", len(got))
	}
}
