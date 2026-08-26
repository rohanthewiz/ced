// =============================================================================
// File: internal/app/scrollbar.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The editor's vertical scrollbar: one reserved column down the right
// edge of the editor body, carrying a thumb whose HEIGHT answers "how
// much of this file fits on screen" and whose POSITION answers "where in
// it am I" — and which drags.
//
// A terminal editor has nowhere else to say either of those things. The
// status bar reports the cursor's line and the file's line count, but a
// number pair is read, not glanced at, and it says nothing about how far
// the remaining text extends past the bottom row. The bar is the glance.
//
//	┌──────────────┬─┐
//	│ editorRect   │▐│ ← thumb: trackH * viewH / lineCount rows,
//	│              │▐│   offset free * ScrollY / MaxScroll
//	│              │▕│
//	│              │▕│ ← rail
//	└──────────────┴─┘
//	                ^ scrollbarRect: one column, ex+ew
//
// The rail also carries the caret's position and the file's off-screen
// diagnostics and find hits, one colored cell each — that half lives in
// scrollbarmarks.go, and drawScrollbar is where the two meet.
//
// The SIDEBAR has no bar. A tree is a list of names, and the question a
// list raises is "have I seen everything?" — a yes/no that one marker on
// the last row answers, without spending a column of every name on it.
// See filetree's drawMoreMarker.
package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

const (
	// scrollbarMinEditor is the editor width below which the bar gives
	// its column back. On a window that narrow the code itself is the
	// scarce thing, and an editor squeezed under this is a worse answer
	// to "where am I" than having to look at the status bar.
	scrollbarMinEditor = 24

	// scrollbarTrackRune / scrollbarThumbRune are the rail and the thumb.
	// Both are single-width per the marker rule — the column is exactly
	// one cell, so a double-width glyph would spill into the code beside
	// it — and both come from the SAME family, right-aligned inside the
	// cell: a one-eighth block for the rail, a half block for the thumb.
	//
	// That pairing is the whole look. The first cut drew a box-drawing
	// '│' under a solid '█', which is two unrelated shapes on one column:
	// a hairline centred in the cell with a slab bulging out of it, so the
	// thumb read as a blot rather than as the same rail thickened. Here
	// the thumb is visibly the rail with more weight, and being
	// right-aligned it hugs the window's edge instead of crowding the code
	// it sits beside. The git gutter's '▎' is the same family and the same
	// argument, one panel over.
	scrollbarTrackRune = '▕'
	scrollbarThumbRune = '▐'

	// scrollbarMinThumb is the shortest thumb the bar will draw, in rows.
	// A single cell is legible as a mark but not as a HANDLE — it is a
	// grab target the width of a character, and in a long file (where the
	// arithmetic produces it) it is also the case where the user most
	// wants to drag. Two rows costs at most one row of positional
	// precision on files big enough that a row is already thousands of
	// lines.
	scrollbarMinThumb = 2
)

// scrollbarCols is the number of columns the bar claims out of the
// editor's band: 1 when it's on and there's something to scroll through,
// 0 otherwise. editorRect subtracts it, which is what keeps every
// existing call site — hit-testing, the hover tooltip, drag auto-scroll,
// Alt+click — ignorant of the bar's existence.
//
// The bar DISPLACES rather than floating over the last column, the same
// choice the Find-all dock made: a thumb painted on top of code would
// cover a character on every row it crossed, and the one column it would
// cover is the one the overflow arrow already uses to say a line runs on.
//
// It is deliberately NOT conditioned on the file being longer than the
// viewport. A bar that came and went as the buffer grew past the bottom
// row would move the editor's right edge — and thus re-wrap nothing but
// re-flow everything the user was reading — on an edit that had nothing
// to do with layout. A short file gets a full-height thumb instead,
// which is the honest way to say "this is all of it".
func (a *App) scrollbarCols() int {
	if !a.scrollbarShown || a.activeTabPtr() == nil {
		return 0
	}
	// Measured against the band the editor would otherwise have had, not
	// against editorRect — which subtracts this very number.
	if a.editorBandCols()-a.findAllPanelWidth()-1 < scrollbarMinEditor {
		return 0
	}
	return 1
}

// scrollbarRect is the bar's screen rectangle — the single source draw
// and hit-testing share (the btnRect house rule). A zero width means the
// bar isn't there; callers must check it rather than assuming a column.
//
// It sits at ex+ew, i.e. between the editor and a right-docked Find-all
// list. The bar belongs to the editor, so it stays welded to the editor's
// edge wherever that edge has moved to.
func (a *App) scrollbarRect() (x, y, w, h int) {
	if a.scrollbarCols() == 0 {
		return 0, 0, 0, 0
	}
	ex, ey, ew, eh := a.editorRect()
	if eh <= 0 {
		return 0, 0, 0, 0
	}
	return ex + ew, ey, 1, eh
}

// scrollbarContains reports whether (x, y) lands on the bar. Used by the
// mouse router, which must ask before its editor catch-all: the column is
// inside the editor's y-band, so an unasked press would move the caret to
// whatever line the user happened to grab the thumb on.
func (a *App) scrollbarContains(x, y int) bool {
	sx, sy, sw, sh := a.scrollbarRect()
	return sw > 0 && x == sx && y >= sy && y < sy+sh
}

// scrollbarMetrics places the thumb inside a track of trackH rows for a
// buffer of `total` lines shown through a viewport of viewH rows, with
// the viewport's first line at scrollY and its last legal value at
// maxScroll. Returns the thumb's offset from the top of the track and its
// height, both in rows.
//
// Split out as a pure function because the interesting part is arithmetic
// with three degenerate cases (empty buffer, file shorter than the
// window, thumb as tall as the track), and each of them is a place a
// division could panic or a subtraction go negative.
//
// The thumb's height is the proportion of the FILE on screen — not of the
// scrollable range, which includes clampScroll's overscroll pad. The pad
// is blank space below the last line; counting it would shrink the thumb
// to claim there is more file than there is. The thumb's POSITION is
// measured against maxScroll instead, precisely so that scrolled all the
// way down (last line parked mid-screen) puts the thumb flush at the
// bottom of the track.
func scrollbarMetrics(total, viewH, trackH, scrollY, maxScroll int) (thumbY, thumbH int) {
	if trackH <= 0 || viewH <= 0 {
		return 0, 0
	}
	if total < 1 {
		total = 1 // an empty buffer still has one (empty) line on screen
	}
	if viewH >= total {
		// Everything fits: the thumb fills the track. Saying so with a
		// full-height thumb is the whole reason the bar is drawn for
		// short files at all.
		return 0, trackH
	}
	thumbH = trackH * viewH / total
	// The floor is applied before the ceiling, and the ceiling wins: on a
	// track shorter than the floor itself (a two-row editor band) a thumb
	// taller than its own track would place off the end of it.
	if thumbH < scrollbarMinThumb {
		thumbH = scrollbarMinThumb
	}
	if thumbH > trackH {
		thumbH = trackH
	}
	free := trackH - thumbH
	if free <= 0 || maxScroll <= 0 {
		return 0, thumbH
	}
	if scrollY < 0 {
		scrollY = 0
	}
	if scrollY > maxScroll {
		scrollY = maxScroll
	}
	// Rounded rather than truncated so the thumb reaches the bottom of
	// the track on the last line of travel instead of a row short of it.
	thumbY = (free*scrollY + maxScroll/2) / maxScroll
	if thumbY > free {
		thumbY = free
	}
	return thumbY, thumbH
}

// scrollbarThumb resolves the current tab's thumb against the live rect,
// returning the track's screen y, its height, the thumb's offset and
// height, and how far the thumb may travel. ok is false when there is no
// bar (or no tab) to talk about.
//
// Every mouse verb below goes through it, so press, drag and paint can
// never disagree about where the thumb is — the same one-measurement rule
// the rect helper follows one floor down.
func (a *App) scrollbarThumb() (trackY, trackH, thumbY, thumbH, maxScroll int, ok bool) {
	t := a.activeTabPtr()
	if t == nil {
		return 0, 0, 0, 0, 0, false
	}
	_, sy, sw, sh := a.scrollbarRect()
	if sw == 0 || sh <= 0 {
		return 0, 0, 0, 0, 0, false
	}
	// The viewport the tab is scrolled through is exactly the bar's own
	// height: editorRect hands both the same h.
	maxScroll = t.MaxScroll(sh)
	thumbY, thumbH = scrollbarMetrics(t.Buffer.LineCount(), sh, sh, t.ScrollY, maxScroll)
	return sy, sh, thumbY, thumbH, maxScroll, true
}

// scrollbarPress handles a left press on the bar and reports the drag
// mode it starts ("scrollbar" when the press landed on the thumb, "" when
// it didn't).
//
// A press on the TRACK pages toward it rather than jumping the thumb to
// the pointer. Paging is the conventional answer and it's the reversible
// one: a mis-aimed page is one press back, while a jump has thrown away
// the position the user was reading from with nothing to restore it.
// Dragging is how you go somewhere specific.
func (a *App) scrollbarPress(x, y int) string {
	t := a.activeTabPtr()
	if t == nil {
		return ""
	}
	trackY, trackH, thumbY, thumbH, _, ok := a.scrollbarThumb()
	if !ok {
		return ""
	}
	rel := y - trackY
	if rel >= thumbY && rel < thumbY+thumbH {
		// Remember where inside the thumb the user grabbed it, so the
		// thumb slides with the pointer instead of snapping its top edge
		// under it on the first motion.
		a.scrollbarGrab = rel - thumbY
		return "scrollbar"
	}
	page := trackH
	if rel < thumbY {
		page = -page
	}
	// Through Tab.Scroll (not a raw assignment) so the zero floor is
	// applied here and Render's clampScroll applies the ceiling — the one
	// pair of guards every other scroll gesture goes through.
	t.Scroll(page)
	return ""
}

// dragScrollbarTo moves the viewport so the thumb's top edge tracks the
// pointer, honouring the grab offset taken at press time. Called for
// every mouse event while dragMode is "scrollbar", including ones whose y
// has left the bar entirely — the clamp below is what makes dragging off
// the top or bottom park at the corresponding end instead of doing
// nothing.
func (a *App) dragScrollbarTo(y int) {
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	trackY, trackH, _, thumbH, maxScroll, ok := a.scrollbarThumb()
	if !ok {
		return
	}
	free := trackH - thumbH
	if free <= 0 || maxScroll <= 0 {
		return // nothing off-screen: the thumb fills the track
	}
	want := y - trackY - a.scrollbarGrab
	if want < 0 {
		want = 0
	}
	if want > free {
		want = free
	}
	// The inverse of scrollbarMetrics' placement, rounded the same way so
	// a drag that doesn't move the thumb doesn't move the buffer either.
	t.ScrollY = (want*maxScroll + free/2) / free
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	if t.ScrollY > maxScroll {
		t.ScrollY = maxScroll
	}
}

// drawScrollbar paints the rail, the thumb, and whatever the rail has
// to say about the rest of the file (scrollbarmarks.go).
//
// Called from draw() immediately AFTER Tab.Render, deliberately: Render
// is where EnsureVisible and clampScroll settle ScrollY, so a bar painted
// before it would show the previous frame's position — and would place
// the caret tick and the marks against a viewport the user never saw.
func (a *App) drawScrollbar() {
	sx, _, sw, _ := a.scrollbarRect()
	if sw == 0 {
		return
	}
	trackY, trackH, thumbY, thumbH, _, ok := a.scrollbarThumb()
	if !ok {
		return
	}
	// Same visual language as the sidebar splitter: subtle at rest,
	// accent while the user has hold of it, so the live grab handle is
	// unmistakable.
	thumbFG := a.theme.Muted
	if a.dragMode == "scrollbar" {
		thumbFG = a.theme.Accent
	}
	trackStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Subtle)
	thumbStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(thumbFG)

	// The viewport the marks are measured against is the bar's own band:
	// editorRect hands the tab and the bar the same y and h, so the last
	// row on screen is trackH-1 rows below the first.
	var marks []railKind
	if t := a.activeTabPtr(); t != nil {
		marks = a.railMarks(t, trackH, t.ScrollY, t.ScrollY+trackH-1)
	}

	for row := 0; row < trackH; row++ {
		r, st := scrollbarTrackRune, trackStyle
		switch {
		// The thumb wins its rows outright. Nothing paints over it: it
		// is the one thing on this column that has to stay readable as a
		// shape, and it covers the lines whose marks are suppressed
		// anyway (railMarks skips everything on screen).
		case row >= thumbY && row < thumbY+thumbH:
			r, st = scrollbarThumbRune, thumbStyle
		case row < len(marks) && marks[row] != railNone:
			r = scrollbarThumbRune
			if marks[row] == railCaret {
				r = railCaretRune
			}
			st = tcell.StyleDefault.Background(a.theme.BG).
				Foreground(railMarkColor(a.theme, marks[row]))
		}
		a.screen.SetContent(sx, trackY+row, r, nil, st)
	}
}

// menuToggleScrollbar flips the bar from the ≡ View group and persists
// the choice. Both directions take effect on the very next draw, since
// the bar is derived per frame rather than stored.
//
// The preference it expresses is "do I want to spend a column of code
// width on knowing where I am in the file" — which is why the sidebar's
// overflow marker is NOT gated on it. That marker costs no layout at
// all, and hiding it would leave a list silently running on past its
// bottom row, which is the one thing a list must never do.
func (a *App) menuToggleScrollbar() {
	a.closeMenu()
	a.scrollbarShown = !a.scrollbarShown
	if a.scrollbarShown {
		a.flash("Scrollbar on")
	} else {
		a.flash("Scrollbar off")
	}
	if err := userconfig.SaveScrollbar(userconfig.DefaultPath(), a.scrollbarShown); err != nil {
		a.flash("config: " + err.Error())
	}
}

// scrollbarToggleLabel names the action the row will perform, not the
// state it's in — the toggle-in-place rule every other View row follows.
func (a *App) scrollbarToggleLabel() string {
	if a.scrollbarShown {
		return "Hide scrollbar"
	}
	return "Show scrollbar"
}
