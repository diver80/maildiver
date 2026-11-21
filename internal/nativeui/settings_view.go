package nativeui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/lansync"
)

// The app's settings: reading behavior, embedded Web Preview auto-link rules, AI providers and keys,
// triage scans, and the archive folder.

type keyRow struct {
	backend string
	envKey  string
	set     bool   // a key is stored (env file or environment)
	input   string // a new key; "" keeps the stored one
	remove  bool
}

type accountFolderPref struct {
	custom   bool
	expanded map[string]bool // clean folder path (exact case from folder list) -> start expanded
}

type settingsUI struct {
	open      bool
	providers []string

	markRead int // 0: at once, 1: after 3 s, 2: never

	// Per-mailbox startup folder expansion preferences
	folderAccLabels []string
	folderAccByLbl  map[string]string
	folderAccLabel  string
	folderPrefs     map[string]*accountFolderPref

	linkPreviewEnabled  bool
	linkPreviewAutoLoad bool
	linkPreviewLayout   int // 0: side, 1: bottom
	linkPreviewRules    []config.LinkPreviewRule
	newRuleName         string
	newRuleSender       string
	newRuleURL          string
	allowedImageSenders []string
	newImageSender      string

	agentBackend, agentModel, agentEndpoint string
	agentModels                             []string
	agentModelsKey                          string
	agentModelsLoading                      bool
	agentModelCustom                        bool

	classifyAI                        bool
	clfBackend, clfModel, clfEndpoint string
	clfModels                         []string
	clfModelsKey                      string
	clfModelsLoading                  bool
	clfModelCustom                    bool
	maxCalls                          string
	allowCLI                          bool

	scanOnNewMail       bool
	scanLimit, scanDays string
	archiveDir          string

	keys []keyRow
	err  string

	// Encrypted LAN QR-code device transfer (Phase 3)
	lanSession   *lansync.Session
	lanQRImg     ui.ImageSource
	lanImportURI string
	lanBusy      bool // a transfer or scan is running
	lanMsg       string
	lanErr       bool
}

// OpenSettings fills the dialog from config.json and the key store (UI thread).
func (a *App) OpenSettings() {
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	lpRules := make([]config.LinkPreviewRule, len(cfg.App.LinkPreview.Rules))
	copy(lpRules, cfg.App.LinkPreview.Rules)
	lpLayout := 0
	if cfg.App.LinkPreview.Layout == "bottom" {
		lpLayout = 1
	}
	s := settingsUI{
		open:                true,
		linkPreviewEnabled:  cfg.App.LinkPreview.Enabled,
		linkPreviewAutoLoad: cfg.App.LinkPreview.AutoLoad,
		linkPreviewLayout:   lpLayout,
		linkPreviewRules:    lpRules,
		allowedImageSenders: append([]string(nil), cfg.App.AllowedImageSenders...),
		agentBackend:        ai.CanonicalBackend(cfg.AI.Agent.Backend),
		agentModel:          cfg.AI.Agent.Model,
		agentEndpoint:       cfg.AI.Agent.Endpoint,
		classifyAI:          cfg.AI.ClassifyWithAI,
		clfBackend:          ai.CanonicalBackend(cfg.AI.Classifier.Backend),
		clfModel:            cfg.AI.Classifier.Model,
		clfEndpoint:         cfg.AI.Classifier.Endpoint,
		maxCalls:            strconv.Itoa(cfg.AI.MaxAICallsPerScan),
		allowCLI:            cfg.AI.AllowUnrestrictedCLI,
		scanOnNewMail:       cfg.Mailbox.ScanOnNewMail,
		scanLimit:           strconv.Itoa(cfg.Mailbox.ScanLimit),
		scanDays:            strconv.Itoa(cfg.Mailbox.ScanDays),
		archiveDir:          cfg.Mailbox.ArchiveDir,
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

	// Populate per-mailbox startup folder preferences from config.json + known folders.
	s.folderPrefs = make(map[string]*accountFolderPref)
	s.folderAccByLbl = make(map[string]string)
	seenAcc := make(map[string]bool)
	addAcc := func(accID, name string) {
		if accID == "" || seenAcc[accID] {
			return
		}
		seenAcc[accID] = true
		lbl := strings.TrimSpace(name)
		if lbl == "" {
			lbl = a.accountDisplayName(accID)
		}
		if lbl == "" {
			lbl = accID
		}
		if _, dup := s.folderAccByLbl[lbl]; dup {
			lbl = lbl + " (" + accID + ")"
		}
		s.folderAccLabels = append(s.folderAccLabels, lbl)
		s.folderAccByLbl[lbl] = accID

		pref := &accountFolderPref{expanded: make(map[string]bool)}
		if paths, ok := cfg.App.ExpandedFolders[accID]; ok {
			pref.custom = true
			for _, p := range paths {
				pref.expanded[strings.ToLower(cleanFolderPath(p))] = true
			}
		} else {
			// Seed checkboxes with current live expanded state so toggling custom mode starts sensible.
			for _, p := range a.currentExpandedFolderPaths(accID) {
				pref.expanded[strings.ToLower(cleanFolderPath(p))] = true
			}
		}
		s.folderPrefs[accID] = pref
	}
	for _, acc := range a.accounts {
		addAcc(acc.ID, acc.Name)
	}
	for _, f := range a.folders {
		addAcc(f.AccountID, f.AccountName)
	}
	if len(s.folderAccLabels) > 0 {
		s.folderAccLabel = s.folderAccLabels[0]
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
	layout := "side"
	if s.linkPreviewLayout == 1 {
		layout = "bottom"
	}
	rules := make([]config.LinkPreviewRule, 0, len(s.linkPreviewRules))
	for _, r := range s.linkPreviewRules {
		r.Name = strings.TrimSpace(r.Name)
		r.SenderPattern = strings.TrimSpace(r.SenderPattern)
		r.URLPattern = strings.TrimSpace(r.URLPattern)
		if r.URLPattern == "" {
			continue
		}
		if r.Name == "" {
			r.Name = r.URLPattern
		}
		rules = append(rules, r)
	}
	cfg.App.LinkPreview = config.LinkPreviewConfig{
		Enabled:  s.linkPreviewEnabled,
		AutoLoad: s.linkPreviewAutoLoad,
		Layout:   layout,
		Rules:    rules,
	}
	if len(s.folderPrefs) > 0 {
		expMap := make(map[string][]string)
		// Preserve any configured accounts not currently loaded in memory.
		for accID, paths := range cfg.App.ExpandedFolders {
			if _, managed := s.folderPrefs[accID]; !managed {
				expMap[accID] = append([]string(nil), paths...)
			}
		}
		for accID, pref := range s.folderPrefs {
			if !pref.custom {
				continue
			}
			// Preserve original folder path casing from known folders where available.
			canonByLow := make(map[string]string)
			for _, row := range a.expandableFoldersForAccount(accID) {
				cp := cleanFolderPath(row.Folder.Path)
				canonByLow[strings.ToLower(cp)] = cp
			}
			out := make([]string, 0, len(pref.expanded))
			for low, on := range pref.expanded {
				if !on {
					continue
				}
				if orig, ok := canonByLow[low]; ok {
					out = append(out, orig)
				} else {
					out = append(out, low)
				}
			}
			expMap[accID] = out
		}
		if len(expMap) > 0 {
			cfg.App.ExpandedFolders = expMap
		} else {
			cfg.App.ExpandedFolders = nil
		}
	}
	cfg.App.AllowedImageSenders = append([]string(nil), s.allowedImageSenders...)
	cfg = config.Normalize(cfg)
	if err := a.opts.ConfigSaver(cfg); err != nil {
		s.err = a.tr("Nicht gespeichert: ", "Not saved: ") + err.Error()
		return false
	}
	a.applyConfig(cfg)
	a.applyConfiguredFolderCollapse()
	a.preview.layout = cfg.App.LinkPreview.Layout
	if a.selectedMsg != nil {
		a.preview.msgID = "" // re-evaluate auto-preview rules for the currently open mail
		a.syncPreviewForMessage(a.selectedMsg)
	}
	a.stopLANQRSession()
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
		ui.Column(c).Width(a.modalW(680)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(8).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Einstellungen", "Settings")).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("set-x"), "✕").Clicked() {
					a.stopLANQRSession()
					s.open = false
				}
			})
			ui.Scroll(c.Key("set-scroll")).FillWidth().MaxHeight(580).Children(func() {
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
					a.stopLANQRSession()
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
		if a.isPhone() {
			// Too narrow for label and control side by side: the control goes below.
			ui.Column(c.Key(key)).FillWidth().Gap(4).Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Row(c).FillWidth().Wrap().Gap(6).Children(body)
			})
			return
		}
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
		note(a.tr("„Nur lesen“ ist in config.json aktiviert: Mails bleiben ungelesen.", "Read-only is enabled in config.json: emails stay unread."), t.Warning)
	}

	if len(s.folderAccLabels) > 0 {
		a.settingsSection(c, a.tr("Ordner beim Start (pro Postfach)", "Startup folders (per mailbox)"))
		note(a.tr(
			"Standardmäßig ist nur INBOX bis Ebene 2 aufgeklappt (Unterordner & Archiv eingeklappt). Hier kannst du pro Postfach festlegen, welche Ordner beim Start aufgeklappt sein sollen.",
			"By default only INBOX expands to level 2 (deeper subfolders & Archives start collapsed). Configure per mailbox which folders should start expanded.",
		), t.TextMuted)
		if len(s.folderAccLabels) > 1 {
			field("set-fld-acc", a.tr("Postfach", "Mailbox"), func() {
				ui.Select(c.Key("set-fld-accs"), &s.folderAccLabel, s.folderAccLabels).Grow(1)
			})
		}
		accID := s.folderAccByLbl[s.folderAccLabel]
		if pref, ok := s.folderPrefs[accID]; ok {
			ui.Column(c.Key("set-fld-box-"+accID)).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
					ui.Checkbox(c.Key("set-fld-cust-"+accID), &pref.custom, a.tr("Eigene Start-Ordner (Rest eingeklappt)", "Custom startup folders (collapse rest)"))
					ui.Spacer(c)
					if ui.Button(c.Key("set-fld-cap-"+accID), a.tr("Aktuelle Sidebar übernehmen", "Use current sidebar")).Padding(2, 8).Clicked() {
						pref.custom = true
						pref.expanded = make(map[string]bool)
						for _, p := range a.currentExpandedFolderPaths(accID) {
							pref.expanded[strings.ToLower(cleanFolderPath(p))] = true
						}
					}
					if pref.custom {
						if ui.Button(c.Key("set-fld-rst-"+accID), a.tr("Standard", "Reset")).Padding(2, 8).Clicked() {
							pref.custom = false
						}
					}
				})
				if pref.custom {
					rows := a.expandableFoldersForAccount(accID)
					if len(rows) == 0 {
						note(a.tr("Dieses Postfach hat keine Ordner mit Unterordnern.", "This mailbox has no folders with subfolders."), t.TextMuted)
					} else {
						ui.Divider(c)
						ui.Scroll(c.Key("set-fld-scroll-" + accID)).MaxHeight(180).FillWidth().Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								for _, row := range rows {
									cp := cleanFolderPath(row.Folder.Path)
									low := strings.ToLower(cp)
									checked := pref.expanded[low]
									lbl := row.Folder.Name
									if lbl == "" {
										lbl = cp
									}
									if row.Depth > 0 {
										lbl = strings.Repeat("   ", row.Depth) + "↳ " + lbl
									}
									ui.Row(c.Key("set-fld-r-" + accID + "-" + low)).FillWidth().AlignItems(ui.Center).Children(func() {
										if ui.Checkbox(c.Key("set-fld-c-"+accID+"-"+low), &checked, lbl).Changed() {
											if checked {
												pref.expanded[low] = true
											} else {
												delete(pref.expanded, low)
											}
										}
									})
								}
							})
						})
					}
				}
			})
		}
	}

	a.settingsSection(c, a.tr("Browser-Vorschau (Split-Panel)", "Browser Preview (Split Panel)"))
	note(a.tr(
		"Öffnet passende Links (z. B. Jira-/Confluence-Issues) direkt neben oder unter der Mail im eingebetteten Browser mit persistenter Login-Session.",
		"Opens matching links (e.g. Jira/Confluence issues) directly beside or below the email in an embedded browser with persistent login session.",
	), t.TextMuted)
	ui.Checkbox(c.Key("set-lp-en"), &s.linkPreviewEnabled, a.tr("Browser-Vorschau aktivieren", "Enable Browser Preview"))
	ui.Checkbox(c.Key("set-lp-auto"), &s.linkPreviewAutoLoad, a.tr("Bei Treffer einer Regel automatisch öffnen (nur aktive Regeln)", "Auto-open when a rule matches (enabled rules only)"))
	field("set-lp-lay", a.tr("Standard-Layout", "Default layout"), func() {
		ui.Segmented(c.Key("set-lp-lays"), &s.linkPreviewLayout, a.tr("Nebeneinander (Rechts)", "Side by side"), a.tr("Übereinander (Unten)", "Top & bottom"))
	})
	ui.Column(c.Key("set-lp-rules")).FillWidth().Gap(4).Padding(8).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
		ui.Text(c, a.tr("Auto-Link-Regeln (Absender & URL-Muster)", "Auto-Link Rules (Sender & URL pattern)")).FontSize(11.5).Bold()
		deleteIdx := -1
		for i := range s.linkPreviewRules {
			r := &s.linkPreviewRules[i]
			ui.Row(c.Key(fmt.Sprintf("set-lpr-%d", i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key(fmt.Sprintf("set-lpr-en-%d", i)), &r.Enabled, r.Name)
				ui.Spacer(c)
				meta := r.URLPattern
				if r.SenderPattern != "" {
					meta = r.SenderPattern + " → " + r.URLPattern
				}
				ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).SingleLine()
				if ui.Button(c.Key(fmt.Sprintf("set-lpr-del-%d", i)), "✕").Padding(1, 6).Clicked() {
					deleteIdx = i
				}
			})
		}
		if deleteIdx >= 0 {
			s.linkPreviewRules = append(s.linkPreviewRules[:deleteIdx], s.linkPreviewRules[deleteIdx+1:]...)
		}
		ui.Divider(c)
		ui.Row(c.Key("set-lp-add")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("set-lpr-nn"), &s.newRuleName).Placeholder(a.tr("Name (z. B. Firmen-Jira)", "Name (e.g. Corp Jira)")).Width(140)
			ui.TextInput(c.Key("set-lpr-ns"), &s.newRuleSender).Placeholder(a.tr("Absender (optional)", "Sender (optional)")).Width(150)
			ui.TextInput(c.Key("set-lpr-nu"), &s.newRuleURL).Placeholder(a.tr("URL-Muster (z. B. jira.firma.de/)", "URL pattern (e.g. jira.corp.com/)")).Grow(1)
			if ui.Button(c.Key("set-lpr-addb"), a.tr("+ Regel", "+ Rule")).Padding(3, 8).Clicked() {
				urlPat := strings.TrimSpace(s.newRuleURL)
				if urlPat != "" {
					name := strings.TrimSpace(s.newRuleName)
					if name == "" {
						name = urlPat
					}
					s.linkPreviewRules = append(s.linkPreviewRules, config.LinkPreviewRule{
						Name:          name,
						Enabled:       true,
						SenderPattern: strings.TrimSpace(s.newRuleSender),
						URLPattern:    urlPat,
					})
					s.newRuleName = ""
					s.newRuleSender = ""
					s.newRuleURL = ""
				}
			}
		})
	})

	a.settingsSection(c, a.tr("Externe Bilder — Erlaubte Absender", "Remote Images — Allowed Senders"))
	note(a.tr(
		"Externe Bilder sind aus Datenschutzgründen standardmäßig blockiert. Für diese Absender werden Bilder automatisch geladen.",
		"Remote images are blocked by default for privacy. Images from these senders load automatically.",
	), t.TextMuted)
	ui.Column(c.Key("set-img-allow")).FillWidth().Gap(4).Padding(8).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
		if len(s.allowedImageSenders) == 0 {
			ui.Text(c, a.tr("Noch keine Absender freigegeben.", "No senders allowed yet.")).FontSize(11.5).TextColor(t.TextMuted)
		} else {
			rmIdx := -1
			for i, em := range s.allowedImageSenders {
				ui.Row(c.Key(fmt.Sprintf("set-imgr-%d", i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, em).FontSize(12).Grow(1).SingleLine()
					if ui.Button(c.Key(fmt.Sprintf("set-imgd-%d", i)), "✕").Padding(1, 6).Clicked() {
						rmIdx = i
					}
				})
			}
			if rmIdx >= 0 {
				s.allowedImageSenders = append(s.allowedImageSenders[:rmIdx], s.allowedImageSenders[rmIdx+1:]...)
			}
		}
		ui.Divider(c)
		ui.Row(c.Key("set-img-add")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("set-img-in"), &s.newImageSender).Placeholder(a.tr("absender@domain.de", "sender@domain.com")).Grow(1)
			clicked := ui.Button(c.Key("set-img-addb"), a.tr("+ Hinzufügen", "+ Add")).Padding(3, 8).Clicked()
			if (clicked || in.Submitted()) && strings.TrimSpace(s.newImageSender) != "" {
				em := extractSenderEmail(s.newImageSender)
				dup := false
				for _, existing := range s.allowedImageSenders {
					if strings.EqualFold(existing, em) {
						dup = true
						break
					}
				}
				if em != "" && !dup {
					s.allowedImageSenders = append(s.allowedImageSenders, em)
				}
				s.newImageSender = ""
			}
		})
	})

	a.settingsSection(c, a.tr("KI-Assistent", "AI assistant"))
	if !a.cfg.Features.Assistant {
		note(a.tr("Der KI-Assistent ist in config.json ausgeschaltet.", "The AI assistant is switched off in config.json."), t.Warning)
	}
	field("set-ag-b", a.tr("Anbieter", "Provider"), func() { ui.Select(c.Key("set-ag-bs"), &s.agentBackend, s.providers).Grow(1) })
	a.renderSettingsModelField(c, field, "set-ag", s.agentBackend, &s.agentModel, s.agentEndpoint, &s.agentModels, &s.agentModelsKey, &s.agentModelsLoading, &s.agentModelCustom, a.tr("leer = Standard des Anbieters", "empty = provider default"))
	field("set-ag-e", "Endpoint", func() {
		ui.TextInput(c.Key("set-ag-ei"), &s.agentEndpoint).Placeholder(a.tr("leer = offizielle Adresse", "empty = official URL")).Grow(1)
	})

	a.settingsSection(c, a.tr("Triage-Scan", "Triage scan"))
	if !a.cfg.Features.Triage {
		note(a.tr("Triage-Scans sind in config.json ausgeschaltet.", "Triage scans are switched off in config.json."), t.Warning)
	}
	ui.Checkbox(c.Key("set-new"), &s.scanOnNewMail, a.tr("Bei neuer Mail automatisch scannen", "Scan automatically when new mail arrives"))
	ui.Checkbox(c.Key("set-clf"), &s.classifyAI, a.tr("Mails per KI einordnen (sonst lokale Schätzung, 0 Tokens)", "Sort mails with AI (else a local estimate, 0 tokens)"))
	if s.classifyAI {
		field("set-clf-b", a.tr("Anbieter", "Provider"), func() { ui.Select(c.Key("set-clf-bs"), &s.clfBackend, s.providers).Grow(1) })
		a.renderSettingsModelField(c, field, "set-clf", s.clfBackend, &s.clfModel, s.clfEndpoint, &s.clfModels, &s.clfModelsKey, &s.clfModelsLoading, &s.clfModelCustom, a.tr("leer = günstiges Standardmodell", "empty = cheap default model"))
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

	a.renderSettingsLANQRSection(c)
}

func (a *App) renderSettingsModelField(
	c *ui.Context,
	field func(string, string, func()),
	keyPrefix string,
	backendName string,
	modelVal *string,
	endpointVal string,
	models *[]string,
	loadedKey *string,
	loading *bool,
	customMode *bool,
	fallbackPH string,
) {
	wantKey := backendName + "|" + strings.TrimSpace(endpointVal)
	hint, isCLI := ai.CLIModelHints(backendName)
	if !isCLI && *loadedKey != wantKey && !*loading {
		*loadedKey = wantKey
		a.loadSettingsModels(backendName, endpointVal, models, loadedKey, loading, wantKey)
	}
	field(keyPrefix+"-m", a.tr("Modell", "Model"), func() {
		if !isCLI && len(*models) > 0 && !*customMode {
			defLbl := a.tr("Standard (Anbieter)", "Default (Provider)")
			customLbl := a.tr("Eigene Modell-ID…", "Custom model ID…")
			choices := make([]string, 0, len(*models)+3)
			choices = append(choices, defLbl)
			cur := strings.TrimSpace(*modelVal)
			if cur != "" && !slices.Contains(*models, cur) {
				choices = append(choices, cur)
			}
			choices = append(choices, *models...)
			choices = append(choices, customLbl)
			sel := cur
			if sel == "" {
				sel = defLbl
			}
			if ui.Select(c.Key(keyPrefix+"-ms"), &sel, choices).Grow(1).Changed() {
				switch sel {
				case defLbl:
					*modelVal = ""
				case customLbl:
					*customMode = true
				default:
					*modelVal = sel
				}
			}
		} else {
			ph := fallbackPH
			if isCLI {
				ph = a.tr(hint.PlaceholderDE, hint.PlaceholderEN)
			}
			ui.TextInput(c.Key(keyPrefix+"-mi"), modelVal).Placeholder(ph).Grow(1)
		}
		if !isCLI {
			if len(*models) > 0 {
				toggleTip := a.tr("Modell-ID frei eingeben", "Enter custom model ID")
				if *customMode {
					toggleTip = a.tr("Aus Modell-Liste wählen", "Choose from model list")
				}
				if iconBtn(c.Key(keyPrefix+"-mc"), iconPen, toggleTip, ui.Color{}, *customMode).Clicked() {
					*customMode = !*customMode
				}
			}
			refBtn := iconBtn(c.Key(keyPrefix+"-mr"), iconRefresh, a.tr("Modelle neu laden", "Reload models"), ui.Color{}, false)
			if *loading {
				refBtn.Disabled(true)
			}
			if refBtn.Clicked() && !*loading {
				a.loadSettingsModels(backendName, endpointVal, models, loadedKey, loading, wantKey)
			}
		}
	})
	if isCLI {
		a.renderCLIModelHintChips(c, keyPrefix+"-cli", modelVal, hint)
	}
}

func (a *App) loadSettingsModels(backendName, endpoint string, dst *[]string, loadedKey *string, loading *bool, wantKey string) {
	*loading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		cfg := a.cfg
		if a.opts.ConfigLoader != nil {
			if loaded, err := a.opts.ConfigLoader(); err == nil {
				cfg = loaded
			}
		}
		prov := ai.ProviderForBackend(cfg.AI, backendName)
		if strings.TrimSpace(endpoint) != "" {
			prov.Endpoint = strings.TrimSpace(endpoint)
		}
		list, _ := ai.ListModels(ctx, prov, a.opts.AnyLLMOpts...)
		names := make([]string, 0, len(list))
		for _, m := range list {
			if m.ID != "" {
				names = append(names, m.ID)
			}
		}
		a.emitUpdate(func() {
			*loading = false
			if *loadedKey == wantKey {
				*dst = names
			}
		})
	}()
}
