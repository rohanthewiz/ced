// =============================================================================
// File: internal/editor/lens_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// fakeLensSource offers a fixed lens set per line and nothing else.
type fakeLensSource struct {
	byLine map[int][]Lens
}

// Decorations satisfies DecorationSource with nothing to paint.
func (fakeLensSource) Decorations(*Tab, theme.Theme, int, int) ([]Span, []GutterMark) {
	return nil, nil
}

// Lenses returns the fixed set.
func (s fakeLensSource) Lenses(*Tab, theme.Theme, int, int) map[int][]Lens { return s.byLine }

// threeLenses is the conflict resolver's set, in its order.
func threeLenses() []Lens {
	return []Lens{
		{Label: "Accept current", Short: "Current", ID: 1},
		{Label: "Accept incoming", Short: "Incoming", ID: 2},
		{Label: "Accept both", Short: "Both", ID: 3},
	}
}

// TestLens_PaintsFullLabelsAfterTheLine pins the placement (after the
// two-cell gap, like a note) and the separator between buttons.
func TestLens_PaintsFullLabelsAfterTheLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("<<<<<<< HEAD\nx")}
	tab.DecoSources = []DecorationSource{fakeLensSource{byLine: map[int][]Lens{0: threeLenses()}}}
	scr := noteScreen(t, tab, 90, 3)
	row := noteRow(scr, 0, 90)
	if !strings.Contains(row, "<<<<<<< HEAD  Accept current · Accept incoming · Accept both") {
		t.Errorf("row 0 = %q", row)
	}
}

// TestLens_HitTestReadsWhatWasPainted pins the stamped geometry: a click
// on the middle button's cells answers that button, the separator
// between buttons answers nothing.
func TestLens_HitTestReadsWhatWasPainted(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("<<<<<<< HEAD\nx")}
	tab.DecoSources = []DecorationSource{fakeLensSource{byLine: map[int][]Lens{0: threeLenses()}}}
	scr := noteScreen(t, tab, 90, 3)
	row := noteRow(scr, 0, 90)
	at := strings.Index(row, "Accept incoming")
	if at < 0 {
		t.Fatalf("lens not painted: %q", row)
	}
	h, ok := tab.LensAt(at+3, 0)
	if !ok || h.Lens.ID != 2 || h.Line != 0 {
		t.Fatalf("LensAt(incoming) = %+v, %v", h, ok)
	}
	if _, ok := tab.LensAt(at-2, 0); ok {
		t.Error("the separator answered as a button")
	}
	if _, ok := tab.LensAt(at+3, 1); ok {
		t.Error("the row below answered as a button")
	}
}

// TestLens_ShortLabelsThenDropFromTheRight pins the degradation order:
// full labels, then short ones, then whole buttons shed from the right —
// never a button cut mid-word.
func TestLens_ShortLabelsThenDropFromTheRight(t *testing.T) {
	set := threeLenses()
	if got := layoutLens(set, 100); len(got) != 3 || got[0] != "Accept current" {
		t.Errorf("roomy: %q", got)
	}
	// "Current · Incoming · Both" is 25 cells.
	if got := layoutLens(set, 25); len(got) != 3 || got[2] != "Both" {
		t.Errorf("short: %q", got)
	}
	if got := layoutLens(set, 20); len(got) != 2 || got[1] != "Incoming" {
		t.Errorf("tight: %q", got)
	}
	if got := layoutLens(set, 3); got != nil {
		t.Errorf("no room: %q", got)
	}
}

// TestLens_OutranksTheCaretNote pins the slot rule: on a line with a lens
// set, the caret note is not painted.
func TestLens_OutranksTheCaretNote(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("<<<<<<< HEAD\nx")}
	tab.DecoSources = []DecorationSource{fakeLensSource{byLine: map[int][]Lens{0: threeLenses()}}}
	tab.SetCaretNote(0, "✗ expected declaration", tcell.ColorRed)
	scr := noteScreen(t, tab, 90, 3)
	row := noteRow(scr, 0, 90)
	if strings.Contains(row, "expected declaration") || !strings.Contains(row, "Accept current") {
		t.Errorf("row 0 = %q", row)
	}
}

// TestLens_HitsDescribeOneFrame pins the reset: a frame whose source no
// longer offers the lens leaves nothing clickable behind.
func TestLens_HitsDescribeOneFrame(t *testing.T) {
	src := &fakeLensSource{byLine: map[int][]Lens{0: threeLenses()}}
	tab := &Tab{Buffer: NewBuffer("<<<<<<< HEAD\nx")}
	tab.DecoSources = []DecorationSource{src}
	noteScreen(t, tab, 90, 3)
	if len(tab.LensHits()) != 3 {
		t.Fatalf("hits = %d, want 3", len(tab.LensHits()))
	}
	src.byLine = nil
	noteScreen(t, tab, 90, 3)
	if len(tab.LensHits()) != 0 {
		t.Errorf("stale hits survived a frame without lenses: %+v", tab.LensHits())
	}
}

// TestLens_DroppedOnALineThatFillsThePane pins the never-squeezed rule
// from the other end: a line with no room left shows no buttons at all.
func TestLens_DroppedOnALineThatFillsThePane(t *testing.T) {
	long := "<<<<<<< " + strings.Repeat("x", 40)
	tab := &Tab{Buffer: NewBuffer(long)}
	tab.DecoSources = []DecorationSource{fakeLensSource{byLine: map[int][]Lens{0: threeLenses()}}}
	noteScreen(t, tab, 60, 2)
	if len(tab.LensHits()) != 0 {
		t.Errorf("painted %d buttons into no room", len(tab.LensHits()))
	}
}
