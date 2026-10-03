// =============================================================================
// File: internal/editor/jumpmargin.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// jumpmargin.go is the CONTEXT MARGIN a jump lands with. EnsureVisible
// scrolls minimally — the right promise for a caret walking line by line,
// since anything more makes the view lurch under the user's typing — but
// a JUMP (a find hit, a definition, a nav retrace) that lands off the
// bottom of the view is then parked on the very last row, the identifier
// found and nothing after it on screen. A hit already sitting in the top
// or bottom row is left there too. Either way the user scrolls by hand
// to see what the line is doing, which is the reason they jumped.
//
// So a jump marks itself (MarkJump, or FocusCurrentMatch for the find
// bar) and Render reveals the caret with EnsureVisibleMargin instead:
// the same minimal scroll, but keeping JumpMargin rows between the
// caret's row and either edge of the view.
//
//	minimal (EnsureVisible)        with margin (EnsureVisibleMargin)
//	┌──────────────────────┐       ┌──────────────────────┐
//	│ ...                  │       │ ...                  │
//	│ ...                  │       │ func Found() {   ◀── caret
//	│ func Found() {  ◀────│       │     body …           │  JumpMargin
//	└──────────────────────┘       │ }                    │  rows of context
//	                               └──────────────────────┘
//
// Design choices:
//   - It is a property of the MOVE, not of the tab. Plain cursor motion
//     (arrows, clicks, typing) keeps the minimal rule: a click on the
//     last row must not scroll the text out from under the pointer, and
//     an editor-wide scrolloff would make Down at the bottom row jump.
//   - The flag is one-shot and consumed by the same Render that consumes
//     cursorMoved, so it can never outlive the move that set it. Paths
//     that clear cursorMoved (RestoreView, CenterOnCursor) clear it too —
//     a centered landing already has more context than the margin.
//   - The margin never pushes the view past the end of the file. A hit on
//     the last line keeps it on the last row: the clamp's overscroll
//     allowance would permit blank rows below it, but blank rows are not
//     context, and a view that scrolls away from real text on a jump
//     reads as a glitch. At the top, ScrollY 0 is the same natural limit.
//   - Small panes scale the margin down (a quarter of the view), so the
//     two margins together never squeeze the caret's own row out.

package editor

// JumpMargin is how many rows of context a jump keeps between the caret's
// row and the top or bottom edge of the view — "a few lines", the
// scrolloff value most editors that have one ship with.
const JumpMargin = 5

// MarkJump flags the cursor move just made as a JUMP, so the next Render
// reveals the caret with JumpMargin rows of context rather than parking
// it on an edge row. It also sets cursorMoved, so a jump to a position
// that was already the cursor's still gets its margin.
func (t *Tab) MarkJump() {
	t.cursorMoved = true
	t.jumpReveal = true
}

// jumpMarginFor is the margin a viewH-row view can afford: JumpMargin,
// shrunk to a quarter of the view so a short pane keeps room for the
// caret's row and some freedom between the two margins.
func jumpMarginFor(margin, viewH int) int {
	if q := viewH / 4; margin > q {
		margin = q
	}
	if margin < 0 {
		margin = 0
	}
	return margin
}

// EnsureVisibleMargin is EnsureVisible plus a context margin: after the
// caret is on screen, scroll the least amount that leaves `margin` rows
// above and below the caret's row — where rows exist to show (see the
// file header for why the end of the file is not padded with blanks).
// Horizontal scroll is EnsureVisible's alone; the margin is vertical.
func (t *Tab) EnsureVisibleMargin(viewW, viewH, margin int) {
	t.EnsureVisible(viewW, viewH)
	m := jumpMarginFor(margin, viewH)
	if m == 0 || t.Buffer == nil {
		return
	}
	if ww := t.wrapWidthFor(viewW); ww > 0 {
		t.marginWrapped(ww, viewH, m)
		return
	}
	line := t.Cursor.Line
	// Top edge: fewer than m lines above the caret inside the view → pull
	// the view up. ScrollY can't go below 0, which is the top-of-file
	// limit doing its job.
	if line-t.ScrollY < m {
		t.ScrollY = line - m
		if t.ScrollY < 0 {
			t.ScrollY = 0
		}
	}
	// Bottom edge: only lines that EXIST below the caret count as context,
	// so the wanted margin is capped by what's left of the file. Because
	// m ≤ viewH/4, satisfying this can't break the top margin just set.
	below := t.Buffer.LineCount() - 1 - line
	if below > m {
		below = m
	}
	if t.ScrollY+viewH-1-line < below {
		t.ScrollY = line + below - viewH + 1
	}
}

// marginWrapped is EnsureVisibleMargin's vertical rule in row units, for
// a soft-wrapped view. ScrollY is still a LINE index, so the view moves a
// whole line at a time: the top margin takes earlier lines only while
// they don't push the caret into the bottom margin, and the bottom margin
// drops top lines until enough rows below the caret are on screen. A
// line taller than the view can make either side fall short — the stated
// limit of line-indexed scrolling (ensureVisibleWrapped has the same).
func (t *Tab) marginWrapped(width, viewH, m int) {
	line := t.Cursor.Line
	_, starts := t.wrapLayout(line, width)
	caretRow := wrapRow(starts, t.Cursor.Col)
	// Rows from the top of the view to the caret's row. EnsureVisible has
	// just put the caret on screen, so the viewH limit never truncates it.
	above := t.wrapRowsBefore(t.ScrollY, line, width, viewH) + caretRow

	for above < m && t.ScrollY > 0 {
		r := t.lineRows(t.ScrollY-1, width)
		if above+r > viewH-1-m {
			break
		}
		above += r
		t.ScrollY--
	}

	// Rows that actually exist below the caret's row: the rest of its own
	// line, then following lines, counted only as far as the margin needs.
	below := len(starts) - 1 - caretRow
	for l := line + 1; below < m && l < t.Buffer.LineCount(); l++ {
		below += t.lineRows(l, width)
	}
	if below > m {
		below = m
	}
	for above > viewH-1-below && t.ScrollY < line {
		above -= t.lineRows(t.ScrollY, width)
		t.ScrollY++
	}
}
