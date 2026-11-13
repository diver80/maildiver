package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fhess/omathunder/internal/config"
)

func TestPresetsCountAndValidity(t *testing.T) {
	presets := ListPresets()
	if len(presets) != 22 {
		t.Fatalf("expected 22 omarchy presets, got %d", len(presets))
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
