package mailengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
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
		DBPath:       filepath.Join(dir, "postdive.db"),
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
		DBPath:       filepath.Join(dir, "postdive.db"),
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
	if org != "Alice <alice@acme.de>" || len(atts) != 1 || atts[0].Display() != "Frank <frank@firma.de>" {
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
		DBPath:       filepath.Join(dir, "postdive.db"),
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

	// messages.get should now surface ResponseStatus="accepted" and InCalendarName="Persönlich".
	rawGet, err := eng.Call(ctx, "messages.get", json.RawMessage(`{"id":"m-invite"}`))
	if err != nil {
		t.Fatalf("messages.get: %v", err)
	}
	var detail backend.MessageDetail
	if err := json.Unmarshal(rawGet, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.ICSInvite == nil || detail.ICSInvite.ResponseStatus != "accepted" || detail.ICSInvite.InCalendarName != "Persönlich" {
		t.Fatalf("expected accepted/Persönlich on ICSInvite, got %+v", detail.ICSInvite)
	}

	// Declining should remove the local calendar copy and record status="declined".
	decRes, err := eng.RespondMessageICS(ctx, "m-invite", LocalCalendarID, "declined")
	if err != nil {
		t.Fatalf("RespondMessageICS decline: %v", err)
	}
	if decRes.Status != "declined" {
		t.Fatalf("expected declined, got %+v", decRes)
	}
	localEvs, _ := eng.store.ListRawEvents(ctx, []string{LocalCalendarID})
	if len(localEvs) != 0 {
		t.Fatalf("expected local copy deleted on decline, got %d", len(localEvs))
	}
	rawGet2, _ := eng.Call(ctx, "messages.get", json.RawMessage(`{"id":"m-invite"}`))
	var detail2 backend.MessageDetail
	_ = json.Unmarshal(rawGet2, &detail2)
	if detail2.ICSInvite == nil || detail2.ICSInvite.ResponseStatus != "declined" {
		t.Fatalf("expected declined status after decline, got %+v", detail2.ICSInvite)
	}

	// When a synced external calendar (e.g. Google Calendar) already holds the event,
	// messages.get should report its authoritative status & name, and RespondMessageICS
	// must NOT create a duplicate in LocalCalendarID.
	if err := eng.store.UpsertCalendar(ctx, StoredCalendar{
		ID:     "gcal-work",
		Name:   "Frank Hess (avono AG)",
		Source: "google",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.store.UpsertEvent(ctx, StoredEvent{
		ID:           "gcal-work::ev-1",
		CalendarID:   "gcal-work",
		CalendarName: "Frank Hess (avono AG)",
		UID:          "invite-42@acme.de",
		Title:        "Architektur-Review",
		StartISO:     "2026-11-25T14:00:00Z",
		EndISO:       "2026-11-25T15:00:00Z",
		StartUnix:    time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC).Unix(),
		EndUnix:      time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC).Unix(),
		Status:       "confirmed",
		SelfResponse: "accepted",
	}); err != nil {
		t.Fatal(err)
	}
	rawGet3, _ := eng.Call(ctx, "messages.get", json.RawMessage(`{"id":"m-invite"}`))
	var detail3 backend.MessageDetail
	_ = json.Unmarshal(rawGet3, &detail3)
	if detail3.ICSInvite == nil || detail3.ICSInvite.ResponseStatus != "accepted" || detail3.ICSInvite.InCalendarName != "Frank Hess (avono AG)" {
		t.Fatalf("expected Google Calendar accepted status + name, got %+v", detail3.ICSInvite)
	}
	accRes, err := eng.RespondMessageICS(ctx, "m-invite", LocalCalendarID, "tentative")
	if err != nil {
		t.Fatalf("RespondMessageICS tentative with external cal: %v", err)
	}
	if accRes.ImportedCount != 0 || accRes.AlreadyInCalendar != "Frank Hess (avono AG)" {
		t.Fatalf("expected no local duplicate when external calendar has event, got %+v", accRes)
	}
	localEvs2, _ := eng.store.ListRawEvents(ctx, []string{LocalCalendarID})
	if len(localEvs2) != 0 {
		t.Fatalf("expected 0 local events (no duplicate), got %d", len(localEvs2))
	}

	// ParseICSSelfPartStat extracts PARTSTAT for own emails.
	psICS := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:ps-1\r\nDTSTART:20261125T140000Z\r\nSUMMARY:Test\r\nATTENDEE;CN=Frank;PARTSTAT=TENTATIVE:mailto:frank@firma.de\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if got := ParseICSSelfPartStat(psICS, []string{"frank@firma.de"}); got != "tentative" {
		t.Fatalf("ParseICSSelfPartStat = %q, want tentative", got)
	}

	// Test accepting an .ics invite directly into a Google Calendar target (uses POST /events/import with iCalUID).
	var importedUID, importedPath string
	gcalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		importedPath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		importedUID, _ = body["iCalUID"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "gcal-imported-99",
			"iCalUID": importedUID,
			"summary": body["summary"],
			"start":   body["start"],
			"end":     body["end"],
			"status":  "confirmed",
		})
	}))
	defer gcalSrv.Close()

	cfg.Direct.GoogleCalendar.ClientID = "test-client"
	cfg.Direct.GoogleCalendar.Enabled = true
	_ = eng.secrets.Set("gcal:token", `{"access_token":"tok-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"}`)
	eng.gcalAPIBase = gcalSrv.URL
	if err := eng.store.UpsertCalendar(ctx, StoredCalendar{
		ID:         "gcal-target",
		ExternalID: "work@avono.de",
		Name:       "Frank Hess (avono AG)",
		Source:     "google",
	}); err != nil {
		t.Fatal(err)
	}
	invite2 := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:work-sync-77@avono.de\r\nDTSTART:20261126T143000Z\r\nDTEND:20261126T150000Z\r\nSUMMARY:Quick sync external users\r\nORGANIZER:mailto:boss@avono.de\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if err := eng.store.UpsertMessage(ctx, StoredMessage{
		ID:          "m-invite-gcal",
		AccountID:   "acc-1",
		FolderID:    "f-inbox",
		FolderPath:  "INBOX",
		UID:         2,
		Subject:     "Quick sync external users",
		From:        "Boss <boss@avono.de>",
		DateISO:     time.Now().UTC().Format(time.RFC3339),
		DateUnix:    time.Now().Unix(),
		BodyFetched: true,
		HasICS:      true,
		ICSBody:     invite2,
	}); err != nil {
		t.Fatal(err)
	}
	gRes, err := eng.RespondMessageICS(ctx, "m-invite-gcal", "gcal-target", "accepted")
	if err != nil {
		t.Fatalf("RespondMessageICS into Google Calendar: %v", err)
	}
	if gRes.ImportedCount != 1 || gRes.CalendarName != "Frank Hess (avono AG)" {
		t.Fatalf("unexpected Google Calendar RSVP result: %+v", gRes)
	}
	if !strings.HasSuffix(importedPath, "/events/import") || importedUID != "work-sync-77@avono.de" {
		t.Fatalf("expected POST .../events/import with iCalUID=work-sync-77@avono.de, got path=%q uid=%q", importedPath, importedUID)
	}
	// Verify stored in gcal-target with SelfResponse="accepted" and NOT duplicated into LocalCalendarID.
	gEvs, _ := eng.store.ListRawEvents(ctx, []string{"gcal-target"})
	if len(gEvs) != 1 || gEvs[0].SelfResponse != "accepted" {
		t.Fatalf("expected 1 accepted event in gcal-target, got %+v", gEvs)
	}
	localEvs3, _ := eng.store.ListRawEvents(ctx, []string{LocalCalendarID})
	if len(localEvs3) != 0 {
		t.Fatalf("expected 0 local events when accepted directly into Google Calendar, got %d", len(localEvs3))
	}
}
