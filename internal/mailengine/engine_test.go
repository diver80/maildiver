package mailengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"golang.org/x/oauth2"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/secretstore"
	"github.com/diver80/maildiver/internal/triage"
)

type literalBytes struct {
	*bytes.Reader
	size int64
}

func (l literalBytes) Size() int64 { return l.size }

func appendMemMsg(t *testing.T, u *imapmemserver.User, mbox, raw string, flags ...imap.Flag) {
	t.Helper()
	b := []byte(strings.ReplaceAll(raw, "\n", "\r\n"))
	_, err := u.Append(mbox, literalBytes{Reader: bytes.NewReader(b), size: int64(len(b))}, &imap.AppendOptions{
		Flags: flags,
		Time:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append %s: %v", mbox, err)
	}
}

func startTestIMAPServer(t *testing.T, user, pass string) (addr string, memUser *imapmemserver.User, cleanup func()) {
	t.Helper()
	memSrv := imapmemserver.New()
	u := imapmemserver.NewUser(user, pass)
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	if err := u.Create("Trash", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrTrash}}); err != nil {
		t.Fatal(err)
	}
	if err := u.Create("Sent", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrSent}}); err != nil {
		t.Fatal(err)
	}
	if err := u.Create("Drafts", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrDrafts}}); err != nil {
		t.Fatal(err)
	}
	memSrv.AddUser(u)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memSrv.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), u, func() {
		_ = srv.Close()
		_ = ln.Close()
	}
}

func TestIMAPSyncSearchActionsFiltersAndBridgeIntegration(t *testing.T) {
	ctx := context.Background()
	imapAddr, memUser, stopIMAP := startTestIMAPServer(t, "me@example.org", "s3cr3t-äöü!")
	defer stopIMAP()

	host, portStr, _ := net.SplitHostPort(imapAddr)
	var imapPort int
	_, _ = fmt.Sscanf(portStr, "%d", &imapPort)

	appendMemMsg(t, memUser, "INBOX",
		"From: Alice Weber <alice@acme.de>\n"+
			"To: me@example.org\n"+
			"Subject: =?UTF-8?Q?Angebot_f=C3=BCr_Q4?=\n"+
			"Message-ID: <msg-q4@acme.de>\n"+
			"Date: "+time.Now().UTC().Format(time.RFC1123Z)+"\n"+
			"Content-Type: text/plain; charset=utf-8\n\n"+
			"Hallo Frank, bitte prüfe das Q4 Angebot.\n")

	appendMemMsg(t, memUser, "INBOX",
		"From: Daily Deals <promo@shop.example>\n"+
			"To: me@example.org\n"+
			"Subject: Super Rabatt heute\n"+
			"Message-ID: <promo-1@shop.example>\n"+
			"Date: "+time.Now().Add(-time.Hour).UTC().Format(time.RFC1123Z)+"\n"+
			"Content-Type: text/html; charset=utf-8\n\n"+
			"<html><body><h1>Mega Sale</h1><p>50% Rabatt auf alles!</p></body></html>\n")

	cfg := config.Default()
	var cfgMu sync.Mutex
	tmp := t.TempDir()
	eng, err := New(Options{
		DBPath:  filepath.Join(tmp, "omathunder.db"),
		Secrets: secretstore.NewMemoryStore(),
		Events:  events.NewHub(),
		ConfigLoader: func() (config.Config, error) {
			cfgMu.Lock()
			defer cfgMu.Unlock()
			return cfg, nil
		},
		ConfigSaver: func(next config.Config) error {
			cfgMu.Lock()
			cfg = next
			cfgMu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Test connection before saving.
	accInput := config.DirectAccount{
		Name:        "Work Test",
		Email:       "me@example.org",
		DisplayName: "Frank",
		IMAPHost:    host,
		IMAPPort:    imapPort,
		IMAPTLS:     "plain",
	}
	testRes := eng.TestAccount(ctx, accInput, "s3cr3t-äöü!")
	if !testRes.IMAPOK || testRes.IMAPFolders < 4 {
		t.Fatalf("expected IMAP test OK with >=4 folders, got %+v", testRes)
	}

	savedAcc, err := eng.SaveAccount(ctx, accInput, "s3cr3t-äöü!")
	if err != nil {
		t.Fatal(err)
	}

	// The app's in-process backend on top of the engine.
	bc := backend.New(backend.Options{
		Engine: eng,
		Events: eng.Events(),
		Store:  triage.NewStoreAt(tmp),
		LoadConfig: func() (config.Config, error) {
			cfgMu.Lock()
			defer cfgMu.Unlock()
			return cfg, nil
		},
	})

	// Trigger IMAP sync.
	if _, err := eng.SyncAccount(ctx, savedAcc.ID, false); err != nil {
		t.Fatalf("SyncAccount failed: %v", err)
	}

	// 1. Verify accounts & folders through backend.
	accs, err := bc.Accounts(ctx)
	if err != nil || len(accs) != 1 || accs[0].ID != savedAcc.ID {
		t.Fatalf("unexpected accounts: %+v err=%v", accs, err)
	}
	folders, err := bc.Folders(ctx, savedAcc.ID)
	if err != nil || len(folders) < 4 {
		t.Fatalf("unexpected folders: %+v err=%v", folders, err)
	}

	// 2. List messages (SQLite instant read) & verify FTS5 search.
	page, err := bc.Messages(ctx, backend.MessagesFilter{AccountID: savedAcc.ID})
	if err != nil || page.Total != 2 {
		t.Fatalf("expected 2 synced messages, got total=%d err=%v", page.Total, err)
	}
	searchPage, err := bc.Messages(ctx, backend.MessagesFilter{Query: "Angebot"})
	if err != nil || len(searchPage.Messages) != 1 || !strings.Contains(searchPage.Messages[0].Subject, "Angebot für Q4") {
		t.Fatalf("FTS5 search failed: %+v err=%v", searchPage, err)
	}

	// 3. Fetch full message detail (including HTML->text fallback for promo mail).
	var aliceID, promoID string
	for _, m := range page.Messages {
		if strings.Contains(m.From, "alice@acme.de") {
			aliceID = m.ID
		} else {
			promoID = m.ID
		}
	}
	detail, err := bc.Message(ctx, promoID, "both")
	if err != nil || !strings.Contains(detail.BodyText, "50% Rabatt") || !strings.Contains(detail.BodyHtml, "Mega Sale") {
		t.Fatalf("unexpected message detail: %+v err=%v", detail, err)
	}

	// 4. Mark Alice's mail as read & starred, move promo mail to Trash via IMAP.
	tr := true
	if _, err := bc.MessageActions(ctx, aliceID, backend.MessageActionsRequest{Read: &tr, Flagged: &tr}); err != nil {
		t.Fatalf("mark read/flag failed: %v", err)
	}
	if _, err := bc.MessageActions(ctx, promoID, backend.MessageActionsRequest{Trash: true}); err != nil {
		t.Fatalf("trash failed: %v", err)
	}
	afterTrash, _ := bc.Messages(ctx, backend.MessagesFilter{AccountID: savedAcc.ID})
	if afterTrash.Total != 1 || !afterTrash.Messages[0].Read || !afterTrash.Messages[0].Flagged {
		t.Fatalf("expected 1 read+flagged message left after trash, got %+v", afterTrash)
	}

	// 5. Save a local OmaThunder filter rule and verify automatic filtering on newly arriving INBOX mail.
	_, err = bc.FilterUpsert(ctx, backend.FilterUpsertRequest{
		Mode:      "create",
		AccountID: savedAcc.ID,
		Name:      "Auto-Trash Shop Promo",
		Enabled:   true,
		MatchAll:  false,
		Conditions: []triage.NativeCondition{
			{Field: "from", Op: "contains", Value: "promo@shop.example"},
		},
		Actions: []backend.FilterActionSpec{
			{Type: "moveToFolder", TargetFolderPath: "Trash"},
		},
	})
	if err != nil {
		t.Fatalf("FilterUpsert failed: %v", err)
	}

	appendMemMsg(t, memUser, "INBOX",
		"From: Daily Deals <promo@shop.example>\n"+
			"To: me@example.org\n"+
			"Subject: Noch ein Rabatt\n"+
			"Message-ID: <promo-2@shop.example>\n"+
			"Date: "+time.Now().UTC().Format(time.RFC1123Z)+"\n\nWerbung\n")

	syncRes, err := eng.SyncAccount(ctx, savedAcc.ID, true)
	if err != nil {
		t.Fatalf("incremental sync failed: %v", err)
	}
	if len(syncRes.NewMessages) != 0 {
		t.Fatalf("expected new promo mail to be auto-moved by local filter, remaining=%+v", syncRes.NewMessages)
	}

	// 6. Save Draft via IMAP APPEND & verify idempotency key.
	idemKey := backend.NewIdempotencyKey()
	if _, err := bc.ComposeDraft(ctx, backend.ComposeRequest{
		AccountID:      savedAcc.ID,
		To:             []string{"alice@acme.de"},
		Subject:        "Re: Angebot für Q4",
		Body:           "Sieht gut aus!",
		IdempotencyKey: idemKey,
	}); err != nil {
		t.Fatalf("ComposeDraft failed: %v", err)
	}
	if _, err := bc.ComposeDraft(ctx, backend.ComposeRequest{
		AccountID:      savedAcc.ID,
		To:             []string{"alice@acme.de"},
		Subject:        "Re: Angebot für Q4",
		Body:           "Sieht gut aus!",
		IdempotencyKey: idemKey,
	}); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}

	// 7. Contacts import + enrich.
	up1, err := bc.ContactUpsert(ctx, backend.ContactUpsertRequest{
		Mode: "create",
		Contact: backend.ContactRecord{
			DisplayName: "Alice Weber",
			Email:       "alice@acme.de",
		},
	})
	if err != nil || up1.Action != "imported" {
		t.Fatalf("contact import failed: %+v err=%v", up1, err)
	}
	up2, err := bc.ContactUpsert(ctx, backend.ContactUpsertRequest{
		Mode: "enrich",
		Contact: backend.ContactRecord{
			DisplayName: "Other Name Should Not Overwrite",
			Email:       "alice@acme.de",
			Phone:       "+49 30 555010",
			Company:     "ACME GmbH",
		},
	})
	if err != nil || up2.Action != "enriched" || up2.Contact.DisplayName != "Alice Weber" || up2.Contact.Phone != "+49 30 555010" {
		t.Fatalf("contact enrich failed: %+v err=%v", up2, err)
	}
}

func TestICalParseExpandExportAndFeed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	eng, err := New(Options{
		DBPath:  filepath.Join(tmp, "cal.db"),
		Secrets: secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) {
			return config.Default(), nil
		},
		ConfigSaver: func(config.Config) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	icsData := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Test//EN",
		"BEGIN:VEVENT",
		"UID:weekly-standup@test",
		"DTSTART:20261102T090000Z",
		"DTEND:20261102T093000Z",
		"RRULE:FREQ=WEEKLY;COUNT=4",
		"EXDATE:20261109T090000Z",
		"SUMMARY:Team Standup",
		"LOCATION:Zoom",
		"END:VEVENT",
		"BEGIN:VEVENT",
		"UID:holiday@test",
		"DTSTART;VALUE=DATE:20261115",
		"DTEND;VALUE=DATE:20261116",
		"SUMMARY:Urlaubstag",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")

	icsPath := filepath.Join(tmp, "import.ics")
	if err := os.WriteFile(icsPath, []byte(icsData), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := eng.ImportICSFile(ctx, LocalCalendarID, icsPath)
	if err != nil || n != 2 {
		t.Fatalf("ImportICSFile got n=%d err=%v", n, err)
	}

	raw, err := eng.store.ListRawEvents(ctx, []string{LocalCalendarID})
	if err != nil || len(raw) != 2 {
		t.Fatalf("ListRawEvents got %d err=%v", len(raw), err)
	}

	expanded := ExpandEventsInWindow(
		raw,
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC),
		50,
	)
	// 4 weekly occurrences minus 1 EXDATE = 3 standups + 1 all-day holiday = 4 events.
	if len(expanded) != 4 {
		t.Fatalf("expected 4 expanded events (3 standups + 1 all-day), got %d: %+v", len(expanded), expanded)
	}

	// Verify HTTP .ics feed subscription.
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = io.WriteString(w, icsData)
	}))
	defer feedSrv.Close()

	// Verify SSRF protection blocks loopback iCal feeds by default.
	if _, err := eng.AddICSFeed(ctx, "Blocked Loopback", feedSrv.URL); err == nil {
		t.Fatalf("expected AddICSFeed to block loopback IP by default")
	}

	eng.allowLoopbackICal = true
	fn, err := eng.AddICSFeed(ctx, "Team Feed", feedSrv.URL)
	if err != nil || fn != 2 {
		t.Fatalf("AddICSFeed got n=%d err=%v", fn, err)
	}

	// Verify ExportEventsICS round-trip parses cleanly.
	exported := ExportEventsICS("Export", raw)
	reParsed, err := ParseICS("test", "Export", false, strings.NewReader(exported))
	if err != nil || len(reParsed) != 2 {
		t.Fatalf("ExportEventsICS round-trip failed: len=%d err=%v", len(reParsed), err)
	}
}

func TestGoogleCalendarOAuthAndBidirectionalSync(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	syncStep := 0
	createdCount := 0
	deletedID := ""

	gcalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "ya29.test-access",
				"refresh_token": "1//test-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case r.URL.Path == "/v3/users/me/calendarList":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{
					"id":              "frank@gmail.com",
					"summary":         "Frank Google",
					"primary":         true,
					"accessRole":      "owner",
					"backgroundColor": "#4285f4",
				}},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			mu.Lock()
			syncStep++
			step := syncStep
			mu.Unlock()
			if step == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"nextSyncToken": "sync-tok-1",
					"items": []map[string]any{{
						"id":      "gev-1",
						"status":  "confirmed",
						"summary": "Google Termin 1",
						"start":   map[string]string{"dateTime": "2026-11-20T10:00:00Z"},
						"end":     map[string]string{"dateTime": "2026-11-20T11:00:00Z"},
					}},
				})
				return
			}
			// Incremental sync with syncToken adds a second event.
			if r.URL.Query().Get("syncToken") != "sync-tok-1" {
				http.Error(w, "missing syncToken", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"nextSyncToken": "sync-tok-2",
				"items": []map[string]any{{
					"id":      "gev-2",
					"status":  "confirmed",
					"summary": "Google Termin 2",
					"start":   map[string]string{"dateTime": "2026-11-21T14:00:00Z"},
					"end":     map[string]string{"dateTime": "2026-11-21T15:00:00Z"},
				}},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			mu.Lock()
			createdCount++
			mu.Unlock()
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "gev-created",
				"status":  "confirmed",
				"summary": body["summary"],
				"start":   body["start"],
				"end":     body["end"],
			})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/events/"):
			mu.Lock()
			deletedID = filepath.Base(r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gcalSrv.Close()

	cfg := config.Default()
	var cfgMu sync.Mutex
	eng, err := New(Options{
		DBPath:  filepath.Join(t.TempDir(), "gcal.db"),
		Secrets: secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) {
			cfgMu.Lock()
			defer cfgMu.Unlock()
			return cfg, nil
		},
		ConfigSaver: func(next config.Config) error {
			cfgMu.Lock()
			cfg = next
			cfgMu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	eng.gcalAPIBase = gcalSrv.URL + "/v3"
	eng.gcalEndpoint = oauth2.Endpoint{
		AuthURL:  gcalSrv.URL + "/auth",
		TokenURL: gcalSrv.URL + "/token",
	}
	eng.disableBrowserOpen = true

	// 1. Complete OAuth2 loopback PKCE flow — cancelling the caller's request context right after
	// StartGoogleCalAuth returns must NOT shut down the loopback callback listener prematurely.
	reqCtx, reqCancel := context.WithCancel(ctx)
	authURL, err := eng.StartGoogleCalAuth(reqCtx, "test-client-id.apps.googleusercontent.com", "test-secret")
	reqCancel()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	redirectURI := u.Query().Get("redirect_uri")
	state := u.Query().Get("state")
	if redirectURI == "" || state == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("invalid OAuth URL: %s", authURL)
	}
	resp, err := http.Get(redirectURI + "?code=auth-code-123&state=" + url.QueryEscape(state))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth callback failed: resp=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()

	// Wait briefly for initial background sync triggered by callback.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := eng.GoogleCalStatus(ctx); st.Connected && st.CalendarNum == 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	st := eng.GoogleCalStatus(ctx)
	if !st.Connected || st.CalendarNum != 1 || st.AccountEmail != "frank@gmail.com" {
		t.Fatalf("unexpected GoogleCalStatus: %+v", st)
	}

	// 2. Run incremental sync (uses sync-tok-1 -> receives gev-2).
	if err := eng.SyncGoogleCalendar(ctx); err != nil {
		t.Fatalf("incremental sync failed: %v", err)
	}
	rawEvs, err := eng.store.ListRawEvents(ctx, []string{"gcal:frank@gmail.com"})
	if err != nil || len(rawEvs) != 2 {
		t.Fatalf("expected 2 Google events after incremental sync, got %d err=%v", len(rawEvs), err)
	}

	// 3. Bidirectional Create + Delete on Google Calendar via RPC.
	createRaw, err := eng.Call(ctx, "calendar.create", backend.CreateCalendarEventRequest{
		CalendarID: "gcal:frank@gmail.com",
		Title:      "Neu aus OmaThunder",
		Start:      "2026-11-22T15:00:00Z",
		End:        "2026-11-22T16:00:00Z",
	})
	if err != nil || !strings.Contains(string(createRaw), "Neu aus OmaThunder") {
		t.Fatalf("google calendar.create failed: %s err=%v", createRaw, err)
	}
	if _, err := eng.Call(ctx, "calendar.delete", map[string]any{
		"calendarId": "gcal:frank@gmail.com",
		"eventId":    "gcal:frank@gmail.com::gev-created",
	}); err != nil {
		t.Fatalf("google calendar.delete failed: %v", err)
	}
	mu.Lock()
	gotCreated, gotDeleted := createdCount, deletedID
	mu.Unlock()
	if gotCreated != 1 || gotDeleted != "gev-created" {
		t.Fatalf("expected Google API create=1 delete=gev-created, got %d / %q", gotCreated, gotDeleted)
	}
}

func TestBuildRFC5322BccOmittedOnSendIncludedOnDraft(t *testing.T) {
	base := OutgoingMessage{
		FromName:         "Me",
		FromEmail:        "me@example.com",
		To:               []string{"alice@example.com"},
		BCC:              []string{"secret@example.com"},
		Subject:          "Confidential",
		Body:             "Hello",
		IncludeBccHeader: false,
	}
	_, sentRaw := BuildRawMessage(base)
	if strings.Contains(string(sentRaw), "secret@example.com") || strings.Contains(strings.ToLower(string(sentRaw)), "\nbcc:") {
		t.Fatalf("sent RFC 5322 message must not leak BCC header: %s", sentRaw)
	}

	base.IncludeBccHeader = true
	_, draftRaw := BuildRawMessage(base)
	if !strings.Contains(string(draftRaw), "Bcc: secret@example.com") {
		t.Fatalf("draft RFC 5322 message should retain BCC header: %s", draftRaw)
	}
}

func TestRRULEHighFrequencyDoSGuard(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	winFrom := start.Add(-24 * time.Hour)
	winTo := start.Add(30 * 24 * time.Hour)

	for _, rule := range []string{"FREQ=SECONDLY;INTERVAL=1", "FREQ=MINUTELY;INTERVAL=1"} {
		ev := StoredEvent{
			ID:        "ev-dos",
			Title:     "DoS test",
			StartISO:  start.Format(time.RFC3339),
			EndISO:    end.Format(time.RFC3339),
			StartUnix: start.Unix(),
			EndUnix:   end.Unix(),
			RRule:     rule,
		}
		expanded := ExpandEventsInWindow([]StoredEvent{ev}, winFrom, winTo, 100)
		if len(expanded) > 2 {
			t.Fatalf("high-frequency RRULE %q must not expand into many instances, got %d", rule, len(expanded))
		}
	}
}

func TestExtractAttachmentByPartName(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: Atts\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"bnd\"\r\n\r\n" +
		"--bnd\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody\r\n" +
		"--bnd\r\nContent-Type: application/pdf; name=\"first.pdf\"\r\nContent-Disposition: attachment; filename=\"first.pdf\"\r\n\r\nPDF-ONE\r\n" +
		"--bnd\r\nContent-Type: application/pdf; name=\"second.pdf\"\r\nContent-Disposition: attachment; filename=\"second.pdf\"\r\n\r\nPDF-TWO\r\n" +
		"--bnd--\r\n")
	parsed, err := ParseRawMessage(raw)
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}
	if len(parsed.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(parsed.Attachments))
	}
	second := parsed.Attachments[1]
	name, ct, data, err := ExtractAttachment(raw, second.PartName, "")
	if err != nil {
		t.Fatalf("ExtractAttachment by partName %q failed: %v", second.PartName, err)
	}
	if name != "second.pdf" || ct != "application/pdf" || string(data) != "PDF-TWO" {
		t.Fatalf("unexpected extracted attachment: name=%q ct=%q data=%q", name, ct, string(data))
	}
}
