// =============================================================================
// File: internal/theme/palette.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// palette.go is the data half of the theme package: a Palette is a flat
// map of canonical color keys to "#rrggbb" strings, and everything the
// editor renders is derived from it.
//
// The whole design turns on one idea: **only eight keys are required**.
// A theme author states `bg fg muted line accent ok warn err` — the
// background, the text, the two quieter greys, the accent, and the three
// semantic signal colors — and Normalize fills the other twenty-seven in
// a fixed dependency order (`selection` is a 32% accent wash over `bg`,
// `syn-string` is `ok`, `git-deleted` is `err`, …). That keeps a
// hand-written user theme eight lines long instead of thirty-five, and
// it means adding a new color key to the editor never invalidates a
// theme file somebody already wrote: the new key just derives.
//
// Any derived key can still be stated explicitly — a stated value always
// wins, and later derivations see it (syn-operator lightens whatever
// syn-type ended up being, authored or derived). The built-in themes use
// both halves: most state a dozen keys and let the rest fall out.
package theme

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Palette maps a canonical color key ("bg", "syn-keyword", …) to a hex
// color string. It is deliberately a map, not a struct: theme files on
// disk are sparse (see Normalize), and "was this key stated?" has to be
// answerable, which a struct of tcell.Color values cannot do — zero is a
// real color.
type Palette map[string]string

// CoreKeys are the eight colors every theme must state. They're the ones
// that cannot be inferred from anything else: the surface, the text on
// it, two levels of quiet, the accent, and the three semantic signals
// (ok / warn / err). Everything else in the editor is some blend or
// alias of these — see derivations.
func CoreKeys() []string {
	return []string{"bg", "fg", "muted", "line", "accent", "ok", "warn", "err"}
}

// derivation is one rule in the fill-in table: the key it produces and
// the function that computes it from the palette as it stands at that
// point. Order matters — a rule may read any key defined before it.
type derivation struct {
	key string
	fn  func(p Palette) string
}

// derivations is the ordered fill-in table applied by Normalize to every
// key the theme author left out. The order is a dependency order, not an
// alphabetical one: syn-operator reads syn-type, which reads accent and
// ok, so it has to come after both.
//
// The rules encode the editor's own color semantics, which is why they
// live here and not in each theme: "the selection is a wash of the
// accent over the background" is true of every theme ced will ever ship,
// so no theme should have to restate it.
func derivations() []derivation {
	return []derivation{
		// Surfaces. The sidebar steps away from the editor surface so
		// the two read as separate panels; the active-line highlight
		// steps the other way, toward the text.
		{"sidebar-bg", func(p Palette) string { return shadePanel(p["bg"]) }},
		{"line-hl", func(p Palette) string { return mix(p["bg"], p["fg"], 0.06) }},
		// The status bar is the one loud surface: it wears the accent
		// with the background as its text (see ToTheme's consumers).
		{"status-bg", func(p Palette) string { return p["accent"] }},

		// Accents & selection. accent-soft is pulled toward err rather
		// than merely lightened, because its main job is syn-keyword and
		// a lightened accent would land a hair away from syn-function
		// (which IS the accent) — keywords and calls in nearly the same
		// color is the single most visible way a derived palette can look
		// cheap. Blending toward the red/pink signal lands in the
		// purple-mauve range most schemes put keywords in.
		{"accent-soft", func(p Palette) string { return lighten(mix(p["accent"], p["err"], 0.45), 0.05) }},
		{"selection", func(p Palette) string { return mix(p["bg"], p["accent"], 0.32) }},

		// Signals. Modified (the dirty-tab dot) is the warn color by
		// definition — "this needs attention" is what warn means.
		{"modified", func(p Palette) string { return p["warn"] }},

		// Find. The all-hits tint is a wash so it never competes with
		// syntax; the active hit is full warn so it jumps off the page.
		{"find-match", func(p Palette) string { return mix(p["bg"], p["warn"], 0.38) }},
		{"find-current", func(p Palette) string { return p["warn"] }},

		// Git gutter — the near-universal green / blue / red convention,
		// expressed in the theme's own three signal colors.
		{"git-added", func(p Palette) string { return p["ok"] }},
		{"git-modified", func(p Palette) string { return p["accent"] }},
		{"git-deleted", func(p Palette) string { return p["err"] }},

		// LSP diagnostics reuse the same three, so severity reads at a
		// glance without a legend.
		{"diag-error", func(p Palette) string { return p["err"] }},
		{"diag-warning", func(p Palette) string { return p["warn"] }},
		{"diag-info", func(p Palette) string { return p["accent"] }},

		// File tree.
		{"folder", func(p Palette) string { return p["accent"] }},
		{"file", func(p Palette) string { return mix(p["fg"], p["muted"], 0.35) }},

		// Syntax. Keywords take the soft accent, strings the ok color,
		// numbers/constants the warn color — the same three-signal idea
		// applied to code instead of chrome.
		{"syn-keyword", func(p Palette) string { return p["accent-soft"] }},
		{"syn-string", func(p Palette) string { return p["ok"] }},
		{"syn-number", func(p Palette) string { return p["warn"] }},
		{"syn-comment", func(p Palette) string { return p["muted"] }},
		{"syn-function", func(p Palette) string { return p["accent"] }},
		// Types land between the accent and ok — that's where cyan sits
		// in a blue/green palette, which is what most schemes use here.
		{"syn-type", func(p Palette) string { return mix(p["accent"], p["ok"], 0.45) }},
		{"syn-builtin", func(p Palette) string { return p["err"] }},
		{"syn-variable", func(p Palette) string { return p["fg"] }},
		{"syn-punct", func(p Palette) string { return mix(p["fg"], p["muted"], 0.35) }},
		// Must follow syn-type: operators are its brighter sibling.
		{"syn-operator", func(p Palette) string { return lighten(p["syn-type"], 0.22) }},
		{"syn-constant", func(p Palette) string { return p["warn"] }},
	}
}

// Keys returns every canonical color key in canonical order: the eight
// core keys first, then the derived ones in derivation order. Used to
// write theme files with a stable, readable key order and to validate
// that a file contains nothing the editor doesn't understand.
func Keys() []string {
	out := CoreKeys()
	for _, d := range derivations() {
		out = append(out, d.key)
	}
	return out
}

// knownKeys is Keys() as a set, for the unknown-key check in Normalize.
func knownKeys() map[string]bool {
	set := make(map[string]bool, 40)
	for _, k := range Keys() {
		set[k] = true
	}
	return set
}

// Normalize returns a complete palette: every stated key validated and
// canonicalised to "#rrggbb", every unstated key filled in from the
// derivation table. The input is never mutated — sparse themes stay
// sparse on disk and are only expanded at resolve time.
//
// Errors are returned for the two things a user can genuinely get wrong:
// a missing core key (nothing can derive without them) and an
// unparseable or unknown key (almost always a typo — silently ignoring
// it would leave the author staring at a color that never changed).
func Normalize(p Palette) (Palette, error) {
	known := knownKeys()
	out := make(Palette, len(Keys()))
	// Copy + validate what the author actually stated.
	for k, v := range p {
		key := strings.ToLower(strings.TrimSpace(k))
		if !known[key] {
			return nil, fmt.Errorf("unknown color key %q (see %s)", k, strings.Join(CoreKeys(), ", ")+", …")
		}
		hex, err := canonHex(v)
		if err != nil {
			return nil, fmt.Errorf("color %q: %w", key, err)
		}
		out[key] = hex
	}
	// Core keys are the floor: without them there's nothing to derive from.
	var missing []string
	for _, k := range CoreKeys() {
		if out[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required color key(s): %s", strings.Join(missing, ", "))
	}
	// Fill the rest, in dependency order, skipping anything stated.
	for _, d := range derivations() {
		if out[d.key] != "" {
			continue
		}
		out[d.key] = d.fn(out)
	}
	return out, nil
}

// ToTheme converts a palette into the tcell-colored Theme struct the
// renderers consume. The palette must already be normalized — every
// canonical key present — which is why this is unexported-by-contract:
// callers go through Spec.Resolve, never here directly.
//
// The mapping is written out longhand rather than reflected so that
// adding a Theme field forces a deliberate decision about which color
// key feeds it (and, in derivations, how it falls out of the core eight).
func ToTheme(p Palette) Theme {
	c := func(key string) tcell.Color { return mustColor(p[key]) }
	return Theme{
		BG:        c("bg"),
		SidebarBG: c("sidebar-bg"),
		StatusBG:  c("status-bg"),
		LineHL:    c("line-hl"),

		Text:       c("fg"),
		Muted:      c("muted"),
		Subtle:     c("line"),
		Accent:     c("accent"),
		AccentSoft: c("accent-soft"),
		Selection:  c("selection"),
		Modified:   c("modified"),
		Error:      c("err"),

		FindMatch:   c("find-match"),
		FindCurrent: c("find-current"),

		GitAdded:    c("git-added"),
		GitModified: c("git-modified"),
		GitDeleted:  c("git-deleted"),

		DiagError:   c("diag-error"),
		DiagWarning: c("diag-warning"),
		DiagInfo:    c("diag-info"),

		FolderColor: c("folder"),
		FileColor:   c("file"),

		SynKeyword:  c("syn-keyword"),
		SynString:   c("syn-string"),
		SynNumber:   c("syn-number"),
		SynComment:  c("syn-comment"),
		SynFunction: c("syn-function"),
		SynType:     c("syn-type"),
		SynBuiltin:  c("syn-builtin"),
		SynVariable: c("syn-variable"),
		SynOperator: c("syn-operator"),
		SynPunct:    c("syn-punct"),
		SynConstant: c("syn-constant"),
	}
}

// --- color math -------------------------------------------------------
//
// Everything below works on canonical "#rrggbb" strings so the whole
// derivation table can be written as string→string rules, which keeps
// theme files, the derivation table, and the JSON round-trip all in one
// representation. The conversion to tcell.Color happens once, in ToTheme.

// canonHex validates a user-written color and returns it as "#rrggbb".
// Accepts "#abc" shorthand and a missing leading "#" because both are
// things people type; anything else is a typo worth reporting.
func canonHex(s string) (string, error) {
	v := strings.TrimSpace(s)
	v = strings.TrimPrefix(v, "#")
	switch len(v) {
	case 3:
		// #abc → #aabbcc, the CSS shorthand rule.
		v = string([]byte{v[0], v[0], v[1], v[1], v[2], v[2]})
	case 6:
	default:
		return "", fmt.Errorf("%q is not a hex color (want #rgb or #rrggbb)", s)
	}
	if _, err := strconv.ParseUint(v, 16, 32); err != nil {
		return "", fmt.Errorf("%q is not a hex color (want #rgb or #rrggbb)", s)
	}
	return "#" + strings.ToLower(v), nil
}

// rgb splits a canonical hex string into its three channels. It assumes
// canonHex has already run (the derivation table only ever sees
// normalized values), so a bad string degrades to black rather than
// erroring — there is no user-facing path that can reach it.
func rgb(hex string) (int, int, int) {
	v := strings.TrimPrefix(hex, "#")
	if len(v) != 6 {
		return 0, 0, 0
	}
	n, err := strconv.ParseUint(v, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(n>>16) & 0xff, int(n>>8) & 0xff, int(n) & 0xff
}

// hexOf reassembles three channels into a canonical hex string, clamping
// each to 0–255 so callers can do unclamped arithmetic.
func hexOf(r, g, b int) string {
	return fmt.Sprintf("#%02x%02x%02x", clamp8(r), clamp8(g), clamp8(b))
}

// clamp8 pins a value into the 0–255 channel range.
func clamp8(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// mix blends b into a by t (0 = pure a, 1 = pure b), channel-wise. This
// is the workhorse of the derivation table: every "wash" color
// (selection, find-match, line-hl) is a mix over the background.
func mix(a, b string, t float64) string {
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	f := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return hexOf(f(ar, br), f(ag, bg), f(ab, bb))
}

// lighten moves a color toward white by t. Used where a derived color
// needs to read as "the brighter sibling" of another (accent-soft,
// syn-operator) rather than as a wash over the surface.
func lighten(c string, t float64) string { return mix(c, "#ffffff", t) }

// darken moves a color toward black by t.
func darken(c string, t float64) string { return mix(c, "#000000", t) }

// shadePanel derives the sidebar/tab-strip surface from the editor
// background. Dark themes step down hard enough to read as a distinct
// panel; light themes only step down a little, because the same
// proportional change on a near-white background is far more visible
// (and a heavily darkened light sidebar looks like an error state).
func shadePanel(bg string) string {
	if IsDark(bg) {
		return darken(bg, 0.18)
	}
	return darken(bg, 0.06)
}

// Luminance returns the perceived brightness of a hex color on a 0–1
// scale using the Rec. 601 weights. Used to auto-detect whether a theme
// is light or dark when its file doesn't say, and to pick the panel
// shading direction.
func Luminance(hex string) float64 {
	r, g, b := rgb(hex)
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255.0
}

// IsDark reports whether a color is dark enough that light text belongs
// on it. The 0.5 threshold is the usual Rec. 601 midpoint — themes that
// sit right on the line should state `dark:` explicitly.
func IsDark(hex string) bool { return Luminance(hex) < 0.5 }

// mustColor converts a canonical hex string to a tcell color. Unparseable
// input yields black rather than panicking: this is only ever reached
// with a normalized palette, and a black cell is a better failure than a
// crashed editor.
func mustColor(hex string) tcell.Color {
	r, g, b := rgb(hex)
	return tcell.NewRGBColor(int32(r), int32(g), int32(b))
}
