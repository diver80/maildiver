package nativeui

import (
	"context"
	"time"

	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
)

// markReadDelay is how long a mail must stay open with MarkRead "delay".
var markReadDelay = 3 * time.Second

// scheduleMarkRead marks the mail the user just opened as read, like Thunderbird does: at once,
// after a delay, or never (☰ → Settings). Nothing happens in read-only mode or for accounts
// OmaThunder may not change – there the mail stays unread until read in Thunderbird (UI thread).
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
// Thunderbird refuses (UI thread).
func (a *App) markReadSilently(ctx context.Context, id string) {
	bc, err := a.client()
	if err != nil {
		return
	}
	read := true
	patch := bridgeclient.MessageActionsRequest{Read: &read, ExpectedMessageID: a.messageIDFor(id)}
	a.setLocalRead(id, true)
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		if _, err := bc.MessageActions(c, id, patch); err != nil {
			a.emitUpdate(func() { a.setLocalRead(id, false) })
		}
	}()
}

// setLocalRead updates the read state shown for a mail before Thunderbird confirms it (UI thread).
func (a *App) setLocalRead(id string, read bool) {
	for i := range a.messages {
		if a.messages[i].ID == id && a.messages[i].Unread == read {
			a.messages[i].Read, a.messages[i].Unread = read, !read
			if a.navID == "focus" {
				if read {
					a.focusUnread = max(a.focusUnread-1, 0)
				} else {
					a.focusUnread++
				}
			}
		}
	}
	if a.selectedMsg != nil && a.selectedMsg.ID == id {
		a.selectedMsg.Read, a.selectedMsg.Unread = read, !read
	}
	a.notifyTray()
}

// keepOpenMail keeps the open mail in the unread view after it was marked read, at its place,
// so j/k go on from there; it leaves the list when you leave the view (UI thread).
func (a *App) keepOpenMail(msgs []bridgeclient.MessageHeader) []bridgeclient.MessageHeader {
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
	var open bridgeclient.MessageHeader
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
	out := make([]bridgeclient.MessageHeader, 0, len(msgs)+1)
	out = append(out, msgs[:pos]...)
	out = append(out, open)
	return append(out, msgs[pos:]...)
}
