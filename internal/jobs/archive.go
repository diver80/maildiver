package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diver80/postdive/internal/archive"
	"github.com/diver80/postdive/internal/config"
)

const (
	candidatesTimeout = 2 * time.Minute
	rawTimeout        = 2 * time.Minute // one message, up to 45 MB
	finishTimeout     = time.Minute
	maxReportItems    = 100
)

// Archive modes. Everything except "simulate" writes verified .eml files first; the last one
// then changes the mailbox, but only for mails whose backup was verified.
const (
	ModeSimulate        = "simulate"
	ModeExportOnly      = "export_only"
	ModeExportAndDelete = "export_and_delete" // then delete at the provider
)

// ArchiveRequest selects the mails of one triage account for archive & cleanup. The archive
// directory always comes from config.json (mailbox.archiveDir), never from a request.
type ArchiveRequest struct {
	AccountID         string `json:"accountId"`
	Scope             string `json:"scope,omitempty"` // "inbox" (default), "folder", "all"
	FolderPath        string `json:"folderPath,omitempty"`
	IncludeSubfolders *bool  `json:"includeSubfolders,omitempty"`
	OlderThanDays     *int   `json:"olderThanDays,omitempty"`  // default 365
	OnlyRead          *bool  `json:"onlyRead,omitempty"`       // default true
	ExcludeStarred    *bool  `json:"excludeStarred,omitempty"` // default true
	MaxMessages       int    `json:"maxMessages,omitempty"`    // default 200, at most 1000
	Mode              string `json:"mode,omitempty"`
	SkipTrash         bool   `json:"skipTrash,omitempty"` // export_and_delete: delete for good instead of moving to the trash
}

// ArchiveCandidate is one mail the add-on found for a request.
type ArchiveCandidate struct {
	MessageID       string `json:"messageId"`
	HeaderMessageID string `json:"headerMessageId"`
	AccountID       string `json:"accountId"`
	AccountName     string `json:"accountName"`
	FolderID        string `json:"folderId"`
	FolderPath      string `json:"folderPath"`
	Subject         string `json:"subject"`
	From            string `json:"from"`
	Date            string `json:"date"`
	Size            int64  `json:"size"`
	Read            bool   `json:"read"`
	Flagged         bool   `json:"flagged"`
}

type candidateList struct {
	Candidates     []ArchiveCandidate `json:"candidates"`
	TotalMatched   int                `json:"totalMatched"`
	TotalBytesAll  int64              `json:"totalBytesAll"`
	ScannedFolders int                `json:"scannedFolders"`
	Truncated      bool               `json:"truncated"`
}

// ArchivePreview is what a run with the same request would cover.
type ArchivePreview struct {
	Candidates     []ArchiveCandidate `json:"candidates"` // at most 100
	TotalCount     int                `json:"totalCount"`
	TotalMatched   int                `json:"totalMatched"`
	TotalBytes     int64              `json:"totalBytes"`
	TotalBytesAll  int64              `json:"totalBytesAll"`
	Truncated      bool               `json:"truncated"`
	ScannedFolders int                `json:"scannedFolders"`
	ReadOnly       bool               `json:"readOnly"`
	ArchiveDir     string             `json:"archiveDir"`
}

type ArchiveItem struct {
	Subject          string `json:"subject"`
	From             string `json:"from,omitempty"`
	Date             string `json:"date,omitempty"`
	Size             int64  `json:"size,omitempty"`
	TargetPath       string `json:"targetPath,omitempty"`
	SkippedDuplicate bool   `json:"skippedDuplicate,omitempty"`
}

type ArchiveFailure struct {
	Subject string `json:"subject"`
	From    string `json:"from,omitempty"`
	Error   string `json:"error"`
}

// ArchiveReport describes a finished run.
type ArchiveReport struct {
	RunID             string           `json:"runId"`
	Mode              string           `json:"mode"`
	DryRun            bool             `json:"dryRun"`
	StartedAt         string           `json:"startedAt"`
	FinishedAt        string           `json:"finishedAt"`
	ScannedFolders    int              `json:"scannedFolders"`
	TotalCandidates   int              `json:"totalCandidates"`
	Exported          int              `json:"exported"`
	SkippedDuplicates int              `json:"skippedDuplicates"`
	Deleted           int              `json:"deleted"`
	BytesExported     int64            `json:"bytesExported"`
	BytesEstimated    int64            `json:"bytesEstimated"`
	Failed            []ArchiveFailure `json:"failed"`
	Items             []ArchiveItem    `json:"items"` // at most 100
}

type ArchiveStatus struct {
	Running bool           `json:"running"`
	Last    *ArchiveReport `json:"last,omitempty"`
}

func (r *Runner) ArchiveStatus() ArchiveStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ArchiveStatus{Running: r.archiving, Last: r.lastArchive}
}

// ArchiveStats reports the configured archive directory.
func (r *Runner) ArchiveStats() (archive.ArchiveStats, error) {
	return archive.Stats(r.config().Mailbox.ArchiveDir)
}

func validMode(mode string) bool {
	switch mode {
	case ModeSimulate, ModeExportOnly, ModeExportAndDelete:
		return true
	}
	return false
}

// checkArchive returns why the request cannot run (nil: it can).
func checkArchive(cfg config.Config, req ArchiveRequest) error {
	if !cfg.Mailbox.TriageIncludes(req.AccountID) {
		if !cfg.Mailbox.HasTriageAccounts() {
			return ErrNoTriageAccounts
		}
		return ErrAccountNotEnabled
	}
	if !validMode(req.Mode) {
		return fmt.Errorf("invalid_argument: unknown archive mode %q", req.Mode)
	}
	if req.Mode == ModeExportAndDelete && cfg.Mailbox.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

func (r *Runner) candidates(ctx context.Context, req ArchiveRequest) (candidateList, error) {
	var list candidateList
	err := r.call(ctx, candidatesTimeout, "host.archive.candidates", req, &list)
	return list, err
}

// PreviewArchive lists what a run with req would cover, without touching anything.
func (r *Runner) PreviewArchive(ctx context.Context, req ArchiveRequest) (ArchivePreview, error) {
	cfg := r.config()
	req.Mode = ModeSimulate
	if err := checkArchive(cfg, req); err != nil {
		return ArchivePreview{}, err
	}
	list, err := r.candidates(ctx, req)
	if err != nil {
		return ArchivePreview{}, err
	}
	p := ArchivePreview{
		TotalCount:     len(list.Candidates),
		TotalMatched:   list.TotalMatched,
		TotalBytesAll:  list.TotalBytesAll,
		Truncated:      list.Truncated,
		ScannedFolders: list.ScannedFolders,
		ReadOnly:       cfg.Mailbox.ReadOnly,
		ArchiveDir:     cfg.Mailbox.ArchiveDir,
	}
	for _, c := range list.Candidates {
		p.TotalBytes += c.Size
	}
	p.Candidates = list.Candidates
	if len(p.Candidates) > maxReportItems {
		p.Candidates = p.Candidates[:maxReportItems]
	}
	return p, nil
}

// StartArchive starts a run in the background. Events: "archive.progress" ({runId, done, total}),
// then "archive.finished" (ArchiveReport) or "archive.failed" ({runId, error}).
func (r *Runner) StartArchive(req ArchiveRequest) (string, error) {
	if req.Mode == "" {
		req.Mode = ModeSimulate
	}
	if err := checkArchive(r.config(), req); err != nil {
		return "", err
	}
	if !r.connected() {
		return "", ErrDisconnected
	}
	r.mu.Lock()
	if r.archiving {
		r.mu.Unlock()
		return "", ErrBusy
	}
	r.archiving = true
	r.mu.Unlock()

	runID := fmt.Sprintf("archive-%d", time.Now().UnixMilli())
	go func() {
		rep, err := r.RunArchive(r.ctx, runID, req)
		r.mu.Lock()
		r.archiving = false
		if err == nil {
			r.lastArchive = &rep
		}
		r.mu.Unlock()
		if err != nil {
			r.publish("archive.failed", map[string]any{"runId": runID, "error": err.Error()})
			return
		}
		r.publish("archive.finished", rep)
	}()
	return runID, nil
}

type rawMessage struct {
	MessageID  string `json:"messageId"` // the live numeric ID
	Base64     string `json:"base64"`
	ByteLength int64  `json:"byteLength"`
}

// RunArchive runs one archive request synchronously. A mail is only moved or deleted after its
// .eml file was written and read back with the same SHA-256.
func (r *Runner) RunArchive(ctx context.Context, runID string, req ArchiveRequest) (ArchiveReport, error) {
	cfg := r.config()
	if err := checkArchive(cfg, req); err != nil {
		return ArchiveReport{}, err
	}
	list, err := r.candidates(ctx, req)
	if err != nil {
		return ArchiveReport{}, err
	}
	rep := ArchiveReport{
		RunID:           runID,
		Mode:            req.Mode,
		DryRun:          req.Mode == ModeSimulate,
		StartedAt:       nowISO(),
		ScannedFolders:  list.ScannedFolders,
		TotalCandidates: len(list.Candidates),
		Failed:          []ArchiveFailure{},
		Items:           []ArchiveItem{},
	}
	for _, c := range list.Candidates {
		rep.BytesEstimated += c.Size
	}
	total := len(list.Candidates)
	for i, c := range list.Candidates {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		item, err := r.archiveOne(ctx, cfg, req, c, &rep)
		if err != nil {
			rep.Failed = append(rep.Failed, ArchiveFailure{Subject: c.Subject, From: c.From, Error: err.Error()})
		} else if len(rep.Items) < maxReportItems {
			rep.Items = append(rep.Items, item)
		}
		r.publish("archive.progress", map[string]any{"runId": runID, "done": i + 1, "total": total})
	}
	rep.FinishedAt = nowISO()
	return rep, nil
}

func (r *Runner) archiveOne(ctx context.Context, cfg config.Config, req ArchiveRequest, c ArchiveCandidate, rep *ArchiveReport) (ArchiveItem, error) {
	exp := archive.ExportRequest{
		ArchiveDir:      cfg.Mailbox.ArchiveDir,
		AccountID:       c.AccountID,
		AccountName:     c.AccountName,
		FolderPath:      c.FolderPath,
		DateISO:         c.Date,
		Subject:         c.Subject,
		MessageID:       c.MessageID,
		HeaderMessageID: c.HeaderMessageID,
		DryRun:          req.Mode == ModeSimulate,
	}
	item := ArchiveItem{Subject: c.Subject, From: c.From, Date: c.Date, Size: c.Size}
	if req.Mode != ModeSimulate {
		// The add-on checks that the mail is still where it was found and returns it as stored
		// (encrypted mails stay encrypted).
		var raw rawMessage
		if err := r.call(ctx, rawTimeout, "host.archive.raw", c, &raw); err != nil {
			return item, err
		}
		exp.MessageID = raw.MessageID
		exp.RawEMLBase64 = raw.Base64
	}
	res, err := archive.Export(exp)
	if err != nil {
		return item, err
	}
	item.TargetPath = res.TargetPath
	item.SkippedDuplicate = res.SkippedDuplicate
	if req.Mode != ModeSimulate && !res.Verified {
		return item, errors.New("archive_verification_failed")
	}
	if res.SkippedDuplicate {
		rep.SkippedDuplicates++
	} else {
		rep.Exported++
	}
	rep.BytesExported += res.BytesWritten

	if req.Mode == ModeExportAndDelete {
		finish := map[string]any{"candidate": c, "messageId": exp.MessageID, "action": "delete", "skipTrash": req.SkipTrash}
		if err := r.call(ctx, finishTimeout, "host.archive.finish", finish, nil); err != nil {
			return item, fmt.Errorf("backup verified, mailbox unchanged: %w", err)
		}
		rep.Deleted++
	}
	return item, nil
}
