package nativeui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// Desktop mail list comfort: a right-click menu on every row and dragging rows onto sidebar
// folders. Both act on the checked selection when the row is part of it, else on that row
// alone, and go through the same paths as the selection bar, the keys and the move picker.

// draggedMails is the value a mail row drags: the IDs of the mails it stands for, joined by
// "\n" (a string, so the drag value stays comparable and cheap to rebuild every frame).
type draggedMails string

func (d draggedMails) ids() []string {
	if d == "" {
		return nil
	}
	return strings.Split(string(d), "\n")
}

// rowComposeReq is a reply/forward asked for on a row whose mail was not open yet: it runs once
// that mail's detail arrived (selectMessage).
type rowComposeReq struct {
	id   string
	kind string // "reply", "replyAll", "forward"
}

// rowTargets returns the mails an action on row m acts on: the checked selection when m is in
// it (batch), else m alone.
func (a *App) rowTargets(m backend.MessageHeader) (ids []string, batch bool) {
	if a.IsMailChecked(m.ID) {
		if checked := a.CheckedMessageIDs(); len(checked) > 0 {
			return checked, true
		}
	}
	return []string{m.ID}, false
}

// rowDragValue is what row m drags: the checked selection when m is in it, else m.
func (a *App) rowDragValue(m backend.MessageHeader) draggedMails {
	ids, _ := a.rowTargets(m)
	return draggedMails(strings.Join(ids, "\n"))
}

// composeFromRow replies to, replies all to or forwards the row's mail, opening it first when
// it is not the open one (the compose window needs its body).
func (a *App) composeFromRow(id, kind string) {
	if a.selectedMsg != nil && a.selectedMsg.ID == id {
		a.runRowCompose(kind, a.selectedMsg)
		return
	}
	a.rowCompose = rowComposeReq{id: id, kind: kind}
	a.SelectMessage(context.Background(), id)
}

func (a *App) runRowCompose(kind string, msg *backend.MessageDetail) {
	switch kind {
	case "replyAll":
		a.OpenComposeReplyAll(msg, "")
	case "forward":
		a.OpenComposeForward(msg)
	default:
		a.OpenComposeReply(msg, "")
	}
}

// takeRowCompose runs a pending row reply/forward once its mail loaded (UI thread).
func (a *App) takeRowCompose(msg *backend.MessageDetail) {
	if msg == nil || a.rowCompose.id != msg.ID {
		return
	}
	kind := a.rowCompose.kind
	a.rowCompose = rowComposeReq{}
	a.runRowCompose(kind, msg)
}

// headersFor returns the listed headers of ids in list order.
func (a *App) headersFor(ids []string) []backend.MessageHeader {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []backend.MessageHeader
	for _, m := range a.messages {
		if want[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// buildMailRowMenu fills the right-click menu of mail row m (desktop).
func (a *App) buildMailRowMenu(menu *ui.Menu, m backend.MessageHeader) {
	ctx := context.Background()
	ids, batch := a.rowTargets(m)
	if !batch {
		if menu.Item(a.tr("Antworten", "Reply")).ID("reply").Chosen() {
			a.composeFromRow(m.ID, "reply")
		}
		if menu.Item(a.tr("Allen antworten", "Reply All")).ID("reply-all").Chosen() {
			a.composeFromRow(m.ID, "replyAll")
		}
		if menu.Item(a.tr("Weiterleiten", "Forward")).ID("forward").Chosen() {
			a.composeFromRow(m.ID, "forward")
		}
		menu.Separator()
	}
	if !a.readOnly {
		hdrs := a.headersFor(ids)
		anyUnread, anyUnflagged := false, false
		for _, h := range hdrs {
			anyUnread = anyUnread || h.Unread
			anyUnflagged = anyUnflagged || !h.Flagged
		}
		readLbl := a.tr("Als gelesen markieren", "Mark as read")
		if !anyUnread {
			readLbl = a.tr("Als ungelesen markieren", "Mark as unread")
		}
		if menu.Item(readLbl).ID("read").Chosen() {
			target := anyUnread
			msg := a.tr("Als gelesen markiert", "Marked as read")
			if !target {
				msg = a.tr("Als ungelesen markiert", "Marked as unread")
			}
			if batch {
				a.PerformBatchAction(ctx, ids, backend.MessageActionsRequest{Read: &target}, msg)
			} else {
				a.PerformMessageAction(ctx, m.ID, backend.MessageActionsRequest{Read: &target}, msg)
			}
		}
		starLbl := a.tr("Mit Stern markieren", "Add star")
		if !anyUnflagged {
			starLbl = a.tr("Stern entfernen", "Remove star")
		}
		if menu.Item(starLbl).ID("star").Chosen() {
			flag := anyUnflagged
			if batch {
				a.PerformBatchAction(ctx, ids, backend.MessageActionsRequest{Flagged: &flag}, a.tr("Markierung aktualisiert", "Star updated"))
			} else {
				a.PerformMessageAction(ctx, m.ID, backend.MessageActionsRequest{Flagged: &flag}, a.tr("Markierung aktualisiert", "Star updated"))
			}
		}
		menu.Separator()
		if menu.Item(a.tr("Archivieren", "Archive")).ID("archive").Chosen() {
			archive := backend.MessageActionsRequest{Archive: true}
			if batch {
				a.PerformBatchAction(ctx, ids, archive, a.tr("Archiviert", "Archived"))
			} else {
				a.performMessageAction(ctx, m.ID, archive, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(m.ID))
			}
		}
		if menu.Item(a.tr("Verschieben nach…", "Move to…")).ID("move").Chosen() {
			account, ok := a.accountOfMails(ids)
			a.openMovePickerFor(ids, batch, account, ok)
		}
		anySnoozed := false
		nowUnix := time.Now().Unix()
		for _, h := range hdrs {
			if h.SnoozedUntil > nowUnix {
				anySnoozed = true
				break
			}
		}
		menu.Submenu(a.tr("Zurückstellen", "Snooze"), func(sm *ui.Menu) {
			a.appendSnoozeMenuItems(sm, ids, anySnoozed)
		})
		if a.isDirectMode() {
			if !batch && a.isJunkFolderID(m.FolderID) {
				if menu.Item(a.tr("Kein Spam", "Not spam")).ID("not-spam").Chosen() {
					a.MarkMailNotSpam(ctx, m.ID)
				}
			} else if menu.Item(a.tr("Als Spam markieren", "Mark as spam")).ID("spam").Chosen() {
				a.MarkMailsSpam(ctx, ids)
			}
		}
		delLbl := a.tr("Löschen", "Delete")
		if a.confirmArmed("trash", ids) {
			delLbl = a.tr("Wirklich löschen?", "Really delete?")
		}
		if menu.Item(delLbl).ID("trash").Danger().Chosen() {
			if batch {
				a.trashMails(ctx, ids, "")
			} else {
				a.trashMails(ctx, nil, m.ID)
			}
		}
		menu.Separator()
	}
	if menu.Item(a.tr("Quick-Regel…", "Quick Rule…")).ID("rule").Chosen() {
		a.OpenQuickRuleForMessages(a.headersFor(ids))
	}
}

// isJunkFolderID reports whether folderID is a junk/spam folder.
func (a *App) isJunkFolderID(folderID string) bool {
	for _, f := range a.folders {
		if f.ID == folderID {
			return strings.EqualFold(f.Type, "junk") || containsFold(f.SpecialUse, "junk")
		}
	}
	return false
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

// acceptMailDrop makes sidebar folder item take dragged mail rows: it highlights while mails
// hover over it and moves them there when dropped (desktop).
func (a *App) acceptMailDrop(c *ui.Context, item ui.Element, f backend.Folder) {
	if a.readOnly || a.isPhone() {
		return
	}
	t := c.Theme()
	if d, ok := ui.DragOver[draggedMails](item); ok && len(d.ids()) > 0 {
		item.Background(withAlpha(t.Accent, 0.22)).Border(1.5, t.Accent)
	}
	if d, ok := ui.Drop[draggedMails](item); ok {
		a.dropMailsOnFolder(context.Background(), d.ids(), f)
	}
}

// dropMailsOnFolder moves dropped mails into f, as the move picker would: within one account,
// never into the folder they are in.
func (a *App) dropMailsOnFolder(ctx context.Context, ids []string, f backend.Folder) {
	if len(ids) == 0 || a.blockedReadOnly() {
		return
	}
	account, ok := a.accountOfMails(ids)
	if !ok {
		a.toast(a.tr("Verschieben geht nur innerhalb eines Kontos — markiere Mails aus einem Konto.", "Moving only works within one account — select emails from a single account."), "warning")
		return
	}
	if account != f.AccountID {
		a.toast(a.tr("Mails lassen sich nur in Ordner desselben Kontos ziehen.", "Emails can only be dropped onto folders of the same account."), "warning")
		return
	}
	var moving []string
	for _, id := range ids {
		if a.listedFolderID(id) != f.ID {
			moving = append(moving, id)
		}
	}
	if len(moving) == 0 {
		return
	}
	// Several mails, or the checked selection, go the batch way (one Undo for all of them).
	batch := len(moving) > 1 || a.IsMailChecked(moving[0])
	a.moveMailsTo(ctx, moving, batch, f)
}
