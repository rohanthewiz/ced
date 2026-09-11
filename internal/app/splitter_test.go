// =============================================================================
// File: internal/app/splitter_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestSplitterIsGrip pins the grip segment's placement: the middle three
// rows of a seam tall enough to sit them clear of both ends, and nothing
// at all on a short one, where the whole rule already reads as a handle.
func TestSplitterIsGrip(t *testing.T) {
	// 11 rows → grip on rows 4,5,6.
	for row := 0; row < 11; row++ {
		want := row >= 4 && row <= 6
		if got := splitterIsGrip(row, 11); got != want {
			t.Errorf("row %d of 11: grip=%v, want %v", row, got, want)
		}
	}
	for row := 0; row < 4; row++ {
		if splitterIsGrip(row, 4) {
			t.Errorf("row %d of a 4-row seam should carry no grip", row)
		}
	}
}

// TestSplitterHit_BorrowsOnlyTheColumnOnItsLeft pins the window seam's
// grab zone. Two columns, not one — a single cell is a coin flip with a
// mouse — and never the column on the RIGHT, which belongs to the editor
// band and can carry a docked panel's one-cell controls.
func TestSplitterHit_BorrowsOnlyTheColumnOnItsLeft(t *testing.T) {
	const dx = 20
	for _, off := range []int{-1, 0} {
		if !splitterHit(dx, dx+off) {
			t.Errorf("column dx%+d should be inside the grab zone", off)
		}
	}
	for _, off := range []int{-2, 1, 2} {
		if splitterHit(dx, dx+off) {
			t.Errorf("column dx%+d should be outside the grab zone", off)
		}
	}
	// A closed panel reports -1 and must own no column at all — least of
	// all the screen's leftmost two.
	for _, x := range []int{-2, -1, 0, 1} {
		if splitterHit(-1, x) {
			t.Errorf("a closed panel claimed column %d", x)
		}
	}
}

// TestSidebarSplitterHit_StartsTheDragFromEitherColumn checks the zone
// through the press router rather than the predicate alone: both cells
// must actually arm the "sidebar" drag, and the editor-band column one
// step right must still fall through to whatever owns it.
func TestSidebarSplitterHit_StartsTheDragFromEitherColumn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	divX := a.splitterX()
	if divX < 1 {
		t.Fatalf("test app has no sidebar seam (splitterX=%d)", divX)
	}
	want := dragModeForDock(dockLeft)
	for _, off := range []int{-1, 0} {
		a.dragMode = ""
		a.handleMouse(tcell.NewEventMouse(divX+off, 5, tcell.Button1, 0))
		if a.dragMode != want {
			t.Errorf("press at divX%+d started %q, want %q", off, a.dragMode, want)
		}
		a.handleMouse(tcell.NewEventMouse(divX+off, 5, tcell.ButtonNone, 0))
	}
	a.dragMode = ""
	a.handleMouse(tcell.NewEventMouse(divX+1, 5, tcell.Button1, 0))
	if a.dragMode == want {
		t.Error("the editor band's first column must not grab the seam")
	}
	a.handleMouse(tcell.NewEventMouse(divX+1, 5, tcell.ButtonNone, 0))

	// Hidden sidebar, no seam: the column it used to occupy is ordinary.
	a.sidebarShown = false
	if a.sidebarSplitterHit(divX, 5) || a.sidebarSplitterHit(divX-1, 5) {
		t.Error("a hidden sidebar should own no grab zone")
	}
}

// TestSidebarSplitterHit_LeavesTheTreeMarkGutterAlone is the flipped
// layout's half of the rule. With the tree docked right its FIRST column
// is the multi-selection's tick gutter — a deliberate one-cell control —
// and it sits immediately right of the seam, which is exactly the cell a
// symmetric grab zone would have swallowed.
func TestSidebarSplitterHit_LeavesTheTreeMarkGutterAlone(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.open = true // whatever owns the left edge flips the tree right
	if !a.treeOnRight() {
		t.Skip("layout did not flip; nothing to pin here")
	}
	sx, _, _, _ := a.sidebarRect()
	if got := a.splitterX(); got != sx-1 {
		t.Fatalf("seam at %d, tree rect starts at %d — assumption broken", got, sx)
	}
	if a.sidebarSplitterHit(sx, 5) {
		t.Error("the tree's mark gutter must keep its column")
	}
	if !a.sidebarSplitterHit(sx-1, 5) || !a.sidebarSplitterHit(sx-2, 5) {
		t.Error("the seam and the editor column left of it should both grab")
	}
}

// TestTermAndChatSplitterHit_TwoColumnZone pins the other two window
// seams. Both strips stop a column short of their rule, so the borrowed
// cell is margin and nothing clickable is spent.
func TestTermAndChatSplitterHit_TwoColumnZone(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if a.dockSplitterHit(a.toolDock(toolTerminal), 0, 5) || a.dockSplitterHit(a.toolDock(toolChat), 0, 5) {
		t.Error("closed strips should own no grab zone")
	}

	a.moveTool(toolTerminal, dockLeft)
	a.term.open = true
	tx := a.toolSplitterX(dockLeft)
	if tx < 1 {
		t.Fatalf("left-docked terminal has no seam (x=%d)", tx)
	}
	if !a.dockSplitterHit(a.toolDock(toolTerminal), tx, 5) || !a.dockSplitterHit(a.toolDock(toolTerminal), tx-1, 5) {
		t.Error("terminal seam should grab from its own column and the one left of it")
	}
	if a.dockSplitterHit(a.toolDock(toolTerminal), tx+1, 5) {
		t.Error("terminal seam must not claim the editor band's first column")
	}
	a.term.open = false
	a.moveTool(toolTerminal, dockBottom)

	// The chat defaults to the RIGHT edge, where the borrowed cell
	// MIRRORS: the panel is to the seam's right, so that is the column
	// the zone may take. Taking the left one there would spend the
	// editor band's last column — the thing the rule forbids.
	a.chat.open = true
	cx := a.chatSplitterX()
	if cx < 1 {
		t.Fatalf("chat strip has no seam (x=%d)", cx)
	}
	if !a.dockSplitterHit(dockRight, cx, 5) || !a.dockSplitterHit(dockRight, cx+1, 5) {
		t.Error("right-edge seam should grab from its own column and the one right of it")
	}
	if a.dockSplitterHit(dockRight, cx-1, 5) {
		t.Error("right-edge seam must not claim the editor band's last column")
	}
}

// TestDragSplitOffset_AnUnmovedGrabChangesNothing is the reason the drag
// carries an offset at all. Seizing the seam by its borrowed column and
// then not moving must leave the width alone — and in particular must
// not trip lockTreeAutoFit, the guard that keeps a press with a pixel of
// jitter from stating a width and writing it to disk.
func TestDragSplitOffset_AnUnmovedGrabChangesNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.treeAutoFit = true
	before := a.sidebarWidth
	divX := a.splitterX()

	a.handleMouse(tcell.NewEventMouse(divX-1, 5, tcell.Button1, 0))
	// A motion event at the very same cell: the drag is live, but the
	// pointer has not actually gone anywhere.
	a.handleMouse(tcell.NewEventMouse(divX-1, 6, tcell.Button1, 0))

	if a.sidebarWidth != before {
		t.Errorf("sidebar width moved to %d from %d on an unmoved grab", a.sidebarWidth, before)
	}
	if !a.treeAutoFit {
		t.Error("an unmoved grab locked auto-fit off")
	}

	// Now really drag: one column right of where it was seized.
	a.handleMouse(tcell.NewEventMouse(divX, 6, tcell.Button1, 0))
	if a.sidebarWidth != before+1 {
		t.Errorf("sidebar width = %d after a one-column drag, want %d", a.sidebarWidth, before+1)
	}
	a.handleMouse(tcell.NewEventMouse(divX, 6, tcell.ButtonNone, 0))
	if a.dragSplitOffset != 0 {
		t.Errorf("release left the grab offset at %d", a.dragSplitOffset)
	}
}

// TestDrawVSplitter_GripThenAccent verifies the affordance reaches the
// screen: an idle seam paints the heavy grip a step up in color across
// its middle rows and a plain rule elsewhere, and a live drag lights the
// whole thing Accent — at which point the grip has nothing left to say.
func TestDrawVSplitter_GripThenAccent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	divX := a.splitterX()
	rows := a.height - 1

	gripRow := -1
	for row := 0; row < rows; row++ {
		if splitterIsGrip(row, rows) {
			gripRow = row
			break
		}
	}
	if gripRow < 0 {
		t.Fatalf("a %d-row seam is too short to carry a grip", rows)
	}

	at := func(row int) (rune, tcell.Color) {
		t.Helper()
		a.screen.Show()
		cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
		c := cells[row*w+divX]
		fg, _, _ := c.Style.Decompose()
		return c.Runes[0], fg
	}

	a.draw()
	if r, fg := at(gripRow); r != splitterGrip || fg != a.theme.Muted {
		t.Errorf("grip row: rune=%q fg=%v, want %q in Muted %v",
			r, fg, splitterGrip, a.theme.Muted)
	}
	if r, fg := at(0); r != splitterRule || fg != a.theme.Subtle {
		t.Errorf("plain row: rune=%q fg=%v, want %q in Subtle %v",
			r, fg, splitterRule, a.theme.Subtle)
	}

	a.dragMode = dragModeForDock(dockLeft)
	a.draw()
	if r, fg := at(gripRow); r != splitterRule || fg != a.theme.Accent {
		t.Errorf("dragging: rune=%q fg=%v, want %q in Accent %v",
			r, fg, splitterRule, a.theme.Accent)
	}
}

// TestDockSplitterHit_EndsAtTheBottomDock is a regression pin. The
// bottom edge wins the corners — it spans the whole window and the side
// docks stop above it — so the seam's column is the BOTTOM PANEL's own
// content below that line. A column-only hit test claimed a press inside
// the git panel as a sidebar drag, which made the panel's left edge
// unclickable and started a resize nobody asked for.
func TestDockSplitterHit_EndsAtTheBottomDock(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit)

	divX := a.splitterX()
	if divX < 1 {
		t.Fatalf("no sidebar seam (splitterX=%d)", divX)
	}
	if !a.sidebarSplitterHit(divX, 2) {
		t.Error("the seam should still grab in the rows the tree occupies")
	}

	gy := a.bottomDockTop()
	if a.sidebarSplitterHit(divX, gy) {
		t.Error("the seam must not claim a press inside the bottom dock")
	}
	a.handleMouse(tcell.NewEventMouse(divX, gy+1, tcell.Button1, tcell.ModNone))
	if _, isDock := dockForDragMode(a.dragMode); isDock {
		t.Errorf("a press in the git panel started %q, want the panel's own gesture", a.dragMode)
	}
}
