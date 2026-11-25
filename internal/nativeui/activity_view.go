package nativeui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/mailengine"
)

// The activity view lists what was done to mails in PostDive on this device, newest first,
// grouped by day: read, flagged, moved, deleted, sent, answered invitations – and by whom (the
// user, a filter rule, an automation, an agent over MCP). An entry opens its mail wherever it
// is now; a move can be undone from here.

const activityPageSize = 200

// activityFilter is one chip: the kinds of entries it shows.
type activityFilter struct {
	key   string
	de    string
	en    string
	kinds []string
}

var activityFilters = []activityFilter{
	{"trash", "Gelöscht", "Deleted", []string{mailengine.ActivityTrash, mailengine.ActivityDelete}},
	{"move", "Verschoben", "Moved", []string{mailengine.ActivityMove, mailengine.ActivityArchive, mailengine.ActivitySpam, mailengine.ActivityRestore}},
	{"send", "Gesendet", "Sent", []string{mailengine.ActivitySend, mailengine.ActivitySchedule}},
	{"flag", "Stern", "Starred", []string{mailengine.ActivityFlag, mailengine.ActivityUnflag}},
	{"rsvp", "Termine", "Invitations", []string{mailengine.ActivityRSVP}},
	{"read", "Gelesen", "Read", []string{mailengine.ActivityRead, mailengine.ActivityUnread, mailengine.ActivityReadAll}},
}

type activityUI struct {
	loaded    bool
	loading   bool
	seq       int
	list      []mailengine.ActivityEntry
	more      bool
	err       string
	off       map[string]bool // chips switched off; "read" starts off as it is the busiest
	search    string
	lastQuery string
	expanded  map[string]bool // batches shown entry by entry
	busyID    int64
}

func (a *App) activityKinds() []string {
	ac := &a.activity
	if ac.off == nil {
		ac.off = map[string]bool{"read": true}
	}
	var kinds []string
	for _, f := range activityFilters {
		if !ac.off[f.key] {
			kinds = append(kinds, f.kinds...)
		}
	}
	return kinds
}

// LoadActivity reloads the log from the newest entry; more appends the next page.
func (a *App) LoadActivity(ctx context.Context, more bool) {
	dc := a.opts.Direct
	ac := &a.activity
	if dc == nil {
		return
	}
	kinds := a.activityKinds()
	if len(kinds) == 0 {
		ac.list, ac.more, ac.loaded = nil, false, true
		return
	}
	q := mailengine.ActivityQuery{Kinds: kinds, Search: strings.TrimSpace(ac.search), Limit: activityPageSize}
	if more && len(ac.list) > 0 {
		q.Before = ac.list[len(ac.list)-1].ID
	}
	ac.lastQuery = q.Search
	ac.loading = true
	ac.seq++
	seq := ac.seq
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		list, err := dc.ListActivity(c, q)
		a.emitUpdate(func() {
			ac := &a.activity
			if seq != ac.seq {
				return
			}
			ac.loading, ac.loaded = false, true
			if err != nil {
				ac.err = err.Error()
				return
			}
			ac.err = ""
			if more {
				ac.list = append(ac.list, list...)
			} else {
				ac.list = list
			}
			ac.more = len(list) == activityPageSize
		})
	}()
}

// openActivityMail shows the mail of an entry in its current folder.
func (a *App) openActivityMail(ctx context.Context, e mailengine.ActivityEntry) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m, ok := dc.LocateActivityMessage(c, e)
		a.emitUpdate(func() {
			if !ok {
				a.toast(a.tr("Die Mail ist nicht mehr vorhanden (endgültig gelöscht oder noch nicht synchronisiert).", "The mail is no longer there (deleted for good or not synced yet)."), "info")
				return
			}
			a.setNav("folder:" + m.FolderID)
			a.startMessageLoad(context.Background())
			a.SelectMessage(context.Background(), m.ID)
		})
	}()
}

// activityUndoable reports whether the move of e can be undone: the mail went from one folder
// to another and is known by its Message-ID.
func activityUndoable(e mailengine.ActivityEntry) bool {
	switch e.Kind {
	case mailengine.ActivityMove, mailengine.ActivityArchive, mailengine.ActivityTrash, mailengine.ActivitySpam:
		return e.MessageID != "" && e.Folder != "" && e.Target != "" && !strings.EqualFold(e.Folder, e.Target)
	}
	return false
}

func (a *App) undoActivity(ctx context.Context, e mailengine.ActivityEntry) {
	bc, err := a.client()
	if err != nil || a.activity.busyID != 0 {
		return
	}
	a.activity.busyID = e.ID
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err := bc.UndoMove(c, backend.UndoMoveRequest{Items: []backend.UndoMoveItem{{
			AccountID: e.AccountID, MessageID: e.MessageID, FromFolderPath: e.Folder, ToFolderPath: e.Target,
		}}})
		a.emitUpdate(func() {
			a.activity.busyID = 0
			switch {
			case err != nil:
				a.toast(a.tr("Rückgängig fehlgeschlagen: ", "Undo failed: ")+a.errText(err), "error")
			case res.Restored == 0:
				a.toast(a.trf("Die Mail liegt nicht mehr in „%s“.", "The mail is no longer in “%s”.", e.Target), "info")
			default:
				a.toast(a.trf("Zurück nach „%s“ verschoben", "Moved back to “%s”", e.Folder), "success")
			}
			a.LoadActivity(context.Background(), false)
		})
	}()
}

// activityVerb is what an entry says was done.
func (a *App) activityVerb(e mailengine.ActivityEntry) (string, *ui.SVG) {
	switch e.Kind {
	case mailengine.ActivityRead:
		return a.tr("Gelesen", "Read"), iconMailOpen
	case mailengine.ActivityUnread:
		return a.tr("Als ungelesen markiert", "Marked unread"), iconMailUnread
	case mailengine.ActivityReadAll:
		return a.trf("%d als gelesen markiert", "%d marked read", e.Count), iconMailOpen
	case mailengine.ActivityFlag:
		return a.tr("Stern gesetzt", "Starred"), iconStarFilled
	case mailengine.ActivityUnflag:
		return a.tr("Stern entfernt", "Unstarred"), iconStar
	case mailengine.ActivityMove:
		return a.tr("Verschoben", "Moved"), iconFolderInput
	case mailengine.ActivityArchive:
		return a.tr("Archiviert", "Archived"), iconArchive
	case mailengine.ActivityTrash:
		return a.tr("In den Papierkorb", "Moved to trash"), iconTrash
	case mailengine.ActivityDelete:
		return a.tr("Endgültig gelöscht", "Deleted for good"), iconTrash
	case mailengine.ActivitySpam:
		return a.tr("Als Spam markiert", "Marked as spam"), iconSpam
	case mailengine.ActivityRestore:
		return a.tr("Zurückgeholt", "Restored"), iconUndo
	case mailengine.ActivitySend:
		if e.Target == "outbox" {
			return a.tr("Gesendet (Postausgang)", "Sent (outbox)"), iconSend
		}
		return a.tr("Gesendet", "Sent"), iconSend
	case mailengine.ActivitySchedule:
		return a.tr("Senden geplant", "Send scheduled"), iconSend
	case mailengine.ActivityRSVP:
		switch e.Target {
		case "accepted":
			return a.tr("Termin zugesagt", "Invitation accepted"), iconCal
		case "declined":
			return a.tr("Termin abgesagt", "Invitation declined"), iconCal
		}
		return a.tr("Termin vorläufig zugesagt", "Invitation tentatively accepted"), iconCal
	}
	return e.Kind, iconMail
}

func (a *App) activitySourceLabel(e mailengine.ActivityEntry) string {
	switch e.Source {
	case mailengine.ActivityByRule:
		if e.SourceName != "" {
			return a.tr("Regel: ", "Rule: ") + e.SourceName
		}
		return a.tr("Regel", "Rule")
	case mailengine.ActivityByMCP:
		return a.tr("Agent (MCP)", "Agent (MCP)")
	case mailengine.ActivityByAuto:
		return a.tr("Automatik", "Automation")
	}
	return ""
}

func (a *App) activityDayLabel(t time.Time) string {
	y, m, d := t.Date()
	sameDay := func(o time.Time) bool { oy, om, od := o.Date(); return oy == y && om == m && od == d }
	switch now := time.Now(); {
	case sameDay(now):
		return a.tr("Heute", "Today")
	case sameDay(now.AddDate(0, 0, -1)):
		return a.tr("Gestern", "Yesterday")
	}
	wd := weekdayShortDE
	if a.uiLang() == "en" {
		wd = weekdayShortEN
	}
	return fmt.Sprintf("%s %02d.%02d.%d", wd[(int(t.Weekday())+6)%7], d, int(m), y)
}

// activityRows groups entries of one batch action into one row, unless it is expanded.
func activityRows(list []mailengine.ActivityEntry, expanded map[string]bool) [][]mailengine.ActivityEntry {
	var rows [][]mailengine.ActivityEntry
	for _, e := range list {
		if n := len(rows); n > 0 && e.Batch != "" && !expanded[e.Batch] {
			last := rows[n-1]
			if last[0].Batch == e.Batch && last[0].Kind == e.Kind {
				rows[n-1] = append(last, e)
				continue
			}
		}
		rows = append(rows, []mailengine.ActivityEntry{e})
	}
	return rows
}

func (a *App) renderActivityWorkspace(c *ui.Context) {
	t := c.Theme()
	ac := &a.activity
	if !ac.loaded && !ac.loading {
		a.LoadActivity(context.Background(), false)
	}
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Column(c).FillWidth().Padding(10, 14).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconActivity).Size(17, 17).TextColor(t.Accent)
				ui.Text(c, a.tr("Aktivität", "Activity")).FontSize(15).Bold().Grow(1)
				sf := ui.SearchField(c.Key("act-search"), &ac.search).Placeholder(a.tr("Betreff, Absender, Regel…", "Subject, sender, rule…")).Width(260)
				if sf.Submitted() || strings.TrimSpace(ac.search) != ac.lastQuery {
					a.LoadActivity(context.Background(), false)
				}
				if iconBtn(c.Key("act-refresh"), iconRefresh, a.tr("Aktualisieren", "Refresh"), ui.Color{}, false).Clicked() {
					a.LoadActivity(context.Background(), false)
				}
			})
			// What the log covers, so nobody looks here for what happened elsewhere.
			ui.Row(c.Key("act-scope")).FillWidth().Gap(8).Padding(6, 10).Radius(6).AlignItems(ui.Center).
				Background(withAlpha(t.Accent, 0.10)).Border(1, withAlpha(t.Accent, 0.30)).Children(func() {
				ui.Icon(c, iconHelp).Size(14, 14).TextColor(t.Accent).Shrink(0)
				ui.Text(c, a.tr(
					"Zeigt nur die Mail-Aktivitäten in PostDive auf diesem Gerät. Was in anderen Mail-Programmen, im Webmail oder auf anderen Geräten passiert, erscheint hier nicht. Einträge werden nach 90 Tagen gelöscht.",
					"Shows only the mail activity in PostDive on this device. What happens in other mail apps, webmail or on other devices does not appear here. Entries are deleted after 90 days.",
				)).FontSize(12).TextColor(t.Text).Grow(1).MinWidth(0)
			})
			ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				a.activityKinds() // makes sure the defaults are set
				for _, f := range activityFilters {
					on := !ac.off[f.key]
					bg, fg, border := t.Surface, t.TextMuted, t.Border
					if on {
						bg, fg, border = withAlpha(t.Accent, 0.16), t.Accent, withAlpha(t.Accent, 0.5)
					}
					lbl := a.tr(f.de, f.en)
					if on {
						lbl = "✓ " + lbl
					}
					chip := ui.Button(c.Key("act-chip-"+f.key), lbl).Padding(3, 10).Radius(999).Background(bg).Border(1, border).TextColor(fg)
					if chip.Clicked() {
						ac.off[f.key] = on
						a.LoadActivity(context.Background(), false)
					}
				}
			})
		})

		if ac.err != "" {
			ui.Text(c, ac.err).FontSize(12).TextColor(t.Danger).Padding(10, 14)
		}
		if ac.loaded && len(ac.list) == 0 {
			ui.Column(c).Fill().Center().Padding(28).Gap(8).Children(func() {
				ui.Icon(c, iconActivity).Size(28, 28).TextColor(t.TextMuted)
				ui.Text(c, a.tr("Noch keine Aktivität", "No activity yet")).FontSize(14).Bold()
				ui.Text(c, a.tr("Sobald du hier Mails liest, verschiebst, löschst oder sendest, erscheint es in dieser Liste.", "Once you read, move, delete or send mails here, it shows up in this list.")).FontSize(12).TextColor(t.TextMuted)
			})
			return
		}

		ui.Scroll(c.Key("act-scroll")).TrackScroll(&a.widgets.activityScroll).Fill().Children(func() {
			ui.Column(c).FillWidth().Padding(4, 14, 14, 14).Children(func() {
				day := ""
				for i, row := range activityRows(ac.list, ac.expanded) {
					e := row[0]
					if d := a.activityDayLabel(e.At); d != day {
						day = d
						ui.Text(c, d).FontSize(12).Bold().TextColor(t.TextMuted).Padding(12, 0, 4, 0)
					}
					a.renderActivityRow(c, i, row)
				}
				if ac.more {
					ui.Row(c).FillWidth().Padding(10, 0).Children(func() {
						ui.Spacer(c)
						if ui.Button(c.Key("act-more"), a.tr("Ältere laden", "Load older")).Clicked() && !ac.loading {
							a.LoadActivity(context.Background(), true)
						}
						ui.Spacer(c)
					})
				}
			})
		})
	})
}

func (a *App) renderActivityRow(c *ui.Context, i int, row []mailengine.ActivityEntry) {
	t := c.Theme()
	ac := &a.activity
	e := row[0]
	verb, icon := a.activityVerb(e)
	clr := t.TextMuted
	switch e.Kind {
	case mailengine.ActivityTrash, mailengine.ActivityDelete, mailengine.ActivitySpam:
		clr = t.Danger
	case mailengine.ActivitySend, mailengine.ActivitySchedule:
		clr = t.Success
	case mailengine.ActivityFlag:
		clr = t.Warning
	case mailengine.ActivityMove, mailengine.ActivityArchive, mailengine.ActivityRestore:
		clr = t.Accent
	}
	subject := strings.TrimSpace(e.Subject)
	if subject == "" && e.Kind != mailengine.ActivityReadAll {
		subject = a.tr("(ohne Betreff)", "(no subject)")
	}
	if len(row) > 1 {
		verb = a.trf("%s · %d Mails", "%s · %d mails", verb, len(row))
		subject = a.trf("%s und %d weitere", "%s and %d more", subject, len(row)-1)
	}
	ui.Row(c.Key(fmt.Sprintf("act-row-%d", e.ID))).FillWidth().Gap(10).Padding(7, 8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(t.Border, 0.6)).Children(func() {
		ui.Text(c, e.At.Format("15:04")).FontSize(11.5).TextColor(t.TextMuted).Width(40).Shrink(0)
		ui.Icon(c, icon).Size(15, 15).TextColor(clr).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, verb).FontSize(12).Bold().TextColor(clr).SingleLine().Shrink(0)
				if subject != "" {
					ui.Text(c, subject).FontSize(12.5).Bold().TextColor(t.Text).SingleLine().Grow(1).MinWidth(0)
				} else {
					ui.Spacer(c)
				}
				if src := a.activitySourceLabel(e); src != "" {
					miniTag(c, src, t.Accent, withAlpha(t.Accent, 0.14))
				}
			})
			var meta []string
			if p := strings.TrimSpace(e.Peer); p != "" && len(row) == 1 {
				if e.Kind == mailengine.ActivitySend || e.Kind == mailengine.ActivitySchedule {
					p = a.tr("an ", "to ") + p
				}
				meta = append(meta, p)
			}
			switch {
			case e.Kind == mailengine.ActivitySchedule && e.Target != "":
				if when, err := time.Parse(time.RFC3339, e.Target); err == nil {
					meta = append(meta, a.tr("für ", "for ")+when.Local().Format("02.01. 15:04"))
				}
			case e.Folder != "" && e.Target != "" && e.Kind != mailengine.ActivityRSVP && e.Kind != mailengine.ActivitySend:
				meta = append(meta, cleanFolderPath(e.Folder)+" → "+cleanFolderPath(e.Target))
			case e.Folder != "":
				meta = append(meta, cleanFolderPath(e.Folder))
			}
			if acc := a.accountDisplayName(e.AccountID); acc != "" && len(a.accounts) > 1 {
				meta = append(meta, acc)
			}
			if len(meta) > 0 {
				ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			}
		})
		if len(row) > 1 {
			if ui.Button(c.Key(fmt.Sprintf("act-exp-%d", e.ID)), a.tr("Einzeln zeigen", "Show each")).Padding(2, 8).Clicked() {
				if ac.expanded == nil {
					ac.expanded = map[string]bool{}
				}
				ac.expanded[e.Batch] = true
			}
			return
		}
		if activityUndoable(e) && !a.readOnly {
			b := iconLabelBtn(c.Key(fmt.Sprintf("act-undo-%d", e.ID)), iconUndo, a.tr("Rückgängig", "Undo"), false,
				a.trf("Zurück nach „%s“ verschieben", "Move back to “%s”", cleanFolderPath(e.Folder)))
			if ac.busyID != 0 {
				b.Disabled(true)
			}
			if b.Clicked() && ac.busyID == 0 {
				a.undoActivity(context.Background(), e)
			}
		}
		if e.MessageID != "" || e.LocalID != "" {
			if iconLabelBtn(c.Key(fmt.Sprintf("act-open-%d", e.ID)), iconExternal, a.tr("Öffnen", "Open"), false, a.tr("Mail dort öffnen, wo sie jetzt liegt", "Open the mail where it is now")).Clicked() {
				a.openActivityMail(context.Background(), e)
			}
		}
	})
}
