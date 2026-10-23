// =============================================================================
// File: internal/app/diffselect.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diffselect.go is drag-to-select over a diff PANE (diffview.go). The
// panels capture the mouse, so the terminal's own drag-select never
// reaches their text — a pane that wants its text copyable has to
// select it itself, exactly as the chat transcript does
// (copilot_chat.go). The git log's commit detail is the first surface
// to arm it; the state and the painter live on the shared layer so any
// other pane can opt in with a press/drag/release of its own.
//
// Design choices:
//
//   - SELECTION LIVES IN DISPLAY-ROW SPACE: (index into diffPane.rows,
//     rune column of that row's DISPLAY text). Scrolling therefore never
//     shifts the highlight, and because the state sits on the diffPane,
//     the layout cache's own rebuild (new commit, new text, a split
//     toggle) drops it — a selection over text that is no longer the
//     text on screen can't survive by accident.
//
//   - ONE SIDE PER SELECTION in split mode. A paired row shows two
//     different lines; dragging "across" both halves has no single
//     reading. The side the press lands on is fixed for the whole drag,
//     the way a column selection works, and full rows (headers, @@,
//     metadata) that the drag passes through are copied whole:
//
//	  press here ─┐                    split row:  left │ right
//	              ▼                                  ▲
//	   10 a      │10 a          selection stays on   │ the LEFT side
//	   11 b      │11 B          even if the pointer wanders right
//
//     A hole on the chosen side (a paired row with no line there) is
//     skipped rather than copied as a blank line — it is not a line of
//     either file.
//
//   - WHAT YOU SEE IS WHAT YOU COPY, minus clipping. The text is the
//     display form (tabs expanded at the hunk's prefix width, the same
//     string the painter draws), so a cell's column IS a rune offset with
//     no remapping. Unified rows keep their +/-/space prefix (it's a
//     diff); split sides drop it and the line-number gutter, which makes
//     split mode the clean way to lift code. A clipped row is NOT cut at
//     the pane edge: a row the drag passes through copies whole, and a
//     pointer on the … cell (or past it) means "to the end of the line".
//
//   - ONE GEOMETRY: diffSelTextRect names where a row's text starts on
//     screen and how many cells it gets; the hit-test, the painter and
//     the tests all ask it, and it mirrors drawDiffRows/drawDiffSide
//     (pinned by TestDiffSel_SplitRectMatchesPaintedText).

package app

import "strings"

// diffSelPos is one end of a pane selection: a display row and a rune
// column within that row's selectable text.
type diffSelPos struct {
	row, col int
}

// before reports whether p sits strictly ahead of q in reading order.
func (p diffSelPos) before(q diffSelPos) bool {
	return p.row < q.row || (p.row == q.row && p.col < q.col)
}

// diffSel is a pane's drag selection. active is set by the press even
// though nothing is selected until the pointer moves — a plain click
// arms an anchor and selects nothing (diffSelRange reports it empty).
type diffSel struct {
	active bool
	// right is the split side the press landed on; meaningless in
	// unified mode and on full rows, where there is only one text.
	right        bool
	anchor, head diffSelPos
}

// clearSel drops the pane's selection. Called by Esc, by the cache
// rebuild in layout, and by a surface that wants the highlight gone.
func (p *diffPane) clearSel() { p.sel = diffSel{} }

// diffSelRange normalizes anchor/head into reading order, reporting
// false when there is no selection or it is empty.
func (p *diffPane) diffSelRange() (start, end diffSelPos, ok bool) {
	if !p.sel.active {
		return diffSelPos{}, diffSelPos{}, false
	}
	start, end = p.sel.anchor, p.sel.head
	if end.before(start) {
		start, end = end, start
	}
	if start == end {
		return start, end, false
	}
	return start, end, true
}

// diffRowSelText returns the selectable text of row r for the given
// side, and ok=false for a hole (a paired row with no line on that
// side). Full rows use the painter's tab expansion at the hunk's prefix
// width; split sides use drawDiffSide's form with the one-byte prefix
// dropped.
func diffRowSelText(lines []string, r diffRow, right bool) (string, bool) {
	if r.full {
		if r.left < 0 || r.left >= len(lines) {
			return "", false
		}
		line := lines[r.left]
		if strings.ContainsRune(line, '\t') {
			return diffDisplayLineCols(line, diffPrefixColsAt(lines, r.left)), true
		}
		return diffDisplayLine(line), true
	}
	idx := r.left
	if right {
		idx = r.right
	}
	if idx < 0 || idx >= len(lines) {
		return "", false
	}
	line := diffDisplayLine(lines[idx])
	if line != "" {
		line = line[1:] // the prefix is one ASCII byte
	}
	return line, true
}

// diffSelTextRect returns the screen column a row's selectable text
// starts at and how many cells it gets under paint d. It mirrors
// drawDiffRows (full rows span the pane) and drawDiffSide (the split
// halves, minus the line-number gutter when the half is wide enough to
// carry one) — keep the three in step.
func (p *diffPane) diffSelTextRect(r diffRow, right bool, d diffPaint) (x, w int) {
	if r.full {
		return d.x, d.w
	}
	lw := (d.w - 1) / 2
	x, w = d.x, lw
	if right {
		x, w = d.x+lw+1, d.w-lw-1
	}
	if w >= p.numW+6 {
		x += p.numW + 1
		w -= p.numW + 1
	}
	return x, max(w, 0)
}

// diffSelPosAt maps a screen cell to a selection position, clamped so a
// drag that leaves the pane still lands somewhere sane: above the pane
// is the start of the top visible row, below it the end of the bottom
// one (or of the last row, on a diff shorter than the pane).
func (p *diffPane) diffSelPosAt(lines []string, rows []diffRow, d diffPaint, right bool, x, y int) diffSelPos {
	if len(rows) == 0 || d.n <= 0 {
		return diffSelPos{}
	}
	want := d.scroll + (y - d.y)
	row := clampInt(want, d.scroll, d.scroll+d.n-1)
	row = clampInt(row, 0, len(rows)-1)
	text, _ := diffRowSelText(lines, rows[row], right)
	n := runeLen(text)
	switch {
	case want < row:
		return diffSelPos{row: row}
	case want > row:
		return diffSelPos{row: row, col: n}
	}
	tx, tw := p.diffSelTextRect(rows[row], right, d)
	col := max(x-tx, 0)
	// A clipped row's last cell is the … — standing on it (or past it)
	// means the hidden tail too, or the end of a long line could never
	// be selected without scrolling a pane that has no horizontal scroll.
	if n > tw && col >= tw-1 {
		col = n
	}
	return diffSelPos{row: row, col: min(col, n)}
}

// diffSelRowSpan returns the [from, to) rune columns of row k inside
// the selection, given the row's rune count; (0, 0) when none are.
func (p *diffPane) diffSelRowSpan(k, n int) (from, to int) {
	start, end, ok := p.diffSelRange()
	if !ok || k < start.row || k > end.row {
		return 0, 0
	}
	from, to = 0, n
	if k == start.row {
		from = min(start.col, n)
	}
	if k == end.row {
		to = min(end.col, n)
	}
	return min(from, to), to
}

// diffSelStart arms a fresh, empty selection at a press: the side is
// decided here (by which split half the pointer is over) and kept for
// the whole drag. Reports whether the press landed on the pane's rows.
func (p *diffPane) diffSelStart(lines []string, rows []diffRow, d diffPaint, x, y int) bool {
	if y < d.y || y >= d.y+d.n || x < d.x || x >= d.x+d.w || len(rows) == 0 {
		return false
	}
	right := false
	if p.split {
		right = x >= d.x+(d.w-1)/2+1
	}
	pos := p.diffSelPosAt(lines, rows, d, right, x, y)
	p.sel = diffSel{active: true, right: right, anchor: pos, head: pos}
	return true
}

// diffSelExtend moves the selection's head to the pointer — the drag
// continuation, which also runs for events outside the pane.
func (p *diffPane) diffSelExtend(lines []string, rows []diffRow, d diffPaint, x, y int) {
	if !p.sel.active {
		return
	}
	p.sel.head = p.diffSelPosAt(lines, rows, d, p.sel.right, x, y)
}

// diffSelText renders the selection back to text, one display row per
// line — the rows the user actually dragged across. Holes on the
// chosen side are skipped (see the file comment).
func (p *diffPane) diffSelText(lines []string, rows []diffRow) string {
	start, end, ok := p.diffSelRange()
	if !ok {
		return ""
	}
	var parts []string
	for k := start.row; k <= end.row && k < len(rows); k++ {
		text, ok := diffRowSelText(lines, rows[k], p.sel.right)
		if !ok {
			continue
		}
		runes := []rune(text)
		from, to := p.diffSelRowSpan(k, len(runes))
		parts = append(parts, string(runes[from:to]))
	}
	return strings.Join(parts, "\n")
}

// paintDiffSelection re-backgrounds the selected cells already painted
// by drawDiffRows with the theme's selection colour, keeping each
// cell's glyph and foreground — an overlay rather than a branch in the
// row painters, so emphasis, gutters and the ▸ marker stay untouched.
// A fully-selected EMPTY row inside the range lights its first cell, so
// a blank line that will be copied doesn't look skipped.
func (a *App) paintDiffSelection(lines []string, p *diffPane, rows []diffRow, d diffPaint) {
	start, end, ok := p.diffSelRange()
	if !ok {
		return
	}
	for row := 0; row < d.n; row++ {
		k := d.scroll + row
		if k < start.row || k > end.row || k >= len(rows) {
			continue
		}
		text, ok := diffRowSelText(lines, rows[k], p.sel.right)
		if !ok {
			continue
		}
		x, w := p.diffSelTextRect(rows[k], p.sel.right, d)
		n := runeLen(text)
		from, to := p.diffSelRowSpan(k, n)
		// Cells past the clip are not on screen; the … cell stands in
		// for the hidden tail, so a span reaching past it lights it.
		if n > w {
			to = min(to, w)
		}
		if n == 0 && k < end.row {
			to = 1
		}
		for c := from; c < min(to, w); c++ {
			mainc, comb, st, _ := a.screen.GetContent(x+c, d.y+row)
			a.screen.SetContent(x+c, d.y+row, mainc, comb, st.Background(a.theme.Selection))
		}
	}
}
