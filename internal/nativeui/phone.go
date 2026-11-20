package nativeui

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/mobile"

	"github.com/diver80/maildiver/internal/backend"
)

// The phone layout: one screen at a time, as mail apps on phones show it – mailboxes, then the
// mails of one, then one mail – with the system's back gesture going up again. It is used on
// Android and in any window narrower than phoneMaxWidth (a desktop window dragged narrow, the
// render tests). Compose fills the screen; the dialogs (settings, accounts, …) shrink to it.

const phoneMaxWidth = 640

// Routes of the phone layout.
const (
	phoneRouteMailboxes = "/"
	phoneRouteList      = "/list"
	phoneRouteMail      = "/list/mail"
)

type phoneUI struct {
	router  *ui.Router
	list    ui.ListState // the mail list, without the desktop's highlighted row
	refresh ui.RefreshState
	more    bool // the open mail's sheet of further actions
	search  bool // the search field above the list
}

// isPhone reports whether the window shows the phone layout.
func (a *App) isPhone() bool {
	return runtime.GOOS == "android" || (a.winW > 0 && a.winW < phoneMaxWidth)
}

// modalW is a dialog's width: w on desktops, on phones the screen less the margins and padding
// of ui.Modal's panel (40 points on each side).
func (a *App) modalW(w float32) float32 {
	if a.winW > 0 && a.winW-80 < w {
		return max(a.winW-80, 200)
	}
	return w
}

func (a *App) phoneRouter() *ui.Router {
	if a.phone.router == nil {
		a.phone.router = ui.NewRouter(phoneRouteMailboxes)
	}
	return a.phone.router
}

// renderPhone builds the phone layout (UI thread).
func (a *App) renderPhone(c *ui.Context) {
	t := c.Theme()
	if a.composeOpen && !a.detachedAuxWindows {
		// Compose fills the screen; back (Escape) closes it, asking first when it was changed.
		if c.Shortcut(0, ui.KeyEscape) {
			a.composeOpen = false
		}
		a.guardComposeClose()
		if a.composeOpen {
			ui.Scroll(c.Key("ph-compose")).Fill().Background(t.Surface).Children(func() {
				ui.Column(c).FillWidth().Padding(12).Gap(10).Children(func() {
					a.renderComposeContent(c, false)
				})
			})
			return
		}
	}
	r := a.phoneRouter()
	r.View(c, func(rt *ui.Route) {
		switch {
		case rt.Match(phoneRouteMail):
			rt.Title(a.tr("Mail", "Email"))
			a.renderPhoneMail(c)
		case rt.Match(phoneRouteList):
			rt.Title(a.currentViewTitle())
			a.renderPhoneList(c)
		default:
			rt.Title("MailDiver")
			a.renderPhoneMailboxes(c)
		}
	}).Fill()
}

// phoneBar is the bar on top of a phone screen: back (when there is a screen to go back to), the
// title and actions.
func (a *App) phoneBar(c *ui.Context, title string, back bool, trailing func()) {
	t := c.Theme()
	bg := t.Surface
	mobile.TopBar(c, mobile.TopBarOptions{
		Title:      title,
		Background: &bg,
		Leading: func() {
			if back {
				b := ui.ButtonBase(c.Key("ph-back")).Padding(10).Radius(20).Label(a.tr("Zurück", "Back"))
				b.Children(func() { ui.Icon(c, iconChevronLt).Size(22, 22) })
				if b.Clicked() {
					a.phoneRouter().Back()
				}
			}
		},
		Trailing: trailing,
	})
	ui.Divider(c)
}

// phoneIconButton is an icon a finger can tap (44 points).
func phoneIconButton(c *ui.Context, svg *ui.SVG, label string, fg ui.Color) ui.Element {
	b := ui.ButtonBase(c).Padding(11).Radius(22).Label(label)
	b.Children(func() {
		ic := ui.Icon(c, svg).Size(21, 21)
		if fg.A > 0 {
			ic.TextColor(fg)
		}
	})
	return b
}

// phoneOpenView shows the mails of a view or folder.
func (a *App) phoneOpenView(nav string) {
	a.selectedID, a.selectedMsg = "", nil
	a.setNav(nav)
	switch nav {
	case "calendar", "contacts":
		if nav == "contacts" {
			a.LoadContacts(context.Background())
		}
	default:
		a.LoadMessages(context.Background())
	}
	a.phoneRouter().Push(phoneRouteList)
}

// --- mailboxes -----------------------------------------------------------------------------

func (a *App) renderPhoneMailboxes(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.phoneBar(c, "MailDiver", false, func() {
			if a.isDirectMode() && phoneIconButton(c.Key("ph-accounts"), iconUsers, a.tr("Konten", "Accounts"), ui.Color{}).Clicked() {
				a.OpenDirectModal(0)
			}
			if phoneIconButton(c.Key("ph-settings"), iconSettings, a.tr("Einstellungen", "Settings"), ui.Color{}).Clicked() {
				a.OpenSettings()
			}
		})
		if !a.engineReady {
			a.renderOfflineBanner(c)
			return
		}
		if a.isDirectMode() && len(a.accounts) == 0 {
			ui.Scroll(c).Fill().Children(func() { a.renderDirectOnboarding(c) })
			return
		}
		if acc, msg := a.firstSyncError(); acc != "" {
			// Phones have no status bar: a failing account shows here.
			b := ui.ButtonBase(c.Key("ph-sync-err")).FillWidth().Padding(10, 16).Gap(4).Background(withAlpha(t.Danger, 0.14)).Label(msg)
			b.Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					ui.Text(c, a.tr("Sync-Fehler: ", "Sync error: ")+a.accountDisplayName(acc)).FontSize(14).Bold().TextColor(t.Danger)
					ui.Text(c, msg).FontSize(12.5).TextColor(t.TextMuted).MaxLines(3)
				})
			})
			if b.Clicked() && a.isDirectMode() {
				a.OpenDirectModal(0)
			}
		}
		box := ui.Scroll(c.Key("ph-boxes")).Fill()
		if box.PullToRefresh(&a.phone.refresh) {
			a.phoneSync()
		}
		box.Children(func() {
			ui.Column(c).FillWidth().Padding(8, 0, 96, 0).Gap(4).Children(func() {
				mobile.Section(c, a.tr("Arbeitsbereich", "Workspace"), func() {
					a.phoneBoxRow(c, "focus", iconFocus, a.tr("Fokus (Ungelesen)", "Focus (Unread)"), a.unreadCount(), 0)
					a.phoneBoxRow(c, "recent", iconMail, a.tr("Aktuelle Mails", "Recent Mail"), 0, 0)
					a.phoneBoxRow(c, "action", iconBolt, a.tr("Action / Warten", "Action / Waiting"), a.barSummary.ActionCount+a.barSummary.WaitingCount, 0)
					a.phoneBoxRow(c, "calendar", iconCal, a.tr("Kalender & Termine", "Calendar & Events"), len(a.todayEvents()), 0)
					if a.isDirectMode() {
						a.phoneBoxRow(c, "contacts", iconUsers, a.tr("Kontakte", "Contacts"), 0, 0)
					}
				})
				for _, g := range a.groupFoldersByAccount() {
					mobile.Section(c, g.Name, func() {
						for _, row := range g.Rows {
							a.phoneFolderRow(c, row)
						}
					})
				}
			})
		})
		a.phoneComposeButton(c)
	})
}

func (a *App) phoneBoxRow(c *ui.Context, nav string, icon *ui.SVG, label string, badge, depth int) {
	t := c.Theme()
	row := ui.Row(c.Key("ph-box-"+nav)).FillWidth().MinHeight(48).Padding(8, 16, 8, 16+float32(depth)*18).Gap(14).AlignItems(ui.Center).Role(ui.RoleButton).Label(label)
	row.Children(func() {
		ui.Icon(c, icon).Size(20, 20).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, label).FontSize(15.5).Grow(1).MinWidth(0).SingleLine()
		if badge > 0 {
			ui.Badge(c, fmt.Sprintf("%d", badge))
		}
	})
	if row.Clicked() {
		a.phoneOpenView(nav)
	}
}

func (a *App) phoneFolderRow(c *ui.Context, row folderRow) {
	t := c.Theme()
	f := row.Folder
	label := f.Name
	if label == "" {
		label = cleanFolderPath(f.Path)
	}
	ui.Row(c.Key("ph-fold-" + row.Key)).FillWidth().AlignItems(ui.Center).Children(func() {
		el := ui.Row(c.Key("ph-fold-open-"+row.Key)).Grow(1).MinWidth(0).MinHeight(48).Padding(8, 8, 8, 16+float32(row.Depth)*18).Gap(14).AlignItems(ui.Center).Role(ui.RoleButton).Label(label)
		el.Children(func() {
			ui.Icon(c, folderIcon(f)).Size(20, 20).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, label).FontSize(15.5).Grow(1).MinWidth(0).SingleLine()
			if f.UnreadCount > 0 {
				ui.Badge(c, fmt.Sprintf("%d", f.UnreadCount))
			}
		})
		if el.Clicked() {
			a.phoneOpenView("folder:" + f.ID)
		}
		if row.HasKids {
			icon, tip := iconChevronDn, a.tr("Unterordner einklappen", "Collapse subfolders")
			if row.Collapsed {
				icon, tip = iconChevronRt, a.tr("Unterordner ausklappen", "Expand subfolders")
			}
			if phoneIconButton(c.Key("ph-fold-tg-"+row.Key), icon, tip, t.TextMuted).Clicked() {
				a.toggleFolderCollapsed(row.Key)
			}
		}
	})
}

// phoneSync pulls new mail for all accounts (pull to refresh).
func (a *App) phoneSync() {
	a.phone.refresh.Refreshing = true
	if a.isDirectMode() {
		a.syncDirectAccountNow(context.Background(), a.selectedAccount)
	} else {
		a.RefreshAll(context.Background())
	}
}

// phoneComposeButton is the round button for a new mail in the lower corner.
func (a *App) phoneComposeButton(c *ui.Context) {
	t := c.Theme()
	if a.readOnly {
		return
	}
	b := ui.ButtonBase(c.Key("ph-compose-fab")).Absolute().Right(18).Bottom(22).Size(58, 58).Radius(29).
		Background(t.Accent).Center().Label(a.tr("Neue E-Mail", "New email"))
	b.Children(func() { ui.Icon(c, iconCompose).Size(24, 24).TextColor(t.Background) })
	if b.Clicked() {
		a.OpenComposeNew()
	}
}

// --- mail list -------------------------------------------------------------------------------

func (a *App) renderPhoneList(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.phoneBar(c, a.currentViewTitle(), true, func() {
			if a.navID != "calendar" && a.navID != "contacts" {
				if phoneIconButton(c.Key("ph-search"), iconSearch, a.tr("Suchen", "Search"), ui.Color{}).Clicked() {
					a.phone.search = !a.phone.search
					a.focusSearch = a.phone.search
				}
			}
		})
		switch a.navID {
		case "calendar":
			a.renderCalendarWorkspace(c)
			return
		case "contacts":
			a.renderContactsWorkspace(c)
			return
		}
		if a.phone.search || strings.TrimSpace(a.searchQuery) != "" {
			ui.Row(c).FillWidth().Padding(8, 12).Children(func() {
				sf := ui.SearchField(c.Key("ph-search-field"), &a.searchQuery).Placeholder(a.tr("Mails durchsuchen", "Search mail")).Grow(1)
				if a.focusSearch {
					sf.Focus()
					a.focusSearch = false
				}
				if sf.Submitted() {
					a.LoadMessages(context.Background())
				}
			})
		}
		if a.undo != nil {
			ui.Row(c.Key("ph-undo")).FillWidth().Padding(6, 14).Gap(8).AlignItems(ui.Center).Background(withAlpha(t.Accent, 0.10)).Children(func() {
				ui.Text(c, a.undo.Label).FontSize(13).Grow(1).MinWidth(0).SingleLine()
				if ui.Button(c.Key("ph-undo-do"), a.tr("Rückgängig", "Undo")).Clicked() && !a.undoBusy {
					a.PerformUndo(context.Background())
				}
			})
		}
		if len(a.messages) == 0 {
			ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
				ui.Text(c, a.tr("Keine Nachrichten in dieser Ansicht", "No messages in this view")).FontSize(15).Bold()
				ui.Text(c, a.tr("Zum Aktualisieren nach unten ziehen.", "Pull down to refresh.")).FontSize(13).TextColor(t.TextMuted)
			})
			a.phoneComposeButton(c)
			return
		}
		ls := a.phoneListState()
		crossFolder := !strings.HasPrefix(a.navID, "folder:")
		outbound := a.isCurrentViewOutbound()
		list := ui.List(c, ls, len(a.messages), func(i int) {
			if i < 0 || i >= len(a.messages) {
				return
			}
			a.renderPhoneMailRow(c, a.messages[i], crossFolder, outbound)
		}).Fill()
		if list.PullToRefresh(&a.phone.refresh) {
			a.phoneSync()
		}
		a.phoneComposeButton(c)
	})
}

func (a *App) renderPhoneMailRow(c *ui.Context, m backend.MessageHeader, crossFolder, outbound bool) {
	t := c.Theme()
	var trailing []mobile.SwipeAction
	if !a.readOnly {
		id := m.ID
		trailing = []mobile.SwipeAction{
			{Label: a.tr("Löschen", "Delete"), Destructive: true, OnTap: func() { a.trashMails(context.Background(), nil, id) }},
			{Label: a.tr("Archiv", "Archive"), OnTap: func() {
				a.performMessageAction(context.Background(), id, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), "")
			}},
		}
	}
	readLbl, target := a.tr("Gelesen", "Read"), true
	if !m.Unread {
		readLbl, target = a.tr("Ungelesen", "Unread"), false
	}
	var leading []mobile.SwipeAction
	if !a.readOnly {
		id := m.ID
		leading = []mobile.SwipeAction{{Label: readLbl, OnTap: func() {
			a.PerformMessageAction(context.Background(), id, backend.MessageActionsRequest{Read: &target}, "")
		}}}
	}
	mobile.SwipeRow(c.Key("ph-mail-"+m.ID), nil, mobile.SwipeOptions{Leading: leading, Trailing: trailing}, func() {
		row := ui.Column(c.Key("ph-mail-body-"+m.ID)).FillWidth().Background(t.Background).Padding(10, 16).Gap(3).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Role(ui.RoleButton).Label(m.Subject)
		row.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if m.Unread {
					statusDot(c, t.Accent, 8)
				}
				peer := shortSender(m.From)
				if outbound {
					peer = a.tr("An: ", "To: ") + shortSender(strings.Join(m.To, ", "))
				}
				st := ui.Text(c, peer).FontSize(15).Grow(1).MinWidth(0).SingleLine()
				if m.Unread {
					st.Bold()
				}
				if m.Flagged {
					ui.Icon(c, iconStarFilled).Size(13, 13).TextColor(t.Warning).Shrink(0)
				}
				ui.Text(c, formatRelTimeLang(m.Date, a.uiLang())).FontSize(12.5).TextColor(t.TextMuted).Shrink(0)
			})
			subj := m.Subject
			if subj == "" {
				subj = a.tr("(Kein Betreff)", "(No Subject)")
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				st := ui.Text(c, subj).FontSize(14).Grow(1).MinWidth(0).SingleLine()
				if !m.Unread {
					st.TextColor(t.TextMuted)
				}
				if m.ThreadCount > 1 {
					threadBadge(c, m.ThreadCount)
				}
				if crossFolder && m.Folder != "" {
					miniTag(c, m.Folder, t.TextMuted, t.Surface)
				}
			})
		})
		if row.Clicked() {
			a.SelectMessage(context.Background(), m.ID)
			a.phone.more = false
			a.phoneRouter().Push(phoneRouteMail)
		}
	})
}

// --- one mail --------------------------------------------------------------------------------

func (a *App) renderPhoneMail(c *ui.Context) {
	t := c.Theme()
	msg := a.selectedMsg
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.phoneBar(c, "", true, func() {
			if msg == nil {
				return
			}
			starIcon, starClr, flag := iconStar, ui.Color{}, true
			if msg.Flagged {
				starIcon, starClr, flag = iconStarFilled, t.Warning, false
			}
			if phoneIconButton(c.Key("ph-m-star"), starIcon, a.tr("Stern", "Star"), starClr).Clicked() {
				a.PerformMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Flagged: &flag}, "")
			}
			if phoneIconButton(c.Key("ph-m-more"), iconMenu, a.tr("Weitere Aktionen", "More actions"), ui.Color{}).Clicked() {
				a.phone.more = true
			}
		})
		if msg == nil {
			ui.Column(c).Fill().Center().Padding(28).Gap(8).Children(func() {
				if a.inspectorError != "" {
					ui.Text(c, a.inspectorError).FontSize(14).TextColor(t.Danger)
					return
				}
				ui.Spinner(c)
			})
			return
		}
		ui.Scroll(c.Key("ph-mail-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(14, 16, 24, 16).Gap(12).Children(func() {
				subj := msg.Subject
				if subj == "" {
					subj = a.tr("(Kein Betreff)", "(No Subject)")
				}
				ui.Text(c, subj).FontSize(20).Bold().Selectable()
				ui.Column(c).FillWidth().Gap(3).Children(func() {
					ui.Text(c, msg.From).FontSize(14).Bold().Selectable()
					ui.Text(c, formatDateTimeLang(msg.Date, a.uiLang())).FontSize(12.5).TextColor(t.TextMuted)
					if len(msg.To) > 0 {
						ui.Text(c, a.tr("An: ", "To: ")+strings.Join(msg.To, ", ")).FontSize(12.5).TextColor(t.TextMuted).MaxLines(3).Selectable()
					}
					if len(msg.CC) > 0 {
						ui.Text(c, "CC: "+strings.Join(msg.CC, ", ")).FontSize(12.5).TextColor(t.TextMuted).MaxLines(3).Selectable()
					}
				})
				if a.isDraftsMessage(msg) {
					if iconLabelBtn(c.Key("ph-edit-draft"), iconPen, a.tr("Entwurf weiterbearbeiten", "Edit Draft"), true, "").Clicked() {
						a.OpenComposeFromDraft(msg)
					}
				}
				a.renderICSBanner(c, msg)
				a.renderInspectorAIResult(c, msg)
				newer, older := a.conversationSplit(msg)
				a.renderConversationGroup(c, "newer", a.trf("Neuere Mails (%d)", "Newer emails (%d)", len(newer)), newer)
				ui.Divider(c)
				a.renderMailBody(c, msg)
				a.renderAttachments(c, msg)
				a.renderConversationGroup(c, "older", a.trf("Frühere Mails (%d)", "Earlier emails (%d)", len(older)), older)
			})
		})
		a.renderPhoneMailActions(c, msg)
	})
	a.renderPhoneMoreSheet(c, msg)
}

// renderPhoneMailActions is the bar of the open mail's main actions at the bottom.
func (a *App) renderPhoneMailActions(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	ui.Divider(c)
	ui.Row(c.Key("ph-m-actions")).FillWidth().Padding(4, 8).Background(t.Surface).Justify(ui.SpaceAround).AlignItems(ui.Center).Children(func() {
		if phoneIconButton(c.Key("ph-m-reply"), iconReply, a.tr("Antworten", "Reply"), ui.Color{}).Clicked() {
			a.OpenComposeReply(msg, a.inspectorDraft)
		}
		if phoneIconButton(c.Key("ph-m-replyall"), iconReplyAll, a.tr("Allen antworten", "Reply all"), ui.Color{}).Clicked() {
			a.OpenComposeReplyAll(msg, a.inspectorDraft)
		}
		if phoneIconButton(c.Key("ph-m-fwd"), iconForward, a.tr("Weiterleiten", "Forward"), ui.Color{}).Clicked() {
			a.OpenComposeForward(msg)
		}
		if a.readOnly {
			return
		}
		if phoneIconButton(c.Key("ph-m-archive"), iconArchive, a.tr("Archivieren", "Archive"), ui.Color{}).Clicked() {
			a.performMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), "")
			a.phoneRouter().Back()
		}
		if phoneIconButton(c.Key("ph-m-trash"), iconTrash, a.tr("Löschen", "Delete"), t.Danger).Clicked() {
			if !a.isTrashFolder(msg.FolderID) {
				a.trashMails(context.Background(), nil, msg.ID)
				a.phoneRouter().Back()
			} else {
				a.trashMails(context.Background(), nil, msg.ID) // asks for a second tap
			}
		}
	})
}

// renderPhoneMoreSheet lists the open mail's other actions in a sheet from the bottom.
func (a *App) renderPhoneMoreSheet(c *ui.Context, msg *backend.MessageDetail) {
	if msg == nil || !a.phone.more {
		return
	}
	read, readLbl := true, a.tr("Als gelesen markieren", "Mark as read")
	if !msg.Unread {
		read, readLbl = false, a.tr("Als ungelesen markieren", "Mark as unread")
	}
	id := msg.ID
	actions := []mobile.Action{
		{Label: readLbl, OnTap: func() {
			a.PerformMessageAction(context.Background(), id, backend.MessageActionsRequest{Read: &read}, "")
		}},
	}
	if !a.readOnly {
		actions = append(actions, mobile.Action{Label: a.tr("Verschieben nach…", "Move to…"), OnTap: func() { a.OpenMovePicker() }})
		if a.isDirectMode() {
			if a.isJunkMessage(msg) {
				actions = append(actions, mobile.Action{Label: a.tr("Kein Spam", "Not spam"), OnTap: func() { a.MarkMailNotSpam(context.Background(), id) }})
			} else {
				actions = append(actions, mobile.Action{Label: a.tr("Als Spam markieren", "Mark as spam"), OnTap: func() {
					a.MarkMailSpam(context.Background(), id)
					a.phoneRouter().Back()
				}})
			}
		}
	}
	actions = append(actions,
		mobile.Action{Label: a.tr("Unterhaltung teilen… (Claude, …)", "Share conversation… (Claude, …)"), OnTap: func() {
			a.OpenConversationIn(context.Background(), msg, handoffShare)
		}},
		mobile.Action{Label: a.tr("KI: Zusammenfassen", "AI: Summarize"), OnTap: func() { a.RunInspectorAI(context.Background(), "summarize") }},
		mobile.Action{Label: a.tr("KI: Antwort entwerfen", "AI: Draft reply"), OnTap: func() { a.RunInspectorAI(context.Background(), "draft_reply") }},
		mobile.Action{Label: a.tr("KI: Termin erkennen", "AI: Find event"), OnTap: func() { a.RunInspectorAI(context.Background(), "extract_events") }},
	)
	mobile.ActionSheet(c, &a.phone.more, mobile.ActionSheetOptions{Title: msg.Subject, Cancel: a.tr("Abbrechen", "Cancel")}, actions...)
}

// phoneListState keys the phone's mail list by mail and loads the next page near its end.
func (a *App) phoneListState() *ui.ListState {
	ls := &a.phone.list
	ls.Key = func(row int) any {
		if row >= 0 && row < len(a.messages) {
			return a.messages[row].ID
		}
		return row
	}
	ls.NearEnd = 15
	ls.EndReached = func(int) { a.loadMoreMessages(context.Background()) }
	return ls
}
