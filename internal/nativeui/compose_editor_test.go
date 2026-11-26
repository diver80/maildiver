package nativeui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/triage"
)

type fakeComposeEditorHost struct {
	mu       sync.Mutex
	widget   *fakeComposeWidget
	loaded   []string
	sigs     []string
	inserted []string
	outHTML  string
	outText  string
}

type fakeComposeWidget struct{ ui.NativeWebView }

func (f *fakeComposeEditorHost) Widget() ui.NativeWebView {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.widget == nil {
		return nil
	}
	return f.widget
}

func (f *fakeComposeEditorHost) Ensure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.widget == nil {
		f.widget = &fakeComposeWidget{}
	}
	return nil
}

func (f *fakeComposeEditorHost) LoadContent(initialHTML string, pal ComposeThemePalette, lang string) error {
	_ = ComposeThemePalette(pal)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = append(f.loaded, composeEditorDocumentHTML(initialHTML, pal, lang))
	return nil
}

func (f *fakeComposeEditorHost) UpdateSignature(sigHTML string) {
	f.mu.Lock()
	f.sigs = append(f.sigs, sigHTML)
	f.mu.Unlock()
}

func (f *fakeComposeEditorHost) UpdateTheme(ComposeThemePalette) {}

func (f *fakeComposeEditorHost) InsertHTML(fragHTML string) {
	f.mu.Lock()
	f.inserted = append(f.inserted, fragHTML)
	f.mu.Unlock()
}

func (f *fakeComposeEditorHost) SetUserHTML(fragHTML string) {
	f.mu.Lock()
	f.inserted = append(f.inserted, "SET:"+fragHTML)
	f.mu.Unlock()
}

func (f *fakeComposeEditorHost) ReadContent(context.Context) (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outHTML, f.outText, true
}

func (f *fakeComposeEditorHost) Focus()   {}
func (f *fakeComposeEditorHost) Destroy() {}

func TestComposeEditorSanitizesForwardHTMLAndReadsOnSend(t *testing.T) {
	initial := buildComposeEditorInitialHTML(
		"Hallo!",
		"Alice Example\nExample GmbH",
		"---------- Forwarded message ----------\nFrom: bob@example.com",
		`<div><p>Bericht</p><script>alert(1)</script><p onclick="evil()">Text</p></div>`,
		nil,
		"",
	)
	if strings.Contains(initial, "<script") || strings.Contains(initial, "onclick") {
		t.Fatalf("buildComposeEditorInitialHTML kept unsafe markup: %s", initial)
	}
	if !strings.Contains(initial, `data-mdv-sig="1"`) || !strings.Contains(initial, `data-mdv-fwd="1"`) || !strings.Contains(initial, "Bericht") {
		t.Fatalf("unexpected initial HTML: %s", initial)
	}
	doc := composeEditorDocumentHTML(initial, ComposeThemePalette{
		Bg: "#151821", Surface: "#1e222d", Text: "#e6edf3", Muted: "#9ca3af", Border: "#2f3545", Accent: "#f59e0b", Dark: true,
	}, "de")
	for _, want := range []string{"tbl-r-add", "tbl-c-add", "insertOrderedList", "removeFormat", "btn-img", "btn-tbl"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("compose editor document missing %q", want)
		}
	}
}

type composeCaptureRPC struct {
	mu   sync.Mutex
	sent []backend.ComposeRequest
}

func (*composeCaptureRPC) Connected() bool { return true }
func (r *composeCaptureRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method == "compose.send" {
		raw, _ := json.Marshal(params)
		var req backend.ComposeRequest
		_ = json.Unmarshal(raw, &req)
		r.mu.Lock()
		r.sent = append(r.sent, req)
		r.mu.Unlock()
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func TestComposeWYSIWYGSendAndFileDrop(t *testing.T) {
	cfg := config.Default()
	cfg.Language = "de"
	cfg.Mailbox.ReadOnly = false
	rpc := &composeCaptureRPC{}
	bc := backend.New(backend.Options{
		Engine:     rpc,
		Events:     events.NewHub(),
		Store:      triage.NewStoreAt(t.TempDir()),
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Version:    "test",
	})
	uiQueue := make(chan func(), 32)
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		Backend:      bc,
	})
	app.SetHooks(func(fn func()) { uiQueue <- fn }, nil, nil, nil, nil)
	host := &fakeComposeEditorHost{
		outHTML: `<!doctype html><html><body><p><b>Fett</b> und <img src="data:image/png;base64,iVBORw0KGgo="></p></body></html>`,
		outText: "Fett und",
	}
	app.SetComposeEditorHost(host)
	app.OpenComposeNew()
	app.composeTo = "alice@example.com"
	app.composeSubject = "WYSIWYG Test"

	tmpFile := filepath.Join(t.TempDir(), "angebot.pdf")
	if err := os.WriteFile(tmpFile, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		app.winW = 1280
		app.winH = 820
		app.renderComposeContent(c, true)
	}, 900, 680)
	tt.Frame()

	if len(host.loaded) != 1 {
		t.Fatalf("expected WebView editor loaded once on first frame, got %d", len(host.loaded))
	}
	if !tt.DropFiles(400, 300, tmpFile) {
		t.Fatal("compose container did not accept dropped file")
	}
	tt.Frame()
	if len(app.composeAttachments) != 1 || app.composeAttachments[0] != tmpFile {
		t.Fatalf("dropped attachment not added: %v", app.composeAttachments)
	}

	app.SendCompose(context.Background(), app.buildComposeReq(false))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case fn := <-uiQueue:
			if fn != nil {
				fn()
			}
		default:
			rpc.mu.Lock()
			n := len(rpc.sent)
			rpc.mu.Unlock()
			if n > 0 {
				goto done
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
done:
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	if len(rpc.sent) != 1 {
		t.Fatalf("expected 1 sent request, got %d", len(rpc.sent))
	}
	sent := rpc.sent[0]
	if !sent.IsHTML || !strings.Contains(sent.BodyHTML, "<b>Fett</b>") || sent.Body != "Fett und" {
		t.Fatalf("expected WYSIWYG HTML/text forwarded to compose.send, got %+v", sent)
	}
}

func TestSettingsModelFieldRendersDropdownWhenModelsAvailable(t *testing.T) {
	cfg := config.Default()
	cfg.Language = "en"
	cfg.AI.Agent.Backend = "ollama"
	cfg.AI.Agent.Model = "example-mini:latest"
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(config.Config) error { return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
	})
	app.OpenSettings()
	app.settings.tab = settingsTabAI
	app.settings.agentModelsKey = "ollama|"
	app.settings.agentModels = []string{"example-mini:latest", "example-pro:latest"}

	tt := ui.NewTester(func(c *ui.Context) {
		app.winW = 1280
		app.winH = 820
		app.renderSettingsModal(c)
	}, 900, 760)
	tt.Frame()
	if !tt.HasText("example-mini:latest") {
		t.Fatalf("expected model select displaying example-mini:latest, texts=%v", tt.Texts())
	}
}

func TestComposeEditorEnhancedToolbarAndImageControls(t *testing.T) {
	pal := ComposeThemePalette{
		Bg:      "#111317",
		Surface: "#1a1d24",
		Text:    "#f3f4f6",
		Muted:   "#9ca3af",
		Border:  "#2e3340",
		Accent:  "#f59e0b",
		Dark:    true,
	}
	for _, lang := range []string{"de", "en"} {
		doc := composeEditorDocumentHTML("<p>Hello</p>", pal, lang)
		if strings.Contains(doc, "%!(") {
			t.Fatalf("lang=%s: unexpected fmt.Sprintf error in compose HTML:\n%s", lang, doc)
		}
		for _, want := range []string{
			`id="btn-undo"`,
			`id="btn-redo"`,
			`id="btn-hr"`,
			`id="img-bar"`,
			`id="img-handle"`,
			`data-img-pct="25"`,
			`data-img-pct="50"`,
			`data-img-pct="75"`,
			`data-img-pct="100"`,
			`id="img-smaller"`,
			`id="img-larger"`,
			`id="img-del"`,
			`mdv-img-sel`,
			`#editor [data-mdv-sig="1"]`,
			`protectSignatures`,
			`ensureParagraphInUserBox`,
			`defaultParagraphSeparator`,
			`insertSoftLineBreak`,
			`#editor p { margin: 0 0 0.75em 0; }`,
			`updateFormatStates`,
			`sanitizePastedHTML`,
			`DOMParser`,
			`clampSelectionBeforeSig`,
			`normalizeUserDivsPreservingSelection`,
			`ensureTrailingBRSentinel`,
			`removeAttribute('contenteditable')`,
		} {
			if !strings.Contains(doc, want) {
				t.Fatalf("lang=%s: expected %q in compose editor document", lang, want)
			}
		}
	}
}

func TestVersionDisplayedInUIAndCSVPreview(t *testing.T) {
	cfg := config.Default()
	cfg.Language = "de"
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(config.Config) error { return nil },
		Store:        triage.NewStoreAt(t.TempDir()),
		Version:      "0.7.13",
	})
	tt := ui.NewTester(func(c *ui.Context) {
		app.winW = 1280
		app.winH = 820
		app.renderToolbar(c)
	}, 1280, 120)
	tt.Frame()
	if !tt.HasText("PostDive") || !tt.HasText("v0.7.13") {
		t.Fatalf("expected PostDive + v0.7.13 in toolbar, texts=%v", tt.Texts())
	}

	app.applyComposeSuggestion("to", mailengine.RecipientSuggestion{DisplayName: "Bob", Email: "bob@example.com"})
	app.composeTo = `"Doe, Jane" <jane@example.com>, bo`
	app.applyComposeSuggestion("to", mailengine.RecipientSuggestion{DisplayName: "Bob", Email: "bob@example.com"})
	if !strings.HasPrefix(app.composeTo, `"Doe, Jane" <jane@example.com>, Bob <bob@example.com>`) {
		t.Fatalf("quoted display name comma corrupted by applyComposeSuggestion: %q", app.composeTo)
	}

	md, cut, ok := formatDelimitedTableMarkdown("Name;Betrag\nAlice;12,50\nBob;9,00\n", ".csv", 200)
	if !ok || cut || !strings.Contains(md, "| Name | Betrag |") || !strings.Contains(md, "| Alice | 12,50 |") {
		t.Fatalf("unexpected CSV markdown preview: ok=%v cut=%v md=%q", ok, cut, md)
	}

	// Focus unread badge must reflect Focus (last 14 days), not older folder unread counts.
	app.navID = "focus"
	app.folders = []backend.Folder{
		{ID: "f1", AccountID: "acc1", Name: "INBOX", Path: "INBOX", Type: "inbox", UnreadCount: 60},
		{ID: "f2", AccountID: "acc1", Name: "REWE", Path: "INBOX/REWE", UnreadCount: 4},
	}
	app.messages = []backend.MessageHeader{
		{ID: "m1", Subject: "Starred only", Unread: false, Flagged: true},
	}
	app.totalMessages = 1
	app.focusUnread = 0
	app.focusTotal = 1
	if got := app.unreadCount(); got != 0 {
		t.Fatalf("Focus unreadCount with 0 recent unread + 1 read starred must be 0 (not 64), got %d", got)
	}
	if got := app.focusCount(); got != 1 {
		t.Fatalf("Focus badge focusCount with 1 starred mail must be 1, got %d", got)
	}
}

func TestComposeAIAssistReplaceUndoAndRender(t *testing.T) {
	// 1. replaceComposeUserText preserves signature and quoted reply.
	orig := "Erster Entwurf.\n\n-- \nFrank Hess\n\nAm 23.11. schrieb Alice:\n> Frage?"
	got := replaceComposeUserText(orig, "Hallo Alice,\n\n15 Uhr passt mir gut!\n\nViele Grüße\nFrank")
	if !strings.HasPrefix(got, "Hallo Alice,\n\n15 Uhr passt mir gut!") {
		t.Fatalf("expected replaced top text, got %q", got)
	}
	if !strings.Contains(got, "-- \nFrank Hess") || !strings.Contains(got, "> Frage?") {
		t.Fatalf("signature or reply quote lost during replaceComposeUserText: %q", got)
	}

	// 2. Render Compose window with AI Assist panel open, apply preview, and undo.
	app := NewApp(Options{ConfigLoader: func() (config.Config, error) {
		c := config.Default()
		c.Language = "de"
		return c, nil
	}})
	app.agentBackend = "llamacpp"
	app.agentModel = "mtplx-inkling-2-mini-optimized-speed"
	app.OpenComposeReply(&backend.MessageDetail{
		MessageHeader: backend.MessageHeader{
			ID:      "m-reply-1",
			Subject: "Projekt-Termin",
			From:    "Alice <alice@example.com>",
			Date:    "2026-11-23T10:00:00Z",
		},
		BodyText: "Können wir um 15 Uhr sprechen?",
	}, "Alter Text")
	app.composeAIOpen = true
	app.composeAIResult = "Hallo Alice,\n\n15 Uhr passt wunderbar."
	app.composeAIReview = []string{"Terminzeit bestätigt."}

	tt := ui.NewTester(app.RenderComposeWindow, 760, 680)
	tt.Frame()
	if !tt.HasText("KI-Hilfe") || !tt.HasText("Antwort entwerfen") || !tt.HasText("DE ⇄ EN") || !tt.HasText("Übernehmen") {
		t.Fatalf("expected Compose AI Assist controls in detached reply window, texts=%v", tt.Texts())
	}

	app.applyComposeAIResult(true)
	if !strings.HasPrefix(app.composeBody, "Hallo Alice,\n\n15 Uhr passt wunderbar.") || !strings.Contains(app.composeBody, "> Können wir um 15 Uhr sprechen?") {
		t.Fatalf("applyComposeAIResult did not replace user text cleanly: %q", app.composeBody)
	}
	if !app.composeAIUndoReady {
		t.Fatalf("expected composeAIUndoReady after apply")
	}
	app.undoComposeAIApply()
	if !strings.HasPrefix(app.composeBody, "Alter Text") || !strings.Contains(app.composeBody, "> Können wir um 15 Uhr sprechen?") {
		t.Fatalf("undoComposeAIApply did not restore previous draft: %q", app.composeBody)
	}
}

func TestComposeEditorCaretUsesAccent(t *testing.T) {
	doc := composeEditorDocumentHTML("<p>Hallo</p>", ComposeThemePalette{}, "de")
	if !strings.Contains(doc, "#editor, #editor * { caret-color: var(--accent); }") {
		t.Fatal("the editor caret should use the accent color")
	}
	if strings.Contains(doc, "%!") {
		t.Fatal("format verbs broken in the editor document")
	}
}
