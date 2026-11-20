package mailengine

import (
	"html"
	"regexp"
	"strings"
)

var (
	sigDividerLineRE = regexp.MustCompile(`^[\s\-_─━=]+$`)
	sigPhoneCleanRE  = regexp.MustCompile(`[^0-9+]`)
	sigEmailRE       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	sigURLRE         = regexp.MustCompile(`https?://[^\s<>()"]+`)
	mdBoldRE         = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	mdItalicRE       = regexp.MustCompile(`\*([^*\n]+)\*`)
	mdCodeRE         = regexp.MustCompile("`([^`\n]+)`")
)

// BuildStyledEmailHTML turns a plain-text outgoing email that includes a standard
// "-- \n" signature block OR rich markdown formatting (**bold**, *italic*, `code`, bullet lists)
// into a clean, dark-mode-friendly HTML alternative (Option D styling: crimson accent bar,
// clickable tel:/mailto:/https: links, subtle legal footer).
func BuildStyledEmailHTML(plainBody string) string {
	norm := strings.ReplaceAll(plainBody, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")

	msgPart, sigPart, quotePart, hasSig := splitPlainMailParts(norm)
	hasMarkup := mdBoldRE.MatchString(msgPart) || mdCodeRE.MatchString(msgPart) || strings.Contains(msgPart, "\n- ") ||
		mdLinkRE.MatchString(msgPart) || hasMarkdownTable(msgPart)
	if (!hasSig || strings.TrimSpace(sigPart) == "") && !hasMarkup {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"></head>`)
	b.WriteString(`<body style="margin:0;padding:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:14px;line-height:1.55;color:#1f2328;">`)

	if strings.TrimSpace(msgPart) != "" {
		b.WriteString(`<div style="margin:0 0 18px 0;">`)
		writeHTMLParagraphs(&b, msgPart)
		b.WriteString(`</div>`)
	}

	if hasSig && strings.TrimSpace(sigPart) != "" {
		writeStyledSignatureHTML(&b, sigPart)
	}

	if strings.TrimSpace(quotePart) != "" {
		b.WriteString(`<blockquote style="margin:18px 0 0 0;padding:0 0 0 12px;border-left:2px solid #d0d7de;color:#57606a;font-size:13px;">`)
		writeHTMLParagraphs(&b, quotePart)
		b.WriteString(`</blockquote>`)
	}

	b.WriteString(`</body></html>`)
	return b.String()
}

// splitPlainMailParts separates an outgoing plain-text mail into message body, signature
// (after "-- \n" or "--\n"), and any trailing reply quote / forwarded block.
func splitPlainMailParts(norm string) (msgPart, sigPart, quotePart string, ok bool) {
	lines := strings.Split(norm, "\n")
	sigStart := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "--" {
			sigStart = i
			break
		}
	}
	if sigStart < 0 {
		return norm, "", "", false
	}
	msgLines := lines[:sigStart]
	rest := lines[sigStart+1:]
	// Skip duplicate "--" lines if any slipped in manually.
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "--" {
		rest = rest[1:]
	}

	// Detect where a trailing quoted reply or forwarded message begins after the signature.
	quoteIdx := -1
	for i, ln := range rest {
		tr := strings.TrimSpace(ln)
		if strings.HasPrefix(tr, ">") ||
			strings.HasPrefix(tr, "---------- ") ||
			(strings.HasPrefix(tr, "Am ") && strings.Contains(tr, " schrieb ")) ||
			(strings.HasPrefix(tr, "On ") && strings.Contains(tr, " wrote:")) {
			quoteIdx = i
			break
		}
	}
	var sigLines, quoteLines []string
	if quoteIdx >= 0 {
		sigLines = rest[:quoteIdx]
		quoteLines = rest[quoteIdx:]
	} else {
		sigLines = rest
	}
	return strings.TrimRight(strings.Join(msgLines, "\n"), "\n"),
		strings.TrimSpace(strings.Join(sigLines, "\n")),
		strings.TrimSpace(strings.Join(quoteLines, "\n")),
		true
}

func writeHTMLParagraphs(b *strings.Builder, text string) {
	paras := strings.Split(strings.TrimSpace(text), "\n\n")
	for i, p := range paras {
		lines := strings.Split(p, "\n")
		isList := len(lines) > 0
		for _, ln := range lines {
			tr := strings.TrimSpace(ln)
			if !strings.HasPrefix(tr, "- ") && !strings.HasPrefix(tr, "• ") {
				isList = false
				break
			}
		}
		if isList {
			b.WriteString(`<ul style="margin:8px 0 0 0;padding-left:20px;">`)
			for _, ln := range lines {
				tr := strings.TrimSpace(ln)
				tr = strings.TrimPrefix(strings.TrimPrefix(tr, "- "), "• ")
				b.WriteString(`<li style="margin:2px 0;">`)
				b.WriteString(formatRichInlineHTML(tr))
				b.WriteString(`</li>`)
			}
			b.WriteString(`</ul>`)
			continue
		}
		writeBlock(b, lines, i == 0)
	}
}

func formatRichInlineHTML(line string) string {
	return formatLinksHTML(line, func(s string) string { return applyInlineMarkup(linkifyHTMLText(s)) })
}

// applyInlineMarkup turns `code`, **bold** and *italic* of escaped text into HTML.
func applyInlineMarkup(s string) string {
	s = mdCodeRE.ReplaceAllString(s, `<code style="background:#f3f4f6;padding:1px 4px;border-radius:4px;font-family:ui-monospace,Menlo,monospace;font-size:12.5px;">$1</code>`)
	s = mdBoldRE.ReplaceAllString(s, `<strong>$1</strong>`)
	s = mdItalicRE.ReplaceAllString(s, `<em>$1</em>`)
	return s
}

func linkifyHTMLText(line string) string {
	matches := sigURLRE.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return html.EscapeString(line)
	}
	var b strings.Builder
	pos := 0
	for _, m := range matches {
		b.WriteString(html.EscapeString(line[pos:m[0]]))
		raw := line[m[0]:m[1]]
		trimmed := strings.TrimRight(raw, ".,;:!?)")
		trail := raw[len(trimmed):]
		esc := html.EscapeString(trimmed)
		b.WriteString(`<a href="`)
		b.WriteString(esc)
		b.WriteString(`" style="color:#9d1140;text-decoration:none;">`)
		b.WriteString(esc)
		b.WriteString(`</a>`)
		b.WriteString(html.EscapeString(trail))
		pos = m[1]
	}
	b.WriteString(html.EscapeString(line[pos:]))
	return b.String()
}

func writeStyledSignatureHTML(b *strings.Builder, sig string) {
	// Split optional legal/secondary footer separated by a blank line.
	sections := strings.Split(sig, "\n\n")
	primaryLines := nonDividerLines(sections[0])
	var legalLines []string
	for _, sec := range sections[1:] {
		legalLines = append(legalLines, nonDividerLines(sec)...)
	}

	b.WriteString(`<div style="margin-top:20px;padding-left:12px;border-left:3px solid #9d1140;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">`)
	for i, ln := range primaryLines {
		switch {
		case i == 0:
			b.WriteString(`<div style="font-size:14px;font-weight:700;color:#1f2328;line-height:1.35;">`)
			b.WriteString(html.EscapeString(ln))
			b.WriteString(`</div>`)
		case i == 1 && !looksLikeContactField(ln) && !strings.Contains(ln, "·"):
			// Company / role subtitle directly under name (e.g. "Acme Corp").
			b.WriteString(`<div style="font-size:12.5px;font-weight:600;color:#57606a;margin-top:1px;line-height:1.35;">`)
			b.WriteString(html.EscapeString(ln))
			b.WriteString(`</div>`)
		default:
			b.WriteString(`<div style="font-size:12px;color:#57606a;margin-top:3px;line-height:1.45;">`)
			b.WriteString(formatSigLineHTML(ln))
			b.WriteString(`</div>`)
		}
	}
	if len(legalLines) > 0 {
		b.WriteString(`<div style="margin-top:10px;padding-top:8px;border-top:1px solid #d0d7de;font-size:11px;color:#6e7781;line-height:1.45;">`)
		for i, ln := range legalLines {
			if i > 0 {
				b.WriteString(`<br>`)
			}
			b.WriteString(html.EscapeString(ln))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
}

func nonDividerLines(block string) []string {
	var out []string
	for _, raw := range strings.Split(block, "\n") {
		ln := strings.TrimSpace(raw)
		if ln == "" || sigDividerLineRE.MatchString(ln) {
			continue
		}
		out = append(out, ln)
	}
	return out
}

func looksLikeContactField(ln string) bool {
	if _, _, ok := splitLabeledContact(ln); ok {
		return true
	}
	return sigEmailRE.MatchString(ln) || sigURLRE.MatchString(ln)
}

func splitLabeledContact(ln string) (label, val string, ok bool) {
	fields := strings.Fields(ln)
	if len(fields) < 2 {
		return "", "", false
	}
	head := strings.TrimRight(fields[0], ".:")
	switch strings.ToUpper(head) {
	case "T", "TEL", "TELEFON", "PHONE", "M", "MOBIL", "MOBILE", "F", "FAX", "E", "EMAIL", "E-MAIL", "W", "WEB":
		rest := strings.TrimSpace(strings.TrimPrefix(ln, fields[0]))
		return fields[0], rest, rest != ""
	}
	return "", "", false
}

func formatSigLineHTML(ln string) string {
	if lbl, val, ok := splitLabeledContact(ln); ok {
		head := strings.ToUpper(strings.TrimRight(lbl, ".:"))
		prefix := `<span style="display:inline-block;min-width:18px;color:#6e7781;font-weight:600;">` + html.EscapeString(lbl) + `</span> `
		switch head {
		case "T", "TEL", "TELEFON", "PHONE", "M", "MOBIL", "MOBILE":
			digits := sigPhoneCleanRE.ReplaceAllString(val, "")
			if len(digits) >= 5 {
				return prefix + `<a href="tel:` + html.EscapeString(digits) + `" style="color:#9d1140;text-decoration:none;font-weight:500;">` + html.EscapeString(val) + `</a>`
			}
		case "F", "FAX":
			return prefix + `<span style="color:#1f2328;">` + html.EscapeString(val) + `</span>`
		case "E", "EMAIL", "E-MAIL":
			if em := sigEmailRE.FindString(val); em != "" {
				return prefix + `<a href="mailto:` + html.EscapeString(em) + `" style="color:#9d1140;text-decoration:none;font-weight:500;">` + html.EscapeString(val) + `</a>`
			}
		case "W", "WEB":
			if u := sigURLRE.FindString(val); u != "" {
				return prefix + `<a href="` + html.EscapeString(u) + `" style="color:#9d1140;text-decoration:none;font-weight:500;">` + html.EscapeString(val) + `</a>`
			}
		}
		return prefix + html.EscapeString(val)
	}
	if sigURLRE.MatchString(ln) {
		return linkifyHTMLText(ln)
	}
	if em := sigEmailRE.FindString(ln); em != "" && em == strings.TrimSpace(ln) {
		esc := html.EscapeString(em)
		return `<a href="mailto:` + esc + `" style="color:#9d1140;text-decoration:none;font-weight:500;">` + esc + `</a>`
	}
	return html.EscapeString(ln)
}
