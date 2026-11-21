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

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/triage"
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

func (f *fakeComposeEditorHost) InsertHTML(fragHTML string) {
	f.mu.Lock()
	f.inserted = append(f.inserted, fragHTML)
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
