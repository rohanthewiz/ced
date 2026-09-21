// =============================================================================
// File: internal/editor/linenote_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// noteRow reads one row of a simulation screen back as text.
func noteRow(scr tcell.SimulationScreen, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		r, _, _, _ := scr.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// noteScreen renders tab into a w×h simulation screen.
func noteScreen(t *testing.T, tab *Tab, w, h int) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(w, h)
	tab.Render(scr, theme.Default(), 0, 0, w, h)
	return scr
}

// TestLineNote_PaintsAfterTheLine pins the placement: the note follows
// the line's text after a gap, on that line's row only.
func TestLineNote_PaintsAfterTheLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("x := f()\ny := 2\n")}
	tab.SetLineNotes(map[int]string{0: "x: int"})
	scr := noteScreen(t, tab, 60, 4)

	if row := noteRow(scr, 0, 60); !strings.Contains(row, "x := f()  » x: int") {
		t.Errorf("row 0 = %q", row)
	}
	if row := noteRow(scr, 1, 60); strings.Contains(row, "»") {
		t.Errorf("row 1 has a note it was never given: %q", row)
	}
}

// TestLineNote_CostsNoGeometry pins the whole reason notes live past
// end-of-line: a click on the note lands at the end of the line, and the
// caret's cell is where it would be with no notes at all.
func TestLineNote_CostsNoGeometry(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("x := f()\n")}
	bx, by, _ := tab.PosScreenCell(Position{Line: 0, Col: 8}, 60, 4)
	tab.SetLineNotes(map[int]string{0: "x: int"})
	noteScreen(t, tab, 60, 4)

	ax, ay, _ := tab.PosScreenCell(Position{Line: 0, Col: 8}, 60, 4)
	if ax != bx || ay != by {
		t.Errorf("end-of-line cell moved: (%d,%d) → (%d,%d)", bx, by, ax, ay)
	}
	if pos, ok := tab.HitTest(ax+6, ay, 60, 4); !ok || pos != (Position{Line: 0, Col: 8}) {
		t.Errorf("a click on the note = %+v, want end of line", pos)
	}
}

// TestLineNote_DroppedWhenThereIsNoRoom pins the no-squeeze rule, and
// that a note too long for its room is cut with a mark rather than
// running into the last column.
func TestLineNote_DroppedWhenThereIsNoRoom(t *testing.T) {
	long := strings.Repeat("a", 50)
	tab := &Tab{Buffer: NewBuffer(long + "\n")}
	tab.SetLineNotes(map[int]string{0: "x: int"})
	scr := noteScreen(t, tab, 60, 3)
	if row := noteRow(scr, 0, 60); strings.Contains(row, "»") {
		t.Errorf("a full line still drew a note: %q", row)
	}

	tab = &Tab{Buffer: NewBuffer("ab\n")}
	tab.SetLineNotes(map[int]string{0: strings.Repeat("n", 200)})
	scr = noteScreen(t, tab, 40, 3)
	row := noteRow(scr, 0, 40)
	if !strings.Contains(row, "…") {
		t.Errorf("an over-long note should be cut with a mark: %q", row)
	}
	if r, _, _, _ := scr.GetContent(39, 0); r != ' ' && r != 0 {
		t.Errorf("the last column must stay free, got %q", r)
	}
}

// TestLineNote_DiesWithTheRevision pins staleness: an edit retires the
// set, so a note never describes text the server did not see.
func TestLineNote_DiesWithTheRevision(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("x := f()\n")}
	tab.SetLineNotes(map[int]string{0: "x: int"})
	tab.InsertRune('z')
	if tab.LiveLineNotes() != nil {
		t.Error("an edit should retire the notes")
	}
	if row := noteRow(noteScreen(t, tab, 60, 3), 0, 60); strings.Contains(row, "»") {
		t.Errorf("a stale note was painted: %q", row)
	}
}
