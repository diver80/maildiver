package mailengine

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

// TBProfileInfo describes a discovered local Thunderbird profile directory.
type TBProfileInfo struct {
	Name      string
	Path      string
	IsDefault bool
}

// TBDetectedCalendar summarizes a non-ICS calendar found in Thunderbird (e.g. Google / CalDAV / Local).
type TBDetectedCalendar struct {
	ID       string
	Name     string
	Type     string // "ics", "storage", "caldav", "gdata"
	URI      string
	Color    string
	ReadOnly bool
	IsGoogle bool
}

// TBImportPreview shows everything discovered in a Thunderbird profile before anything is imported.
type TBImportPreview struct {
	ProfileDir   string
	Accounts     []config.DirectAccount
	ICalFeeds    []config.ICalFeedConfig
	OtherCals    []TBDetectedCalendar
	LocalEvents  []StoredEvent
	Contacts     []backend.ContactRecord
	Filters      []backend.FilterUpsertRequest
	SignatureCnt int
	Warnings     []string
}

// TBImportOptions selects which categories to import from a Thunderbird profile.
type TBImportOptions struct {
	ImportAccounts    bool
	ImportICalFeeds   bool
	ImportLocalEvents bool
	ImportContacts    bool
	ImportFilters     bool
}

// TBImportReport summarizes what was imported vs. skipped (already present).
type TBImportReport struct {
	AccountsAdded    int
	AccountsSkipped  int
	SignaturesLoaded int
	FeedsAdded       int
	FeedsSkipped     int
	EventsAdded      int
	ContactsAdded    int
	ContactsSkipped  int
	FiltersAdded     int
	FiltersSkipped   int
}

// DiscoverThunderbirdProfiles locates installed Thunderbird profiles on macOS, Linux, and Windows
// purely read-only (never writes to any Thunderbird file).
func DiscoverThunderbirdProfiles() []TBProfileInfo {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var roots []string
	switch runtime.GOOS {
	case "darwin":
		roots = []string{filepath.Join(home, "Library", "Thunderbird")}
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			roots = []string{filepath.Join(ad, "Thunderbird")}
		}
	default:
		roots = []string{
			filepath.Join(home, ".thunderbird"),
			filepath.Join(home, ".var", "app", "org.mozilla.Thunderbird", ".thunderbird"),
			filepath.Join(home, "snap", "thunderbird", "common", ".thunderbird"),
		}
	}

	seen := map[string]bool{}
	var out []TBProfileInfo
	for _, root := range roots {
		iniPath := filepath.Join(root, "profiles.ini")
		for _, p := range parseProfilesINI(root, iniPath) {
			abs, _ := filepath.Abs(p.Path)
			if abs == "" || seen[abs] {
				continue
			}
			if st, err := os.Stat(filepath.Join(abs, "prefs.js")); err == nil && !st.IsDir() {
				seen[abs] = true
				p.Path = abs
				out = append(out, p)
			}
		}
		// Fallback: scan Profiles/ subdirectories in case profiles.ini is missing or partial.
		entries, err := os.ReadDir(filepath.Join(root, "Profiles"))
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				full := filepath.Join(root, "Profiles", e.Name())
				abs, _ := filepath.Abs(full)
				if seen[abs] {
					continue
				}
				if st, err := os.Stat(filepath.Join(abs, "prefs.js")); err == nil && !st.IsDir() {
					seen[abs] = true
					out = append(out, TBProfileInfo{Name: e.Name(), Path: abs})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func parseProfilesINI(root, iniPath string) []TBProfileInfo {
	f, err := os.Open(iniPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []TBProfileInfo
	var cur *TBProfileInfo
	isRelative := true
	defaultPath := ""

	flush := func() {
		if cur == nil || cur.Path == "" {
			cur = nil
			return
		}
		p := cur.Path
		if isRelative {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		cur.Path = p
		if cur.Name == "" {
			cur.Name = filepath.Base(p)
		}
		out = append(out, *cur)
		cur = nil
		isRelative = true
	}

	sc := bufio.NewScanner(f)
	inInstall := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			sec := line[1 : len(line)-1]
			inInstall = strings.HasPrefix(sec, "Install")
			if strings.HasPrefix(sec, "Profile") {
				cur = &TBProfileInfo{}
				isRelative = true
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if inInstall && k == "Default" && v != "" {
			defaultPath = filepath.Join(root, filepath.FromSlash(v))
			continue
		}
		if cur == nil {
			continue
		}
		switch k {
		case "Name":
			cur.Name = v
		case "Path":
			cur.Path = v
		case "IsRelative":
			isRelative = v != "0"
		case "Default":
			if v == "1" {
				cur.IsDefault = true
			}
		}
	}
	flush()
	if defaultPath != "" {
		absDef, _ := filepath.Abs(defaultPath)
		for i := range out {
			if abs, _ := filepath.Abs(out[i].Path); abs == absDef {
				out[i].IsDefault = true
			}
		}
	}
	return out
}

// InspectThunderbirdProfile inspects a Thunderbird profile folder strictly read-only and returns
// a structured preview of accounts (+ signatures), calendars, local events, contacts, and filters.
func InspectThunderbirdProfile(profileDir string) (TBImportPreview, error) {
	profileDir = strings.TrimSpace(profileDir)
	if strings.HasPrefix(profileDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			profileDir = filepath.Join(home, profileDir[2:])
		}
	}
	if profileDir == "" {
		return TBImportPreview{}, fmt.Errorf("Kein Thunderbird-Profilordner angegeben")
	}
	prefsPath := filepath.Join(profileDir, "prefs.js")
	rawPrefs, err := os.ReadFile(prefsPath)
	if err != nil {
		return TBImportPreview{}, fmt.Errorf("prefs.js nicht gefunden in %s: %w", profileDir, err)
	}

	prefs := parseTBPrefs(string(rawPrefs))
	prev := TBImportPreview{ProfileDir: profileDir}

	// 1. Accounts (IMAP + SMTP + Identity + Signature)
	accounts, accByHost := extractTBAccounts(profileDir, prefs)
	prev.Accounts = accounts
	for _, a := range accounts {
		if strings.TrimSpace(a.Signature) != "" {
			prev.SignatureCnt++
		}
	}

	// 2. Calendars (prefs.js registry + calendar-data/local.sqlite)
	feeds, others := extractTBCalendars(prefs)
	prev.ICalFeeds = feeds
	prev.OtherCals = others
	if localEvs, lerr := readTBLocalCalendarEvents(profileDir, others); lerr == nil {
		prev.LocalEvents = localEvs
	}

	// 3. Contacts (abook.sqlite, abook-*.sqlite, history.sqlite)
	contacts, cerr := readTBContacts(profileDir)
	if cerr != nil {
		prev.Warnings = append(prev.Warnings, "Kontakte: "+cerr.Error())
	} else {
		prev.Contacts = contacts
	}

	// 4. Filter Rules (ImapMail/<host>/msgFilterRules.dat)
	prev.Filters = readTBFilters(profileDir, accByHost)

	return prev, nil
}

// ApplyThunderbirdImport imports the selected categories from profileDir into PostDive.
// Existing accounts (matched by email+imapHost), feeds (matched by URL), contacts (matched by email),
// and filters (matched by account+name) are preserved without creating duplicates.
func (e *Engine) ApplyThunderbirdImport(ctx context.Context, profileDir string, opts TBImportOptions) (TBImportReport, error) {
	prev, err := InspectThunderbirdProfile(profileDir)
	if err != nil {
		return TBImportReport{}, err
	}

	var rep TBImportReport
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	cfgDirty := false

	// 1. Accounts & Signatures
	if opts.ImportAccounts {
		existingKey := map[string]bool{}
		for _, a := range cfg.Direct.Accounts {
			existingKey[strings.ToLower(a.Email+"|"+a.IMAPHost)] = true
		}
		for _, a := range prev.Accounts {
			k := strings.ToLower(a.Email + "|" + a.IMAPHost)
			if existingKey[k] {
				rep.AccountsSkipped++
				continue
			}
			// Imported accounts start enabled=false until the user enters their password & tests connection,
			// so PostDive doesn't hammer IMAP servers with missing credentials.
			a.Enabled = false
			cfg.Direct.Accounts = append(cfg.Direct.Accounts, a)
			if !cfg.Mailbox.AccountEnabled(a.ID) {
				cfg.Mailbox.TriageAccountIDs = append(cfg.Mailbox.TriageAccountIDs, a.ID)
			}
			existingKey[k] = true
			cfgDirty = true
			rep.AccountsAdded++
			if strings.TrimSpace(a.Signature) != "" {
				rep.SignaturesLoaded++
			}
		}
	}

	// 2. iCal Feeds
	if opts.ImportICalFeeds {
		existingURL := map[string]bool{}
		for _, f := range cfg.Direct.ICalFeeds {
			existingURL[strings.ToLower(strings.TrimSpace(f.URL))] = true
		}
		for _, f := range prev.ICalFeeds {
			key := strings.ToLower(strings.TrimSpace(f.URL))
			if key == "" || existingURL[key] {
				rep.FeedsSkipped++
				continue
			}
			cfg.Direct.ICalFeeds = append(cfg.Direct.ICalFeeds, f)
			existingURL[key] = true
			cfgDirty = true
			rep.FeedsAdded++
		}
	}

	var saveErr error
	if cfgDirty {
		saveErr = e.saveConfig(cfg)
	}
	e.cfgMu.Unlock()
	if saveErr != nil {
		return rep, fmt.Errorf("Konfiguration speichern: %w", saveErr)
	}

	// 3. Local Calendar Events
	if opts.ImportLocalEvents && len(prev.LocalEvents) > 0 {
		for _, ev := range prev.LocalEvents {
			ev.CalendarID = LocalCalendarID
			ev.CalendarName = "Persönlich"
			ev.ReadOnly = false
			if ev.ID == "" {
				ev.ID = LocalCalendarID + "::" + ev.UID
			}
			if err := e.store.UpsertEvent(ctx, ev); err == nil {
				rep.EventsAdded++
			}
		}
		if rep.EventsAdded > 0 {
			e.emitCalendarChanged()
		}
	}

	// 4. Contacts
	if opts.ImportContacts && len(prev.Contacts) > 0 {
		existing, lerr := e.store.ListAllContacts(ctx)
		if lerr != nil {
			return rep, fmt.Errorf("bestehende Kontakte lesen: %w", lerr)
		}
		existingEmails := map[string]bool{}
		for _, c := range existing {
			if em := strings.ToLower(strings.TrimSpace(c.Email)); em != "" {
				existingEmails[em] = true
			}
			if em2 := strings.ToLower(strings.TrimSpace(c.SecondEmail)); em2 != "" {
				existingEmails[em2] = true
			}
		}
		for _, c := range prev.Contacts {
			emailKey := strings.ToLower(strings.TrimSpace(c.Email))
			if emailKey != "" && existingEmails[emailKey] {
				rep.ContactsSkipped++
				continue
			}
			c.ID = ""
			c.AddressBookID = LocalAddressBookID
			if _, err := e.store.SaveContactFull(ctx, c); err == nil {
				rep.ContactsAdded++
				if emailKey != "" {
					existingEmails[emailKey] = true
				}
				if em2 := strings.ToLower(strings.TrimSpace(c.SecondEmail)); em2 != "" {
					existingEmails[em2] = true
				}
			}
		}
	}

	// 5. Filter Rules
	if opts.ImportFilters && len(prev.Filters) > 0 {
		// Re-map Thunderbird account IDs to actual configured PostDive account IDs when possible.
		configuredIDs := map[string]bool{}
		accIDByEmail := map[string]string{}
		for _, a := range cfg.Direct.Accounts {
			configuredIDs[a.ID] = true
			if em := strings.ToLower(strings.TrimSpace(a.Email)); em != "" {
				accIDByEmail[em] = a.ID
			}
		}
		tbIDToEmail := map[string]string{}
		for _, a := range prev.Accounts {
			if em := strings.ToLower(strings.TrimSpace(a.Email)); em != "" {
				tbIDToEmail[a.ID] = em
			}
		}
		existingFilters, _ := e.store.ListFilters(ctx, "")
		existingKey := map[string]bool{}
		for _, f := range existingFilters {
			existingKey[strings.ToLower(f.AccountID+"|"+f.Name)] = true
		}
		for _, req := range prev.Filters {
			if !configuredIDs[req.AccountID] {
				if em, ok := tbIDToEmail[req.AccountID]; ok {
					if mappedID, ok := accIDByEmail[em]; ok {
						req.AccountID = mappedID
					}
				}
			}
			if (req.AccountID == "" || !configuredIDs[req.AccountID]) && len(cfg.Direct.Accounts) > 0 {
				req.AccountID = cfg.Direct.Accounts[0].ID
			}
			k := strings.ToLower(req.AccountID + "|" + req.Name)
			if existingKey[k] {
				rep.FiltersSkipped++
				continue
			}
			req.Mode = "create"
			if _, err := e.store.UpsertFilter(ctx, req); err == nil {
				rep.FiltersAdded++
				existingKey[k] = true
			}
		}
	}

	// Trigger background sync for newly imported iCal feeds tied to Engine lifecycle.
	if rep.FeedsAdded > 0 {
		e.spawn(func(rootCtx context.Context) {
			sctx, cancel := context.WithTimeout(rootCtx, 30*time.Second)
			defer cancel()
			_ = e.SyncCalendarsNow(sctx)
		})
	}

	return rep, nil
}

// --- prefs.js parser ---

var tbPrefLineRe = regexp.MustCompile(`^\s*user_pref\(\s*"((?:[^"\\]|\\.)*)"\s*,\s*(.+?)\s*\);\s*$`)

func parseTBPrefs(content string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		m := tbPrefLineRe.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		key := unescapeJSString(m[1])
		val := strings.TrimSpace(m[2])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = unescapeJSString(val[1 : len(val)-1])
		}
		out[key] = val
	}
	return out
}

func unescapeJSString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"', '\'', '\\':
			b.WriteByte(s[i])
		case 'u':
			if i+4 < len(s) {
				if r, err := strconv.ParseInt(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(r))
					i += 4
					continue
				}
			}
			b.WriteString(`\u`)
		case 'x':
			if i+2 < len(s) {
				if r, err := strconv.ParseInt(s[i+1:i+3], 16, 32); err == nil {
					b.WriteByte(byte(r))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func extractTBAccounts(profileDir string, prefs map[string]string) ([]config.DirectAccount, map[string]string) {
	accList := strings.Split(prefs["mail.accountmanager.accounts"], ",")
	defaultSMTP := strings.TrimSpace(prefs["mail.smtp.defaultserver"])

	var out []config.DirectAccount
	accByHost := map[string]string{} // lowercase IMAP host or directory name -> accountID

	for _, rawAcc := range accList {
		accKey := strings.TrimSpace(rawAcc)
		if accKey == "" {
			continue
		}
		srvKey := strings.TrimSpace(prefs["mail.account."+accKey+".server"])
		if srvKey == "" {
			continue
		}
		srvType := strings.ToLower(strings.TrimSpace(prefs["mail.server."+srvKey+".type"]))
		if srvType != "imap" {
			continue // Skip Local Folders ("none"), RSS ("rss"), News ("nntp"), POP3
		}
		imapHost := strings.TrimSpace(prefs["mail.server."+srvKey+".hostname"])
		if imapHost == "" {
			continue
		}
		imapPort, _ := strconv.Atoi(prefs["mail.server."+srvKey+".port"])
		imapSock, _ := strconv.Atoi(prefs["mail.server."+srvKey+".socketType"])
		imapTLS := "tls"
		switch imapSock {
		case 2:
			imapTLS = "starttls"
			if imapPort <= 0 {
				imapPort = 143
			}
		default:
			imapTLS = "tls"
			if imapPort <= 0 {
				imapPort = 993
			}
		}
		imapUser := strings.TrimSpace(prefs["mail.server."+srvKey+".userName"])
		accName := strings.TrimSpace(prefs["mail.server."+srvKey+".name"])

		// First identity attached to this account
		idList := strings.Split(prefs["mail.account."+accKey+".identities"], ",")
		idKey := ""
		for _, cand := range idList {
			if c := strings.TrimSpace(cand); c != "" {
				idKey = c
				break
			}
		}

		email := imapUser
		displayName := ""
		smtpKey := defaultSMTP
		signature := ""
		if idKey != "" {
			if v := strings.TrimSpace(prefs["mail.identity."+idKey+".useremail"]); v != "" {
				email = v
			}
			displayName = strings.TrimSpace(prefs["mail.identity."+idKey+".fullName"])
			if v := strings.TrimSpace(prefs["mail.identity."+idKey+".smtpServer"]); v != "" {
				smtpKey = v
			}
			signature = extractTBSignature(profileDir, prefs, idKey)
		}
		if accName == "" {
			accName = email
		}

		smtpHost := ""
		smtpPort := 465
		smtpTLS := "tls"
		if smtpKey != "" {
			smtpHost = strings.TrimSpace(prefs["mail.smtpserver."+smtpKey+".hostname"])
			if p, err := strconv.Atoi(prefs["mail.smtpserver."+smtpKey+".port"]); err == nil && p > 0 {
				smtpPort = p
			}
			switch strings.TrimSpace(prefs["mail.smtpserver."+smtpKey+".try_ssl"]) {
			case "2":
				smtpTLS = "starttls"
				if smtpPort == 465 {
					smtpPort = 587
				}
			default:
				smtpTLS = "tls"
			}
		}
		if smtpHost == "" {
			smtpHost = strings.Replace(imapHost, "imap.", "smtp.", 1)
		}

		accID := sanitizeTBAccountID(email, imapHost, len(out)+1)
		acc := config.DirectAccount{
			ID:          accID,
			Name:        accName,
			Email:       email,
			DisplayName: displayName,
			Username:    imapUser,
			IMAPHost:    imapHost,
			IMAPPort:    imapPort,
			IMAPTLS:     imapTLS,
			SMTPHost:    smtpHost,
			SMTPPort:    smtpPort,
			SMTPTLS:     smtpTLS,
			SyncLimit:   250,
			Signature:   signature,
			Enabled:     false,
		}
		out = append(out, acc)

		accByHost[strings.ToLower(imapHost)] = accID
		if dir := strings.TrimSpace(prefs["mail.server."+srvKey+".directory"]); dir != "" {
			accByHost[strings.ToLower(filepath.Base(dir))] = accID
		}
		if dirRel := strings.TrimSpace(prefs["mail.server."+srvKey+".directory-rel"]); dirRel != "" {
			accByHost[strings.ToLower(filepath.Base(dirRel))] = accID
		}
	}
	return out, accByHost
}

func extractTBSignature(profileDir string, prefs map[string]string, idKey string) string {
	prefix := "mail.identity." + idKey + "."
	// 1. Signature from external file if attach_signature is true
	if prefs[prefix+"attach_signature"] == "true" {
		sigPath := strings.TrimSpace(prefs[prefix+"sig_file"])
		if sigPath == "" {
			if rel := strings.TrimSpace(prefs[prefix+"sig_file-rel"]); strings.HasPrefix(rel, "[ProfD]") {
				cand := filepath.Clean(filepath.Join(profileDir, filepath.FromSlash(strings.TrimPrefix(rel, "[ProfD]"))))
				if r, rerr := filepath.Rel(filepath.Clean(profileDir), cand); rerr == nil && !strings.HasPrefix(r, "..") {
					sigPath = cand
				}
			}
		}
		if sigPath != "" {
			if raw, err := readBoundedRegularFile(sigPath, 256*1024); err == nil {
				txt := decodeTBSignatureBytes(raw)
				if strings.HasSuffix(strings.ToLower(sigPath), ".html") || strings.HasSuffix(strings.ToLower(sigPath), ".htm") || strings.Contains(txt, "<br") || strings.Contains(txt, "<p") {
					txt = htmlToPlainText(txt)
				}
				return strings.TrimSpace(txt)
			}
		}
	}
	// 2. Inline signature (htmlSigText — Thunderbird stores both plain & HTML signatures here!)
	raw := strings.TrimSpace(prefs[prefix+"htmlSigText"])
	if raw == "" {
		return ""
	}
	if prefs[prefix+"htmlSigFormat"] == "true" || strings.Contains(raw, "<br") || strings.Contains(raw, "<p") || strings.Contains(raw, "<div") {
		raw = htmlToPlainText(raw)
	}
	return strings.TrimSpace(raw)
}

func decodeTBSignatureBytes(raw []byte) string {
	// Strip UTF-8 BOM if present.
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	// Handle UTF-16LE / UTF-16BE BOMs (common for Windows Thunderbird signature files).
	if len(raw) >= 2 && ((raw[0] == 0xFF && raw[1] == 0xFE) || (raw[0] == 0xFE && raw[1] == 0xFF)) {
		little := raw[0] == 0xFF
		data := raw[2:]
		u16 := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			if little {
				u16 = append(u16, uint16(data[i])|uint16(data[i+1])<<8)
			} else {
				u16 = append(u16, uint16(data[i])<<8|uint16(data[i+1]))
			}
		}
		return string(utf16.Decode(u16))
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	// Fallback: decode single-byte ISO-8859-1 / Windows-1252 bytes losslessly to UTF-8.
	var b strings.Builder
	b.Grow(len(raw))
	for _, c := range raw {
		b.WriteRune(rune(c))
	}
	return b.String()
}

// tbSQLiteDSN constructs a URL-escaped strictly read-only, immutable SQLite file URI
// so SQLite never creates -shm/-wal files inside a live Thunderbird profile.
func tbSQLiteDSN(dbPath string) string {
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(dbPath),
		RawQuery: "mode=ro&immutable=1",
	}
	return u.String()
}

func sanitizeTBAccountID(email, host string, idx int) string {
	src := strings.ToLower(strings.TrimSpace(email))
	if src == "" {
		src = strings.ToLower(strings.TrimSpace(host))
	}
	var b strings.Builder
	b.WriteString("tb-")
	for _, r := range src {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "tb" {
		return fmt.Sprintf("tb-acc-%d", idx)
	}
	return id
}

// --- Calendars from prefs.js + calendar-data/local.sqlite ---

func extractTBCalendars(prefs map[string]string) ([]config.ICalFeedConfig, []TBDetectedCalendar) {
	byID := map[string]*TBDetectedCalendar{}
	for k, v := range prefs {
		if !strings.HasPrefix(k, "calendar.registry.") {
			continue
		}
		rest := strings.TrimPrefix(k, "calendar.registry.")
		uuid, prop, ok := strings.Cut(rest, ".")
		if !ok || uuid == "" {
			continue
		}
		cal := byID[uuid]
		if cal == nil {
			cal = &TBDetectedCalendar{ID: uuid, Color: "#7aa2f7"}
			byID[uuid] = cal
		}
		switch prop {
		case "type":
			cal.Type = strings.ToLower(strings.TrimSpace(v))
		case "uri":
			cal.URI = strings.TrimSpace(v)
		case "name":
			cal.Name = strings.TrimSpace(v)
		case "color":
			if strings.HasPrefix(v, "#") {
				cal.Color = v
			}
		case "readOnly":
			cal.ReadOnly = v == "true"
		case "disabled":
			if v == "true" {
				cal.Type = "disabled"
			}
		}
	}

	var feeds []config.ICalFeedConfig
	var others []TBDetectedCalendar
	for _, cal := range byID {
		if cal.Type == "" || cal.Type == "disabled" {
			continue
		}
		if cal.Name == "" {
			cal.Name = "Thunderbird Kalender"
		}
		lowURI := strings.ToLower(cal.URI)
		cal.IsGoogle = cal.Type == "gdata" || strings.Contains(lowURI, "google.com") || strings.Contains(lowURI, "googleapis.com")
		if cal.Type == "ics" && cal.URI != "" {
			u := cal.URI
			if strings.HasPrefix(strings.ToLower(u), "file://") {
				if parsed, err := url.Parse(u); err == nil {
					u = parsed.Path
				}
			}
			feeds = append(feeds, config.ICalFeedConfig{
				ID:       "tb-" + cal.ID,
				Name:     cal.Name,
				URL:      u,
				Color:    cal.Color,
				ReadOnly: true,
				Enabled:  true,
			})
		} else {
			others = append(others, *cal)
		}
	}
	sort.Slice(feeds, func(i, j int) bool { return feeds[i].Name < feeds[j].Name })
	sort.Slice(others, func(i, j int) bool { return others[i].Name < others[j].Name })
	return feeds, others
}

func readTBLocalCalendarEvents(profileDir string, cals []TBDetectedCalendar) ([]StoredEvent, error) {
	dbPath := filepath.Join(profileDir, "calendar-data", "local.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", tbSQLiteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Thunderbird's local.sqlite stores timestamps in microseconds since Unix epoch.
	rows, err := db.Query(`SELECT id, title, event_start, event_end, flags FROM cal_events`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Load extra properties (LOCATION, DESCRIPTION) keyed by item_id.
	locs := map[string]string{}
	descs := map[string]string{}
	if prows, perr := db.Query(`SELECT item_id, key, value FROM cal_properties WHERE key IN ('LOCATION','DESCRIPTION')`); perr == nil {
		defer prows.Close()
		for prows.Next() {
			var id, k, v string
			if prows.Scan(&id, &k, &v) == nil {
				if k == "LOCATION" {
					locs[id] = v
				} else if k == "DESCRIPTION" {
					descs[id] = v
				}
			}
		}
	}

	var out []StoredEvent
	for rows.Next() {
		var id string
		var title sql.NullString
		var startUS, endUS, flags sql.NullInt64
		if err := rows.Scan(&id, &title, &startUS, &endUS, &flags); err != nil {
			continue
		}
		if strings.TrimSpace(title.String) == "" || !startUS.Valid {
			continue
		}
		st := time.Unix(startUS.Int64/1_000_000, 0).Local()
		en := st.Add(time.Hour)
		if endUS.Valid && endUS.Int64 > startUS.Int64 {
			en = time.Unix(endUS.Int64/1_000_000, 0).Local()
		}
		allDay := (flags.Int64 & 16) != 0 // CAL_ITEM_FLAG_EVENT_ALLDAY = 16 in Lightning
		startISO, endISO := st.Format(time.RFC3339), en.Format(time.RFC3339)
		if allDay {
			startISO, endISO = st.Format("2006-01-02"), en.Format("2006-01-02")
		}
		out = append(out, StoredEvent{
			ID:           LocalCalendarID + "::tb-" + id,
			CalendarID:   LocalCalendarID,
			CalendarName: "Persönlich",
			ExternalID:   "tb-" + id,
			UID:          id,
			Title:        strings.TrimSpace(title.String),
			StartISO:     startISO,
			EndISO:       endISO,
			StartUnix:    st.Unix(),
			EndUnix:      en.Unix(),
			AllDay:       allDay,
			Location:     strings.TrimSpace(locs[id]),
			Description:  strings.TrimSpace(descs[id]),
			Status:       "confirmed",
		})
	}
	return out, nil
}

// --- Contacts from abook*.sqlite ---

func readTBContacts(profileDir string) ([]backend.ContactRecord, error) {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return nil, err
	}
	var dbFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		if low == "abook.sqlite" || (strings.HasPrefix(low, "abook-") && strings.HasSuffix(low, ".sqlite")) || low == "history.sqlite" {
			dbFiles = append(dbFiles, filepath.Join(profileDir, e.Name()))
		}
	}
	sort.Strings(dbFiles)

	seenEmail := map[string]bool{}
	var out []backend.ContactRecord
	for _, p := range dbFiles {
		recs, err := readSingleTBAbook(p)
		if err != nil {
			continue
		}
		for _, r := range recs {
			k := strings.ToLower(strings.TrimSpace(r.Email))
			if k != "" {
				if seenEmail[k] {
					continue
				}
				seenEmail[k] = true
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func readSingleTBAbook(dbPath string) ([]backend.ContactRecord, error) {
	db, err := sql.Open("sqlite", tbSQLiteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT card, name, value FROM properties`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCard := map[string]map[string]string{}
	var order []string
	for rows.Next() {
		var card, name, val string
		if err := rows.Scan(&card, &name, &val); err != nil {
			continue
		}
		if _, ok := byCard[card]; !ok {
			byCard[card] = map[string]string{}
			order = append(order, card)
		}
		byCard[card][name] = strings.TrimSpace(val)
	}

	var out []backend.ContactRecord
	for _, card := range order {
		m := byCard[card]
		email := m["PrimaryEmail"]
		display := m["DisplayName"]
		first := m["FirstName"]
		last := m["LastName"]
		if display == "" {
			display = strings.TrimSpace(first + " " + last)
		}
		if display == "" && email == "" {
			continue
		}
		if display == "" {
			display = email
		}
		phone := m["WorkPhone"]
		if phone == "" {
			phone = m["HomePhone"]
		}
		addrParts := []string{}
		for _, k := range []string{"WorkAddress", "WorkZipCode", "WorkCity", "WorkCountry"} {
			if v := m[k]; v != "" {
				addrParts = append(addrParts, v)
			}
		}
		if len(addrParts) == 0 {
			for _, k := range []string{"HomeAddress", "HomeZipCode", "HomeCity", "HomeCountry"} {
				if v := m[k]; v != "" {
					addrParts = append(addrParts, v)
				}
			}
		}
		out = append(out, backend.ContactRecord{
			AddressBookID: LocalAddressBookID,
			DisplayName:   display,
			FirstName:     first,
			LastName:      last,
			Email:         email,
			SecondEmail:   m["SecondEmail"],
			Phone:         phone,
			Mobile:        m["CellularNumber"],
			Company:       m["Company"],
			JobTitle:      m["JobTitle"],
			Website:       m["WebPage1"],
			Address:       strings.Join(addrParts, ", "),
			Notes:         m["Notes"],
		})
	}
	return out, nil
}

// --- Filter Rules from ImapMail/<host>/msgFilterRules.dat ---

var tbCondItemRe = regexp.MustCompile(`\(([^,]+),([^,]+),([^)]*)\)`)

func readTBFilters(profileDir string, accByHost map[string]string) []backend.FilterUpsertRequest {
	imapDir := filepath.Join(profileDir, "ImapMail")
	entries, err := os.ReadDir(imapDir)
	if err != nil {
		return nil
	}
	var out []backend.FilterUpsertRequest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		datPath := filepath.Join(imapDir, e.Name(), "msgFilterRules.dat")
		data, err := os.ReadFile(datPath)
		if err != nil {
			continue
		}
		accID := accByHost[strings.ToLower(e.Name())]
		out = append(out, parseTBFilterRules(string(data), accID)...)
	}
	return out
}

func parseTBFilterRules(content, accountID string) []backend.FilterUpsertRequest {
	var out []backend.FilterUpsertRequest
	type rawRule struct {
		name    string
		enabled bool
		cond    string
		actions []backend.FilterActionSpec
		lastAct string
	}
	var cur *rawRule

	flush := func() {
		if cur == nil || cur.name == "" || len(cur.actions) == 0 {
			cur = nil
			return
		}
		matchAll := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(cur.cond)), "AND")
		var conds []triage.NativeCondition
		for _, m := range tbCondItemRe.FindAllStringSubmatch(cur.cond, -1) {
			if len(m) != 4 {
				continue
			}
			field := strings.ToLower(strings.TrimSpace(m[1]))
			op := strings.ToLower(strings.TrimSpace(m[2]))
			val := strings.Trim(strings.TrimSpace(m[3]), `"`)
			if val == "" {
				continue
			}
			switch field {
			case "from", "sender":
				field = "from"
			case "subject":
				field = "subject"
			default:
				continue
			}
			switch op {
			case "contains", "is":
			case "doesn't contain":
				op = "doesNotContain"
			case "isn't":
				op = "isNot"
			case "begins with":
				op = "beginsWith"
			case "ends with":
				op = "endsWith"
			default:
				op = "contains"
			}
			conds = append(conds, triage.NativeCondition{
				Field:      field,
				Op:         op,
				Value:      val,
				BooleanAnd: matchAll,
			})
		}
		if len(conds) > 0 {
			out = append(out, backend.FilterUpsertRequest{
				Mode:       "create",
				AccountID:  accountID,
				Name:       cur.name,
				Enabled:    cur.enabled,
				MatchAll:   matchAll,
				Conditions: conds,
				Actions:    cur.actions,
			})
		}
		cur = nil
	}

	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "version=") || strings.HasPrefix(line, "logging=") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "name":
			flush()
			cur = &rawRule{name: v, enabled: true}
		case "enabled":
			if cur != nil {
				cur.enabled = strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
			}
		case "condition":
			if cur != nil {
				cur.cond = v
			}
		case "action":
			if cur == nil {
				continue
			}
			cur.lastAct = v
			switch strings.ToLower(v) {
			case "delete", "move to trash":
				cur.actions = append(cur.actions, backend.FilterActionSpec{Type: "moveToFolder", TargetFolderPath: "Trash"})
			case "mark read":
				cur.actions = append(cur.actions, backend.FilterActionSpec{Type: "markRead"})
			case "mark flagged":
				cur.actions = append(cur.actions, backend.FilterActionSpec{Type: "addStar"})
			case "move to folder":
				cur.actions = append(cur.actions, backend.FilterActionSpec{Type: "moveToFolder"})
			}
		case "actionValue":
			if cur != nil && strings.EqualFold(cur.lastAct, "move to folder") && len(cur.actions) > 0 {
				target := extractTBFolderTarget(v)
				cur.actions[len(cur.actions)-1].TargetFolderPath = target
			}
		}
	}
	flush()
	return out
}

func extractTBFolderTarget(uri string) string {
	// e.g. imap://user@imap.ionos.de/Archive/2025 -> Archive/2025
	if parsed, err := url.Parse(uri); err == nil && parsed.Path != "" {
		p, _ := url.PathUnescape(strings.TrimPrefix(parsed.Path, "/"))
		if p != "" {
			return p
		}
	}
	return strings.TrimPrefix(uri, "/")
}
