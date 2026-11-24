package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/secretstore"
	"github.com/diver80/postdive/internal/triage"
)

func TestDirectModeUIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Direct.Mode = config.EngineDirect
	cfg.Language = "de"

	loadCfg := func() (config.Config, error) { return cfg, nil }
	saveCfg := func(next config.Config) error {
		cfg = next
		return nil
	}

	eng, err := mailengine.New(mailengine.Options{
		DBPath:       filepath.Join(dir, "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: loadCfg,
		ConfigSaver:  saveCfg,
	})
	if err != nil {
		t.Fatalf("mailengine.New: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Import a local .ics event so Calendar view has real data.
	tomorrow := time.Now().Add(24 * time.Hour).UTC().Format("20060102")
	icsPath := filepath.Join(dir, "test.ics")
	icsContent := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//PostDive//Test//EN\r\nBEGIN:VEVENT\r\nUID:ui-test-1@postdive\r\nSUMMARY:Direkt-Test-Termin\r\nDTSTART:" + tomorrow + "T100000Z\r\nDTEND:" + tomorrow + "T110000Z\r\nLOCATION:Berlin\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if err := os.WriteFile(icsPath, []byte(icsContent), 0o600); err != nil {
		t.Fatalf("write ics: %v", err)
	}
	n, err := eng.ImportICSFile(context.Background(), mailengine.LocalCalendarID, icsPath)
	if err != nil || n != 1 {
		t.Fatalf("ImportICSFile: n=%d err=%v", n, err)
	}

	// Seed a contact so the Contacts workspace & compose autocomplete have live data.
	if _, err := eng.SaveContact(context.Background(), backend.ContactRecord{
		DisplayName: "Erika Mustermann",
		Email:       "erika@beispiel.de",
		Company:     "Beispiel GmbH",
		Phone:       "+49 30 123456",
	}); err != nil {
		t.Fatalf("SaveContact: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := triage.NewStoreAt(dir)
	bc := backend.New(backend.Options{Engine: eng, Events: eng.Events(), Store: store, LoadConfig: loadCfg, Version: "test"})
	app := NewApp(Options{
		ConfigLoader: loadCfg,
		ConfigSaver:  saveCfg,
		Store:        store,
		Backend:      bc,
		Direct:       eng,
	})
	if !app.isDirectMode() {
		t.Fatalf("expected direct mode to be active")
	}

	updates := make(chan func(), 64)
	app.SetHooks(func(fn func()) {
		updates <- fn
	}, nil, nil, nil, nil)

	drainUpdates := func(waitFor func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !waitFor() {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("timed out waiting for UI update condition")
			}
		}
		for {
			select {
			case fn := <-updates:
				fn()
			default:
				return
			}
		}
	}

	app.RefreshAll(ctx)
	drainUpdates(func() bool { return len(app.events) > 0 })

	tt := ui.NewTester(app.RenderMain, 1280, 820)

	// 1. Onboarding card (no IMAP accounts yet) & PostDive branding
	tt.Frame()
	if _, ok := tt.Find("PostDive"); !ok {
		t.Fatalf("expected PostDive header")
	}
	if _, ok := tt.Find("IMAP/SMTP-Konto verbinden"); !ok {
		t.Fatalf("expected PostDive onboarding button")
	}
	if _, ok := tt.Find("Verbunden (IMAP)"); !ok {
		t.Fatalf("expected Verbunden (IMAP) footer status")
	}

	// 2. Calendar workspace: Day default + persistence + Month/Week/Day/Agenda + Event Edit/Recurrence modal
	if app.calViewMode != 2 {
		t.Fatalf("expected default calendar view to be Day (2), got %d", app.calViewMode)
	}
	app.calViewMode = 1 // Week
	app.persistCalendarView()
	if cfg.App.CalendarView != "week" {
		t.Fatalf("expected persisted CalendarView='week', got %q", cfg.App.CalendarView)
	}
	if got := calendarViewModeFromName(cfg.App.CalendarView); got != 1 {
		t.Fatalf("calendarViewModeFromName('week') = %d, want 1", got)
	}
	app.navID = "calendar"
	app.calViewMode = 0 // Month Grid
	tt.Frame()
	if _, ok := tt.Find("Google / .ics"); !ok {
		t.Fatalf("expected Google / .ics button in calendar workspace")
	}
	if _, ok := tt.Find("Monat"); !ok {
		t.Fatalf("expected Month/Week/Agenda mode switcher in calendar workspace")
	}
	hasText := func(substr string) bool {
		for _, txt := range tt.Texts() {
			if strings.Contains(txt, substr) {
				return true
			}
		}
		return false
	}
	if !hasText("Direkt-Test-Termin") {
		t.Fatalf("expected imported .ics event in calendar month grid; texts=%v", tt.Texts())
	}
	app.calCursor = time.Now().Add(24 * time.Hour)
	app.calViewMode = 1 // Week Grid
	tt.Frame()
	if !hasText("Direkt-Test-Termin") {
		t.Fatalf("expected imported .ics event in calendar week grid; texts=%v", tt.Texts())
	}
	app.calViewMode = 2 // Day Timeline
	tt.Frame()
	if !hasText("Direkt-Test-Termin") {
		t.Fatalf("expected imported .ics event in calendar day view; texts=%v", tt.Texts())
	}
	// Switching Day view to today shows the live current-time badge (HH:MM)
	app.calCursor = time.Now()
	app.calDayScrolledKey = ""
	tt.Frame()
	if _, ok := tt.Find(time.Now().Format("15:04")); !ok {
		t.Fatalf("expected live red current-time badge in Day view for today")
	}
	app.calViewMode = 3 // Agenda
	tt.Frame()
	if _, ok := tt.Find("Direkt-Test-Termin"); !ok {
		t.Fatalf("expected imported .ics event in calendar agenda view")
	}
	// Open edit event modal with recurrence rule
	app.OpenEditEventModal(backend.CalendarEvent{
		ID:             "ev-edit-1",
		CalendarID:     mailengine.LocalCalendarID,
		Title:          "Wöchentlicher Sync",
		Start:          tomorrow + "T09:00:00Z",
		End:            tomorrow + "T10:00:00Z",
		RecurrenceRule: "FREQ=WEEKLY;COUNT=6",
	})
	if app.eventRecurrence != 2 || app.eventRecCount != "6" {
		t.Fatalf("expected weekly recurrence with count 6, got rec=%d cnt=%q", app.eventRecurrence, app.eventRecCount)
	}
	tt.Frame()
	if _, ok := tt.Find("Termin bearbeiten"); !ok {
		t.Fatalf("expected Edit Calendar Event modal title")
	}
	if _, ok := tt.Find("Wöchentlich"); !ok {
		t.Fatalf("expected recurrence options in event modal")
	}
	app.eventOpen = false

	// 3. Contacts workspace (search, details, 1-click compose, VCF & Google Sync)
	app.navID = "contacts"
	app.LoadContacts(ctx)
	drainUpdates(func() bool { return len(app.contacts.list) > 0 })
	tt.Frame()
	if _, ok := tt.Find("Erika Mustermann"); !ok {
		t.Fatalf("expected Erika Mustermann in contacts workspace; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("Google Sync"); !ok {
		t.Fatalf("expected Google Sync button in contacts workspace")
	}
	if _, ok := tt.Find(".vcf Import/Export"); !ok {
		t.Fatalf("expected .vcf Import/Export button in contacts workspace")
	}

	// 4. Reply All, Forward, Draft editing, BCC, and .ics RSVP banner in Mail Reader
	app.navID = "focus"
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{
			ID:      "m-ics",
			Subject: "Projekt Kickoff",
			From:    "Max <max@beispiel.de>",
			To:      []string{"me@beispiel.de", "team@beispiel.de"},
			CC:      []string{"boss@beispiel.de"},
			Folder:  "Drafts",
		},
		BodyText:  "Bitte Termin bestätigen.",
		HasICS:    true,
		ICSInvite: &backend.CalendarEvent{Title: "Kickoff Meeting", Start: tomorrow + "T14:00:00Z", Location: "Raum 4", Organizer: "max@beispiel.de"},
	}
	tt.Frame()
	if _, ok := tt.Find("Entwurf weiterbearbeiten"); !ok {
		t.Fatalf("expected Edit Draft button for message in Drafts folder")
	}
	if _, ok := tt.Find("Zusagen"); !ok {
		t.Fatalf("expected .ics Accept (Zusagen) RSVP button in reader")
	}
	if _, ok := tt.Find("Vielleicht"); !ok {
		t.Fatalf("expected .ics Tentative (Vielleicht) RSVP button in reader")
	}
	if _, ok := tt.Find("Absagen"); !ok {
		t.Fatalf("expected .ics Decline (Absagen) RSVP button in reader")
	}
	if _, ok := tt.Find("Nur übernehmen"); !ok {
		t.Fatalf("expected .ics Import Only button in reader")
	}
	if _, ok := tt.Find("Kickoff Meeting"); !ok {
		t.Fatalf("expected .ics event title in invitation banner")
	}

	// Verify RSVP status badge, target calendar selector, smart default selection, and hidden Import Only.
	app.accounts = []backend.Account{{ID: "acc-work", Name: "frank.hess@avono.de", Identities: []backend.AccountIdentity{{ID: "id-1", Email: "frank.hess@avono.de"}}}}
	app.calendars = []backend.Calendar{
		{ID: mailengine.LocalCalendarID, Name: "Persönlich"},
		{ID: "gcal-work", Name: "Frank Hess (avono AG)"},
		{ID: "gcal-fam", Name: "Frank und Simone"},
	}
	app.eventCalLabels = []string{"Persönlich", "Frank Hess (avono AG)", "Frank und Simone"}
	app.eventCalByLabel = map[string]string{
		"Persönlich":            mailengine.LocalCalendarID,
		"Frank Hess (avono AG)": "gcal-work",
		"Frank und Simone":      "gcal-fam",
	}
	app.selectedMsg.AccountID = "acc-work"
	app.selectedMsg.ICSInvite.ResponseStatus = ""
	app.selectedMsg.ICSInvite.InCalendarName = ""
	app.icsTargetMsgID = ""
	tt.Frame()
	if app.icsTargetCal != "Frank Hess (avono AG)" {
		t.Fatalf("expected smart default target calendar 'Frank Hess (avono AG)' for frank.hess@avono.de, got %q", app.icsTargetCal)
	}
	if _, ok := tt.Find("Kalender:"); !ok {
		t.Fatalf("expected calendar selector label in .ics banner when multiple writable calendars exist")
	}

	app.selectedMsg.ICSInvite.ResponseStatus = "accepted"
	app.selectedMsg.ICSInvite.InCalendarName = "Frank Hess (avono AG)"
	tt.Frame()
	if _, ok := tt.Find("✓ Zugesagt · Frank Hess (avono AG)"); !ok {
		t.Fatalf("expected accepted status badge with calendar name in .ics banner")
	}
	if _, ok := tt.Find("Zugesagt"); !ok {
		t.Fatalf("expected accepted button label in .ics banner")
	}
	if _, ok := tt.Find("Nur übernehmen"); ok {
		t.Fatalf("expected Import Only button to be hidden when selected calendar already contains the event")
	}
	// Switching target calendar to a different calendar re-enables Import Only.
	app.icsTargetCal = "Frank und Simone"
	tt.Frame()
	if _, ok := tt.Find("Nur übernehmen"); !ok {
		t.Fatalf("expected Import Only button to appear when choosing a different target calendar")
	}

	// Test Reply All, Forward, Draft edit & BCC prefill
	app.OpenComposeReplyAll(app.selectedMsg, "")
	if !strings.Contains(app.composeTo, "max@beispiel.de") || !strings.Contains(app.composeCC, "boss@beispiel.de") {
		t.Fatalf("Reply All did not populate To/CC: to=%q cc=%q", app.composeTo, app.composeCC)
	}
	app.composeBCC = "hidden@beispiel.de"
	req := app.buildComposeReq(false)
	if len(req.BCC) != 1 || req.BCC[0] != "hidden@beispiel.de" {
		t.Fatalf("expected BCC in compose request, got %+v", req.BCC)
	}
	app.OpenComposeFromDraft(app.selectedMsg)
	if app.composeReplyType != "draft" || app.composeSubject != "Projekt Kickoff" {
		t.Fatalf("OpenComposeFromDraft did not populate draft fields: type=%q sub=%q", app.composeReplyType, app.composeSubject)
	}
	app.OpenComposeForward(app.selectedMsg)
	if !strings.HasPrefix(app.composeSubject, "Fwd: ") || !strings.Contains(app.composeBody, "Bitte Termin bestätigen.") {
		t.Fatalf("Forward did not populate subject/body: sub=%q body=%q", app.composeSubject, app.composeBody)
	}
	app.composeOpen = false

	// 5. Accounts button is no longer in the top toolbar; OpenDirectModal opens unified Settings on the Accounts tab.
	tt.Frame()
	if _, ok := tt.Find("Konten"); ok {
		t.Fatalf("did not expect standalone 'Konten' button in top toolbar; texts=%v", tt.Texts())
	}
	app.OpenDirectModal(0)
	if !app.settings.open || app.settings.tab != settingsTabAccounts || app.direct.open {
		t.Fatalf("expected OpenDirectModal(0) to open unified Settings on Accounts tab, got settings.open=%v tab=%d direct.open=%v", app.settings.open, app.settings.tab, app.direct.open)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for reloadDirectState")
	}
	tt.Frame()
	if _, ok := tt.Find("Einstellungen"); !ok {
		t.Fatalf("expected unified Settings modal title")
	}
	if _, ok := tt.Find("Neues IMAP/SMTP-Konto hinzufügen"); !ok {
		t.Fatalf("expected IMAP/SMTP form in tab 0")
	}
	if _, ok := tt.Find("Verbindung testen"); !ok {
		t.Fatalf("expected Test Connection button in tab 0")
	}
	app.direct.tab = 3
	tt.Frame()
	if _, ok := tt.Find("Thunderbird-Profil auswählen"); !ok {
		t.Fatalf("expected Thunderbird profile importer in tab 3")
	}
	if _, ok := tt.Find("Ordner wählen…"); !ok {
		t.Fatalf("expected native folder picker button in tab 3")
	}

	app.direct.tab = 1
	tt.Frame()
	if _, ok := tt.Find("Google Kalender (Bidirektional)"); !ok {
		t.Fatalf("expected Google Calendar section in tab 1")
	}
	if _, ok := tt.Find("iCal (.ics) Import & Kalender-Abos"); !ok {
		t.Fatalf("expected iCal section in tab 1")
	}

	app.direct.tab = 2
	tt.Frame()
	if _, ok := tt.Find("Noch keine Filterregeln gespeichert."); !ok {
		t.Fatalf("expected filter rules empty state in tab 2")
	}
}
