// =============================================================================
// File: internal/editor/find_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"reflect"
	"strings"
	"testing"
)

// TestFindAll_BasicMatches walks across multiple lines and pins down the
// document-order ordering plus the rune-indexed Col / Width fields.
func TestFindAll_BasicMatches(t *testing.T) {
	buf := NewBuffer("foo bar foo\nbaz foo\n")
	got := FindAll(buf, "foo")
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 8, Width: 3},
		{Line: 1, Col: 4, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindAll mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindAll_CaseInsensitive proves matching ignores letter case in
// both the query and the buffer. Without this, the "type to find" UX
// is much less forgiving than users expect from VS Code.
func TestFindAll_CaseInsensitive(t *testing.T) {
	buf := NewBuffer("Foo FOO foO")
	got := FindAll(buf, "fOo")
	if len(got) != 3 {
		t.Fatalf("expected 3 case-insensitive matches, got %d: %v", len(got), got)
	}
}

// TestFindAll_EmptyQuery returns nil so the UI can render an empty
// state without a special "0 of 0" branch.
func TestFindAll_EmptyQuery(t *testing.T) {
	buf := NewBuffer("anything")
	if got := FindAll(buf, ""); got != nil {
		t.Fatalf("empty query should return nil, got %v", got)
	}
}

// TestFindAll_NonOverlapping pins down the scanner's advance-past-match
// behaviour. "aaa" in "aaaaaa" should yield two non-overlapping hits,
// matching VS Code's default search semantics.
func TestFindAll_NonOverlapping(t *testing.T) {
	buf := NewBuffer("aaaaaa")
	got := FindAll(buf, "aaa")
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 3, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected non-overlapping matches, got %v", got)
	}
}

// TestFindAll_MultiByteRunes pins down the rune-indexed column
// convention. The buffer contains a 3-byte UTF-8 character before the
// match — Col must report 1 (one rune in), not 3 (three bytes in).
func TestFindAll_MultiByteRunes(t *testing.T) {
	buf := NewBuffer("✓foo")
	got := FindAll(buf, "foo")
	want := []Match{{Line: 0, Col: 1, Width: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-byte handling wrong, got %v", got)
	}
}

// TestFindAll_NilBuffer is the defensive guard — callers may hold a
// freshly-zeroed Tab during construction. Returning nil rather than
// panicking lets the UI cope without an explicit nil check.
func TestFindAll_NilBuffer(t *testing.T) {
	if got := FindAll(nil, "x"); got != nil {
		t.Fatalf("nil buffer should return nil, got %v", got)
	}
}

// TestFirstMatchAtOrAfter_BasicForward finds the first match at or
// after the cursor, which is what we want when a user types a query
// in the bar — we shouldn't snap them backwards past where they were
// already looking.
func TestFirstMatchAtOrAfter_BasicForward(t *testing.T) {
	matches := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 1, Col: 4, Width: 3},
		{Line: 2, Col: 0, Width: 3},
	}
	idx := FirstMatchAtOrAfter(matches, Position{Line: 1, Col: 0})
	if idx != 1 {
		t.Fatalf("expected idx=1 (line 1 match), got %d", idx)
	}
}

// TestFirstMatchAtOrAfter_WrapsToTop covers the case where the cursor
// is past every match: we wrap to the top so the user can keep
// pressing Enter to cycle.
func TestFirstMatchAtOrAfter_WrapsToTop(t *testing.T) {
	matches := []Match{{Line: 0, Col: 0, Width: 3}}
	idx := FirstMatchAtOrAfter(matches, Position{Line: 99, Col: 0})
	if idx != 0 {
		t.Fatalf("expected wrap to idx=0, got %d", idx)
	}
}

// TestFirstMatchAtOrAfter_Empty is the no-matches case — return -1 so
// the caller can short-circuit without checking length again.
func TestFirstMatchAtOrAfter_Empty(t *testing.T) {
	if got := FirstMatchAtOrAfter(nil, Position{}); got != -1 {
		t.Fatalf("expected -1 for empty matches, got %d", got)
	}
}

// TestTab_SetFindQuery_PicksNearestMatch installs a query and pins the
// "land on the nearest hit, not always the first hit" contract: with the
// cursor on line 1, the index should point at the line-1 match, not the
// earlier line-0 one.
func TestTab_SetFindQuery_PicksNearestMatch(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.Cursor = Position{Line: 1, Col: 0}

	tab.SetFindQuery("foo")
	if got, want := tab.FindIndex, 1; got != want {
		t.Fatalf("FindIndex = %d, want %d (nearest to cursor)", got, want)
	}
}

// TestTab_SetFindQuery_EmptyClears proves an empty query clears every
// piece of find state. Closing the bar relies on this behaviour to wipe
// out the highlight band.
func TestTab_SetFindQuery_EmptyClears(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo")
	tab.SetFindQuery("foo")
	if tab.FindIndex < 0 {
		t.Fatal("setup expected a current match")
	}
	tab.SetFindQuery("")
	if tab.FindMatches != nil || tab.FindIndex != -1 || tab.FindQuery != "" {
		t.Fatalf("empty query should clear all find state, got %+v", tab)
	}
}

// TestTab_FindNext_WrapsAndMovesCursor exercises the Enter-in-the-bar
// path. After three Next presses we should land on match 0 again (wrap)
// with the cursor on top of it.
func TestTab_FindNext_WrapsAndMovesCursor(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.SetFindQuery("foo") // FindIndex = 0
	tab.FindNext()          // -> 1
	tab.FindNext()          // -> 2
	tab.FindNext()          // -> 0 (wrap)
	if tab.FindIndex != 0 {
		t.Fatalf("expected wrap to 0, got %d", tab.FindIndex)
	}
	if tab.Cursor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("cursor should follow the active match, got %+v", tab.Cursor)
	}
}

// TestTab_FindPrev_WrapsBackwards is the Shift-Enter equivalent — from
// the first match, Prev wraps to the last.
func TestTab_FindPrev_WrapsBackwards(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.SetFindQuery("foo")
	tab.FindPrev()
	if tab.FindIndex != 2 {
		t.Fatalf("expected wrap to last (2), got %d", tab.FindIndex)
	}
}

// TestTab_FindNext_NoMatchesIsSafe pins the contract that Find ops are
// no-ops when there's nothing to find. Without this, a stray hotkey on
// an empty result set would crash.
func TestTab_FindNext_NoMatchesIsSafe(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world")
	tab.SetFindQuery("zzz")
	tab.FindNext() // must not panic
	tab.FindPrev() // must not panic
	if tab.FindIndex != -1 {
		t.Fatalf("FindIndex should stay -1 with no matches, got %d", tab.FindIndex)
	}
}

// TestTab_ClearFind wipes everything so the renderer stops highlighting.
func TestTab_ClearFind(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo")
	tab.SetFindQuery("foo")
	tab.ClearFind()
	if tab.FindQuery != "" || tab.FindMatches != nil || tab.FindIndex != -1 {
		t.Fatalf("ClearFind left residue: %+v", tab)
	}
}

// TestFindAllOpts_CaseSensitive pins the toggle that makes find usable
// on code: with it on, `Cursor` and `cursor` stop being the same word.
func TestFindAllOpts_CaseSensitive(t *testing.T) {
	buf := NewBuffer("Foo FOO foo")
	got := FindAllOpts(buf, "foo", FindOptions{CaseSensitive: true})
	want := []Match{{Line: 0, Col: 8, Width: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("case-sensitive mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindAllOpts_WholeWord rejects hits inside a longer identifier —
// the "err must not match errors" rule, borrowed from the word
// highlighter so both features agree on where a word ends.
func TestFindAllOpts_WholeWord(t *testing.T) {
	buf := NewBuffer("err errors err_x (err)")
	got := FindAllOpts(buf, "err", FindOptions{WholeWord: true})
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 18, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("whole-word mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindAllOpts_BothToggles proves the two modifiers compose rather
// than one silently winning.
func TestFindAllOpts_BothToggles(t *testing.T) {
	buf := NewBuffer("Err err errs Err")
	got := FindAllOpts(buf, "Err", FindOptions{CaseSensitive: true, WholeWord: true})
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 13, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("combined mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindAllOpts_CaseFoldPreservesColumns is the regression guard for
// the fold: U+0130 lowercases to TWO runes under strings.ToLower, which
// would shift every column reported after it. Per-rune folding keeps the
// column of the hit honest.
func TestFindAllOpts_CaseFoldPreservesColumns(t *testing.T) {
	buf := NewBuffer("İx foo")
	got := FindAll(buf, "foo")
	want := []Match{{Line: 0, Col: 3, Width: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fold shifted columns:\n got=%v\nwant=%v", got, want)
	}
}

// TestTab_SetFindOptions_RerunsQuery pins the reason options live behind
// a setter: flipping one must re-derive the match list, or the bar would
// show a count the toggles say is impossible.
func TestTab_SetFindOptions_RerunsQuery(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("Foo foo FOO")
	tab.SetFindQuery("foo")
	if len(tab.FindMatches) != 3 {
		t.Fatalf("insensitive search should find 3, got %d", len(tab.FindMatches))
	}
	tab.SetFindOptions(FindOptions{CaseSensitive: true})
	if len(tab.FindMatches) != 1 {
		t.Fatalf("case-sensitive re-run should find 1, got %d", len(tab.FindMatches))
	}
}

// TestMatchOccurrences_MatchesFindScanner pins the shared-scanner claim:
// the word highlighter and a case-sensitive whole-word find must return
// the same hits on the same text, or the user gets two different answers
// to one question.
func TestMatchOccurrences_MatchesFindScanner(t *testing.T) {
	buf := NewBuffer("err errors\nErr err")
	viaWordHL := MatchOccurrences(buf, "err", true, 0, buf.LineCount()-1)
	viaFind := FindAllOpts(buf, "err", FindOptions{CaseSensitive: true, WholeWord: true})
	if !reflect.DeepEqual(viaWordHL, viaFind) {
		t.Fatalf("scanners disagree:\n wordhl=%v\n   find=%v", viaWordHL, viaFind)
	}
}

// TestMatchesAt_AgreesWithTheScanner pins MatchesAt to FindAllOpts under
// every combination of the two toggles: each hit the scan reports must
// answer true at its column, and every other column false. A check that
// drifted from the scan is exactly how a fresh hit reads as "stale".
func TestMatchesAt_AgreesWithTheScanner(t *testing.T) {
	const text = "Count count counter recount COUNT"
	line := []rune(text)
	buf := NewBuffer(text)
	for _, opts := range []FindOptions{
		{}, {CaseSensitive: true}, {WholeWord: true}, {CaseSensitive: true, WholeWord: true},
	} {
		hitAt := map[int]bool{}
		for _, m := range FindAllOpts(buf, "count", opts) {
			hitAt[m.Col] = true
		}
		for col := range line {
			if got := MatchesAt(line, col, 5, "count", opts); got != hitAt[col] {
				t.Errorf("opts %+v col %d: MatchesAt = %v, scan says %v", opts, col, got, hitAt[col])
			}
		}
	}
}

// TestMatchesAt_RejectsOutOfRangeAndEmpty covers the guards: a range
// running off either end of the line, and an empty query, are never a
// match (and never a panic).
func TestMatchesAt_RejectsOutOfRangeAndEmpty(t *testing.T) {
	line := []rune("count")
	for _, col := range []int{-1, 1, 5, 99} {
		if MatchesAt(line, col, 5, "count", FindOptions{}) {
			t.Errorf("col %d should not match", col)
		}
	}
	if MatchesAt(line, 0, 0, "", FindOptions{}) {
		t.Error("an empty query should never match")
	}
}

// TestFindAllOpts_RegexMatchesVariableWidthHits pins the core of regex
// mode: each hit carries its OWN width (the literal path's "width = query
// length" no longer holds), columns are runes not bytes, and the default
// is case-insensitive like the literal search.
func TestFindAllOpts_RegexMatchesVariableWidthHits(t *testing.T) {
	buf := NewBuffer("é foo1 FOO22\nbar")
	got := FindAllOpts(buf, `foo\d+`, FindOptions{Regex: true})
	want := []Match{{Line: 0, Col: 2, Width: 4}, {Line: 0, Col: 7, Width: 5}}
	if len(got) != len(want) {
		t.Fatalf("hits = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hit %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if n := len(FindAllOpts(buf, `foo\d+`, FindOptions{Regex: true, CaseSensitive: true})); n != 1 {
		t.Errorf("match case: %d hits, want 1", n)
	}
}

// TestFindAllOpts_RegexWholeWordUsesWordRunes: whole word under regex is
// the literal path's IsWordRune boundary, not RE2's ASCII \b — so "caf"
// inside "café" is NOT a standalone word.
func TestFindAllOpts_RegexWholeWordUsesWordRunes(t *testing.T) {
	buf := NewBuffer("café caf")
	got := FindAllOpts(buf, `ca.`, FindOptions{Regex: true, WholeWord: true})
	if len(got) != 1 || got[0].Col != 5 {
		t.Fatalf("hits = %+v, want only the standalone caf at col 5", got)
	}
}

// TestFindAllOpts_RegexDropsZeroWidthHits: a pattern that matches
// emptily has no cells to paint, so it reports nothing rather than an
// invisible stop at every column.
func TestFindAllOpts_RegexDropsZeroWidthHits(t *testing.T) {
	buf := NewBuffer("abc\ndef")
	if got := FindAllOpts(buf, `^`, FindOptions{Regex: true}); len(got) != 0 {
		t.Fatalf("zero-width hits leaked: %+v", got)
	}
	if got := FindAllOpts(buf, `x*`, FindOptions{Regex: true}); len(got) != 0 {
		t.Fatalf("empty-matching pattern leaked hits: %+v", got)
	}
}

// TestFindAllOpts_BadRegexFindsNothing: a half-typed pattern is "no
// hits", never a panic, and FindQueryError names the reason without
// the parser's preamble. Literal mode never errors on the same text.
func TestFindAllOpts_BadRegexFindsNothing(t *testing.T) {
	buf := NewBuffer("(foo")
	if got := FindAllOpts(buf, `(foo`, FindOptions{Regex: true}); got != nil {
		t.Fatalf("bad pattern produced hits: %+v", got)
	}
	err := FindQueryError(`(foo`, FindOptions{Regex: true})
	if err == nil || strings.Contains(err.Error(), "error parsing") || !strings.Contains(err.Error(), "missing closing )") {
		t.Fatalf("FindQueryError = %v", err)
	}
	if err := FindQueryError(`(foo`, FindOptions{}); err != nil {
		t.Fatalf("literal mode errored: %v", err)
	}
	if got := FindAllOpts(buf, `(foo`, FindOptions{}); len(got) != 1 {
		t.Fatalf("literal (foo: %d hits, want 1", len(got))
	}
}

// TestMatcher_AtAgreesWithTheScanUnderRegex is the regex twin of
// TestMatchesAt_AgreesWithTheScanner: every hit answers true at its own
// (col, width), and a right column with the wrong width does not — a
// row recorded as "foo12" must read stale once the text is "foo1".
func TestMatcher_AtAgreesWithTheScanUnderRegex(t *testing.T) {
	const text = "foo1 xfoo22 foo333"
	line := []rune(text)
	for _, opts := range []FindOptions{{Regex: true}, {Regex: true, WholeWord: true}} {
		mt, err := NewMatcher(`foo\d+`, opts)
		if err != nil {
			t.Fatal(err)
		}
		hits := map[[2]int]bool{}
		for _, m := range mt.Line(0, text) {
			hits[[2]int{m.Col, m.Width}] = true
		}
		for col := range line {
			for width := 1; col+width <= len(line); width++ {
				if got := mt.At(line, col, width); got != hits[[2]int{col, width}] {
					t.Errorf("opts %+v (%d,%d): At = %v, scan says %v", opts, col, width, got, !got)
				}
			}
		}
	}
}

// TestMatcher_ExpandFillsCapturesOnlyUnderRegex: $1 / ${name} expand
// against the hit's own captures in regex mode, and a literal replace
// keeps a typed "$1" as text.
func TestMatcher_ExpandFillsCapturesOnlyUnderRegex(t *testing.T) {
	line := []rune("a=1 b=22")
	mt, _ := NewMatcher(`(?P<k>\w)=(\d+)`, FindOptions{Regex: true})
	hits := mt.Line(0, string(line))
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if got := mt.Expand(line, hits[1].Col, hits[1].Width, "$2:${k}"); got != "22:b" {
		t.Errorf("Expand = %q, want %q", got, "22:b")
	}
	lit, _ := NewMatcher("a=1", FindOptions{})
	if got := lit.Expand(line, 0, 3, "$1"); got != "$1" {
		t.Errorf("literal Expand = %q, want the template verbatim", got)
	}
}

// TestNewMatcher_EmptyQueryIsANilMatcherThatMatchesNothing: the nil
// Matcher is a valid "no search" value every method tolerates.
func TestNewMatcher_EmptyQueryIsANilMatcherThatMatchesNothing(t *testing.T) {
	mt, err := NewMatcher("", FindOptions{Regex: true})
	if mt != nil || err != nil {
		t.Fatalf("NewMatcher(\"\") = %v, %v", mt, err)
	}
	if mt.All(NewBuffer("x")) != nil || mt.At([]rune("x"), 0, 1) || mt.IsRegex() {
		t.Fatal("nil Matcher matched something")
	}
}
