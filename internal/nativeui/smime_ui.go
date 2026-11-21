package nativeui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) reloadSMIMECerts() {
	if a.opts.Direct == nil {
		return
	}
	a.direct.smimeIdentities = a.opts.Direct.ListSMIMEIdentities()
	a.direct.smimeContacts = a.opts.Direct.ListSMIMEContacts()
}

func (a *App) reloadPGPKeys() {
	if a.opts.Direct == nil {
		return
	}
	a.direct.pgpIdentities = a.opts.Direct.ListPGPIdentities()
	a.direct.pgpContacts = a.opts.Direct.ListPGPContacts()
	if a.direct.pgpGenEmail == "" && len(a.cfg.Direct.Accounts) > 0 {
		a.direct.pgpGenEmail = a.cfg.Direct.Accounts[0].Email
		a.direct.pgpGenName = a.cfg.Direct.Accounts[0].DisplayName
	}
}

func (a *App) renderDirectSMIMETab(c *ui.Context) {
	d := &a.direct
	if len(d.smimeIdentities) == 0 && len(d.smimeContacts) == 0 && a.opts.Direct != nil {
		a.reloadSMIMECerts()
	}
	if len(d.pgpIdentities) == 0 && len(d.pgpContacts) == 0 && a.opts.Direct != nil {
		a.reloadPGPKeys()
	}

	ui.Segmented(c.Key("sec-subtab"), &d.secSubTab,
		a.tr("S/MIME (Zertifikate)", "S/MIME (Certificates)"),
		a.tr("OpenPGP (PGP/MIME)", "OpenPGP (PGP/MIME)"),
	)

	if d.secSubTab == 1 {
		a.renderDirectPGPTab(c)
		return
	}
	a.renderDirectSMIMESection(c)
}

func (a *App) renderDirectSMIMESection(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

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

		ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
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

		ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
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

func (a *App) renderDirectPGPTab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

	ui.Text(c, a.tr(
		"Natives OpenPGP (PGP/MIME & Inline PGP) ohne externes gpg. Private Schlüssel werden im System-Schlüsselbund gespeichert.",
		"Native OpenPGP (PGP/MIME & Inline PGP) with zero external gpg binary. Secret keys are stored securely in your OS keychain.",
	)).FontSize(12).TextColor(t.TextMuted)

	if d.pgpMsg != "" {
		clr := t.Success
		if d.pgpErr {
			clr = t.Danger
		}
		ui.Text(c, d.pgpMsg).FontSize(12).TextColor(clr)
	}

	// Section 1: Personal Secret Keys
	ui.Column(c.Key("pgp-id-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr("Eigene OpenPGP-Schlüssel (Signieren & Entschlüsseln)", "Personal OpenPGP Keys (Sign & Decrypt)")).FontSize(13).Bold()
		if len(d.pgpIdentities) == 0 {
			ui.Text(c, a.tr("Noch kein eigener OpenPGP-Privatschlüssel vorhanden.", "No personal OpenPGP secret key yet.")).FontSize(12).TextColor(t.TextMuted)
		} else {
			for i, id := range d.pgpIdentities {
				idCopy := id
				ui.Row(c.Key(fmt.Sprintf("pgp-id-%d", i))).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconLock).Size(14, 14).TextColor(t.Success)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, idCopy.Email+" · "+idCopy.UserID).FontSize(12.5).Bold().SingleLine()
						ui.Text(c, fmt.Sprintf("%s · KeyID %s · %s", idCopy.Algorithm, idCopy.KeyID, truncateText(idCopy.Fingerprint, 20))).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if ui.Button(c.Key(fmt.Sprintf("pgp-id-pub-%d", i)), a.tr("Public Key kopieren", "Copy Public Key")).Padding(3, 8).Clicked() {
						if arm, err := a.opts.Direct.ExportPGPPublicKey(idCopy.Email); err == nil {
							mygo.Clipboard.WriteText(arm)
							a.toast(a.tr("OpenPGP Public Key in Zwischenablage kopiert", "OpenPGP Public Key copied to clipboard"), "success")
						}
					}
					if iconBtn(c.Key(fmt.Sprintf("pgp-id-del-%d", i)), iconTrash, a.tr("Schlüssel entfernen", "Remove key"), t.Danger, false).Clicked() {
						_ = a.opts.Direct.DeletePGPIdentity(idCopy.Email)
						a.reloadPGPKeys()
						d.pgpMsg = a.trf("OpenPGP-Identität %s entfernt", "Removed OpenPGP identity %s", idCopy.Email)
						d.pgpErr = false
					}
				})
			}
		}

		ui.Divider(c)
		ui.Text(c, a.tr("Neuen OpenPGP-Schlüssel erzeugen (RSA-3072):", "Generate new OpenPGP keypair (RSA-3072):")).FontSize(11.5).Bold()
		ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("pgp-gen-name"), &d.pgpGenName).Placeholder(a.tr("Name", "Name")).Width(170)
			ui.TextInput(c.Key("pgp-gen-email"), &d.pgpGenEmail).Placeholder("mail@domain.de").Grow(1)
			btn := ui.PrimaryButton(c.Key("pgp-gen-go"), a.tr("Schlüssel erzeugen", "Generate Key"))
			if d.pgpBusy {
				btn.Disabled(true)
			}
			if btn.Clicked() && !d.pgpBusy {
				d.pgpBusy = true
				d.pgpMsg = a.tr("Erzeuge OpenPGP-Schlüsselpaar…", "Generating OpenPGP keypair…")
				d.pgpErr = false
				name, email := d.pgpGenName, d.pgpGenEmail
				go func() {
					info, err := a.opts.Direct.GeneratePGPKey(name, email, "")
					a.emitUpdate(func() {
						d.pgpBusy = false
						if err != nil {
							d.pgpMsg = err.Error()
							d.pgpErr = true
							return
						}
						d.pgpMsg = a.trf("Schlüssel für %s erzeugt (KeyID %s)", "Generated key for %s (KeyID %s)", info.Email, info.KeyID)
						d.pgpErr = false
						a.reloadPGPKeys()
					})
				}()
			}
		})
	})

	// Section 2: Import .asc/.gpg file or paste ASCII Armor
	ui.Column(c.Key("pgp-imp-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr("Schlüssel importieren (.asc / .gpg — Privat oder Öffentlich)", "Import Keys (.asc / .gpg — Secret or Public)")).FontSize(13).Bold()
		ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("pgp-imp-pick"), a.tr("Datei wählen…", "Choose file…")).Clicked() {
				a.pickFile(FilePickRequest{
					Mode:  "open",
					Title: a.tr("OpenPGP-Schlüssel (.asc / .gpg) auswählen", "Select OpenPGP Key (.asc / .gpg)"),
				}, func(paths []string) {
					if len(paths) > 0 {
						a.direct.pgpImpPath = paths[0]
					}
				})
			}
			ui.TextInput(c.Key("pgp-imp-path"), &d.pgpImpPath).Placeholder("~/key.asc").Grow(1)
			ui.TextInput(c.Key("pgp-imp-pass"), &d.pgpImpPass).Password().Placeholder(a.tr("Passwort (falls geschützt)", "Passphrase (optional)")).Width(155)
			if ui.PrimaryButton(c.Key("pgp-imp-file"), a.tr("Importieren", "Import")).Clicked() && strings.TrimSpace(d.pgpImpPath) != "" {
				infos, err := a.opts.Direct.ImportPGPKeyFile(strings.TrimSpace(d.pgpImpPath), d.pgpImpPass)
				if err != nil {
					d.pgpMsg = err.Error()
					d.pgpErr = true
				} else {
					d.pgpImpPath = ""
					d.pgpImpPass = ""
					d.pgpMsg = a.trf("%d OpenPGP-Schlüssel importiert", "Imported %d OpenPGP key(s)", len(infos))
					d.pgpErr = false
					a.reloadPGPKeys()
				}
			}
		})
		ui.TextArea(c.Key("pgp-paste-arm"), &d.pgpPasteArmor).Placeholder(a.tr("Oder -----BEGIN PGP PUBLIC/PRIVATE KEY BLOCK----- direkt hier einfügen…", "Or paste -----BEGIN PGP PUBLIC/PRIVATE KEY BLOCK----- here…")).FillWidth().Height(64)
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			if ui.Button(c.Key("pgp-imp-arm"), a.tr("Textblock importieren", "Import Armored Block")).Clicked() && strings.TrimSpace(d.pgpPasteArmor) != "" {
				infos, err := a.opts.Direct.ImportPGPArmor(d.pgpPasteArmor, d.pgpImpPass)
				if err != nil {
					d.pgpMsg = err.Error()
					d.pgpErr = true
				} else {
					d.pgpPasteArmor = ""
					d.pgpImpPass = ""
					d.pgpMsg = a.trf("%d OpenPGP-Schlüssel importiert", "Imported %d OpenPGP key(s)", len(infos))
					d.pgpErr = false
					a.reloadPGPKeys()
				}
			}
		})
	})

	// Section 3: Contact Public Keys + WKD/keys.openpgp.org lookup
	ui.Column(c.Key("pgp-ct-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr("Empfänger Public Keys (Verschlüsseln & Prüfen)", "Recipient Public Keys (Encrypt & Verify)")).FontSize(13).Bold()
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("pgp-fetch-em"), &d.pgpFetchEmail).Placeholder(a.tr("E-Mail über WKD / keys.openpgp.org suchen…", "Lookup email via WKD / keys.openpgp.org…")).Grow(1)
			btn := ui.Button(c.Key("pgp-fetch-go"), a.tr("Online suchen", "Fetch Key"))
			if d.pgpBusy {
				btn.Disabled(true)
			}
			if btn.Clicked() && !d.pgpBusy && strings.TrimSpace(d.pgpFetchEmail) != "" {
				em := strings.TrimSpace(d.pgpFetchEmail)
				d.pgpBusy = true
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
					defer cancel()
					infos, err := a.opts.Direct.FetchPGPKeyOnline(ctx, em)
					a.emitUpdate(func() {
						d.pgpBusy = false
						if err != nil {
							d.pgpMsg = err.Error()
							d.pgpErr = true
							return
						}
						d.pgpFetchEmail = ""
						d.pgpMsg = a.trf("%d Public Key(s) für %s geladen", "Fetched %d public key(s) for %s", len(infos), em)
						d.pgpErr = false
						a.reloadPGPKeys()
					})
				}()
			}
		})

		if len(d.pgpContacts) == 0 {
			ui.Text(c, a.tr("Noch keine Empfänger-Public-Keys gespeichert.", "No recipient public keys stored yet.")).FontSize(12).TextColor(t.TextMuted)
		} else {
			for i, ct := range d.pgpContacts {
				ctCopy := ct
				ui.Row(c.Key(fmt.Sprintf("pgp-ct-%d", i))).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.Accent)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, ctCopy.Email+" · "+ctCopy.UserID).FontSize(12.5).Bold().SingleLine()
						ui.Text(c, fmt.Sprintf("%s · KeyID %s · %s", ctCopy.Algorithm, ctCopy.KeyID, truncateText(ctCopy.Fingerprint, 20))).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if iconBtn(c.Key(fmt.Sprintf("pgp-ct-del-%d", i)), iconTrash, a.tr("Schlüssel entfernen", "Remove key"), t.Danger, false).Clicked() {
						_ = a.opts.Direct.DeletePGPContact(ctCopy.Email)
						a.reloadPGPKeys()
					}
				})
			}
		}
	})
}
