package nativeui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/mailengine"
)

// RenderMain builds the entire native GPU-accelerated MailDiver UI.
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

	a.winW, a.winH = c.Size()
	c.Root().Background(t.Background)
	if a.isPhone() {
		a.renderPhone(c)
		a.renderDialogs(c)
		return
	}

	a.handleShortcuts(c)

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

	if !a.detachedAuxWindows {
		a.guardComposeClose()
		a.renderComposeModal(c)
		a.guardComposeClose()
	}
	a.renderDialogs(c)
}

// renderDialogs shows the dialogs over the main window (desktop and phone).
func (a *App) renderDialogs(c *ui.Context) {
	a.renderEventModal(c)
	a.renderEventDetailModal(c)
	a.renderQuickRuleModal(c)
	a.renderCalDeleteModal(c)
	a.renderMovePicker(c)
	a.renderArchiveModal(c)
	a.renderSettingsModal(c)
	a.renderDirectModal(c)
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
		ui.Row(c).FillWidth().Padding(10, 10, 4, 10).Children(func() {
			if iconLabelBtn(c.Key("sb-compose"), iconCompose, a.tr("Neue E-Mail", "New Email"), true, a.tr("Neue E-Mail verfassen (c / ⌘N)", "Compose new email (c / ⌘N)")).FillWidth().Clicked() {
				a.OpenComposeNew()
			}
		})
		sb := ui.Sidebar(c, &a.navID, func() {
			ui.SidebarSection(c, a.tr("Arbeitsbereich", "Workspace"), &a.secViewsOpen, func() {
				ui.SidebarItem(c, "focus", iconFocus, a.tr("Fokus (Ungelesen & ★)", "Focus (Unread & ★)")).Children(func() {
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
				if a.isDirectMode() {
					ui.SidebarItem(c, "contacts", iconUsers, a.tr("Kontakte", "Contacts")).Children(func() {
						if len(a.contacts.list) > 0 {
							ui.Badge(c, fmt.Sprintf("%d", len(a.contacts.list)))
						}
					})
				}
			})

			a.renderFolderSections(c)
		}).Grow(1).MinHeight(0).FillWidth()

		if sb.Changed() {
			a.selectedID = ""
			a.selectedMsg = nil
			a.syncPreviewForMessage(nil)
			a.setNav(a.navID)
			switch a.navID {
			case "calendar":
				// Calendar events are loaded via RefreshAll.
			case "contacts":
				a.LoadContacts(context.Background())
			default:
				a.LoadMessages(context.Background())
			}
		}

		// Compact "Today" mini-agenda at bottom of sidebar
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
	switch a.navID {
	case "calendar":
		a.renderCalendarWorkspace(c)
	case "contacts":
		a.renderContactsWorkspace(c)
	default:
		a.renderMailWorkspace(c)
	}
}

func (a *App) currentTrashAccountID() string {
	if !strings.HasPrefix(a.navID, "folder:") {
		return ""
	}
	fid := strings.TrimPrefix(a.navID, "folder:")
	for _, f := range a.folders {
		if f.ID == fid && strings.EqualFold(f.Type, "trash") {
			return f.AccountID
		}
	}
	return ""
}

func (a *App) renderMailWorkspace(c *ui.Context) {
	t := c.Theme()
	ui.Split(c, &a.mailSplit, func() {
		// Message list column
		ui.Column(c).Fill().Background(t.Background).Children(func() {
			ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				title := a.currentViewTitle()
				ui.Text(c, title).FontSize(13).Bold().Grow(1).SingleLine()
				if !a.readOnly && a.engineReady && (a.navID == "focus" || strings.HasPrefix(a.navID, "folder:")) {
					hasUnread := a.navID == "focus" && a.unreadCount() > 0
					if !hasUnread {
						for _, m := range a.messages {
							if m.Unread {
								hasUnread = true
								break
							}
						}
					}
					if !hasUnread && strings.HasPrefix(a.navID, "folder:") {
						fid := strings.TrimPrefix(a.navID, "folder:")
						for _, f := range a.folders {
							if f.ID == fid && f.UnreadCount > 0 {
								hasUnread = true
								break
							}
						}
					}
					if hasUnread {
						if iconBtn(c.Key("list-mark-all-read"), iconMailOpen, a.tr("Alle in dieser Ansicht als gelesen markieren", "Mark all in this view as read"), t.Accent, false).Clicked() {
							a.MarkCurrentViewAllRead(context.Background())
						}
					}
				}
				if trashAcc := a.currentTrashAccountID(); trashAcc != "" && a.isDirectMode() && len(a.messages) > 0 && !a.readOnly {
					armed := a.direct.emptyTrashArmID == trashAcc && time.Since(a.direct.emptyTrashArmAt) < 5*time.Second
					lbl := a.tr("Papierkorb leeren", "Empty Trash")
					if armed {
						lbl = a.tr("Wirklich leeren?", "Confirm Empty?")
					}
					btn := ui.Button(c.Key("trash-empty"), lbl).Padding(3, 8)
					if armed {
						btn.Background(withAlpha(t.Danger, 0.18)).Border(1, t.Danger)
					}
					if btn.Clicked() {
						if armed {
							a.EmptyAccountTrash(context.Background(), trashAcc)
						} else {
							a.direct.emptyTrashArmID = trashAcc
							a.direct.emptyTrashArmAt = time.Now()
						}
					}
				}
				ui.Text(c, a.trf("%d Mails", "%d emails", len(a.messages))).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
			})

			// Server-side IMAP full-text search bar (searches mail bodies not yet downloaded locally).
			if q := strings.TrimSpace(a.searchQuery); q != "" && a.engineReady {
				ui.Row(c.Key("srv-search-bar")).FillWidth().Padding(6, 10).Gap(8).AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					if a.serverSearchBusy {
						ui.Spinner(c)
						ui.Text(c, a.tr("Suche auf IMAP-Server…", "Searching on IMAP server…")).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
					} else if a.serverSearchDoneKey == a.serverSearchScopeKey(q) {
						ui.Icon(c, iconSearch).Size(13, 13).TextColor(t.Success)
						ui.Text(c, a.trf("Server-Suche: %d Treffer", "Server search: %d matches", a.serverSearchCount)).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
						if ui.Button(c.Key("srv-search-again"), a.tr("Erneut", "Retry")).Padding(2, 7).Clicked() {
							a.SearchMessagesOnServer(context.Background())
						}
					} else {
						ui.Text(c, a.tr("Lokal gesucht (geladene Texte).", "Searched locally (downloaded bodies).")).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
						if iconLabelBtn(c.Key("srv-search-go"), iconSearch, a.tr("Auf Server suchen", "Search on Server"), false, a.tr("Gesamtes Postfach inkl. noch nicht geladener Mailtexte auf dem IMAP-Server durchsuchen", "Search entire mailbox including unfetched bodies on IMAP server")).Clicked() {
							a.SearchMessagesOnServer(context.Background())
						}
					}
				})
			}

			// Inline Undo bar for recent reversible mail actions (Archive, Move, Trash, Read, Star).
			if a.undo != nil && time.Since(a.undo.CreatedAt) <= undoExpiry {
				ui.Row(c.Key("undo-bar")).FillWidth().Padding(6, 10).Gap(8).AlignItems(ui.Center).Background(withAlpha(t.Accent, 0.10)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Icon(c, iconUndo).Size(13, 13).TextColor(t.Accent).Shrink(0)
					ui.Text(c, a.undo.Label).FontSize(11.5).Grow(1).MinWidth(0).SingleLine()
					undoBtn := ui.Button(c.Key("undo-do"), a.tr("Rückgängig (⌘Z)", "Undo (⌘Z)")).Padding(2, 8)
					if a.undoBusy {
						undoBtn.Disabled(true)
					}
					if undoBtn.Clicked() && !a.undoBusy {
						a.PerformUndo(context.Background())
					}
					if ui.Button(c.Key("undo-dismiss"), "✕").Padding(2, 6).Clicked() {
						a.undo = nil
					}
				})
			}

			checked := a.CheckedMessageIDs()
			if len(checked) > 0 {
				ui.Row(c.Key("bulk-bar")).FillWidth().Padding(6, 10).Gap(6).AlignItems(ui.Center).Background(withAlpha(t.Accent, 0.12)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Text(c, a.trf("%d markiert", "%d selected", len(checked))).FontSize(11.5).Bold().TextColor(t.Accent).Grow(1).SingleLine()
					if iconLabelBtn(c.Key("bulk-rule"), iconFilter, a.tr("Quick-Regel", "Quick Rule"), true, a.tr("Aus markierten Mails eine Filterregel erstellen (q · mit Preview)", "Create filter rule from selected emails (q · with preview)")).Clicked() {
						a.OpenQuickRuleWizard()
					}
					if iconBtn(c.Key("bulk-read"), iconMailOpen, a.tr("Markierte als gelesen markieren (u)", "Mark selected as read (u)"), t.Accent, false).Clicked() {
						readTrue := true
						a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Read: &readTrue}, a.tr("Als gelesen markiert", "Marked as read"))
					}
					anyUnflagged := false
					for _, cm := range a.CheckedMessages() {
						if !cm.Flagged {
							anyUnflagged = true
							break
						}
					}
					starIcon, starClr, starTip := iconStarFilled, t.Warning, a.tr("Markierung entfernen (s)", "Remove star (s)")
					if anyUnflagged {
						starIcon, starClr, starTip = iconStar, ui.Color{}, a.tr("Markierte mit Stern markieren (s)", "Star selected (s)")
					}
					if iconBtn(c.Key("bulk-star"), starIcon, starTip, starClr, !anyUnflagged).Clicked() {
						flagVal := anyUnflagged
						a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Flagged: &flagVal}, a.tr("Markierung aktualisiert", "Star updated"))
					}
					if !a.readOnly {
						if iconBtn(c.Key("bulk-archive"), iconArchive, a.tr("Markierte archivieren (e)", "Archive selected (e)"), ui.Color{}, false).Clicked() {
							a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"))
						}
						if iconBtn(c.Key("bulk-move"), iconFolderInput, a.tr("Markierte verschieben nach… (v)", "Move selected to… (v)"), ui.Color{}, false).Clicked() {
							a.OpenMovePicker()
						}
						if iconBtn(c.Key("bulk-trash"), iconTrash, a.tr("Markierte in Papierkorb verschieben (d d)", "Move selected to Trash (d d)"), t.Danger, false).Clicked() {
							a.trashMails(context.Background(), checked, "")
						}
					}
					if ui.Button(c.Key("bulk-clear"), "✕").Padding(4, 7).Tooltip(a.tr("Auswahl aufheben (esc)", "Clear selection (esc)")).Clicked() {
						a.ClearMailSelection()
					}
				})
			}

			if !a.engineReady {
				a.renderOfflineBanner(c)
				return
			}
			if a.isDirectMode() && len(a.accounts) == 0 {
				a.renderDirectOnboarding(c)
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
			outboundView := a.isCurrentViewOutbound()
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
					starClicked := false
					row := ui.Column(c.Key("mail-body-"+m.ID)).Grow(1).MinWidth(0).Padding(8, 12, 8, 6).Gap(3).Children(func() {
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							if m.Unread {
								statusDot(c, t.Accent, 6)
							}
							// Muted only on plain rows: the highlighted row brings its own text color.
							peerLabel := shortSender(m.From)
							if outboundView {
								if len(m.To) > 0 {
									peerLabel = a.tr("An: ", "To: ") + shortSender(strings.Join(m.To, ", "))
								} else {
									peerLabel = a.tr("(Kein Empfänger)", "(No recipient)")
								}
							}
							st := ui.Text(c, peerLabel).FontSize(11.5).Grow(1).MinWidth(0).SingleLine()
							if m.Unread {
								st.Bold()
							} else if !selected {
								st.TextColor(t.TextMuted)
							}
							if !a.readOnly {
								sIcon, sClr := iconStar, withAlpha(t.TextMuted, 0.45)
								sTip := a.tr("Mit Stern markieren (s)", "Star (s)")
								if m.Flagged {
									sIcon, sClr = iconStarFilled, t.Warning
									sTip = a.tr("Markierung entfernen (s)", "Remove star (s)")
								}
								sb := ui.ButtonBase(c.Key("row-star-" + m.ID)).Padding(2).Radius(4).Shrink(0).Label(sTip).Tooltip(sTip)
								sb.Children(func() { ui.Icon(c, sIcon).Size(12, 12).TextColor(sClr) })
								if sb.Clicked() {
									starClicked = true
									flagVal := !m.Flagged
									a.PerformMessageAction(context.Background(), m.ID, backend.MessageActionsRequest{Flagged: &flagVal}, a.tr("Markierung aktualisiert", "Star updated"))
								}
							} else if m.Flagged {
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
							if m.ThreadCount > 1 {
								threadBadge(c, m.ThreadCount)
							}
							if crossFolder && m.Folder != "" {
								miniTag(c, m.Folder, t.TextMuted, t.Surface)
							}
							if col := a.triageColumnFor(m.ID); col != "" {
								miniTag(c, strings.ToUpper(col), t.Accent, withAlpha(t.Accent, 0.16))
							}
						})
					})
					if row.Clicked() && !starClicked {
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
		ui.Text(c, a.tr("Mail-Engine startet…", "Starting mail engine…")).FontSize(15).Bold()
		ui.Text(c, a.tr(
			"MailDiver verbindet sich lokal mit der IMAP/SMTP-Engine. Falls ein Fehler auftritt, prüfe deine Kontoeinstellungen.",
			"MailDiver is connecting to the local IMAP/SMTP engine. If an error occurs, check your account settings.",
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
		if a.preview.msgID != msg.ID {
			a.syncPreviewForMessage(msg)
		}

		renderMailPane := func() {
			ui.Scroll(c.Key("insp-scroll")).TrackScroll(&a.widgets.inspectorScroll).Fill().Children(func() {
				ui.Column(c).FillWidth().Padding(18).Gap(14).Children(func() {
					// Header block
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						subj := msg.Subject
						if subj == "" {
							subj = a.tr("(Kein Betreff)", "(No Subject)")
						}
						ui.Text(c, subj).FontSize(18).Bold().Selectable()
						ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
							ui.Text(c, a.tr("Von: ", "From: ")+msg.From).FontSize(12).Bold().Selectable()
							ui.Text(c, "· "+formatDateTimeLang(msg.Date, a.uiLang())).FontSize(12).TextColor(t.TextMuted)
							if msg.Folder != "" {
								pillBadge(c, msg.Folder, t.Surface, t.TextMuted)
							}
						})
						if len(msg.To) > 0 {
							ui.Text(c, a.tr("An: ", "To: ")+strings.Join(msg.To, ", ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
						}
						if len(msg.CC) > 0 {
							ui.Text(c, "CC: "+strings.Join(msg.CC, ", ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
						}
					})

					// Mail actions first (the single keys are in the tooltips), the AI actions below.
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						if a.isDraftsMessage(msg) {
							if iconLabelBtn(c.Key("act-edit-draft"), iconPen, a.tr("Entwurf weiterbearbeiten", "Edit Draft"), true, a.tr("Gespeicherten Entwurf im Editor öffnen", "Open saved draft in compose editor")).Clicked() {
								a.OpenComposeFromDraft(msg)
							}
							ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 2)
						}
						if iconBtn(c.Key("act-reply-edit"), iconReply, a.tr("Antworten (r)", "Reply (r)"), ui.Color{}, false).Clicked() {
							a.OpenComposeReply(msg, a.inspectorDraft)
						}
						if iconBtn(c.Key("act-reply-all"), iconReplyAll, a.tr("Allen antworten", "Reply All"), ui.Color{}, false).Clicked() {
							a.OpenComposeReplyAll(msg, a.inspectorDraft)
						}
						if iconBtn(c.Key("act-forward"), iconForward, a.tr("Weiterleiten", "Forward"), ui.Color{}, false).Clicked() {
							a.OpenComposeForward(msg)
						}
						ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 2)
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
							a.PerformMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Read: &targetRead}, a.tr("Lesestatus aktualisiert", "Read status updated"))
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
							a.PerformMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Flagged: &targetFlag}, a.tr("Markierung aktualisiert", "Star updated"))
						}
						if !a.readOnly {
							ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 2)
							if iconBtn(c.Key("act-archive"), iconArchive, a.tr("Archivieren (e)", "Archive (e)"), ui.Color{}, false).Clicked() {
								a.performMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(msg.ID))
							}
							if iconBtn(c.Key("act-move"), iconFolderInput, a.tr("Verschieben nach… (v)", "Move to… (v)"), ui.Color{}, false).Clicked() {
								a.OpenMovePicker()
							}
							if a.isDirectMode() {
								if a.isJunkMessage(msg) {
									if iconLabelBtn(c.Key("act-not-spam"), iconCheck, a.tr("Kein Spam", "Not Spam"), false, a.tr("Aus Spam-Ordner zurück in den Posteingang verschieben", "Move out of Spam back to Inbox")).Clicked() {
										a.MarkMailNotSpam(context.Background(), msg.ID)
									}
								} else if iconBtn(c.Key("act-spam"), iconSpam, a.tr("Als Spam markieren", "Mark as Spam"), t.Warning, false).Clicked() {
									a.MarkMailSpam(context.Background(), msg.ID)
								}
							}
							if iconBtn(c.Key("act-trash"), iconTrash, a.tr("In Papierkorb verschieben (d)", "Move to Trash (d)"), t.Danger, false).Clicked() {
								a.trashMails(context.Background(), nil, msg.ID)
							}
						}
						ui.Spacer(c)
						iconBtn(c.Key("act-open-in"), iconExternal, a.tr("Unterhaltung öffnen in… (Claude, Antigravity)", "Open conversation in… (Claude, Antigravity)"), ui.Color{}, false).PopoverMenuButton(func(m *ui.Menu) {
							if m.Item(a.tr("In Claude Code (Terminal)", "In Claude Code (terminal)")).Chosen() {
								a.OpenConversationIn(context.Background(), msg, handoffClaudeCode)
							}
							if m.Item(a.tr("In Antigravity (App)", "In Antigravity (app)")).Chosen() {
								a.OpenConversationIn(context.Background(), msg, handoffAntigravity)
							}
							if m.Item(a.tr("In Antigravity-Agent (Terminal)", "In Antigravity agent (terminal)")).Chosen() {
								a.OpenConversationIn(context.Background(), msg, handoffAgy)
							}
							if m.Item(a.tr("In Claude (App, über Zwischenablage)", "In Claude (app, via clipboard)")).Chosen() {
								a.OpenConversationIn(context.Background(), msg, handoffClaudeApp)
							}
							m.Separator()
							if m.Item(a.tr("Als Markdown kopieren", "Copy as Markdown")).Chosen() {
								a.OpenConversationIn(context.Background(), msg, handoffCopy)
							}
						})
						if iconBtn(c.Key("act-print"), iconPrinter, a.tr("Mail drucken (⌘P)", "Print email (⌘P)"), ui.Color{}, false).Clicked() {
							a.PrintSelectedMessage(context.Background())
						}
						prevLbl := a.tr("Web-Vorschau", "Web Preview")
						if len(a.preview.links) > 0 && !a.preview.open {
							prevLbl = fmt.Sprintf("%s (%d)", prevLbl, len(a.preview.links))
						}
						if iconLabelBtn(c.Key("act-preview"), iconLink, prevLbl, a.preview.open, a.tr("Eingebettete Browser-Vorschau im Split-Panel ein/ausblenden", "Toggle embedded browser preview split panel")).Clicked() {
							a.ToggleWebPreview()
						}
						if iconLabelBtn(c.Key("act-rule"), iconFilter, a.tr("Regel", "Rule"), false, a.tr("Quick-Regel-Assistent öffnen (q · 0 Tokens)", "Open Quick-Rule Wizard (q · 0 tokens)")).Clicked() {
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

					a.renderICSBanner(c, msg)
					a.renderInspectorAIResult(c, msg)

					ui.Divider(c)

					// Conversation: newer mails above the open one, older ones below (newest on top).
					newerMails, olderMails := a.conversationSplit(msg)
					a.renderConversationGroup(c, "newer", a.trf("Neuere Mails in dieser Unterhaltung (%d)", "Newer emails in this conversation (%d)", len(newerMails)), newerMails)

					// Body
					a.renderMailBody(c, msg)

					a.renderAttachments(c, msg)

					a.renderConversationGroup(c, "older", a.trf("Frühere Mails in dieser Unterhaltung (%d)", "Earlier emails in this conversation (%d)", len(olderMails)), olderMails)
				})
			})
		}

		if !a.preview.open {
			renderMailPane()
			return
		}
		a.preview.splitPct = clampPreviewSplitPct(a.preview.splitPct)
		if a.preview.layout == "bottom" {
			ui.SplitVerticalPercent(c.Key("insp-prev-vsplit"), &a.preview.splitPct, renderMailPane, func() {
				a.renderWebPreviewPane(c)
			}).Fill()
		} else {
			ui.SplitPercent(c.Key("insp-prev-hsplit"), &a.preview.splitPct, renderMailPane, func() {
				a.renderWebPreviewPane(c)
			}).Fill()
		}
	})
}

// renderICSBanner shows a calendar invitation in a mail with its answers (desktop and phone).
func (a *App) renderICSBanner(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	// .ics Calendar Invitation Banner (RSVP Accept/Tentative/Decline + 1-click import, 0 AI tokens)
	if msg.HasICS {
		ui.Column(c.Key("ics-banner")).FillWidth().Padding(10, 12).Radius(8).Background(withAlpha(t.Accent, 0.12)).Border(1, withAlpha(t.Accent, 0.45)).Gap(8).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconCal).Size(18, 18).TextColor(t.Accent).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					invTitle := a.tr("Termineinladung (.ics)", "Calendar Invitation (.ics)")
					if msg.ICSInvite != nil && msg.ICSInvite.Title != "" {
						invTitle = msg.ICSInvite.Title
					}
					ui.Text(c, invTitle).FontSize(12.5).Bold().Selectable()
					if msg.ICSInvite != nil {
						var parts []string
						if msg.ICSInvite.Start != "" {
							parts = append(parts, formatDateTimeLang(msg.ICSInvite.Start, a.uiLang()))
						}
						if msg.ICSInvite.Location != "" {
							parts = append(parts, msg.ICSInvite.Location)
						}
						if msg.ICSInvite.Organizer != "" {
							parts = append(parts, a.tr("Organisator: ", "Organizer: ")+msg.ICSInvite.Organizer)
						}
						if len(parts) > 0 {
							ui.Text(c, strings.Join(parts, " · ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
						}
					}
				})
				if msg.ICSInvite != nil {
					if joinURL, joinLabel := a.eventJoinLink(*msg.ICSInvite); joinURL != "" {
						if iconLabelBtn(c.Key("ics-join"), iconLink, joinLabel, true, joinURL).Clicked() {
							_ = openExternalURL(joinURL)
						}
					}
				}
			})
			if a.isDirectMode() {
				ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					if iconLabelBtn(c.Key("ics-accept"), iconCheck, a.tr("Zusagen", "Accept"), true, a.tr("Einladung annehmen, in Kalender übernehmen & Antwort senden", "Accept invitation, add to calendar & send RSVP reply")).Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, "accepted")
					}
					if ui.Button(c.Key("ics-tentative"), a.tr("Vielleicht", "Tentative")).Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, "tentative")
					}
					if ui.Button(c.Key("ics-decline"), a.tr("Absagen", "Decline")).TextColor(t.Danger).Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, "declined")
					}
					ui.Spacer(c)
					if iconLabelBtn(c.Key("ics-adopt"), iconCalPlus, a.tr("Nur übernehmen", "Import Only"), false, a.tr("Termin ohne E-Mail-Antwort in den Kalender übernehmen", "Import event into calendar without sending RSVP")).Clicked() {
						a.ImportMailICS(context.Background(), msg.ID)
					}
				})
			}
		})
	}

}

// renderInspectorAIResult shows the result of an AI action on the open mail (desktop and phone).
func (a *App) renderInspectorAIResult(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
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
					if iconLabelBtn(c.Key("draft-reply"), iconReply, a.tr("In Antworten öffnen", "Open in Reply"), true, a.tr("Entwurf im Antworten-Dialog bearbeiten & senden", "Edit & send draft in Reply modal")).Clicked() {
						a.OpenComposeReply(msg, a.inspectorDraft)
					}
					if !a.readOnly && iconLabelBtn(c.Key("draft-save"), iconCheck, a.tr("Als Entwurf speichern", "Save Draft"), false, a.tr("Im Entwürfe-Ordner speichern", "Save to Drafts folder")).Clicked() {
						a.SaveDraft(context.Background(), backend.ComposeRequest{
							AccountID:        msg.AccountID,
							ReplyToMessageID: msg.ID,
							ReplyType:        "reply",
							Body:             a.inspectorDraft,
						}, a.tr("Entwurf im Entwürfe-Ordner gespeichert", "Draft saved to Drafts folder"))
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

}

// renderAttachments lists the open mail's attachments (desktop and phone).
func (a *App) renderAttachments(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	// Attachments (clickable download & open)
	if len(msg.Attachments) > 0 {
		ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
			ui.Text(c, a.trf("Anhänge (%d)", "Attachments (%d)", len(msg.Attachments))).FontSize(12).Bold()
			ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
				for i, att := range msg.Attachments {
					attCopy := att
					lbl := fmt.Sprintf("%s (%d KB)", attCopy.Name, max(1, attCopy.Size/1024))
					if a.isDirectMode() {
						if iconLabelBtn(c.Key(fmt.Sprintf("att-dl-%d", i)), iconDownload, lbl, false, a.tr("In ~/Downloads speichern & öffnen", "Save to ~/Downloads & open")).Clicked() {
							a.DownloadAndOpenAttachment(context.Background(), msg.ID, attCopy.PartName, attCopy.Name)
						}
					} else {
						pillBadge(c, lbl, t.Background, t.Text)
					}
				}
			})
		})
	}

}

// RenderComposeWindow renders the detached native Compose window on desktop.
func (a *App) RenderComposeWindow(c *ui.Context) {
	if a.uiTheme != nil && a.uiTheme.theme != nil {
		c.SetTheme(a.uiTheme.theme)
	}
	t := c.Theme()
	c.Root().Background(t.Surface)
	if a.composeOpen && a.composeBaseline == "" {
		a.composeBaseline = a.composeSnapshot()
	}
	if c.Shortcut(0, ui.KeyEscape) {
		a.TryCloseComposeWindow()
	}
	ui.Column(c).Fill().Padding(16).Background(t.Surface).Gap(10).Children(func() {
		a.renderComposeContent(c, true)
	})
}

func (a *App) renderComposeModal(c *ui.Context) {
	if a.detachedAuxWindows {
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.composeOpen, func() {
		ui.Column(c).Width(a.modalW(620)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			a.renderComposeContent(c, false)
		})
	})
}

func (a *App) renderComposeContent(c *ui.Context, inWindow bool) {
	t := c.Theme()
	title := a.ComposeWindowTitle()
	cmpWrap := ui.Column(c.Key("cmp-wrap")).FillWidth().Gap(8)
	if inWindow {
		cmpWrap.Fill()
	}
	if dropped := cmpWrap.DroppedFiles(); len(dropped) > 0 && !a.readOnly {
		added := 0
		for _, p := range dropped {
			before := len(a.composeAttachments)
			a.addComposeAttachment(p)
			if len(a.composeAttachments) > before {
				added++
			}
		}
		if added > 0 {
			a.toast(a.trf("%d Datei(en) als Anhang hinzugefügt", "%d file(s) attached", added), "success")
		}
	}
	if !a.readOnly && !a.isPhone() && !c.FocusTakesText() && c.Shortcut(ui.Cmd, ui.KeyV) {
		a.pasteClipboardIntoCompose(true)
	}
	cmpWrap.Children(func() {
		if !inWindow {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("cmp-x"), "✕").Tooltip(a.tr("Schließen (esc)", "Close (esc)")).Clicked() {
					a.composeOpen = false
				}
			})
		}
		if a.composeCloseAsk {
			ui.Row(c.Key("cmp-close-ask")).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).Radius(8).
				Background(withAlpha(t.Warning, 0.16)).Border(1, withAlpha(t.Warning, 0.5)).Children(func() {
				ui.Text(c, a.tr("Nicht gesendet.", "Not sent.")).FontSize(12.5).Bold().Grow(1).MinWidth(0).SingleLine()
				if !a.readOnly && ui.PrimaryButton(c.Key("cmp-ask-save"), a.tr("Als Entwurf speichern", "Save as draft")).Clicked() {
					a.SaveDraft(context.Background(), a.buildComposeReq(false), a.tr("Entwurf im Entwürfe-Ordner gespeichert", "Draft saved to Drafts folder"))
				}
				if ui.Button(c.Key("cmp-ask-discard"), a.tr("Verwerfen", "Discard")).Clicked() {
					a.closeCompose()
				}
				if ui.Button(c.Key("cmp-ask-keep"), a.tr("Weiter schreiben", "Keep writing")).Clicked() {
					a.composeCloseAsk = false
				}
			})
		}

		// Sender account / alias identity selector when multiple identities exist
		if choices := a.composeIdentityChoices(); len(choices) > 1 {
			labels := make([]string, 0, len(choices))
			choiceByLabel := make(map[string]composeIdentityChoice, len(choices))
			for _, ch := range choices {
				labels = append(labels, ch.Label)
				choiceByLabel[ch.Label] = ch
			}
			if _, ok := choiceByLabel[a.composeAccountLabel]; !ok && len(choices) > 0 {
				a.composeAccountLabel = choices[0].Label
				a.composeAccountID = choices[0].AccountID
				a.composeIdentityID = choices[0].IdentityID
				a.composeFromEmail = choices[0].Email
				a.composeFromName = choices[0].Name
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Von:", "From:")).FontSize(12).Width(42).TextColor(t.TextMuted)
				if ui.Select(c.Key("cmp-from"), &a.composeAccountLabel, labels).Grow(1).Changed() {
					oldSig := stripSignatureDelim(a.composeSignatureFor(a.composeAccountID, a.composeFromEmail))
					ch := choiceByLabel[a.composeAccountLabel]
					a.composeAccountID = ch.AccountID
					a.composeIdentityID = ch.IdentityID
					a.composeFromEmail = ch.Email
					a.composeFromName = ch.Name
					newSig := stripSignatureDelim(ch.Signature)
					if newSig == "" {
						newSig = stripSignatureDelim(a.composeSignatureFor(a.composeAccountID, a.composeFromEmail))
					}
					if oldSig != "" && strings.Contains(a.composeBody, "-- \n"+oldSig) {
						if newSig != "" {
							a.composeBody = strings.Replace(a.composeBody, "-- \n"+oldSig, "-- \n"+newSig, 1)
						} else {
							a.composeBody = strings.Replace(a.composeBody, "\n\n-- \n"+oldSig, "", 1)
							a.composeBody = strings.Replace(a.composeBody, "-- \n"+oldSig, "", 1)
						}
					} else if oldSig == "" && newSig != "" {
						a.composeBody = appendAccountSignature(a.composeBody, newSig)
					}
					if a.composeUsingWYSIWYG() && a.composeEditor.Widget() != nil {
						a.composeEditor.UpdateSignature(composeSignatureBlockHTML(newSig))
					}
				}
			})
		}

		// To field + autocomplete suggestions
		ui.TextInput(c.Key("cmp-to"), &a.composeTo).Placeholder(a.tr("An (Komma-getrennt)…", "To (comma-separated)…")).FillWidth()
		a.refreshComposeSuggestions("to", a.composeTo)
		a.renderComposeSuggestions(c, "to", a.composeSuggestTo)

		// CC & BCC row + autocomplete suggestions
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextInput(c.Key("cmp-cc"), &a.composeCC).Placeholder(a.tr("CC (optional)…", "CC (optional)…")).Grow(1)
			ui.TextInput(c.Key("cmp-bcc"), &a.composeBCC).Placeholder(a.tr("BCC (optional)…", "BCC (optional)…")).Grow(1)
		})
		a.refreshComposeSuggestions("cc", a.composeCC)
		a.renderComposeSuggestions(c, "cc", a.composeSuggestCC)
		a.refreshComposeSuggestions("bcc", a.composeBCC)
		a.renderComposeSuggestions(c, "bcc", a.composeSuggestBCC)

		ui.TextInput(c.Key("cmp-sub"), &a.composeSubject).Placeholder(a.tr("Betreff…", "Subject…")).FillWidth()
		if a.composeUsingWYSIWYG() {
			if a.composeEditor.Widget() == nil {
				_ = a.composeEditor.Ensure()
			}
			if w := a.composeEditor.Widget(); w != nil {
				if a.composeEditorLoadedGen != a.composeEditorGen {
					if err := a.composeEditor.LoadContent(a.composeBodyHTML, a.composeThemePalette(), a.uiLang()); err == nil {
						a.composeEditorLoadedGen = a.composeEditorGen
					}
				}
				wvEl := ui.WebView(c, w).FillWidth()
				if inWindow {
					wvEl.Grow(1).MinHeight(240)
				} else {
					wvEl.Height(290)
				}
				goto afterComposeEditor
			}
		}
		ui.Row(c.Key("cmp-fmt-bar")).FillWidth().Gap(4).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Format:", "Format:")).FontSize(11).TextColor(t.TextMuted)
			if ui.Button(c.Key("cmp-fmt-b"), "B").Padding(1, 7).Tooltip(a.tr("Fett (**Text**)", "Bold (**text**)")).Clicked() {
				a.composeBody = insertComposeSnippet(a.composeBody, "**fett**")
			}
			if ui.Button(c.Key("cmp-fmt-i"), "I").Padding(1, 7).Tooltip(a.tr("Kursiv (*Text*)", "Italic (*text*)")).Clicked() {
				a.composeBody = insertComposeSnippet(a.composeBody, "*kursiv*")
			}
			if ui.Button(c.Key("cmp-fmt-code"), "</>").Padding(1, 7).Tooltip(a.tr("Code (`code`)", "Code (`code`)")).Clicked() {
				a.composeBody = insertComposeSnippet(a.composeBody, "`code`")
			}
			if ui.Button(c.Key("cmp-fmt-ul"), "• Liste").Padding(1, 7).Tooltip(a.tr("Aufzählung (- Punkt)", "Bullet list (- item)")).Clicked() {
				a.composeBody = insertComposeSnippet(a.composeBody, "\n- Punkt 1\n- Punkt 2")
			}
			if ui.Button(c.Key("cmp-fmt-table"), a.tr("▦ Tabelle", "▦ Table")).Padding(1, 7).Tooltip(a.tr(
				"Tabelle einfügen: übernimmt eine kopierte Tabelle (Confluence, Excel, Webseite) mit Links, sonst eine leere Vorlage. Wird als echte Tabelle gesendet.",
				"Insert table: takes a copied table (Confluence, Excel, web page) with its links, or an empty template. Sent as a real table.")).Clicked() {
				a.insertComposeTable()
			}
		})
		{
			bodyArea := ui.TextArea(c.Key("cmp-body"), &a.composeBody).Placeholder(a.tr("Nachrichtentext (⌘↩ zum Senden)…", "Message body (⌘↩ to send)…")).FillWidth()
			switch {
			case inWindow:
				bodyArea.Grow(1).MinHeight(180)
			case a.isPhone():
				bodyArea.MinHeight(max(240, a.winH-380))
			default:
				bodyArea.Height(190)
			}
			if !a.readOnly && !a.composeBusy && (bodyArea.Shortcut(ui.Cmd, ui.KeyEnter) || c.Shortcut(ui.Cmd, ui.KeyEnter)) {
				if !a.composeConfirm {
					a.composeConfirm = true
					a.toast(a.tr("Nochmal ⌘↩ drücken zum Senden", "Press ⌘↩ again to send"), "info")
				} else {
					a.SendCompose(context.Background(), a.buildComposeReq(false))
				}
			}
		}
	afterComposeEditor:
		if a.composeForwardHTML != "" && (!a.composeUsingWYSIWYG() || a.composeEditor.Widget() == nil) {
			ui.Checkbox(c.Key("cmp-keep-html"), &a.composeKeepHTML, a.tr("Original-Formatierung (HTML) beim Weiterleiten beibehalten", "Keep original HTML formatting when forwarding"))
		}

		// Outgoing attachments bar (native file picker + clipboard paste + manual path input + carried-over draft/forward attachments)
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconPaperclip).Size(14, 14).TextColor(t.TextMuted)
				if ui.Button(c.Key("cmp-att-browse"), a.tr("Datei wählen…", "Browse…")).Clicked() {
					a.pickFile(FilePickRequest{
						Mode:  "open-multi",
						Title: a.tr("Anhänge auswählen", "Select Attachments"),
					}, func(paths []string) {
						for _, p := range paths {
							a.addComposeAttachment(p)
						}
					})
				}
				if !a.isPhone() {
					if ui.Button(c.Key("cmp-att-paste"), a.tr("Einfügen (Zwischenablage)", "Paste Clipboard")).Tooltip(a.tr("Kopierte Dateien oder Bilder als Anhang einfügen (oder Dateien hierher ziehen)", "Attach copied files or images from clipboard (or drag & drop files)")).Clicked() {
						a.pasteClipboardIntoCompose(false)
					}
				}
				attIn := ui.TextInput(c.Key("cmp-att-in"), &a.composeAttachInput).Placeholder(a.tr("Oder Pfad eingeben (z. B. ~/Downloads/datei.pdf)…", "Or type path (e.g. ~/Downloads/file.pdf)…")).Grow(1)
				addClicked := ui.Button(c.Key("cmp-att-add"), a.tr("Anhängen", "Attach")).Clicked()
				if (addClicked || attIn.Submitted()) && strings.TrimSpace(a.composeAttachInput) != "" {
					a.addComposeAttachment(a.composeAttachInput)
				}
			})
			if len(a.composeSourceAttachments) > 0 || len(a.composeAttachments) > 0 {
				removeSrcIdx := -1
				removeIdx := -1
				ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					for i, sa := range a.composeSourceAttachments {
						idx := i
						name := sa.Name
						if name == "" {
							name = "attachment"
						}
						ui.Row(c.Key(fmt.Sprintf("cmp-src-att-%d", idx))).Padding(3, 8).Radius(6).Background(t.Background).Border(1, t.Border).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconPaperclip).Size(12, 12).TextColor(t.Accent)
							ui.Text(c, name).FontSize(11.5).SingleLine()
							if ui.Button(c.Key(fmt.Sprintf("cmp-src-att-rm-%d", idx)), "✕").Padding(1, 4).Clicked() {
								removeSrcIdx = idx
							}
						})
					}
					for i, p := range a.composeAttachments {
						idx := i
						name := filepath.Base(p)
						ui.Row(c.Key(fmt.Sprintf("cmp-att-%d", idx))).Padding(3, 8).Radius(6).Background(t.Background).Border(1, t.Border).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconPaperclip).Size(12, 12).TextColor(t.Accent)
							ui.Text(c, name).FontSize(11.5).SingleLine()
							if ui.Button(c.Key(fmt.Sprintf("cmp-att-rm-%d", idx)), "✕").Padding(1, 4).Clicked() {
								removeIdx = idx
							}
						})
					}
				})
				if removeSrcIdx >= 0 {
					a.removeComposeSourceAttachment(removeSrcIdx)
				}
				if removeIdx >= 0 {
					a.removeComposeAttachment(removeIdx)
				}
			}
		})

		if a.composeReplyEncrypted {
			ui.Text(c, a.tr(
				"Hinweis: Die Original-Mail war verschlüsselt. Aktiviere unten „S/MIME verschlüsseln“, falls das Empfänger-Zertifikat vorhanden ist.",
				"Note: the original email was encrypted. Enable “S/MIME encrypt” below if the recipient certificate is available.",
			)).FontSize(11.5).TextColor(t.Warning)
		}

		if a.isDirectMode() && !a.readOnly {
			ui.Row(c.Key("cmp-smime-bar")).FillWidth().Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconLock).Size(13, 13).TextColor(t.TextMuted)
				ui.Text(c, "S/MIME:").FontSize(11.5).TextColor(t.TextMuted)
				ui.Segmented(c.Key("cmp-sign"), &a.composeSign, a.tr("Auto", "Auto"), a.tr("Signieren", "Sign"), a.tr("Ohne", "Off"))
				ui.Checkbox(c.Key("cmp-enc"), &a.composeEncrypt, a.tr("Verschlüsseln", "Encrypt"))
			})
		}

		if a.readOnly {
			ui.Text(c, a.tr("Bitte zuerst ein IMAP/SMTP-Konto verbinden, um E-Mails zu senden.", "Please connect an IMAP/SMTP account first to send emails.")).FontSize(11.5).TextColor(t.Warning)
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if inWindow {
				if ui.Button(c.Key("cmp-win-close"), a.tr("Schließen", "Close")).Clicked() {
					a.TryCloseComposeWindow()
				}
			}
			ui.Spacer(c)
			if !a.readOnly {
				if ui.Button(c.Key("cmp-draft"), a.tr("Entwurf speichern", "Save Draft")).Clicked() {
					a.SaveDraft(context.Background(), a.buildComposeReq(false), a.tr("Entwurf im Entwürfe-Ordner gespeichert", "Draft saved to Drafts folder"))
				}
				sendLabel := a.tr("Senden", "Send")
				if a.composeConfirm {
					sendLabel = a.tr("Wirklich senden?", "Confirm Send?")
				}
				if a.composeBusy {
					sendLabel = a.tr("Sende…", "Sending…")
				}
				if ui.PrimaryButton(c.Key("cmp-send"), sendLabel).Tooltip(a.tr("Senden (⌘↩)", "Send (⌘↩)")).Clicked() && !a.composeBusy {
					if !a.composeConfirm {
						a.composeConfirm = true
					} else {
						a.SendCompose(context.Background(), a.buildComposeReq(false))
					}
				}
			}
		})
	})
}

func (a *App) renderComposeSuggestions(c *ui.Context, field string, suggestions []mailengine.RecipientSuggestion) {
	if len(suggestions) == 0 {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("cmp-sug-" + field)).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
		for i, s := range suggestions {
			sCopy := s
			lbl := sCopy.Email
			if sCopy.DisplayName != "" && !strings.EqualFold(sCopy.DisplayName, sCopy.Email) {
				lbl = fmt.Sprintf("%s <%s>", sCopy.DisplayName, sCopy.Email)
			}
			btn := ui.Button(c.Key(fmt.Sprintf("cmp-sug-%s-%d", field, i)), lbl).Padding(3, 8).Background(t.Background).Border(1, withAlpha(t.Accent, 0.45))
			if btn.Clicked() {
				a.applyComposeSuggestion(field, sCopy)
			}
		}
	})
}

func (a *App) buildComposeReq(openWindow bool) backend.ComposeRequest {
	accID := a.composeAccountID
	if accID == "" {
		accID = a.selectedAccount
	}
	return backend.ComposeRequest{
		AccountID:         accID,
		IdentityID:        a.composeIdentityID,
		FromEmail:         a.composeFromEmail,
		FromName:          a.composeFromName,
		DraftMessageID:    a.composeDraftID,
		To:                splitEmailList(a.composeTo),
		CC:                splitEmailList(a.composeCC),
		BCC:               splitEmailList(a.composeBCC),
		Subject:           strings.TrimSpace(a.composeSubject),
		Body:              a.composeBody,
		Attachments:       append([]string(nil), a.composeAttachments...),
		SourceAttachments: append([]backend.SourceAttachment(nil), a.composeSourceAttachments...),
		ReplyToMessageID:  a.composeReplyTo,
		ReplyType:         a.composeReplyType,
		InReplyTo:         a.composeInReplyTo,
		References:        a.composeReferences,
		OpenWindow:        openWindow,
		Sign:              [...]string{"", "on", "off"}[min(max(a.composeSign, 0), 2)],
		Encrypt:           a.composeEncrypt,
	}
}

func (a *App) currentViewTitle() string {
	switch a.navID {
	case "focus":
		return a.tr("Fokus (Ungelesen & ★)", "Focus (Unread & ★)")
	case "recent":
		return a.tr("Aktuelle Mails", "Recent Mail")
	case "action":
		return a.tr("Action & Warten", "Action & Waiting")
	case "calendar":
		return a.tr("Kalender & Termine", "Calendar & Events")
	case "contacts":
		return a.tr("Kontakte", "Contacts")
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
							"Kontakt existiert im Adressbuch — fehlende Felder ergänzen: ",
							"Contact exists in address book — fill missing fields: ",
						) + strings.Join(p.MissingFields, ", ") + "?"
						ui.Text(c, prompt).FontSize(12).Bold().TextColor(t.Warning)
					} else {
						ui.Text(c, a.tr(
							"Neuer Kontakt im Footer erkannt — in dein MailDiver-Adressbuch importieren?",
							"New contact detected in footer — import into your MailDiver address book?",
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
								btn := iconLabelBtn(c.Key(fmt.Sprintf("ct-apply-%d", idx)), iconCheck, a.tr("Daten vervollständigen", "Complete Missing Fields"), true, a.tr("Nur fehlende Felder im bestehenden Kontakt ergänzen", "Only add missing fields to existing contact"))
								if a.contactsBusy {
									btn.Disabled(true)
								}
								if btn.Clicked() {
									a.ApplyContactProposal(context.Background(), idx)
								}
							} else {
								btn := iconLabelBtn(c.Key(fmt.Sprintf("ct-apply-%d", idx)), iconUserPlus, a.tr("Kontakt importieren", "Import Contact"), true, a.tr("Neuen Kontakt im Adressbuch anlegen", "Create new contact in address book"))
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
				ui.Text(c, a.tr("Quick-Regel-Assistent · Filterregel", "Quick-Rule Wizard · Filter Rule")).FontSize(16).Bold().Grow(1)
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

					// Maintenance notice
					ui.Row(c).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
						notice := a.tr(
							"Hinweis: Die Regel wird lokal in MailDiver gespeichert, sofort auf die passenden Mails angewendet und künftig automatisch bei neu eintreffenden Posteingangs-Mails ausgeführt. Verwalten & Löschen unter Konten → Filterregeln.",
							"Note: The rule is saved locally in MailDiver, applied right away to matching emails, and executed automatically on newly arriving inbox mail. Manage or delete rules under Accounts → Filter Rules.",
						)
						ui.Text(c, notice).FontSize(11.5).TextColor(t.TextMuted)
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
				if iconLabelBtn(c.Key("qr-open-tb"), iconFilter, a.tr("Filterregeln verwalten", "Manage Filter Rules"), false, a.tr("Gespeicherte MailDiver-Regeln anzeigen und verwalten", "View and manage saved MailDiver filter rules")).Clicked() {
					a.OpenFilterRules(context.Background())
				}
				ui.Spacer(c)
				if ui.Button(c.Key("qr-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
					a.quickRuleOpen = false
				}
				saveLbl := a.tr("Regel speichern & anwenden", "Save Rule & Apply")
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
		ui.Column(c).Width(a.modalW(440)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
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
						a.ConfirmDeleteCalendarEvent(context.Background(), backend.ScopeOccurrence)
					}
					series := ui.PrimaryButton(c.Key("cd-series"), a.tr("Ganze Serie löschen", "Delete whole series")).Background(t.Danger)
					if a.calDelBusy {
						series.Disabled(true)
					}
					if series.Clicked() && !a.calDelBusy {
						a.ConfirmDeleteCalendarEvent(context.Background(), backend.ScopeSeries)
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
		ui.Column(c).Width(a.modalW(720)).MaxHeight(760).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconHelp).Size(18, 18).TextColor(t.Accent)
				ui.Text(c, a.tr("Hilfe · Überblick, Filterregeln & KI-Transparenz", "Help · Overview, Filter Rules & AI Transparency")).FontSize(16).Bold().Grow(1)
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
				{"rules", a.tr("4. Filterregeln", "4. Filter Rules")},
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
							a.tr("Was MailDiver macht · Überblick", "What MailDiver Does · Overview"),
							[]string{
								a.tr("• MailDiver ist dein eigenständiger nativer E-Mail-, Kalender- & Kontakte-Client: direkte IMAP/SMTP-Anbindung mit Echtzeit-Sync (IDLE), Google Kalender & iCal (.ics), Adressbuch und optionale KI-Hilfe in einer schnellen Desktop-App.", "• MailDiver is your standalone native email, calendar & contacts client: direct IMAP/SMTP with real-time sync (IDLE), Google Calendar & iCal (.ics), address book, and optional AI assistance in one fast desktop app."),
								a.tr("• Aufräumen statt manuell löschen: Wiederkehrende Mails per Multi-Select bündeln, mit dem Quick-Regel-Assistenten inklusive Preview prüfen und als automatische MailDiver-Filterregel speichern.", "• Clean up instead of daily manual deletes: batch-select recurring emails, preview matches with the Quick-Rule Wizard, and automate them as MailDiver filter rules."),
								a.tr("• KI bleibt strikt opt-in: Solange du keinen KI-Button klickst, läuft die App 100 % lokal direkt über IMAP/SMTP ohne Token-Verbrauch.", "• AI stays strictly opt-in: until you trigger an AI action, everything runs 100% locally via IMAP/SMTP with zero token usage."),
							},
						)
					}

					if show("local") {
						helpSection(c, iconShieldChk, t.Success,
							a.tr("1. 100% Lokal · Keine KI, 0 Tokens", "1. 100% Local · No AI, 0 Tokens"),
							[]string{
								a.tr("• Postfach-Sync (IMAP IDLE), Senden (SMTP), Anhänge, Ordner-Verwaltung, Suche (⌘K), Multi-Select und Lesen laufen komplett lokal ohne KI.", "• Mailbox sync (IMAP IDLE), sending (SMTP), attachments, folder management, search (⌘K), multi-select, and reading run completely locally without AI."),
								a.tr("• Mail-Aktionen (Antworten, Allen antworten, Weiterleiten, Gelesen/Ungelesen, Stern, Spam, Papierkorb, Archivieren) nutzen keine KI.", "• Mail actions (Reply, Reply All, Forward, read/unread, star, spam, trash, archive) do not use AI."),
								a.tr("• Quick-Regel-Assistent & Live-Preview prüfen Absender-/Betreff-Muster komplett lokal ohne KI-Tokens.", "• Quick-Rule Wizard & live preview evaluate sender/subject patterns completely locally without AI tokens."),
								a.tr("• Kalender (Google & .ics-Einladungen), Kontakte (.vcf Import/Export, Google Sync, Autocomplete) und Desktop-Meldungen kosten 0 Tokens.", "• Calendar (Google & .ics invites), Contacts (.vcf import/export, Google Sync, autocomplete), and desktop notifications cost 0 tokens."),
							},
						)
					}

					if show("ai") {
						helpSection(c, iconSparkles, t.Accent,
							a.tr("2. Wann KI & Tokens verwendet werden (Explizite Aktionen)", "2. When AI & Tokens Are Used (User-Triggered Actions)"),
							[]string{
								a.tr("• Inspector-Buttons: „Zusammenfassen“, „Entwurf“, „Termin“ und „Kontakte“ starten erst nach deinem Klick und senden nur den benötigten Mail-Kontext.", "• Inspector buttons: “Summarize”, “Draft”, “Event”, and “Contacts” only run when clicked and send only the required email context."),
								a.tr("• Quick-Regel optional: Nur ein Klick auf „Mit KI verfeinern“ im Assistenten nutzt das Modell für komplexe Betreff-Muster.", "• Quick Rule optional: only clicking “Refine with AI” inside the wizard uses the model for complex patterns."),
								a.tr("• KI-Assistent & Briefing: „Briefing“ und jede Chat-Nachricht im Assistent-Panel nutzen das gewählte Modell; Mail- und Kalenderinhalte werden dabei als Daten an den Anbieter gesendet (bei Cloud-Modellen).", "• AI Assistant & Briefing: “Briefing” and each chat prompt use your selected model; mail and calendar content is sent to that provider as data (for cloud models)."),
								a.tr("• Schreibende Aktionen des Assistenten (Entwurf speichern, Termin anlegen, Gelesen-Status) werden nur als Vorschlag mit allen Details angezeigt und erst nach Klick auf „Ausführen“ umgesetzt. Mails sendet der Assistent nie.", "• Write actions of the assistant (save draft, create event, read status) are shown as a proposal with all details and only run after you click “Execute”. The assistant never sends emails."),
								a.tr("• Triage-Scan (☰ → Triage-Scan starten): nutzt die KI nur, wenn „Mails per KI einordnen“ in den Einstellungen aktiviert ist.", "• Triage scan (☰ → Run triage scan): uses AI only when “Sort mails with AI” is enabled in Settings."),
							},
						)
					}

					if show("tokens") {
						helpSection(c, iconCpu, t.Warning,
							a.tr("3. Wo Tokens anfallen — Lokal vs. Cloud vs. CLI", "3. Where Tokens Apply — Local vs. Cloud vs. CLI"),
							[]string{
								a.tr("• Cloud-APIs (anthropic, openai, gemini, mistral, deepseek, groq): Verbrauchen bezahlte API-Tokens beim jeweiligen Anbieter.", "• Cloud APIs (anthropic, openai, gemini, mistral, deepseek, groq): Consume paid API tokens with that provider."),
								a.tr("• Lokale Modelle (ollama, llamacpp, llamafile): Laufen komplett auf deinem Gerät — keine Cloud-Übertragung, 0 Token-Kosten.", "• Local models (ollama, llamacpp, llamafile): Run entirely on your machine — no cloud traffic, 0 token cost."),
								a.tr("• CLI-Agenten (claude-cli, gemini-cli, opencode-cli): Nutzen dein lokal installiertes CLI-Konto bzw. Abo.", "• CLI agents (claude-cli, gemini-cli, opencode-cli): Use your locally installed CLI account or subscription."),
							},
						)
					}

					if show("rules") {
						ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
								ui.Icon(c, iconFilter).Size(15, 15).TextColor(t.Accent)
								ui.Text(c, a.tr("4. Quick-Regeln & Automatische Filter", "4. Quick Rules & Automatic Filters")).FontSize(13).Bold().Grow(1)
								if iconLabelBtn(c.Key("help-open-tb-filters"), iconFilter, a.tr("Filterregeln verwalten", "Manage Filter Rules"), false, a.tr("Gespeicherte Filterregeln öffnen", "Open saved filter rules")).Clicked() {
									a.helpOpen = false
									a.OpenFilterRules(context.Background())
								}
							})
							for _, line := range []string{
								a.tr("• Workflow: Mehrere ähnliche Mails per Checkbox (oder ⌘-Klick / Shift-Klick) markieren → „Quick-Regel“ klicken → Live-Preview prüfen → speichern & sofort anwenden.", "• Workflow: Check multiple similar emails (or ⌘-click / Shift-click) → click “Quick Rule” → review live preview → save & apply immediately."),
								a.tr("• Sichere Voreinstellung: Standardaktion ist „In Papierkorb verschieben“, damit versehentliche Treffer wiederherstellbar bleiben. Breite Regeln brauchen eine zweite Bestätigung.", "• Safe default: Default action is “Move to Trash” so any unexpected match remains recoverable. Broad rules require a second confirmation."),
								a.tr("• Verwaltung: Alle gespeicherten Regeln findest du unter Konten → Filterregeln (Pausieren, manuell Ausführen oder Löschen).", "• Management: View, pause, manually run, or delete all saved rules under Accounts → Filter Rules."),
							} {
								ui.Text(c, line).FontSize(12).TextColor(t.TextMuted)
							}
						})
					}

					if show("contacts") {
						helpSection(c, iconUserPlus, t.Accent,
							a.tr("5. Kontakte, vCard (.vcf), Google Sync & Footer-Erkennung", "5. Contacts, vCard (.vcf), Google Sync & Footer Extraction"),
							[]string{
								a.tr("• Eigene Kontakte-Ansicht in der Seitenleiste: Kontakte suchen, anlegen, bearbeiten, löschen und mit 1 Klick eine E-Mail schreiben.", "• Dedicated Contacts view in the sidebar: search, create, edit, delete contacts, and compose an email with 1 click."),
								a.tr("• Import & Sync: vCard-Dateien (.vcf) importieren/exportieren oder Google Kontakte (über deine Google-Anmeldung) synchronisieren.", "• Import & Sync: import/export vCard (.vcf) files or sync Google Contacts using your Google sign-in."),
								a.tr("• Autocomplete im Verfassen-Dialog: Beim Tippen in „An:“ oder „CC:“ schlägt MailDiver passende Kontakte und letzte Absender vor.", "• Compose autocomplete: typing in “To:” or “CC:” suggests matching contacts and recent senders."),
								a.tr("• KI-Footer-Erkennung: Klicke in einer Mail auf „Kontakte“, um Kontaktdaten aus der Signatur automatisch zu erkennen und nach Bestätigung zu importieren oder fehlende Felder zu ergänzen.", "• AI footer extraction: click “Contacts” on any email to extract signature details and import or fill missing fields after confirmation."),
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
					"Datenschutz: Passwörter liegen im System-Schlüsselbund; Schreibaktionen, Assistent-Vorschläge & Regeln wirken erst nach deinem Klick.",
					"Privacy: passwords stay in your system keychain; write actions, assistant proposals and filters only take effect after your explicit click.",
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
