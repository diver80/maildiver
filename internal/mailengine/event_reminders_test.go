package mailengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func newReminderTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

func createTestEvent(t *testing.T, eng *Engine, title string, start time.Time, reminder int) backend.CalendarEvent {
	t.Helper()
	raw, err := eng.Call(context.Background(), "calendar.create", backend.CreateCalendarEventRequest{
		CalendarID:      LocalCalendarID,
		Title:           title,
		Start:           start.Format(time.RFC3339),
		End:             start.Add(30 * time.Minute).Format(time.RFC3339),
		ReminderMinutes: reminder,
	})
	if err != nil {
		t.Fatalf("calendar.create %s: %v", title, err)
	}
	var res struct {
		Event backend.CalendarEvent `json:"event"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res.Event
}

// B5: a local event keeps its own reminder, and calendar.update changes it.
func TestLocalEventKeepsReminder(t *testing.T) {
	eng := newReminderTestEngine(t)
	start := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	ev := createTestEvent(t, eng, "Zahnarzt", start, 30)
	if ev.ReminderMinutes != 30 {
		t.Fatalf("created with reminder %d, want 30", ev.ReminderMinutes)
	}
	raw, err := eng.Call(context.Background(), "calendar.update", map[string]any{
		"calendarId": LocalCalendarID, "id": ev.ID, "reminderMinutes": backend.ReminderNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reminderMinutes":-1`) {
		t.Fatalf("update did not turn the reminder off: %s", raw)
	}
	stored, err := eng.store.GetEvent(context.Background(), LocalCalendarID, ev.ID)
	if err != nil || stored.ReminderMin != ReminderNone {
		t.Fatalf("stored reminder %d (%v), want none", stored.ReminderMin, err)
	}
}

// B5: CalDAV keeps the reminder as a VALARM, and "none" survives the round trip.
func TestICSReminderRoundTrip(t *testing.T) {
	start := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for _, want := range []int{15, 24 * 60, ReminderNone, 0} {
		ics := ExportEventsICS("Team", []StoredEvent{{UID: "u1@example.org", Title: "Planung", StartUnix: start.Unix(), EndUnix: start.Add(time.Hour).Unix(), ReminderMin: want}})
		if want > 0 && !strings.Contains(ics, "BEGIN:VALARM") {
			t.Fatalf("reminder %d: no VALARM in\n%s", want, ics)
		}
		evs, err := ParseICS("cal", "Team", false, strings.NewReader(ics))
		if err != nil || len(evs) != 1 {
			t.Fatalf("parse: %v (%d events)", err, len(evs))
		}
		if evs[0].ReminderMin != want {
			t.Fatalf("round trip of %d gave %d", want, evs[0].ReminderMin)
		}
	}
}

// B5: the background wake-up reminds of what starts soon, once, honours the reminder per event
// and leaves out hidden calendars; what the open app reminded of is not reminded again.
func TestTakeDueEventReminders(t *testing.T) {
	eng := newReminderTestEngine(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)
	soon := createTestEvent(t, eng, "Standup", now.Add(12*time.Minute), 0)           // default 10 min: due within the wake-up's 20 min
	createTestEvent(t, eng, "Später", now.Add(3*time.Hour), 0)                       // not yet
	createTestEvent(t, eng, "Ohne Erinnerung", now.Add(5*time.Minute), ReminderNone) // never
	early := createTestEvent(t, eng, "Flug", now.Add(50*time.Minute), 60)            // 1 hour ahead
	appDone := createTestEvent(t, eng, "Schon erinnert", now.Add(8*time.Minute), 0)

	if err := eng.MarkEventsReminded(ctx, []backend.CalendarEvent{appDone}); err != nil {
		t.Fatal(err)
	}
	due, err := eng.TakeDueEventReminders(ctx, now, 20*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, ev := range due {
		titles = append(titles, ev.Title)
	}
	if strings.Join(titles, ",") != soon.Title+","+early.Title {
		t.Fatalf("due %v, want Standup and Flug", titles)
	}
	if again, _ := eng.TakeDueEventReminders(ctx, now.Add(time.Minute), 20*time.Minute, nil); len(again) != 0 {
		t.Fatalf("reminded twice: %v", again)
	}
	done, err := eng.RemindedEvents(ctx)
	if err != nil || !done[EventReminderKey(soon)] || !done[EventReminderKey(appDone)] {
		t.Fatalf("reminded set %v (%v)", done, err)
	}

	other := createTestEvent(t, eng, "Versteckt", now.Add(9*time.Minute), 0)
	if due, _ := eng.TakeDueEventReminders(ctx, now, 20*time.Minute, []string{other.CalendarID}); len(due) != 0 {
		t.Fatalf("hidden calendar reminded: %v", due)
	}
}

// The settings' default applies to events without a reminder of their own; declined events and
// a default of "none" do not remind.
func TestDueRemindersFollowDefaultAndSkipDeclined(t *testing.T) {
	eng := newReminderTestEngine(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)
	def := createTestEvent(t, eng, "Standard", now.Add(25*time.Minute), 0)
	own := createTestEvent(t, eng, "Eigene", now.Add(25*time.Minute), 30)
	declined := createTestEvent(t, eng, "Abgesagt", now.Add(5*time.Minute), 30)
	eng.store.SetEventSelfResponse(ctx, declined.ID, "declined")

	cfg := eng.loadConfig()
	none := 0
	cfg.App.EventReminderMinutes = &none
	_ = eng.saveConfig(cfg)
	due, _ := eng.TakeDueEventReminders(ctx, now, time.Minute, nil)
	if len(due) != 1 || due[0].Title != own.Title {
		t.Fatalf("default off: only the event's own reminder is due, got %v", due)
	}

	thirty := 30
	cfg.App.EventReminderMinutes = &thirty
	_ = eng.saveConfig(cfg)
	due, _ = eng.TakeDueEventReminders(ctx, now, time.Minute, nil)
	if len(due) != 1 || due[0].Title != def.Title {
		t.Fatalf("default 30 min: the default event is due, got %v", due)
	}
}
