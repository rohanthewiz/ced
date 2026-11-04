// =============================================================================
// File: internal/app/diffselect_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// diffSelPaint is a roomy paint geometry for the pure pane tests: ten
// rows from (10, 5), forty cells wide — wide enough that each split
// half carries its line-number gutter.
var diffSelPaint = diffPaint{x: 10, y: 5, w: 40, n: 10}

// TestDiffSel_UnifiedDragCopiesDisplayText: a unified drag from inside
// one row to inside a later one copies the partial first row, the whole
// rows between (prefixes kept — it's a diff), and the head of the last.
func TestDiffSel_UnifiedDragCopiesDisplayText(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	if !p.diffSelStart(diffViewSample, rows, d, d.x+1, d.y+3) { // " a", after the space
		t.Fatal("press on the pane's rows did not arm a selection")
	}
	p.diffSelExtend(diffViewSample, rows, d, d.x+2, d.y+6) // "+B", whole
	if got, want := p.diffSelText(diffViewSample, rows), "a\n-b\n-c\n+B"; got != want {
		t.Fatalf("selection = %q, want %q", got, want)
	}
}

// TestDiffSel_PlainClickSelectsNothing: a press with no drag arms an
// anchor but no text — the release of an ordinary click must copy
// nothing.
func TestDiffSel_PlainClickSelectsNothing(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, rows, diffSelPaint, diffSelPaint.x+1, diffSelPaint.y+3)
	if _, _, ok := p.diffSelRange(); ok {
		t.Fatal("a plain click reported a selection")
	}
	if got := p.diffSelText(diffViewSample, rows); got != "" {
		t.Fatalf("plain click text = %q, want empty", got)
	}
}

// TestDiffSel_PressOutsideThePaneArmsNothing: only the pane's own rows
// start a selection — the gap column left of the text (the ▸ marker)
// and rows below the pane do not.
func TestDiffSel_PressOutsideThePaneArmsNothing(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	if p.diffSelStart(diffViewSample, rows, d, d.x-1, d.y) {
		t.Error("press left of the text armed a selection")
	}
	if p.diffSelStart(diffViewSample, rows, d, d.x, d.y+d.n) {
		t.Error("press below the pane armed a selection")
	}
}

// TestDiffSel_SplitStaysOnPressedSide: in split mode the side the press
// landed on holds for the whole drag even when the pointer crosses to
// the other half, the hole on that side is skipped, and a drag below
// the pane takes the last row to its end.
func TestDiffSel_SplitStaysOnPressedSide(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, true)
	d := diffSelPaint
	// Row 3 is the a|a pair; press on the RIGHT side's text.
	rx, _ := p.diffSelTextRect(rows[3], true, d)
	if !p.diffSelStart(diffViewSample, rows, d, rx, d.y+3) {
		t.Fatal("press did not arm")
	}
	if !p.sel.right {
		t.Fatal("press on the right half did not pick the right side")
	}
	p.diffSelExtend(diffViewSample, rows, d, d.x, d.y+d.n+2) // left half, below the pane
	if !p.sel.right {
		t.Fatal("dragging into the left half switched sides")
	}
	// Right side of rows 3..6: a, B, (hole under c), d.
	if got, want := p.diffSelText(diffViewSample, rows), "a\nB\nd"; got != want {
		t.Fatalf("selection = %q, want %q", got, want)
	}
}

// TestDiffSel_ClippedRowEllipsisTakesTheTail: a pointer on a clipped
// row's … cell selects to the END of the line, hidden part included —
// the pane has no horizontal scroll, so this is the only way to reach it.
func TestDiffSel_ClippedRowEllipsisTakesTheTail(t *testing.T) {
	lines := []string{"+abcdefghij"}
	var p diffPane
	rows := p.layout(lines, false)
	d := diffPaint{x: 0, y: 0, w: 6, n: 3}
	p.diffSelStart(lines, rows, d, 0, 0)
	p.diffSelExtend(lines, rows, d, 5, 0) // the … cell
	if got := p.diffSelText(lines, rows); got != lines[0] {
		t.Fatalf("selection = %q, want the whole line %q", got, lines[0])
	}
}

// TestDiffSel_AbovePaneClampsToTopRowStart: dragging above the pane
// lands on the first VISIBLE row's start, not on rows scrolled away.
func TestDiffSel_AbovePaneClampsToTopRowStart(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	d.scroll = 3
	got := p.diffSelPosAt(diffViewSample, rows, d, false, d.x+5, d.y-4)
	if got != (diffSelPos{row: 3}) {
		t.Fatalf("pos above the pane = %+v, want {row:3 col:0}", got)
	}
}

// TestDiffPane_LayoutRebuildClearsSelection: new text (a new slice) and
// a mode switch both rebuild the layout, and with it the selection —
// a highlight must never outlive the text it was made over.
func TestDiffPane_LayoutRebuildClearsSelection(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, rows, diffSelPaint, diffSelPaint.x, diffSelPaint.y)
	p.layout(diffViewSample, false) // same text, same mode: cached
	if !p.sel.active {
		t.Fatal("a cache hit dropped the selection")
	}
	p.layout(diffViewSample, true)
	if p.sel.active {
		t.Fatal("a mode switch kept the selection")
	}
	p.layout(diffViewSample, false)
	p.diffSelStart(diffViewSample, p.rows, diffSelPaint, diffSelPaint.x, diffSelPaint.y)
	p.layout(append([]string(nil), diffViewSample...), false)
	if p.sel.active {
		t.Fatal("new text kept the selection")
	}
}

// TestDiffSel_SplitRectMatchesPaintedText pins diffSelTextRect against
// what drawDiffRows actually paints: on both split sides the rect's
// first cell is the line's first text rune, past the gutter.
func TestDiffSel_SplitRectMatchesPaintedText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var p diffPane
	rows := p.layout(diffViewSample, true)
	d := diffSelPaint
	d.style = func(string) tcell.Style { return tcell.StyleDefault }
	d.bg = a.theme.BG
	a.drawDiffRows(diffViewSample, &p, rows, d)
	lx, _ := p.diffSelTextRect(rows[4], false, d)
	rx, _ := p.diffSelTextRect(rows[4], true, d)
	if got := screenRow(t, a, d.y+4, lx, 1); got != "b" {
		t.Errorf("left rect starts on %q, want b", got)
	}
	if got := screenRow(t, a, d.y+4, rx, 1); got != "B" {
		t.Errorf("right rect starts on %q, want B", got)
	}
}

// TestPaintDiffSelection_BackgroundsOnlySelectedCells: the overlay
// re-backgrounds the selected cells with the theme's selection colour,
// keeps their glyphs, and leaves the rest of the row alone.
func TestPaintDiffSelection_BackgroundsOnlySelectedCells(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var p diffPane
	rows := p.layout(diffViewSample, false)
	d := diffSelPaint
	d.style = func(string) tcell.Style { return tcell.StyleDefault.Background(a.theme.BG) }
	d.bg = a.theme.BG
	p.diffSelStart(diffViewSample, rows, d, d.x, d.y+4)    // "-b" from the prefix
	p.diffSelExtend(diffViewSample, rows, d, d.x+1, d.y+4) // just the "-"
	a.drawDiffRows(diffViewSample, &p, rows, d)
	mainc, _, st, _ := a.screen.GetContent(d.x, d.y+4)
	if _, bg, _ := st.Decompose(); bg != a.theme.Selection || mainc != '-' {
		t.Errorf("selected cell = %q bg %v, want '-' on the selection colour", mainc, bg)
	}
	_, _, st, _ = a.screen.GetContent(d.x+1, d.y+4)
	if _, bg, _ := st.Decompose(); bg == a.theme.Selection {
		t.Error("the cell after the selection was highlighted too")
	}
}

// dragAcross presses at (x0, y0), moves with the button held to (x1, y1)
// and releases there — the whole drag-select gesture through the real
// mouse router.
func dragAcross(a *App, x0, y0, x1, y1 int) {
	a.handleMouse(tcell.NewEventMouse(x0, y0, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y1, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y1, tcell.ButtonNone, tcell.ModNone))
}

// TestCompareSel_DragCopiesOnRelease pins N-087 on the compare panel: a
// press on the diff text starts "comparesel", the held motion extends
// it, the release copies the display text (prefixes kept — it's a diff)
// and the highlight stays up; Esc then drops it.
func TestCompareSel_DragCopiesOnRelease(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.compareTexts("left", []string{"x", "y"}, "right", []string{"x", "z"})
	a.draw()
	d := a.comparePaint()
	// Row 0 is "--- left"; find the "-y" / "+z" rows by content.
	rows := a.compareRows()
	minus, plus := -1, -1
	for i, r := range rows {
		switch a.compare.lines[r.line()] {
		case "-y":
			minus = i
		case "+z":
			plus = i
		}
	}
	if minus < 0 || plus != minus+1 {
		t.Fatalf("fixture rows: -y at %d, +z at %d in %q", minus, plus, a.compare.lines)
	}
	a.handleMouse(tcell.NewEventMouse(d.x, d.y+minus, tcell.Button1, tcell.ModNone))
	if a.dragMode != "comparesel" {
		t.Fatalf("press on the text started drag %q, want comparesel", a.dragMode)
	}
	a.handleMouse(tcell.NewEventMouse(d.x+2, d.y+plus, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(d.x+2, d.y+plus, tcell.ButtonNone, tcell.ModNone))
	if a.clipBuf != "-y\n+z" {
		t.Fatalf("clipboard = %q, want %q", a.clipBuf, "-y\n+z")
	}
	if _, ok := a.diffSelLive(); !ok {
		t.Error("the highlight did not survive the release")
	}
	a.handleKey(keyEvent(tcell.KeyEsc, 0))
	if _, ok := a.diffSelLive(); ok {
		t.Error("Esc left the compare highlight up")
	}
}

// TestGitPanelSel_DragCopiesAndChipsStillWin pins N-087 on the git panel's
// diff: a drag over the text copies it, and a press on a hunk chip runs
// the chip and starts NO selection — the chips must keep winning.
func TestGitPanelSel_DragCopiesAndChipsStillWin(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.open = true
	a.gitPanel.files = []gitPanelFile{{Path: "/p/f.txt", Rel: "f.txt", Code: " M"}}
	a.gitPanel.diffLines = []string{
		"diff --git a/f.txt b/f.txt", "--- a/f.txt", "+++ b/f.txt",
		"@@ -1,2 +1,3 @@", " one", "+two", " three",
	}
	a.gitPanel.diffSide = sideUnstaged
	a.draw()
	d := a.gitPanelDiffPaint()
	dragAcross(a, d.x, d.y+4, d.x+4, d.y+5) // " one" → "+two" (the head column is exclusive)
	if a.clipBuf != " one\n+two" {
		t.Fatalf("clipboard = %q, want %q", a.clipBuf, " one\n+two")
	}

	chips := a.gitPanelHunkChipsAt(3)
	if len(chips) == 0 {
		t.Fatal("fixture: no hunk chips on the @@ row")
	}
	a.handleMouse(tcell.NewEventMouse(chips[0].rect.x, chips[0].rect.y, tcell.Button1, tcell.ModNone))
	if a.dragMode == "gitpanelsel" {
		t.Error("a press on a hunk chip started a drag-select")
	}
	a.handleMouse(tcell.NewEventMouse(chips[0].rect.x, chips[0].rect.y, tcell.ButtonNone, tcell.ModNone))
}

// TestDiffSel_PressElsewhereAndCmdC covers the two remaining doors: ⌘C
// with a live surface selection copies it (not the active file's path,
// cmdCopy's answer), and a press outside the pane drops the highlight so
// a later ⌘C belongs to the editor again.
func TestDiffSel_PressElsewhereAndCmdC(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	t.Setenv("TERM", "xterm-kitty") // arm the ⌘ layer (metaAccelArmed)
	a.compareTexts("left", []string{"x", "y"}, "right", []string{"x", "z"})
	a.draw()
	d := a.comparePaint()
	dragAcross(a, d.x, d.y+1, d.x+4, d.y+2)
	a.clipBuf = ""
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModMeta))
	if a.clipBuf == "" {
		t.Fatal("⌘C with a live compare selection copied nothing")
	}
	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+1, ey, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(ex+1, ey, tcell.ButtonNone, tcell.ModNone))
	if _, ok := a.diffSelLive(); ok {
		t.Error("a press outside the panel left its highlight up")
	}
}

// TestCherryPickDiffSel_DragCopiesInsideTheDialog pins N-087 in the
// cherry-pick preview, which lives in a modal and so runs the drag on its
// own: press on the text, held motion, release copies; a press on the
// list drops the highlight; ⌘C copies a live one again.
func TestCherryPickDiffSel_DragCopiesInsideTheDialog(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := diffTestModal(a, 3, numberedLines(10))
	m.toggleDiff(a)
	a.draw()
	d := m.diffPaint(a)
	m.handleMouse(a, d.x, d.y, tcell.Button1)
	if !m.selecting {
		t.Fatal("a press on the preview text did not start a selection")
	}
	m.handleMouse(a, d.x+4, d.y+1, tcell.Button1) // the head column is exclusive
	m.handleMouse(a, d.x+4, d.y+1, tcell.ButtonNone)
	if a.clipBuf != "+line 1\n+lin" {
		t.Fatalf("clipboard = %q, want %q", a.clipBuf, "+line 1\n+lin")
	}
	if a.modal != m {
		t.Fatal("the drag closed the dialog")
	}
	a.clipBuf = ""
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModMeta))
	if a.clipBuf == "" {
		t.Error("⌘C with a live preview selection copied nothing")
	}
	_, _, listTop, _, _, _, _ := m.rowY(a)
	mx, _, _, _ := m.rect(a)
	m.handleMouse(a, mx+6, listTop, tcell.Button1)
	m.handleMouse(a, mx+6, listTop, tcell.ButtonNone)
	if _, _, ok := m.diff.pane.diffSelRange(); ok {
		t.Error("a press on the list left the preview highlight up")
	}
}

