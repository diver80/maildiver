package mailengine

import (
	"strings"
	"testing"
)

func TestBuildStyledEmailHTML(t *testing.T) {
	// Unsigned plain mail -> returns empty so BuildRawMessage keeps lightweight text/plain.
	if got := BuildStyledEmailHTML("Hallo, bis morgen!"); got != "" {
		t.Fatalf("expected empty HTML for unsigned mail, got %q", got)
	}

	// Signed corporate mail with legal footer + quoted reply.
	body := "Hallo Team,\n\nbitte prueft <script>alert(1)</script> unter https://www.acme.example/docs.\n\n" +
		"-- \n" +
		"Alex Example\n" +
		"Acme Corp AG\n" +
		"───────────────\n" +
		"Musterstraße 1 · 10115 Berlin · Germany\n" +
		"T  +49 30 5550101\n" +
		"F  +49 30 5550102\n" +
		"M  +49 170 5550199\n" +
		"E  alex@acme.example\n" +
		"W  https://www.acme.example\n\n" +
		"Sitz: Berlin · Amtsgericht Charlottenburg, HRB 123456 · USt-IdNr.: DE000000000\n" +
		"Vorstand: Erika Muster (Vors.), Max Beispiel · Aufsichtsrat: Petra Demo\n\n" +
		"Am 20.11.2026 schrieb Max:\n> Hallo Alex"

	out := BuildStyledEmailHTML(body)
	if out == "" {
		t.Fatal("expected styled HTML alternative for signed mail")
	}
	if strings.Contains(out, "<script>") {
		t.Fatal("HTML output must escape raw tags")
	}
	for _, want := range []string{
		"border-left:3px solid #9d1140",
		`href="tel:+49305550101"`,
		`href="tel:+491705550199"`,
		`href="mailto:alex@acme.example"`,
		`href="https://www.acme.example"`,
		"HRB 123456",
		"<blockquote",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in generated HTML:\n%s", want, out)
		}
	}
	if strings.Contains(out, "───────────────") {
		t.Fatal("divider line should be replaced by left accent bar in HTML signature")
	}
}
