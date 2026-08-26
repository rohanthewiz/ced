// =============================================================================
// File: internal/app/chatcomposer.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// chatcomposer.go is the chat panel's multi-line input widget — textField
// grown a second dimension, and ONLY for the chat prompt. Every other
// single-line input in the editor stays on textField: a prompt modal, the
// find bar and the finder have no meaning for a newline, and a widget
// that can hold one must not be reachable from surfaces that would send
// it somewhere expecting one line.
//
// The design constraints, in order:
//
//   - The value is ONE rune slice with '\n' separators, not a line list.
//     The composer's whole lifetime is "editing a few lines of prose";
//     caret motion, splices and history round-trips are all simpler on a
//     flat slice, and there is no per-line state (no styles, no undo) to
//     justify a Buffer.
//   - Display rows HARD-wrap at the field width, the find-bar/signature
//     argument: only an exact rune-per-column mapping lets a caret index
//     become a (row, col) by arithmetic, and lets a click become a caret
//     index the same way. A word wrapper drops and merges spaces, so
//     every position past the first break would be a guess. The wrapped
//     transcript is prose being READ; this is text being EDITED.
//   - Wrapping is derived on demand from (value, width), never cached —
//     the chatRows rule one floor up. A panel resize re-wraps for free
//     and there is no dirty flag to forget.
//   - Enter is NOT handled here (textField's contract): send-vs-newline
//     is the caller's policy, so the caller routes Enter and calls
//     insertNewline for the chords that mean "break the line".
package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

type chatComposer struct {
	value  []rune
	cursor int // rune index into value
	scroll int // first visible display row when taller than the window
}

// newChatComposer builds a composer pre-filled with initial, caret at
// the end — the newTextField convention, so recalling a history entry
// lets the user immediately append. The seed is sanitized so a
// multi-line history entry round-trips and a CRLF from anywhere never
// enters the value.
func newChatComposer(initial string) chatComposer {
	v := []rune(composerSanitize(initial))
	return chatComposer{value: v, cursor: len(v)}
}

// String returns the composer's current value, newlines included.
func (c *chatComposer) String() string { return string(c.value) }

// composerSanitize folds arbitrary text into what the composer can hold
// and render: CRLF and lone CR become '\n' (a break survives, Windows
// text doesn't double-break), tabs become one space (the compactLine
// rule — a tab has no fixed width, and the composer's caret math needs
// rune == column), and the remaining control runes are dropped.
func composerSanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevCR := false
	for _, r := range s {
		switch {
		case r == '\r':
			b.WriteRune('\n')
		case r == '\n':
			if !prevCR {
				b.WriteRune('\n')
			}
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// Unrenderable; drop it.
		default:
			b.WriteRune(r)
		}
		prevCR = r == '\r'
	}
	return b.String()
}

// lineBounds returns the [start, end) rune indices of the LOGICAL line
// the caret sits on — what Home and End mean in a multi-line field.
func (c *chatComposer) lineBounds() (start, end int) {
	start, end = 0, len(c.value)
	for i := c.cursor - 1; i >= 0; i-- {
		if c.value[i] == '\n' {
			start = i + 1
			break
		}
	}
	for i := c.cursor; i < len(c.value); i++ {
		if c.value[i] == '\n' {
			end = i
			break
		}
	}
	return start, end
}

// rows derives the display rows for width w: each element is the
// [start, end) rune span of one row, the terminating '\n' (when there is
// one) belonging to no row. An empty logical line still yields a row —
// paragraph breaks must survive — so an empty composer is one empty row.
func (c *chatComposer) rows(w int) [][2]int {
	if w < 1 {
		w = 1
	}
	var out [][2]int
	ls := 0
	for i := 0; i <= len(c.value); i++ {
		if i < len(c.value) && c.value[i] != '\n' {
			continue
		}
		if i == ls {
			out = append(out, [2]int{ls, ls})
		}
		for s := ls; s < i; s += w {
			out = append(out, [2]int{s, min(s+w, i)})
		}
		ls = i + 1
	}
	return out
}

// rowCount is how many display rows the value occupies at width w.
func (c *chatComposer) rowCount(w int) int { return len(c.rows(w)) }

// caretRowCol maps the caret index to its display (row, col) at width w.
// A caret exactly on a wrap boundary belongs to the FOLLOWING row at
// column 0 — the row a typed rune would land on — while a caret at the
// end of a logical line stays on that line's last row (col may equal the
// width there, one past the last cell, the textField caret convention).
func (c *chatComposer) caretRowCol(w int) (row, col int) {
	rows := c.rows(w)
	for r, sp := range rows {
		if c.cursor < sp[0] || c.cursor > sp[1] {
			continue
		}
		atLineEnd := sp[1] == len(c.value) || c.value[sp[1]] == '\n'
		if c.cursor < sp[1] || atLineEnd {
			return r, c.cursor - sp[0]
		}
	}
	// Unreachable for a valid caret; clamp to the last row defensively.
	last := len(rows) - 1
	return last, rows[last][1] - rows[last][0]
}

// moveVertical moves the caret delta display rows (±1 from the arrow
// keys), reporting false when the caret is already on the edge row — the
// caller's signal to fall back to prompt history, which is what Up/Down
// meant when the composer was one line tall and still mean at its edges.
// The column clamps to the target row's length; no goal column is kept —
// the composer is a few short lines, not a code buffer.
func (c *chatComposer) moveVertical(delta, w int) bool {
	rows := c.rows(w)
	row, col := c.caretRowCol(w)
	target := row + delta
	if target < 0 || target >= len(rows) {
		return false
	}
	sp := rows[target]
	c.cursor = sp[0] + min(col, sp[1]-sp[0])
	return true
}

// handleKey applies one keystroke of plain editing: caret motion,
// Backspace/Delete (which join lines when they consume a '\n'), and
// printable rune insertion. Enter and Esc are deliberately not handled —
// the textField contract: those mean policy, and the caller owns policy.
// Returns handled/edited exactly as textField does.
func (c *chatComposer) handleKey(ev *tcell.EventKey) (handled, edited bool) {
	switch ev.Key() {
	case tcell.KeyLeft:
		if c.cursor > 0 {
			c.cursor--
		}
		return true, false
	case tcell.KeyRight:
		if c.cursor < len(c.value) {
			c.cursor++
		}
		return true, false
	case tcell.KeyHome:
		c.cursor, _ = c.lineBounds()
		return true, false
	case tcell.KeyEnd:
		_, c.cursor = c.lineBounds()
		return true, false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if c.cursor > 0 {
			c.value = append(c.value[:c.cursor-1], c.value[c.cursor:]...)
			c.cursor--
			return true, true
		}
		return true, false
	case tcell.KeyDelete:
		if c.cursor < len(c.value) {
			c.value = append(c.value[:c.cursor], c.value[c.cursor+1:]...)
			return true, true
		}
		return true, false
	case tcell.KeyRune:
		r := ev.Rune()
		if r < 0x20 {
			return true, false
		}
		c.spliceRunes([]rune{r})
		return true, true
	}
	return false, false
}

// insertNewline splices a line break at the caret — the caller's verb
// for the newline chords (Alt+Enter and friends), since handleKey never
// sees Enter.
func (c *chatComposer) insertNewline() {
	c.spliceRunes([]rune{'\n'})
}

// insertString splices s in at the caret, sanitized but with its line
// breaks KEPT — the paste counterpart to handleKey, one splice per
// paste. This is the half of flattenPaste's old job that survives the
// multi-line composer: control noise still can't enter the value, but a
// pasted snippet now arrives shaped as it was copied.
func (c *chatComposer) insertString(s string) {
	add := []rune(composerSanitize(s))
	if len(add) == 0 {
		return
	}
	c.spliceRunes(add)
}

// spliceRunes is the one insertion primitive: add lands at the caret and
// the caret lands after it.
func (c *chatComposer) spliceRunes(add []rune) {
	next := make([]rune, 0, len(c.value)+len(add))
	next = append(next, c.value[:c.cursor]...)
	next = append(next, add...)
	next = append(next, c.value[c.cursor:]...)
	c.value = next
	c.cursor += len(add)
}

// clickAt moves the caret to the rune under a click at screen (x, y),
// given the composer text's origin (x0, y0) and width. Out-of-band rows
// clamp to the nearest row and a click past a row's end parks the caret
// at the row's end — the textField behavior, one dimension up.
func (c *chatComposer) clickAt(x0, y0, w, x, y int) {
	rows := c.rows(w)
	row := c.scroll + (y - y0)
	if row < 0 {
		row = 0
	}
	if last := len(rows) - 1; row > last {
		row = last
	}
	sp := rows[row]
	col := x - x0
	if col < 0 {
		col = 0
	}
	if n := sp[1] - sp[0]; col > n {
		col = n
	}
	c.cursor = sp[0] + col
}

// adjustScroll slides the vertical window so the caret's row stays
// visible within h display rows — adjustScroll's contract, one
// dimension up.
func (c *chatComposer) adjustScroll(w, h int) {
	if h < 1 {
		c.scroll = 0
		return
	}
	row, _ := c.caretRowCol(w)
	if row < c.scroll {
		c.scroll = row
	}
	if row >= c.scroll+h {
		c.scroll = row - h + 1
	}
	if max := c.rowCount(w) - h; c.scroll > max {
		c.scroll = max
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

// draw renders the visible window of rows at (x, y), w columns by h
// rows, and places the terminal caret when showCaret is set. The caller
// clears the band first (the panel row-clear convention), so only the
// runes are painted here.
func (c *chatComposer) draw(scr tcell.Screen, x, y, w, h int, style tcell.Style, showCaret bool) {
	if w < 1 || h < 1 {
		return
	}
	c.adjustScroll(w, h)
	rows := c.rows(w)
	for vr := 0; vr < h; vr++ {
		idx := c.scroll + vr
		if idx >= len(rows) {
			break
		}
		sp := rows[idx]
		for i, r := range c.value[sp[0]:sp[1]] {
			scr.SetContent(x+i, y+vr, r, nil, style)
		}
	}
	if !showCaret {
		return
	}
	row, col := c.caretRowCol(w)
	if row >= c.scroll && row < c.scroll+h {
		scr.ShowCursor(x+col, y+(row-c.scroll))
	}
}
