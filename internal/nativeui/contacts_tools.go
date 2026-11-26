package nativeui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
)

// Two tools for the address book, both of which only propose: the AI fills empty fields of the
// open contact from the signatures of that person's latest mails (the user saves), and contacts
// with the same name can be merged field by field after looking at them side by side.

// contactMailsForAI is how many of a contact's latest mails the AI reads.
const contactMailsForAI = 3

// --- Complete a contact from mail signatures ---

func (a *App) enrichContactWithAI(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.editingNew || ct.selectedID == "" || ct.aiBusyID == ct.selectedID {
		return
	}
	emails := nonEmpty(ct.editEmail, ct.editEmail2)
	if len(emails) == 0 {
		ct.statusMsg, ct.statusErr = a.tr("Der Kontakt hat keine E-Mail-Adresse, deren Mails gelesen werden könnten.", "The contact has no email address whose mails could be read."), true
		return
	}
	id, name := ct.selectedID, strings.TrimSpace(ct.editDisplay)
	lang := a.uiLang()
	aiBackend, model := a.agentBackend, a.agentModel
	bc, _ := a.client()
	ct.aiSeq++
	seq := ct.aiSeq
	ct.aiBusyID = id
	ct.statusMsg, ct.statusErr = a.tr("KI liest die Signaturen der letzten Mails…", "AI is reading the signatures of the latest mails…"), false
	go func() {
		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
		defer cancel()
		mails, err := dc.MailsFromSender(c, emails, contactMailsForAI)
		var res ai.AnalyzeResponse
		if err == nil && len(mails) > 0 {
			who := strings.TrimSpace(name + " <" + emails[0] + ">")
			question := a.tr(
				"Alle diese E-Mails stammen von "+who+". Erkenne aus Signatur bzw. Footer die Kontaktdaten genau dieser Person (Telefon, Mobil, Firma, Position, Website, Adresse) und liefere sie in contacts. Andere Personen aus zitierten Mails nicht aufnehmen.",
				"All these emails are from "+who+". Extract this person's contact details (phone, mobile, company, job title, website, address) from the signature / footer into contacts. Leave out other people from quoted mails.",
			)
			res, err = a.analyzeWithAI(c, bc, aiBackend, model, ai.AnalyzeRequest{
				Mode:           "extract_contacts",
				Language:       lang,
				Question:       question,
				PrimaryMessage: mails[0],
				ThreadMessages: mails,
			})
		}
		a.emitUpdate(func() {
			ct := &a.contacts
			if seq != ct.aiSeq {
				return
			}
			ct.aiBusyID = ""
			if ct.selectedID != id || ct.editingNew {
				return // another contact was opened meanwhile
			}
			switch {
			case err != nil:
				ct.statusMsg, ct.statusErr = a.tr("KI-Fehler: ", "AI error: ")+a.errText(err), true
			case len(mails) == 0:
				ct.statusMsg, ct.statusErr = a.tr("Keine lesbaren Mails von dieser Adresse gefunden (verschlüsselte Mails werden nicht gelesen).", "No readable mails from this address found (encrypted mails are not read)."), false
			default:
				filled := a.fillContactForm(matchExtractedContact(res.Contacts, emails, name))
				if len(filled) == 0 {
					ct.statusMsg, ct.statusErr = a.trf("Keine neuen Angaben in den Signaturen der letzten %d Mail(s).", "No new details in the signatures of the last %d mail(s).", len(mails)), false
					return
				}
				labels := make([]string, len(filled))
				for i, k := range filled {
					labels[i] = contactFieldLabelLang(k, lang)
				}
				ct.statusMsg, ct.statusErr = a.trf("KI hat %s ergänzt (aus %d Mail(s)) – bitte prüfen und speichern.", "AI filled in %s (from %d mail(s)) – please check and save.", strings.Join(labels, ", "), len(mails)), false
			}
		})
	}()
}

// matchExtractedContact picks the extracted contact that is the person asked about: by email,
// else by name. Others (people quoted in the mails) are not used.
func matchExtractedContact(list []ai.ExtractedContact, emails []string, name string) *ai.ExtractedContact {
	for i := range list {
		for _, em := range emails {
			if strings.EqualFold(strings.TrimSpace(list[i].Email), em) {
				return &list[i]
			}
		}
	}
	if key := contactNameKey(name); key != "" {
		for i := range list {
			n := list[i].DisplayName
			if strings.TrimSpace(n) == "" {
				n = list[i].FirstName + " " + list[i].LastName
			}
			if contactNameKey(n) == key {
				return &list[i]
			}
		}
	}
	return nil
}

// sanitizeContactField strips control characters, caps length, and validates website URLs
// before AI-extracted values are written into the contact editor.
func sanitizeContactField(key, val string) string {
	allowNewline := key == "address"
	val = strings.Map(func(r rune) rune {
		if r == '\n' && allowNewline {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, val)
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	maxLen := 120
	switch key {
	case "website":
		maxLen = 256
	case "address":
		maxLen = 300
	}
	if rs := []rune(val); len(rs) > maxLen {
		val = strings.TrimSpace(string(rs[:maxLen]))
	}
	if key == "website" {
		if strings.ContainsAny(val, " \t\r\n") {
			return ""
		}
		raw := val
		if !strings.Contains(raw, "://") {
			if strings.Contains(raw, ":") {
				return "" // reject javascript:, data:, mailto:, etc.
			}
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") {
			return ""
		}
	}
	return val
}

// fillContactForm writes ex into the empty fields of the open contact form and returns the keys
// of the fields it filled. Nothing is saved.
func (a *App) fillContactForm(ex *ai.ExtractedContact) []string {
	if ex == nil {
		return nil
	}
	ct := &a.contacts
	var filled []string
	set := func(key string, dst *string, val string) {
		if val = sanitizeContactField(key, val); val != "" && strings.TrimSpace(*dst) == "" {
			*dst = val
			filled = append(filled, key)
		}
	}
	set("firstName", &ct.editFirst, ex.FirstName)
	set("lastName", &ct.editLast, ex.LastName)
	set("phone", &ct.editPhone, ex.Phone)
	set("mobile", &ct.editMobile, ex.Mobile)
	set("company", &ct.editCompany, ex.Company)
	set("jobTitle", &ct.editTitle, ex.JobTitle)
	set("website", &ct.editWebsite, ex.Website)
	set("address", &ct.editAddress, ex.Address)
	ct.aiFilled = map[string]bool{}
	for _, k := range filled {
		ct.aiFilled[k] = true
	}
	return filled
}

// analyzeWithAI runs one AI request through the engine, or in process when there is none.
func (a *App) analyzeWithAI(ctx context.Context, bc *backend.Client, aiBackend, model string, req ai.AnalyzeRequest) (ai.AnalyzeResponse, error) {
	req.Backend, req.Model = aiBackend, model
	if bc != nil {
		return bc.AIAnalyze(ctx, req)
	}
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = config.Default()
	}
	prov := ai.ProviderForBackend(cfg.AI, aiBackend)
	if model != "" {
		prov.Model = model
	}
	cfg.AI.Agent = prov
	return ai.Analyze(ctx, cfg.AI, req)
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// --- Duplicates ---

var stripMarks = runes.Remove(runes.In(unicode.Mn))

// contactNameKey is the name two entries of the same person share: "Carlé, Lukas",
// "Lukas Carle" and "Lukas Carlé (extern)" all give "lukas carle". A single word is no key, as
// first names alone are shared by too many people.
func contactNameKey(name string) string {
	name = strings.TrimSpace(name)
	for {
		open := strings.IndexAny(name, "([")
		if open < 0 {
			break
		}
		end := strings.IndexAny(name[open:], ")]")
		if end < 0 {
			name = name[:open]
			break
		}
		name = name[:open] + " " + name[open+end+1:]
	}
	if strings.Contains(name, "@") {
		return ""
	}
	if parts := strings.FieldsFunc(name, func(r rune) bool { return r == ',' || r == ';' }); len(parts) == 2 {
		name = parts[1] + " " + parts[0]
	}
	if s, _, err := transform.String(transform.Chain(norm.NFD, stripMarks, norm.NFC), name); err == nil {
		name = s
	}
	words := strings.Fields(strings.ToLower(strings.NewReplacer(".", " ", "-", " ", "\"", " ", "'", " ").Replace(name)))
	if len(words) < 2 {
		return ""
	}
	return strings.Join(words, " ")
}

func contactName(r backend.ContactRecord) string {
	if n := strings.TrimSpace(r.DisplayName); n != "" {
		return n
	}
	return strings.TrimSpace(r.FirstName + " " + r.LastName)
}

// duplicateContactGroups returns the entries that share a name within one writable address book,
// in list order; read-only synced books (such as Google Contacts) and cross-book pairs are
// skipped because merging them locally would be reverted on the next sync.
func duplicateContactGroups(list []backend.ContactRecord) [][]backend.ContactRecord {
	idx := map[string]int{}
	var groups [][]backend.ContactRecord
	for _, r := range list {
		if r.AddressBookID == mailengine.GoogleAddressBookID {
			continue
		}
		k := contactNameKey(contactName(r))
		if k == "" {
			continue
		}
		k = r.AddressBookID + "\x00" + k
		if i, ok := idx[k]; ok {
			groups[i] = append(groups[i], r)
			continue
		}
		idx[k] = len(groups)
		groups = append(groups, []backend.ContactRecord{r})
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g) > 1 {
			out = append(out, g)
		}
	}
	return out
}

// contactMergeFields are the fields chosen one value each when merging; emails and notes are
// combined instead.
var contactMergeFields = []string{"displayName", "firstName", "lastName", "phone", "mobile", "company", "jobTitle", "website", "address"}

func contactFieldValue(r backend.ContactRecord, key string) string {
	switch key {
	case "displayName":
		return r.DisplayName
	case "firstName":
		return r.FirstName
	case "lastName":
		return r.LastName
	case "phone":
		return r.Phone
	case "mobile":
		return r.Mobile
	case "company":
		return r.Company
	case "jobTitle":
		return r.JobTitle
	case "website":
		return r.Website
	case "address":
		return r.Address
	}
	return ""
}

func setContactField(r *backend.ContactRecord, key, val string) {
	switch key {
	case "displayName":
		r.DisplayName = val
	case "firstName":
		r.FirstName = val
	case "lastName":
		r.LastName = val
	case "phone":
		r.Phone = val
	case "mobile":
		r.Mobile = val
	case "company":
		r.Company = val
	case "jobTitle":
		r.JobTitle = val
	case "website":
		r.Website = val
	case "address":
		r.Address = val
	}
}

// contactMerge is the user's choice for one group: which entry's value each field takes, and
// which addresses become the first and second email.
type contactMerge struct {
	group   []backend.ContactRecord
	pick    map[string]int
	emails  []string
	primary int
	second  int // -1: none
}

func newContactMerge(group []backend.ContactRecord) *contactMerge {
	m := &contactMerge{group: group, pick: map[string]int{}, second: -1}
	base, best := 0, -1
	for i, r := range group {
		n := 0
		for _, k := range contactMergeFields {
			if strings.TrimSpace(contactFieldValue(r, k)) != "" {
				n++
			}
		}
		n += len(nonEmpty(r.Email, r.SecondEmail, r.Notes))
		if n > best {
			base, best = i, n
		}
	}
	for _, k := range contactMergeFields {
		m.pick[k] = base
		if strings.TrimSpace(contactFieldValue(group[base], k)) == "" {
			for i, r := range group {
				if strings.TrimSpace(contactFieldValue(r, k)) != "" {
					m.pick[k] = i
					break
				}
			}
		}
	}
	seen := map[string]bool{}
	add := func(em string) {
		if em = strings.TrimSpace(em); em != "" && !seen[strings.ToLower(em)] {
			seen[strings.ToLower(em)] = true
			m.emails = append(m.emails, em)
		}
	}
	add(group[base].Email)
	add(group[base].SecondEmail)
	for _, r := range group {
		add(r.Email)
		add(r.SecondEmail)
	}
	if len(m.emails) > 1 {
		m.second = 1
	}
	m.pick["_base"] = base
	return m
}

// result is the merged contact (it keeps the ID of the most complete entry) and the IDs of the
// entries to delete. Addresses beyond the first two and all notes are kept in the notes.
func (m *contactMerge) result() (backend.ContactRecord, []string) {
	base := m.pick["_base"]
	out := m.group[base]
	for _, k := range contactMergeFields {
		setContactField(&out, k, strings.TrimSpace(contactFieldValue(m.group[m.pick[k]], k)))
	}
	out.Email, out.SecondEmail = "", ""
	var extra []string
	for i, em := range m.emails {
		switch {
		case i == m.primary:
			out.Email = em
		case i == m.second:
			out.SecondEmail = em
		default:
			extra = append(extra, em)
		}
	}
	var notes []string
	seen := map[string]bool{}
	for _, r := range m.group {
		if n := strings.TrimSpace(r.Notes); n != "" && !seen[n] {
			seen[n] = true
			notes = append(notes, n)
		}
	}
	if len(extra) > 0 {
		notes = append(notes, "Weitere E-Mail: "+strings.Join(extra, ", "))
	}
	out.Notes = strings.Join(notes, "\n")
	var del []string
	for i, r := range m.group {
		if i != base {
			del = append(del, r.ID)
		}
	}
	return out, del
}

func (a *App) openContactDuplicates(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil {
		return
	}
	ct.dupOpen, ct.dupLoading = true, true
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		all, err := dc.AllContacts(c)
		a.emitUpdate(func() {
			ct := &a.contacts
			ct.dupLoading = false
			if err != nil {
				ct.statusMsg, ct.statusErr = err.Error(), true
				return
			}
			ct.dupGroups = duplicateContactGroups(all)
			ct.dupIdx = min(ct.dupIdx, max(len(ct.dupGroups)-1, 0))
			ct.dupMerge = nil
			if len(ct.dupGroups) > 0 {
				ct.dupMerge = newContactMerge(ct.dupGroups[ct.dupIdx])
			}
		})
	}()
}

func (a *App) showDuplicateGroup(i int) {
	ct := &a.contacts
	if i < 0 || i >= len(ct.dupGroups) {
		return
	}
	ct.dupIdx = i
	ct.dupMerge = newContactMerge(ct.dupGroups[i])
}

func (a *App) applyContactMerge(ctx context.Context) {
	dc := a.opts.Direct
	ct := &a.contacts
	if dc == nil || ct.dupMerge == nil || ct.busy || a.blockedReadOnly() {
		return
	}
	merged, del := ct.dupMerge.result()
	ct.busy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		saved, err := dc.SaveContact(c, merged)
		savedOK := err == nil
		// Delete the others only once the merged entry is stored.
		for _, id := range del {
			if err != nil {
				break
			}
			err = dc.DeleteContact(c, id)
		}
		a.emitUpdate(func() {
			ct := &a.contacts
			ct.busy = false
			if savedOK && !ct.editingNew && ct.selectedID == saved.ID {
				dupOpen := ct.dupOpen
				a.selectContact(saved)
				ct.dupOpen = dupOpen
			}
			if err != nil {
				ct.statusMsg, ct.statusErr = a.tr("Zusammenführen fehlgeschlagen: ", "Merge failed: ")+err.Error(), true
				if savedOK {
					a.LoadContacts(context.Background())
					a.openContactDuplicates(context.Background())
				}
				return
			}
			a.toast(a.trf("„%s“ zusammengeführt (%d Einträge → 1)", "Merged “%s” (%d entries → 1)", saved.DisplayName, len(del)+1), "success")
			if ct.selectedID != "" && ct.selectedID != saved.ID {
				for _, id := range del {
					if id == ct.selectedID {
						ct.selectedID = ""
					}
				}
			}
			a.LoadContacts(context.Background())
			a.openContactDuplicates(context.Background())
		})
	}()
}

func (a *App) renderContactDuplicates(c *ui.Context) {
	t := c.Theme()
	ct := &a.contacts
	ui.Scroll(c.Key("ct-dup-scroll")).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(18).Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Duplikate zusammenführen", "Merge duplicates")).FontSize(16).Bold().Grow(1)
				if ui.Button(c.Key("ct-dup-close"), a.tr("Schließen", "Close")).Clicked() {
					ct.dupOpen = false
				}
			})
			if ct.dupLoading {
				ui.Text(c, a.tr("Suche Kontakte mit gleichem Namen…", "Looking for contacts with the same name…")).FontSize(12).TextColor(t.TextMuted)
				return
			}
			if len(ct.dupGroups) == 0 || ct.dupMerge == nil {
				ui.Text(c, a.tr("Keine Kontakte mit gleichem Namen gefunden.", "No contacts with the same name found.")).FontSize(13)
				return
			}
			m := ct.dupMerge
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				prev := ui.Button(c.Key("ct-dup-prev"), "‹").Padding(2, 9)
				if ct.dupIdx == 0 {
					prev.Disabled(true)
				}
				if prev.Clicked() {
					a.showDuplicateGroup(ct.dupIdx - 1)
				}
				ui.Text(c, a.trf("Gruppe %d von %d · %s", "Group %d of %d · %s", ct.dupIdx+1, len(ct.dupGroups), contactName(m.group[0]))).FontSize(13).Bold().Grow(1).SingleLine()
				next := ui.Button(c.Key("ct-dup-next"), "›").Padding(2, 9)
				if ct.dupIdx >= len(ct.dupGroups)-1 {
					next.Disabled(true)
				}
				if next.Clicked() {
					a.showDuplicateGroup(ct.dupIdx + 1)
				}
			})
			ui.Text(c, a.tr(
				"Gleicher Name ist nicht immer dieselbe Person – bitte prüfen. Pro Feld den Wert anklicken, der bleiben soll. Notizen werden zusammengefasst.",
				"The same name is not always the same person – please check. Click the value to keep for each field. Notes are combined.",
			)).FontSize(12).TextColor(t.TextMuted)

			cell := func(key string, i int, val string, chosen, same bool) {
				bg, border := t.Surface, withAlpha(t.Border, 0.7)
				if chosen && !same {
					bg, border = withAlpha(t.Accent, 0.16), t.Accent
				}
				txt := val
				if strings.TrimSpace(txt) == "" {
					txt = "–"
				}
				box := ui.Box(c.Key(fmt.Sprintf("ct-dup-%s-%d", key, i))).Grow(1).MinWidth(0).Basis(0).Padding(5, 8).Radius(6).Background(bg).Border(1, border)
				if !same && strings.TrimSpace(val) != "" {
					box.OnClick(func() { m.pick[key] = i })
				}
				box.Children(func() {
					clr := t.Text
					if strings.TrimSpace(val) == "" || (same && i > 0) {
						clr = t.TextMuted
					}
					ui.Text(c, txt).FontSize(12).TextColor(clr).MaxLines(3)
				})
			}
			// Header: one column per entry.
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				ui.Box(c).Width(110).Shrink(0)
				for i := range m.group {
					lbl := a.trf("Eintrag %d", "Entry %d", i+1)
					if i == m.pick["_base"] {
						lbl += a.tr(" (bleibt)", " (kept)")
					}
					ui.Text(c, lbl).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1).Basis(0).MinWidth(0)
				}
			})
			for _, key := range contactMergeFields {
				any, same := false, true
				first := strings.TrimSpace(contactFieldValue(m.group[0], key))
				for _, r := range m.group {
					v := strings.TrimSpace(contactFieldValue(r, key))
					any = any || v != ""
					same = same && v == first
				}
				if !any {
					continue
				}
				ui.Row(c.Key("ct-dup-row-" + key)).FillWidth().Gap(6).AlignItems(ui.Stretch).Children(func() {
					ui.Text(c, contactFieldLabelLang(key, a.uiLang())).FontSize(12).TextColor(t.TextMuted).Width(110).Shrink(0).Padding(5, 0, 0, 0)
					for i, r := range m.group {
						cell(key, i, contactFieldValue(r, key), m.pick[key] == i, same)
					}
				})
			}

			// Email addresses: all are kept, the first two as fields, the rest in the notes.
			ui.Text(c, a.tr("E-Mail-Adressen", "Email addresses")).FontSize(13).Bold().Padding(6, 0, 0, 0)
			for i, em := range m.emails {
				ui.Row(c.Key(fmt.Sprintf("ct-dup-em-%d", i))).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					role, clr := a.tr("→ Notizen", "→ notes"), t.TextMuted
					switch i {
					case m.primary:
						role, clr = a.tr("E-Mail", "Email"), t.Accent
					case m.second:
						role, clr = a.tr("2. E-Mail", "2nd email"), t.Text
					}
					ui.Text(c, role).FontSize(11.5).Bold().TextColor(clr).Width(110).Shrink(0)
					ui.Text(c, em).FontSize(12).Grow(1).MinWidth(0).SingleLine()
					if i != m.primary && ui.Button(c.Key(fmt.Sprintf("ct-dup-em1-%d", i)), a.tr("Als E-Mail", "As email")).Padding(2, 8).Clicked() {
						if m.second == i {
							m.second = m.primary
						}
						m.primary = i
					}
					if i != m.second && i != m.primary && ui.Button(c.Key(fmt.Sprintf("ct-dup-em2-%d", i)), a.tr("Als 2. E-Mail", "As 2nd email")).Padding(2, 8).Clicked() {
						m.second = i
					}
				})
			}

			merged, del := m.result()
			if merged.Notes != "" {
				ui.Text(c, a.tr("Notizen danach", "Notes afterwards")).FontSize(13).Bold().Padding(6, 0, 0, 0)
				ui.Text(c, merged.Notes).FontSize(12).TextColor(t.TextMuted).MaxLines(6)
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(6, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if ct.dupIdx < len(ct.dupGroups)-1 && ui.Button(c.Key("ct-dup-skip"), a.tr("Überspringen", "Skip")).Clicked() {
					a.showDuplicateGroup(ct.dupIdx + 1)
				}
				lbl := a.trf("%d Einträge zusammenführen", "Merge %d entries", len(del)+1)
				if ct.busy {
					lbl = a.tr("Führe zusammen…", "Merging…")
				}
				btn := ui.PrimaryButton(c.Key("ct-dup-merge"), lbl)
				if ct.busy {
					btn.Disabled(true)
				}
				if btn.Clicked() && !ct.busy {
					a.applyContactMerge(context.Background())
				}
			})
		})
	})
}
