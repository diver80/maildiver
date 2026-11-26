package nativeui

import (
	"net/url"
	"strings"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/egoist/mygo/ui"
)

// A Teams meeting with its join link can only be made by Microsoft. Without an app registered
// for the Graph API, PostDive opens Teams' own scheduling form filled in from the event form
// (Microsoft's documented deep link); the meeting is created and sent from there. Inviting an own
// PostDive address too brings the invitation back into PostDive, where accepting it puts the
// meeting into a PostDive calendar.

const teamsScheduleURL = "https://teams.microsoft.com/l/meeting/new"

// teamsQueryEscape escapes a deep link value: Teams takes no "+" for spaces, only "%20".
func teamsQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// teamsMeetingURL is the deep link to Teams' scheduling form with subject, times (ISO 8601 with
// UTC offset), notes and attendees (comma separated, no spaces). Teams takes no location.
func teamsMeetingURL(subject string, start, end time.Time, content string, attendees []backend.EventAttendee) string {
	var parts []string
	if s := strings.TrimSpace(subject); s != "" {
		parts = append(parts, "subject="+teamsQueryEscape(s))
	}
	if !start.IsZero() {
		parts = append(parts, "startTime="+teamsQueryEscape(start.Format(time.RFC3339)))
	}
	if !end.IsZero() && end.After(start) {
		parts = append(parts, "endTime="+teamsQueryEscape(end.Format(time.RFC3339)))
	}
	if c := strings.TrimSpace(content); c != "" {
		parts = append(parts, "content="+teamsQueryEscape(c))
	}
	var emails []string
	seen := map[string]bool{}
	for _, at := range attendees {
		key := strings.ToLower(strings.TrimSpace(at.Email))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		emails = append(emails, strings.TrimSpace(at.Email))
	}
	if len(emails) > 0 {
		parts = append(parts, "attendees="+teamsQueryEscape(strings.Join(emails, ",")))
	}
	if len(parts) == 0 {
		return teamsScheduleURL
	}
	return teamsScheduleURL + "?" + strings.Join(parts, "&")
}

// teamsSelfAddress is the own address an invitation to the Teams meeting should also go to, so
// it shows up in PostDive: the address of the account chosen for invitations.
func (a *App) teamsSelfAddress() string {
	id := a.eventInviteAccount
	if id == "" {
		id = a.defaultComposeAccountID("")
	}
	for _, acc := range a.cfg.Direct.Accounts {
		if acc.ID == id && strings.TrimSpace(acc.Email) != "" {
			return strings.TrimSpace(acc.Email)
		}
	}
	for _, acc := range a.cfg.Direct.Accounts {
		if strings.TrimSpace(acc.Email) != "" {
			return strings.TrimSpace(acc.Email)
		}
	}
	return ""
}

// eventFormTimes are the form's start and end (an all-day event runs from midnight to midnight).
func (a *App) eventFormTimes() (time.Time, time.Time) {
	start, okS := parseEventInput(a.eventStart)
	end, okE := parseEventInput(a.eventEnd)
	if !okS {
		return time.Time{}, time.Time{}
	}
	if a.eventAllDay {
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
		if okE {
			end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
		} else {
			end = start.AddDate(0, 0, 1)
		}
		return start, end
	}
	if !okE || !end.After(start) {
		end = start.Add(time.Hour)
	}
	return start, end
}

// OpenTeamsScheduling opens Teams' scheduling form with the event form's data and closes the
// form: the meeting is made and sent in Teams, not saved in PostDive.
func (a *App) OpenTeamsScheduling() {
	if strings.TrimSpace(a.eventTitle) == "" {
		a.toast(a.tr("Bitte einen Titel eingeben.", "Please enter a title."), "error")
		return
	}
	if msg := a.eventTimeError(); msg != "" {
		a.toast(msg, "error")
		return
	}
	atts := parseAttendeeList(a.eventAttendees)
	self := ""
	if a.eventTeamsSelf {
		if self = a.teamsSelfAddress(); self != "" {
			atts = append(atts, backend.EventAttendee{Email: self})
		}
	}
	notes := strings.TrimSpace(a.eventDesc)
	if loc := strings.TrimSpace(a.eventLocation); loc != "" {
		// Teams takes no location: keep it in the details.
		notes = strings.TrimSpace(a.tr("Ort: ", "Location: ") + loc + "\n\n" + notes)
	}
	start, end := a.eventFormTimes()
	if err := openExternalURL(teamsMeetingURL(a.eventTitle, start, end, notes, atts)); err != nil {
		a.toast(a.tr("Teams konnte nicht geöffnet werden: ", "Could not open Teams: ")+err.Error(), "error")
		return
	}
	a.CloseEventDialog()
	if self != "" {
		a.toast(a.trf("Teams ist geöffnet – dort „Senden“ klicken. Die Einladung an %s erscheint dann in PostDive; mit „Zusagen“ kommt der Termin in den Kalender.",
			"Teams is open – click “Send” there. The invitation to %s then shows up in PostDive; “Accept” puts the meeting into the calendar.", self), "success")
		return
	}
	a.toast(a.tr("Teams ist geöffnet – dort „Senden“ klicken.", "Teams is open – click “Send” there."), "success")
}

// renderEventTeamsRow offers to plan a new event as a Teams meeting instead of saving it here.
func (a *App) renderEventTeamsRow(c *ui.Context) {
	if a.eventEditingID != "" {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("ev-teams")).FillWidth().Gap(6).Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Als Teams-Besprechung?", "As a Teams meeting?")).FontSize(11.5).Bold().TextColor(t.TextMuted).Grow(1)
			if iconLabelBtn(c.Key("ev-teams-open"), iconExternal, a.tr("In Teams planen", "Plan in Teams"), false,
				a.tr("Öffnet das Teams-Formular mit Titel, Zeit, Notizen und Teilnehmern; gesendet wird in Teams", "Opens Teams' form with title, time, notes and attendees; it is sent from Teams")).Clicked() {
				a.OpenTeamsScheduling()
			}
		})
		if self := a.teamsSelfAddress(); self != "" {
			ui.Checkbox(c.Key("ev-teams-self"), &a.eventTeamsSelf, a.trf("Mich selbst einladen (%s), damit der Termin in PostDive erscheint", "Invite myself (%s) so the meeting shows up in PostDive", self))
		}
	})
}
