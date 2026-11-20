package nativeui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/mailengine"
)

const (
	maxRemoteImageBytes  = 5 * 1024 * 1024
	maxImageDimension    = 8192
	maxImagePixels       = 16 * 1024 * 1024
	maxCachedImages      = 64
	maxMailtoBodyBytes   = 100 * 1024
	maxForwardOrigHTML   = 1500 * 1024
	undoExpiry           = 30 * time.Second
	printFallbackTTL     = 5 * time.Minute
	autoPrintScriptBody  = `window.addEventListener("load",function(){setTimeout(function(){window.print();},120);});`
	autoPrintScriptBlock = `<script>` + autoPrintScriptBody + `</script>`
)

var autoPrintScriptCSPHash = func() string {
	sum := sha256.Sum256([]byte(autoPrintScriptBody))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// MailtoFields holds parsed parameters from a mailto: URI.
type MailtoFields struct {
	To      string
	CC      string
	BCC     string
	Subject string
	Body    string
}

func cleanHeaderFieldValue(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(s)
	return strings.TrimSpace(s)
}

// ParseMailtoURL parses RFC 6068 mailto: URIs into compose fields,
// stripping header-injection control bytes (CR/LF/NUL) from header fields.
func ParseMailtoURL(raw string) (MailtoFields, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 7 || !strings.EqualFold(raw[:7], "mailto:") {
		return MailtoFields{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
		return MailtoFields{}, false
	}
	var toParts, ccParts, bccParts []string
	if opaque, err := url.PathUnescape(strings.TrimSpace(u.Opaque)); err == nil && opaque != "" {
		if c := cleanHeaderFieldValue(opaque); c != "" {
			toParts = append(toParts, c)
		}
	} else if u.Opaque != "" {
		if c := cleanHeaderFieldValue(u.Opaque); c != "" {
			toParts = append(toParts, c)
		}
	}

	var subject, body string
	// Walk raw query in deterministic order so repeated keys preserve order.
	rawQ := u.RawQuery
	for rawQ != "" {
		var part string
		if idx := strings.IndexByte(rawQ, '&'); idx >= 0 {
			part = rawQ[:idx]
			rawQ = rawQ[idx+1:]
		} else {
			part = rawQ
			rawQ = ""
		}
		if part == "" {
			continue
		}
		kRaw, vRaw, _ := strings.Cut(part, "=")
		k, err := url.QueryUnescape(kRaw)
		if err != nil {
			continue
		}
		v, err := url.QueryUnescape(vRaw)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "to":
			if c := cleanHeaderFieldValue(v); c != "" {
				toParts = append(toParts, c)
			}
		case "cc":
			if c := cleanHeaderFieldValue(v); c != "" {
				ccParts = append(ccParts, c)
			}
		case "bcc":
			if c := cleanHeaderFieldValue(v); c != "" {
				bccParts = append(bccParts, c)
			}
		case "subject":
			if subject == "" {
				subject = cleanHeaderFieldValue(v)
			}
		case "body":
			if body == "" {
				b := strings.ReplaceAll(v, "\x00", "")
				if len(b) > maxMailtoBodyBytes {
					b = b[:maxMailtoBodyBytes]
				}
				body = strings.TrimSpace(b)
			}
		}
	}

	return MailtoFields{
		To:      strings.Join(toParts, ", "),
		CC:      strings.Join(ccParts, ", "),
		BCC:     strings.Join(bccParts, ", "),
		Subject: subject,
		Body:    body,
	}, true
}

// --- Image loading for mail reading pane (cid:, data:image/, and opt-in http(s)://) ---

func (a *App) resetReaderImages(msgID string) {
	a.readerImgMu.Lock()
	if a.readerImgMsgID != msgID {
		a.readerImgMsgID = msgID
		a.readerImgCache = nil
	}
	a.readerImgMu.Unlock()
}

// extractSenderEmail parses a From header ("Name <user@example.com>") into a lowercase email address.
func extractSenderEmail(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(from); err == nil && strings.TrimSpace(addr.Address) != "" {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	return strings.ToLower(from)
}

func (a *App) senderRemoteImagesAllowed(from string) bool {
	em := extractSenderEmail(from)
	if em == "" || len(a.cfg.App.AllowedImageSenders) == 0 {
		return false
	}
	for _, s := range a.cfg.App.AllowedImageSenders {
		if strings.EqualFold(strings.TrimSpace(s), em) {
			return true
		}
	}
	return false
}

// SetSenderRemoteImagesAllowed persistently adds or removes a sender from the remote-image allowlist.
func (a *App) SetSenderRemoteImagesAllowed(from string, allow bool) {
	em := extractSenderEmail(from)
	if em == "" {
		return
	}
	next := make([]string, 0, len(a.cfg.App.AllowedImageSenders)+1)
	found := false
	for _, s := range a.cfg.App.AllowedImageSenders {
		low := strings.ToLower(strings.TrimSpace(s))
		if low == "" {
			continue
		}
		if low == em {
			found = true
			if !allow {
				continue
			}
		}
		next = append(next, low)
	}
	if allow && !found {
		next = append(next, em)
	}
	sort.Strings(next)
	a.cfg.App.AllowedImageSenders = next
	if a.opts.ConfigSaver != nil {
		cfg := a.cfg
		if a.opts.ConfigLoader != nil {
			if loaded, err := a.opts.ConfigLoader(); err == nil {
				cfg = loaded
			}
		}
		cfg.App.AllowedImageSenders = next
		cfg = config.Normalize(cfg)
		_ = a.opts.ConfigSaver(cfg)
		a.cfg = cfg
	}
}

func (a *App) readerRemoteImagesAllowedForMsg(msg *backend.MessageDetail) bool {
	if msg == nil {
		return false
	}
	if a.readerRemoteImagesAllowed(msg.ID) {
		return true
	}
	if a.senderRemoteImagesAllowed(msg.From) {
		a.setReaderRemoteImagesAllowed(msg.ID, true)
		return true
	}
	return false
}

func (a *App) readerRemoteImagesAllowed(msgID string) bool {
	if msgID == "" {
		return false
	}
	a.readerImgMu.RLock()
	ok := a.readerImgAllowed != nil && a.readerImgAllowed[msgID]
	a.readerImgMu.RUnlock()
	return ok
}

func (a *App) setReaderRemoteImagesAllowed(msgID string, allow bool) {
	if msgID == "" {
		return
	}
	a.readerImgMu.Lock()
	if a.readerImgAllowed == nil {
		a.readerImgAllowed = make(map[string]bool)
	}
	if allow {
		a.readerImgAllowed[msgID] = true
	} else {
		delete(a.readerImgAllowed, msgID)
		// Clear remote cache entries for this message so re-enabling starts fresh.
		if a.readerImgMsgID == msgID {
			for k := range a.readerImgCache {
				if strings.HasPrefix(strings.ToLower(k), "http://") || strings.HasPrefix(strings.ToLower(k), "https://") {
					delete(a.readerImgCache, k)
				}
			}
		}
	}
	a.readerImgMu.Unlock()
}

// validateImageBytes rejects SVG payloads and decompression bombs (huge width*height)
// before calling ui.DecodeImage.
func validateImageBytes(data []byte) error {
	if len(data) == 0 || len(data) > maxRemoteImageBytes {
		return fmt.Errorf("image size out of bounds")
	}
	trimmed := bytes.TrimSpace(data)
	head := bytes.ToLower(trimmed)
	if len(head) > 256 {
		head = head[:256]
	}
	if bytes.Contains(head, []byte("<svg")) || bytes.Contains(head, []byte("<!doctype svg")) {
		return fmt.Errorf("svg images are blocked")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("unsupported or corrupt image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxImageDimension || cfg.Height > maxImageDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return fmt.Errorf("image dimensions (%dx%d) exceed safety limit", cfg.Width, cfg.Height)
	}
	return nil
}

func (a *App) loadMailImage(rawURL string) (ui.ImageSource, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("empty image url")
	}

	a.readerImgMu.RLock()
	msgID := a.readerImgMsgID
	if a.readerImgCache != nil {
		if cached, ok := a.readerImgCache[rawURL]; ok {
			a.readerImgMu.RUnlock()
			return cached, nil
		}
	}
	a.readerImgMu.RUnlock()

	var imgBytes []byte
	isRemote := false
	low := strings.ToLower(rawURL)
	switch {
	case strings.HasPrefix(low, "cid:"):
		cid := strings.TrimSpace(rawURL[4:])
		if unesc, uerr := url.PathUnescape(cid); uerr == nil && unesc != "" {
			cid = unesc
		}
		if cid == "" || msgID == "" || a.opts.Direct == nil {
			return nil, fmt.Errorf("inline image unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, data, err := a.opts.Direct.ReadInlineImage(ctx, msgID, cid)
		if err != nil {
			return nil, err
		}
		imgBytes = data

	case strings.HasPrefix(low, "data:image/"):
		data, err := decodeDataImageURI(rawURL)
		if err != nil {
			return nil, err
		}
		imgBytes = data

	case strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://"):
		isRemote = true
		if !a.readerRemoteImagesAllowed(msgID) {
			return nil, fmt.Errorf("remote images blocked")
		}
		data, err := fetchRemoteImageSafe(rawURL)
		if err != nil {
			return nil, err
		}
		imgBytes = data

	default:
		return nil, fmt.Errorf("unsupported image scheme")
	}

	if err := validateImageBytes(imgBytes); err != nil {
		return nil, err
	}
	src, err := ui.DecodeImage(imgBytes)
	if err != nil {
		return nil, err
	}

	a.readerImgMu.Lock()
	if a.readerImgMsgID == msgID {
		if isRemote && (a.readerImgAllowed == nil || !a.readerImgAllowed[msgID]) {
			a.readerImgMu.Unlock()
			return nil, fmt.Errorf("remote images blocked")
		}
		if a.readerImgCache == nil {
			a.readerImgCache = make(map[string]ui.ImageSource)
		}
		if len(a.readerImgCache) < maxCachedImages {
			a.readerImgCache[rawURL] = src
		}
	}
	a.readerImgMu.Unlock()
	return src, nil
}

func decodeDataImageURI(raw string) ([]byte, error) {
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return nil, fmt.Errorf("invalid data URI")
	}
	meta := strings.ToLower(strings.TrimSpace(raw[:comma]))
	if !strings.HasPrefix(meta, "data:image/") || !strings.Contains(meta, ";base64") {
		return nil, fmt.Errorf("unsupported data URI encoding")
	}
	if strings.Contains(meta, "svg") {
		return nil, fmt.Errorf("svg data URI blocked")
	}
	payload := raw[comma+1:]
	if len(payload) > maxRemoteImageBytes*2 {
		return nil, fmt.Errorf("data URI too large")
	}
	// Strip RFC 2045 whitespace and percent-encoded spaces commonly embedded in HTML emails.
	payload = strings.ReplaceAll(payload, "%20", "")
	payload = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, payload)
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return nil, err
		}
	}
	if len(data) == 0 || len(data) > maxRemoteImageBytes {
		return nil, fmt.Errorf("data URI image out of bounds")
	}
	return data, nil
}

var blockedRemoteImagePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // RFC 1122 "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("100::/64"),      // Discard-Only
	netip.MustParsePrefix("2001:db8::/32"), // Documentation
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, pfx := range blockedRemoteImagePrefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

var remoteImageHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("resolve %q failed", host)
			}
			for _, ipAddr := range ips {
				if isPrivateOrLocalIP(ipAddr.IP) {
					return nil, fmt.Errorf("blocked private/local image host %q", host)
				}
			}
			d := &net.Dialer{Timeout: 8 * time.Second}
			var lastErr error
			for _, ipAddr := range ips {
				conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
				if derr == nil {
					return conn, nil
				}
				lastErr = derr
			}
			return nil, lastErr
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return fmt.Errorf("too many redirects")
		}
		if !isSafeWebURL(req.URL.String()) {
			return fmt.Errorf("unsafe redirect")
		}
		return nil
	},
}

func fetchRemoteImageSafe(rawURL string) ([]byte, error) {
	if !isSafeWebURL(rawURL) {
		return nil, fmt.Errorf("unsafe image URL")
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/*;q=0.8")
	resp, err := remoteImageHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if strings.Contains(ct, "svg") {
		return nil, fmt.Errorf("svg image blocked")
	}
	if ct != "" && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "application/octet-stream") {
		return nil, fmt.Errorf("non-image content-type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxRemoteImageBytes {
		return nil, fmt.Errorf("image exceeds 5 MB limit")
	}
	return data, nil
}

// --- Strict Allowlist HTML Sanitizer for Forwarding & Printing ---

var allowedEmailHTMLTags = map[atom.Atom]bool{
	atom.A: true, atom.Abbr: true, atom.Address: true, atom.Article: true,
	atom.B: true, atom.Bdi: true, atom.Bdo: true, atom.Big: true, atom.Blockquote: true, atom.Br: true,
	atom.Caption: true, atom.Center: true, atom.Cite: true, atom.Code: true, atom.Col: true, atom.Colgroup: true,
	atom.Dd: true, atom.Del: true, atom.Details: true, atom.Dfn: true, atom.Div: true, atom.Dl: true, atom.Dt: true,
	atom.Em:         true,
	atom.Figcaption: true, atom.Figure: true, atom.Font: true, atom.Footer: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true, atom.Header: true, atom.Hr: true,
	atom.I: true, atom.Img: true, atom.Ins: true,
	atom.Kbd:  true,
	atom.Li:   true,
	atom.Main: true, atom.Mark: true,
	atom.Nav: true,
	atom.Ol:  true,
	atom.P:   true, atom.Pre: true,
	atom.Q: true,
	atom.S: true, atom.Samp: true, atom.Section: true, atom.Small: true, atom.Span: true, atom.Strike: true, atom.Strong: true, atom.Sub: true, atom.Summary: true, atom.Sup: true,
	atom.Table: true, atom.Tbody: true, atom.Td: true, atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Time: true, atom.Tr: true, atom.Tt: true,
	atom.U: true, atom.Ul: true,
	atom.Var: true,
	atom.Wbr: true,
}

// dropSubtreeHTMLTags are foreign/executable/active elements whose entire subtree must be discarded.
var dropSubtreeHTMLTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Svg: true, atom.Math: true,
	atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Applet: true,
	atom.Video: true, atom.Audio: true, atom.Source: true, atom.Picture: true,
	atom.Form: true, atom.Input: true, atom.Button: true, atom.Select: true, atom.Textarea: true,
	atom.Meta: true, atom.Base: true, atom.Link: true, atom.Title: true, atom.Head: true,
	atom.Frameset: true, atom.Frame: true, atom.Noscript: true, atom.Template: true,
	atom.Canvas: true, atom.Map: true, atom.Area: true,
}

var allowedCommonHTMLAttrs = map[string]bool{
	"align": true, "valign": true, "width": true, "height": true,
	"colspan": true, "rowspan": true, "cellpadding": true, "cellspacing": true, "border": true,
	"bgcolor": true, "color": true, "face": true, "size": true,
	"dir": true, "lang": true, "title": true, "start": true, "type": true,
}

// sanitizeInlineStyle strips CSS declarations that can load remote resources,
// execute scripts, or spoof/overlay UI elements.
func sanitizeInlineStyle(raw string) string {
	var safe []string
	for _, decl := range strings.Split(raw, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		p := strings.ToLower(strings.TrimSpace(prop))
		v := strings.TrimSpace(val)
		vLow := strings.ToLower(v)
		if p == "" || v == "" {
			continue
		}
		// Reject CSS escape sequences, remote URL loads, legacy script expressions, and overlay positioning.
		if strings.Contains(vLow, "url(") || strings.Contains(vLow, "@import") ||
			strings.Contains(vLow, "expression(") || strings.Contains(vLow, "javascript:") ||
			strings.Contains(vLow, "vbscript:") || strings.Contains(vLow, "behavior") ||
			strings.Contains(vLow, "-moz-binding") || strings.Contains(vLow, "\\") {
			continue
		}
		if p == "position" && (strings.Contains(vLow, "fixed") || strings.Contains(vLow, "sticky") || strings.Contains(vLow, "absolute")) {
			continue
		}
		if p == "z-index" || strings.HasPrefix(p, "background-image") || p == "list-style-image" || p == "content" || p == "behavior" || p == "-moz-binding" {
			continue
		}
		safe = append(safe, p+": "+v)
	}
	return strings.Join(safe, "; ")
}

// SanitizeEmailHTML cleans an email HTML body using a strict tag & attribute allowlist.
// Scripts, styles, SVGs, MathML, media, forms, event handlers, CSS url()/position overlays,
// srcset/poster/background attributes, and unsafe links are stripped.
// When inlineImgs is provided, cid: image references are rewritten to safe raster data: URIs.
// When blockRemoteImages is true, external http(s) <img src> URLs are stripped.
func SanitizeEmailHTML(raw string, inlineImgs map[string]mailengine.InlineImage, blockRemoteImages bool) string {
	doc, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return "<pre>" + html.EscapeString(raw) + "</pre>"
	}
	// Find <body> if present so we emit clean inner HTML.
	var body *xhtml.Node
	var findBody func(*xhtml.Node)
	findBody = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.DataAtom == atom.Body {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			findBody(c)
		}
	}
	findBody(doc)
	root := body
	if root == nil {
		root = doc
	}

	var clean func(*xhtml.Node)
	clean = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == xhtml.CommentNode {
				n.RemoveChild(c)
				c = next
				continue
			}
			if c.Type == xhtml.ElementNode {
				// Discard foreign namespaces (SVG/MathML) and dangerous subtrees completely.
				if c.Namespace != "" || dropSubtreeHTMLTags[c.DataAtom] {
					n.RemoveChild(c)
					c = next
					continue
				}
				// Unwrap unknown non-allowlisted tags (e.g. Outlook <o:p>) while still sanitizing their children.
				if !allowedEmailHTMLTags[c.DataAtom] {
					clean(c)
					for ch := c.FirstChild; ch != nil; {
						chNext := ch.NextSibling
						c.RemoveChild(ch)
						n.InsertBefore(ch, c)
						ch = chNext
					}
					n.RemoveChild(c)
					c = next
					continue
				}
				// Strict attribute allowlist.
				filtered := make([]xhtml.Attribute, 0, len(c.Attr))
				dropImg := false
				hasSafeImgSrc := false
				for _, attr := range c.Attr {
					if attr.Namespace != "" {
						continue // drop xlink:href, xml:*, etc.
					}
					k := strings.ToLower(strings.TrimSpace(attr.Key))
					switch {
					case k == "href" && c.DataAtom == atom.A:
						if safe := safeHref(attr.Val); safe != "" {
							attr.Val = safe
							filtered = append(filtered, attr)
						}
					case k == "src" && c.DataAtom == atom.Img:
						v := strings.TrimSpace(attr.Val)
						low := strings.ToLower(v)
						switch {
						case strings.HasPrefix(low, "cid:"):
							cid := strings.Trim(strings.TrimSpace(v[4:]), "<>")
							if unesc, uerr := url.PathUnescape(cid); uerr == nil && unesc != "" {
								cid = unesc
							}
							cid = strings.ToLower(strings.Trim(strings.TrimSpace(cid), "<>"))
							if img, ok := inlineImgs[cid]; ok && len(img.Data) > 0 && !strings.Contains(strings.ToLower(img.ContentType), "svg") {
								attr.Val = "data:" + img.ContentType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
								filtered = append(filtered, attr)
								hasSafeImgSrc = true
							} else {
								dropImg = true
							}
						case strings.HasPrefix(low, "data:image/"):
							if strings.Contains(low, "svg") {
								dropImg = true
							} else if _, derr := decodeDataImageURI(v); derr == nil {
								filtered = append(filtered, attr)
								hasSafeImgSrc = true
							} else {
								dropImg = true
							}
						case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
							if blockRemoteImages || !isSafeWebURL(v) {
								dropImg = true
							} else {
								filtered = append(filtered, attr)
								hasSafeImgSrc = true
							}
						default:
							dropImg = true
						}
					case k == "alt" && c.DataAtom == atom.Img:
						filtered = append(filtered, attr)
					case k == "style":
						if st := sanitizeInlineStyle(attr.Val); st != "" {
							attr.Val = st
							filtered = append(filtered, attr)
						}
					case allowedCommonHTMLAttrs[k]:
						filtered = append(filtered, attr)
					}
				}
				if c.DataAtom == atom.Img && (dropImg || !hasSafeImgSrc) {
					n.RemoveChild(c)
					c = next
					continue
				}
				c.Attr = filtered
			}
			clean(c)
			c = next
		}
	}
	clean(root)

	var buf bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		_ = xhtml.Render(&buf, c)
	}
	return buf.String()
}

// BuildForwardHTML builds a clean HTML alternative for forwarding an HTML email,
// placing the user's top text above a sanitized copy of the original HTML message.
func BuildForwardHTML(userTopText, fwdTitle, from, dateStr, subject, origHTML string, inlineImgs map[string]mailengine.InlineImage) string {
	if len(origHTML) > maxForwardOrigHTML {
		origHTML = origHTML[:maxForwardOrigHTML]
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:14px;line-height:1.5;">`)
	if top := strings.TrimSpace(userTopText); top != "" {
		b.WriteString("<div>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(top), "\n", "<br>"))
		b.WriteString("</div><br>")
	}
	b.WriteString(`<div style="border-top:1px solid #d0d7de;padding-top:10px;margin-top:10px;color:#57606a;font-size:12px;">`)
	fmt.Fprintf(&b, "<strong>%s</strong><br>", html.EscapeString(fwdTitle))
	fmt.Fprintf(&b, "From: %s<br>", html.EscapeString(from))
	if dateStr != "" {
		fmt.Fprintf(&b, "Date: %s<br>", html.EscapeString(dateStr))
	}
	fmt.Fprintf(&b, "Subject: %s", html.EscapeString(subject))
	b.WriteString(`</div><div style="margin-top:12px;">`)
	b.WriteString(SanitizeEmailHTML(origHTML, inlineImgs, false))
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// BuildPrintHTML creates a self-contained, CSP-hardened, printer-friendly HTML page for a message.
func BuildPrintHTML(msg *backend.MessageDetail, lang string, inlineImgs map[string]mailengine.InlineImage, allowRemote bool, autoPrintScript bool) string {
	if msg == nil {
		return ""
	}
	tr := func(de, en string) string {
		if lang == "en" {
			return en
		}
		return de
	}
	subj := strings.TrimSpace(msg.Subject)
	if subj == "" {
		subj = tr("(Kein Betreff)", "(No Subject)")
	}
	imgSrc := "data:"
	if allowRemote {
		imgSrc = "data: https: http:"
	}
	scriptSrc := "'none'"
	if autoPrintScript {
		scriptSrc = autoPrintScriptCSPHash
	}
	csp := fmt.Sprintf("default-src 'none'; img-src %s; style-src 'unsafe-inline'; font-src 'none'; connect-src 'none'; script-src %s; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'", imgSrc, scriptSrc)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="`)
	b.WriteString(csp)
	b.WriteString(`"><title>`)
	b.WriteString(html.EscapeString(subj))
	b.WriteString(`</title><style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#111;background:#fff;margin:28px auto;max-width:760px;line-height:1.5;font-size:13.5px;}
header{border-bottom:1px solid #ccc;padding-bottom:12px;margin-bottom:18px;}
h1{font-size:18px;margin:0 0 8px 0;}
.meta{font-size:12px;color:#444;margin:2px 0;}
.meta strong{color:#111;}
.body{word-wrap:break-word;overflow-wrap:anywhere;}
.body img{max-width:100%;height:auto;}
.body pre{white-space:pre-wrap;font-family:ui-monospace,Menlo,monospace;font-size:12.5px;}
.atts{margin-top:20px;padding-top:10px;border-top:1px solid #ddd;font-size:12px;color:#444;}
@media print{body{margin:0;max-width:none;}}
</style></head><body><header><h1>`)
	b.WriteString(html.EscapeString(subj))
	b.WriteString(`</h1>`)
	fmt.Fprintf(&b, `<div class="meta"><strong>%s</strong> %s</div>`, tr("Von:", "From:"), html.EscapeString(msg.From))
	if msg.Date != "" {
		fmt.Fprintf(&b, `<div class="meta"><strong>%s</strong> %s</div>`, tr("Datum:", "Date:"), html.EscapeString(formatDateTimeLang(msg.Date, lang)))
	}
	if len(msg.To) > 0 {
		fmt.Fprintf(&b, `<div class="meta"><strong>%s</strong> %s</div>`, tr("An:", "To:"), html.EscapeString(strings.Join(msg.To, ", ")))
	}
	if len(msg.CC) > 0 {
		fmt.Fprintf(&b, `<div class="meta"><strong>CC:</strong> %s</div>`, html.EscapeString(strings.Join(msg.CC, ", ")))
	}
	b.WriteString(`</header><main class="body">`)
	if strings.TrimSpace(msg.BodyHtml) != "" {
		b.WriteString(SanitizeEmailHTML(msg.BodyHtml, inlineImgs, !allowRemote))
	} else {
		b.WriteString("<pre>")
		b.WriteString(html.EscapeString(msg.BodyText))
		b.WriteString("</pre>")
	}
	b.WriteString(`</main>`)
	if len(msg.Attachments) > 0 {
		b.WriteString(`<footer class="atts"><strong>`)
		b.WriteString(tr("Anhänge:", "Attachments:"))
		b.WriteString(`</strong> `)
		names := make([]string, 0, len(msg.Attachments))
		for _, att := range msg.Attachments {
			names = append(names, html.EscapeString(att.Name))
		}
		b.WriteString(strings.Join(names, " · "))
		b.WriteString(`</footer>`)
	}
	if autoPrintScript {
		b.WriteString(autoPrintScriptBlock)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// ensureAutoPrintScript adds the hashed auto-print script (and updates the CSP script-src hash)
// when falling back from native WebView printing to the external system browser.
func ensureAutoPrintScript(htmlDoc string) string {
	if strings.Contains(htmlDoc, autoPrintScriptBlock) {
		return htmlDoc
	}
	doc := strings.Replace(htmlDoc, "script-src 'none'", "script-src "+autoPrintScriptCSPHash, 1)
	if idx := strings.LastIndex(doc, "</body>"); idx >= 0 {
		return doc[:idx] + autoPrintScriptBlock + doc[idx:]
	}
	return doc + autoPrintScriptBlock
}

// --- Undo state ---

type undoAction struct {
	Kind      string // "move" | "read" | "flag"
	Label     string
	CreatedAt time.Time
	// For "move" (Archive, Move to folder, non-permanent Trash):
	MoveItems []backend.UndoMoveItem
	// For "read" / "flag":
	ToggleItems []backend.BatchActionItem
	PrevValue   bool
}

// --- Alias parsing & formatting for DirectAccount form ---

func formatAliasesInput(aliases []config.AccountAlias) string {
	if len(aliases) == 0 {
		return ""
	}
	lines := make([]string, 0, len(aliases))
	for _, al := range aliases {
		if al.Email == "" {
			continue
		}
		lines = append(lines, formatEmailAddress(al.DisplayName, al.Email))
	}
	return strings.Join(lines, ", ")
}

func parseSingleAliasToken(part string) (config.AccountAlias, bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return config.AccountAlias{}, false
	}
	if a, err := mail.ParseAddress(part); err == nil && strings.TrimSpace(a.Address) != "" {
		return config.AccountAlias{Email: strings.TrimSpace(a.Address), DisplayName: strings.TrimSpace(a.Name)}, true
	}
	// Handle unquoted display names containing dots (e.g. "Dr. Max Mustermann <max@firma.de>").
	if lt := strings.LastIndexByte(part, '<'); lt >= 0 {
		if gt := strings.LastIndexByte(part, '>'); gt > lt {
			addrRaw := strings.TrimSpace(part[lt+1 : gt])
			nameRaw := strings.Trim(strings.TrimSpace(part[:lt]), `"`)
			if a, err := mail.ParseAddress(addrRaw); err == nil && strings.TrimSpace(a.Address) != "" {
				return config.AccountAlias{Email: strings.TrimSpace(a.Address), DisplayName: nameRaw}, true
			}
		}
	}
	if strings.Contains(part, "@") && !strings.ContainsAny(part, " \t<>") {
		return config.AccountAlias{Email: part}, true
	}
	return config.AccountAlias{}, false
}

func parseAliasesInput(raw string) []config.AccountAlias {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Accept newline- or comma-separated entries (including "Dr. Max <alias@domain.de>").
	normalized := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", ", ")
	if addrs, err := mail.ParseAddressList(normalized); err == nil && len(addrs) > 0 {
		out := make([]config.AccountAlias, 0, len(addrs))
		for _, a := range addrs {
			if strings.TrimSpace(a.Address) == "" {
				continue
			}
			out = append(out, config.AccountAlias{
				Email:       strings.TrimSpace(a.Address),
				DisplayName: strings.TrimSpace(a.Name),
			})
		}
		return out
	}
	// Fallback split by comma outside quotes/angle brackets so "Dr. Max <a@b.de>, sales@b.de" parses cleanly.
	var out []config.AccountAlias
	var cur strings.Builder
	inQuote := false
	inAngle := false
	flush := func() {
		if al, ok := parseSingleAliasToken(cur.String()); ok {
			out = append(out, al)
		}
		cur.Reset()
	}
	for i := 0; i < len(normalized); i++ {
		ch := normalized[i]
		switch ch {
		case '"':
			inQuote = !inQuote
			cur.WriteByte(ch)
		case '<':
			if !inQuote {
				inAngle = true
			}
			cur.WriteByte(ch)
		case '>':
			if !inQuote {
				inAngle = false
			}
			cur.WriteByte(ch)
		case ',':
			if !inQuote && !inAngle {
				flush()
				continue
			}
			cur.WriteByte(ch)
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return out
}

func cleanupStalePrintFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-printFallbackTTL)
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasPrefix(ent.Name(), "maildiver-print-") {
			continue
		}
		if info, err := ent.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, ent.Name()))
		}
	}
}

// openPrintHTMLFallback writes a self-contained print-ready HTML document to a private 0700 temp dir,
// injects the auto-print script if needed, opens it in the system browser, and cleans up stale files.
func openPrintHTMLFallback(htmlDoc string) error {
	dir := filepath.Join(os.TempDir(), "maildiver-print")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	cleanupStalePrintFiles(dir)

	doc := ensureAutoPrintScript(htmlDoc)
	f, err := os.CreateTemp(dir, "maildiver-print-*.html")
	if err != nil {
		return err
	}
	path := f.Name()
	_ = f.Chmod(0o600)
	if _, err := f.WriteString(doc); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	// Best-effort delayed cleanup after browser has opened the print page.
	time.AfterFunc(printFallbackTTL, func() { _ = os.Remove(path) })
	return openLocalFile(path)
}
