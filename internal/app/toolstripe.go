// =============================================================================
// File: internal/app/toolstripe.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toolstripe.go draws the TOOL BUTTONS on the window's edges — the thin
// rail JetBrains calls a stripe. One cell per edge that has tools
// assigned to it; each tool on that edge gets a button, and the one
// currently showing wears the accent.
//
// WHY IT IS WORTH A COLUMN. The tool windows are single-occupancy per
// edge (toolwindow.go), which means most of them are collapsed most of
// the time — and a collapsed panel with no button is a feature the user
// has to remember exists. ced already knows what that costs: the whole
// reason the ≡ menu carries every file action is that macOS Terminal
// swallows right-click, and "a row nobody can find is worse than a row
// that explains itself" is the rule that put "Open in $EDITOR" back in
// the popup unconditionally. A stripe is that rule applied to panels: it
// is the only surface in the editor that says, without being opened,
// WHAT tool windows there are and WHERE each one lives.
//
// HOUSE RULES:
//
//   - IT COSTS ONE CELL, AND ONLY ON AN EDGE THAT HAS TOOLS. An edge
//     nobody has assigned anything to draws nothing and takes nothing —
//     which is what lets a user who moves every tool to the bottom get
//     both side columns back without a preference.
//
//   - THE GLYPH IS SINGLE-WIDTH, per the marker rule. runeLen counts
//     runes, so a double-width emoji here would overrun the stripe into
//     the panel beside it.
//
//   - DRAW AND HIT-TEST SHARE ONE ENUMERATOR (stripeButtons), the
//     btnRect house rule. A button drawn where nothing is clickable is
//     the single worst thing a discovery surface can be.
//
//   - THE VERTICAL STRIPES ARE OUTERMOST AND FULL HEIGHT, so a button
//     never moves. The bottom one is the row directly above the status
//     bar for the same reason — the find bar opens ABOVE it rather than
//     pushing it down, because chrome that shifts is chrome you have to
//     look for.
//
//   - A BUTTON REPORTS WHAT ACTUALLY HAPPENED. showTool can refuse (an
//     agent with no binary, a compare with nothing to compare), so the
//     click reads its answer instead of assuming; a button that latched
//     on over a panel that never opened would be the stripe lying about
//     the layout it exists to describe.
//
// The `"toolstripes"` config key (default on) is the way out for anyone
// who wants the cells back and is content to reach the tools from the ≡
// menu. It is a preference about CHROME, which is the same thing the
// retired scrollbar key was — the difference is that this one has
// something to give back, since the stripe really does reserve its cell.

package app

import "github.com/gdamore/tcell/v2"

// stripeButton is one drawn tool button: which tool, and the cell it
// occupies. Draw and hit-testing both read a slice of these, so the two
// cannot disagree about where a button is.
type stripeButton struct {
	id   toolID
	x, y int
}

// stripesEnabled reports whether the tool stripes are drawn at all. A
// hand-built App (the newTestApp shape) leaves the field false, which
// keeps every existing geometry test measuring the layout it was written
// against — the stripe is opt-in for tests and on by default for users,
// resolved from config at startup.
func (a *App) stripesEnabled() bool { return a.toolStripes }

// stripeCols is how many columns a vertical edge's stripe consumes: one
// when stripes are on and that edge has at least one tool assigned, zero
// otherwise. Every layout helper goes through this rather than testing
// the preference itself, so turning stripes off reshapes the window in
// one place.
func (a *App) stripeCols(side dockSide) int {
	if !dockIsVertical(side) || !a.stripesEnabled() {
		return 0
	}
	if len(a.toolsOn(side)) == 0 {
		return 0
	}
	return 1
}

// stripeRows is stripeCols for the bottom edge.
func (a *App) stripeRows() int {
	if !a.stripesEnabled() || len(a.toolsOn(dockBottom)) == 0 {
		return 0
	}
	return 1
}

// stripeRow is the screen row the bottom stripe is drawn on — directly
// above the status bar. Meaningless (and never read) when stripeRows is
// zero.
func (a *App) stripeRow() int { return a.height - 2 }

// stripeButtons enumerates every drawn tool button, in registry order
// within each edge. THE one geometry source: drawToolStripes paints
// exactly this, stripeButtonAt hit-tests exactly this, and a tool whose
// button did not fit is simply absent from both.
//
// Vertical stripes stack one glyph per row from the top. The bottom
// stripe runs left to right and spends TWO cells per tool — the glyph
// and a separating space — because it has the width to and because a row
// of glyphs jammed together reads as one word rather than as buttons.
func (a *App) stripeButtons() []stripeButton {
	if !a.stripesEnabled() {
		return nil
	}
	var out []stripeButton

	// The vertical stripes run the full height of the window above the
	// bottom chrome, so a button's row is stable whatever else opens.
	rows := a.height - 1 - a.stripeRows()
	for _, side := range []dockSide{dockLeft, dockRight} {
		if a.stripeCols(side) == 0 {
			continue
		}
		x := 0
		if side == dockRight {
			x = a.width - 1
		}
		for i, id := range a.toolsOn(side) {
			if i >= rows {
				break // A window too short for the whole edge simply shows fewer.
			}
			out = append(out, stripeButton{id: id, x: x, y: i})
		}
	}

	if a.stripeRows() > 0 {
		x := a.stripeCols(dockLeft) + 1 // one cell of left margin
		limit := a.width - a.stripeCols(dockRight)
		y := a.stripeRow()
		for _, id := range a.toolsOn(dockBottom) {
			if x >= limit {
				break
			}
			out = append(out, stripeButton{id: id, x: x, y: y})
			x += 2
		}
	}
	return out
}

// stripeButtonAt reports which tool's button sits at (x, y), if any.
func (a *App) stripeButtonAt(x, y int) (toolID, bool) {
	for _, b := range a.stripeButtons() {
		if b.x == x && b.y == y {
			return b.id, true
		}
	}
	return "", false
}

// drawToolStripes paints the stripes: the rail's background on every
// cell of an active edge (so it reads as one continuous strip rather
// than as loose glyphs floating over the editor), then each button.
//
// The showing tool wears Accent; the rest are Muted. That is the whole
// state a button has to carry — "this is the one you are looking at" —
// and it is carried in COLOR rather than in a second glyph, because the
// cell has room for exactly one rune.
func (a *App) drawToolStripes() {
	if !a.stripesEnabled() {
		return
	}
	railStyle := tcell.StyleDefault.Background(a.theme.StatusBG).Foreground(a.theme.Subtle)

	rows := a.height - 1 - a.stripeRows()
	for _, side := range []dockSide{dockLeft, dockRight} {
		if a.stripeCols(side) == 0 {
			continue
		}
		x := 0
		if side == dockRight {
			x = a.width - 1
		}
		for y := 0; y < rows; y++ {
			a.screen.SetContent(x, y, ' ', nil, railStyle)
		}
	}
	if a.stripeRows() > 0 {
		y := a.stripeRow()
		for x := a.stripeCols(dockLeft); x < a.width-a.stripeCols(dockRight); x++ {
			a.screen.SetContent(x, y, ' ', nil, railStyle)
		}
		// The corners belong to whichever vertical stripe is there, so
		// the rail turns rather than breaking.
		if a.stripeCols(dockLeft) > 0 {
			a.screen.SetContent(0, y, ' ', nil, railStyle)
		}
		if a.stripeCols(dockRight) > 0 {
			a.screen.SetContent(a.width-1, y, ' ', nil, railStyle)
		}
	}

	for _, b := range a.stripeButtons() {
		d, ok := toolDefFor(b.id)
		if !ok {
			continue
		}
		fg := a.theme.Muted
		if a.toolOpen(b.id) {
			fg = a.theme.Accent
		}
		st := tcell.StyleDefault.Background(a.theme.StatusBG).Foreground(fg)
		if a.toolOpen(b.id) {
			st = st.Bold(true)
		}
		a.screen.SetContent(b.x, b.y, d.glyph, nil, st)
	}
}

// stripeClick handles a left press on a stripe button: it toggles that
// tool and flashes its name and edge, because the glyph alone cannot say
// either. Reports whether the press was claimed — a press on the rail
// but not on a button is still claimed (the rail is chrome, and a click
// on it must not fall through to the editor behind it).
func (a *App) stripeClick(x, y int) bool {
	if !a.stripesEnabled() {
		return false
	}
	if id, ok := a.stripeButtonAt(x, y); ok {
		side := a.toolDock(id)
		if a.toggleTool(id) {
			a.flash(toolTitle(id) + " — " + dockLabel(side))
		} else if !a.toolOpen(id) {
			a.flash(toolTitle(id) + " hidden")
		}
		return true
	}
	return a.onStripeRail(x, y)
}

// onStripeRail reports whether (x, y) lands on a stripe's rail at all —
// button or bare cell. The click router uses it to swallow presses that
// hit the rail's empty part, and the scroll router to leave them alone.
func (a *App) onStripeRail(x, y int) bool {
	if !a.stripesEnabled() {
		return false
	}
	if a.stripeRows() > 0 && y == a.stripeRow() {
		return true
	}
	if y >= a.height-1-a.stripeRows() {
		return false
	}
	if a.stripeCols(dockLeft) > 0 && x == 0 {
		return true
	}
	if a.stripeCols(dockRight) > 0 && x == a.width-1 {
		return true
	}
	return false
}
