package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStripSignatureDelimAndAppendNoDoubleDash(t *testing.T) {
	legacy := "-- \n--\nAlex Example\nalex@example.org"
	got := appendAccountSignature("Hallo,", legacy)
	if strings.Count(got, "-- ") != 1 || strings.Contains(got, "-- \n--") {
		t.Fatalf("expected single '-- ' delimiter, got:\n%s", got)
	}
	if !strings.Contains(got, "Alex Example") {
		t.Fatalf("signature body lost: %q", got)
	}

	emptyBody := appendAccountSignature("", "-- \nAlex Example")
	if emptyBody != "\n\n-- \nAlex Example" {
		t.Fatalf("unexpected empty-body signature result: %q", emptyBody)
	}
}

func TestDetachedComposeAndEventWindows(t *testing.T) {
	app := NewApp(Options{})
	app.engineReady = true

	syncCalls := 0
	app.SetAuxWindowHook(func() { syncCalls++ })

	// Opening Compose triggers aux-window sync, does NOT block main-window shortcuts,
	// and renders cleanly inside RenderComposeWindow.
	app.OpenComposeNew()
	if !app.composeOpen || syncCalls == 0 {
		t.Fatalf("expected composeOpen=true and sync hook called (calls=%d)", syncCalls)
	}
	if app.modalOpen() {
		t.Fatal("detached Compose window must not block main-window modalOpen()")
	}

	mainTT := ui.NewTester(app.RenderMain, 1280, 800)
	mainTT.Frame()
	if _, ok := mainTT.Find("Save Draft"); ok {
		t.Fatal("RenderMain must not draw inline Compose modal when detachedAuxWindows is active")
	}

	cmpTT := ui.NewTester(app.RenderComposeWindow, 720, 640)
	cmpTT.Frame()
	if _, ok := cmpTT.Find("Close"); !ok {
		t.Fatalf("expected Close button in detached Compose window; texts=%v", cmpTT.Texts())
	}

	// Untouched compose closes directly via TryCloseComposeWindow.
	if !app.TryCloseComposeWindow() || app.composeOpen {
		t.Fatal("expected untouched detached Compose window to close cleanly")
	}

	// Edited compose keeps window open with confirmation prompt.
	app.OpenComposeNew()
	cmpTT.Frame()
	app.composeSubject = "Wichtige Rückmeldung"
	if app.TryCloseComposeWindow() || !app.composeOpen || !app.composeCloseAsk {
		t.Fatalf("expected dirty Compose window to prompt before closing (open=%v ask=%v)", app.composeOpen, app.composeCloseAsk)
	}
	app.closeCompose()

	// Opening Event editor triggers aux-window sync and renders in RenderEventWindow.
	prevSync := syncCalls
	app.OpenNewEventModal(nil)
	if !app.eventOpen || syncCalls <= prevSync {
		t.Fatalf("expected eventOpen=true and sync hook called")
	}
	if app.modalOpen() {
		t.Fatal("detached Event window must not block main-window modalOpen()")
	}
	evTT := ui.NewTester(app.RenderEventWindow, 560, 580)
	evTT.Frame()
	if _, ok := evTT.Find("Save Event"); !ok {
		t.Fatalf("expected Save Event button in detached Event window; texts=%v", evTT.Texts())
	}
	app.CloseEventDialog()
	if app.eventOpen {
		t.Fatal("expected CloseEventDialog to close event window")
	}
}
