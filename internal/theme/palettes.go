package theme

import "sort"

// Palette represents a normalized Omarchy color palette.
type Palette struct {
	Name             string `json:"name"`
	Label            string `json:"label"`
	IsLight          bool   `json:"isLight"`
	Background       string `json:"background"`
	DarkBackground   string `json:"darkBackground"`
	DarkerBackground string `json:"darkerBackground"`
	LighterBg        string `json:"lighterBackground"`
	Foreground       string `json:"foreground"`
	DarkForeground   string `json:"darkForeground"`
	Accent           string `json:"accent"`
	Selection        string `json:"selection"`
	Border           string `json:"border"`
	Green            string `json:"green"`
	Yellow           string `json:"yellow"`
	Red              string `json:"red"`
	Blue             string `json:"blue"`
}

var builtInPalettes = map[string]Palette{
	"diver": {
		Name: "diver", Label: "Diver", IsLight: false,
		Background: "#18181b", DarkBackground: "#121215", DarkerBackground: "#0c0c0e", LighterBg: "#27272a",
		Foreground: "#f4f4f5", DarkForeground: "#a1a1aa", Accent: "#3b82f6", Selection: "#323236",
		Border: "#3f3f46", Green: "#22c55e", Yellow: "#f59e0b", Red: "#ef4444", Blue: "#3b82f6",
	},
	"diver-light": {
		Name: "diver-light", Label: "Diver Light", IsLight: true,
		Background: "#ffffff", DarkBackground: "#f4f4f5", DarkerBackground: "#e4e4e7", LighterBg: "#ffffff",
		Foreground: "#18181b", DarkForeground: "#71717a", Accent: "#2563eb", Selection: "#e9e9ec",
		Border: "#d9d9de", Green: "#16a34a", Yellow: "#d97706", Red: "#dc2626", Blue: "#2563eb",
	},
	"tokyo-night": {
		Name: "tokyo-night", Label: "Tokyo Night", IsLight: false,
		Background: "#1a1b26", DarkBackground: "#16161e", DarkerBackground: "#13131a", LighterBg: "#24283b",
		Foreground: "#c0caf5", DarkForeground: "#787c99", Accent: "#7aa2f7", Selection: "#292e42",
		Border: "#292e42", Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e", Blue: "#7aa2f7",
	},
	"catppuccin": {
		Name: "catppuccin", Label: "Catppuccin Mocha", IsLight: false,
		Background: "#1e1e2e", DarkBackground: "#181825", DarkerBackground: "#11111b", LighterBg: "#313244",
		Foreground: "#cdd6f4", DarkForeground: "#a6adc8", Accent: "#cba6f7", Selection: "#45475a",
		Border: "#313244", Green: "#a6e3a1", Yellow: "#f9e2af", Red: "#f38ba8", Blue: "#89b4fa",
	},
	"catppuccin-latte": {
		Name: "catppuccin-latte", Label: "Catppuccin Latte", IsLight: true,
		Background: "#eff1f5", DarkBackground: "#e6e9ef", DarkerBackground: "#dce0e8", LighterBg: "#ffffff",
		Foreground: "#4c4f69", DarkForeground: "#6c6f85", Accent: "#8839ef", Selection: "#ccd0da",
		Border: "#bcc0cc", Green: "#40a02b", Yellow: "#df8e1d", Red: "#d20f39", Blue: "#1e66f5",
	},
	"nord": {
		Name: "nord", Label: "Nord", IsLight: false,
		Background: "#2e3440", DarkBackground: "#292e39", DarkerBackground: "#242933", LighterBg: "#3b4252",
		Foreground: "#eceff4", DarkForeground: "#d8dee9", Accent: "#88c0d0", Selection: "#434c5e",
		Border: "#434c5e", Green: "#a3be8c", Yellow: "#ebcb8b", Red: "#bf616a", Blue: "#81a1c1",
	},
	"gruvbox": {
		Name: "gruvbox", Label: "Gruvbox Dark", IsLight: false,
		Background: "#282828", DarkBackground: "#1d2021", DarkerBackground: "#17191a", LighterBg: "#3c3836",
		Foreground: "#ebdbb2", DarkForeground: "#a89984", Accent: "#fe8019", Selection: "#504945",
		Border: "#3c3836", Green: "#b8bb26", Yellow: "#fabd2f", Red: "#fb4934", Blue: "#83a598",
	},
	"kanagawa": {
		Name: "kanagawa", Label: "Kanagawa", IsLight: false,
		Background: "#1f1f28", DarkBackground: "#181820", DarkerBackground: "#16161d", LighterBg: "#2a2a37",
		Foreground: "#dcd7ba", DarkForeground: "#727169", Accent: "#7e9cd8", Selection: "#363646",
		Border: "#2a2a37", Green: "#98bb6c", Yellow: "#e6c384", Red: "#e46876", Blue: "#7fb4ca",
	},
	"rose-pine": {
		Name: "rose-pine", Label: "Rosé Pine", IsLight: false,
		Background: "#191724", DarkBackground: "#16141f", DarkerBackground: "#12101a", LighterBg: "#26233a",
		Foreground: "#e0def4", DarkForeground: "#908caa", Accent: "#ebbcba", Selection: "#403d52",
		Border: "#26233a", Green: "#9ccfd8", Yellow: "#f6c177", Red: "#eb6f92", Blue: "#31748f",
	},
	"everforest": {
		Name: "everforest", Label: "Everforest", IsLight: false,
		Background: "#2d353b", DarkBackground: "#272e33", DarkerBackground: "#21272b", LighterBg: "#343f44",
		Foreground: "#d3c6aa", DarkForeground: "#859289", Accent: "#a7c080", Selection: "#475258",
		Border: "#3d484d", Green: "#a7c080", Yellow: "#dbbc7f", Red: "#e67e80", Blue: "#7fbbb3",
	},
	"matte-black": {
		Name: "matte-black", Label: "Matte Black", IsLight: false,
		Background: "#121212", DarkBackground: "#0c0c0c", DarkerBackground: "#080808", LighterBg: "#1e1e1e",
		Foreground: "#eaeaea", DarkForeground: "#8a8a8d", Accent: "#e68e0d", Selection: "#2a2a2a",
		Border: "#262626", Green: "#4ade80", Yellow: "#facc15", Red: "#ef4444", Blue: "#60a5fa",
	},
	"flexoki-light": {
		Name: "flexoki-light", Label: "Flexoki Light", IsLight: true,
		Background: "#fffcf0", DarkBackground: "#f2f0e5", DarkerBackground: "#e6e4d9", LighterBg: "#ffffff",
		Foreground: "#100f0f", DarkForeground: "#6f6e69", Accent: "#205ea6", Selection: "#dad8ce",
		Border: "#cecdc3", Green: "#66800b", Yellow: "#ad8301", Red: "#af3029", Blue: "#205ea6",
	},
	"osaka-jade": {
		Name: "osaka-jade", Label: "Osaka Jade", IsLight: false,
		Background: "#111c18", DarkBackground: "#0c1512", DarkerBackground: "#09100e", LighterBg: "#1b2b25",
		Foreground: "#d7e4dc", DarkForeground: "#759586", Accent: "#2dd4bf", Selection: "#233931",
		Border: "#1f332c", Green: "#34d399", Yellow: "#f59e0b", Red: "#f87171", Blue: "#38bdf8",
	},
	"hackerman": {
		Name: "hackerman", Label: "Hackerman", IsLight: false,
		Background: "#0b0f14", DarkBackground: "#070a0e", DarkerBackground: "#040608", LighterBg: "#141b24",
		Foreground: "#a8ffcf", DarkForeground: "#548c6e", Accent: "#22ff88", Selection: "#1a2b24",
		Border: "#192822", Green: "#22ff88", Yellow: "#facc15", Red: "#ff4d6d", Blue: "#38bdf8",
	},
	"lumon": {
		Name: "lumon", Label: "Lumon", IsLight: true,
		Background: "#e8eff0", DarkBackground: "#dce5e7", DarkerBackground: "#cedadf", LighterBg: "#f5f9fa",
		Foreground: "#16292e", DarkForeground: "#516b73", Accent: "#0d7c7b", Selection: "#c2d4d8",
		Border: "#b7cacf", Green: "#15803d", Yellow: "#b45309", Red: "#b91c1c", Blue: "#0369a1",
	},
	"ristretto": {
		Name: "ristretto", Label: "Ristretto", IsLight: false,
		Background: "#2c2525", DarkBackground: "#221c1c", DarkerBackground: "#191515", LighterBg: "#393030",
		Foreground: "#e6d9d9", DarkForeground: "#9e8e8e", Accent: "#f38d70", Selection: "#4a3f3f",
		Border: "#403535", Green: "#adda78", Yellow: "#f9cc6c", Red: "#fd6883", Blue: "#85dacc",
	},
	"ethereal": {
		Name: "ethereal", Label: "Ethereal", IsLight: false,
		Background: "#0e141f", DarkBackground: "#0a0e17", DarkerBackground: "#070a11", LighterBg: "#182132",
		Foreground: "#dce8ff", DarkForeground: "#8094ba", Accent: "#7dcfff", Selection: "#22304a",
		Border: "#1f2b42", Green: "#73daca", Yellow: "#e0af68", Red: "#f7768e", Blue: "#7dcfff",
	},
	"miasma": {
		Name: "miasma", Label: "Miasma", IsLight: false,
		Background: "#222222", DarkBackground: "#191919", DarkerBackground: "#121212", LighterBg: "#2e2e2e",
		Foreground: "#c2c2b0", DarkForeground: "#78786a", Accent: "#78824b", Selection: "#3c3c34",
		Border: "#33332d", Green: "#5f875f", Yellow: "#b36d43", Red: "#bb7744", Blue: "#5f8787",
	},
	"vantablack": {
		Name: "vantablack", Label: "Vantablack", IsLight: false,
		Background: "#0a0a0a", DarkBackground: "#050505", DarkerBackground: "#000000", LighterBg: "#161616",
		Foreground: "#f0f0f0", DarkForeground: "#808080", Accent: "#ffffff", Selection: "#262626",
		Border: "#1f1f1f", Green: "#4ade80", Yellow: "#fbbf24", Red: "#f87171", Blue: "#93c5fd",
	},
	"white": {
		Name: "white", Label: "Pure White", IsLight: true,
		Background: "#ffffff", DarkBackground: "#f4f4f5", DarkerBackground: "#e4e4e7", LighterBg: "#ffffff",
		Foreground: "#18181b", DarkForeground: "#71717a", Accent: "#2563eb", Selection: "#e4e4e7",
		Border: "#d4d4d8", Green: "#16a34a", Yellow: "#ca8a04", Red: "#dc2626", Blue: "#2563eb",
	},
	"retro-82": {
		Name: "retro-82", Label: "Retro 82", IsLight: false,
		Background: "#181425", DarkBackground: "#110e1c", DarkerBackground: "#0b0913", LighterBg: "#26203a",
		Foreground: "#f4f4f4", DarkForeground: "#8b9bb4", Accent: "#feae34", Selection: "#3a4466",
		Border: "#2b2542", Green: "#63c74d", Yellow: "#feae34", Red: "#e43b44", Blue: "#0099db",
	},
	"lupine": {
		Name: "lupine", Label: "Lupine", IsLight: false,
		Background: "#1c1826", DarkBackground: "#15121d", DarkerBackground: "#0f0d15", LighterBg: "#282236",
		Foreground: "#e6dff5", DarkForeground: "#9284b5", Accent: "#a87ffb", Selection: "#39304d",
		Border: "#2f2740", Green: "#7ce38b", Yellow: "#f2cc60", Red: "#ff7b72", Blue: "#79c0ff",
	},
	"solitude": {
		Name: "solitude", Label: "Solitude", IsLight: false,
		Background: "#171a21", DarkBackground: "#111419", DarkerBackground: "#0c0e12", LighterBg: "#222731",
		Foreground: "#d8dee9", DarkForeground: "#7c8599", Accent: "#81a1c1", Selection: "#2e3440",
		Border: "#282d38", Green: "#8fbcbb", Yellow: "#ebcb8b", Red: "#bf616a", Blue: "#81a1c1",
	},
	"last-horizon": {
		Name: "last-horizon", Label: "Last Horizon", IsLight: false,
		Background: "#1b1518", DarkBackground: "#140f12", DarkerBackground: "#0e0a0c", LighterBg: "#281f24",
		Foreground: "#f1e3e8", DarkForeground: "#9d858e", Accent: "#e06c75", Selection: "#3b2c33",
		Border: "#32242a", Green: "#98c379", Yellow: "#e5c07b", Red: "#e06c75", Blue: "#61afef",
	},
}

func GetPreset(name string) (Palette, bool) {
	p, ok := builtInPalettes[name]
	return p, ok
}

func ListPresets() []Palette {
	names := make([]string, 0, len(builtInPalettes))
	for k := range builtInPalettes {
		if k == "diver" || k == "diver-light" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]Palette, 0, len(builtInPalettes))
	if p, ok := builtInPalettes["diver"]; ok {
		out = append(out, p)
	}
	if p, ok := builtInPalettes["diver-light"]; ok {
		out = append(out, p)
	}
	for _, n := range names {
		out = append(out, builtInPalettes[n])
	}
	return out
}
