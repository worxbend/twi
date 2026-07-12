package theme

import "sort"

// presets holds the built-in named palettes. Nord, Dracula, Gruvbox,
// Solarized Dark, Monokai, One Dark, Tokyo Night, Catppuccin Mocha, and Rose
// Pine use each scheme's well-known published colors. Claude, Codex, Btop,
// and Mono are authored for this project.
var presets = map[string]Palette{
	"claude": {
		Background: "#1a1523",
		Foreground: "#f2ede4",
		Accent:     "#d97757",
		Muted:      "#948f9c",
		Border:     "#4a4358",
		Surface:    "#241d30",
		Warning:    "#e0a72e",
		Error:      "#e0685a",
		Success:    "#7fbf8e",
	},
	"codex": {
		Background: "#0d1117",
		Foreground: "#e6edf3",
		Accent:     "#3fb950",
		Muted:      "#8b949e",
		Border:     "#30363d",
		Surface:    "#161b22",
		Warning:    "#d29922",
		Error:      "#f85149",
		Success:    "#3fb950",
	},
	"btop": {
		Background: "#000000",
		Foreground: "#d3d3d3",
		Accent:     "#00ff00",
		Muted:      "#5a5a5a",
		Border:     "#3a3a3a",
		Surface:    "#101010",
		Warning:    "#ffdd33",
		Error:      "#ff3333",
		Success:    "#00ff00",
	},
	"nord": {
		Background: "#2e3440",
		Foreground: "#eceff4",
		Accent:     "#88c0d0",
		Muted:      "#4c566a",
		Border:     "#3b4252",
		Surface:    "#3b4252",
		Warning:    "#ebcb8b",
		Error:      "#bf616a",
		Success:    "#a3be8c",
	},
	"dracula": {
		Background: "#282a36",
		Foreground: "#f8f8f2",
		Accent:     "#bd93f9",
		Muted:      "#6272a4",
		Border:     "#44475a",
		Surface:    "#343746",
		Warning:    "#f1fa8c",
		Error:      "#ff5555",
		Success:    "#50fa7b",
	},
	"gruvbox": {
		Background: "#282828",
		Foreground: "#ebdbb2",
		Accent:     "#fe8019",
		Muted:      "#928374",
		Border:     "#3c3836",
		Surface:    "#32302f",
		Warning:    "#fabd2f",
		Error:      "#fb4934",
		Success:    "#b8bb26",
	},
	"solarized-dark": {
		Background: "#002b36",
		Foreground: "#839496",
		Accent:     "#268bd2",
		Muted:      "#586e75",
		Border:     "#073642",
		Surface:    "#073642",
		Warning:    "#b58900",
		Error:      "#dc322f",
		Success:    "#859900",
	},
	"monokai": {
		Background: "#272822",
		Foreground: "#f8f8f2",
		Accent:     "#f92672",
		Muted:      "#75715e",
		Border:     "#3e3d32",
		Surface:    "#3e3d32",
		Warning:    "#e6db74",
		Error:      "#f92672",
		Success:    "#a6e22e",
	},
	"one-dark": {
		Background: "#282c34",
		Foreground: "#abb2bf",
		Accent:     "#61afef",
		Muted:      "#5c6370",
		Border:     "#3e4451",
		Surface:    "#21252b",
		Warning:    "#e5c07b",
		Error:      "#e06c75",
		Success:    "#98c379",
	},
	"tokyo-night": {
		Background: "#1a1b26",
		Foreground: "#c0caf5",
		Accent:     "#7aa2f7",
		Muted:      "#565f89",
		Border:     "#414868",
		Surface:    "#24283b",
		Warning:    "#e0af68",
		Error:      "#f7768e",
		Success:    "#9ece6a",
	},
	"catppuccin-mocha": {
		Background: "#1e1e2e",
		Foreground: "#cdd6f4",
		Accent:     "#cba6f7",
		Muted:      "#a6adc8",
		Border:     "#45475a",
		Surface:    "#313244",
		Warning:    "#f9e2af",
		Error:      "#f38ba8",
		Success:    "#a6e3a1",
	},
	"rose-pine": {
		Background: "#191724",
		Foreground: "#e0def4",
		Accent:     "#c4a7e7",
		Muted:      "#6e6a86",
		Border:     "#403d52",
		Surface:    "#26233a",
		Warning:    "#f6c177",
		Error:      "#eb6f92",
		Success:    "#31748f",
	},
	"mono": {
		Background: "#000000",
		Foreground: "#ffffff",
		Accent:     "#ffffff",
		Muted:      "#808080",
		Border:     "#808080",
		Surface:    "#1a1a1a",
		Warning:    "#c0c0c0",
		Error:      "#ffffff",
		Success:    "#ffffff",
	},
}

// Presets returns the built-in named palettes, keyed by lowercase name.
// Callers must not mutate the returned map.
func Presets() map[string]Palette {
	return presets
}

// PresetNames returns preset keys in a stable, deterministic order.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
