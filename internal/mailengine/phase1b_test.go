package mailengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/secretstore"
)

func TestThunderbirdProfileInspectAndImport(t *testing.T) {
	profDir := t.TempDir()

	// 1. Write synthetic Thunderbird prefs.js (IMAP+SMTP+HTML Signature, iCal feed, Google cal, storage cal)
	prefs := `
user_pref("mail.accountmanager.accounts", "account1,account2");
user_pref("mail.account.account1.server", "server1");
user_pref("mail.account.account1.identities", "id1");
user_pref("mail.server.server1.type", "imap");
user_pref("mail.server.server1.hostname", "imap.ionos.de");
user_pref("mail.server.server1.port", 993);
user_pref("mail.server.server1.socketType", 3);
user_pref("mail.server.server1.userName", "frank@firma.de");
user_pref("mail.server.server1.name", "Firma IONOS");
user_pref("mail.identity.id1.useremail", "frank@firma.de");
user_pref("mail.identity.id1.fullName", "Max Muster");
user_pref("mail.identity.id1.smtpServer", "smtp1");
user_pref("mail.identity.id1.htmlSigFormat", true);
user_pref("mail.identity.id1.htmlSigText", "Viele Gr\u00fc\u00dfe<br>Max Muster<br><b>Firma GmbH</b>");
user_pref("mail.smtpserver.smtp1.hostname", "smtp.ionos.de");
user_pref("mail.smtpserver.smtp1.port", 465);
user_pref("mail.smtpserver.smtp1.try_ssl", 3);

user_pref("mail.account.account2.server", "server2");
user_pref("mail.server.server2.type", "none");
user_pref("mail.server.server2.name", "Local Folders");

user_pref("calendar.registry.cal-ics-1.type", "ics");
user_pref("calendar.registry.cal-ics-1.name", "Feiertage DE");
user_pref("calendar.registry.cal-ics-1.uri", "https://example.com/feiertage.ics");
user_pref("calendar.registry.cal-ics-1.color", "#f7768e");
user_pref("calendar.registry.cal-ics-1.readOnly", true);

user_pref("calendar.registry.cal-gcal-1.type", "caldav");
user_pref("calendar.registry.cal-gcal-1.name", "Google Privat");
user_pref("calendar.registry.cal-gcal-1.uri", "https://apidata.googleusercontent.com/caldav/v2/test@gmail.com/events");

user_pref("calendar.registry.cal-local-1.type", "storage");
user_pref("calendar.registry.cal-local-1.name", "Privat Lokal");
`
	if err := os.WriteFile(filepath.Join(profDir, "prefs.js"), []byte(prefs), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. Write synthetic Thunderbird abook.sqlite
	abDB, err := sql.Open("sqlite", filepath.Join(profDir, "abook.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = abDB.Exec(`
		CREATE TABLE properties (card TEXT, name TEXT, value TEXT);
		INSERT INTO properties VALUES ('c1', 'DisplayName', 'Anna Schmidt');
		INSERT INTO properties VALUES ('c1', 'FirstName', 'Anna');
		INSERT INTO properties VALUES ('c1', 'LastName', 'Schmidt');
		INSERT INTO properties VALUES ('c1', 'PrimaryEmail', 'anna@kunde.de');
		INSERT INTO properties VALUES ('c1', 'Company', 'Kunde AG');
		INSERT INTO properties VALUES ('c1', 'WorkPhone', '+49 40 112233');
	`)
	_ = abDB.Close()
	if err != nil {
		t.Fatal(err)
	}

	// 3. Write synthetic Thunderbird calendar-data/local.sqlite
	if err := os.MkdirAll(filepath.Join(profDir, "calendar-data"), 0o700); err != nil {
		t.Fatal(err)
	}
	calDB, err := sql.Open("sqlite", filepath.Join(profDir, "calendar-data", "local.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	stUS := time.Now().Add(48*time.Hour).Unix() * 1_000_000
	enUS := stUS + 3600*1_000_000
	_, err = calDB.Exec(`
		CREATE TABLE cal_events (id TEXT, title TEXT, event_start INTEGER, event_end INTEGER, flags INTEGER);
		CREATE TABLE cal_properties (item_id TEXT, key TEXT, value TEXT);
		INSERT INTO cal_events VALUES ('tb-ev-1', 'Zahnarzt', ?, ?, 0);
		INSERT INTO cal_properties VALUES ('tb-ev-1', 'LOCATION', 'Praxis Mitte');
	`, stUS, enUS)
	_ = calDB.Close()
	if err != nil {
		t.Fatal(err)
	}

	// 4. Write synthetic Thunderbird msgFilterRules.dat
	imapHostDir := filepath.Join(profDir, "ImapMail", "imap.ionos.de")
	if err := os.MkdirAll(imapHostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	filterDat := `version="9"
logging="no"
name="Werbung weg"
enabled="yes"
type="17"
action="Move to folder"
actionValue="imap://frank%40firma.de@imap.ionos.de/Trash"
action="Mark read"
condition="OR (from,contains,newsletter@shop.de) OR (subject,contains,Rabatt)"
`
	if err := os.WriteFile(filepath.Join(imapHostDir, "msgFilterRules.dat"), []byte(filterDat), 0o600); err != nil {
		t.Fatal(err)
	}

	// Inspect preview
	prev, err := InspectThunderbirdProfile(profDir)
	if err != nil {
		t.Fatalf("InspectThunderbirdProfile: %v", err)
	}
	if len(prev.Accounts) != 1 {
		t.Fatalf("expected 1 IMAP account, got %d", len(prev.Accounts))
	}
	acc := prev.Accounts[0]
	if acc.IMAPHost != "imap.ionos.de" || acc.SMTPHost != "smtp.ionos.de" || acc.Email != "frank@firma.de" || acc.DisplayName != "Max Muster" {
		t.Fatalf("unexpected account parsed: %+v", acc)
	}
	if !strings.Contains(acc.Signature, "Viele Grüße") || !strings.Contains(acc.Signature, "Firma GmbH") || strings.Contains(acc.Signature, "<br>") {
		t.Fatalf("expected cleaned plain-text signature, got %q", acc.Signature)
	}
	if len(prev.ICalFeeds) != 1 || prev.ICalFeeds[0].Name != "Feiertage DE" {
		t.Fatalf("expected 1 iCal feed, got %+v", prev.ICalFeeds)
	}
	if len(prev.OtherCals) < 2 {
		t.Fatalf("expected Google + local storage calendars in OtherCals, got %+v", prev.OtherCals)
	}
	if len(prev.LocalEvents) != 1 || prev.LocalEvents[0].Title != "Zahnarzt" || prev.LocalEvents[0].Location != "Praxis Mitte" {
		t.Fatalf("expected 1 local TB event, got %+v", prev.LocalEvents)
	}
	if len(prev.Contacts) != 1 || prev.Contacts[0].Email != "anna@kunde.de" || prev.Contacts[0].Company != "Kunde AG" {
		t.Fatalf("expected 1 TB contact, got %+v", prev.Contacts)
	}
	if len(prev.Filters) != 1 || prev.Filters[0].Name != "Werbung weg" || len(prev.Filters[0].Conditions) != 2 || len(prev.Filters[0].Actions) != 2 {
		t.Fatalf("expected 1 TB filter rule with 2 conditions & 2 actions, got %+v", prev.Filters)
	}

	// Apply import via Engine and verify idempotency
	dir := t.TempDir()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "maildiver.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	opts := TBImportOptions{
		ImportAccounts:    true,
		ImportICalFeeds:   true,
		ImportLocalEvents: true,
		ImportContacts:    true,
		ImportFilters:     true,
	}
	rep, err := eng.ApplyThunderbirdImport(context.Background(), profDir, opts)
	if err != nil {
		t.Fatalf("ApplyThunderbirdImport: %v", err)
	}
	if rep.AccountsAdded != 1 || rep.SignaturesLoaded != 1 || rep.FeedsAdded != 1 || rep.EventsAdded != 1 || rep.ContactsAdded != 1 || rep.FiltersAdded != 1 {
		t.Fatalf("unexpected first import report: %+v", rep)
	}
	if len(cfg.Direct.Accounts) != 1 || cfg.Direct.Accounts[0].Enabled {
		t.Fatalf("imported account should start disabled until password is set: %+v", cfg.Direct.Accounts)
	}

	// Second run must skip duplicates cleanly
	rep2, err := eng.ApplyThunderbirdImport(context.Background(), profDir, opts)
	if err != nil {
		t.Fatalf("second ApplyThunderbirdImport: %v", err)
	}
	if rep2.AccountsAdded != 0 || rep2.AccountsSkipped != 1 || rep2.FeedsSkipped != 1 || rep2.ContactsSkipped != 1 || rep2.FiltersSkipped != 1 {
		t.Fatalf("expected idempotent second import, got %+v", rep2)
	}
}

func TestRecurringEventCreateUpdateAndExpand(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "maildiver.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	ctx := context.Background()
	baseDay := time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
	start := baseDay.Add(10 * time.Hour)
	end := baseDay.Add(11 * time.Hour)

	raw, err := eng.Call(ctx, "calendar.create", backend.CreateCalendarEventRequest{
		CalendarID:     LocalCalendarID,
		Title:          "Weekly Standup",
		Start:          start.Format(time.RFC3339),
		End:            end.Format(time.RFC3339),
		RecurrenceRule: "FREQ=WEEKLY;COUNT=4",
	})
	if err != nil {
		t.Fatalf("calendar.create: %v", err)
	}
	var created struct {
		Event backend.CalendarEvent `json:"event"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if !created.Event.IsRecurring || created.Event.RecurrenceRule != "FREQ=WEEKLY;COUNT=4" {
		t.Fatalf("expected recurring event created, got %+v", created.Event)
	}

	// Expand over 60 days -> should yield 4 occurrences
	evRaw, err := eng.Call(ctx, "calendar.events", map[string]any{
		"start": baseDay.Format(time.RFC3339),
		"end":   baseDay.AddDate(0, 0, 60).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("calendar.events: %v", err)
	}
	var evResp backend.CalendarEventsResponse
	if err := json.Unmarshal(evRaw, &evResp); err != nil {
		t.Fatal(err)
	}
	if len(evResp.Events) != 4 {
		t.Fatalf("expected 4 weekly occurrences, got %d: %+v", len(evResp.Events), evResp.Events)
	}

	// Update title and recurrence via calendar.update
	_, err = eng.Call(ctx, "calendar.update", map[string]any{
		"calendarId": LocalCalendarID,
		"eventId":    created.Event.ID,
		"title":      "Weekly Sync Updated",
		"rrule":      "FREQ=WEEKLY;COUNT=2",
	})
	if err != nil {
		t.Fatalf("calendar.update: %v", err)
	}
	evRaw2, err := eng.Call(ctx, "calendar.events", map[string]any{
		"start": baseDay.Format(time.RFC3339),
		"end":   baseDay.AddDate(0, 0, 60).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	var evResp2 backend.CalendarEventsResponse
	_ = json.Unmarshal(evRaw2, &evResp2)
	if len(evResp2.Events) != 2 || evResp2.Events[0].Title != "Weekly Sync Updated" {
		t.Fatalf("expected 2 updated occurrences, got %+v", evResp2.Events)
	}
}

func TestICSRSVPBuildAndRespond(t *testing.T) {
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nPRODID:-//Test//Test//EN\r\nBEGIN:VEVENT\r\nUID:invite-42@acme.de\r\nDTSTART:20261125T140000Z\r\nDTEND:20261125T150000Z\r\nSUMMARY:Architektur-Review\r\nORGANIZER;CN=Alice:mailto:alice@acme.de\r\nATTENDEE;CN=Frank:mailto:frank@firma.de\r\nLOCATION:Raum 2\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	org, atts := ParseICSInviteMeta(invite)
	if org != "Alice <alice@acme.de>" || len(atts) != 1 || atts[0] != "Frank <frank@firma.de>" {
		t.Fatalf("unexpected invite meta: org=%q atts=%v", org, atts)
	}

	replyICS, orgEmail, summary, err := BuildICSRSVP(invite, "frank@firma.de", "Frank", "accepted")
	if err != nil {
		t.Fatalf("BuildICSRSVP: %v", err)
	}
	if orgEmail != "alice@acme.de" || summary != "Architektur-Review" {
		t.Fatalf("unexpected RSVP return: org=%q summary=%q", orgEmail, summary)
	}
	if !strings.Contains(replyICS, "METHOD:REPLY") || !strings.Contains(replyICS, "PARTSTAT=ACCEPTED") || !strings.Contains(replyICS, "UID:invite-42@acme.de") {
		t.Fatalf("unexpected RSVP ICS payload:\n%s", replyICS)
	}

	_, mimeRaw := BuildCalendarReplyMIME("Frank <frank@firma.de>", orgEmail, "Zugesagt: "+summary, "Ich bin dabei.", replyICS, "msg-1@acme.de")
	if !strings.Contains(string(mimeRaw), "text/calendar; charset=utf-8; method=REPLY") {
		t.Fatalf("expected text/calendar method=REPLY in MIME:\n%s", string(mimeRaw))
	}

	// Test RespondMessageICS (imports event locally even when no SMTP account is configured)
	dir := t.TempDir()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "maildiver.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	ctx := context.Background()
	if err := eng.store.UpsertMessage(ctx, StoredMessage{
		ID:          "m-invite",
		AccountID:   "acc-1",
		FolderID:    "f-inbox",
		FolderPath:  "INBOX",
		Subject:     "Einladung: Architektur-Review",
		From:        "Alice <alice@acme.de>",
		DateISO:     time.Now().UTC().Format(time.RFC3339),
		DateUnix:    time.Now().Unix(),
		BodyFetched: true,
		HasICS:      true,
		ICSBody:     invite,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := eng.RespondMessageICS(ctx, "m-invite", LocalCalendarID, "accepted")
	if err != nil {
		t.Fatalf("RespondMessageICS: %v", err)
	}
	if res.ImportedCount != 1 || res.Status != "accepted" {
		t.Fatalf("expected 1 imported event on accept, got %+v", res)
	}
}
