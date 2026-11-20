package mailengine

import (
	"html"
	"regexp"
	"strings"
)

// Tables and links in outgoing mail. Compose writes Markdown: a table is a block of lines that
// start with "|", its second line a separator ("|---|:--:|"), the first line the header. A cell
// may hold "<br>" for a line break and "\|" for a pipe. Links are [text](https://…).

var (
	mdLinkRE      = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	mdTableSepRE  = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	cellBreakHTML = regexp.MustCompile(`(?i)&lt;br\s*/?&gt;`)
)

// hasMarkdownTable reports a table anywhere in text.
func hasMarkdownTable(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if isTableLine(lines[i]) && mdTableSepRE.MatchString(strings.TrimSpace(lines[i+1])) {
			return true
		}
	}
	return false
}

func isTableLine(ln string) bool {
	t := strings.TrimSpace(ln)
	return strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2
}

// splitTableCells splits "| a | b \| c |" into its cells.
func splitTableCells(ln string) []string {
	t := strings.TrimSpace(ln)
	t = strings.TrimPrefix(t, "|")
	if strings.HasSuffix(t, "|") && !strings.HasSuffix(t, `\|`) {
		t = t[:len(t)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(t); i++ {
		switch {
		case t[i] == '\\' && i+1 < len(t) && t[i+1] == '|':
			cur.WriteByte('|')
			i++
		case t[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(t[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

const (
	tableStyle = `border-collapse:collapse;margin:10px 0;font-size:13px;line-height:1.4;`
	thStyle    = `border:1px solid #b7bcc4;background:#dde4f2;padding:5px 9px;text-align:left;font-weight:600;vertical-align:top;`
	tdStyle    = `border:1px solid #b7bcc4;padding:5px 9px;text-align:left;vertical-align:top;`
)

// writeHTMLTable writes the table in lines (header, separator, rows).
func writeHTMLTable(b *strings.Builder, lines []string) {
	header := splitTableCells(lines[0])
	cols := len(header)
	cell := func(tag, style, text string) {
		b.WriteString("<" + tag + ` style="` + style + `">`)
		b.WriteString(cellBreakHTML.ReplaceAllString(formatRichInlineHTML(text), "<br>"))
		b.WriteString("</" + tag + ">")
	}
	b.WriteString(`<table style="` + tableStyle + `" cellpadding="0" cellspacing="0"><thead><tr>`)
	for _, h := range header {
		cell("th", thStyle, h)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, ln := range lines[2:] {
		cells := splitTableCells(ln)
		b.WriteString(`<tr>`)
		for i := 0; i < cols; i++ {
			v := ""
			if i < len(cells) {
				v = cells[i]
			}
			if i == cols-1 && len(cells) > cols {
				v = strings.Join(cells[i:], " | ")
			}
			cell("td", tdStyle, v)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
}

// writeBlock writes one paragraph's lines, as tables where they form one and as text (with line
// breaks) elsewhere.
func writeBlock(b *strings.Builder, lines []string, first bool) {
	var text []string
	flushText := func() {
		if len(text) == 0 {
			return
		}
		if first {
			b.WriteString(`<p style="margin:0;">`)
		} else {
			b.WriteString(`<p style="margin:10px 0 0 0;">`)
		}
		for j, ln := range text {
			if j > 0 {
				b.WriteString(`<br>`)
			}
			b.WriteString(formatRichInlineHTML(ln))
		}
		b.WriteString(`</p>`)
		text, first = nil, false
	}
	for i := 0; i < len(lines); {
		if i+1 < len(lines) && isTableLine(lines[i]) && mdTableSepRE.MatchString(strings.TrimSpace(lines[i+1])) {
			flushText()
			end := i + 2
			for end < len(lines) && isTableLine(lines[end]) {
				end++
			}
			writeHTMLTable(b, lines[i:end])
			first = false
			i = end
			continue
		}
		text = append(text, lines[i])
		i++
	}
	flushText()
}

// formatLinksHTML writes [text](url) as links and the rest with fmt.
func formatLinksHTML(line string, fmtText func(string) string) string {
	ms := mdLinkRE.FindAllStringSubmatchIndex(line, -1)
	if len(ms) == 0 {
		return fmtText(line)
	}
	var b strings.Builder
	pos := 0
	for _, m := range ms {
		b.WriteString(fmtText(line[pos:m[0]]))
		label, href := line[m[2]:m[3]], line[m[4]:m[5]]
		b.WriteString(`<a href="` + html.EscapeString(href) + `" style="color:#0969da;">`)
		b.WriteString(applyInlineMarkup(html.EscapeString(label)))
		b.WriteString(`</a>`)
		pos = m[1]
	}
	b.WriteString(fmtText(line[pos:]))
	return b.String()
}
