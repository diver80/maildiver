package nativeui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/lansync"
	"github.com/diver80/maildiver/internal/secretstore"
)

func insertComposeSnippet(body, snippet string) string {
	// Insert before signature delimiter if present so formatting stays in the message body.
	if idx := strings.Index(body, "\n\n-- \n"); idx >= 0 {
		head := strings.TrimRight(body[:idx], " ")
		if head != "" && !strings.HasSuffix(head, "\n") && !strings.HasPrefix(snippet, "\n") {
			head += " "
		}
		return head + snippet + body[idx:]
	}
	if body != "" && !strings.HasSuffix(body, "\n") && !strings.HasPrefix(snippet, "\n") {
		return body + " " + snippet
	}
	return body + snippet
}

func (a *App) stopLANQRSession() {
	if a.settings.lanSession != nil {
		a.settings.lanSession.Close()
		a.settings.lanSession = nil
		a.settings.lanQRImg = nil
	}
}

func (a *App) startLANQRSession() {
	a.stopLANQRSession()
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	bundle := lansync.BuildBundle(cfg, secretstore.Default())
	sess, err := lansync.StartSession(context.Background(), bundle, "", 3*time.Minute)
	if err != nil {
		a.settings.lanMsg = err.Error()
		a.settings.lanErr = true
		return
	}
	a.settings.lanSession = sess
	a.settings.lanQRImg, _ = ui.DecodeImage(sess.QRPNG)
	a.settings.lanMsg = a.tr("Einmaliger LAN-Transfer aktiv (3 Min., AES-256-GCM verschlüsselt). Scanne den QR-Code auf dem Zielgerät im selben WLAN.",
		"One-shot LAN transfer active (3 min, AES-256-GCM encrypted). Scan QR code on target device in the same Wi-Fi.")
	a.settings.lanErr = false
}

func (a *App) importLANPairingURI(ctx context.Context) {
	uri := strings.TrimSpace(a.settings.lanImportURI)
	if uri == "" {
		return
	}
	curCfg, err := a.opts.ConfigLoader()
	if err != nil {
		curCfg = a.cfg
	}
	merged, added, err := lansync.ImportFromPairingURI(ctx, uri, curCfg, secretstore.Default())
	if err != nil {
		a.settings.lanMsg = err.Error()
		a.settings.lanErr = true
		return
	}
	if a.opts.ConfigSaver != nil {
		if serr := a.opts.ConfigSaver(merged); serr != nil {
			a.settings.lanMsg = serr.Error()
			a.settings.lanErr = true
			return
		}
	}
	a.applyConfig(merged)
	a.settings.lanImportURI = ""
	a.settings.lanMsg = a.trf("Import erfolgreich: %d Konto/Konten & Einstellungen übernommen.", "Import succeeded: %d account(s) & settings transferred.", added)
	a.settings.lanErr = false
	a.RefreshAll(ctx)
}

func (a *App) renderSettingsLANQRSection(c *ui.Context) {
	t := c.Theme()
	s := &a.settings

	a.settingsSection(c, a.tr("Geräte-Übertragung im WLAN (QR-Code)", "Device Transfer over LAN (QR Code)"))
	ui.Text(c, a.tr(
		"Überträgt Konten, Passwörter und Einstellungen Ende-zu-Ende AES-256-GCM-verschlüsselt an ein anderes Gerät oder Handy (Android) im selben Netzwerk. Einmalig gültig, max. 3 Minuten.",
		"Transfers accounts, credentials and settings end-to-end AES-256-GCM encrypted to another device or Android phone on the same Wi-Fi. One-shot, expires in 3 minutes.",
	)).FontSize(11.5).TextColor(t.TextMuted)

	ui.Column(c.Key("set-lan-qr")).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if s.lanSession == nil || s.lanSession.Completed() {
				if ui.PrimaryButton(c.Key("lan-qr-start"), a.tr("QR-Code zum Übertragen erzeugen", "Generate Transfer QR Code")).Clicked() {
					a.startLANQRSession()
				}
			} else {
				rem := int(time.Until(s.lanSession.ExpiresAt).Seconds())
				if rem < 0 {
					rem = 0
				}
				ui.Text(c, fmt.Sprintf("%s (%ds)", a.tr("Transfer aktiv", "Transfer active"), rem)).FontSize(12).Bold().TextColor(t.Success).Grow(1)
				if ui.Button(c.Key("lan-qr-stop"), a.tr("Beenden", "Stop")).Clicked() {
					a.stopLANQRSession()
				}
			}
		})

		if s.lanSession != nil && !s.lanSession.Completed() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				if s.lanQRImg != nil {
					ui.Image(c, s.lanQRImg).Size(160, 160).Radius(8)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, a.tr("Pairing-Code (alternativ kopierbar):", "Pairing code (copyable fallback):")).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, s.lanSession.URI).FontSize(10.5).TextColor(t.Text).Selectable()
				})
			})
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("lan-imp-uri"), &s.lanImportURI).Placeholder("maildiver-pair://v1?…").Grow(1)
			if ui.Button(c.Key("lan-imp-btn"), a.tr("Empfangen", "Import")).Clicked() && strings.TrimSpace(s.lanImportURI) != "" {
				a.importLANPairingURI(context.Background())
			}
		})

		if s.lanMsg != "" {
			clr := t.Success
			if s.lanErr {
				clr = t.Danger
			}
			ui.Text(c, s.lanMsg).FontSize(11.5).TextColor(clr)
		}
	})
}

// Keep config referenced for build clarity.
var _ config.Config
