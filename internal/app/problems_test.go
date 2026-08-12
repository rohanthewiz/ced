// =============================================================================
// File: internal/app/problems_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the Problems panel: the row build (order, severity folding,
// path rendering, message flattening), the two filters and what they do
// to the counts, the geometry clamps and the single-occupancy strip, the
// jump, the next/previous seek, and the status-bar segment that opens
// the whole thing.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// diagAt builds a diagnostic at one position — the fixtures below only
// ever care about where it starts, what it says and how bad it is.
func diagAt(line, char, sev int, msg, src string) lsp.Diagnostic {
	return lsp.Diagnostic{
		Range:    lsp.Range{Start: lsp.Position{Line: line, Character: char}, End: lsp.Position{Line: line, Character: char + 1}},
		Severity: sev,
		Message:  msg,
		Source:   src,
	}
}

// newProblemsTestApp seeds an app with a second Go file and diagnostics
// spread across both, so scope and ordering have something to bite on.
// Returns the app plus the two paths in the order the panel should sort
// them (a.go before main.go).
func newProblemsTestApp(t *testing.T) (a *App, aPath, mainPath string) {
	t.Helper()
	app, _, goPath := newLSPTestApp(t)
	app.width, app.height = 120, 40
	other := filepath.Join(app.rootDir, "a.go")
	if err := os.WriteFile(other, []byte("package main\n\nvar x int\n\nvar y int\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	app.lsp.diags = map[string][]lsp.Diagnostic{
		goPath: {
			diagAt(2, 4, lsp.SeverityWarning, "unused variable", "staticcheck"),
			diagAt(0, 0, lsp.SeverityError, "undefined: doThing", "compiler"),
		},
		other: {
			diagAt(4, 0, lsp.SeverityHint, "consider renaming", ""),
			diagAt(2, 0, 0, "syntax error", "compiler"), // omitted severity == error
		},
	}
	return app, other, goPath
}

// TestBuildProblemRows pins the flattening: rows come out sorted by
// (path, line, character) regardless of the map's iteration order,
// severities fold into the three painted buckets (omitted == error,
// hint == info), and the label is the project-relative path plus the
// ONE-based line the user reads in the gutter.
func TestBuildProblemRows(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	rows := a.buildProblemRows()
	if len(rows) != 4 {
		t.Fatalf("built %d rows, want 4", len(rows))
	}
	wantOrder := []struct {
		path string
		line int
		sev  int
	}{
		{aPath, 2, lsp.SeverityError}, // omitted severity is an error
		{aPath, 4, lsp.SeverityInfo},  // hint folds into info
		{mainPath, 0, lsp.SeverityError},
		{mainPath, 2, lsp.SeverityWarning},
	}
	for i, want := range wantOrder {
		if rows[i].path != want.path || rows[i].start.Line != want.line || rows[i].sev != want.sev {
			t.Errorf("row %d = %s:%d sev%d, want %s:%d sev%d",
				i, rows[i].path, rows[i].start.Line, rows[i].sev,
				want.path, want.line, want.sev)
		}
	}
	if rows[0].label != "a.go:3" {
		t.Errorf("label = %q, want %q (relative path, 1-based line)", rows[0].label, "a.go:3")
	}
	// The server's object rides along verbatim — a quick fix is matched
	// by fields this client never modelled.
	if rows[0].diag.Message != "syntax error" {
		t.Errorf("row lost its diagnostic: %+v", rows[0].diag)
	}
}

// TestProblemRelPathOutsideRoot pins the outside-the-project case: a
// ../../.. chain would be longer than the truth and read worse, so the
// absolute path survives.
func TestProblemRelPathOutsideRoot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	outside := filepath.Join(filepath.Dir(a.rootDir), "elsewhere", "far.go")
	if got := a.problemRelPath(outside); got != outside {
		t.Errorf("relPath(outside) = %q, want the absolute path", got)
	}
	inside := filepath.Join(a.rootDir, "pkg", "in.go")
	if got := a.problemRelPath(inside); got != filepath.Join("pkg", "in.go") {
		t.Errorf("relPath(inside) = %q, want pkg/in.go", got)
	}
}

// TestFlattenProblemMsg pins the one-line rule: gopls wraps type errors
// across several padded lines, and a row that showed only the first
// fragment would hide the part that explains it.
func TestFlattenProblemMsg(t *testing.T) {
	got := flattenProblemMsg("cannot use x (variable of type int)\n\tas string value\n\tin argument")
	want := "cannot use x (variable of type int) as string value in argument"
	if got != want {
		t.Errorf("flatten = %q, want %q", got, want)
	}
	if got := flattenProblemMsg("  simple  "); got != "simple" {
		t.Errorf("flatten trimmed = %q", got)
	}
}

// TestProblemsSeverityFilter pins the chip: hiding a severity drops its
// rows from the view but never from the counts — the count is the whole
// argument for unhiding it.
func TestProblemsSeverityFilter(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.refreshProblems()
	if len(a.problems.view) != 4 {
		t.Fatalf("unfiltered view = %d rows, want 4", len(a.problems.view))
	}
	a.toggleProblemsChip(lsp.SeverityError)
	if len(a.problems.view) != 2 {
		t.Errorf("hiding errors left %d rows, want 2", len(a.problems.view))
	}
	errs, warns, infos := a.problemsCounts()
	if errs != 2 || warns != 1 || infos != 1 {
		t.Errorf("counts after hiding = %d/%d/%d, want 2/1/1 (hiding must not change counts)",
			errs, warns, infos)
	}
	a.toggleProblemsChip(lsp.SeverityError)
	if len(a.problems.view) != 4 {
		t.Errorf("un-hiding left %d rows, want 4", len(a.problems.view))
	}
}

// TestProblemsScopeFilter pins the scope chip: narrowing to the active
// file drops the other file's rows AND its counts, because a chip
// counting rows the panel can't show would be counting nothing the user
// can act on.
func TestProblemsScopeFilter(t *testing.T) {
	a, _, mainPath := newProblemsTestApp(t)
	a.openFile(mainPath)
	a.refreshProblems()

	a.toggleProblemsChip(problemsChipScope)
	if len(a.problems.view) != 2 {
		t.Fatalf("scoped view = %d rows, want 2", len(a.problems.view))
	}
	for _, ri := range a.problems.view {
		if a.problems.rows[ri].path != mainPath {
			t.Errorf("scoped view kept a row from %s", a.problems.rows[ri].path)
		}
	}
	errs, warns, infos := a.problemsCounts()
	if errs != 1 || warns != 1 || infos != 0 {
		t.Errorf("scoped counts = %d/%d/%d, want 1/1/0", errs, warns, infos)
	}
}

// TestProblemsSelectionSurvivesFilter pins the identity rule: toggling a
// filter must not move the highlight off the problem the user was
// standing on, even though every view index shifted underneath it.
func TestProblemsSelectionSurvivesFilter(t *testing.T) {
	a, _, mainPath := newProblemsTestApp(t)
	a.refreshProblems()
	// Park on main.go's error — view index 2 in document order.
	a.problems.selected = 2
	before := *a.problemRow(a.problems.selected)
	if before.path != mainPath {
		t.Fatalf("fixture drifted: selected row is %s", before.path)
	}
	a.toggleProblemsChip(lsp.SeverityInfo) // hides a row ABOVE the selection
	after := a.problemRow(a.problems.selected)
	if after == nil || problemKey(*after) != problemKey(before) {
		t.Errorf("selection moved off %q after filtering", before.msg)
	}
}

// TestProblemsSelectionSurvivesRefresh pins the same rule across a
// publish: a diagnostic appearing ABOVE the selection must not slide the
// highlight onto a different problem.
func TestProblemsSelectionSurvivesRefresh(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	a.problems.open = true
	a.refreshProblems()
	a.problems.selected = 2
	before := *a.problemRow(a.problems.selected)

	a.lsp.diags[aPath] = append(a.lsp.diags[aPath],
		diagAt(0, 0, lsp.SeverityError, "another problem, sorted first", "compiler"))
	a.handleLSPDiags(&lspDiagsEvent{path: aPath, diags: a.lsp.diags[aPath]})

	after := a.problemRow(a.problems.selected)
	if after == nil || problemKey(*after) != problemKey(before) {
		t.Fatalf("selection moved after a publish above it")
	}
	if after.path != mainPath {
		t.Errorf("selected row is now %s", after.path)
	}
}

// TestProblemsHeightClamps pins the resize band: the floor, the cap, and
// the hard limit that leaves the editor its minimum working rows even
// when the user drags for more.
func TestProblemsHeightClamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 40
	a.problems.open = true

	a.resizeProblems(1)
	if got := a.problemsHeight(); got != problemsMinHeight {
		t.Errorf("tiny resize = %d, want floor %d", got, problemsMinHeight)
	}
	a.resizeProblems(1000)
	if got, max := a.problemsHeight(), a.maxProblemsHeight(); got != max {
		t.Errorf("huge resize = %d, want cap %d", got, max)
	}
	if got := a.problemsHeight(); a.height-2-got < problemsMinEditorRows {
		t.Errorf("panel of %d rows starves the editor on a %d-row window", got, a.height)
	}
	// Auto mode: a third of the window, under the ceiling.
	a.problems.height = 0
	if got := a.problemsHeight(); got != a.height/3 {
		t.Errorf("auto height = %d, want %d", got, a.height/3)
	}
}

// TestProblemsTakesEditorRows pins that the panel DISPLACES the editor
// rather than floating over it — the same contract every bottom-strip
// panel keeps, and what makes a jump's target land in visible code.
func TestProblemsTakesEditorRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 40
	before := a.editorBandRows()
	a.problems.open = true
	if got := a.editorBandRows(); got != before-a.problemsHeight() {
		t.Errorf("editor band = %d, want %d (panel takes %d rows)",
			got, before-a.problemsHeight(), a.problemsHeight())
	}
	_, py, _, ph := a.problemsRect()
	if py+ph != a.height-1 {
		t.Errorf("panel bottom = %d, want %d (above the status bar)", py+ph, a.height-1)
	}
}

// TestProblemsSingleOccupancy pins the bottom strip's one-panel rule in
// both directions: opening problems evicts the git panels, and opening a
// git panel evicts problems.
func TestProblemsSingleOccupancy(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 40
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitPanel.open = true

	a.menuToggleProblems()
	if !a.problems.open {
		t.Fatal("problems did not open")
	}
	if a.gitLog.open || a.gitPanel.open {
		t.Error("problems opened without evicting the git panels")
	}
	a.menuToggleGitLog()
	if a.problems.open {
		t.Error("the git log opened without evicting problems")
	}
}

// TestProblemsChipsClearTheButtons pins the header layout: chips lay out
// left to right and stop short of the ⟳ / ✕ buttons, because a control
// you can't click is worse than a filter you have to reach by menu.
func TestProblemsChipsClearTheButtons(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.problems.open = true
	a.refreshProblems()

	refresh := a.problemsRefreshRect()
	chips := a.problemsChips()
	if len(chips) != 4 {
		t.Fatalf("wide panel shows %d chips, want 4", len(chips))
	}
	prev := 0
	for i, c := range chips {
		if c.rect.x < prev {
			t.Errorf("chip %d overlaps its neighbour", i)
		}
		if c.rect.x+c.rect.w > refresh.x {
			t.Errorf("chip %d runs into the ⟳ button", i)
		}
		prev = c.rect.x + c.rect.w
	}
	// A narrow panel drops chips from the right rather than drawing over
	// the buttons.
	a.width = 34
	for _, c := range a.problemsChips() {
		if c.rect.x+c.rect.w > a.problemsRefreshRect().x {
			t.Errorf("narrow panel drew a chip over the buttons")
		}
	}
}

// TestProblemsChipClickToggles drives the real gesture: a press on a
// chip's stamped rect flips that filter and starts no drag.
func TestProblemsChipClickToggles(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.problems.open = true
	a.refreshProblems()

	chips := a.problemsChips()
	warn := chips[1] // ✗, ⚠, ℹ, scope
	if drag := a.problemsPress(warn.rect.x+1, warn.rect.y); drag != "" {
		t.Errorf("chip press started drag %q, want none", drag)
	}
	if !a.problems.hidden[lsp.SeverityWarning] {
		t.Error("clicking the ⚠ chip did not hide warnings")
	}
	if len(a.problems.view) != 3 {
		t.Errorf("view = %d rows, want 3", len(a.problems.view))
	}
	// The header rule outside every button IS the resize handle.
	px, py, pw, _ := a.problemsRect()
	if drag := a.problemsPress(px+pw-20, py); drag != "problems" {
		t.Errorf("bare header press = %q, want the resize drag", drag)
	}
}

// TestProblemsRowClickJumps is the panel's whole point: clicking a row
// opens its file and parks the caret on the problem, with the panel
// still open behind it (a worklist, not a picker).
func TestProblemsRowClickJumps(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	a.openFile(mainPath)
	a.menuToggleProblems()

	// Row 0 is a.go:3 — a different file from the one on screen.
	px, py, _, _ := a.problemsRect()
	if idx := a.problemsRowIndexAt(px+2, py); idx != -1 {
		t.Errorf("header row mapped to result %d, want -1", idx)
	}
	if drag := a.problemsPress(px+4, py+1); drag != "" {
		t.Errorf("row press started drag %q", drag)
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != aPath {
		t.Fatalf("click did not open %s (active: %v)", aPath, tab)
	}
	if tab.Cursor.Line != 2 {
		t.Errorf("caret on line %d, want 2", tab.Cursor.Line)
	}
	if !a.problems.open {
		t.Error("the panel closed on a jump — it is a worklist, it stays")
	}
	if a.problems.selected != 0 {
		t.Errorf("selected = %d, want 0", a.problems.selected)
	}
}

// TestProblemsJumpClampsPastEOF pins the stale-diagnostic case: the
// server lags the buffer, so a row can point past the end of the file it
// describes. Landing near beats not landing, and neither may panic.
func TestProblemsJumpClampsPastEOF(t *testing.T) {
	a, _, mainPath := newProblemsTestApp(t)
	a.lsp.diags = map[string][]lsp.Diagnostic{
		mainPath: {diagAt(999, 40, lsp.SeverityError, "long gone", "compiler")},
	}
	a.menuToggleProblems()
	a.problemsJump(0)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != mainPath {
		t.Fatalf("jump did not open the file")
	}
	if tab.Cursor.Line >= tab.Buffer.LineCount() {
		t.Errorf("caret at line %d, past EOF (%d lines)", tab.Cursor.Line, tab.Buffer.LineCount())
	}
}

// TestStepProblemFromCaret pins next/previous: they walk the whole
// project in list order FROM WHERE THE CARET IS, not from wherever the
// panel's highlight was left, and they say so at the ends rather than
// silently landing back where you already were.
func TestStepProblemFromCaret(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	a.openFile(aPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	a.menuNextProblem() // a.go:3 (line 2)
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 2 {
		t.Fatalf("first next landed at %s:%d", filepath.Base(a.activeTabPtr().Path), a.activeTabPtr().Cursor.Line)
	}
	a.menuNextProblem() // a.go:5 (line 4)
	if tab := a.activeTabPtr(); tab.Cursor.Line != 4 {
		t.Fatalf("second next landed on line %d, want 4", tab.Cursor.Line)
	}
	a.menuNextProblem() // crosses into main.go
	if tab := a.activeTabPtr(); tab.Path != mainPath || tab.Cursor.Line != 0 {
		t.Fatalf("third next landed at %s:%d, want %s:0",
			tab.Path, tab.Cursor.Line, mainPath)
	}
	a.menuPrevProblem() // back across the file boundary
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 4 {
		t.Fatalf("prev landed at %s:%d, want %s:4", tab.Path, tab.Cursor.Line, aPath)
	}

	// Past the end: the caret stays put and the editor says why.
	a.openFile(mainPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 3, Col: 0}, false)
	a.menuNextProblem()
	if got := a.activeTabPtr().Cursor.Line; got != 3 {
		t.Errorf("next past the last problem moved to line %d", got)
	}
	if !strings.Contains(a.statusMsg, "No further problems") {
		t.Errorf("flash = %q, want the end-of-list message", a.statusMsg)
	}
}

// TestStepProblemHonorsFilters pins that the keyboard walks the SAME
// list the panel shows: a user who hid warnings said so, and next must
// not jump to one.
func TestStepProblemHonorsFilters(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	a.refreshProblems()
	a.toggleProblemsChip(lsp.SeverityError)
	a.toggleProblemsChip(lsp.SeverityWarning)

	a.openFile(aPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	a.menuNextProblem()
	// The only surviving row is a.go:5, the hint/info one.
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 4 {
		t.Errorf("filtered next landed at %s:%d, want %s:4", tab.Path, tab.Cursor.Line, aPath)
	}
	a.menuNextProblem()
	if tab := a.activeTabPtr(); tab.Path == mainPath {
		t.Error("next walked into a filtered-out row")
	}
}

// TestProblemsContextMenu pins the right-click: it selects the row under
// the pointer WITHOUT jumping (the menu is a question, not an answer)
// and opens the anchored menu with Quick fix leading.
func TestProblemsContextMenu(t *testing.T) {
	a, _, mainPath := newProblemsTestApp(t)
	a.openFile(mainPath)
	a.menuToggleProblems()
	before := a.activeTabPtr().Cursor

	px, py, _, _ := a.problemsRect()
	if !a.tryProblemsContextClick(px+6, py+1) {
		t.Fatal("right-click on a row was not consumed")
	}
	if got := a.activeTabPtr().Cursor; got != before {
		t.Errorf("right-click moved the caret to %v — it must only select", got)
	}
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("modal = %T, want the anchored context menu", a.modal)
	}
	if len(m.items) == 0 || m.items[0].label != "Quick fix…" {
		t.Errorf("first row = %q, want Quick fix…", m.items[0].label)
	}
	// Quick fix needs a live server AND a selected row; the fixture has
	// both (newLSPTestApp installs a fake client).
	if !a.hasProblemsQuickFix() {
		t.Error("quick fix is dimmed with a server up and a row selected")
	}
	a.closeModal()
	// A right-click on the header is swallowed, not escalated to the ≡
	// menu — the panel owns its own rectangle.
	if !a.tryProblemsContextClick(px+6, py) {
		t.Error("header right-click escaped the panel")
	}
	if a.menuOpen {
		t.Error("header right-click opened the main menu")
	}
}

// TestProblemsEmptyText pins the four empty states apart: "no problems"
// is a lie for three of them.
func TestProblemsEmptyText(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.refreshProblems()
	a.toggleProblemsChip(lsp.SeverityError)
	a.toggleProblemsChip(lsp.SeverityWarning)
	a.toggleProblemsChip(lsp.SeverityInfo)
	if got := a.problemsEmptyText(); !strings.Contains(got, "hidden by the filters") {
		t.Errorf("all-filtered text = %q", got)
	}

	a.lsp.diags = nil
	a.refreshProblems()
	if got := a.problemsEmptyText(); got != "No problems" {
		t.Errorf("clean text = %q, want No problems", got)
	}
	a.lsp.client = nil
	a.lsp.dead = true
	if got := a.problemsEmptyText(); !strings.Contains(got, "No language server") {
		t.Errorf("dead-server text = %q", got)
	}
}

// TestDiagStatusSegmentOpensPanel closes Phase 1's loop: the status bar
// stamped `✗ 2 ⚠ 1` as a click target and left it inert waiting for this
// panel. Clicking the drawn segment must now open it, landing on the
// active file's first problem — the counts and the highlight agreeing.
func TestDiagStatusSegmentOpensPanel(t *testing.T) {
	a, aPath, mainPath := newProblemsTestApp(t)
	a.openFile(mainPath)
	// A live flash owns the whole left side of the bar; the segments only
	// exist once it has expired.
	a.statusUntil, a.statusMsg = time.Time{}, ""
	a.drawStatusBar()

	var seg *statusSegment
	for i := range a.statusSegs {
		if strings.Contains(a.statusSegs[i].text, "✗") {
			seg = &a.statusSegs[i]
			break
		}
	}
	if seg == nil {
		t.Fatal("no diagnostic segment in the status bar")
	}
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if !a.problems.open {
		t.Fatal("clicking the diagnostic counts did not open the panel")
	}
	r := a.problemRow(a.problems.selected)
	if r == nil || r.path != mainPath {
		t.Errorf("landed on %v, want the active file's first problem", r)
	}
	if r != nil && r.path == aPath {
		t.Error("landed in the wrong file")
	}
	// Clicking again closes it — the toggle contract every other panel
	// door keeps.
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if a.problems.open {
		t.Error("second click did not close the panel")
	}
}

// TestDrawProblems is the render smoke test: the header carries its
// chips and buttons, and a row shows its severity glyph, its label and
// its message. Draws through the simulation screen the same way every
// other panel's test does.
func TestDrawProblems(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.menuToggleProblems()
	a.drawProblems()

	px, py, pw, _ := a.problemsRect()
	header := screenRow(t, a, py, px, pw)
	for _, want := range []string{"✗ 2", "⚠ 1", "ℹ 1", "all files", "Problems · 4", "⟳", "✕"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q missing %q", header, want)
		}
	}
	row := screenRow(t, a, py+1, px, pw)
	for _, want := range []string{"✗", "a.go:3", "│", "syntax error", "compiler"} {
		if !strings.Contains(row, want) {
			t.Errorf("first row %q missing %q", row, want)
		}
	}
	// An empty list explains itself instead of showing a blank strip.
	a.lsp.diags = nil
	a.refreshProblems()
	a.drawProblems()
	if got := screenRow(t, a, py+1, px, pw); !strings.Contains(got, "No problems") {
		t.Errorf("empty panel row = %q", got)
	}
}

// screenRow reads w cells of row y off the simulation screen as a
// string — the shared idiom for asserting on what was actually painted.
func screenRow(t *testing.T, a *App, y, x, w int) string {
	t.Helper()
	var b strings.Builder
	for cx := x; cx < x+w; cx++ {
		mainc, _, _, _ := a.screen.GetContent(cx, y)
		b.WriteRune(mainc)
	}
	return b.String()
}

// TestProblemsRowIndexAt pins the row hit-test against the scroll
// offset, and that nothing outside the body maps to a row.
func TestProblemsRowIndexAt(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.menuToggleProblems()
	px, py, _, ph := a.problemsRect()

	if got := a.problemsRowIndexAt(px+2, py+1); got != 0 {
		t.Errorf("first body row = %d, want 0", got)
	}
	if got := a.problemsRowIndexAt(px+2, py); got != -1 {
		t.Errorf("header row = %d, want -1", got)
	}
	if got := a.problemsRowIndexAt(px+2, py+ph); got != -1 {
		t.Errorf("below the panel = %d, want -1", got)
	}
	// Past the last row: empty cells are not rows.
	if got := a.problemsRowIndexAt(px+2, py+ph-1); got != -1 {
		t.Errorf("row past the list = %d, want -1", got)
	}
	a.problems.scroll = 2
	if got := a.problemsRowIndexAt(px+2, py+1); got != 2 {
		t.Errorf("scrolled first row = %d, want 2", got)
	}
}

// TestProblemsScrollClamps pins the viewer contract: hard clamp, no
// overscroll, in both directions.
func TestProblemsScrollClamps(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.menuToggleProblems()
	a.problemsScroll(100)
	max := len(a.problems.view) - a.problemsVisibleRows()
	if max < 0 {
		max = 0
	}
	if a.problems.scroll != max {
		t.Errorf("scroll = %d, want %d", a.problems.scroll, max)
	}
	a.problemsScroll(-100)
	if a.problems.scroll != 0 {
		t.Errorf("scroll = %d, want 0", a.problems.scroll)
	}
}

// TestProblemsWheelRoutes pins that the wheel over the panel scrolls the
// LIST and not the buffer behind it.
func TestProblemsWheelRoutes(t *testing.T) {
	a, _, mainPath := newProblemsTestApp(t)
	a.openFile(mainPath)
	a.menuToggleProblems()
	// A list long enough to have somewhere to scroll to.
	diags := make([]lsp.Diagnostic, 0, 60)
	for i := 0; i < 60; i++ {
		diags = append(diags, diagAt(i, 0, lsp.SeverityError, "problem", "compiler"))
	}
	a.lsp.diags = map[string][]lsp.Diagnostic{mainPath: diags}
	a.refreshProblems()

	px, py, _, _ := a.problemsRect()
	beforeTab := a.activeTabPtr().ScrollY
	a.scrollAt(px+4, py+2, wheelLines)
	if a.problems.scroll == 0 {
		t.Error("the wheel over the panel did not scroll the list")
	}
	if a.activeTabPtr().ScrollY != beforeTab {
		t.Error("the wheel over the panel scrolled the editor behind it")
	}
}

// TestProblemsLabelWidth pins the left column: it fits the widest label
// it has, but never eats more than two fifths of a wide panel.
func TestProblemsLabelWidth(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.problems.open = true
	a.refreshProblems()
	widest := 0
	for _, ri := range a.problems.view {
		widest = max(widest, runeLen(a.problems.rows[ri].label))
	}
	if got := a.problemsLabelWidth(); got != widest {
		t.Errorf("label width = %d, want %d (short labels fit whole)", got, widest)
	}
	// A very long label is capped by the panel's share.
	a.problems.rows[0].label = strings.Repeat("deep/nested/", 20) + "file.go:1"
	_, _, pw, _ := a.problemsRect()
	if got := a.problemsLabelWidth(); got != pw*2/5 {
		t.Errorf("capped label width = %d, want %d", got, pw*2/5)
	}
}

// TestProblemsGlyphsAreSingleWidth guards the marker rule: every glyph
// the panel stamps into a one-cell slot must actually be one cell, or
// every rect to its right drifts.
func TestProblemsGlyphsAreSingleWidth(t *testing.T) {
	for _, sev := range []int{lsp.SeverityError, lsp.SeverityWarning, lsp.SeverityInfo, 0} {
		if got := runeLen(string(problemGlyph(sev))); got != 1 {
			t.Errorf("glyph for severity %d is %d cells wide", sev, got)
		}
	}
}

// TestProblemsMouseDragResizes drives the header-rule drag end to end
// through the real router, so the drag mode and the resize agree.
func TestProblemsMouseDragResizes(t *testing.T) {
	a, _, _ := newProblemsTestApp(t)
	a.menuToggleProblems()
	px, py, pw, _ := a.problemsRect()
	before := a.problemsHeight()
	handleX := px + pw - 20

	a.handleMouse(tcell.NewEventMouse(handleX, py, tcell.Button1, 0))
	if a.dragMode != "problems" {
		t.Fatalf("dragMode = %q, want problems", a.dragMode)
	}
	a.handleMouse(tcell.NewEventMouse(handleX, py-3, tcell.Button1, 0))
	if got := a.problemsHeight(); got != before+3 {
		t.Errorf("height after dragging up 3 = %d, want %d", got, before+3)
	}
	a.handleMouse(tcell.NewEventMouse(handleX, py-3, tcell.ButtonNone, 0))
	if a.dragMode != "" {
		t.Errorf("dragMode = %q after release", a.dragMode)
	}
}
