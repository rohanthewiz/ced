// =============================================================================
// File: internal/editor/replace_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import "testing"

// TestReplaceCurrent_ReplacesAndAdvances pins the walk: the pointed hit
// is swapped and the index lands on the NEXT one, which is what makes
// repeated presses march through the file.
func TestReplaceCurrent_ReplacesAndAdvances(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar foo")
	tab.SetFindQuery("foo")
	if !tab.ReplaceCurrent("baz") {
		t.Fatal("ReplaceCurrent reported no change")
	}
	if got := tab.Buffer.String(); got != "baz bar foo" {
		t.Fatalf("buffer = %q, want %q", got, "baz bar foo")
	}
	if len(tab.FindMatches) != 1 || tab.FindIndex != 0 {
		t.Fatalf("expected the remaining hit to be current, got idx=%d of %d",
			tab.FindIndex, len(tab.FindMatches))
	}
	if tab.FindMatches[0].Col != 8 {
		t.Fatalf("remaining match at col %d, want 8", tab.FindMatches[0].Col)
	}
}

// TestReplaceCurrent_IsOneUndoStep is the whole reason it goes through
// the selection+insert path: one press, one undo.
func TestReplaceCurrent_IsOneUndoStep(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar")
	tab.SetFindQuery("foo")
	tab.ReplaceCurrent("qux")
	if !tab.Undo() {
		t.Fatal("Undo reported nothing to roll back")
	}
	if got := tab.Buffer.String(); got != "foo bar" {
		t.Fatalf("after one undo buffer = %q, want the original", got)
	}
}

// TestReplaceCurrent_GrowingReplacementTerminates guards the s/a/aa/
// case: advancing past the text just written is what stops a
// replacement that contains its own query from matching forever.
func TestReplaceCurrent_GrowingReplacementTerminates(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("a b a")
	tab.SetFindQuery("a")
	tab.ReplaceCurrent("aa")
	if got := tab.Buffer.String(); got != "aa b a" {
		t.Fatalf("buffer = %q, want %q", got, "aa b a")
	}
	// The current match must be the one at the END of the line, not the
	// second half of what was just inserted.
	if tab.FindIndex < 0 || tab.FindMatches[tab.FindIndex].Col != 5 {
		t.Fatalf("current match = %+v, want the untouched hit at col 5",
			tab.FindMatches[tab.FindIndex])
	}
}

// TestReplaceAll_ReplacesEveryHit covers the bulk path across lines,
// including the count it reports back for the status flash.
func TestReplaceAll_ReplacesEveryHit(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nbar foo\nfoofoo")
	tab.SetFindQuery("foo")
	if n := tab.ReplaceAll("X"); n != 4 {
		t.Fatalf("ReplaceAll = %d, want 4", n)
	}
	if got := tab.Buffer.String(); got != "X\nbar X\nXX" {
		t.Fatalf("buffer = %q", got)
	}
	if len(tab.FindMatches) != 0 {
		t.Fatalf("query should have no hits left, got %d", len(tab.FindMatches))
	}
}

// TestReplaceAll_DifferentLengthsStayAligned is the bottom-up guard: a
// replacement wider than the query shifts every later column on the
// line, so a top-down pass would corrupt the tail. Three hits on one
// line with a longer replacement is the shortest case that catches it.
func TestReplaceAll_DifferentLengthsStayAligned(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("a-a-a")
	tab.SetFindQuery("a")
	tab.ReplaceAll("long")
	if got := tab.Buffer.String(); got != "long-long-long" {
		t.Fatalf("buffer = %q, want %q", got, "long-long-long")
	}
}

// TestReplaceAll_IsOneUndoStep pins the contract that makes the bulk
// verb safe to reach for: 200 replacements, one undo.
func TestReplaceAll_IsOneUndoStep(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("x x x x x")
	tab.SetFindQuery("x")
	tab.ReplaceAll("y")
	if !tab.Undo() {
		t.Fatal("Undo reported nothing to roll back")
	}
	if got := tab.Buffer.String(); got != "x x x x x" {
		t.Fatalf("after one undo buffer = %q, want the original", got)
	}
}

// TestReplaceAll_HonorsFindOptions proves the bulk path re-scans under
// the tab's modifiers rather than under the defaults — replacing more
// than the toggles promised is the worst failure this feature has.
func TestReplaceAll_HonorsFindOptions(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("Foo foo")
	tab.SetFindOptions(FindOptions{CaseSensitive: true})
	tab.SetFindQuery("foo")
	if n := tab.ReplaceAll("z"); n != 1 {
		t.Fatalf("ReplaceAll = %d, want 1 (case-sensitive)", n)
	}
	if got := tab.Buffer.String(); got != "Foo z" {
		t.Fatalf("buffer = %q, want %q", got, "Foo z")
	}
}

// TestReplaceAll_NoMatchesFilesNoSnapshot: an undo step that changes
// nothing is worse than no step at all — the next Undo would look like
// it did nothing.
func TestReplaceAll_NoMatchesFilesNoSnapshot(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello")
	tab.SetFindQuery("zzz")
	if n := tab.ReplaceAll("q"); n != 0 {
		t.Fatalf("ReplaceAll = %d, want 0", n)
	}
	if tab.CanUndo() {
		t.Fatal("a no-op replace-all filed an undo entry")
	}
}

// TestReplaceAll_MultilineReplacement pins that a replacement carrying a
// newline is handled structurally (the line count grows) rather than
// smearing the style grid against rows that no longer line up.
func TestReplaceAll_MultilineReplacement(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("a;b")
	tab.SetFindQuery(";")
	tab.ReplaceAll("\n")
	if got := tab.Buffer.LineCount(); got != 2 {
		t.Fatalf("line count = %d, want 2", got)
	}
	if tab.styleDefer {
		t.Fatal("a structural replace must not leave the style pass deferred")
	}
}

// TestReplaceCurrent_ImageTabIsSafe — image tabs have no buffer to edit,
// and every mutating path is expected to no-op rather than panic.
func TestReplaceCurrent_ImageTabIsSafe(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo")
	tab.SetFindQuery("foo")
	tab.Mode = imageMode
	if tab.ReplaceCurrent("bar") {
		t.Fatal("image tab reported a replacement")
	}
	if n := tab.ReplaceAll("bar"); n != 0 {
		t.Fatalf("image tab replaced %d hits", n)
	}
}

// TestReplaceAll_RegexExpandsCapturesPerHit: under regex each hit gets
// its OWN expansion of the template, in one undo step — and the
// bottom-up pass keeps the differing widths from corrupting later hits.
func TestReplaceAll_RegexExpandsCapturesPerHit(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("get_x get_long_name\nget_y")
	tab.SetFindOptions(FindOptions{Regex: true, CaseSensitive: true})
	tab.SetFindQuery(`get_(\w+)`)
	if n := tab.ReplaceAll("Get(${1})"); n != 3 {
		t.Fatalf("replaced %d, want 3", n)
	}
	want := "Get(x) Get(long_name)\nGet(y)"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "get_x get_long_name\nget_y" {
		t.Fatalf("one undo did not restore the buffer: %q", got)
	}
}

// TestReplaceCurrent_RegexExpandsTheCurrentHit: the single replace
// expands against the hit it overwrites, then advances as usual.
func TestReplaceCurrent_RegexExpandsTheCurrentHit(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("v1.2 v3.4")
	tab.SetFindOptions(FindOptions{Regex: true})
	tab.SetFindQuery(`v(\d)\.(\d)`)
	if !tab.ReplaceCurrent("$2.$1") {
		t.Fatal("ReplaceCurrent reported no change")
	}
	if got := tab.Buffer.String(); got != "2.1 v3.4" {
		t.Fatalf("buffer = %q", got)
	}
	if len(tab.FindMatches) != 1 || tab.FindIndex != 0 {
		t.Fatalf("expected to advance to the remaining hit, got %d of %d", tab.FindIndex, len(tab.FindMatches))
	}
}

// TestReplaceAll_LiteralKeepsDollarSigns: a "$1" typed into a PLAIN
// replace is text, not a capture reference.
func TestReplaceAll_LiteralKeepsDollarSigns(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("price")
	tab.SetFindQuery("price")
	tab.ReplaceAll("$1")
	if got := tab.Buffer.String(); got != "$1" {
		t.Fatalf("buffer = %q, want the literal $1", got)
	}
}
