package nativeui

import (
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/theme"
)

// A refused login must not sit next to a green "Connected (IMAP)" (issue #1).
func TestFooterStatusReflectsSyncErrors(t *testing.T) {
	th := PaletteToUITheme(theme.ListPresets()[0])
	a := &App{engineReady: true, syncErrors: map[string]string{}, lang: "en"}
	a.accounts = []backend.Account{{ID: "work"}, {ID: "home"}}
	if s, c := a.footerStatus(th); s != "Connected (IMAP)" || c != th.Success {
		t.Fatalf("no errors: %q", s)
	}
	a.syncErrors["work"] = "IMAP Anmeldung fehlgeschlagen (x): imap: NO authentication failed"
	if s, c := a.footerStatus(th); s != "Partly connected (1/2)" || c != th.Warning {
		t.Fatalf("one of two failing: %q", s)
	}
	a.accounts = a.accounts[:1]
	if s, c := a.footerStatus(th); s != "Login failed" || c != th.Danger {
		t.Fatalf("only account failing: %q", s)
	}
	a.syncErrors["work"] = "dial tcp: i/o timeout"
	if s, c := a.footerStatus(th); s != "Not connected" || c != th.Danger {
		t.Fatalf("network error: %q", s)
	}
}
