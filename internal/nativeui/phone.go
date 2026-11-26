package nativeui

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/mobile"

	"github.com/diver80/postdive/internal/backend"
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
	phoneRouteAssistant = "/assistant"
)

// phoneDetailQuery marks, on the list's route, a state inside the list screen that back must
// leave before the screen: "?detail=contact" (a contact open) or "?detail=select" (mails
// checked). The same path with a query is the same page to the router, keeping its state.
const phoneDetailQuery = "detail"

type phoneUI struct {
	router  *ui.Router
	list    ui.ListState // the mail list, without the desktop's highlighted row
	refresh ui.RefreshState
	swipes  phoneSwipes // mail rows slid aside
	more    bool        // the open mail's sheet of further actions
	search  bool        // the search field above the list

	// stepAfter is the mail whose next one opens once the list's next page arrived (the next
	// button at the end of what is loaded).
	stepAfter string
	// detail is the list state (phoneDetailQuery) the router's history was last given.
	detail string

	// A folder long-pressed: its sheet of actions, the rename dialog and the delete confirmation.
	folder        backend.Folder
	folderSheet   bool
	bulkMore      bool // the selection bar's "More" sheet
	folderRename  bool
	folderNewName string
	folderDelete  bool
}

// isPhone reports whether the window shows the phone layout.
func (a *App) isPhone() bool {
	return runtime.GOOS == "android" || (a.winW > 0 && a.winW < phoneMaxWidth)
}

// modalW is a dialog's width: w on desktops, the screen's full width on phones, whose dialogs
// (App.modal) have no margins of their own – the panel's padding keeps the content off the edges.
func (a *App) modalW(w float32) float32 {
	if a.isPhone() && a.winW > 0 {
		return max(a.winW, 200)
	}
	if a.winW > 0 && a.winW-80 < w {
		return max(a.winW-80, 200)
	}
	return w
}

// modalH is a dialog's greatest height: h on desktops, at most the screen within the system
// bars on phones.
func (a *App) modalH(c *ui.Context, h float32) float32 {
	if !a.isPhone() || a.winH <= 0 {
		return h
	}
	top, _, bottom, _ := c.SafeArea()
	return max(min(h, a.winH-top-bottom), 200)
}

// modal shows a dialog centered over a dimmed backdrop. Every caller renders its own styled
// Surface card (Radius 12 + Border), so we use DialogBase directly instead of ui.Modal's outer
// 20pt padded Background slab + heavy shadow, avoiding a clunky double frame around dialogs.
func (a *App) modal(c *ui.Context, open *bool, fn func()) ui.Element {
	if !a.isPhone() {
		return ui.DialogBase(c, open, func(back, panel ui.Element) {
			w, h := c.Size()
			back.Background(ui.RGBA(0, 0, 0, 0.36))
			panel.Radius(12).MaxWidth(max(w-48, 320)).MaxHeight(max(h-48, 240))
			panel.Shadow(0, 8, 22, 0, ui.RGBA(0, 0, 0, 0.22))
			fn()
		})
	}
	return ui.DialogBase(c, open, func(back, panel ui.Element) {
		top, _, bottom, _ := c.SafeArea()
		_, h := c.Size()
		back.Background(ui.RGBA(0, 0, 0, 0.4)).Padding(top, 0, bottom, 0)
		panel.MaxWidth(a.winW).MaxHeight(max(h-top-bottom, 200))
		fn()
	})
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
	a.phoneSyncDetailRoute()
	ui.Column(c.Key("ph-root")).Fill().Children(func() {
		r.View(c, func(rt *ui.Route) {
			switch {
			case rt.Match(phoneRouteAssistant):
				rt.Title(a.tr("KI-Assistent", "AI Assistant"))
				a.renderPhoneAssistant(c)
			case rt.Match(phoneRouteMail):
				rt.Title(a.tr("Mail", "Email"))
				a.renderPhoneMail(c)
			case rt.Match(phoneRouteList):
				rt.Title(a.currentViewTitle())
				a.renderPhoneList(c)
			default:
				rt.Title("PostDive")
				a.renderPhoneMailboxes(c)
			}
		}).FillWidth().Grow(1).MinHeight(0)
		// Below every screen: a mail sent from the open mail or archived from it must stay
		// undoable after the screen changed.
		a.renderPhoneUndoBars(c)
	})
}

// renderPhoneUndoBars shows the undo-send countdowns and the undo of the last move/archive.
func (a *App) renderPhoneUndoBars(c *ui.Context) {
	t := c.Theme()
	a.renderUndoSendBar(c)
	if a.undo != nil && time.Since(a.undo.CreatedAt) <= undoExpiry {
		ui.Row(c.Key("ph-undo")).FillWidth().Padding(6, 14).Gap(8).AlignItems(ui.Center).Background(withAlpha(t.Accent, 0.10)).Shrink(0).Children(func() {
			ui.Text(c, a.undo.Label).FontSize(13).Grow(1).MinWidth(0).SingleLine()
			if ui.Button(c.Key("ph-undo-do"), a.tr("Rückgängig", "Undo")).Clicked() && !a.undoBusy {
				a.PerformUndo(context.Background())
			}
			if ui.Button(c.Key("ph-undo-x"), "✕").Padding(2, 6).Clicked() {
				a.undo = nil
			}
		})
	}
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
	a.ClearMailSelection()
	a.phone.swipes.closeAll()
	a.setNav(nav)
	switch nav {
	case "calendar", "contacts", "activity":
		if nav == "contacts" {
			a.LoadContacts(context.Background())
		}
		if nav == "activity" {
			a.LoadActivity(context.Background(), false)
		}
	default:
		a.LoadMessages(context.Background())
	}
	a.phoneRouter().Push(phoneRouteList)
}

// phoneListDetail is the state of the list screen back has to leave first ("" for none).
func (a *App) phoneListDetail() string {
	switch a.navID {
	case "contacts":
		if a.contacts.editingNew || a.contacts.selectedID != "" {
			return "contact"
		}
	case "calendar":
	default:
		if len(a.checkedIDs) > 0 {
			return "select"
		}
	}
	return ""
}

// phoneSyncDetailRoute keeps the list's history entry in step with phoneListDetail, so the
// system's back (key, gesture or button) first closes an open contact or clears the checked
// mails, and only then leaves the list: an entry "/list?detail=…" is pushed as the state
// starts, the state is cleared when back took the entry away, and the entry is dropped when the
// screen's own buttons cleared the state.
func (a *App) phoneSyncDetailRoute() {
	r := a.phoneRouter()
	if r.Path() != phoneRouteList {
		if r.Path() == phoneRouteMailboxes {
			a.phone.detail = ""
		}
		return
	}
	cur := r.Query(phoneDetailQuery)
	if a.phone.detail != "" && cur == "" {
		// Back left the state's entry: leave the state too.
		switch a.phone.detail {
		case "contact":
			a.contacts.selectedID, a.contacts.editingNew = "", false
		case "select":
			a.ClearMailSelection()
		}
		a.phone.detail = ""
	}
	want := a.phoneListDetail()
	switch {
	case want != "" && cur == "":
		r.Push(phoneRouteList + "?" + phoneDetailQuery + "=" + want)
	case want != "" && cur != want:
		r.Replace(phoneRouteList + "?" + phoneDetailQuery + "=" + want)
	case want == "" && cur != "":
		r.Back()
	}
	a.phone.detail = want
}

// phoneShowRoutes rebuilds the phone's history as mailboxes → routes…, so a notification opens
// its mail (or list) whatever screen was shown, and back still leads up to the mailboxes.
func (a *App) phoneShowRoutes(routes ...string) {
	if !a.isPhone() {
		return
	}
	a.phone.more, a.phone.search = false, false
	a.phone.swipes.closeAll()
	r := a.phoneRouter()
	r.Go(-(1 << 30)) // back to the first entry, then make it the mailboxes
	r.Replace(phoneRouteMailboxes)
	for _, p := range routes {
		r.Push(p)
	}
}

// --- mailboxes -----------------------------------------------------------------------------

// phonePopMailIfOpen leaves the phone mail reader route when the open email was removed
// (e.g. unstarred in Focus, permanently deleted from Trash, or marked Not Spam).
func (a *App) phonePopMailIfOpen() {
	if !a.isPhone() {
		return
	}
	r := a.phoneRouter()
	if r.Path() == phoneRouteMail {
		r.Back()
	}
}

func (a *App) renderPhoneMailboxes(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		barH := float32(44)
		titleSz := float32(17)
		verTop := float32(3)
		if mobile.Current() == mobile.Material {
			barH = 56
			titleSz = 20
			verTop = 5
		}
		ui.Row(c.Key("ph-home-bar")).FillWidth().Height(barH).Background(t.Surface).Padding(0, 6, 0, 16).Gap(6).AlignItems(ui.Center).Shrink(0).Role(ui.RoleGroup).Children(func() {
			ui.Text(c, "PostDive").FontSize(titleSz).FontWeight(600).SingleLine().Role(ui.RoleHeading).Level(1)
			if v := a.versionTag(); v != "" {
				ui.Text(c, v).FontSize(10.5).TextColor(withAlpha(t.TextMuted, 0.70)).Margin(verTop, 0, 0, 0).SingleLine()
			}
			ui.Spacer(c)
			if phoneIconButton(c.Key("ph-settings"), iconSettings, a.tr("Einstellungen", "Settings"), ui.Color{}).Clicked() {
				a.OpenSettings()
			}
		})
		ui.Divider(c)
		if !a.engineReady {
			a.renderOfflineBanner(c)
			return
		}
		if a.isDirectMode() && len(a.accounts) == 0 {
			ui.Scroll(c).Fill().Children(func() { a.renderDirectOnboarding(c) })
			return
		}
		if a.networkOffline {
			ui.Row(c.Key("ph-offline-bar")).FillWidth().Padding(9, 16).Gap(8).AlignItems(ui.Center).Background(withAlpha(t.Warning, 0.14)).Children(func() {
				statusDot(c, t.Warning, 8)
				ui.Text(c, a.tr("Offline — warte auf Netzwerkverbindung", "Offline — waiting for network")).FontSize(13.5).Bold().TextColor(t.Warning).Grow(1)
			})
		} else if acc, msg := a.firstSyncError(); acc != "" {
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
				if len(a.outboxItems) > 0 {
					obLbl, onlyLater := a.outboxLabel()
					obClr := t.Warning
					if onlyLater {
						obClr = t.Accent
					}
					obRow := ui.ButtonBase(c.Key("ph-outbox-btn")).FillWidth().Padding(10, 16).Gap(8).Background(withAlpha(obClr, 0.14)).Label(obLbl)
					obRow.Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Icon(c, iconSend).Size(18, 18).TextColor(obClr)
							ui.Text(c, obLbl).FontSize(14).Bold().TextColor(obClr).Grow(1)
							ui.Icon(c, iconChevronRt).Size(16, 16).TextColor(obClr)
						})
					})
					if obRow.Clicked() {
						a.outboxOpen = true
						a.LoadOutbox(context.Background())
					}
				}
				mobile.Section(c, a.tr("Arbeitsbereich", "Workspace"), func() {
					a.phoneBoxRow(c, "focus", iconFocus, a.tr("Fokus (Ungelesen & ★)", "Focus (Unread & ★)"), a.focusCount(), 0)
					a.phoneBoxRow(c, "recent", iconMail, a.tr("Aktuelle Mails", "Recent Mail"), 0, 0)
					a.phoneBoxRow(c, "action", iconBolt, a.tr("Action / Warten", "Action / Waiting"), a.barSummary.ActionCount+a.barSummary.WaitingCount, 0)
					a.phoneBoxRow(c, "calendar", iconCal, a.tr("Kalender & Termine", "Calendar & Events"), len(a.todayEvents()), 0)
					if a.isDirectMode() {
						a.phoneBoxRow(c, "contacts", iconUsers, a.tr("Kontakte", "Contacts"), 0, 0)
						a.phoneBoxRow(c, "activity", iconActivity, a.tr("Aktivität", "Activity"), 0, 0)
					}
				})
				groups := a.groupFoldersByAccount()
				for _, g := range groups {
					gCopy := g
					accOpen := a.accountSectionOpen(gCopy.AccountID)
					renderPhoneNewFolder := func() {
						if !a.isDirectMode() || a.readOnly {
							return
						}
						if a.direct.newFolderAccountID == gCopy.AccountID {
							ui.Row(c.Key("ph-nf-row-"+gCopy.AccountID)).FillWidth().Padding(6, 14).Gap(6).AlignItems(ui.Center).Children(func() {
								inp := ui.TextInput(c.Key("ph-nf-in-"+gCopy.AccountID), &a.direct.newFolderName).Placeholder(a.tr("Ordnername…", "Folder name…")).Grow(1)
								if (ui.Button(c.Key("ph-nf-ok-"+gCopy.AccountID), "✓").Padding(6, 12).Clicked() || inp.Submitted()) && !a.direct.newFolderBusy && strings.TrimSpace(a.direct.newFolderName) != "" {
									a.CreateIMAPFolder(context.Background(), gCopy.AccountID, a.direct.newFolderName)
								}
								if ui.Button(c.Key("ph-nf-x-"+gCopy.AccountID), "✕").Padding(6, 10).Clicked() {
									a.direct.newFolderAccountID = ""
									a.direct.newFolderName = ""
								}
							})
						} else {
							b := ui.ButtonBase(c.Key("ph-nf-add-"+gCopy.AccountID)).FillWidth().Padding(8, 16).Label(a.tr("Neuer Ordner", "New Folder"))
							b.Children(func() {
								ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Icon(c, iconFolderPlus).Size(16, 16).TextColor(t.TextMuted)
									ui.Text(c, a.tr("Neuer Ordner…", "New Folder…")).FontSize(13.5).TextColor(t.TextMuted)
								})
							})
							if b.Clicked() {
								a.direct.newFolderAccountID = gCopy.AccountID
								a.direct.newFolderName = ""
							}
						}
					}
					ui.Column(c.Key("ph-acc-"+gCopy.AccountID)).FillWidth().Gap(2).Padding(8, 0, 2, 0).Children(func() {
						if len(groups) > 1 {
							hdr := ui.ButtonBase(c.Key("ph-acc-hdr-"+gCopy.AccountID)).FillWidth().Padding(6, 16).Label(gCopy.Name)
							hdr.Children(func() {
								ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
									chev := iconChevronDn
									if !*accOpen {
										chev = iconChevronRt
									}
									ui.Icon(c, chev).Size(15, 15).TextColor(t.Accent).Shrink(0)
									ui.Text(c, gCopy.Name).FontSize(13.5).Bold().TextColor(t.Accent).Grow(1).MinWidth(0).SingleLine()
								})
							})
							if hdr.Clicked() {
								*accOpen = !*accOpen
								a.persistCollapsedAccounts()
							}
						} else {
							ui.Row(c).FillWidth().Padding(6, 16).Children(func() {
								ui.Text(c, gCopy.Name).FontSize(13.5).Bold().TextColor(t.Accent).SingleLine()
							})
						}
						if len(groups) == 1 || *accOpen {
							for _, row := range gCopy.PrimaryRows {
								a.phoneFolderRow(c, row)
							}
							if len(gCopy.OtherRows) > 0 {
								moreOpen := a.moreFoldersOpen(gCopy.AccountID)
								chev := iconChevronRt
								if moreOpen {
									chev = iconChevronDn
								}
								lbl := a.trf("Weitere Ordner (%d)", "More folders (%d)", gCopy.OtherTopCount)
								moreRow := ui.Row(c.Key("ph-more-"+gCopy.AccountID)).FillWidth().MinHeight(44).Padding(6, 16).Gap(12).AlignItems(ui.Center).Role(ui.RoleButton).Label(lbl)
								moreRow.Children(func() {
									ui.Icon(c, chev).Size(17, 17).TextColor(t.TextMuted).Shrink(0)
									ui.Text(c, lbl).FontSize(14).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
									if !moreOpen && gCopy.OtherUnreadCount > 0 {
										ui.Badge(c, fmt.Sprintf("%d", gCopy.OtherUnreadCount))
									}
								})
								if moreRow.Clicked() {
									a.toggleMoreFolders(gCopy.AccountID)
								}
								if moreOpen {
									for _, row := range gCopy.OtherRows {
										a.phoneFolderRow(c, row)
									}
									renderPhoneNewFolder()
								}
							} else {
								renderPhoneNewFolder()
							}
						}
					})
				}
				mobile.Section(c, a.tr("KI", "AI"), func() {
					a.phoneToolRow(c, "ph-ai-chat", iconSparkles, t.Accent, a.tr("KI-Assistent", "AI Assistant"), func() {
						a.phoneRouter().Push(phoneRouteAssistant)
					})
					a.phoneToolRow(c, "ph-ai-brief", iconBookOpen, t.Accent, a.tr("Tagesbriefing", "Daily briefing"), func() {
						a.OpenDayBriefing(context.Background())
						a.phoneRouter().Push(phoneRouteAssistant)
					})
				})
				mobile.Section(c, a.tr("Werkzeuge", "Tools"), func() {
					if a.cfg.Features.Triage {
						scanLbl := a.tr("Triage-Scan starten", "Run triage scan")
						if a.scanRunning {
							scanLbl = a.tr("Triage-Scan läuft…", "Triage scan running…")
						}
						bScan := ui.ButtonBase(c.Key("ph-tool-scan")).FillWidth().Padding(10, 16).Label(scanLbl)
						bScan.Children(func() {
							ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, iconSparkles).Size(18, 18).TextColor(t.Accent)
								ui.Text(c, scanLbl).FontSize(14.5)
							})
						})
						if bScan.Clicked() && a.engineReady && !a.scanRunning {
							a.StartTriageScan(context.Background())
						}
					}
					bArch := ui.ButtonBase(c.Key("ph-tool-arch")).FillWidth().Padding(10, 16).Label(a.tr("Archiv & Aufräumen", "Archive & cleanup"))
					bArch.Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconArchive).Size(18, 18).TextColor(t.TextMuted)
							ui.Text(c, a.tr("Archiv & Aufräumen…", "Archive & cleanup…")).FontSize(14.5)
						})
					})
					if bArch.Clicked() && a.engineReady {
						a.OpenArchiveDialog(context.Background())
					}
					bRules := ui.ButtonBase(c.Key("ph-tool-rules")).FillWidth().Padding(10, 16).Label(a.tr("Filterregeln", "Filter rules"))
					bRules.Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconFilter).Size(18, 18).TextColor(t.TextMuted)
							ui.Text(c, a.tr("Filterregeln verwalten…", "Manage filter rules…")).FontSize(14.5)
						})
					})
					if bRules.Clicked() && a.engineReady {
						a.OpenFilterRules(context.Background())
					}
				})
			})
		})
		a.phoneComposeButton(c)
	})
	a.renderPhoneFolderSheets(c)
}

// phoneToolRow is a row of the mailboxes screen that runs an action.
func (a *App) phoneToolRow(c *ui.Context, key string, icon *ui.SVG, iconClr ui.Color, label string, run func()) {
	b := ui.ButtonBase(c.Key(key)).FillWidth().Padding(10, 16).Label(label)
	b.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(18, 18).TextColor(iconClr)
			ui.Text(c, label).FontSize(14.5)
		})
	})
	if b.Clicked() {
		run()
	}
}

// renderPhoneFolderSheets shows what a long press on a user folder offers: rename (in a dialog
// with its name) and delete (after a confirmation), with the desktop folder menu's calls.
func (a *App) renderPhoneFolderSheets(c *ui.Context) {
	t := c.Theme()
	f := a.phone.folder
	name := f.Name
	if name == "" {
		name = cleanFolderPath(f.Path)
	}
	mobile.ActionSheet(c, &a.phone.folderSheet, mobile.ActionSheetOptions{Title: name, Cancel: a.tr("Abbrechen", "Cancel")},
		mobile.Action{Label: a.tr("Umbenennen…", "Rename…"), OnTap: func() {
			a.phone.folderNewName, a.phone.folderRename = name, true
		}},
		mobile.Action{Label: a.tr("Löschen…", "Delete…"), Destructive: true, OnTap: func() {
			a.phone.folderDelete = true
		}},
	)
	mobile.ActionSheet(c, &a.phone.folderDelete, mobile.ActionSheetOptions{
		Title:   a.trf("Ordner „%s“ löschen?", "Delete folder “%s”?", name),
		Message: a.tr("Der Ordner wird samt den Mails darin auf dem Server gelöscht.", "The folder and the emails in it are deleted on the server."),
		Cancel:  a.tr("Abbrechen", "Cancel"),
	}, mobile.Action{Label: a.tr("Ordner löschen", "Delete folder"), Destructive: true, OnTap: func() {
		a.DeleteIMAPFolder(context.Background(), f.AccountID, f.ID)
	}})
	a.modal(c, &a.phone.folderRename, func() {
		ui.Column(c.Key("ph-fold-rename")).Width(a.modalW(360)).Padding(18).Radius(12).Background(t.Surface).Gap(12).Children(func() {
			ui.Text(c, a.tr("Ordner umbenennen", "Rename folder")).FontSize(16).Bold()
			in := ui.TextInput(c.Key("ph-fold-rename-in"), &a.phone.folderNewName).Placeholder(a.tr("Neuer Name…", "New name…")).FillWidth()
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("ph-fold-rename-x"), a.tr("Abbrechen", "Cancel")).Clicked() {
					a.phone.folderRename = false
				}
				newName := strings.TrimSpace(a.phone.folderNewName)
				ok := ui.PrimaryButton(c.Key("ph-fold-rename-ok"), a.tr("Umbenennen", "Rename"))
				if newName == "" || newName == name || a.direct.newFolderBusy {
					ok.Disabled(true)
				}
				if (ok.Clicked() || in.Submitted()) && newName != "" && newName != name && !a.direct.newFolderBusy {
					a.RenameIMAPFolder(context.Background(), f.AccountID, f.ID, newName)
					a.phone.folderRename = false
				}
			})
		})
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
			if f.UnreadCount > 0 && !isTrashOrJunkFolder(f) {
				ui.Badge(c, fmt.Sprintf("%d", f.UnreadCount))
			}
		})
		if el.LongPressed() {
			// As the desktop's folder menu: user folders can be renamed and deleted.
			if a.isDirectMode() && !a.readOnly && isUserManageableFolder(f) {
				a.phone.folder, a.phone.folderSheet = f, true
			}
			return
		}
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
	a.clearTransientSyncErrors()
	if a.isDirectMode() {
		a.syncDirectAccountNow(context.Background(), "")
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
	checked := a.CheckedMessages()
	selecting := len(checked) > 0 && isMailNav(a.navID)
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if selecting {
			bg := t.Surface
			mobile.TopBar(c, mobile.TopBarOptions{
				Title:      fmt.Sprintf(a.tr("%d ausgewählt", "%d selected"), len(checked)),
				Background: &bg,
				Leading: func() {
					if phoneIconButton(c.Key("ph-sel-clear"), iconX, a.tr("Auswahl aufheben", "Clear selection"), ui.Color{}).Clicked() {
						a.ClearMailSelection()
					}
				},
				Trailing: func() {
					lbl := a.tr("Alle", "All")
					if a.AllVisibleMailsChecked() {
						lbl = a.tr("Keine", "None")
					}
					if ui.Button(c.Key("ph-sel-all"), lbl).Padding(6, 10).Clicked() {
						a.ToggleSelectAllVisibleMails()
					}
				},
			})
			ui.Divider(c)
		} else {
			a.phoneBar(c, a.currentViewTitle(), true, func() {
				if isMailNav(a.navID) {
					if !a.readOnly && a.engineReady && (a.navID == "focus" || strings.HasPrefix(a.navID, "folder:")) {
						if phoneIconButton(c.Key("ph-mark-all-read"), iconMailOpen, a.tr("Alle gelesen", "Mark all read"), ui.Color{}).Clicked() {
							a.MarkCurrentViewAllRead(context.Background())
						}
					}
					if trashAcc := a.currentTrashAccountID(); trashAcc != "" && a.isDirectMode() && len(a.messages) > 0 && !a.readOnly {
						if phoneIconButton(c.Key("ph-empty-trash"), iconTrash, a.tr("Papierkorb leeren", "Empty Trash"), t.Danger).Clicked() {
							if a.direct.emptyTrashArmID == trashAcc && time.Since(a.direct.emptyTrashArmAt) < 5*time.Second {
								a.EmptyAccountTrash(context.Background(), trashAcc)
							} else {
								a.direct.emptyTrashArmID = trashAcc
								a.direct.emptyTrashArmAt = time.Now()
								a.toast(a.tr("Nochmal tippen zum endgültigen Leeren", "Tap again to empty Trash"), "warning")
							}
						}
					}
					if spamAcc := a.currentSpamAccountID(); spamAcc != "" && a.isDirectMode() && len(a.messages) > 0 && !a.readOnly {
						if phoneIconButton(c.Key("ph-empty-spam"), iconTrash, a.tr("Spam leeren", "Empty Spam"), t.Danger).Clicked() {
							if a.direct.emptySpamArmID == spamAcc && time.Since(a.direct.emptySpamArmAt) < 5*time.Second {
								a.EmptyAccountSpam(context.Background(), spamAcc)
							} else {
								a.direct.emptySpamArmID = spamAcc
								a.direct.emptySpamArmAt = time.Now()
								a.toast(a.tr("Nochmal tippen zum endgültigen Spam-Leeren", "Tap again to empty Spam"), "warning")
							}
						}
					}
					if phoneIconButton(c.Key("ph-search"), iconSearch, a.tr("Suchen", "Search"), ui.Color{}).Clicked() {
						a.phone.search = !a.phone.search
						a.focusSearch = a.phone.search
					}
				}
			})
		}
		switch a.navID {
		case "calendar":
			a.renderCalendarWorkspace(c)
			return
		case "contacts":
			a.renderContactsWorkspace(c)
			return
		case "activity":
			a.renderActivityWorkspace(c)
			return
		}
		if a.phone.search || strings.TrimSpace(a.searchQuery) != "" {
			ui.Column(c).FillWidth().Padding(8, 12).Gap(6).Children(func() {
				ui.Row(c).FillWidth().Children(func() {
					sf := ui.SearchField(c.Key("ph-search-field"), &a.searchQuery).Placeholder(a.searchPlaceholder(false)).Grow(1)
					if a.focusSearch {
						sf.Focus()
						a.focusSearch = false
					}
					if sf.Submitted() {
						a.lastSearchQuery = strings.TrimSpace(a.searchQuery)
						a.LoadMessages(context.Background())
					} else if strings.TrimSpace(a.searchQuery) == "" && a.lastSearchQuery != "" {
						a.lastSearchQuery = ""
						a.serverSearchDoneQuery = ""
						a.serverSearchDoneKey = ""
						a.LoadMessages(context.Background())
					}
				})
				a.renderSearchScopeBar(c, "ph-search-scope")
				if q := strings.TrimSpace(a.searchQuery); q != "" && a.engineReady {
					ui.Row(c.Key("ph-srv-search-bar")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						if a.serverSearchBusy {
							ui.Spinner(c)
							ui.Text(c, a.tr("Suche auf Server…", "Searching server…")).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
						} else if a.serverSearchDoneKey == a.serverSearchScopeKey(q) {
							ui.Text(c, a.trf("Server-Suche: %d Treffer", "Server search: %d matches", a.serverSearchCount)).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
						} else {
							ui.Text(c, a.tr("Nur geladene Mails durchsucht.", "Downloaded mail only.")).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
							if ui.Button(c.Key("ph-srv-search-go"), a.tr("Auch auf Server", "Search server too")).Padding(4, 10).Clicked() {
								a.SearchMessagesOnServer(context.Background())
							}
						}
					})
				}
			})
		}
		if len(a.messages) == 0 {
			ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
				ui.Text(c, a.tr("Keine Nachrichten in dieser Ansicht", "No messages in this view")).FontSize(15).Bold()
				ui.Text(c, a.tr("Zum Aktualisieren nach unten ziehen.", "Pull down to refresh.")).FontSize(13).TextColor(t.TextMuted)
			})
			if !selecting {
				a.phoneComposeButton(c)
			}
			return
		}
		ls := a.phoneListState()
		crossFolder := !strings.HasPrefix(a.navID, "folder:")
		outbound := a.isCurrentViewOutbound()
		list := ui.List(c, ls, len(a.messages), func(i int) {
			if i < 0 || i >= len(a.messages) {
				return
			}
			a.renderPhoneMailRow(c, i, a.messages[i], crossFolder, outbound, selecting)
		}).Grow(1).MinHeight(0).FillWidth()
		if list.PullToRefresh(&a.phone.refresh) {
			a.phoneSync()
		}
		if selecting {
			a.renderPhoneBulkBar(c, checked)
		} else {
			a.phoneComposeButton(c)
		}
	})
}

func (a *App) renderPhoneBulkBar(c *ui.Context, checked []backend.MessageHeader) {
	if a.readOnly || len(checked) == 0 {
		return
	}
	t := c.Theme()
	ids := make([]string, len(checked))
	anyUnread := false
	anyUnflagged := false
	for i, m := range checked {
		ids[i] = m.ID
		if m.Unread {
			anyUnread = true
		}
		if !m.Flagged {
			anyUnflagged = true
		}
	}
	readLbl, readTarget := a.tr("Gelesen", "Read"), true
	readIcon := iconMailOpen
	if !anyUnread {
		readLbl, readTarget = a.tr("Ungelesen", "Unread"), false
		readIcon = iconMailUnread
	}
	starIcon := iconStar
	if !anyUnflagged {
		starIcon = iconStarFilled
	}
	bulkBtn := func(key string, svg *ui.SVG, label string, danger bool) ui.Element {
		fg := t.Text
		bg := ui.Color{}
		if danger {
			fg = t.Danger
			if a.confirmArmed("trash", ids) {
				bg = withAlpha(t.Danger, 0.14)
			}
		}
		b := ui.ButtonBase(c.Key(key)).Grow(1).MinHeight(46).Padding(6, 6).Radius(8).Background(bg).Center().Role(ui.RoleButton).Label(label)
		b.Children(func() {
			ui.Column(c).Center().Gap(3).Children(func() {
				ui.Icon(c, svg).Size(19, 19).TextColor(fg)
				ui.Text(c, label).FontSize(12).TextColor(fg).SingleLine()
			})
		})
		return b
	}
	ui.Divider(c)
	ui.Row(c.Key("ph-bulk-bar")).FillWidth().Padding(4, 8).Gap(4).Background(t.Surface).Justify(ui.SpaceAround).AlignItems(ui.Center).Children(func() {
		if bulkBtn("ph-bulk-star", starIcon, a.tr("Stern", "Star"), false).Clicked() {
			target := anyUnflagged
			a.PerformBatchAction(context.Background(), ids, backend.MessageActionsRequest{Flagged: &target}, a.tr("Markierung aktualisiert", "Star updated"))
			a.ClearMailSelection()
		}
		if bulkBtn("ph-bulk-read", readIcon, readLbl, false).Clicked() {
			target := readTarget
			msg := a.tr("Als gelesen markiert", "Marked as read")
			if !target {
				msg = a.tr("Als ungelesen markiert", "Marked as unread")
			}
			a.PerformBatchAction(context.Background(), ids, backend.MessageActionsRequest{Read: &target}, msg)
			a.ClearMailSelection()
		}
		if bulkBtn("ph-bulk-archive", iconArchive, a.tr("Archiv", "Archive"), false).Clicked() {
			a.PerformBatchAction(context.Background(), ids, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"))
			a.ClearMailSelection()
		}
		if bulkBtn("ph-bulk-move", iconFolderInput, a.tr("Ordner", "Move"), false).Clicked() {
			a.OpenMovePicker()
		}
		if bulkBtn("ph-bulk-delete", iconTrash, a.tr("Löschen", "Delete"), true).Clicked() {
			a.trashMails(context.Background(), ids, "")
		}
		if bulkBtn("ph-bulk-more", iconDots, a.tr("Mehr", "More"), false).Clicked() {
			a.phone.bulkMore = true
		}
	})
	// Less frequent actions in a sheet, so the bar keeps fitting a phone's width.
	var more []mobile.Action
	if a.isDirectMode() {
		more = append(more, mobile.Action{Label: a.tr("Als Spam markieren", "Mark as spam"), OnTap: func() {
			a.MarkMailsSpam(context.Background(), ids)
		}})
	}
	more = append(more,
		mobile.Action{Label: a.tr("KI: Auswahl zusammenfassen", "AI: Summarize selection"), OnTap: func() {
			a.RunSelectionSummary(context.Background())
		}},
		mobile.Action{Label: a.tr("Filterregel erstellen…", "Create filter rule…"), OnTap: func() {
			a.OpenQuickRuleWizard()
		}},
	)
	mobile.ActionSheet(c, &a.phone.bulkMore, mobile.ActionSheetOptions{
		Title:  a.trf("%d ausgewählt", "%d selected", len(ids)),
		Cancel: a.tr("Abbrechen", "Cancel"),
	}, more...)
}

func (a *App) renderPhoneMailRow(c *ui.Context, idx int, m backend.MessageHeader, crossFolder, outbound, selecting bool) {
	t := c.Theme()
	isChecked := selecting && a.IsMailChecked(m.ID)
	var leading *phoneSwipeAction
	var trailing []phoneSwipeAction
	if !a.readOnly && !selecting {
		id := m.ID
		readLbl, target := a.tr("Gelesen", "Read"), true
		if !m.Unread {
			readLbl, target = a.tr("Ungelesen", "Unread"), false
		}
		leading = &phoneSwipeAction{label: readLbl, run: func() {
			a.PerformMessageAction(context.Background(), id, backend.MessageActionsRequest{Read: &target}, "")
		}}
		trailing = []phoneSwipeAction{
			{label: a.tr("Löschen", "Delete"), destructive: true, run: func() { a.trashMails(context.Background(), nil, id) }},
			{label: a.tr("Archiv", "Archive"), run: func() {
				a.performMessageAction(context.Background(), id, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), "")
			}},
		}
	}
	rowBg := t.Background
	if isChecked {
		rowBg = withAlpha(t.Accent, 0.14)
	}
	a.phoneSwipeRow(c.Key("ph-mail-"+m.ID), m.ID, leading, trailing, func() {
		row := ui.Row(c.Key("ph-mail-body-"+m.ID)).FillWidth().Background(rowBg).Padding(10, 16).Gap(12).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Role(ui.RoleButton).Label(m.Subject)
		row.Children(func() {
			if selecting {
				ring := ui.Box(c.Key("ph-mail-chk-"+m.ID)).Size(22, 22).Radius(11).Center().Shrink(0)
				if isChecked {
					ring.Background(t.Accent).Children(func() {
						ui.Text(c, "✓").FontSize(13).Bold().TextColor(ui.RGB(255, 255, 255))
					})
				} else {
					ring.Border(1.5, t.TextMuted)
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
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
					if m.HasAttachments {
						ui.Icon(c, iconPaperclip).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
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
				if m.Snippet != "" && a.cfg.App.EffectiveListSnippets() {
					ui.Text(c, m.Snippet).FontSize(13).TextColor(t.TextMuted).FillWidth().MinWidth(0).SingleLine()
				}
			})
		})
		if !a.readOnly && row.LongPressed() {
			a.phone.swipes.closeAll()
			if !selecting {
				a.SetMailChecked(idx, true, false)
			} else {
				a.ToggleMailChecked(idx, false)
			}
			return
		}
		if row.Clicked() {
			if selecting {
				a.ToggleMailChecked(idx, false)
				return
			}
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
	a.phoneStepAfterLoad()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.phoneBar(c, "", true, func() {
			if msg == nil {
				return
			}
			// Up and down through the list's order, as the desktop's j/k (‹ › would read as
			// the back button's chevron).
			if idx := a.currentIndex(); idx >= 0 {
				prev := phoneIconButton(c.Key("ph-m-prev"), iconChevronUp, a.tr("Vorherige Mail", "Previous email"), ui.Color{})
				if idx == 0 {
					prev.Disabled(true)
				}
				if prev.Clicked() && idx > 0 {
					a.phoneStepMail(-1)
				}
				next := phoneIconButton(c.Key("ph-m-next"), iconChevronDn, a.tr("Nächste Mail", "Next email"), ui.Color{})
				if idx+1 >= len(a.messages) && a.nextCursor == "" {
					next.Disabled(true)
				}
				if next.Clicked() {
					a.phoneStepMail(1)
				}
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
			if a.selectedID == "" {
				a.phonePopMailIfOpen()
				return
			}
			ui.Column(c).Fill().Center().Padding(28).Gap(8).Children(func() {
				if a.inspectorError != "" {
					ui.Text(c, a.inspectorError).FontSize(14).TextColor(t.Danger)
					return
				}
				ui.Spinner(c)
			})
			return
		}
		ui.Scroll(c.Key("ph-mail-scroll-" + msg.ID)).Grow(1).MinHeight(0).FillWidth().Children(func() {
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
				a.renderAttachments(c, msg)
				a.renderMailBody(c, msg)
				a.renderConversationGroup(c, "older", a.trf("Frühere Mails (%d)", "Earlier emails (%d)", len(older)), older)
			})
		})
		a.renderPhoneMailActions(c, msg)
	})
	a.renderPhoneMoreSheet(c, msg)
}

// afterOpenMailRemoved shows what follows once the open mail left its list (archived, moved,
// deleted, spam): its neighbour openNext, as on the desktop, else the list. On a phone the
// neighbour opens only while the mail screen shows, so a mail swiped away in the list never
// opens one (marking it read) behind the list.
func (a *App) afterOpenMailRemoved(ctx context.Context, openNext string) {
	if openNext != "" && (!a.isPhone() || a.phoneRouter().Path() == phoneRouteMail) {
		a.SelectMessage(ctx, openNext)
		if a.isPhone() {
			a.phone.more = false
			if idx := a.currentIndex(); idx >= 0 {
				a.phone.list.ScrollIntoView(idx)
			}
		}
		return
	}
	a.syncPreviewForMessage(nil)
	a.phonePopMailIfOpen()
}

// phoneStepMail opens the mail delta rows away in the list's order. At the end of what is
// loaded the next page is loaded first, and the step taken as it arrives (phoneStepAfterLoad).
func (a *App) phoneStepMail(delta int) {
	idx := a.currentIndex()
	if idx < 0 {
		return
	}
	to := idx + delta
	if to < 0 {
		return
	}
	if to >= len(a.messages) {
		if delta > 0 && a.nextCursor != "" {
			a.phone.stepAfter = a.selectedID
			a.loadMoreMessages(context.Background())
		}
		return
	}
	a.phone.more = false
	a.SelectMessage(context.Background(), a.messages[to].ID)
	a.phone.list.ScrollIntoView(to)
	if to >= len(a.messages)-3 {
		a.loadMoreMessages(context.Background()) // the next page before the end is reached
	}
}

// phoneStepAfterLoad takes the step phoneStepMail waits for once the next page arrived, or
// drops it when none came (UI thread, every frame of the mail screen).
func (a *App) phoneStepAfterLoad() {
	if a.phone.stepAfter == "" || a.loadingMore {
		return
	}
	waited := a.phone.stepAfter
	a.phone.stepAfter = ""
	if waited != a.selectedID {
		return // another mail was opened meanwhile
	}
	if idx := a.currentIndex(); idx >= 0 && idx+1 < len(a.messages) {
		a.phoneStepMail(1)
	}
}

// renderPhoneAssistant is the AI assistant's screen: the desktop's assistant pane at a phone's
// width, with its stop and clear buttons in the bar.
func (a *App) renderPhoneAssistant(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		a.phoneBar(c, a.tr("KI-Assistent", "AI Assistant"), true, func() {
			if a.chatStreaming {
				ui.Spinner(c)
				if phoneIconButton(c.Key("ph-cp-stop"), iconStop, a.tr("Generierung stoppen", "Stop generating"), t.Danger).Clicked() && a.chatCancel != nil {
					a.chatCancel()
				}
			} else if len(a.chatEntries) > 0 {
				if phoneIconButton(c.Key("ph-cp-clear"), iconTrash, a.tr("Chat leeren", "Clear chat"), t.TextMuted).Clicked() {
					a.ClearChat()
				}
			}
		})
		ui.Column(c.Key("ph-cp-controls")).FillWidth().Padding(8, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			a.renderCopilotControls(c, true)
		})
		a.renderCopilotChat(c, true)
	})
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
		// Archive and delete open the next mail once done (afterOpenMailRemoved), the list
		// when there is none.
		if phoneIconButton(c.Key("ph-m-archive"), iconArchive, a.tr("Archivieren", "Archive"), ui.Color{}).Clicked() {
			a.performMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(msg.ID))
		}
		if phoneIconButton(c.Key("ph-m-trash"), iconTrash, a.tr("Löschen", "Delete"), t.Danger).Clicked() {
			a.trashMails(context.Background(), nil, msg.ID) // in the trash: asks for a second tap
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
		{Label: func() string {
			if msg.Flagged {
				return a.tr("Stern entfernen", "Remove star")
			}
			return a.tr("Mit Stern markieren", "Add star")
		}(), OnTap: func() {
			flag := !msg.Flagged
			a.PerformMessageAction(context.Background(), id, backend.MessageActionsRequest{Flagged: &flag}, "")
		}},
	}
	if unsubHTTPS, unsubMailto, _ := parseUnsubscribeTargets(msg); unsubHTTPS != "" || unsubMailto != "" {
		actions = append(actions, mobile.Action{
			Label: a.tr("Von Newsletter abmelden", "Unsubscribe"),
			OnTap: func() { a.PerformUnsubscribe(context.Background(), msg) },
		})
	}
	if !a.readOnly {
		actions = append(actions, mobile.Action{Label: a.tr("Verschieben nach…", "Move to…"), OnTap: func() { a.OpenMovePicker() }})
		if a.isDirectMode() {
			if a.isJunkMessage(msg) {
				actions = append(actions, mobile.Action{Label: a.tr("Kein Spam", "Not spam"), OnTap: func() { a.MarkMailNotSpam(context.Background(), id) }})
			} else {
				actions = append(actions, mobile.Action{Label: a.tr("Als Spam markieren", "Mark as spam"), OnTap: func() {
					a.MarkMailSpam(context.Background(), id)
				}})
			}
		}
	}
	if a.isDirectMode() {
		// The .eml export reads the engine's raw mail, as on the desktop.
		actions = append(actions, mobile.Action{Label: a.tr("Als .eml speichern…", "Save as .eml…"), OnTap: func() {
			a.SaveSelectedMessageEML(context.Background())
		}})
	}
	actions = append(actions,
		mobile.Action{Label: a.tr("Filterregel erstellen…", "Create filter rule…"), OnTap: func() {
			a.OpenQuickRuleWizard()
		}},
		mobile.Action{Label: a.tr("Unterhaltung teilen… (Claude, …)", "Share conversation… (Claude, …)"), OnTap: func() {
			a.OpenConversationIn(context.Background(), msg, handoffShare)
		}},
		mobile.Action{Label: a.tr("KI: Zusammenfassen", "AI: Summarize"), OnTap: func() { a.RunInspectorAI(context.Background(), "summarize") }},
		mobile.Action{Label: a.tr("KI: Antwort entwerfen", "AI: Draft reply"), OnTap: func() { a.RunInspectorAI(context.Background(), "draft_reply") }},
		mobile.Action{Label: a.tr("KI: Termin erkennen", "AI: Find event"), OnTap: func() { a.RunInspectorAI(context.Background(), "extract_events") }},
		mobile.Action{Label: a.tr("KI: Kontakte extrahieren", "AI: Extract contacts"), OnTap: func() { a.RunInspectorAI(context.Background(), "extract_contacts") }},
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
