// =============================================================================
// File: internal/editor/multiedit_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"testing"
)

// tabWith builds a detached Tab over the given text, with the undo history
// seeded the way NewTab leaves it.
func tabWith(text string) *Tab {
	t := &Tab{Buffer: NewBuffer(text)}
	t.initUndo()
	return t
}

// TestApplyEdits_BottomUpPreservesLaterColumns is the test a top-down
// implementation fails. Two edits on one line whose replacements differ in
// width from what they replace: applied top-down, the first one shifts every
// column after it and the second lands in the wrong place.
func TestApplyEdits_BottomUpPreservesLaterColumns(t *testing.T) {
	buf := NewBuffer("alpha beta gamma")
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 5}, NewText: "X"},           // alpha -> X (shorter)
		{Start: Position{0, 11}, End: Position{0, 16}, NewText: "LONGERONE"}, // gamma -> LONGERONE
	}
	NormalizeEdits(edits)
	if n := ApplyEdits(buf, edits); n != 2 {
		t.Fatalf("applied %d edits, want 2", n)
	}
	if got, want := buf.Lines[0], "X beta LONGERONE"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// TestApplyEdits_MultiLineRange pins that a range spanning several lines
// collapses them, which is what a server sends when it rewrites a block.
func TestApplyEdits_MultiLineRange(t *testing.T) {
	buf := NewBuffer("one\ntwo\nthree\nfour")
	edits := []Edit{{Start: Position{1, 1}, End: Position{2, 2}, NewText: "X"}}
	ApplyEdits(buf, edits)
	if buf.LineCount() != 3 {
		t.Fatalf("line count = %d, want 3", buf.LineCount())
	}
	if got, want := buf.Lines[1], "tXree"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// TestApplyEdits_WholeFileReplaceViaClampEnd covers the range a server sends
// for "rewrite this document": an end one line PAST the last. Clamp would
// resolve that to the last line and spare its text; ClampEnd must not.
func TestApplyEdits_WholeFileReplaceViaClampEnd(t *testing.T) {
	buf := NewBuffer("aaa\nbbb\nccc")
	edits := []Edit{{
		Start:   Position{0, 0},
		End:     Position{3, 0}, // LineCount == 3, so one past the end
		NewText: "replaced",
	}}
	ApplyEdits(buf, edits)
	if got, want := buf.String(), "replaced"; got != want {
		t.Errorf("buffer = %q, want %q — the last line survived a whole-file replace", got, want)
	}
}

// TestApplyEdits_EqualPositionInsertsKeepArrayOrder pins the stable-sort
// contract: two zero-width inserts at one position are legal, and the
// caller's array order decides which text lands first.
func TestApplyEdits_EqualPositionInsertsKeepArrayOrder(t *testing.T) {
	buf := NewBuffer("xy")
	edits := []Edit{
		{Start: Position{0, 1}, End: Position{0, 1}, NewText: "A"},
		{Start: Position{0, 1}, End: Position{0, 1}, NewText: "B"},
	}
	NormalizeEdits(edits)
	ApplyEdits(buf, edits)
	if got, want := buf.Lines[0], "xABy"; got != want {
		t.Errorf("line = %q, want %q — array order among equal positions was lost", got, want)
	}
}

// TestOverlapIndex_FindsOverlapAndIgnoresTouching separates the legal case
// from the illegal one. Adjacent edits (one's end == the next's start) are an
// ordinary way to rewrite "a.b"; genuinely overlapping ones mean the server
// disagrees with itself about the text, and must be refused.
func TestOverlapIndex_FindsOverlapAndIgnoresTouching(t *testing.T) {
	touching := []Edit{
		{Start: Position{0, 0}, End: Position{0, 3}, NewText: "x"},
		{Start: Position{0, 3}, End: Position{0, 6}, NewText: "y"},
	}
	if got := OverlapIndex(touching); got != -1 {
		t.Errorf("OverlapIndex(touching) = %d, want -1", got)
	}
	overlapping := []Edit{
		{Start: Position{0, 0}, End: Position{0, 4}, NewText: "x"},
		{Start: Position{0, 3}, End: Position{0, 6}, NewText: "y"},
	}
	if got := OverlapIndex(overlapping); got != 1 {
		t.Errorf("OverlapIndex(overlapping) = %d, want 1", got)
	}
}

// TestNormalizeEdits_OrdersReversedRanges pins that a server sending a range
// end-first is corrected rather than left to make OverlapIndex meaningless.
func TestNormalizeEdits_OrdersReversedRanges(t *testing.T) {
	edits := []Edit{{Start: Position{2, 5}, End: Position{1, 0}, NewText: "x"}}
	NormalizeEdits(edits)
	if edits[0].Start != (Position{1, 0}) || edits[0].End != (Position{2, 5}) {
		t.Errorf("range = %v..%v, want {1 0}..{2 5}", edits[0].Start, edits[0].End)
	}
}

// TestApplyMultiEdit_IsOneUndoStep is the whole point of the primitive:
// however many ranges a set carries, one press puts the buffer back.
func TestApplyMultiEdit_IsOneUndoStep(t *testing.T) {
	tab := tabWith("aaa\nbbb\nccc")
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 3}, NewText: "111"},
		{Start: Position{1, 0}, End: Position{1, 3}, NewText: "222"},
		{Start: Position{2, 0}, End: Position{2, 3}, NewText: "333"},
	}
	if _, ok := tab.ApplyMultiEdit(edits); !ok {
		t.Fatal("ApplyMultiEdit refused a valid set")
	}
	if got, want := tab.Buffer.String(), "111\n222\n333"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if !tab.Undo() {
		t.Fatal("Undo reported nothing to undo")
	}
	if got, want := tab.Buffer.String(), "aaa\nbbb\nccc"; got != want {
		t.Errorf("after undo buffer = %q, want %q", got, want)
	}
	if tab.CanUndo() {
		t.Error("three edits left more than one undo step on the stack")
	}
}

// TestApplyMultiEdit_DropsCaretsAndBumpsEditRev pins the bookkeeping the LSP
// and Copilot sync layers read. Secondary carets were measured against the
// buffer this just rewrote, so keeping them would leave the next keystroke
// editing text that moved.
func TestApplyMultiEdit_DropsCaretsAndBumpsEditRev(t *testing.T) {
	tab := tabWith("aaa\nbbb")
	tab.Carets = []Caret{{Cursor: Position{1, 0}, Anchor: Position{1, 0}}}
	before := tab.EditRev
	if _, ok := tab.ApplyMultiEdit([]Edit{
		{Start: Position{0, 0}, End: Position{0, 3}, NewText: "z"},
	}); !ok {
		t.Fatal("ApplyMultiEdit refused a valid set")
	}
	if tab.Carets != nil {
		t.Error("secondary carets survived a multi-edit")
	}
	if tab.EditRev != before+1 {
		t.Errorf("EditRev = %d, want %d", tab.EditRev, before+1)
	}
	if !tab.Dirty {
		t.Error("tab not marked dirty")
	}
	if !tab.StyleStale {
		t.Error("styles not invalidated — a structural edit must re-lex")
	}
}

// TestApplyMultiEdit_RefusesOverlapWithoutTouchingTheBuffer pins the refusal
// contract: an overlapping set leaves the buffer and the undo stack exactly
// as they were, so the caller can report rather than clean up.
func TestApplyMultiEdit_RefusesOverlapWithoutTouchingTheBuffer(t *testing.T) {
	tab := tabWith("abcdef")
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 4}, NewText: "x"},
		{Start: Position{0, 2}, End: Position{0, 6}, NewText: "y"},
	}
	if _, ok := tab.ApplyMultiEdit(edits); ok {
		t.Fatal("ApplyMultiEdit accepted an overlapping set")
	}
	if got, want := tab.Buffer.String(), "abcdef"; got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
	if tab.CanUndo() {
		t.Error("a refused edit filed an undo snapshot")
	}
	if tab.Dirty {
		t.Error("a refused edit marked the tab dirty")
	}
}

// TestEditResults_TracksShiftedPositions pins the receipt data. The second
// edit's reported column has to account for the first one changing width, or
// the results panel lights up the wrong cells.
func TestEditResults_TracksShiftedPositions(t *testing.T) {
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 5}, NewText: "X"},           // 5 runes -> 1
		{Start: Position{0, 11}, End: Position{0, 16}, NewText: "LONGERONE"}, // 5 -> 9
	}
	NormalizeEdits(edits)
	res := EditResults(edits)
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	if res[0].Start != (Position{0, 0}) || res[0].End != (Position{0, 1}) {
		t.Errorf("result[0] = %v..%v, want {0 0}..{0 1}", res[0].Start, res[0].End)
	}
	// "alpha beta gamma" -> "X beta LONGERONE": the second edit now starts at
	// column 7, four left of its original 11.
	if res[1].Start != (Position{0, 7}) || res[1].End != (Position{0, 16}) {
		t.Errorf("result[1] = %v..%v, want {0 7}..{0 16}", res[1].Start, res[1].End)
	}
}

// TestEditResults_TracksLineShift pins the other accumulator: an edit that
// adds lines moves every later edit's row.
func TestEditResults_TracksLineShift(t *testing.T) {
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 1}, NewText: "a\nb\nc"}, // +2 lines
		{Start: Position{2, 0}, End: Position{2, 1}, NewText: "z"},
	}
	NormalizeEdits(edits)
	res := EditResults(edits)
	if res[1].Start.Line != 4 {
		t.Errorf("result[1] line = %d, want 4 (2 original + 2 added)", res[1].Start.Line)
	}
}

// TestEditResults_MatchesTheAppliedBuffer is the cross-check that keeps the
// analytic shift calculation honest: whatever EditResults claims, the text at
// that range in the real applied buffer must be the replacement.
func TestEditResults_MatchesTheAppliedBuffer(t *testing.T) {
	buf := NewBuffer("alpha beta gamma\ndelta epsilon")
	edits := []Edit{
		{Start: Position{0, 0}, End: Position{0, 5}, NewText: "XX"},
		{Start: Position{0, 11}, End: Position{0, 16}, NewText: "LONGERONE"},
		{Start: Position{1, 6}, End: Position{1, 13}, NewText: "E"},
	}
	NormalizeEdits(edits)
	res := EditResults(edits)
	ApplyEdits(buf, edits)

	for i, want := range []string{"XX", "LONGERONE", "E"} {
		got := buf.Substring(res[i].Start, res[i].End)
		if got != want {
			t.Errorf("result[%d] spans %q in the applied buffer, want %q", i, got, want)
		}
	}
}

// TestApplyMultiEdit_RejectsEmptyAndImage pins the guards that stop the
// primitive filing an undo step for a no-op.
func TestApplyMultiEdit_RejectsEmptyAndImage(t *testing.T) {
	tab := tabWith("abc")
	if _, ok := tab.ApplyMultiEdit(nil); ok {
		t.Error("an empty edit set was accepted")
	}
	if tab.CanUndo() {
		t.Error("an empty edit set filed an undo snapshot")
	}
}
