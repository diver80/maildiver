package nativeui

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/mailengine"
)

// offlineGrace is how long the network must stay away before the app says it is offline. Waking a
// phone, Android first reports the network the sleeping app was cut off from, and the real one
// right after: without the grace, "Offline" flashes up on every wake.
const offlineGrace = 3 * time.Second

// noteNetworkOffline takes a network report (UI thread): online clears the offline state at once,
// offline shows only when no newer report comes within offlineGrace.
func (a *App) noteNetworkOffline(offline bool) {
	a.netOfflineGen++
	if !offline {
		a.networkOffline = false
		return
	}
	a.clearTransientSyncErrors()
	gen := a.netOfflineGen
	time.AfterFunc(offlineGrace, func() {
		a.emitUpdate(func() {
			if a.netOfflineGen == gen {
				a.networkOffline = true
				a.clearTransientSyncErrors()
			}
		})
	})
}

// refreshBackgroundRun reads the last background wake-up again, at most every few seconds (UI
// thread).
func (a *App) refreshBackgroundRun() {
	rec, ok := a.opts.Direct.(backgroundRunRecorder)
	if !ok || a.bgRunBusy || time.Since(a.bgRunAt) < 5*time.Second {
		return
	}
	a.bgRunBusy, a.bgRunAt = true, time.Now()
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		run, found, err := rec.LastBackgroundRun(c)
		a.emitUpdate(func() {
			a.bgRunBusy = false
			if err == nil {
				a.bgRun, a.bgRunFound = run, found
			}
		})
	}()
}

// backgroundRunText says when Android last woke the app in the background and how it went, with a
// hint when it does not seem to.
func (a *App) backgroundRunText(now time.Time) (text string, level int) {
	const (
		levelOK = iota
		levelWarn
		levelError
	)
	if !a.bgRunFound {
		return a.tr("Noch kein Abruf im Hintergrund. Weckt Android PostDive nicht, in den Android-Einstellungen unter Apps → PostDive → Akku „Nicht eingeschränkt“ wählen.",
			"No background check yet. If Android does not wake PostDive, choose “Unrestricted” under Apps → PostDive → Battery in the Android settings."), levelWarn
	}
	at := a.bgRun.At.Local()
	when := at.Format("15:04")
	if at.YearDay() != now.YearDay() || at.Year() != now.Year() {
		when = at.Format("02.01. 15:04")
	}
	ago := now.Sub(at)
	var since string
	switch {
	case ago < time.Minute:
		since = a.tr("gerade eben", "just now")
	case ago < time.Hour:
		since = a.trf("vor %d Min.", "%d min ago", int(ago.Minutes()))
	default:
		since = a.trf("vor %d Std.", "%d h ago", int(ago.Hours()))
	}
	text = a.trf("Zuletzt %s (%s)", "Last %s (%s)", when, since)
	if a.bgRun.Error != "" {
		return text + " · " + a.tr("Fehler: ", "Error: ") + localizeError(errors.New(a.bgRun.Error), a.uiLang()), levelError
	}
	if ago > 2*time.Hour {
		return text + ". " + a.tr("Android weckt PostDive selten: unter Apps → PostDive → Akku „Nicht eingeschränkt“ wählen.",
			"Android rarely wakes PostDive: choose “Unrestricted” under Apps → PostDive → Battery."), levelWarn
	}
	return text + " · " + a.tr("ok", "ok"), levelOK
}

// renderBackgroundRunStatus shows the last background check in the settings (phones).
func (a *App) renderBackgroundRunStatus(c *ui.Context, field func(key, label string, body func())) {
	if !a.bgRunsShown {
		return
	}
	if _, ok := a.opts.Direct.(backgroundRunRecorder); !ok {
		return
	}
	a.refreshBackgroundRun()
	t := c.Theme()
	text, level := a.backgroundRunText(time.Now())
	color := t.TextMuted
	switch level {
	case 1:
		color = t.Warning
	case 2:
		color = t.Danger
	}
	field("set-bgrun", a.tr("Abruf im Hintergrund", "Background check"), func() {
		ui.Text(c, text).FontSize(12).TextColor(color).Grow(1).MinWidth(0)
	})
}

var _ backgroundRunRecorder = (*mailengine.Engine)(nil)
