package nativeui

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// "Move to…" picker (v) and archive (e)
// -------------------------------------
// The bridge only moves mails inside their own account (cross_account_move), so the picker lists
// the folders of that one account and refuses a selection that spans several accounts. Archive
// uses the account's archive folder; the engine decides the target folder.

type movePickerState struct {
	moveOpen    bool
	moveIDs     []string // mails to move; one mail = the open one (its neighbour opens after)
	moveBatch   bool     // the IDs come from the checked selection
	moveAccount string
	moveFilter  string
	moveCursor  int
	moveFocus   bool
}

// moveTargetsAccount returns the mails a move/archive acts on and their single account ("" and
// ok=false when they span several accounts or nothing is selected).
func (a *App) moveTargets() (ids []string, batch bool, account string, ok bool) {
	if checked := a.CheckedMessages(); len(checked) > 0 {
		for _, m := range checked {
			if account != "" && m.AccountID != account {
				return nil, true, "", false
			}
			account = m.AccountID
			ids = append(ids, m.ID)
		}
		return ids, true, account, true
	}
	if h := a.currentHeader(); h != nil {
		return []string{h.ID}, false, h.AccountID, true
	}
	return nil, false, "", false
}

// OpenMovePicker opens the folder picker for the checked mails or the open mail (UI thread).
func (a *App) OpenMovePicker() {
	if a.blockedReadOnly() {
		return
	}
	ids, batch, account, ok := a.moveTargets()
	a.openMovePickerFor(ids, batch, account, ok)
}

// accountOfMails returns the one account the listed mails ids belong to (ok=false when they
// span several accounts or none is listed).
func (a *App) accountOfMails(ids []string) (account string, ok bool) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	for _, m := range a.messages {
		if !want[m.ID] {
			continue
		}
		if account != "" && m.AccountID != account {
			return "", false
		}
		account = m.AccountID
	}
	if account == "" && a.selectedMsg != nil && len(ids) == 1 && a.selectedMsg.ID == ids[0] {
		account = a.selectedMsg.AccountID
	}
	return account, account != ""
}

// openMovePickerFor opens the folder picker for ids (batch: they are the checked selection).
func (a *App) openMovePickerFor(ids []string, batch bool, account string, ok bool) {
	if a.blockedReadOnly() {
		return
	}
	if !ok {
		if batch {
			a.toast(a.tr("Verschieben geht nur innerhalb eines Kontos — markiere Mails aus einem Konto.", "Moving only works within one account — select emails from a single account."), "warning")
		}
		return
	}
	if len(a.moveFolderRows()) == 0 && account != "" {
		// Folder list not loaded for this account (e.g. account filter set to another one).
		has := false
		for _, f := range a.folders {
			if f.AccountID == account {
				has = true
				break
			}
		}
		if !has {
			a.toast(a.tr("Ordner dieses Kontos sind nicht geladen — Kontofilter prüfen.", "Folders of this account are not loaded — check the account filter."), "warning")
			return
		}
	}
	a.moveIDs, a.moveBatch, a.moveAccount = ids, batch, account
	a.moveFilter, a.moveCursor = "", 0
	a.moveOpen, a.moveFocus = true, true
}

// moveFolderRows lists the target folders of the picker: the account's tree, or a flat list of
// matches while a filter is typed.
func (a *App) moveFolderRows() []folderRow {
	var acc []backend.Folder
	for _, f := range a.folders {
		if f.AccountID == a.moveAccount {
			acc = append(acc, f)
		}
	}
	rows := buildFolderRows(acc, nil)
	q := strings.ToLower(strings.TrimSpace(a.moveFilter))
	if q == "" {
		return rows
	}
	out := rows[:0:0]
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Folder.Name), q) || strings.Contains(strings.ToLower(cleanFolderPath(r.Folder.Path)), q) {
			r.Depth = 0
			out = append(out, r)
		}
	}
	return out
}

// sourceFolderID is the folder the picked mails are in when they share one ("" otherwise).
func (a *App) moveSourceFolderID() string {
	src := ""
	for _, id := range a.moveIDs {
		fid := ""
		for _, m := range a.messages {
			if m.ID == id {
				fid = m.FolderID
				break
			}
		}
		if fid == "" || (src != "" && fid != src) {
			return ""
		}
		src = fid
	}
	return src
}

// MoveTo moves the picked mails into folder f and closes the picker (UI thread).
func (a *App) MoveTo(ctx context.Context, f backend.Folder) {
	ids, batch := a.moveIDs, a.moveBatch
	a.moveOpen = false
	a.moveMailsTo(ctx, ids, batch, f)
}

// moveMailsTo moves ids into folder f, with Undo: the picker's and drag and drop's path. A
// single open mail makes way for its neighbour.
func (a *App) moveMailsTo(ctx context.Context, ids []string, batch bool, f backend.Folder) {
	if len(ids) == 0 {
		return
	}
	name := f.Name
	if name == "" {
		name = cleanFolderPath(f.Path)
	}
	patch := backend.MessageActionsRequest{MoveToFolderID: f.ID}
	msg := a.tr("Verschoben nach ", "Moved to ") + name
	if batch {
		a.PerformBatchAction(ctx, ids, patch, msg)
		return
	}
	a.performMessageAction(ctx, ids[0], patch, msg, a.neighborAfterRemoval(ids[0]))
}

// archiveKey archives the checked mails or the open mail (whose neighbour opens after).
func (a *App) archiveKey(ctx context.Context) {
	if a.blockedReadOnly() {
		return
	}
	archive := backend.MessageActionsRequest{Archive: true}
	if ids := a.CheckedMessageIDs(); len(ids) > 0 {
		a.PerformBatchAction(ctx, ids, archive, a.tr("Archiviert", "Archived"))
		return
	}
	if a.selectedID == "" {
		return
	}
	a.performMessageAction(ctx, a.selectedID, archive, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(a.selectedID))
}

func (a *App) renderMovePicker(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.moveOpen, func() {
		rows := a.moveFolderRows()
		if a.moveCursor >= len(rows) {
			a.moveCursor = max(len(rows)-1, 0)
		}
		src := a.moveSourceFolderID()
		ui.Column(c).Width(a.modalW(440)).MaxHeight(a.modalH(c, 560)).Padding(16).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconFolderInput).Size(16, 16).TextColor(t.Accent)
				title := a.tr("Verschieben nach…", "Move to…")
				if len(a.moveIDs) > 1 {
					title = a.trf("%d Mails verschieben nach…", "Move %d emails to…", len(a.moveIDs))
				}
				ui.Text(c, title).FontSize(15).Bold().Grow(1)
				ui.Text(c, a.accountDisplayName(a.moveAccount)).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			})
			ti := ui.TextInput(c.Key("move-filter"), &a.moveFilter).Placeholder(a.tr("Ordner filtern…", "Filter folders…")).FillWidth()
			if a.moveFocus {
				ti.Focus()
				a.moveFocus = false
			}
			if ti.Changed() {
				a.moveCursor = 0
			}
			if ti.Shortcut(0, ui.KeyDown) && a.moveCursor < len(rows)-1 {
				a.moveCursor++
			}
			if ti.Shortcut(0, ui.KeyUp) && a.moveCursor > 0 {
				a.moveCursor--
			}
			if ti.Submitted() && a.moveCursor < len(rows) && rows[a.moveCursor].Folder.ID != src {
				a.MoveTo(context.Background(), rows[a.moveCursor].Folder)
				return
			}

			ui.Scroll(c.Key("move-scroll")).Grow(1).MinHeight(0).FillWidth().MaxHeight(380).Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					if len(rows) == 0 {
						ui.Text(c, a.tr("Kein passender Ordner", "No matching folder")).FontSize(12).TextColor(t.TextMuted).Padding(8)
					}
					for i, r := range rows {
						f := r.Folder
						current := f.ID == src
						btn := ui.ButtonBase(c.Key("move-"+f.ID)).FillWidth().Padding(6, 8, 6, 8+float32(r.Depth)*14).Radius(6).Gap(8).AlignItems(ui.Center).Label(f.Name)
						if i == a.moveCursor {
							btn.Background(withAlpha(t.Accent, 0.16))
						}
						btn.Children(func() {
							ui.Icon(c, folderIcon(f)).Size(14, 14).TextColor(t.Accent)
							label := f.Name
							if a.moveFilter != "" {
								label = cleanFolderPath(f.Path)
							}
							txt := ui.Text(c, label).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
							if current {
								txt.TextColor(t.TextMuted)
								ui.Text(c, a.tr("aktueller Ordner", "current folder")).FontSize(11).TextColor(t.TextMuted)
							}
						})
						if current {
							btn.Disabled(true)
						}
						if btn.Clicked() && !current {
							a.MoveTo(context.Background(), f)
						}
					}
				})
			})
			ui.Text(c, a.tr("Nur Ordner desselben Kontos — die Bridge verschiebt nie kontoübergreifend.", "Only folders of the same account — the bridge never moves across accounts.")).FontSize(11).TextColor(t.TextMuted)
		})
	})
}
