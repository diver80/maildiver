package nativeui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/omathunder/internal/theme"
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
	chatScroll      ui.ScrollState
	inspectorScroll ui.ScrollState
	calScroll       ui.ScrollState
	flyoutScroll    ui.ScrollState
	helpScroll      ui.ScrollState
	quickRuleScroll ui.ScrollState
	mdInspector     markdown.Options
	mdBody          markdown.Options
	mdChat          map[string]*markdown.Options
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

func splitEmailList(raw string) []string {
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
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
