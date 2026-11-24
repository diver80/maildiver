package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/archive"
	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/jobs"
)

// Triage scans and archive runs run in the host (internal/jobs); the app starts them through the
// bridge and follows them through events.

// archiveUI is the state of the "Archive & cleanup" dialog.
type archiveUI struct {
	open           bool
	account        string // label in accountLabels
	accountLabels  []string
	accountByLabel map[string]string
	scope          int    // 0: inbox, 1: one folder (folder), 2: all folders
	folder         string // folder path for scope 1
	includeSub     bool
	maxMessages    string
	olderThanDays  string
	onlyRead       bool
	excludeStarred bool
	mode           int    // index into archiveModes, derived from modeLabel
	modeLabel      string // the Select's value
	skipTrash      bool
	confirm        bool

	stats       *archive.ArchiveStats
	preview     *jobs.ArchivePreview
	previewBusy bool
	running     bool
	runID       string
	done, total int
	report      *jobs.ArchiveReport
	err         string
}

// archiveModes are the dialog's choices, in the order of archiveModeLabels.
var archiveModes = []string{jobs.ModeSimulate, jobs.ModeExportOnly, jobs.ModeExportAndDelete}

func (a *App) archiveModeLabels() []string {
	return []string{
		a.tr("Simulieren", "Simulate"),
		a.tr("Nur Backup (.eml)", "Backup only (.eml)"),
		a.tr("Backup + beim Anbieter löschen", "Backup + delete at provider"),
	}
}

func (a *App) selectedArchiveMode() string {
	return archiveModes[min(max(a.arch.mode, 0), len(archiveModes)-1)]
}

// StartTriageScan starts a scan of all triage inboxes in the host (UI thread).
func (a *App) StartTriageScan(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		a.toast(a.tr("Mail-Dienst ist nicht verbunden", "Mail engine is not connected"), "error")
		return
	}
	a.scanRunning = true
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := bc.StartScan(c, "")
		a.emitUpdate(func() {
			if err != nil {
				a.scanRunning = false
				a.toast(a.tr("Scan nicht gestartet: ", "Scan not started: ")+a.errText(err), "error")
				return
			}
			a.toast(a.tr("Triage-Scan läuft…", "Triage scan running…"), "success")
		})
	}()
}

// scanReportText summarizes a finished scan for a toast.
func (a *App) scanReportText(rep jobs.ScanReport) string {
	msg := a.trf("Triage-Scan fertig: %d Mails eingeordnet", "Triage scan done: %d emails sorted", rep.Classified)
	if rep.Encrypted > 0 {
		msg += a.trf(" (%d verschlüsselt, nur nach Absender und Betreff)", " (%d encrypted, by sender and subject only)", rep.Encrypted)
	}
	if rep.ClassifyErrors > 0 && rep.ClassifyError != "" {
		msg += a.tr(" – KI-Fehler: ", " – AI error: ") + rep.ClassifyError
	}
	return msg
}

// handleJobEvent reacts to scan and archive events; reports whether name was one. Any goroutine.
func (a *App) handleJobEvent(ctx context.Context, name string, payload json.RawMessage) bool {
	switch name {
	case "triage.scan.started":
		a.emitUpdate(func() { a.scanRunning = true })
	case "triage.scan.finished":
		var rep jobs.ScanReport
		_ = json.Unmarshal(payload, &rep)
		a.emitUpdate(func() {
			a.scanRunning = false
			// Scans on new mail run quietly; a manual scan reports.
			if rep.Reason != "new_mail" {
				a.toast(a.scanReportText(rep), "success")
			}
			a.startRefresh(ctx)
		})
	case "triage.scan.failed":
		var f struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &f)
		a.emitUpdate(func() {
			a.scanRunning = false
			a.toast(a.tr("Triage-Scan fehlgeschlagen: ", "Triage scan failed: ")+a.errText(fmt.Errorf("%s", f.Error)), "error")
		})
	case "archive.progress":
		var p struct {
			RunID string `json:"runId"`
			Done  int    `json:"done"`
			Total int    `json:"total"`
		}
		_ = json.Unmarshal(payload, &p)
		a.emitUpdate(func() {
			if p.RunID == a.arch.runID {
				a.arch.done, a.arch.total = p.Done, p.Total
			}
		})
	case "archive.finished":
		var rep jobs.ArchiveReport
		_ = json.Unmarshal(payload, &rep)
		a.emitUpdate(func() {
			if rep.RunID != a.arch.runID {
				return
			}
			a.arch.running = false
			a.arch.report = &rep
			a.toast(a.archiveReportText(rep), "success")
			a.loadArchiveStats(ctx)
			if rep.Deleted > 0 {
				a.msgReload.trigger(func() { a.LoadMessages(ctx) })
			}
		})
	case "archive.failed":
		var f struct {
			RunID string `json:"runId"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &f)
		a.emitUpdate(func() {
			if f.RunID != a.arch.runID {
				return
			}
			a.arch.running = false
			a.arch.err = a.errText(fmt.Errorf("%s", f.Error))
		})
	default:
		return false
	}
	return true
}

// OpenArchiveDialog opens "Archive & cleanup" with safe defaults: simulate, read and unflagged
// mails older than a year (UI thread).
func (a *App) OpenArchiveDialog(ctx context.Context) {
	a.arch = archiveUI{open: true, olderThanDays: "365", onlyRead: true, excludeStarred: true, accountByLabel: map[string]string{},
		includeSub: true, maxMessages: strconv.Itoa(defaultArchiveMax),
		modeLabel: a.archiveModeLabels()[0]}
	for _, acc := range a.accounts {
		if !a.cfg.Mailbox.TriageIncludes(acc.ID) {
			continue
		}
		label := acc.Name
		if label == "" {
			label = acc.ID
		}
		a.arch.accountLabels = append(a.arch.accountLabels, label)
		a.arch.accountByLabel[label] = acc.ID
	}
	if len(a.arch.accountLabels) > 0 {
		a.arch.account = a.arch.accountLabels[0]
		for _, l := range a.arch.accountLabels {
			if a.arch.accountByLabel[l] == a.selectedAccount {
				a.arch.account = l
			}
		}
	}
	a.loadArchiveStats(ctx)
}

func (a *App) loadArchiveStats(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		st, err := bc.ArchiveStats(c)
		if err != nil {
			return
		}
		a.emitUpdate(func() { a.arch.stats = &st })
	}()
}

// archiveRequest builds the request from the dialog (UI thread).
func (a *App) archiveRequest() jobs.ArchiveRequest {
	days, err := strconv.Atoi(strings.TrimSpace(a.arch.olderThanDays))
	if err != nil || days < 0 {
		days = 365
	}
	maxMsgs, ok := parseBounded(a.arch.maxMessages, 1, 1000)
	if !ok {
		maxMsgs = defaultArchiveMax
	}
	onlyRead, excludeStarred := a.arch.onlyRead, a.arch.excludeStarred
	mode := a.selectedArchiveMode()
	req := jobs.ArchiveRequest{
		AccountID:      a.arch.accountByLabel[a.arch.account],
		Scope:          "inbox",
		OlderThanDays:  &days,
		OnlyRead:       &onlyRead,
		ExcludeStarred: &excludeStarred,
		MaxMessages:    maxMsgs,
		Mode:           mode,
		SkipTrash:      a.arch.skipTrash && mode == jobs.ModeExportAndDelete,
	}
	switch a.arch.scope {
	case 1:
		incSub := a.arch.includeSub
		req.Scope = "folder"
		req.FolderPath = a.arch.folder
		req.IncludeSubfolders = &incSub
	case 2:
		req.Scope = "all"
	}
	return req
}

// defaultArchiveMax matches the engine's default number of mails per archive run.
const defaultArchiveMax = 200

// archiveFolderChoices lists the folder paths of the dialog's account that an archive run can
// cover (the engine skips trash, junk and drafts anyway).
func (a *App) archiveFolderChoices() []string {
	accID := a.arch.accountByLabel[a.arch.account]
	var out []string
	for _, f := range a.folders {
		if f.AccountID != accID || f.Type == "trash" || f.Type == "junk" || f.Type == "drafts" {
			continue
		}
		out = append(out, strings.Trim(f.Path, "/"))
	}
	return out
}

// PreviewArchive lists what the dialog's settings would cover (UI thread).
func (a *App) PreviewArchive(ctx context.Context) {
	bc, err := a.client()
	if err != nil || a.arch.previewBusy {
		return
	}
	req := a.archiveRequest()
	a.arch.previewBusy = true
	a.arch.err = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, backend.ArchivePreviewTimeout)
		defer cancel()
		p, err := bc.ArchivePreview(c, req)
		a.emitUpdate(func() {
			a.arch.previewBusy = false
			if err != nil {
				a.arch.err = a.errText(err)
				return
			}
			a.arch.preview = &p
		})
	}()
}

// StartArchiveRun starts the run; deleting at the provider needs a second click (UI thread).
func (a *App) StartArchiveRun(ctx context.Context) {
	bc, err := a.client()
	if err != nil || a.arch.running {
		return
	}
	req := a.archiveRequest()
	if req.Mode == jobs.ModeExportAndDelete && !a.arch.confirm {
		a.arch.confirm = true
		return
	}
	a.arch.confirm = false
	a.arch.running = true
	a.arch.report = nil
	a.arch.err = ""
	a.arch.done, a.arch.total = 0, 0
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		id, err := bc.StartArchive(c, req)
		a.emitUpdate(func() {
			if err != nil {
				a.arch.running = false
				a.arch.err = a.errText(err)
				return
			}
			a.arch.runID = id
		})
	}()
}

func (a *App) archiveReportText(rep jobs.ArchiveReport) string {
	if rep.DryRun {
		return a.trf("Simulation: %d Mails würden gesichert (%d schon im Archiv)", "Simulation: %d emails would be backed up (%d already archived)", rep.Exported, rep.SkippedDuplicates)
	}
	msg := a.trf("Archiv: %d gesichert, %d schon vorhanden", "Archive: %d backed up, %d already there", rep.Exported, rep.SkippedDuplicates)
	if rep.Deleted > 0 {
		msg += a.trf(", %d beim Anbieter gelöscht", ", %d deleted at the provider", rep.Deleted)
	}
	if len(rep.Failed) > 0 {
		msg += a.trf(", %d fehlgeschlagen", ", %d failed", len(rep.Failed))
	}
	return msg
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (a *App) renderArchiveModal(c *ui.Context) {
	t := c.Theme()
	ar := &a.arch
	a.modal(c, &ar.open, func() {
		ui.Column(c).Width(a.modalW(600)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Archiv & Aufräumen", "Archive & Cleanup")).FontSize(15).Bold().Grow(1)
				if ui.Button(c.Key("ar-x"), "✕").Clicked() {
					ar.open = false
				}
			})
			ui.Text(c, a.tr("Sichert alte Mails als geprüfte .eml-Dateien (SHA-256) und räumt danach auf Wunsch das Postfach auf. Verschlüsselte Mails bleiben verschlüsselt.",
				"Backs up old emails as verified .eml files (SHA-256) and then, if you want, cleans up the mailbox. Encrypted emails stay encrypted.")).FontSize(11.5).TextColor(t.TextMuted)
			dir := a.cfg.Mailbox.ArchiveDir
			stats := a.tr("Archiv-Ordner: ", "Archive folder: ") + dir
			if ar.stats != nil && ar.stats.Exists {
				stats += a.trf(" · %d Dateien, %s", " · %d files, %s", ar.stats.FileCount, humanBytes(ar.stats.TotalBytes))
			}
			ui.Text(c, stats).FontSize(11.5).TextColor(t.TextMuted)

			if len(ar.accountLabels) == 0 {
				ui.Text(c, a.tr("Kein Konto eingerichtet. Füge zuerst unter „Konten & Regeln“ ein IMAP-Konto hinzu.",
					"No account configured. Add an IMAP account under “Accounts & Rules” first.")).FontSize(12).TextColor(t.Warning)
				return
			}
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				accSel := ui.Select(c.Key("ar-acc"), &ar.account, ar.accountLabels).Grow(1).MinWidth(160)
				if accSel.Changed() {
					ar.folder = ""
				}
				ui.Segmented(c.Key("ar-scope"), &ar.scope, a.tr("Posteingang", "Inbox"), a.tr("Ordner", "Folder"), a.tr("Alle Ordner", "All folders"))
			})
			if ar.scope == 1 {
				folders := a.archiveFolderChoices()
				if len(folders) == 0 {
					ui.Text(c, a.tr("Für dieses Konto sind noch keine Ordner geladen.", "No folders loaded for this account yet.")).FontSize(11.5).TextColor(t.Warning)
				} else {
					if !slices.Contains(folders, ar.folder) {
						ar.folder = folders[0]
					}
					ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
						ui.Select(c.Key("ar-folder"), &ar.folder, folders).Grow(1).MinWidth(160)
						ui.Checkbox(c.Key("ar-sub"), &ar.includeSub, a.tr("Unterordner einbeziehen", "Include subfolders"))
					})
				}
			}
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Älter als (Tage)", "Older than (days)")).FontSize(12)
				ui.TextInput(c.Key("ar-days"), &ar.olderThanDays).Width(80)
				ui.Text(c, a.tr("Höchstens Mails", "At most emails")).FontSize(12)
				ui.TextInput(c.Key("ar-max"), &ar.maxMessages).Placeholder(strconv.Itoa(defaultArchiveMax)).Width(80)
			})
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("ar-read"), &ar.onlyRead, a.tr("Nur gelesene", "Read only"))
				ui.Checkbox(c.Key("ar-star"), &ar.excludeStarred, a.tr("Markierte auslassen", "Skip starred"))
			})
			labels := a.archiveModeLabels()
			ui.Select(c.Key("ar-mode"), &ar.modeLabel, labels).FillWidth()
			ar.mode = 0
			for i, l := range labels {
				if l == ar.modeLabel {
					ar.mode = i
				}
			}
			curMode := a.selectedArchiveMode()
			if curMode != jobs.ModeExportAndDelete {
				ar.confirm = false
			}
			if curMode == jobs.ModeExportAndDelete {
				ui.Checkbox(c.Key("ar-skiptrash"), &ar.skipTrash, a.tr("Endgültig löschen statt in den Papierkorb", "Delete for good instead of moving to the trash"))
			}
			if curMode == jobs.ModeExportAndDelete && a.readOnly {
				ui.Text(c, a.tr("Read-Only ist aktiv: Nur Simulieren und Backup sind möglich.", "Read-only is on: only simulate and backup are possible.")).FontSize(11.5).TextColor(t.Warning)
			}

			if p := ar.preview; p != nil {
				line := a.trf("%d Mails (%s) in %d Ordnern", "%d emails (%s) in %d folders", p.TotalCount, humanBytes(p.TotalBytes), p.ScannedFolders)
				if p.Truncated {
					line += a.trf(" – von %d passenden", " – of %d matching", p.TotalMatched)
				}
				ui.Text(c, line).FontSize(12.5).Bold()
				ui.Scroll(c.Key("ar-scroll")).FillWidth().MaxHeight(180).Children(func() {
					for i, cand := range p.Candidates {
						ui.Text(c.Key(fmt.Sprintf("ar-c-%d", i)), fmt.Sprintf("%s · %s · %s", shortDate(cand.Date), truncateRunes(cand.From, 32), truncateRunes(cand.Subject, 60))).FontSize(11.5).TextColor(t.TextMuted)
					}
				})
			}
			if ar.running {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, a.trf("Läuft: %d von %d", "Running: %d of %d", ar.done, ar.total)).FontSize(12)
				})
			}
			if rep := ar.report; rep != nil {
				ui.Text(c, a.archiveReportText(*rep)).FontSize(12.5).TextColor(t.Success)
				for i, f := range rep.Failed {
					if i >= 5 {
						break
					}
					ui.Text(c.Key(fmt.Sprintf("ar-f-%d", i)), truncateRunes(f.Subject, 50)+": "+f.Error).FontSize(11.5).TextColor(t.Danger)
				}
			}
			if ar.err != "" {
				ui.Text(c, ar.err).FontSize(12).TextColor(t.Danger)
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				previewLbl := a.tr("Vorschau", "Preview")
				if ar.previewBusy {
					previewLbl = a.tr("Suche…", "Searching…")
				}
				if ui.Button(c.Key("ar-preview"), previewLbl).Clicked() && !ar.previewBusy {
					a.PreviewArchive(context.Background())
				}
				runLbl := a.tr("Starten", "Start")
				if ar.confirm {
					runLbl = a.tr("Wirklich löschen?", "Really delete?")
				}
				if ar.running {
					runLbl = a.tr("Läuft…", "Running…")
				}
				if ui.PrimaryButton(c.Key("ar-run"), runLbl).Clicked() && !ar.running {
					a.StartArchiveRun(context.Background())
				}
			})
		})
	})
}

// shortDate is "2024-03-04" of an ISO date.
func shortDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}
