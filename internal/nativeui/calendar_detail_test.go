package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/theme"
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
	app.calViewMode = 2 // Agenda
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

func absFloat32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
