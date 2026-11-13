package theme

import "fmt"

// ThunderbirdTheme mirrors the WebExtension ThemeType object expected by browser.theme.update().
type ThunderbirdTheme struct {
	Colors     map[string]string `json:"colors"`
	Properties map[string]string `json:"properties"`
}

// Payload is pushed over Native Messaging to the Thunderbird extension.
type Payload struct {
	Palette     Palette          `json:"palette"`
	Source      string           `json:"source"`
	Thunderbird ThunderbirdTheme `json:"thunderbird"`
	BodyCSS     string           `json:"bodyCss"`
	ComposeCSS  string           `json:"composeCss"`
	UICSSVars   string           `json:"uiCssVars"`
}

// BuildPayload converts a Palette into Thunderbird WebExtension theme.update() + live CSS.
func BuildPayload(p Palette, source string) Payload {
	scheme := "dark"
	if p.IsLight {
		scheme = "light"
	}
	contrastFg := "#0f1117"
	if isLightColor(p.Accent) {
		contrastFg = "#101218"
	} else {
		contrastFg = "#ffffff"
	}

	tb := ThunderbirdTheme{
		Colors: map[string]string{
			"frame":                        p.DarkBackground,
			"frame_inactive":               p.DarkerBackground,
			"tab_selected":                 p.Background,
			"tab_text":                     p.Foreground,
			"tab_background_text":          p.DarkForeground,
			"tab_background_separator":     p.Border,
			"tab_line":                     p.Accent,
			"tab_loading":                  p.Accent,
			"toolbar":                      p.Background,
			"toolbar_text":                 p.Foreground,
			"toolbar_field":                p.LighterBg,
			"toolbar_field_text":           p.Foreground,
			"toolbar_field_border":         p.Border,
			"toolbar_field_focus":          p.LighterBg,
			"toolbar_field_text_focus":     p.Foreground,
			"toolbar_field_highlight":      p.Accent,
			"toolbar_field_highlight_text": contrastFg,
			"toolbar_top_separator":        p.Border,
			"toolbar_bottom_separator":     p.Border,
			"toolbar_vertical_separator":   p.Border,
			"icons":                        p.Foreground,
			"icons_attention":              p.Accent,
			"button_background_hover":      p.Selection,
			"button_background_active":     p.Accent,
			"popup":                        p.LighterBg,
			"popup_text":                   p.Foreground,
			"popup_border":                 p.Border,
			"popup_highlight":              p.Accent,
			"popup_highlight_text":         contrastFg,
			"sidebar":                      p.DarkBackground,
			"sidebar_text":                 p.Foreground,
			"sidebar_border":               p.Border,
			"sidebar_highlight":            p.Selection,
			"sidebar_highlight_text":       p.Foreground,
			"sidebar_highlight_border":     p.Accent,
			"ntp_background":               p.Background,
			"ntp_text":                     p.Foreground,
		},
		Properties: map[string]string{
			"color_scheme":         scheme,
			"content_color_scheme": scheme,
		},
	}

	vars := fmt.Sprintf(`:root {
  --oma-bg: %s;
  --oma-bg-dark: %s;
  --oma-bg-darker: %s;
  --oma-bg-elevated: %s;
  --oma-fg: %s;
  --oma-fg-muted: %s;
  --oma-accent: %s;
  --oma-accent-fg: %s;
  --oma-selection: %s;
  --oma-border: %s;
  --oma-green: %s;
  --oma-yellow: %s;
  --oma-red: %s;
  --oma-blue: %s;
  color-scheme: %s;
}`, p.Background, p.DarkBackground, p.DarkerBackground, p.LighterBg,
		p.Foreground, p.DarkForeground, p.Accent, contrastFg, p.Selection, p.Border,
		p.Green, p.Yellow, p.Red, p.Blue, scheme)

	// Non-destructive message body styling: respects rich HTML tables/cards if explicitly styled,
	// while harmonizing plain-text & unstyled message surfaces with the active Omarchy theme.
	bodyCSS := fmt.Sprintf(`%s
html, body {
  background-color: var(--oma-bg) !important;
  color: var(--oma-fg) !important;
}
a {
  color: var(--oma-accent) !important;
}
blockquote[type="cite"] {
  border-left: 2px solid var(--oma-accent) !important;
  color: var(--oma-fg-muted) !important;
  padding-left: 12px !important;
  margin-left: 4px !important;
}
pre, code {
  background-color: var(--oma-bg-dark) !important;
  border-radius: 4px;
}
hr {
  border-color: var(--oma-border) !important;
}
`, vars)

	composeCSS := fmt.Sprintf(`%s
html, body {
  background-color: var(--oma-bg) !important;
  color: var(--oma-fg) !important;
  caret-color: var(--oma-accent) !important;
}
a {
  color: var(--oma-accent) !important;
}
blockquote[type="cite"] {
  border-left: 2px solid var(--oma-accent) !important;
  color: var(--oma-fg-muted) !important;
  padding-left: 12px !important;
}
`, vars)

	return Payload{
		Palette:     p,
		Source:      source,
		Thunderbird: tb,
		BodyCSS:     bodyCSS,
		ComposeCSS:  composeCSS,
		UICSSVars:   vars,
	}
}
