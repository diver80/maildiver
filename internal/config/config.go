package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	AppID       = "maildiver"
	LegacyAppID = "omathunder"
)

// LinkPreviewRule defines when an email should auto-load a matching link in the embedded browser preview panel.
type LinkPreviewRule struct {
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	SenderPattern string `json:"senderPattern,omitempty"` // case-insensitive substring on From
	URLPattern    string `json:"urlPattern"`              // case-insensitive substring on link URL
}

// LinkPreviewConfig configures the embedded native WebView split-panel in the mail reader.
type LinkPreviewConfig struct {
	Enabled  bool              `json:"enabled"`          // auto-open preview split when a rule matches
	Layout   string            `json:"layout,omitempty"` // "side" (default) or "bottom"
	AutoLoad bool              `json:"autoLoad"`         // automatically load the matched link when selecting an email
	Rules    []LinkPreviewRule `json:"rules"`
}

// DefaultLinkPreviewRules returns curated starter rules (Atlassian Jira/Confluence active by default).
func DefaultLinkPreviewRules() []LinkPreviewRule {
	return []LinkPreviewRule{
		{Name: "Atlassian Jira", Enabled: true, SenderPattern: "atlassian", URLPattern: "atlassian.net/browse/"},
		{Name: "Atlassian Confluence", Enabled: true, SenderPattern: "atlassian", URLPattern: "atlassian.net/wiki/"},
		{Name: "Atlassian (Alle Links)", Enabled: true, SenderPattern: "atlassian", URLPattern: "atlassian.net/"},
		{Name: "GitHub Issues / PRs", Enabled: false, SenderPattern: "github.com", URLPattern: "github.com/"},
		{Name: "GitLab Issues / MRs", Enabled: false, SenderPattern: "gitlab", URLPattern: "/-/"},
		{Name: "Linear Issues", Enabled: false, SenderPattern: "linear.app", URLPattern: "linear.app/"},
	}
}

// AppConfig holds preferences of the MailDiver app.
type AppConfig struct {
	// MarkRead: when the app marks an opened unread mail as read – "" (at once),
	// "delay" (after a few seconds) or "never".
	MarkRead    string            `json:"markRead,omitempty"`
	LinkPreview LinkPreviewConfig `json:"linkPreview,omitempty"`
	// ExpandedFolders optionally maps accountID -> folder paths that start expanded in the
	// sidebar (all other expandable folders in that account start collapsed).
	// Absent accountID uses the built-in default (INBOX expanded to level 2; level 2+ & Archives collapsed).
	ExpandedFolders map[string][]string `json:"expandedFolders,omitempty"`
	// HiddenCalendars stores calendar IDs unchecked in the multi-select calendar filter.
	HiddenCalendars []string `json:"hiddenCalendars,omitempty"`
	// AllowedImageSenders stores lowercase sender emails whose remote images load automatically.
	AllowedImageSenders []string `json:"allowedImageSenders,omitempty"`
}

// Values of AppConfig.MarkRead.
const (
	MarkReadOnOpen = ""
	MarkReadDelay  = "delay"
	MarkReadNever  = "never"
)

type ThemeConfig struct {
	Mode          string `json:"mode"`   // "auto", "omarchy", "preset"
	Preset        string `json:"preset"` // e.g. "tokyo-night", "catppuccin", "flexoki-light"
	LightFallback string `json:"lightFallback"`
	DarkFallback  string `json:"darkFallback"`
	StyleBody     bool   `json:"styleBody"` // also recolor message bodies and the compose editor
}

type ProviderConfig struct {
	// Backend is a canonical backend name (see BackendNames); Normalize resolves aliases such as
	// "anthropic" -> "claude-api" and "gemini" -> "gemini-api".
	Backend string `json:"backend"`
	Model   string `json:"model"`
	// Endpoint overrides the backend's official base URL. It is only ever read from config.json:
	// the backend's API key is sent to it.
	Endpoint string `json:"endpoint,omitempty"`
}

type AIConfig struct {
	Classifier      ProviderConfig `json:"classifier"`
	Agent           ProviderConfig `json:"agent"`
	SkipNewsletters bool           `json:"skipNewsletters"`
	MaxSnippetChars int            `json:"maxSnippetChars"`
	// Language is the UI language for generated texts ("de" or "en"); set per request, never stored.
	Language string `json:"-"`
	// ClassifyWithAI sends mails that pass Stage 0 to the classifier model. Off = local estimate only, 0 tokens.
	ClassifyWithAI bool `json:"classifyWithAi"`
	// MaxAICallsPerScan caps classifier calls per scan; mails beyond it get the local estimate
	// and are picked up by a later scan.
	MaxAICallsPerScan int `json:"maxAiCallsPerScan"`
	// AllowUnrestrictedCLI permits the gemini-cli and opencode-cli backends. They cannot be started
	// with tools disabled, so a prompt injection in a mail could trigger tool use. Off by default.
	AllowUnrestrictedCLI bool `json:"allowUnrestrictedCli"`
	// Endpoints optionally sets a base URL per backend (canonical name -> URL) for backends picked
	// ad hoc (e.g. in the native app) that are neither the agent nor the classifier. Empty = official URL.
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

// FeatureFlags switches the three independent MailDiver building blocks on or off.
type FeatureFlags struct {
	Theme     bool `json:"theme"`     // Omarchy theme sync (chrome, message body, compose)
	Assistant bool `json:"assistant"` // AI writing assistant (summarize, draft reply, review draft)
	Triage    bool `json:"triage"`    // Triage scans: classification of the triage inboxes (app: action view)
}

// MailboxPolicy limits what MailDiver may read and change in connected mailboxes.
type MailboxPolicy struct {
	// ReadOnly blocks every change to mails, folders and tags. Scans only report planned moves.
	ReadOnly bool `json:"readOnly"`
	// TriageAccountIDs lists the accounts MailDiver may change (mail actions, filters)
	// and whose inboxes it scans and archives. Empty = none.
	TriageAccountIDs []string `json:"triageAccountIds"`
	// ScanOnNewMail runs a triage scan automatically when new mail arrives (same limits as a manual scan).
	ScanOnNewMail bool `json:"scanOnNewMail"`
	// ScanLimit is the number of newest mails scanned per folder.
	ScanLimit int `json:"scanLimit"`
	// ScanDays limits scans to mails received within the last N days.
	ScanDays int `json:"scanDays"`
	// ArchiveDir is the local filesystem root for verified .eml backups.
	ArchiveDir string `json:"archiveDir,omitempty"`
}

// DefaultArchiveDir returns the default local folder for verified .eml backups.
func DefaultArchiveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "MailDiver-Archiv"
	}
	return filepath.Join(home, "MailDiver-Archiv")
}

// AccountEnabled reports whether triage/spaces/cleanup may access the given account.
func (p MailboxPolicy) AccountEnabled(accountID string) bool {
	if accountID == "" {
		return false
	}
	for _, id := range p.TriageAccountIDs {
		if id == accountID {
			return true
		}
	}
	return false
}

// CanWrite reports whether MailDiver may change mails, folders or tags in the given account.
func (p MailboxPolicy) CanWrite(accountID string) bool {
	return !p.ReadOnly && p.AccountEnabled(accountID)
}

const (
	defaultMaxAICalls = 50
	maxMaxAICalls     = 1000
	defaultScanLimit  = 20
	maxScanLimit      = 200
	defaultScanDays   = 30
	maxScanDays       = 365
)

type Config struct {
	Language string        `json:"language,omitempty"` // "auto", "de", "en"
	Features FeatureFlags  `json:"features"`
	Mailbox  MailboxPolicy `json:"mailbox"`
	Direct   DirectConfig  `json:"direct,omitempty"`
	Theme    ThemeConfig   `json:"theme"`
	AI       AIConfig      `json:"ai"`
	App      AppConfig     `json:"app"`
}

func Default() Config {
	return Config{
		Language: "auto",
		// Safe defaults: theme + writing assistant on, triage off until explicitly enabled.
		Features: FeatureFlags{Theme: true, Assistant: true, Triage: false},
		Mailbox: MailboxPolicy{
			ReadOnly:         true,
			TriageAccountIDs: []string{},
			ScanOnNewMail:    true,
			ScanLimit:        defaultScanLimit,
			ScanDays:         defaultScanDays,
			ArchiveDir:       DefaultArchiveDir(),
		},
		Direct: DirectConfig{
			Mode: EngineDirect,
		},
		Theme: ThemeConfig{
			Mode:          "auto",
			Preset:        "tokyo-night",
			LightFallback: "flexoki-light",
			DarkFallback:  "tokyo-night",
			// Recoloring mail bodies can make HTML mails with fixed text colors hard to read: opt-in.
			StyleBody: false,
		},
		AI: AIConfig{
			Classifier: ProviderConfig{
				Backend: "gemini-api",
				Model:   "gemini-4-hydrogen",
			},
			Agent: ProviderConfig{
				Backend: "claude-cli",
				Model:   "",
			},
			// Scans cost no tokens until the user switches AI classification on.
			ClassifyWithAI:    false,
			MaxAICallsPerScan: defaultMaxAICalls,
			SkipNewsletters:   true,
			MaxSnippetChars:   1600,
		},
		App: AppConfig{
			// Auto web preview is opt-in: it adds a whole extra column to the reader,
			// so it stays off until the user flips the toolbar/menu switch.
			LinkPreview: LinkPreviewConfig{
				Enabled:  false,
				Layout:   "side",
				AutoLoad: true,
				Rules:    DefaultLinkPreviewRules(),
			},
		},
	}
}

func configDirFor(appID string) (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, appID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", appID), nil
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, appID), nil
		}
	}
	return filepath.Join(home, ".config", appID), nil
}

func stateDirFor(appID string) (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, appID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", appID), nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, appID), nil
		}
	}
	return filepath.Join(home, ".local", "state", appID), nil
}

func dataDirFor(appID string) (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", appID), nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, appID), nil
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, appID), nil
		}
	}
	return filepath.Join(home, ".local", "share", appID), nil
}

func ConfigDir() (string, error)       { return configDirFor(AppID) }
func LegacyConfigDir() (string, error) { return configDirFor(LegacyAppID) }
func DataDir() (string, error)         { return dataDirFor(AppID) }
func StateDir() (string, error)        { return stateDirFor(AppID) }
func LegacyStateDir() (string, error)  { return stateDirFor(LegacyAppID) }

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (Config, error) {
	cfg := Default()
	path, err := ConfigPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if legacyDir, lerr := LegacyConfigDir(); lerr == nil {
				if ldata, rerr := os.ReadFile(filepath.Join(legacyDir, "config.json")); rerr == nil {
					// Reset slice defaults before Unmarshal so encoding/json allocates fresh elements
					// instead of mutating DefaultLinkPreviewRules() entries in-place.
					cfg.App.LinkPreview.Rules = nil
					if uerr := json.Unmarshal(ldata, &cfg); uerr != nil {
						return Default(), uerr
					}
					return Normalize(cfg), nil
				}
			}
			return cfg, nil
		}
		return cfg, err
	}
	cfg.App.LinkPreview.Rules = nil
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	return Normalize(cfg), nil
}

// Normalize fills empty or out-of-range values with safe defaults.
func Normalize(cfg Config) Config {
	switch cfg.App.MarkRead {
	case MarkReadOnOpen, MarkReadDelay, MarkReadNever:
	default:
		cfg.App.MarkRead = MarkReadOnOpen
	}
	if cfg.App.LinkPreview.Layout != "bottom" {
		cfg.App.LinkPreview.Layout = "side"
	}
	if len(cfg.App.ExpandedFolders) > 0 {
		clean := make(map[string][]string, len(cfg.App.ExpandedFolders))
		for accID, paths := range cfg.App.ExpandedFolders {
			accID = strings.TrimSpace(accID)
			if accID == "" {
				continue
			}
			seen := make(map[string]bool, len(paths))
			out := make([]string, 0, len(paths))
			for _, p := range paths {
				p = strings.Trim(strings.TrimSpace(p), "/")
				if p == "" {
					continue
				}
				low := strings.ToLower(p)
				if !seen[low] {
					seen[low] = true
					out = append(out, p)
				}
			}
			clean[accID] = out
		}
		cfg.App.ExpandedFolders = clean
	}
	if len(cfg.App.HiddenCalendars) > 0 {
		seen := make(map[string]bool, len(cfg.App.HiddenCalendars))
		out := make([]string, 0, len(cfg.App.HiddenCalendars))
		for _, id := range cfg.App.HiddenCalendars {
			id = strings.TrimSpace(id)
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		cfg.App.HiddenCalendars = out
	}
	if len(cfg.App.AllowedImageSenders) > 0 {
		seen := make(map[string]bool, len(cfg.App.AllowedImageSenders))
		out := make([]string, 0, len(cfg.App.AllowedImageSenders))
		for _, s := range cfg.App.AllowedImageSenders {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
		sort.Strings(out)
		cfg.App.AllowedImageSenders = out
	}
	if cfg.App.LinkPreview.Rules == nil {
		cfg.App.LinkPreview.Rules = DefaultLinkPreviewRules()
	}
	if cfg.AI.MaxSnippetChars <= 0 {
		cfg.AI.MaxSnippetChars = 1600
	}
	if cfg.AI.MaxAICallsPerScan <= 0 {
		cfg.AI.MaxAICallsPerScan = defaultMaxAICalls
	}
	if cfg.AI.MaxAICallsPerScan > maxMaxAICalls {
		cfg.AI.MaxAICallsPerScan = maxMaxAICalls
	}
	cfg.AI = normalizeAI(cfg.AI)
	if cfg.Mailbox.TriageAccountIDs == nil {
		cfg.Mailbox.TriageAccountIDs = []string{}
	}
	if cfg.Mailbox.ScanLimit <= 0 {
		cfg.Mailbox.ScanLimit = defaultScanLimit
	}
	if cfg.Mailbox.ScanLimit > maxScanLimit {
		cfg.Mailbox.ScanLimit = maxScanLimit
	}
	if cfg.Mailbox.ScanDays <= 0 {
		cfg.Mailbox.ScanDays = defaultScanDays
	}
	if cfg.Mailbox.ScanDays > maxScanDays {
		cfg.Mailbox.ScanDays = maxScanDays
	}
	if cfg.Mailbox.ArchiveDir == "" {
		cfg.Mailbox.ArchiveDir = DefaultArchiveDir()
	}
	cfg.Direct = normalizeDirect(cfg.Direct)
	return cfg
}

// ApplyPatch merges a partial JSON config onto base. Top-level and nested fields that are
// absent from the patch keep their current values; arrays present in the patch replace the old ones.
func ApplyPatch(base Config, patch []byte) (Config, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil {
		return base, err
	}
	// encoding/json decodes into existing slice elements, so stale entries could survive.
	// Reset slices the patch replaces before decoding.
	if raw, ok := keys["app"]; ok {
		var ak map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ak); err == nil {
			if lpRaw, ok := ak["linkPreview"]; ok {
				var lp map[string]json.RawMessage
				if err := json.Unmarshal(lpRaw, &lp); err == nil {
					if _, ok := lp["rules"]; ok {
						base.App.LinkPreview.Rules = nil
					}
				}
			}
		}
	}
	if raw, ok := keys["mailbox"]; ok {
		var mb map[string]json.RawMessage
		if err := json.Unmarshal(raw, &mb); err == nil {
			if _, ok := mb["triageAccountIds"]; ok {
				base.Mailbox.TriageAccountIDs = nil
			}
		}
	}
	if raw, ok := keys["direct"]; ok {
		var dk map[string]json.RawMessage
		if err := json.Unmarshal(raw, &dk); err == nil {
			if _, ok := dk["accounts"]; ok {
				base.Direct.Accounts = nil
			}
			if _, ok := dk["icalFeeds"]; ok {
				base.Direct.ICalFeeds = nil
			}
		}
	}
	if raw, ok := keys["ai"]; ok {
		var aiKeys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &aiKeys); err == nil {
			// A patched endpoints map replaces the old one (and never mutates the caller's map).
			if _, ok := aiKeys["endpoints"]; ok {
				base.AI.Endpoints = nil
			}
		}
	}
	if err := json.Unmarshal(patch, &base); err != nil {
		return base, err
	}
	return Normalize(base), nil
}

func Save(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
