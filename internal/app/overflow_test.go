// =============================================================================
// File: internal/app/overflow_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-27
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// overflowApp builds an App holding a tab of `lines` numbered rows, so a
// fixture can say exactly how much is off-screen in each direction.
func overflowApp(t *testing.T, lines int) (*App, string) {
	t.Helper()
	root := t.TempDir()
	body := make([]string, lines)
	for i := range body {
		body[i] = "line"
	}
	target := filepath.Join(root, "long.txt")
	if err := os.WriteFile(target, []byte(strings.Join(body, "\n")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(target)
	if a.activeTabPtr() == nil {
		t.Fatalf("fixture failed to open %s", target)
	}
	return a, target
}

// markerAt finds the enumerated marker on a cell, or fails.
func markerAt(t *testing.T, a *App, x, y int) overflowMarker {
	t.Helper()
	m, ok := a.overflowMarkerAt(x, y)
	if !ok {
		t.Fatalf("no overflow marker at (%d, %d)", x, y)
	}
	return m
}

// TestOverflowMarkers_EditorEnds pins the marker's whole contract on the
// editor: down only at the top of a long file, both when the middle is
// showing, up only at the end — and nothing at all when the file fits.
// A marker that outstayed its content would be a permanent glyph in the
// corner meaning nothing, which is worse than no marker at all.
func TestOverflowMarkers_EditorEnds(t *testing.T) {
	a, _ := overflowApp(t, 400)
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	col, topRow, botRow := ex+ew-1, ey, ey+eh-1

	// Top of the file: nothing above, the rest below.
	if _, ok := a.overflowMarkerAt(col, topRow); ok {
		t.Error("unscrolled file drew an up-marker")
	}
	down := markerAt(t, a, col, botRow)
	if !down.down || down.off.lines != 400-eh {
		t.Errorf("down marker = %+v, want down with %d lines", down, 400-eh)
	}

	// Middle: both ends have something to say.
	tab.ScrollY = 100
	up := markerAt(t, a, col, topRow)
	if up.down || up.off.lines != 100 {
		t.Errorf("up marker = %+v, want up with 100 lines", up)
	}
	if got := markerAt(t, a, col, botRow); got.off.lines != 400-100-eh {
		t.Errorf("down marker lines = %d, want %d", got.off.lines, 400-100-eh)
	}

	// Scrolled to the very end. clampScroll's overscroll pad parks the
	// last line mid-screen, so the count must floor at zero rather than
	// going negative and drawing a marker for lines that do not exist.
	tab.ScrollY = tab.MaxScroll(eh)
	if _, ok := a.overflowMarkerAt(col, botRow); ok {
		t.Error("marker survived a scroll past the last line")
	}
	if got := markerAt(t, a, col, topRow); got.off.lines != tab.ScrollY {
		t.Errorf("up marker lines = %d, want %d", got.off.lines, tab.ScrollY)
	}

	// A file that fits gets no markers in either direction.
	short, _ := overflowApp(t, 5)
	for _, y := range []int{topRow, botRow} {
		if _, ok := short.overflowMarkerAt(col, y); ok {
			t.Errorf("a file that fits drew a marker at row %d", y)
		}
	}
}

// TestOverflowMarkers_ShareTheLastColumn is the geometry contract that
// separates this from the scrollbar it replaced: the marker reserves
// nothing, so the editor's width is the whole band and the glyph lands
// on the last column Tab.Render paints code into. A reserved column
// would move the editor's right edge as content grew past the fold.
func TestOverflowMarkers_ShareTheLastColumn(t *testing.T) {
	a, _ := overflowApp(t, 400)
	_, _, ew, _ := a.editorRect()
	if want := a.editorBandCols() - a.findAllPanelWidth(); ew != want {
		t.Fatalf("editor width = %d, want the full band %d — the marker must cost no layout", ew, want)
	}
	// And the count is unchanged by the file having grown past the fold,
	// which is the failure mode a conditional column has.
	short, _ := overflowApp(t, 3)
	if _, _, sw, _ := short.editorRect(); sw != ew {
		t.Fatalf("editor width with a short file = %d, with a long one = %d; must not differ", sw, ew)
	}
}

// TestDrawOverflowMarkers_PaintsTheGlyph checks the glyph actually
// reaches the screen on the right cell, in the right direction, and that
// it keeps the background of the row it annotates — the marker is an
// annotation on somebody else's row, so only the foreground is its own.
func TestDrawOverflowMarkers_PaintsTheGlyph(t *testing.T) {
	a, _ := overflowApp(t, 400)
	a.activeTabPtr().ScrollY = 100
	ex, ey, ew, eh := a.editorRect()
	col := ex + ew - 1

	a.draw()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	for _, c := range []struct {
		y    int
		want rune
	}{{ey, overflowUpRune}, {ey + eh - 1, overflowDownRune}} {
		cell := cells[c.y*w+col]
		if len(cell.Runes) == 0 || cell.Runes[0] != c.want {
			t.Errorf("cell (%d, %d) = %q, want %q", col, c.y, cell.Runes, c.want)
		}
		// The background must be the one the editor painted on that row.
		// A marker that set its own would punch a hole in the current-line
		// highlight, the tree's selection bar or the git panel's fill.
		_, markBG, _ := cell.Style.Decompose()
		_, rowBG, _ := cells[c.y*w+col-1].Style.Decompose()
		if markBG != rowBG {
			t.Errorf("marker background at row %d = %v, want the row's %v", c.y, markBG, rowBG)
		}
	}
}

// TestEditorOffscreen_SplitsBySide pins the counting: everything inside
// the viewport is skipped (the gutter and the tint already say it, in
// place), and everything outside lands on the side it is actually on.
func TestEditorOffscreen_SplitsBySide(t *testing.T) {
	a, path := overflowApp(t, 400)
	tab := a.activeTabPtr()
	tab.ScrollY = 100
	_, _, _, eh := a.editorRect()
	last := 100 + eh - 1

	a.lsp.diags = map[string][]lsp.Diagnostic{path: {
		{Range: lsp.Range{Start: lsp.Position{Line: 10}}, Severity: lsp.SeverityError},
		{Range: lsp.Range{Start: lsp.Position{Line: 12}}, Severity: lsp.SeverityWarning},
		{Range: lsp.Range{Start: lsp.Position{Line: 105}}, Severity: lsp.SeverityError}, // on screen
		{Range: lsp.Range{Start: lsp.Position{Line: 300}}, Severity: lsp.SeverityHint},
		{Range: lsp.Range{Start: lsp.Position{Line: 9000}}, Severity: lsp.SeverityError}, // past EOF
	}}
	tab.FindMatches = []editor.Match{{Line: last + 5}, {Line: last + 6}}
	tab.Cursor.Line = 350

	above, below := a.editorOffscreen(tab, 100, last)

	if above.lines != 100 || above.errors != 1 || above.warns != 1 {
		t.Errorf("above = %+v, want 100 lines with 1 error and 1 warning", above)
	}
	if above.caret {
		t.Error("caret counted above; it is at line 350")
	}
	if below.infos != 1 || below.hits != 2 || !below.caret {
		t.Errorf("below = %+v, want 1 note, 2 hits and the caret", below)
	}
	if below.errors != 0 {
		t.Errorf("below.errors = %d: an on-screen diagnostic and one past EOF must both be skipped", below.errors)
	}
}

// TestOffscreenKind_Precedence pins the ranking one cell has to resolve.
// The caret wins outright because it is the only UNIQUE mark — a
// diagnostic is redundantly reported by the gutter, the status bar and
// the Problems panel, while a cursor that goes unreported is a feature
// that has silently failed.
func TestOffscreenKind_Precedence(t *testing.T) {
	cases := []struct {
		name string
		o    offscreen
		want offscreenKind
	}{
		{"empty", offscreen{lines: 9}, offNone},
		{"info", offscreen{lines: 9, infos: 1}, offInfo},
		{"warn over info", offscreen{lines: 9, infos: 3, warns: 1}, offWarn},
		{"error over warn", offscreen{lines: 9, warns: 3, errors: 1}, offError},
		{"find over error", offscreen{lines: 9, errors: 3, hits: 1}, offFind},
		{"caret over all", offscreen{lines: 9, errors: 3, hits: 3, caret: true}, offCaret},
	}
	for _, c := range cases {
		if got := c.o.kind(); got != c.want {
			t.Errorf("%s: kind = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestOverflowTipLines names the popup's text, which is the whole reason
// the popup exists: the marker says "there is more", the number is what
// a thumb's height used to say and what a hover can now be asked for.
func TestOverflowTipLines(t *testing.T) {
	got := overflowTipLines(overflowMarker{down: true, unit: "line",
		off: offscreen{lines: 1842, errors: 2, hits: 5}})
	if len(got) != 2 {
		t.Fatalf("lines = %v, want two rows", got)
	}
	if got[0] != "1842 lines below" {
		t.Errorf("count row = %q", got[0])
	}
	if got[1] != "5 hits · 2 errors" {
		t.Errorf("detail row = %q, want the loudest first", got[1])
	}

	// One row when there is nothing but text out there, and the noun
	// follows the surface: a tree counts rows, not lines.
	got = overflowTipLines(overflowMarker{unit: "row", off: offscreen{lines: 1}})
	if len(got) != 1 || got[0] != "1 row above" {
		t.Errorf("bare marker lines = %v, want [\"1 row above\"]", got)
	}
}

// TestOverflowTip_DwellLifecycle pins the popup's arming: a pointer over
// ordinary code schedules nothing, a pointer on a marker opens after its
// tick, a press dismisses, and a stale tick (the pointer moved on) is
// dropped rather than opening a box about a cell nobody is pointing at.
func TestOverflowTip_DwellLifecycle(t *testing.T) {
	a, _ := overflowApp(t, 400)
	ex, ey, ew, eh := a.editorRect()
	col, botRow := ex+ew-1, ey+eh-1

	// Ordinary code: nothing armed, so the seq never moves.
	before := a.overflowTip.seq
	a.noteOverflowPointer(ex+2, ey+2, tcell.ButtonNone)
	if a.overflowTip.seq != before {
		t.Error("a pointer over code armed the tip; only a marker cell may")
	}

	a.noteOverflowPointer(col, botRow, tcell.ButtonNone)
	seq := a.overflowTip.seq
	if seq == before {
		t.Fatal("a pointer on the marker armed nothing")
	}
	a.handleOverflowTipTick(&overflowTipEvent{seq: seq})
	if !a.overflowTip.open || len(a.overflowTip.lines) == 0 {
		t.Fatalf("tick left the tip closed: %+v", a.overflowTip)
	}
	if !strings.HasSuffix(a.overflowTip.lines[0], "lines below") {
		t.Errorf("tip says %q", a.overflowTip.lines[0])
	}

	// Drawing stamps the box, which is what a press is hit-tested
	// against; it must not cover the marker it describes.
	a.draw()
	if b := a.overflowTip.box; b.w == 0 || b.h == 0 {
		t.Fatalf("draw stamped no box: %+v", b)
	}
	if a.overflowTipContains(col, botRow) {
		t.Error("the popup covered the marker it is about")
	}
	if !a.overflowTipContains(a.overflowTip.box.x, a.overflowTip.box.y) {
		t.Error("the stamped box does not contain its own origin")
	}

	// A press is an action, not a rest: it dismisses. Outside the drawn
	// box it must NOT be consumed, or the marker would eat a click on the
	// code it sits over.
	if a.noteOverflowPointer(ex+1, ey+1, tcell.Button1) {
		t.Error("a press away from the popup was consumed")
	}
	if a.overflowTip.open {
		t.Error("a press left the tip open")
	}

	// A tick whose seq has been superseded opens nothing.
	a.noteOverflowPointer(col, botRow, tcell.ButtonNone)
	stale := a.overflowTip.seq
	a.noteOverflowPointer(ex+3, ey+3, tcell.ButtonNone)
	a.handleOverflowTipTick(&overflowTipEvent{seq: stale})
	if a.overflowTip.open {
		t.Error("a stale tick opened the tip")
	}
}

// TestOverflowMarkers_GitDiffPane pins the diff pane. Its markers land
// in the pane's blank right margin — drawGitPanelDiffRow and the hunk
// chips both stop a column short of the panel's edge — so unlike the
// editor's they cover nothing at all.
func TestOverflowMarkers_GitDiffPane(t *testing.T) {
	a, _ := overflowApp(t, 20)
	a.gitPanel.open = true
	px, py, pw, ph := a.gitPanelRect()
	visible := ph - 1
	if visible <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", ph)
	}
	lines := make([]string, visible+30)
	for i := range lines {
		lines[i] = "+ added"
	}
	a.gitPanel.diffLines = lines
	col := px + pw - 1

	if _, ok := a.overflowMarkerAt(col, py+1); ok {
		t.Error("unscrolled diff drew an up-marker")
	}
	if got := markerAt(t, a, col, py+ph-1); got.off.lines != 30 {
		t.Errorf("down marker lines = %d, want 30", got.off.lines)
	}

	a.gitPanel.diffScroll = 30
	if _, ok := a.overflowMarkerAt(col, py+ph-1); ok {
		t.Error("marker survived a scroll to the end of the diff")
	}
	if got := markerAt(t, a, col, py+1); got.off.lines != 30 {
		t.Errorf("up marker lines = %d, want 30", got.off.lines)
	}

	// A closed panel contributes nothing, even with lines still cached.
	a.gitPanel.open = false
	for _, m := range a.overflowMarkers() {
		if m.x == col && (m.y == py+1 || m.y == py+ph-1) {
			t.Error("a closed git panel still enumerated a marker")
		}
	}
}

// TestOverflowMarkers_Tree pins the third surface, the one the shape came
// from — and that it now counts in ROWS, since a tree is a list of names
// rather than a body of text.
func TestOverflowMarkers_Tree(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 80; i++ {
		name := filepath.Join(root, "file-"+itoa(i)+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, root)
	sx, sy, sw, sh := a.sidebarRect()
	off, listH := a.tree.ListRows(sh)
	col := sx + sw - 1

	down := markerAt(t, a, col, sy+off+listH-1)
	if down.unit != "row" {
		t.Errorf("tree marker unit = %q, want \"row\"", down.unit)
	}
	if want := 80 - listH; down.off.lines != want {
		t.Errorf("tree down marker = %d rows, want %d", down.off.lines, want)
	}
	if _, ok := a.overflowMarkerAt(col, sy+off); ok {
		t.Error("unscrolled tree drew an up-marker")
	}

	a.tree.ScrollY = 10
	if got := markerAt(t, a, col, sy+off); got.off.lines != 10 {
		t.Errorf("tree up marker = %d rows, want 10", got.off.lines)
	}

	// A hidden sidebar contributes nothing.
	a.sidebarShown = false
	for _, m := range a.overflowMarkers() {
		if m.unit == "row" {
			t.Error("a hidden sidebar still enumerated its marker")
		}
	}
}

// TestOverflowMarkers_GitFileList pins the panel's other pane. It scrolls
// independently of the diff beside it, so it carries its own pair — in
// its own column, and counted in FILES, which is the unit somebody about
// to commit is asking in.
func TestOverflowMarkers_GitFileList(t *testing.T) {
	a, _ := overflowApp(t, 20)
	a.gitPanel.open = true
	px, py, pw, ph := a.gitPanelRect()
	visible := ph - 1
	if visible <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", ph)
	}
	listW := a.gitPanelListW(pw)
	col := px + listW - 1
	if col >= px+pw-1 {
		t.Fatalf("list column %d collides with the diff pane's at %d", col, px+pw-1)
	}

	files := make([]gitPanelFile, visible+9)
	for i := range files {
		files[i] = gitPanelFile{Path: "/x/f" + itoa(i) + ".go", Code: " M"}
	}
	a.gitPanel.files = files

	if _, ok := a.overflowMarkerAt(col, py+1); ok {
		t.Error("unscrolled list drew an up-marker")
	}
	down := markerAt(t, a, col, py+ph-1)
	if down.off.lines != 9 {
		t.Errorf("down marker = %d, want 9", down.off.lines)
	}
	if down.unit != "file" {
		t.Errorf("unit = %q, want \"file\"", down.unit)
	}
	if got := overflowTipLines(down)[0]; got != "9 files below" {
		t.Errorf("popup says %q", got)
	}

	// The two panes are independent: scrolling the list must not move the
	// diff's markers, and vice versa.
	a.gitPanel.listScroll = 9
	if _, ok := a.overflowMarkerAt(col, py+ph-1); ok {
		t.Error("marker survived a scroll to the end of the list")
	}
	if got := markerAt(t, a, col, py+1); got.off.lines != 9 {
		t.Errorf("up marker = %d, want 9", got.off.lines)
	}
	if _, ok := a.overflowMarkerAt(px+pw-1, py+ph-1); ok {
		t.Error("scrolling the list drew a marker for an empty diff pane")
	}

	// An empty change list has nothing to announce, "(clean)" and all.
	a.gitPanel.files = nil
	a.gitPanel.listScroll = 0
	for _, m := range a.overflowMarkers() {
		if m.unit == "file" {
			t.Error("a clean panel still enumerated a list marker")
		}
	}
}

// TestOverflowMarkers_GitLogPanes pins the fifth and sixth surfaces. The
// log panel is the changes panel's twin — two columns, independent
// scrolls — with one difference that matters: its body starts below the
// search bar when that is open, so the markers must ride gitLogBodyTop /
// gitLogBodyRows rather than the panel rect.
func TestOverflowMarkers_GitLogPanes(t *testing.T) {
	a, _ := overflowApp(t, 20)
	a.gitLog.open = true
	px, _, pw, _ := a.gitLogRect()
	top, visible := a.gitLogBodyTop(), a.gitLogBodyRows()
	if visible <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", visible)
	}
	listW := a.gitLogListW(pw)
	listCol, detailCol := px+listW-1, px+pw-1

	commits := make([]gitLogCommit, visible+7)
	for i := range commits {
		commits[i] = gitLogCommit{Short: itoa(i), Subject: "subject " + itoa(i)}
	}
	a.gitLog.commits = commits
	detail := make([]string, visible+40)
	for i := range detail {
		detail[i] = "+ line"
	}
	a.gitLog.detailLines = detail

	// Both panes report their own remainder, in their own unit.
	list := markerAt(t, a, listCol, top+visible-1)
	if list.off.lines != 7 || list.unit != "commit" {
		t.Errorf("list marker = %+v, want 7 commits", list)
	}
	if got := overflowTipLines(list)[0]; got != "7 commits below" {
		t.Errorf("list popup says %q", got)
	}
	if got := markerAt(t, a, detailCol, top+visible-1); got.off.lines != 40 || got.unit != "line" {
		t.Errorf("detail marker = %+v, want 40 lines", got)
	}
	for _, col := range []int{listCol, detailCol} {
		if _, ok := a.overflowMarkerAt(col, top); ok {
			t.Errorf("unscrolled pane at column %d drew an up-marker", col)
		}
	}

	// The search bar takes a row off the TOP of the body, so the up-marker
	// moves down with it and one more commit falls below the fold. Reading
	// the panel rect instead of gitLogBodyTop is what this catches.
	a.gitLog.listScroll = 3
	a.gitLog.filter.open = true
	newTop, newVisible := a.gitLogBodyTop(), a.gitLogBodyRows()
	if newTop != top+1 || newVisible != visible-1 {
		t.Fatalf("filter bar changed the body to top=%d rows=%d, want %d/%d", newTop, newVisible, top+1, visible-1)
	}
	if _, ok := a.overflowMarkerAt(listCol, top); ok {
		t.Error("the up-marker stayed on the row the search bar now occupies")
	}
	if got := markerAt(t, a, listCol, newTop); got.off.lines != 3 {
		t.Errorf("up marker with the bar open = %d commits, want 3", got.off.lines)
	}
	if got := markerAt(t, a, listCol, newTop+newVisible-1); got.off.lines != 7-3+1 {
		t.Errorf("down marker with the bar open = %d commits, want %d", got.off.lines, 7-3+1)
	}

	// A closed panel contributes nothing, even with commits still cached.
	a.gitLog.open = false
	for _, m := range a.overflowMarkers() {
		if m.unit == "commit" {
			t.Error("a closed git log still enumerated a marker")
		}
	}
}
