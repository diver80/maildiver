package nativeui

import (
	"fmt"
	"github.com/egoist/mygo"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

// WebPreviewHost abstracts the embedded native browser (mygo.WebView) so the reader split-panel
// works identically in desktop builds and headless UI tests.
type WebPreviewHost interface {
	Widget() ui.NativeWebView
	LoadURL(rawURL string) error
	GoBack()
	GoForward()
	Reload()
	CanGoBack() bool
	CanGoForward() bool
	IsLoading() bool
	CurrentURL() string
}

// ExtractedLink is an http(s) link discovered in an email's HTML or plain-text body.
type ExtractedLink struct {
	URL   string
	Label string
}

type webPreviewState struct {
	open        bool
	autoOpened  bool
	layout      string  // "side" or "bottom"
	splitPct    float32 // 0.25..0.75, default 0.48
	msgID       string
	links       []ExtractedLink
	activeURL   string
	urlInput    string
	matchedRule string
	loading     bool
	loadErr     string
	host        WebPreviewHost
}

const (
	maxPreviewHTMLScanBytes = 2 * 1024 * 1024
	maxPreviewLinks         = 50
)

var (
	plainURLRe              = regexp.MustCompile(`https?://[^\s<>()"'\[\]]+`)
	urlSchemeRe             = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	workItemKeyRe           = regexp.MustCompile(`\b([A-Z][A-Z0-9_]{1,15}-[0-9]+)\b`)
	jiraBrowseIssueRe       = regexp.MustCompile(`(?i)/(?:browse|issues)/([A-Z][A-Z0-9_]{1,15}-[0-9]+)(?:[/?#]|$)`)
	jiraBrowseProjectOnlyRe = regexp.MustCompile(`(?i)/browse/([A-Z][A-Z0-9_]{1,15})(?:[/?#]|$)`)
	jiraPortalIssueRe       = regexp.MustCompile(`(?i)(?:/servicedesk/customer/portal/\d+/|/requests/#!/|selectedIssue=)([A-Z][A-Z0-9_]{1,15}-[0-9]+)(?:[/?#&]|$)`)
)

func isSafeWebURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, "\r\n\t\x00 ") {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "https" || scheme == "http") && u.Hostname() != ""
}

func isNoisePreviewLink(rawURL, label string) bool {
	low := strings.ToLower(rawURL + " " + label)
	for _, kw := range []string{
		"unsubscribe", "abmelden", "abbestellen", "optout", "opt-out",
		"notification-preferences", "email-preferences", "emailpreferences",
		"viewmyemailsettings", "manage-subscriptions", "manage notifications",
		"viewprofile.jspa", "/banner/opt-out",
		"/legal", "/privacy", "/terms", "datenschutz", "impressum",
		"pixel", "/open?", "track/open",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// extractWorkItemKeys extracts unique uppercase Jira/Atlassian work item keys (e.g. WORMS-4103) from s.
func extractWorkItemKeys(s string) []string {
	matches := workItemKeyRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		k := m[1]
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// urlWorkItemInfo inspects rawURL for a direct Jira/Atlassian work item key or a bare project link (/browse/PROJ).
func urlWorkItemInfo(rawURL string) (issueKey string, bareProjectKey string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ""
	}
	if m := jiraBrowseIssueRe.FindStringSubmatch(u.Path); len(m) == 2 {
		return strings.ToUpper(m[1]), ""
	}
	if m := jiraPortalIssueRe.FindStringSubmatch(rawURL); len(m) == 2 {
		return strings.ToUpper(m[1]), ""
	}
	if m := jiraBrowseProjectOnlyRe.FindStringSubmatch(u.Path); len(m) == 2 {
		return "", strings.ToUpper(m[1])
	}
	return "", ""
}

func isGenericAtlassianContainerURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	lowPath := strings.ToLower(strings.TrimRight(u.Path, "/"))
	lowRaw := strings.ToLower(rawURL)
	if strings.Contains(lowPath, "/wiki/home") || strings.Contains(lowPath, "/wiki/people/") {
		return true
	}
	if strings.Contains(lowPath, "/jira/") && strings.Contains(lowPath, "/projects/") && !strings.Contains(lowRaw, "selectedissue=") {
		return true
	}
	if strings.HasSuffix(strings.ToLower(u.Hostname()), ".atlassian.net") && lowPath == "" {
		return true
	}
	return false
}

func containsIssueKeyToken(lowURL, lowKey string) bool {
	idx := 0
	for {
		pos := strings.Index(lowURL[idx:], lowKey)
		if pos < 0 {
			return false
		}
		start := idx + pos
		end := start + len(lowKey)
		leftOK := start == 0 || !isIssueKeyChar(lowURL[start-1])
		rightOK := end == len(lowURL) || !isIssueKeyChar(lowURL[end])
		if leftOK && rightOK {
			return true
		}
		idx = start + 1
	}
}

func isIssueKeyChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

// previewLinkScore prioritizes direct work-item links (especially matching a key from the email subject)
// over bare project/board/home links such as https://site.atlassian.net/browse/PROJ.
func previewLinkScore(lk ExtractedLink, subjectKeys []string) int {
	score := 0
	issueKey, bareProject := urlWorkItemInfo(lk.URL)
	if issueKey != "" {
		score += 60
		for _, sk := range subjectKeys {
			if strings.EqualFold(issueKey, sk) {
				score += 40
				break
			}
		}
		if strings.Contains(strings.ToLower(lk.URL), "focusedcommentid=") {
			score += 10
		}
		return score
	}
	if bareProject != "" || isGenericAtlassianContainerURL(lk.URL) {
		return -40
	}
	if len(subjectKeys) > 0 {
		lowURL := strings.ToLower(lk.URL)
		for _, sk := range subjectKeys {
			if containsIssueKeyToken(lowURL, strings.ToLower(sk)) {
				return 50
			}
		}
	}
	return score
}

// promoteBareProjectURL upgrades a bare /browse/PROJ link to /browse/PROJ-123 when no direct
// work-item link was present and the email subject contains a key for that exact project.
func promoteBareProjectURL(rawURL string, subjectKeys []string) string {
	issueKey, bareProject := urlWorkItemInfo(rawURL)
	if issueKey != "" || bareProject == "" || len(subjectKeys) == 0 {
		return rawURL
	}
	prefix := bareProject + "-"
	for _, sk := range subjectKeys {
		if strings.HasPrefix(strings.ToUpper(sk), prefix) {
			u, err := url.Parse(rawURL)
			if err != nil {
				return rawURL
			}
			u.Path = "/browse/" + strings.ToUpper(sk)
			return u.String()
		}
	}
	return rawURL
}

// ExtractMessageLinks collects unique, safe http(s) links from msg (HTML anchors first, then plain text).
func ExtractMessageLinks(msg *backend.MessageDetail) []ExtractedLink {
	if msg == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []ExtractedLink
	add := func(rawURL, label string, stripTrailingPunct bool) {
		if len(out) >= maxPreviewLinks {
			return
		}
		rawURL = strings.TrimSpace(rawURL)
		if stripTrailingPunct {
			rawURL = strings.TrimRight(rawURL, ".,;:!?")
		}
		if !isSafeWebURL(rawURL) || seen[rawURL] {
			return
		}
		seen[rawURL] = true
		label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
		if label == "" {
			if u, err := url.Parse(rawURL); err == nil {
				label = u.Host + truncateText(u.Path, 28)
			} else {
				label = truncateText(rawURL, 36)
			}
		}
		out = append(out, ExtractedLink{URL: rawURL, Label: truncateText(label, 42)})
	}

	if htmlBody := strings.TrimSpace(msg.BodyHtml); htmlBody != "" {
		if len(htmlBody) > maxPreviewHTMLScanBytes {
			htmlBody = htmlBody[:maxPreviewHTMLScanBytes]
		}
		if doc, err := xhtml.Parse(strings.NewReader(htmlBody)); err == nil {
			var walk func(*xhtml.Node)
			walk = func(n *xhtml.Node) {
				if len(out) >= maxPreviewLinks {
					return
				}
				if n.Type == xhtml.ElementNode && n.DataAtom == atom.A {
					if href, ok := attrOf(n, "href"); ok {
						add(href, textContent(n), false)
					}
				}
				for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
					walk(ch)
				}
			}
			walk(doc)
		}
	}

	if len(out) < maxPreviewLinks && strings.TrimSpace(msg.BodyText) != "" {
		for _, m := range plainURLRe.FindAllString(msg.BodyText, maxPreviewLinks) {
			add(m, "", true)
		}
	}
	return out
}

// MatchPreviewLink checks msg + links against configured auto-preview rules and returns the
// best matching URL and rule name. Noise links (unsubscribe/legal/preferences/profiles) are skipped,
// and direct work-item links (e.g. /browse/WORMS-4103) are prioritized over bare project/board links (/browse/WORMS).
func MatchPreviewLink(msg *backend.MessageDetail, links []ExtractedLink, cfg config.LinkPreviewConfig) (string, string, bool) {
	if msg == nil || len(links) == 0 {
		return "", "", false
	}
	fromLow := strings.ToLower(strings.TrimSpace(msg.From))
	subjectKeys := extractWorkItemKeys(msg.Subject)
	fallbackURL := ""
	fallbackRule := ""
	fallbackScore := -1 << 30
	for _, rule := range cfg.Rules {
		if !rule.Enabled {
			continue
		}
		urlPat := strings.ToLower(strings.TrimSpace(rule.URLPattern))
		if urlPat == "" {
			continue
		}
		senderPat := strings.ToLower(strings.TrimSpace(rule.SenderPattern))
		if senderPat != "" && !strings.Contains(fromLow, senderPat) {
			continue
		}
		bestIdx := -1
		bestScore := -1 << 30
		for i, lk := range links {
			if isNoisePreviewLink(lk.URL, lk.Label) {
				continue
			}
			if !strings.Contains(strings.ToLower(lk.URL), urlPat) {
				continue
			}
			sc := previewLinkScore(lk, subjectKeys)
			if bestIdx < 0 || sc > bestScore {
				bestIdx = i
				bestScore = sc
			}
		}
		if bestIdx >= 0 {
			if bestScore >= 0 {
				return links[bestIdx].URL, rule.Name, true
			}
			if fallbackURL == "" || bestScore > fallbackScore {
				fallbackURL = links[bestIdx].URL
				fallbackRule = rule.Name
				fallbackScore = bestScore
			}
		}
	}
	if fallbackURL != "" {
		return promoteBareProjectURL(fallbackURL, subjectKeys), fallbackRule, true
	}
	return "", "", false
}

// PickPrimaryLink picks the best link for manual preview opening (matched rule first, else highest-scoring non-noise link).
func PickPrimaryLink(msg *backend.MessageDetail, links []ExtractedLink, cfg config.LinkPreviewConfig) (string, string) {
	if u, ruleName, ok := MatchPreviewLink(msg, links, cfg); ok {
		return u, ruleName
	}
	var subjectKeys []string
	if msg != nil {
		subjectKeys = extractWorkItemKeys(msg.Subject)
	}
	bestIdx := -1
	bestScore := -1 << 30
	for i, lk := range links {
		if isNoisePreviewLink(lk.URL, lk.Label) {
			continue
		}
		sc := previewLinkScore(lk, subjectKeys)
		if bestIdx < 0 || sc > bestScore {
			bestIdx = i
			bestScore = sc
		}
	}
	if bestIdx >= 0 {
		return promoteBareProjectURL(links[bestIdx].URL, subjectKeys), ""
	}
	if len(links) > 0 {
		return links[0].URL, ""
	}
	return "", ""
}

// SetWebPreviewHost attaches the desktop shell's embedded mygo.WebView host to App.
func (a *App) SetWebPreviewHost(host WebPreviewHost) {
	a.preview.host = host
}

// NotifyPreviewNavigated updates the URL bar & loading state from WebView navigation callbacks (UI thread).
func (a *App) NotifyPreviewNavigated(currentURL string, loading bool, errText string) {
	if currentURL != "" && currentURL != "about:blank" {
		a.preview.activeURL = currentURL
		a.preview.urlInput = currentURL
	}
	a.preview.loading = loading
	a.preview.loadErr = errText
}

func clampPreviewSplitPct(v float32) float32 {
	if v <= 0 {
		return 48
	}
	if v < 15 {
		return 15
	}
	if v > 85 {
		return 85
	}
	return v
}

// syncPreviewForMessage inspects a newly opened mail, extracts its links, and auto-opens or
// updates the embedded browser preview panel if a configured rule matches.
func (a *App) syncPreviewForMessage(msg *backend.MessageDetail) {
	p := &a.preview
	if msg == nil {
		p.msgID = ""
		p.links = nil
		p.activeURL = ""
		p.urlInput = ""
		p.matchedRule = ""
		p.loadErr = ""
		p.loading = false
		if p.autoOpened {
			p.open = false
			p.autoOpened = false
		}
		return
	}
	if p.msgID == msg.ID && len(p.links) > 0 {
		return
	}
	p.msgID = msg.ID
	p.links = ExtractMessageLinks(msg)
	p.matchedRule = ""
	p.loadErr = ""
	if p.layout == "" {
		p.layout = a.cfg.App.LinkPreview.Layout
		if p.layout != "bottom" {
			p.layout = "side"
		}
	}
	p.splitPct = clampPreviewSplitPct(p.splitPct)

	lpCfg := a.cfg.App.LinkPreview
	if lpCfg.Enabled && lpCfg.AutoLoad {
		if matchedURL, ruleName, ok := MatchPreviewLink(msg, p.links, lpCfg); ok {
			p.open = true
			p.autoOpened = true
			a.LoadPreviewURL(matchedURL, ruleName)
			return
		}
	}

	// No auto-rule matched this mail: collapse if it was only auto-opened for the previous mail.
	if p.autoOpened {
		p.open = false
		p.autoOpened = false
		p.activeURL = ""
		p.urlInput = ""
		return
	}
	// If the user explicitly toggled the preview panel on, refresh to this mail's primary link,
	// or clear activeURL if this email has no links so the previous email's webpage never leaks.
	if p.open {
		if u, rule := PickPrimaryLink(msg, p.links, lpCfg); u != "" {
			a.LoadPreviewURL(u, rule)
		} else {
			p.activeURL = ""
			p.urlInput = ""
			p.matchedRule = ""
			p.loading = false
		}
	}
}

// ToggleWebPreview toggles the embedded browser preview split-panel for the current email.
func (a *App) ToggleWebPreview() {
	p := &a.preview
	if p.open {
		p.open = false
		p.autoOpened = false
		return
	}
	if p.layout == "" {
		p.layout = a.cfg.App.LinkPreview.Layout
		if p.layout != "bottom" {
			p.layout = "side"
		}
	}
	p.splitPct = clampPreviewSplitPct(p.splitPct)
	p.open = true
	p.autoOpened = false
	if a.selectedMsg != nil {
		if p.msgID != a.selectedMsg.ID {
			p.msgID = a.selectedMsg.ID
			p.links = ExtractMessageLinks(a.selectedMsg)
		}
		if u, rule := PickPrimaryLink(a.selectedMsg, p.links, a.cfg.App.LinkPreview); u != "" {
			a.LoadPreviewURL(u, rule)
		} else {
			p.activeURL = ""
			p.urlInput = ""
			p.matchedRule = ""
		}
	}
}

// SetWebPreviewAutoEnabled is the global auto web-preview switch (toolbar + ☰ menu).
// It persists app.linkPreview.enabled to config.json. Turning it off immediately closes
// any open preview pane so the reader gets its full width back; turning it on re-evaluates
// the currently selected mail so a matching (e.g. Jira/Confluence) link opens right away.
// The per-mail "Web-Vorschau" button keeps working on demand either way.
func (a *App) SetWebPreviewAutoEnabled(enabled bool) {
	a.cfg.App.LinkPreview.Enabled = enabled
	a.settings.linkPreviewEnabled = enabled
	if a.opts.ConfigSaver != nil {
		cfg := a.cfg
		if a.opts.ConfigLoader != nil {
			if loaded, err := a.opts.ConfigLoader(); err == nil {
				cfg = loaded
			}
		}
		cfg.App.LinkPreview.Enabled = enabled
		cfg = config.Normalize(cfg)
		_ = a.opts.ConfigSaver(cfg)
		a.cfg = cfg
	}

	p := &a.preview
	if !enabled {
		p.open = false
		p.autoOpened = false
		return
	}
	// Force re-evaluation of the current mail against the auto-preview rules.
	p.msgID = ""
	p.links = nil
	if a.selectedMsg != nil {
		a.syncPreviewForMessage(a.selectedMsg)
	}
}

// ToggleWebPreviewAuto flips the global auto web-preview switch.
func (a *App) ToggleWebPreviewAuto() {
	a.SetWebPreviewAutoEnabled(!a.cfg.App.LinkPreview.Enabled)
}

// TogglePreviewLayout switches between side-by-side ("side") and top/bottom ("bottom") split.
func (a *App) TogglePreviewLayout() {
	if a.preview.layout == "bottom" {
		a.preview.layout = "side"
	} else {
		a.preview.layout = "bottom"
	}
}

// LoadPreviewURL validates rawURL (http/https only) and loads it into the embedded WebView host.
func (a *App) LoadPreviewURL(rawURL, ruleName string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" && !strings.Contains(rawURL, "://") && !urlSchemeRe.MatchString(rawURL) {
		rawURL = "https://" + rawURL
	}
	if !isSafeWebURL(rawURL) {
		a.preview.loadErr = a.tr("Nur http:// und https:// URLs sind in der Web-Vorschau erlaubt.", "Only http:// and https:// URLs are allowed in Web Preview.")
		return
	}
	a.preview.activeURL = rawURL
	a.preview.urlInput = rawURL
	a.preview.matchedRule = ruleName
	a.preview.loadErr = ""
	if a.preview.host != nil {
		a.preview.loading = true
		if err := a.preview.host.LoadURL(rawURL); err != nil {
			a.preview.loading = false
			a.preview.loadErr = err.Error()
		}
	}
}

// isMobileOS reports a phone or tablet, where links and files open through the system (mygo.Shell)
// rather than open/xdg-open.
func isMobileOS() bool { return runtime.GOOS == "android" || runtime.GOOS == "ios" }

func openExternalURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if !isSafeWebURL(trimmed) {
		return fmt.Errorf("invalid url")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	safe := u.String()
	if isMobileOS() {
		return mygo.Shell.OpenExternal(safe)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", safe)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", safe)
	default:
		cmd = exec.Command("xdg-open", safe)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (a *App) renderWebPreviewPane(c *ui.Context) {
	t := c.Theme()
	p := &a.preview

	ui.Column(c.Key("web-preview-pane")).Fill().Background(t.Surface).Children(func() {
		// Mini browser chrome bar
		ui.Row(c).FillWidth().Padding(6, 10).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			if p.host != nil {
				backBtn := ui.Button(c.Key("wp-back"), "‹").Padding(2, 7)
				if !p.host.CanGoBack() {
					backBtn.Disabled(true)
				}
				if backBtn.Clicked() {
					p.host.GoBack()
				}
				fwdBtn := ui.Button(c.Key("wp-fwd"), "›").Padding(2, 7)
				if !p.host.CanGoForward() {
					fwdBtn.Disabled(true)
				}
				if fwdBtn.Clicked() {
					p.host.GoForward()
				}
				if iconBtn(c.Key("wp-reload"), iconRefresh, a.tr("Neu laden", "Reload"), ui.Color{}, false).Clicked() {
					p.host.Reload()
				}
			}

			urlIn := ui.TextInput(c.Key("wp-url"), &p.urlInput).Placeholder("https://…").Grow(1)
			if urlIn.Submitted() && strings.TrimSpace(p.urlInput) != "" {
				a.LoadPreviewURL(p.urlInput, "")
			}

			if p.loading {
				ui.Spinner(c)
			}
			if p.matchedRule != "" {
				pillBadge(c, "Auto: "+p.matchedRule, withAlpha(t.Accent, 0.16), t.Accent)
			}

			layoutLbl := a.tr("Unten", "Bottom")
			if p.layout == "bottom" {
				layoutLbl = a.tr("Rechts", "Side")
			}
			if ui.Button(c.Key("wp-layout"), layoutLbl).Padding(3, 8).Clicked() {
				a.TogglePreviewLayout()
			}
			if p.activeURL != "" {
				if ui.Button(c.Key("wp-ext"), a.tr("Browser ↗", "Browser ↗")).Padding(3, 8).Clicked() {
					_ = openExternalURL(p.activeURL)
				}
			}
			if iconBtn(c.Key("wp-cfg"), iconSettings, a.tr("Auto-Link-Regeln konfigurieren", "Configure Auto-Link rules"), ui.Color{}, false).Clicked() {
				a.OpenSettings()
			}
			if ui.Button(c.Key("wp-close"), "✕").Padding(2, 7).Clicked() {
				p.open = false
				p.autoOpened = false
			}
		})

		// Quick link switcher strip when the open email contains multiple links
		if len(p.links) > 1 {
			ui.Row(c.Key("wp-links")).FillWidth().Padding(5, 10).Gap(5).Wrap().AlignItems(ui.Center).Background(t.Background).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, a.tr("Links:", "Links:")).FontSize(11).TextColor(t.TextMuted)
				limit := min(len(p.links), 6)
				for i := 0; i < limit; i++ {
					lk := p.links[i]
					active := lk.URL == p.activeURL
					bg := t.Surface
					bd := t.Border
					if active {
						bg = withAlpha(t.Accent, 0.18)
						bd = t.Accent
					}
					if ui.Button(c.Key(fmt.Sprintf("wp-lk-%d", i)), lk.Label).Padding(2, 7).Radius(5).Background(bg).Border(1, bd).Clicked() {
						a.LoadPreviewURL(lk.URL, "")
					}
				}
			})
		}

		if p.loadErr != "" {
			ui.Row(c).FillWidth().Padding(6, 10).Background(withAlpha(t.Danger, 0.12)).BorderWidth(0, 0, 1, 0).BorderColor(withAlpha(t.Danger, 0.35)).Children(func() {
				ui.Text(c, p.loadErr).FontSize(11.5).TextColor(t.Danger).Grow(1)
			})
		}

		// Embedded native WebView (or graceful fallback when no URL / headless mode)
		if p.activeURL == "" {
			ui.Column(c).Fill().Center().Padding(20).Gap(8).Children(func() {
				ui.Text(c, a.tr("Keine Links in dieser E-Mail gefunden.", "No links found in this email.")).FontSize(13).Bold()
				ui.Text(c, a.tr("Gib oben eine https://-Adresse ein oder öffne eine Mail mit Issue-Links (z. B. Jira, Confluence, GitHub).", "Enter an https:// URL above or open an email containing issue links (e.g. Jira, Confluence, GitHub).")).FontSize(12).TextColor(t.TextMuted)
			})
		} else if p.host != nil && p.host.Widget() != nil {
			ui.WebView(c, p.host.Widget()).Grow(1).FillWidth()
		} else {
			ui.Column(c).Fill().Center().Padding(20).Gap(10).Children(func() {
				ui.Text(c, a.tr("Web-Vorschau", "Web Preview")).FontSize(14).Bold()
				ui.Text(c, p.activeURL).FontSize(12).TextColor(t.Accent)
				ui.Text(c, a.tr("Eingebettete System-WebView ist in dieser Umgebung nicht aktiv.", "Embedded system WebView is not active in this environment.")).FontSize(11.5).TextColor(t.TextMuted)
				if ui.PrimaryButton(c.Key("wp-fallback-open"), a.tr("Im Systembrowser öffnen", "Open in System Browser")).Clicked() {
					_ = openExternalURL(p.activeURL)
				}
			})
		}
	})
}
