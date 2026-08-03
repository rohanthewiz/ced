// =============================================================================
// File: internal/editor/replace.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// replace.go turns the find state into an editing verb: swap the current
// hit for a replacement, or swap every hit in one step.
//
// Two rules shape both entry points.
//
// ONE UNDO STEP PER GESTURE. "Replace all" that took 200 presses to undo
// would be a trap, not a feature — so the bulk path files exactly one
// structural snapshot up front and then edits the Buffer directly.
// Editing through the Buffer rather than through Tab.InsertString is what
// keeps it to one step WITHOUT touching undoSuppress: that flag belongs
// to the multi-caret fan-out alone (an unbalanced `true` silently
// disables undo for the rest of the session), and the Buffer primitives
// don't record history at all, so pushUndo is never re-entered.
//
// BOTTOM-UP, ALWAYS. Matches are applied in descending document order,
// the same discipline the caret fan-out follows and for the same reason:
// an edit can only move text that comes AFTER it, so replacing from the
// end means every position still waiting is still correct. Top-down would
// invalidate every match below the first replacement — and a replacement
// of a different length invalidates them by a different amount per line,
// which is the flavor of bug that survives a whole test suite because it
// only shows up when the replacement isn't the same width as the query.

package editor

// ReplaceCurrent swaps the currently-pointed match for replacement and
// leaves the find state pointing at the next hit, reporting whether
// anything changed.
//
// It goes through the ordinary selection+insert path (select the match,
// InsertString over it) rather than the Buffer, because that path already
// records exactly one structural undo step, patches the style grid, bumps
// EditRev for the LSP/Copilot sync, and marks the tab dirty. Nothing here
// needs to be smarter than the user typing over their own selection —
// that is precisely the edit being made.
func (t *Tab) ReplaceCurrent(replacement string) bool {
	if t == nil || t.IsImage() {
		return false
	}
	if t.FindIndex < 0 || t.FindIndex >= len(t.FindMatches) {
		return false
	}
	m := t.FindMatches[t.FindIndex]
	// Secondary carets would fan the insert out to every one of them —
	// "replace this hit" must touch exactly one place, so drop them the
	// same way any other explicit jump does.
	t.Carets = nil
	t.Anchor = MatchPosition(m)
	t.Cursor = MatchEndPosition(m)
	t.InsertString(replacement)
	// The cursor now sits past the replacement, so re-running the query
	// re-points FindIndex at the first hit at or after it — "replace and
	// advance", which is what makes repeated presses walk the file. A
	// replacement that itself contains the query (s/a/aa/) therefore
	// steps PAST what it just wrote instead of matching it forever.
	after := t.Cursor
	t.SetFindQuery(t.FindQuery)
	t.FindIndex = FirstMatchAtOrAfter(t.FindMatches, after)
	return true
}

// ReplaceAll swaps every match of the current query for replacement in a
// single undo step and returns how many were replaced. A query with no
// hits is a no-op that files no snapshot — an undo step that changes
// nothing is worse than none at all.
func (t *Tab) ReplaceAll(replacement string) int {
	if t == nil || t.IsImage() || t.FindQuery == "" {
		return 0
	}
	// Re-scan rather than trusting FindMatches: the cached list is what
	// the last SetFindQuery produced, and any edit since then (a manual
	// keystroke, a previous single replace) may have moved it.
	matches := FindAllOpts(t.Buffer, t.FindQuery, t.FindOpts)
	if len(matches) == 0 {
		return 0
	}
	t.pushUndo(undoGroupStructural)
	// Carets and any selection were measured against the buffer this is
	// about to rewrite; keeping either would leave the next keystroke
	// editing text that moved.
	t.Carets = nil
	for i := len(matches) - 1; i >= 0; i-- {
		start := MatchPosition(matches[i])
		end := MatchEndPosition(matches[i])
		t.Buffer.DeleteRange(start, end)
		t.Buffer.InsertString(start, replacement)
	}
	t.Dirty = true
	t.EditRev++
	// Structural by definition: a replacement may carry newlines, and
	// even one that doesn't has changed enough of the file that patching
	// the style grid row by row would cost more than the re-lex. This is
	// the InvalidateStyles default the syntax layer asks new mutators to
	// take unless they can prove the rows still line up.
	t.InvalidateStyles()
	// The cursor may now point past the end of a shortened line.
	t.Cursor = t.Buffer.Clamp(t.Cursor)
	t.Anchor = t.Cursor
	t.cursorMoved = true
	// Re-run the query so the bar's counter and the tinted hits describe
	// the buffer as it is now — normally zero matches, unless the
	// replacement contains the query.
	t.SetFindQuery(t.FindQuery)
	return len(matches)
}
