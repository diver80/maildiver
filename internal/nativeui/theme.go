package nativeui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/postdive/internal/theme"
)

// PaletteToUITheme maps an Omarchy palette directly into a native mygo/ui Theme.
func PaletteToUITheme(pal theme.Palette) *ui.Theme {
	base := ui.DarkTheme()
	if pal.IsLight {
		base = ui.LightTheme()
	}

	bg := hexColor(pal.Background, base.Background)
	surface := hexColor(pal.LighterBg, base.Surface)
	if pal.IsLight {
		surface = hexColor(pal.DarkBackground, base.Surface)
	}
	surfaceHover := hexColor(pal.Selection, base.SurfaceHover)
	surfacePressed := blendColor(surfaceHover, bg, 0.25)
	border := hexColor(pal.Border, base.Border)
	text := hexColor(pal.Foreground, base.Text)
	muted := hexColor(pal.DarkForeground, base.TextMuted)
	accent := hexColor(pal.Accent, base.Accent)

	var accentHover, accentPressed, accentText ui.Color
	if pal.IsLight {
		accentHover = darkenColor(accent, 0.10)
		accentPressed = darkenColor(accent, 0.20)
		accentText = ui.Hex("#ffffff")
	} else {
		accentHover = lightenColor(accent, 0.12)
		accentPressed = darkenColor(accent, 0.12)
		if luminance(accent) > 0.58 {
			accentText = hexColor(pal.DarkerBackground, ui.Hex("#11111b"))
		} else {
			accentText = ui.Hex("#ffffff")
		}
	}

	danger := hexColor(pal.Red, base.Danger)
	warning := hexColor(pal.Yellow, base.Warning)
	success := hexColor(pal.Green, base.Success)
	// Use accent-tinted selection so dragged text highlights stay clearly visible
	// across all themes (dark palettes often reuse a near-background gray for pal.Selection).
	selection := withAlpha(accent, 0.38)
	if pal.IsLight {
		selection = withAlpha(accent, 0.26)
	}

	scrollbar := withAlpha(muted, 0.35)
	inverse, inverseText := text, bg

	out := *base
	out.Dark = !pal.IsLight
	out.Background = bg
	out.Surface = surface
	out.SurfaceHover = surfaceHover
	out.SurfacePressed = surfacePressed
	out.Border = border
	out.Text = text
	out.TextMuted = muted
	out.Accent = accent
	out.AccentHover = accentHover
	out.AccentPressed = accentPressed
	out.AccentText = accentText
	out.Danger = danger
	out.Warning = warning
	out.Success = success
	out.Selection = selection
	out.Focus = withAlpha(accent, 0.60)
	out.Inverse = inverse
	out.InverseText = inverseText
	out.Scrollbar = scrollbar
	out.Radius = 7
	out.Spacing = 4
	return &out
}

func hexColor(raw string, fallback ui.Color) ui.Color {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fallback
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if len(s) != 7 && len(s) != 4 && len(s) != 9 {
		return fallback
	}
	return ui.Hex(s)
}

func withAlpha(c ui.Color, alpha float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, alpha)
}

func blendColor(a, b ui.Color, t float32) ui.Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(x, y uint8) uint8 {
		return uint8(float32(x)*(1-t) + float32(y)*t)
	}
	return ui.RGB(lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B))
}

func darkenColor(c ui.Color, amount float32) ui.Color {
	return blendColor(c, ui.RGB(0, 0, 0), amount)
}

func lightenColor(c ui.Color, amount float32) ui.Color {
	return blendColor(c, ui.RGB(255, 255, 255), amount)
}

func luminance(c ui.Color) float32 {
	return (0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)) / 255.0
}

type themeChoice struct {
	Label string
	Mode  string // "auto", "omarchy", or "preset"
	Name  string // preset name when Mode == "preset"
}

func buildThemeChoices() []themeChoice {
	choices := []themeChoice{
		{Label: "Omarchy Auto", Mode: "auto"},
		{Label: "Omarchy Live", Mode: "omarchy"},
	}
	for _, p := range theme.ListPresets() {
		choices = append(choices, themeChoice{
			Label: fmt.Sprintf("%s (%s)", p.Label, p.Name),
			Mode:  "preset",
			Name:  p.Name,
		})
	}
	return choices
}
