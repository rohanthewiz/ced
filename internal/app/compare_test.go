// =============================================================================
// File: internal/app/compare_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
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

// seedCompareApp writes the named files into a temp project and opens
// the first one, which becomes the "+" side of every comparison.
func seedCompareApp(t *testing.T, files map[string]string, open string) *App {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, open))
	return a
}

// TestCompareWithFile_ProducesUnifiedDiff is the core path: the active
// buffer is the new side, the picked file is the old one, and the result
// is unified-diff text the git panel's colorizer already understands.
func TestCompareWithFile_ProducesUnifiedDiff(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "one\ntwo\nthree\n",
		"other.txt": "one\nTWO\nthree\n",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))

	if !a.compare.open {
		t.Fatal("comparing did not open the panel")
	}
	joined := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(joined, "-TWO") || !strings.Contains(joined, "+two") {
		t.Fatalf("diff does not read old→new:\n%s", joined)
	}
	if a.compare.added != 1 || a.compare.remove != 1 {
		t.Fatalf("stats = +%d -%d, want +1 -1", a.compare.added, a.compare.remove)
	}
}

// TestCompare_IdenticalSaysSo distinguishes "ran and found nothing" from
// "nothing has been compared yet" — an empty box answers neither.
func TestCompare_IdenticalSaysSo(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"a.txt": "same\n",
		"b.txt": "same\n",
	}, "a.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "b.txt"))
	if !a.compare.identical || len(a.compare.lines) != 0 {
		t.Fatalf("identical files produced %d diff lines", len(a.compare.lines))
	}
	if msg := a.compareEmptyMessage(); msg != "No differences" {
		t.Fatalf("empty message = %q", msg)
	}
}

// TestCompare_UsesBufferNotDisk is the rule that keeps the answer
// honest: an open file's UNSAVED text is what gets compared, on both
// sides. Comparing the stale disk copy of the file you just edited is
// the one failure mode that would be quietly wrong.
func TestCompare_UsesBufferNotDisk(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "one\n",
		"other.txt": "one\n",
	}, "other.txt")
	// Edit other.txt in its tab, then switch to mine.txt and compare.
	a.activeTabPtr().InsertString("EDITED ")
	a.openFile(filepath.Join(a.rootDir, "mine.txt"))
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	joined := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(joined, "EDITED") {
		t.Fatalf("comparison read the disk copy, not the buffer:\n%s", joined)
	}
}

// TestCompareSaved_ReadsDiskForTheSameFile: comparing a file with itself
// only means anything against the SAVED copy, so that one side comes off
// the disk deliberately — and the label says so, since "t.txt ↔ t.txt"
// would otherwise read as a bug.
func TestCompareSaved_ReadsDiskForTheSameFile(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "one\ntwo\n"}, "t.txt")
	a.activeTabPtr().InsertString("NEW ")
	a.menuCompareSaved()
	joined := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(joined, "+NEW one") || !strings.Contains(joined, "-one") {
		t.Fatalf("buffer-vs-disk diff wrong:\n%s", joined)
	}
	if !strings.HasSuffix(a.compare.oldLabel, "(saved)") {
		t.Fatalf("old label = %q, want it marked as the saved copy", a.compare.oldLabel)
	}
	if !strings.Contains(a.compare.newLabel, "(unsaved)") {
		t.Fatalf("new label = %q, want it marked as carrying edits", a.compare.newLabel)
	}
}

// TestComparePaste_ClaimsTheNextPaste covers the whole armed-mode
// contract: the panel opens showing the instruction, the paste targets
// yield to it, the pasted text becomes the old side, and the arm clears
// so the paste after it goes back to the editor.
func TestComparePaste_ClaimsTheNextPaste(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "one\ntwo\n"}, "t.txt")
	a.menuComparePaste()
	if !a.comparePasteTarget() {
		t.Fatal("arming did not claim the paste")
	}
	if a.editorPasteTarget() != nil {
		t.Fatal("the editor is still a paste target while compare is armed")
	}
	if !strings.Contains(a.compareEmptyMessage(), "Paste now") {
		t.Fatalf("panel body = %q, want the instruction", a.compareEmptyMessage())
	}
	a.compareInsertPaste("one\nTWO\n")
	if a.compare.awaitPaste {
		t.Fatal("the arm survived the paste that answered it")
	}
	joined := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(joined, "-TWO") || !strings.Contains(joined, "+two") {
		t.Fatalf("pasted text did not become the old side:\n%s", joined)
	}
	if a.editorPasteTarget() == nil {
		t.Fatal("the editor never got the paste target back")
	}
}

// TestComparePaste_EscCancels — a mode that silently claims the next
// paste has to be escapable.
func TestComparePaste_EscCancels(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "x\n"}, "t.txt")
	a.menuComparePaste()
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.compare.awaitPaste {
		t.Fatal("Esc did not disarm the pending paste")
	}
	if a.editorPasteTarget() == nil {
		t.Fatal("the editor did not get the paste target back after Esc")
	}
}

// TestComparePanel_SingleOccupancy pins the bottom strip's rule in both
// directions: opening compare evicts the git panels and a bottom-docked
// terminal, and opening one of those evicts compare.
func TestComparePanel_SingleOccupancy(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "x\n"}, "t.txt")
	a.gitPanel.open = true
	a.gitLog.open = true
	a.term.open = true
	a.openComparePanel()
	if a.gitPanel.open || a.gitLog.open || a.term.open {
		t.Fatal("opening compare did not claim the strip")
	}
	a.gitIsRepo = true
	a.menuToggleGitPanel()
	if a.compare.open {
		t.Fatal("opening the git panel did not evict compare")
	}
}

// TestComparePanel_TakesRowsFromTheEditor is the layout contract every
// bottom panel shares: it displaces the editor rather than floating over
// it, so the code stays readable beside the diff.
func TestComparePanel_TakesRowsFromTheEditor(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "x\n"}, "t.txt")
	_, _, _, before := a.editorRect()
	a.openComparePanel()
	_, _, _, after := a.editorRect()
	if before-after != a.comparePanelHeight() {
		t.Fatalf("editor lost %d rows, want %d", before-after, a.comparePanelHeight())
	}
	// And the panel sits directly above the status bar.
	_, py, _, ph := a.comparePanelRect()
	if py+ph != a.height-1 {
		t.Fatalf("panel bottom = %d, want %d", py+ph, a.height-1)
	}
}

// TestComparePanel_HeaderButtons walks the mouse story: ✕ collapses, the
// header rule elsewhere starts a resize drag.
func TestComparePanel_HeaderButtons(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "x\n"}, "t.txt")
	a.openComparePanel()
	px, py, _, _ := a.comparePanelRect()
	if mode := a.comparePanelPress(px+1, py); mode != "comparepanel" {
		t.Fatalf("header press = %q, want a resize drag", mode)
	}
	c := a.compareCloseRect()
	a.comparePanelPress(c.x, c.y)
	if a.compare.open {
		t.Fatal("the ✕ did not collapse the panel")
	}
}

// TestComparePanel_DrawsHeaderAndDiff renders the real panel and checks
// that both labels and a diff line reached the screen.
func TestComparePanel_DrawsHeaderAndDiff(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "one\ntwo\n",
		"other.txt": "one\nTWO\n",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	a.drawComparePanel()
	if !screenHasText(t, a, "Compare · other.txt ↔ mine.txt") {
		t.Error("header does not name both sides")
	}
	if !screenHasText(t, a, "+two") {
		t.Error("diff body did not reach the screen")
	}
}

// TestCompare_RefusesBinaryAndOversize keeps the panel's read guards in
// step with the editor's own open guards — a file ced won't open has no
// lines worth diffing either.
func TestCompare_RefusesBinaryAndOversize(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "x\n"}, "t.txt")
	bin := filepath.Join(a.rootDir, "blob.bin")
	if err := os.WriteFile(bin, []byte{'a', 0, 'b'}, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.compareWithFile(bin)
	if !strings.Contains(a.statusMsg, "binary") {
		t.Fatalf("status = %q, want a binary-file refusal", a.statusMsg)
	}
	if len(a.compare.lines) != 0 {
		t.Fatal("a refused comparison still produced diff lines")
	}
}

// TestCompareJumpToRow_OpensTheNewSide pins why the buffer is the "+"
// side: a double-click on a diff row maps through diffTargetLine — the
// git panel's mapper — to a real line in the open file.
func TestCompareJumpToRow_OpensTheNewSide(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "one\ntwo\nthree\nfour\n",
		"other.txt": "one\ntwo\nTHREE\nfour\n",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	// Find the "+three" row and jump to it.
	row := -1
	for i, l := range a.compare.lines {
		if l == "+three" {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("no +three row in:\n%s", strings.Join(a.compare.lines, "\n"))
	}
	a.compareJumpToRow(row)
	if got := a.activeTabPtr().Cursor.Line; got != 2 {
		t.Fatalf("jumped to line %d, want 2 (0-based)", got)
	}
}

// TestCompareRefresh_RereadsTheFileSide pins ⟳: a diff is a snapshot,
// and both sides may have moved since it was taken. The file side is
// re-read rather than replayed from the label — a path outside the
// project root, or the "(saved)" suffix, would not survive that guess.
func TestCompareRefresh_RereadsTheFileSide(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "one\ntwo\n",
		"other.txt": "one\ntwo\n",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	if !a.compare.identical {
		t.Fatal("expected identical files to start with")
	}
	if err := os.WriteFile(filepath.Join(a.rootDir, "other.txt"),
		[]byte("one\nCHANGED ON DISK\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	a.compareRefresh()
	if !strings.Contains(strings.Join(a.compare.lines, "\n"), "CHANGED ON DISK") {
		t.Fatalf("refresh did not re-read the file:\n%s", strings.Join(a.compare.lines, "\n"))
	}
}

// TestCompareRefresh_RediffsPastedSide — a pasted side has nothing to
// re-read, so ⟳ re-diffs the held lines against the current buffer.
// That's the useful half: the buffer is what's been edited since.
func TestCompareRefresh_RediffsPastedSide(t *testing.T) {
	a := seedCompareApp(t, map[string]string{"t.txt": "one\ntwo\n"}, "t.txt")
	a.menuComparePaste()
	a.compareInsertPaste("one\ntwo\n")
	if !a.compare.identical {
		t.Fatal("expected the paste to match the buffer")
	}
	a.activeTabPtr().InsertString("EDITED ")
	a.compareRefresh()
	if !strings.Contains(strings.Join(a.compare.lines, "\n"), "EDITED") {
		t.Fatalf("refresh did not pick up the buffer edit:\n%s",
			strings.Join(a.compare.lines, "\n"))
	}
}
