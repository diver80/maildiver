package nativeui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/secretstore"
)

// Waking a phone, Android reports "offline" and then "online" right after: no banner for that.
func TestOfflineBannerWaitsForGrace(t *testing.T) {
	app, _, _ := keymapApp(t)
	updates := make(chan func(), 4)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)

	app.noteNetworkOffline(true)
	app.noteNetworkOffline(false)
	(<-updates)() // the first report's timer: outdated by now
	if app.networkOffline {
		t.Fatal("a short dropout must not show offline")
	}

	app.noteNetworkOffline(true)
	if app.networkOffline {
		t.Fatal("offline only after the grace")
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(offlineGrace + 2*time.Second):
		t.Fatal("timed out")
	}
	if !app.networkOffline {
		t.Fatal("a lasting dropout shows offline")
	}
	app.noteNetworkOffline(false)
	if app.networkOffline {
		t.Fatal("online clears at once")
	}
}

func TestBackgroundRunShownInSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Language = "de"
	loadCfg := func() (config.Config, error) { return cfg, nil }
	eng, err := mailengine.New(mailengine.Options{DBPath: filepath.Join(dir, "postdive.db"), Secrets: secretstore.NewMemoryStore(), ConfigLoader: loadCfg})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	if _, found, err := eng.LastBackgroundRun(context.Background()); err != nil || found {
		t.Fatalf("fresh store: found=%v err=%v", found, err)
	}
	app := NewApp(Options{ConfigLoader: loadCfg, Direct: eng})
	app.bgRunsShown = true
	updates := make(chan func(), 4)
	app.SetHooks(func(fn func()) { updates <- fn }, nil, nil, nil, nil)
	app.OpenSettings()
	tt := ui.NewTester(func(c *ui.Context) { app.renderSettingsBody(c) }, 900, 1400)
	tt.Frame()
	(<-updates)()
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), " "), "Noch kein Abruf im Hintergrund") {
		t.Fatalf("no run yet should say so; texts=%v", tt.Texts())
	}

	at := time.Now().Add(-10 * time.Minute)
	if err := eng.RecordBackgroundRun(context.Background(), at, errors.New("dial tcp: i/o timeout")); err != nil {
		t.Fatal(err)
	}
	app.bgRunAt = time.Time{}
	tt.Frame()
	(<-updates)()
	tt.Frame()
	want := "Zuletzt " + at.Format("15:04") + " (vor 10 Min.) · Fehler: "
	if !strings.Contains(strings.Join(tt.Texts(), " "), want) {
		t.Fatalf("want %q; texts=%v", want, tt.Texts())
	}

	if err := eng.RecordBackgroundRun(context.Background(), at, nil); err != nil {
		t.Fatal(err)
	}
	app.bgRunAt = time.Time{}
	tt.Frame()
	(<-updates)()
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), " "), "(vor 10 Min.) · ok") {
		t.Fatalf("ok run; texts=%v", tt.Texts())
	}
}
