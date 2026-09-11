// =============================================================================
// File: internal/app/toolheader.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toolheader.go draws the header a tool window wears when it does not
// draw one of its own: a rule across the dock, the panel's name and
// whatever count it has to add, and a ✕ to put it away.
//
// WHY IT EXISTS FOR A SET OF ONE. Six of the seven tools already draw
// their own header, because six of them were BORN as bottom strips: the
// git panels, the problems list, the compare view and the terminal all
// arrived with a rule, a title and a ✕ because that is what a bottom
// strip in this editor looks like. The file tree is the exception — it
// was a left-hand sidebar for the editor's whole life, where the seam
// resizes it and the ≡ row hides it, so it had no ✕ on ANY dock, and the
// only way to put it away was a menu row you had to already know about.
//
// So the header is stated generically rather than bolted onto the tree.
// `toolDef.ownHeader` says which tools bring their own; anything that
// does not gets this one, on every edge. That is a small amount of code
// and it is the truthful model — "a tool window has a header" — rather
// than a special case the next headerless tool would have to discover
// the same way.
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
//   - IT IS DRAWN ON EVERY EDGE, and it costs no rows anywhere. The
//     tree gives up its own EXPLORER row for it (HideLabel), so the
//     header REPLACES a row rather than adding one — the same title, the
//     same mark count, plus a ✕ the tree never had on any dock.
//
//   - WHAT THE RULE DOES DEPENDS ON THE EDGE. On the bottom it is the
//     height-drag handle, because there is no seam down there. On a
//     vertical edge the seam already resizes the panel, so the rule is
//     inert — but a press on it is still SWALLOWED, exactly as the
//     EXPLORER row it replaced was: it is chrome, not a tree row.

package app

import "github.com/gdamore/tcell/v2"

// toolNeedsHeader reports whether a tool gets the generic header: it is
// showing and it does not draw one itself. Every edge, because a panel
// wants a name and a ✕ wherever it is sitting.
func (a *App) toolNeedsHeader(id toolID) bool {
	d, ok := toolDefFor(id)
	if !ok || d.ownHeader {
		return false
	}
	return d.isOpen(a)
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

// toolHeaderIsHandle reports whether this header's rule is the panel's
// resize grip. Only on the bottom edge: a vertical dock has a seam
// (splitter.go), and two handles for one dimension would be one too
// many — the rule would be a control that sometimes did nothing.
func (a *App) toolHeaderIsHandle(id toolID) bool {
	return a.toolNeedsHeader(id) && a.toolDock(id) == dockBottom
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
	if a.toolHeaderIsHandle(id) && a.dragMode == dragModeForDock(dockBottom) {
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
	// yields. It sheds its COUNT first and its name only after that, so
	// a narrow sidebar loses the annotation before it loses the word
	// telling you what the panel is.
	tx := hx + 1
	for _, title := range []string{a.toolHeaderTitle(id), " " + toolTitle(id) + " "} {
		if tx+runeLen(title) <= close.x {
			drawAt(a.screen, tx, hy, title, titleSt)
			break
		}
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
	if !a.toolHeaderIsHandle(id) {
		// Inert, but still swallowed: this row is chrome, and it stands
		// where the tree's own (unclickable) EXPLORER row used to.
		return ""
	}
	a.dragSplitOffset = 0
	return dragModeForDock(dockBottom)
}

// headerlessTools lists every showing tool wearing the generic header,
// one per edge at most. The draw pass walks it, and the click router
// asks toolHeaderAt, so neither has to know which tools those are.
func (a *App) headerlessTools() []toolID {
	var out []toolID
	for _, side := range dockSides {
		if id, ok := a.visibleTool(side); ok && a.toolNeedsHeader(id) {
			out = append(out, id)
		}
	}
	return out
}

// toolHeaderAt reports which tool's generic header (x, y) lands on, if
// any — the click router's single question.
func (a *App) toolHeaderAt(x, y int) (toolID, bool) {
	for _, id := range a.headerlessTools() {
		if a.toolHeaderContains(id, x, y) {
			return id, true
		}
	}
	return "", false
}
