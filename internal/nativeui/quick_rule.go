package nativeui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/triage"
)

const (
	quickRulePerMailTimeout = 10 * time.Second
	quickRuleFilterTimeout  = 15 * time.Second
)

// quickRuleState holds multi-select and the Quick-Rule wizard (Thunderbird native filters).
type quickRuleState struct {
	checkedIDs       map[string]bool
	lastCheckedIndex int // Shift-click anchor; reset whenever the list is replaced

	quickRuleOpen         bool
	quickRuleName         string
	quickRuleAccount      string            // account the filter is saved to (and the only one acted on)
	quickRuleAccountIDs   []string          // accounts among the marked mails (selector when >1)
	quickRuleAccountLabel string            // selected label in the account selector
	quickRuleAccountByLbl map[string]string // selector label -> account id
	quickRuleAllMarked    []bridgeclient.MessageHeader
	quickRuleMarked       []bridgeclient.MessageHeader // marked mails of quickRuleAccount
	quickRuleMarkedIDs    map[string]bool
	quickRuleMatchAll     bool
	quickRuleFromText     string
	quickRuleSubjectText  string
	quickRuleAction       string // "trash", "mark_read_trash", "mark_read", "move"
	quickRuleActionLabel  string
	quickRuleFolderPath   string
	quickRuleApplyPreview bool
	quickRuleConfirmSig   string // rule signature confirmed by a first click on a broad destructive rule
	quickRuleBusy         bool
	quickRuleAILoading    bool
	quickRuleAISeq        uint64
	quickRuleError        string
	quickRuleInfo         string
}

var quickRuleActions = []string{"trash", "mark_read_trash", "mark_read", "move"}

func quickRuleActionLabelLang(action, lang string) string {
	switch action {
	case "mark_read_trash":
		return trLang(lang, "Als gelesen + Papierkorb", "Mark Read + Move to Trash")
	case "mark_read":
		return trLang(lang, "Als gelesen markieren", "Mark as Read")
	case "move":
		return trLang(lang, "In Ordner verschieben", "Move to Folder")
	default:
		return trLang(lang, "In Papierkorb verschieben (Standard)", "Move to Trash (Safe Default)")
	}
}

// quickRuleActionLabelsLang lists the selectable actions. Thunderbird's immediate "delete" is
// not offered: the bridge rejects it (contract §6) and Trash keeps mistakes recoverable.
func quickRuleActionLabelsLang(lang string) []string {
	out := make([]string, 0, len(quickRuleActions))
	for _, act := range quickRuleActions {
		out = append(out, quickRuleActionLabelLang(act, lang))
	}
	return out
}

func quickRuleActionFromLabel(label string) string {
	for _, action := range quickRuleActions {
		if label == quickRuleActionLabelLang(action, "de") || label == quickRuleActionLabelLang(action, "en") {
			return action
		}
	}
	return "trash"
}

func (a *App) IsMailChecked(id string) bool {
	if a.checkedIDs == nil || id == "" {
		return false
	}
	return a.checkedIDs[id]
}

func (a *App) CheckedMessageIDs() []string {
	if len(a.checkedIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(a.checkedIDs))
	for _, m := range a.messages {
		if a.checkedIDs[m.ID] {
			out = append(out, m.ID)
		}
	}
	return out
}

func (a *App) CheckedMessages() []bridgeclient.MessageHeader {
	if len(a.checkedIDs) == 0 {
		return nil
	}
	out := make([]bridgeclient.MessageHeader, 0, len(a.checkedIDs))
	for _, m := range a.messages {
		if a.checkedIDs[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// SetMailChecked (un)checks row idx; with shiftRange it extends from the anchor. The anchor is
// only valid for the list it was set on (setMessages resets it).
func (a *App) SetMailChecked(idx int, checked bool, shiftRange bool) {
	if idx < 0 || idx >= len(a.messages) {
		return
	}
	if a.checkedIDs == nil {
		a.checkedIDs = make(map[string]bool)
	}
	if shiftRange && a.lastCheckedIndex >= 0 && a.lastCheckedIndex < len(a.messages) {
		lo, hi := a.lastCheckedIndex, idx
		if lo > hi {
			lo, hi = hi, lo
		}
		for i := lo; i <= hi; i++ {
			id := a.messages[i].ID
			if checked {
				a.checkedIDs[id] = true
			} else {
				delete(a.checkedIDs, id)
			}
		}
	} else {
		id := a.messages[idx].ID
		if checked {
			a.checkedIDs[id] = true
		} else {
			delete(a.checkedIDs, id)
		}
	}
	a.lastCheckedIndex = idx
}

func (a *App) ToggleMailChecked(idx int, shiftRange bool) {
	if idx < 0 || idx >= len(a.messages) {
		return
	}
	next := !a.IsMailChecked(a.messages[idx].ID)
	a.SetMailChecked(idx, next, shiftRange)
}

func (a *App) SelectAllVisibleMails() {
	if a.checkedIDs == nil {
		a.checkedIDs = make(map[string]bool)
	}
	for _, m := range a.messages {
		a.checkedIDs[m.ID] = true
	}
}

func (a *App) ClearMailSelection() {
	a.checkedIDs = make(map[string]bool)
	a.lastCheckedIndex = -1
}

type actionTarget struct {
	id        string
	messageID string
}

// applyActionTargets runs patch on every target with its own timeout and expectedMessageId
// (background goroutine). It returns the IDs that succeeded and the first error.
func applyActionTargets(ctx context.Context, bc *bridgeclient.Client, targets []actionTarget, patch bridgeclient.MessageActionsRequest) (okIDs []string, failed int, firstErr error) {
	for _, t := range targets {
		p := patch
		p.ExpectedMessageID = t.messageID
		c, cancel := context.WithTimeout(ctx, quickRulePerMailTimeout)
		_, err := bc.MessageActions(c, t.id, p)
		cancel()
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		okIDs = append(okIDs, t.id)
	}
	return okIDs, failed, firstErr
}

// PerformBatchAction applies a mail action (e.g. Trash or Mark Read) across multiple selected
// IDs (UI thread).
func (a *App) PerformBatchAction(ctx context.Context, ids []string, patch bridgeclient.MessageActionsRequest, toastMsg string) {
	if len(ids) == 0 {
		return
	}
	if a.readOnly {
		a.toast(a.tr("Read-Only-Modus aktiv — Änderung blockiert", "Read-only mode active — change blocked"), "error")
		return
	}
	bc, err := a.client()
	if err != nil {
		a.toast(a.tr("Sammelaktion fehlgeschlagen: ", "Bulk action failed: ")+a.errText(err), "error")
		return
	}
	targets := make([]actionTarget, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, actionTarget{id: id, messageID: a.messageIDFor(id)})
	}
	removes := patch.RemovesFromFolder()
	go func() {
		okIDs, failed, firstErr := applyActionTargets(ctx, bc, targets, patch)
		a.emitUpdate(func() {
			if removes {
				for _, id := range okIDs {
					delete(a.checkedIDs, id)
					if a.selectedID == id {
						a.selectedID = ""
						a.selectedMsg = nil
					}
				}
				a.lastCheckedIndex = -1
			}
			switch {
			case failed == 0:
				a.toast(fmt.Sprintf("%s (%d)", toastMsg, len(okIDs)), "success")
			case len(okIDs) == 0:
				a.toast(a.tr("Sammelaktion fehlgeschlagen: ", "Bulk action failed: ")+a.errText(firstErr), "error")
			default:
				a.toast(a.trf("%d von %d angewendet, %d Fehler: %s", "%d of %d applied, %d failed: %s",
					len(okIDs), len(targets), failed, a.errText(firstErr)), "warning")
			}
			a.startMessageLoad(ctx)
		})
	}()
}

// OpenQuickRuleWizard initializes the Quick-Rule Wizard from checked emails, or falls back to
// the active email.
func (a *App) OpenQuickRuleWizard() {
	msgs := a.CheckedMessages()
	if len(msgs) == 0 && a.selectedID != "" {
		for _, m := range a.messages {
			if m.ID == a.selectedID {
				msgs = []bridgeclient.MessageHeader{m}
				break
			}
		}
		if len(msgs) == 0 && a.selectedMsg != nil && a.selectedMsg.ID == a.selectedID {
			msgs = []bridgeclient.MessageHeader{a.selectedMsg.MessageHeader}
		}
	}
	a.OpenQuickRuleForMessages(msgs)
}

// OpenQuickRuleForMessages opens the Quick-Rule Wizard seeded from the provided messages (0 AI
// tokens). Marked mails of several accounts are split: the user picks the account and only that
// account's mails are used (a Thunderbird filter belongs to exactly one account).
func (a *App) OpenQuickRuleForMessages(msgs []bridgeclient.MessageHeader) {
	if len(msgs) == 0 {
		a.toast(a.tr("Bitte zuerst mindestens eine Mail markieren oder auswählen.", "Please check or select at least one email first."), "warning")
		return
	}
	a.quickRuleAllMarked = append([]bridgeclient.MessageHeader(nil), msgs...)
	counts := map[string]int{}
	a.quickRuleAccountIDs = nil
	for _, m := range msgs {
		if counts[m.AccountID] == 0 {
			a.quickRuleAccountIDs = append(a.quickRuleAccountIDs, m.AccountID)
		}
		counts[m.AccountID]++
	}
	acc := a.quickRuleAccountIDs[0]
	if counts[a.selectedAccount] > 0 {
		acc = a.selectedAccount
	} else {
		for _, id := range a.quickRuleAccountIDs {
			if counts[id] > counts[acc] {
				acc = id
			}
		}
	}
	a.quickRuleAccountByLbl = make(map[string]string, len(a.quickRuleAccountIDs))
	for _, id := range a.quickRuleAccountIDs {
		a.quickRuleAccountByLbl[a.accountDisplayName(id)] = id
	}

	a.quickRuleAction = "trash"
	a.quickRuleActionLabel = quickRuleActionLabelLang(a.quickRuleAction, a.uiLang())
	a.quickRuleApplyPreview = false
	a.quickRuleBusy = false
	a.quickRuleAILoading = false
	a.quickRuleAISeq++
	a.quickRuleError = ""
	a.quickRuleInfo = ""
	a.quickRuleConfirmSig = ""
	a.selectQuickRuleAccount(acc)
	a.quickRuleOpen = true
}

// selectQuickRuleAccount restricts the wizard to one account and re-derives the draft from that
// account's marked mails (UI thread).
func (a *App) selectQuickRuleAccount(accID string) {
	var marked []bridgeclient.MessageHeader
	ids := map[string]bool{}
	for _, m := range a.quickRuleAllMarked {
		if m.AccountID == accID {
			marked = append(marked, m)
			ids[m.ID] = true
		}
	}
	inputs := make([]triage.MessageInput, 0, len(marked))
	for _, m := range marked {
		inputs = append(inputs, messageHeaderToInput(m))
	}
	draft := triage.SuggestQuickRule(inputs, a.uiLang())
	if accID == "" {
		accID = a.selectedAccount
		if accID == "" && len(a.accounts) > 0 {
			accID = a.accounts[0].ID
		}
	}
	a.quickRuleAccount = accID
	a.quickRuleAccountLabel = a.accountDisplayName(accID)
	a.quickRuleMarked = marked
	a.quickRuleMarkedIDs = ids
	a.quickRuleName = draft.Name
	a.quickRuleMatchAll = draft.MatchAll
	a.quickRuleFromText = strings.Join(draft.FromValues, ", ")
	a.quickRuleSubjectText = strings.Join(draft.SubjectValues, ", ")
	a.quickRuleConfirmSig = ""
	a.quickRuleError = ""
	if folders := a.quickRuleAccountFolderPaths(); len(folders) > 0 {
		a.quickRuleFolderPath = folders[0]
	} else {
		a.quickRuleFolderPath = ""
	}
}

// applyQuickRuleAccountChoice handles the account selector (UI thread).
func (a *App) applyQuickRuleAccountChoice(label string) {
	if id, ok := a.quickRuleAccountByLbl[label]; ok && id != a.quickRuleAccount {
		a.selectQuickRuleAccount(id)
	}
}

// quickRuleExcludedCount is the number of marked mails from other accounts (ignored).
func (a *App) quickRuleExcludedCount() int {
	return len(a.quickRuleAllMarked) - len(a.quickRuleMarked)
}

func (a *App) accountDisplayName(id string) string {
	for _, acc := range a.accounts {
		if acc.ID == id {
			if acc.Name != "" {
				return acc.Name
			}
			return acc.ID
		}
	}
	if id != "" {
		return id
	}
	return a.tr("Standardkonto", "Default Account")
}

func messageHeaderToInput(m bridgeclient.MessageHeader) triage.MessageInput {
	return triage.MessageInput{
		ID:        m.ID,
		ThreadID:  m.ThreadID,
		AccountID: m.AccountID,
		Folder:    m.Folder,
		FolderID:  m.FolderID,
		InInbox:   m.InInbox,
		Subject:   m.Subject,
		From:      m.From,
		To:        m.To,
		Date:      m.Date,
		Unread:    m.Unread,
	}
}

func (a *App) applyQuickRuleActionChoice(label string) {
	a.quickRuleActionLabel = label
	a.quickRuleAction = quickRuleActionFromLabel(label)
}

func (a *App) quickRuleAccountFolderPaths() []string {
	var paths []string
	seen := map[string]bool{}
	for _, f := range a.folders {
		if a.quickRuleAccount != "" && f.AccountID != "" && f.AccountID != a.quickRuleAccount {
			continue
		}
		p := strings.TrimPrefix(strings.TrimSpace(f.Path), "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths
}

func (a *App) quickRuleAccountLabelText() string {
	return a.accountDisplayName(a.quickRuleAccount)
}

// isInboxHeader reports whether m lives in its account's inbox (what a Thunderbird incoming
// filter matches).
func (a *App) isInboxHeader(m bridgeclient.MessageHeader) bool {
	if m.InInbox {
		return true
	}
	if m.FolderID != "" {
		for _, f := range a.folders {
			if f.ID != m.FolderID {
				continue
			}
			if strings.EqualFold(f.Type, "inbox") {
				return true
			}
			for _, su := range f.SpecialUse {
				if strings.EqualFold(su, "inbox") {
					return true
				}
			}
		}
	}
	folder := strings.Trim(strings.TrimSpace(m.Folder), "/")
	return strings.EqualFold(folder, "inbox") || strings.EqualFold(folder, "posteingang")
}

// quickRuleCandidates are the marked mails of the chosen account plus the loaded inbox mails of
// that account (the "+N weitere Treffer" a Thunderbird inbox filter would match).
func (a *App) quickRuleCandidates() []triage.MessageInput {
	out := make([]triage.MessageInput, 0, len(a.quickRuleMarked)+len(a.messages))
	seen := make(map[string]bool, len(a.quickRuleMarked)+len(a.messages))
	for _, m := range a.quickRuleMarked {
		seen[m.ID] = true
		out = append(out, messageHeaderToInput(m))
	}
	for _, m := range a.messages {
		if seen[m.ID] {
			continue
		}
		if a.quickRuleAccount != "" && m.AccountID != a.quickRuleAccount {
			continue
		}
		if !a.isInboxHeader(m) {
			continue
		}
		seen[m.ID] = true
		out = append(out, messageHeaderToInput(m))
	}
	return out
}

func (a *App) quickRuleConditions() []triage.NativeCondition {
	fromVals := triage.SplitCSVTerms(a.quickRuleFromText)
	subjVals := triage.SplitCSVTerms(a.quickRuleSubjectText)
	return triage.BuildQuickRuleConditions(fromVals, subjVals, a.quickRuleMatchAll)
}

func (a *App) normalizedQuickRuleAction() string {
	if a.quickRuleAction == "delete" {
		return "trash"
	}
	return a.quickRuleAction
}

// QuickRulePreview computes the live pre-save match preview (0 tokens).
func (a *App) QuickRulePreview() triage.QuickRulePreviewReport {
	return triage.EvaluateQuickRulePreview(a.quickRuleConditions(), a.quickRuleMatchAll, a.quickRuleMarkedIDs, a.quickRuleCandidates(), a.normalizedQuickRuleAction())
}

// quickRuleSignature identifies the rule as configured; any edit disarms a pending confirmation.
func (a *App) quickRuleSignature() string {
	return strings.Join([]string{
		a.quickRuleAccount, a.quickRuleFromText, a.quickRuleSubjectText, fmt.Sprint(a.quickRuleMatchAll),
		a.normalizedQuickRuleAction(), a.quickRuleFolderPath, fmt.Sprint(a.quickRuleApplyPreview),
	}, "\x00")
}

// quickRuleConfirmArmed reports whether the user already confirmed exactly this broad rule.
func (a *App) quickRuleConfirmArmed() bool {
	return a.quickRuleConfirmSig != "" && a.quickRuleConfirmSig == a.quickRuleSignature()
}

// RefineQuickRuleWithAI asks the configured AI model to refine patterns across marked emails
// (explicit user click).
func (a *App) RefineQuickRuleWithAI(ctx context.Context) {
	if a.quickRuleAILoading {
		return
	}
	bc, err := a.client()
	if err != nil {
		a.quickRuleError = a.errText(err)
		return
	}
	var marked []triage.MessageInput
	for _, m := range a.quickRuleMarked {
		marked = append(marked, messageHeaderToInput(m))
	}
	if len(marked) == 0 {
		return
	}
	lang := a.uiLang()
	account := a.quickRuleAccount
	a.quickRuleAISeq++
	seq := a.quickRuleAISeq
	a.quickRuleAILoading = true
	a.quickRuleError = ""
	a.quickRuleInfo = ""

	go func() {
		var nativeFilters []triage.NativeFilter
		if account != "" {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if fl, err := bc.FiltersList(c, account); err == nil && fl.Supported {
				nativeFilters = fl.Filters
			}
			cancel()
		}
		c, cancel := context.WithTimeout(ctx, bridgeclient.AIRequestTimeout)
		resp, err := bc.AIAnalyze(c, ai.AnalyzeRequest{
			Mode:           "suggest_filter",
			Ephemeral:      true,
			Language:       lang,
			PrimaryMessage: marked[0],
			ThreadMessages: marked,
			NativeFilters:  nativeFilters,
		})
		cancel()
		a.emitUpdate(func() {
			if seq != a.quickRuleAISeq || !a.quickRuleOpen {
				return
			}
			a.quickRuleAILoading = false
			if err != nil {
				a.quickRuleError = a.errText(err)
				return
			}
			if len(resp.FilterSuggestions) == 0 {
				a.quickRuleInfo = resp.Answer
				return
			}
			sug := resp.FilterSuggestions[0]
			if strings.TrimSpace(sug.Name) != "" {
				a.quickRuleName = strings.TrimSpace(sug.Name)
			}
			fromTerms := append([]string(nil), sug.FromContains...)
			for _, d := range sug.Domains {
				clean := strings.TrimPrefix(strings.TrimSpace(d), "@")
				if clean != "" {
					fromTerms = append(fromTerms, "@"+clean)
				}
			}
			if len(fromTerms) > 0 {
				a.quickRuleFromText = strings.Join(fromTerms, ", ")
			}
			if len(sug.SubjectContains) > 0 {
				a.quickRuleSubjectText = strings.Join(sug.SubjectContains, ", ")
			}
			a.quickRuleMatchAll = sug.MatchAll
			switch {
			case sug.Delete && sug.MarkRead:
				a.quickRuleAction = "mark_read_trash"
			case sug.Delete:
				a.quickRuleAction = "trash"
			case sug.SuggestedFolder != "":
				a.quickRuleAction = "move"
				a.quickRuleFolderPath = strings.TrimPrefix(sug.SuggestedFolder, "/")
			case sug.MarkRead:
				a.quickRuleAction = "mark_read"
			}
			a.quickRuleActionLabel = quickRuleActionLabelLang(a.quickRuleAction, a.uiLang())
			a.quickRuleInfo = sug.Reason
			if a.quickRuleInfo == "" {
				a.quickRuleInfo = resp.Answer
			}
		})
	}()
}

// quickRuleSaveBlockReason returns why Save is impossible right now ("" = possible).
func (a *App) quickRuleSaveBlockReason(preview triage.QuickRulePreviewReport) string {
	if a.readOnly {
		return a.tr("Read-Only-Modus aktiv — Filter kann nicht gespeichert werden.", "Read-only mode active — cannot save filter.")
	}
	if len(a.quickRuleConditions()) == 0 {
		return a.tr("Bitte mindestens eine Bedingung (Absender oder Betreff) angeben.", "Please enter at least one condition (sender or subject).")
	}
	if a.quickRuleAccount == "" {
		return a.tr("Kein Thunderbird-Konto für diesen Filter ausgewählt.", "No Thunderbird account selected for this filter.")
	}
	if len(preview.MarkedHitIDs) == 0 {
		return a.tr("Die Regel trifft keine der markierten Mails — bitte Absender/Betreff anpassen.", "The rule matches none of the selected emails — please adjust sender/subject.")
	}
	if a.normalizedQuickRuleAction() == "move" && strings.TrimSpace(a.quickRuleFolderPath) == "" {
		return a.tr("Bitte Zielordner für das Verschieben wählen.", "Please choose a target folder.")
	}
	return ""
}

// SaveAndApplyQuickRule saves the native Thunderbird filter and applies its action to the
// marked mails it actually matches (plus, opt-in, the preview hits) (UI thread). A destructive
// rule with broad warnings needs a second, explicit click.
func (a *App) SaveAndApplyQuickRule(ctx context.Context) {
	if a.quickRuleBusy {
		return
	}
	preview := a.QuickRulePreview()
	if reason := a.quickRuleSaveBlockReason(preview); reason != "" {
		a.quickRuleError = reason
		return
	}
	bc, err := a.client()
	if err != nil {
		a.quickRuleError = a.errText(err)
		return
	}
	if preview.RequiresConfirm && !a.quickRuleConfirmArmed() {
		a.quickRuleConfirmSig = a.quickRuleSignature()
		a.quickRuleError = a.tr(
			"Breite Regel mit destruktiver Aktion: Bitte Warnungen prüfen und „Breite Regel bestätigen“ klicken – oder die Regel eingrenzen.",
			"Broad rule with a destructive action: review the warnings and click “Confirm broad rule” – or narrow the rule.",
		)
		return
	}

	var actions []bridgeclient.FilterActionSpec
	var msgPatch bridgeclient.MessageActionsRequest
	readTrue := true
	switch a.normalizedQuickRuleAction() {
	case "mark_read_trash":
		actions = []bridgeclient.FilterActionSpec{{Type: "markRead"}, {Type: "moveToFolder", TargetFolderPath: "Trash"}}
		msgPatch = bridgeclient.MessageActionsRequest{Read: &readTrue, Trash: true}
	case "mark_read":
		actions = []bridgeclient.FilterActionSpec{{Type: "markRead"}}
		msgPatch = bridgeclient.MessageActionsRequest{Read: &readTrue}
	case "move":
		target := strings.TrimSpace(a.quickRuleFolderPath)
		actions = []bridgeclient.FilterActionSpec{{Type: "moveToFolder", TargetFolderPath: target}}
		msgPatch = bridgeclient.MessageActionsRequest{MoveToPath: target}
	default:
		actions = []bridgeclient.FilterActionSpec{{Type: "moveToFolder", TargetFolderPath: "Trash"}}
		msgPatch = bridgeclient.MessageActionsRequest{Trash: true}
	}

	// Only mails the rule really matches — never a fallback to all marked mails.
	applyIDs := append([]string(nil), preview.MarkedHitIDs...)
	if a.quickRuleApplyPreview {
		for _, ex := range preview.ExtraMatches {
			applyIDs = append(applyIDs, ex.ID)
		}
	}
	msgIDs := make(map[string]string, len(a.quickRuleMarked))
	for _, m := range a.quickRuleMarked {
		msgIDs[m.ID] = m.MessageID
	}
	targets := make([]actionTarget, 0, len(applyIDs))
	for _, id := range applyIDs {
		mid := msgIDs[id]
		if mid == "" {
			mid = a.messageIDFor(id)
		}
		targets = append(targets, actionTarget{id: id, messageID: mid})
	}

	name := strings.TrimSpace(a.quickRuleName)
	if name == "" {
		name = a.tr("OmaThunder Quick-Regel", "OmaThunder Quick Rule")
	}
	req := bridgeclient.FilterUpsertRequest{
		Mode:       "create",
		AccountID:  a.quickRuleAccount,
		Name:       name,
		Enabled:    true,
		MatchAll:   a.quickRuleMatchAll,
		Conditions: a.quickRuleConditions(),
		Actions:    actions,
	}

	a.quickRuleBusy = true
	a.quickRuleError = ""
	removes := msgPatch.Trash || msgPatch.MoveToPath != ""

	go func() {
		savedName, err := saveQuickRuleFilter(ctx, bc, req)
		if err != nil {
			a.emitUpdate(func() {
				a.quickRuleBusy = false
				a.quickRuleError = a.tr("Filter nicht gespeichert: ", "Filter not saved: ") + a.errText(err)
			})
			return
		}
		okIDs, failed, firstErr := applyActionTargets(ctx, bc, targets, msgPatch)
		a.emitUpdate(func() {
			a.quickRuleBusy = false
			a.quickRuleOpen = false
			a.quickRuleConfirmSig = ""
			for _, id := range okIDs {
				delete(a.checkedIDs, id)
				if removes && a.selectedID == id {
					a.selectedID = ""
					a.selectedMsg = nil
				}
			}
			a.lastCheckedIndex = -1
			if failed == 0 {
				a.toast(a.trf(
					"TB-Filter „%s“ gespeichert & auf %d Mail(s) angewendet",
					"TB filter “%s” saved & applied to %d email(s)",
					savedName, len(okIDs),
				), "success")
			} else {
				a.toast(a.trf(
					"TB-Filter „%s“ gespeichert · %d von %d angewendet, %d Fehler: %s",
					"TB filter “%s” saved · %d of %d applied, %d failed: %s",
					savedName, len(okIDs), len(targets), failed, a.errText(firstErr),
				), "warning")
			}
			a.startMessageLoad(ctx)
		})
	}()
}

// saveQuickRuleFilter creates the filter (mode "create", contract §6). If the create times out
// with an unknown outcome, the filter list is checked for a newly created filter of that name
// before a single retry, so a retry can never produce a duplicate.
func saveQuickRuleFilter(ctx context.Context, bc *bridgeclient.Client, req bridgeclient.FilterUpsertRequest) (string, error) {
	before, beforeErr := listFilterNames(ctx, bc, req.AccountID)
	upsert := func() (bridgeclient.FilterUpsertResponse, error) {
		c, cancel := context.WithTimeout(ctx, quickRuleFilterTimeout)
		defer cancel()
		return bc.FilterUpsert(c, req)
	}
	saved, err := upsert()
	if err != nil && isUnknownOutcome(err) {
		if beforeErr != nil {
			return "", err // cannot verify: don't risk a duplicate
		}
		if after, lerr := listFilterNames(ctx, bc, req.AccountID); lerr == nil {
			if created := findCreatedFilter(req.Name, before, after); created != "" {
				return created, nil
			}
			saved, err = upsert()
		}
	}
	if err != nil {
		return "", err
	}
	if saved.Filter.Name != "" {
		return saved.Filter.Name, nil
	}
	return req.Name, nil
}

func isUnknownOutcome(err error) bool {
	return bridgeclient.IsOutcomeUnknown(err) || errors.Is(err, context.DeadlineExceeded)
}

func listFilterNames(ctx context.Context, bc *bridgeclient.Client, accountID string) (map[string]bool, error) {
	c, cancel := context.WithTimeout(ctx, quickRuleFilterTimeout)
	defer cancel()
	fl, err := bc.FiltersList(c, accountID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(fl.Filters))
	for _, f := range fl.Filters {
		if accountID == "" || f.AccountID == "" || f.AccountID == accountID {
			names[f.Name] = true
		}
	}
	return names, nil
}

// findCreatedFilter returns a filter that exists in after but not in before and is name or its
// uniquified variant "name (N)".
func findCreatedFilter(name string, before, after map[string]bool) string {
	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(name) + `(?: \(\d+\))?$`)
	for n := range after {
		if !before[n] && re.MatchString(n) {
			return n
		}
	}
	return ""
}

// OpenThunderbirdFilters opens Thunderbird's native Message Filters dialog so the user can
// edit/manage all rules (UI thread).
func (a *App) OpenThunderbirdFilters(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		a.toast(a.errText(err), "error")
		return
	}
	accID := a.quickRuleAccount
	if accID == "" {
		accID = a.selectedAccount
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		opened, err := bc.FilterOpenDialog(c, accID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.errText(err), "error")
			} else if !opened {
				a.toast(a.tr("Filter-Dialog bitte direkt in Thunderbird unter Extras → Filter öffnen.", "Please open Tools → Message Filters directly in Thunderbird."), "info")
			}
		})
	}()
}
