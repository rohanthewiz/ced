// =============================================================================
// File: internal/editor/decoration_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// TestStyleDelta_Apply pins the partial-override contract: colors land
// only when flagged, attributes OR in, and an empty delta is a no-op —
// the property that lets deltas from unrelated sources compose.
func TestStyleDelta_Apply(t *testing.T) {
	base := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)

	if got := (StyleDelta{}).Apply(base); got != base {
		t.Fatal("empty delta must leave the style untouched")
	}

	d := StyleDelta{SetBG: true, BG: tcell.ColorRed, Underline: true}
	got := d.Apply(base)
	fg, bg, attr := got.Decompose()
	if bg != tcell.ColorRed {
		t.Fatalf("bg: got %v, want red", bg)
	}
	if fg != tcell.ColorWhite {
		t.Fatalf("fg must be preserved without SetFG, got %v", fg)
	}
	if attr&tcell.AttrUnderline == 0 {
		t.Fatal("underline attribute should be OR-ed in")
	}
}

// TestSpan_ColRange exercises the line-projection math for single-line
// and multi-line spans — the piece every rendered decoration cell
// depends on, and the classic home of off-by-ones.
func TestSpan_ColRange(t *testing.T) {
	// Multi-line span: (1,3) → (3,2), half-open.
	sp := Span{Start: Position{Line: 1, Col: 3}, End: Position{Line: 3, Col: 2}}

	if _, _, ok := sp.colRange(0, 10); ok {
		t.Fatal("line before the span must not be covered")
	}
	if s, e, ok := sp.colRange(1, 10); !ok || s != 3 || e != 10 {
		t.Fatalf("start line: got (%d,%d,%v), want (3,10,true)", s, e, ok)
	}
	if s, e, ok := sp.colRange(2, 7); !ok || s != 0 || e != 7 {
		t.Fatalf("interior line: got (%d,%d,%v), want (0,7,true)", s, e, ok)
	}
	if s, e, ok := sp.colRange(3, 10); !ok || s != 0 || e != 2 {
		t.Fatalf("end line: got (%d,%d,%v), want (0,2,true)", s, e, ok)
	}
	if _, _, ok := sp.colRange(4, 10); ok {
		t.Fatal("line after the span must not be covered")
	}
	// End col 0 on the end line means the span stops at the previous
	// line's newline — nothing to paint on the end line itself.
	sp2 := Span{Start: Position{Line: 0, Col: 0}, End: Position{Line: 1, Col: 0}}
	if _, _, ok := sp2.colRange(1, 10); ok {
		t.Fatal("end line with End.Col 0 must not be covered")
	}
}

// TestSelectionSource_EmitsOrderedSpan checks the migrated selection
// overlay: a backwards (cursor-before-anchor) selection still emits one
// document-ordered span with the theme's selection background.
func TestSelectionSource_EmitsOrderedSpan(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("alpha\nbravo\ncharlie")
	tab.Anchor = Position{Line: 2, Col: 3}
	tab.Cursor = Position{Line: 0, Col: 1} // selected backwards
	th := theme.Default()

	spans, marks := selectionSource{}.Decorations(tab, th, 0, 10)
	if len(marks) != 0 {
		t.Fatal("selection emits no gutter marks")
	}
	if len(spans) != 1 {
		t.Fatalf("span count: got %d, want 1", len(spans))
	}
	sp := spans[0]
	if sp.Start != (Position{Line: 0, Col: 1}) || sp.End != (Position{Line: 2, Col: 3}) {
		t.Fatalf("span range: got %v→%v, want (0,1)→(2,3)", sp.Start, sp.End)
	}
	if !sp.Delta.SetBG || sp.Delta.BG != th.Selection {
		t.Fatalf("span delta should set the selection bg, got %+v", sp.Delta)
	}
}

// TestSelectionSource_CullsAndSkips pins the two nothing-to-do paths:
// no selection at all, and a selection entirely outside the viewport.
func TestSelectionSource_CullsAndSkips(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("a\nb\nc\nd\ne")
	th := theme.Default()

	if spans, _ := (selectionSource{}).Decorations(tab, th, 0, 10); spans != nil {
		t.Fatal("no selection should emit no spans")
	}
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 1}
	if spans, _ := (selectionSource{}).Decorations(tab, th, 3, 4); spans != nil {
		t.Fatal("selection outside the window should be culled")
	}
}

// TestFindSource_SpansAndCurrentMatch verifies the migrated find
// overlay: every visible match becomes a span, gaps stay uncovered,
// and the current match alone gets the FindCurrent treatment. This
// carries the coverage the old per-cell matchAtRune test provided.
func TestFindSource_SpansAndCurrentMatch(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar foo")
	tab.SetFindQuery("foo") // matches at (0,0) and (0,8); index 0 is current
	th := theme.Default()

	spans, _ := findSource{}.Decorations(tab, th, 0, 10)
	if len(spans) != 2 {
		t.Fatalf("span count: got %d, want 2", len(spans))
	}
	cur, other := spans[0], spans[1]
	if cur.Start.Col != 0 || cur.End.Col != 3 {
		t.Fatalf("current span cols: got %d..%d, want 0..3 (gap at 4 stays bare)", cur.Start.Col, cur.End.Col)
	}
	if cur.Delta.BG != th.FindCurrent || !cur.Delta.SetFG {
		t.Fatalf("current match should get FindCurrent bg + inverted fg, got %+v", cur.Delta)
	}
	if other.Delta.BG != th.FindMatch || other.Delta.SetFG {
		t.Fatalf("non-current match should get the soft FindMatch bg, got %+v", other.Delta)
	}
}

// TestFindSource_CullsOffscreenMatches pins the viewport cull: matches
// outside [firstLine, lastLine] must not produce spans, so huge match
// lists cost nothing beyond the visible rows.
func TestFindSource_CullsOffscreenMatches(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hit\nmiss\nhit\nmiss\nhit")
	tab.SetFindQuery("hit") // lines 0, 2, 4

	spans, _ := findSource{}.Decorations(tab, theme.Default(), 1, 3)
	if len(spans) != 1 || spans[0].Start.Line != 2 {
		t.Fatalf("expected only the line-2 match, got %+v", spans)
	}
}

// stubSource is a canned DecorationSource for plumbing tests.
type stubSource struct {
	spans []Span
	marks []GutterMark
}

// Decorations returns the canned payload regardless of the window.
func (s stubSource) Decorations(*Tab, theme.Theme, int, int) ([]Span, []GutterMark) {
	return s.spans, s.marks
}

// TestCollectDecorations_PrecedenceOrder pins the merge order the whole
// design leans on: external sources come first, then selection, then
// find — so interaction feedback always ends up painted last (on top).
func TestCollectDecorations_PrecedenceOrder(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar")
	tab.DecoSources = []DecorationSource{stubSource{
		spans: []Span{{Start: Position{0, 0}, End: Position{0, 7}, Delta: StyleDelta{Underline: true}}},
	}}
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 7}
	tab.SetFindQuery("bar")

	spans, _ := tab.collectDecorations(theme.Default(), 0, 10)
	if len(spans) != 3 {
		t.Fatalf("span count: got %d, want 3 (stub + selection + find)", len(spans))
	}
	if !spans[0].Delta.Underline {
		t.Fatal("external source span must come first")
	}
	if spans[1].Delta.BG != theme.Default().Selection {
		t.Fatal("selection span must come second")
	}
	if spans[2].Delta.BG != theme.Default().FindCurrent {
		t.Fatal("find span must come last (highest precedence)")
	}
}

// TestRender_FindBeatsSelection renders overlapping selection and find
// decorations and asserts the find background wins on the shared cell —
// the same behavior the pre-decoration inline branches produced.
func TestRender_FindBeatsSelection(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 5)

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar")
	tab.SelectAll()
	tab.SetFindQuery("bar")
	th := theme.Default()
	tab.Render(scr, th, 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	contentX := gutterWidth + 1
	// Cell under 'f' (col 0): selected, not a find hit → selection bg.
	_, bg, _ := cells[contentX].Style.Decompose()
	if bg != th.Selection {
		t.Fatalf("plain selected cell bg: got %v, want selection %v", bg, th.Selection)
	}
	// Cell under 'b' (col 4): selected AND current find match → find wins.
	_, bg, _ = cells[contentX+4].Style.Decompose()
	if bg != th.FindCurrent {
		t.Fatalf("find-hit cell bg: got %v, want FindCurrent %v", bg, th.FindCurrent)
	}
	_ = w
}

// TestRender_DrawsGutterMark proves the mark column end to end: a
// registered source's mark lands in the cell between the line numbers
// and the code, on the right row, with its own foreground.
func TestRender_DrawsGutterMark(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 5)

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("one\ntwo\nthree")
	markFG := tcell.ColorGreen
	tab.DecoSources = []DecorationSource{stubSource{
		marks: []GutterMark{{Line: 1, Glyph: '▎', FG: markFG}},
	}}
	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	cell := cells[1*w+gutterWidth]
	if len(cell.Runes) == 0 || cell.Runes[0] != '▎' {
		t.Fatalf("mark cell: got %v, want '▎'", cell.Runes)
	}
	fg, _, _ := cell.Style.Decompose()
	if fg != markFG {
		t.Fatalf("mark fg: got %v, want %v", fg, markFG)
	}
	// Unmarked line 0 keeps a blank mark column.
	blank := cells[0*w+gutterWidth]
	if len(blank.Runes) > 0 && blank.Runes[0] != ' ' {
		t.Fatalf("unmarked mark column should stay blank, got %q", blank.Runes[0])
	}
}

// stubAnnotator is a canned AnnotationSource — the third primitive,
// which unlike spans and marks makes ROOM for itself.
type stubAnnotator struct {
	stubSource
	width int
	anns  map[int]LineAnnotation
}

// Annotations returns the canned column regardless of the window.
func (s stubAnnotator) Annotations(*Tab, theme.Theme, int, int) (int, map[int]LineAnnotation) {
	return s.width, s.anns
}

// TestRender_AnnotationColumnMovesTheCodeAndTheGeometryTogether is the
// contract the whole primitive rests on. The column takes cells, so the
// code starts further right — and PosScreenCell, HitTest and
// EnsureVisible have to agree about that, or every click lands a few
// characters from where the user aimed and the caret drifts out of
// view. The line NUMBERS deliberately do not move: they anchor the
// left edge, and a column that appears should not slide them.
func TestRender_AnnotationColumnMovesTheCodeAndTheGeometryTogether(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(60, 5)

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("one\ntwo\nthree")
	const annW = 8
	tab.DecoSources = []DecorationSource{stubAnnotator{
		width: annW,
		anns:  map[int]LineAnnotation{1: {Text: "a3f2c1 x", FG: tcell.ColorGreen}},
	}}

	// Before the first render nothing has been measured, so the layout is
	// exactly the pre-annotation one.
	if s, e := tab.AnnotationCols(); s != e {
		t.Fatalf("unrendered tab already claims a column [%d,%d)", s, e)
	}

	tab.Render(scr, theme.Default(), 0, 0, 60, 5)
	scr.Show()

	start, end := tab.AnnotationCols()
	if end-start != annW {
		t.Fatalf("annotation column = [%d,%d), want %d cells", start, end, annW)
	}
	cells, w, _ := scr.GetContents()
	// The annotation is painted at the column's start, on its own row.
	if got := cells[1*w+start].Runes; len(got) == 0 || got[0] != 'a' {
		t.Fatalf("annotation cell = %v, want the text's first rune", got)
	}
	if fg, _, _ := cells[1*w+start].Style.Decompose(); fg != tcell.ColorGreen {
		t.Fatalf("annotation fg = %v, want the source's color", fg)
	}
	// An unannotated line leaves the column blank rather than borrowing
	// the row above.
	if got := cells[0*w+start].Runes; len(got) > 0 && got[0] != ' ' {
		t.Fatalf("unannotated row should be blank, got %q", got[0])
	}
	// The line numbers stayed where they were…
	if got := cells[0*w+gutterWidth-2].Runes; len(got) == 0 || got[0] != '1' {
		t.Fatalf("line number moved: %v", cells[0*w+gutterWidth-2].Runes)
	}
	// …and the code moved right by exactly the column plus its mark cell.
	if got := cells[0*w+end+1].Runes; len(got) == 0 || got[0] != 'o' {
		t.Fatalf("code should start at %d, got %v", end+1, got)
	}

	// The three geometry helpers now answer about that same layout.
	dx, dy, ok := tab.PosScreenCell(Position{Line: 0, Col: 0}, 60, 5)
	if !ok || dx != end+1 || dy != 0 {
		t.Fatalf("PosScreenCell = (%d,%d,%v), want (%d,0,true)", dx, dy, ok, end+1)
	}
	if pos, ok := tab.HitTest(end+1, 0, 60, 5); !ok || pos.Col != 0 {
		t.Fatalf("HitTest on the first code cell = %+v (ok=%v), want col 0", pos, ok)
	}
	// A click IN the column still resolves to that line at column 0 —
	// the pre-existing gutter behaviour, which a caller who wants to
	// give the column its own verb overrides before asking.
	if pos, ok := tab.HitTest(start+1, 2, 60, 5); !ok || pos.Line != 2 || pos.Col != 0 {
		t.Fatalf("HitTest inside the column = %+v (ok=%v)", pos, ok)
	}
}

// TestRender_AnnotationColumnYieldsOnANarrowWindow pins the refusal: a
// column that would squeeze the code below annMinContent is dropped
// whole rather than shrunk, because a blame stamp beside four readable
// characters of code is not a trade anyone wants.
func TestRender_AnnotationColumnYieldsOnANarrowWindow(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(30, 3)

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("package main")
	tab.DecoSources = []DecorationSource{stubAnnotator{
		width: 20, // 30 - 6 - 1 - 20 = 3 cells of code: not a layout.
		anns:  map[int]LineAnnotation{0: {Text: "a3f2c1 rohan 3d"}},
	}}

	tab.Render(scr, theme.Default(), 0, 0, 30, 3)
	scr.Show()

	if s, e := tab.AnnotationCols(); s != e {
		t.Fatalf("column survived a narrow window: [%d,%d)", s, e)
	}
	cells, w, _ := scr.GetContents()
	if got := cells[0*w+gutterWidth+1].Runes; len(got) == 0 || got[0] != 'p' {
		t.Fatalf("code should start where it always did, got %v", got)
	}
}

// TestCollectAnnotations_FirstColumnWins: the column is a place, not a
// stack. Two sources splitting it would each render into a fraction of
// the width they measured themselves against.
func TestCollectAnnotations_FirstColumnWins(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("one")
	tab.DecoSources = []DecorationSource{
		stubSource{}, // no column at all — skipped, not "a zero-width one"
		stubAnnotator{width: 7, anns: map[int]LineAnnotation{0: {Text: "first"}}},
		stubAnnotator{width: 40, anns: map[int]LineAnnotation{0: {Text: "second"}}},
	}

	w, anns := tab.collectAnnotations(theme.Default(), 0, 0)

	if w != 7 || anns[0].Text != "first" {
		t.Fatalf("collectAnnotations = (%d, %q), want the first source's column", w, anns[0].Text)
	}
}

// TestRender_ExternalSpanUnderlines closes the loop on the phase-4/5
// seam: a span from an external source (an LSP-style underline) reaches
// the drawn cells, and coexists with syntax styling.
func TestRender_ExternalSpanUnderlines(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 5)

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("abc def")
	tab.DecoSources = []DecorationSource{stubSource{
		spans: []Span{{
			Start: Position{Line: 0, Col: 4},
			End:   Position{Line: 0, Col: 7},
			Delta: StyleDelta{Underline: true},
		}},
	}}
	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, _, _ := scr.GetContents()
	contentX := gutterWidth + 1
	_, _, attr := cells[contentX+4].Style.Decompose()
	if attr&tcell.AttrUnderline == 0 {
		t.Fatal("external span's underline should reach the drawn cell")
	}
	_, _, attr = cells[contentX].Style.Decompose()
	if attr&tcell.AttrUnderline != 0 {
		t.Fatal("cells outside the span must stay un-underlined")
	}
}
