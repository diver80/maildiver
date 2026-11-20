package nativeui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) reloadSMIMECerts() {
	if a.opts.Direct == nil {
		return
	}
	a.direct.smimeIdentities = a.opts.Direct.ListSMIMEIdentities()
	a.direct.smimeContacts = a.opts.Direct.ListSMIMEContacts()
}

func (a *App) renderDirectSMIMETab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct
	if len(d.smimeIdentities) == 0 && len(d.smimeContacts) == 0 && a.opts.Direct != nil {
		a.reloadSMIMECerts()
	}

	ui.Text(c, a.tr(
		"S/MIME-Zertifikate für digitales Signieren, Prüfen, Ver- und Entschlüsseln. Private Schlüssel werden sicher im System-Schlüsselbund gespeichert.",
		"S/MIME certificates for digital signing, verification, encryption & decryption. Private keys are stored securely in your OS keychain.",
	)).FontSize(12).TextColor(t.TextMuted)

	if d.smimeMsg != "" {
		clr := t.Success
		if d.smimeErr {
			clr = t.Danger
		}
		ui.Text(c, d.smimeMsg).FontSize(12).TextColor(clr)
	}

	// Section 1: Personal .p12/.pfx identity certificates
	ui.Column(c.Key("smime-id-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr("Eigene Zertifikate (.p12 / .pfx)", "Personal Certificates (.p12 / .pfx)")).FontSize(13).Bold()
		if len(d.smimeIdentities) == 0 {
			ui.Text(c, a.tr("Noch kein eigenes S/MIME-Zertifikat importiert.", "No personal S/MIME certificate imported yet.")).FontSize(12).TextColor(t.TextMuted)
		} else {
			for i, id := range d.smimeIdentities {
				idCopy := id
				ui.Row(c.Key(fmt.Sprintf("sm-id-%d", i))).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconLock).Size(14, 14).TextColor(t.Success)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, idCopy.Email+" · "+idCopy.Subject).FontSize(12.5).Bold().SingleLine()
						ui.Text(c, a.trf("Aussteller: %s · Gültig bis: %s", "Issuer: %s · Valid until: %s", idCopy.Issuer, truncateText(idCopy.NotAfter, 10))).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if iconBtn(c.Key(fmt.Sprintf("sm-id-del-%d", i)), iconTrash, a.tr("Zertifikat entfernen", "Remove certificate"), t.Danger, false).Clicked() {
						_ = a.opts.Direct.DeleteSMIMEIdentity(idCopy.Email)
						a.reloadSMIMECerts()
						d.smimeMsg = a.trf("Zertifikat für %s entfernt", "Removed certificate for %s", idCopy.Email)
						d.smimeErr = false
					}
				})
			}
		}

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("sm-p12-pick"), a.tr(".p12/.pfx wählen…", "Choose .p12/.pfx…")).Clicked() {
				a.pickFile(FilePickRequest{
					Mode:  "open",
					Title: a.tr("S/MIME-Zertifikat (.p12/.pfx) auswählen", "Select S/MIME Certificate (.p12/.pfx)"),
				}, func(paths []string) {
					if len(paths) > 0 {
						a.direct.smimeP12Path = paths[0]
					}
				})
			}
			ui.TextInput(c.Key("sm-p12-path"), &d.smimeP12Path).Placeholder("~/cert.p12").Grow(1)
			ui.TextInput(c.Key("sm-p12-pass"), &d.smimeP12Pass).Password().Placeholder(a.tr("Passwort", "Password")).Width(135)
			if ui.PrimaryButton(c.Key("sm-p12-imp"), a.tr("Importieren", "Import")).Clicked() && strings.TrimSpace(d.smimeP12Path) != "" {
				info, err := a.opts.Direct.ImportSMIMEP12File(strings.TrimSpace(d.smimeP12Path), d.smimeP12Pass)
				if err != nil {
					d.smimeMsg = err.Error()
					d.smimeErr = true
				} else {
					d.smimeP12Path = ""
					d.smimeP12Pass = ""
					d.smimeMsg = a.trf("S/MIME-Identität für %s importiert", "Imported S/MIME identity for %s", info.Email)
					d.smimeErr = false
					a.reloadSMIMECerts()
				}
			}
		})
	})

	// Section 2: Contact public certificates (.cer/.crt/.pem)
	ui.Column(c.Key("smime-ct-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr("Empfänger-Zertifikate (.cer / .crt / .pem)", "Recipient Certificates (.cer / .crt / .pem)")).FontSize(13).Bold()
		ui.Text(c, a.tr(
			"Zertifikate aus gültig signierten eingehenden Mails werden automatisch ergänzt.",
			"Certificates from verified signed incoming emails are collected automatically.",
		)).FontSize(11.5).TextColor(t.TextMuted)
		if len(d.smimeContacts) == 0 {
			ui.Text(c, a.tr("Noch keine Empfänger-Zertifikate gespeichert.", "No recipient certificates stored yet.")).FontSize(12).TextColor(t.TextMuted)
		} else {
			for i, ct := range d.smimeContacts {
				ctCopy := ct
				ui.Row(c.Key(fmt.Sprintf("sm-ct-%d", i))).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.Accent)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, ctCopy.Email+" · "+ctCopy.Subject).FontSize(12.5).Bold().SingleLine()
						ui.Text(c, a.trf("Aussteller: %s · Gültig bis: %s", "Issuer: %s · Valid until: %s", ctCopy.Issuer, truncateText(ctCopy.NotAfter, 10))).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if iconBtn(c.Key(fmt.Sprintf("sm-ct-del-%d", i)), iconTrash, a.tr("Zertifikat entfernen", "Remove certificate"), t.Danger, false).Clicked() {
						_ = a.opts.Direct.DeleteSMIMEContact(ctCopy.Email)
						a.reloadSMIMECerts()
					}
				})
			}
		}

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("sm-ct-pick"), a.tr("Datei wählen…", "Choose file…")).Clicked() {
				a.pickFile(FilePickRequest{
					Mode:  "open",
					Title: a.tr("Öffentliches Zertifikat (.cer/.crt/.pem)", "Public Certificate (.cer/.crt/.pem)"),
				}, func(paths []string) {
					if len(paths) > 0 {
						a.direct.smimeCertPath = paths[0]
					}
				})
			}
			ui.TextInput(c.Key("sm-ct-path"), &d.smimeCertPath).Placeholder("~/contact.pem").Grow(1)
			if ui.Button(c.Key("sm-ct-imp"), a.tr("Importieren", "Import")).Clicked() && strings.TrimSpace(d.smimeCertPath) != "" {
				info, err := a.opts.Direct.ImportSMIMECertFile(strings.TrimSpace(d.smimeCertPath))
				if err != nil {
					d.smimeMsg = err.Error()
					d.smimeErr = true
				} else {
					d.smimeCertPath = ""
					d.smimeMsg = a.trf("Empfänger-Zertifikat für %s gespeichert", "Saved recipient certificate for %s", info.Email)
					d.smimeErr = false
					a.reloadSMIMECerts()
				}
			}
		})
	})
}
