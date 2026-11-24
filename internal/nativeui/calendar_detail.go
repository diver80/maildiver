package nativeui

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/diver80/postdive/internal/backend"
)

var (
	calLineBreakRE = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</tr>`)
	calMultiNLRE   = regexp.MustCompile(`\n{3,}`)
)

// calEventKey uniquely identifies an event (or one recurring instance) in the UI.
func calEventKey(ev backend.CalendarEvent) string {
	return ev.ID + "|" + ev.RecurrenceID + "|" + ev.Start
}

func (a *App) toggleCalEventExpanded(key string) {
	if a.calExpanded == nil {
		a.calExpanded = make(map[string]bool)
	}
	if a.calExpanded[key] {
		delete(a.calExpanded, key)
	} else {
		a.calExpanded[key] = true
	}
}

// OpenEventDetails opens the read-only detail modal for ev (used by month/week calendar chips).
func (a *App) OpenEventDetails(ev backend.CalendarEvent) {
	a.evDetail = ev
	a.evDetailOpen = true
}

// cleanEventFullText formats an event description for readable, multi-line display
// without stripping plain-text angle-bracketed URLs (<https://...>) or truncating content.
func cleanEventFullText(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = calLineBreakRE.ReplaceAllString(s, "\n")
	s = calHTMLTagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	var out []string
	for _, ln := range lines {
		ln = strings.TrimSpace(calRuleRE.ReplaceAllString(ln, ""))
		out = append(out, ln)
	}
	joined := strings.TrimSpace(strings.Join(out, "\n"))
	return calMultiNLRE.ReplaceAllString(joined, "\n\n")
}

// ExtractEventLinks finds unique safe http(s) links in ev.Location and ev.Description
// (HTML anchors first, then plain-text / angle-bracketed URLs).
func ExtractEventLinks(ev backend.CalendarEvent) []ExtractedLink {
	seen := make(map[string]bool)
	var out []ExtractedLink
	add := func(rawURL, label string, stripTrailingPunct bool) {
		if len(out) >= 24 {
			return
		}
		rawURL = strings.TrimSpace(rawURL)
		rawURL = strings.Trim(rawURL, "<>")
		if stripTrailingPunct {
			rawURL = strings.TrimRight(rawURL, ".,;:!?)>]\"'")
		}
		if !isSafeWebURL(rawURL) || seen[rawURL] {
			return
		}
		seen[rawURL] = true
		label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
		if label == "" || strings.HasPrefix(strings.ToLower(label), "http://") || strings.HasPrefix(strings.ToLower(label), "https://") {
			if prov := meetingProviderLabel(rawURL); prov != "" {
				label = prov
			} else if u, err := url.Parse(rawURL); err == nil {
				label = u.Host + truncateText(u.Path, 30)
			} else {
				label = truncateText(rawURL, 42)
			}
		}
		out = append(out, ExtractedLink{URL: rawURL, Label: truncateText(label, 48)})
	}

	// 1. Direct URLs in Location first (often the join link itself).
	for _, m := range plainURLRe.FindAllString(ev.Location, 8) {
		add(m, "", true)
	}

	// 2. HTML <a href="..."> tags in Description (e.g. Teams / Outlook / Google HTML invites).
	if desc := strings.TrimSpace(ev.Description); desc != "" {
		if len(desc) > maxPreviewHTMLScanBytes {
			desc = desc[:maxPreviewHTMLScanBytes]
		}
		if strings.Contains(desc, "<") {
			if doc, err := xhtml.Parse(strings.NewReader(desc)); err == nil {
				var walk func(*xhtml.Node)
				walk = func(n *xhtml.Node) {
					if n.Type == xhtml.ElementNode && n.DataAtom == atom.A {
						if href, ok := attrOf(n, "href"); ok {
							add(href, textContent(n), false)
						}
					}
					for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
						walk(ch)
					}
				}
				walk(doc)
			}
		}
		// 3. Plain-text URLs (also catches Outlook's <https://teams.microsoft.com/...>).
		for _, m := range plainURLRe.FindAllString(desc, 24) {
			add(m, "", true)
		}
	}
	return out
}

func meetingProviderLabel(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.Path)
	switch {
	case host == "teams.microsoft.com" || host == "teams.live.com" || strings.HasSuffix(host, ".teams.microsoft.com"):
		if strings.Contains(path, "meetup-join") || strings.Contains(path, "/meet/") || strings.Contains(path, "/l/chat") {
			return "Microsoft Teams"
		}
	case host == "meet.google.com":
		return "Google Meet"
	case host == "zoom.us" || strings.HasSuffix(host, ".zoom.us") || host == "zoom.com" || strings.HasSuffix(host, ".zoom.com"):
		if strings.Contains(path, "/j/") || strings.Contains(path, "/w/") || strings.Contains(path, "/my/") || strings.Contains(path, "/s/") {
			return "Zoom"
		}
	case strings.HasSuffix(host, ".webex.com") || host == "webex.com":
		return "Webex"
	case host == "meet.jit.si" || strings.HasPrefix(host, "meet."):
		return "Online-Meeting"
	case strings.Contains(host, "gotomeet") || host == "app.goto.com":
		return "GoTo Meeting"
	case strings.Contains(host, "whereby.com"):
		return "Whereby"
	}
	return ""
}

// eventJoinLink returns the primary video-meeting join URL (or a direct URL in Location)
// along with a concise action button label.
func (a *App) eventJoinLink(ev backend.CalendarEvent) (string, string) {
	links := ExtractEventLinks(ev)
	for _, lk := range links {
		if prov := meetingProviderLabel(lk.URL); prov != "" {
			switch prov {
			case "Microsoft Teams":
				return lk.URL, a.tr("Teams beitreten ↗", "Join Teams ↗")
			case "Google Meet":
				return lk.URL, a.tr("Meet beitreten ↗", "Join Meet ↗")
			case "Zoom":
				return lk.URL, a.tr("Zoom beitreten ↗", "Join Zoom ↗")
			case "Webex":
				return lk.URL, a.tr("Webex beitreten ↗", "Join Webex ↗")
			default:
				return lk.URL, a.tr("Beitreten ↗", "Join Meeting ↗")
			}
		}
	}
	// If Location itself holds a safe http(s) URL, surface it as a direct button.
	for _, m := range plainURLRe.FindAllString(ev.Location, 2) {
		u := strings.TrimRight(strings.Trim(strings.TrimSpace(m), "<>"), ".,;:!?)>]\"'")
		if isSafeWebURL(u) {
			return u, a.tr("Link öffnen ↗", "Open Link ↗")
		}
	}
	return "", ""
}

func (a *App) eventHasDetails(ev backend.CalendarEvent) bool {
	if strings.TrimSpace(cleanEventFullText(ev.Description)) != "" {
		return true
	}
	if strings.TrimSpace(ev.Location) != "" || strings.TrimSpace(ev.Organizer) != "" || len(ev.Attendees) > 0 {
		return true
	}
	return len(ExtractEventLinks(ev)) > 0
}

// renderEventDetails renders full, selectable event details (links, metadata, complete description).
func (a *App) renderEventDetails(c *ui.Context, ev backend.CalendarEvent, keyPrefix string, showPrimaryJoin bool) {
	t := c.Theme()
	links := ExtractEventLinks(ev)
	joinURL, joinLabel := a.eventJoinLink(ev)

	if showPrimaryJoin && joinURL != "" {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if iconLabelBtn(c.Key(keyPrefix+"-join"), iconLink, joinLabel, true, joinURL).Clicked() {
				_ = openExternalURL(joinURL)
			}
			ui.Text(c, truncateText(joinURL, 60)).FontSize(11).TextColor(t.TextMuted).Selectable()
		})
	}

	if len(links) > 0 {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, a.tr("Links in diesem Termin:", "Links in this event:")).FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				for i, lk := range links {
					lkCopy := lk
					primary := lkCopy.URL == joinURL && !showPrimaryJoin
					if iconLabelBtn(c.Key(fmt.Sprintf("%s-lk-%d", keyPrefix, i)), iconExternal, lkCopy.Label, primary, lkCopy.URL).Clicked() {
						_ = openExternalURL(lkCopy.URL)
					}
				}
			})
		})
	}

	ui.Column(c.Key(keyPrefix + "-sel")).FillWidth().Gap(6).Selectable().Children(func() {
		if loc := strings.TrimSpace(ev.Location); loc != "" {
			ui.Text(c, a.tr("Ort: ", "Location: ")+loc).FontSize(12).TextColor(t.Text)
		}
		if org := strings.TrimSpace(ev.Organizer); org != "" {
			ui.Text(c, a.tr("Organisator: ", "Organizer: ")+org).FontSize(12).TextColor(t.TextMuted)
		}
		if len(ev.Attendees) > 0 {
			ui.Text(c, a.tr("Teilnehmer: ", "Attendees: ")+strings.Join(ev.Attendees, ", ")).FontSize(11.5).TextColor(t.TextMuted)
		}
		if full := cleanEventFullText(ev.Description); full != "" {
			ui.Column(c).FillWidth().Padding(10, 12).Radius(6).Background(t.Background).Border(1, t.Border).Children(func() {
				ui.Text(c, full).FontSize(12).LineHeight(1.45).TextColor(t.Text)
			})
		}
	})
}

func (a *App) renderEventDetailModal(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.evDetailOpen, func() {
		ev := a.evDetail
		evClr := a.calColorForEvent(ev, t.Accent)
		ui.Column(c).Width(a.modalW(580)).MaxHeight(a.modalH(c, 640)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(5, 32).Radius(3).Background(evClr).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Text(c, ev.Title).FontSize(15).Bold().Selectable()
						if ev.IsRecurring || ev.RecurrenceID != "" || ev.RecurrenceRule != "" {
							pillBadge(c, a.recurrenceBadgeText(ev), t.Background, t.TextMuted)
						}
					})
					sub := formatEventRangeLang(ev.AllDay, ev.Start, ev.End, a.uiLang())
					if dk := eventDayKey(ev.Start); dk != "" {
						sub = dayHeadingLang(dk, a.uiLang()) + " · " + sub
					}
					if ev.Calendar != "" {
						sub += " · " + ev.Calendar
					}
					ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted).Selectable()
				})
				if ui.Button(c.Key("evd-x"), "✕").Clicked() {
					a.evDetailOpen = false
				}
			})

			ui.Divider(c)

			ui.Scroll(c.Key("evd-scroll")).Grow(1).MinHeight(0).FillWidth().MaxHeight(440).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					if a.eventHasDetails(ev) {
						a.renderEventDetails(c, ev, "evd", true)
					} else {
						ui.Text(c, a.tr("Keine weiteren Details zu diesem Termin.", "No additional details for this event.")).FontSize(12).TextColor(t.TextMuted)
					}
				})
			})

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if (!a.readOnly || a.isDirectMode()) && !ev.ReadOnly {
					if iconLabelBtn(c.Key("evd-edit"), iconPen, a.tr("Bearbeiten", "Edit"), false, a.tr("Termin bearbeiten", "Edit event")).Clicked() {
						a.evDetailOpen = false
						a.OpenEditEventModal(ev)
					}
					if iconLabelBtn(c.Key("evd-del"), iconTrash, a.tr("Löschen", "Delete"), false, a.tr("Termin löschen…", "Delete event…")).Clicked() {
						a.evDetailOpen = false
						a.RequestDeleteCalendarEvent(ev)
					}
				}
				ui.Spacer(c)
				if ui.PrimaryButton(c.Key("evd-close"), a.tr("Schließen", "Close")).Clicked() {
					a.evDetailOpen = false
				}
			})
		})
	})
}
