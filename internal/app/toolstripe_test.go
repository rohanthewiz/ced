// =============================================================================
// File: internal/app/toolstripe_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// stripeApp is the fixture for stripe tests: newTestApp leaves the
// preference OFF (so every geometry test in the package keeps measuring
// the layout it was written against), and these tests are the ones that
// want it on.
func stripeApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.toolStripes = true
	return a
}

// TestStripeCells_OnlyOnPopulatedEdges pins the rail's cost: one cell on
// an edge that has tools assigned, nothing at all on one that does not.
// That is what lets a user who moves every tool to the bottom get both
// side columns back without there being a preference for it.
func TestStripeCells_OnlyOnPopulatedEdges(t *testing.T) {
	a := stripeApp(t)

	if got := a.stripeCols(dockLeft); got != 1 {
		t.Errorf("left stripe = %d columns, want 1 (the Project tool lives there)", got)
	}
	if got := a.stripeRows(); got != 1 {
		t.Errorf("bottom stripe = %d rows, want 1", got)
	}

	// Empty the left edge: its column goes with it.
	a.moveTool(toolProject, dockBottom)
	if got := a.stripeCols(dockLeft); got != 0 {
		t.Errorf("an empty edge should draw no stripe, got %d columns", got)
	}

	// And the preference switches all of them off.
	a.moveTool(toolProject, dockLeft)
	a.toolStripes = false
	if a.stripeCols(dockLeft)+a.stripeCols(dockRight)+a.stripeRows() != 0 {
		t.Error("stripes off should cost no cells on any edge")
	}
}

// TestStripeButtons_DrawAndHitTestAgree is the btnRect house rule as it
// applies here: every button the enumerator lists is hit-testable at
// exactly its own cell, and a button drawn where nothing is clickable
// would be the worst thing a discovery surface could be.
func TestStripeButtons_DrawAndHitTestAgree(t *testing.T) {
	a := stripeApp(t)
	btns := a.stripeButtons()
	if len(btns) != len(toolDefs) {
		t.Fatalf("%d buttons for %d tools — every tool should have one", len(btns), len(toolDefs))
	}
	seen := map[[2]int]toolID{}
	for _, b := range btns {
		cell := [2]int{b.x, b.y}
		if other, dup := seen[cell]; dup {
			t.Errorf("%q and %q share cell %v", b.id, other, cell)
		}
		seen[cell] = b.id

		got, ok := a.stripeButtonAt(b.x, b.y)
		if !ok || got != b.id {
			t.Errorf("stripeButtonAt(%d,%d) = %q (%v), want %q", b.x, b.y, got, ok, b.id)
		}
		if !a.onStripeRail(b.x, b.y) {
			t.Errorf("%q's button at (%d,%d) is not on a rail", b.id, b.x, b.y)
		}
	}
}

// TestStripeButtons_LandOnTheDrawnEdges pins each button's side: left
// buttons on column 0, right buttons on the last column, bottom buttons
// on the row above the status bar. It is the geometry the layout helpers
// subtract for, so a button off its own rail would mean the reserved
// cell and the drawn one had parted company.
func TestStripeButtons_LandOnTheDrawnEdges(t *testing.T) {
	a := stripeApp(t)
	for _, b := range a.stripeButtons() {
		switch a.toolDock(b.id) {
		case dockLeft:
			if b.x != 0 {
				t.Errorf("%q is left-docked but sits at x=%d", b.id, b.x)
			}
		case dockRight:
			if b.x != a.width-1 {
				t.Errorf("%q is right-docked but sits at x=%d, want %d", b.id, b.x, a.width-1)
			}
		case dockBottom:
			if b.y != a.stripeRow() {
				t.Errorf("%q is bottom-docked but sits at y=%d, want %d", b.id, b.y, a.stripeRow())
			}
		}
	}
}

// TestStripeClick_TogglesAndSwallows covers the gesture: a press on a
// button toggles that tool, a press on the bare rail is swallowed (the
// rail is chrome and must not fall through to the editor behind it), and
// a press anywhere else is not claimed.
func TestStripeClick_TogglesAndSwallows(t *testing.T) {
	a := stripeApp(t)
	a.gitIsRepo = true

	var gitBtn stripeButton
	for _, b := range a.stripeButtons() {
		if b.id == toolGit {
			gitBtn = b
		}
	}
	if !a.stripeClick(gitBtn.x, gitBtn.y) {
		t.Fatal("a press on a button should be claimed")
	}
	if !a.gitPanel.open {
		t.Error("the button should have shown the git panel")
	}
	if !a.stripeClick(gitBtn.x, gitBtn.y) || a.gitPanel.open {
		t.Error("a second press should hide it again")
	}

	// The bare rail, well past the last button on the bottom edge.
	if !a.stripeClick(a.width-3, a.stripeRow()) {
		t.Error("the bare rail should swallow a press")
	}
	// The editor body is not the rail.
	if a.stripeClick(a.width/2, 5) {
		t.Error("a press in the editor must not be claimed by the stripe")
	}
}

// TestStripeClick_DoesNotLatchOnARefusal pins the reason stripeClick
// reads showTool's answer instead of assuming it: a tool can refuse to
// come up, and a button lit over a panel that never opened would be the
// stripe lying about the layout it exists to describe.
func TestStripeClick_DoesNotLatchOnARefusal(t *testing.T) {
	a := stripeApp(t)
	a.chat.dead = true // newTestApp's default; stated here because it IS the case

	var chatBtn stripeButton
	for _, b := range a.stripeButtons() {
		if b.id == toolChat {
			chatBtn = b
		}
	}
	a.stripeClick(chatBtn.x, chatBtn.y)
	if a.chat.open {
		t.Error("a chat with no agent must not open from the stripe")
	}
	if a.toolOpen(toolChat) {
		t.Error("the button must not read as shown")
	}
}

// TestDrawToolStripes_PaintsTheGlyphs renders the stripes on the
// simulation screen and checks each button's own glyph landed on its own
// cell, with the showing tool in Accent and the rest Muted. Colour is
// the whole state a one-cell button can carry.
func TestDrawToolStripes_PaintsTheGlyphs(t *testing.T) {
	a := stripeApp(t)
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()

	for _, b := range a.stripeButtons() {
		d, _ := toolDefFor(b.id)
		c := cells[b.y*w+b.x]
		if len(c.Runes) == 0 || c.Runes[0] != d.glyph {
			t.Errorf("%q: cell (%d,%d) = %q, want its glyph %q", b.id, b.x, b.y, c.Runes, d.glyph)
			continue
		}
		fg, _, _ := c.Style.Decompose()
		want := a.theme.Muted
		if a.toolOpen(b.id) {
			want = a.theme.Accent
		}
		if fg != want {
			t.Errorf("%q: fg = %v, want %v (open=%v)", b.id, fg, want, a.toolOpen(b.id))
		}
	}
}

// TestStripes_ShiftTheLayoutByExactlyTheirCells pins that the reserved
// cells and the drawn ones are the same cells: turning stripes on moves
// the editor band in by one column per populated vertical edge and up by
// one row for a populated bottom edge, and nothing else changes.
func TestStripes_ShiftTheLayoutByExactlyTheirCells(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x0, y0, w0, h0 := a.editorRect()

	a.toolStripes = true
	x1, y1, w1, h1 := a.editorRect()

	if x1-x0 != 1 {
		t.Errorf("editor x moved by %d, want 1 (the left stripe)", x1-x0)
	}
	if w0-w1 != 2 {
		t.Errorf("editor width shrank by %d, want 2 (both vertical stripes)", w0-w1)
	}
	if y1 != y0 {
		t.Errorf("editor y moved to %d, want %d — a stripe takes rows from the BOTTOM", y1, y0)
	}
	if h0-h1 != 1 {
		t.Errorf("editor height shrank by %d, want 1 (the bottom stripe)", h0-h1)
	}
}

// TestStripeRow_IsStableWhenTheFindBarOpens pins the reason the bottom
// rail sits directly above the status bar rather than hugging the dock
// it labels: chrome that shifts is chrome you have to look for.
func TestStripeRow_IsStableWhenTheFindBarOpens(t *testing.T) {
	a := stripeApp(t)
	before := a.stripeRow()
	a.findOpen = true
	if got := a.stripeRow(); got != before {
		t.Errorf("stripe row moved to %d when the find bar opened, want %d", got, before)
	}
}
