package nativeui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

// TestRenderVideo records a 1080p feature tour video of PostDive into POSTDIVE_VIDEO:
//
//	POSTDIVE_VIDEO=dist/video go test -v -timeout 10m ./internal/nativeui -run TestRenderVideo
func TestRenderVideo(t *testing.T) {
	dir := os.Getenv("POSTDIVE_VIDEO")
	if dir == "" {
		t.Skip("POSTDIVE_VIDEO not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	realHome, _ := os.UserHomeDir()
	ffPath := findFFmpeg(t, realHome)

	// Isolate environment from host user config/paths.
	t.Setenv("HOME", "/Users/you")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	app, updates := sampleVideoApp(t)
	icon := decodeAppIcon(t)

	v := newVideo(t, ffPath, filepath.Join(dir, "postdive-tour.mp4"), updates)
	v.isDark = true
	win := v.window(app.RenderMain)

	// Card 0: Overview Architecture & Features Card (also exported as overview.png)
	v.card(overviewCard(icon))
	v.snap(filepath.Join(dir, "overview.png"))
	v.hold(4.4)

	// Scene 1: Diver Dark Focus Inbox, Avatars, Filter Pills, Tracker Counter & Live-Meeting Pill
	v.scene(win,
		"Ruhiger Fokus-Posteingang im Diver-Design",
		"Absender-Avatare, Filter-Pills, blockierte Tracker, Live-Meeting-Pill & Schnellantwort",
	)
	v.pointer(vidW/2, vidH-140)
	v.hold(0.5)
	v.snap(filepath.Join(dir, "poster.png"))
	v.click("Ungelesen")
	v.hold(1.1)
	v.click("★")
	v.hold(0.9)
	unr, _ := v.win.tt.Find("Ungelesen")
	v.moveTo(unr.X-34, unr.Y+unr.H/2, 0.45)
	v.step(2)
	v.active.tt.ClickAt(unr.X-34, unr.Y+unr.H/2)
	v.rx, v.ry, v.ripple = v.px, v.py, 14
	v.step(8)
	v.hold(0.8)
	v.hover("1 Tracker blockiert", 1.1)

	// Scene 2: Consistent Right-Side AI Dock in Main Workspace
	v.scene(win,
		"Einheitliches KI-Panel rechts",
		"Zusammenfassen, Übersetzen, Termin- & Kontakterkennung + Copilot-Chat an einem festen Ort",
	)
	v.click("KI")
	v.hold(0.7)
	v.hover("Zusammenfassen", 0.9)
	v.clickVisual("Zusammenfassen", func() {
		app.inspectorSeq++
		app.inspectorLoading = false
		app.inspectorMode = "summarize"
		app.inspectorResult = "**Kernaussage:** Alice bittet um Budget-Freigabe f\u00fcr das Q4-Rollout (**17.800 EUR**) bis Freitag und schl\u00e4gt ein kurzes Gespräch heute um **14:00 Uhr** vor.\n\n- **Frist:** Freitag\n- **N\u00e4chster Schritt:** Termin um 14:00 best\u00e4tigen."
		app.inspectorDraft = "Hallo Alice,\n\n14:00 Uhr passt perfekt — wir geben die 17.800 EUR im Call frei.\n\nViele Grüße\nAlex"
	})
	v.hold(2.4)

	// Scene 3: Detached-Style Reply Modal with Right-Side AI Assist
	v.scene(win,
		"Antworten & Verfassen mit KI-Hilfe rechts",
		"Konsistentes KI-Panel im Antworten-Fenster: Entwerfen, Verfeinern, Übersetzen & 1-Klick-Übernehmen",
	)
	v.click("Im großen Antwort-Fenster öffnen")
	v.hold(0.6)
	v.click("KI-Hilfe")
	v.hold(0.6)
	v.clickVisual("Antwort entwerfen", func() {
		app.composeAISeq++
		app.composeAILoading = false
		app.composeAIMode = "draft"
		app.composeAIResult = "Hallo Alice,\n\nvielen Dank f\u00fcr die finale Q4-Kalkulation! Die 17.800 EUR sind freigegeben — lass uns um 14:00 Uhr im Zoom-Call kurz abstimmen.\n\nViele Gr\u00fc\u00dfe\nAlex"
		app.composeAIReview = []string{"Tonfall freundlich & klar · Termin 14:00 best\u00e4tigt."}
	})
	v.hold(1.6)
	v.click("Übernehmen")
	v.hold(1.5)
	app.closeCompose()
	app.clearInspectorAI()
	app.showCopilot = false
	v.active.tt.Frame()
	v.step(4)

	// Scene 4: Snooze, Waiting-for-Reply & Subscriptions Cleanup
	v.scene(win,
		"Zurückstellen, Warten & Abos aufräumen",
		"Snooze mit Auto-Wiedervorlage, 0-Token-Warteliste & List-Unsubscribe-Aufräumer",
	)
	v.click("Action / Warten")
	v.hold(1.4)
	v.click("Zurückgestellt")
	v.hold(1.3)
	v.clickVisual("Abos aufräumen", func() {
		app.subs.seq++
		app.subs.open = true
		app.subs.loading = false
		app.subs.err = ""
		app.subs.items = []backend.SubscriptionSummary{
			{
				AccountID:        "acc1",
				SenderEmail:      "digest@engineering.example",
				SenderName:       "Daily Engineering Digest",
				Count:            14,
				UnreadCount:      5,
				OneClick:         true,
				UnsubscribeHTTPS: "https://engineering.example/unsub",
				MessageIDs:       []string{"s1", "s2"},
			},
			{
				AccountID:        "acc1",
				SenderEmail:      "security@cloud-alerts.example",
				SenderName:       "Cloud Security Weekly",
				Count:            8,
				UnreadCount:      2,
				OneClick:         true,
				UnsubscribeHTTPS: "https://cloud-alerts.example/unsub",
				MessageIDs:       []string{"s3"},
			},
			{
				AccountID:         "acc1",
				SenderEmail:       "updates@saas-tool.example",
				SenderName:        "Product Changelog",
				Count:             4,
				UnsubscribeMailto: "mailto:unsub@saas-tool.example",
				MessageIDs:        []string{"s4"},
			},
		}
	})
	v.hold(2.4)
	v.click("✕")
	app.subs.open = false
	v.active.tt.Frame()
	v.click("Fokus (Ungelesen & ★)")
	v.hold(0.4)

	// Scene 5: Cron-Style Calendar (Week & 24h Day Grid + Sidebar + Live Meeting)
	v.scene(win,
		"Kalender im Cron-Stil mit Live-Meeting-Pill",
		"24h-Wochen- & Tagesraster, einklappbare Mini-Monat-Seitenleiste, Kalender-Filter & Google/CalDAV",
	)
	app.calViewMode = 1 // Week Grid
	app.calCursor = time.Now()
	app.calMiniCursor = time.Date(app.calCursor.Year(), app.calCursor.Month(), 1, 12, 0, 0, 0, time.Local)
	v.click("Kalender")
	v.hold(1.8)
	v.click("Arbeit")
	v.hold(1.0)
	v.click("Arbeit")
	v.hold(0.9)
	v.click("Tag")
	v.hold(2.0)

	// Scene 6: 1-Click MCP Client Setup & Diver Light Theme
	v.scene(win,
		"1-Klick-MCP-Setup & Diver Light Design",
		"Claude Desktop, Cursor & Antigravity per Klick verbinden — hell oder dunkel nach Wahl",
	)
	v.key(ui.Cmd, ui.KeyComma)
	app.settings.tab = settingsTabAI
	app.settings.mcpProbed = true
	app.settings.mcpCmd = "/Applications/PostDive.app/Contents/MacOS/PostDive"
	app.settings.mcpTargets = []mcpClientTarget{
		{ID: "claude-desktop", Name: "Claude Desktop", ConfigPath: "/Users/you/Library/Application Support/Claude/claude_desktop_config.json"},
		{ID: "cursor", Name: "Cursor", ConfigPath: "/Users/you/.cursor/mcp.json"},
		{ID: "antigravity", Name: "Antigravity", ConfigPath: "/Users/you/.gemini/antigravity/mcp_config.json"},
	}
	app.settings.mcpInstalled = map[string]bool{"claude-desktop": true}
	v.active.tt.Frame()
	// Smoothly scroll Settings AI tab down so 1-Click MCP client setup buttons sit clearly in the viewport.
	v.active.tt.Move(float32(winLW)/2, float32(winLH)/2)
	for range 12 {
		if r, ok := v.active.tt.Find("Cursor"); ok && r.Y >= 240 && r.Y <= 580 {
			break
		}
		v.active.tt.Wheel(float32(winLW)/2, float32(winLH)/2, 0, 90)
		v.step(4)
	}
	v.step(4)
	v.hold(1.0)
	v.clickVisual("Cursor", func() {
		app.settings.mcpInstalled["cursor"] = true
	})
	v.hold(1.5)
	app.settings.open = false
	v.active.tt.Frame()
	v.switchTheme(app, "Diver Light (diver-light)", false)
	app.calViewMode = 1
	v.active.tt.Frame()
	v.pointer(vidW/2, vidH-140)
	v.hold(2.2)

	// Outro Card
	v.card(titleCard(
		icon,
		"PostDive 0.8.0",
		"brew install --cask diver80/tap/postdive",
		"Open Source (MIT) · github.com/diver80/postdive",
	))
	v.hold(3.8)

	v.finish()
	t.Logf("rendered video: %d frames (%.1f s)", v.nframes, float64(v.nframes)/vidFPS)
}

func sampleVideoApp(t *testing.T) (*App, chan func()) {
	t.Helper()
	store := triage.NewStoreAt(t.TempDir())
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	cfg.Theme.Mode = "preset"
	cfg.Theme.Preset = "diver"

	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        store,
	})
	updates := make(chan func(), 64)
	app.SetHooks(func(fn func()) {
		if fn != nil {
			updates <- fn
		}
	}, nil, nil, nil, nil)

	now := time.Now()
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	app.engineReady = true
	app.readOnly = false
	app.providerLabel = "anthropic (api)"
	app.agentBackend = "anthropic"
	app.agentModel = "claude-sonnet-4-5"
	app.modelChoices = []string{"claude-sonnet-4-5", "gemini-2.5-flash", "mtplx-inkling-2-mini"}
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work (IMAP)"}, {ID: "acc2", Name: "Privat"}}
	app.lastSync = now.Add(-1 * time.Minute)
	app.accountLabels = []string{"Alle Konten", "Work (IMAP)", "Privat"}
	app.accountIDByLabel = map[string]string{"Alle Konten": "", "Work (IMAP)": "acc1", "Privat": "acc2"}
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Posteingang", Path: "INBOX", Type: "inbox"},
		{ID: "f2", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Spaces", Path: "Spaces"},
		{ID: "f2a", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "ACME", Path: "Spaces/ACME"},
		{ID: "f3", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Rechnungen", Path: "Rechnungen"},
		{ID: "f4", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Gesendet", Path: "Sent", Type: "sent"},
		{ID: "f5", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Archiv", Path: "Archives", Type: "archives"},
		{ID: "p1", AccountID: "acc2", AccountName: "Privat", Name: "Posteingang", Path: "INBOX", Type: "inbox"},
	}
	app.messages = []backend.MessageHeader{
		{
			ID: "m1", AccountID: "acc1", Folder: "INBOX",
			From: "Alice Meyer <alice@acme.example>", To: []string{"alex@example.org"},
			Subject: "Q4 Rollout & Budget-Freigabe bis Freitag",
			Date: today + "T09:15:00+01:00", Unread: true, Flagged: true, HasAttachments: true,
		},
		{
			ID: "m2", AccountID: "acc1", Folder: "Spaces/ACME",
			From: "Markus Klein <m.klein@partner.example>",
			Subject: "Cloud Migration: Terminvorschlag Workshop",
			Date: today + "T08:40:00+01:00", Unread: true,
		},
		{
			ID: "m3", AccountID: "acc1", Folder: "Rechnungen",
			From: "Cloud Billing <billing@cloud.example>",
			Subject: "Rechnung #2026-11-4821 f\u00e4llig",
			Date: today + "T07:20:00+01:00", Unread: true,
		},
		{
			ID: "m4", AccountID: "acc1", Folder: "INBOX",
			From: "Sarah M\u00fcller <sarah@example.org>",
			Subject: "Review Notes: PostDive Native UI",
			Date: now.AddDate(0, 0, -1).Format("2006-01-02") + "T16:45:00+01:00", Read: true,
		},
	}
	app.totalMessages = len(app.messages)
	app.focusUnread = 3
	app.snoozedCount = 1
	app.waitingReplyIDs = map[string]bool{"m2": true}
	app.waitingReplies = []backend.MessageHeader{app.messages[1]}
	app.selectedID = "m1"
	app.mailList.selected = 0
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Hallo Alex,\n\ndie finale Kalkulation für das Q4-Rollout liegt vor:\n- Lizenzen & Infrastruktur: 14.200 EUR\n- Migration & Workshop: 3.600 EUR\n\nKönnen wir heute um 14:00 Uhr kurz abstimmen?\n\nViele Grüße\nAlice",
		BodyHtml: `<p>Hallo Alex,</p><p>die finale Kalkulation f&uuml;r das Q4-Rollout liegt vor:</p><ul><li>Lizenzen &amp; Infrastruktur: <b>14.200 EUR</b></li><li>Migration &amp; Workshop: <b>3.600 EUR</b></li></ul><p>K&ouml;nnen wir heute um 14:00 Uhr kurz abstimmen?</p><p>Viele Gr&uuml;&szlig;e<br>Alice</p><img src="https://track.example.com/pixel.gif" width="1" height="1" alt="">`,
		Attachments: []backend.Attachment{
			{Name: "Q4-Kalkulation.pdf", Size: 248000, ContentType: "application/pdf"},
		},
	}
	app.calendars = []backend.Calendar{
		{ID: "c1", Name: "Arbeit", Type: "google", Color: "#3b82f6"},
		{ID: "c2", Name: "Privat", Type: "local", Color: "#10b981"},
		{ID: "c3", Name: "Feiertage", Type: "ical", Color: "#f59e0b"},
	}
	app.calLoadedStart = now.AddDate(0, 0, -7)
	app.calLoadedEnd = now.AddDate(0, 0, 14)
	app.events = []backend.CalendarEvent{
		{ID: "e0", CalendarID: "c1", Calendar: "Arbeit", Title: "Q4 Architecture Review", Start: now.Add(4 * time.Minute).Format(time.RFC3339), End: now.Add(34 * time.Minute).Format(time.RFC3339), Location: "https://zoom.us/j/123456"},
		{ID: "e1", CalendarID: "c1", Calendar: "Arbeit", Title: "Daily Engineering Sync", Start: today + "T10:00:00+01:00", End: today + "T10:30:00+01:00", Location: "Meet"},
		{ID: "e2", CalendarID: "c1", Calendar: "Arbeit", Title: "Q4 Rollout Abstimmung", Start: today + "T14:00:00+01:00", End: today + "T14:30:00+01:00", Location: "Zoom"},
		{ID: "e3", CalendarID: "c2", Calendar: "Privat", Title: "Zahnarzt Termin", Start: today + "T17:15:00+01:00", End: today + "T18:00:00+01:00", Location: "Innenstadt"},
		{ID: "e4", CalendarID: "c1", Calendar: "Arbeit", Title: "Partner Review", Start: tomorrow + "T11:00:00+01:00", End: tomorrow + "T12:00:00+01:00", Location: "Teams"},
	}
	wd := (int(now.Weekday()) + 6) % 7
	weekMon := now.AddDate(0, 0, -wd)
	for _, we := range []struct {
		id, calID, cal, title, loc, sh, eh string
		offset                             int
	}{
		{"vw1", "c1", "Arbeit", "Sprint Planning", "Meet", "09:30:00", "10:30:00", 0},
		{"vw2", "c1", "Arbeit", "Produkt-Review", "Zoom", "14:00:00", "15:00:00", 0},
		{"vw3", "c1", "Arbeit", "1:1 mit Sarah", "Teams", "10:00:00", "10:45:00", 1},
		{"vw4", "c2", "Privat", "Sport & Laufen", "Park", "18:00:00", "19:00:00", 1},
		{"vw5", "c1", "Arbeit", "Design Critique", "Figma", "11:00:00", "12:00:00", 2},
		{"vw6", "c1", "Arbeit", "Roadmap Q1", "Zoom", "13:30:00", "14:30:00", 3},
		{"vw7", "c2", "Privat", "Abendessen Team", "Bistro", "19:00:00", "20:15:00", 3},
		{"vw8", "c1", "Arbeit", "Release-Check", "Teams", "15:30:00", "16:30:00", 4},
	} {
		dayStr := weekMon.AddDate(0, 0, we.offset).Format("2006-01-02")
		if dayStr == today || dayStr == tomorrow {
			continue
		}
		app.events = append(app.events, backend.CalendarEvent{
			ID: we.id, CalendarID: we.calID, Calendar: we.cal, Title: we.title, Location: we.loc,
			Start: dayStr + "T" + we.sh + "+01:00", End: dayStr + "T" + we.eh + "+01:00",
		})
	}
	app.chatEntries = []ChatEntry{
		{ID: "u1", Role: "user", Content: "Was steht heute wichtig an?"},
		{
			ID:   "a1",
			Role: "assistant",
			Tools: []ToolTrace{
				{ID: "t1", Name: "search_messages", Summary: "4 Mails", Done: true},
				{ID: "t2", Name: "list_calendar_events", Summary: "5 Termine", Done: true},
			},
			Content: "- **Budget-Freigabe:** Alice wartet bis Freitag (17.800 EUR)\n- **N\u00e4chstes Meeting:** Q4 Architecture Review in 4 Min. (Zoom)",
		},
	}
	return app, updates
}

func findFFmpeg(t *testing.T, realHome string) string {
	t.Helper()
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	candidates := []string{
		filepath.Join(realHome, ".local", "bin", "ffmpeg"),
		"/opt/homebrew/bin/ffmpeg",
		"/usr/local/bin/ffmpeg",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	t.Fatal("ffmpeg not found in PATH or standard local/Homebrew locations")
	return ""
}

func decodeAppIcon(t *testing.T) image.Image {
	t.Helper()
	raw := RenderAppIconPNG(256)
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode app icon PNG: %v", err)
	}
	return img
}

const (
	vidW, vidH   = 1920, 1080
	vidFPS       = 30
	frameDur     = time.Second / vidFPS
	capH         = 150
	winScale     = 1.15
	winLW, winLH = 1400, 740
	barH         = 30
	winRadius    = 12
)

var (
	vidBG   = color.RGBA{15, 17, 23, 255}
	winRect = image.Rect((vidW-int(winLW*winScale))/2, capH+8,
		(vidW+int(winLW*winScale))/2, capH+8+barH+int(math.Round(winLH*winScale)))
	vidAccent = color.NRGBA{59, 130, 246, 255}
)

type videoSurface struct {
	tt    *ui.Tester
	x, y  int
	scale float64
	top   bool
}

func (s *videoSurface) rect() image.Rectangle {
	return s.tt.Image().Bounds().Add(image.Pt(s.x, s.y))
}

func (s *videoSurface) draw(dst, base *image.RGBA) {
	r := s.rect()
	draw.Draw(dst, r, s.tt.Image(), image.Point{}, draw.Src)
	vidRoundCorners(dst, base, r, winRadius, s.top, true)
}

type video struct {
	t        *testing.T
	ff       *exec.Cmd
	out      io.WriteCloser
	updates  chan func()
	frame    *image.RGBA
	base     *image.RGBA
	surfaces []*videoSurface
	active   *videoSurface
	win      *videoSurface
	frames   map[bool]*image.RGBA
	title    string
	sub      string
	isDark   bool
	px, py   float64
	showPtr  bool
	ripple   int
	rx, ry   float64
	prev     *image.RGBA
	fade     int
	sprite   *image.RGBA
	nframes  int
}

func newVideo(t *testing.T, ffPath, outPath string, updates chan func()) *video {
	t.Helper()
	cmd := exec.Command(ffPath, "-y", "-loglevel", "error",
		"-f", "rawvideo", "-pix_fmt", "rgba", "-s", "1920x1080", "-r", "30", "-i", "-",
		"-c:v", "libx264", "-preset", "slow", "-crf", "20", "-tune", "stillimage",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart", outPath)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &video{
		t:       t,
		ff:      cmd,
		out:     in,
		updates: updates,
		frame:   image.NewRGBA(image.Rect(0, 0, vidW, vidH)),
		frames:  map[bool]*image.RGBA{},
		sprite:  vidPointerSprite(),
	}
}

func (v *video) drainUpdates() {
	for {
		select {
		case fn := <-v.updates:
			if fn != nil {
				fn()
			}
		default:
			return
		}
	}
}

func (v *video) window(view func(*ui.Context)) *videoSurface {
	tt := ui.NewTester(view, winLW, winLH)
	tt.SetScale(winScale)
	tt.Frame()
	v.win = &videoSurface{tt: tt, x: winRect.Min.X, y: winRect.Min.Y + barH, scale: winScale}
	return v.win
}

func (v *video) cut() {
	if v.nframes > 0 {
		v.prev = vidCloneRGBA(v.frame)
		v.fade = 12
	}
	v.ripple = 0
}

func (v *video) scene(s *videoSurface, title, sub string) {
	v.cut()
	v.title, v.sub = title, sub
	v.base = v.windowBase()
	v.surfaces, v.active, v.showPtr = []*videoSurface{s}, s, true
}

func (v *video) card(img *image.RGBA) {
	v.cut()
	v.base, v.surfaces, v.active, v.showPtr = img, nil, nil, false
}

func (v *video) switchTheme(a *App, presetLabel string, dark bool) {
	v.isDark = dark
	a.applyThemeChoice(presetLabel)
	v.active.tt.Frame()
	v.base = v.windowBase()
	v.step(4)
}

func (v *video) windowBase() *image.RGBA {
	f := v.frames[v.isDark]
	if f == nil {
		f = image.NewRGBA(image.Rect(0, 0, vidW, vidH))
		draw.Draw(f, f.Bounds(), image.NewUniform(vidBG), image.Point{}, draw.Src)
		vidShadow(f, winRect, winRadius)
		under := vidCloneRGBA(f)
		bar := color.NRGBA{236, 236, 238, 255}
		if v.isDark {
			bar = color.NRGBA{39, 39, 42, 255}
		}
		vidFillRRect(f, winRect, winRadius, bar)
		top := image.Rect(winRect.Min.X, winRect.Min.Y, winRect.Max.X, winRect.Min.Y+barH)
		draw.Draw(f, top, vidRender(vidTitleBar(bar, v.isDark), top.Dx(), barH, 1), image.Point{}, draw.Src)
		vidRoundCorners(f, under, winRect, winRadius, true, false)
		for i, c := range []color.NRGBA{{255, 95, 87, 255}, {254, 188, 46, 255}, {40, 200, 64, 255}} {
			vidFillCircle(f, float64(winRect.Min.X+22+i*20), float64(winRect.Min.Y+barH/2), 6, c)
		}
		v.frames[v.isDark] = f
	}
	b := vidCloneRGBA(f)
	draw.Draw(b, image.Rect(0, 0, vidW, capH), vidRender(vidCaptionView(v.title, v.sub), vidW, capH, 1), image.Point{}, draw.Src)
	return b
}

func (v *video) pointer(x, y float64) { v.px, v.py, v.showPtr = x, y, true }

func (v *video) toVideo(x, y float32) (float64, float64) {
	s := v.active
	return float64(s.x) + float64(x)*s.scale, float64(s.y) + float64(y)*s.scale
}

func (v *video) toSurface(x, y float64) (float32, float32) {
	s := v.active
	return float32((x - float64(s.x)) / s.scale), float32((y - float64(s.y)) / s.scale)
}

func (v *video) find(text string) (float32, float32) {
	r, ok := v.active.tt.Find(text)
	if !ok {
		v.t.Fatalf("video: no %q among %v", text, v.active.tt.Texts())
	}
	return r.X + r.W/2, r.Y + r.H/2
}

func (v *video) moveTo(x, y float32, secs float64) {
	tx, ty := v.toVideo(x, y)
	fx, fy := v.px, v.py
	n := max(1, int(secs*vidFPS))
	for i := 1; i <= n; i++ {
		f := float64(i) / float64(n)
		f = f * f * (3 - 2*f)
		v.px, v.py = fx+(tx-fx)*f, fy+(ty-fy)*f
		if v.active != nil {
			v.active.tt.Move(v.toSurface(v.px, v.py))
		}
		v.step(1)
	}
}

func (v *video) hover(text string, secs float64) {
	x, y := v.find(text)
	v.moveTo(x, y, 0.55)
	if secs > 0.55 {
		v.hold(secs - 0.55)
	}
}

func (v *video) click(text string) {
	x, y := v.find(text)
	v.moveTo(x, y, 0.5)
	v.step(3)
	v.active.tt.ClickAt(x, y)
	v.rx, v.ry, v.ripple = v.px, v.py, 14
	v.step(8)
}

func (v *video) clickVisual(text string, afterFn func()) {
	x, y := v.find(text)
	v.moveTo(x, y, 0.5)
	v.step(3)
	if afterFn != nil {
		afterFn()
		v.active.tt.Frame()
	}
	v.rx, v.ry, v.ripple = v.px, v.py, 14
	v.step(8)
}

func (v *video) key(mods ui.Modifiers, k ui.Key) {
	v.active.tt.Key(mods, k)
	v.step(5)
}

func (v *video) step(n int) {
	for range n {
		v.drainUpdates()
		for _, s := range v.surfaces {
			s.tt.Advance(frameDur)
		}
		v.compose()
		v.write()
	}
}

func (v *video) hold(secs float64) {
	n := int(math.Round(secs * vidFPS))
	moving := min(n, max(9, v.fade+1, v.ripple+1))
	v.step(moving)
	for range n - moving {
		v.write()
	}
}

func (v *video) compose() {
	copy(v.frame.Pix, v.base.Pix)
	for _, s := range v.surfaces {
		s.draw(v.frame, v.base)
	}
	if v.ripple > 0 {
		f := float64(v.ripple) / 14
		vidRing(v.frame, v.rx, v.ry, 10+22*(1-f), 3, color.NRGBA{vidAccent.R, vidAccent.G, vidAccent.B, uint8(165 * f)})
		v.ripple--
	}
	if v.showPtr {
		b := v.sprite.Bounds()
		at := image.Pt(int(v.px)-3, int(v.py)-3)
		draw.Draw(v.frame, b.Add(at), v.sprite, image.Point{}, draw.Over)
	}
	if v.fade > 0 {
		vidBlend(v.frame, v.prev, float64(v.fade)/13)
		v.fade--
	}
}

func (v *video) write() {
	if _, err := v.out.Write(v.frame.Pix); err != nil {
		v.t.Fatalf("ffmpeg write: %v", err)
	}
	v.nframes++
}

func (v *video) snap(path string) {
	v.compose()
	f, err := os.Create(path)
	if err != nil {
		v.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, v.frame); err != nil {
		v.t.Fatal(err)
	}
}

func (v *video) finish() {
	_ = v.out.Close()
	if err := v.ff.Wait(); err != nil {
		v.t.Fatalf("ffmpeg exit: %v", err)
	}
}

// --- Video Graphic Cards -------------------------------------------------------------------------

var vidWhite = ui.RGB(255, 255, 255)

func vidCaptionView(title, sub string) func(*ui.Context) {
	return func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.RGB(vidBG.R, vidBG.G, vidBG.B)).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(38).Bold().TextColor(vidWhite)
			ui.Text(c, sub).FontSize(21).TextColor(ui.RGBA(255, 255, 255, 0.75))
		})
	}
}

func vidTitleBar(bar color.NRGBA, dark bool) func(*ui.Context) {
	return func(c *ui.Context) {
		fg := ui.RGBA(0, 0, 0, 0.62)
		if dark {
			fg = ui.RGBA(255, 255, 255, 0.72)
		}
		ui.Row(c).Fill().Background(ui.RGB(bar.R, bar.G, bar.B)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "PostDive").FontSize(13).FontWeight(600).TextColor(fg)
		})
	}
}

func overviewCard(icon image.Image) *image.RGBA {
	bmp := ui.NewBitmap(icon)
	icMail := ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="16" x="2" y="4" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg>`))
	icCal := ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="18" x="3" y="4" rx="2"/><path d="M16 2v4"/><path d="M8 2v4"/><path d="M3 10h18"/></svg>`))
	icShield := ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/></svg>`))
	icSparkles := ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/></svg>`))

	cardBg := ui.RGBA(255, 255, 255, 0.055)
	cardBorder := ui.RGBA(255, 255, 255, 0.14)
	muted := ui.RGBA(255, 255, 255, 0.72)
	blue := ui.RGB(96, 165, 250)
	green := ui.RGB(74, 222, 128)
	amber := ui.RGB(251, 191, 36)
	purple := ui.RGB(192, 132, 252)

	badge := func(c *ui.Context, text string, col ui.Color) {
		ui.Text(c, text).FontSize(14).FontWeight(600).Padding(5, 13).Radius(14).
			Background(col.Alpha(0.18)).Border(1, col.Alpha(0.45)).TextColor(col)
	}
	bullet := func(c *ui.Context, dotCol ui.Color, title, desc string) {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Size(9, 9).Radius(5).Background(dotCol).Margin(7, 0, 0, 0)
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, title).FontSize(18).Bold().TextColor(vidWhite)
				ui.Text(c, desc).FontSize(15).TextColor(muted)
			})
		})
	}

	return vidRender(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.RGB(vidBG.R, vidBG.G, vidBG.B)).Padding(52, 72).Gap(32).Justify(ui.Center).Children(func() {
			ui.Row(c).Gap(26).AlignItems(ui.Center).Children(func() {
				ui.Image(c, bmp).Size(108, 108)
				ui.Column(c).Gap(8).Grow(1).Children(func() {
					ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "PostDive 0.8.0").FontSize(50).Bold().TextColor(vidWhite)
						badge(c, "macOS · Linux · Windows · Android", blue)
					})
					ui.Text(c, "Nativer E-Mail-, Kalender- & Kontakt-Client — mit ruhiger Oberfläche, Cron-Kalender & flexibler KI").
						FontSize(22).TextColor(ui.RGBA(255, 255, 255, 0.85))
				})
			})

			ui.Row(c).Gap(16).AlignItems(ui.Center).Justify(ui.Center).Padding(16, 26).Radius(16).
				Background(ui.RGBA(59, 130, 246, 0.10)).Border(1, ui.RGBA(96, 165, 250, 0.30)).Children(func() {
				badge(c, "IMAP / SMTP · CalDAV & Google", blue)
				ui.Text(c, "──▶").FontSize(22).Bold().TextColor(blue)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(7, 18).Radius(12).
					Background(ui.RGBA(255, 255, 255, 0.12)).Border(1, ui.RGBA(255, 255, 255, 0.28)).Children(func() {
					ui.Icon(c, icSparkles).FontSize(20).TextColor(vidWhite)
					ui.Text(c, "Diver UI · KI-Panel rechts · Schnellantwort · Cron-Kalender").FontSize(17).Bold().TextColor(vidWhite)
				})
				ui.Text(c, "──▶").FontSize(22).Bold().TextColor(green)
				badge(c, "Lokal (MTPLX / Ollama) · Cloud · 1-Klick-MCP", green)
			})

			ui.Row(c).Gap(24).Grow(1).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).Basis(0).Padding(26, 28).Radius(18).Background(cardBg).Border(1, cardBorder).Gap(16).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icMail).FontSize(26).TextColor(blue)
						ui.Text(c, "Ruhiges Mail-UI & KI rechts").FontSize(23).Bold().TextColor(vidWhite)
					})
					ui.Divider(c)
					bullet(c, blue, "Diver & Diver Light Themes", "24 eingebaute Themes inkl. Omarchy-Live-Sync — ruhig & kontraststark")
					bullet(c, purple, "Fokus, Filter-Pills & Schnellantwort", "Absender-Avatare, Ungelesen/★/Anhang-Filter, Undo-Send & KI-Chips")
					bullet(c, amber, "Konsistentes KI-Dock rechts", "Im Hauptfenster und im Antworten-Dialog immer an derselben Stelle")
					bullet(c, green, "HTML-WYSIWYG & Unterhaltungen", "Inline-Bilder, Tabellen, Drag & Drop-Anhänge & gefaltete Threads")
				})

				ui.Column(c).Grow(1).Basis(0).Padding(26, 28).Radius(18).Background(cardBg).Border(1, cardBorder).Gap(16).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icCal).FontSize(26).TextColor(amber)
						ui.Text(c, "Cron-Kalender & Triage").FontSize(23).Bold().TextColor(vidWhite)
					})
					ui.Divider(c)
					bullet(c, blue, "24h-Tag/Woche & Mini-Monat-Seitenleiste", "Einklappbare Kalender-Liste nach Konten, Frei/Belegt-Check bei .ics")
					bullet(c, green, "Live-Meeting-Pill in der Toolbar", "Zeigt laufende/bevorstehende Calls mit 1-Klick-Beitritt (Teams/Meet/Zoom)")
					bullet(c, purple, "Snooze, Warten & Abo-Aufräumer", "Wiedervorlage ohne Token-Verbrauch inkl. 1-Klick-Newsletter-Abmeldung")
					bullet(c, amber, "Google, CalDAV, iCal & Teams", "Bidirektionale Synchronisation & direkte Einladungen mit Antworten")
				})

				ui.Column(c).Grow(1).Basis(0).Padding(26, 28).Radius(18).Background(cardBg).Border(1, cardBorder).Gap(16).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icShield).FontSize(26).TextColor(green)
						ui.Text(c, "Datenschutz, Crypto & MCP").FontSize(23).Bold().TextColor(vidWhite)
					})
					ui.Divider(c)
					bullet(c, green, "S/MIME & natives OpenPGP", "Verschlüsselte & entschlüsselte Mails werden niemals an KI-Anbieter gesendet")
					bullet(c, amber, "Tracker-Zähler & Bild-Blocker", "Externe Tracking-Pixel gezählt & blockiert, SSRF-geschütztes Unsubscribe")
					bullet(c, blue, "1-Klick-MCP & Android-Parität", "Claude Desktop, Cursor & Antigravity per Klick verbinden — auch mobil")
					bullet(c, purple, "0-Token-Filterregeln & LAN-QR", "Schnellregeln aus Headern & verschlüsselter Konto-Umzug per QR")
				})
			})
		})
	}, vidW, vidH, 1)
}

func titleCard(icon image.Image, title, sub, foot string) *image.RGBA {
	bmp := ui.NewBitmap(icon)
	return vidRender(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.RGB(vidBG.R, vidBG.G, vidBG.B)).Justify(ui.Center).AlignItems(ui.Center).Gap(22).Children(func() {
			ui.Image(c, bmp).Size(164, 164)
			ui.Text(c, title).FontSize(64).Bold().TextColor(vidWhite)
			if strings.HasPrefix(sub, "brew ") {
				ui.Text(c, sub).FontSize(30).FontWeight(500).Padding(14, 26).Radius(14).
					Background(ui.RGBA(255, 255, 255, 0.1)).TextColor(vidWhite)
			} else {
				ui.Text(c, sub).FontSize(28).TextColor(ui.RGBA(255, 255, 255, 0.8))
			}
			if foot != "" {
				ui.Text(c, foot).FontSize(20).TextColor(ui.RGBA(255, 255, 255, 0.6))
			}
		})
	}, vidW, vidH, 1)
}

func vidRender(view func(*ui.Context), w, h int, scale float32) *image.RGBA {
	tt := ui.NewTester(view, w, h)
	tt.SetScale(scale)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	return vidCloneRGBA(tt.Image())
}

func vidCloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

func vidOver(pix []uint8, i int, c color.NRGBA, cov float64) {
	a := float64(c.A) / 255 * cov
	if a <= 0 {
		return
	}
	pix[i] = uint8(float64(c.R)*a + float64(pix[i])*(1-a))
	pix[i+1] = uint8(float64(c.G)*a + float64(pix[i+1])*(1-a))
	pix[i+2] = uint8(float64(c.B)*a + float64(pix[i+2])*(1-a))
	pix[i+3] = uint8(255*a + float64(pix[i+3])*(1-a))
}

func vidRRectCov(x, y int, r image.Rectangle, rad float64) float64 {
	px, py := float64(x)+0.5, float64(y)+0.5
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	qx := math.Abs(px-cx) - (float64(r.Dx())/2 - rad)
	qy := math.Abs(py-cy) - (float64(r.Dy())/2 - rad)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - rad
	return math.Max(0, math.Min(1, 0.5-d))
}

func vidFillRRect(dst *image.RGBA, r image.Rectangle, rad float64, c color.NRGBA) {
	r = r.Intersect(dst.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if cov := vidRRectCov(x, y, r, rad); cov > 0 {
				vidOver(dst.Pix, dst.PixOffset(x, y), c, cov)
			}
		}
	}
}

func vidRoundCorners(dst, base *image.RGBA, r image.Rectangle, rad float64, top, bottom bool) {
	n := int(math.Ceil(rad)) + 1
	var corners []image.Rectangle
	if top {
		corners = append(corners, image.Rect(r.Min.X, r.Min.Y, r.Min.X+n, r.Min.Y+n), image.Rect(r.Max.X-n, r.Min.Y, r.Max.X, r.Min.Y+n))
	}
	if bottom {
		corners = append(corners, image.Rect(r.Min.X, r.Max.Y-n, r.Min.X+n, r.Max.Y), image.Rect(r.Max.X-n, r.Max.Y-n, r.Max.X, r.Max.Y))
	}
	for _, q := range corners {
		q = q.Intersect(dst.Bounds())
		for y := q.Min.Y; y < q.Max.Y; y++ {
			for x := q.Min.X; x < q.Max.X; x++ {
				cov := vidRRectCov(x, y, r, rad)
				i := dst.PixOffset(x, y)
				for k := range 4 {
					dst.Pix[i+k] = uint8(float64(dst.Pix[i+k])*cov + float64(base.Pix[i+k])*(1-cov))
				}
			}
		}
	}
}

func vidShadow(dst *image.RGBA, r image.Rectangle, rad float64) {
	for i := 28; i >= 1; i-- {
		vidFillRRect(dst, r.Inset(-i).Add(image.Pt(0, 10)), rad+float64(i), color.NRGBA{0, 0, 0, 5})
	}
}

func vidFillCircle(dst *image.RGBA, cx, cy, rad float64, c color.NRGBA) {
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if cov := math.Max(0, math.Min(1, rad-d+0.5)); cov > 0 {
				vidOver(dst.Pix, dst.PixOffset(x, y), c, cov)
			}
		}
	}
}

func vidRing(dst *image.RGBA, cx, cy, rad, width float64, c color.NRGBA) {
	for y := int(cy - rad - width); y <= int(cy+rad+width); y++ {
		for x := int(cx - rad - width); x <= int(cx+rad+width); x++ {
			if !(image.Point{x, y}).In(dst.Bounds()) {
				continue
			}
			d := math.Abs(math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - rad)
			if cov := math.Max(0, math.Min(1, width/2-d+0.5)); cov > 0 {
				vidOver(dst.Pix, dst.PixOffset(x, y), c, cov)
			}
		}
	}
}

func vidBlend(dst, src *image.RGBA, f float64) {
	for i := range dst.Pix {
		dst.Pix[i] = uint8(float64(src.Pix[i])*f + float64(dst.Pix[i])*(1-f))
	}
}

func vidPointerSprite() *image.RGBA {
	const s, rim, ss = 1.6, 1.5, 4
	pts := [][2]float64{{0, 0}, {0, 17}, {4.2, 13.2}, {7, 19.6}, {9.8, 18.4}, {7.1, 12.2}, {12.6, 12.2}}
	for i := range pts {
		pts[i][0], pts[i][1] = pts[i][0]*s+3, pts[i][1]*s+3
	}
	img := image.NewRGBA(image.Rect(0, 0, 30, 40))
	for y := range 40 {
		for x := range 30 {
			var black, whiteN int
			for sy := range ss {
				for sx := range ss {
					px, py := float64(x)+(float64(sx)+0.5)/ss, float64(y)+(float64(sy)+0.5)/ss
					switch {
					case vidInPolygon(pts, px, py):
						black++
					case vidEdgeDist(pts, px, py) <= rim:
						whiteN++
					}
				}
			}
			i := img.PixOffset(x, y)
			w := uint8(255 * whiteN / (ss * ss))
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = w, w, w
			img.Pix[i+3] = uint8(255 * (black + whiteN) / (ss * ss))
		}
	}
	return img
}

func vidInPolygon(pts [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

func vidEdgeDist(pts [][2]float64, x, y float64) float64 {
	best := math.Inf(1)
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[j], pts[i]
		dx, dy := b[0]-a[0], b[1]-a[1]
		t := math.Max(0, math.Min(1, ((x-a[0])*dx+(y-a[1])*dy)/(dx*dx+dy*dy)))
		best = math.Min(best, math.Hypot(x-a[0]-t*dx, y-a[1]-t*dy))
	}
	return best
}
