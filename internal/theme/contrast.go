// =============================================================================
// File: internal/theme/contrast.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// contrast.go is the WCAG arithmetic every surface uses to keep text
// readable on a fill ced chose itself: blame bands, conflict labels, diff
// emphasis, the ✕ button, the markdown preview's code slab. The rule is
// always the same — keep the colour's HUE (it usually means something:
// a side, a role, a syntax class) and push only its lightness until it
// reads — so it lives here once, in the package every renderer already
// imports, rather than in app where the editor package cannot reach it.
//
// These work on tcell colours, not hex strings, on purpose: callers hold
// a resolved Theme and blend fills at paint time. Luminance (palette.go)
// is the different, Rec. 601 "is this a dark theme?" question asked of
// hex strings at load time; it is not the WCAG figure.
package theme

import (
	"math"

	"github.com/gdamore/tcell/v2"
)

// MinTextContrast is WCAG AA's ratio for body text, the floor ced lifts
// text to whenever it paints on a surface of its own making.
const MinTextContrast = 4.5

// RelLuminance is the WCAG relative luminance of an RGB colour, 0..1.
// Non-RGB colours (palette indexes, ColorDefault) count as black: every
// caller asks about colours it blended itself or a theme's hex-defined
// ones, and a terminal index has no RGB to measure.
func RelLuminance(c tcell.Color) float64 {
	r, g, b := c.RGB()
	if r < 0 {
		return 0
	}
	lin := func(v int32) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// ContrastRatio is the WCAG contrast ratio between two colours, 1..21.
// Symmetric: which one is the text does not matter.
func ContrastRatio(a, b tcell.Color) float64 {
	la, lb := RelLuminance(a), RelLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// LiftToContrast returns fg when it already reads at target:1 on bg;
// otherwise fg pushed toward white (dark bg) or black (light bg) in tenth
// steps, stopping at the first step that gets there. The hue survives
// the push — a purple stays a paler purple — which is the point: the
// colour keeps its meaning while becoming legible.
//
// Tenth steps rather than a solved exact mix: the overshoot is at most
// one step, nobody can see the difference between 4.5 and 4.7, and the
// stepping is what the blame and conflict colours were tuned against.
// A target the colour can't reach (a mid-grey bg) ends at pure white or
// black, the best available. ColorDefault bg and non-RGB fg come back
// unchanged — there is nothing to measure.
func LiftToContrast(fg, bg tcell.Color, target float64) tcell.Color {
	if bg == tcell.ColorDefault || ContrastRatio(fg, bg) >= target {
		return fg
	}
	tr, tg, tb := fg.RGB()
	if tr < 0 {
		return fg
	}
	var toward int32 // toward black on a light surface…
	if RelLuminance(bg) < 0.5 {
		toward = 255 // …toward white on a dark one.
	}
	out := fg
	for step := 1; step <= 10; step++ {
		f := float64(step) / 10
		mix := func(c int32) int32 { return int32(float64(c)*(1-f) + float64(toward)*f + 0.5) }
		out = tcell.NewRGBColor(mix(tr), mix(tg), mix(tb))
		if ContrastRatio(out, bg) >= target {
			break
		}
	}
	return out
}
