package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
)

type fakePreviewHost struct {
	loaded  []string
	current string
	back    bool
	fwd     bool
	loading bool
}

func (f *fakePreviewHost) Widget() ui.NativeWebView { return nil }
func (f *fakePreviewHost) LoadURL(rawURL string) error {
	f.loaded = append(f.loaded, rawURL)
	f.current = rawURL
	return nil
}
func (f *fakePreviewHost) GoBack()            {}
func (f *fakePreviewHost) GoForward()         {}
func (f *fakePreviewHost) Reload()            {}
func (f *fakePreviewHost) CanGoBack() bool    { return f.back }
func (f *fakePreviewHost) CanGoForward() bool { return f.fwd }
func (f *fakePreviewHost) IsLoading() bool    { return f.loading }
func (f *fakePreviewHost) CurrentURL() string { return f.current }

func TestExtractMessageLinksAndSecurity(t *testing.T) {
	msg := &backend.MessageDetail{
		ID:   "m1",
		From: "Jira <jira@acme.atlassian.net>",
		BodyHtml: `<div>
			<a href="javascript:alert(1)">Evil JS</a>
			<a href="file:///etc/passwd">Local File</a>
			<a href="mailto:test@example.com">Mailto</a>
			<a href="https://acme.atlassian.net/browse/PROJ-42">View issue PROJ-42</a>
			<a href="https://acme.atlassian.net/browse/PROJ-42">Duplicate</a>
			<a href="https://acme.atlassian.net/unsubscribe?token=xyz">Unsubscribe</a>
		</div>`,
		BodyText: "See also https://docs.example.org/spec. And https://acme.atlassian.net/browse/PROJ-42!",
	}

	links := ExtractMessageLinks(msg)
	if len(links) != 3 {
		t.Fatalf("expected 3 unique http(s) links, got %d: %+v", len(links), links)
	}
	if links[0].URL != "https://acme.atlassian.net/browse/PROJ-42" || links[0].Label != "View issue PROJ-42" {
		t.Fatalf("unexpected first link: %+v", links[0])
	}
	if links[2].URL != "https://docs.example.org/spec" {
		t.Fatalf("trailing dot not stripped from plain text URL: %+v", links[2])
	}
	for _, lk := range links {
		if !isSafeWebURL(lk.URL) {
			t.Fatalf("unsafe URL leaked into ExtractMessageLinks: %q", lk.URL)
		}
	}
}

func TestMatchPreviewLinkRules(t *testing.T) {
	cfg := config.Default().App.LinkPreview

	// 1. Atlassian Jira mail with noise unsubscribe link listed FIRST -> must skip noise and pick /browse/ link.
	jiraMsg := &backend.MessageDetail{
		ID:      "jira-1",
		From:    "Jira (Acme) <jira@acme.atlassian.net>",
		Subject: "[PROJ-42] External notification: Issue updated",
		BodyHtml: `<a href="https://acme.atlassian.net/unsubscribe?id=1">Manage email preferences</a>
		           <a href="https://acme.atlassian.net/browse/PROJ-42">View issue</a>`,
	}
	links := ExtractMessageLinks(jiraMsg)
	u, rule, ok := MatchPreviewLink(jiraMsg, links, cfg)
	if !ok || u != "https://acme.atlassian.net/browse/PROJ-42" || rule != "Atlassian Jira" {
		t.Fatalf("expected Atlassian Jira match, got ok=%v url=%q rule=%q", ok, u, rule)
	}

	// 2. GitHub mail: rule exists in defaults, but is disabled by default -> must NOT auto-open.
	ghMsg := &backend.MessageDetail{
		ID:       "gh-1",
		From:     "notifications@github.com",
		BodyText: "PR updated: https://github.com/org/repo/pull/99",
	}
	if _, _, ok := MatchPreviewLink(ghMsg, ExtractMessageLinks(ghMsg), cfg); ok {
		t.Fatal("disabled GitHub rule must not match")
	}

	// 3. Normal newsletter mail: must not match.
	newsMsg := &backend.MessageDetail{
		ID:       "news-1",
		From:     "news@example.com",
		BodyText: "Read more at https://example.com/article",
	}
	if _, _, ok := MatchPreviewLink(newsMsg, ExtractMessageLinks(newsMsg), cfg); ok {
		t.Fatal("normal newsletter must not match default auto-preview rules")
	}

	// 4. Real Jira Cloud mail: bare project breadcrumb (/browse/WORMS) precedes work item (/browse/WORMS-4103).
	// Must open the work item directly instead of the project board.
	jiraCloudMsg := &backend.MessageDetail{
		ID:      "jira-cloud-1",
		From:    "David Reif (Jira) <jira@atlassian.rewe-group.com>",
		Subject: "[JIRA] Updates for WORMS-4103",
		BodyHtml: `<a href="https://rewe.atlassian.net/browse/WORMS?atlOrigin=abc">Work Management Systems</a>
		           <a href="https://rewe.atlassian.net/browse/WORMS-4103?atlOrigin=abc">WORMS-4103</a>
		           <a href="https://rewe.atlassian.net/browse/WORMS-4103">View comment</a>
		           <a href="https://rewe.atlassian.net/jira/settings/personal/notifications?emailPreferences=true#emailpreferences">Manage Notifications</a>`,
	}
	u, rule, ok = MatchPreviewLink(jiraCloudMsg, ExtractMessageLinks(jiraCloudMsg), cfg)
	if !ok || u != "https://rewe.atlassian.net/browse/WORMS-4103?atlOrigin=abc" || rule != "Atlassian Jira" {
		t.Fatalf("expected direct work item WORMS-4103 over project link, got ok=%v url=%q rule=%q", ok, u, rule)
	}

	// 5. Jira Cloud mention with focusedCommentId should prefer the direct work-item + comment link over bare project link.
	jiraMentionMsg := &backend.MessageDetail{
		ID:      "jira-mention-1",
		From:    "Marlon Gerber (Jira) <jira@helvetia-baloise.atlassian.net>",
		Subject: "[JIRA] Marlon Gerber mentioned you on UATHCM-326",
		BodyHtml: `<a href="https://helvetia-baloise.atlassian.net/browse/UATHCM?atlOrigin=x">Support</a>
		           <a href="https://helvetia-baloise.atlassian.net/browse/UATHCM-326">Plain</a>
		           <a href="https://helvetia-baloise.atlassian.net/browse/UATHCM-326?focusedCommentId=847891&amp;sourceType=mention">UATHCM-326</a>`,
	}
	u, rule, ok = MatchPreviewLink(jiraMentionMsg, ExtractMessageLinks(jiraMentionMsg), cfg)
	if !ok || !strings.Contains(u, "/browse/UATHCM-326?focusedCommentId=847891") || rule != "Atlassian Jira" {
		t.Fatalf("expected focused comment work item URL, got ok=%v url=%q rule=%q", ok, u, rule)
	}

	// 6. Jira Cloud email with bare /browse/WORMS + /jira/software/c/projects/WORMS/issues/WORMS-4103 must pick the work item across rules.
	jiraBoardAndIssue := &backend.MessageDetail{
		ID:      "jira-cross-rule",
		From:    "Jira <jira@rewe.atlassian.net>",
		Subject: "[JIRA] Updates for WORMS-4103",
		BodyHtml: `<a href="https://rewe.atlassian.net/browse/WORMS">Work Management Systems</a>
		           <a href="https://rewe.atlassian.net/jira/software/c/projects/WORMS/issues/WORMS-4103">WORMS-4103</a>`,
	}
	u, _, ok = MatchPreviewLink(jiraBoardAndIssue, ExtractMessageLinks(jiraBoardAndIssue), cfg)
	if !ok || u != "https://rewe.atlassian.net/jira/software/c/projects/WORMS/issues/WORMS-4103" {
		t.Fatalf("expected work item issue URL across rules, got ok=%v url=%q", ok, u)
	}

	// 7. Bare /browse/WORMS only, while subject names WORMS-4103 -> promote to /browse/WORMS-4103.
	jiraBareOnly := &backend.MessageDetail{
		ID:       "jira-bare-only",
		From:     "Jira <jira@rewe.atlassian.net>",
		Subject:  "[JIRA] Updates for WORMS-4103",
		BodyHtml: `<a href="https://rewe.atlassian.net/browse/WORMS?atlOrigin=abc">Work Management Systems</a>`,
	}
	u, rule, ok = MatchPreviewLink(jiraBareOnly, ExtractMessageLinks(jiraBareOnly), cfg)
	if !ok || u != "https://rewe.atlassian.net/browse/WORMS-4103?atlOrigin=abc" || rule != "Atlassian Jira" {
		t.Fatalf("expected promoted /browse/WORMS-4103, got ok=%v url=%q rule=%q", ok, u, rule)
	}

	// 8. Manual PickPrimaryLink on non-atlassian.net JSM email must skip root site link and open the portal ticket matching the subject key.
	jsmMsg := &backend.MessageDetail{
		ID:      "jsm-1",
		From:    "Support (Jira) <support@getxray.app>",
		Subject: "SUPPORT-124424 Xray MCP Server",
		BodyHtml: `<a href="https://one-carrier.atlassian.net/">https://one-carrier.atlassian.net</a>
		           <a href="https://jira.getxray.app/servicedesk/customer/portal/2/SUPPORT-124424?sda_source=notification-email">View request</a>`,
	}
	u, _ = PickPrimaryLink(jsmMsg, ExtractMessageLinks(jsmMsg), cfg)
	if u != "https://jira.getxray.app/servicedesk/customer/portal/2/SUPPORT-124424?sda_source=notification-email" {
		t.Fatalf("expected JSM portal ticket URL, got %q", u)
	}
}

func TestWebPreviewSplitPanelLifecycleAndRender(t *testing.T) {
	cfg := config.Default()
	cfg.Language = "de"
	cfg.App.LinkPreview.Enabled = true
	saved := cfg
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return saved, nil },
		ConfigSaver: func(next config.Config) error {
			saved = next
			return nil
		},
	})
	host := &fakePreviewHost{}
	app.SetWebPreviewHost(host)

	jiraMsg := &backend.MessageDetail{
		ID:      "m-jira",
		From:    "Jira <jira@acme.atlassian.net>",
		Subject: "[DEV-108] Status geändert",
		BodyHtml: `<p>Es gibt Änderungen an diesem Vorgang.</p>
			<a href="https://acme.atlassian.net/browse/DEV-108">DEV-108 öffnen</a>
			<a href="https://acme.atlassian.net/wiki/spaces/ENG/pages/12">Spezifikation</a>`,
	}
	normalMsg := &backend.MessageDetail{
		ID:       "m-normal",
		From:     "colleague@example.org",
		Subject:  "Mittagessen",
		BodyText: "Treffen wir uns um 12:30? Menü: https://bistro.example.org/today",
	}

	// 1. Opening Jira mail auto-opens preview split-panel and loads Jira URL.
	app.selectedID = jiraMsg.ID
	app.selectedMsg = jiraMsg
	app.syncPreviewForMessage(jiraMsg)
	if !app.preview.open || !app.preview.autoOpened {
		t.Fatalf("expected preview panel to auto-open for Jira mail: %+v", app.preview)
	}
	if app.preview.activeURL != "https://acme.atlassian.net/browse/DEV-108" {
		t.Fatalf("unexpected activeURL: %q", app.preview.activeURL)
	}
	if app.preview.splitPct != 48 {
		t.Fatalf("expected splitPct 48 (0..100 scale), got %v", app.preview.splitPct)
	}
	if len(host.loaded) != 1 || host.loaded[0] != "https://acme.atlassian.net/browse/DEV-108" {
		t.Fatalf("expected host.LoadURL called with Jira link, got %v", host.loaded)
	}

	// Render headless side-by-side frame and verify preview chrome & badge are present.
	app.navID = "focus"
	app.engineReady = true
	tt := ui.NewTester(app.RenderMain, 1400, 900)
	tt.Frame()
	if _, ok := tt.Find("Auto: Atlassian Jira"); !ok {
		t.Fatalf("expected 'Auto: Atlassian Jira' badge in split preview pane; texts=%v", tt.Texts())
	}

	// Switch to top/bottom split layout and render without panic.
	app.TogglePreviewLayout()
	if app.preview.layout != "bottom" {
		t.Fatalf("expected bottom layout, got %q", app.preview.layout)
	}
	tt.Frame()

	// 2. Switching to a normal mail auto-collapses the previously auto-opened panel.
	app.selectedID = normalMsg.ID
	app.selectedMsg = normalMsg
	app.syncPreviewForMessage(normalMsg)
	if app.preview.open {
		t.Fatal("expected auto-opened preview panel to collapse when switching to non-matching mail")
	}

	// 3. Manual toggle opens preview for normal mail and blocks unsafe URLs.
	app.ToggleWebPreview()
	if !app.preview.open || app.preview.autoOpened {
		t.Fatalf("expected manual preview open: %+v", app.preview)
	}
	if app.preview.activeURL != "https://bistro.example.org/today" {
		t.Fatalf("expected primary link loaded on manual open, got %q", app.preview.activeURL)
	}
	app.LoadPreviewURL("javascript:alert(1)", "")
	if app.preview.loadErr == "" {
		t.Fatal("expected unsafe javascript: URL to be rejected with error")
	}
	if !strings.HasPrefix(app.preview.activeURL, "https://") {
		t.Fatalf("activeURL must remain safe after rejected load: %q", app.preview.activeURL)
	}

	// 4. Settings UI persists custom rule and disables auto-open cleanly.
	app.OpenSettings()
	app.settings.linkPreviewAutoLoad = false
	app.settings.linkPreviewLayout = 1
	app.settings.linkPreviewRules = append(app.settings.linkPreviewRules, config.LinkPreviewRule{
		Name:          "Firmen-Jira",
		Enabled:       true,
		SenderPattern: "jira@corp.local",
		URLPattern:    "jira.corp.local/browse/",
	})
	if !app.SaveSettings() {
		t.Fatalf("SaveSettings failed: %s", app.settings.err)
	}
	if saved.App.LinkPreview.AutoLoad {
		t.Fatal("expected AutoLoad=false persisted")
	}
	if saved.App.LinkPreview.Layout != "bottom" {
		t.Fatalf("expected Layout=bottom persisted, got %q", saved.App.LinkPreview.Layout)
	}
	foundCustom := false
	for _, r := range saved.App.LinkPreview.Rules {
		if r.Name == "Firmen-Jira" && r.URLPattern == "jira.corp.local/browse/" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatalf("custom preview rule not persisted: %+v", saved.App.LinkPreview.Rules)
	}
}

func TestSplitEmailListAndFormatAddress(t *testing.T) {
	formatted := formatEmailAddress("Doe, Jane", "jane@example.com")
	if !strings.Contains(formatted, `"Doe, Jane"`) || !strings.Contains(formatted, "<jane@example.com>") {
		t.Fatalf("formatEmailAddress did not quote comma name: %q", formatted)
	}
	if bare := formatEmailAddress("", "bob@example.com"); bare != "bob@example.com" {
		t.Fatalf("formatEmailAddress bare = %q", bare)
	}

	got := splitEmailList(formatted + ", bob@example.com, ")
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed addresses, got %d: %+v", len(got), got)
	}
	if got[1] != "bob@example.com" {
		t.Fatalf("bare address should stay unbracketed, got %q", got[1])
	}
}

func TestPreviewClearsOnNilOrZeroLinkMailAndRejectsSchemes(t *testing.T) {
	app := NewApp(Options{})
	app.cfg.App.LinkPreview.Enabled = true
	host := &fakePreviewHost{}
	app.SetWebPreviewHost(host)

	jiraMsg := &backend.MessageDetail{
		ID:       "m1",
		From:     "Jira <jira@acme.atlassian.net>",
		BodyHtml: `<a href="https://acme.atlassian.net/browse/DEV-1">DEV-1</a>`,
	}
	app.syncPreviewForMessage(jiraMsg)
	if app.preview.activeURL == "" {
		t.Fatal("expected activeURL set for Jira mail")
	}

	// Switching to a message with zero links must clear activeURL/urlInput/matchedRule
	plainMsg := &backend.MessageDetail{
		ID:       "m2",
		From:     "friend@example.org",
		BodyText: "Keine Links in dieser Nachricht.",
	}
	app.preview.open = true
	app.preview.autoOpened = false
	app.syncPreviewForMessage(plainMsg)
	if app.preview.activeURL != "" || app.preview.urlInput != "" || app.preview.matchedRule != "" {
		t.Fatalf("expected preview URL state cleared for 0-link mail, got %+v", app.preview)
	}

	// Clearing selection (nil) must also clear preview URL state
	app.syncPreviewForMessage(jiraMsg)
	app.syncPreviewForMessage(nil)
	if app.preview.activeURL != "" || app.preview.urlInput != "" || app.preview.matchedRule != "" {
		t.Fatalf("expected preview URL state cleared on nil message, got %+v", app.preview)
	}

	// Reject dangerous schemes and whitespace/control characters
	for _, bad := range []string{"javascript:alert(1)", "mailto:a@b.de", "file:///etc/passwd", "data:text/html,hi", "https://example.com/a\nb", "https:///nohost"} {
		app.preview.loadErr = ""
		app.LoadPreviewURL(bad, "")
		if app.preview.loadErr == "" {
			t.Fatalf("expected LoadPreviewURL(%q) to set loadErr", bad)
		}
	}
}

func TestIsRiskyAttachmentExt(t *testing.T) {
	for _, risky := range []string{"invoice.exe", "setup.DMG", "script.sh", "payload.html", "pic.svg", "macro.vbs", "run.ps1"} {
		if !isRiskyAttachmentExt("/tmp/" + risky) {
			t.Fatalf("expected %q to be flagged as risky attachment", risky)
		}
	}
	for _, safe := range []string{"invoice.pdf", "photo.png", "report.docx", "notes.txt", "data.csv"} {
		if isRiskyAttachmentExt("/tmp/" + safe) {
			t.Fatalf("expected %q to be allowed for auto-open", safe)
		}
	}
}
