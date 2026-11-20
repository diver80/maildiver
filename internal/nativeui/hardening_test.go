package nativeui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/copilot"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/jobs"
	"github.com/diver80/maildiver/internal/triage"
)

// --- shared test harness -------------------------------------------------------------

// countingRPC is a goroutine-safe Thunderbird stub with a configurable message list.
type countingRPC struct {
	mu            sync.Mutex
	writes        []string
	params        []map[string]any
	msgsJSON      string
	filterErr     error
	createOnError bool // with filterErr: the filter is created although an error is returned
	filters       []string
	last          map[string]map[string]any // params of the latest call per method
	getJSON       string                    // messages.get answer, when set
	answers       map[string]string         // fixed answers per method
}

// lastParams returns the params of the latest call of method (nil: not called).
func (r *countingRPC) lastParams(method string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last[method]
}

func (*countingRPC) Connected() bool { return true }
func (r *countingRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	today := time.Now().Format("2006-01-02")
	rawParams, _ := json.Marshal(params)
	var lastP map[string]any
	_ = json.Unmarshal(rawParams, &lastP)
	r.mu.Lock()
	msgs := r.msgsJSON
	getJSON := r.getJSON
	answer, hasAnswer := r.answers[method]
	if r.last == nil {
		r.last = map[string]map[string]any{}
	}
	r.last[method] = lastP
	r.mu.Unlock()
	if hasAnswer {
		return json.RawMessage(answer), nil
	}
	switch method {
	case "accounts.list":
		return json.RawMessage(`{"accounts":[{"id":"acc1","name":"Work","type":"imap"},{"id":"acc2","name":"Privat","type":"imap"}]}`), nil
	case "folders.list":
		return json.RawMessage(`{"folders":[{"id":"f1","accountId":"acc1","name":"INBOX","path":"/INBOX","type":"inbox"},{"id":"f2","accountId":"acc2","name":"INBOX","path":"/INBOX","type":"inbox"}]}`), nil
	case "messages.list":
		if msgs == "" {
			msgs = `[{"id":"m1","messageId":"m1@acme.de","accountId":"acc1","folderId":"f1","folder":"INBOX","inInbox":true,"subject":"Angebot Q4","from":"Alice <alice@acme.de>","date":"` + today + `T08:15:00Z","unread":true}]`
		}
		return json.RawMessage(`{"messages":` + msgs + `,"total":1}`), nil
	case "messages.get":
		if getJSON != "" {
			return json.RawMessage(getJSON), nil
		}
		return json.RawMessage(`{"id":"m1","messageId":"m1@acme.de","accountId":"acc1","folder":"INBOX","subject":"Angebot Q4","from":"Alice <alice@acme.de>","bodyText":"Hallo"}`), nil
	case "filters.list":
		r.mu.Lock()
		names := append([]string(nil), r.filters...)
		r.mu.Unlock()
		var fl []map[string]any
		for _, n := range names {
			fl = append(fl, map[string]any{"accountId": "acc1", "name": n, "enabled": true})
		}
		b, _ := json.Marshal(map[string]any{"supported": true, "filters": fl})
		return b, nil
	case "calendar.list":
		return json.RawMessage(`{"supported":true,"calendars":[{"id":"c1","name":"Arbeit","type":"local","readOnly":false}]}`), nil
	case "calendar.events":
		return json.RawMessage(`{"supported":true,"events":[{"id":"e1","calendarId":"c1","calendarName":"Arbeit","title":"Weekly","start":"` + today + `T14:00:00Z","end":"` + today + `T15:00:00Z","recurrenceId":"20261118T140000Z","isRecurring":true}]}`), nil
	case "messages.actions", "filters.upsert", "compose.draft", "compose.send", "calendar.create", "calendar.delete":
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		r.mu.Lock()
		r.writes = append(r.writes, method)
		r.params = append(r.params, p)
		ferr := r.filterErr
		r.mu.Unlock()
		if method == "filters.upsert" && ferr != nil {
			if r.createOnError {
				name, _ := p["name"].(string)
				r.mu.Lock()
				r.filters = append(r.filters, name)
				r.mu.Unlock()
			}
			return nil, ferr
		}
		return json.RawMessage(`{"ok":true}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

// CallHost answers the host jobs' calls (triage scans, archive runs).
func (r *countingRPC) CallHost(_ context.Context, method string, params any) (json.RawMessage, error) {
	rawParams, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(rawParams, &p)
	r.mu.Lock()
	if r.last == nil {
		r.last = map[string]map[string]any{}
	}
	r.last[method] = p
	r.mu.Unlock()
	switch method {
	case "host.triage.collect":
		return json.RawMessage(`{"messages":[{"id":"m1","threadId":"m1@acme.de","accountId":"acc1","subject":"Angebot Q4","from":"Alice <alice@acme.de>","unread":true},{"id":"m9","threadId":"m9@acme.de","accountId":"acc1","subject":"Vertrag","from":"Bob <bob@acme.de>","encrypted":true,"unread":true}],"scannedFolders":1}`), nil
	case "host.archive.candidates":
		return json.RawMessage(`{"candidates":[{"messageId":"old1","headerMessageId":"old1@x","accountId":"acc1","accountName":"Work","folderId":"f1","folderPath":"INBOX","subject":"Alte Rechnung","from":"a@b.de","date":"2024-02-03T10:00:00Z","size":2048}],"totalMatched":1,"scannedFolders":1,"hasLocalAccount":true}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func (r *countingRPC) snapshot() ([]string, []map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...), append([]map[string]any(nil), r.params...)
}

type testStack struct {
	t     *testing.T
	store *triage.Store
	box   *cfgBox
	rpc   *countingRPC
	hub   *events.Hub
	bc    *backend.Client
	ctx   context.Context
}

func newTestStack(t *testing.T) *testStack {
	t.Helper()
	tmp := t.TempDir()
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "gpt-4o-mini", "object": "model"}}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		msg := map[string]any{"role": "assistant", "content": "## Briefing\n- Angebot Q4"}
		finish := "stop"
		if !strings.Contains(string(body), `"role":"tool"`) {
			finish = "tool_calls"
			msg = map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
				"id": "tc-1", "type": "function", "function": map[string]any{"name": "search_messages", "arguments": `{"unread":true}`},
			}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c", "object": "chat.completion", "model": "gpt-4o-mini",
			"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": msg}}})
	}))
	t.Cleanup(aiSrv.Close)
	t.Setenv("OPENAI_API_KEY", "test-key")

	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	cfg.Theme.Mode = "preset"
	cfg.Theme.Preset = "tokyo-night"
	cfg.AI.Agent = config.ProviderConfig{Backend: "openai", Model: "gpt-4o-mini", Endpoint: aiSrv.URL + "/v1"}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := &testStack{
		t:     t,
		store: triage.NewStoreAt(tmp),
		box:   newCfgBox(cfg),
		rpc:   &countingRPC{},
		hub:   events.NewHub(),
		ctx:   ctx,
	}
	st.bc = backend.New(backend.Options{
		Engine:     st.rpc,
		Events:     st.hub,
		Store:      st.store,
		Jobs:       jobs.New(ctx, st.rpc, st.hub, st.store, st.box.load),
		LoadConfig: st.box.load,
		Version:    "test",
	})
	return st
}

// newApp returns an app wired to the stack's backend.
func (s *testStack) newApp() *App {
	return NewApp(Options{ConfigLoader: s.box.load, ConfigSaver: s.box.save, Store: s.store, Backend: s.bc})
}

// uiLoop simulates mygo's single UI thread: closures posted via emitUpdate run here only.
type uiLoop struct {
	queue chan func()
}

func attachUILoop(a *App, onNotify func(AlertItem)) *uiLoop {
	l := &uiLoop{queue: make(chan func(), 4096)}
	a.SetHooks(func(fn func()) { l.queue <- fn }, func() { _ = a.TrayTitle() }, nil, nil, onNotify)
	return l
}

func (l *uiLoop) drain() {
	for {
		select {
		case fn := <-l.queue:
			if fn != nil {
				fn()
			}
		default:
			return
		}
	}
}

func (l *uiLoop) waitUntil(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		l.drain()
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.drain()
	return cond()
}

// --- race test --------------------------------------------------------------------------

// TestRenderWhileBackgroundWorkAndSSERun renders frames on the (simulated) UI thread while the
// real SSE loop, coalesced refreshes, message loads, copilot runs, batch actions and client
// invalidation run concurrently. Run with -race.
func TestRenderWhileBackgroundWorkAndSSERun(t *testing.T) {
	st := newTestStack(t)
	app := st.newApp()
	var alerts atomic.Int32
	loop := attachUILoop(app, func(AlertItem) { alerts.Add(1) })
	ctx, cancel := context.WithCancel(st.ctx)
	defer cancel()

	main := ui.NewTester(app.RenderMain, 1200, 800)
	fly := ui.NewTester(app.RenderStatusWidget, 400, 510)
	app.StartEventLoop(ctx)
	app.RefreshAll(ctx)
	if !loop.waitUntil(t, 5*time.Second, func() bool { return app.engineReady && len(app.messages) > 0 }) {
		t.Fatalf("app never came online (err=%q)", app.lastError)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(3)
	go func() { // SSE producer: real wrapped payloads through the bridge
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
			}
			st.hub.Publish("mail.new", map[string]any{"accountId": "acc1", "folderId": "f1", "count": 1,
				"messages": []map[string]any{{"id": fmt.Sprintf("n%d", i), "subject": "Neu", "from": "x@y.de", "accountId": "acc1"}}})
			st.hub.Publish("mail.updated", map[string]any{"ids": []string{"m1"}, "accountIds": []string{"acc1"}})
			if i%5 == 0 {
				st.hub.Publish("calendar.changed", map[string]any{"calendarId": "c1", "ids": []string{"e1"}})
			}
		}
	}()
	go func() { // any-goroutine entry points
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			app.RefreshAll(ctx)
			app.LoadMessages(ctx)
		}
	}()
	go func() { // client swaps away underneath running work (previously a nil-pointer panic)
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(7 * time.Millisecond):
			}
			app.LoadMessages(ctx) // more work racing the briefing
		}
	}()

	app.OpenDayBriefing(ctx)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		loop.drain()
		main.Frame()
		fly.Frame()
		if len(app.messages) > 0 {
			app.ToggleMailChecked(0, i%2 == 0)
			if i%7 == 0 {
				readTrue := true
				app.PerformBatchAction(ctx, app.CheckedMessageIDs(), backend.MessageActionsRequest{Read: &readTrue}, "ok")
			}
			if i%11 == 0 {
				app.SelectMessage(ctx, app.messages[0].ID)
			}
			if i%13 == 0 {
				app.OpenQuickRuleWizard()
				_ = app.QuickRulePreview()
				app.quickRuleOpen = false
			}
		}
		if i%17 == 0 {
			app.SendCopilotPrompt(ctx, "Status?")
		}
		// UI-thread writes to fields background code used to read directly (every frame, so an
		// unsynchronized background read is very likely to overlap a write).
		app.setNav([]string{"focus", "recent", "action"}[i%3])
		app.searchQuery = fmt.Sprintf("q%d", i%2)
		app.selectedAccount = []string{"", "acc1"}[i%2]
		app.calDays = []int{7, 14}[i%2]
		app.agentModel = []string{"gpt-4o-mini", "gpt-4o"}[i%2]
		app.notifyEnabled = i%10 != 5
		if i%23 == 0 {
			app.SetLanguage([]string{"de", "en"}[i%2])
		}
		time.Sleep(time.Millisecond)
		app.setNav(app.navID)
		app.searchQuery += ""
	}
	close(stop)
	wg.Wait()
	loop.waitUntil(t, 3*time.Second, func() bool { return !app.chatStreaming && !app.refreshing })
	main.Frame()

	if alerts.Load() == 0 {
		t.Fatal("expected desktop alerts from wrapped mail.new SSE payloads")
	}
	if len(app.chatEntries) == 0 {
		t.Fatal("expected the briefing to run")
	}
	cancel()
	loop.waitUntil(t, 200*time.Millisecond, func() bool { return false })
}

// --- connection manager ------------------------------------------------------------------

func TestStaleMessageLoadIsDropped(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }, ConfigSaver: func(config.Config) error { return nil }, Store: triage.NewStoreAt(t.TempDir())})
	older := app.newMessageLoad()
	app.navID = "recent"
	newer := app.newMessageLoad()
	fresh := []backend.MessageHeader{{ID: "new1"}, {ID: "new2"}}
	stale := []backend.MessageHeader{{ID: "old1"}}
	if !app.applyMessageLoad(newer, fresh, 2) {
		t.Fatal("newest load must apply")
	}
	if app.applyMessageLoad(older, stale, 1) {
		t.Fatal("stale load must be dropped")
	}
	if len(app.messages) != 2 || app.messages[0].ID != "new1" || app.totalMessages != 2 {
		t.Fatalf("stale response overwrote the list: %+v", app.messages)
	}
}

func TestShiftAnchorResetOnListReplacement(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }, ConfigSaver: func(config.Config) error { return nil }, Store: triage.NewStoreAt(t.TempDir())})
	list := func(prefix string, n int) []backend.MessageHeader {
		var out []backend.MessageHeader
		for i := 0; i < n; i++ {
			out = append(out, backend.MessageHeader{ID: fmt.Sprintf("%s%d", prefix, i)})
		}
		return out
	}
	app.setMessages(list("a", 5))
	app.ToggleMailChecked(1, false)
	if app.lastCheckedIndex != 1 {
		t.Fatalf("anchor not set: %d", app.lastCheckedIndex)
	}
	// Folder/view change replaces the list.
	app.setMessages(append(list("a", 2), list("b", 6)...))
	if app.lastCheckedIndex != -1 {
		t.Fatal("anchor must be reset on list replacement")
	}
	if !app.IsMailChecked("a1") {
		t.Fatal("still listed checked mail must stay checked")
	}
	// Shift+click without a valid anchor selects only the clicked row.
	app.ToggleMailChecked(6, true)
	if got := app.CheckedMessageIDs(); len(got) != 2 {
		t.Fatalf("shift-click after reload range-selected unseen mails: %v", got)
	}
	app.setMessages(list("c", 3))
	if len(app.checkedIDs) != 0 {
		t.Fatalf("checked IDs no longer listed must be dropped: %v", app.checkedIDs)
	}
	app.setMessages(list("c", 3))
	app.ToggleMailChecked(0, false)
	app.setNav("recent")
	if app.lastCheckedIndex != -1 {
		t.Fatal("anchor must be reset on view change")
	}
}

// --- quick rule ------------------------------------------------------------------------

func TestQuickRuleNeverAppliesWithoutHitsAndScopesAccount(t *testing.T) {
	st := newTestStack(t)
	app := st.newApp()
	loop := attachUILoop(app, nil)
	app.engineReady = true
	app.readOnly = false
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work"}, {ID: "acc2", Name: "Privat"}}
	app.setMessages([]backend.MessageHeader{
		{ID: "w1", MessageID: "w1@x", AccountID: "acc1", Folder: "INBOX", InInbox: true, From: "News <news@shop.de>", Subject: "Angebot der Woche"},
		{ID: "w2", MessageID: "w2@x", AccountID: "acc1", Folder: "INBOX", InInbox: true, From: "News <news@shop.de>", Subject: "Angebot Spezial"},
		{ID: "w3", MessageID: "w3@x", AccountID: "acc1", Folder: "Archiv", From: "News <news@shop.de>", Subject: "Angebot alt"},
		{ID: "p1", MessageID: "p1@x", AccountID: "acc2", Folder: "INBOX", InInbox: true, From: "News <news@shop.de>", Subject: "Angebot privat"},
		{ID: "p2", MessageID: "p2@x", AccountID: "acc2", Folder: "INBOX", InInbox: true, From: "Shop <news@shop.de>", Subject: "Angebot Familie"},
	})
	app.SetMailChecked(0, true, false)
	app.SetMailChecked(3, true, false)
	app.SetMailChecked(4, true, false)
	app.OpenQuickRuleWizard()

	// acc2 has more marked mails → default; acc1's mail is excluded.
	if app.quickRuleAccount != "acc2" || len(app.quickRuleMarked) != 2 || app.quickRuleExcludedCount() != 1 {
		t.Fatalf("expected acc2 scope with 2 marked, 1 excluded; got acc=%s marked=%d", app.quickRuleAccount, len(app.quickRuleMarked))
	}
	prev := app.QuickRulePreview()
	if prev.ExtraTotal != 0 {
		t.Fatalf("extra matches must only count acc2 inbox mails, got %+v", prev.ExtraMatches)
	}
	app.applyQuickRuleAccountChoice("Work")
	prev = app.QuickRulePreview()
	if app.quickRuleAccount != "acc1" || prev.MarkedTotal != 1 || prev.ExtraTotal != 1 || prev.ExtraMatches[0].ID != "w2" {
		t.Fatalf("acc1 scope: want 1 marked + only inbox extra w2, got %+v", prev)
	}

	// A rule that matches none of the marked mails must not apply to anything.
	app.quickRuleFromText = "nobody@else.example"
	app.quickRuleSubjectText = ""
	app.SaveAndApplyQuickRule(context.Background())
	loop.drain()
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 {
		t.Fatalf("zero-hit rule must not write anything, got %v", writes)
	}
	if !app.quickRuleOpen || !strings.Contains(app.quickRuleError, "keine der markierten") {
		t.Fatalf("expected blocking message, got %q", app.quickRuleError)
	}

	// Specific rule (address + subject, mark read): saved with mode=create, only the hit is
	// touched, with expectedMessageId.
	app.quickRuleFromText = "news@shop.de"
	app.quickRuleSubjectText = "Angebot"
	app.quickRuleMatchAll = true
	app.applyQuickRuleActionChoice(quickRuleActionLabelLang("mark_read", "de"))
	app.SaveAndApplyQuickRule(context.Background())
	if !loop.waitUntil(t, 3*time.Second, func() bool { return !app.quickRuleBusy && !app.quickRuleOpen }) {
		t.Fatalf("save did not finish: %q", app.quickRuleError)
	}
	writes, params := st.rpc.snapshot()
	if len(writes) != 2 || writes[0] != "filters.upsert" || writes[1] != "messages.actions" {
		t.Fatalf("unexpected writes %v", writes)
	}
	if params[0]["mode"] != "create" || params[0]["accountId"] != "acc1" {
		t.Fatalf("filter must be created in acc1 with mode=create: %+v", params[0])
	}
	if params[1]["id"] != "w1" || params[1]["expectedMessageId"] != "w1@x" {
		t.Fatalf("action must target w1 with expectedMessageId: %+v", params[1])
	}
}

func TestQuickRuleFilterRetryNeverDuplicates(t *testing.T) {
	if got := findCreatedFilter("Auto-Filter: news@shop.de", map[string]bool{"Auto-Filter: news@shop.de": true},
		map[string]bool{"Auto-Filter: news@shop.de": true, "Auto-Filter: news@shop.de (2)": true}); got != "Auto-Filter: news@shop.de (2)" {
		t.Fatalf("expected uniquified created filter, got %q", got)
	}
	if got := findCreatedFilter("X", map[string]bool{}, map[string]bool{"Y": true, "X (a)": true}); got != "" {
		t.Fatalf("unexpected match %q", got)
	}

	// The engine reports outcome_unknown, but the filter was created (the answer got lost):
	// the client must find it instead of creating a duplicate.
	st := newTestStack(t)
	bc := st.bc
	st.rpc.mu.Lock()
	st.rpc.filterErr = errors.New("outcome_unknown: timed out")
	st.rpc.createOnError = true
	st.rpc.mu.Unlock()
	name, err := saveQuickRuleFilter(context.Background(), bc, backend.FilterUpsertRequest{Mode: "create", AccountID: "acc1", Name: "Regel"})
	if err != nil || name != "Regel" {
		t.Fatalf("expected verified create, got name=%q err=%v", name, err)
	}
	if writes, _ := st.rpc.snapshot(); len(writes) != 1 {
		t.Fatalf("expected exactly one create call, got %v", writes)
	}

	// Outcome unknown and NOT created: exactly one retry.
	st.rpc.mu.Lock()
	st.rpc.createOnError = false
	st.rpc.writes = nil
	st.rpc.mu.Unlock()
	_, err = saveQuickRuleFilter(context.Background(), bc, backend.FilterUpsertRequest{Mode: "create", AccountID: "acc1", Name: "Andere"})
	if err == nil {
		t.Fatal("expected the persisting error to be reported")
	}
	if writes, _ := st.rpc.snapshot(); len(writes) != 2 {
		t.Fatalf("expected one retry after verifying absence, got %v", writes)
	}
}

// --- copilot confirmation ---------------------------------------------------------------

func TestCopilotProposalNeedsExplicitExecute(t *testing.T) {
	st := newTestStack(t)
	app := st.newApp()
	loop := attachUILoop(app, nil)
	app.readOnly = false
	app.chatEntries = []ChatEntry{{ID: "a-1", Role: "assistant", Streaming: true}}
	prop := &copilot.PendingAction{ID: "p-1", Kind: copilot.ActionDraft, IdempotencyKey: "k-1", Draft: &copilot.DraftProposal{
		To: []string{"alice@acme.de"}, CC: []string{"boss@acme.de"}, Subject: "Re: Angebot", Body: "Passt.",
	}}
	app.applyCopilotEvent("a-1", "de", copilot.Event{Kind: copilot.EventProposal, Proposal: prop})
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	for _, want := range []string{"Vorschlag: E-Mail-Entwurf speichern", "alice@acme.de", "boss@acme.de", "Re: Angebot", "Ausführen", "Verwerfen"} {
		if !tt.HasText(want) {
			t.Fatalf("confirmation card misses %q; texts=%v", want, tt.Texts())
		}
	}
	time.Sleep(50 * time.Millisecond)
	loop.drain()
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 {
		t.Fatalf("proposal executed without a click: %v", writes)
	}

	app.DiscardCopilotAction("p-1")
	app.ExecuteCopilotAction(context.Background(), "p-1")
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 || app.pendingActions["p-1"].Status != actionDiscarded {
		t.Fatalf("discarded proposal must never execute: %v", writes)
	}

	app.pendingActions["p-1"].Status = actionPending
	app.ExecuteCopilotAction(context.Background(), "p-1")
	if !loop.waitUntil(t, 3*time.Second, func() bool { return app.pendingActions["p-1"].Status == actionDone }) {
		t.Fatalf("execute did not finish: %+v", app.pendingActions["p-1"])
	}
	writes, params := st.rpc.snapshot()
	if len(writes) != 1 || writes[0] != "compose.draft" || params[0]["idempotencyKey"] != "k-1" {
		t.Fatalf("expected one draft with the proposal's idempotency key, got %v %+v", writes, params)
	}
	if _, bad := params[0]["bcc"]; bad {
		t.Fatal("bcc must never be sent")
	}
}

// --- error localization ------------------------------------------------------------------

func TestLocalizeErrorCodes(t *testing.T) {
	cases := map[string]error{
		"Read-Only-Modus aktiv":           &backend.APIError{Code: "read_only", Message: "read_only"},
		"geändert":                        &backend.APIError{Code: "message_changed", Message: "message_changed: x"},
		"Ergebnis unbekannt":              &backend.APIError{Code: "outcome_unknown", Message: "outcome_unknown"},
		"Mail-Engine ist nicht verbunden": backend.ErrOffline,
		"Mehrdeutig":                      errors.New("ambiguous: 2 copies"),
		"Funktion deaktiviert: ai":        errors.New("feature_disabled:ai"),
	}
	for want, err := range cases {
		if got := localizeError(err, "de"); !strings.Contains(got, want) {
			t.Errorf("localizeError(%v) = %q, want it to contain %q", err, got, want)
		}
	}
	if got := localizeError(&backend.APIError{Code: "account_not_enabled", Message: "account_not_enabled"}, "en"); !strings.Contains(got, "not enabled") {
		t.Errorf("unexpected EN text %q", got)
	}
	if got := localizeError(errors.New("something else"), "en"); got != "something else" {
		t.Errorf("unknown errors must pass through, got %q", got)
	}
}

// --- calendar delete ---------------------------------------------------------------------

func TestCalendarDeleteNeedsConfirmationAndScope(t *testing.T) {
	st := newTestStack(t)
	app := st.newApp()
	loop := attachUILoop(app, nil)
	app.readOnly = false
	app.engineReady = true
	ev := backend.CalendarEvent{ID: "e1", CalendarID: "c1", Title: "Weekly", Start: "2026-11-18T14:00:00Z", RecurrenceID: "20261118T140000Z", IsRecurring: true}
	app.RequestDeleteCalendarEvent(ev)
	tt := ui.NewTester(app.RenderMain, 1380, 860)
	tt.Frame()
	if !tt.HasText("Nur dieses Vorkommen") || !tt.HasText("Ganze Serie löschen") {
		t.Fatalf("expected occurrence/series choice; texts=%v", tt.Texts())
	}
	if writes, _ := st.rpc.snapshot(); len(writes) != 0 {
		t.Fatal("opening the confirmation must not delete")
	}
	app.ConfirmDeleteCalendarEvent(context.Background(), backend.ScopeOccurrence)
	app.ConfirmDeleteCalendarEvent(context.Background(), backend.ScopeOccurrence) // double click: busy
	if !loop.waitUntil(t, 3*time.Second, func() bool { return !app.calDelBusy && !app.calDelOpen }) {
		t.Fatalf("delete did not finish: %q", app.calDelError)
	}
	writes, params := st.rpc.snapshot()
	if len(writes) != 1 || writes[0] != "calendar.delete" {
		t.Fatalf("expected exactly one delete, got %v", writes)
	}
	if params[0]["recurrenceId"] != "20261118T140000Z" || params[0]["scope"] != "occurrence" {
		t.Fatalf("expected occurrence scope with recurrenceId, got %+v", params[0])
	}
}

// Folders and searches reach back past 30 days; reloading the view on screen keeps the pages
// already loaded.
func TestMessageLoadPaging(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }, ConfigSaver: func(config.Config) error { return nil }, Store: triage.NewStoreAt(t.TempDir())})
	app.navID = "recent"
	if req := app.newMessageLoad(); req.filter.Days != 30 || req.filter.Limit != messagePage {
		t.Fatalf("recent: %+v", req.filter)
	}
	app.searchQuery = "angebot"
	if req := app.newMessageLoad(); req.filter.Days != 0 {
		t.Fatalf("a search must not stop at 30 days: %+v", req.filter)
	}
	app.searchQuery = ""
	app.navID = "folder:acc/INBOX"
	req := app.newMessageLoad()
	if req.filter.Days != 0 || req.filter.FolderID != "acc/INBOX" {
		t.Fatalf("folder: %+v", req.filter)
	}
	var msgs []backend.MessageHeader
	for i := 0; i < 150; i++ {
		msgs = append(msgs, backend.MessageHeader{ID: fmt.Sprintf("m%d", i)})
	}
	if !app.applyMessageLoad(req, msgs, 400) {
		t.Fatal("load must apply")
	}
	app.listKey, app.nextCursor = req.key, "150"
	if again := app.newMessageLoad(); again.filter.Limit != 150 {
		t.Fatalf("a reload of the same folder must keep 150 mails, limit=%d", again.filter.Limit)
	}
	app.navID = "folder:acc/Sent"
	if other := app.newMessageLoad(); other.filter.Limit != messagePage {
		t.Fatalf("another folder starts with one page, limit=%d", other.filter.Limit)
	}
}

func TestSplitEmailListNamesWithCommas(t *testing.T) {
	got := splitEmailList("Müller, Hans <h@x.de>; b@y.de, \"Weber, Alice\" <a@acme.de>")
	if len(got) != 3 {
		t.Fatalf("got %q", got)
	}
	for _, g := range got {
		if _, err := mail.ParseAddress(g); err != nil {
			t.Errorf("%q does not parse: %v", g, err)
		}
	}
	if a, _ := mail.ParseAddress(got[0]); a == nil || a.Name != "Müller, Hans" || a.Address != "h@x.de" {
		t.Fatalf("first = %q", got[0])
	}
	if got := splitEmailList("Hans Müller"); len(got) != 1 || got[0] != "Hans Müller" {
		t.Fatalf("a name alone stays visible for the error: %q", got)
	}
}

// Closing the compose window with unsent text keeps it open and asks; unchanged it just closes.
func TestComposeCloseAsksWhenChanged(t *testing.T) {
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) { return config.Default(), nil }, ConfigSaver: func(config.Config) error { return nil }, Store: triage.NewStoreAt(t.TempDir())})
	app.OpenComposeNew()
	app.guardComposeClose()
	app.composeOpen = false // Escape, unchanged
	app.guardComposeClose()
	if app.composeOpen || app.composeCloseAsk {
		t.Fatal("an unchanged compose window closes")
	}
	app.OpenComposeNew()
	app.guardComposeClose()
	app.composeBody = "Hallo Hans,"
	app.composeOpen = false // Escape with text
	app.guardComposeClose()
	if !app.composeOpen || !app.composeCloseAsk {
		t.Fatal("closing with text must ask")
	}
	app.closeCompose() // Discard
	app.guardComposeClose()
	if app.composeOpen {
		t.Fatal("discard closes")
	}
}

func TestReplyGoesToReplyTo(t *testing.T) {
	msg := &backend.MessageDetail{MessageHeader: backend.MessageHeader{From: "Shop <noreply@shop.de>"}, Headers: map[string]string{"reply-to": "Support <help@shop.de>"}}
	if got := replyRecipient(msg); got != `"Support" <help@shop.de>` && got != "Support <help@shop.de>" {
		t.Fatalf("reply to %q", got)
	}
	msg.Headers = nil
	if got := replyRecipient(msg); got != msg.From {
		t.Fatalf("without Reply-To: %q", got)
	}
}
