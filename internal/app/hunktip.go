// =============================================================================
// File: internal/app/hunktip.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// hunktip.go answers "what changed here?" where the diff gutter says
// "something changed here". Rest the pointer on a change bar (▎ added /
// modified, ▁ removed) and a popup shows that hunk's own diff — the
// removed lines in red, the added lines in green — and the wheel scrolls
// it when the change is taller than the box.
//
//	  41 ▎│ return parse(src)          ← pointer rests on the ▎
//	      ┌ Changed · lines 41–43  −1 +3 ──────────────┐
//	      │ -	return parseAll(src, opts)        ▴│
//	      │ +	return parse(src)                   │
//	      │ +	// opts moved to the caller         ▾│
//	      └───────────────────────────── 1–3 of 9 ┘
//
// Doors, for the terminals that cannot report every gesture:
//
//   - REST THE POINTER on the bar (the mark cell of a changed line's
//     first screen row) for diagTipDelay — the dwell door.
//   - CLICK the bar: opens at once, a second click closes. The mouse door
//     for a terminal that reports presses but no motion (macOS
//     Terminal.app), the diagnostic tooltip's rule (diagtip.go).
//   - The keyboard already has the full diff: ≡ Git "Show file's
//     uncommitted changes" opens the changes panel on this file. A
//     popup that keys could scroll would have to own the keyboard, and
//     every passive layer here gives it straight back.
//
// Design choices:
//
//   - **The text comes from the gutter's own diff.** gitdiff.go already
//     runs `git diff -U0` per open file; it now keeps each hunk's -/+
//     lines (diffHunk.Body), so hovering costs no fork and can never
//     disagree with the bar it describes. -U0 means no context lines,
//     which is right for a popup: the context is the code right beside
//     it.
//   - **Passive like every tooltip, with one difference: it can be
//     ENTERED.** The other popups close the moment the pointer leaves
//     its cell; this one has to survive the trip from the bar into the
//     box, or the wheel could never reach it. tooltipPlace hangs the box
//     on the row below (or above) the anchor starting at the anchor's
//     column, so that trip never crosses a foreign cell. Motion inside
//     the box is CLAIMED, so the LSP dwell tooltip does not wake up on
//     the code underneath it and stack a second box on the first.
//   - **Wheel inside the box scrolls the box and nothing else** — even a
//     change that fits — because the editor scrolling under a popup
//     would slide the bar away from the box that describes it. A wheel
//     anywhere else closes the popup and scrolls what it always did.
//   - **Any key, any press closes it**, the passive-layer contract. A
//     press inside the box is swallowed: it covers code the user cannot
//     see, so it must not move the caret there.
//   - **The bar loses to a diagnostic dot.** The mark cell shows one mark
//     by precedence (git < validate < plugin < LSP), and on a line with a
//     dot that cell answers with the diagnostic tooltip; showing the
//     change there would describe a glyph that is not on screen.
//   - **Code is clipped, not wrapped.** A wrapped diff line reads as two
//     lines; a "…" at the edge reads as "more of this line". Tabs expand
//     to four cells so the indentation survives the box's own grid.
//   - **Scrolling is announced** in the box itself — ▴/▾ on the right
//     border (the overflow markers' glyphs, so it reads as the same
//     signal) and "a–b of n" in the bottom border, the caps-are-announced
//     rule. Not an overflow-marker surface (overflow.go): that layer
//     enumerates the docked viewports, and a popup that pops its own
//     popup on hover would be a tooltip on a tooltip.

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

const (
	// hunkTipMaxWidth caps the box. Wider than the prose tooltips
	// (hoverModalMaxWidth): this is CODE, which is clipped rather than
	// wrapped, so every cell given back is a cell of the line shown.
	hunkTipMaxWidth = 100

	// hunkTipMaxRows is the body height before the box scrolls instead
	// of growing — enough for a typical hunk at a glance, short enough
	// that the popup never buries the code around the bar.
	hunkTipMaxRows = 16

	// hunkTipTabWidth is how many cells a tab in a diff line becomes.
	hunkTipTabWidth = 4
)

// hunkTipEvent is the dwell timer's tick; seq pins it to the pointer
// position that scheduled it.
type hunkTipEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *hunkTipEvent) When() time.Time { return e.when }

// Compile-time check that hunkTipEvent really is a tcell.Event.
var _ tcell.Event = (*hunkTipEvent)(nil)

// hunkTipState is the popup's whole state. Kept apart from diagTipState
// although the dwell half is the same shape: this one scrolls and can be
// entered, and folding those rules into the diagnostic tooltip would
// change how THAT one dismisses.
type hunkTipState struct {
	seq  int
	x, y int // the cell the pointer was last seen on
	open bool
	// path pins the popup to the tab it describes; the draw hides it if
	// another tab came to the front without a key or press closing it.
	path   string
	title  string
	lines  []string // body rows, tabs expanded, in diff order
	scroll int      // first body row shown
	ax, ay int      // the anchor cell: the bar the popup describes
	box    struct{ x, y, w, h int }
	// pressClosed: the press being dispatched now just dismissed a popup
	// anchored on the very cell it landed on — so the gutter click reads
	// it as "close" rather than close-and-reopen (diagTipState's toggle).
	pressClosed bool
}

// noteHunkTipPointer is handleMouse's per-event hook for the popup. It
// runs BEFORE the other pointer hooks because it is the one layer the
// pointer may enter: it claims motion and wheel inside its box (so the
// dwell layers never see the code under it) and a press inside it.
// Everything else falls through untouched.
func (a *App) noteHunkTipPointer(x, y int, btn tcell.ButtonMask) bool {
	inBox := a.hunkTip.open && a.hunkTipContains(x, y)

	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if inBox {
			delta := wheelLines
			if btn&tcell.WheelUp != 0 {
				delta = -wheelLines
			}
			a.scrollHunkTip(delta)
			return true
		}
		a.closeHunkTip()
		return false
	}

	if btn != tcell.ButtonNone {
		hit := inBox && btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0
		a.hunkTip.pressClosed = a.hunkTip.open && x == a.hunkTip.ax && y == a.hunkTip.ay
		a.closeHunkTip()
		return hit
	}

	// Pure motion.
	if a.hunkTip.open {
		if inBox {
			a.hunkTip.x, a.hunkTip.y = x, y
			return true // reading it: keep it, and keep the dwell layers off
		}
		if x == a.hunkTip.ax && y == a.hunkTip.ay {
			a.hunkTip.x, a.hunkTip.y = x, y
			return false // back on the bar it describes
		}
		a.closeHunkTip()
	}
	if x == a.hunkTip.x && y == a.hunkTip.y {
		return false // same cell: some hosts repeat motion reports
	}
	a.hunkTip.x, a.hunkTip.y = x, y
	a.armHunkTip()
	return false
}

// armHunkTip schedules a tick only when the pointer is on a change bar,
// so crossing the rest of the editor costs nothing.
func (a *App) armHunkTip() {
	if a.screen == nil {
		return
	}
	if _, ok := a.hunkAtCell(a.hunkTip.x, a.hunkTip.y); !ok {
		return
	}
	a.hunkTip.seq++
	seq := a.hunkTip.seq
	scr := a.screen
	time.AfterFunc(diagTipDelay, func() {
		// Goroutine territory: post, never mutate.
		_ = scr.PostEvent(&hunkTipEvent{when: time.Now(), seq: seq})
	})
}

// closeHunkTip hides the popup and invalidates a pending tick. Cheap and
// safe when nothing is open, so callers fire it blind.
func (a *App) closeHunkTip() {
	a.hunkTip.open = false
	a.hunkTip.lines = nil
	a.hunkTip.scroll = 0
	a.hunkTip.box = struct{ x, y, w, h int }{}
	a.hunkTip.seq++
}

// handleHunkTipTick opens the popup if the tick is current and nothing
// that owns the screen has appeared since it was armed. The hunk is
// looked up AGAIN: the diff may have refreshed, or the view scrolled
// under a stationary pointer.
func (a *App) handleHunkTipTick(e *hunkTipEvent) {
	if e.seq != a.hunkTip.seq || a.modal != nil || a.menuOpen {
		return
	}
	if a.completion.open || a.whichKey.open || a.dragMode != "" {
		return
	}
	a.openHunkTipAt(a.hunkTip.x, a.hunkTip.y)
}

// openHunkTipAt shows the popup for the bar at a screen cell, reporting
// whether there was one. The one opener the dwell tick and the gutter
// click share, so the two doors cannot show different text.
func (a *App) openHunkTipAt(x, y int) bool {
	h, ok := a.hunkAtCell(x, y)
	if !ok {
		return false
	}
	lines := hunkTipLines(h)
	if len(lines) == 0 {
		return false // a hunk with no body has nothing to show
	}
	a.hunkTip.open = true
	a.hunkTip.path = a.activeTabPtr().Path
	a.hunkTip.title = hunkTipTitle(h)
	a.hunkTip.lines = lines
	a.hunkTip.scroll = 0
	a.hunkTip.ax, a.hunkTip.ay = x, y
	// One box at a time over the editor: the others are about cells this
	// one is about to cover.
	a.closeHoverDwell()
	a.closeDiagTip()
	a.closeOverflowTip()
	return true
}

// hunkGutterPress is the click door: a press on a change bar opens the
// popup immediately instead of parking the caret at column 0, and a
// second press on the same bar closes it. Reports whether the press was
// claimed; a claimed press moves no caret and starts no drag. Runs after
// noteHunkTipPointer, which has already closed any open popup on this
// press — hence pressClosed.
func (a *App) hunkGutterPress(x, y int) bool {
	if _, ok := a.hunkAtCell(x, y); !ok {
		return false
	}
	if a.hunkTip.pressClosed {
		return true // second click on the same bar: it closed, leave it closed
	}
	// The button RELEASE arrives next as a motion report on this cell;
	// stamping it makes that report a repeat, not travel off the bar.
	a.hunkTip.x, a.hunkTip.y = x, y
	return a.openHunkTipAt(x, y)
}

// hunkAtCell resolves a screen cell to the hunk whose bar is drawn there.
// The cell must be the MARK cell (just left of the code) on the FIRST
// screen row of a line — the gutter is first-row-only, so a wrapped
// continuation's mark cell is blank and must not answer. A line whose
// mark cell shows a diagnostic dot instead (precedence) is refused.
func (a *App) hunkAtCell(x, y int) (diffHunk, bool) {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.Path == "" || t.IsMarkdownView() {
		return diffHunk{}, false
	}
	hunks := a.fileDiffs[t.Path]
	if len(hunks) == 0 {
		return diffHunk{}, false
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return diffHunk{}, false
	}
	lx, ly := x-ex, y-ey
	_, annEnd := t.AnnotationCols()
	if lx != annEnd {
		return diffHunk{}, false
	}
	pos, ok := t.HitTest(lx, ly, ew, eh)
	if !ok || pos.Line >= t.Buffer.LineCount() {
		return diffHunk{}, false
	}
	// First screen row of the line only — asked of the renderer, never
	// derived as ScrollY + row (soft wrap would make that wrong).
	if _, dy, ok := t.PosScreenCell(editor.Position{Line: pos.Line}, ew, eh); !ok || dy != ly {
		return diffHunk{}, false
	}
	if len(a.diagsAtCell(x, y)) > 0 {
		return diffHunk{}, false
	}
	for _, h := range hunks {
		if pos.Line >= h.Start && pos.Line <= h.End {
			return h, true
		}
	}
	return diffHunk{}, false
}

// hunkTipTitle names the change in the gutter's own terms — what kind,
// which lines of the file as it is now — and sizes it in removed / added
// lines, the git panel's "−3 +4" vocabulary.
func hunkTipTitle(h diffHunk) string {
	var head string
	switch h.Kind {
	case diffAdded:
		head = "Added · " + hunkTipLineRange(h)
	case diffModified:
		head = "Changed · " + hunkTipLineRange(h)
	default:
		if h.Top {
			head = "Removed · above line 1"
		} else {
			head = fmt.Sprintf("Removed · below line %d", h.Start+1)
		}
	}
	removed, added := hunkTipCounts(h)
	var counts []string
	if removed > 0 {
		counts = append(counts, fmt.Sprintf("−%d", removed))
	}
	if added > 0 {
		counts = append(counts, fmt.Sprintf("+%d", added))
	}
	if len(counts) > 0 {
		head += "  " + strings.Join(counts, " ")
	}
	return head
}

// hunkTipLineRange spells a hunk's 1-based line span: "line 12" or
// "lines 12–15".
func hunkTipLineRange(h diffHunk) string {
	if h.End <= h.Start {
		return fmt.Sprintf("line %d", h.Start+1)
	}
	return fmt.Sprintf("lines %d–%d", h.Start+1, h.End+1)
}

// hunkTipCounts tallies a hunk body's removed and added lines.
func hunkTipCounts(h diffHunk) (removed, added int) {
	for _, ln := range h.Body {
		switch {
		case strings.HasPrefix(ln, "-"):
			removed++
		case strings.HasPrefix(ln, "+"):
			added++
		}
	}
	return removed, added
}

// hunkTipLines turns a hunk body into the popup's rows: the -/+ prefix
// kept (it is what colors the row and what a diff reader looks for),
// tabs expanded, stray carriage returns dropped.
func hunkTipLines(h diffHunk) []string {
	out := make([]string, 0, len(h.Body))
	tab := strings.Repeat(" ", hunkTipTabWidth)
	for _, ln := range h.Body {
		ln = strings.TrimRight(ln, "\r")
		out = append(out, strings.ReplaceAll(ln, "\t", tab))
	}
	return out
}

// hunkTipLineStyle colors one popup row by its diff prefix, on the
// popup's own background.
func hunkTipLineStyle(line string, th theme.Theme, bg tcell.Color) tcell.Style {
	st := tcell.StyleDefault.Background(bg)
	switch {
	case strings.HasPrefix(line, "-"):
		return st.Foreground(th.GitDeleted)
	case strings.HasPrefix(line, "+"):
		return st.Foreground(th.GitAdded)
	default:
		return st.Foreground(th.Muted) // git's "\ No newline at end of file"
	}
}

// scrollHunkTip moves the body by delta rows, clamped to the content.
// The body height is the last drawn one (tooltipPlace may have
// shortened the box); before the first draw, the natural cap.
func (a *App) scrollHunkTip(delta int) {
	body := a.hunkTip.box.h - 2
	if body < 1 {
		body = min(len(a.hunkTip.lines), hunkTipMaxRows)
	}
	a.hunkTip.scroll = clampHunkTipScroll(a.hunkTip.scroll+delta, len(a.hunkTip.lines), body)
}

// clampHunkTipScroll keeps the first shown row inside [0, n-body].
func clampHunkTipScroll(scroll, n, body int) int {
	if hi := n - body; scroll > hi {
		scroll = hi
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

// hunkTipVisible asks at DRAW time, like every passive layer: a modal
// opened by something this file never hears about still suppresses it,
// and so does a different tab coming to the front.
func (a *App) hunkTipVisible() bool {
	if !a.hunkTip.open || a.modal != nil || a.menuOpen || len(a.hunkTip.lines) == 0 {
		return false
	}
	t := a.activeTabPtr()
	return t != nil && t.Path == a.hunkTip.path
}

// hunkTipContains reports whether a cell is inside the drawn popup,
// using the rect the last draw stamped.
func (a *App) hunkTipContains(x, y int) bool {
	b := a.hunkTip.box
	return b.w > 0 && b.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// hunkTipSize measures the popup's natural box: wide enough for the
// longest row and the title (each with border + one cell of padding a
// side), capped at hunkTipMaxWidth and the window; one row per body
// line up to hunkTipMaxRows, plus the two borders.
func (a *App) hunkTipSize() (w, h int) {
	w = runeLen(a.hunkTip.title) + 6 // " title " inside the corners
	for _, ln := range a.hunkTip.lines {
		w = max(w, runeLen(ln)+4)
	}
	w = min(w, hunkTipMaxWidth, a.width)
	return w, min(len(a.hunkTip.lines), hunkTipMaxRows) + 2
}

// drawHunkTip paints the popup. Always called, because the call is also
// what clears the stamped rect when nothing is shown.
//
//	┌ title ───────────────────┐   title in the top border
//	│ -removed line           ▴│   ▴ / ▾ on the border when rows are
//	│ +added line              │         hidden that way
//	│ +added line             ▾│
//	└──────────────── 1–3 of 9 ┘   position, only when it scrolls
func (a *App) drawHunkTip() {
	if !a.hunkTipVisible() {
		a.hunkTip.box = struct{ x, y, w, h int }{}
		return
	}
	w, h := a.hunkTipSize()
	mx, my, mw, mh := tooltipPlace(a, w, h, a.hunkTip.ax, a.hunkTip.ay)
	a.hunkTip.box = struct{ x, y, w, h int }{mx, my, mw, mh}
	body := mh - 2
	if body < 1 || mw < 5 {
		return
	}
	n := len(a.hunkTip.lines)
	a.hunkTip.scroll = clampHunkTipScroll(a.hunkTip.scroll, n, body)

	c := a.chrome()
	fillRect(a.screen, mx, my, mw, mh, c.bgSt)
	drawBorder(a.screen, mx, my, mw, mh, c.border)
	drawAt(a.screen, mx+1, my, clipRunes(" "+a.hunkTip.title+" ", mw-2), c.title)

	textW := mw - 4
	for i := 0; i < body; i++ {
		idx := a.hunkTip.scroll + i
		if idx >= n {
			break
		}
		ln := a.hunkTip.lines[idx]
		drawAt(a.screen, mx+2, my+1+i, clipRunes(ln, textW), hunkTipLineStyle(ln, a.theme, c.bg))
	}

	if n > body {
		arrow := c.title
		if a.hunkTip.scroll > 0 {
			a.screen.SetContent(mx+mw-1, my+1, overflowUpRune, nil, arrow)
		}
		if a.hunkTip.scroll+body < n {
			a.screen.SetContent(mx+mw-1, my+mh-2, overflowDownRune, nil, arrow)
		}
		pos := fmt.Sprintf(" %d–%d of %d ", a.hunkTip.scroll+1, a.hunkTip.scroll+body, n)
		if px := mx + mw - 1 - runeLen(pos); px > mx {
			drawAt(a.screen, px, my+mh-1, pos, c.muted)
		}
	}
}

// clipRunes cuts s to at most w cells, ending in "…" when it had to cut,
// so a clipped line says it runs on.
func clipRunes(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:max(w, 0)])
	}
	return string(r[:w-1]) + "…"
}
