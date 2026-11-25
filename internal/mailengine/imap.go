package mailengine

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

// IsTransientNetworkError reports whether err looks like a temporary DNS, offline, or socket
// drop error (common on Android Doze / background UID firewall / Wi-Fi↔cellular handoff).
func IsTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	return IsTransientNetworkErrorMsg(err.Error())
}

// IsTransientNetworkErrorMsg classifies an error string as a transient network/DNS outage
// (as opposed to an authentication or configuration fault).
func IsTransientNetworkErrorMsg(msg string) bool {
	low := strings.ToLower(strings.TrimSpace(msg))
	if low == "" {
		return false
	}
	if low == "eof" {
		return true
	}
	if strings.Contains(low, "anmeldung fehlgeschlagen") || strings.Contains(low, "authentication") ||
		strings.Contains(low, "invalid credentials") || strings.Contains(low, "login failed") {
		return false
	}
	for _, needle := range []string{
		"no such host",
		"temporary failure in name resolution",
		"server misbehaving",
		"network is unreachable",
		"network is down",
		"no route to host",
		"software caused connection abort",
		"connection reset by peer",
		"broken pipe",
		"i/o timeout",
		"context deadline exceeded",
		"unexpected eof",
		": eof",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func dialIMAPOnce(ctx context.Context, acc config.DirectAccount, addr string, unilateral *imapclient.UnilateralDataHandler) (*imapclient.Client, error) {
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	if d, ok := ctx.Deadline(); ok {
		dialer.Deadline = d
	}
	opts := &imapclient.Options{
		Dialer:                dialer,
		TLSConfig:             &tls.Config{ServerName: strings.TrimSpace(acc.IMAPHost), MinVersion: tls.VersionTLS12},
		UnilateralDataHandler: unilateral,
	}
	mode := strings.ToLower(strings.TrimSpace(acc.IMAPTLS))
	switch {
	case mode == "plain" && isLoopbackHost(acc.IMAPHost):
		return imapclient.DialInsecure(addr, opts)
	case mode == "starttls":
		return imapclient.DialStartTLS(addr, opts)
	default:
		return imapclient.DialTLS(addr, opts)
	}
}

func dialIMAP(ctx context.Context, acc config.DirectAccount, password string, unilateral *imapclient.UnilateralDataHandler) (*imapclient.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := time.Now()
	addr := net.JoinHostPort(strings.TrimSpace(acc.IMAPHost), fmt.Sprintf("%d", acc.IMAPPort))
	user := acc.EffectiveUsername()
	authMode := strings.TrimSpace(acc.AuthType)
	if authMode == "" {
		authMode = "password"
	}
	tlsMode := strings.ToLower(strings.TrimSpace(acc.IMAPTLS))
	if tlsMode == "" {
		tlsMode = "tls"
	}
	logAccountDiag("IMAP", "dial host=%s tls=%s user=%s auth=%s pw=(%s)", addr, tlsMode, user, authMode, PasswordMeta(password))

	c, err := dialIMAPOnce(ctx, acc, addr, unilateral)
	// On Android/iOS, DNS lookups immediately after Activity.onResume or a Wi-Fi↔5G handoff
	// can fail once with "no such host" while netd unfreezes the UID's network route.
	if err != nil && ctx.Err() == nil && (runtime.GOOS == "android" || runtime.GOOS == "ios") && IsTransientNetworkError(err) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
		if retryClient, rerr := dialIMAPOnce(ctx, acc, addr, unilateral); rerr == nil {
			c, err = retryClient, nil
		} else {
			err = rerr
		}
	}
	if err != nil {
		logAccountDiag("IMAP", "dial_err host=%s dur=%s err=%s", addr, time.Since(started).Round(time.Millisecond), SanitizeDiagErr(err))
		return nil, fmt.Errorf("IMAP Verbindung zu %s fehlgeschlagen: %w", addr, err)
	}

	// Authenticate: XOAUTH2 (Gmail/Microsoft 365) when configured, else SASL PLAIN with LOGIN fallback.
	stopAuthWatch := watchIMAPCtx(ctx, c)
	caps := c.Caps()
	hasPlain := caps != nil && caps.Has(imap.AuthCap(sasl.Plain))
	hasLogin := caps != nil && caps.Has(imap.AuthCap(sasl.Login))
	loginDisabled := caps != nil && caps.Has(imap.CapLoginDisabled)
	logAccountDiag("IMAP", "connected host=%s dur=%s caps=(AUTH=PLAIN:%v AUTH=LOGIN:%v LOGINDISABLED:%v)",
		addr, time.Since(started).Round(time.Millisecond), hasPlain, hasLogin, loginDisabled)

	var authErr error
	if strings.EqualFold(authMode, "xoauth2") {
		authErr = c.Authenticate(NewXOAUTH2Client(user, password))
		if authErr != nil {
			logAccountDiag("IMAP", "auth_err host=%s user=%s mech=XOAUTH2 err=%s", addr, user, SanitizeDiagErr(authErr))
		} else {
			logAccountDiag("IMAP", "auth_ok host=%s user=%s mech=XOAUTH2", addr, user)
		}
	} else {
		if hasPlain {
			authErr = c.Authenticate(sasl.NewPlainClient("", user, password))
			if authErr != nil {
				logAccountDiag("IMAP", "auth_err host=%s user=%s mech=PLAIN err=%s", addr, user, SanitizeDiagErr(authErr))
			} else {
				logAccountDiag("IMAP", "auth_ok host=%s user=%s mech=PLAIN", addr, user)
			}
		} else {
			authErr = c.Login(user, password).Wait()
			if authErr != nil {
				logAccountDiag("IMAP", "auth_err host=%s user=%s mech=LOGIN err=%s", addr, user, SanitizeDiagErr(authErr))
			} else {
				logAccountDiag("IMAP", "auth_ok host=%s user=%s mech=LOGIN", addr, user)
			}
		}
		if authErr != nil {
			// Retry with LOGIN if PLAIN failed on a quirky server (except if LOGIN was already tried).
			if hasPlain && !loginDisabled {
				if loginErr := c.Login(user, password).Wait(); loginErr == nil {
					logAccountDiag("IMAP", "auth_ok host=%s user=%s mech=LOGIN_FALLBACK", addr, user)
					authErr = nil
				} else {
					logAccountDiag("IMAP", "auth_err host=%s user=%s mech=LOGIN_FALLBACK err=%s", addr, user, SanitizeDiagErr(loginErr))
				}
			}
		}
	}
	stopAuthWatch()
	if authErr != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("IMAP Anmeldung fehlgeschlagen (%s): %w", user, authErr)
	}
	return c, nil
}

func watchIMAPCtx(ctx context.Context, c *imapclient.Client) func() {
	if ctx == nil || c == nil {
		return func() {}
	}
	stop := context.AfterFunc(ctx, func() {
		_ = c.Close()
	})
	return func() { stop() }
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// TestIMAPConnection verifies connectivity and credentials without storing anything.
func TestIMAPConnection(ctx context.Context, acc config.DirectAccount, password string) (folderCount int, err error) {
	c, err := dialIMAP(ctx, acc, password, nil)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	mboxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return 0, fmt.Errorf("IMAP LIST fehlgeschlagen: %w", err)
	}
	return len(mboxes), nil
}

// SyncResult summarizes an incremental IMAP sync pass for one account.
type SyncResult struct {
	AccountID    string
	Folders      int
	NewMessages  []StoredMessage
	UpdatedFlags int
	RemovedUIDs  int
	// BackfillPending: older mails on the server are still missing locally; the next full sync
	// should come soon.
	BackfillPending bool
}

func (e *Engine) syncAccountIMAP(ctx context.Context, acc config.DirectAccount, inboxOnly bool) (SyncResult, error) {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		logAccountDiag("SYNC", "secret_err account=%s email=%s err=%s", acc.ID, acc.Email, SanitizeDiagErr(err))
		return SyncResult{AccountID: acc.ID}, fmt.Errorf("kein Passwort gespeichert für %s", acc.Email)
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return SyncResult{AccountID: acc.ID}, err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()

	res := SyncResult{AccountID: acc.ID}
	var folders []StoredFolder
	if inboxOnly {
		if inbox, ok := e.store.FindFolderByType(ctx, acc.ID, "inbox"); ok {
			folders = []StoredFolder{inbox}
		}
	}
	if len(folders) == 0 {
		var err error
		folders, err = e.syncFoldersIMAP(ctx, c, acc)
		if err != nil {
			return res, err
		}
	}
	res.Folders = len(folders)

	// Prioritize INBOX first, then Sent/Drafts/other folders, skipping Trash/Junk if inboxOnly.
	sort.SliceStable(folders, func(i, j int) bool {
		return folderSyncPriority(folders[i]) < folderSyncPriority(folders[j])
	})

	for _, f := range folders {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if slices.Contains(f.SpecialUse, "noselect") || slices.Contains(f.SpecialUse, "nonexistent") {
			continue
		}
		if inboxOnly && f.Type != "inbox" {
			continue
		}
		fRes, err := e.syncSingleFolderIMAP(ctx, c, acc, f, !inboxOnly)
		if err != nil {
			// Don't fail the whole account if one auxiliary folder is unselectable.
			if f.Type == "inbox" {
				return res, err
			}
			continue
		}
		res.NewMessages = append(res.NewMessages, fRes.NewMessages...)
		res.UpdatedFlags += fRes.UpdatedFlags
		res.RemovedUIDs += fRes.RemovedUIDs
		res.BackfillPending = res.BackfillPending || fRes.BackfillPending
	}

	// Prefetch newest INBOX bodies so clicking through unread mails is instant.
	// On quick/inbox-only refreshes with no new messages, skip prefetch to keep refresh snappy.
	if !inboxOnly || len(res.NewMessages) > 0 {
		prefetchLimit := 20
		if inboxOnly || runtime.GOOS == "android" || runtime.GOOS == "ios" {
			prefetchLimit = 5
		}
		if inbox, ok := e.store.FindFolderByType(ctx, acc.ID, "inbox"); ok {
			e.prefetchFolderBodies(ctx, c, acc, inbox, prefetchLimit)
		}
	}

	return res, nil
}

func folderSyncPriority(f StoredFolder) int {
	switch f.Type {
	case "inbox":
		return 0
	case "sent":
		return 1
	case "drafts":
		return 2
	case "archive":
		return 3
	case "trash":
		return 8
	case "junk":
		return 9
	default:
		return 5
	}
}

func (e *Engine) syncFoldersIMAP(ctx context.Context, c *imapclient.Client, acc config.DirectAccount) ([]StoredFolder, error) {
	mboxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("IMAP LIST: %w", err)
	}
	existingList, _ := e.store.ListFolders(ctx, acc.ID)
	existingByPath := map[string]StoredFolder{}
	for _, f := range existingList {
		existingByPath[f.Path] = f
	}

	var out []StoredFolder
	for _, mb := range mboxes {
		delim := "/"
		if mb.Delim != 0 {
			delim = string(mb.Delim)
		}
		fType, su := classifyFolderAttrs(mb.Mailbox, mb.Attrs)
		name := mb.Mailbox
		if delim != "" && strings.Contains(name, delim) {
			parts := strings.Split(name, delim)
			name = parts[len(parts)-1]
		}
		sf := StoredFolder{
			ID:         FolderID(acc.ID, mb.Mailbox),
			AccountID:  acc.ID,
			Name:       name,
			Path:       mb.Mailbox,
			Delimiter:  delim,
			Type:       fType,
			SpecialUse: su,
		}
		if prev, ok := existingByPath[mb.Mailbox]; ok {
			sf.UIDValidity = prev.UIDValidity
			sf.UIDNext = prev.UIDNext
			sf.LastSync = prev.LastSync
		}
		if err := e.store.UpsertFolder(ctx, sf); err != nil {
			return nil, err
		}
		out = append(out, sf)
	}
	if len(out) > 0 {
		keep := make(map[string]bool, len(out))
		for _, f := range out {
			keep[f.Path] = true
		}
		_ = e.store.DeleteMissingAccountFolders(ctx, acc.ID, keep)
	}
	return out, nil
}

func classifyFolderAttrs(path string, attrs []imap.MailboxAttr) (folderType string, specialUse []string) {
	for _, a := range attrs {
		switch a {
		case imap.MailboxAttrNoSelect:
			specialUse = append(specialUse, "noselect")
		case imap.MailboxAttrNonExistent:
			specialUse = append(specialUse, "nonexistent")
		case imap.MailboxAttrSent:
			folderType = "sent"
			specialUse = append(specialUse, "sent")
		case imap.MailboxAttrDrafts:
			folderType = "drafts"
			specialUse = append(specialUse, "drafts")
		case imap.MailboxAttrTrash:
			folderType = "trash"
			specialUse = append(specialUse, "trash")
		case imap.MailboxAttrJunk:
			folderType = "junk"
			specialUse = append(specialUse, "junk")
		case imap.MailboxAttrArchive:
			folderType = "archive"
			specialUse = append(specialUse, "archive")
		case imap.MailboxAttrAll:
			specialUse = append(specialUse, "all")
		}
	}
	low := strings.ToLower(strings.TrimSpace(path))
	if low == "inbox" {
		folderType = "inbox"
		if !slices.Contains(specialUse, "inbox") {
			specialUse = append(specialUse, "inbox")
		}
	}
	if folderType == "" {
		base := low
		if idx := strings.LastIndexAny(base, "/."); idx >= 0 {
			base = base[idx+1:]
		}
		switch base {
		case "sent", "sent items", "sent messages", "sent mail", "gesendet", "gesendete", "gesendete objekte", "gesendete elemente", "gesendete nachrichten":
			folderType = "sent"
		case "drafts", "entwürfe", "entwuerfe":
			folderType = "drafts"
		case "trash", "deleted", "deleted items", "deleted messages", "papierkorb", "gelöscht", "geloescht":
			folderType = "trash"
		case "junk", "spam", "junk-e-mail", "junk e-mail", "bulk mail", "werbung":
			folderType = "junk"
		case "archive", "archiv", "all mail":
			folderType = "archive"
		}
	}
	return folderType, specialUse
}

// Sync window and backfill: every sync compares flags and deletions for the newest mails of a
// folder (the window). Full syncs also fill in older mails, backfillBatch per folder and run,
// newest first, and drop local copies of older mails the server no longer has – so a mailbox is
// complete after a few runs without a long first sync.
const (
	defaultSyncWindow = 500
	otherSyncWindow   = 200
)

var backfillBatch = 1000 // a variable for tests

func (e *Engine) syncSingleFolderIMAP(ctx context.Context, c *imapclient.Client, acc config.DirectAccount, f StoredFolder, full bool) (SyncResult, error) {
	res := SyncResult{AccountID: acc.ID}
	sel, err := c.Select(f.Path, nil).Wait()
	if err != nil {
		return res, fmt.Errorf("IMAP SELECT %q: %w", f.Path, err)
	}

	// If UIDVALIDITY changed, cached UIDs for this folder are invalid.
	hadPriorSync := f.LastSync != ""
	if f.UIDValidity != 0 && sel.UIDValidity != 0 && f.UIDValidity != sel.UIDValidity {
		_ = e.store.ClearFolderMessages(ctx, f.ID)
		hadPriorSync = false
	}

	cached, err := e.store.FolderUIDFlags(ctx, f.ID)
	if err != nil {
		return res, err
	}

	if sel.NumMessages == 0 {
		if len(cached) > 0 {
			_ = e.store.ClearFolderMessages(ctx, f.ID)
			res.RemovedUIDs = len(cached)
		}
		_ = e.store.UpdateFolderSyncState(ctx, f.ID, sel.UIDValidity, uint32(sel.UIDNext))
		return res, nil
	}

	limit := uint32(acc.SyncLimit)
	if limit == 0 {
		limit = defaultSyncWindow
	}
	if f.Type != "inbox" && limit > otherSyncWindow {
		limit = otherSyncWindow
	}
	startSeq := uint32(1)
	if sel.NumMessages > limit {
		startSeq = sel.NumMessages - limit + 1
	}
	var seqSet imap.SeqSet
	seqSet.AddRange(startSeq, sel.NumMessages)

	// Step 1: Fast UID + Flags scan for the sync window.
	scanMsgs, err := c.Fetch(seqSet, &imap.FetchOptions{
		UID:   true,
		Flags: true,
	}).Collect()
	if err != nil {
		return res, fmt.Errorf("IMAP FETCH flags %q: %w", f.Path, err)
	}

	serverUIDs := make(map[uint32]bool, len(scanMsgs))
	var newUIDs []imap.UID
	var minWindowUID uint32

	for _, sm := range scanMsgs {
		uid := uint32(sm.UID)
		if uid == 0 {
			continue
		}
		serverUIDs[uid] = true
		if minWindowUID == 0 || uid < minWindowUID {
			minWindowUID = uid
		}
		read, flagged := parseIMAPFlags(sm.Flags)
		if prev, ok := cached[uid]; ok {
			if prev[0] != read || prev[1] != flagged {
				_ = e.store.UpdateMessageFlagsByUID(ctx, f.ID, uid, read, flagged)
				res.UpdatedFlags++
			}
		} else {
			newUIDs = append(newUIDs, sm.UID)
		}
	}

	// Remove expunged UIDs that should have been in the fetched window.
	var toDelete []uint32
	for cachedUID := range cached {
		if !serverUIDs[cachedUID] && (startSeq == 1 || cachedUID >= minWindowUID) {
			toDelete = append(toDelete, cachedUID)
		}
	}
	if len(toDelete) > 0 {
		_ = e.store.DeleteFolderUIDs(ctx, f.ID, toDelete)
		res.RemovedUIDs = len(toDelete)
	}

	// Step 2: Fetch envelopes & threading headers for new UIDs. Only mails above the last known
	// UIDNEXT are new: a mail that slid into the window after a deletion is an old one.
	filterSource := isFilterSourceFolder(f)
	var newFilterMsgs []StoredMessage
	if len(newUIDs) > 0 {
		sort.Slice(newUIDs, func(i, j int) bool { return newUIDs[i] > newUIDs[j] })
		err := e.fetchEnvelopesIMAP(ctx, c, acc, f, newUIDs, 50, func(sm StoredMessage) {
			if hadPriorSync && (f.UIDNext == 0 || sm.UID >= f.UIDNext) {
				if f.Type == "inbox" {
					res.NewMessages = append(res.NewMessages, sm)
				}
				if filterSource {
					newFilterMsgs = append(newFilterMsgs, sm)
				}
			}
		})
		if err != nil {
			return res, err
		}
	}

	// Step 3 (full syncs): older mails below the window.
	if full && startSeq > 1 && minWindowUID > 1 && f.Type != "trash" && f.Type != "junk" {
		pending, removed, err := e.backfillOlderIMAP(ctx, c, acc, f, minWindowUID, cached)
		if err != nil && ctx.Err() != nil {
			return res, err
		}
		res.BackfillPending = pending
		res.RemovedUIDs += removed
	}

	_ = e.store.UpdateFolderSyncState(ctx, f.ID, sel.UIDValidity, uint32(sel.UIDNext))

	// Execute enabled local PostDive filters on newly arrived INBOX and active INBOX/* subfolder messages.
	if filterSource && len(newFilterMsgs) > 0 {
		remaining := e.applyLocalFiltersOnNewMessages(ctx, c, acc, f, newFilterMsgs)
		if f.Type == "inbox" {
			res.NewMessages = remaining
		}
	}
	return res, nil
}

// fetchEnvelopesIMAP fetches envelopes and threading headers for uids (in chunks) and stores
// them; stored is called for each stored mail.
func (e *Engine) fetchEnvelopesIMAP(ctx context.Context, c *imapclient.Client, acc config.DirectAccount, f StoredFolder, uids []imap.UID, chunk int, stored func(StoredMessage)) error {
	hdrSection := &imap.FetchItemBodySection{
		Specifier:    imap.PartSpecifierHeader,
		HeaderFields: []string{"Message-ID", "In-Reply-To", "References", "List-Id", "List-Unsubscribe"},
		Peek:         true,
	}
	for i := 0; i < len(uids); i += chunk {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := min(i+chunk, len(uids))
		var uidSet imap.UIDSet
		for _, u := range uids[i:end] {
			uidSet.AddNum(u)
		}
		fetched, err := c.Fetch(uidSet, &imap.FetchOptions{
			UID:          true,
			Flags:        true,
			Envelope:     true,
			InternalDate: true,
			RFC822Size:   true,
			BodySection:  []*imap.FetchItemBodySection{hdrSection},
		}).Collect()
		if err != nil {
			return fmt.Errorf("IMAP FETCH headers %q: %w", f.Path, err)
		}
		for _, fm := range fetched {
			sm := fetchBufferToStoredMessage(acc.ID, f, fm, hdrSection)
			if err := e.store.UpsertMessage(ctx, sm); err != nil {
				continue
			}
			if stored != nil {
				stored(sm)
			}
		}
	}
	return nil
}

// backfillOlderIMAP handles the mails below the sync window (UIDs < below): local copies the
// server no longer has are removed, and up to backfillBatch of the newest missing ones are
// fetched. It reports whether older mails are still missing and how many copies it removed.
func (e *Engine) backfillOlderIMAP(ctx context.Context, c *imapclient.Client, acc config.DirectAccount, f StoredFolder, below uint32, cached map[uint32][3]bool) (bool, int, error) {
	var older imap.UIDSet
	older.AddRange(1, imap.UID(below-1))
	data, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{older}}, nil).Wait()
	if err != nil {
		return false, 0, fmt.Errorf("IMAP UID SEARCH %q: %w", f.Path, err)
	}
	server := data.AllUIDs()
	onServer := make(map[uint32]bool, len(server))
	var missing []imap.UID
	for _, u := range server {
		onServer[uint32(u)] = true
		if _, ok := cached[uint32(u)]; !ok {
			missing = append(missing, u)
		}
	}
	var gone []uint32
	for uid := range cached {
		if uid < below && !onServer[uid] {
			gone = append(gone, uid)
		}
	}
	if len(gone) > 0 {
		_ = e.store.DeleteFolderUIDs(ctx, f.ID, gone)
	}
	if len(missing) == 0 {
		return false, len(gone), nil
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] > missing[j] })
	pending := len(missing) > backfillBatch
	if pending {
		missing = missing[:backfillBatch]
	}
	if err := e.fetchEnvelopesIMAP(ctx, c, acc, f, missing, 200, nil); err != nil {
		return true, len(gone), err
	}
	return pending, len(gone), nil
}

func (e *Engine) prefetchFolderBodies(ctx context.Context, c *imapclient.Client, acc config.DirectAccount, f StoredFolder, limit int) {
	uids, err := e.store.UnfetchedRecentUIDs(ctx, f.ID, limit)
	if err != nil || len(uids) == 0 {
		return
	}
	if _, err := c.Select(f.Path, nil).Wait(); err != nil {
		return
	}
	bodySec := &imap.FetchItemBodySection{Peek: true}
	for i := 0; i < len(uids); i += 10 {
		if ctx.Err() != nil {
			return
		}
		end := min(i+10, len(uids))
		var uidSet imap.UIDSet
		for _, u := range uids[i:end] {
			uidSet.AddNum(imap.UID(u))
		}
		fetched, err := c.Fetch(uidSet, &imap.FetchOptions{
			UID:         true,
			Flags:       true,
			BodySection: []*imap.FetchItemBodySection{bodySec},
		}).Collect()
		if err != nil {
			return
		}
		for _, fm := range fetched {
			raw := fm.FindBodySection(bodySec)
			if len(raw) == 0 {
				continue
			}
			id := MessageStoreID(acc.ID, f.Path, uint32(fm.UID))
			existing, err := e.store.GetMessage(ctx, id)
			if err != nil {
				continue
			}
			mergeParsedIntoStored(&existing, raw)
			_ = e.store.UpsertMessage(ctx, existing)
		}
	}
}

const maxFetchRawEMLBytes = 50 << 20

func (e *Engine) fetchMessageBodyIMAP(ctx context.Context, acc config.DirectAccount, m StoredMessage) (StoredMessage, error) {
	if m.Size > maxFetchRawEMLBytes {
		return m, fmt.Errorf("Nachricht überschreitet das Größenlimit von %d MB", maxFetchRawEMLBytes>>20)
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return m, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return m, err
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	sel, err := c.Select(m.FolderPath, nil).Wait()
	if err != nil {
		return m, fmt.Errorf("IMAP SELECT %q: %w", m.FolderPath, err)
	}
	if f, ok := e.store.FindFolderByIDOrPath(ctx, acc.ID, m.FolderPath); ok && f.UIDValidity > 0 && sel.UIDValidity > 0 && sel.UIDValidity != f.UIDValidity {
		return m, fmt.Errorf("UIDVALIDITY für %q hat sich geändert (lokal %d, Server %d) – bitte zuerst synchronisieren", m.FolderPath, f.UIDValidity, sel.UIDValidity)
	}
	bodySec := &imap.FetchItemBodySection{Peek: true}
	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(m.UID))
	fetched, err := c.Fetch(uidSet, &imap.FetchOptions{
		UID:         true,
		Flags:       true,
		RFC822Size:  true,
		BodySection: []*imap.FetchItemBodySection{bodySec},
	}).Collect()
	if err != nil || len(fetched) == 0 {
		if err == nil {
			err = fmt.Errorf("not_found: UID %d not found in %q", m.UID, m.FolderPath)
		}
		return m, err
	}
	raw := fetched[0].FindBodySection(bodySec)
	if len(raw) > maxFetchRawEMLBytes {
		return m, fmt.Errorf("Nachricht überschreitet das Größenlimit von %d MB", maxFetchRawEMLBytes>>20)
	}
	mergeParsedIntoStored(&m, raw)
	_ = e.store.UpsertMessage(ctx, m)
	return m, nil
}

func mergeParsedIntoStored(m *StoredMessage, raw []byte) {
	parsed, err := ParseRawMessage(raw)
	if err != nil {
		m.BodyText = strings.TrimSpace(string(raw))
		m.BodyFetched = true
		m.RawEML = raw
		return
	}
	if parsed.Subject != "" && m.Subject == "" {
		m.Subject = parsed.Subject
	}
	if parsed.From != "" && m.From == "" {
		m.From = parsed.From
	}
	if len(parsed.To) > 0 && len(m.To) == 0 {
		m.To = parsed.To
	}
	if len(parsed.CC) > 0 && len(m.CC) == 0 {
		m.CC = parsed.CC
	}
	if parsed.MessageID != "" && m.MessageID == "" {
		m.MessageID = parsed.MessageID
	}
	if parsed.InReplyTo != "" && m.InReplyTo == "" {
		m.InReplyTo = parsed.InReplyTo
	}
	if parsed.References != "" && m.References == "" {
		m.References = parsed.References
	}
	m.BodyText = parsed.BodyText
	m.BodyHTML = parsed.BodyHTML
	m.Attachments = parsed.Attachments
	m.Headers = parsed.Headers
	m.RawEML = raw
	m.BodyFetched = true
	if parsed.ICSBody != "" {
		m.HasICS = true
		m.ICSBody = parsed.ICSBody
	}
}

const imapUIDChunkSize = 500

func chunkUIDs(uids []imap.UID, size int) []imap.UIDSet {
	if size <= 0 {
		size = imapUIDChunkSize
	}
	valid := make([]imap.UID, 0, len(uids))
	for _, u := range uids {
		if u > 0 {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	var chunks []imap.UIDSet
	for i := 0; i < len(valid); i += size {
		end := min(i+size, len(valid))
		var set imap.UIDSet
		for _, u := range valid[i:end] {
			set.AddNum(u)
		}
		chunks = append(chunks, set)
	}
	return chunks
}

func uidExpungeIMAP(c *imapclient.Client, uidSet imap.UIDSet) error {
	caps := c.Caps()
	if caps != nil && (caps.Has(imap.CapUIDPlus) || caps.Has(imap.CapIMAP4rev2)) {
		if err := c.UIDExpunge(uidSet).Close(); err != nil {
			return fmt.Errorf("IMAP UID EXPUNGE: %w", err)
		}
		return nil
	}
	// Without UIDPLUS/IMAP4rev2, only run EXPUNGE if no unrelated messages are marked \Deleted.
	data, err := c.UIDSearch(&imap.SearchCriteria{Flag: []imap.Flag{imap.FlagDeleted}}, nil).Wait()
	if err != nil {
		return nil
	}
	for _, u := range data.AllUIDs() {
		if !uidSet.Contains(u) {
			// Unrelated \Deleted message exists in this mailbox — never purge it via blind EXPUNGE.
			return nil
		}
	}
	if err := c.Expunge().Close(); err != nil {
		return fmt.Errorf("IMAP EXPUNGE: %w", err)
	}
	return nil
}

func moveMessagesIMAP(c *imapclient.Client, uidSet imap.UIDSet, targetPath string) error {
	caps := c.Caps()
	if caps != nil && (caps.Has(imap.CapMove) || caps.Has(imap.CapIMAP4rev2)) {
		if _, err := c.Move(uidSet, targetPath).Wait(); err != nil {
			return fmt.Errorf("IMAP MOVE nach %q: %w", targetPath, err)
		}
		return nil
	}
	if _, err := c.Copy(uidSet, targetPath).Wait(); err != nil {
		return fmt.Errorf("IMAP COPY nach %q: %w", targetPath, err)
	}
	if err := c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return fmt.Errorf("IMAP STORE \\Deleted: %w", err)
	}
	return uidExpungeIMAP(c, uidSet)
}

func chunkUIDSlices(uids []imap.UID, size int) [][]imap.UID {
	if size <= 0 {
		size = imapUIDChunkSize
	}
	valid := make([]imap.UID, 0, len(uids))
	for _, u := range uids {
		if u > 0 {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	var out [][]imap.UID
	for i := 0; i < len(valid); i += size {
		end := min(i+size, len(valid))
		out = append(out, valid[i:end])
	}
	return out
}

func applyFlagsAndMoveOnClient(c *imapclient.Client, folderPath string, expectedUIDValidity uint32, uids []imap.UID, read, flagged *bool, moveTargetPath string, permanentDelete bool, copyTargetPaths ...string) (map[uint32]bool, error) {
	chunks := chunkUIDSlices(uids, imapUIDChunkSize)
	if len(chunks) == 0 {
		return nil, nil
	}
	sel, err := c.Select(folderPath, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("IMAP SELECT %q: %w", folderPath, err)
	}
	if expectedUIDValidity > 0 && sel.UIDValidity > 0 && sel.UIDValidity != expectedUIDValidity {
		return nil, fmt.Errorf("UIDVALIDITY für %q hat sich geändert (lokal %d, Server %d) – bitte zuerst synchronisieren", folderPath, expectedUIDValidity, sel.UIDValidity)
	}
	succeeded := make(map[uint32]bool, len(uids))
	for _, chunk := range chunks {
		var uidSet imap.UIDSet
		for _, u := range chunk {
			uidSet.AddNum(u)
		}
		if read != nil {
			op := imap.StoreFlagsAdd
			if !*read {
				op = imap.StoreFlagsDel
			}
			if err := c.Store(uidSet, &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
				return succeeded, fmt.Errorf("IMAP STORE \\Seen: %w", err)
			}
		}
		if flagged != nil {
			op := imap.StoreFlagsAdd
			if !*flagged {
				op = imap.StoreFlagsDel
			}
			if err := c.Store(uidSet, &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close(); err != nil {
				return succeeded, fmt.Errorf("IMAP STORE \\Flagged: %w", err)
			}
		}
		for _, copyTargetPath := range copyTargetPaths {
			if copyTargetPath != "" && !strings.EqualFold(copyTargetPath, folderPath) {
				if _, err := c.Copy(uidSet, copyTargetPath).Wait(); err != nil {
					return succeeded, fmt.Errorf("IMAP COPY %q: %w", copyTargetPath, err)
				}
			}
		}
		if moveTargetPath != "" && !strings.EqualFold(moveTargetPath, folderPath) {
			if err := moveMessagesIMAP(c, uidSet, moveTargetPath); err != nil {
				return succeeded, err
			}
		} else if permanentDelete {
			if err := c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
				return succeeded, fmt.Errorf("IMAP STORE \\Deleted: %w", err)
			}
			if err := uidExpungeIMAP(c, uidSet); err != nil {
				return succeeded, err
			}
		}
		for _, u := range chunk {
			succeeded[uint32(u)] = true
		}
	}
	return succeeded, nil
}

func (e *Engine) applyMessageActionIMAP(ctx context.Context, acc config.DirectAccount, m StoredMessage, read, flagged *bool, moveTargetPath string, permanentDelete bool) error {
	if m.UID == 0 {
		return nil
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	var expectedValidity uint32
	if f, ok := e.store.FindFolderByIDOrPath(ctx, acc.ID, m.FolderPath); ok {
		expectedValidity = f.UIDValidity
	}
	_, err = applyFlagsAndMoveOnClient(c, m.FolderPath, expectedValidity, []imap.UID{imap.UID(m.UID)}, read, flagged, moveTargetPath, permanentDelete)
	return err
}

type folderBatchOp struct {
	FolderPath      string
	Messages        []StoredMessage
	Read            *bool
	Flagged         *bool
	CopyTargetPaths []string
	MoveTargetPath  string
	PermanentDelete bool
}

type batchOpResult struct {
	SucceededUIDs map[uint32]bool
	Err           error
}

// applyBatchActionsIMAP executes multiple folder batches for one account over a single IMAP connection.
// Returns per-batch results including partial UID successes when a later chunk fails.
func (e *Engine) applyBatchActionsIMAP(ctx context.Context, acc config.DirectAccount, ops []folderBatchOp) []batchOpResult {
	results := make([]batchOpResult, len(ops))
	if len(ops) == 0 {
		return results
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	validityByPath := map[string]uint32{}
	for i, op := range ops {
		if len(op.Messages) == 0 {
			continue
		}
		uids := make([]imap.UID, 0, len(op.Messages))
		for _, m := range op.Messages {
			if m.UID > 0 {
				uids = append(uids, imap.UID(m.UID))
			}
		}
		if len(uids) == 0 {
			continue
		}
		expVal, ok := validityByPath[op.FolderPath]
		if !ok {
			if f, fok := e.store.FindFolderByIDOrPath(ctx, acc.ID, op.FolderPath); fok {
				expVal = f.UIDValidity
			}
			validityByPath[op.FolderPath] = expVal
		}
		succ, aerr := applyFlagsAndMoveOnClient(c, op.FolderPath, expVal, uids, op.Read, op.Flagged, op.MoveTargetPath, op.PermanentDelete, op.CopyTargetPaths...)
		results[i] = batchOpResult{SucceededUIDs: succ, Err: aerr}
	}
	return results
}

// markFoldersReadIMAP marks all \Unseen messages in the given folders as \Seen over a single IMAP connection.
// Returns the folder paths that succeeded on IMAP, the number of marked messages, and the first error (if any).
func (e *Engine) markFoldersReadIMAP(ctx context.Context, acc config.DirectAccount, folderPaths []string) ([]string, int, error) {
	if len(folderPaths) == 0 {
		return nil, 0, nil
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return nil, 0, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	var succeeded []string
	var firstErr error
	total := 0
	for _, fp := range folderPaths {
		if ctx.Err() != nil {
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			return succeeded, total, firstErr
		}
		if _, err := c.Select(fp, nil).Wait(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("IMAP SELECT %q: %w", fp, err)
			}
			continue
		}
		data, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("IMAP UID SEARCH %q: %w", fp, err)
			}
			continue
		}
		uids := data.AllUIDs()
		folderFailed := false
		for _, uidSet := range chunkUIDs(uids, imapUIDChunkSize) {
			if err := c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("IMAP STORE \\Seen %q: %w", fp, err)
				}
				folderFailed = true
				break
			}
		}
		if !folderFailed {
			succeeded = append(succeeded, fp)
			total += len(uids)
		}
	}
	return succeeded, total, firstErr
}

// searchServerIMAP runs IMAP UID SEARCH TEXT across the specified folders of acc over a single connection,
// returning matching StoredMessages (and fetching/storing any envelopes not yet in SQLite).
func (e *Engine) searchServerIMAP(ctx context.Context, acc config.DirectAccount, folders []StoredFolder, query string, limit int) ([]StoredMessage, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(folders) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return nil, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()

	var results []StoredMessage
	var firstErr error
	for _, f := range folders {
		if ctx.Err() != nil || len(results) >= limit {
			break
		}
		if slices.Contains(f.SpecialUse, "noselect") || slices.Contains(f.SpecialUse, "nonexistent") {
			continue
		}
		sel, err := c.Select(f.Path, nil).Wait()
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("IMAP SELECT %q: %w", f.Path, err)
			}
			continue
		}
		validityChanged := f.UIDValidity > 0 && sel.UIDValidity > 0 && sel.UIDValidity != f.UIDValidity
		// IMAP TEXT searches headers + full body on the server.
		data, err := c.UIDSearch(&imap.SearchCriteria{Text: []string{query}}, nil).Wait()
		if err != nil {
			// Some IMAP servers reject UTF-8/TEXT with BADCHARSET; retry with Subject OR From, then ASCII fallback.
			data, err = c.UIDSearch(&imap.SearchCriteria{
				Or: [][2]imap.SearchCriteria{{
					{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: query}}},
					{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: query}}},
				}},
			}, nil).Wait()
			if err != nil {
				if asciiQ := toASCIIQuery(query); asciiQ != "" {
					data, err = c.UIDSearch(&imap.SearchCriteria{
						Or: [][2]imap.SearchCriteria{{
							{Text: []string{asciiQ}},
							{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: asciiQ}}},
						}},
					}, nil).Wait()
				}
			}
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("IMAP SEARCH %q: %w", f.Path, err)
				}
				continue
			}
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			continue
		}
		// Newest UIDs first.
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		rem := limit - len(results)
		if len(uids) > rem {
			uids = uids[:rem]
		}
		var missing []imap.UID
		for _, u := range uids {
			if validityChanged {
				missing = append(missing, u)
				continue
			}
			id := MessageStoreID(acc.ID, f.Path, uint32(u))
			if sm, gerr := e.store.GetMessage(ctx, id); gerr == nil {
				results = append(results, sm)
			} else {
				missing = append(missing, u)
			}
		}
		if len(missing) > 0 {
			if ferr := e.fetchEnvelopesIMAP(ctx, c, acc, f, missing, 50, func(sm StoredMessage) {
				results = append(results, sm)
			}); ferr != nil && firstErr == nil {
				firstErr = ferr
			}
		}
	}
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// undoMoveIMAP restores messages from ToFolderPath back to FromFolderPath on a single IMAP connection,
// matching them by RFC Message-ID header.
func (e *Engine) undoMoveIMAP(ctx context.Context, acc config.DirectAccount, items []backend.UndoMoveItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return 0, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return 0, err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()

	type pairKey struct{ to, from string }
	type undoEntry struct {
		mid           string
		restoreUnread bool
	}
	grouped := map[pairKey][]undoEntry{}
	var order []pairKey
	for _, it := range items {
		to := strings.TrimSpace(it.ToFolderPath)
		from := strings.TrimSpace(it.FromFolderPath)
		mid := cleanMsgID(it.MessageID)
		if to == "" || from == "" || mid == "" || strings.EqualFold(to, from) {
			continue
		}
		k := pairKey{to: to, from: from}
		if _, ok := grouped[k]; !ok {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], undoEntry{mid: mid, restoreUnread: it.RestoreUnread})
	}

	restored := 0
	var firstErr error
	for _, k := range order {
		if ctx.Err() != nil {
			return restored, ctx.Err()
		}
		if _, err := c.Select(k.to, nil).Wait(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("IMAP SELECT %q: %w", k.to, err)
			}
			continue
		}
		var matchedUIDs []imap.UID
		var restoreUnreadUIDs []imap.UID
		seenUID := map[imap.UID]bool{}
		for _, entry := range grouped[k] {
			mid := entry.mid
			// Match exact angle-bracketed Message-ID first to avoid substring collisions.
			data, err := c.UIDSearch(&imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: "<" + mid + ">"}},
			}, nil).Wait()
			var uids []imap.UID
			if err == nil {
				uids = data.AllUIDs()
			}
			if len(uids) == 0 {
				data2, err2 := c.UIDSearch(&imap.SearchCriteria{
					Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: mid}},
				}, nil).Wait()
				if err2 != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("IMAP UID SEARCH %q: %w", k.to, err2)
					}
					continue
				}
				uids = data2.AllUIDs()
			}
			if len(uids) == 0 {
				continue
			}
			// Pick the newest (highest) UID in the target folder so duplicate copies aren't moved twice.
			newest := uids[0]
			for _, u := range uids[1:] {
				if u > newest {
					newest = u
				}
			}
			if newest > 0 && !seenUID[newest] {
				seenUID[newest] = true
				matchedUIDs = append(matchedUIDs, newest)
				if entry.restoreUnread {
					restoreUnreadUIDs = append(restoreUnreadUIDs, newest)
				}
			}
		}
		if len(matchedUIDs) == 0 {
			continue
		}
		// Restore \Unseen before moving back for messages that were auto-marked read on Trash/Spam move.
		for _, unreadSet := range chunkUIDs(restoreUnreadUIDs, imapUIDChunkSize) {
			_ = c.Store(unreadSet, &imap.StoreFlags{
				Op:     imap.StoreFlagsDel,
				Silent: true,
				Flags:  []imap.Flag{imap.FlagSeen},
			}, nil).Close()
		}
		for _, uidSet := range chunkUIDs(matchedUIDs, imapUIDChunkSize) {
			if err := moveMessagesIMAP(c, uidSet, k.from); err != nil {
				return restored, err
			}
		}
		restored += len(matchedUIDs)
	}
	if restored == 0 && firstErr != nil {
		return 0, firstErr
	}
	return restored, nil
}

// syncSpecificFoldersIMAP quickly re-syncs only the given folder paths of acc on one IMAP connection.
func (e *Engine) syncSpecificFoldersIMAP(ctx context.Context, acc config.DirectAccount, folderPaths []string) error {
	if len(folderPaths) == 0 {
		return nil
	}
	lk := e.accountSyncLock(acc.ID)
	lk.Lock()
	defer lk.Unlock()

	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	seen := map[string]bool{}
	for _, fp := range folderPaths {
		key := strings.ToLower(strings.TrimSpace(fp))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		f, ok := e.store.FindFolderByIDOrPath(ctx, acc.ID, fp)
		if !ok {
			continue
		}
		_, _ = e.syncSingleFolderIMAP(ctx, c, acc, f, false)
	}
	e.emitMailSync(acc.ID)
	return nil
}

func (e *Engine) appendMessageIMAP(ctx context.Context, acc config.DirectAccount, folderPath string, raw []byte, flags []imap.Flag, searchMsgID string) (uint32, error) {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return 0, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	cmd := c.Append(folderPath, int64(len(raw)), &imap.AppendOptions{
		Flags: flags,
		Time:  time.Now(),
	})
	if _, err := cmd.Write(raw); err != nil {
		return 0, fmt.Errorf("IMAP APPEND write: %w", err)
	}
	if err := cmd.Close(); err != nil {
		return 0, fmt.Errorf("IMAP APPEND close: %w", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("IMAP APPEND %q: %w", folderPath, err)
	}
	if data != nil && data.UID > 0 {
		return uint32(data.UID), nil
	}
	// Fallback for IMAP servers without UIDPLUS: resolve the appended UID via Message-ID header.
	mid := cleanMsgID(searchMsgID)
	if mid != "" {
		if _, serr := c.Select(folderPath, nil).Wait(); serr == nil {
			if sdata, serr := c.UIDSearch(&imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: "<" + mid + ">"}},
			}, nil).Wait(); serr == nil {
				uids := sdata.AllUIDs()
				if len(uids) == 0 {
					if sdata2, serr2 := c.UIDSearch(&imap.SearchCriteria{
						Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: mid}},
					}, nil).Wait(); serr2 == nil {
						uids = sdata2.AllUIDs()
					}
				}
				var newest imap.UID
				for _, u := range uids {
					if u > newest {
						newest = u
					}
				}
				if newest > 0 {
					return uint32(newest), nil
				}
			}
		}
	}
	return 0, nil
}

func toASCIIQuery(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r <= 0x7e {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (e *Engine) createFolderIMAP(ctx context.Context, acc config.DirectAccount, folderPath string) error {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	// Respect the server's hierarchy delimiter (e.g. '.' on Dovecot) when user enters '/'.
	if strings.Contains(folderPath, "/") {
		if mboxes, lerr := c.List("", "", nil).Collect(); lerr == nil && len(mboxes) > 0 && mboxes[0].Delim != 0 && mboxes[0].Delim != '/' {
			folderPath = strings.ReplaceAll(folderPath, "/", string(mboxes[0].Delim))
		}
	}
	if err := c.Create(folderPath, nil).Wait(); err != nil {
		return fmt.Errorf("IMAP CREATE %q: %w", folderPath, err)
	}
	return nil
}

func (e *Engine) renameFolderIMAP(ctx context.Context, acc config.DirectAccount, oldPath, newPath string) error {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	if err := c.Rename(oldPath, newPath, nil).Wait(); err != nil {
		return fmt.Errorf("IMAP RENAME %q -> %q: %w", oldPath, newPath, err)
	}
	return nil
}

func (e *Engine) deleteFolderIMAP(ctx context.Context, acc config.DirectAccount, folderPath string) error {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	if err := c.Delete(folderPath).Wait(); err != nil {
		return fmt.Errorf("IMAP DELETE %q: %w", folderPath, err)
	}
	return nil
}

func (e *Engine) emptyFolderIMAP(ctx context.Context, acc config.DirectAccount, folderPath string) (int, error) {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return 0, err
	}
	c, err := dialIMAP(ctx, acc, pass, nil)
	if err != nil {
		return 0, err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Logout().Wait()
		_ = c.Close()
	}()
	sel, err := c.Select(folderPath, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("IMAP SELECT %q: %w", folderPath, err)
	}
	if sel.NumMessages == 0 {
		return 0, nil
	}
	var seqSet imap.SeqSet
	seqSet.AddRange(1, sel.NumMessages)
	if err := c.Store(seqSet, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}, nil).Close(); err != nil {
		return 0, fmt.Errorf("IMAP STORE \\Deleted: %w", err)
	}
	if err := c.Expunge().Close(); err != nil {
		return 0, fmt.Errorf("IMAP EXPUNGE: %w", err)
	}
	return int(sel.NumMessages), nil
}

// isFilterSourceFolder reports whether local PostDive filter rules should inspect messages
// in f: INBOX itself plus active INBOX/* subfolders (excluding terminal folders like */done,
// archive, trash, junk, sent, or drafts).
func isFilterSourceFolder(f StoredFolder) bool {
	if slices.Contains(f.SpecialUse, "noselect") || slices.Contains(f.SpecialUse, "nonexistent") {
		return false
	}
	switch strings.ToLower(f.Type) {
	case "inbox":
		return true
	case "trash", "junk", "sent", "drafts", "archive":
		return false
	}
	norm := strings.ToLower(strings.Trim(strings.ReplaceAll(f.Path, "\\", "/"), "/"))
	if norm == "inbox" {
		return true
	}
	if !strings.HasPrefix(norm, "inbox/") {
		return false
	}
	parts := strings.Split(norm, "/")
	for _, seg := range parts[1:] {
		switch seg {
		case "done", "erledigt", "archive", "archiv", "archives", "trash", "papierkorb", "junk", "spam", "sent", "drafts":
			return false
		}
	}
	return true
}

func filtersNeedFullBody(filters []StoredFilter) bool {
	for _, flt := range filters {
		if !flt.Enabled {
			continue
		}
		for _, cd := range flt.Conditions {
			switch strings.ToLower(strings.TrimSpace(cd.Field)) {
			case "body", "attachment", "attachments":
				return true
			case "header":
				h := strings.ToLower(strings.TrimSpace(cd.HeaderName))
				if h != "message-id" && h != "in-reply-to" && h != "references" && h != "list-id" && h != "list-unsubscribe" {
					return true
				}
			}
		}
	}
	return false
}

// applyLocalFiltersOnNewMessages evaluates enabled PostDive filter rules directly on newly
// arrived INBOX / INBOX/* messages (using the active IMAP connection) and returns remaining messages.
func (e *Engine) applyLocalFiltersOnNewMessages(ctx context.Context, c *imapclient.Client, acc config.DirectAccount, folder StoredFolder, msgs []StoredMessage) []StoredMessage {
	filters, err := e.store.ListFilters(ctx, acc.ID)
	if err != nil || len(filters) == 0 {
		return msgs
	}
	// If any enabled filter inspects body, attachments, or custom headers, fetch bodies for new
	// messages before evaluating rules so those conditions work reliably on incoming mail.
	if filtersNeedFullBody(filters) {
		var needUIDs []imap.UID
		byUID := map[uint32]int{}
		for i, m := range msgs {
			if !m.BodyFetched && m.UID > 0 && (m.Size == 0 || m.Size <= maxFetchRawEMLBytes) {
				needUIDs = append(needUIDs, imap.UID(m.UID))
				byUID[m.UID] = i
			}
		}
		if len(needUIDs) > 0 {
			bodySec := &imap.FetchItemBodySection{Peek: true}
			for _, chunk := range chunkUIDs(needUIDs, 10) {
				if ctx.Err() != nil {
					break
				}
				fetched, ferr := c.Fetch(chunk, &imap.FetchOptions{
					UID:         true,
					BodySection: []*imap.FetchItemBodySection{bodySec},
				}).Collect()
				if ferr != nil {
					break
				}
				for _, fm := range fetched {
					idx, ok := byUID[uint32(fm.UID)]
					if !ok {
						continue
					}
					raw := fm.FindBodySection(bodySec)
					if len(raw) == 0 || len(raw) > maxFetchRawEMLBytes {
						continue
					}
					mergeParsedIntoStored(&msgs[idx], raw)
					_ = e.store.UpsertMessage(ctx, msgs[idx])
				}
			}
		}
	}

	junkFolderPath := ""
	if jf, ok := e.store.FindFolderByType(ctx, acc.ID, "junk"); ok {
		junkFolderPath = jf.Path
	} else {
		junkFolderPath = e.resolveTargetFolderPath(ctx, acc.ID, "Junk")
	}

	var remaining []StoredMessage
	for _, m := range msgs {
		removed := false
		for _, flt := range filters {
			if !flt.Enabled || len(flt.Conditions) == 0 {
				continue
			}
			if !matchFilterAgainstMessage(flt, m) {
				continue
			}
			var uidSet imap.UIDSet
			uidSet.AddNum(imap.UID(m.UID))
			stop := false
			for _, act := range flt.Actions {
				switch act.Type {
				case "markRead":
					_ = c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
					t := true
					_ = e.store.UpdateMessageLocal(ctx, m.ID, &t, nil)
					m.Read = true
				case "addStar":
					_ = c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close()
					t := true
					_ = e.store.UpdateMessageLocal(ctx, m.ID, nil, &t)
					m.Flagged = true
				case "copyToFolder":
					target := e.resolveTargetFolderPath(ctx, acc.ID, act.TargetFolderPath)
					if target != "" && !strings.EqualFold(target, folder.Path) {
						_, _ = c.Copy(uidSet, target).Wait()
					}
				case "junk":
					if junkFolderPath != "" && !strings.EqualFold(junkFolderPath, folder.Path) {
						_ = c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
						if err := moveMessagesIMAP(c, uidSet, junkFolderPath); err == nil {
							_ = e.store.DeleteMessage(ctx, m.ID)
							removed = true
							stop = true
						}
					}
				case "moveToFolder":
					target := e.resolveTargetFolderPath(ctx, acc.ID, act.TargetFolderPath)
					if target != "" && !strings.EqualFold(target, folder.Path) {
						if tf, ok := e.store.FindFolderByType(ctx, acc.ID, "trash"); ok && strings.EqualFold(target, tf.Path) {
							_ = c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
						}
						if err := moveMessagesIMAP(c, uidSet, target); err == nil {
							_ = e.store.DeleteMessage(ctx, m.ID)
							removed = true
							stop = true
						}
					}
				case "delete":
					_ = c.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
					_ = uidExpungeIMAP(c, uidSet)
					_ = e.store.DeleteMessage(ctx, m.ID)
					removed = true
					stop = true
				case "stopExecution":
					stop = true
				}
				if stop || removed {
					break
				}
			}
			if stop || removed {
				break
			}
		}
		if !removed {
			remaining = append(remaining, m)
		}
	}
	return remaining
}

func filterHasHeaderCondition(conds []triage.NativeCondition) bool {
	for _, c := range conds {
		if strings.EqualFold(strings.TrimSpace(c.Field), "header") {
			return true
		}
	}
	return false
}

func matchFilterAgainstMessage(flt StoredFilter, m StoredMessage) bool {
	attNames := make([]string, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if strings.TrimSpace(a.Name) != "" {
			attNames = append(attNames, a.Name)
		}
	}
	hdrs := m.Headers
	if len(m.RawEML) > 0 && filterHasHeaderCondition(flt.Conditions) {
		if allHdrs := ExtractAllHeadersMap(m.RawEML); len(allHdrs) > 0 {
			for k, v := range m.Headers {
				lk := strings.ToLower(strings.TrimSpace(k))
				if lk != "" && allHdrs[lk] == "" {
					allHdrs[lk] = v
				}
			}
			hdrs = allHdrs
		}
	}
	in := triage.MessageInput{
		ID:          m.ID,
		AccountID:   m.AccountID,
		Folder:      m.FolderPath,
		Subject:     m.Subject,
		From:        m.From,
		To:          m.To,
		CC:          m.CC,
		Date:        m.DateISO,
		Headers:     hdrs,
		BodyText:    m.BodyText,
		BodyHTML:    m.BodyHTML,
		Unread:      !m.Read,
		Attachments: attNames,
	}
	rep := triage.EvaluateQuickRulePreview(flt.Conditions, flt.MatchAll, map[string]bool{m.ID: true}, []triage.MessageInput{in}, "")
	return rep.MarkedMatched > 0
}

func parseIMAPFlags(flags []imap.Flag) (read, flagged bool) {
	for _, fl := range flags {
		switch fl {
		case imap.FlagSeen:
			read = true
		case imap.FlagFlagged:
			flagged = true
		}
	}
	return read, flagged
}

func fetchBufferToStoredMessage(accountID string, f StoredFolder, fm *imapclient.FetchMessageBuffer, hdrSec *imap.FetchItemBodySection) StoredMessage {
	read, flagged := parseIMAPFlags(fm.Flags)
	sm := StoredMessage{
		ID:         MessageStoreID(accountID, f.Path, uint32(fm.UID)),
		AccountID:  accountID,
		FolderID:   f.ID,
		FolderPath: f.Path,
		UID:        uint32(fm.UID),
		Read:       read,
		Flagged:    flagged,
		Size:       fm.RFC822Size,
		Headers:    map[string]string{},
	}
	dt := fm.InternalDate
	if fm.Envelope != nil {
		sm.Subject = decodeHeader(fm.Envelope.Subject)
		sm.From = formatIMAPAddresses(fm.Envelope.From)
		sm.To = formatIMAPAddressSlice(fm.Envelope.To)
		sm.CC = formatIMAPAddressSlice(fm.Envelope.Cc)
		sm.MessageID = cleanMsgID(fm.Envelope.MessageID)
		if len(fm.Envelope.InReplyTo) > 0 {
			sm.InReplyTo = cleanMsgID(fm.Envelope.InReplyTo[0])
		}
		if !fm.Envelope.Date.IsZero() {
			dt = fm.Envelope.Date
		}
	}
	if dt.IsZero() {
		dt = time.Now().UTC()
	}
	sm.DateISO = dt.UTC().Format(time.RFC3339)
	sm.DateUnix = dt.Unix()

	if hdrSec != nil {
		if rawHdr := fm.FindBodySection(hdrSec); len(rawHdr) > 0 {
			if msg, err := mail.ReadMessage(bytes.NewReader(append(rawHdr, '\r', '\n'))); err == nil {
				if sm.MessageID == "" {
					sm.MessageID = cleanMsgID(msg.Header.Get("Message-ID"))
				}
				if sm.InReplyTo == "" {
					sm.InReplyTo = cleanMsgID(msg.Header.Get("In-Reply-To"))
				}
				if refs := strings.TrimSpace(msg.Header.Get("References")); refs != "" {
					sm.References = refs
				}
				for _, k := range []string{"List-Id", "List-Unsubscribe"} {
					if v := strings.TrimSpace(msg.Header.Get(k)); v != "" {
						sm.Headers[strings.ToLower(k)] = v
					}
				}
			}
		}
	}
	return sm
}

func formatIMAPAddresses(addrs []imap.Address) string {
	list := formatIMAPAddressSlice(addrs)
	return strings.Join(list, ", ")
}

func formatIMAPAddressSlice(addrs []imap.Address) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		email := a.Addr()
		name := decodeHeader(strings.TrimSpace(a.Name))
		if name != "" && email != "" {
			out = append(out, fmt.Sprintf("%s <%s>", name, email))
		} else if email != "" {
			out = append(out, email)
		} else if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// runIMAPIdleLoop maintains a real-time IMAP IDLE connection on INBOX for instant push sync.
func (e *Engine) runIMAPIdleLoop(ctx context.Context, acc config.DirectAccount) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		// Perform an initial sync (or recovery sync after reconnect).
		res, err := e.SyncAccount(ctx, acc.ID, false)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 60*time.Second)
			continue
		}
		backoff = 2 * time.Second

		// Start IDLE watcher on INBOX.
		if err := e.idleOnce(ctx, acc, res.BackfillPending); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 60*time.Second)
		}
	}
}

// fullSyncEvery is how often all folders are synced while INBOX is watched; backfillSyncEvery
// replaces it while older mails are still being fetched.
const (
	fullSyncEvery     = 15 * time.Minute
	backfillSyncEvery = 30 * time.Second
	pollEvery         = 45 * time.Second
)

func nextFullSync(backfillPending bool) time.Duration {
	if backfillPending {
		return backfillSyncEvery
	}
	return fullSyncEvery
}

func (e *Engine) idleOnce(ctx context.Context, acc config.DirectAccount, backfillPending bool) error {
	pass, err := e.accountPassword(acc.ID)
	if err != nil {
		return err
	}
	notifyCh := make(chan struct{}, 1)
	trigger := func() {
		select {
		case notifyCh <- struct{}{}:
		default:
		}
	}
	c, err := dialIMAP(ctx, acc, pass, &imapclient.UnilateralDataHandler{
		Mailbox: func(_ *imapclient.UnilateralDataMailbox) { trigger() },
		Expunge: func(_ uint32) { trigger() },
		// Flag changes from other devices (read, starred).
		Fetch: func(msg *imapclient.FetchMessageData) {
			_, _ = msg.Collect()
			trigger()
		},
	})
	if err != nil {
		return err
	}
	stopWatch := watchIMAPCtx(ctx, c)
	defer func() {
		stopWatch()
		_ = c.Close()
	}()

	inboxPath := "INBOX"
	if inbox, ok := e.store.FindFolderByType(ctx, acc.ID, "inbox"); ok {
		inboxPath = inbox.Path
	}
	if _, err := c.Select(inboxPath, nil).Wait(); err != nil {
		return err
	}
	e.setAccountState(acc.ID, "idle", "")

	// If server doesn't advertise IDLE, fall back to polling INBOX every 45 s and all folders
	// as often as with IDLE.
	caps := c.Caps()
	if caps != nil && !caps.Has(imap.CapIdle) && !caps.Has(imap.CapIMAP4rev2) {
		_ = c.Close()
		ticker := time.NewTicker(pollEvery)
		defer ticker.Stop()
		lastFull := time.Now()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				full := time.Since(lastFull) >= nextFullSync(backfillPending)
				res, err := e.SyncAccount(ctx, acc.ID, !full)
				if full && err == nil {
					lastFull = time.Now()
					backfillPending = res.BackfillPending
				}
			}
		}
	}

	// The caller has just run a full sync. Count from there, so frequent IDLE wake-ups don't
	// keep pushing the next full sync away.
	lastFull := time.Now()
	for ctx.Err() == nil {
		idleCmd, err := c.Idle()
		if err != nil {
			return err
		}
		doneCh := make(chan error, 1)
		go func() {
			doneCh <- idleCmd.Wait()
		}()

		// Sync all folders regularly even if IDLE stays quiet (sooner while backfilling).
		refreshTimer := time.NewTimer(max(0, nextFullSync(backfillPending)-time.Since(lastFull)))
		select {
		case <-ctx.Done():
			refreshTimer.Stop()
			_ = idleCmd.Close()
			_ = c.Close()
			<-doneCh
			return ctx.Err()
		case <-notifyCh:
			refreshTimer.Stop()
			_ = idleCmd.Close()
			if werr := <-doneCh; werr != nil {
				return werr
			}
			_, _ = e.SyncAccount(ctx, acc.ID, true)
		case <-refreshTimer.C:
			_ = idleCmd.Close()
			if werr := <-doneCh; werr != nil {
				return werr
			}
			if res, err := e.SyncAccount(ctx, acc.ID, false); err == nil {
				backfillPending = res.BackfillPending
				lastFull = time.Now()
			} else {
				// Retry failed full sync after 30s rather than pushing a full sync interval away.
				lastFull = time.Now().Add(-nextFullSync(backfillPending) + 30*time.Second)
			}
		case werr := <-doneCh:
			refreshTimer.Stop()
			return werr
		}
	}
	return ctx.Err()
}
