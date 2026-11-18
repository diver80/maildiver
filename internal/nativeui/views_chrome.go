package nativeui

import (
	"context"

	"github.com/egoist/mygo/ui"
)

// Window chrome: the slim toolbar on top (menu, search, primary actions) and the status bar at
// the bottom (connection, selection, account, sync time, keyboard legend). Settings that are not
// used daily (language, theme, alerts, panels) live in the ☰ menu.

func (a *App) renderToolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			a.renderAppMenuButton(c)
			ui.Text(c, "OmaThunder").FontSize(14).Bold().SingleLine()
		})

		// Search (and the account filter) centered in the free space.
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			sf := ui.SearchField(c, &a.searchQuery).Placeholder(a.tr("Mails durchsuchen  ( / )", "Search mail  ( / )")).Grow(1).MinWidth(150).MaxWidth(560)
			if a.focusSearch {
				sf.Focus()
				a.focusSearch = false
			}
			if sf.Submitted() {
				if a.navID == "calendar" {
					a.setNav("recent")
				}
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
		})

		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if iconBtn(c.Key("tb-refresh"), iconRefresh, a.tr("Aktualisieren (⌘R)", "Refresh (⌘R)"), ui.Color{}, false).Clicked() {
				a.RefreshAll(context.Background())
			}
			if iconLabelBtn(c.Key("tb-compose"), iconCompose, a.tr("Verfassen", "Compose"), false, a.tr("Neue E-Mail (c / ⌘N)", "New email (c / ⌘N)")).Clicked() {
				a.OpenComposeNew()
			}
			if iconLabelBtn(c.Key("tb-briefing"), iconSparkles, a.tr("Briefing", "Briefing"), true, a.tr("Tages-Briefing (KI · benötigt Tokens)", "Daily Briefing (AI · uses tokens)")).Clicked() {
				a.OpenDayBriefing(context.Background())
			}
			if iconBtn(c.Key("tb-copilot"), iconSidebar, a.tr("KI-Assistent ein/aus (⌘J)", "Toggle AI Assistant (⌘J)"), ui.Color{}, a.showCopilot).Clicked() {
				a.showCopilot = !a.showCopilot
			}
		})
	})
}

// renderAppMenuButton is the ☰ menu: view toggles, language, theme, alerts and help.
func (a *App) renderAppMenuButton(c *ui.Context) {
	iconBtn(c.Key("tb-menu"), iconMenu, a.tr("Menü", "Menu"), ui.Color{}, false).PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(a.tr("Seitenleiste", "Sidebar")).Checked(!a.hideSidebar).Chosen() {
			a.hideSidebar = !a.hideSidebar
		}
		if m.Item(a.tr("KI-Assistent", "AI Assistant")).Checked(a.showCopilot).Shortcut(ui.Cmd, ui.KeyJ).Chosen() {
			a.showCopilot = !a.showCopilot
		}
		if m.Item(a.tr("Desktop-Meldungen", "Desktop alerts")).Checked(a.notifyEnabled).Chosen() {
			a.notifyEnabled = !a.notifyEnabled
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
		if m.Item(a.tr("Thunderbird-Filter öffnen…", "Open Thunderbird filters…")).Disabled(!a.thunderbirdConnected).Chosen() {
			a.OpenThunderbirdFilters(context.Background())
		}
		m.Separator()
		if m.Item(a.tr("Tastenkürzel…", "Keyboard shortcuts…")).Shortcut(0, ui.KeyH).Chosen() {
			a.toggleHelp("shortcuts")
		}
		if m.Item(a.tr("Hilfe…", "Help…")).Shortcut(ui.Cmd, ui.KeySlash).Chosen() {
			a.toggleHelp("all")
		}
	})
}

// footerStatus returns the connection label and its dot color.
func (a *App) footerStatus(t *ui.Theme) (string, ui.Color) {
	switch {
	case a.bridgeOnline && a.thunderbirdConnected:
		return a.tr("Thunderbird verbunden", "Thunderbird live"), t.Success
	case a.bridgeOnline:
		return a.tr("Warte auf Extension", "Waiting for extension"), t.Warning
	case a.bridgeDisabled:
		return a.tr("Bridge deaktiviert", "Bridge disabled"), t.Danger
	default:
		return a.tr("Nicht verbunden", "Offline"), t.Danger
	}
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
