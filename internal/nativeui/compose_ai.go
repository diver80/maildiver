package nativeui

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

// composeTailStart returns the index of the first line of what follows the user's own text in a
// plain-text compose body: the signature ("-- "), a quoted reply or a forwarded block.
func composeTailStart(lines []string) int {
	for i, ln := range lines {
		tr := strings.TrimSpace(ln)
		if tr == "--" ||
			strings.HasPrefix(tr, ">") ||
			strings.HasPrefix(tr, "---------- ") ||
			(strings.HasPrefix(tr, "Am ") && strings.Contains(tr, " schrieb ")) ||
			(strings.HasPrefix(tr, "On ") && strings.Contains(tr, " wrote:")) {
			return i
		}
	}
	return len(lines)
}

// insertComposeSnippet adds snippet at the end of the user's own text: above the signature, the
// quoted original or a forwarded mail, not below them.
func insertComposeSnippet(body, snippet string) string {
	lines := strings.Split(body, "\n")
	splitIdx := composeTailStart(lines)
	if splitIdx == len(lines) {
		if body != "" && !strings.HasSuffix(body, "\n") && !strings.HasPrefix(snippet, "\n") {
			return body + " " + snippet
		}
		return body + snippet
	}
	cut := len(strings.Join(lines[:splitIdx], "\n"))
	if splitIdx > 0 {
		cut++ // the line break before the tail
	}
	head, tail := body[:cut], body[cut:]
	user := strings.TrimRight(head, " \t\r\n")
	gap := head[len(user):]
	if !strings.Contains(gap, "\n") {
		gap = "\n\n" + strings.TrimLeft(gap, " \t")
	}
	if user != "" && !strings.HasPrefix(snippet, "\n") {
		if strings.Contains(snippet, "\n") {
			user += "\n\n" // a paragraph of its own
		} else {
			user += " "
		}
	}
	return user + strings.TrimRight(snippet, "\n") + gap + tail
}

// insertComposeSnippetAt inserts snippet at rune offset caret of body and returns the new body
// and the caret after the snippet.
func insertComposeSnippetAt(body, snippet string, caret int) (string, int) {
	r := []rune(body)
	caret = min(max(caret, 0), len(r))
	return string(r[:caret]) + snippet + string(r[caret:]), caret + len([]rune(snippet))
}

// insertComposeText inserts text into the plain-text compose body (UI thread): at the caret the
// user left in the text field, else at the end of the own text above signature and quote.
func (a *App) insertComposeText(text string) {
	if a.composeCaretOK && a.composeCaretBody == a.composeBody {
		body, caret := insertComposeSnippetAt(a.composeBody, text, a.composeCaret)
		a.composeBody = body
		a.composeCaret, a.composeCaretBody = caret, body
		a.composeCaretApply = true
	} else {
		a.composeBody = insertComposeSnippet(a.composeBody, text)
	}
	a.composeDirtyAt = time.Now()
}

// replaceComposeUserText replaces only the user-authored top portion of a plain-text compose
// body while preserving trailing signatures ("-- "), quoted replies, and forwarded blocks.
func replaceComposeUserText(body, nextUserText string) string {
	norm := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(norm, "\n")
	splitIdx := composeTailStart(lines)
	next := strings.TrimSpace(strings.ReplaceAll(nextUserText, "\r\n", "\n"))
	tail := ""
	if splitIdx < len(lines) {
		tail = strings.TrimLeft(strings.Join(lines[splitIdx:], "\n"), "\n")
	}
	if tail == "" {
		return next
	}
	if next == "" {
		return "\n\n" + tail
	}
	return next + "\n\n" + tail
}

func (a *App) resetComposeAIState() {
	a.composeAISeq++
	a.composeAIOpen = false
	a.composeAIPrompt = ""
	a.composeAILoading = false
	a.composeAIMode = ""
	a.composeAIResult = ""
	a.composeAISubject = ""
	a.composeAIReview = nil
	a.composeAIError = ""
	a.composeAIBeforeUndo = ""
	a.composeAIUndoReady = false
}

// RunComposeAI invokes PostDive's configured AI provider (local or cloud) directly from the
// open Compose/Reply window to draft, improve, shorten, formalize, translate, or review the mail.
func (a *App) RunComposeAI(ctx context.Context, mode, tone string) {
	if a.composeAILoading {
		return
	}
	a.composeAIOpen = true
	a.composeAISeq++
	seq := a.composeAISeq
	a.composeAILoading = true
	a.composeAIError = ""
	a.composeAIResult = ""
	a.composeAISubject = ""
	a.composeAIReview = nil

	aiBackend, model := a.agentBackend, a.agentModel
	lang := a.uiLang()
	prompt := strings.TrimSpace(a.composeAIPrompt)
	subject := strings.TrimSpace(a.composeSubject)
	plainFallback := a.composeBody
	useWYSIWYG := a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil

	var srcCopy *backend.MessageDetail
	if a.composeSourceMsg != nil {
		cp := *a.composeSourceMsg
		srcCopy = &cp
	}
	convSize := 0
	if srcCopy != nil && srcCopy.ID != "" {
		convSize = a.conversationSize(srcCopy.ID)
	}
	attNames := make([]string, 0, len(a.composeAttachments)+len(a.composeSourceAttachments))
	for _, sa := range a.composeSourceAttachments {
		if n := strings.TrimSpace(sa.Name); n != "" {
			attNames = append(attNames, n)
		}
	}
	for _, p := range a.composeAttachments {
		if n := strings.TrimSpace(filepath.Base(p)); n != "" {
			attNames = append(attNames, n)
		}
	}
	bc, _ := a.client()
	hiddenCals := a.hiddenCalendarsCopy()

	go func() {
		rawDraft := plainFallback
		if useWYSIWYG {
			rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
			_, edPlain, ok := a.composeEditor.ReadContent(rctx)
			rcancel()
			if ok && strings.TrimSpace(edPlain) != "" {
				rawDraft = edPlain
			}
		}
		userDraft := extractComposeUserTextOnly(rawDraft)

		effMode := strings.TrimSpace(mode)
		if effMode == "" {
			switch {
			case srcCopy != nil && userDraft == "":
				effMode = "draft_reply"
			case userDraft != "":
				effMode = "improve_draft"
			default:
				effMode = "compose_new"
			}
		}
		// If the user clicked Improve/Translate/Review on an empty draft without a prompt, guide gently.
		if (effMode == "improve_draft" || effMode == "translate_draft" || effMode == "review_draft") && userDraft == "" {
			if srcCopy != nil && effMode == "improve_draft" {
				effMode = "draft_reply"
			} else {
				a.emitUpdate(func() {
					if seq != a.composeAISeq {
						return
					}
					a.composeAILoading = false
					a.composeAIError = a.tr(
						"Bitte zuerst etwas Text im Entwurf schreiben (oder „Antwort entwerfen“ / eine Anweisung nutzen).",
						"Please type some draft text first (or use “Draft reply” / enter an instruction).",
					)
				})
				return
			}
		}
		if effMode == "compose_new" && prompt == "" && userDraft == "" && subject == "" {
			a.emitUpdate(func() {
				if seq != a.composeAISeq {
					return
				}
				a.composeAILoading = false
				a.composeAIError = a.tr(
					"Bitte kurz angeben, worum die E-Mail gehen soll.",
					"Please enter a brief note on what the email should say.",
				)
			})
			return
		}

		req := ai.AnalyzeRequest{
			Backend:          aiBackend,
			Model:            model,
			Mode:             effMode,
			Language:         lang,
			Question:         prompt,
			UserDraft:        userDraft,
			DraftAttachments: attNames,
			Tone:             tone,
		}
		if srcCopy != nil {
			bodyText := strings.TrimSpace(srcCopy.BodyText)
			if bodyText == "" && strings.TrimSpace(srcCopy.BodyHtml) != "" {
				if md, _ := htmlMailMarkdown(srcCopy.BodyHtml); strings.TrimSpace(md) != "" {
					bodyText = strings.TrimSpace(md)
				}
			}
			req.PrimaryMessage = triage.MessageInput{
				ID:       srcCopy.ID,
				Subject:  srcCopy.Subject,
				From:     srcCopy.From,
				To:       srcCopy.To,
				Date:     srcCopy.Date,
				BodyText: bodyText,
			}
			if effMode == "draft_reply" || effMode == "review_draft" {
				req.ThreadMessages = threadForAI(ctx, bc, srcCopy.ID, convSize)
			}
			if effMode == "draft_reply" {
				req.CalendarHint = calendarHintForAI(ctx, bc, hiddenCals, time.Now())
			}
		} else if subject != "" {
			req.PrimaryMessage = triage.MessageInput{Subject: subject}
		}

		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
		defer cancel()
		var res ai.AnalyzeResponse
		var err error
		if bc != nil {
			res, err = bc.AIAnalyze(c, req)
		} else {
			cfg, cerr := a.opts.ConfigLoader()
			if cerr != nil {
				cfg = config.Default()
			}
			prov := ai.ProviderForBackend(cfg.AI, aiBackend)
			if model != "" {
				prov.Model = model
			}
			cfg.AI.Agent = prov
			res, err = ai.Analyze(c, cfg.AI, req)
		}
		if err != nil {
			a.emitUpdate(func() {
				if seq != a.composeAISeq {
					return
				}
				a.composeAILoading = false
				a.composeAIError = a.errText(err)
			})
			return
		}

		// Only a draft_reply proposal is mail text; other kinds (move_folder, …) have no place
		// in the compose window and are left out.
		draftBody := strings.TrimSpace(res.Answer)
		draftSubj := ""
		if p, ok := res.DraftProposal(); ok {
			draftBody = strings.TrimSpace(p.Body)
			draftSubj = strings.TrimSpace(p.Subject)
		}
		a.emitUpdate(func() {
			if seq != a.composeAISeq || !a.composeOpen {
				return
			}
			a.composeAILoading = false
			a.composeAIMode = effMode
			a.composeAIResult = draftBody
			a.composeAISubject = draftSubj
			a.composeAIReview = res.ReviewIssues
		})
	}()
}

func (a *App) applyComposeAIResult(replace bool) {
	text := strings.TrimSpace(a.composeAIResult)
	if text == "" {
		return
	}
	prevUser := extractComposeUserTextOnly(a.composeBody)
	if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
		// Snapshot current plain text synchronously if possible so Undo can restore it.
		rctx, rcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, edPlain, ok := a.composeEditor.ReadContent(rctx)
		rcancel()
		if ok {
			prevUser = extractComposeUserTextOnly(edPlain)
		}
	}
	a.composeAIBeforeUndo = prevUser
	a.composeAIUndoReady = true

	if replace {
		a.composeBody = replaceComposeUserText(a.composeBody, text)
		if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
			a.composeEditor.SetUserHTML(plainBlockToEditableHTML(text))
			a.NotifyComposeDirty()
		}
	} else {
		if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
			a.composeEditor.InsertHTML(plainBlockToEditableHTML(text))
			a.NotifyComposeDirty()
		} else {
			a.insertComposeText(text)
		}
	}
	if a.composeAISubject != "" && (strings.TrimSpace(a.composeSubject) == "" || a.composeAIMode == "translate_draft") {
		a.composeSubject = a.composeAISubject
	}
	a.composeDirtyAt = time.Now()
	a.composeAIResult = ""
	a.composeAISubject = ""
	a.composeAIReview = nil
	a.composeAIError = ""
}

func (a *App) undoComposeAIApply() {
	if !a.composeAIUndoReady {
		return
	}
	prev := a.composeAIBeforeUndo
	a.composeAIBeforeUndo = ""
	a.composeAIUndoReady = false
	a.composeBody = replaceComposeUserText(a.composeBody, prev)
	if a.composeUsingWYSIWYG() && a.composeEditor != nil && a.composeEditor.Widget() != nil {
		a.composeEditor.SetUserHTML(plainBlockToEditableHTML(prev))
		a.NotifyComposeDirty()
	}
	a.composeDirtyAt = time.Now()
}

func (a *App) renderComposeAIAssist(c *ui.Context) {
	if !a.composeAIOpen {
		return
	}
	t := c.Theme()
	hasReplySource := a.composeSourceMsg != nil || a.composeReplyTo != ""
	modelHint := strings.TrimSpace(a.agentModel)
	if modelHint == "" {
		modelHint = strings.TrimSpace(a.agentBackend)
	}

	ui.Column(c.Key("cmp-ai-box")).FillWidth().Padding(8, 10).Radius(8).
		Background(withAlpha(t.Accent, 0.08)).Border(1, withAlpha(t.Accent, 0.40)).Gap(6).Children(func() {
		// Header row: title + active model badge + optional Undo + Close
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconSparkles).Size(13, 13).TextColor(t.Accent).Shrink(0)
			ui.Text(c, a.tr("KI-Hilfe beim Schreiben", "AI Writing Assistant")).FontSize(12).Bold().TextColor(t.Accent)
			if modelHint != "" {
				pillBadge(c, truncateText(modelHint, 32), withAlpha(t.Accent, 0.14), t.TextMuted)
			}
			ui.Spacer(c)
			if a.composeAIUndoReady && !a.composeAILoading {
				if ui.Button(c.Key("cmp-ai-undo"), a.tr("↶ KI-Text rückgängig", "↶ Undo AI text")).Padding(2, 7).Clicked() {
					a.undoComposeAIApply()
				}
			}
			if ui.Button(c.Key("cmp-ai-close"), "✕").Padding(2, 6).Clicked() {
				a.composeAIOpen = false
			}
		})

		// Prompt input + Generate button
		ph := a.tr("Worum geht es? Stichworte reichen, ⌘↩ generiert (z. B. „Termin auf Donnerstag 10 Uhr verschieben“)…", "What should this email say? Notes are enough, ⌘↩ generates (e.g. “reschedule to Thursday 10am”)…")
		if hasReplySource {
			ph = a.tr("Anweisung oder Stichworte für die KI, ⌘↩ generiert (z. B. „kurz zusagen, ab 15 Uhr passt“)…", "Instruction or notes for the AI, ⌘↩ generates (e.g. “accept briefly, free after 3pm”)…")
		}
		// Multi-line, growing with the notes: Return breaks the line, ⌘↩ generates.
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			in := ui.TextArea(c.Key("cmp-ai-prompt"), &a.composeAIPrompt).Placeholder(ph).Grow(1).MinWidth(0).
				MinHeight(0).Lines(1, 10)
			goLabel := a.tr("Generieren", "Generate")
			if a.composeAILoading {
				goLabel = a.tr("Läuft…", "Working…")
			}
			goClicked := ui.PrimaryButton(c.Key("cmp-ai-go"), goLabel).Tooltip(a.tr("Generieren (⌘↩)", "Generate (⌘↩)")).Clicked()
			if (goClicked || in.Shortcut(ui.Cmd, ui.KeyEnter)) && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "", "")
			}
		})

		// 1-click quick actions
		ui.Row(c).FillWidth().Gap(5).Wrap().AlignItems(ui.Center).Children(func() {
			if hasReplySource {
				if ui.Button(c.Key("cmp-ai-reply"), a.tr("Antwort entwerfen", "Draft Reply")).Padding(3, 8).Clicked() && !a.composeAILoading {
					a.RunComposeAI(context.Background(), "draft_reply", "")
				}
			}
			if ui.Button(c.Key("cmp-ai-improve"), a.tr("Verbessern", "Polish")).Padding(3, 8).Clicked() && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "improve_draft", "")
			}
			if ui.Button(c.Key("cmp-ai-short"), a.tr("Kürzer", "Shorter")).Padding(3, 8).Clicked() && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "improve_draft", "kurz")
			}
			if ui.Button(c.Key("cmp-ai-formal"), a.tr("Formeller", "More Formal")).Padding(3, 8).Clicked() && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "improve_draft", "formal")
			}
			if ui.Button(c.Key("cmp-ai-trans"), "DE ⇄ EN").Padding(3, 8).Tooltip(a.tr("Entwurf zwischen Deutsch und Englisch übersetzen", "Translate draft between German and English")).Clicked() && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "translate_draft", "")
			}
			if ui.Button(c.Key("cmp-ai-review"), a.tr("Prüfen", "Review")).Padding(3, 8).Tooltip(a.tr("Antwort auf fehlende Punkte, Tonfall & vergessene Anhänge prüfen", "Check reply for missed questions, tone & forgotten attachments")).Clicked() && !a.composeAILoading {
				a.RunComposeAI(context.Background(), "review_draft", "")
			}
		})

		if a.composeAILoading {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(4, 0).Children(func() {
				ui.Spinner(c)
				ui.Text(c, a.tr("KI formuliert Vorschlag…", "AI is drafting suggestion…")).FontSize(11.5).TextColor(t.TextMuted)
			})
		}

		if a.composeAIError != "" {
			ui.Text(c, a.composeAIError).FontSize(11.5).TextColor(t.Danger)
		}

		if len(a.composeAIReview) > 0 {
			ui.Column(c.Key("cmp-ai-review-box")).FillWidth().Padding(6, 8).Radius(6).
				Background(withAlpha(t.Warning, 0.12)).Border(1, withAlpha(t.Warning, 0.40)).Gap(3).Children(func() {
				ui.Text(c, a.tr("KI-Prüfhinweise:", "AI review notes:")).FontSize(11.5).Bold().TextColor(t.Warning)
				for _, issue := range a.composeAIReview {
					ui.Text(c, "• "+issue).FontSize(11.5).TextColor(t.Text)
				}
			})
		}

		if a.composeAIResult != "" {
			ui.Column(c.Key("cmp-ai-preview")).FillWidth().Padding(8, 10).Radius(6).
				Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				if a.composeAISubject != "" {
					ui.Text(c, a.tr("Betreff: ", "Subject: ")+a.composeAISubject).FontSize(11.5).Bold().TextColor(t.Accent).Selectable()
				}
				ui.Scroll(c.Key("cmp-ai-prev-scroll")).FillWidth().MaxHeight(140).Children(func() {
					ui.Text(c, a.composeAIResult).FontSize(12).TextColor(t.Text).Selectable()
				})
				ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c.Key("cmp-ai-apply"), a.tr("Übernehmen", "Use Draft")).Tooltip(a.tr("Ersetzt deinen Entwurfstext (Signatur & Zitat bleiben erhalten)", "Replaces your draft text (keeps signature & reply quote)")).Clicked() {
						a.applyComposeAIResult(true)
					}
					if ui.Button(c.Key("cmp-ai-insert"), a.tr("Einfügen", "Insert")).Tooltip(a.tr("Fügt den Text zusätzlich an der Cursorposition ein", "Inserts text without replacing existing draft")).Clicked() {
						a.applyComposeAIResult(false)
					}
					ui.Spacer(c)
					if ui.Button(c.Key("cmp-ai-discard"), a.tr("Verwerfen", "Dismiss")).Clicked() {
						a.composeAIResult = ""
						a.composeAISubject = ""
						a.composeAIReview = nil
					}
				})
			})
		}
	})
}
