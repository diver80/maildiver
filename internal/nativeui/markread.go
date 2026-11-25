package nativeui

import (
	"context"
	"strings"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

// pendingReadOp tracks an in-flight read/unread mutation so concurrent message-list loads
// cannot overwrite the UI with stale SQLite state, and out-of-order completions cannot roll
// back a newer user toggle.
type pendingReadOp struct {
	read bool
	seq  uint64
}

// markReadDelay is how long a mail must stay open with MarkRead "delay".
var markReadDelay = 3 * time.Second

func (a *App) beginPendingRead(id string, read bool) uint64 {
	if a.pendingRead == nil {
		a.pendingRead = make(map[string]pendingReadOp)
	}
	a.readOpSeq++
	seq := a.readOpSeq
	a.pendingRead[id] = pendingReadOp{read: read, seq: seq}
	return seq
}

func (a *App) endPendingRead(id string, seq uint64) bool {
	op, ok := a.pendingRead[id]
	if !ok || op.seq != seq {
		return false
	}
	delete(a.pendingRead, id)
	return true
}

// applyPendingReads overlays in-flight read/unread changes onto a freshly fetched list (UI thread).
// In Focus (Unread & ★), it also drops non-open items that just became read+unflagged and returns
// the net delta to apply to the server's total count so stale SQLite snapshots never inflate Focus.
func (a *App) applyPendingReads(navID string, msgs []backend.MessageHeader) ([]backend.MessageHeader, int) {
	if len(a.pendingRead) == 0 {
		return msgs, 0
	}
	totalDelta := 0
	out := make([]backend.MessageHeader, 0, len(msgs))
	for _, m := range msgs {
		if op, ok := a.pendingRead[m.ID]; ok {
			if m.Unread == op.read && !m.Flagged {
				if op.read {
					totalDelta--
				} else {
					totalDelta++
				}
			}
			m.Read = op.read
			m.Unread = !op.read
			if navID == "focus" && !m.Unread && !m.Flagged && m.ID != a.selectedID {
				continue
			}
		}
		out = append(out, m)
	}
	return out, totalDelta
}

// scheduleMarkRead marks the mail the user just opened as read, like mail clients do: at once,
// after a delay, or never (☰ → Settings). Nothing happens in read-only mode or for accounts
// PostDive may not change – there the mail stays unread (UI thread).
func (a *App) scheduleMarkRead(ctx context.Context, id string) {
	mode := a.cfg.App.MarkRead
	if mode == config.MarkReadNever || a.readOnly {
		return
	}
	h := a.currentHeader()
	if h == nil || h.ID != id || !h.Unread || !a.cfg.Mailbox.CanWrite(h.AccountID) {
		return
	}
	a.markReadSeq++
	seq := a.markReadSeq
	if mode != config.MarkReadDelay {
		a.markReadSilently(ctx, id)
		return
	}
	time.AfterFunc(markReadDelay, func() {
		a.emitUpdate(func() {
			// Still the same opening of the same mail.
			if seq == a.markReadSeq && a.selectedID == id {
				a.markReadSilently(ctx, id)
			}
		})
	})
}

// markReadSilently marks a mail read without a toast; the list updates at once and is reset if
// the engine refuses (UI thread).
func (a *App) markReadSilently(ctx context.Context, id string) {
	bc, err := a.client()
	if err != nil {
		return
	}
	read := true
	patch := backend.MessageActionsRequest{Read: &read, ExpectedMessageID: a.messageIDFor(id)}
	opSeq := a.beginPendingRead(id, true)
	a.setLocalRead(id, true)
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		_, err := bc.MessageActions(c, id, patch)
		a.emitUpdate(func() {
			stillCurrent := a.endPendingRead(id, opSeq)
			if err != nil {
				if stillCurrent {
					a.setLocalRead(id, false)
				}
				return
			}
			a.refreshFoldersBackground(ctx)
		})
	}()
}

// setLocalRead updates the read state and unread counters shown for a mail before the engine
// confirms it (UI thread).
func (a *App) setLocalRead(id string, read bool) {
	changed := false
	var folderID, accountID, folderPath string
	var flagged bool
	for i := range a.messages {
		if a.messages[i].ID == id && a.messages[i].Unread == read {
			a.messages[i].Read, a.messages[i].Unread = read, !read
			folderID = a.messages[i].FolderID
			accountID = a.messages[i].AccountID
			folderPath = a.messages[i].Folder
			flagged = a.messages[i].Flagged
			changed = true
		}
	}
	if a.selectedMsg != nil && a.selectedMsg.ID == id {
		if !changed && a.selectedMsg.Unread == read {
			folderID = a.selectedMsg.FolderID
			accountID = a.selectedMsg.AccountID
			folderPath = a.selectedMsg.Folder
			flagged = a.selectedMsg.Flagged
			changed = true
		}
		a.selectedMsg.Read, a.selectedMsg.Unread = read, !read
	}
	if changed {
		delta := 1
		if read {
			delta = -1
		}
		// Keep sidebar folder unread badge in sync immediately.
		for i := range a.folders {
			f := &a.folders[i]
			if (folderID != "" && f.ID == folderID) ||
				(accountID != "" && folderPath != "" && f.AccountID == accountID && strings.EqualFold(f.Path, folderPath)) {
				f.UnreadCount = max(f.UnreadCount+delta, 0)
				break
			}
		}
		// Keep Focus counts in sync (excluding Trash/Junk which are never in Focus).
		if a.navID == "focus" || !a.isTrashOrJunkPath(accountID, folderPath) {
			a.focusUnread = max(a.focusUnread+delta, 0)
			if !flagged {
				a.focusTotal = max(a.focusTotal+delta, 0)
				if a.navID == "focus" {
					a.totalMessages = max(a.totalMessages+delta, 0)
				}
			}
		}
		if a.navID == "focus" {
			if read {
				if a.focusReadIDs == nil {
					a.focusReadIDs = make(map[string]bool)
				}
				a.focusReadIDs[id] = true
			} else {
				delete(a.focusReadIDs, id)
			}
		}
	}
	a.notifyTray()
}

// pruneFocusGhost removes a mail from Focus (Unread & ★) once it is no longer open and was
// marked read during this Focus session (read and unflagged) (UI thread).
func (a *App) pruneFocusGhost(id string) {
	if a.navID != "focus" || id == "" || id == a.selectedID || !a.focusReadIDs[id] {
		return
	}
	delete(a.focusReadIDs, id)
	idx := -1
	for i, m := range a.messages {
		if m.ID == id {
			if !m.Unread && !m.Flagged {
				idx = i
			}
			break
		}
	}
	if idx < 0 {
		return
	}
	a.messages = append(a.messages[:idx], a.messages[idx+1:]...)
	delete(a.checkedIDs, id)
	a.lastCheckedIndex = -1
	if a.selectedID != "" {
		a.mailList.selected = -1
		for i, m := range a.messages {
			if m.ID == a.selectedID {
				a.mailList.selected = i
				break
			}
		}
	} else if a.mailList.selected >= len(a.messages) {
		a.mailList.selected = len(a.messages) - 1
	}
}

// keepOpenMail keeps the open mail in the unread view after it was marked read, at its place,
// so j/k go on from there; it leaves the list when you move to another mail or leave the view (UI thread).
func (a *App) keepOpenMail(msgs []backend.MessageHeader) []backend.MessageHeader {
	if a.selectedID == "" {
		return msgs
	}
	present := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.ID == a.selectedID {
			return msgs
		}
		present[m.ID] = true
	}
	pos, found := 0, false
	var open backend.MessageHeader
	for _, m := range a.messages {
		if m.ID == a.selectedID {
			open, found = m, true
			break
		}
		if present[m.ID] {
			pos++
		}
	}
	if !found {
		return msgs
	}
	out := make([]backend.MessageHeader, 0, len(msgs)+1)
	out = append(out, msgs[:pos]...)
	out = append(out, open)
	return append(out, msgs[pos:]...)
}
