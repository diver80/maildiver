package nativeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/theme"
)

func TestDiverThemeDefaultsAndNeutralMutedText(t *testing.T) {
	cfg := config.Default()
	if cfg.Theme.Preset != "diver" || cfg.Theme.DarkFallback != "diver" || cfg.Theme.LightFallback != "diver-light" {
		t.Fatalf("expected Diver theme defaults, got %+v", cfg.Theme)
	}
	diver, ok := theme.GetPreset("diver")
	if !ok || diver.IsLight || diver.Background != "#18181b" || diver.Accent != "#3b82f6" {
		t.Fatalf("unexpected diver palette: %+v ok=%v", diver, ok)
	}
	diverLight, ok := theme.GetPreset("diver-light")
	if !ok || !diverLight.IsLight || diverLight.Background != "#ffffff" || diverLight.Accent != "#2563eb" {
		t.Fatalf("unexpected diver-light palette: %+v ok=%v", diverLight, ok)
	}
	th := PaletteToUITheme(diver)
	if th.TextMuted != ui.Hex("#a1a1aa") {
		t.Fatalf("expected neutral muted gray #a1a1aa in Diver theme, got %+v", th.TextMuted)
	}
}

func TestCalendarSidebarMiniMonthAndGroupedToggles(t *testing.T) {
	app, _, _ := keymapApp(t)
	updates := make(chan func(), 16)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)
	app.storeLang("de")
	app.navID = "calendar"
	app.calViewMode = 1 // Week view
	app.calCursor = time.Date(2026, 11, 27, 12, 0, 0, 0, time.Local)
	app.calendars = []backend.Calendar{
		{ID: "local-1", Name: "Persönlich", Type: "local", Color: "#3b82f6"},
		{ID: "gcal-1", Name: "Arbeit", Type: "google", Color: "#10b981"},
		{ID: "ical-1", Name: "Feiertage", Type: "ical", Color: "#f59e0b"},
	}

	tt := ui.NewTester(app.RenderMain, 1280, 860)
	tt.Frame()

	for _, want := range []string{"November 2026", "Lokal", "Google Kalender", "iCal-Abos", "Persönlich", "Arbeit", "Feiertage"} {
		if !tt.HasText(want) {
			t.Fatalf("expected %q in calendar sidebar; texts=%v", want, tt.Texts())
		}
	}

	// Clicking a calendar row hides it, clicking again restores it.
	if err := tt.Click("Arbeit"); err != nil {
		t.Fatalf("click Arbeit calendar toggle: %v", err)
	}
	tt.Frame()
	if !app.isCalendarHidden("gcal-1") {
		t.Fatal("expected clicking Arbeit in calendar sidebar to hide gcal-1")
	}
	if err := tt.Click("Arbeit"); err != nil {
		t.Fatalf("re-click Arbeit calendar toggle: %v", err)
	}
	tt.Frame()
	if app.isCalendarHidden("gcal-1") {
		t.Fatal("expected second click on Arbeit to show gcal-1 again")
	}

	// Clicking a mini-month day updates calCursor.
	if err := tt.Click("2026-11-18"); err != nil {
		t.Fatalf("click mini-month day: %v", err)
	}
	tt.Frame()
	if got := app.calCursor.Format("2006-01-02"); got != "2026-11-18" {
		t.Fatalf("expected calCursor=2026-11-18 after mini-month click, got %s", got)
	}
}

func TestLiveMeetingPillAndJoinAction(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.storeLang("en")
	now := time.Now()
	app.events = []backend.CalendarEvent{
		{
			ID:       "ev-later",
			Title:    "Far Future",
			Start:    now.Add(2 * time.Hour).Format(time.RFC3339),
			End:      now.Add(3 * time.Hour).Format(time.RFC3339),
			Location: "https://meet.google.com/aaa-bbbb-ccc",
		},
		{
			ID:             "ev-declined",
			Title:          "Declined Now",
			Start:          now.Add(-5 * time.Minute).Format(time.RFC3339),
			End:            now.Add(25 * time.Minute).Format(time.RFC3339),
			ResponseStatus: "declined",
			Location:       "https://meet.google.com/ddd-eeee-fff",
		},
		{
			ID:       "ev-live",
			Title:    "Q4 roadmap review",
			Start:    now.Add(-5 * time.Minute).Format(time.RFC3339),
			End:      now.Add(25 * time.Minute).Format(time.RFC3339),
			Location: "https://teams.microsoft.com/l/meetup-join/demo",
		},
	}

	ev, status, joinURL, _, ok := app.liveMeeting(now)
	if !ok || ev.ID != "ev-live" || status != "now" || joinURL == "" {
		t.Fatalf("unexpected liveMeeting: ev=%+v status=%q joinURL=%q ok=%v", ev, status, joinURL, ok)
	}

	var opened string
	prevOpen := openExternalURLFn
	openExternalURLFn = func(raw string) error {
		opened = raw
		return nil
	}
	defer func() { openExternalURLFn = prevOpen }()

	tt := ui.NewTester(app.RenderMain, 1280, 860)
	tt.Frame()
	if !tt.HasText("Q4 roadmap review") || !tt.HasText("now") || !tt.HasText("Join") {
		t.Fatalf("expected live meeting pill in desktop toolbar; texts=%v", tt.Texts())
	}
	if err := tt.Click("Join"); err != nil {
		t.Fatalf("click Join on live meeting pill: %v", err)
	}
	tt.Frame()
	if opened != "https://teams.microsoft.com/l/meetup-join/demo" {
		t.Fatalf("expected Join to open Teams URL, got %q", opened)
	}

	// Phone layout parity: slim live meeting bar should also show Join.
	opened = ""
	tp := ui.NewTester(app.RenderMain, 412, 915)
	tp.Frame()
	if !tp.HasText("Q4 roadmap review") || !tp.HasText("Join") {
		t.Fatalf("expected live meeting bar on phone; texts=%v", tp.Texts())
	}
}

func TestICSBannerFreeBusyConflictBadge(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.storeLang("en")
	start := time.Date(2026, 11, 28, 14, 0, 0, 0, time.Local)
	end := start.Add(time.Hour)
	msg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{ID: "m-fb", Subject: "Design critique"},
		HasICS:        true,
		ICSInvite: &backend.CalendarEvent{
			Title: "Design critique",
			Start: start.Format(time.RFC3339),
			End:   end.Format(time.RFC3339),
		},
	}

	// 1. No overlapping events (even when the same event is already in calendar) -> "You're free".
	app.events = []backend.CalendarEvent{
		{ID: "same", Title: "Design critique", Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)},
		{ID: "decl", Title: "Declined Overlap", Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), ResponseStatus: "declined"},
	}
	tt := ui.NewTester(func(c *ui.Context) { app.renderICSBanner(c, msg) }, 900, 300)
	tt.Frame()
	if !tt.HasText("✓ You're free") {
		t.Fatalf("expected free badge when no real conflict exists; texts=%v", tt.Texts())
	}

	// 2. Overlapping event -> warning conflict badge with event title.
	app.events = append(app.events, backend.CalendarEvent{
		ID:    "busy-1",
		Title: "Board prep",
		Start: start.Add(15 * time.Minute).Format(time.RFC3339),
		End:   start.Add(45 * time.Minute).Format(time.RFC3339),
	})
	tt.Frame()
	if !tt.HasText("⚠ Conflict: Board prep (14:15 – 14:45)") {
		t.Fatalf("expected conflict badge with overlapping event; texts=%v", tt.Texts())
	}

	// 3. When the loaded calendar window doesn't cover the invite (and there's no conflict), omit free/busy badge completely.
	app.events = app.events[:2]
	app.calLoadedStart = start.Add(48 * time.Hour)
	app.calLoadedEnd = start.Add(72 * time.Hour)
	tt.Frame()
	if tt.HasText("✓ You're free") || tt.HasText("Conflict") {
		t.Fatalf("expected no free/busy badge when invite lies outside loaded window; texts=%v", tt.Texts())
	}
}
