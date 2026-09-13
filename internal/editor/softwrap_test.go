// =============================================================================
// File: internal/editor/softwrap_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for soft wrap: the row layout itself, the paint, and the promise
// the whole file is built on — that rendering, hit-testing, the caret's
// screen cell, scrolling and Up/Down all agree about where a row is.

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// wrapTestW is the render width every test here uses: 6 gutter + 1 mark
// cell leaves 23 content cells, and the reserved last column leaves 22 to
// wrap at.
const (
	wrapTestW    = 30
	wrapTestRowW = 22
)

// newWrappedTab builds a wrapped tab over text and renders it once into a
// wrapTestW × h screen, which is what measures the wrap width the
// width-less helpers (Up/Down, LastVisibleLine) read.
func newWrappedTab(t *testing.T, text string, h int) (*Tab, tcell.SimulationScreen) {
	t.Helper()
	tab := &Tab{Buffer: NewBuffer(text), StyleStale: true}
	tab.SetSoftWrap(true)
	scr := newSimScreen(t, wrapTestW, h)
	t.Cleanup(scr.Fini)
	tab.Render(scr, theme.Default(), 0, 0, wrapTestW, h)
	scr.Show()
	return tab, scr
}

// cellAt returns the rune and style painted at (x, y).
func cellAt(scr tcell.SimulationScreen, x, y int) (rune, tcell.Style) {
	cells, w, _ := scr.GetContents()
	c := cells[y*w+x]
	if len(c.Runes) == 0 {
		return ' ', c.Style
	}
	return c.Runes[0], c.Style
}

// TestWrapStarts_WordsFirstThenCharacters pins the break rule: a row ends
// after the last space that fits, the space stays on the row it ends, and
// a run with nowhere to break is cut at the row's edge.
func TestWrapStarts_WordsFirstThenCharacters(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  []int
	}{
		{"prose breaks at words", "the quick brown foxtrot", 10, []int{0, 10, 16}},
		{"no spaces cuts at the edge", "abcdefghij", 4, []int{0, 4, 8}},
		{"empty line is one row", "", 10, []int{0}},
		{"fits exactly", "abcd", 4, []int{0}},
	}
	for _, c := range cases {
		got := wrapStarts([]rune(c.text), c.width)
		if len(got) != len(c.want) {
			t.Errorf("%s: starts = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: starts = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

// TestWrapStarts_RuneWiderThanRowTerminates is the infinite-loop guard: a
// tab expands to four cells, and in a two-cell row it can never fit. It
// must get a row of its own rather than spin forever looking for room.
func TestWrapStarts_RuneWiderThanRowTerminates(t *testing.T) {
	got := wrapStarts([]rune("\tx"), 2)
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("starts = %v, want [0 1]", got)
	}
}

// TestSetSoftWrap_IsAViewNotAnEdit pins that wrapping never touches the
// document: no EditRev bump, no dirty flag, the same text — and that it
// kills horizontal scroll, which has nothing left to reveal.
func TestSetSoftWrap_IsAViewNotAnEdit(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world"), ScrollX: 5}
	rev := tab.EditRev
	tab.SetSoftWrap(true)
	if !tab.IsSoftWrap() {
		t.Fatal("SetSoftWrap(true) did not turn wrap on")
	}
	if tab.EditRev != rev || tab.Dirty || tab.Buffer.String() != "hello world" {
		t.Fatal("wrapping must not edit the buffer")
	}
	if tab.ScrollX != 0 {
		t.Fatalf("ScrollX = %d, want 0 once wrapped", tab.ScrollX)
	}
	tab.ScrollH(3)
	if tab.ScrollX != 0 {
		t.Fatalf("a horizontal wheel moved a wrapped view to %d", tab.ScrollX)
	}
	tab.SetSoftWrap(false)
	if tab.IsSoftWrap() || tab.wrapW != 0 {
		t.Fatal("SetSoftWrap(false) must clear the flag and the cached width at once")
	}
}

// TestRender_SoftWrapPaintsContinuationRows checks the paint: a long line
// fills its rows, only the first row carries the line number, and the
// last content column is left empty for the caret and the overflow marker.
func TestRender_SoftWrapPaintsContinuationRows(t *testing.T) {
	_, scr := newWrappedTab(t, "short\n"+strings.Repeat("x", 50), 6)
	contentX := gutterWidth + 1

	if r, _ := cellAt(scr, 4, 1); r != '2' {
		t.Errorf("line 2's number = %q, want '2' on its first row", r)
	}
	if r, _ := cellAt(scr, 4, 2); r != ' ' {
		t.Errorf("a continuation row carries %q in the gutter, want blank", r)
	}
	for row, n := range []int{wrapTestRowW, wrapTestRowW, 6} {
		y := 1 + row
		if r, _ := cellAt(scr, contentX+n-1, y); r != 'x' {
			t.Errorf("row %d: last painted cell = %q, want 'x'", y, r)
		}
		if r, _ := cellAt(scr, contentX+n, y); r != ' ' {
			t.Errorf("row %d: cell past the row = %q, want blank", y, r)
		}
	}
	if r, _ := cellAt(scr, wrapTestW-1, 1); r != ' ' {
		t.Errorf("the reserved last column holds %q", r)
	}
	if r, _ := cellAt(scr, contentX, 4); r != ' ' {
		t.Errorf("row after the line's last row holds %q", r)
	}
}

// TestSoftWrap_HitTestRoundTripsPosScreenCell is the agreement test: for
// every column of a wrapped line, the cell PosScreenCell reports must
// hit-test straight back to that column. A disagreement here is a click
// landing on a different character than the one under the pointer.
func TestSoftWrap_HitTestRoundTripsPosScreenCell(t *testing.T) {
	line := "the quick brown fox jumps over the lazy dog and keeps\trunning along"
	tab, _ := newWrappedTab(t, line, 10)
	for col := 0; col <= len([]rune(line)); col++ {
		p := Position{Line: 0, Col: col}
		dx, dy, ok := tab.PosScreenCell(p, wrapTestW, 10)
		if !ok {
			t.Fatalf("col %d: PosScreenCell says off screen", col)
		}
		got, ok := tab.HitTest(dx, dy, wrapTestW, 10)
		if !ok || got != p {
			t.Fatalf("col %d at cell (%d,%d) hit-tests to %+v (ok=%v)", col, dx, dy, got, ok)
		}
	}
	// A column that starts a row draws at the head of THAT row.
	starts := wrapStarts([]rune(line), wrapTestRowW)
	dx, dy, _ := tab.PosScreenCell(Position{Line: 0, Col: starts[1]}, wrapTestW, 10)
	if dy != 1 || dx != gutterWidth+1 {
		t.Fatalf("row-start column drew at (%d,%d), want (%d,1)", dx, dy, gutterWidth+1)
	}
}

// TestSoftWrap_UpDownStepByRow pins what the arrows mean in a wrapped
// view: one screen row, keeping the caret's offset within the row, and
// crossing into the neighbouring line's nearest row at the edges.
func TestSoftWrap_UpDownStepByRow(t *testing.T) {
	tab, _ := newWrappedTab(t, strings.Repeat("x", 50)+"\nend", 10)
	tab.MoveCursorTo(Position{Line: 0, Col: 5}, false)

	steps := []struct {
		dLine int
		want  Position
	}{
		{1, Position{Line: 0, Col: 27}},  // row 1: 22 + 5
		{1, Position{Line: 0, Col: 49}},  // row 2: 44 + 5
		{1, Position{Line: 1, Col: 3}},   // "end" is shorter than the offset
		{-1, Position{Line: 0, Col: 47}}, // back onto line 0's last row, 44 + 3
		{-1, Position{Line: 0, Col: 25}},
	}
	for i, s := range steps {
		tab.MoveCursor(s.dLine, 0, false)
		if tab.Cursor != s.want {
			t.Fatalf("step %d: cursor = %+v, want %+v", i, tab.Cursor, s.want)
		}
	}
}

// TestSoftWrap_EnsureVisibleCountsRows pins the scroll rule in row units.
// Line 2 is well within six LINES of the top, but at three rows a line it
// starts on the seventh screen row — so a line-counting EnsureVisible
// would leave the caret below the fold.
func TestSoftWrap_EnsureVisibleCountsRows(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat(strings.Repeat("x", 50)+"\n", 10), "\n")
	tab, scr := newWrappedTab(t, text, 6)
	tab.MoveCursorTo(Position{Line: 2, Col: 0}, false)
	tab.Render(scr, theme.Default(), 0, 0, wrapTestW, 6)

	if tab.ScrollY != 1 {
		t.Fatalf("ScrollY = %d, want 1 (line 1's three rows plus the caret's row fit)", tab.ScrollY)
	}
	if !tab.CursorLineVisible(6) {
		t.Fatal("caret line should report visible after EnsureVisible")
	}
	if got := tab.LastVisibleLine(6); got != 2 {
		t.Fatalf("LastVisibleLine = %d, want 2", got)
	}
}

// TestSoftWrap_CaretOnRowBoundaryPaintedOnce pins paintCarets' row rule: a
// secondary caret at a column that starts a row draws there, and NOT also
// at the end of the row above, which would show one caret twice.
func TestSoftWrap_CaretOnRowBoundaryPaintedOnce(t *testing.T) {
	tab, scr := newWrappedTab(t, strings.Repeat("x", 50), 6)
	at := Position{Line: 0, Col: wrapTestRowW}
	tab.Carets = []Caret{{Cursor: at, Anchor: at}}
	tab.Render(scr, theme.Default(), 0, 0, wrapTestW, 6)
	scr.Show()

	contentX := gutterWidth + 1
	_, caret := cellAt(scr, contentX, 1)
	_, plain := cellAt(scr, contentX+1, 1)
	if caret == plain {
		t.Fatal("the caret at the head of row 1 was not painted")
	}
	_, endOfRow0 := cellAt(scr, contentX+wrapTestRowW, 0)
	_, endOfRow1 := cellAt(scr, contentX+wrapTestRowW, 1)
	if endOfRow0 != endOfRow1 {
		t.Fatal("the caret was also painted past the end of row 0")
	}
}
