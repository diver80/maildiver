package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const (
	AppID          = "omathunder"
	ExtensionID    = "omathunder@omathunder.dev"
	NativeHostName = "dev.omathunder.host"
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

// RoutingRule maps sender domains (and optional sender/subject filters) to a Space label and Thunderbird folder.
type RoutingRule struct {
	ID                string            `json:"id"`
	Space             string            `json:"space"`
	AccountID         string            `json:"accountId,omitempty"` // Thunderbird account ID for multi-account disambiguation & auto-create
	FolderPath        string            `json:"folderPath"`          // e.g. "Spaces/Acme" or "Rechnungen"
	FolderID          string            `json:"folderId,omitempty"`  // Thunderbird folder ID if bound from UI
	Domains           []string          `json:"domains"`             // e.g. ["acme.de", "acme-corp.com"]
	FromContains      []string          `json:"fromContains,omitempty"`
	SubjectContains   []string          `json:"subjectContains,omitempty"`
	NativeFilterNames []string          `json:"nativeFilterNames,omitempty"` // Linked native Thunderbird filter names
	AutoMove          bool              `json:"autoMove"`                    // Stage-0 deterministic move from INBOX -> FolderPath
	IncludeInScan     bool              `json:"includeInScan"`               // Scan this folder + subfolders for the Kanban board
	ColumnFolders     map[string]string `json:"columnFolders,omitempty"`     // Optional: action/waiting/done -> subfolderPath
}

// FeatureFlags switches the three independent OmaThunder building blocks on or off.
type FeatureFlags struct {
	Theme     bool `json:"theme"`     // Omarchy theme sync (chrome, message body, compose)
	Assistant bool `json:"assistant"` // AI writing assistant (summarize, draft reply, review draft)
	Triage    bool `json:"triage"`    // Scan, classification, Kanban, Space routing & auto-move
}

// MailboxPolicy limits what OmaThunder may read and change in Thunderbird.
type MailboxPolicy struct {
	// ReadOnly blocks every change to mails, folders and tags. Scans only report planned moves.
	ReadOnly bool `json:"readOnly"`
	// TriageAccountIDs lists the Thunderbird accounts enabled for Spaces, rules, cleanup and triage. Empty = none.
	TriageAccountIDs []string `json:"triageAccountIds"`
	// KanbanAccountIDs lists the subset of enabled accounts scanned and displayed on the Kanban board.
	KanbanAccountIDs []string `json:"kanbanAccountIds"`
	// KanbanFolders maps accountID -> enabled level-1 folder paths (each scanned recursively except INBOX itself).
	// When an account is absent from this map, only its INBOX is scanned (legacy default).
	KanbanFolders map[string][]string `json:"kanbanFolders,omitempty"`
	// AutoMoveOnlyUnread restricts deterministic auto-moves to unread inbox mails.
	AutoMoveOnlyUnread bool `json:"autoMoveOnlyUnread"`
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
		return "OmaThunder-Archiv"
	}
	return filepath.Join(home, "OmaThunder-Archiv")
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

// KanbanEnabled reports whether the given account is enabled for the Kanban board and scans.
func (p MailboxPolicy) KanbanEnabled(accountID string) bool {
	if !p.AccountEnabled(accountID) {
		return false
	}
	for _, id := range p.KanbanAccountIDs {
		if id == accountID {
			return true
		}
	}
	return false
}

// HasKanbanFolders reports whether per-account folder selection is configured for accountID.
func (p MailboxPolicy) HasKanbanFolders(accountID string) bool {
	if p.KanbanFolders == nil {
		return false
	}
	_, ok := p.KanbanFolders[accountID]
	return ok
}

// CanWrite reports whether OmaThunder may change mails, folders or tags in the given account.
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

// BridgeConfig controls the local authenticated REST + SSE backend bridge for external UIs
// (the native OmaThunder app, scripts). It is off by default: the host only opens the localhost
// port after the user enabled it (app button, `omathunder bridge enable` or the settings page).
type BridgeConfig struct {
	Enabled   bool `json:"enabled"`
	Port      int  `json:"port,omitempty"`
	AllowSend bool `json:"allowSend,omitempty"`
	// AccountIDs optionally limits which Thunderbird accounts bridge clients may read.
	// Empty = all accounts are readable. Writes additionally always require
	// mailbox.readOnly=false and an account listed in mailbox.triageAccountIds.
	AccountIDs []string `json:"accountIds,omitempty"`
}

type Config struct {
	Language string        `json:"language,omitempty"` // "auto", "de", "en"
	Features FeatureFlags  `json:"features"`
	Mailbox  MailboxPolicy `json:"mailbox"`
	Bridge   BridgeConfig  `json:"bridge"`
	Theme    ThemeConfig   `json:"theme"`
	AI       AIConfig      `json:"ai"`
	Rules    []RoutingRule `json:"rules"`
}

func Default() Config {
	return Config{
		Language: "auto",
		// Safe defaults: theme + writing assistant on, triage off until explicitly enabled.
		Features: FeatureFlags{Theme: true, Assistant: true, Triage: false},
		Mailbox: MailboxPolicy{
			ReadOnly:           true,
			TriageAccountIDs:   []string{},
			KanbanAccountIDs:   []string{},
			AutoMoveOnlyUnread: true,
			ScanOnNewMail:      true,
			ScanLimit:          defaultScanLimit,
			ScanDays:           defaultScanDays,
			ArchiveDir:         DefaultArchiveDir(),
		},
		Bridge: BridgeConfig{
			Enabled:   false,
			Port:      0,
			AllowSend: false,
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
		Rules: []RoutingRule{},
	}
}

func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, AppID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", AppID), nil
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, AppID), nil
		}
	}
	return filepath.Join(home, ".config", AppID), nil
}

func StateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, AppID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", AppID), nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, AppID), nil
		}
	}
	return filepath.Join(home, ".local", "state", AppID), nil
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// BridgeInfoPath returns the runtime discovery file path for the local REST/SSE bridge.
func BridgeInfoPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bridge.json"), nil
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
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	return Normalize(cfg), nil
}

// Normalize fills empty or out-of-range values with safe defaults.
func Normalize(cfg Config) Config {
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
	if cfg.Bridge.Port < 0 || cfg.Bridge.Port > 65535 {
		cfg.Bridge.Port = 0
	}
	if cfg.Rules == nil {
		cfg.Rules = []RoutingRule{}
	}
	if cfg.Mailbox.TriageAccountIDs == nil {
		cfg.Mailbox.TriageAccountIDs = []string{}
	}
	if cfg.Mailbox.KanbanAccountIDs == nil {
		cfg.Mailbox.KanbanAccountIDs = []string{}
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
	return cfg
}

// ApplyPatch merges a partial JSON config onto base. Top-level and nested fields that are
// absent from the patch keep their current values; arrays present in the patch replace the old ones.
func ApplyPatch(base Config, patch []byte) (Config, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil {
		return base, err
	}
	// encoding/json decodes into existing slice elements, so stale fields of old rules
	// could survive. Reset slices the patch replaces before decoding.
	if _, ok := keys["rules"]; ok {
		base.Rules = nil
	}
	if raw, ok := keys["mailbox"]; ok {
		var mb map[string]json.RawMessage
		if err := json.Unmarshal(raw, &mb); err == nil {
			if _, ok := mb["triageAccountIds"]; ok {
				base.Mailbox.TriageAccountIDs = nil
			}
			if _, ok := mb["kanbanAccountIds"]; ok {
				base.Mailbox.KanbanAccountIDs = nil
			}
			if _, ok := mb["kanbanFolders"]; ok {
				base.Mailbox.KanbanFolders = nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
