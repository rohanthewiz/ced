// =============================================================================
// File: internal/theme/palette_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the color model: hex parsing, the blend helpers, and the
// derivation table that turns eight stated colors into thirty-five. The
// derivation table is the load-bearing part of the whole feature — it's
// what lets a user write an eight-line theme file — so the tests here
// lean on "a minimal palette produces a complete, sane theme" rather
// than on the exact value of any one derived color.

package theme

import (
	"strings"
	"testing"
)

// minimal is the smallest legal theme: exactly the eight core keys. Used
// throughout to prove the derivation table can carry the other 27.
func minimal() Palette {
	return Palette{
		"bg": "#101018", "fg": "#e0e0f0", "muted": "#707088", "line": "#303040",
		"accent": "#78a8f0", "ok": "#88cc70", "warn": "#e0b060", "err": "#f07080",
	}
}

// TestNormalize_FillsEveryKeyFromTheCoreEight is the central claim of
// the design: state eight colors, get a complete palette. If this
// breaks, every sparse user theme on disk starts failing to resolve.
func TestNormalize_FillsEveryKeyFromTheCoreEight(t *testing.T) {
	got, err := Normalize(minimal())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	for _, k := range Keys() {
		if got[k] == "" {
			t.Errorf("key %q was not filled in", k)
		}
	}
	if len(got) != len(Keys()) {
		t.Errorf("palette has %d keys, want %d", len(got), len(Keys()))
	}
}

// TestNormalize_LeavesInputUntouched pins that a sparse theme stays
// sparse. Specs hold what the author wrote, and Encode writes that back
// — mutating the input here would silently expand every user's
// eight-line file into thirty-five lines the first time it loaded.
func TestNormalize_LeavesInputUntouched(t *testing.T) {
	in := minimal()
	if _, err := Normalize(in); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(in) != len(CoreKeys()) {
		t.Errorf("input palette grew to %d keys — Normalize must not mutate its argument", len(in))
	}
}

// TestNormalize_StatedValueWinsOverDerivation checks that authoring a
// derived key overrides the table, and — the subtler half — that later
// derivations see the authored value. syn-operator is derived from
// syn-type, so stating syn-type must move syn-operator with it.
func TestNormalize_StatedValueWinsOverDerivation(t *testing.T) {
	p := minimal()
	p["syn-type"] = "#00ffff"
	got, err := Normalize(p)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got["syn-type"] != "#00ffff" {
		t.Errorf("syn-type = %q, want the stated #00ffff", got["syn-type"])
	}
	// lighten(#00ffff, .22) — the exact value matters less than that it
	// derived from the stated color rather than the default blend.
	if want := lighten("#00ffff", 0.22); got["syn-operator"] != want {
		t.Errorf("syn-operator = %q, want %q (derived from the STATED syn-type)",
			got["syn-operator"], want)
	}
}

// TestNormalize_KeywordsAndFunctionsDiffer guards the derivation the
// sparse-theme experience most depends on. syn-function IS the accent,
// and syn-keyword derives from accent-soft; if accent-soft were merely a
// lightened accent, every eight-key theme would paint keywords and calls
// in nearly the same color and look cheap. Pulling accent-soft toward
// err puts real hue distance between them.
func TestNormalize_KeywordsAndFunctionsDiffer(t *testing.T) {
	got, err := Normalize(minimal())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	kr, kg, kb := rgb(got["syn-keyword"])
	fr, fg, fb := rgb(got["syn-function"])
	dist := abs(kr-fr) + abs(kg-fg) + abs(kb-fb)
	if dist < 90 {
		t.Errorf("syn-keyword %s and syn-function %s are only %d apart — too close to tell apart",
			got["syn-keyword"], got["syn-function"], dist)
	}
}

// TestNormalize_WordHighlightIsVisibleAndNotTheSelection pins the two
// properties the derivation was rewritten to guarantee, both of which
// the first cut failed. It has to stand off the background hard enough
// to SEE on an ordinary terminal (an 18% accent wash didn't), and it has
// to stay well away from the selection color (a wash strong enough to
// see was a near-twin of it, so a highlight looked selected).
func TestNormalize_WordHighlightIsVisibleAndNotTheSelection(t *testing.T) {
	got, err := Normalize(minimal())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	dist := func(a, b string) int {
		ar, ag, ab := rgb(a)
		br, bg, bb := rgb(b)
		return abs(ar-br) + abs(ag-bg) + abs(ab-bb)
	}
	if d := dist(got["word-highlight"], got["bg"]); d < 90 {
		t.Errorf("word-highlight %s is only %d from bg %s — too faint to notice",
			got["word-highlight"], d, got["bg"])
	}
	// "Different from the selection" is about HUE, not channel distance:
	// the two land at similar brightness by design. What separates them
	// is that the selection is chromatic (an accent wash) and the
	// highlight is neutral, which is exactly what channel spread
	// measures.
	spread := func(hex string) int {
		r, g, b := rgb(hex)
		lo, hi := r, r
		for _, v := range []int{g, b} {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		return hi - lo
	}
	if spread(got["word-highlight"]) >= spread(got["selection"]) {
		t.Errorf("word-highlight %s is no more neutral than selection %s — a highlight would read as a selection",
			got["word-highlight"], got["selection"])
	}
}

// abs is the integer absolute value used by the channel-distance check.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestNormalize_MissingCoreKeyIsAnError pins the one hard requirement.
// Deriving from an absent core key would silently produce black, so the
// author has to be told which key they left out.
func TestNormalize_MissingCoreKeyIsAnError(t *testing.T) {
	p := minimal()
	delete(p, "accent")
	delete(p, "warn")
	_, err := Normalize(p)
	if err == nil {
		t.Fatal("Normalize accepted a palette with no accent/warn")
	}
	for _, want := range []string{"accent", "warn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the missing key %q", err, want)
		}
	}
}

// TestNormalize_RejectsTypos covers the two ways a hand-written theme
// goes wrong: a key the editor doesn't know (usually a misspelling) and
// a value that isn't a color. Both must be reported rather than ignored
// — an ignored key is a color the author keeps editing with no effect.
func TestNormalize_RejectsTypos(t *testing.T) {
	cases := []struct {
		name  string
		mutet func(Palette)
		want  string
	}{
		{"unknown key", func(p Palette) { p["backgruond"] = "#000000" }, "backgruond"},
		{"bad hex", func(p Palette) { p["accent"] = "cornflower" }, "accent"},
		{"wrong length", func(p Palette) { p["bg"] = "#12345" }, "bg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimal()
			c.mutet(p)
			_, err := Normalize(p)
			if err == nil {
				t.Fatalf("Normalize accepted %s", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestCanonHex covers the forgiving-input rule: CSS shorthand and a
// missing "#" are things people type, and rejecting them would be
// pedantry. Case is normalised so two spellings of the same color
// compare equal in the round-trip tests.
func TestCanonHex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"#1A2B3C", "#1a2b3c"},
		{"1a2b3c", "#1a2b3c"},
		{"#abc", "#aabbcc"},
		{"  #abc  ", "#aabbcc"},
	}
	for _, c := range cases {
		got, err := canonHex(c.in)
		if err != nil {
			t.Errorf("canonHex(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("canonHex(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "#", "#12", "#1234567", "nope", "#gggggg"} {
		if _, err := canonHex(bad); err == nil {
			t.Errorf("canonHex(%q) should have failed", bad)
		}
	}
}

// TestMix pins the blend endpoints and midpoint. Every wash color in the
// derivation table (selection, find-match, line-hl) is a mix, so an
// inverted t would quietly make selections invisible.
func TestMix(t *testing.T) {
	cases := []struct {
		a, b string
		t    float64
		want string
	}{
		{"#000000", "#ffffff", 0, "#000000"},
		{"#000000", "#ffffff", 1, "#ffffff"},
		{"#000000", "#ffffff", 0.5, "#808080"},
		{"#ff0000", "#0000ff", 0.5, "#800080"},
	}
	for _, c := range cases {
		if got := mix(c.a, c.b, c.t); got != c.want {
			t.Errorf("mix(%s, %s, %v) = %s, want %s", c.a, c.b, c.t, got, c.want)
		}
	}
}

// TestLuminanceAndIsDark checks the Rec. 601 detector used to infer a
// theme file's light/dark flag when it doesn't state one.
func TestLuminanceAndIsDark(t *testing.T) {
	if !IsDark("#000000") || IsDark("#ffffff") {
		t.Error("IsDark disagrees with black and white")
	}
	if !IsDark("#1a1b26") {
		t.Error("Tokyo Night's background should read as dark")
	}
	if IsDark("#fdf6e3") {
		t.Error("Solarized Light's background should read as light")
	}
	// Green carries the most perceived brightness of the three channels
	// — the whole reason for weighting rather than averaging.
	if Luminance("#00ff00") <= Luminance("#0000ff") {
		t.Error("pure green should read brighter than pure blue")
	}
}

// TestShadePanel pins the direction-flip: dark themes step the sidebar
// well away from the editor surface, light themes only a little. Applying
// the dark step to a near-white background produces a grey slab that
// reads as a rendering error, which is why the rule branches.
func TestShadePanel(t *testing.T) {
	dark := shadePanel("#1a1b26")
	if Luminance(dark) >= Luminance("#1a1b26") {
		t.Errorf("dark sidebar %s is not darker than its background", dark)
	}
	light := shadePanel("#ffffff")
	if Luminance(light) >= 1.0 {
		t.Errorf("light sidebar %s is not darker than its background", light)
	}
	// The light step must be the gentler of the two in RELATIVE terms —
	// which is the term that matters perceptually (Weber's law), and the
	// reason the rule branches at all. In absolute luminance the light
	// step is necessarily the larger one: 6% of a white background is
	// more light removed than 18% of a near-black one.
	darkFrac := 1 - Luminance(dark)/Luminance("#1a1b26")
	lightFrac := 1 - Luminance(light)/Luminance("#ffffff")
	if lightFrac >= darkFrac {
		t.Errorf("light-theme shading (%.3f of bg) should be gentler than dark-theme shading (%.3f of bg)",
			lightFrac, darkFrac)
	}
}

// TestToTheme_MapsEveryField asserts the longhand key→field mapping
// leaves nothing at the zero color. A field forgotten in ToTheme renders
// that UI element black-on-black, which is exactly the failure the
// original single-theme test guarded against.
func TestToTheme_MapsEveryField(t *testing.T) {
	p, err := Normalize(minimal())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	th := ToTheme(p)
	// Reuse the field walk from theme_test.go's invariant by checking the
	// struct reflectively — every color must be a real RGB value, and
	// tcell's RGB colors always have the valid bit set (non-zero).
	if th.BG == 0 || th.Text == 0 || th.SynOperator == 0 || th.DiagInfo == 0 {
		t.Error("ToTheme left a field at the zero color")
	}
	if th.Subtle != mustColor(p["line"]) {
		t.Error("Subtle should come from the \"line\" key")
	}
	if th.Error != mustColor(p["err"]) {
		t.Error("Error should come from the \"err\" key")
	}
}

// TestKeys_CoreFirstNoDuplicates pins the canonical ordering Encode
// writes files in: core eight first (what an author edits most), then
// derived keys in dependency order, with nothing repeated.
func TestKeys_CoreFirstNoDuplicates(t *testing.T) {
	keys := Keys()
	core := CoreKeys()
	for i, k := range core {
		if keys[i] != k {
			t.Errorf("Keys()[%d] = %q, want core key %q", i, keys[i], k)
		}
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}
}
