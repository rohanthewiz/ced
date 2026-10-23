// =============================================================================
// File: internal/app/hunkview_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the change view: splitting a hunk into its sides, the revert
// edit (round-tripped against real `git diff -U0` output, end-of-file
// newline states included), its refusals, the gutter catch-up and the
// exact-hunks stamp, the rows, the modal's keys, and its three doors.

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// hunkViewTestTab is a tab over text with no file behind it, for the
// pure edit tests that need only a buffer.
func hunkViewTestTab(text string) *editor.Tab {
	return &editor.Tab{Buffer: editor.NewBuffer(text)}
}

// TestSplitHunkSides pins the body parse: prefixes stripped, CRs dropped,
// and the "\ No newline" note credited to the side it follows.
func TestSplitHunkSides(t *testing.T) {
	s := splitHunkSides(diffHunk{Body: []string{
		"-old\r", `\ No newline at end of file`, "+new", "+more", `\ No newline at end of file`,
	}})
	if strings.Join(s.old, "|") != "old" || strings.Join(s.new, "|") != "new|more" {
		t.Fatalf("sides = %q / %q", s.old, s.new)
	}
	if !s.oldNoEOL || !s.newNoEOL {
		t.Fatalf("EOF notes = old %v new %v, want both", s.oldNoEOL, s.newNoEOL)
	}
	if s := splitHunkSides(diffHunk{Body: []string{"-a", "+b"}}); s.oldNoEOL || s.newNoEOL {
		t.Fatal("no note, yet a side was marked as lacking its newline")
	}
}

// TestRevertHunk_RoundTripsRealDiffs is the property the feature rests
// on: for each (committed, working) pair, reverting EVERY hunk git
// reports — top-down, so each revert relies on the previous one's shift
// of the hunks below — brings the buffer back to exactly the committed
// text. The cases cover each kind, the top of the file, several hunks
// at once, a removal after an earlier revert dirtied the buffer, CRLF,
// and every end-of-file newline combination.
func TestRevertHunk_RoundTripsRealDiffs(t *testing.T) {
	requireGit(t)
	cases := []struct{ name, old, new string }{
		{"modified", "a\nb\nc\n", "a\nB\nc\n"},
		{"added", "a\nc\n", "a\nb1\nb2\nc\n"},
		{"removed", "a\nb\nc\n", "a\nc\n"},
		{"removed at top", "x\ny\na\nb\n", "a\nb\n"},
		{"added at top", "a\n", "x\na\n"},
		{"several hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\nTWO\n3\n5\n6\n6b\n6c\n7\n8\n9\n"},
		{"emptied file", "a\nb\n", ""},
		{"old without final newline", "a\nb", "a\nB\n"},
		{"new without final newline", "a\nb\n", "a\nb\nc"},
		{"neither has a final newline", "a\nb", "a\nc"},
		{"removed last line without newline", "a\nb", "a\n"},
		{"crlf", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initRepo(t)
			path := filepath.Join(repo, "f.txt")
			writeFileT(t, path, tc.old)
			gitRun(t, repo, "add", "f.txt")
			gitRun(t, repo, "commit", "-q", "-m", "base")
			writeFileT(t, path, tc.new)

			a := newTestApp(t, repo)
			a.openFile(path)
			tab := a.activeTabPtr()
			a.fileDiffs = map[string][]diffHunk{path: loadFileDiff(repo, path)}
			n := len(a.fileDiffs[path])
			if n == 0 {
				t.Fatal("git reported no hunks")
			}
			for i := 0; i < n; i++ {
				hunks := a.fileDiffs[path]
				if len(hunks) != n-i {
					t.Fatalf("after %d reverts the gutter has %d hunks, want %d", i, len(hunks), n-i)
				}
				if why := a.revertHunk(tab, hunks[0]); why != "" {
					t.Fatalf("revert %d of %d refused: %s", i+1, n, why)
				}
			}
			want := strings.ReplaceAll(tc.old, "\r\n", "\n")
			if got := tab.Buffer.String(); got != want {
				t.Fatalf("buffer after reverting every hunk = %q, want %q", got, want)
			}
			if _, left := a.fileDiffs[path]; left {
				t.Fatal("the gutter still has hunks after all of them were reverted")
			}
		})
	}
}

// TestRevertHunk_IsOneUndoStep: a revert is one structural undo step, so
// a single Undo brings the change back.
func TestRevertHunk_IsOneUndoStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFileT(t, path, "a\nB1\nB2\nc\n")
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	h := diffHunk{Start: 1, End: 2, Kind: diffModified, Body: []string{"-b", "+B1", "+B2"}}
	a.fileDiffs = map[string][]diffHunk{path: {h}}

	if why := a.revertHunk(tab, h); why != "" {
		t.Fatalf("revert refused: %s", why)
	}
	if got := tab.Buffer.String(); got != "a\nb\nc\n" {
		t.Fatalf("after revert = %q", got)
	}
	if tab.Cursor.Line != 1 {
		t.Errorf("caret on line %d, want the restored line 1", tab.Cursor.Line)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "a\nB1\nB2\nc\n" {
		t.Fatalf("after one undo = %q, want the change back", got)
	}
}

// TestHunkRevertEdit_RefusesAStaleHunk: when the buffer no longer holds
// the hunk's "+" lines where git saw them (edited, or lines inserted
// above), nothing is written and the reason says to save.
func TestHunkRevertEdit_RefusesAStaleHunk(t *testing.T) {
	h := diffHunk{Start: 1, End: 1, Kind: diffModified, Body: []string{"-b", "+B"}}
	for _, text := range []string{"a\nX\nc\n", "new\na\nB\nc\n", "a\n"} {
		if _, why := hunkRevertEdit(hunkViewTestTab(text), h, true); why != hunkStaleReason {
			t.Errorf("buffer %q: why = %q, want the stale reason", text, why)
		}
	}
	if _, why := hunkRevertEdit(hunkViewTestTab("a\nB\nc\n"), h, false); why != "" {
		t.Errorf("a hunk that verifies by content was refused for inexact hunks: %q", why)
	}
}

// TestHunkRevertEdit_RemovalNeedsExactHunks: a removal has no "+" lines
// to verify, so it is placed only when the hunks are known to match the
// buffer.
func TestHunkRevertEdit_RemovalNeedsExactHunks(t *testing.T) {
	h := diffHunk{Start: 0, End: 0, Kind: diffDeleted, Body: []string{"-b"}}
	if _, why := hunkRevertEdit(hunkViewTestTab("a\nc\n"), h, false); !strings.HasPrefix(why, "Save the file first") {
		t.Fatalf("why = %q, want the save-first reason", why)
	}
	e, why := hunkRevertEdit(hunkViewTestTab("a\nc\n"), h, true)
	if why != "" || e.Start != (editor.Position{Line: 1}) || e.NewText != "b\n" {
		t.Fatalf("edit = %+v, %q; want an insert of \"b\\n\" at line 1", e, why)
	}
}

// TestHunksMatchBuffer pins the exact-hunks rule: clean with no stamp is
// exact; dirty is not; a revert on exact hunks stamps its rev; any edit
// after that, an Undo back to clean included, is not exact; a disk diff
// clears the stamp.
func TestHunksMatchBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFileT(t, path, "a\nB\nc\nd\n")
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	if !a.hunksMatchBuffer(tab) {
		t.Fatal("a clean tab with no revert should be exact")
	}

	h := diffHunk{Start: 1, End: 1, Kind: diffModified, Body: []string{"-b", "+B"}}
	a.fileDiffs = map[string][]diffHunk{path: {h, {Start: 2, End: 2, Kind: diffDeleted, Body: []string{"-x"}}}}
	if why := a.revertHunk(tab, h); why != "" {
		t.Fatalf("revert refused: %s", why)
	}
	if !tab.Dirty || !a.hunksMatchBuffer(tab) {
		t.Fatal("after a revert on exact hunks the dirty buffer should still be exact")
	}
	tab.Undo()
	if tab.Dirty {
		t.Fatal("undo should leave the buffer clean (test premise)")
	}
	if a.hunksMatchBuffer(tab) {
		t.Fatal("after Undo the shifted hunks no longer match, clean flag or not")
	}
	a.handleGitDiff(&gitDiffEvent{path: path, hunks: []diffHunk{h}})
	if !a.hunksMatchBuffer(tab) {
		t.Fatal("a disk diff on a clean buffer should be exact again")
	}
}

// TestDropRevertedHunk shifts the hunks below by the revert's line delta
// and leaves the ones above alone; an unknown hunk leaves the map as is.
func TestDropRevertedHunk(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	above := diffHunk{Start: 0, End: 0, Kind: diffAdded, Body: []string{"+x"}}
	mid := diffHunk{Start: 3, End: 5, Kind: diffAdded, Body: []string{"+1", "+2", "+3"}}
	below := diffHunk{Start: 9, End: 9, Kind: diffModified, Body: []string{"-y", "+Y"}}
	a.fileDiffs = map[string][]diffHunk{"p": {above, mid, below}}

	a.dropRevertedHunk("p", diffHunk{Start: 7, End: 7})
	if len(a.fileDiffs["p"]) != 3 {
		t.Fatal("an unknown hunk changed the map")
	}
	a.dropRevertedHunk("p", mid)
	got := a.fileDiffs["p"]
	if len(got) != 2 || got[0].Start != 0 || got[1].Start != 6 || got[1].End != 6 {
		t.Fatalf("hunks = %+v, want above untouched and below moved up 3", got)
	}
	a.dropRevertedHunk("p", above) // a −1 delta: below moves up again
	if left := a.fileDiffs["p"]; len(left) != 1 || left[0].Start != 5 {
		t.Fatalf("hunks = %+v, want only below, now at line 5", left)
	}
	a.dropRevertedHunk("p", a.fileDiffs["p"][0])
	if _, ok := a.fileDiffs["p"]; ok {
		t.Fatal("the last hunk's revert should drop the path")
	}
}

// TestHunkViewRows lays out both sections: the diff with kinds, then the
// original framed by context from the buffer — and, for a hunk whose
// place can't be checked, the original bare plus the reason.
func TestHunkViewRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := hunkViewTestTab("one\ntwo\nNEW\nthree\nfour\n")
	h := diffHunk{Start: 2, End: 2, Kind: diffModified, Body: []string{"-old", "+NEW"}}
	rows := a.hunkViewRows(tab, h)
	var got []string
	for _, r := range rows {
		got = append(got, r.text)
	}
	want := []string{"Diff", "-old", "+NEW", "", "Original — what Revert puts back",
		"  one", "  two", "  old", "  three", "  four"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows =\n%q\nwant\n%q", got, want)
	}
	if rows[1].kind != hunkRowRemoved || rows[2].kind != hunkRowAdded ||
		rows[5].kind != hunkRowContext || rows[7].kind != hunkRowOriginal {
		t.Fatalf("row kinds wrong: %+v", rows)
	}

	stale := a.hunkViewRows(hunkViewTestTab("x\n"), h)
	last := stale[len(stale)-1]
	if last.text != hunkStaleReason {
		t.Fatalf("stale hunk's last row = %q, want the reason", last.text)
	}
	for _, r := range stale {
		if r.kind == hunkRowContext {
			t.Fatal("a stale hunk was framed with context from the wrong lines")
		}
	}

	added := a.hunkViewRows(tab, diffHunk{Start: 2, End: 2, Kind: diffAdded, Body: []string{"+NEW"}})
	found := false
	for _, r := range added {
		found = found || strings.Contains(r.text, "these lines were added")
	}
	if !found {
		t.Fatal("a pure addition's original section doesn't say there was nothing")
	}
}

// hunkViewTestApp opens a file whose line 1 is a change against "b",
// with the hunk in the gutter, and returns the app, the tab, the path.
func hunkViewTestApp(t *testing.T) (*App, *editor.Tab, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFileT(t, path, "a\nB\nc\n")
	a := newTestApp(t, dir)
	a.width, a.height = 120, 40
	a.openFile(path)
	a.fileDiffs = map[string][]diffHunk{path: {{Start: 1, End: 1, Kind: diffModified, Body: []string{"-b", "+B"}}}}
	return a, a.activeTabPtr(), path
}

// TestHunkView_KeysAndDefault: Enter on a fresh view closes it without
// touching the file (Close is the default); alt+r reverts and closes.
func TestHunkView_KeysAndDefault(t *testing.T) {
	a, tab, _ := hunkViewTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1}, false)

	a.menuShowHunkAtCaret()
	m, ok := a.modal.(*hunkViewModal)
	if !ok {
		t.Fatalf("modal = %T, want the change view", a.modal)
	}
	a.draw()
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if a.modal != nil || tab.Buffer.String() != "a\nB\nc\n" {
		t.Fatalf("Enter on a fresh view: modal %T, buffer %q", a.modal, tab.Buffer.String())
	}

	a.menuShowHunkAtCaret()
	a.modal.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModAlt))
	if a.modal != nil {
		t.Fatalf("alt+r left modal %T open", a.modal)
	}
	if got := tab.Buffer.String(); got != "a\nb\nc\n" {
		t.Fatalf("after alt+r = %q", got)
	}
	if !strings.Contains(a.statusMsg, "Undo brings it back") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestHunkView_RefusalKeepsTheViewOpen: a revert that can't be placed
// flashes why and leaves the view up, so the change can still be read.
func TestHunkView_RefusalKeepsTheViewOpen(t *testing.T) {
	a, tab, _ := hunkViewTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1}, false)
	a.menuShowHunkAtCaret()
	tab.Buffer.Lines[1] = "edited meanwhile" // test-only: the view is modal
	a.modal.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModAlt))
	if _, ok := a.modal.(*hunkViewModal); !ok {
		t.Fatalf("modal = %T, want the view still open", a.modal)
	}
	if a.statusMsg != hunkStaleReason {
		t.Fatalf("flash = %q, want the stale reason", a.statusMsg)
	}
}

// TestHunkView_ClickRevert drives the Revert button through the real
// mouse dispatch, using the rect the modal itself reports.
func TestHunkView_ClickRevert(t *testing.T) {
	a, tab, _ := hunkViewTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1}, false)
	a.menuShowHunkAtCaret()
	a.draw()
	m := a.modal.(*hunkViewModal)
	rev, _ := m.buttons(a)
	if got := screenSpan(a, rev.x, rev.y, rev.w); got != hunkViewRevertLabel {
		t.Fatalf("drawn at the Revert rect: %q", got)
	}
	a.handleMouse(tcell.NewEventMouse(rev.x+1, rev.y, tcell.Button1, tcell.ModNone))
	if got := tab.Buffer.String(); got != "a\nb\nc\n" {
		t.Fatalf("after clicking Revert = %q", got)
	}
}

// TestMenuShowHunkAtCaret_OffAChangeFlashes: a caret on an unchanged
// line gets the reason and the way to a change, not a modal.
func TestMenuShowHunkAtCaret_OffAChangeFlashes(t *testing.T) {
	a, tab, _ := hunkViewTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 2}, false)
	a.menuShowHunkAtCaret()
	if a.modal != nil {
		t.Fatalf("modal = %T, want none", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Next change") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestEditorContextShowChangeRow: the right-click row is offered only on
// a changed line, first of the git rows, and opens the view.
func TestEditorContextShowChangeRow(t *testing.T) {
	a, tab, _ := hunkViewTestApp(t)
	a.gitIsRepo = true
	a.draw()

	m := openEditorContextAt(t, a, localXForCol(t, a, tab, 0), 0) // line 0: unchanged
	if i := contextRowIndex(m, "Show change"); i >= 0 {
		t.Fatalf("Show change offered on an unchanged line: %v", labelsOf(m))
	}
	a.closeModal()

	ex, ey, ew, eh := a.editorRect()
	cx, cy, ok := tab.PosScreenCell(editor.Position{Line: 1}, ew, eh)
	if !ok {
		t.Fatal("line 1 is not on screen")
	}
	if !a.tryEditorContextClick(ex+cx, ey+cy) {
		t.Fatal("right-click on line 1 declined")
	}
	m = a.modal.(*editorContextModal)
	i := contextRowIndex(m, "Show change")
	if i < 0 || i != contextRowIndex(m, "Blame this line")-1 {
		t.Fatalf("Show change at %d, want just before Blame; rows: %v", i, labelsOf(m))
	}
	m.hover = i
	m.activate(a)
	if _, ok := a.modal.(*hunkViewModal); !ok {
		t.Fatalf("modal after the row = %T, want the change view", a.modal)
	}
}

// TestHunkTip_ClickInsideOpensTheView: a left press inside the change
// popup is claimed (it never reaches the code under it) and opens the
// change view for the popup's hunk.
func TestHunkTip_ClickInsideOpensTheView(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	if !a.hunkGutterPress(barX, barY) {
		t.Fatal("the click door didn't open the popup")
	}
	a.draw()
	b := a.hunkTip.box
	if !a.noteHunkTipPointer(b.x+2, b.y+1, tcell.Button1) {
		t.Fatal("a press inside the popup was not claimed")
	}
	m, ok := a.modal.(*hunkViewModal)
	if !ok {
		t.Fatalf("modal = %T, want the change view", a.modal)
	}
	if m.hunk.Start != 2 || len(m.hunk.Body) != 20 {
		t.Fatalf("view opened on %+v, want the popup's hunk", m.hunk)
	}
}

// screenSpan reads w cells of one screen row as a string.
func screenSpan(a *App, x, y, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		r, _, _, _ := a.screen.GetContent(x+i, y)
		b.WriteRune(r)
	}
	return b.String()
}

// TestHunkView_PrevNextWalkTheFile: alt+n / alt+p re-target the view at
// the neighbouring change, the caret follows, and the ends flash instead
// of wrapping.
func TestHunkView_PrevNextWalkTheFile(t *testing.T) {
	a, tab, path := hunkViewTestApp(t)
	a.fileDiffs[path] = []diffHunk{
		{Start: 0, End: 0, Kind: diffModified, Body: []string{"-x", "+a"}},
		{Start: 1, End: 1, Kind: diffModified, Body: []string{"-b", "+B"}},
		{Start: 2, End: 2, Kind: diffAdded, Body: []string{"+c"}},
	}
	a.openHunkView(tab, a.fileDiffs[path][1])
	m := a.modal.(*hunkViewModal)
	alt := func(r rune) { m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt)) }

	alt('n')
	if m.hunk.Start != 2 || tab.Cursor.Line != 2 {
		t.Fatalf("next: hunk %d, caret %d", m.hunk.Start, tab.Cursor.Line)
	}
	if k, n := m.position(a); k != 3 || n != 3 {
		t.Fatalf("position = %d of %d", k, n)
	}
	alt('n')
	if m.hunk.Start != 2 || !strings.Contains(a.statusMsg, "No next change") {
		t.Fatalf("past the end: hunk %d flash %q", m.hunk.Start, a.statusMsg)
	}
	alt('p')
	alt('p')
	if m.hunk.Start != 0 {
		t.Fatalf("prev twice: hunk %d", m.hunk.Start)
	}
}

// TestHunkView_SideBySide: alt+s lays the Diff section out as git's lines
// beside the file's, flips the shared switch, and the nav buttons are
// painted where the click router finds them.
func TestHunkView_SideBySide(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, tab, path := hunkViewTestApp(t)
	a.openHunkView(tab, a.fileDiffs[path][0])
	m := a.modal.(*hunkViewModal)
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt))
	if !a.diffSplit {
		t.Fatal("alt+s did not flip the shared layout")
	}
	if m.rows[1].kind != hunkRowPair || m.rows[1].text != "b" || m.rows[1].right != "B" {
		t.Fatalf("first diff row = %+v, want b │ B", m.rows[1])
	}
	a.draw()
	prev, next, split := m.navButtons(a)
	for _, b := range []struct {
		r     btnRect
		label string
	}{{prev, hunkViewPrevLabel}, {next, hunkViewNextLabel}, {split, hunkViewUnifiedLabel}} {
		if got := screenRow(t, a, b.r.y, b.r.x, b.r.w); got != b.label {
			t.Fatalf("drawn %q, want %q", got, b.label)
		}
	}
	m.handleMouse(a, split.x+1, split.y, tcell.Button1)
	if a.diffSplit || m.rows[1].kind == hunkRowPair {
		t.Fatal("clicking Unified did not switch back")
	}
}

// TestHunkViewPairRows zips removals beside additions and keeps notes
// as full rows.
func TestHunkViewPairRows(t *testing.T) {
	rows := hunkViewPairRows([]string{"-a", "-b", "+A", `\ No newline at end of file`})
	if len(rows) != 4 || !rows[1].hasRight || rows[2].hasRight || rows[3].kind != hunkRowMeta {
		t.Fatalf("rows = %+v", rows)
	}
}
