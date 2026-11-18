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

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
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
	case "filters.openDialog":
		return json.RawMessage(`{"supported":true,"ok":true}`), nil
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
	discPath := filepath.Join(tmp, "bridge.json")
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
	br, err := bridge.Start(ctx, bridge.Options{
		Port:          0,
		DiscoveryPath: discPath,
		RPC:           rpc,
		Events:        native.NewEventHub(),
		Store:         store,
		ConfigLoader:  box.load,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	bc, _, err := bridgeclient.Discover(discPath)
	if err != nil {
		t.Fatal(err)
	}

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
		DiscoveryPath: discPath,
		ConfigLoader:  box.load,
		ConfigSaver:   box.save,
		Store:         store,
		BridgeClient:  bc,
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
	if !app.bridgeOnline || !app.thunderbirdConnected {
		t.Fatalf("expected bridgeOnline & thunderbirdConnected, got online=%v tb=%v", app.bridgeOnline, app.thunderbirdConnected)
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
	if _, ok := tt.Find("OmaThunder"); !ok {
		t.Fatal("expected OmaThunder header in main view")
	}
	if _, ok := tt.Find("Angebot Q4"); !ok {
		t.Fatal("expected message subject in main view")
	}
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

	// 3b. Open Help Modal and verify Overview, Interactive TOC, TB Filter maintenance note, and AI transparency.
	app.helpOpen = true
	tt.Frame()
	if !tt.HasText("Was OmaThunder macht · Überblick") || !tt.HasText("Inhalt:") || !tt.HasText("100% Lokal") || !tt.HasText("4. Quick-Regeln & Thunderbird-Filter (Pflege in TB)") || !tt.HasText("Extras → Filter") {
		t.Fatalf("expected help modal overview, TOC, and TB rule maintenance sections rendered; texts=%v", tt.Texts())
	}
	app.helpTopic = "rules"
	tt.Frame()
	if tt.HasText("1. 100% Lokal · Keine KI, 0 Tokens") || !tt.HasText("Filter in TB öffnen") {
		t.Fatalf("expected help TOC filtering to show only rules chapter; texts=%v", tt.Texts())
	}
	app.helpTopic = "all"
	assertNoLegacyCopilot(tt.Texts())
	app.helpOpen = false

	// 3c. Verify contact discovery cross-check against Thunderbird address book & enrich flow.
	props := buildContactProposals([]ai.ExtractedContact{
		{
			DisplayName: "Alice Weber", FirstName: "Alice", LastName: "Weber",
			Email: "alice@acme.de", Phone: "+49 30 555010", Company: "ACME GmbH",
		},
		{
			DisplayName: "Bob Meyer", FirstName: "Bob", LastName: "Meyer",
			Email: "bob@partner.io", Mobile: "+49 170 223344", Company: "Partner AG",
		},
	}, []bridgeclient.ContactRecord{
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
	if !strings.Contains(app.TrayTitle(), "1") || !strings.Contains(app.TrayTooltip(), "OmaThunder Live") {
		t.Fatalf("unexpected tray strings: title=%q tip=%q", app.TrayTitle(), app.TrayTooltip())
	}

	// 5. Switch UI language dynamically to English and verify Main, Contacts, Help, and Tray Widget.
	app.inspectorContacts[0].Applied = false
	app.SetLanguage("en")
	if cur, _ := box.load(); app.uiLang() != "en" || cur.Language != "en" {
		t.Fatalf("expected language switched to en, got ui=%q cfg=%q", app.uiLang(), cur.Language)
	}
	tt.Frame()
	if !tt.HasText("AI Assistant") || !tt.HasText("Focus (Unread)") || !tt.HasText("Summarize") || !tt.HasText("Contacts") {
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
	if !tt.HasText("Help · Overview, TB Filters & AI Transparency") || !tt.HasText("What OmaThunder Does · Overview") || !tt.HasText("4. Quick Rules & Thunderbird Filters (Managed in TB)") || !tt.HasText("Tools → Message Filters") {
		t.Fatalf("expected English help modal with overview, TOC, and TB rule maintenance; texts=%v", tt.Texts())
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

	// 5b. Switch back to DE, multi-select emails, preview Quick-Rule, save to Thunderbird, and trash marked emails immediately.
	app.SetLanguage("de")
	app.ToggleMailChecked(0, false)
	app.ToggleMailChecked(1, false)
	tt.Frame()
	if !tt.HasText("2 markiert") || !tt.HasText("Quick-Regel") {
		t.Fatalf("expected bulk action bar for 2 checked messages; texts=%v", tt.Texts())
	}
	app.OpenQuickRuleWizard()
	tt.Frame()
	if !tt.HasText("Quick-Regel-Assistent · Thunderbird-Filter") || !tt.HasText("Live-Preview vor dem Speichern") || !tt.HasText("Markiert: 2/2") || !tt.HasText("Extras → Filter") {
		t.Fatalf("expected quick-rule modal with live preview and TB maintenance note; texts=%v", tt.Texts())
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

	// 6. Simulate incoming mail SSE (real bridge wrapper payload) and verify alert creation + click navigation.
	app.handleBridgeEvent(ctx, nil, "mail.new", `{"event":"mail.new","data":{"accountId":"acc1","folderId":"f1","folder":"INBOX","count":1,"messages":[{"id":"m99","messageId":"m99@acme.de","subject":"Dringend","from":"Chef <chef@acme.de>","accountId":"acc1","folderId":"f1","unread":true}]},"timestamp":"2026-11-18T08:00:00Z"}`)
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

// TestRenderScreens renders high-DPI PNG screenshots into OMATHUNDER_RENDER:
//
//	OMATHUNDER_RENDER=/tmp/shots go test ./internal/nativeui -run TestRenderScreens
func TestRenderScreens(t *testing.T) {
	dir := os.Getenv("OMATHUNDER_RENDER")
	if dir == "" {
		t.Skip("OMATHUNDER_RENDER not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	store := triage.NewStoreAt(tmp)
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	cfg.Theme.Mode = "preset"
	cfg.Theme.Preset = "tokyo-night"

	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
		Store:        store,
	})

	now := time.Now()
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	app.bridgeOnline = true
	app.thunderbirdConnected = true
	app.readOnly = false
	app.providerLabel = "anthropic (api)"
	app.agentBackend = "anthropic"
	app.agentModel = "claude-sonnet-4-5"
	app.modelChoices = []string{"claude-sonnet-4-5", "gemini-4-hydrogen", "gpt-4o-mini"}
	app.accounts = []bridgeclient.Account{{ID: "acc1", Name: "Work (IMAP)"}, {ID: "acc2", Name: "Privat"}}
	app.lastSync = now.Add(-2 * time.Minute)
	app.accountLabels = []string{"Alle Konten", "Work (IMAP)", "Privat"}
	app.accountIDByLabel = map[string]string{"Alle Konten": "", "Work (IMAP)": "acc1", "Privat": "acc2"}
	app.folders = []bridgeclient.Folder{
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
	app.messages = []bridgeclient.MessageHeader{
		{
			ID: "m1", AccountID: "acc1", Folder: "INBOX",
			From: "Alice Weber <alice@acme.de>", To: []string{"frank@avono.de"}, Subject: "Q4 Rollout & Budget-Freigabe bis Freitag",
			Date: today + "T09:15:00+01:00", Unread: true, Flagged: true,
		},
		{
			ID: "m2", AccountID: "acc1", Folder: "Spaces/ACME",
			From: "Markus Klein <m.klein@partner.io>", Subject: "Jira Cloud Migration: Terminvorschlag Workshop",
			Date: today + "T08:40:00+01:00", Unread: true,
		},
		{
			ID: "m3", AccountID: "acc1", Folder: "Rechnungen",
			From: "Hetzner Billing <billing@hetzner.com>", Subject: "Rechnung #2026-11-4821 f\u00e4llig",
			Date: today + "T07:20:00+01:00", Unread: true,
		},
		{
			ID: "m4", AccountID: "acc1", Folder: "INBOX",
			From: "Sarah M\u00fcller <sarah@avono.de>", Subject: "Review Notes: OmaThunder Native UI",
			Date: now.AddDate(0, 0, -1).Format("2006-01-02") + "T16:45:00+01:00", Unread: false,
		},
	}
	app.totalMessages = len(app.messages)
	app.focusUnread = 3
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
	app.selectedMsg = &bridgeclient.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Hallo Frank,\n\ndie finale Kalkulation f\u00fcr das Q4-Rollout liegt vor:\n- Lizenzen & Infrastruktur: 14.200 EUR\n- Migration & Workshop (2 Tage): 3.600 EUR\n\nK\u00f6nnen wir heute um 14:00 Uhr kurz abstimmen, ob das Budget so freigegeben werden kann?\n\nViele Gr\u00fc\u00dfe\nAlice",
		Attachments: []bridgeclient.Attachment{
			{Name: "Q4-Kalkulation.pdf", Size: 248000, ContentType: "application/pdf"},
		},
	}
	app.inspectorMode = "summarize"
	app.inspectorResult = "**Kernaussage:** Alice bittet um Budget-Freigabe f\u00fcr das Q4-Rollout (**17.800 EUR** gesamt) und schl\u00e4gt ein kurzes Abstimmungsgespr\u00e4ch heute um **14:00 Uhr** vor.\n\n- **Frist:** Freitag\n- **N\u00e4chster Schritt:** Termin-Slot 14:00 pr\u00fcfen & Freigabe-Antwort entwerfen."
	app.inspectorDraft = "Hallo Alice,\n\nvielen Dank f\u00fcr die \u00dcbersicht. 14:00 Uhr passt mir heute gut \u2014 lass uns das Q4-Budget dort final freigeben.\n\nViele Gr\u00fc\u00dfe\nFrank"

	app.events = []bridgeclient.CalendarEvent{
		{ID: "e1", CalendarID: "c1", Calendar: "Arbeit", Title: "Daily Engineering Sync", Start: today + "T10:00:00+01:00", End: today + "T10:30:00+01:00", Location: "Meet"},
		{ID: "e2", CalendarID: "c1", Calendar: "Arbeit", Title: "Q4 Rollout Budget-Abstimmung", Start: today + "T14:00:00+01:00", End: today + "T14:30:00+01:00", Location: "Zoom"},
		{ID: "e3", CalendarID: "c1", Calendar: "Privat", Title: "Zahnarzt Termin", Start: today + "T17:15:00+01:00", End: today + "T18:00:00+01:00", Location: "Innenstadt"},
		{ID: "e4", CalendarID: "c1", Calendar: "Arbeit", Title: "Atlassian Partner Review", Start: tomorrow + "T11:00:00+01:00", End: tomorrow + "T12:00:00+01:00", Location: "Teams"},
	}
	app.recentAlerts = []AlertItem{
		{ID: "omathunder-mail-m1", Kind: "mail", Title: "Alice Weber", Subtitle: "Neue E-Mail", Body: "Q4 Rollout & Budget-Freigabe"},
		{ID: "omathunder-cal-e2", Kind: "calendar", Title: "Budget-Abstimmung", Subtitle: "Heute 14:00", Body: "Arbeit"},
	}
	app.chatEntries = []ChatEntry{
		{ID: "u-1", Role: "user", Content: "Gib mir mein Tages-Briefing f\u00fcr heute."},
		{
			ID:   "a-1",
			Role: "assistant",
			Tools: []ToolTrace{
				{ID: "t1", Name: "search_messages", Summary: "4 Mails gefunden", Done: true},
				{ID: "t2", Name: "list_calendar_events", Summary: "4 Kalendereintr\u00e4ge geladen", Done: true},
			},
			Content: "### Tages-Briefing\n\n**Priorit\u00e4t 1 \u2014 Mails:**\n- **Alice Weber** wartet auf Freigabe f\u00fcr das **Q4-Rollout (17.800 EUR)** bis Freitag.\n- **Markus Klein** hat Workshop-Termine vorgeschlagen.\n\n**Heutiger Kalender:**\n| Zeit | Termin |\n| --- | --- |\n| 10:00 | Daily Engineering Sync |\n| 14:00 | Q4 Rollout Budget-Abstimmung |\n| 17:15 | Zahnarzt Termin |",
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

	saveShot("omathunder-main-dark.png", app.RenderMain, 1380, 860)
	saveShot("omathunder-tray-widget.png", app.RenderStatusWidget, 400, 510)

	// Screenshot 2: Footer Contact Discovery card in Inspector (1 existing to enrich + 1 new to import)
	app.inspectorMode = "extract_contacts"
	app.inspectorResult = "2 Kontakte im E-Mail-Footer erkannt und lokal mit deinem Thunderbird-Adressbuch abgeglichen."
	app.inspectorDraft = ""
	app.contactsBookLabels = []string{"Persönliches Adressbuch", "Geschäftskontakte"}
	app.contactsBookByLabel = map[string]string{"Persönliches Adressbuch": "ab1", "Geschäftskontakte": "ab2"}
	app.selectedContactBook = "Persönliches Adressbuch"
	app.inspectorContacts = []ContactProposal{
		{
			Extracted: ai.ExtractedContact{
				DisplayName: "Alice Weber", FirstName: "Alice", LastName: "Weber",
				Email: "alice@acme.de", Phone: "+49 30 555010", Mobile: "+49 170 9876543",
				Company: "ACME GmbH", JobTitle: "Head of Engineering", Website: "https://acme.de",
				Address: "Musterstr. 12, 10115 Berlin",
			},
			Existing:         &bridgeclient.ContactRecord{ID: "ct1", DisplayName: "Alice Weber", Email: "alice@acme.de"},
			Status:           "enrich",
			MissingFieldKeys: []string{"phone", "mobile", "company", "job_title", "website", "address"},
			MissingFields:    []string{"Telefon", "Mobil", "Firma", "Position", "Website", "Adresse"},
		},
		{
			Extracted: ai.ExtractedContact{
				DisplayName: "Markus Klein", FirstName: "Markus", LastName: "Klein",
				Email: "m.klein@partner.io", Phone: "+49 89 441200", Company: "Partner.io AG",
				JobTitle: "Solution Architect",
			},
			Status: "new",
		},
	}
	saveShot("omathunder-contacts-dark.png", app.RenderMain, 1380, 860)

	// Screenshot 3: Help Modal (Overview + Interactive TOC + TB Filter maintenance note)
	app.inspectorContacts = nil
	app.helpTopic = "all"
	app.helpOpen = true
	saveShot("omathunder-help-dark.png", app.RenderMain, 1380, 860)
	app.helpTopic = "shortcuts"
	saveShot("omathunder-help-shortcuts-dark.png", app.RenderMain, 1380, 860)
	app.helpTopic = "all"
	app.helpOpen = false

	// Screenshot 3b: Quick-Rule Wizard with Multi-Select & Live Preview
	prevMsgs := append([]bridgeclient.MessageHeader(nil), app.messages...)
	app.messages = append([]bridgeclient.MessageHeader{
		{
			ID: "m-nl-1", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.io>", Subject: "[Daily Digest] Nightly Build OK #412",
			Date: today + "T06:05:00+01:00", Unread: true,
		},
		{
			ID: "m-nl-2", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.io>", Subject: "[Daily Digest] Staging Sync OK #411",
			Date: today + "T05:50:00+01:00", Unread: true,
		},
		{
			ID: "m-nl-3", AccountID: "acc1", Folder: "INBOX",
			From: "Monitoring Bot <alerts@cloud-status.io>", Subject: "[Daily Digest] Backup Report OK #410",
			Date: now.AddDate(0, 0, -1).Format("2006-01-02") + "T06:00:00+01:00", Unread: false,
		},
	}, app.messages...)
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(1, true, false)
	app.OpenQuickRuleWizard()
	saveShot("omathunder-quick-rule-dark.png", app.RenderMain, 1380, 860)
	app.quickRuleOpen = false
	app.ClearMailSelection()
	app.messages = prevMsgs

	// Screenshot 3c: "Move to…" picker (v)
	app.OpenMovePicker()
	saveShot("omathunder-move-dark.png", app.RenderMain, 1380, 860)
	app.moveOpen = false

	// Screenshot 3d: an HTML mail, formatted (links by title, remote images blocked)
	prevDetail, prevMode, prevResult, prevDraft := app.selectedMsg, app.inspectorMode, app.inspectorResult, app.inspectorDraft
	app.inspectorMode, app.inspectorResult, app.inspectorDraft = "", "", ""
	app.selectedMsg = &bridgeclient.MessageDetail{
		MessageHeader: bridgeclient.MessageHeader{ID: "m1", AccountID: "acc1", Folder: "INBOX", From: "Confluence <confluence@example.atlassian.net>", To: []string{"Frank <frank@example.com>"}, Subject: "Gryphon SDK Release (2.0.0) plus more pages your team is working on", Date: today + "T10:56:00+01:00"},
		BodyText:      "Stay connected\n\nGryphon SDK Release (2.0.0)\n[https://example.atlassian.net/wiki/spaces/GS/pages/1]",
		BodyHtml: `<div style="display:none">Stay connected to people and their work &zwnj;&nbsp;&zwnj;</div>
<table><tr><td><img src="https://cdn.example.net/logo.png" width="120" alt="Confluence"></td></tr><tr><td>
<h1>Stay connected to people and their work</h1><p>Your weekly content recommendations, ready to explore.</p>
<table><tr><td><img src="https://cdn.example.net/page.png" width="16" alt="Page"></td><td><a href="https://example.atlassian.net/wiki/spaces/GS/pages/2326593582?atl_f=x">Gryphon SDK Release Data Center (2.0.0) 2026/10/26</a></td></tr>
<tr><td>Owned by</td><td><b>Mohammad Shariq</b> · 3 contributors · updated 2 days ago</td></tr></table>
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
	saveShot("omathunder-mail-html-dark.png", app.RenderMain, 1380, 860)
	app.readerPlain = true
	saveShot("omathunder-mail-text-dark.png", app.RenderMain, 1380, 860)
	app.readerPlain = false
	app.selectedMsg, app.inspectorMode, app.inspectorResult, app.inspectorDraft = prevDetail, prevMode, prevResult, prevDraft
	app.readerBody = readerBody{}

	// Screenshot 4: English UI switch verification shot
	app.SetLanguage("en")
	app.inspectorMode = "summarize"
	saveShot("omathunder-main-en.png", app.RenderMain, 1380, 860)

	app.SetLanguage("de")
	app.applyThemeChoice("Catppuccin Latte (catppuccin-latte)")
	app.navID = "calendar"
	saveShot("omathunder-calendar-light.png", app.RenderMain, 1380, 860)
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
