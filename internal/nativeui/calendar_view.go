package nativeui

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

var (
	calHTMLBreakRE = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</tr>`)
	// Only real tags (<p>, </a>, <br/>…) — NOT plain-text autolinks like
	// <https://teams.microsoft.com/…> used by Outlook/Teams invitations.
	calHTMLTagRE = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9]*(\s[^>]*)?/?>`)
	// Separator rules (Teams: "________________…").
	calRuleRE = regexp.MustCompile(`[_=─━]{4,}|-{5,}`)
)

func cleanEventPreviewText(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = calHTMLBreakRE.ReplaceAllString(s, " ")
	s = calHTMLTagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = calRuleRE.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func (a *App) isCalendarHidden(calID string) bool {
	if calID == "" || len(a.hiddenCalendars) == 0 {
		return false
	}
	return a.hiddenCalendars[calID]
}

func (a *App) setCalendarVisible(calID string, visible bool) {
	if calID == "" {
		return
	}
	if visible {
		delete(a.hiddenCalendars, calID)
	} else {
		if a.hiddenCalendars == nil {
			a.hiddenCalendars = make(map[string]bool)
		}
		a.hiddenCalendars[calID] = true
	}
	a.persistHiddenCalendars()
}

func (a *App) setAllCalendarsVisible(visible bool) {
	if visible {
		a.hiddenCalendars = nil
	} else {
		if a.hiddenCalendars == nil {
			a.hiddenCalendars = make(map[string]bool, len(a.calendars))
		}
		for _, cal := range a.calendars {
			a.hiddenCalendars[cal.ID] = true
		}
	}
	a.persistHiddenCalendars()
}

func calendarViewModeFromName(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "month":
		return 0
	case "week":
		return 1
	case "day":
		return 2
	case "agenda":
		return 3
	default:
		return 2 // Day view with live current-time line as default
	}
}

func calendarViewNameFromMode(mode int) string {
	switch mode {
	case 0:
		return "month"
	case 1:
		return "week"
	case 3:
		return "agenda"
	default:
		return "day"
	}
}

func (a *App) persistCalendarView() {
	if a.opts.ConfigSaver == nil {
		return
	}
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	cfg.App.CalendarView = calendarViewNameFromMode(a.calViewMode)
	cfg = config.Normalize(cfg)
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
}

func (a *App) persistHiddenCalendars() {
	if a.opts.ConfigSaver == nil {
		return
	}
	var hidden []string
	for id, off := range a.hiddenCalendars {
		if off && id != "" {
			hidden = append(hidden, id)
		}
	}
	sort.Strings(hidden)
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	cfg.App.HiddenCalendars = hidden
	cfg = config.Normalize(cfg)
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
}

func (a *App) visibleCalendarCount() (visible, total int) {
	total = len(a.calendars)
	for _, cal := range a.calendars {
		if !a.isCalendarHidden(cal.ID) {
			visible++
		}
	}
	return visible, total
}

func (a *App) calendarFilterButtonLabel() string {
	vis, total := a.visibleCalendarCount()
	if total == 0 || vis == total {
		return a.tr("Alle Kalender", "All Calendars")
	}
	if vis == 0 {
		return a.tr("Keine Kalender", "No Calendars")
	}
	if vis == 1 {
		for _, cal := range a.calendars {
			if !a.isCalendarHidden(cal.ID) {
				return truncateText(cal.Name, 18)
			}
		}
	}
	return a.trf("%d / %d Kalender", "%d / %d Calendars", vis, total)
}

func (a *App) renderCalendarFilterButton(c *ui.Context) {
	t := c.Theme()
	vis, total := a.visibleCalendarCount()
	filtered := total > 0 && vis < total

	btn := ui.ButtonBase(c.Key("tb-cal-filter")).Width(175).Padding(5, 10).Radius(7).
		Background(t.Background).Border(1, t.Border).Label(a.calendarFilterButtonLabel())
	if filtered {
		btn.Border(1, t.Accent)
	}
	btn.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconCal).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, a.calendarFilterButtonLabel()).FontSize(12).SingleLine().Grow(1).MinWidth(0)
			ui.Icon(c, iconChevronDn).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
		})
	})
	if btn.Clicked() {
		a.calFilterOpen = !a.calFilterOpen
	}

	ui.Popover(c.Key("tb-cal-pop"), btn, &a.calFilterOpen, func() {
		ui.Column(c).Width(280).MaxHeight(360).Padding(10).Gap(8).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, a.tr("Kalender auswählen", "Select Calendars")).FontSize(12).Bold().Grow(1)
				if ui.Button(c.Key("cal-flt-all"), a.tr("Alle", "All")).Padding(2, 7).Clicked() {
					a.setAllCalendarsVisible(true)
				}
				if ui.Button(c.Key("cal-flt-none"), a.tr("Keine", "None")).Padding(2, 7).Clicked() {
					a.setAllCalendarsVisible(false)
				}
			})
			ui.Divider(c)
			ui.Scroll(c.Key("cal-flt-scroll")).MaxHeight(280).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					for _, cal := range a.calendars {
						calCopy := cal
						checked := !a.isCalendarHidden(calCopy.ID)
						clr := t.Accent
						if parsed, ok := parseHexColorUI(calCopy.Color); ok {
							clr = parsed
						}
						ui.Row(c.Key("cal-flt-row-"+calCopy.ID)).FillWidth().Gap(8).AlignItems(ui.Center).Padding(2, 4).Children(func() {
							ui.Box(c).Size(9, 9).Radius(5).Background(clr).Shrink(0)
							if ui.Checkbox(c.Key("cal-flt-chk-"+calCopy.ID), &checked, calCopy.Name).Changed() {
								a.setCalendarVisible(calCopy.ID, checked)
							}
						})
					}
				})
			})
		})
	})
}

// filteredCalendarEvents applies both the multi-select calendar filter and calSearchQuery
// so Month, Week, Agenda, and Search always stay in sync.
func (a *App) filteredCalendarEvents() []backend.CalendarEvent {
	q := strings.ToLower(strings.TrimSpace(a.calSearchQuery))
	terms := strings.Fields(q)
	out := make([]backend.CalendarEvent, 0, len(a.events))
	for _, ev := range a.events {
		if a.isCalendarHidden(ev.CalendarID) {
			continue
		}
		if len(terms) > 0 {
			hay := strings.ToLower(ev.Title + " " + ev.Location + " " + cleanEventPreviewText(ev.Description) + " " + ev.Calendar)
			matched := true
			for _, term := range terms {
				if !strings.Contains(hay, term) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

var monthNamesDE = [...]string{
	"", "Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

var monthNamesEN = [...]string{
	"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

var weekdayShortDE = [...]string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}
var weekdayShortEN = [...]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
var weekdayLongDE = [...]string{"Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"}
var weekdayLongEN = [...]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func (a *App) calColorForEvent(ev backend.CalendarEvent, fallback ui.Color) ui.Color {
	for _, cal := range a.calendars {
		if cal.ID == ev.CalendarID && strings.HasPrefix(cal.Color, "#") {
			if c, ok := parseHexColorUI(cal.Color); ok {
				return c
			}
		}
	}
	return fallback
}

func parseHexColorUI(hex string) (ui.Color, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(s) != 6 {
		return ui.Color{}, false
	}
	r, err1 := strconv.ParseUint(s[0:2], 16, 8)
	g, err2 := strconv.ParseUint(s[2:4], 16, 8)
	b, err3 := strconv.ParseUint(s[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(r), uint8(g), uint8(b)), true
}

func (a *App) ensureCalCursor() time.Time {
	if a.calCursor.IsZero() {
		a.calCursor = time.Now()
	}
	return a.calCursor
}

func (a *App) stepCalendar(ctx context.Context, delta int) {
	cur := a.ensureCalCursor()
	switch a.calViewMode {
	case 0: // Month
		a.calCursor = time.Date(cur.Year(), cur.Month()+time.Month(delta), 1, 12, 0, 0, 0, cur.Location())
	case 1: // Week
		a.calCursor = cur.AddDate(0, 0, delta*7)
	case 2: // Day
		a.calCursor = cur.AddDate(0, 0, delta)
		a.calDayScrolledKey = ""
	default:
		a.calCursor = cur.AddDate(0, 0, delta*max(a.calDays, 7))
	}
	a.RefreshAll(ctx)
}

func (a *App) calendarPeriodLabel() string {
	cur := a.ensureCalCursor()
	if a.calViewMode == 0 {
		m := monthNamesDE[cur.Month()]
		if a.uiLang() == "en" {
			m = monthNamesEN[cur.Month()]
		}
		return fmt.Sprintf("%s %d", m, cur.Year())
	}
	if a.calViewMode == 1 {
		offset := (int(cur.Weekday()) + 6) % 7
		mon := time.Date(cur.Year(), cur.Month(), cur.Day()-offset, 0, 0, 0, 0, cur.Location())
		sun := mon.AddDate(0, 0, 6)
		_, kw := mon.ISOWeek()
		prefix := "KW"
		if a.uiLang() == "en" {
			prefix = "W"
		}
		return fmt.Sprintf("%s %d · %02d.%02d. – %02d.%02d.%d", prefix, kw, mon.Day(), int(mon.Month()), sun.Day(), int(sun.Month()), sun.Year())
	}
	if a.calViewMode == 2 {
		wdIdx := (int(cur.Weekday()) + 6) % 7
		kwLbl := a.calendarWeekLabel(cur)
		if a.uiLang() == "en" {
			if a.isPhone() {
				return fmt.Sprintf("%s, %s %d · %s", weekdayShortEN[wdIdx], monthNamesEN[cur.Month()][:3], cur.Day(), kwLbl)
			}
			return fmt.Sprintf("%s, %s %d, %d · %s", weekdayLongEN[wdIdx], monthNamesEN[cur.Month()], cur.Day(), cur.Year(), kwLbl)
		}
		if a.isPhone() {
			return fmt.Sprintf("%s, %02d.%02d.%d · %s", weekdayShortDE[wdIdx], cur.Day(), int(cur.Month()), cur.Year(), kwLbl)
		}
		return fmt.Sprintf("%s, %d. %s %d · %s", weekdayLongDE[wdIdx], cur.Day(), monthNamesDE[cur.Month()], cur.Year(), kwLbl)
	}
	return a.tr("Kalender & Termine", "Calendar & Events")
}

func (a *App) calendarWeekLabel(day time.Time) string {
	_, kw := day.ISOWeek()
	if a.uiLang() == "en" {
		return fmt.Sprintf("W%d", kw)
	}
	return fmt.Sprintf("KW %d", kw)
}

func (a *App) recurrenceBadgeText(ev backend.CalendarEvent) string {
	recIdx, _, _ := parseEventRRule(ev.RecurrenceRule)
	switch recIdx {
	case 1:
		return a.tr("Täglich", "Daily")
	case 2:
		return a.tr("Wöchentlich", "Weekly")
	case 3:
		return a.tr("Monatlich", "Monthly")
	case 4:
		return a.tr("Jährlich", "Yearly")
	default:
		return a.tr("Serie", "Recurring")
	}
}

func (a *App) renderCalendarWorkspace(c *ui.Context) {
	t := c.Theme()
	a.ensureCalCursor()

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		// Top toolbar: title + view switcher + navigation + actions
		ui.Row(c).FillWidth().Padding(10, 16).Gap(8).AlignItems(ui.Center).Wrap().BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, a.calendarPeriodLabel()).FontSize(15).Bold().MinWidth(160)

			// Navigation buttons (Month / Week / Day)
			if a.calViewMode == 0 || a.calViewMode == 1 || a.calViewMode == 2 {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("cal-prev"), "‹").Padding(4, 9).Clicked() {
						a.stepCalendar(context.Background(), -1)
					}
					if ui.Button(c.Key("cal-today"), a.tr("Heute", "Today")).Padding(4, 10).Clicked() {
						a.calCursor = time.Now()
						a.calDayScrolledKey = ""
						a.RefreshAll(context.Background())
					}
					if ui.Button(c.Key("cal-next"), "›").Padding(4, 9).Clicked() {
						a.stepCalendar(context.Background(), 1)
					}
				})
			} else {
				winIdx := 1
				switch a.calDays {
				case 7:
					winIdx = 0
				case 30:
					winIdx = 2
				}
				if ui.Segmented(c.Key("cal-days"), &winIdx, a.tr("7 Tage", "7 Days"), a.tr("14 Tage", "14 Days"), a.tr("30 Tage", "30 Days")).Changed() {
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
			}

			ui.Spacer(c)

			// Mode switcher: Month | Week | Day | Agenda
			if pillTabs(c, "cal-mode", &a.calViewMode, []string{a.tr("Monat", "Month"), a.tr("Woche", "Week"), a.tr("Tag", "Day"), a.tr("Agenda", "Agenda")}, false, false) {
				if a.calViewMode == 2 {
					a.calDayScrolledKey = ""
				}
				a.persistCalendarView()
				a.RefreshAll(context.Background())
			}

			if a.isDirectMode() {
				if iconBtn(c.Key("cal-sync"), iconRefresh, a.tr("Kalender synchronisieren", "Sync calendars"), ui.Color{}, false).Clicked() {
					a.syncDirectCalendarsNow(context.Background())
				}
				if iconLabelBtn(c.Key("cal-cfg"), iconCal, a.tr("Google / .ics", "Google / .ics"), false, a.tr("Google Kalender & iCal-Abos verwalten", "Manage Google Calendar & iCal feeds")).Clicked() {
					a.OpenDirectModal(1)
				}
			}
			if !a.readOnly || a.isDirectMode() {
				if iconLabelBtn(c.Key("cal-new"), iconCalPlus, a.tr("Neuer Termin", "New Event"), true, a.tr("Neuen Kalendereintrag anlegen", "Create new calendar event")).Clicked() {
					a.OpenNewEventModal(nil)
				}
			}
		})

		if !a.engineReady {
			a.renderOfflineBanner(c)
			return
		}

		if a.isPhone() {
			ui.Row(c.Key("ph-cal-tools")).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				sf := ui.SearchField(c.Key("ph-cal-search"), &a.calSearchQuery).Placeholder(a.tr("Termine durchsuchen", "Search calendar")).Grow(1)
				curQ := strings.TrimSpace(a.calSearchQuery)
				if sf.Submitted() || (sf.Changed() && ((curQ != "" && a.lastCalSearch == "") || (curQ == "" && a.lastCalSearch != ""))) {
					a.lastCalSearch = curQ
					a.RefreshAll(context.Background())
				}
				if len(a.calendars) > 0 {
					a.renderCalendarFilterButton(c)
				}
			})
		}

		filteredEvents := a.filteredCalendarEvents()
		q := strings.TrimSpace(a.calSearchQuery)
		visCal, totalCal := a.visibleCalendarCount()
		if q != "" || (totalCal > 0 && visCal < totalCal) {
			ui.Row(c.Key("cal-filter-bar")).FillWidth().Padding(6, 16).Gap(8).AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				var parts []string
				if q != "" {
					parts = append(parts, a.trf("Suche „%s“: %d Treffer", "Search “%s”: %d matches", q, len(filteredEvents)))
				}
				if totalCal > 0 && visCal < totalCal {
					parts = append(parts, a.trf("%d / %d Kalender aktiv", "%d / %d calendars shown", visCal, totalCal))
				}
				ui.Text(c, strings.Join(parts, " · ")).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
				if q != "" {
					if ui.Button(c.Key("cal-clr-q"), a.tr("Suche leeren", "Clear Search")).Padding(2, 8).Clicked() {
						a.calSearchQuery = ""
						a.lastCalSearch = ""
						a.RefreshAll(context.Background())
					}
				}
				if totalCal > 0 && visCal < totalCal {
					if ui.Button(c.Key("cal-clr-cals"), a.tr("Alle Kalender", "All Calendars")).Padding(2, 8).Clicked() {
						a.setAllCalendarsVisible(true)
					}
				}
			})
		}

		// Group filtered events by local day key YYYY-MM-DD
		byDay := make(map[string][]backend.CalendarEvent)
		for _, ev := range filteredEvents {
			dk := eventDayKey(ev.Start)
			if dk != "" {
				byDay[dk] = append(byDay[dk], ev)
			}
		}
		for dk := range byDay {
			evs := byDay[dk]
			sort.SliceStable(evs, func(i, j int) bool {
				if evs[i].AllDay != evs[j].AllDay {
					return evs[i].AllDay
				}
				return evs[i].Start < evs[j].Start
			})
			byDay[dk] = evs
		}

		switch a.calViewMode {
		case 0:
			a.renderCalendarMonthGrid(c, byDay)
		case 1:
			a.renderCalendarWeekGrid(c, byDay)
		case 2:
			a.renderCalendarDayView(c, byDay)
		default:
			a.renderCalendarAgendaList(c, byDay)
		}
	})
}

func (a *App) renderCalendarMonthGrid(c *ui.Context, byDay map[string][]backend.CalendarEvent) {
	t := c.Theme()
	cur := a.ensureCalCursor()
	loc := cur.Location()
	firstOfMonth := time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, loc)
	offset := (int(firstOfMonth.Weekday()) + 6) % 7 // Monday = 0
	gridStart := firstOfMonth.AddDate(0, 0, -offset)
	todayKey := time.Now().Format("2006-01-02")

	// Determine 5 or 6 week rows needed
	daysInMonth := firstOfMonth.AddDate(0, 1, -1).Day()
	numWeeks := 5
	if offset+daysInMonth > 35 {
		numWeeks = 6
	}

	weekdays := weekdayShortDE
	if a.uiLang() == "en" {
		weekdays = weekdayShortEN
	}

	nowISOYear, nowISOWK := time.Now().ISOWeek()

	monthGrid := func() {
		col := ui.Column(c).Gap(6)
		if a.isPhone() {
			col.MinWidth(748).Grow(1).MinHeight(0)
		} else {
			col.Grow(1).MinHeight(0).FillWidth()
		}
		col.Children(func() {
			// KW gutter + 7-column weekday header
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				ui.Box(c.Key("cal-wd-kw")).Width(48).Shrink(0).Padding(2, 4).Center().Children(func() {
					ui.Text(c, a.tr("KW", "Wk")).FontSize(11).Bold().TextColor(withAlpha(t.TextMuted, 0.75)).SingleLine()
				})
				for i, wd := range weekdays {
					clr := t.TextMuted
					if i >= 5 {
						clr = withAlpha(t.TextMuted, 0.75)
					}
					ui.Box(c.Key("cal-wd-"+wd)).Grow(1).MinWidth(0).Padding(2, 6).Children(func() {
						ui.Text(c, wd).FontSize(11.5).Bold().TextColor(clr)
					})
				}
			})

			// Scrollable week rows
			ui.Scroll(c.Key("cal-month-scroll")).TrackScroll(&a.widgets.calScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for w := 0; w < numWeeks; w++ {
						weekIdx := w
						weekStart := gridStart.AddDate(0, 0, weekIdx*7)
						isoYear, kw := weekStart.ISOWeek()
						isCurrentWeek := isoYear == nowISOYear && kw == nowISOWK
						kwLbl := a.calendarWeekLabel(weekStart)
						ui.Row(c.Key(fmt.Sprintf("cal-wk-%d", weekIdx))).FillWidth().Gap(6).Children(func() {
							ui.Column(c.Key(fmt.Sprintf("cal-kw-col-%d", weekIdx))).Width(48).Shrink(0).Padding(4, 0, 0, 0).AlignItems(ui.Center).Children(func() {
								kwBg := withAlpha(t.Surface, 0.65)
								kwBorder := t.Border
								kwFg := t.TextMuted
								if isCurrentWeek {
									kwBg = withAlpha(t.Accent, 0.18)
									kwBorder = withAlpha(t.Accent, 0.5)
									kwFg = t.Accent
								}
								kwBtn := ui.ButtonBase(c.Key(fmt.Sprintf("cal-kw-%d", weekIdx))).FillWidth().Padding(4, 3).Radius(6).Background(kwBg).Border(1, kwBorder).Center().
									Label(kwLbl).Tooltip(a.trf("Woche %d öffnen", "Open week %d", kw))
								kwBtn.Children(func() {
									txt := ui.Text(c, kwLbl).FontSize(10.5).TextColor(kwFg).SingleLine()
									if isCurrentWeek {
										txt.Bold()
									}
								})
								if kwBtn.Clicked() {
									a.calCursor = weekStart
									a.calViewMode = 1
									a.persistCalendarView()
									a.RefreshAll(context.Background())
								}
							})
							for d := 0; d < 7; d++ {
								day := gridStart.AddDate(0, 0, weekIdx*7+d)
								dk := day.Format("2006-01-02")
								inMonth := day.Month() == cur.Month()
								isToday := dk == todayKey
								dayEvents := byDay[dk]

								cellBg := t.Surface
								if !inMonth {
									cellBg = withAlpha(t.Surface, 0.45)
								}
								cellBorder := t.Border
								if isToday {
									cellBorder = t.Accent
								}

								ui.Column(c.Key("cal-cell-"+dk)).Grow(1).MinWidth(0).MinHeight(106).Padding(6).Radius(8).Background(cellBg).Border(1, cellBorder).Gap(4).Children(func() {
									// Day header row: day number + quick "+" button
									ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
										numText := fmt.Sprintf("%d", day.Day())
										if isToday {
											ui.Box(c).Padding(1, 7).Radius(10).Background(t.Accent).Children(func() {
												ui.Text(c, numText).FontSize(11.5).Bold().TextColor(ui.RGB(255, 255, 255))
											})
										} else {
											numClr := t.Text
											if !inMonth {
												numClr = t.TextMuted
											}
											ui.Text(c, numText).FontSize(12).Bold().TextColor(numClr)
										}
										ui.Spacer(c)
										if !a.readOnly || a.isDirectMode() {
											dayCopy := day
											if ui.Button(c.Key("cal-add-"+dk), "+").Padding(0, 5).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.TextMuted).Clicked() {
												a.OpenNewEventOnDate(dayCopy)
											}
										}
									})

									// Up to 3 event chips per cell
									const maxChips = 3
									for idx, ev := range dayEvents {
										if idx >= maxChips {
											extra := len(dayEvents) - maxChips
											if ui.Button(c.Key("cal-more-"+dk), a.trf("+%d weitere", "+%d more", extra)).Padding(1, 4).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.Accent).Clicked() {
												a.calCursor = day
												a.calViewMode = 2 // Jump straight to Day view for this day
												a.calDayScrolledKey = ""
												a.RefreshAll(context.Background())
											}
											break
										}
										evCopy := ev
										evClr := a.calColorForEvent(evCopy, t.Accent)
										lbl := evCopy.Title
										if !evCopy.AllDay {
											if st, err := parseEventTime(evCopy.Start); err == nil {
												lbl = st.Local().Format("15:04") + " " + lbl
											}
										}
										if evCopy.IsRecurring || evCopy.RecurrenceRule != "" {
											lbl = "↻ " + lbl
										}
										chip := ui.Button(c.Key(fmt.Sprintf("cal-chip-%s-%d", dk, idx)), truncateText(lbl, 30)).
											FillWidth().
											Padding(2, 5).
											Radius(4).
											Background(withAlpha(evClr, 0.16)).
											Border(1, withAlpha(evClr, 0.45)).
											TextColor(t.Text)
										if chip.Clicked() {
											a.OpenEventDetails(evCopy)
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
	if a.isPhone() {
		ui.ScrollHorizontal(c.Key("cal-month-hscroll")).Grow(1).MinHeight(0).FillWidth().Padding(8, 10).Children(monthGrid)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(10, 14).Children(monthGrid)
}

func (a *App) renderCalendarWeekGrid(c *ui.Context, byDay map[string][]backend.CalendarEvent) {
	t := c.Theme()
	cur := a.ensureCalCursor()
	loc := cur.Location()
	offset := (int(cur.Weekday()) + 6) % 7
	weekStart := time.Date(cur.Year(), cur.Month(), cur.Day()-offset, 0, 0, 0, 0, loc)
	todayKey := time.Now().Format("2006-01-02")

	weekdays := weekdayShortDE
	if a.uiLang() == "en" {
		weekdays = weekdayShortEN
	}

	// Precompute days, all-day events and the timed events laid out like the day view.
	days := make([]time.Time, 7)
	dayKeys := make([]string, 7)
	allDay := make([][]backend.CalendarEvent, 7)
	blocks := make([][]dayEventBlock, 7)
	firstMin := float32(-1)
	for d := 0; d < 7; d++ {
		days[d] = weekStart.AddDate(0, 0, d)
		dayKeys[d] = days[d].Format("2006-01-02")
		for _, ev := range byDay[dayKeys[d]] {
			if ev.AllDay {
				allDay[d] = append(allDay[d], ev)
			}
		}
		blocks[d] = layoutDayEvents(byDay[dayKeys[d]], days[d])
		if len(blocks[d]) > 0 && (firstMin < 0 || blocks[d][0].startMin < firstMin) {
			firstMin = blocks[d][0].startMin
		}
	}

	const gutterW float32 = 52
	const hourHeight float32 = 48
	const minuteH = hourHeight / 60
	const totalHeight = 24*hourHeight + 12
	now := time.Now().In(loc)
	nowY := (float32(now.Hour()*60+now.Minute()) + float32(now.Second())/60) * minuteH
	todayIdx := -1
	for d := range dayKeys {
		if dayKeys[d] == todayKey {
			todayIdx = d
		}
	}

	// Scroll once per week to the start of the working day (07:00), or earlier when an event of
	// the week starts earlier: a week is read from the morning on, unlike a single day.
	if wk := "wk:" + dayKeys[0]; a.calDayScrolledKey != wk {
		a.calDayScrolledKey = wk
		targetY := 7*hourHeight - hourHeight/4
		if firstMin >= 0 && firstMin*minuteH-hourHeight/4 < targetY {
			targetY = firstMin*minuteH - hourHeight/4
		}
		a.widgets.calWeekScroll.Y = max(0, min(targetY, totalHeight-360))
	}

	weekGrid := func() {
		col := ui.Column(c).Gap(6)
		if a.isPhone() {
			col.MinWidth(740).Grow(1).MinHeight(0)
		} else {
			col.Grow(1).MinHeight(0).FillWidth()
		}
		col.Children(func() {
			// Day column headers (with ISO calendar week badge in top-left gutter)
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c.Key("cal-wk-kw")).Width(gutterW).Shrink(0).Padding(4, 2).Radius(6).Background(withAlpha(t.Accent, 0.14)).Border(1, withAlpha(t.Accent, 0.4)).Center().Children(func() {
					ui.Text(c, a.calendarWeekLabel(days[0])).FontSize(10.5).Bold().TextColor(t.Accent).SingleLine()
				})
				for d := 0; d < 7; d++ {
					dayCopy := days[d]
					isToday := dayKeys[d] == todayKey
					hdrBg := t.Surface
					hdrBorder := t.Border
					if isToday {
						hdrBg = withAlpha(t.Accent, 0.14)
						hdrBorder = t.Accent
					}
					ui.Row(c.Key("cal-wkhdr-"+dayKeys[d])).Grow(1).MinWidth(0).Padding(5, 8).Radius(6).Background(hdrBg).Border(1, hdrBorder).AlignItems(ui.Center).Children(func() {
						hdrClr := t.Text
						if isToday {
							hdrClr = t.Accent
						}
						if ui.Button(c.Key("cal-wkday-"+dayKeys[d]), fmt.Sprintf("%s %02d.%02d.", weekdays[d], dayCopy.Day(), int(dayCopy.Month()))).
							Padding(0, 2).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(hdrClr).Tooltip(a.tr("Zur Tagesansicht wechseln", "Open day view")).Clicked() {
							a.calCursor = dayCopy
							a.calViewMode = 2
							a.calDayScrolledKey = ""
							a.RefreshAll(context.Background())
						}
						ui.Spacer(c)
						if !a.readOnly || a.isDirectMode() {
							if ui.Button(c.Key("cal-wkadd-"+dayKeys[d]), "+").Padding(0, 5).Background(ui.Color{}).Border(0, ui.Color{}).TextColor(t.TextMuted).Clicked() {
								a.OpenNewEventOnDate(dayCopy)
							}
						}
					})
				}
			})

			// All-day strip (shown if any all-day event exists in this week)
			hasAllDay := false
			for d := 0; d < 7; d++ {
				if len(allDay[d]) > 0 {
					hasAllDay = true
					break
				}
			}
			if hasAllDay {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
					ui.Text(c, a.tr("Ganztag", "All-day")).FontSize(10.5).TextColor(t.TextMuted).Width(gutterW).Shrink(0)
					for d := 0; d < 7; d++ {
						ui.Column(c.Key(fmt.Sprintf("cal-wkad-%d", d))).Grow(1).MinWidth(0).Gap(3).Children(func() {
							for idx, ev := range allDay[d] {
								evCopy := ev
								evClr := a.calColorForEvent(evCopy, t.Accent)
								if ui.Button(c.Key(fmt.Sprintf("cal-wkad-ev-%d-%d", d, idx)), truncateText(evCopy.Title, 20)).
									FillWidth().Padding(2, 6).Radius(4).Background(withAlpha(evClr, 0.22)).Border(1, withAlpha(evClr, 0.5)).TextColor(t.Text).Clicked() {
									a.OpenEventDetails(evCopy)
								}
							}
						})
					}
				})
			}

			// 24-hour timeline like the day view: events sit at their time, as tall as they last,
			// overlapping ones side by side; the red line marks now in today's column.
			nowClr := ui.RGB(239, 68, 68)
			ui.Scroll(c.Key("cal-week-scroll")).TrackScroll(&a.widgets.calWeekScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Row(c.Key("cal-week-grid-" + dayKeys[0])).FillWidth().Height(totalHeight).Gap(6).AlignItems(ui.Start).Children(func() {
					ui.Box(c.Key("cal-wk-gutter")).Width(gutterW).Height(totalHeight).Shrink(0).Children(func() {
						for h := 0; h < 24; h++ {
							y := float32(h) * hourHeight
							if todayIdx >= 0 && y-nowY > -12 && y-nowY < 12 {
								continue
							}
							ui.Text(c, fmt.Sprintf("%02d:00", h)).FontSize(11).TextColor(t.TextMuted).
								Absolute().Top(max(1, y-7)).Right(6)
						}
						if todayIdx >= 0 {
							ui.Box(c.Key("cal-wk-now-badge")).
								Absolute().Top(max(0, min(nowY-9, totalHeight-18))).Left(2).Right(4).ZIndex(30).
								Padding(1, 4).Radius(4).Background(nowClr).Center().
								Children(func() {
									ui.Text(c, now.Format("15:04")).FontSize(10.5).Bold().TextColor(ui.RGB(255, 255, 255)).SingleLine()
								})
						}
					})
					for d := 0; d < 7; d++ {
						a.renderCalendarWeekColumn(c, days[d], dayKeys[d], blocks[d], d == todayIdx, nowY, hourHeight, totalHeight)
					}
				})
			})
		})
	}
	if a.isPhone() {
		ui.ScrollHorizontal(c.Key("cal-week-hscroll")).Grow(1).MinHeight(0).FillWidth().Padding(8, 10).Children(weekGrid)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(8, 14).Children(weekGrid)
}

// renderCalendarWeekColumn is one day of the week view: the day view's timeline, with compact
// cards because seven columns share the width.
func (a *App) renderCalendarWeekColumn(c *ui.Context, day time.Time, dayKey string, blocks []dayEventBlock, isToday bool, nowY, hourHeight, totalHeight float32) {
	t := c.Theme()
	minuteH := hourHeight / 60
	nowClr := ui.RGB(239, 68, 68)
	canvasBorder := t.Border
	if isToday {
		canvasBorder = withAlpha(t.Accent, 0.6)
	}
	ui.Box(c.Key("cal-wk-col-"+dayKey)).Grow(1).MinWidth(0).Height(totalHeight).Radius(8).Border(1, canvasBorder).Background(t.Background).Clip().Children(func() {
		for h := 0; h < 24; h++ {
			hour := h
			rowBg := t.Background
			if hour < 7 || hour >= 20 {
				rowBg = withAlpha(t.Surface, 0.45)
			}
			slot := ui.Box(c.Key(fmt.Sprintf("cal-wk-h-%s-%02d", dayKey, hour))).
				Absolute().Top(float32(hour) * hourHeight).Left(0).Right(0).Height(hourHeight).
				Background(rowBg)
			if hour > 0 {
				slot.BorderWidth(1, 0, 0, 0).BorderColor(withAlpha(t.Border, 0.75))
			}
			if !a.readOnly || a.isDirectMode() {
				slot.OnClick(func() {
					a.OpenNewEventOnDateHour(day, hour)
				})
			}
			slot.Children(func() {
				ui.Box(c).Absolute().Top(hourHeight / 2).Left(0).Right(0).Height(1).
					Background(withAlpha(t.Border, 0.28)).PassThrough()
			})
		}

		for idx, blk := range blocks {
			evCopy := blk.ev
			evClr := a.calColorForEvent(evCopy, t.Accent)
			cardH := max((blk.endMin-blk.startMin)*minuteH-2, 20)
			colW := float32(100) / float32(blk.numCols)
			title := evCopy.Title
			if evCopy.IsRecurring || evCopy.RecurrenceRule != "" {
				title = "↻ " + title
			}
			startText := ""
			if st, err := parseEventTime(evCopy.Start); err == nil {
				startText = st.Local().Format("15:04")
			}
			ui.Box(c.Key(fmt.Sprintf("cal-wk-evwrap-%s-%d", dayKey, idx))).
				Absolute().Top(blk.startMin*minuteH+1).Height(cardH).LeftPercent(float32(blk.col)*colW).WidthPercent(colW).
				Padding(0, 2).ZIndex(10).
				Children(func() {
					card := ui.Row(c.Key(fmt.Sprintf("cal-wk-ev-%s-%d", dayKey, idx))).
						Fill().Radius(5).Background(withAlpha(evClr, 0.20)).Border(1, withAlpha(evClr, 0.55)).
						Clip().AlignItems(ui.Start).
						Tooltip(title + " · " + formatEventRangeLang(false, evCopy.Start, evCopy.End, a.uiLang()))
					card.OnClick(func() {
						a.OpenEventDetails(evCopy)
					})
					card.Children(func() {
						ui.Box(c).Width(3).FillHeight().Background(evClr).Shrink(0)
						ui.Column(c).Grow(1).MinWidth(0).Padding(2, 5).Gap(1).Children(func() {
							if cardH < 34 {
								ui.Text(c, strings.TrimSpace(startText+" "+title)).FontSize(11).Bold().TextColor(t.Text).SingleLine()
								return
							}
							ui.Text(c, title).FontSize(11.5).Bold().TextColor(t.Text).SingleLine()
							ui.Text(c, startText).FontSize(10.5).TextColor(evClr).SingleLine()
							if cardH >= 58 && evCopy.Location != "" {
								ui.Text(c, evCopy.Location).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
							}
						})
					})
				})
		}

		if isToday {
			ui.Box(c.Key("cal-wk-now-dot")).Absolute().Top(nowY-4).Left(0).Size(8, 8).Radius(4).
				Background(nowClr).ZIndex(31).PassThrough()
			ui.Box(c.Key("cal-wk-now-line")).Absolute().Top(nowY - 1).Left(0).Right(0).Height(2).
				Background(nowClr).ZIndex(30).PassThrough()
		}
	})
}

type dayEventBlock struct {
	ev       backend.CalendarEvent
	startMin float32
	endMin   float32
	visEnd   float32 // endMin clamped to at least startMin+28 so short cards don't overlap visually
	col      int
	numCols  int
}

// layoutDayEvents converts timed events for a single day into minute-positioned blocks
// and assigns side-by-side columns within each overlapping cluster.
func layoutDayEvents(evs []backend.CalendarEvent, day time.Time) []dayEventBlock {
	loc := day.Location()
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	var blocks []dayEventBlock
	for _, ev := range evs {
		if ev.AllDay {
			continue
		}
		startMin := float32(9 * 60)
		var st time.Time
		hasStart := false
		if parsed, err := parseEventTime(ev.Start); err == nil {
			st = parsed.In(loc)
			hasStart = true
			startMin = float32(st.Sub(dayStart).Minutes())
		}
		if startMin < 0 {
			startMin = 0
		}
		if startMin > 23*60+30 {
			startMin = 23*60 + 30
		}
		endMin := startMin + 60
		if hasStart && strings.TrimSpace(ev.End) != "" {
			if parsedEnd, err := parseEventTime(ev.End); err == nil {
				et := parsedEnd.In(loc)
				if et.After(st) {
					endMin = float32(et.Sub(dayStart).Minutes())
				}
			}
		}
		if endMin > 24*60 {
			endMin = 24 * 60
		}
		if endMin < startMin+15 {
			endMin = startMin + 15
		}
		visEnd := endMin
		if visEnd < startMin+28 {
			visEnd = startMin + 28
		}
		if visEnd > 24*60 {
			visEnd = 24 * 60
		}
		blocks = append(blocks, dayEventBlock{
			ev:       ev,
			startMin: startMin,
			endMin:   endMin,
			visEnd:   visEnd,
			numCols:  1,
		})
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].startMin != blocks[j].startMin {
			return blocks[i].startMin < blocks[j].startMin
		}
		return blocks[i].endMin > blocks[j].endMin
	})

	// Partition into connected overlapping clusters and assign columns greedily.
	i := 0
	for i < len(blocks) {
		clusterEnd := blocks[i].visEnd
		j := i + 1
		for j < len(blocks) && blocks[j].startMin < clusterEnd {
			if blocks[j].visEnd > clusterEnd {
				clusterEnd = blocks[j].visEnd
			}
			j++
		}
		var colEnds []float32
		for k := i; k < j; k++ {
			placed := -1
			for cIdx, end := range colEnds {
				if end <= blocks[k].startMin {
					placed = cIdx
					colEnds[cIdx] = blocks[k].visEnd
					break
				}
			}
			if placed < 0 {
				placed = len(colEnds)
				colEnds = append(colEnds, blocks[k].visEnd)
			}
			blocks[k].col = placed
		}
		nCols := len(colEnds)
		if nCols < 1 {
			nCols = 1
		}
		for k := i; k < j; k++ {
			blocks[k].numCols = nCols
		}
		i = j
	}
	return blocks
}

func (a *App) renderCalendarDayView(c *ui.Context, byDay map[string][]backend.CalendarEvent) {
	t := c.Theme()
	cur := a.ensureCalCursor()
	loc := cur.Location()
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	dayKey := day.Format("2006-01-02")
	now := time.Now().In(loc)
	todayKey := now.Format("2006-01-02")
	isToday := dayKey == todayKey

	var allDay []backend.CalendarEvent
	for _, ev := range byDay[dayKey] {
		if ev.AllDay {
			allDay = append(allDay, ev)
		}
	}
	blocks := layoutDayEvents(byDay[dayKey], day)

	const hourHeight float32 = 60 // 1px = 1 minute
	const totalHeight float32 = 24*hourHeight + 16
	nowClr := ui.RGB(239, 68, 68)
	nowY := float32(now.Hour()*60+now.Minute()) + float32(now.Second())/60.0

	// Auto-scroll to current time (today) or first timed event / 07:30 once per day switch.
	if a.calDayScrolledKey != dayKey {
		a.calDayScrolledKey = dayKey
		targetY := float32(7*60 + 30)
		if isToday {
			targetY = nowY - 120
		} else if len(blocks) > 0 {
			targetY = blocks[0].startMin - 60
		}
		if targetY < 0 {
			targetY = 0
		}
		if targetY > totalHeight-360 {
			targetY = totalHeight - 360
		}
		a.widgets.calDayScroll.Y = targetY
	}

	padH := float32(14)
	if a.isPhone() {
		padH = 8
	}

	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(8, padH).Gap(8).Children(func() {
		// All-day strip (when all-day events exist on this day)
		if len(allDay) > 0 {
			ui.Row(c.Key("cal-day-allday")).FillWidth().Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Text(c, a.tr("Ganztag", "All-day")).FontSize(11).Bold().TextColor(t.TextMuted).Width(50).Shrink(0)
				ui.Row(c).Grow(1).MinWidth(0).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					for idx, ev := range allDay {
						evCopy := ev
						evClr := a.calColorForEvent(evCopy, t.Accent)
						if ui.Button(c.Key(fmt.Sprintf("cal-day-ad-%d", idx)), evCopy.Title).
							Padding(3, 8).Radius(5).Background(withAlpha(evClr, 0.22)).Border(1, withAlpha(evClr, 0.55)).TextColor(t.Text).Clicked() {
							a.OpenEventDetails(evCopy)
						}
					}
				})
			})
		}

		// 24-hour visual timeline with proportional event blocks and live red current-time line
		ui.Scroll(c.Key("cal-day-scroll")).TrackScroll(&a.widgets.calDayScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Row(c.Key("cal-day-grid-" + dayKey)).FillWidth().Height(totalHeight).AlignItems(ui.Start).Children(func() {
				// Left hour-label gutter
				ui.Box(c.Key("cal-day-gutter")).Width(58).Height(totalHeight).Shrink(0).Children(func() {
					for h := 0; h < 24; h++ {
						y := float32(h) * hourHeight
						// Avoid colliding with the red current-time pill when viewing today.
						if isToday && (y-nowY > -14 && y-nowY < 14) {
							continue
						}
						lblTop := y - 7
						if lblTop < 0 {
							lblTop = 1
						}
						ui.Text(c, fmt.Sprintf("%02d:00", h)).
							FontSize(11).TextColor(t.TextMuted).
							Absolute().Top(lblTop).Right(8)
					}
					if isToday {
						pillTop := nowY - 9
						if pillTop < 0 {
							pillTop = 0
						}
						if pillTop > totalHeight-18 {
							pillTop = totalHeight - 18
						}
						ui.Box(c.Key("cal-day-now-badge")).
							Absolute().Top(pillTop).Left(2).Right(6).ZIndex(30).
							Padding(1, 5).Radius(4).Background(nowClr).Center().
							Children(func() {
								ui.Text(c, now.Format("15:04")).FontSize(10.5).Bold().TextColor(ui.RGB(255, 255, 255)).SingleLine()
							})
					}
				})

				// Right 24h timeline canvas
				ui.Box(c.Key("cal-day-canvas")).Grow(1).MinWidth(0).Height(totalHeight).Radius(8).Border(1, t.Border).Background(t.Background).Children(func() {
					// Hour rows (00:00..23:00), off-hours shading, half-hour hairlines & click-to-create
					for h := 0; h < 24; h++ {
						hour := h
						rowBg := t.Background
						if hour < 7 || hour >= 20 {
							rowBg = withAlpha(t.Surface, 0.45)
						}
						slot := ui.Box(c.Key(fmt.Sprintf("cal-day-h-%02d", hour))).
							Absolute().Top(float32(hour) * hourHeight).Left(0).Right(0).Height(hourHeight).
							Background(rowBg)
						if hour > 0 {
							slot.BorderWidth(1, 0, 0, 0).BorderColor(withAlpha(t.Border, 0.75))
						}
						if !a.readOnly || a.isDirectMode() {
							slot.OnClick(func() {
								a.OpenNewEventOnDateHour(day, hour)
							})
						}
						slot.Children(func() {
							ui.Box(c).Absolute().Top(hourHeight / 2).Left(0).Right(0).Height(1).
								Background(withAlpha(t.Border, 0.28)).PassThrough()
						})
					}

					// Positioned, overlap-aware event blocks
					for idx, blk := range blocks {
						evCopy := blk.ev
						evClr := a.calColorForEvent(evCopy, t.Accent)
						cardH := blk.endMin - blk.startMin - 2
						if cardH < 26 {
							cardH = 26
						}
						colW := float32(100) / float32(blk.numCols)
						colL := float32(blk.col) * colW
						timeText := formatEventRangeLang(false, evCopy.Start, evCopy.End, a.uiLang())
						joinURL, joinLabel := a.eventJoinLink(evCopy)

						ui.Box(c.Key(fmt.Sprintf("cal-day-evwrap-%d", idx))).
							Absolute().Top(blk.startMin+1).Height(cardH).LeftPercent(colL).WidthPercent(colW).
							Padding(0, 4).ZIndex(10).
							Children(func() {
								card := ui.Row(c.Key(fmt.Sprintf("cal-day-ev-%d", idx))).
									Fill().Radius(6).
									Background(withAlpha(evClr, 0.20)).
									Border(1, withAlpha(evClr, 0.55)).
									Clip().AlignItems(ui.Start)
								card.Children(func() {
									ui.Box(c).Width(4).FillHeight().Background(evClr).Shrink(0)
									info := ui.Column(c).Grow(1).MinWidth(0).FillHeight().Padding(4, 8).Gap(2)
									info.OnClick(func() {
										a.OpenEventDetails(evCopy)
									})
									info.Children(func() {
										titleText := evCopy.Title
										if evCopy.IsRecurring || evCopy.RecurrenceRule != "" {
											titleText = "↻ " + titleText
										}
										if cardH < 42 {
											// Compact 1-line layout for 15–30 min meetings
											ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
												ui.Text(c, titleText).FontSize(12).Bold().TextColor(t.Text).SingleLine().Grow(1).MinWidth(0)
												ui.Text(c, timeText).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0)
											})
										} else {
											ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
												ui.Text(c, titleText).FontSize(12.5).Bold().TextColor(t.Text).SingleLine().Grow(1).MinWidth(0)
												ui.Text(c, timeText).FontSize(11).Bold().TextColor(evClr).SingleLine().Shrink(0)
											})
											var meta []string
											if evCopy.Calendar != "" {
												meta = append(meta, evCopy.Calendar)
											}
											if evCopy.Location != "" {
												meta = append(meta, evCopy.Location)
											}
											if len(meta) > 0 {
												ui.Text(c, strings.Join(meta, " · ")).FontSize(11).TextColor(t.TextMuted).SingleLine()
											}
											if cardH >= 72 {
												if desc := cleanEventPreviewText(evCopy.Description); desc != "" {
													ui.Text(c, truncateText(desc, 160)).FontSize(11).TextColor(t.TextMuted).MaxLines(2)
												}
											}
										}
									})
									if joinURL != "" && !a.isPhone() && cardH >= 38 && blk.numCols == 1 {
										ui.Box(c).Padding(4, 6).Shrink(0).Children(func() {
											if iconLabelBtn(c.Key(fmt.Sprintf("cal-day-join-%d", idx)), iconLink, joinLabel, true, a.tr("Online-Besprechung im Browser öffnen", "Open online meeting in browser")).Clicked() {
												_ = openExternalURL(joinURL)
											}
										})
									}
								})
							})
					}

					// Live red current-time indicator line + dot (PassThrough so clicks reach events underneath)
					if isToday {
						ui.Box(c.Key("cal-day-now-dot")).
							Absolute().Top(nowY-5).Left(-5).Size(10, 10).Radius(5).
							Background(nowClr).ZIndex(31).PassThrough()
						ui.Box(c.Key("cal-day-now-line")).
							Absolute().Top(nowY - 1).Left(0).Right(0).Height(2).
							Background(nowClr).ZIndex(30).PassThrough()
					}
				})
			})
		})
	})
}

func (a *App) renderCalendarAgendaList(c *ui.Context, byDay map[string][]backend.CalendarEvent) {
	t := c.Theme()
	now := time.Now()
	loc := now.Location()
	startKey := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Format("2006-01-02")
	endKey := time.Date(now.Year(), now.Month(), now.Day()+max(a.calDays, 7), 0, 0, 0, 0, loc).Format("2006-01-02")

	searching := strings.TrimSpace(a.calSearchQuery) != ""
	var keys []string
	for dk := range byDay {
		if searching || (dk >= startKey && dk <= endKey) {
			keys = append(keys, dk)
		}
	}
	sort.Strings(keys)

	ui.Scroll(c.Key("cal-scroll")).TrackScroll(&a.widgets.calScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
			if len(keys) == 0 {
				if searching {
					ui.Text(c, a.trf("Keine Termine für „%s“ in den ausgewählten Kalendern gefunden.", "No events matching “%s” in selected calendars.", strings.TrimSpace(a.calSearchQuery))).FontSize(13).TextColor(t.TextMuted)
				} else {
					ui.Text(c, a.trf("Keine Termine in den nächsten %d Tagen.", "No events in the next %d days.", a.calDays)).FontSize(13).TextColor(t.TextMuted)
				}
				return
			}

			for _, dk := range keys {
				ui.Column(c.Key("calday-" + dk)).FillWidth().Gap(8).Children(func() {
					ui.Text(c, dayHeadingLang(dk, a.uiLang())).FontSize(12.5).Bold().TextColor(t.Accent)
					for _, ev := range byDay[dk] {
						evCopy := ev
						evClr := a.calColorForEvent(evCopy, t.Accent)
						evKey := calEventKey(evCopy)
						expanded := a.calExpanded[evKey]
						joinURL, joinLabel := a.eventJoinLink(evCopy)
						ui.Column(c.Key("cal-ev-"+evKey)).FillWidth().Padding(10, 12).Radius(8).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
							timeText := formatEventRangeLang(evCopy.AllDay, evCopy.Start, evCopy.End, a.uiLang())
							infoCol := func() {
								ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
									if a.isPhone() {
										ui.Text(c, timeText).FontSize(12).Bold().TextColor(t.Accent)
									}
									ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
										ui.Text(c, evCopy.Title).FontSize(13).Bold()
										if evCopy.IsRecurring || evCopy.RecurrenceID != "" || evCopy.RecurrenceRule != "" {
											pillBadge(c, a.recurrenceBadgeText(evCopy), t.Background, t.TextMuted)
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
									if !expanded {
										if desc := cleanEventPreviewText(evCopy.Description); desc != "" {
											ui.Text(c, truncateText(desc, 280)).FontSize(11.5).TextColor(t.TextMuted).MaxLines(2)
										}
									}
								}).OnClick(func() { a.toggleCalEventExpanded(evKey) })
							}
							actionsRow := func() {
								if joinURL != "" {
									if iconLabelBtn(c.Key("ev-join-"+evKey), iconLink, joinLabel, true, a.tr("Online-Besprechung im Browser öffnen", "Open online meeting in browser")).Clicked() {
										_ = openExternalURL(joinURL)
									}
								}
								if a.eventHasDetails(evCopy) {
									lbl := a.tr("Details", "Details")
									if expanded {
										lbl = a.tr("Weniger", "Less")
									}
									if ui.Button(c.Key("ev-more-"+evKey), lbl).Padding(3, 8).Clicked() {
										a.toggleCalEventExpanded(evKey)
									}
								}
								if (!a.readOnly || a.isDirectMode()) && !evCopy.ReadOnly {
									if iconBtn(c.Key("ev-edit-"+evKey), iconPen, a.tr("Termin bearbeiten", "Edit event"), ui.Color{}, false).Clicked() {
										a.OpenEditEventModal(evCopy)
									}
									if iconBtn(c.Key("ev-del-"+evKey), iconTrash, a.tr("Termin löschen…", "Delete event…"), t.Danger, false).Clicked() {
										a.RequestDeleteCalendarEvent(evCopy)
									}
								}
							}
							if a.isPhone() {
								ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
									ui.Box(c).Size(4, 38).Radius(2).Background(evClr).Shrink(0)
									ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
										infoCol()
										ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(actionsRow)
									})
								})
							} else {
								ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
									ui.Box(c).Size(4, 34).Radius(2).Background(evClr).Shrink(0)
									ui.Text(c, timeText).FontSize(12).Bold().TextColor(t.Accent).Width(110).Shrink(0)
									infoCol()
									actionsRow()
								})
							}
							if expanded {
								indent := float32(138)
								if a.isPhone() {
									indent = 14
								}
								ui.Column(c).FillWidth().Padding(0, 0, 0, indent).Gap(8).Children(func() {
									a.renderEventDetails(c, evCopy, "agenda-"+evKey, false)
								})
							}
						})
					}
				})
			}
		})
	})
}

// RenderEventWindow renders the detached native Calendar Event create/edit window on desktop.
func (a *App) RenderEventWindow(c *ui.Context) {
	if a.uiTheme != nil && a.uiTheme.theme != nil {
		c.SetTheme(a.uiTheme.theme)
	}
	t := c.Theme()
	c.Root().Background(t.Surface)
	if c.Shortcut(0, ui.KeyEscape) {
		a.CloseEventDialog()
	}
	ui.Column(c).Fill().Padding(16).Background(t.Surface).Gap(10).Children(func() {
		a.renderEventFormContent(c, true)
	})
}

func (a *App) renderEventModal(c *ui.Context) {
	if a.detachedAuxWindows {
		return
	}
	t := c.Theme()
	a.modal(c, &a.eventOpen, func() {
		ui.Column(c).Width(a.modalW(520)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			a.renderEventFormContent(c, false)
		})
	})
}

func (a *App) renderEventFormContent(c *ui.Context, inWindow bool) {
	t := c.Theme()
	isEdit := a.eventEditingID != ""
	if !inWindow {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.EventWindowTitle()).FontSize(15).Bold().Grow(1)
			if ui.Button(c.Key("ev-x"), "✕").Clicked() {
				a.CloseEventDialog()
			}
		})
	}
	if len(a.eventCalLabels) > 0 && !isEdit {
		ui.Select(c.Key("ev-cal"), &a.eventCalendar, a.eventCalLabels).FillWidth()
	} else if isEdit && a.eventCalendar != "" {
		ui.Text(c, a.tr("Kalender: ", "Calendar: ")+a.eventCalendar).FontSize(12).TextColor(t.TextMuted)
	}
	ui.TextInput(c.Key("ev-title"), &a.eventTitle).Placeholder(a.tr("Titel…", "Title…")).FillWidth()
	a.renderEventTimeFields(c)
	ui.Checkbox(c.Key("ev-allday"), &a.eventAllDay, a.tr("Ganztägiger Termin", "All-day event"))

	// Recurrence selector (Serientermin / RRULE)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, a.tr("Wiederholung (Serientermin)", "Recurrence (Repeating Event)")).FontSize(11.5).TextColor(t.TextMuted)
		recLabels := []string{
			a.tr("Keine", "None"),
			a.tr("Täglich", "Daily"),
			a.tr("Wöchentlich", "Weekly"),
			a.tr("Monatlich", "Monthly"),
			a.tr("Jährlich", "Yearly"),
		}
		if a.isPhone() {
			// Five segments do not fit a phone's width ("Jährlich" was cut off): a drop-down.
			sel := recLabels[min(max(a.eventRecurrence, 0), len(recLabels)-1)]
			if ui.Select(c.Key("ev-rec-sel"), &sel, recLabels).FillWidth().Changed() {
				for i, l := range recLabels {
					if l == sel {
						a.eventRecurrence = i
					}
				}
			}
		} else {
			ui.Segmented(c.Key("ev-rec"), &a.eventRecurrence, recLabels...)
		}
		if a.eventRecurrence > 0 {
			cnt := func() ui.Element {
				return ui.TextInput(c.Key("ev-rec-cnt"), &a.eventRecCount).Placeholder(a.tr("Anzahl (optional, z. B. 10)", "Count (optional, e.g. 10)"))
			}
			until := func() ui.Element {
				return ui.TextInput(c.Key("ev-rec-until"), &a.eventRecUntil).Placeholder(a.tr("Bis (JJJJ-MM-TT, optional)", "Until (YYYY-MM-DD, optional)"))
			}
			if a.isPhone() {
				cnt().FillWidth()
				until().FillWidth()
			} else {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					cnt().Grow(1)
					until().Grow(1)
				})
			}
		}
	})
	if a.eventReminderSupported() && !a.eventAllDay {
		ui.Row(c.Key("ev-rem-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Erinnerung", "Reminder")).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
			labels, values := a.eventReminderOptions()
			sel := labels[3] // 15 minutes, the default unless the settings say otherwise
			for i, v := range values {
				if v == a.eventReminder {
					sel = labels[i]
				}
			}
			if ui.Select(c.Key("ev-rem"), &sel, labels).Grow(1).MinWidth(0).Changed() {
				for i, l := range labels {
					if l == sel {
						a.eventReminder = values[i]
					}
				}
			}
		})
	}

	ui.TextInput(c.Key("ev-loc"), &a.eventLocation).Placeholder(a.tr("Ort / Meeting-Link…", "Location / Meeting link…")).FillWidth()
	a.renderEventAttendeeFields(c)
	a.renderEventTeamsRow(c)
	descArea := ui.TextArea(c.Key("ev-desc"), &a.eventDesc).Placeholder(a.tr("Notizen…", "Notes…")).FillWidth()
	if inWindow {
		descArea.Grow(1).MinHeight(120)
	} else {
		descArea.Height(86)
	}

	ui.Row(c).FillWidth().Gap(8).Children(func() {
		if isEdit && !a.eventEditingEv.ReadOnly {
			if iconLabelBtn(c.Key("ev-modal-del"), iconTrash, a.tr("Löschen", "Delete"), false, a.tr("Termin löschen…", "Delete event…")).Clicked() {
				ev := a.eventEditingEv
				a.CloseEventDialog()
				a.RequestDeleteCalendarEvent(ev)
			}
		}
		ui.Spacer(c)
		if ui.Button(c.Key("ev-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
			a.CloseEventDialog()
		}
		saveLbl := a.tr("Termin speichern", "Save Event")
		if isEdit {
			saveLbl = a.tr("Änderungen speichern", "Save Changes")
		}
		if a.eventBusy {
			saveLbl = a.tr("Speichere…", "Saving…")
		}
		if ui.PrimaryButton(c.Key("ev-save"), saveLbl).Clicked() && !a.eventBusy {
			a.SaveCalendarEvent(context.Background())
		}
	})
}

// eventInputLayout is the format of the form's start and end fields (local time).
const eventInputLayout = "2006-01-02T15:04"

// parseEventInput reads a start or end field: "2006-01-02T15:04", a date alone or RFC 3339.
func parseEventInput(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{eventInputLayout, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, true
		}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Local(), true
	}
	return time.Time{}, false
}

// eventTimeError says what is wrong with the start and end ("" when nothing): the end must come
// after the start (on or after its day for all-day events).
func (a *App) eventTimeError() string {
	start, okS := parseEventInput(a.eventStart)
	end, okE := parseEventInput(a.eventEnd)
	switch {
	case !okS:
		return a.tr("Ungültiger Beginn.", "Invalid start.")
	case strings.TrimSpace(a.eventEnd) == "":
		return ""
	case !okE:
		return a.tr("Ungültiges Ende.", "Invalid end.")
	case a.eventAllDay:
		sd := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
		ed := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.Local)
		if ed.Before(sd) {
			return a.tr("Das Ende darf nicht vor dem Beginn liegen.", "The end must not be before the start.")
		}
	case !end.After(start):
		return a.tr("Das Ende muss nach dem Beginn liegen.", "The end must be after the start.")
	}
	return ""
}

// renderEventTimeFields shows the start and end as a date and a time picker each (the time
// hidden for all-day events) – no typing of "YYYY-MM-DDTHH:MM" – and what is wrong with them.
func (a *App) renderEventTimeFields(c *ui.Context) {
	t := c.Theme()
	start, okS := parseEventInput(a.eventStart)
	if !okS {
		start = time.Now().Add(time.Hour).Truncate(time.Hour)
	}
	end, okE := parseEventInput(a.eventEnd)
	if !okE {
		end = start.Add(time.Hour)
	}
	field := func(key, label string, v *time.Time) bool {
		changed := false
		ui.Row(c.Key(key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).Width(52).Shrink(0)
			if ui.DateInput(c.Key(key+"-date"), v).Label(label).Grow(1).MinWidth(0).Changed() {
				changed = true
			}
			if !a.eventAllDay && ui.TimeInput(c.Key(key+"-time"), v).Label(label).Shrink(0).Changed() {
				changed = true
			}
		})
		return changed
	}
	was := start
	if field("ev-start", a.tr("Beginn", "Start"), &start) {
		// The end moves with the start, keeping the event's length.
		if okE {
			end = end.Add(start.Sub(was))
		}
		a.eventStart, a.eventEnd = start.Format(eventInputLayout), end.Format(eventInputLayout)
	}
	if field("ev-end", a.tr("Ende", "End"), &end) {
		a.eventStart, a.eventEnd = start.Format(eventInputLayout), end.Format(eventInputLayout)
	}
	if msg := a.eventTimeError(); msg != "" {
		ui.Text(c, msg).FontSize(12).TextColor(t.Danger).Role(ui.RoleAlert)
	}
}

// eventReminderOptions are the reminder choices and their minutes.
func (a *App) eventReminderOptions() ([]string, []int) {
	return []string{
			a.tr("Keine", "None"),
			a.tr("5 Minuten vorher", "5 minutes before"),
			a.tr("10 Minuten vorher", "10 minutes before"),
			a.tr("15 Minuten vorher", "15 minutes before"),
			a.tr("30 Minuten vorher", "30 minutes before"),
			a.tr("1 Stunde vorher", "1 hour before"),
			a.tr("1 Tag vorher", "1 day before"),
		},
		[]int{backend.ReminderNone, 5, 10, 15, 30, 60, 24 * 60}
}

// eventReminderSupported reports whether the form's calendar keeps a reminder per event: local
// calendars and CalDAV (as VALARM). Google's sync would drop it, so its events and those of the
// bridge use the default.
func (a *App) eventReminderSupported() bool {
	if !a.isDirectMode() {
		return false
	}
	id := a.eventEditCalID
	if id == "" {
		id = a.eventCalByLabel[a.eventCalendar]
	}
	if id == "" {
		return true // the engine's local calendar
	}
	for _, cal := range a.calendars {
		if cal.ID == id {
			return cal.Type == "local" || cal.Type == "caldav"
		}
	}
	return false
}
