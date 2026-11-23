package nativeui

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/mailengine"
	"github.com/diver80/maildiver/internal/pgp"
	"github.com/diver80/maildiver/internal/smime"
	"github.com/diver80/maildiver/internal/triage"
)

// DirectController exposes the standalone Go mail/calendar/contacts engine to the native UI.
// Satisfied directly by *mailengine.Engine.
type DirectController interface {
	AccountsStatus(ctx context.Context) []mailengine.AccountSyncStatus
	TestAccount(ctx context.Context, acc config.DirectAccount, password string) mailengine.AccountTestResult
	SaveAccount(ctx context.Context, acc config.DirectAccount, password string) (config.DirectAccount, error)
	DeleteAccount(ctx context.Context, accountID string) error
	ReorderAccounts(ctx context.Context, orderedIDs []string) error
	SyncNow(ctx context.Context, accountID string) error
	ImportICSFile(ctx context.Context, calendarID, filePath string) (int, error)
	AddICSFeed(ctx context.Context, name, rawURL string) (int, error)
	DeleteICSFeed(ctx context.Context, feedID string) error
	GoogleCalStatus(ctx context.Context) mailengine.GoogleCalStatus
	StartGoogleCalAuth(ctx context.Context, clientID, clientSecret string) (string, error)
	DisconnectGoogleCal(ctx context.Context) error
	SyncCalendarsNow(ctx context.Context) error
	DeleteFilter(ctx context.Context, accountID, name string) error
	ToggleFilter(ctx context.Context, accountID, name string, enabled bool) error
	MoveFilter(ctx context.Context, accountID, name string, delta int) error
	SaveAttachmentToDownloads(ctx context.Context, msgID, partName, filename string) (string, error)
	SaveMessageEMLToDownloads(ctx context.Context, msgID string) (string, error)
	ReadAttachmentPreview(ctx context.Context, msgID, partName, filename string) (mailengine.AttachmentPreviewData, error)
	ReadInlineImage(ctx context.Context, msgID, cid string) (string, []byte, error)
	ReadAllInlineImages(ctx context.Context, msgID string) (map[string]mailengine.InlineImage, error)
	ImportMessageICS(ctx context.Context, msgID, calendarID string) (int, error)
	RespondMessageICS(ctx context.Context, msgID, calendarID, status string) (mailengine.ICSRespondResult, error)
	CreateFolder(ctx context.Context, accountID, name string) error
	RenameFolder(ctx context.Context, accountID, folderID, newName string) error
	DeleteFolder(ctx context.Context, accountID, folderID string) error
	EmptyTrash(ctx context.Context, accountID string) (int, error)
	EmptySpam(ctx context.Context, accountID string) (int, error)
	ListOutbox(ctx context.Context) ([]mailengine.OutboxItem, error)
	FlushOutboxNow(ctx context.Context) (int, int)
	DeleteOutbox(ctx context.Context, id string) error
	MarkSpam(ctx context.Context, msgID string) error
	ListContacts(ctx context.Context, query string) ([]backend.ContactRecord, error)
	SaveContact(ctx context.Context, c backend.ContactRecord) (backend.ContactRecord, error)
	DeleteContact(ctx context.Context, id string) error
	SuggestRecipients(ctx context.Context, query string, limit int) []mailengine.RecipientSuggestion
	ImportVCFFile(ctx context.Context, filePath string) (int, error)
	ExportVCFFile(ctx context.Context, filePath string) (int, error)
	SyncGoogleContacts(ctx context.Context) (int, error)
	ApplyThunderbirdImport(ctx context.Context, profilePath string, opts mailengine.TBImportOptions) (mailengine.TBImportReport, error)
	ImportSMIMEP12File(path, password string) (smime.CertInfo, error)
	ImportSMIMECertFile(path string) (smime.CertInfo, error)
	ListSMIMEIdentities() []smime.CertInfo
	ListSMIMEContacts() []smime.CertInfo
	DeleteSMIMEIdentity(email string) error
	DeleteSMIMEContact(email string) error
	ImportPGPKeyFile(path, passphrase string) ([]pgp.KeyInfo, error)
	ImportPGPArmor(armored, passphrase string) ([]pgp.KeyInfo, error)
	GeneratePGPKey(name, email, passphrase string) (pgp.KeyInfo, error)
	ExportPGPPublicKey(email string) (string, error)
	FetchPGPKeyOnline(ctx context.Context, email string) ([]pgp.KeyInfo, error)
	ListPGPIdentities() []pgp.KeyInfo
	ListPGPContacts() []pgp.KeyInfo
	DeletePGPIdentity(email string) error
	DeletePGPContact(email string) error
	AddCalDAVAccount(ctx context.Context, name, rawURL, username, password string) (int, error)
	AddCardDAVAccount(ctx context.Context, name, rawURL, username, password string) (int, error)
	DeleteCalDAVAccount(ctx context.Context, id string) error
	DeleteCardDAVAccount(ctx context.Context, id string) error
	StartMailOAuth2(ctx context.Context, acc config.DirectAccount, clientSecret string) (string, error)
}

var _ DirectController = (*mailengine.Engine)(nil)

type directUI struct {
	open bool
	tab  int // 0 = Mail (IMAP/SMTP), 1 = Calendar (Google & iCal), 2 = Filter Rules, 3 = Thunderbird Import

	// Accounts list & form
	accounts          []mailengine.AccountSyncStatus
	editingID         string
	presetLabel       string
	accName           string
	accSender         string // the name in From:
	accEmail          string
	accAliases        string // comma/newline-separated sender aliases
	accUser           string
	accPassword       string
	accSignature      string
	accSignatureHTML  string
	accSigPos         int // 0 = above reply quote, 1 = below quote
	imapHost          string
	imapPort          string
	imapTLS           string
	smtpHost          string
	smtpPort          string
	smtpTLS           string
	authType          string // "password" | "xoauth2"
	oauthProvider     string // "google" | "microsoft"
	oauthClientID     string
	oauthClientSecret string
	oauthTenant       string
	accBusy           bool
	accTesting        bool
	accStatusMsg      string
	accStatusErr      bool

	// Google Calendar & iCal
	gcalStatus   mailengine.GoogleCalStatus
	gcalClientID string
	gcalSecret   string
	gcalBusy     bool
	gcalMsg      string
	gcalErr      bool
	icsFilePath  string
	icsFeedName  string
	icsFeedURL   string
	icsFeeds     []config.ICalFeedConfig
	icsBusy      bool
	icsMsg       string
	icsErr       bool

	// CalDAV / CardDAV (Phase 2B)
	davKind     int // 0 = CalDAV, 1 = CardDAV
	davName     string
	davURL      string
	davUser     string
	davPass     string
	davBusy     bool
	davMsg      string
	davErr      bool
	caldavList  []config.DAVAccountConfig
	carddavList []config.DAVAccountConfig

	// Local filter rules & inline rule editor
	rules                []triage.NativeFilter
	rulesBusy            bool
	rulesErr             string
	ruleEditOpen         bool
	ruleEditOrigName     string
	ruleEditOrigAccID    string
	ruleEditAccountLbl   string
	ruleEditAccountMap   map[string]string
	ruleEditName         string
	ruleEditEnabled      bool
	ruleEditMatchMode    int // 0 = Match All (AND), 1 = Match Any (OR)
	ruleEditConds        []triage.NativeCondition
	ruleEditMarkRead     bool
	ruleEditAddStar      bool
	ruleEditMove         bool
	ruleEditMoveFolder   string
	ruleEditCopy         bool
	ruleEditCopyFolder   string
	ruleEditJunk         bool
	ruleEditDelete       bool
	ruleEditStop         bool
	ruleEditPreviewDone  bool
	ruleEditPreviewCount int
	ruleEditPreviewTotal int
	ruleEditPreviewHits  []string
	ruleEditPreviewNote  string

	// Read-only Thunderbird profile importer
	tbInitDone      bool
	tbProfiles      []mailengine.TBProfileInfo
	tbSelectedLabel string
	tbProfilePath   string
	tbPreview       *mailengine.TBImportPreview
	tbInspectErr    string
	tbOptAccounts   bool
	tbOptICal       bool
	tbOptLocalEv    bool
	tbOptContacts   bool
	tbOptFilters    bool
	tbBusy          bool
	tbResult        *mailengine.TBImportReport
	tbResultErr     string

	// S/MIME certificates & OpenPGP keys (Phase 2)
	secSubTab       int // 0 = S/MIME, 1 = OpenPGP
	smimeLoaded     bool
	smimeBusy       bool
	smimeIdentities []smime.CertInfo
	smimeContacts   []smime.CertInfo
	smimeP12Path    string
	smimeP12Pass    string
	smimeCertPath   string
	smimeMsg        string
	smimeErr        bool

	pgpLoaded     bool
	pgpIdentities []pgp.KeyInfo
	pgpContacts   []pgp.KeyInfo
	pgpImpPath    string
	pgpImpPass    string
	pgpPasteArmor string
	pgpGenName    string
	pgpGenEmail   string
	pgpGenPass    string
	pgpFetchEmail string
	pgpBusy       bool
	pgpMsg        string
	pgpErr        bool

	// Sidebar folder creation & destructive action confirmations
	newFolderAccountID string
	newFolderName      string
	newFolderBusy      bool
	renameFolderID     string
	renameFolderAccID  string
	renameFolderName   string
	delFolderArmID     string
	delFolderArmAt     time.Time
	emptyTrashArmID    string
	emptyTrashArmAt    time.Time
	emptySpamArmID     string
	emptySpamArmAt     time.Time
	delAccArmID        string
	delAccArmAt        time.Time
}

func (a *App) isDirectMode() bool {
	return a.opts.Direct != nil
}

// OpenDirectModal opens the standalone accounts/calendars/rules/import manager on the given tab
// (0 = IMAP/SMTP, 1 = Google & iCal, 2 = Filter Rules, 3 = Thunderbird Import). UI thread only.
func (a *App) OpenDirectModal(tab int) {
	if !a.isDirectMode() {
		return
	}
	d := &a.direct
	d.open = true
	if tab >= 0 && tab <= 3 {
		d.tab = tab
	}
	if !d.tbInitDone {
		d.tbInitDone = true
		d.tbOptAccounts = true
		d.tbOptICal = true
		d.tbOptLocalEv = true
		d.tbOptContacts = true
		d.tbOptFilters = true
	}
	if d.presetLabel == "" {
		a.applyDirectPreset(config.MailProviderPresets()[0])
	}
	if d.tab == 3 {
		a.discoverThunderbirdProfiles()
	}
	a.reloadDirectState(context.Background())
}

func (a *App) applyDirectPreset(p config.MailProviderPreset) {
	d := &a.direct
	d.presetLabel = p.Label
	if p.IMAPHost != "" {
		d.imapHost = p.IMAPHost
	}
	if p.IMAPPort > 0 {
		d.imapPort = strconv.Itoa(p.IMAPPort)
	}
	if p.IMAPTLS != "" {
		d.imapTLS = p.IMAPTLS
	}
	if p.SMTPHost != "" {
		d.smtpHost = p.SMTPHost
	}
	if p.SMTPPort > 0 {
		d.smtpPort = strconv.Itoa(p.SMTPPort)
	}
	if p.SMTPTLS != "" {
		d.smtpTLS = p.SMTPTLS
	}
	if p.AuthType != "" {
		d.authType = p.AuthType
	} else {
		d.authType = "password"
	}
	d.oauthProvider = p.OAuthProvider
	if d.oauthProvider == "microsoft" && strings.TrimSpace(d.oauthTenant) == "" {
		d.oauthTenant = "common"
	}
}

func (a *App) resetDirectAccountForm() {
	d := &a.direct
	d.editingID = ""
	d.accName = ""
	d.accSender = ""
	d.accEmail = ""
	d.accAliases = ""
	d.accUser = ""
	d.accPassword = ""
	d.accSignature = ""
	d.accSignatureHTML = ""
	d.accSigPos = 0
	d.authType = "password"
	d.oauthProvider = ""
	d.oauthClientID = ""
	d.oauthClientSecret = ""
	d.oauthTenant = "common"
	d.accStatusMsg = ""
	d.accStatusErr = false
	a.applyDirectPreset(config.MailProviderPresets()[0])
}

func (a *App) editDirectAccount(accountID string) {
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		cfg = a.cfg
	}
	for _, acc := range cfg.Direct.Accounts {
		if acc.ID == accountID {
			d := &a.direct
			d.editingID = acc.ID
			d.accName = acc.Name
			d.accSender = acc.DisplayName
			d.accEmail = acc.Email
			d.accAliases = formatAliasesInput(acc.Aliases)
			d.accUser = acc.Username
			d.accPassword = ""
			d.accSignature = acc.Signature
			d.accSignatureHTML = acc.SignatureHTML
			d.accSigPos = 0
			if strings.EqualFold(acc.SignaturePosition, "below") {
				d.accSigPos = 1
			}
			d.imapHost = acc.IMAPHost
			d.imapPort = strconv.Itoa(acc.IMAPPort)
			d.imapTLS = acc.IMAPTLS
			d.smtpHost = acc.SMTPHost
			d.smtpPort = strconv.Itoa(acc.SMTPPort)
			d.smtpTLS = acc.SMTPTLS
			d.authType = acc.AuthType
			if d.authType == "" {
				d.authType = "password"
			}
			d.oauthProvider = acc.OAuthProvider
			d.oauthClientID = acc.OAuthClientID
			d.oauthClientSecret = ""
			d.oauthTenant = acc.OAuthTenant
			if d.oauthTenant == "" {
				d.oauthTenant = "common"
			}
			d.presetLabel = config.MailProviderPresets()[len(config.MailProviderPresets())-1].Label
			for _, p := range config.MailProviderPresets() {
				if p.IMAPHost != "" && strings.EqualFold(p.IMAPHost, acc.IMAPHost) &&
					(p.AuthType == acc.AuthType || (p.AuthType == "" && (acc.AuthType == "" || acc.AuthType == "password"))) {
					d.presetLabel = p.Label
					break
				}
			}
			d.accStatusMsg = ""
			d.accStatusErr = false
			return
		}
	}
}

func (a *App) reloadDirectState(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	bc, _ := a.client()
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		accs := dc.AccountsStatus(c)
		gcal := dc.GoogleCalStatus(c)
		cfg, cfgErr := a.opts.ConfigLoader()
		var rules []triage.NativeFilter
		if bc != nil {
			if fl, err := bc.FiltersList(c, ""); err == nil {
				rules = fl.Filters
			}
		}
		a.emitUpdate(func() {
			a.direct.accounts = accs
			a.direct.gcalStatus = gcal
			if a.direct.gcalClientID == "" && gcal.ClientID != "" {
				a.direct.gcalClientID = gcal.ClientID
			}
			a.direct.rules = rules
			if cfgErr == nil {
				a.direct.icsFeeds = append([]config.ICalFeedConfig(nil), cfg.Direct.ICalFeeds...)
				a.direct.caldavList = append([]config.DAVAccountConfig(nil), cfg.Direct.CalDAVAccounts...)
				a.direct.carddavList = append([]config.DAVAccountConfig(nil), cfg.Direct.CardDAVAccounts...)
				a.applyConfig(cfg)
			}
		})
	}()
}

func (a *App) buildDirectAccountFromForm() (config.DirectAccount, string, error) {
	d := &a.direct
	email := strings.TrimSpace(d.accEmail)
	imapHost := strings.TrimSpace(d.imapHost)
	if email == "" || imapHost == "" {
		return config.DirectAccount{}, "", fmt.Errorf("%s", a.tr("E-Mail-Adresse und IMAP-Server sind erforderlich.", "Email address and IMAP server are required."))
	}
	imapPort, err := strconv.Atoi(strings.TrimSpace(d.imapPort))
	if err != nil || imapPort <= 0 || imapPort > 65535 {
		imapPort = 993
	}
	smtpPort, err := strconv.Atoi(strings.TrimSpace(d.smtpPort))
	if err != nil || smtpPort <= 0 || smtpPort > 65535 {
		smtpPort = 465
	}
	name := strings.TrimSpace(d.accName)
	if name == "" {
		name = email
	}
	authType := strings.ToLower(strings.TrimSpace(d.authType))
	if authType != "xoauth2" {
		authType = ""
	}
	sigPos := "above"
	if d.accSigPos == 1 {
		sigPos = "below"
	}
	acc := config.DirectAccount{
		ID:                d.editingID,
		Name:              name,
		DisplayName:       strings.TrimSpace(d.accSender),
		Email:             email,
		Aliases:           parseAliasesInput(d.accAliases),
		Username:          strings.TrimSpace(d.accUser),
		Signature:         strings.TrimRight(d.accSignature, " \t\r\n"),
		SignatureHTML:     strings.TrimSpace(d.accSignatureHTML),
		SignaturePosition: sigPos,
		IMAPHost:          imapHost,
		IMAPPort:          imapPort,
		IMAPTLS:           d.imapTLS,
		SMTPHost:          strings.TrimSpace(d.smtpHost),
		SMTPPort:          smtpPort,
		SMTPTLS:           d.smtpTLS,
		AuthType:          authType,
		OAuthProvider:     strings.TrimSpace(d.oauthProvider),
		OAuthClientID:     strings.TrimSpace(d.oauthClientID),
		OAuthTenant:       strings.TrimSpace(d.oauthTenant),
		Enabled:           true,
	}
	return acc, d.accPassword, nil
}

func (a *App) testDirectAccountForm(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.accTesting || a.direct.accBusy {
		return
	}
	acc, pw, err := a.buildDirectAccountFromForm()
	if err != nil {
		a.direct.accStatusMsg = err.Error()
		a.direct.accStatusErr = true
		return
	}
	a.direct.accTesting = true
	a.direct.accStatusMsg = a.tr("Prüfe IMAP & SMTP…", "Testing IMAP & SMTP…")
	a.direct.accStatusErr = false

	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		res := dc.TestAccount(c, acc, pw)
		a.emitUpdate(func() {
			a.direct.accTesting = false
			if res.IMAPOK && res.SMTPOK {
				a.direct.accStatusMsg = a.trf(
					"Verbindung erfolgreich! IMAP OK (%d Ordner), SMTP OK.",
					"Connection succeeded! IMAP OK (%d folders), SMTP OK.",
					res.IMAPFolders,
				)
				a.direct.accStatusErr = false
			} else {
				var parts []string
				if res.IMAPOK {
					parts = append(parts, fmt.Sprintf("IMAP OK (%d)", res.IMAPFolders))
				} else {
					parts = append(parts, "IMAP: "+res.IMAPError)
				}
				if res.SMTPOK {
					parts = append(parts, "SMTP OK")
				} else {
					parts = append(parts, "SMTP: "+res.SMTPError)
				}
				a.direct.accStatusMsg = strings.Join(parts, " · ")
				a.direct.accStatusErr = true
			}
		})
	}()
}

func (a *App) saveDirectAccountForm(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.accBusy {
		return
	}
	acc, pw, err := a.buildDirectAccountFromForm()
	if err != nil {
		a.direct.accStatusMsg = err.Error()
		a.direct.accStatusErr = true
		return
	}
	if acc.AuthType != "xoauth2" && acc.ID == "" && strings.TrimSpace(pw) == "" {
		a.direct.accStatusMsg = a.tr("Bitte Passwort eingeben (wird sicher im Schlüsselbund gespeichert).", "Please enter a password (stored securely in Keychain).")
		a.direct.accStatusErr = true
		return
	}
	a.direct.accBusy = true
	a.direct.accStatusMsg = a.tr("Speichere Konto & starte Synchronisierung…", "Saving account & starting sync…")
	a.direct.accStatusErr = false

	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		saved, err := dc.SaveAccount(c, acc, pw)
		if err != nil {
			a.emitUpdate(func() {
				a.direct.accBusy = false
				a.direct.accStatusMsg = err.Error()
				a.direct.accStatusErr = true
			})
			return
		}
		// Trigger an immediate initial sync so folders & inbox messages show up right away.
		sc, scancel := context.WithTimeout(ctx, 45*time.Second)
		syncErr := dc.SyncNow(sc, saved.ID)
		scancel()
		a.emitUpdate(func() {
			a.direct.accBusy = false
			a.resetDirectAccountForm()
			if errors.Is(syncErr, context.DeadlineExceeded) {
				a.toast(a.trf("Konto „%s“ verbunden", "Account “%s” connected", saved.Name), "success")
				a.direct.accStatusMsg = a.trf("Konto „%s“ gespeichert. Der erste Abgleich läuft im Hintergrund weiter.",
					"Account “%s” saved. The first sync continues in the background.", saved.Name)
				a.direct.accStatusErr = false
			} else if syncErr != nil {
				a.direct.accStatusMsg = a.tr("Konto gespeichert, erster Sync mit Hinweis: ", "Account saved, initial sync note: ") + syncErr.Error()
				a.direct.accStatusErr = true
			} else {
				a.toast(a.trf("Konto „%s“ verbunden & synchronisiert", "Account “%s” connected & synced", saved.Name), "success")
				a.direct.accStatusMsg = a.trf("Konto „%s“ gespeichert & synchronisiert.", "Account “%s” saved & synced.", saved.Name)
				a.direct.accStatusErr = false
			}
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) deleteDirectAccount(ctx context.Context, accountID string) {
	dc := a.opts.Direct
	if dc == nil || accountID == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err := dc.DeleteAccount(c, accountID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(err.Error(), "error")
				return
			}
			if a.direct.editingID == accountID {
				a.resetDirectAccountForm()
			}
			a.toast(a.tr("Konto entfernt", "Account removed"), "success")
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

type quickSyncController interface {
	SyncQuick(ctx context.Context, accountID, folderPath string) error
}

func (a *App) syncDirectAccountNow(ctx context.Context, accountID string) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	folderPath := ""
	targetAcc := accountID
	if strings.HasPrefix(a.navID, "folder:") {
		fid := strings.TrimPrefix(a.navID, "folder:")
		for _, f := range a.folders {
			if f.ID == fid {
				folderPath = f.Path
				if targetAcc == "" {
					targetAcc = f.AccountID
				}
				break
			}
		}
	}
	if !a.phone.refresh.Refreshing {
		a.toast(a.tr("Synchronisiere Postfach…", "Syncing mailbox…"), "info")
	}
	go func() {
		timeout := 45 * time.Second
		var err error
		if qs, ok := dc.(quickSyncController); ok {
			timeout = 20 * time.Second
			c, cancel := context.WithTimeout(ctx, timeout)
			err = qs.SyncQuick(c, targetAcc, folderPath)
			cancel()
		} else {
			c, cancel := context.WithTimeout(ctx, timeout)
			err = dc.SyncNow(c, accountID)
			cancel()
		}
		a.emitUpdate(func() {
			a.phone.refresh.Refreshing = false
			if errors.Is(err, context.DeadlineExceeded) {
				a.toast(a.tr("Synchronisierung läuft im Hintergrund weiter", "Sync continues in the background"), "info")
			} else if err != nil {
				a.toast(a.tr("Sync-Fehler: ", "Sync error: ")+err.Error(), "error")
			} else {
				a.toast(a.tr("Postfach synchronisiert", "Mailbox synced"), "success")
			}
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) startGoogleCalendarConnect(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.gcalBusy {
		return
	}
	cid := strings.TrimSpace(a.direct.gcalClientID)
	sec := strings.TrimSpace(a.direct.gcalSecret)
	if cid == "" {
		a.direct.gcalMsg = a.tr("Bitte Google OAuth Client-ID eintragen.", "Please enter a Google OAuth Client ID.")
		a.direct.gcalErr = true
		return
	}
	a.direct.gcalBusy = true
	a.direct.gcalMsg = a.tr("Browser wird geöffnet — bitte Anmeldung bei Google bestätigen…", "Opening browser — please approve Google sign-in…")
	a.direct.gcalErr = false

	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := dc.StartGoogleCalAuth(c, cid, sec)
		cancel()
		if err != nil {
			a.emitUpdate(func() {
				a.direct.gcalBusy = false
				a.direct.gcalMsg = err.Error()
				a.direct.gcalErr = true
			})
			return
		}
		a.emitUpdate(func() {
			a.direct.gcalBusy = false
			a.direct.gcalSecret = ""
			a.direct.gcalMsg = a.tr(
				"Browser geöffnet — bitte Anmeldung bei Google bestätigen. MailDiver verbindet sich danach automatisch.",
				"Browser opened — please complete Google sign-in. MailDiver will connect automatically.",
			)
			a.direct.gcalErr = false
		})
		// Poll gently until the browser OAuth callback completes (or fails / times out).
		deadline := time.Now().Add(5 * time.Minute)
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
				st := dc.GoogleCalStatus(sCtx)
				sCancel()
				if st.Error != "" {
					a.emitUpdate(func() {
						a.direct.gcalStatus = st
						a.direct.gcalMsg = st.Error
						a.direct.gcalErr = true
					})
					return
				}
				if st.Connected && (st.CalendarNum > 0 || st.AccountEmail != "") {
					a.emitUpdate(func() {
						a.direct.gcalStatus = st
						who := st.AccountEmail
						if who == "" {
							who = "Google"
						}
						a.direct.gcalMsg = a.trf("Verbunden mit %s & synchronisiert!", "Connected to %s & synced!", who)
						a.direct.gcalErr = false
						a.toast(a.direct.gcalMsg, "success")
						a.reloadDirectState(context.Background())
						a.RefreshAll(context.Background())
					})
					return
				}
			}
		}
	}()
}

func (a *App) disconnectGoogleCalendar(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.gcalBusy {
		return
	}
	a.direct.gcalBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err := dc.DisconnectGoogleCal(c)
		a.emitUpdate(func() {
			a.direct.gcalBusy = false
			if err != nil {
				a.direct.gcalMsg = err.Error()
				a.direct.gcalErr = true
				return
			}
			a.direct.gcalMsg = a.tr("Google Kalender getrennt.", "Google Calendar disconnected.")
			a.direct.gcalErr = false
			a.toast(a.direct.gcalMsg, "info")
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) syncDirectCalendarsNow(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	a.direct.icsBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err := dc.SyncCalendarsNow(c)
		a.emitUpdate(func() {
			a.direct.icsBusy = false
			if err != nil {
				a.toast(a.tr("Kalender-Sync-Fehler: ", "Calendar sync error: ")+err.Error(), "error")
			} else {
				a.toast(a.tr("Kalender synchronisiert", "Calendars synced"), "success")
			}
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) importICSFileFromForm(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.icsBusy {
		return
	}
	path := strings.TrimSpace(a.direct.icsFilePath)
	if path == "" {
		a.direct.icsMsg = a.tr("Bitte Pfad zu einer .ics-Datei angeben (z. B. ~/Downloads/termine.ics).", "Please enter path to an .ics file (e.g. ~/Downloads/events.ics).")
		a.direct.icsErr = true
		return
	}
	a.direct.icsBusy = true
	a.direct.icsMsg = ""
	a.direct.icsErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		n, err := dc.ImportICSFile(c, mailengine.LocalCalendarID, path)
		a.emitUpdate(func() {
			a.direct.icsBusy = false
			if err != nil {
				a.direct.icsMsg = err.Error()
				a.direct.icsErr = true
				return
			}
			a.direct.icsFilePath = ""
			a.direct.icsMsg = a.trf("%d Termin(e) in „Persönlich“ importiert.", "Imported %d event(s) into “Persönlich”.", n)
			a.direct.icsErr = false
			a.toast(a.direct.icsMsg, "success")
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) addICSFeedFromForm(ctx context.Context) {
	dc := a.opts.Direct
	if dc == nil || a.direct.icsBusy {
		return
	}
	name := strings.TrimSpace(a.direct.icsFeedName)
	rawURL := strings.TrimSpace(a.direct.icsFeedURL)
	if name == "" || rawURL == "" {
		a.direct.icsMsg = a.tr("Name und .ics-/webcal-URL sind erforderlich.", "Name and .ics/webcal URL are required.")
		a.direct.icsErr = true
		return
	}
	a.direct.icsBusy = true
	a.direct.icsMsg = ""
	a.direct.icsErr = false
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		n, err := dc.AddICSFeed(c, name, rawURL)
		a.emitUpdate(func() {
			a.direct.icsBusy = false
			if err != nil {
				a.direct.icsMsg = err.Error()
				a.direct.icsErr = true
				return
			}
			a.direct.icsFeedName = ""
			a.direct.icsFeedURL = ""
			a.direct.icsMsg = a.trf("Kalender-Abo „%s“ hinzugefügt (%d Termine).", "Subscribed to “%s” (%d events).", name, n)
			a.direct.icsErr = false
			a.toast(a.direct.icsMsg, "success")
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) deleteICSFeed(ctx context.Context, feedID string) {
	dc := a.opts.Direct
	if dc == nil || feedID == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := dc.DeleteICSFeed(c, feedID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(err.Error(), "error")
				return
			}
			a.toast(a.tr("Kalender-Abo entfernt", "Calendar feed removed"), "info")
			a.reloadDirectState(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) toggleDirectRule(ctx context.Context, accountID, name string, enabled bool) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := dc.ToggleFilter(c, accountID, name, enabled)
		a.emitUpdate(func() {
			if err != nil {
				a.direct.rulesErr = err.Error()
				return
			}
			a.reloadDirectState(context.Background())
		})
	}()
}

func (a *App) deleteDirectRule(ctx context.Context, accountID, name string) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := dc.DeleteFilter(c, accountID, name)
		a.emitUpdate(func() {
			if err != nil {
				a.direct.rulesErr = err.Error()
				return
			}
			a.toast(a.trf("Regel „%s“ gelöscht", "Rule “%s” deleted", name), "info")
			a.reloadDirectState(context.Background())
		})
	}()
}

func (a *App) moveDirectRule(ctx context.Context, accountID, name string, delta int) {
	dc := a.opts.Direct
	if dc == nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := dc.MoveFilter(c, accountID, name, delta)
		a.emitUpdate(func() {
			if err != nil {
				a.direct.rulesErr = err.Error()
				return
			}
			a.reloadDirectState(context.Background())
		})
	}()
}

func (a *App) runAllDirectRulesNow(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		a.direct.rulesErr = a.errText(err)
		return
	}
	seenAcc := map[string]bool{}
	var accIDs []string
	for _, r := range a.direct.rules {
		if r.Enabled && r.AccountID != "" && !seenAcc[r.AccountID] {
			seenAcc[r.AccountID] = true
			accIDs = append(accIDs, r.AccountID)
		}
	}
	if len(accIDs) == 0 {
		return
	}
	a.direct.rulesBusy = true
	a.direct.rulesErr = ""
	go func() {
		total := 0
		var firstErr error
		for _, accID := range accIDs {
			c, cancel := context.WithTimeout(ctx, 45*time.Second)
			res, rerr := bc.FilterRun(c, accID, nil)
			cancel()
			if rerr != nil {
				if firstErr == nil {
					firstErr = rerr
				}
				continue
			}
			total += res.Executed
		}
		a.emitUpdate(func() {
			a.direct.rulesBusy = false
			if firstErr != nil && total == 0 {
				a.direct.rulesErr = a.errText(firstErr)
				return
			}
			a.toast(a.trf("Alle Regeln ausgeführt: %d Mail(s) bearbeitet", "All rules executed: %d email(s) matched", total), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) presetAccountID() string {
	if a.selectedAccount != "" {
		return a.selectedAccount
	}
	if len(a.direct.accounts) > 0 {
		return a.direct.accounts[0].AccountID
	}
	if len(a.accounts) > 0 {
		return a.accounts[0].ID
	}
	return ""
}

func (a *App) resolvePresetTargetFolder(accountID, preferred string) string {
	for _, f := range a.folders {
		if (accountID == "" || f.AccountID == accountID) && strings.EqualFold(strings.TrimPrefix(f.Path, "/"), strings.TrimPrefix(preferred, "/")) {
			return strings.TrimPrefix(f.Path, "/")
		}
	}
	for _, f := range a.folders {
		if (accountID == "" || f.AccountID == accountID) && strings.EqualFold(f.Type, "archive") {
			return strings.TrimPrefix(f.Path, "/")
		}
	}
	return preferred
}

func (a *App) installFilterPreset(ctx context.Context, p triage.FilterRulePreset) {
	accID := a.presetAccountID()
	if accID == "" {
		a.direct.rulesErr = a.tr("Kein E-Mail-Konto konfiguriert.", "No email account configured.")
		return
	}
	bc, err := a.client()
	if err != nil {
		a.direct.rulesErr = a.errText(err)
		return
	}
	target := a.resolvePresetTargetFolder(accID, p.DefaultFolder)
	actions := make([]backend.FilterActionSpec, 0, len(p.Actions))
	for _, ac := range p.Actions {
		spec := backend.FilterActionSpec{
			Type:             ac.Type,
			TargetFolderPath: ac.TargetFolderPath,
		}
		if spec.Type == "moveToFolder" {
			spec.TargetFolderPath = target
		}
		actions = append(actions, spec)
	}
	a.direct.rulesBusy = true
	a.direct.rulesErr = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, uerr := bc.FilterUpsert(c, backend.FilterUpsertRequest{
			AccountID:  accID,
			Name:       p.Name,
			Enabled:    true,
			MatchAll:   p.MatchAll,
			Conditions: p.Conditions,
			Actions:    actions,
		})
		a.emitUpdate(func() {
			a.direct.rulesBusy = false
			if uerr != nil {
				a.direct.rulesErr = a.errText(uerr)
				return
			}
			a.toast(a.trf("Vorlage „%s“ gespeichert (→ %s)", "Preset “%s” saved (→ %s)", p.Name, target), "success")
			a.reloadDirectState(context.Background())
		})
	}()
}

func (a *App) runDirectRuleNow(ctx context.Context, accountID, name string) {
	bc, err := a.client()
	if err != nil {
		a.direct.rulesErr = a.errText(err)
		return
	}
	a.direct.rulesBusy = true
	a.direct.rulesErr = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err := bc.FilterRun(c, accountID, []string{name})
		a.emitUpdate(func() {
			a.direct.rulesBusy = false
			if err != nil {
				a.direct.rulesErr = a.errText(err)
				return
			}
			a.toast(a.trf("Regel „%s“ ausgeführt: %d Mail(s) bearbeitet", "Rule “%s” executed: %d email(s) matched", name, res.Executed), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// renderDirectOnboarding is shown when no IMAP/SMTP account is configured yet.
func (a *App) renderDirectOnboarding(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(28).Gap(14).Children(func() {
		ui.Column(c).MaxWidth(480).Padding(20).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconMail).Size(22, 22).TextColor(t.Accent)
				ui.Text(c, a.tr("Willkommen bei MailDiver", "Welcome to MailDiver")).FontSize(16).Bold()
			})
			ui.Text(c, a.tr(
				"Verbinde jetzt dein E-Mail-Postfach (z. B. IONOS, Gmail, iCloud) per IMAP/SMTP, deinen Google-/iCal-Kalender oder übernimm Konten, Signaturen, Kontakte & Regeln direkt aus einem vorhandenen Thunderbird-Profil.",
				"Connect your email mailbox (e.g. IONOS, Gmail, iCloud) via IMAP/SMTP, your Google/iCal calendar, or import accounts, signatures, contacts & rules directly from an existing Thunderbird profile.",
			)).FontSize(12.5).TextColor(t.TextMuted)
			if isMobileOS() {
				ui.Text(c, a.tr(
					"Am schnellsten: Auf dem Computer unter ☰ → Einstellungen → KI & Sync → Geräte-Übertragung einen QR-Code erzeugen und hier abfotografieren (gleiches WLAN).",
					"Quickest: on your computer, open ☰ → Settings → AI & Sync → Device Transfer to create a QR code and scan it here (same Wi-Fi).",
				)).FontSize(12.5).TextColor(t.TextMuted)
				if iconLabelBtn(c.Key("onb-qr"), iconCamera, a.tr("Vom Computer übernehmen (QR-Code)", "Transfer from computer (QR code)"), true, "").Disabled(a.settings.lanBusy).FillWidth().Clicked() {
					a.ScanPairingQR(context.Background())
				}
				if a.settings.lanBusy {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, a.tr("Übertrage…", "Transferring…")).FontSize(12.5).TextColor(t.TextMuted)
					})
				}
			}
			ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
				if iconLabelBtn(c.Key("onb-mail"), iconMail, a.tr("IMAP/SMTP-Konto verbinden", "Connect IMAP/SMTP Account"), !isMobileOS(), "").Clicked() {
					a.OpenDirectModal(0)
				}
				if !isMobileOS() && iconLabelBtn(c.Key("onb-tb"), iconDownload, a.tr("Aus Thunderbird importieren", "Import from Thunderbird"), false, a.tr("Konten, Signaturen, Kalender, Kontakte & Regeln nur lesend übernehmen", "Read-only import of accounts, signatures, calendars, contacts & rules")).Clicked() {
					a.OpenDirectModal(3)
				}
				if iconLabelBtn(c.Key("onb-cal"), iconCal, a.tr("Google Kalender / .ics", "Google Calendar / .ics"), false, "").Clicked() {
					a.OpenDirectModal(1)
				}
			})
		})
	})
}

// renderDirectModal renders the 4-tab modal for IMAP/SMTP accounts, Google/iCal calendars, local filter rules, and Thunderbird import.
func (a *App) renderDirectModal(c *ui.Context) {
	if !a.isDirectMode() {
		return
	}
	t := c.Theme()
	d := &a.direct
	ui.Modal(c, &d.open, func() {
		ui.Column(c).Width(a.modalW(760)).MaxHeight(780).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(12).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconMail).Size(18, 18).TextColor(t.Accent)
				ui.Text(c, a.tr("Konten, Kalender, Regeln & Import", "Accounts, Calendars, Rules & Import")).FontSize(16).Bold().Grow(1)
				if ui.Button(c.Key("dir-x"), "✕").Clicked() {
					d.open = false
				}
			})

			// d.tab is the view (3 the Thunderbird import); a phone has no Thunderbird to import from,
			// and without that tab the others fit its width.
			views := []int{0, 1, 2, 3, 4}
			labels := []string{a.tr("E-Mail", "Email"), a.tr("Kalender", "Calendar"), a.tr("Regeln", "Rules"), a.tr("TB-Import", "TB Import"), "S/MIME & PGP"}
			if isMobileOS() {
				views = []int{0, 1, 2, 4}
				labels = append(labels[:3:3], labels[4])
			}
			sel := 0
			for i, v := range views {
				if v == d.tab {
					sel = i
				}
			}
			if ui.Segmented(c.Key("dir-tabs"), &sel, labels...).Changed() {
				d.tab = views[sel]
				if d.tab == 3 && len(d.tbProfiles) == 0 && d.tbProfilePath == "" {
					a.discoverThunderbirdProfiles()
				}
				if d.tab == 4 {
					a.reloadSMIMECerts()
					a.reloadPGPKeys()
				}
			}

			ui.Scroll(c.Key("dir-scroll")).TrackScroll(&a.widgets.directScroll).Grow(1).MinHeight(0).MaxHeight(590).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Padding(0, 6, 0, 0).Children(func() {
					switch d.tab {
					case 0:
						a.renderDirectMailTab(c)
					case 1:
						a.renderDirectCalendarTab(c)
					case 2:
						a.renderDirectRulesTab(c)
					case 3:
						a.renderDirectTBImportTab(c)
					default:
						a.renderDirectSMIMETab(c)
					}
				})
			})

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconLock).Size(13, 13).TextColor(t.TextMuted)
				ui.Text(c, a.tr("Passwörter & OAuth-Tokens werden sicher im System-Schlüsselbund gespeichert.", "Passwords & OAuth tokens are stored securely in your system keychain.")).FontSize(11).TextColor(t.TextMuted).Grow(1)
				if ui.Button(c.Key("dir-close"), a.tr("Schließen", "Close")).Clicked() {
					d.open = false
				}
			})
		})
	})
}

func (a *App) renderDirectMailTab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

	// Existing configured accounts
	ui.Text(c, a.tr("Verbundene Konten", "Connected Accounts")).FontSize(13).Bold()
	if len(d.accounts) == 0 {
		ui.Text(c, a.tr("Noch kein E-Mail-Konto eingerichtet. Unten ein Konto hinzufügen:", "No email account configured yet. Add an account below:")).FontSize(12).TextColor(t.TextMuted)
	} else {
		for _, acc := range d.accounts {
			accCopy := acc
			stateClr := t.Success
			stateLbl := a.tr("Live (IDLE)", "Live (IDLE)")
			switch accCopy.State {
			case "syncing":
				stateClr = t.Accent
				stateLbl = a.tr("Synchronisiert…", "Syncing…")
			case "error":
				stateClr = t.Danger
				stateLbl = a.tr("Fehler", "Error")
			case "stopped":
				stateClr = t.TextMuted
				stateLbl = a.tr("Pausiert", "Paused")
			}
			card := ui.Column(c.Key("dir-acc-"+accCopy.AccountID)).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).
				Tooltip(a.tr("Ziehen zum Ändern der Konto-Reihenfolge", "Drag to reorder accounts")).
				Drag(draggedAccount(accCopy.AccountID))
			if card.Dragging() {
				card.Opacity(0.45)
			}
			if src, ok := ui.DragOver[draggedAccount](card); ok && string(src) != accCopy.AccountID {
				card.BorderWidth(2, 1, 1, 1).BorderColor(t.Accent).Background(withAlpha(t.Accent, 0.08))
			}
			if src, ok := ui.Drop[draggedAccount](card); ok && string(src) != accCopy.AccountID {
				a.ReorderAccounts(string(src), accCopy.AccountID)
			}
			card.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					if len(d.accounts) > 1 {
						ui.Text(c, "⋮⋮").FontSize(12).TextColor(withAlpha(t.TextMuted, 0.7)).Shrink(0)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, accCopy.Name).FontSize(13).Bold().SingleLine()
						ui.Text(c, fmt.Sprintf("%s · %d %s", accCopy.Email, accCopy.MessageCount, a.tr("Mails lokal", "emails cached"))).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
					})
					pillBadge(c, stateLbl, withAlpha(stateClr, 0.18), stateClr)
					if iconBtn(c.Key("dir-sync-"+accCopy.AccountID), iconRefresh, a.tr("Jetzt synchronisieren", "Sync now"), ui.Color{}, false).Clicked() {
						a.syncDirectAccountNow(context.Background(), accCopy.AccountID)
					}
					if ui.Button(c.Key("dir-edit-"+accCopy.AccountID), a.tr("Bearbeiten", "Edit")).Clicked() {
						a.editDirectAccount(accCopy.AccountID)
					}
					delArmed := d.delAccArmID == accCopy.AccountID && time.Since(d.delAccArmAt) < 5*time.Second
					if delArmed {
						btn := ui.Button(c.Key("dir-del-confirm-"+accCopy.AccountID), a.tr("Wirklich entfernen?", "Confirm remove?")).Padding(3, 8).
							Background(withAlpha(t.Danger, 0.18)).Border(1, t.Danger)
						if btn.Clicked() {
							d.delAccArmID = ""
							a.deleteDirectAccount(context.Background(), accCopy.AccountID)
						}
					} else if iconBtn(c.Key("dir-del-"+accCopy.AccountID), iconTrash, a.tr("Konto entfernen", "Remove account"), t.Danger, false).Clicked() {
						d.delAccArmID = accCopy.AccountID
						d.delAccArmAt = time.Now()
					}
				})
				if accCopy.LastError != "" {
					ui.Text(c, accCopy.LastError).FontSize(11).TextColor(t.Danger)
				}
			})
		}
	}

	ui.Divider(c)

	// Add / Edit account form
	formTitle := a.tr("Neues IMAP/SMTP-Konto hinzufügen", "Add New IMAP/SMTP Account")
	if d.editingID != "" {
		formTitle = a.tr("Konto bearbeiten", "Edit Account")
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, formTitle).FontSize(13).Bold().Grow(1)
		if d.editingID != "" {
			if ui.Button(c.Key("dir-new"), a.tr("Neu", "New")).Clicked() {
				a.resetDirectAccountForm()
			}
		}
	})

	formFieldRow := func(label string, body func()) {
		if a.isPhone() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				if label != "" {
					ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
				}
				body()
			})
			return
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).Width(135).TextColor(t.TextMuted)
			body()
		})
	}
	fieldGrow := func(el ui.Element) {
		if a.isPhone() {
			el.FillWidth()
		} else {
			el.Grow(1)
		}
	}

	presets := config.MailProviderPresets()
	presetLabels := make([]string, len(presets))
	for i, p := range presets {
		presetLabels[i] = p.Label
	}
	formFieldRow(a.tr("Anbieter-Vorlage", "Provider Preset"), func() {
		sel := ui.Select(c.Key("dir-preset"), &d.presetLabel, presetLabels)
		fieldGrow(sel)
		if sel.Changed() {
			for _, p := range presets {
				if p.Label == d.presetLabel {
					a.applyDirectPreset(p)
					break
				}
			}
		}
	})

	formFieldRow(a.tr("E-Mail-Adresse", "Email Address"), func() {
		fieldGrow(ui.TextInput(c.Key("dir-email"), &d.accEmail).Placeholder("name@domain.de"))
	})
	formFieldRow(a.tr("Dein Name", "Your Name"), func() {
		fieldGrow(ui.TextInput(c.Key("dir-sender"), &d.accSender).Placeholder(a.tr("steht als Absender in deinen Mails", "shown as the sender of your emails")))
	})
	formFieldRow(a.tr("Aliase", "Aliases"), func() {
		fieldGrow(ui.TextInput(c.Key("dir-aliases"), &d.accAliases).Placeholder(a.tr("optional, z. B. info@firma.de, Max <max@firma.de>", "optional, e.g. info@example.com, Max <max@example.com>")))
	})
	formFieldRow(a.tr("Kontoname", "Account Name"), func() {
		fieldGrow(ui.TextInput(c.Key("dir-name"), &d.accName).Placeholder(a.tr("optional, z. B. IONOS Privat", "optional, e.g. Work")))
	})
	formFieldRow(a.tr("Benutzername", "Username"), func() {
		fieldGrow(ui.TextInput(c.Key("dir-user"), &d.accUser).Placeholder(a.tr("leer = E-Mail-Adresse", "empty = email address")))
	})
	pwHint := a.tr("Passwort / App-Passwort", "Password / App password")
	if d.editingID != "" {
		pwHint = a.tr("leer lassen, um gespeichertes Passwort zu behalten", "leave empty to keep stored password")
	}
	authChoices := []string{"password", "xoauth2"}
	if d.authType == "" {
		d.authType = "password"
	}
	formFieldRow(a.tr("Anmeldung", "Auth Method"), func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Select(c.Key("dir-auth"), &d.authType, authChoices).Width(130)
			if d.authType == "xoauth2" {
				provChoices := []string{"google", "microsoft"}
				if d.oauthProvider == "" {
					d.oauthProvider = "google"
				}
				ui.Select(c.Key("dir-oauth-prov"), &d.oauthProvider, provChoices).Width(120)
			}
		})
	})
	if d.authType == "xoauth2" {
		formFieldRow("OAuth Client-ID", func() {
			fieldGrow(ui.TextInput(c.Key("dir-oauth-cid"), &d.oauthClientID).Placeholder("Client-ID (Google Cloud / Entra ID)"))
		})
		if d.oauthProvider == "microsoft" {
			formFieldRow("Tenant", func() {
				fieldGrow(ui.TextInput(c.Key("dir-oauth-ten"), &d.oauthTenant).Placeholder("common"))
			})
		} else {
			formFieldRow("Client-Secret", func() {
				fieldGrow(ui.TextInput(c.Key("dir-oauth-sec"), &d.oauthClientSecret).Password().Placeholder(a.tr("optional (Google Desktop Client)", "optional (Google Desktop Client)")))
			})
		}
		formFieldRow("", func() {
			if ui.Button(c.Key("dir-oauth-go"), a.tr("Per OAuth2 im Browser anmelden…", "Sign in via OAuth2 in Browser…")).Clicked() && !d.accBusy {
				acc, _, berr := a.buildDirectAccountFromForm()
				if berr != nil {
					d.accStatusMsg = berr.Error()
					d.accStatusErr = true
				} else if strings.TrimSpace(acc.OAuthClientID) == "" {
					d.accStatusMsg = a.tr("Bitte OAuth Client-ID angeben.", "Please enter OAuth Client ID.")
					d.accStatusErr = true
				} else {
					sec := strings.TrimSpace(d.oauthClientSecret)
					d.accBusy = true
					d.accStatusMsg = a.tr("Browser wird für OAuth2-Anmeldung geöffnet…", "Opening browser for OAuth2 sign-in…")
					d.accStatusErr = false
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						saved, serr := a.opts.Direct.SaveAccount(ctx, acc, "")
						if serr != nil {
							a.emitUpdate(func() {
								a.direct.accBusy = false
								a.direct.accStatusMsg = serr.Error()
								a.direct.accStatusErr = true
							})
							return
						}
						_, oerr := a.opts.Direct.StartMailOAuth2(ctx, saved, sec)
						a.emitUpdate(func() {
							a.direct.accBusy = false
							a.direct.editingID = saved.ID
							a.direct.oauthClientSecret = ""
							if oerr != nil {
								a.direct.accStatusMsg = oerr.Error()
								a.direct.accStatusErr = true
							} else {
								a.direct.accStatusMsg = a.tr("Browser geöffnet — Anmeldung dort abschließen, danach startet der Sync automatisch.", "Browser opened — complete sign-in there, sync will start automatically.")
								a.direct.accStatusErr = false
							}
							a.reloadDirectState(context.Background())
						})
					}()
				}
			}
		})
	} else {
		formFieldRow(a.tr("Passwort", "Password"), func() {
			fieldGrow(ui.TextInput(c.Key("dir-pw"), &d.accPassword).Password().Placeholder(pwHint))
		})
	}

	tlsChoices := []string{"tls", "starttls"}
	formFieldRow("IMAP-Server", func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("dir-imap-h"), &d.imapHost).Placeholder("imap.ionos.de").Grow(1).MinWidth(140)
			ui.TextInput(c.Key("dir-imap-p"), &d.imapPort).Placeholder("993").Width(68)
			ui.Select(c.Key("dir-imap-t"), &d.imapTLS, tlsChoices).Width(100)
		})
	})
	formFieldRow("SMTP-Server", func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("dir-smtp-h"), &d.smtpHost).Placeholder("smtp.ionos.de").Grow(1).MinWidth(140)
			ui.TextInput(c.Key("dir-smtp-p"), &d.smtpPort).Placeholder("465").Width(68)
			ui.Select(c.Key("dir-smtp-t"), &d.smtpTLS, tlsChoices).Width(100)
		})
	})
	if a.isPhone() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, a.tr("Signatur (Text)", "Signature (plain text)")).FontSize(12).TextColor(t.TextMuted)
			ui.TextArea(c.Key("dir-sig"), &d.accSignature).Placeholder(a.tr("optional, wird automatisch an neue Mails angehängt…", "optional, automatically appended to new emails…")).FillWidth().Height(64)
			ui.Text(c, a.tr("HTML-Signatur (optional)", "HTML signature (optional)")).FontSize(12).TextColor(t.TextMuted)
			ui.TextArea(c.Key("dir-sig-html"), &d.accSignatureHTML).Placeholder(a.tr("leer = automatisch aus Text-Signatur formatiert; sonst eigenes HTML…", "empty = auto-styled from plain signature; else custom HTML…")).FillWidth().Height(64)
			ui.Text(c, a.tr("Signatur-Position bei Antworten", "Signature position in replies")).FontSize(12).TextColor(t.TextMuted)
			ui.Segmented(c.Key("dir-sig-pos"), &d.accSigPos, a.tr("Über Zitat", "Above quote"), a.tr("Unter Zitat", "Below quote"))
		})
	} else {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, a.tr("Signatur (Text)", "Signature (plain)")).FontSize(12).Width(135).TextColor(t.TextMuted).Padding(6, 0, 0, 0)
			ui.TextArea(c.Key("dir-sig"), &d.accSignature).Placeholder(a.tr("optional, wird automatisch an neue Mails angehängt…", "optional, automatically appended to new emails…")).Grow(1).Height(64)
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(135).Gap(4).Padding(4, 0, 0, 0).Children(func() {
				ui.Text(c, a.tr("HTML-Signatur", "HTML signature")).FontSize(12).TextColor(t.TextMuted)
				if strings.TrimSpace(d.accSignature) != "" {
					if ui.Button(c.Key("dir-sig-gen"), a.tr("Aus Text erzeugen", "Generate from text")).Padding(2, 6).Clicked() {
						d.accSignatureHTML = composeSignatureBlockHTML(stripSignatureDelim(d.accSignature))
					}
				}
			})
			ui.TextArea(c.Key("dir-sig-html"), &d.accSignatureHTML).Placeholder(a.tr("optional (leer = wird automatisch aus Text-Signatur hübsch formatiert; oder eigenes HTML z. B. <p><b>Name</b></p>)", "optional (empty = auto-styled from plain signature; or custom HTML e.g. <p><b>Name</b></p>)")).Grow(1).Height(64)
		})
		formFieldRow(a.tr("Signatur-Position", "Signature position"), func() {
			ui.Segmented(c.Key("dir-sig-pos"), &d.accSigPos, a.tr("Über Zitat (Standard)", "Above quote (default)"), a.tr("Unter Zitat (ganz unten)", "Below quote (bottom)"))
		})
	}

	if d.accStatusMsg != "" {
		clr := t.Success
		if d.accStatusErr {
			clr = t.Danger
		}
		ui.Row(c).FillWidth().Padding(8, 10).Radius(8).Background(withAlpha(clr, 0.14)).Border(1, withAlpha(clr, 0.4)).Children(func() {
			ui.Text(c, d.accStatusMsg).FontSize(12).TextColor(clr)
		})
	}

	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		testLbl := a.tr("Verbindung testen", "Test Connection")
		if d.accTesting {
			testLbl = a.tr("Teste…", "Testing…")
		}
		testBtn := ui.Button(c.Key("dir-test"), testLbl)
		if d.accTesting || d.accBusy {
			testBtn.Disabled(true)
		}
		if testBtn.Clicked() && !d.accTesting && !d.accBusy {
			a.testDirectAccountForm(context.Background())
		}

		saveLbl := a.tr("Speichern & synchronisieren", "Save & Sync")
		if d.accBusy {
			saveLbl = a.tr("Speichere…", "Saving…")
		}
		saveBtn := ui.PrimaryButton(c.Key("dir-save"), saveLbl)
		if d.accBusy || d.accTesting {
			saveBtn.Disabled(true)
		}
		if saveBtn.Clicked() && !d.accBusy && !d.accTesting {
			a.saveDirectAccountForm(context.Background())
		}
	})
}

// googleCloudSetupLinks are the Google Cloud Console pages needed to create the
// Desktop-App OAuth client for Google Calendar (+ contacts) sync.
func googleCloudSetupLinks(a *App) []ExtractedLink {
	return []ExtractedLink{
		{URL: "https://console.cloud.google.com/", Label: a.tr("Google Cloud Console ↗", "Google Cloud Console ↗")},
		{URL: "https://console.cloud.google.com/apis/library/calendar-json.googleapis.com", Label: a.tr("1. Calendar API aktivieren ↗", "1. Enable Calendar API ↗")},
		{URL: "https://console.cloud.google.com/apis/library/people.googleapis.com", Label: a.tr("2. People API (Kontakte) ↗", "2. People API (contacts) ↗")},
		{URL: "https://console.cloud.google.com/auth/clients", Label: a.tr("3. OAuth-Client (Desktop-App) ↗", "3. OAuth client (Desktop app) ↗")},
	}
}

func (a *App) renderDirectCalendarTab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

	// Section 1: Google Calendar bidirectional OAuth2 PKCE
	ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("Google Kalender (Bidirektional)", "Google Calendar (Bidirectional)")).FontSize(13).Bold().Grow(1)
			if d.gcalStatus.Connected {
				pillBadge(c, a.tr("Verbunden", "Connected"), withAlpha(t.Success, 0.18), t.Success)
			} else {
				pillBadge(c, a.tr("Nicht verbunden", "Not Connected"), t.Surface, t.TextMuted)
			}
		})

		if d.gcalStatus.Connected {
			info := d.gcalStatus.AccountEmail
			if d.gcalStatus.CalendarNum > 0 {
				info += fmt.Sprintf(" · %d %s", d.gcalStatus.CalendarNum, a.tr("Kalender", "calendars"))
			}
			if d.gcalStatus.LastSync != "" {
				info += " · Sync " + formatDateTimeLang(d.gcalStatus.LastSync, a.uiLang())
			}
			ui.Text(c, info).FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, a.tr(
				"Erstelle in der Google Cloud Console unter „OAuth-Client-IDs“ eine Desktop-App (Calendar API aktivieren). Die Anmeldung erfolgt per Browser-Loopback (PKCE).",
				"Create a Desktop App OAuth Client ID in Google Cloud Console (enable Calendar API). Sign-in uses local browser loopback (PKCE).",
			)).FontSize(11.5).TextColor(t.TextMuted)
			ui.Row(c.Key("dir-gc-links")).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				for i, lk := range googleCloudSetupLinks(a) {
					if ui.Button(c.Key(fmt.Sprintf("dir-gc-link-%d", i)), lk.Label).Padding(3, 8).Clicked() {
						_ = openExternalURL(lk.URL)
					}
				}
			})
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "OAuth Client-ID").FontSize(12).Width(135).TextColor(t.TextMuted)
			ui.TextInput(c.Key("dir-gc-id"), &d.gcalClientID).Placeholder("123456-abc.apps.googleusercontent.com").Grow(1)
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Client-Secret").FontSize(12).Width(135).TextColor(t.TextMuted)
			ui.TextInput(c.Key("dir-gc-sec"), &d.gcalSecret).Password().Placeholder(a.tr("optional / bei Desktop-Clients empfohlen", "optional / recommended for Desktop clients")).Grow(1)
		})

		if d.gcalMsg != "" || d.gcalStatus.Error != "" {
			msg := d.gcalMsg
			isErr := d.gcalErr
			if msg == "" {
				msg = d.gcalStatus.Error
				isErr = true
			}
			clr := t.Success
			if isErr {
				clr = t.Danger
			}
			ui.Text(c, msg).FontSize(11.5).TextColor(clr)
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if d.gcalStatus.Connected {
				if ui.Button(c.Key("dir-gc-disc"), a.tr("Trennen", "Disconnect")).Clicked() && !d.gcalBusy {
					a.disconnectGoogleCalendar(context.Background())
				}
				if ui.Button(c.Key("dir-gc-sync"), a.tr("Jetzt synchronisieren", "Sync Now")).Clicked() && !d.icsBusy {
					a.syncDirectCalendarsNow(context.Background())
				}
			}
			connLbl := a.tr("Mit Google verbinden", "Connect Google Calendar")
			if d.gcalBusy {
				connLbl = a.tr("Warte auf Browser…", "Waiting for browser…")
			}
			btn := ui.PrimaryButton(c.Key("dir-gc-conn"), connLbl)
			if d.gcalBusy {
				btn.Disabled(true)
			}
			if btn.Clicked() && !d.gcalBusy {
				a.startGoogleCalendarConnect(context.Background())
			}
		})
	})

	// Section 2: iCal (.ics) File Import & URL Subscriptions
	ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("iCal (.ics) Import & Kalender-Abos", "iCal (.ics) Import & Subscriptions")).FontSize(13).Bold().Grow(1)
			if ui.Button(c.Key("dir-ics-sync"), a.tr("Alle aktualisieren", "Refresh All")).Clicked() && !d.icsBusy {
				a.syncDirectCalendarsNow(context.Background())
			}
		})

		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr(".ics-Datei", ".ics File")).FontSize(12).Width(135).TextColor(t.TextMuted)
			if ui.Button(c.Key("dir-ics-browse"), a.tr("Öffnen…", "Browse…")).Clicked() && !d.icsBusy {
				a.pickFile(FilePickRequest{
					Mode:       "open",
					Title:      a.tr("iCal-Datei (.ics) auswählen", "Select iCal (.ics) File"),
					FilterName: "iCalendar (.ics)",
					Extensions: []string{"ics"},
				}, func(paths []string) {
					if len(paths) > 0 {
						d.icsFilePath = paths[0]
						a.importICSFileFromForm(context.Background())
					}
				})
			}
			ui.TextInput(c.Key("dir-ics-file"), &d.icsFilePath).Placeholder("~/Downloads/termine.ics").Grow(1)
			impBtn := ui.Button(c.Key("dir-ics-imp"), a.tr("Importieren", "Import"))
			if d.icsBusy {
				impBtn.Disabled(true)
			}
			if impBtn.Clicked() && !d.icsBusy {
				a.importICSFileFromForm(context.Background())
			}
		})

		ui.Divider(c)
		ui.Text(c, a.tr("Kalender-Abo hinzufügen (https:// oder webcal://)", "Add Calendar Subscription (https:// or webcal://)")).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("dir-feed-name"), &d.icsFeedName).Placeholder(a.tr("Name (z. B. Feiertage)", "Name (e.g. Holidays)")).Width(170)
			ui.TextInput(c.Key("dir-feed-url"), &d.icsFeedURL).Placeholder("https://…/calendar.ics").Grow(1)
			addBtn := ui.PrimaryButton(c.Key("dir-feed-add"), a.tr("Abonnieren", "Subscribe"))
			if d.icsBusy {
				addBtn.Disabled(true)
			}
			if addBtn.Clicked() && !d.icsBusy {
				a.addICSFeedFromForm(context.Background())
			}
		})

		if d.icsMsg != "" {
			clr := t.Success
			if d.icsErr {
				clr = t.Danger
			}
			ui.Text(c, d.icsMsg).FontSize(11.5).TextColor(clr)
		}

		if len(d.icsFeeds) > 0 {
			for _, f := range d.icsFeeds {
				fCopy := f
				ui.Row(c.Key("dir-feed-"+fCopy.ID)).FillWidth().Padding(6, 8).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, fCopy.Name).FontSize(12.5).Bold().SingleLine()
						ui.Text(c, fCopy.URL).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if iconBtn(c.Key("dir-feed-rm-"+fCopy.ID), iconTrash, a.tr("Abo entfernen", "Remove feed"), t.Danger, false).Clicked() {
						a.deleteICSFeed(context.Background(), fCopy.ID)
					}
				})
			}
		}
	})

	// Section 3: CalDAV (Calendars) & CardDAV (Contacts) — Nextcloud, iCloud, Fastmail, Radicale, Baïkal
	ui.Column(c.Key("dir-dav-sec")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("CalDAV & CardDAV (Nextcloud, Fastmail, Radicale…)", "CalDAV & CardDAV (Nextcloud, Fastmail, Radicale…)")).FontSize(13).Bold().Grow(1)
			ui.Segmented(c.Key("dir-dav-kind"), &d.davKind, "CalDAV", "CardDAV")
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextInput(c.Key("dir-dav-name"), &d.davName).Placeholder(a.tr("Name (z. B. Nextcloud)", "Name (e.g. Nextcloud)")).Width(160)
			ui.TextInput(c.Key("dir-dav-url"), &d.davURL).Placeholder("https://cloud.example.org/remote.php/dav/…").Grow(1)
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("dir-dav-user"), &d.davUser).Placeholder(a.tr("Benutzername", "Username")).Grow(1)
			ui.TextInput(c.Key("dir-dav-pass"), &d.davPass).Password().Placeholder(a.tr("Passwort / App-Passwort", "Password / App password")).Grow(1)
			davBtn := ui.PrimaryButton(c.Key("dir-dav-add"), a.tr("Verbinden", "Connect"))
			if d.davBusy {
				davBtn.Disabled(true)
			}
			if davBtn.Clicked() && !d.davBusy && strings.TrimSpace(d.davURL) != "" {
				kind, name, rawURL, user, pass := d.davKind, strings.TrimSpace(d.davName), strings.TrimSpace(d.davURL), strings.TrimSpace(d.davUser), d.davPass
				d.davBusy = true
				d.davMsg = ""
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
					defer cancel()
					var n int
					var err error
					if kind == 0 {
						n, err = a.opts.Direct.AddCalDAVAccount(ctx, name, rawURL, user, pass)
					} else {
						n, err = a.opts.Direct.AddCardDAVAccount(ctx, name, rawURL, user, pass)
					}
					a.emitUpdate(func() {
						a.direct.davBusy = false
						if err != nil {
							a.direct.davMsg = err.Error()
							a.direct.davErr = true
							return
						}
						a.direct.davName, a.direct.davURL, a.direct.davUser, a.direct.davPass = "", "", "", ""
						a.direct.davErr = false
						if kind == 0 {
							a.direct.davMsg = a.trf("CalDAV verbunden (%d Termine geladen)", "CalDAV connected (%d events synced)", n)
						} else {
							a.direct.davMsg = a.trf("CardDAV verbunden (%d Kontakte importiert)", "CardDAV connected (%d contacts imported)", n)
						}
						a.RefreshAll(context.Background())
					})
				}()
			}
		})
		if d.davMsg != "" {
			clr := t.Success
			if d.davErr {
				clr = t.Danger
			}
			ui.Text(c, d.davMsg).FontSize(11.5).TextColor(clr)
		}
		for _, dv := range d.caldavList {
			dvCopy := dv
			ui.Row(c.Key("dir-caldav-"+dvCopy.ID)).FillWidth().Padding(6, 8).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
				pillBadge(c, "CalDAV", withAlpha(t.Accent, 0.16), t.Accent)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, dvCopy.Name).FontSize(12).Bold().SingleLine()
					ui.Text(c, dvCopy.URL).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
				})
				if iconBtn(c.Key("dir-caldav-rm-"+dvCopy.ID), iconTrash, a.tr("CalDAV entfernen", "Remove CalDAV"), t.Danger, false).Clicked() {
					go func(id string) {
						_ = a.opts.Direct.DeleteCalDAVAccount(context.Background(), id)
						a.emitUpdate(func() {
							a.reloadDirectState(context.Background())
							a.RefreshAll(context.Background())
						})
					}(dvCopy.ID)
				}
			})
		}
		for _, dv := range d.carddavList {
			dvCopy := dv
			ui.Row(c.Key("dir-carddav-"+dvCopy.ID)).FillWidth().Padding(6, 8).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(8).AlignItems(ui.Center).Children(func() {
				pillBadge(c, "CardDAV", withAlpha(t.Success, 0.16), t.Success)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, dvCopy.Name).FontSize(12).Bold().SingleLine()
					ui.Text(c, dvCopy.URL).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
				})
				if iconBtn(c.Key("dir-carddav-rm-"+dvCopy.ID), iconTrash, a.tr("CardDAV entfernen", "Remove CardDAV"), t.Danger, false).Clicked() {
					go func(id string) {
						_ = a.opts.Direct.DeleteCardDAVAccount(context.Background(), id)
						a.emitUpdate(func() {
							a.reloadDirectState(context.Background())
						})
					}(dvCopy.ID)
				}
			})
		}
	})
}

func (a *App) renderDirectRulesTab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, a.tr(
			"Lokale MailDiver-Filterregeln laufen automatisch auf neu eintreffenden Posteingangs- und Unterordner-Mails (z. B. INBOX, INBOX/Projekte).",
			"Local MailDiver filter rules automatically run on newly arriving inbox and subfolder emails (e.g. INBOX, INBOX/Projects).",
		)).FontSize(12).TextColor(t.TextMuted).Grow(1)
		if ui.Button(c.Key("dir-new-rule"), a.tr("+ Neue Regel", "+ New Rule")).Clicked() {
			a.openRuleEditor(nil)
		}
		if len(d.rules) > 0 {
			runAllBtn := ui.PrimaryButton(c.Key("dir-run-all-rules"), a.tr("Alle jetzt ausführen", "Run All Now"))
			if d.rulesBusy {
				runAllBtn.Disabled(true)
			}
			if runAllBtn.Clicked() && !d.rulesBusy {
				a.runAllDirectRulesNow(context.Background())
			}
		}
	})

	if d.rulesErr != "" {
		ui.Text(c, d.rulesErr).FontSize(12).TextColor(t.Danger)
	}

	if issues := triage.FindFilterIssues(d.rules, ""); len(issues) > 0 {
		errCnt := 0
		for _, is := range issues {
			if is.Severity == "error" {
				errCnt++
			}
		}
		if errCnt > 0 {
			ui.Column(c.Key("dir-rule-issues")).FillWidth().Padding(8, 10).Radius(8).Background(withAlpha(t.Warning, 0.12)).Border(1, withAlpha(t.Warning, 0.45)).Gap(3).Children(func() {
				ui.Text(c, a.trf("Erkannte Regel-Hinweise (%d):", "Detected rule issues (%d):", errCnt)).FontSize(11.5).Bold().TextColor(t.Warning)
				shown := 0
				for _, is := range issues {
					if is.Severity != "error" {
						continue
					}
					ui.Text(c, "• "+is.FilterName+": "+is.Detail).FontSize(11).TextColor(t.Text)
					shown++
					if shown >= 4 {
						break
					}
				}
			})
		}
	}

	if d.ruleEditOpen {
		a.renderRuleEditorPanel(c)
	}

	if len(d.rules) == 0 {
		ui.Column(c).FillWidth().Padding(18).Radius(8).Background(t.Background).Border(1, t.Border).Center().Gap(6).Children(func() {
			ui.Text(c, a.tr("Noch keine Filterregeln gespeichert.", "No filter rules saved yet.")).FontSize(13).Bold()
			ui.Text(c, a.tr("Aktiviere unten eine 1-Klick-Vorlage oder markiere Mails in der Liste → „Quick-Regel“.", "Enable a 1-click preset below or select emails in the list → “Quick Rule”.")).FontSize(12).TextColor(t.TextMuted)
		})
	} else {
		accFirst := map[string]int{}
		accLast := map[string]int{}
		for idx, r := range d.rules {
			if _, ok := accFirst[r.AccountID]; !ok {
				accFirst[r.AccountID] = idx
			}
			accLast[r.AccountID] = idx
		}
		for i, flt := range d.rules {
			fCopy := flt
			isFirstForAcc := i == accFirst[fCopy.AccountID]
			isLastForAcc := i == accLast[fCopy.AccountID]
			condSummary := triage.FormatConditionChain(fCopy.Conditions, fCopy.MatchAll, a.uiLang())
			var acts []string
			for _, ac := range fCopy.Actions {
				if ac.TargetFolderPath != "" {
					acts = append(acts, ac.Type+" → "+ac.TargetFolderPath)
				} else {
					acts = append(acts, ac.Type)
				}
			}
			ui.Column(c.Key(fmt.Sprintf("dir-rule-%d-%s", i, fCopy.Name))).FillWidth().Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, fCopy.Name).FontSize(13).Bold().Grow(1).SingleLine()
					if fCopy.AccountID != "" {
						pillBadge(c, a.accountDisplayName(fCopy.AccountID), t.Surface, t.TextMuted)
					}
					if fCopy.Enabled {
						pillBadge(c, a.tr("Aktiv", "Active"), withAlpha(t.Success, 0.18), t.Success)
					} else {
						pillBadge(c, a.tr("Pausiert", "Paused"), t.Surface, t.TextMuted)
					}
					if ui.Button(c.Key(fmt.Sprintf("dir-rup-%d", i)), "↑").Tooltip(a.tr("Nach oben", "Move up")).Padding(2, 6).Disabled(isFirstForAcc).Clicked() {
						a.moveDirectRule(context.Background(), fCopy.AccountID, fCopy.Name, -1)
					}
					if ui.Button(c.Key(fmt.Sprintf("dir-rdn-%d", i)), "↓").Tooltip(a.tr("Nach unten", "Move down")).Padding(2, 6).Disabled(isLastForAcc).Clicked() {
						a.moveDirectRule(context.Background(), fCopy.AccountID, fCopy.Name, 1)
					}
					toggleLbl := a.tr("Pausieren", "Pause")
					if !fCopy.Enabled {
						toggleLbl = a.tr("Aktivieren", "Enable")
					}
					if ui.Button(c.Key(fmt.Sprintf("dir-rtog-%d", i)), toggleLbl).Clicked() {
						a.toggleDirectRule(context.Background(), fCopy.AccountID, fCopy.Name, !fCopy.Enabled)
					}
					runBtn := ui.Button(c.Key(fmt.Sprintf("dir-rrun-%d", i)), a.tr("Ausführen", "Run"))
					if d.rulesBusy {
						runBtn.Disabled(true)
					}
					if runBtn.Clicked() && !d.rulesBusy {
						a.runDirectRuleNow(context.Background(), fCopy.AccountID, fCopy.Name)
					}
					if iconBtn(c.Key(fmt.Sprintf("dir-redit-%d", i)), iconPen, a.tr("Regel bearbeiten", "Edit rule"), ui.Color{}, false).Clicked() {
						a.openRuleEditor(&fCopy)
					}
					if iconBtn(c.Key(fmt.Sprintf("dir-rdel-%d", i)), iconTrash, a.tr("Regel löschen", "Delete rule"), t.Danger, false).Clicked() {
						a.deleteDirectRule(context.Background(), fCopy.AccountID, fCopy.Name)
					}
				})
				if condSummary != "" {
					ui.Text(c, condSummary).FontSize(11).TextColor(t.TextMuted)
				}
				if len(acts) > 0 {
					ui.Text(c, "→ "+strings.Join(acts, ", ")).FontSize(11.5).TextColor(t.Accent)
				}
			})
		}
	}

	// Curated 1-click preset rules (Atlassian Access/App requests, Backups/Sandbox, Server updates).
	ui.Divider(c)
	ui.Text(c, a.tr("Regel-Vorlagen (1-Klick)", "Rule Presets (1-Click)")).FontSize(13).Bold()
	accID := a.presetAccountID()
	existingNames := make(map[string]bool, len(d.rules))
	for _, r := range d.rules {
		existingNames[strings.ToLower(strings.TrimSpace(r.Name))] = true
	}
	for _, preset := range triage.BuiltinFilterPresets(a.uiLang(), "") {
		pCopy := preset
		target := a.resolvePresetTargetFolder(accID, pCopy.DefaultFolder)
		installed := existingNames[strings.ToLower(strings.TrimSpace(pCopy.Name))]
		ui.Row(c.Key("dir-preset-"+pCopy.ID)).FillWidth().Padding(8, 10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, pCopy.Name).FontSize(12.5).Bold().SingleLine()
					pillBadge(c, "→ "+target, t.Surface, t.TextMuted)
				})
				ui.Text(c, pCopy.Description).FontSize(11).TextColor(t.TextMuted)
			})
			lbl := a.tr("Hinzufügen", "Add")
			if installed {
				lbl = a.tr("Aktualisieren", "Update")
			}
			btn := ui.Button(c.Key("dir-preset-add-"+pCopy.ID), lbl)
			if d.rulesBusy || accID == "" {
				btn.Disabled(true)
			}
			if btn.Clicked() && !d.rulesBusy && accID != "" {
				a.installFilterPreset(context.Background(), pCopy)
			}
		})
	}
}

// --- Everyday Mail Actions (Attachments, .ics Invites, Spam, Folders, Empty Trash) ---

// DownloadAndOpenAttachment extracts an attachment into ~/Downloads and opens safe file types with the OS default handler.
// Executable/script/active web attachments are saved to ~/Downloads only without auto-launching them.
func (a *App) DownloadAndOpenAttachment(ctx context.Context, msgID, partName, filename string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	a.toast(a.trf("Lade Anhang „%s“…", "Downloading attachment “%s”…", filename), "info")
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		savedPath, err := dc.SaveAttachmentToDownloads(c, msgID, partName, filename)
		risky := false
		var openErr error
		if err == nil {
			risky = isRiskyAttachmentExt(savedPath)
			if !risky {
				openErr = openLocalFile(savedPath)
			}
		}
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Anhang-Fehler: ", "Attachment error: ")+err.Error(), "error")
				return
			}
			if risky {
				a.toast(a.trf("Nur in Downloads gespeichert (nicht automatisch gestartet): %s", "Saved to Downloads only (not auto-opened for safety): %s", filepath.Base(savedPath)), "warning")
				return
			}
			if openErr != nil {
				a.toast(a.trf("In Downloads gespeichert (Öffnen fehlgeschlagen): %s", "Saved to Downloads (could not open): %s", filepath.Base(savedPath)), "warning")
				return
			}
			a.toast(a.trf("Gespeichert & geöffnet: %s", "Saved & opened: %s", filepath.Base(savedPath)), "success")
		})
	}()
}

// SaveSelectedMessageEML exports the currently selected message as an .eml file to ~/Downloads.
func (a *App) SaveSelectedMessageEML(ctx context.Context) {
	dc := a.opts.Direct
	msg := a.selectedMsg
	if dc == nil || msg == nil || msg.ID == "" {
		return
	}
	msgID := msg.ID
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		savedPath, err := dc.SaveMessageEMLToDownloads(c, msgID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("EML-Export fehlgeschlagen: ", "EML export failed: ")+err.Error(), "error")
				return
			}
			a.toast(a.trf("Als .eml gespeichert: %s", "Saved as .eml: %s", filepath.Base(savedPath)), "success")
		})
	}()
}

// isRiskyAttachmentExt returns true for executable, script, installer, or active HTML/SVG attachments
// that should never be auto-launched directly upon download.
func isRiskyAttachmentExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch ext {
	case ".exe", ".bat", ".cmd", ".com", ".msi", ".ps1", ".psm1", ".sh", ".command",
		".app", ".pkg", ".dmg", ".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh", ".hta",
		".scr", ".pif", ".jar", ".lnk", ".reg", ".html", ".htm", ".svg", ".xhtml":
		return true
	}
	return false
}

// ImportMailICS imports an .ics calendar invitation embedded in a message into the local calendar.
func (a *App) ImportMailICS(ctx context.Context, msgID string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		n, err := dc.ImportMessageICS(c, msgID, mailengine.LocalCalendarID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Einladung-Fehler: ", "Invite error: ")+err.Error(), "error")
				return
			}
			a.toast(a.trf("%d Termin(e) in den Kalender übernommen", "Added %d event(s) to calendar", n), "success")
			if a.selectedMsg != nil && a.selectedMsg.ID == msgID {
				a.reloadMessage(context.Background(), msgID)
			}
			a.RefreshAll(context.Background())
		})
	}()
}

// MarkMailSpam moves a message to the account's Spam/Junk folder and selects the next message.
func (a *App) MarkMailSpam(ctx context.Context, msgID string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	if a.readOnly {
		a.toast(a.tr("Read-Only-Modus aktiv — Änderung blockiert", "Read-only mode active — change blocked"), "error")
		return
	}
	// Snapshot message info for Undo before moving to Spam.
	var undoMeta backend.UndoMoveItem
	for _, m := range a.messages {
		if m.ID == msgID {
			undoMeta = backend.UndoMoveItem{
				AccountID:      m.AccountID,
				MessageID:      m.MessageID,
				FromFolderPath: m.Folder,
				RestoreUnread:  m.Unread,
			}
			break
		}
	}
	if undoMeta.AccountID == "" && a.selectedMsg != nil && a.selectedMsg.ID == msgID {
		undoMeta = backend.UndoMoveItem{
			AccountID:      a.selectedMsg.AccountID,
			MessageID:      a.selectedMsg.MessageID,
			FromFolderPath: a.selectedMsg.Folder,
			RestoreUnread:  a.selectedMsg.Unread,
		}
	}
	if undoMeta.AccountID != "" {
		for _, f := range a.folders {
			if f.AccountID == undoMeta.AccountID && strings.EqualFold(f.Type, "junk") {
				undoMeta.ToFolderPath = f.Path
				break
			}
		}
		if undoMeta.ToFolderPath == "" {
			undoMeta.ToFolderPath = "Junk"
		}
	}

	nextID := a.neighborAfterRemoval(msgID)
	go func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		err := dc.MarkSpam(c, msgID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Spam-Fehler: ", "Spam error: ")+err.Error(), "error")
				return
			}
			if a.selectedID == msgID {
				a.selectedID = ""
				a.selectedMsg = nil
				if nextID != "" && !a.isPhone() {
					a.SelectMessage(context.Background(), nextID)
				} else {
					a.syncPreviewForMessage(nil)
					a.phonePopMailIfOpen()
				}
			}
			if undoMeta.AccountID != "" && undoMeta.MessageID != "" && undoMeta.FromFolderPath != "" && undoMeta.ToFolderPath != "" {
				a.undo = &undoAction{
					Kind:      "move",
					Label:     a.tr("Als Spam markiert", "Marked as Spam"),
					CreatedAt: time.Now(),
					MoveItems: []backend.UndoMoveItem{undoMeta},
				}
			}
			a.toast(a.tr("Als Spam markiert", "Marked as Spam"), "success")
			a.startMessageLoad(context.Background())
			a.refreshFoldersBackground(context.Background())
		})
	}()
}

// MarkMailNotSpam moves a message from Junk/Spam back to the account's INBOX.
func (a *App) MarkMailNotSpam(ctx context.Context, msgID string) {
	if msgID == "" {
		return
	}
	nextID := a.neighborAfterRemoval(msgID)
	if a.isPhone() {
		nextID = ""
	}
	a.performMessageAction(ctx, msgID, backend.MessageActionsRequest{MoveToPath: "INBOX"}, a.tr("Kein Spam — in Posteingang verschoben", "Not Spam — moved to Inbox"), nextID)
}

// CreateIMAPFolder creates a new IMAP folder for accountID.
func (a *App) CreateIMAPFolder(ctx context.Context, accountID, name string) {
	dc := a.opts.Direct
	name = strings.TrimSpace(name)
	if dc == nil || name == "" || a.direct.newFolderBusy {
		return
	}
	a.direct.newFolderBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		err := dc.CreateFolder(c, accountID, name)
		a.emitUpdate(func() {
			a.direct.newFolderBusy = false
			if err != nil {
				a.toast(a.tr("Ordner-Fehler: ", "Folder error: ")+err.Error(), "error")
				return
			}
			a.direct.newFolderAccountID = ""
			a.direct.newFolderName = ""
			a.toast(a.trf("Ordner „%s“ erstellt", "Created folder “%s”", name), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// RenameIMAPFolder renames an IMAP folder and refreshes the folder list.
func (a *App) RenameIMAPFolder(ctx context.Context, accountID, folderID, newName string) {
	dc := a.opts.Direct
	newName = strings.TrimSpace(newName)
	if dc == nil || folderID == "" || newName == "" || a.direct.newFolderBusy {
		return
	}
	a.direct.newFolderBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		err := dc.RenameFolder(c, accountID, folderID, newName)
		a.emitUpdate(func() {
			a.direct.newFolderBusy = false
			if err != nil {
				a.toast(a.tr("Ordner umbenennen fehlgeschlagen: ", "Rename folder failed: ")+err.Error(), "error")
				return
			}
			a.direct.renameFolderID = ""
			a.direct.renameFolderAccID = ""
			a.direct.renameFolderName = ""
			a.toast(a.trf("Ordner umbenannt in „%s“", "Folder renamed to “%s”", newName), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// DeleteIMAPFolder deletes a user IMAP folder and refreshes the folder list.
func (a *App) DeleteIMAPFolder(ctx context.Context, accountID, folderID string) {
	dc := a.opts.Direct
	if dc == nil || folderID == "" || a.direct.newFolderBusy {
		return
	}
	a.direct.delFolderArmID = ""
	a.direct.newFolderBusy = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		err := dc.DeleteFolder(c, accountID, folderID)
		a.emitUpdate(func() {
			a.direct.newFolderBusy = false
			if err != nil {
				a.toast(a.tr("Ordner löschen fehlgeschlagen: ", "Delete folder failed: ")+err.Error(), "error")
				return
			}
			if a.navID == "folder:"+folderID {
				a.setNav("recent")
			}
			a.toast(a.tr("Ordner gelöscht", "Folder deleted"), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// EmptyAccountTrash permanently empties the Trash folder of accountID.
func (a *App) EmptyAccountTrash(ctx context.Context, accountID string) {
	dc := a.opts.Direct
	if dc == nil || accountID == "" {
		return
	}
	a.direct.emptyTrashArmID = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := dc.EmptyTrash(c, accountID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Papierkorb-Fehler: ", "Empty trash error: ")+err.Error(), "error")
				return
			}
			a.selectedID = ""
			a.selectedMsg = nil
			a.syncPreviewForMessage(nil)
			a.ClearMailSelection()
			a.toast(a.trf("Papierkorb geleert (%d Mails entfernt)", "Trash emptied (%d emails deleted)", n), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// EmptyAccountSpam permanently empties the Spam/Junk folder of accountID.
func (a *App) EmptyAccountSpam(ctx context.Context, accountID string) {
	dc := a.opts.Direct
	if dc == nil || accountID == "" {
		return
	}
	a.direct.emptySpamArmID = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := dc.EmptySpam(c, accountID)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Spam-Fehler: ", "Empty spam error: ")+err.Error(), "error")
				return
			}
			a.selectedID = ""
			a.selectedMsg = nil
			a.syncPreviewForMessage(nil)
			a.ClearMailSelection()
			a.toast(a.trf("Spam geleert (%d Mails entfernt)", "Spam emptied (%d emails deleted)", n), "success")
			a.RefreshAll(context.Background())
		})
	}()
}

func openLocalFile(path string) error {
	if isMobileOS() {
		return mygo.Shell.OpenPath(path)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "shell32.dll,ShellExec_RunDLL", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// RespondMailICS accepts, tentatively accepts, or declines an .ics invitation email,
// importing the event on Accept/Tentative and sending an RFC 5546 iTIP REPLY via SMTP.
func (a *App) RespondMailICS(ctx context.Context, msgID, status string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		res, err := dc.RespondMessageICS(c, msgID, mailengine.LocalCalendarID, status)
		a.emitUpdate(func() {
			if err != nil {
				a.toast(a.tr("Einladung-Fehler: ", "Invite error: ")+err.Error(), "error")
				return
			}
			var verb string
			switch res.Status {
			case "accepted":
				verb = a.tr("Zugesagt", "Accepted")
			case "tentative":
				verb = a.tr("Vorläufig zugesagt", "Marked tentative")
			default:
				verb = a.tr("Abgesagt", "Declined")
			}
			var detail string
			if res.ReplySent && res.OrganizerEmail != "" {
				detail = a.trf(" · Antwort an %s gesendet", " · RSVP sent to %s", res.OrganizerEmail)
			} else if res.ReplyError != "" {
				detail = a.trf(" · Antwort nicht gesendet: %s", " · Reply not sent: %s", res.ReplyError)
			}
			if res.ImportedCount > 0 {
				detail = a.tr(" · im Kalender gespeichert", " · saved to calendar") + detail
			} else if res.AlreadyInCalendar != "" {
				detail = a.trf(" · bereits in %s", " · already in %s", res.AlreadyInCalendar) + detail
			}
			if a.selectedMsg != nil && a.selectedMsg.ID == msgID {
				if a.selectedMsg.ICSInvite != nil {
					a.selectedMsg.ICSInvite.ResponseStatus = res.Status
					if res.AlreadyInCalendar != "" && a.selectedMsg.ICSInvite.InCalendarName == "" {
						a.selectedMsg.ICSInvite.InCalendarName = res.AlreadyInCalendar
					}
				}
				a.reloadMessage(context.Background(), msgID)
			}
			a.toast(verb+detail, "success")
			a.RefreshAll(context.Background())
		})
	}()
}

// --- Read-Only Thunderbird Profile Importer ---

func (a *App) discoverThunderbirdProfiles() {
	d := &a.direct
	d.tbProfiles = mailengine.DiscoverThunderbirdProfiles()
	if len(d.tbProfiles) > 0 && strings.TrimSpace(d.tbProfilePath) == "" {
		chosen := d.tbProfiles[0]
		for _, p := range d.tbProfiles {
			if p.IsDefault {
				chosen = p
				break
			}
		}
		d.tbProfilePath = chosen.Path
		d.tbSelectedLabel = tbProfileLabel(chosen)
		a.inspectThunderbirdProfile(chosen.Path)
	}
}

func tbProfileLabel(p mailengine.TBProfileInfo) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = filepath.Base(p.Path)
	}
	if p.IsDefault {
		return name + " (Standard)"
	}
	return name
}

func (a *App) inspectThunderbirdProfile(path string) {
	d := &a.direct
	path = strings.TrimSpace(path)
	d.tbProfilePath = path
	d.tbInspectErr = ""
	d.tbResult = nil
	d.tbResultErr = ""
	if path == "" {
		d.tbPreview = nil
		return
	}
	prev, err := mailengine.InspectThunderbirdProfile(path)
	if err != nil {
		d.tbPreview = nil
		d.tbInspectErr = err.Error()
		return
	}
	d.tbPreview = &prev
}

func (a *App) applyThunderbirdImport(ctx context.Context) {
	dc := a.opts.Direct
	d := &a.direct
	if dc == nil || d.tbBusy || strings.TrimSpace(d.tbProfilePath) == "" {
		return
	}
	path := strings.TrimSpace(d.tbProfilePath)
	opts := mailengine.TBImportOptions{
		ImportAccounts:    d.tbOptAccounts,
		ImportICalFeeds:   d.tbOptICal,
		ImportLocalEvents: d.tbOptLocalEv,
		ImportContacts:    d.tbOptContacts,
		ImportFilters:     d.tbOptFilters,
	}
	d.tbBusy = true
	d.tbResult = nil
	d.tbResultErr = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		res, err := dc.ApplyThunderbirdImport(c, path, opts)
		a.emitUpdate(func() {
			a.direct.tbBusy = false
			if err != nil {
				a.direct.tbResultErr = err.Error()
				a.toast(a.tr("TB-Import fehlgeschlagen: ", "TB import failed: ")+err.Error(), "error")
				return
			}
			a.direct.tbResult = &res
			a.toast(a.trf(
				"Thunderbird-Import abgeschlossen: %d Konto/en, %d Kontakte, %d Termine, %d Regeln",
				"Thunderbird import finished: %d account(s), %d contacts, %d events, %d rules",
				res.AccountsAdded, res.ContactsAdded, res.EventsAdded, res.FiltersAdded,
			), "success")
			a.reloadDirectState(context.Background())
			a.LoadContacts(context.Background())
			a.RefreshAll(context.Background())
		})
	}()
}

func (a *App) renderDirectTBImportTab(c *ui.Context) {
	t := c.Theme()
	d := &a.direct

	// Read-only safety notice
	ui.Row(c).FillWidth().Padding(10, 12).Radius(8).Background(withAlpha(t.Accent, 0.10)).Border(1, withAlpha(t.Accent, 0.35)).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconLock).Size(16, 16).TextColor(t.Accent).Shrink(0)
		ui.Text(c, a.tr(
			"Nur-Lese-Import ohne Thunderbird-Add-on: Dein bestehendes Thunderbird-Profil wird niemals verändert. Passwörter werden aus Sicherheitsgründen nicht entschlüsselt und nach dem Import einmalig im E-Mail-Tab eingetragen.",
			"Read-only import without any Thunderbird add-on: your existing Thunderbird profile is never modified. Passwords are not decrypted for security and can be entered once in the Email tab afterwards.",
		)).FontSize(11.5).TextColor(t.Text).Grow(1)
	})

	// Profile discovery & folder selection
	ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("Thunderbird-Profil auswählen", "Select Thunderbird Profile")).FontSize(13).Bold().Grow(1)
			if ui.Button(c.Key("tb-rescan"), a.tr("Neu suchen", "Rescan")).Clicked() {
				d.tbProfilePath = ""
				a.discoverThunderbirdProfiles()
			}
		})

		if len(d.tbProfiles) > 0 {
			labels := make([]string, 0, len(d.tbProfiles))
			byLabel := make(map[string]string, len(d.tbProfiles))
			for _, p := range d.tbProfiles {
				lbl := tbProfileLabel(p)
				labels = append(labels, lbl)
				byLabel[lbl] = p.Path
			}
			if d.tbSelectedLabel == "" && len(labels) > 0 {
				d.tbSelectedLabel = labels[0]
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Gefundene Profile", "Detected Profiles")).FontSize(12).Width(135).TextColor(t.TextMuted)
				if ui.Select(c.Key("tb-prof-sel"), &d.tbSelectedLabel, labels).Grow(1).Changed() {
					if path := byLabel[d.tbSelectedLabel]; path != "" {
						a.inspectThunderbirdProfile(path)
					}
				}
			})
		} else {
			ui.Text(c, a.tr(
				"Kein Standard-Thunderbird-Profil automatisch gefunden — wähle unten den Profilordner manuell aus.",
				"No default Thunderbird profile auto-detected — select your profile folder manually below.",
			)).FontSize(11.5).TextColor(t.TextMuted)
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Profilordner", "Profile Folder")).FontSize(12).Width(135).TextColor(t.TextMuted)
			if ui.Button(c.Key("tb-browse"), a.tr("Ordner wählen…", "Browse…")).Clicked() {
				a.pickFile(FilePickRequest{
					Mode:       "dir",
					Title:      a.tr("Thunderbird-Profilordner auswählen", "Select Thunderbird Profile Folder"),
					ShowHidden: true,
				}, func(paths []string) {
					if len(paths) > 0 {
						a.inspectThunderbirdProfile(paths[0])
					}
				})
			}
			pathIn := ui.TextInput(c.Key("tb-path"), &d.tbProfilePath).Placeholder("~/Library/Thunderbird/Profiles/xxxx.default-release").Grow(1)
			if ui.Button(c.Key("tb-inspect"), a.tr("Prüfen", "Inspect")).Clicked() || pathIn.Submitted() {
				a.inspectThunderbirdProfile(d.tbProfilePath)
			}
		})

		if d.tbInspectErr != "" {
			ui.Text(c, d.tbInspectErr).FontSize(12).TextColor(t.Danger)
		}
	})

	// Live preview & selective import options
	if d.tbPreview != nil {
		prev := d.tbPreview
		ui.Column(c).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Border).Gap(8).Children(func() {
			ui.Text(c, a.tr("Gefundene Daten (Vorschau)", "Detected Data (Preview)")).FontSize(13).Bold()

			ui.Checkbox(c.Key("tb-opt-acc"), &d.tbOptAccounts, a.trf(
				"E-Mail-Konten inkl. Signaturen (%d Konten, davon %d mit Signatur)",
				"Email accounts incl. signatures (%d accounts, %d with signature)",
				len(prev.Accounts), prev.SignatureCnt,
			))
			if len(prev.Accounts) > 0 {
				ui.Column(c).FillWidth().Padding(0, 0, 0, 22).Gap(3).Children(func() {
					for i, acc := range prev.Accounts {
						ui.Row(c.Key(fmt.Sprintf("tb-acc-%d", i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, fmt.Sprintf("%s <%s> (%s / %s)", acc.Name, acc.Email, acc.IMAPHost, acc.SMTPHost)).FontSize(11.5).TextColor(t.TextMuted).Grow(1).SingleLine()
							if strings.TrimSpace(acc.Signature) != "" {
								pillBadge(c, a.tr("✓ Signatur", "✓ Signature"), withAlpha(t.Success, 0.16), t.Success)
							}
						})
					}
				})
			}

			ui.Checkbox(c.Key("tb-opt-ical"), &d.tbOptICal, a.trf("iCal-Kalender-Abos (%d)", "iCal calendar feeds (%d)", len(prev.ICalFeeds)))
			ui.Checkbox(c.Key("tb-opt-lev"), &d.tbOptLocalEv, a.trf("Lokale Kalendertermine (%d Termine)", "Local calendar events (%d events)", len(prev.LocalEvents)))
			ui.Checkbox(c.Key("tb-opt-ct"), &d.tbOptContacts, a.trf("Adressbuch-Kontakte (%d Kontakte)", "Address book contacts (%d contacts)", len(prev.Contacts)))
			ui.Checkbox(c.Key("tb-opt-flt"), &d.tbOptFilters, a.trf("Filterregeln (%d Regeln)", "Filter rules (%d rules)", len(prev.Filters)))

			// Inform user about Google / CalDAV calendars that need OAuth sign-in
			var cloudCals []string
			for _, dc := range prev.OtherCals {
				if dc.Type == "gdata" || dc.Type == "caldav" || dc.IsGoogle {
					cloudCals = append(cloudCals, fmt.Sprintf("%s (%s)", dc.Name, dc.Type))
				}
			}
			if len(cloudCals) > 0 {
				ui.Text(c, a.trf(
					"Hinweis: %d Cloud-Kalender erkannt (%s). Bitte verbinde Google Kalender zusätzlich im Tab „Kalender (Google & iCal)“.",
					"Note: %d cloud calendar(s) detected (%s). Please connect Google Calendar in the “Calendar (Google & iCal)” tab.",
					len(cloudCals), strings.Join(cloudCals, ", "),
				)).FontSize(11.5).TextColor(t.Warning)
			}

			if len(prev.Warnings) > 0 {
				for _, w := range prev.Warnings {
					ui.Text(c, "• "+w).FontSize(11).TextColor(t.TextMuted)
				}
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				lbl := a.tr("Ausgewählte Daten importieren", "Import Selected Data")
				if d.tbBusy {
					lbl = a.tr("Importiere…", "Importing…")
				}
				btn := ui.PrimaryButton(c.Key("tb-run"), lbl)
				if d.tbBusy {
					btn.Disabled(true)
				}
				if btn.Clicked() && !d.tbBusy {
					a.applyThunderbirdImport(context.Background())
				}
			})
		})
	}

	if d.tbResultErr != "" {
		ui.Text(c, d.tbResultErr).FontSize(12).TextColor(t.Danger)
	}
	if d.tbResult != nil {
		r := d.tbResult
		ui.Column(c).FillWidth().Padding(12).Radius(8).Background(withAlpha(t.Success, 0.12)).Border(1, withAlpha(t.Success, 0.4)).Gap(4).Children(func() {
			ui.Text(c, a.tr("Import abgeschlossen", "Import Complete")).FontSize(13).Bold().TextColor(t.Success)
			ui.Text(c, a.trf(
				"Konten: %d neu (%d übersprungen, %d Signaturen) · iCal-Abos: %d neu · Lokale Termine: %d · Kontakte: %d neu (%d übersprungen) · Filterregeln: %d neu (%d übersprungen)",
				"Accounts: %d new (%d skipped, %d signatures) · iCal feeds: %d new · Local events: %d · Contacts: %d new (%d skipped) · Filter rules: %d new (%d skipped)",
				r.AccountsAdded, r.AccountsSkipped, r.SignaturesLoaded, r.FeedsAdded, r.EventsAdded, r.ContactsAdded, r.ContactsSkipped, r.FiltersAdded, r.FiltersSkipped,
			)).FontSize(11.5).TextColor(t.Text)
			if r.AccountsAdded > 0 {
				ui.Text(c, a.tr(
					"Nächster Schritt: Wechsle in den Tab „E-Mail (IMAP/SMTP)“, klicke bei jedem importierten Konto auf „Bearbeiten“ und gib einmalig das Passwort ein (Signatur & Serverdaten sind schon ausgefüllt).",
					"Next step: switch to the “Email (IMAP/SMTP)” tab, click “Edit” on each imported account and enter its password once (signature & server settings are already filled in).",
				)).FontSize(11.5).TextColor(t.TextMuted)
			}
		})
	}
}
