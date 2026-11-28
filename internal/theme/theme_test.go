package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diver80/postdive/internal/config"
)

func TestPresetsCountAndValidity(t *testing.T) {
	presets := ListPresets()
	if len(presets) != 24 {
		t.Fatalf("expected 24 presets (diver, diver-light + 22 omarchy), got %d", len(presets))
	}
	if presets[0].Name != "diver" || presets[1].Name != "diver-light" {
		t.Fatalf("expected diver & diver-light first in presets, got %s, %s", presets[0].Name, presets[1].Name)
	}
	if def, src := ResolveActive(config.ThemeConfig{Mode: "preset"}); def.Name != "diver" || src != "default:diver" {
		t.Fatalf("expected default preset diver, got %s (%s)", def.Name, src)
	}
	for _, p := range presets {
		if !strings.HasPrefix(p.Background, "#") || !strings.HasPrefix(p.Foreground, "#") || !strings.HasPrefix(p.Accent, "#") {
			t.Fatalf("invalid hex in preset %s: %+v", p.Name, p)
		}
		payload := BuildPayload(p, "test")
		if payload.Thunderbird.Colors["toolbar"] != p.Background {
			t.Fatalf("toolbar color mismatch for %s", p.Name)
		}
		if payload.BodyCSS == "" || payload.ComposeCSS == "" {
			t.Fatalf("missing generated CSS for %s", p.Name)
		}
	}
}

func TestParseColorsTOMLAndLiveOverride(t *testing.T) {
	toml := `
# Omarchy colors
background = "#1a1b26"
foreground = "#c0caf5"
accent = "#7aa2f7"
selection = "#292e42"
`
	p, err := ParseColorsTOML(toml, "tokyo-custom")
	if err != nil {
		t.Fatalf("ParseColorsTOML failed: %v", err)
	}
	if p.IsLight {
		t.Fatalf("expected dark palette")
	}
	if p.DarkBackground == "" || p.LighterBg == "" {
		t.Fatalf("expected derived surface variants, got %+v", p)
	}

	dir := t.TempDir()
	colorsPath := filepath.Join(dir, "colors.toml")
	if err := os.WriteFile(colorsPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMARCHY_COLORS_TOML", colorsPath)

	got, src := ResolveActive(config.ThemeConfig{Mode: "omarchy"})
	if !strings.HasPrefix(src, "omarchy:") || got.Accent != "#7aa2f7" {
		t.Fatalf("expected live omarchy resolution, got src=%q palette=%+v", src, got)
	}
}
