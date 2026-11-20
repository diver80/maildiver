package mailengine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/backend"
)

const (
	GoogleAddressBookID   = "google-contacts"
	GoogleAddressBookName = "Google Kontakte"
	peopleDefaultAPIBase  = "https://people.googleapis.com/v1"
)

// RecipientSuggestion is a compact autocomplete item for To/CC fields.
type RecipientSuggestion struct {
	DisplayName string
	Email       string
	Source      string // "contact" | "recent"
}

// DeleteContact removes a contact by ID from SQLite.
func (s *Store) DeleteContact(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE id=?`, id)
	return err
}

// SaveContactFull inserts or replaces a contact record (used by the Contacts editor UI).
func (s *Store) SaveContactFull(ctx context.Context, c backend.ContactRecord) (backend.ContactRecord, error) {
	if c.AddressBookID == "" {
		c.AddressBookID = LocalAddressBookID
	}
	c.Email = strings.TrimSpace(c.Email)
	c.SecondEmail = strings.TrimSpace(c.SecondEmail)
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	c.FirstName = strings.TrimSpace(c.FirstName)
	c.LastName = strings.TrimSpace(c.LastName)
	if c.DisplayName == "" {
		c.DisplayName = strings.TrimSpace(c.FirstName + " " + c.LastName)
	}
	if c.DisplayName == "" {
		c.DisplayName = c.Email
	}
	if c.Email == "" && c.DisplayName == "" {
		return c, fmt.Errorf("Name oder E-Mail-Adresse erforderlich")
	}
	if c.ID == "" {
		c.ID = fmt.Sprintf("ct-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contacts(
			id, address_book_id, display_name, first_name, last_name, email, second_email,
			phone, mobile, company, job_title, website, address, notes, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			address_book_id=excluded.address_book_id,
			display_name=excluded.display_name,
			first_name=excluded.first_name,
			last_name=excluded.last_name,
			email=excluded.email,
			second_email=excluded.second_email,
			phone=excluded.phone,
			mobile=excluded.mobile,
			company=excluded.company,
			job_title=excluded.job_title,
			website=excluded.website,
			address=excluded.address,
			notes=excluded.notes,
			updated_at=excluded.updated_at`,
		c.ID, c.AddressBookID, c.DisplayName, c.FirstName, c.LastName, c.Email, c.SecondEmail,
		strings.TrimSpace(c.Phone), strings.TrimSpace(c.Mobile), strings.TrimSpace(c.Company),
		strings.TrimSpace(c.JobTitle), strings.TrimSpace(c.Website), strings.TrimSpace(c.Address),
		strings.TrimSpace(c.Notes), now,
	)
	return c, err
}

// SuggestRecipients returns matching recipient suggestions from the address book and recent emails.
func (s *Store) SuggestRecipients(ctx context.Context, query string, limit int) []RecipientSuggestion {
	if limit <= 0 || limit > 25 {
		limit = 8
	}
	q := strings.ToLower(strings.TrimSpace(query))
	seen := map[string]bool{}
	var out []RecipientSuggestion

	add := func(name, email, src string) {
		email = strings.TrimSpace(email)
		if email == "" || len(out) >= limit {
			return
		}
		key := strings.ToLower(email)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, RecipientSuggestion{
			DisplayName: strings.TrimSpace(name),
			Email:       email,
			Source:      src,
		})
	}

	// 1. Address book contacts first.
	contacts, err := s.SearchContacts(ctx, q)
	if err == nil {
		for _, c := range contacts {
			add(c.DisplayName, c.Email, "contact")
			if c.SecondEmail != "" {
				add(c.DisplayName, c.SecondEmail, "contact")
			}
			if len(out) >= limit {
				return out
			}
		}
	}

	// 2. Complement with recent senders from cached messages.
	like := "%" + q + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_addr FROM messages
		WHERE from_addr != '' AND (lower(from_addr) LIKE ?)
		ORDER BY date_unix DESC LIMIT 60`, like)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() && len(out) < limit {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		if addr, err := mail.ParseAddress(raw); err == nil && addr.Address != "" {
			add(addr.Name, addr.Address, "recent")
		} else if strings.Contains(raw, "@") {
			add("", raw, "recent")
		}
	}
	return out
}

// --- Engine contact management methods ---

func (e *Engine) ListContacts(ctx context.Context, query string) ([]backend.ContactRecord, error) {
	return e.store.SearchContacts(ctx, query)
}

func (e *Engine) SaveContact(ctx context.Context, c backend.ContactRecord) (backend.ContactRecord, error) {
	return e.store.SaveContactFull(ctx, c)
}

func (e *Engine) DeleteContact(ctx context.Context, id string) error {
	return e.store.DeleteContact(ctx, id)
}

func (e *Engine) SuggestRecipients(ctx context.Context, query string, limit int) []RecipientSuggestion {
	return e.store.SuggestRecipients(ctx, query, limit)
}

// --- vCard (.vcf) Import & Export (vCard 2.1 / 3.0 / 4.0) ---

// ParseVCF parses one or more VCARD records from r into ContactRecords.
func ParseVCF(r io.Reader) ([]backend.ContactRecord, error) {
	// Unfold RFC 6350/2426 folded lines (CRLF/LF followed by single space or tab).
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if len(lines) > 0 && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			lines[len(lines)-1] += line[1:]
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("vCard lesen: %w", err)
	}

	var out []backend.ContactRecord
	var cur *backend.ContactRecord
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if upper == "BEGIN:VCARD" {
			cur = &backend.ContactRecord{AddressBookID: LocalAddressBookID}
			continue
		}
		if upper == "END:VCARD" {
			if cur != nil {
				if cur.DisplayName == "" {
					cur.DisplayName = strings.TrimSpace(cur.FirstName + " " + cur.LastName)
				}
				if cur.DisplayName == "" {
					cur.DisplayName = cur.Email
				}
				if cur.DisplayName != "" || cur.Email != "" {
					out = append(out, *cur)
				}
			}
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		colon := indexUnquotedColon(line)
		if colon <= 0 {
			continue
		}
		propPart := line[:colon]
		rawVal := line[colon+1:]

		segs := strings.Split(propPart, ";")
		propName := strings.ToUpper(strings.TrimSpace(segs[0]))
		// Strip optional group prefix like "item1.EMAIL"
		if dot := strings.LastIndexByte(propName, '.'); dot >= 0 {
			propName = propName[dot+1:]
		}
		paramsUpper := strings.ToUpper(strings.Join(segs[1:], ";"))
		if strings.Contains(paramsUpper, "ENCODING=QUOTED-PRINTABLE") || strings.Contains(paramsUpper, "QUOTED-PRINTABLE") {
			if decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(rawVal))); err == nil {
				rawVal = string(decoded)
			}
		}

		switch propName {
		case "FN":
			if cur.DisplayName == "" {
				cur.DisplayName = strings.TrimSpace(unescapeVCardValue(rawVal))
			}
		case "N":
			parts := splitUnescapedSemicolon(rawVal)
			if len(parts) >= 1 && cur.LastName == "" {
				cur.LastName = strings.TrimSpace(unescapeVCardValue(parts[0]))
			}
			if len(parts) >= 2 && cur.FirstName == "" {
				cur.FirstName = strings.TrimSpace(unescapeVCardValue(parts[1]))
			}
		case "EMAIL":
			em := strings.TrimSpace(unescapeVCardValue(rawVal))
			if em == "" {
				continue
			}
			if cur.Email == "" {
				cur.Email = em
			} else if cur.SecondEmail == "" && !strings.EqualFold(cur.Email, em) {
				cur.SecondEmail = em
			}
		case "TEL":
			tel := strings.TrimSpace(unescapeVCardValue(rawVal))
			if tel == "" {
				continue
			}
			if strings.Contains(paramsUpper, "CELL") || strings.Contains(paramsUpper, "MOBILE") {
				if cur.Mobile == "" {
					cur.Mobile = tel
				} else if cur.Phone == "" {
					cur.Phone = tel
				}
			} else {
				if cur.Phone == "" {
					cur.Phone = tel
				} else if cur.Mobile == "" {
					cur.Mobile = tel
				}
			}
		case "ORG":
			if cur.Company == "" {
				var orgParts []string
				for _, p := range splitUnescapedSemicolon(rawVal) {
					if s := strings.TrimSpace(unescapeVCardValue(p)); s != "" {
						orgParts = append(orgParts, s)
					}
				}
				cur.Company = strings.Join(orgParts, " ")
			}
		case "TITLE":
			if cur.JobTitle == "" {
				cur.JobTitle = strings.TrimSpace(unescapeVCardValue(rawVal))
			}
		case "URL":
			if cur.Website == "" {
				cur.Website = strings.TrimSpace(unescapeVCardValue(rawVal))
			}
		case "ADR":
			if cur.Address == "" {
				var parts []string
				for _, p := range splitUnescapedSemicolon(rawVal) {
					if s := strings.TrimSpace(unescapeVCardValue(p)); s != "" {
						parts = append(parts, s)
					}
				}
				cur.Address = strings.Join(parts, ", ")
			}
		case "NOTE":
			if cur.Notes == "" {
				cur.Notes = strings.TrimSpace(unescapeVCardValue(rawVal))
			}
		}
	}
	return out, nil
}

func indexUnquotedColon(line string) int {
	inQuotes := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case ':':
			if !inQuotes {
				return i
			}
		}
	}
	return -1
}

func splitUnescapedSemicolon(s string) []string {
	var parts []string
	start := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == ';' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func unescapeVCardValue(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(s)
}

func escapeVCardValue(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`, ",", `\,`, ";", `\;`)
	return r.Replace(s)
}

func cleanVCardSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}

// FormatVCF serializes ContactRecords as standard RFC 6350 vCard 3.0 (universally accepted by macOS, iOS, Thunderbird, Google).
func FormatVCF(contacts []backend.ContactRecord) string {
	var b strings.Builder
	for _, c := range contacts {
		b.WriteString("BEGIN:VCARD\r\n")
		b.WriteString("VERSION:3.0\r\n")
		fn := strings.TrimSpace(c.DisplayName)
		if fn == "" {
			fn = strings.TrimSpace(c.FirstName + " " + c.LastName)
		}
		if fn == "" {
			fn = c.Email
		}
		fmt.Fprintf(&b, "FN:%s\r\n", escapeVCardValue(fn))
		if c.LastName != "" || c.FirstName != "" {
			fmt.Fprintf(&b, "N:%s;%s;;;\r\n", escapeVCardValue(c.LastName), escapeVCardValue(c.FirstName))
		}
		if em := cleanVCardSingleLine(c.Email); em != "" {
			fmt.Fprintf(&b, "EMAIL;TYPE=INTERNET:%s\r\n", em)
		}
		if em2 := cleanVCardSingleLine(c.SecondEmail); em2 != "" {
			fmt.Fprintf(&b, "EMAIL;TYPE=INTERNET:%s\r\n", em2)
		}
		if ph := cleanVCardSingleLine(c.Phone); ph != "" {
			fmt.Fprintf(&b, "TEL;TYPE=WORK,VOICE:%s\r\n", ph)
		}
		if mob := cleanVCardSingleLine(c.Mobile); mob != "" {
			fmt.Fprintf(&b, "TEL;TYPE=CELL,VOICE:%s\r\n", mob)
		}
		if c.Company != "" {
			fmt.Fprintf(&b, "ORG:%s\r\n", escapeVCardValue(c.Company))
		}
		if c.JobTitle != "" {
			fmt.Fprintf(&b, "TITLE:%s\r\n", escapeVCardValue(c.JobTitle))
		}
		if web := cleanVCardSingleLine(c.Website); web != "" {
			fmt.Fprintf(&b, "URL:%s\r\n", web)
		}
		if c.Address != "" {
			fmt.Fprintf(&b, "ADR;TYPE=WORK:;;%s;;;;\r\n", escapeVCardValue(c.Address))
		}
		if c.Notes != "" {
			fmt.Fprintf(&b, "NOTE:%s\r\n", escapeVCardValue(c.Notes))
		}
		b.WriteString("END:VCARD\r\n")
	}
	return b.String()
}

// ImportVCFFile parses a local .vcf file and upserts all contacts into the local address book.
func (e *Engine) ImportVCFFile(ctx context.Context, filePath string) (int, error) {
	p := expandHomePath(filePath)
	f, err := os.Open(p)
	if err != nil {
		return 0, fmt.Errorf(".vcf Datei nicht lesbar: %w", err)
	}
	defer f.Close()
	recs, err := ParseVCF(f)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, r := range recs {
		if _, _, err := e.store.UpsertContact(ctx, backend.ContactUpsertRequest{
			AddressBookID: LocalAddressBookID,
			Contact:       r,
		}); err == nil {
			imported++
		}
	}
	return imported, nil
}

// ExportVCFFile writes all contacts (or a subset) to a .vcf file.
func (e *Engine) ExportVCFFile(ctx context.Context, filePath string) (int, error) {
	p := expandHomePath(filePath)
	if p == "" {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, "Downloads", "maildiver-kontakte.vcf")
		}
	}
	recs, err := e.store.ListAllContacts(ctx)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(p, []byte(FormatVCF(recs)), 0o600); err != nil {
		return 0, err
	}
	return len(recs), nil
}

func expandHomePath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// --- Google Contacts Sync (Google People API v1) ---

func (e *Engine) peopleBaseURL() string {
	if e.gcalPeopleBase != "" {
		return strings.TrimRight(e.gcalPeopleBase, "/")
	}
	return peopleDefaultAPIBase
}

// SyncGoogleContacts pulls contacts from Google People API v1 using the existing Google OAuth token.
func (e *Engine) SyncGoogleContacts(ctx context.Context) (int, error) {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return 0, err
	}
	pageToken := ""
	synced := 0
	const maxPeoplePages = 100
	const maxPeopleBodyBytes = 16 * 1024 * 1024
	for pageNum := 0; pageNum < maxPeoplePages; pageNum++ {
		if err := ctx.Err(); err != nil {
			return synced, err
		}
		vals := url.Values{}
		vals.Set("personFields", "names,emailAddresses,phoneNumbers,organizations,urls,addresses,biographies")
		vals.Set("pageSize", "200")
		if pageToken != "" {
			vals.Set("pageToken", pageToken)
		}
		endpoint := e.peopleBaseURL() + "/people/me/connections?" + vals.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return synced, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return synced, fmt.Errorf("Google Contacts Anfrage fehlgeschlagen: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPeopleBodyBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return synced, fmt.Errorf("Google Contacts Antwort lesen: %w", readErr)
		}
		if resp.StatusCode == http.StatusForbidden {
			return synced, fmt.Errorf("Google Contacts Berechtigung fehlt — bitte Google-Konto im Kalender-Tab erneut verbinden (People API aktivieren)")
		}
		if resp.StatusCode >= 400 {
			return synced, fmt.Errorf("Google Contacts API (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var page struct {
			Connections []struct {
				ResourceName string `json:"resourceName"`
				Names        []struct {
					DisplayName string `json:"displayName"`
					GivenName   string `json:"givenName"`
					FamilyName  string `json:"familyName"`
				} `json:"names"`
				EmailAddresses []struct {
					Value string `json:"value"`
				} `json:"emailAddresses"`
				PhoneNumbers []struct {
					Value string `json:"value"`
					Type  string `json:"type"`
				} `json:"phoneNumbers"`
				Organizations []struct {
					Name  string `json:"name"`
					Title string `json:"title"`
				} `json:"organizations"`
				URLs []struct {
					Value string `json:"value"`
				} `json:"urls"`
				Addresses []struct {
					FormattedValue string `json:"formattedValue"`
				} `json:"addresses"`
				Biographies []struct {
					Value string `json:"value"`
				} `json:"biographies"`
			} `json:"connections"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return synced, err
		}
		for _, p := range page.Connections {
			rec := backend.ContactRecord{
				ID:            "gct-" + strings.ReplaceAll(p.ResourceName, "/", "_"),
				AddressBookID: GoogleAddressBookID,
			}
			if len(p.Names) > 0 {
				rec.DisplayName = p.Names[0].DisplayName
				rec.FirstName = p.Names[0].GivenName
				rec.LastName = p.Names[0].FamilyName
			}
			if len(p.EmailAddresses) > 0 {
				rec.Email = p.EmailAddresses[0].Value
			}
			if len(p.EmailAddresses) > 1 {
				rec.SecondEmail = p.EmailAddresses[1].Value
			}
			for _, ph := range p.PhoneNumbers {
				typ := strings.ToLower(ph.Type)
				if strings.Contains(typ, "mobile") || strings.Contains(typ, "cell") {
					if rec.Mobile == "" {
						rec.Mobile = ph.Value
					}
				} else if rec.Phone == "" {
					rec.Phone = ph.Value
				}
			}
			if len(p.Organizations) > 0 {
				rec.Company = p.Organizations[0].Name
				rec.JobTitle = p.Organizations[0].Title
			}
			if len(p.URLs) > 0 {
				rec.Website = p.URLs[0].Value
			}
			if len(p.Addresses) > 0 {
				rec.Address = strings.ReplaceAll(p.Addresses[0].FormattedValue, "\n", ", ")
			}
			if len(p.Biographies) > 0 {
				rec.Notes = p.Biographies[0].Value
			}
			if rec.DisplayName == "" && rec.Email == "" {
				continue
			}
			if _, err := e.store.SaveContactFull(ctx, rec); err == nil {
				synced++
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return synced, nil
}
