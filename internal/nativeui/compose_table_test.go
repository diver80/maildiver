package nativeui

import (
	"strings"
	"testing"
)

func TestMarkdownTableFromConfluenceHTML(t *testing.T) {
	src := `<meta charset="utf-8"><table><colgroup><col></colgroup><tbody>
<tr><th><p>Nr.</p></th><th><p>Seite (Diagramm)</p></th><th>Version</th><th>Korrigierte Link-IDs</th></tr>
<tr><td>10</td><td><p><a href="https://wiki.example.org/pages/10">2_04_03_04_02_Maßnahmenwert Größer 100KBE/100ml</a></p><p>Diagramm: 2_04_13_02</p></td><td>v12 → v13</td><td>1d5rm51, 0uy1385</td></tr>
<tr><td>11</td><td>A | B</td><td>v26 → v27</td><td>1k6qoc8 (2×)</td></tr>
</tbody></table>`
	got := markdownTableFromHTML(src)
	want := "| Nr. | Seite (Diagramm) | Version | Korrigierte Link-IDs |\n" +
		"|---|---|---|---|\n" +
		"| 10 | [2_04_03_04_02_Maßnahmenwert Größer 100KBE/100ml](https://wiki.example.org/pages/10)<br>Diagramm: 2_04_13_02 | v12 → v13 | 1d5rm51, 0uy1385 |\n" +
		"| 11 | A \\| B | v26 → v27 | 1k6qoc8 (2×) |"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if markdownTableFromHTML("<p>kein Tisch</p>") != "" {
		t.Fatal("HTML without a table gives nothing")
	}
}

func TestMarkdownTableFromTSV(t *testing.T) {
	got := markdownTableFromTSV("Nr.\tSeite\tVersion\n1\tAusschreibung\tv26 → v27\n2\tBauprojekt\n")
	if !strings.HasPrefix(got, "| Nr. | Seite | Version |\n|---|---|---|\n| 1 | Ausschreibung | v26 → v27 |\n| 2 | Bauprojekt |  |") {
		t.Fatalf("got\n%s", got)
	}
	if markdownTableFromTSV("ein Satz ohne Tabs\nnoch einer") != "" {
		t.Fatal("text without tabs is no table")
	}
}

// A data table in a received mail shows as a table; a table that lays out a newsletter stays text.
func TestReaderShowsDataTables(t *testing.T) {
	data := `<p>Auf Produktion:</p><table border="1"><tr><th>Nr.</th><th>Seite</th><th>Version</th></tr>
<tr><td>1</td><td><a href="https://wiki.example.org/x/1">1_08_01_Ausschreibung</a></td><td>v26 → v27</td></tr>
<tr><td>2</td><td>A | B</td><td>v12 → v13</td></tr></table><p>VG</p>`
	md, _ := htmlMailMarkdown(data)
	for _, want := range []string{"| Nr\\. | Seite | Version |", "| --- | --- | --- |", "| 2 | A \\| B | v12 → v13 |", "](https://wiki.example.org/x/1)"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	layout := `<table><tr><td><img src="https://cdn.example.org/logo.png" width="200" height="60"></td><td>Sale</td></tr>
<tr><td><table><tr><td>a</td><td>b</td></tr></table></td><td>x</td></tr></table>`
	if md, _ := htmlMailMarkdown(layout); strings.Contains(md, "| --- |") {
		t.Fatalf("a layout table must not become a table:\n%s", md)
	}
}
