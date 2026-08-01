// =============================================================================
// File: internal/editor/multicaret.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// multicaret.go is the editor's multi-line editing model: extra caret
// positions that type, delete, and move alongside the primary one.
//
// The shape is "primary + secondaries", NOT a list of equal carets.
// Tab.Cursor / Tab.Anchor stay exactly what they were — the caret the
// hardware cursor sits on, the one EnsureVisible scrolls to, the one
// every existing feature (find, ghost text, hover, line ops, the status
// bar) already reads. Secondaries live in Tab.Carets and are empty in
// the overwhelmingly common case, so a single-caret session pays for
// none of this.
//
// The fan-out is the interesting part. Every edit primitive already
// knows how to work at Cursor/Anchor, so instead of rewriting them to
// loop, applyAtCarets swaps each caret into Cursor/Anchor in turn, runs
// the ORIGINAL single-caret code, and reads the updated position back
// out. Two rules make that safe:
//
//   - **Bottom-up.** Carets are visited in descending document order,
//     so an edit can only ever move text that lies AFTER the carets
//     still waiting their turn. Top-down would invalidate every
//     position below the first edit.
//   - **One undo step.** The fan-out pushes a single structural
//     snapshot up front and sets undoSuppress for the duration, so
//     undoing a five-caret typing burst is one Esc-u, not five. Without
//     that, each primitive's own pushUndo would file its own entry.
//
// Undo / redo and any explicit jump (a click, a definition jump, a find
// hit) drop the secondaries: their positions were measured against a
// buffer state that no longer exists, and silently editing at a stale
// line is the one failure mode this design must not have.
package editor

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// Caret is one editing point: a cursor and its selection anchor —
// exactly the pair Tab.Cursor / Tab.Anchor hold for the primary.
type Caret struct {
	Cursor Position
	Anchor Position
}

// HasSelection reports whether this caret covers a non-empty range.
func (c Caret) HasSelection() bool { return c.Cursor != c.Anchor }

// caretStart returns the caret's document-order start — the position
// ordering and overlap checks compare on.
func caretStart(c Caret) Position {
	s, _ := PosOrdered(c.Anchor, c.Cursor)
	return s
}

// HasCarets reports whether any secondary carets are active. Callers use
// it to gate multi-caret-only UI (the status readout, the Clear row).
func (t *Tab) HasCarets() bool { return len(t.Carets) > 0 }

// CaretCount returns the number of editing points, primary included —
// always at least 1.
func (t *Tab) CaretCount() int { return len(t.Carets) + 1 }

// ClearCarets drops every secondary caret, reporting whether there was
// anything to drop. The bool lets Esc distinguish "I cleared something"
// from "nothing happened" without re-checking.
func (t *Tab) ClearCarets() bool {
	if len(t.Carets) == 0 {
		return false
	}
	t.Carets = nil
	t.cursorMoved = true
	return true
}

// AllCarets returns every editing point in document order, primary
// included. Consumers that only need to READ the caret set (rendering,
// selection spans) go through this so they don't have to remember that
// the primary lives in different fields.
func (t *Tab) AllCarets() []Caret {
	out := make([]Caret, 0, len(t.Carets)+1)
	out = append(out, Caret{Cursor: t.Cursor, Anchor: t.Anchor})
	out = append(out, t.Carets...)
	sort.SliceStable(out, func(i, j int) bool {
		return posLess(caretStart(out[i]), caretStart(out[j]))
	})
	return out
}

// AddCaretAt toggles a secondary caret at p — the Alt+click gesture.
// Clicking an existing caret removes it (undoing a misplaced one
// shouldn't cost the whole set), and a click on the primary is ignored
// rather than duplicating it. Reports whether the caret set changed.
func (t *Tab) AddCaretAt(p Position) bool {
	if t.IsImage() {
		return false
	}
	p = t.Buffer.Clamp(p)
	if p == t.Cursor {
		return false
	}
	for i, c := range t.Carets {
		if c.Cursor == p {
			t.Carets = append(t.Carets[:i], t.Carets[i+1:]...)
			t.cursorMoved = true
			return true
		}
	}
	t.Carets = append(t.Carets, Caret{Cursor: p, Anchor: p})
	t.normalizeCarets()
	t.breakUndoGroup()
	return true
}

// AddCaretLine adds a caret one line above (delta -1) or below (+1) the
// outermost caret in that direction, at the caret column's goal column.
// Repeating the gesture therefore grows the column downward (or upward)
// instead of piling carets on one line.
//
// The new caret becomes the PRIMARY and the old one demotes to a
// secondary — so the viewport follows the caret the user just created
// (EnsureVisible only ever tracks the primary), and "the primary is the
// last caret you placed" stays true across every gesture in this file.
func (t *Tab) AddCaretLine(delta int) bool {
	if t.IsImage() || delta == 0 {
		return false
	}
	line := t.Cursor.Line
	for _, c := range t.Carets {
		if delta > 0 && c.Cursor.Line > line {
			line = c.Cursor.Line
		}
		if delta < 0 && c.Cursor.Line < line {
			line = c.Cursor.Line
		}
	}
	line += delta
	if line < 0 || line >= t.Buffer.LineCount() {
		return false
	}
	p := t.Buffer.Clamp(Position{Line: line, Col: t.caretGoalCol()})
	t.promoteCaret(Caret{Cursor: p, Anchor: p})
	return true
}

// caretGoalCol is the column a newly added caret aims for: the widest
// column any existing caret holds.
//
// The obvious rule — "use the primary's column" — drifts. Clamp pulls a
// caret back on a short line, that shortened column then seeds the next
// add, and a column started at the end of a long line walks left every
// time it passes a short one. Taking the maximum lets the caret that
// WASN'T clamped keep carrying the goal, so the intended column survives
// short lines without a sticky-column field to keep in sync.
func (t *Tab) caretGoalCol() int {
	col := t.Cursor.Col
	for _, c := range t.Carets {
		if c.Cursor.Col > col {
			col = c.Cursor.Col
		}
	}
	return col
}

// AddNextOccurrence is the "select the next one too" gesture. With a
// bare cursor it selects the word under the caret and stops — that first
// press is how the user states WHAT they're matching, and adding a
// second caret in the same stroke would skip past the chance to look at
// it. Every press after that adds the next occurrence (wrapping at the
// end of the file) as a new primary caret, so the view follows along.
func (t *Tab) AddNextOccurrence() bool {
	if t.IsImage() {
		return false
	}
	if !t.HasSelection() {
		runes := t.Buffer.LineRunes(t.Cursor.Line)
		start, end, ok := WordRange(runes, t.Cursor.Col)
		if !ok {
			return false
		}
		t.Anchor = Position{Line: t.Cursor.Line, Col: start}
		t.Cursor = Position{Line: t.Cursor.Line, Col: end}
		t.cursorMoved = true
		t.breakUndoGroup()
		return true
	}
	text, wholeWord, ok := t.caretQuery()
	if !ok {
		return false
	}
	matches := MatchOccurrences(t.Buffer, text, wholeWord, 0, t.Buffer.LineCount()-1)
	if len(matches) == 0 {
		return false
	}
	all := t.AllCarets()
	covered := make(map[Position]bool, len(all))
	for _, c := range all {
		covered[caretStart(c)] = true
	}
	// Start scanning just past the last caret in the file; falling
	// through with begin=0 is the wrap-to-top case.
	last := caretStart(all[len(all)-1])
	begin := 0
	for i, m := range matches {
		if posLess(last, MatchPosition(m)) {
			begin = i
			break
		}
	}
	for k := 0; k < len(matches); k++ {
		m := matches[(begin+k)%len(matches)]
		if covered[MatchPosition(m)] {
			continue
		}
		t.promoteCaret(Caret{Anchor: MatchPosition(m), Cursor: MatchEndPosition(m)})
		return true
	}
	return false // every occurrence already has a caret
}

// SelectAllOccurrences puts a caret on every occurrence of the word
// under the cursor (or of the current single-line selection) and returns
// how many there are. The occurrence the caret is already sitting in
// stays primary so the viewport doesn't jump; 0 means nothing matched
// and the caret set is left alone.
func (t *Tab) SelectAllOccurrences() int {
	if t.IsImage() {
		return 0
	}
	text, wholeWord, ok := t.caretQuery()
	if !ok {
		return 0
	}
	matches := MatchOccurrences(t.Buffer, text, wholeWord, 0, t.Buffer.LineCount()-1)
	if len(matches) == 0 {
		return 0
	}
	primary := matchIndexAt(matches, t.Cursor)
	t.Carets = nil
	for i, m := range matches {
		if i == primary {
			continue
		}
		t.Carets = append(t.Carets, Caret{Anchor: MatchPosition(m), Cursor: MatchEndPosition(m)})
	}
	m := matches[primary]
	t.Anchor = MatchPosition(m)
	t.Cursor = MatchEndPosition(m)
	t.cursorMoved = true
	t.breakUndoGroup()
	return len(matches)
}

// matchIndexAt returns the index of the match containing pos, falling
// back to the first match at or after it (wrapping to the top). The
// containment check matters because the caret usually sits at the END of
// the word it's in — which "at or after" would read as the next match.
func matchIndexAt(matches []Match, pos Position) int {
	for i, m := range matches {
		if m.Line == pos.Line && pos.Col >= m.Col && pos.Col <= m.Col+m.Width {
			return i
		}
	}
	if i := FirstMatchAtOrAfter(matches, pos); i >= 0 {
		return i
	}
	return 0
}

// caretQuery returns the text the occurrence gestures search for: a
// single-line selection when there is one, otherwise the word under the
// primary cursor. A multi-line or blank selection returns ok=false;
// neither names a thing the user could mean by "the next one".
//
// wholeWord is decided by what the range IS, not by which branch found
// it: a selection that exactly spans an identifier matches whole-word,
// anything else matches as a substring. That's what keeps the gesture
// coherent across presses — the first Esc-* turns the word under the
// cursor INTO a selection, and without this rule the second press would
// quietly widen "count" to also mean "counter".
func (t *Tab) caretQuery() (text string, wholeWord bool, ok bool) {
	if t.HasSelection() {
		s, e := PosOrdered(t.Anchor, t.Cursor)
		if s.Line != e.Line {
			return "", false, false
		}
		sel := t.Buffer.Substring(s, e)
		if strings.TrimSpace(sel) == "" {
			return "", false, false
		}
		return sel, isWholeWordRange(t.Buffer.LineRunes(s.Line), s.Col, e.Col), true
	}
	runes := t.Buffer.LineRunes(t.Cursor.Line)
	start, end, found := WordRange(runes, t.Cursor.Col)
	if !found {
		return "", false, false
	}
	return string(runes[start:end]), true, true
}

// isWholeWordRange reports whether [start, end) covers a complete
// identifier: every rune inside is word content and neither neighbour
// is. See caretQuery for why the distinction is drawn from the range
// rather than remembered as a mode.
func isWholeWordRange(runes []rune, start, end int) bool {
	if start < 0 || end > len(runes) || start >= end {
		return false
	}
	for i := start; i < end; i++ {
		if !IsWordRune(runes[i]) {
			return false
		}
	}
	if start > 0 && IsWordRune(runes[start-1]) {
		return false
	}
	if end < len(runes) && IsWordRune(runes[end]) {
		return false
	}
	return true
}

// promoteCaret installs c as the primary caret and demotes the current
// primary to a secondary. See AddCaretLine for why every gesture that
// creates a caret goes through here.
func (t *Tab) promoteCaret(c Caret) {
	t.Carets = append(t.Carets, Caret{Cursor: t.Cursor, Anchor: t.Anchor})
	t.Cursor = c.Cursor
	t.Anchor = c.Anchor
	t.normalizeCarets()
	t.cursorMoved = true
	t.breakUndoGroup()
}

// normalizeCarets clamps every secondary into the buffer and drops
// duplicates — both of each other and of the primary. Run after every
// mutation: an edit can easily collapse two carets onto one position
// (two carets on the same line, each deleting the text between them),
// and leaving both would double every subsequent keystroke there.
func (t *Tab) normalizeCarets() {
	if len(t.Carets) == 0 {
		return
	}
	seen := map[Position]bool{t.Cursor: true}
	out := t.Carets[:0]
	for _, c := range t.Carets {
		c.Cursor = t.Buffer.Clamp(c.Cursor)
		c.Anchor = t.Buffer.Clamp(c.Anchor)
		if seen[c.Cursor] {
			continue
		}
		seen[c.Cursor] = true
		out = append(out, c)
	}
	t.Carets = out
	if len(t.Carets) == 0 {
		t.Carets = nil
	}
}

// dropCaretsForLineOp collapses to a single caret before a whole-LINE
// gesture (duplicate, move, comment toggle) runs.
//
// Those three are block operations whose extent is defined by the
// selection, and two of them change the line count or order — which
// would leave every caret below the edit pointing at the wrong line,
// the one failure mode this design must not have. Fanning them out
// isn't the fix either: two carets on one line would duplicate it
// twice. Collapsing is the honest answer, and the caret readout
// disappearing from the status bar is what tells the user the mode
// ended.
func (t *Tab) dropCaretsForLineOp() {
	t.Carets = nil
}

// caretSlot is a caret plus the flag saying which one is the primary, so
// applyAtCarets can put it back where it belongs after sorting.
type caretSlot struct {
	Caret
	primary bool
}

// caretSlotsDescending returns every caret in REVERSE document order —
// the order applyAtCarets must visit them in. See the file comment.
func (t *Tab) caretSlotsDescending() []caretSlot {
	out := make([]caretSlot, 0, len(t.Carets)+1)
	out = append(out, caretSlot{Caret: Caret{Cursor: t.Cursor, Anchor: t.Anchor}, primary: true})
	for _, c := range t.Carets {
		out = append(out, caretSlot{Caret: c})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return posLess(caretStart(out[j].Caret), caretStart(out[i].Caret))
	})
	return out
}

// applyAtCarets runs a single-caret primitive once per caret, bottom-up,
// and collects the resulting positions back into the caret set. With no
// secondaries it's a direct call — the single-caret path costs one
// length check.
//
// mutating selects whether the run is wrapped as one undo step: content
// edits are (five carets typing a rune is ONE thing the user did), plain
// cursor movement isn't (it pushes nothing to begin with).
func (t *Tab) applyAtCarets(mutating bool, op func()) {
	if len(t.Carets) == 0 {
		op()
		return
	}
	slots := t.caretSlotsDescending()
	if mutating {
		t.pushUndo(undoGroupStructural)
		t.undoSuppress = true
	}
	for i := range slots {
		t.Cursor, t.Anchor = slots[i].Cursor, slots[i].Anchor
		op()
		slots[i].Cursor, slots[i].Anchor = t.Cursor, t.Anchor
	}
	t.undoSuppress = false

	t.Carets = t.Carets[:0]
	for _, s := range slots {
		if s.primary {
			t.Cursor, t.Anchor = s.Cursor, s.Anchor
			continue
		}
		t.Carets = append(t.Carets, s.Caret)
	}
	t.normalizeCarets()
	t.cursorMoved = true
}

// paintCarets draws the secondary carets that fall on lineIdx as an
// inverted cell — the rune under the caret in the background color on
// the accent, or a plain accent block when the caret sits past the end
// of the line. Called once per rendered row by Tab.Render.
//
// A terminal has exactly ONE hardware cursor, which the primary caret
// owns; every other caret has to be drawn as content, and it has to be
// drawn after the row's paint walk so it wins over the text underneath.
// That's also why the blink is CaretsHidden and not a style attribute:
// SGR blink toggles the glyph, and a caret past the end of a line has no
// glyph to toggle — the cell it paints is a space.
func (t *Tab) paintCarets(scr tcell.Screen, th theme.Theme, lineIdx, cy, contentX, contentW int, lineBg tcell.Color) {
	if len(t.Carets) == 0 || t.CaretsHidden {
		return
	}
	runes := t.Buffer.LineRunes(lineIdx)
	scrollVisual := LineVisualCol(runes, t.ScrollX)
	style := wordHighlightStyle(th, lineBg)
	for _, c := range t.Carets {
		if c.Cursor.Line != lineIdx {
			continue
		}
		col := c.Cursor.Col
		sc := LineVisualCol(runes, col) - scrollVisual
		if sc < 0 || sc >= contentW {
			continue
		}
		glyph := ' '
		if col < len(runes) && runes[col] != '\t' {
			glyph = runes[col]
		}
		scr.SetContent(contentX+sc, cy, glyph, nil, style)
	}
}

// CaretText returns the text every caret covers, joined by newlines in
// document order — what a copy with several carets down should yield.
// Empty when nothing anywhere is selected.
func (t *Tab) CaretText() string {
	var parts []string
	for _, c := range t.AllCarets() {
		if !c.HasSelection() {
			continue
		}
		parts = append(parts, t.Buffer.Substring(c.Anchor, c.Cursor))
	}
	return strings.Join(parts, "\n")
}
