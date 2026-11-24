package nativeui

import (
	"context"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/copilot"
)

func (a *App) renderCopilotPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().MinWidth(280).Background(t.Surface).Children(func() {
		// AI Assistant header + provider/model controls
		ui.Column(c).FillWidth().Padding(10, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, iconSparkles).Size(15, 15).TextColor(t.Accent)
				ui.Text(c, a.tr("KI-Assistent", "AI Assistant")).FontSize(13.5).Bold().Grow(1)
				if a.chatStreaming {
					ui.Spinner(c)
					if iconBtn(c.Key("cp-stop"), iconStop, a.tr("Generierung stoppen", "Stop generating"), t.Danger, false).Clicked() && a.chatCancel != nil {
						a.chatCancel()
					}
				} else if len(a.chatEntries) > 0 {
					if iconBtn(c.Key("cp-clear"), iconTrash, a.tr("Chat leeren", "Clear chat"), t.TextMuted, false).Clicked() {
						a.ClearChat()
					}
				}
			})
			a.renderCopilotControls(c, false)
		})
		a.renderCopilotChat(c, false)
	})
}

// renderCopilotControls shows the provider and model choice and the quick prompts (the phone's
// with the daily briefing, which the desktop has in its toolbar).
func (a *App) renderCopilotControls(c *ui.Context, phone bool) {
	t := c.Theme()
	cliHint, hasCLIHint := ai.CLIModelHints(a.agentBackend)
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		if len(a.providerLabels) > 0 {
			label := a.providerLabel
			if ui.Select(c.Key("cp-prov"), &label, a.providerLabels).Grow(1).MinWidth(0).Changed() {
				a.selectProvider(context.Background(), label)
			}
		}
		if len(a.modelChoices) > 0 {
			ui.Select(c.Key("cp-model"), &a.agentModel, a.modelChoices).Grow(1).MinWidth(0)
		} else {
			ph := a.tr("Modell…", "Model…")
			if hasCLIHint {
				ph = a.tr(cliHint.PlaceholderDE, cliHint.PlaceholderEN)
			}
			ui.TextInput(c.Key("cp-model-in"), &a.agentModel).Placeholder(ph).Grow(1).MinWidth(0)
		}
		if !hasCLIHint {
			ref := iconBtn(c.Key("cp-model-refresh"), iconRefresh, a.tr("Modelle neu laden", "Reload models"), ui.Color{}, false)
			if a.modelsLoading {
				ref.Disabled(true)
			}
			if ref.Clicked() && !a.modelsLoading {
				a.LoadModelsAsync(context.Background())
			}
		}
	})
	if hasCLIHint {
		a.renderCLIModelHintChips(c, "cp-cli-m", &a.agentModel, cliHint)
	}
	if a.modelsError != "" {
		ui.Text(c, a.tr("Modelle nicht ladbar: ", "Could not load models: ")+a.modelsError).FontSize(11).TextColor(t.Warning)
	}
	// Quick prompt chips
	ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
		if ui.Button(c.Key("qp-action"), a.tr("Action-Mails", "Action Mail")).Clicked() {
			a.SendCopilotPrompt(context.Background(), a.tr(
				"Welche ungelesenen Mails erfordern heute eine Antwort oder Entscheidung von mir?",
				"Which unread emails require a reply or decision from me today?",
			))
		}
		if ui.Button(c.Key("qp-free"), a.tr("Freie Slots", "Free Slots")).Clicked() {
			a.SendCopilotPrompt(context.Background(), a.tr(
				"Prüfe meinen Kalender für heute und morgen und nenne freie Zeitfenster.",
				"Check my calendar for today and tomorrow and list free time slots.",
			))
		}
		if ui.Button(c.Key("qp-followup"), a.tr("Follow-ups", "Follow-ups")).Clicked() {
			a.SendCopilotPrompt(context.Background(), a.tr(
				"Prüfe meine letzten Mails der vergangenen 7 Tage: Wo warte ich noch auf eine Rückmeldung oder sollte nachfassen? Liste konkrete offene Threads mit Empfänger und schlage bei Bedarf Nachfass-Entwürfe vor.",
				"Check my recent emails from the last 7 days: where am I still waiting for a reply or need to follow up? List open threads with recipients and offer follow-up drafts if helpful.",
			))
		}
		if phone {
			if ui.Button(c.Key("qp-brief"), a.tr("Tagesbriefing", "Daily briefing")).Clicked() {
				a.OpenDayBriefing(context.Background())
			}
		}
		if a.selectedMsg != nil {
			if ui.Button(c.Key("qp-sel"), a.tr("Diese Mail", "This Email")).Clicked() {
				a.SendCopilotPrompt(context.Background(), a.tr(
					"Fasse die aktuell ausgewählte Mail zusammen und schlage die nächste sinnvolle Aktion oder einen Antwort-Entwurf vor.",
					"Summarize the currently selected email and suggest the next best action or reply draft.",
				))
			}
		}
	})
}

// renderCopilotChat shows the conversation and the composer below it.
func (a *App) renderCopilotChat(c *ui.Context, phone bool) {
	t := c.Theme()
	// Streaming chat log
	if a.chatStreaming && a.widgets.chatScroll.Y >= a.widgets.chatScroll.MaxY-2 {
		a.widgets.chatScroll.Y = math.MaxFloat32
	}
	ui.Scroll(c.Key("cp-scroll")).TrackScroll(&a.widgets.chatScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(12).Gap(10).Children(func() {
			if len(a.chatEntries) == 0 {
				ui.Column(c).FillWidth().Padding(14).Radius(10).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
					ui.Text(c, a.tr("Direkter Live-Zugriff auf Mail & Kalender", "Direct Live Access to Mail & Calendar")).FontSize(12.5).Bold()
					ui.Text(c, a.tr(
						"Frage nach Postfach-Prioritäten, offenen Terminen, Antwort-Entwürfen oder starte ein Tages-Briefing. Schreibende Aktionen (Entwurf, Termin, Gelesen-Status) schlägt der Assistent nur vor — ausgeführt wird erst nach deinem Klick.",
						"Ask about inbox priorities, upcoming events, reply drafts, or start a daily briefing. Write actions (draft, event, read status) are only proposed — nothing runs until you click.",
					)).FontSize(12).TextColor(t.TextMuted)
				})
			}
			for _, entry := range a.chatEntries {
				e := entry
				if e.Role == "user" {
					ui.Row(c.Key(e.ID)).FillWidth().Children(func() {
						ui.Spacer(c)
						ui.Column(c).Padding(9, 12).Radius(10).Background(withAlpha(t.Accent, 0.22)).Border(1, withAlpha(t.Accent, 0.45)).Children(func() {
							ui.Text(c, e.Content).FontSize(12.5).Selectable()
						})
					})
					continue
				}
				ui.Column(c.Key(e.ID)).FillWidth().Padding(10, 12).Radius(10).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
					if len(e.Tools) > 0 {
						ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
							for _, tr := range e.Tools {
								lbl := "⚙ " + tr.Name
								bg, fg := withAlpha(t.Warning, 0.16), t.Warning
								if tr.Done {
									lbl = "✓ " + tr.Name
									if tr.Summary != "" {
										lbl += ": " + tr.Summary
									}
									bg, fg = withAlpha(t.Success, 0.16), t.Success
								}
								pillBadge(c, lbl, bg, fg)
							}
						})
					}
					if e.Status != "" {
						ui.Text(c, e.Status).FontSize(11.5).TextColor(t.TextMuted)
					}
					if e.Error != "" {
						ui.Text(c, a.tr("Fehler: ", "Error: ")+e.Error).FontSize(12).TextColor(t.Danger)
					}
					if e.Content != "" {
						opts := a.chatMarkdownOpts(e.ID, e.Streaming)
						if e.Streaming {
							markdown.Stream(c, e.Content, opts)
						} else {
							markdown.View(c, e.Content, opts)
						}
					}
					for _, id := range e.ActionIDs {
						if pa := a.pendingActions[id]; pa != nil {
							a.renderActionCard(c, id, pa)
						}
					}
				})
			}
		})
	})

	// Composer input at bottom
	ui.Divider(c)
	ui.Column(c).FillWidth().Padding(10, 12).Gap(8).Children(func() {
		ph := a.tr("Frage deinen KI-Assistenten (⌘Enter zum Senden)…", "Ask your AI Assistant (⌘Enter to send)…")
		if phone {
			ph = a.tr("Frage deinen KI-Assistenten…", "Ask your AI Assistant…")
		}
		ta := ui.TextArea(c.Key("cp-input"), &a.chatInput).Placeholder(ph).FillWidth().Height(68)
		if a.focusChat {
			ta.Focus()
			a.focusChat = false
		}
		if ta.Shortcut(ui.Cmd, ui.KeyEnter) {
			a.SendCopilotPrompt(context.Background(), a.chatInput)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.agentBackend).FontSize(11).TextColor(t.TextMuted).Grow(1).SingleLine()
			btn := iconLabelBtn(c.Key("cp-send"), iconSend, a.tr("Senden", "Send"), true, a.tr("Nachricht senden (⌘Enter)", "Send message (⌘Enter)"))
			if a.chatStreaming {
				btn.Disabled(true)
			}
			if btn.Clicked() {
				a.SendCopilotPrompt(context.Background(), a.chatInput)
			}
		})
	})
}

// renderActionCard shows a proposed write action with every detail and Execute/Discard buttons.
func (a *App) renderActionCard(c *ui.Context, id string, pa *pendingAction) {
	t := c.Theme()
	act := pa.Action
	border := t.Warning
	switch pa.Status {
	case actionDone:
		border = t.Success
	case actionDiscarded:
		border = t.Border
	case actionFailed:
		border = t.Danger
	}
	row := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, label).FontSize(11.5).Bold().TextColor(t.TextMuted).Width(78).Shrink(0)
			ui.Text(c, value).FontSize(11.5).Grow(1).MinWidth(0).Selectable()
		})
	}
	ui.Column(c.Key("pa-"+id)).FillWidth().Padding(10, 12).Radius(8).Background(t.Surface).Border(1.2, border).Gap(6).Children(func() {
		var title string
		switch act.Kind {
		case copilot.ActionDraft:
			title = a.tr("Vorschlag: E-Mail-Entwurf speichern", "Proposal: save email draft")
		case copilot.ActionCalendarEvent:
			title = a.tr("Vorschlag: Termin anlegen", "Proposal: create calendar event")
		case copilot.ActionMarkRead:
			if act.MarkRead != nil && !act.MarkRead.Read {
				title = a.tr("Vorschlag: Als ungelesen markieren", "Proposal: mark as unread")
			} else {
				title = a.tr("Vorschlag: Als gelesen markieren", "Proposal: mark as read")
			}
		case copilot.ActionMove:
			if act.Move != nil && act.Move.Archive {
				title = a.tr("Vorschlag: Archivieren", "Proposal: archive emails")
			} else if act.Move != nil && act.Move.FolderPath != "" {
				title = a.trf("Vorschlag: Verschieben nach „%s“", "Proposal: move to “%s”", act.Move.FolderPath)
			} else {
				title = a.tr("Vorschlag: Verschieben", "Proposal: move emails")
			}
		case copilot.ActionFlag:
			if act.Flag != nil && !act.Flag.Flagged {
				title = a.tr("Vorschlag: Stern entfernen", "Proposal: remove star")
			} else {
				title = a.tr("Vorschlag: Mit Stern markieren", "Proposal: star emails")
			}
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconShieldChk).Size(13, 13).TextColor(border)
			ui.Text(c, title).FontSize(12.5).Bold().Grow(1)
			switch pa.Status {
			case actionPending:
				pillBadge(c, a.tr("Wartet auf Bestätigung", "Awaiting confirmation"), withAlpha(t.Warning, 0.18), t.Warning)
			case actionRunning:
				ui.Spinner(c)
			case actionDone:
				pillBadge(c, a.tr("Ausgeführt", "Executed"), withAlpha(t.Success, 0.18), t.Success)
			case actionDiscarded:
				pillBadge(c, a.tr("Verworfen", "Discarded"), t.Background, t.TextMuted)
			case actionFailed:
				pillBadge(c, a.tr("Fehler", "Failed"), withAlpha(t.Danger, 0.18), t.Danger)
			}
		})

		switch act.Kind {
		case copilot.ActionDraft:
			if d := act.Draft; d != nil {
				if d.ReplyToMessageID != "" {
					kind := a.tr("Antwort auf", "Reply to")
					switch d.ReplyType {
					case "replyAll":
						kind = a.tr("Allen antworten", "Reply all")
					case "forward":
						kind = a.tr("Weiterleiten", "Forward")
					}
					row(kind+":", strings.TrimSpace(d.ReplyToSubject+" · "+d.ReplyToFrom))
				}
				to := strings.Join(d.To, ", ")
				if to == "" && d.ReplyToMessageID != "" {
					to = a.tr("(Empfänger der Original-Mail)", "(recipients of the original email)")
				}
				row(a.tr("An:", "To:"), to)
				row("CC:", strings.Join(d.CC, ", "))
				row(a.tr("Betreff:", "Subject:"), d.Subject)
				ui.Column(c).FillWidth().Padding(8).Radius(6).Background(t.Background).Border(1, t.Border).Children(func() {
					ui.Text(c, truncateText(d.Body, 700)).FontSize(11.5).Selectable()
				})
				ui.Text(c, a.tr("Wird im Entwürfe-Ordner gespeichert — nicht gesendet.", "Saved to Drafts folder — not sent.")).FontSize(11).TextColor(t.TextMuted)
			}
		case copilot.ActionCalendarEvent:
			if ev := act.Event; ev != nil {
				row(a.tr("Titel:", "Title:"), ev.Title)
				when := formatDateTimeLang(ev.Start, a.uiLang())
				if ev.AllDay {
					when = ev.Start + " · " + a.tr("ganztägig", "all day")
				} else if ev.End != "" {
					when += " – " + formatEventRangeLang(false, ev.End, "", a.uiLang())
				}
				row(a.tr("Wann:", "When:"), when)
				row(a.tr("Ort:", "Location:"), ev.Location)
				row(a.tr("Notiz:", "Notes:"), truncateText(ev.Description, 300))
				if len(a.eventCalLabels) > 0 && (pa.Status == actionPending || pa.Status == actionFailed) {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.tr("Kalender:", "Calendar:")).FontSize(11.5).Bold().TextColor(t.TextMuted).Width(78).Shrink(0)
						ui.Select(c.Key("pa-cal-"+id), &pa.CalendarLabel, a.eventCalLabels).Grow(1)
					})
				} else {
					row(a.tr("Kalender:", "Calendar:"), pa.CalendarLabel)
				}
			}
		case copilot.ActionMarkRead, copilot.ActionMove, copilot.ActionFlag:
			var targets []copilot.MarkReadTarget
			if act.Kind == copilot.ActionMarkRead && act.MarkRead != nil {
				targets = act.MarkRead.Targets
			} else if act.Kind == copilot.ActionMove && act.Move != nil {
				targets = act.Move.Targets
			} else if act.Kind == copilot.ActionFlag && act.Flag != nil {
				targets = act.Flag.Targets
			}
			if len(targets) > 0 {
				maxShow := min(len(targets), 5)
				for i := 0; i < maxShow; i++ {
					tg := targets[i]
					ui.Row(c).FillWidth().Gap(6).Children(func() {
						ui.Text(c, "•").FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, truncateText(shortSender(tg.From), 24)).FontSize(11.5).Bold().Shrink(0)
						ui.Text(c, truncateText(tg.Subject, 60)).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Grow(1)
					})
				}
				if len(targets) > maxShow {
					ui.Text(c, a.trf("… und %d weitere", "… and %d more", len(targets)-maxShow)).FontSize(11).TextColor(t.TextMuted)
				}
			}
		}

		if pa.Err != "" {
			ui.Text(c, pa.Err).FontSize(11.5).TextColor(t.Danger)
		}
		if pa.Result != "" {
			ui.Text(c, pa.Result).FontSize(11.5).TextColor(t.Success)
		}
		if pa.Status == actionPending || pa.Status == actionFailed {
			blocked := a.actionBlockedReason()
			if blocked != "" {
				ui.Text(c, blocked).FontSize(11).TextColor(t.Warning)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("pa-discard-"+id), a.tr("Verwerfen", "Discard")).Clicked() {
					a.DiscardCopilotAction(id)
				}
				exec := iconLabelBtn(c.Key("pa-exec-"+id), iconCheck, a.tr("Ausführen", "Execute"), true, a.tr("Aktion jetzt ausführen", "Run this action now"))
				if blocked != "" {
					exec.Disabled(true)
				}
				if exec.Clicked() && blocked == "" {
					a.ExecuteCopilotAction(context.Background(), id)
				}
			})
		}
	})
}

// renderCLIModelHintChips shows clickable model suggestions (aliases + full IDs) and a short
// naming guide for CLI backends so users never have to guess valid --model / -m values.
func (a *App) renderCLIModelHintChips(c *ui.Context, keyPrefix string, model *string, hint ai.CLIModelHint) {
	t := c.Theme()
	cur := strings.TrimSpace(*model)
	ui.Column(c.Key(keyPrefix + "-box")).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(4).Wrap().AlignItems(ui.Center).Children(func() {
			defBtn := ui.Button(c.Key(keyPrefix+"-def"), a.tr("Standard", "Default")).Padding(2, 7)
			if cur == "" {
				defBtn.Background(withAlpha(t.Accent, 0.2)).Border(1, t.Accent)
			}
			if defBtn.Clicked() {
				*model = ""
			}
			for _, m := range hint.Models {
				mCopy := m
				btn := ui.Button(c.Key(keyPrefix+"-"+mCopy), mCopy).Padding(2, 7)
				if strings.EqualFold(cur, mCopy) {
					btn.Background(withAlpha(t.Accent, 0.2)).Border(1, t.Accent)
				}
				if btn.Clicked() {
					*model = mCopy
				}
			}
		})
		ui.Text(c, a.tr(hint.HintDE, hint.HintEN)).FontSize(10.5).TextColor(t.TextMuted)
	})
}
