package nativeui

import (
	"context"
	"encoding/csv"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/postdive/internal/mailengine"
)

const (
	maxTextPreviewBytes = 128 * 1024
	maxCSVPreviewRows   = 200
)

type attachmentPreviewState struct {
	open        bool
	loading     bool
	seq         uint64
	errMsg      string
	msgID       string
	partName    string
	filename    string
	contentType string
	sizeBytes   int
	imgSrc      ui.ImageSource
	imgW        int
	imgH        int
	textContent string
	csvMarkdown string
	csvRowsCut  bool
	mdOpts      markdown.Options
	truncated   bool
	parsedEML   *mailengine.ParsedMessage
	isPDF       bool
}

func truncateUTF8Bytes(b []byte, maxBytes int) ([]byte, bool) {
	if len(b) <= maxBytes {
		return b, false
	}
	cut := b[:maxBytes]
	// Walk back at most 3 bytes if we sliced in the middle of a multi-byte UTF-8 rune.
	for len(cut) > 0 && !utf8.Valid(cut) && len(b[:maxBytes])-len(cut) < 4 {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

func (p *attachmentPreviewState) resetMedia() {
	p.imgSrc = nil
	p.imgW = 0
	p.imgH = 0
	p.textContent = ""
	p.csvMarkdown = ""
	p.csvRowsCut = false
	p.parsedEML = nil
	p.isPDF = false
	p.truncated = false
}

// formatDelimitedTableMarkdown parses CSV/TSV text (auto-detecting tab, semicolon, or comma)
// into a GitHub-flavored Markdown table capped at maxRows rows.
func formatDelimitedTableMarkdown(raw string, ext string, maxRows int) (md string, truncated bool, ok bool) {
	cleaned := strings.TrimPrefix(raw, "\uFEFF")
	if strings.TrimSpace(cleaned) == "" {
		return "", false, false
	}
	delim := ','
	if ext == ".tsv" {
		delim = '\t'
	} else {
		firstLine := cleaned
		if nl := strings.IndexAny(firstLine, "\r\n"); nl >= 0 {
			firstLine = firstLine[:nl]
		}
		tabs := strings.Count(firstLine, "\t")
		semis := strings.Count(firstLine, ";")
		commas := strings.Count(firstLine, ",")
		switch {
		case tabs > semis && tabs > commas && tabs > 0:
			delim = '\t'
		case semis > commas && semis > 0:
			delim = ';'
		}
	}
	r := csv.NewReader(strings.NewReader(cleaned))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var rows [][]string
	maxCols := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, false
		}
		if len(rows) >= maxRows {
			truncated = true
			break
		}
		rows = append(rows, rec)
		if len(rec) > maxCols {
			maxCols = len(rec)
		}
	}
	if len(rows) == 0 || maxCols < 2 || maxCols > 32 {
		return "", false, false
	}
	esc := func(s string) string {
		s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", " ")
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.ReplaceAll(s, "|", "\\|")
		return s
	}
	var b strings.Builder
	writeRow := func(rec []string) {
		b.WriteByte('|')
		for i := 0; i < maxCols; i++ {
			b.WriteByte(' ')
			if i < len(rec) {
				b.WriteString(esc(rec[i]))
			}
			b.WriteString(" |")
		}
		b.WriteByte('\n')
	}
	writeRow(rows[0])
	b.WriteByte('|')
	for i := 0; i < maxCols; i++ {
		b.WriteString(" --- |")
	}
	b.WriteByte('\n')
	for _, rec := range rows[1:] {
		writeRow(rec)
	}
	return b.String(), truncated, true
}

// OpenAttachmentPreview loads attachment bytes in memory and displays images, plain/code files,
// or nested .eml messages inside an in-app modal (with Download & Open button).
func (a *App) OpenAttachmentPreview(ctx context.Context, msgID, partName, filename string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	nextSeq := a.attPreview.seq + 1
	a.attPreview = attachmentPreviewState{
		open:     true,
		loading:  true,
		seq:      nextSeq,
		msgID:    msgID,
		partName: partName,
		filename: filename,
	}
	go func(reqSeq uint64) {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		data, err := dc.ReadAttachmentPreview(c, msgID, partName, filename)
		var imgSrc ui.ImageSource
		var imgW, imgH int
		var txt string
		var csvMD string
		var csvCut bool
		var truncated bool
		var isPDF bool
		var decodeErr error

		if err == nil {
			ext := strings.ToLower(filepath.Ext(data.Name))
			ct := strings.ToLower(data.ContentType)
			switch {
			case strings.HasPrefix(ct, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp":
				if src, derr := ui.DecodeImage(data.Data); derr == nil {
					imgSrc = src
				} else {
					decodeErr = derr
				}
				if cfgImg, _, cerr := image.DecodeConfig(strings.NewReader(string(data.Data))); cerr == nil {
					imgW, imgH = cfgImg.Width, cfgImg.Height
				}
			case data.ParsedEML != nil:
				// Rendered via parsedEML fields in the modal.
			case ext == ".pdf" || strings.Contains(ct, "pdf"):
				isPDF = true
			default:
				raw, wasCut := truncateUTF8Bytes(data.Data, maxTextPreviewBytes)
				truncated = wasCut
				if utf8.Valid(raw) {
					txt = string(raw)
				} else {
					txt = strings.ToValidUTF8(string(raw), "")
				}
				if ext == ".csv" || ext == ".tsv" || strings.Contains(ct, "csv") || strings.Contains(ct, "tab-separated-values") {
					if md, rowsCut, ok := formatDelimitedTableMarkdown(txt, ext, maxCSVPreviewRows); ok {
						csvMD = md
						csvCut = rowsCut
					}
				}
			}
		}

		a.emitUpdate(func() {
			if !a.attPreview.open || a.attPreview.seq != reqSeq {
				return
			}
			a.attPreview.loading = false
			if err != nil {
				a.attPreview.errMsg = err.Error()
				return
			}
			a.attPreview.filename = data.Name
			a.attPreview.contentType = data.ContentType
			a.attPreview.sizeBytes = len(data.Data)
			if decodeErr != nil {
				a.attPreview.errMsg = a.trf(
					"Bildformat konnte nicht dekodiert werden (%v) — bitte über „Speichern & Öffnen“ anzeigen.",
					"Could not decode image format (%v) — please use “Save & Open” to view it.",
					decodeErr,
				)
				return
			}
			a.attPreview.imgSrc = imgSrc
			a.attPreview.imgW = imgW
			a.attPreview.imgH = imgH
			a.attPreview.textContent = txt
			a.attPreview.csvMarkdown = csvMD
			a.attPreview.csvRowsCut = csvCut
			a.attPreview.truncated = truncated
			a.attPreview.parsedEML = data.ParsedEML
			a.attPreview.isPDF = isPDF
		})
	}(nextSeq)
}

func (a *App) renderAttachmentPreviewModal(c *ui.Context) {
	p := &a.attPreview
	if !p.open {
		if p.imgSrc != nil || p.parsedEML != nil || p.textContent != "" {
			p.resetMedia()
		}
		return
	}
	t := c.Theme()
	a.modal(c, &p.open, func() {
		ui.Column(c).Width(a.modalW(720)).MaxHeight(a.modalH(c, 720)).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
				ui.Icon(c, iconPaperclip).Size(17, 17).TextColor(t.Accent).Shrink(0)
				title := p.filename
				if title == "" {
					title = a.tr("Anhang-Vorschau", "Attachment Preview")
				}
				ui.Text(c, title).FontSize(15).Bold().Grow(1).MinWidth(120).SingleLine()
				if p.sizeBytes > 0 {
					pillBadge(c, fmt.Sprintf("%d KB", max(1, p.sizeBytes/1024)), t.Background, t.TextMuted)
				}
				if p.msgID != "" {
					dlLabel := a.tr("Speichern & Öffnen", "Save & Open")
					if a.isPhone() {
						dlLabel = a.tr("Öffnen", "Open")
					}
					if iconLabelBtn(c.Key("attpv-dl"), iconDownload, dlLabel, true, "").Clicked() {
						a.DownloadAndOpenAttachment(context.Background(), p.msgID, p.partName, p.filename)
					}
					if a.onSaveCopy != nil && iconLabelBtn(c.Key("attpv-save"), iconDownload, a.tr("Speichern…", "Save…"), false, "").Clicked() {
						a.SaveAttachmentCopy(context.Background(), p.msgID, p.partName, p.filename)
					}
				}
				if ui.Button(c.Key("attpv-x"), "✕").Clicked() {
					p.open = false
					p.resetMedia()
				}
			})

			switch {
			case p.loading:
				ui.Column(c).FillWidth().Height(220).Center().Gap(8).Children(func() {
					ui.Spinner(c)
					ui.Text(c, a.tr("Lade Anhang-Vorschau…", "Loading attachment preview…")).FontSize(12.5).TextColor(t.TextMuted)
				})
			case p.errMsg != "":
				ui.Column(c).FillWidth().Padding(14).Radius(8).Background(withAlpha(t.Danger, 0.12)).Border(1, withAlpha(t.Danger, 0.4)).Children(func() {
					ui.Text(c, p.errMsg).FontSize(12.5).TextColor(t.Danger)
				})
			case p.imgSrc != nil:
				if p.imgW > 0 && p.imgH > 0 {
					ui.Text(c, fmt.Sprintf("%d × %d px · %s", p.imgW, p.imgH, p.contentType)).FontSize(11.5).TextColor(t.TextMuted)
				}
				ui.Scroll(c.Key("attpv-img-sc")).FillWidth().MaxHeight(520).Children(func() {
					ui.Column(c).FillWidth().Center().Padding(8).Radius(8).Background(t.Background).Children(func() {
						ui.Image(c, p.imgSrc).MaxWidth(min(660, a.modalW(720)-52)).MaxHeight(480).Radius(6)
					})
				})
			case p.parsedEML != nil:
				eml := p.parsedEML
				ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(4).Children(func() {
					subj := strings.TrimSpace(eml.Subject)
					if subj == "" {
						subj = a.tr("(Kein Betreff)", "(No Subject)")
					}
					ui.Text(c, subj).FontSize(14).Bold().Selectable()
					if eml.From != "" {
						ui.Text(c, a.tr("Von: ", "From: ")+eml.From).FontSize(11.5).TextColor(t.TextMuted).Selectable()
					}
					if len(eml.To) > 0 {
						ui.Text(c, a.tr("An: ", "To: ")+strings.Join(eml.To, ", ")).FontSize(11.5).TextColor(t.TextMuted).Selectable()
					}
					if !eml.Date.IsZero() {
						ui.Text(c, formatDateTimeLang(eml.Date.Format(time.RFC3339), a.uiLang())).FontSize(11.5).TextColor(t.TextMuted)
					}
					if len(eml.Attachments) > 0 {
						var names []string
						for _, na := range eml.Attachments {
							names = append(names, na.Name)
						}
						ui.Text(c, a.tr("Enthaltene Anhänge: ", "Nested attachments: ")+strings.Join(names, ", ")).FontSize(11.5).TextColor(t.Accent)
					}
				})
				bodyTxt := strings.TrimSpace(eml.BodyText)
				if bodyTxt == "" && strings.TrimSpace(eml.BodyHTML) != "" {
					bodyTxt, _ = htmlMailMarkdown(eml.BodyHTML)
				}
				ui.Scroll(c.Key("attpv-eml-sc")).FillWidth().MaxHeight(380).Children(func() {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Children(func() {
						ui.Text(c, bodyTxt).FontSize(12.5).Selectable()
					})
				})
			case p.isPDF:
				ui.Column(c).FillWidth().Padding(18).Radius(8).Background(t.Background).Border(1, t.Border).Gap(10).Center().Children(func() {
					ui.Text(c, a.tr("PDF-Dokument bereit", "PDF document ready")).FontSize(14).Bold()
					ui.Text(c, a.tr(
						"Klicke auf „Speichern & Öffnen“, um das PDF im System-Viewer (z. B. macOS Vorschau) anzuzeigen.",
						"Click “Save & Open” to view this PDF in your system viewer (e.g. Preview).",
					)).FontSize(12).TextColor(t.TextMuted)
					if ui.PrimaryButton(c.Key("attpv-pdf-open"), a.tr("In System-Viewer öffnen", "Open in System Viewer")).Clicked() {
						a.DownloadAndOpenAttachment(context.Background(), p.msgID, p.partName, p.filename)
						p.open = false
					}
				})
			case p.csvMarkdown != "":
				ui.Scroll(c.Key("attpv-csv-sc")).FillWidth().MaxHeight(500).Children(func() {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
						markdown.View(c, p.csvMarkdown, &p.mdOpts)
						if p.csvRowsCut || p.truncated {
							ui.Text(c, a.trf("… Tabellen-Vorschau auf %d Zeilen / 128 KB begrenzt — für vollständige Datei „Speichern & Öffnen“ nutzen.",
								"… table preview capped at %d rows / 128 KB — use “Save & Open” for the full file.", maxCSVPreviewRows)).FontSize(11).TextColor(t.Warning)
						}
					})
				})
			default:
				ui.Scroll(c.Key("attpv-txt-sc")).FillWidth().MaxHeight(500).Children(func() {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
						ui.Text(c, p.textContent).Font("monospace").FontSize(12).Selectable()
						if p.truncated {
							ui.Text(c, a.tr("… Vorschau nach 128 KB abgeschnitten — für vollständige Datei „Speichern & Öffnen“ nutzen.",
								"… preview truncated at 128 KB — use “Save & Open” for full file.")).FontSize(11).TextColor(t.Warning)
						}
					})
				})
			}
		})
	})
	if !p.open && (p.imgSrc != nil || p.parsedEML != nil || p.textContent != "") {
		p.resetMedia()
	}
}
