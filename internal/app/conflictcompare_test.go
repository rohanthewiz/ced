// =============================================================================
// File: internal/app/conflictcompare_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
)

// conflictText3 is one diff3 block (base section included) between
// ordinary lines. Line map: 0 package, 1 <<<, 2–3 current, 4 |||,
// 5–6 base, 7 ===, 8–9 incoming, 10 >>>, 11 tail.
const conflictText3 = "package x\n" +
	"<<<<<<< HEAD\n" +
	"var a = 1\n" +
	"var b = 1\n" +
	"||||||| parent of 2a0370b\n" +
	"var a = 0\n" +
	"var b = 1\n" +
	"=======\n" +
	"var a = 0\n" +
	"var b = 2\n" +
	">>>>>>> 2a0370b (bump b)\n" +
	"var z = 0\n"

// diff3TestApp is conflictTestApp with the diff3 fixture in the open tab.
func diff3TestApp(t *testing.T) (*App, *editor.Tab) {
	t.Helper()
	a, paths := conflictTestApp(t, "a.go")
	writeFileT(t, paths[0], conflictText3)
	tab := a.activeTabPtr()
	tab.Buffer.Lines = strings.Split(strings.TrimSuffix(conflictText3, "\n"), "\n")
	tab.EditRev++
	return a, tab
}

// TestMenuCompareConflictAtCaret_TwoWayOpensTheDiff pins the two-way
// door: outside a block it explains itself; inside, it goes straight to
// current ↔ incoming (no one-row picker), labelled by side and marker.
func TestMenuCompareConflictAtCaret_TwoWayOpensTheDiff(t *testing.T) {
	a, paths := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 0}
	a.menuCompareConflictAtCaret()
	if a.compare.open || !strings.Contains(a.statusMsg, "not inside a conflict") {
		t.Fatalf("outside a block: open %v, flash %q", a.compare.open, a.statusMsg)
	}

	tab.Cursor = editor.Position{Line: 2}
	a.menuCompareConflictAtCaret()
	if a.modal != nil {
		t.Fatalf("a two-way block opened a %T; want the diff directly", a.modal)
	}
	if !a.compare.open {
		t.Fatal("compare panel did not open")
	}
	if a.compare.oldLabel != "current (HEAD)" || !strings.HasPrefix(a.compare.newLabel, "incoming (2a0370b") {
		t.Errorf("labels = %q ↔ %q", a.compare.oldLabel, a.compare.newLabel)
	}
	got := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(got, "-var a = 1") || !strings.Contains(got, "+var a = 2") {
		t.Errorf("diff = %q", got)
	}
	if a.compare.added != 1 || a.compare.remove != 1 {
		t.Errorf("stats = +%d −%d", a.compare.added, a.compare.remove)
	}
	if a.compare.newPath != paths[0] || a.compare.newLineBase != 4 {
		t.Errorf("jump target = %q @%d, want the incoming side at line 4", a.compare.newPath, a.compare.newLineBase)
	}
	if a.compare.conflict == nil || a.compare.conflict.start != 1 {
		t.Errorf("conflict source = %+v", a.compare.conflict)
	}
}

// TestCompareConflictBlock_Diff3AsksWhichPair pins the diff3 picker and
// each row's direction: base is the old side of both base pairs, and the
// jump base follows the "+" side.
func TestCompareConflictBlock_Diff3AsksWhichPair(t *testing.T) {
	a, tab := diff3TestApp(t)
	a.compareConflictBlock(tab, 0)
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	if len(m.items) != 3 || !strings.Contains(m.title, "HEAD ↔ 2a0370b") {
		t.Fatalf("picker %q with %d rows", m.title, len(m.items))
	}

	cases := []struct {
		row            int
		oldPfx, newPfx string
		minus, plus    string
		base           int
	}{
		{0, "current", "incoming", "-var a = 1", "+var b = 2", 8},
		{1, "base", "current", "-var a = 0", "+var a = 1", 2},
		{2, "base", "incoming", "-var b = 1", "+var b = 2", 8},
	}
	for _, c := range cases {
		m.items[c.row].run(a)
		got := strings.Join(a.compare.lines, "\n")
		if !strings.HasPrefix(a.compare.oldLabel, c.oldPfx) || !strings.HasPrefix(a.compare.newLabel, c.newPfx) {
			t.Errorf("row %d labels = %q ↔ %q", c.row, a.compare.oldLabel, a.compare.newLabel)
		}
		if !strings.Contains(got, c.minus) || !strings.Contains(got, c.plus) {
			t.Errorf("row %d diff = %q", c.row, got)
		}
		if a.compare.newLineBase != c.base {
			t.Errorf("row %d newLineBase = %d, want %d", c.row, a.compare.newLineBase, c.base)
		}
	}
}

// TestCompareConflictRefresh_RereadsTheBlock pins ⟳: an edit inside the
// block is picked up, and a block that is gone is said so while the old
// diff stays on screen.
func TestCompareConflictRefresh_RereadsTheBlock(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	a.compareConflictBlock(tab, 0)

	tab.Buffer.Lines[4] = "var a = 1" // incoming now equals current
	tab.EditRev++
	a.compareRefresh()
	if !a.compare.identical {
		t.Fatalf("refresh did not re-read the block: %q", a.compare.lines)
	}
	if a.compare.conflict == nil {
		t.Fatal("refresh dropped the conflict source")
	}

	a.compare.lines = []string{"held"}
	a.compare.identical = false
	tab.ResolveConflict(0, editor.ChooseCurrent)
	a.compareRefresh()
	if !strings.Contains(a.statusMsg, "conflict is gone") {
		t.Errorf("flash = %q", a.statusMsg)
	}
	if len(a.compare.lines) != 1 || a.compare.lines[0] != "held" {
		t.Errorf("a failed refresh replaced the diff: %q", a.compare.lines)
	}
}

// TestCompareJumpToRow_ConflictLandsOnTheSide pins the double-click
// offset: a "+" row of a conflict diff lands on its line in the buffer,
// not that many lines from the top of the file.
func TestCompareJumpToRow_ConflictLandsOnTheSide(t *testing.T) {
	a, tab := diff3TestApp(t)
	a.showConflictCompare(tab, 1, pairBaseIncoming)
	row := -1
	for i, l := range a.compare.lines {
		if l == "+var b = 2" {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no + row in %q", a.compare.lines)
	}
	a.compareJumpToRow(row)
	if got := a.activeTabPtr().Cursor.Line; got != 9 {
		t.Errorf("jump landed on line %d, want 9", got)
	}
}

// TestConflictLensPress_CompareOpensThePanel clicks the painted
// "Compare sides" lens end to end: the panel opens, the buffer is
// untouched — the button looks, it never settles.
func TestConflictLensPress_CompareOpensThePanel(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.width = 160 // room for the whole lens set, nothing shed
	a.draw()
	x, y := lensCell(t, a, "Compare sides")
	if !a.conflictLensPress(x, y) {
		t.Fatal("press on the lens was not consumed")
	}
	tab := a.activeTabPtr()
	if !a.compare.open || a.compare.conflict == nil {
		t.Fatal("compare panel did not open on the block")
	}
	if tab.Dirty || len(tab.Conflicts()) != 1 {
		t.Error("the Compare lens edited the buffer")
	}
}

// TestConflictSideLabel pins the label form and the bare word when git
// wrote no label.
func TestConflictSideLabel(t *testing.T) {
	if got := conflictSideLabel("current", "HEAD"); got != "current (HEAD)" {
		t.Errorf("got %q", got)
	}
	if got := conflictSideLabel("base", ""); got != "base" {
		t.Errorf("got %q", got)
	}
	if got := nonEmpty("", "x"); got != "x" {
		t.Errorf("nonEmpty fallback = %q", got)
	}
}
