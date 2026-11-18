package nativeui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/config"
)

// The app's settings: AI providers and keys, triage scans and the archive folder. What OmaThunder
// may do at all (accounts, read-only, bridge, modules) stays in the Thunderbird add-on's settings,
// so the app cannot widen its own permissions.

type keyRow struct {
	backend string
	envKey  string
	set     bool   // a key is stored (env file or environment)
	input   string // a new key; "" keeps the stored one
	remove  bool
}

type settingsUI struct {
	open      bool
	providers []string

	markRead int // 0: at once, 1: after 3 s, 2: never

	agentBackend, agentModel, agentEndpoint string

	classifyAI                        bool
	clfBackend, clfModel, clfEndpoint string
	maxCalls                          string
	allowCLI                          bool

	scanOnNewMail       bool
	scanLimit, scanDays string
	archiveDir          string

	keys []keyRow
	err  string
}

// OpenSettings fills the dialog from config.json and the key store (UI thread).
func (a *App) OpenSettings() {
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	s := settingsUI{
		open:          true,
		agentBackend:  ai.CanonicalBackend(cfg.AI.Agent.Backend),
		agentModel:    cfg.AI.Agent.Model,
		agentEndpoint: cfg.AI.Agent.Endpoint,
		classifyAI:    cfg.AI.ClassifyWithAI,
		clfBackend:    ai.CanonicalBackend(cfg.AI.Classifier.Backend),
		clfModel:      cfg.AI.Classifier.Model,
		clfEndpoint:   cfg.AI.Classifier.Endpoint,
		maxCalls:      strconv.Itoa(cfg.AI.MaxAICallsPerScan),
		allowCLI:      cfg.AI.AllowUnrestrictedCLI,
		scanOnNewMail: cfg.Mailbox.ScanOnNewMail,
		scanLimit:     strconv.Itoa(cfg.Mailbox.ScanLimit),
		scanDays:      strconv.Itoa(cfg.Mailbox.ScanDays),
		archiveDir:    cfg.Mailbox.ArchiveDir,
	}
	switch cfg.App.MarkRead {
	case config.MarkReadDelay:
		s.markRead = 1
	case config.MarkReadNever:
		s.markRead = 2
	}
	for _, p := range ai.SupportedProviders() {
		s.providers = append(s.providers, p.ID)
	}
	for _, k := range ai.APIKeyNames() {
		s.keys = append(s.keys, keyRow{backend: k.Backend, envKey: k.EnvKey, set: config.LookupAPIKey(k.EnvKey) != ""})
	}
	a.settings = s
}

func parseBounded(raw string, lo, hi int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

// SaveSettings writes config.json (only the fields of this dialog) and the key store (UI thread).
func (a *App) SaveSettings() bool {
	s := &a.settings
	s.err = ""
	maxCalls, ok1 := parseBounded(s.maxCalls, 1, 1000)
	limit, ok2 := parseBounded(s.scanLimit, 1, 200)
	days, ok3 := parseBounded(s.scanDays, 1, 365)
	if !ok1 || !ok2 || !ok3 {
		s.err = a.tr("Zahlen prüfen: KI-Aufrufe 1–1000, Mails pro Ordner 1–200, Tage 1–365.",
			"Check the numbers: AI calls 1–1000, emails per folder 1–200, days 1–365.")
		return false
	}
	updates := map[string]string{}
	for _, k := range s.keys {
		switch {
		case strings.TrimSpace(k.input) != "":
			updates[k.envKey] = strings.TrimSpace(k.input)
		case k.remove:
			updates[k.envKey] = ""
		}
	}
	if len(updates) > 0 {
		if err := config.SetEnvFileKeys(updates); err != nil {
			s.err = a.tr("API-Schlüssel nicht gespeichert: ", "API keys not saved: ") + err.Error()
			return false
		}
	}
	// Read the file again right before writing: the add-on may have saved meanwhile.
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	cfg.AI.Agent = config.ProviderConfig{Backend: s.agentBackend, Model: strings.TrimSpace(s.agentModel), Endpoint: strings.TrimSpace(s.agentEndpoint)}
	cfg.AI.Classifier = config.ProviderConfig{Backend: s.clfBackend, Model: strings.TrimSpace(s.clfModel), Endpoint: strings.TrimSpace(s.clfEndpoint)}
	cfg.AI.ClassifyWithAI = s.classifyAI
	cfg.AI.MaxAICallsPerScan = maxCalls
	cfg.AI.AllowUnrestrictedCLI = s.allowCLI
	cfg.Mailbox.ScanOnNewMail = s.scanOnNewMail
	cfg.Mailbox.ScanLimit = limit
	cfg.Mailbox.ScanDays = days
	cfg.Mailbox.ArchiveDir = strings.TrimSpace(s.archiveDir)
	cfg.App.MarkRead = [...]string{config.MarkReadOnOpen, config.MarkReadDelay, config.MarkReadNever}[min(max(s.markRead, 0), 2)]
	cfg = config.Normalize(cfg)
	if err := a.opts.ConfigSaver(cfg); err != nil {
		s.err = a.tr("Nicht gespeichert: ", "Not saved: ") + err.Error()
		return false
	}
	a.applyConfig(cfg)
	s.open = false
	a.toast(a.tr("Einstellungen gespeichert", "Settings saved"), "success")
	return true
}

func (a *App) settingsSection(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(13).Bold().Margin(8, 0, 0, 0)
}

func (a *App) settingsLabel(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(12).Width(150)
}

func (a *App) renderSettingsModal(c *ui.Context) {
	t := c.Theme()
	s := &a.settings
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(640).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(8).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Einstellungen", "Settings")).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("set-x"), "✕").Clicked() {
					s.open = false
				}
			})
			ui.Scroll(c.Key("set-scroll")).FillWidth().MaxHeight(560).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Padding(0, 6, 0, 0).Children(func() {
					a.renderSettingsBody(c)
				})
			})
			if s.err != "" {
				ui.Text(c, s.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("set-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
					s.open = false
				}
				if ui.PrimaryButton(c.Key("set-save"), a.tr("Speichern", "Save")).Clicked() {
					a.SaveSettings()
				}
			})
		})
	})
}

func (a *App) renderSettingsBody(c *ui.Context) {
	t := c.Theme()
	s := &a.settings
	field := func(key, label string, body func()) {
		ui.Row(c.Key(key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			a.settingsLabel(c, label)
			body()
		})
	}
	note := func(text string, color ui.Color) {
		ui.Text(c, text).FontSize(11.5).TextColor(color)
	}

	a.settingsSection(c, a.tr("Lesen", "Reading"))
	field("set-read", a.tr("Als gelesen markieren", "Mark as read"), func() {
		ui.Segmented(c.Key("set-read-s"), &s.markRead, a.tr("Beim Öffnen", "On open"), a.tr("Nach 3 Sekunden", "After 3 seconds"), a.tr("Nie", "Never"))
	})
	if a.readOnly {
		note(a.tr("„Nur lesen“ ist in den Einstellungen des Thunderbird-Add-ons an: Mails bleiben ungelesen, bis du sie in Thunderbird liest.", "Read-only is on in the Thunderbird add-on's settings: mails stay unread until you read them in Thunderbird."), t.Warning)
	}

	a.settingsSection(c, a.tr("KI-Assistent", "AI assistant"))
	if !a.cfg.Features.Assistant {
		note(a.tr("Der KI-Assistent ist in den Einstellungen des Thunderbird-Add-ons ausgeschaltet.", "The AI assistant is switched off in the Thunderbird add-on's settings."), t.Warning)
	}
	field("set-ag-b", a.tr("Anbieter", "Provider"), func() { ui.Select(c.Key("set-ag-bs"), &s.agentBackend, s.providers).Grow(1) })
	field("set-ag-m", a.tr("Modell", "Model"), func() {
		ui.TextInput(c.Key("set-ag-mi"), &s.agentModel).Placeholder(a.tr("leer = Standard des Anbieters", "empty = provider default")).Grow(1)
	})
	field("set-ag-e", "Endpoint", func() {
		ui.TextInput(c.Key("set-ag-ei"), &s.agentEndpoint).Placeholder(a.tr("leer = offizielle Adresse", "empty = official URL")).Grow(1)
	})

	a.settingsSection(c, a.tr("Triage-Scan", "Triage scan"))
	if !a.cfg.Features.Triage {
		note(a.tr("Triage-Scans sind in den Einstellungen des Thunderbird-Add-ons ausgeschaltet.", "Triage scans are switched off in the Thunderbird add-on's settings."), t.Warning)
	}
	ui.Checkbox(c.Key("set-new"), &s.scanOnNewMail, a.tr("Bei neuer Mail automatisch scannen", "Scan automatically when new mail arrives"))
	ui.Checkbox(c.Key("set-clf"), &s.classifyAI, a.tr("Mails per KI einordnen (sonst lokale Schätzung, 0 Tokens)", "Sort mails with AI (else a local estimate, 0 tokens)"))
	if s.classifyAI {
		field("set-clf-b", a.tr("Anbieter", "Provider"), func() { ui.Select(c.Key("set-clf-bs"), &s.clfBackend, s.providers).Grow(1) })
		field("set-clf-m", a.tr("Modell", "Model"), func() {
			ui.TextInput(c.Key("set-clf-mi"), &s.clfModel).Placeholder(a.tr("leer = günstiges Standardmodell", "empty = cheap default model")).Grow(1)
		})
		field("set-clf-e", "Endpoint", func() {
			ui.TextInput(c.Key("set-clf-ei"), &s.clfEndpoint).Placeholder(a.tr("leer = offizielle Adresse", "empty = official URL")).Grow(1)
		})
		field("set-max", a.tr("KI-Aufrufe pro Scan", "AI calls per scan"), func() { ui.TextInput(c.Key("set-max-i"), &s.maxCalls).Width(90) })
		note(a.tr("Verschlüsselte Mails gehen nur mit Absender und Betreff an die KI, nie mit Inhalt.", "Encrypted emails only reach the AI with sender and subject, never with content."), t.TextMuted)
	}
	field("set-lim", a.tr("Gelesene Mails je Posteingang", "Read emails per inbox"), func() { ui.TextInput(c.Key("set-lim-i"), &s.scanLimit).Width(90) })
	field("set-days", a.tr("Zeitraum (Tage)", "Period (days)"), func() { ui.TextInput(c.Key("set-days-i"), &s.scanDays).Width(90) })
	ui.Checkbox(c.Key("set-cli"), &s.allowCLI, a.tr("Unbeschränkte CLIs erlauben (gemini-cli, opencode-cli)", "Allow unrestricted CLIs (gemini-cli, opencode-cli)"))
	if s.allowCLI {
		note(a.tr("Diese CLIs laufen ohne Werkzeugsperre: Ein präparierter Mailtext könnte sie Werkzeuge benutzen lassen.", "These CLIs run without a tool lock: a crafted email could make them use tools."), t.Warning)
	}

	a.settingsSection(c, a.tr("Archiv", "Archive"))
	field("set-arch", a.tr("Archiv-Ordner", "Archive folder"), func() { ui.TextInput(c.Key("set-arch-i"), &s.archiveDir).Grow(1) })

	a.settingsSection(c, a.tr("API-Schlüssel", "API keys"))
	note(a.tr("Gespeichert in ", "Stored in ")+config.EnvFilePath()+a.tr(" (nur für dich lesbar). Neue Schlüssel gelten sofort.", " (readable only by you). New keys apply at once."), t.TextMuted)
	for i := range s.keys {
		k := &s.keys[i]
		ui.Row(c.Key(fmt.Sprintf("set-key-%d", i))).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, k.backend).FontSize(12).Width(150)
			ph := a.tr("nicht gesetzt", "not set")
			if k.set && !k.remove {
				ph = a.tr("gesetzt – leer lassen zum Behalten", "set – leave empty to keep")
			}
			if k.remove {
				ph = a.tr("wird entfernt", "will be removed")
			}
			ui.TextInput(c.Key(fmt.Sprintf("set-key-i-%d", i)), &k.input).Password().Placeholder(ph).Grow(1)
			if k.set {
				ui.Checkbox(c.Key(fmt.Sprintf("set-key-rm-%d", i)), &k.remove, a.tr("Entfernen", "Remove"))
			}
		})
	}
}
