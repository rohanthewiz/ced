// =============================================================================
// File: internal/editor/bracket_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// bracketTab builds a tab holding text with the caret at (line, col) and
// NO syntax grid, which is the SyntaxOff / naive-matching path. Tests
// that care about strings and comments call withGrid to add one.
func bracketTab(text string, line, col int) *Tab {
	return &Tab{Buffer: NewBuffer(text), Cursor: Position{Line: line, Col: col}}
}

// withGrid paints a real Chroma grid onto the tab so the string/comment
// classifier has something to read. Going through Highlight rather than
// hand-building styles is deliberate: the classifier's contract is with
// the grid the RENDERER produces, and a hand-built one could agree with
// the test while disagreeing with the editor.
func withGrid(tab *Tab, path string, th theme.Theme) *Tab {
	tab.Path = path
	tab.Styles = Highlight(path, tab.Buffer.String(), th)
	return tab
}

// TestMatchingBracket_AllThreePairsBothDirections is the core contract:
// each of the three pairs resolves from the opener forward and from the
// closer backward, and the two answers are each other's inverse.
func TestMatchingBracket_AllThreePairsBothDirections(t *testing.T) {
	th := theme.Default()
	//              0123456789
	const text = "a(b[c{d}e]f)g"
	cases := []struct {
		col     int
		want    int
		bracket rune
	}{
		{col: 1, want: 11, bracket: '('},
		{col: 3, want: 9, bracket: '['},
		{col: 5, want: 7, bracket: '{'},
		{col: 7, want: 5, bracket: '}'},
		{col: 9, want: 3, bracket: ']'},
		{col: 11, want: 1, bracket: ')'},
	}
	for _, c := range cases {
		tab := bracketTab(text, 0, c.col)
		bp, ok := tab.MatchingBracket(th)
		if !ok {
			t.Fatalf("col %d: no bracket found under the caret", c.col)
		}
		if bp.Rune != c.bracket {
			t.Errorf("col %d: bracket = %q, want %q", c.col, bp.Rune, c.bracket)
		}
		if !bp.Matched || !bp.Conclusive {
			t.Fatalf("col %d: matched=%v conclusive=%v, want both true", c.col, bp.Matched, bp.Conclusive)
		}
		if bp.Partner.Line != 0 || bp.Partner.Col != c.want {
			t.Errorf("col %d: partner = %v, want col %d", c.col, bp.Partner, c.want)
		}
	}
}

// TestMatchingBracket_RoundTrips pins the property the repeatable Esc-%
// key depends on: jumping to a partner and asking again comes back to
// where you started. Without it the key would walk off in one direction.
func TestMatchingBracket_RoundTrips(t *testing.T) {
	th := theme.Default()
	tab := bracketTab("func f() {\n\tif x {\n\t\ty()\n\t}\n}", 0, 9)

	bp, ok := tab.MatchingBracket(th)
	if !ok || !bp.Matched {
		t.Fatalf("opening brace did not match: %+v ok=%v", bp, ok)
	}
	if bp.Partner.Line != 4 {
		t.Fatalf("partner line = %d, want 4", bp.Partner.Line)
	}

	tab.Cursor = bp.Partner
	back, ok := tab.MatchingBracket(th)
	if !ok || !back.Matched {
		t.Fatalf("closing brace did not match: %+v ok=%v", back, ok)
	}
	if back.Partner != (Position{Line: 0, Col: 9}) {
		t.Errorf("round trip landed at %v, want {0 9}", back.Partner)
	}
}

// TestMatchingBracket_CaretJustPastBracket covers the WordRange
// courtesy: the caret sits AFTER a bracket the instant you type it, so
// the pair has to light up from there or the feature is silent at the
// only moment it is most wanted.
func TestMatchingBracket_CaretJustPastBracket(t *testing.T) {
	th := theme.Default()
	tab := bracketTab("foo(bar)", 0, 8) // one past the ')'
	bp, ok := tab.MatchingBracket(th)
	if !ok {
		t.Fatal("a caret immediately after ')' should name it")
	}
	if bp.At.Col != 7 || bp.Rune != ')' {
		t.Fatalf("named %q at col %d, want ')' at 7", bp.Rune, bp.At.Col)
	}
	if !bp.Matched || bp.Partner.Col != 3 {
		t.Errorf("partner = %v matched=%v, want col 3", bp.Partner, bp.Matched)
	}
}

// TestMatchingBracket_OnBeatsBehind pins the tie-break: with brackets on
// both sides of the caret the one it is ON wins, which is what makes a
// deliberate cursor move (and the jump verb's landing) predictable.
func TestMatchingBracket_OnBeatsBehind(t *testing.T) {
	th := theme.Default()
	tab := bracketTab("(a)(b)", 0, 3) // ')' behind, '(' under
	bp, ok := tab.MatchingBracket(th)
	if !ok {
		t.Fatal("expected a bracket")
	}
	if bp.Rune != '(' || bp.At.Col != 3 {
		t.Errorf("chose %q at col %d, want '(' at 3", bp.Rune, bp.At.Col)
	}
}

// TestMatchingBracket_NoBracketAtCaret keeps "there is nothing here"
// distinct from "this has no partner" — the app flashes different
// messages for the two, and collapsing them would be the confusing one.
func TestMatchingBracket_NoBracketAtCaret(t *testing.T) {
	th := theme.Default()
	if _, ok := bracketTab("hello world", 0, 4).MatchingBracket(th); ok {
		t.Error("a caret in a word should name no bracket")
	}
	if _, ok := bracketTab("", 0, 0).MatchingBracket(th); ok {
		t.Error("an empty buffer should name no bracket")
	}
	// Angle brackets are deliberately excluded: they are comparisons far
	// more often than pairs.
	if _, ok := bracketTab("a < b", 0, 2).MatchingBracket(th); ok {
		t.Error("'<' must not be treated as a bracket")
	}
}

// TestMatchingBracket_UnmatchedIsConclusive pins the honest negative: a
// scan that reached the edge of a small buffer reports Matched=false
// WITH Conclusive=true, which is the only state allowed to paint red.
func TestMatchingBracket_UnmatchedIsConclusive(t *testing.T) {
	th := theme.Default()
	for _, tc := range []struct {
		text string
		col  int
	}{
		{"func f( {", 7}, // unclosed '('
		{"a) b", 1},      // unopened ')'
	} {
		bp, ok := bracketTab(tc.text, 0, tc.col).MatchingBracket(th)
		if !ok {
			t.Fatalf("%q: expected a bracket at col %d", tc.text, tc.col)
		}
		if bp.Matched {
			t.Errorf("%q: reported a partner at %v", tc.text, bp.Partner)
		}
		if !bp.Conclusive {
			t.Errorf("%q: a scan over a 1-line buffer must be conclusive", tc.text)
		}
	}
}

// TestMatchingBracket_BudgetIsInconclusive is the other half of that
// pair: past bracketScanLines the answer is "we stopped looking", not
// "there is no partner". Reporting the latter would tint a balanced
// brace red purely for living in a big file.
func TestMatchingBracket_BudgetIsInconclusive(t *testing.T) {
	th := theme.Default()
	// An opener, then more filler than the budget allows, then its close.
	text := "{\n" + strings.Repeat("x\n", bracketScanLines+10) + "}"
	tab := bracketTab(text, 0, 0)

	bp, ok := tab.MatchingBracket(th)
	if !ok {
		t.Fatal("expected the '{' to be named")
	}
	if bp.Matched {
		t.Fatalf("the close is beyond the budget; got a partner at %v", bp.Partner)
	}
	if bp.Conclusive {
		t.Error("running out of budget must NOT be reported as a conclusive miss")
	}
}

// TestMatchingBracket_SkipsStringsAndComments is the reason the matcher
// reads the syntax grid at all: naive counting pairs the braces inside a
// format string and then reports every brace after it one level off.
func TestMatchingBracket_SkipsStringsAndComments(t *testing.T) {
	th := theme.Default()
	//                              111111111122222222
	//                    0123456789012345678901234567
	const call = "\tfmt.Printf(\"{%d} )\", n) // ) and {"
	src := "func f() {\n" + call + "\n}\n"
	tab := withGrid(bracketTab(src, 0, 9), "f.go", th)

	// The body's brace pairs with the '}' on the last line, not with the
	// '{' inside the format string.
	bp, ok := tab.MatchingBracket(th)
	if !ok || !bp.Matched {
		t.Fatalf("the body brace did not match: %+v ok=%v", bp, ok)
	}
	if bp.Partner != (Position{Line: 2, Col: 0}) {
		t.Errorf("partner = %v, want the '}' at {2 0}", bp.Partner)
	}

	// Printf's own '(' pairs with the call's closing ')' — the one inside
	// the literal, and the one in the trailing comment, are both skipped.
	runes := tab.Buffer.LineRunes(1)
	openCol := indexRune(runes, '(')
	wantCol := lastIndexRune(runes[:indexRune(runes, '/')], ')')
	tab.Cursor = Position{Line: 1, Col: openCol}
	bp2, ok := tab.MatchingBracket(th)
	if !ok || !bp2.Matched {
		t.Fatalf("Printf's '(' did not match: %+v ok=%v", bp2, ok)
	}
	if bp2.Partner != (Position{Line: 1, Col: wantCol}) {
		t.Errorf("partner = %v (%q), want the call's ')' at col %d",
			bp2.Partner, string(runes[bp2.Partner.Col]), wantCol)
	}
}

// indexRune / lastIndexRune are rune-indexed helpers so the expectations
// above can be derived from the fixture instead of hand-counted — a
// miscounted column would make this test pass for the wrong reason.
func indexRune(rs []rune, r rune) int {
	for i, c := range rs {
		if c == r {
			return i
		}
	}
	return -1
}

func lastIndexRune(rs []rune, r rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

// TestMatchingBracket_CaretOnStringBracketFallsThrough pins the negative
// side of the classifier: a bracket the grid calls string content is not
// a candidate at all, so the matcher never scans out of a literal.
func TestMatchingBracket_CaretOnStringBracketFallsThrough(t *testing.T) {
	th := theme.Default()
	src := "s := \"({[\"\n"
	tab := withGrid(bracketTab(src, 0, 6), "f.go", th) // on the '(' inside the literal
	if bp, ok := tab.MatchingBracket(th); ok {
		t.Errorf("a bracket inside a string literal should name nothing; got %+v", bp)
	}
}

// TestInStringOrComment_DegradesToCode covers the three ways the
// classifier deliberately answers "code": no grid, a short row, and a
// theme whose string color IS the plain text color (where it can no
// longer tell content from code and must not skip real brackets).
func TestInStringOrComment_DegradesToCode(t *testing.T) {
	th := theme.Default()

	off := bracketTab("\"(\"", 0, 1)
	off.SyntaxOff = true
	if off.inStringOrComment(th, 0, 1) {
		t.Error("SyntaxOff must degrade to naive matching")
	}

	short := bracketTab("(((", 0, 0)
	short.Styles = [][]tcell.Style{{}}
	if short.inStringOrComment(th, 0, 0) {
		t.Error("a row the grid doesn't cover must read as code")
	}

	// A theme that paints strings in the text color.
	flat := th
	flat.SynString = th.Text
	tab := withGrid(bracketTab("s := \"(\"\n", 0, 6), "f.go", flat)
	if tab.inStringOrComment(flat, 0, 6) {
		t.Error("a syn-string equal to the text color must not classify code as string")
	}
}

// TestBracketSource_PaintsBothCells checks the decoration: two one-cell
// spans carrying the match fill, and nothing at all once the pair scrolls
// out of the window.
func TestBracketSource_PaintsBothCells(t *testing.T) {
	th := theme.Default()
	tab := bracketTab("f(x)\ny\nz", 0, 1)

	spans, marks := (bracketSource{}).Decorations(tab, th, 0, 2)
	if len(marks) != 0 {
		t.Errorf("bracket matching claims no gutter marks, got %d", len(marks))
	}
	if len(spans) != 2 {
		t.Fatalf("span count = %d, want 2", len(spans))
	}
	for _, sp := range spans {
		if sp.End.Col != sp.Start.Col+1 {
			t.Errorf("span %v..%v is not one cell wide", sp.Start, sp.End)
		}
		if !sp.Delta.SetBG || sp.Delta.BG != th.BracketMatch || !sp.Delta.Bold {
			t.Errorf("matched span delta = %+v, want the bold BracketMatch fill", sp.Delta)
		}
	}

	if got, _ := (bracketSource{}).Decorations(tab, th, 2, 2); got != nil {
		t.Errorf("a pair outside the window should paint nothing, got %v", got)
	}
}

// TestBracketSource_UnmatchedAndInconclusive pins the two negative
// paints: a provably unmatched bracket gets the error FOREGROUND (one
// cell, so a fill would shout), and one we merely stopped looking for
// gets nothing.
func TestBracketSource_UnmatchedAndInconclusive(t *testing.T) {
	th := theme.Default()

	spans, _ := (bracketSource{}).Decorations(bracketTab("f( x", 0, 1), th, 0, 0)
	if len(spans) != 1 {
		t.Fatalf("unmatched span count = %d, want 1", len(spans))
	}
	if !spans[0].Delta.SetFG || spans[0].Delta.BG == th.BracketUnmatched && spans[0].Delta.SetBG {
		t.Errorf("unmatched delta = %+v, want a foreground tint, not a fill", spans[0].Delta)
	}
	if spans[0].Delta.FG != th.BracketUnmatched {
		t.Errorf("unmatched FG = %v, want BracketUnmatched", spans[0].Delta.FG)
	}

	over := "{\n" + strings.Repeat("x\n", bracketScanLines+10) + "}"
	if got, _ := (bracketSource{}).Decorations(bracketTab(over, 0, 0), th, 0, 0); got != nil {
		t.Errorf("an inconclusive scan must paint nothing, got %v", got)
	}
}

// TestBracketSource_QuietWithCarets keeps the source out of the way of
// multi-caret editing, where every interesting position is already
// marked by a caret the user placed on purpose.
func TestBracketSource_QuietWithCarets(t *testing.T) {
	th := theme.Default()
	tab := bracketTab("f(x)\ng(y)", 0, 1)
	tab.Carets = []Caret{{Cursor: Position{Line: 1, Col: 1}, Anchor: Position{Line: 1, Col: 1}}}
	if spans, _ := (bracketSource{}).Decorations(tab, th, 0, 1); spans != nil {
		t.Errorf("multi-caret mode should suppress the pair box, got %v", spans)
	}
}
