package mailengine

import (
	"strings"
	"testing"
)

func TestOutgoingMarkdownTable(t *testing.T) {
	body := "Hallo Erika,\n\nAuf Produktion wurden folgende Diagramme angepasst:\n" +
		"| Nr. | Seite (Diagramm) | Version | Korrigierte Link-IDs |\n" +
		"|---|---|---|---|\n" +
		"| 1 | [1_08_01_Ausschreibung](https://wiki.example.org/x/1) | v26 → v27 | 1e0i2zp |\n" +
		"| 10 | [2_04_03_04_02_Maßnahmenwert](https://wiki.example.org/x/10)<br>Diagramm: 2_04_13_02 | v12 → v13 | 1d5rm51, 0uy1385 |\n" +
		"| 2 | a \\| b | <script>x</script> |\n" +
		"Durch die Speicherung stehen die Seiten im Freigabe-Workflow.\n\nVG\nFrank"
	h := BuildStyledEmailHTML(body)
	for _, want := range []string{
		"<table", "<th style=", ">Nr.</th>", ">Korrigierte Link-IDs</th>",
		`<a href="https://wiki.example.org/x/1"`, ">1_08_01_Ausschreibung</a>",
		"Maßnahmenwert</a><br>Diagramm: 2_04_13_02",
		"<td style=\"" + tdStyle + "\">a | b</td>",
		"&lt;script&gt;",
		"Auf Produktion wurden folgende Diagramme angepasst:</p><table",
		"</table><p", "Freigabe-Workflow.",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
	if strings.Contains(h, "<script>") {
		t.Fatal("cell content must be escaped")
	}
	// A missing cell is an empty one; the row keeps the table's width.
	if strings.Count(h, "<tr>") != 4 || strings.Count(h, "<td ") != 12 {
		t.Errorf("rows/cells: %d/%d", strings.Count(h, "<tr>"), strings.Count(h, "<td "))
	}
}

func TestPipeTextWithoutSeparatorIsNoTable(t *testing.T) {
	if hasMarkdownTable("| nur eine Zeile |\nweiter") {
		t.Fatal("a pipe line without a separator is text")
	}
	if h := BuildStyledEmailHTML("Link: [Doku](https://example.org/doc)"); !strings.Contains(h, `<a href="https://example.org/doc"`) {
		t.Fatalf("markdown link: %s", h)
	}
}
