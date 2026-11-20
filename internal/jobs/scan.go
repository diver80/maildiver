package jobs

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/triage"
)

const (
	// collectTimeout covers reading the inboxes in the engine.
	collectTimeout = 5 * time.Minute
	// classifyTimeout bounds one model call, so a hanging CLI or API cannot block a scan forever.
	classifyTimeout = 50 * time.Second
	// maxScanMessages caps one scan (unread first, then the newest read mails).
	maxScanMessages = 300
)

// ScanReport describes one finished triage scan.
type ScanReport struct {
	ScanID          string `json:"scanId"`
	Reason          string `json:"reason"` // "manual" or "new_mail"
	AccountID       string `json:"accountId,omitempty"`
	StartedAt       string `json:"startedAt"`
	FinishedAt      string `json:"finishedAt"`
	ScannedFolders  int    `json:"scannedFolders"`
	ScannedMessages int    `json:"scannedMessages"`
	Classified      int    `json:"classified"`
	// Encrypted counts mails classified from their headers only: they are never decrypted here.
	Encrypted      int    `json:"encrypted"`
	AIEnabled      bool   `json:"aiEnabled"`
	AICalls        int    `json:"aiCalls"`
	AISkipped      int    `json:"aiSkipped"`
	ClassifyErrors int    `json:"classifyErrors"`
	ClassifyError  string `json:"classifyError,omitempty"`
}

// ScanStatus is what GET /v1/triage/scan returns.
type ScanStatus struct {
	Running bool        `json:"running"`
	Last    *ScanReport `json:"last,omitempty"`
}

func (r *Runner) ScanStatus() ScanStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ScanStatus{Running: r.scanning, Last: r.lastScan}
}

// checkScan returns why a scan cannot run now (nil: it can).
func checkScan(cfg config.Config, accountID string) error {
	if !cfg.Features.Triage {
		return ErrTriageDisabled
	}
	if len(cfg.Mailbox.TriageAccountIDs) == 0 {
		return ErrNoTriageAccounts
	}
	if accountID != "" && !cfg.Mailbox.AccountEnabled(accountID) {
		return ErrAccountNotEnabled
	}
	return nil
}

// StartScan starts a scan in the background and returns its ID. Events: "triage.scan.started",
// then "triage.scan.finished" (data: ScanReport) or "triage.scan.failed" ({scanId, error}).
func (r *Runner) StartScan(accountID, reason string) (string, error) {
	if err := checkScan(r.config(), accountID); err != nil {
		return "", err
	}
	if !r.connected() {
		return "", ErrDisconnected
	}
	r.mu.Lock()
	if r.scanning {
		r.mu.Unlock()
		return "", ErrBusy
	}
	r.scanning = true
	r.mu.Unlock()

	scanID := fmt.Sprintf("scan-%d", time.Now().UnixMilli())
	r.publish("triage.scan.started", map[string]any{"scanId": scanID, "reason": reason, "accountId": accountID})
	go func() {
		rep, err := r.Scan(r.ctx, scanID, accountID, reason)
		r.mu.Lock()
		r.scanning = false
		if err == nil {
			r.lastScan = &rep
		}
		r.mu.Unlock()
		if err != nil {
			r.publish("triage.scan.failed", map[string]any{"scanId": scanID, "error": err.Error()})
			return
		}
		r.publish("triage.scan.finished", rep)
	}()
	return scanID, nil
}

// NewMail schedules a scan of all triage inboxes after new mail arrived, when the settings ask
// for it. A burst of notifications gives one scan; a scan already running is retried later.
func (r *Runner) NewMail() {
	cfg := r.config()
	if !cfg.Mailbox.ScanOnNewMail || checkScan(cfg, "") != nil {
		return
	}
	r.scheduleNewMailScan(newMailDelay)
}

func (r *Runner) scheduleNewMailScan(delay time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.newMailTimer != nil {
		r.newMailTimer.Stop()
	}
	r.newMailTimer = time.AfterFunc(delay, func() {
		if r.ctx.Err() != nil {
			return
		}
		if _, err := r.StartScan("", "new_mail"); err == ErrBusy {
			r.scheduleNewMailScan(10 * time.Second)
		}
	})
}

type collected struct {
	Messages       []triage.MessageInput `json:"messages"`
	ScannedFolders int                   `json:"scannedFolders"`
}

// Scan runs one scan synchronously: reads the triage inboxes (never decrypting), classifies the
// mails and stores the results.
func (r *Runner) Scan(ctx context.Context, scanID, accountID, reason string) (ScanReport, error) {
	cfg := r.config()
	if err := checkScan(cfg, accountID); err != nil {
		return ScanReport{}, err
	}
	rep := ScanReport{ScanID: scanID, Reason: reason, AccountID: accountID, StartedAt: nowISO(), AIEnabled: cfg.AI.ClassifyWithAI}
	var col collected
	params := map[string]any{"accountId": accountID, "limit": cfg.Mailbox.ScanLimit, "days": cfg.Mailbox.ScanDays, "max": maxScanMessages}
	if err := r.call(ctx, collectTimeout, "host.triage.collect", params, &col); err != nil {
		return rep, err
	}
	// The settings may have changed while the engine read the inboxes.
	cfg = r.config()
	if err := checkScan(cfg, accountID); err != nil {
		return rep, err
	}
	rep.ScannedFolders = col.ScannedFolders
	rep.ScannedMessages = len(col.Messages)

	if _, err := r.store.RetainAccounts(cfg.Mailbox); err != nil {
		return rep, err
	}
	prev, _ := r.store.Load()
	results, stats := r.classifyMessages(ctx, cfg, col.Messages, prev)
	rep.Classified = len(results)
	rep.AICalls, rep.AISkipped = stats.aiCalls, stats.aiSkipped
	rep.ClassifyErrors, rep.ClassifyError = stats.errors, stats.firstError
	isRead := make(map[string]bool, len(col.Messages))
	for _, m := range col.Messages {
		isRead[m.ID] = !m.Unread
		if m.Encrypted {
			rep.Encrypted++
		}
	}
	if _, err := r.store.SaveScanBatch(results, isRead); err != nil {
		return rep, err
	}
	rep.FinishedAt = nowISO()
	return rep, nil
}

type classifyStats struct {
	aiCalls, aiSkipped, errors int
	firstError                 string
}

// classifyMessages classifies mails of enabled accounts: Stage-0 rules first, earlier model
// results reused by Message-ID, then the model within the scan's call budget (unread first).
func (r *Runner) classifyMessages(ctx context.Context, cfg config.Config, msgs []triage.MessageInput, prev triage.State) ([]triage.Classification, classifyStats) {
	cfg.AI.Language = cfg.Language
	allowed := make([]triage.MessageInput, 0, len(msgs))
	for _, m := range msgs {
		// Defense in depth: never classify (or send to a model) mails of accounts not enabled.
		if cfg.Mailbox.AccountEnabled(m.AccountID) {
			allowed = append(allowed, m)
		}
	}
	byThread := map[string]triage.Classification{}
	for _, p := range prev.Classifications {
		if p.ThreadID != "" {
			byThread[p.ThreadID] = p
		}
	}
	sort.SliceStable(allowed, func(i, j int) bool { return allowed[i].Unread && !allowed[j].Unread })

	var st classifyStats
	results := make([]triage.Classification, 0, len(allowed))
	for _, m := range allowed {
		base, matched := triage.EvaluateStage0(m, cfg.AI.SkipNewsletters)
		if matched {
			results = append(results, base)
			continue
		}
		// Reuse only by Message-ID: a numeric ID can belong to another mail after a resync.
		if p, ok := byThread[m.ThreadID]; ok && m.ThreadID != "" && p.Stage == "stage1-llm" && p.Subject == m.Subject {
			p.MessageID, p.AccountID, p.Folder, p.FolderID = m.ID, m.AccountID, m.Folder, m.FolderID
			results = append(results, p)
			continue
		}
		if !cfg.AI.ClassifyWithAI {
			results = append(results, ai.ClassifyLocalWithBase(cfg.AI, m, base, "sum_ai_off", "Lokale Schätzung (KI-Klassifikation ausgeschaltet)"))
			continue
		}
		if st.aiCalls >= cfg.AI.MaxAICallsPerScan {
			st.aiSkipped++
			results = append(results, ai.ClassifyLocalWithBase(cfg.AI, m, base, "sum_ai_limit", "Lokale Schätzung (KI-Limit pro Scan erreicht)"))
			continue
		}
		st.aiCalls++
		c, cancel := context.WithTimeout(ctx, classifyTimeout)
		res, err := r.classify(c, cfg.AI, m, base)
		cancel()
		if err != nil {
			// The mail still gets a local heuristic result; the report says why the model failed.
			st.errors++
			if st.firstError == "" {
				st.firstError = err.Error()
			}
		}
		results = append(results, res)
	}
	return results, st
}
