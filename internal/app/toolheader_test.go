// =============================================================================
// File: internal/app/toolheader_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// bottomTreeApp docks the file tree at the bottom and shows it there —
// the arrangement the generic header exists for.
func bottomTreeApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	// Real entries, so the click-mapping test has rows to land on.
	if err := os.Mkdir(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.go"), []byte("package beta\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestApp(t, dir)
	a.moveTool(toolProject, dockBottom)
	a.showTool(toolProject)
	return a
}

// TestToolNeedsHeader_OnlyTheHeaderlessOneAtTheBottom pins who gets the
// generic header: a tool that does not paint its own, docked at the
// bottom. Six of the seven were born as bottom strips and arrived with a
// rule, a title and a ✕ of their own; giving them a second one would
// draw two headers.
func TestToolNeedsHeader_OnlyTheHeaderlessOneAtTheBottom(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true

	// The tree on a vertical edge: no header. The seam resizes it and
	// its own first row is its title.
	if a.toolNeedsHeader(toolProject) {
		t.Error("a left-docked tree should not get the dock header")
	}
	// A panel that draws its own, at the bottom: still no.
	a.showTool(toolGit)
	if a.toolNeedsHeader(toolGit) {
		t.Error("the git panel paints its own header and must not get a second")
	}
	// The tree at the bottom: yes.
	a.moveTool(toolProject, dockBottom)
	a.showTool(toolProject)
	if !a.toolNeedsHeader(toolProject) {
		t.Error("a bottom-docked tree should get the dock header")
	}
	// And a hidden tool has nothing to put a header on.
	a.hideTool(toolProject)
	if a.toolNeedsHeader(toolProject) {
		t.Error("a hidden tool should not claim a header row")
	}
}

// TestToolBodyRect_HeaderComesOutOfTheDock pins the split every other
// consumer depends on: toolRect is the whole dock (header included, the
// way a hand-drawn panel's rect includes its own), and toolBodyRect is
// what the panel's CONTENT gets. sidebarRect returns the body, which is
// why the tree's hit-testing, marks and overflow markers needed no
// changes at all.
func TestToolBodyRect_HeaderComesOutOfTheDock(t *testing.T) {
	a := bottomTreeApp(t)

	dx, dy, dw, dh := a.toolRect(toolProject)
	bx, by, bw, bh := a.toolBodyRect(toolProject)
	if bx != dx || bw != dw {
		t.Errorf("body columns = [%d,%d), want the dock's [%d,%d)", bx, bx+bw, dx, dx+dw)
	}
	if by != dy+1 || bh != dh-1 {
		t.Errorf("body rows = [%d,%d), want the dock's minus one header row [%d,%d)",
			by, by+bh, dy+1, dy+dh)
	}

	sx, sy, sw, sh := a.sidebarRect()
	if sx != bx || sy != by || sw != bw || sh != bh {
		t.Errorf("sidebarRect = (%d,%d,%d,%d), want the body rect (%d,%d,%d,%d)",
			sx, sy, sw, sh, bx, by, bw, bh)
	}

	hx, hy, hw, hh := a.toolHeaderRect(toolProject)
	if hy != dy || hh != 1 || hx != dx || hw != dw {
		t.Errorf("header = (%d,%d,%d,%d), want the dock's first row", hx, hy, hw, hh)
	}
}

// TestToolBodyRect_NoHeaderIsTheWholeDock: on a vertical edge, and for
// every panel that draws its own, the body IS the dock.
func TestToolBodyRect_NoHeaderIsTheWholeDock(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	dx, dy, dw, dh := a.toolRect(toolProject)
	bx, by, bw, bh := a.toolBodyRect(toolProject)
	if bx != dx || by != dy || bw != dw || bh != dh {
		t.Errorf("body = (%d,%d,%d,%d), want the whole dock (%d,%d,%d,%d)",
			bx, by, bw, bh, dx, dy, dw, dh)
	}
}

// TestDrawToolHeader_RuleTitleAndClose renders the header and checks all
// three parts landed: the rule across the dock, the panel's name, and
// the ✕ exactly where toolHeaderCloseRect points (the btnRect rule — a
// button drawn where nothing is clickable is the worst kind).
func TestDrawToolHeader_RuleTitleAndClose(t *testing.T) {
	a := bottomTreeApp(t)
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()

	hx, hy, hw, _ := a.toolHeaderRect(toolProject)
	var row strings.Builder
	for x := hx; x < hx+hw; x++ {
		if c := cells[hy*w+x]; len(c.Runes) > 0 {
			row.WriteRune(c.Runes[0])
		}
	}
	got := row.String()
	if !strings.Contains(got, "Explorer") {
		t.Errorf("header row = %q, want the panel's name", got)
	}
	if !strings.Contains(got, "─") {
		t.Errorf("header row = %q, want the rule", got)
	}
	btn := a.toolHeaderCloseRect(toolProject)
	if c := cells[btn.y*w+btn.x+1]; len(c.Runes) == 0 || c.Runes[0] != '✕' {
		t.Errorf("✕ is not where toolHeaderCloseRect points (x=%d)", btn.x+1)
	}
}

// TestToolHeaderTitle_CarriesTheMarkCount pins the one thing the header
// has to say beyond the name. The tree's own EXPLORER row is suppressed
// under this header, and that row was the mark set's ONLY always-visible
// surface — marks survive scrolling and folding, so without a count
// somewhere a user could run a delete over rows nowhere on screen.
func TestToolHeaderTitle_CarriesTheMarkCount(t *testing.T) {
	a := bottomTreeApp(t)
	if got := a.toolHeaderTitle(toolProject); strings.Contains(got, "marked") {
		t.Errorf("title = %q with nothing marked, want just the name", got)
	}
	a.tree.Marked = map[string]bool{"/a": true, "/b": true}
	if got := a.toolHeaderTitle(toolProject); !strings.Contains(got, "2 marked") {
		t.Errorf("title = %q, want the mark count", got)
	}
}

// TestToolHeaderPress_CloseAndDrag covers both gestures the header
// carries: the ✕ puts the panel away, and the rule anywhere else seizes
// the bottom edge's height drag — the arrangement every other bottom
// panel already uses, so a user who has dragged one has dragged them all.
func TestToolHeaderPress_CloseAndDrag(t *testing.T) {
	a := bottomTreeApp(t)
	hx, hy, _, _ := a.toolHeaderRect(toolProject)

	if mode := a.toolHeaderPress(toolProject, hx+1, hy); mode != dragModeForDock(dockBottom) {
		t.Errorf("press on the rule started %q, want the bottom edge's drag", mode)
	}
	if !a.sidebarShown {
		t.Error("a drag press must not close the panel")
	}

	btn := a.toolHeaderCloseRect(toolProject)
	if mode := a.toolHeaderPress(toolProject, btn.x+1, btn.y); mode != "" {
		t.Errorf("the ✕ started drag %q, want none", mode)
	}
	if a.sidebarShown {
		t.Error("the ✕ should have hidden the panel")
	}
}

// TestHandleMouse_ToolHeaderRoutesBeforeTheBody drives the real click
// path. The header is claimed before the panel it belongs to, because
// its rule is the drag handle and its ✕ is the only mouse route to
// putting the panel away — neither of which the tree's own hit-test
// knows anything about.
func TestHandleMouse_ToolHeaderRoutesBeforeTheBody(t *testing.T) {
	a := bottomTreeApp(t)
	hx, hy, _, _ := a.toolHeaderRect(toolProject)

	a.handleMouse(tcell.NewEventMouse(hx+2, hy, tcell.Button1, tcell.ModNone))
	if want := dragModeForDock(dockBottom); a.dragMode != want {
		t.Fatalf("dragMode = %q, want %q", a.dragMode, want)
	}
	// Dragging up grows the panel, glued to the row under the pointer.
	a.handleMouse(tcell.NewEventMouse(hx+2, hy-3, tcell.Button1, tcell.ModNone))
	if got, want := a.toolHeight(toolProject), a.height-1-(hy-3); got != want {
		t.Errorf("height after drag = %d, want %d", got, want)
	}
	a.handleMouse(tcell.NewEventMouse(hx+2, hy-3, tcell.ButtonNone, tcell.ModNone))

	// And the ✕ through the same path.
	btn := a.toolHeaderCloseRect(toolProject)
	a.handleMouse(tcell.NewEventMouse(btn.x+1, btn.y, tcell.Button1, tcell.ModNone))
	if a.sidebarShown {
		t.Error("clicking the ✕ should have hidden the tree")
	}
}

// TestBottomTree_HeaderReplacesTheExplorerRow pins why the tree's own
// label is suppressed rather than stacked under the dock's: two rows of
// title on a ten-row panel is a third of it spent saying the same thing
// twice. The project name is the body's first row either way.
func TestBottomTree_HeaderReplacesTheExplorerRow(t *testing.T) {
	a := bottomTreeApp(t)
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()

	readRow := func(y int) string {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			if c := cells[y*w+x]; len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			}
		}
		return strings.TrimRight(sb.String(), " ")
	}
	_, by, _, _ := a.sidebarRect()
	if got := readRow(by); strings.Contains(got, "EXPLORER") {
		t.Errorf("body's first row = %q, want the project name — not a second label", got)
	}
	if got := readRow(by); !strings.Contains(got, a.tree.Root.Name) {
		t.Errorf("body's first row = %q, want the project name %q", got, a.tree.Root.Name)
	}
	if !a.tree.HideLabel {
		t.Error("the draw should have suppressed the tree's own label")
	}
}

// TestBottomTree_ClicksStillLandOnTheRightRow is the bug this whole
// arrangement could most easily have: the header shifts the body down a
// row AND the tree drops a row of its own, so a click map that did not
// follow both would open the neighbour of whatever was clicked.
func TestBottomTree_ClicksStillLandOnTheRightRow(t *testing.T) {
	a := bottomTreeApp(t)
	a.draw() // sets HideLabel and fills the tree's visible rows

	bx, by, _, _ := a.sidebarRect()
	// The body's first row is the project root — clicking it resets the
	// active folder to the root.
	a.activeFolder = "/somewhere/else"
	a.handleMouse(tcell.NewEventMouse(bx+1, by, tcell.Button1, tcell.ModNone))
	if a.activeFolder != a.rootDir {
		t.Errorf("activeFolder = %q after clicking the root row, want %q", a.activeFolder, a.rootDir)
	}
	a.handleMouse(tcell.NewEventMouse(bx+1, by, tcell.ButtonNone, tcell.ModNone))

	// And the row under it is the first child, not the second.
	if len(a.tree.Root.Children) == 0 {
		t.Skip("no children to click")
	}
	want := a.tree.Root.Children[0]
	got, ok := a.tree.HitTest(1, 1)
	if !ok || got != want {
		t.Errorf("row under the root maps to %v, want the first child %q", got, want.Name)
	}
}
