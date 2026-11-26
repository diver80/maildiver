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
