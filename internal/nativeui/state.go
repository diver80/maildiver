package nativeui

// Threading model
// ---------------
// mygo builds the UI on a single UI (main) thread. Every App field is owned by that thread: it
// is read and written only from render callbacks, mygo event callbacks and closures passed to
// emitUpdate (which mygo runs on the UI thread). Background goroutines never touch App fields:
// they work on snapshots taken on the UI thread before they start, obtain the bridge client via
// a.client() (mutex-guarded, returns bridgeclient.ErrOffline instead of nil) and publish results
// with emitUpdate. Methods documented "safe from any goroutine" post themselves to the UI thread;
// all other exported methods must be called on the UI thread.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/copilot"
	"github.com/diver80/omathunder/internal/theme"
	"github.com/diver80/omathunder/internal/triage"
)

const (
	alertMailPrefix      = "omathunder-mail-"
	alertMailBatchPrefix = "omathunder-mail-batch-"
	alertCalPrefix       = "omathunder-cal-"
)

type Options struct {
	DiscoveryPath string
	StartHidden   bool
	DisableTray   bool
	ConfigLoader  func() (config.Config, error)
	ConfigSaver   func(config.Config) error
	Store         *triage.Store
	AnyLLMOpts    []anyllm.Option
	BridgeClient  *bridgeclient.Client // optional pre-wired client for testing
}

type ChatEntry struct {
	ID        string
	Role      string // "user", "assistant"
	Content   string
	Tools     []ToolTrace
	Status    string
	Streaming bool
	Error     string
	ActionIDs []string // pending copilot actions proposed in this turn
}

type ToolTrace struct {
	ID      string
	Name    string
	Summary string
	Done    bool
}

type AlertItem struct {
	ID        string
	Kind      string // "mail", "calendar", "system"
	Title     string
	Subtitle  string
	Body      string
	MessageID string
	CreatedAt time.Time
}

type ExtractedEvent struct {
	HasEvent    bool   `json:"hasEvent"`
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	AllDay      bool   `json:"allDay"`
	Location    string `json:"location"`
	Description string `json:"description"`
}

type ContactProposal struct {
	Extracted        ai.ExtractedContact
	Existing         *bridgeclient.ContactRecord
	Status           string   // "new", "enrich", "complete"
	MissingFieldKeys []string // canonical field keys ("firstName", "lastName", ...)
	MissingFields    []string // localized missing field labels for current UI language
	Applied          bool
	AppliedAction    string // "imported" | "enriched"
	Ignored          bool
}

type toastItem struct {
	msg  string
	kind string
}

// inspectorState is the message inspector (AI results, contact discovery).
type inspectorState struct {
	inspectorMode       string
	inspectorLoading    bool
	inspectorResult     string
	inspectorDraft      string
	inspectorEvent      *ExtractedEvent
	inspectorContacts   []ContactProposal
	inspectorError      string
	inspectorSeq        uint64
	addressBooks        []bridgeclient.AddressBook
	contactsBookLabels  []string
	contactsBookByLabel map[string]string
	selectedContactBook string
	contactsSupported   bool
	contactsBusy        bool
}

// composeState is the compose modal.
type composeState struct {
	composeOpen      bool
	composeReplyTo   string
	composeReplyType string
	composeTo        string
	composeCC        string
	composeSubject   string
	composeBody      string
	composeBusy      bool
	composeConfirm   bool
	composeNonce     string // per compose session; send idempotency key = nonce + content hash
	composeSign      int    // 0: the identity's default in Thunderbird, 1: sign, 2: don't sign
	// scanRunning: a triage scan runs in the host; arch: the "Archive & cleanup" dialog.
	scanRunning bool
	arch        archiveUI
	settings    settingsUI
	// composeReplyEncrypted: the mail being answered is encrypted, so the reply is sent encrypted.
	composeReplyEncrypted bool
}

// eventState is the new-event modal and the calendar picker data.
type eventState struct {
	eventOpen       bool
	eventCalendar   string
	eventCalLabels  []string
	eventCalByLabel map[string]string
	eventTitle      string
	eventStart      string
	eventEnd        string
	eventAllDay     bool
	eventLocation   string
	eventDesc       string
	eventBusy       bool
}

// calDeleteState is the delete-confirmation dialog for calendar events.
type calDeleteState struct {
	calDelOpen  bool
	calDelEvent bridgeclient.CalendarEvent
	calDelBusy  bool
	calDelError string
}

type App struct {
	opts          Options
	cfg           config.Config
	store         *triage.Store
	conn          *connManager
	copilotRunner *copilot.Runner

	// Hooks supplied by the desktop shell (nil in headless tests). All are called on the UI thread.
	updateUI      func(func())
	onTray        func()
	onShowMain    func()
	onHideFly     func()
	onNotify      func(item AlertItem)
	onLangChanged func()

	// Connection & live status
	bridgeOnline         bool
	thunderbirdConnected bool
	bridgeDisabled       bool // discovery failed and config says bridge.enabled=false (contract §9)
	enablingBridge       bool
	readOnly             bool
	lastSync             time.Time
	lastError            string
	lastLoadWarning      string
	loading              bool
	refreshing           bool
	refreshPending       bool

	// Theme & Language (DE/EN)
	activePalette theme.Palette
	themeSource   string
	uiTheme       *uiThemeState
	themeChoices  []themeChoice
	themeLabel    string
	langChoice    string // "auto", "de", "en"
	langLabel     string // "Auto", "Deutsch", "English"
	lang          string // resolved: "de" or "en"
	themeCfg      atomic.Pointer[config.ThemeConfig]

	// Navigation & filters
	navID            string // "focus", "recent", "action", "calendar", or "folder:<id>"
	secViewsOpen     bool
	secFoldersOpen   bool
	secAccountOpen   map[string]*bool // per-account folder sections (folders.go)
	collapsedFolders map[string]bool  // folder tree: collapsed folders by account+path
	searchQuery      string
	selectedAccount  string // "" = All Accounts
	accountLabels    []string
	accountIDByLabel map[string]string
	calDays          int // 7, 14, 30
	showCopilot      bool
	hideSidebar      bool
	notifyEnabled    bool
	sidebarSplit     float32
	mailSplit        float32
	copilotSplit     float32

	// Mailbox & Calendar data
	accounts      []bridgeclient.Account
	folders       []bridgeclient.Folder
	messages      []bridgeclient.MessageHeader
	msgLoadSeq    uint64 // incremented per message-list load; stale responses are dropped
	totalMessages int
	focusUnread   int
	selectedID    string
	selectedMsg   *bridgeclient.MessageDetail
	readerBody    readerBody // selectedMsg's body as Markdown (mailbody.go)
	readerPlain   bool       // show the text version of HTML mails
	threadMsgs    []triage.MessageInput
	calendars     []bridgeclient.Calendar
	events        []bridgeclient.CalendarEvent
	triageState   triage.State
	barSummary    triage.BarSummary

	// Widget states
	mailList    listStateHolder
	widgets     uiWidgetsState
	focusSearch bool
	focusChat   bool
	toasts      []toastItem

	inspectorState
	copilotState
	quickRuleState
	composeState
	eventState
	calDeleteState
	movePickerState

	// Keyboard: "press again to confirm" for key actions on several mails (keymap.go).
	keyArmSig string
	keyArmAt  time.Time

	// Help Modal
	helpOpen  bool
	helpTopic string // "all", "overview", "local", "ai", "rules", "contacts", "shortcuts"

	// Notifications & SSE deduplication (UI thread)
	seenMails      map[string]bool
	notifiedEvents map[string]bool
	recentAlerts   []AlertItem

	// Debounced reloads triggered by SSE events (safe from any goroutine).
	msgReload *debouncer
	calReload *debouncer
}

func NewApp(opts Options) *App {
	if opts.ConfigLoader == nil {
		opts.ConfigLoader = config.Load
	}
	if opts.ConfigSaver == nil {
		opts.ConfigSaver = config.Save
	}
	if opts.Store == nil {
		if st, err := triage.OpenStore(); err == nil {
			opts.Store = st
		}
	}

	cfg, err := opts.ConfigLoader()
	if err != nil {
		cfg = config.Default()
	}

	pal, src := theme.ResolveActive(cfg.Theme)
	choices := buildThemeChoices()
	langChoice := normalizeLangPref(cfg.Language)
	resolvedLang := resolveUILang(langChoice)
	allAcc := allAccountsLabelLang(resolvedLang)

	a := &App{
		opts:             opts,
		cfg:              cfg,
		store:            opts.Store,
		conn:             newConnManager(opts.DiscoveryPath, opts.BridgeClient),
		bridgeOnline:     opts.BridgeClient != nil,
		readOnly:         cfg.Mailbox.ReadOnly,
		activePalette:    pal,
		themeSource:      src,
		uiTheme:          newUIThemeState(pal),
		themeChoices:     choices,
		themeLabel:       matchThemeLabel(cfg.Theme, choices),
		langChoice:       langChoice,
		langLabel:        langLabelForPref(langChoice),
		lang:             resolvedLang,
		navID:            "focus",
		secViewsOpen:     true,
		secFoldersOpen:   true,
		selectedAccount:  "",
		accountLabels:    []string{allAcc},
		accountIDByLabel: map[string]string{allAcc: ""},
		calDays:          14,
		showCopilot:      true,
		notifyEnabled:    true,
		sidebarSplit:     230,
		mailSplit:        370,
		copilotSplit:     0,
		helpTopic:        "all",
		seenMails:        make(map[string]bool),
		notifiedEvents:   make(map[string]bool),
		msgReload:        &debouncer{delay: reloadDebounce, maxDelay: reloadMaxDelay},
		calReload:        &debouncer{delay: reloadDebounce, maxDelay: reloadMaxDelay},
	}
	a.checkedIDs = make(map[string]bool)
	a.lastCheckedIndex = -1
	a.quickRuleAction = "trash"
	a.agentBackend = cfg.AI.Agent.Backend
	a.agentModel = cfg.AI.Agent.Model
	a.pendingActions = make(map[string]*pendingAction)
	tc := cfg.Theme
	a.themeCfg.Store(&tc)
	a.quickRuleActionLabel = quickRuleActionLabelLang(a.quickRuleAction, resolvedLang)
	a.initProvidersFromStatic()
	a.copilotRunner = &copilot.Runner{
		ConfigLoader: opts.ConfigLoader,
		ClientFunc:   a.liveClient,
		Store:        a.store,
		AnyLLMOpts:   opts.AnyLLMOpts,
	}
	return a
}

func (a *App) SetHooks(update func(func()), onTray func(), onShowMain func(), onHideFly func(), onNotify func(AlertItem)) {
	a.updateUI = update
	a.onTray = onTray
	a.onShowMain = onShowMain
	a.onHideFly = onHideFly
	a.onNotify = onNotify
}

// SetLanguageHook registers a callback (UI thread) run after the UI language changed, e.g. to
// update the window title and the application menu.
func (a *App) SetLanguageHook(fn func()) { a.onLangChanged = fn }

// emitUpdate runs fn on the UI thread (asynchronously when a shell is attached). Safe from any
// goroutine; this is the only way background work may change App state.
func (a *App) emitUpdate(fn func()) {
	if a.updateUI != nil {
		a.updateUI(fn)
		return
	}
	if fn != nil {
		fn()
	}
}

// notifyTray refreshes the menu bar title/tooltip (UI thread).
func (a *App) notifyTray() {
	if a.onTray != nil {
		a.onTray()
	}
}

// toast queues an in-window toast (UI thread). All queued toasts are shown on the next frame.
func (a *App) toast(msg, kind string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	a.toasts = append(a.toasts, toastItem{msg: msg, kind: kind})
	if len(a.toasts) > 6 {
		a.toasts = a.toasts[len(a.toasts)-6:]
	}
}

// applyConfig installs a freshly loaded config (UI thread).
func (a *App) applyConfig(cfg config.Config) {
	a.cfg = cfg
	a.readOnly = cfg.Mailbox.ReadOnly
	tc := cfg.Theme
	a.themeCfg.Store(&tc)
	norm := normalizeLangPref(cfg.Language)
	prevLang := a.lang
	a.langChoice = norm
	a.langLabel = langLabelForPref(norm)
	a.lang = resolveUILang(norm)
	if a.lang != prevLang {
		a.languageChanged()
	}
}

func (a *App) SetLanguage(pref string) {
	norm := normalizeLangPref(pref)
	resolved := resolveUILang(norm)
	a.langChoice = norm
	a.langLabel = langLabelForPref(norm)
	a.lang = resolved

	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	cfg.Language = norm
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
	a.languageChanged()
}

// languageChanged re-localizes cached labels after a.lang changed (UI thread).
func (a *App) languageChanged() {
	resolved := a.uiLang()
	a.rebuildAccountLabels()
	a.quickRuleActionLabel = quickRuleActionLabelLang(a.quickRuleAction, resolved)
	for i := range a.inspectorContacts {
		if len(a.inspectorContacts[i].MissingFieldKeys) > 0 {
			lbls := make([]string, len(a.inspectorContacts[i].MissingFieldKeys))
			for j, k := range a.inspectorContacts[i].MissingFieldKeys {
				lbls[j] = contactFieldLabelLang(k, resolved)
			}
			a.inspectorContacts[i].MissingFields = lbls
		}
	}
	a.notifyTray()
	if a.onLangChanged != nil {
		a.onLangChanged()
	}
}

func (a *App) applyLanguageChoice(label string) {
	a.SetLanguage(langPrefForLabel(label))
}

func (a *App) rebuildAccountLabels() {
	allLbl := allAccountsLabelLang(a.uiLang())
	labels := []string{allLbl}
	idMap := map[string]string{allLbl: ""}
	for _, acc := range a.accounts {
		lbl := acc.Name
		if lbl == "" {
			lbl = acc.ID
		}
		labels = append(labels, lbl)
		idMap[lbl] = acc.ID
	}
	a.accountLabels = labels
	a.accountIDByLabel = idMap
}

func matchThemeLabel(tc config.ThemeConfig, choices []themeChoice) string {
	mode := strings.ToLower(strings.TrimSpace(tc.Mode))
	if mode == "auto" || mode == "" {
		return choices[0].Label
	}
	if mode == "omarchy" {
		return choices[1].Label
	}
	for _, c := range choices {
		if c.Mode == "preset" && c.Name == tc.Preset {
			return c.Label
		}
	}
	return choices[0].Label
}

func (a *App) applyThemeChoice(label string) {
	var picked *themeChoice
	for i := range a.themeChoices {
		if a.themeChoices[i].Label == label {
			picked = &a.themeChoices[i]
			break
		}
	}
	if picked == nil {
		return
	}
	a.themeLabel = picked.Label
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	if picked.Mode == "preset" {
		cfg.Theme.Mode = "preset"
		cfg.Theme.Preset = picked.Name
	} else {
		cfg.Theme.Mode = picked.Mode
	}
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
	tc := cfg.Theme
	a.themeCfg.Store(&tc)
	pal, src := theme.ResolveActive(cfg.Theme)
	a.applyPalette(pal, src)
}

// applyPalette installs pal if it differs from the active one (UI thread).
func (a *App) applyPalette(pal theme.Palette, src string) bool {
	if pal.Name == a.activePalette.Name && pal.Background == a.activePalette.Background && pal.Accent == a.activePalette.Accent {
		a.themeSource = src
		return false
	}
	a.activePalette = pal
	a.themeSource = src
	a.uiTheme.setPalette(pal)
	return true
}

// runThemeWatcher resolves the live (Omarchy/system) theme OFF the UI thread (it may shell out
// to `defaults read`) and only posts an update when the palette actually changed.
func (a *App) runThemeWatcher(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	lastKey := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		tc := a.themeCfg.Load()
		if tc == nil || tc.Mode == "preset" {
			lastKey = ""
			continue
		}
		pal, src := theme.ResolveActive(*tc)
		key := pal.Name + "|" + pal.Background + "|" + pal.Accent
		if key == lastKey {
			continue
		}
		lastKey = key
		a.emitUpdate(func() {
			if cur := a.themeCfg.Load(); cur != nil && cur.Mode != "preset" {
				a.applyPalette(pal, src)
			}
		})
	}
}

// refreshSnapshot is the UI state a refresh needs, captured on the UI thread.
type refreshSnapshot struct {
	account string
	calDays int
	msgLoad messageLoad
}

// RefreshAll reloads config, triage state, bridge health, accounts, folders, calendars,
// messages and models. Safe from any goroutine; overlapping calls are coalesced.
func (a *App) RefreshAll(ctx context.Context) {
	a.emitUpdate(func() { a.startRefresh(ctx) })
}

// startRefresh begins a refresh (UI thread).
func (a *App) startRefresh(ctx context.Context) {
	if a.refreshing {
		a.refreshPending = true
		return
	}
	a.refreshing = true
	a.loading = true
	calDays := a.calDays
	if calDays <= 0 {
		calDays = 14
	}
	snap := refreshSnapshot{account: a.selectedAccount, calDays: calDays, msgLoad: a.newMessageLoad()}
	go a.refreshBackground(ctx, snap)
}

// finishRefresh ends a refresh and runs a coalesced follow-up (UI thread).
func (a *App) finishRefresh(ctx context.Context) {
	a.refreshing = false
	a.loading = false
	a.notifyTray()
	if a.refreshPending {
		a.refreshPending = false
		a.startRefresh(ctx)
	}
}

func (a *App) refreshBackground(ctx context.Context, snap refreshSnapshot) {
	cfg, cfgErr := a.opts.ConfigLoader()
	var st triage.State
	stOK := false
	if a.store != nil {
		if s, err := a.store.Load(); err == nil {
			st, stOK = s, true
		}
	}

	bc, health, connErr := a.conn.connect(ctx)
	a.emitUpdate(func() {
		if cfgErr == nil {
			a.applyConfig(cfg)
		}
		if stOK {
			a.triageState = st
			a.barSummary = triage.ComputeBarSummary(st)
		}
		if connErr != nil {
			a.bridgeOnline = false
			a.thunderbirdConnected = false
			a.bridgeDisabled = cfgErr == nil && !cfg.Bridge.Enabled
			a.lastError = a.errText(connErr)
			return
		}
		if !a.conn.isCurrent(bc) {
			// The connection dropped while this refresh ran; the event stream owns that state.
			a.bridgeOnline = false
			a.thunderbirdConnected = false
			return
		}
		a.bridgeOnline = true
		a.bridgeDisabled = false
		a.thunderbirdConnected = health.ThunderbirdConnected
		a.readOnly = health.ReadOnly
		a.lastError = ""
	})
	if connErr != nil {
		a.emitUpdate(func() { a.finishRefresh(ctx) })
		return
	}
	if !health.ThunderbirdConnected {
		a.emitUpdate(func() {
			a.lastSync = time.Now()
			a.finishRefresh(ctx)
		})
		return
	}

	type result struct {
		accounts  []bridgeclient.Account
		folders   []bridgeclient.Folder
		calendars []bridgeclient.Calendar
		events    []bridgeclient.CalendarEvent
		errs      [4]error
	}
	var res result
	done := make(chan struct{}, 4)
	load := func(i int, fn func(c context.Context) error) {
		go func() {
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			res.errs[i] = fn(c)
			done <- struct{}{}
		}()
	}
	load(0, func(c context.Context) (err error) { res.accounts, err = bc.Accounts(c); return })
	load(1, func(c context.Context) (err error) { res.folders, err = bc.Folders(c, snap.account); return })
	load(2, func(c context.Context) (err error) { res.calendars, err = bc.Calendars(c); return })
	load(3, func(c context.Context) error {
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format(time.RFC3339)
		end := time.Date(now.Year(), now.Month(), now.Day()+snap.calDays, 23, 59, 59, 0, now.Location()).Format(time.RFC3339)
		r, err := bc.CalendarEvents(c, start, end, nil, 100)
		if err == nil {
			res.events = r.Events
			if res.events == nil {
				res.events = []bridgeclient.CalendarEvent{}
			}
		}
		return err
	})
	for i := 0; i < 4; i++ {
		<-done
	}

	a.emitUpdate(func() {
		if !a.conn.isCurrent(bc) {
			return
		}
		if res.errs[0] == nil && len(res.accounts) > 0 {
			a.accounts = res.accounts
			a.rebuildAccountLabels()
		}
		if res.errs[1] == nil && res.folders != nil {
			a.folders = res.folders
		}
		if res.errs[2] == nil && res.calendars != nil {
			a.calendars = res.calendars
			var calLabels []string
			calMap := make(map[string]string)
			for _, cal := range res.calendars {
				if cal.ReadOnly {
					continue
				}
				calLabels = append(calLabels, cal.Name)
				calMap[cal.Name] = cal.ID
			}
			a.eventCalLabels = calLabels
			a.eventCalByLabel = calMap
			if _, ok := calMap[a.eventCalendar]; !ok {
				a.eventCalendar = ""
			}
			if a.eventCalendar == "" && len(calLabels) > 0 {
				a.eventCalendar = calLabels[0]
			}
		}
		if res.errs[3] == nil && res.events != nil {
			a.events = res.events
		}
		var warn []string
		for i, label := range [][2]string{{"Konten", "Accounts"}, {"Ordner", "Folders"}, {"Kalender", "Calendars"}, {"Termine", "Events"}} {
			if res.errs[i] != nil {
				warn = append(warn, a.tr(label[0], label[1])+": "+a.errText(res.errs[i]))
			}
		}
		w := strings.Join(warn, " · ")
		if w != "" && w != a.lastLoadWarning {
			a.toast(a.tr("Teilweise geladen — ", "Partially loaded — ")+w, "warning")
		}
		a.lastLoadWarning = w
	})

	a.loadMessagesBackground(ctx, bc, snap.msgLoad)
	a.emitUpdate(func() {
		a.startModelLoad(ctx)
		a.lastSync = time.Now()
		a.finishRefresh(ctx)
	})
}

// messageLoad is one message-list request, captured on the UI thread.
type messageLoad struct {
	seq       uint64
	navID     string
	filter    bridgeclient.MessagesFilter
	actionIDs map[string]bool // triage action/waiting cards for the "action" view
}

// newMessageLoad snapshots the current view into a new load request (UI thread). Every call
// supersedes all earlier ones: their responses are dropped.
func (a *App) newMessageLoad() messageLoad {
	a.msgLoadSeq++
	req := messageLoad{
		seq:   a.msgLoadSeq,
		navID: a.navID,
		filter: bridgeclient.MessagesFilter{
			AccountID: a.selectedAccount,
			Query:     strings.TrimSpace(a.searchQuery),
			Days:      30,
			Limit:     80,
		},
	}
	switch {
	case strings.HasPrefix(req.navID, "folder:"):
		req.filter.FolderID = strings.TrimPrefix(req.navID, "folder:")
	case req.navID == "focus":
		unread := true
		req.filter.Unread = &unread
		req.filter.Days = 14
	case req.navID == "action":
		req.actionIDs = make(map[string]bool)
		for id, rec := range a.triageState.Classifications {
			if rec.KanbanColumn == "action" || rec.KanbanColumn == "waiting" {
				req.actionIDs[id] = true
			}
		}
	}
	return req
}

// LoadMessages reloads the message list of the current view. Safe from any goroutine.
func (a *App) LoadMessages(ctx context.Context) {
	a.emitUpdate(func() { a.startMessageLoad(ctx) })
}

// startMessageLoad starts a message-list load (UI thread).
func (a *App) startMessageLoad(ctx context.Context) {
	if !a.thunderbirdConnected {
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	req := a.newMessageLoad()
	go a.loadMessagesBackground(ctx, bc, req)
}

func (a *App) loadMessagesBackground(ctx context.Context, bc *bridgeclient.Client, req messageLoad) {
	c, cancel := context.WithTimeout(ctx, 12*time.Second)
	page, err := bc.Messages(c, req.filter)
	cancel()
	if err != nil {
		a.emitUpdate(func() {
			if req.seq == a.msgLoadSeq {
				a.lastError = a.errText(err)
			}
		})
		return
	}
	msgs := page.Messages
	if req.navID == "action" {
		filtered := make([]bridgeclient.MessageHeader, 0, len(msgs))
		for _, m := range msgs {
			if req.actionIDs[m.ID] || m.Flagged {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}
	total := page.Total
	a.emitUpdate(func() { a.applyMessageLoad(req, msgs, total) })
}

// applyMessageLoad installs a load result unless a newer load was started (UI thread).
func (a *App) applyMessageLoad(req messageLoad, msgs []bridgeclient.MessageHeader, total int) bool {
	if req.seq != a.msgLoadSeq {
		return false
	}
	// Seed seenMails on the first non-empty fetch so startup doesn't spam notifications.
	if len(a.seenMails) == 0 {
		for _, m := range msgs {
			a.seenMails[m.ID] = true
		}
	}
	a.setMessages(msgs)
	a.totalMessages = total
	if req.navID == "focus" {
		a.focusUnread = len(msgs)
	}
	a.notifyTray()
	return true
}

// setMessages replaces the list (UI thread): the Shift-click anchor is reset and checked IDs
// that are no longer listed are dropped, so range selection can never reach mails the user
// did not see.
func (a *App) setMessages(msgs []bridgeclient.MessageHeader) {
	a.messages = msgs
	a.lastCheckedIndex = -1
	if len(a.checkedIDs) > 0 {
		present := make(map[string]bool, len(msgs))
		for _, m := range msgs {
			present[m.ID] = true
		}
		for id := range a.checkedIDs {
			if !present[id] {
				delete(a.checkedIDs, id)
			}
		}
	}
	a.mailList.selected = -1
	if a.selectedID != "" {
		for i, m := range msgs {
			if m.ID == a.selectedID {
				a.mailList.selected = i
				break
			}
		}
	}
}

// setNav switches the view (UI thread) and resets the multi-select anchor.
func (a *App) setNav(nav string) {
	if a.navID != nav {
		a.navID = nav
	}
	a.lastCheckedIndex = -1
}

// messageIDFor returns the RFC Message-ID of a listed/selected mail ("" if unknown).
func (a *App) messageIDFor(id string) string {
	for _, m := range a.messages {
		if m.ID == id {
			return m.MessageID
		}
	}
	if a.selectedMsg != nil && a.selectedMsg.ID == id {
		return a.selectedMsg.MessageID
	}
	return ""
}

// SelectMessage selects a mail and loads its detail (UI thread).
func (a *App) SelectMessage(ctx context.Context, id string) {
	if id == "" {
		return
	}
	if a.selectedMsg != nil && a.selectedMsg.ID != id {
		a.selectedMsg = nil
	}
	a.selectedID = id
	a.inspectorSeq++
	a.inspectorResult = ""
	a.inspectorDraft = ""
	a.inspectorEvent = nil
	a.inspectorContacts = nil
	a.inspectorError = ""
	a.inspectorMode = ""
	a.inspectorLoading = false
	for i, m := range a.messages {
		if m.ID == id {
			a.mailList.selected = i
			break
		}
	}
	bc, err := a.client()
	if err != nil {
		a.inspectorError = a.errText(err)
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Opening a mail is an explicit action: encrypted mails are decrypted for the reading pane.
		detail, err := bc.MessageWithOptions(c, id, bridgeclient.MessageOptions{Format: "both", Decrypt: true})
		var body readerBody
		if err == nil {
			body = buildReaderBody(&detail) // off the UI thread: large HTML takes a moment
		}
		a.emitUpdate(func() {
			if a.selectedID != id {
				return
			}
			if err != nil {
				a.inspectorError = a.errText(err)
				return
			}
			a.selectedMsg = &detail
			a.readerBody = body
		})
	}()
}

func (a *App) RunInspectorAI(ctx context.Context, mode string) {
	msg := a.selectedMsg
	if msg == nil || a.inspectorLoading {
		return
	}
	lang := a.uiLang()
	backend, model := a.agentBackend, a.agentModel
	selectedBook := a.selectedContactBook
	a.inspectorSeq++
	seq := a.inspectorSeq
	a.inspectorMode = mode
	a.inspectorLoading = true
	a.inspectorResult = ""
	a.inspectorDraft = ""
	a.inspectorEvent = nil
	a.inspectorContacts = nil
	a.inspectorError = ""
	req := ai.AnalyzeRequest{
		Mode:      mode,
		Ephemeral: true,
		Language:  lang,
		PrimaryMessage: triage.MessageInput{
			ID:       msg.ID,
			Subject:  msg.Subject,
			From:     msg.From,
			To:       msg.To,
			Date:     msg.Date,
			BodyText: msg.BodyText,
		},
	}
	bc, _ := a.client()
	go func() {
		// The bridge allows ~110 s for an analysis; don't cut it short on the client.
		c, cancel := context.WithTimeout(ctx, bridgeclient.AIRequestTimeout)
		defer cancel()
		var res ai.AnalyzeResponse
		var err error
		if bc != nil {
			res, err = bc.AIAnalyze(c, req)
		} else {
			cfg, cerr := a.opts.ConfigLoader()
			if cerr != nil {
				cfg = config.Default()
			}
			prov := ai.ProviderForBackend(cfg.AI, backend)
			if model != "" {
				prov.Model = model
			}
			cfg.AI.Agent = prov
			res, err = ai.Analyze(c, cfg.AI, req)
		}
		if err != nil {
			a.emitUpdate(func() {
				if seq != a.inspectorSeq {
					return
				}
				a.inspectorLoading = false
				a.inspectorError = a.errText(err)
			})
			return
		}

		var contactProps []ContactProposal
		var books []bridgeclient.AddressBook
		var bookLabels []string
		bookMap := make(map[string]string)
		contactsSupported := true
		if mode == "extract_contacts" {
			var existing []bridgeclient.ContactRecord
			if bc != nil {
				if sr, sErr := bc.ContactsSearch(c, ""); sErr == nil {
					contactsSupported = sr.Supported
					books = sr.AddressBooks
					existing = sr.Contacts
					for _, b := range books {
						if b.ReadOnly {
							continue
						}
						lbl := b.Name
						if lbl == "" {
							lbl = b.ID
						}
						bookLabels = append(bookLabels, lbl)
						bookMap[lbl] = b.ID
					}
					if selectedBook == "" && len(bookLabels) > 0 {
						selectedBook = bookLabels[0]
					}
				}
			}
			contactProps = buildContactProposalsLang(res.Contacts, existing, lang)
		}

		a.emitUpdate(func() {
			if seq != a.inspectorSeq {
				return
			}
			a.inspectorLoading = false
			a.inspectorResult = res.Answer
			if mode == "draft_reply" {
				if len(res.Proposals) > 0 && strings.TrimSpace(res.Proposals[0].Body) != "" {
					a.inspectorDraft = res.Proposals[0].Body
				} else {
					a.inspectorDraft = res.Answer
				}
			}
			if mode == "extract_events" {
				if len(res.Events) > 0 {
					ev := res.Events[0]
					a.inspectorEvent = &ExtractedEvent{
						HasEvent:    true,
						Title:       ev.Title,
						Start:       ev.Start,
						End:         ev.End,
						AllDay:      ev.AllDay,
						Location:    ev.Location,
						Description: ev.Description,
					}
				} else if parsed := parseExtractedEvent(res.Answer); parsed != nil {
					a.inspectorEvent = parsed
				}
			}
			if mode == "extract_contacts" {
				a.contactsSupported = contactsSupported
				if len(books) > 0 {
					a.addressBooks = books
					a.contactsBookLabels = bookLabels
					a.contactsBookByLabel = bookMap
					a.selectedContactBook = selectedBook
				}
				a.inspectorContacts = contactProps
				if strings.TrimSpace(a.inspectorResult) == "" {
					if len(contactProps) == 0 {
						a.inspectorResult = a.tr("Keine Kontaktdaten im E-Mail-Footer gefunden.", "No contact details found in email footer.")
					} else {
						a.inspectorResult = a.trf(
							"%d Kontakt(e) im E-Mail-Footer erkannt und lokal mit dem Thunderbird-Adressbuch abgeglichen.",
							"%d contact(s) detected in the email footer and matched locally against your Thunderbird address book.",
							len(contactProps),
						)
					}
				}
			}
		})
	}()
}

func buildContactProposals(extracted []ai.ExtractedContact, existing []bridgeclient.ContactRecord) []ContactProposal {
	return buildContactProposalsLang(extracted, existing, "de")
}

func buildContactProposalsLang(extracted []ai.ExtractedContact, existing []bridgeclient.ContactRecord, lang string) []ContactProposal {
	byEmail := make(map[string]bridgeclient.ContactRecord, len(existing)*2)
	byName := make(map[string]bridgeclient.ContactRecord, len(existing))
	for _, c := range existing {
		if em := strings.ToLower(strings.TrimSpace(c.Email)); em != "" {
			byEmail[em] = c
		}
		if em2 := strings.ToLower(strings.TrimSpace(c.SecondEmail)); em2 != "" {
			byEmail[em2] = c
		}
		if dn := strings.ToLower(strings.TrimSpace(c.DisplayName)); dn != "" {
			byName[dn] = c
		}
	}

	out := make([]ContactProposal, 0, len(extracted))
	for _, ex := range extracted {
		prop := ContactProposal{Extracted: ex, Status: "new"}
		var match *bridgeclient.ContactRecord
		if em := strings.ToLower(strings.TrimSpace(ex.Email)); em != "" {
			if found, ok := byEmail[em]; ok {
				cp := found
				match = &cp
			}
		}
		if match == nil {
			if dn := strings.ToLower(strings.TrimSpace(ex.DisplayName)); dn != "" {
				if found, ok := byName[dn]; ok {
					cp := found
					match = &cp
				}
			}
		}
		if match != nil {
			prop.Existing = match
			keys, labels := missingContactFieldPairs(ex, *match, lang)
			prop.MissingFieldKeys = keys
			prop.MissingFields = labels
			if len(keys) > 0 {
				prop.Status = "enrich"
			} else {
				prop.Status = "complete"
			}
		}
		out = append(out, prop)
	}
	return out
}

func missingContactFieldPairs(ex ai.ExtractedContact, cur bridgeclient.ContactRecord, lang string) (keys, labels []string) {
	check := func(newVal, curVal, key string) {
		if strings.TrimSpace(newVal) != "" && strings.TrimSpace(curVal) == "" {
			keys = append(keys, key)
			labels = append(labels, contactFieldLabelLang(key, lang))
		}
	}
	check(ex.FirstName, cur.FirstName, "firstName")
	check(ex.LastName, cur.LastName, "lastName")
	check(ex.Phone, cur.Phone, "phone")
	check(ex.Mobile, cur.Mobile, "mobile")
	check(ex.Company, cur.Company, "company")
	check(ex.JobTitle, cur.JobTitle, "jobTitle")
	check(ex.Website, cur.Website, "website")
	check(ex.Address, cur.Address, "address")
	return keys, labels
}

func (a *App) ApplyContactProposal(ctx context.Context, idx int) {
	if idx < 0 || idx >= len(a.inspectorContacts) || a.contactsBusy {
		return
	}
	prop := a.inspectorContacts[idx]
	if prop.Applied || prop.Status == "complete" {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Read-Only oder Offline: Kontakt nicht gespeichert", "Read-only or offline: contact not saved"), "error")
		return
	}
	bookID := a.contactsBookByLabel[a.selectedContactBook]
	req := bridgeclient.ContactUpsertRequest{
		AddressBookID: bookID,
		Mode:          "create",
		Contact: bridgeclient.ContactRecord{
			DisplayName: prop.Extracted.DisplayName,
			FirstName:   prop.Extracted.FirstName,
			LastName:    prop.Extracted.LastName,
			Email:       prop.Extracted.Email,
			Phone:       prop.Extracted.Phone,
			Mobile:      prop.Extracted.Mobile,
			Company:     prop.Extracted.Company,
			JobTitle:    prop.Extracted.JobTitle,
			Website:     prop.Extracted.Website,
			Address:     prop.Extracted.Address,
			Notes:       prop.Extracted.Notes,
		},
	}
	if prop.Existing != nil && prop.Existing.ID != "" {
		req.ContactID = prop.Existing.ID
		req.Mode = "enrich"
	}
	seq := a.inspectorSeq
	a.contactsBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		res, err := bc.ContactUpsert(c, req)
		a.emitUpdate(func() {
			a.contactsBusy = false
			if err != nil {
				a.toast(a.tr("Kontakt-Fehler: ", "Contact error: ")+a.errText(err), "error")
				return
			}
			if seq == a.inspectorSeq && idx < len(a.inspectorContacts) {
				a.inspectorContacts[idx].Applied = true
				a.inspectorContacts[idx].AppliedAction = res.Action
				if a.inspectorContacts[idx].AppliedAction == "" {
					if req.Mode == "enrich" {
						a.inspectorContacts[idx].AppliedAction = "enriched"
					} else {
						a.inspectorContacts[idx].AppliedAction = "imported"
					}
				}
			}
			if req.Mode == "enrich" {
				a.toast(a.tr("Kontaktdaten in Thunderbird vervollständigt", "Contact details updated in Thunderbird"), "success")
			} else {
				a.toast(a.tr("Kontakt in Thunderbird importiert", "Contact imported into Thunderbird"), "success")
			}
		})
	}()
}

func (a *App) DismissContactProposal(idx int) {
	if idx < 0 || idx >= len(a.inspectorContacts) {
		return
	}
	a.inspectorContacts[idx].Ignored = true
}

func parseExtractedEvent(raw string) *ExtractedEvent {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "{"); idx >= 0 {
		if end := strings.LastIndex(s, "}"); end > idx {
			s = s[idx : end+1]
		}
	}
	var ev ExtractedEvent
	if err := json.Unmarshal([]byte(s), &ev); err != nil {
		return nil
	}
	if !ev.HasEvent || ev.Title == "" || ev.Start == "" {
		return nil
	}
	return &ev
}

// SaveDraft saves a draft or (OpenWindow) opens a compose window in Thunderbird (UI thread).
// Opening a compose window is user-visible and allowed in read-only mode (contract §5).
func (a *App) SaveDraft(ctx context.Context, req bridgeclient.ComposeRequest, successMsg string) {
	if a.composeBusy {
		return
	}
	bc, err := a.client()
	if err != nil || (a.readOnly && !req.OpenWindow) {
		a.toast(a.tr("Read-Only oder Offline: Entwurf nicht gespeichert", "Read-only or offline: draft not saved"), "error")
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = bridgeclient.NewIdempotencyKey()
	}
	a.composeBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		raw, err := bc.ComposeDraft(c, req)
		a.emitUpdate(func() {
			a.composeBusy = false
			if err != nil {
				a.toast(a.tr("Fehler: ", "Error: ")+a.errText(err), "error")
				return
			}
			a.composeOpen = false
			if res := bridgeclient.ParseComposeResult(raw); res.Reason == "encrypted_reply" && !req.OpenWindow {
				a.toast(a.tr("Antwort auf eine verschlüsselte Mail: in Thunderbird geöffnet statt still gespeichert, damit sie verschlüsselt bleibt",
					"Reply to an encrypted email: opened in Thunderbird instead of saved silently, so it stays encrypted"), "success")
				return
			}
			a.toast(successMsg, "success")
		})
	}()
}

// SendCompose sends the compose modal content directly (UI thread). The idempotency key is
// derived from the compose session and the content: a retry of the same content can never
// send twice, while edited content gets a new key.
func (a *App) SendCompose(ctx context.Context, req bridgeclient.ComposeRequest) {
	if a.composeBusy {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Senden nicht erlaubt (Read-Only oder Offline)", "Sending not allowed (read-only or offline)"), "error")
		return
	}
	if a.composeNonce == "" {
		a.composeNonce = bridgeclient.NewIdempotencyKey()
	}
	req.IdempotencyKey = bridgeclient.ContentIdempotencyKey(a.composeNonce, "send",
		strings.Join(req.To, ","), strings.Join(req.CC, ","), req.Subject, req.Body, req.ReplyToMessageID, req.ReplyType, req.Sign)
	a.composeBusy = true
	go func() {
		// Signing may wait for a key password or smartcard PIN in Thunderbird.
		c, cancel := context.WithTimeout(ctx, bridgeclient.ComposeSendTimeout)
		defer cancel()
		raw, err := bc.ComposeSend(c, req)
		a.emitUpdate(func() {
			a.composeBusy = false
			if err != nil {
				if !bridgeclient.IsOutcomeUnknown(err) {
					// Definitive failure: a retry must really send again.
					a.composeNonce = bridgeclient.NewIdempotencyKey()
				}
				a.toast(a.tr("Senden fehlgeschlagen: ", "Send failed: ")+a.errText(err), "error")
				return
			}
			a.composeOpen = false
			a.composeConfirm = false
			a.toast(a.tr("Nachricht direkt über Thunderbird gesendet", "Message sent directly via Thunderbird")+
				a.composeSecuritySuffix(bridgeclient.ParseComposeResult(raw).Security), "success")
		})
	}()
}

// OpenInThunderbird shows a mail in Thunderbird, e.g. to check its signature (UI thread).
func (a *App) OpenInThunderbird(ctx context.Context, id string) {
	bc, err := a.client()
	if err != nil || id == "" {
		a.toast(a.tr("Thunderbird ist nicht verbunden", "Thunderbird is not connected"), "error")
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := bc.OpenMessage(c, id)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Fehler: ", "Error: ")+a.errText(err), "error")
			}
		})
	}()
}

// PerformMessageAction applies one mail action (UI thread). The current Message-ID is sent as
// expectedMessageId so a stale list can't hit a different mail.
func (a *App) PerformMessageAction(ctx context.Context, id string, patch bridgeclient.MessageActionsRequest, toastMsg string) {
	a.performMessageAction(ctx, id, patch, toastMsg, "")
}

// performMessageAction is PerformMessageAction; openNext, when set, is opened after the mail
// left the list (trash/move) while it was still the open one, so triage continues with its
// neighbour.
func (a *App) performMessageAction(ctx context.Context, id string, patch bridgeclient.MessageActionsRequest, toastMsg, openNext string) {
	if id == "" {
		return
	}
	if a.readOnly {
		a.toast(a.tr("Read-Only-Modus aktiv — Änderung blockiert", "Read-only mode active — change blocked"), "error")
		return
	}
	bc, err := a.client()
	if err != nil {
		a.toast(a.tr("Aktion fehlgeschlagen: ", "Action failed: ")+a.errText(err), "error")
		return
	}
	if patch.ExpectedMessageID == "" {
		patch.ExpectedMessageID = a.messageIDFor(id)
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		_, err := bc.MessageActions(c, id, patch)
		cancel()
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Aktion fehlgeschlagen: ", "Action failed: ")+a.errText(err), "error")
				return
			}
			removed := patch.RemovesFromFolder()
			if removed && a.selectedID == id {
				a.selectedID = ""
				a.selectedMsg = nil
				if openNext != "" {
					a.SelectMessage(ctx, openNext)
				}
			}
			a.toast(toastMsg, "success")
			a.startMessageLoad(ctx)
			if !removed && a.selectedID == id {
				a.SelectMessage(ctx, id)
			}
		})
	}()
}

func (a *App) CreateCalendarEvent(ctx context.Context, req bridgeclient.CreateCalendarEventRequest) {
	if a.eventBusy {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Read-Only oder Offline: Termin nicht angelegt", "Read-only or offline: event not created"), "error")
		return
	}
	a.eventBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ev, err := bc.CreateCalendarEvent(c, req)
		a.emitUpdate(func() {
			a.eventBusy = false
			if err != nil {
				a.toast(a.tr("Termin-Fehler: ", "Event error: ")+a.errText(err), "error")
				return
			}
			a.eventOpen = false
			a.toast(a.trf("Termin „%s“ angelegt", "Created event \"%s\"", ev.Title), "success")
			a.startRefresh(ctx)
		})
	}()
}

// RequestDeleteCalendarEvent opens the delete confirmation for ev (UI thread).
func (a *App) RequestDeleteCalendarEvent(ev bridgeclient.CalendarEvent) {
	if a.readOnly || ev.ReadOnly || a.calDelBusy {
		return
	}
	a.calDelEvent = ev
	a.calDelError = ""
	a.calDelOpen = true
}

// ConfirmDeleteCalendarEvent deletes the event pending confirmation (UI thread). scope is
// bridgeclient.ScopeOccurrence or ScopeSeries; for non-recurring events it is ignored.
func (a *App) ConfirmDeleteCalendarEvent(ctx context.Context, scope string) {
	if !a.calDelOpen || a.calDelBusy {
		return
	}
	ev := a.calDelEvent
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.calDelError = a.tr("Read-Only oder Offline: Termin nicht gelöscht", "Read-only or offline: event not deleted")
		return
	}
	recurrenceID := ""
	if ev.IsRecurring || ev.RecurrenceID != "" {
		if scope == bridgeclient.ScopeOccurrence {
			recurrenceID = ev.RecurrenceID
		}
	} else {
		scope = ""
	}
	a.calDelBusy = true
	a.calDelError = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err := bc.DeleteCalendarEvent(c, ev.CalendarID, ev.ID, recurrenceID, scope)
		a.emitUpdate(func() {
			a.calDelBusy = false
			if err != nil {
				a.calDelError = a.tr("Löschen fehlgeschlagen: ", "Delete failed: ") + a.errText(err)
				return
			}
			a.calDelOpen = false
			a.toast(a.trf("Termin „%s“ gelöscht", "Deleted event \"%s\"", ev.Title), "success")
			a.startRefresh(ctx)
		})
	}()
}

func (a *App) OpenComposeNew() {
	a.composeReplyTo = ""
	a.composeReplyType = ""
	a.composeTo = ""
	a.composeCC = ""
	a.composeSubject = ""
	a.composeBody = ""
	a.composeConfirm = false
	a.composeNonce = bridgeclient.NewIdempotencyKey()
	a.composeSign = 0
	a.composeReplyEncrypted = false
	a.composeOpen = true
}

func (a *App) OpenComposeReply(msg *bridgeclient.MessageDetail, body string) {
	if msg == nil {
		return
	}
	a.composeReplyTo = msg.ID
	a.composeReplyType = "reply"
	a.composeTo = msg.From
	a.composeCC = ""
	subj := msg.Subject
	if !strings.HasPrefix(strings.ToLower(subj), "re:") {
		subj = "Re: " + subj
	}
	a.composeSubject = subj
	a.composeBody = body
	a.composeConfirm = false
	a.composeNonce = bridgeclient.NewIdempotencyKey()
	a.composeSign = 0
	a.composeReplyEncrypted = msg.Security.Encrypted
	a.composeOpen = true
}

// composeSecuritySuffix names what Thunderbird applied to a sent mail: " (signiert, verschlüsselt)".
func (a *App) composeSecuritySuffix(sec *bridgeclient.ComposeSecurity) string {
	if sec == nil {
		return ""
	}
	var parts []string
	if sec.Signed {
		parts = append(parts, a.tr("signiert", "signed"))
	}
	if sec.Encrypted {
		parts = append(parts, a.tr("verschlüsselt", "encrypted"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (a *App) OpenNewEventModal(prefill *ExtractedEvent) {
	now := time.Now().Add(time.Hour).Truncate(time.Hour)
	a.eventTitle = ""
	a.eventStart = now.Format("2006-01-02T15:04")
	a.eventEnd = now.Add(time.Hour).Format("2006-01-02T15:04")
	a.eventAllDay = false
	a.eventLocation = ""
	a.eventDesc = ""
	if prefill != nil {
		a.eventTitle = prefill.Title
		if prefill.Start != "" {
			a.eventStart = strings.TrimSuffix(prefill.Start, "Z")
			if len(a.eventStart) > 16 {
				a.eventStart = a.eventStart[:16]
			}
		}
		if prefill.End != "" {
			a.eventEnd = strings.TrimSuffix(prefill.End, "Z")
			if len(a.eventEnd) > 16 {
				a.eventEnd = a.eventEnd[:16]
			}
		}
		a.eventAllDay = prefill.AllDay
		a.eventLocation = prefill.Location
		a.eventDesc = prefill.Description
	}
	if a.eventCalendar == "" && len(a.eventCalLabels) > 0 {
		a.eventCalendar = a.eventCalLabels[0]
	}
	a.eventOpen = true
}

// StartEventLoop starts the bridge event stream (live refresh + notifications), the meeting
// reminder ticker and the live theme watcher. Safe from any goroutine.
func (a *App) StartEventLoop(ctx context.Context) {
	go a.runThemeWatcher(ctx, 4*time.Second)
	go func() {
		reminderTicker := time.NewTicker(60 * time.Second)
		defer reminderTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				a.msgReload.stop()
				a.calReload.stop()
				return
			case <-reminderTicker.C:
				a.emitUpdate(a.checkUpcomingMeetingReminders)
			}
		}
	}()
	go a.runEventStream(ctx)
}

// checkUpcomingMeetingReminders raises alerts for meetings starting within ~10 minutes (UI thread).
func (a *App) checkUpcomingMeetingReminders() {
	if !a.notifyEnabled || len(a.events) == 0 {
		return
	}
	now := time.Now()
	for _, ev := range a.events {
		if ev.AllDay || ev.ID == "" {
			continue
		}
		start, err := time.Parse(time.RFC3339, ev.Start)
		if err != nil {
			continue
		}
		diff := start.Sub(now)
		key := ev.ID + "|" + ev.RecurrenceID + "|" + ev.Start
		if diff > 0 && diff <= 11*time.Minute && !a.notifiedEvents[key] {
			a.notifiedEvents[key] = true
			mins := int(diff.Minutes()) + 1
			sub := a.trf("In %d Min (%s)", "In %d min (%s)", mins, start.Local().Format("15:04"))
			if ev.Location != "" {
				sub += " · " + ev.Location
			}
			a.pushAlert(AlertItem{
				ID:        alertCalPrefix + ev.ID,
				Kind:      "calendar",
				Title:     ev.Title,
				Subtitle:  sub,
				Body:      ev.Calendar,
				CreatedAt: now,
			})
		}
	}
}

// pushAlert records an alert and forwards it to the native notification hook (UI thread).
func (a *App) pushAlert(item AlertItem) {
	a.recentAlerts = append([]AlertItem{item}, a.recentAlerts...)
	if len(a.recentAlerts) > 12 {
		a.recentAlerts = a.recentAlerts[:12]
	}
	if a.onNotify != nil {
		a.onNotify(item)
	}
}

// HandleNotificationClick navigates to what a notification is about (UI thread).
func (a *App) HandleNotificationClick(id string) {
	ctx := context.Background()
	if a.onShowMain != nil {
		a.onShowMain()
	}
	switch {
	case strings.HasPrefix(id, alertMailBatchPrefix):
		// A batch alert stands for several mails: show the focus list, select nothing.
		a.setNav("focus")
		a.startMessageLoad(ctx)
	case strings.HasPrefix(id, alertMailPrefix):
		msgID := strings.TrimPrefix(id, alertMailPrefix)
		a.setNav("recent")
		a.startMessageLoad(ctx)
		a.SelectMessage(ctx, msgID)
	case strings.HasPrefix(id, alertCalPrefix):
		a.setNav("calendar")
	}
}

func (a *App) triageColumnFor(id string) string {
	if a.triageState.Classifications == nil {
		return ""
	}
	if rec, ok := a.triageState.Classifications[id]; ok {
		return rec.KanbanColumn
	}
	return ""
}

func (a *App) unreadCount() int {
	n := 0
	for _, m := range a.messages {
		if m.Unread {
			n++
		}
	}
	if a.navID != "focus" && a.focusUnread > n {
		return a.focusUnread
	}
	return n
}

func (a *App) todayEvents() []bridgeclient.CalendarEvent {
	now := time.Now()
	y, m, d := now.Date()
	var out []bridgeclient.CalendarEvent
	for _, ev := range a.events {
		t, err := parseEventTime(ev.Start)
		if err != nil {
			continue
		}
		lt := t.Local()
		ey, em, ed := lt.Date()
		if ey == y && em == m && ed == d {
			out = append(out, ev)
		}
	}
	return out
}

// TrayTitle returns the compact status string rendered beside the menu bar icon (UI thread).
func (a *App) TrayTitle() string {
	if !a.bridgeOnline || !a.thunderbirdConnected {
		return "○"
	}
	unread := a.unreadCount()
	action := a.barSummary.ActionCount
	if unread > 0 && action > 0 {
		return fmt.Sprintf("● %d · %d", unread, action)
	}
	if unread > 0 {
		return fmt.Sprintf("● %d", unread)
	}
	return "●"
}

// TrayTooltip returns a rich status summary tooltip for the menu bar icon (UI thread).
func (a *App) TrayTooltip() string {
	if !a.bridgeOnline {
		return a.tr("OmaThunder — Thunderbird Bridge offline", "OmaThunder — Thunderbird bridge offline")
	}
	if !a.thunderbirdConnected {
		return a.tr("OmaThunder — Warte auf Thunderbird-Erweiterung", "OmaThunder — Waiting for Thunderbird extension")
	}
	today := a.todayEvents()
	return a.trf(
		"OmaThunder Live · %d ungelesen · %d Action · %d Termine heute",
		"OmaThunder Live · %d unread · %d action · %d events today",
		a.unreadCount(), a.barSummary.ActionCount, len(today),
	)
}

func parseEventTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04", raw); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", raw)
}

func shortSender(from string) string {
	s := strings.TrimSpace(from)
	if idx := strings.Index(s, "<"); idx > 0 {
		name := strings.Trim(strings.TrimSpace(s[:idx]), `"`)
		if name != "" {
			return name
		}
	}
	return strings.Trim(s, "<>")
}
