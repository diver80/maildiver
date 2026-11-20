package nativeui

// Keyboard model
// --------------
// Single-key shortcuts follow the usual mail-client conventions (Gmail, Omamail): j/k move, x
// checks, s stars, u toggles read, d trashes, r replies, esc goes back. They are window
// shortcuts, so mygo only delivers them when nothing else takes the key: a focused text field
// types letters instead (search, compose, Copilot), and window shortcuts are off while a modal
// is open. Letters are matched by the character the keyboard layout produces, so they work on
// QWERTZ as well. '#' and '?' have no key code in mygo, which is why trash is "d" and help "h";
// "/" is Shift+7 on German layouts and registered with and without Shift.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
)

// keyHint is one entry of the footer's shortcut legend.
type keyHint struct {
	Keys  string
	Label string
}

// keyContext is the part of the UI the single-key shortcuts currently act on. The footer shows
// the hints of the active context.
type keyContext int

const (
	keyCtxList      keyContext = iota // mail list, no mail open
	keyCtxMessage                     // a mail is open in the reading pane
	keyCtxSelection                   // mails are checked in the list
	keyCtxCalendar
	keyCtxContacts
	keyCtxOffline
	keyCtxModal
)

// batchKeyConfirmWindow is how long a first "d" on several checked mails stays armed.
const batchKeyConfirmWindow = 4 * time.Second

// readerScrollStep is how far Shift+J/K scroll the reading pane.
const readerScrollStep = 180

func (a *App) modalOpen() bool {
	if !a.detachedAuxWindows && (a.composeOpen || a.eventOpen) {
		return true
	}
	return a.evDetailOpen || a.quickRuleOpen || a.calDelOpen || a.helpOpen || a.moveOpen || a.arch.open || a.settings.open || a.direct.open
}

func (a *App) currentKeyContext() keyContext {
	switch {
	case a.modalOpen():
		return keyCtxModal
	case !a.engineReady:
		return keyCtxOffline
	case a.navID == "calendar":
		return keyCtxCalendar
	case a.navID == "contacts":
		return keyCtxContacts
	case len(a.checkedIDs) > 0:
		return keyCtxSelection
	case a.selectedID != "":
		return keyCtxMessage
	}
	return keyCtxList
}

// footerHints returns the shortcut legend for the active context, most important first; the
// footer drops entries from the end when the window is too narrow. Write actions are left out
// in read-only mode, where their keys only report that the change is blocked.
func (a *App) footerHints() []keyHint {
	h := func(keys, de, en string) keyHint { return keyHint{Keys: keys, Label: a.tr(de, en)} }
	ro := a.readOnly
	var out []keyHint
	switch a.currentKeyContext() {
	case keyCtxModal:
		if a.moveOpen {
			out = append(out, h("↑/↓", "Ordner wählen", "choose folder"), h("↩", "Verschieben", "move"))
		}
		out = append(out, h("esc", "Schließen", "close"))
	case keyCtxOffline:
		out = append(out, h("⌘R", "Neu verbinden", "reconnect"), h("h", "Hilfe", "help"))
	case keyCtxCalendar:
		if !ro {
			out = append(out, h("n", "Neuer Termin", "new event"))
		}
		out = append(out, h("c", "Verfassen", "compose"), h("⌘R", "Aktualisieren", "refresh"), h("h", "Hilfe", "help"))
	case keyCtxContacts:
		out = append(out, h("n", "Neuer Kontakt", "new contact"), h("c", "Verfassen", "compose"), h("⌘R", "Aktualisieren", "refresh"), h("h", "Hilfe", "help"))
	case keyCtxSelection:
		out = append(out, h("x", "Markieren", "check"), h("shift+x", "Bereich", "range"))
		if !ro {
			out = append(out, h("e", "Archivieren", "archive"), h("d", "Papierkorb", "trash"), h("v", "Verschieben", "move"), h("u", "Gelesen", "mark read"), h("s", "Stern", "star"))
		}
		out = append(out, h("q", "Quick-Regel", "quick rule"), h("esc", "Auswahl aufheben", "clear"))
	case keyCtxMessage:
		out = append(out, h("j/k", "Nächste/Vorige", "next/prev"), h("r", "Antworten", "reply"))
		if !ro {
			out = append(out, h("e", "Archivieren", "archive"), h("d", "Papierkorb", "trash"), h("v", "Verschieben", "move"), h("s", "Stern", "star"), h("u", "Gelesen/Ungelesen", "read/unread"))
		}
		out = append(out, h("shift+j/k", "Scrollen", "scroll"), h("x", "Markieren", "check"), h("esc", "Zurück", "back"))
	default:
		out = append(out, h("j/k", "Auswählen", "select"), h("x", "Markieren", "check"), h("/", "Suchen", "search"), h("c", "Verfassen", "compose"), h("h", "Hilfe", "help"))
	}
	return out
}

// estimateHintWidth approximates the rendered width of one hint (keycap + label + gap) in DIPs.
func estimateHintWidth(h keyHint) float32 {
	return float32(utf8.RuneCountInString(h.Keys))*6.8 + 12 + float32(utf8.RuneCountInString(h.Label))*6.2 + 16
}

// fitHints keeps the leading hints that fit into avail DIPs.
func fitHints(hints []keyHint, avail float32) []keyHint {
	used := float32(0)
	for i, h := range hints {
		used += estimateHintWidth(h)
		if used > avail {
			return hints[:i]
		}
	}
	return hints
}

// handleShortcuts processes the window's keyboard shortcuts (UI thread, once per frame).
func (a *App) handleShortcuts(c *ui.Context) {
	ctx := context.Background()

	if c.Shortcut(ui.Cmd, ui.KeyK) {
		a.focusSearch = true
	}
	if c.Shortcut(ui.Cmd, ui.KeyJ) {
		a.showCopilot = !a.showCopilot
		if a.showCopilot {
			a.focusChat = true
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		a.RefreshAll(ctx)
	}
	if c.Shortcut(ui.Cmd, ui.KeyN) {
		a.OpenComposeNew()
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) && !a.modalOpen() {
		a.OpenSettings()
	}
	if c.Shortcut(ui.Cmd, ui.KeyZ) && !a.modalOpen() && a.undo != nil {
		a.PerformUndo(ctx)
	}
	if c.Shortcut(ui.Cmd, ui.KeyP) && !a.modalOpen() && a.selectedMsg != nil {
		a.PrintSelectedMessage(ctx)
	}
	// Both calls register every frame; "/" needs Shift on German layouts.
	helpPlain := c.Shortcut(ui.Cmd, ui.KeySlash)
	helpShift := c.Shortcut(ui.Cmd|ui.Shift, ui.KeySlash)
	if helpPlain || helpShift {
		a.toggleHelp("all")
	}

	searchPlain := c.Shortcut(0, ui.KeySlash)
	searchShift := c.Shortcut(ui.Shift, ui.KeySlash)
	if searchPlain || searchShift {
		a.focusSearch = true
	}
	if c.Shortcut(0, ui.KeyC) {
		a.OpenComposeNew()
	}
	if c.Shortcut(0, ui.KeyH) {
		a.toggleHelp("shortcuts")
	}

	switch a.currentKeyContext() {
	case keyCtxCalendar:
		if c.Shortcut(0, ui.KeyN) && !a.readOnly {
			a.OpenNewEventModal(nil)
		}
		return
	case keyCtxContacts:
		if c.Shortcut(0, ui.KeyN) && a.isDirectMode() {
			a.startNewContact()
		}
		return
	case keyCtxList, keyCtxMessage, keyCtxSelection:
	default:
		return
	}

	if c.Shortcut(0, ui.KeyJ) {
		a.moveSelection(ctx, 1)
	}
	if c.Shortcut(0, ui.KeyK) {
		a.moveSelection(ctx, -1)
	}
	if c.Shortcut(ui.Shift, ui.KeyJ) {
		a.scrollReader(1)
	}
	if c.Shortcut(ui.Shift, ui.KeyK) {
		a.scrollReader(-1)
	}
	if c.Shortcut(0, ui.KeyX) {
		a.toggleCheckCurrent(false)
	}
	if c.Shortcut(ui.Shift, ui.KeyX) {
		a.toggleCheckCurrent(true)
	}
	if c.Shortcut(0, ui.KeyS) {
		a.starKey(ctx)
	}
	if c.Shortcut(0, ui.KeyU) {
		a.readKey(ctx)
	}
	if c.Shortcut(0, ui.KeyD) {
		a.trashKey(ctx)
	}
	if c.Shortcut(0, ui.KeyE) {
		a.archiveKey(ctx)
	}
	if c.Shortcut(0, ui.KeyV) {
		a.OpenMovePicker()
	}
	if c.Shortcut(0, ui.KeyR) && a.selectedMsg != nil {
		a.OpenComposeReply(a.selectedMsg, "")
	}
	if c.Shortcut(0, ui.KeyQ) && (len(a.checkedIDs) > 0 || a.selectedID != "") {
		a.OpenQuickRuleWizard()
	}
	// Escape is only claimed while there is something to go back from, so it still reaches the
	// window (e.g. leaving full screen) otherwise.
	if len(a.checkedIDs) > 0 || a.selectedID != "" {
		if c.Shortcut(0, ui.KeyEscape) {
			a.escapeKey()
		}
	}
}

func (a *App) toggleHelp(topic string) {
	if a.helpOpen {
		a.helpOpen = false
		return
	}
	a.helpTopic = topic
	a.widgets.helpScroll.Y = 0
	a.helpOpen = true
}

// currentIndex is the list row the keyboard acts on: the open mail, else the list cursor.
func (a *App) currentIndex() int {
	if a.selectedID != "" {
		for i, m := range a.messages {
			if m.ID == a.selectedID {
				return i
			}
		}
	}
	if a.mailList.selected >= 0 && a.mailList.selected < len(a.messages) {
		return a.mailList.selected
	}
	return -1
}

// currentHeader returns the list header of the open mail (nil if it is not listed).
func (a *App) currentHeader() *backend.MessageHeader {
	if a.selectedID == "" {
		return nil
	}
	if a.selectedMsg != nil && a.selectedMsg.ID == a.selectedID {
		return &a.selectedMsg.MessageHeader
	}
	for i := range a.messages {
		if a.messages[i].ID == a.selectedID {
			return &a.messages[i]
		}
	}
	return nil
}

// moveSelection opens the next (delta>0) or previous mail of the list.
func (a *App) moveSelection(ctx context.Context, delta int) {
	n := len(a.messages)
	if n == 0 {
		return
	}
	cur := a.currentIndex()
	next := 0
	switch {
	case cur < 0 && delta < 0:
		next = n - 1
	case cur < 0:
		next = 0
	default:
		next = min(max(cur+delta, 0), n-1)
	}
	if next == cur && a.selectedID == a.messages[next].ID {
		return
	}
	a.SelectMessage(ctx, a.messages[next].ID)
	a.mailList.state.ScrollIntoView(next)
}

func (a *App) scrollReader(dir int) {
	st := &a.widgets.inspectorScroll
	y := st.Y + float32(dir)*readerScrollStep
	if st.MaxY > 0 {
		y = min(y, st.MaxY)
	}
	st.ScrollTo(st.X, max(y, 0), ui.Smooth)
}

func (a *App) toggleCheckCurrent(shiftRange bool) {
	if idx := a.currentIndex(); idx >= 0 {
		a.ToggleMailChecked(idx, shiftRange)
	}
}

func (a *App) blockedReadOnly() bool {
	if a.readOnly {
		a.toast(a.tr("Read-Only-Modus aktiv — Änderung blockiert", "Read-only mode active — change blocked"), "error")
		return true
	}
	return false
}

// starKey stars all checked mails, or toggles the star of the open mail.
func (a *App) starKey(ctx context.Context) {
	if a.blockedReadOnly() {
		return
	}
	if ids := a.CheckedMessageIDs(); len(ids) > 0 {
		on := true
		a.PerformBatchAction(ctx, ids, backend.MessageActionsRequest{Flagged: &on}, a.tr("Mit Stern markiert", "Starred"))
		return
	}
	h := a.currentHeader()
	if h == nil {
		return
	}
	target := !h.Flagged
	a.PerformMessageAction(ctx, h.ID, backend.MessageActionsRequest{Flagged: &target}, a.tr("Markierung aktualisiert", "Star updated"))
}

// readKey marks all checked mails read, or toggles read/unread on the open mail.
func (a *App) readKey(ctx context.Context) {
	if a.blockedReadOnly() {
		return
	}
	if ids := a.CheckedMessageIDs(); len(ids) > 0 {
		on := true
		a.PerformBatchAction(ctx, ids, backend.MessageActionsRequest{Read: &on}, a.tr("Als gelesen markiert", "Marked as read"))
		return
	}
	h := a.currentHeader()
	if h == nil {
		return
	}
	target := h.Unread
	a.PerformMessageAction(ctx, h.ID, backend.MessageActionsRequest{Read: &target}, a.tr("Lesestatus aktualisiert", "Read status updated"))
}

// trashKey moves the open mail to the trash and opens its neighbour. Several checked mails need
// a second "d" within batchKeyConfirmWindow, so one stray key never empties a whole selection.
func (a *App) trashKey(ctx context.Context) {
	if ids := a.CheckedMessageIDs(); len(ids) > 0 {
		a.trashMails(ctx, ids, "")
		return
	}
	if a.selectedID != "" {
		a.trashMails(ctx, nil, a.selectedID)
	}
}

// trashMails moves the checked mails (ids) or the open one (single) to the trash. Several mails,
// and mails already in the trash – those are deleted for good – need a second press.
func (a *App) trashMails(ctx context.Context, ids []string, single string) {
	if a.blockedReadOnly() {
		return
	}
	all := ids
	if single != "" {
		all = []string{single}
	}
	permanent := false
	for _, id := range all {
		if a.isTrashFolder(a.listedFolderID(id)) {
			permanent = true
		}
	}
	if (len(all) > 1 || permanent) && !a.confirmKey("trash", all) {
		if permanent {
			a.toast(a.trf("Nochmal drücken, um %d Mail(s) endgültig zu löschen", "Press again to delete %d email(s) for good", len(all)), "warning")
		} else {
			a.toast(a.trf("Nochmal drücken, um %d Mails in den Papierkorb zu verschieben", "Press again to move %d emails to Trash", len(all)), "warning")
		}
		return
	}
	done := a.tr("In Papierkorb verschoben", "Moved to Trash")
	if permanent {
		done = a.tr("Endgültig gelöscht", "Deleted for good")
	}
	trash := backend.MessageActionsRequest{Trash: true}
	if single == "" {
		a.PerformBatchAction(ctx, ids, trash, done)
		return
	}
	a.performMessageAction(ctx, single, trash, done, a.neighborAfterRemoval(single))
}

// listedFolderID is the folder of a listed mail ("" if it is not listed).
func (a *App) listedFolderID(id string) string {
	for _, m := range a.messages {
		if m.ID == id {
			return m.FolderID
		}
	}
	if a.selectedMsg != nil && a.selectedMsg.ID == id {
		return a.selectedMsg.FolderID
	}
	return ""
}

func (a *App) isTrashFolder(folderID string) bool {
	if folderID == "" {
		return false
	}
	for _, f := range a.folders {
		if f.ID == folderID {
			return strings.EqualFold(f.Type, "trash")
		}
	}
	return false
}

// confirmKey implements "press again to confirm" for key actions on several mails: the first
// call arms action for exactly these ids and returns false, a second call within
// batchKeyConfirmWindow returns true and disarms.
func (a *App) confirmKey(action string, ids []string) bool {
	sig := action + ":" + strings.Join(ids, ",")
	now := time.Now()
	if a.keyArmSig == sig && now.Sub(a.keyArmAt) <= batchKeyConfirmWindow {
		a.keyArmSig = ""
		return true
	}
	a.keyArmSig, a.keyArmAt = sig, now
	return false
}

// neighborAfterRemoval returns the mail to open once id leaves the list: the next one, else the
// previous one ("" if id is the only or an unlisted mail).
func (a *App) neighborAfterRemoval(id string) string {
	for i, m := range a.messages {
		if m.ID != id {
			continue
		}
		if i+1 < len(a.messages) {
			return a.messages[i+1].ID
		}
		if i > 0 {
			return a.messages[i-1].ID
		}
		return ""
	}
	return ""
}

// escapeKey clears the checked mails, else closes the open mail (the list cursor stays).
func (a *App) escapeKey() {
	if len(a.checkedIDs) > 0 {
		a.ClearMailSelection()
		return
	}
	if a.selectedID != "" {
		a.selectedID = ""
		a.selectedMsg = nil
		a.inspectorSeq++
		a.inspectorLoading = false
		a.inspectorError = ""
		a.syncPreviewForMessage(nil)
	}
}

// shortcutHelpRows is the full keyboard reference shown in the help modal.
func (a *App) shortcutHelpRows() []keyHint {
	h := func(keys, de, en string) keyHint { return keyHint{Keys: keys, Label: a.tr(de, en)} }
	return []keyHint{
		h("j / k", "Nächste / vorige Mail öffnen", "Open next / previous email"),
		h("shift+j / k", "Lesebereich scrollen", "Scroll the reading pane"),
		h("x / shift+x", "Mail markieren / Bereich markieren", "Check email / check range"),
		h("s", "Stern setzen/entfernen (markierte: Stern setzen)", "Toggle star (checked: add star)"),
		h("u", "Gelesen/ungelesen (markierte: gelesen)", "Read/unread (checked: mark read)"),
		h("d", "In den Papierkorb, öffnet die nächste Mail · mehrere markierte: d zweimal", "Move to Trash and open the next email · several checked: press d twice"),
		h("e", "Archivieren, öffnet die nächste Mail", "Archive and open the next email"),
		h("v", "Verschieben nach… (Ordner desselben Kontos)", "Move to… (folders of the same account)"),
		h("r", "Antworten", "Reply"),
		h("q", "Quick-Regel aus Auswahl", "Quick rule from selection"),
		h("esc", "Auswahl aufheben / Mail schließen", "Clear selection / close email"),
		h("/  ·  ⌘K", "Suche", "Search"),
		h("c  ·  ⌘N", "Neue E-Mail", "New email"),
		h("n", "Neuer Termin (Kalender)", "New event (calendar)"),
		h("⌘R", "Aktualisieren", "Refresh"),
		h("⌘Z", "Letzte Aktion rückgängig machen", "Undo last mail action"),
		h("⌘P", "Geöffnete Mail drucken", "Print open email"),
		h("⌘J", "KI-Assistent ein/aus", "Toggle AI Assistant"),
		h("⌘↩", "E-Mail / Chat-Nachricht senden", "Send email / chat message"),
		h("h  ·  ⌘/", "Hilfe / diese Übersicht", "Help / this overview"),
		h("⌘/shift-Klick", "Mehrfachauswahl in der Liste", "Multi-select in the list"),
	}
}

func (a *App) renderShortcutHelp(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
		ui.Text(c, a.tr("6. Tastenkürzel", "6. Keyboard Shortcuts")).FontSize(12.5).Bold()
		ui.Text(c, a.tr(
			"Einzeltasten wirken nur, wenn kein Eingabefeld aktiv ist. Die Fußleiste zeigt jeweils die passenden Kürzel.",
			"Single keys only act while no text field has focus. The footer always shows the shortcuts that apply.",
		)).FontSize(11.5).TextColor(t.TextMuted)
		for _, row := range a.shortcutHelpRows() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(110).Shrink(0).Children(func() {
					ui.Text(c, row.Keys).Font("monospace").FontSize(11).Bold().SingleLine().
						Padding(1, 6).Radius(4).Background(t.Surface).Border(1, t.Border)
				})
				ui.Text(c, row.Label).FontSize(12).TextColor(t.TextMuted).Grow(1)
			})
		}
	})
}
