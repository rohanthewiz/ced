// =============================================================================
// File: internal/app/splitter.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// splitter.go is the one place a resizable WINDOW seam is described —
// the vertical rules the user drags to re-apportion the screen's
// columns. There is now exactly ONE per edge rather than one per panel:
// the seam belongs to the left or right DOCK, and whichever tool window
// is showing there is what it resizes (see toolwindow.go). It also holds
// the grip glyph and the middle-rows test the git panels' internal
// list/diff seams share.
//
// It exists because the git panels' seam was fixed three ways at once
// and the window seams had two of the same problems. The house rules,
// once, so a fourth seam inherits them:
//
//   - A ONE-COLUMN GRAB ZONE IS A COIN FLIP WITH A MOUSE. The seam is
//     among the most-aimed-at cells in the layout and was the hardest
//     to hit. So the zone is the divider PLUS ONE MORE COLUMN.
//
//   - THAT COLUMN IS THE ONE ON ITS LEFT, always, and the asymmetry is
//     load-bearing rather than a shortcut. Left of a window seam is the
//     panel it resizes, which stops a column short of the rule in every
//     layout — a strip's right margin, the file tree's row tail. Right
//     of it is the EDITOR BAND, whose first column belongs to whatever
//     is docked there, and two of those put a deliberate one-cell
//     control in exactly that cell: the git panel's review column and
//     (flipped) the file tree's own mark gutter. A zone that swallowed
//     them would trade a hard-to-hit seam for a control with no second
//     mouse path at all. The git panels' internal seams took both
//     neighbours because both were verifiably blank there; a window
//     seam cannot make that claim, so it doesn't.
//
//   - A PLAIN RULE READS AS A BORDER, NOT AS SOMETHING YOU CAN SEIZE.
//     The middle three rows carry a heavier glyph a step up in color,
//     so the difference is in WEIGHT rather than only in hue — a border
//     and a handle have to be tellable apart at a glance on a terminal
//     whose contrast ced cannot vouch for. Grabbing brightens the whole
//     rule to Accent, which is the state the grip does not need to say.
//
// The ceiling half of that fix — stating a pane's maximum as the
// reserve its NEIGHBOUR keeps rather than as a constant of its own — is
// how every dock clamps (clampToolWidth: `a.width -
// minEditorAfterDrag`, minus whatever the opposite edge spends), so
// there was nothing to port there.

package app

import "github.com/gdamore/tcell/v2"

// splitterGrip is the glyph the middle rows of a seam carry — a heavy
// vertical, single-width per the marker rule.
const splitterGrip = '┃'

// splitterRule is the seam's ordinary glyph: a light vertical, the same
// rule every border in the editor is drawn with.
const splitterRule = '│'

// splitterIsGrip reports whether row `row` of a `rows`-tall seam belongs
// to its grip segment — the middle three rows. A seam too short for the
// grip to sit clear of both ends keeps a plain rule: there, the whole
// thing is short enough to read as one handle already. Shared with both
// git panels, whose internal seams are the same affordance.
func splitterIsGrip(row, rows int) bool {
	const grip = 3
	if rows < grip+2 {
		return false
	}
	top := (rows - grip) / 2
	return row >= top && row < top+grip
}

// splitterHit reports whether a press at column x grabs the window seam
// drawn at column dx: the rule itself, or the column to its left (see
// the file header for why the right one is never taken). A dx < 0 is
// the "no seam" every splitterX helper returns when its panel is
// closed, so a hidden panel can never claim a drag.
func splitterHit(dx, x int) bool {
	return dx >= 0 && x >= dx-1 && x <= dx
}

// drawVSplitter paints a full-height vertical seam at column x: the
// rule, the grip in its middle rows, and the whole thing in Accent
// while `active` (the drag is live, so the grip has nothing left to
// say and the rule reads as one lit handle). A negative x is the
// closed-panel no-op.
//
// The three window seams share this rather than each painting their own
// loop — they had already converged on the same colors, and a grip that
// appeared on only two of three would read as a difference in kind
// between panels that resize identically.
func (a *App) drawVSplitter(x int, active bool) {
	if x < 0 {
		return
	}
	fg := a.theme.Subtle
	if active {
		fg = a.theme.Accent
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg)
	gripStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)

	// The status bar owns the bottom row and the bottom tool stripe (when
	// there is one) the row above it, so the seam runs to whatever those
	// leave and the grip is centred on THAT extent — not on the window's.
	rows := a.sideDockRows()
	for y := 0; y < rows; y++ {
		glyph, st := splitterRule, style
		if !active && splitterIsGrip(y, rows) {
			glyph, st = splitterGrip, gripStyle
		}
		a.screen.SetContent(x, y, glyph, nil, st)
	}
}

// dockSplitterHit reports whether a press at column x grabs the seam
// beside a vertical dock. It replaced one hit-tester per PANEL — the
// sidebar's, the terminal strip's, the chat strip's — with one per EDGE,
// which is the change that made every panel movable: a seam belongs to
// the edge, and the tool showing there is whatever the layout says.
//
// THE BORROWED COLUMN MIRRORS. The file header's rule is that the extra
// cell is taken from the PANEL side, never from the editor band, because
// the panel stops a column short of its rule in every layout while the
// band's first column can carry a deliberate one-cell control. On a LEFT
// dock the panel is to the seam's left, which is the case that rule was
// written for; on a RIGHT dock it is to its right, so the zone flips with
// it. Taking the left cell on both edges would spend the editor's last
// column — exactly what the rule forbids.
func (a *App) dockSplitterHit(side dockSide, x, y int) bool {
	dx := a.toolSplitterX(side)
	if dx < 0 {
		return false
	}
	// THE ROW MATTERS, because the bottom dock wins the corners: it
	// spans the whole window and the side docks stop above it, so this
	// column is the bottom panel's own content below that line. A
	// column-only test claimed a press inside the git panel as a sidebar
	// drag — the seam has to end where the panel it resizes ends.
	if y < 0 || y >= a.sideDockRows() {
		return false
	}
	if side == dockRight {
		return x >= dx && x <= dx+1
	}
	return splitterHit(dx, x)
}

// dockSplitterAt reports which edge's seam a press at column x grabs, if
// any. The click router asks this ONE question instead of testing three
// panels in a fixed order, so a seam can never be shadowed by whichever
// panel happened to be checked first.
func (a *App) dockSplitterAt(x, y int) (dockSide, bool) {
	for _, side := range []dockSide{dockLeft, dockRight} {
		if a.dockSplitterHit(side, x, y) {
			return side, true
		}
	}
	return dockNone, false
}

// sidebarSplitterHit reports whether a press at column x grabs the file
// tree's seam. Kept as a named helper because the tree's own tests and
// the auto-fit lock read it, but it is now just "the seam of whichever
// edge the Project tool is docked to".
func (a *App) sidebarSplitterHit(x, y int) bool {
	side := a.toolDock(toolProject)
	if !dockIsVertical(side) || !a.sidebarShown {
		return false
	}
	return a.dockSplitterHit(side, x, y)
}

// drawDockSplitters paints the seam beside each vertical dock that has
// something showing. One call for both edges, so a new edge would have
// one place to be added rather than one draw function per panel.
func (a *App) drawDockSplitters() {
	for _, side := range []dockSide{dockLeft, dockRight} {
		a.drawVSplitter(a.toolSplitterX(side), a.dragMode == dragModeForDock(side))
	}
}

// dragModeForDock names the drag a seam starts. The modes stay STRINGS
// on App.dragMode beside the editor's and the panels' own, so the click
// router's shape is unchanged; they are just derived from the edge now
// instead of from which panel was open.
func dragModeForDock(side dockSide) string {
	switch side {
	case dockLeft:
		return "docksplit-left"
	case dockRight:
		return "docksplit-right"
	case dockBottom:
		return "docksplit-bottom"
	}
	return ""
}

// dockForDragMode is dragModeForDock's inverse: which edge a live drag
// belongs to, and whether it is a dock drag at all. The router asks it
// once instead of testing three mode strings by name.
func dockForDragMode(mode string) (dockSide, bool) {
	for _, side := range dockSides {
		if dragModeForDock(side) == mode {
			return side, true
		}
	}
	return dockNone, false
}
