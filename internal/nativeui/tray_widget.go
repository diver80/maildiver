package nativeui

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
)

// RenderStatusWidget builds the native menu bar / tray flyout status widget.
func (a *App) RenderStatusWidget(c *ui.Context) {
	if a.uiTheme != nil && a.uiTheme.theme != nil {
		c.SetTheme(a.uiTheme.theme)
	}
	t := c.Theme()

	root := c.Root().Padding(14).Gap(10)
	if c.Vibrancy() {
		root.Background(ui.Transparent)
	} else {
		root.Background(t.Background).Border(1, t.Border).Radius(12)
	}

	openMainAt := func(nav string) {
		a.setNav(nav)
		if a.onHideFly != nil {
			a.onHideFly()
		}
		if a.onShowMain != nil {
			a.onShowMain()
		}
		if nav != "calendar" {
			a.LoadMessages(context.Background())
		}
	}

	ui.Column(c).Fill().Gap(10).Children(func() {
		// Header row: Brand + live status + quick controls
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			var dotClr ui.Color
			var statusTxt string
			switch {
			case a.bridgeOnline && a.thunderbirdConnected:
				dotClr = t.Success
				statusTxt = a.tr("Thunderbird verbunden", "Thunderbird Live")
			case a.bridgeOnline:
				dotClr = t.Warning
				statusTxt = a.tr("Bridge aktiv · Warte auf Extension", "Bridge active · Waiting for extension")
			case a.bridgeDisabled:
				dotClr = t.Danger
				statusTxt = a.tr("Bridge deaktiviert", "Bridge disabled")
			default:
				dotClr = t.Danger
				statusTxt = a.tr("Thunderbird nicht verbunden", "Thunderbird offline")
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, "OmaThunder").FontSize(14).Bold().SingleLine()
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					statusDot(c, dotClr, 7)
					ui.Text(c, statusTxt).FontSize(11).TextColor(t.TextMuted).SingleLine()
				})
			})
			if a.readOnly {
				pillBadge(c, a.tr("Nur lesen", "Read-Only"), withAlpha(t.Warning, 0.2), t.Warning)
			}
			if iconBtn(c.Key("tw-ref"), iconRefresh, a.tr("Aktualisieren", "Refresh"), ui.Color{}, false).Clicked() {
				a.RefreshAll(context.Background())
			}
			if iconLabelBtn(c.Key("tw-open"), iconExternal, a.tr("Öffnen", "Open"), true, a.tr("Command Center öffnen", "Open Command Center")).Clicked() {
				openMainAt(a.navID)
			}
		})

		// 4 interactive KPI cards
		today := a.todayEvents()
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			a.renderStatusKPI(c, "kpi-unread", a.tr("Ungelesen", "Unread"), fmt.Sprintf("%d", a.unreadCount()), t.Accent, func() {
				openMainAt("focus")
			})
			a.renderStatusKPI(c, "kpi-action", "Action", fmt.Sprintf("%d", a.barSummary.ActionCount), t.Warning, func() {
				openMainAt("action")
			})
			a.renderStatusKPI(c, "kpi-wait", a.tr("Warten", "Waiting"), fmt.Sprintf("%d", a.barSummary.WaitingCount), t.TextMuted, func() {
				openMainAt("action")
			})
			a.renderStatusKPI(c, "kpi-cal", a.tr("Heute", "Today"), fmt.Sprintf("%d", len(today)), t.Success, func() {
				openMainAt("calendar")
			})
		})

		// Scrollable middle: Today's Events + Top Focus Mails + Recent Notifications
		ui.Scroll(c.Key("tw-scroll")).TrackScroll(&a.widgets.flyoutScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				// Today's Agenda
				ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Heute im Kalender", "Today's Calendar")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
						if ui.Button(c.Key("tw-cal-all"), a.tr("Kalender →", "Calendar →")).Clicked() {
							openMainAt("calendar")
						}
					})
					if len(today) == 0 {
						ui.Text(c, a.tr("Keine anstehenden Termine heute.", "No upcoming events today.")).FontSize(12).TextColor(t.TextMuted)
					} else {
						maxEv := min(len(today), 4)
						for i := 0; i < maxEv; i++ {
							ev := today[i]
							ui.Row(c.Key("twev-" + ev.ID)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, formatEventRangeLang(ev.AllDay, ev.Start, "", a.uiLang())).FontSize(11.5).Bold().TextColor(t.Accent).Shrink(0)
								ui.Text(c, ev.Title).FontSize(12).SingleLine().Grow(1)
							})
						}
					}
				})

				// Top Unread / Focus Mails
				ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Aktuelle Fokus-Mails", "Recent Focus Mail")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
						if ui.Button(c.Key("tw-mail-all"), a.tr("Alle →", "All →")).Clicked() {
							openMainAt("focus")
						}
					})
					if len(a.messages) == 0 {
						msg := a.tr("Keine ungelesenen Mails geladen.", "No unread emails loaded.")
						if !a.thunderbirdConnected {
							msg = a.tr("Verbindet automatisch, sobald Thunderbird läuft.", "Connects automatically once Thunderbird is running.")
						}
						ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted)
					} else {
						maxMail := min(len(a.messages), 4)
						for i := 0; i < maxMail; i++ {
							m := a.messages[i]
							row := ui.Row(c.Key("twm-"+m.ID)).FillWidth().Padding(5, 7).Radius(6).Background(t.Background).Gap(6).AlignItems(ui.Center).Children(func() {
								if m.Unread {
									statusDot(c, t.Accent, 6)
								}
								ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
									ui.Text(c, shortSender(m.From)).FontSize(11.5).Bold().SingleLine()
									ui.Text(c, m.Subject).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
								})
								ui.Text(c, formatRelTimeLang(m.Date, a.uiLang())).FontSize(10.5).TextColor(t.TextMuted).Shrink(0)
							})
							if row.Clicked() {
								if a.onHideFly != nil {
									a.onHideFly()
								}
								if a.onShowMain != nil {
									a.onShowMain()
								}
								a.SelectMessage(context.Background(), m.ID)
							}
						}
					}
				})

				// Recent Alerts + Notification toggle
				ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Icon(c, iconBell).Size(13, 13).TextColor(t.TextMuted)
						ui.Text(c, a.tr("Desktop-Meldungen", "Desktop Alerts")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
						ui.Switch(c.Key("tw-notify"), &a.notifyEnabled)
					})
					if len(a.recentAlerts) == 0 {
						ui.Text(c, a.tr("Noch keine neuen Benachrichtigungen.", "No recent notifications yet.")).FontSize(11.5).TextColor(t.TextMuted)
					} else {
						maxAlerts := min(len(a.recentAlerts), 3)
						for i := 0; i < maxAlerts; i++ {
							al := a.recentAlerts[i]
							row := ui.Row(c.Key("twal-" + al.ID)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
								pillBadge(c, al.Subtitle, withAlpha(t.Accent, 0.16), t.Accent)
								ui.Text(c, al.Title+": "+al.Body).FontSize(11.5).SingleLine().Grow(1)
							})
							if row.Clicked() {
								if a.onHideFly != nil {
									a.onHideFly()
								}
								a.HandleNotificationClick(al.ID)
							}
						}
					}
				})
			})
		})

		// Footer Quick Actions
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if iconLabelBtn(c.Key("tw-brief"), iconSparkles, a.tr("Tages-Briefing (KI)", "Daily Briefing (AI)"), true, a.tr("Tages-Briefing starten", "Start daily briefing")).Grow(1).Justify(ui.Center).Clicked() {
				if a.onHideFly != nil {
					a.onHideFly()
				}
				a.OpenDayBriefing(context.Background())
			}
			if !a.readOnly {
				if iconBtn(c.Key("tw-new"), iconCompose, a.tr("Neue E-Mail verfassen", "Compose new email"), ui.Color{}, false).Clicked() {
					if a.onHideFly != nil {
						a.onHideFly()
					}
					if a.onShowMain != nil {
						a.onShowMain()
					}
					a.OpenComposeNew()
				}
			}
		})
	})
}

func (a *App) renderStatusKPI(c *ui.Context, key, label, value string, accent ui.Color, onClick func()) {
	t := c.Theme()
	card := ui.Column(c.Key(key)).Grow(1).MinWidth(0).Padding(8, 10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(2).Children(func() {
		ui.Text(c, value).FontSize(18).Bold().TextColor(accent).SingleLine()
		ui.Text(c, label).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
	})
	if card.Clicked() && onClick != nil {
		onClick()
	}
}
