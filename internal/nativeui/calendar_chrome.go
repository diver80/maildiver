package nativeui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// ensureCalMiniCursor returns the displayed month in the calendar sidebar's mini-month.
func (a *App) ensureCalMiniCursor() time.Time {
	if a.calMiniCursor.IsZero() {
		cur := a.ensureCalCursor()
		a.calMiniCursor = time.Date(cur.Year(), cur.Month(), 1, 12, 0, 0, 0, cur.Location())
	}
	return a.calMiniCursor
}

func (a *App) calendarGroupLabel(calType string) string {
	switch strings.ToLower(strings.TrimSpace(calType)) {
	case "local":
		return a.tr("Lokal", "Local")
	case "google":
		return a.tr("Google Kalender", "Google Calendar")
	case "caldav":
		return "CalDAV"
	case "ical":
		return a.tr("iCal-Abos", "iCal Feeds")
	default:
		return a.tr("Kalender", "Calendars")
	}
}

func calendarGroupRank(calType string) int {
	switch strings.ToLower(strings.TrimSpace(calType)) {
	case "local":
		return 0
	case "google":
		return 1
	case "caldav":
		return 2
	case "ical":
		return 3
	default:
		return 4
	}
}

// renderCalendarSidebar renders the calm Cron-style left calendar panel on Desktop:
// an interactive mini-month picker on top and grouped calendar toggles with color chips below.
func (a *App) renderCalendarSidebar(c *ui.Context) {
	t := c.Theme()
	cur := a.ensureCalCursor()
	mini := a.ensureCalMiniCursor()
	loc := cur.Location()
	today := time.Now().In(loc)
	todayKey := today.Format("2006-01-02")
	selectedKey := cur.Format("2006-01-02")

	// Index days with visible events so the mini-month can show subtle activity dots.
	hasEventDay := make(map[string]bool)
	for _, ev := range a.events {
		if a.isCalendarHidden(ev.CalendarID) {
			continue
		}
		for _, dk := range eventDayKeys(ev) {
			hasEventDay[dk] = true
		}
	}

	ui.Column(c.Key("cal-sidebar")).Width(a.sidebarWidth()).FillHeight().Background(t.Surface).Shrink(0).Children(func() {
		// Mini-month calendar
		ui.Column(c.Key("cal-mini")).FillWidth().Padding(12, 12, 10, 12).Gap(6).Children(func() {
			monthName := monthNamesDE[mini.Month()]
			weekdays := weekdayShortDE
			if a.uiLang() == "en" {
				monthName = monthNamesEN[mini.Month()]
				weekdays = weekdayShortEN
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
				ui.Text(c, fmt.Sprintf("%s %d", monthName, mini.Year())).FontSize(12.5).Bold().Grow(1).SingleLine()
				if ui.Button(c.Key("cal-mini-prev"), "‹").Padding(1, 6).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.TextMuted).Clicked() {
					a.calMiniCursor = time.Date(mini.Year(), mini.Month()-1, 1, 12, 0, 0, 0, loc)
					a.RefreshAll(context.Background())
				}
				if ui.Button(c.Key("cal-mini-next"), "›").Padding(1, 6).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.TextMuted).Clicked() {
					a.calMiniCursor = time.Date(mini.Year(), mini.Month()+1, 1, 12, 0, 0, 0, loc)
					a.RefreshAll(context.Background())
				}
			})

			ui.Row(c).FillWidth().Gap(2).Children(func() {
				for _, wd := range weekdays {
					ui.Box(c).Grow(1).Basis(0).MinWidth(0).Center().Children(func() {
						ui.Text(c, wd).FontSize(10).TextColor(withAlpha(t.TextMuted, 0.78)).SingleLine()
					})
				}
			})

			firstOfMonth := time.Date(mini.Year(), mini.Month(), 1, 12, 0, 0, 0, loc)
			offset := (int(firstOfMonth.Weekday()) + 6) % 7
			gridStart := firstOfMonth.AddDate(0, 0, -offset)

			for week := 0; week < 6; week++ {
				ui.Row(c.Key(fmt.Sprintf("cal-mini-w-%d", week))).FillWidth().Gap(2).Children(func() {
					for d := 0; d < 7; d++ {
						day := gridStart.AddDate(0, 0, week*7+d)
						dk := day.Format("2006-01-02")
						inMonth := day.Month() == mini.Month()
						isToday := dk == todayKey
						isSelected := dk == selectedKey

						btn := ui.ButtonBase(c.Key("cal-mini-d-"+dk)).Grow(1).Basis(0).MinWidth(0).Height(26).Radius(6).Center().Label(dk)
						fg := t.Text
						if !inMonth {
							fg = withAlpha(t.TextMuted, 0.42)
						}
						switch {
						case isToday:
							btn.Background(t.Accent)
							fg = t.AccentText
						case isSelected:
							btn.Background(withAlpha(t.Accent, 0.20))
							fg = t.Accent
						case btn.Hovered():
							btn.Background(t.SurfaceHover)
						}
						btn.Children(func() {
							ui.Column(c).Center().Gap(1).Children(func() {
								txt := ui.Text(c, fmt.Sprintf("%d", day.Day())).FontSize(11).FontFeatures("tnum").TextColor(fg).SingleLine()
								if isToday || isSelected {
									txt.Bold()
								}
								if hasEventDay[dk] {
									dotClr := t.Accent
									if isToday {
										dotClr = withAlpha(t.AccentText, 0.85)
									}
									ui.Box(c).Size(3, 3).Radius(2).Background(dotClr)
								}
							})
						})
						if btn.Clicked() {
							a.calCursor = day
							a.calMiniCursor = time.Date(day.Year(), day.Month(), 1, 12, 0, 0, 0, loc)
							a.calDayScrolledKey = ""
							a.RefreshAll(context.Background())
						}
					}
				})
			}
		})

		ui.Divider(c)

		// Grouped calendar toggles
		ui.Scroll(c.Key("cal-sb-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(10, 12).Gap(10).Children(func() {
				vis, total := a.visibleCalendarCount()
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
					ui.Text(c, a.tr("Kalender", "Calendars")).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1)
					if total > 1 {
						if vis < total {
							if ui.Button(c.Key("cal-sb-all"), a.tr("Alle", "All")).Padding(1, 6).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.Accent).Clicked() {
								a.setAllCalendarsVisible(true)
							}
						} else {
							if ui.Button(c.Key("cal-sb-none"), a.tr("Keine", "None")).Padding(1, 6).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.TextMuted).Clicked() {
								a.setAllCalendarsVisible(false)
							}
						}
					}
				})

				if len(a.calendars) == 0 {
					ui.Text(c, a.tr("Keine Kalender geladen.", "No calendars loaded.")).FontSize(11.5).TextColor(t.TextMuted)
				} else {
					type calGroup struct {
						key   string
						rank  int
						label string
						items []backend.Calendar
					}
					byKey := map[string]*calGroup{}
					var groups []*calGroup
					for _, cal := range a.calendars {
						gk := strings.ToLower(strings.TrimSpace(cal.Type))
						if gk == "" {
							gk = "other"
						}
						g, ok := byKey[gk]
						if !ok {
							g = &calGroup{key: gk, rank: calendarGroupRank(gk), label: a.calendarGroupLabel(gk)}
							byKey[gk] = g
							groups = append(groups, g)
						}
						g.items = append(g.items, cal)
					}
					sort.Slice(groups, func(i, j int) bool {
						if groups[i].rank != groups[j].rank {
							return groups[i].rank < groups[j].rank
						}
						return groups[i].label < groups[j].label
					})
					showGroupHeaders := len(groups) > 1
					for _, g := range groups {
						ui.Column(c.Key("cal-sb-grp-"+g.key)).FillWidth().Gap(3).Children(func() {
							if showGroupHeaders {
								ui.Text(c, g.label).FontSize(10.5).Bold().TextColor(withAlpha(t.TextMuted, 0.85)).Padding(2, 2, 0, 2).SingleLine()
							}
							for _, cal := range g.items {
								calCopy := cal
								checked := !a.isCalendarHidden(calCopy.ID)
								clr := t.Accent
								if parsed, ok := parseHexColorUI(calCopy.Color); ok {
									clr = parsed
								}
								row := ui.ButtonBase(c.Key("cal-sb-item-"+calCopy.ID)).FillWidth().Padding(4, 6).Radius(6).Gap(8).AlignItems(ui.Center).Label(calCopy.Name)
								if row.Hovered() {
									row.Background(t.SurfaceHover)
								}
								row.Children(func() {
									box := ui.Box(c).Size(14, 14).Radius(4).Shrink(0).Center()
									if checked {
										box.Background(clr)
										box.Children(func() {
											ui.Icon(c, iconCheck).Size(10, 10).TextColor(ui.RGB(255, 255, 255))
										})
									} else {
										box.Border(1.4, withAlpha(clr, 0.75))
									}
									lblClr := t.Text
									if !checked {
										lblClr = t.TextMuted
									}
									ui.Text(c, calCopy.Name).FontSize(12).TextColor(lblClr).SingleLine().Grow(1).MinWidth(0)
								})
								if row.Clicked() {
									a.setCalendarVisible(calCopy.ID, !checked)
								}
							}
						})
					}
				}
			})
		})

		if a.isDirectMode() {
			ui.Divider(c)
			ui.Row(c).FillWidth().Padding(8, 12).Children(func() {
				if iconLabelBtn(c.Key("cal-sb-add"), iconCalPlus, a.tr("Kalender verwalten", "Manage Calendars"), false, a.tr("Google Kalender, CalDAV & iCal-Abos verwalten", "Manage Google Calendar, CalDAV & iCal feeds")).FillWidth().Clicked() {
					a.OpenDirectModal(1)
				}
			})
		}
	})
}

// liveMeeting finds a visible, non-declined timed event that is either running right now
// or starting within the next 15 minutes (preferring currently running events and events with a join link).
func (a *App) liveMeeting(now time.Time) (ev backend.CalendarEvent, statusLabel, joinURL, joinLabel string, ok bool) {
	ev, statusLabel, joinURL, joinLabel, _, ok = a.liveMeetingWithRunning(now)
	return
}

func (a *App) liveMeetingWithRunning(now time.Time) (ev backend.CalendarEvent, statusLabel, joinURL, joinLabel string, isRunning, ok bool) {
	const lookahead = 15 * time.Minute
	var best backend.CalendarEvent
	var bestStart time.Time
	var bestRunning bool
	var bestJoinURL, bestJoinLabel string
	found := false

	for _, cand := range a.events {
		if cand.AllDay || a.isCalendarHidden(cand.CalendarID) || strings.EqualFold(cand.ResponseStatus, "declined") {
			continue
		}
		st, err := parseEventTime(cand.Start)
		if err != nil {
			continue
		}
		et := st.Add(time.Hour)
		if strings.TrimSpace(cand.End) != "" {
			if parsedEnd, eerr := parseEventTime(cand.End); eerr == nil && parsedEnd.After(st) {
				et = parsedEnd
			}
		}
		running := !now.Before(st) && now.Before(et)
		upcoming := st.After(now) && st.Sub(now) <= lookahead
		if !running && !upcoming {
			continue
		}
		jURL, jLbl := a.eventJoinLink(cand)
		if !found ||
			(running && !bestRunning) ||
			(running == bestRunning && jURL != "" && bestJoinURL == "") ||
			(running == bestRunning && (jURL != "") == (bestJoinURL != "") && st.Before(bestStart)) {
			best = cand
			bestStart = st
			bestRunning = running
			bestJoinURL = jURL
			bestJoinLabel = jLbl
			found = true
		}
	}
	if !found {
		return backend.CalendarEvent{}, "", "", "", false, false
	}
	if bestRunning {
		statusLabel = a.tr("jetzt", "now")
	} else {
		mins := int((bestStart.Sub(now) + 30*time.Second) / time.Minute)
		if mins < 1 {
			statusLabel = a.tr("gleich", "soon")
		} else {
			statusLabel = a.trf("in %d Min.", "in %dm", mins)
		}
	}
	return best, statusLabel, bestJoinURL, bestJoinLabel, bestRunning, true
}

// renderLiveMeetingPill shows a compact Cron-style live meeting pill in the desktop top toolbar
// with a direct Join button whenever a meeting is live or about to start.
func (a *App) renderLiveMeetingPill(c *ui.Context) {
	ev, status, joinURL, joinLabel, running, ok := a.liveMeetingWithRunning(time.Now())
	if !ok {
		return
	}
	t := c.Theme()
	dotClr := t.Accent
	if running {
		dotClr = t.Success
	}
	ui.Row(c.Key("tb-live-meeting")).Padding(3, 6, 3, 8).Gap(6).Radius(8).
		Background(withAlpha(t.Accent, 0.12)).Border(1, withAlpha(t.Accent, 0.35)).
		AlignItems(ui.Center).Shrink(0).Children(func() {
		info := ui.ButtonBase(c.Key("tb-live-info")).Gap(6).Padding(1, 2).AlignItems(ui.Center).Label(ev.Title).
			Tooltip(ev.Title + " · " + formatEventRangeLang(false, ev.Start, ev.End, a.uiLang()))
		info.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(dotClr).Shrink(0)
			ui.Text(c, truncateText(ev.Title, 22)).FontSize(11.5).Bold().TextColor(t.Text).SingleLine()
			ui.Text(c, status).FontSize(11).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine()
		})
		if info.Clicked() {
			a.OpenEventDetails(ev)
		}
		if joinURL != "" {
			btnLbl := a.tr("Beitreten", "Join")
			if ui.Button(c.Key("tb-live-join"), btnLbl).Padding(2, 8).Radius(6).
				Background(t.Accent).Border(0, ui.Color{}).TextColor(t.AccentText).Tooltip(joinLabel).Clicked() {
				_ = openExternalURLFn(joinURL)
			}
		}
	})
}

// renderPhoneLiveMeetingBar shows a slim top banner on Android/phone when a meeting is live
// or starting within 15 minutes, giving phone users 1-tap join parity with desktop.
func (a *App) renderPhoneLiveMeetingBar(c *ui.Context) {
	ev, status, joinURL, joinLabel, running, ok := a.liveMeetingWithRunning(time.Now())
	if !ok {
		return
	}
	t := c.Theme()
	dotClr := t.Accent
	if running {
		dotClr = t.Success
	}
	ui.Row(c.Key("ph-live-meeting")).FillWidth().Padding(8, 12).Gap(8).
		Background(withAlpha(t.Accent, 0.14)).BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(t.Accent, 0.35)).
		AlignItems(ui.Center).Children(func() {
		info := ui.ButtonBase(c.Key("ph-live-info")).Grow(1).MinWidth(0).MinHeight(40).Padding(4, 2).Gap(6).AlignItems(ui.Center).Label(ev.Title)
		info.Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(dotClr).Shrink(0)
			ui.Text(c, ev.Title).FontSize(12.5).Bold().TextColor(t.Text).SingleLine().Grow(1).MinWidth(0)
			ui.Text(c, status).FontSize(11.5).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine().Shrink(0)
		})
		if info.Clicked() {
			a.OpenEventDetails(ev)
		}
		if joinURL != "" {
			if ui.Button(c.Key("ph-live-join"), a.tr("Beitreten", "Join")).MinHeight(40).Padding(8, 14).Radius(8).
				Background(t.Accent).Border(0, ui.Color{}).TextColor(t.AccentText).Tooltip(joinLabel).Clicked() {
				_ = openExternalURLFn(joinURL)
			}
		}
	})
}

// inviteConflicts finds visible, non-declined timed calendar events overlapping inv's time span,
// skipping the invitation itself if it is already stored in the calendar.
func (a *App) inviteConflicts(inv backend.CalendarEvent) []backend.CalendarEvent {
	if inv.AllDay || strings.TrimSpace(inv.Start) == "" || strings.TrimSpace(inv.End) == "" {
		return nil
	}
	invStart, err := parseEventTime(inv.Start)
	if err != nil {
		return nil
	}
	invEnd, err := parseEventTime(inv.End)
	if err != nil || !invEnd.After(invStart) {
		return nil
	}
	invTitleNorm := strings.ToLower(strings.TrimSpace(inv.Title))
	var conflicts []backend.CalendarEvent
	for _, ev := range a.events {
		if ev.AllDay || a.isCalendarHidden(ev.CalendarID) || strings.EqualFold(ev.ResponseStatus, "declined") {
			continue
		}
		if inv.ID != "" && ev.ID == inv.ID {
			continue
		}
		st, err := parseEventTime(ev.Start)
		if err != nil {
			continue
		}
		et := st.Add(time.Hour)
		if strings.TrimSpace(ev.End) != "" {
			if parsedEnd, eerr := parseEventTime(ev.End); eerr == nil && parsedEnd.After(st) {
				et = parsedEnd
			}
		}
		// Skip the same event already imported into a calendar (same title + exact same start & end).
		if invTitleNorm != "" && strings.ToLower(strings.TrimSpace(ev.Title)) == invTitleNorm && st.Equal(invStart) && et.Equal(invEnd) {
			continue
		}
		if st.Before(invEnd) && et.After(invStart) {
			conflicts = append(conflicts, ev)
		}
	}
	sort.SliceStable(conflicts, func(i, j int) bool {
		si, _ := parseEventTime(conflicts[i].Start)
		sj, _ := parseEventTime(conflicts[j].Start)
		return si.Before(sj)
	})
	return conflicts
}

// inviteFreeBusyBadge returns a pill badge indicating whether the user is free or has a
// calendar conflict during an invitation's time window.
func (a *App) inviteFreeBusyBadge(inv backend.CalendarEvent, t *ui.Theme) (text string, bg, fg ui.Color, ok bool) {
	if inv.AllDay || strings.TrimSpace(inv.Start) == "" || strings.TrimSpace(inv.End) == "" {
		return "", ui.Color{}, ui.Color{}, false
	}
	st, err := parseEventTime(inv.Start)
	if err != nil {
		return "", ui.Color{}, ui.Color{}, false
	}
	et, err := parseEventTime(inv.End)
	if err != nil || !et.After(st) {
		return "", ui.Color{}, ui.Color{}, false
	}
	conflicts := a.inviteConflicts(inv)
	if len(conflicts) == 0 {
		// Never falsely claim "free" if calendar events were loaded for a range not covering inv.
		if !a.calLoadedStart.IsZero() && (st.Before(a.calLoadedStart) || et.After(a.calLoadedEnd)) {
			return "", ui.Color{}, ui.Color{}, false
		}
		return a.tr("✓ Du bist frei", "✓ You're free"), withAlpha(t.Success, 0.14), t.Success, true
	}
	first := conflicts[0]
	title := truncateText(first.Title, 24)
	if len(conflicts) == 1 {
		rng := formatEventRangeLang(false, first.Start, first.End, a.uiLang())
		return a.trf("⚠ Überschneidung: %s (%s)", "⚠ Conflict: %s (%s)", title, rng), withAlpha(t.Warning, 0.18), t.Warning, true
	}
	return a.trf("⚠ %d Überschneidungen (%s …)", "⚠ %d conflicts (%s …)", len(conflicts), title), withAlpha(t.Warning, 0.18), t.Warning, true
}
