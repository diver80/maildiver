package mailengine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestDAVDiscoveryWritebackAndDeleteSync(t *testing.T) {
	var mu sync.Mutex
	var putBodies []string
	var deletedPaths []string
	cardRemoved := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			if strings.HasPrefix(r.URL.Path, "/.well-known/caldav") {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:propstat><d:prop><c:calendar-home-set><d:href>/cal-home/</d:href></c:calendar-home-set></d:prop></d:propstat></d:response></d:multistatus>`))
				return
			}
			if r.URL.Path == "/cal-home/" {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/cal-home/team/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`))
				return
			}
		case "REPORT":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			if strings.HasPrefix(r.URL.Path, "/cal-home/team") {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"></d:multistatus>`))
				return
			}
			if cardRemoved {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"></d:multistatus>`))
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:response><d:href>/card/a1.vcf</d:href><d:propstat><d:prop><card:address-data>BEGIN:VCARD
VERSION:3.0
FN:Sam Example
EMAIL:sam@example.org
END:VCARD</card:address-data></d:prop></d:propstat></d:response></d:multistatus>`))
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			putBodies = append(putBodies, r.URL.Path+"\n"+string(b))
			w.Header().Set("ETag", `"etag-put-1"`)
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "davwb.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	eng.allowLoopbackICal = true
	ctx := context.Background()

	// 1. AddCalDAVAccount via base server URL -> resolves /.well-known/caldav -> /cal-home/team/
	if _, err := eng.AddCalDAVAccount(ctx, "Work", srv.URL, "alice", "secret"); err != nil {
		t.Fatalf("AddCalDAVAccount discovery: %v", err)
	}
	if len(cfg.Direct.CalDAVAccounts) != 1 || !strings.Contains(cfg.Direct.CalDAVAccounts[0].URL, "/cal-home/team/") {
		t.Fatalf("expected discovered CalDAV URL, got %+v", cfg.Direct.CalDAVAccounts)
	}

	// 2. calendar.create pushes ICS PUT, calendar.update pushes ICS PUT, calendar.delete issues DELETE.
	calID := cfg.Direct.CalDAVAccounts[0].ID
	rawCreate, err := eng.Call(ctx, "calendar.create", backend.CreateCalendarEventRequest{
		CalendarID: calID,
		Title:      "Design Kickoff",
		Start:      "2026-11-23T10:00:00Z",
		End:        "2026-11-23T11:00:00Z",
	})
	if err != nil {
		t.Fatalf("calendar.create on caldav: %v", err)
	}
	var created struct {
		Event backend.CalendarEvent `json:"event"`
	}
	_ = json.Unmarshal(rawCreate, &created)
	if created.Event.ID == "" {
		t.Fatalf("expected created event id")
	}
	if _, err := eng.Call(ctx, "calendar.update", map[string]any{"calendarId": calID, "id": created.Event.ID, "title": "Updated Kickoff"}); err != nil {
		t.Fatalf("calendar.update on caldav: %v", err)
	}
	if _, err := eng.Call(ctx, "calendar.delete", map[string]any{"calendarId": calID, "id": created.Event.ID}); err != nil {
		t.Fatalf("calendar.delete on caldav: %v", err)
	}
	mu.Lock()
	if len(putBodies) != 2 || !strings.Contains(putBodies[1], "SUMMARY:Updated Kickoff") || len(deletedPaths) != 1 {
		mu.Unlock()
		t.Fatalf("unexpected CalDAV HTTP operations: puts=%d dels=%d", len(putBodies), len(deletedPaths))
	}
	mu.Unlock()

	// 3. CardDAV writeback + remote delete sync.
	if _, err := eng.AddCardDAVAccount(ctx, "TeamBook", srv.URL+"/card/", "alice", "secret"); err != nil {
		t.Fatalf("AddCardDAVAccount: %v", err)
	}
	bookID := "carddav:" + cfg.Direct.CardDAVAccounts[0].ID
	if _, err := eng.Call(ctx, "contacts.upsert", backend.ContactUpsertRequest{
		AddressBookID: bookID,
		Contact:       backend.ContactRecord{DisplayName: "Kim Test", Email: "kim@example.org"},
	}); err != nil {
		t.Fatalf("contacts.upsert on carddav: %v", err)
	}
	mu.Lock()
	putCount := len(putBodies)
	cardRemoved = true
	mu.Unlock()
	if putCount != 3 {
		t.Fatalf("expected CardDAV PUT after contacts.upsert, total PUTs=%d", putCount)
	}
	if err := eng.SyncAllDAV(ctx); err != nil {
		t.Fatalf("SyncAllDAV: %v", err)
	}
	cts, _ := eng.ListContacts(ctx, "")
	if len(cts) != 0 {
		t.Fatalf("expected remotely deleted CardDAV contacts to be pruned, got %+v", cts)
	}
}
