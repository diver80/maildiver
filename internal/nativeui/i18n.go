package nativeui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type langChoiceItem struct {
	Code  string // "auto", "de", "en"
	Label string
}

var uiLangChoices = []langChoiceItem{
	{Code: "auto", Label: "Auto"},
	{Code: "de", Label: "Deutsch"},
	{Code: "en", Label: "English"},
}

var (
	sysLangOnce   sync.Once
	cachedSysLang string
)

func detectSystemLang() string {
	sysLangOnce.Do(func() {
		for _, env := range []string{"POSTDIVE_LANG", "MAILDIVER_LANG", "OMATHUNDER_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
			v := strings.ToLower(strings.TrimSpace(os.Getenv(env)))
			if v == "" || v == "c" || strings.HasPrefix(v, "c.") || v == "posix" {
				continue
			}
			if strings.HasPrefix(v, "de") {
				cachedSysLang = "de"
				return
			}
			if strings.HasPrefix(v, "en") {
				cachedSysLang = "en"
				return
			}
		}
		if runtime.GOOS == "darwin" {
			if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
				s := strings.ToLower(string(out))
				for _, line := range strings.Split(s, "\n") {
					trimmed := strings.Trim(strings.TrimSpace(line), `(", `)
					if trimmed == "" || trimmed == ")" {
						continue
					}
					if strings.HasPrefix(trimmed, "de") {
						cachedSysLang = "de"
						return
					}
					if strings.HasPrefix(trimmed, "en") {
						cachedSysLang = "en"
						return
					}
					break
				}
			}
		}
		cachedSysLang = "en"
	})
	return cachedSysLang
}

func normalizeLangPref(pref string) string {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case "de":
		return "de"
	case "en":
		return "en"
	default:
		return "auto"
	}
}

func resolveUILang(pref string) string {
	switch normalizeLangPref(pref) {
	case "de":
		return "de"
	case "en":
		return "en"
	default:
		return detectSystemLang()
	}
}

func langChoiceLabels() []string {
	out := make([]string, len(uiLangChoices))
	for i, ch := range uiLangChoices {
		out[i] = ch.Label
	}
	return out
}

func langLabelForPref(pref string) string {
	norm := normalizeLangPref(pref)
	for _, ch := range uiLangChoices {
		if ch.Code == norm {
			return ch.Label
		}
	}
	return uiLangChoices[0].Label
}

func langPrefForLabel(label string) string {
	for _, ch := range uiLangChoices {
		if ch.Label == label {
			return ch.Code
		}
	}
	return "auto"
}

// formatShortcutsForOS adapts macOS ⌘ shortcut labels to Ctrl+ on Linux/Windows.
func formatShortcutsForOS(s, goos string) string {
	if goos == "darwin" || !strings.Contains(s, "⌘") {
		return s
	}
	r := strings.NewReplacer(
		"⌘⇧", "Ctrl+Shift+",
		"⌘↩", "Ctrl+Enter",
		"⌘/shift", "Ctrl/Shift",
		"⌘-", "Ctrl-",
		"⌘", "Ctrl+",
	)
	return r.Replace(s)
}

func formatShortcuts(s string) string {
	return formatShortcutsForOS(s, runtime.GOOS)
}

func trLang(lang, de, en string) string {
	if lang == "en" {
		return formatShortcuts(en)
	}
	return formatShortcuts(de)
}

func (a *App) uiLang() string {
	if a == nil {
		return "de"
	}
	if p := a.langAtomic.Load(); p != nil && (*p == "de" || *p == "en") {
		return *p
	}
	if a.lang == "de" || a.lang == "en" {
		return a.lang
	}
	return "de"
}

func (a *App) storeLang(lang string) {
	if lang != "de" && lang != "en" {
		lang = "de"
	}
	a.lang = lang
	v := lang
	a.langAtomic.Store(&v)
}

func (a *App) tr(de, en string) string {
	return trLang(a.uiLang(), de, en)
}

func (a *App) trf(deFmt, enFmt string, args ...any) string {
	return fmt.Sprintf(a.tr(deFmt, enFmt), args...)
}

func allAccountsLabelLang(lang string) string {
	return trLang(lang, "Alle Konten", "All Accounts")
}

func formatRelTimeLang(raw, lang string) string {
	t, err := parseEventTime(raw)
	if err != nil {
		return raw
	}
	lt := t.Local()
	now := time.Now()
	ny, nm, nd := now.Date()
	ly, lm, ld := lt.Date()
	if ny == ly && nm == lm && nd == ld {
		return lt.Format("15:04")
	}
	yest := now.AddDate(0, 0, -1)
	yy, ym, yd := yest.Date()
	if yy == ly && ym == lm && yd == ld {
		return trLang(lang, "Gestern", "Yesterday")
	}
	if lt.Year() == now.Year() {
		if lang == "en" {
			return lt.Format("Jan 2")
		}
		return lt.Format("02.01.")
	}
	if lang == "en" {
		return lt.Format("2006-01-02")
	}
	return lt.Format("02.01.06")
}

func formatDateTimeLang(raw, lang string) string {
	t, err := parseEventTime(raw)
	if err != nil {
		return raw
	}
	if lang == "en" {
		return t.Local().Format("2006-01-02 15:04")
	}
	return t.Local().Format("02.01.2006 15:04")
}

func formatEventRangeLang(evAllDay bool, startRaw, endRaw, lang string) string {
	if evAllDay {
		return trLang(lang, "Ganztägig", "All day")
	}
	s, err := parseEventTime(startRaw)
	if err != nil {
		return startRaw
	}
	sl := s.Local().Format("15:04")
	if endRaw == "" {
		return sl
	}
	e, err := parseEventTime(endRaw)
	if err != nil {
		return sl
	}
	return sl + " – " + e.Local().Format("15:04")
}

func dayHeadingLang(dayKey, lang string) string {
	t, err := time.Parse("2006-01-02", dayKey)
	if err != nil {
		return dayKey
	}
	now := time.Now()
	todayKey := now.Format("2006-01-02")
	tomKey := now.AddDate(0, 0, 1).Format("2006-01-02")
	dateFmt := "02.01.2006"
	if lang == "en" {
		dateFmt = "Jan 2, 2006"
	}
	if dayKey == todayKey {
		return trLang(lang, "Heute · ", "Today · ") + t.Format(dateFmt)
	}
	if dayKey == tomKey {
		return trLang(lang, "Morgen · ", "Tomorrow · ") + t.Format(dateFmt)
	}
	weekdays := [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}
	if lang == "en" {
		weekdays = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	}
	return fmt.Sprintf("%s, %s", weekdays[t.Weekday()], t.Format(dateFmt))
}

func contactFieldLabelLang(key, lang string) string {
	switch key {
	case "firstName":
		return trLang(lang, "Vorname", "First name")
	case "lastName":
		return trLang(lang, "Nachname", "Last name")
	case "phone":
		return trLang(lang, "Telefon", "Phone")
	case "mobile":
		return trLang(lang, "Mobil", "Mobile")
	case "company":
		return trLang(lang, "Firma", "Company")
	case "jobTitle":
		return trLang(lang, "Position", "Job title")
	case "website":
		return "Website"
	case "address":
		return trLang(lang, "Adresse", "Address")
	default:
		return key
	}
}
