package mailengine

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

// Invitations of local and CalDAV events go out as iTIP mails (RFC 5546) from one of the own
// accounts: REQUEST for a new or changed event, CANCEL when it is deleted or an attendee is
// taken off. Attendees answer with REPLY mails, which update their status when the mail is
// opened. Google calendars do all of this themselves (sendUpdates).

// cleanAttendees drops invalid and repeated addresses and the organizer's own one.
func cleanAttendees(in []backend.EventAttendee, organizerEmail string) []backend.EventAttendee {
	seen := map[string]bool{}
	if o := strings.ToLower(strings.TrimSpace(organizerEmail)); o != "" {
		seen[o] = true
	}
	var out []backend.EventAttendee
	for _, a := range in {
		email := strings.TrimSpace(a.Email)
		if p, err := mail.ParseAddress(email); err == nil && p.Address != "" {
			email = p.Address
		} else {
			continue
		}
		key := strings.ToLower(email)
		if seen[key] {
			continue
		}
		seen[key] = true
		name := strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(a.Name))
		if strings.EqualFold(name, email) {
			name = ""
		}
		out = append(out, backend.EventAttendee{Email: email, Name: name, Status: a.Status})
	}
	return out
}

// keepAttendeeAnswers carries the answers of attendees that stay over to the new list; new
// ones have not answered yet.
func keepAttendeeAnswers(old, updated []backend.EventAttendee) []backend.EventAttendee {
	prev := map[string]string{}
	for _, a := range old {
		prev[strings.ToLower(a.Email)] = a.Status
	}
	out := make([]backend.EventAttendee, len(updated))
	for i, a := range updated {
		if a.Status == "" {
			a.Status = prev[strings.ToLower(a.Email)]
		}
		if a.Status == "" {
			a.Status = "needsAction"
		}
		out[i] = a
	}
	return out
}

// removedAttendees are the attendees of old that updated no longer has.
func removedAttendees(old, updated []backend.EventAttendee) []backend.EventAttendee {
	keep := map[string]bool{}
	for _, a := range updated {
		keep[strings.ToLower(a.Email)] = true
	}
	var out []backend.EventAttendee
	for _, a := range old {
		if !keep[strings.ToLower(a.Email)] {
			out = append(out, a)
		}
	}
	return out
}

// accountForAddress is the own account that sends as email (its address or an alias).
func (e *Engine) accountForAddress(email string) (config.DirectAccount, string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return config.DirectAccount{}, "", false
	}
	for _, acc := range e.loadConfig().Direct.Accounts {
		if strings.EqualFold(acc.Email, email) {
			return acc, accountDisplayName(acc), true
		}
		for _, al := range acc.Aliases {
			if strings.EqualFold(al.Email, email) {
				name := strings.TrimSpace(al.DisplayName)
				if name == "" {
					name = accountDisplayName(acc)
				}
				return acc, name, true
			}
		}
	}
	return config.DirectAccount{}, "", false
}

func accountDisplayName(acc config.DirectAccount) string {
	if n := strings.TrimSpace(acc.DisplayName); n != "" {
		return n
	}
	return strings.TrimSpace(acc.Name)
}

// isOwnOrganizer says whether one of the own addresses organizes an event with this organizer
// ("" counts: an event of the own calendar that nobody else organizes).
func (e *Engine) isOwnOrganizer(organizer string, own map[string]bool) bool {
	org := parseAttendeeAddress(organizer).Email
	return org == "" || own[strings.ToLower(org)]
}

func (e *Engine) ownEmailSet() map[string]bool {
	own := map[string]bool{}
	for _, em := range e.ownEmails() {
		own[em] = true
	}
	return own
}

// organizerFor is the organizer of a new event with attendees: accountID's address, or the
// default sending account's.
func (e *Engine) organizerFor(accountID string) (string, error) {
	acc, err := e.pickOutgoingAccount(accountID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(acc.Email) == "" {
		return "", fmt.Errorf("Konto %q hat keine E-Mail-Adresse", acc.Name)
	}
	return formatDisplayAttendee(accountDisplayName(acc), acc.Email), nil
}

// inviteWhen describes when an event takes place, for the text of an invitation.
func inviteWhen(ev StoredEvent) string {
	st := parseFlexibleEventTime(ev.StartISO, ev.StartUnix).Local()
	en := parseFlexibleEventTime(ev.EndISO, ev.EndUnix).Local()
	days := [...]string{"So.", "Mo.", "Di.", "Mi.", "Do.", "Fr.", "Sa."}
	day := func(t time.Time) string { return days[t.Weekday()] + " " + t.Format("02.01.2006") }
	if ev.AllDay {
		last := en.AddDate(0, 0, -1)
		if !last.After(st) {
			return day(st) + " (ganztägig)"
		}
		return day(st) + " – " + day(last) + " (ganztägig)"
	}
	if st.YearDay() == en.YearDay() && st.Year() == en.Year() {
		return day(st) + ", " + st.Format("15:04") + "–" + en.Format("15:04") + " Uhr"
	}
	return day(st) + ", " + st.Format("15:04") + " – " + day(en) + ", " + en.Format("15:04") + " Uhr"
}

// inviteText is the plain text part of an invitation or cancellation.
func inviteText(ev StoredEvent, method string, update bool) string {
	var b strings.Builder
	switch {
	case method == "CANCEL":
		fmt.Fprintf(&b, "Der Termin „%s“ wurde abgesagt.\n\n", ev.Title)
	case update:
		fmt.Fprintf(&b, "Der Termin „%s“ wurde geändert.\n\n", ev.Title)
	default:
		fmt.Fprintf(&b, "Einladung zu „%s“\n\n", ev.Title)
	}
	fmt.Fprintf(&b, "Wann: %s\n", inviteWhen(ev))
	if ev.Location != "" {
		fmt.Fprintf(&b, "Wo: %s\n", ev.Location)
	}
	if ev.Organizer != "" {
		fmt.Fprintf(&b, "Organisator: %s\n", ev.Organizer)
	}
	if len(ev.Attendees) > 0 {
		names := make([]string, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			names = append(names, a.Display())
		}
		fmt.Fprintf(&b, "Teilnehmer: %s\n", strings.Join(names, ", "))
	}
	if d := strings.TrimSpace(ev.Description); d != "" && method != "CANCEL" {
		b.WriteString("\n" + d + "\n")
	}
	return b.String()
}

// mailEventToAttendees sends ev with method (REQUEST or CANCEL) to the attendees in to, from
// the account of the organizer. Offline the mail waits in the outbox.
func (e *Engine) mailEventToAttendees(ctx context.Context, ev StoredEvent, method string, to []backend.EventAttendee, update bool) error {
	if len(to) == 0 {
		return nil
	}
	org := parseAttendeeAddress(ev.Organizer)
	acc, name, ok := e.accountForAddress(org.Email)
	if !ok {
		return fmt.Errorf("kein eigenes Konto für den Organisator %q", ev.Organizer)
	}
	if org.Name != "" {
		name = org.Name
	}
	var rcpts, toHdr []string
	for _, a := range to {
		rcpts = append(rcpts, a.Email)
		toHdr = append(toHdr, (&mail.Address{Name: a.Name, Address: a.Email}).String())
	}
	subject := "Einladung: " + ev.Title
	switch {
	case method == "CANCEL":
		subject = "Abgesagt: " + ev.Title
	case update:
		subject = "Geändert: " + ev.Title
	}
	payload := buildEventsICS("", []StoredEvent{ev}, method)
	return e.sendCalendarMail(ctx, acc, org.Email, name, to, subject, inviteText(ev, method, update), payload, method)
}

// sendCalendarMail sends an iTIP mail (method REQUEST, CANCEL or REPLY) from fromEmail of acc
// to recipients, keeps a copy in Sent and logs it; offline it waits in the outbox.
func (e *Engine) sendCalendarMail(ctx context.Context, acc config.DirectAccount, fromEmail, fromName string, to []backend.EventAttendee, subject, text, payload, method string) error {
	var rcpts, toHdr []string
	for _, a := range to {
		rcpts = append(rcpts, a.Email)
		toHdr = append(toHdr, (&mail.Address{Name: a.Name, Address: a.Email}).String())
	}
	from := (&mail.Address{Name: fromName, Address: fromEmail}).String()
	msgID, raw := BuildCalendarMIME(from, strings.Join(toHdr, ", "), subject, text, payload, method, "")
	act := ActivityEntry{Kind: ActivitySend, AccountID: acc.ID, MessageID: msgID, Subject: subject, Peer: strings.Join(rcpts, ", "), Target: "invite"}

	pass, err := e.accountPassword(acc.ID)
	if err != nil || strings.TrimSpace(pass) == "" {
		return fmt.Errorf("kein Passwort für %s gespeichert", acc.Email)
	}
	if err := SendSMTP(ctx, acc, pass, fromEmail, rcpts, raw); err != nil {
		if !isTransientNetworkError(err) {
			return err
		}
		if qerr := e.store.EnqueueOutbox(ctx, OutboxItem{
			ID: "ob-inv-" + randomHex(6), AccountID: acc.ID, FromEmail: fromEmail, Recipients: rcpts,
			Subject: subject, Status: "queued", RawEML: raw, LastError: err.Error(),
		}); qerr != nil {
			return err
		}
		act.Target = "outbox"
		e.logActivity(ctx, act)
		e.emitOutboxChanged()
		return nil
	}
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_ = e.appendToSentFolder(postCtx, acc, raw, msgID)
	e.logActivity(postCtx, act)
	e.emitMailSync(acc.ID)
	return nil
}

// ownAttendee is the own entry among ev's attendees ("" Email when none).
func (e *Engine) ownAttendee(ev StoredEvent, own map[string]bool) backend.EventAttendee {
	for _, a := range ev.Attendees {
		if own[strings.ToLower(a.Email)] {
			return a
		}
	}
	return backend.EventAttendee{}
}

// declineEvent tells the organizer of an invitation that the own address does not come: Google
// records the answer and notifies the organizer itself, otherwise an iTIP REPLY (declined) goes
// out from the own account. ev may be a single occurrence (occurrenceOf).
func (e *Engine) declineEvent(ctx context.Context, cal StoredCalendar, ev StoredEvent) error {
	own := e.ownEmailSet()
	me := e.ownAttendee(ev, own)
	// Google answers for invitations that came through Google (the Google account is invited);
	// an invitation PostDive copied into Google from a mail is answered by mail like any other.
	gAccount := strings.ToLower(strings.TrimSpace(e.loadConfig().Direct.GoogleCalendar.AccountEmail))
	if cal.Source == "google" && gAccount != "" && e.ownAttendee(ev, map[string]bool{gAccount: true}).Email != "" {
		me = e.ownAttendee(ev, map[string]bool{gAccount: true})
		atts := make([]backend.EventAttendee, len(ev.Attendees))
		copy(atts, ev.Attendees)
		for i := range atts {
			if strings.EqualFold(atts[i].Email, me.Email) {
				atts[i].Status = "declined"
			}
		}
		// Only the answer changes: a guest may not touch the rest of someone else's event.
		return e.declineGoogle(ctx, cal, ev, atts)
	}
	org := parseAttendeeAddress(ev.Organizer)
	if org.Email == "" {
		return fmt.Errorf("der Termin nennt keinen Organisator")
	}
	var acc config.DirectAccount
	name := ""
	ok := false
	if me.Email != "" {
		acc, name, ok = e.accountForAddress(me.Email)
	}
	if !ok {
		var err error
		if acc, err = e.pickOutgoingAccount(""); err != nil {
			return err
		}
		name = accountDisplayName(acc)
		me = backend.EventAttendee{Email: acc.Email}
	}
	if me.Name != "" {
		name = me.Name
	}
	request := buildEventsICS("", []StoredEvent{ev}, "REQUEST")
	reply, _, summary, err := BuildICSRSVP(request, me.Email, name, "declined")
	if err != nil {
		return err
	}
	if summary == "" {
		summary = ev.Title
	}
	return e.sendCalendarMail(ctx, acc, me.Email, name, []backend.EventAttendee{org}, "Abgesagt: "+summary,
		fmt.Sprintf("Ich habe die Einladung „%s“ (%s) abgelehnt.", summary, inviteWhen(ev)), reply, "REPLY")
}

// occurrenceOf is the single occurrence recurrenceID of the series ev, for cancelling it.
func occurrenceOf(ev StoredEvent, recurrenceID string) StoredEvent {
	occ := ev
	st := parseFlexibleEventTime(recurrenceID, 0)
	dur := time.Duration(ev.EndUnix-ev.StartUnix) * time.Second
	occ.RecurrenceID = recurrenceID
	occ.RRule = ""
	occ.ExDates = nil
	occ.StartUnix, occ.EndUnix = st.Unix(), st.Add(dur).Unix()
	if ev.AllDay {
		occ.StartISO, occ.EndISO = st.Format("2006-01-02"), st.Add(dur).Format("2006-01-02")
	} else {
		occ.StartISO, occ.EndISO = st.Format(time.RFC3339), st.Add(dur).Format(time.RFC3339)
	}
	return occ
}

// ICSReplyResult is what an attendee answered to one of the own invitations.
type ICSReplyResult struct {
	Attendee string // "Name <email>"
	Status   string // accepted, tentative, declined
	Title    string
	Updated  bool // the event was found in a calendar and its attendee updated
}

// applyICSReply records the answer of an iTIP REPLY in the own event it answers (local or
// CalDAV; Google tracks answers itself).
func (e *Engine) applyICSReply(ctx context.Context, rawICS string) (ICSReplyResult, bool) {
	cal, err := ical.NewDecoder(strings.NewReader(rawICS)).Decode()
	if err != nil {
		return ICSReplyResult{}, false
	}
	if m, _ := cal.Props.Text(ical.PropMethod); !strings.EqualFold(strings.TrimSpace(m), "REPLY") {
		return ICSReplyResult{}, false
	}
	evs := cal.Events()
	if len(evs) == 0 {
		return ICSReplyResult{}, false
	}
	uid, _ := evs[0].Props.Text(ical.PropUID)
	title, _ := evs[0].Props.Text(ical.PropSummary)
	_, atts := icalParticipants(evs[0])
	if len(atts) == 0 || atts[0].Status == "" {
		return ICSReplyResult{}, false
	}
	who := atts[0]
	res := ICSReplyResult{Attendee: who.Display(), Status: who.Status, Title: strings.TrimSpace(title)}
	if strings.TrimSpace(uid) == "" {
		return res, true
	}
	// Answers can be opened in any order: only one newer than the last recorded counts.
	var stamp int64
	if t, err := evs[0].Props.DateTime(ical.PropDateTimeStamp, time.UTC); err == nil {
		stamp = t.Unix()
	}
	stampKey := "ics_reply:" + strings.ToLower(strings.TrimSpace(uid)) + ":" + strings.ToLower(who.Email)
	var last int64
	_ = e.store.db.QueryRowContext(ctx, `SELECT CAST(v AS INTEGER) FROM kv_meta WHERE k=?`, stampKey).Scan(&last)
	if stamp > 0 && last >= stamp {
		return res, true
	}
	_, _ = e.store.db.ExecContext(ctx, `INSERT OR REPLACE INTO kv_meta(k, v) VALUES(?, ?)`, stampKey, fmt.Sprint(stamp))
	matches, _ := e.store.FindEventsByUIDOrMatch(ctx, uid, "", 0)
	own := e.ownEmailSet()
	for _, ev := range matches {
		if !strings.EqualFold(ev.UID, uid) || !e.isOwnOrganizer(ev.Organizer, own) {
			continue
		}
		if res.Title == "" {
			res.Title = ev.Title
		}
		changed := false
		for i, a := range ev.Attendees {
			if strings.EqualFold(a.Email, who.Email) {
				if a.Status != who.Status {
					ev.Attendees[i].Status = who.Status
					changed = true
				}
				res.Updated = true
			}
		}
		if !changed {
			continue
		}
		cal, cerr := e.store.GetCalendar(ctx, ev.CalendarID)
		if cerr != nil {
			continue
		}
		switch cal.Source {
		case "google":
			continue
		case "caldav":
			if _, err := e.pushCalDAVEvent(ctx, cal, ev); err != nil {
				_ = e.store.UpsertEvent(ctx, ev)
			}
		default:
			_ = e.store.UpsertEvent(ctx, ev)
		}
		e.emitCalendarChanged()
	}
	return res, true
}

// markNotifiable sets CanNotify on events with attendees that an own address organizes, and
// CanDecline on writable invitations someone else organizes.
func (e *Engine) markNotifiable(events []backend.CalendarEvent) {
	var own map[string]bool
	for i := range events {
		if len(events[i].Attendees) == 0 {
			continue
		}
		if own == nil {
			own = e.ownEmailSet()
		}
		events[i].CanNotify = e.isOwnOrganizer(events[i].Organizer, own)
	}
	for i := range events {
		if events[i].Organizer != "" && !events[i].ReadOnly {
			if own == nil {
				own = e.ownEmailSet()
			}
			events[i].CanDecline = !e.isOwnOrganizer(events[i].Organizer, own)
		}
	}
}

// attendeesFromParam reads the attendees of an update request ([{email,name,status}]).
func attendeesFromParam(v any) ([]backend.EventAttendee, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]backend.EventAttendee, 0, len(list))
	for _, it := range list {
		switch x := it.(type) {
		case map[string]any:
			a := backend.EventAttendee{}
			a.Email, _ = x["email"].(string)
			a.Name, _ = x["name"].(string)
			a.Status, _ = x["status"].(string)
			out = append(out, a)
		case string:
			out = append(out, parseAttendeeAddress(x))
		}
	}
	return out, true
}

// icsInviteOf is the invitation card of a mail with an .ics: the event, who organizes it, who is
// invited (the mail's recipients when the invitation names nobody) and the own answer. An
// attendee's answer (REPLY) is recorded in the own event it answers.
func (e *Engine) icsInviteOf(ctx context.Context, detail backend.MessageDetail, rawICS string) *backend.CalendarEvent {
	evs, err := ParseICS(LocalCalendarID, "Persönlich", false, strings.NewReader(rawICS))
	if err != nil || len(evs) == 0 {
		return nil
	}
	ev := storedEventToBridge(evs[0])
	ev.Method = ICSMethod(rawICS)
	if ev.Method == "REPLY" {
		if r, ok := e.applyICSReply(ctx, rawICS); ok {
			ev.Attendees = []backend.EventAttendee{parseAttendeeAddress(r.Attendee)}
			ev.Attendees[0].Status = r.Status
			ev.ResponseStatus = r.Status
			if ev.Title == "" {
				ev.Title = r.Title
			}
		}
		return &ev
	}
	if len(ev.Attendees) == 0 {
		var recips []backend.EventAttendee
		for _, raw := range append(append([]string{}, detail.To...), detail.CC...) {
			if a := parseAttendeeAddress(raw); a.Email != "" {
				recips = append(recips, a)
			}
		}
		ev.Attendees = cleanAttendees(recips, parseAttendeeAddress(ev.Organizer).Email)
		ev.AttendeesFromMail = len(ev.Attendees) > 0
	}
	ev.ResponseStatus, ev.InCalendarName = e.resolveICSInviteStatus(ctx, detail.ID, rawICS, evs[0])
	return &ev
}
