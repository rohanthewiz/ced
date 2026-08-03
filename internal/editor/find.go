// =============================================================================
// File: internal/editor/find.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// find.go implements the editor's in-file search primitives. Matching runs
// on rune-decoded lines so multi-byte characters behave as one column each
// (consistent with how the cursor / selection already treat columns
// elsewhere), and the default is a case-INSENSITIVE substring: find is a
// reading tool, so "buffer" should turn up every spelling of it.
//
// FindOptions makes that default a choice rather than a law. The two
// toggles it carries (match case, whole word) are exactly the two the
// word-highlight scanner already needed for its own reasons — see
// wordhl.go's header for why THAT feature is case-sensitive and
// whole-word by default — so both features now share one scanner
// (matchCols). Two implementations of "what counts as a hit" would drift,
// and the user would have no way to tell which one answered.
//
// Regex is still deliberately out of scope: it needs its own input
// grammar, its own error surface for a half-typed pattern, and a
// different replacement language ($1). None of that is the 80/20 of the
// type-and-jump loop.

package editor

import "unicode"

// Match describes one find hit. Line and Col follow the same rune-indexed
// convention as Position; Width is the rune count of the query so the
// renderer can paint the right number of cells without re-running the
// matcher.
type Match struct {
	Line  int
	Col   int
	Width int
}

// FindOptions carries the search modifiers a query is matched under. The
// zero value is the historical default (case-insensitive, substring),
// which is what keeps every caller that doesn't care about options — and
// every test written before they existed — working unchanged.
type FindOptions struct {
	// CaseSensitive matches "Buffer" against "Buffer" but not "buffer".
	CaseSensitive bool
	// WholeWord rejects a hit that sits inside a longer identifier, so
	// "err" stops matching "errors". Boundaries use IsWordRune — the
	// programmer's definition, shared with double-click word select.
	WholeWord bool
}

// FindAll returns every case-insensitive substring match of query inside
// buf, in document order — FindAllOpts under the default options. Kept as
// its own entry point because most callers (project search, the find-all
// list's seeding) genuinely want the plain reading-tool behavior and
// shouldn't have to name a zero struct to say so.
func FindAll(buf *Buffer, query string) []Match {
	return FindAllOpts(buf, query, FindOptions{})
}

// FindAllOpts returns every match of query inside buf under opts, in
// document order. An empty query returns nil — the caller is expected to
// clear its UI rather than show "0 of 0" results. Matches do not overlap:
// after a hit the scanner advances past the matched run, so "aaaa" with
// query "aa" yields two matches at columns 0 and 2.
func FindAllOpts(buf *Buffer, query string, opts FindOptions) []Match {
	if query == "" || buf == nil {
		return nil
	}
	needle := foldRunes([]rune(query), opts.CaseSensitive)
	if len(needle) == 0 {
		return nil
	}
	var out []Match
	for lineIdx, raw := range buf.Lines {
		hay := foldRunes([]rune(raw), opts.CaseSensitive)
		for _, col := range matchCols(hay, needle, opts.WholeWord) {
			out = append(out, Match{Line: lineIdx, Col: col, Width: len(needle)})
		}
	}
	return out
}

// matchCols returns the starting columns of every non-overlapping hit of
// needle inside hay. It is the ONE place the editor decides what counts
// as a match: FindAllOpts (whole buffer, optionally case-folded) and
// MatchOccurrences (a line window, always case-sensitive) both come
// through here, so an in-file search, a project search and the word
// highlight can never disagree about a hit.
//
// Case folding is the CALLER's job — it must fold the needle and the
// haystack the same way, and folding a whole line once beats folding a
// slice of it per candidate column.
func matchCols(hay, needle []rune, wholeWord bool) []int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return nil
	}
	var cols []int
	for col := 0; col+len(needle) <= len(hay); col++ {
		if !runesEqual(hay[col:col+len(needle)], needle) {
			continue
		}
		if wholeWord {
			if col > 0 && IsWordRune(hay[col-1]) {
				continue
			}
			if end := col + len(needle); end < len(hay) && IsWordRune(hay[end]) {
				continue
			}
		}
		cols = append(cols, col)
		col += len(needle) - 1 // the loop's ++ carries us past the hit
	}
	return cols
}

// foldRunes lowercases rs for a case-insensitive compare, or returns it
// untouched when the search is case-sensitive.
//
// Per-rune unicode.ToLower rather than strings.ToLower on purpose: a few
// runes (U+0130 LATIN CAPITAL I WITH DOT ABOVE is the classic) lowercase
// to MORE than one rune, and a fold that changes the rune count would
// shift every column reported after it — the match would paint over the
// wrong cells. Per-rune folding is length-preserving by construction, so
// a column in the folded line is the same column in the real one.
func foldRunes(rs []rune, caseSensitive bool) []rune {
	if caseSensitive {
		return rs
	}
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// runesEqual returns true when two equal-length rune slices match
// element-for-element. Inlined so the hot inner loop of FindAll doesn't
// pay for a generic slices.Equal call (which exists in 1.21+ but pulls in
// the slices package).
func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FirstMatchAtOrAfter returns the index into matches of the first hit at
// or after cursor, or 0 when cursor sits past the last match (we wrap
// around to the top — that's what the user expects after typing a query
// at the bottom of a file).
//
// Returns -1 when matches is empty so callers can short-circuit without
// re-checking the length.
func FirstMatchAtOrAfter(matches []Match, cursor Position) int {
	if len(matches) == 0 {
		return -1
	}
	for i, m := range matches {
		if m.Line > cursor.Line || (m.Line == cursor.Line && m.Col >= cursor.Col) {
			return i
		}
	}
	// Cursor is past every match — wrap to the top.
	return 0
}

// MatchPosition returns the cursor-friendly Position at the start of m.
// Trivial helper, but it keeps callers from constructing Position literals
// by hand (which loses the "rune-indexed" intent at the call site).
func MatchPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col}
}

// MatchEndPosition returns the position one past the end of m — useful
// when the caller wants to set a selection that covers the match.
func MatchEndPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col + m.Width}
}

// SetFindQuery installs a new search query on the tab, recomputes the
// match list against the current buffer, and points FindIndex at the
// first match at or after the cursor (so the user lands on the nearest
// hit, not always the first hit in the file). An empty query clears all
// find state — symmetrical with closing the bar via Esc.
//
// The cursor is left where it is; SetFindQuery only updates state. It is
// the caller's job to call FocusCurrentMatch when they want the cursor
// to actually move (which is what happens on the first non-empty query
// and on every Enter / Shift-Enter press).
func (t *Tab) SetFindQuery(query string) {
	t.FindQuery = query
	if query == "" {
		t.FindMatches = nil
		t.FindIndex = -1
		return
	}
	t.FindMatches = FindAllOpts(t.Buffer, query, t.FindOpts)
	t.FindIndex = FirstMatchAtOrAfter(t.FindMatches, t.Cursor)
}

// SetFindOptions installs new search modifiers and re-runs the current
// query under them. Going through one method (rather than letting callers
// poke t.FindOpts) is what guarantees the match list on screen was
// produced by the options the toggles are showing — a stale list under a
// flipped toggle is a lie the user can't detect.
func (t *Tab) SetFindOptions(opts FindOptions) {
	t.FindOpts = opts
	if t.FindQuery != "" {
		t.SetFindQuery(t.FindQuery)
	}
}

// FocusCurrentMatch moves the cursor (and anchor — we don't want a
// dangling selection from an earlier action) to the start of the
// currently-pointed match. No-op when FindIndex is out of range, so
// callers don't have to re-check it themselves.
func (t *Tab) FocusCurrentMatch() {
	if t.FindIndex < 0 || t.FindIndex >= len(t.FindMatches) {
		return
	}
	m := t.FindMatches[t.FindIndex]
	t.Cursor = MatchPosition(m)
	t.Anchor = t.Cursor
	// Jumping to a hit is an explicit navigation, so it drops secondary
	// carets for the same reason MoveCursorTo does.
	t.Carets = nil
	t.cursorMoved = true
}

// FindNext advances FindIndex by one (wrapping at the end) and moves
// the cursor onto the new match. No-op when there are no matches. Used
// by Enter inside the find bar and by the Esc-g "again" leader.
func (t *Tab) FindNext() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex = (t.FindIndex + 1) % len(t.FindMatches)
	t.FocusCurrentMatch()
}

// FindPrev moves FindIndex backwards by one (wrapping at the start) and
// moves the cursor onto the new match. Used by Shift-Enter inside the
// find bar.
func (t *Tab) FindPrev() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex--
	if t.FindIndex < 0 {
		t.FindIndex = len(t.FindMatches) - 1
	}
	t.FocusCurrentMatch()
}

// ClearFind drops every piece of find state. The app calls this when the
// buffer has been edited enough that the cached match list is stale and
// can't safely be re-used; the user will re-type their query.
func (t *Tab) ClearFind() {
	t.FindQuery = ""
	t.FindMatches = nil
	t.FindIndex = -1
}
