// =============================================================================
// File: internal/filetree/filetree_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// Tests for the filetree package — the lazy file explorer that powers the
// editor's left sidebar. These pin down disk-merge behavior (refresh keeps
// expanded folders open), the small visibility/hide rules, the flatten +
// hit-test math, and a handful of render assertions made via tcell's
// SimulationScreen so we can verify chevrons, the bold active row, etc.

package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/icons"
	"github.com/rohanthewiz/ced/internal/theme"
)

// mkTree is a tiny helper that builds a small directory layout under t.TempDir
// and returns the absolute root path. Several tests use the same shape so
// pulling it into a helper keeps each test focused on the behavior it cares
// about rather than scaffolding.
func mkTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "alpha"))
	mustMkdir(t, filepath.Join(root, "Beta"))
	mustMkdir(t, filepath.Join(root, ".git")) // hidden — should be filtered
	mustMkdir(t, filepath.Join(root, "node_modules"))
	mustWrite(t, filepath.Join(root, "zeta.txt"), "z")
	mustWrite(t, filepath.Join(root, "Apple.md"), "a")
	mustWrite(t, filepath.Join(root, ".DS_Store"), "junk")
	mustWrite(t, filepath.Join(root, "alpha", "inner.go"), "package x")
	return root
}

// mustMkdir is a fail-on-error mkdir helper for test setup.
func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

// mustWrite is a fail-on-error file-write helper for test setup.
func mustWrite(t *testing.T, p, contents string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// mustWriteMode is a fail-on-error file-write helper that sets an explicit
// permission mode — used to exercise the executable-bit detection that
// drives the ls -F '*' marker.
func mustWriteMode(t *testing.T, p, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, []byte(contents), mode); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// findChild walks a node's children for an entry named name. Returns nil
// when not present so tests can assert absence as well as presence.
func findChild(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestNew_NonExistentRoot verifies that pointing the tree at a path that
// doesn't exist surfaces an error rather than panicking or producing an
// empty tree (which would silently mislead the user).
func TestNew_NonExistentRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := New(missing); err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

// TestNew_RootIsFile guards the "user passed a filename, not a folder" case.
// The constructor should reject it instead of trying to read children.
func TestNew_RootIsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	mustWrite(t, f, "hi")
	if _, err := New(f); err == nil {
		t.Fatal("expected error when root is a regular file")
	}
}

// TestNew_LoadsAndHides confirms a successful build returns a tree whose
// root is expanded, has its children loaded, and excludes the well-known
// noise entries (.git, node_modules, .DS_Store).
func TestNew_LoadsAndHides(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.Root.IsDir || !tr.Root.Expanded || !tr.Root.Loaded {
		t.Fatalf("root flags wrong: %+v", tr.Root)
	}
	for _, hidden := range []string{".git", ".DS_Store", "node_modules"} {
		if findChild(tr.Root, hidden) != nil {
			t.Fatalf("hidden entry %s should have been filtered", hidden)
		}
	}
	// Sanity: visible names ARE present.
	for _, want := range []string{"alpha", "Beta", "zeta.txt", "Apple.md"} {
		if findChild(tr.Root, want) == nil {
			t.Fatalf("expected child %s to be present", want)
		}
	}
}

// TestLoadChildren_SortOrder asserts directories sort before files and that
// each group is case-insensitive alphabetical — what users expect from a
// VSCode-style sidebar.
func TestLoadChildren_SortOrder(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names := make([]string, 0, len(tr.Root.Children))
	for _, c := range tr.Root.Children {
		names = append(names, c.Name)
	}
	// Expected: alpha, Beta (dirs alpha-by-lower), then Apple.md, zeta.txt.
	want := []string{"alpha", "Beta", "Apple.md", "zeta.txt"}
	if len(names) != len(want) {
		t.Fatalf("child count mismatch: got %v want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("sort mismatch at %d: got %q want %q (full=%v)", i, names[i], n, names)
		}
	}
}

// TestRefresh_PreservesExpandedState verifies that refreshing the tree
// after files appear or vanish on disk keeps the *Node pointers (and
// their Expanded flag) intact for entries that still exist — important
// because the 10-second auto-refresh would otherwise collapse every
// folder the user had opened.
func TestRefresh_PreservesExpandedState(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if alpha == nil {
		t.Fatal("alpha missing")
	}
	tr.Toggle(alpha) // expand + load
	if !alpha.Expanded || !alpha.Loaded {
		t.Fatalf("alpha state after toggle wrong: %+v", alpha)
	}

	// Mutate disk: add a new sibling, remove zeta.txt.
	mustWrite(t, filepath.Join(root, "Newcomer.txt"), "n")
	if err := os.Remove(filepath.Join(root, "zeta.txt")); err != nil {
		t.Fatalf("remove zeta: %v", err)
	}

	tr.Refresh()

	// Pointer identity preserved for survivors.
	alphaAfter := findChild(tr.Root, "alpha")
	if alphaAfter != alpha {
		t.Fatal("alpha pointer changed across refresh")
	}
	if !alphaAfter.Expanded {
		t.Fatal("alpha.Expanded was lost across refresh")
	}
	// New file appears.
	if findChild(tr.Root, "Newcomer.txt") == nil {
		t.Fatal("Newcomer.txt should have been picked up")
	}
	// Deleted file vanished.
	if findChild(tr.Root, "zeta.txt") != nil {
		t.Fatal("zeta.txt should have been removed from the tree")
	}
}

// TestShouldHide is an exhaustive table for the small hide list — keeps
// future edits to that list honest by showing exactly what's in/out.
func TestShouldHide(t *testing.T) {
	cases := []struct {
		name string
		hide bool
	}{
		{".git", true},
		{".svn", true},
		{".hg", true},
		{".DS_Store", true},
		{"node_modules", true},
		{".idea", true},
		{".vscode", true},
		{"main.go", false},
		{"README.md", false},
		{".env", false}, // dotfiles are intentionally NOT hidden
		{"git", false},
		{"node_modules2", false},
	}
	for _, tc := range cases {
		if got := shouldHide(tc.name); got != tc.hide {
			t.Errorf("shouldHide(%q) = %v, want %v", tc.name, got, tc.hide)
		}
	}
}

// TestFlattenInto_Collapsed ensures a non-expanded directory contributes
// only itself to the flat list — its children stay hidden until the user
// expands it.
func TestFlattenInto_Collapsed(t *testing.T) {
	dir := &Node{Name: "d", IsDir: true, Expanded: false, Children: []*Node{
		{Name: "c1"}, {Name: "c2"},
	}}
	var out []flatNode
	flattenInto(dir, 0, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 row for collapsed dir, got %d", len(out))
	}
	if out[0].Depth != 0 {
		t.Fatalf("depth wrong: %d", out[0].Depth)
	}
}

// TestFlattenInto_Expanded checks the recursive case: an expanded directory
// flattens itself plus children at depth+1, and nested expansion compounds.
func TestFlattenInto_Expanded(t *testing.T) {
	leaf := &Node{Name: "leaf"}
	inner := &Node{Name: "inner", IsDir: true, Expanded: true, Children: []*Node{leaf}}
	root := &Node{Name: "root", IsDir: true, Expanded: true, Children: []*Node{inner}}

	var out []flatNode
	flattenInto(root, 0, &out)

	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[0].Node.Name != "root" || out[0].Depth != 0 {
		t.Fatalf("row 0 wrong: %+v", out[0])
	}
	if out[1].Node.Name != "inner" || out[1].Depth != 1 {
		t.Fatalf("row 1 wrong: %+v", out[1])
	}
	if out[2].Node.Name != "leaf" || out[2].Depth != 2 {
		t.Fatalf("row 2 wrong: %+v", out[2])
	}
}

// TestFlattenInto_NilSafe documents that a nil *Node is a tolerated input
// (defensive: avoids requiring callers to nil-check before recursing).
func TestFlattenInto_NilSafe(t *testing.T) {
	var out []flatNode
	flattenInto(nil, 0, &out)
	if len(out) != 0 {
		t.Fatalf("nil node should produce no rows, got %d", len(out))
	}
}

// TestToggle_LoadsThenFlips verifies the two-step contract for Toggle:
// the first call on a never-loaded directory loads its children AND flips
// Expanded; subsequent calls just flip Expanded without re-reading disk.
func TestToggle_LoadsThenFlips(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if alpha.Loaded || alpha.Expanded {
		t.Fatalf("alpha should start unloaded+collapsed: %+v", alpha)
	}

	tr.Toggle(alpha)
	if !alpha.Expanded || !alpha.Loaded {
		t.Fatalf("after first toggle alpha should be expanded+loaded: %+v", alpha)
	}
	if len(alpha.Children) == 0 {
		t.Fatal("expected alpha's children to be loaded")
	}

	tr.Toggle(alpha)
	if alpha.Expanded {
		t.Fatal("second toggle should collapse")
	}
	tr.Toggle(alpha)
	if !alpha.Expanded {
		t.Fatal("third toggle should re-expand")
	}
}

// TestToggle_FileIsNoop ensures Toggle on a file doesn't mutate state —
// only directories have an open/closed concept.
func TestToggle_FileIsNoop(t *testing.T) {
	tr := &Tree{Root: &Node{Name: "r", IsDir: true}}
	f := &Node{Name: "x.txt"}
	tr.Toggle(f)
	if f.Expanded || f.Loaded {
		t.Fatalf("file node should not be mutated: %+v", f)
	}
}

// TestScroll_ClampsAtZero exercises Tree.Scroll's lower bound: scrolling
// past the top should pin at 0 rather than going negative.
func TestScroll_ClampsAtZero(t *testing.T) {
	tr := &Tree{Root: &Node{IsDir: true}}
	tr.Scroll(-5)
	if tr.ScrollY != 0 {
		t.Fatalf("ScrollY should clamp to 0, got %d", tr.ScrollY)
	}
	tr.Scroll(3)
	if tr.ScrollY != 3 {
		t.Fatalf("expected ScrollY=3, got %d", tr.ScrollY)
	}
	tr.Scroll(-10)
	if tr.ScrollY != 0 {
		t.Fatalf("ScrollY should clamp to 0 after big up-scroll, got %d", tr.ScrollY)
	}
}

// TestClampScroll_AllCases tabulates clampScroll's three regimes: list
// fits entirely (=> 0), overflow with valid scroll (=> unchanged), and
// scroll past max (=> pinned to total-viewH).
func TestClampScroll_AllCases(t *testing.T) {
	cases := []struct {
		label  string
		start  int
		total  int
		viewH  int
		expect int
	}{
		{"fits entirely", 4, 5, 10, 0},
		{"in range", 3, 20, 10, 3},
		{"past max", 50, 20, 10, 10},
		{"negative", -5, 20, 10, 0},
	}
	for _, c := range cases {
		tr := &Tree{ScrollY: c.start}
		tr.clampScroll(c.total, c.viewH)
		if tr.ScrollY != c.expect {
			t.Errorf("%s: ScrollY=%d want %d", c.label, tr.ScrollY, c.expect)
		}
	}
}

// TestMaxScroll_MatchesClampScroll pins the exported ceiling against the
// clamp it was pulled out of: the sidebar's scrollbar places its thumb
// against exactly this number, so a drift between the two would show up
// as a thumb that can't be dragged to the bottom of a tree the wheel
// scrolls to happily. Unlike the editor's there is no overscroll pad.
func TestMaxScroll_MatchesClampScroll(t *testing.T) {
	cases := []struct {
		label string
		files int
		viewH int
		want  int
	}{
		{"list fits", 5, 10, 0},
		{"list exactly fits", 10, 10, 0},
		{"list overflows", 25, 10, 15},
		{"no band at all", 25, 0, 25},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for i := 0; i < c.files; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("x"), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		tr, err := New(dir)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := tr.RowCount(); got != c.files {
			t.Fatalf("%s: RowCount = %d, want %d", c.label, got, c.files)
		}
		if got := tr.MaxScroll(c.viewH); got != c.want {
			t.Errorf("%s: MaxScroll(%d) = %d, want %d", c.label, c.viewH, got, c.want)
		}
		tr.ScrollY = 9999
		tr.clampScroll(c.files, c.viewH)
		if tr.ScrollY != c.want {
			t.Errorf("%s: clampScroll capped at %d, want MaxScroll's %d", c.label, tr.ScrollY, c.want)
		}
	}
}

// TestListRows_SplitsOffTheHeader pins the one number the scrollbar
// needed out of this package: the file list starts two rows down (the
// EXPLORER label and the project name) and gets whatever is left.
func TestListRows_SplitsOffTheHeader(t *testing.T) {
	tr := &Tree{}
	if off, rows := tr.ListRows(20); off != 2 || rows != 18 {
		t.Errorf("ListRows(20) = (%d, %d), want (2, 18)", off, rows)
	}
	// A rect too short for the header must report no list rather than a
	// negative band — Render loops on this count.
	if off, rows := tr.ListRows(1); off != 2 || rows != 0 {
		t.Errorf("ListRows(1) = (%d, %d), want (2, 0)", off, rows)
	}
}

// TestHitTest_ExplorerHeaderMisses confirms y=0 (the all-caps
// "EXPLORER" row) is not a click target — clicking it should
// neither set the active folder nor open anything.
func TestHitTest_ExplorerHeaderMisses(t *testing.T) {
	tr := &Tree{visible: []*Node{{Name: "a"}}}
	if n, ok := tr.HitTest(0, 0); ok || n != nil {
		t.Fatalf("EXPLORER row should miss, got ok=%v node=%v", ok, n)
	}
}

// TestHitTest_ProjectRootRowReturnsRoot pins the "click the project
// name to reset active folder" behaviour. Without this, once a user
// has selected any subfolder there's no way to set the active folder
// back to the project root short of restarting the editor.
func TestHitTest_ProjectRootRowReturnsRoot(t *testing.T) {
	root := &Node{Name: "proj", IsDir: true, Path: "/proj"}
	tr := &Tree{Root: root, visible: []*Node{{Name: "a"}}}
	n, ok := tr.HitTest(0, 1)
	if !ok || n != root {
		t.Fatalf("y=1 should map to root, got ok=%v node=%v", ok, n)
	}
}

// TestHitTest_ValidRow checks the happy path: a click on a real row maps
// back to the same Node we recorded during the last Render.
func TestHitTest_ValidRow(t *testing.T) {
	target := &Node{Name: "x"}
	tr := &Tree{visible: []*Node{target, nil}}
	n, ok := tr.HitTest(5, 2) // first list row
	if !ok || n != target {
		t.Fatalf("expected hit on target, got ok=%v n=%v", ok, n)
	}
	// nil entry (blank padding row) should miss.
	if n, ok := tr.HitTest(5, 3); ok || n != nil {
		t.Fatalf("blank row should miss, got ok=%v n=%v", ok, n)
	}
}

// TestHitTest_OutOfRange covers clicks below the last visible row — the
// renderer pads with nil but the hit test should still cleanly miss.
func TestHitTest_OutOfRange(t *testing.T) {
	tr := &Tree{visible: []*Node{{Name: "a"}}}
	if n, ok := tr.HitTest(0, 99); ok || n != nil {
		t.Fatalf("out-of-range should miss, got ok=%v n=%v", ok, n)
	}
}

// renderAndCollect is a small helper that builds a SimulationScreen, runs
// Tree.Render, and returns the cell buffer + width so individual tests
// can inspect both runes and styles.
func renderAndCollect(t *testing.T, tr *Tree, w, h int) ([]tcell.SimCell, int) {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(w, h)
	tr.Render(scr, theme.Default(), 0, 0, w, h)
	scr.Show() // flush back buffer to front so GetContents sees it
	cells, cw, _ := scr.GetContents()
	return cells, cw
}

// rowText reconstructs the visible text of a single screen row, which is
// far more readable in test failures than dumping the raw cell array.
func rowText(cells []tcell.SimCell, w, y int) string {
	row := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) == 0 {
			row = append(row, ' ')
			continue
		}
		row = append(row, c.Runes[0])
	}
	return string(row)
}

// TestRender_ProjectNameAndChevrons asserts that the explorer header shows
// the project (root) name on row 1 and that an expanded directory renders
// with a '▾' while a collapsed sibling renders with a '▸'.
func TestRender_ProjectNameAndChevrons(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// alpha will appear collapsed (default), Beta the same. Force alpha
	// expanded so we can see both chevrons in one render.
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand alpha

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Row 1 should contain the project (root) folder name.
	rootName := filepath.Base(root)
	if got := rowText(cells, w, 1); !containsRune(got, rootName) {
		t.Fatalf("row 1 missing project name %q: got %q", rootName, got)
	}

	// Find the row containing alpha; verify '▾' present.
	if !findRowWithBoth(cells, w, 20, "alpha", '▾') {
		t.Fatal("expected an expanded-row showing alpha with '▾'")
	}
	// Beta is collapsed — verify '▸' present.
	if !findRowWithBoth(cells, w, 20, "Beta", '▸') {
		t.Fatal("expected a collapsed-row showing Beta with '▸'")
	}
}

// TestRender_ActiveFolderIsBold sets ActiveFolder to alpha's path and
// checks that alpha's row carries the AttrBold style — the visual cue
// the user uses to confirm where "New file" will land.
func TestRender_ActiveFolderIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Find any cell on the alpha row; assert the foreground style has Bold.
	rowY := -1
	for y := 2; y < 20; y++ {
		if containsRune(rowText(cells, w, y), "alpha") {
			rowY = y
			break
		}
	}
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	// Scan the row for any cell with AttrBold set.
	bold := false
	for x := 0; x < w; x++ {
		_, _, attr := cells[rowY*w+x].Style.Decompose()
		if attr&tcell.AttrBold != 0 {
			bold = true
			break
		}
	}
	if !bold {
		t.Fatal("expected alpha row to be rendered bold (active folder)")
	}
}

// TestRender_ActiveFileIsBold sets ActiveFile to a top-level file's path
// and checks that its row renders bold while a sibling file does not —
// the cue that tells the user which tree entry is open in the active tab.
func TestRender_ActiveFileIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.ActiveFile = findChild(tr.Root, "zeta.txt").Path

	cells, w := renderAndCollect(t, tr, 40, 20)

	activeY := findRowY(cells, w, 20, "zeta.txt")
	if activeY < 0 {
		t.Fatal("could not find zeta.txt row")
	}
	if !rowHasBold(cells, w, activeY) {
		t.Fatal("expected zeta.txt row to be bold (active file)")
	}
	// A sibling file that isn't the active tab must stay un-bold, so the
	// cue actually distinguishes the open file.
	otherY := findRowY(cells, w, 20, "Apple.md")
	if otherY < 0 {
		t.Fatal("could not find Apple.md row")
	}
	if rowHasBold(cells, w, otherY) {
		t.Fatal("non-active file Apple.md should not be bold")
	}
}

// TestRender_NoActiveFileNoBoldFile verifies that with ActiveFile empty
// no file row is bolded — the empty path must never match a real node
// and light up an arbitrary row.
func TestRender_NoActiveFileNoBoldFile(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.ActiveFile = ""

	cells, w := renderAndCollect(t, tr, 40, 20)
	for _, name := range []string{"zeta.txt", "Apple.md"} {
		y := findRowY(cells, w, 20, name)
		if y >= 0 && rowHasBold(cells, w, y) {
			t.Fatalf("%s should not be bold when ActiveFile is empty", name)
		}
	}
}

// TestRender_TinyHeightDoesNotPanic guards against an off-by-one when the
// caller hands Render a height smaller than the 2-row header — listH goes
// to zero and we shouldn't blow up dividing or indexing.
func TestRender_TinyHeightDoesNotPanic(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(20, 1)
	tr.Render(scr, theme.Default(), 0, 0, 20, 1) // listH would be -1 -> clamped to 0
	// no panic = pass; also visible must be empty.
	if len(tr.visible) != 0 {
		t.Fatalf("expected empty visible slice, got len=%d", len(tr.visible))
	}
}

// TestRender_DirtyFileUsesModifiedColor seeds the tree's DirtyFiles set
// with one path and asserts the renderer paints that row in
// theme.Modified — the colour the editor uses everywhere else (tab dot,
// future status indicators) for "uncommitted change".
func TestRender_DirtyFileUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := alpha.reload(); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("alpha/inner.go missing from fixture")
	}
	tr.DirtyFiles = map[string]bool{inner.Path: true}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Modified) {
		t.Fatalf("expected inner.go row to be drawn in Modified color")
	}
}

// TestRender_DirtyFolderUsesModifiedColor proves that a folder appearing
// in DirtyFolders gets the Modified colour even when none of its visible
// children do — collapsed branches still need to signal "something
// changed inside".
func TestRender_DirtyFolderUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.DirtyFolders = map[string]bool{alpha.Path: true}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Modified) {
		t.Fatal("expected alpha folder row to be drawn in Modified color")
	}
}

// TestRender_DirtyAndActiveStaysBold confirms that the active-folder
// styling (bold) and the dirty-folder styling (Modified colour) compose
// cleanly — the user shouldn't lose the "current target" cue just
// because the folder also has uncommitted changes.
func TestRender_DirtyAndActiveStaysBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path
	tr.DirtyFolders = map[string]bool{alpha.Path: true}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Modified) {
		t.Error("expected alpha row to be Modified colour")
	}
	if !rowHasBold(cells, w, rowY) {
		t.Error("expected alpha row to remain bold")
	}
}

// findRowY scans the rendered cell buffer for the first row whose
// reconstructed text contains needle.
func findRowY(cells []tcell.SimCell, w, h int, needle string) int {
	for y := 0; y < h; y++ {
		if containsRune(rowText(cells, w, y), needle) {
			return y
		}
	}
	return -1
}

// rowHasColor reports whether any non-blank cell in row y was drawn with
// the given foreground colour. The tree pads rows with blank spaces; we
// ignore those so a leading-pad colour mismatch isn't reported.
func rowHasColor(cells []tcell.SimCell, w, y int, want tcell.Color) bool {
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) == 0 || c.Runes[0] == ' ' {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg == want {
			return true
		}
	}
	return false
}

// rowHasBold reports whether any cell in row y carries tcell.AttrBold.
func rowHasBold(cells []tcell.SimCell, w, y int) bool {
	for x := 0; x < w; x++ {
		_, _, attr := cells[y*w+x].Style.Decompose()
		if attr&tcell.AttrBold != 0 {
			return true
		}
	}
	return false
}

// containsRune is a tiny "string contains substring" wrapper that keeps
// the imports of this test file lean.
func containsRune(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// findRowWithBoth scans the simulation buffer for any row that contains
// both the given name substring and the given chevron rune — used to
// assert "Beta is shown collapsed" / "alpha is shown expanded".
func findRowWithBoth(cells []tcell.SimCell, w, h int, name string, chev rune) bool {
	for y := 0; y < h; y++ {
		text := rowText(cells, w, y)
		if !containsRune(text, name) {
			continue
		}
		for _, r := range text {
			if r == chev {
				return true
			}
		}
	}
	return false
}

// TestRender_IconsDisabledByDefault pins down the default look — a tree
// whose IconsEnabled flag was never flipped should not embed any Nerd
// Font glyph in its output. Important so users on terminals without a
// Nerd Font don't see broken-glyph "tofu" boxes after upgrading.
func TestRender_IconsDisabledByDefault(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	// Walk every visible row and assert none of the file-default,
	// folder-open, or folder-closed glyphs appear.
	for y := 0; y < 20; y++ {
		row := rowText(cells, w, y)
		for _, g := range []string{icons.FileDefault, icons.FolderOpen, icons.FolderClosed} {
			if containsRune(row, g) {
				t.Fatalf("row %d unexpectedly contains glyph %q: %q", y, g, row)
			}
		}
	}
}

// TestRender_IconsEnabledShowsFolderGlyph verifies that flipping
// IconsEnabled actually emits the folder-closed glyph for an
// unexpanded directory — the most common visible case.
func TestRender_IconsEnabledShowsFolderGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "Beta") // collapsed
	if rowY < 0 {
		t.Fatal("could not find Beta row")
	}
	if !containsRune(rowText(cells, w, rowY), icons.FolderClosed) {
		t.Fatalf("expected FolderClosed glyph on Beta row, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledShowsFileGlyph picks the .go file inside
// alpha/, expands the parent so it's visible, and checks the
// language-specific glyph from icons.For lands on its row.
func TestRender_IconsEnabledShowsFileGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand so inner.go renders

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}
	want := icons.For("inner.go", false, false)
	if !containsRune(rowText(cells, w, rowY), want) {
		t.Fatalf("expected glyph %q on inner.go row, got %q",
			want, rowText(cells, w, rowY))
	}
}

// TestRender_DotFileRendersMuted verifies hidden / dotted entries
// fall back to the theme's Muted colour rather than FileColor — this
// is the visual cue users rely on to skim a tree full of metadata
// (.gitignore, .env, .github/) and find the source files at a glance.
func TestRender_DotFileRendersMuted(t *testing.T) {
	root := mkTree(t)
	// mkTree already creates .git but it's filtered by shouldHide. Add
	// a .env file that *will* show up so we can assert against its row.
	mustWrite(t, filepath.Join(root, ".env"), "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Muted) {
		t.Fatalf(".env row should render in Muted; got %q", rowText(cells, w, rowY))
	}
	// Sanity check: a non-dot file on the same level should *not* be muted.
	zetaY := findRowY(cells, w, 20, "zeta.txt")
	if zetaY < 0 {
		t.Fatal("could not find zeta.txt row")
	}
	if rowHasColor(cells, w, zetaY, theme.Default().Muted) {
		t.Fatalf("non-dot file zeta.txt should not be muted")
	}
}

// TestRender_DirtyOverridesDotMute verifies the priority cascade
// documented in drawNodeRow: a modified .env should still flip to the
// Modified colour rather than staying muted, because "this file has
// uncommitted changes" is louder information than "this is metadata".
func TestRender_DirtyOverridesDotMute(t *testing.T) {
	root := mkTree(t)
	envPath := filepath.Join(root, ".env")
	mustWrite(t, envPath, "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]bool{envPath: true}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Modified) {
		t.Fatalf("dirty .env should override Muted with Modified, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledColoursGlyphPerLanguage proves the glyph cell
// is drawn in icons.ColorFor's mapped colour rather than the row's
// regular file fg. Without this, every glyph would inherit the same
// FileColor and the visual cue (Go cyan / Markdown blue / etc.) would
// be lost.
func TestRender_IconsEnabledColoursGlyphPerLanguage(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha)

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}

	// Locate the cell carrying the .go glyph and assert its fg is the
	// per-language colour, not the row's FileColor.
	wantGlyph := []rune(icons.For("inner.go", false, false))[0]
	wantColor := icons.ColorFor("inner.go", false, theme.Default().FileColor)
	found := false
	for x := 0; x < w; x++ {
		c := cells[rowY*w+x]
		if len(c.Runes) == 0 || c.Runes[0] != wantGlyph {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg != wantColor {
			t.Fatalf("glyph fg = %v, want %v (per-language)", fg, wantColor)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("no cell carried glyph %q on inner.go row", string(wantGlyph))
	}
}

// TestReload_MarksExecutable verifies reload sets IsExec for a regular
// file carrying an execute bit while leaving an ordinary file and any
// directory unmarked — the signal that drives the ls -F '*' marker.
func TestReload_MarksExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	mustWriteMode(t, filepath.Join(root, "run.sh"), "#!/bin/sh\n", 0o755)
	mustWriteMode(t, filepath.Join(root, "notes.txt"), "hi", 0o644)
	mustMkdir(t, filepath.Join(root, "sub"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sh := findChild(tr.Root, "run.sh"); sh == nil || !sh.IsExec {
		t.Fatalf("run.sh should be marked executable: %+v", sh)
	}
	if txt := findChild(tr.Root, "notes.txt"); txt == nil || txt.IsExec {
		t.Fatalf("notes.txt should not be executable: %+v", txt)
	}
	if sub := findChild(tr.Root, "sub"); sub == nil || sub.IsExec {
		t.Fatalf("directory should never be marked executable: %+v", sub)
	}
}

// TestReload_ExecBitUpdatesOnRefresh pins the survivor-update path: a
// file that gains the execute bit after load (chmod +x) must flip to
// IsExec on the next refresh even though its *Node pointer is reused —
// otherwise a freshly-built binary would render without its marker until
// the editor restarts.
func TestReload_ExecBitUpdatesOnRefresh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	p := filepath.Join(root, "tool")
	mustWriteMode(t, p, "#!/bin/sh\n", 0o644)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	node := findChild(tr.Root, "tool")
	if node == nil || node.IsExec {
		t.Fatalf("tool should start non-executable: %+v", node)
	}

	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	tr.Refresh()

	if findChild(tr.Root, "tool") != node {
		t.Fatal("tool pointer should survive refresh (identity preservation)")
	}
	if !node.IsExec {
		t.Fatal("tool should be marked executable after chmod +x + refresh")
	}
}

// TestRender_ExecutableShowsStarSuffix confirms an executable file's row
// carries the trailing '*' marker (mirroring a directory's '/') while a
// plain file's row does not.
func TestRender_ExecutableShowsStarSuffix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	mustWriteMode(t, filepath.Join(root, "run.sh"), "#!/bin/sh\n", 0o755)
	mustWriteMode(t, filepath.Join(root, "plain.txt"), "hi", 0o644)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	shY := findRowY(cells, w, 20, "run.sh")
	if shY < 0 {
		t.Fatal("could not find run.sh row")
	}
	if !containsRune(rowText(cells, w, shY), "run.sh*") {
		t.Fatalf("run.sh row should carry trailing '*': %q", rowText(cells, w, shY))
	}

	txtY := findRowY(cells, w, 20, "plain.txt")
	if txtY < 0 {
		t.Fatal("could not find plain.txt row")
	}
	if containsRune(rowText(cells, w, txtY), "plain.txt*") {
		t.Fatalf("plain.txt row should not carry '*': %q", rowText(cells, w, txtY))
	}
}

// TestNew_ExecMarksDefaultOn documents that a freshly built tree shows
// the '*' marker out of the box; the app stamps the user's config
// preference over this at startup.
func TestNew_ExecMarksDefaultOn(t *testing.T) {
	tr, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.ExecMarks {
		t.Fatal("New() should default ExecMarks on")
	}
}

// TestRender_ExecMarksOffHidesStar verifies the render-time gate: with
// ExecMarks cleared (the ≡ toggle / config "off"), an executable file's
// row drops the trailing '*' even though its IsExec bit is still set.
func TestRender_ExecMarksOffHidesStar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	mustWriteMode(t, filepath.Join(root, "run.sh"), "#!/bin/sh\n", 0o755)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.ExecMarks = false // user turned the marker off

	cells, w := renderAndCollect(t, tr, 40, 20)
	shY := findRowY(cells, w, 20, "run.sh")
	if shY < 0 {
		t.Fatal("could not find run.sh row")
	}
	// The node is still executable — only the marker is suppressed.
	if sh := findChild(tr.Root, "run.sh"); sh == nil || !sh.IsExec {
		t.Fatalf("run.sh should still be detected executable: %+v", sh)
	}
	if containsRune(rowText(cells, w, shY), "run.sh*") {
		t.Fatalf("run.sh row should drop '*' when ExecMarks is off: %q", rowText(cells, w, shY))
	}
}

// TestRender_ExecStarInheritsDirtyColor pins the "marker, not colour"
// contract: when an executable file is also dirty, its '*' cell is drawn
// in the Modified colour along with the name — the marker rides the
// row's own style rather than introducing a competing hue.
func TestRender_ExecStarInheritsDirtyColor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	p := filepath.Join(root, "run.sh")
	mustWriteMode(t, p, "#!/bin/sh\n", 0o755)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]bool{p: true}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "run.sh")
	if rowY < 0 {
		t.Fatal("could not find run.sh row")
	}
	// Locate the '*' cell specifically and assert its fg is Modified.
	want := theme.Default().Modified
	found := false
	for x := 0; x < w; x++ {
		c := cells[rowY*w+x]
		if len(c.Runes) == 0 || c.Runes[0] != '*' {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg != want {
			t.Fatalf("'*' fg = %v, want Modified %v", fg, want)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no '*' cell found on dirty run.sh row")
	}
}

// TestRender_IconsEnabledFolderOpenSwitches verifies the open/closed
// folder glyph pair flips correctly when the user expands a folder —
// the visual cue most users will rely on more than the chevron.
func TestRender_IconsEnabledFolderOpenSwitches(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")

	// Collapsed: should show closed-folder glyph, not the open one.
	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (collapsed)")
	}
	collapsed := rowText(cells, w, rowY)
	if !containsRune(collapsed, icons.FolderClosed) {
		t.Fatalf("collapsed alpha row missing FolderClosed: %q", collapsed)
	}
	if containsRune(collapsed, icons.FolderOpen) {
		t.Fatalf("collapsed alpha row should not show FolderOpen: %q", collapsed)
	}

	// Expanded: should switch to open-folder glyph.
	tr.Toggle(alpha)
	cells, w = renderAndCollect(t, tr, 40, 20)
	rowY = findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (expanded)")
	}
	expanded := rowText(cells, w, rowY)
	if !containsRune(expanded, icons.FolderOpen) {
		t.Fatalf("expanded alpha row missing FolderOpen: %q", expanded)
	}
}

// TestContentWidth_MatchesWhatRenderDraws is the anti-drift test for the
// auto-fit measurement: whatever ContentWidth reports must be exactly the
// number of columns the renderer needs, so a panel sized to it truncates
// nothing and wastes nothing. Rendering into a screen exactly that wide
// and one column narrower proves both halves at once.
func TestContentWidth_MatchesWhatRenderDraws(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "internal"))
	mustMkdir(t, filepath.Join(root, "internal", "app"))
	mustWrite(t, filepath.Join(root, "internal", "app", "a-rather-long-name.go"), "package app")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Expand down to the deepest row — the one that sets the width.
	internal := findChild(tr.Root, "internal")
	tr.Toggle(internal)
	tr.Toggle(findChild(internal, "app"))

	want := tr.ContentWidth()
	cells, w := renderAndCollect(t, tr, want, 20)
	y := findRowY(cells, w, 20, "a-rather-long-name.go")
	if y < 0 {
		t.Fatalf("row truncated at the reported width %d", want)
	}
	// The last column must be in use: a wider answer than necessary would
	// hand the tree columns the editor could have kept.
	if txt := rowText(cells, w, y); txt[len(txt)-1] == ' ' {
		t.Fatalf("width %d is one too many; row = %q", want, txt)
	}
	// One column narrower and the same row must lose its tail.
	cells, w = renderAndCollect(t, tr, want-1, 20)
	if findRowY(cells, w, 20, "a-rather-long-name.go") >= 0 {
		t.Fatalf("width %d should have truncated the deepest row", want-1)
	}
}

// TestContentWidth_TracksExpansionNotScroll pins the two scoping choices:
// expanding a folder is what changes the answer (that's the gesture
// auto-fit reacts to), and scrolling never does — a panel that resized
// while the user wheeled past a long name would shift the editor's
// columns for no reason.
func TestContentWidth_TracksExpansionNotScroll(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "d"))
	mustWrite(t, filepath.Join(root, "d", "quite-a-long-file-name-here.go"), "x")
	for i := 0; i < 30; i++ {
		mustWrite(t, filepath.Join(root, "pad"+string(rune('a'+i))+".txt"), "x")
	}

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	collapsed := tr.ContentWidth()
	tr.Toggle(findChild(tr.Root, "d"))
	expanded := tr.ContentWidth()
	if expanded <= collapsed {
		t.Fatalf("expanding should widen: collapsed %d, expanded %d", collapsed, expanded)
	}

	// Scroll the long row off the top of a short viewport: the tree still
	// needs the same room.
	tr.Render(newSimScreen(t, 20, 6), theme.Default(), 0, 0, 20, 6)
	tr.Scroll(10)
	if got := tr.ContentWidth(); got != expanded {
		t.Fatalf("scrolling changed the width: got %d, want %d", got, expanded)
	}
}

// TestContentWidth_CountsIconsAndExecMarks documents that the measurement
// follows the same render-time gates the drawing does — turning icons on
// or the '*' marker off changes what a row occupies, so it has to change
// what the panel is sized to.
func TestContentWidth_CountsIconsAndExecMarks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix execute bit on Windows")
	}
	root := t.TempDir()
	mustWriteMode(t, filepath.Join(root, "run.sh"), "#!/bin/sh\n", 0o755)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := tr.ContentWidth()
	tr.IconsEnabled = true
	withIcons := tr.ContentWidth()
	if withIcons != plain+3 { // glyph + the two spaces after it
		t.Fatalf("icons should add 3 columns: %d → %d", plain, withIcons)
	}
	tr.ExecMarks = false
	if got := tr.ContentWidth(); got != withIcons-1 {
		t.Fatalf("dropping the '*' should lose a column: %d → %d", withIcons, got)
	}
}

// TestContentWidth_MeasuresHeaderBlock keeps the two header rows in the
// answer: a project whose own name is longer than any child would
// otherwise be the one row auto-fit truncated.
func TestContentWidth_MeasuresHeaderBlock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a-project-with-a-very-long-directory-name")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "a.go"), "x")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := tr.ContentWidth(), runeLen(" "+tr.Root.Name); got != want {
		t.Fatalf("ContentWidth = %d, want the project row's %d", got, want)
	}
}

// TestContentWidth_NilSafe — the app calls this from its draw path, where
// a nil tree is the defensive case every sidebar helper already handles.
func TestContentWidth_NilSafe(t *testing.T) {
	var tr *Tree
	if got := tr.ContentWidth(); got != 0 {
		t.Fatalf("nil tree width = %d, want 0", got)
	}
	if got := (&Tree{}).ContentWidth(); got != 0 {
		t.Fatalf("rootless tree width = %d, want 0", got)
	}
}

// newSimScreen builds a sized SimulationScreen for tests that only need
// somewhere for Render to draw (so ScrollY gets clamped against a real
// viewport) rather than the painted cells.
func newSimScreen(t *testing.T, w, h int) tcell.Screen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(w, h)
	return scr
}

// TestIsExecFile pins the shared execute-bit question both the tree's
// '*' marker and the app's Run verb ask: any execute bit on a REGULAR
// file counts, a plain file does not, and a directory never does
// (its traversal bit is not what a user reads as "executable").
func TestIsExecFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	plain := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(plain, []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}

	cases := []struct {
		path string
		want bool
	}{
		{exe, true},
		{plain, false},
		{dir, false},
	}
	for _, c := range cases {
		info, err := os.Stat(c.path)
		if err != nil {
			t.Fatalf("stat %s: %v", c.path, err)
		}
		if got := IsExecFile(info); got != c.want {
			t.Errorf("IsExecFile(%s) = %v, want %v", filepath.Base(c.path), got, c.want)
		}
	}
}

// TestRender_OverflowInputs pins what the tree owes the overflow markers
// now that it no longer paints one itself (app/overflow.go does, for the
// editor and the git panel's diff pane as well). Two obligations: Render
// must leave the last column to the names — a glyph painted here would be
// drawn over by the app's, in the wrong color and on the wrong rows — and
// RowCount / ScrollY / ListRows must add up to the hidden counts the
// marker is placed and captioned by.
func TestRender_OverflowInputs(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		name := filepath.Join(root, fmt.Sprintf("file-%02d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const w, h = 30, 12
	cells, cw := renderAndCollect(t, tr, w, h)
	off, rows := tr.ListRows(h)
	for row := off; row < off+rows; row++ {
		if got := cells[row*cw+w-1].Runes[0]; got == '▾' || got == '▴' {
			t.Fatalf("row %d's final cell = %q: the tree must not paint the marker itself", row, got)
		}
	}

	// Unscrolled: nothing above, the remainder below.
	if tr.ScrollY != 0 {
		t.Fatalf("fresh tree ScrollY = %d, want 0", tr.ScrollY)
	}
	if below := tr.RowCount() - (tr.ScrollY + rows); below != 40-rows {
		t.Errorf("hidden below = %d, want %d", below, 40-rows)
	}

	// Scrolled to the end: everything above, nothing below.
	tr.ScrollY = tr.MaxScroll(rows)
	if below := tr.RowCount() - (tr.ScrollY + rows); below != 0 {
		t.Errorf("hidden below at the bottom = %d, want 0", below)
	}
	if tr.ScrollY <= 0 {
		t.Errorf("hidden above at the bottom = %d, want > 0", tr.ScrollY)
	}
}
