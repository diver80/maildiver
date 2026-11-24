package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/jobs"
)

func enableTriage(st *testStack) {
	st.box.mu.Lock()
	st.box.cfg.Features.Triage = true
	st.box.cfg.AI.ClassifyWithAI = false
	st.box.cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	st.box.cfg.Mailbox.ArchiveDir = st.t.TempDir()
	st.box.mu.Unlock()
}

// A scan from the menu runs in the host; the app reports it and reloads the triage state.
func TestTriageScanFromApp(t *testing.T) {
	app, loop, st := keymapApp(t)
	enableTriage(st)
	cfg, _ := st.box.load()
	app.applyConfig(cfg)
	go app.runEventStream(st.ctx)

	app.StartTriageScan(context.Background())
	if !app.scanRunning {
		t.Fatal("scan should be marked running at once")
	}
	if !loop.waitUntil(t, 5*time.Second, func() bool { return !app.scanRunning && len(app.triageState.Classifications) == 2 }) {
		t.Fatalf("scan did not finish: running=%v state=%v", app.scanRunning, app.triageState.Classifications)
	}
	if got := st.rpc.lastParams("host.triage.collect"); got == nil {
		t.Fatal("the host did not collect through the add-on")
	}
	if got := app.scanReportText(jobs.ScanReport{Classified: 2, Encrypted: 1}); !strings.Contains(got, "2 Mails eingeordnet") || !strings.Contains(got, "1 verschlüsselt") {
		t.Errorf("report text = %q", got)
	}
}

// The archive dialog previews, simulates by default and needs a second click to delete.
func TestArchiveDialog(t *testing.T) {
	app, loop, st := keymapApp(t)
	enableTriage(st)
	cfg, _ := st.box.load()
	app.applyConfig(cfg)
	app.accounts = []backend.Account{{ID: "acc1", Name: "Work"}, {ID: "acc2", Name: "Privat"}}
	go app.runEventStream(st.ctx)

	app.OpenArchiveDialog(context.Background())
	if !app.modalOpen() {
		t.Fatal("the dialog must block single-key shortcuts")
	}
	if len(app.arch.accountLabels) != 1 || app.arch.account != "Work" {
		t.Fatalf("only enabled accounts: %v", app.arch.accountLabels)
	}
	req := app.archiveRequest()
	if req.Mode != jobs.ModeSimulate || req.AccountID != "acc1" || *req.OlderThanDays != 365 || !*req.OnlyRead || !*req.ExcludeStarred {
		t.Fatalf("defaults = %+v", req)
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1000)
	tt.Frame()
	if err := tt.Click("Vorschau"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 5*time.Second, func() bool { return app.arch.preview != nil }) {
		t.Fatalf("no preview: %s", app.arch.err)
	}
	tt.Frame()
	if !tt.HasText("1 Mails (2 KB) in 1 Ordnern") {
		t.Errorf("preview line missing; texts: %v", tt.Texts())
	}

	if err := tt.Click("Starten"); err != nil {
		t.Fatal(err)
	}
	if !loop.waitUntil(t, 5*time.Second, func() bool { return app.arch.report != nil }) {
		t.Fatalf("no report: %s", app.arch.err)
	}
	if !app.arch.report.DryRun || app.arch.report.Exported != 1 {
		t.Errorf("simulation report = %+v", app.arch.report)
	}
	if st.rpc.lastParams("host.archive.raw") != nil {
		t.Error("simulating must not read mails")
	}

	// Deleting at the provider asks once more before it starts.
	app.arch.modeLabel = app.archiveModeLabels()[2]
	tt.Frame()
	app.StartArchiveRun(context.Background())
	if !app.arch.confirm || app.arch.running {
		t.Fatalf("delete must ask first: confirm=%v running=%v", app.arch.confirm, app.arch.running)
	}
	tt.Frame()
	if !tt.HasText("Wirklich löschen?") {
		t.Error("confirm label missing")
	}
}
