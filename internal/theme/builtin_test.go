// =============================================================================
// File: internal/theme/builtin_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the shipped themes. The palette derivation is exercised in
// palette_test.go; what these tests pin is that every theme ced offers a
// user actually resolves, is legible when it does, and that the default
// one still renders exactly what the editor rendered before named themes
// existed.

package theme

import (
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestBuiltin_TokyoNightMatchesDefault is the tripwire that keeps the
// named-theme path and the hardcoded fallback from drifting. Default()
// is the palette the editor shipped with and the safety net when a theme
// won't resolve; the "tokyo-night" built-in is what a user selects. If
// those two ever differ, switching to the default theme would visibly
// change the editor — which is the one thing "default" must never do.
func TestBuiltin_TokyoNightMatchesDefault(t *testing.T) {
	spec, ok := Find(Builtins(), DefaultName)
	if !ok {
		t.Fatalf("built-in %q missing from Builtins()", DefaultName)
	}
	got, err := spec.Resolve()
	if err != nil {
		t.Fatalf("resolve %s: %v", DefaultName, err)
	}
	if want := Default(); !reflect.DeepEqual(got, want) {
		// Report field by field — "structs differ" is useless when the
		// struct has 33 colors in it.
		gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
		for i := 0; i < gv.NumField(); i++ {
			if gv.Field(i).Interface() != wv.Field(i).Interface() {
				t.Errorf("%s: theme = %v, Default() = %v",
					gv.Type().Field(i).Name, gv.Field(i), wv.Field(i))
			}
		}
	}
}

// TestBuiltins_AllResolve walks every shipped theme and asserts it
// normalizes cleanly. A built-in with a typo'd hex or a missing core key
// would only be discovered by the user who picked it, so it has to fail
// here instead.
func TestBuiltins_AllResolve(t *testing.T) {
	for _, s := range Builtins() {
		if _, err := s.Resolve(); err != nil {
			t.Errorf("theme %q does not resolve: %v", s.Name, err)
		}
	}
}

// TestBuiltins_MetadataSane pins the registry-level invariants a picker
// depends on: unique ids, a human label on every row, and the shipped
// default sitting first (its position is the presentation contract
// documented in builtin.go).
func TestBuiltins_MetadataSane(t *testing.T) {
	specs := Builtins()
	if len(specs) < 10 {
		t.Errorf("Builtins() has %d themes, want at least 10", len(specs))
	}
	if specs[0].Name != DefaultName {
		t.Errorf("Builtins()[0] = %q, want the default %q", specs[0].Name, DefaultName)
	}
	seen := map[string]bool{}
	for _, s := range specs {
		if s.Name == "" || s.Label == "" {
			t.Errorf("theme %+v: name and label are both required", s)
		}
		if seen[s.Name] {
			t.Errorf("duplicate theme name %q — a picker would show two identical rows", s.Name)
		}
		seen[s.Name] = true
		if s.Source != SourceBuiltin {
			t.Errorf("theme %q: source = %q, want %q", s.Name, s.Source, SourceBuiltin)
		}
		if s.Path != "" {
			t.Errorf("theme %q: built-ins have no file path (got %q)", s.Name, s.Path)
		}
	}
}

// TestBuiltins_DarkFlagMatchesBackground checks that each theme's
// declared light/dark agrees with what its background actually looks
// like. The flag drives the "(light)" marker in the picker and the
// sidebar-shading direction, so a theme that lies about it gets a
// mislabelled row and a wrongly-shaded panel.
func TestBuiltins_DarkFlagMatchesBackground(t *testing.T) {
	for _, s := range Builtins() {
		p, err := Normalize(s.Colors)
		if err != nil {
			t.Fatalf("theme %q: %v", s.Name, err)
		}
		if got := IsDark(p["bg"]); got != s.Dark {
			t.Errorf("theme %q: Dark = %v but bg %s reads as dark=%v",
				s.Name, s.Dark, p["bg"], got)
		}
	}
}

// TestBuiltins_Legible walks every shipped theme through the contrast
// invariants the original single-theme test applied to Default(): text
// must not vanish into the background, the sidebar must read as its own
// panel, the selection must be visible, and the status bar (which paints
// the background color as its TEXT) must not swallow its own label.
// A theme that fails any of these is unusable, not merely ugly.
func TestBuiltins_Legible(t *testing.T) {
	for _, s := range Builtins() {
		th, err := s.Resolve()
		if err != nil {
			t.Fatalf("theme %q: %v", s.Name, err)
		}
		pairs := []struct {
			name string
			a, b tcell.Color
			// min is the minimum acceptable luminance gap (0–1). Body
			// text needs real separation; panel shading only needs to be
			// perceptible.
			min float64
		}{
			{"BG vs Text", th.BG, th.Text, 0.25},
			{"BG vs SidebarBG", th.BG, th.SidebarBG, 0.0},
			{"BG vs Selection", th.BG, th.Selection, 0.0},
			{"StatusBG vs BG (status bar paints BG as its text)", th.StatusBG, th.BG, 0.25},
			{"BG vs Muted (line numbers)", th.BG, th.Muted, 0.10},
			{"BG vs SynComment", th.BG, th.SynComment, 0.10},
		}
		for _, p := range pairs {
			if p.a == p.b {
				t.Errorf("theme %q: %s collide (%v)", s.Name, p.name, p.a)
				continue
			}
			if gap := colorGap(p.a, p.b); gap < p.min {
				t.Errorf("theme %q: %s luminance gap %.2f < %.2f", s.Name, p.name, gap, p.min)
			}
		}
	}
}

// colorGap returns the absolute luminance difference between two tcell
// colors, for the legibility assertions above.
func colorGap(a, b tcell.Color) float64 {
	lum := func(c tcell.Color) float64 {
		r, g, bl := c.RGB()
		return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 255.0
	}
	d := lum(a) - lum(b)
	if d < 0 {
		return -d
	}
	return d
}
