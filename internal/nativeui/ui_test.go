package nativeui

import (
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/jobs"
	"github.com/diver80/postdive/internal/triage"
)

type stubBridgeRPC struct {
	mu            sync.Mutex
	filterUpserts []map[string]any
	trashedIDs    []string
}

func (*stubBridgeRPC) Connected() bool { return true }
func (s *stubBridgeRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	today := time.Now().Format("2006-01-02")
	switch method {
	case "accounts.list":
		return json.RawMessage(`{"accounts":[{"id":"acc1","name":"Work","type":"imap"}]}`), nil
	case "folders.list":
		return json.RawMessage(`{"folders":[{"id":"f1","accountId":"acc1","name":"INBOX","path":"/INBOX","type":"inbox"}]}`), nil
	case "messages.list":
		return json.RawMessage(`{"messages":[{"id":"m1","accountId":"acc1","folder":"INBOX","subject":"Angebot Q4","from":"Alice <alice@acme.de>","date":"` + today + `T08:15:00Z","unread":true,"flagged":true},{"id":"m2","accountId":"acc1","folder":"INBOX","subject":"Reisekosten","from":"Bob <bob@acme.de>","date":"` + today + `T07:30:00Z","unread":false}],"total":2}`), nil
	case "messages.get":
		return json.RawMessage(`{"id":"m1","accountId":"acc1","folder":"INBOX","subject":"Angebot Q4","from":"Alice <alice@acme.de>","to":["frank@acme.de"],"date":"` + today + `T08:15:00Z","unread":true,"bodyText":"Hallo Frank,\nbitte pr\u00fcf das Q4-Angebot bis Freitag 15 Uhr.\nDanke!\n\n--\nAlice Weber\nHead of Engineering · ACME GmbH\nTel: +49 30 555010\nhttps://acme.de"}`), nil
	case "messages.actions":
		raw, _ := json.Marshal(params)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if trash, _ := req["trash"].(bool); trash {
			s.mu.Lock()
			if id, _ := req["id"].(string); id != "" {
				s.trashedIDs = append(s.trashedIDs, id)
			}
			s.mu.Unlock()
		}
		return json.RawMessage(`{"ok":true}`), nil
	case "filters.list":
		return json.RawMessage(`{"supported":true,"filters":[]}`), nil
	case "filters.upsert":
		raw, _ := json.Marshal(params)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		s.mu.Lock()
		s.filterUpserts = append(s.filterUpserts, req)
		s.mu.Unlock()
		name, _ := req["name"].(string)
		b, _ := json.Marshal(map[string]any{"supported": true, "ok": true, "action": "created", "accountId": "acc1", "name": name})
		return json.RawMessage(b), nil
	case "calendar.list":
		return json.RawMessage(`{"supported":true,"calendars":[{"id":"c1","name":"Arbeit","type":"local","readOnly":false}]}`), nil
	case "calendar.events":
		return json.RawMessage(`{"supported":true,"events":[{"id":"e1","calendarId":"c1","calendarName":"Arbeit","title":"Design Review","start":"` + today + `T14:00:00Z","end":"` + today + `T15:00:00Z","location":"Zoom"}]}`), nil
	case "contacts.search":
		return json.RawMessage(`{"supported":true,"addressBooks":[{"id":"ab1","name":"Persönliches Adressbuch","readOnly":false}],"contacts":[{"id":"ct1","addressBookId":"ab1","displayName":"Alice Weber","email":"alice@acme.de"}]}`), nil
	case "contacts.upsert":
		return json.RawMessage(`{"ok":true,"action":"enriched","contact":{"id":"ct1","displayName":"Alice Weber","email":"alice@acme.de","phone":"+49 30 555010","company":"ACME GmbH"}}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestNativeUIRendersMainAndStatusWidgetHeadlessly(t *testing.T) {
	tmp := t.TempDir()
	store := triage.NewStoreAt(tmp)

	var aiCalls atomic.Int32
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "gpt-4o-mini", "object": "model"}},
			})
			return
		}
		n := aiCalls.Add(1)
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmp-1",
				"object": "chat.completion",
				"model":  "gpt-4o-mini",
				"choices": []map[string]any{{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id":   "tc-1",
							"type": "function",
							"function": map[string]any{
								"name":      "search_messages",
								"arguments": `{"unread":true}`,
							},
						}},
					},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "cmp-2",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "## Briefing\n\n- **Angebot Q4** von Alice wartet auf Freigabe.",
				},
			}},
		})
	}))
	defer aiSrv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	cfg.Theme.Mode = "preset"
	cfg.Theme.Preset = "tokyo-night"
	cfg.AI.Agent = config.ProviderConfig{
		Backend:  "openai",
		Model:    "gpt-4o-mini",
		Endpoint: aiSrv.URL + "/v1",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The bridge server and app goroutines read the config while the UI thread saves it.
	box := newCfgBox(cfg)
	rpc := &stubBridgeRPC{}
	bc := backend.New(backend.Options{Engine: rpc, Events: events.NewHub(), Store: store, LoadConfig: box.load, Version: "test"})

	// Simulate mygo's single UI thread event loop.
	uiQueue := make(chan func(), 128)
	drainUI := func() {
		for {
			select {
			case fn := <-uiQueue:
				if fn != nil {
					fn()
				}
			default:
				return
			}
		}
	}
	waitUntil := func(cond func() bool) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			drainUI()
			if cond() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	var alerts []AlertItem
	app := NewApp(Options{
		ConfigLoader: box.load,
		ConfigSaver:  box.save,
		Store:        store,
		Backend:      bc,
	})
	app.SetHooks(
		func(fn func()) { uiQueue <- fn },
		nil,
		nil,
		nil,
		func(al AlertItem) { alerts = append(alerts, al) },
	)

	assertNoLegacyCopilot := func(texts []string) {
		t.Helper()
		for _, s := range texts {
			if strings.Contains(strings.ToLower(s), "co-pilot") {
				t.Fatalf("unexpected legacy Co-Pilot label still present: %q", s)
			}
		}
	}

	// 1. Load live Thunderbird data into native UI state.
	app.RefreshAll(ctx)
	waitUntil(func() bool { return !app.loading && !app.modelsLoading })
	if !app.engineReady {
		t.Fatalf("expected the engine to be ready")
	}
	if len(app.messages) != 2 || len(app.events) != 1 {
		t.Fatalf("unexpected loaded counts: msgs=%d events=%d", len(app.messages), len(app.events))
	}

	// Select first message and drain UI updates until loaded.
	app.SelectMessage(ctx, "m1")
	waitUntil(func() bool { return app.selectedMsg != nil })
	if app.selectedMsg == nil || app.selectedMsg.Subject != "Angebot Q4" {
		t.Fatalf("expected selected message loaded, got %+v", app.selectedMsg)
	}

	// 2. Render Main Window across Mail, Calendar, Light/Dark themes, and Modals using ui.NewTester.
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	if _, ok := tt.Find("PostDive"); !ok {
		t.Fatal("expected PostDive header in main view")
	}
	if _, ok := tt.Find("Angebot Q4"); !ok {
		t.Fatal("expected message subject in main view")
	}
	// AI Assistant starts collapsed by default and opens when toggled.
	app.showCopilot = true
	tt.Frame()
	if !tt.HasText("KI-Assistent") {
		t.Fatalf("expected renamed KI-Assistent header in DE mode; texts=%v", tt.Texts())
	}
	assertNoLegacyCopilot(tt.Texts())

	// Switch to Calendar workspace and re-render.
	app.navID = "calendar"
	tt.Frame()
	if _, ok := tt.Find("Design Review"); !ok {
		t.Fatal("expected Design Review event in calendar view")
	}

	// Switch theme dynamically to light preset and open Compose Modal.
	app.applyThemeChoice("Flexoki Light (flexoki-light)")
	if cur, _ := box.load(); !app.activePalette.IsLight || cur.Theme.Preset != "flexoki-light" {
		t.Fatalf("expected flexoki-light applied, got %+v", cur.Theme)
	}
	app.OpenComposeNew()
	tt.Frame()
	if _, ok := tt.Find("Neue E-Mail"); !ok {
		t.Fatal("expected compose modal rendered")
	}
	app.composeOpen = false
	app.navID = "focus"

	// 3. Run AI Assistant prompt with tool calling and verify markdown renders cleanly.
	app.SendCopilotPrompt(ctx, "Was ist wichtig?")
	waitUntil(func() bool {
		return len(app.chatEntries) >= 2 && !app.chatStreaming
	})
	if len(app.chatEntries) < 2 || !strings.Contains(app.chatEntries[1].Content, "Angebot Q4") {
		t.Fatalf("unexpected assistant chat entries: %+v", app.chatEntries)
	}
	tt.Frame()
	if !tt.HasText("search_messages") {
		t.Fatalf("expected tool execution badge rendered in AI Assistant pane; texts=%v", tt.Texts())
	}

	// 3b. Open Help Modal and verify Overview, Interactive TOC, Filter rules note, and AI transparency.
	app.helpOpen = true
	tt.Frame()
	if !tt.HasText("Was PostDive macht · Überblick") || !tt.HasText("Inhalt:") || !tt.HasText("100% Lokal") || !tt.HasText("4. Quick-Regeln & Automatische Filter") || !tt.HasText("Filterregeln verwalten") {
		t.Fatalf("expected help modal overview, TOC, and filter rule sections rendered; texts=%v", tt.Texts())
	}
	app.helpTopic = "rules"
	tt.Frame()
	if tt.HasText("1. 100% Lokal · Keine KI, 0 Tokens") || !tt.HasText("Filterregeln verwalten") {
		t.Fatalf("expected help TOC filtering to show only rules chapter; texts=%v", tt.Texts())
	}
	app.helpTopic = "all"
	assertNoLegacyCopilot(tt.Texts())
	app.helpOpen = false

	// 3c. Verify contact discovery cross-check against address book & enrich flow.
	props := buildContactProposals([]ai.ExtractedContact{
		{
			DisplayName: "Alice Weber", FirstName: "Alice", LastName: "Weber",
			Email: "alice@acme.de", Phone: "+49 30 555010", Company: "ACME GmbH",
		},
		{
			DisplayName: "Bob Meyer", FirstName: "Bob", LastName: "Meyer",
			Email: "bob@partner.io", Mobile: "+49 170 223344", Company: "Partner AG",
		},
	}, []backend.ContactRecord{
		{ID: "ct1", DisplayName: "Alice Weber", Email: "alice@acme.de"},
	})
	if len(props) != 2 || props[0].Status != "enrich" || props[1].Status != "new" {
		t.Fatalf("expected 1 enrich + 1 new contact proposal, got %+v", props)
	}
	app.readOnly = false
	app.contactsBookLabels = []string{"Persönliches Adressbuch"}
	app.contactsBookByLabel = map[string]string{"Persönliches Adressbuch": "ab1"}
	app.selectedContactBook = "Persönliches Adressbuch"
	app.inspectorMode = "extract_contacts"
	app.inspectorContacts = props
	tt.Frame()
	if !tt.HasText("Daten vervollständigen") || !tt.HasText("Kontakt importieren") {
		t.Fatalf("expected contact import/enrich buttons rendered; texts=%v", tt.Texts())
	}
	app.ApplyContactProposal(ctx, 0)
	waitUntil(func() bool { return !app.contactsBusy && app.inspectorContacts[0].Applied })
	if !app.inspectorContacts[0].Applied {
		t.Fatal("expected ApplyContactProposal to mark contact applied")
	}

	// 4. Render Menu Bar Status Widget Flyout and verify tray status strings + notifications.
	fly := ui.NewTester(app.RenderStatusWidget, 400, 510)
	fly.Frame()
	if _, ok := fly.Find("Heute im Kalender"); !ok {
		t.Fatal("expected calendar section in tray status widget")
	}
	assertNoLegacyCopilot(fly.Texts())
	if !strings.Contains(app.TrayTitle(), "1") || !strings.Contains(app.TrayTooltip(), "PostDive Live") {
		t.Fatalf("unexpected tray strings: title=%q tip=%q", app.TrayTitle(), app.TrayTooltip())
	}

	// 5. Switch UI language dynamically to English and verify Main, Contacts, Help, and Tray Widget.
	app.inspectorContacts[0].Applied = false
	app.SetLanguage("en")
	if cur, _ := box.load(); app.uiLang() != "en" || cur.Language != "en" {
		t.Fatalf("expected language switched to en, got ui=%q cfg=%q", app.uiLang(), cur.Language)
	}
	tt.Frame()
	if !tt.HasText("AI Assistant") || !tt.HasText("Focus (Unread & ★)") || !tt.HasText("AI ▾") || !tt.HasText("More actions") {
		t.Fatalf("expected English main view labels; texts=%v", tt.Texts())
	}
	if !tt.HasText("Complete Missing Fields") || !tt.HasText("Import Contact") {
		t.Fatalf("expected English contact discovery buttons; texts=%v", tt.Texts())
	}
	if !tt.HasText("Phone") || !tt.HasText("Company") {
		t.Fatalf("expected English refreshed contact missing field labels; texts=%v", tt.Texts())
	}
	assertNoLegacyCopilot(tt.Texts())

	app.helpOpen = true
	tt.Frame()
	if !tt.HasText("Help · Overview, Filter Rules & AI Transparency") || !tt.HasText("What PostDive Does · Overview") || !tt.HasText("4. Quick Rules & Automatic Filters") || !tt.HasText("Manage Filter Rules") {
		t.Fatalf("expected English help modal with overview, TOC, and filter rules; texts=%v", tt.Texts())
	}
	assertNoLegacyCopilot(tt.Texts())
	app.helpOpen = false
	app.inspectorContacts = nil
	app.inspectorMode = ""

	fly.Frame()
	if !fly.HasText("Today's Calendar") || !fly.HasText("Unread") {
		t.Fatalf("expected English tray widget labels; texts=%v", fly.Texts())
	}
	assertNoLegacyCopilot(fly.Texts())

	// 5b. Switch back to DE, multi-select emails, preview Quick-Rule, save rule, and trash marked emails immediately.
	app.SetLanguage("de")
	app.ToggleMailChecked(0, false)
	app.ToggleMailChecked(1, false)
	tt.Frame()
	if !tt.HasText("2 markiert") || !tt.HasText("Quick-Regel") {
		t.Fatalf("expected bulk action bar for 2 checked messages; texts=%v", tt.Texts())
	}
	app.OpenQuickRuleWizard()
	tt.Frame()
	if !tt.HasText("Quick-Regel-Assistent · Filterregel") || !tt.HasText("Live-Preview vor dem Speichern") || !tt.HasText("Markiert: 2/2") || !tt.HasText("Filterregeln verwalten") {
		t.Fatalf("expected quick-rule modal with live preview and manage rules button; texts=%v", tt.Texts())
	}
	// alice@ + bob@acme.de derive "@acme.de" (whole domain) + trash: a broad destructive rule.
	// The first click only arms the confirmation; nothing is written yet.
	app.SaveAndApplyQuickRule(ctx)
	drainUI()
	rpc.mu.Lock()
	gotUpserts := len(rpc.filterUpserts)
	rpc.mu.Unlock()
	if gotUpserts != 0 || !app.quickRuleOpen || !app.quickRuleConfirmArmed() {
		t.Fatalf("expected broad rule to require a second confirmation, upserts=%d open=%v", gotUpserts, app.quickRuleOpen)
	}
	tt.Frame()
	if !tt.HasText("Breite Regel bestätigen & speichern") {
		t.Fatalf("expected clearly labelled confirm button; texts=%v", tt.Texts())
	}
	app.SaveAndApplyQuickRule(ctx)
	waitUntil(func() bool { return !app.quickRuleBusy && !app.quickRuleOpen })
	rpc.mu.Lock()
	gotUpserts = len(rpc.filterUpserts)
	gotTrashed := len(rpc.trashedIDs)
	var mode any
	if gotUpserts > 0 {
		mode = rpc.filterUpserts[0]["mode"]
	}
	rpc.mu.Unlock()
	if gotUpserts != 1 || gotTrashed != 2 || mode != "create" {
		t.Fatalf("expected 1 filter create and 2 trashed messages, got upserts=%d trashed=%d mode=%v err=%q", gotUpserts, gotTrashed, mode, app.quickRuleError)
	}

	// 6. Simulate an incoming mail event and verify alert creation + click navigation.
	app.handleEngineEvent(ctx, "mail.new", json.RawMessage(`{"accountId":"acc1","folderId":"f1","folder":"INBOX","count":1,"messages":[{"id":"m99","messageId":"m99@acme.de","subject":"Dringend","from":"Chef <chef@acme.de>","accountId":"acc1","folderId":"f1","unread":true}]}`))
	drainUI()
	if len(alerts) != 1 || alerts[0].MessageID != "m99" {
		t.Fatalf("expected 1 native alert for m99, got %+v", alerts)
	}
	app.HandleNotificationClick(alerts[0].ID)
	drainUI()
	if app.selectedID != "m99" {
		t.Fatalf("expected clicking notification to select m99, got %q", app.selectedID)
	}

	// Wait for any background SelectMessage from HandleNotificationClick before test finishes.
	time.Sleep(50 * time.Millisecond)
	drainUI()

	// Verify tray icon PNG is non-empty valid PNG.
	pngBytes := trayIconPNG()
	if len(pngBytes) < 32 {
		t.Fatalf("expected valid PNG tray icon, got %d bytes", len(pngBytes))
	}
}

// TestRenderScreens renders high-DPI PNG screenshots into POSTDIVE_RENDER (or OMATHUNDER_RENDER):
//
//	POSTDIVE_RENDER=docs/screenshots go test ./internal/nativeui -run TestRenderScreens
func TestRenderScreens(t *testing.T) {
	dir := os.Getenv("POSTDIVE_RENDER")
	if dir == "" {
		dir = os.Getenv("OMATHUNDER_RENDER")
	}
	if dir == "" {
		t.Skip("POSTDIVE_RENDER not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Paths in the screenshots (key file, archive folder) must not show the real user.
	t.Setenv("HOME", "/Users/you")
	t.Setenv("XDG_CONFIG_HOME", "")

	tmp := t.TempDir()
	store := triage.NewStoreAt(tmp)
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

	now := time.Now()
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	app.engineReady = true
	app.readOnly = false
	app.providerLabel = "anthropic (api)"
	app.agentBackend = "anthropic"
	app.agentModel = "claude-sonnet-4-5"
	app.modelChoices = []string{"claude-sonnet-4-5", "gemini-2.5-flash", "gpt-4o-mini"}
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work (IMAP)"}, {ID: "acc2", Name: "Privat"}}
	app.lastSync = now.Add(-2 * time.Minute)
	app.accountLabels = []string{"Alle Konten", "Work (IMAP)", "Privat"}
	app.accountIDByLabel = map[string]string{"Alle Konten": "", "Work (IMAP)": "acc1", "Privat": "acc2"}
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Posteingang", Path: "INBOX", Type: "inbox"},
		{ID: "f2", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Spaces", Path: "Spaces"},
		{ID: "f2a", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "ACME", Path: "Spaces/ACME"},
		{ID: "f3", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Rechnungen", Path: "Rechnungen"},
		{ID: "f5", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Archiv", Path: "Archives", Type: "archives"},
		{ID: "f5a", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "2025", Path: "Archives/2025"},
		{ID: "f5b", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "2026", Path: "Archives/2026"},
		{ID: "f4", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Gesendet", Path: "Sent", Type: "sent"},
		{ID: "f6", AccountID: "acc1", AccountName: "Work (IMAP)", Name: "Papierkorb", Path: "Trash", Type: "trash"},
		{ID: "p1", AccountID: "acc2", AccountName: "Privat", Name: "Posteingang", Path: "INBOX", Type: "inbox"},
		{ID: "p2", AccountID: "acc2", AccountName: "Privat", Name: "Reisen", Path: "Reisen"},
		{ID: "p3", AccountID: "acc2", AccountName: "Privat", Name: "Rom 2026", Path: "Reisen/Rom 2026"},
	}
	app.collapsedFolders = map[string]bool{"acc2\x00Reisen": true}
	app.messages = []backend.MessageHeader{
		{
			ID: "m1", AccountID: "acc1", Folder: "INBOX",
			From: "Alice Meyer <alice@acme.example>", To: []string{"alex@example.org"}, Subject: "Q4 Rollout & Budget-Freigabe bis Freitag",
			Date: today + "T09:15:00+01:00", Unread: true, Flagged: true,
		},
		{
			ID: "m2", AccountID: "acc1", Folder: "Spaces/ACME",
			From: "Markus Klein <m.klein@partner.example>", Subject: "Cloud Migration: Terminvorschlag Workshop",
			Date: today + "T08:40:00+01:00", Unread: true,
		},
		{
			ID: "m3", AccountID: "acc1", Folder: "Rechnungen",
			From: "Cloud Billing <billing@cloud.example>", Subject: "Rechnung #2026-11-4821 f\u00e4llig",
			Date: today + "T07:20:00+01:00", Unread: true,
		},
		{
			ID: "m4", AccountID: "acc1", Folder: "INBOX",
			From: "Sarah M\u00fcller <sarah@example.org>", Subject: "Review Notes: PostDive Native UI",
			Date: now.AddDate(0, 0, -1).Format("2006-01-02") + "T16:45:00+01:00", Unread: false,
		},
	}
	app.totalMessages = len(app.messages)
	app.focusUnread = 3
	app.snoozedCount = 1
	app.waitingReplyIDs = map[string]bool{"m2": true}
	app.waitingReplies = []backend.MessageHeader{app.messages[1]}
	app.selectedID = "m1"
	app.mailList.selected = 0
	app.triageState = triage.State{
		Classifications: map[string]triage.Classification{
			"m1": {MessageID: "m1", KanbanColumn: "action"},
			"m2": {MessageID: "m2", KanbanColumn: "waiting"},
			"m3": {MessageID: "m3", KanbanColumn: "invoice"},
		},
	}
	app.barSummary = triage.BarSummary{ActionCount: 2, WaitingCount: 1, InvoiceCount: 1}
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Hallo Alex,\n\ndie finale Kalkulation f\u00fcr das Q4-Rollout liegt vor:\n- Lizenzen & Infrastruktur: 14.200 EUR\n- Migration & Workshop (2 Tage): 3.600 EUR\n\nK\u00f6nnen wir heute um 14:00 Uhr kurz abstimmen, ob das Budget so freigegeben werden kann?\n\nViele Gr\u00fc\u00dfe\nAlice",
		BodyHtml: `<p>Hallo Alex,</p><p>die finale Kalkulation f&uuml;r das Q4-Rollout liegt vor:</p><ul><li>Lizenzen &amp; Infrastruktur: <b>14.200 EUR</b></li><li>Migration &amp; Workshop (2 Tage): <b>3.600 EUR</b></li></ul><p>K&ouml;nnen wir heute um 14:00 Uhr kurz abstimmen, ob das Budget so freigegeben werden kann?</p><p>Viele Gr&uuml;&szlig;e<br>Alice</p><img src="https://track.example.com/open.gif" width="1" height="1" alt="">`,
		Attachments: []backend.Attachment{
			{Name: "Q4-Kalkulation.pdf", Size: 248000, ContentType: "application/pdf"},
		},
	}
	app.inspectorMode = "summarize"
	app.inspectorResult = "**Kernaussage:** Alice bittet um Budget-Freigabe f\u00fcr das Q4-Rollout (**17.800 EUR** gesamt) und schl\u00e4gt ein kurzes Abstimmungsgespr\u00e4ch heute um **14:00 Uhr** vor.\n\n- **Frist:** Freitag\n- **N\u00e4chster Schritt:** Termin-Slot 14:00 pr\u00fcfen & Freigabe-Antwort entwerfen."
	app.inspectorDraft = "Hallo Alice,\n\nvielen Dank f\u00fcr die \u00dcbersicht. 14:00 Uhr passt mir heute gut \u2014 lass uns das Q4-Budget dort final freigeben.\n\nViele Gr\u00fc\u00dfe\nAlex"

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
		{ID: "e2", CalendarID: "c1", Calendar: "Arbeit", Title: "Q4 Rollout Budget-Abstimmung", Start: today + "T14:00:00+01:00", End: today + "T14:30:00+01:00", Location: "Zoom"},
		{ID: "e3", CalendarID: "c2", Calendar: "Privat", Title: "Zahnarzt Termin", Start: today + "T17:15:00+01:00", End: today + "T18:00:00+01:00", Location: "Innenstadt"},
		{ID: "e4", CalendarID: "c1", Calendar: "Arbeit", Title: "Partner Architecture Review", Start: tomorrow + "T11:00:00+01:00", End: tomorrow + "T12:00:00+01:00", Location: "Teams"},
	}
	wd := (int(now.Weekday()) + 6) % 7
	weekMon := now.AddDate(0, 0, -wd)
	for _, we := range []struct {
		id, calID, cal, title, loc, sh, eh string
		offset                             int
	}{
		{"mw1", "c1", "Arbeit", "Sprint Planning", "Meet", "09:30:00", "10:30:00", 0},
		{"mw2", "c1", "Arbeit", "Produkt-Review", "Zoom", "14:00:00", "15:00:00", 0},
		{"mw3", "c1", "Arbeit", "1:1 mit Sarah", "Teams", "10:00:00", "10:45:00", 1},
		{"mw4", "c2", "Privat", "Sport & Laufen", "Park", "18:00:00", "19:00:00", 1},
		{"mw5", "c1", "Arbeit", "Design Critique", "Figma", "11:00:00", "12:00:00", 2},
		{"mw6", "c1", "Arbeit", "Roadmap Q1", "Zoom", "13:30:00", "14:30:00", 3},
		{"mw7", "c2", "Privat", "Abendessen Team", "Bistro", "19:00:00", "20:15:00", 3},
		{"mw8", "c1", "Arbeit", "Release-Check", "Teams", "15:30:00", "16:30:00", 4},
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
	app.recentAlerts = []AlertItem{
		{ID: "postdive-mail-m1", Kind: "mail", Title: "Alice Meyer", Subtitle: "Neue E-Mail", Body: "Q4 Rollout & Budget-Freigabe"},
		{ID: "postdive-cal-e2", Kind: "calendar", Title: "Budget-Abstimmung", Subtitle: "Heute 14:00", Body: "Arbeit"},
	}
	app.chatEntries = []ChatEntry{
		{ID: "u-1", Role: "user", Content: "Gib mir mein Tages-Briefing f\u00fcr heute."},
		{
			ID:   "a-1",
			Role: "assistant",
			Tools: []ToolTrace{
				{ID: "t1", Name: "search_messages", Summary: "4 Mails gefunden", Done: true},
				{ID: "t2", Name: "list_calendar_events", Summary: "8 Kalendereintr\u00e4ge geladen", Done: true},
			},
			Content: "### Tages-Briefing\n\n**Priorit\u00e4t 1 \u2014 Mails:**\n- **Alice Meyer** wartet auf Freigabe f\u00fcr das **Q4-Rollout (17.800 EUR)** bis Freitag.\n- **Markus Klein** hat Workshop-Termine vorgeschlagen.\n\n**Heutiger Kalender:**\n| Zeit | Termin |\n| --- | --- |\n| 10:00 | Daily Engineering Sync |\n| 14:00 | Q4 Rollout Budget-Abstimmung |\n| 17:15 | Zahnarzt Termin |",
		},
	}

	saveShot := func(name string, view func(*ui.Context), w, h int) {
		tt := ui.NewTester(view, w, h)
		tt.SetScale(2)
		tt.Frame()
		tt.Advance(120 * time.Millisecond)
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	saveShot("app-main.png", app.RenderMain, 1380, 860)
	saveShot("postdive-tray-widget.png", app.RenderStatusWidget, 400, 510)

	// Screenshot 2: Footer Contact Discovery card in Inspector (1 existing to enrich + 1 new to import)
	app.inspectorMode = "extract_contacts"
	app.inspectorResult = "2 Kontakte im E-Mail-Footer erkannt und lokal mit deinem Adressbuch abgeglichen."
	app.inspectorDraft = ""
	app.contactsBookLabels = []string{"Persönliches Adressbuch", "Geschäftskontakte"}
	app.contactsBookByLabel = map[string]string{"Persönliches Adressbuch": "ab1", "Geschäftskontakte": "ab2"}
	app.selectedContactBook = "Persönliches Adressbuch"
	app.inspectorContacts = []ContactProposal{
		{
			Extracted: ai.ExtractedContact{
				DisplayName: "Alice Meyer", FirstName: "Alice", LastName: "Meyer",
				Email: "alice@acme.example", Phone: "+49 30 555010", Mobile: "+49 170 9876543",
				Company: "ACME GmbH", JobTitle: "Head of Engineering", Website: "https://acme.example",
				Address: "Musterstr. 12, 10115 Berlin",
			},
			Existing:         &backend.ContactRecord{ID: "ct1", DisplayName: "Alice Meyer", Email: "alice@acme.example"},
			Status:           "enrich",
			MissingFieldKeys: []string{"phone", "mobile", "company", "job_title", "website", "address"},
			MissingFields:    []string{"Telefon", "Mobil", "Firma", "Position", "Website", "Adresse"},
		},
		{
			Extracted: ai.ExtractedContact{
				DisplayName: "Markus Klein", FirstName: "Markus", LastName: "Klein",
				Email: "m.klein@partner.example", Phone: "+49 89 441200", Company: "Partner.example AG",
				JobTitle: "Solution Architect",
			},
			Status: "new",
		},
	}
	saveShot("postdive-contacts-dark.png", app.RenderMain, 1380, 860)

	// Screenshot 3: Help Modal (Overview + Interactive TOC)
	app.inspectorContacts = nil
	app.helpTopic = "all"
	app.helpOpen = true
	saveShot("postdive-help-dark.png", app.RenderMain, 1380, 860)
	app.helpTopic = "shortcuts"
	saveShot("postdive-help-shortcuts-dark.png", app.RenderMain, 1380, 860)
	app.helpTopic = "all"
	app.helpOpen = false

	// Screenshot 3b: Quick-Rule Wizard with Multi-Select & Live Preview
	prevMsgs := append([]backend.MessageHeader(nil), app.messages...)
	app.messages = append([]backend.MessageHeader{
		{
			ID: "m-nl-1", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.example>", Subject: "[Daily Digest] Nightly Build OK #412",
			Date: today + "T06:05:00+01:00", Unread: true,
		},
		{
			ID: "m-nl-2", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.example>", Subject: "[Daily Digest] Staging Sync OK #411",
			Date: today + "T05:50:00+01:00", Unread: true,
		},
		{
			ID: "m-nl-3", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.example>", Subject: "[Daily Digest] Backup Report OK #410",
			Date: now.AddDate(0, 0, -1).Format("2006-01-02") + "T06:00:00+01:00", Unread: false,
		},
	}, app.messages...)
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(1, true, false)
	app.OpenQuickRuleWizard()
	saveShot("app-quick-rule.png", app.RenderMain, 1380, 860)
	app.quickRuleOpen = false
	app.ClearMailSelection()
	app.messages = prevMsgs

	// Screenshot 3c: "Move to…" picker (v)
	app.OpenMovePicker()
	saveShot("postdive-move-dark.png", app.RenderMain, 1380, 860)
	app.moveOpen = false

	// Screenshot 3c1: the open mail inside its conversation (newest on top) + right-side AI dock
	prevInspector, prevDraft := app.inspectorResult, app.inspectorDraft
	app.inspectorResult, app.inspectorDraft, app.inspectorMode = "", "", ""
	app.showCopilot = true
	app.conversation = conversationUI{forID: "m1", open: map[string]*convItem{}, conv: &backend.Conversation{ThreadID: "th:q4", Messages: []backend.ConversationMessage{
		{MessageHeader: backend.MessageHeader{ID: "c3", From: "Alex Muster <alex@example.org>", Date: today + "T09:40:00+01:00", Folder: "Sent"}, Snippet: "Danke Alice, 14:00 passt. Ich bringe die Freigabe gleich mit."},
		{MessageHeader: app.messages[0]},
		{MessageHeader: backend.MessageHeader{ID: "c1", From: "Alice Meyer <alice@acme.example>", Date: now.AddDate(0, 0, -2).Format("2006-01-02") + "T16:20:00+01:00"}, Snippet: "Hier die erste Schätzung für das Rollout – Lizenzen noch ohne Rabatt."},
		{MessageHeader: backend.MessageHeader{ID: "c0", From: "Alex Muster <alex@example.org>", Date: now.AddDate(0, 0, -3).Format("2006-01-02") + "T11:05:00+01:00", Folder: "Sent"}, Snippet: "Kannst du mir bis Ende der Woche eine Kalkulation für Q4 schicken?"},
	}}}
	saveShot("app-conversation.png", app.RenderMain, 1380, 1100)
	app.showCopilot = false
	app.conversation = conversationUI{}
	app.inspectorResult, app.inspectorDraft, app.inspectorMode = prevInspector, prevDraft, "summarize"

	// Screenshot 3c1b: a reply with the quoted original, closed by Escape: it asks instead of
	// losing the text.
	app.OpenComposeReply(&backend.MessageDetail{MessageHeader: app.messages[0], BodyText: "Hallo Alex,\n\nkönnen wir heute um 14:00 kurz abstimmen?\n\nViele Grüße\nAlice"}, "Hallo Alice,\n\n14:00 passt.")
	app.guardComposeClose()
	app.composeBody = "Hallo Alice,\n\n14:00 passt, ich bringe die Freigabe mit." + strings.TrimPrefix(app.composeBody, "Hallo Alice,\n\n14:00 passt.")
	app.composeOpen = false
	app.guardComposeClose()
	saveShot("postdive-compose-ask-dark.png", app.RenderMain, 1380, 900)
	app.closeCompose()

	// Screenshot 3c2: settings (AI & 1-Click MCP tab) and the archive dialog
	app.OpenSettings()
	app.settings.tab = settingsTabAI
	app.settings.mcpProbed = true
	app.settings.mcpCmd = "/Applications/PostDive.app/Contents/MacOS/PostDive"
	app.settings.mcpTargets = []mcpClientTarget{
		{ID: "claude-desktop", Name: "Claude Desktop", ConfigPath: "/Users/you/Library/Application Support/Claude/claude_desktop_config.json"},
		{ID: "cursor", Name: "Cursor", ConfigPath: "/Users/you/.cursor/mcp.json"},
		{ID: "antigravity", Name: "Antigravity", ConfigPath: "/Users/you/.gemini/antigravity/mcp_config.json"},
	}
	app.settings.mcpInstalled = map[string]bool{"claude-desktop": true}
	{
		tt := ui.NewTester(app.RenderMain, 1380, 860)
		tt.SetScale(2)
		tt.Frame()
		// Scroll Settings AI tab down so the 1-Click MCP client setup section is clearly visible.
		tt.Move(690, 480)
		for range 12 {
			if r, ok := tt.Find("Cursor"); ok && r.Y >= 240 && r.Y <= 620 {
				break
			}
			tt.Wheel(690, 480, 0, 90)
			tt.Advance(300 * time.Millisecond)
			tt.Frame()
		}
		f, err := os.Create(filepath.Join(dir, "app-settings.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		_ = f.Close()
	}
	app.settings.open = false
	prevAccounts := app.accounts
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work"}}
	app.cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	app.OpenArchiveDialog(context.Background())
	app.arch.preview = &jobs.ArchivePreview{TotalCount: 2, TotalMatched: 2, TotalBytes: 48000, ScannedFolders: 1, Candidates: []jobs.ArchiveCandidate{
		{Subject: "Rechnung März 2024", From: "billing@acme.example", Date: "2024-03-02T09:00:00Z", Size: 24000},
		{Subject: "Angebot Messestand", From: "Laura Brandt <laura@messe.example>", Date: "2024-04-11T09:00:00Z", Size: 24000},
	}}
	saveShot("app-archive.png", app.RenderMain, 1380, 860)
	app.arch = archiveUI{}
	app.accounts = prevAccounts

	// Screenshot 3c3: Reply window with consistent Right-Side AI Assist dock
	app.OpenComposeReply(app.selectedMsg, "Hallo Alice,\n\n14:00 Uhr passt mir gut.")
	app.composeAIOpen = true
	app.composeAIResult = "Hallo Alice,\n\nvielen Dank f\u00fcr die finale Q4-Kalkulation! Die 17.800 EUR sind hiermit freigegeben \u2014 lass uns die Details um 14:00 Uhr im Zoom-Call kurz abstimmen.\n\nViele Gr\u00fc\u00dfe\nAlex"
	app.composeAIReview = []string{"Tonfall freundlich & klar \u00b7 R\u00fcckfrage zu 14:00 Uhr beantwortet."}
	saveShot("app-compose-ai.png", app.RenderComposeWindow, 1040, 680)
	app.closeCompose()

	// Screenshot 3d: an HTML mail, formatted (links by title, remote images blocked)
	prevDetail, prevMode, prevResult, prevDraft := app.selectedMsg, app.inspectorMode, app.inspectorResult, app.inspectorDraft
	app.inspectorMode, app.inspectorResult, app.inspectorDraft = "", "", ""
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{ID: "m1", AccountID: "acc1", Folder: "INBOX", From: "Confluence <confluence@example.atlassian.net>", To: []string{"Alex <alex@example.org>"}, Subject: "Acme SDK Release (2.0.0) plus more pages your team is working on", Date: today + "T10:56:00+01:00"},
		BodyText:      "Stay connected\n\nAcme SDK Release (2.0.0)\n[https://example.atlassian.net/wiki/spaces/GS/pages/1]",
		BodyHtml: `<div style="display:none">Stay connected to people and their work &zwnj;&nbsp;&zwnj;</div>
<table><tr><td><img src="https://cdn.example.net/logo.png" width="120" alt="Confluence"></td></tr><tr><td>
<h1>Stay connected to people and their work</h1><p>Your weekly content recommendations, ready to explore.</p>
<table><tr><td><img src="https://cdn.example.net/page.png" width="16" alt="Page"></td><td><a href="https://example.atlassian.net/wiki/spaces/GS/pages/2326593582?atl_f=x">Acme SDK Release Data Center (2.0.0) 2026/10/26</a></td></tr>
<tr><td>Owned by</td><td><b>Jane Doe</b> · 3 contributors · updated 2 days ago</td></tr></table>
<table><tr><td><a href="https://example.atlassian.net/wiki/spaces/GS/pages/2326593583">Mobile SDK roadmap Q1</a></td></tr>
<tr><td>Owned by</td><td><b>Lena Vogt</b> · 5 comments</td></tr></table>
<p>Questions? Read the <a href="https://support.atlassian.com/confluence-cloud/">https://support.atlassian.com/confluence-cloud/</a> or reply to this email.</p>
<ul><li>Pages you follow: <b>12</b></li><li>New comments: <b>4</b></li></ul>
<blockquote>“Great release notes, thanks!” — Patrick</blockquote>
<p><a href="https://example.atlassian.net/wiki/my/notifications"><img src="https://cdn.example.net/btn.png" alt="Manage notifications"></a></p>
<img src="https://track.example.net/o.gif" width="1" height="1">
</td></tr></table>`,
	}
	app.readerBody = readerBody{}
	saveShot("app-reading-pane.png", app.RenderMain, 1380, 860)
	app.readerPlain = true
	saveShot("postdive-mail-text-dark.png", app.RenderMain, 1380, 860)
	app.readerPlain = false
	app.selectedMsg, app.inspectorMode, app.inspectorResult, app.inspectorDraft = prevDetail, prevMode, prevResult, prevDraft
	app.readerBody = readerBody{}

	// Phone layout (Android, or a narrow window): mailboxes, a folder's mails, one mail, compose.
	prevNav := app.navID
	app.phone.router = ui.NewRouter(phoneRouteMailboxes)
	saveShot("postdive-phone-mailboxes.png", app.RenderMain, 390, 844)
	app.navID = "focus"
	app.phone.router = ui.NewRouter(phoneRouteList)
	saveShot("postdive-phone-list.png", app.RenderMain, 390, 844)
	app.phone.router = ui.NewRouter(phoneRouteMail)
	saveShot("postdive-phone-mail.png", app.RenderMain, 390, 844)
	app.phone.more = true
	saveShot("postdive-phone-mail-more.png", app.RenderMain, 390, 844)
	app.phone.more = false
	app.phone.router = ui.NewRouter(phoneRouteAssistant)
	saveShot("postdive-phone-assistant.png", app.RenderMain, 390, 844)
	app.OpenComposeReply(app.selectedMsg, "")
	saveShot("postdive-phone-compose.png", app.RenderMain, 390, 844)
	app.closeCompose()
	app.OpenSettings()
	saveShot("postdive-phone-settings.png", app.RenderMain, 390, 844)
	app.settings.open = false
	app.navID, app.phone.router = prevNav, nil

	// Screenshot 4: English UI switch verification shot
	app.SetLanguage("en")
	app.inspectorMode = "summarize"
	saveShot("postdive-main-en.png", app.RenderMain, 1380, 860)

	app.SetLanguage("de")
	app.applyThemeChoice("Diver Light (diver-light)")
	app.navID = "calendar"
	app.calViewMode = 1
	app.calCursor = now
	app.calMiniCursor = time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, time.Local)
	saveShot("app-calendar-light.png", app.RenderMain, 1380, 860)
}

func TestRenderAppIconPNG(t *testing.T) {
	b := RenderAppIconPNG(256)
	if len(b) < 256 {
		t.Fatalf("expected valid app icon PNG, got %d bytes", len(b))
	}
	if out := os.Getenv("OMATHUNDER_WRITE_ICON"); out != "" {
		hi := RenderAppIconPNG(1024)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, hi, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// cfgBox is a goroutine-safe config store for tests (the bridge server and app background
// goroutines load it while the UI thread saves it).
type cfgBox struct {
	mu  sync.Mutex
	cfg config.Config
}

func newCfgBox(cfg config.Config) *cfgBox { return &cfgBox{cfg: cfg} }

func (b *cfgBox) load() (config.Config, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg, nil
}

func (b *cfgBox) save(next config.Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cfg = next
	return nil
}

func TestPasswordInputToggleAndPasteSanitize(t *testing.T) {
	if got := stripPasswordLineBreaks("  s€crät 123\r\n"); got != "  s€crät 123" {
		t.Fatalf("stripPasswordLineBreaks = %q, want %q", got, "  s€crät 123")
	}

	app := &App{cfg: config.Default()}
	app.storeLang("en")
	var pw, pending string
	show := false

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Padding(8).Children(func() {
			app.renderPasswordInput(c, "test-pw", &pw, &show, &pending, "Password", true)
		})
	}, 420, 120)

	// Paste with trailing CRLF should strip line breaks while keeping characters intact.
	tt.Click("Password")
	tt.SetClipboard("my-pass-word!\r\n")
	tt.Key(ui.Cmd, ui.KeyV)
	if pw != "my-pass-word!" {
		t.Fatalf("after paste pw = %q, want %q", pw, "my-pass-word!")
	}
	if show {
		t.Fatalf("expected password hidden initially")
	}

	// Clicking the eye button toggles reveal on and back off.
	tt.Click("Show password")
	if !show {
		t.Fatalf("expected show=true after clicking Show password")
	}
	tt.Click("Hide password")
	if show {
		t.Fatalf("expected show=false after clicking Hide password")
	}
}

func TestAccountFormSimpleDefaultAndGmailHint(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }})
	app.storeLang("en")
	if app.showCopilot {
		t.Fatalf("expected showCopilot=false by default for calmer 3-pane layout")
	}
	if app.mailSplit != 340 {
		t.Fatalf("expected default mailSplit=340, got %.0f", app.mailSplit)
	}
	app.resetDirectAccountForm()
	if app.direct.accShowAdvanced {
		t.Fatalf("expected advanced settings collapsed by default for preset account")
	}

	presets := config.MailProviderPresets()
	if len(presets) < 2 || presets[1].ID != "gmail" || presets[1].AuthType != "" {
		t.Fatalf("expected Gmail App Password preset right after IONOS, got %+v", presets[:min(3, len(presets))])
	}
	app.applyDirectPreset(presets[1])
	if app.direct.accShowAdvanced {
		t.Fatalf("expected Gmail App Password preset to stay in simple mode")
	}
	app.applyDirectPreset(presets[len(presets)-1]) // Custom
	if !app.direct.accShowAdvanced {
		t.Fatalf("expected Custom preset to expand advanced settings automatically")
	}
}

func TestWorkspaceModeSwitcherSeparatesMailAndCalendar(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }})
	app.storeLang("en")
	app.setNav("recent")

	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	if !tt.HasText("Mailbox") || tt.HasText("Calendar & Events") {
		t.Fatalf("expected Mail-only sidebar without Calendar mixed in; texts=%v", tt.Texts())
	}

	tt.Click("Calendar")
	tt.Frame()
	if app.navID != "calendar" {
		t.Fatalf("expected navID=calendar after clicking Calendar tab, got %q", app.navID)
	}
	if tt.HasText("Mailbox") || !tt.HasText("Calendars") || app.sidebarWidth() == 0 {
		t.Fatalf("expected calendar sidebar (not mail sidebar) in calendar mode, width=%.0f texts=%v", app.sidebarWidth(), tt.Texts())
	}
	app.setNav("contacts")
	if app.sidebarWidth() != 0 {
		t.Fatalf("expected sidebar hidden in contacts mode, got width %.0f", app.sidebarWidth())
	}
	app.setNav("calendar")
	tt.Frame()

	tt.Click("Mail")
	if app.navID != "recent" {
		t.Fatalf("expected Mail tab to restore lastMailNavID=recent, got %q", app.navID)
	}
}

func TestCalendarMonthShowsISOWeeksAndJumpsToWeek(t *testing.T) {
	box := newCfgBox(config.Default())
	app := NewApp(Options{ConfigLoader: box.load, ConfigSaver: box.save})
	updates := make(chan func(), 16)
	app.SetHooks(func(fn func()) {
		select {
		case updates <- fn:
		default:
		}
	}, nil, nil, nil, nil)
	app.storeLang("de")
	app.engineReady = true
	app.navID = "calendar"
	app.calViewMode = 0
	app.calCursor = time.Date(2026, 12, 15, 10, 0, 0, 0, time.Local)

	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	for _, want := range []string{"KW", "KW 49", "KW 50", "KW 51", "KW 52", "KW 53"} {
		if !tt.HasText(want) {
			t.Fatalf("expected %q in December 2026 month view; texts=%v", want, tt.Texts())
		}
	}
	if dir := os.Getenv("POSTDIVE_RENDER_CAL_MONTH"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		if f, err := os.Create(filepath.Join(dir, "cal-month-kw.png")); err == nil {
			_ = png.Encode(f, tt.Image())
			_ = f.Close()
		}
	}

	// Clicking KW 51 jumps straight into Week view for that week.
	tt.Click("KW 51")
	if app.calViewMode != 1 {
		t.Fatalf("expected calViewMode=1 (Week) after clicking KW 51, got %d", app.calViewMode)
	}
	if _, kw := app.calCursor.ISOWeek(); kw != 51 {
		t.Fatalf("expected calCursor in ISO week 51, got week %d (%s)", kw, app.calCursor.Format("2006-01-02"))
	}
}
