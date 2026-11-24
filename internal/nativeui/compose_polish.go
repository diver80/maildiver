package nativeui

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/mailengine"
)

var missingAttachmentKeywordRE = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}_])(anhang|anhänge|anhängen|angehängt|angehangen|anbei|beigefügt|beigefuegt|beiliegend|dateianhang|attached|attachment|attachments|enclosed|enclosing)([^\p{L}\p{N}_]|$)`)

type pendingComposeSend struct {
	Req       backend.ComposeRequest
	Nonce     string
	Restore   composeDraftBackup
	CreatedAt time.Time
	Deadline  time.Time
	Busy      bool
}

type composeDraftBackup struct {
	AccountID         string
	AccountLabel      string
	IdentityID        string
	FromEmail         string
	FromName          string
	DraftID           string
	ReplyTo           string
	ReplyType         string
	InReplyTo         string
	References        string
	To                string
	CC                string
	BCC               string
	Subject           string
	Body              string
	BodyHTML          string
	ForwardHTML       string
	ForwardMsgID      string
	ForwardFrom       string
	ForwardDate       string
	ForwardSubj       string
	ForwardSep        string
	KeepHTML          bool
	Attachments       []string
	SourceAttachments []backend.SourceAttachment
	Sign              int
	Encrypt           bool
	CryptoTech        int
	ReplyEncrypted    bool
}

// composeSignatureParts returns the effective plain signature, optional custom HTML signature,
// and whether the user prefers the signature placed below the reply quote.
func (a *App) composeSignatureParts(accountID, email string) (plain, customHTML string, belowQuote bool) {
	em := strings.ToLower(strings.TrimSpace(email))
	for _, acc := range a.cfg.Direct.Accounts {
		if accountID != "" && acc.ID != accountID {
			continue
		}
		belowQuote = strings.EqualFold(strings.TrimSpace(acc.SignaturePosition), "below")
		plain = strings.TrimSpace(acc.Signature)
		customHTML = strings.TrimSpace(acc.SignatureHTML)
		if em != "" {
			for _, al := range acc.Aliases {
				if strings.EqualFold(strings.TrimSpace(al.Email), em) {
					if sig := strings.TrimSpace(al.Signature); sig != "" {
						plain = sig
					}
					if sh := strings.TrimSpace(al.SignatureHTML); sh != "" {
						customHTML = sh
					}
					break
				}
			}
		}
		if accountID != "" {
			return plain, customHTML, belowQuote
		}
	}
	if plain == "" {
		plain = a.composeAccountSignature(accountID)
	}
	return plain, customHTML, belowQuote
}

// composeSignatureBlockHTMLFromParts produces the editor's [data-mdv-sig="1"] HTML block
// using sanitized custom HTML when present, otherwise styled plain text.
func composeSignatureBlockHTMLFromParts(plain, customHTML string) string {
	if strings.TrimSpace(customHTML) != "" {
		safe := SanitizeEmailHTML(customHTML, nil, false)
		if strings.TrimSpace(safe) != "" {
			if strings.Contains(safe, `data-mdv-sig="1"`) {
				return safe
			}
			return `<div data-mdv-sig="1" style="margin-top:20px;font-size:12.5px;color:#57606a;">` + safe + `</div>`
		}
	}
	return composeSignatureBlockHTML(stripSignatureDelim(plain))
}

func appendAccountSignatureWithQuote(body, sig, quote string, belowQuote bool) string {
	if !belowQuote {
		return appendAccountSignature(body, sig) + quote
	}
	trimmed := strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(quote) != "" {
		trimmed += quote
	}
	return appendAccountSignature(trimmed, sig)
}

// extractComposeUserTextOnly strips trailing quoted replies, forwarded blocks, and RFC 3676
// signatures so attachment-reminder detection never triggers on quoted messages.
func extractComposeUserTextOnly(body string) string {
	norm := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	var out []string
	for _, ln := range strings.Split(norm, "\n") {
		tr := strings.TrimSpace(ln)
		if tr == "--" ||
			strings.HasPrefix(tr, ">") ||
			strings.HasPrefix(tr, "---------- ") ||
			(strings.HasPrefix(tr, "Am ") && strings.Contains(tr, " schrieb ")) ||
			(strings.HasPrefix(tr, "On ") && strings.Contains(tr, " wrote:")) {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// detectMissingAttachmentKeyword scans newly composed text for attachment hints when no file is attached.
func detectMissingAttachmentKeyword(userText string) string {
	m := missingAttachmentKeywordRE.FindStringSubmatch(userText)
	if len(m) >= 3 {
		return m[2]
	}
	return ""
}

func (a *App) captureComposeBackup() composeDraftBackup {
	return composeDraftBackup{
		AccountID:         a.composeAccountID,
		AccountLabel:      a.composeAccountLabel,
		IdentityID:        a.composeIdentityID,
		FromEmail:         a.composeFromEmail,
		FromName:          a.composeFromName,
		DraftID:           a.composeDraftID,
		ReplyTo:           a.composeReplyTo,
		ReplyType:         a.composeReplyType,
		InReplyTo:         a.composeInReplyTo,
		References:        a.composeReferences,
		To:                a.composeTo,
		CC:                a.composeCC,
		BCC:               a.composeBCC,
		Subject:           a.composeSubject,
		Body:              a.composeBody,
		BodyHTML:          a.composeBodyHTML,
		ForwardHTML:       a.composeForwardHTML,
		ForwardMsgID:      a.composeForwardMsgID,
		ForwardFrom:       a.composeForwardFrom,
		ForwardDate:       a.composeForwardDate,
		ForwardSubj:       a.composeForwardSubj,
		ForwardSep:        a.composeForwardSep,
		KeepHTML:          a.composeKeepHTML,
		Attachments:       append([]string(nil), a.composeAttachments...),
		SourceAttachments: append([]backend.SourceAttachment(nil), a.composeSourceAttachments...),
		Sign:              a.composeSign,
		Encrypt:           a.composeEncrypt,
		CryptoTech:        a.composeCryptoTech,
		ReplyEncrypted:    a.composeReplyEncrypted,
	}
}

var (
	htmlBodyOpenRE  = regexp.MustCompile(`(?is)^.*<body\b[^>]*>`)
	htmlBodyCloseRE = regexp.MustCompile(`(?is)</body\s*>.*$`)
)

// extractHTMLBodyInner strips full-document <html><head><body> wrappers so restored HTML
// can be safely injected into the WYSIWYG editor's innerHTML without nesting documents.
func extractHTMLBodyInner(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if htmlBodyOpenRE.MatchString(s) {
		s = htmlBodyOpenRE.ReplaceAllString(s, "")
		s = htmlBodyCloseRE.ReplaceAllString(s, "")
		return strings.TrimSpace(s)
	}
	return s
}

func (a *App) restoreComposeFromBackup(b composeDraftBackup) {
	a.initComposeCommon(b.AccountID)
	a.composeAccountID = b.AccountID
	a.composeAccountLabel = b.AccountLabel
	a.composeIdentityID = b.IdentityID
	a.composeFromEmail = b.FromEmail
	a.composeFromName = b.FromName
	a.composeDraftID = b.DraftID
	a.composeReplyTo = b.ReplyTo
	a.composeReplyType = b.ReplyType
	a.composeInReplyTo = b.InReplyTo
	a.composeReferences = b.References
	a.composeTo = b.To
	a.composeCC = b.CC
	a.composeBCC = b.BCC
	a.composeSubject = b.Subject
	a.composeBody = b.Body
	a.composeBodyHTML = extractHTMLBodyInner(b.BodyHTML)
	a.composeForwardHTML = b.ForwardHTML
	a.composeForwardMsgID = b.ForwardMsgID
	a.composeForwardFrom = b.ForwardFrom
	a.composeForwardDate = b.ForwardDate
	a.composeForwardSubj = b.ForwardSubj
	a.composeForwardSep = b.ForwardSep
	a.composeKeepHTML = b.KeepHTML
	a.composeAttachments = append([]string(nil), b.Attachments...)
	a.composeSourceAttachments = append([]backend.SourceAttachment(nil), b.SourceAttachments...)
	a.composeSign = b.Sign
	a.composeEncrypt = b.Encrypt
	a.composeCryptoTech = b.CryptoTech
	a.composeReplyEncrypted = b.ReplyEncrypted
	a.composeEditorDirty = true
	a.composeDirtyAt = time.Now()
	// Mark baseline distinct from current snapshot so closing asks before discarding unsent mail.
	a.composeBaseline = "__restored__"
	a.composeEditorGen++
}

// checkComposeMissingAttachment arms the "attachment missing?" warning when the text mentions
// an attachment but none is attached (and the user has not confirmed sending anyway).
func (a *App) checkComposeMissingAttachment(req backend.ComposeRequest) bool {
	if a.composeMissingAttAck || len(req.Attachments) > 0 || len(req.SourceAttachments) > 0 {
		return false
	}
	userText := extractComposeUserTextOnly(req.Body)
	if kw := detectMissingAttachmentKeyword(userText); kw != "" {
		a.composeMissingAttWarn = kw
		return true
	}
	return false
}

// requestComposeSend runs missing-attachment guard and either starts the Undo-Send countdown
// or sends immediately when UndoSendSeconds == 0. Never blocks the UI thread on WebView JS eval.
func (a *App) requestComposeSend(ctx context.Context, req backend.ComposeRequest) {
	if a.composeBusy {
		return
	}
	_, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Senden nicht erlaubt (Read-Only oder Offline)", "Sending not allowed (read-only or offline)"), "error")
		return
	}

	sessSeq := a.composeSessionSeq
	dc := a.opts.Direct
	ed := a.composeEditor
	usingWYSIWYG := a.composeUsingWYSIWYG() && ed != nil && ed.Widget() != nil
	needFwdImgs := a.composeKeepHTML && a.composeForwardHTML != "" && req.BodyHTML == "" && dc != nil && a.composeForwardMsgID != ""
	fwdMsgID := a.composeForwardMsgID
	fwdHTML := a.composeForwardHTML
	fwdFrom, fwdDate, fwdSubj, fwdSep := a.composeForwardFrom, a.composeForwardDate, a.composeForwardSubj, a.composeForwardSep
	fwdTitle := a.tr("Weitergeleitete Nachricht", "Forwarded message")

	if usingWYSIWYG || needFwdImgs {
		a.composeBusy = true
		go func() {
			if usingWYSIWYG {
				rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				if htmlOut, textOut, ok := ed.ReadContent(rctx); ok {
					req.BodyHTML = htmlOut
					req.Body = textOut
					req.IsHTML = true
				}
				cancel()
			}
			if needFwdImgs && req.BodyHTML == "" {
				fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				inlineImgs, _ := dc.ReadAllInlineImages(fctx, fwdMsgID)
				cancel()
				req.BodyHTML = BuildForwardHTML(
					extractUserTopText(req.Body, fwdSep),
					fwdTitle, fwdFrom, fwdDate, fwdSubj, fwdHTML, inlineImgs,
				)
			}
			a.emitUpdate(func() {
				a.composeBusy = false
				if !a.composeOpen || a.composeSessionSeq != sessSeq {
					return
				}
				a.finalizeComposeSend(ctx, sessSeq, req)
			})
		}()
		return
	}
	a.finalizeComposeSend(ctx, sessSeq, req)
}

func (a *App) finalizeComposeSend(ctx context.Context, sessSeq uint64, req backend.ComposeRequest) {
	if a.checkComposeMissingAttachment(req) {
		a.composeWarnSendAt = req.SendAt // "Send anyway" keeps a chosen later time
		return
	}
	if req.SendAt > 0 {
		// Scheduled: the outbox is the undo (cancel or back to drafts there), no countdown.
		a.composeConfirm = false
		a.SendCompose(ctx, req)
		return
	}

	delaySec := a.cfg.App.EffectiveUndoSendSeconds()
	if delaySec <= 0 {
		a.SendCompose(ctx, req)
		return
	}

	if a.composeNonce == "" {
		a.composeNonce = backend.NewIdempotencyKey()
	}
	backup := a.captureComposeBackup()
	backup.Body = req.Body
	if req.BodyHTML != "" {
		backup.BodyHTML = req.BodyHTML
	}
	now := time.Now()
	ps := &pendingComposeSend{
		Req:       req,
		Nonce:     a.composeNonce,
		Restore:   backup,
		CreatedAt: now,
		Deadline:  now.Add(time.Duration(delaySec) * time.Second),
	}
	a.pendingSends = append(a.pendingSends, ps)
	a.composeConfirm = false
	a.composeMissingAttWarn = ""
	a.composeMissingAttAck = false
	a.closeCompose()

	go func(nonce string, deadline time.Time) {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			done := false
			a.emitUpdate(func() {
				ps := a.findPendingSend(nonce)
				if ps == nil {
					done = true
					return
				}
				if !time.Now().Before(deadline) {
					done = true
					a.commitPendingSend(context.Background(), nonce)
				}
			})
			if done {
				return
			}
		}
	}(ps.Nonce, ps.Deadline)
}

func (a *App) findPendingSend(nonce string) *pendingComposeSend {
	if len(a.pendingSends) == 0 {
		return nil
	}
	if nonce == "" {
		return a.pendingSends[0]
	}
	for _, ps := range a.pendingSends {
		if ps != nil && ps.Nonce == nonce {
			return ps
		}
	}
	return nil
}

func (a *App) popPendingSend(nonce string) *pendingComposeSend {
	for i, ps := range a.pendingSends {
		if ps != nil && (nonce == "" || ps.Nonce == nonce) {
			a.pendingSends = append(a.pendingSends[:i], a.pendingSends[i+1:]...)
			return ps
		}
	}
	return nil
}

func (a *App) cancelPendingSend(nonces ...string) {
	nonce := ""
	if len(nonces) > 0 {
		nonce = nonces[0]
	}
	ps := a.findPendingSend(nonce)
	if ps == nil || ps.Busy {
		return
	}
	a.popPendingSend(ps.Nonce)
	a.restoreComposeFromBackup(ps.Restore)
	a.toast(a.tr("Senden abgebrochen — E-Mail wieder im Editor", "Send cancelled — email restored in compose"), "info")
}

func (a *App) commitPendingSend(ctx context.Context, nonces ...string) {
	nonce := ""
	if len(nonces) > 0 {
		nonce = nonces[0]
	}
	ps := a.findPendingSend(nonce)
	if ps == nil || ps.Busy {
		return
	}
	ps.Busy = true
	a.popPendingSend(ps.Nonce)
	restoreCopy := ps.Restore
	a.sendPreparedCompose(ctx, ps.Req, ps.Nonce, ps.Restore.ForwardMsgID, ps.Restore.ForwardHTML, ps.Restore.KeepHTML,
		ps.Restore.ForwardFrom, ps.Restore.ForwardDate, ps.Restore.ForwardSubj, ps.Restore.ForwardSep, 0, &restoreCopy)
}

// FlushPendingSendsOnQuit synchronously dispatches any queued Undo-Send emails (or saves them as
// drafts on failure) before the app exits so no email is silently dropped during shutdown.
func (a *App) FlushPendingSendsOnQuit() {
	bc, queue := a.takePendingSends()
	for _, ps := range queue {
		flushPendingSend(bc, ps)
	}
}

// FlushPendingSendsOnSuspend sends the Undo-Send queue at once when a phone app goes to the
// background (UI thread): the queue lives in memory only, and Android may kill a background
// app without a quit. The sends run off the UI thread so the app's pause is not held up.
func (a *App) FlushPendingSendsOnSuspend() {
	bc, queue := a.takePendingSends()
	if len(queue) == 0 {
		return
	}
	go func() {
		failed := 0
		for _, ps := range queue {
			if !flushPendingSend(bc, ps) {
				failed++
			}
		}
		if failed > 0 {
			a.emitUpdate(func() {
				a.toast(a.trf("%d E-Mail(s) im Hintergrund nicht gesendet — als Entwurf gesichert", "%d email(s) not sent in the background — saved as draft", failed), "warning")
			})
		}
	}()
}

// takePendingSends empties the Undo-Send queue for a flush (UI thread).
func (a *App) takePendingSends() (*backend.Client, []*pendingComposeSend) {
	if len(a.pendingSends) == 0 {
		return nil, nil
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		return nil, nil
	}
	var queue []*pendingComposeSend
	for _, ps := range a.pendingSends {
		if ps != nil && !ps.Busy {
			ps.Busy = true
			queue = append(queue, ps)
		}
	}
	a.pendingSends = nil
	return bc, queue
}

// flushPendingSend sends one queued mail, or saves it as a draft when sending fails (the engine
// itself queues it in the outbox on network errors). It reports whether the mail went out.
func flushPendingSend(bc *backend.Client, ps *pendingComposeSend) bool {
	req := ps.Req
	var srcAttKeys []string
	for _, sa := range req.SourceAttachments {
		srcAttKeys = append(srcAttKeys, sa.MessageID+":"+sa.PartName+":"+sa.Name)
	}
	req.IdempotencyKey = backend.ContentIdempotencyKey(ps.Nonce, "send",
		req.AccountID, req.IdentityID, req.FromEmail, req.DraftMessageID,
		strings.Join(req.To, ","), strings.Join(req.CC, ","), strings.Join(req.BCC, ","),
		req.Subject, req.Body, req.BodyHTML, req.ReplyToMessageID, req.ReplyType, req.Sign,
		strings.Join(req.Attachments, ","), strings.Join(srcAttKeys, ","))
	c, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	_, err := bc.ComposeSend(c, req)
	cancel()
	if err != nil {
		// A fresh deadline: a send that timed out must not leave the draft no time.
		dc, dcancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer dcancel()
		req.IdempotencyKey = backend.NewIdempotencyKey()
		_, _ = bc.ComposeDraft(dc, req)
		return false
	}
	return true
}

func (a *App) startComposeAutoSaveTicker(sessSeq uint64) {
	if a.updateUI == nil || a.cfg.App.EffectiveDraftAutoSaveSeconds() <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			stop := false
			a.emitUpdate(func() {
				if !a.composeOpen || a.composeSessionSeq != sessSeq {
					stop = true
					return
				}
				a.maybeAutoSaveComposeDraft()
			})
			if stop {
				return
			}
		}
	}()
}

// maybeAutoSaveComposeDraft runs from desktop/phone compose rendering and the idle ticker to silently save dirty drafts.
func (a *App) maybeAutoSaveComposeDraft() {
	if !a.composeOpen || a.readOnly || a.composeBusy || a.composeAutoSaveBusy {
		return
	}
	intervalSec := a.cfg.App.EffectiveDraftAutoSaveSeconds()
	if intervalSec <= 0 {
		return
	}
	if strings.TrimSpace(a.composeTo) == "" &&
		strings.TrimSpace(a.composeSubject) == "" &&
		len(a.composeAttachments) == 0 &&
		len(a.composeSourceAttachments) == 0 &&
		extractComposeUserTextOnly(a.composeBody) == "" {
		return
	}
	snap := a.composeSnapshot()
	if snap == a.composeBaseline || snap == a.composeAutoSaveSnap {
		return
	}
	now := time.Now()
	if !a.composeDirtyAt.IsZero() && now.Sub(a.composeDirtyAt) < 4*time.Second {
		return
	}
	if !a.composeAutoSaveLast.IsZero() && now.Sub(a.composeAutoSaveLast) < time.Duration(intervalSec)*time.Second {
		return
	}
	a.autoSaveDraft(context.Background(), snap)
}

func (a *App) autoSaveDraft(ctx context.Context, snapAtStart string) {
	bc, err := a.client()
	if err != nil {
		return
	}
	sessSeq := a.composeSessionSeq
	resumedDraft := a.composeReplyType == "draft"
	req := a.buildComposeReq(false)
	req.IdempotencyKey = backend.NewIdempotencyKey()
	a.composeAutoSaveBusy = true
	a.composeAutoSaveLast = time.Now()
	ed := a.composeEditor
	usingWYSIWYG := a.composeUsingWYSIWYG() && ed != nil && ed.Widget() != nil
	fwdMsgID := a.composeForwardMsgID
	fwdHTML := a.composeForwardHTML
	keepHTML := a.composeKeepHTML
	fwdTitle := a.tr("Weitergeleitete Nachricht", "Forwarded message")
	fwdFrom, fwdDate, fwdSubj, fwdSep := a.composeForwardFrom, a.composeForwardDate, a.composeForwardSubj, a.composeForwardSep
	dc := a.opts.Direct

	go func() {
		c, cancel := context.WithTimeout(ctx, 18*time.Second)
		defer cancel()
		if usingWYSIWYG {
			if htmlOut, textOut, ok := ed.ReadContent(c); ok {
				req.BodyHTML = htmlOut
				req.Body = textOut
				req.IsHTML = true
			}
		}
		if keepHTML && fwdHTML != "" && req.BodyHTML == "" {
			var inlineImgs map[string]mailengine.InlineImage
			if dc != nil && fwdMsgID != "" {
				inlineImgs, _ = dc.ReadAllInlineImages(c, fwdMsgID)
			}
			req.BodyHTML = BuildForwardHTML(extractUserTopText(req.Body, fwdSep), fwdTitle, fwdFrom, fwdDate, fwdSubj, fwdHTML, inlineImgs)
		}
		raw, err := bc.ComposeDraft(c, req)
		a.emitUpdate(func() {
			a.composeAutoSaveBusy = false
			if err != nil {
				return
			}
			res := backend.ParseComposeResult(raw)
			// Guard against stale in-flight auto-saves from a previous/closed compose session.
			if !a.composeOpen || a.composeSessionSeq != sessSeq {
				// Discarded while this save ran: its draft must not stay behind.
				if sessSeq == a.composeDiscardedSeq && !resumedDraft && res.DraftID != "" {
					a.deleteDiscardedDraft(req.AccountID, res.DraftID)
				}
				return
			}
			if res.DraftID != "" {
				a.composeDraftID = res.DraftID
			}
			a.composeAutoSaveSnap = snapAtStart
			a.composeAutoSaveSavedAt = time.Now()
			if a.composeSnapshot() == snapAtStart {
				a.composeBaseline = snapAtStart
			}
		})
	}()
}

// renderUndoSendBar displays timed Undo-Send banners for all queued outgoing emails.
func (a *App) renderUndoSendBar(c *ui.Context) {
	if len(a.pendingSends) == 0 {
		return
	}
	t := c.Theme()
	for i, ps := range a.pendingSends {
		if ps == nil {
			continue
		}
		psCopy := ps
		rem := int(time.Until(psCopy.Deadline).Seconds()) + 1
		if rem < 0 {
			rem = 0
		}
		subj := strings.TrimSpace(psCopy.Req.Subject)
		if subj == "" {
			subj = a.tr("(Kein Betreff)", "(No Subject)")
		}
		msg := a.trf("Sende „%s“ in %d s…", "Sending “%s” in %d s…", subj, rem)
		ui.Row(c.Key(fmt.Sprintf("undo-send-bar-%d-%s", i, psCopy.Nonce))).FillWidth().Padding(7, 12).Gap(8).AlignItems(ui.Center).
			Background(withAlpha(t.Accent, 0.14)).BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(t.Accent, 0.55)).Shrink(0).Children(func() {
			ui.Icon(c, iconSend).Size(14, 14).TextColor(t.Accent).Shrink(0)
			ui.Text(c, msg).FontSize(12).Bold().Grow(1).MinWidth(0).SingleLine()
			if ui.Button(c.Key("undo-send-now-"+psCopy.Nonce), a.tr("Jetzt senden", "Send now")).Padding(3, 9).Clicked() && !psCopy.Busy {
				a.commitPendingSend(context.Background(), psCopy.Nonce)
			}
			if ui.PrimaryButton(c.Key("undo-send-cancel-"+psCopy.Nonce), a.tr("Senden abbrechen", "Undo send")).Clicked() && !psCopy.Busy {
				a.cancelPendingSend(psCopy.Nonce)
			}
		})
	}
}

func (a *App) insertComposeTemplate(tpl config.ComposeTemplate) {
	if strings.TrimSpace(a.composeSubject) == "" && strings.TrimSpace(tpl.Subject) != "" {
		a.composeSubject = tpl.Subject
	}
	if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
		a.composeEditor.InsertHTML(plainBlockToEditableHTML(tpl.Body))
		a.NotifyComposeDirty()
	} else {
		a.insertComposeText(tpl.Body)
	}
	a.composeTplOpen = false
}

func (a *App) saveNewComposeTemplate(name, subject, body string) bool {
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" || body == "" {
		a.toast(a.tr("Bitte Namen und Vorlagentext angeben.", "Please provide template name and text."), "warning")
		return false
	}
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	cfg.App.ComposeTemplates = append(cfg.App.ComposeTemplates, config.ComposeTemplate{
		ID:      fmt.Sprintf("tpl-%d", time.Now().UnixNano()),
		Name:    name,
		Subject: strings.TrimSpace(subject),
		Body:    body,
	})
	if a.opts.ConfigSaver != nil {
		if serr := a.opts.ConfigSaver(cfg); serr != nil {
			a.toast(a.tr("Vorlage nicht gespeichert: ", "Template not saved: ")+serr.Error(), "error")
			return false
		}
	}
	a.applyConfig(cfg)
	a.toast(a.trf("Vorlage „%s“ gespeichert", "Template “%s” saved", name), "success")
	return true
}

func (a *App) deleteComposeTemplate(id string) {
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	next := slices.DeleteFunc(append([]config.ComposeTemplate(nil), cfg.App.ComposeTemplates...), func(t config.ComposeTemplate) bool {
		return t.ID == id
	})
	cfg.App.ComposeTemplates = next
	if a.opts.ConfigSaver != nil {
		_ = a.opts.ConfigSaver(cfg)
	}
	a.applyConfig(cfg)
}

func isPreviewableAttachmentName(name string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp",
		".eml", ".txt", ".md", ".csv", ".tsv", ".json", ".log", ".xml", ".yaml", ".yml", ".ics", ".vcf", ".pdf":
		return true
	}
	return false
}
