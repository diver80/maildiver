package mailengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestMIMEAttachmentsRoundtrip(t *testing.T) {
	payload := []byte("Hallo PostDive Anhang PDF-Daten 12345!")
	_, raw := BuildRawMessage(OutgoingMessage{
		FromName:  "Frank",
		FromEmail: "frank@example.de",
		To:        []string{"alice@example.de"},
		Subject:   "Dokument im Anhang",
		Body:      "Bitte prüfen.",
		Attachments: []OutgoingAttachment{{
			Filename:    "rechnung-2026.pdf",
			ContentType: "application/pdf",
			Data:        payload,
		}},
	})

	parsed, err := ParseRawMessage(raw)
	if err != nil {
		t.Fatalf("ParseRawMessage: %v", err)
	}
	if !strings.Contains(parsed.BodyText, "Bitte prüfen.") {
		t.Fatalf("unexpected body: %q", parsed.BodyText)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.Name != "rechnung-2026.pdf" {
		t.Fatalf("unexpected attachment name: %q", att.Name)
	}

	name, ct, got, err := ExtractAttachment(raw, att.PartName, att.Name)
	if err != nil {
		t.Fatalf("ExtractAttachment: %v", err)
	}
	if name != "rechnung-2026.pdf" || ct != "application/pdf" {
		t.Fatalf("unexpected extracted meta: name=%q ct=%q", name, ct)
	}
	if string(got) != string(payload) {
		t.Fatalf("extracted payload mismatch: got %q, want %q", string(got), string(payload))
	}
}

func TestContactsVCFAndGoogleContactsAndAutocomplete(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Default()
	loadCfg := func() (config.Config, error) { return cfg, nil }
	saveCfg := func(c config.Config) error { cfg = c; return nil }
	mem := secretstore.NewMemoryStore()

	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "postdive.db"),
		Secrets:      mem,
		ConfigLoader: loadCfg,
		ConfigSaver:  saveCfg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 1. Import .vcf (vCard 3.0 with folded line)
	vcfPath := filepath.Join(dir, "contacts.vcf")
	vcfData := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:Dr. Anna Müller",
		"N:Müller;Anna;;;",
		"EMAIL;TYPE=INTERNET:anna@firma.de",
		"TEL;TYPE=CELL:+49 170 987654",
		"ORG:Firma GmbH",
		"TITLE:CTO",
		"NOTE:Wichtige ",
		" Ansprechpartnerin",
		"END:VCARD",
		"",
	}, "\r\n")
	if err := os.WriteFile(vcfPath, []byte(vcfData), 0o600); err != nil {
		t.Fatalf("write vcf: %v", err)
	}
	n, err := eng.ImportVCFFile(ctx, vcfPath)
	if err != nil || n != 1 {
		t.Fatalf("ImportVCFFile: n=%d err=%v", n, err)
	}

	// 2. Export .vcf and re-parse
	outPath := filepath.Join(dir, "export.vcf")
	exported, err := eng.ExportVCFFile(ctx, outPath)
	if err != nil || exported != 1 {
		t.Fatalf("ExportVCFFile: n=%d err=%v", exported, err)
	}
	rawOut, err := os.ReadFile(outPath)
	if err != nil || !strings.Contains(string(rawOut), "anna@firma.de") || !strings.Contains(string(rawOut), "Firma GmbH") {
		t.Fatalf("exported vcf missing fields: %s", string(rawOut))
	}

	// 3. SuggestRecipients autocomplete
	sug := eng.SuggestRecipients(ctx, "anna", 5)
	if len(sug) != 1 || sug[0].Email != "anna@firma.de" || sug[0].Source != "contact" {
		t.Fatalf("unexpected suggestions: %+v", sug)
	}

	// 4. Google Contacts Sync via People API mock
	peopleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/people/me/connections") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"connections": [{
				"resourceName": "people/c12345",
				"names": [{"displayName":"Max Mustermann","givenName":"Max","familyName":"Mustermann"}],
				"emailAddresses": [{"value":"max@googlemail.com"}],
				"phoneNumbers": [{"value":"+49 30 111222","type":"work"}],
				"organizations": [{"name":"Beispiel AG","title":"CEO"}]
			}]
		}`))
	}))
	defer peopleSrv.Close()

	cfg.Direct.GoogleCalendar.ClientID = "test-client"
	eng.gcalPeopleBase = peopleSrv.URL
	eng.gcalEndpoint = oauth2.Endpoint{AuthURL: peopleSrv.URL + "/auth", TokenURL: peopleSrv.URL + "/token"}
	tok := &oauth2.Token{AccessToken: "valid-token", Expiry: time.Now().Add(time.Hour)}
	if err := eng.saveGCalToken(tok); err != nil {
		t.Fatalf("saveGCalToken: %v", err)
	}

	gCount, err := eng.SyncGoogleContacts(ctx)
	if err != nil || gCount != 1 {
		t.Fatalf("SyncGoogleContacts: count=%d err=%v", gCount, err)
	}

	list, err := eng.ListContacts(ctx, "")
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 contacts (local + google), got %d err=%v", len(list), err)
	}

	// 5. SaveContact edit + DeleteContact
	edited, err := eng.SaveContact(ctx, backend.ContactRecord{
		ID:          list[0].ID,
		DisplayName: "Updated Contact",
		Email:       list[0].Email,
	})
	if err != nil || edited.DisplayName != "Updated Contact" {
		t.Fatalf("SaveContact: %+v err=%v", edited, err)
	}
	if err := eng.DeleteContact(ctx, edited.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	after, _ := eng.ListContacts(ctx, "")
	if len(after) != 1 {
		t.Fatalf("expected 1 contact after delete, got %d", len(after))
	}
}
