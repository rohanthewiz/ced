// =============================================================================
// File: internal/app/diffselect_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// diffSelPaint is a roomy paint geometry for the pure pane tests: ten
// rows from (10, 5), forty cells wide — wide enough that each split
// half carries its line-number gutter.
var diffSelPaint = diffPaint{x: 10, y: 5, w: 40, n: 10}

// TestDiffSel_UnifiedDragCopiesDisplayText: a unified drag from inside
// one row to inside a later one copies the partial first row, the whole
// rows between (prefixes kept — it's a diff), and the head of the last.
func TestDiffSel_UnifiedDragCopiesDisplayText(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	if !p.diffSelStart(diffViewSample, rows, d, d.x+1, d.y+3) { // " a", after the space
		t.Fatal("press on the pane's rows did not arm a selection")
	}
	p.diffSelExtend(diffViewSample, rows, d, d.x+2, d.y+6) // "+B", whole
	if got, want := p.diffSelText(diffViewSample, rows), "a\n-b\n-c\n+B"; got != want {
		t.Fatalf("selection = %q, want %q", got, want)
	}
}

// TestDiffSel_PlainClickSelectsNothing: a press with no drag arms an
// anchor but no text — the release of an ordinary click must copy
// nothing.
func TestDiffSel_PlainClickSelectsNothing(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, rows, diffSelPaint, diffSelPaint.x+1, diffSelPaint.y+3)
	if _, _, ok := p.diffSelRange(); ok {
		t.Fatal("a plain click reported a selection")
	}
	if got := p.diffSelText(diffViewSample, rows); got != "" {
		t.Fatalf("plain click text = %q, want empty", got)
	}
}

// TestDiffSel_PressOutsideThePaneArmsNothing: only the pane's own rows
// start a selection — the gap column left of the text (the ▸ marker)
// and rows below the pane do not.
func TestDiffSel_PressOutsideThePaneArmsNothing(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	if p.diffSelStart(diffViewSample, rows, d, d.x-1, d.y) {
		t.Error("press left of the text armed a selection")
	}
	if p.diffSelStart(diffViewSample, rows, d, d.x, d.y+d.n) {
		t.Error("press below the pane armed a selection")
	}
}

// TestDiffSel_SplitStaysOnPressedSide: in split mode the side the press
// landed on holds for the whole drag even when the pointer crosses to
// the other half, the hole on that side is skipped, and a drag below
// the pane takes the last row to its end.
func TestDiffSel_SplitStaysOnPressedSide(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, true)
	d := diffSelPaint
	// Row 3 is the a|a pair; press on the RIGHT side's text.
	rx, _ := p.diffSelTextRect(rows[3], true, d)
	if !p.diffSelStart(diffViewSample, rows, d, rx, d.y+3) {
		t.Fatal("press did not arm")
	}
	if !p.sel.right {
		t.Fatal("press on the right half did not pick the right side")
	}
	p.diffSelExtend(diffViewSample, rows, d, d.x, d.y+d.n+2) // left half, below the pane
	if !p.sel.right {
		t.Fatal("dragging into the left half switched sides")
	}
	// Right side of rows 3..6: a, B, (hole under c), d.
	if got, want := p.diffSelText(diffViewSample, rows), "a\nB\nd"; got != want {
		t.Fatalf("selection = %q, want %q", got, want)
	}
}

// TestDiffSel_ClippedRowEllipsisTakesTheTail: a pointer on a clipped
// row's … cell selects to the END of the line, hidden part included —
// the pane has no horizontal scroll, so this is the only way to reach it.
func TestDiffSel_ClippedRowEllipsisTakesTheTail(t *testing.T) {
	lines := []string{"+abcdefghij"}
	var p diffPane
	rows := p.layout(lines, false)
	d := diffPaint{x: 0, y: 0, w: 6, n: 3}
	p.diffSelStart(lines, rows, d, 0, 0)
	p.diffSelExtend(lines, rows, d, 5, 0) // the … cell
	if got := p.diffSelText(lines, rows); got != lines[0] {
		t.Fatalf("selection = %q, want the whole line %q", got, lines[0])
	}
}

// TestDiffSel_AbovePaneClampsToTopRowStart: dragging above the pane
// lands on the first VISIBLE row's start, not on rows scrolled away.
func TestDiffSel_AbovePaneClampsToTopRowStart(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	d.scroll = 3
	got := p.diffSelPosAt(diffViewSample, rows, d, false, d.x+5, d.y-4)
	if got != (diffSelPos{row: 3}) {
		t.Fatalf("pos above the pane = %+v, want {row:3 col:0}", got)
	}
}

// TestDiffPane_LayoutRebuildClearsSelection: new text (a new slice) and
// a mode switch both rebuild the layout, and with it the selection —
// a highlight must never outlive the text it was made over.
func TestDiffPane_LayoutRebuildClearsSelection(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, rows, diffSelPaint, diffSelPaint.x, diffSelPaint.y)
	p.layout(diffViewSample, false) // same text, same mode: cached
	if !p.sel.active {
		t.Fatal("a cache hit dropped the selection")
	}
	p.layout(diffViewSample, true)
	if p.sel.active {
		t.Fatal("a mode switch kept the selection")
	}
	p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, p.rows, diffSelPaint, diffSelPaint.x, diffSelPaint.y)
	p.layout(append([]string(nil), diffViewSample...), false)
	if p.sel.active {
		t.Fatal("new text kept the selection")
	}
}

// TestDiffSel_SplitRectMatchesPaintedText pins diffSelTextRect against
// what drawDiffRows actually paints: on both split sides the rect's
// first cell is the line's first text rune, past the gutter.
func TestDiffSel_SplitRectMatchesPaintedText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var p diffPane
	rows := p.layout(diffViewSample, true)
	d := diffSelPaint
	d.style = func(string) tcell.Style { return tcell.StyleDefault }
	d.bg = a.theme.BG
	a.drawDiffRows(diffViewSample, &p, rows, d)
	lx, _ := p.diffSelTextRect(rows[4], false, d)
	rx, _ := p.diffSelTextRect(rows[4], true, d)
	if got := screenRow(t, a, d.y+4, lx, 1); got != "b" {
		t.Errorf("left rect starts on %q, want b", got)
	}
	if got := screenRow(t, a, d.y+4, rx, 1); got != "B" {
		t.Errorf("right rect starts on %q, want B", got)
	}
}

// TestPaintDiffSelection_BackgroundsOnlySelectedCells: the overlay
// re-backgrounds the selected cells with the theme's selection colour,
// keeps their glyphs, and leaves the rest of the row alone.
func TestPaintDiffSelection_BackgroundsOnlySelectedCells(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	d.style = func(string) tcell.Style { return tcell.StyleDefault.Background(a.theme.BG) }
	d.bg = a.theme.BG
	p.diffSelStart(diffViewSample, rows, d, d.x, d.y+4)    // "-b" from the prefix
	p.diffSelExtend(diffViewSample, rows, d, d.x+1, d.y+4) // just the "-"
	a.drawDiffRows(diffViewSample, &p, rows, d)
	mainc, _, st, _ := a.screen.GetContent(d.x, d.y+4)
	if _, bg, _ := st.Decompose(); bg != a.theme.Selection || mainc != '-' {
		t.Errorf("selected cell = %q bg %v, want '-' on the selection colour", mainc, bg)
	}
	_, _, st, _ = a.screen.GetContent(d.x+1, d.y+4)
	if _, bg, _ := st.Decompose(); bg == a.theme.Selection {
		t.Error("the cell after the selection was highlighted too")
	}
}
