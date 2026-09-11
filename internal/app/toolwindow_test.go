// =============================================================================
// File: internal/app/toolwindow_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import "testing"

// TestToolRegistry_WellFormed pins the registry's invariants, all of
// which are things the rest of the feature silently assumes: ids and
// titles are unique (the ≡ pickers list one row per tool, and two rows
// with one name is a bug report), every default edge is real, and every
// tool states both halves of the visibility pair plus both floors.
func TestToolRegistry_WellFormed(t *testing.T) {
	seenID := map[toolID]bool{}
	seenTitle := map[string]toolID{}
	for _, d := range toolDefs {
		if d.id == "" {
			t.Fatal("a tool with no id")
		}
		if seenID[d.id] {
			t.Errorf("duplicate tool id %q", d.id)
		}
		seenID[d.id] = true

		if other, dup := seenTitle[d.title]; dup {
			t.Errorf("%q and %q share the title %q", d.id, other, d.title)
		}
		seenTitle[d.title] = d.id

		if !validDock(d.defDock) {
			t.Errorf("%q default dock %q is not an edge", d.id, d.defDock)
		}
		if d.title == "" {
			t.Errorf("%q has no title", d.id)
		}
		if d.isOpen == nil || d.setOpen == nil || d.show == nil || d.hide == nil {
			t.Errorf("%q is missing one of the four hooks", d.id)
		}
		if d.minW < 1 || d.minH < 1 {
			t.Errorf("%q has a zero floor (w=%d h=%d)", d.id, d.minW, d.minH)
		}
	}
}

// TestDefaultLayout_IsTreeLeftEditorRest pins the literal promise a new
// project makes: the file tree on the left, everything else closed, and
// the editor taking the rest of the window.
func TestDefaultLayout_IsTreeLeftEditorRest(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if got := a.toolDock(toolProject); got != dockLeft {
		t.Errorf("project docks %q, want left", got)
	}
	for _, d := range toolDefs {
		if d.id == toolProject {
			continue
		}
		if d.isOpen(a) {
			t.Errorf("%q is open in a fresh workspace", d.id)
		}
	}
	if !a.sidebarShown {
		t.Fatal("the file tree should be showing")
	}
	if got := a.rightBlockW(); got != 0 {
		t.Errorf("rightBlockW = %d, want nothing on an unused edge", got)
	}
	if got := a.dockRows(); got != 0 {
		t.Errorf("bottom dock = %d rows, want none", got)
	}
	ex, _, ew, _ := a.editorRect()
	if ex != a.leftBlockW() || ex+ew != a.width-a.rightBlockW() {
		t.Errorf("editor band = [%d,%d), want [%d,%d)", ex, ex+ew, a.leftBlockW(), a.width-a.rightBlockW())
	}
}

// TestClaimDock_OneVisiblePerEdge is the model's central rule: showing a
// tool evicts whatever else that edge was showing, and tools on DIFFERENT
// edges never touch each other.
func TestClaimDock_OneVisiblePerEdge(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true

	a.showTool(toolGit)
	a.showTool(toolProblems) // same edge (bottom)
	if a.gitPanel.open {
		t.Error("the git panel should have yielded the bottom edge")
	}
	if !a.problems.open {
		t.Error("problems should be showing on the bottom edge")
	}
	if !a.sidebarShown {
		t.Error("a bottom-edge claim must not touch the left edge")
	}
	if id, ok := a.visibleTool(dockBottom); !ok || id != toolProblems {
		t.Errorf("visibleTool(bottom) = %q (%v), want problems", id, ok)
	}
}

// TestMoveTool_CarriesVisibilityAndClaims covers the move: a showing
// tool stays showing on its new edge and claims it, a hidden one moves
// silently, and the edge it left keeps its other tools untouched.
func TestMoveTool_CarriesVisibilityAndClaims(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit)

	a.moveTool(toolGit, dockRight)
	if !a.gitPanel.open {
		t.Fatal("a showing tool should stay showing after a move")
	}
	if got := a.toolDock(toolGit); got != dockRight {
		t.Fatalf("git docks %q, want right", got)
	}
	if id, ok := a.visibleTool(dockBottom); ok {
		t.Errorf("the bottom edge still shows %q", id)
	}
	gx, _, gw, _ := a.gitPanelRect()
	if gx+gw != a.width {
		t.Errorf("git rect ends at %d, want the right edge %d", gx+gw, a.width)
	}

	// A move onto an occupied edge claims it.
	a.moveTool(toolGit, dockLeft)
	if a.sidebarShown {
		t.Error("moving git onto the left edge should have evicted the tree")
	}

	// Moving a HIDDEN tool changes nothing on screen.
	a.hideTool(toolGit)
	a.showTool(toolProject)
	a.moveTool(toolGit, dockBottom)
	if a.gitPanel.open {
		t.Error("moving a hidden tool must not open it")
	}
	if !a.sidebarShown {
		t.Error("moving a hidden tool must not disturb another edge")
	}
}

// TestSetToolDock_StoresOnlyDepartures pins the sparse layout: a tool
// sitting on its registry default stores nothing, which is what lets a
// project nobody has rearranged serialise to almost nothing and what
// lets a changed default reach the users who never touched that tool.
func TestSetToolDock_StoresOnlyDepartures(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.setToolDock(toolGit, dockBottom) // its default
	if _, stored := a.tools().dock[toolGit]; stored {
		t.Error("a tool on its default edge should store no entry")
	}
	a.setToolDock(toolGit, dockLeft)
	if got := a.tools().dock[toolGit]; got != dockLeft {
		t.Errorf("stored dock = %q, want left", got)
	}
	a.setToolDock(toolGit, dockBottom)
	if _, stored := a.tools().dock[toolGit]; stored {
		t.Error("returning to the default should clear the entry")
	}
}

// TestToolDock_TolerantOfNonsense pins the per-item degradation rule: a
// hand-edited layout naming an edge this version has never heard of
// resolves to the tool's default rather than leaving it nowhere.
func TestToolDock_TolerantOfNonsense(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tools().dock[toolGit] = dockSide("northeast")
	if got := a.toolDock(toolGit); got != dockBottom {
		t.Errorf("bogus edge resolved to %q, want the default bottom", got)
	}
	if got := a.toolDock(toolID("nosuchtool")); !validDock(got) {
		t.Errorf("unknown tool resolved to %q, want a real edge", got)
	}
}

// TestClampToolWidth_FloorWinsOnANarrowWindow pins the Find-all dock's
// precedence rule as this layer states it: the editor's reserve caps the
// dock, but the tool's own floor is applied LAST and wins when the
// window cannot satisfy both — a panel too narrow to read is worse than
// a cramped editor, and a zero-width one reads as a broken feature.
func TestClampToolWidth_FloorWinsOnANarrowWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if got := a.clampToolWidth(toolProject, 1); got != minSidebarWidth {
		t.Errorf("tiny target = %d, want the floor %d", got, minSidebarWidth)
	}
	wide := a.clampToolWidth(toolProject, 10000)
	if want := a.width - toolMinEditorCols; wide != want {
		t.Errorf("huge target = %d, want the editor's reserve %d", wide, want)
	}

	a.width = minSidebarWidth + 4 // narrower than floor + editor reserve
	if got := a.clampToolWidth(toolProject, 10000); got != minSidebarWidth {
		t.Errorf("on a %d-column window the floor should win, got %d", a.width, got)
	}
}

// TestClampToolHeight_LeavesTheEditorItsRows is the row-axis twin, and
// it is the guard against a bottom dock squeezing the editor out of the
// window entirely.
func TestClampToolHeight_LeavesTheEditorItsRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit)
	a.setToolHeight(toolGit, 10000)

	if _, _, _, eh := a.editorRect(); eh < toolMinEditorRows {
		t.Errorf("editor kept %d rows under a maximal git panel, want >= %d", eh, toolMinEditorRows)
	}
	if got := a.toolHeight(toolGit); got < gitPanelMinHeight {
		t.Errorf("panel height %d fell below its floor %d", got, gitPanelMinHeight)
	}
}

// TestOppositeDocksDoNotRecurse is a regression pin, not a behaviour
// one. Each vertical edge's clamp asks what the OTHER spends, so a clamp
// that asked through the other's clamp would recurse forever. Measuring
// the opposite edge RAW is the fix; this test is what fails if somebody
// "tidies" rawDockCols away.
func TestOppositeDocksDoNotRecurse(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.moveTool(toolTerminal, dockLeft)
	a.showTool(toolTerminal)
	a.moveTool(toolProject, dockRight)
	a.showTool(toolProject)

	// Both edges answer, and together they leave the editor something.
	l, r := a.leftBlockW(), a.rightBlockW()
	if l <= 0 || r <= 0 {
		t.Fatalf("both edges should be occupied (left=%d right=%d)", l, r)
	}
	if _, _, ew, _ := a.editorRect(); ew <= 0 {
		t.Errorf("editor width = %d with both edges docked", ew)
	}
}

// TestDragDockTo_TracksTheSeamOnBothEdges pins the resize arithmetic in
// both directions. The right edge is the one worth a test of its own:
// its seam is the block's LEFTMOST column, so the same pointer motion
// has to widen the panel the other way.
func TestDragDockTo_TracksTheSeamOnBothEdges(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.dragDockTo(dockLeft, 40)
	if got := a.toolWidth(toolProject); got != 41 {
		t.Errorf("left drag to column 40 gave width %d, want 41", got)
	}
	if got := a.toolSplitterX(dockLeft); got != 40 {
		t.Errorf("seam landed at %d, want the column that was dragged to (40)", got)
	}

	a.moveTool(toolProject, dockRight)
	a.dragDockTo(dockRight, a.width-30)
	if got := a.toolWidth(toolProject); got != 30 {
		t.Errorf("right drag gave width %d, want 30", got)
	}
	if got := a.toolSplitterX(dockRight); got != a.width-30 {
		t.Errorf("seam landed at %d, want %d", got, a.width-30)
	}
}

// TestResetToolLayout_ReturnsToTheDefault covers the ≡ row: everything
// back on its registry edge at its default size, everything but the tree
// closed.
func TestResetToolLayout_ReturnsToTheDefault(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.moveTool(toolGit, dockRight)
	a.showTool(toolGit)
	a.setToolWidth(toolGit, 50)
	a.hideTool(toolProject)

	a.resetToolLayout()

	if !a.sidebarShown {
		t.Error("reset should show the file tree")
	}
	if a.gitPanel.open {
		t.Error("reset should close the git panel")
	}
	if got := a.toolDock(toolGit); got != dockBottom {
		t.Errorf("git docks %q after reset, want its default bottom", got)
	}
	if got := a.sidebarWidth; got != defaultSidebarWidth {
		t.Errorf("sidebarWidth = %d after reset, want %d", got, defaultSidebarWidth)
	}
}

// TestStoredToolWidth_TreeReadsThroughToSidebarWidth pins the one
// special case in the layer: the file tree's width is App.sidebarWidth,
// because auto-fit re-derives that field every frame and a second copy
// here would be a number that could disagree with the screen.
func TestStoredToolWidth_TreeReadsThroughToSidebarWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.sidebarWidth = 37
	if got := a.storedToolWidth(toolProject); got != 37 {
		t.Errorf("storedToolWidth = %d, want sidebarWidth 37", got)
	}
	a.setToolWidth(toolProject, 44)
	if a.sidebarWidth != 44 {
		t.Errorf("setToolWidth wrote %d to sidebarWidth, want 44", a.sidebarWidth)
	}
	if _, shadowed := a.tools().size[toolProject]; shadowed {
		t.Error("the tree's width must not also be stored in the size map")
	}
}

// TestToolRect_ZeroWhenHidden pins the property every xxxContains helper
// now leans on: a tool that is not showing has no rectangle, so a hit
// test cannot land inside a panel that is not there.
func TestToolRect_ZeroWhenHidden(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, d := range toolDefs {
		if d.isOpen(a) {
			continue
		}
		if _, _, w, h := a.toolRect(d.id); w != 0 || h != 0 {
			t.Errorf("%q is hidden but has a %dx%d rect", d.id, w, h)
		}
	}
}

// TestBottomDockWinsTheCorners pins the arrangement the whole layout
// turns on: the bottom edge spans the WHOLE window and the side docks
// stop above it. A git panel therefore gets the full width for its file
// list and diff, and the file tree keeps its columns above — rather than
// the widest surface in the editor being the one that gets narrowed.
func TestBottomDockWinsTheCorners(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit) // bottom

	gx, gy, gw, gh := a.gitPanelRect()
	if gx != 0 || gw != a.width {
		t.Errorf("bottom dock = x %d w %d, want the full width of %d", gx, gw, a.width)
	}
	if gy+gh != a.height-1 {
		t.Errorf("bottom dock ends at %d, want flush against the status bar at %d", gy+gh, a.height-1)
	}

	_, sy, _, sh := a.toolRect(toolProject)
	if sy != 0 || sy+sh != gy {
		t.Errorf("tree dock rows = [%d,%d), want [0,%d) — stopping at the bottom dock", sy, sy+sh, gy)
	}

	// And with nothing on the bottom edge, the side dock takes the rows
	// back down to the status bar.
	a.hideTool(toolGit)
	if _, _, _, sh := a.toolRect(toolProject); sh != a.height-1 {
		t.Errorf("tree dock rows = %d with no bottom dock, want %d", sh, a.height-1)
	}
}

// TestFindBarHugsTheEditor pins the one strip that does NOT span the
// window: the find bar is about the file in front of you, so it stops
// where the side docks stop and sits ABOVE the bottom dock rather than
// under it — a bar pinned below a git panel would be a long way from the
// line it is searching.
func TestFindBarHugsTheEditor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit)
	a.findOpen = true

	fx, fy, fw, fh := a.findBarRect()
	if fx != a.leftBlockW() || fw != a.editorBandCols() {
		t.Errorf("find bar = x %d w %d, want the editor band [%d,%d)",
			fx, fw, a.leftBlockW(), a.leftBlockW()+a.editorBandCols())
	}
	_, gy, _, _ := a.gitPanelRect()
	if fy+fh != gy {
		t.Errorf("find bar ends at %d, want it flush above the bottom dock at %d", fy+fh, gy)
	}
}

// TestGrowShrinkDock_FollowsTheAxis pins that the resize leaders keep
// meaning "more of this tool" after it has been moved: on a vertical
// edge they spend columns, on the bottom one rows.
func TestGrowShrinkDock_FollowsTheAxis(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.showTool(toolGit)

	h0 := a.toolHeight(toolGit)
	a.growDock(dockBottom, 2)
	if got := a.toolHeight(toolGit); got != h0+2 {
		t.Errorf("bottom grow: height %d → %d, want %d", h0, got, h0+2)
	}
	a.shrinkDock(dockBottom, 2)
	if got := a.toolHeight(toolGit); got != h0 {
		t.Errorf("bottom shrink did not return to %d (got %d)", h0, got)
	}

	a.moveTool(toolGit, dockLeft)
	w0 := a.toolWidth(toolGit)
	a.growDock(dockLeft, 3)
	if got := a.toolWidth(toolGit); got != w0+3 {
		t.Errorf("left grow: width %d → %d, want %d", w0, got, w0+3)
	}
}

// TestResizeTargetDock_AimsAtTheFocusedTool pins what Esc-= resizes once
// tools can live anywhere: the edge holding the tool that owns the
// keyboard, falling back to the bottom — which is what the leaders did
// before, when the bottom was the only place a resizable panel could be.
func TestResizeTargetDock_AimsAtTheFocusedTool(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if got := a.resizeTargetDock(); got != dockBottom {
		t.Errorf("with nothing focused, target = %q, want bottom", got)
	}
	a.moveTool(toolTerminal, dockLeft)
	a.showTool(toolTerminal)
	a.term.focused = true
	if got := a.resizeTargetDock(); got != dockLeft {
		t.Errorf("with a left terminal focused, target = %q, want left", got)
	}
	a.term.focused = false
	a.treeFocus = true
	a.showTool(toolProject) // evicts the terminal, tree back on the left
	if got := a.resizeTargetDock(); got != dockLeft {
		t.Errorf("with the tree focused, target = %q, want left", got)
	}
}
