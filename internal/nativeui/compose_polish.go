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
	a.composeBodyHTML = b.BodyHTML
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
	a.composeEditorGen++
}

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
// or sends immediately when UndoSendSeconds == 0.
func (a *App) requestComposeSend(ctx context.Context, req backend.ComposeRequest) {
	if a.composeBusy {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Senden nicht erlaubt (Read-Only oder Offline)", "Sending not allowed (read-only or offline)"), "error")
		return
	}
	_ = bc

	dc := a.opts.Direct
	ed := a.composeEditor
	usingWYSIWYG := a.composeUsingWYSIWYG() && ed != nil && ed.Widget() != nil

	delaySec := a.cfg.App.EffectiveUndoSendSeconds()
	if !a.composeMissingAttAck && len(req.Attachments) == 0 && len(req.SourceAttachments) == 0 {
		// Check current editor text synchronously if WYSIWYG is active so we catch recent edits.
		bodyToCheck := req.Body
		if usingWYSIWYG {
			rctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			if htmlOut, textOut, ok := ed.ReadContent(rctx); ok {
				req.BodyHTML = htmlOut
				req.Body = textOut
				req.IsHTML = true
				bodyToCheck = textOut
			}
			cancel()
		}
		if kw := detectMissingAttachmentKeyword(extractComposeUserTextOnly(bodyToCheck)); kw != "" {
			a.composeMissingAttWarn = kw
			return
		}
	}

	if delaySec <= 0 {
		a.SendCompose(ctx, req)
		return
	}

	// Read WYSIWYG contents into req + backup before closing compose for the Undo-Send grace window.
	if usingWYSIWYG && !req.IsHTML {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if htmlOut, textOut, ok := ed.ReadContent(rctx); ok {
			req.BodyHTML = htmlOut
			req.Body = textOut
			req.IsHTML = true
		}
		cancel()
	}
	if a.composeNonce == "" {
		a.composeNonce = backend.NewIdempotencyKey()
	}
	if a.composeKeepHTML && a.composeForwardHTML != "" && req.BodyHTML == "" && dc != nil && a.composeForwardMsgID != "" {
		fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		inlineImgs, _ := dc.ReadAllInlineImages(fctx, a.composeForwardMsgID)
		cancel()
		req.BodyHTML = BuildForwardHTML(
			extractUserTopText(req.Body, a.composeForwardSep),
			a.tr("Weitergeleitete Nachricht", "Forwarded message"),
			a.composeForwardFrom, a.composeForwardDate, a.composeForwardSubj,
			a.composeForwardHTML, inlineImgs,
		)
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
	a.pendingSend = ps
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
				if a.pendingSend == nil || a.pendingSend.Nonce != nonce {
					done = true
					return
				}
				if !time.Now().Before(deadline) {
					done = true
					a.commitPendingSend(context.Background())
				}
			})
			if done {
				return
			}
		}
	}(ps.Nonce, ps.Deadline)
}

func (a *App) cancelPendingSend() {
	ps := a.pendingSend
	if ps == nil || ps.Busy {
		return
	}
	a.pendingSend = nil
	a.restoreComposeFromBackup(ps.Restore)
	a.toast(a.tr("Senden abgebrochen — E-Mail wieder im Editor", "Send cancelled — email restored in compose"), "info")
}

func (a *App) commitPendingSend(ctx context.Context) {
	ps := a.pendingSend
	if ps == nil || ps.Busy {
		return
	}
	ps.Busy = true
	a.pendingSend = nil
	a.sendPreparedCompose(ctx, ps.Req, ps.Nonce, ps.Restore.ForwardMsgID, ps.Restore.ForwardHTML, ps.Restore.KeepHTML,
		ps.Restore.ForwardFrom, ps.Restore.ForwardDate, ps.Restore.ForwardSubj, ps.Restore.ForwardSep)
}

// maybeAutoSaveComposeDraft runs from desktop/phone compose rendering to silently save dirty drafts.
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
			if a.composeOpen {
				if res.DraftID != "" {
					a.composeDraftID = res.DraftID
				}
				a.composeAutoSaveSnap = snapAtStart
				a.composeAutoSaveSavedAt = time.Now()
				if a.composeSnapshot() == snapAtStart {
					a.composeBaseline = snapAtStart
				}
			}
		})
	}()
}

// renderUndoSendBar displays the timed Undo-Send banner with Cancel & Send-Now buttons.
func (a *App) renderUndoSendBar(c *ui.Context) {
	ps := a.pendingSend
	if ps == nil {
		return
	}
	t := c.Theme()
	rem := int(time.Until(ps.Deadline).Seconds()) + 1
	if rem < 0 {
		rem = 0
	}
	subj := strings.TrimSpace(ps.Req.Subject)
	if subj == "" {
		subj = a.tr("(Kein Betreff)", "(No Subject)")
	}
	msg := a.trf("Sende „%s“ in %d s…", "Sending “%s” in %d s…", subj, rem)
	ui.Row(c.Key("undo-send-bar")).FillWidth().Padding(7, 12).Gap(8).AlignItems(ui.Center).
		Background(withAlpha(t.Accent, 0.14)).BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(t.Accent, 0.55)).Children(func() {
		ui.Icon(c, iconSend).Size(14, 14).TextColor(t.Accent).Shrink(0)
		ui.Text(c, msg).FontSize(12).Bold().Grow(1).MinWidth(0).SingleLine()
		if ui.Button(c.Key("undo-send-now"), a.tr("Jetzt senden", "Send now")).Padding(3, 9).Clicked() && !ps.Busy {
			a.commitPendingSend(context.Background())
		}
		if ui.PrimaryButton(c.Key("undo-send-cancel"), a.tr("Senden abbrechen", "Undo send")).Clicked() && !ps.Busy {
			a.cancelPendingSend()
		}
	})
}

func (a *App) insertComposeTemplate(tpl config.ComposeTemplate) {
	if strings.TrimSpace(a.composeSubject) == "" && strings.TrimSpace(tpl.Subject) != "" {
		a.composeSubject = tpl.Subject
	}
	if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
		a.composeEditor.InsertHTML(plainBlockToEditableHTML(tpl.Body))
		a.NotifyComposeDirty()
	} else {
		a.composeBody = insertComposeSnippet(a.composeBody, tpl.Body)
		a.composeDirtyAt = time.Now()
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
