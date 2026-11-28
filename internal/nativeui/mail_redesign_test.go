package nativeui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

func TestBuildReaderBodyCountsTrackingPixels(t *testing.T) {
	msg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{ID: "m-px-1"},
		BodyHtml: `<html><body>
			<p>Hello world</p>
			<img src="https://track.example.com/open.gif" width="1" height="1" alt="">
			<img src="https://track.example.com/pixel.png" style="width: 1px; height: 1px;" alt="">
			<img src="https://cdn.example.com/hero.jpg" width="600" height="300" alt="Hero">
		</body></html>`,
	}
	rb := buildReaderBodyWithImages(msg, false)
	if rb.TrackingPixels != 2 {
		t.Fatalf("expected 2 tracking pixels counted, got %d", rb.TrackingPixels)
	}
	if rb.BlockedImages != 1 || rb.RemoteImages != 1 {
		t.Fatalf("expected 1 blocked non-pixel remote image, got blocked=%d remote=%d", rb.BlockedImages, rb.RemoteImages)
	}
}

func TestSenderAvatarAndSnoozePresets(t *testing.T) {
	if got := senderInitials("Alice Weber <alice@acme.de>"); got != "AW" {
		t.Fatalf("senderInitials = %q, want AW", got)
	}
	if got := senderInitials("billing@stripe.com"); got != "BI" {
		t.Fatalf("senderInitials email = %q, want BI", got)
	}
	if got := senderInitials("max.muster@example.de"); got != "MM" {
		t.Fatalf("senderInitials dotted email = %q, want MM", got)
	}
	if got := senderInitials("Ümit <u@test.de>"); got != "ÜM" {
		t.Fatalf("senderInitials unicode = %q, want ÜM", got)
	}
	if c1, c2 := senderAvatarColor("Alice <alice@acme.de>"), senderAvatarColor("alice@acme.de"); c1 != c2 {
		t.Fatalf("expected deterministic avatar color for same email, got %v vs %v", c1, c2)
	}

	now := time.Date(2026, 11, 27, 10, 15, 0, 0, time.Local) // Friday
	presets := nextSnoozePresets(now, "de")
	if len(presets) != 3 {
		t.Fatalf("expected 3 snooze presets, got %d", len(presets))
	}
	for _, p := range presets {
		if !p.Until.After(now) {
			t.Fatalf("preset %q must be in the future: %v <= %v", p.ID, p.Until, now)
		}
	}
	if presets[1].Until.Hour() != 8 || presets[1].Until.Day() != 28 {
		t.Fatalf("tomorrow preset unexpected: %v", presets[1].Until)
	}
	if presets[2].Until.Weekday() != time.Monday || presets[2].Until.Hour() != 8 {
		t.Fatalf("next week preset should be Monday 08:00, got %v", presets[2].Until)
	}
}

func TestInstallMCPServerConfigPreservesOtherServersAndBackup(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "claude_desktop_config.json")

	initial := `{
  "theme": "dark",
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem"]
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installMCPServerConfig(cfgPath, "/Applications/PostDive.app/Contents/MacOS/PostDive", []string{"--mcp", "--allow-drafts"}); err != nil {
		t.Fatalf("installMCPServerConfig failed: %v", err)
	}
	bak, err := os.ReadFile(cfgPath + ".postdive.bak")
	if err != nil || string(bak) != initial {
		t.Fatalf("expected .postdive.bak backup with original content, err=%v", err)
	}
	if !isMCPServerInstalled(cfgPath) {
		t.Fatal("expected isMCPServerInstalled == true")
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["theme"] != "dark" {
		t.Fatalf("top-level key lost: %+v", parsed)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["filesystem"]; !ok {
		t.Fatalf("existing MCP server was lost: %+v", servers)
	}
	pd := servers["postdive"].(map[string]any)
	if pd["command"] != "/Applications/PostDive.app/Contents/MacOS/PostDive" {
		t.Fatalf("unexpected postdive command: %+v", pd)
	}

	// Malformed JSON must fail cleanly without overwriting.
	badPath := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(badPath, []byte("{not json"), 0o600)
	if err := installMCPServerConfig(badPath, "postdive", []string{"--mcp"}); err == nil {
		t.Fatal("expected error for malformed existing JSON")
	}
}

func TestConsistentRightAIDockPrivacyAndPhoneParity(t *testing.T) {
	app, _, _ := keymapApp(t)
	updates := make(chan func(), 32)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)
	app.storeLang("de")
	app.readOnly = false
	app.engineReady = true
	app.showCopilot = true
	app.navID = "focus"
	app.snoozedCount = 2
	app.messages = []backend.MessageHeader{
		{
			ID: "m1", Subject: "Q4 Review", From: "Alice Weber <alice@acme.de>",
			Date: time.Now().Format(time.RFC3339), Unread: true, Flagged: true, HasAttachments: true,
		},
		{
			ID: "m2", Subject: "Rechnung", From: "Billing <bill@vendor.io>",
			Date: time.Now().Format(time.RFC3339), Read: true,
		},
	}
	app.waitingReplyIDs = map[string]bool{"m1": true}
	app.waitingReplies = []backend.MessageHeader{app.messages[0]}
	hdr := app.messages[0]
	hdr.To = []string{"me@example.org"}
	msg := &backend.MessageDetail{
		MessageHeader: hdr,
		BodyText:      "Hi, could we meet tomorrow?",
	}
	app.selectedID = "m1"
	app.selectedMsg = msg
	app.readerBody = readerBody{MsgID: "m1", Markdown: "Hi, could we meet tomorrow?", TrackingPixels: 3}
	app.inspectorMode = "translate"
	app.inspectorResult = "Hallo, können wir uns morgen treffen?"

	tt := ui.NewTester(app.RenderMain, 1280, 820)
	tt.Frame()

	for _, want := range []string{
		"Zurückgestellt",
		"Abos aufräumen",
		"3 Tracker blockiert",
		"Wartet auf Antwort",
		"Zusammenfassen",
		"Übersetzen",
		"KI-Analyse (Übersetzung)",
		"Hallo, können wir uns morgen treffen?",
		"Schnellantwort an Alice Weber",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected %q in main redesigned UI; texts=%v", want, tt.Texts())
		}
	}

	// Privacy guard: encrypted mail must NEVER be sent to AI for translate or quick_replies.
	encMsg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{ID: "m-enc", Subject: "Secret", From: "bob@acme.de"},
		BodyText:      "Decrypted secret text",
		Security:      backend.MessageSecurity{Encrypted: true, Decryption: "success"},
	}
	app.selectedMsg = encMsg
	app.selectedID = "m-enc"
	app.inspectorLoading = false
	app.inspectorError = ""
	app.RunInspectorAI(context.Background(), "translate")
	if app.inspectorLoading || !strings.Contains(strings.ToLower(app.inspectorError), "verschlüsselt") {
		t.Fatalf("expected encrypted mail refusal for translate, loading=%v err=%q", app.inspectorLoading, app.inspectorError)
	}
	app.RunQuickReplySuggestions(context.Background(), encMsg)
	if app.quickReply.aiLoading || !strings.Contains(strings.ToLower(app.quickReply.aiErr), "verschlüsselt") {
		t.Fatalf("expected encrypted mail refusal for quick replies, loading=%v err=%q", app.quickReply.aiLoading, app.quickReply.aiErr)
	}

	// Filter pills narrow visible messages cleanly.
	app.mailListFilter = 1 // Unread only -> m1 only
	if vis := app.visibleMessageIndices(); len(vis) != 1 || app.messages[vis[0]].ID != "m1" {
		t.Fatalf("expected filter Unread to match only m1, got %+v", vis)
	}

	// Phone UI parity
	app.winW = 390
	ph := ui.NewTester(app.RenderMain, 390, 844)
	ph.Frame()
	if !ph.HasText("Zurückgestellt") || !ph.HasText("Abos aufräumen…") {
		t.Fatalf("expected phone mailboxes to render Snoozed and Subscriptions; texts=%v", ph.Texts())
	}
	app.phoneOpenView("focus")
	app.selectedMsg = msg
	app.selectedID = "m1"
	app.readerBody = readerBody{MsgID: "m1", Markdown: "Hi, could we meet tomorrow?", TrackingPixels: 3}
	app.phoneShowRoutes(phoneRouteList, phoneRouteMail)
	ph.Frame()
	if !ph.HasText("3 Tracker blockiert") || !ph.HasText("Schnellantwort an Alice Weber") {
		t.Fatalf("expected phone mail view to render tracker pill & quick reply card; texts=%v", ph.Texts())
	}
}

func TestFilterPillSelectionAndMoveRespectVisibleRows(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.setMessages([]backend.MessageHeader{
		{ID: "m1", MessageID: "m1@x", AccountID: "acc1", Folder: "INBOX", From: "Alice <a@x.de>", Subject: "Unread 1", Unread: true},
		{ID: "m2", MessageID: "m2@x", AccountID: "acc1", Folder: "INBOX", From: "Bob <b@x.de>", Subject: "Read 2", Read: true},
		{ID: "m3", MessageID: "m3@x", AccountID: "acc1", Folder: "INBOX", From: "Carol <c@x.de>", Subject: "Unread 3", Unread: true},
	})
	app.setMailListFilter(1) // Unread only -> m1, m3

	app.SelectAllVisibleMails()
	checked := app.CheckedMessageIDs()
	if len(checked) != 2 || checked[0] != "m1" || checked[1] != "m3" {
		t.Fatalf("SelectAllVisibleMails should only select m1 and m3, got %v", checked)
	}
	if app.checkedIDs["m2"] {
		t.Fatal("hidden message m2 must not be checked by SelectAllVisibleMails")
	}

	app.ClearMailSelection()
	app.SetMailChecked(0, true, false) // m1
	app.SetMailChecked(2, true, true)  // Shift-range to m3 -> m2 stays hidden/unchecked
	if app.checkedIDs["m2"] {
		t.Fatal("Shift-range selection across visible rows must not check hidden m2")
	}
	if checked := app.CheckedMessageIDs(); len(checked) != 2 || checked[0] != "m1" || checked[1] != "m3" {
		t.Fatalf("expected [m1 m3] checked, got %v", checked)
	}

	// moveSelection & neighborAfterRemoval must skip hidden m2 cleanly.
	app.selectedID = "m1"
	app.moveSelection(context.Background(), 1)
	if app.selectedID != "m3" {
		t.Fatalf("expected moveSelection(1) to skip hidden m2 and land on m3, got %q", app.selectedID)
	}
	if next := app.neighborAfterRemoval("m1"); next != "m3" {
		t.Fatalf("expected neighborAfterRemoval(m1) == m3, got %q", next)
	}
}

func TestComposeAIDockShortcutIsolationAndCalendarHiding(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.storeLang("de")
	app.OpenComposeReply(&backend.MessageDetail{
		MessageHeader: backend.MessageHeader{ID: "m-1", Subject: "Termin", From: "Alice <alice@acme.de>", Date: time.Now().Format(time.RFC3339)},
		BodyText:      "Passt Freitag?",
	}, "")
	app.composeAIOpen = true

	tt := ui.NewTester(app.RenderComposeWindow, 920, 680)
	tt.Frame()
	gen, ok := tt.Find("Generieren")
	if !ok {
		t.Fatalf("expected Generate button in Compose AI dock; texts=%v", tt.Texts())
	}
	tt.ClickAt(gen.X-60, gen.Y+gen.H/2)
	tt.Type("Kurz zusagen")
	if app.composeAIPrompt != "Kurz zusagen" {
		t.Fatalf("expected cmp-ai-prompt focused and typed, got %q", app.composeAIPrompt)
	}
	seqBefore := app.composeAISeq
	tt.Key(ui.Cmd, ui.KeyEnter)
	tt.Frame()
	if app.composeAISeq <= seqBefore {
		t.Fatalf("expected Cmd+Enter inside cmp-ai-prompt to trigger RunComposeAI (seq %d -> %d)", seqBefore, app.composeAISeq)
	}
	if app.composeConfirm {
		t.Fatal("Cmd+Enter inside cmp-ai-prompt must not arm/trigger outer Compose send shortcut")
	}

	// Main Inspector right AI dock shows Draft actions even when inspectorResult is empty, and hides on calendar.
	app.closeCompose()
	app.showCopilot = true
	app.navID = "focus"
	app.selectedID = "m1"
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Hallo",
	}
	app.inspectorMode = "draft"
	app.inspectorResult = ""
	app.inspectorDraft = "Hallo Alice, passt!"
	tm := ui.NewTester(app.RenderMain, 1280, 820)
	tm.Frame()
	if !tm.HasText("In Antworten öffnen") {
		t.Fatalf("expected Draft adopt button when inspectorDraft is set; texts=%v", tm.Texts())
	}
	app.navID = "calendar"
	tm.Frame()
	if tm.HasText("KI-Analyse") || tm.HasText("In Antworten öffnen") {
		t.Fatalf("expected mail inspector AI card hidden in calendar view; texts=%v", tm.Texts())
	}
}

func TestSnoozeReadOnlyAndUnsubscribeSSRFGuard(t *testing.T) {
	_, _, st := keymapApp(t)
	roCfg := config.Default()
	roCfg.Mailbox.ReadOnly = true
	bc := backend.New(backend.Options{
		LoadConfig: func() (config.Config, error) { return roCfg, nil },
		Engine:     st.rpc,
	})
	if err := bc.SnoozeMessage(context.Background(), "m1", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected SnoozeMessage to fail when mailbox.readOnly is true")
	}

	if !isSafePublicHTTPSURL("https://digest.io/unsub?id=42") {
		t.Fatal("expected public HTTPS URL to be accepted")
	}
	for _, bad := range []string{
		"http://digest.io/unsub",
		"https://localhost/unsub",
		"https://127.0.0.1/unsub",
		"https://10.0.0.1/unsub",
		"https://printer.local/unsub",
		"ftp://digest.io/unsub",
	} {
		if isSafePublicHTTPSURL(bad) {
			t.Fatalf("expected SSRF URL %q to be rejected", bad)
		}
	}
}
