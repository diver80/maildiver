package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// sysNotifyStatus is how the system shows PostDive's notifications. Only macOS reports it;
// elsewhere it stays unknown and the settings say nothing.
type sysNotifyStatus int

const (
	sysNotifyUnknown  sysNotifyStatus = iota
	sysNotifyNotAsked                 // macOS asks with the first notification
	sysNotifyDenied                   // switched off in System Settings
	sysNotifyNoAlerts                 // allowed, but with the style "None": no banner
	sysNotifyBanners                  // banners, gone after a few seconds
	sysNotifyAlerts                   // alerts, stay until dismissed
)

// classifyMacNotifySettings maps UNNotificationSettings' authorizationStatus and alertStyle.
func classifyMacNotifySettings(authorization, alertStyle int) sysNotifyStatus {
	switch authorization {
	case 0: // notDetermined
		return sysNotifyNotAsked
	case 1: // denied
		return sysNotifyDenied
	}
	switch alertStyle { // authorized, provisional or ephemeral
	case 0:
		return sysNotifyNoAlerts
	case 1:
		return sysNotifyBanners
	case 2:
		return sysNotifyAlerts
	}
	return sysNotifyUnknown
}

// SetSystemNotifyProbe lets the settings tell how the system shows PostDive's notifications:
// probe asks the system (off the UI thread), openSettings opens its notification settings, or
// the Focus settings.
func (a *App) SetSystemNotifyProbe(probe func() sysNotifyStatus, openSettings func(focus bool)) {
	a.sysNotifyProbe = probe
	a.sysNotifyOpen = openSettings
}

// refreshSystemNotifyStatus asks the system again, at most every few seconds, so the settings
// catch up after a change in System Settings (UI thread).
func (a *App) refreshSystemNotifyStatus() {
	if a.sysNotifyProbe == nil || a.sysNotifyBusy || time.Since(a.sysNotifyAt) < 3*time.Second {
		return
	}
	a.sysNotifyBusy, a.sysNotifyAt = true, time.Now()
	probe := a.sysNotifyProbe
	go func() {
		st := probe()
		a.emitUpdate(func() {
			a.sysNotifyBusy = false
			a.sysNotify = st
		})
	}()
}

// renderSystemNotifyHint warns in the settings when macOS swallows PostDive's notifications: switched
// off, or allowed with the style "None", which shows no banner (event reminders then go unnoticed).
func (a *App) renderSystemNotifyHint(c *ui.Context) {
	if !a.notifyEnabled || a.sysNotifyProbe == nil {
		return
	}
	a.refreshSystemNotifyStatus()
	t := c.Theme()
	var text string
	color := t.TextMuted
	switch a.sysNotify {
	case sysNotifyDenied:
		text = a.tr("macOS blockiert Mitteilungen von PostDive: Termin-Erinnerungen und neue Mails erscheinen nicht.",
			"macOS blocks PostDive's notifications: event reminders and new mail do not show.")
		color = t.Danger
	case sysNotifyNoAlerts:
		text = a.tr("macOS zeigt Mitteilungen von PostDive ohne Banner an (Stil „Ohne“): Termin-Erinnerungen gehen so unter. Stil „Hinweise“ wählen.",
			"macOS shows PostDive's notifications without a banner (style “None”): event reminders go unnoticed. Choose the style “Alerts”.")
		color = t.Warning
	case sysNotifyBanners:
		text = a.tr("Stil „Banner“: Mitteilungen verschwinden nach wenigen Sekunden. „Hinweise“ bleiben stehen, bis sie weggeklickt werden.",
			"Style “Banners”: notifications disappear after a few seconds. “Alerts” stay until dismissed.")
	}
	ui.Column(c.Key("set-sysnotify")).FillWidth().Gap(6).Children(func() {
		if text != "" {
			ui.Text(c, text).FontSize(11.5).TextColor(color)
		}
		ui.Text(c, a.tr("Bei aktivem Fokus zeigt macOS nur Mitteilungen von Apps, die der Fokus erlaubt.",
			"While a Focus is on, macOS only shows notifications of the apps it allows.")).FontSize(11.5).TextColor(t.TextMuted)
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c.Key("set-sysnotify-open"), a.tr("Mitteilungseinstellungen öffnen", "Open notification settings")).Clicked() && a.sysNotifyOpen != nil {
				a.sysNotifyOpen(false)
			}
			if ui.Button(c.Key("set-sysnotify-focus"), a.tr("Fokus-Einstellungen öffnen", "Open Focus settings")).Clicked() && a.sysNotifyOpen != nil {
				a.sysNotifyOpen(true)
			}
		})
	})
}
