package nativeui

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
)

func TestHTMLImagesEmbeddedAlwaysAndRemoteOptIn(t *testing.T) {
	src := `<p>Hallo</p>` +
		`<img src="cid:logo@acme" alt="Firmenlogo">` +
		`<img src="data:image/png;base64,iVBORw0KGgo=" alt="Inline">` +
		`<img src="https://cdn.example.org/banner.png" alt="Banner">` +
		`<img src="https://track.example.org/p.gif" width="1" height="1">`

	// Default (showRemote = false): embedded cid/data shown, remote blocked.
	md, blocked, remote := htmlMailMarkdownWithImages(src, false)
	if blocked != 1 || remote != 1 {
		t.Fatalf("blocked=%d remote=%d, want 1 and 1", blocked, remote)
	}
	if !strings.Contains(md, "![Firmenlogo](cid:logo@acme)") {
		t.Fatalf("embedded cid image missing in markdown:\n%s", md)
	}
	if !strings.Contains(md, "![Inline](data:image/png;base64,iVBORw0KGgo=)") {
		t.Fatalf("embedded data:image missing in markdown:\n%s", md)
	}
	if strings.Contains(md, "banner.png") {
		t.Fatalf("remote image must stay blocked by default:\n%s", md)
	}

	// Opt-in (showRemote = true): remote image appears, tracking pixel stays dropped.
	md2, blocked2, remote2 := htmlMailMarkdownWithImages(src, true)
	if blocked2 != 0 || remote2 != 1 {
		t.Fatalf("with showRemote=true: blocked=%d remote=%d, want 0 and 1", blocked2, remote2)
	}
	if !strings.Contains(md2, "![Banner](https://cdn.example.org/banner.png)") {
		t.Fatalf("remote image missing when showRemote=true:\n%s", md2)
	}
	if strings.Contains(md2, "track.example.org") {
		t.Fatalf("1x1 tracking pixel must still be dropped:\n%s", md2)
	}
}

func TestParseMailtoURLAndOpenComposeFromMailto(t *testing.T) {
	f, ok := ParseMailtoURL("mailto:alice@example.de?cc=bob@example.de&bcc=hid@example.de&subject=Gr%C3%BC%C3%9Fe%20aus%20Berlin&body=Hallo%20Welt%0AZeile%202")
	if !ok {
		t.Fatal("expected ParseMailtoURL to succeed")
	}
	if f.To != "alice@example.de" || f.CC != "bob@example.de" || f.BCC != "hid@example.de" {
		t.Fatalf("unexpected recipients: %+v", f)
	}
	if f.Subject != "Grüße aus Berlin" || !strings.Contains(f.Body, "Hallo Welt\nZeile 2") {
		t.Fatalf("unexpected subject/body: %+v", f)
	}
	if _, ok := ParseMailtoURL("https://example.de"); ok {
		t.Fatal("non-mailto URL must return false")
	}

	app := NewApp(Options{})
	app.OpenComposeFromMailto("mailto:info@example.de?subject=Test")
	if !app.composeOpen || app.composeTo != "info@example.de" || app.composeSubject != "Test" {
		t.Fatalf("OpenComposeFromMailto did not open compose properly: open=%v to=%q subj=%q", app.composeOpen, app.composeTo, app.composeSubject)
	}
}

func TestOpenComposeFromDraftAndForwardPreserveState(t *testing.T) {
	app := NewApp(Options{})
	msg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{
			ID:        "acc1::Drafts::7",
			AccountID: "acc1",
			Subject:   "Vertrag",
			From:      "me@example.de",
			To:        []string{"bob@example.de"},
			CC:        []string{"cc@example.de"},
			Date:      "2026-11-20T09:00:00Z",
		},
		BCC:      []string{"secret@example.de"},
		BodyText: "Hallo Bob",
		BodyHtml: "<p>Hallo <b>Bob</b></p><script>alert(1)</script>",
		Attachments: []backend.Attachment{
			{Name: "vertrag.pdf", ContentType: "application/pdf", PartName: "2", Size: 2048},
		},
	}

	app.OpenComposeFromDraft(msg)
	if app.composeDraftID != msg.ID || app.composeBCC != "secret@example.de" {
		t.Fatalf("draft reopen lost draftID or BCC: id=%q bcc=%q", app.composeDraftID, app.composeBCC)
	}
	if len(app.composeSourceAttachments) != 1 || app.composeSourceAttachments[0].Name != "vertrag.pdf" {
		t.Fatalf("draft reopen lost attachments: %+v", app.composeSourceAttachments)
	}

	app.OpenComposeForward(msg)
	if len(app.composeSourceAttachments) != 1 || app.composeSourceAttachments[0].Name != "vertrag.pdf" {
		t.Fatalf("forward lost attachments: %+v", app.composeSourceAttachments)
	}
	if !app.composeKeepHTML || app.composeForwardHTML == "" {
		t.Fatalf("forward should enable HTML preservation for HTML emails")
	}

	// Sanitize & build forward/print HTML
	inline := map[string]mailengine.InlineImage{
		"pic1": {CID: "pic1", ContentType: "image/png", Data: []byte("PNG")},
	}
	sanitized := SanitizeEmailHTML(`<p onclick="x()">Ok</p><script>evil()</script><a href="javascript:alert(1)">bad</a><img src="cid:pic1"><img src="https://evil.test/x.png">`, inline, true)
	if strings.Contains(sanitized, "script") || strings.Contains(sanitized, "onclick") || strings.Contains(sanitized, "javascript:") || strings.Contains(sanitized, "evil.test") {
		t.Fatalf("SanitizeEmailHTML leaked unsafe content: %s", sanitized)
	}
	wantData := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNG"))
	if !strings.Contains(sanitized, wantData) {
		t.Fatalf("SanitizeEmailHTML did not inline cid image: %s", sanitized)
	}

	printDoc := BuildPrintHTML(msg, "de", inline, false, false)
	if !strings.Contains(printDoc, "Vertrag") || !strings.Contains(printDoc, "vertrag.pdf") || strings.Contains(printDoc, "alert(1)") {
		t.Fatalf("BuildPrintHTML unexpected output: %s", printDoc)
	}
}

func TestReplyAllMatchesAndExcludesAliases(t *testing.T) {
	app := NewApp(Options{})
	app.cfg.Direct.Accounts = []config.DirectAccount{{
		ID:          "acc1",
		Name:        "Work",
		Email:       "main@example.de",
		DisplayName: "Max Muster",
		Aliases: []config.AccountAlias{
			{Email: "team@example.de", DisplayName: "Max Team"},
		},
		Enabled: true,
	}}
	app.accounts = []backend.Account{{
		ID:   "acc1",
		Name: "Work",
		Identities: []backend.AccountIdentity{
			{ID: "acc1-id", Email: "main@example.de", Name: "Max Muster"},
			{ID: "acc1-alias-0", Email: "team@example.de", Name: "Max Team"},
		},
	}}

	msg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{
			ID:        "m1",
			AccountID: "acc1",
			From:      "Partner <partner@other.de>",
			To:        []string{"team@example.de", "other@other.de"},
			CC:        []string{"main@example.de"},
			Subject:   "Projekt",
		},
		BodyText: "Hi",
	}
	app.OpenComposeReplyAll(msg, "")
	if app.composeFromEmail != "team@example.de" || app.composeIdentityID != "acc1-alias-0" {
		t.Fatalf("expected reply to auto-select matched alias team@example.de, got email=%q id=%q", app.composeFromEmail, app.composeIdentityID)
	}
	if strings.Contains(app.composeCC, "main@example.de") || strings.Contains(app.composeCC, "team@example.de") {
		t.Fatalf("reply-all CC must exclude primary email and aliases, got %q", app.composeCC)
	}
	if !strings.Contains(app.composeCC, "other@other.de") {
		t.Fatalf("reply-all CC must keep external recipient, got %q", app.composeCC)
	}
}

func TestFolderUnreadBadgeRender(t *testing.T) {
	app := NewApp(Options{})
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", Name: "INBOX", Path: "/INBOX", Type: "inbox", UnreadCount: 7},
		{ID: "f2", AccountID: "acc1", Name: "Archiv", Path: "/Archiv", Type: "archive", UnreadCount: 0},
	}
	tt := ui.NewTester(app.RenderMain, 1200, 800)
	tt.Frame()
	if _, ok := tt.Find("7"); !ok {
		t.Fatal("expected folder unread count badge '7' in sidebar")
	}
}

func TestReviewSecuritySanitizerSSRFAndDailyUXFixes(t *testing.T) {
	// 1. Strict allowlist HTML sanitizer blocks SVG, MathML, style, video, srcset, CSS url(), and SVG data URIs.
	evilHTML := `<p style="color:red;background:url(https://leak.test/1);position:fixed">Safe</p>` +
		`<svg onload="alert(1)"><script>x()</script></svg>` +
		`<math><mtext><img src=x onerror=alert(1)></mtext></math>` +
		`<style>body{background:url(https://leak.test/css)}</style>` +
		`<video poster="https://leak.test/p.jpg"><source src="https://leak.test/v.mp4"></video>` +
		`<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">` +
		`<img src="data:image/png;base64,iVBORw0KGgo=" srcset="https://leak.test/2x.png 2x">`
	clean := SanitizeEmailHTML(evilHTML, nil, true)
	for _, forbidden := range []string{"<svg", "<math", "<style", "<video", "leak.test", "position", "srcset", "svg+xml", "alert(1)"} {
		if strings.Contains(strings.ToLower(clean), forbidden) {
			t.Fatalf("SanitizeEmailHTML leaked %q in: %s", forbidden, clean)
		}
	}
	if !strings.Contains(clean, "Safe") || !strings.Contains(clean, "color: red") {
		t.Fatalf("SanitizeEmailHTML dropped safe text/style: %s", clean)
	}

	// 2. BuildPrintHTML includes strict CSP and ensureAutoPrintScript switches script-src to exact hash.
	printDoc := BuildPrintHTML(&backend.MessageDetail{
		MessageHeader: backend.MessageHeader{Subject: "Rechnung", From: "shop@example.de"},
		BodyText:      "Hallo",
	}, "de", nil, false, false)
	if !strings.Contains(printDoc, `http-equiv="Content-Security-Policy"`) || !strings.Contains(printDoc, "script-src 'none'") {
		t.Fatalf("BuildPrintHTML missing strict CSP: %s", printDoc)
	}
	fallbackDoc := ensureAutoPrintScript(printDoc)
	if !strings.Contains(fallbackDoc, autoPrintScriptBlock) || !strings.Contains(fallbackDoc, autoPrintScriptCSPHash) {
		t.Fatalf("ensureAutoPrintScript did not inject hashed script cleanly")
	}

	// 3. Image pixel-bomb & SVG rejection before DecodeImage.
	if err := validateImageBytes([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)); err == nil {
		t.Fatal("validateImageBytes must reject SVG bytes")
	}
	// Minimal PNG header with valid IHDR CRC advertising 10000x10000 pixels (100M px > 16M limit).
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(10000))
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(10000))
	ihdr.Write([]byte{8, 2, 0, 0, 0})
	var bomb bytes.Buffer
	bomb.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	_ = binary.Write(&bomb, binary.BigEndian, uint32(13))
	bomb.Write(ihdr.Bytes())
	_ = binary.Write(&bomb, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	if err := validateImageBytes(bomb.Bytes()); err == nil || !strings.Contains(err.Error(), "exceed safety limit") {
		t.Fatalf("validateImageBytes must reject oversized pixel-bomb PNG with dimension error, got: %v", err)
	}
	// A small valid 2x2 PNG must pass.
	var smallPNG bytes.Buffer
	if err := png.Encode(&smallPNG, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := validateImageBytes(smallPNG.Bytes()); err != nil {
		t.Fatalf("valid small PNG should pass validateImageBytes, got: %v", err)
	}

	// 4. SSRF IP check blocks CGNAT, 0.0.0.0/8, NAT64, allows public.
	for _, blocked := range []string{"100.64.1.1", "0.0.0.1", "198.18.0.1", "64:ff9b::1", "127.0.0.1"} {
		if !isPrivateOrLocalIP(net.ParseIP(blocked)) {
			t.Fatalf("expected %s to be blocked", blocked)
		}
	}
	if isPrivateOrLocalIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public IP 1.1.1.1 should be allowed")
	}

	// 5. ParseMailtoURL strips CR/LF injection and collects repeated params deterministically.
	mf, ok := ParseMailtoURL("mailto:a@example.de?to=b@example.de&cc=c1@example.de&cc=c2@example.de&subject=Hi%0D%0ABcc:evil@x.test")
	if !ok {
		t.Fatal("expected ParseMailtoURL ok")
	}
	if mf.To != "a@example.de, b@example.de" || mf.CC != "c1@example.de, c2@example.de" {
		t.Fatalf("unexpected deterministic recipients: %+v", mf)
	}
	if strings.ContainsAny(mf.Subject, "\r\n") {
		t.Fatalf("subject must not contain CR/LF: %q", mf.Subject)
	}

	// 7. isDraftsMessage does not misclassify "Vertragsentwürfe"; isCurrentViewOutbound matches Sent/Drafts.
	app := NewApp(Options{})
	app.folders = []backend.Folder{
		{ID: "f-contract", Name: "Vertragsentwürfe", Path: "Vertragsentwürfe", Type: ""},
		{ID: "f-drafts", Name: "Entwürfe", Path: "Drafts", Type: "drafts"},
		{ID: "f-sent", Name: "Gesendet", Path: "Sent", Type: "sent"},
	}
	if app.isDraftsMessage(&backend.MessageDetail{MessageHeader: backend.MessageHeader{FolderID: "f-contract", Folder: "Vertragsentwürfe"}}) {
		t.Fatal("Vertragsentwürfe must not be misclassified as Drafts")
	}
	if !app.isDraftsMessage(&backend.MessageDetail{MessageHeader: backend.MessageHeader{FolderID: "f-drafts", Folder: "Drafts"}}) {
		t.Fatal("Drafts folder must be recognized as Drafts")
	}
	app.navID = "folder:f-sent"
	if !app.isCurrentViewOutbound() {
		t.Fatal("Sent folder must be recognized as outbound view")
	}
	app.navID = "folder:f-contract"
	if app.isCurrentViewOutbound() {
		t.Fatal("regular folder must not be recognized as outbound view")
	}

	// 8. CLI backends show clickable model hint chips instead of a model-listing warning.
	app.showCopilot = true
	app.agentBackend = "claude-cli"
	app.startModelLoad(t.Context())
	if app.modelsError != "" {
		t.Fatalf("claude-cli must not set modelsError, got %q", app.modelsError)
	}
	tt := ui.NewTester(app.RenderMain, 1400, 900)
	tt.Frame()
	if _, ok := tt.Find("opus"); !ok {
		t.Fatal("expected clickable 'opus' CLI model hint chip in AI Assistant panel")
	}
	if _, ok := tt.Find("sonnet"); !ok {
		t.Fatal("expected clickable 'sonnet' CLI model hint chip in AI Assistant panel")
	}
}

func TestCalendarSearchAndMultiSelectFilter(t *testing.T) {
	app := NewApp(Options{})
	app.engineReady = true
	app.navID = "calendar"
	app.calViewMode = 3 // Agenda
	app.calendars = []backend.Calendar{
		{ID: "cal-work", Name: "Work", Color: "#7aa2f7"},
		{ID: "cal-family", Name: "Family", Color: "#f7768e"},
		{ID: "cal-sport", Name: "Sport", Color: "#9ece6a"},
	}
	today := time.Now().Format("2006-01-02")
	app.events = []backend.CalendarEvent{
		{ID: "e1", CalendarID: "cal-work", Calendar: "Work", Title: "MCP Server Security", Start: today + "T10:30:00+01:00", End: today + "T11:00:00+01:00", Description: "Hallo<br><br>Teams"},
		{ID: "e2", CalendarID: "cal-family", Calendar: "Family", Title: "Hockey Maja", Start: today + "T16:00:00+01:00", End: today + "T17:00:00+01:00"},
		{ID: "e3", CalendarID: "cal-sport", Calendar: "Sport", Title: "Faustball Spiel", Location: "Stammheim", Start: today + "T18:00:00+01:00", End: today + "T19:00:00+01:00"},
	}

	// All calendars enabled initially.
	if got := len(app.filteredCalendarEvents()); got != 3 {
		t.Fatalf("expected 3 unfiltered events, got %d", got)
	}
	if lbl := app.calendarFilterButtonLabel(); lbl != "Alle Kalender" && lbl != "All Calendars" {
		t.Fatalf("unexpected all-calendars label: %q", lbl)
	}

	// Multi-select: hide Family calendar -> affects display, search, and todayEvents().
	app.setCalendarVisible("cal-family", false)
	if got := len(app.filteredCalendarEvents()); got != 2 {
		t.Fatalf("expected 2 events after hiding Family, got %d", got)
	}
	if got := len(app.todayEvents()); got != 2 {
		t.Fatalf("todayEvents must respect hidden calendars, got %d", got)
	}
	if !strings.Contains(app.calendarFilterButtonLabel(), "2 / 3") {
		t.Fatalf("expected '2 / 3' label, got %q", app.calendarFilterButtonLabel())
	}

	// Search respects hidden calendars AND matches location/description/title.
	app.calSearchQuery = "Hockey"
	if got := len(app.filteredCalendarEvents()); got != 0 {
		t.Fatalf("hidden calendar event must not appear in calendar search, got %d", got)
	}
	app.calSearchQuery = "Stammheim"
	if got := app.filteredCalendarEvents(); len(got) != 1 || got[0].ID != "e3" {
		t.Fatalf("expected location search 'Stammheim' to match e3, got %+v", got)
	}
	if clean := cleanEventPreviewText("Hallo<br><br><b>Welt</b>"); clean != "Hallo Welt" {
		t.Fatalf("cleanEventPreviewText = %q, want 'Hallo Welt'", clean)
	}

	// Render in calendar mode: toolbar shows calendar filter button instead of mail All Accounts.
	tt := ui.NewTester(app.RenderMain, 1400, 900)
	tt.Frame()
	if _, ok := tt.Find("Faustball Spiel"); !ok {
		t.Fatal("expected matching calendar event 'Faustball Spiel' rendered in Agenda")
	}
}
