package nativeui

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

type quickReplyState struct {
	msgID       string
	body        string
	sending     bool
	aiLoading   bool
	aiSeq       uint64
	suggestions []string
	aiErr       string
}

type subsState struct {
	open            bool
	loading         bool
	seq             uint64
	items           []backend.SubscriptionSummary
	err             string
	archivingSender string
}

var avatarPalette = []ui.Color{
	ui.RGB(45, 118, 210), // Ocean Blue
	ui.RGB(34, 139, 126), // Teal
	ui.RGB(108, 92, 198), // Indigo
	ui.RGB(184, 92, 56),  // Terracotta
	ui.RGB(48, 138, 86),  // Forest
	ui.RGB(158, 72, 138), // Plum
	ui.RGB(178, 124, 34), // Ochre
	ui.RGB(72, 110, 152), // Slate Steel
}

func senderInitials(from string) string {
	name := strings.TrimSpace(shortSender(from))
	if name == "" {
		name = strings.TrimSpace(extractSenderEmail(from))
	}
	if local, _, hasAt := strings.Cut(name, "@"); hasAt && strings.TrimSpace(local) != "" {
		name = strings.TrimSpace(local)
	}
	if name == "" {
		return "?"
	}
	words := strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.' || r == '_' || r == '-' || r == '@'
	})
	var parts []string
	for _, w := range words {
		var letters []rune
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				letters = append(letters, unicode.ToUpper(r))
				break
			}
		}
		if len(letters) > 0 {
			parts = append(parts, string(letters[0]))
		}
	}
	if len(parts) >= 2 {
		return parts[0] + parts[len(parts)-1]
	}
	if len(parts) == 1 {
		// Try first two letters from single token if available
		var runes []rune
		for _, r := range words[0] {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				runes = append(runes, unicode.ToUpper(r))
			}
		}
		if len(runes) >= 2 {
			return string(runes[:2])
		}
		return parts[0]
	}
	return "?"
}

func senderAvatarColor(seed string) ui.Color {
	key := strings.ToLower(strings.TrimSpace(extractSenderEmail(seed)))
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(seed))
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return avatarPalette[int(h.Sum32())%len(avatarPalette)]
}

func (a *App) renderSenderAvatar(c *ui.Context, from string, size float32) {
	initials := senderInitials(from)
	seed := extractSenderEmail(from)
	if seed == "" {
		seed = from
	}
	bg := senderAvatarColor(seed)
	fontSize := size * 0.40
	if fontSize < 10 {
		fontSize = 10
	}
	ui.Box(c).Size(size, size).Radius(size/2).Background(bg).Center().Shrink(0).Children(func() {
		ui.Text(c, initials).FontSize(fontSize).Bold().TextColor(ui.RGB(255, 255, 255)).SingleLine()
	})
}

func (a *App) hasInspectorAIContent() bool {
	return a.inspectorLoading || a.inspectorResult != "" || a.inspectorError != "" ||
		len(a.inspectorContacts) > 0 || a.inspectorEvent != nil || a.inspectorDraft != "" || a.inspectorMove != nil
}

func (a *App) clearInspectorAI() {
	a.inspectorSeq++
	a.inspectorLoading = false
	a.inspectorResult = ""
	a.inspectorDraft = ""
	a.inspectorMove, a.inspectorMoveMsgID = nil, ""
	a.inspectorEvent = nil
	a.inspectorContacts = nil
	a.inspectorError = ""
	a.inspectorMode = ""
}

// --- Snooze ----------------------------------------------------------------------------------

type snoozePreset struct {
	ID    string
	Label string
	Until time.Time
}

func nextSnoozePresets(now time.Time, lang string) []snoozePreset {
	loc := now.Location()
	later := now.Add(3 * time.Hour)
	minRounded := ((later.Minute() + 29) / 30) * 30
	later = time.Date(later.Year(), later.Month(), later.Day(), later.Hour(), 0, 0, 0, loc).Add(time.Duration(minRounded) * time.Minute)

	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 8, 0, 0, 0, loc)

	daysUntilMon := (8 - int(now.Weekday())) % 7
	if daysUntilMon == 0 {
		daysUntilMon = 7
	}
	nextWeek := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMon, 8, 0, 0, 0, loc)

	return []snoozePreset{
		{ID: "later", Label: trLang(lang, "Später heute (ca. "+later.Format("15:04")+")", "Later today (~"+later.Format("15:04")+")"), Until: later},
		{ID: "tomorrow", Label: trLang(lang, "Morgen früh (08:00)", "Tomorrow morning (8:00 AM)"), Until: tomorrow},
		{ID: "week", Label: trLang(lang, "Nächste Woche (Mo 08:00)", "Next week (Mon 8:00 AM)"), Until: nextWeek},
	}
}

func formatSnoozeWake(untilUnix int64, lang string) string {
	if untilUnix <= 0 {
		return ""
	}
	t := time.Unix(untilUnix, 0).Local()
	if lang == "en" {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("02.01. 15:04")
}

func (a *App) snoozeMail(ctx context.Context, msgID string, until time.Time) {
	if strings.TrimSpace(msgID) == "" {
		return
	}
	a.snoozeMailsBatch(ctx, []string{msgID}, until)
}

func (a *App) snoozeMailsBatch(ctx context.Context, ids []string, until time.Time) {
	if len(ids) == 0 || a.blockedReadOnly() {
		return
	}
	bc, err := a.client()
	if err != nil {
		a.toast(a.errText(err), "error")
		return
	}
	accID := a.selectedAccount
	lang := a.uiLang()
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var toastText string
	if until.IsZero() {
		toastText = a.tr("Zurückstellung aufgehoben", "Snooze cleared")
	} else {
		toastText = a.trf("Zurückgestellt bis %s", "Snoozed until %s", formatSnoozeWake(until.Unix(), lang))
	}
	neighbor := ""
	if len(ids) == 1 {
		neighbor = a.neighborAfterRemoval(ids[0])
	}
	a.snoozeSeq++
	seq := a.snoozeSeq
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		succeeded := make([]string, 0, len(ids))
		var firstErr error
		for _, id := range ids {
			if serr := bc.SnoozeMessage(c, id, until); serr != nil {
				if firstErr == nil {
					firstErr = serr
				}
				continue
			}
			succeeded = append(succeeded, id)
		}
		if len(succeeded) == 0 && firstErr != nil {
			a.emitUpdate(func() { a.toast(a.errText(firstErr), "error") })
			return
		}
		cnt, cerr := bc.CountSnoozed(c, accID)
		okSet := make(map[string]bool, len(succeeded))
		for _, id := range succeeded {
			okSet[id] = true
		}
		a.emitUpdate(func() {
			if cerr == nil && a.selectedAccount == accID && seq == a.snoozeSeq {
				a.snoozedCount = cnt
			}
			if a.selectedMsg != nil && okSet[a.selectedMsg.ID] {
				if until.IsZero() {
					a.selectedMsg.SnoozedUntil = 0
				} else {
					a.selectedMsg.SnoozedUntil = until.Unix()
				}
			}
			for i := range a.messages {
				if okSet[a.messages[i].ID] {
					if until.IsZero() {
						a.messages[i].SnoozedUntil = 0
					} else {
						a.messages[i].SnoozedUntil = until.Unix()
					}
				}
			}
			if (!until.IsZero() && a.navID == "focus") || (until.IsZero() && a.navID == "snoozed") {
				if a.selectedID != "" && okSet[a.selectedID] {
					a.selectedID, a.selectedMsg = "", nil
					a.syncPreviewForMessage(nil)
					if neighbor != "" && !a.isPhone() {
						a.SelectMessage(context.Background(), neighbor)
					} else {
						a.afterOpenMailRemoved(context.Background(), "")
					}
				}
			}
			if firstErr != nil {
				a.toast(a.tr("Teilweise zurückgestellt: ", "Partially snoozed: ")+a.errText(firstErr), "warning")
			} else {
				a.toast(toastText, "success")
			}
			a.startMessageLoad(context.Background())
		})
	}()
}

func (a *App) renderSnoozeMenuContent(c *ui.Context, msgID string, snoozedUntil int64, closeFn func()) {
	a.renderSnoozeMenuForIDs(c, []string{msgID}, snoozedUntil > time.Now().Unix(), closeFn)
}

func (a *App) renderSnoozeMenuForIDs(c *ui.Context, ids []string, anySnoozed bool, closeFn func()) {
	t := c.Theme()
	ui.Column(c).MinWidth(220).Padding(8).Gap(4).Background(t.Surface).Radius(8).Border(1, t.Border).Children(func() {
		ui.Text(c, a.tr("Später erinnern", "Snooze until")).FontSize(11.5).Bold().TextColor(t.TextMuted).Padding(2, 6)
		for _, p := range nextSnoozePresets(time.Now(), a.uiLang()) {
			pr := p
			if ui.Button(c.Key("snz-p-"+pr.ID), pr.Label).FillWidth().Padding(4, 8).Clicked() {
				if closeFn != nil {
					closeFn()
				}
				a.snoozeMailsBatch(context.Background(), ids, pr.Until)
			}
		}
		if anySnoozed {
			ui.Divider(c)
			if ui.Button(c.Key("snz-clear"), a.tr("Zurückstellung aufheben", "Unsnooze")).FillWidth().Padding(4, 8).TextColor(t.Accent).Clicked() {
				if closeFn != nil {
					closeFn()
				}
				a.snoozeMailsBatch(context.Background(), ids, time.Time{})
			}
		}
	})
}

func (a *App) appendSnoozeMenuItems(menu *ui.Menu, ids []string, anySnoozed bool) {
	ctx := context.Background()
	for _, p := range nextSnoozePresets(time.Now(), a.uiLang()) {
		pr := p
		if menu.Item(pr.Label).ID("snooze-" + pr.ID).Chosen() {
			a.snoozeMailsBatch(ctx, ids, pr.Until)
		}
	}
	if anySnoozed {
		menu.Separator()
		if menu.Item(a.tr("Zurückstellung aufheben", "Unsnooze")).ID("snooze-clear").Chosen() {
			a.snoozeMailsBatch(ctx, ids, time.Time{})
		}
	}
}

// --- Inline Quick-Reply & Reader Privacy Pill & Waiting Banner -------------------------------

func (a *App) resetQuickReply(msgID string) {
	if a.quickReply.msgID == msgID {
		return
	}
	a.quickReply.aiSeq++
	a.quickReply.msgID = msgID
	a.quickReply.body = ""
	a.quickReply.sending = false
	a.quickReply.aiLoading = false
	a.quickReply.suggestions = nil
	a.quickReply.aiErr = ""
}

func (a *App) RunQuickReplySuggestions(ctx context.Context, msg *backend.MessageDetail) {
	if msg == nil || a.quickReply.aiLoading {
		return
	}
	a.resetQuickReply(msg.ID)
	if !a.cfg.Features.Assistant {
		a.quickReply.aiErr = a.tr("KI-Assistent ist in den Einstellungen deaktiviert.", "AI assistant is disabled in Settings.")
		return
	}
	if detailIsEncrypted(*msg) {
		a.quickReply.aiErr = a.tr(
			"Verschlüsselte Mails werden aus Datenschutzgründen nicht an die KI gesendet.",
			"Encrypted emails are not sent to AI for privacy.",
		)
		return
	}
	a.quickReply.aiSeq++
	seq := a.quickReply.aiSeq
	a.quickReply.aiLoading = true
	a.quickReply.aiErr = ""
	a.quickReply.suggestions = nil

	aiBackend, model := a.agentBackend, a.agentModel
	lang := a.uiLang()
	req := ai.AnalyzeRequest{
		Backend:  aiBackend,
		Model:    model,
		Mode:     "quick_replies",
		Language: lang,
		PrimaryMessage: triage.MessageInput{
			ID:       msg.ID,
			Subject:  msg.Subject,
			From:     msg.From,
			To:       msg.To,
			Date:     msg.Date,
			BodyText: msg.BodyText,
		},
	}
	convSize := a.conversationSize(msg.ID)
	bc, _ := a.client()

	go func() {
		req.ThreadMessages = threadForAI(ctx, bc, msg.ID, convSize)
		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
		defer cancel()
		var res ai.AnalyzeResponse
		var err error
		if bc != nil {
			res, err = bc.AIAnalyze(c, req)
		} else {
			cfg := config.Default()
			if a.opts.ConfigLoader != nil {
				if loaded, cerr := a.opts.ConfigLoader(); cerr == nil {
					cfg = loaded
				}
			}
			prov := ai.ProviderForBackend(cfg.AI, aiBackend)
			if model != "" {
				prov.Model = model
			}
			cfg.AI.Agent = prov
			res, err = ai.Analyze(c, cfg.AI, req)
		}
		a.emitUpdate(func() {
			if seq != a.quickReply.aiSeq || a.quickReply.msgID != msg.ID {
				return
			}
			a.quickReply.aiLoading = false
			if err != nil {
				a.quickReply.aiErr = a.errText(err)
				return
			}
			a.quickReply.suggestions = res.QuickReplies
			if len(res.QuickReplies) == 0 && strings.TrimSpace(res.Answer) != "" {
				a.quickReply.suggestions = []string{strings.TrimSpace(res.Answer)}
			}
		})
	}()
}

func (a *App) quickReplyComposeRequest(msg *backend.MessageDetail, bodyText string) backend.ComposeRequest {
	accID := a.defaultComposeAccountID(msg.AccountID)
	own := a.accountAllEmails(msg.AccountID)
	preferEmail := ""
	if from := emailAddrKey(msg.From); own[from] {
		preferEmail = from
	} else if alias := a.matchedRecipientAlias(msg.AccountID, msg.To, msg.CC); alias != "" {
		preferEmail = alias
	}
	choices := a.composeIdentityChoices()
	var identityID, fromEmail, fromName string
	for _, ch := range choices {
		if preferEmail != "" && (accID == "" || ch.AccountID == accID) && strings.EqualFold(ch.Email, preferEmail) {
			accID, identityID, fromEmail, fromName = ch.AccountID, ch.IdentityID, ch.Email, ch.Name
			break
		}
	}
	if fromEmail == "" {
		for _, ch := range choices {
			if accID == "" || ch.AccountID == accID {
				accID, identityID, fromEmail, fromName = ch.AccountID, ch.IdentityID, ch.Email, ch.Name
				break
			}
		}
	}
	subj := msg.Subject
	if low := strings.ToLower(subj); !strings.HasPrefix(low, "re:") && !strings.HasPrefix(low, "aw:") {
		subj = "Re: " + subj
	}
	sig, sigHTML, sigBelow := a.composeSignatureParts(accID, fromEmail)
	quote := a.replyQuote(msg)
	fullBody := appendAccountSignatureWithQuote(bodyText, sig, quote, sigBelow)
	fullHTML := buildComposeEditorInitialHTMLWithSig(bodyText, composeSignatureBlockHTMLFromParts(sig, sigHTML), sigBelow, "", "", nil, quote)
	return backend.ComposeRequest{
		AccountID:        accID,
		IdentityID:       identityID,
		FromEmail:        fromEmail,
		FromName:         fromName,
		ReplyToMessageID: msg.ID,
		ReplyType:        "reply",
		To:               splitEmailList(replyRecipient(msg, own)),
		Subject:          subj,
		Body:             fullBody,
		BodyHTML:         fullHTML,
		IsHTML:           true,
	}
}

func (a *App) SendQuickReply(ctx context.Context, msg *backend.MessageDetail) {
	if msg == nil || a.quickReply.sending || a.blockedReadOnly() {
		return
	}
	bodyText := strings.TrimSpace(a.quickReply.body)
	if bodyText == "" {
		return
	}
	// Never send a plaintext quick reply to an encrypted email: hand off to Compose with crypto controls.
	if detailIsEncrypted(*msg) {
		draft := a.quickReply.body
		a.quickReply.body = ""
		a.OpenComposeReply(msg, draft)
		return
	}
	// If the user mentions an attachment, hand off to Compose so they can attach files before sending.
	if m := missingAttachmentKeywordRE.FindString(bodyText); m != "" {
		a.toast(a.trf("Anhang erwähnt („%s“) — im Antworten-Fenster kannst du Dateien anhängen.", "Attachment mentioned (“%s”) — attach files in the reply editor.", strings.TrimSpace(m)), "warning")
		draft := a.quickReply.body
		a.quickReply.body = ""
		a.OpenComposeReply(msg, draft)
		return
	}
	req := a.quickReplyComposeRequest(msg, bodyText)
	if len(req.To) == 0 {
		a.toast(a.tr("Kein Empfänger ermittelt — bitte Antworten-Dialog nutzen.", "No recipient found — please open full Reply."), "warning")
		return
	}
	backup := composeDraftBackup{
		AccountID: req.AccountID,
		To:        strings.Join(req.To, ", "),
		Subject:   req.Subject,
		Body:      req.Body,
		BodyHTML:  req.BodyHTML,
		ReplyTo:   msg.ID,
		ReplyType: "reply",
	}
	a.quickReply.body = ""
	a.quickReply.suggestions = nil
	a.quickReply.aiErr = ""
	if delaySec := a.cfg.App.EffectiveUndoSendSeconds(); delaySec > 0 {
		a.enqueuePendingSend(req, backend.NewIdempotencyKey(), backup, delaySec)
		return
	}
	a.sendPreparedCompose(ctx, req, backend.NewIdempotencyKey(), "", "", false, "", "", "", "", 0, &backup)
}

func (a *App) renderInlineQuickReplyCard(c *ui.Context, msg *backend.MessageDetail) {
	if msg == nil || a.readOnly || a.isDraftsMessage(msg) {
		return
	}
	if a.quickReply.msgID != msg.ID {
		a.resetQuickReply(msg.ID)
	}
	t := c.Theme()
	phone := a.isPhone()
	own := a.accountAllEmails(msg.AccountID)
	target := shortSender(replyRecipient(msg, own))
	ui.Column(c.Key("qr-card")).FillWidth().Padding(12).Radius(10).Background(t.Surface).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconReply).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, a.tr("Schnellantwort an ", "Quick reply to ")+target).FontSize(12).Bold().TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			if a.cfg.Features.Assistant && !detailIsEncrypted(*msg) {
				aiLbl := a.tr("Vorschläge", "Suggestions")
				if a.quickReply.aiLoading {
					aiLbl = a.tr("Läuft…", "Thinking…")
				}
				aiBtn := iconLabelBtn(c.Key("qr-ai-btn"), iconSparkles, aiLbl, false, a.tr("3 kurze Antwortvorschläge per KI erzeugen", "Generate 3 quick reply suggestions with AI"))
				if phone {
					aiBtn.MinHeight(38).Padding(6, 12)
				} else {
					aiBtn.Padding(2, 8)
				}
				if aiBtn.Clicked() && !a.quickReply.aiLoading {
					a.RunQuickReplySuggestions(context.Background(), msg)
				}
			}
			popBtn := iconBtn(c.Key("qr-popout"), iconExternal, a.tr("Im großen Antwort-Fenster öffnen", "Open full reply editor"), t.TextMuted, false)
			if phone {
				popBtn.MinHeight(38).Padding(8, 10)
			}
			if popBtn.Clicked() {
				draft := a.quickReply.body
				a.quickReply.body = ""
				a.OpenComposeReply(msg, draft)
			}
		})

		if a.quickReply.aiErr != "" {
			ui.Text(c, a.quickReply.aiErr).FontSize(11.5).TextColor(t.Danger)
		}
		if len(a.quickReply.suggestions) > 0 {
			ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				for i, sug := range a.quickReply.suggestions {
					idx := i
					sText := sug
					chip := ui.Button(c.Key(fmt.Sprintf("qr-chip-%d", idx)), truncateText(sText, 64)).Tooltip(sText)
					if phone {
						chip.MinHeight(38).Padding(7, 12)
					} else {
						chip.Padding(3, 9)
					}
					if chip.Clicked() {
						a.quickReply.body = sText
					}
				}
			})
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.End).Children(func() {
			ph := a.tr("Kurze Antwort… (⌘↩)", "Quick reply… (⌘↩)")
			sendTip := a.tr("Schnellantwort senden (⌘↩)", "Send quick reply (⌘↩)")
			if phone {
				ph = a.tr("Kurze Antwort…", "Quick reply…")
				sendTip = a.tr("Schnellantwort senden", "Send quick reply")
			}
			inp := ui.TextArea(c.Key("qr-input"), &a.quickReply.body).
				Placeholder(ph).
				Grow(1).MinWidth(0).MinHeight(0).Lines(1, 5)
			canSend := !a.quickReply.sending && strings.TrimSpace(a.quickReply.body) != ""
			sendBtn := iconLabelBtn(c.Key("qr-send"), iconSend, a.tr("Senden", "Send"), true, sendTip)
			if phone {
				sendBtn.MinHeight(40).Padding(8, 14)
			}
			if !canSend {
				sendBtn.Disabled(true)
			}
			if (sendBtn.Clicked() || (canSend && !phone && inp.Shortcut(ui.Cmd, ui.KeyEnter))) && canSend {
				a.SendQuickReply(context.Background(), msg)
			}
		})
	})
}

func (a *App) renderReaderPrivacyPill(c *ui.Context, msg *backend.MessageDetail) {
	if msg == nil || a.readerBody.MsgID != msg.ID {
		return
	}
	px := a.readerBody.TrackingPixels
	blk := a.readerBody.BlockedImages
	if px <= 0 && blk <= 0 {
		return
	}
	t := c.Theme()
	var parts []string
	if px > 0 {
		if px == 1 {
			parts = append(parts, a.tr("1 Tracker blockiert", "1 tracker blocked"))
		} else {
			parts = append(parts, a.trf("%d Tracker blockiert", "%d trackers blocked", px))
		}
	}
	if blk > 0 {
		if blk == 1 {
			parts = append(parts, a.tr("1 externes Bild geschützt", "1 remote image blocked"))
		} else {
			parts = append(parts, a.trf("%d externe Bilder geschützt", "%d remote images blocked", blk))
		}
	}
	fg := t.Success
	bg := withAlpha(t.Success, 0.13)
	if px == 0 {
		fg = t.TextMuted
		bg = t.Surface
	}
	ui.Row(c.Key("rdr-privacy-pill")).Padding(2, 8).Radius(999).Background(bg).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Icon(c, iconShieldChk).Size(11, 11).TextColor(fg)
		ui.Text(c, strings.Join(parts, " · ")).FontSize(11).Bold().TextColor(fg).SingleLine()
	})
}

func (a *App) renderWaitingFollowUpBanner(c *ui.Context, msg *backend.MessageDetail) {
	if msg == nil || !a.waitingReplyIDs[msg.ID] || a.readOnly {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("wait-followup-banner")).FillWidth().Padding(8, 12).Radius(8).Background(withAlpha(t.Warning, 0.12)).
		Border(1, withAlpha(t.Warning, 0.40)).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconClock).Size(14, 14).TextColor(t.Warning).Shrink(0)
		ui.Text(c, a.trf(
			"Wartet auf Antwort (gesendet %s) — noch keine Rückmeldung im Thread.",
			"Waiting for reply (sent %s) — no response in this thread yet.",
			formatRelTimeLang(msg.Date, a.uiLang()),
		)).FontSize(12).Bold().TextColor(t.Text).Grow(1).MinWidth(0)
		if iconLabelBtn(c.Key("wait-followup-btn"), iconReply, a.tr("Nachfassen", "Follow up"), false, a.tr("Freundliche Erinnerung im Antwort-Fenster öffnen", "Open gentle follow-up in reply editor")).Clicked() {
			own := a.accountAllEmails(msg.AccountID)
			peer := shortSender(replyRecipient(msg, own))
			template := a.trf(
				"Hallo %s,\n\nkurze Rückfrage zu meiner Nachricht unten — konntest du schon einen Blick darauf werfen?\n\nViele Grüße",
				"Hi %s,\n\nJust following up on my note below — did you get a chance to take a look?\n\nBest regards",
				peer,
			)
			a.OpenComposeReply(msg, template)
		}
	})
}

// --- Subscriptions Manager Modal ---------------------------------------------------------------

func (a *App) OpenSubscriptionsModal(ctx context.Context) {
	if !a.engineReady {
		return
	}
	a.subs.open = true
	a.subs.loading = true
	a.subs.err = ""
	a.subs.archivingSender = ""
	a.subs.seq++
	seq := a.subs.seq
	accID := a.selectedAccount
	bc, err := a.client()
	if err != nil {
		a.subs.loading = false
		a.subs.err = a.errText(err)
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		items, lerr := bc.ListSubscriptions(c, accID)
		a.emitUpdate(func() {
			if seq != a.subs.seq {
				return
			}
			a.subs.loading = false
			if lerr != nil {
				a.subs.err = a.errText(lerr)
				return
			}
			a.subs.items = items
		})
	}()
}

func (a *App) renderSubscriptionsModal(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.subs.open, func() {
		ui.Column(c.Key("subs-modal")).Width(a.modalW(660)).MaxHeight(a.modalH(c, 560)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconMailOpen).Size(16, 16).TextColor(t.Accent)
				ui.Text(c, a.tr("Abos & Newsletter aufräumen", "Subscriptions & Newsletter Cleanup")).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("subs-refresh"), "↻").Padding(2, 8).Clicked() && !a.subs.loading {
					a.OpenSubscriptionsModal(context.Background())
				}
				if ui.Button(c.Key("subs-close"), "✕").Padding(2, 8).Clicked() {
					a.subs.open = false
				}
			})
			ui.Text(c, a.tr(
				"Absender in deinem Postfach mit aktivem List-Unsubscribe-Header. Abmelden oder alte Ausgaben direkt archivieren.",
				"Senders in your mailbox with a List-Unsubscribe header. Unsubscribe in one click or archive existing issues.",
			)).FontSize(12).TextColor(t.TextMuted)

			if a.subs.loading {
				ui.Row(c).FillWidth().Padding(24).Center().Gap(8).Children(func() {
					ui.Spinner(c)
					ui.Text(c, a.tr("Durchsuche Newsletter-Header…", "Scanning newsletter headers…")).FontSize(12.5).TextColor(t.TextMuted)
				})
				return
			}
			if a.subs.err != "" {
				ui.Text(c, a.subs.err).FontSize(12).TextColor(t.Danger)
				return
			}
			if len(a.subs.items) == 0 {
				ui.Column(c).FillWidth().Padding(24).Center().Gap(6).Children(func() {
					ui.Text(c, a.tr("Keine offenen Newsletter-Abos gefunden", "No active newsletter subscriptions found")).FontSize(13).Bold()
					ui.Text(c, a.tr("Nur geladene Mails außerhalb von Archiv & Papierkorb werden berücksichtigt.", "Only downloaded messages outside Archive & Trash are counted.")).FontSize(12).TextColor(t.TextMuted)
				})
				return
			}

			ui.Scroll(c.Key("subs-scroll")).FillWidth().Grow(1).MinHeight(120).Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for _, sub := range a.subs.items {
						item := sub
						name := strings.TrimSpace(item.SenderName)
						if name == "" {
							name = item.SenderEmail
						}
						ui.Row(c.Key("sub-row-"+item.SenderEmail)).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(10).AlignItems(ui.Center).Children(func() {
							a.renderSenderAvatar(c, name+" <"+item.SenderEmail+">", 30)
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, name).FontSize(12.5).Bold().SingleLine().MinWidth(0)
									if item.OneClick {
										miniTag(c, "One-Click", t.Success, withAlpha(t.Success, 0.14))
									}
								})
								meta := fmt.Sprintf("%s · %s", item.SenderEmail, a.trf("%d Mails (%d ungelesen)", "%d emails (%d unread)", item.Count, item.UnreadCount))
								ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).SingleLine().MinWidth(0)
							})
							if ui.Button(c.Key("sub-unsub-"+item.SenderEmail), a.tr("Abmelden", "Unsubscribe")).Padding(4, 10).Clicked() {
								accID := item.AccountID
								if accID == "" {
									accID = a.selectedAccount
								}
								a.executeUnsubscribeTarget(context.Background(), accID, item.UnsubscribeHTTPS, item.UnsubscribeMailto, item.OneClick)
							}
							if !a.readOnly && len(item.MessageIDs) > 0 {
								archBtn := ui.Button(c.Key("sub-arch-"+item.SenderEmail), a.trf("Archiv (%d)", "Archive (%d)", len(item.MessageIDs))).Padding(4, 10)
								if a.subs.archivingSender != "" {
									archBtn.Disabled(true)
								}
								if archBtn.Clicked() && a.subs.archivingSender == "" {
									a.archiveSubscription(context.Background(), item)
								}
							}
						})
					}
				})
			})
		})
	})
}

func (a *App) archiveSubscription(ctx context.Context, item backend.SubscriptionSummary) {
	if a.subs.archivingSender != "" || len(item.MessageIDs) == 0 || a.blockedReadOnly() {
		return
	}
	bc, err := a.client()
	if err != nil {
		a.toast(a.errText(err), "error")
		return
	}
	items := make([]backend.BatchActionItem, 0, len(item.MessageIDs))
	for _, id := range item.MessageIDs {
		if strings.TrimSpace(id) != "" {
			items = append(items, backend.BatchActionItem{ID: id})
		}
	}
	if len(items) == 0 {
		return
	}
	a.subs.archivingSender = item.SenderEmail
	senderEmail := item.SenderEmail
	seq := a.subs.seq
	go func() {
		c, cancel := context.WithTimeout(ctx, max(20*time.Second, time.Duration(len(items))*2*time.Second))
		defer cancel()
		res, err := bc.BatchMessageActions(c, backend.BatchActionsRequest{
			Items:  items,
			Action: backend.MessageActionsRequest{Archive: true},
		})
		a.emitUpdate(func() {
			if a.subs.seq == seq {
				a.subs.archivingSender = ""
			}
			if err != nil {
				a.toast(a.errText(err), "error")
				return
			}
			if !res.OK || res.Failed > 0 {
				msg := res.FirstError
				if msg == "" {
					msg = a.tr("Einige Nachrichten konnten nicht archiviert werden", "Some messages could not be archived")
				}
				a.toast(msg, "error")
				if a.subs.open {
					a.OpenSubscriptionsModal(context.Background())
				}
				return
			}
			if a.subs.seq == seq {
				var next []backend.SubscriptionSummary
				for _, cur := range a.subs.items {
					if cur.SenderEmail != senderEmail {
						next = append(next, cur)
					}
				}
				a.subs.items = next
			}
			a.toast(a.tr("Newsletter archiviert", "Newsletter archived"), "success")
			a.startMessageLoad(context.Background())
			a.refreshFoldersBackground(context.Background())
		})
	}()
}

func (a *App) setMailListFilter(mode int) {
	if a.mailListFilter == mode {
		return
	}
	a.mailListFilter = mode
	a.lastCheckedIndex = -1
	if len(a.checkedIDs) > 0 {
		visibleSet := make(map[string]bool)
		for _, origIdx := range a.visibleMessageIndices() {
			visibleSet[a.messages[origIdx].ID] = true
		}
		for id := range a.checkedIDs {
			if !visibleSet[id] {
				delete(a.checkedIDs, id)
			}
		}
	}
	a.mailList.selected = -1
	if a.selectedID != "" {
		for vi, origIdx := range a.visibleMessageIndices() {
			if a.messages[origIdx].ID == a.selectedID {
				a.mailList.selected = vi
				break
			}
		}
	}
	a.phone.list.Selected = nil
}

func (a *App) visibleMessageIndices() []int {
	if len(a.messages) == 0 {
		return nil
	}
	if a.mailListFilter == 0 || a.searchActive() {
		out := make([]int, len(a.messages))
		for i := range a.messages {
			out[i] = i
		}
		return out
	}
	out := make([]int, 0, len(a.messages))
	for i, m := range a.messages {
		switch a.mailListFilter {
		case 1: // Unread
			if m.Unread {
				out = append(out, i)
			}
		case 2: // Starred
			if m.Flagged {
				out = append(out, i)
			}
		case 3: // Attachments
			if m.HasAttachments {
				out = append(out, i)
			}
		default:
			out = append(out, i)
		}
	}
	return out
}
