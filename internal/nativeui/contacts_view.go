package nativeui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/mailengine"
)

type contactsUI struct {
	loaded     bool
	loading    bool
	searchSeq  int
	query      string
	lastQuery  string
	list       []backend.ContactRecord
	split      float32
	selectedID string

	// Editor form fields
	editingNew  bool
	editBookID  string
	editDisplay string
	editFirst   string
	editLast    string
	editEmail   string
	editEmail2  string
	editPhone   string
	editMobile  string
	editCompany string
	editTitle   string
	editWebsite string
	editAddress string
	editNotes   string
	busy        bool

	// vCard (.vcf) bar & Google Contacts sync
	vcfOpen   bool
	vcfPath   string
	syncBusy  bool
	statusMsg string
	statusErr bool
	delArmID  string
	delArmAt  time.Time

	// AI completion from mail signatures: the fields it filled are marked until saved.
	aiBusy   bool
	aiFilled map[string]bool

	// Merging contacts with the same name.
	dupOpen    bool
	dupLoading bool
	dupGroups  [][]backend.ContactRecord
	dupIdx     int
	dupMerge   *contactMerge
}

// LoadContacts fetches contacts matching the current search query (UI thread).
func (a *App) LoadContacts(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	q := strings.TrimSpace(a.contacts.query)
	a.contacts.lastQuery = q
	a.contacts.loading = true
	a.contacts.searchSeq++
	seq := a.contacts.searchSeq
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		recs, err := dc.ListContacts(c, q)
		a.emitUpdate(func() {
			if seq != a.contacts.searchSeq {
				return
			}
			a.contacts.loading = false
			a.contacts.loaded = true
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				return
			}
			a.contacts.list = recs
			// Keep currently edited contact synced unless creating a new one.
			if !a.contacts.editingNew && a.contacts.selectedID != "" {
				found := false
				for _, r := range recs {
					if r.ID == a.contacts.selectedID {
						found = true
						break
					}
				}
				if !found {
					a.contacts.selectedID = ""
				}
			}
		})
	}()
}

func (a *App) selectContact(rec backend.ContactRecord) {
	ct := &a.contacts
	ct.editingNew = false
	ct.dupOpen = false
	ct.aiFilled = nil
	ct.selectedID = rec.ID
	ct.editBookID = rec.AddressBookID
	ct.editDisplay = rec.DisplayName
	ct.editFirst = rec.FirstName
	ct.editLast = rec.LastName
	ct.editEmail = rec.Email
	ct.editEmail2 = rec.SecondEmail
	ct.editPhone = rec.Phone
	ct.editMobile = rec.Mobile
	ct.editCompany = rec.Company
	ct.editTitle = rec.JobTitle
	ct.editWebsite = rec.Website
	ct.editAddress = rec.Address
	ct.editNotes = rec.Notes
	ct.statusMsg = ""
	ct.statusErr = false
}

func (a *App) startNewContact() {
	ct := &a.contacts
	ct.editingNew = true
	ct.dupOpen = false
	ct.aiFilled = nil
	ct.selectedID = ""
	ct.editBookID = mailengine.LocalAddressBookID
	ct.editDisplay = ""
	ct.editFirst = ""
	ct.editLast = ""
	ct.editEmail = ""
	ct.editEmail2 = ""
	ct.editPhone = ""
	ct.editMobile = ""
	ct.editCompany = ""
	ct.editTitle = ""
	ct.editWebsite = ""
	ct.editAddress = ""
	ct.editNotes = ""
	ct.statusMsg = ""
	ct.statusErr = false
}

func (a *App) saveContactForm(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.busy {
		return
	}
	rec := backend.ContactRecord{
		ID:            ct.selectedID,
		AddressBookID: ct.editBookID,
		DisplayName:   strings.TrimSpace(ct.editDisplay),
		FirstName:     strings.TrimSpace(ct.editFirst),
		LastName:      strings.TrimSpace(ct.editLast),
		Email:         strings.TrimSpace(ct.editEmail),
		SecondEmail:   strings.TrimSpace(ct.editEmail2),
		Phone:         strings.TrimSpace(ct.editPhone),
		Mobile:        strings.TrimSpace(ct.editMobile),
		Company:       strings.TrimSpace(ct.editCompany),
		JobTitle:      strings.TrimSpace(ct.editTitle),
		Website:       strings.TrimSpace(ct.editWebsite),
		Address:       strings.TrimSpace(ct.editAddress),
		Notes:         strings.TrimSpace(ct.editNotes),
	}
	if rec.DisplayName == "" && rec.FirstName == "" && rec.LastName == "" && rec.Email == "" {
		ct.statusMsg = a.tr("Bitte mindestens Name oder E-Mail-Adresse eingeben.", "Please enter at least a name or email address.")
		ct.statusErr = true
		return
	}
	ct.busy = true
	ct.statusMsg = ""
	ct.statusErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		saved, err := dc.SaveContact(c, rec)
		a.emitUpdate(func() {
			a.contacts.busy = false
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				return
			}
			a.selectContact(saved)
			a.toast(a.trf("Kontakt „%s“ gespeichert", "Saved contact “%s”", saved.DisplayName), "success")
			a.LoadContacts(context.Background())
		})
	}()
}

func (a *App) deleteSelectedContact(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.busy || ct.selectedID == "" {
		return
	}
	id := ct.selectedID
	ct.busy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := dc.DeleteContact(c, id)
		a.emitUpdate(func() {
			a.contacts.busy = false
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				return
			}
			a.contacts.selectedID = ""
			a.contacts.editingNew = false
			a.toast(a.tr("Kontakt gelöscht", "Contact deleted"), "info")
			a.LoadContacts(context.Background())
		})
	}()
}

func (a *App) importContactsVCF(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.syncBusy {
		return
	}
	p := strings.TrimSpace(ct.vcfPath)
	if p == "" {
		ct.statusMsg = a.tr("Bitte Pfad zu einer .vcf-Datei angeben (z. B. ~/Downloads/kontakte.vcf).", "Please enter path to a .vcf file (e.g. ~/Downloads/contacts.vcf).")
		ct.statusErr = true
		return
	}
	ct.syncBusy = true
	ct.statusMsg = ""
	ct.statusErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		n, err := dc.ImportVCFFile(c, p)
		a.emitUpdate(func() {
			a.contacts.syncBusy = false
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				return
			}
			a.contacts.vcfPath = ""
			a.contacts.statusMsg = a.trf("%d Kontakt(e) aus vCard importiert.", "Imported %d contact(s) from vCard.", n)
			a.contacts.statusErr = false
			a.toast(a.contacts.statusMsg, "success")
			a.LoadContacts(context.Background())
		})
	}()
}

func (a *App) exportContactsVCF(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.syncBusy {
		return
	}
	p := strings.TrimSpace(ct.vcfPath)
	if p == "" {
		p = "~/Downloads/postdive-kontakte.vcf"
	}
	ct.syncBusy = true
	ct.statusMsg = ""
	ct.statusErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		n, err := dc.ExportVCFFile(c, p)
		a.emitUpdate(func() {
			a.contacts.syncBusy = false
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				return
			}
			a.contacts.statusMsg = a.trf("%d Kontakt(e) nach %s exportiert.", "Exported %d contact(s) to %s.", n, p)
			a.contacts.statusErr = false
			a.toast(a.contacts.statusMsg, "success")
		})
	}()
}

func (a *App) syncGoogleContactsNow(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.syncBusy {
		return
	}
	ct.syncBusy = true
	ct.statusMsg = a.tr("Synchronisiere Google Kontakte…", "Syncing Google Contacts…")
	ct.statusErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		n, err := dc.SyncGoogleContacts(c)
		a.emitUpdate(func() {
			a.contacts.syncBusy = false
			if err != nil {
				a.contacts.statusMsg = err.Error()
				a.contacts.statusErr = true
				a.toast(a.tr("Google-Kontakte: ", "Google Contacts: ")+err.Error(), "error")
				return
			}
			a.contacts.statusMsg = a.trf("%d Google-Kontakt(e) synchronisiert.", "Synced %d Google contact(s).", n)
			a.contacts.statusErr = false
			a.toast(a.contacts.statusMsg, "success")
			a.LoadContacts(context.Background())
		})
	}()
}

func (a *App) renderContactsWorkspace(c *ui.Context) {
	t := c.Theme()
	ct := &a.contacts
	if !ct.loaded && !ct.loading && a.isDirectMode() {
		a.LoadContacts(context.Background())
	}
	if ct.split <= 0 {
		ct.split = 320
	}

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		// Top header bar
		ui.Row(c).FillWidth().Padding(10, 14).Gap(8).Wrap().AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Icon(c, iconUsers).Size(17, 17).TextColor(t.Accent)
			ui.Text(c, a.tr("Kontakte", "Contacts")).FontSize(15).Bold().Grow(1)
			ui.Text(c, a.trf("%d Einträge", "%d entries", len(ct.list))).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
			if iconLabelBtn(c.Key("ct-new"), iconUserPlus, a.tr("Neuer Kontakt", "New Contact"), true, a.tr("Neuen Kontakt anlegen", "Create new contact")).Clicked() {
				a.startNewContact()
			}
			if iconLabelBtn(c.Key("ct-dups"), iconUsers, a.tr("Duplikate", "Duplicates"), false, a.tr("Kontakte mit gleichem Namen vergleichen und zusammenführen", "Compare and merge contacts with the same name")).Clicked() {
				a.openContactDuplicates(context.Background())
			}
			if ui.Button(c.Key("ct-vcf-tog"), a.tr(".vcf Import/Export", ".vcf Import/Export")).Clicked() {
				ct.vcfOpen = !ct.vcfOpen
			}
			gSyncLbl := a.tr("Google Sync", "Google Sync")
			if ct.syncBusy {
				gSyncLbl = a.tr("Sync…", "Syncing…")
			}
			gBtn := iconLabelBtn(c.Key("ct-gsync"), iconRefresh, gSyncLbl, false, a.tr("Google Kontakte über Google-Konto synchronisieren", "Sync Google Contacts using your Google connection"))
			if ct.syncBusy {
				gBtn.Disabled(true)
			}
			if gBtn.Clicked() && !ct.syncBusy {
				a.syncGoogleContactsNow(context.Background())
			}
		})

		// Optional collapsible vCard (.vcf) bar
		if ct.vcfOpen {
			ui.Row(c.Key("ct-vcf-bar")).FillWidth().Padding(8, 14).Gap(8).Wrap().AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, "vCard (.vcf):").FontSize(12).TextColor(t.TextMuted).Shrink(0)
				if ui.Button(c.Key("ct-vcf-browse"), a.tr("Öffnen…", "Browse…")).Clicked() && !ct.syncBusy {
					a.pickFile(FilePickRequest{
						Mode:       "open",
						Title:      a.tr("vCard-Datei (.vcf) importieren", "Import vCard (.vcf) File"),
						FilterName: "vCard (.vcf)",
						Extensions: []string{"vcf"},
					}, func(paths []string) {
						if len(paths) > 0 {
							ct.vcfPath = paths[0]
							a.importContactsVCF(context.Background())
						}
					})
				}
				ui.TextInput(c.Key("ct-vcf-path"), &ct.vcfPath).Placeholder("~/Downloads/kontakte.vcf").Grow(1)
				impBtn := ui.Button(c.Key("ct-vcf-imp"), a.tr("Importieren", "Import"))
				expBtn := ui.Button(c.Key("ct-vcf-exp"), a.tr("Exportieren…", "Export…"))
				if ct.syncBusy {
					impBtn.Disabled(true)
					expBtn.Disabled(true)
				}
				if impBtn.Clicked() && !ct.syncBusy {
					a.importContactsVCF(context.Background())
				}
				if expBtn.Clicked() && !ct.syncBusy {
					if strings.TrimSpace(ct.vcfPath) != "" || a.onPickFile == nil {
						a.exportContactsVCF(context.Background())
					} else {
						a.pickFile(FilePickRequest{
							Mode:        "save",
							Title:       a.tr("Kontakte als vCard (.vcf) exportieren", "Export Contacts as vCard (.vcf)"),
							DefaultPath: "postdive-kontakte.vcf",
							FilterName:  "vCard (.vcf)",
							Extensions:  []string{"vcf"},
						}, func(paths []string) {
							if len(paths) > 0 {
								ct.vcfPath = paths[0]
								a.exportContactsVCF(context.Background())
							}
						})
					}
				}
			})
		}

		if ct.statusMsg != "" {
			clr := t.Success
			if ct.statusErr {
				clr = t.Danger
			}
			ui.Row(c).FillWidth().Padding(6, 14).Background(withAlpha(clr, 0.12)).BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(clr, 0.35)).Children(func() {
				ui.Text(c, ct.statusMsg).FontSize(12).TextColor(clr).Grow(1)
				if ui.Button(c.Key("ct-st-x"), "✕").Padding(2, 6).Clicked() {
					ct.statusMsg = ""
				}
			})
		}

		renderList := func() {
			// Left column: search & contact list
			ui.Column(c).Fill().Background(t.Background).Children(func() {
				ui.Row(c).FillWidth().Padding(8, 10).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					sf := ui.SearchField(c.Key("ct-search"), &ct.query).Placeholder(a.tr("Kontakte suchen…", "Search contacts…")).Grow(1)
					if sf.Submitted() || strings.TrimSpace(ct.query) != ct.lastQuery {
						a.LoadContacts(context.Background())
					}
				})

				if len(ct.list) == 0 {
					ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
						ui.Text(c, a.tr("Keine Kontakte gefunden", "No contacts found")).FontSize(13).Bold()
						ui.Text(c, a.tr("Lege einen Kontakt an, importiere eine .vcf-Datei oder synchronisiere Google Kontakte.", "Create a contact, import a .vcf file, or sync Google Contacts.")).FontSize(12).TextColor(t.TextMuted)
					})
					return
				}

				ui.Scroll(c.Key("ct-list-scroll")).TrackScroll(&a.widgets.contactsListScroll).Fill().Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						for _, rec := range ct.list {
							r := rec
							sel := !ct.editingNew && r.ID == ct.selectedID
							bg := t.Background
							if sel {
								bg = withAlpha(t.Accent, 0.14)
							}
							row := ui.Column(c.Key("ct-row-"+r.ID)).FillWidth().Padding(9, 12).Gap(2).Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
								ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
									title := r.DisplayName
									if title == "" {
										title = r.Email
									}
									ui.Text(c, title).FontSize(13).Bold().SingleLine().Grow(1)
									if r.AddressBookID == mailengine.GoogleAddressBookID {
										miniTag(c, "Google", t.Accent, withAlpha(t.Accent, 0.14))
									}
								})
								sub := r.Email
								if r.Company != "" {
									if sub != "" {
										sub += " · " + r.Company
									} else {
										sub = r.Company
									}
								}
								if sub != "" {
									ui.Text(c, sub).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
								}
							})
							if row.Clicked() {
								a.selectContact(r)
							}
						}
					})
				})
			})
		}

		if a.isPhone() {
			if ct.editingNew || ct.selectedID != "" || ct.dupOpen {
				ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
					ui.Row(c).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
						if ui.Button(c.Key("ct-ph-back"), "← "+a.tr("Alle Kontakte", "All Contacts")).Padding(4, 10).Clicked() {
							ct.selectedID = ""
							ct.editingNew = false
							ct.dupOpen = false
						}
					})
					ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
						a.renderContactDetail(c)
					})
				})
			} else {
				ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(renderList)
			}
			return
		}

		ui.Split(c, &ct.split, renderList, func() {
			// Right column: detail & editor
			a.renderContactDetail(c)
		}).Grow(1).MinHeight(0).FillWidth()
	})
}

func (a *App) renderContactDetail(c *ui.Context) {
	t := c.Theme()
	ct := &a.contacts
	if ct.dupOpen {
		a.renderContactDuplicates(c)
		return
	}
	if !ct.editingNew && ct.selectedID == "" {
		ui.Column(c).Fill().Center().Padding(28).Gap(10).Children(func() {
			ui.Icon(c, iconUsers).Size(28, 28).TextColor(t.TextMuted)
			ui.Text(c, a.tr("Kontakt auswählen oder neu anlegen", "Select or create a contact")).FontSize(14).Bold()
			ui.Text(c, a.tr(
				"Tipp: Kontakte aus E-Mail-Signaturen lassen sich direkt beim Lesen einer Mail über „KI → Kontakte“ übernehmen. Alle gespeicherten Kontakte stehen automatisch als Autocomplete im Verfassen-Fenster bereit.",
				"Tip: Contacts from email footers can be imported right from any email via “AI → Contacts”. All saved contacts automatically appear as autocomplete suggestions when composing.",
			)).FontSize(12).TextColor(t.TextMuted)
			if iconLabelBtn(c.Key("ct-empty-new"), iconUserPlus, a.tr("Neuer Kontakt", "New Contact"), true, "").Clicked() {
				a.startNewContact()
			}
		})
		return
	}

	ui.Scroll(c.Key("ct-detail-scroll")).TrackScroll(&a.widgets.contactsDetailScroll).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(18).Gap(10).Children(func() {
			heading := a.tr("Kontakt bearbeiten", "Edit Contact")
			if ct.editingNew {
				heading = a.tr("Neuer Kontakt", "New Contact")
			}
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, heading).FontSize(16).Bold().Grow(1)
				if strings.TrimSpace(ct.editEmail) != "" {
					if iconLabelBtn(c.Key("ct-mail"), iconMail, a.tr("E-Mail schreiben", "Write Email"), false, a.tr("Neue E-Mail an diesen Kontakt verfassen", "Compose a new email to this contact")).Clicked() {
						target := formatEmailAddress(ct.editDisplay, ct.editEmail)
						a.OpenComposeTo(target)
					}
				}
				if !ct.editingNew && ct.selectedID != "" && strings.TrimSpace(ct.editEmail+ct.editEmail2) != "" {
					lbl := a.tr("Mit KI vervollständigen", "Complete with AI")
					if ct.aiBusy {
						lbl = a.tr("KI liest…", "AI reading…")
					}
					aiBtn := iconLabelBtn(c.Key("ct-ai"), iconSparkles, lbl, false, a.tr("Leere Felder aus den Signaturen der letzten Mails dieser Person ergänzen (verschlüsselte Mails werden nicht gelesen). Danach prüfen und speichern.", "Fill empty fields from the signatures of this person's latest mails (encrypted mails are not read). Then check and save."))
					if ct.aiBusy {
						aiBtn.Disabled(true)
					}
					if aiBtn.Clicked() && !ct.aiBusy {
						a.enrichContactWithAI(context.Background())
					}
				}
				if !ct.editingNew && ct.selectedID != "" {
					armed := ct.delArmID == ct.selectedID && time.Since(ct.delArmAt) < 5*time.Second
					if armed {
						btn := ui.Button(c.Key("ct-del-confirm"), a.tr("Wirklich löschen?", "Confirm delete?")).Padding(4, 9).
							Background(withAlpha(t.Danger, 0.18)).Border(1, t.Danger)
						if btn.Clicked() {
							ct.delArmID = ""
							a.deleteSelectedContact(context.Background())
						}
					} else if iconBtn(c.Key("ct-del"), iconTrash, a.tr("Kontakt löschen", "Delete contact"), t.Danger, false).Clicked() {
						ct.delArmID = ct.selectedID
						ct.delArmAt = time.Now()
					}
				}
			})

			// Fields the AI just filled carry a ✦ until the contact is saved or another one opened.
			mark := func(label string, keys ...string) string {
				for _, k := range keys {
					if ct.aiFilled[k] {
						return label + " ✦"
					}
				}
				return label
			}
			rowField := func(label string, inputs func()) {
				if a.isPhone() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
						ui.Column(c).FillWidth().Gap(6).Children(inputs)
					})
					return
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(12).Width(120).TextColor(t.TextMuted)
					inputs()
				})
			}
			field := func(key, label string, val *string, ph string) {
				rowField(label, func() {
					in := ui.TextInput(c.Key(key), val).Placeholder(ph)
					if a.isPhone() {
						in.FillWidth()
					} else {
						in.Grow(1)
					}
				})
			}

			field("ct-f-disp", a.tr("Anzeigename", "Display Name"), &ct.editDisplay, a.tr("z. B. Max Mustermann", "e.g. Jane Doe"))
			rowField(mark(a.tr("Vor- / Nachname", "First / Last"), "firstName", "lastName"), func() {
				if a.isPhone() {
					ui.TextInput(c.Key("ct-f-first"), &ct.editFirst).Placeholder(a.tr("Vorname", "First name")).FillWidth()
					ui.TextInput(c.Key("ct-f-last"), &ct.editLast).Placeholder(a.tr("Nachname", "Last name")).FillWidth()
				} else {
					ui.TextInput(c.Key("ct-f-first"), &ct.editFirst).Placeholder(a.tr("Vorname", "First name")).Grow(1)
					ui.TextInput(c.Key("ct-f-last"), &ct.editLast).Placeholder(a.tr("Nachname", "Last name")).Grow(1)
				}
			})
			field("ct-f-em", a.tr("E-Mail", "Email"), &ct.editEmail, "name@domain.de")
			field("ct-f-em2", a.tr("2. E-Mail", "2nd Email"), &ct.editEmail2, a.tr("optional", "optional"))
			rowField(mark(a.tr("Telefon / Mobil", "Phone / Mobile"), "phone", "mobile"), func() {
				if a.isPhone() {
					ui.TextInput(c.Key("ct-f-ph"), &ct.editPhone).Placeholder(a.tr("Telefon", "Phone")).FillWidth()
					ui.TextInput(c.Key("ct-f-mob"), &ct.editMobile).Placeholder(a.tr("Mobil", "Mobile")).FillWidth()
				} else {
					ui.TextInput(c.Key("ct-f-ph"), &ct.editPhone).Placeholder(a.tr("Telefon", "Phone")).Grow(1)
					ui.TextInput(c.Key("ct-f-mob"), &ct.editMobile).Placeholder(a.tr("Mobil", "Mobile")).Grow(1)
				}
			})
			rowField(mark(a.tr("Firma / Position", "Company / Role"), "company", "jobTitle"), func() {
				if a.isPhone() {
					ui.TextInput(c.Key("ct-f-comp"), &ct.editCompany).Placeholder(a.tr("Firma", "Company")).FillWidth()
					ui.TextInput(c.Key("ct-f-title"), &ct.editTitle).Placeholder(a.tr("Position", "Job title")).FillWidth()
				} else {
					ui.TextInput(c.Key("ct-f-comp"), &ct.editCompany).Placeholder(a.tr("Firma", "Company")).Grow(1)
					ui.TextInput(c.Key("ct-f-title"), &ct.editTitle).Placeholder(a.tr("Position", "Job title")).Grow(1)
				}
			})
			field("ct-f-web", mark("Website", "website"), &ct.editWebsite, "https://…")
			field("ct-f-adr", mark(a.tr("Adresse", "Address"), "address"), &ct.editAddress, a.tr("Straße, PLZ Ort", "Street, City"))
			if a.isPhone() {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					ui.Text(c, a.tr("Notizen", "Notes")).FontSize(12).TextColor(t.TextMuted)
					ui.TextArea(c.Key("ct-f-notes"), &ct.editNotes).Placeholder(a.tr("Notizen zum Kontakt…", "Notes about this contact…")).FillWidth().Height(84)
				})
			} else {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, a.tr("Notizen", "Notes")).FontSize(12).Width(120).TextColor(t.TextMuted).Padding(6, 0, 0, 0)
					ui.TextArea(c.Key("ct-f-notes"), &ct.editNotes).Placeholder(a.tr("Notizen zum Kontakt…", "Notes about this contact…")).Grow(1).Height(84)
				})
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				saveLbl := a.tr("Kontakt speichern", "Save Contact")
				if ct.busy {
					saveLbl = a.tr("Speichere…", "Saving…")
				}
				saveBtn := ui.PrimaryButton(c.Key("ct-save"), saveLbl)
				if ct.busy {
					saveBtn.Disabled(true)
				}
				if saveBtn.Clicked() && !ct.busy {
					a.saveContactForm(context.Background())
				}
			})
		})
	})
}
