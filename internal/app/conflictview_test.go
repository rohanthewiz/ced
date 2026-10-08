// =============================================================================
// File: internal/app/conflictview_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// conflictText is one two-way block between ordinary lines.
const conflictText = "package x\n" +
	"<<<<<<< HEAD\n" +
	"var a = 1\n" +
	"=======\n" +
	"var a = 2\n" +
	">>>>>>> 2a0370b (bump a)\n" +
	"var z = 0\n"

// conflictTestApp roots an App in a temp dir holding one marker file per
// name given, opens the first, and lists every one as unmerged the way the
// git snapshot would. Returns the app and the absolute paths.
func conflictTestApp(t *testing.T, names ...string) (*App, []string) {
	t.Helper()
	root := t.TempDir()
	a := newTestApp(t, root)
	a.gitIsRepo = true
	a.gitConflicted = map[string]bool{}
	var paths []string
	for _, n := range names {
		p := filepath.Join(a.rootDir, n)
		writeFileT(t, p, conflictText)
		a.gitConflicted[p] = true
		paths = append(paths, p)
	}
	a.openFile(paths[0])
	return a, paths
}

// lensCell finds a lens label on the painted screen and returns a cell
// inside it.
func lensCell(t *testing.T, a *App, label string) (int, int) {
	t.Helper()
	for y := 0; y < a.height; y++ {
		row := screenRow(t, a, y, 0, a.width)
		if i := strings.Index(row, label); i >= 0 {
			return len([]rune(row[:i])) + 1, y
		}
	}
	t.Fatalf("lens %q not on screen", label)
	return 0, 0
}

// TestConflictLensID_RoundTrip pins the tagged packing, and that an ID
// some other lens source minted is not claimed.
func TestConflictLensID_RoundTrip(t *testing.T) {
	id := conflictLensID(37, editor.ChooseBothIncomingFirst)
	b, c, ok := conflictLensDecode(id)
	if !ok || b != 37 || c != editor.ChooseBothIncomingFirst {
		t.Fatalf("decode = %d,%d,%v", b, c, ok)
	}
	if _, _, ok := conflictLensDecode(5); ok {
		t.Error("an untagged ID decoded as a conflict verb")
	}
}

// TestConflictSource_GatedOnGit pins the gate: the same markers paint
// nothing on a file git does not list as unmerged, and wash every block
// row plus put the lens on the opener once it does.
func TestConflictSource_GatedOnGit(t *testing.T) {
	a, paths := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	src := conflictSource{app: a}

	delete(a.gitConflicted, paths[0])
	if w := src.LineWashes(tab, a.theme, 0, 10); w != nil {
		t.Errorf("washed a file git does not call conflicted: %v", w)
	}
	if l := src.Lenses(tab, a.theme, 0, 10); l != nil {
		t.Errorf("lenses on a file git does not call conflicted: %v", l)
	}

	a.gitConflicted[paths[0]] = true
	washes := src.LineWashes(tab, a.theme, 0, 10)
	for line := 1; line <= 5; line++ {
		if _, ok := washes[line]; !ok {
			t.Errorf("block line %d not washed", line)
		}
	}
	if _, ok := washes[0]; ok {
		t.Error("a line outside the block was washed")
	}
	if washes[2] == washes[4] {
		t.Error("the two sides share one colour")
	}
	lenses := src.Lenses(tab, a.theme, 0, 10)
	if set := lenses[1]; len(set) != 4 || set[0].Label != "Accept current" || set[3].Label != "Compare sides" {
		t.Errorf("opener lens = %+v", lenses[1])
	}
}

// TestConflictSource_CaretLineIsDistinct pins the caret-line shade: the
// wash replaces the line highlight, so the caret's row needs its own.
func TestConflictSource_CaretLineIsDistinct(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	src := conflictSource{app: a}
	tab.Cursor = editor.Position{Line: 0}
	before := src.LineWashes(tab, a.theme, 0, 10)[2]
	tab.Cursor = editor.Position{Line: 2}
	if after := src.LineWashes(tab, a.theme, 0, 10)[2]; after == before {
		t.Error("the caret's row inside a conflict looks like every other row")
	}
}

// TestConflictLensPress_ResolvesTheBlock clicks the painted "Accept
// incoming" button end to end: the block becomes the incoming side, the
// buffer is dirty, and the flash names the next step.
func TestConflictLensPress_ResolvesTheBlock(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.draw()
	x, y := lensCell(t, a, "Accept incoming")
	if !a.conflictLensPress(x, y) {
		t.Fatal("press on the lens was not consumed")
	}
	tab := a.activeTabPtr()
	if got := tab.Buffer.String(); got != "package x\nvar a = 2\nvar z = 0\n" {
		t.Fatalf("buffer = %q", got)
	}
	if !tab.Dirty {
		t.Error("resolving did not dirty the tab")
	}
	if !strings.Contains(a.statusMsg, "no conflicts left") || !strings.Contains(a.statusMsg, "Mark resolved") {
		t.Errorf("flash = %q, want the next step named", a.statusMsg)
	}
}

// TestConflictLensPress_StaleFrameIsSwallowed pins the re-check: a button
// painted for a block that has since moved is consumed without writing.
func TestConflictLensPress_StaleFrameIsSwallowed(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.draw()
	x, y := lensCell(t, a, "Accept current")
	tab := a.activeTabPtr()
	tab.Buffer.Lines = append([]string{"// a line typed above"}, tab.Buffer.Lines...)
	tab.EditRev++
	if !a.conflictLensPress(x, y) {
		t.Fatal("a press on a painted lens was not consumed")
	}
	if len(tab.Conflicts()) != 1 {
		t.Error("a stale lens resolved a block it no longer pointed at")
	}
}

// TestConflictLensPress_IgnoresOtherCells pins that ordinary editor
// presses are left alone.
func TestConflictLensPress_IgnoresOtherCells(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.draw()
	ex, ey, _, _ := a.editorRect()
	if a.conflictLensPress(ex+8, ey) {
		t.Error("a press on code was claimed by the lens")
	}
}

// TestConflictContextItems_OnlyInsideABlock pins the right-click rows:
// present with the caret in a block (lens verbs plus the rarer ones),
// absent outside one.
func TestConflictContextItems_OnlyInsideABlock(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 0}
	if rows := a.conflictContextItems(); rows != nil {
		t.Errorf("rows outside a block: %d", len(rows))
	}
	tab.Cursor = editor.Position{Line: 4}
	rows := a.conflictContextItems()
	labels := make([]string, len(rows))
	for i, r := range rows {
		labels[i] = r.label
	}
	want := "Accept current|Accept incoming|Accept both|Accept both, incoming first|Accept neither|Compare sides"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("rows = %s", got)
	}
	rows[2].action(a)
	if got := tab.Buffer.String(); !strings.Contains(got, "var a = 1\nvar a = 2") {
		t.Errorf("Accept both produced %q", got)
	}
}

// TestMenuResolveConflictAtCaret pins the keyboard door: outside a block
// it explains itself; inside, the picker names the sides and runs.
func TestMenuResolveConflictAtCaret(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 0}
	a.menuResolveConflictAtCaret()
	if a.modal != nil || !strings.Contains(a.statusMsg, "not inside a conflict") {
		t.Fatalf("outside a block: modal %T, flash %q", a.modal, a.statusMsg)
	}
	tab.Cursor = editor.Position{Line: 2}
	a.menuResolveConflictAtCaret()
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	if !strings.Contains(m.title, "HEAD ↔ 2a0370b") {
		t.Errorf("title = %q, want both sides named", m.title)
	}
	m.items[0].run(a) // Accept current
	if got := tab.Buffer.String(); got != "package x\nvar a = 1\nvar z = 0\n" {
		t.Errorf("buffer = %q", got)
	}
}

// TestStepConflict_AcrossFiles pins the walk: past the last block of one
// unmerged file, Next opens the next one at its first block; past the
// last file it says so instead of wrapping.
func TestStepConflict_AcrossFiles(t *testing.T) {
	a, paths := conflictTestApp(t, "a.go", "b.go")
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 0}
	a.stepConflict(1)
	if tab.Cursor.Line != 1 {
		t.Fatalf("first step landed on line %d, want the opener (1)", tab.Cursor.Line)
	}
	a.stepConflict(1)
	if nt := a.activeTabPtr(); nt == nil || nt.Path != paths[1] || nt.Cursor.Line != 1 {
		t.Fatalf("second step: tab %v, want b.go at its opener", nt)
	}
	a.stepConflict(1)
	if !strings.Contains(a.statusMsg, "No further conflicts") {
		t.Errorf("flash = %q", a.statusMsg)
	}
	a.stepConflict(-1)
	if nt := a.activeTabPtr(); nt.Path != paths[0] {
		t.Errorf("previous from b.go's only block went to %s, want a.go", nt.Path)
	}
}

// TestNeighbourConflictFile_SkipsResolvedFiles pins the buffer-first
// count: an unmerged file whose open buffer has no blocks left is passed
// over rather than opened.
func TestNeighbourConflictFile_SkipsResolvedFiles(t *testing.T) {
	a, paths := conflictTestApp(t, "a.go", "b.go", "c.go")
	a.openFile(paths[1])
	a.activeTabPtr().ResolveAllConflicts(editor.ChooseCurrent)
	if got, ok := a.neighbourConflictFile(paths[0], 1); !ok || got != paths[2] {
		t.Errorf("next after a.go = %q, want c.go (b.go is resolved in its buffer)", got)
	}
}

// TestBlendColor pins the mix and the degradation for non-RGB colours.
func TestBlendColor(t *testing.T) {
	black, white := tcell.NewRGBColor(0, 0, 0), tcell.NewRGBColor(200, 100, 0)
	if got := blendColor(black, white, 0.5); got != tcell.NewRGBColor(100, 50, 0) {
		t.Errorf("half mix = %v", got)
	}
	if got := blendColor(tcell.ColorDefault, white, 0.5); got != tcell.ColorDefault {
		t.Errorf("non-RGB base came back as %v", got)
	}
}

// TestRender_ConflictRowsAreWashed is the visible end: a drawn frame shows
// the two sides in two different backgrounds, past the end of the text.
func TestRender_ConflictRowsAreWashed(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.activeTabPtr().Cursor = editor.Position{Line: 6}
	a.draw()
	ex, ey, ew, _ := a.editorRect()
	bgAt := func(line int) tcell.Color {
		_, _, st, _ := a.screen.GetContent(ex+ew-5, ey+line)
		_, bg, _ := st.Decompose()
		return bg
	}
	if bgAt(2) != a.theme.ConflictCurrent || bgAt(4) != a.theme.ConflictIncoming {
		t.Errorf("side rows: %v / %v, want the theme's two conflict washes", bgAt(2), bgAt(4))
	}
	if bgAt(0) == a.theme.ConflictCurrent {
		t.Error("a line outside the block is washed")
	}
}
