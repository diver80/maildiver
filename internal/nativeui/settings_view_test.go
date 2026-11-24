package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/config"
)

// The settings dialog writes only its own fields; keys go to the key file and apply at once.
func TestSettingsDialogSavesAIAndKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	app, _, st := keymapApp(t)
	st.box.mu.Lock()
	st.box.cfg.Mailbox.ReadOnly = false
	st.box.cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	st.box.mu.Unlock()

	app.OpenSettings()
	if !app.modalOpen() || len(app.settings.providers) == 0 || len(app.settings.keys) == 0 {
		t.Fatalf("dialog not filled: %+v", app.settings)
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1000)
	tt.Frame()
	if !tt.HasText("Darstellung & Sprache") || !tt.HasText("KI & Sync") {
		t.Fatalf("general tab / tab bar missing: %v", tt.Texts())
	}
	// Verify each settings tab renders its respective domain cleanly.
	tabChecks := []struct {
		tab  int
		want string
	}{
		{settingsTabCompose, "Textbausteine / Vorlagen"},
		{settingsTabFolders, "Ordner in der Seitenleiste (pro Postfach)"},
		{settingsTabPrivacy, "Browser-Vorschau (Split-Panel)"},
		{settingsTabAI, "API-Schlüssel"},
	}
	for _, tc := range tabChecks {
		app.settings.tab = tc.tab
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Fatalf("tab %d missing %q: %v", tc.tab, tc.want, tt.Texts())
		}
	}
	if !tt.HasText("KI-Assistent") || !tt.HasText("MCP-Config kopieren") {
		t.Fatalf("sections missing on AI tab: %v", tt.Texts())
	}

	app.settings.agentBackend = "claude-api"
	app.settings.agentModel = "  claude-sonnet  "
	app.settings.customInstructions = "  Immer kurz und direkt antworten.  "
	app.settings.classifyAI = true
	app.settings.maxCalls = "12"
	app.settings.scanOnNewMail = false
	for i := range app.settings.keys {
		if app.settings.keys[i].envKey == "ANTHROPIC_API_KEY" {
			app.settings.keys[i].input = "sk-ant-test"
		}
	}
	// Out-of-range numbers are refused before anything is written.
	app.settings.scanDays = "999"
	if app.SaveSettings() || app.settings.err == "" {
		t.Fatal("invalid days must be refused")
	}
	if config.LookupAPIKey("ANTHROPIC_API_KEY") != "" {
		t.Fatal("nothing may be written when the form is invalid")
	}
	app.settings.scanDays = "14"
	if !app.SaveSettings() {
		t.Fatalf("save failed: %s", app.settings.err)
	}
	cfg, _ := st.box.load()
	if cfg.AI.Agent.Backend != "claude-api" || cfg.AI.Agent.Model != "claude-sonnet" || !cfg.AI.ClassifyWithAI ||
		cfg.AI.CustomInstructions != "Immer kurz und direkt antworten." ||
		cfg.AI.MaxAICallsPerScan != 12 || cfg.Mailbox.ScanOnNewMail || cfg.Mailbox.ScanDays != 14 {
		t.Fatalf("saved config = %+v / %+v", cfg.AI, cfg.Mailbox)
	}
	// Permissions belong to the add-on and stay as they were.
	if cfg.Mailbox.ReadOnly || len(cfg.Mailbox.TriageAccountIDs) != 1 {
		t.Fatalf("permissions changed: %+v", cfg.Mailbox)
	}
	if got := config.LookupAPIKey("ANTHROPIC_API_KEY"); got != "sk-ant-test" {
		t.Fatalf("key = %q", got)
	}

	// Removing a key.
	app.OpenSettings()
	for i := range app.settings.keys {
		if app.settings.keys[i].envKey == "ANTHROPIC_API_KEY" {
			if !app.settings.keys[i].set {
				t.Fatal("the stored key should show as set")
			}
			app.settings.keys[i].remove = true
		}
	}
	if !app.SaveSettings() || config.LookupAPIKey("ANTHROPIC_API_KEY") != "" {
		t.Fatal("key not removed")
	}
}
