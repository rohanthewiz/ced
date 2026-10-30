// =============================================================================
// File: internal/theme/contrast_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package theme

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestContrastRatio_KnownPairs pins the WCAG endpoints — black on white
// is 21:1, a colour on itself 1:1 — and that the order of the arguments
// does not matter.
func TestContrastRatio_KnownPairs(t *testing.T) {
	black, white := tcell.NewRGBColor(0, 0, 0), tcell.NewRGBColor(255, 255, 255)
	if c := ContrastRatio(black, white); math.Abs(c-21) > 0.01 {
		t.Errorf("black/white = %.3f, want 21", c)
	}
	grey := tcell.NewRGBColor(0x80, 0x80, 0x80)
	if c := ContrastRatio(grey, grey); math.Abs(c-1) > 1e-9 {
		t.Errorf("grey on itself = %.3f, want 1", c)
	}
	purple := tcell.NewRGBColor(0x98, 0x76, 0xaa)
	if ContrastRatio(purple, grey) != ContrastRatio(grey, purple) {
		t.Error("ContrastRatio is not symmetric")
	}
}

// TestRelLuminance_NonRGBIsBlack pins the documented fallback: a
// terminal palette index has no RGB, and counts as black rather than
// producing NaN or a panic.
func TestRelLuminance_NonRGBIsBlack(t *testing.T) {
	if l := RelLuminance(tcell.ColorDefault); l != 0 {
		t.Errorf("ColorDefault luminance = %v, want 0", l)
	}
}

// TestLiftToContrast_ReachesTargetKeepingHue is the case that motivated
// moving the lift here: Darcula's syn-constant purple on the markdown
// code slab reads at ~2.8:1. Lifted, it must clear 4.5 and still be a
// purple — blue and red above green, as in the original.
func TestLiftToContrast_ReachesTargetKeepingHue(t *testing.T) {
	purple := tcell.NewRGBColor(0x98, 0x76, 0xaa)
	slab := tcell.NewRGBColor(0x3c, 0x3c, 0x3c)
	if ContrastRatio(purple, slab) >= MinTextContrast {
		t.Fatal("fixture no longer low-contrast; pick a darker colour")
	}
	out := LiftToContrast(purple, slab, MinTextContrast)
	if c := ContrastRatio(out, slab); c < MinTextContrast {
		t.Errorf("lifted contrast %.2f, want >= %.1f", c, MinTextContrast)
	}
	r, g, b := out.RGB()
	if !(r > g && b > g) {
		t.Errorf("lift lost the hue: got #%02x%02x%02x", r, g, b)
	}
}

// TestLiftToContrast_LightSurfaceDeepens pins the direction flip: on a
// light background the lift goes toward BLACK, not white.
func TestLiftToContrast_LightSurfaceDeepens(t *testing.T) {
	pale := tcell.NewRGBColor(0xaa, 0xaa, 0xcc)
	paper := tcell.NewRGBColor(0xfd, 0xf6, 0xe3)
	out := LiftToContrast(pale, paper, MinTextContrast)
	if RelLuminance(out) >= RelLuminance(pale) {
		t.Errorf("light-surface lift got brighter: %v -> %v", pale, out)
	}
	if c := ContrastRatio(out, paper); c < MinTextContrast {
		t.Errorf("lifted contrast %.2f, want >= %.1f", c, MinTextContrast)
	}
}

// TestLiftToContrast_LeavesReadableAndUnmeasurableAlone pins the three
// no-op paths: a colour already over target, a default background, and
// a non-RGB foreground all come back exactly as given.
func TestLiftToContrast_LeavesReadableAndUnmeasurableAlone(t *testing.T) {
	white := tcell.NewRGBColor(255, 255, 255)
	dark := tcell.NewRGBColor(0x1a, 0x1b, 0x26)
	if out := LiftToContrast(white, dark, MinTextContrast); out != white {
		t.Errorf("readable colour changed: %v", out)
	}
	dim := tcell.NewRGBColor(0x30, 0x30, 0x30)
	if out := LiftToContrast(dim, tcell.ColorDefault, MinTextContrast); out != dim {
		t.Errorf("default-bg lift changed the colour: %v", out)
	}
	if out := LiftToContrast(tcell.ColorDefault, dark, MinTextContrast); out != tcell.ColorDefault {
		t.Errorf("non-RGB fg changed: %v", out)
	}
}

// TestLiftToContrast_LowerTargetLiftsLess pins that the target is
// honoured rather than always 4.5: the markdown slab asks for a fenced
// comment's page contrast (~3.6 in Darcula), and must get a dimmer
// result than a 4.5 ask would.
func TestLiftToContrast_LowerTargetLiftsLess(t *testing.T) {
	grey := tcell.NewRGBColor(0x60, 0x60, 0x60)
	slab := tcell.NewRGBColor(0x3c, 0x3c, 0x3c)
	low := LiftToContrast(grey, slab, 3)
	high := LiftToContrast(grey, slab, MinTextContrast)
	if c := ContrastRatio(low, slab); c < 3 {
		t.Errorf("low target missed: %.2f", c)
	}
	if RelLuminance(low) >= RelLuminance(high) {
		t.Errorf("3:1 lift (%v) not dimmer than the 4.5:1 lift (%v)", low, high)
	}
}
