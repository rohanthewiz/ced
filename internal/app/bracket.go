// =============================================================================
// File: internal/app/bracket.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// bracket.go is the keyboard half of brace matching: Esc-% and the ≡
// Code row that jump the caret to the other end of the pair it is
// standing on. The matcher, the string/comment classifier and the
// highlight itself live in internal/editor/bracket.go.
//
// The verb owns almost nothing, which is the point — it resolves the
// pair, hands the position to goToLine, and spends the rest of its
// length saying WHY when it can't. Those messages are the whole reason
// this isn't three lines inline in the leader table: "no bracket here",
// "this bracket has no partner", and "the file is too big to say" are
// three different facts, and a single "no match" for all of them would
// leave a user staring at a brace they can see is balanced.
//
// One thing worth naming: the matcher's string/comment classifier reads
// the syntax grid the LAST FRAME painted, not a fresh lex. That is
// deliberate — forcing an O(file) Chroma pass out of a keystroke handler
// is precisely what syntax.go's settle policy exists to stop — and it is
// also the more honest answer, since the grid the user is looking at is
// the one their sense of "this is inside a string" came from.
package app

import "fmt"

// menuGoToMatchingBracket moves the caret to the partner of the bracket
// it is on (or immediately after), leaving it ON that bracket so a
// second press comes straight back — the round trip is what makes this
// worth a repeatable key rather than a menu row alone.
//
// Landing is delegated to goToLine, which clamps and re-centers an
// off-screen destination for the same reason the find-all preview does:
// a minimal scroll would park a function's closing brace on the last
// row, answering "where is it?" but not "what is around it?". Dropping
// any secondary carets comes free with MoveCursorTo, and is right —
// this is an explicit jump.
func (a *App) menuGoToMatchingBracket() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil || tab.IsImage() {
		return
	}
	bp, ok := tab.MatchingBracket(a.theme)
	if !ok {
		a.flash("Put the cursor on a bracket first")
		return
	}
	if !bp.Matched {
		// Conclusive says the scan reached the edge of the buffer rather
		// than the end of its budget. Only then is "unmatched" a claim
		// about the code; otherwise all we know is that we stopped
		// looking, and saying so beats accusing a balanced file.
		if bp.Conclusive {
			a.flash(fmt.Sprintf("No match for %c", bp.Rune))
		} else {
			a.flash(fmt.Sprintf("No match for %c within range", bp.Rune))
		}
		return
	}
	a.goToLine(bp.Partner.Line, bp.Partner.Col)
}
