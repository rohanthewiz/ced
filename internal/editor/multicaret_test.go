// =============================================================================
// File: internal/editor/multicaret_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// caretTab builds a text tab over the given lines with the cursor at
// the origin — the fixture every test here starts from.
func caretTab(lines ...string) *Tab {
	t := &Tab{Buffer: NewBuffer(strings.Join(lines, "\n"))}
	t.IndentUnit = "\t"
	t.initUndo()
	return t
}

// TestAddCaretLine_GrowsColumnAndPromotes pins the two things the
// workhorse gesture must do: each press adds a caret on the next line
// down, and the NEW caret becomes primary so EnsureVisible follows it.
func TestAddCaretLine_GrowsColumnAndPromotes(t *testing.T) {
	tab := caretTab("aaa", "bbb", "ccc", "ddd")
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor

	if !tab.AddCaretLine(1) {
		t.Fatal("first add below should succeed")
	}
	if !tab.AddCaretLine(1) {
		t.Fatal("second add below should succeed")
	}
	if got := tab.CaretCount(); got != 3 {
		t.Fatalf("caret count = %d, want 3", got)
	}
	if tab.Cursor != (Position{Line: 2, Col: 2}) {
		t.Fatalf("primary = %+v, want line 2 col 2 (the newest caret)", tab.Cursor)
	}
	var lines []int
	for _, c := range tab.AllCarets() {
		lines = append(lines, c.Cursor.Line)
	}
	if len(lines) != 3 || lines[0] != 0 || lines[1] != 1 || lines[2] != 2 {
		t.Fatalf("caret lines = %v, want [0 1 2] in document order", lines)
	}
}

// TestAddCaretLine_StopsAtBufferEdge confirms the gesture refuses rather
// than clamping onto a line that already has a caret — the caller
// flashes on false, and silently doing nothing would look like a bug.
func TestAddCaretLine_StopsAtBufferEdge(t *testing.T) {
	tab := caretTab("only line")
	if tab.AddCaretLine(1) {
		t.Error("add below on the last line should fail")
	}
	if tab.AddCaretLine(-1) {
		t.Error("add above on the first line should fail")
	}
	if tab.HasCarets() {
		t.Error("a refused add must not leave a caret behind")
	}
}

// TestAddCaretLine_AboveWalksFromTopmost pins the direction rule: "add
// above" extends from the topmost caret, not from the primary (which by
// then is at the bottom of a downward column).
func TestAddCaretLine_AboveWalksFromTopmost(t *testing.T) {
	tab := caretTab("a", "b", "c", "d")
	tab.Cursor = Position{Line: 2}
	tab.Anchor = tab.Cursor
	tab.AddCaretLine(-1) // caret on line 1, now primary

	if !tab.AddCaretLine(-1) {
		t.Fatal("second add above should succeed")
	}
	if tab.Cursor.Line != 0 {
		t.Fatalf("primary line = %d, want 0 (above the topmost caret)", tab.Cursor.Line)
	}
}

// TestAddCaretLine_KeepsGoalColumnAcrossShortLines pins the drift fix: a
// column started at the end of a long line must not walk left every time
// it crosses a short one. The clamped caret loses the column; the goal
// (the widest caret's column) carries it forward.
func TestAddCaretLine_KeepsGoalColumnAcrossShortLines(t *testing.T) {
	tab := caretTab("alpha := 1", "b := 2", "gamma := 3")
	tab.Cursor = Position{Line: 0, Col: 10} // end of the long first line
	tab.Anchor = tab.Cursor

	tab.AddCaretLine(1) // line 1 is only 6 runes — clamps to 6
	tab.AddCaretLine(1) // line 2 is 10 again — must return to col 10

	if tab.Cursor.Col != 10 {
		t.Fatalf("third caret at col %d, want 10 (the goal column)", tab.Cursor.Col)
	}
	tab.InsertString(";")
	if got := tab.Buffer.String(); got != "alpha := 1;\nb := 2;\ngamma := 3;" {
		t.Fatalf("append-at-column produced:\n%q", got)
	}
}

// TestInsertRune_FansOutToEveryCaret is the feature's whole point: one
// keystroke edits every caret's line, bottom-up so the earlier positions
// stay valid.
func TestInsertRune_FansOutToEveryCaret(t *testing.T) {
	tab := caretTab("one", "two", "three")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.AddCaretLine(1)
	tab.AddCaretLine(1)

	tab.InsertRune('#')

	want := []string{"#one", "#two", "#three"}
	for i, w := range want {
		if tab.Buffer.Lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, tab.Buffer.Lines[i], w)
		}
	}
	// Every caret advanced past its own insert.
	for _, c := range tab.AllCarets() {
		if c.Cursor.Col != 1 {
			t.Errorf("caret on line %d at col %d, want 1", c.Cursor.Line, c.Cursor.Col)
		}
	}
}

// TestMultiCaretEdit_IsOneUndoStep pins the undo contract: a burst
// across N carets collapses into a single snapshot, so one Esc-u puts
// the file back. Without undoSuppress this took three presses.
func TestMultiCaretEdit_IsOneUndoStep(t *testing.T) {
	tab := caretTab("one", "two", "three")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.AddCaretLine(1)
	tab.AddCaretLine(1)

	tab.InsertRune('#')
	if !tab.Undo() {
		t.Fatal("undo should report a step")
	}
	if got := tab.Buffer.String(); got != "one\ntwo\nthree" {
		t.Fatalf("after one undo:\n%q\nwant the original three lines", got)
	}
	if tab.CanUndo() {
		t.Error("the fan-out should have filed exactly one undo entry")
	}
	if tab.HasCarets() {
		t.Error("undo must drop secondary carets — their positions are stale")
	}
}

// TestBackspace_FansOutBottomUp pins the ordering rule with an edit that
// changes line COUNT: joining line 3 into line 2 must not disturb the
// caret still waiting on line 1.
func TestBackspace_FansOutBottomUp(t *testing.T) {
	tab := caretTab("ab", "cd", "ef")
	tab.Cursor = Position{Line: 1, Col: 0} // start of "cd" → joins with "ab"
	tab.Anchor = tab.Cursor
	tab.AddCaretLine(1) // start of "ef" → joins with "cd"

	tab.Backspace()

	if got := tab.Buffer.String(); got != "abcdef" {
		t.Fatalf("buffer = %q, want %q", got, "abcdef")
	}
}

// TestMultiCaretTyping_SameLine pins two carets on ONE line: the
// bottom-up order means the later column is edited first, so the earlier
// caret's column is still meaningful when its turn comes.
func TestMultiCaretTyping_SameLine(t *testing.T) {
	tab := caretTab("abcd")
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.AddCaretAt(Position{Line: 0, Col: 3})

	tab.InsertRune('-')

	if got := tab.Buffer.Lines[0]; got != "a-bc-d" {
		t.Fatalf("line = %q, want %q", got, "a-bc-d")
	}
}

// TestAddCaretAt_TogglesAndIgnoresPrimary pins the Alt+click rules: a
// second click on a caret removes it, and clicking the primary is a
// no-op rather than a duplicate.
func TestAddCaretAt_TogglesAndIgnoresPrimary(t *testing.T) {
	tab := caretTab("hello world")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	if tab.AddCaretAt(Position{Line: 0, Col: 0}) {
		t.Error("clicking the primary caret should report no change")
	}
	if !tab.AddCaretAt(Position{Line: 0, Col: 6}) {
		t.Fatal("adding a caret should report a change")
	}
	if got := tab.CaretCount(); got != 2 {
		t.Fatalf("caret count = %d, want 2", got)
	}
	if !tab.AddCaretAt(Position{Line: 0, Col: 6}) {
		t.Fatal("re-clicking a caret should report a change")
	}
	if tab.HasCarets() {
		t.Error("re-clicking a caret should have removed it")
	}
}

// TestAddNextOccurrence_SelectsThenAdds pins the two-stage gesture: the
// first press only selects the word (that's the user stating what to
// match), the second claims the next occurrence.
func TestAddNextOccurrence_SelectsThenAdds(t *testing.T) {
	tab := caretTab("count := 0", "count++", "total := count")
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor

	if !tab.AddNextOccurrence() {
		t.Fatal("first press should select the word")
	}
	if tab.HasCarets() {
		t.Fatal("first press must not add a caret yet")
	}
	if got := tab.SelectionText(); got != "count" {
		t.Fatalf("selection = %q, want %q", got, "count")
	}

	if !tab.AddNextOccurrence() {
		t.Fatal("second press should add the next occurrence")
	}
	if got := tab.CaretCount(); got != 2 {
		t.Fatalf("caret count = %d, want 2", got)
	}
	if tab.Cursor.Line != 1 {
		t.Fatalf("primary should have moved to the new occurrence, got line %d", tab.Cursor.Line)
	}
	tab.AddNextOccurrence()
	if got := tab.CaretCount(); got != 3 {
		t.Fatalf("caret count = %d, want 3", got)
	}
	// A fourth press has nowhere left to go and must not loop back onto
	// an occurrence that already has a caret.
	if tab.AddNextOccurrence() {
		t.Error("with every occurrence claimed the gesture should refuse")
	}
}

// TestAddNextOccurrence_WholeWordOnly confirms the match is whole-word
// when it comes from a bare cursor: claiming "count" must not land a
// caret inside "counter".
func TestAddNextOccurrence_WholeWordOnly(t *testing.T) {
	tab := caretTab("count", "counter", "count")
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.AddNextOccurrence() // select "count"
	tab.AddNextOccurrence() // claim the next whole-word hit

	if tab.Cursor.Line != 2 {
		t.Fatalf("next occurrence landed on line %d, want 2 (line 1 is 'counter')", tab.Cursor.Line)
	}
}

// TestSelectAllOccurrences_CaretsEverywhere pins the bulk gesture,
// including that the occurrence under the cursor stays primary so the
// viewport doesn't jump somewhere else in the file.
func TestSelectAllOccurrences_CaretsEverywhere(t *testing.T) {
	tab := caretTab("x := 1", "y := x", "z := x + x")
	tab.Cursor = Position{Line: 1, Col: 5} // inside the "x" on line 1
	tab.Anchor = tab.Cursor

	if got := tab.SelectAllOccurrences(); got != 4 {
		t.Fatalf("occurrence count = %d, want 4", got)
	}
	if got := tab.CaretCount(); got != 4 {
		t.Fatalf("caret count = %d, want 4", got)
	}
	if tab.Cursor.Line != 1 {
		t.Fatalf("primary line = %d, want 1 (the occurrence under the cursor)", tab.Cursor.Line)
	}
	// Every caret selects the identifier, so typing replaces all four.
	tab.InsertRune('q')
	if got := tab.Buffer.String(); got != "q := 1\ny := q\nz := q + q" {
		t.Fatalf("after typing over all carets:\n%q", got)
	}
}

// TestSelectAllOccurrences_UsesSelectionWhenPresent pins the other input
// to the gesture: a single-line selection matches as a substring, so a
// user can claim every "://" if they want to.
func TestSelectAllOccurrences_UsesSelectionWhenPresent(t *testing.T) {
	tab := caretTab("ab-ab", "cd-ab")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 2} // "ab"

	if got := tab.SelectAllOccurrences(); got != 3 {
		t.Fatalf("occurrence count = %d, want 3", got)
	}
}

// TestLineOps_DropCarets pins the collapse rule for the whole-line
// gestures: duplicate and move change the line count or order, so
// leaving carets behind would point them at the wrong lines.
func TestLineOps_DropCarets(t *testing.T) {
	dup := caretTab("a", "b", "c")
	dup.AddCaretLine(1)
	dup.DuplicateLines()
	if dup.HasCarets() {
		t.Error("DuplicateLines should collapse to one caret")
	}

	mv := caretTab("a", "b", "c")
	mv.Cursor = Position{Line: 1}
	mv.Anchor = mv.Cursor
	mv.AddCaretLine(1)
	mv.MoveLines(-1)
	if mv.HasCarets() {
		t.Error("MoveLines should collapse to one caret")
	}

	cm := &Tab{Buffer: NewBuffer("a\nb\nc"), Path: "x.go"}
	cm.initUndo()
	cm.AddCaretLine(1)
	cm.ToggleLineComment()
	if cm.HasCarets() {
		t.Error("ToggleLineComment should collapse to one caret")
	}
}

// TestMoveCursorTo_DropsCarets pins the safety rule that makes the whole
// design tolerable: an explicit jump (click, find hit, definition) can
// never leave carets editing text the user isn't looking at.
func TestMoveCursorTo_DropsCarets(t *testing.T) {
	tab := caretTab("a", "b", "c")
	tab.AddCaretLine(1)
	if !tab.HasCarets() {
		t.Fatal("fixture should have a secondary caret")
	}
	tab.MoveCursorTo(Position{Line: 2}, false)
	if tab.HasCarets() {
		t.Error("an explicit jump must drop secondary carets")
	}
}

// TestMoveCursor_MovesEveryCaret pins the opposite rule for arrows: they
// move the whole set, which is how a column of carets gets lined up
// (place carets, press End, type).
func TestMoveCursor_MovesEveryCaret(t *testing.T) {
	tab := caretTab("short", "much longer line", "mid")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.AddCaretLine(1)
	tab.AddCaretLine(1)

	tab.MoveLineEnd(false)

	for _, c := range tab.AllCarets() {
		want := len([]rune(tab.Buffer.Lines[c.Cursor.Line]))
		if c.Cursor.Col != want {
			t.Errorf("caret on line %d at col %d, want end-of-line %d", c.Cursor.Line, c.Cursor.Col, want)
		}
	}
	tab.InsertString(";")
	if got := tab.Buffer.String(); got != "short;\nmuch longer line;\nmid;" {
		t.Fatalf("append-to-each produced:\n%q", got)
	}
}

// TestNormalizeCarets_DropsDuplicates pins the collapse rule: an edit
// can drive two carets onto one position, and leaving both would double
// every keystroke there afterwards.
func TestNormalizeCarets_DropsDuplicates(t *testing.T) {
	tab := caretTab("abc")
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.Carets = []Caret{
		{Cursor: Position{Line: 0, Col: 2}, Anchor: Position{Line: 0, Col: 2}},
		{Cursor: Position{Line: 0, Col: 2}, Anchor: Position{Line: 0, Col: 2}},
		{Cursor: Position{Line: 0, Col: 1}, Anchor: Position{Line: 0, Col: 1}}, // == primary
		{Cursor: Position{Line: 9, Col: 9}, Anchor: Position{Line: 9, Col: 9}}, // out of range
	}
	tab.normalizeCarets()

	if got := len(tab.Carets); got != 2 {
		t.Fatalf("secondary carets = %d, want 2 (one dedup, one primary match)", got)
	}
	for _, c := range tab.Carets {
		if c.Cursor.Line >= tab.Buffer.LineCount() {
			t.Errorf("caret %+v was not clamped into the buffer", c.Cursor)
		}
	}
}

// TestSelectionText_JoinsEveryCaret pins the copy behaviour: copying a
// column of matches yields the column, one selection per line.
func TestSelectionText_JoinsEveryCaret(t *testing.T) {
	tab := caretTab("alpha", "beta", "gamma")
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.SelectAllOccurrences() // nothing repeats — falls back to one caret
	tab.Carets = nil

	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Carets = []Caret{{Anchor: Position{Line: 1, Col: 0}, Cursor: Position{Line: 1, Col: 4}}}

	if got := tab.SelectionText(); got != "alpha\nbeta" {
		t.Fatalf("SelectionText = %q, want %q", got, "alpha\nbeta")
	}
}

// TestPaintCarets_DrawsInvertedCell pins the render path: a secondary
// caret shows up as the rune under it painted on the accent, since the
// terminal's one hardware cursor belongs to the primary.
func TestPaintCarets_DrawsInvertedCell(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)
	th := theme.Default()

	tab := caretTab("abc", "def")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.AddCaretAt(Position{Line: 1, Col: 1}) // over the 'e'
	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()

	cells, _, _ := scr.GetContents()
	// Content starts one cell past the gutter; row 1, column 1 of the line.
	idx := 1*40 + (gutterWidth + 1) + 1
	if got := cells[idx].Runes[0]; got != 'e' {
		t.Fatalf("caret cell rune = %q, want 'e'", got)
	}
	fg, bg, _ := cells[idx].Style.Decompose()
	if bg != th.Accent {
		t.Errorf("caret cell bg = %v, want the accent %v", bg, th.Accent)
	}
	if fg == th.Accent {
		t.Error("caret cell fg must contrast with its bg")
	}
}

// TestPaintCarets_EndOfLine covers the case that kept carets out of the
// decoration system: a caret past the last rune has no cell to restyle,
// so it has to paint its own.
func TestPaintCarets_EndOfLine(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)
	th := theme.Default()

	tab := caretTab("ab", "cd")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.AddCaretAt(Position{Line: 1, Col: 2}) // past the end of "cd"
	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()

	cells, _, _ := scr.GetContents()
	idx := 1*40 + (gutterWidth + 1) + 2
	_, bg, _ := cells[idx].Style.Decompose()
	if bg != th.Accent {
		t.Fatalf("end-of-line caret bg = %v, want the accent %v", bg, th.Accent)
	}
}

// TestCaretQuery_RejectsUnusableRanges pins what the occurrence gestures
// refuse to guess at: a multi-line selection, a whitespace-only one, and
// a cursor sitting in punctuation.
func TestCaretQuery_RejectsUnusableRanges(t *testing.T) {
	tab := caretTab("alpha", "beta")

	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 1, Col: 2}
	if _, _, ok := tab.caretQuery(); ok {
		t.Error("a multi-line selection should not name a query")
	}

	tab2 := caretTab("  x")
	tab2.Anchor = Position{Line: 0, Col: 0}
	tab2.Cursor = Position{Line: 0, Col: 2}
	if _, _, ok := tab2.caretQuery(); ok {
		t.Error("a whitespace-only selection should not name a query")
	}

	tab3 := caretTab("a + b")
	tab3.Cursor = Position{Line: 0, Col: 2} // the '+'
	tab3.Anchor = tab3.Cursor
	if _, _, ok := tab3.caretQuery(); ok {
		t.Error("a cursor in punctuation should not name a query")
	}
}

// TestImageTab_RefusesCarets confirms the read-only preview mode stays
// read-only: no gesture may put a caret on an image tab.
func TestImageTab_RefusesCarets(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer(""), Mode: imageMode}
	if tab.AddCaretAt(Position{}) || tab.AddCaretLine(1) || tab.AddNextOccurrence() {
		t.Error("image tabs must refuse every caret gesture")
	}
	if tab.SelectAllOccurrences() != 0 {
		t.Error("image tabs must refuse select-all-occurrences")
	}
}
