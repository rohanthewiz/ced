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
//	    intra-line edit          structural edit
//	   (typing, backspace)     (Enter, paste, undo,
//	          │                 line ops, reload…)
//	          ▼                        │
//	   patch the grid                  │
//	   in place, defer                 ▼
//	          │                  re-lex on the
//	          │                  next render
//	          ▼
//	   ── SyntaxSettle idle ──►  re-lex on the next render
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

	"github.com/gdamore/tcell/v2"
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
// Multi-line inserts fall back to a full re-lex — see the file comment
// for why the line-structure boundary is where deferral stops.
func (t *Tab) stylesAfterInsert(at Position, s string) {
	if s == "" {
		return
	}
	if strings.ContainsRune(s, '\n') || !t.patchRowInsert(at.Line, at.Col, len([]rune(s))) {
		t.InvalidateStyles()
		return
	}
	t.deferStyles()
}

// stylesAfterDelete mirrors the removal of the range [from, to) into the
// cached grid. A range spanning lines joins rows, so it re-lexes.
func (t *Tab) stylesAfterDelete(from, to Position) {
	if from.Line != to.Line || !t.patchRowDelete(from.Line, from.Col, to.Col-from.Col) {
		t.InvalidateStyles()
		return
	}
	t.deferStyles()
}

// patchRowInsert widens row `line` by n style entries at col, filling them
// with the style of the rune to the left of the insertion point so typed
// text inherits the color of what it's being typed into. Reports whether
// the patch was possible; false means the caller must re-lex instead.
//
// A row with no cached styles at all is left alone and still counts as
// patched: Render already falls back to the theme's base style for any
// rune past the end of a cached row, which is the correct rendering for
// text nothing has lexed yet.
func (t *Tab) patchRowInsert(line, col, n int) bool {
	if t.Styles == nil || line < 0 || line >= len(t.Styles) || n <= 0 {
		return false
	}
	row := t.Styles[line]
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
	out := make([]tcell.Style, 0, len(row)+n)
	out = append(out, row[:col]...)
	for range n {
		out = append(out, fill)
	}
	t.Styles[line] = append(out, row[col:]...)
	return true
}

// patchRowDelete drops n style entries starting at col from row `line`.
// The count is clamped to what the row actually holds — a row can legally
// be shorter than its line (see patchRowInsert's empty-row case), and a
// short row is not a reason to throw the whole grid away.
func (t *Tab) patchRowDelete(line, col, n int) bool {
	if t.Styles == nil || line < 0 || line >= len(t.Styles) || n <= 0 {
		return false
	}
	row := t.Styles[line]
	if col < 0 || col > len(row) {
		return false
	}
	end := min(col+n, len(row))
	t.Styles[line] = append(row[:col], row[end:]...)
	return true
}

// needsRelex reports whether Render should re-run Chroma this frame.
// Deferred staleness waits out the settle window; everything else — an
// invalidation, or a tab that has no grid to paint from yet — is due now.
func (t *Tab) needsRelex() bool {
	if !t.StyleStale || t.SyntaxOff || t.IsImage() {
		return false
	}
	if !t.styleDefer || t.Styles == nil {
		return true
	}
	return time.Since(t.lastEditAt) >= SyntaxSettle
}

// SyntaxSettleWait returns how long until a deferred re-lex comes due, or
// 0 when nothing is waiting on the clock. The app arms its settle timer
// from this. Zero covers the already-due case too: the redraw that
// follows every dispatch will re-lex, so no wake-up is needed for it.
func (t *Tab) SyntaxSettleWait() time.Duration {
	if !t.StyleStale || t.SyntaxOff || !t.styleDefer || t.Styles == nil {
		return 0
	}
	if d := SyntaxSettle - time.Since(t.lastEditAt); d > 0 {
		return d
	}
	return 0
}
