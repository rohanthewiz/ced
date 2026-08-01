// =============================================================================
// File: internal/editor/wordhl_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// TestWordRange_FindsIdentifierAroundCol covers the three positions a
// caret can take relative to a word — inside it, at its start, and just
// past its end (where the caret lands after typing it).
func TestWordRange_FindsIdentifierAroundCol(t *testing.T) {
	runes := []rune("foo bar_baz qux")
	cases := []struct {
		col             int
		wantStart       int
		wantEnd         int
		wantWord        string
		shouldFindAtAll bool
	}{
		{col: 1, wantStart: 0, wantEnd: 3, wantWord: "foo", shouldFindAtAll: true},
		{col: 0, wantStart: 0, wantEnd: 3, wantWord: "foo", shouldFindAtAll: true},
		{col: 3, wantStart: 0, wantEnd: 3, wantWord: "foo", shouldFindAtAll: true}, // caret just past "foo"
		{col: 6, wantStart: 4, wantEnd: 11, wantWord: "bar_baz", shouldFindAtAll: true},
		{col: 12, wantStart: 12, wantEnd: 15, wantWord: "qux", shouldFindAtAll: true},
	}
	for _, c := range cases {
		start, end, ok := WordRange(runes, c.col)
		if ok != c.shouldFindAtAll {
			t.Fatalf("col %d: ok = %v, want %v", c.col, ok, c.shouldFindAtAll)
		}
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("col %d: range = [%d,%d), want [%d,%d)", c.col, start, end, c.wantStart, c.wantEnd)
		}
		if got := string(runes[start:end]); got != c.wantWord {
			t.Errorf("col %d: word = %q, want %q", c.col, got, c.wantWord)
		}
	}
}

// TestWordRange_RejectsNonWord pins the negative cases: whitespace,
// punctuation, and an empty line name no word at all.
func TestWordRange_RejectsNonWord(t *testing.T) {
	if _, _, ok := WordRange([]rune("a + b"), 2); ok {
		t.Error("a caret on '+' should name no word")
	}
	if _, _, ok := WordRange([]rune("  x"), 1); ok {
		t.Error("a caret in leading whitespace should name no word")
	}
	if _, _, ok := WordRange(nil, 0); ok {
		t.Error("an empty line should name no word")
	}
}

// TestMatchOccurrences_WholeWordVsSubstring pins the distinction the two
// caller paths rely on: whole-word matching must not claim a hit inside
// a longer identifier, substring matching must.
func TestMatchOccurrences_WholeWordVsSubstring(t *testing.T) {
	b := NewBuffer("err\nerrors\nif err != nil")

	whole := MatchOccurrences(b, "err", true, 0, b.LineCount()-1)
	if len(whole) != 2 {
		t.Fatalf("whole-word matches = %d, want 2 (lines 0 and 2)", len(whole))
	}
	if whole[1].Line != 2 {
		t.Errorf("second whole-word match on line %d, want 2", whole[1].Line)
	}

	sub := MatchOccurrences(b, "err", false, 0, b.LineCount()-1)
	if len(sub) != 3 {
		t.Fatalf("substring matches = %d, want 3 ('errors' counts)", len(sub))
	}
}

// TestMatchOccurrences_CaseSensitive pins the difference from find.go:
// Cursor and cursor are two identifiers, and highlighting one from the
// other would be a lie about the code.
func TestMatchOccurrences_CaseSensitive(t *testing.T) {
	b := NewBuffer("Cursor\ncursor")
	if got := MatchOccurrences(b, "cursor", true, 0, 1); len(got) != 1 {
		t.Fatalf("matches = %d, want 1 (case-sensitive)", len(got))
	}
}

// TestMatchOccurrences_ClampsWindow confirms a render window wider than
// the buffer is clamped rather than panicking — Render passes
// ScrollY+h-1, which routinely runs past the last line.
func TestMatchOccurrences_ClampsWindow(t *testing.T) {
	b := NewBuffer("x\nx")
	if got := MatchOccurrences(b, "x", true, 0, 99); len(got) != 2 {
		t.Fatalf("matches = %d, want 2", len(got))
	}
	if got := MatchOccurrences(b, "x", true, 1, 99); len(got) != 1 {
		t.Fatalf("windowed matches = %d, want 1", len(got))
	}
}

// wordHLTab builds a tab with the highlight enabled and the cursor
// placed inside the given line/col.
func wordHLTab(text string, line, col int) *Tab {
	t := &Tab{Buffer: NewBuffer(text), WordHighlight: true}
	t.Cursor = Position{Line: line, Col: col}
	t.Anchor = t.Cursor
	t.initUndo()
	return t
}

// TestWordHighlight_SpansEveryVisibleOccurrence is the feature's happy
// path: the word under the cursor tints wherever else it appears on
// screen, including under the caret itself.
func TestWordHighlight_SpansEveryVisibleOccurrence(t *testing.T) {
	tab := wordHLTab("total := 1\nsum := total\ntotal++", 0, 2)
	spans, marks := wordHighlightSource{}.Decorations(tab, theme.Default(), 0, 2)

	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	if len(marks) != 0 {
		t.Errorf("word highlight should not claim the gutter, got %d marks", len(marks))
	}
	for _, s := range spans {
		if !s.Delta.SetBG || s.Delta.BG != theme.Default().WordHL {
			t.Fatalf("span delta = %+v, want the WordHL background", s.Delta)
		}
		// Weight, not just a box: a background step alone is at the
		// mercy of the terminal's contrast, and the first cut of this
		// feature was invisible on an ordinary screen.
		if !s.Delta.Bold {
			t.Fatalf("span delta = %+v, want bold as well as the box", s.Delta)
		}
	}
}

// TestWordHighlight_QuietWhenNothingToSay pins the restraint rules — a
// lone occurrence, a caret in punctuation, and the disabled preference
// all produce nothing.
func TestWordHighlight_QuietWhenNothingToSay(t *testing.T) {
	lone := wordHLTab("alpha\nbeta", 0, 1)
	if spans, _ := (wordHighlightSource{}).Decorations(lone, theme.Default(), 0, 1); len(spans) != 0 {
		t.Errorf("a word appearing once should produce no spans, got %d", len(spans))
	}

	punct := wordHLTab("a + b\nc + d", 0, 2)
	if spans, _ := (wordHighlightSource{}).Decorations(punct, theme.Default(), 0, 1); len(spans) != 0 {
		t.Errorf("a caret on punctuation should produce no spans, got %d", len(spans))
	}

	off := wordHLTab("x := x", 0, 0)
	off.WordHighlight = false
	if spans, _ := (wordHighlightSource{}).Decorations(off, theme.Default(), 0, 0); len(spans) != 0 {
		t.Errorf("the disabled preference should produce no spans, got %d", len(spans))
	}
}

// TestWordHighlight_WindowScoped pins the cost rule from the file
// comment: only the requested rows are scanned, so a long file costs
// what's on screen.
func TestWordHighlight_WindowScoped(t *testing.T) {
	tab := wordHLTab("total\ntotal\ntotal", 0, 1)
	spans, _ := wordHighlightSource{}.Decorations(tab, theme.Default(), 0, 1)
	if len(spans) != 2 {
		t.Fatalf("spans in a 2-row window = %d, want 2", len(spans))
	}
}

// TestWordHighlight_SelectionNeedsLength pins the noise guard: dragging
// across a single bracket must not light up every bracket in view, while
// a real two-character selection still matches.
func TestWordHighlight_SelectionNeedsLength(t *testing.T) {
	tab := wordHLTab("f(x)\ng(y)", 0, 0)
	tab.Anchor = Position{Line: 0, Col: 1}
	tab.Cursor = Position{Line: 0, Col: 2} // just "("
	if spans, _ := (wordHighlightSource{}).Decorations(tab, theme.Default(), 0, 1); len(spans) != 0 {
		t.Errorf("a one-rune selection should produce no spans, got %d", len(spans))
	}

	tab2 := wordHLTab("ab-cd\nab-ef", 0, 0)
	tab2.Anchor = Position{Line: 0, Col: 0}
	tab2.Cursor = Position{Line: 0, Col: 2} // "ab"
	if spans, _ := (wordHighlightSource{}).Decorations(tab2, theme.Default(), 0, 1); len(spans) != 2 {
		t.Errorf("a two-rune selection should match, got %d spans", len(spans))
	}
}

// TestWordHighlight_SilentUnderMultiCaret pins the interaction with the
// other feature: carets the user placed deliberately already mark the
// occurrences, and a weaker wash under them only muddies which is which.
func TestWordHighlight_SilentUnderMultiCaret(t *testing.T) {
	tab := wordHLTab("x := x", 0, 0)
	tab.Carets = []Caret{{Cursor: Position{Line: 0, Col: 5}, Anchor: Position{Line: 0, Col: 5}}}
	if spans, _ := (wordHighlightSource{}).Decorations(tab, theme.Default(), 0, 0); len(spans) != 0 {
		t.Errorf("multi-caret mode should suppress the word highlight, got %d spans", len(spans))
	}
}

// TestWordHighlight_LosesToSelection closes the precedence loop end to
// end: where a real selection covers a highlighted word, the selection
// color is the one that reaches the screen.
func TestWordHighlight_LosesToSelection(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 5)

	th := theme.Default()
	tab := wordHLTab("total total", 0, 1)
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5} // selects the first "total"
	tab.Render(scr, th, 0, 0, 40, 5)
	scr.Show()

	cells, _, _ := scr.GetContents()
	contentX := gutterWidth + 1
	if _, bg, _ := cells[contentX].Style.Decompose(); bg != th.Selection {
		t.Fatalf("selected cell bg = %v, want the selection %v", bg, th.Selection)
	}
	// The second copy is highlighted but unselected — the wash shows.
	if _, bg, _ := cells[contentX+6].Style.Decompose(); bg != th.WordHL {
		t.Fatalf("unselected match bg = %v, want the word highlight %v", bg, th.WordHL)
	}
}
