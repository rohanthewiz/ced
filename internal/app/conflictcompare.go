// =============================================================================
// File: internal/app/conflictcompare.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// conflictcompare.go is "Compare sides" for one conflict block: the two
// halves of a block, diffed line by line in the compare panel. The washes
// (conflictview.go) show WHERE the sides are; on a block of any size they
// do not show what actually differs between them — a one-character change
// buried in a twenty-line function reads as two identical-looking walls of
// tint. A diff answers that question directly.
//
//	<<<<<<< HEAD
//	    if s == "" {
//	        return nil
//	    }
//	=======
//	    if s == "" {
//	        return errEmpty
//	    }
//	>>>>>>> 2a0370b (…)
//	─ Compare · current (HEAD) ↔ incoming (2a0370b (…)) ──── +1 −1 ─ ⟳ ─ ✕ ─
//	  @@ -1,3 +1,3 @@
//	       if s == "" {
//	  -        return nil
//	  +        return errEmpty
//	       }
//
// Decisions worth knowing:
//
//   - IT IS THE COMPARE PANEL, not a new 3-way merge editor. The panel
//     already draws a unified diff, colours it and jumps from a row; what
//     it lacked was taking two arbitrary texts instead of "the buffer vs
//     something" (compareTexts). A dedicated merge editor would be a
//     second diff surface with its own geometry, scrolling and keys, for
//     a gesture that is "look, then click a lens".
//   - DIRECTION: the side being APPLIED is new. Current → incoming reads
//     "what the incoming commit would change in what I have", which is the
//     direction git's own operation runs. With a diff3 base section the
//     base is old on both of its pairs, so each diff reads "what this side
//     changed" — the two answers a 3-way view exists to put side by side.
//   - A diff3 block asks WHICH pair (a picker); a two-way block has only
//     one pair and opens straight into it — a picker with one row is a
//     click that asks nothing.
//   - THE BLOCK IS NAMED BY ITS OPENER LINE, the lens's re-check rule:
//     ⟳ re-reads the block whose `<<<<<<<` is still on that line, and says
//     so when there is none (resolved, or lines added above it) instead of
//     diffing a different block that happens to have inherited the index.

package app

import (
	"github.com/rohanthewiz/ced/internal/editor"
)

// conflictPair is which two parts of a block a comparison diffs.
type conflictPair int

const (
	// pairCurrentIncoming diffs the two sides against each other.
	pairCurrentIncoming conflictPair = iota
	// pairBaseCurrent is "what the current side changed" (diff3 only).
	pairBaseCurrent
	// pairBaseIncoming is "what the incoming side changed" (diff3 only).
	pairBaseIncoming
)

// compareConflict is the compare panel's memory of a conflict source:
// enough to find the block again for ⟳, nothing that goes stale silently.
type compareConflict struct {
	path  string // the conflicted file's absolute path
	start int    // the block's `<<<<<<<` line when it was compared
	pair  conflictPair
}

// conflictCompareLens is the lens ID's choice nibble for "Compare sides".
// It is not an editor.ConflictChoice — it settles nothing — so it takes
// the top of the nibble, far past the real choices, and conflictLensPress
// routes it here before anything reaches the resolver.
const conflictCompareLens editor.ConflictChoice = 0xf

// conflictSideLabel names one part of a block for the panel header and
// the diff's ---/+++ lines: the positional word, then git's marker label
// when there is one ("current (HEAD)", "incoming (2a0370b (bump a))").
// Elided — a merge's labels can be whole branch paths, and the header
// has to fit both names on one row.
func conflictSideLabel(word, marker string) string {
	if marker == "" {
		return word
	}
	return word + " (" + elide(marker, 24) + ")"
}

// conflictPairSides returns a pair's old and new lines and labels, and
// the buffer line the new side starts on (compare.newLineBase).
func conflictPairSides(b editor.ConflictBlock, lines []string, p conflictPair) (oldLabel string, oldLines []string, newLabel string, newLines []string, newBase int) {
	cur := conflictSideLabel("current", b.CurrentLabel)
	inc := conflictSideLabel("incoming", b.IncomingLabel)
	base := conflictSideLabel("base", b.BaseLabel)
	switch p {
	case pairBaseCurrent:
		return base, b.BaseLines(lines), cur, b.CurrentLines(lines), b.Start + 1
	case pairBaseIncoming:
		return base, b.BaseLines(lines), inc, b.IncomingLines(lines), b.Mid + 1
	default:
		return cur, b.CurrentLines(lines), inc, b.IncomingLines(lines), b.Mid + 1
	}
}

// conflictBlockStartingAt finds the block whose opener is on `line` — the
// identity a remembered comparison is checked against.
func conflictBlockStartingAt(t *editor.Tab, line int) (editor.ConflictBlock, bool) {
	for _, b := range t.Conflicts() {
		if b.Start == line {
			return b, true
		}
		if b.Start > line {
			break // document order
		}
	}
	return editor.ConflictBlock{}, false
}

// -----------------------------------------------------------------------------
// Doors
// -----------------------------------------------------------------------------

// menuCompareConflictAtCaret is the ≡ Git row (and the palette's): the
// keyboard twin of the lens's "Compare" button.
func (a *App) menuCompareConflictAtCaret() {
	a.closeMenu()
	t, idx, ok := a.conflictAtCaret()
	if !ok {
		a.flash("The caret is not inside a conflict — Next conflict finds one")
		return
	}
	a.compareConflictBlock(t, idx)
}

// compareConflictBlock is the single entry behind every door: a two-way
// block opens its one comparison, a diff3 block asks which of three.
func (a *App) compareConflictBlock(t *editor.Tab, idx int) {
	blocks := t.Conflicts()
	if idx < 0 || idx >= len(blocks) {
		return
	}
	b := blocks[idx]
	if !b.HasBase() {
		a.showConflictCompare(t, b.Start, pairCurrentIncoming)
		return
	}
	// Rows name the SIDE in the user's words first and git's label second,
	// so the picker reads without knowing which hash is whose.
	type row struct {
		label string
		pair  conflictPair
	}
	rows := []row{
		{"Current ↔ incoming — the two sides", pairCurrentIncoming},
		{"Base ↔ current — what " + elide(nonEmpty(b.CurrentLabel, "current"), 24) + " changed", pairBaseCurrent},
		{"Base ↔ incoming — what " + elide(nonEmpty(b.IncomingLabel, "incoming"), 24) + " changed", pairBaseIncoming},
	}
	items := make([]paletteItem, 0, len(rows))
	for _, r := range rows {
		r := r
		start := b.Start
		items = append(items, paletteItem{label: r.label, run: func(app *App) {
			app.showConflictCompare(t, start, r.pair)
		}})
	}
	a.openPicker("Compare conflict "+itoa(idx+1)+" of "+itoa(len(blocks))+
		" · "+conflictSidesNote(b), items)
}

// nonEmpty returns s, or fallback when s is empty.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// showConflictCompare diffs one pair of the block opening on line `start`
// into the compare panel, and remembers the block for ⟳ and the "+" side's
// buffer line for the double-click. Reports whether the block was there.
func (a *App) showConflictCompare(t *editor.Tab, start int, p conflictPair) bool {
	b, ok := conflictBlockStartingAt(t, start)
	if !ok {
		a.flash("That conflict is gone — resolved, or lines moved above it")
		return false
	}
	oldLabel, oldLines, newLabel, newLines, newBase := conflictPairSides(b, t.Buffer.Lines, p)
	a.compareTexts(oldLabel, oldLines, newLabel, newLines)
	a.compare.newPath = t.Path
	a.compare.newLineBase = newBase
	a.compare.conflict = &compareConflict{path: t.Path, start: start, pair: p}
	return true
}

// compareConflictRefresh is ⟳ for a conflict comparison: re-read the same
// pair of the same block from the file's buffer. A failed re-read leaves
// the old diff on screen — it is still what the block WAS — and flashes
// why, rather than closing the panel under the reader.
func (a *App) compareConflictRefresh() {
	c := a.compare.conflict
	t := a.tabForPath(c.path)
	if t == nil {
		a.flash("Compare: the conflicted file is no longer open")
		return
	}
	a.showConflictCompare(t, c.start, c.pair)
}
