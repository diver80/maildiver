package nativeui

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"golang.org/x/net/html"
)

// Tables in compose: the body is Markdown, and the engine sends a pipe table as an HTML table.
// "Tabelle" takes a table from the clipboard – HTML (Confluence, a web page, Word) or text with
// tabs (Excel, Numbers) – keeping links, or inserts an empty one.

const composeTableTemplate = "| Spalte 1 | Spalte 2 | Spalte 3 |\n|---|---|---|\n|  |  |  |\n|  |  |  |"

// insertComposeTable adds a table at the end of the text, before the signature (UI thread).
func (a *App) insertComposeTable() {
	go func() {
		md := ""
		if raw, err := mygo.Clipboard.ReadFormat(transfer.HTML); err == nil {
			md = markdownTableFromHTML(string(raw))
		}
		if md == "" {
			if raw, err := mygo.Clipboard.ReadFormat(transfer.Text); err == nil {
				md = markdownTableFromTSV(string(raw))
			}
		}
		a.emitUpdate(func() {
			if md == "" {
				md = composeTableTemplate
				if a.uiLang() != "de" {
					md = strings.NewReplacer("Spalte", "Column").Replace(md)
				}
			} else {
				a.toast(a.tr("Tabelle aus der Zwischenablage eingefügt", "Table inserted from the clipboard"), "success")
			}
			a.composeBody = insertComposeSnippet(a.composeBody, "\n\n"+md+"\n")
		})
	}()
}

// markdownTableFromHTML converts the first table in an HTML fragment ("" without one).
func markdownTableFromHTML(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return ""
	}
	table := findElement(doc, "table")
	if table == nil {
		return ""
	}
	var rows [][]string
	var walkRows func(n *html.Node)
	walkRows = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "table":
				// A table inside a cell belongs to that cell.
			case "tr":
				var row []string
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
						row = append(row, cellMarkdown(cell))
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			default:
				walkRows(c)
			}
		}
	}
	walkRows(table)
	return markdownTable(rows)
}

func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findElement(c, tag); f != nil {
			return f
		}
	}
	return nil
}

// cellMarkdown writes a cell's content on one line: links as [text](url), line breaks and
// paragraphs as <br>.
func cellMarkdown(cell *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	brk := func() {
		s := strings.TrimRight(b.String(), " ")
		if s != "" && !strings.HasSuffix(s, "<br>") {
			b.Reset()
			b.WriteString(s + "<br>")
		}
	}
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(strings.Join(strings.Fields(n.Data), " "))
			if strings.HasSuffix(n.Data, " ") || strings.HasSuffix(n.Data, "\n") {
				b.WriteString(" ")
			}
			return
		case n.Type != html.ElementNode && n.Type != html.DocumentNode:
			return
		}
		switch n.Data {
		case "br":
			brk()
			return
		case "style", "script":
			return
		case "a":
			href := ""
			for _, at := range n.Attr {
				if at.Key == "href" {
					href = strings.TrimSpace(at.Val)
				}
			}
			var inner strings.Builder
			var text func(*html.Node)
			text = func(x *html.Node) {
				if x.Type == html.TextNode {
					inner.WriteString(x.Data)
				}
				for c := x.FirstChild; c != nil; c = c.NextSibling {
					text(c)
				}
			}
			text(n)
			label := strings.Join(strings.Fields(inner.String()), " ")
			if (strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://")) && label != "" {
				b.WriteString("[" + strings.NewReplacer("[", "(", "]", ")").Replace(label) + "](" + strings.ReplaceAll(href, " ", "%20") + ")")
			} else {
				b.WriteString(label)
			}
			return
		}
		block := n.Data == "p" || n.Data == "div" || n.Data == "li" || n.Data == "tr"
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			brk()
		}
	}
	for c := cell.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	s := strings.TrimSpace(b.String())
	s = strings.TrimSuffix(s, "<br>")
	return strings.TrimSpace(strings.ReplaceAll(s, "|", `\|`))
}

// markdownTableFromTSV converts text with tabs between columns (two lines or more).
func markdownTableFromTSV(src string) string {
	src = strings.ReplaceAll(strings.TrimRight(src, "\r\n"), "\r\n", "\n")
	lines := strings.Split(src, "\n")
	if len(lines) < 2 {
		return ""
	}
	var rows [][]string
	for _, ln := range lines {
		if !strings.Contains(ln, "\t") {
			return ""
		}
		cells := strings.Split(ln, "\t")
		for i, c := range cells {
			cells[i] = strings.ReplaceAll(strings.TrimSpace(c), "|", `\|`)
		}
		rows = append(rows, cells)
	}
	return markdownTable(rows)
}

// markdownTable writes rows as a pipe table, the first row as its header.
func markdownTable(rows [][]string) string {
	if len(rows) < 2 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols < 2 {
		return ""
	}
	var b strings.Builder
	line := func(r []string) {
		b.WriteString("|")
		for i := 0; i < cols; i++ {
			v := ""
			if i < len(r) {
				v = r[i]
			}
			b.WriteString(" " + v + " |")
		}
		b.WriteString("\n")
	}
	line(rows[0])
	b.WriteString("|" + strings.Repeat("---|", cols) + "\n")
	for _, r := range rows[1:] {
		line(r)
	}
	return strings.TrimRight(b.String(), "\n")
}
