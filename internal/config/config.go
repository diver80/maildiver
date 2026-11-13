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
	Backend  string `json:"backend"` // "gemini-api", "gemini-cli", "claude-cli", "claude-api", "opencode-cli", "ollama"
	Model    string `json:"model"`
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
}

// RoutingRule maps sender domains to a customer label and Thunderbird folder (with optional flexible subfolders).
type RoutingRule struct {
	ID            string            `json:"id"`
	Customer      string            `json:"customer"`
	AccountID     string            `json:"accountId,omitempty"`     // Thunderbird account ID for multi-account disambiguation & auto-create
	FolderPath    string            `json:"folderPath"`              // e.g. "Kunden/Acme" or "Kunden/Acme/Incoming"
	FolderID      string            `json:"folderId,omitempty"`      // Thunderbird folder ID if bound from UI
	Domains       []string          `json:"domains"`                 // e.g. ["acme.de", "acme-corp.com"]
	AutoMove      bool              `json:"autoMove"`                // Stage-0 deterministic move from INBOX -> FolderPath
	IncludeInScan bool              `json:"includeInScan"`           // Scan this folder + subfolders for the Kanban board
	ColumnFolders map[string]string `json:"columnFolders,omitempty"` // Optional: action/waiting/done -> subfolderPath
}

// FeatureFlags switches the three independent OmaThunder building blocks on or off.
type FeatureFlags struct {
	Theme     bool `json:"theme"`     // Omarchy theme sync (chrome, message body, compose)
	Assistant bool `json:"assistant"` // AI writing assistant (summarize, draft reply, review draft)
	Triage    bool `json:"triage"`    // Scan, classification, Kanban, customer routing & auto-move
}

// MailboxPolicy limits what OmaThunder may read and change in Thunderbird.
type MailboxPolicy struct {
	// ReadOnly blocks every change to mails, folders and tags. Scans only report planned moves.
	ReadOnly bool `json:"readOnly"`
	// TriageAccountIDs lists the Thunderbird accounts that triage may scan and change. Empty = none.
	TriageAccountIDs []string `json:"triageAccountIds"`
	// AutoMoveOnlyUnread restricts deterministic auto-moves to unread inbox mails.
	AutoMoveOnlyUnread bool `json:"autoMoveOnlyUnread"`
	// ScanOnNewMail runs a triage scan automatically when new mail arrives (same limits as a manual scan).
	ScanOnNewMail bool `json:"scanOnNewMail"`
	// ScanLimit is the number of newest mails scanned per folder.
	ScanLimit int `json:"scanLimit"`
	// ScanDays limits scans to mails received within the last N days.
	ScanDays int `json:"scanDays"`
}

// AccountEnabled reports whether triage may scan and change mails of the given account.
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

type Config struct {
	Language string        `json:"language,omitempty"` // "auto", "de", "en"
	Features FeatureFlags  `json:"features"`
	Mailbox  MailboxPolicy `json:"mailbox"`
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
			AutoMoveOnlyUnread: true,
			ScanLimit:          defaultScanLimit,
			ScanDays:           defaultScanDays,
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
	if cfg.Rules == nil {
		cfg.Rules = []RoutingRule{}
	}
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
