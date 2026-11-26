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

	"github.com/diver80/postdive/internal/mailengine"
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
	UpdateTheme(pal ComposeThemePalette)
	InsertHTML(fragHTML string)
	SetUserHTML(fragHTML string)
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
	return buildComposeEditorInitialHTMLWithSig(topPlain, composeSignatureBlockHTML(sigPlain), false, fwdHeader, fwdHTML, inlineImgs, replyQuotePlain)
}

// buildComposeEditorInitialHTMLWithSig builds editable HTML using an already formatted [data-mdv-sig="1"] block
// and supports placing the signature above or below quoted replies.
func buildComposeEditorInitialHTMLWithSig(topPlain, sigBlockHTML string, sigBelow bool, fwdHeader, fwdHTML string, inlineImgs map[string]mailengine.InlineImage, replyQuotePlain string) string {
	var b strings.Builder
	b.WriteString(`<div data-mdv-user="1">`)
	b.WriteString(plainBlockToEditableHTML(topPlain))
	b.WriteString(`</div>`)

	if !sigBelow && sigBlockHTML != "" {
		b.WriteString(sigBlockHTML)
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

	if sigBelow && sigBlockHTML != "" {
		b.WriteString(sigBlockHTML)
	}
	return b.String()
}

// NotifyComposeDirty schedules a debounced read of the HTML editor so composeSnapshot & Draft guards stay fresh.
func (a *App) NotifyComposeDirty() {
	a.composeEditorDirty = true
	a.composeDirtyAt = time.Now()
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
	a.requestComposeSend(context.Background(), a.buildComposeReq(false))
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
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("postdive-clipboard-%d.png", time.Now().UnixNano()))
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
			return formatShortcuts(d)
		}
		return formatShortcuts(e)
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
    padding: 7px 10px; background: var(--surface);
    border-bottom: 2px solid var(--border);
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.16);
    user-select: none; position: relative; z-index: 5;
  }
  .tb-sub {
    display: none; flex-wrap: wrap; align-items: center; gap: 5px;
    padding: 6px 10px; background: var(--surface);
    border-bottom: 1px solid var(--border); font-size: 12px;
  }
  .tb-sub.open { display: flex; }
  .sep { width: 1px; height: 18px; background: var(--border); margin: 0 3px; }
  button.btn, select.sel {
    height: 26px; border: 1px solid var(--border); border-radius: 5px;
    background: rgba(127, 127, 127, 0.13); color: var(--text); font-size: 12px;
    padding: 0 8px; cursor: pointer; line-height: 24px;
    transition: border-color 0.12s, background 0.12s, color 0.12s;
  }
  button.btn:hover, select.sel:hover {
    border-color: var(--accent);
    background: rgba(127, 127, 127, 0.24);
  }
  button.btn.active {
    background: var(--bg);
    border-color: var(--accent);
    color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
    font-weight: 600;
  }
  button.btn-danger {
    color: #f87171;
    border-color: rgba(248, 113, 113, 0.45);
  }
  button.btn-danger:hover {
    border-color: #f87171;
    background: rgba(248, 113, 113, 0.14);
  }
  input.clr {
    width: 26px; height: 26px; padding: 2px; border: 1px solid var(--border);
    border-radius: 5px; background: rgba(127, 127, 127, 0.13); cursor: pointer;
  }
  input.url-in {
    flex: 1; min-width: 180px; height: 26px; padding: 0 8px;
    border: 1px solid var(--border); border-radius: 5px;
    background: var(--bg); color: var(--text); font-size: 12px;
  }
  #ed-wrap {
    position: relative; flex: 1; display: flex; flex-direction: column;
    min-height: 0; overflow: hidden;
  }
  #editor {
    flex: 1; overflow-y: auto; padding: 12px 14px;
    background: var(--ed-bg); color: var(--ed-fg);
    font-size: 14px; line-height: 1.55; outline: none;
  }
  /* The caret in the accent color: WebKit's default follows the text and goes nearly unseen on
     dark themes, also where pasted or quoted HTML carries its own text color. */
  #editor, #editor * { caret-color: var(--accent); }
  #editor p { margin: 0 0 0.75em 0; }
  #editor p:last-child, #editor [data-mdv-user="1"] > p:last-child { margin-bottom: 0; }
  #editor div:not([data-mdv-user]):not([data-mdv-sig]):not([data-mdv-fwd]) { margin: 0; }
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
  #editor hr { border: none; border-top: 1px solid var(--border); margin: 12px 0; }
  #editor a { color: var(--accent); }
  #editor img { max-width: 100%%; height: auto; border-radius: 6px; margin: 4px 0; cursor: pointer; }
  #editor img.mdv-img-sel { outline: 2px solid var(--accent); outline-offset: 2px; }
  #editor [data-mdv-sig="1"], #editor [data-mdv-sig="1"] * { color: var(--muted) !important; }
  #editor [data-mdv-sig="1"] a { color: var(--accent) !important; }
  #editor [data-mdv-sig="1"] { opacity: 0.9; margin-top: 12px; }
  #img-handle {
    display: none; position: absolute; width: 12px; height: 12px;
    background: var(--accent); border: 2px solid var(--bg); border-radius: 3px;
    cursor: nwse-resize; z-index: 20; box-shadow: 0 1px 4px rgba(0, 0, 0, 0.35);
  }
  #img-handle.open { display: block; }
</style>
</head>
<body>
  <div class="tb" id="toolbar">
    <button class="btn" id="btn-undo" data-cmd="undo" title="%s">↶</button>
    <button class="btn" id="btn-redo" data-cmd="redo" title="%s">↷</button>
    <span class="sep"></span>
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
    <button class="btn" data-cmd="bold" title="%s"><b>B</b></button>
    <button class="btn" data-cmd="italic" title="%s"><i>I</i></button>
    <button class="btn" data-cmd="underline" title="%s"><u>U</u></button>
    <button class="btn" data-cmd="strikeThrough" title="%s"><s>S</s></button>
    <input class="clr" type="color" id="fg" value="%s" title="%s">
    <input class="clr" type="color" id="bg" value="%s" title="%s">
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
    <button class="btn" id="btn-hr" data-cmd="insertHorizontalRule" title="%s">―</button>
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
    <button class="btn btn-danger" id="tbl-del">%s</button>
  </div>

  <div class="tb-sub" id="img-bar">
    <span>%s:</span>
    <button class="btn" data-img-pct="25">25%%</button>
    <button class="btn" data-img-pct="50">50%%</button>
    <button class="btn" data-img-pct="75">75%%</button>
    <button class="btn" data-img-pct="100">100%%</button>
    <button class="btn" id="img-smaller" title="%s">−</button>
    <button class="btn" id="img-larger" title="%s">+</button>
    <span id="img-dim" style="color:var(--muted);font-variant-numeric:tabular-nums;"></span>
    <button class="btn btn-danger" id="img-del">🗑 %s</button>
  </div>

  <div id="ed-wrap">
    <div id="editor" contenteditable="true" spellcheck="true"></div>
    <div id="img-handle" aria-hidden="true"></div>
  </div>

<script>
(function() {
  const edWrap = document.getElementById('ed-wrap');
  const ed = document.getElementById('editor');
  const linkBar = document.getElementById('link-bar');
  const linkURL = document.getElementById('link-url');
  const tblBar = document.getElementById('tbl-bar');
  const imgBar = document.getElementById('img-bar');
  const imgDim = document.getElementById('img-dim');
  const imgHandle = document.getElementById('img-handle');
  const imgPick = document.getElementById('img-pick');
  const statefulCmds = new Set([
    'bold', 'italic', 'underline', 'strikeThrough',
    'insertUnorderedList', 'insertOrderedList',
    'justifyLeft', 'justifyCenter', 'justifyRight'
  ]);
  let savedRange = null;
  let selImg = null;
  let seq = 0;

  ed.innerHTML = %s;
  try { document.execCommand('defaultParagraphSeparator', false, 'p'); } catch (_) {}

  function ensureParagraphInUserBox() {
    normalizeUserDivsPreservingSelection();
  }

  function normalizeUserDivsPreservingSelection() {
    const host = ed.querySelector('[data-mdv-user="1"]') || ed;
    const divs = Array.from(host.childNodes).filter((node) =>
      node.nodeType === 1 && node.tagName === 'DIV' &&
      !node.hasAttribute('data-mdv-user') && !node.hasAttribute('data-mdv-sig') && !node.hasAttribute('data-mdv-fwd')
    );
    if (divs.length === 0) return;
    const sel = window.getSelection();
    let aNode = sel && sel.rangeCount > 0 ? sel.anchorNode : null;
    let aOff = sel && sel.rangeCount > 0 ? sel.anchorOffset : 0;
    let fNode = sel && sel.rangeCount > 0 ? sel.focusNode : null;
    let fOff = sel && sel.rangeCount > 0 ? sel.focusOffset : 0;
    const mapped = new Map();
    divs.forEach((div) => {
      const p = document.createElement('p');
      while (div.firstChild) p.appendChild(div.firstChild);
      if (!p.firstChild) p.innerHTML = '<br>';
      div.replaceWith(p);
      mapped.set(div, p);
    });
    if (sel && aNode) {
      if (mapped.has(aNode)) aNode = mapped.get(aNode);
      if (mapped.has(fNode)) fNode = mapped.get(fNode);
      try {
        if (sel.setBaseAndExtent && fNode) {
          sel.setBaseAndExtent(aNode, Math.min(aOff, aNode.nodeType === 3 ? aNode.length : aNode.childNodes.length), fNode, Math.min(fOff, fNode.nodeType === 3 ? fNode.length : fNode.childNodes.length));
        } else {
          const nr = document.createRange();
          nr.setStart(aNode, Math.min(aOff, aNode.nodeType === 3 ? aNode.length : aNode.childNodes.length));
          nr.collapse(true);
          sel.removeAllRanges();
          sel.addRange(nr);
        }
      } catch (_) {}
    }
  }

  function clampSelectionBeforeSig() {
    const sig = ed.querySelector('[data-mdv-sig="1"]');
    const sel = window.getSelection();
    if (!sig || !sel || sel.rangeCount === 0) return false;
    const r = sel.getRangeAt(0);
    if (!r.intersectsNode || !r.intersectsNode(sig)) return false;
    const userBox = ed.querySelector('[data-mdv-user="1"]');
    const endLimit = document.createRange();
    if (userBox && (userBox.compareDocumentPosition(sig) & Node.DOCUMENT_POSITION_FOLLOWING)) {
      endLimit.selectNodeContents(userBox);
      endLimit.collapse(false);
    } else {
      endLimit.setStartBefore(sig);
      endLimit.collapse(true);
    }
    const clamped = r.cloneRange();
    // If start is already at or after the signature, collapse caret safely before signature.
    if (clamped.compareBoundaryPoints(Range.START_TO_START, endLimit) >= 0) {
      sel.removeAllRanges();
      sel.addRange(endLimit);
      return true;
    }
    if (clamped.compareBoundaryPoints(Range.END_TO_START, endLimit) > 0) {
      clamped.setEnd(endLimit.startContainer, endLimit.startOffset);
    }
    sel.removeAllRanges();
    sel.addRange(clamped);
    return clamped.collapsed;
  }

  function ensureTrailingBRSentinel() {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || !sel.isCollapsed) return;
    const r = sel.getRangeAt(0);
    let container = r.startContainer;
    let off = r.startOffset;
    if (container.nodeType !== 1) return;
    const prev = off > 0 ? container.childNodes[off - 1] : null;
    const next = off < container.childNodes.length ? container.childNodes[off] : null;
    if (prev && prev.nodeType === 1 && prev.tagName === 'BR' && !next && /^(P|DIV|LI|BLOCKQUOTE|TD|TH)$/i.test(container.tagName)) {
      const sentinel = document.createElement('br');
      prev.after(sentinel);
      const nr = document.createRange();
      nr.setStartAfter(prev);
      nr.collapse(true);
      sel.removeAllRanges();
      sel.addRange(nr);
    }
  }

  function insertSoftLineBreak() {
    clampSelectionBeforeSig();
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return;
    if (document.execCommand && document.execCommand('insertLineBreak', false, null)) {
      ensureTrailingBRSentinel();
      notify('mdv:dirty');
      return;
    }
    const range = sel.getRangeAt(0);
    range.deleteContents();
    const br = document.createElement('br');
    range.insertNode(br);
    const next = br.nextSibling;
    const block = br.parentElement;
    // In WebKit/Blink, a single trailing <br> at the end of a <p> doesn't create a visible new line
    // until a sentinel <br> follows it.
    if (!next && block && /^(P|DIV|LI|BLOCKQUOTE|TD|TH)$/i.test(block.tagName)) {
      const sentinel = document.createElement('br');
      br.after(sentinel);
    }
    const nr = document.createRange();
    nr.setStartAfter(br);
    nr.collapse(true);
    sel.removeAllRanges();
    sel.addRange(nr);
    notify('mdv:dirty');
  }

  function protectSignatures() {
    ed.querySelectorAll('[data-mdv-sig="1"]').forEach((sig) => {
      sig.setAttribute('contenteditable', 'false');
      if (!sig.previousElementSibling && sig === ed.firstElementChild) {
        const p = document.createElement('p');
        p.innerHTML = '<br>';
        sig.before(p);
      }
    });
  }
  protectSignatures();
  ensureParagraphInUserBox();

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

  function activeAnchor() {
    const sel = window.getSelection();
    if (!sel || !sel.anchorNode) return null;
    let n = sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement;
    while (n && n !== ed) {
      if (n.tagName === 'A') return n;
      n = n.parentElement;
    }
    return null;
  }

  function updateFormatStates() {
    document.querySelectorAll('#toolbar [data-cmd]').forEach((btn) => {
      const cmd = btn.dataset.cmd;
      if (!statefulCmds.has(cmd)) return;
      let on = false;
      try { on = document.queryCommandState(cmd); } catch (_) {}
      btn.classList.toggle('active', !!on);
    });
    const a = activeAnchor();
    document.getElementById('btn-link').classList.toggle('active', !!a);
    let blkVal = '';
    try { blkVal = String(document.queryCommandValue('formatBlock') || '').replace(/[<>]/g, '').toUpperCase(); } catch (_) {}
    const blkSel = document.getElementById('blk');
    if (blkSel) {
      const opts = Array.from(blkSel.options).map((o) => o.value);
      blkSel.value = opts.includes(blkVal) ? blkVal : 'P';
    }
  }

  function clearImageSelection() {
    if (selImg) {
      selImg.classList.remove('mdv-img-sel');
      selImg = null;
    }
    imgBar.classList.remove('open');
    imgHandle.classList.remove('open');
    imgDim.textContent = '';
  }

  function positionImgHandle() {
    if (!selImg || !ed.contains(selImg)) {
      clearImageSelection();
      return;
    }
    const r = selImg.getBoundingClientRect();
    const wr = edWrap.getBoundingClientRect();
    imgDim.textContent = Math.round(r.width) + ' × ' + Math.round(r.height) + ' px';
    if (r.width <= 0 || r.bottom < wr.top || r.top > wr.bottom) {
      imgHandle.classList.remove('open');
      return;
    }
    imgHandle.style.left = Math.round(r.right - wr.left - 6) + 'px';
    imgHandle.style.top = Math.round(r.bottom - wr.top - 6) + 'px';
    imgHandle.classList.add('open');
  }

  function selectImage(img) {
    if (!img || img.closest('[data-mdv-sig="1"]')) return;
    if (selImg && selImg !== img) selImg.classList.remove('mdv-img-sel');
    selImg = img;
    selImg.classList.add('mdv-img-sel');
    imgBar.classList.add('open');
    positionImgHandle();
  }

  function deleteSelectedImage() {
    if (!selImg) return;
    const img = selImg;
    clearImageSelection();
    img.remove();
    ed.focus();
    notify('mdv:dirty');
  }

  document.getElementById('toolbar').addEventListener('mousedown', (e) => {
    if (e.target.tagName !== 'SELECT' && e.target.tagName !== 'INPUT') {
      e.preventDefault();
    }
  });
  tblBar.addEventListener('mousedown', (e) => e.preventDefault());
  imgBar.addEventListener('mousedown', (e) => e.preventDefault());

  document.querySelectorAll('[data-cmd]').forEach((btn) => {
    btn.addEventListener('click', () => {
      ed.focus();
      document.execCommand(btn.dataset.cmd, false, null);
      updateFormatStates();
      notify('mdv:dirty');
    });
  });

  document.getElementById('blk').addEventListener('change', (e) => {
    ed.focus();
    document.execCommand('formatBlock', false, '<' + e.target.value + '>');
    updateFormatStates();
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
    const curA = activeAnchor();
    linkBar.classList.toggle('open');
    if (linkBar.classList.contains('open')) {
      linkURL.value = (curA && curA.getAttribute('href')) || 'https://';
      linkURL.focus();
      linkURL.select();
    }
  });
  document.getElementById('link-ok').addEventListener('click', () => {
    let u = linkURL.value.trim();
    if (u && !/^https?:\/\//i.test(u) && !/^mailto:/i.test(u)) u = 'https://' + u;
    linkBar.classList.remove('open');
    restoreSel();
    if (u) document.execCommand('createLink', false, u);
    updateFormatStates();
    notify('mdv:dirty');
  });
  document.getElementById('link-unlink').addEventListener('click', () => {
    linkBar.classList.remove('open');
    restoreSel();
    document.execCommand('unlink', false, null);
    updateFormatStates();
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

  ed.addEventListener('click', (e) => {
    if (e.target && e.target.tagName === 'IMG' && !e.target.closest('[data-mdv-sig="1"]')) {
      e.preventDefault();
      ed.focus();
      selectImage(e.target);
      return;
    }
    clearImageSelection();
  });
  ed.addEventListener('scroll', positionImgHandle);
  window.addEventListener('resize', positionImgHandle);

  imgBar.querySelectorAll('[data-img-pct]').forEach((btn) => {
    btn.addEventListener('click', () => {
      if (!selImg) return;
      selImg.style.width = btn.dataset.imgPct + '%%';
      selImg.style.height = 'auto';
      selImg.removeAttribute('width');
      positionImgHandle();
      notify('mdv:dirty');
    });
  });
  function nudgeImageWidth(delta) {
    if (!selImg) return;
    const curW = Math.round(selImg.getBoundingClientRect().width || selImg.naturalWidth || 200);
    const maxW = Math.max(80, ed.clientWidth - 28);
    const nextW = Math.max(40, Math.min(maxW, curW + delta));
    selImg.style.width = nextW + 'px';
    selImg.style.height = 'auto';
    selImg.setAttribute('width', String(nextW));
    positionImgHandle();
    notify('mdv:dirty');
  }
  document.getElementById('img-smaller').addEventListener('click', () => nudgeImageWidth(-50));
  document.getElementById('img-larger').addEventListener('click', () => nudgeImageWidth(50));
  document.getElementById('img-del').addEventListener('click', deleteSelectedImage);

  imgHandle.addEventListener('mousedown', (e) => {
    if (!selImg) return;
    e.preventDefault();
    e.stopPropagation();
    const startX = e.clientX;
    const startW = selImg.getBoundingClientRect().width || selImg.naturalWidth || 200;
    const maxW = Math.max(80, ed.clientWidth - 28);
    const onMove = (ev) => {
      if (!selImg) return;
      const nextW = Math.max(40, Math.min(maxW, Math.round(startW + (ev.clientX - startX))));
      selImg.style.width = nextW + 'px';
      selImg.style.height = 'auto';
      selImg.setAttribute('width', String(nextW));
      positionImgHandle();
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      notify('mdv:dirty');
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  });

  function sanitizePastedHTML(raw) {
    const parsed = new DOMParser().parseFromString(String(raw || ''), 'text/html');
    const tmp = parsed && parsed.body ? parsed.body : document.createElement('div');
    tmp.querySelectorAll('script,style,meta,link,title,xml,iframe,object,embed,svg,math,form,input,button,textarea,select,base,template,noscript').forEach((n) => n.remove());
    const safeUrl = (val, isImg) => {
      const s = String(val || '').replace(/[\u0000-\u001F\s]+/g, '').toLowerCase();
      if (!s) return false;
      if (s.startsWith('http://') || s.startsWith('https://') || s.startsWith('mailto:') || s.startsWith('#')) return true;
      if (isImg && s.startsWith('data:image/') && !s.startsWith('data:image/svg')) return true;
      return false;
    };
    tmp.querySelectorAll('*').forEach((el) => {
      for (const attr of Array.from(el.attributes)) {
        const name = attr.name.toLowerCase();
        if (name.startsWith('on') || name.startsWith('data-mdv-') || name === 'class' || name === 'color' || name === 'face' || name === 'srcdoc' || name === 'formaction' || name === 'contenteditable') {
          el.removeAttribute(attr.name);
          continue;
        }
        if (name === 'href' || name === 'src' || name === 'xlink:href' || name === 'action') {
          if (!safeUrl(attr.value, el.tagName === 'IMG' && name === 'src')) {
            el.removeAttribute(attr.name);
          }
        }
      }
      if (el.style) {
        el.style.background = '';
        el.style.backgroundColor = '';
        el.style.color = '';
        el.style.fontFamily = '';
      }
      if (!el.getAttribute('style')) el.removeAttribute('style');
    });
    return tmp.innerHTML;
  }

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
    if (ed.contains(document.activeElement) || document.activeElement === ed) {
      const sel = window.getSelection();
      if (selImg && sel && sel.rangeCount > 0) {
        const r = sel.getRangeAt(0);
        if (!r.intersectsNode || !r.intersectsNode(selImg)) {
          clearImageSelection();
        }
      }
      updateTblBar();
      updateFormatStates();
    }
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
    if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey && !e.altKey) {
      clampSelectionBeforeSig();
      if (e.shiftKey) {
        e.preventDefault();
        insertSoftLineBreak();
        return;
      }
      // Plain Return: ensure paragraph separator is <p> (distinct from Shift+Return <br>).
      try { document.execCommand('defaultParagraphSeparator', false, 'p'); } catch (_) {}
      const sel = window.getSelection();
      const anchorEl = sel && sel.anchorNode ? (sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement) : null;
      const inSpecialBlock = anchorEl && anchorEl.closest('li,pre,td,th');
      if (!inSpecialBlock) {
        setTimeout(normalizeUserDivsPreservingSelection, 0);
      }
    }
    if (selImg) {
      if (e.key === 'Backspace' || e.key === 'Delete') {
        e.preventDefault();
        deleteSelectedImage();
        return;
      }
      if (e.key === 'Escape' || e.key === 'Enter' || e.key.startsWith('Arrow') || (e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey)) {
        clearImageSelection();
      }
    }
    if (e.key === 'Backspace' || e.key === 'Delete') {
      const sel = window.getSelection();
      const sig = ed.querySelector('[data-mdv-sig="1"]');
      if (sig && sel && sel.rangeCount > 0 && !sel.isCollapsed) {
        const r = sel.getRangeAt(0);
        if (r.intersectsNode && r.intersectsNode(sig)) {
          e.preventDefault();
          const collapsed = clampSelectionBeforeSig();
          if (!collapsed) {
            document.execCommand('delete', false, null);
          }
          const userBox = ed.querySelector('[data-mdv-user="1"]');
          if (userBox && !userBox.firstElementChild && !String(userBox.textContent || '').trim()) {
            userBox.innerHTML = '<p><br></p>';
            const nr = document.createRange();
            nr.setStart(userBox.firstChild, 0);
            nr.collapse(true);
            sel.removeAllRanges();
            sel.addRange(nr);
          }
          notify('mdv:dirty');
          return;
        }
      }
    }
    if (e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
      clampSelectionBeforeSig();
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

  ed.addEventListener('cut', () => {
    clampSelectionBeforeSig();
  });

  ed.addEventListener('paste', (e) => {
    clampSelectionBeforeSig();
    const cd = e.clipboardData;
    if (!cd) return;
    const types = cd.types ? Array.from(cd.types) : [];
    const hasHTML = types.includes('text/html');
    const htmlRaw = hasHTML ? cd.getData('text/html') : '';
    const hasStructuredHTML = hasHTML && /<(table|tr|td|th|ul|ol|li|p|div|h[1-6]|blockquote|pre|a)\b/i.test(htmlRaw);

    // Prefer structured HTML (Excel/Confluence tables, formatted text) over companion preview PNGs.
    if (hasStructuredHTML) {
      const cleaned = sanitizePastedHTML(htmlRaw);
      if (cleaned) {
        e.preventDefault();
        document.execCommand('insertHTML', false, cleaned);
        notify('mdv:dirty');
        return;
      }
    }

    // Finder/Explorer file paste (often populates text/plain with the filename too).
    if (cd.files && cd.files.length > 0 && !hasHTML) {
      let allImages = true;
      for (const f of cd.files) {
        if (!f.type || !f.type.startsWith('image/')) {
          allImages = false;
          break;
        }
      }
      if (!allImages) {
        e.preventDefault();
        notify('mdv:paste-files');
        return;
      }
    }

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

    if (hasHTML) {
      const cleaned = sanitizePastedHTML(htmlRaw);
      if (cleaned) {
        e.preventDefault();
        document.execCommand('insertHTML', false, cleaned);
        notify('mdv:dirty');
      }
    }
  });

  ed.addEventListener('input', () => {
    if (selImg && !ed.contains(selImg)) clearImageSelection();
    normalizeUserDivsPreservingSelection();
    updateFormatStates();
    notify('mdv:dirty');
  });

  window.__mdvSetSig = function(sigHTML, sigBelow) {
    const cur = ed.querySelector('[data-mdv-sig="1"]');
    if (!sigHTML) {
      if (cur) {
        cur.remove();
        notify('mdv:dirty');
      }
      return;
    }
    const tmp = document.createElement('div');
    tmp.innerHTML = sigHTML;
    const next = tmp.firstElementChild;
    if (!next) return;
    if (cur) {
      cur.remove();
    }
    const below = !!sigBelow || next.getAttribute('data-mdv-sig-below') === '1';
    if (below) {
      ed.appendChild(next);
    } else {
      const fwd = ed.querySelector('[data-mdv-fwd="1"]');
      const q = ed.querySelector('[data-mdv-quote="1"]');
      if (fwd) fwd.before(next);
      else if (q) q.before(next);
      else ed.appendChild(next);
    }
    protectSignatures();
    ensureParagraphInUserBox();
    notify('mdv:dirty');
  };

  window.__mdvSetTheme = function(pal) {
    if (!pal) return;
    const rs = document.documentElement.style;
    if (pal.bg) rs.setProperty('--bg', pal.bg);
    if (pal.surface) rs.setProperty('--surface', pal.surface);
    if (pal.text) rs.setProperty('--text', pal.text);
    if (pal.muted) rs.setProperty('--muted', pal.muted);
    if (pal.border) rs.setProperty('--border', pal.border);
    if (pal.accent) rs.setProperty('--accent', pal.accent);
    if (pal.edBg) rs.setProperty('--ed-bg', pal.edBg);
    if (pal.edFg) rs.setProperty('--ed-fg', pal.edFg);
    const fgEl = document.getElementById('fg');
    if (fgEl && pal.edFg) fgEl.value = pal.edFg;
    const bgEl = document.getElementById('bg');
    if (bgEl && pal.accent) bgEl.value = pal.accent;
  };

  window.__mdvInsertHTML = function(frag) {
    ed.focus();
    document.execCommand('insertHTML', false, frag);
    notify('mdv:dirty');
  };

  window.__mdvSetUserHTML = function(frag) {
    let userBox = ed.querySelector('[data-mdv-user="1"]');
    if (!userBox) {
      userBox = document.createElement('div');
      userBox.setAttribute('data-mdv-user', '1');
      ed.insertBefore(userBox, ed.firstChild);
    }
    userBox.innerHTML = frag || '<p><br></p>';
    ed.focus();
    const sel = window.getSelection();
    if (sel) {
      const range = document.createRange();
      range.selectNodeContents(userBox);
      range.collapse(false);
      sel.removeAllRanges();
      sel.addRange(range);
    }
    notify('mdv:dirty');
  };

  window.__mdvRead = function() {
    const clone = ed.cloneNode(true);
    clone.querySelectorAll('img.mdv-img-sel').forEach((img) => {
      img.classList.remove('mdv-img-sel');
      if (!img.getAttribute('class')) img.removeAttribute('class');
    });
    clone.querySelectorAll('[contenteditable]').forEach((el) => {
      el.removeAttribute('contenteditable');
    });
    // Strip dark-mode default foreground colors generated by execCommand so outgoing HTML stays
    // legible on white recipient backgrounds.
    const isNearWhite = (c) => {
      const s = String(c || '').trim().toLowerCase();
      if (!s) return false;
      const m = s.match(/^rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$/);
      if (m) {
        const lum = Number(m[1]) * 299 + Number(m[2]) * 587 + Number(m[3]) * 114;
        return lum > 205000;
      }
      if (s.startsWith('#') && (s.length === 7 || s.length === 4)) {
        const hex = s.length === 4 ? s[1]+s[1]+s[2]+s[2]+s[3]+s[3] : s.slice(1);
        const r = parseInt(hex.slice(0, 2), 16), g = parseInt(hex.slice(2, 4), 16), b = parseInt(hex.slice(4, 6), 16);
        return (r * 299 + g * 587 + b * 114) > 205000;
      }
      return s === 'white';
    };
    clone.querySelectorAll('*').forEach((el) => {
      if (el.closest('[data-mdv-sig="1"]') || el.closest('[data-mdv-fwd="1"]')) return;
      if (el.style && el.style.color && isNearWhite(el.style.color) && !el.style.backgroundColor) {
        el.style.color = '';
        if (!el.getAttribute('style')) el.removeAttribute('style');
      }
      if (el.tagName === 'FONT' && el.hasAttribute('color') && isNearWhite(el.getAttribute('color'))) {
        el.removeAttribute('color');
      }
    });
    clone.querySelectorAll('table').forEach((t) => {
      if (t.closest('[data-mdv-sig="1"]') || t.closest('[data-mdv-fwd="1"]') || t.getAttribute('style')) return;
      t.setAttribute('style', 'border-collapse:collapse;border:1px solid #d0d7de;margin:8px 0;font-size:13.5px;');
      t.querySelectorAll('th').forEach((th) => {
        if (!th.getAttribute('style')) {
          th.setAttribute('style', 'border:1px solid #d0d7de;padding:6px 10px;background:#f6f8fa;text-align:left;font-weight:600;color:#1f2328;');
        }
      });
      t.querySelectorAll('td').forEach((td) => {
        if (!td.getAttribute('style')) {
          td.setAttribute('style', 'border:1px solid #d0d7de;padding:6px 10px;vertical-align:top;color:#1f2328;');
        }
      });
    });
    clone.querySelectorAll('blockquote').forEach((bq) => {
      if (!bq.getAttribute('style')) {
        bq.setAttribute('style', 'margin:10px 0;padding:0 0 0 12px;border-left:2px solid #d0d7de;color:#57606a;');
      }
    });
    // Normalize any remaining direct user-box <div>s to <p> and preserve clear paragraph spacing
    // (Return = <p> with gap, Shift+Return = tight <br>) in outgoing HTML across mail clients.
    const userHost = clone.querySelector('[data-mdv-user="1"]') || clone;
    Array.from(userHost.childNodes).forEach((node) => {
      if (node.nodeType === 1 && node.tagName === 'DIV' && !node.hasAttribute('data-mdv-user') && !node.hasAttribute('data-mdv-sig') && !node.hasAttribute('data-mdv-fwd')) {
        const p = document.createElement('p');
        while (node.firstChild) p.appendChild(node.firstChild);
        node.replaceWith(p);
      }
    });
    const userParas = Array.from(clone.querySelectorAll('p')).filter((p) =>
      !p.closest('[data-mdv-sig="1"]') && !p.closest('[data-mdv-fwd="1"]') && !p.closest('[data-mdv-quote="1"]') && !p.closest('td,th')
    );
    userParas.forEach((p, idx) => {
      if (!p.getAttribute('style')) {
        p.setAttribute('style', idx === userParas.length - 1 ? 'margin:0;' : 'margin:0 0 10px 0;');
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
		lbl("Rückgängig (⌘Z)", "Undo (⌘Z)"),
		lbl("Wiederholen (⌘⇧Z)", "Redo (⌘⇧Z)"),
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
		lbl("Fett (⌘B)", "Bold (⌘B)"),
		lbl("Kursiv (⌘I)", "Italic (⌘I)"),
		lbl("Unterstrichen (⌘U)", "Underline (⌘U)"),
		lbl("Durchgestrichen", "Strikethrough"),
		editorFg,
		lbl("Textfarbe", "Text color"),
		pal.Accent,
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
		lbl("Trennlinie einfügen", "Insert horizontal line"),
		lbl("Übernehmen", "Apply"),
		lbl("Link entfernen", "Unlink"),
		lbl("Tabelle", "Table"),
		lbl("Zeile", "Row"),
		lbl("Zeile", "Row"),
		lbl("Spalte", "Column"),
		lbl("Spalte", "Column"),
		lbl("Tabelle löschen", "Delete table"),
		lbl("Bild", "Image"),
		lbl("Bild verkleinern (-50 px)", "Smaller image (-50 px)"),
		lbl("Bild vergrößern (+50 px)", "Larger image (+50 px)"),
		lbl("Bild löschen", "Delete image"),
		string(initJSON),
		lbl("Spalte", "Column"),
		lbl("Spalte", "Column"),
	)
}
