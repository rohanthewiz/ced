// =============================================================================
// File: internal/app/treenav_test.go
// Author: Rohan Allison
// =============================================================================

// Tests for the file tree's keyboard layer: focus handoff, arrow
// navigation, expand/collapse, Enter, typeahead, and the n/N/d/r verbs.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// newTreeNavApp builds an App over a small fixed tree:
//
//	adir/inner.txt
//	bdir/           (empty)
//	afile.txt
//	notes.txt
func newTreeNavApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"adir", "bdir"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{"adir/inner.txt", "afile.txt", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return newTestApp(t, root)
}

// pressTreeKey sends a special key through the full router.
func pressTreeKey(a *App, k tcell.Key) {
	a.handleKey(tcell.NewEventKey(k, 0, tcell.ModNone))
}

func TestTreeFocusToggle(t *testing.T) {
	a := newTreeNavApp(t)
	a.sidebarShown = false

	a.menuFocusTree()
	if !a.treeFocus || !a.sidebarShown {
		t.Fatal("focusing should focus the tree and show the sidebar")
	}
	if a.tree.Selected == nil {
		t.Fatal("focusing should place the cursor somewhere")
	}
	a.menuFocusTree()
	if a.treeFocus {
		t.Fatal("second toggle should hand focus back")
	}
}

func TestTreeFocusSelectsActiveFile(t *testing.T) {
	a := newTreeNavApp(t)
	p := filepath.Join(a.rootDir, "notes.txt")
	a.openFile(p)
	a.menuFocusTree()
	if a.tree.Selected == nil || a.tree.Selected.Path != p {
		t.Fatalf("cursor should start on the edited file, got %+v", a.tree.Selected)
	}
}

func TestTreeArrowNavigation(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	rows := a.tree.VisibleNodes()
	// Fixture order: adir, bdir, afile.txt, notes.txt.
	a.tree.Selected = rows[0]

	pressTreeKey(a, tcell.KeyDown)
	if a.tree.Selected != rows[1] {
		t.Fatalf("Down should move to bdir, got %s", a.tree.Selected.Name)
	}
	pressTreeKey(a, tcell.KeyUp)
	pressTreeKey(a, tcell.KeyUp) // clamped at the top
	if a.tree.Selected != rows[0] {
		t.Fatal("Up should clamp at the first row")
	}
}

func TestTreeExpandCollapse(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	rows := a.tree.VisibleNodes()
	adir := rows[0]
	a.tree.Selected = adir

	pressTreeKey(a, tcell.KeyRight)
	if !adir.Expanded {
		t.Fatal("Right should expand a collapsed folder")
	}
	pressTreeKey(a, tcell.KeyRight)
	if a.tree.Selected == adir || a.tree.Selected.Name != "inner.txt" {
		t.Fatalf("second Right should step into the folder, got %s", a.tree.Selected.Name)
	}
	pressTreeKey(a, tcell.KeyLeft)
	if a.tree.Selected != adir {
		t.Fatal("Left on a child should jump to its parent")
	}
	pressTreeKey(a, tcell.KeyLeft)
	if adir.Expanded {
		t.Fatal("Left on an expanded folder should collapse it")
	}
}

func TestTreeEnterOpensFile(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	var notes = func() (n int) {
		for i, r := range a.tree.VisibleNodes() {
			if r.Name == "notes.txt" {
				return i
			}
		}
		return -1
	}()
	a.tree.Selected = a.tree.VisibleNodes()[notes]

	pressTreeKey(a, tcell.KeyEnter)
	tab := a.activeTabPtr()
	if tab == nil || filepath.Base(tab.Path) != "notes.txt" {
		t.Fatal("Enter should open the selected file")
	}
	if a.treeFocus {
		t.Fatal("opening a file should hand focus to the editor")
	}
}

func TestTreeTypeaheadJumpsAndCycles(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	rows := a.tree.VisibleNodes()
	a.tree.Selected = rows[0] // adir

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if a.tree.Selected.Name != "afile.txt" {
		t.Fatalf("typeahead should jump to the NEXT a-name, got %s", a.tree.Selected.Name)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if a.tree.Selected.Name != "adir" {
		t.Fatalf("typeahead should wrap and cycle, got %s", a.tree.Selected.Name)
	}
}

func TestTreeVerbsOpenPrompts(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	rows := a.tree.VisibleNodes()

	// 'n' on a folder: new-file prompt targeting it.
	a.tree.Selected = rows[0] // adir
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if a.modal == nil {
		t.Fatal("n should open the New file prompt")
	}
	if a.activeFolder != rows[0].Path {
		t.Fatalf("n should target the selected folder, got %s", a.activeFolder)
	}
	a.closeModal()

	// 'r' on a file: rename prompt.
	a.treeFocus = true
	sel := func(name string) {
		for _, r := range a.tree.VisibleNodes() {
			if r.Name == name {
				a.tree.Selected = r
				return
			}
		}
		t.Fatalf("no row named %s", name)
	}
	sel("notes.txt")
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	if a.modal == nil {
		t.Fatal("r should open the Rename prompt")
	}
	a.closeModal()

	// 'd': delete confirm.
	a.treeFocus = true
	sel("notes.txt")
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
	if a.modal == nil {
		t.Fatal("d should open the Delete confirm")
	}
}

// TestTreeNewFolderKey pins 'N' as the shifted twin of 'n': it resolves
// the same target (the selected folder, or a selected file's parent) and
// opens the New folder prompt, which creates a directory rather than a
// file — the tree's keyboard door onto the right-click row.
func TestTreeNewFolderKey(t *testing.T) {
	a := newTreeNavApp(t)
	a.menuFocusTree()
	rows := a.tree.VisibleNodes()

	// On a folder: targets the folder itself.
	a.tree.Selected = rows[0] // adir
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'N', tcell.ModNone))
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("N should open the New folder prompt, got %T", a.modal)
	}
	if a.activeFolder != rows[0].Path {
		t.Fatalf("N should target the selected folder, got %s", a.activeFolder)
	}
	pm.field = newTextField("sub")
	pm.submit(a)
	if info, err := os.Stat(filepath.Join(rows[0].Path, "sub")); err != nil || !info.IsDir() {
		t.Fatalf("N should create a directory: err=%v", err)
	}

	// On a file: targets the file's parent, not the file.
	a.treeFocus = true
	for _, r := range a.tree.VisibleNodes() {
		if r.Name == "notes.txt" {
			a.tree.Selected = r
		}
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'N', tcell.ModNone))
	if a.modal == nil {
		t.Fatal("N on a file should still open the New folder prompt")
	}
	if a.activeFolder != a.tree.Root.Path {
		t.Fatalf("N on a root-level file should target the root, got %s", a.activeFolder)
	}
}

func TestTreeFocusedKeysDontEditBuffer(t *testing.T) {
	a := newTreeNavApp(t)
	p := filepath.Join(a.rootDir, "notes.txt")
	a.openFile(p)
	tab := a.activeTabPtr()
	before := tab.Buffer.String()

	a.menuFocusTree()
	for _, r := range "zqy" { // unbound runes: typeahead misses, never edits
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	pressTreeKey(a, tcell.KeyDown)
	if tab.Buffer.String() != before {
		t.Fatal("tree-focused keys must never reach the buffer")
	}
}

func TestTreeLeaderStillWorksWhileFocused(t *testing.T) {
	a := newTreeNavApp(t)
	a.openFile(filepath.Join(a.rootDir, "notes.txt"))
	a.menuFocusTree()

	pressEsc(a)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	if a.modal == nil {
		t.Fatal("Esc-j should still open Go to line while the tree is focused")
	}
}

func TestTreeClickFocusesAndSelects(t *testing.T) {
	a := newTreeNavApp(t)
	a.draw() // HitTest maps clicks through the rows the last render stamped
	sx, sy, _, _ := a.sidebarRect()
	a.sidebarClick(sx+2, sy+2) // first child row (rows 0-1 are the header)
	if !a.treeFocus {
		t.Fatal("clicking a tree row should focus the tree")
	}
	if a.tree.Selected == nil || a.tree.Selected.Name != "adir" {
		t.Fatalf("clicking should move the cursor to the clicked row, got %+v", a.tree.Selected)
	}
}
