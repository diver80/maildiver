package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/backend"
)

// confluenceHTML is shaped like Atlassian's notification mails: a hidden preheader, layout
// tables, a tracking pixel, a remote logo, links whose text is a title or an address.
const confluenceHTML = `<!DOCTYPE html><html><head><title>x</title><style>.a{color:red}</style></head><body>
<div style="display:none;max-height:0;overflow:hidden">Stay connected to people &zwnj;&nbsp;&zwnj;&nbsp;&zwnj;</div>
<table width="100%"><tr><td><img src="https://cdn.example.net/logo.png" width="120" height="40" alt="Confluence"></td></tr>
<tr><td>
  <h1 style="font-size:24px">Your weekly content recommendations</h1>
  <p>Stay connected to people and their work.<br>Your recommendations, ready to explore.</p>
  <table><tr><td><img src="https://cdn.example.net/page.png" width="16" height="16" alt="Page icon"></td>
  <td><a href="https://example.atlassian.net/wiki/spaces/GS/pages/2326593582/Acme+SDK?atl_f=FLLWEML&amp;tid=3ccc0633">Acme SDK Release Data Center (2.0.0)</a></td></tr></table>
  <p>Owned by <b>Doe, Jane</b> · <a href="https://example.atlassian.net/wiki/people/5fad7f6c">https://example.atlassian.net/wiki/people/5fad7f6c</a></p>
  <p><a href="https://example.net/go"><img src="https://cdn.example.net/btn.png" alt="Open Confluence"></a></p>
  <p><a href="javascript:alert(1)">Click me</a> and more</p>
  <div><a href="https://example.net/x"><div>Card title</div><div>Card text</div></a></div>
  <ul><li>First <i>item</i></li><li>Second<ul><li>Nested</li></ul></li></ul>
  <ol><li>One</li><li>Two</li></ol>
  <table><tr><th>Status</th><td>Open</td></tr><tr><td>Assignee</td><td>Frank</td></tr></table>
  <blockquote><p>Quoted line one</p><p>Quoted line two</p></blockquote>
  <pre>code  block
  indented</pre>
  <p>Literal *stars* _under_ 1. # [x](y) &lt;b&gt;tag&lt;/b&gt; \(math\) $$x$$ | pipe ~~s~~ www.firma.de</p>
  <hr>
  <script>alert(1)</script>
  <img src="https://track.example.net/open.gif" width="1" height="1">
</td></tr></table>
<hr>
</body></html>`

func TestHTMLMailMarkdown(t *testing.T) {
	md, blocked := htmlMailMarkdown(confluenceHTML)
	t.Logf("markdown:\n%s", md)
	if blocked != 2 {
		t.Errorf("blocked images = %d, want 2 (logo, page icon; not the pixel, not the linked button)", blocked)
	}
	for _, want := range []string{
		"### Your weekly content recommendations",
		"Stay connected to people and their work\\.<br>Your recommendations, ready to explore\\.",
		"[Acme SDK Release Data Center (2\\.0\\.0)](https://example.atlassian.net/wiki/spaces/GS/pages/2326593582/Acme+SDK?atl_f=FLLWEML&tid=3ccc0633)",
		"Owned by <b>Doe, Jane</b> · [example\\.atlassian\\.net/…](https://example.atlassian.net/wiki/people/5fad7f6c)",
		"[Open Confluence](https://example.net/go)",
		"Click me and more",
		"[Card title Card text](https://example.net/x)",
		"- First <i>item</i>\n- Second\n    - Nested\n\n1. One\n2. Two",
		"<b>Status</b> · Open",
		"Assignee · Frank",
		"> Quoted line one\n>\n> Quoted line two",
		"```\ncode  block\n  indented\n```",
		"Literal \\*stars\\* \\_under\\_ 1\\. \\# \\[x\\](y) \\<b\\>tag\\</b\\> \\\\(math\\\\) \\$\\$x\\$\\$ \\| pipe \\~\\~s\\~\\~ [firma\\.de](https://www.firma.de)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	for _, bad := range []string{"Stay connected to people &", "alert", "javascript", "track.example", "logo.png", "‌", "Confluence\n"} {
		if strings.Contains(md, bad) {
			t.Errorf("markdown contains %q", bad)
		}
	}
	if strings.HasSuffix(strings.TrimSpace(md), "---") {
		t.Error("a trailing rule should be dropped")
	}
}

// Jira change mails: "~~Unassigned~~ [arrow image] Max Muster"; footers: links in a row.
func TestInlineDetails(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<p>Assignee: <del>Unassigned</del><img src="https://x.example/arrow.png" width="16" height="16">Max Muster</p>`,
			"Assignee: <s>Unassigned</s> Max Muster"},
		{`<p><span style="text-decoration: line-through">alt</span><img src="https://x.example/a.gif" width="1" height="1">neu</p>`,
			"<s>alt</s> neu"},
		{`<p><s>a</s> and <strike>b</strike></p>`, "<s>a</s> and <s>b</s>"},
		{`<p><a href="https://a.example/n">Manage Notifications</a> <a href="https://a.example/f">Give feedback</a>&nbsp;<a href="https://a.example/p">Privacy Policy</a></p>`,
			"[Manage Notifications](https://a.example/n) · [Give feedback](https://a.example/f) · [Privacy Policy](https://a.example/p)"},
		{`<p><a href="https://a.example/x">One</a><a href="https://a.example/y">Two</a></p>`,
			"[One](https://a.example/x) · [Two](https://a.example/y)"},
		{`<p><a href="https://a.example/x">One</a> or <a href="https://a.example/y">Two</a></p>`,
			"[One](https://a.example/x) or [Two](https://a.example/y)"},
		{`<p><a href="https://a.example/x">One</a><br><a href="https://a.example/y">Two</a></p>`,
			"[One](https://a.example/x)<br>[Two](https://a.example/y)"},
	} {
		md, _ := htmlMailMarkdown(tc.in)
		if md != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.in, md, tc.want)
		}
	}
}

func TestOrderedListMarkers(t *testing.T) {
	md, _ := htmlMailMarkdown(`<ol><li>One</li><li>Two</li></ol><ul><li>a</li></ul>`)
	if !strings.Contains(md, "1. One\n2. Two") {
		t.Errorf("ordered list:\n%s", md)
	}
}

func TestPlainMailMarkdown(t *testing.T) {
	src := "Hallo Frank,\r\n\r\ndie Kalkulation:\n- Lizenzen: 14.200 EUR\n  eingerückt\n\nAcme SDK Release Data Center (2.0.0)\n[https://example.atlassian.net/wiki/spaces/GS/pages/1?atl_f=x]\n\n" +
		"Doe, Jane\n[https://secure.gravatar.com/avatar/d08?d=x]https://example.atlassian.net/wiki/people/5fad?x=1\n" +
		"Siehe https://example.com/a/b. Oder <https://example.org> und (https://example.net/x_(y)).\n\n" +
		"> Zitat eins\n> > tiefer\n\n-- \nAlice"
	md := plainMailMarkdown(src)
	t.Logf("markdown:\n%s", md)
	for _, want := range []string{
		"Hallo Frank,",
		"die Kalkulation:<br>\\- Lizenzen: 14\\.200 EUR<br>  eingerückt",
		"[Acme SDK Release Data Center (2\\.0\\.0)](https://example.atlassian.net/wiki/spaces/GS/pages/1?atl_f=x)",
		"Doe, Jane<br>[secure\\.gravatar\\.com/…](https://secure.gravatar.com/avatar/d08?d=x) · [example\\.atlassian\\.net/…](https://example.atlassian.net/wiki/people/5fad?x=1)",
		"Siehe [example\\.com/…](https://example.com/a/b)\\. Oder [example\\.org](https://example.org) und ([example\\.net/…](https://example.net/x_%28y%29))\\.",
		"> Zitat eins",
		"> > tiefer",
		"\\-\\-<br>Alice",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	if strings.Contains(md, "[https") || strings.Contains(md, "]https") {
		t.Error("raw bracketed URLs should become links")
	}
}

func TestBuildReaderBodyPrefersHTML(t *testing.T) {
	msg := &backend.MessageDetail{BodyText: "plain [https://x.example/a]", BodyHtml: "<p>Hello <b>you</b></p><img src='https://x.example/i.png'>"}
	msg.ID = "m1"
	b := buildReaderBody(msg)
	if !b.FromHTML || b.Markdown != "Hello <b>you</b>" || b.BlockedImages != 1 {
		t.Errorf("html body = %+v", b)
	}
	if !strings.Contains(b.Plain, "[x\\.example/…](https://x.example/a)") {
		t.Errorf("plain version = %q", b.Plain)
	}

	// HTML without visible text (only hidden parts, images): the text version shows.
	msg.BodyHtml = `<div style="display:none">pre</div><img src="https://x.example/i.png">`
	if b := buildReaderBody(msg); b.FromHTML || b.Markdown != b.Plain {
		t.Errorf("empty html should fall back to text: %+v", b)
	}
	// HTML nested deeper than the parser allows: the text version shows.
	msg.BodyHtml = strings.Repeat("<div><table><tr><td>", 200) + "deep" + strings.Repeat("</td></tr></table></div>", 200)
	if b := buildReaderBody(msg); b.FromHTML {
		t.Error("html the parser refuses should fall back to text")
	}
	// Oversized HTML: the text version shows.
	msg.BodyHtml = "<p>" + strings.Repeat("a", maxBodyHTMLBytes) + "</p>"
	if b := buildReaderBody(msg); b.FromHTML {
		t.Error("oversized html should fall back to text")
	}
}

func TestSafeHrefAndShortURL(t *testing.T) {
	for in, want := range map[string]string{
		" https://a.example/x ":   "https://a.example/x",
		"mailto:bob@example.com":  "mailto:bob@example.com",
		"javascript:alert(1)":     "",
		"JaVaScRiPt:alert(1)":     "",
		"data:text/html,<b>x</b>": "",
		"/relative":               "",
		"cid:part1":               "",
		"https://":                "",
		"http://a\nb":             "",
	} {
		if got := safeHref(in); got != want {
			t.Errorf("safeHref(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"https://www.Example.com":          "example.com",
		"https://example.com/":             "example.com",
		"https://example.com/a?b=c":        "example.com/…",
		"mailto:bob@example.com?subject=x": "bob@example.com",
	} {
		if got := shortURL(in); got != want {
			t.Errorf("shortURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The reading pane renders the converted body: link titles instead of addresses, formatting
// applied rather than shown as tags, escapes shown literally, a note about blocked images, the
// text version one click away, and single keys still working after a click into the text.
func TestMailBodyRendersHeadless(t *testing.T) {
	app, loop, _ := keymapApp(t)
	app.selectedID = "m1"
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		BodyText:      "Plain version\n[https://plain.example/doc]",
		BodyHtml:      confluenceHTML,
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1400)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"Acme SDK Release Data Center (2.0.0)", "Your weekly content recommendations", "2 Bilder nicht geladen", "Formatiert"} {
		if !strings.Contains(texts, want) {
			t.Errorf("rendered body lacks %q; texts:\n%s", want, texts)
		}
	}
	// "<b>tag</b>" is in the mail's text on purpose; the converter's own tags must not show.
	for _, bad := range []string{"https://example", "<b>Doe", "<i>item", "<br>", "\\*", "Stay connected to people &"} {
		if strings.Contains(texts, bad) {
			t.Errorf("rendered body shows %q", bad)
		}
	}
	if !strings.Contains(texts, "Literal *stars* _under_ 1. # [x](y) <b>tag</b>") {
		t.Errorf("escaped text should show literally; texts:\n%s", texts)
	}

	// The text version.
	if err := tt.Click("Text"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.readerPlain || !tt.HasText("Plain version") || tt.HasText("Your weekly content recommendations") {
		t.Fatalf("text version expected, plain=%v texts=%v", app.readerPlain, tt.Texts())
	}
	if err := tt.Click("Formatiert"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	// A click into the body text keeps j working.
	r, ok := tt.Find("Your weekly content recommendations")
	if !ok {
		t.Fatal("heading not found")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Key(0, ui.KeyJ)
	loop.drain()
	tt.Frame()
	if app.selectedID != "m2" {
		t.Fatalf("j after clicking the body must open the next mail, open=%q", app.selectedID)
	}
}

func TestMailSourceViewShowsHeadersAndRawPlaintext(t *testing.T) {
	app, _, _ := keymapApp(t)
	app.selectedID = "m1"
	app.selectedMsg = &backend.MessageDetail{
		MessageHeader: app.messages[0],
		Headers: map[string]string{
			"x-jira-fingerprint": "abc123fingerprint",
			"list-id":            "<dev.lists.example.org>",
		},
		BodyText: "Raw plaintext body line",
		BodyHtml: confluenceHTML,
	}
	tt := ui.NewTester(app.RenderMain, 1380, 1400)
	tt.Frame()

	// Switch to Quelltext (Source) view via the segmented button.
	if err := tt.Click("Quelltext"); err != nil {
		t.Fatalf("click Quelltext: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	if !app.readerSource {
		t.Fatal("expected readerSource=true after clicking Quelltext")
	}
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"x-jira-fingerprint: abc123fingerprint", "list-id: <dev.lists.example.org>", "Raw plaintext body line", "Nur Header", "Kopieren"} {
		if !strings.Contains(texts, want) {
			t.Errorf("source view missing %q; texts:\n%s", want, texts)
		}
	}

	// Switch to Headers-only submode + filter by "jira".
	if err := tt.Click("Nur Header"); err != nil {
		t.Fatalf("click Nur Header: %v", err)
	}
	app.readerSourceFilter = "jira"
	tt.Frame()
	texts = strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "x-jira-fingerprint: abc123fingerprint") || strings.Contains(texts, "Raw plaintext body line") {
		t.Errorf("headers-only filtered view unexpected; texts:\n%s", texts)
	}

	// Return to Formatiert cleanly.
	if err := tt.Click("Formatiert"); err != nil {
		t.Fatalf("click Formatiert: %v", err)
	}
	tt.Frame()
	if app.readerSource || app.readerPlain {
		t.Fatalf("expected Formatiert mode restored, source=%v plain=%v", app.readerSource, app.readerPlain)
	}
}
