// =============================================================================
// File: internal/diff/diff_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package diff

import (
	"reflect"
	"strings"
	"testing"
)

// script renders an edit list as a compact string so a test can state
// its expectation on one line: "=a -b +c".
func script(edits []Edit) string {
	var parts []string
	for _, e := range edits {
		sym := "="
		switch e.Op {
		case OpDelete:
			sym = "-"
		case OpInsert:
			sym = "+"
		}
		parts = append(parts, sym+e.Text)
	}
	return strings.Join(parts, " ")
}

// TestDiff_Identical produces an all-equal script and, more usefully,
// no unified output at all — "no differences" is a sentence the caller
// says better than an empty diff box does.
func TestDiff_Identical(t *testing.T) {
	a := []string{"one", "two", "three"}
	edits := Diff(a, a)
	if got := script(edits); got != "=one =two =three" {
		t.Fatalf("script = %q", got)
	}
	if u := Unified(edits, "a", "b", 3); u != nil {
		t.Fatalf("identical inputs rendered %d lines, want none", len(u))
	}
}

// TestDiff_PureInsertAndDelete covers the two degenerate sides, where
// one input is empty.
func TestDiff_PureInsertAndDelete(t *testing.T) {
	if got := script(Diff(nil, []string{"x", "y"})); got != "+x +y" {
		t.Errorf("insert-only script = %q", got)
	}
	if got := script(Diff([]string{"x", "y"}, nil)); got != "-x -y" {
		t.Errorf("delete-only script = %q", got)
	}
}

// TestDiff_LineNumbers pins the numbering both sides carry, since the
// @@ headers and the panel's jump-to-line are computed from it.
func TestDiff_LineNumbers(t *testing.T) {
	edits := Diff([]string{"a", "b", "c"}, []string{"a", "B", "c"})
	want := []Edit{
		{Op: OpEqual, Text: "a", OldLine: 1, NewLine: 1},
		{Op: OpDelete, Text: "b", OldLine: 2},
		{Op: OpInsert, Text: "B", NewLine: 2},
		{Op: OpEqual, Text: "c", OldLine: 3, NewLine: 3},
	}
	if !reflect.DeepEqual(edits, want) {
		t.Fatalf("edits:\n got=%+v\nwant=%+v", edits, want)
	}
}

// TestDiff_PatienceAnchorsOnUniqueLines is the reason this isn't a
// naive LCS: inserting a whole function must be reported as an
// insertion, not as "every closing brace moved". A plain LCS matches
// the first `}` of the new block against the old one and reports the
// change straddling the boundary; anchoring on the unique signature
// lines keeps the inserted block whole.
func TestDiff_PatienceAnchorsOnUniqueLines(t *testing.T) {
	a := []string{
		"func alpha() {",
		"\treturn 1",
		"}",
	}
	b := []string{
		"func alpha() {",
		"\treturn 1",
		"}",
		"",
		"func beta() {",
		"\treturn 2",
		"}",
	}
	edits := Diff(a, b)
	// Every original line survives as equal, and the four new lines are
	// contiguous inserts at the end.
	for i, e := range edits[:3] {
		if e.Op != OpEqual {
			t.Fatalf("edit %d = %v, want the original line kept", i, e)
		}
	}
	for i, e := range edits[3:] {
		if e.Op != OpInsert {
			t.Fatalf("edit %d = %v, want an insert", i+3, e)
		}
	}
}

// TestDiff_NoUniqueLinesFallsBack exercises the LCS fallback: a region
// where every line is repeated has no anchors at all, and the result
// must still be a minimal, correct script.
func TestDiff_NoUniqueLinesFallsBack(t *testing.T) {
	a := []string{"x", "x", "x"}
	b := []string{"x", "x"}
	if got := script(Diff(a, b)); got != "=x =x -x" {
		t.Fatalf("script = %q, want %q", got, "=x =x -x")
	}
}

// TestDiff_ReorderIsDeleteAndInsert pins the model: this is a line
// differ, not a move detector, so a swapped pair reads as one line
// removed and one added.
func TestDiff_ReorderIsDeleteAndInsert(t *testing.T) {
	edits := Diff([]string{"a", "b"}, []string{"b", "a"})
	added, removed := Stats(edits)
	if added != 1 || removed != 1 {
		t.Fatalf("stats = +%d -%d, want +1 -1", added, removed)
	}
}

// TestUnified_HeaderNumbers checks the @@ line against git's own
// conventions — 1-based starts, the count omitted when it is exactly 1.
// The app's diffTargetLine parses these, so being off by one here means
// every double-click jump lands on the wrong line.
func TestUnified_HeaderNumbers(t *testing.T) {
	a := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}
	b := []string{"1", "2", "3", "4", "five", "6", "7", "8", "9", "10"}
	got := Unified(Diff(a, b), "old", "new", 2)
	want := []string{
		"--- old",
		"+++ new",
		"@@ -3,5 +3,5 @@",
		" 3",
		" 4",
		"-5",
		"+five",
		" 6",
		" 7",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unified:\n got=%q\nwant=%q", got, want)
	}
}

// TestUnified_MergesNearbyHunks: two changes closer than 2*context
// belong in ONE hunk, or the diff repeats the lines between them under
// two headers.
func TestUnified_MergesNearbyHunks(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e"}
	b := []string{"A", "b", "c", "d", "E"}
	got := Unified(Diff(a, b), "old", "new", 3)
	headers := 0
	for _, l := range got {
		if strings.HasPrefix(l, "@@") {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("got %d hunks, want 1 merged:\n%s", headers, strings.Join(got, "\n"))
	}
}

// TestUnified_SplitsDistantHunks is the other half of the same rule.
func TestUnified_SplitsDistantHunks(t *testing.T) {
	a := make([]string, 40)
	for i := range a {
		a[i] = "line" + string(rune('a'+i%26)) + strings.Repeat("x", i)
	}
	b := append([]string(nil), a...)
	b[1] = "CHANGED-TOP"
	b[38] = "CHANGED-BOTTOM"
	got := Unified(Diff(a, b), "old", "new", 3)
	headers := 0
	for _, l := range got {
		if strings.HasPrefix(l, "@@") {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("got %d hunks, want 2:\n%s", headers, strings.Join(got, "\n"))
	}
}

// TestUnified_InsertionAtTopReportsZeroOldStart mirrors git's output for
// a hunk with no old-side lines at all.
func TestUnified_InsertionAtTopReportsZeroOldStart(t *testing.T) {
	got := Unified(Diff(nil, []string{"new line"}), "old", "new", 0)
	if len(got) < 3 || got[2] != "@@ -0,0 +1 @@" {
		t.Fatalf("header = %q, want %q", got, "@@ -0,0 +1 @@")
	}
}

// TestSplitLines_TrailingNewline is the regression that would otherwise
// report a phantom edit on every buffer-vs-file comparison: a file
// ending in "\n" has as many lines as it has newlines.
func TestSplitLines_TrailingNewline(t *testing.T) {
	if got := SplitLines("a\nb\n"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("SplitLines = %q", got)
	}
	if got := SplitLines(""); got != nil {
		t.Fatalf("empty text = %q, want nil", got)
	}
	if got := SplitLines("a\n\n"); !reflect.DeepEqual(got, []string{"a", ""}) {
		t.Fatalf("blank final line lost: %q", got)
	}
}

// TestDiff_LargeUnanchorableRegionDoesNotBlowUp pins the budget: a big
// region with no unique lines must degrade to a wholesale replace
// rather than allocating an n·m table. Without the cap this test is the
// one that hangs.
func TestDiff_LargeUnanchorableRegionDoesNotBlowUp(t *testing.T) {
	a := make([]string, 800)
	b := make([]string, 900)
	for i := range a {
		a[i] = "same"
	}
	for i := range b {
		b[i] = "other"
	}
	edits := Diff(a, b)
	added, removed := Stats(edits)
	if added != 900 || removed != 800 {
		t.Fatalf("stats = +%d -%d, want +900 -800", added, removed)
	}
}

// TestDiff_LongCommonSuffixIsLinear guards the shape of the suffix
// peel. It ran quadratically when the tail was collected by prepending;
// 20k identical trailing lines make that unmistakable in wall time.
func TestDiff_LongCommonSuffixIsLinear(t *testing.T) {
	n := 20000
	a := make([]string, n)
	for i := range a {
		a[i] = "line" + string(rune('a'+i%26))
	}
	b := append([]string{"a brand new first line"}, a...)
	edits := Diff(a, b)
	if added, removed := Stats(edits); added != 1 || removed != 0 {
		t.Fatalf("stats = +%d -%d, want +1 -0", added, removed)
	}
}
