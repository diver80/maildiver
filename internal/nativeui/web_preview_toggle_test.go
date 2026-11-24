package nativeui

import (
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

func TestWebPreviewDefaultOffAndGlobalToggle(t *testing.T) {
	if config.Default().App.LinkPreview.Enabled {
		t.Fatal("auto web preview must be off by default")
	}

	saved := config.Default()
	saves := 0
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return saved, nil },
		ConfigSaver: func(next config.Config) error {
			saved = next
			saves++
			return nil
		},
	})
	host := &fakePreviewHost{}
	app.SetWebPreviewHost(host)

	jiraMsg := &backend.MessageDetail{
		ID:       "m-jira",
		From:     "Jira <jira@acme.atlassian.net>",
		BodyHtml: `<a href="https://acme.atlassian.net/browse/DEV-7">DEV-7</a>`,
	}
	app.selectedID = jiraMsg.ID
	app.selectedMsg = jiraMsg

	// Off by default: matching Jira mail must NOT auto-open the preview column.
	app.syncPreviewForMessage(jiraMsg)
	if app.preview.open || len(host.loaded) != 0 {
		t.Fatalf("preview must stay closed while auto preview is off: %+v", app.preview)
	}

	// Switch ON: persists and immediately opens preview for the selected matching mail.
	app.ToggleWebPreviewAuto()
	if !saved.App.LinkPreview.Enabled || !app.cfg.App.LinkPreview.Enabled || saves != 1 {
		t.Fatalf("expected Enabled=true persisted (saves=%d)", saves)
	}
	if !app.preview.open || app.preview.activeURL != "https://acme.atlassian.net/browse/DEV-7" {
		t.Fatalf("expected preview auto-opened after switching on: %+v", app.preview)
	}

	// Switch OFF: persists and immediately closes the pane.
	app.ToggleWebPreviewAuto()
	if saved.App.LinkPreview.Enabled || app.cfg.App.LinkPreview.Enabled {
		t.Fatal("expected Enabled=false persisted")
	}
	if app.preview.open {
		t.Fatal("preview pane must close when auto preview is switched off")
	}

	// Manual per-mail button still works on demand while auto is off.
	app.ToggleWebPreview()
	if !app.preview.open || app.preview.activeURL == "" {
		t.Fatalf("manual web preview must still open while auto is off: %+v", app.preview)
	}
}

func TestGoogleCloudSetupLinks(t *testing.T) {
	app := NewApp(Options{})
	links := googleCloudSetupLinks(app)
	if len(links) == 0 || links[0].URL != "https://console.cloud.google.com/" {
		t.Fatalf("expected Google Cloud Console link first, got %+v", links)
	}
	for _, lk := range links {
		if !isSafeWebURL(lk.URL) {
			t.Fatalf("unsafe setup link %q", lk.URL)
		}
	}
}
