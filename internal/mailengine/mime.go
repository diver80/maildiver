package mailengine

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"golang.org/x/net/html"

	"github.com/diver80/postdive/internal/backend"
)

const maxParsedPartBytes = 8 * 1024 * 1024

// MessageHeaderLine represents a single RFC 5322 header line in original wire order.
type MessageHeaderLine struct {
	Name  string
	Value string
}

// MessageSource holds all RFC 5322 headers and raw plaintext source of a message for inspection.
type MessageSource struct {
	MessageID  string
	Headers    []MessageHeaderLine
	RawSource  string
	TotalBytes int
	Truncated  bool
}

// splitRawHeaderAndBody splits an RFC 5322 message into its raw header section and body section.
func splitRawHeaderAndBody(raw []byte) (headerPart []byte, bodyPart []byte) {
	if idx := bytes.Index(raw, []byte("\r\n\r\n")); idx >= 0 {
		return raw[:idx], raw[idx+4:]
	}
	if idx := bytes.Index(raw, []byte("\n\n")); idx >= 0 {
		return raw[:idx], raw[idx+2:]
	}
	return raw, nil
}

// ExtractRawHeaderLines parses every RFC 5322 header field from raw in original wire order,
// unfolding continuation lines and decoding RFC 2047 encoded words for readability.
func ExtractRawHeaderLines(raw []byte) []MessageHeaderLine {
	hdrBytes, _ := splitRawHeaderAndBody(raw)
	if len(hdrBytes) == 0 {
		return nil
	}
	norm := strings.ReplaceAll(string(hdrBytes), "\r\n", "\n")
	rawLines := strings.Split(norm, "\n")
	var out []MessageHeaderLine
	for _, line := range rawLines {
		if line == "" {
			break
		}
		if (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			// RFC 5322 folded continuation line
			cont := strings.TrimSpace(line)
			if cont != "" {
				if out[len(out)-1].Value != "" {
					out[len(out)-1].Value += " " + cont
				} else {
					out[len(out)-1].Value = cont
				}
			}
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		if name == "" {
			continue
		}
		out = append(out, MessageHeaderLine{Name: name, Value: val})
	}
	for i := range out {
		out[i].Value = decodeHeader(out[i].Value)
	}
	return out
}

// ExtractAllHeadersMap extracts every RFC 5322 header from raw into a lowercase-keyed map.
// Duplicate headers (such as multiple Received lines) are joined with "\n".
func ExtractAllHeadersMap(raw []byte) map[string]string {
	lines := ExtractRawHeaderLines(raw)
	if len(lines) == 0 {
		return nil
	}
	out := make(map[string]string, len(lines))
	for _, ln := range lines {
		k := strings.ToLower(strings.TrimSpace(ln.Name))
		if k == "" {
			continue
		}
		if prev, exists := out[k]; exists && prev != "" {
			out[k] = prev + "\n" + ln.Value
		} else {
			out[k] = ln.Value
		}
	}
	return out
}

// ParsedMessage holds extracted text, HTML, attachments, headers, and optional .ics invite.
type ParsedMessage struct {
	Subject     string
	From        string
	To          []string
	CC          []string
	Date        time.Time
	MessageID   string
	InReplyTo   string
	References  string
	Headers     map[string]string
	BodyText    string
	BodyHTML    string
	Attachments []backend.Attachment
	ICSBody     string
	Encrypted   bool
	Signed      bool
	IsPGP       bool
}

// ParseRawMessage parses an RFC 5322 / MIME message into structured fields.
func ParseRawMessage(raw []byte) (ParsedMessage, error) {
	var out ParsedMessage
	out.Headers = map[string]string{}

	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		// Fall back to net/mail header parse + raw body if malformed MIME.
		if msg, merr := mail.ReadMessage(bytes.NewReader(raw)); merr == nil {
			out.Subject = decodeHeader(msg.Header.Get("Subject"))
			out.From = decodeHeader(msg.Header.Get("From"))
			out.MessageID = cleanMsgID(msg.Header.Get("Message-ID"))
			out.InReplyTo = cleanMsgID(msg.Header.Get("In-Reply-To"))
			out.References = strings.TrimSpace(msg.Header.Get("References"))
			if d, derr := msg.Header.Date(); derr == nil {
				out.Date = d
			}
			body, _ := io.ReadAll(io.LimitReader(msg.Body, maxParsedPartBytes))
			out.BodyText = strings.TrimSpace(string(body))
			return out, nil
		}
		return out, err
	}

	h := entity.Header
	out.Subject = decodeHeader(h.Get("Subject"))
	out.From = decodeHeader(h.Get("From"))
	out.To = parseAddressList(h.Get("To"))
	out.CC = parseAddressList(h.Get("Cc"))
	out.MessageID = cleanMsgID(h.Get("Message-ID"))
	out.InReplyTo = cleanMsgID(h.Get("In-Reply-To"))
	out.References = strings.TrimSpace(h.Get("References"))
	if d, derr := mail.ParseDate(h.Get("Date")); derr == nil {
		out.Date = d
	}

	for _, key := range []string{"Content-Type", "List-Id", "List-Unsubscribe", "Auto-Submitted", "Precedence", "Reply-To", "X-Mailer", "Bcc"} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			out.Headers[strings.ToLower(key)] = decodeHeader(v)
		}
	}

	var inlineFallback []backend.Attachment
	walkMIMEEntity(entity, &out, &inlineFallback, 0, "")

	if strings.TrimSpace(out.BodyHTML) == "" && len(inlineFallback) > 0 {
		out.Attachments = append(out.Attachments, inlineFallback...)
	}
	if strings.TrimSpace(out.BodyText) == "" && strings.TrimSpace(out.BodyHTML) != "" {
		out.BodyText = htmlToPlainText(out.BodyHTML)
	}
	if strings.Contains(out.BodyText, "-----BEGIN PGP MESSAGE-----") {
		out.IsPGP = true
		out.Encrypted = true
	} else if strings.Contains(out.BodyText, "-----BEGIN PGP SIGNED MESSAGE-----") {
		out.IsPGP = true
		out.Signed = true
	}
	return out, nil
}

func parseMIMEContentType(h message.Header) (string, map[string]string) {
	ct, params, err := h.ContentType()
	if err == nil && strings.TrimSpace(ct) != "" {
		return strings.ToLower(strings.TrimSpace(ct)), params
	}
	raw := strings.TrimSpace(h.Get("Content-Type"))
	if raw == "" {
		return "text/plain", nil
	}
	mainPart, rest, _ := strings.Cut(raw, ";")
	mainPart = strings.ToLower(strings.TrimSpace(mainPart))
	if mainPart == "" {
		mainPart = "text/plain"
	}
	return mainPart, parseLooseMIMEParams(rest)
}

func parseMIMEDisposition(h message.Header, ctParams map[string]string) (disp string, filename string) {
	d, dispParams, err := h.ContentDisposition()
	if err == nil {
		disp = strings.ToLower(strings.TrimSpace(d))
		filename = decodeHeader(dispParams["filename"])
	} else if raw := strings.TrimSpace(h.Get("Content-Disposition")); raw != "" {
		mainPart, rest, _ := strings.Cut(raw, ";")
		disp = strings.ToLower(strings.TrimSpace(mainPart))
		loose := parseLooseMIMEParams(rest)
		filename = decodeHeader(loose["filename"])
	}
	if filename == "" && ctParams != nil {
		filename = decodeHeader(ctParams["name"])
	}
	if filename == "" {
		if rawCT := strings.TrimSpace(h.Get("Content-Type")); rawCT != "" {
			if _, rest, ok := strings.Cut(rawCT, ";"); ok {
				loose := parseLooseMIMEParams(rest)
				filename = decodeHeader(loose["name"])
			}
		}
	}
	return disp, strings.TrimSpace(filename)
}

// splitMIMEParams splits a semicolon-separated MIME parameter string while respecting
// quoted values (so semicolons inside quotes like filename="a;b.pdf" stay intact).
func splitMIMEParams(rest string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if inQuote && ch == '\\' {
			cur.WriteByte(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			cur.WriteByte(ch)
			continue
		}
		if ch == ';' && !inQuote {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func trimMatchingQuotes(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				i++
			}
			b.WriteByte(inner[i])
		}
		return b.String()
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// parseLooseMIMEParams extracts key=value parameters (including RFC 2231 continuations like
// filename*0*=utf-8'de'foo%20; filename*1*=bar%20baz.pdf) from headers whose strict
// mime.ParseMediaType failed.
func parseLooseMIMEParams(rest string) map[string]string {
	type contPiece struct {
		idx   int
		order int
		val   string
	}
	plain := map[string]string{}
	ext := map[string]string{}
	cont := map[string][]contPiece{}
	order := 0
	for _, seg := range splitMIMEParams(rest) {
		seg = strings.TrimSpace(seg)
		eq := strings.IndexByte(seg, '=')
		if eq <= 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(seg[:eq]))
		v := trimMatchingQuotes(seg[eq+1:])
		if idx := strings.IndexByte(k, '*'); idx > 0 {
			base := k[:idx]
			suffix := k[idx+1:]
			isExt := suffix == "" || strings.HasSuffix(suffix, "*")
			if isExt {
				// RFC 2231: charset'lang'value appears on key*= and key*0*=; subsequent *N*= segments are percent-encoded only.
				if suffix == "" || suffix == "0*" {
					if p1 := strings.IndexByte(v, '\''); p1 >= 0 {
						if p2 := strings.IndexByte(v[p1+1:], '\''); p2 >= 0 {
							v = v[p1+1+p2+1:]
						}
					}
				}
				v = decodeRFC2231Percent(v)
			}
			if suffix == "" {
				if v != "" {
					ext[base] = v
				}
			} else {
				numStr := strings.TrimSuffix(suffix, "*")
				n, err := strconv.Atoi(numStr)
				if err != nil {
					n = order
				}
				cont[base] = append(cont[base], contPiece{idx: n, order: order, val: v})
				order++
			}
			continue
		}
		if plain[k] == "" && v != "" {
			plain[k] = v
		}
	}
	out := make(map[string]string, len(plain)+len(ext)+len(cont))
	for k, v := range plain {
		out[k] = v
	}
	for base, pieces := range cont {
		sort.SliceStable(pieces, func(i, j int) bool {
			if pieces[i].idx != pieces[j].idx {
				return pieces[i].idx < pieces[j].idx
			}
			return pieces[i].order < pieces[j].order
		})
		var b strings.Builder
		for _, p := range pieces {
			b.WriteString(p.val)
		}
		if joined := b.String(); joined != "" {
			out[base] = joined
		}
	}
	for base, v := range ext {
		if v != "" {
			out[base] = v
		}
	}
	return out
}

func decodeRFC2231Percent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := hex.DecodeString(s[i+1 : i+3]); err == nil && len(v) == 1 {
				b.WriteByte(v[0])
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func walkMIMEEntity(e *message.Entity, out *ParsedMessage, inlineFallback *[]backend.Attachment, depth int, partPrefix string) {
	if e == nil || depth > 12 {
		return
	}
	ct, params := parseMIMEContentType(e.Header)

	switch ct {
	case "multipart/signed":
		out.Signed = true
		if strings.Contains(strings.ToLower(params["protocol"]), "pgp") || strings.HasPrefix(strings.ToLower(params["micalg"]), "pgp") {
			out.IsPGP = true
		}
	case "multipart/encrypted":
		out.Encrypted = true
		if strings.Contains(strings.ToLower(params["protocol"]), "pgp") {
			out.IsPGP = true
		}
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		out.Encrypted = true
	case "application/pgp-encrypted":
		out.Encrypted = true
		out.IsPGP = true
	case "application/pgp-signature":
		out.Signed = true
		out.IsPGP = true
	}

	if mr := e.MultipartReader(); mr != nil {
		idx := 0
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			idx++
			sub := fmt.Sprintf("%d", idx)
			if partPrefix != "" {
				sub = partPrefix + "." + sub
			}
			walkMIMEEntity(p, out, inlineFallback, depth+1, sub)
		}
		return
	}

	disp, filename := parseMIMEDisposition(e.Header, params)

	bodyBytes, _ := io.ReadAll(io.LimitReader(e.Body, maxParsedPartBytes))

	if ct == "text/calendar" || strings.HasSuffix(strings.ToLower(filename), ".ics") {
		if out.ICSBody == "" {
			out.ICSBody = string(bodyBytes)
		}
	}

	cid := cleanContentID(e.Header.Get("Content-ID"))
	isInlineImage := cid != "" && strings.HasPrefix(ct, "image/") && disp != "attachment"
	isAttachment := disp == "attachment" || (!isInlineImage && filename != "" && ct != "text/plain" && ct != "text/html")
	if isAttachment {
		name := filename
		if name == "" {
			name = "attachment"
		}
		out.Attachments = append(out.Attachments, backend.Attachment{
			Name:        name,
			ContentType: ct,
			Size:        int64(len(bodyBytes)),
			PartName:    partPrefix,
		})
		return
	}
	if isInlineImage {
		if filename != "" && inlineFallback != nil {
			*inlineFallback = append(*inlineFallback, backend.Attachment{
				Name:        filename,
				ContentType: ct,
				Size:        int64(len(bodyBytes)),
				PartName:    partPrefix,
			})
		}
		return
	}

	switch ct {
	case "text/plain":
		txt := strings.TrimSpace(string(bodyBytes))
		if txt != "" {
			if out.BodyText == "" {
				out.BodyText = txt
			} else {
				out.BodyText += "\n\n" + txt
			}
		}
	case "text/html":
		h := strings.TrimSpace(string(bodyBytes))
		if h != "" {
			if out.BodyHTML == "" {
				out.BodyHTML = h
			} else {
				out.BodyHTML += "\n" + h
			}
		}
	}
}

func decodeHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	dec := new(mime.WordDecoder)
	if s, err := dec.DecodeHeader(raw); err == nil {
		return s
	}
	return raw
}

func parseAddressList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(raw)
	if err != nil {
		// Simple comma split fallback.
		var out []string
		for _, p := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(decodeHeader(p)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Name != "" {
			out = append(out, fmt.Sprintf("%s <%s>", decodeHeader(a.Name), a.Address))
		} else {
			out = append(out, a.Address)
		}
	}
	return out
}

func cleanMsgID(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), "<>")
}

func cleanContentID(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), "<>")
}

// InlineImage holds an embedded CID image extracted from a MIME message.
type InlineImage struct {
	CID         string
	ContentType string
	Data        []byte
}

const (
	maxInlineImageBytes = 10 * 1024 * 1024
	maxInlineImageCount = 32
)

func isSafeInlineImageType(ct string) bool {
	if !strings.HasPrefix(ct, "image/") {
		return false
	}
	// Never embed SVG as inline image (can execute scripts/external loads in HTML/print contexts).
	return !strings.Contains(ct, "svg")
}

// ExtractInlineImage finds a MIME part matching cid (Content-ID without angle brackets,
// case-insensitive) and returns its content type and decoded bytes, stopping as soon as it matches.
func ExtractInlineImage(raw []byte, cid string) (contentType string, data []byte, err error) {
	want := strings.ToLower(cleanContentID(cid))
	if want == "" || len(raw) == 0 {
		return "", nil, fmt.Errorf("inline image %q not found", cid)
	}
	entity, rerr := message.Read(bytes.NewReader(raw))
	if rerr != nil && !message.IsUnknownCharset(rerr) {
		return "", nil, fmt.Errorf("inline image %q not found", cid)
	}
	var foundCT string
	var foundData []byte
	var found bool
	var walk func(e *message.Entity, depth int)
	walk = func(e *message.Entity, depth int) {
		if e == nil || depth > 12 || found {
			return
		}
		ct, _ := parseMIMEContentType(e.Header)
		if strings.HasPrefix(ct, "multipart/") {
			mr := e.MultipartReader()
			if mr == nil {
				return
			}
			for !found {
				p, perr := mr.NextPart()
				if perr != nil {
					break
				}
				walk(p, depth+1)
			}
			return
		}
		partCID := strings.ToLower(cleanContentID(e.Header.Get("Content-ID")))
		if partCID != want || !isSafeInlineImageType(ct) {
			return
		}
		buf, berr := io.ReadAll(io.LimitReader(e.Body, maxInlineImageBytes+1))
		if berr != nil || len(buf) == 0 || len(buf) > maxInlineImageBytes {
			return
		}
		foundCT = ct
		foundData = buf
		found = true
	}
	walk(entity, 0)
	if found {
		return foundCT, foundData, nil
	}
	return "", nil, fmt.Errorf("inline image %q not found", cid)
}

// ExtractAllInlineImages extracts embedded Content-ID image parts from raw RFC 5322 bytes.
// Map keys are lower-cased Content-IDs without angle brackets.
func ExtractAllInlineImages(raw []byte) map[string]InlineImage {
	out := map[string]InlineImage{}
	if len(raw) == 0 {
		return out
	}
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return out
	}
	var walk func(e *message.Entity, depth int)
	walk = func(e *message.Entity, depth int) {
		if e == nil || depth > 12 || len(out) >= maxInlineImageCount {
			return
		}
		ct, _ := parseMIMEContentType(e.Header)
		if strings.HasPrefix(ct, "multipart/") {
			mr := e.MultipartReader()
			if mr == nil {
				return
			}
			for {
				p, perr := mr.NextPart()
				if perr != nil {
					break
				}
				walk(p, depth+1)
			}
			return
		}
		cid := strings.ToLower(cleanContentID(e.Header.Get("Content-ID")))
		if cid == "" || !isSafeInlineImageType(ct) {
			return
		}
		if _, exists := out[cid]; exists {
			return
		}
		buf, rerr := io.ReadAll(io.LimitReader(e.Body, maxInlineImageBytes+1))
		if rerr != nil || len(buf) == 0 || len(buf) > maxInlineImageBytes {
			return
		}
		out[cid] = InlineImage{
			CID:         cid,
			ContentType: ct,
			Data:        buf,
		}
	}
	walk(entity, 0)
	return out
}

// htmlToPlainText extracts readable text from HTML bodies while skipping script/style tags.
func htmlToPlainText(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return strings.TrimSpace(src)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "head", "noscript":
				return
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
				b.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			txt := strings.TrimSpace(n.Data)
			if txt != "" {
				if b.Len() > 0 {
					last := b.String()[b.Len()-1]
					if last != '\n' && last != ' ' {
						b.WriteByte(' ')
					}
				}
				b.WriteString(txt)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	var clean []string
	blankRun := 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			blankRun++
			if blankRun <= 1 && len(clean) > 0 {
				clean = append(clean, "")
			}
			continue
		}
		blankRun = 0
		clean = append(clean, t)
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

// OutgoingAttachment is a file attached to an outgoing message.
type OutgoingAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// OutgoingInlineImage represents a cid-referenced inline image inside multipart/related HTML.
type OutgoingInlineImage struct {
	ContentID   string
	Filename    string
	ContentType string
	Data        []byte
}

// OutgoingMessage describes an email to serialize into RFC 5322 format for SMTP/Draft APPEND.
type OutgoingMessage struct {
	FromName         string
	FromEmail        string
	To               []string
	CC               []string
	BCC              []string
	IncludeBccHeader bool // set true ONLY when saving Drafts to IMAP; NEVER when sending via SMTP
	Subject          string
	Body             string
	BodyHTML         string // optional HTML alternative; when set, emits multipart/alternative (plain + HTML)
	IsHTML           bool
	InlineImages     []OutgoingInlineImage
	Attachments      []OutgoingAttachment
	InReplyTo        string
	References       string
	Date             time.Time
}

var dataURLImageAttrRE = regexp.MustCompile(`(?i)\bsrc\s*=\s*(["'])data:image/(png|jpeg|jpg|gif|webp);base64,([A-Za-z0-9+/=\r\n\s]+)(["'])`)

// ExtractInlineDataURLImages extracts base64 data:image/* URIs from an HTML body into
// RFC 2387 cid-referenced OutgoingInlineImage parts so standard mail clients render them inline.
func ExtractInlineDataURLImages(htmlBody string) (string, []OutgoingInlineImage) {
	if !strings.Contains(strings.ToLower(htmlBody), "data:image/") {
		return htmlBody, nil
	}
	const maxPerImage = 15 * 1024 * 1024
	const maxTotalInline = 25 * 1024 * 1024
	var out []OutgoingInlineImage
	var total int
	idx := 0
	cleaned := dataURLImageAttrRE.ReplaceAllStringFunc(htmlBody, func(match string) string {
		sub := dataURLImageAttrRE.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		ext := strings.ToLower(sub[2])
		b64 := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, sub[3])
		if base64.StdEncoding.DecodedLen(len(b64)) > maxPerImage {
			return match
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) == 0 || len(raw) > maxPerImage || total+len(raw) > maxTotalInline {
			return match
		}
		total += len(raw)
		idx++
		mimeType := "image/" + ext
		if ext == "jpg" {
			mimeType = "image/jpeg"
		}
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		cid := fmt.Sprintf("mdv-img-%d-%s@postdive.local", idx, hex.EncodeToString(rnd[:]))
		out = append(out, OutgoingInlineImage{
			ContentID:   cid,
			Filename:    fmt.Sprintf("image-%d.%s", idx, ext),
			ContentType: mimeType,
			Data:        raw,
		})
		return `src="cid:` + cid + `"`
	})
	return cleaned, out
}

func encodeQPBody(body string) string {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	norm = strings.ReplaceAll(norm, "\n", "\r\n")
	if !strings.HasSuffix(norm, "\r\n") {
		norm += "\r\n"
	}
	var out bytes.Buffer
	w := quotedprintable.NewWriter(&out)
	_, _ = w.Write([]byte(norm))
	_ = w.Close()
	return out.String()
}

func sanitizeMediaType(raw, filename string) string {
	if raw != "" {
		if mt, _, err := mime.ParseMediaType(strings.TrimSpace(raw)); err == nil && mt != "" && !strings.ContainsAny(mt, "\r\n") {
			return mt
		}
	}
	if ext := filepath.Ext(filename); ext != "" {
		if mt, _, err := mime.ParseMediaType(mime.TypeByExtension(ext)); err == nil && mt != "" {
			return mt
		}
	}
	return "application/octet-stream"
}

// BuildRawMessage constructs a standards-compliant RFC 5322 message (with CRLF line endings,
// quoted-printable body encoding, and multipart/mixed with Base64 encoding when attachments are present)
// and returns the generated Message-ID and raw message bytes.
func BuildRawMessage(msg OutgoingMessage) (msgID string, raw []byte) {
	now := msg.Date
	if now.IsZero() {
		now = time.Now()
	}
	domain := "postdive.local"
	if _, d, ok := strings.Cut(strings.TrimSpace(msg.FromEmail), "@"); ok && d != "" {
		domain = d
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	msgID = fmt.Sprintf("mdv-%d-%s@%s", now.UnixNano(), hex.EncodeToString(rnd[:]), domain)

	fromAddr := mail.Address{Name: strings.TrimSpace(msg.FromName), Address: strings.TrimSpace(msg.FromEmail)}
	var b bytes.Buffer
	writeHdr := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		// Prevent CRLF header injection.
		v = strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " ")
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	writeHdr("From", fromAddr.String())
	if len(msg.To) > 0 {
		writeHdr("To", strings.Join(msg.To, ", "))
	}
	if len(msg.CC) > 0 {
		writeHdr("Cc", strings.Join(msg.CC, ", "))
	}
	if msg.IncludeBccHeader && len(msg.BCC) > 0 {
		writeHdr("Bcc", strings.Join(msg.BCC, ", "))
	}
	writeHdr("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHdr("Date", now.Format(time.RFC1123Z))
	writeHdr("Message-ID", "<"+msgID+">")
	if id := cleanMsgID(msg.InReplyTo); id != "" {
		writeHdr("In-Reply-To", "<"+id+">")
	}
	if refs := strings.TrimSpace(msg.References); refs != "" {
		writeHdr("References", refs)
	}
	writeHdr("MIME-Version", "1.0")

	qpBody := encodeQPBody(msg.Body)
	hasHTMLAlt := strings.TrimSpace(msg.BodyHTML) != ""

	bodyCT := "text/plain; charset=utf-8"
	if msg.IsHTML && !hasHTMLAlt {
		bodyCT = "text/html; charset=utf-8"
	}

	writeAlternative := func(w *bytes.Buffer, altBoundary string) {
		plain := msg.Body
		if strings.TrimSpace(plain) == "" {
			plain = htmlToPlainText(msg.BodyHTML)
		}
		qpPlain := encodeQPBody(plain)
		qpHTML := encodeQPBody(msg.BodyHTML)

		fmt.Fprintf(w, "--%s\r\n", altBoundary)
		w.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		w.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		w.WriteString(qpPlain)
		if !strings.HasSuffix(qpPlain, "\r\n") {
			w.WriteString("\r\n")
		}
		fmt.Fprintf(w, "--%s\r\n", altBoundary)
		if len(msg.InlineImages) == 0 {
			w.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			w.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			w.WriteString(qpHTML)
			if !strings.HasSuffix(qpHTML, "\r\n") {
				w.WriteString("\r\n")
			}
		} else {
			var relRnd [12]byte
			_, _ = rand.Read(relRnd[:])
			relBnd := "mdv-rel-" + hex.EncodeToString(relRnd[:])
			fmt.Fprintf(w, "Content-Type: multipart/related; boundary=%q; type=\"text/html\"\r\n\r\n", relBnd)
			fmt.Fprintf(w, "--%s\r\n", relBnd)
			w.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			w.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			w.WriteString(qpHTML)
			if !strings.HasSuffix(qpHTML, "\r\n") {
				w.WriteString("\r\n")
			}
			for _, img := range msg.InlineImages {
				ct := sanitizeMediaType(img.ContentType, img.Filename)
				safeName := strings.Map(func(r rune) rune {
					if r == '\r' || r == '\n' || r == '"' || r == '\\' || r < 0x20 {
						return '_'
					}
					return r
				}, filepath.Base(strings.TrimSpace(img.Filename)))
				if safeName == "" || safeName == "." {
					safeName = "image.png"
				}
				cid := strings.Trim(img.ContentID, "<> \r\n\t")
				fmt.Fprintf(w, "--%s\r\n", relBnd)
				fmt.Fprintf(w, "Content-Type: %s; name=%q\r\n", ct, safeName)
				w.WriteString("Content-Transfer-Encoding: base64\r\n")
				fmt.Fprintf(w, "Content-ID: <%s>\r\n", cid)
				fmt.Fprintf(w, "Content-Disposition: inline; filename=%q\r\n\r\n", safeName)
				b64 := base64.StdEncoding.EncodeToString(img.Data)
				for i := 0; i < len(b64); i += 76 {
					end := i + 76
					if end > len(b64) {
						end = len(b64)
					}
					w.WriteString(b64[i:end] + "\r\n")
				}
			}
			fmt.Fprintf(w, "--%s--\r\n", relBnd)
		}
		fmt.Fprintf(w, "--%s--\r\n", altBoundary)
	}

	if len(msg.Attachments) == 0 {
		if hasHTMLAlt {
			var altRnd [12]byte
			_, _ = rand.Read(altRnd[:])
			altBnd := "mdv-alt-" + hex.EncodeToString(altRnd[:])
			writeHdr("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", altBnd))
			b.WriteString("\r\n")
			writeAlternative(&b, altBnd)
			return msgID, b.Bytes()
		}
		writeHdr("Content-Type", bodyCT)
		writeHdr("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		b.WriteString(qpBody)
		return msgID, b.Bytes()
	}

	var bndRnd [12]byte
	_, _ = rand.Read(bndRnd[:])
	boundary := "mdv-mixed-" + hex.EncodeToString(bndRnd[:])
	writeHdr("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", boundary))
	b.WriteString("\r\n")

	// Body part (single part or nested multipart/alternative)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	if hasHTMLAlt {
		var altRnd [12]byte
		_, _ = rand.Read(altRnd[:])
		altBnd := "mdv-alt-" + hex.EncodeToString(altRnd[:])
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBnd)
		writeAlternative(&b, altBnd)
	} else {
		fmt.Fprintf(&b, "Content-Type: %s\r\n", bodyCT)
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		b.WriteString(qpBody)
		if !strings.HasSuffix(qpBody, "\r\n") {
			b.WriteString("\r\n")
		}
	}

	// Attachment parts (RFC 2045 76-char wrapped Base64)
	for _, att := range msg.Attachments {
		name := filepath.Base(strings.TrimSpace(att.Filename))
		if name == "" || name == "." || name == ".." || name == "/" {
			name = "attachment.bin"
		}
		ct := sanitizeMediaType(att.ContentType, name)
		safeName := strings.ReplaceAll(strings.ReplaceAll(name, "\r", ""), "\n", "")
		ctHeader := mime.FormatMediaType(ct, map[string]string{"name": safeName})
		if ctHeader == "" {
			ctHeader = fmt.Sprintf("%s; name=%q", ct, safeName)
		}
		cdHeader := mime.FormatMediaType("attachment", map[string]string{"filename": safeName})
		if cdHeader == "" {
			cdHeader = fmt.Sprintf("attachment; filename=%q", safeName)
		}
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s\r\n", ctHeader)
		fmt.Fprintf(&b, "Content-Disposition: %s\r\n", cdHeader)
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		encoded := base64.StdEncoding.EncodeToString(att.Data)
		for i := 0; i < len(encoded); i += 76 {
			end := min(i+76, len(encoded))
			b.WriteString(encoded[i:end])
			b.WriteString("\r\n")
		}
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return msgID, b.Bytes()
}

// BuildCalendarReplyMIME builds an RFC 5322 + RFC 5546 multipart/alternative message carrying
// both a human-readable summary and a text/calendar; method=REPLY part for iTIP RSVP replies.
func BuildCalendarReplyMIME(from, to, subject, textBody, replyICS, inReplyTo string) (msgID string, raw []byte) {
	return BuildCalendarMIME(from, to, subject, textBody, replyICS, "REPLY", inReplyTo)
}

// BuildCalendarMIME builds an iTIP mail (RFC 6047): a text part and the calendar part with
// method (REQUEST, REPLY, CANCEL) as alternatives. to is a ready To header.
func BuildCalendarMIME(from, to, subject, textBody, icsPayload, method, inReplyTo string) (msgID string, raw []byte) {
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	msgID = fmt.Sprintf("mdv-rsvp-%d-%s@postdive.local", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))

	var bndRnd [12]byte
	_, _ = rand.Read(bndRnd[:])
	boundary := "mdv-rsvp-" + hex.EncodeToString(bndRnd[:])

	var b bytes.Buffer
	writeHdr := func(k, v string) {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " ")
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	writeHdr("From", from)
	writeHdr("To", to)
	writeHdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHdr("Date", time.Now().Format(time.RFC1123Z))
	writeHdr("Message-ID", "<"+msgID+">")
	if inReplyTo != "" {
		writeHdr("In-Reply-To", "<"+cleanMsgID(inReplyTo)+">")
	}
	writeHdr("MIME-Version", "1.0")
	writeHdr("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	b.WriteString("\r\n")

	qpBody := encodeQPBody(textBody)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(qpBody)
	if !strings.HasSuffix(qpBody, "\r\n") {
		b.WriteString("\r\n")
	}

	icsNorm := strings.ReplaceAll(icsPayload, "\r\n", "\n")
	icsNorm = strings.ReplaceAll(icsNorm, "\r", "\n")
	icsNorm = strings.ReplaceAll(icsNorm, "\n", "\r\n")
	if !strings.HasSuffix(icsNorm, "\r\n") {
		icsNorm += "\r\n"
	}
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	fmt.Fprintf(&b, "Content-Type: text/calendar; charset=utf-8; method=%s\r\n", method)
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	encodedICS := base64.StdEncoding.EncodeToString([]byte(icsNorm))
	for i := 0; i < len(encodedICS); i += 76 {
		end := min(i+76, len(encodedICS))
		b.WriteString(encodedICS[i:end])
		b.WriteString("\r\n")
	}

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return msgID, b.Bytes()
}

// ExtractAttachment locates and decodes a single attachment from a raw RFC 5322 message
// by MIME partName (preferred) or filename fallback.
func ExtractAttachment(raw []byte, partName, filename string) (name, contentType string, data []byte, err error) {
	if len(raw) == 0 {
		return "", "", nil, fmt.Errorf("Nachricht hat keinen Rohinhalt")
	}
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return "", "", nil, fmt.Errorf("MIME lesen: %w", err)
	}
	var foundName, foundCT string
	var foundData []byte
	var extractErr error
	var walk func(e *message.Entity, prefix string, depth int) bool
	walk = func(e *message.Entity, prefix string, depth int) bool {
		if e == nil || depth > 12 {
			return false
		}
		ct, params := parseMIMEContentType(e.Header)
		if strings.HasPrefix(ct, "multipart/") {
			mr := e.MultipartReader()
			if mr == nil {
				return false
			}
			idx := 0
			for {
				p, perr := mr.NextPart()
				if perr != nil {
					break
				}
				idx++
				sub := fmt.Sprintf("%d", idx)
				if prefix != "" {
					sub = prefix + "." + sub
				}
				if walk(p, sub, depth+1) {
					return true
				}
			}
			return false
		}
		disp, fn := parseMIMEDisposition(e.Header, params)
		isAtt := disp == "attachment" || (fn != "" && ct != "text/plain" && ct != "text/html")
		if !isAtt {
			return false
		}
		if fn == "" {
			fn = "attachment"
		}
		partID := prefix
		if partID == "" {
			partID = "1"
		}
		const maxAttachmentExtractBytes = 32 * 1024 * 1024
		matchPart := partName != "" && partID == partName
		matchName := partName == "" && filename != "" && strings.EqualFold(fn, filename)
		if matchPart || matchName {
			buf, rerr := io.ReadAll(io.LimitReader(e.Body, maxAttachmentExtractBytes+1))
			if rerr != nil {
				extractErr = fmt.Errorf("Anhang %q lesen: %w", fn, rerr)
				return true
			}
			if len(buf) > maxAttachmentExtractBytes {
				extractErr = fmt.Errorf("Anhang %q überschreitet das Limit von %d MB", fn, maxAttachmentExtractBytes/(1024*1024))
				return true
			}
			foundName = fn
			foundCT = ct
			foundData = buf
			return true
		}
		return false
	}
	walk(entity, "", 0)
	if extractErr != nil {
		return "", "", nil, extractErr
	}
	if foundData == nil {
		target := filename
		if target == "" {
			target = partName
		}
		return "", "", nil, fmt.Errorf("Anhang %q nicht gefunden", target)
	}
	return foundName, foundCT, foundData, nil
}

// InvalidAddresses returns the recipient entries that are no email address, so sending can refuse
// them instead of leaving a recipient out unnoticed.
func InvalidAddresses(lists ...[]string) []string {
	var bad []string
	for _, list := range lists {
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, err := mail.ParseAddressList(item); err != nil {
				bad = append(bad, item)
			}
		}
	}
	return bad
}

// ExtractAddresses extracts validated bare email addresses from formatted recipient strings for SMTP RCPT TO.
func ExtractAddresses(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" || strings.ContainsAny(addr, "\r\n\x00 \t") {
			return
		}
		low := strings.ToLower(addr)
		if !seen[low] {
			seen[low] = true
			out = append(out, addr)
		}
	}
	for _, list := range lists {
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" || strings.ContainsAny(item, "\r\n\x00") {
				continue
			}
			if addrs, err := mail.ParseAddressList(item); err == nil {
				for _, a := range addrs {
					add(a.Address)
				}
				continue
			}
			if a, err := mail.ParseAddress(item); err == nil && a.Address != "" {
				add(a.Address)
			}
		}
	}
	return out
}
