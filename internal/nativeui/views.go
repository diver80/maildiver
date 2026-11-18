package nativeui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/omathunder/internal/bridgeclient"
)

// RenderMain builds the entire native GPU-accelerated OmaThunder Command Center UI.
func (a *App) RenderMain(c *ui.Context) {
	if a.uiTheme != nil && a.uiTheme.theme != nil {
		c.SetTheme(a.uiTheme.theme)
	}
	t := c.Theme()

	// Flush all queued background toasts onto the window's native toast viewport.
	if len(a.toasts) > 0 {
		for _, tst := range a.toasts {
			c.AddToast(ui.Toast{Title: tst.msg, Type: tst.kind})
		}
		a.toasts = nil
	}

	a.handleShortcuts(c)

	c.Root().Background(t.Background)

	ui.Column(c).Fill().Children(func() {
		a.renderToolbar(c)
		ui.Divider(c)

		ui.Row(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if !a.hideSidebar {
				a.renderSidebar(c)
				ui.Divider(c).Vertical()
			}

			if a.showCopilot {
				if a.copilotSplit <= 0 {
					w, _ := c.Size()
					if w > 660 {
						a.copilotSplit = max(420, w-a.sidebarWidth()-380)
					} else {
						a.copilotSplit = 540
					}
				}
				ui.Split(c, &a.copilotSplit, func() {
					a.renderWorkspace(c)
				}, func() {
					a.renderCopilotPane(c)
				}).Grow(1).MinWidth(0).FillHeight()
			} else {
				ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
					a.renderWorkspace(c)
				})
			}
		})
		ui.Divider(c)
		a.renderFooter(c)
	})

	a.renderComposeModal(c)
	a.renderEventModal(c)
	a.renderQuickRuleModal(c)
	a.renderCalDeleteModal(c)
	a.renderMovePicker(c)
	a.renderArchiveModal(c)
	a.renderHelpModal(c)
}

func (a *App) themeLabelList() []string {
	out := make([]string, len(a.themeChoices))
	for i, ch := range a.themeChoices {
		out[i] = ch.Label
	}
	return out
}

func (a *App) renderSidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(a.sidebarWidth()).FillHeight().Background(t.Surface).Shrink(0).Children(func() {
		sb := ui.Sidebar(c, &a.navID, func() {
			ui.SidebarSection(c, a.tr("Arbeitsbereich", "Workspace"), &a.secViewsOpen, func() {
				ui.SidebarItem(c, "focus", iconFocus, a.tr("Fokus (Ungelesen)", "Focus (Unread)")).Children(func() {
					if n := a.unreadCount(); n > 0 {
						ui.Badge(c, fmt.Sprintf("%d", n))
					}
				})
				ui.SidebarItem(c, "recent", iconMail, a.tr("Aktuelle Mails", "Recent Mail")).Children(func() {
					if a.totalMessages > 0 && a.navID == "recent" {
						ui.Badge(c, fmt.Sprintf("%d", a.totalMessages))
					}
				})
				ui.SidebarItem(c, "action", iconBolt, a.tr("Action / Warten", "Action / Waiting")).Children(func() {
					if n := a.barSummary.ActionCount + a.barSummary.WaitingCount; n > 0 {
						ui.Badge(c, fmt.Sprintf("%d", n))
					}
				})
				ui.SidebarItem(c, "calendar", iconCal, a.tr("Kalender & Termine", "Calendar & Events")).Children(func() {
					if n := len(a.todayEvents()); n > 0 {
						ui.Badge(c, fmt.Sprintf("%d", n))
					}
				})
			})

			a.renderFolderSections(c)
		}).Grow(1).MinHeight(0).FillWidth()

		if sb.Changed() {
			a.selectedID = ""
			a.selectedMsg = nil
			a.setNav(a.navID)
			if a.navID != "calendar" {
				a.LoadMessages(context.Background())
			}
		}

		// Compact "Today" mini-agenda & notification switch at bottom of sidebar
		ui.Divider(c)
		ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Heute", "Today")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
				if ui.Button(c.Key("sb-cal"), a.tr("Alle", "All")).Clicked() {
					a.setNav("calendar")
				}
			})
			today := a.todayEvents()
			if len(today) == 0 {
				ui.Text(c, a.tr("Keine Termine heute", "No events today")).FontSize(11.5).TextColor(t.TextMuted)
			} else {
				maxShow := min(len(today), 3)
				for i := 0; i < maxShow; i++ {
					ev := today[i]
					ui.Row(c).FillWidth().Padding(4, 6).Radius(6).Background(t.Background).Border(1, t.Border).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, formatEventRangeLang(ev.AllDay, ev.Start, "", a.uiLang())).FontSize(11).Bold().TextColor(t.Accent).Shrink(0)
						ui.Text(c, ev.Title).FontSize(11.5).SingleLine().Grow(1)
					})
				}
			}
		})
	})
}

func (a *App) renderWorkspace(c *ui.Context) {
	if a.navID == "calendar" {
		a.renderCalendarWorkspace(c)
		return
	}
	a.renderMailWorkspace(c)
}

func (a *App) renderMailWorkspace(c *ui.Context) {
	t := c.Theme()
	ui.Split(c, &a.mailSplit, func() {
		// Message list column
		ui.Column(c).Fill().Background(t.Background).Children(func() {
			ui.Row(c).FillWidth().Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				title := a.currentViewTitle()
				ui.Text(c, title).FontSize(13).Bold().Grow(1).SingleLine()
				ui.Text(c, a.trf("%d Mails", "%d emails", len(a.messages))).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
			})

			checked := a.CheckedMessageIDs()
			if len(checked) > 0 {
				ui.Row(c.Key("bulk-bar")).FillWidth().Padding(6, 10).Gap(6).AlignItems(ui.Center).Background(withAlpha(t.Accent, 0.12)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Text(c, a.trf("%d markiert", "%d selected", len(checked))).FontSize(11.5).Bold().TextColor(t.Accent).Grow(1).SingleLine()
					if iconLabelBtn(c.Key("bulk-rule"), iconFilter, a.tr("Quick-Regel", "Quick Rule"), true, a.tr("Aus markierten Mails Thunderbird-Filter erstellen (q · mit Preview)", "Create Thunderbird filter from selected emails (q · with preview)")).Clicked() {
						a.OpenQuickRuleWizard()
					}
					if iconBtn(c.Key("bulk-read"), iconMailOpen, a.tr("Markierte als gelesen markieren (u)", "Mark selected as read (u)"), t.Accent, false).Clicked() {
						readTrue := true
						a.PerformBatchAction(context.Background(), checked, bridgeclient.MessageActionsRequest{Read: &readTrue}, a.tr("Als gelesen markiert", "Marked as read"))
					}
					if !a.readOnly {
						if iconBtn(c.Key("bulk-archive"), iconArchive, a.tr("Markierte archivieren (e)", "Archive selected (e)"), ui.Color{}, false).Clicked() {
							a.PerformBatchAction(context.Background(), checked, bridgeclient.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"))
						}
						if iconBtn(c.Key("bulk-move"), iconFolderInput, a.tr("Markierte verschieben nach… (v)", "Move selected to… (v)"), ui.Color{}, false).Clicked() {
							a.OpenMovePicker()
						}
						if iconBtn(c.Key("bulk-trash"), iconTrash, a.tr("Markierte in Papierkorb verschieben (d d)", "Move selected to Trash (d d)"), t.Danger, false).Clicked() {
							a.PerformBatchAction(context.Background(), checked, bridgeclient.MessageActionsRequest{Trash: true}, a.tr("In Papierkorb verschoben", "Moved to Trash"))
						}
					}
					if ui.Button(c.Key("bulk-clear"), "✕").Padding(4, 7).Tooltip(a.tr("Auswahl aufheben (esc)", "Clear selection (esc)")).Clicked() {
						a.ClearMailSelection()
					}
				})
			}

			if !a.bridgeOnline || !a.thunderbirdConnected {
				a.renderOfflineBanner(c)
				return
			}
			if len(a.messages) == 0 {
				ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
					ui.Text(c, a.tr("Keine Nachrichten in dieser Ansicht", "No messages in this view")).FontSize(13).Bold()
					ui.Text(c, a.tr("Prüfe Filter, Konto oder klicke auf ↻ Aktualisieren.", "Check filters, account, or click ↻ Refresh.")).FontSize(12).TextColor(t.TextMuted)
				})
				return
			}

			ls := a.ensureListState()
			crossFolder := !strings.HasPrefix(a.navID, "folder:")
			listEl := ui.List(c, ls, len(a.messages), func(i int) {
				if i < 0 || i >= len(a.messages) {
					return
				}
				m := a.messages[i]
				selected := m.ID == a.selectedID || ls.Selected != nil && *ls.Selected == i
				isChecked := a.IsMailChecked(m.ID)
				bg := t.Background
				if isChecked {
					bg = withAlpha(t.Accent, 0.18)
				} else if selected {
					bg = withAlpha(t.Accent, 0.11)
				}

				ui.Row(c.Key("mail-"+m.ID)).FillWidth().Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).AlignItems(ui.Center).Children(func() {
					chkBg := t.Surface
					chkBorder := t.Border
					if isChecked {
						chkBg = t.Accent
						chkBorder = t.Accent
					}
					chkTap := ui.Row(c.Key("chk-"+m.ID)).Padding(12, 4, 12, 10).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Box(c).Size(15, 15).Radius(4).Background(chkBg).Border(1.2, chkBorder).Center().Children(func() {
							if isChecked {
								ui.Icon(c, iconCheck).Size(11, 11).TextColor(t.Background)
							}
						})
					})
					if chkTap.Clicked() {
						a.ToggleMailChecked(i, c.Modifiers()&ui.Shift != 0)
					}

					// Two lines, as dense as Omamail's: sender + time, then subject with small tags.
					// The folder tag only shows in views that span several folders.
					row := ui.Column(c.Key("mail-body-"+m.ID)).Grow(1).MinWidth(0).Padding(8, 12, 8, 6).Gap(3).Children(func() {
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							if m.Unread {
								statusDot(c, t.Accent, 6)
							}
							// Muted only on plain rows: the highlighted row brings its own text color.
							st := ui.Text(c, shortSender(m.From)).FontSize(11.5).Grow(1).MinWidth(0).SingleLine()
							if m.Unread {
								st.Bold()
							} else if !selected {
								st.TextColor(t.TextMuted)
							}
							if m.Flagged {
								ui.Icon(c, iconStarFilled).Size(11, 11).TextColor(t.Warning).Shrink(0)
							}
							tm := ui.Text(c, formatRelTimeLang(m.Date, a.uiLang())).FontSize(11).Shrink(0)
							if !selected {
								tm.TextColor(t.TextMuted)
							}
						})
						subj := m.Subject
						if subj == "" {
							subj = a.tr("(Kein Betreff)", "(No Subject)")
						}
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							subTxt := ui.Text(c, subj).FontSize(13).Grow(1).MinWidth(0).SingleLine()
							if m.Unread {
								subTxt.Bold()
							}
							if crossFolder && m.Folder != "" {
								miniTag(c, m.Folder, t.TextMuted, t.Surface)
							}
							if col := a.triageColumnFor(m.ID); col != "" {
								miniTag(c, strings.ToUpper(col), t.Accent, withAlpha(t.Accent, 0.16))
							}
						})
					})
					if row.Clicked() {
						mods := c.Modifiers()
						if mods&(ui.Cmd|ui.Ctrl|ui.Shift) != 0 {
							a.ToggleMailChecked(i, mods&ui.Shift != 0)
						} else {
							a.SelectMessage(context.Background(), m.ID)
						}
					}
				})
			}).Fill()

			if listEl.Changed() && a.mailList.selected >= 0 && a.mailList.selected < len(a.messages) {
				id := a.messages[a.mailList.selected].ID
				if id != a.selectedID {
					a.SelectMessage(context.Background(), id)
				}
			}
		})
	}, func() {
		a.renderMessageInspector(c)
	}).Fill()
}

func (a *App) renderOfflineBanner(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(28).Gap(10).Children(func() {
		if a.bridgeDisabled {
			ui.Text(c, a.tr("Lokale Bridge ist deaktiviert", "Local bridge is disabled")).FontSize(15).Bold()
			ui.Text(c, a.tr(
				"Die App spricht über die lokale REST/SSE-Bridge mit Thunderbird. Sie ist in config.json ausgeschaltet (bridge.enabled=false). Aktivieren startet sie beim laufenden Host innerhalb weniger Sekunden.",
				"The app talks to Thunderbird via the local REST/SSE bridge. It is switched off in config.json (bridge.enabled=false). Enabling starts it within a few seconds while the host is running.",
			)).FontSize(12.5).TextColor(t.TextMuted)
			lbl := a.tr("Bridge aktivieren", "Enable bridge")
			if a.enablingBridge {
				lbl = a.tr("Aktiviere…", "Enabling…")
			}
			btn := ui.PrimaryButton(c.Key("enable-bridge"), lbl)
			if a.enablingBridge {
				btn.Disabled(true)
			}
			if btn.Clicked() && !a.enablingBridge {
				a.EnableBridge(context.Background())
			}
			return
		}
		ui.Text(c, a.tr("Thunderbird nicht verbunden", "Thunderbird not connected")).FontSize(15).Bold()
		ui.Text(c, a.tr(
			"Starte Thunderbird mit aktivierter OmaThunder-Erweiterung. Der KI-Assistent und die Live-Bridge verbinden sich automatisch.",
			"Launch Thunderbird with the OmaThunder extension enabled. The AI Assistant and live bridge connect automatically.",
		)).FontSize(12.5).TextColor(t.TextMuted)
		if a.lastError != "" {
			ui.Text(c, a.lastError).FontSize(11.5).TextColor(t.Danger)
		}
		if ui.PrimaryButton(c.Key("retry-conn"), a.tr("Verbindung prüfen", "Check Connection")).Clicked() {
			a.RefreshAll(context.Background())
		}
	})
}

func (a *App) renderMessageInspector(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if a.selectedMsg == nil && a.selectedID != "" {
			ui.Column(c).Fill().Center().Padding(28).Gap(8).Children(func() {
				if a.inspectorError != "" {
					ui.Text(c, a.inspectorError).FontSize(12.5).TextColor(t.Danger)
					return
				}
				ui.Spinner(c)
				ui.Text(c, a.tr("Lade Mail…", "Loading email…")).FontSize(12.5).TextColor(t.TextMuted)
			})
			return
		}
		if a.selectedMsg == nil {
			ui.Column(c).Fill().Center().Padding(28).Gap(8).Children(func() {
				ui.Text(c, a.tr("Keine Mail ausgewählt", "No Email Selected")).FontSize(15).Bold()
				ui.Text(c, a.tr(
					"Wähle links eine Nachricht aus, um Details, KI-Zusammenfassung, Antwort-Entwurf, Termin- oder Kontakterkennung zu starten.",
					"Select an email on the left to view details or trigger AI summary, reply draft, event, or contact extraction.",
				)).FontSize(12.5).TextColor(t.TextMuted)
			})
			return
		}
		msg := a.selectedMsg

		ui.Scroll(c.Key("insp-scroll")).TrackScroll(&a.widgets.inspectorScroll).Fill().Children(func() {
			ui.Column(c).FillWidth().Padding(18).Gap(14).Children(func() {
				// Header block
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					subj := msg.Subject
					if subj == "" {
						subj = a.tr("(Kein Betreff)", "(No Subject)")
					}
					ui.Text(c, subj).FontSize(18).Bold()
					ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Von: ", "From: ")+msg.From).FontSize(12).Bold()
						ui.Text(c, "· "+formatDateTimeLang(msg.Date, a.uiLang())).FontSize(12).TextColor(t.TextMuted)
						if msg.Folder != "" {
							pillBadge(c, msg.Folder, t.Surface, t.TextMuted)
						}
					})
					if len(msg.To) > 0 {
						ui.Text(c, a.tr("An: ", "To: ")+strings.Join(msg.To, ", ")).FontSize(11.5).TextColor(t.TextMuted)
					}
				})

				// Mail actions first (the single keys are in the tooltips), the AI actions below.
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					if iconBtn(c.Key("act-reply-edit"), iconReply, a.tr("Antworten (r)", "Reply (r)"), ui.Color{}, false).Clicked() {
						a.OpenComposeReply(msg, a.inspectorDraft)
					}
					readIcon := iconMailOpen
					readTip := a.tr("Als gelesen markieren", "Mark as read")
					readClr := t.Accent
					readActive := msg.Unread
					targetRead := true
					if !msg.Unread {
						readIcon = iconMailUnread
						readTip = a.tr("Als ungelesen markieren", "Mark as unread")
						readClr = ui.Color{}
						targetRead = false
					}
					if iconBtn(c.Key("act-read"), readIcon, readTip+" (u)", readClr, readActive).Clicked() {
						a.PerformMessageAction(context.Background(), msg.ID, bridgeclient.MessageActionsRequest{Read: &targetRead}, a.tr("Lesestatus aktualisiert", "Read status updated"))
					}
					starIcon := iconStar
					starTip := a.tr("Mit Stern markieren", "Add star")
					starClr := ui.Color{}
					targetFlag := true
					if msg.Flagged {
						starIcon = iconStarFilled
						starTip = a.tr("Markierung entfernen", "Remove star")
						starClr = t.Warning
						targetFlag = false
					}
					if iconBtn(c.Key("act-star"), starIcon, starTip+" (s)", starClr, msg.Flagged).Clicked() {
						a.PerformMessageAction(context.Background(), msg.ID, bridgeclient.MessageActionsRequest{Flagged: &targetFlag}, a.tr("Markierung aktualisiert", "Star updated"))
					}
					if !a.readOnly {
						ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 4)
						if iconBtn(c.Key("act-archive"), iconArchive, a.tr("Archivieren (e)", "Archive (e)"), ui.Color{}, false).Clicked() {
							a.performMessageAction(context.Background(), msg.ID, bridgeclient.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(msg.ID))
						}
						if iconBtn(c.Key("act-move"), iconFolderInput, a.tr("Verschieben nach… (v)", "Move to… (v)"), ui.Color{}, false).Clicked() {
							a.OpenMovePicker()
						}
						if iconBtn(c.Key("act-trash"), iconTrash, a.tr("In Papierkorb verschieben (d)", "Move to Trash (d)"), t.Danger, false).Clicked() {
							a.performMessageAction(context.Background(), msg.ID, bridgeclient.MessageActionsRequest{Trash: true}, a.tr("In Papierkorb verschoben", "Moved to Trash"), a.neighborAfterRemoval(msg.ID))
						}
					}
					ui.Spacer(c)
					if iconLabelBtn(c.Key("act-rule"), iconFilter, a.tr("Regel", "Rule"), false, a.tr("Quick-Regel-Assistent für Thunderbird-Filter öffnen (q · 0 Tokens)", "Open Quick-Rule Wizard for Thunderbird filters (q · 0 tokens)")).Clicked() {
						a.OpenQuickRuleWizard()
					}
				})
				ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					if iconLabelBtn(c.Key("ai-sum"), iconSparkles, a.tr("Zusammenfassen", "Summarize"), true, a.tr("Mail mit KI zusammenfassen (verbraucht KI-Tokens)", "Summarize email with AI (uses AI tokens)")).Clicked() {
						a.RunInspectorAI(context.Background(), "summarize")
					}
					if iconLabelBtn(c.Key("ai-rep"), iconPen, a.tr("Entwurf", "Draft"), false, a.tr("Antwort mit KI entwerfen (verbraucht KI-Tokens)", "Draft reply with AI (uses AI tokens)")).Clicked() {
						a.RunInspectorAI(context.Background(), "draft_reply")
					}
					if iconLabelBtn(c.Key("ai-ev"), iconCalPlus, a.tr("Termin", "Event"), false, a.tr("Termine aus Mail erkennen (verbraucht KI-Tokens)", "Extract event from email (uses AI tokens)")).Clicked() {
						a.RunInspectorAI(context.Background(), "extract_events")
					}
					if iconLabelBtn(c.Key("ai-contacts"), iconUserPlus, a.tr("Kontakte", "Contacts"), false, a.tr("Kontakte aus E-Mail-Footer per KI erkennen (verbraucht KI-Tokens)", "Extract contacts from footer with AI (uses AI tokens)")).Clicked() {
						a.RunInspectorAI(context.Background(), "extract_contacts")
					}
				})

				// AI Result Card (rendered natively via markdown.View)
				if a.inspectorLoading || a.inspectorResult != "" || a.inspectorError != "" || len(a.inspectorContacts) > 0 {
					ui.Column(c).FillWidth().Padding(14).Radius(10).Border(1, t.Accent).Background(t.Surface).Gap(10).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Icon(c, iconSparkles).Size(14, 14).TextColor(t.Accent)
							ui.Text(c, a.tr("KI-Analyse", "AI Analysis")+" ("+inspectorModeLabelLang(a.inspectorMode, a.uiLang())+")").FontSize(12.5).Bold().TextColor(t.Accent).Grow(1)
							if a.inspectorLoading {
								ui.Spinner(c)
							}
						})
						if a.inspectorError != "" {
							ui.Text(c, a.inspectorError).FontSize(12).TextColor(t.Danger)
						}
						if a.inspectorResult != "" {
							markdown.View(c, a.inspectorResult, &a.widgets.mdInspector)
						}
						if len(a.inspectorContacts) > 0 {
							a.renderInspectorContacts(c)
						}
						if a.inspectorDraft != "" {
							ui.Row(c).Gap(8).Wrap().Children(func() {
								if !a.readOnly && iconLabelBtn(c.Key("draft-save"), iconCheck, a.tr("Entwurf speichern", "Save Draft"), true, a.tr("Als Entwurf in Thunderbird speichern", "Save as draft in Thunderbird")).Clicked() {
									a.SaveDraft(context.Background(), bridgeclient.ComposeRequest{
										ReplyToMessageID: msg.ID,
										ReplyType:        "reply",
										Body:             a.inspectorDraft,
									}, a.tr("Entwurf in Thunderbird gespeichert", "Draft saved in Thunderbird"))
								}
								if iconLabelBtn(c.Key("draft-open"), iconExternal, a.tr("In Thunderbird öffnen", "Open in Thunderbird"), false, a.tr("Compose-Fenster in Thunderbird öffnen", "Open compose window in Thunderbird")).Clicked() {
									a.SaveDraft(context.Background(), bridgeclient.ComposeRequest{
										ReplyToMessageID: msg.ID,
										ReplyType:        "reply",
										Body:             a.inspectorDraft,
										OpenWindow:       true,
									}, a.tr("Compose-Fenster geöffnet", "Compose window opened"))
								}
							})
						}
						if a.inspectorEvent != nil && !a.readOnly {
							ui.Row(c).Gap(8).Children(func() {
								if iconLabelBtn(c.Key("ev-adopt"), iconCalPlus, a.tr("Termin übernehmen: ", "Add Event: ")+a.inspectorEvent.Title, true, "").Clicked() {
									a.OpenNewEventModal(a.inspectorEvent)
								}
							})
						}
					})
				}

				ui.Divider(c)

				// Body
				a.renderMailBody(c, msg)

				// Attachments
				if len(msg.Attachments) > 0 {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
						ui.Text(c, a.trf("Anhänge (%d)", "Attachments (%d)", len(msg.Attachments))).FontSize(12).Bold()
						ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
							for _, att := range msg.Attachments {
								pillBadge(c, fmt.Sprintf("%s (%d KB)", att.Name, max(1, att.Size/1024)), t.Background, t.Text)
							}
						})
					})
				}
			})
		})
	})
}

func (a *App) renderCalendarWorkspace(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).FillWidth().Padding(12, 16).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, a.tr("Kalender & Termine", "Calendar & Events")).FontSize(15).Bold().Grow(1)
			winIdx := 1
			switch a.calDays {
			case 7:
				winIdx = 0
			case 30:
				winIdx = 2
			}
			if ui.Segmented(c, &winIdx, a.tr("7 Tage", "7 Days"), a.tr("14 Tage", "14 Days"), a.tr("30 Tage", "30 Days")).Changed() {
				switch winIdx {
				case 0:
					a.calDays = 7
				case 2:
					a.calDays = 30
				default:
					a.calDays = 14
				}
				a.RefreshAll(context.Background())
			}
			if !a.readOnly {
				if iconLabelBtn(c.Key("cal-new"), iconCalPlus, a.tr("Neuer Termin", "New Event"), true, a.tr("Neuen Kalendereintrag anlegen", "Create new calendar event")).Clicked() {
					a.OpenNewEventModal(nil)
				}
			}
		})

		if !a.bridgeOnline || !a.thunderbirdConnected {
			a.renderOfflineBanner(c)
			return
		}

		ui.Scroll(c.Key("cal-scroll")).TrackScroll(&a.widgets.calScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
				if len(a.events) == 0 {
					ui.Text(c, a.trf("Keine Termine in den nächsten %d Tagen.", "No events in the next %d days.", a.calDays)).FontSize(13).TextColor(t.TextMuted)
					return
				}

				grouped := make(map[string][]bridgeclient.CalendarEvent)
				var keys []string
				for _, ev := range a.events {
					dk := eventDayKey(ev.Start)
					if _, ok := grouped[dk]; !ok {
						keys = append(keys, dk)
					}
					grouped[dk] = append(grouped[dk], ev)
				}
				sort.Strings(keys)

				for _, dk := range keys {
					ui.Column(c.Key("calday-" + dk)).FillWidth().Gap(8).Children(func() {
						ui.Text(c, dayHeadingLang(dk, a.uiLang())).FontSize(12.5).Bold().TextColor(t.Accent)
						for _, ev := range grouped[dk] {
							evCopy := ev
							ui.Row(c).FillWidth().Padding(10, 12).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(12).AlignItems(ui.Center).Children(func() {
								ui.Text(c, formatEventRangeLang(evCopy.AllDay, evCopy.Start, evCopy.End, a.uiLang())).FontSize(12).Bold().TextColor(t.Accent).Width(110).Shrink(0)
								ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
									ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
										ui.Text(c, evCopy.Title).FontSize(13).Bold()
										if evCopy.IsRecurring || evCopy.RecurrenceID != "" {
											pillBadge(c, a.tr("Serie", "Recurring"), t.Background, t.TextMuted)
										}
									})
									var meta []string
									if evCopy.Calendar != "" {
										meta = append(meta, evCopy.Calendar)
									}
									if evCopy.Location != "" {
										meta = append(meta, evCopy.Location)
									}
									if len(meta) > 0 {
										ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(t.TextMuted)
									}
									if evCopy.Description != "" {
										ui.Text(c, truncateText(evCopy.Description, 140)).FontSize(11.5).TextColor(t.TextMuted)
									}
								})
								if !a.readOnly && !evCopy.ReadOnly {
									if iconBtn(c.Key("ev-del-"+evCopy.ID+"-"+evCopy.RecurrenceID), iconTrash, a.tr("Termin löschen…", "Delete event…"), t.Danger, false).Clicked() {
										a.RequestDeleteCalendarEvent(evCopy)
									}
								}
							})
						}
					})
				}
			})
		})
	})
}

func (a *App) renderComposeModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.composeOpen, func() {
		ui.Column(c).Width(560).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			title := a.tr("Neue E-Mail", "New Email")
			if a.composeReplyTo != "" {
				title = a.tr("Antwort verfassen", "Compose Reply")
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("cmp-x"), "✕").Clicked() {
					a.composeOpen = false
				}
			})
			ui.TextInput(c.Key("cmp-to"), &a.composeTo).Placeholder(a.tr("An (Komma-getrennt)…", "To (comma-separated)…")).FillWidth()
			ui.TextInput(c.Key("cmp-cc"), &a.composeCC).Placeholder(a.tr("CC (optional)…", "CC (optional)…")).FillWidth()
			ui.TextInput(c.Key("cmp-sub"), &a.composeSubject).Placeholder(a.tr("Betreff…", "Subject…")).FillWidth()
			ui.TextArea(c.Key("cmp-body"), &a.composeBody).Placeholder(a.tr("Nachrichtentext…", "Message body…")).FillWidth().Height(190)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.TextMuted)
				ui.Text(c, a.tr("Signatur", "Signature")).FontSize(12).TextColor(t.TextMuted).
					Tooltip(a.tr("Standard: wie in Thunderbird für diese Identität eingestellt. Signieren braucht einen OpenPGP- oder S/MIME-Schlüssel.",
						"Default: as set in Thunderbird for this identity. Signing needs an OpenPGP or S/MIME key."))
				ui.Segmented(c.Key("cmp-sign"), &a.composeSign, a.tr("Standard", "Default"), a.tr("Signieren", "Sign"), a.tr("Ohne", "None"))
			})
			if a.composeReplyEncrypted {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Icon(c, iconLock).Size(13, 13).TextColor(t.Success)
					ui.Text(c, a.tr("Antwort auf eine verschlüsselte Mail: wird verschlüsselt gesendet. „Entwurf speichern“ öffnet sie in Thunderbird.",
						"Reply to an encrypted email: it is sent encrypted. “Save Draft” opens it in Thunderbird.")).FontSize(11.5).TextColor(t.TextMuted).Grow(1)
				})
			}

			if a.readOnly {
				ui.Text(c, a.tr("Read-Only-Modus aktiv: Speichern/Senden in Thunderbird deaktiviert.", "Read-only mode active: saving/sending in Thunderbird is disabled.")).FontSize(11.5).TextColor(t.Warning)
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if !a.readOnly {
					if ui.Button(c.Key("cmp-draft"), a.tr("Entwurf speichern", "Save Draft")).Clicked() {
						a.SaveDraft(context.Background(), a.buildComposeReq(false), a.tr("Entwurf in Thunderbird gespeichert", "Draft saved in Thunderbird"))
					}
					if ui.Button(c.Key("cmp-open"), a.tr("In Thunderbird öffnen", "Open in Thunderbird")).Clicked() {
						a.SaveDraft(context.Background(), a.buildComposeReq(true), a.tr("Compose-Fenster in Thunderbird geöffnet", "Compose window opened in Thunderbird"))
					}
					if a.cfg.Bridge.AllowSend {
						sendLabel := a.tr("Direkt senden", "Send Now")
						if a.composeConfirm {
							sendLabel = a.tr("Wirklich senden?", "Confirm Send?")
						}
						if a.composeBusy {
							sendLabel = a.tr("Sende…", "Sending…")
						}
						if ui.PrimaryButton(c.Key("cmp-send"), sendLabel).Clicked() && !a.composeBusy {
							if !a.composeConfirm {
								a.composeConfirm = true
							} else {
								a.SendCompose(context.Background(), a.buildComposeReq(false))
							}
						}
					}
				}
			})
		})
	})
}

func (a *App) buildComposeReq(openWindow bool) bridgeclient.ComposeRequest {
	return bridgeclient.ComposeRequest{
		AccountID:        a.selectedAccount,
		To:               splitEmailList(a.composeTo),
		CC:               splitEmailList(a.composeCC),
		Subject:          strings.TrimSpace(a.composeSubject),
		Body:             a.composeBody,
		ReplyToMessageID: a.composeReplyTo,
		ReplyType:        a.composeReplyType,
		OpenWindow:       openWindow,
		Sign:             [...]string{"", "on", "off"}[min(max(a.composeSign, 0), 2)],
	}
}

func (a *App) renderEventModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.eventOpen, func() {
		ui.Column(c).Width(480).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Neuer Kalendereintrag", "New Calendar Event")).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("ev-x"), "✕").Clicked() {
					a.eventOpen = false
				}
			})
			if len(a.eventCalLabels) > 0 {
				ui.Select(c.Key("ev-cal"), &a.eventCalendar, a.eventCalLabels).FillWidth()
			}
			ui.TextInput(c.Key("ev-title"), &a.eventTitle).Placeholder(a.tr("Titel…", "Title…")).FillWidth()
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.TextInput(c.Key("ev-start"), &a.eventStart).Placeholder("Start (YYYY-MM-DDTHH:MM)").Grow(1)
				ui.TextInput(c.Key("ev-end"), &a.eventEnd).Placeholder(a.tr("Ende (YYYY-MM-DDTHH:MM)", "End (YYYY-MM-DDTHH:MM)")).Grow(1)
			})
			ui.Checkbox(c.Key("ev-allday"), &a.eventAllDay, a.tr("Ganztägiger Termin", "All-day event"))
			ui.TextInput(c.Key("ev-loc"), &a.eventLocation).Placeholder(a.tr("Ort / Meeting-Link…", "Location / Meeting link…")).FillWidth()
			ui.TextArea(c.Key("ev-desc"), &a.eventDesc).Placeholder(a.tr("Notizen…", "Notes…")).FillWidth().Height(90)

			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("ev-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
					a.eventOpen = false
				}
				saveLbl := a.tr("Termin speichern", "Save Event")
				if a.eventBusy {
					saveLbl = a.tr("Speichere…", "Saving…")
				}
				if ui.PrimaryButton(c.Key("ev-save"), saveLbl).Clicked() && !a.eventBusy {
					a.CreateCalendarEvent(context.Background(), bridgeclient.CreateCalendarEventRequest{
						CalendarID:  a.eventCalByLabel[a.eventCalendar],
						Title:       strings.TrimSpace(a.eventTitle),
						Start:       normalizeDateTimeInput(a.eventStart),
						End:         normalizeDateTimeInput(a.eventEnd),
						AllDay:      a.eventAllDay,
						Location:    strings.TrimSpace(a.eventLocation),
						Description: strings.TrimSpace(a.eventDesc),
					})
				}
			})
		})
	})
}

func (a *App) currentViewTitle() string {
	switch a.navID {
	case "focus":
		return a.tr("Fokus (Ungelesen)", "Focus (Unread)")
	case "recent":
		return a.tr("Aktuelle Mails", "Recent Mail")
	case "action":
		return a.tr("Action & Warten", "Action & Waiting")
	case "calendar":
		return a.tr("Kalender & Termine", "Calendar & Events")
	default:
		if strings.HasPrefix(a.navID, "folder:") {
			fid := strings.TrimPrefix(a.navID, "folder:")
			for _, f := range a.folders {
				if f.ID == fid {
					if f.Name != "" {
						return f.Name
					}
					return f.Path
				}
			}
			return a.tr("Ordner", "Folder")
		}
		return a.tr("Postfach", "Mailbox")
	}
}

func inspectorModeLabel(mode string) string {
	return inspectorModeLabelLang(mode, "de")
}

func inspectorModeLabelLang(mode, lang string) string {
	switch mode {
	case "summarize":
		return trLang(lang, "Zusammenfassung", "Summary")
	case "draft_reply":
		return trLang(lang, "Antwort-Entwurf", "Reply Draft")
	case "extract_events":
		return trLang(lang, "Termin-Erkennung", "Event Extraction")
	case "extract_contacts":
		return trLang(lang, "Kontakte aus Footer", "Footer Contacts")
	default:
		return mode
	}
}

func (a *App) renderInspectorContacts(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			ui.Icon(c, iconUserPlus).Size(14, 14).TextColor(t.Accent)
			ui.Text(c, a.trf("Erkannte Kontakte (%d)", "Detected Contacts (%d)", len(a.inspectorContacts))).FontSize(12.5).Bold().Grow(1)
			if len(a.contactsBookLabels) > 0 && !a.readOnly {
				ui.Select(c.Key("ct-book"), &a.selectedContactBook, a.contactsBookLabels).Width(175)
			}
		})

		for i := range a.inspectorContacts {
			p := a.inspectorContacts[i]
			if p.Ignored {
				continue
			}
			idx := i
			ex := p.Extracted
			name := ex.DisplayName
			if name == "" {
				name = strings.TrimSpace(ex.FirstName + " " + ex.LastName)
			}
			if name == "" {
				name = ex.Email
			}

			ui.Column(c.Key(fmt.Sprintf("ct-card-%d", idx))).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
					ui.Text(c, name).FontSize(13).Bold()
					if ex.Email != "" && ex.Email != name {
						ui.Text(c, ex.Email).FontSize(11.5).TextColor(t.TextMuted)
					}
					ui.Spacer(c)
					switch {
					case p.Applied && p.AppliedAction == "enriched":
						pillBadge(c, a.tr("Vervollständigt", "Completed"), withAlpha(t.Success, 0.18), t.Success)
					case p.Applied:
						pillBadge(c, a.tr("Importiert", "Imported"), withAlpha(t.Success, 0.18), t.Success)
					case p.Status == "new":
						pillBadge(c, a.tr("Neuer Kontakt", "New Contact"), withAlpha(t.Accent, 0.18), t.Accent)
					case p.Status == "enrich":
						pillBadge(c, a.tr("Daten fehlen", "Missing Fields"), withAlpha(t.Warning, 0.20), t.Warning)
					default:
						pillBadge(c, a.tr("Bereits vollständig", "Already Complete"), withAlpha(t.Success, 0.18), t.Success)
					}
				})

				var meta []string
				if ex.JobTitle != "" && ex.Company != "" {
					meta = append(meta, ex.JobTitle+" · "+ex.Company)
				} else if ex.Company != "" {
					meta = append(meta, ex.Company)
				} else if ex.JobTitle != "" {
					meta = append(meta, ex.JobTitle)
				}
				if ex.Phone != "" {
					meta = append(meta, a.tr("Tel: ", "Phone: ")+ex.Phone)
				}
				if ex.Mobile != "" {
					meta = append(meta, a.tr("Mobil: ", "Mobile: ")+ex.Mobile)
				}
				if ex.Website != "" {
					meta = append(meta, ex.Website)
				}
				if len(meta) > 0 {
					ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(t.TextMuted)
				}
				if ex.Address != "" {
					ui.Text(c, ex.Address).FontSize(11.5).TextColor(t.TextMuted)
				}

				if !p.Applied && p.Status != "complete" {
					if p.Status == "enrich" {
						prompt := a.tr(
							"Kontakt existiert in Thunderbird — fehlende Felder ergänzen: ",
							"Contact exists in Thunderbird — fill missing fields: ",
						) + strings.Join(p.MissingFields, ", ") + "?"
						ui.Text(c, prompt).FontSize(12).Bold().TextColor(t.Warning)
					} else {
						ui.Text(c, a.tr(
							"Neuer Kontakt im Footer erkannt — in dein Thunderbird-Adressbuch importieren?",
							"New contact detected in footer — import into your Thunderbird address book?",
						)).FontSize(12).Bold().TextColor(t.Accent)
					}

					if a.readOnly {
						ui.Text(c, a.tr(
							"Read-Only-Modus aktiv: Importieren/Vervollständigen gesperrt.",
							"Read-only mode active: import and field updates are disabled.",
						)).FontSize(11.5).TextColor(t.Warning)
					} else {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							if p.Status == "enrich" {
								btn := iconLabelBtn(c.Key(fmt.Sprintf("ct-apply-%d", idx)), iconCheck, a.tr("Daten vervollständigen", "Complete Missing Fields"), true, a.tr("Nur fehlende Felder im bestehenden Thunderbird-Kontakt ergänzen", "Only add missing fields to existing Thunderbird contact"))
								if a.contactsBusy {
									btn.Disabled(true)
								}
								if btn.Clicked() {
									a.ApplyContactProposal(context.Background(), idx)
								}
							} else {
								btn := iconLabelBtn(c.Key(fmt.Sprintf("ct-apply-%d", idx)), iconUserPlus, a.tr("Kontakt importieren", "Import Contact"), true, a.tr("Neuen Kontakt in Thunderbird anlegen", "Create new contact in Thunderbird"))
								if a.contactsBusy {
									btn.Disabled(true)
								}
								if btn.Clicked() {
									a.ApplyContactProposal(context.Background(), idx)
								}
							}
							if ui.Button(c.Key(fmt.Sprintf("ct-skip-%d", idx)), a.tr("Ignorieren", "Dismiss")).Clicked() {
								a.DismissContactProposal(idx)
							}
						})
					}
				}
			})
		}
	})
}

func (a *App) renderQuickRuleModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.quickRuleOpen, func() {
		preview := a.QuickRulePreview()
		ui.Column(c).Width(740).MaxHeight(760).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconFilter).Size(18, 18).TextColor(t.Accent)
				ui.Text(c, a.tr("Quick-Regel-Assistent · Thunderbird-Filter", "Quick-Rule Wizard · Thunderbird Filter")).FontSize(16).Bold().Grow(1)
				pillBadge(c, a.tr("Vorschlag & Preview: 0 Tokens", "Draft & Preview: 0 Tokens"), withAlpha(t.Success, 0.18), t.Success)
				if ui.Button(c.Key("qr-x"), "✕").Clicked() {
					a.quickRuleOpen = false
				}
			})

			// Context (account scope) & optional AI refinement bar
			markedCount := len(a.quickRuleMarked)
			ui.Row(c).FillWidth().Padding(8, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, a.tr("Konto:", "Account:")).FontSize(12).TextColor(t.TextMuted)
				if len(a.quickRuleAccountIDs) > 1 {
					labels := make([]string, 0, len(a.quickRuleAccountIDs))
					for _, id := range a.quickRuleAccountIDs {
						labels = append(labels, a.accountDisplayName(id))
					}
					if ui.Select(c.Key("qr-acc"), &a.quickRuleAccountLabel, labels).Width(190).Changed() {
						a.applyQuickRuleAccountChoice(a.quickRuleAccountLabel)
					}
				} else {
					ui.Text(c, a.quickRuleAccountLabelText()).FontSize(12).Bold()
				}
				pillBadge(c, a.trf("%d markierte Mail(s)", "%d selected email(s)", markedCount), withAlpha(t.Accent, 0.16), t.Accent)
				if n := a.quickRuleExcludedCount(); n > 0 {
					pillBadge(c, a.trf("%d aus anderen Konten ignoriert", "%d from other accounts ignored", n), withAlpha(t.Warning, 0.18), t.Warning)
				}
				if a.readOnly {
					pillBadge(c, a.tr("Read-Only aktiv", "Read-Only Active"), withAlpha(t.Warning, 0.20), t.Warning)
				}
				ui.Spacer(c)
				aiLbl := a.tr("Mit KI verfeinern", "Refine with AI")
				if a.quickRuleAILoading {
					aiLbl = a.tr("Analysiere…", "Analyzing…")
				}
				if iconLabelBtn(c.Key("qr-ai"), iconSparkles, aiLbl, false, a.tr("Muster optional per KI verfeinern (verbraucht KI-Tokens)", "Optionally refine patterns with AI (uses AI tokens)")).Clicked() && !a.quickRuleAILoading {
					a.RefineQuickRuleWithAI(context.Background())
				}
			})

			ui.Scroll(c.Key("qr-scroll")).TrackScroll(&a.widgets.quickRuleScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					// Rule Name
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Regelname", "Rule Name")).Width(130).FontSize(12).TextColor(t.TextMuted)
						ui.TextInput(c.Key("qr-name"), &a.quickRuleName).Placeholder(a.tr("z. B. Auto-Filter: Newsletter", "e.g. Auto filter: Newsletter")).Grow(1)
					})

					// Match mode toggle (AND vs OR)
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Verknüpfung", "Match Logic")).Width(130).FontSize(12).TextColor(t.TextMuted)
						orBtn := ui.Button(c.Key("qr-or"), a.tr("Mindestens eine Bedingung (ODER)", "Match ANY condition (OR)"))
						if !a.quickRuleMatchAll {
							orBtn.Background(withAlpha(t.Accent, 0.18)).Border(1, t.Accent)
						}
						if orBtn.Clicked() {
							a.quickRuleMatchAll = false
						}
						andBtn := ui.Button(c.Key("qr-and"), a.tr("Alle Bedingungen erfüllen (UND)", "Match ALL conditions (AND)"))
						if a.quickRuleMatchAll {
							andBtn.Background(withAlpha(t.Accent, 0.18)).Border(1, t.Accent)
						}
						if andBtn.Clicked() {
							a.quickRuleMatchAll = true
						}
					})

					// Editable conditions
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Absender enthält", "From contains")).Width(130).FontSize(12).TextColor(t.TextMuted)
						ui.TextInput(c.Key("qr-from"), &a.quickRuleFromText).Placeholder(a.tr("z. B. newsletter@shop.de, @domain.de (kommagetrennt)", "e.g. newsletter@shop.de, @domain.de (comma-separated)")).Grow(1)
					})
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Betreff enthält", "Subject contains")).Width(130).FontSize(12).TextColor(t.TextMuted)
						ui.TextInput(c.Key("qr-subj"), &a.quickRuleSubjectText).Placeholder(a.tr("optional, z. B. [Alert], Wochenangebot (kommagetrennt)", "optional, e.g. [Alert], Weekly Deals (comma-separated)")).Grow(1)
					})

					// Action & optional folder
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Text(c, a.tr("Aktion", "Action")).Width(130).FontSize(12).TextColor(t.TextMuted)
						actSel := ui.Select(c.Key("qr-act"), &a.quickRuleActionLabel, quickRuleActionLabelsLang(a.uiLang())).Width(280)
						if actSel.Changed() {
							a.applyQuickRuleActionChoice(a.quickRuleActionLabel)
						}
						if a.quickRuleAction == "move" {
							folders := a.quickRuleAccountFolderPaths()
							if len(folders) > 0 {
								if a.quickRuleFolderPath == "" {
									a.quickRuleFolderPath = folders[0]
								}
								ui.Select(c.Key("qr-folder"), &a.quickRuleFolderPath, folders).Grow(1)
							} else {
								ui.TextInput(c.Key("qr-folder-in"), &a.quickRuleFolderPath).Placeholder(a.tr("Ordnerpfad, z. B. Archive/News", "Folder path, e.g. Archive/News")).Grow(1)
							}
						}
					})

					if a.quickRuleInfo != "" {
						ui.Row(c).FillWidth().Padding(8, 10).Radius(8).Background(withAlpha(t.Accent, 0.12)).Border(1, withAlpha(t.Accent, 0.35)).Children(func() {
							ui.Text(c, a.quickRuleInfo).FontSize(11.5)
						})
					}

					// Live Preview BEFORE saving
					ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
							ui.Text(c, a.tr("Live-Preview vor dem Speichern", "Live Preview Before Saving")).FontSize(13).Bold().Grow(1)
							markColor := t.Success
							if preview.MarkedTotal > 0 && preview.MarkedMatched < preview.MarkedTotal {
								markColor = t.Warning
							}
							if preview.MarkedMatched == 0 {
								markColor = t.Danger
							}
							pillBadge(c, a.trf("Markiert: %d/%d", "Selected: %d/%d", preview.MarkedMatched, preview.MarkedTotal), withAlpha(markColor, 0.18), markColor)
							extraClr := t.TextMuted
							extraBg := t.Surface
							if preview.ExtraTotal > 0 {
								extraClr = t.Warning
								extraBg = withAlpha(t.Warning, 0.18)
							}
							pillBadge(c, a.trf("+%d weitere Treffer im Posteingang", "+%d more inbox matches", preview.ExtraTotal), extraBg, extraClr)
						})

						for i, w := range preview.Warnings {
							clr := t.Warning
							if w.Broad && preview.Destructive {
								clr = t.Danger
							}
							ui.Row(c.Key(fmt.Sprintf("qr-warn-%d", i))).FillWidth().Padding(6, 10).Radius(6).Background(withAlpha(clr, 0.14)).Border(1, withAlpha(clr, 0.45)).Children(func() {
								ui.Text(c, a.tr(w.DE, w.EN)).FontSize(11.5).TextColor(clr)
							})
						}

						if len(preview.MarkedItems) > 0 {
							ui.Text(c, a.tr("Markierte Mails:", "Selected emails:")).FontSize(11.5).Bold().TextColor(t.TextMuted)
							maxShow := min(len(preview.MarkedItems), 5)
							for i := 0; i < maxShow; i++ {
								it := preview.MarkedItems[i]
								badge := "✓"
								clr := t.Success
								if !it.Matched {
									badge = "✕"
									clr = t.Danger
								}
								ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, badge).FontSize(11.5).Bold().TextColor(clr).Shrink(0)
									ui.Text(c, truncateText(shortSender(it.From), 24)).FontSize(11.5).Bold().Shrink(0)
									ui.Text(c, truncateText(it.Subject, 48)).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Grow(1)
								})
							}
							if len(preview.MarkedItems) > maxShow {
								ui.Text(c, a.trf("… und %d weitere markierte Mails", "… and %d more selected emails", len(preview.MarkedItems)-maxShow)).FontSize(11).TextColor(t.TextMuted)
							}
						}

						if len(preview.ExtraMatches) > 0 {
							ui.Text(c, a.tr("Weitere passende Mails im Posteingang dieses Kontos (aktuelle Ansicht):", "Other matching inbox emails of this account (current view):")).FontSize(11.5).Bold().TextColor(t.TextMuted)
							maxEx := min(len(preview.ExtraMatches), 5)
							for i := 0; i < maxEx; i++ {
								it := preview.ExtraMatches[i]
								ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, "•").FontSize(11.5).Bold().TextColor(t.Warning).Shrink(0)
									ui.Text(c, truncateText(shortSender(it.From), 24)).FontSize(11.5).Bold().Shrink(0)
									ui.Text(c, truncateText(it.Subject, 48)).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Grow(1)
								})
							}
							if len(preview.ExtraMatches) > maxEx {
								ui.Text(c, a.trf("… und %d weitere Treffer", "… and %d more matches", len(preview.ExtraMatches)-maxEx)).FontSize(11).TextColor(t.TextMuted)
							}
							ui.Checkbox(c.Key("qr-apply-all"), &a.quickRuleApplyPreview, a.trf(
								"Aktion jetzt auch sofort auf die %d weiteren Preview-Treffer anwenden",
								"Also apply action immediately to the %d additional preview matches",
								len(preview.ExtraMatches),
							))
						} else if preview.MarkedTotal > 0 && preview.MarkedMatched == preview.MarkedTotal {
							ui.Text(c, a.tr("Keine unerwarteten Beifänge im Posteingang dieses Kontos gefunden.", "No unintended extra matches in this account's inbox.")).FontSize(11.5).TextColor(t.Success)
						}
					})

					// Maintenance notice: Thunderbird manages rules directly
					ui.Row(c).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
						ui.Text(c, a.tr(
							"Hinweis: Die Regel wird als echter Thunderbird-Filter (ans Ende der Filterliste) gespeichert und sofort nur auf die Mails angewendet, die sie tatsächlich trifft. Die Pflege, Bearbeitung und Sortierung aller Regeln erfolgt anschließend direkt in Thunderbird (Extras → Filter).",
							"Note: The rule is saved as a native Thunderbird filter (appended to the filter list) and applied right away only to the emails it actually matches. Ongoing maintenance, editing, and ordering of all rules happens directly in Thunderbird (Tools → Message Filters).",
						)).FontSize(11.5).TextColor(t.TextMuted)
					})
				})
			})

			blockReason := a.quickRuleSaveBlockReason(preview)
			errText := a.quickRuleError
			if errText == "" && blockReason != "" && preview.MarkedTotal > 0 {
				errText = blockReason
			}
			if errText != "" {
				ui.Text(c, errText).FontSize(12).TextColor(t.Danger)
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if iconLabelBtn(c.Key("qr-open-tb"), iconExternal, a.tr("Filter in TB öffnen", "Open TB Filters"), false, a.tr("Regelverwaltung direkt in Thunderbird öffnen", "Open Message Filters directly in Thunderbird")).Clicked() {
					a.OpenThunderbirdFilters(context.Background())
				}
				ui.Spacer(c)
				if ui.Button(c.Key("qr-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
					a.quickRuleOpen = false
				}
				saveLbl := a.tr("TB-Filter speichern & anwenden", "Save TB Filter & Apply")
				armed := preview.RequiresConfirm && a.quickRuleConfirmArmed()
				switch {
				case a.quickRuleBusy:
					saveLbl = a.tr("Wird gespeichert…", "Saving…")
				case armed:
					saveLbl = a.tr("Breite Regel bestätigen & speichern", "Confirm broad rule & save")
				}
				save := ui.PrimaryButton(c.Key("qr-save"), saveLbl)
				if armed {
					save.Background(t.Danger)
				}
				if blockReason != "" || a.quickRuleBusy {
					save.Disabled(true)
				}
				if save.Clicked() && !a.quickRuleBusy && blockReason == "" {
					a.SaveAndApplyQuickRule(context.Background())
				}
			})
		})
	})
}

// renderCalDeleteModal asks before deleting a calendar event; recurring events offer
// "only this occurrence" vs "whole series".
func (a *App) renderCalDeleteModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.calDelOpen, func() {
		ev := a.calDelEvent
		recurring := ev.IsRecurring || ev.RecurrenceID != ""
		ui.Column(c).Width(440).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconTrash).Size(16, 16).TextColor(t.Danger)
				ui.Text(c, a.tr("Termin löschen?", "Delete event?")).FontSize(15).Bold().Grow(1)
			})
			ui.Text(c, ev.Title).FontSize(13).Bold()
			meta := formatDateTimeLang(ev.Start, a.uiLang())
			if ev.AllDay {
				meta = eventDayKey(ev.Start) + " · " + a.tr("ganztägig", "all day")
			}
			if ev.Calendar != "" {
				meta += " · " + ev.Calendar
			}
			ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
			if recurring {
				ui.Text(c, a.tr(
					"Dieser Termin gehört zu einer Serie. Soll nur dieses Vorkommen oder die ganze Serie gelöscht werden?",
					"This event is part of a series. Delete only this occurrence or the whole series?",
				)).FontSize(12)
			}
			if a.calDelError != "" {
				ui.Text(c, a.calDelError).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if a.calDelBusy {
					ui.Spinner(c)
				}
				ui.Spacer(c)
				cancel := ui.Button(c.Key("cd-cancel"), a.tr("Abbrechen", "Cancel"))
				if a.calDelBusy {
					cancel.Disabled(true)
				}
				if cancel.Clicked() && !a.calDelBusy {
					a.calDelOpen = false
				}
				if recurring {
					occ := ui.Button(c.Key("cd-occ"), a.tr("Nur dieses Vorkommen", "Only this occurrence"))
					if a.calDelBusy || ev.RecurrenceID == "" {
						occ.Disabled(true)
					}
					if occ.Clicked() && !a.calDelBusy && ev.RecurrenceID != "" {
						a.ConfirmDeleteCalendarEvent(context.Background(), bridgeclient.ScopeOccurrence)
					}
					series := ui.PrimaryButton(c.Key("cd-series"), a.tr("Ganze Serie löschen", "Delete whole series")).Background(t.Danger)
					if a.calDelBusy {
						series.Disabled(true)
					}
					if series.Clicked() && !a.calDelBusy {
						a.ConfirmDeleteCalendarEvent(context.Background(), bridgeclient.ScopeSeries)
					}
				} else {
					del := ui.PrimaryButton(c.Key("cd-del"), a.tr("Löschen", "Delete")).Background(t.Danger)
					if a.calDelBusy {
						del.Disabled(true)
					}
					if del.Clicked() && !a.calDelBusy {
						a.ConfirmDeleteCalendarEvent(context.Background(), "")
					}
				}
			})
		})
	})
}

func (a *App) renderHelpModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.helpOpen, func() {
		ui.Column(c).Width(720).MaxHeight(760).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconHelp).Size(18, 18).TextColor(t.Accent)
				ui.Text(c, a.tr("Hilfe · Überblick, TB-Filter & KI-Transparenz", "Help · Overview, TB Filters & AI Transparency")).FontSize(16).Bold().Grow(1)
				if ui.Button(c.Key("help-x"), "✕").Clicked() {
					a.helpOpen = false
				}
			})

			// Current configuration status bar
			backendInfo := a.agentBackend
			if a.agentModel != "" {
				backendInfo += " · " + a.agentModel
			}
			isLocalModel := a.agentBackend == "ollama" || a.agentBackend == "llamacpp" || a.agentBackend == "llamafile"
			tokenBadge := a.tr("Cloud/CLI Tokens bei KI-Aktion", "Cloud/CLI Tokens on AI Action")
			badgeBg, badgeFg := withAlpha(t.Accent, 0.16), t.Accent
			if isLocalModel {
				tokenBadge = a.tr("Lokal (0 Cloud-Tokens)", "Local (0 Cloud Tokens)")
				badgeBg, badgeFg = withAlpha(t.Success, 0.18), t.Success
			}
			ui.Row(c).FillWidth().Padding(8, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Aktives KI-Backend:", "Active AI Backend:")).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, backendInfo).FontSize(12).Bold()
				ui.Spacer(c)
				pillBadge(c, tokenBadge, badgeBg, badgeFg)
				if a.readOnly {
					pillBadge(c, a.tr("Read-Only aktiv", "Read-Only Active"), withAlpha(t.Warning, 0.20), t.Warning)
				}
			})

			// Inhaltsverzeichnis / Interactive Table of Contents
			if a.helpTopic == "" {
				a.helpTopic = "all"
			}
			tocItems := []struct {
				id    string
				label string
			}{
				{"all", a.tr("Alle Kapitel", "All Chapters")},
				{"overview", a.tr("Überblick", "Overview")},
				{"local", a.tr("1. 100% Lokal", "1. 100% Local")},
				{"ai", a.tr("2. KI-Aktionen", "2. AI Actions")},
				{"tokens", a.tr("3. Tokens", "3. Tokens")},
				{"rules", a.tr("4. TB-Filter & Regeln", "4. TB Filters & Rules")},
				{"contacts", a.tr("5. Kontakte", "5. Contacts")},
				{"shortcuts", a.tr("6. Kürzel", "6. Shortcuts")},
			}
			ui.Row(c.Key("help-toc")).FillWidth().Gap(5).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Inhalt:", "Contents:")).FontSize(11.5).Bold().TextColor(t.TextMuted)
				for _, item := range tocItems {
					active := a.helpTopic == item.id
					btn := ui.Button(c.Key("toc-"+item.id), item.label).Padding(4, 8)
					if active {
						btn.Background(withAlpha(t.Accent, 0.18)).Border(1, t.Accent)
					}
					if btn.Clicked() {
						a.helpTopic = item.id
						a.widgets.helpScroll.Y = 0
					}
				}
			})

			show := func(topic string) bool {
				return a.helpTopic == "all" || a.helpTopic == topic
			}

			ui.Scroll(c.Key("help-scroll")).TrackScroll(&a.widgets.helpScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					if show("overview") {
						helpSection(c, iconBookOpen, t.Accent,
							a.tr("Was OmaThunder macht · Überblick", "What OmaThunder Does · Overview"),
							[]string{
								a.tr("• OmaThunder ist dein natives Cockpit direkt neben Thunderbird: Fokus-Posteingang, Kalender, Desktop-Meldungen und optionale KI-Hilfe in einer schnellen macOS-App.", "• OmaThunder is your native command center alongside Thunderbird: Focus inbox, calendar, desktop notifications, and optional AI assistance in one fast macOS app."),
								a.tr("• Aufräumen statt manuell löschen: Wiederkehrende Mails per Multi-Select bündeln, mit dem Quick-Regel-Assistenten inklusive Preview prüfen und als native Thunderbird-Regel automatisieren.", "• Clean up instead of daily manual deletes: batch-select recurring emails, preview matches with the Quick-Rule Wizard, and automate them as native Thunderbird rules."),
								a.tr("• KI bleibt strikt opt-in: Solange du keinen KI-Button klickst, läuft die App lokal über Thunderbird ohne Token-Verbrauch.", "• AI stays strictly opt-in: until you trigger an AI action, everything runs locally via Thunderbird with zero token usage."),
							},
						)
					}

					if show("local") {
						helpSection(c, iconShieldChk, t.Success,
							a.tr("1. 100% Lokal · Keine KI, 0 Tokens", "1. 100% Local · No AI, 0 Tokens"),
							[]string{
								a.tr("• Postfach-Sync, Ordner-Navigation, Suche (⌘K), Multi-Select und Lesen laufen direkt über Thunderbird.", "• Mailbox sync, folder navigation, search (⌘K), multi-select, and reading run directly via Thunderbird."),
								a.tr("• Mail-Aktionen (Gelesen/Ungelesen, Stern, Papierkorb, Entwurf öffnen/speichern) nutzen keine KI.", "• Mail actions (mark read/unread, star, trash, save/open draft) do not use AI."),
								a.tr("• Quick-Regel-Assistent & Live-Preview prüfen lokale Absender-/Betreff-Muster komplett ohne KI-Tokens.", "• Quick-Rule Wizard & live preview evaluate sender/subject patterns completely locally without AI tokens."),
								a.tr("• Kalender-Anzeige, Termin-Erstellung, native Meldungen und Adressbuch-Abgleich kosten 0 Tokens.", "• Calendar views, event creation, native notifications, and address-book matching cost 0 tokens."),
							},
						)
					}

					if show("ai") {
						helpSection(c, iconSparkles, t.Accent,
							a.tr("2. Wann KI & Tokens verwendet werden (Explizite Aktionen)", "2. When AI & Tokens Are Used (User-Triggered Actions)"),
							[]string{
								a.tr("• Inspector-Buttons: „Zusammenfassen“, „Entwurf“, „Termin“ und „Kontakte“ starten erst nach deinem Klick und senden nur den benötigten Mail-Kontext.", "• Inspector buttons: “Summarize”, “Draft”, “Event”, and “Contacts” only run when clicked and send only the required email context."),
								a.tr("• Quick-Regel optional: Nur ein Klick auf „Mit KI verfeinern“ im Assistenten nutzt das Modell für komplexe Betreff-Muster.", "• Quick Rule optional: only clicking “Refine with AI” inside the wizard uses the model for complex patterns."),
								a.tr("• KI-Assistent & Briefing: „Tagesbriefing“ und jede Chat-Nachricht im Assistent-Panel nutzen das gewählte Modell; Mail- und Kalenderinhalte werden dabei als Daten an den Anbieter gesendet (bei Cloud-Modellen).", "• AI Assistant & Briefing: “Briefing” and each chat prompt use your selected model; mail and calendar content is sent to that provider as data (for cloud models)."),
								a.tr("• Schreibende Aktionen des Assistenten (Entwurf speichern, Termin anlegen, Gelesen-Status) werden nur als Vorschlag mit allen Details angezeigt und erst nach Klick auf „Ausführen“ in Thunderbird umgesetzt. Mails sendet der Assistent nie.", "• Write actions of the assistant (save draft, create event, read status) are shown as a proposal with all details and only run in Thunderbird after you click “Execute”. The assistant never sends emails."),
								a.tr("• Optionale Stage-1-Triage: Wird nur bei einem Triage-Scan ausgeführt, wenn ein Klassifikator aktiv ist und keine Stage-0-Regel greift.", "• Optional Stage-1 triage: runs only during a triage scan when a classifier is enabled and no Stage-0 rule matched."),
							},
						)
					}

					if show("tokens") {
						helpSection(c, iconCpu, t.Warning,
							a.tr("3. Wo Tokens anfallen — Lokal vs. Cloud vs. CLI", "3. Where Tokens Apply — Local vs. Cloud vs. CLI"),
							[]string{
								a.tr("• Cloud-APIs (anthropic, openai, gemini, mistral, deepseek, groq): Verbrauchen bezahlte API-Tokens beim jeweiligen Anbieter.", "• Cloud APIs (anthropic, openai, gemini, mistral, deepseek, groq): Consume paid API tokens with that provider."),
								a.tr("• Lokale Modelle (ollama, llamacpp, llamafile): Laufen komplett auf deinem Mac — keine Cloud-Übertragung, 0 Token-Kosten.", "• Local models (ollama, llamacpp, llamafile): Run entirely on your machine — no cloud traffic, 0 token cost."),
								a.tr("• CLI-Agenten (claude-cli, gemini-cli, opencode-cli): Nutzen dein lokal installiertes CLI-Konto bzw. Abo.", "• CLI agents (claude-cli, gemini-cli, opencode-cli): Use your locally installed CLI account or subscription."),
							},
						)
					}

					if show("rules") {
						ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
								ui.Icon(c, iconFilter).Size(15, 15).TextColor(t.Accent)
								ui.Text(c, a.tr("4. Quick-Regeln & Thunderbird-Filter (Pflege in TB)", "4. Quick Rules & Thunderbird Filters (Managed in TB)")).FontSize(13).Bold().Grow(1)
								if iconLabelBtn(c.Key("help-open-tb-filters"), iconExternal, a.tr("Filter in TB öffnen", "Open TB Filters"), false, a.tr("Filterverwaltung direkt in Thunderbird öffnen", "Open Message Filters in Thunderbird")).Clicked() {
									a.OpenThunderbirdFilters(context.Background())
								}
							})
							for _, line := range []string{
								a.tr("• Workflow: Mehrere ähnliche Mails per Checkbox (oder ⌘-Klick / Shift-Klick) markieren → „Quick-Regel“ klicken → Live-Preview prüfen → speichern & sofort anwenden.", "• Workflow: Check multiple similar emails (or ⌘-click / Shift-click) → click “Quick Rule” → review live preview → save & apply immediately."),
								a.tr("• Sichere Voreinstellung: Standardaktion ist „In Papierkorb verschieben“, damit versehentliche Treffer wiederherstellbar bleiben. Angewendet wird nur auf Mails, die die Regel tatsächlich trifft; breite Regeln (ganze Domain, Freemail-Anbieter, nur ein Absender) brauchen eine zweite Bestätigung.", "• Safe default: Default action is “Move to Trash” so any unexpected match remains recoverable. The action only applies to emails the rule actually matches; broad rules (whole domain, free-mail provider, sender only) need a second confirmation."),
								a.tr("• Wichtig — Pflege aller Regeln direkt in Thunderbird: OmaThunder kapselt nur die Erstellung. Die laufende Pflege, Bearbeitung, Reihenfolge, Aktivierung oder Löschung aller Regeln erfolgt direkt in Thunderbird unter Extras → Filter.", "• Important — Manage all rules directly in Thunderbird: OmaThunder encapsulates rule creation only. Ongoing editing, reordering, toggling, or deleting of all rules takes place directly in Thunderbird under Tools → Message Filters."),
							} {
								ui.Text(c, line).FontSize(12).TextColor(t.TextMuted)
							}
						})
					}

					if show("contacts") {
						helpSection(c, iconUserPlus, t.Accent,
							a.tr("5. Kontakte automatisch aus E-Mail-Footer erkennen", "5. Extract Contacts from Email Signatures"),
							[]string{
								a.tr("• Klicke in einer E-Mail auf „Kontakte“: Die KI liest Name, E-Mail, Telefon/Mobil, Firma, Position, Website und Adresse aus der Signatur.", "• Click “Contacts” on an email: AI extracts name, email, phone/mobile, company, job title, website, and address from the footer."),
								a.tr("• Danach prüft OmaThunder lokal (0 Tokens) gegen dein Thunderbird-Adressbuch und fragt explizit nach:", "• OmaThunder then checks your Thunderbird address books locally (0 tokens) and prompts before saving:"),
								a.tr("  – Neuer Kontakt: Per Klick auf „Kontakt importieren“ im gewählten Adressbuch anlegen.", "  – New contact: Click “Import Contact” to add it to the chosen address book."),
								a.tr("  – Bestehender Kontakt: Bei Lücken erscheint „Daten vervollständigen“ — vorhandene Felder bleiben unverändert geschützt.", "  – Existing contact: When fields are missing, click “Complete Missing Fields” — existing data stays untouched."),
							},
						)
					}

					if show("shortcuts") {
						a.renderShortcutHelp(c)
					}
				})
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr(
					"Datenschutz: Schreibaktionen, Assistent-Vorschläge & Regeln wirken in Thunderbird erst nach deinem Klick.",
					"Privacy: write actions, assistant proposals and filters only take effect in Thunderbird after your explicit click.",
				)).FontSize(11.5).TextColor(t.TextMuted).Grow(1)
				if ui.PrimaryButton(c.Key("help-close"), a.tr("Verstanden", "Got it")).Clicked() {
					a.helpOpen = false
				}
			})
		})
	})
}

func helpSection(c *ui.Context, ic *ui.SVG, clr ui.Color, title string, lines []string) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, ic).Size(15, 15).TextColor(clr)
			ui.Text(c, title).FontSize(13).Bold()
		})
		for _, line := range lines {
			ui.Text(c, line).FontSize(12).TextColor(t.TextMuted)
		}
	})
}
