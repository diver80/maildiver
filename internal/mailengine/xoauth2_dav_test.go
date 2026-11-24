package mailengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestXOAUTH2SASLAndConfig(t *testing.T) {
	cli := NewXOAUTH2Client("alice@contoso.com", "eyJ-access-token")
	mech, ir, err := cli.Start()
	if err != nil {
		t.Fatalf("XOAUTH2 Start: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Fatalf("unexpected mech %q", mech)
	}
	want := "user=alice@contoso.com\x01auth=Bearer eyJ-access-token\x01\x01"
	if string(ir) != want {
		t.Fatalf("unexpected initial response %q, want %q", string(ir), want)
	}
	resp, err := cli.Next([]byte(`{"status":"400"}`))
	if err != nil || len(resp) != 0 {
		t.Fatalf("expected empty ack on XOAUTH2 error challenge, got resp=%q err=%v", string(resp), err)
	}

	gcfg, err := mailOAuthConfig("google", "cid-g", "sec", "", "http://127.0.0.1:9999/cb")
	if err != nil || len(gcfg.Scopes) == 0 || !strings.Contains(gcfg.Scopes[0], "mail.google.com") {
		t.Fatalf("unexpected google mail oauth config: %+v err=%v", gcfg, err)
	}
	mcfg, err := mailOAuthConfig("microsoft", "cid-m", "", "organizations", "http://127.0.0.1:9999/cb")
	if err != nil || !strings.Contains(mcfg.Endpoint.AuthURL, "login.microsoftonline.com/organizations") {
		t.Fatalf("unexpected microsoft oauth config: %+v err=%v", mcfg, err)
	}

	// Verify resolveAccountSecret returns stored valid OAuth2 access token.
	secrets := secretstore.NewMemoryStore()
	tokBytes, _ := json.Marshal(oauth2.Token{
		AccessToken: "fresh-bearer-xyz",
		Expiry:      time.Now().Add(time.Hour),
	})
	_ = secrets.Set(mailOAuthTokenKey("acc-m365"), string(tokBytes))
	eng, err := New(Options{DBPath: filepath.Join(t.TempDir(), "test.db"), Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	got, err := eng.resolveAccountSecret(context.Background(), config.DirectAccount{
		ID:            "acc-m365",
		Email:         "frank@contoso.com",
		AuthType:      "xoauth2",
		OAuthProvider: "microsoft",
		OAuthClientID: "cid-m",
	})
	if err != nil || got != "fresh-bearer-xyz" {
		t.Fatalf("resolveAccountSecret got %q err=%v", got, err)
	}
}

func TestCalDAVAndCardDAVSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "davpass" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != "REPORT" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		if strings.HasPrefix(r.URL.Path, "/cal/") {
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/ev1.ics</d:href>
    <d:propstat>
      <d:status>HTTP/1.1 200 OK</d:status>
      <d:prop>
        <d:getetag>"etag-1"</d:getetag>
        <c:calendar-data>BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:dav-ev-1@example.org
DTSTART:20261122T090000Z
DTEND:20261122T100000Z
SUMMARY:CalDAV Sprint Review
LOCATION:https://meet.jit.si/postdive
END:VEVENT
END:VCALENDAR</c:calendar-data>
      </d:prop>
    </d:propstat>
  </d:response>
</d:multistatus>`))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">
  <d:response>
    <d:href>/card/c1.vcf</d:href>
    <d:propstat>
      <d:status>HTTP/1.1 200 OK</d:status>
      <d:prop>
        <d:getetag>"cetag-1"</d:getetag>
        <card:address-data>BEGIN:VCARD
VERSION:3.0
FN:Erika Musterfrau
EMAIL:erika@example.org
ORG:Muster GmbH
TEL:+49 711 123456
END:VCARD</card:address-data>
      </d:prop>
    </d:propstat>
  </d:response>
</d:multistatus>`))
	}))
	defer srv.Close()

	// Reject non-HTTPS non-loopback DAV URLs.
	if _, err := validateDAVURL("http://cal.example.org/dav", false); err == nil {
		t.Fatal("expected plaintext http:// DAV URL to be rejected")
	}

	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "dav.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	eng.allowLoopbackICal = true

	nEv, err := eng.AddCalDAVAccount(context.Background(), "Nextcloud", srv.URL+"/cal/", "alice", "davpass")
	if err != nil || nEv != 1 {
		t.Fatalf("AddCalDAVAccount got n=%d err=%v", nEv, err)
	}
	nCt, err := eng.AddCardDAVAccount(context.Background(), "Nextcloud Kontakte", srv.URL+"/card/", "alice", "davpass")
	if err != nil || nCt != 1 {
		t.Fatalf("AddCardDAVAccount got n=%d err=%v", nCt, err)
	}
	cts, err := eng.ListContacts(context.Background(), "erika")
	if err != nil || len(cts) != 1 || cts[0].Email != "erika@example.org" {
		t.Fatalf("expected CardDAV contact stored, got %+v err=%v", cts, err)
	}
}
