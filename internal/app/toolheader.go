// =============================================================================
// File: internal/app/toolheader.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toolheader.go draws the header rule a BOTTOM-DOCKED tool window wears
// when it does not draw one of its own: the rule itself, the panel's
// name, and a ✕ to put it away — with the rule outside the button
// doubling as the height-drag handle.
//
// WHY IT EXISTS FOR A SET OF ONE. Six of the seven tools already draw
// their own header, because six of them were BORN as bottom strips: the
// git panels, the problems list, the compare view and the terminal all
// arrived with a rule, a title and a ✕ because that is what a bottom
// strip in this editor looks like. The file tree is the exception — it
// was a left-hand sidebar for the editor's whole life, where the seam
// resizes it and the ≡ row hides it, so it never needed either
// affordance. The moment it could be docked at the bottom it had
// NEITHER: no seam down there, no ✕ anywhere, and the only way back was
// a menu row you had to already know about.
//
// So the header is stated generically rather than bolted onto the tree.
// `toolDef.ownHeader` says which tools bring their own; anything that
// does not gets this one for free, on whichever edge it needs it. That
// is a small amount of code and it is the truthful model — "a bottom
// dock has a header" — rather than a special case that the next
// headerless tool would have to discover the same way.
//
// HOUSE RULES:
//
//   - THE HEADER IS INSIDE THE TOOL'S RECT, not beside it. `toolRect`
//     returns the whole dock including this row, exactly as every
//     hand-drawn panel's rect includes its own header; `toolBodyRect`
//     is what the panel's CONTENT gets. That split is why the file
//     tree's hit-testing, marks and overflow markers needed no changes
//     at all — they read sidebarRect, which is the body.
//
//   - ONE RECT SOURCE for draw and hit-test (the btnRect rule), and the
//     rule OUTSIDE the button is the grab handle — the arrangement
//     every other bottom panel already uses, so a user who has dragged
//     one has dragged all of them.
//
//   - IT IS ONLY DRAWN ON THE BOTTOM EDGE. On a vertical one the seam
//     is the resize affordance and the tool's own first row is its
//     title; a header there would cost a row of content on the edge
//     where rows are scarcest, to say something already on screen.

package app

import "github.com/gdamore/tcell/v2"

// toolNeedsHeader reports whether a tool gets the generic header: it is
// showing, it is on the bottom edge, and it does not draw one itself.
func (a *App) toolNeedsHeader(id toolID) bool {
	d, ok := toolDefFor(id)
	if !ok || d.ownHeader || !d.isOpen(a) {
		return false
	}
	return a.toolDock(id) == dockBottom
}

// toolHeaderRows is how many rows of a tool's dock the generic header
// takes — one, or none when the tool draws its own.
func (a *App) toolHeaderRows(id toolID) int {
	if a.toolNeedsHeader(id) {
		return 1
	}
	return 0
}

// toolBodyRect is the tool's CONTENT rectangle: its dock minus whatever
// the generic header took. Panels that draw their own header read
// toolRect and get the header row back, because for them it is part of
// what they paint.
func (a *App) toolBodyRect(id toolID) (x, y, w, h int) {
	x, y, w, h = a.toolRect(id)
	if n := a.toolHeaderRows(id); n > 0 && h > n {
		y += n
		h -= n
	}
	return
}

// toolHeaderRect is the header row itself, or a zero rect when the tool
// has none.
func (a *App) toolHeaderRect(id toolID) (x, y, w, h int) {
	if !a.toolNeedsHeader(id) {
		return 0, 0, 0, 0
	}
	x, y, w, _ = a.toolRect(id)
	return x, y, w, 1
}

// toolHeaderCloseRect is the ✕ button, at the header's right end — the
// same cell every other bottom panel puts it in, so the gesture is in
// the same place whichever panel is down there.
func (a *App) toolHeaderCloseRect(id toolID) btnRect {
	hx, hy, hw, hh := a.toolHeaderRect(id)
	if hh == 0 {
		return btnRect{}
	}
	return btnRect{x: hx + hw - 4, y: hy, w: 3}
}

// toolHeaderContains reports whether (x, y) lands on a tool's generic
// header. The click router asks this before the panel's own hit-test,
// which is why the tree's body routing needed no change.
func (a *App) toolHeaderContains(id toolID, x, y int) bool {
	hx, hy, hw, hh := a.toolHeaderRect(id)
	return hh > 0 && y == hy && x >= hx && x < hx+hw
}

// toolHeaderTitle names the panel in its header, with whatever count it
// has to add — the "Git changes · 27 files" shape, so a header says what
// the panel holds rather than only what it is.
//
// The file tree's count is its MARK SET, which is the one thing about
// the tree that a user can change and then scroll away from. With the
// tree's own EXPLORER row suppressed (HideLabel), this is the only place
// that count is drawn at all, so it is not optional decoration here.
func (a *App) toolHeaderTitle(id toolID) string {
	title := " " + toolTitle(id)
	if id == toolProject && a.tree != nil {
		if n := a.tree.MarkCount(); n > 0 {
			title += " · " + itoa(n) + " marked"
		}
	}
	return title + " "
}

// drawToolHeader paints one tool's generic header: the rule across the
// dock, the title, and the ✕. The rule brightens to Accent while the
// drag is live — the sidebar splitter's grab-handle language, which
// every resizable strip in the editor shares.
func (a *App) drawToolHeader(id toolID) {
	hx, hy, hw, hh := a.toolHeaderRect(id)
	if hh == 0 {
		return
	}
	th := a.theme
	ruleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	if a.dragMode == dragModeForDock(dockBottom) {
		ruleSt = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent)
	}
	titleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent).Bold(true)

	for cx := hx; cx < hx+hw; cx++ {
		a.screen.SetContent(cx, hy, '─', nil, ruleSt)
	}
	close := a.toolHeaderCloseRect(id)
	drawAt(a.screen, close.x, close.y, " ✕ ", titleSt)

	// The title is DROPPED rather than overlapped when the dock is too
	// narrow to hold both — the problems header's rule, for its reason:
	// the ✕ is a control and the title is a label, so the label is what
	// yields.
	title := a.toolHeaderTitle(id)
	if tx := hx + 1; tx+runeLen(title) <= close.x {
		drawAt(a.screen, tx, hy, title, titleSt)
	}
}

// toolHeaderPress handles a press on a tool's generic header and returns
// the drag mode it started, if any: the ✕ hides the tool, and the rule
// anywhere else seizes the bottom edge's height drag.
func (a *App) toolHeaderPress(id toolID, x, y int) string {
	if !a.toolHeaderContains(id, x, y) {
		return ""
	}
	if a.toolHeaderCloseRect(id).contains(x, y) {
		a.hideTool(id)
		return ""
	}
	a.dragSplitOffset = 0
	return dragModeForDock(dockBottom)
}

// headerlessBottomTool reports which showing tool, if any, is wearing
// the generic header right now. The click router asks once instead of
// walking the registry, and the draw pass uses it for the same reason.
func (a *App) headerlessBottomTool() (toolID, bool) {
	id, ok := a.visibleTool(dockBottom)
	if !ok || !a.toolNeedsHeader(id) {
		return "", false
	}
	return id, true
}
