package mailengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/smime"
	"github.com/diver80/maildiver/internal/triage"
)

// Call implements bridge.RPCClient so bridge.Server can serve every OmaThunder endpoint
// directly from the in-process IMAP/SMTP + SQLite + iCal + Google Calendar engine.
func (e *Engine) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "accounts.list":
		return e.rpcAccountsList(ctx)
	case "folders.list":
		return e.rpcFoldersList(ctx, params)
	case "messages.list":
		return e.rpcMessagesList(ctx, params)
	case "messages.get":
		return e.rpcMessagesGet(ctx, params)
	case "messages.open":
		return marshalJSON(map[string]any{"ok": true})
	case "messages.thread":
		return e.rpcMessagesThread(ctx, params)
	case "messages.conversation":
		return e.rpcMessagesConversation(ctx, params)
	case "messages.actions":
		return e.rpcMessagesActions(ctx, params)
	case "messages.batchActions":
		return e.rpcMessagesBatchActions(ctx, params)
	case "messages.markAllRead":
		return e.rpcMessagesMarkAllRead(ctx, params)
	case "messages.searchServer":
		return e.rpcMessagesSearchServer(ctx, params)
	case "messages.undoMove":
		return e.rpcMessagesUndoMove(ctx, params)
	case "compose.draft":
		return e.rpcComposeDraft(ctx, params)
	case "compose.send":
		return e.rpcComposeSend(ctx, params)
	case "calendar.list":
		return e.rpcCalendarList(ctx)
	case "calendar.events":
		return e.rpcCalendarEvents(ctx, params)
	case "calendar.create":
		return e.rpcCalendarCreate(ctx, params)
	case "calendar.update":
		return e.rpcCalendarUpdate(ctx, params)
	case "calendar.delete":
		return e.rpcCalendarDelete(ctx, params)
	case "contacts.search":
		return e.rpcContactsSearch(ctx, params)
	case "contacts.upsert":
		return e.rpcContactsUpsert(ctx, params)
	case "filters.list":
		return e.rpcFiltersList(ctx, params)
	case "filters.upsert":
		return e.rpcFiltersUpsert(ctx, params)
	case "filters.run":
		return e.rpcFiltersRun(ctx, params)
	case "filters.openDialog":
		return marshalJSON(map[string]any{"supported": true, "opened": false, "directMode": true})
	default:
		return nil, fmt.Errorf("unknown rpc method: %s", method)
	}
}

func decodeParams(params any, out any) error {
	if params == nil {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func marshalJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	return json.RawMessage(b), err
}

func (e *Engine) rpcAccountsList(_ context.Context) (json.RawMessage, error) {
	cfg := e.loadConfig()
	accounts := make([]backend.Account, 0, len(cfg.Direct.Accounts))
	for _, a := range cfg.Direct.Accounts {
		if !a.Enabled {
			continue
		}
		ids := []backend.AccountIdentity{{
			ID:    a.ID + "-id",
			Email: a.Email,
			Name:  a.DisplayName,
		}}
		for i, al := range a.Aliases {
			name := al.DisplayName
			if name == "" {
				name = a.DisplayName
			}
			ids = append(ids, backend.AccountIdentity{
				ID:    fmt.Sprintf("%s-alias-%d", a.ID, i),
				Email: al.Email,
				Name:  name,
			})
		}
		accounts = append(accounts, backend.Account{
			ID:         a.ID,
			Name:       a.Name,
			Type:       "imap",
			Identities: ids,
		})
	}
	return marshalJSON(map[string]any{"accounts": accounts})
}

func (e *Engine) rpcFoldersList(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	_ = decodeParams(params, &p)
	cfg := e.loadConfig()
	accNames := map[string]string{}
	for _, a := range cfg.Direct.Accounts {
		accNames[a.ID] = a.Name
	}
	stored, err := e.store.ListFolders(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	out := make([]backend.Folder, 0, len(stored))
	for _, f := range stored {
		out = append(out, backend.Folder{
			ID:          f.ID,
			AccountID:   f.AccountID,
			AccountName: accNames[f.AccountID],
			Name:        f.Name,
			Path:        "/" + strings.TrimPrefix(f.Path, "/"),
			Type:        f.Type,
			SpecialUse:  f.SpecialUse,
			UnreadCount: f.UnreadCount,
		})
	}
	return marshalJSON(map[string]any{"folders": out})
}

func (e *Engine) rpcMessagesList(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID       string `json:"accountId"`
		FolderID        string `json:"folderId"`
		Query           string `json:"q"`
		Unread          *bool  `json:"unread"`
		UnreadOrFlagged bool   `json:"unreadOrFlagged"`
		Days            int    `json:"days"`
		Limit           int    `json:"limit"`
		Cursor          string `json:"cursor"`
	}
	_ = decodeParams(params, &p)
	offset := 0
	if p.Cursor != "" {
		if n, err := strconv.Atoi(p.Cursor); err == nil && n > 0 {
			offset = n
		}
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 80
	}
	list, total, err := e.store.ListMessages(ctx, MessageQuery{
		AccountID:       p.AccountID,
		FolderID:        p.FolderID,
		Query:           p.Query,
		Unread:          p.Unread,
		UnreadOrFlagged: p.UnreadOrFlagged,
		Days:            p.Days,
		Limit:           limit,
		Offset:          offset,
		HeadersOnly:     true,
	})
	if err != nil {
		return nil, err
	}

	inboxIDs := map[string]bool{}
	if folders, ferr := e.store.ListFolders(ctx, p.AccountID); ferr == nil {
		for _, f := range folders {
			if f.Type == "inbox" || strings.EqualFold(f.Path, "INBOX") {
				inboxIDs[f.ID] = true
			}
		}
	}

	threadIDs := make([]string, 0, len(list))
	for _, m := range list {
		threadIDs = append(threadIDs, m.ThreadID)
	}
	sizes, _ := e.store.ThreadSizes(ctx, threadIDs)
	msgs := make([]backend.MessageHeader, 0, len(list))
	for _, m := range list {
		h := storedToHeader(m, inboxIDs[m.FolderID])
		h.ThreadCount = sizes[m.ThreadID]
		msgs = append(msgs, h)
	}
	nextCursor := ""
	if offset+len(msgs) < total {
		nextCursor = strconv.Itoa(offset + len(msgs))
	}
	return marshalJSON(backend.MessagesPage{
		Messages:   msgs,
		Total:      total,
		NextCursor: nextCursor,
	})
}

func storedToHeader(m StoredMessage, inInbox bool) backend.MessageHeader {
	if !inInbox && strings.EqualFold(m.FolderPath, "INBOX") {
		inInbox = true
	}
	return backend.MessageHeader{
		ID:        m.ID,
		MessageID: m.MessageID,
		ThreadID:  m.ThreadID,
		AccountID: m.AccountID,
		FolderID:  m.FolderID,
		Folder:    m.FolderPath,
		InInbox:   inInbox,
		Subject:   m.Subject,
		From:      m.From,
		To:        nonNilStrings(m.To),
		CC:        nonNilStrings(m.CC),
		Date:      m.DateISO,
		Read:      m.Read,
		Unread:    !m.Read,
		Flagged:   m.Flagged,
		Tags:      nonNilStrings(m.Tags),
		Size:      m.Size,
	}
}

func (e *Engine) rpcMessagesGet(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		ID      string `json:"id"`
		Format  string `json:"format"`
		Decrypt bool   `json:"decrypt"`
	}
	_ = decodeParams(params, &p)
	m, err := e.store.GetMessage(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", p.ID)
	}
	if !m.BodyFetched {
		if acc, ok := e.findAccount(m.AccountID); ok {
			if fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m); ferr == nil {
				m = fetched
			}
		}
	}
	hdrs := make(map[string]string, len(m.Headers)+2)
	for k, v := range m.Headers {
		hdrs[k] = v
	}
	if m.InReplyTo != "" && hdrs["in-reply-to"] == "" {
		hdrs["in-reply-to"] = m.InReplyTo
	}
	if m.References != "" && hdrs["references"] == "" {
		hdrs["references"] = m.References
	}
	detail := backend.MessageDetail{
		MessageHeader: storedToHeader(m, strings.EqualFold(m.FolderPath, "INBOX")),
		BCC:           parseAddressList(m.Headers["bcc"]),
		Headers:       hdrs,
		BodyText:      m.BodyText,
		Attachments:   m.Attachments,
		Security:      backend.MessageSecurity{Decryption: "none"},
		HasICS:        m.HasICS && strings.TrimSpace(m.ICSBody) != "",
	}
	if p.Format == "html" || p.Format == "both" {
		detail.BodyHtml = m.BodyHTML
	}

	// S/MIME decryption & signature verification on raw RFC 5322 bytes (plaintext never persisted to SQLite).
	if len(m.RawEML) > 0 && e.smime != nil {
		e.applySMIMERead(m.RawEML, p.Decrypt, p.Format, &detail)
	}

	if detail.HasICS && detail.ICSInvite == nil && strings.TrimSpace(m.ICSBody) != "" {
		if evs, perr := ParseICS(LocalCalendarID, "Persönlich", false, strings.NewReader(m.ICSBody)); perr == nil && len(evs) > 0 {
			ev := storedEventToBridge(evs[0])
			ev.Organizer, ev.Attendees = ParseICSInviteMeta(m.ICSBody)
			detail.ICSInvite = &ev
		}
	}
	return marshalJSON(detail)
}

func (e *Engine) applySMIMERead(rawEML []byte, allowDecrypt bool, format string, detail *backend.MessageDetail) {
	// Quick detection via ParseRawMessage flags / headers
	parsed, _ := ParseRawMessage(rawEML)
	if parsed.Encrypted {
		detail.Security.Encrypted = true
		if !allowDecrypt {
			detail.Security.Decryption = "skipped"
			detail.BodyText = ""
			detail.BodyHtml = ""
			detail.Attachments = nil
			return
		}
		decrypted, vr, derr := smime.DecryptMIME(rawEML, e.smime.AllIdentities(), nil)
		if derr != nil {
			detail.Security.SMIME = true
			detail.Security.Decryption = "fail"
			detail.Security.Error = derr.Error()
			return
		}
		if len(decrypted) > 0 {
			detail.Security.SMIME = true
			detail.Security.Decryption = "success"
			if dp, perr := ParseRawMessage(decrypted); perr == nil {
				detail.BodyText = dp.BodyText
				if format == "html" || format == "both" {
					detail.BodyHtml = dp.BodyHTML
				}
				detail.Attachments = dp.Attachments
				if strings.TrimSpace(dp.ICSBody) != "" {
					detail.HasICS = true
					if evs, ierr := ParseICS(LocalCalendarID, "Persönlich", false, strings.NewReader(dp.ICSBody)); ierr == nil && len(evs) > 0 {
						ev := storedEventToBridge(evs[0])
						ev.Organizer, ev.Attendees = ParseICSInviteMeta(dp.ICSBody)
						detail.ICSInvite = &ev
					}
				}
			}
			if vr != nil {
				applySMIMEVerifyToSecurity(vr, &detail.Security)
				if vr.Valid && vr.SignerCert != nil {
					e.smime.CacheContactCert(vr.SignerCert)
				}
			}
			return
		}
		detail.Security.Decryption = "fail"
		return
	}

	if parsed.Signed || strings.Contains(strings.ToLower(string(rawEML[:min(len(rawEML), 2048)])), "pkcs7") {
		vr, opaqueInner, _ := smime.VerifyMIME(rawEML, nil)
		if vr != nil {
			applySMIMEVerifyToSecurity(vr, &detail.Security)
			if vr.Valid && vr.SignerCert != nil {
				e.smime.CacheContactCert(vr.SignerCert)
			}
			if len(opaqueInner) > 0 && strings.TrimSpace(detail.BodyText) == "" {
				if dp, perr := ParseRawMessage(opaqueInner); perr == nil {
					detail.BodyText = dp.BodyText
					if format == "html" || format == "both" {
						detail.BodyHtml = dp.BodyHTML
					}
					if len(dp.Attachments) > 0 {
						detail.Attachments = dp.Attachments
					}
				}
			}
		} else if parsed.Signed {
			detail.Security.Signed = true
		}
	}
}

func applySMIMEVerifyToSecurity(vr *smime.VerifyResult, sec *backend.MessageSecurity) {
	sec.Signed = true
	sec.SMIME = true
	sec.SignatureValid = vr.Valid
	sec.SignatureTrusted = vr.Trusted
	sec.SignerEmail = vr.SignerEmail
	sec.SignerSubject = vr.SignerSubject
	sec.SignerIssuer = vr.Issuer
	sec.SignatureError = vr.Error
}

func (e *Engine) rpcMessagesThread(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	_ = decodeParams(params, &p)
	m, err := e.store.GetMessage(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", p.ID)
	}
	list, err := e.store.ListThreadMessages(ctx, m.ThreadID, p.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]triage.MessageInput, 0, len(list))
	seen := map[string]bool{}
	for _, tm := range list {
		if key := tm.MessageID; key != "" {
			if seen[key] {
				continue // the same mail in another folder
			}
			seen[key] = true
		}
		out = append(out, triage.MessageInput{
			ID:        tm.ID,
			ThreadID:  tm.ThreadID,
			AccountID: tm.AccountID,
			Folder:    tm.FolderPath,
			Subject:   tm.Subject,
			From:      tm.From,
			To:        tm.To,
			Date:      tm.DateISO,
			BodyText:  tm.BodyText,
			Unread:    !tm.Read,
		})
	}
	return marshalJSON(map[string]any{"messages": out})
}

// rpcMessagesConversation returns a mail's conversation for the reading pane: newest first,
// each mail once, headers and the start of the text (no full bodies).
func (e *Engine) rpcMessagesConversation(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	_ = decodeParams(params, &p)
	m, err := e.store.GetMessage(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", p.ID)
	}
	list, err := e.store.ConversationMessages(ctx, m.ThreadID, p.Limit)
	if err != nil {
		return nil, err
	}
	out := backend.Conversation{ThreadID: m.ThreadID, Messages: make([]backend.ConversationMessage, 0, len(list))}
	for _, cm := range list {
		out.Messages = append(out.Messages, backend.ConversationMessage{
			MessageHeader: storedToHeader(cm, false),
			Snippet:       conversationSnippet(cm.BodyText),
		})
	}
	return marshalJSON(out)
}

// conversationSnippet is the first words of a mail's own text: quoted lines and blank lines
// left out, whitespace collapsed.
func conversationSnippet(body string) string {
	var parts []string
	n := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") {
			continue
		}
		parts = append(parts, line)
		n += len(line)
		if n > 220 {
			break
		}
	}
	s := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}

func (e *Engine) rpcMessagesActions(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		ID                string   `json:"id"`
		ExpectedMessageID string   `json:"expectedMessageId"`
		Read              *bool    `json:"read"`
		Flagged           *bool    `json:"flagged"`
		AddTags           []string `json:"addTags"`
		RemoveTags        []string `json:"removeTags"`
		MoveToFolderID    string   `json:"moveToFolderId"`
		MoveToPath        string   `json:"moveToPath"`
		Trash             bool     `json:"trash"`
		Archive           bool     `json:"archive"`
	}
	_ = decodeParams(params, &p)
	m, err := e.store.GetMessage(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", p.ID)
	}
	if p.ExpectedMessageID != "" && m.MessageID != "" && p.ExpectedMessageID != m.MessageID {
		return nil, fmt.Errorf("message_changed: message-id mismatch")
	}

	var moveTarget string
	permanentDelete := false
	switch {
	case p.Trash:
		if f, ok := e.store.FindFolderByType(ctx, m.AccountID, "trash"); ok {
			if strings.EqualFold(f.Path, m.FolderPath) {
				permanentDelete = true
			} else {
				moveTarget = f.Path
			}
		} else {
			// Never delete for good just because no trash folder is known.
			return nil, fmt.Errorf("not_found: kein Papierkorb-Ordner gefunden – bitte in den Kontoeinstellungen prüfen")
		}
	case p.Archive:
		if f, ok := e.store.FindFolderByType(ctx, m.AccountID, "archive"); ok {
			moveTarget = f.Path
		} else if f, ok := e.store.FindFolderByIDOrPath(ctx, m.AccountID, "Archive"); ok {
			moveTarget = f.Path
		} else {
			// First archive on a server without an archive folder: create it (the next sync lists
			// it; a full sync here would wait for a running one).
			acc, ok := e.findAccount(m.AccountID)
			if !ok {
				return nil, fmt.Errorf("not_found: account %q", m.AccountID)
			}
			if err := e.createFolderIMAP(ctx, acc, "Archive"); err != nil {
				return nil, fmt.Errorf("Archiv-Ordner anlegen fehlgeschlagen: %w", err)
			}
			moveTarget = "Archive"
		}
	case p.MoveToFolderID != "":
		if f, ok := e.store.FindFolderByIDOrPath(ctx, m.AccountID, p.MoveToFolderID); ok {
			moveTarget = f.Path
		} else {
			return nil, fmt.Errorf("not_found: folder %q", p.MoveToFolderID)
		}
	case p.MoveToPath != "":
		moveTarget = e.resolveTargetFolderPath(ctx, m.AccountID, p.MoveToPath)
	}

	if acc, ok := e.findAccount(m.AccountID); ok {
		if p.Read != nil || p.Flagged != nil || moveTarget != "" || permanentDelete {
			if err := e.applyMessageActionIMAP(ctx, acc, m, p.Read, p.Flagged, moveTarget, permanentDelete); err != nil {
				return nil, err
			}
		}
	}

	moved := moveTarget != "" && !strings.EqualFold(moveTarget, m.FolderPath)
	if moved || permanentDelete {
		_ = e.store.DeleteMessage(ctx, m.ID)
	} else {
		_ = e.store.UpdateMessageLocal(ctx, m.ID, p.Read, p.Flagged, p.AddTags, p.RemoveTags)
	}
	e.emitMailSync(m.AccountID)
	resp := map[string]any{"ok": true}
	if moved {
		resp["movedTargetPath"] = moveTarget
	}
	return marshalJSON(resp)
}

func (e *Engine) rpcMessagesBatchActions(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.BatchActionsRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	resp := backend.BatchActionsResponse{
		OK:              true,
		MovedTargetPath: map[string]string{},
	}
	if len(req.Items) == 0 {
		return marshalJSON(resp)
	}

	type batchKey struct {
		accountID  string
		folderPath string
		moveTarget string
		permDel    bool
	}
	type accGroup struct {
		acc    config.DirectAccount
		hasAcc bool
		ops    []folderBatchOp
		keys   map[batchKey]int
	}
	groups := map[string]*accGroup{}
	var accOrder []string

	recordErr := func(err error) {
		resp.Failed++
		if resp.FirstError == "" && err != nil {
			resp.FirstError = err.Error()
		}
	}

	p := req.Action
	archiveCreated := map[string]bool{}

	for _, item := range req.Items {
		m, err := e.store.GetMessage(ctx, item.ID)
		if err != nil {
			recordErr(fmt.Errorf("not_found: message %q", item.ID))
			continue
		}
		if item.ExpectedMessageID != "" && m.MessageID != "" && item.ExpectedMessageID != m.MessageID {
			recordErr(fmt.Errorf("message_changed: message-id mismatch"))
			continue
		}

		var moveTarget string
		permanentDelete := false
		switch {
		case p.Trash:
			if f, ok := e.store.FindFolderByType(ctx, m.AccountID, "trash"); ok {
				if strings.EqualFold(f.Path, m.FolderPath) {
					permanentDelete = true
				} else {
					moveTarget = f.Path
				}
			} else {
				recordErr(fmt.Errorf("not_found: kein Papierkorb-Ordner gefunden – bitte in den Kontoeinstellungen prüfen"))
				continue
			}
		case p.Archive:
			if f, ok := e.store.FindFolderByType(ctx, m.AccountID, "archive"); ok {
				moveTarget = f.Path
			} else if f, ok := e.store.FindFolderByIDOrPath(ctx, m.AccountID, "Archive"); ok {
				moveTarget = f.Path
			} else {
				if !archiveCreated[m.AccountID] {
					acc, ok := e.findAccount(m.AccountID)
					if !ok {
						recordErr(fmt.Errorf("not_found: account %q", m.AccountID))
						continue
					}
					if err := e.createFolderIMAP(ctx, acc, "Archive"); err != nil {
						recordErr(fmt.Errorf("Archiv-Ordner anlegen fehlgeschlagen: %w", err))
						continue
					}
					archiveCreated[m.AccountID] = true
				}
				moveTarget = "Archive"
			}
		case p.MoveToFolderID != "":
			if f, ok := e.store.FindFolderByIDOrPath(ctx, m.AccountID, p.MoveToFolderID); ok {
				moveTarget = f.Path
			} else {
				recordErr(fmt.Errorf("not_found: folder %q", p.MoveToFolderID))
				continue
			}
		case p.MoveToPath != "":
			moveTarget = e.resolveTargetFolderPath(ctx, m.AccountID, p.MoveToPath)
		}

		g, ok := groups[m.AccountID]
		if !ok {
			acc, hasAcc := e.findAccount(m.AccountID)
			g = &accGroup{acc: acc, hasAcc: hasAcc, keys: map[batchKey]int{}}
			groups[m.AccountID] = g
			accOrder = append(accOrder, m.AccountID)
		}
		k := batchKey{
			accountID:  m.AccountID,
			folderPath: m.FolderPath,
			moveTarget: moveTarget,
			permDel:    permanentDelete,
		}
		idx, exists := g.keys[k]
		if !exists {
			idx = len(g.ops)
			g.keys[k] = idx
			g.ops = append(g.ops, folderBatchOp{
				FolderPath:      m.FolderPath,
				Read:            p.Read,
				Flagged:         p.Flagged,
				MoveTargetPath:  moveTarget,
				PermanentDelete: permanentDelete,
			})
		}
		g.ops[idx].Messages = append(g.ops[idx].Messages, m)
	}

	for _, accID := range accOrder {
		g := groups[accID]
		var errs []error
		if g.hasAcc && (p.Read != nil || p.Flagged != nil || p.RemovesFromFolder()) {
			errs = e.applyBatchActionsIMAP(ctx, g.acc, g.ops)
		} else {
			errs = make([]error, len(g.ops))
		}
		anyOK := false
		for i, op := range g.ops {
			if errs[i] != nil {
				for range op.Messages {
					recordErr(errs[i])
				}
				continue
			}
			moved := op.MoveTargetPath != "" && !strings.EqualFold(op.MoveTargetPath, op.FolderPath)
			for _, m := range op.Messages {
				if moved || op.PermanentDelete {
					_ = e.store.DeleteMessage(ctx, m.ID)
					if moved {
						resp.MovedTargetPath[m.ID] = op.MoveTargetPath
					}
				} else {
					_ = e.store.UpdateMessageLocal(ctx, m.ID, p.Read, p.Flagged, p.AddTags, p.RemoveTags)
				}
				resp.OKIDs = append(resp.OKIDs, m.ID)
				anyOK = true
			}
		}
		if anyOK {
			e.emitMailSync(accID)
		}
	}
	if len(resp.MovedTargetPath) == 0 {
		resp.MovedTargetPath = nil
	}
	return marshalJSON(resp)
}

func (e *Engine) rpcMessagesMarkAllRead(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID string `json:"accountId"`
		FolderID  string `json:"folderId"`
	}
	_ = decodeParams(params, &p)

	var folders []StoredFolder
	if p.FolderID != "" {
		all, err := e.store.ListFolders(ctx, p.AccountID)
		if err != nil {
			return nil, err
		}
		for _, f := range all {
			if f.ID == p.FolderID || strings.EqualFold(f.Path, strings.TrimPrefix(p.FolderID, "/")) {
				folders = append(folders, f)
				break
			}
		}
		if len(folders) == 0 {
			return nil, fmt.Errorf("not_found: folder %q", p.FolderID)
		}
	} else {
		var err error
		folders, err = e.store.UnreadFolders(ctx, p.AccountID)
		if err != nil {
			return nil, err
		}
	}

	byAcc := map[string][]StoredFolder{}
	var order []string
	for _, f := range folders {
		if _, ok := byAcc[f.AccountID]; !ok {
			order = append(order, f.AccountID)
		}
		byAcc[f.AccountID] = append(byAcc[f.AccountID], f)
	}

	updated := 0
	var firstErr error
	for _, accID := range order {
		flist := byAcc[accID]
		paths := make([]string, 0, len(flist))
		byPath := make(map[string]StoredFolder, len(flist))
		for _, f := range flist {
			paths = append(paths, f.Path)
			byPath[f.Path] = f
		}
		succeededPaths := paths
		if acc, ok := e.findAccount(accID); ok {
			var imapErr error
			succeededPaths, _, imapErr = e.markFoldersReadIMAP(ctx, acc, paths)
			if imapErr != nil && firstErr == nil {
				firstErr = imapErr
			}
		}
		anyLocal := false
		for _, fp := range succeededPaths {
			if f, ok := byPath[fp]; ok {
				n, _ := e.store.MarkFolderReadLocal(ctx, f.ID)
				updated += n
				anyLocal = true
			}
		}
		if anyLocal {
			e.emitMailSync(accID)
		}
	}
	if firstErr != nil && updated == 0 {
		return nil, firstErr
	}
	return marshalJSON(map[string]any{"ok": firstErr == nil, "updated": updated})
}

func (e *Engine) rpcMessagesSearchServer(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID string `json:"accountId"`
		FolderID  string `json:"folderId"`
		Query     string `json:"q"`
		Limit     int    `json:"limit"`
	}
	_ = decodeParams(params, &p)
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return marshalJSON(backend.MessagesPage{Messages: []backend.MessageHeader{}})
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	cfg := e.loadConfig()
	var accs []config.DirectAccount
	for _, a := range cfg.Direct.Accounts {
		if !a.Enabled {
			continue
		}
		if p.AccountID != "" && a.ID != p.AccountID {
			continue
		}
		accs = append(accs, a)
	}

	var matched []StoredMessage
	seen := map[string]bool{}
	var firstErr error
	for _, acc := range accs {
		if ctx.Err() != nil || len(matched) >= limit {
			break
		}
		allFolders, err := e.store.ListFolders(ctx, acc.ID)
		if err != nil {
			continue
		}
		var targetFolders []StoredFolder
		if p.FolderID != "" {
			for _, f := range allFolders {
				if f.ID == p.FolderID || strings.EqualFold(f.Path, strings.TrimPrefix(p.FolderID, "/")) {
					targetFolders = append(targetFolders, f)
					break
				}
			}
		} else {
			for _, f := range allFolders {
				if f.Type == "trash" || f.Type == "junk" {
					continue
				}
				targetFolders = append(targetFolders, f)
			}
			sort.SliceStable(targetFolders, func(i, j int) bool {
				return folderSyncPriority(targetFolders[i]) < folderSyncPriority(targetFolders[j])
			})
		}
		if len(targetFolders) == 0 {
			continue
		}
		found, err := e.searchServerIMAP(ctx, acc, targetFolders, q, limit-len(matched))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, sm := range found {
			if !seen[sm.ID] {
				seen[sm.ID] = true
				matched = append(matched, sm)
			}
		}
	}
	if len(matched) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].DateUnix > matched[j].DateUnix })

	threadIDs := make([]string, 0, len(matched))
	for _, m := range matched {
		threadIDs = append(threadIDs, m.ThreadID)
	}
	sizes, _ := e.store.ThreadSizes(ctx, threadIDs)
	out := make([]backend.MessageHeader, 0, len(matched))
	for _, m := range matched {
		h := storedToHeader(m, strings.EqualFold(m.FolderPath, "INBOX"))
		h.ThreadCount = sizes[m.ThreadID]
		out = append(out, h)
	}
	return marshalJSON(backend.MessagesPage{
		Messages: out,
		Total:    len(out),
	})
}

func (e *Engine) rpcMessagesUndoMove(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.UndoMoveRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	byAcc := map[string][]backend.UndoMoveItem{}
	var order []string
	for _, it := range req.Items {
		if it.AccountID == "" {
			continue
		}
		if _, ok := byAcc[it.AccountID]; !ok {
			order = append(order, it.AccountID)
		}
		byAcc[it.AccountID] = append(byAcc[it.AccountID], it)
	}
	totalRestored := 0
	for _, accID := range order {
		acc, ok := e.findAccount(accID)
		if !ok {
			continue
		}
		n, err := e.undoMoveIMAP(ctx, acc, byAcc[accID])
		if err != nil {
			return nil, err
		}
		totalRestored += n
		if n > 0 {
			// Re-sync only the affected folders so restored messages reappear immediately without a slow full-account sync.
			var affected []string
			for _, it := range byAcc[accID] {
				if it.FromFolderPath != "" {
					affected = append(affected, it.FromFolderPath)
				}
				if it.ToFolderPath != "" {
					affected = append(affected, it.ToFolderPath)
				}
			}
			_ = e.syncSpecificFoldersIMAP(ctx, acc, affected)
		}
	}
	return marshalJSON(backend.UndoMoveResponse{OK: true, Restored: totalRestored})
}

func (e *Engine) pickOutgoingAccount(accountID string) (config.DirectAccount, error) {
	cfg := e.loadConfig()
	if accountID != "" {
		for _, a := range cfg.Direct.Accounts {
			if a.ID == accountID && a.Enabled {
				return a, nil
			}
		}
	}
	for _, a := range cfg.Direct.Accounts {
		if a.Enabled {
			return a, nil
		}
	}
	return config.DirectAccount{}, fmt.Errorf("kein aktives E-Mail-Konto konfiguriert")
}

const (
	maxOutgoingAttachmentBytes      = 25 * 1024 * 1024
	maxOutgoingTotalAttachmentBytes = 35 * 1024 * 1024
	maxOutgoingHTMLBytes            = 2 * 1024 * 1024
)

func isPathWithinDir(dir, target string) bool {
	if dir == "" {
		return false
	}
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolvedDir
	}
	rel, err := filepath.Rel(dir, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isBlockedSensitiveAttachmentPath(p string) bool {
	absPath, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		absPath = resolved
	}
	base := strings.ToLower(filepath.Base(absPath))
	for _, dirFn := range []func() (string, error){config.ConfigDir, config.LegacyConfigDir} {
		if cfgDir, err := dirFn(); err == nil && isPathWithinDir(cfgDir, absPath) {
			if base == "secrets.json" || base == "env" || base == "bridge.json" {
				return true
			}
		}
	}
	if dataDir, err := config.DataDir(); err == nil && isPathWithinDir(dataDir, absPath) {
		if base == "maildiver.db" || base == "maildiver.db-wal" || base == "maildiver.db-shm" {
			return true
		}
	}
	return false
}

// resolveOutgoingIdentity picks the sender email and display name from acc and its configured aliases.
// Only configured addresses on acc are accepted so callers cannot spoof arbitrary From addresses.
func resolveOutgoingIdentity(acc config.DirectAccount, req backend.ComposeRequest) (fromEmail, fromName string) {
	fromEmail = strings.TrimSpace(acc.Email)
	fromName = strings.TrimSpace(acc.DisplayName)
	if id := strings.TrimSpace(req.IdentityID); id != "" {
		if id == acc.ID+"-id" {
			return fromEmail, fromName
		}
		for i, al := range acc.Aliases {
			if id == fmt.Sprintf("%s-alias-%d", acc.ID, i) {
				name := strings.TrimSpace(al.DisplayName)
				if name == "" {
					name = fromName
				}
				return strings.TrimSpace(al.Email), name
			}
		}
	}
	if want := strings.TrimSpace(req.FromEmail); want != "" {
		if strings.EqualFold(want, acc.Email) {
			return fromEmail, fromName
		}
		for _, al := range acc.Aliases {
			if strings.EqualFold(want, al.Email) {
				name := strings.TrimSpace(al.DisplayName)
				if name == "" {
					name = fromName
				}
				return strings.TrimSpace(al.Email), name
			}
		}
	}
	return fromEmail, fromName
}

func (e *Engine) buildComposeRaw(ctx context.Context, acc config.DirectAccount, req backend.ComposeRequest, includeBcc bool) (fromEmail, msgID string, raw []byte, err error) {
	if len(req.BodyHTML) > maxOutgoingHTMLBytes {
		return "", "", nil, fmt.Errorf("HTML-Inhalt überschreitet das Limit von 2 MB")
	}
	fromEmail, fromName := resolveOutgoingIdentity(acc, req)
	inReplyTo := cleanMsgID(req.InReplyTo)
	refs := strings.TrimSpace(req.References)
	if req.ReplyToMessageID != "" {
		if orig, err := e.store.GetMessage(ctx, req.ReplyToMessageID); err == nil && orig.MessageID != "" {
			inReplyTo = orig.MessageID
			if orig.References != "" {
				refs = orig.References + " <" + orig.MessageID + ">"
			} else {
				refs = "<" + orig.MessageID + ">"
			}
		}
	}
	var atts []OutgoingAttachment
	var totalBytes int64
	// Carried-over attachments from a reopened draft or forwarded email.
	rawCache := map[string][]byte{}
	for _, sa := range req.SourceAttachments {
		if sa.MessageID == "" {
			continue
		}
		srcRaw, ok := rawCache[sa.MessageID]
		if !ok {
			m, gerr := e.store.GetMessage(ctx, sa.MessageID)
			if gerr != nil {
				return "", "", nil, fmt.Errorf("Quell-Nachricht für Anhang %q nicht gefunden", sa.Name)
			}
			if len(m.RawEML) == 0 {
				if srcAcc, aok := e.findAccount(m.AccountID); aok {
					if fetched, ferr := e.fetchMessageBodyIMAP(ctx, srcAcc, m); ferr == nil {
						m = fetched
					}
				}
			}
			srcRaw = m.RawEML
			rawCache[sa.MessageID] = srcRaw
		}
		name, ct, data, eerr := ExtractAttachment(srcRaw, sa.PartName, sa.Name)
		if eerr != nil {
			return "", "", nil, fmt.Errorf("Anhang %q konnte nicht übernommen werden: %w", sa.Name, eerr)
		}
		if int64(len(data)) > maxOutgoingAttachmentBytes {
			return "", "", nil, fmt.Errorf("Anhang %q überschreitet 25 MB", name)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxOutgoingTotalAttachmentBytes {
			return "", "", nil, fmt.Errorf("Gesamtgröße aller Anhänge überschreitet 35 MB")
		}
		atts = append(atts, OutgoingAttachment{
			Filename:    name,
			ContentType: ct,
			Data:        data,
		})
	}
	for _, rawPath := range req.Attachments {
		p := expandHomePath(rawPath)
		if p == "" {
			continue
		}
		if isBlockedSensitiveAttachmentPath(p) {
			return "", "", nil, fmt.Errorf("Anhang %q ist eine geschützte MailDiver-Geheimdatei und kann nicht versendet werden", filepath.Base(p))
		}
		data, err := readBoundedRegularFile(p, maxOutgoingAttachmentBytes)
		if err != nil {
			return "", "", nil, fmt.Errorf("Anhang %q nicht lesbar: %w", filepath.Base(p), err)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxOutgoingTotalAttachmentBytes {
			return "", "", nil, fmt.Errorf("Gesamtgröße aller Anhänge überschreitet 35 MB")
		}
		atts = append(atts, OutgoingAttachment{
			Filename: filepath.Base(p),
			Data:     data,
		})
	}
	bodyHTML := req.BodyHTML
	if strings.TrimSpace(bodyHTML) == "" && !req.IsHTML && strings.TrimSpace(req.Body) != "" {
		bodyHTML = BuildStyledEmailHTML(req.Body)
	}
	var inlineImgs []OutgoingInlineImage
	if strings.TrimSpace(bodyHTML) != "" {
		bodyHTML, inlineImgs = ExtractInlineDataURLImages(bodyHTML)
	}
	msgID, raw = BuildRawMessage(OutgoingMessage{
		FromName:         fromName,
		FromEmail:        fromEmail,
		To:               req.To,
		CC:               req.CC,
		BCC:              req.BCC,
		IncludeBccHeader: includeBcc,
		Subject:          req.Subject,
		Body:             req.Body,
		BodyHTML:         bodyHTML,
		IsHTML:           req.IsHTML,
		InlineImages:     inlineImgs,
		Attachments:      atts,
		InReplyTo:        inReplyTo,
		References:       refs,
	})

	// Apply S/MIME signing & encryption on outgoing sends (drafts stay editable until sent).
	if !includeBcc && e.smime != nil {
		wantSign := req.Sign == "on" || (req.Sign == "" && e.smime.HasIdentity(fromEmail))
		if wantSign {
			id, lerr := e.smime.LoadIdentity(fromEmail)
			if lerr != nil {
				if req.Sign == "on" {
					return "", "", nil, lerr
				}
			} else {
				signedRaw, serr := smime.SignMIME(raw, id)
				if serr != nil {
					return "", "", nil, serr
				}
				raw = signedRaw
			}
		}
		if req.Encrypt {
			rcptAddrs := ExtractAddresses(req.To, req.CC, req.BCC)
			certs, missing := e.smime.LookupRecipientCerts(rcptAddrs)
			if len(missing) > 0 {
				return "", "", nil, fmt.Errorf("S/MIME-Verschlüsselung nicht möglich — fehlendes Zertifikat für: %s", strings.Join(missing, ", "))
			}
			// Also include sender's own cert if available so the Sent folder copy remains readable.
			if selfCerts, _ := e.smime.LookupRecipientCerts([]string{fromEmail}); len(selfCerts) > 0 {
				certs = append(certs, selfCerts...)
			}
			encRaw, eerr := smime.EncryptMIME(raw, certs)
			if eerr != nil {
				return "", "", nil, eerr
			}
			raw = encRaw
		}
	}
	return fromEmail, msgID, raw, nil
}

// deleteDraftIfExists removes a superseded or sent draft from IMAP and local SQLite (best-effort).
// Refuses to delete messages outside the account's Drafts folder.
func (e *Engine) deleteDraftIfExists(ctx context.Context, accountID, draftID string) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return
	}
	m, err := e.store.GetMessage(ctx, draftID)
	if err != nil {
		return
	}
	if accountID != "" && m.AccountID != accountID {
		return
	}
	// Safety check: only ever permanently delete messages that reside in a Drafts folder.
	isDraftFolder := false
	if f, ok := e.store.FindFolderByIDOrPath(ctx, m.AccountID, m.FolderPath); ok {
		if strings.EqualFold(f.Type, "drafts") || slices.Contains(f.SpecialUse, "drafts") {
			isDraftFolder = true
		}
	}
	if !isDraftFolder {
		base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(m.FolderPath, "\\", "/"))))
		if base == "drafts" || base == "entwürfe" || base == "entwuerfe" {
			isDraftFolder = true
		}
	}
	if !isDraftFolder {
		return
	}
	if acc, ok := e.findAccount(m.AccountID); ok && m.UID > 0 {
		if err := e.applyMessageActionIMAP(ctx, acc, m, nil, nil, "", true); err != nil {
			// Keep local copy so UI doesn't hide an un-deleted draft on IMAP.
			return
		}
	}
	_ = e.store.DeleteMessage(ctx, m.ID)
}

func (e *Engine) rpcComposeDraft(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.ComposeRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	idemKey := ""
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		idemKey = "draft:" + strings.TrimSpace(req.IdempotencyKey)
		if prev, ok := e.store.GetIdempotency(ctx, idemKey); ok {
			return prev, nil
		}
	}
	acc, err := e.pickOutgoingAccount(req.AccountID)
	if err != nil {
		return nil, err
	}
	_, msgID, raw, err := e.buildComposeRaw(ctx, acc, req, true)
	if err != nil {
		return nil, err
	}
	draftsPath := "Drafts"
	draftsFolderID := FolderID(acc.ID, draftsPath)
	if f, ok := e.store.FindFolderByType(ctx, acc.ID, "drafts"); ok {
		draftsPath = f.Path
		draftsFolderID = f.ID
	}
	uid, err := e.appendMessageIMAP(ctx, acc, draftsPath, raw, []imap.Flag{imap.FlagDraft, imap.FlagSeen}, msgID)
	if err != nil {
		return nil, err
	}
	newDraftID := ""
	if uid > 0 {
		now := time.Now().UTC()
		sm := StoredMessage{
			ID:         MessageStoreID(acc.ID, draftsPath, uid),
			AccountID:  acc.ID,
			FolderID:   draftsFolderID,
			FolderPath: draftsPath,
			UID:        uid,
			MessageID:  msgID,
			DateISO:    now.Format(time.RFC3339),
			DateUnix:   now.Unix(),
			Read:       true,
			Size:       int64(len(raw)),
		}
		mergeParsedIntoStored(&sm, raw)
		if uerr := e.store.UpsertMessage(ctx, sm); uerr == nil {
			newDraftID = sm.ID
		}
	}
	// Only remove the previous draft AFTER the new draft is safely stored on IMAP.
	if req.DraftMessageID != "" && req.DraftMessageID != newDraftID {
		e.deleteDraftIfExists(ctx, acc.ID, req.DraftMessageID)
	}
	e.emitMailSync(acc.ID)

	res, _ := marshalJSON(map[string]any{"ok": true, "messageId": msgID, "draftId": newDraftID, "folder": draftsPath, "windowOpened": false})
	if idemKey != "" {
		e.store.PutIdempotency(ctx, idemKey, res)
	}
	return res, nil
}

func (e *Engine) rpcComposeSend(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.ComposeRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	idemKey := ""
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		idemKey = "send:" + strings.TrimSpace(req.IdempotencyKey)
		if prev, ok := e.store.GetIdempotency(ctx, idemKey); ok {
			return prev, nil
		}
	}
	acc, err := e.pickOutgoingAccount(req.AccountID)
	if err != nil {
		return nil, err
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return nil, fmt.Errorf("kein Passwort für %s gespeichert", acc.Email)
	}
	if bad := InvalidAddresses(req.To, req.CC, req.BCC); len(bad) > 0 {
		return nil, fmt.Errorf("invalid_argument: keine gültige E-Mail-Adresse: %s", strings.Join(bad, ", "))
	}
	recipients := ExtractAddresses(req.To, req.CC, req.BCC)
	if len(recipients) == 0 {
		return nil, fmt.Errorf("invalid_argument: bitte mindestens einen Empfänger angeben")
	}
	fromEmail, msgID, raw, err := e.buildComposeRaw(ctx, acc, req, false)
	if err != nil {
		return nil, err
	}
	if err := SendSMTP(ctx, acc, pass, fromEmail, recipients, raw); err != nil {
		if isTransientNetworkError(err) {
			obID := "ob-" + randomHex(8)
			if qerr := e.store.EnqueueOutbox(ctx, OutboxItem{
				ID:         obID,
				AccountID:  acc.ID,
				FromEmail:  fromEmail,
				Recipients: recipients,
				Subject:    req.Subject,
				RawEML:     raw,
				LastError:  err.Error(),
			}); qerr == nil {
				res, _ := marshalJSON(map[string]any{"ok": true, "queued": true, "outboxId": obID, "messageId": msgID})
				if idemKey != "" {
					e.store.PutIdempotency(ctx, idemKey, res)
				}
				return res, nil
			}
		}
		return nil, err
	}
	// Post-SMTP housekeeping uses a detached short timeout so client cancellation right after
	// SMTP acceptance cannot skip idempotency persistence or leave stale drafts behind.
	postCtx, postCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer postCancel()

	// Best-effort APPEND to Sent folder.
	sentPath := "Sent"
	if f, ok := e.store.FindFolderByType(postCtx, acc.ID, "sent"); ok {
		sentPath = f.Path
	}
	_, sentErr := e.appendMessageIMAP(postCtx, acc, sentPath, raw, []imap.Flag{imap.FlagSeen}, "")
	// Clean up the draft that was just sent.
	if req.DraftMessageID != "" {
		e.deleteDraftIfExists(postCtx, acc.ID, req.DraftMessageID)
	}
	e.emitMailSync(acc.ID)

	res, _ := marshalJSON(map[string]any{"ok": true, "messageId": msgID, "sent": true, "sentCopyFailed": sentErr != nil})
	if idemKey != "" {
		e.store.PutIdempotency(postCtx, idemKey, res)
	}
	return res, nil
}

// --- Calendar RPCs ---

func (e *Engine) rpcCalendarList(ctx context.Context) (json.RawMessage, error) {
	stored, err := e.store.ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]backend.Calendar, 0, len(stored))
	for _, c := range stored {
		out = append(out, backend.Calendar{
			ID:       c.ID,
			Name:     c.Name,
			Type:     c.Source,
			ReadOnly: c.ReadOnly,
			Color:    c.Color,
		})
	}
	return marshalJSON(map[string]any{"supported": true, "calendars": out})
}

func (e *Engine) rpcCalendarEvents(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		Start       string   `json:"start"`
		End         string   `json:"end"`
		CalendarIDs []string `json:"calendarIds"`
		Limit       int      `json:"limit"`
	}
	_ = decodeParams(params, &p)
	rawEvents, err := e.store.ListRawEvents(ctx, p.CalendarIDs)
	if err != nil {
		return nil, err
	}
	var startT, endT time.Time
	if p.Start != "" {
		startT = parseFlexibleEventTime(p.Start, 0)
	}
	if p.End != "" {
		endT = parseFlexibleEventTime(p.End, 0)
	}
	expanded := ExpandEventsInWindow(rawEvents, startT, endT, p.Limit)
	if expanded == nil {
		expanded = []backend.CalendarEvent{}
	}
	return marshalJSON(backend.CalendarEventsResponse{
		Supported: true,
		Events:    expanded,
	})
}

func (e *Engine) rpcCalendarCreate(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.CreateCalendarEventRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Start) == "" {
		return nil, fmt.Errorf("invalid_argument: Titel und Startzeit sind erforderlich")
	}
	calID := req.CalendarID
	if calID == "" {
		calID = LocalCalendarID
	}
	cal, err := e.store.GetCalendar(ctx, calID)
	if err != nil {
		return nil, fmt.Errorf("not_found: calendar %q", calID)
	}
	if cal.ReadOnly {
		return nil, fmt.Errorf("read_only: Kalender %q ist schreibgeschützt", cal.Name)
	}

	var se StoredEvent
	if cal.Source == "google" {
		se, err = e.createGoogleEvent(ctx, cal, req)
		if err != nil {
			return nil, err
		}
	} else {
		st := parseFlexibleEventTime(req.Start, time.Now().Unix())
		en := parseFlexibleEventTime(req.End, st.Add(time.Hour).Unix())
		if !en.After(st) {
			if req.AllDay {
				en = st.AddDate(0, 0, 1)
			} else {
				en = st.Add(time.Hour)
			}
		}
		startISO, endISO := st.Format(time.RFC3339), en.Format(time.RFC3339)
		if req.AllDay {
			startISO, endISO = st.Format("2006-01-02"), en.Format("2006-01-02")
		}
		rruleStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.RecurrenceRule), "RRULE:"))
		uid := fmt.Sprintf("evt-%d@maildiver.local", time.Now().UnixNano())
		se = StoredEvent{
			ID:           cal.ID + "::" + uid,
			CalendarID:   cal.ID,
			CalendarName: cal.Name,
			ExternalID:   uid,
			UID:          uid,
			Title:        strings.TrimSpace(req.Title),
			StartISO:     startISO,
			EndISO:       endISO,
			StartUnix:    st.Unix(),
			EndUnix:      en.Unix(),
			AllDay:       req.AllDay,
			Location:     strings.TrimSpace(req.Location),
			Description:  strings.TrimSpace(req.Description),
			RRule:        rruleStr,
			IsRecurring:  rruleStr != "",
			Status:       "confirmed",
		}
		if err := e.store.UpsertEvent(ctx, se); err != nil {
			return nil, err
		}
	}
	e.emitCalendarChanged()
	return marshalJSON(map[string]any{"event": storedEventToBridge(se)})
}

func (e *Engine) rpcCalendarUpdate(ctx context.Context, params any) (json.RawMessage, error) {
	var p map[string]any
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	calID, _ := p["calendarId"].(string)
	eventID, _ := p["eventId"].(string)
	if eventID == "" {
		eventID, _ = p["id"].(string)
	}
	cal, err := e.store.GetCalendar(ctx, calID)
	if err != nil {
		return nil, fmt.Errorf("not_found: calendar %q", calID)
	}
	if cal.ReadOnly {
		return nil, fmt.Errorf("read_only: calendar %q", cal.Name)
	}
	ev, err := e.store.GetEvent(ctx, calID, eventID)
	if err != nil {
		return nil, fmt.Errorf("not_found: event %q", eventID)
	}
	if v, ok := p["title"].(string); ok {
		ev.Title = strings.TrimSpace(v)
	}
	if v, ok := p["location"].(string); ok {
		ev.Location = strings.TrimSpace(v)
	}
	if v, ok := p["description"].(string); ok {
		ev.Description = strings.TrimSpace(v)
	}
	if v, ok := p["allDay"].(bool); ok {
		ev.AllDay = v
	}
	if v, ok := p["rrule"].(string); ok {
		rr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "RRULE:"))
		ev.RRule = rr
		ev.IsRecurring = rr != ""
	} else if v, ok := p["recurrenceRule"].(string); ok {
		rr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "RRULE:"))
		ev.RRule = rr
		ev.IsRecurring = rr != ""
	}
	if v, ok := p["start"].(string); ok && v != "" {
		st := parseFlexibleEventTime(v, ev.StartUnix)
		ev.StartUnix = st.Unix()
		if ev.AllDay {
			ev.StartISO = st.Format("2006-01-02")
		} else {
			ev.StartISO = st.Format(time.RFC3339)
		}
	}
	if v, ok := p["end"].(string); ok && v != "" {
		en := parseFlexibleEventTime(v, ev.EndUnix)
		ev.EndUnix = en.Unix()
		if ev.AllDay {
			ev.EndISO = en.Format("2006-01-02")
		} else {
			ev.EndISO = en.Format(time.RFC3339)
		}
	}

	if cal.Source == "google" {
		ev, err = e.updateGoogleEvent(ctx, cal, ev)
		if err != nil {
			return nil, err
		}
	} else {
		if err := e.store.UpsertEvent(ctx, ev); err != nil {
			return nil, err
		}
	}
	e.emitCalendarChanged()
	return marshalJSON(map[string]any{"event": storedEventToBridge(ev)})
}

func (e *Engine) rpcCalendarDelete(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		CalendarID   string `json:"calendarId"`
		EventID      string `json:"eventId"`
		ID           string `json:"id"`
		RecurrenceID string `json:"recurrenceId"`
		Scope        string `json:"scope"`
	}
	_ = decodeParams(params, &p)
	if p.EventID == "" {
		p.EventID = p.ID
	}
	cal, err := e.store.GetCalendar(ctx, p.CalendarID)
	if err != nil {
		return nil, fmt.Errorf("not_found: calendar %q", p.CalendarID)
	}
	if cal.ReadOnly {
		return nil, fmt.Errorf("read_only: calendar %q", cal.Name)
	}
	ev, err := e.store.GetEvent(ctx, p.CalendarID, p.EventID)
	if err != nil {
		return nil, fmt.Errorf("not_found: event %q", p.EventID)
	}

	// Deleting a single occurrence of a recurring event never deletes the whole series.
	if ev.RRule != "" && p.RecurrenceID != "" && p.Scope != backend.ScopeSeries {
		if cal.Source == "google" {
			if err := e.deleteGoogleEventOccurrence(ctx, cal, ev, p.RecurrenceID); err != nil {
				return nil, err
			}
			e.emitCalendarChanged()
			return marshalJSON(map[string]any{"ok": true})
		}
		if !slices.Contains(ev.ExDates, p.RecurrenceID) {
			ev.ExDates = append(ev.ExDates, p.RecurrenceID)
		}
		if err := e.store.UpsertEvent(ctx, ev); err != nil {
			return nil, err
		}
		e.emitCalendarChanged()
		return marshalJSON(map[string]any{"ok": true})
	}

	if cal.Source == "google" {
		if err := e.deleteGoogleEvent(ctx, cal, ev); err != nil {
			return nil, err
		}
	} else {
		if err := e.store.DeleteEvent(ctx, p.CalendarID, ev.ID); err != nil {
			return nil, err
		}
	}
	e.emitCalendarChanged()
	return marshalJSON(map[string]any{"ok": true})
}

// --- Contacts RPCs ---

func (e *Engine) rpcContactsSearch(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		Query string `json:"q"`
	}
	_ = decodeParams(params, &p)
	contacts, err := e.store.SearchContacts(ctx, p.Query)
	if err != nil {
		return nil, err
	}
	if contacts == nil {
		contacts = []backend.ContactRecord{}
	}
	return marshalJSON(backend.ContactsSearchResponse{
		Supported: true,
		AddressBooks: []backend.AddressBook{{
			ID:       LocalAddressBookID,
			Name:     LocalAddressBookName,
			ReadOnly: false,
			Type:     "local",
		}},
		Contacts: contacts,
	})
}

func (e *Engine) rpcContactsUpsert(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.ContactUpsertRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	rec, action, err := e.store.UpsertContact(ctx, req)
	if err != nil {
		return nil, err
	}
	return marshalJSON(backend.ContactUpsertResponse{
		OK:      true,
		Action:  action,
		Contact: rec,
	})
}

// --- Filter RPCs ---

func (e *Engine) rpcFiltersList(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID string `json:"accountId"`
	}
	_ = decodeParams(params, &p)
	stored, err := e.store.ListFilters(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	out := make([]triage.NativeFilter, 0, len(stored))
	for _, f := range stored {
		out = append(out, storedFilterToNative(f))
	}
	return marshalJSON(backend.FilterListResponse{
		Supported: true,
		Filters:   out,
	})
}

func (e *Engine) rpcFiltersUpsert(ctx context.Context, params any) (json.RawMessage, error) {
	var req backend.FilterUpsertRequest
	if err := decodeParams(params, &req); err != nil {
		return nil, err
	}
	saved, err := e.store.UpsertFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	return marshalJSON(backend.FilterUpsertResponse{
		OK:     true,
		Filter: storedFilterToNative(saved),
	})
}

func (e *Engine) rpcFiltersRun(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID   string   `json:"accountId"`
		FilterNames []string `json:"filterNames"`
	}
	_ = decodeParams(params, &p)
	acc, ok := e.findAccount(p.AccountID)
	if !ok {
		return nil, fmt.Errorf("not_found: account %q", p.AccountID)
	}
	allFolders, err := e.store.listFoldersMeta(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	var sourceFolders []StoredFolder
	for _, f := range allFolders {
		if isFilterSourceFolder(f) {
			sourceFolders = append(sourceFolders, f)
		}
	}
	if len(sourceFolders) == 0 {
		return marshalJSON(backend.FilterRunResponse{OK: true, Executed: 0})
	}
	filters, err := e.store.ListFilters(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	nameSet := map[string]bool{}
	for _, n := range p.FilterNames {
		nameSet[strings.ToLower(strings.TrimSpace(n))] = true
	}
	type fltKey struct {
		folderPath string
		read       bool
		flagged    bool
		moveTarget string
		permDel    bool
	}
	var ops []folderBatchOp
	opIdx := map[fltKey]int{}

	for _, sf := range sourceFolders {
		msgs, _, merr := e.store.ListMessages(ctx, MessageQuery{
			AccountID: acc.ID,
			FolderID:  sf.ID,
			Limit:     500,
		})
		if merr != nil {
			return nil, merr
		}
		for _, m := range msgs {
			var readSet, flagSet bool
			var moveTarget string
			var permDel bool
			matchedAny := false
			for _, flt := range filters {
				if !flt.Enabled {
					continue
				}
				if len(nameSet) > 0 && !nameSet[strings.ToLower(flt.Name)] {
					continue
				}
				if !matchFilterAgainstMessage(flt, m) {
					continue
				}
				stop := false
				for _, act := range flt.Actions {
					switch act.Type {
					case "markRead":
						readSet = true
						matchedAny = true
					case "addStar":
						flagSet = true
						matchedAny = true
					case "moveToFolder":
						moveTarget = e.resolveTargetFolderPath(ctx, acc.ID, act.TargetFolderPath)
						matchedAny = true
						stop = true
					case "delete":
						permDel = true
						matchedAny = true
						stop = true
					case "stopExecution":
						stop = true
					}
					if stop {
						break
					}
				}
				if stop {
					break
				}
			}
			moved := moveTarget != "" && !strings.EqualFold(moveTarget, m.FolderPath)
			if !matchedAny || (!readSet && !flagSet && !moved && !permDel) {
				continue
			}
			// Skip no-op read/flag updates when message is not moving/deleting and already has those flags.
			if !moved && !permDel && (!readSet || m.Read) && (!flagSet || m.Flagged) {
				continue
			}
			k := fltKey{folderPath: m.FolderPath, read: readSet, flagged: flagSet, moveTarget: moveTarget, permDel: permDel}
			idx, ok := opIdx[k]
			if !ok {
				idx = len(ops)
				opIdx[k] = idx
				var rPtr, fPtr *bool
				if readSet {
					t := true
					rPtr = &t
				}
				if flagSet {
					t := true
					fPtr = &t
				}
				ops = append(ops, folderBatchOp{
					FolderPath:      m.FolderPath,
					Read:            rPtr,
					Flagged:         fPtr,
					MoveTargetPath:  moveTarget,
					PermanentDelete: permDel,
				})
			}
			ops[idx].Messages = append(ops[idx].Messages, m)
		}
	}

	executed := 0
	var firstErr error
	if len(ops) > 0 {
		errs := e.applyBatchActionsIMAP(ctx, acc, ops)
		for i, op := range ops {
			if errs[i] != nil {
				if firstErr == nil {
					firstErr = errs[i]
				}
				continue
			}
			moved := op.MoveTargetPath != "" && !strings.EqualFold(op.MoveTargetPath, op.FolderPath)
			for _, m := range op.Messages {
				if moved || op.PermanentDelete {
					_ = e.store.DeleteMessage(ctx, m.ID)
				} else {
					_ = e.store.UpdateMessageLocal(ctx, m.ID, op.Read, op.Flagged, nil, nil)
				}
				executed++
			}
		}
	}
	if executed > 0 {
		e.emitMailSync(acc.ID)
	}
	if executed == 0 && firstErr != nil {
		return nil, firstErr
	}
	return marshalJSON(backend.FilterRunResponse{OK: true, Executed: executed})
}

func storedFilterToNative(f StoredFilter) triage.NativeFilter {
	nf := triage.NativeFilter{
		Name:       f.Name,
		AccountID:  f.AccountID,
		Index:      f.Position,
		Enabled:    f.Enabled,
		MatchAll:   f.MatchAll,
		Conditions: f.Conditions,
		Actions:    make([]triage.NativeAction, 0, len(f.Actions)),
	}
	for _, a := range f.Actions {
		nf.Actions = append(nf.Actions, triage.NativeAction{
			Type:             a.Type,
			TargetFolderPath: a.TargetFolderPath,
			TargetFolderURI:  a.TargetFolderURI,
		})
	}
	return nf
}

// DeleteFilter removes a local OmaThunder rule by account and name.
func (e *Engine) DeleteFilter(ctx context.Context, accountID, name string) error {
	return e.store.DeleteFilter(ctx, accountID, name)
}

// ToggleFilter enables or disables a local OmaThunder rule.
func (e *Engine) ToggleFilter(ctx context.Context, accountID, name string, enabled bool) error {
	_, err := e.store.UpsertFilter(ctx, backend.FilterUpsertRequest{
		Mode:         "toggle",
		AccountID:    accountID,
		Name:         name,
		ExistingName: name,
		Enabled:      enabled,
	})
	return err
}

// CallHost implements jobs.Caller so triage scans and archive runs work directly in-process.
func (e *Engine) CallHost(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "host.triage.collect":
		return e.rpcHostTriageCollect(ctx, params)
	case "host.archive.candidates":
		return e.rpcHostArchiveCandidates(ctx, params)
	case "host.archive.raw":
		return e.rpcHostArchiveRaw(ctx, params)
	case "host.archive.finish":
		return e.rpcHostArchiveFinish(ctx, params)
	default:
		return nil, fmt.Errorf("not_supported: %s", method)
	}
}

func (e *Engine) rpcHostArchiveCandidates(ctx context.Context, params any) (json.RawMessage, error) {
	var req struct {
		AccountID         string `json:"accountId"`
		Scope             string `json:"scope"`
		FolderPath        string `json:"folderPath"`
		IncludeSubfolders *bool  `json:"includeSubfolders"`
		OlderThanDays     *int   `json:"olderThanDays"`
		OnlyRead          *bool  `json:"onlyRead"`
		ExcludeStarred    *bool  `json:"excludeStarred"`
		MaxMessages       int    `json:"maxMessages"`
	}
	_ = decodeParams(params, &req)
	acc, ok := e.findAccount(req.AccountID)
	if !ok {
		return nil, fmt.Errorf("not_found: account %q", req.AccountID)
	}
	olderDays := 365
	if req.OlderThanDays != nil && *req.OlderThanDays >= 0 {
		olderDays = *req.OlderThanDays
	}
	onlyRead := true
	if req.OnlyRead != nil {
		onlyRead = *req.OnlyRead
	}
	excludeStarred := true
	if req.ExcludeStarred != nil {
		excludeStarred = *req.ExcludeStarred
	}
	maxMsgs := req.MaxMessages
	if maxMsgs <= 0 || maxMsgs > 1000 {
		maxMsgs = 200
	}
	cutoff := time.Now().AddDate(0, 0, -olderDays).Unix()
	folders, err := e.store.ListFolders(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "inbox"
	}
	targetPath := strings.Trim(strings.TrimSpace(req.FolderPath), "/")
	incSub := req.IncludeSubfolders == nil || *req.IncludeSubfolders

	var selected []StoredFolder
	for _, f := range folders {
		if f.Type == "trash" || f.Type == "junk" || f.Type == "drafts" {
			continue
		}
		switch scope {
		case "inbox":
			if f.Type == "inbox" || strings.EqualFold(f.Path, "INBOX") {
				selected = append(selected, f)
			}
		case "folder":
			fp := strings.Trim(f.Path, "/")
			if strings.EqualFold(fp, targetPath) || (incSub && targetPath != "" && strings.HasPrefix(strings.ToLower(fp), strings.ToLower(targetPath)+"/")) {
				selected = append(selected, f)
			}
		default: // "all"
			selected = append(selected, f)
		}
	}

	type cand struct {
		MessageID       string `json:"messageId"`
		HeaderMessageID string `json:"headerMessageId"`
		AccountID       string `json:"accountId"`
		AccountName     string `json:"accountName"`
		FolderID        string `json:"folderId"`
		FolderPath      string `json:"folderPath"`
		Subject         string `json:"subject"`
		From            string `json:"from"`
		Date            string `json:"date"`
		Size            int64  `json:"size"`
		Read            bool   `json:"read"`
		Flagged         bool   `json:"flagged"`
	}
	var cands []cand
	totalMatched := 0
	var totalBytes int64
	for _, f := range selected {
		// Query oldest-first directly in SQL with cutoff & read/flagged filters so large folders
		// (>1000 mails) never miss old archive candidates behind newer emails.
		offset := 0
		for {
			msgs, folderTotal, err := e.store.ListMessages(ctx, MessageQuery{
				AccountID:      acc.ID,
				FolderID:       f.ID,
				BeforeUnix:     cutoff,
				OnlyRead:       onlyRead,
				ExcludeFlagged: excludeStarred,
				OldestFirst:    true,
				Limit:          1000,
				Offset:         offset,
				HeadersOnly:    true,
			})
			if err != nil {
				break
			}
			if offset == 0 {
				totalMatched += folderTotal
			}
			for _, m := range msgs {
				totalBytes += m.Size
				if len(cands) < maxMsgs {
					cands = append(cands, cand{
						MessageID:       m.ID,
						HeaderMessageID: m.MessageID,
						AccountID:       acc.ID,
						AccountName:     acc.Name,
						FolderID:        f.ID,
						FolderPath:      f.Path,
						Subject:         m.Subject,
						From:            m.From,
						Date:            m.DateISO,
						Size:            m.Size,
						Read:            m.Read,
						Flagged:         m.Flagged,
					})
				}
			}
			offset += len(msgs)
			if len(msgs) < 1000 || offset >= folderTotal {
				break
			}
		}
	}
	if cands == nil {
		cands = []cand{}
	}
	return marshalJSON(map[string]any{
		"candidates":       cands,
		"totalMatched":     totalMatched,
		"totalBytesAll":    totalBytes,
		"scannedFolders":   len(selected),
		"truncated":        totalMatched > len(cands),
		"hasLocalAccount":  false,
		"localAccountName": "",
	})
}

func (e *Engine) rpcHostArchiveRaw(ctx context.Context, params any) (json.RawMessage, error) {
	var c struct {
		MessageID string `json:"messageId"`
	}
	_ = decodeParams(params, &c)
	m, err := e.store.GetMessage(ctx, c.MessageID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", c.MessageID)
	}
	if len(m.RawEML) == 0 {
		acc, ok := e.findAccount(m.AccountID)
		if !ok {
			return nil, fmt.Errorf("not_found: account %q", m.AccountID)
		}
		fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m)
		if ferr != nil {
			return nil, ferr
		}
		m = fetched
	}
	return marshalJSON(map[string]any{
		"messageId": m.ID,
		"base64":    base64.StdEncoding.EncodeToString(m.RawEML),
	})
}

func (e *Engine) rpcHostArchiveFinish(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		MessageID string `json:"messageId"`
		Action    string `json:"action"`
		SkipTrash bool   `json:"skipTrash"`
	}
	_ = decodeParams(params, &p)
	m, err := e.store.GetMessage(ctx, p.MessageID)
	if err != nil {
		return nil, fmt.Errorf("not_found: message %q", p.MessageID)
	}
	acc, ok := e.findAccount(m.AccountID)
	if !ok {
		return nil, fmt.Errorf("not_found: account %q", m.AccountID)
	}
	if p.Action == "delete" {
		if p.SkipTrash {
			if err := e.applyMessageActionIMAP(ctx, acc, m, nil, nil, "", true); err != nil {
				return nil, err
			}
		} else {
			trash := e.resolveTargetFolderPath(ctx, acc.ID, "trash")
			if err := e.applyMessageActionIMAP(ctx, acc, m, nil, nil, trash, false); err != nil {
				return nil, err
			}
		}
		_ = e.store.DeleteMessage(ctx, m.ID)
		e.emitMailSync(acc.ID)
	}
	return marshalJSON(map[string]any{"ok": true})
}

// isMessageEncrypted reports whether a cached message carries S/MIME or OpenPGP ciphertext
// whose body and attachment names must never be forwarded to AI triage.
func isMessageEncrypted(m StoredMessage) bool {
	ct := strings.ToLower(strings.TrimSpace(m.Headers["content-type"]))
	if strings.Contains(ct, "multipart/encrypted") ||
		strings.Contains(ct, "application/pkcs7-mime") ||
		strings.Contains(ct, "application/x-pkcs7-mime") {
		return true
	}
	if strings.Contains(m.BodyText, "-----BEGIN PGP MESSAGE-----") {
		return true
	}
	for _, att := range m.Attachments {
		lowName := strings.ToLower(strings.TrimSpace(att.Name))
		lowCT := strings.ToLower(strings.TrimSpace(att.ContentType))
		if lowName == "smime.p7m" || lowName == "encrypted.asc" ||
			strings.Contains(lowCT, "pkcs7-mime") || strings.Contains(lowCT, "pgp-encrypted") {
			return true
		}
	}
	return false
}

func (e *Engine) rpcHostTriageCollect(ctx context.Context, params any) (json.RawMessage, error) {
	var p struct {
		AccountID string `json:"accountId"`
		Limit     int    `json:"limit"`
		Days      int    `json:"days"`
		Max       int    `json:"max"`
	}
	_ = decodeParams(params, &p)
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Max <= 0 {
		p.Max = 200
	}
	cfg := e.loadConfig()
	var msgs []triage.MessageInput
	seen := map[string]bool{}
	scannedFolders := 0

	appendMsg := func(m StoredMessage) {
		if len(msgs) >= p.Max || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		encrypted := isMessageEncrypted(m)
		var attNames []string
		bodyText := m.BodyText
		if encrypted {
			// Never send encrypted mail body or attachment names to AI triage.
			bodyText = ""
		} else {
			for _, att := range m.Attachments {
				attNames = append(attNames, att.Name)
			}
		}
		msgs = append(msgs, triage.MessageInput{
			ID:          m.ID,
			ThreadID:    m.ThreadID,
			AccountID:   m.AccountID,
			Folder:      m.FolderPath,
			FolderID:    m.FolderID,
			InInbox:     true,
			Subject:     m.Subject,
			From:        m.From,
			To:          m.To,
			Date:        m.DateISO,
			Headers:     m.Headers,
			BodyText:    bodyText,
			Unread:      !m.Read,
			Attachments: attNames,
			Encrypted:   encrypted,
		})
	}

	unreadTrue := true
	unreadFalse := false
	for _, acc := range cfg.Direct.Accounts {
		if !acc.Enabled {
			continue
		}
		if p.AccountID != "" && acc.ID != p.AccountID {
			continue
		}
		if !cfg.Mailbox.AccountEnabled(acc.ID) {
			continue
		}
		inbox, ok := e.store.FindFolderByType(ctx, acc.ID, "inbox")
		if !ok {
			continue
		}
		scannedFolders++
		// 1) Collect unread inbox mails first (up to remaining max budget).
		if rem := p.Max - len(msgs); rem > 0 {
			if unreadList, _, err := e.store.ListMessages(ctx, MessageQuery{
				AccountID: acc.ID,
				FolderID:  inbox.ID,
				Unread:    &unreadTrue,
				Limit:     rem,
			}); err == nil {
				for _, m := range unreadList {
					appendMsg(m)
				}
			}
		}
		// 2) Then collect up to p.Limit recent read mails within p.Days.
		if rem := p.Max - len(msgs); rem > 0 {
			readLim := p.Limit
			if readLim > rem {
				readLim = rem
			}
			if readList, _, err := e.store.ListMessages(ctx, MessageQuery{
				AccountID: acc.ID,
				FolderID:  inbox.ID,
				Unread:    &unreadFalse,
				Days:      p.Days,
				Limit:     readLim,
			}); err == nil {
				for _, m := range readList {
					appendMsg(m)
				}
			}
		}
	}
	if msgs == nil {
		msgs = []triage.MessageInput{}
	}
	return marshalJSON(map[string]any{
		"messages":       msgs,
		"scannedFolders": scannedFolders,
	})
}
