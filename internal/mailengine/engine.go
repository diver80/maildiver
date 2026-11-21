package mailengine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/oauth2"

	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/secretstore"
	"github.com/diver80/maildiver/internal/smime"
)

// Options configures a standalone direct mail/calendar Engine.
type Options struct {
	DBPath       string
	Secrets      secretstore.Store
	Events       *events.Hub
	ConfigLoader func() (config.Config, error)
	ConfigSaver  func(config.Config) error
}

// AccountSyncStatus reports live sync state for one configured IMAP/SMTP account.
type AccountSyncStatus struct {
	AccountID    string
	Name         string
	Email        string
	Enabled      bool
	State        string // "idle", "syncing", "error", "stopped"
	LastSync     time.Time
	LastError    string
	MessageCount int
}

// AccountTestResult holds independent IMAP & SMTP connection test results.
type AccountTestResult struct {
	IMAPOK      bool
	IMAPFolders int
	IMAPError   string
	SMTPOK      bool
	SMTPError   string
}

// Engine is the standalone Go IMAP/SMTP + SQLite + iCal + Google Calendar/Contacts backend.
// It implements bridge.RPCClient directly so the entire MailDiver native UI, AI
// assistant, triage, archive, and Quick-Rule stack runs in-process.
type Engine struct {
	store   *Store
	secrets secretstore.Store
	smime   *smime.Manager
	events  *events.Hub
	loadCfg func() (config.Config, error)
	saveCfg func(config.Config) error

	// Test hooks for Google Calendar/Contacts/iCal httptest mocking.
	gcalAPIBase        string
	gcalPeopleBase     string
	gcalEndpoint       oauth2.Endpoint
	disableBrowserOpen bool
	allowLoopbackICal  bool

	cfgMu           sync.Mutex
	mu              sync.RWMutex
	wg              sync.WaitGroup
	rootCtx         context.Context
	rootCancel      context.CancelFunc
	workers         map[string]context.CancelFunc
	accState        map[string]string
	accLastSync     map[string]time.Time
	accLastErr      map[string]string
	syncLocks       map[string]*sync.Mutex
	gcalLastErr     string
	gcalOAuthCancel context.CancelFunc
	started         bool
	closed          bool
}

// New creates an Engine and opens its local SQLite database.
func New(opts Options) (*Engine, error) {
	dbPath := opts.DBPath
	if dbPath == "" {
		p, err := config.DirectDBPath()
		if err != nil {
			return nil, err
		}
		if legacyPath, lerr := config.LegacyDirectDBPath(); lerr == nil && legacyPath != "" && legacyPath != p {
			migrateDBIfNeeded(legacyPath, p)
		}
		dbPath = p
	}
	st, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	secrets := opts.Secrets
	if secrets == nil {
		secrets = secretstore.Default()
	}
	ev := opts.Events
	if ev == nil {
		ev = events.NewHub()
	}
	loadCfg := opts.ConfigLoader
	if loadCfg == nil {
		loadCfg = config.Load
	}
	saveCfg := opts.ConfigSaver
	if saveCfg == nil {
		saveCfg = config.Save
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	smimeDir := filepath.Join(filepath.Dir(dbPath), "smime")
	return &Engine{
		store:       st,
		secrets:     secrets,
		smime:       smime.NewManager(smimeDir, secrets),
		events:      ev,
		loadCfg:     loadCfg,
		saveCfg:     saveCfg,
		rootCtx:     rootCtx,
		rootCancel:  rootCancel,
		workers:     map[string]context.CancelFunc{},
		accState:    map[string]string{},
		accLastSync: map[string]time.Time{},
		accLastErr:  map[string]string{},
		syncLocks:   map[string]*sync.Mutex{},
	}, nil
}

// Store returns the underlying SQLite Store.
func (e *Engine) Store() *Store { return e.store }

// Events returns the hub publishing live engine events to the app.
func (e *Engine) Events() *events.Hub { return e.events }

// SMIME returns the engine's S/MIME certificate & key manager.
func (e *Engine) SMIME() *smime.Manager { return e.smime }

// ImportSMIMEP12File imports a personal .p12/.pfx certificate bundle from disk into the OS Keychain.
func (e *Engine) ImportSMIMEP12File(path, password string) (smime.CertInfo, error) {
	if e.smime == nil {
		return smime.CertInfo{}, fmt.Errorf("S/MIME nicht initialisiert")
	}
	raw, err := readBoundedRegularFile(expandHomePath(path), 5*1024*1024)
	if err != nil {
		return smime.CertInfo{}, err
	}
	return e.smime.ImportPKCS12(raw, password)
}

// ImportSMIMECertFile imports a contact's public .cer/.crt/.pem certificate from disk.
func (e *Engine) ImportSMIMECertFile(path string) (smime.CertInfo, error) {
	if e.smime == nil {
		return smime.CertInfo{}, fmt.Errorf("S/MIME nicht initialisiert")
	}
	raw, err := readBoundedRegularFile(expandHomePath(path), 2*1024*1024)
	if err != nil {
		return smime.CertInfo{}, err
	}
	return e.smime.ImportPublicCert(raw)
}

func (e *Engine) ListSMIMEIdentities() []smime.CertInfo {
	if e.smime == nil {
		return nil
	}
	return e.smime.ListIdentities()
}

func (e *Engine) ListSMIMEContacts() []smime.CertInfo {
	if e.smime == nil {
		return nil
	}
	return e.smime.ListContactCerts()
}

func (e *Engine) DeleteSMIMEIdentity(email string) error {
	if e.smime == nil {
		return nil
	}
	return e.smime.DeleteIdentity(email)
}

func (e *Engine) DeleteSMIMEContact(email string) error {
	if e.smime == nil {
		return nil
	}
	return e.smime.DeleteContactCert(email)
}

// Connected implements bridge.RPCClient; direct engine is always connected locally.
func (e *Engine) Connected() bool { return e != nil && e.store != nil }

func (e *Engine) rootContext() context.Context {
	e.mu.RLock()
	ctx := e.rootCtx
	e.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// spawn runs fn in a goroutine tracked by e.wg and cancelled on Close().
func (e *Engine) spawn(fn func(ctx context.Context)) {
	e.mu.RLock()
	if e.closed || e.rootCtx == nil {
		e.mu.RUnlock()
		return
	}
	ctx := e.rootCtx
	e.wg.Add(1)
	e.mu.RUnlock()
	go func() {
		defer e.wg.Done()
		fn(ctx)
	}()
}

// Start begins background IMAP IDLE workers and periodic calendar sync.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started || e.closed {
		e.mu.Unlock()
		return
	}
	e.started = true
	rootCancel := e.rootCancel
	e.mu.Unlock()

	if ctx != nil && ctx.Done() != nil {
		e.spawn(func(rctx context.Context) {
			select {
			case <-ctx.Done():
				rootCancel()
			case <-rctx.Done():
			}
		})
	}

	e.reconcileAccountWorkers()

	e.spawn(func(rctx context.Context) {
		e.runCalendarSyncLoop(rctx)
	})
	e.spawn(func(rctx context.Context) {
		e.runOutboxLoop(rctx)
	})
}

func (e *Engine) runOutboxLoop(ctx context.Context) {
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = e.FlushOutbox(ctx)
		}
	}
}

// Close stops all background IMAP IDLE / calendar workers, waits for them to exit, and closes SQLite.
func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	e.started = false
	if e.rootCancel != nil {
		e.rootCancel()
	}
	for id, cancel := range e.workers {
		cancel()
		delete(e.workers, id)
	}
	e.mu.Unlock()

	e.wg.Wait()
	return e.store.Close()
}

func (e *Engine) loadConfig() config.Config {
	cfg, err := e.loadCfg()
	if err != nil {
		return config.Default()
	}
	return config.Normalize(cfg)
}

func (e *Engine) saveConfig(cfg config.Config) error {
	return e.saveCfg(config.Normalize(cfg))
}

func (e *Engine) accountSecretKey(accountID string) string {
	return "account:" + accountID
}

func (e *Engine) accountPassword(accountID string) (string, error) {
	if acc, ok := e.findAccount(accountID); ok && strings.EqualFold(strings.TrimSpace(acc.AuthType), "xoauth2") {
		ctx, cancel := context.WithTimeout(e.rootCtx, 20*time.Second)
		defer cancel()
		return e.resolveAccountSecret(ctx, acc)
	}
	return e.secrets.Get(e.accountSecretKey(accountID))
}

func (e *Engine) findAccount(accountID string) (config.DirectAccount, bool) {
	cfg := e.loadConfig()
	for _, a := range cfg.Direct.Accounts {
		if a.ID == accountID {
			return a, true
		}
	}
	return config.DirectAccount{}, false
}

func (e *Engine) accountSyncLock(accountID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if lk, ok := e.syncLocks[accountID]; ok {
		return lk
	}
	lk := &sync.Mutex{}
	e.syncLocks[accountID] = lk
	return lk
}

func (e *Engine) setAccountState(accountID, state, lastErr string) {
	e.mu.Lock()
	e.accState[accountID] = state
	if lastErr != "" || state == "idle" {
		e.accLastErr[accountID] = lastErr
	}
	if state == "idle" {
		e.accLastSync[accountID] = time.Now()
	}
	e.mu.Unlock()
}

// ReloadAccounts starts syncing accounts that came into the configuration from elsewhere (a
// transfer from another device) and stops those that left it.
func (e *Engine) ReloadAccounts() {
	e.reconcileAccountWorkers()
	e.emitMailSync("")
}

func (e *Engine) reconcileAccountWorkers() {
	cfg := e.loadConfig()
	e.mu.Lock()
	if !e.started || e.closed || e.rootCtx == nil {
		e.mu.Unlock()
		return
	}
	want := map[string]config.DirectAccount{}
	for _, acc := range cfg.Direct.Accounts {
		if acc.Enabled && acc.ID != "" && acc.IMAPHost != "" {
			want[acc.ID] = acc
		}
	}
	for id, cancel := range e.workers {
		if _, ok := want[id]; !ok {
			cancel()
			delete(e.workers, id)
			e.accState[id] = "stopped"
		}
	}
	for id, acc := range want {
		if _, running := e.workers[id]; !running {
			wctx, cancel := context.WithCancel(e.rootCtx)
			e.workers[id] = cancel
			aCopy := acc
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				e.runIMAPIdleLoop(wctx, aCopy)
			}()
		}
	}
	e.mu.Unlock()
}

// AccountsStatus returns live sync status for all configured DirectAccounts.
func (e *Engine) AccountsStatus(ctx context.Context) []AccountSyncStatus {
	cfg := e.loadConfig()
	out := make([]AccountSyncStatus, 0, len(cfg.Direct.Accounts))
	e.mu.RLock()
	for _, a := range cfg.Direct.Accounts {
		st := e.accState[a.ID]
		if st == "" {
			if a.Enabled {
				st = "idle"
			} else {
				st = "stopped"
			}
		}
		out = append(out, AccountSyncStatus{
			AccountID: a.ID,
			Name:      a.Name,
			Email:     a.Email,
			Enabled:   a.Enabled,
			State:     st,
			LastSync:  e.accLastSync[a.ID],
			LastError: e.accLastErr[a.ID],
		})
	}
	e.mu.RUnlock()
	for i := range out {
		out[i].MessageCount = e.store.CountAccountMessages(ctx, out[i].AccountID)
	}
	return out
}

// TestAccount tests both IMAP and SMTP credentials/TLS before saving.
func (e *Engine) TestAccount(ctx context.Context, acc config.DirectAccount, password string) AccountTestResult {
	var res AccountTestResult
	if password == "" && acc.ID != "" {
		password, _ = e.accountPassword(acc.ID)
	}
	n, err := TestIMAPConnection(ctx, acc, password)
	if err != nil {
		res.IMAPError = err.Error()
	} else {
		res.IMAPOK = true
		res.IMAPFolders = n
	}
	if strings.TrimSpace(acc.SMTPHost) != "" {
		if err := TestSMTPConnection(ctx, acc, password); err != nil {
			res.SMTPError = err.Error()
		} else {
			res.SMTPOK = true
		}
	} else {
		res.SMTPOK = true
	}
	return res
}

// SaveAccount creates or updates a DirectAccount, stores its password in secretstore,
// enables write/send access for the user-owned direct account, and starts syncing immediately.
func (e *Engine) SaveAccount(ctx context.Context, acc config.DirectAccount, password string) (config.DirectAccount, error) {
	acc.Email = strings.TrimSpace(acc.Email)
	acc.IMAPHost = strings.TrimSpace(acc.IMAPHost)
	acc.SMTPHost = strings.TrimSpace(acc.SMTPHost)
	if acc.Email == "" || acc.IMAPHost == "" {
		return acc, fmt.Errorf("E-Mail-Adresse und IMAP-Server sind erforderlich")
	}
	if acc.ID == "" {
		slug := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			if r >= 'A' && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return '-'
		}, acc.Email)
		acc.ID = "acc-" + strings.Trim(slug, "-") + "-" + randomHex(4)
	}
	if acc.Name == "" {
		acc.Name = acc.Email
	}
	if acc.IMAPPort <= 0 {
		acc.IMAPPort = 993
	}
	if acc.SMTPPort <= 0 {
		acc.SMTPPort = 465
	}
	if acc.IMAPTLS == "" {
		acc.IMAPTLS = "tls"
	}
	if acc.SMTPTLS == "" {
		acc.SMTPTLS = "tls"
	}
	acc.Enabled = true

	if password != "" {
		if err := e.secrets.Set(e.accountSecretKey(acc.ID), password); err != nil {
			return acc, fmt.Errorf("Passwort speichern fehlgeschlagen: %w", err)
		}
	}

	e.cfgMu.Lock()
	cfg := e.loadConfig()
	cfg.Direct.Mode = config.EngineDirect
	// In standalone Direct Mode, user-added IMAP/SMTP accounts are writable and send-capable.
	cfg.Mailbox.ReadOnly = false

	updated := false
	for i := range cfg.Direct.Accounts {
		if cfg.Direct.Accounts[i].ID == acc.ID {
			prev := cfg.Direct.Accounts[i]
			if acc.SyncDays <= 0 {
				acc.SyncDays = prev.SyncDays
			}
			if acc.SyncLimit <= 0 {
				acc.SyncLimit = prev.SyncLimit
			}
			if len(prev.Aliases) > 0 && len(acc.Aliases) > 0 {
				prevSig := map[string]string{}
				for _, pa := range prev.Aliases {
					if sig := strings.TrimSpace(pa.Signature); sig != "" {
						prevSig[strings.ToLower(strings.TrimSpace(pa.Email))] = pa.Signature
					}
				}
				for j := range acc.Aliases {
					if strings.TrimSpace(acc.Aliases[j].Signature) == "" {
						if sig, ok := prevSig[strings.ToLower(strings.TrimSpace(acc.Aliases[j].Email))]; ok {
							acc.Aliases[j].Signature = sig
						}
					}
				}
			}
			cfg.Direct.Accounts[i] = acc
			updated = true
			break
		}
	}
	if !updated {
		cfg.Direct.Accounts = append(cfg.Direct.Accounts, acc)
	}
	if !cfg.Mailbox.AccountEnabled(acc.ID) {
		cfg.Mailbox.TriageAccountIDs = append(cfg.Mailbox.TriageAccountIDs, acc.ID)
	}
	saveErr := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if saveErr != nil {
		return acc, saveErr
	}

	// Restart worker for this account so updated settings take effect immediately.
	e.mu.Lock()
	if cancel, ok := e.workers[acc.ID]; ok {
		cancel()
		delete(e.workers, acc.ID)
	}
	e.mu.Unlock()
	e.reconcileAccountWorkers()

	e.emitMailSync(acc.ID)
	return acc, nil
}

// DeleteAccount removes an IMAP/SMTP account, its password from Keychain/secrets, and local SQLite cache.
func (e *Engine) DeleteAccount(ctx context.Context, accountID string) error {
	e.mu.Lock()
	if cancel, ok := e.workers[accountID]; ok {
		cancel()
		delete(e.workers, accountID)
	}
	delete(e.accState, accountID)
	delete(e.accLastErr, accountID)
	delete(e.accLastSync, accountID)
	e.mu.Unlock()

	// Remove from config first so no new sync can start for this account.
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	var removedEmails []string
	keptEmails := map[string]bool{}
	filtered := make([]config.DirectAccount, 0, len(cfg.Direct.Accounts))
	for _, a := range cfg.Direct.Accounts {
		if a.ID == accountID {
			if em := strings.ToLower(strings.TrimSpace(a.Email)); em != "" {
				removedEmails = append(removedEmails, em)
			}
			for _, al := range a.Aliases {
				if em := strings.ToLower(strings.TrimSpace(al.Email)); em != "" {
					removedEmails = append(removedEmails, em)
				}
			}
			continue
		}
		if em := strings.ToLower(strings.TrimSpace(a.Email)); em != "" {
			keptEmails[em] = true
		}
		for _, al := range a.Aliases {
			if em := strings.ToLower(strings.TrimSpace(al.Email)); em != "" {
				keptEmails[em] = true
			}
		}
		filtered = append(filtered, a)
	}
	cfg.Direct.Accounts = filtered
	triageIDs := cfg.Mailbox.TriageAccountIDs[:0]
	for _, id := range cfg.Mailbox.TriageAccountIDs {
		if id != accountID {
			triageIDs = append(triageIDs, id)
		}
	}
	cfg.Mailbox.TriageAccountIDs = triageIDs
	saveErr := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if saveErr != nil {
		return saveErr
	}

	// Serialize with any in-flight SyncAccount so it cannot resurrect rows after deletion.
	lk := e.accountSyncLock(accountID)
	lk.Lock()
	_ = e.secrets.Delete(e.accountSecretKey(accountID))
	_ = e.store.DeleteAccountData(ctx, accountID)
	if e.smime != nil {
		for _, em := range removedEmails {
			if !keptEmails[em] {
				_ = e.smime.DeleteIdentity(em)
			}
		}
	}
	lk.Unlock()

	e.emitMailSync(accountID)
	return nil
}

// ReorderAccounts persists a new display order for configured DirectAccounts.
// Accounts not listed in orderedIDs are preserved at the end in their existing relative order.
func (e *Engine) ReorderAccounts(_ context.Context, orderedIDs []string) error {
	if len(orderedIDs) == 0 {
		return nil
	}
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	byID := make(map[string]config.DirectAccount, len(cfg.Direct.Accounts))
	for _, a := range cfg.Direct.Accounts {
		byID[a.ID] = a
	}
	reordered := make([]config.DirectAccount, 0, len(cfg.Direct.Accounts))
	seen := make(map[string]bool, len(orderedIDs))
	for _, id := range orderedIDs {
		if acc, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			reordered = append(reordered, acc)
		}
	}
	for _, a := range cfg.Direct.Accounts {
		if !seen[a.ID] {
			reordered = append(reordered, a)
		}
	}
	cfg.Direct.Accounts = reordered
	err := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if err != nil {
		return err
	}
	e.emitMailSync("")
	return nil
}

// SyncAccount runs a serialized IMAP sync for one account and emits live SSE events.
func (e *Engine) SyncAccount(ctx context.Context, accountID string, inboxOnly bool) (SyncResult, error) {
	acc, ok := e.findAccount(accountID)
	if !ok {
		return SyncResult{}, fmt.Errorf("not_found: account %q", accountID)
	}
	lk := e.accountSyncLock(accountID)
	lk.Lock()
	defer lk.Unlock()

	// Re-check account existence after acquiring lock in case DeleteAccount ran concurrently.
	acc, ok = e.findAccount(accountID)
	if !ok {
		return SyncResult{}, fmt.Errorf("not_found: account %q", accountID)
	}

	e.mu.RLock()
	prevState := e.accState[accountID]
	e.mu.RUnlock()
	e.setAccountState(accountID, "syncing", "")
	res, err := e.syncAccountIMAP(ctx, acc, inboxOnly)
	if err != nil && ctx.Err() != nil {
		// The caller stopped waiting (its timeout, the system ending a background task): not
		// a fault of the account, whose worker syncs on.
		e.mu.Lock()
		e.accState[accountID] = prevState
		e.mu.Unlock()
		return res, err
	}
	if err != nil {
		e.setAccountState(accountID, "error", err.Error())
		if e.events != nil && ctx.Err() == nil {
			e.events.Publish("mail.sync.error", map[string]any{"accountId": accountID, "error": err.Error()})
		}
		return res, err
	}
	e.setAccountState(accountID, "idle", "")

	if len(res.NewMessages) > 0 {
		payloadMsgs := make([]map[string]any, 0, len(res.NewMessages))
		for _, m := range res.NewMessages {
			payloadMsgs = append(payloadMsgs, map[string]any{
				"id":        m.ID,
				"accountId": m.AccountID,
				"folder":    m.FolderPath,
				"subject":   m.Subject,
				"from":      m.From,
				"date":      m.DateISO,
			})
		}
		e.events.Publish("mail.new", map[string]any{
			"accountId": accountID,
			"count":     len(payloadMsgs),
			"messages":  payloadMsgs,
		})
	}
	e.emitMailSync(accountID)
	return res, nil
}

// SyncNow syncs accountID (or every enabled account if empty) now. ctx bounds only how long the
// caller waits: the sync itself runs to its end in the background, as a first sync of a large
// mailbox takes minutes and an aborted one leaves its work to the next. When ctx ends first,
// SyncNow returns ctx's error.
func (e *Engine) SyncNow(ctx context.Context, accountID string) error {
	done := make(chan error, 1)
	e.spawn(func(root context.Context) { done <- e.syncNow(root, accountID) })
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-e.rootContext().Done():
		return context.Canceled
	}
}

func (e *Engine) syncNow(ctx context.Context, accountID string) error {
	if accountID != "" {
		_, err := e.SyncAccount(ctx, accountID, false)
		return err
	}
	cfg := e.loadConfig()
	var firstErr error
	for _, a := range cfg.Direct.Accounts {
		if !a.Enabled {
			continue
		}
		if _, err := e.SyncAccount(ctx, a.ID, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// AddICSFeed saves a new iCal subscription feed and immediately syncs it.
func (e *Engine) AddICSFeed(ctx context.Context, name, rawURL string) (int, error) {
	name = strings.TrimSpace(name)
	rawURL = strings.TrimSpace(rawURL)
	if name == "" || rawURL == "" {
		return 0, fmt.Errorf("Name und URL/Pfad sind erforderlich")
	}
	feed := config.ICalFeedConfig{
		ID:       "feed-" + randomHex(6),
		Name:     name,
		URL:      rawURL,
		Color:    "#73daca",
		ReadOnly: true,
		Enabled:  true,
	}
	n, err := e.SyncICalFeed(ctx, feed)
	if err != nil {
		return 0, err
	}
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	cfg.Direct.ICalFeeds = append(cfg.Direct.ICalFeeds, feed)
	saveErr := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if saveErr != nil {
		return n, saveErr
	}
	return n, nil
}

// DeleteICSFeed removes a subscribed iCal feed and its cached calendar/events.
func (e *Engine) DeleteICSFeed(ctx context.Context, feedID string) error {
	_ = e.store.DeleteCalendar(ctx, "ical-"+feedID)
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	out := cfg.Direct.ICalFeeds[:0]
	for _, f := range cfg.Direct.ICalFeeds {
		if f.ID != feedID {
			out = append(out, f)
		}
	}
	cfg.Direct.ICalFeeds = out
	saveErr := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	e.emitCalendarChanged()
	return nil
}

// SyncCalendarsNow refreshes Google Calendar (if connected) and all enabled iCal feeds.
func (e *Engine) SyncCalendarsNow(ctx context.Context) error {
	cfg := e.loadConfig()
	var firstErr error
	if cfg.Direct.GoogleCalendar.Enabled && cfg.Direct.GoogleCalendar.ClientID != "" {
		if _, err := e.secrets.Get(secretGCalToken); err == nil {
			if err := e.SyncGoogleCalendar(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, f := range cfg.Direct.ICalFeeds {
		if !f.Enabled {
			continue
		}
		if _, err := e.SyncICalFeed(ctx, f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := e.SyncAllDAV(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (e *Engine) runCalendarSyncLoop(ctx context.Context) {
	// Initial calendar sync shortly after startup.
	select {
	case <-ctx.Done():
		return
	case <-time.After(1500 * time.Millisecond):
		_ = e.SyncCalendarsNow(ctx)
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.SyncCalendarsNow(ctx)
		}
	}
}

func (e *Engine) emitMailSync(accountID string) {
	if e.events == nil {
		return
	}
	e.events.Publish("mail.sync", map[string]any{
		"accountId": accountID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (e *Engine) emitCalendarChanged() {
	if e.events == nil {
		return
	}
	e.events.Publish("calendar.changed", map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (e *Engine) emitOutboxChanged() {
	if e.events == nil {
		return
	}
	count := 0
	if items, err := e.store.ListOutbox(context.Background()); err == nil {
		count = len(items)
	}
	e.events.Publish("outbox.changed", map[string]any{
		"count":     count,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (e *Engine) resolveTargetFolderPath(ctx context.Context, accountID, target string) string {
	t := strings.TrimSpace(strings.TrimPrefix(target, "/"))
	low := strings.ToLower(t)
	if low == "trash" || low == "papierkorb" {
		if f, ok := e.store.FindFolderByType(ctx, accountID, "trash"); ok {
			return f.Path
		}
		return "Trash"
	}
	if low == "archive" || low == "archiv" {
		if f, ok := e.store.FindFolderByType(ctx, accountID, "archive"); ok {
			return f.Path
		}
		return "Archive"
	}
	if low == "junk" || low == "spam" {
		if f, ok := e.store.FindFolderByType(ctx, accountID, "junk"); ok {
			return f.Path
		}
		return "Junk"
	}
	if f, ok := e.store.FindFolderByIDOrPath(ctx, accountID, t); ok {
		return f.Path
	}
	return t
}

// SaveAttachmentToDownloads extracts an attachment from a message (fetching raw EML over IMAP
// if not yet cached) and saves it to ~/Downloads without overwriting existing files.
func (e *Engine) SaveAttachmentToDownloads(ctx context.Context, msgID, partName, filename string) (string, error) {
	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return "", fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	if len(m.RawEML) == 0 {
		acc, ok := e.findAccount(m.AccountID)
		if !ok {
			return "", fmt.Errorf("Konto nicht gefunden")
		}
		fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m)
		if ferr != nil {
			return "", ferr
		}
		m = fetched
	}
	name, _, data, err := ExtractAttachment(m.RawEML, partName, filename)
	if err != nil {
		return "", err
	}
	safeName := sanitizeDownloadFilename(name)
	var downloadsDir string
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		// On mobile platforms app cache storage is writable and covered by FileProvider.
		if cache, err := os.UserCacheDir(); err == nil && cache != "" {
			downloadsDir = filepath.Join(cache, "maildiver-attachments")
		} else {
			downloadsDir = filepath.Join(os.TempDir(), "maildiver-attachments")
		}
	} else {
		downloadsDir = os.TempDir()
		if home, err := os.UserHomeDir(); err == nil {
			downloadsDir = filepath.Join(home, "Downloads")
		}
	}
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		fallback := filepath.Join(os.TempDir(), "maildiver-attachments")
		if ferr := os.MkdirAll(fallback, 0o755); ferr != nil {
			return "", err
		}
		downloadsDir = fallback
	}
	target, f, err := createUniqueFile(downloadsDir, safeName)
	if err != nil {
		return "", fmt.Errorf("Anhang speichern: %w", err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(target)
		return "", fmt.Errorf("Anhang speichern: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("Anhang speichern: %w", cerr)
	}
	return target, nil
}

func sanitizeDownloadFilename(raw string) string {
	// Normalize both POSIX and Windows path separators before taking base name.
	s := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	s = filepath.Base(s)
	// Strip control characters, NUL, and Unicode BiDi override/isolate spoofing runes.
	var b strings.Builder
	for _, r := range s {
		if r == 0 || r == '/' || r == '\\' || r == ':' || unicode.IsControl(r) {
			continue
		}
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	clean = strings.TrimLeft(clean, ".")
	if clean == "" || clean == "." || clean == ".." {
		return "attachment.bin"
	}
	if len(clean) > 180 {
		ext := filepath.Ext(clean)
		if len(ext) > 20 {
			ext = ""
		}
		base := strings.TrimSuffix(clean, ext)
		maxBase := 180 - len(ext)
		if maxBase < 1 {
			maxBase = 1
		}
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		clean = base + ext
	}
	return clean
}

// createUniqueFile atomically creates a new non-existing file with O_CREATE|O_EXCL so it never
// overwrites existing files or follows pre-existing symlinks.
func createUniqueFile(dir, name string) (string, *os.File, error) {
	tryOpen := func(cand string) (*os.File, error) {
		return os.OpenFile(cand, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	target := filepath.Join(dir, name)
	if f, err := tryOpen(target); err == nil {
		return target, f, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", nil, err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if f, err := tryOpen(cand); err == nil {
			return cand, f, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", nil, err
		}
	}
	cand := filepath.Join(dir, fmt.Sprintf("%s-%s%s", base, randomHex(4), ext))
	f, err := tryOpen(cand)
	if err != nil {
		return "", nil, err
	}
	return cand, f, nil
}

// ImportMessageICS imports the text/calendar (.ics) invite embedded in a message into calendarID
// (defaults to LocalCalendarID).
func (e *Engine) ImportMessageICS(ctx context.Context, msgID, calendarID string) (int, error) {
	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return 0, fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	if strings.TrimSpace(m.ICSBody) == "" && !m.BodyFetched {
		if acc, ok := e.findAccount(m.AccountID); ok {
			if fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m); ferr == nil {
				m = fetched
			}
		}
	}
	if strings.TrimSpace(m.ICSBody) == "" {
		return 0, fmt.Errorf("Diese E-Mail enthält keine .ics-Termineinladung")
	}
	if calendarID == "" {
		calendarID = LocalCalendarID
	}
	return e.ImportICSBytes(ctx, calendarID, []byte(m.ICSBody))
}

// ICSRespondResult summarizes the outcome of an RSVP response to an .ics invitation.
type ICSRespondResult struct {
	Status         string
	ImportedCount  int
	ReplySent      bool
	OrganizerEmail string
	ReplyError     string
}

// RespondMessageICS responds to a message's .ics invitation ("accepted", "tentative", or "declined"):
//   - For accepted/tentative: imports the event into calendarID (defaults to LocalCalendarID).
//   - If an ORGANIZER is present and an outgoing SMTP account is available: sends an RFC 5546
//     METHOD:REPLY email via SMTP so the organizer's calendar updates automatically.
func (e *Engine) RespondMessageICS(ctx context.Context, msgID, calendarID, status string) (ICSRespondResult, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accept", "accepted":
		status = "accepted"
	case "maybe", "tentative":
		status = "tentative"
	case "decline", "declined":
		status = "declined"
	default:
		return ICSRespondResult{}, fmt.Errorf("invalid_argument: unbekannter RSVP-Status %q", status)
	}

	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return ICSRespondResult{}, fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	if strings.TrimSpace(m.ICSBody) == "" && !m.BodyFetched {
		if acc, ok := e.findAccount(m.AccountID); ok {
			if fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m); ferr == nil {
				m = fetched
			}
		}
	}
	if strings.TrimSpace(m.ICSBody) == "" {
		return ICSRespondResult{}, fmt.Errorf("Diese E-Mail enthält keine .ics-Termineinladung")
	}
	if calendarID == "" {
		calendarID = LocalCalendarID
	}

	res := ICSRespondResult{Status: status}
	if status == "accepted" || status == "tentative" {
		n, err := e.ImportICSBytes(ctx, calendarID, []byte(m.ICSBody))
		if err != nil {
			return res, err
		}
		res.ImportedCount = n
	}

	acc, ok := e.findAccount(m.AccountID)
	if !ok {
		acc, _ = e.pickOutgoingAccount("")
	}
	attendeeEmail := strings.TrimSpace(acc.Email)
	attendeeName := strings.TrimSpace(acc.DisplayName)
	if attendeeName == "" {
		attendeeName = strings.TrimSpace(acc.Name)
	}
	if attendeeEmail == "" {
		return res, nil
	}

	replyICS, orgEmail, summary, err := BuildICSRSVP(m.ICSBody, attendeeEmail, attendeeName, status)
	if err != nil {
		res.ReplyError = err.Error()
		return res, nil
	}
	if orgEmail == "" {
		// Fall back to the email sender if ORGANIZER was omitted in the .ics.
		if addrs := ExtractAddresses([]string{m.From}); len(addrs) > 0 {
			orgEmail = addrs[0]
		}
	}
	res.OrganizerEmail = orgEmail
	if orgEmail == "" || strings.EqualFold(orgEmail, attendeeEmail) {
		return res, nil
	}

	if summary == "" {
		summary = m.Subject
	}
	var prefix, line string
	switch status {
	case "accepted":
		prefix = "Zugesagt: "
		line = fmt.Sprintf("Ich habe die Einladung „%s“ angenommen.", summary)
	case "tentative":
		prefix = "Vorläufig: "
		line = fmt.Sprintf("Ich habe die Einladung „%s“ unter Vorbehalt angenommen.", summary)
	default:
		prefix = "Abgesagt: "
		line = fmt.Sprintf("Ich habe die Einladung „%s“ abgelehnt.", summary)
	}

	fromHdr := (&mail.Address{Name: attendeeName, Address: attendeeEmail}).String()
	_, raw := BuildCalendarReplyMIME(fromHdr, orgEmail, prefix+summary, line, replyICS, m.MessageID)
	pass, perr := e.accountPassword(acc.ID)
	if perr != nil || strings.TrimSpace(pass) == "" {
		res.ReplyError = "Kein SMTP-Passwort hinterlegt (Termin lokal verarbeitet)"
		return res, nil
	}
	if err := SendSMTP(ctx, acc, pass, attendeeEmail, []string{orgEmail}, raw); err != nil {
		if isTransientNetworkError(err) {
			obID := "ob-rsvp-" + randomHex(6)
			if qerr := e.store.EnqueueOutbox(ctx, OutboxItem{
				ID:         obID,
				AccountID:  acc.ID,
				FromEmail:  attendeeEmail,
				Recipients: []string{orgEmail},
				Subject:    prefix + summary,
				Status:     "queued",
				RawEML:     raw,
				LastError:  err.Error(),
			}); qerr == nil {
				e.emitOutboxChanged()
				res.ReplyError = "Offline – Antwort in Postausgang eingereiht"
				return res, nil
			}
		}
		res.ReplyError = err.Error()
		return res, nil
	}
	res.ReplySent = true
	return res, nil
}

// CreateFolder creates a new IMAP folder on the server and refreshes the account's folder list.
func (e *Engine) CreateFolder(ctx context.Context, accountID, name string) error {
	name = strings.TrimSpace(strings.Trim(name, "/"))
	if name == "" {
		return fmt.Errorf("Ordnername erforderlich")
	}
	acc, ok := e.findAccount(accountID)
	if !ok {
		var err error
		acc, err = e.pickOutgoingAccount(accountID)
		if err != nil {
			return err
		}
	}
	if err := e.createFolderIMAP(ctx, acc, name); err != nil {
		return err
	}
	_, err := e.SyncAccount(ctx, acc.ID, false)
	return err
}

func isProtectedSpecialFolder(f StoredFolder) bool {
	switch strings.ToLower(strings.TrimSpace(f.Type)) {
	case "inbox", "sent", "drafts", "trash", "junk", "archive":
		return true
	}
	for _, su := range f.SpecialUse {
		switch strings.ToLower(strings.TrimSpace(su)) {
		case "inbox", "sent", "drafts", "trash", "junk", "archive", "all":
			return true
		}
	}
	return strings.EqualFold(strings.TrimSpace(f.Path), "INBOX")
}

// RenameFolder renames an IMAP folder (blocking system Special-Use folders) and syncs the folder tree.
func (e *Engine) RenameFolder(ctx context.Context, accountID, folderID, newName string) error {
	newName = strings.TrimSpace(strings.Trim(newName, "/."))
	if newName == "" {
		return fmt.Errorf("Neuer Ordnername erforderlich")
	}
	acc, ok := e.findAccount(accountID)
	if !ok {
		var err error
		acc, err = e.pickOutgoingAccount(accountID)
		if err != nil {
			return err
		}
	}
	f, ok := e.store.FindFolderByIDOrPath(ctx, acc.ID, folderID)
	if !ok {
		return fmt.Errorf("not_found: Ordner %q nicht gefunden", folderID)
	}
	if isProtectedSpecialFolder(f) {
		return fmt.Errorf("Systemordner %q kann nicht umbenannt werden", f.Name)
	}
	delim := f.Delimiter
	if delim == "" {
		delim = "/"
	}
	// Preserve parent prefix when caller provides only the leaf name without delimiter.
	targetPath := newName
	if !strings.Contains(newName, delim) && !strings.Contains(newName, "/") {
		if idx := strings.LastIndex(f.Path, delim); idx >= 0 {
			targetPath = f.Path[:idx+len(delim)] + newName
		}
	} else if delim != "/" {
		targetPath = strings.ReplaceAll(newName, "/", delim)
	}
	if err := e.renameFolderIMAP(ctx, acc, f.Path, targetPath); err != nil {
		return err
	}
	_, err := e.SyncAccount(ctx, acc.ID, false)
	return err
}

// DeleteFolder deletes a user IMAP folder (blocking system Special-Use folders) and updates the local cache.
func (e *Engine) DeleteFolder(ctx context.Context, accountID, folderID string) error {
	acc, ok := e.findAccount(accountID)
	if !ok {
		var err error
		acc, err = e.pickOutgoingAccount(accountID)
		if err != nil {
			return err
		}
	}
	f, ok := e.store.FindFolderByIDOrPath(ctx, acc.ID, folderID)
	if !ok {
		return fmt.Errorf("not_found: Ordner %q nicht gefunden", folderID)
	}
	if isProtectedSpecialFolder(f) {
		return fmt.Errorf("Systemordner %q kann nicht gelöscht werden", f.Name)
	}
	if err := e.deleteFolderIMAP(ctx, acc, f.Path); err != nil {
		return err
	}
	_ = e.store.ClearFolderMessages(ctx, f.ID)
	_, err := e.SyncAccount(ctx, acc.ID, false)
	return err
}

// EmptyTrash permanently deletes all messages in the account's Trash folder on IMAP and locally.
func (e *Engine) EmptyTrash(ctx context.Context, accountID string) (int, error) {
	acc, ok := e.findAccount(accountID)
	if !ok {
		var err error
		acc, err = e.pickOutgoingAccount(accountID)
		if err != nil {
			return 0, err
		}
	}
	trash, ok := e.store.FindFolderByType(ctx, acc.ID, "trash")
	if !ok {
		return 0, fmt.Errorf("Kein Papierkorb-Ordner für %s gefunden", acc.Name)
	}
	n, err := e.emptyFolderIMAP(ctx, acc, trash.Path)
	if err != nil {
		return 0, err
	}
	_ = e.store.ClearFolderMessages(ctx, trash.ID)
	e.emitMailSync(acc.ID)
	return n, nil
}

// migrateDBIfNeeded safely migrates a pre-existing SQLite database (including any
// uncheckpointed WAL pages) from legacyPath (formerly ~/Library/Caches/maildiver on macOS)
// into targetPath (Application Support / DataDir) when targetPath does not yet exist.
// Uses SQLite VACUUM INTO a temp file followed by atomic rename so WAL data is never lost
// or split across filesystems.
func migrateDBIfNeeded(legacyPath, targetPath string) {
	if _, err := os.Stat(targetPath); err == nil {
		return // target already exists; never overwrite
	}
	info, err := os.Stat(legacyPath)
	if err != nil || info.IsDir() {
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return
	}

	tmp := targetPath + ".migrate.tmp"
	_ = os.Remove(tmp)

	dsn := legacyPath + "?_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return
	}
	// Checkpoint WAL and write a clean self-contained snapshot into tmp.
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)
	_, vacErr := db.Exec(`VACUUM INTO ?`, tmp)
	_ = db.Close()
	if vacErr != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, targetPath); err != nil {
		_ = os.Remove(tmp)
		return
	}
	// Only remove legacy files after the new self-contained database is atomically in place.
	_ = os.Remove(legacyPath)
	_ = os.Remove(legacyPath + "-wal")
	_ = os.Remove(legacyPath + "-shm")
}

// ReadInlineImage returns the content type and bytes of a Content-ID inline image from msgID.
func (e *Engine) ReadInlineImage(ctx context.Context, msgID, cid string) (string, []byte, error) {
	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return "", nil, fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	if len(m.RawEML) == 0 {
		acc, ok := e.findAccount(m.AccountID)
		if !ok {
			return "", nil, fmt.Errorf("Konto nicht gefunden")
		}
		fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m)
		if ferr != nil {
			return "", nil, ferr
		}
		m = fetched
	}
	return ExtractInlineImage(m.RawEML, cid)
}

// ReadAllInlineImages returns all embedded Content-ID inline images of msgID.
func (e *Engine) ReadAllInlineImages(ctx context.Context, msgID string) (map[string]InlineImage, error) {
	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return nil, fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	if len(m.RawEML) == 0 {
		acc, ok := e.findAccount(m.AccountID)
		if !ok {
			return nil, fmt.Errorf("Konto nicht gefunden")
		}
		fetched, ferr := e.fetchMessageBodyIMAP(ctx, acc, m)
		if ferr != nil {
			return nil, ferr
		}
		m = fetched
	}
	return ExtractAllInlineImages(m.RawEML), nil
}

// MarkSpam moves a message into the account's Junk/Spam folder on IMAP and removes it from the current folder.
func (e *Engine) MarkSpam(ctx context.Context, msgID string) error {
	m, err := e.store.GetMessage(ctx, msgID)
	if err != nil {
		return fmt.Errorf("not_found: Nachricht %q", msgID)
	}
	acc, ok := e.findAccount(m.AccountID)
	if !ok {
		return fmt.Errorf("not_found: Konto %q", m.AccountID)
	}
	target := e.resolveTargetFolderPath(ctx, acc.ID, "junk")
	if strings.EqualFold(target, m.FolderPath) {
		return nil
	}
	if err := e.applyMessageActionIMAP(ctx, acc, m, nil, nil, target, false); err != nil {
		return err
	}
	_ = e.store.DeleteMessage(ctx, m.ID)
	e.emitMailSync(acc.ID)
	return nil
}
