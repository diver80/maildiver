package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/omathunder/internal/archive"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/jobs"
)

// Triage scans and archive runs run in the host (internal/jobs); the app starts them through the
// bridge and follows them through events.

// archiveUI is the state of the "Archive & cleanup" dialog.
type archiveUI struct {
	open           bool
	account        string // label in accountLabels
	accountLabels  []string
	accountByLabel map[string]string
	scope          int // 0: inbox, 1: all folders
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

var archiveModes = []string{jobs.ModeSimulate, jobs.ModeExportOnly, jobs.ModeExportAndLocal, jobs.ModeExportAndDelete}

func (a *App) archiveModeLabels() []string {
	return []string{
		a.tr("Simulieren", "Simulate"),
		a.tr("Nur Backup", "Backup only"),
		a.tr("Backup + Lokale Ordner", "Backup + Local Folders"),
		a.tr("Backup + beim Anbieter löschen", "Backup + delete at provider"),
	}
}

// StartTriageScan starts a scan of all triage inboxes in the host (UI thread).
func (a *App) StartTriageScan(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		a.toast(a.tr("Thunderbird ist nicht verbunden", "Thunderbird is not connected"), "error")
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
			if rep.Moved > 0 || rep.Deleted > 0 {
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
		modeLabel: a.archiveModeLabels()[0]}
	for _, acc := range a.accounts {
		if !a.cfg.Mailbox.AccountEnabled(acc.ID) {
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
	scope := "inbox"
	if a.arch.scope == 1 {
		scope = "all"
	}
	onlyRead, excludeStarred := a.arch.onlyRead, a.arch.excludeStarred
	return jobs.ArchiveRequest{
		AccountID:      a.arch.accountByLabel[a.arch.account],
		Scope:          scope,
		OlderThanDays:  &days,
		OnlyRead:       &onlyRead,
		ExcludeStarred: &excludeStarred,
		Mode:           archiveModes[min(max(a.arch.mode, 0), len(archiveModes)-1)],
		SkipTrash:      a.arch.skipTrash && a.arch.mode == 3,
	}
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
		c, cancel := context.WithTimeout(ctx, bridgeclient.ArchivePreviewTimeout)
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
	if rep.Moved > 0 {
		msg += a.trf(", %d in Lokale Ordner verschoben", ", %d moved to Local Folders", rep.Moved)
	}
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
	ui.Modal(c, &ar.open, func() {
		ui.Column(c).Width(600).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
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
				ui.Text(c, a.tr("Kein Konto für OmaThunder freigegeben. Gib Konten in den Einstellungen des Thunderbird-Add-ons unter „Konten & Rechte“ frei.",
					"No account is enabled for OmaThunder. Enable accounts in the Thunderbird add-on's settings under “Accounts & permissions”.")).FontSize(12).TextColor(t.Warning)
				return
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Select(c.Key("ar-acc"), &ar.account, ar.accountLabels).Grow(1)
				ui.Segmented(c.Key("ar-scope"), &ar.scope, a.tr("Posteingang", "Inbox"), a.tr("Alle Ordner", "All folders"))
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Älter als (Tage)", "Older than (days)")).FontSize(12)
				ui.TextInput(c.Key("ar-days"), &ar.olderThanDays).Width(80)
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
			if ar.mode != 3 {
				ar.confirm = false
			}
			if ar.mode == 3 {
				ui.Checkbox(c.Key("ar-skiptrash"), &ar.skipTrash, a.tr("Endgültig löschen statt in den Papierkorb", "Delete for good instead of moving to the trash"))
			}
			if ar.mode >= 2 && a.readOnly {
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
