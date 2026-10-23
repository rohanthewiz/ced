// =============================================================================
// File: internal/app/diffview_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
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

// diffViewSample is the header comment's example: one hunk where two
// removed lines meet one added one, framed by context.
var diffViewSample = []string{
	"--- a/f.go", "+++ b/f.go",
	"@@ -10,4 +10,4 @@ func f()",
	" a", "-b", "-c", "+B", " d",
}

// TestDiffLayout_UnifiedIsOneFullRowPerLine: the unified layout is the
// degenerate one — a full row per line, so every display row IS a line
// index, and only the hunk's -/+ lines are changes (the --- / +++ file
// headers are not, though they start with the same characters).
func TestDiffLayout_UnifiedIsOneFullRowPerLine(t *testing.T) {
	rows := diffLayout(diffViewSample, false)
	if len(rows) != len(diffViewSample) {
		t.Fatalf("rows = %d, want %d", len(rows), len(diffViewSample))
	}
	for i, r := range rows {
		if !r.full || r.left != i || r.right != i || r.line() != i {
			t.Fatalf("row %d = %+v, want a full row of line %d", i, r, i)
		}
	}
	var changed []int
	for i, r := range rows {
		if r.change {
			changed = append(changed, i)
		}
	}
	if got := strings.Trim(strings.Join(strings.Fields(itoaAll(changed)), ","), ","); got != "4,5,6" {
		t.Fatalf("changed rows = %s, want 4,5,6", got)
	}
}

// itoaAll renders ints space-separated, for one-line expectations.
func itoaAll(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = itoa(n)
	}
	return strings.Join(parts, " ")
}

// TestDiffLayout_SplitPairsRuns: removed and added runs are zipped row
// by row with each side's own line numbers; the longer run's tail sits
// against a blank; headers stay full width.
func TestDiffLayout_SplitPairsRuns(t *testing.T) {
	rows := diffLayout(diffViewSample, true)
	want := []diffRow{
		{full: true, left: 0, right: 0},
		{full: true, left: 1, right: 1},
		{full: true, left: 2, right: 2},
		{left: 3, right: 3, oldNo: 10, newNo: 10},
		{left: 4, right: 6, oldNo: 11, newNo: 11, change: true},
		{left: 5, right: -1, oldNo: 12, change: true},
		{left: 7, right: 7, oldNo: 13, newNo: 12},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	// The jump target of the paired change row is the NEW side's line.
	if rows[4].line() != 6 || rows[5].line() != 5 {
		t.Fatalf("line() = %d, %d", rows[4].line(), rows[5].line())
	}
}

// TestDiffLayout_AddBeforeDelIsTwoRuns: "+a -b" is two edits; zipping b
// beside a would claim one replaced the other.
func TestDiffLayout_AddBeforeDelIsTwoRuns(t *testing.T) {
	rows := diffLayout([]string{"@@ -1 +1 @@", "+a", "-b"}, true)
	if len(rows) != 3 || rows[1].right != 1 || rows[1].left != -1 || rows[2].left != 2 || rows[2].right != -1 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestDiffLayout_GitShowMetadataStaysFull: `git show`'s indented
// message and its stat lines start with a space, and must never be read
// as context — hunk state starts at an @@ and ends at the next file.
func TestDiffLayout_GitShowMetadataStaysFull(t *testing.T) {
	lines := []string{
		"commit abc", "Author: x", "", "    subject line", "",
		" f.go | 2 +-", " 1 file changed", "",
		"diff --git a/f.go b/f.go", "--- a/f.go", "+++ b/f.go",
		"@@ -1 +1 @@", "-old", "+new",
		"diff --git a/g.go b/g.go", "--- a/g.go", "+++ b/g.go",
		"@@ -3 +3 @@", "-x", "+y",
	}
	rows := diffLayout(lines, true)
	for i := 0; i <= 11; i++ {
		if !rows[i].full {
			t.Fatalf("row %d (%q) was paired: %+v", i, lines[i], rows[i])
		}
	}
	if rows[12].left != 12 || rows[12].right != 13 || !rows[12].change {
		t.Fatalf("first file's change = %+v", rows[12])
	}
	if !rows[13].full || rows[13].left != 14 {
		t.Fatalf("second file header = %+v", rows[13])
	}
	if starts := diffChangeStarts(rows); len(starts) != 2 {
		t.Fatalf("change starts = %v, want one per file", starts)
	}
}

// TestDiffLayout_CombinedDiffNeverSplits: a merge's combined diff has no
// single old side, so it stays full width — but its changes still count
// for the stepper.
func TestDiffLayout_CombinedDiffNeverSplits(t *testing.T) {
	lines := []string{"@@@ -1 -1 +1 @@@", "  ctx", "+ ours", " -theirs"}
	rows := diffLayout(lines, true)
	for i, r := range rows {
		if !r.full {
			t.Fatalf("row %d paired in a combined diff: %+v", i, r)
		}
	}
	if rows[1].change || !rows[2].change || !rows[3].change {
		t.Fatalf("combined change flags = %v %v %v", rows[1].change, rows[2].change, rows[3].change)
	}
}

// TestDiffLayout_UntrackedViewIsAllNewSide: the git panel's synthesized
// view of a new file pairs every line on the right, numbered from 1.
func TestDiffLayout_UntrackedViewIsAllNewSide(t *testing.T) {
	rows := diffLayout([]string{gitPanelUntrackedHeader, "+a", "+b"}, true)
	if len(rows) != 3 || rows[1].left != -1 || rows[1].right != 1 || rows[1].newNo != 1 || rows[2].newNo != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestDiffLayout_NoNewlineNoteRidesWithItsRun: git's "\ No newline" note
// is a full row that does not split the change it belongs to in two.
func TestDiffLayout_NoNewlineNoteRidesWithItsRun(t *testing.T) {
	lines := []string{"@@ -1 +1 @@", "-a", `\ No newline at end of file`, "+a"}
	for _, split := range []bool{false, true} {
		rows := diffLayout(lines, split)
		if got := diffChangeStarts(rows); len(got) != 1 {
			t.Fatalf("split=%v: starts = %v, want one change", split, got)
		}
	}
}

// TestDiffPane_CacheAndCursor: the cache rebuilds only for new text or a
// new mode, and new text resets the change cursor.
func TestDiffPane_CacheAndCursor(t *testing.T) {
	var p diffPane
	rows := p.layout(diffViewSample, false)
	p.cur = 0
	if again := p.layout(diffViewSample, false); &again[0] != &rows[0] || p.cur != 0 {
		t.Fatal("same text and mode rebuilt the layout")
	}
	fresh := append([]string(nil), diffViewSample...)
	p.layout(fresh, false)
	if p.cur != -1 {
		t.Fatalf("new text kept cursor %d", p.cur)
	}
}

// TestDiffPane_SwitchModeKeepsTopLineAndCursor: flipping the layout keeps
// the same text at the top and the stepper on the same change.
func TestDiffPane_SwitchModeKeepsTopLineAndCursor(t *testing.T) {
	var p diffPane
	p.layout(diffViewSample, false)
	p.cur = 0 // the only change, starting at unified row 4
	scroll := p.switchMode(diffViewSample, false, true, 4)
	rows := p.layout(diffViewSample, true)
	if !rows[scroll].covers(4) {
		t.Fatalf("top row after the switch = %+v, want the one showing line 4", rows[scroll])
	}
	if p.cur != 0 || !rows[p.starts[p.cur]].covers(4) {
		t.Fatalf("cursor = %d, starts = %v", p.cur, p.starts)
	}
	back := p.switchMode(diffViewSample, true, false, scroll)
	if back != 4 {
		t.Fatalf("round trip landed on %d, want 4", back)
	}
}

// TestDiffPane_NextChange: relative to the current change while it is on
// screen, relative to the viewport once scrolled away, and -1 at the ends.
func TestDiffPane_NextChange(t *testing.T) {
	var p diffPane
	lines := []string{"@@ -1,9 +1,9 @@", " a", "-b", "+B", " c", " d", " e", "-f", "+F", " g"}
	p.layout(lines, false)
	if len(p.starts) != 2 {
		t.Fatalf("starts = %v", p.starts)
	}
	if k := p.nextChange(1, 0, 20); k != 0 {
		t.Fatalf("first next = %d", k)
	}
	p.cur = 0
	if k := p.nextChange(1, 0, 20); k != 1 {
		t.Fatalf("next from 0 = %d", k)
	}
	p.cur = 1
	if k := p.nextChange(1, 0, 20); k != -1 {
		t.Fatalf("next past the end = %d", k)
	}
	// Current change scrolled off the top: Previous is relative to the
	// view, so with the top at row 8 the last change above is #1.
	if k := p.nextChange(-1, 8, 2); k != 1 {
		t.Fatalf("prev relative to the view = %d", k)
	}
}

// TestDiffToolbar_ShedsTogglesFirst: a narrow header loses ws, then
// split, then the stepper pair together — never a half-drawn button.
func TestDiffToolbar_ShedsTogglesFirst(t *testing.T) {
	full := diffToolbar(0, 100, 0)
	if len(full) != 4 || full[3].rect.x+full[3].rect.w != 100 {
		t.Fatalf("full toolbar = %+v", full)
	}
	w := full[3].rect.x + full[3].rect.w - full[0].rect.x
	if got := diffToolbar(100-w+1, 100, 0); len(got) != 3 || got[2].kind != diffToolSplit {
		t.Fatalf("one cell short = %+v", got)
	}
	if got := diffToolbar(95, 100, 0); len(got) != 0 {
		t.Fatalf("tiny header = %+v", got)
	}
}

// seedDiffApp opens a compare of two files with two separate changes,
// so the stepper has somewhere to go.
func seedDiffApp(t *testing.T) *App {
	t.Helper()
	body := func(b, f string) string {
		return "a\n" + b + "\nc\nd\ne\nf\ng\nh\ni\n" + f + "\nk\n"
	}
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  body("B", "J"),
		"other.txt": body("b", "j"),
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	return a
}

// TestDiffStep_CompareWalksChanges: Next lands on each change in turn,
// says where it is, and stops at the end with a flash.
func TestDiffStep_CompareWalksChanges(t *testing.T) {
	a := seedDiffApp(t)
	a.diffStep(diffSurfCompare, 1)
	if a.compare.pane.cur != 0 || a.statusMsg != "Change 1 of 2" {
		t.Fatalf("cur=%d flash=%q", a.compare.pane.cur, a.statusMsg)
	}
	a.diffStep(diffSurfCompare, 1)
	if a.compare.pane.cur != 1 || a.statusMsg != "Change 2 of 2" {
		t.Fatalf("cur=%d flash=%q", a.compare.pane.cur, a.statusMsg)
	}
	a.diffStep(diffSurfCompare, 1)
	if a.compare.pane.cur != 1 || a.statusMsg != "No next change" {
		t.Fatalf("past the end: cur=%d flash=%q", a.compare.pane.cur, a.statusMsg)
	}
	a.diffStep(diffSurfCompare, -1)
	if a.compare.pane.cur != 0 {
		t.Fatalf("prev: cur=%d", a.compare.pane.cur)
	}
}

// TestDiffToolbar_CompareDrawAndClickAgree is the btnRect contract for
// the compare header: the buttons are painted where the click router
// finds them, and a click on split flips (and persists) the layout.
func TestDiffToolbar_CompareDrawAndClickAgree(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := seedDiffApp(t)
	a.draw()
	btns := a.compareToolbar()
	if len(btns) != 4 {
		t.Fatalf("toolbar = %+v", btns)
	}
	for _, b := range btns {
		if got := screenRow(t, a, b.rect.y, b.rect.x, b.rect.w); got != diffToolLabel(b.kind) {
			t.Fatalf("drawn %q at %+v, want %q", got, b.rect, diffToolLabel(b.kind))
		}
	}
	split := btns[2]
	a.handleMouse(tcell.NewEventMouse(split.rect.x+1, split.rect.y, tcell.Button1, 0))
	a.handleMouse(tcell.NewEventMouse(split.rect.x+1, split.rect.y, tcell.ButtonNone, 0))
	if !a.diffSplit {
		t.Fatal("split click did not switch the layout")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil || !strings.Contains(string(data), `"diffview": "split"`) {
		t.Fatalf("config not persisted: %s %v", data, err)
	}
}

// TestDrawDiffRows_SplitPaintsBothSides: side by side, the removed text
// is on the left half, the added text on the right, with line numbers.
func TestDrawDiffRows_SplitPaintsBothSides(t *testing.T) {
	a := seedDiffApp(t)
	a.diffSplit = true
	a.draw()
	px, py, pw, ph := a.comparePanelRect()
	var found bool
	for y := py + 1; y < py+ph; y++ {
		row := screenRow(t, a, y, px, pw)
		if strings.Contains(row, "2 b") && strings.Contains(row, "│") && strings.Contains(row, "2 B") {
			if strings.Index(row, "2 b") > strings.Index(row, "2 B") {
				t.Fatalf("sides reversed: %q", row)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no row shows b beside B")
	}
}

// TestSetDiffIgnoreWS_RediffsCompare: the whitespace switch re-asks the
// same comparison — a re-indent stops being a difference, and switching
// back brings it back — without re-reading anything.
func TestSetDiffIgnoreWS_RediffsCompare(t *testing.T) {
	a := seedCompareApp(t, map[string]string{
		"mine.txt":  "func f() {\n    return 1\n}\n",
		"other.txt": "func f() {\n\treturn 1\n}\n",
	}, "mine.txt")
	a.compareWithFile(filepath.Join(a.rootDir, "other.txt"))
	if a.compare.identical {
		t.Fatal("a re-indent should differ with whitespace shown")
	}
	a.setDiffIgnoreWS(true)
	if !a.compare.identical || a.compareEmptyMessage() != "No differences (ignoring whitespace)" {
		t.Fatalf("ignoring whitespace: identical=%v msg=%q", a.compare.identical, a.compareEmptyMessage())
	}
	a.setDiffIgnoreWS(false)
	if a.compare.identical {
		t.Fatal("showing whitespace again did not bring the difference back")
	}
}

// TestMenuDiffStep_ExplainsWithNoPanel: the ≡ steppers flash how to get
// a diff on screen rather than dimming.
func TestMenuDiffStep_ExplainsWithNoPanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuDiffNextChange()
	if !strings.Contains(a.statusMsg, "No diff on screen") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestActiveDiffSurface_PrefersTheOneLastTouched: with two diff panes
// open, the ≡ rows step the one the user last pressed or scrolled.
func TestActiveDiffSurface_PrefersTheOneLastTouched(t *testing.T) {
	a := seedDiffApp(t)
	a.gitLog.open = true
	a.gitLog.detailLines = []string{"@@ -1 +1 @@", "-x", "+y"}
	if got := a.activeDiffSurface(); got != diffSurfCompare {
		t.Fatalf("default = %v, want compare (first open with a diff)", got)
	}
	a.noteDiffSurface(diffSurfLog)
	if got := a.activeDiffSurface(); got != diffSurfLog {
		t.Fatalf("after touching the log = %v", got)
	}
	a.gitLog.open = false
	if got := a.activeDiffSurface(); got != diffSurfCompare {
		t.Fatalf("closed log still active: %v", got)
	}
}

// TestDiffSplitLabels: the ≡ rows read as the action they perform.
func TestDiffSplitLabels(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.diffSplitToggleLabel() != "Diff view: side by side" || a.diffIgnoreWSToggleLabel() != "Diff view: ignore whitespace" {
		t.Fatalf("labels = %q / %q", a.diffSplitToggleLabel(), a.diffIgnoreWSToggleLabel())
	}
	a.diffSplit, a.diffIgnoreWS = true, true
	if a.diffSplitToggleLabel() != "Diff view: unified" || a.diffIgnoreWSToggleLabel() != "Diff view: show whitespace changes" {
		t.Fatalf("labels = %q / %q", a.diffSplitToggleLabel(), a.diffIgnoreWSToggleLabel())
	}
}
