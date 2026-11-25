package mailengine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

// Accepting into a Google calendar while Google is not set up (no client ID) still answers the
// organizer and keeps the event, in the local calendar, with the calendar's problem reported.
func TestAcceptIntoUnwritableCalendarStillAnswers(t *testing.T) {
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nPRODID:-//Test//Test//EN\r\nBEGIN:VEVENT\r\nUID:kickoff-7@acme.example\r\nDTSTART:20261209T120000Z\r\nDTEND:20261209T130000Z\r\nSUMMARY:Kick-off\r\nORGANIZER;CN=Oscar:mailto:oscar@acme.example\r\nATTENDEE;CN=Me:mailto:me@work.example\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{{ID: "acc-1", Email: "me@work.example", Enabled: true}}
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	ctx := context.Background()
	if err := eng.store.UpsertCalendar(ctx, StoredCalendar{ID: "google-work", Name: "Work (Google)", Source: "google", ExternalID: "me@work.example"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.store.UpsertMessage(ctx, StoredMessage{
		ID: "m-kickoff", AccountID: "acc-1", FolderID: "f-inbox", FolderPath: "INBOX",
		Subject: "Kick-off", From: "Oscar <oscar@acme.example>",
		DateISO: time.Now().UTC().Format(time.RFC3339), DateUnix: time.Now().Unix(),
		BodyFetched: true, HasICS: true, ICSBody: invite,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := eng.RespondMessageICS(ctx, "m-kickoff", "google-work", "accepted")
	if err != nil {
		t.Fatalf("accept must not fail because of the calendar: %v", err)
	}
	if !strings.Contains(res.CalendarError, "Client-ID") {
		t.Errorf("CalendarError = %q, want the missing client ID named", res.CalendarError)
	}
	if res.ImportedCount != 1 || res.CalendarName != "Persönlich" {
		t.Errorf("event not kept locally: %+v", res)
	}
	// The answer was attempted: it only stops at the missing SMTP password of this test account.
	if res.OrganizerEmail != "oscar@acme.example" || res.ReplyError == "" {
		t.Errorf("answer to the organizer not attempted: %+v", res)
	}
}
