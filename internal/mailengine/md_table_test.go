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

func TestExtractInlineDataURLImagesAndBuildRelatedMIME(t *testing.T) {
	// 1x1 transparent PNG in base64
	const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	htmlIn := `<p>Hallo,</p><p><img src="data:image/png;base64,` + tinyPNG + `" alt="shot"></p>`
	cleaned, inline := ExtractInlineDataURLImages(htmlIn)
	if len(inline) != 1 || inline[0].ContentType != "image/png" || len(inline[0].Data) == 0 {
		t.Fatalf("expected 1 extracted inline PNG, got %+v", inline)
	}
	if !strings.Contains(cleaned, `src="cid:`+inline[0].ContentID+`"`) || strings.Contains(cleaned, "data:image/") {
		t.Fatalf("expected cid reference replacing data URL, got %s", cleaned)
	}

	_, raw := BuildRawMessage(OutgoingMessage{
		FromEmail:    "sender@example.com",
		To:           []string{"rcpt@example.com"},
		Subject:      "Inline Bild",
		Body:         "Hallo",
		BodyHTML:     cleaned,
		InlineImages: inline,
	})
	rawStr := string(raw)
	for _, want := range []string{
		"multipart/alternative",
		"multipart/related",
		"Content-ID: <" + inline[0].ContentID + ">",
		"Content-Disposition: inline",
	} {
		if !strings.Contains(rawStr, want) {
			t.Fatalf("missing %q in raw MIME:\n%s", want, rawStr)
		}
	}
	parsed, err := ParseRawMessage(raw)
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}
	if !strings.Contains(parsed.BodyHTML, "cid:"+inline[0].ContentID) {
		t.Fatalf("parsed BodyHTML lost cid reference: %s", parsed.BodyHTML)
	}
}
