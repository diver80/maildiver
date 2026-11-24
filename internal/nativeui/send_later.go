package nativeui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// "Später senden…": the compose window hands the mail to the engine with a send time; the
// engine keeps it in its outbox (surviving restarts) and its outbox flush – every 30 s while the
// app runs, and in Android's background task – sends it once the time has come. The outbox view
// lists it with its time, to send now or take back into the drafts.

type sendLaterUI struct {
	open bool
	at   time.Time
}

// sendLaterPresets returns the menu's times: in an hour, tomorrow 8:00 and next Monday 8:00
// (a week ahead on a Monday).
func sendLaterPresets(now time.Time) (inHour, tomorrow, monday time.Time) {
	inHour = now.Add(time.Hour).Truncate(time.Minute)
	day := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())
	tomorrow = day.AddDate(0, 0, 1)
	ahead := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if ahead == 0 {
		ahead = 7
	}
	monday = day.AddDate(0, 0, ahead)
	return inHour, tomorrow, monday
}

// sendLaterValid reports whether at is far enough ahead to schedule (else it would just send).
func sendLaterValid(at, now time.Time) bool {
	return at.After(now.Add(time.Minute))
}

// formatSendAt shows a send time: "Mo 23.11. 08:00" / "Mon 11/23 08:00".
func (a *App) formatSendAt(t time.Time) string {
	if a.uiLang() == "en" {
		return t.Format("Mon 01/02 15:04")
	}
	days := [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}
	return days[t.Weekday()] + " " + t.Format("02.01. 15:04")
}

// ScheduleComposeSend sends the open compose window's mail at the given time (UI thread).
func (a *App) ScheduleComposeSend(ctx context.Context, at time.Time) {
	if !sendLaterValid(at, time.Now()) {
		a.toast(a.tr("Der Zeitpunkt muss in der Zukunft liegen.", "The time must be in the future."), "warning")
		return
	}
	req := a.buildComposeReq(false)
	req.SendAt = at.Unix()
	a.composeConfirm = false
	a.requestComposeSend(ctx, req)
}

// renderSendLaterButton is the "Send later…" menu next to Send.
func (a *App) renderSendLaterButton(c *ui.Context) {
	btn := ui.Button(c.Key("cmp-later"), a.tr("Später senden…", "Send later…")).
		Tooltip(a.tr("Mail zu einem späteren Zeitpunkt senden (wartet im Postausgang)", "Send the email later (waits in the Outbox)"))
	if a.composeBusy {
		btn.Disabled(true)
		return
	}
	btn.PopoverMenuButton(func(m *ui.Menu) {
		inHour, tomorrow, monday := sendLaterPresets(time.Now())
		if m.Item(a.tr("In 1 Stunde", "In 1 hour")).ID("in-hour").Trailing(inHour.Format("15:04")).Chosen() {
			a.ScheduleComposeSend(context.Background(), inHour)
		}
		if m.Item(a.tr("Morgen 8:00", "Tomorrow 8:00")).ID("tomorrow").Trailing(a.formatSendAt(tomorrow)).Chosen() {
			a.ScheduleComposeSend(context.Background(), tomorrow)
		}
		if m.Item(a.tr("Montag 8:00", "Monday 8:00")).ID("monday").Trailing(a.formatSendAt(monday)).Chosen() {
			a.ScheduleComposeSend(context.Background(), monday)
		}
		m.Separator()
		if m.Item(a.tr("Datum und Uhrzeit wählen…", "Pick date and time…")).ID("custom").Chosen() {
			a.sendLater = sendLaterUI{open: true, at: tomorrow}
		}
	})
}

// renderSendLaterModal picks a custom send time with the date and time inputs of the event form.
func (a *App) renderSendLaterModal(c *ui.Context) {
	if !a.sendLater.open {
		return
	}
	if !a.composeOpen {
		a.sendLater.open = false
		return
	}
	t := c.Theme()
	a.modal(c, &a.sendLater.open, func() {
		ui.Column(c).Width(a.modalW(380)).Padding(16).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(12).Children(func() {
			ui.Text(c, a.tr("Später senden", "Send later")).FontSize(15).Bold()
			ui.Row(c.Key("sl-when")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.DateInput(c.Key("sl-date"), &a.sendLater.at).Label(a.tr("Datum", "Date")).Grow(1).MinWidth(0)
				ui.TimeInput(c.Key("sl-time"), &a.sendLater.at).Label(a.tr("Uhrzeit", "Time")).Shrink(0)
			})
			valid := sendLaterValid(a.sendLater.at, time.Now())
			if !valid {
				ui.Text(c, a.tr("Der Zeitpunkt muss in der Zukunft liegen.", "The time must be in the future.")).FontSize(12).TextColor(t.Danger).Role(ui.RoleAlert)
			}
			ui.Text(c, a.tr("Die Mail wartet bis dahin im Postausgang; dort lässt sie sich sofort senden oder zurück in die Entwürfe legen.",
				"The email waits in the Outbox until then; there it can be sent at once or moved back to Drafts.")).FontSize(11.5).TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("sl-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
					a.sendLater.open = false
				}
				if ui.PrimaryButton(c.Key("sl-ok"), a.tr("Planen", "Schedule")).Disabled(!valid).Clicked() && valid {
					a.sendLater.open = false
					a.ScheduleComposeSend(context.Background(), a.sendLater.at)
				}
			})
		})
	})
}

// outboxScheduled reports whether an outbox entry waits for its send time.
func outboxScheduled(it backend.OutboxEntry, now time.Time) bool {
	return it.ScheduledAt > now.Unix()
}

// OutboxSendNow sends one outbox entry at once (UI thread).
func (a *App) OutboxSendNow(ctx context.Context, id string) {
	a.outboxEntryAction(ctx, id, func(c context.Context, bc *backend.Client) error { return bc.OutboxSendNow(c, id) },
		a.tr("E-Mail gesendet", "Email sent"), a.tr("Senden fehlgeschlagen: ", "Send failed: "))
}

// OutboxToDrafts moves one outbox entry back into the drafts (UI thread).
func (a *App) OutboxToDrafts(ctx context.Context, id string) {
	a.outboxEntryAction(ctx, id, func(c context.Context, bc *backend.Client) error { return bc.OutboxToDrafts(c, id) },
		a.tr("Zurück in den Entwürfen", "Moved back to Drafts"), a.tr("Fehlgeschlagen: ", "Failed: "))
}

func (a *App) outboxEntryAction(ctx context.Context, id string, run func(context.Context, *backend.Client) error, okMsg, errPrefix string) {
	bc, err := a.client()
	if err != nil || a.readOnly || id == "" || a.outboxBusy {
		return
	}
	a.outboxBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, backend.ComposeSendTimeout)
		defer cancel()
		rerr := run(c, bc)
		items, _ := bc.OutboxList(c)
		a.emitUpdate(func() {
			a.outboxBusy = false
			a.outboxItems = items
			if len(items) == 0 {
				a.outboxOpen = false
			}
			if rerr != nil {
				a.toast(errPrefix+a.errText(rerr), "error")
				return
			}
			a.toast(okMsg, "success")
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
		})
	}()
}

// outboxLabel names the outbox button: "Geplant (n)" while it only holds mails sent later.
func (a *App) outboxLabel() (label string, onlyScheduled bool) {
	now := time.Now()
	onlyScheduled = len(a.outboxItems) > 0
	for _, it := range a.outboxItems {
		if !outboxScheduled(it, now) {
			onlyScheduled = false
		}
	}
	if onlyScheduled {
		return a.trf("Geplant (%d)", "Scheduled (%d)", len(a.outboxItems)), true
	}
	return a.trf("Postausgang (%d)", "Outbox (%d)", len(a.outboxItems)), false
}
