package nativeui

// Threading model
// ---------------
// mygo builds the UI on a single UI (main) thread. Every App field is owned by that thread: it
// is read and written only from render callbacks, mygo event callbacks and closures passed to
// emitUpdate (which mygo runs on the UI thread). Background goroutines never touch App fields:
// they work on snapshots taken on the UI thread before they start, obtain the bridge client via
// a.client() (mutex-guarded, returns backend.ErrOffline instead of nil) and publish results
// with emitUpdate. Methods documented "safe from any goroutine" post themselves to the UI thread;
// all other exported methods must be called on the UI thread.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/copilot"
	"github.com/diver80/maildiver/internal/mailengine"
	"github.com/diver80/maildiver/internal/theme"
	"github.com/diver80/maildiver/internal/triage"
)

const (
	alertMailPrefix      = "maildiver-mail-"
	alertMailBatchPrefix = "maildiver-mail-batch-"
	alertCalPrefix       = "maildiver-cal-"
)

type Options struct {
	StartHidden  bool
	DisableTray  bool
	ConfigLoader func() (config.Config, error)
	ConfigSaver  func(config.Config) error
	Store        *triage.Store
	AnyLLMOpts   []anyllm.Option
	// Backend is the in-process access to the mail engine, the AI layer and the jobs.
	Backend       *backend.Client
	Direct        DirectController // account setup, Google/iCal, contacts, Thunderbird import
	InitialMailto string           // optional mailto: URL passed on launch
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
	Existing         *backend.ContactRecord
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
	addressBooks        []backend.AddressBook
	contactsBookLabels  []string
	contactsBookByLabel map[string]string
	selectedContactBook string
	contactsSupported   bool
	contactsBusy        bool
}

// FilePickRequest describes a native OS open/save file dialog request.
type FilePickRequest struct {
	Mode        string // "open", "open-multi", "dir", "save"
	Title       string
	DefaultPath string
	FilterName  string
	Extensions  []string
	ShowHidden  bool
}

// composeState is the compose modal.
type composeState struct {
	composeOpen bool
	// composeBaseline is the compose content as opened; closing with other content asks first
	// (composeCloseAsk) instead of losing it.
	composeBaseline          string
	composeCloseAsk          bool
	composeAccountID         string
	composeAccountLabel      string
	composeIdentityID        string
	composeFromEmail         string
	composeFromName          string
	composeDraftID           string
	composeReplyTo           string
	composeReplyType         string
	composeInReplyTo         string
	composeReferences        string
	composeTo                string
	composeCC                string
	composeBCC               string
	composeSubject           string
	composeBody              string
	composeBodyHTML          string
	composeEditor            ComposeEditorHost
	composeEditorGen         int
	composeEditorLoadedGen   int
	composeEditorDirty       bool
	composeSyncSeq           uint64
	composeForwardHTML       string
	composeForwardMsgID      string
	composeForwardFrom       string
	composeForwardDate       string
	composeForwardSubj       string
	composeForwardSep        string
	composeKeepHTML          bool
	composeAttachments       []string
	composeSourceAttachments []backend.SourceAttachment
	composeAttachInput       string
	composeSuggestTo         []mailengine.RecipientSuggestion
	composeSuggestCC         []mailengine.RecipientSuggestion
	composeSuggestBCC        []mailengine.RecipientSuggestion
	composeLastToQuery       string
	composeLastCCQuery       string
	composeLastBCCQuery      string
	composeContactPickerOpen bool
	composeContactFilter     string
	composeBusy              bool
	composeConfirm           bool
	composeNonce             string // per compose session; send idempotency key = nonce + content hash
	composeSign              int    // 0: default, 1: sign, 2: don't sign
	composeDirtyAt           time.Time
	composePrevSnap          string
	composeAutoSaveLast      time.Time
	composeAutoSaveSavedAt   time.Time
	composeAutoSaveBusy      bool
	composeAutoSaveSnap      string
	composeMissingAttWarn    string
	composeMissingAttAck     bool
	composeTplOpen           bool
	composeTplName           string
	// scanRunning: a triage scan runs in the host; arch: the "Archive & cleanup" dialog.
	scanRunning bool
	arch        archiveUI
	settings    settingsUI
	// markReadSeq identifies the latest opening of a mail, so a delayed "mark read" from an
	// earlier one is dropped.
	markReadSeq int
	// conversation is the open mail's conversation in the reading pane.
	conversation conversationUI
	// composeEncrypt: S/MIME encrypt outgoing mail.
	composeEncrypt bool
	// composeReplyEncrypted: the mail being answered is encrypted, so the reply is sent encrypted.
	composeReplyEncrypted bool
}

// eventState is the create/edit event modal and the calendar picker data.
type eventState struct {
	// evDetailOpen/evDetail: read-only event details modal (from month/week chips).
	evDetailOpen bool
	evDetail     backend.CalendarEvent
	// calExpanded: agenda rows expanded inline, keyed by calEventKey.
	calExpanded     map[string]bool
	eventOpen       bool
	eventEditingID  string // non-empty when editing an existing event
	eventEditCalID  string // calendar ID of the event being edited
	eventEditingEv  backend.CalendarEvent
	eventCalendar   string
	eventCalLabels  []string
	eventCalByLabel map[string]string
	eventTitle      string
	eventStart      string
	eventEnd        string
	eventAllDay     bool
	eventLocation   string
	eventDesc       string
	eventRecurrence int    // 0: none, 1: daily, 2: weekly, 3: monthly, 4: yearly
	eventRecCount   string // optional COUNT (e.g. "10")
	eventRecUntil   string // optional UNTIL (YYYY-MM-DD)
	eventBusy       bool
}

// calDeleteState is the delete-confirmation dialog for calendar events.
type calDeleteState struct {
	calDelOpen  bool
	calDelEvent backend.CalendarEvent
	calDelBusy  bool
	calDelError string
}

type App struct {
	opts          Options
	cfg           config.Config
	store         *triage.Store
	copilotRunner *copilot.Runner

	// Hooks supplied by the desktop shell (nil in headless tests). All are called on the UI thread.
	updateUI           func(func())
	onTray             func()
	onShowMain         func()
	onHideFly          func()
	onNotify           func(item AlertItem)
	onLangChanged      func()
	onPickFile         func(req FilePickRequest, onDone func(paths []string))
	onPrint            func(title, htmlDoc string)
	detachedAuxWindows bool
	onSyncAuxWindows   func()
	composeWindowSeq   uint64
	eventWindowSeq     uint64

	// Connection & live status
	engineReady     bool
	readOnly        bool
	lastSync        time.Time
	lastError       string
	phone           phoneUI           // the phone layout (phone.go)
	winW, winH      float32           // the main window's size, as last built
	syncErrors      map[string]string // account ID → last sync error, until it syncs again
	lastLoadWarning string
	loading         bool
	refreshing      bool
	refreshPending  bool

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
	navID              string // "focus", "recent", "action", "calendar", "contacts", or "folder:<id>"
	secViewsOpen       bool
	secFoldersOpen     bool
	secAccountOpen     map[string]*bool // per-account folder sections (folders.go)
	collapsedFolders   map[string]bool  // folder tree: collapsed folders by account+path
	folderCollapseSeen map[string]bool  // folder keys whose startup collapse default was applied
	searchQuery        string
	lastSearchQuery    string
	selectedAccount    string // "" = All Accounts
	accountLabels      []string
	accountIDByLabel   map[string]string
	calDays            int             // 7, 14, 30 (Agenda mode)
	calViewMode        int             // 0 = Month Grid, 1 = Week Grid, 2 = Agenda
	calCursor          time.Time       // reference date for Month/Week navigation
	calSearchQuery     string          // top-bar search query when viewing Calendar
	lastCalSearch      string          // tracks empty <-> non-empty widened fetch transitions
	hiddenCalendars    map[string]bool // calendar IDs toggled off in multi-select filter
	hiddenCalsLoaded   bool            // whether hiddenCalendars was initialized from config.json
	calFilterOpen      bool            // multi-select calendar popover state
	showCopilot        bool
	hideSidebar        bool
	notifyEnabled      bool
	sidebarSplit       float32
	mailSplit          float32
	copilotSplit       float32
	lastLayoutKey      string
	lastLayoutSaveAt   time.Time

	// Mailbox & Calendar data
	accounts      []backend.Account
	folders       []backend.Folder
	messages      []backend.MessageHeader
	outboxItems   []backend.OutboxEntry
	outboxOpen    bool
	outboxBusy    bool
	msgLoadSeq    uint64 // incremented per message-list load; stale responses are dropped
	totalMessages int
	// Paging: nextCursor continues the list (empty at its end); listKey names the view the list
	// shows, so a reload of the same view keeps the pages already loaded.
	nextCursor  string
	loadingMore bool
	listKey     string
	focusUnread int
	selectedID  string
	selectedMsg *backend.MessageDetail
	readerBody  readerBody // selectedMsg's body as Markdown (mailbody.go)
	readerPlain bool       // show the text version of HTML mails

	// Reader image state (mutex-guarded because markdown.Options.LoadImage runs on worker goroutines)
	readerImgMu      sync.RWMutex
	readerImgMsgID   string
	readerImgAllowed map[string]bool
	readerImgCache   map[string]ui.ImageSource

	// Server-side IMAP search state
	serverSearchBusy      bool
	serverSearchDoneQuery string
	serverSearchDoneKey   string
	serverSearchCount     int

	// Reversible action Undo state (⌘Z) and timed Undo Send
	undo        *undoAction
	undoBusy    bool
	pendingSend *pendingComposeSend
	attPreview  attachmentPreviewState

	// Pending mailto: URI received before UI finished startup
	pendingMailto string
	threadMsgs    []triage.MessageInput
	calendars     []backend.Calendar
	events        []backend.CalendarEvent
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
	direct   directUI
	contacts contactsUI
	preview  webPreviewState

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
		engineReady:      opts.Backend != nil,
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
		calViewMode:      0,
		calCursor:        time.Now(),
		showCopilot:      cfg.App.ShowCopilot == nil || *cfg.App.ShowCopilot,
		hideSidebar:      cfg.App.HideSidebar != nil && *cfg.App.HideSidebar,
		notifyEnabled:    cfg.App.NotifyEnabled == nil || *cfg.App.NotifyEnabled,
		sidebarSplit:     max(float32(180), cfg.App.SidebarSplit),
		mailSplit:        max(float32(280), cfg.App.MailSplit),
		copilotSplit:     cfg.App.CopilotSplit,
		helpTopic:        "all",
		seenMails:        make(map[string]bool),
		syncErrors:       make(map[string]string),
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
	if strings.TrimSpace(opts.InitialMailto) != "" {
		a.pendingMailto = strings.TrimSpace(opts.InitialMailto)
	}
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

// SetFilePickerHook registers the native OS file/folder dialog callback supplied by desktopShell.
func (a *App) SetFilePickerHook(fn func(req FilePickRequest, onDone func(paths []string))) {
	a.onPickFile = fn
}

// SetPrintHook registers the native print callback supplied by desktopShell.
func (a *App) SetPrintHook(fn func(title, htmlDoc string)) {
	a.onPrint = fn
}

// SetAuxWindowHook enables detached native desktop windows for Compose and Calendar Event
// dialogs so users can keep reading mails and browsing the calendar alongside them.
func (a *App) SetAuxWindowHook(fn func()) {
	a.detachedAuxWindows = fn != nil
	a.onSyncAuxWindows = fn
}

func (a *App) syncAuxWindows() {
	if a.onSyncAuxWindows != nil {
		a.onSyncAuxWindows()
	}
}

// ComposeWindowTitle returns the localized title for the Compose dialog / window.
func (a *App) ComposeWindowTitle() string {
	switch a.composeReplyType {
	case "reply":
		return a.tr("Antwort verfassen", "Compose Reply")
	case "replyAll":
		return a.tr("Allen antworten", "Reply All")
	case "forward":
		return a.tr("Weiterleiten", "Forward Email")
	case "draft":
		return a.tr("Entwurf weiterbearbeiten", "Edit Draft")
	default:
		if a.composeReplyTo != "" {
			return a.tr("Antwort verfassen", "Compose Reply")
		}
		return a.tr("Neue E-Mail", "New Email")
	}
}

// EventWindowTitle returns the localized title for the Calendar Event create/edit window.
func (a *App) EventWindowTitle() string {
	if a.eventEditingID != "" {
		return a.tr("Termin bearbeiten", "Edit Calendar Event")
	}
	return a.tr("Neuer Kalendereintrag", "New Calendar Event")
}

// TryCloseComposeWindow attempts to close Compose; if unsaved edits exist, it keeps Compose
// open with the confirmation banner and returns false.
func (a *App) TryCloseComposeWindow() bool {
	if !a.composeOpen {
		return true
	}
	if a.composeBaseline != "" && a.composeSnapshot() != a.composeBaseline {
		a.composeCloseAsk = true
		return false
	}
	a.closeCompose()
	return true
}

// CloseEventDialog closes the Calendar Event create/edit modal or detached window.
func (a *App) CloseEventDialog() {
	a.eventOpen = false
	a.syncAuxWindows()
}

// pickFile opens a native OS file dialog when a desktop shell is attached.
func (a *App) pickFile(req FilePickRequest, onDone func(paths []string)) {
	if a.onPickFile == nil {
		a.toast(a.tr("Dateidialog im aktuellen Modus nicht verfügbar — bitte Pfad direkt eingeben.", "File dialog unavailable in current mode — please type path directly."), "info")
		return
	}
	a.onPickFile(req, onDone)
}

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
	if !a.hiddenCalsLoaded {
		a.hiddenCalsLoaded = true
		if len(cfg.App.HiddenCalendars) > 0 {
			a.hiddenCalendars = make(map[string]bool, len(cfg.App.HiddenCalendars))
			for _, id := range cfg.App.HiddenCalendars {
				if id != "" {
					a.hiddenCalendars[id] = true
				}
			}
		}
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
	if a.composeOpen && a.composeUsingWYSIWYG() && a.composeEditor.Widget() != nil {
		a.composeEditor.UpdateTheme(a.composeThemePalette())
	}
	return true
}

func (a *App) layoutStateKey() string {
	return fmt.Sprintf("%v|%v|%v|%.0f|%.0f|%.0f", a.hideSidebar, a.showCopilot, a.notifyEnabled, a.sidebarSplit, a.mailSplit, a.copilotSplit)
}

func (a *App) persistLayoutPrefs() {
	if a.opts.ConfigLoader == nil || a.opts.ConfigSaver == nil {
		return
	}
	a.lastLayoutKey = a.layoutStateKey()
	a.lastLayoutSaveAt = time.Now()
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	hs := a.hideSidebar
	sc := a.showCopilot
	ne := a.notifyEnabled
	cfg.App.HideSidebar = &hs
	cfg.App.ShowCopilot = &sc
	cfg.App.NotifyEnabled = &ne
	if a.sidebarSplit > 0 {
		cfg.App.SidebarSplit = a.sidebarSplit
	}
	if a.mailSplit > 0 {
		cfg.App.MailSplit = a.mailSplit
	}
	if a.copilotSplit > 0 {
		cfg.App.CopilotSplit = a.copilotSplit
	}
	a.cfg = cfg
	go func(c config.Config) { _ = a.opts.ConfigSaver(c) }(cfg)
}

func (a *App) maybePersistLayoutSplits() {
	k := a.layoutStateKey()
	if a.lastLayoutKey == "" {
		a.lastLayoutKey = k
		return
	}
	if k != a.lastLayoutKey && time.Since(a.lastLayoutSaveAt) >= 800*time.Millisecond {
		a.persistLayoutPrefs()
	}
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
	account  string
	calStart time.Time
	calEnd   time.Time
	msgLoad  messageLoad
}

// calendarFetchWindow returns the time range to load from the calendar engine.
// It always covers both the currently visible Month/Week/Agenda window and today..today+calDays
// so the tray widget and upcoming meeting reminders stay up to date even when browsing another month.
func (a *App) calendarFetchWindow() (time.Time, time.Time) {
	now := time.Now()
	loc := now.Location()
	calDays := a.calDays
	if calDays <= 0 {
		calDays = 14
	}
	baseStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	baseEnd := time.Date(now.Year(), now.Month(), now.Day()+calDays, 23, 59, 59, 0, loc)

	cur := a.calCursor
	if cur.IsZero() {
		cur = now
	}
	var viewStart, viewEnd time.Time
	switch a.calViewMode {
	case 0: // Month grid (include leading Monday & trailing Sunday)
		first := time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, loc)
		offset := (int(first.Weekday()) + 6) % 7 // Monday = 0
		viewStart = first.AddDate(0, 0, -offset)
		viewEnd = viewStart.AddDate(0, 0, 42).Add(-time.Second)
	case 1: // Week grid (Monday..Sunday)
		day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
		offset := (int(day.Weekday()) + 6) % 7
		viewStart = day.AddDate(0, 0, -offset)
		viewEnd = viewStart.AddDate(0, 0, 7).Add(-time.Second)
	default: // Agenda
		viewStart = baseStart
		viewEnd = baseEnd
	}
	start := baseStart
	if viewStart.Before(start) {
		start = viewStart
	}
	end := baseEnd
	if viewEnd.After(end) {
		end = viewEnd
	}
	// When searching calendar events, widen the fetch range so past and upcoming matches are included.
	if strings.TrimSpace(a.calSearchQuery) != "" {
		searchStart := time.Date(now.Year(), now.Month(), now.Day()-60, 0, 0, 0, 0, loc)
		searchEnd := time.Date(now.Year(), now.Month(), now.Day()+180, 23, 59, 59, 0, loc)
		if searchStart.Before(start) {
			start = searchStart
		}
		if searchEnd.After(end) {
			end = searchEnd
		}
	}
	return start, end
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
	calStart, calEnd := a.calendarFetchWindow()
	snap := refreshSnapshot{account: a.selectedAccount, calStart: calStart, calEnd: calEnd, msgLoad: a.newMessageLoad()}
	go a.refreshBackground(ctx, snap)
}

// finishRefresh ends a refresh and runs a coalesced follow-up (UI thread).
func (a *App) finishRefresh(ctx context.Context) {
	a.refreshing = false
	if !a.isDirectMode() {
		a.phone.refresh.Refreshing = false
	} else {
		a.LoadOutbox(ctx)
	}
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

	bc, connErr := a.client()
	var health backend.HealthStatus
	if connErr == nil {
		health, connErr = bc.Health(ctx)
	}
	a.emitUpdate(func() {
		if cfgErr == nil {
			a.applyConfig(cfg)
		}
		if stOK {
			a.triageState = st
			a.barSummary = triage.ComputeBarSummary(st)
		}
		if connErr != nil {
			a.engineReady = false
			a.lastError = a.errText(connErr)
			return
		}
		a.engineReady = health.Connected
		a.readOnly = health.ReadOnly
		a.lastError = ""
	})
	if connErr != nil {
		a.emitUpdate(func() { a.finishRefresh(ctx) })
		return
	}
	if !health.Connected {
		a.emitUpdate(func() {
			a.lastSync = time.Now()
			a.finishRefresh(ctx)
		})
		return
	}

	type result struct {
		accounts  []backend.Account
		folders   []backend.Folder
		calendars []backend.Calendar
		events    []backend.CalendarEvent
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
		start := snap.calStart.Format(time.RFC3339)
		end := snap.calEnd.Format(time.RFC3339)
		r, err := bc.CalendarEvents(c, start, end, nil, 500)
		if err == nil {
			res.events = r.Events
			if res.events == nil {
				res.events = []backend.CalendarEvent{}
			}
		}
		return err
	})
	for i := 0; i < 4; i++ {
		<-done
	}

	a.emitUpdate(func() {
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
	key       string // the view, see App.listKey
	filter    backend.MessagesFilter
	actionIDs map[string]bool // triage action/waiting cards for the "action" view
}

// messagePage is how many mails one list request loads; scrolling near the end loads the next.
const messagePage = 100

// newMessageLoad snapshots the current view into a new load request (UI thread). Every call
// supersedes all earlier ones: their responses are dropped. Reloading the view on screen keeps
// as many mails as are loaded, so new mail does not cut the list back to its first page.
func (a *App) newMessageLoad() messageLoad {
	a.msgLoadSeq++
	return a.viewLoad()
}

// viewLoad describes a load of the view on screen under the current load number (UI thread).
func (a *App) viewLoad() messageLoad {
	query := strings.TrimSpace(a.searchQuery)
	key := a.navID + "\x00" + a.selectedAccount + "\x00" + query
	limit := messagePage
	if key == a.listKey {
		limit = min(max(limit, len(a.messages)), 1000)
	}
	req := messageLoad{
		seq:   a.msgLoadSeq,
		navID: a.navID,
		key:   key,
		filter: backend.MessagesFilter{
			AccountID: a.selectedAccount,
			Query:     query,
			Limit:     limit,
		},
	}
	switch {
	case strings.HasPrefix(req.navID, "folder:"):
		req.filter.FolderID = strings.TrimPrefix(req.navID, "folder:")
	case req.navID == "recent" && query == "":
		// "Recent mail" across all folders; searches and folders reach back to the first mail.
		req.filter.Days = 30
	case req.navID == "focus":
		req.filter.UnreadOrFlagged = true
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
	if !a.engineReady {
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	req := a.newMessageLoad()
	go a.loadMessagesBackground(ctx, bc, req)
}

func (a *App) loadMessagesBackground(ctx context.Context, bc *backend.Client, req messageLoad) {
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
	msgs := req.keep(page.Messages)
	total, next := page.Total, page.NextCursor
	a.emitUpdate(func() {
		if a.applyMessageLoad(req, msgs, total) {
			a.nextCursor, a.listKey, a.loadingMore = next, req.key, false
		}
	})
}

// keep filters a page for views the engine cannot filter (the action view).
func (req messageLoad) keep(msgs []backend.MessageHeader) []backend.MessageHeader {
	if req.navID != "action" {
		return msgs
	}
	filtered := make([]backend.MessageHeader, 0, len(msgs))
	for _, m := range msgs {
		if req.actionIDs[m.ID] || m.Flagged {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// loadMoreMessages appends the next page of the list on screen (UI thread). A newer load of the
// list drops the page.
func (a *App) loadMoreMessages(ctx context.Context) {
	if a.nextCursor == "" || a.loadingMore || !a.engineReady {
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	req := a.viewLoad()
	if req.key != a.listKey {
		return
	}
	req.filter.Cursor, req.filter.Limit = a.nextCursor, messagePage
	a.loadingMore = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		page, err := bc.Messages(c, req.filter)
		cancel()
		msgs := req.keep(page.Messages)
		a.emitUpdate(func() {
			if req.seq != a.msgLoadSeq {
				return
			}
			a.loadingMore = false
			if err != nil {
				a.toast(a.errText(err), "error")
				return
			}
			seen := make(map[string]bool, len(a.messages))
			for _, m := range a.messages {
				seen[m.ID] = true
			}
			for _, m := range msgs {
				if !seen[m.ID] {
					a.messages = append(a.messages, m)
				}
			}
			a.nextCursor, a.totalMessages = page.NextCursor, page.Total
		})
	}()
}

// applyMessageLoad installs a load result unless a newer load was started (UI thread).
func (a *App) applyMessageLoad(req messageLoad, msgs []backend.MessageHeader, total int) bool {
	if req.seq != a.msgLoadSeq {
		return false
	}
	// Seed seenMails on the first non-empty fetch so startup doesn't spam notifications.
	if len(a.seenMails) == 0 {
		for _, m := range msgs {
			a.seenMails[m.ID] = true
		}
	}
	if req.navID == "focus" {
		msgs = a.keepOpenMail(msgs)
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
func (a *App) setMessages(msgs []backend.MessageHeader) {
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
		a.serverSearchDoneKey = ""
		a.serverSearchDoneQuery = ""
	}
	a.lastCheckedIndex = -1
}

func (a *App) serverSearchScopeKey(q string) string {
	return a.navID + "|" + a.selectedAccount + "|" + strings.TrimSpace(q)
}

func isOutboundFolderName(name string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/"))))
	switch base {
	case "sent", "sent items", "sent messages", "gesendet", "gesendete", "gesendete objekte", "gesendete elemente",
		"drafts", "entwürfe", "entwuerfe":
		return true
	}
	return false
}

// isCurrentViewOutbound reports whether the active folder is Sent or Drafts,
// where the message list should display recipients ("An: …") instead of the user's own From address.
func (a *App) isCurrentViewOutbound() bool {
	if !strings.HasPrefix(a.navID, "folder:") {
		return false
	}
	fid := strings.TrimPrefix(a.navID, "folder:")
	for _, f := range a.folders {
		if f.ID == fid {
			t := strings.ToLower(f.Type)
			if t == "sent" || t == "drafts" {
				return true
			}
			for _, su := range f.SpecialUse {
				if strings.EqualFold(su, "sent") || strings.EqualFold(su, "drafts") {
					return true
				}
			}
			return isOutboundFolderName(f.Path) || isOutboundFolderName(f.Name)
		}
	}
	return false
}

// isJunkMessage reports whether msg resides in a Junk/Spam folder.
func (a *App) isJunkMessage(msg *backend.MessageDetail) bool {
	if msg == nil {
		return false
	}
	for _, f := range a.folders {
		if f.ID == msg.FolderID {
			if strings.EqualFold(f.Type, "junk") || slices.Contains(f.SpecialUse, "junk") {
				return true
			}
		}
	}
	base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(msg.Folder, "\\", "/"))))
	return base == "junk" || base == "spam" || base == "junk e-mail" || base == "bulk mail"
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
// SelectMessage opens a mail because the user asked for it (click, j/k, notification): an
// unread mail gets marked read as set in the app's settings (UI thread).
func (a *App) SelectMessage(ctx context.Context, id string) {
	a.selectMessage(ctx, id, true)
}

// reloadMessage shows the open mail again after an action, without marking it read: "mark as
// unread" must stay unread.
func (a *App) reloadMessage(ctx context.Context, id string) {
	a.selectMessage(ctx, id, false)
}

func (a *App) selectMessage(ctx context.Context, id string, userOpened bool) {
	if id == "" {
		return
	}
	sameMsgReload := !userOpened && a.selectedID == id
	if a.selectedMsg != nil && a.selectedMsg.ID != id {
		a.selectedMsg = nil
	}
	a.selectedID = id
	if !sameMsgReload {
		a.inspectorSeq++
		a.inspectorResult = ""
		a.inspectorDraft = ""
		a.inspectorEvent = nil
		a.inspectorContacts = nil
		a.inspectorError = ""
		a.inspectorMode = ""
		a.inspectorLoading = false
	}
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
		detail, err := bc.MessageWithOptions(c, id, backend.MessageOptions{Format: "both", Decrypt: true})
		var body readerBody
		if err == nil {
			showRemote := a.readerRemoteImagesAllowed(id)
			body = buildReaderBodyWithImages(&detail, showRemote) // off the UI thread: large HTML takes a moment
		}
		a.emitUpdate(func() {
			if a.selectedID != id {
				return
			}
			if err != nil {
				a.inspectorError = a.errText(err)
				return
			}
			a.resetReaderImages(id)
			a.selectedMsg = &detail
			a.readerBody = body
			a.syncPreviewForMessage(a.selectedMsg)
			a.loadConversation(ctx, id)
			if userOpened {
				a.scheduleMarkRead(ctx, id)
			}
		})
	}()
}

func (a *App) RunInspectorAI(ctx context.Context, mode string) {
	msg := a.selectedMsg
	if msg == nil || a.inspectorLoading {
		return
	}
	lang := a.uiLang()
	aiBackend, model := a.agentBackend, a.agentModel
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
	// Summaries and replies see the whole conversation (reading order), not just the open mail.
	convSize := 0
	if mode == "summarize" || mode == "draft_reply" {
		convSize = a.conversationSize(msg.ID)
	}
	go func() {
		req.ThreadMessages = threadForAI(ctx, bc, msg.ID, convSize)
		c, cancel := context.WithTimeout(ctx, backend.AIRequestTimeout)
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
			prov := ai.ProviderForBackend(cfg.AI, aiBackend)
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
		var books []backend.AddressBook
		var bookLabels []string
		bookMap := make(map[string]string)
		contactsSupported := true
		if mode == "extract_contacts" {
			var existing []backend.ContactRecord
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
							"%d Kontakt(e) im E-Mail-Footer erkannt und lokal mit deinem Adressbuch abgeglichen.",
							"%d contact(s) detected in the email footer and matched locally against your address book.",
							len(contactProps),
						)
					}
				}
			}
		})
	}()
}

func buildContactProposals(extracted []ai.ExtractedContact, existing []backend.ContactRecord) []ContactProposal {
	return buildContactProposalsLang(extracted, existing, "de")
}

func buildContactProposalsLang(extracted []ai.ExtractedContact, existing []backend.ContactRecord, lang string) []ContactProposal {
	byEmail := make(map[string]backend.ContactRecord, len(existing)*2)
	byName := make(map[string]backend.ContactRecord, len(existing))
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
		var match *backend.ContactRecord
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

func missingContactFieldPairs(ex ai.ExtractedContact, cur backend.ContactRecord, lang string) (keys, labels []string) {
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
	req := backend.ContactUpsertRequest{
		AddressBookID: bookID,
		Mode:          "create",
		Contact: backend.ContactRecord{
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
			a.contacts.loaded = false
			if req.Mode == "enrich" {
				a.toast(a.tr("Kontaktdaten im Adressbuch vervollständigt", "Contact details updated in address book"), "success")
			} else {
				a.toast(a.tr("Kontakt im Adressbuch gespeichert", "Contact saved to address book"), "success")
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

// refreshFoldersBackground reloads sidebar folder metadata (including unread badges).
func (a *App) refreshFoldersBackground(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		return
	}
	acc := a.selectedAccount
	go func() {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		folders, err := bc.Folders(c, acc)
		if err != nil || folders == nil {
			return
		}
		a.emitUpdate(func() {
			if a.selectedAccount == acc {
				a.folders = folders
			}
		})
	}()
}

// SaveDraft saves a draft in the Drafts folder (UI thread). When replacing an existing draft
// (req.DraftMessageID != ""), the previous draft copy is deleted after the new one succeeds.
func (a *App) SaveDraft(ctx context.Context, req backend.ComposeRequest, successMsg string) {
	if a.composeBusy {
		return
	}
	bc, err := a.client()
	if err != nil || (a.readOnly && !req.OpenWindow) {
		a.toast(a.tr("Read-Only oder Offline: Entwurf nicht gespeichert", "Read-only or offline: draft not saved"), "error")
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = backend.NewIdempotencyKey()
	}
	fwdMsgID := a.composeForwardMsgID
	fwdHTML := a.composeForwardHTML
	keepHTML := a.composeKeepHTML
	fwdTitle := a.tr("Weitergeleitete Nachricht", "Forwarded message")
	fwdFrom, fwdDate, fwdSubj, fwdSep := a.composeForwardFrom, a.composeForwardDate, a.composeForwardSubj, a.composeForwardSep
	dc := a.opts.Direct
	ed := a.composeEditor
	usingWYSIWYG := a.composeUsingWYSIWYG() && ed != nil && ed.Widget() != nil
	a.composeBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if usingWYSIWYG {
			if htmlOut, textOut, ok := ed.ReadContent(c); ok {
				req.BodyHTML = htmlOut
				req.Body = textOut
				req.IsHTML = true
			}
		}
		if keepHTML && fwdHTML != "" && req.BodyHTML == "" {
			var inlineImgs map[string]mailengine.InlineImage
			if dc != nil && fwdMsgID != "" {
				inlineImgs, _ = dc.ReadAllInlineImages(c, fwdMsgID)
			}
			req.BodyHTML = BuildForwardHTML(extractUserTopText(req.Body, fwdSep), fwdTitle, fwdFrom, fwdDate, fwdSubj, fwdHTML, inlineImgs)
		}
		raw, err := bc.ComposeDraft(c, req)
		a.emitUpdate(func() {
			a.composeBusy = false
			if err != nil {
				a.toast(a.tr("Fehler: ", "Error: ")+a.errText(err), "error")
				return
			}
			res := backend.ParseComposeResult(raw)
			if req.DraftMessageID != "" && a.selectedID == req.DraftMessageID && res.DraftID != "" && res.DraftID != req.DraftMessageID {
				a.selectedID = ""
				a.selectedMsg = nil
				a.syncPreviewForMessage(nil)
			}
			a.closeCompose()
			a.toast(successMsg, "success")
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
		})
	}()
}

func extractUserTopText(body, sep string) string {
	if sep != "" {
		if idx := strings.Index(body, sep); idx >= 0 {
			return strings.TrimSpace(body[:idx])
		}
	}
	return strings.TrimSpace(body)
}

// SendCompose sends the compose modal content directly via SMTP (UI thread). The idempotency key is
// derived from the compose session and the content: a retry of the same content can never
// send twice, while edited content gets a new key.
func (a *App) SendCompose(ctx context.Context, req backend.ComposeRequest) {
	if a.composeNonce == "" {
		a.composeNonce = backend.NewIdempotencyKey()
	}
	a.sendPreparedCompose(ctx, req, a.composeNonce, a.composeForwardMsgID, a.composeForwardHTML, a.composeKeepHTML,
		a.composeForwardFrom, a.composeForwardDate, a.composeForwardSubj, a.composeForwardSep)
}

func (a *App) sendPreparedCompose(ctx context.Context, req backend.ComposeRequest, composeNonce, fwdMsgID, fwdHTML string, keepHTML bool, fwdFrom, fwdDate, fwdSubj, fwdSep string) {
	if a.composeBusy {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Senden nicht erlaubt (Read-Only oder Offline)", "Sending not allowed (read-only or offline)"), "error")
		return
	}
	if composeNonce == "" {
		composeNonce = backend.NewIdempotencyKey()
	}
	var srcAttKeys []string
	for _, sa := range req.SourceAttachments {
		srcAttKeys = append(srcAttKeys, sa.MessageID+":"+sa.PartName+":"+sa.Name)
	}
	fwdTitle := a.tr("Weitergeleitete Nachricht", "Forwarded message")
	dc := a.opts.Direct
	ed := a.composeEditor
	usingWYSIWYG := !req.IsHTML && a.composeUsingWYSIWYG() && ed != nil && ed.Widget() != nil
	a.composeBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, backend.ComposeSendTimeout)
		defer cancel()
		if usingWYSIWYG {
			if htmlOut, textOut, ok := ed.ReadContent(c); ok {
				req.BodyHTML = htmlOut
				req.Body = textOut
				req.IsHTML = true
			}
		}
		if keepHTML && fwdHTML != "" && req.BodyHTML == "" {
			var inlineImgs map[string]mailengine.InlineImage
			if dc != nil && fwdMsgID != "" {
				inlineImgs, _ = dc.ReadAllInlineImages(c, fwdMsgID)
			}
			req.BodyHTML = BuildForwardHTML(extractUserTopText(req.Body, fwdSep), fwdTitle, fwdFrom, fwdDate, fwdSubj, fwdHTML, inlineImgs)
		}
		req.IdempotencyKey = backend.ContentIdempotencyKey(composeNonce, "send",
			req.AccountID, req.IdentityID, req.FromEmail, req.DraftMessageID,
			strings.Join(req.To, ","), strings.Join(req.CC, ","), strings.Join(req.BCC, ","),
			req.Subject, req.Body, req.BodyHTML, req.ReplyToMessageID, req.ReplyType, req.Sign,
			strings.Join(req.Attachments, ","), strings.Join(srcAttKeys, ","))
		raw, err := bc.ComposeSend(c, req)
		a.emitUpdate(func() {
			a.composeBusy = false
			if err != nil {
				if !backend.IsOutcomeUnknown(err) {
					a.composeNonce = backend.NewIdempotencyKey()
				}
				a.toast(a.tr("Senden fehlgeschlagen: ", "Send failed: ")+a.errText(err), "error")
				return
			}
			if req.DraftMessageID != "" && a.selectedID == req.DraftMessageID {
				a.selectedID = ""
				a.selectedMsg = nil
				a.syncPreviewForMessage(nil)
			}
			if a.composeOpen {
				a.closeCompose()
			}
			a.composeConfirm = false
			res := backend.ParseComposeResult(raw)
			if req.DraftMessageID != "" {
				a.startMessageLoad(ctx)
				a.refreshFoldersBackground(ctx)
			}
			if res.Queued {
				a.LoadOutbox(ctx)
				a.toast(a.tr("Offline – in Postausgang eingereiht", "Offline – queued in Outbox")+a.composeSecuritySuffix(res.Security), "warning")
				return
			}
			if res.SentCopyFailed {
				a.toast(a.tr("E-Mail gesendet – aber nicht in „Gesendet“ abgelegt", "Email sent – but not stored in Sent"), "warning")
				return
			}
			a.toast(a.tr("E-Mail gesendet", "Email sent")+a.composeSecuritySuffix(res.Security), "success")
		})
	}()
}

// LoadOutbox fetches all pending or failed outgoing emails from the offline outbox.
func (a *App) LoadOutbox(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		items, lerr := bc.OutboxList(c)
		if lerr != nil {
			return
		}
		a.emitUpdate(func() {
			a.outboxItems = items
		})
	}()
}

// FlushOutbox immediately retries all messages currently in the offline outbox.
func (a *App) FlushOutbox(ctx context.Context) {
	if a.outboxBusy {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		return
	}
	a.outboxBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, backend.ComposeSendTimeout)
		defer cancel()
		sent, rem, ferr := bc.OutboxFlush(c)
		items, _ := bc.OutboxList(c)
		a.emitUpdate(func() {
			a.outboxBusy = false
			if items != nil || ferr == nil {
				a.outboxItems = items
			}
			if ferr != nil {
				a.toast(a.tr("Postausgang-Versand fehlgeschlagen: ", "Outbox send failed: ")+a.errText(ferr), "error")
				return
			}
			if sent > 0 && rem == 0 {
				a.toast(a.trf("%d E-Mail(s) aus Postausgang gesendet", "Sent %d email(s) from Outbox", sent), "success")
				a.outboxOpen = false
			} else if sent > 0 {
				a.toast(a.trf("%d gesendet, %d verbleiben im Postausgang", "%d sent, %d remaining in Outbox", sent, rem), "warning")
			} else if rem > 0 {
				a.toast(a.trf("Noch %d E-Mail(s) im Postausgang (Details prüfen)", "%d email(s) still in Outbox (check errors)", rem), "warning")
			}
		})
	}()
}

// DeleteOutboxItem removes a single queued or failed email from the offline outbox.
func (a *App) DeleteOutboxItem(ctx context.Context, id string) {
	bc, err := a.client()
	if err != nil || a.readOnly || id == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if derr := bc.OutboxDelete(c, id); derr != nil {
			a.emitUpdate(func() {
				a.toast(a.tr("Löschen fehlgeschlagen: ", "Delete failed: ")+a.errText(derr), "error")
			})
			return
		}
		items, _ := bc.OutboxList(c)
		a.emitUpdate(func() {
			a.outboxItems = items
			if len(items) == 0 {
				a.outboxOpen = false
			}
		})
	}()
}

// PerformMessageAction applies one mail action (UI thread). The current Message-ID is sent as
// expectedMessageId so a stale list can't hit a different mail.
func (a *App) PerformMessageAction(ctx context.Context, id string, patch backend.MessageActionsRequest, toastMsg string) {
	a.performMessageAction(ctx, id, patch, toastMsg, "")
}

// performMessageAction is PerformMessageAction; openNext, when set, is opened after the mail
// left the list (trash/move) while it was still the open one, so triage continues with its
// neighbour.
func (a *App) performMessageAction(ctx context.Context, id string, patch backend.MessageActionsRequest, toastMsg, openNext string) {
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
	// Capture metadata for Undo before mutation.
	var prevAccountID, prevFolderPath, prevHeaderMsgID string
	for _, m := range a.messages {
		if m.ID == id {
			prevAccountID, prevFolderPath, prevHeaderMsgID = m.AccountID, m.Folder, m.MessageID
			break
		}
	}
	if prevAccountID == "" && a.selectedMsg != nil && a.selectedMsg.ID == id {
		prevAccountID, prevFolderPath, prevHeaderMsgID = a.selectedMsg.AccountID, a.selectedMsg.Folder, a.selectedMsg.MessageID
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		raw, err := bc.MessageActions(c, id, patch)
		cancel()
		var movedTarget string
		if err == nil && len(raw) > 0 {
			var res struct {
				MovedTargetPath string `json:"movedTargetPath"`
			}
			_ = json.Unmarshal(raw, &res)
			movedTarget = res.MovedTargetPath
		}
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
				} else {
					a.syncPreviewForMessage(nil)
				}
			}
			// Record reversible Undo action (never for permanent deletes).
			switch {
			case removed && movedTarget != "" && prevAccountID != "" && prevFolderPath != "" && prevHeaderMsgID != "":
				a.undo = &undoAction{
					Kind:      "move",
					Label:     toastMsg,
					CreatedAt: time.Now(),
					MoveItems: []backend.UndoMoveItem{{
						AccountID:      prevAccountID,
						MessageID:      prevHeaderMsgID,
						FromFolderPath: prevFolderPath,
						ToFolderPath:   movedTarget,
					}},
				}
			case !removed && patch.Read != nil:
				a.undo = &undoAction{
					Kind:        "read",
					Label:       toastMsg,
					CreatedAt:   time.Now(),
					ToggleItems: []backend.BatchActionItem{{ID: id, ExpectedMessageID: prevHeaderMsgID}},
					PrevValue:   !*patch.Read,
				}
			case !removed && patch.Flagged != nil:
				a.undo = &undoAction{
					Kind:        "flag",
					Label:       toastMsg,
					CreatedAt:   time.Now(),
					ToggleItems: []backend.BatchActionItem{{ID: id, ExpectedMessageID: prevHeaderMsgID}},
					PrevValue:   !*patch.Flagged,
				}
			}
			a.toast(toastMsg, "success")
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
			if !removed && a.selectedID == id {
				a.reloadMessage(ctx, id)
			}
		})
	}()
}

// PerformUndo reverses the last reversible mail action (Archive, Move, Trash, Read, Star). UI thread.
func (a *App) PerformUndo(ctx context.Context) {
	if a.undo == nil || a.undoBusy {
		return
	}
	if time.Since(a.undo.CreatedAt) > undoExpiry {
		a.undo = nil
		a.toast(a.tr("Rückgängig ist abgelaufen", "Undo expired"), "info")
		return
	}
	if a.readOnly {
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	u := *a.undo
	a.undo = nil
	a.undoBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var uerr error
		var restoredCount int
		var failedCount int
		switch u.Kind {
		case "move":
			res, err := bc.UndoMove(c, backend.UndoMoveRequest{Items: u.MoveItems})
			uerr = err
			restoredCount = res.Restored
		case "read":
			v := u.PrevValue
			res, err := bc.BatchMessageActions(c, backend.BatchActionsRequest{
				Items:  u.ToggleItems,
				Action: backend.MessageActionsRequest{Read: &v},
			})
			uerr = err
			restoredCount = len(res.OKIDs)
			failedCount = res.Failed
			if uerr == nil && failedCount > 0 && res.FirstError != "" {
				uerr = fmt.Errorf("%s", res.FirstError)
			}
		case "flag":
			v := u.PrevValue
			res, err := bc.BatchMessageActions(c, backend.BatchActionsRequest{
				Items:  u.ToggleItems,
				Action: backend.MessageActionsRequest{Flagged: &v},
			})
			uerr = err
			restoredCount = len(res.OKIDs)
			failedCount = res.Failed
			if uerr == nil && failedCount > 0 && res.FirstError != "" {
				uerr = fmt.Errorf("%s", res.FirstError)
			}
		}
		a.emitUpdate(func() {
			a.undoBusy = false
			if uerr != nil {
				a.toast(a.tr("Rückgängig fehlgeschlagen: ", "Undo failed: ")+a.errText(uerr), "error")
				return
			}
			if restoredCount == 0 {
				a.toast(a.tr("Nachricht zum Rückgängigmachen nicht mehr gefunden", "Message to undo was no longer found"), "info")
				return
			}
			a.toast(a.tr("Rückgängig gemacht", "Undone"), "success")
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
		})
	}()
}

// MarkCurrentViewAllRead marks unread messages in the active folder (or only the visible unread
// messages in Focus view, never silently wiping other folders across all time) as read.
func (a *App) MarkCurrentViewAllRead(ctx context.Context) {
	if a.readOnly {
		a.toast(a.tr("Read-Only-Modus aktiv — Änderung blockiert", "Read-only mode active — change blocked"), "error")
		return
	}
	if !strings.HasPrefix(a.navID, "folder:") {
		// In Focus / cross-folder views, only mark the messages currently shown in this view
		// so we never unexpectedly wipe unread flags in unrelated folders or older archives.
		var ids []string
		for _, m := range a.messages {
			if m.Unread {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) == 0 {
			a.toast(a.tr("Keine ungelesenen Mails in dieser Ansicht", "No unread emails in this view"), "info")
			return
		}
		readTrue := true
		a.PerformBatchAction(ctx, ids, backend.MessageActionsRequest{Read: &readTrue}, a.tr("Als gelesen markiert", "Marked as read"))
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	folderID := strings.TrimPrefix(a.navID, "folder:")
	accID := a.selectedAccount
	go func() {
		c, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		n, err := bc.MarkAllRead(c, accID, folderID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Fehler: ", "Error: ")+a.errText(err), "error")
				return
			}
			a.toast(a.trf("%d Mail(s) als gelesen markiert", "Marked %d email(s) as read", n), "success")
			a.startMessageLoad(ctx)
			a.refreshFoldersBackground(ctx)
		})
	}()
}

// SearchMessagesOnServer executes an IMAP server-side TEXT search (searching bodies on the server
// that haven't been downloaded locally yet) and merges hits into the active message list.
func (a *App) SearchMessagesOnServer(ctx context.Context) {
	q := strings.TrimSpace(a.searchQuery)
	if q == "" || a.serverSearchBusy || !a.engineReady {
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	folderID := ""
	if strings.HasPrefix(a.navID, "folder:") {
		folderID = strings.TrimPrefix(a.navID, "folder:")
	}
	accID := a.selectedAccount
	scopeKey := a.serverSearchScopeKey(q)
	seq := a.msgLoadSeq
	a.serverSearchBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		page, err := bc.SearchServerMessages(c, backend.MessagesFilter{
			AccountID: accID,
			FolderID:  folderID,
			Query:     q,
			Limit:     100,
		})
		a.emitUpdate(func() {
			a.serverSearchBusy = false
			if seq != a.msgLoadSeq || a.serverSearchScopeKey(a.searchQuery) != scopeKey {
				return
			}
			if err != nil {
				a.toast(a.tr("Server-Suche fehlgeschlagen: ", "Server search failed: ")+a.errText(err), "error")
				return
			}
			seen := make(map[string]bool, len(a.messages))
			merged := make([]backend.MessageHeader, 0, len(a.messages)+len(page.Messages))
			for _, m := range a.messages {
				seen[m.ID] = true
				merged = append(merged, m)
			}
			added := 0
			for _, m := range page.Messages {
				if !seen[m.ID] {
					seen[m.ID] = true
					merged = append(merged, m)
					added++
				}
			}
			if added > 0 {
				sort.SliceStable(merged, func(i, j int) bool {
					return parseMailDateUnix(merged[i].Date) > parseMailDateUnix(merged[j].Date)
				})
				a.setMessages(merged)
				a.totalMessages = len(merged)
			}
			a.serverSearchDoneQuery = q
			a.serverSearchDoneKey = scopeKey
			a.serverSearchCount = len(page.Messages)
			a.toast(a.trf("Server-Suche: %d Treffer (%d neu)", "Server search: %d matches (%d new)", len(page.Messages), added), "info")
		})
	}()
}

func parseMailDateUnix(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RFC1123Z, time.RFC1123} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// PrintSelectedMessage builds a clean print-ready HTML document for the currently open email
// and invokes the desktop print hook (or falls back to opening the print page in the browser).
func (a *App) PrintSelectedMessage(ctx context.Context) {
	msg := a.selectedMsg
	if msg == nil {
		a.toast(a.tr("Keine Mail ausgewählt zum Drucken", "No email selected to print"), "info")
		return
	}
	msgCopy := *msg
	lang := a.uiLang()
	allowRemote := a.readerRemoteImagesAllowed(msg.ID)
	dc := a.opts.Direct
	onPrint := a.onPrint
	go func() {
		var inlineImgs map[string]mailengine.InlineImage
		if dc != nil && msgCopy.ID != "" {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			inlineImgs, _ = dc.ReadAllInlineImages(c, msgCopy.ID)
			cancel()
		}
		doc := BuildPrintHTML(&msgCopy, lang, inlineImgs, allowRemote, onPrint == nil)
		title := msgCopy.Subject
		if strings.TrimSpace(title) == "" {
			title = "MailDiver"
		}
		a.emitUpdate(func() {
			if a.onPrint != nil {
				a.onPrint(title, doc)
				return
			}
			if err := openPrintHTMLFallback(doc); err != nil {
				a.toast(a.tr("Drucken fehlgeschlagen: ", "Print failed: ")+err.Error(), "error")
			}
		})
	}()
}

func (a *App) CreateCalendarEvent(ctx context.Context, req backend.CreateCalendarEventRequest) {
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
			a.syncAuxWindows()
			a.toast(a.trf("Termin „%s“ angelegt", "Created event \"%s\"", ev.Title), "success")
			a.startRefresh(ctx)
		})
	}()
}

// UpdateCalendarEvent updates an existing calendar event via calendar.update.
func (a *App) UpdateCalendarEvent(ctx context.Context, calendarID, eventID string, patch map[string]any) {
	if a.eventBusy || strings.TrimSpace(eventID) == "" {
		return
	}
	bc, err := a.client()
	if err != nil || a.readOnly {
		a.toast(a.tr("Read-Only oder Offline: Termin nicht aktualisiert", "Read-only or offline: event not updated"), "error")
		return
	}
	a.eventBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ev, err := bc.UpdateCalendarEvent(c, calendarID, eventID, patch)
		a.emitUpdate(func() {
			a.eventBusy = false
			if err != nil {
				a.toast(a.tr("Termin-Fehler: ", "Event error: ")+a.errText(err), "error")
				return
			}
			a.eventOpen = false
			a.eventEditingID = ""
			a.syncAuxWindows()
			a.toast(a.trf("Termin „%s“ aktualisiert", "Updated event \"%s\"", ev.Title), "success")
			a.startRefresh(ctx)
		})
	}()
}

// RequestDeleteCalendarEvent opens the delete confirmation for ev (UI thread).
func (a *App) RequestDeleteCalendarEvent(ev backend.CalendarEvent) {
	if a.readOnly || ev.ReadOnly || a.calDelBusy {
		return
	}
	a.calDelEvent = ev
	a.calDelError = ""
	a.calDelOpen = true
}

// ConfirmDeleteCalendarEvent deletes the event pending confirmation (UI thread). scope is
// backend.ScopeOccurrence or ScopeSeries; for non-recurring events it is ignored.
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
		if scope == backend.ScopeOccurrence {
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

func (a *App) defaultComposeAccountID(preferred string) string {
	if preferred != "" {
		for _, acc := range a.accounts {
			if acc.ID == preferred {
				return acc.ID
			}
		}
	}
	if a.selectedAccount != "" {
		return a.selectedAccount
	}
	if len(a.accounts) > 0 {
		return a.accounts[0].ID
	}
	return ""
}

func (a *App) composeAccountLabelFor(id string) string {
	for _, acc := range a.accounts {
		if acc.ID == id {
			return a.accountDisplayName(acc.ID)
		}
	}
	if len(a.accounts) > 0 {
		return a.accountDisplayName(a.accounts[0].ID)
	}
	return ""
}

func (a *App) composeAccountSignature(accountID string) string {
	for _, acc := range a.cfg.Direct.Accounts {
		if acc.ID == accountID {
			return strings.TrimSpace(acc.Signature)
		}
	}
	if len(a.cfg.Direct.Accounts) > 0 && accountID == "" {
		return strings.TrimSpace(a.cfg.Direct.Accounts[0].Signature)
	}
	return ""
}

// composeSignatureFor returns the alias-specific signature when configured, else the account signature.
func (a *App) composeSignatureFor(accountID, email string) string {
	em := strings.ToLower(strings.TrimSpace(email))
	for _, acc := range a.cfg.Direct.Accounts {
		if accountID != "" && acc.ID != accountID {
			continue
		}
		if em != "" {
			for _, al := range acc.Aliases {
				if strings.EqualFold(strings.TrimSpace(al.Email), em) {
					if sig := strings.TrimSpace(al.Signature); sig != "" {
						return sig
					}
					break
				}
			}
		}
		if accountID != "" {
			return strings.TrimSpace(acc.Signature)
		}
	}
	return a.composeAccountSignature(accountID)
}

func (a *App) composeAccountEmail(accountID string) string {
	for _, acc := range a.cfg.Direct.Accounts {
		if acc.ID == accountID {
			return strings.ToLower(strings.TrimSpace(acc.Email))
		}
	}
	return ""
}

// stripSignatureDelim removes any leading RFC 3676 "--" separator line from a stored
// signature so appending "-- \n" never produces a doubled "-- / --" block.
func stripSignatureDelim(sig string) string {
	s := strings.ReplaceAll(sig, "\r\n", "\n")
	s = strings.TrimSpace(s)
	for {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != "--" {
			break
		}
		if len(lines) == 1 {
			return ""
		}
		s = strings.TrimSpace(lines[1])
	}
	return s
}

func appendAccountSignature(body, sig string) string {
	sig = stripSignatureDelim(sig)
	if sig == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return "\n\n-- \n" + sig
	}
	if strings.Contains(body, sig) {
		return body
	}
	return strings.TrimRight(body, "\r\n") + "\n\n-- \n" + sig
}

type composeIdentityChoice struct {
	AccountID  string
	IdentityID string
	Email      string
	Name       string
	Label      string
	Signature  string
}

func (a *App) composeIdentityChoices() []composeIdentityChoice {
	sigByAcc := map[string]string{}
	aliasSigByEmail := map[string]string{}
	for _, acc := range a.cfg.Direct.Accounts {
		sigByAcc[acc.ID] = strings.TrimSpace(acc.Signature)
		for _, al := range acc.Aliases {
			if s := strings.TrimSpace(al.Signature); s != "" {
				aliasSigByEmail[acc.ID+"|"+strings.ToLower(strings.TrimSpace(al.Email))] = s
			}
		}
	}
	var choices []composeIdentityChoice
	for _, acc := range a.accounts {
		if len(acc.Identities) == 0 {
			lbl := a.accountDisplayName(acc.ID)
			choices = append(choices, composeIdentityChoice{
				AccountID:  acc.ID,
				IdentityID: acc.ID + "-id",
				Label:      lbl,
				Signature:  sigByAcc[acc.ID],
			})
			continue
		}
		multiID := len(acc.Identities) > 1
		for i, id := range acc.Identities {
			lbl := a.accountDisplayName(acc.ID)
			if multiID || len(a.accounts) == 1 {
				if id.Email != "" {
					lbl = formatEmailAddress(id.Name, id.Email)
				}
			} else if i > 0 && id.Email != "" {
				lbl = formatEmailAddress(id.Name, id.Email)
			}
			sig := sigByAcc[acc.ID]
			if as, ok := aliasSigByEmail[acc.ID+"|"+strings.ToLower(strings.TrimSpace(id.Email))]; ok && as != "" {
				sig = as
			}
			choices = append(choices, composeIdentityChoice{
				AccountID:  acc.ID,
				IdentityID: id.ID,
				Email:      id.Email,
				Name:       id.Name,
				Label:      lbl,
				Signature:  sig,
			})
		}
	}
	return choices
}

func (a *App) selectComposeIdentity(accountID, preferEmail string) {
	choices := a.composeIdentityChoices()
	if len(choices) == 0 {
		a.composeAccountID = a.defaultComposeAccountID(accountID)
		a.composeAccountLabel = a.composeAccountLabelFor(a.composeAccountID)
		a.composeIdentityID = ""
		a.composeFromEmail = ""
		a.composeFromName = ""
		return
	}
	targetAcc := a.defaultComposeAccountID(accountID)
	wantEmail := strings.ToLower(strings.TrimSpace(preferEmail))
	if wantEmail != "" {
		for _, ch := range choices {
			if (targetAcc == "" || ch.AccountID == targetAcc) && strings.EqualFold(ch.Email, wantEmail) {
				a.composeAccountID = ch.AccountID
				a.composeAccountLabel = ch.Label
				a.composeIdentityID = ch.IdentityID
				a.composeFromEmail = ch.Email
				a.composeFromName = ch.Name
				return
			}
		}
	}
	for _, ch := range choices {
		if ch.AccountID == targetAcc {
			a.composeAccountID = ch.AccountID
			a.composeAccountLabel = ch.Label
			a.composeIdentityID = ch.IdentityID
			a.composeFromEmail = ch.Email
			a.composeFromName = ch.Name
			return
		}
	}
	ch := choices[0]
	a.composeAccountID = ch.AccountID
	a.composeAccountLabel = ch.Label
	a.composeIdentityID = ch.IdentityID
	a.composeFromEmail = ch.Email
	a.composeFromName = ch.Name
}

func (a *App) accountAllEmails(accountID string) map[string]bool {
	out := map[string]bool{}
	for _, acc := range a.cfg.Direct.Accounts {
		if acc.ID == accountID {
			if em := strings.ToLower(strings.TrimSpace(acc.Email)); em != "" {
				out[em] = true
			}
			for _, al := range acc.Aliases {
				if em := strings.ToLower(strings.TrimSpace(al.Email)); em != "" {
					out[em] = true
				}
			}
		}
	}
	for _, acc := range a.accounts {
		if acc.ID == accountID {
			for _, id := range acc.Identities {
				if em := strings.ToLower(strings.TrimSpace(id.Email)); em != "" {
					out[em] = true
				}
			}
		}
	}
	return out
}

// matchedRecipientAlias returns the first identity email of accountID found in to/cc.
func (a *App) matchedRecipientAlias(accountID string, lists ...[]string) string {
	own := a.accountAllEmails(accountID)
	if len(own) == 0 {
		return ""
	}
	for _, list := range lists {
		for _, raw := range list {
			addr := strings.ToLower(strings.TrimSpace(raw))
			if parsed, err := mail.ParseAddress(raw); err == nil && parsed.Address != "" {
				addr = strings.ToLower(strings.TrimSpace(parsed.Address))
			}
			if own[addr] {
				return addr
			}
		}
	}
	return ""
}

func (a *App) initComposeCommon(accountID string) {
	a.selectComposeIdentity(accountID, "")
	a.composeDraftID = ""
	a.composeInReplyTo = ""
	a.composeReferences = ""
	a.composeForwardHTML = ""
	a.composeForwardMsgID = ""
	a.composeForwardFrom = ""
	a.composeForwardDate = ""
	a.composeForwardSubj = ""
	a.composeForwardSep = ""
	a.composeKeepHTML = false
	a.composeAttachments = nil
	a.composeSourceAttachments = nil
	a.composeAttachInput = ""
	a.composeSuggestTo = nil
	a.composeSuggestCC = nil
	a.composeSuggestBCC = nil
	a.composeLastToQuery = ""
	a.composeLastCCQuery = ""
	a.composeLastBCCQuery = ""
	a.composeContactPickerOpen = false
	a.composeContactFilter = ""
	a.composeConfirm = false
	a.composeNonce = backend.NewIdempotencyKey()
	a.composeSign = 0
	a.composeEncrypt = false
	a.composeReplyEncrypted = false
	a.composeBaseline, a.composeCloseAsk = "", false
	a.composeBodyHTML = ""
	a.composeEditorDirty = false
	a.composeDirtyAt = time.Time{}
	a.composePrevSnap = ""
	a.composeAutoSaveLast = time.Time{}
	a.composeAutoSaveSavedAt = time.Time{}
	a.composeAutoSaveBusy = false
	a.composeAutoSaveSnap = ""
	a.composeMissingAttWarn = ""
	a.composeMissingAttAck = false
	a.composeTplOpen = false
	a.composeTplName = ""
	a.composeEditorGen++
	a.composeOpen = true
	a.composeWindowSeq++
	a.syncAuxWindows()
}

// composeSnapshot is the compose content that would be lost on closing.
func (a *App) composeSnapshot() string {
	srcNames := make([]string, len(a.composeSourceAttachments))
	for i, sa := range a.composeSourceAttachments {
		srcNames[i] = sa.MessageID + ":" + sa.PartName + ":" + sa.Name
	}
	dirtyFlag := ""
	if a.composeEditorDirty {
		dirtyFlag = "dirty"
	}
	return strings.Join([]string{a.composeTo, a.composeCC, a.composeBCC, a.composeSubject, a.composeBody, a.composeBodyHTML, dirtyFlag,
		strings.Join(a.composeAttachments, "\x00"), strings.Join(srcNames, "\x00")}, "\x01")
}

// closeCompose closes the compose window on purpose (sent, saved, discarded).
func (a *App) closeCompose() {
	a.composeOpen, a.composeCloseAsk, a.composeBaseline = false, false, ""
	a.composeDraftID = ""
	a.composeInReplyTo = ""
	a.composeReferences = ""
	a.composeForwardHTML = ""
	a.composeForwardMsgID = ""
	a.composeKeepHTML = false
	a.composeBodyHTML = ""
	a.composeEditorDirty = false
	a.composeDirtyAt = time.Time{}
	a.composePrevSnap = ""
	a.composeAutoSaveLast = time.Time{}
	a.composeAutoSaveSavedAt = time.Time{}
	a.composeAutoSaveBusy = false
	a.composeAutoSaveSnap = ""
	a.composeMissingAttWarn = ""
	a.composeMissingAttAck = false
	a.composeTplOpen = false
	a.composeTplName = ""
	a.composeContactPickerOpen = false
	a.composeContactFilter = ""
	a.syncAuxWindows()
}

// guardComposeClose runs every frame (UI thread): the window was closed by Escape, a click beside
// it or ✕ while it holds unsaved changes – it stays open and asks what to do with them.
func (a *App) guardComposeClose() {
	switch {
	case a.composeOpen && a.composeBaseline == "":
		a.composeBaseline = a.composeSnapshot()
	case !a.composeOpen && a.composeBaseline != "":
		if a.composeSnapshot() != a.composeBaseline {
			a.composeOpen, a.composeCloseAsk = true, true
			return
		}
		a.composeBaseline = ""
	}
}

func (a *App) OpenComposeNew() {
	a.initComposeCommon("")
	a.composeReplyTo = ""
	a.composeReplyType = ""
	a.composeTo = ""
	a.composeCC = ""
	a.composeBCC = ""
	a.composeSubject = ""
	sig, sigHTML, sigBelow := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
	a.composeBody = appendAccountSignature("", sig)
	a.composeBodyHTML = buildComposeEditorInitialHTMLWithSig("", composeSignatureBlockHTMLFromParts(sig, sigHTML), sigBelow, "", "", nil, "")
	a.composeEditorGen++
}

// OpenComposeFromMailto opens the compose dialog prefilled from a mailto: URI (UI thread).
func (a *App) OpenComposeFromMailto(rawURL string) {
	f, ok := ParseMailtoURL(rawURL)
	if !ok {
		return
	}
	a.OpenComposeNew()
	a.composeTo = f.To
	a.composeCC = f.CC
	a.composeBCC = f.BCC
	a.composeSubject = f.Subject
	if f.Body != "" {
		sig, sigHTML, sigBelow := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
		a.composeBody = appendAccountSignature(f.Body, sig)
		a.composeBodyHTML = buildComposeEditorInitialHTMLWithSig(f.Body, composeSignatureBlockHTMLFromParts(sig, sigHTML), sigBelow, "", "", nil, "")
		a.composeEditorGen++
	}
}

// OpenComposeFromDraft opens a saved draft message in the compose modal for editing & sending,
// restoring BCC, threading headers, attachments, and tracking the original draft ID so saving replaces it and sending deletes it.
func (a *App) OpenComposeFromDraft(msg *backend.MessageDetail) {
	if msg == nil {
		return
	}
	a.initComposeCommon(msg.AccountID)
	if parsed, err := mail.ParseAddress(msg.From); err == nil && parsed.Address != "" {
		a.selectComposeIdentity(msg.AccountID, parsed.Address)
	}
	a.composeDraftID = msg.ID
	a.composeReplyTo = ""
	a.composeReplyType = "draft"
	if msg.Headers != nil {
		a.composeInReplyTo = strings.TrimSpace(msg.Headers["in-reply-to"])
		a.composeReferences = strings.TrimSpace(msg.Headers["references"])
	}
	a.composeTo = strings.Join(msg.To, ", ")
	a.composeCC = strings.Join(msg.CC, ", ")
	a.composeBCC = strings.Join(msg.BCC, ", ")
	a.composeSubject = msg.Subject
	for _, att := range msg.Attachments {
		a.composeSourceAttachments = append(a.composeSourceAttachments, backend.SourceAttachment{
			MessageID: msg.ID,
			PartName:  att.PartName,
			Name:      att.Name,
		})
	}
	body := msg.BodyText
	if strings.TrimSpace(body) == "" && strings.TrimSpace(msg.BodyHtml) != "" {
		body, _ = htmlMailMarkdown(msg.BodyHtml)
	}
	a.composeBody = body
	if strings.TrimSpace(msg.BodyHtml) != "" {
		a.composeBodyHTML = SanitizeEmailHTML(msg.BodyHtml, nil, false)
	} else {
		a.composeBodyHTML = buildComposeEditorInitialHTML(body, "", "", "", nil, "")
	}
	a.composeEditorGen++
	if strings.Contains(strings.ToLower(msg.BodyHtml), "cid:") {
		a.loadInlineImagesForCompose(msg.ID, msg.BodyHtml, "", "", false, a.composeEditorGen)
	}
}

// isDraftsMessage reports whether msg resides in a Drafts folder.
func (a *App) isDraftsMessage(msg *backend.MessageDetail) bool {
	if msg == nil {
		return false
	}
	for _, f := range a.folders {
		if f.ID == msg.FolderID {
			if strings.EqualFold(f.Type, "drafts") || slices.Contains(f.SpecialUse, "drafts") {
				return true
			}
			base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(f.Path, "\\", "/"))))
			if base == "drafts" || base == "entwürfe" || base == "entwuerfe" {
				return true
			}
			return false
		}
	}
	base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(msg.Folder, "\\", "/"))))
	return base == "drafts" || base == "entwürfe" || base == "entwuerfe"
}

func (a *App) OpenComposeTo(to string) {
	a.OpenComposeNew()
	a.composeTo = strings.TrimSpace(to)
}

func (a *App) OpenComposeReply(msg *backend.MessageDetail, body string) {
	if msg == nil {
		return
	}
	a.initComposeCommon(msg.AccountID)
	if alias := a.matchedRecipientAlias(msg.AccountID, msg.To, msg.CC); alias != "" {
		a.selectComposeIdentity(msg.AccountID, alias)
	}
	a.composeReplyTo = msg.ID
	a.composeReplyType = "reply"
	a.composeTo = replyRecipient(msg)
	a.composeCC = ""
	a.composeBCC = ""
	subj := msg.Subject
	if low := strings.ToLower(subj); !strings.HasPrefix(low, "re:") && !strings.HasPrefix(low, "aw:") {
		subj = "Re: " + subj
	}
	a.composeSubject = subj
	sig, sigHTML, sigBelow := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
	quote := a.replyQuote(msg)
	a.composeBody = appendAccountSignatureWithQuote(body, sig, quote, sigBelow)
	a.composeBodyHTML = buildComposeEditorInitialHTMLWithSig(body, composeSignatureBlockHTMLFromParts(sig, sigHTML), sigBelow, "", "", nil, quote)
	a.composeEditorGen++
	a.composeReplyEncrypted = msg.Security.Encrypted
	if msg.Security.Encrypted && a.opts.Direct != nil {
		knownCert := map[string]bool{}
		for _, ci := range a.opts.Direct.ListSMIMEContacts() {
			if em := strings.ToLower(strings.TrimSpace(ci.Email)); em != "" {
				knownCert[em] = true
			}
			for _, em := range ci.Emails {
				knownCert[strings.ToLower(strings.TrimSpace(em))] = true
			}
		}
		for _, ci := range a.opts.Direct.ListSMIMEIdentities() {
			if em := strings.ToLower(strings.TrimSpace(ci.Email)); em != "" {
				knownCert[em] = true
			}
		}
		rcpts := splitEmailList(a.composeTo)
		if len(rcpts) > 0 {
			allHave := true
			for _, r := range rcpts {
				em := strings.ToLower(strings.TrimSpace(r))
				if addr, err := mail.ParseAddress(r); err == nil {
					em = strings.ToLower(strings.TrimSpace(addr.Address))
				}
				if !knownCert[em] {
					allHave = false
					break
				}
			}
			if allHave {
				a.composeEncrypt = true
			}
		}
	}
}

// maxQuoteRunes bounds the quoted original in a reply.
const maxQuoteRunes = 20000

// replyQuote is the original mail quoted below a reply ("Am … schrieb …:" and "> " lines).
// Encrypted mails are not quoted: the reply would carry their text unencrypted.
func (a *App) replyQuote(msg *backend.MessageDetail) string {
	if msg.Security.Encrypted {
		return ""
	}
	text := msg.BodyText
	if strings.TrimSpace(text) == "" && strings.TrimSpace(msg.BodyHtml) != "" {
		text, _ = htmlMailMarkdown(msg.BodyHtml)
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	if r := []rune(text); len(r) > maxQuoteRunes {
		text = string(r[:maxQuoteRunes]) + "\n[…]"
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(a.trf("Am %s schrieb %s:", "On %s, %s wrote:", formatDateTimeLang(msg.Date, a.uiLang()), msg.From))
	b.WriteString("\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, ">") {
			b.WriteString(">" + line + "\n")
		} else if line == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

// replyRecipient is where a reply goes: the Reply-To address when the mail names one (mailing
// lists, shops, helpdesks), else its sender.
func replyRecipient(msg *backend.MessageDetail) string {
	if rt := strings.TrimSpace(msg.Headers["reply-to"]); rt != "" {
		if list := splitEmailList(rt); len(list) > 0 {
			return strings.Join(list, ", ")
		}
	}
	return msg.From
}

func (a *App) OpenComposeReplyAll(msg *backend.MessageDetail, body string) {
	if msg == nil {
		return
	}
	a.OpenComposeReply(msg, body)
	a.composeReplyType = "replyAll"

	seen := a.accountAllEmails(a.composeAccountID)
	extractAddr := func(raw string) string {
		if addr, err := mail.ParseAddress(raw); err == nil {
			return strings.ToLower(strings.TrimSpace(addr.Address))
		}
		return strings.ToLower(strings.TrimSpace(raw))
	}
	if fromAddr := extractAddr(msg.From); fromAddr != "" {
		seen[fromAddr] = true
	}
	var cc []string
	addCC := func(list []string) {
		for _, raw := range list {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			key := extractAddr(raw)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			cc = append(cc, raw)
		}
	}
	addCC(msg.To)
	addCC(msg.CC)
	a.composeCC = strings.Join(cc, ", ")
}

func (a *App) OpenComposeForward(msg *backend.MessageDetail) {
	if msg == nil {
		return
	}
	a.initComposeCommon(msg.AccountID)
	if alias := a.matchedRecipientAlias(msg.AccountID, msg.To, msg.CC); alias != "" {
		a.selectComposeIdentity(msg.AccountID, alias)
	}
	a.composeReplyTo = msg.ID
	a.composeReplyType = "forward"
	a.composeTo = ""
	a.composeCC = ""
	a.composeBCC = ""
	subj := msg.Subject
	low := strings.ToLower(subj)
	if !strings.HasPrefix(low, "fwd:") && !strings.HasPrefix(low, "wg:") {
		subj = "Fwd: " + subj
	}
	a.composeSubject = subj
	dateStr := formatDateTimeLang(msg.Date, a.uiLang())
	sep := fmt.Sprintf("---------- %s ----------", a.tr("Weitergeleitete Nachricht", "Forwarded message"))
	header := fmt.Sprintf("\n\n%s\n%s %s\n%s %s\n%s %s\n\n%s",
		sep,
		a.tr("Von:", "From:"), msg.From,
		a.tr("Datum:", "Date:"), dateStr,
		a.tr("Betreff:", "Subject:"), msg.Subject,
		strings.TrimSpace(msg.BodyText),
	)
	sigPlain, sigCustomHTML, sigBelow := a.composeSignatureParts(a.composeAccountID, a.composeFromEmail)
	sig := stripSignatureDelim(sigPlain)
	if sig != "" {
		a.composeBody = "\n\n-- \n" + sig + header
	} else {
		a.composeBody = header
	}
	sigBlock := composeSignatureBlockHTMLFromParts(sigPlain, sigCustomHTML)
	// Preserve original attachments when forwarding (user can remove individual ones in the compose modal).
	for _, att := range msg.Attachments {
		a.composeSourceAttachments = append(a.composeSourceAttachments, backend.SourceAttachment{
			MessageID: msg.ID,
			PartName:  att.PartName,
			Name:      att.Name,
		})
	}
	// Preserve original HTML formatting as multipart/alternative unless the source mail was encrypted.
	if strings.TrimSpace(msg.BodyHtml) != "" && !msg.Security.Encrypted {
		a.composeForwardHTML = msg.BodyHtml
		a.composeForwardMsgID = msg.ID
		a.composeForwardFrom = msg.From
		a.composeForwardDate = dateStr
		a.composeForwardSubj = msg.Subject
		a.composeForwardSep = sep
		a.composeKeepHTML = true
		fwdMeta := fmt.Sprintf("%s\n%s %s\n%s %s\n%s %s", sep, a.tr("Von:", "From:"), msg.From, a.tr("Datum:", "Date:"), dateStr, a.tr("Betreff:", "Subject:"), msg.Subject)
		a.composeBodyHTML = buildComposeEditorInitialHTMLWithSig("", sigBlock, sigBelow, fwdMeta, msg.BodyHtml, nil, "")
		a.composeEditorGen++
		if strings.Contains(strings.ToLower(msg.BodyHtml), "cid:") {
			a.loadInlineImagesForCompose(msg.ID, msg.BodyHtml, fwdMeta, sigBlock, sigBelow, a.composeEditorGen)
		}
		return
	} else {
		a.composeBodyHTML = buildComposeEditorInitialHTMLWithSig("", sigBlock, sigBelow, "", "", nil, header)
	}
	a.composeEditorGen++
}

func (a *App) loadInlineImagesForCompose(msgID, rawHTML, fwdMeta, sigBlock string, sigBelow bool, gen int) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" || strings.TrimSpace(rawHTML) == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		imgs, err := dc.ReadAllInlineImages(c, msgID)
		if err != nil || len(imgs) == 0 {
			return
		}
		var updatedHTML string
		if fwdMeta != "" {
			updatedHTML = buildComposeEditorInitialHTMLWithSig("", sigBlock, sigBelow, fwdMeta, rawHTML, imgs, "")
		} else {
			updatedHTML = SanitizeEmailHTML(rawHTML, imgs, false)
		}
		a.emitUpdate(func() {
			if !a.composeOpen || a.composeEditorGen != gen || a.composeEditorDirty {
				return
			}
			a.composeBodyHTML = updatedHTML
			a.composeEditorGen++
		})
	}()
}

func lastRecipientToken(raw string) string {
	inQuotes := false
	lastComma := -1
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			inQuotes = !inQuotes
		case '\\':
			i++
		case ',':
			if !inQuotes {
				lastComma = i
			}
		}
	}
	if lastComma >= 0 {
		return strings.TrimSpace(raw[lastComma+1:])
	}
	return strings.TrimSpace(raw)
}

func (a *App) refreshComposeSuggestions(field, raw string) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	tok := lastRecipientToken(raw)
	switch field {
	case "to":
		if tok == a.composeLastToQuery {
			return
		}
		a.composeLastToQuery = tok
		if len(tok) < 2 {
			a.composeSuggestTo = nil
			return
		}
		a.composeSuggestTo = dc.SuggestRecipients(context.Background(), tok, 5)
	case "cc":
		if tok == a.composeLastCCQuery {
			return
		}
		a.composeLastCCQuery = tok
		if len(tok) < 2 {
			a.composeSuggestCC = nil
			return
		}
		a.composeSuggestCC = dc.SuggestRecipients(context.Background(), tok, 5)
	case "bcc":
		if tok == a.composeLastBCCQuery {
			return
		}
		a.composeLastBCCQuery = tok
		if len(tok) < 2 {
			a.composeSuggestBCC = nil
			return
		}
		a.composeSuggestBCC = dc.SuggestRecipients(context.Background(), tok, 5)
	}
}

func (a *App) applyComposeSuggestion(field string, s mailengine.RecipientSuggestion) {
	formatted := formatEmailAddress(s.DisplayName, s.Email)
	replaceLast := func(cur string) string {
		if idx := strings.LastIndex(cur, ","); idx >= 0 {
			prefix := strings.TrimSpace(cur[:idx])
			if prefix != "" {
				return prefix + ", " + formatted + ", "
			}
		}
		return formatted + ", "
	}
	switch field {
	case "to":
		a.composeTo = replaceLast(a.composeTo)
		a.composeSuggestTo = nil
		a.composeLastToQuery = ""
	case "cc":
		a.composeCC = replaceLast(a.composeCC)
		a.composeSuggestCC = nil
		a.composeLastCCQuery = ""
	case "bcc":
		a.composeBCC = replaceLast(a.composeBCC)
		a.composeSuggestBCC = nil
		a.composeLastBCCQuery = ""
	}
}

func (a *App) addComposeAttachment(rawPath string) {
	p := strings.TrimSpace(rawPath)
	if p == "" {
		return
	}
	for _, existing := range a.composeAttachments {
		if existing == p {
			a.composeAttachInput = ""
			return
		}
	}
	a.composeAttachments = append(a.composeAttachments, p)
	a.composeAttachInput = ""
}

func (a *App) removeComposeAttachment(idx int) {
	if idx < 0 || idx >= len(a.composeAttachments) {
		return
	}
	a.composeAttachments = append(a.composeAttachments[:idx], a.composeAttachments[idx+1:]...)
}

func (a *App) removeComposeSourceAttachment(idx int) {
	if idx < 0 || idx >= len(a.composeSourceAttachments) {
		return
	}
	a.composeSourceAttachments = append(a.composeSourceAttachments[:idx], a.composeSourceAttachments[idx+1:]...)
}

// composeSecuritySuffix names security flags on a sent mail: " (signiert, verschlüsselt)".
func (a *App) composeSecuritySuffix(sec *backend.ComposeSecurity) string {
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
	a.eventEditingID = ""
	a.eventEditCalID = ""
	a.eventEditingEv = backend.CalendarEvent{}
	a.eventTitle = ""
	a.eventStart = now.Format("2006-01-02T15:04")
	a.eventEnd = now.Add(time.Hour).Format("2006-01-02T15:04")
	a.eventAllDay = false
	a.eventLocation = ""
	a.eventDesc = ""
	a.eventRecurrence = 0
	a.eventRecCount = ""
	a.eventRecUntil = ""
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
	a.eventWindowSeq++
	a.syncAuxWindows()
}

// OpenNewEventOnDate opens the new-event modal pre-filled for a specific calendar day (09:00–10:00).
func (a *App) OpenNewEventOnDate(day time.Time) {
	a.OpenNewEventModal(nil)
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, day.Location())
	a.eventStart = start.Format("2006-01-02T15:04")
	a.eventEnd = start.Add(time.Hour).Format("2006-01-02T15:04")
}

// OpenEditEventModal opens the event modal to edit an existing writable calendar event.
func (a *App) OpenEditEventModal(ev backend.CalendarEvent) {
	if ev.ReadOnly {
		a.toast(a.tr("Dieser Kalender ist schreibgeschützt (Abo).", "This calendar is read-only (subscription)."), "info")
		return
	}
	a.eventEditingID = ev.ID
	a.eventEditCalID = ev.CalendarID
	a.eventEditingEv = ev
	a.eventTitle = ev.Title
	a.eventStart = formatDateTimeInput(ev.Start)
	a.eventEnd = formatDateTimeInput(ev.End)
	a.eventAllDay = ev.AllDay
	a.eventLocation = ev.Location
	a.eventDesc = ev.Description
	a.eventRecurrence, a.eventRecCount, a.eventRecUntil = parseEventRRule(ev.RecurrenceRule)
	// Match calendar label
	for lbl, id := range a.eventCalByLabel {
		if id == ev.CalendarID {
			a.eventCalendar = lbl
			break
		}
	}
	a.eventOpen = true
	a.eventWindowSeq++
	a.syncAuxWindows()
}

func formatDateTimeInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if t, err := parseEventTime(raw); err == nil {
		return t.Local().Format("2006-01-02T15:04")
	}
	raw = strings.TrimSuffix(raw, "Z")
	if len(raw) > 16 {
		return raw[:16]
	}
	return raw
}

func buildEventRRule(recIdx int, countStr, untilStr string) string {
	var freq string
	switch recIdx {
	case 1:
		freq = "DAILY"
	case 2:
		freq = "WEEKLY"
	case 3:
		freq = "MONTHLY"
	case 4:
		freq = "YEARLY"
	default:
		return ""
	}
	parts := []string{"FREQ=" + freq}
	if n, err := strconv.Atoi(strings.TrimSpace(countStr)); err == nil && n > 0 {
		parts = append(parts, fmt.Sprintf("COUNT=%d", n))
	} else if u := strings.TrimSpace(untilStr); u != "" {
		if parsed, err := time.Parse("2006-01-02", u); err == nil {
			parts = append(parts, "UNTIL="+parsed.Format("20060102")+"T235959Z")
		}
	}
	return strings.Join(parts, ";")
}

func parseEventRRule(rrule string) (recIdx int, countStr, untilStr string) {
	raw := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(rrule)), "RRULE:")
	if raw == "" {
		return 0, "", ""
	}
	for _, part := range strings.Split(raw, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "FREQ":
			switch strings.TrimSpace(v) {
			case "DAILY":
				recIdx = 1
			case "WEEKLY":
				recIdx = 2
			case "MONTHLY":
				recIdx = 3
			case "YEARLY":
				recIdx = 4
			}
		case "COUNT":
			countStr = strings.TrimSpace(v)
		case "UNTIL":
			v = strings.TrimSpace(v)
			if len(v) >= 8 {
				untilStr = v[:4] + "-" + v[4:6] + "-" + v[6:8]
			}
		}
	}
	return recIdx, countStr, untilStr
}

// SaveCalendarEvent creates a new event or updates an existing event from the modal fields.
func (a *App) SaveCalendarEvent(ctx context.Context) {
	if a.eventBusy {
		return
	}
	title := strings.TrimSpace(a.eventTitle)
	if title == "" {
		a.toast(a.tr("Bitte einen Titel eingeben.", "Please enter a title."), "error")
		return
	}
	start := normalizeDateTimeInput(a.eventStart)
	end := normalizeDateTimeInput(a.eventEnd)
	rrule := buildEventRRule(a.eventRecurrence, a.eventRecCount, a.eventRecUntil)

	if a.eventEditingID != "" {
		calID := a.eventEditCalID
		if calID == "" {
			calID = a.eventCalByLabel[a.eventCalendar]
		}
		patch := map[string]any{
			"title":          title,
			"start":          start,
			"end":            end,
			"allDay":         a.eventAllDay,
			"location":       strings.TrimSpace(a.eventLocation),
			"description":    strings.TrimSpace(a.eventDesc),
			"rrule":          rrule,
			"recurrenceRule": rrule,
		}
		a.UpdateCalendarEvent(ctx, calID, a.eventEditingID, patch)
		return
	}

	a.CreateCalendarEvent(ctx, backend.CreateCalendarEventRequest{
		CalendarID:     a.eventCalByLabel[a.eventCalendar],
		Title:          title,
		Start:          start,
		End:            end,
		AllDay:         a.eventAllDay,
		Location:       strings.TrimSpace(a.eventLocation),
		Description:    strings.TrimSpace(a.eventDesc),
		RecurrenceRule: rrule,
	})
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
		if ev.AllDay || ev.ID == "" || a.isCalendarHidden(ev.CalendarID) {
			continue
		}
		start, err := parseEventTime(ev.Start)
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

// parseUnsubscribeTargets extracts https:// and mailto: URIs from RFC 2369 List-Unsubscribe
// and checks RFC 8058 List-Unsubscribe-Post one-click support.
func parseUnsubscribeTargets(msg *backend.MessageDetail) (httpsURL, mailtoURI string, oneClick bool) {
	if msg == nil || len(msg.Headers) == 0 {
		return "", "", false
	}
	raw := strings.TrimSpace(msg.Headers["list-unsubscribe"])
	if raw == "" {
		return "", "", false
	}
	postHdr := strings.ToLower(msg.Headers["list-unsubscribe-post"])
	oneClick = strings.Contains(postHdr, "list-unsubscribe=one-click")
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		s = strings.TrimPrefix(s, "<")
		s = strings.TrimSuffix(s, ">")
		s = strings.TrimSpace(s)
		low := strings.ToLower(s)
		if strings.HasPrefix(low, "https://") && httpsURL == "" {
			httpsURL = s
		} else if strings.HasPrefix(low, "mailto:") && mailtoURI == "" {
			mailtoURI = s
		}
	}
	return httpsURL, mailtoURI, oneClick
}

// PerformUnsubscribe triggers RFC 8058 one-click HTTPS POST, opens the https unsubscribe URL in
// the system browser, or opens a prefilled mailto unsubscribe compose draft.
func (a *App) PerformUnsubscribe(ctx context.Context, msg *backend.MessageDetail) {
	httpsURL, mailtoURI, oneClick := parseUnsubscribeTargets(msg)
	if httpsURL != "" && oneClick {
		go func(u string) {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(c, http.MethodPost, u, strings.NewReader("List-Unsubscribe=One-Click"))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, rerr := (&http.Client{Timeout: 10 * time.Second}).Do(req)
				if rerr == nil {
					_ = resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						a.emitUpdate(func() {
							a.toast(a.tr("Newsletter-Abmeldung gesendet", "Unsubscribe request sent"), "success")
						})
						return
					}
				}
			}
			_ = openExternalURL(u)
		}(httpsURL)
		return
	}
	if httpsURL != "" {
		_ = openExternalURL(httpsURL)
		return
	}
	if mailtoURI != "" {
		u, err := url.Parse(mailtoURI)
		if err != nil {
			return
		}
		accID := msg.AccountID
		a.initComposeCommon(accID)
		a.composeTo = u.Opaque
		if subj := strings.TrimSpace(u.Query().Get("subject")); subj != "" {
			a.composeSubject = subj
		} else {
			a.composeSubject = "Unsubscribe"
		}
		body := strings.TrimSpace(u.Query().Get("body"))
		a.composeBody = body
		a.composeBodyHTML = buildComposeEditorInitialHTML(body, "", "", "", nil, "")
		a.composeEditorGen++
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

func (a *App) todayEvents() []backend.CalendarEvent {
	now := time.Now()
	y, m, d := now.Date()
	var out []backend.CalendarEvent
	for _, ev := range a.events {
		if a.isCalendarHidden(ev.CalendarID) {
			continue
		}
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
	if !a.engineReady {
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
	if !a.engineReady {
		return a.tr("MailDiver — Mail-Dienst offline", "MailDiver — Mail engine offline")
	}
	if !a.engineReady {
		return a.tr("MailDiver — Verbinde Postfächer…", "MailDiver — Connecting mailboxes…")
	}
	today := a.todayEvents()
	return a.trf(
		"MailDiver Live · %d ungelesen · %d Action · %d Termine heute",
		"MailDiver Live · %d unread · %d action · %d events today",
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
