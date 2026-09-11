// =============================================================================
// File: internal/app/treemarks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the file tree's multi-selection: the gestures that build the
// set, the picker that spends it, and the verbs behind its rows. The
// mechanics of the set itself (ordering, pruning, ranges) are pinned in
// internal/filetree; what these tests own is the app's half — which rows
// appear for which selection, and that a verb acts on exactly what the
// tick said it would.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// markApp seeds a project with three files and a folder holding two
// more, expands the folder, and returns the app plus a name→node index.
func markApp(t *testing.T) (*App, map[string]*filetree.Node) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, rel := range []string{"a.txt", "b.txt", "c.txt", "sub/x.txt", "sub/y.txt"} {
		if err := writeFile(filepath.Join(root, rel), "payload\n"); err != nil {
			t.Fatalf("seed %s: %v", rel, err)
		}
	}
	a := newTestApp(t, root)
	for _, c := range a.tree.Root.Children {
		if c.IsDir && !c.Expanded {
			a.tree.Toggle(c)
		}
	}
	idx := map[string]*filetree.Node{}
	var walk func(n *filetree.Node)
	walk = func(n *filetree.Node) {
		for _, c := range n.Children {
			idx[c.Name] = c
			if c.IsDir {
				walk(c)
			}
		}
	}
	walk(a.tree.Root)
	return a, idx
}

// treeRowY returns the screen row a node is drawn on, so mouse tests
// press where the user would rather than at a hard-coded offset.
func treeRowY(t *testing.T, a *App, n *filetree.Node) int {
	t.Helper()
	// Draw FIRST: it is what populates the hit-test's visible-row list,
	// and also what tells the tree whether the dock is drawing its title
	// (HideLabel) — which moves the header's row count. Asking ListRows
	// before the draw reads the wrong offset and lands every row one out.
	a.draw()
	_, sy, _, sh := a.sidebarRect()
	off, rows := a.tree.ListRows(sh)
	for row := 0; row < rows; row++ {
		got, ok := a.tree.HitTest(0, off+row)
		if ok && got == n {
			return sy + off + row
		}
	}
	t.Fatalf("node %s is not on screen", n.Name)
	return 0
}

// -----------------------------------------------------------------------------
// Gestures
// -----------------------------------------------------------------------------

// TestTreeMarkPress_GutterTicksWithoutOpening is the primary mouse
// gesture: a press in a row's FIRST column marks it and must not also
// open the file — the whole point of routing sidebarPress through the
// mark layer first.
func TestTreeMarkPress_GutterTicksWithoutOpening(t *testing.T) {
	a, idx := markApp(t)
	node := idx["a.txt"]
	sx, _, _, _ := a.sidebarRect()
	y := treeRowY(t, a, node)

	a.sidebarPress(sx, y, false)

	if !a.tree.IsMarked(node) {
		t.Fatal("a gutter press should mark the row")
	}
	if len(a.tabs) != 0 {
		t.Fatalf("a gutter press must not open the file, got %d tabs", len(a.tabs))
	}
	// And it is a toggle.
	a.sidebarPress(sx, y, false)
	if a.tree.IsMarked(node) {
		t.Fatal("a second gutter press should unmark")
	}
}

// TestTreeMarkPress_BodyClickStillOpens pins the other half of that
// routing: a press anywhere but the gutter falls through to the ordinary
// click, so the tree's normal behaviour is untouched by the feature.
func TestTreeMarkPress_BodyClickStillOpens(t *testing.T) {
	a, idx := markApp(t)
	node := idx["a.txt"]
	sx, _, _, _ := a.sidebarRect()
	y := treeRowY(t, a, node)

	a.sidebarPress(sx+6, y, false)

	if a.tree.MarkCount() != 0 {
		t.Fatalf("a body press must not mark, count=%d", a.tree.MarkCount())
	}
	if len(a.tabs) != 1 || a.tabs[0].Path != node.Path {
		t.Fatalf("a body press should open the file, tabs=%d", len(a.tabs))
	}
}

// TestTreeMarkPress_ShiftExtendsRange pins the bonus gesture: shift +
// a press anywhere on a row extends from the anchor, so a run of files
// is two clicks rather than one per file.
func TestTreeMarkPress_ShiftExtendsRange(t *testing.T) {
	a, _ := markApp(t)
	rows := a.tree.VisibleNodes()
	if len(rows) < 4 {
		t.Fatalf("need at least 4 rows, got %d", len(rows))
	}
	sx, _, _, _ := a.sidebarRect()

	a.sidebarPress(sx, treeRowY(t, a, rows[0]), false)  // anchor
	a.sidebarPress(sx+4, treeRowY(t, a, rows[3]), true) // shift-extend

	for i := 0; i <= 3; i++ {
		if !a.tree.IsMarked(rows[i]) {
			t.Fatalf("row %d (%s) should be marked by the range", i, rows[i].Name)
		}
	}
	if len(a.tabs) != 0 {
		t.Fatal("a shift press must not open a file")
	}
}

// TestTreeMarkKeys_SpaceStarAndActions pins the keyboard half: Space
// ticks the cursor's row, '*' marks every visible row and clears again,
// and 'A' opens the verb picker.
func TestTreeMarkKeys_SpaceStarAndActions(t *testing.T) {
	a, idx := markApp(t)
	a.focusTree()
	a.tree.Selected = idx["b.txt"]

	a.treeNavRune(' ', a.tree.Selected)
	if !a.tree.IsMarked(idx["b.txt"]) {
		t.Fatal("Space should tick the cursor's row")
	}

	a.treeNavRune('*', a.tree.Selected)
	if a.tree.MarkCount() != 0 {
		t.Fatalf("'*' with a live set should clear it, count=%d", a.tree.MarkCount())
	}
	a.treeNavRune('*', a.tree.Selected)
	if got, want := a.tree.MarkCount(), len(a.tree.VisibleNodes()); got != want {
		t.Fatalf("'*' on an empty set marked %d rows, want %d", got, want)
	}

	a.treeNavRune('A', a.tree.Selected)
	if paletteOf(a) == nil {
		t.Fatal("'A' should open the actions picker")
	}
}

// TestTreeMarkKeys_SpaceDoesNotTypeahead pins that the multi-selection
// keys shadow typeahead rather than leaking into it — a Space that
// jumped the cursor somewhere would make ticking a run impossible.
func TestTreeMarkKeys_SpaceDoesNotTypeahead(t *testing.T) {
	a, idx := markApp(t)
	a.focusTree()
	a.tree.Selected = idx["b.txt"]
	a.treeNavRune(' ', a.tree.Selected)
	if a.tree.Selected != idx["b.txt"] {
		t.Fatalf("Space moved the cursor to %s", a.tree.Selected.Name)
	}
}

// -----------------------------------------------------------------------------
// Targets and the picker
// -----------------------------------------------------------------------------

// TestTreeMarkTargets_FallsBackToCursor pins gitPanelTargets' rule: with
// nothing ticked the picker still acts on the row under the cursor, so
// the feature is useful on its first open.
func TestTreeMarkTargets_FallsBackToCursor(t *testing.T) {
	a, idx := markApp(t)
	a.tree.Selected = idx["c.txt"]
	got := a.treeMarkTargets()
	if len(got) != 1 || got[0] != idx["c.txt"] {
		t.Fatalf("targets = %v, want just the cursor's row", got)
	}

	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["b.txt"], true)
	if got := a.treeMarkTargets(); len(got) != 2 {
		t.Fatalf("with marks, targets = %d, want 2", len(got))
	}
}

// TestTreeMarkActionItems_RowsForASet pins which verbs a mixed set is
// offered, and that the labels carry the count — the picker's title and
// rows are the only place a user can check the blast radius before
// spending it.
func TestTreeMarkActionItems_RowsForASet(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["b.txt"], true)
	items := a.treeMarkActionItems(a.treeMarkTargets())
	labels := actionLabels(items)

	for _, want := range []string{
		"Open 2 items", "Copy relative path of 2 items", "Copy absolute path of 2 items",
		"Copy 2 items for paste", "Zip 2 items…", "Delete 2 items…",
		"Clear selection (2)", "Select all visible rows",
	} {
		if !strings.Contains(labels, want) {
			t.Errorf("missing row %q; have %s", want, labels)
		}
	}
	if !strings.Contains(a.treeMarkActionsTitle(), "(2)") {
		t.Errorf("title = %q, want the count", a.treeMarkActionsTitle())
	}
}

// TestTreeMarkActionItems_OpenSkipsFolders pins the filter rather than a
// refusal: a set holding a folder and two files still offers Open, and
// it offers it for the two files.
func TestTreeMarkActionItems_OpenSkipsFolders(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["sub"], true)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["b.txt"], true)
	labels := actionLabels(a.treeMarkActionItems(a.treeMarkTargets()))
	if !strings.Contains(labels, "Open 2 items") {
		t.Fatalf("Open should cover the two files only; have %s", labels)
	}
}

// TestTreeMarkActionItems_FolderOnlySetHasNoOpenRow pins the omit-rather-
// than-dim rule: with nothing openable in the set, the row is gone.
func TestTreeMarkActionItems_FolderOnlySetHasNoOpenRow(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["sub"], true)
	if labels := actionLabels(a.treeMarkActionItems(a.treeMarkTargets())); strings.Contains(labels, "Open ") {
		t.Fatalf("a folder-only set should offer no Open row; have %s", labels)
	}
}

// TestTreeMarkActionItems_GitRowsNeedARepo pins that staging is offered
// only where it means something — and that Discard never is, since this
// surface cannot show the diff a discard would destroy.
func TestTreeMarkActionItems_GitRowsNeedARepo(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)

	if labels := actionLabels(a.treeMarkActionItems(a.treeMarkTargets())); strings.Contains(labels, "Stage") {
		t.Fatalf("no repo should mean no Stage row; have %s", labels)
	}
	a.gitIsRepo = true
	labels := actionLabels(a.treeMarkActionItems(a.treeMarkTargets()))
	if !strings.Contains(labels, "Stage a.txt") || !strings.Contains(labels, "Unstage a.txt") {
		t.Fatalf("a repo should offer both staging rows; have %s", labels)
	}
	if strings.Contains(labels, "Discard") {
		t.Fatalf("Discard belongs to the git panel, not here; have %s", labels)
	}
}

// TestOpenTreeMarkActions_EmptyRefusesWithTheGesture pins the refusal:
// with no marks and no cursor there is nothing to act on, and the flash
// names the gesture that builds a set — which a dimmed menu row could
// not do.
func TestOpenTreeMarkActions_EmptyRefusesWithTheGesture(t *testing.T) {
	a, _ := markApp(t)
	a.tree.Selected = nil
	a.openTreeMarkActions()
	if paletteOf(a) != nil {
		t.Fatal("an empty selection should not open a picker")
	}
	if !strings.Contains(a.statusMsg, "Space") {
		t.Fatalf("flash = %q, want the marking gesture named", a.statusMsg)
	}
}

// TestTreeMarkActionsLabel_CarriesTheCount pins the ≡ row's label: it is
// where a user learns they still have rows ticked, since the tree's own
// header count is invisible while the sidebar is hidden.
func TestTreeMarkActionsLabel_CarriesTheCount(t *testing.T) {
	a, idx := markApp(t)
	if got := a.treeMarkActionsLabel(); got != "File tree actions…" {
		t.Fatalf("empty label = %q", got)
	}
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["b.txt"], true)
	if got := a.treeMarkActionsLabel(); !strings.Contains(got, "(2)") {
		t.Fatalf("label = %q, want the count", got)
	}
}

// -----------------------------------------------------------------------------
// The verbs
// -----------------------------------------------------------------------------

// TestTreeMarkOpen_OpensEveryFile pins the Open verb over a set.
func TestTreeMarkOpen_OpensEveryFile(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["x.txt"], true)
	actionItem(t, a.treeMarkActionItems(a.treeMarkTargets()), "Open 2 items").run(a)

	if len(a.tabs) != 2 {
		t.Fatalf("opened %d tabs, want 2", len(a.tabs))
	}
	if a.treeFocus {
		t.Fatal("opening files should hand focus to the editor")
	}
}

// TestTreeMarkDelete_ConfirmsThenDeletesTheSet pins the destructive
// verb: it confirms first, the body LISTS what will go (a mark survives
// scrolling and folding, so a count alone would ask the user to approve
// a deletion they cannot see), and Yes removes every path and clears the
// set.
func TestTreeMarkDelete_ConfirmsThenDeletesTheSet(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["sub"], true)
	paths := []string{idx["a.txt"].Path, idx["sub"].Path}

	actionItem(t, a.treeMarkActionItems(a.treeMarkTargets()), "Delete 2 items…").run(a)
	m := confirmOf(a)
	if m == nil {
		t.Fatal("Delete should confirm first")
	}
	body := strings.Join(m.lines, "\n")
	if !strings.Contains(body, "a.txt") || !strings.Contains(body, "sub/") {
		t.Fatalf("confirm body should name both targets, got:\n%s", body)
	}
	if !strings.Contains(body, "everything inside them") {
		t.Fatalf("a set holding a folder must keep the recursive warning, got:\n%s", body)
	}

	m.callback(a)
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s survived the delete", p)
		}
	}
	if a.tree.MarkCount() != 0 {
		t.Fatalf("the set should be cleared after a delete, count=%d", a.tree.MarkCount())
	}
}

// TestTreeMarkDelete_SingleTargetUsesTheOrdinaryDialog pins that a
// one-item set is the existing single-file confirm, verbatim: the verb
// reached from a tick must not look different from the one reached by
// right-clicking the same row.
func TestTreeMarkDelete_SingleTargetUsesTheOrdinaryDialog(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.treeMarkDelete(a.treeMarkTargets())
	m := confirmOf(a)
	if m == nil {
		t.Fatal("expected a confirm")
	}
	if len(m.lines) != 0 {
		t.Fatalf("a single target should use the one-line dialog, got lines %v", m.lines)
	}
}

// TestTreeMarkPathLines_OnePathPerLine pins the clipboard shape for a
// set — one project-relative path per line, in tree order, which is the
// form every shell, editor and chat prompt accepts. Asserted on the pure
// renderer rather than through clipboard.CopyToSystem, which writes to
// /dev/tty and is therefore not available on every test host.
func TestTreeMarkPathLines_OnePathPerLine(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["x.txt"], true)

	got := treeMarkPathLines(a.treeMarkTargets(), a.relativePathFor)
	want := []string{filepath.Join("sub", "x.txt"), "a.txt"} // sub/ sorts first
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines = %v, want %v", got, want)
	}

	abs := treeMarkPathLines(a.treeMarkTargets(), absolutePathFor)
	for _, l := range abs {
		if !filepath.IsAbs(l) {
			t.Fatalf("absolute rendering produced %q", l)
		}
	}
}

// TestTreeMarkCopyForPaste_ArmsTheWholeSet pins the bridge to
// copypaste.go: the marks arm the file clipboard as a set, so the
// existing Paste surfaces land all of them.
func TestTreeMarkCopyForPaste_ArmsTheWholeSet(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.tree.SetMark(idx["b.txt"], true)
	actionItem(t, a.treeMarkActionItems(a.treeMarkTargets()), "Copy 2 items for paste").run(a)

	if len(a.fileClipPaths) != 2 || a.clipKind != clipFile {
		t.Fatalf("clip = %v kind=%v, want both files", a.fileClipPaths, a.clipKind)
	}
	if !a.hasFileClip() {
		t.Fatal("hasFileClip should report the set")
	}
	if got := a.pasteItemLabel(); !strings.Contains(got, "2 items") {
		t.Fatalf("paste label = %q, want the count", got)
	}
}

// TestTreeMarkFolderTarget_NeedsAnExpandedFolder pins the "select
// contents of…" row's gate: an unexpanded folder has no rows to tick,
// and expanding one as a side effect of reading a menu would be a
// surprise, so the row simply stays out.
func TestTreeMarkFolderTarget_NeedsAnExpandedFolder(t *testing.T) {
	a, idx := markApp(t)
	sub := idx["sub"]

	a.tree.Selected = sub
	if got := a.treeMarkFolderTarget(); got != sub {
		t.Fatalf("expanded folder should be the target, got %v", got)
	}
	a.tree.Toggle(sub) // collapse
	if got := a.treeMarkFolderTarget(); got != nil {
		t.Fatalf("collapsed folder should offer no target, got %v", got)
	}

	// A file's target is its parent folder.
	a.tree.Toggle(sub)
	a.tree.Selected = idx["x.txt"]
	if got := a.treeMarkFolderTarget(); got != sub {
		t.Fatalf("a file's target should be its folder, got %v", got)
	}
}

// TestCtxToggleMark_MarksTheClickedRow pins the context-menu row that is
// the feature's discovery surface: it marks the row the popup was opened
// on, not wherever the keyboard cursor happened to be.
func TestCtxToggleMark_MarksTheClickedRow(t *testing.T) {
	a, idx := markApp(t)
	a.tree.Selected = idx["c.txt"]
	ctxToggleMark(a, idx["a.txt"])
	if !a.tree.IsMarked(idx["a.txt"]) || a.tree.IsMarked(idx["c.txt"]) {
		t.Fatal("the right-clicked row is the one that gets marked")
	}
}

// TestOpenTreeContext_ShowsSelectedRowWhenMarked pins the context menu's
// second mark row: it appears only once something is ticked, and it opens
// the same picker the ≡ row does.
func TestOpenTreeContext_ShowsSelectedRowWhenMarked(t *testing.T) {
	a, idx := markApp(t)
	a.tree.SetMark(idx["a.txt"], true)
	a.openTreeContext(idx["b.txt"], 5, 5)

	m := contextOf(a)
	if m == nil {
		t.Fatal("context menu should open")
	}
	var labels []string
	for _, it := range m.items {
		labels = append(labels, it.label)
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "Selected (1)…") {
		t.Fatalf("want a Selected row; have %s", joined)
	}
	// The clicked row is unmarked, so its own row invites a tick.
	if !strings.Contains(joined, "Select") {
		t.Fatalf("want a Select row; have %s", joined)
	}
}
