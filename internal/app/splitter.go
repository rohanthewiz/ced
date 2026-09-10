// =============================================================================
// File: internal/app/splitter.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// splitter.go is the one place a resizable WINDOW seam is described —
// the three vertical rules the user drags to re-apportion the screen's
// columns: the sidebar's, a left-docked terminal strip's, and the chat
// strip's. It also holds the grip glyph and the middle-rows test the
// git panels' internal list/diff seams share.
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
// already how all three window seams clamp (`a.width -
// minEditorAfterDrag`, minus whatever strip owns the other edge), so
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

	// The status bar owns the bottom row, so the seam runs to height-1
	// and the grip is centred on that extent — not on the window's.
	rows := a.height - 1
	for y := 0; y < rows; y++ {
		glyph, st := splitterRule, style
		if !active && splitterIsGrip(y, rows) {
			glyph, st = splitterGrip, gripStyle
		}
		a.screen.SetContent(x, y, glyph, nil, st)
	}
}

// sidebarSplitterHit reports whether a press at column x grabs the
// sidebar's seam. Classic layout borrows the file tree's row tail;
// flipped, it borrows the editor band's last column — code, or the
// blank margin a docked panel's right-hand pane already leaves.
func (a *App) sidebarSplitterHit(x int) bool {
	return splitterHit(a.splitterX(), x)
}

// termSplitterHit reports whether a press at column x grabs a
// left-docked terminal strip's seam. The strip's own rect stops a
// column short of it (termPanelRect), so the borrowed cell is margin.
func (a *App) termSplitterHit(x int) bool {
	return splitterHit(a.termSplitterX(), x)
}

// chatSplitterHit reports whether a press at column x grabs the chat
// strip's seam. Same margin as the terminal's, and the transcript's ⧉
// action buttons stop a further column short of it (chatActionRect), so
// nothing clickable is spent.
func (a *App) chatSplitterHit(x int) bool {
	return splitterHit(a.chatSplitterX(), x)
}
