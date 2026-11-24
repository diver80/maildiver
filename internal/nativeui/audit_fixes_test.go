package nativeui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/triage"
)

// recordRPC answers every engine call (canned answers or "{}") and records it.
type recordRPC struct {
	mu     sync.Mutex
	calls  []string
	params []map[string]any
	answer map[string]string
}

func (*recordRPC) Connected() bool { return true }

func (r *recordRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, method)
	r.params = append(r.params, p)
	if ans := r.answer[method]; ans != "" {
		return json.RawMessage(ans), nil
	}
	return json.RawMessage(`{}`), nil
}

func (r *recordRPC) find(method string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for i, c := range r.calls {
		if c == method {
			out = append(out, r.params[i])
		}
	}
	return out
}

type auditApp struct {
	*App
	rpc *recordRPC
	box *cfgBox
	ui  chan func()
}

func newAuditApp(t *testing.T, direct DirectController) *auditApp {
	t.Helper()
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	box := newCfgBox(cfg)
	rpc := &recordRPC{answer: map[string]string{}}
	bc := backend.New(backend.Options{Engine: rpc, Events: events.NewHub(), Store: triage.NewStoreAt(t.TempDir()), LoadConfig: box.load, Version: "test"})
	app := NewApp(Options{ConfigLoader: box.load, ConfigSaver: box.save, Backend: bc, Direct: direct, Store: triage.NewStoreAt(t.TempDir())})
	q := make(chan func(), 256)
	app.SetHooks(func(fn func()) { q <- fn }, nil, nil, nil, nil)
	return &auditApp{App: app, rpc: rpc, box: box, ui: q}
}

func (a *auditApp) drain() {
	for {
		select {
		case fn := <-a.ui:
			if fn != nil {
				fn()
			}
		default:
			return
		}
	}
}

// waitFor runs the UI queue until cond holds (3 s at most).
func (a *auditApp) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.drain()
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func hasTextContaining(tt *ui.Tester, part string) bool {
	for _, s := range tt.Texts() {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// A2: a notification tapped on a phone opens its mail (list → mail, back leads up again).
func TestPhoneNotificationClickOpensMail(t *testing.T) {
	app := newAuditApp(t, nil)
	app.winW, app.winH = 360, 740
	r := app.phoneRouter()
	r.Push(phoneRouteList)
	r.Push(phoneRouteMail) // another mail was open

	app.HandleNotificationClick(alertMailPrefix + "m7")
	if r.Path() != phoneRouteMail || app.selectedID != "m7" {
		t.Fatalf("mail notification: path %q selected %q", r.Path(), app.selectedID)
	}
	r.Back()
	if r.Path() != phoneRouteList {
		t.Fatalf("back from the mail goes to the list, got %q", r.Path())
	}
	r.Back()
	if r.Path() != phoneRouteMailboxes || r.CanGoBack() {
		t.Fatalf("then to the mailboxes, got %q (can go back: %v)", r.Path(), r.CanGoBack())
	}

	app.HandleNotificationClick(alertMailBatchPrefix + "123")
	if r.Path() != phoneRouteList || app.navID != "focus" || app.selectedID != "" {
		t.Fatalf("batch notification: path %q nav %q selected %q", r.Path(), app.navID, app.selectedID)
	}
	app.HandleNotificationClick(alertCalPrefix + "e1")
	if r.Path() != phoneRouteList || app.navID != "calendar" {
		t.Fatalf("calendar notification: path %q nav %q", r.Path(), app.navID)
	}
	app.drain()
}

// A3: "Verwerfen" deletes the draft this compose auto-saved, never a draft reopened from Drafts.
func TestDiscardComposeDeletesOwnAutoSavedDraft(t *testing.T) {
	app := newAuditApp(t, nil)
	app.OpenComposeNew()
	app.composeTo = "max.muster@example.org"
	app.composeDraftID = "d1"
	app.discardCompose()
	app.waitFor(t, "compose.discardDraft d1", func() bool { return len(app.rpc.find("compose.discardDraft")) == 1 })
	if got := app.rpc.find("compose.discardDraft")[0]["draftId"]; got != "d1" {
		t.Fatalf("discarded draft %v, want d1", got)
	}

	// Reopened from Drafts: discarding the edits keeps the draft.
	app.OpenComposeFromDraft(&backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "d2", AccountID: "acc1", Subject: "Entwurf"}, BodyText: "Text"})
	app.composeBody += " geändert"
	app.discardCompose()
	// Sent (or saved): closeCompose keeps it, the engine deletes it after sending.
	app.OpenComposeNew()
	app.composeDraftID = "d3"
	app.closeCompose()
	time.Sleep(30 * time.Millisecond)
	app.drain()
	if n := len(app.rpc.find("compose.discardDraft")); n != 1 {
		t.Fatalf("only the auto-saved draft of a new mail may be deleted, got %d deletes", n)
	}

	// An auto-save still running when the user discards is deleted once it lands.
	app.rpc.mu.Lock()
	app.rpc.answer["compose.draft"] = `{"ok":true,"draftId":"d9"}`
	app.rpc.mu.Unlock()
	app.OpenComposeNew()
	app.composeTo = "max.muster@example.org"
	app.composeBody = "Halbfertig"
	app.autoSaveDraft(context.Background(), app.composeSnapshot())
	app.discardCompose()
	app.waitFor(t, "late auto-save deleted", func() bool {
		ds := app.rpc.find("compose.discardDraft")
		return len(ds) == 2 && ds[1]["draftId"] == "d9"
	})
}

// A4: a reply to a mail the user sent goes to its recipients, not back to the user.
func TestReplyToOwnSentMailGoesToRecipients(t *testing.T) {
	app := newAuditApp(t, nil)
	app.cfg.Direct.Accounts = []config.DirectAccount{{ID: "acc1", Email: "max.muster@example.org", Aliases: []config.AccountAlias{{Email: "info@example.org"}}}}
	sent := &backend.MessageDetail{MessageHeader: backend.MessageHeader{
		ID: "s1", AccountID: "acc1", Subject: "Angebot",
		From: "Max Muster <max.muster@example.org>",
		To:   []string{"Erika Beispiel <erika@example.org>", "info@example.org"},
		CC:   []string{"team@example.org", "max.muster@example.org"},
	}}
	app.OpenComposeReply(sent, "")
	if app.composeTo != "Erika Beispiel <erika@example.org>" {
		t.Fatalf("reply to own mail goes to %q", app.composeTo)
	}
	app.closeCompose()
	app.OpenComposeReplyAll(sent, "")
	if app.composeTo != "Erika Beispiel <erika@example.org>" || app.composeCC != "team@example.org" {
		t.Fatalf("reply-all to own mail: to %q cc %q", app.composeTo, app.composeCC)
	}
	app.closeCompose()

	// Someone else's mail still goes to its sender.
	in := &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "i1", AccountID: "acc1", Subject: "Frage", From: "Erika Beispiel <erika@example.org>", To: []string{"max.muster@example.org"}}}
	app.OpenComposeReply(in, "")
	if app.composeTo != in.From {
		t.Fatalf("reply goes to %q, want the sender", app.composeTo)
	}
	// Only own addresses in To: back to the sender rather than an empty reply.
	if got := replyRecipient(&backend.MessageDetail{MessageHeader: backend.MessageHeader{From: "max.muster@example.org", To: []string{"info@example.org"}}}, map[string]bool{"max.muster@example.org": true, "info@example.org": true}); got != "max.muster@example.org" {
		t.Fatalf("note to self replies to %q", got)
	}
}

// A5: the undo bars show on every phone screen, and going to the background sends at once.
func TestPhoneUndoBarsOnEveryScreenAndSuspendFlush(t *testing.T) {
	app := newAuditApp(t, nil)
	app.winW, app.winH = 360, 740
	now := time.Now()
	app.pendingSends = []*pendingComposeSend{{
		Req:   backend.ComposeRequest{AccountID: "acc1", To: []string{"max.muster@example.org"}, Subject: "Projekt", Body: "Hallo"},
		Nonce: "n1", CreatedAt: now, Deadline: now.Add(20 * time.Second),
	}}
	app.undo = &undoAction{Kind: "move", Label: "Archiviert", CreatedAt: now}
	app.selectedMsg = &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "m1", AccountID: "acc1", Subject: "Hallo", From: "Erika <erika@example.org>"}}
	app.selectedID = "m1"
	for _, route := range []string{phoneRouteMailboxes, phoneRouteList, phoneRouteMail} {
		app.phone.router = ui.NewRouter(route)
		tt := ui.NewTester(func(c *ui.Context) {
			app.winW, app.winH = 360, 740
			app.renderPhone(c)
		}, 360, 740)
		tt.Frame()
		if !hasTextContaining(tt, "Sende „Projekt“") || !tt.HasText("Archiviert") {
			t.Fatalf("%s: undo bars missing in %v", route, tt.Texts())
		}
	}

	app.FlushPendingSendsOnSuspend()
	if len(app.pendingSends) != 0 {
		t.Fatal("the queue must be handed off at once")
	}
	app.waitFor(t, "compose.send", func() bool { return len(app.rpc.find("compose.send")) == 1 })
}

// A6: inserted text lands at the caret, else above the quoted original – not below it.
func TestComposeInsertAboveQuoteOrAtCaret(t *testing.T) {
	reply := "Hallo Erika,\n\nAm 1. März schrieb Erika Beispiel:\n> Passt Montag?\n> Gruß"
	got := insertComposeSnippet(reply, "Montag passt.\n\nViele Grüße")
	want := "Hallo Erika,\n\nMontag passt.\n\nViele Grüße\n\nAm 1. März schrieb Erika Beispiel:\n> Passt Montag?\n> Gruß"
	if got != want {
		t.Fatalf("insert above quote:\n%q\nwant\n%q", got, want)
	}
	empty := "\n\n-- \nMax Muster\n\nAm 1. März schrieb Erika:\n> Passt Montag?"
	if got := insertComposeSnippet(empty, "Ja."); got != "Ja.\n\n-- \nMax Muster\n\nAm 1. März schrieb Erika:\n> Passt Montag?" {
		t.Fatalf("insert into empty reply: %q", got)
	}
	if got := insertComposeSnippet("Hallo\n\n-- \nMax", "**fett**"); got != "Hallo **fett**\n\n-- \nMax" {
		t.Fatalf("inline snippet: %q", got)
	}
	if got := insertComposeSnippet("Hallo", "\n- Punkt"); got != "Hallo\n- Punkt" {
		t.Fatalf("no quote: %q", got)
	}

	app := newAuditApp(t, nil)
	app.OpenComposeNew()
	app.composeBody = reply
	app.composeCaret, app.composeCaretBody, app.composeCaretOK = len([]rune("Hallo Erika,")), reply, true
	app.insertComposeText(" Montag passt")
	if !strings.HasPrefix(app.composeBody, "Hallo Erika, Montag passt\n\nAm 1. März") || !app.composeCaretApply ||
		app.composeCaret != len([]rune("Hallo Erika, Montag passt")) {
		t.Fatalf("caret insert: %q caret %d", app.composeBody, app.composeCaret)
	}
	// A caret read from another text is stale: above the quote instead.
	app.composeCaretBody = "anders"
	app.insertComposeText("Bis dann.")
	if !strings.Contains(app.composeBody, "Montag passt Bis dann.\n\nAm 1. März") {
		t.Fatalf("stale caret: %q", app.composeBody)
	}
}

// A7a: the mail text is the draft_reply proposal, wherever the model lists it; a move proposal
// becomes a chip that moves only on click.
func TestAIDraftAndMoveProposals(t *testing.T) {
	res := ai.AnalyzeResponse{Answer: "Kurz", Proposals: []triage.Proposal{
		{Kind: "move_folder", FolderPath: "Archiv/Rechnungen"},
		{Kind: "draft_reply", Body: ""},
		{Kind: "draft_reply", Body: "Hallo Erika,\n\nDanke!", Subject: "Re: Rechnung"},
	}}
	if p, ok := res.DraftProposal(); !ok || p.Body != "Hallo Erika,\n\nDanke!" {
		t.Fatalf("draft proposal %+v %v", p, ok)
	}
	if p, ok := res.MoveProposal(); !ok || p.FolderPath != "Archiv/Rechnungen" {
		t.Fatalf("move proposal %+v %v", p, ok)
	}
	if _, ok := (ai.AnalyzeResponse{Proposals: []triage.Proposal{{Kind: "move_folder", Body: "x"}}}).DraftProposal(); ok {
		t.Fatal("a move proposal is no draft")
	}

	app := newAuditApp(t, nil)
	app.folders = []backend.Folder{{ID: "f1", AccountID: "acc1", Name: "INBOX", Path: "/INBOX", Type: "inbox"}, {ID: "f2", AccountID: "acc1", Name: "Rechnungen", Path: "/Archiv/Rechnungen"}}
	f, ok := app.findAccountFolder("acc1", "Archiv/Rechnungen")
	if !ok || f.ID != "f2" {
		t.Fatalf("folder lookup %+v %v", f, ok)
	}
	app.inspectorMove, app.inspectorMoveMsgID = &f, "m1"
	app.ApplyInspectorMove(context.Background())
	app.waitFor(t, "move action", func() bool { return len(app.rpc.find("messages.actions")) == 1 })
	if got := app.rpc.find("messages.actions")[0]["moveToFolderId"]; got != "f2" {
		t.Fatalf("moved to %v, want f2", got)
	}
}

// A7b: AI filter suggestions update the rule they name instead of always adding one.
func TestQuickRuleAISuggestionUpdatesExistingRule(t *testing.T) {
	filters := []triage.NativeFilter{
		{AccountID: "acc1", Name: "Rechnungen", Enabled: true, Conditions: []triage.NativeCondition{{Field: "from", Op: "contains", Value: "@shop.example.org"}},
			Actions: []triage.NativeAction{{Type: "moveToFolder", TargetFolderPath: "Rechnungen"}}},
		{AccountID: "acc1", Name: "Alerts", Enabled: true, MatchAll: true, Conditions: []triage.NativeCondition{
			{Field: "from", Op: "contains", Value: "alerts@example.org", BooleanAnd: true},
			{Field: "subject", Op: "contains", Value: "[Alert]", BooleanAnd: true},
		}, Actions: []triage.NativeAction{{Type: "markRead"}}},
	}
	wizard := backend.FilterUpsertRequest{Mode: "create", AccountID: "acc1", Name: "Neu", Enabled: true, MatchAll: true,
		Conditions: []triage.NativeCondition{{Field: "from", Op: "contains", Value: "@billing.example.org", BooleanAnd: true}, {Field: "from", Op: "contains", Value: "@shop.example.org", BooleanAnd: true}},
		Actions:    []backend.FilterActionSpec{{Type: "moveToFolder", TargetFolderPath: "Trash"}}}

	target, mode := quickRuleTargetFor(ai.FilterRuleSuggestion{Action: "append_or", TargetFilterName: "rechnungen"}, filters, "acc1")
	if target == nil || target.Name != "Rechnungen" || mode != "append_or" {
		t.Fatalf("target %+v mode %q", target, mode)
	}
	// An AND rule split into OR terms would catch far more than the preview shows: a new rule.
	if req := buildQuickRuleUpsert(target, mode, wizard); req.Mode != "create" || req.Name != "Neu" {
		t.Fatalf("append_or of an AND wizard rule must create a new rule: %+v", req)
	}
	orWizard := wizard
	orWizard.MatchAll = false
	orWizard.Conditions = []triage.NativeCondition{{Field: "from", Op: "contains", Value: "@shop.example.org"}, {Field: "from", Op: "contains", Value: "@billing.example.org"}}
	req := buildQuickRuleUpsert(target, mode, orWizard)
	if req.Mode != "replace" || req.ExistingName != "Rechnungen" || req.Name != "Rechnungen" || req.MatchAll || len(req.Conditions) != 2 ||
		req.Conditions[1].Value != "@billing.example.org" || req.Conditions[1].BooleanAnd ||
		len(req.Actions) != 1 || req.Actions[0].TargetFolderPath != "Rechnungen" {
		t.Fatalf("append_or request %+v", req)
	}

	// append_or on an AND rule would change its logic: a new rule instead.
	if tf, _ := quickRuleTargetFor(ai.FilterRuleSuggestion{Action: "append_or", TargetFilterName: "Alerts"}, filters, "acc1"); tf != nil {
		t.Fatal("append_or must not turn an AND rule into OR")
	}
	// fix_and_to_or would widen the user's AND rule: a new rule instead.
	if tf, _ := quickRuleTargetFor(ai.FilterRuleSuggestion{Action: "fix_and_to_or", TargetFilterName: "Alerts"}, filters, "acc1"); tf != nil {
		t.Fatal("fix_and_to_or must not change an existing rule")
	}

	target, mode = quickRuleTargetFor(ai.FilterRuleSuggestion{Action: "replace", TargetFilterName: "Alerts"}, filters, "acc1")
	req = buildQuickRuleUpsert(target, mode, wizard)
	if req.Mode != "replace" || req.Name != "Alerts" || !req.MatchAll || len(req.Conditions) != 2 || req.Actions[0].TargetFolderPath != "Trash" {
		t.Fatalf("replace request %+v", req)
	}

	// Unknown target, another account's rule or "create": a new rule.
	for _, sug := range []ai.FilterRuleSuggestion{
		{Action: "append_or", TargetFilterName: "Gibt es nicht"},
		{Action: "create", TargetFilterName: "Rechnungen"},
	} {
		if tf, _ := quickRuleTargetFor(sug, filters, "acc1"); tf != nil {
			t.Fatalf("%+v must create a rule", sug)
		}
	}
	if tf, _ := quickRuleTargetFor(ai.FilterRuleSuggestion{Action: "replace", TargetFilterName: "Rechnungen"}, filters, "acc2"); tf != nil {
		t.Fatal("a rule of another account must not be changed")
	}
	if got := buildQuickRuleUpsert(nil, "", wizard); got.Mode != "create" || got.Name != "Neu" {
		t.Fatalf("no target: %+v", got)
	}
}

// A8: the notification switch is in the settings and background notifications obey it.
func TestNotificationSwitchInSettingsAndBackground(t *testing.T) {
	app := newAuditApp(t, nil)
	msgs := []mailengine.StoredMessage{{ID: "m1", From: "Erika <erika@example.org>", Subject: "Hallo"}}
	if opts, ok := app.backgroundMailNotification(msgs); !ok || opts.ID != alertMailPrefix+"m1" {
		t.Fatalf("default: %+v %v", opts, ok)
	}
	two := append(msgs, mailengine.StoredMessage{ID: "m2", Subject: "Zweite"})
	if opts, ok := app.backgroundMailNotification(two); !ok || !strings.HasPrefix(opts.ID, alertMailBatchPrefix) {
		t.Fatalf("batch: %+v %v", opts, ok)
	}

	app.OpenSettings()
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 360, 740
		app.renderSettingsBody(c)
	}, 360, 1400)
	tt.Frame()
	if err := tt.Click("Systembenachrichtigungen anzeigen"); err != nil {
		t.Fatalf("switch not in the general settings: %v (%v)", err, tt.Texts())
	}
	tt.Frame()
	if app.notifyEnabled {
		t.Fatal("the switch must turn notifications off")
	}
	app.waitFor(t, "saved switch", func() bool {
		cfg, _ := app.box.load()
		return cfg.App.NotifyEnabled != nil && !*cfg.App.NotifyEnabled
	})
	if _, ok := app.backgroundMailNotification(msgs); ok {
		t.Fatal("background mail must not notify when notifications are off")
	}
}

// fakeSaveDirect is the engine's file export for the phone save tests.
type fakeSaveDirect struct {
	DirectController
	dir string
}

func (f *fakeSaveDirect) SaveMessageEMLToDownloads(_ context.Context, msgID string) (string, error) {
	p := filepath.Join(f.dir, msgID+".eml")
	return p, os.WriteFile(p, []byte("Subject: x\r\n\r\nx"), 0o600)
}

func (f *fakeSaveDirect) SaveAttachmentToDownloads(_ context.Context, _, _, filename string) (string, error) {
	if filename == "" {
		return "", errors.New("no name")
	}
	p := filepath.Join(f.dir, filename)
	return p, os.WriteFile(p, []byte("MZ"), 0o600)
}

// A9: on phones files go through the system's save dialog, not into the private cache.
func TestPhoneSavesThroughSystemDialog(t *testing.T) {
	app := newAuditApp(t, &fakeSaveDirect{dir: t.TempDir()})
	var mu sync.Mutex
	var saved []string
	app.SetSaveCopyHook(func(path, name string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, name)
		return "content://downloads/" + name, nil
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(saved)
	}
	app.selectedMsg = &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "m1", AccountID: "acc1"}}
	app.SaveSelectedMessageEML(context.Background())
	app.waitFor(t, ".eml save dialog", func() bool { return count() == 1 })
	// A risky type is never opened: the user picks where it goes.
	app.DownloadAndOpenAttachment(context.Background(), "m1", "2", "setup.exe")
	app.waitFor(t, "attachment save dialog", func() bool { return count() == 2 })
	app.SaveAttachmentCopy(context.Background(), "m1", "3", "rechnung.pdf")
	app.waitFor(t, "save copy", func() bool { return count() == 3 })
	mu.Lock()
	defer mu.Unlock()
	if saved[0] != "m1.eml" || saved[1] != "setup.exe" || saved[2] != "rechnung.pdf" {
		t.Fatalf("saved %v", saved)
	}
}

// A9: without the direct engine the phone's mail sheet offers no .eml export (as the desktop).
func TestPhoneSheetHidesEMLWithoutDirectEngine(t *testing.T) {
	app := newAuditApp(t, nil)
	app.winW, app.winH = 360, 740
	app.selectedMsg = &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "m1", AccountID: "acc1", Subject: "Hallo", From: "Erika <erika@example.org>"}}
	app.selectedID = "m1"
	app.phone.router = ui.NewRouter(phoneRouteMail)
	app.phone.more = true
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW, app.winH = 360, 740
		app.renderPhone(c)
	}, 360, 740)
	tt.Frame()
	if !hasTextContaining(tt, "Filterregel erstellen") {
		t.Fatalf("sheet not shown: %v", tt.Texts())
	}
	if hasTextContaining(tt, ".eml") {
		t.Fatal("no .eml export without the direct engine")
	}
}
