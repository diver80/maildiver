package config

import (
	"path/filepath"
	"strings"
)

// EngineDirect is the only engine mode: IMAP/SMTP + SQLite + Google Calendar / iCal.
const EngineDirect = "direct"

// AccountAlias is an additional sender identity (alias) for a DirectAccount.
type AccountAlias struct {
	Email         string `json:"email"`
	DisplayName   string `json:"displayName,omitempty"`
	Signature     string `json:"signature,omitempty"`
	SignatureHTML string `json:"signatureHtml,omitempty"`
}

// DirectAccount configures a standalone IMAP/SMTP mailbox. Passwords are NEVER
// stored in config.json; they live in secretstore under key "account:<ID>".
type DirectAccount struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Email       string         `json:"email"`
	DisplayName string         `json:"displayName,omitempty"`
	Username    string         `json:"username,omitempty"` // empty = Email
	Aliases     []AccountAlias `json:"aliases,omitempty"`

	IMAPHost string `json:"imapHost"`
	IMAPPort int    `json:"imapPort"`
	IMAPTLS  string `json:"imapTls"` // "tls" (implicit, 993) | "starttls" (143)

	SMTPHost string `json:"smtpHost"`
	SMTPPort int    `json:"smtpPort"`
	SMTPTLS  string `json:"smtpTls"` // "tls" (implicit, 465) | "starttls" (587)

	// AuthType: "" / "password" (default SASL PLAIN/LOGIN) or "xoauth2" (Gmail / Microsoft 365 OAuth2).
	AuthType      string `json:"authType,omitempty"`
	OAuthProvider string `json:"oauthProvider,omitempty"` // "google" | "microsoft"
	OAuthClientID string `json:"oauthClientId,omitempty"`
	OAuthTenant   string `json:"oauthTenant,omitempty"` // Microsoft tenant (default "common")

	// SyncLimit is how many of the newest mails each sync compares: INBOX uses it as-is
	// (default 500, max 2000), other folders at most 200; older mails are backfilled.
	SyncLimit         int    `json:"syncLimit,omitempty"`
	Signature         string `json:"signature,omitempty"`         // optional plain-text signature appended when composing
	SignatureHTML     string `json:"signatureHtml,omitempty"`     // optional HTML signature for desktop WYSIWYG compose
	SignaturePosition string `json:"signaturePosition,omitempty"` // "" / "above" (above reply quote) or "below" (after quote)
	Enabled           bool   `json:"enabled"`
}

// Bounds for DirectAccount.SyncLimit.
const (
	DefaultSyncLimit = 500
	MinSyncLimit     = 50
	MaxSyncLimit     = 2000
)

// EffectiveUsername returns Username if set, otherwise Email.
func (a DirectAccount) EffectiveUsername() string {
	if u := strings.TrimSpace(a.Username); u != "" {
		return u
	}
	return strings.TrimSpace(a.Email)
}

// GoogleCalendarConfig configures bidirectional Google Calendar API v3 sync.
// ClientSecret and OAuth tokens are stored in secretstore ("gcal:client_secret", "gcal:token").
type GoogleCalendarConfig struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"clientId,omitempty"`
	AccountEmail string `json:"accountEmail,omitempty"`
}

// ICalFeedConfig configures an imported/subscribed .ics calendar (HTTP/webcal URL or local file).
type ICalFeedConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Color    string `json:"color,omitempty"`
	ReadOnly bool   `json:"readOnly"`
	Enabled  bool   `json:"enabled"`
}

// DAVAccountConfig configures a CalDAV calendar or CardDAV address book server.
// Passwords live in secretstore under key "dav:<ID>", never in config.json.
type DAVAccountConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Color    string `json:"color,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// DirectConfig configures the Go mail/calendar engine.
type DirectConfig struct {
	// Mode is always "direct" (older files may say "thunderbird"; normalizing fixes that).
	Mode            string               `json:"mode,omitempty"`
	Accounts        []DirectAccount      `json:"accounts,omitempty"`
	GoogleCalendar  GoogleCalendarConfig `json:"googleCalendar,omitempty"`
	ICalFeeds       []ICalFeedConfig     `json:"icalFeeds,omitempty"`
	CalDAVAccounts  []DAVAccountConfig   `json:"caldavAccounts,omitempty"`
	CardDAVAccounts []DAVAccountConfig   `json:"carddavAccounts,omitempty"`
}

// MailProviderPreset describes well-known IMAP/SMTP defaults for 1-click setup.
type MailProviderPreset struct {
	ID            string
	Label         string
	IMAPHost      string
	IMAPPort      int
	IMAPTLS       string
	SMTPHost      string
	SMTPPort      int
	SMTPTLS       string
	AuthType      string
	OAuthProvider string
}

// MailProviderPresets returns common provider presets (IONOS, Gmail, iCloud, Fastmail, etc.).
func MailProviderPresets() []MailProviderPreset {
	return []MailProviderPreset{
		{ID: "ionos", Label: "IONOS", IMAPHost: "imap.ionos.de", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.ionos.de", SMTPPort: 465, SMTPTLS: "tls"},
		{ID: "gmail", Label: "Gmail (App-Passwort)", IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPTLS: "tls"},
		{ID: "icloud", Label: "iCloud Mail", IMAPHost: "imap.mail.me.com", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.mail.me.com", SMTPPort: 587, SMTPTLS: "starttls"},
		{ID: "fastmail", Label: "Fastmail", IMAPHost: "imap.fastmail.com", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.fastmail.com", SMTPPort: 465, SMTPTLS: "tls"},
		{ID: "gmx", Label: "GMX", IMAPHost: "imap.gmx.net", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "mail.gmx.net", SMTPPort: 587, SMTPTLS: "starttls"},
		{ID: "webde", Label: "WEB.DE", IMAPHost: "imap.web.de", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.web.de", SMTPPort: 587, SMTPTLS: "starttls"},
		{ID: "strato", Label: "STRATO", IMAPHost: "imap.strato.de", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.strato.de", SMTPPort: 465, SMTPTLS: "tls"},
		{ID: "1und1", Label: "1&1", IMAPHost: "imap.1und1.de", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.1und1.de", SMTPPort: 587, SMTPTLS: "starttls"},
		{ID: "gmail-oauth", Label: "Gmail (OAuth2 · eigene Client-ID)", IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPTLS: "tls", AuthType: "xoauth2", OAuthProvider: "google"},
		{ID: "m365-oauth", Label: "Microsoft 365 / Outlook (OAuth2 · eigene Client-ID)", IMAPHost: "outlook.office365.com", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "smtp.office365.com", SMTPPort: 587, SMTPTLS: "starttls", AuthType: "xoauth2", OAuthProvider: "microsoft"},
		{ID: "custom", Label: "Benutzerdefiniert / Custom", IMAPHost: "", IMAPPort: 993, IMAPTLS: "tls", SMTPHost: "", SMTPPort: 465, SMTPTLS: "tls"},
	}
}

// DirectDBPath returns the default local SQLite database path for the PostDive engine.
// Stored in DataDir() (~/Library/Application Support/postdive on macOS, ~/.local/share/postdive on Linux)
// so local calendars, contacts, and filter rules are never wiped by cache-cleaning tools.
func DirectDBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "postdive.db"), nil
}

// LegacyDirectDBPath returns the pre-0.5 database path under StateDir() (formerly ~/Library/Caches/postdive on macOS)
// used for automatic one-time migration into DataDir().
func LegacyDirectDBPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "postdive.db"), nil
}

// LegacyDirectDBPaths returns candidate legacy SQLite DB paths (from MailDiver and pre-0.5 StateDir)
// in priority order for automatic one-time migration into DirectDBPath().
func LegacyDirectDBPaths() []string {
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	if dir, err := DataDir(); err == nil {
		add(filepath.Join(dir, "maildiver.db"))
	}
	if dir, err := dataDirFor("maildiver"); err == nil {
		add(filepath.Join(dir, "maildiver.db"))
	}
	if dir, err := StateDir(); err == nil {
		add(filepath.Join(dir, "postdive.db"))
		add(filepath.Join(dir, "maildiver.db"))
	}
	if dir, err := stateDirFor("maildiver"); err == nil {
		add(filepath.Join(dir, "maildiver.db"))
	}
	return out
}

func normalizeDirect(d DirectConfig) DirectConfig {
	d.Mode = EngineDirect
	for i := range d.Accounts {
		a := &d.Accounts[i]
		a.ID = strings.TrimSpace(a.ID)
		a.Name = strings.TrimSpace(a.Name)
		a.Email = strings.TrimSpace(a.Email)
		a.DisplayName = strings.TrimSpace(a.DisplayName)
		a.Username = strings.TrimSpace(a.Username)
		a.IMAPHost = strings.TrimSpace(a.IMAPHost)
		a.SMTPHost = strings.TrimSpace(a.SMTPHost)
		if len(a.Aliases) > 0 {
			seen := map[string]bool{}
			if a.Email != "" {
				seen[strings.ToLower(a.Email)] = true
			}
			cleanAliases := make([]AccountAlias, 0, len(a.Aliases))
			for _, al := range a.Aliases {
				al.Email = strings.TrimSpace(al.Email)
				al.DisplayName = strings.TrimSpace(al.DisplayName)
				al.Signature = strings.TrimRight(al.Signature, " \t\r\n")
				if al.Email == "" || !strings.Contains(al.Email, "@") {
					continue
				}
				low := strings.ToLower(al.Email)
				if seen[low] {
					continue
				}
				seen[low] = true
				cleanAliases = append(cleanAliases, al)
			}
			a.Aliases = cleanAliases
		}
		if a.IMAPPort <= 0 || a.IMAPPort > 65535 {
			a.IMAPPort = 993
		}
		if a.SMTPPort <= 0 || a.SMTPPort > 65535 {
			a.SMTPPort = 465
		}
		if a.IMAPTLS != "starttls" && !(a.IMAPTLS == "plain" && isLoopback(a.IMAPHost)) {
			a.IMAPTLS = "tls"
		}
		if a.SMTPTLS != "starttls" && !(a.SMTPTLS == "plain" && isLoopback(a.SMTPHost)) {
			a.SMTPTLS = "tls"
		}
		if a.SyncLimit <= 0 || a.SyncLimit > MaxSyncLimit {
			a.SyncLimit = DefaultSyncLimit
		}
		if a.Name == "" {
			a.Name = a.Email
		}
	}
	for i := range d.ICalFeeds {
		f := &d.ICalFeeds[i]
		f.ID = strings.TrimSpace(f.ID)
		f.Name = strings.TrimSpace(f.Name)
		f.URL = strings.TrimSpace(f.URL)
		if f.Color == "" {
			f.Color = "#7aa2f7"
		}
	}
	return d
}

func isLoopback(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}
