// =============================================================================
// File: internal/editor/syntax.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// syntax.go decides WHEN a tab re-runs Chroma, and keeps the cached style
// grid paintable in the meantime.
//
// The problem it solves: Highlight is O(file). It tokenises the whole
// buffer and allocates a per-rune style grid, and Render used to call it
// on every frame where StyleStale was set — which every buffer mutation
// sets. So a single typed rune re-lexed the entire file. Measured on
// ced's own internal/app/app.go (3831 lines): ~70ms and 36MB of garbage
// PER KEYSTROKE, which is ~14fps typing before any SSH latency is added.
//
// Chroma has no incremental API, so the fix is not incremental lexing —
// it's asking for the re-lex less often, and making the answer we paint
// from in the meantime indistinguishable from a fresh one:
//
//	 intra-line edit          structural edit
//	(typing, backspace)     (Enter, paste, undo,
//	       │                 line ops, reload…)
//	       ▼                        │
//	patch the grid                  │
//	in place, defer                 ▼
//	       │                  re-lex on the
//	       │                  next render
//	       ▼
//	── SyntaxSettle idle ──►  re-lex on the next render
//
// Two rules make that safe:
//
//   - ONLY intra-line edits defer. Anything that changes the line
//     structure — Enter, a multi-line paste, undo, a line op, a reload —
//     re-lexes immediately, because a grid whose rows no longer line up
//     with the buffer's rows would repaint the whole screen below the
//     edit in the wrong colors. That boundary is free: it's the same
//     "structural" cut the undo grouping already makes, and it means the
//     deferral covers exactly the keystrokes that arrive dozens per
//     second and skips the ones that arrive once a line.
//
//   - An intra-line edit PATCHES the row it touched (stylesAfterInsert /
//     stylesAfterDelete splice the row's style slice by the same number
//     of runes the buffer changed). Without that, everything after the
//     caret would smear one column sideways for the length of the settle
//     window. Newly typed runes inherit the style of their left
//     neighbour, which is what you want: a character typed inside a
//     string stays string-colored until the real lex confirms it.
//
// The settle timer itself lives in the app layer (app/syntax.go) — the
// editor has no event loop to wake itself from. It arms only while a tab
// is actually waiting, per the standing rule that an event-driven editor
// must never hold a repeating timer while idle.
package editor

import (
	"strings"
	"time"
	"unicode/utf8"
)

// SyntaxSettle is how long the buffer must be quiet before a deferred
// re-lex runs. 150ms is under the threshold where a color correction
// reads as a change rather than as lag, and comfortably longer than the
// gap between keystrokes in a typing burst — so a burst of any length
// pays for exactly one re-lex, at its end.
//
// A package var rather than a const purely so tests can collapse the
// window and assert the settled state without sleeping. It is not a user
// setting and nothing in the editor writes it.
var SyntaxSettle = 150 * time.Millisecond

// MaxHighlightBytes is the file size above which a tab is opened with
// highlighting off entirely. Cost scales with file size (~0.5ms per KB),
// so past this point even one re-lex per settle is a visible freeze —
// 512KB is already ~270ms, and a 1.5MB file measured at 1.7 SECONDS.
// Plain text that scrolls smoothly beats colored text that stutters.
const MaxHighlightBytes = 512 << 10

// InvalidateStyles marks the cached grid unusable and requires a full
// re-lex on the next render. This is the DEFAULT contract for anything
// that mutates the buffer — the deferred path below is the opt-in, taken
// only by edits that provably keep the grid's rows aligned with the
// buffer's. Exported because the theme layer invalidates every open tab
// on a palette switch.
func (t *Tab) InvalidateStyles() {
	t.StyleStale = true
	t.styleDefer = false
}

// deferStyles marks the grid stale but still paintable: the row patch has
// already kept it aligned, so Render can keep using it until the buffer
// has been quiet for SyntaxSettle.
func (t *Tab) deferStyles() {
	t.StyleStale = true
	t.styleDefer = true
	t.lastEditAt = time.Now()
}

// stylesAfterInsert mirrors an insertion of s at at into the cached grid.
// A multi-line insert (Enter, a multi-line paste) SPLITS the row and
// defers too (N-128): it used to invalidate, and the next frame re-lexed
// the whole file on the main loop — 113 ms per Enter on a 256KB file.
func (t *Tab) stylesAfterInsert(at Position, s string) {
	if s == "" {
		return
	}
	var ok bool
	if strings.ContainsRune(s, '\n') {
		ok = t.patchRowsInsert(at, s)
	} else {
		ok = t.patchRowInsert(at.Line, at.Col, utf8.RuneCountInString(s))
	}
	if !ok {
		t.InvalidateStyles()
		return
	}
	t.deferStyles()
}

// stylesAfterDelete mirrors the removal of the range [from, to) into the
// cached grid. A range spanning lines JOINS the rows and defers (N-128).
func (t *Tab) stylesAfterDelete(from, to Position) {
	var ok bool
	if from.Line != to.Line {
		ok = t.patchRowsDelete(from, to)
	} else {
		ok = t.patchRowDelete(from.Line, from.Col, to.Col-from.Col)
	}
	if !ok {
		t.InvalidateStyles()
		return
	}
	t.deferStyles()
}

// patchRowsInsert mirrors a MULTI-LINE insertion of s at at: the row at
// at.Line splits at at.Col, the head keeps its classes and gains the
// first inserted line's, every further inserted line gets a row of its
// own, and the tail keeps its classes at the end of the last one.
//
//	before:  row L = [ head | tail ]          s = "x\ny\nz"
//	after:   row L   = [ head | x ]
//	         row L+1 = [ y ]
//	         row L+2 = [ z | tail ]
//
// Inserted runes take the class to the left of the insertion point, as
// typed text does in patchRowInsert — a newline typed inside a string
// leaves both halves string-coloured until the settle re-lex says
// otherwise. Reports false when the grid can't be patched (no grid, the
// row is out of range); the caller then invalidates.
func (t *Tab) patchRowsInsert(at Position, s string) bool {
	if t.Classes == nil || at.Line < 0 || at.Line >= len(t.Classes) {
		return false
	}
	row := t.Classes[at.Line]
	if at.Col < 0 || at.Col > len(row) {
		return false
	}
	fill := SynPlain
	switch {
	case at.Col > 0:
		fill = row[at.Col-1]
	case len(row) > 0:
		fill = row[0]
	}
	parts := strings.Split(s, "\n")
	rows := make([][]SynClass, len(parts))
	for k, part := range parts {
		n := utf8.RuneCountInString(part)
		var r []SynClass
		switch k {
		case 0:
			r = make([]SynClass, 0, at.Col+n)
			r = append(r, row[:at.Col]...)
		case len(parts) - 1:
			r = make([]SynClass, 0, n+len(row)-at.Col)
		}
		for range n {
			r = append(r, fill)
		}
		if k == len(parts)-1 {
			r = append(r, row[at.Col:]...)
		}
		rows[k] = r
	}
	t.Classes = spliceRows(t.Classes, at.Line, at.Line+1, rows)
	return true
}

// patchRowsDelete mirrors the removal of a range that spans lines: the
// head of from.Line's row and the tail of to.Line's row join into one,
// and the rows between go. Columns are clamped to what the rows hold —
// a row may be shorter than its line (see patchRowInsert).
func (t *Tab) patchRowsDelete(from, to Position) bool {
	if t.Classes == nil || from.Line < 0 || to.Line >= len(t.Classes) || from.Line > to.Line {
		return false
	}
	a, b := t.Classes[from.Line], t.Classes[to.Line]
	fc, tc := min(max(from.Col, 0), len(a)), min(max(to.Col, 0), len(b))
	joined := make([]SynClass, 0, fc+len(b)-tc)
	joined = append(joined, a[:fc]...)
	joined = append(joined, b[tc:]...)
	t.Classes = spliceRows(t.Classes, from.Line, to.Line+1, [][]SynClass{joined})
	return true
}

// spliceRows replaces grid[lo:hi] with rows, in a fresh slice: the old
// one may still be referenced by an async lex's caller, never mutated.
func spliceRows(grid [][]SynClass, lo, hi int, rows [][]SynClass) [][]SynClass {
	out := make([][]SynClass, 0, len(grid)-(hi-lo)+len(rows))
	out = append(out, grid[:lo]...)
	out = append(out, rows...)
	return append(out, grid[hi:]...)
}

// patchRowInsert widens row `line` by n class entries at col, filling them
// with the style of the rune to the left of the insertion point so typed
// text inherits the color of what it's being typed into. Reports whether
// the patch was possible; false means the caller must re-lex instead.
//
// A row with no cached styles at all is left alone and still counts as
// patched: Render already falls back to the theme's base style for any
// rune past the end of a cached row, which is the correct rendering for
// text nothing has lexed yet.
func (t *Tab) patchRowInsert(line, col, n int) bool {
	if t.Classes == nil || line < 0 || line >= len(t.Classes) || n <= 0 {
		return false
	}
	row := t.Classes[line]
	if col < 0 || col > len(row) {
		return false
	}
	if len(row) == 0 {
		return true
	}
	fill := row[0]
	if col > 0 {
		fill = row[col-1]
	}
	out := make([]SynClass, 0, len(row)+n)
	out = append(out, row[:col]...)
	for range n {
		out = append(out, fill)
	}
	t.Classes[line] = append(out, row[col:]...)
	return true
}

// patchRowDelete drops n style entries starting at col from row `line`.
// The count is clamped to what the row actually holds — a row can legally
// be shorter than its line (see patchRowInsert's empty-row case), and a
// short row is not a reason to throw the whole grid away.
func (t *Tab) patchRowDelete(line, col, n int) bool {
	if t.Classes == nil || line < 0 || line >= len(t.Classes) || n <= 0 {
		return false
	}
	row := t.Classes[line]
	if col < 0 || col > len(row) {
		return false
	}
	end := min(col+n, len(row))
	t.Classes[line] = append(row[:col], row[end:]...)
	return true
}

// needsRelex reports whether Render should re-run Chroma this frame.
// Deferred staleness waits out the settle window; everything else — an
// invalidation, or a tab that has no grid to paint from yet — is due now.
func (t *Tab) needsRelex() bool {
	if !t.StyleStale || t.SyntaxOff || t.IsImage() {
		return false
	}
	if !t.styleDefer || t.Classes == nil {
		return true
	}
	return time.Since(t.lastEditAt) >= SyntaxSettle
}

// InlineLexBytes is the largest buffer Render re-lexes itself (N-128).
// Chroma costs ~0.5-0.9 ms per KB, so 32KB is a frame or two; anything
// bigger is lexed OFF the main loop (BeginAsyncLex / AdoptClasses, run by
// the app) and the frame paints the patched — or, after a structural edit
// no patch covers, the previous — grid until the answer lands. A var only
// so tests can move the line.
var InlineLexBytes = 32 << 10

// lexInline reports whether this tab's re-lex may run inside Render. The
// byte count is summed from the lines (O(lines), only asked while a
// re-lex is due).
func (t *Tab) lexInline() bool {
	n := 0
	for _, ln := range t.Buffer.Lines {
		n += len(ln) + 1
		if n > InlineLexBytes {
			return false
		}
	}
	return true
}

// BeginAsyncLex hands the app a re-lex to run off the loop: the tab's
// path, a snapshot of its text, and the revision that text is. ok is
// false when nothing is due, the buffer is small enough for Render to
// lex inline, or a lex for this tab is already out — ONE at a time, so a
// burst of undos on a big file can't stack 200ms lexes; the answer's
// arrival (AdoptClasses) clears the way for the next.
func (t *Tab) BeginAsyncLex() (path, src string, rev int, ok bool) {
	if t.lexing || !t.needsRelex() || t.lexInline() {
		return "", "", 0, false
	}
	t.lexing = true
	return t.Path, t.Buffer.String(), t.EditRev, true
}

// AdoptClasses installs an async lex's grid if the buffer is still the
// text it was computed from (rev == EditRev), and reports whether it did.
// Either way the tab is free to start another; the caller asks
// BeginAsyncLex again, which says whether one is still due.
func (t *Tab) AdoptClasses(rev int, grid [][]SynClass) bool {
	t.lexing = false
	if rev != t.EditRev || t.SyntaxOff {
		return false
	}
	t.Classes = grid
	t.StyleStale = false
	t.styleDefer = false
	return true
}

// SyntaxSettleWait returns how long until a deferred re-lex comes due, or
// 0 when nothing is waiting on the clock. The app arms its settle timer
// from this. Zero covers the already-due case too: the redraw that
// follows every dispatch will re-lex, so no wake-up is needed for it.
func (t *Tab) SyntaxSettleWait() time.Duration {
	if !t.StyleStale || t.SyntaxOff || !t.styleDefer || t.Classes == nil {
		return 0
	}
	if d := SyntaxSettle - time.Since(t.lastEditAt); d > 0 {
		return d
	}
	return 0
}
