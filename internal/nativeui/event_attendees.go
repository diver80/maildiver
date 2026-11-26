package nativeui

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/diver80/postdive/internal/backend"
	"github.com/egoist/mygo/ui"
)

// attendeesCollapsed is how many attendees a list shows before "+N more".
const attendeesCollapsed = 6

// ownAddressSet holds the own accounts' addresses and identities, lower case.
func (a *App) ownAddressSet() map[string]bool {
	own := map[string]bool{}
	for _, acc := range a.accounts {
		for _, id := range acc.Identities {
			if em := strings.ToLower(strings.TrimSpace(id.Email)); em != "" {
				own[em] = true
			}
		}
	}
	return own
}

// parseAttendeeList reads the attendee field: addresses separated by commas or semicolons,
// with or without names. Invalid entries are dropped, repeated ones kept once.
func parseAttendeeList(raw string) []backend.EventAttendee {
	var out []backend.EventAttendee
	seen := map[string]bool{}
	for _, item := range splitAttendeeField(raw) {
		addr, err := mail.ParseAddress(item)
		if err != nil || addr.Address == "" || seen[strings.ToLower(addr.Address)] {
			continue
		}
		seen[strings.ToLower(addr.Address)] = true
		out = append(out, backend.EventAttendee{Email: addr.Address, Name: strings.TrimSpace(addr.Name)})
	}
	return out
}

// splitAttendeeField splits the attendee field at commas and semicolons outside quotes and <>.
func splitAttendeeField(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuotes, inAngle := false, false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case ch == '\\' && inQuotes && i+1 < len(raw):
			cur.WriteByte(ch)
			i++
			ch = raw[i]
		case ch == '"':
			inQuotes = !inQuotes
		case ch == '<' && !inQuotes:
			inAngle = true
		case ch == '>' && !inQuotes:
			inAngle = false
		case (ch == ',' || ch == ';') && !inQuotes && !inAngle:
			flush()
			continue
		}
		cur.WriteByte(ch)
	}
	flush()
	return out
}

// invalidAttendees are the entries of the attendee field that are no address.
func invalidAttendees(raw string) []string {
	var bad []string
	for _, item := range splitAttendeeField(raw) {
		if _, err := mail.ParseAddress(item); err != nil {
			bad = append(bad, item)
		}
	}
	return bad
}

// formatAttendeeList is the attendee field's text for atts.
func formatAttendeeList(atts []backend.EventAttendee) string {
	parts := make([]string, 0, len(atts))
	for _, at := range atts {
		parts = append(parts, formatEmailAddress(at.Name, at.Email))
	}
	return strings.Join(parts, ", ")
}

// mailParticipants are the people of a mail – sender, To and Cc – without the own addresses,
// as attendees of an event taken from it.
func (a *App) mailParticipants(msg *backend.MessageDetail) []backend.EventAttendee {
	if msg == nil {
		return nil
	}
	own := a.ownAddressSet()
	var out []backend.EventAttendee
	seen := map[string]bool{}
	for _, raw := range append(append([]string{msg.From}, msg.To...), msg.CC...) {
		addr, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil || addr.Address == "" {
			continue
		}
		key := strings.ToLower(addr.Address)
		if own[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, backend.EventAttendee{Email: addr.Address, Name: strings.Trim(strings.TrimSpace(addr.Name), `"'`)})
	}
	return out
}

// attendeeStatusLook is the icon, color and label of an attendee's answer.
func (a *App) attendeeStatusLook(t *ui.Theme, status string) (*ui.SVG, ui.Color, string) {
	switch status {
	case "accepted":
		return iconCheck, t.Success, a.tr("zugesagt", "accepted")
	case "tentative":
		return iconHelp, t.Warning, a.tr("vielleicht", "tentative")
	case "declined":
		return iconX, t.Danger, a.tr("abgesagt", "declined")
	}
	return iconClock, t.TextMuted, a.tr("keine Antwort", "no answer")
}

// renderAttendees lists attendees with their answers, the first few unless expanded. ownStatus
// is the own answer, shown for the own entry when the list itself has none.
func (a *App) renderAttendees(c *ui.Context, key string, atts []backend.EventAttendee, ownStatus string, showStatus bool) {
	if len(atts) == 0 {
		return
	}
	t := c.Theme()
	if a.attExpanded == nil {
		a.attExpanded = map[string]bool{}
	}
	own := a.ownAddressSet()
	shown := atts
	if !a.attExpanded[key] && len(atts) > attendeesCollapsed+1 {
		shown = atts[:attendeesCollapsed]
	}
	counts := map[string]int{}
	for _, at := range atts {
		st := at.Status
		if own[strings.ToLower(at.Email)] && (st == "" || st == "needsAction") {
			st = ownStatus
		}
		counts[st]++
	}
	ui.Column(c.Key(key)).FillWidth().Gap(3).Children(func() {
		head := a.trf("Teilnehmer (%d)", "Attendees (%d)", len(atts))
		if showStatus {
			var parts []string
			if n := counts["accepted"]; n > 0 {
				parts = append(parts, a.trf("%d zugesagt", "%d accepted", n))
			}
			if n := counts["tentative"]; n > 0 {
				parts = append(parts, a.trf("%d vielleicht", "%d tentative", n))
			}
			if n := counts["declined"]; n > 0 {
				parts = append(parts, a.trf("%d abgesagt", "%d declined", n))
			}
			if len(parts) > 0 {
				head += " · " + strings.Join(parts, ", ")
			}
		}
		ui.Text(c, head).FontSize(11.5).Bold().TextColor(t.TextMuted)
		for i, at := range shown {
			st := at.Status
			if own[strings.ToLower(at.Email)] && (st == "" || st == "needsAction") {
				st = ownStatus
			}
			ui.Row(c.Key(fmt.Sprintf("%s-%d", key, i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if showStatus {
					icon, col, label := a.attendeeStatusLook(t, st)
					ui.Icon(c, icon).Size(12, 12).TextColor(col).Shrink(0).Tooltip(label)
				} else {
					ui.Icon(c, iconUser).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
				}
				ui.Text(c, at.Display()).FontSize(11.5).TextColor(t.Text).Selectable().Grow(1).MinWidth(0)
			})
		}
		if len(shown) < len(atts) {
			if ui.Button(c.Key(key+"-more"), a.trf("+%d weitere", "+%d more", len(atts)-len(shown))).Padding(2, 8).Clicked() {
				a.attExpanded[key] = true
			}
		} else if a.attExpanded[key] && len(atts) > attendeesCollapsed+1 {
			if ui.Button(c.Key(key+"-less"), a.tr("Weniger", "Less")).Padding(2, 8).Clicked() {
				a.attExpanded[key] = false
			}
		}
	})
}

// eventCalendarType is the type ("local", "caldav", "google"…) of the calendar the event form
// writes to.
func (a *App) eventCalendarType() string {
	id := a.eventEditCalID
	if id == "" {
		id = a.eventCalByLabel[a.eventCalendar]
	}
	if id == "" {
		return "local"
	}
	for _, cal := range a.calendars {
		if cal.ID == id {
			return cal.Type
		}
	}
	return ""
}

// eventAttendeesEditable: attendees can be set on new events and on own ones; an invitation
// someone else organizes keeps its list.
func (a *App) eventAttendeesEditable() bool {
	if !a.isDirectMode() {
		return false
	}
	if a.eventEditingID == "" {
		return true
	}
	ev := a.eventEditingEv
	return len(ev.Attendees) == 0 || ev.CanNotify
}

func (a *App) refreshEventAttendeeSuggestions() {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	tok := lastRecipientToken(a.eventAttendees)
	if tok == a.eventAttLastQuery {
		return
	}
	a.eventAttLastQuery = tok
	if len(tok) < 2 {
		a.eventAttSuggest = nil
		return
	}
	a.eventAttSuggest = dc.SuggestRecipients(context.Background(), tok, 5)
}

// renderEventAttendeeFields is the attendee part of the event form: the addresses with
// suggestions from the contacts, whether to mail them, from which account, and a Meet link.
func (a *App) renderEventAttendeeFields(c *ui.Context) {
	if !a.isDirectMode() {
		return
	}
	t := c.Theme()
	if !a.eventAttendeesEditable() {
		a.renderAttendees(c, "ev-att-ro", a.eventEditingEv.Attendees, a.eventEditingEv.ResponseStatus, true)
		return
	}
	isEdit := a.eventEditingID != ""
	ui.Column(c.Key("ev-att")).FillWidth().Gap(6).Children(func() {
		in := ui.TextInput(c.Key("ev-att-in"), &a.eventAttendees).Placeholder(a.tr("Teilnehmer einladen (Name oder E-Mail, mit Komma getrennt)…", "Invite attendees (name or email, comma separated)…")).FillWidth()
		if in.Changed() {
			a.refreshEventAttendeeSuggestions()
		}
		if len(a.eventAttSuggest) > 0 {
			ui.Row(c.Key("ev-att-sug")).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				for i, s := range a.eventAttSuggest {
					sCopy := s
					lbl := formatEmailAddress(sCopy.DisplayName, sCopy.Email)
					if ui.Button(c.Key(fmt.Sprintf("ev-att-sug-%d", i)), lbl).Padding(3, 8).Background(t.Background).Border(1, withAlpha(t.Accent, 0.45)).Clicked() {
						a.eventAttendees = replaceLastRecipient(a.eventAttendees, formatEmailAddress(sCopy.DisplayName, sCopy.Email))
						a.eventAttSuggest = nil
						a.eventAttLastQuery = ""
					}
				}
			})
		}
		if bad := invalidAttendees(a.eventAttendees); len(bad) > 0 && len(a.eventAttSuggest) == 0 && !strings.HasSuffix(strings.TrimSpace(a.eventAttendees), ",") {
			ui.Text(c, a.tr("Keine E-Mail-Adresse: ", "Not an email address: ")+strings.Join(bad, ", ")).FontSize(11).TextColor(t.TextMuted)
		}
		atts := parseAttendeeList(a.eventAttendees)
		hadAttendees := isEdit && len(a.eventEditingEv.Attendees) > 0
		if len(atts) > 0 || hadAttendees {
			label := a.tr("Einladung an die Teilnehmer senden", "Send the invitation to the attendees")
			if isEdit {
				label = a.tr("Teilnehmer über die Änderung informieren", "Tell the attendees about the change")
			}
			ui.Checkbox(c.Key("ev-att-send"), &a.eventSendInvites, label)
		}
		calType := a.eventCalendarType()
		if a.eventSendInvites && len(atts) > 0 && calType != "google" && !(isEdit && a.eventEditingEv.Organizer != "") && len(a.accounts) > 1 {
			labels := make([]string, 0, len(a.accounts))
			sel := ""
			for _, acc := range a.accounts {
				lbl := a.composeAccountLabelFor(acc.ID)
				labels = append(labels, lbl)
				if acc.ID == a.eventInviteAccount {
					sel = lbl
				}
			}
			if sel == "" && len(labels) > 0 {
				sel = labels[0]
			}
			ui.Row(c.Key("ev-att-acc-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Senden von", "Send from")).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
				if ui.Select(c.Key("ev-att-acc"), &sel, labels).Grow(1).MinWidth(0).Changed() {
					for i, l := range labels {
						if l == sel {
							a.eventInviteAccount = a.accounts[i].ID
						}
					}
				}
			})
		}
		if calType == "google" && !isEdit {
			ui.Checkbox(c.Key("ev-meet"), &a.eventAddMeet, a.tr("Google-Meet-Link hinzufügen", "Add a Google Meet link"))
		}
		if isEdit && len(a.eventEditingEv.Attendees) > 0 {
			a.renderAttendees(c, "ev-att-st", a.eventEditingEv.Attendees, a.eventEditingEv.ResponseStatus, true)
		}
	})
}

// replaceLastRecipient puts formatted in place of the address being typed at the end of cur.
func replaceLastRecipient(cur, formatted string) string {
	inQuotes := false
	lastComma := -1
	for i := 0; i < len(cur); i++ {
		switch cur[i] {
		case '"':
			inQuotes = !inQuotes
		case '\\':
			i++
		case ',':
			if !inQuotes {
				lastComma = i
			}
		}
	}
	if lastComma >= 0 {
		if prefix := strings.TrimSpace(cur[:lastComma]); prefix != "" {
			return prefix + ", " + formatted + ", "
		}
	}
	return formatted + ", "
}

// renderICSNoticeBanner shows an invitation mail that needs no answer: an attendee's answer to
// an own invitation (REPLY) or the organizer's cancellation (CANCEL).
func (a *App) renderICSNoticeBanner(c *ui.Context, inv *backend.CalendarEvent) {
	t := c.Theme()
	tint := t.Accent
	icon := iconCal
	title := inv.Title
	line := ""
	if inv.Method == "CANCEL" {
		tint, icon = t.Danger, iconX
		line = a.tr("Der Organisator hat diesen Termin abgesagt.", "The organizer cancelled this event.")
	} else {
		who := a.tr("Ein Teilnehmer", "An attendee")
		if len(inv.Attendees) > 0 && inv.Attendees[0].Email != "" {
			who = inv.Attendees[0].Display()
		}
		var col ui.Color
		icon, col, _ = a.attendeeStatusLook(t, inv.ResponseStatus)
		tint = col
		switch inv.ResponseStatus {
		case "accepted":
			line = a.trf("%s hat zugesagt.", "%s accepted.", who)
		case "tentative":
			line = a.trf("%s hat vorläufig zugesagt.", "%s accepted tentatively.", who)
		case "declined":
			line = a.trf("%s hat abgesagt.", "%s declined.", who)
		default:
			line = a.trf("%s hat geantwortet.", "%s answered.", who)
		}
	}
	ui.Row(c.Key("ics-notice")).FillWidth().Padding(10, 12).Radius(8).Background(withAlpha(tint, 0.10)).Border(1, withAlpha(tint, 0.45)).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(18, 18).TextColor(tint).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			if title != "" {
				ui.Text(c, title).FontSize(12.5).Bold().Selectable()
			}
			meta := line
			if inv.Start != "" {
				meta += " · " + formatDateTimeLang(inv.Start, a.uiLang())
			}
			ui.Text(c, meta).FontSize(11.5).TextColor(t.TextMuted).Selectable()
		})
	})
}
