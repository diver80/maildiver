package nativeui

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/mailengine"
)

// Window chrome: the slim toolbar on top (menu, search, primary actions) and the status bar at
// the bottom (connection, selection, account, sync time, keyboard legend). Settings that are not
// used daily (language, theme, alerts, panels) live in the ☰ menu.

func (a *App) renderToolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			a.renderAppMenuButton(c)
			ui.Text(c, "MailDiver").FontSize(14).Bold().SingleLine()
			if v := a.versionTag(); v != "" {
				ui.Text(c, v).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
		})

		// Context-aware search & filter (Calendar search + multi-select calendars in Calendar view;
		// Mail search + account filter otherwise).
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if a.navID == "calendar" {
				sf := ui.SearchField(c.Key("tb-cal-search"), &a.calSearchQuery).Placeholder(a.tr("Termine durchsuchen  ( / )", "Search calendar  ( / )")).Grow(1).MinWidth(150).MaxWidth(560)
				if a.focusSearch {
					sf.Focus()
					a.focusSearch = false
				}
				curQ := strings.TrimSpace(a.calSearchQuery)
				if sf.Submitted() || (sf.Changed() && ((curQ != "" && a.lastCalSearch == "") || (curQ == "" && a.lastCalSearch != ""))) {
					a.lastCalSearch = curQ
					a.RefreshAll(context.Background())
				}
				if len(a.calendars) > 0 {
					a.renderCalendarFilterButton(c)
				}
			} else {
				sf := ui.SearchField(c.Key("tb-mail-search"), &a.searchQuery).Placeholder(a.tr("Mails durchsuchen  ( / )", "Search mail  ( / )")).Grow(1).MinWidth(150).MaxWidth(560)
				if a.focusSearch {
					sf.Focus()
					a.focusSearch = false
				}
				if sf.Submitted() {
					if a.navID == "contacts" {
						a.setNav("recent")
					}
					a.lastSearchQuery = strings.TrimSpace(a.searchQuery)
					a.lastCheckedIndex = -1
					a.LoadMessages(context.Background())
				} else if strings.TrimSpace(a.searchQuery) == "" && a.lastSearchQuery != "" {
					// Automatically reload full view when the user clears the search box.
					a.lastSearchQuery = ""
					a.serverSearchDoneQuery = ""
					a.serverSearchDoneKey = ""
					a.lastCheckedIndex = -1
					a.LoadMessages(context.Background())
				}
				if len(a.accountLabels) > 1 {
					accLabel := allAccountsLabelLang(a.uiLang())
					for lbl, id := range a.accountIDByLabel {
						if id == a.selectedAccount {
							accLabel = lbl
							break
						}
					}
					if ui.Select(c.Key("tb-acc"), &accLabel, a.accountLabels).Width(150).Changed() {
						a.selectedAccount = a.accountIDByLabel[accLabel]
						a.lastCheckedIndex = -1
						a.RefreshAll(context.Background())
					}
				}
			}
		})

		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if iconBtn(c.Key("tb-refresh"), iconRefresh, a.tr("Aktualisieren (⌘R)", "Refresh (⌘R)"), ui.Color{}, false).Clicked() {
				if a.isDirectMode() {
					if a.navID == "calendar" {
						a.syncDirectCalendarsNow(context.Background())
					} else {
						a.syncDirectAccountNow(context.Background(), a.selectedAccount)
					}
				} else {
					a.RefreshAll(context.Background())
				}
			}
			if a.isDirectMode() {
				if len(a.outboxItems) > 0 {
					obLbl := fmt.Sprintf("%s (%d)", a.tr("Postausgang", "Outbox"), len(a.outboxItems))
					if ui.Button(c.Key("tb-outbox"), obLbl).TextColor(t.Warning).Tooltip(a.tr("Wartende E-Mails im Offline-Postausgang anzeigen & senden", "View & send queued offline Outbox emails")).Clicked() {
						a.outboxOpen = true
						a.LoadOutbox(context.Background())
					}
				}
				if iconLabelBtn(c.Key("tb-direct"), iconMail, a.tr("Konten", "Accounts"), false, a.tr("IMAP/SMTP-Konten, Google Kalender & .ics verwalten", "Manage IMAP/SMTP accounts, Google Calendar & .ics")).Clicked() {
					a.OpenDirectModal(0)
				}
			}
			if iconLabelBtn(c.Key("tb-compose"), iconCompose, a.tr("Verfassen", "Compose"), false, a.tr("Neue E-Mail (c / ⌘N)", "New email (c / ⌘N)")).Clicked() {
				a.OpenComposeNew()
			}
			if iconLabelBtn(c.Key("tb-briefing"), iconSparkles, a.tr("Briefing", "Briefing"), true, a.tr("Tages-Briefing (KI · benötigt Tokens)", "Daily Briefing (AI · uses tokens)")).Clicked() {
				a.OpenDayBriefing(context.Background())
			}
			if iconBtn(c.Key("tb-preview"), iconLink, a.tr("Auto-Web-Vorschau ein/aus", "Toggle auto web preview"), ui.Color{}, a.cfg.App.LinkPreview.Enabled).Clicked() {
				a.ToggleWebPreviewAuto()
			}
			if iconBtn(c.Key("tb-copilot"), iconSidebar, a.tr("KI-Assistent ein/aus (⌘J)", "Toggle AI Assistant (⌘J)"), ui.Color{}, a.showCopilot).Clicked() {
				a.showCopilot = !a.showCopilot
				a.persistLayoutPrefs()
			}
		})
	})
}

// renderAppMenuButton is the ☰ menu: view toggles, language, theme, alerts and help.
func (a *App) renderAppMenuButton(c *ui.Context) {
	iconBtn(c.Key("tb-menu"), iconMenu, a.tr("Menü", "Menu"), ui.Color{}, false).PopoverMenuButton(func(m *ui.Menu) {
		if a.isDirectMode() {
			if m.Item(a.tr("Konten & Kalender…", "Accounts & Calendars…")).Chosen() {
				a.OpenDirectModal(0)
			}
			m.Separator()
		}
		if m.Item(a.tr("Seitenleiste", "Sidebar")).Checked(!a.hideSidebar).Chosen() {
			a.hideSidebar = !a.hideSidebar
			a.persistLayoutPrefs()
		}
		if m.Item(a.tr("KI-Assistent", "AI Assistant")).Checked(a.showCopilot).Shortcut(ui.Cmd, ui.KeyJ).Chosen() {
			a.showCopilot = !a.showCopilot
			a.persistLayoutPrefs()
		}
		if m.Item(a.tr("Web-Vorschau (automatisch)", "Web preview (auto)")).Checked(a.cfg.App.LinkPreview.Enabled).Chosen() {
			a.ToggleWebPreviewAuto()
		}
		if m.Item(a.tr("Desktop-Meldungen", "Desktop alerts")).Checked(a.notifyEnabled).Chosen() {
			a.notifyEnabled = !a.notifyEnabled
			a.persistLayoutPrefs()
		}
		m.Separator()
		m.Submenu(a.tr("Sprache", "Language"), func(m *ui.Menu) {
			for _, lbl := range langChoiceLabels() {
				if m.Item(lbl).Radio(a.langLabel == lbl).Chosen() {
					a.langLabel = lbl
					a.applyLanguageChoice(lbl)
				}
			}
		})
		m.Submenu(a.tr("Design", "Theme"), func(m *ui.Menu) {
			for _, lbl := range a.themeLabelList() {
				if m.Item(lbl).Radio(a.themeLabel == lbl).Chosen() {
					a.applyThemeChoice(lbl)
				}
			}
		})
		m.Separator()
		scanLbl := a.tr("Triage-Scan starten", "Run triage scan")
		if a.scanRunning {
			scanLbl = a.tr("Triage-Scan läuft…", "Triage scan running…")
		}
		if m.Item(scanLbl).Disabled(!a.engineReady || a.scanRunning || !a.cfg.Features.Triage).Chosen() {
			a.StartTriageScan(context.Background())
		}
		if m.Item(a.tr("Archiv & Aufräumen…", "Archive & cleanup…")).Disabled(!a.engineReady).Chosen() {
			a.OpenArchiveDialog(context.Background())
		}
		if m.Item(a.tr("Filterregeln verwalten…", "Manage filter rules…")).Disabled(!a.engineReady).Chosen() {
			a.OpenFilterRules(context.Background())
		}
		m.Separator()
		if m.Item(a.tr("Einstellungen…", "Settings…")).Shortcut(ui.Cmd, ui.KeyComma).Chosen() {
			a.OpenSettings()
		}
		if m.Item(a.tr("Tastenkürzel…", "Keyboard shortcuts…")).Shortcut(0, ui.KeyH).Chosen() {
			a.toggleHelp("shortcuts")
		}
		if m.Item(a.tr("Hilfe…", "Help…")).Shortcut(ui.Cmd, ui.KeySlash).Chosen() {
			a.toggleHelp("all")
		}
		if v := a.versionTag(); v != "" {
			m.Separator()
			m.Item("MailDiver " + v).Disabled(true)
		}
	})
}

// footerStatus returns the connection label and its dot color.
func (a *App) footerStatus(t *ui.Theme) (string, ui.Color) {
	if a.networkOffline {
		return a.tr("Offline (kein Netz)", "Offline (no network)"), t.Warning
	}
	if a.engineReady {
		return a.tr("Verbunden (IMAP)", "Connected (IMAP)"), t.Success
	}
	return a.tr("Nicht verbunden", "Offline"), t.Danger
}

// clearTransientSyncErrors drops stale DNS/network error banners (e.g. from Android Doze or
// system sleep) while keeping real authentication/configuration errors visible.
func (a *App) clearTransientSyncErrors() {
	for id, msg := range a.syncErrors {
		if mailengine.IsTransientNetworkErrorMsg(msg) {
			delete(a.syncErrors, id)
		}
	}
}

// firstSyncError returns an account whose last sync failed and the error ("" if none), in a
// stable order.
func (a *App) firstSyncError() (string, string) {
	best := ""
	for id, msg := range a.syncErrors {
		if a.networkOffline && mailengine.IsTransientNetworkErrorMsg(msg) {
			continue
		}
		if best == "" || id < best {
			best = id
		}
	}
	if best == "" {
		return "", ""
	}
	return best, a.syncErrors[best]
}

// footerAccount names the account in focus: the open mail's, else the account filter's.
func (a *App) footerAccount() string {
	if a.selectedMsg != nil && a.selectedMsg.ID == a.selectedID && a.selectedMsg.AccountID != "" {
		return a.accountDisplayName(a.selectedMsg.AccountID)
	}
	if a.selectedAccount != "" {
		return a.accountDisplayName(a.selectedAccount)
	}
	return ""
}

func (a *App) renderFooter(c *ui.Context) {
	t := c.Theme()
	w, _ := c.Size()
	status, dot := a.footerStatus(t)
	checked := len(a.CheckedMessageIDs())
	account := a.footerAccount()
	sync := ""
	if !a.lastSync.IsZero() {
		sync = a.tr("Stand ", "Synced ") + a.lastSync.Format("15:04")
	}

	// Rough width of the left part, so the legend never overlaps it.
	left := float32(60) + float32(len([]rune(status)))*6.5
	if a.readOnly {
		left += 80
	}
	if checked > 0 {
		left += 90
	}
	left += float32(len([]rune(account))+len([]rune(sync)))*6.5 + 40
	hints := fitHints(a.footerHints(), w-left-24)

	ui.Row(c.Key("footer")).FillWidth().Padding(4, 10).Gap(10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		sb := ui.Button(c.Key("ft-sidebar"), "").Padding(3, 4).Label(a.tr("Seitenleiste ein/aus", "Toggle sidebar")).Tooltip(a.tr("Seitenleiste ein/aus", "Toggle sidebar"))
		sb.Children(func() {
			ic := ui.Icon(c, iconPanelLeft).Size(14, 14)
			if a.hideSidebar {
				ic.TextColor(t.TextMuted)
			}
		})
		if sb.Clicked() {
			a.hideSidebar = !a.hideSidebar
		}

		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			statusDot(c, dot, 7)
			ui.Text(c, status).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		})
		if acc, msg := a.firstSyncError(); acc != "" {
			b := ui.Button(c.Key("ft-sync-error"), "").Padding(2, 6).Tooltip(msg).Label(msg)
			b.Children(func() {
				ui.Text(c, a.tr("Sync-Fehler: ", "Sync error: ")+a.accountDisplayName(acc)).FontSize(11.5).Bold().TextColor(t.Danger).SingleLine()
			})
			if b.Clicked() {
				a.OpenDirectModal(0)
			}
		}
		if a.readOnly {
			pillBadge(c, a.tr("Nur lesen", "Read-only"), withAlpha(t.Warning, 0.20), t.Warning)
		}
		if checked > 0 {
			ui.Text(c, a.trf("%d markiert", "%d selected", checked)).FontSize(11.5).Bold().TextColor(t.Accent).SingleLine().Shrink(0)
		}
		if account != "" {
			ui.Text(c, account).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		}
		if sync != "" {
			ui.Text(c, sync).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
		}

		ui.Spacer(c)
		ui.Row(c.Key("footer-hints")).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
			for _, h := range hints {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Text(c, h.Keys).Font("monospace").FontSize(10.5).Bold().SingleLine().
						Padding(1, 5).Radius(4).Background(t.Background).Border(1, t.Border)
					ui.Text(c, h.Label).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
				})
			}
		})
	})
}

// sidebarWidth is the width the left sidebar takes (0 while hidden).
func (a *App) sidebarWidth() float32 {
	if a.hideSidebar {
		return 0
	}
	return 230
}
