package nativeui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/triage"
)

// "KI: Auswahl zusammenfassen": the summarize_selection mode over the checked mails, sent as
// the thread the way the conversation summary sends one. It is an explicit action, but it never
// decrypts: encrypted mails go in with their headers only, and the result says how many.

// maxSelectionSummary caps how many checked mails one summary reads.
const maxSelectionSummary = 30

type selectionSummary struct {
	open      bool
	loading   bool
	seq       uint64
	count     int // mails summarized
	skipped   int // checked mails over maxSelectionSummary, left out
	encrypted int // encrypted mails sent without their content
	result    string
	err       string
	md        markdown.Options
}

// detailIsEncrypted reports whether a mail read without decrypting is encrypted (also when the
// engine has no keys set up and the ciphertext came through as text).
func detailIsEncrypted(d backend.MessageDetail) bool {
	if d.Security.Encrypted {
		return true
	}
	if strings.Contains(d.BodyText, "-----BEGIN PGP MESSAGE-----") {
		return true
	}
	for k, v := range d.Headers {
		if strings.EqualFold(k, "content-type") {
			ct := strings.ToLower(v)
			if strings.Contains(ct, "multipart/encrypted") || strings.Contains(ct, "pkcs7-mime") {
				return true
			}
		}
	}
	return false
}

// selectionSummaryInputs turns the checked mails into the AI's thread, oldest first. A mail
// whose detail is missing goes in with its header; an encrypted one without its body.
func selectionSummaryInputs(hdrs []backend.MessageHeader, details map[string]backend.MessageDetail) (inputs []triage.MessageInput, encrypted int) {
	for i := len(hdrs) - 1; i >= 0; i-- { // lists are newest first
		h := hdrs[i]
		in := messageHeaderToInput(h)
		if d, ok := details[h.ID]; ok {
			if detailIsEncrypted(d) {
				in.Encrypted = true
				encrypted++
			} else {
				in.BodyText = d.BodyText
			}
		}
		inputs = append(inputs, in)
	}
	return inputs, encrypted
}

// RunSelectionSummary summarizes the checked mails with the AI (UI thread).
func (a *App) RunSelectionSummary(ctx context.Context) {
	hdrs := a.CheckedMessages()
	if len(hdrs) == 0 || a.selSum.loading {
		return
	}
	bc, err := a.client()
	if err != nil {
		a.toast(a.tr("KI nicht verfügbar: ", "AI not available: ")+a.errText(err), "error")
		return
	}
	skipped := 0
	if len(hdrs) > maxSelectionSummary {
		skipped = len(hdrs) - maxSelectionSummary
		hdrs = hdrs[:maxSelectionSummary]
	}
	a.selSum.seq++
	seq := a.selSum.seq
	a.selSum.open, a.selSum.loading = true, true
	a.selSum.count, a.selSum.skipped, a.selSum.encrypted = len(hdrs), skipped, 0
	a.selSum.result, a.selSum.err = "", ""
	req := ai.AnalyzeRequest{
		Backend:  a.agentBackend,
		Model:    a.agentModel,
		Mode:     "summarize_selection",
		Language: a.uiLang(),
	}
	go func() {
		details := make(map[string]backend.MessageDetail, len(hdrs))
		for _, h := range hdrs {
			// Decrypt stays off: an automation-like batch read must never decrypt.
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			d, derr := bc.MessageWithOptions(c, h.ID, backend.MessageOptions{Format: "text"})
			cancel()
			if derr == nil {
				details[h.ID] = d
			}
		}
		inputs, enc := selectionSummaryInputs(hdrs, details)
		req.ThreadMessages = inputs
		if len(inputs) > 0 {
			req.PrimaryMessage = inputs[len(inputs)-1]
		}
		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
		res, aerr := bc.AIAnalyze(c, req)
		cancel()
		a.emitUpdate(func() {
			if seq != a.selSum.seq {
				return
			}
			a.selSum.loading = false
			a.selSum.encrypted = enc
			if aerr != nil {
				a.selSum.err = a.errText(aerr)
				return
			}
			a.selSum.result = res.Answer
		})
	}()
}

// closeSelectionSummary hides the summary and drops a request still running.
func (a *App) closeSelectionSummary() {
	s := &a.selSum
	s.seq++ // a reply still on its way is dropped
	s.open, s.loading = false, false
	s.count, s.skipped, s.encrypted = 0, 0, 0
	s.result, s.err = "", ""
}

// renderSelectionSummaryCard shows the summary as the reading pane shows AI results.
func (a *App) renderSelectionSummaryCard(c *ui.Context) {
	if !a.selSum.open {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("selsum-card")).FillWidth().Padding(14).Radius(10).Border(1, t.Accent).Background(t.Surface).Gap(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconSparkles).Size(14, 14).TextColor(t.Accent).Shrink(0)
			ui.Text(c, a.trf("KI: Zusammenfassung von %d Mails", "AI: Summary of %d emails", a.selSum.count)).FontSize(12.5).Bold().TextColor(t.Accent).Grow(1).MinWidth(0)
			if a.selSum.loading {
				ui.Spinner(c)
			}
			if ui.Button(c.Key("selsum-x"), "✕").Padding(2, 6).Tooltip(a.tr("Schließen", "Close")).Clicked() {
				a.closeSelectionSummary()
			}
		})
		if a.selSum.encrypted > 0 {
			ui.Text(c, a.trf("%d verschlüsselte Mail(s): nur Betreff und Absender gesendet, Inhalt nicht entschlüsselt.",
				"%d encrypted email(s): only subject and sender sent, content not decrypted.", a.selSum.encrypted)).FontSize(11.5).TextColor(t.Warning)
		}
		if a.selSum.skipped > 0 {
			ui.Text(c, a.trf("%d weitere markierte Mail(s) nicht berücksichtigt (höchstens %d).", "%d more checked email(s) left out (at most %d).",
				a.selSum.skipped, maxSelectionSummary)).FontSize(11.5).TextColor(t.TextMuted)
		}
		if a.selSum.loading && a.selSum.result == "" {
			ui.Text(c, a.tr("Lese Mails und frage die KI…", "Reading emails and asking the AI…")).FontSize(12).TextColor(t.TextMuted)
		}
		if a.selSum.err != "" {
			ui.Text(c, a.selSum.err).FontSize(12).TextColor(t.Danger)
		}
		if a.selSum.result != "" {
			markdown.View(c, a.selSum.result, &a.selSum.md)
		}
	})
}

// renderSelectionSummaryModal shows the summary in a full-width dialog on phones.
func (a *App) renderSelectionSummaryModal(c *ui.Context) {
	if !a.isPhone() || !a.selSum.open {
		return
	}
	t := c.Theme()
	open := a.selSum.open
	a.modal(c, &open, func() {
		ui.Scroll(c.Key("selsum-scroll")).FillWidth().MaxHeight(a.modalH(c, 640)).Background(t.Surface).Radius(12).Children(func() {
			ui.Column(c).FillWidth().Padding(12).Children(func() {
				a.renderSelectionSummaryCard(c)
			})
		})
	})
	if !open {
		a.closeSelectionSummary()
	}
}
