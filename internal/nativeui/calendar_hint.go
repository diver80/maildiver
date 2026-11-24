package nativeui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/backend"
)

const (
	calendarHintDays     = 14
	calendarHintMaxLines = 60
)

// calendarHintForAI lists the user's busy slots of the next 14 days for AI draft replies, so a
// proposed meeting time does not clash. Only date and time leave the app: titles often name
// people or private matters ("1:1 Anna", "Therapie"), and attendees, locations and descriptions
// stay too; hidden and declined events are left out.
func calendarHintForAI(ctx context.Context, bc *backend.Client, hidden map[string]bool, now time.Time) string {
	if bc == nil {
		return ""
	}
	end := now.AddDate(0, 0, calendarHintDays)
	res, err := bc.CalendarEvents(ctx, now.Format(time.RFC3339), end.Format(time.RFC3339), nil, 500)
	if err != nil {
		return ""
	}
	return formatCalendarHint(res.Events, hidden, now, end)
}

func formatCalendarHint(events []backend.CalendarEvent, hidden map[string]bool, now, end time.Time) string {
	type slot struct {
		start time.Time
		line  string
	}
	var slots []slot
	for _, ev := range events {
		if hidden[ev.CalendarID] || ev.ResponseStatus == "declined" {
			continue
		}
		st, err := parseEventTime(ev.Start)
		if err != nil {
			continue
		}
		en, err := parseEventTime(ev.End)
		if err != nil || !en.After(st) {
			en = st
		}
		if ev.AllDay {
			// All-day dates carry no zone: compare them as local calendar days.
			st = time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, now.Location())
			en = time.Date(en.Year(), en.Month(), en.Day(), 0, 0, 0, 0, now.Location())
			if !en.After(st) {
				en = st.AddDate(0, 0, 1)
			}
		} else {
			st, en = st.In(now.Location()), en.In(now.Location())
		}
		if !en.After(now) || !st.Before(end) {
			continue
		}
		var when string
		if ev.AllDay {
			when = st.Format("2006-01-02 Mon") + " all day"
			if days := int(en.Sub(st).Hours()/24 + 0.5); days > 1 {
				when = fmt.Sprintf("%s – %s all day", st.Format("2006-01-02 Mon"), en.AddDate(0, 0, -1).Format("2006-01-02 Mon"))
			}
		} else {
			when = st.Format("2006-01-02 Mon 15:04") + "–" + en.Format("15:04")
			if !sameDay(st, en) {
				when = st.Format("2006-01-02 Mon 15:04") + " – " + en.Format("2006-01-02 Mon 15:04")
			}
		}
		slots = append(slots, slot{start: st, line: when + " busy"})
	}
	if len(slots) == 0 {
		return ""
	}
	sort.SliceStable(slots, func(i, j int) bool { return slots[i].start.Before(slots[j].start) })
	var b strings.Builder
	fmt.Fprintf(&b, "User's busy times %s to %s (local time %s). Do not propose these slots:\n",
		now.Format("2006-01-02"), end.Format("2006-01-02"), now.Format("MST"))
	for i, s := range slots {
		if i >= calendarHintMaxLines {
			fmt.Fprintf(&b, "… %d more\n", len(slots)-i)
			break
		}
		b.WriteString("- " + s.line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// hiddenCalendarsCopy snapshots the calendar filter for use off the UI thread.
func (a *App) hiddenCalendarsCopy() map[string]bool {
	out := make(map[string]bool, len(a.hiddenCalendars))
	for k, v := range a.hiddenCalendars {
		out[k] = v
	}
	return out
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
