package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/theme"
)

func TestExtractEventLinksAndJoinDetection(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) {
		c := config.Default()
		c.Language = "de"
		return c, nil
	}})

	teamsEv := backend.CalendarEvent{
		ID:       "ev-teams",
		Title:    "MCP Server Security",
		Location: "Microsoft Teams-Besprechung",
		Description: "Hallo Frank, wie besprochen ein Termin für die MCP Sicherheit.\n" +
			"________________________________________________________________________________\n" +
			"Microsoft Teams-Besprechung\n" +
			"Nehmen Sie auf dem Computer teil: <https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc/0?context=1>\n" +
			"Besprechungs-ID: 345 678 901\n" +
			`<a href="javascript:alert(1)">Evil</a> <a href="https://docs.example.org/mcp-security">Agenda</a>`,
	}

	links := ExtractEventLinks(teamsEv)
	if len(links) != 2 {
		t.Fatalf("expected 2 safe links (Teams + docs), got %d: %+v", len(links), links)
	}
	joinURL, joinLabel := app.eventJoinLink(teamsEv)
	if !strings.Contains(joinURL, "teams.microsoft.com/l/meetup-join/") {
		t.Fatalf("expected Teams join URL, got %q", joinURL)
	}
	if !strings.Contains(joinLabel, "Teams") {
		t.Fatalf("expected Teams button label, got %q", joinLabel)
	}

	// Preview must strip underscore separator rules while keeping the Teams URL intact in full text.
	preview := cleanEventPreviewText(teamsEv.Description)
	if strings.Contains(preview, "________") {
		t.Fatalf("preview should collapse underscore rules: %q", preview)
	}
	full := cleanEventFullText(teamsEv.Description)
	if !strings.Contains(full, "Besprechungs-ID: 345 678 901") || !strings.Contains(full, "teams.microsoft.com/l/meetup-join/") {
		t.Fatalf("cleanEventFullText lost meeting details or URL: %q", full)
	}

	// Google Meet link detection
	meetEv := backend.CalendarEvent{
		ID:          "ev-meet",
		Title:       "Sync",
		Description: "https://meet.google.com/abc-defg-hij",
	}
	u, lbl := app.eventJoinLink(meetEv)
	if u != "https://meet.google.com/abc-defg-hij" || !strings.Contains(lbl, "Meet") {
		t.Fatalf("expected Google Meet join link, got %q (%q)", u, lbl)
	}
}

func TestCalendarAgendaExpandAndDetailModalRender(t *testing.T) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 10, 30, 0, 0, now.Location()).Format(time.RFC3339)
	end := time.Date(now.Year(), now.Month(), now.Day(), 11, 0, 0, 0, now.Location()).Format(time.RFC3339)

	ev := backend.CalendarEvent{
		ID:          "ev-1",
		CalendarID:  "cal-1",
		Calendar:    "Work (Acme Corp)",
		Title:       "MCP Server Security",
		Start:       start,
		End:         end,
		Location:    "Microsoft Teams-Besprechung",
		Description: "Hallo Frank, Termin.\n__________\n<https://teams.microsoft.com/l/meetup-join/19%3aabc/0>\nBesprechungs-ID: 999 111 222",
	}

	app := NewApp(Options{ConfigLoader: func() (config.Config, error) {
		c := config.Default()
		c.Language = "de"
		return c, nil
	}})
	app.engineReady = true
	app.navID = "calendar"
	app.calViewMode = 3 // Agenda
	app.events = []backend.CalendarEvent{ev}

	tt := ui.NewTester(app.RenderMain, 1400, 900)
	tt.Frame()
	if _, ok := tt.Find("Teams beitreten ↗"); !ok {
		t.Fatalf("expected direct Teams join button in agenda row; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("Details"); !ok {
		t.Fatalf("expected Details toggle button in agenda row")
	}

	// Expand inline and verify full meeting ID + links appear.
	app.toggleCalEventExpanded(calEventKey(ev))
	tt.Frame()
	if _, ok := tt.Find("Weniger"); !ok {
		t.Fatalf("expected expanded toggle label 'Weniger'")
	}
	if _, ok := tt.Find("Links in diesem Termin:"); !ok {
		t.Fatalf("expected links section when event is expanded; texts=%v", tt.Texts())
	}

	// Open read-only detail modal (used by Month/Week chips) and verify it renders cleanly.
	app.OpenEventDetails(ev)
	if !app.modalOpen() {
		t.Fatal("evDetailOpen must be reported by modalOpen()")
	}
	tt.Frame()
	if _, ok := tt.Find("Schließen"); !ok {
		t.Fatalf("expected event detail modal Close button")
	}
}

func TestMailBodySelectionHighlightContrastAndCopy(t *testing.T) {
	// Verify every preset palette (including Matte Black) has a clearly visible text selection highlight.
	for _, pal := range theme.ListPresets() {
		th := PaletteToUITheme(pal)
		if th.Selection.A < 60 {
			t.Fatalf("palette %q selection alpha too faint: %+v", pal.Name, th.Selection)
		}
		// Selection must not be a near-identical dark gray to Background.
		bg := th.Background
		sel := th.Selection
		a := float32(sel.A) / 255.0
		blendedR := float32(sel.R)*a + float32(bg.R)*(1-a)
		blendedG := float32(sel.G)*a + float32(bg.G)*(1-a)
		blendedB := float32(sel.B)*a + float32(bg.B)*(1-a)
		diff := (absFloat32(blendedR-float32(bg.R)) + absFloat32(blendedG-float32(bg.G)) + absFloat32(blendedB-float32(bg.B))) / 255.0
		if diff < 0.14 {
			t.Fatalf("palette %q selection highlight blends invisibly into background (diff=%.3f, bg=%+v, sel=%+v)", pal.Name, diff, bg, sel)
		}
	}

	// Verify drag-selecting & copying a token from the mail body works via mygo's Selectable markdown.View.
	app := NewApp(Options{})
	app.engineReady = true
	app.navID = "focus"
	msg := &backend.MessageDetail{
		ID:       "tok-1",
		Subject:  "Verification Code",
		From:     "auth@example.com",
		BodyText: "Here is your login token: TOK-984721-XYZ",
	}
	app.selectedID = msg.ID
	app.selectedMsg = msg

	tt := ui.NewTester(app.RenderMain, 1280, 800)
	tt.Frame()
	r, ok := tt.Find("Here is your login token: TOK-984721-XYZ")
	if !ok {
		t.Fatalf("token text not rendered in mail body; texts=%v", tt.Texts())
	}
	// Simulate dragging across the paragraph (WITHOUT Cmd+A) and pressing Cmd+C.
	tt.Press(r.X+2, r.Y+4)
	tt.Move(r.X+r.W-2, r.Y+r.H-4)
	tt.Release(r.X+r.W-2, r.Y+r.H-4)
	tt.Key(ui.Cmd, ui.KeyC)
	clip := tt.Clipboard()
	if clip == "" {
		if b, err := tt.ClipboardData().Read("text/plain"); err == nil {
			clip = string(b)
		}
	}
	if !strings.Contains(clip, "TOK-984721-XYZ") {
		t.Fatalf("expected mouse-drag + Cmd+C to copy token, got clipboard=%q data=%+v", clip, tt.ClipboardData())
	}

	// Verify prominent sidebar compose button opens Compose modal.
	if _, ok := tt.Find("New Email"); !ok {
		t.Fatalf("expected sidebar 'New Email' compose button; texts=%v", tt.Texts())
	}
}

func TestCalendarDayViewLayoutAndNowLine(t *testing.T) {
	loc := time.Local
	day := time.Date(2026, 11, 23, 0, 0, 0, 0, loc)
	mk := func(id, title string, sh, sm, eh, em int) backend.CalendarEvent {
		return backend.CalendarEvent{
			ID:       id,
			Title:    title,
			Calendar: "Work",
			Start:    time.Date(2026, 11, 23, sh, sm, 0, 0, loc).Format(time.RFC3339),
			End:      time.Date(2026, 11, 23, eh, em, 0, 0, loc).Format(time.RFC3339),
		}
	}
	blocks := layoutDayEvents([]backend.CalendarEvent{
		{ID: "ad", Title: "Feiertag", AllDay: true, Start: "2026-11-23"},
		mk("e1", "Standup", 9, 0, 9, 30),
		mk("e2", "Overlap Call", 9, 15, 10, 0),
		mk("e3", "Nachmittag", 14, 0, 15, 0),
	}, day)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 timed blocks (all-day excluded), got %d", len(blocks))
	}
	if blocks[0].numCols != 2 || blocks[1].numCols != 2 || blocks[0].col == blocks[1].col {
		t.Fatalf("expected overlapping morning events in 2 distinct columns, got %+v and %+v", blocks[0], blocks[1])
	}
	if blocks[2].numCols != 1 || blocks[2].col != 0 || blocks[2].startMin != 14*60 {
		t.Fatalf("expected non-overlapping afternoon event at 14:00 full width, got %+v", blocks[2])
	}

	// Verify Day view renders all-day strip, timed event, and live red time indicator on today.
	now := time.Now()
	today := now.Format("2006-01-02")
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) {
		c := config.Default()
		c.Language = "de"
		return c, nil
	}})
	app.engineReady = true
	app.navID = "calendar"
	app.calViewMode = 2 // Day Timeline
	app.calCursor = now
	app.events = []backend.CalendarEvent{
		{ID: "ad1", Title: "Urlaubstag", AllDay: true, Start: today},
		{ID: "t1", Title: "Tages-Meeting", Calendar: "Work", Start: now.Format(time.RFC3339), End: now.Add(45 * time.Minute).Format(time.RFC3339)},
	}

	tt := ui.NewTester(app.RenderMain, 1280, 820)
	tt.Frame()
	if _, ok := tt.Find("Urlaubstag"); !ok {
		t.Fatalf("expected all-day chip in Day view")
	}
	if _, ok := tt.Find("Tages-Meeting"); !ok {
		t.Fatalf("expected timed event block in Day view")
	}
	if _, ok := tt.Find(now.Format("15:04")); !ok {
		t.Fatalf("expected red current-time badge %q in Day view", now.Format("15:04"))
	}
}

func absFloat32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Outlook writes every link twice – as a link and as "<https://…>" text, the query with &amp; –
// and the details list each once.
func TestEventLinksDeduplicated(t *testing.T) {
	ev := backend.CalendarEvent{Description: `Microsoft Teams Meeting <https://teams.microsoft.com/l/meetup-join/19%3ameeting_X/0?context=%7b%7d>
Hilfe<https://aka.ms/JoinTeamsMeeting?omkt=de-DE> | Optionen<https://teams.microsoft.com/meetingOptions/?organizerId=1&amp;tenantId=2>
<a href="https://aka.ms/JoinTeamsMeeting?omkt=de-DE">&lt;https://aka.ms/JoinTeamsMeeting?omkt=de-DE&gt;</a>
<a href="https://teams.microsoft.com/meetingOptions?tenantId=2&organizerId=1">Optionen</a>`}
	links := ExtractEventLinks(ev)
	if len(links) != 3 {
		t.Fatalf("expected 3 distinct links, got %d: %+v", len(links), links)
	}
	for _, lk := range links {
		if strings.HasPrefix(lk.Label, "<") || strings.Contains(lk.URL, "&amp;") {
			t.Fatalf("link not cleaned: %+v", lk)
		}
	}
}
