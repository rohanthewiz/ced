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

// TestOverflowMarkers_FindAllList pins the seventh surface. The list is a
// viewport like any other, with two things none of the others have: its
// marker lands in the blank cell drawRow leaves between the text and the
// row's ✕ (so it covers nothing, and — the point of the next test — it
// must not sit ON the ✕), and it counts the DISPLAYED rows, which
// dismissals and the filter narrow.
func TestOverflowMarkers_FindAllList(t *testing.T) {
	a, _ := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	mx, my, mw, _ := m.rect(a)
	vis := m.visibleRows(a)
	if vis <= 2 || vis >= len(m.view) {
		t.Skipf("fixture shows %d of %d rows — nothing is off-screen", vis, len(m.view))
	}
	col, top, bot := mx+mw-3, my+4, my+4+vis-1
	total := len(m.view)

	if _, ok := a.overflowMarkerAt(col, top); ok {
		t.Error("an unscrolled list drew an up-marker")
	}
	down := markerAt(t, a, col, bot)
	if down.off.lines != total-vis || down.unit != "result" {
		t.Errorf("down marker = %+v, want %d results", down, total-vis)
	}
	if got := overflowTipLines(down)[0]; got != itoa(total-vis)+" results below" {
		t.Errorf("popup says %q", got)
	}
	// The ✕ is the row's own control and keeps its cell — a marker there
	// would hide it on exactly the two rows the eye lands on first.
	if _, ok := a.overflowMarkerAt(mx+mw-2, bot); ok {
		t.Error("the marker took the row's ✕ cell")
	}

	// Scrolling moves what is off-screen from one end to the other.
	m.scrollList(a, total)
	if _, ok := a.overflowMarkerAt(col, bot); ok {
		t.Error("marker survived a scroll to the end of the list")
	}
	if got := markerAt(t, a, col, top); got.off.lines != total-vis {
		t.Errorf("up marker = %d, want %d", got.off.lines, total-vis)
	}

	// The total is the DISPLAYED list: a dismissed row is one fewer
	// result below, because the panel is a viewport onto view, not rows.
	m.scrollList(a, -total)
	m.dismissRow(a, 0)
	if got := markerAt(t, a, col, bot); got.off.lines != total-vis-1 {
		t.Errorf("after a dismissal the marker says %d, want %d", got.off.lines, total-vis-1)
	}

	// A closed list contributes nothing, in either home.
	m.abort(a)
	for _, mk := range a.overflowMarkers() {
		if mk.unit == "result" {
			t.Error("a closed find-all list still enumerated a marker")
		}
	}
}

// TestOverflowMarkers_FindAllLayers pins the one thing about this surface
// that is not like the other six: it lives in two homes, and only one of
// them is drawn on the body layer. Unpinned it is a modal, painted AFTER
// the body pass — so a marker stamped there would be covered by the very
// panel it describes, and its own draw has to do it instead.
func TestOverflowMarkers_FindAllLayers(t *testing.T) {
	a, _ := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	mx, my, mw, _ := m.rect(a)
	vis := m.visibleRows(a)
	if vis <= 2 || vis >= len(m.view) {
		t.Skipf("fixture shows %d of %d rows — nothing is off-screen", vis, len(m.view))
	}
	col, bot := mx+mw-3, my+4+vis-1

	if !markerAt(t, a, col, bot).overlay {
		t.Fatal("an unpinned list's marker is not flagged for the overlay layer")
	}
	// The body pass must decline it: on its own it paints nothing here.
	a.screen.Clear()
	a.drawOverflowMarkers()
	a.screen.Show()
	if got := screenRuneAt(t, a, col, bot); got == overflowDownRune {
		t.Error("the body pass painted a marker the modal is about to cover")
	}
	// The full frame does paint it, because the panel's own draw does.
	a.draw()
	a.screen.Show()
	if got := screenRuneAt(t, a, col, bot); got != overflowDownRune {
		t.Errorf("unpinned list's marker = %q, want %q", got, overflowDownRune)
	}

	// Pinned, the list draws with the panels and the body pass owns it.
	m.togglePin(a)
	if markerAt(t, a, col, bot).overlay {
		t.Fatal("a pinned panel's marker still claims the overlay layer")
	}
	a.screen.Clear()
	a.drawOverflowMarkers()
	a.screen.Show()
	if got := screenRuneAt(t, a, col, bot); got != overflowDownRune {
		t.Errorf("pinned panel's marker = %q, want the body pass to paint it", got)
	}

	// And only pinned can the popup explain itself: the modal slot is
	// free, so the passive layer is allowed to open under it.
	a.noteOverflowPointer(col, bot, tcell.ButtonNone)
	a.handleOverflowTipTick(&overflowTipEvent{seq: a.overflowTip.seq})
	if !a.overflowTip.open || !strings.HasSuffix(a.overflowTip.lines[0], "results below") {
		t.Errorf("pinned panel's marker opened no popup: %+v", a.overflowTip)
	}
}

// screenRuneAt reads one cell of the simulation screen.
func screenRuneAt(t *testing.T, a *App, x, y int) rune {
	t.Helper()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	cell := cells[y*w+x]
	if len(cell.Runes) == 0 {
		return ' '
	}
	return cell.Runes[0]
}

// pressAt drives a left press through the real router, which is what
// pins the marker's claim on it: the same event that pages the editor
// would otherwise have moved the caret.
func pressAt(a *App, x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
}

// TestOverflowClick_PagesTheEditor pins the single click: the down
// marker moves the viewport one page (the height less a row of overlap)
// and the up marker brings it back, both without touching the caret —
// this is a reading gesture, and a click that scrolled AND moved the
// cursor would lose the user's place in the file.
func TestOverflowClick_PagesTheEditor(t *testing.T) {
	a, _ := overflowApp(t, 400)
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	col, topRow, botRow := ex+ew-1, ey, ey+eh-1
	caret := tab.Cursor

	pressAt(a, col, botRow)
	if tab.ScrollY != eh-1 {
		t.Fatalf("a click on ▾ scrolled to %d, want one page (%d)", tab.ScrollY, eh-1)
	}
	if tab.Cursor != caret {
		t.Errorf("the click moved the caret to %+v", tab.Cursor)
	}

	// Back up. A fresh cell record, or the press would read as the
	// second half of a double-click on the marker below.
	a.overflowClick = overflowClickRecord{}
	pressAt(a, col, topRow)
	if tab.ScrollY != 0 {
		t.Errorf("a click on ▴ scrolled to %d, want back to the top", tab.ScrollY)
	}
}

// TestOverflowClick_PageStopsAtTheEdge pins the clamp: with less than a
// page left, the click travels exactly that far. Unclamped it would run
// into clampScroll's overscroll pad, answering an arrow that said "3
// lines below" with half a screen of blank rows.
func TestOverflowClick_PageStopsAtTheEdge(t *testing.T) {
	a, _ := overflowApp(t, 400)
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	col, botRow := ex+ew-1, ey+eh-1

	tab.ScrollY = 400 - eh - 3 // three lines left below
	pressAt(a, col, botRow)
	if tab.ScrollY != 400-eh {
		t.Errorf("scrolled to %d, want the last line on the last row (%d)", tab.ScrollY, 400-eh)
	}
	if _, ok := a.overflowMarkerAt(col, botRow); ok {
		t.Error("a marker still points down from the end of the file")
	}
}

// TestOverflowDoubleClick_RunsToTheEnd pins the second click: it lands
// the far end of the document on the far edge of the viewport, and the
// press is swallowed even when the first click already got there — the
// marker stops being drawn at that moment, and a second press falling
// through to the editor would drop the caret into the code beneath it.
func TestOverflowDoubleClick_RunsToTheEnd(t *testing.T) {
	a, _ := overflowApp(t, 400)
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	col, topRow, botRow := ex+ew-1, ey, ey+eh-1
	caret := tab.Cursor

	pressAt(a, col, botRow)
	pressAt(a, col, botRow)
	if tab.ScrollY != 400-eh {
		t.Fatalf("a double click on ▾ scrolled to %d, want the bottom (%d)", tab.ScrollY, 400-eh)
	}

	// Two pages' worth above, so the first press of this double reaches
	// the top and takes the marker with it; the second must still be the
	// gesture's own rather than a click in the file.
	tab.ScrollY = eh - 1
	a.overflowClick = overflowClickRecord{}
	pressAt(a, col, topRow)
	if tab.ScrollY != 0 {
		t.Fatalf("the first press left ScrollY at %d, want the top", tab.ScrollY)
	}
	pressAt(a, col, topRow)
	if tab.ScrollY != 0 {
		t.Errorf("the second press moved off the top to %d", tab.ScrollY)
	}
	if tab.Cursor != caret {
		t.Errorf("a press on a vanished marker fell through and moved the caret to %+v", tab.Cursor)
	}
}

// TestOverflowClick_LeavesOrdinaryCellsAlone pins the claim's edges: a
// press one column in from the marker is an ordinary click on the file,
// and a press mid-drag is the drag's, not the marker's — the hook runs
// before handleMouse's drag branches, so a splitter drag sweeping through
// the editor's last column must not page the file it passes over.
func TestOverflowClick_LeavesOrdinaryCellsAlone(t *testing.T) {
	a, _ := overflowApp(t, 400)
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	col, botRow := ex+ew-1, ey+eh-1

	if a.overflowMarkerClick(col-1, botRow, tcell.Button1) {
		t.Error("the cell beside the marker was claimed")
	}
	if a.overflowMarkerClick(col, botRow, tcell.ButtonSecondary) {
		t.Error("a right press was claimed; that gesture opens a menu")
	}
	a.dragMode = "sidebar"
	if a.overflowMarkerClick(col, botRow, tcell.Button1) {
		t.Error("a press mid-drag was claimed")
	}
	a.dragMode = ""
	if tab.ScrollY != 0 {
		t.Errorf("nothing should have scrolled; ScrollY = %d", tab.ScrollY)
	}
}

// TestOverflowClick_PagesTheTree pins that the gesture is the enumerator's
// rather than the editor's: the same press on the sidebar's marker moves
// the tree, through the same scrollAt every wheel event uses, and without
// opening the file whose row the glyph shares a cell with.
func TestOverflowClick_PagesTheTree(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 200; i++ {
		name := filepath.Join(root, "f"+itoa(i)+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, root)
	a.refreshTreeNow()
	sx, sy, sw, sh := a.sidebarRect()
	off, listH := a.tree.ListRows(sh)
	if listH <= 0 || a.tree.RowCount() <= listH {
		t.Skipf("fixture drew %d rows into %d — no marker", a.tree.RowCount(), listH)
	}
	col, botRow := sx+sw-1, sy+off+listH-1
	tabs := len(a.tabs)

	pressAt(a, col, botRow)
	if a.tree.ScrollY != listH-1 {
		t.Errorf("tree scrolled to %d, want one page (%d)", a.tree.ScrollY, listH-1)
	}
	if len(a.tabs) != tabs {
		t.Error("the press opened the file under the marker")
	}
}

// panelMarkerPair asserts the up/down pair on one panel column: nothing
// up while unscrolled, `hidden` rows down in `unit`; then, scrolled to
// the end via setScroll, the pair swaps. It is the git-diff-pane test's
// shape, shared by the four panels that gained markers together so a
// fifth copy of the arithmetic cannot drift from the others.
func panelMarkerPair(t *testing.T, a *App, name string, col, top, visible, hidden int,
	unit string, setScroll func(int)) {
	t.Helper()
	bot := top + visible - 1
	if _, ok := a.overflowMarkerAt(col, top); ok {
		t.Errorf("%s: unscrolled panel drew an up-marker", name)
	}
	down := markerAt(t, a, col, bot)
	if down.off.lines != hidden || down.unit != unit || !down.down {
		t.Errorf("%s: down marker = %d %s (down=%v), want %d %s",
			name, down.off.lines, down.unit, down.down, hidden, unit)
	}
	if down.page != visible {
		t.Errorf("%s: marker page = %d, want the viewport's %d rows", name, down.page, visible)
	}

	setScroll(hidden)
	if _, ok := a.overflowMarkerAt(col, bot); ok {
		t.Errorf("%s: down marker survived a scroll to the end", name)
	}
	if up := markerAt(t, a, col, top); up.off.lines != hidden || up.down {
		t.Errorf("%s: up marker = %d (down=%v), want %d pointing up", name, up.off.lines, up.down, hidden)
	}
}

// TestOverflowMarkers_ComparePanel pins the compare panel's pair: in the
// diff's blank right margin (the body is drawn from px+2 and truncated
// short of the edge), counted in lines, over the rows under the header.
func TestOverflowMarkers_ComparePanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.compare.open = true
	px, py, pw, ph := a.comparePanelRect()
	visible := ph - 1
	if visible <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", ph)
	}
	lines := make([]string, visible+25)
	for i := range lines {
		lines[i] = "+ added"
	}
	a.compare.lines = lines
	panelMarkerPair(t, a, "compare", px+pw-1, py+1, visible, 25, "line",
		func(n int) { a.compare.scroll = n })

	a.compare.open = false
	for _, m := range a.overflowMarkers() {
		if m.x == px+pw-1 && m.y == py+1 {
			t.Error("a closed compare panel still enumerated a marker")
		}
	}
}

// problemsFixture opens the Problems panel over `n` rows whose severity
// is chosen per index, bypassing the LSP plumbing: the marker reads the
// derived view, and building it directly keeps the counts exact.
func problemsFixture(t *testing.T, n int, sev func(i int) int) *App {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.problems.open = true
	rows := make([]problemRow, n)
	view := make([]int, n)
	for i := range rows {
		rows[i] = problemRow{path: "/x/f.go", label: "f.go:" + itoa(i+1), msg: "m", sev: sev(i)}
		view[i] = i
	}
	a.problems.rows, a.problems.view = rows, view
	return a
}

// TestOverflowMarkers_ProblemsPanel pins the Problems list's pair,
// counted in problems over the FILTERED view — a row the chips hide is
// not "below", it is not in the list at all.
func TestOverflowMarkers_ProblemsPanel(t *testing.T) {
	vis := 0
	a := problemsFixture(t, 1, func(int) int { return lsp.SeverityWarning })
	if vis = a.problemsVisibleRows(); vis <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", vis)
	}
	a = problemsFixture(t, vis+12, func(int) int { return lsp.SeverityWarning })
	px, py, pw, _ := a.problemsRect()
	panelMarkerPair(t, a, "problems", px+pw-1, py+1, vis, 12, "problem",
		func(n int) { a.problems.scroll = n })

	// Filtered down to what fits: no marker in either direction.
	a.problems.scroll = 0
	a.problems.view = a.problems.view[:vis]
	for _, y := range []int{py + 1, py + vis} {
		if _, ok := a.overflowMarkerAt(px+pw-1, y); ok {
			t.Errorf("a view that fits still drew a marker at row %d", y)
		}
	}
}

// TestOverflowMarkers_ProblemsColoredBySeverity pins the one list that
// colors its marker: the rows are sorted by path, not rank, so an error
// past the bottom edge is news — and only what is OFF screen counts, so
// an error on screen leaves the marker at the loudest hidden severity.
func TestOverflowMarkers_ProblemsColoredBySeverity(t *testing.T) {
	probe := problemsFixture(t, 1, func(int) int { return lsp.SeverityInfo })
	vis := probe.problemsVisibleRows()
	if vis <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", vis)
	}
	// Row 0 (on screen) is an error; one hidden row is a warning, the
	// rest notes.
	a := problemsFixture(t, vis+5, func(i int) int {
		switch i {
		case 0:
			return lsp.SeverityError
		case vis + 2:
			return lsp.SeverityWarning
		}
		return lsp.SeverityInfo
	})
	px, py, pw, _ := a.problemsRect()
	down := markerAt(t, a, px+pw-1, py+vis)
	if down.off.kind() != offWarn || down.off.warns != 1 || down.off.infos != 4 {
		t.Errorf("down marker = %+v, want 1 warning + 4 notes (the on-screen error not counted)", down.off)
	}
	if got := overflowTipLines(down); len(got) != 2 || got[0] != "5 problems below" {
		t.Errorf("tip = %q, want the count in problems plus the severity line", got)
	}

	// Scrolled past the error, the UP marker carries it.
	a.problems.scroll = 5
	if up := markerAt(t, a, px+pw-1, py+1); up.off.kind() != offError || up.off.errors != 1 {
		t.Errorf("up marker = %+v, want the hidden error", up.off)
	}
}

// TestOverflowMarkers_ChatTranscript pins the chat pair to the
// TRANSCRIPT band — never the composer below it, which does not scroll —
// counted in wrapped rows.
func TestOverflowMarkers_ChatTranscript(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.open = true
	px, py, pw, ph := a.chatPanelRect()
	band := ph - 1 - a.chatComposerRowsView() - a.chatAttachRows()
	if pw <= 2 || band <= 2 {
		t.Skipf("panel too small in this fixture (%dx%d)", pw, ph)
	}
	body := make([]string, band+20)
	for i := range body {
		body[i] = "x"
	}
	a.chat.msgs = []chatMsg{{role: chatRoleAgent, text: strings.Join(body, "\n")}}
	// Taken from the derivation rather than assumed: chatRows adds its
	// own action rows (the copy buttons), and the marker must agree with
	// the scroller, which counts those too.
	hidden := a.chatContentRows() - band
	if hidden < 20 {
		t.Fatalf("fixture derived %d hidden rows, want at least 20", hidden)
	}
	panelMarkerPair(t, a, "chat", px+pw-1, py+1, band, hidden, "row",
		func(n int) { a.chat.scroll = n })
	if py+band >= a.chatComposerTop() {
		t.Errorf("the band (%d rows) runs into the composer at row %d", band, a.chatComposerTop())
	}
	if _, ok := a.overflowMarkerAt(px+pw-1, a.chatComposerTop()); ok {
		t.Error("a marker landed on the composer row")
	}
}

// TestOverflowMarkers_TerminalScrollback pins the terminal pair to the
// scrollback — between the header rule and the input row — counted in
// lines, and plain-colored even over stderr (see overflowMarkers for why).
func TestOverflowMarkers_TerminalScrollback(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.term.open = true
	px, py, pw, ph := a.termPanelRect()
	visible := ph - 2
	if visible <= 2 {
		t.Skipf("panel too short in this fixture (%d rows)", ph)
	}
	for i := 0; i < visible+15; i++ {
		a.term.lines = append(a.term.lines, termLine{text: "boom", kind: termErr})
	}
	panelMarkerPair(t, a, "terminal", px+pw-1, py+1, visible, 15, "line",
		func(n int) { a.term.scroll = n })
	if up := markerAt(t, a, px+pw-1, py+1); up.off.kind() != offNone {
		t.Errorf("terminal marker kind = %v, want plain over stderr", up.off.kind())
	}
	iy, _, _ := a.termInputSpan()
	if _, ok := a.overflowMarkerAt(px+pw-1, iy); ok {
		t.Error("a marker landed on the input row")
	}

	// And it is really PAINTED: the panel draws before the marker pass,
	// so the glyph survives on the frame rather than being covered by
	// the scrollback it annotates.
	a.draw()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	if cell := cells[(py+1)*w+px+pw-1]; len(cell.Runes) == 0 || cell.Runes[0] != overflowUpRune {
		t.Errorf("terminal's top-right cell = %q, want %q", cell.Runes, overflowUpRune)
	}
}

// TestOverflowClick_PagesThePanels pins that the click came along for
// free: a press on each new panel's ▾ pages THAT panel through scrollAt
// by the viewport less a row, and is claimed — so it neither focuses the
// terminal/chat under it nor jumps to a problem.
func TestOverflowClick_PagesThePanels(t *testing.T) {
	type surface struct {
		name   string
		open   func(a *App) (col, bot, visible int)
		scroll func(a *App) int
	}
	surfaces := []surface{
		{"compare", func(a *App) (int, int, int) {
			a.compare.open = true
			px, py, pw, ph := a.comparePanelRect()
			a.compare.lines = make([]string, 3*ph)
			return px + pw - 1, py + ph - 1, ph - 1
		}, func(a *App) int { return a.compare.scroll }},
		{"problems", func(a *App) (int, int, int) {
			a.problems.open = true
			vis := a.problemsVisibleRows()
			for i := 0; i < 3*vis; i++ {
				a.problems.rows = append(a.problems.rows, problemRow{path: "/nope/f.go", sev: lsp.SeverityError})
				a.problems.view = append(a.problems.view, i)
			}
			px, py, pw, _ := a.problemsRect()
			return px + pw - 1, py + vis, vis
		}, func(a *App) int { return a.problems.scroll }},
		{"chat", func(a *App) (int, int, int) {
			a.chat.open = true
			px, py, pw, ph := a.chatPanelRect()
			band := ph - 1 - a.chatComposerRowsView() - a.chatAttachRows()
			a.chat.msgs = []chatMsg{{role: chatRoleAgent, text: strings.Repeat("x\n", 3*ph)}}
			return px + pw - 1, py + band, band
		}, func(a *App) int { return a.chat.scroll }},
		{"terminal", func(a *App) (int, int, int) {
			a.term.open = true
			px, py, pw, ph := a.termPanelRect()
			for i := 0; i < 3*ph; i++ {
				a.term.lines = append(a.term.lines, termLine{text: "out"})
			}
			return px + pw - 1, py + ph - 2, ph - 2
		}, func(a *App) int { return a.term.scroll }},
	}
	for _, s := range surfaces {
		a := newTestApp(t, t.TempDir())
		col, bot, visible := s.open(a)
		if visible <= 2 {
			t.Logf("%s: panel too short in this fixture (%d rows); skipped", s.name, visible)
			continue
		}
		tabs := len(a.tabs)
		pressAt(a, col, bot)
		if got := s.scroll(a); got != visible-1 {
			t.Errorf("%s: a click on ▾ scrolled to %d, want one page (%d)", s.name, got, visible-1)
		}
		if a.term.focused || a.chat.focused {
			t.Errorf("%s: the press fell through and focused a panel", s.name)
		}
		if len(a.tabs) != tabs {
			t.Errorf("%s: the press fell through and opened a file", s.name)
		}
	}
}
