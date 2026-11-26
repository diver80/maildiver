package mailengine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

// ParseICS parses RFC 5545 iCalendar VEVENT entries from r into StoredEvents.
func ParseICS(calendarID, calendarName string, readOnly bool, r io.Reader) ([]StoredEvent, error) {
	dec := ical.NewDecoder(r)
	var out []StoredEvent
	for {
		cal, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, fmt.Errorf("iCal parsen: %w", err)
		}
		for _, ev := range cal.Events() {
			se, ok := icalEventToStored(calendarID, calendarName, readOnly, ev)
			if ok {
				out = append(out, se)
			}
		}
	}
	return out, nil
}

func icalEventToStored(calendarID, calendarName string, readOnly bool, ev ical.Event) (StoredEvent, bool) {
	uid, _ := ev.Props.Text(ical.PropUID)
	summary, _ := ev.Props.Text(ical.PropSummary)
	location, _ := ev.Props.Text(ical.PropLocation)
	description, _ := ev.Props.Text(ical.PropDescription)
	status, _ := ev.Props.Text(ical.PropStatus)
	rruleProp := ev.Props.Get(ical.PropRecurrenceRule)
	recIDProp := ev.Props.Get(ical.PropRecurrenceID)

	startProp := ev.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return StoredEvent{}, false
	}
	startTime, allDay, err := parseICalPropTime(startProp)
	if err != nil {
		return StoredEvent{}, false
	}

	var endTime time.Time
	if endProp := ev.Props.Get(ical.PropDateTimeEnd); endProp != nil {
		if et, _, eerr := parseICalPropTime(endProp); eerr == nil {
			endTime = et
		}
	}
	if endTime.IsZero() {
		if durProp := ev.Props.Get(ical.PropDuration); durProp != nil {
			if d, derr := parseICalDuration(durProp.Value); derr == nil && d > 0 {
				endTime = startTime.Add(d)
			}
		}
	}
	if endTime.IsZero() {
		if allDay {
			endTime = startTime.AddDate(0, 0, 1)
		} else {
			endTime = startTime.Add(time.Hour)
		}
	}

	var exdates []string
	for _, p := range ev.Props.Values("EXDATE") {
		for _, raw := range strings.Split(p.Value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			tmp := ical.Prop{Name: p.Name, Params: p.Params, Value: raw}
			if t, isDate, terr := parseICalPropTime(&tmp); terr == nil {
				if isDate || allDay {
					exdates = append(exdates, t.Format("2006-01-02"))
				} else {
					exdates = append(exdates, t.UTC().Format(time.RFC3339))
				}
			}
		}
	}

	rruleVal := ""
	if rruleProp != nil {
		rruleVal = strings.TrimSpace(rruleProp.Value)
	}
	recID := ""
	if recIDProp != nil {
		if t, isDate, terr := parseICalPropTime(recIDProp); terr == nil {
			if isDate || allDay {
				recID = t.Format("2006-01-02")
			} else {
				recID = t.UTC().Format(time.RFC3339)
			}
		} else {
			recID = strings.TrimSpace(recIDProp.Value)
		}
	}
	if uid == "" {
		uid = fmt.Sprintf("ics-%d-%s", startTime.Unix(), strings.ReplaceAll(summary, " ", "_"))
	}
	id := calendarID + "::" + uid
	if recID != "" {
		id += "::" + recID
	}

	st := "confirmed"
	if strings.EqualFold(status, "CANCELLED") {
		st = "cancelled"
	}
	org, atts := icalParticipants(ev)
	seq := 0
	if p := ev.Props.Get(ical.PropSequence); p != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil && n > 0 {
			seq = n
		}
	}

	startISO := startTime.Format(time.RFC3339)
	endISO := endTime.Format(time.RFC3339)
	if allDay {
		startISO = startTime.Format("2006-01-02")
		endISO = endTime.Format("2006-01-02")
	}

	return StoredEvent{
		ID:           id,
		CalendarID:   calendarID,
		CalendarName: calendarName,
		ExternalID:   uid,
		UID:          uid,
		Title:        strings.TrimSpace(summary),
		StartISO:     startISO,
		EndISO:       endISO,
		StartUnix:    startTime.Unix(),
		EndUnix:      endTime.Unix(),
		AllDay:       allDay,
		Location:     strings.TrimSpace(location),
		Description:  strings.TrimSpace(description),
		RRule:        rruleVal,
		ExDates:      exdates,
		RecurrenceID: recID,
		IsRecurring:  rruleVal != "" || recID != "",
		ReadOnly:     readOnly,
		Status:       st,
		ReminderMin:  icalReminder(ev.Component),
		Organizer:    org,
		Attendees:    atts,
		Sequence:     seq,
	}, true
}

// icalParticipants reads an event's ORGANIZER ("Name <email>") and ATTENDEEs with their answers.
func icalParticipants(ev ical.Event) (organizer string, attendees []backend.EventAttendee) {
	if p := ev.Props.Get(ical.PropOrganizer); p != nil {
		if email := cleanMailto(p.Value); email != "" {
			organizer = formatDisplayAttendee(strings.TrimSpace(p.Params.Get(ical.ParamCommonName)), email)
		}
	}
	seen := map[string]bool{}
	for _, p := range ev.Props.Values(ical.PropAttendee) {
		email := cleanMailto(p.Value)
		if email == "" || seen[strings.ToLower(email)] {
			continue
		}
		seen[strings.ToLower(email)] = true
		name := strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(p.Params.Get(ical.ParamCommonName)))
		if strings.EqualFold(name, email) {
			name = ""
		}
		attendees = append(attendees, backend.EventAttendee{
			Email: email, Name: name, Status: partstatToStatus(p.Params.Get(ical.ParamParticipationStatus)),
		})
	}
	return organizer, attendees
}

// partstatToStatus maps an RFC 5545 PARTSTAT to the app's answer names.
func partstatToStatus(ps string) string {
	switch strings.ToUpper(strings.TrimSpace(ps)) {
	case "ACCEPTED":
		return "accepted"
	case "TENTATIVE":
		return "tentative"
	case "DECLINED":
		return "declined"
	case "NEEDS-ACTION":
		return "needsAction"
	}
	return ""
}

// statusToPartstat is partstatToStatus backwards (NEEDS-ACTION for anything unknown).
func statusToPartstat(st string) string {
	switch st {
	case "accepted":
		return "ACCEPTED"
	case "tentative":
		return "TENTATIVE"
	case "declined":
		return "DECLINED"
	}
	return "NEEDS-ACTION"
}

// ReminderNone in StoredEvent.ReminderMin / CalendarEvent.ReminderMinutes: no reminder at all
// (0 is the app's default).
const ReminderNone = backend.ReminderNone

// propNoAlarm marks an event whose reminder the user turned off, so that a CalDAV round trip
// (no VALARM) does not bring the default back.
const propNoAlarm = "X-POSTDIVE-NO-ALARM"

// icalReminder reads the first VALARM triggered before the start (TRIGGER:-PT15M) as minutes,
// ReminderNone for an event marked without one, and 0 (the default) otherwise.
func icalReminder(comp *ical.Component) int {
	if comp == nil {
		return 0
	}
	if p := comp.Props.Get(propNoAlarm); p != nil && strings.EqualFold(strings.TrimSpace(p.Value), "TRUE") {
		return ReminderNone
	}
	for _, child := range comp.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		trig := child.Props.Get(ical.PropTrigger)
		if trig == nil {
			continue
		}
		if rel := trig.Params.Get("RELATED"); rel != "" && !strings.EqualFold(rel, "START") {
			continue // relative to the end: not a "before the start" reminder
		}
		v := strings.TrimSpace(trig.Value)
		if !strings.HasPrefix(v, "-") {
			continue // absolute times and alarms at or after the start
		}
		d, err := parseICalDuration(strings.TrimPrefix(v, "-"))
		if err != nil || d <= 0 {
			continue
		}
		return max(1, int(d/time.Minute))
	}
	return 0
}

// addICalReminder writes ReminderMin as a display VALARM (or the no-alarm marker).
func addICalReminder(ev *ical.Event, title string, minutes int) {
	switch {
	case minutes == ReminderNone:
		ev.Props.Set(&ical.Prop{Name: propNoAlarm, Params: ical.Params{}, Value: "TRUE"})
	case minutes > 0:
		alarm := ical.NewComponent(ical.CompAlarm)
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		alarm.Props.SetText(ical.PropDescription, title)
		alarm.Props.Set(&ical.Prop{Name: ical.PropTrigger, Params: ical.Params{}, Value: fmt.Sprintf("-PT%dM", minutes)})
		ev.Children = append(ev.Children, alarm)
	}
}

func parseICalPropTime(p *ical.Prop) (time.Time, bool, error) {
	val := strings.TrimSpace(p.Value)
	if val == "" {
		return time.Time{}, false, fmt.Errorf("empty datetime")
	}
	loc := time.Local
	if tzid := strings.TrimSpace(p.Params.Get(ical.ParamTimezoneID)); tzid != "" {
		if loaded, err := loadTimezone(tzid); err == nil {
			loc = loaded
		}
	}
	// VALUE=DATE or YYYYMMDD => all-day event.
	if strings.EqualFold(p.Params.Get(ical.ParamValue), "DATE") || len(val) == 8 {
		t, err := time.ParseInLocation("20060102", val, loc)
		if err == nil {
			return t, true, nil
		}
	}
	if strings.HasSuffix(val, "Z") {
		if t, err := time.Parse("20060102T150405Z", val); err == nil {
			return t.In(time.Local), false, nil
		}
	}
	if t, err := time.ParseInLocation("20060102T150405", val, loc); err == nil {
		return t, false, nil
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t, false, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", val, loc); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("unsupported iCal time %q", val)
}

func loadTimezone(tzid string) (*time.Location, error) {
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, nil
	}
	// Common Windows timezone aliases used by Outlook/Exchange invites.
	windowsAliases := map[string]string{
		"W. Europe Standard Time":      "Europe/Berlin",
		"Central Europe Standard Time": "Europe/Berlin",
		"Romance Standard Time":        "Europe/Paris",
		"GMT Standard Time":            "Europe/London",
		"UTC":                          "UTC",
		"Eastern Standard Time":        "America/New_York",
		"Pacific Standard Time":        "America/Los_Angeles",
	}
	if mapped, ok := windowsAliases[tzid]; ok {
		return time.LoadLocation(mapped)
	}
	return time.Local, fmt.Errorf("unknown tz %q", tzid)
}

func parseICalDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	s = s[1:]
	var total time.Duration
	inTime := false
	num := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == 'T' {
			inTime = true
			continue
		}
		if ch >= '0' && ch <= '9' {
			num = num*10 + int(ch-'0')
			continue
		}
		switch ch {
		case 'W':
			total += time.Duration(num) * 7 * 24 * time.Hour
		case 'D':
			total += time.Duration(num) * 24 * time.Hour
		case 'H':
			total += time.Duration(num) * time.Hour
		case 'M':
			if inTime {
				total += time.Duration(num) * time.Minute
			}
		case 'S':
			total += time.Duration(num) * time.Second
		}
		num = 0
	}
	return total, nil
}

// ExpandEventsInWindow expands stored events (including RRULE + EXDATE series) inside [start, end].
func ExpandEventsInWindow(events []StoredEvent, start, end time.Time, limit int) []backend.CalendarEvent {
	// Collect RECURRENCE-ID overrides per (CalendarID, ExternalID/UID) so master RRULE expansion
	// never duplicates rescheduled or cancelled occurrences.
	overrideExDates := map[string][]time.Time{}
	for _, ev := range events {
		if ev.RecurrenceID != "" && ev.ExternalID != "" {
			if t := parseFlexibleEventTime(ev.RecurrenceID, 0); !t.IsZero() {
				k := ev.CalendarID + "\x00" + ev.ExternalID
				overrideExDates[k] = append(overrideExDates[k], t)
			}
		}
	}

	type timedEvent struct {
		ev backend.CalendarEvent
		ts time.Time
	}
	var collected []timedEvent

	inWindow := func(evStart, evEnd time.Time) bool {
		if !end.IsZero() && evStart.After(end) {
			return false
		}
		if !start.IsZero() && evEnd.Before(start) {
			return false
		}
		return true
	}

	const maxOccurrencesPerEvent = 500
	for _, ev := range events {
		if strings.EqualFold(ev.Status, "cancelled") {
			continue
		}
		dtStart := parseFlexibleEventTime(ev.StartISO, ev.StartUnix)
		dtEnd := parseFlexibleEventTime(ev.EndISO, ev.EndUnix)
		if dtEnd.IsZero() || dtEnd.Before(dtStart) {
			if ev.AllDay {
				dtEnd = dtStart.AddDate(0, 0, 1)
			} else {
				dtEnd = dtStart.Add(time.Hour)
			}
		}
		if strings.TrimSpace(ev.RRule) == "" {
			if !inWindow(dtStart, dtEnd) {
				continue
			}
			collected = append(collected, timedEvent{ev: storedEventToBridge(ev), ts: dtStart})
			continue
		}

		// Expand RRULE recurrence series within [start, end], defending against high-frequency DoS rules.
		rruleUpper := strings.ToUpper(ev.RRule)
		if strings.Contains(rruleUpper, "FREQ=SECONDLY") || strings.Contains(rruleUpper, "FREQ=MINUTELY") {
			if inWindow(dtStart, dtEnd) {
				collected = append(collected, timedEvent{ev: storedEventToBridge(ev), ts: dtStart})
			}
			continue
		}

		dur := dtEnd.Sub(dtStart)
		if dur <= 0 {
			if ev.AllDay {
				dur = 24 * time.Hour
			} else {
				dur = time.Hour
			}
		}
		allDaySpan := 1
		if ev.AllDay {
			days := int(dtEnd.Sub(dtStart).Hours()+12) / 24
			if days >= 1 {
				allDaySpan = days
			}
		}

		opt, err := rrule.StrToROptionInLocation(ev.RRule, dtStart.Location())
		if err != nil {
			if inWindow(dtStart, dtEnd) {
				collected = append(collected, timedEvent{ev: storedEventToBridge(ev), ts: dtStart})
			}
			continue
		}
		if opt.Freq == rrule.SECONDLY || opt.Freq == rrule.MINUTELY {
			if inWindow(dtStart, dtEnd) {
				collected = append(collected, timedEvent{ev: storedEventToBridge(ev), ts: dtStart})
			}
			continue
		}
		opt.Dtstart = dtStart
		rule, err := rrule.NewRRule(*opt)
		if err != nil {
			if inWindow(dtStart, dtEnd) {
				collected = append(collected, timedEvent{ev: storedEventToBridge(ev), ts: dtStart})
			}
			continue
		}
		var set rrule.Set
		set.RRule(rule)
		for _, ex := range ev.ExDates {
			if t, ok := parseStrictEventTime(ex); ok {
				set.ExDate(t.In(dtStart.Location()))
			}
		}
		if ovs := overrideExDates[ev.CalendarID+"\x00"+ev.ExternalID]; len(ovs) > 0 {
			for _, ov := range ovs {
				set.ExDate(ov.In(dtStart.Location()))
			}
		}

		winStart := start
		if winStart.IsZero() {
			winStart = time.Now().AddDate(0, -1, 0)
		}
		winEnd := end
		if winEnd.IsZero() {
			winEnd = winStart.AddDate(0, 3, 0)
		}
		if winEnd.After(winStart.AddDate(5, 0, 0)) {
			winEnd = winStart.AddDate(5, 0, 0)
		}
		occ := set.Between(winStart.Add(-dur), winEnd, true)
		emitted := 0
		for _, o := range occ {
			if emitted >= maxOccurrencesPerEvent {
				break
			}
			var occEnd time.Time
			if ev.AllDay {
				// Calendar-day arithmetic stays immune to 23h/25h DST transition days.
				occEnd = o.AddDate(0, 0, allDaySpan)
			} else {
				occEnd = o.Add(dur)
			}
			if occEnd.Before(winStart) || o.After(winEnd) {
				continue
			}
			ce := storedEventToBridge(ev)
			ce.IsRecurring = true
			if ev.AllDay {
				ce.RecurrenceID = o.Format("2006-01-02")
				ce.Start = o.Format("2006-01-02")
				ce.End = occEnd.Format("2006-01-02")
			} else {
				ce.RecurrenceID = o.UTC().Format(time.RFC3339)
				ce.Start = o.Format(time.RFC3339)
				ce.End = occEnd.Format(time.RFC3339)
			}
			collected = append(collected, timedEvent{ev: ce, ts: o})
			emitted++
		}
	}

	sort.Slice(collected, func(i, j int) bool {
		if !collected[i].ts.Equal(collected[j].ts) {
			return collected[i].ts.Before(collected[j].ts)
		}
		return collected[i].ev.ID < collected[j].ev.ID
	})
	if limit > 0 && len(collected) > limit {
		collected = collected[:limit]
	}
	out := make([]backend.CalendarEvent, len(collected))
	for i, item := range collected {
		out[i] = item.ev
	}
	return out
}

func storedEventToBridge(ev StoredEvent) backend.CalendarEvent {
	return backend.CalendarEvent{
		ID:              ev.ID,
		CalendarID:      ev.CalendarID,
		Calendar:        ev.CalendarName,
		Title:           ev.Title,
		Start:           ev.StartISO,
		End:             ev.EndISO,
		AllDay:          ev.AllDay,
		Location:        ev.Location,
		Description:     ev.Description,
		ReadOnly:        ev.ReadOnly,
		RecurrenceID:    ev.RecurrenceID,
		RecurrenceRule:  ev.RRule,
		IsRecurring:     ev.IsRecurring || ev.RRule != "",
		ResponseStatus:  ev.SelfResponse,
		ReminderMinutes: ev.ReminderMin,
		Organizer:       ev.Organizer,
		Attendees:       ev.Attendees,
	}
}

func cleanMailto(val string) string {
	v := strings.TrimSpace(val)
	if len(v) >= 7 && strings.EqualFold(v[:7], "mailto:") {
		v = strings.TrimSpace(v[7:])
	}
	if idx := strings.IndexByte(v, '?'); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	if v == "" || strings.ContainsAny(v, "\r\n\x00") {
		return ""
	}
	if a, err := mail.ParseAddress(v); err == nil && a.Address != "" {
		return strings.TrimSpace(a.Address)
	}
	return ""
}

func formatDisplayAttendee(cn, email string) string {
	cn = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(cn))
	email = strings.TrimSpace(email)
	if cn != "" && !strings.EqualFold(cn, email) {
		if email != "" {
			if strings.ContainsAny(cn, `,;"<>()@:\[]`) {
				return (&mail.Address{Name: cn, Address: email}).String()
			}
			return cn + " <" + email + ">"
		}
		return cn
	}
	return email
}

// ParseICSInviteMeta extracts Organizer and Attendees from an RFC 5545 .ics invite.
func ParseICSInviteMeta(rawICS string) (organizer string, attendees []backend.EventAttendee) {
	cal, err := ical.NewDecoder(strings.NewReader(rawICS)).Decode()
	if err != nil {
		return "", nil
	}
	for _, ev := range cal.Events() {
		return icalParticipants(ev)
	}
	return "", nil
}

// ICSMethod is the iTIP METHOD of an .ics payload, upper case ("REQUEST", "REPLY", "CANCEL"…).
func ICSMethod(rawICS string) string {
	cal, err := ical.NewDecoder(strings.NewReader(rawICS)).Decode()
	if err != nil {
		return ""
	}
	m, _ := cal.Props.Text(ical.PropMethod)
	return strings.ToUpper(strings.TrimSpace(m))
}

// ParseICSSelfPartStat extracts the current user's RFC 5545 PARTSTAT from an .ics VEVENT
// ("accepted", "tentative", "declined", "needsAction", or "").
func ParseICSSelfPartStat(rawICS string, ownEmails []string) string {
	if strings.TrimSpace(rawICS) == "" || len(ownEmails) == 0 {
		return ""
	}
	want := make(map[string]bool, len(ownEmails))
	for _, em := range ownEmails {
		if e := strings.ToLower(strings.TrimSpace(em)); e != "" {
			want[e] = true
		}
	}
	dec := ical.NewDecoder(strings.NewReader(rawICS))
	cal, err := dec.Decode()
	if err != nil {
		return ""
	}
	for _, ev := range cal.Events() {
		for _, att := range ev.Props.Values(ical.PropAttendee) {
			email := strings.ToLower(cleanMailto(att.Value))
			if !want[email] {
				continue
			}
			switch strings.ToUpper(strings.TrimSpace(att.Params.Get(ical.ParamParticipationStatus))) {
			case "ACCEPTED":
				return "accepted"
			case "TENTATIVE":
				return "tentative"
			case "DECLINED":
				return "declined"
			case "NEEDS-ACTION":
				return "needsAction"
			}
		}
		break
	}
	return ""
}

// BuildICSRSVP creates an RFC 5546 iTIP METHOD:REPLY calendar payload for an incoming .ics invite.
// status should be "accepted", "tentative", or "declined".
func BuildICSRSVP(rawICS, attendeeEmail, attendeeName, status string) (replyICS, organizerEmail, summary string, err error) {
	partstat := "ACCEPTED"
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accepted", "accept":
		partstat = "ACCEPTED"
	case "tentative", "maybe":
		partstat = "TENTATIVE"
	case "declined", "decline":
		partstat = "DECLINED"
	default:
		return "", "", "", fmt.Errorf("unbekannter RSVP-Status: %q", status)
	}

	dec := ical.NewDecoder(strings.NewReader(rawICS))
	inCal, err := dec.Decode()
	if err != nil {
		return "", "", "", fmt.Errorf("iCal-Einladung konnte nicht gelesen werden: %w", err)
	}
	if m, _ := inCal.Props.Text(ical.PropMethod); m != "" && !strings.EqualFold(strings.TrimSpace(m), "REQUEST") {
		return "", "", "", fmt.Errorf("iCal-Methode %q unterstützt keine RSVP-Antwort", m)
	}
	evs := inCal.Events()
	if len(evs) == 0 {
		return "", "", "", fmt.Errorf("keine VEVENT-Einladung enthalten")
	}
	src := evs[0]
	uid, _ := src.Props.Text(ical.PropUID)
	if strings.TrimSpace(uid) == "" {
		return "", "", "", fmt.Errorf("iCal-Einladung enthält keine UID")
	}
	summary, _ = src.Props.Text(ical.PropSummary)

	reply := ical.NewCalendar()
	reply.Props.SetText(ical.PropVersion, "2.0")
	reply.Props.SetText(ical.PropProductID, "-//PostDive//Calendar//EN")
	reply.Props.SetText(ical.PropMethod, "REPLY")

	for _, ch := range inCal.Children {
		if ch != nil && strings.EqualFold(ch.Name, ical.CompTimezone) {
			reply.Children = append(reply.Children, ch)
		}
	}

	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	if summary != "" {
		ev.Props.SetText(ical.PropSummary, summary)
	}
	if p := src.Props.Get(ical.PropDateTimeStart); p != nil {
		ev.Props.Set(p)
	}
	if p := src.Props.Get(ical.PropDateTimeEnd); p != nil {
		ev.Props.Set(p)
	}
	if p := src.Props.Get(ical.PropDuration); p != nil {
		ev.Props.Set(p)
	}
	if p := src.Props.Get(ical.PropRecurrenceID); p != nil {
		ev.Props.Set(p)
	}
	if p := src.Props.Get(ical.PropSequence); p != nil {
		ev.Props.Set(p)
	}
	if org := src.Props.Get(ical.PropOrganizer); org != nil {
		ev.Props.Set(org)
		organizerEmail = cleanMailto(org.Value)
	}
	if loc, _ := src.Props.Text(ical.PropLocation); loc != "" {
		ev.Props.SetText(ical.PropLocation, loc)
	}

	attProp := ical.NewProp(ical.PropAttendee)
	attProp.Value = "mailto:" + strings.TrimSpace(attendeeEmail)
	if strings.TrimSpace(attendeeName) != "" {
		attProp.Params.Set(ical.ParamCommonName, strings.TrimSpace(attendeeName))
	}
	attProp.Params.Set(ical.ParamParticipationStatus, partstat)
	ev.Props.Add(attProp)

	reply.Children = append(reply.Children, ev.Component)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(reply); err != nil {
		return "", "", "", err
	}
	return buf.String(), organizerEmail, summary, nil
}

func parseStrictEventTime(iso string) (time.Time, bool) {
	iso = strings.TrimSpace(iso)
	if iso == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, iso, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseFlexibleEventTime(iso string, fallbackUnix int64) time.Time {
	if t, ok := parseStrictEventTime(iso); ok {
		return t
	}
	if fallbackUnix > 0 {
		return time.Unix(fallbackUnix, 0)
	}
	return time.Now()
}

// ExportEventsICS serializes one or more StoredEvents into an RFC 5545 .ics calendar document.
func ExportEventsICS(calName string, events []StoredEvent) string {
	return buildEventsICS(calName, events, "")
}

// buildEventsICS writes events as an .ics document. Without a method it is the calendar's own
// copy (a CalDAV resource, an export): attendees carry SCHEDULE-AGENT=CLIENT so a scheduling
// server does not mail them a second time, PostDive does that. With an iTIP method (REQUEST,
// CANCEL) it is the payload of an invitation mail: no reminder, and a cancellation is marked
// CANCELLED.
func buildEventsICS(calName string, events []StoredEvent, method string) string {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//PostDive//Calendar//EN")
	if method != "" {
		cal.Props.SetText(ical.PropMethod, method)
	}
	if calName != "" {
		cal.Props.SetText("X-WR-CALNAME", calName)
	}
	for _, e := range events {
		ev := ical.NewEvent()
		uid := e.UID
		if uid == "" {
			uid = e.ID + "@postdive.local"
		}
		ev.Props.SetText(ical.PropUID, uid)
		ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
		ev.Props.SetText(ical.PropSummary, e.Title)
		if e.Location != "" {
			ev.Props.SetText(ical.PropLocation, e.Location)
		}
		if e.Description != "" {
			ev.Props.SetText(ical.PropDescription, e.Description)
		}
		st := parseFlexibleEventTime(e.StartISO, e.StartUnix)
		en := parseFlexibleEventTime(e.EndISO, e.EndUnix)
		if e.AllDay {
			ev.Props.SetDate(ical.PropDateTimeStart, st)
			ev.Props.SetDate(ical.PropDateTimeEnd, en)
		} else {
			ev.Props.SetDateTime(ical.PropDateTimeStart, st.UTC())
			ev.Props.SetDateTime(ical.PropDateTimeEnd, en.UTC())
		}
		if e.RRule != "" {
			ev.Props.Set(&ical.Prop{Name: ical.PropRecurrenceRule, Value: e.RRule})
		}
		for _, ex := range e.ExDates {
			if et, ok := parseStrictEventTime(ex); ok {
				p := ical.NewProp("EXDATE")
				if e.AllDay {
					p.SetDate(et.Local())
				} else {
					p.SetDateTime(et.UTC())
				}
				ev.Props.Add(p)
			}
		}
		if e.RecurrenceID != "" {
			if rt, ok := parseStrictEventTime(e.RecurrenceID); ok {
				if e.AllDay {
					ev.Props.SetDate(ical.PropRecurrenceID, rt.Local())
				} else {
					ev.Props.SetDateTime(ical.PropRecurrenceID, rt.UTC())
				}
			}
		}
		if e.Sequence > 0 || method != "" {
			ev.Props.Set(&ical.Prop{Name: ical.PropSequence, Value: strconv.Itoa(max(e.Sequence, 0))})
		}
		addICalParticipants(ev, e, method == "")
		switch method {
		case "CANCEL":
			ev.Props.SetText(ical.PropStatus, "CANCELLED")
		case "":
			addICalReminder(ev, e.Title, e.ReminderMin)
		}
		cal.Children = append(cal.Children, ev.Component)
	}
	var buf bytes.Buffer
	_ = ical.NewEncoder(&buf).Encode(cal)
	return buf.String()
}

// addICalParticipants writes e's ORGANIZER and ATTENDEEs; stored marks the calendar's own copy.
func addICalParticipants(ev *ical.Event, e StoredEvent, stored bool) {
	if e.Organizer == "" && len(e.Attendees) == 0 {
		return
	}
	if org := parseAttendeeAddress(e.Organizer); org.Email != "" {
		p := ical.NewProp(ical.PropOrganizer)
		p.Value = "mailto:" + org.Email
		if org.Name != "" {
			p.Params.Set(ical.ParamCommonName, org.Name)
		}
		if stored {
			p.Params.Set("SCHEDULE-AGENT", "CLIENT")
		}
		ev.Props.Add(p)
	}
	for _, a := range e.Attendees {
		if a.Email == "" {
			continue
		}
		p := ical.NewProp(ical.PropAttendee)
		p.Value = "mailto:" + a.Email
		if a.Name != "" {
			p.Params.Set(ical.ParamCommonName, a.Name)
		}
		p.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
		p.Params.Set(ical.ParamParticipationStatus, statusToPartstat(a.Status))
		if a.Status == "" || a.Status == "needsAction" {
			p.Params.Set(ical.ParamRSVP, "TRUE")
		}
		if stored {
			p.Params.Set("SCHEDULE-AGENT", "CLIENT")
		}
		ev.Props.Add(p)
	}
}

// parseAttendeeAddress reads "Name <email>" or a bare address, including unquoted names with commas.
func parseAttendeeAddress(raw string) backend.EventAttendee {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return backend.EventAttendee{}
	}
	if a, err := mail.ParseAddress(raw); err == nil && a.Address != "" {
		name := strings.TrimSpace(a.Name)
		if strings.EqualFold(name, a.Address) {
			name = ""
		}
		return backend.EventAttendee{Email: strings.TrimSpace(a.Address), Name: name}
	}
	// Fallback for legacy unquoted display names containing commas/colons: "Becker, Thomas <t@x.de>".
	if lt := strings.LastIndexByte(raw, '<'); lt >= 0 {
		if gt := strings.IndexByte(raw[lt:], '>'); gt > 1 {
			addrPart := strings.TrimSpace(raw[lt+1 : lt+gt])
			if a, err := mail.ParseAddress(addrPart); err == nil && a.Address != "" {
				name := strings.Trim(strings.TrimSpace(raw[:lt]), `"' `)
				if strings.EqualFold(name, a.Address) {
					name = ""
				}
				return backend.EventAttendee{Email: strings.TrimSpace(a.Address), Name: name}
			}
		}
	}
	return backend.EventAttendee{}
}

// ImportICSBytes parses an .ics payload and upserts all events into calendarID,
// syncing directly to Google Calendar or CalDAV when calendarID is a remote writable calendar.
func (e *Engine) ImportICSBytes(ctx context.Context, calendarID string, data []byte) (int, error) {
	if calendarID == "" {
		calendarID = LocalCalendarID
	}
	cal, err := e.store.GetCalendar(ctx, calendarID)
	if err != nil {
		return 0, fmt.Errorf("Kalender %q nicht gefunden", calendarID)
	}
	if cal.ReadOnly {
		return 0, fmt.Errorf("Kalender %q ist schreibgeschützt", cal.Name)
	}
	events, err := ParseICS(cal.ID, cal.Name, cal.ReadOnly, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	for _, ev := range events {
		switch cal.Source {
		case "google":
			if _, err := e.importGoogleEvent(ctx, cal, ev); err != nil {
				return 0, err
			}
		case "caldav":
			if _, err := e.pushCalDAVEvent(ctx, cal, ev); err != nil {
				return 0, err
			}
		default:
			if err := e.store.UpsertEvent(ctx, ev); err != nil {
				return 0, err
			}
		}
	}
	if len(events) > 0 {
		e.emitCalendarChanged()
	}
	return len(events), nil
}

const maxICSFileBytes = 16 * 1024 * 1024

func formatByteLimit(maxBytes int64) string {
	if maxBytes >= 1024*1024 {
		return fmt.Sprintf("%d MB", maxBytes/(1024*1024))
	}
	return fmt.Sprintf("%d KB", maxBytes/1024)
}

func readBoundedRegularFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("keine reguläre Datei: %s", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("Datei überschreitet Limit von %s", formatByteLimit(maxBytes))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("Datei überschreitet Limit von %s", formatByteLimit(maxBytes))
	}
	return data, nil
}

var blockedFeedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // RFC 1122 "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("100::/64"),      // Discard-Only
	netip.MustParsePrefix("2001:db8::/32"), // Documentation
}

func isBlockedFeedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, pfx := range blockedFeedPrefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

func newSafeFeedHTTPClient(allowLoopback bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ipa := range ips {
				if !allowLoopback && isBlockedFeedIP(ipa.IP) {
					return nil, fmt.Errorf("iCal-Feed-Ziel blockiert (private/lokale IP %s)", ipa.IP.String())
				}
			}
			for _, ipa := range ips {
				conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
				if derr == nil {
					return conn, nil
				}
				err = derr
			}
			return nil, err
		},
	}
	return &http.Client{
		Timeout:   25 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("zu viele Weiterleitungen beim iCal-Feed")
			}
			scheme := strings.ToLower(req.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return fmt.Errorf("ungültiges Weiterleitungs-Schema: %s", req.URL.Scheme)
			}
			return nil
		},
	}
}

// ImportICSFile reads a local .ics file and imports all events into calendarID.
func (e *Engine) ImportICSFile(ctx context.Context, calendarID, filePath string) (int, error) {
	p := strings.TrimSpace(filePath)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	data, err := readBoundedRegularFile(p, maxICSFileBytes)
	if err != nil {
		return 0, fmt.Errorf(".ics Datei nicht lesbar: %w", err)
	}
	return e.ImportICSBytes(ctx, calendarID, data)
}

// SyncICalFeed downloads (or reads locally) an iCal subscription feed and replaces its cached events.
func (e *Engine) SyncICalFeed(ctx context.Context, feed config.ICalFeedConfig) (int, error) {
	calID := "ical-" + feed.ID
	color := feed.Color
	if color == "" {
		color = "#73daca"
	}
	if err := e.store.UpsertCalendar(ctx, StoredCalendar{
		ID:         calID,
		Name:       feed.Name,
		Source:     "ical",
		ReadOnly:   true,
		Color:      color,
		ExternalID: feed.URL,
	}); err != nil {
		return 0, err
	}

	rawURL := strings.TrimSpace(feed.URL)
	low := strings.ToLower(rawURL)
	var data []byte
	var err error
	switch {
	case strings.HasPrefix(low, "http://"),
		strings.HasPrefix(low, "https://"),
		strings.HasPrefix(low, "webcal://"):
		fetchURL := rawURL
		if strings.HasPrefix(low, "webcal://") {
			fetchURL = "https://" + fetchURL[len("webcal://"):]
		}
		parsedURL, perr := url.Parse(fetchURL)
		if perr != nil || parsedURL.Hostname() == "" {
			return 0, fmt.Errorf("ungültige iCal-Feed-URL")
		}
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
		if rerr != nil {
			return 0, rerr
		}
		req.Header.Set("User-Agent", "PostDive/1.0")
		client := newSafeFeedHTTPClient(e.allowLoopbackICal)
		resp, derr := client.Do(req)
		if derr != nil {
			return 0, fmt.Errorf("iCal-Feed Abruf fehlgeschlagen: %w", derr)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return 0, fmt.Errorf("iCal-Feed HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxICSFileBytes+1))
		if err == nil && int64(len(data)) > maxICSFileBytes {
			return 0, fmt.Errorf("iCal-Feed überschreitet Limit von 16 MB")
		}
	case strings.HasPrefix(low, "file://"):
		u, uerr := url.Parse(rawURL)
		if uerr != nil || u.Path == "" {
			return 0, fmt.Errorf("ungültige file:// URL")
		}
		data, err = readBoundedRegularFile(u.Path, maxICSFileBytes)
	default:
		return 0, fmt.Errorf("nicht unterstütztes iCal-Feed-Schema (nur https://, http://, webcal://, file://)")
	}
	if err != nil {
		return 0, fmt.Errorf("iCal-Feed lesen: %w", err)
	}

	events, err := ParseICS(calID, feed.Name, true, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	if err := e.store.ReplaceCalendarEvents(ctx, calID, events); err != nil {
		return 0, err
	}
	_ = e.store.SetCalendarSyncToken(ctx, calID, "")
	e.emitCalendarChanged()
	return len(events), nil
}
