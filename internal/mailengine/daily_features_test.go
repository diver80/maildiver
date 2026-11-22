package mailengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/secretstore"
)

func setupTestEngineWithIMAPAndSMTP(t *testing.T, aliases []config.AccountAlias) (*Engine, config.DirectAccount, interface{}, *[]string, *string, func()) {
	t.Helper()
	ctx := context.Background()
	imapAddr, memUser, stopIMAP := startTestIMAPServer(t, "me@example.org", "pw")
	smtpAddr, smtpFrom, smtpBody, stopSMTP := startTestSMTPServer(t)

	_ = memUser.Create("Drafts", nil)
	_ = memUser.Create("Sent", nil)
	_ = memUser.Create("Archive", nil)
	_ = memUser.Create("Trash", nil)

	imapHost, imapPortStr, _ := net.SplitHostPort(imapAddr)
	var imapPort int
	_, _ = fmt.Sscanf(imapPortStr, "%d", &imapPort)

	smtpHost, smtpPortStr, _ := net.SplitHostPort(smtpAddr)
	var smtpPort int
	_, _ = fmt.Sscanf(smtpPortStr, "%d", &smtpPort)

	cfg := config.Default()
	var mu sync.Mutex
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "maildiver.db"),
		Secrets:      secretstore.NewMemoryStore(),
		Events:       events.NewHub(),
		ConfigLoader: func() (config.Config, error) { mu.Lock(); defer mu.Unlock(); return cfg, nil },
		ConfigSaver:  func(c config.Config) error { mu.Lock(); cfg = c; mu.Unlock(); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	acc, err := eng.SaveAccount(ctx, config.DirectAccount{
		Name:        "Test",
		DisplayName: "Max Muster",
		Email:       "me@example.org",
		Aliases:     aliases,
		IMAPHost:    imapHost,
		IMAPPort:    imapPort,
		IMAPTLS:     "plain",
		SMTPHost:    smtpHost,
		SMTPPort:    smtpPort,
		SMTPTLS:     "plain",
	}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = eng.Close()
		stopSMTP()
		stopIMAP()
	}
	return eng, acc, memUser, smtpFrom, smtpBody, cleanup
}

func startTestSMTPServer(t *testing.T) (addr string, lastFrom *[]string, lastBody *string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var froms []string
	var body string
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Write([]byte("220 localhost ESMTP\r\n"))
				buf := make([]byte, 64*1024)
				inData := false
				var dataBuf strings.Builder
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					chunk := string(buf[:n])
					if inData {
						dataBuf.WriteString(chunk)
						if strings.Contains(dataBuf.String(), "\r\n.\r\n") {
							mu.Lock()
							body = dataBuf.String()
							mu.Unlock()
							inData = false
							_, _ = c.Write([]byte("250 OK\r\n"))
						}
						continue
					}
					for _, line := range strings.Split(chunk, "\r\n") {
						line = strings.TrimSpace(line)
						if line == "" {
							continue
						}
						up := strings.ToUpper(line)
						switch {
						case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
							_, _ = c.Write([]byte("250-localhost\r\n250 AUTH PLAIN LOGIN\r\n"))
						case strings.HasPrefix(up, "AUTH "):
							_, _ = c.Write([]byte("235 Authenticated\r\n"))
						case strings.HasPrefix(up, "MAIL FROM:"):
							mu.Lock()
							froms = append(froms, strings.TrimSpace(line[len("MAIL FROM:"):]))
							mu.Unlock()
							_, _ = c.Write([]byte("250 OK\r\n"))
						case strings.HasPrefix(up, "RCPT TO:"):
							_, _ = c.Write([]byte("250 OK\r\n"))
						case up == "DATA":
							inData = true
							dataBuf.Reset()
							_, _ = c.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
						case up == "QUIT":
							_, _ = c.Write([]byte("221 Bye\r\n"))
							return
						default:
							_, _ = c.Write([]byte("250 OK\r\n"))
						}
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), &froms, &body, func() {
		_ = ln.Close()
		<-done
	}
}

func TestMigrateDBIfNeeded(t *testing.T) {
	ctx := context.Background()
	legacyDir := t.TempDir()
	targetDir := t.TempDir()
	legacyDB := filepath.Join(legacyDir, "maildiver.db")
	targetDB := filepath.Join(targetDir, "maildiver.db")

	// Create a real WAL-mode SQLite DB in legacyDir with a contact row.
	st, err := OpenStore(legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertContact(ctx, backend.ContactUpsertRequest{
		Contact: backend.ContactRecord{DisplayName: "WAL Test", Email: "wal@example.org"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	migrateDBIfNeeded(legacyDB, targetDB)

	if _, err := os.Stat(targetDB); err != nil {
		t.Fatalf("expected migrated DB in targetDir: %v", err)
	}
	if _, err := os.Stat(legacyDB); !os.IsNotExist(err) {
		t.Fatalf("legacy DB should have been removed after migration, stat err=%v", err)
	}
	st2, err := OpenStore(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := st2.SearchContacts(ctx, "wal@example.org")
	if err != nil || len(recs) != 1 || recs[0].DisplayName != "WAL Test" {
		t.Fatalf("expected migrated WAL data in target DB, got %+v err=%v", recs, err)
	}
	_ = st2.Close()

	// Never overwrite an already-existing target DB.
	stOld, err := OpenStore(legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	_ = stOld.Close()
	migrateDBIfNeeded(legacyDB, targetDB)
	st3, err := OpenStore(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	recs, err = st3.SearchContacts(ctx, "wal@example.org")
	if err != nil || len(recs) != 1 {
		t.Fatalf("migration must not overwrite existing target DB, got %+v err=%v", recs, err)
	}
}

func TestDraftReplaceReopenBCCAttachmentsAndDeleteOnSend(t *testing.T) {
	ctx := context.Background()
	eng, acc, _, _, smtpBody, cleanup := setupTestEngineWithIMAPAndSMTP(t, nil)
	defer cleanup()

	attFile := filepath.Join(t.TempDir(), "angebot.txt")
	if err := os.WriteFile(attFile, []byte("Anhang-Inhalt 123"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Save initial draft with BCC + attachment
	raw1, err := eng.Call(ctx, "compose.draft", backend.ComposeRequest{
		AccountID:   acc.ID,
		To:          []string{"alice@example.org"},
		BCC:         []string{"hidden@example.org"},
		Subject:     "Entwurf v1",
		Body:        "Erster Text",
		Attachments: []string{attFile},
	})
	if err != nil {
		t.Fatalf("first compose.draft failed: %v", err)
	}
	res1 := backend.ParseComposeResult(raw1)
	if res1.DraftID == "" {
		t.Fatalf("expected DraftID from first save, got %s", raw1)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 1 {
		t.Fatalf("expected 1 draft after first save, got %d", n)
	}

	// 2. Reopen draft via messages.get: BCC and attachment must be restored
	detailRaw, err := eng.Call(ctx, "messages.get", map[string]any{"id": res1.DraftID, "format": "both"})
	if err != nil {
		t.Fatalf("messages.get draft failed: %v", err)
	}
	var detail backend.MessageDetail
	if err := json.Unmarshal(detailRaw, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.BCC) != 1 || detail.BCC[0] != "hidden@example.org" {
		t.Fatalf("expected BCC restored on draft, got %+v", detail.BCC)
	}
	if len(detail.Attachments) != 1 || detail.Attachments[0].Name != "angebot.txt" {
		t.Fatalf("expected attachment restored on draft, got %+v", detail.Attachments)
	}

	// 3. Re-save updated draft with DraftMessageID + SourceAttachments: must replace, NOT duplicate
	raw2, err := eng.Call(ctx, "compose.draft", backend.ComposeRequest{
		AccountID:      acc.ID,
		DraftMessageID: res1.DraftID,
		To:             []string{"alice@example.org"},
		BCC:            []string{"hidden@example.org"},
		Subject:        "Entwurf v2",
		Body:           "Aktualisierter Text",
		SourceAttachments: []backend.SourceAttachment{{
			MessageID: detail.ID,
			PartName:  detail.Attachments[0].PartName,
			Name:      detail.Attachments[0].Name,
		}},
	})
	if err != nil {
		t.Fatalf("second compose.draft failed: %v", err)
	}
	res2 := backend.ParseComposeResult(raw2)
	if res2.DraftID == "" || res2.DraftID == res1.DraftID {
		t.Fatalf("expected fresh DraftID on replace, got %q vs %q", res2.DraftID, res1.DraftID)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 1 {
		t.Fatalf("re-saving draft must replace previous copy (want 1, got %d)", n)
	}

	// 4. Send the draft with DraftMessageID: draft must be deleted after SMTP send & BCC not leaked
	_, err = eng.Call(ctx, "compose.send", backend.ComposeRequest{
		AccountID:      acc.ID,
		DraftMessageID: res2.DraftID,
		To:             []string{"alice@example.org"},
		BCC:            []string{"hidden@example.org"},
		Subject:        "Entwurf v2",
		Body:           "Finaler Text",
		BodyHTML:       "<p>Finaler <b>HTML</b> Text</p>",
		SourceAttachments: []backend.SourceAttachment{{
			MessageID: res2.DraftID,
			PartName:  detail.Attachments[0].PartName,
			Name:      detail.Attachments[0].Name,
		}},
	})
	if err != nil {
		t.Fatalf("compose.send from draft failed: %v", err)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 0 {
		t.Fatalf("sent draft must be deleted from Drafts (want 0, got %d)", n)
	}
	if strings.Contains(*smtpBody, "hidden@example.org") {
		t.Fatalf("sent SMTP message must not leak BCC header: %s", *smtpBody)
	}
	if !strings.Contains(*smtpBody, "multipart/alternative") || !strings.Contains(*smtpBody, "angebot.txt") {
		t.Fatalf("expected multipart/alternative + carried-over attachment in sent mail: %s", *smtpBody)
	}
}

func TestBatchActionsMarkAllReadServerSearchUndoAndAliases(t *testing.T) {
	ctx := context.Background()
	aliases := []config.AccountAlias{{Email: "sales@example.org", DisplayName: "Sales Team"}}
	eng, acc, _, smtpFrom, _, cleanup := setupTestEngineWithIMAPAndSMTP(t, aliases)
	defer cleanup()

	// Verify aliases appear in accounts.list and are used when sending.
	accRaw, err := eng.Call(ctx, "accounts.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var accResp struct {
		Accounts []backend.Account `json:"accounts"`
	}
	_ = json.Unmarshal(accRaw, &accResp)
	if len(accResp.Accounts) != 1 || len(accResp.Accounts[0].Identities) != 2 {
		t.Fatalf("expected primary + 1 alias identity, got %+v", accResp.Accounts)
	}
	if accResp.Accounts[0].Identities[1].Email != "sales@example.org" {
		t.Fatalf("unexpected alias identity: %+v", accResp.Accounts[0].Identities[1])
	}
	if _, err := eng.Call(ctx, "compose.send", backend.ComposeRequest{
		AccountID:  acc.ID,
		IdentityID: accResp.Accounts[0].Identities[1].ID,
		To:         []string{"customer@example.org"},
		Subject:    "From Alias",
		Body:       "Hi",
	}); err != nil {
		t.Fatalf("send with alias failed: %v", err)
	}
	if len(*smtpFrom) == 0 || !strings.Contains((*smtpFrom)[len(*smtpFrom)-1], "sales@example.org") {
		t.Fatalf("expected SMTP MAIL FROM to use alias sales@example.org, got %+v", *smtpFrom)
	}
	// Spoofed unconfigured FromEmail must fall back to primary email.
	gotFrom, _ := resolveOutgoingIdentity(acc, backend.ComposeRequest{FromEmail: "spoof@evil.test"})
	if gotFrom != acc.Email {
		t.Fatalf("spoofed alias must not be accepted, got %q", gotFrom)
	}

	// Append 3 unread messages directly on IMAP server (not yet body-fetched).
	c, err := dialIMAP(ctx, acc, "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC1123Z)
	appendRaw := func(subj, msgID, body string) {
		raw := fmt.Sprintf("From: sender@example.org\r\nTo: me@example.org\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\n\r\n%s\r\n", subj, msgID, now, body)
		cmd := c.Append("INBOX", int64(len(raw)), nil)
		_, _ = cmd.Write([]byte(raw))
		_ = cmd.Close()
		_, _ = cmd.Wait()
	}
	appendRaw("Mail A", "ma@test", "Unbekanntes Geheimwort: ZEPPELIN99")
	appendRaw("Mail B", "mb@test", "Alltagstext B")
	appendRaw("Mail C", "mc@test", "Alltagstext C")
	_ = c.Close()

	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}

	// Verify folder unread counts in folders.list.
	fRaw, err := eng.Call(ctx, "folders.list", map[string]any{"accountId": acc.ID})
	if err != nil {
		t.Fatal(err)
	}
	var fResp struct {
		Folders []backend.Folder `json:"folders"`
	}
	_ = json.Unmarshal(fRaw, &fResp)
	inboxUnread := -1
	inboxID := ""
	for _, f := range fResp.Folders {
		if f.Type == "inbox" {
			inboxUnread = f.UnreadCount
			inboxID = f.ID
		}
	}
	if inboxUnread != 3 {
		t.Fatalf("expected INBOX unreadCount=3, got %d", inboxUnread)
	}

	// Force body_fetched=0 on Mail A to simulate an older mail whose body isn't local yet,
	// then verify messages.searchServer finds "ZEPPELIN99" on the IMAP server.
	_, _ = eng.store.db.ExecContext(ctx, `UPDATE messages SET body_text='', body_fetched=0`)
	_, _ = eng.store.db.ExecContext(ctx, `DELETE FROM messages_fts`)

	srvRaw, err := eng.Call(ctx, "messages.searchServer", map[string]any{
		"accountId": acc.ID,
		"folderId":  inboxID,
		"q":         "ZEPPELIN99",
	})
	if err != nil {
		t.Fatalf("messages.searchServer failed: %v", err)
	}
	var srvPage backend.MessagesPage
	_ = json.Unmarshal(srvRaw, &srvPage)
	if len(srvPage.Messages) != 1 || srvPage.Messages[0].Subject != "Mail A" {
		t.Fatalf("expected server search to find Mail A by body keyword, got %+v", srvPage.Messages)
	}

	// Test messages.markAllRead on INBOX.
	marRaw, err := eng.Call(ctx, "messages.markAllRead", map[string]any{
		"accountId": acc.ID,
		"folderId":  inboxID,
	})
	if err != nil {
		t.Fatalf("messages.markAllRead failed: %v", err)
	}
	var marResp struct {
		Updated int `json:"updated"`
	}
	_ = json.Unmarshal(marRaw, &marResp)
	if marResp.Updated != 3 {
		t.Fatalf("expected markAllRead updated=3, got %d", marResp.Updated)
	}
	fRaw2, _ := eng.Call(ctx, "folders.list", map[string]any{"accountId": acc.ID})
	var fResp2 struct {
		Folders []backend.Folder `json:"folders"`
	}
	_ = json.Unmarshal(fRaw2, &fResp2)
	for _, f := range fResp2.Folders {
		if f.ID == inboxID && f.UnreadCount != 0 {
			t.Fatalf("expected INBOX unreadCount=0 after markAllRead, got %d", f.UnreadCount)
		}
	}

	// Test messages.batchActions (Archive 2 messages in one IMAP connection) + messages.undoMove!
	listRaw, _ := eng.Call(ctx, "messages.list", map[string]any{"accountId": acc.ID, "folderId": inboxID})
	var listPage backend.MessagesPage
	_ = json.Unmarshal(listRaw, &listPage)
	if len(listPage.Messages) != 3 {
		t.Fatalf("expected 3 messages in INBOX, got %d", len(listPage.Messages))
	}
	batchRaw, err := eng.Call(ctx, "messages.batchActions", backend.BatchActionsRequest{
		Items: []backend.BatchActionItem{
			{ID: listPage.Messages[0].ID, ExpectedMessageID: listPage.Messages[0].MessageID},
			{ID: listPage.Messages[1].ID, ExpectedMessageID: listPage.Messages[1].MessageID},
		},
		Action: backend.MessageActionsRequest{Archive: true},
	})
	if err != nil {
		t.Fatalf("messages.batchActions failed: %v", err)
	}
	var batchResp backend.BatchActionsResponse
	_ = json.Unmarshal(batchRaw, &batchResp)
	if len(batchResp.OKIDs) != 2 || batchResp.Failed != 0 || len(batchResp.MovedTargetPath) != 2 {
		t.Fatalf("unexpected batchActions response: %+v", batchResp)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 1 {
		t.Fatalf("expected 1 message left in INBOX after batch archive, got %d", n)
	}

	// Undo the move back to INBOX!
	undoRaw, err := eng.Call(ctx, "messages.undoMove", backend.UndoMoveRequest{
		Items: []backend.UndoMoveItem{
			{
				AccountID:      acc.ID,
				MessageID:      listPage.Messages[0].MessageID,
				FromFolderPath: "INBOX",
				ToFolderPath:   batchResp.MovedTargetPath[listPage.Messages[0].ID],
			},
			{
				AccountID:      acc.ID,
				MessageID:      listPage.Messages[1].MessageID,
				FromFolderPath: "INBOX",
				ToFolderPath:   batchResp.MovedTargetPath[listPage.Messages[1].ID],
			},
		},
	})
	if err != nil {
		t.Fatalf("messages.undoMove failed: %v", err)
	}
	var undoResp backend.UndoMoveResponse
	_ = json.Unmarshal(undoRaw, &undoResp)
	if undoResp.Restored != 2 {
		t.Fatalf("expected 2 messages restored by undoMove, got %+v", undoResp)
	}
	if n := countFolder(t, eng, acc.ID, "INBOX"); n != 3 {
		t.Fatalf("expected all 3 messages back in INBOX after undoMove, got %d", n)
	}
}

func TestExtractInlineCIDImageAndSeparateFromAttachments(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	raw := []byte("From: a@example.org\r\nTo: b@example.org\r\nSubject: Inline Pic\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=\"rel\"\r\n\r\n" +
		"--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Hi <img src=\"cid:logo@acme\" alt=\"Logo\"></p>\r\n" +
		"--rel\r\nContent-Type: image/png; name=\"logo.png\"\r\nContent-ID: <logo@acme>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + b64 + "\r\n" +
		"--rel\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\nContent-Disposition: attachment; filename=\"doc.pdf\"\r\n\r\nPDFDATA\r\n" +
		"--rel--\r\n")

	parsed, err := ParseRawMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Name != "doc.pdf" {
		t.Fatalf("inline cid image should not clutter regular attachments, got %+v", parsed.Attachments)
	}
	ct, data, err := ExtractInlineImage(raw, "LOGO@acme")
	if err != nil {
		t.Fatalf("ExtractInlineImage failed: %v", err)
	}
	if ct != "image/png" || string(data) != string(pngBytes) {
		t.Fatalf("unexpected inline image: ct=%q len=%d", ct, len(data))
	}

	// Inline SVG must be rejected for security.
	svgRaw := []byte("From: a@example.org\r\nTo: b@example.org\r\nSubject: SVG\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=\"rel\"\r\n\r\n" +
		"--rel\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:x@svg\">\r\n" +
		"--rel\r\nContent-Type: image/svg+xml\r\nContent-ID: <x@svg>\r\n\r\n<svg onload=alert(1)></svg>\r\n--rel--\r\n")
	if imgs := ExtractAllInlineImages(svgRaw); len(imgs) != 0 {
		t.Fatalf("SVG inline images must be rejected, got %+v", imgs)
	}
}

func TestReviewSecurityAndSaveAccountPreservation(t *testing.T) {
	ctx := context.Background()
	aliases := []config.AccountAlias{{Email: "sales@example.org", DisplayName: "Sales", Signature: "Beste Grüße,\nSales"}}
	eng, acc, _, _, _, cleanup := setupTestEngineWithIMAPAndSMTP(t, aliases)
	defer cleanup()

	// 1. SaveAccount must preserve SyncDays, SyncLimit, and per-alias Signature when form omits them.
	acc.SyncDays = 180
	acc.SyncLimit = 500
	if _, err := eng.SaveAccount(ctx, acc, ""); err != nil {
		t.Fatal(err)
	}
	edited := acc
	edited.SyncDays = 0
	edited.SyncLimit = 0
	edited.Aliases = []config.AccountAlias{{Email: "sales@example.org", DisplayName: "Sales Team", Signature: ""}}
	saved, err := eng.SaveAccount(ctx, edited, "")
	if err != nil {
		t.Fatal(err)
	}
	if saved.SyncDays != 180 || saved.SyncLimit != 500 {
		t.Fatalf("SaveAccount wiped SyncDays/SyncLimit: got %d/%d", saved.SyncDays, saved.SyncLimit)
	}
	if len(saved.Aliases) != 1 || saved.Aliases[0].Signature != "Beste Grüße,\nSales" {
		t.Fatalf("SaveAccount wiped alias signature: got %+v", saved.Aliases)
	}

	// 2. deleteDraftIfExists must refuse to delete a message in INBOX (non-draft folder).
	c, err := dialIMAP(ctx, acc, "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := "From: sender@example.org\r\nTo: me@example.org\r\nSubject: Keep Me\r\nMessage-ID: <keep@test>\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\n\r\nImportant\r\n"
	cmd := c.Append("INBOX", int64(len(raw)), nil)
	_, _ = cmd.Write([]byte(raw))
	_ = cmd.Close()
	_, _ = cmd.Wait()
	_ = c.Close()

	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	inbox, ok := eng.store.FindFolderByType(ctx, acc.ID, "inbox")
	if !ok {
		t.Fatal("missing inbox")
	}
	msgs, _, _ := eng.store.ListMessages(ctx, MessageQuery{AccountID: acc.ID, FolderID: inbox.ID})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox mail, got %d", len(msgs))
	}
	eng.deleteDraftIfExists(ctx, acc.ID, msgs[0].ID)
	if _, err := eng.store.GetMessage(ctx, msgs[0].ID); err != nil {
		t.Fatalf("deleteDraftIfExists must NEVER delete non-draft INBOX mail: %v", err)
	}

	// 3. SSRF feed IP denylist must cover CGNAT, 0.0.0.0/8, benchmarking, NAT64.
	for _, blocked := range []string{"100.64.0.1", "0.0.0.1", "198.18.0.1", "240.0.0.1", "64:ff9b::1", "127.0.0.1", "169.254.169.254"} {
		if !isBlockedFeedIP(net.ParseIP(blocked)) {
			t.Fatalf("expected %s to be blocked by isBlockedFeedIP", blocked)
		}
	}
	if isBlockedFeedIP(net.ParseIP("93.184.216.34")) {
		t.Fatal("public IP 93.184.216.34 should be allowed")
	}

	// 4. Local filters inspect INBOX and active INBOX/* subfolders, but skip terminal */done/Trash/Archive folders.
	if !isFilterSourceFolder(StoredFolder{Path: "INBOX", Type: "inbox"}) {
		t.Fatal("INBOX must be a filter source folder")
	}
	if !isFilterSourceFolder(StoredFolder{Path: "INBOX/ops"}) {
		t.Fatal("INBOX/ops must be a filter source folder")
	}
	for _, skip := range []StoredFolder{
		{Path: "INBOX/ops/done"},
		{Path: "INBOX/atlassian/done"},
		{Path: "Archives", Type: "archive"},
		{Path: "Trash", Type: "trash"},
		{Path: "Sent", Type: "sent"},
	} {
		if isFilterSourceFolder(skip) {
			t.Fatalf("terminal folder %q must NOT be a filter source folder", skip.Path)
		}
	}
}

func TestTrashMarksUnreadMessagesRead(t *testing.T) {
	ctx := context.Background()
	eng, acc, _, _, _, cleanup := setupTestEngineWithIMAPAndSMTP(t, nil)
	defer cleanup()

	c, err := dialIMAP(ctx, acc, "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC1123Z)
	for _, m := range []struct{ subj, mid string }{
		{"Unread Single Trash", "trash-single@test"},
		{"Unread Batch Trash 1", "trash-batch-1@test"},
		{"Unread Batch Trash 2", "trash-batch-2@test"},
	} {
		raw := fmt.Sprintf("From: s@example.org\r\nTo: me@example.org\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\n\r\nBody\r\n", m.subj, m.mid, now)
		cmd := c.Append("INBOX", int64(len(raw)), nil)
		_, _ = cmd.Write([]byte(raw))
		_ = cmd.Close()
		_, _ = cmd.Wait()
	}
	_ = c.Close()

	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	msgs, _, err := eng.store.ListMessages(ctx, MessageQuery{AccountID: acc.ID, FolderID: FolderID(acc.ID, "INBOX"), Limit: 20})
	if err != nil || len(msgs) != 3 {
		t.Fatalf("expected 3 INBOX messages, got %d (err=%v)", len(msgs), err)
	}

	// 1. Single trash via messages.actions
	if _, err := eng.Call(ctx, "messages.actions", map[string]any{
		"id":    msgs[0].ID,
		"trash": true,
	}); err != nil {
		t.Fatalf("single trash failed: %v", err)
	}

	// 2. Batch trash via messages.batchActions
	if _, err := eng.Call(ctx, "messages.batchActions", backend.BatchActionsRequest{
		Items: []backend.BatchActionItem{
			{ID: msgs[1].ID},
			{ID: msgs[2].ID},
		},
		Action: backend.MessageActionsRequest{Trash: true},
	}); err != nil {
		t.Fatalf("batch trash failed: %v", err)
	}

	// Sync again and verify Trash has all 3 messages with 0 unread.
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	trashFolder, ok := eng.store.FindFolderByType(ctx, acc.ID, "trash")
	if !ok {
		t.Fatal("expected Trash folder")
	}
	trashMsgs, _, err := eng.store.ListMessages(ctx, MessageQuery{AccountID: acc.ID, FolderID: trashFolder.ID, Limit: 20})
	if err != nil || len(trashMsgs) != 3 {
		t.Fatalf("expected 3 messages in Trash, got %d (err=%v)", len(trashMsgs), err)
	}
	for _, tm := range trashMsgs {
		if !tm.Read {
			t.Fatalf("trashed message %q must be marked read in Trash", tm.Subject)
		}
	}
}
