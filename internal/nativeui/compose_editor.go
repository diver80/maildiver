package nativeui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/mailengine"
)

// ComposeThemePalette passes the active app colors into the desktop WYSIWYG HTML compose surface.
type ComposeThemePalette struct {
	Bg      string
	Surface string
	Text    string
	Muted   string
	Border  string
	Accent  string
	Dark    bool
}

// ComposeEditorHost abstracts the embedded HTML WYSIWYG editor (mygo.WebView on desktop).
type ComposeEditorHost interface {
	Widget() ui.NativeWebView
	Ensure() error
	LoadContent(initialHTML string, pal ComposeThemePalette, lang string) error
	UpdateSignature(sigHTML string)
	InsertHTML(fragHTML string)
	ReadContent(ctx context.Context) (htmlBody string, plainText string, ok bool)
	Focus()
	Destroy()
}

// SetComposeEditorHost wires the desktop HTML WYSIWYG editor into App (nil in headless tests/mobile).
func (a *App) SetComposeEditorHost(h ComposeEditorHost) {
	a.composeEditor = h
}

func (a *App) composeUsingWYSIWYG() bool {
	return !a.isPhone() && a.composeEditor != nil
}

func hexOfColor(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func (a *App) composeThemePalette() ComposeThemePalette {
	// Light/dark heuristic uses current theme colors when initialized.
	pal := ComposeThemePalette{
		Bg:      "#151821",
		Surface: "#1e222d",
		Text:    "#e6edf3",
		Muted:   "#9ca3af",
		Border:  "#2f3545",
		Accent:  "#f59e0b",
		Dark:    true,
	}
	if a.uiTheme != nil && a.uiTheme.theme != nil {
		th := a.uiTheme.theme
		pal.Bg = hexOfColor(th.Background)
		pal.Surface = hexOfColor(th.Surface)
		pal.Text = hexOfColor(th.Text)
		pal.Muted = hexOfColor(th.TextMuted)
		pal.Border = hexOfColor(th.Border)
		pal.Accent = hexOfColor(th.Accent)
		lum := int(th.Background.R)*299 + int(th.Background.G)*587 + int(th.Background.B)*114
		pal.Dark = lum < 128000
	}
	return pal
}

// composeSignatureBlockHTML converts a plain-text signature into an email-safe Option-D HTML block.
func composeSignatureBlockHTML(sigPlain string) string {
	sigPlain = strings.TrimSpace(strings.ReplaceAll(sigPlain, "\r\n", "\n"))
	if sigPlain == "" {
		return ""
	}
	styled := mailengine.BuildStyledEmailHTML("\n\n-- \n" + sigPlain)
	if styled == "" {
		return `<div data-mdv-sig="1" style="margin-top:20px;padding-left:12px;border-left:3px solid #9d1140;font-size:12.5px;color:#57606a;">` +
			strings.ReplaceAll(html.EscapeString(sigPlain), "\n", "<br>") + `</div>`
	}
	// Extract inner signature div produced by BuildStyledEmailHTML so the editor owns one clean block.
	if idx := strings.Index(styled, `<div style="margin-top:20px;`); idx >= 0 {
		rest := styled[idx:]
		rest = strings.TrimSuffix(rest, `</body></html>`)
		return strings.Replace(rest, `<div style="margin-top:20px;`, `<div data-mdv-sig="1" style="margin-top:20px;`, 1)
	}
	return `<div data-mdv-sig="1" style="margin-top:20px;padding-left:12px;border-left:3px solid #9d1140;font-size:12.5px;color:#57606a;">` +
		strings.ReplaceAll(html.EscapeString(sigPlain), "\n", "<br>") + `</div>`
}

func plainBlockToEditableHTML(plain string) string {
	norm := strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(plain, "\r\n", "\n")), "\r", "\n")
	if norm == "" {
		return "<p><br></p>"
	}
	paras := strings.Split(norm, "\n\n")
	var b strings.Builder
	for _, p := range paras {
		esc := html.EscapeString(p)
		esc = strings.ReplaceAll(esc, "\n", "<br>")
		b.WriteString(`<p style="margin:0 0 10px 0;">` + esc + `</p>`)
	}
	return b.String()
}

// buildComposeEditorInitialHTML builds editable HTML for new mails, drafts, replies, and forwards.
func buildComposeEditorInitialHTML(topPlain, sigPlain, fwdHeader, fwdHTML string, inlineImgs map[string]mailengine.InlineImage, replyQuotePlain string) string {
	var b strings.Builder
	b.WriteString(`<div data-mdv-user="1">`)
	b.WriteString(plainBlockToEditableHTML(topPlain))
	b.WriteString(`</div>`)

	if sigHTML := composeSignatureBlockHTML(sigPlain); sigHTML != "" {
		b.WriteString(sigHTML)
	}

	if strings.TrimSpace(fwdHTML) != "" {
		if len(fwdHTML) > maxForwardOrigHTML {
			fwdHTML = fwdHTML[:maxForwardOrigHTML]
		}
		b.WriteString(`<div data-mdv-fwd="1" style="margin-top:18px;">`)
		if strings.TrimSpace(fwdHeader) != "" {
			b.WriteString(`<div style="border-top:1px solid #d0d7de;padding-top:10px;margin-bottom:10px;color:#57606a;font-size:12px;">`)
			b.WriteString(strings.ReplaceAll(html.EscapeString(strings.TrimSpace(fwdHeader)), "\n", "<br>"))
			b.WriteString(`</div>`)
		}
		b.WriteString(SanitizeEmailHTML(fwdHTML, inlineImgs, false))
		b.WriteString(`</div>`)
	} else if strings.TrimSpace(replyQuotePlain) != "" {
		norm := strings.TrimSpace(strings.ReplaceAll(replyQuotePlain, "\r\n", "\n"))
		b.WriteString(`<blockquote data-mdv-quote="1" style="margin:18px 0 0 0;padding:0 0 0 12px;border-left:2px solid #d0d7de;color:#57606a;font-size:13px;">`)
		b.WriteString(strings.ReplaceAll(html.EscapeString(norm), "\n", "<br>"))
		b.WriteString(`</blockquote>`)
	}
	return b.String()
}

// NotifyComposeDirty schedules a debounced read of the HTML editor so composeSnapshot & Draft guards stay fresh.
func (a *App) NotifyComposeDirty() {
	a.composeEditorDirty = true
	a.composeSyncSeq++
	seq := a.composeSyncSeq
	ed := a.composeEditor
	if ed == nil || ed.Widget() == nil {
		return
	}
	go func() {
		time.Sleep(220 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		htmlOut, textOut, ok := ed.ReadContent(ctx)
		if !ok {
			return
		}
		a.emitUpdate(func() {
			if a.composeSyncSeq != seq {
				return
			}
			a.composeBodyHTML = htmlOut
			a.composeBody = textOut
		})
	}()
}

// NotifyComposeSend triggers sending (with existing 2-step confirmation) when Cmd/Ctrl+Enter is pressed in the HTML editor.
func (a *App) NotifyComposeSend() {
	if a.readOnly || a.composeBusy {
		return
	}
	if !a.composeConfirm {
		a.composeConfirm = true
		a.toast(a.tr("Nochmal ⌘↩ drücken zum Senden", "Press ⌘↩ again to send"), "info")
		return
	}
	a.SendCompose(context.Background(), a.buildComposeReq(false))
}

// NotifyComposePasteFiles attaches copied files from Finder/Explorer when pasted inside the compose editor.
func (a *App) NotifyComposePasteFiles() {
	a.pasteClipboardIntoCompose(false)
}

// pasteClipboardIntoCompose reads files or images from the system clipboard and attaches them (or inserts image inline).
func (a *App) pasteClipboardIntoCompose(inlineImage bool) {
	if a.readOnly {
		return
	}
	go func() {
		files, err := mygo.Clipboard.ReadFiles()
		if err == nil && len(files) > 0 {
			a.emitUpdate(func() {
				added := 0
				for _, p := range files {
					before := len(a.composeAttachments)
					a.addComposeAttachment(p)
					if len(a.composeAttachments) > before {
						added++
					}
				}
				if added > 0 {
					a.toast(a.trf("%d Datei(en) aus Zwischenablage angehängt", "%d file(s) attached from clipboard", added), "success")
				}
			})
			return
		}
		imgBytes := mygo.Clipboard.ReadImage()
		if len(imgBytes) == 0 {
			a.emitUpdate(func() {
				a.toast(a.tr("Keine Dateien oder Bilder in der Zwischenablage", "No files or images found in clipboard"), "info")
			})
			return
		}
		if inlineImage && a.composeUsingWYSIWYG() && a.composeEditor.Widget() != nil {
			b64 := base64.StdEncoding.EncodeToString(imgBytes)
			frag := `<img src="data:image/png;base64,` + b64 + `" style="max-width:100%;height:auto;border-radius:6px;margin:4px 0;">`
			a.emitUpdate(func() {
				a.composeEditor.InsertHTML(frag)
				a.NotifyComposeDirty()
				a.toast(a.tr("Bild aus Zwischenablage eingefügt", "Pasted image from clipboard"), "success")
			})
			return
		}
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("maildiver-clipboard-%d.png", time.Now().UnixNano()))
		if werr := os.WriteFile(tmpFile, imgBytes, 0o600); werr != nil {
			a.emitUpdate(func() {
				a.toast(a.tr("Zwischenablage-Bild nicht speicherbar: ", "Could not save clipboard image: ")+werr.Error(), "error")
			})
			return
		}
		a.emitUpdate(func() {
			a.addComposeAttachment(tmpFile)
			a.toast(a.tr("Bild aus Zwischenablage angehängt", "Image attached from clipboard"), "success")
		})
	}()
}

// composeEditorDocumentHTML renders the self-contained dark/light-aware WYSIWYG HTML compose UI.
func composeEditorDocumentHTML(initialBodyHTML string, pal ComposeThemePalette, lang string) string {
	de := lang != "en"
	lbl := func(d, e string) string {
		if de {
			return d
		}
		return e
	}
	initJSON, _ := json.Marshal(initialBodyHTML)

	editorFg := "#1f2328"
	editorBg := pal.Surface
	if pal.Dark {
		editorFg = pal.Text
	} else {
		editorBg = "#ffffff"
	}

	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
  :root {
    --bg: %s;
    --surface: %s;
    --text: %s;
    --muted: %s;
    --border: %s;
    --accent: %s;
    --ed-bg: %s;
    --ed-fg: %s;
  }
  * { box-sizing: border-box; }
  html, body {
    margin: 0; padding: 0; height: 100%%; width: 100%%;
    background: var(--bg); color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    display: flex; flex-direction: column; overflow: hidden;
  }
  .tb {
    display: flex; flex-wrap: wrap; align-items: center; gap: 4px;
    padding: 6px 8px; background: var(--surface);
    border-bottom: 1px solid var(--border); user-select: none;
  }
  .tb-sub {
    display: none; flex-wrap: wrap; align-items: center; gap: 5px;
    padding: 5px 8px; background: var(--bg);
    border-bottom: 1px solid var(--border); font-size: 12px;
  }
  .tb-sub.open { display: flex; }
  .sep { width: 1px; height: 18px; background: var(--border); margin: 0 2px; }
  button.btn, select.sel {
    height: 26px; border: 1px solid var(--border); border-radius: 5px;
    background: var(--bg); color: var(--text); font-size: 12px;
    padding: 0 7px; cursor: pointer; line-height: 24px;
  }
  button.btn:hover { border-color: var(--accent); }
  button.btn.active { border-color: var(--accent); color: var(--accent); }
  input.clr {
    width: 26px; height: 26px; padding: 2px; border: 1px solid var(--border);
    border-radius: 5px; background: var(--bg); cursor: pointer;
  }
  input.url-in {
    flex: 1; min-width: 180px; height: 26px; padding: 0 8px;
    border: 1px solid var(--border); border-radius: 5px;
    background: var(--surface); color: var(--text); font-size: 12px;
  }
  #editor {
    flex: 1; overflow-y: auto; padding: 12px 14px;
    background: var(--ed-bg); color: var(--ed-fg);
    font-size: 14px; line-height: 1.55; outline: none;
  }
  #editor:focus { box-shadow: inset 0 0 0 1px var(--accent); }
  #editor table { border-collapse: collapse; margin: 8px 0; min-width: 240px; }
  #editor th, #editor td {
    border: 1px solid var(--border); padding: 6px 10px; min-width: 64px;
    vertical-align: top;
  }
  #editor th { background: rgba(127,127,127,0.14); font-weight: 600; text-align: left; }
  #editor blockquote {
    margin: 10px 0; padding: 0 0 0 12px; border-left: 3px solid var(--border);
  }
  #editor a { color: var(--accent); }
  #editor img { max-width: 100%%; height: auto; border-radius: 6px; margin: 4px 0; }
</style>
</head>
<body>
  <div class="tb" id="toolbar">
    <select class="sel" id="blk" title="%s">
      <option value="P">%s</option>
      <option value="H1">%s 1</option>
      <option value="H2">%s 2</option>
      <option value="H3">%s 3</option>
      <option value="BLOCKQUOTE">%s</option>
      <option value="PRE">Code</option>
    </select>
    <select class="sel" id="fsize" title="%s">
      <option value="2">%s</option>
      <option value="3" selected>%s</option>
      <option value="4">%s</option>
      <option value="5">%s</option>
    </select>
    <span class="sep"></span>
    <button class="btn" data-cmd="bold" title="%s (⌘B)"><b>B</b></button>
    <button class="btn" data-cmd="italic" title="%s (⌘I)"><i>I</i></button>
    <button class="btn" data-cmd="underline" title="%s (⌘U)"><u>U</u></button>
    <button class="btn" data-cmd="strikeThrough" title="%s"><s>S</s></button>
    <input class="clr" type="color" id="fg" value="#e6edf3" title="%s">
    <input class="clr" type="color" id="bg" value="#f59e0b" title="%s">
    <button class="btn" data-cmd="removeFormat" title="%s">T<sub>x</sub></button>
    <span class="sep"></span>
    <button class="btn" data-cmd="insertUnorderedList" title="%s">• %s</button>
    <button class="btn" data-cmd="insertOrderedList" title="%s">1. %s</button>
    <button class="btn" data-cmd="outdent" title="%s">⇤</button>
    <button class="btn" data-cmd="indent" title="%s">⇥</button>
    <button class="btn" data-cmd="justifyLeft" title="%s">≡L</button>
    <button class="btn" data-cmd="justifyCenter" title="%s">≡M</button>
    <button class="btn" data-cmd="justifyRight" title="%s">≡R</button>
    <span class="sep"></span>
    <button class="btn" id="btn-link" title="%s">🔗 Link</button>
    <button class="btn" id="btn-img" title="%s">🖼 %s</button>
    <button class="btn" id="btn-tbl" title="%s">▦ %s</button>
    <input type="file" id="img-pick" accept="image/*" style="display:none">
  </div>

  <div class="tb-sub" id="link-bar">
    <span>URL:</span>
    <input class="url-in" id="link-url" placeholder="https://…">
    <button class="btn" id="link-ok">%s</button>
    <button class="btn" id="link-unlink">%s</button>
    <button class="btn" id="link-cancel">✕</button>
  </div>

  <div class="tb-sub" id="tbl-bar">
    <span>%s:</span>
    <button class="btn" id="tbl-r-add">+ %s</button>
    <button class="btn" id="tbl-r-del">- %s</button>
    <button class="btn" id="tbl-c-add">+ %s</button>
    <button class="btn" id="tbl-c-del">- %s</button>
    <button class="btn" id="tbl-del" style="color:#f87171;">%s</button>
  </div>

  <div id="editor" contenteditable="true" spellcheck="true"></div>

<script>
(function() {
  const ed = document.getElementById('editor');
  const linkBar = document.getElementById('link-bar');
  const linkURL = document.getElementById('link-url');
  const tblBar = document.getElementById('tbl-bar');
  const imgPick = document.getElementById('img-pick');
  let savedRange = null;
  let seq = 0;

  ed.innerHTML = %s;

  function notify(prefix) {
    seq++;
    document.title = prefix + ':' + seq;
  }

  function saveSel() {
    const sel = window.getSelection();
    if (sel && sel.rangeCount > 0) {
      const r = sel.getRangeAt(0);
      if (ed.contains(r.commonAncestorContainer)) savedRange = r.cloneRange();
    }
  }
  function restoreSel() {
    if (!savedRange) { ed.focus(); return; }
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(savedRange);
  }

  document.getElementById('toolbar').addEventListener('mousedown', (e) => {
    if (e.target.tagName !== 'SELECT' && e.target.tagName !== 'INPUT') {
      e.preventDefault();
    }
  });
  tblBar.addEventListener('mousedown', (e) => e.preventDefault());

  document.querySelectorAll('[data-cmd]').forEach((btn) => {
    btn.addEventListener('click', () => {
      ed.focus();
      document.execCommand(btn.dataset.cmd, false, null);
      notify('mdv:dirty');
    });
  });

  document.getElementById('blk').addEventListener('change', (e) => {
    ed.focus();
    document.execCommand('formatBlock', false, '<' + e.target.value + '>');
    notify('mdv:dirty');
  });
  document.getElementById('fsize').addEventListener('change', (e) => {
    ed.focus();
    document.execCommand('fontSize', false, e.target.value);
    notify('mdv:dirty');
  });
  document.getElementById('fg').addEventListener('input', (e) => {
    ed.focus();
    document.execCommand('foreColor', false, e.target.value);
    notify('mdv:dirty');
  });
  document.getElementById('bg').addEventListener('input', (e) => {
    ed.focus();
    document.execCommand('hiliteColor', false, e.target.value);
    notify('mdv:dirty');
  });

  document.getElementById('btn-link').addEventListener('click', () => {
    saveSel();
    linkBar.classList.toggle('open');
    if (linkBar.classList.contains('open')) {
      linkURL.value = 'https://';
      linkURL.focus();
    }
  });
  document.getElementById('link-ok').addEventListener('click', () => {
    let u = linkURL.value.trim();
    if (u && !/^https?:\/\//i.test(u) && !/^mailto:/i.test(u)) u = 'https://' + u;
    linkBar.classList.remove('open');
    restoreSel();
    if (u) document.execCommand('createLink', false, u);
    notify('mdv:dirty');
  });
  document.getElementById('link-unlink').addEventListener('click', () => {
    linkBar.classList.remove('open');
    restoreSel();
    document.execCommand('unlink', false, null);
    notify('mdv:dirty');
  });
  document.getElementById('link-cancel').addEventListener('click', () => {
    linkBar.classList.remove('open');
    ed.focus();
  });

  function insertImageBlob(file) {
    const reader = new FileReader();
    reader.onload = () => {
      restoreSel();
      const img = document.createElement('img');
      img.src = String(reader.result || '');
      img.style.maxWidth = '100%%';
      img.style.height = 'auto';
      img.style.borderRadius = '6px';
      img.style.margin = '4px 0';
      document.execCommand('insertHTML', false, img.outerHTML);
      notify('mdv:dirty');
    };
    reader.readAsDataURL(file);
  }
  document.getElementById('btn-img').addEventListener('click', () => {
    saveSel();
    imgPick.value = '';
    imgPick.click();
  });
  imgPick.addEventListener('change', () => {
    if (imgPick.files && imgPick.files[0]) insertImageBlob(imgPick.files[0]);
  });

  function makeTableHTML(rows, cols) {
    let h = '<table><thead><tr>';
    for (let c = 0; c < cols; c++) h += '<th>%s ' + (c + 1) + '</th>';
    h += '</tr></thead><tbody>';
    for (let r = 0; r < rows - 1; r++) {
      h += '<tr>';
      for (let c = 0; c < cols; c++) h += '<td><br></td>';
      h += '</tr>';
    }
    return h + '</tbody></table><p><br></p>';
  }
  document.getElementById('btn-tbl').addEventListener('click', () => {
    ed.focus();
    document.execCommand('insertHTML', false, makeTableHTML(3, 3));
    updateTblBar();
    notify('mdv:dirty');
  });

  function activeCell() {
    const sel = window.getSelection();
    if (!sel || !sel.anchorNode) return null;
    let n = sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement;
    while (n && n !== ed) {
      if (n.tagName === 'TD' || n.tagName === 'TH') return n;
      n = n.parentElement;
    }
    return null;
  }
  function updateTblBar() {
    tblBar.classList.toggle('open', !!activeCell());
  }
  document.addEventListener('selectionchange', () => {
    if (ed.contains(document.activeElement) || document.activeElement === ed) updateTblBar();
  });
  document.getElementById('tbl-r-add').addEventListener('click', () => {
    const cell = activeCell(); if (!cell) return;
    const tr = cell.parentElement;
    const tbl = tr.closest('table');
    const nr = document.createElement('tr');
    for (let i = 0; i < tr.cells.length; i++) {
      const td = document.createElement('td'); td.innerHTML = '<br>'; nr.appendChild(td);
    }
    tr.after(nr);
    notify('mdv:dirty');
  });
  document.getElementById('tbl-r-del').addEventListener('click', () => {
    const cell = activeCell(); if (!cell) return;
    const tr = cell.parentElement;
    const tbl = tr.closest('table');
    if (tbl.rows.length <= 1) tbl.remove(); else tr.remove();
    updateTblBar();
    notify('mdv:dirty');
  });
  document.getElementById('tbl-c-add').addEventListener('click', () => {
    const cell = activeCell(); if (!cell) return;
    const idx = cell.cellIndex;
    const tbl = cell.closest('table');
    Array.from(tbl.rows).forEach((r) => {
      const tag = (r.cells[idx] && r.cells[idx].tagName === 'TH') ? 'th' : 'td';
      const nc = document.createElement(tag);
      nc.innerHTML = tag === 'th' ? '%s' : '<br>';
      if (r.cells[idx]) r.cells[idx].after(nc); else r.appendChild(nc);
    });
    notify('mdv:dirty');
  });
  document.getElementById('tbl-c-del').addEventListener('click', () => {
    const cell = activeCell(); if (!cell) return;
    const idx = cell.cellIndex;
    const tbl = cell.closest('table');
    Array.from(tbl.rows).forEach((r) => { if (r.cells[idx]) r.cells[idx].remove(); });
    if (!tbl.rows.length || !tbl.rows[0].cells.length) tbl.remove();
    updateTblBar();
    notify('mdv:dirty');
  });
  document.getElementById('tbl-del').addEventListener('click', () => {
    const cell = activeCell(); if (!cell) return;
    cell.closest('table').remove();
    updateTblBar();
    notify('mdv:dirty');
  });

  ed.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      notify('mdv:send');
      return;
    }
    if (e.key === 'Tab') {
      const cell = activeCell();
      if (!cell) return;
      e.preventDefault();
      const tbl = cell.closest('table');
      const cells = Array.from(tbl.querySelectorAll('th,td'));
      const i = cells.indexOf(cell);
      const next = e.shiftKey ? cells[i - 1] : cells[i + 1];
      if (next) {
        const rng = document.createRange();
        rng.selectNodeContents(next);
        rng.collapse(true);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(rng);
      } else if (!e.shiftKey && i === cells.length - 1) {
        const lastRow = tbl.rows[tbl.rows.length - 1];
        const nr = document.createElement('tr');
        for (let c = 0; c < lastRow.cells.length; c++) {
          const td = document.createElement('td'); td.innerHTML = '<br>'; nr.appendChild(td);
        }
        lastRow.after(nr);
        const rng = document.createRange();
        rng.selectNodeContents(nr.cells[0]);
        rng.collapse(true);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(rng);
        notify('mdv:dirty');
      }
    }
  });

  ed.addEventListener('paste', (e) => {
    const cd = e.clipboardData;
    if (!cd) return;
    if (cd.items) {
      for (const it of cd.items) {
        if (it.type && it.type.startsWith('image/')) {
          const blob = it.getAsFile();
          if (blob) {
            e.preventDefault();
            saveSel();
            insertImageBlob(blob);
            return;
          }
        }
      }
    }
    const hasHTML = cd.types && Array.from(cd.types).includes('text/html');
    if (cd.files && cd.files.length > 0 && !hasHTML && !cd.getData('text/plain')) {
      e.preventDefault();
      notify('mdv:paste-files');
    }
  });

  ed.addEventListener('input', () => notify('mdv:dirty'));

  window.__mdvSetSig = function(sigHTML) {
    const cur = ed.querySelector('[data-mdv-sig="1"]');
    if (!sigHTML) {
      if (cur) cur.remove();
      return;
    }
    const tmp = document.createElement('div');
    tmp.innerHTML = sigHTML;
    const next = tmp.firstElementChild;
    if (!next) return;
    if (cur) cur.replaceWith(next);
    else {
      const fwd = ed.querySelector('[data-mdv-fwd="1"]');
      const q = ed.querySelector('[data-mdv-quote="1"]');
      if (fwd) fwd.before(next);
      else if (q) q.before(next);
      else ed.appendChild(next);
    }
  };

  window.__mdvInsertHTML = function(frag) {
    ed.focus();
    document.execCommand('insertHTML', false, frag);
    notify('mdv:dirty');
  };

  window.__mdvRead = function() {
    const clone = ed.cloneNode(true);
    clone.querySelectorAll('table').forEach((t) => {
      t.setAttribute('style', 'border-collapse:collapse;border:1px solid #d0d7de;margin:8px 0;font-size:13.5px;');
    });
    clone.querySelectorAll('th').forEach((th) => {
      th.setAttribute('style', 'border:1px solid #d0d7de;padding:6px 10px;background:#f6f8fa;text-align:left;font-weight:600;color:#1f2328;');
    });
    clone.querySelectorAll('td').forEach((td) => {
      td.setAttribute('style', 'border:1px solid #d0d7de;padding:6px 10px;vertical-align:top;color:#1f2328;');
    });
    clone.querySelectorAll('blockquote').forEach((bq) => {
      if (!bq.getAttribute('style')) {
        bq.setAttribute('style', 'margin:10px 0;padding:0 0 0 12px;border-left:2px solid #d0d7de;color:#57606a;');
      }
    });
    const fullHTML = '<!doctype html><html><head><meta charset="utf-8"></head>' +
      '<body style="margin:0;padding:0;font-family:-apple-system,BlinkMacSystemFont,\'Segoe UI\',Roboto,Helvetica,Arial,sans-serif;font-size:14px;line-height:1.55;color:#1f2328;">' +
      clone.innerHTML + '</body></html>';
    return { html: fullHTML, text: (ed.innerText || '').trim() };
  };
})();
</script>
</body>
</html>`,
		pal.Bg, pal.Surface, pal.Text, pal.Muted, pal.Border, pal.Accent, editorBg, editorFg,
		lbl("Absatzformat", "Block format"),
		lbl("Absatz", "Paragraph"),
		lbl("Überschrift", "Heading"),
		lbl("Überschrift", "Heading"),
		lbl("Überschrift", "Heading"),
		lbl("Zitat", "Quote"),
		lbl("Schriftgröße", "Font size"),
		lbl("Klein", "Small"),
		lbl("Normal", "Normal"),
		lbl("Groß", "Large"),
		lbl("Sehr groß", "Huge"),
		lbl("Fett", "Bold"),
		lbl("Kursiv", "Italic"),
		lbl("Unterstrichen", "Underline"),
		lbl("Durchgestrichen", "Strikethrough"),
		lbl("Textfarbe", "Text color"),
		lbl("Hervorheben", "Highlight"),
		lbl("Formatierung entfernen", "Clear formatting"),
		lbl("Aufzählung", "Bullet list"),
		lbl("Liste", "List"),
		lbl("Nummerierte Liste", "Numbered list"),
		lbl("Liste", "List"),
		lbl("Einzug verkleinern", "Outdent"),
		lbl("Einzug vergrößern", "Indent"),
		lbl("Linksbündig", "Align left"),
		lbl("Zentriert", "Align center"),
		lbl("Rechtsbündig", "Align right"),
		lbl("Link einfügen", "Insert link"),
		lbl("Bild einfügen", "Insert image"),
		lbl("Bild", "Image"),
		lbl("Tabelle einfügen", "Insert table"),
		lbl("Tabelle", "Table"),
		lbl("Übernehmen", "Apply"),
		lbl("Link entfernen", "Unlink"),
		lbl("Tabelle", "Table"),
		lbl("Zeile", "Row"),
		lbl("Zeile", "Row"),
		lbl("Spalte", "Column"),
		lbl("Spalte", "Column"),
		lbl("Tabelle löschen", "Delete table"),
		string(initJSON),
		lbl("Spalte", "Column"),
		lbl("Spalte", "Column"),
	)
}
