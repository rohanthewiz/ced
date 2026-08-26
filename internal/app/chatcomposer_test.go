// =============================================================================
// File: internal/app/chatcomposer_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestComposerSanitize pins the paste/seed contract: CRLF and lone CR
// become one '\n' each, tabs become one space (rune == column is what
// the caret math is built on), and control noise is dropped.
func TestComposerSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\tb", "a b"},
		{"a\x00\x01b", "ab"},
		{"one\ntwo\n", "one\ntwo\n"},
	}
	for _, c := range cases {
		if got := composerSanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestComposerRows pins the hard-wrap derivation: logical lines split
// on '\n', each wrapped into width-sized spans, empty lines surviving
// as one empty row — including the empty composer itself.
func TestComposerRows(t *testing.T) {
	c := newChatComposer("")
	if got := c.rowCount(10); got != 1 {
		t.Fatalf("empty composer rows = %d, want 1", got)
	}
	c = newChatComposer("abcde\n\nfg")
	rows := c.rows(3)
	// "abcde" → [0,3) [3,5); "" → [6,6); "fg" → [7,9)
	want := [][2]int{{0, 3}, {3, 5}, {6, 6}, {7, 9}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
}

// TestComposerCaretRowCol pins the boundary rules: a caret on a wrap
// boundary belongs to the FOLLOWING row at column 0 (where the next
// typed rune lands), while a caret at a logical line's end stays on
// that line's last row.
func TestComposerCaretRowCol(t *testing.T) {
	c := newChatComposer("abcdef\ngh")
	// value: a b c d e f \n g h — wrap at 3: [0,3) [3,6) [7,9)
	c.cursor = 3 // wrap boundary → row 1 col 0
	if r, col := c.caretRowCol(3); r != 1 || col != 0 {
		t.Errorf("wrap boundary caret = (%d,%d), want (1,0)", r, col)
	}
	c.cursor = 6 // end of logical line 1 → row 1 col 3
	if r, col := c.caretRowCol(3); r != 1 || col != 3 {
		t.Errorf("line-end caret = (%d,%d), want (1,3)", r, col)
	}
	c.cursor = 9 // end of value → last row
	if r, col := c.caretRowCol(3); r != 2 || col != 2 {
		t.Errorf("value-end caret = (%d,%d), want (2,2)", r, col)
	}
}

// TestComposerMoveVertical pins the arrow contract: moves succeed while
// there is a row to land on (column clamped to the shorter row), and
// the edge rows report false — the caller's cue to fall back to prompt
// history.
func TestComposerMoveVertical(t *testing.T) {
	c := newChatComposer("longline\nab")
	c.cursor = 6 // row 0 col 6 at width 20
	if c.moveVertical(-1, 20) {
		t.Error("Up on the first row must report false")
	}
	if !c.moveVertical(1, 20) {
		t.Fatal("Down with a row below must move")
	}
	if c.cursor != len("longline\nab") { // col 6 clamps to "ab"'s end
		t.Errorf("cursor = %d after clamped Down", c.cursor)
	}
	if c.moveVertical(1, 20) {
		t.Error("Down on the last row must report false")
	}
}

// TestComposerHomeEndLineScoped pins Home/End as LOGICAL-line verbs —
// in a multi-line field they must not jump to the value's ends the way
// textField's do.
func TestComposerHomeEndLineScoped(t *testing.T) {
	c := newChatComposer("one\ntwo\nthree")
	c.cursor = 5 // inside "two"
	c.handleKey(tcell.NewEventKey(tcell.KeyHome, 0, 0))
	if c.cursor != 4 {
		t.Errorf("Home = %d, want 4 (start of \"two\")", c.cursor)
	}
	c.handleKey(tcell.NewEventKey(tcell.KeyEnd, 0, 0))
	if c.cursor != 7 {
		t.Errorf("End = %d, want 7 (end of \"two\")", c.cursor)
	}
}

// TestComposerBackspaceJoinsLines pins that deleting across a '\n'
// joins the lines — the multi-line twin of textField's backspace.
func TestComposerBackspaceJoinsLines(t *testing.T) {
	c := newChatComposer("ab\ncd")
	c.cursor = 3 // start of "cd"
	c.handleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	if got := c.String(); got != "abcd" {
		t.Errorf("joined value = %q, want abcd", got)
	}
}

// TestComposerInsertNewlineAndString pins the two splice verbs: a
// newline lands at the caret, and insertString keeps its breaks while
// sanitizing.
func TestComposerInsertNewlineAndString(t *testing.T) {
	c := newChatComposer("ab")
	c.cursor = 1
	c.insertNewline()
	if got := c.String(); got != "a\nb" {
		t.Fatalf("after newline = %q", got)
	}
	c.insertString("x\r\ny")
	if got := c.String(); got != "a\nx\nyb" {
		t.Errorf("after paste = %q, want a\\nx\\nyb", got)
	}
	if c.cursor != 5 {
		t.Errorf("caret = %d, want 5 (after the splice)", c.cursor)
	}
}

// TestComposerClickAt pins click-to-caret: rows clamp into the derived
// row space and a click past a row's end parks the caret at that row's
// end, never on the next line's text.
func TestComposerClickAt(t *testing.T) {
	c := newChatComposer("ab\ncdef")
	c.clickAt(10, 5, 10, 13, 5) // row 0, col 3 → clamps to "ab"'s end
	if c.cursor != 2 {
		t.Errorf("click past line end: caret = %d, want 2", c.cursor)
	}
	c.clickAt(10, 5, 10, 11, 6) // row 1, col 1 → inside "cdef"
	if c.cursor != 4 {
		t.Errorf("row-1 click: caret = %d, want 4", c.cursor)
	}
	c.clickAt(10, 5, 10, 10, 99) // far below → last row
	if c.cursor != 3 {
		t.Errorf("below-band click: caret = %d, want 3 (row start)", c.cursor)
	}
}

// TestComposerDrawScrollsToCaret pins the vertical window: with more
// rows than the band, drawing keeps the caret's row visible and the
// hardware cursor lands on it.
func TestComposerDrawScrollsToCaret(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(40, 12)

	c := newChatComposer("l1\nl2\nl3\nl4")
	c.cursor = len(c.value) // last row
	st := tcell.StyleDefault
	c.draw(scr, 0, 0, 10, 2, st, true)
	if c.scroll != 2 {
		t.Fatalf("scroll = %d, want 2 (caret row pinned in a 2-row band)", c.scroll)
	}
	scr.Show()
	cx, cy, _ := scr.GetCursor()
	if cx != 2 || cy != 1 {
		t.Errorf("cursor = (%d,%d), want (2,1)", cx, cy)
	}
	// The visible band shows the last two rows.
	cells, w, _ := scr.GetContents()
	if r := cells[0*w+0].Runes[0]; r != 'l' {
		t.Errorf("top band row starts with %q, want 'l'", r)
	}
	if r := cells[0*w+1].Runes[0]; r != '3' {
		t.Errorf("top band row = l%c, want l3", r)
	}
}
