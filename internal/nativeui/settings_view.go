package nativeui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	anyllm "github.com/mozilla-ai/any-llm-go"

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
	custom       bool
	expanded     map[string]bool // clean folder path (lowercase) -> start expanded
	customPinned bool
	pinned       map[string]bool // top-level folder path (lowercase) -> always visible outside "More folders"
}

const (
	settingsTabGeneral  = 0
	settingsTabCompose  = 1
	settingsTabFolders  = 2
	settingsTabPrivacy  = 3
	settingsTabAI       = 4
	settingsTabAccounts = 5
)

type settingsUI struct {
	open      bool
	tab       int // 0: General, 1: Compose, 2: Folders, 3: Web & Images, 4: AI & Sync
	providers []string

	markRead int // 0: at once, 1: after 3 s, 2: never

	undoSendOpt int // 0: off, 1: 5s, 2: 10s, 3: 20s
	autoSaveOpt int // 0: off, 1: 15s, 2: 25s, 3: 60s
	templates   []config.ComposeTemplate
	newTplName  string
	newTplSubj  string
	newTplBody  string

	// Per-mailbox sidebar folder visibility & startup expansion preferences
	compactFolders  bool
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
	agentModelsErr                          string
	agentModelCustom                        bool
	savedEndpoints                          map[string]string

	classifyAI                        bool
	clfBackend, clfModel, clfEndpoint string
	clfModels                         []string
	clfModelsKey                      string
	clfModelsLoading                  bool
	clfModelsErr                      string
	clfModelCustom                    bool
	maxCalls           string
	allowCLI           bool
	customInstructions string
	mcpMode            int // 0: read-only, 1: +drafts, 2: +writes

	scanOnNewMail       bool
	scanLimit, scanDays string
	triageAccounts      []triageAccountRow
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
// triageAccountRow is one "In KI-Sortierung einbeziehen" checkbox in the triage section.
type triageAccountRow struct {
	id, label string
	include   bool
}

func (a *App) OpenSettings() {
	cfg := a.cfg
	if a.opts.ConfigLoader != nil {
		if loaded, err := a.opts.ConfigLoader(); err == nil {
			cfg = loaded
		}
	}
	lpRules := make([]config.LinkPreviewRule, len(cfg.App.LinkPreview.Rules))
	copy(lpRules, cfg.App.LinkPreview.Rules)
	lpLayout := 0
	if cfg.App.LinkPreview.Layout == "bottom" {
		lpLayout = 1
	}
	savedEndpoints := make(map[string]string, len(cfg.AI.Endpoints)+2)
	for k, v := range cfg.AI.Endpoints {
		if ck := ai.CanonicalBackend(k); ck != "" && strings.TrimSpace(v) != "" {
			savedEndpoints[ck] = strings.TrimSpace(v)
		}
	}
	if ck := ai.CanonicalBackend(cfg.AI.Agent.Backend); ck != "" && strings.TrimSpace(cfg.AI.Agent.Endpoint) != "" {
		savedEndpoints[ck] = strings.TrimSpace(cfg.AI.Agent.Endpoint)
	}
	if ck := ai.CanonicalBackend(cfg.AI.Classifier.Backend); ck != "" && strings.TrimSpace(cfg.AI.Classifier.Endpoint) != "" {
		if _, exists := savedEndpoints[ck]; !exists {
			savedEndpoints[ck] = strings.TrimSpace(cfg.AI.Classifier.Endpoint)
		}
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
		savedEndpoints:      savedEndpoints,
		classifyAI:          cfg.AI.ClassifyWithAI,
		clfBackend:          ai.CanonicalBackend(cfg.AI.Classifier.Backend),
		clfModel:            cfg.AI.Classifier.Model,
		clfEndpoint:         cfg.AI.Classifier.Endpoint,
		maxCalls:            strconv.Itoa(cfg.AI.MaxAICallsPerScan),
		allowCLI:            cfg.AI.AllowUnrestrictedCLI,
		customInstructions:  cfg.AI.CustomInstructions,
		scanOnNewMail:       cfg.Mailbox.ScanOnNewMail,
		scanLimit:           strconv.Itoa(cfg.Mailbox.ScanLimit),
		scanDays:            strconv.Itoa(cfg.Mailbox.ScanDays),
		archiveDir:          cfg.Mailbox.ArchiveDir,
	}
	for _, acc := range cfg.Direct.Accounts {
		label := acc.Name
		if label == "" {
			label = acc.Email
		}
		s.triageAccounts = append(s.triageAccounts, triageAccountRow{id: acc.ID, label: label, include: cfg.Mailbox.TriageIncludes(acc.ID)})
	}
	switch cfg.App.EffectiveUndoSendSeconds() {
	case 0:
		s.undoSendOpt = 0
	case 10:
		s.undoSendOpt = 2
	case 20:
		s.undoSendOpt = 3
	default:
		s.undoSendOpt = 1
	}
	switch cfg.App.EffectiveDraftAutoSaveSeconds() {
	case 0:
		s.autoSaveOpt = 0
	case 15:
		s.autoSaveOpt = 1
	case 60:
		s.autoSaveOpt = 3
	default:
		s.autoSaveOpt = 2
	}
	s.templates = append([]config.ComposeTemplate(nil), cfg.App.ComposeTemplates...)
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

	// Populate per-mailbox sidebar folder preferences from config.json + known folders.
	s.compactFolders = cfg.App.EffectiveCompactFolders()
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

		pref := &accountFolderPref{
			expanded: make(map[string]bool),
			pinned:   make(map[string]bool),
		}
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
		if paths, ok := cfg.App.PinnedFolders[accID]; ok {
			pref.customPinned = true
			for _, p := range paths {
				pref.pinned[strings.ToLower(cleanFolderPath(p))] = true
			}
		} else {
			ruleTargets := a.activeRuleTargetFolders(accID)
			for _, f := range a.topLevelFoldersForAccount(accID) {
				low := strings.ToLower(cleanFolderPath(f.Path))
				if isInboxFolder(f) || ruleTargets[low] {
					pref.pinned[low] = true
				}
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
		s.tab = settingsTabAI
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
	if s.savedEndpoints == nil {
		s.savedEndpoints = make(map[string]string)
	}
	if s.agentBackend != "" {
		if ep := strings.TrimSpace(s.agentEndpoint); ep != "" {
			s.savedEndpoints[s.agentBackend] = ep
		} else {
			delete(s.savedEndpoints, s.agentBackend)
		}
	}
	if s.classifyAI && s.clfBackend != "" {
		if ep := strings.TrimSpace(s.clfEndpoint); ep != "" {
			s.savedEndpoints[s.clfBackend] = ep
		} else if s.clfBackend != s.agentBackend {
			delete(s.savedEndpoints, s.clfBackend)
		}
	}
	if len(s.savedEndpoints) > 0 {
		epCopy := make(map[string]string, len(s.savedEndpoints))
		for k, v := range s.savedEndpoints {
			if strings.TrimSpace(v) != "" {
				epCopy[k] = strings.TrimSpace(v)
			}
		}
		cfg.AI.Endpoints = epCopy
	} else {
		cfg.AI.Endpoints = nil
	}
	cfg.AI.ClassifyWithAI = s.classifyAI
	cfg.AI.MaxAICallsPerScan = maxCalls
	cfg.AI.AllowUnrestrictedCLI = s.allowCLI
	cfg.AI.CustomInstructions = strings.TrimSpace(s.customInstructions)
	cfg.Mailbox.ScanOnNewMail = s.scanOnNewMail
	cfg.Mailbox.TriageExcludedAccountIDs = nil
	for _, row := range s.triageAccounts {
		if row.include {
			if !cfg.Mailbox.AccountEnabled(row.id) {
				cfg.Mailbox.TriageAccountIDs = append(cfg.Mailbox.TriageAccountIDs, row.id)
			}
		} else {
			cfg.Mailbox.TriageExcludedAccountIDs = append(cfg.Mailbox.TriageExcludedAccountIDs, row.id)
		}
	}
	cfg.Mailbox.ScanLimit = limit
	cfg.Mailbox.ScanDays = days
	cfg.Mailbox.ArchiveDir = strings.TrimSpace(s.archiveDir)
	cfg.App.MarkRead = [...]string{config.MarkReadOnOpen, config.MarkReadDelay, config.MarkReadNever}[min(max(s.markRead, 0), 2)]
	undoSec := [...]int{0, 5, 10, 20}[min(max(s.undoSendOpt, 0), 3)]
	autoSec := [...]int{0, 15, 25, 60}[min(max(s.autoSaveOpt, 0), 3)]
	cfg.App.UndoSendSeconds = &undoSec
	cfg.App.DraftAutoSaveSeconds = &autoSec
	tpls := make([]config.ComposeTemplate, 0, len(s.templates))
	for _, tpl := range s.templates {
		tpl.Name = strings.TrimSpace(tpl.Name)
		tpl.Subject = strings.TrimSpace(tpl.Subject)
		tpl.Body = strings.TrimSpace(tpl.Body)
		if tpl.Name != "" && tpl.Body != "" {
			tpls = append(tpls, tpl)
		}
	}
	cfg.App.ComposeTemplates = tpls
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
	compactVal := s.compactFolders
	cfg.App.CompactFolders = &compactVal
	if len(s.folderPrefs) > 0 {
		expMap := make(map[string][]string)
		pinMap := make(map[string][]string)
		// Preserve any configured accounts not currently loaded in memory.
		for accID, paths := range cfg.App.ExpandedFolders {
			if _, managed := s.folderPrefs[accID]; !managed {
				expMap[accID] = append([]string(nil), paths...)
			}
		}
		for accID, paths := range cfg.App.PinnedFolders {
			if _, managed := s.folderPrefs[accID]; !managed {
				pinMap[accID] = append([]string(nil), paths...)
			}
		}
		for accID, pref := range s.folderPrefs {
			if pref.custom {
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
			if pref.customPinned {
				canonByLow := make(map[string]string)
				for _, f := range a.topLevelFoldersForAccount(accID) {
					cp := cleanFolderPath(f.Path)
					canonByLow[strings.ToLower(cp)] = cp
				}
				out := make([]string, 0, len(pref.pinned))
				for low, on := range pref.pinned {
					if !on {
						continue
					}
					if orig, ok := canonByLow[low]; ok {
						out = append(out, orig)
					} else {
						out = append(out, low)
					}
				}
				pinMap[accID] = out
			}
		}
		if len(expMap) > 0 {
			cfg.App.ExpandedFolders = expMap
		} else {
			cfg.App.ExpandedFolders = nil
		}
		if len(pinMap) > 0 {
			cfg.App.PinnedFolders = pinMap
		} else {
			cfg.App.PinnedFolders = nil
		}
	}
	cfg.App.AllowedImageSenders = append([]string(nil), s.allowedImageSenders...)
	// The switch saves itself in the background; this save must not bring back the old value.
	notify := a.notifyEnabled
	cfg.App.NotifyEnabled = &notify
	cfg = config.Normalize(cfg)
	if err := a.opts.ConfigSaver(cfg); err != nil {
		s.err = a.tr("Nicht gespeichert: ", "Not saved: ") + err.Error()
		return false
	}
	a.applyConfig(cfg)
	a.syncCopilotFromConfig(cfg)
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
	wasOpen := s.open
	maxScrollH := float32(560)
	if a.winH > 0 {
		maxScrollH = min(560, max(220, a.winH-220))
	}
	a.modal(c, &s.open, func() {
		ui.Column(c).Width(a.modalW(760)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, a.tr("Einstellungen", "Settings")).FontSize(15).Bold().Grow(1)
				if v := a.versionTag(); v != "" {
					ui.Text(c, v).FontSize(10.5).TextColor(withAlpha(t.TextMuted, 0.72)).SingleLine()
				}
				if ui.Button(c.Key("set-x"), "✕").Clicked() {
					a.stopLANQRSession()
					s.open = false
				}
			})
			type tabSpec struct {
				id    int
				label string
			}
			tabs := []tabSpec{
				{settingsTabGeneral, a.tr("Allgemein", "General")},
			}
			if a.isDirectMode() {
				tabs = append(tabs, tabSpec{settingsTabAccounts, a.tr("Konten", "Accounts")})
			}
			tabs = append(tabs,
				tabSpec{settingsTabCompose, a.tr("Verfassen", "Compose")},
				tabSpec{settingsTabFolders, a.tr("Ordner", "Folders")},
				tabSpec{settingsTabPrivacy, a.tr("Web & Bilder", "Web & Images")},
				tabSpec{settingsTabAI, a.tr("KI & Sync", "AI & Sync")},
			)
			tabLabels := make([]string, len(tabs))
			selIdx := 0
			for i, tb := range tabs {
				tabLabels[i] = tb.label
				if tb.id == s.tab {
					selIdx = i
				}
			}
			prevTab := s.tab
			if a.isPhone() {
				ui.Row(c.Key("set-tabs-ph")).FillWidth().Wrap().Gap(6).Children(func() {
					for i, tb := range tabs {
						idx := tb.id
						var btn ui.Element
						if s.tab == idx {
							btn = ui.PrimaryButton(c.Key(fmt.Sprintf("set-tab-%d", i)), tb.label).Padding(4, 10)
						} else {
							btn = ui.Button(c.Key(fmt.Sprintf("set-tab-%d", i)), tb.label).Padding(4, 10)
						}
						if btn.Clicked() {
							s.tab = idx
						}
					}
				})
			} else {
				if ui.Segmented(c.Key("set-tabs"), &selIdx, tabLabels...).Changed() && selIdx >= 0 && selIdx < len(tabs) {
					s.tab = tabs[selIdx].id
				}
			}
			if s.tab == settingsTabAccounts && prevTab != settingsTabAccounts {
				a.initDirectTabState(a.direct.tab)
			}
			sc := ui.Scroll(c.Key(fmt.Sprintf("set-scroll-%d", s.tab))).FillWidth().MaxHeight(maxScrollH)
			if s.tab == settingsTabAccounts {
				sc = sc.TrackScroll(&a.widgets.directScroll)
			}
			sc.Children(func() {
				ui.Column(c).FillWidth().Gap(8).Padding(0, 6, 0, 0).Children(func() {
					a.renderSettingsBody(c)
				})
			})
			if s.err != "" && s.tab != settingsTabAccounts {
				ui.Text(c, s.err).FontSize(12).TextColor(t.Danger)
			}
			if s.tab == settingsTabAccounts {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconLock).Size(13, 13).TextColor(t.TextMuted)
					ui.Text(c, a.tr("Passwörter & OAuth-Tokens werden sicher im System-Schlüsselbund gespeichert.", "Passwords & OAuth tokens are stored securely in your system keychain.")).FontSize(11).TextColor(t.TextMuted).Grow(1)
					if ui.Button(c.Key("set-close"), a.tr("Schließen", "Close")).Clicked() {
						a.stopLANQRSession()
						s.open = false
					}
				})
			} else {
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
			}
		})
	})
	if wasOpen && !s.open && s.lanSession != nil {
		a.stopLANQRSession()
	}
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

	switch s.tab {
	case settingsTabAccounts:
		a.renderDirectContent(c)
	case settingsTabGeneral:
		a.settingsSection(c, a.tr("Darstellung & Sprache", "Appearance & Language"))
		field("set-lang", a.tr("Sprache", "Language"), func() {
			langLbl := a.langLabel
			if ui.Select(c.Key("set-lang-s"), &langLbl, langChoiceLabels()).Grow(1).Changed() {
				a.langLabel = langLbl
				a.applyLanguageChoice(langLbl)
			}
		})
		field("set-theme", a.tr("Design", "Theme"), func() {
			thLbl := a.themeLabel
			if ui.Select(c.Key("set-theme-s"), &thLbl, a.themeLabelList()).Grow(1).Changed() {
				a.applyThemeChoice(thLbl)
			}
		})
		field("set-style-body", a.tr("Mail-Inhalt einfärben", "Recolor mail bodies"), func() {
			sb := a.cfg.Theme.StyleBody
			if ui.Checkbox(c.Key("set-style-body-c"), &sb, a.tr("HTML-Mails an App-Theme anpassen", "Adapt HTML emails to app theme")).Changed() {
				cfg, err := a.opts.ConfigLoader()
				if err != nil {
					cfg = a.cfg
				}
				cfg.Theme.StyleBody = sb
				if a.opts.ConfigSaver != nil {
					_ = a.opts.ConfigSaver(cfg)
				}
				a.applyConfig(cfg)
				a.readerBody = readerBody{}
			}
		})

		field("set-list-snippet", a.tr("Mail-Liste", "Mail list"), func() {
			// Saved at once, like "Recolor mail bodies".
			on := a.cfg.App.EffectiveListSnippets()
			if ui.Checkbox(c.Key("set-list-snippet-c"), &on, a.tr("Vorschauzeile in der Liste", "Preview line in the list")).Changed() {
				cfg, err := a.opts.ConfigLoader()
				if err != nil {
					cfg = a.cfg
				}
				cfg.App.ListSnippets = &on
				if a.opts.ConfigSaver != nil {
					_ = a.opts.ConfigSaver(cfg)
				}
				a.applyConfig(cfg)
			}
		})

		a.settingsSection(c, a.tr("Benachrichtigungen", "Notifications"))
		field("set-notify", a.tr("Neue Mails & Termine", "New mail & events"), func() {
			// Applies at once and is saved like the toolbar/tray toggle (also on phones).
			if ui.Checkbox(c.Key("set-notify-c"), &a.notifyEnabled, a.tr("Systembenachrichtigungen anzeigen", "Show system notifications")).Changed() {
				a.persistLayoutPrefs()
			}
		})

		a.settingsSection(c, a.tr("Lesen", "Reading"))
		field("set-read", a.tr("Als gelesen markieren", "Mark as read"), func() {
			ui.Segmented(c.Key("set-read-s"), &s.markRead, a.tr("Beim Öffnen", "On open"), a.tr("Nach 3 Sekunden", "After 3 seconds"), a.tr("Nie", "Never"))
		})
		if a.readOnly {
			note(a.tr("„Nur lesen“ ist in config.json aktiviert: Mails bleiben ungelesen.", "Read-only is enabled in config.json: emails stay unread."), t.Warning)
		}

		a.settingsSection(c, a.tr("Archiv", "Archive"))
		field("set-arch", a.tr("Archiv-Ordner", "Archive folder"), func() { ui.TextInput(c.Key("set-arch-i"), &s.archiveDir).Grow(1) })

	case settingsTabCompose:
		a.settingsSection(c, a.tr("Verfassen, Undo-Send & Auto-Save", "Compose, Undo Send & Auto-Save"))
		field("set-undo-send", a.tr("Senden rückgängig (Verzögerung)", "Undo Send delay"), func() {
			ui.Segmented(c.Key("set-undo-sends"), &s.undoSendOpt, a.tr("Sofort", "Off (0s)"), "5 s", "10 s", "20 s")
		})
		field("set-auto-save", a.tr("Entwurf Auto-Save", "Draft auto-save"), func() {
			ui.Segmented(c.Key("set-auto-saves"), &s.autoSaveOpt, a.tr("Aus", "Off"), "15 s", "25 s", "60 s")
		})

		a.settingsSection(c, a.tr("Textbausteine / Vorlagen", "Snippets / Templates"))
		ui.Column(c.Key("set-tpl-box")).FillWidth().Gap(6).Padding(8).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Text(c, a.tr("Vorlagen lassen sich im Verfassen-Fenster direkt am Cursor einfügen.", "Templates can be inserted directly at the cursor while composing.")).FontSize(11.5).TextColor(t.TextMuted)
			if len(s.templates) == 0 {
				ui.Text(c, a.tr("Noch keine Vorlagen angelegt.", "No templates added yet.")).FontSize(11.5).TextColor(t.TextMuted)
			} else {
				delIdx := -1
				for i, tpl := range s.templates {
					ui.Row(c.Key(fmt.Sprintf("set-tpl-%d", i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tpl.Name).FontSize(12).Bold().Width(140).SingleLine()
						ui.Text(c, strings.ReplaceAll(tpl.Body, "\n", " ")).FontSize(11.5).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
						if ui.Button(c.Key(fmt.Sprintf("set-tpl-del-%d", i)), "✕").Padding(1, 6).Clicked() {
							delIdx = i
						}
					})
				}
				if delIdx >= 0 {
					s.templates = append(s.templates[:delIdx], s.templates[delIdx+1:]...)
				}
			}
			ui.Divider(c)
			ui.Row(c.Key("set-tpl-add-hdr")).FillWidth().Gap(6).Wrap().Children(func() {
				ui.TextInput(c.Key("set-tpl-nn"), &s.newTplName).Placeholder(a.tr("Vorlagenname…", "Template name…")).Width(160)
				ui.TextInput(c.Key("set-tpl-ns"), &s.newTplSubj).Placeholder(a.tr("Betreff (optional)…", "Subject (optional)…")).Grow(1)
			})
			ui.Row(c.Key("set-tpl-add-b")).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
				ui.TextArea(c.Key("set-tpl-nb"), &s.newTplBody).Placeholder(a.tr("Vorlagentext…", "Template text…")).Grow(1).Height(54)
				if ui.Button(c.Key("set-tpl-add"), a.tr("+ Vorlage", "+ Template")).Padding(4, 9).Clicked() {
					name := strings.TrimSpace(s.newTplName)
					body := strings.TrimSpace(s.newTplBody)
					if name != "" && body != "" {
						s.templates = append(s.templates, config.ComposeTemplate{
							ID:      fmt.Sprintf("tpl-%d", time.Now().UnixNano()),
							Name:    name,
							Subject: strings.TrimSpace(s.newTplSubj),
							Body:    body,
						})
						s.newTplName = ""
						s.newTplSubj = ""
						s.newTplBody = ""
					}
				}
			})
		})

	case settingsTabFolders:
		a.settingsSection(c, a.tr("Ordner in der Seitenleiste (pro Postfach)", "Sidebar folders (per mailbox)"))
		ui.Checkbox(c.Key("set-fld-compact"), &s.compactFolders, a.tr(
			"Kompakte Ordneransicht (weniger genutzte Ordner unter „Weitere Ordner“ einklappen)",
			"Compact folder view (collapse less-used folders into “More folders”)",
		))
		note(a.tr(
			"Zeigt pro Postfach nur Posteingang (INBOX) und aktive Filterregel-Zielordner direkt an. Archiv, Entwürfe, Gesendet, Papierkorb & Co. bleiben eingeklappt unter „Weitere Ordner“ — ideal auf Handy & Desktop bei mehreren Konten.",
			"Shows only INBOX and active filter-rule target folders directly per mailbox. Archives, Drafts, Sent, Trash, etc. stay collapsed inside “More folders” — keeping all accounts visible on phone & desktop.",
		), t.TextMuted)
		if len(s.folderAccLabels) == 0 {
			note(a.tr("Noch keine Postfächer geladen. Verbinde zuerst ein E-Mail-Konto.", "No mailboxes loaded yet. Connect an email account first."), t.TextMuted)
			return
		}
		if len(s.folderAccLabels) > 1 {
			field("set-fld-acc", a.tr("Postfach", "Mailbox"), func() {
				ui.Select(c.Key("set-fld-accs"), &s.folderAccLabel, s.folderAccLabels).Grow(1)
			})
		}
		accID := s.folderAccByLbl[s.folderAccLabel]
		if pref, ok := s.folderPrefs[accID]; ok {
			if s.compactFolders {
				ui.Column(c.Key("set-pin-box-"+accID)).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
						ui.Checkbox(c.Key("set-pin-cust-"+accID), &pref.customPinned, a.tr("Immer sichtbare Ordner manuell wählen", "Customize always-visible folders"))
						ui.Spacer(c)
						if pref.customPinned {
							if ui.Button(c.Key("set-pin-rst-"+accID), a.tr("Auto (INBOX + Regeln)", "Auto (INBOX + rules)")).Padding(2, 8).Clicked() {
								pref.customPinned = false
							}
						}
					})
					if pref.customPinned {
						roots := a.topLevelFoldersForAccount(accID)
						ruleTargets := a.activeRuleTargetFolders(accID)
						ui.Divider(c)
						ui.Scroll(c.Key("set-pin-scroll-" + accID)).MaxHeight(180).FillWidth().Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								for _, f := range roots {
									cp := cleanFolderPath(f.Path)
									low := strings.ToLower(cp)
									lbl := f.Name
									if lbl == "" {
										lbl = cp
									}
									if isInboxFolder(f) {
										checked := true
										ui.Checkbox(c.Key("set-pin-c-"+accID+"-"+low), &checked, lbl+a.tr(" (immer sichtbar)", " (always visible)")).Disabled(true)
										continue
									}
									if ruleTargets[low] {
										lbl += a.tr(" (Regelziel)", " (rule target)")
									}
									checked := pref.pinned[low]
									if ui.Checkbox(c.Key("set-pin-c-"+accID+"-"+low), &checked, lbl).Changed() {
										if checked {
											pref.pinned[low] = true
										} else {
											delete(pref.pinned, low)
										}
									}
								}
							})
						})
					}
				})
			}

			ui.Column(c.Key("set-fld-box-"+accID)).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
					ui.Checkbox(c.Key("set-fld-cust-"+accID), &pref.custom, a.tr("Aufgeklappte Unterordner beim Start festlegen", "Custom expanded subfolders on startup"))
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

	case settingsTabPrivacy:
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
			ui.Row(c.Key("set-lp-add")).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
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

	case settingsTabAI:
		a.settingsSection(c, a.tr("KI-Assistent", "AI assistant"))
		if !a.cfg.Features.Assistant {
			note(a.tr("Der KI-Assistent ist in config.json ausgeschaltet.", "The AI assistant is switched off in config.json."), t.Warning)
		}
		prevAgentBackend := s.agentBackend
		field("set-ag-b", a.tr("Anbieter", "Provider"), func() {
			if ui.Select(c.Key("set-ag-bs"), &s.agentBackend, s.providers).Grow(1).Changed() && s.agentBackend != prevAgentBackend {
				if s.savedEndpoints == nil {
					s.savedEndpoints = make(map[string]string)
				}
				if ep := strings.TrimSpace(s.agentEndpoint); ep != "" {
					s.savedEndpoints[prevAgentBackend] = ep
				} else {
					delete(s.savedEndpoints, prevAgentBackend)
				}
				s.agentEndpoint = s.savedEndpoints[s.agentBackend]
				s.agentModels = nil
				s.agentModelsKey = ""
				s.agentModelsErr = ""
				s.agentModelCustom = false
			}
		})
		a.renderSettingsModelField(c, field, "set-ag", s.agentBackend, &s.agentModel, s.agentEndpoint, &s.agentModels, &s.agentModelsKey, &s.agentModelsLoading, &s.agentModelsErr, &s.agentModelCustom, a.tr("leer = Standard des Anbieters", "empty = provider default"))
		field("set-ag-e", "Endpoint", func() {
			epIn := ui.TextInput(c.Key("set-ag-ei"), &s.agentEndpoint).Placeholder(a.tr("leer = offizielle Adresse", "empty = official URL")).Grow(1)
			if epIn.Submitted() && !s.agentModelsLoading {
				wantKey := s.agentBackend + "|" + strings.TrimSpace(s.agentEndpoint)
				s.agentModelsKey = wantKey
				a.loadSettingsModels(s.agentBackend, s.agentEndpoint, &s.agentModels, &s.agentModelsKey, &s.agentModelsLoading, &s.agentModelsErr, wantKey)
			}
		})
		field("set-ai-inst", a.tr("Persönliche KI-Hinweise", "Custom AI instructions"), func() {
			ui.TextArea(c.Key("set-ai-inst-ta"), &s.customInstructions).
				Placeholder(a.tr("z. B. Ich heiße Frank (Avono AG). Kollegen (@avono.de) duzen, Kunden siezen, kurz & direkt ohne Floskeln…",
					"e.g. My name is Frank (Acme Corp). Informal tone for teammates, formal for customers, concise without fluff…")).
				Grow(1).Height(68)
		})
		note(a.tr("Wird bei Entwürfen, Zusammenfassungen, Compose-KI-Hilfe und im KI-Assistenten automatisch berücksichtigt.",
			"Automatically applied to reply drafts, summaries, Compose AI Assist, and the AI Assistant chat."), t.TextMuted)

		a.settingsSection(c, a.tr("Triage-Scan", "Triage scan"))
		if !a.cfg.Features.Triage {
			note(a.tr("Triage-Scans sind in config.json ausgeschaltet.", "Triage scans are switched off in config.json."), t.Warning)
		}
		ui.Checkbox(c.Key("set-new"), &s.scanOnNewMail, a.tr("Bei neuer Mail automatisch scannen", "Scan automatically when new mail arrives"))
		if len(s.triageAccounts) > 0 {
			a.settingsLabel(c, a.tr("In KI-Sortierung einbeziehen", "Include in AI sorting"))
			for i := range s.triageAccounts {
				row := &s.triageAccounts[i]
				ui.Checkbox(c.Key("set-tri-acc-"+row.id), &row.include, row.label)
			}
			note(a.tr("Scans und Archiv-Aufträge lassen nicht einbezogene Konten aus.", "Scans and archive jobs skip accounts that are not included."), t.TextMuted)
		}
		ui.Checkbox(c.Key("set-clf"), &s.classifyAI, a.tr("Mails per KI einordnen (sonst lokale Schätzung, 0 Tokens)", "Sort mails with AI (else a local estimate, 0 tokens)"))
		if s.classifyAI {
			prevClfBackend := s.clfBackend
			field("set-clf-b", a.tr("Anbieter", "Provider"), func() {
				if ui.Select(c.Key("set-clf-bs"), &s.clfBackend, s.providers).Grow(1).Changed() && s.clfBackend != prevClfBackend {
					if s.savedEndpoints == nil {
						s.savedEndpoints = make(map[string]string)
					}
					if ep := strings.TrimSpace(s.clfEndpoint); ep != "" {
						s.savedEndpoints[prevClfBackend] = ep
					} else {
						delete(s.savedEndpoints, prevClfBackend)
					}
					s.clfEndpoint = s.savedEndpoints[s.clfBackend]
					s.clfModels = nil
					s.clfModelsKey = ""
					s.clfModelsErr = ""
					s.clfModelCustom = false
				}
			})
			a.renderSettingsModelField(c, field, "set-clf", s.clfBackend, &s.clfModel, s.clfEndpoint, &s.clfModels, &s.clfModelsKey, &s.clfModelsLoading, &s.clfModelsErr, &s.clfModelCustom, a.tr("leer = günstiges Standardmodell", "empty = cheap default model"))
			field("set-clf-e", "Endpoint", func() {
				epIn := ui.TextInput(c.Key("set-clf-ei"), &s.clfEndpoint).Placeholder(a.tr("leer = offizielle Adresse", "empty = official URL")).Grow(1)
				if epIn.Submitted() && !s.clfModelsLoading {
					wantKey := s.clfBackend + "|" + strings.TrimSpace(s.clfEndpoint)
					s.clfModelsKey = wantKey
					a.loadSettingsModels(s.clfBackend, s.clfEndpoint, &s.clfModels, &s.clfModelsKey, &s.clfModelsLoading, &s.clfModelsErr, wantKey)
				}
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

		a.settingsSection(c, a.tr("API-Schlüssel", "API keys"))
		note(a.tr("Gespeichert in ", "Stored in ")+config.EnvFilePath()+a.tr(" (nur für dich lesbar). Neue Schlüssel gelten sofort.", " (readable only by you). New keys apply at once."), t.TextMuted)
		for i := range s.keys {
			k := &s.keys[i]
			ph := a.tr("nicht gesetzt", "not set")
			if k.set && !k.remove {
				ph = a.tr("gesetzt – leer lassen zum Behalten", "set – leave empty to keep")
			}
			if k.remove {
				ph = a.tr("wird entfernt", "will be removed")
			}
			if a.isPhone() {
				ui.Column(c.Key(fmt.Sprintf("set-key-%d", i))).FillWidth().Gap(4).Children(func() {
					ui.Text(c, k.backend).FontSize(12).TextColor(t.TextMuted)
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c.Key(fmt.Sprintf("set-key-i-%d", i)), &k.input).Password().Placeholder(ph).Grow(1)
						if k.set {
							ui.Checkbox(c.Key(fmt.Sprintf("set-key-rm-%d", i)), &k.remove, a.tr("Entfernen", "Remove"))
						}
					})
				})
			} else {
				ui.Row(c.Key(fmt.Sprintf("set-key-%d", i))).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, k.backend).FontSize(12).Width(150)
					ui.TextInput(c.Key(fmt.Sprintf("set-key-i-%d", i)), &k.input).Password().Placeholder(ph).Grow(1)
					if k.set {
						ui.Checkbox(c.Key(fmt.Sprintf("set-key-rm-%d", i)), &k.remove, a.tr("Entfernen", "Remove"))
					}
				})
			}
		}

		if !isMobileOS() {
			a.renderSettingsMCPSection(c)
		}
		a.renderSettingsLANQRSection(c)
	}
}

func mcpConfigSnippet(mode int) string {
	args := `["--mcp"]`
	switch mode {
	case 1:
		args = `["--mcp", "--allow-drafts"]`
	case 2:
		args = `["--mcp", "--allow-writes"]`
	}
	return "{\n  \"mcpServers\": {\n    \"maildiver\": {\n      \"command\": \"maildiver\",\n      \"args\": " + args + "\n    }\n  }\n}"
}

func (a *App) renderSettingsMCPSection(c *ui.Context) {
	t := c.Theme()
	s := &a.settings
	a.settingsSection(c, a.tr("Lokaler MCP-Server (Claude Code, Cursor, Antigravity, Claude Desktop)", "Local MCP Server (Claude Code, Cursor, Antigravity, Claude Desktop)"))
	ui.Column(c.Key("set-mcp-box")).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Text(c, a.tr(
			"Externe KI-Agenten können lokal per stdio (maildiver --mcp) auf Postfach, Kalender & Kontakte zugreifen — ohne offene Ports, ohne direktes Senden/Löschen und ohne S/MIME-/PGP-Entschlüsselung.",
			"External AI agents can query your local mailbox, calendar & contacts over stdio (maildiver --mcp) — zero open ports, never sends or permanently deletes, and never decrypts S/MIME/PGP.",
		)).FontSize(11.5).TextColor(t.TextMuted)
		ui.Segmented(c.Key("set-mcp-mode"), &s.mcpMode,
			a.tr("Nur Lesen", "Read-only"),
			a.tr("+ Entwürfe", "+ Drafts"),
			a.tr("+ Kalender & Sortieren", "+ Calendar & Triage"),
		)
		snippet := mcpConfigSnippet(s.mcpMode)
		ui.Column(c).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Text(c, snippet).FontSize(11).Selectable()
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if ui.Button(c.Key("set-mcp-copy"), a.tr("MCP-Config kopieren", "Copy MCP Config")).Padding(3, 10).Clicked() {
				mygo.Clipboard.WriteText(snippet)
				a.toast(a.tr("MCP-Konfiguration in Zwischenablage kopiert", "MCP config copied to clipboard"), "success")
			}
			cliCmd := "claude mcp add maildiver -- maildiver --mcp"
			if s.mcpMode == 1 {
				cliCmd += " --allow-drafts"
			} else if s.mcpMode == 2 {
				cliCmd += " --allow-writes"
			}
			if ui.Button(c.Key("set-mcp-copy-cli"), a.tr("Claude-CLI-Befehl kopieren", "Copy Claude CLI command")).Padding(3, 10).Clicked() {
				mygo.Clipboard.WriteText(cliCmd)
				a.toast(a.tr("Claude-CLI-Befehl kopiert", "Claude CLI command copied"), "success")
			}
		})
	})
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
	modelsErr *string,
	customMode *bool,
	fallbackPH string,
) {
	wantKey := backendName + "|" + strings.TrimSpace(endpointVal)
	hint, isCLI := ai.CLIModelHints(backendName)
	// Auto-load only on initial display or after switching provider (loadedKey == ""),
	// never on every keystroke while the user types an endpoint URL.
	if !isCLI && *loadedKey == "" && !*loading {
		*loadedKey = wantKey
		a.loadSettingsModels(backendName, endpointVal, models, loadedKey, loading, modelsErr, wantKey)
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
				*loadedKey = wantKey
				a.loadSettingsModels(backendName, endpointVal, models, loadedKey, loading, modelsErr, wantKey)
			}
		}
	})
	if isCLI {
		a.renderCLIModelHintChips(c, keyPrefix+"-cli", modelVal, hint)
	} else if strings.TrimSpace(*modelsErr) != "" {
		ui.Text(c, a.tr("Modelle nicht ladbar: ", "Could not load models: ")+*modelsErr).FontSize(11).TextColor(c.Theme().Warning)
	}
}

func (a *App) loadSettingsModels(backendName, endpoint string, dst *[]string, loadedKey *string, loading *bool, dstErr *string, wantKey string) {
	*loading = true
	*dstErr = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		cfg := a.cfg
		if a.opts.ConfigLoader != nil {
			if loaded, err := a.opts.ConfigLoader(); err == nil {
				cfg = loaded
			}
		}
		ep := strings.TrimSpace(endpoint)
		savedProv := ai.ProviderForBackend(cfg.AI, backendName)
		var list []anyllm.Model
		var err error
		if ep == "" || ep == strings.TrimSpace(savedProv.Endpoint) {
			list, err = ai.ListModelsFor(ctx, cfg.AI, backendName, a.opts.AnyLLMOpts...)
		} else {
			prov := savedProv
			prov.Endpoint = ep
			list, err = ai.ListModels(ctx, prov, a.opts.AnyLLMOpts...)
		}
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
				if err != nil {
					*dstErr = a.errText(err)
				} else {
					*dstErr = ""
				}
			}
		})
	}()
}
