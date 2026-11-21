package nativeui

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/mailengine"
)

const maxTextPreviewBytes = 128 * 1024

type attachmentPreviewState struct {
	open        bool
	loading     bool
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
	truncated   bool
	parsedEML   *mailengine.ParsedMessage
	isPDF       bool
}

// OpenAttachmentPreview loads attachment bytes in memory and displays images, plain/code files,
// or nested .eml messages inside an in-app modal (with Download & Open button).
func (a *App) OpenAttachmentPreview(ctx context.Context, msgID, partName, filename string) {
	dc := a.opts.Direct
	if dc == nil || msgID == "" {
		return
	}
	a.attPreview = attachmentPreviewState{
		open:     true,
		loading:  true,
		msgID:    msgID,
		partName: partName,
		filename: filename,
	}
	go func() {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		data, err := dc.ReadAttachmentPreview(c, msgID, partName, filename)
		var imgSrc ui.ImageSource
		var imgW, imgH int
		var txt string
		var truncated bool
		var isPDF bool

		if err == nil {
			ext := strings.ToLower(filepath.Ext(data.Name))
			ct := strings.ToLower(data.ContentType)
			switch {
			case strings.HasPrefix(ct, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp":
				if src, derr := ui.DecodeImage(data.Data); derr == nil {
					imgSrc = src
				}
				if cfgImg, _, cerr := image.DecodeConfig(strings.NewReader(string(data.Data))); cerr == nil {
					imgW, imgH = cfgImg.Width, cfgImg.Height
				}
			case data.ParsedEML != nil:
				// Rendered via parsedEML fields in the modal.
			case ext == ".pdf" || strings.Contains(ct, "pdf"):
				isPDF = true
			default:
				raw := data.Data
				if len(raw) > maxTextPreviewBytes {
					raw = raw[:maxTextPreviewBytes]
					truncated = true
				}
				if utf8.Valid(raw) {
					txt = string(raw)
				} else {
					txt = string([]rune(string(raw)))
				}
			}
		}

		a.emitUpdate(func() {
			if !a.attPreview.open || a.attPreview.msgID != msgID || a.attPreview.partName != partName {
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
			a.attPreview.imgSrc = imgSrc
			a.attPreview.imgW = imgW
			a.attPreview.imgH = imgH
			a.attPreview.textContent = txt
			a.attPreview.truncated = truncated
			a.attPreview.parsedEML = data.ParsedEML
			a.attPreview.isPDF = isPDF
		})
	}()
}

func (a *App) renderAttachmentPreviewModal(c *ui.Context) {
	p := &a.attPreview
	if !p.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &p.open, func() {
		ui.Column(c).Width(a.modalW(720)).MaxHeight(720).Padding(18).Radius(12).Background(t.Surface).Border(1, t.Border).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconPaperclip).Size(17, 17).TextColor(t.Accent)
				title := p.filename
				if title == "" {
					title = a.tr("Anhang-Vorschau", "Attachment Preview")
				}
				ui.Text(c, title).FontSize(15).Bold().Grow(1).MinWidth(0).SingleLine()
				if p.sizeBytes > 0 {
					pillBadge(c, fmt.Sprintf("%d KB", max(1, p.sizeBytes/1024)), t.Background, t.TextMuted)
				}
				if p.msgID != "" {
					if iconLabelBtn(c.Key("attpv-dl"), iconDownload, a.tr("Speichern & Öffnen", "Save & Open"), true, "").Clicked() {
						a.DownloadAndOpenAttachment(context.Background(), p.msgID, p.partName, p.filename)
					}
				}
				if ui.Button(c.Key("attpv-x"), "✕").Clicked() {
					p.open = false
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
						ui.Image(c, p.imgSrc).MaxWidth(a.modalW(660)).MaxHeight(480).Radius(6)
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
			default:
				ui.Scroll(c.Key("attpv-txt-sc")).FillWidth().MaxHeight(500).Children(func() {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(t.Background).Border(1, t.Border).Gap(6).Children(func() {
						ui.Text(c, p.textContent).FontSize(12).Selectable()
						if p.truncated {
							ui.Text(c, a.tr("… Vorschau nach 128 KB abgeschnitten — für vollständige Datei „Speichern & Öffnen“ nutzen.",
								"… preview truncated at 128 KB — use “Save & Open” for full file.")).FontSize(11).TextColor(t.Warning)
						}
					})
				})
			}
		})
	})
}
