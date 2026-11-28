package theme

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/diver80/postdive/internal/config"
)

// OmarchyColorsPath returns the live Omarchy colors.toml path on Linux.
func OmarchyColorsPath() string {
	if p := os.Getenv("OMARCHY_COLORS_TOML"); p != "" {
		return p
	}
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "omarchy", "current", "theme", "colors.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
}

// ParseColorsTOML parses Omarchy's flat/sectioned colors.toml without external TOML dependencies.
func ParseColorsTOML(content string, name string) (Palette, error) {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			// Preserve hex colors inside quotes!
			inQuote := false
			for i := 0; i < len(line); i++ {
				if line[i] == '"' || line[i] == '\'' {
					inQuote = !inQuote
				} else if line[i] == '#' && !inQuote {
					line = strings.TrimSpace(line[:i])
					break
				}
			}
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		if k != "" && v != "" {
			kv[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return Palette{}, err
	}

	bg := normalizeHex(pick(kv, "background", "primary_background"))
	fg := normalizeHex(pick(kv, "foreground", "primary_foreground"))
	if bg == "" || fg == "" {
		return Palette{}, fmt.Errorf("colors.toml missing required background or foreground")
	}

	isLight := isLightColor(bg)
	darkBg := normalizeHex(pick(kv, "dark_background"))
	if darkBg == "" {
		darkBg = shiftBrightness(bg, -0.05)
	}
	darkerBg := normalizeHex(pick(kv, "darker_background"))
	if darkerBg == "" {
		darkerBg = shiftBrightness(bg, -0.10)
	}
	lighterBg := normalizeHex(pick(kv, "lighter_background"))
	if lighterBg == "" {
		if isLight {
			lighterBg = "#ffffff"
		} else {
			lighterBg = shiftBrightness(bg, 0.08)
		}
	}
	darkFg := normalizeHex(pick(kv, "dark_foreground", "muted", "comment"))
	if darkFg == "" {
		darkFg = mixHex(fg, bg, 0.42)
	}
	accent := normalizeHex(pick(kv, "accent", "color4", "blue"))
	if accent == "" {
		accent = "#7aa2f7"
	}
	selection := normalizeHex(pick(kv, "selection", "selection_background"))
	if selection == "" {
		selection = mixHex(accent, bg, 0.25)
	}
	border := normalizeHex(pick(kv, "border"))
	if border == "" {
		border = mixHex(fg, bg, 0.16)
	}

	if name == "" {
		name = "omarchy-live"
	}

	return Palette{
		Name:             name,
		Label:            "Omarchy Live",
		IsLight:          isLight,
		Background:       bg,
		DarkBackground:   darkBg,
		DarkerBackground: darkerBg,
		LighterBg:        lighterBg,
		Foreground:       fg,
		DarkForeground:   darkFg,
		Accent:           accent,
		Selection:        selection,
		Border:           border,
		Green:            orDefault(normalizeHex(pick(kv, "green", "color2")), "#9ece6a"),
		Yellow:           orDefault(normalizeHex(pick(kv, "yellow", "color3")), "#e0af68"),
		Red:              orDefault(normalizeHex(pick(kv, "red", "color1")), "#f7768e"),
		Blue:             orDefault(normalizeHex(pick(kv, "blue", "color4")), accent),
	}, nil
}

// ResolveActive resolves the effective Palette for the current OS and configuration.
func ResolveActive(cfg config.ThemeConfig) (Palette, string) {
	if cfg.Mode == "omarchy" || cfg.Mode == "auto" {
		if p, src, ok := loadOmarchyLive(); ok {
			return p, src
		}
	}
	if cfg.Mode == "preset" && cfg.Preset != "" {
		if p, ok := GetPreset(cfg.Preset); ok {
			return p, "preset:" + p.Name
		}
	}
	if cfg.Mode == "auto" {
		if IsSystemLight() {
			if p, ok := GetPreset(cfg.LightFallback); ok {
				return p, "system-light:" + p.Name
			}
			if p, ok := GetPreset("diver-light"); ok {
				return p, "system-light:diver-light"
			}
		} else {
			if p, ok := GetPreset(cfg.DarkFallback); ok {
				return p, "system-dark:" + p.Name
			}
			if p, ok := GetPreset("diver"); ok {
				return p, "system-dark:diver"
			}
		}
	}
	p, _ := GetPreset("diver")
	return p, "default:diver"
}

func loadOmarchyLive() (Palette, string, bool) {
	path := OmarchyColorsPath()
	if path == "" {
		return Palette{}, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Palette{}, "", false
	}
	name := "omarchy-live"
	if rawName, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "theme.name")); err == nil {
		if trimmed := strings.TrimSpace(string(rawName)); trimmed != "" {
			name = trimmed
		}
	}
	if name == "omarchy-live" {
		if target, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			base := filepath.Base(target)
			if base != "" && base != "." && base != "theme" {
				name = base
			}
		}
	}
	p, err := ParseColorsTOML(string(data), name)
	if err != nil {
		return Palette{}, "", false
	}
	p.Label = "Omarchy (" + name + ")"
	return p, "omarchy:" + path, true
}

var (
	systemLightMu      sync.Mutex
	systemLightValue   bool
	systemLightChecked time.Time
)

// IsSystemLight reports the OS light/dark preference. The lookup starts a helper process,
// so the result is cached for a few seconds: the theme watcher asks about once per second.
func IsSystemLight() bool {
	systemLightMu.Lock()
	defer systemLightMu.Unlock()
	if !systemLightChecked.IsZero() && time.Since(systemLightChecked) < 5*time.Second {
		return systemLightValue
	}
	systemLightValue = detectSystemLight()
	systemLightChecked = time.Now()
	return systemLightValue
}

func detectSystemLight() bool {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
		if err != nil {
			// Key is absent when macOS is in Light appearance.
			return true
		}
		return !strings.EqualFold(strings.TrimSpace(string(out)), "Dark")
	}
	if runtime.GOOS == "linux" {
		out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
		if err == nil {
			s := strings.ToLower(string(out))
			if strings.Contains(s, "prefer-light") || strings.Contains(s, "default") {
				return !strings.Contains(s, "prefer-dark")
			}
		}
	}
	return false
}

func pick(kv map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(kv[k]); v != "" {
			return v
		}
	}
	return ""
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func normalizeHex(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "0x") || strings.HasPrefix(v, "0X") {
		v = "#" + v[2:]
	}
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	if len(v) == 7 {
		return strings.ToLower(v)
	}
	return ""
}

func parseRGB(hex string) (int, int, int, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	r, err1 := strconv.ParseInt(hex[0:2], 16, 32)
	g, err2 := strconv.ParseInt(hex[2:4], 16, 32)
	b, err3 := strconv.ParseInt(hex[4:6], 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return int(r), int(g), int(b), true
}

func isLightColor(hex string) bool {
	r, g, b, ok := parseRGB(hex)
	if !ok {
		return false
	}
	lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255.0
	return lum > 0.55
}

func clampByte(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v + 0.5)
}

func shiftBrightness(hex string, delta float64) string {
	r, g, b, ok := parseRGB(hex)
	if !ok {
		return hex
	}
	shift := delta * 255.0
	return fmt.Sprintf("#%02x%02x%02x",
		clampByte(float64(r)+shift),
		clampByte(float64(g)+shift),
		clampByte(float64(b)+shift),
	)
}

func mixHex(a, b string, t float64) string {
	r1, g1, b1, ok1 := parseRGB(a)
	r2, g2, b2, ok2 := parseRGB(b)
	if !ok1 || !ok2 {
		return a
	}
	return fmt.Sprintf("#%02x%02x%02x",
		clampByte(float64(r1)*t+float64(r2)*(1-t)),
		clampByte(float64(g1)*t+float64(g2)*(1-t)),
		clampByte(float64(b1)*t+float64(b2)*(1-t)),
	)
}
