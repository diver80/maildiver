package nativeui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/secretstore"
	"github.com/diver80/postdive/internal/triage"
)

func TestAttendeeFieldParsing(t *testing.T) {
	raw := `"Muster, Anna" <anna@example.org>, ben@example.org; anna@example.org, kein-mail, `
	atts := parseAttendeeList(raw)
	if len(atts) != 2 || atts[0].Name != "Muster, Anna" || atts[1].Email != "ben@example.org" {
		t.Fatalf("parseAttendeeList: %+v", atts)
	}
	if bad := invalidAttendees(raw); len(bad) != 1 || bad[0] != "kein-mail" {
		t.Fatalf("invalidAttendees: %v", bad)
	}
	if got := formatAttendeeList(atts); got != `"Muster, Anna" <anna@example.org>, ben@example.org` {
		t.Fatalf("formatAttendeeList: %q", got)
	}
	if got := replaceLastRecipient("anna@example.org, Er", "Erika <erika@example.org>"); got != "anna@example.org, Erika <erika@example.org>, " {
		t.Fatalf("replaceLastRecipient: %q", got)
	}
}

func TestMailParticipantsLeaveOutOwnAddresses(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }})
	app.accounts = []backend.Account{{ID: "a1", Identities: []backend.AccountIdentity{{Email: "Ich@Example.org"}}}}
	msg := &backend.MessageDetail{MessageHeader: backend.MessageHeader{
		From: `"Olga" <olga@example.org>`,
		To:   []string{"Ich <ich@example.org>", "anna@example.org"},
		CC:   []string{"olga@example.org", "kaputt"},
	}}
	got := app.mailParticipants(msg)
	if len(got) != 2 || got[0].Email != "olga@example.org" || got[0].Name != "Olga" || got[1].Email != "anna@example.org" {
		t.Fatalf("mailParticipants: %+v", got)
	}
}

// The event form takes attendees with suggestions from the contacts, saves them with the event,
// and the event's details list them.
func TestEventFormAttendeesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Direct.Mode = config.EngineDirect
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	loadCfg := func() (config.Config, error) { return cfg, nil }
	saveCfg := func(next config.Config) error { cfg = next; return nil }
	eng, err := mailengine.New(mailengine.Options{
		DBPath: filepath.Join(dir, "postdive.db"), Secrets: secretstore.NewMemoryStore(),
		ConfigLoader: loadCfg, ConfigSaver: saveCfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	if _, err := eng.SaveContact(context.Background(), backend.ContactRecord{DisplayName: "Erika Mustermann", Email: "erika@beispiel.de"}); err != nil {
		t.Fatal(err)
	}
	store := triage.NewStoreAt(dir)
	bc := backend.New(backend.Options{Engine: eng, Events: eng.Events(), Store: store, LoadConfig: loadCfg, Version: "test"})
	app := NewApp(Options{ConfigLoader: loadCfg, ConfigSaver: saveCfg, Store: store, Backend: bc, Direct: eng})
	updates := make(chan func(), 64)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)
	wait := func(cond func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !cond() {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatal("timed out")
			}
		}
	}

	app.OpenNewEventModal(nil)
	app.eventTitle = "Planung"
	app.eventAttendees = "Eri"
	app.refreshEventAttendeeSuggestions()
	if len(app.eventAttSuggest) != 1 || app.eventAttSuggest[0].Email != "erika@beispiel.de" {
		t.Fatalf("expected Erika as suggestion, got %+v", app.eventAttSuggest)
	}
	tt := ui.NewTester(app.RenderMain, 1280, 900)
	tt.Frame()
	if _, ok := tt.Find("Erika Mustermann <erika@beispiel.de>"); !ok {
		t.Fatalf("suggestion not shown; texts=%v", tt.Texts())
	}
	app.eventAttendees = replaceLastRecipient(app.eventAttendees, "Erika Mustermann <erika@beispiel.de>")
	app.eventAttSuggest = nil
	tt.Frame()
	if _, ok := tt.Find("Einladung an die Teilnehmer senden"); !ok {
		t.Fatalf("invite checkbox not shown; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("In Teams planen"); !ok {
		t.Fatalf("Teams button not shown; texts=%v", tt.Texts())
	}

	// Saved without mailing (no account here): the attendee stays with the event.
	app.eventSendInvites = false
	app.SaveCalendarEvent(context.Background())
	wait(func() bool { return !app.eventBusy })
	evs, err := bc.CalendarEvents(context.Background(), time.Now().Add(-time.Hour).Format(time.RFC3339), time.Now().Add(48*time.Hour).Format(time.RFC3339), nil, 50)
	if err != nil || len(evs.Events) != 1 {
		t.Fatalf("expected the saved event: %+v err=%v toasts=%+v", evs, err, app.toasts)
	}
	ev := evs.Events[0]
	if len(ev.Attendees) != 1 || ev.Attendees[0].Email != "erika@beispiel.de" || ev.Attendees[0].Status != "needsAction" {
		t.Fatalf("attendee not saved: %+v", ev.Attendees)
	}

	app.OpenEventDetails(ev)
	tt.Frame()
	if _, ok := tt.Find("Teilnehmer (1)"); !ok {
		t.Fatalf("details should list the attendee; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("Erika Mustermann <erika@beispiel.de>"); !ok {
		t.Fatalf("details should name the attendee; texts=%v", tt.Texts())
	}
}

func TestTeamsMeetingURL(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, loc)
	got := teamsMeetingURL("Planung & Review", start, start.Add(30*time.Minute), "Ort: Raum 2\n\nAgenda",
		[]backend.EventAttendee{{Email: "anna@example.org", Name: "Anna"}, {Email: "ben+x@example.org"}, {Email: "ANNA@example.org"}})
	want := "https://teams.microsoft.com/l/meeting/new?subject=Planung%20%26%20Review" +
		"&startTime=2026-12-01T10%3A00%3A00%2B01%3A00&endTime=2026-12-01T10%3A30%3A00%2B01%3A00" +
		"&content=Ort%3A%20Raum%202%0A%0AAgenda&attendees=anna%40example.org%2Cben%2Bx%40example.org"
	if got != want {
		t.Fatalf("teamsMeetingURL:\n got %s\nwant %s", got, want)
	}
	if teamsMeetingURL("", time.Time{}, time.Time{}, "", nil) != teamsScheduleURL {
		t.Fatal("empty form should open the plain scheduling form")
	}
}

func TestTeamsSelfAddressAndAllDayTimes(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }})
	app.cfg.Direct.Accounts = []config.DirectAccount{{ID: "a1", Email: "ich@example.org"}, {ID: "a2", Email: "zwei@example.org"}}
	app.eventInviteAccount = "a2"
	if got := app.teamsSelfAddress(); got != "zwei@example.org" {
		t.Fatalf("teamsSelfAddress: %q", got)
	}
	app.eventStart, app.eventEnd, app.eventAllDay = "2026-12-01", "2026-12-02", true
	s, e := app.eventFormTimes()
	if s.Hour() != 0 || e.Sub(s) != 48*time.Hour {
		t.Fatalf("all-day times: %v – %v", s, e)
	}
}

func TestDeleteDialogOffersDeclineForInvitations(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.cfg.Language = "de"
	app.RequestDeleteCalendarEvent(backend.CalendarEvent{ID: "e1", CalendarID: "c1", Title: "Refinement", Start: "2026-12-01T08:30:00Z", Organizer: "Olga <olga@example.org>", CanDecline: true})
	if !app.calDelNotify {
		t.Fatal("declining should be preselected")
	}
	tt := ui.NewTester(app.RenderMain, 1380, 900)
	tt.Frame()
	if !tt.HasText("Absage an den Organisator senden (Olga <olga@example.org>)") {
		t.Fatalf("decline option missing; texts=%v", tt.Texts())
	}
}

func TestRemindersUseDefaultSettingAndSkipDeclined(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.notifyEnabled = true
	now := time.Now()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	app.events = []backend.CalendarEvent{
		{ID: "e1", Title: "Standard", Start: at(14 * time.Minute)},
		{ID: "e2", Title: "Abgesagt", Start: at(5 * time.Minute), ResponseStatus: "declined"},
	}
	app.checkUpcomingMeetingReminders()
	if len(app.recentAlerts) != 1 || app.recentAlerts[0].Title != "Standard" {
		t.Fatalf("15 min default should remind of the 14-minute event only: %+v", app.recentAlerts)
	}
	if app.defaultEventReminder() != 15 {
		t.Fatalf("new events start with 15 minutes, got %d", app.defaultEventReminder())
	}
	none := 0
	app.cfg.App.EventReminderMinutes = &none
	if app.defaultEventReminder() != backend.ReminderNone {
		t.Fatal("default off: new events start without a reminder")
	}
}

func TestSettingsWarnWhenMacOSHidesNotifications(t *testing.T) {
	if classifyMacNotifySettings(2, 0) != sysNotifyNoAlerts || classifyMacNotifySettings(1, 2) != sysNotifyDenied ||
		classifyMacNotifySettings(2, 2) != sysNotifyAlerts || classifyMacNotifySettings(0, 0) != sysNotifyNotAsked {
		t.Fatal("classifyMacNotifySettings")
	}
	app, _, _ := keymapApp(t)
	app.cfg.Language = "de"
	app.notifyEnabled = true
	opened := ""
	updates := make(chan func(), 4)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)
	app.SetSystemNotifyProbe(func() sysNotifyStatus { return sysNotifyNoAlerts },
		func(focus bool) { opened = map[bool]string{false: "notifications", true: "focus"}[focus] })
	app.OpenSettings()
	tt := ui.NewTester(func(c *ui.Context) { app.renderSettingsBody(c) }, 900, 1400)
	tt.Frame()
	(<-updates)() // the probe's answer, on the UI thread
	tt.Frame()
	if !tt.HasText("macOS zeigt Mitteilungen von PostDive ohne Banner an (Stil „Ohne“): Termin-Erinnerungen gehen so unter. Stil „Hinweise“ wählen.") {
		t.Fatalf("warning missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("Mitteilungseinstellungen öffnen"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if opened != "notifications" {
		t.Fatalf("button should open the notification settings, got %q", opened)
	}
	app.sysNotify = sysNotifyAlerts
	tt.Frame()
	if tt.HasText("macOS zeigt Mitteilungen von PostDive ohne Banner an (Stil „Ohne“): Termin-Erinnerungen gehen so unter. Stil „Hinweise“ wählen.") {
		t.Fatal("no warning once alerts show")
	}
}
