package nativeui

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/postdive/internal/theme"
)

type uiThemeState struct {
	theme *ui.Theme
}

func newUIThemeState(pal theme.Palette) *uiThemeState {
	return &uiThemeState{theme: PaletteToUITheme(pal)}
}

func (s *uiThemeState) setPalette(pal theme.Palette) {
	s.theme = PaletteToUITheme(pal)
}

type listStateHolder struct {
	state    ui.ListState
	selected int
}

type uiWidgetsState struct {
	chatScroll           ui.ScrollState
	inspectorScroll      ui.ScrollState
	calScroll            ui.ScrollState
	calDayScroll         ui.ScrollState
	flyoutScroll         ui.ScrollState
	helpScroll           ui.ScrollState
	quickRuleScroll      ui.ScrollState
	directScroll         ui.ScrollState
	contactsListScroll   ui.ScrollState
	contactsDetailScroll ui.ScrollState
	mdInspector          markdown.Options
	mdBody               markdown.Options
	mdChat               map[string]*markdown.Options
}

func (a *App) chatMarkdownOpts(id string, streaming bool) *markdown.Options {
	if a.widgets.mdChat == nil {
		a.widgets.mdChat = make(map[string]*markdown.Options)
	}
	opts, ok := a.widgets.mdChat[id]
	if !ok {
		opts = &markdown.Options{
			WrapCode:       true,
			ShareSelection: false,
		}
		a.widgets.mdChat[id] = opts
	}
	if streaming {
		if opts.Reveal == nil {
			opts.Reveal = &markdown.Reveal{Unit: markdown.ByWord}
		}
	} else {
		opts.Reveal = nil
	}
	return opts
}

func (a *App) ensureListState() *ui.ListState {
	ls := &a.mailList.state
	ls.Selected = &a.mailList.selected
	ls.Key = func(row int) any {
		if row >= 0 && row < len(a.messages) {
			return a.messages[row].ID
		}
		return row
	}
	// Scrolling (or j/k) near the end of the loaded mails loads the next page.
	ls.NearEnd = 15
	ls.EndReached = func(int) { a.loadMoreMessages(context.Background()) }
	return ls
}

func pillBadge(c *ui.Context, text string, bg, fg ui.Color) {
	ui.Row(c).Padding(2, 8).Radius(999).Background(bg).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).Bold().TextColor(fg).SingleLine()
	})
}

func statusDot(c *ui.Context, clr ui.Color, size float32) {
	ui.Box(c).Size(size, size).Radius(size / 2).Background(clr).Shrink(0)
}

// pillTabs renders a consistent pill tab bar with high-contrast active highlight across
// Settings, Accounts, Security, and Calendar view switchers.
func pillTabs(c *ui.Context, key string, selected *int, labels []string, fillWidth, wrap bool) bool {
	t := c.Theme()
	changed := false
	row := ui.Row(c.Key(key)).Padding(3).Gap(3).Radius(8).Background(t.Background).Border(1, t.Border).AlignItems(ui.Center)
	if fillWidth {
		row.FillWidth()
	} else {
		row.Shrink(0)
	}
	if wrap {
		row.Wrap()
	}
	row.Children(func() {
		for i, lbl := range labels {
			active := *selected == i
			b := ui.ButtonBase(c.Key(fmt.Sprintf("%s-%d", key, i))).Padding(4, 10).Radius(6).AlignItems(ui.Center).Shrink(0).Label(lbl)
			fg := t.TextMuted
			if active {
				b.Background(withAlpha(t.Accent, 0.18)).Border(1, withAlpha(t.Accent, 0.45))
				fg = t.Accent
			}
			b.Children(func() {
				txt := ui.Text(c, lbl).FontSize(12).TextColor(fg).SingleLine()
				if active {
					txt.Bold()
				}
			})
			if b.Clicked() && !active {
				*selected = i
				changed = true
			}
		}
	})
	return changed
}

func iconBtn(c *ui.Context, svg *ui.SVG, tooltip string, fg ui.Color, active bool) ui.Element {
	t := c.Theme()
	b := ui.Button(c, "").Padding(6, 7).Center().Label(tooltip)
	if tooltip != "" {
		b.Tooltip(tooltip)
	}
	if active {
		b.Background(withAlpha(t.Accent, 0.16)).Border(1, withAlpha(t.Accent, 0.45))
		if fg.A == 0 {
			fg = t.Accent
		}
	}
	b.Children(func() {
		ic := ui.Icon(c, svg).Size(15, 15)
		if fg.A > 0 {
			ic.TextColor(fg)
		}
	})
	return b
}

func iconLabelBtn(c *ui.Context, svg *ui.SVG, label string, primary bool, tooltip string) ui.Element {
	var b ui.Element
	if primary {
		b = ui.PrimaryButton(c, "")
	} else {
		b = ui.Button(c, "")
	}
	b.Padding(5, 10).Gap(6).AlignItems(ui.Center).Label(label)
	if tooltip != "" {
		b.Tooltip(tooltip)
	}
	b.Children(func() {
		if svg != nil {
			ui.Icon(c, svg).Size(14, 14)
		}
		ui.Text(c, label).FontSize(12.5).SingleLine()
	})
	return b
}

func formatRelTime(raw string) string {
	return formatRelTimeLang(raw, "de")
}

func formatDateTime(raw string) string {
	return formatDateTimeLang(raw, "de")
}

func formatEventRange(evAllDay bool, startRaw, endRaw string) string {
	return formatEventRangeLang(evAllDay, startRaw, endRaw, "de")
}

func dayHeading(dayKey string) string {
	return dayHeadingLang(dayKey, "de")
}

func eventDayKey(raw string) string {
	t, err := parseEventTime(raw)
	if err != nil {
		if len(raw) >= 10 {
			return raw[:10]
		}
		return raw
	}
	return t.Local().Format("2006-01-02")
}

func markdownOptions(streaming bool) *markdown.Options {
	opts := &markdown.Options{
		WrapCode: true,
	}
	if streaming {
		opts.Reveal = &markdown.Reveal{Unit: markdown.ByWord}
	}
	return opts
}

// formatEmailAddress formats a display name + email cleanly, quoting names that contain commas or RFC 5322 specials.
func formatEmailAddress(name, email string) string {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, email) || strings.Contains(name, "<") {
		return email
	}
	if strings.ContainsAny(name, ",;\"<>\\()") {
		return (&mail.Address{Name: name, Address: email}).String()
	}
	return name + " <" + email + ">"
}

func splitEmailList(raw string) []string {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(raw), ",;"))
	if trimmed == "" {
		return nil
	}
	if addrs, err := mail.ParseAddressList(trimmed); err == nil && len(addrs) > 0 {
		out := make([]string, 0, len(addrs))
		for _, a := range addrs {
			if a.Name == "" {
				out = append(out, a.Address)
			} else {
				out = append(out, a.String())
			}
		}
		return out
	}
	// Not RFC 5322 – typically a name with a comma and without quotes ("Müller, Hans <h@x.de>")
	// or ";" between addresses. A part without "@" belongs to the next one; each entry is then
	// written so the engine can parse it (quoted, encoded name).
	var out []string
	pending := ""
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if pending != "" {
			p, pending = pending+", "+p, ""
		}
		if !strings.Contains(p, "@") {
			pending = p
			continue
		}
		out = append(out, normalizeRecipient(p))
	}
	if pending != "" {
		out = append(out, pending) // sending names it as no address
	}
	return out
}

// normalizeRecipient writes "Name <a@b>" with an unquoted name in RFC 5322 form.
func normalizeRecipient(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		if a.Name == "" {
			return a.Address
		}
		return a.String()
	}
	if i := strings.LastIndex(s, "<"); i > 0 && strings.HasSuffix(s, ">") {
		name := strings.Trim(strings.TrimSpace(s[:i]), `"`)
		if addr, err := mail.ParseAddress(strings.TrimSpace(s[i+1 : len(s)-1])); err == nil {
			return (&mail.Address{Name: name, Address: addr.Address}).String()
		}
	}
	return s
}

func normalizeDateTimeInput(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return s
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return t.Format(time.RFC3339)
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t.Format(time.RFC3339)
	}
	return s
}

func truncateText(s string, maxLen int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxLen || maxLen <= 1 {
		return string(r)
	}
	return string(r[:maxLen-1]) + "…"
}

// miniTag is a small rounded label for list rows (folder, triage column).
func miniTag(c *ui.Context, text string, fg, bg ui.Color) {
	ui.Row(c).Padding(1, 6).Radius(999).Background(bg).AlignItems(ui.Center).Shrink(0).MaxWidth(120).Children(func() {
		ui.Text(c, text).FontSize(10).Bold().TextColor(fg).SingleLine()
	})
}
