package nativeui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/mailengine"
)

// RenderMain builds the entire native GPU-accelerated PostDive UI.
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
			if !a.hideSidebar && isMailNav(a.navID) {
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
	a.maybePersistLayoutSplits()
}

func (a *App) renderComposeContactPicker(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("cmp-ct-pick")).FillWidth().Padding(8).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c.Key("cmp-ct-search"), &a.composeContactFilter).Placeholder(a.tr("Kontakte filtern…", "Filter contacts…")).Grow(1)
			if ui.Button(c.Key("cmp-ct-close"), "✕").Padding(3, 8).Clicked() {
				a.composeContactPickerOpen = false
			}
		})
		q := strings.ToLower(strings.TrimSpace(a.composeContactFilter))
		var matches []backend.ContactRecord
		for _, rec := range a.contacts.list {
			if strings.TrimSpace(rec.Email) == "" {
				continue
			}
			if q != "" {
				hay := strings.ToLower(rec.DisplayName + " " + rec.FirstName + " " + rec.LastName + " " + rec.Email + " " + rec.Company)
				if !strings.Contains(hay, q) {
					continue
				}
			}
			matches = append(matches, rec)
			if len(matches) >= 20 {
				break
			}
		}
		if len(matches) == 0 {
			ui.Text(c, a.tr("Keine passenden Kontakte mit E-Mail-Adresse.", "No matching contacts with email address.")).FontSize(11.5).TextColor(t.TextMuted)
			return
		}
		ui.Scroll(c.Key("cmp-ct-list")).FillWidth().MaxHeight(160).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, rec := range matches {
					rc := rec
					formatted := formatEmailAddress(rc.DisplayName, rc.Email)
					appendAddr := func(cur string) string {
						cur = strings.TrimSpace(cur)
						if cur == "" {
							return formatted
						}
						return cur + ", " + formatted
					}
					ui.Row(c.Key("cmp-ct-row-" + rc.ID)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						label := rc.DisplayName
						if label == "" {
							label = rc.Email
						} else {
							label += " <" + rc.Email + ">"
						}
						ui.Text(c, label).FontSize(12).Grow(1).SingleLine()
						if ui.Button(c.Key("cmp-ct-to-"+rc.ID), a.tr("+ An", "+ To")).Padding(2, 7).Clicked() {
							a.composeTo = appendAddr(a.composeTo)
						}
						if ui.Button(c.Key("cmp-ct-cc-"+rc.ID), "+ CC").Padding(2, 7).Clicked() {
							a.composeCC = appendAddr(a.composeCC)
						}
						if ui.Button(c.Key("cmp-ct-bcc-"+rc.ID), "+ BCC").Padding(2, 7).Clicked() {
							a.composeBCC = appendAddr(a.composeBCC)
						}
					})
				}
			})
		})
	})
}

// renderDialogs shows the dialogs over the main window (desktop and phone).
func (a *App) renderDialogs(c *ui.Context) {
	a.renderEventModal(c)
	a.renderEventDetailModal(c)
	a.renderQuickRuleModal(c)
	a.renderCalDeleteModal(c)
	a.renderMovePicker(c)
	a.renderAttachmentPreviewModal(c)
	a.renderArchiveModal(c)
	a.renderOutboxModal(c)
	a.renderSelectionSummaryModal(c)
	a.renderSendLaterModal(c)
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
			ui.SidebarSection(c, a.tr("Postfach", "Mailbox"), &a.secViewsOpen, func() {
				ui.SidebarItem(c, "focus", iconFocus, a.tr("Fokus (Ungelesen & ★)", "Focus (Unread & ★)")).Children(func() {
					if n := a.focusCount(); n > 0 {
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
			case "activity":
				a.LoadActivity(context.Background(), false)
			default:
				a.LoadMessages(context.Background())
			}
		}

		// Compact "Today" mini-agenda at bottom of sidebar
		today := a.todayEvents()
		ui.Divider(c)
		if len(today) == 0 {
			ui.Row(c).FillWidth().Padding(8, 12).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Heute · Keine Termine", "Today · No events")).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
				if ui.Button(c.Key("sb-cal"), a.tr("Kalender", "Calendar")).Padding(2, 7).Clicked() {
					a.setNav("calendar")
				}
			})
		} else {
			ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.tr("Heute", "Today")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
					if ui.Button(c.Key("sb-cal"), a.tr("Alle", "All")).Padding(2, 7).Clicked() {
						a.setNav("calendar")
					}
				})
				maxShow := min(len(today), 3)
				for i := 0; i < maxShow; i++ {
					ev := today[i]
					row := ui.ButtonBase(c.Key(fmt.Sprintf("sb-today-%d", i))).FillWidth().Padding(4, 6).Radius(6).Background(t.Background).Border(1, t.Border).Gap(6).AlignItems(ui.Center).Label(ev.Title)
					row.Children(func() {
						ui.Text(c, formatEventRangeLang(ev.AllDay, ev.Start, "", a.uiLang())).FontSize(11).Bold().TextColor(t.Accent).Shrink(0)
						ui.Text(c, ev.Title).FontSize(11.5).SingleLine().Grow(1)
					})
					if row.Clicked() {
						a.setNav("calendar")
					}
				}
			})
		}
	})
}

func (a *App) renderWorkspace(c *ui.Context) {
	switch a.navID {
	case "calendar":
		a.renderCalendarWorkspace(c)
	case "contacts":
		a.renderContactsWorkspace(c)
	case "activity":
		a.renderActivityWorkspace(c)
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

func (a *App) currentSpamAccountID() string {
	if !strings.HasPrefix(a.navID, "folder:") {
		return ""
	}
	fid := strings.TrimPrefix(a.navID, "folder:")
	for _, f := range a.folders {
		if f.ID == fid {
			if strings.EqualFold(f.Type, "junk") || slices.Contains(f.SpecialUse, "junk") {
				return f.AccountID
			}
			base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(f.Path, "\\", "/"))))
			if base == "junk" || base == "spam" || base == "junk e-mail" || base == "bulk mail" {
				return f.AccountID
			}
		}
	}
	return ""
}

func (a *App) renderMailWorkspace(c *ui.Context) {
	t := c.Theme()
	ui.Split(c, &a.mailSplit, func() {
		// Message list column
		ui.Column(c).Fill().Background(t.Background).Children(func() {
			checked := a.CheckedMessageIDs()
			ui.Row(c).FillWidth().Padding(8, 12, 8, 10).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				if len(a.messages) > 0 {
					allChecked := a.AllVisibleMailsChecked()
					someChecked := len(checked) > 0 && !allChecked
					chkBg, chkBorder := t.Surface, t.Border
					if allChecked {
						chkBg, chkBorder = t.Accent, t.Accent
					} else if someChecked {
						chkBg, chkBorder = withAlpha(t.Accent, 0.25), t.Accent
					}
					selTip := a.tr("Alle sichtbaren Mails auswählen (⌘A)", "Select all visible emails (⌘A)")
					if allChecked {
						selTip = a.tr("Auswahl aufheben (⌘A)", "Clear selection (⌘A)")
					}
					selAllBtn := ui.ButtonBase(c.Key("list-select-all")).Padding(2, 0).Radius(4).Shrink(0).Label(selTip).Tooltip(selTip)
					selAllBtn.Children(func() {
						ui.Box(c).Size(15, 15).Radius(4).Background(chkBg).Border(1.2, chkBorder).Center().Children(func() {
							if allChecked {
								ui.Icon(c, iconCheck).Size(11, 11).TextColor(t.Background)
							} else if someChecked {
								ui.Box(c).Size(7, 2).Radius(1).Background(t.Accent)
							}
						})
					})
					if selAllBtn.Clicked() {
						a.ToggleSelectAllVisibleMails()
						checked = a.CheckedMessageIDs()
					}
				}
				title := a.currentViewTitle()
				ui.Text(c, title).FontSize(13).Bold().Grow(1).MinWidth(0).SingleLine()
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
				if spamAcc := a.currentSpamAccountID(); spamAcc != "" && a.isDirectMode() && len(a.messages) > 0 && !a.readOnly {
					armed := a.direct.emptySpamArmID == spamAcc && time.Since(a.direct.emptySpamArmAt) < 5*time.Second
					lbl := a.tr("Spam leeren", "Empty Spam")
					if armed {
						lbl = a.tr("Wirklich leeren?", "Confirm Empty?")
					}
					btn := ui.Button(c.Key("spam-empty"), lbl).Padding(3, 8)
					if armed {
						btn.Background(withAlpha(t.Danger, 0.18)).Border(1, t.Danger)
					}
					if btn.Clicked() {
						if armed {
							a.EmptyAccountSpam(context.Background(), spamAcc)
						} else {
							a.direct.emptySpamArmID = spamAcc
							a.direct.emptySpamArmAt = time.Now()
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

			a.renderUndoSendBar(c)

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

			if len(checked) > 0 {
				ui.Column(c.Key("bulk-bar")).FillWidth().Padding(6, 10).Gap(5).Background(withAlpha(t.Accent, 0.12)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.trf("%d / %d markiert", "%d / %d selected", len(checked), len(a.messages))).FontSize(11.5).Bold().TextColor(t.Accent).Grow(1).MinWidth(0).SingleLine()
						allBtnLbl := a.tr("Alle", "All")
						if a.AllVisibleMailsChecked() {
							allBtnLbl = a.tr("Keine", "None")
						}
						if ui.Button(c.Key("bulk-select-all"), allBtnLbl).Padding(2, 7).Tooltip(a.tr("Alle sichtbaren Mails auswählen / abwählen (⌘A)", "Select / deselect all visible emails (⌘A)")).Clicked() {
							a.ToggleSelectAllVisibleMails()
						}
						if ui.Button(c.Key("bulk-clear"), "✕").Padding(2, 7).Tooltip(a.tr("Auswahl aufheben (esc)", "Clear selection (esc)")).Clicked() {
							a.ClearMailSelection()
						}
					})
					ui.Row(c).FillWidth().Wrap().Gap(4).AlignItems(ui.Center).Children(func() {
						if iconLabelBtn(c.Key("bulk-rule"), iconFilter, a.tr("Quick-Regel", "Quick Rule"), true, a.tr("Aus markierten Mails eine Filterregel erstellen (q · mit Preview)", "Create filter rule from selected emails (q · with preview)")).Clicked() {
							a.OpenQuickRuleWizard()
						}
						if iconBtn(c.Key("bulk-ai-sum"), iconSparkles, a.tr("KI: Auswahl zusammenfassen", "AI: Summarize selection"), t.Accent, a.selSum.loading).Clicked() {
							a.RunSelectionSummary(context.Background())
						}
						if !a.readOnly {
							checkedMsgs := a.CheckedMessages()
							anyUnread := false
							anyUnflagged := false
							for _, cm := range checkedMsgs {
								if cm.Unread {
									anyUnread = true
								}
								if !cm.Flagged {
									anyUnflagged = true
								}
							}
							readIcon, readTip, readTarget := iconMailOpen, a.tr("Markierte als gelesen markieren (u)", "Mark selected as read (u)"), true
							readClr := t.Accent
							if !anyUnread {
								readIcon, readTip, readTarget = iconMailUnread, a.tr("Markierte als ungelesen markieren (u)", "Mark selected as unread (u)"), false
								readClr = ui.Color{}
							}
							if iconBtn(c.Key("bulk-read"), readIcon, readTip, readClr, false).Clicked() {
								target := readTarget
								msg := a.tr("Als gelesen markiert", "Marked as read")
								if !target {
									msg = a.tr("Als ungelesen markiert", "Marked as unread")
								}
								a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Read: &target}, msg)
							}
							starIcon, starClr, starTip := iconStarFilled, t.Warning, a.tr("Markierung entfernen (s)", "Remove star (s)")
							if anyUnflagged {
								starIcon, starClr, starTip = iconStar, ui.Color{}, a.tr("Markierte mit Stern markieren (s)", "Star selected (s)")
							}
							if iconBtn(c.Key("bulk-star"), starIcon, starTip, starClr, !anyUnflagged).Clicked() {
								flagVal := anyUnflagged
								a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Flagged: &flagVal}, a.tr("Markierung aktualisiert", "Star updated"))
								a.ClearMailSelection()
							}
							if iconBtn(c.Key("bulk-archive"), iconArchive, a.tr("Markierte archivieren (e)", "Archive selected (e)"), ui.Color{}, false).Clicked() {
								a.PerformBatchAction(context.Background(), checked, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"))
							}
							if iconBtn(c.Key("bulk-move"), iconFolderInput, a.tr("Markierte verschieben nach… (v)", "Move selected to… (v)"), ui.Color{}, false).Clicked() {
								a.OpenMovePicker()
							}
							if a.isDirectMode() && iconBtn(c.Key("bulk-spam"), iconSpam, a.tr("Markierte als Spam markieren", "Mark selected as spam"), ui.Color{}, false).Clicked() {
								a.MarkMailsSpam(context.Background(), checked)
							}
							if iconBtn(c.Key("bulk-trash"), iconTrash, a.tr("Markierte in Papierkorb verschieben (d d)", "Move selected to Trash (d d)"), t.Danger, a.confirmArmed("trash", checked)).Clicked() {
								a.trashMails(context.Background(), checked, "")
							}
						}
					})
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
			showSnippet := a.cfg.App.EffectiveListSnippets()
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

				// Right-click: the row's actions, on the checked selection when the row is in it.
				mRow := m
				ui.Row(c.Key("mail-"+m.ID)).FillWidth().Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).AlignItems(ui.Center).ContextMenu(func(menu *ui.Menu) {
					a.buildMailRowMenu(menu, mRow)
				}).Children(func() {
					chkBg := ui.Color{}
					chkBorder := withAlpha(t.Border, 0.65)
					if isChecked {
						chkBg = t.Accent
						chkBorder = t.Accent
					}
					chkTap := ui.Row(c.Key("chk-"+m.ID)).Padding(12, 4, 12, 12).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Box(c).Size(15, 15).Radius(4).Background(chkBg).Border(1.2, chkBorder).Center().Children(func() {
							if isChecked {
								ui.Icon(c, iconCheck).Size(11, 11).TextColor(t.Background)
							}
						})
					})
					if chkTap.Clicked() {
						a.ToggleMailChecked(i, c.Modifiers()&ui.Shift != 0)
					}

					// Clean two-line row with generous breathing room (Apple Mail / Canary style).
					starClicked := false
					row := ui.Column(c.Key("mail-body-"+m.ID)).Grow(1).MinWidth(0).Padding(10, 14, 10, 6).Gap(4).Children(func() {
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							if m.Unread {
								dotClr := t.Accent
								if selected {
									dotClr = t.AccentText
								}
								statusDot(c, dotClr, 6)
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
							st := ui.Text(c, peerLabel).FontSize(12).Grow(1).MinWidth(0).SingleLine()
							if m.Unread {
								st.Bold()
							} else if !selected {
								st.TextColor(t.TextMuted)
							}
							if m.Flagged {
								if !a.readOnly {
									sTip := a.tr("Markierung entfernen (s)", "Remove star (s)")
									sb := ui.ButtonBase(c.Key("row-star-" + m.ID)).Padding(2).Radius(4).Shrink(0).Label(sTip).Tooltip(sTip)
									sb.Children(func() { ui.Icon(c, iconStarFilled).Size(12, 12).TextColor(t.Warning) })
									if sb.Clicked() {
										starClicked = true
										flagVal := false
										a.PerformMessageAction(context.Background(), m.ID, backend.MessageActionsRequest{Flagged: &flagVal}, a.tr("Markierung aktualisiert", "Star updated"))
									}
								} else {
									ui.Icon(c, iconStarFilled).Size(11, 11).TextColor(t.Warning).Shrink(0)
								}
							}
							if m.HasAttachments {
								ui.Icon(c, iconPaperclip).Size(11, 11).TextColor(t.TextMuted).Shrink(0)
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
						if showSnippet && m.Snippet != "" {
							ui.Text(c, m.Snippet).FontSize(11.5).TextColor(t.TextMuted).FillWidth().MinWidth(0).SingleLine()
						}
					})
					// Dragging the row onto a sidebar folder moves it (or the checked selection).
					if !a.readOnly {
						row.Drag(a.rowDragValue(m))
						if row.Dragging() {
							row.Opacity(0.5)
						}
					}
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
			"PostDive verbindet sich lokal mit der IMAP/SMTP-Engine. Falls ein Fehler auftritt, prüfe deine Kontoeinstellungen.",
			"PostDive is connecting to the local IMAP/SMTP engine. If an error occurs, check your account settings.",
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
		if a.selSum.open {
			// The AI summary of the checked mails sits on top of the reading pane.
			ui.Scroll(c.Key("selsum-pane")).FillWidth().MaxHeight(420).Shrink(0).Children(func() {
				ui.Column(c).FillWidth().Padding(12, 12, 0, 12).Children(func() {
					a.renderSelectionSummaryCard(c)
				})
			})
		}
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
			ui.Column(c).Fill().Children(func() {
				ui.Scroll(c.Key("insp-scroll")).TrackScroll(&a.widgets.inspectorScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Padding(22, 24).Gap(14).Children(func() {
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
								if len(msg.Attachments) > 0 {
									attLabel := a.trf("%d Anhang", "%d attachment", len(msg.Attachments))
									if len(msg.Attachments) > 1 {
										attLabel = a.trf("%d Anhänge", "%d attachments", len(msg.Attachments))
									}
									ui.Row(c).Padding(2, 8).Radius(999).Background(withAlpha(t.Accent, 0.14)).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
										ui.Icon(c, iconPaperclip).Size(11, 11).TextColor(t.Accent)
										ui.Text(c, attLabel).FontSize(11).Bold().TextColor(t.Accent).SingleLine()
									})
								}
							})
							if len(msg.To) > 0 {
								ui.Text(c, a.tr("An: ", "To: ")+strings.Join(msg.To, ", ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
							}
							if len(msg.CC) > 0 {
								ui.Text(c, "CC: "+strings.Join(msg.CC, ", ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
							}
						})

						// Single clean action bar: core mail actions left, AI menu + More menu right.
						ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
							if a.isDraftsMessage(msg) {
								if iconLabelBtn(c.Key("act-edit-draft"), iconPen, a.tr("Entwurf weiterbearbeiten", "Edit Draft"), true, a.tr("Gespeicherten Entwurf im Editor öffnen", "Open saved draft in compose editor")).Clicked() {
									a.OpenComposeFromDraft(msg)
								}
								ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 2)
							}
							if iconBtn(c.Key("act-reply-edit"), iconReply, a.tr("Antworten (r)", "Reply (r)"), ui.Color{}, false).Clicked() {
								a.OpenComposeReply(msg, a.inspectorDraft)
							}
							if iconBtn(c.Key("act-reply-all"), iconReplyAll, a.tr("Allen antworten (a)", "Reply All (a)"), ui.Color{}, false).Clicked() {
								a.OpenComposeReplyAll(msg, a.inspectorDraft)
							}
							if iconBtn(c.Key("act-forward"), iconForward, a.tr("Weiterleiten (f)", "Forward (f)"), ui.Color{}, false).Clicked() {
								a.OpenComposeForward(msg)
							}
							if !a.readOnly {
								ui.Box(c).Size(1, 18).Background(t.Border).Margin(0, 2)
								if iconBtn(c.Key("act-archive"), iconArchive, a.tr("Archivieren (e)", "Archive (e)"), ui.Color{}, false).Clicked() {
									a.performMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Archive: true}, a.tr("Archiviert", "Archived"), a.neighborAfterRemoval(msg.ID))
								}
								if iconBtn(c.Key("act-move"), iconFolderInput, a.tr("Verschieben nach… (v)", "Move to… (v)"), ui.Color{}, false).Clicked() {
									a.OpenMovePicker()
								}
								trashTip := a.tr("In Papierkorb verschieben (d)", "Move to Trash (d)")
								if a.isTrashFolder(msg.FolderID) {
									trashTip = a.tr("Endgültig löschen (d)", "Delete permanently (d)")
								}
								if iconBtn(c.Key("act-trash"), iconTrash, trashTip, t.Danger, false).Clicked() {
									a.trashMails(context.Background(), nil, msg.ID)
								}
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
							if a.isDirectMode() && !a.readOnly && a.isJunkMessage(msg) {
								if iconLabelBtn(c.Key("act-not-spam"), iconCheck, a.tr("Kein Spam", "Not Spam"), false, a.tr("Aus Spam-Ordner zurück in den Posteingang verschieben", "Move out of Spam back to Inbox")).Clicked() {
									a.MarkMailNotSpam(context.Background(), msg.ID)
								}
							}

							ui.Spacer(c)

							if a.preview.open {
								if iconLabelBtn(c.Key("act-preview"), iconLink, a.tr("Web-Vorschau", "Web Preview"), true, a.tr("Eingebettete Browser-Vorschau schließen", "Close embedded browser preview")).Clicked() {
									a.ToggleWebPreview()
								}
							}

							iconLabelBtn(c.Key("act-ai-menu"), iconSparkles, a.tr("KI ▾", "AI ▾"), true, a.tr("KI-Aktionen für diese Mail (Zusammenfassen, Antwort-Entwurf, Termin, Kontakte)", "AI actions for this email (Summarize, Draft reply, Event, Contacts)")).PopoverMenuButton(func(m *ui.Menu) {
								if m.Item(a.tr("Zusammenfassen", "Summarize")).Chosen() {
									a.RunInspectorAI(context.Background(), "summarize")
								}
								if m.Item(a.tr("Antwort-Entwurf erstellen", "Draft reply")).Chosen() {
									a.RunInspectorAI(context.Background(), "draft_reply")
								}
								if m.Item(a.tr("Termin erkennen", "Extract event")).Chosen() {
									a.RunInspectorAI(context.Background(), "extract_events")
								}
								if m.Item(a.tr("Kontakte erkennen", "Extract contacts")).Chosen() {
									a.RunInspectorAI(context.Background(), "extract_contacts")
								}
								m.Separator()
								if m.Item(a.tr("Im KI-Assistent fragen…", "Ask AI Assistant…")).Shortcut(ui.Cmd, ui.KeyJ).Chosen() {
									a.showCopilot = true
									a.persistLayoutPrefs()
									a.SendCopilotPrompt(context.Background(), a.tr(
										"Fasse die aktuell ausgewählte Mail zusammen und schlage die nächste sinnvolle Aktion oder einen Antwort-Entwurf vor.",
										"Summarize the currently selected email and suggest the next best action or reply draft.",
									))
								}
							})

							iconBtn(c.Key("act-more"), iconDots, a.tr("Weitere Aktionen", "More actions"), ui.Color{}, false).PopoverMenuButton(func(m *ui.Menu) {
								readLbl := a.tr("Als gelesen markieren", "Mark as read")
								targetRead := true
								if !msg.Unread {
									readLbl = a.tr("Als ungelesen markieren", "Mark as unread")
									targetRead = false
								}
								if m.Item(readLbl).Chosen() {
									a.PerformMessageAction(context.Background(), msg.ID, backend.MessageActionsRequest{Read: &targetRead}, a.tr("Lesestatus aktualisiert", "Read status updated"))
								}
								if !a.readOnly && a.isDirectMode() && !a.isJunkMessage(msg) {
									if m.Item(a.tr("Als Spam markieren", "Mark as Spam")).Chosen() {
										a.MarkMailSpam(context.Background(), msg.ID)
									}
								}
								if unsubHTTPS, unsubMailto, _ := parseUnsubscribeTargets(msg); unsubHTTPS != "" || unsubMailto != "" {
									if m.Item(a.tr("Vom Newsletter abmelden", "Unsubscribe from newsletter")).Chosen() {
										a.PerformUnsubscribe(context.Background(), msg)
									}
								}
								m.Separator()
								prevLbl := a.tr("Web-Vorschau ein/aus", "Toggle Web Preview")
								if len(a.preview.links) > 0 && !a.preview.open {
									prevLbl = fmt.Sprintf("%s (%d)", a.tr("Web-Vorschau öffnen", "Open Web Preview"), len(a.preview.links))
								}
								if m.Item(prevLbl).Checked(a.preview.open).Chosen() {
									a.ToggleWebPreview()
								}
								if m.Item(a.tr("Filterregel erstellen…", "Create filter rule…")).Chosen() {
									a.OpenQuickRuleWizard()
								}
								m.Separator()
								if m.Item(a.tr("Drucken", "Print")).Shortcut(ui.Cmd, ui.KeyP).Chosen() {
									a.PrintSelectedMessage(context.Background())
								}
								if a.isDirectMode() {
									if m.Item(a.tr("Als .eml speichern", "Save as .eml")).Chosen() {
										a.SaveSelectedMessageEML(context.Background())
									}
								}
								m.Submenu(a.tr("Unterhaltung öffnen in", "Open conversation in"), func(sm *ui.Menu) {
									if sm.Item(a.tr("Claude Code (Terminal)", "Claude Code (terminal)")).Chosen() {
										a.OpenConversationIn(context.Background(), msg, handoffClaudeCode)
									}
									if sm.Item(a.tr("Antigravity (App)", "Antigravity (app)")).Chosen() {
										a.OpenConversationIn(context.Background(), msg, handoffAntigravity)
									}
									if sm.Item(a.tr("Antigravity-Agent (Terminal)", "Antigravity agent (terminal)")).Chosen() {
										a.OpenConversationIn(context.Background(), msg, handoffAgy)
									}
									if sm.Item(a.tr("Claude (App, über Zwischenablage)", "Claude (app, via clipboard)")).Chosen() {
										a.OpenConversationIn(context.Background(), msg, handoffClaudeApp)
									}
									sm.Separator()
									if sm.Item(a.tr("Als Markdown kopieren", "Copy as Markdown")).Chosen() {
										a.OpenConversationIn(context.Background(), msg, handoffCopy)
									}
								})
							})
						})

						a.renderICSBanner(c, msg)
						a.renderInspectorAIResult(c, msg)

						ui.Divider(c)

						// Conversation: newer mails above the open one, older ones below (newest on top).
						newerMails, olderMails := a.conversationSplit(msg)
						a.renderConversationGroup(c, "newer", a.trf("Neuere Mails in dieser Unterhaltung (%d)", "Newer emails in this conversation (%d)", len(newerMails)), newerMails)

						// Body
						a.renderMailBody(c, msg)

						a.renderConversationGroup(c, "older", a.trf("Frühere Mails in dieser Unterhaltung (%d)", "Earlier emails in this conversation (%d)", len(olderMails)), olderMails)
					})
				})
				// Pinned attachment bar (always visible at the bottom of the reading pane, like Thunderbird)
				if len(msg.Attachments) > 0 {
					a.renderAttachmentsBar(c, msg)
				}
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

// defaultICSTargetCalendar picks the best writable calendar label for an .ics invitation:
// 1. The calendar the event is already in (if writable),
// 2. A writable calendar matching the receiving mail account's email or domain,
// 3. The last calendar manually chosen by the user for an .ics invite,
// 4. The first non-local synced writable calendar (e.g. Google Calendar / CalDAV),
// 5. Fallback to the first available writable calendar.
func (a *App) defaultICSTargetCalendar(msg *backend.MessageDetail) string {
	if len(a.eventCalLabels) == 0 {
		return ""
	}
	if msg != nil && msg.ICSInvite != nil {
		if inCal := strings.TrimSpace(msg.ICSInvite.InCalendarName); inCal != "" {
			if _, ok := a.eventCalByLabel[inCal]; ok {
				return inCal
			}
		}
	}
	if msg != nil {
		var hints []string
		for _, acc := range a.accounts {
			if acc.ID == msg.AccountID {
				for _, id := range acc.Identities {
					if em := strings.ToLower(strings.TrimSpace(id.Email)); em != "" {
						hints = append(hints, em)
					}
				}
				if nm := strings.ToLower(strings.TrimSpace(acc.Name)); strings.Contains(nm, "@") {
					hints = append(hints, nm)
				}
				break
			}
		}
		for _, addr := range msg.To {
			if em := strings.ToLower(strings.TrimSpace(addr)); em != "" {
				hints = append(hints, em)
			}
		}
		genericDomains := map[string]bool{
			"gmail": true, "googlemail": true, "gmx": true, "web": true,
			"icloud": true, "me": true, "outlook": true, "hotmail": true,
			"live": true, "yahoo": true, "posteo": true, "mailbox": true,
			"proton": true, "protonmail": true, "t-online": true,
		}
		for _, raw := range hints {
			em := raw
			if lt := strings.IndexByte(em, '<'); lt >= 0 {
				if gt := strings.IndexByte(em[lt+1:], '>'); gt >= 0 {
					em = strings.TrimSpace(em[lt+1 : lt+1+gt])
				}
			}
			domBase := ""
			if at := strings.LastIndexByte(em, '@'); at >= 0 && at+1 < len(em) {
				dom := em[at+1:]
				if dot := strings.IndexByte(dom, '.'); dot > 0 {
					domBase = dom[:dot]
				} else {
					domBase = dom
				}
				if len(domBase) < 3 || genericDomains[domBase] {
					domBase = ""
				}
			}
			for _, cal := range a.calendars {
				if cal.ReadOnly {
					continue
				}
				if _, ok := a.eventCalByLabel[cal.Name]; !ok {
					continue
				}
				lowName := strings.ToLower(cal.Name)
				lowID := strings.ToLower(cal.ID)
				if (em != "" && (strings.Contains(lowName, em) || strings.Contains(lowID, em))) ||
					(domBase != "" && strings.Contains(lowName, domBase)) {
					return cal.Name
				}
			}
		}
	}
	if a.icsLastCalLabel != "" {
		if _, ok := a.eventCalByLabel[a.icsLastCalLabel]; ok {
			return a.icsLastCalLabel
		}
	}
	for _, cal := range a.calendars {
		if cal.ReadOnly || cal.ID == mailengine.LocalCalendarID {
			continue
		}
		if _, ok := a.eventCalByLabel[cal.Name]; ok {
			return cal.Name
		}
	}
	return a.eventCalLabels[0]
}

// renderICSBanner shows a calendar invitation in a mail with its answers (desktop and phone).
func (a *App) renderICSBanner(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	// .ics Calendar Invitation Banner (RSVP Accept/Tentative/Decline + 1-click import, 0 AI tokens)
	if msg.HasICS && msg.ICSInvite != nil && (msg.ICSInvite.Method == "REPLY" || msg.ICSInvite.Method == "CANCEL") {
		a.renderICSNoticeBanner(c, msg.ICSInvite)
		return
	}
	if msg.HasICS {
		status := ""
		inCal := ""
		if msg.ICSInvite != nil {
			status = strings.TrimSpace(msg.ICSInvite.ResponseStatus)
			inCal = strings.TrimSpace(msg.ICSInvite.InCalendarName)
		}

		bannerTint := t.Accent
		badgeText := ""
		badgeFg := t.Accent
		badgeBg := withAlpha(t.Accent, 0.16)
		switch status {
		case "accepted":
			bannerTint = t.Success
			badgeText = a.tr("✓ Zugesagt", "✓ Accepted")
			badgeFg = t.Success
			badgeBg = withAlpha(t.Success, 0.18)
		case "tentative":
			bannerTint = t.Warning
			badgeText = a.tr("? Vorläufig", "? Tentative")
			badgeFg = t.Warning
			badgeBg = withAlpha(t.Warning, 0.20)
		case "declined":
			bannerTint = t.Danger
			badgeText = a.tr("✕ Abgesagt", "✕ Declined")
			badgeFg = t.Danger
			badgeBg = withAlpha(t.Danger, 0.18)
		case "needsAction":
			badgeText = a.tr("Antwort offen", "Pending")
			badgeFg = t.Accent
			badgeBg = withAlpha(t.Accent, 0.16)
		case "imported":
			badgeText = a.tr("✓ Im Kalender", "✓ In Calendar")
			badgeFg = t.Success
			badgeBg = withAlpha(t.Success, 0.16)
		}
		if badgeText != "" && inCal != "" && status != "declined" {
			badgeText += " · " + inCal
		}

		ui.Column(c.Key("ics-banner")).FillWidth().Padding(10, 12).Radius(8).Background(withAlpha(bannerTint, 0.10)).Border(1, withAlpha(bannerTint, 0.45)).Gap(8).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconCal).Size(18, 18).TextColor(bannerTint).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					invTitle := a.tr("Termineinladung (.ics)", "Calendar Invitation (.ics)")
					if msg.ICSInvite != nil && msg.ICSInvite.Title != "" {
						invTitle = msg.ICSInvite.Title
					}
					ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
						ui.Text(c, invTitle).FontSize(12.5).Bold().Selectable()
						if badgeText != "" {
							pillBadge(c, badgeText, badgeBg, badgeFg)
						}
					})
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
						if inv := msg.ICSInvite; len(inv.Attendees) > 0 {
							a.renderAttendees(c, "ics-att-"+msg.ID, inv.Attendees, status, !inv.AttendeesFromMail)
							if inv.AttendeesFromMail {
								ui.Text(c, a.tr("Die Einladung nennt keine Teilnehmer – das sind die Empfänger der Mail.", "The invitation names no attendees – these are the mail's recipients.")).FontSize(11).TextColor(t.TextMuted)
							}
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
				if a.icsTargetMsgID != msg.ID {
					a.icsTargetMsgID = msg.ID
					a.icsTargetCal = a.defaultICSTargetCalendar(msg)
				} else if _, ok := a.eventCalByLabel[a.icsTargetCal]; !ok {
					a.icsTargetCal = a.defaultICSTargetCalendar(msg)
				}
				targetCalID := a.eventCalByLabel[a.icsTargetCal]
				if targetCalID == "" {
					targetCalID = mailengine.LocalCalendarID
				}
				alreadyInSelected := inCal != "" && (a.icsTargetCal == "" || strings.EqualFold(inCal, a.icsTargetCal))

				ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					acceptPrimary := status == "" || status == "needsAction" || status == "imported"
					acceptLabel := a.tr("Zusagen", "Accept")
					if status == "accepted" {
						acceptLabel = a.tr("Zugesagt", "Accepted")
					}
					acceptBtn := iconLabelBtn(c.Key("ics-accept"), iconCheck, acceptLabel, acceptPrimary, a.tr("Einladung annehmen, in den gewählten Kalender übernehmen & Antwort senden", "Accept invitation, add to selected calendar & send RSVP reply"))
					if status == "accepted" {
						acceptBtn.Background(withAlpha(t.Success, 0.22)).Border(1, t.Success).TextColor(t.Success)
					}
					if acceptBtn.Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, targetCalID, "accepted")
					}

					tentLabel := a.tr("Vielleicht", "Tentative")
					if status == "tentative" {
						tentLabel = a.tr("✓ Vielleicht", "✓ Tentative")
					}
					tentBtn := ui.Button(c.Key("ics-tentative"), tentLabel)
					if status == "tentative" {
						tentBtn.Background(withAlpha(t.Warning, 0.22)).Border(1, t.Warning).TextColor(t.Warning)
					}
					if tentBtn.Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, targetCalID, "tentative")
					}

					declLabel := a.tr("Absagen", "Decline")
					if status == "declined" {
						declLabel = a.tr("✕ Abgesagt", "✕ Declined")
					}
					declBtn := ui.Button(c.Key("ics-decline"), declLabel).TextColor(t.Danger)
					if status == "declined" {
						declBtn.Background(withAlpha(t.Danger, 0.22)).Border(1, t.Danger)
					}
					if declBtn.Clicked() {
						a.RespondMailICS(context.Background(), msg.ID, targetCalID, "declined")
					}
					if len(a.eventCalLabels) > 1 {
						ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
							ui.Text(c, a.tr("Kalender:", "Calendar:")).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
							if ui.Select(c.Key("ics-cal-sel"), &a.icsTargetCal, a.eventCalLabels).MinWidth(140).Changed() {
								a.icsLastCalLabel = a.icsTargetCal
							}
						})
					}
					if !alreadyInSelected {
						ui.Spacer(c)
						if iconLabelBtn(c.Key("ics-adopt"), iconCalPlus, a.tr("Nur übernehmen", "Import Only"), false, a.tr("Termin ohne E-Mail-Antwort in den gewählten Kalender übernehmen", "Import event into selected calendar without sending RSVP")).Clicked() {
							a.ImportMailICS(context.Background(), msg.ID, targetCalID)
						}
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
			if f := a.inspectorMove; f != nil && a.inspectorMoveMsgID == msg.ID && !a.readOnly {
				name := f.Name
				if name == "" {
					name = cleanFolderPath(f.Path)
				}
				ui.Row(c).Gap(8).Wrap().Children(func() {
					if iconLabelBtn(c.Key("ai-move"), iconFolderInput, a.tr("Verschieben nach ", "Move to ")+name, false, a.tr("Von der KI vorgeschlagen", "Suggested by AI")).Clicked() {
						a.ApplyInspectorMove(context.Background())
					}
				})
			}
			if a.inspectorEvent != nil && !a.readOnly {
				ui.Row(c).Gap(8).Children(func() {
					if iconLabelBtn(c.Key("ev-adopt"), iconCalPlus, a.tr("Termin übernehmen: ", "Add Event: ")+a.inspectorEvent.Title, true, "").Clicked() {
						a.OpenNewEventModal(a.inspectorEvent)
						// The people of the mail are the likely attendees; inviting them stays a
						// choice of its own.
						a.resetEventAttendees(a.mailParticipants(msg))
						a.eventSendInvites = false
					}
				})
			}
		})
	}

}

// renderAttachmentsBar renders a sticky attachment footer pinned to the bottom of the desktop
// reading pane so attachments stay visible without scrolling past long bodies or signatures.
func (a *App) renderAttachmentsBar(c *ui.Context, msg *backend.MessageDetail) {
	if len(msg.Attachments) == 0 {
		return
	}
	t := c.Theme()
	var totalBytes int64
	for _, att := range msg.Attachments {
		totalBytes += att.Size
	}
	hdr := a.trf("%d Anhang", "%d attachment", len(msg.Attachments))
	if len(msg.Attachments) > 1 {
		hdr = a.trf("%d Anhänge", "%d attachments", len(msg.Attachments))
	}
	hdr = fmt.Sprintf("%s · %s", hdr, formatByteSize(totalBytes))
	ui.Column(c.Key("att-bar")).FillWidth().Padding(8, 14).Gap(6).Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Shrink(0).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconPaperclip).Size(13, 13).TextColor(t.Accent).Shrink(0)
			ui.Text(c, hdr).FontSize(11.5).Bold().TextColor(t.Text).SingleLine()
		})
		ui.Scroll(c.Key("att-bar-scroll")).FillWidth().MaxHeight(120).Children(func() {
			a.renderAttachmentChips(c, msg)
		})
	})
}

func formatByteSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", max(0, n))
	}
}

func (a *App) renderAttachmentChips(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
		for i, att := range msg.Attachments {
			attCopy := att
			lbl := fmt.Sprintf("%s (%s)", attCopy.Name, formatByteSize(attCopy.Size))
			if a.isDirectMode() {
				if isPreviewableAttachmentName(attCopy.Name) {
					ui.Row(c.Key(fmt.Sprintf("att-pair-%d", i))).Gap(2).AlignItems(ui.Center).Children(func() {
						if iconLabelBtn(c.Key(fmt.Sprintf("att-pv-%d", i)), iconBookOpen, lbl, false, a.tr("Anhang direkt in der App ansehen", "Preview attachment in app")).Clicked() {
							a.OpenAttachmentPreview(context.Background(), msg.ID, attCopy.PartName, attCopy.Name)
						}
						if a.onSaveCopy != nil {
							// Phones: the private cache is no place to keep it; the user picks one.
							if iconBtn(c.Key(fmt.Sprintf("att-dl-%d", i)), iconDownload, a.tr("Speichern unter…", "Save to…"), ui.Color{}, false).Clicked() {
								a.SaveAttachmentCopy(context.Background(), msg.ID, attCopy.PartName, attCopy.Name)
							}
						} else if iconBtn(c.Key(fmt.Sprintf("att-dl-%d", i)), iconDownload, a.tr("In ~/Downloads speichern & öffnen", "Save to ~/Downloads & open"), ui.Color{}, false).Clicked() {
							a.DownloadAndOpenAttachment(context.Background(), msg.ID, attCopy.PartName, attCopy.Name)
						}
					})
				} else if iconLabelBtn(c.Key(fmt.Sprintf("att-dl-%d", i)), iconDownload, lbl, false, a.tr("In ~/Downloads speichern & öffnen", "Save to ~/Downloads & open")).Clicked() {
					a.DownloadAndOpenAttachment(context.Background(), msg.ID, attCopy.PartName, attCopy.Name)
				}
			} else {
				pillBadge(c, lbl, t.Background, t.Text)
			}
		}
	})
}

// renderAttachments lists the open mail's attachments inline (phone view & compact layouts).
func (a *App) renderAttachments(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	if len(msg.Attachments) > 0 {
		ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(6).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconPaperclip).Size(13, 13).TextColor(t.Accent).Shrink(0)
				ui.Text(c, a.trf("Anhänge (%d)", "Attachments (%d)", len(msg.Attachments))).FontSize(12).Bold()
			})
			a.renderAttachmentChips(c, msg)
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
	a.modal(c, &a.composeOpen, func() {
		ui.Column(c).Width(a.modalW(620)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			a.renderComposeContent(c, false)
		})
	})
}

func (a *App) renderComposeContent(c *ui.Context, inWindow bool) {
	t := c.Theme()
	title := a.ComposeWindowTitle()
	if snap := a.composeSnapshot(); snap != a.composePrevSnap {
		if a.composePrevSnap != "" {
			a.composeDirtyAt = time.Now()
		}
		a.composePrevSnap = snap
	}
	a.maybeAutoSaveComposeDraft()
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
					a.SaveDraft(context.Background(), a.buildComposeReq(false), a.composeDraftSavedMsg())
				}
				if ui.Button(c.Key("cmp-ask-discard"), a.tr("Verwerfen", "Discard")).Clicked() {
					a.discardCompose()
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
					oldPlain, _, _ := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
					oldSig := stripSignatureDelim(oldPlain)
					ch := choiceByLabel[a.composeAccountLabel]
					a.composeAccountID = ch.AccountID
					a.composeIdentityID = ch.IdentityID
					a.composeFromEmail = ch.Email
					a.composeFromName = ch.Name
					newPlain, newHTML, newBelow := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
					newSig := stripSignatureDelim(ch.Signature)
					if newSig == "" {
						newSig = stripSignatureDelim(newPlain)
					} else {
						newPlain = ch.Signature
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
						sigHTML := composeSignatureBlockHTMLFromParts(newPlain, newHTML)
						if newBelow && sigHTML != "" {
							sigHTML = strings.Replace(sigHTML, `data-mdv-sig="1"`, `data-mdv-sig="1" data-mdv-sig-below="1"`, 1)
						}
						a.composeEditor.UpdateSignature(sigHTML)
					}
				}
			})
		}

		// To field + Contacts picker + autocomplete suggestions
		ui.Row(c.Key("cmp-to-row")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("cmp-to"), &a.composeTo).Placeholder(a.tr("An (Komma-getrennt)…", "To (comma-separated)…")).Grow(1)
			if a.isDirectMode() {
				if ui.Button(c.Key("cmp-pick-contacts"), a.tr("Kontakte…", "Contacts…")).Padding(5, 9).Clicked() {
					a.composeContactPickerOpen = !a.composeContactPickerOpen
					if a.composeContactPickerOpen && len(a.contacts.list) == 0 {
						a.LoadContacts(context.Background())
					}
				}
			}
		})
		if a.composeContactPickerOpen {
			a.renderComposeContactPicker(c)
		}
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

		ui.Row(c.Key("cmp-sub-row")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("cmp-sub"), &a.composeSubject).Placeholder(a.tr("Betreff…", "Subject…")).Grow(1)
			if iconLabelBtn(c.Key("cmp-ai-btn"), iconSparkles, a.tr("KI-Hilfe", "AI Assist"), a.composeAIOpen, a.tr("KI-Assistent zum Entwerfen, Verbessern, Übersetzen & Prüfen", "AI assistant to draft, polish, translate & review")).Clicked() {
				a.composeAIOpen = !a.composeAIOpen
			}
			tplLabel := a.tr("Vorlagen ▾", "Templates ▾")
			if n := len(a.cfg.App.ComposeTemplates); n > 0 {
				tplLabel = fmt.Sprintf("%s (%d) ▾", a.tr("Vorlagen", "Templates"), n)
			}
			if ui.Button(c.Key("cmp-tpl-btn"), tplLabel).Padding(5, 9).Clicked() {
				a.composeTplOpen = !a.composeTplOpen
			}
		})
		a.renderComposeAIAssist(c)
		if a.composeTplOpen {
			ui.Column(c.Key("cmp-tpl-box")).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				if len(a.cfg.App.ComposeTemplates) == 0 {
					ui.Text(c, a.tr("Noch keine Textbausteine gespeichert.", "No templates saved yet.")).FontSize(11.5).TextColor(t.TextMuted)
				} else {
					for _, tpl := range a.cfg.App.ComposeTemplates {
						tp := tpl
						ui.Row(c.Key("cmp-tpl-r-" + tp.ID)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							lbl := tp.Name
							if tp.Subject != "" {
								lbl += " · " + tp.Subject
							}
							if ui.Button(c.Key("cmp-tpl-ins-"+tp.ID), lbl).Grow(1).Padding(3, 8).Clicked() {
								a.insertComposeTemplate(tp)
							}
							if ui.Button(c.Key("cmp-tpl-rm-"+tp.ID), "✕").Padding(2, 6).Clicked() {
								a.deleteComposeTemplate(tp.ID)
							}
						})
					}
				}
				ui.Divider(c)
				ui.Row(c.Key("cmp-tpl-save-row")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key("cmp-tpl-name"), &a.composeTplName).Placeholder(a.tr("Name für neuen Textbaustein (übernimmt aktuellen Text)…", "Name for new snippet (saves current body)…")).Grow(1)
					if ui.Button(c.Key("cmp-tpl-save"), a.tr("+ Als Vorlage speichern", "+ Save as template")).Padding(3, 8).Clicked() {
						bodyTxt := extractComposeUserTextOnly(a.composeBody)
						if a.saveNewComposeTemplate(a.composeTplName, a.composeSubject, bodyTxt) {
							a.composeTplName = ""
						}
					}
				})
			})
		}
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
				a.insertComposeText(a.tr("**fett**", "**bold**"))
			}
			if ui.Button(c.Key("cmp-fmt-i"), "I").Padding(1, 7).Tooltip(a.tr("Kursiv (*Text*)", "Italic (*text*)")).Clicked() {
				a.insertComposeText(a.tr("*kursiv*", "*italic*"))
			}
			if ui.Button(c.Key("cmp-fmt-code"), "</>").Padding(1, 7).Tooltip(a.tr("Code (`code`)", "Code (`code`)")).Clicked() {
				a.insertComposeText("`code`")
			}
			if ui.Button(c.Key("cmp-fmt-ul"), a.tr("• Liste", "• List")).Padding(1, 7).Tooltip(a.tr("Aufzählung (- Punkt)", "Bullet list (- item)")).Clicked() {
				a.insertComposeText(a.tr("\n- Punkt 1\n- Punkt 2", "\n- Item 1\n- Item 2"))
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
			// The format buttons, templates and AI "Einfügen" insert at the caret left here.
			if a.composeCaretApply {
				bodyArea.SetTextSelection(a.composeCaret, a.composeCaret)
				a.composeCaretApply = false
			} else if bodyArea.Focused() {
				_, a.composeCaret = bodyArea.TextSelection()
				a.composeCaretBody, a.composeCaretOK = a.composeBody, true
			}
			if !a.readOnly && !a.composeBusy && bodyArea.Shortcut(ui.Cmd, ui.KeyEnter) {
				if !a.composeConfirm {
					a.composeConfirm = true
					a.toast(a.tr("Nochmal ⌘↩ drücken zum Senden", "Press ⌘↩ again to send"), "info")
				} else {
					a.requestComposeSend(context.Background(), a.buildComposeReq(false))
				}
			}
		}
	afterComposeEditor:
		if !a.readOnly && !a.composeBusy {
			if c.Shortcut(ui.Cmd, ui.KeyEnter) {
				if !a.composeConfirm {
					a.composeConfirm = true
					a.toast(a.tr("Nochmal ⌘↩ drücken zum Senden", "Press ⌘↩ again to send"), "info")
				} else {
					a.requestComposeSend(context.Background(), a.buildComposeReq(false))
				}
			}
			if c.Shortcut(ui.Cmd, ui.KeyS) {
				a.SaveDraft(context.Background(), a.buildComposeReq(false), a.composeDraftSavedMsg())
			}
		}
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
					attIn := ui.TextInput(c.Key("cmp-att-in"), &a.composeAttachInput).Placeholder(a.tr("Oder Pfad eingeben (z. B. ~/Downloads/datei.pdf)…", "Or type path (e.g. ~/Downloads/file.pdf)…")).Grow(1)
					addClicked := ui.Button(c.Key("cmp-att-add"), a.tr("Anhängen", "Attach")).Clicked()
					if (addClicked || attIn.Submitted()) && strings.TrimSpace(a.composeAttachInput) != "" {
						a.addComposeAttachment(a.composeAttachInput)
					}
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

		if a.composeReplyEncrypted && a.composeHasDecrypted && a.composeEncrypt {
			ui.Text(c, a.tr(
				"Die Original-Mail war verschlüsselt und ist unten zitiert – die Antwort wird ebenfalls verschlüsselt gesendet. Entwürfe werden nicht automatisch gespeichert.",
				"The original mail was encrypted and is quoted below – the reply is sent encrypted too. Drafts are not saved automatically.",
			)).FontSize(11.5).TextColor(t.TextMuted)
		} else if a.composeReplyEncrypted {
			if a.composeCryptoTech == 2 {
				ui.Text(c, a.tr(
					"Hinweis: Die Original-Mail war OpenPGP-verschlüsselt. Aktiviere unten „Verschlüsseln“, falls der Empfänger-Public-Key vorhanden ist.",
					"Note: the original email was OpenPGP-encrypted. Enable “Encrypt” below if the recipient public key is available.",
				)).FontSize(11.5).TextColor(t.Warning)
			} else {
				ui.Text(c, a.tr(
					"Hinweis: Die Original-Mail war verschlüsselt. Aktiviere unten „S/MIME verschlüsseln“, falls das Empfänger-Zertifikat vorhanden ist.",
					"Note: the original email was encrypted. Enable “S/MIME encrypt” below if the recipient certificate is available.",
				)).FontSize(11.5).TextColor(t.Warning)
			}
		}

		if a.isDirectMode() && !a.readOnly {
			ui.Row(c.Key("cmp-smime-bar")).FillWidth().Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconLock).Size(13, 13).TextColor(t.TextMuted)
				ui.Text(c, a.tr("Sicherheit:", "Security:")).FontSize(11.5).TextColor(t.TextMuted)
				ui.Segmented(c.Key("cmp-sign"), &a.composeSign, a.tr("Auto", "Auto"), a.tr("Signieren", "Sign"), a.tr("Ohne", "Off"))
				ui.Checkbox(c.Key("cmp-enc"), &a.composeEncrypt, a.tr("Verschlüsseln", "Encrypt"))
				ui.Segmented(c.Key("cmp-tech"), &a.composeCryptoTech, a.tr("Auto", "Auto"), "S/MIME", "OpenPGP")
			})
		}

		if a.readOnly {
			ui.Text(c, a.tr("Read-Only-Modus aktiv — Senden und Speichern sind deaktiviert.", "Read-only mode active — sending and saving are disabled.")).FontSize(11.5).TextColor(t.Warning)
		}

		if a.composePlainWarn {
			ui.Row(c.Key("cmp-plain-warn")).FillWidth().Padding(8, 10).Radius(8).Gap(8).Wrap().AlignItems(ui.Center).
				Background(withAlpha(t.Danger, 0.14)).Border(1, withAlpha(t.Danger, 0.5)).Children(func() {
				ui.Text(c, a.tr("Die Mail enthält den Text einer verschlüsselten Mail, würde aber unverschlüsselt gesendet.", "This mail contains the text of an encrypted mail but would be sent unencrypted.")).
					FontSize(12).Bold().Grow(1).MinWidth(0)
				if ui.PrimaryButton(c.Key("cmp-plain-enc"), a.tr("Verschlüsselt senden", "Send encrypted")).Clicked() {
					a.composePlainWarn = false
					a.composeEncrypt = true
					req := a.buildComposeReq(false)
					req.SendAt = a.composeWarnSendAt
					a.requestComposeSend(context.Background(), req)
				}
				if ui.Button(c.Key("cmp-plain-send"), a.tr("Unverschlüsselt senden", "Send unencrypted")).Clicked() {
					a.composePlainWarn = false
					a.composePlainAck = true
					req := a.buildComposeReq(false)
					req.SendAt = a.composeWarnSendAt
					a.requestComposeSend(context.Background(), req)
				}
				if ui.Button(c.Key("cmp-plain-x"), "✕").Padding(2, 6).Clicked() {
					a.composePlainWarn = false
				}
			})
		}

		if a.composeMissingAttWarn != "" {
			ui.Row(c.Key("cmp-att-warn")).FillWidth().Padding(8, 10).Radius(8).Gap(8).AlignItems(ui.Center).
				Background(withAlpha(t.Warning, 0.16)).Border(1, withAlpha(t.Warning, 0.5)).Children(func() {
				ui.Text(c, a.trf("Anhang vergessen? Im Text steht „%s“, es ist aber keine Datei angehängt.", "Forgot attachment? Body mentions “%s”, but no file is attached.", a.composeMissingAttWarn)).
					FontSize(12).Bold().Grow(1).MinWidth(0)
				if ui.Button(c.Key("cmp-attw-pick"), a.tr("Datei wählen…", "Browse…")).Clicked() {
					a.composeMissingAttWarn = ""
					a.pickFile(FilePickRequest{
						Mode:  "open-multi",
						Title: a.tr("Anhänge auswählen", "Select Attachments"),
					}, func(paths []string) {
						for _, p := range paths {
							a.addComposeAttachment(p)
						}
					})
				}
				if ui.PrimaryButton(c.Key("cmp-attw-send"), a.tr("Trotzdem senden", "Send anyway")).Clicked() {
					a.composeMissingAttAck = true
					a.composeMissingAttWarn = ""
					req := a.buildComposeReq(false)
					req.SendAt = a.composeWarnSendAt
					a.requestComposeSend(context.Background(), req)
				}
				if ui.Button(c.Key("cmp-attw-x"), "✕").Padding(2, 6).Clicked() {
					a.composeMissingAttWarn = ""
				}
			})
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if inWindow {
				if ui.Button(c.Key("cmp-win-close"), a.tr("Schließen", "Close")).Clicked() {
					a.TryCloseComposeWindow()
				}
			}
			if a.composeAutoSaveBusy {
				ui.Text(c, a.tr("Speichere Entwurf…", "Saving draft…")).FontSize(11).TextColor(t.TextMuted)
			} else if !a.composeAutoSaveSavedAt.IsZero() {
				ui.Text(c, a.trf("Entwurf auto-gespeichert (%s)", "Draft auto-saved (%s)", a.composeAutoSaveSavedAt.Format("15:04"))).FontSize(11).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			if !a.readOnly {
				if ui.Button(c.Key("cmp-draft"), a.tr("Entwurf speichern", "Save Draft")).Clicked() {
					a.SaveDraft(context.Background(), a.buildComposeReq(false), a.composeDraftSavedMsg())
				}
				sendLabel := a.tr("Senden", "Send")
				if a.composeConfirm {
					sendLabel = a.tr("Wirklich senden?", "Confirm Send?")
				}
				if a.composeBusy {
					sendLabel = a.tr("Sende…", "Sending…")
				}
				// Only the engine keeps mails for later; without it there is no "send later".
				if a.isDirectMode() {
					a.renderSendLaterButton(c)
				}
				if ui.PrimaryButton(c.Key("cmp-send"), sendLabel).Tooltip(a.tr("Senden (⌘↩)", "Send (⌘↩)")).Clicked() && !a.composeBusy {
					if !a.composeConfirm {
						a.composeConfirm = true
					} else {
						a.requestComposeSend(context.Background(), a.buildComposeReq(false))
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
		CryptoTech:        [...]string{"", "smime", "pgp"}[min(max(a.composeCryptoTech, 0), 2)],
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
	case "activity":
		return a.tr("Aktivität", "Activity")
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
							"Neuer Kontakt im Footer erkannt — in dein PostDive-Adressbuch importieren?",
							"New contact detected in footer — import into your PostDive address book?",
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

func (a *App) renderOutboxModal(c *ui.Context) {
	if !a.outboxOpen {
		return
	}
	t := c.Theme()
	a.modal(c, &a.outboxOpen, func() {
		ui.Column(c).Width(a.modalW(620)).MaxHeight(a.modalH(c, 520)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, a.tr("Postausgang", "Outbox")).FontSize(16).Bold().Grow(1)
				if ui.Button(c.Key("ob-x"), "✕").Clicked() {
					a.outboxOpen = false
				}
			})
			if len(a.outboxItems) == 0 {
				ui.Text(c, a.tr("Der Postausgang ist leer.", "The Outbox is empty.")).FontSize(13).TextColor(t.TextMuted)
			} else {
				ui.Scroll(c.Key("ob-scroll")).FillWidth().MaxHeight(340).Children(func() {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						for _, it := range a.outboxItems {
							item := it
							ui.Column(c.Key("ob-item-"+item.ID)).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(4).Children(func() {
								ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
									subj := item.Subject
									if strings.TrimSpace(subj) == "" {
										subj = a.tr("(Kein Betreff)", "(No subject)")
									}
									ui.Text(c, subj).FontSize(13).Bold().Grow(1).SingleLine()
									statusLbl := a.tr("Wartend", "Queued")
									statusCol := t.Warning
									if item.Status == "failed" {
										statusLbl = a.tr("Fehlgeschlagen", "Failed")
										statusCol = t.Danger
									}
									if outboxScheduled(item, time.Now()) {
										ui.Text(c, a.tr("Geplant: ", "Scheduled: ")+a.formatSendAt(time.Unix(item.ScheduledAt, 0))).FontSize(11).TextColor(t.Accent).Bold().Shrink(0)
									} else {
										ui.Text(c, fmt.Sprintf("%s · %dx", statusLbl, item.Attempts)).FontSize(11).TextColor(statusCol).Bold().Shrink(0)
									}
								})
								ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
									ui.Spacer(c)
									if a.readOnly {
										return
									}
									if item.ScheduledAt > 0 {
										// A "send later" mail: back into the drafts rather than lost.
										if ui.Button(c.Key("ob-drafts-"+item.ID), a.tr("Zurück zu Entwürfen", "Back to Drafts")).Padding(3, 8).Disabled(a.outboxBusy).Clicked() {
											a.OutboxToDrafts(context.Background(), item.ID)
										}
									} else if ui.Button(c.Key("ob-del-"+item.ID), a.tr("Verwerfen", "Discard")).Padding(3, 8).TextColor(t.Danger).Clicked() {
										a.DeleteOutboxItem(context.Background(), item.ID)
									}
									if ui.Button(c.Key("ob-now-"+item.ID), a.tr("Jetzt senden", "Send now")).Padding(3, 8).Disabled(a.outboxBusy).Clicked() {
										a.OutboxSendNow(context.Background(), item.ID)
									}
								})
								ui.Text(c, a.tr("An: ", "To: ")+strings.Join(item.Recipients, ", ")).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
								if item.LastError != "" {
									ui.Text(c, item.LastError).FontSize(11).TextColor(t.Danger).MaxLines(2)
								}
							})
						}
					})
				})
			}
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("ob-close"), a.tr("Schließen", "Close")).Clicked() {
					a.outboxOpen = false
				}
				if _, onlyLater := a.outboxLabel(); len(a.outboxItems) > 0 && !onlyLater && !a.readOnly {
					lbl := a.tr("Alle jetzt senden", "Send All Now")
					if a.outboxBusy {
						lbl = a.tr("Sende…", "Sending…")
					}
					if ui.PrimaryButton(c.Key("ob-flush"), lbl).Disabled(a.outboxBusy).Clicked() {
						a.FlushOutbox(context.Background())
					}
				}
			})
		})
	})
}

func (a *App) renderQuickRuleModal(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.quickRuleOpen, func() {
		preview := a.QuickRulePreview()
		ui.Column(c).Width(a.modalW(740)).MaxHeight(a.modalH(c, 760)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
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
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Header enthält", "Header contains")).Width(130).FontSize(12).TextColor(t.TextMuted)
						ui.TextInput(c.Key("qr-hdr"), &a.quickRuleHeaderText).Placeholder(a.tr("optional, z. B. List-Id: jira, X-Mailer: Atlassian", "optional, e.g. List-Id: jira, X-Mailer: Atlassian")).Grow(1)
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
					if tf := a.quickRuleTarget; tf != nil {
						msg := a.trf("Ergänzt die bestehende Regel „%s“ um diese Bedingungen (ODER); ihre Aktionen bleiben.", "Adds these conditions to the existing rule “%s” (OR); its actions stay.", tf.Name)
						if a.quickRuleTargetMode == "replace" {
							msg = a.trf("Ersetzt die bestehende Regel „%s“ durch diese Regel.", "Replaces the existing rule “%s” with this rule.", tf.Name)
						} else if !quickRuleAppendable(backend.FilterUpsertRequest{MatchAll: a.quickRuleMatchAll, Conditions: a.quickRuleConditions()}) {
							msg = a.trf("Diese Bedingungen gelten nur zusammen (UND) und lassen sich nicht an „%s“ anhängen; es wird eine neue Regel angelegt.",
								"These conditions only apply together (AND) and can't be added to “%s”; a new rule is created.", tf.Name)
						}
						ui.Row(c.Key("qr-target")).FillWidth().Padding(8, 10).Gap(8).Radius(8).AlignItems(ui.Center).Wrap().Background(withAlpha(t.Warning, 0.12)).Border(1, withAlpha(t.Warning, 0.4)).Children(func() {
							ui.Text(c, msg).FontSize(11.5).Grow(1).MinWidth(160)
							if ui.Button(c.Key("qr-target-new"), a.tr("Stattdessen neue Regel", "Create a new rule instead")).Padding(3, 9).Clicked() {
								a.quickRuleTarget, a.quickRuleTargetMode = nil, ""
							}
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
							"Hinweis: Die Regel wird lokal in PostDive gespeichert, sofort auf die passenden Mails angewendet und künftig automatisch bei neu eintreffenden Posteingangs-Mails ausgeführt. Verwalten & Löschen unter Einstellungen → Konten → Regeln.",
							"Note: The rule is saved locally in PostDive, applied right away to matching emails, and executed automatically on newly arriving inbox mail. Manage or delete rules under Settings → Accounts → Rules.",
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
				if iconLabelBtn(c.Key("qr-open-tb"), iconFilter, a.tr("Filterregeln verwalten", "Manage Filter Rules"), false, a.tr("Gespeicherte PostDive-Regeln anzeigen und verwalten", "View and manage saved PostDive filter rules")).Clicked() {
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
	a.modal(c, &a.calDelOpen, func() {
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
			if ev.CanNotify {
				ui.Checkbox(c.Key("cd-notify"), &a.calDelNotify, a.trf("Absage an %d Teilnehmer senden", "Send a cancellation to %d attendees", len(ev.Attendees)))
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
	a.modal(c, &a.helpOpen, func() {
		ui.Column(c).Width(a.modalW(720)).MaxHeight(a.modalH(c, 760)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconHelp).Size(18, 18).TextColor(t.Accent)
				ui.Text(c, a.tr("Hilfe · Überblick, Filterregeln & KI-Transparenz", "Help · Overview, Filter Rules & AI Transparency")).FontSize(16).Bold().Grow(1)
				if v := a.versionTag(); v != "" {
					ui.Text(c, v).FontSize(10.5).TextColor(withAlpha(t.TextMuted, 0.72)).SingleLine()
				}
				if ui.Button(c.Key("help-x"), "✕").Clicked() {
					a.helpOpen = false
				}
			})

			// Current configuration status bar
			backendInfo := a.agentBackend
			if a.agentModel != "" {
				backendInfo += " · " + a.agentModel
			}
			provCfg := ai.ProviderForBackend(a.cfg.AI, a.agentBackend)
			isLocalModel := a.agentBackend == "ollama" || a.agentBackend == "llamacpp" || a.agentBackend == "llamafile" ||
				(a.agentBackend == "openaicompat" && isLoopbackEndpointURL(provCfg.Endpoint))
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
							a.tr("Was PostDive macht · Überblick", "What PostDive Does · Overview"),
							[]string{
								a.tr("• PostDive ist dein eigenständiger nativer E-Mail-, Kalender- & Kontakte-Client: direkte IMAP/SMTP-Anbindung mit Echtzeit-Sync (IDLE), Google Kalender & iCal (.ics), Adressbuch und optionale KI-Hilfe in einer schnellen Desktop-App.", "• PostDive is your standalone native email, calendar & contacts client: direct IMAP/SMTP with real-time sync (IDLE), Google Calendar & iCal (.ics), address book, and optional AI assistance in one fast desktop app."),
								a.tr("• Aufräumen statt manuell löschen: Wiederkehrende Mails per Multi-Select bündeln, mit dem Quick-Regel-Assistenten inklusive Preview prüfen und als automatische PostDive-Filterregel speichern.", "• Clean up instead of daily manual deletes: batch-select recurring emails, preview matches with the Quick-Rule Wizard, and automate them as PostDive filter rules."),
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
								a.tr("• Verwaltung: Alle gespeicherten Regeln findest du unter Einstellungen → Konten → Regeln (Pausieren, manuell Ausführen oder Löschen).", "• Management: View, pause, manually run, or delete all saved rules under Settings → Accounts → Rules."),
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
								a.tr("• Autocomplete im Verfassen-Dialog: Beim Tippen in „An:“ oder „CC:“ schlägt PostDive passende Kontakte und letzte Absender vor.", "• Compose autocomplete: typing in “To:” or “CC:” suggests matching contacts and recent senders."),
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
