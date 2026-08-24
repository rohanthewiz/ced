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
//	│ editorRect   │█│ ← thumb: trackH * viewH / lineCount rows,
//	│              │█│   offset free * ScrollY / MaxScroll
//	│              │ │
//	│              │ │ ← track
//	└──────────────┴─┘
//	                ^ scrollbarRect: one column, ex+ew
//
// The file tree gets the same bar on different terms: it SHARES the
// tree's own last column instead of reserving one, which is what lets it
// come and go with the content. See treeScrollbarRect.
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

	// scrollbarTrackRune / scrollbarThumbRune are both single-width per
	// the marker rule — the column is exactly one cell, so a double-width
	// glyph would spill into the code beside it.
	scrollbarTrackRune = '│'
	scrollbarThumbRune = '█'

	// treeScrollbarMinWidth is the tree width below which the sidebar's
	// bar stays away. The tree's column is SHARED, so at that size the
	// bar would be sitting on a meaningful share of every name.
	treeScrollbarMinWidth = 12
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
	if thumbH < 1 {
		thumbH = 1 // a thumb you can't see is a bar that says nothing
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

// drawScrollbar paints the track and thumb.
//
// Called from draw() immediately AFTER Tab.Render, deliberately: Render
// is where EnsureVisible and clampScroll settle ScrollY, so a bar painted
// before it would show the previous frame's position on any tick that
// moved the cursor.
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
	for row := 0; row < trackH; row++ {
		r, st := scrollbarTrackRune, trackStyle
		if row >= thumbY && row < thumbY+thumbH {
			r, st = scrollbarThumbRune, thumbStyle
		}
		a.screen.SetContent(sx, trackY+row, r, nil, st)
	}
}

// -----------------------------------------------------------------------------
// The file tree's bar
// -----------------------------------------------------------------------------
//
// Same thumb, same drag, same page-on-track — and one deliberate
// difference: it SHARES the tree's rightmost column rather than
// reserving one, which is what the two rules below follow from.

// treeScrollbarRect is the sidebar bar's screen rectangle, or a zero
// width when there is no bar. It spans the LIST band only: the EXPLORER
// header and the project-name row scroll with nothing, and the project
// name is itself a click target.
//
// The column is SHARED — it is the tree's own last column, painted over
// after Render rather than subtracted from sidebarRect. The tree keeps
// its full drawing width, and the cost is that the longest row's final
// rune sits under the bar. That trade is what buys the second rule:
//
// **The bar appears only while the list overflows.** The editor's bar
// can't do that (its column is reserved, so coming and going would move
// the editor's right edge and re-flow the code on an unrelated edit);
// this one costs no layout at all, so a tree with nothing to scroll gets
// its column back instead of a full-height thumb sitting on the names.
func (a *App) treeScrollbarRect() (x, y, w, h int) {
	if !a.scrollbarShown || !a.sidebarShown || a.tree == nil {
		return 0, 0, 0, 0
	}
	sx, sy, sw, sh := a.sidebarRect()
	if sw < treeScrollbarMinWidth {
		return 0, 0, 0, 0
	}
	off, rows := a.tree.ListRows(sh)
	if rows <= 0 || a.tree.RowCount() <= rows {
		return 0, 0, 0, 0 // nothing off-screen: the column stays the tree's
	}
	return sx + sw - 1, sy + off, 1, rows
}

// treeScrollbarCols is 1 while the sidebar's bar is on screen, 0
// otherwise. It is NOT read by sidebarRect — the column is shared, so
// the tree keeps its full drawing width either way. The one caller is
// auto-fit (treeautofit.go), which asks for one extra column so the
// longest name doesn't end up under the bar: auto-fit exists precisely
// to stop the tree truncating names, and a bar sitting on the last rune
// of the row it just widened to fit would undo that.
//
// With auto-fit OFF (the width the user dragged) nothing compensates and
// the bar simply overlays. That is what "shared" costs, and dragging one
// column wider is the out.
func (a *App) treeScrollbarCols() int {
	if _, _, w, _ := a.treeScrollbarRect(); w > 0 {
		return 1
	}
	return 0
}

// treeScrollbarContains reports whether (x, y) lands on the sidebar's
// bar. The router must ask before sidebarClick, which would otherwise
// select whatever node the user grabbed the thumb on.
func (a *App) treeScrollbarContains(x, y int) bool {
	sx, sy, sw, sh := a.treeScrollbarRect()
	return sw > 0 && x == sx && y >= sy && y < sy+sh
}

// treeScrollbarThumb resolves the tree's thumb against the live rect —
// the sidebar twin of scrollbarThumb, and it shares scrollbarMetrics, so
// the two bars can never disagree about what a thumb of a given size
// means.
func (a *App) treeScrollbarThumb() (trackY, trackH, thumbY, thumbH, maxScroll int, ok bool) {
	_, sy, sw, sh := a.treeScrollbarRect()
	if sw == 0 || sh <= 0 {
		return 0, 0, 0, 0, 0, false
	}
	total := a.tree.RowCount()
	maxScroll = a.tree.MaxScroll(sh)
	thumbY, thumbH = scrollbarMetrics(total, sh, sh, a.tree.ScrollY, maxScroll)
	return sy, sh, thumbY, thumbH, maxScroll, true
}

// treeScrollbarPress handles a left press on the sidebar's bar and
// reports the drag mode it starts ("treescroll" on the thumb, "" on the
// track, which pages toward the press). Same verbs as the editor's bar.
func (a *App) treeScrollbarPress(x, y int) string {
	trackY, trackH, thumbY, thumbH, _, ok := a.treeScrollbarThumb()
	if !ok {
		return ""
	}
	rel := y - trackY
	if rel >= thumbY && rel < thumbY+thumbH {
		a.scrollbarGrab = rel - thumbY
		return "treescroll"
	}
	page := trackH
	if rel < thumbY {
		page = -page
	}
	// Tree.Scroll applies the zero floor; Render's clampScroll applies
	// the ceiling — the pair every other tree scroll gesture goes through.
	a.tree.Scroll(page)
	return ""
}

// dragTreeScrollbarTo moves the tree's viewport so the thumb tracks the
// pointer, honouring the grab offset taken at press time.
func (a *App) dragTreeScrollbarTo(y int) {
	trackY, trackH, _, thumbH, maxScroll, ok := a.treeScrollbarThumb()
	if !ok {
		return
	}
	free := trackH - thumbH
	if free <= 0 || maxScroll <= 0 {
		return
	}
	want := y - trackY - a.scrollbarGrab
	if want < 0 {
		want = 0
	}
	if want > free {
		want = free
	}
	a.tree.ScrollY = (want*maxScroll + free/2) / free
	if a.tree.ScrollY < 0 {
		a.tree.ScrollY = 0
	}
	if a.tree.ScrollY > maxScroll {
		a.tree.ScrollY = maxScroll
	}
}

// drawTreeScrollbar paints the sidebar's bar OVER the column the tree
// just drew — which is why it must run after Tree.Render, and not only
// for the usual settle reason (Render is where clampScroll lands
// ScrollY): anything painted before it would simply be overwritten.
//
// The track keeps the sidebar's own background rather than the editor's,
// so the shared column still reads as part of the panel.
func (a *App) drawTreeScrollbar() {
	sx, _, sw, _ := a.treeScrollbarRect()
	if sw == 0 {
		return
	}
	trackY, trackH, thumbY, thumbH, _, ok := a.treeScrollbarThumb()
	if !ok {
		return
	}
	thumbFG := a.theme.Muted
	if a.dragMode == "treescroll" {
		thumbFG = a.theme.Accent
	}
	trackStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Subtle)
	thumbStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(thumbFG)
	for row := 0; row < trackH; row++ {
		r, st := scrollbarTrackRune, trackStyle
		if row >= thumbY && row < thumbY+thumbH {
			r, st = scrollbarThumbRune, thumbStyle
		}
		a.screen.SetContent(sx, trackY+row, r, nil, st)
	}
}

// menuToggleScrollbar flips BOTH bars from the ≡ View group and persists
// the choice. Both directions take effect on the very next draw, since
// each bar is derived per frame rather than stored.
//
// One key, not two. The preference the user is expressing is "do I want
// scrollbars" — the editor's and the tree's are one feature at two
// surfaces, the way find-in-file and find-in-project are one question at
// two scopes, and answering them separately would be a trap. A second key
// would also be a setting almost nobody has a reason to flip: the tree's
// bar shares its column, so the one argument for turning a bar off (give
// me the width back) does not apply to it.
func (a *App) menuToggleScrollbar() {
	a.closeMenu()
	a.scrollbarShown = !a.scrollbarShown
	if a.scrollbarShown {
		a.flash("Scrollbars on")
	} else {
		a.flash("Scrollbars off")
	}
	if err := userconfig.SaveScrollbar(userconfig.DefaultPath(), a.scrollbarShown); err != nil {
		a.flash("config: " + err.Error())
	}
}

// scrollbarToggleLabel names the action the row will perform, not the
// state it's in — the toggle-in-place rule every other View row follows.
func (a *App) scrollbarToggleLabel() string {
	if a.scrollbarShown {
		return "Hide scrollbars"
	}
	return "Show scrollbars"
}
