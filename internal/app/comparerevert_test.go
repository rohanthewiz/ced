// =============================================================================
// File: internal/app/comparerevert_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// compareHeaders lists the "@@" rows of the compare panel's diff.
func compareHeaders(a *App) []int {
	var out []int
	for i, ln := range a.compare.lines {
		if strings.HasPrefix(ln, "@@") {
			out = append(out, i)
		}
	}
	return out
}

// TestCompareRevertHunk_RestoresTheSavedCopy is the core path: edit two
// separate places, compare with the saved copy, revert ONE hunk — that
// place goes back, the other edit stays, the comparison re-runs with one
// hunk left, and it is one undo step.
func TestCompareRevertHunk_RestoresTheSavedCopy(t *testing.T) {
	saved := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	a := seedCompareApp(t, map[string]string{"f.txt": saved}, "f.txt")
	tab := a.activeTabPtr()
	tab.Buffer.Lines[1] = "B"
	tab.Buffer.Lines[9] = "J"
	tab.EditRev++
	a.menuCompareSaved()
	hs := compareHeaders(a)
	if len(hs) != 2 {
		t.Fatalf("hunks = %d, want 2:\n%s", len(hs), strings.Join(a.compare.lines, "\n"))
	}
	depth := tab.UndoDepth()

	a.compareRevertHunk(hs[0])
	if got := tab.Buffer.String(); got != strings.Replace(saved, "j\n", "J\n", 1) {
		t.Fatalf("buffer after revert = %q", got)
	}
	if tab.UndoDepth() != depth+1 {
		t.Fatalf("undo depth %d → %d, want one step", depth, tab.UndoDepth())
	}
	if n := len(compareHeaders(a)); n != 1 {
		t.Fatalf("comparison not re-run: %d hunks left", n)
	}
	if !strings.Contains(a.statusMsg, "Undo brings it back") {
		t.Fatalf("flash = %q", a.statusMsg)
	}

	a.compareRevertHunk(compareHeaders(a)[0])
	if tab.Buffer.String() != saved || !a.compare.identical {
		t.Fatalf("second revert: buffer %q identical=%v", tab.Buffer.String(), a.compare.identical)
	}
}

// TestCompareRevertEdit_RefusesAStaleBuffer: a buffer typed in since the
// diff no longer holds the hunk's "+" side, and the revert refuses with
// the way out rather than writing into the wrong place.
func TestCompareRevertEdit_RefusesAStaleBuffer(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"f.txt": "a\nb\nc\n"}, "f.txt")
	tab := a.activeTabPtr()
	tab.Buffer.Lines[1] = "B"
	a.menuCompareSaved()
	tab.Buffer.Lines[1] = "typed since"
	before := tab.Buffer.String()
	a.compareRevertHunk(compareHeaders(a)[0])
	if a.statusMsg != compareStaleReason || tab.Buffer.String() != before {
		t.Fatalf("flash = %q, buffer = %q", a.statusMsg, tab.Buffer.String())
	}
}

// TestCompareRevertEdit_NoFinalNewline: a buffer without a final newline
// ends its last hunk at the END of the last line, there being no next
// line to stop at — and the revert leaves it without one.
func TestCompareRevertEdit_NoFinalNewline(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "a\nb",
		"other.txt": "a\nB\nc",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	a.compareRevertHunk(compareHeaders(a)[0])
	if got := a.activeTabPtr().Buffer.String(); got != "a\nB\nc" {
		t.Fatalf("buffer = %q, want the other file's text", got)
	}
}

// TestCompareRevertBlocked_SnapshotsAndConflicts: only a whole open
// buffer can take a revert; a selection snapshot says why.
func TestCompareRevertBlocked_SnapshotsAndConflicts(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"f.txt": "a\nb\n"}, "f.txt")
	a.compareTexts("pasted text", []string{"x"}, "selection", []string{"y"})
	if why := a.compareRevertBlocked(); !strings.Contains(why, "isn't an open file") {
		t.Fatalf("selection compare: %q", why)
	}
	a.compare.newPath = a.activeTabPtr().Path
	a.compare.conflict = &compareConflict{}
	if why := a.compareRevertBlocked(); !strings.Contains(why, "conflict") {
		t.Fatalf("conflict compare: %q", why)
	}
	if _, ok := a.compareRevertChipAt(compareHeaders(a)[0]); ok {
		t.Fatal("a chip was offered on a comparison that can't take a revert")
	}
}

// TestCompareRevertChip_DrawAndClickAgree is the btnRect contract for
// the chip, in BOTH layouts: painted where the click router finds it, on
// the hunk header row, and a click reverts.
func TestCompareRevertChip_DrawAndClickAgree(t *testing.T) {
	for _, split := range []bool{false, true} {
		a := seedCompareApp(t, map[string]string{"f.txt": "a\nb\nc\n"}, "f.txt")
		a.diffSplit = split
		tab := a.activeTabPtr()
		tab.Buffer.Lines[1] = "B"
		a.menuCompareSaved()
		a.draw()
		row := a.compare.pane.rowOfLine(compareHeaders(a)[0])
		chip, ok := a.compareRevertChipAt(row)
		if !ok {
			t.Fatalf("split=%v: no chip on the header row", split)
		}
		if got := screenRow(t, a, chip.y, chip.x, chip.w); got != gitHunkChipLabel(hunkRevert) {
			t.Fatalf("split=%v: drawn %q", split, got)
		}
		a.handleMouse(tcell.NewEventMouse(chip.x+1, chip.y, tcell.Button1, 0))
		a.handleMouse(tcell.NewEventMouse(chip.x+1, chip.y, tcell.ButtonNone, 0))
		if tab.Buffer.Lines[1] != "b" {
			t.Fatalf("split=%v: chip click did not revert: %q", split, tab.Buffer.Lines)
		}
	}
}

// TestCompareRevert_IgnoringWhitespaceKeepsReindents: under ignore
// whitespace the hunk's context carries the buffer's spelling, so a
// revert puts back the real edit and leaves the re-indent alone.
func TestCompareRevert_IgnoringWhitespaceKeepsReindents(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"f.txt": "if x {\n\tcall()\n}\nend\n"}, "f.txt")
	a.diffIgnoreWS = true
	tab := a.activeTabPtr()
	tab.Buffer.Lines[1] = "    call()" // re-indent: invisible to the diff
	tab.Buffer.Lines[3] = "END"        // a real edit
	a.menuCompareSaved()
	a.compareRevertHunk(compareHeaders(a)[0])
	if got := tab.Buffer.String(); got != "if x {\n    call()\n}\nend\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// TestMenuCompareRevertPick_ListsEveryHunk: the ≡ row's picker has one
// row per hunk; with no revertable comparison it flashes why instead.
func TestMenuCompareRevertPick_ListsEveryHunk(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuCompareRevertPick()
	if !strings.Contains(a.statusMsg, "no differences") {
		t.Fatalf("flash with nothing compared = %q", a.statusMsg)
	}
	a = seedDiffApp(t)
	a.menuCompareRevertPick()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	if n := len(pm.items); n != 2 || !strings.HasPrefix(pm.items[0].label, "Revert lines") {
		t.Fatalf("items = %+v", pm.items)
	}
}
