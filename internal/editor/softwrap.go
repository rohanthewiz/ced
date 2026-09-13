// =============================================================================
// File: internal/editor/softwrap.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// softwrap.go is the editor half of soft wrap: a long line drawn across as
// many screen rows as it needs instead of running off the right edge
// behind a '›'. The buffer is never touched — a wrapped row is a DRAWING
// of one line, exactly as a markdown preview's row is a drawing of a
// passage — so saving, undo, the LSP sync and every column a language
// server reports keep meaning what they meant.
//
// House rules the shape follows:
//
//   - IT IS A VIEW FLAG PER TAB, NOT A MODE AND NOT A PREFERENCE. The
//     question it answers is "how do I want to look at THIS file right
//     now" — a prose README wants it, the Go file beside it does not — so
//     it lives beside mdView rather than in config.json.
//
//   - ScrollY STAYS A LINE INDEX. The viewport begins at the top of line
//     ScrollY; wrap changes how many rows each line below it costs, never
//     what the scroll offset counts. That is what keeps every consumer
//     above this layer that thinks in lines — the overflow markers'
//     counts, the session's stored scroll, the Find-all restore, the
//     wheel — correct without learning about rows. The price is one edge
//     case, stated rather than hidden: a single line taller than the whole
//     viewport shows only its first screenful, and a caret parked below
//     that has nowhere on screen to be drawn.
//
//   - ONE LAYOUT FUNCTION. Render, hit-testing, the caret's screen cell,
//     EnsureVisible, centering and Up/Down all ask wrapStarts through
//     wrapLayout, so a click can never land on a row the paint did not
//     draw. The markdown preview's MarkdownRows argument, one floor down.
//
//   - THE LAST CONTENT COLUMN IS NEVER WRITTEN INTO. Rows wrap one cell
//     short of the pane, for two reasons that happen to agree: a caret at
//     the end of an exactly-full row needs a cell to sit in, and the app's
//     vertical overflow marker (app/overflow.go) paints into that column
//     on the viewport's first and last row — where, wrapped, it would
//     otherwise cover a character of the code.
//
//   - WORD BOUNDARIES FIRST, CHARACTERS WHEN FORCED. A row breaks after
//     the last space or tab that fits, so prose reads as prose; a run with
//     no break opportunity (a URL, minified JS) is cut where the row ends.
//     The whitespace stays on the row it ends, so a row's runes plus the
//     next row's runes are always the line — no rune is dropped, which is
//     what lets a rune column map to exactly one (row, cell).
//
// Diagram — one 23-rune line in a 10-cell wrap width:
//
//	buffer:  "the quick brown foxtrot"
//	rows:    |the quick |   starts[0] = 0
//	         |brown     |   starts[1] = 10
//	         |foxtrot   |   starts[2] = 16
//
// A caret at col 10 draws at the head of row 1, not past the end of row 0:
// a column equal to a row's start belongs to that row. Only the end of the
// LAST row can hold the caret after its final rune.

package editor

import (
	"github.com/gdamore/tcell/v2"
)

// IsSoftWrap reports whether the tab draws long lines across several rows.
func (t *Tab) IsSoftWrap() bool {
	return t != nil && t.softWrap
}

// SetSoftWrap turns soft wrap on or off. The single write path, so the
// scroll reset and the caret re-reveal can't be forgotten by one surface.
//
// ScrollX is zeroed in both directions: wrapped, there is nothing to the
// right to scroll to, and unwrapping from a wrapped view restores the
// left edge the user was just reading from. cursorMoved is set so the
// next Render scrolls the caret into view under the NEW geometry — a
// caret that was on screen at one row per line may be well below the
// fold once the lines above it take several.
func (t *Tab) SetSoftWrap(on bool) {
	if t == nil || t.softWrap == on {
		return
	}
	t.softWrap = on
	t.ScrollX = 0
	t.cursorMoved = true
	// Off takes effect for the cached-width helpers at once; on waits for
	// the next render to measure a width, and until then they answer as
	// the unwrapped view the screen is still showing.
	if !on {
		t.wrapW = 0
	}
}

// wrapWidthFor is the row width wrapping uses in a viewW-wide render, or 0
// when the tab is not wrapping. One column short of the content area —
// see the "last content column" rule in the file header.
func (t *Tab) wrapWidthFor(viewW int) int {
	if !t.softWrap || t.IsImage() {
		return 0
	}
	w := viewW - t.gutterCols() - 1 - 1
	if w < 1 {
		w = 1
	}
	return w
}

// wrapStarts returns the rune index at which each display row of runes
// begins, for rows at most width cells wide. The first entry is always 0,
// so an empty line is one row and the result is never empty.
//
// Widths are measured in LINE-anchored visual columns (tab stops count
// from the start of the line, not the row) because that is how Render
// already expands tabs; measuring per row would give a tab a different
// width in the layout than on screen.
func wrapStarts(runes []rune, width int) []int {
	starts := []int{0}
	if width < 1 || len(runes) == 0 {
		return starts
	}
	// vis[i] is the visual column at which rune i begins; vis[len] is the
	// line's total width. Built once so every row-width question below is
	// a subtraction.
	vis := make([]int, len(runes)+1)
	for i, r := range runes {
		vis[i+1] = vis[i] + RuneVisualWidth(r, vis[i])
	}
	rowStart := 0
	breakAt := 0 // rune index just past the last space/tab on this row; 0 = none
	for i, r := range runes {
		// The `i > rowStart` guard is what stops a single rune wider than
		// the whole row (a tab in a 2-cell pane) from looping forever: it
		// gets a row of its own and overhangs it.
		for vis[i+1]-vis[rowStart] > width && i > rowStart {
			next := i
			if breakAt > rowStart && breakAt <= i {
				next = breakAt
			}
			starts = append(starts, next)
			rowStart = next
			breakAt = 0
		}
		if r == ' ' || r == '\t' {
			breakAt = i + 1
		}
	}
	return starts
}

// wrapRow returns which row of starts holds rune column col. A column
// equal to a row's start belongs to that row; the end of the line belongs
// to the last row.
func wrapRow(starts []int, col int) int {
	row := 0
	for i := 1; i < len(starts); i++ {
		if starts[i] > col {
			break
		}
		row = i
	}
	return row
}

// wrapRowEnd is the rune index one past the last rune of row r.
func wrapRowEnd(starts []int, r, lineLen int) int {
	if r+1 < len(starts) {
		return starts[r+1]
	}
	return lineLen
}

// wrapLayout returns the runes a line is DRAWN with and where each of its
// rows starts, at the given width. The runes include the ghost-text splice
// when a suggestion sits on this line, because Render wraps the spliced
// row — a layout measured without it would disagree with the paint about
// how many rows the cursor line takes, and every click below it would
// land one row off for as long as the suggestion was up. width 0 means
// "not wrapping": one row holding the whole line.
func (t *Tab) wrapLayout(line, width int) ([]rune, []int) {
	runes := t.Buffer.LineRunes(line)
	if t.Ghost != nil && t.Ghost.Pos.Line == line {
		runes, _ = t.ghostOverlay(line, runes, make([]tcell.Style, len(runes)), tcell.StyleDefault)
	}
	if width <= 0 {
		return runes, []int{0}
	}
	return runes, wrapStarts(runes, width)
}

// lineRows is how many display rows line occupies at width.
func (t *Tab) lineRows(line, width int) int {
	_, starts := t.wrapLayout(line, width)
	return len(starts)
}

// wrapRowsBefore counts the display rows from the top of line `from` down
// to (not including) line `to`, giving up once the count reaches limit —
// the answer past a viewport's height is "off screen", and walking a
// thousand lines to say so would put a whole-file cost inside a frame.
func (t *Tab) wrapRowsBefore(from, to, width, limit int) int {
	n := 0
	for l := from; l < to && n < limit; l++ {
		n += t.lineRows(l, width)
	}
	return n
}

// ensureVisibleWrapped is EnsureVisible's vertical rule in row units: pull
// ScrollY up when the caret's line is above the viewport, and push it down
// just far enough that the caret's ROW is on the last screen row when it
// has fallen below. Minimal scroll, the same promise the unwrapped rule
// makes, so a caret walking down a paragraph doesn't make the view jump.
func (t *Tab) ensureVisibleWrapped(width, viewH int) {
	t.ScrollX = 0
	if t.Cursor.Line < t.ScrollY {
		t.ScrollY = t.Cursor.Line
		return
	}
	_, starts := t.wrapLayout(t.Cursor.Line, width)
	caretRow := wrapRow(starts, t.Cursor.Col)
	if t.wrapRowsBefore(t.ScrollY, t.Cursor.Line, width, viewH)+caretRow < viewH {
		return
	}
	// Walk up from the caret's line, taking whole lines while they still
	// fit above the caret's row. A caret row that alone is past the
	// viewport (a line taller than the screen) leaves ScrollY on its own
	// line — the stated limit of line-indexed scrolling.
	top, used := t.Cursor.Line, caretRow+1
	for top > 0 {
		r := t.lineRows(top-1, width)
		if used+r > viewH {
			break
		}
		used += r
		top--
	}
	t.ScrollY = top
}

// centerOnCursorWrapped is CenterOnCursor in row units: as many whole
// lines above the caret's line as fit in half the view, counting the rows
// of the caret's own line that sit above the caret.
func (t *Tab) centerOnCursorWrapped(width, viewH int) {
	_, starts := t.wrapLayout(t.Cursor.Line, width)
	above := wrapRow(starts, t.Cursor.Col)
	top := t.Cursor.Line
	for top > 0 {
		r := t.lineRows(top-1, width)
		if above+r > viewH/2 {
			break
		}
		above += r
		top--
	}
	t.ScrollY = top
}

// moveVisualRows moves the caret by n display rows (negative = up),
// keeping its cell offset within the row. It is what Up and Down mean in
// a wrapped view: stepping by buffer line would jump a whole paragraph of
// prose at once, which is the one thing a user turning wrap on for a
// document is trying to get away from.
//
// The top and bottom rows of the file stay put, the unwrapped rule. A
// target column that would equal the NEXT row's start is pulled back one
// rune, since that column draws on the next row and the caret would
// appear to have moved two.
func (t *Tab) moveVisualRows(n, width int) {
	cur := t.Cursor
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	for ; n > 0; n-- {
		runes, starts := t.wrapLayout(cur.Line, width)
		row := wrapRow(starts, cur.Col)
		offset := LineVisualCol(runes, cur.Col) - LineVisualCol(runes, starts[row])

		line, target := cur.Line, row+step
		if target < 0 {
			if line == 0 {
				break
			}
			line--
			runes, starts = t.wrapLayout(line, width)
			target = len(starts) - 1
		} else if target >= len(starts) {
			if line >= t.Buffer.LineCount()-1 {
				break
			}
			line++
			runes, starts = t.wrapLayout(line, width)
			target = 0
		}

		s := starts[target]
		e := wrapRowEnd(starts, target, len(runes))
		col := RuneColAtVisual(runes, LineVisualCol(runes, s)+offset)
		if col < s {
			col = s
		}
		if target < len(starts)-1 && col >= e && e > s {
			col = e - 1
		}
		if col > e {
			col = e
		}
		cur = Position{Line: line, Col: col}
	}
	t.Cursor = t.Buffer.Clamp(cur)
}

// posScreenCellWrapped is PosScreenCell for a wrapped view: the row is the
// rows above p's line plus p's row within it, the cell is p's offset from
// the start of that row.
func (t *Tab) posScreenCellWrapped(p Position, width, h int) (dx, dy int, ok bool) {
	if p.Line < t.ScrollY {
		return 0, 0, false
	}
	runes, starts := t.wrapLayout(p.Line, width)
	row := wrapRow(starts, p.Col)
	dy = t.wrapRowsBefore(t.ScrollY, p.Line, width, h) + row
	if dy >= h {
		return 0, 0, false
	}
	dx = t.gutterCols() + 1 + LineVisualCol(runes, p.Col) - LineVisualCol(runes, starts[row])
	return dx, dy, true
}

// hitTestWrapped is HitTest for a wrapped view: find the line and row the
// screen row falls on, then the column within that row. A click past the
// end of a row that is not the line's last lands on the row's final rune,
// because the column one past it draws at the head of the next row.
func (t *Tab) hitTestWrapped(localX, localY, width int) (Position, bool) {
	row := 0
	for line := t.ScrollY; line < t.Buffer.LineCount(); line++ {
		runes, starts := t.wrapLayout(line, width)
		if localY >= row+len(starts) {
			row += len(starts)
			continue
		}
		r := localY - row
		s := starts[r]
		e := wrapRowEnd(starts, r, len(runes))
		contentX := t.gutterCols() + 1
		col := s
		if localX >= contentX {
			col = RuneColAtVisual(runes, LineVisualCol(runes, s)+(localX-contentX))
			if r < len(starts)-1 && col >= e && e > s {
				col = e - 1
			}
			if col > e {
				col = e
			}
		}
		// The layout may include a ghost splice; a column inside or past
		// it has no buffer rune, so it clamps to the line's real end.
		if n := len(t.Buffer.LineRunes(line)); col > n {
			col = n
		}
		return Position{Line: line, Col: col}, true
	}
	return Position{}, false
}

// LastVisibleLine is the index of the last buffer line that starts on
// screen in a viewH-row viewport, as of the last render's width. Unwrapped
// that is simply ScrollY+viewH-1; wrapped, the lines above it may each
// have taken several rows. It exists for the overflow markers, which count
// what lies below the viewport and must not count a line that is plainly
// on screen as "below".
func (t *Tab) LastVisibleLine(viewH int) int {
	if t.wrapW <= 0 {
		return t.ScrollY + viewH - 1
	}
	row, line := 0, t.ScrollY
	for ; line < t.Buffer.LineCount(); line++ {
		row += t.lineRows(line, t.wrapW)
		if row >= viewH {
			return line
		}
	}
	// The buffer ran out first: the rest of the viewport is blank rows,
	// so the answer is the unwrapped formula's (past EOF, which the
	// marker's count floors at zero).
	return line + (viewH - row) - 1
}
