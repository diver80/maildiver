package nativeui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/triage"
)

const (
	quickRulePerMailTimeout = 10 * time.Second
	quickRuleFilterTimeout  = 15 * time.Second
)

// quickRuleState holds multi-select and the Quick-Rule wizard (filter rules).
type quickRuleState struct {
	checkedIDs       map[string]bool
	lastCheckedIndex int // Shift-click anchor; reset whenever the list is replaced

	quickRuleOpen         bool
	quickRuleName         string
	quickRuleAccount      string            // account the filter is saved to (and the only one acted on)
	quickRuleAccountIDs   []string          // accounts among the marked mails (selector when >1)
	quickRuleAccountLabel string            // selected label in the account selector
	quickRuleAccountByLbl map[string]string // selector label -> account id
	quickRuleAllMarked    []backend.MessageHeader
	quickRuleMarked       []backend.MessageHeader // marked mails of quickRuleAccount
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
	// quickRuleTarget is an existing rule the AI proposed to fix instead of creating a new one
	// (nil: create); quickRuleTargetMode is how ("append_or", "replace").
	quickRuleTarget     *triage.NativeFilter
	quickRuleTargetMode string
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

// quickRuleActionLabelsLang lists the selectable actions. An immediate "delete" is
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

func (a *App) CheckedMessages() []backend.MessageHeader {
	if len(a.checkedIDs) == 0 {
		return nil
	}
	out := make([]backend.MessageHeader, 0, len(a.checkedIDs))
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

func (a *App) AllVisibleMailsChecked() bool {
	if len(a.messages) == 0 || len(a.checkedIDs) == 0 {
		return false
	}
	for _, m := range a.messages {
		if !a.checkedIDs[m.ID] {
			return false
		}
	}
	return true
}

func (a *App) ToggleSelectAllVisibleMails() {
	if len(a.messages) == 0 {
		return
	}
	if a.AllVisibleMailsChecked() {
		a.ClearMailSelection()
		return
	}
	a.SelectAllVisibleMails()
}

func (a *App) ClearMailSelection() {
	a.checkedIDs = make(map[string]bool)
	a.lastCheckedIndex = -1
}

type actionTarget struct {
	id         string
	messageID  string
	accountID  string
	folderPath string
	unread     bool
}

// applyActionTargets runs patch on every target using batched IMAP calls (one connection per account).
// Falls back to per-message calls only when connected to a test stub that doesn't implement batchActions.
func applyActionTargets(ctx context.Context, bc *backend.Client, targets []actionTarget, patch backend.MessageActionsRequest) (okIDs []string, failed int, firstErr error) {
	okIDs, _, failed, firstErr = applyActionTargetsWithMoves(ctx, bc, targets, patch)
	return okIDs, failed, firstErr
}

func applyActionTargetsWithMoves(ctx context.Context, bc *backend.Client, targets []actionTarget, patch backend.MessageActionsRequest) (okIDs []string, movedTargets map[string]string, failed int, firstErr error) {
	if len(targets) == 0 {
		return nil, nil, 0, nil
	}
	items := make([]backend.BatchActionItem, 0, len(targets))
	for _, t := range targets {
		items = append(items, backend.BatchActionItem{
			ID:                t.id,
			ExpectedMessageID: t.messageID,
		})
	}
	c, cancel := context.WithTimeout(ctx, max(30*time.Second, time.Duration(len(targets))*2*time.Second))
	res, err := bc.BatchMessageActions(c, backend.BatchActionsRequest{
		Items:  items,
		Action: patch,
	})
	cancel()
	if err == nil && res.OK {
		var ferr error
		if res.FirstError != "" {
			ferr = fmt.Errorf("%s", res.FirstError)
		}
		return res.OKIDs, res.MovedTargetPath, res.Failed, ferr
	}
	// Fallback for legacy/stub RPC implementations that return empty/unknown for batchActions.
	for _, t := range targets {
		p := patch
		p.ExpectedMessageID = t.messageID
		c, cancel := context.WithTimeout(ctx, quickRulePerMailTimeout)
		_, perr := bc.MessageActions(c, t.id, p)
		cancel()
		if perr != nil {
			failed++
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		okIDs = append(okIDs, t.id)
	}
	return okIDs, nil, failed, firstErr
}

// PerformBatchAction applies a mail action (e.g. Trash or Mark Read) across multiple selected
// IDs over batched IMAP connections (UI thread).
func (a *App) PerformBatchAction(ctx context.Context, ids []string, patch backend.MessageActionsRequest, toastMsg string) {
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
	metaByID := make(map[string]actionTarget, len(ids))
	for _, m := range a.messages {
		metaByID[m.ID] = actionTarget{id: m.ID, messageID: m.MessageID, accountID: m.AccountID, folderPath: m.Folder, unread: m.Unread}
	}
	targets := make([]actionTarget, 0, len(ids))
	for _, id := range ids {
		if t, ok := metaByID[id]; ok {
			targets = append(targets, t)
		} else {
			targets = append(targets, actionTarget{id: id, messageID: a.messageIDFor(id)})
		}
	}
	removes := patch.RemovesFromFolder()
	go func() {
		okIDs, movedTargets, failed, firstErr := applyActionTargetsWithMoves(ctx, bc, targets, patch)
		a.emitUpdate(func() {
			okSet := make(map[string]bool, len(okIDs))
			for _, id := range okIDs {
				okSet[id] = true
			}
			// Apply flag/read mutations immediately in memory so list rows, reading pane, and
			// Focus view refresh cleanly without stale star/read icons.
			selectedLeftFocus := false
			selectedAffected := okSet[a.selectedID]
			if !removes && (patch.Read != nil || patch.Flagged != nil) {
				for i := range a.messages {
					if !okSet[a.messages[i].ID] {
						continue
					}
					if patch.Read != nil {
						a.messages[i].Read = *patch.Read
						a.messages[i].Unread = !*patch.Read
					}
					if patch.Flagged != nil {
						a.messages[i].Flagged = *patch.Flagged
					}
					if a.messages[i].ID == a.selectedID && a.navID == "focus" && patch.Flagged != nil && !*patch.Flagged && !a.messages[i].Unread {
						selectedLeftFocus = true
					}
				}
				if a.selectedMsg != nil && okSet[a.selectedMsg.ID] {
					if patch.Read != nil {
						a.selectedMsg.Read = *patch.Read
						a.selectedMsg.Unread = !*patch.Read
					}
					if patch.Flagged != nil {
						a.selectedMsg.Flagged = *patch.Flagged
					}
					if a.navID == "focus" && patch.Flagged != nil && !*patch.Flagged && !a.selectedMsg.Unread {
						selectedLeftFocus = true
					}
				}
			}
			if removes || selectedLeftFocus {
				for _, id := range okIDs {
					delete(a.checkedIDs, id)
					if a.selectedID == id && (removes || selectedLeftFocus) {
						a.selectedID = ""
						a.selectedMsg = nil
						a.syncPreviewForMessage(nil)
					}
				}
				a.lastCheckedIndex = -1
			}
			if len(okIDs) > 0 {
				switch {
				case removes && len(movedTargets) > 0:
					var undoMoves []backend.UndoMoveItem
					for _, id := range okIDs {
						toPath := movedTargets[id]
						t := metaByID[id]
						if toPath != "" && t.accountID != "" && t.folderPath != "" && t.messageID != "" {
							undoMoves = append(undoMoves, backend.UndoMoveItem{
								AccountID:      t.accountID,
								MessageID:      t.messageID,
								FromFolderPath: t.folderPath,
								ToFolderPath:   toPath,
								RestoreUnread:  t.unread && (patch.Trash || a.isTrashOrJunkPath(t.accountID, toPath)),
							})
						}
					}
					if len(undoMoves) > 0 {
						a.undo = &undoAction{
							Kind:      "move",
							Label:     fmt.Sprintf("%s (%d)", toastMsg, len(undoMoves)),
							CreatedAt: time.Now(),
							MoveItems: undoMoves,
						}
					}
				case !removes && patch.Read != nil:
					items := make([]backend.BatchActionItem, 0, len(okIDs))
					for _, id := range okIDs {
						items = append(items, backend.BatchActionItem{ID: id, ExpectedMessageID: metaByID[id].messageID})
					}
					a.undo = &undoAction{
						Kind:        "read",
						Label:       fmt.Sprintf("%s (%d)", toastMsg, len(okIDs)),
						CreatedAt:   time.Now(),
						ToggleItems: items,
						PrevValue:   !*patch.Read,
					}
				case !removes && patch.Flagged != nil:
					items := make([]backend.BatchActionItem, 0, len(okIDs))
					for _, id := range okIDs {
						items = append(items, backend.BatchActionItem{ID: id, ExpectedMessageID: metaByID[id].messageID})
					}
					a.undo = &undoAction{
						Kind:        "flag",
						Label:       fmt.Sprintf("%s (%d)", toastMsg, len(okIDs)),
						CreatedAt:   time.Now(),
						ToggleItems: items,
						PrevValue:   !*patch.Flagged,
					}
				}
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
			a.refreshFoldersBackground(ctx)
			if !removes && !selectedLeftFocus && selectedAffected && a.selectedID != "" {
				a.reloadMessage(ctx, a.selectedID)
			}
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
				msgs = []backend.MessageHeader{m}
				break
			}
		}
		if len(msgs) == 0 && a.selectedMsg != nil && a.selectedMsg.ID == a.selectedID {
			msgs = []backend.MessageHeader{a.selectedMsg.MessageHeader}
		}
	}
	a.OpenQuickRuleForMessages(msgs)
}

// OpenQuickRuleForMessages opens the Quick-Rule Wizard seeded from the provided messages (0 AI
// tokens). Marked mails of several accounts are split: the user picks the account and only that
// account's mails are used (a filter rule belongs to exactly one account).
func (a *App) OpenQuickRuleForMessages(msgs []backend.MessageHeader) {
	if len(msgs) == 0 {
		a.toast(a.tr("Bitte zuerst mindestens eine Mail markieren oder auswählen.", "Please check or select at least one email first."), "warning")
		return
	}
	a.quickRuleAllMarked = append([]backend.MessageHeader(nil), msgs...)
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
	var marked []backend.MessageHeader
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
	a.quickRuleTarget, a.quickRuleTargetMode = nil, ""
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

func messageHeaderToInput(m backend.MessageHeader) triage.MessageInput {
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

// isInboxHeader reports whether m lives in its account's inbox (what an incoming
// filter matches).
func (a *App) isInboxHeader(m backend.MessageHeader) bool {
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
// that account (the "+N weitere Treffer" an inbox filter would match).
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
		a.quickRuleTargetMode + ":" + a.quickRuleTargetName(),
	}, "\x00")
}

func (a *App) quickRuleTargetName() string {
	if a.quickRuleTarget == nil {
		return ""
	}
	return a.quickRuleTarget.Name
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
		req := ai.AnalyzeRequest{
			Mode:           "suggest_filter",
			Language:       lang,
			PrimaryMessage: marked[0],
			ThreadMessages: marked,
			NativeFilters:  nativeFilters,
		}
		if len(nativeFilters) > 0 {
			// Why the existing rules missed this mail, so the model can fix one of them.
			diag := triage.DiagnoseFilters(marked[0], nativeFilters)
			req.FilterDiagnostic = &diag
		}
		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
		resp, err := bc.AIAnalyze(c, req)
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
			a.quickRuleTarget, a.quickRuleTargetMode = nil, ""
			if target, mode := quickRuleTargetFor(sug, nativeFilters, account); target != nil {
				a.quickRuleTarget, a.quickRuleTargetMode = target, mode
			}
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
		return a.tr("Kein Konto für diese Regel ausgewählt.", "No account selected for this rule.")
	}
	if len(preview.MarkedHitIDs) == 0 {
		return a.tr("Die Regel trifft keine der markierten Mails — bitte Absender/Betreff anpassen.", "The rule matches none of the selected emails — please adjust sender/subject.")
	}
	if a.normalizedQuickRuleAction() == "move" && strings.TrimSpace(a.quickRuleFolderPath) == "" {
		return a.tr("Bitte Zielordner für das Verschieben wählen.", "Please choose a target folder.")
	}
	return ""
}

// SaveAndApplyQuickRule saves the MailDiver filter rule and applies its action to the
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

	var actions []backend.FilterActionSpec
	var msgPatch backend.MessageActionsRequest
	readTrue := true
	switch a.normalizedQuickRuleAction() {
	case "mark_read_trash":
		actions = []backend.FilterActionSpec{{Type: "markRead"}, {Type: "moveToFolder", TargetFolderPath: "Trash"}}
		msgPatch = backend.MessageActionsRequest{Read: &readTrue, Trash: true}
	case "mark_read":
		actions = []backend.FilterActionSpec{{Type: "markRead"}}
		msgPatch = backend.MessageActionsRequest{Read: &readTrue}
	case "move":
		target := strings.TrimSpace(a.quickRuleFolderPath)
		actions = []backend.FilterActionSpec{{Type: "moveToFolder", TargetFolderPath: target}}
		msgPatch = backend.MessageActionsRequest{MoveToPath: target}
	default:
		actions = []backend.FilterActionSpec{{Type: "moveToFolder", TargetFolderPath: "Trash"}}
		msgPatch = backend.MessageActionsRequest{Trash: true}
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
		name = a.tr("MailDiver Quick-Regel", "MailDiver Quick Rule")
	}
	req := buildQuickRuleUpsert(a.quickRuleTarget, a.quickRuleTargetMode, backend.FilterUpsertRequest{
		Mode:       "create",
		AccountID:  a.quickRuleAccount,
		Name:       name,
		Enabled:    true,
		MatchAll:   a.quickRuleMatchAll,
		Conditions: a.quickRuleConditions(),
		Actions:    actions,
	})

	a.quickRuleBusy = true
	a.quickRuleError = ""
	removes := msgPatch.Trash || msgPatch.MoveToPath != ""

	go func() {
		savedName, err := saveQuickRuleFilter(ctx, bc, req)
		if err != nil {
			a.emitUpdate(func() {
				a.quickRuleBusy = false
				a.quickRuleError = a.tr("Regel nicht gespeichert: ", "Rule not saved: ") + a.errText(err)
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
					a.syncPreviewForMessage(nil)
				}
			}
			a.lastCheckedIndex = -1
			if failed == 0 {
				a.toast(a.trf(
					"Regel „%s“ gespeichert & auf %d Mail(s) angewendet",
					"Rule “%s” saved & applied to %d email(s)",
					savedName, len(okIDs),
				), "success")
			} else {
				a.toast(a.trf(
					"Regel „%s“ gespeichert · %d von %d angewendet, %d Fehler: %s",
					"Rule “%s” saved · %d of %d applied, %d failed: %s",
					savedName, len(okIDs), len(targets), failed, a.errText(firstErr),
				), "warning")
			}
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
			a.reloadDirectState(ctx)
		})
	}()
}

// quickRuleTargetFor returns the existing rule of accountID that an AI suggestion asks to
// update (append_or, replace), and how; nil when it creates a new rule or names no existing
// rule. fix_and_to_or is not taken: turning the user's AND rule into OR would widen it beyond
// anything the wizard's preview shows (e.g. "@bank AND Newsletter → Trash" becoming all bank
// mail), so such a suggestion becomes a new rule.
func quickRuleTargetFor(sug ai.FilterRuleSuggestion, filters []triage.NativeFilter, accountID string) (*triage.NativeFilter, string) {
	mode := strings.ToLower(strings.TrimSpace(sug.Action))
	switch mode {
	case "append_or", "replace":
	default:
		return nil, ""
	}
	name := strings.TrimSpace(sug.TargetFilterName)
	if name == "" {
		return nil, ""
	}
	for i := range filters {
		f := filters[i]
		if accountID != "" && f.AccountID != "" && f.AccountID != accountID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(f.Name), name) {
			// append_or keeps the rule's logic: an AND rule takes no extra OR term.
			if mode == "append_or" && f.MatchAll && len(f.Conditions) > 1 {
				return nil, ""
			}
			return &f, mode
		}
	}
	return nil, ""
}

// quickRuleAppendable reports whether the wizard's rule can join an OR rule as OR terms without
// matching more than the wizard previews: one condition, or conditions that are OR already. An
// AND rule ("sender AND subject") split into OR terms would catch every mail of either.
func quickRuleAppendable(create backend.FilterUpsertRequest) bool {
	return !create.MatchAll || len(create.Conditions) <= 1
}

// buildQuickRuleUpsert turns the wizard's new rule into an update of target when the AI asked
// for one: append_or adds the wizard's conditions to the rule's as OR terms and keeps its
// actions (only when quickRuleAppendable; otherwise the rule is created new); replace puts the
// wizard's rule in its place, under its name. Without target the new rule is created. The engine
// creates a rule when the target vanished meanwhile.
func buildQuickRuleUpsert(target *triage.NativeFilter, mode string, create backend.FilterUpsertRequest) backend.FilterUpsertRequest {
	if target == nil || (mode == "append_or" && !quickRuleAppendable(create)) {
		return create
	}
	req := create
	req.Mode = "replace"
	req.Name = target.Name
	req.ExistingName = target.Name
	switch mode {
	case "append_or":
		req.Enabled = target.Enabled
		req.MatchAll = false
		req.Conditions = nil
		seen := map[string]bool{}
		for _, c := range append(append([]triage.NativeCondition(nil), target.Conditions...), create.Conditions...) {
			key := strings.ToLower(c.Field + "\x00" + c.HeaderName + "\x00" + c.Op + "\x00" + strings.TrimSpace(c.Value))
			if seen[key] {
				continue
			}
			seen[key] = true
			c.BooleanAnd, c.MatchAll = false, false
			req.Conditions = append(req.Conditions, c)
		}
		req.Actions = make([]backend.FilterActionSpec, 0, len(target.Actions))
		for _, ac := range target.Actions {
			req.Actions = append(req.Actions, backend.FilterActionSpec{Type: ac.Type, TargetFolderPath: ac.TargetFolderPath, TargetFolderURI: ac.TargetFolderURI})
		}
		if len(req.Actions) == 0 {
			req.Actions = create.Actions
		}
	case "replace":
	default:
		return create
	}
	return req
}

// saveQuickRuleFilter creates the filter (mode "create", contract §6) or updates the rule the AI
// named (mode "replace", which a retry repeats harmlessly). If the create times out
// with an unknown outcome, the filter list is checked for a newly created filter of that name
// before a single retry, so a retry can never produce a duplicate.
func saveQuickRuleFilter(ctx context.Context, bc *backend.Client, req backend.FilterUpsertRequest) (string, error) {
	before, beforeErr := listFilterNames(ctx, bc, req.AccountID)
	upsert := func() (backend.FilterUpsertResponse, error) {
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
	return backend.IsOutcomeUnknown(err) || errors.Is(err, context.DeadlineExceeded)
}

func listFilterNames(ctx context.Context, bc *backend.Client, accountID string) (map[string]bool, error) {
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

// OpenFilterRules opens the Rules tab so the user can edit and manage all rules (UI thread).
func (a *App) OpenFilterRules(ctx context.Context) {
	a.OpenDirectModal(2)
}
