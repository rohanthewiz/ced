// =============================================================================
// File: internal/theme/builtin.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// builtin.go carries the themes ced ships with, and the Spec type that
// wraps a palette with the metadata a picker needs (id, human label,
// light/dark, where it came from).
//
// Presentation order is deliberate: the default first, then the rest of
// the dark themes, then the light ones. A terminal editor's audience
// skews dark, and burying "solarized-light" three rows from the top
// costs a light-theme user one scroll while putting it first would cost
// everyone else a mis-click.
//
// Only tokyo-night is authored in full. The other nine state their core
// eight plus the handful of keys that actually give the scheme its
// character, and let the derivation table (palette.go) fill the rest —
// which is also the working demonstration that the table produces a
// usable editor from a sparse theme, since these are the themes people
// will actually look at.
package theme

// Source records where a theme came from, so the picker can group and
// label rows and so "Customize theme…" knows which files it may write.
type Source string

const (
	// SourceBuiltin marks a theme compiled into the binary. Built-ins are
	// never written to disk and can't be deleted.
	SourceBuiltin Source = "builtin"
	// SourceUser marks a theme loaded from ~/.config/ced/themes/*.json.
	// A user theme whose name matches a built-in shadows it in place.
	SourceUser Source = "user"
	// SourceHost marks a theme synthesized from the terminal host's own
	// palette (the cats multiplexer — see internal/app/catstheme.go). It
	// exists on disk nowhere, lives only as long as the host connection,
	// and is rebuilt whenever the host's theme changes; the constant is
	// here rather than in the app so the picker's label and "Customize
	// theme…" (which may only write files it owns) can tell the three
	// origins apart.
	SourceHost Source = "host"
)

// DefaultName is the theme ced starts in when config.json says nothing —
// the Tokyo Night palette the editor shipped with before theming existed.
// Resolving it must reproduce theme.Default() exactly; a test pins that.
const DefaultName = "tokyo-night"

// Spec is a named theme: the metadata a picker shows plus the (possibly
// sparse) palette behind it. Colors is kept sparse on purpose — it is
// what a user's file literally said, so writing a Spec back out doesn't
// bloat an eight-line theme into thirty-five lines of derived values.
type Spec struct {
	// Name is the stable id used in config.json and on the command line.
	// Lowercase, hyphenated, unique within a registry.
	Name string
	// Label is the human-readable name shown in the picker.
	Label string
	// Dark reports whether this is a dark theme. Loaded files may omit
	// it, in which case it's inferred from the background's luminance.
	Dark bool
	// Colors is the palette as authored — possibly sparse. Resolve
	// normalizes it; it is never normalized in place.
	Colors Palette
	// Source is builtin or user. Path is set for user themes only.
	Source Source
	Path   string
}

// Resolve normalizes the spec's palette and converts it into the
// tcell-colored Theme the renderers consume. A malformed palette
// (missing core key, bad hex, unknown key) returns the error rather than
// a half-built theme — callers fall back to the default and flash.
func (s Spec) Resolve() (Theme, error) {
	p, err := Normalize(s.Colors)
	if err != nil {
		return Theme{}, err
	}
	return ToTheme(p), nil
}

// Builtins returns the shipped themes in presentation order: the default
// first, the remaining dark themes, then the light ones. The slice is
// rebuilt on each call so a caller can't mutate the shared table.
func Builtins() []Spec {
	return []Spec{
		// --- The default. Authored in full so that resolving it
		// reproduces the pre-theming palette byte for byte; every other
		// theme leans on the derivation table.
		{
			Name: DefaultName, Label: "Tokyo Night", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#1a1b26", "fg": "#c0caf5", "muted": "#565f89", "line": "#32344a",
				"accent": "#7aa2f7", "ok": "#9ece6a", "warn": "#e0af68", "err": "#f7768e",

				"sidebar-bg": "#16161e", "status-bg": "#7aa2f7", "line-hl": "#1f202e",
				"accent-soft": "#bb9af7", "selection": "#33467c", "modified": "#e89838",
				"find-match": "#6f521f", "find-current": "#e89838",
				"git-added": "#9ece6a", "git-modified": "#7aa2f7", "git-deleted": "#f7768e",
				"diag-error": "#f7768e", "diag-warning": "#e0af68", "diag-info": "#7aa2f7",
				"folder": "#7aa2f7", "file": "#a9b1d6",

				"syn-keyword": "#bb9af7", "syn-string": "#9ece6a", "syn-number": "#ff9e64",
				"syn-comment": "#565f89", "syn-function": "#7aa2f7", "syn-type": "#2ac3de",
				"syn-builtin": "#f7768e", "syn-variable": "#c0caf5", "syn-operator": "#89ddff",
				"syn-punct": "#a9b1d6", "syn-constant": "#ff9e64",
			},
		},

		// --- Dark themes.
		{
			Name: "darcula", Label: "Darcula", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#2b2b2b", "fg": "#a9b7c6", "muted": "#808080", "line": "#4b4b4b",
				"accent": "#6897bb", "ok": "#6a8759", "warn": "#ffc66d", "err": "#ff6b68",

				"sidebar-bg": "#3c3f41", "line-hl": "#323232", "selection": "#214283",
				"accent-soft": "#9876aa", "find-match": "#32593d",
				// JetBrains leaves types, operators, and punctuation the
				// plain text color — that restraint is most of what makes
				// Darcula read as Darcula, so state it rather than let the
				// table tint them.
				"syn-keyword": "#cc7832", "syn-string": "#6a8759", "syn-number": "#6897bb",
				"syn-function": "#ffc66d", "syn-type": "#a9b7c6", "syn-builtin": "#8888c6",
				"syn-constant": "#9876aa", "syn-operator": "#a9b7c6", "syn-punct": "#a9b7c6",
				"syn-variable": "#a9b7c6",
			},
		},
		{
			Name: "gruvbox-dark", Label: "Gruvbox Dark", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#282828", "fg": "#ebdbb2", "muted": "#928374", "line": "#504945",
				"accent": "#83a598", "ok": "#b8bb26", "warn": "#fabd2f", "err": "#fb4934",

				"sidebar-bg": "#1d2021", "line-hl": "#32302f", "selection": "#504945",
				"accent-soft": "#d3869b",
				"syn-keyword": "#fb4934", "syn-string": "#b8bb26", "syn-number": "#d3869b",
				"syn-function": "#8ec07c", "syn-type": "#fabd2f", "syn-builtin": "#fe8019",
				"syn-operator": "#fe8019", "syn-constant": "#d3869b", "syn-punct": "#bdae93",
			},
		},
		{
			Name: "solarized-dark", Label: "Solarized Dark", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#002b36", "fg": "#839496", "muted": "#586e75", "line": "#0e4451",
				"accent": "#268bd2", "ok": "#859900", "warn": "#b58900", "err": "#dc322f",

				"sidebar-bg": "#073642", "line-hl": "#073642", "selection": "#0a4a5c",
				"accent-soft": "#6c71c4",
				"syn-keyword": "#859900", "syn-string": "#2aa198", "syn-number": "#d33682",
				"syn-function": "#268bd2", "syn-type": "#b58900", "syn-builtin": "#cb4b16",
				"syn-constant": "#d33682", "syn-operator": "#859900", "syn-punct": "#839496",
				"syn-variable": "#93a1a1", "file": "#93a1a1",
			},
		},
		{
			Name: "cool-blue", Label: "Cool Blue (Nord)", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#2e3440", "fg": "#d8dee9", "muted": "#616e88", "line": "#3b4252",
				"accent": "#88c0d0", "ok": "#a3be8c", "warn": "#ebcb8b", "err": "#bf616a",

				"sidebar-bg": "#272c36", "line-hl": "#3b4252", "selection": "#434c5e",
				"accent-soft": "#81a1c1",
				"syn-keyword": "#81a1c1", "syn-string": "#a3be8c", "syn-number": "#b48ead",
				"syn-function": "#88c0d0", "syn-type": "#8fbcbb", "syn-builtin": "#d08770",
				"syn-operator": "#81a1c1", "syn-constant": "#b48ead", "syn-punct": "#d8dee9",
			},
		},
		{
			Name: "super-warm", Label: "Super Warm", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#2a1f1a", "fg": "#f0dfc8", "muted": "#9a7f6b", "line": "#4a352a",
				"accent": "#ffab5e", "ok": "#b6c25e", "warn": "#ffcf6b", "err": "#ff6f5e",

				"sidebar-bg": "#201713", "line-hl": "#35271f", "selection": "#5b3a25",
				"accent-soft": "#ffd2a1",
				"syn-keyword": "#ff9b6a", "syn-string": "#b6c25e", "syn-number": "#ffd479",
				"syn-function": "#ffc178", "syn-type": "#e6b98c", "syn-builtin": "#ff6f5e",
				"syn-comment": "#8a6a56", "syn-operator": "#ffd2a1", "syn-punct": "#c9ad95",
			},
		},
		{
			Name: "dark-game", Label: "Dark Game (Neon)", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#12101c", "fg": "#e6e1ff", "muted": "#6c6390", "line": "#2a2440",
				"accent": "#00e5ff", "ok": "#4dff9f", "warn": "#ffcf3d", "err": "#ff3d71",

				"sidebar-bg": "#0c0a14", "line-hl": "#1c1830", "selection": "#372b6b",
				"accent-soft": "#c77dff", "find-match": "#4a3a12",
				"syn-keyword": "#ff5cf5", "syn-string": "#4dff9f", "syn-number": "#ffcf3d",
				"syn-function": "#00e5ff", "syn-type": "#7df9ff", "syn-builtin": "#ff3d71",
				"syn-comment": "#5b5480", "syn-operator": "#c77dff", "syn-punct": "#a49ccc",
			},
		},
		{
			Name: "dark-city", Label: "Dark City (Noir)", Dark: true, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#1b1f23", "fg": "#c3ccd6", "muted": "#5d6a76", "line": "#2b333b",
				"accent": "#7fb3d5", "ok": "#8fbf7f", "warn": "#d6b06a", "err": "#d9737a",

				"sidebar-bg": "#14181c", "line-hl": "#232a30", "selection": "#33414d",
				"accent-soft": "#a8c8dd",
				"syn-keyword": "#a8c8dd", "syn-string": "#8fbf7f", "syn-number": "#d6b06a",
				"syn-function": "#7fb3d5", "syn-type": "#9ec9c4", "syn-builtin": "#d9737a",
				"syn-comment": "#566370", "syn-operator": "#b0bcc7", "syn-punct": "#93a1ae",
			},
		},

		// --- Light themes. Last on purpose (see the package comment):
		// the audience skews dark, and a mis-click into a light theme in
		// a dim room is the one theme switch that actually hurts.
		{
			Name: "solarized-light", Label: "Solarized Light", Dark: false, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#fdf6e3", "fg": "#657b83", "muted": "#93a1a1", "line": "#d5cdb6",
				"accent": "#268bd2", "ok": "#859900", "warn": "#b58900", "err": "#dc322f",

				"sidebar-bg": "#eee8d5", "line-hl": "#f2ecd8", "selection": "#cfe0ea",
				"accent-soft": "#6c71c4", "find-match": "#f2e2b0",
				"syn-keyword": "#859900", "syn-string": "#2aa198", "syn-number": "#d33682",
				"syn-function": "#268bd2", "syn-type": "#b58900", "syn-builtin": "#cb4b16",
				"syn-constant": "#d33682", "syn-operator": "#859900", "syn-punct": "#657b83",
				"syn-variable": "#586e75", "file": "#586e75",
			},
		},
		{
			Name: "corporate", Label: "Corporate (Light)", Dark: false, Source: SourceBuiltin,
			Colors: Palette{
				"bg": "#ffffff", "fg": "#1f2933", "muted": "#7b8794", "line": "#cbd2d9",
				"accent": "#2b6cb0", "ok": "#2f855a", "warn": "#b7791f", "err": "#c53030",

				"sidebar-bg": "#f3f5f7", "line-hl": "#eef2f6", "selection": "#cfe0f5",
				"accent-soft": "#6b46c1", "find-match": "#fdefc3",
				"syn-keyword": "#6b46c1", "syn-string": "#2f855a", "syn-number": "#b7791f",
				"syn-function": "#2b6cb0", "syn-type": "#0987a0", "syn-builtin": "#c53030",
				"syn-operator": "#0987a0", "syn-constant": "#b7791f", "syn-punct": "#4a5568",
				"syn-variable": "#1f2933", "file": "#3e4c59",
			},
		},
	}
}
