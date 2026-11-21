package nativeui

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/diver80/maildiver/internal/backend"
)

// Mail body
// ---------
// The reading pane shows a mail as Markdown in a markdown.View: it sizes to its content, keeps
// the text selectable (so single-key shortcuts keep working), opens only http/https/mailto links
// on click, shows a link's address on hover, and loads nothing from the network.
//
// HTML mails are converted here rather than with richtext.ParseHTML, which turns every <br> into
// a paragraph of its own. This converter is made for mail:
//   - it keeps line breaks and drops what the mail hides (preheaders), scripts and styles;
//   - images are never loaded: tracking pixels vanish, other images are counted for a note, and
//     an image inside a link leaves its alt text so the link stays clickable;
//   - layout tables are flattened, a row of short cells becomes one line;
//   - a link whose text is its own URL gets a short label (host/…), and bare URLs in text become
//     such links too.
//
// Every character of the sender's text is escaped, so nothing in a mail becomes Markdown or HTML
// formatting; the converter itself only emits links and the inline tags <b>, <i>, <code>, <br>.
// Plain-text mails go through the same writer: URLs become short links, "> " lines quotes.

// maxBodyHTMLBytes caps the HTML converted; larger mails show their text version.
const maxBodyHTMLBytes = 2 << 20

// readerBody is the converted body of the open mail.
type readerBody struct {
	MsgID         string
	Markdown      string // what the pane shows by default
	Plain         string // the text version as Markdown
	FromHTML      bool   // Markdown was made from the HTML part
	BlockedImages int    // remote images currently withheld; tracking pixels and cid: images are not counted
	RemoteImages  int    // total non-pixel remote http(s) images in this mail
	ShowRemote    bool   // whether Markdown was built with remote images enabled
}

func buildReaderBody(msg *backend.MessageDetail) readerBody {
	return buildReaderBodyWithImages(msg, false)
}

func buildReaderBodyWithImages(msg *backend.MessageDetail, showRemote bool) readerBody {
	b := readerBody{MsgID: msg.ID, Plain: plainMailMarkdown(msg.BodyText), ShowRemote: showRemote}
	b.Markdown = b.Plain
	if h := msg.BodyHtml; strings.TrimSpace(h) != "" && len(h) <= maxBodyHTMLBytes {
		if md, blocked, remote := htmlMailMarkdownWithImages(h, showRemote); strings.TrimSpace(md) != "" {
			b.Markdown, b.FromHTML, b.BlockedImages, b.RemoteImages = md, true, blocked, remote
		}
	}
	return b
}

// mdWriter builds Markdown from a mail's blocks and inline text.
type mdWriter struct {
	out  strings.Builder    // finished blocks
	bufs []*strings.Builder // inline buffers: bufs[0] the block's, more inside a link or <b>

	space    bool // whitespace seen since the last character written
	lineText bool // the current block has text
	collapse bool // collapse whitespace (HTML); plain text keeps it

	quote   int // block quote depth
	heading int // the current block is a heading of this Markdown level
	item    bool
	mark    string // the current list item's marker
	lists   []mdList
	inItem  int // depth of <li> elements around
	link    int // depth of links around
	pre     int

	listSeq    int // numbers the lists, so that two lists in a row stay apart
	itemRoot   int // the outermost list of the item being written
	lastRoot   int
	lastItem   bool
	lastQuote  int
	lastRule   bool
	ruleAt     int // where the last rule's block starts in out
	blocked    int
	remoteImgs int
	showRemote bool

	// Where the last link ended: a link right after it (whitespace aside) gets " · " between
	// them, so that "Manage Notifications Give feedback" stays two links.
	linkBuf *strings.Builder
	linkEnd int
}

type mdList struct {
	ordered bool
	n       int
	id      int
}

func newMDWriter(collapse bool) *mdWriter {
	return &mdWriter{bufs: []*strings.Builder{{}}, collapse: collapse}
}

func (m *mdWriter) cur() *strings.Builder { return m.bufs[len(m.bufs)-1] }
func (m *mdWriter) inline() bool          { return len(m.bufs) > 1 }

func (m *mdWriter) push() { m.bufs = append(m.bufs, &strings.Builder{}) }

func (m *mdWriter) pop() string {
	s := m.cur().String()
	m.bufs = m.bufs[:len(m.bufs)-1]
	return s
}

// invisibleRune reports characters mails use as filler (preheader padding, tracking markers).
func invisibleRune(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff', '\u034f', '\u00ad', '\u180e':
		return true
	}
	return r < ' ' && r != '\n' && r != '\t' && r != '\r'
}

// mdEscapable are the ASCII characters that could start Markdown or HTML syntax with the
// extensions the body view turns on (bodyMarkdownOpts: strikethrough only).
const mdEscapable = "\\`*_{}[]<>#+-.!|~&$"

func writeEscaped(b *strings.Builder, r rune) {
	if r < utf8.RuneSelf && strings.ContainsRune(mdEscapable, r) {
		b.WriteByte('\\')
	}
	b.WriteRune(r)
}

func escapeMarkdown(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !invisibleRune(r) {
			writeEscaped(&b, r)
		}
	}
	return b.String()
}

// textRun writes text, escaped, collapsing whitespace when the writer does.
func (m *mdWriter) textRun(s string) {
	b := m.cur()
	for _, r := range s {
		if invisibleRune(r) {
			continue
		}
		if m.collapse && unicode.IsSpace(r) {
			m.space = true
			continue
		}
		m.spaceBefore()
		if !m.collapse && (r == ' ' || r == '\t') && m.atLineStart() {
			// Keep a plain-text line's indentation: Markdown drops leading spaces.
			if r == '\t' {
				b.WriteString("\u00a0\u00a0\u00a0\u00a0")
			} else {
				b.WriteRune('\u00a0')
			}
			continue
		}
		writeEscaped(b, r)
		m.lineText = true
	}
}

// atLineStart reports that the current line has no text yet (indentation aside).
func (m *mdWriter) atLineStart() bool {
	s := strings.TrimRight(m.cur().String(), "\u00a0")
	return !m.lineText || strings.HasSuffix(s, "<br>")
}

// spaceBefore writes the collapsed whitespace before the next character, unless the block or the
// line has just begun.
func (m *mdWriter) spaceBefore() {
	if m.space && m.lineText && !strings.HasSuffix(m.cur().String(), "<br>") {
		m.cur().WriteByte(' ')
	}
	m.space = false
}

// urlRe finds URLs in text, with the brackets some mails put around them ("[https://…]").
var urlRe = regexp.MustCompile(`(?i)([\[<]?)((?:https?://|www\.)[^\s<>"\[\]{}|\\^` + "`" + `]+)([\]>]?)`)

// text writes text with its URLs as short links.
func (m *mdWriter) text(s string) {
	if m.link > 0 {
		m.textRun(s)
		return
	}
	last := 0
	for _, loc := range urlRe.FindAllStringSubmatchIndex(s, -1) {
		open, raw, closing := s[loc[2]:loc[3]], s[loc[4]:loc[5]], s[loc[6]:loc[7]]
		trimmed := trimURLTail(raw)
		// "[https://…]" and "<https://…>" lose their brackets; a lone bracket stays text.
		paired := trimmed == raw && ((open == "[" && closing == "]") || (open == "<" && closing == ">"))
		start, end := loc[4], loc[4]+len(trimmed)
		if paired {
			start, end = loc[2], loc[7]
		}
		m.textRun(s[last:start])
		href := trimmed
		if strings.HasPrefix(strings.ToLower(href), "www.") {
			href = "https://" + href
		}
		m.writeLink(escapeMarkdown(shortURL(href)), href, m.space && m.lineText)
		last = end
	}
	m.textRun(s[last:])
}

// trimURLTail drops punctuation that ends the sentence around a URL, keeping a closing parenthesis
// the URL opened.
func trimURLTail(u string) string {
	for len(u) > 0 {
		c := u[len(u)-1]
		if strings.IndexByte(".,;:!?'\"*", c) >= 0 {
			u = u[:len(u)-1]
			continue
		}
		if c == ')' && strings.Count(u, "(") < strings.Count(u, ")") {
			u = u[:len(u)-1]
			continue
		}
		break
	}
	return u
}

// writeLink writes a Markdown link, after a space when lead (not at the start of a line), or after
// " · " when only whitespace separates it from the link before.
func (m *mdWriter) writeLink(label, href string, lead bool) {
	if label == "" {
		return
	}
	b := m.cur()
	switch {
	case m.linkBuf == b && b.Len() == m.linkEnd && m.lineText:
		b.WriteString(" · ")
	case lead && m.lineText && !strings.HasSuffix(b.String(), "<br>"):
		b.WriteByte(' ')
	}
	m.space = false
	b.WriteString("[" + label + "](" + markdownDest(href) + ")")
	m.lineText = true
	m.linkBuf, m.linkEnd = b, b.Len()
}

// markdownDest encodes what would end or break a link destination.
func markdownDest(u string) string {
	r := strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\\", "%5C", "\n", "", "\r", "", "\t", "")
	return r.Replace(u)
}

// safeHref returns href when a click may open it (http, https, mailto), else "".
func safeHref(href string) string {
	h := strings.TrimFunc(href, func(r rune) bool { return r <= ' ' })
	if strings.ContainsAny(h, "\n\r\t") {
		return ""
	}
	lower := strings.ToLower(h)
	for _, p := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(lower, p) && len(h) > len(p) {
			return h
		}
	}
	return ""
}

// shortURL is the label of a link whose text is its address: the host, with "/…" when the
// address goes further.
func shortURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return truncateRunes(raw, 48)
	}
	if strings.EqualFold(u.Scheme, "mailto") {
		return truncateRunes(u.Opaque, 64)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == "" {
		return truncateRunes(raw, 48)
	}
	host = truncateRunes(host, 48)
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return host + "/…"
	}
	return host
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "www.")
}

// lineBreak ends a line within the block (<br>), at most one empty line in a row.
func (m *mdWriter) lineBreak() {
	if !m.lineText {
		return
	}
	if m.inline() {
		m.space = true
		return
	}
	b := m.cur()
	if !strings.HasSuffix(b.String(), "<br><br>") {
		b.WriteString("<br>")
	}
	m.space = false
}

// trimBreaks strips spaces and line breaks around a block's text (indentation, as non-breaking
// spaces, stays).
func trimBreaks(s string) string {
	for {
		t := strings.Trim(strings.TrimPrefix(strings.TrimSuffix(s, "<br>"), "<br>"), " ")
		if t == s {
			return s
		}
		s = t
	}
}

// flush ends the current block and writes it.
func (m *mdWriter) flush() {
	s := trimBreaks(m.bufs[0].String())
	m.bufs = m.bufs[:1]
	m.bufs[0].Reset()
	heading, item, mark := m.heading, m.item, m.mark
	m.heading, m.item, m.mark = 0, false, ""
	m.space, m.lineText = false, false
	if s == "" {
		return
	}
	prefix := strings.Repeat("> ", m.quote)
	var line string
	switch {
	case heading > 0:
		line = prefix + strings.Repeat("#", heading) + " " + s
	case item:
		line = prefix + strings.Repeat("    ", max(len(m.lists)-1, 0)) + mark + s
	default:
		line = prefix + s
	}
	m.writeBlock(line, item)
}

func (m *mdWriter) writeBlock(line string, item bool) {
	m.ruleAt = m.out.Len()
	if m.out.Len() > 0 {
		switch {
		case item && m.lastItem && m.quote == m.lastQuote && m.itemRoot == m.lastRoot:
			m.out.WriteString("\n") // a tight list
		case m.quote > 0 && m.quote == m.lastQuote:
			m.out.WriteString("\n" + strings.TrimSpace(strings.Repeat("> ", m.quote)) + "\n")
		default:
			m.out.WriteString("\n\n")
		}
	}
	m.out.WriteString(line)
	m.lastItem, m.lastQuote, m.lastRule, m.lastRoot = item, m.quote, false, m.itemRoot
}

func (m *mdWriter) rule() {
	m.flush()
	if m.out.Len() == 0 || m.lastRule {
		return
	}
	m.writeBlock(strings.Repeat("> ", m.quote)+"---", false)
	m.lastRule = true
}

func (m *mdWriter) result() string {
	m.flush()
	s := m.out.String()
	if m.lastRule {
		s = s[:m.ruleAt] // a rule closing the mail separates nothing
	}
	return s
}

// HTML
// ----

var skipTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Title: true, atom.Noscript: true,
	atom.Template: true, atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Svg: true,
	atom.Math: true, atom.Canvas: true, atom.Video: true, atom.Audio: true, atom.Select: true,
	atom.Textarea: true, atom.Input: true, atom.Map: true, atom.Meta: true, atom.Link: true,
	atom.Base: true, atom.Frameset: true, atom.Frame: true,
}

var blockTags = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Header: true,
	atom.Footer: true, atom.Main: true, atom.Aside: true, atom.Nav: true, atom.Figure: true,
	atom.Figcaption: true, atom.Address: true, atom.Center: true, atom.Table: true, atom.Thead: true,
	atom.Tbody: true, atom.Tfoot: true, atom.Tr: true, atom.Td: true, atom.Th: true, atom.Caption: true,
	atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Ul: true, atom.Ol: true, atom.Li: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Blockquote: true, atom.Pre: true, atom.Hr: true, atom.Form: true, atom.Fieldset: true,
	atom.Details: true, atom.Summary: true, atom.Body: true, atom.Html: true,
}

func attrOf(n *xhtml.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func attrVal(n *xhtml.Node, key string) string {
	v, _ := attrOf(n, key)
	return v
}

// normStyle returns a style attribute lower-cased without whitespace ("display:none;…").
func normStyle(n *xhtml.Node) string {
	s := attrVal(n, "style")
	if s == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// hiddenStyleRe matches the styles mails hide preheaders with. font-size:0 is left out: layouts
// put it on containers whose columns set their own size.
var hiddenStyleRe = regexp.MustCompile(`(^|;)(display:none|visibility:hidden|mso-hide:all|max-height:0(px|em|pt|%)?(;|!|$)|opacity:0(\.0+)?(;|!|$))`)

// hiddenNode reports what a mail shows nobody: preheaders and other hidden text.
func hiddenNode(n *xhtml.Node) bool {
	if _, ok := attrOf(n, "hidden"); ok {
		return true
	}
	if st := normStyle(n); st != "" && hiddenStyleRe.MatchString(st) {
		return true
	}
	for _, cls := range strings.Fields(strings.ToLower(attrVal(n, "class"))) {
		if strings.Contains(cls, "preheader") {
			return true
		}
	}
	return false
}

func hasBlockDescendant(n *xhtml.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode && (blockTags[c.DataAtom] || hasBlockDescendant(c)) {
			return true
		}
	}
	return false
}

func textContent(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// pixelImage reports tracking pixels and spacers (at most 3 px in a dimension).
func pixelImage(n *xhtml.Node) bool {
	small := func(v string) bool {
		v = strings.TrimSuffix(strings.TrimSpace(v), "px")
		f, err := strconv.ParseFloat(v, 64)
		return err == nil && f <= 3
	}
	if small(attrVal(n, "width")) || small(attrVal(n, "height")) {
		return true
	}
	st := normStyle(n)
	for _, key := range []string{"width:", "height:"} {
		if i := strings.Index(st, key); i >= 0 {
			v := st[i+len(key):]
			if j := strings.IndexByte(v, ';'); j >= 0 {
				v = v[:j]
			}
			if small(v) {
				return true
			}
		}
	}
	return false
}

// htmlMailMarkdown converts a mail's HTML (with remote images blocked) and counts the remote
// images left out. Embedded cid: and data:image/ images are always included.
func htmlMailMarkdown(src string) (string, int) {
	md, blocked, _ := htmlMailMarkdownWithImages(src, false)
	return md, blocked
}

func htmlMailMarkdownWithImages(src string, showRemote bool) (string, int, int) {
	root, err := xhtml.Parse(strings.NewReader(src))
	if err != nil {
		return "", 0, 0
	}
	m := newMDWriter(true)
	m.showRemote = showRemote
	m.node(root)
	return m.result(), m.blocked, m.remoteImgs
}

func (m *mdWriter) children(n *xhtml.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		m.node(c)
	}
}

func (m *mdWriter) node(n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		m.text(n.Data)
		return
	case xhtml.DocumentNode:
		m.children(n)
		return
	case xhtml.ElementNode:
	default:
		return
	}
	if skipTags[n.DataAtom] || hiddenNode(n) {
		return
	}
	switch n.DataAtom {
	case atom.Br:
		m.lineBreak()
		return
	case atom.Img:
		m.image(n)
		return
	case atom.A:
		m.anchor(n)
		return
	case atom.B, atom.Strong:
		m.wrap(n, "<b>", "</b>")
		return
	case atom.I, atom.Em, atom.Cite, atom.Dfn:
		m.wrap(n, "<i>", "</i>")
		return
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		m.wrap(n, "<code>", "</code>")
		return
	case atom.S, atom.Strike, atom.Del:
		m.wrap(n, "<s>", "</s>")
		return
	}
	if blockTags[n.DataAtom] {
		m.block(n)
		return
	}
	if st := normStyle(n); st != "" {
		if open, closing := styleTags(st); open != "" {
			m.wrap(n, open, closing)
			return
		}
	}
	m.children(n)
}

var boldStyleRe = regexp.MustCompile(`(^|;)font-weight:(bold|bolder|[6-9]00)`)

var strikeStyleRe = regexp.MustCompile(`(^|;)text-decoration(-line)?:[^;]*line-through`)

// styleTags returns the inline tags a span's style asks for (bold, italic, struck through).
func styleTags(st string) (string, string) {
	open, closing := "", ""
	if boldStyleRe.MatchString(st) {
		open, closing = "<b>", "</b>"
	}
	if strings.Contains(st, "font-style:italic") {
		open, closing = open+"<i>", "</i>"+closing
	}
	if strikeStyleRe.MatchString(st) {
		open, closing = open+"<s>", "</s>"+closing
	}
	return open, closing
}

// wrap writes n's content between inline tags; content with blocks is written without them.
func (m *mdWriter) wrap(n *xhtml.Node, open, closing string) {
	if hasBlockDescendant(n) {
		m.children(n)
		return
	}
	m.push()
	m.children(n)
	inner := m.pop()
	core := strings.TrimSpace(inner)
	if core == "" || core == "<br>" {
		m.cur().WriteString(inner)
		return
	}
	if strings.HasPrefix(inner, " ") {
		m.cur().WriteByte(' ')
	}
	m.cur().WriteString(open + core + closing)
}

func (m *mdWriter) anchor(n *xhtml.Node) {
	href := safeHref(attrVal(n, "href"))
	if href == "" || m.link > 0 {
		m.children(n)
		return
	}
	pending, hadText := m.space, m.lineText
	m.space = false
	m.push()
	m.link++
	m.children(n)
	m.link--
	label := trimBreaks(m.pop())
	if label == "" {
		m.space = pending || m.space
		return
	}
	if looksLikeURL(textContent(n)) {
		label = escapeMarkdown(shortURL(href))
	}
	m.writeLink(label, href, pending && hadText)
}

func (m *mdWriter) writeImage(alt, src string) {
	b := m.cur()
	m.spaceBefore()
	b.WriteString("![" + escapeMarkdown(alt) + "](" + markdownDest(src) + ")")
	m.lineText = true
	m.space = true
}

func (m *mdWriter) image(n *xhtml.Node) {
	src := strings.TrimSpace(attrVal(n, "src"))
	alt := strings.TrimSpace(attrVal(n, "alt"))
	low := strings.ToLower(src)
	isEmbedded := strings.HasPrefix(low, "cid:") || strings.HasPrefix(low, "data:image/")
	isRemote := strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")

	if isEmbedded && !pixelImage(n) {
		if m.link > 0 && alt != "" {
			m.text(alt)
			return
		}
		m.writeImage(alt, src)
		return
	}

	if m.link > 0 {
		if alt != "" {
			m.text(alt)
			return
		}
	}
	// An image between words (an arrow in "old → new", an icon) keeps them apart.
	m.space = true
	if pixelImage(n) || src == "" {
		return
	}
	if isRemote {
		m.remoteImgs++
		if m.showRemote && isSafeWebURL(src) {
			m.writeImage(alt, src)
			return
		}
		m.blocked++
		return
	}
	m.blocked++
}

func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1, atom.H2:
		return 3
	case atom.H3:
		return 4
	}
	return 5
}

func (m *mdWriter) block(n *xhtml.Node) {
	if m.inline() {
		// A block inside a link or <b>: a space keeps the words apart.
		m.space = true
		m.children(n)
		m.space = true
		return
	}
	switch n.DataAtom {
	case atom.Html, atom.Body:
		m.children(n)
		return
	case atom.Hr:
		m.rule()
		return
	case atom.Pre:
		m.flush()
		m.preBlock(n)
		return
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		m.flush()
		m.heading = headingLevel(n.DataAtom)
		m.children(n)
		m.flush()
		return
	case atom.Blockquote:
		m.flush()
		m.quote++
		m.children(n)
		m.flush()
		m.quote--
		return
	case atom.Ul, atom.Ol:
		m.flush()
		m.listSeq++
		m.lists = append(m.lists, mdList{ordered: n.DataAtom == atom.Ol, id: m.listSeq})
		m.children(n)
		m.flush()
		m.lists = m.lists[:len(m.lists)-1]
		return
	case atom.Li:
		m.flush()
		m.item, m.mark, m.itemRoot = true, "- ", 0
		if len(m.lists) > 0 {
			m.itemRoot = m.lists[0].id
		}
		if k := len(m.lists); k > 0 && m.lists[k-1].ordered {
			m.lists[k-1].n++
			m.mark = strconv.Itoa(m.lists[k-1].n) + ". "
		}
		m.inItem++
		m.children(n)
		m.inItem--
		m.flush()
		return
	case atom.Table:
		if rows := dataTableRows(n); rows != nil {
			m.flush()
			m.table(rows)
			return
		}
	case atom.Tr:
		if cells := simpleRowCells(n); cells != nil {
			m.flush()
			m.row(cells)
			m.flush()
			return
		}
	}
	if m.inItem > 0 && m.item {
		// Paragraphs inside a list item stay in the item, on lines of their own.
		m.lineBreak()
		m.children(n)
		m.lineBreak()
		return
	}
	m.flush()
	if open, closing := styleTags(normStyle(n)); open != "" && !hasBlockDescendant(n) {
		m.wrap(n, open, closing)
	} else {
		m.children(n)
	}
	m.flush()
}

// dataTableRows returns the cells of a table that holds data – rows of the same number of cells
// (two or more, in two rows or more), no table inside and no pictures – so that it shows as a
// table. Mails lay themselves out with tables too; those return nil and read as text.
func dataTableRows(t *xhtml.Node) [][]*xhtml.Node {
	var rows [][]*xhtml.Node
	ok := true
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil && ok; c = c.NextSibling {
			if c.Type != xhtml.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(c)
			case atom.Caption, atom.Colgroup, atom.Col:
			case atom.Tr:
				if hiddenNode(c) {
					continue
				}
				var cells []*xhtml.Node
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type != xhtml.ElementNode {
						continue
					}
					if (cell.DataAtom != atom.Td && cell.DataAtom != atom.Th) || hasLayoutContent(cell) {
						ok = false
						return
					}
					cells = append(cells, cell)
				}
				rows = append(rows, cells)
			default:
				ok = false
			}
		}
	}
	walk(t)
	if !ok || len(rows) < 2 {
		return nil
	}
	cols := len(rows[0])
	headerRow, anyHeader := true, false
	for i, r := range rows {
		if len(r) != cols || cols < 2 {
			return nil
		}
		for _, cell := range r {
			if cell.DataAtom == atom.Th {
				anyHeader = true
			} else if i == 0 {
				headerRow = false
			}
		}
	}
	// A header row on top makes a table. Header cells elsewhere (on the left: "Status | Open")
	// make a list of fields, which reads better as lines; so do two columns without headers.
	switch {
	case headerRow && anyHeader:
		return rows
	case !anyHeader && cols >= 3:
		return rows
	}
	return nil
}

// hasLayoutContent reports a table or a picture inside a cell: the table lays out a mail.
func hasLayoutContent(n *xhtml.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != xhtml.ElementNode {
			continue
		}
		if c.DataAtom == atom.Table || (c.DataAtom == atom.Img && !pixelImage(c)) || hasLayoutContent(c) {
			return true
		}
	}
	return false
}

// table writes rows as a Markdown table, the first row as its header.
func (m *mdWriter) table(rows [][]*xhtml.Node) {
	prefix := strings.Repeat("> ", m.quote)
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(prefix + "|")
		for _, cell := range r {
			m.push()
			m.lineText, m.space = false, false
			m.children(cell)
			txt := trimBreaks(strings.Join(strings.Fields(m.pop()), " "))
			b.WriteString(" " + txt + " |") // the text arrives escaped, "|" as "\|"
		}
		if i == 0 {
			b.WriteString("\n" + prefix + "|" + strings.Repeat(" --- |", len(r)))
		}
	}
	m.space, m.lineText = false, false
	m.writeBlock(b.String(), false)
}

// simpleRowCells returns the cells of a table row that holds only inline content (a row of
// short cells, as "Status | Open"), else nil.
func simpleRowCells(tr *xhtml.Node) []*xhtml.Node {
	var cells []*xhtml.Node
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != xhtml.ElementNode {
			continue
		}
		if c.DataAtom != atom.Td && c.DataAtom != atom.Th {
			return nil
		}
		if hiddenNode(c) {
			continue
		}
		if hasBlockDescendant(c) {
			return nil
		}
		cells = append(cells, c)
	}
	if len(cells) < 2 {
		return nil
	}
	return cells
}

// row writes the cells of a simple row on one line, apart by " · ".
func (m *mdWriter) row(cells []*xhtml.Node) {
	var parts []string
	for _, cell := range cells {
		m.push()
		m.lineText = false
		m.space = false
		if cell.DataAtom == atom.Th {
			m.wrap(cell, "<b>", "</b>")
		} else {
			m.children(cell)
		}
		if s := trimBreaks(m.pop()); s != "" {
			parts = append(parts, s)
		}
	}
	m.space = false
	if len(parts) > 0 {
		m.cur().WriteString(strings.Join(parts, " · "))
		m.lineText = true
	}
}

// preBlock writes preformatted text as a fenced code block.
func (m *mdWriter) preBlock(n *xhtml.Node) {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		switch {
		case n.Type == xhtml.TextNode:
			b.WriteString(n.Data)
		case n.Type == xhtml.ElementNode && n.DataAtom == atom.Br:
			b.WriteByte('\n')
		case n.Type == xhtml.ElementNode && (skipTags[n.DataAtom] || hiddenNode(n)):
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	code := strings.Map(func(r rune) rune {
		if invisibleRune(r) && r != '\n' {
			return -1
		}
		return r
	}, strings.Trim(b.String(), "\n"))
	if strings.TrimSpace(code) == "" {
		return
	}
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	prefix := strings.Repeat("> ", m.quote)
	lines := strings.Split(code, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	m.writeBlock(prefix+fence+"\n"+strings.Join(lines, "\n")+"\n"+prefix+fence, false)
}

// Plain text
// ----------

// bracketURLRe matches a line that is only a URL in brackets, as Atlassian writes a link's
// address on the line after its text.
var bracketURLRe = regexp.MustCompile(`^\s*[\[<](https?://[^\s<>\[\]]+)[\]>]\s*$`)

// quoteDepth splits a plain-text line into its quote depth ("> > ") and its text.
func quoteDepth(line string) (int, string) {
	depth := 0
	rest := line
	for {
		t := strings.TrimLeft(rest, " ")
		if !strings.HasPrefix(t, ">") {
			break
		}
		depth++
		rest = strings.TrimPrefix(t[1:], " ")
	}
	if depth == 0 {
		return 0, line
	}
	return depth, rest
}

// plainMailMarkdown converts a plain-text mail: paragraphs at blank lines, line breaks kept,
// quotes as block quotes, URLs as short links.
func plainMailMarkdown(src string) string {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(strings.TrimSpace(src), "\n")
	m := newMDWriter(false)
	for i := 0; i < len(lines); i++ {
		depth, text := quoteDepth(strings.TrimRight(lines[i], " \t"))
		if depth != m.quote {
			m.flush()
			m.quote = depth
		}
		if strings.TrimSpace(text) == "" {
			m.flush()
			continue
		}
		m.lineBreak()
		// "Title" followed by a line "[https://…]": the title becomes the link.
		if i+1 < len(lines) && !urlRe.MatchString(text) {
			nd, next := quoteDepth(strings.TrimRight(lines[i+1], " \t"))
			if sub := bracketURLRe.FindStringSubmatch(next); nd == depth && sub != nil && utf8.RuneCountInString(strings.TrimSpace(text)) <= 200 {
				label := strings.TrimSpace(text)
				lead := text[:len(text)-len(strings.TrimLeft(text, " \t"))]
				m.textRun(lead)
				m.writeLink(escapeMarkdown(label), sub[1], false)
				i++
				continue
			}
		}
		m.text(text)
	}
	return m.result()
}

// Rendering
// ---------

func (a *App) bodyMarkdownOpts() *markdown.Options {
	if a.widgets.mdBody.Theme == nil {
		a.widgets.mdBody.Theme = &markdown.Theme{FontSize: 13.5, BlockSpacing: 0.7}
		a.widgets.mdBody.WrapCode = true
		// Only what the converter writes: no math (\( \) would read as TeX), footnotes, tables
		// or autolinks.
		a.widgets.mdBody.Extensions = parser.ExtStrikethrough
		a.widgets.mdBody.LoadImage = a.loadMailImage
		a.widgets.mdBody.OnLink = func(u string) {
			u = strings.TrimSpace(u)
			if len(u) >= 7 && strings.EqualFold(u[:7], "mailto:") {
				a.emitUpdate(func() {
					a.OpenComposeFromMailto(u)
				})
				return
			}
			if isSafeWebURL(u) {
				openExternalURL(u)
			}
		}
	}
	return &a.widgets.mdBody
}

// renderMailBody shows the open mail's body, formatted from its HTML or from its text version.
func (a *App) renderMailBody(c *ui.Context, msg *backend.MessageDetail) {
	t := c.Theme()
	senderAllowed := a.senderRemoteImagesAllowed(msg.From)
	showRemote := a.readerRemoteImagesAllowedForMsg(msg)
	if a.readerBody.MsgID != msg.ID || a.readerBody.ShowRemote != showRemote {
		a.resetReaderImages(msg.ID)
		a.readerBody = buildReaderBodyWithImages(msg, showRemote)
	}
	body := &a.readerBody
	md := body.Markdown
	plain := body.FromHTML && a.readerPlain
	if plain {
		md = body.Plain
	}
	a.renderSecurityNote(c, msg)
	if body.FromHTML || body.BlockedImages > 0 || body.RemoteImages > 0 {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
			if body.BlockedImages > 0 && !plain {
				ui.Icon(c, iconImageOff).Size(13, 13).TextColor(t.TextMuted)
				ui.Text(c, a.trf("%d Bilder nicht geladen", "%d images not loaded", body.BlockedImages)).FontSize(11.5).TextColor(t.TextMuted).
					Tooltip(a.tr("Externe Bilder sind aus Datenschutzgründen blockiert, bis du sie für diese Mail erlaubst.", "Remote images are blocked for privacy until you allow them for this email."))
				if iconLabelBtn(c.Key("img-show"), iconImage, a.tr("Bilder anzeigen", "Show Images"), false, a.tr("Externe Bilder für diese Mail laden", "Load remote images for this email")).Clicked() {
					a.setReaderRemoteImagesAllowed(msg.ID, true)
					a.readerBody = buildReaderBodyWithImages(msg, true)
				}
				if extractSenderEmail(msg.From) != "" {
					if ui.Button(c.Key("img-allow-sender"), a.tr("Absender immer erlauben", "Always allow sender")).Padding(2, 7).Clicked() {
						a.SetSenderRemoteImagesAllowed(msg.From, true)
						a.setReaderRemoteImagesAllowed(msg.ID, true)
						a.readerBody = buildReaderBodyWithImages(msg, true)
						a.toast(a.trf("Bilder von %s werden ab jetzt immer geladen", "Remote images from %s will always load", extractSenderEmail(msg.From)), "success")
					}
				}
			} else if body.ShowRemote && body.RemoteImages > 0 && !plain {
				ui.Icon(c, iconImage).Size(13, 13).TextColor(t.Accent)
				lbl := a.tr("Externe Bilder geladen", "Remote images shown")
				if senderAllowed {
					lbl = a.tr("Bilder erlaubt (Absender-Allowlist)", "Images allowed (sender allowlist)")
				}
				ui.Text(c, lbl).FontSize(11.5).TextColor(t.TextMuted)
				if senderAllowed {
					if ui.Button(c.Key("img-revoke-sender"), a.tr("Absender blockieren", "Block sender")).Padding(2, 7).Clicked() {
						a.SetSenderRemoteImagesAllowed(msg.From, false)
						a.setReaderRemoteImagesAllowed(msg.ID, false)
						a.readerBody = buildReaderBodyWithImages(msg, false)
					}
				} else if ui.Button(c.Key("img-hide"), a.tr("Ausblenden", "Hide")).Padding(2, 7).Clicked() {
					a.setReaderRemoteImagesAllowed(msg.ID, false)
					a.readerBody = buildReaderBodyWithImages(msg, false)
				}
			}
			ui.Spacer(c)
			if body.FromHTML {
				idx := 0
				if a.readerPlain {
					idx = 1
				}
				if ui.Segmented(c, &idx, a.tr("Formatiert", "Formatted"), a.tr("Text", "Text")).Changed() {
					a.readerPlain = idx == 1
				}
			}
		})
	}
	if strings.TrimSpace(md) == "" {
		if !msg.Security.Readable() {
			ui.Text(c, a.tr("(Inhalt verschlüsselt)", "(Content encrypted)")).FontSize(13).TextColor(t.TextMuted)
			return
		}
		ui.Text(c, a.tr("(Kein Textinhalt)", "(No text content)")).FontSize(13).TextColor(t.TextMuted)
		return
	}
	markdown.View(c.Key("mail-body-"+msg.ID), md, a.bodyMarkdownOpts()).FillWidth().MaxWidth(900)
}

// renderSecurityNote says that a mail is encrypted or signed.
func (a *App) renderSecurityNote(c *ui.Context, msg *backend.MessageDetail) {
	sec := msg.Security
	if !sec.Encrypted && !sec.Signed {
		return
	}
	t := c.Theme()
	var lines []string
	if sec.Encrypted {
		switch {
		case sec.PGP:
			lines = append(lines, a.tr(
				"OpenPGP-verschlüsselt (PGP/MIME). Entschlüsselung folgt in Phase 2.",
				"OpenPGP encrypted (PGP/MIME). Decryption arrives in Phase 2."))
		case sec.Readable():
			if sec.SMIME {
				lines = append(lines, a.tr("S/MIME-verschlüsselt & erfolgreich entschlüsselt. KI-Aktionen senden den Klartext an den KI-Anbieter.",
					"S/MIME encrypted & decrypted. AI actions on this email send decrypted text to the AI provider."))
			} else {
				lines = append(lines, a.tr("Verschlüsselt. KI-Aktionen auf diese Mail senden den entschlüsselten Text an den KI-Anbieter.",
					"Encrypted. AI actions on this email send its decrypted text to the AI provider."))
			}
		default:
			msgErr := a.tr("S/MIME-verschlüsselt. MailDiver konnte den Inhalt nicht entschlüsseln (Zertifikat unter Konten → S/MIME importieren).",
				"S/MIME encrypted. MailDiver could not decrypt this email (import certificate under Accounts → S/MIME).")
			if sec.Error != "" {
				msgErr += " (" + sec.Error + ")"
			}
			lines = append(lines, msgErr)
		}
	}
	if sec.Signed {
		switch {
		case sec.PGP && !sec.SMIME:
			lines = append(lines, a.tr("Mit OpenPGP signiert (Signaturprüfung folgt in Phase 2).", "Signed with OpenPGP (signature verification arrives in Phase 2)."))
		case sec.SMIME && sec.SignatureValid && sec.SignerAddressMismatch:
			lines = append(lines, a.trf("WARNUNG: S/MIME-Signatur gültig für %s, weicht aber von der Absender-Adresse ab!",
				"WARNING: S/MIME signature valid for %s, but does not match the From address!", sec.SignerEmail))
		case sec.SMIME && sec.SignatureValid && sec.SignatureTrusted:
			who := sec.SignerEmail
			if sec.SignerSubject != "" && !strings.EqualFold(sec.SignerSubject, who) {
				who = sec.SignerSubject + " <" + who + ">"
			}
			lines = append(lines, a.trf("S/MIME-Signatur gültig & vertrauenswürdig (%s · Aussteller: %s)",
				"S/MIME signature valid & trusted (%s · Issuer: %s)", who, sec.SignerIssuer))
		case sec.SMIME && sec.SignatureValid:
			lines = append(lines, a.trf("S/MIME-Signatur kryptografisch gültig (%s), Zertifikatskette nicht im System-Vertrauensspeicher.",
				"S/MIME signature cryptographically valid (%s), certificate chain not in system trust store.", sec.SignerEmail))
		case sec.SMIME && !sec.SignatureValid:
			lines = append(lines, a.tr("WARNUNG: S/MIME-Signatur ungültig — Nachricht wurde möglicherweise verändert!",
				"WARNING: S/MIME signature invalid — message may have been tampered with!"))
		default:
			lines = append(lines, a.tr("Signiert (PGP/S/MIME).", "Signed (PGP/S/MIME)."))
		}
	}
	ui.Row(c.Key("sec-note")).FillWidth().AlignItems(ui.Center).Gap(8).Padding(6, 10).Radius(8).Border(1, t.Border).Children(func() {
		switch {
		case sec.SMIME && sec.Signed && (!sec.SignatureValid || sec.SignerAddressMismatch):
			ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.Danger)
		case sec.Encrypted && !sec.Readable():
			ui.Icon(c, iconLock).Size(14, 14).TextColor(t.Warning)
		case sec.Encrypted || (sec.SMIME && sec.SignatureValid && sec.SignatureTrusted):
			ui.Icon(c, iconLock).Size(14, 14).TextColor(t.Success)
		case sec.SMIME && sec.SignatureValid:
			ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.Warning)
		default:
			ui.Icon(c, iconSignature).Size(14, 14).TextColor(t.TextMuted)
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			for _, line := range lines {
				ui.Text(c, line).FontSize(11.5).TextColor(t.TextMuted)
			}
		})
	})
}
