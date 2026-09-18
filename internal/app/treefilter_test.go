// =============================================================================
// File: internal/app/treefilter_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for type-to-find in the focused file tree: the keys that build,
// cycle and clear the pattern, the scope it searches, and the scroll that
// brings an off-screen first match into view. Matching and painting are
// pinned in internal/filetree/filter_test.go.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// filterApp seeds root/{sub/{apple.txt, apricot.txt, cdrom.txt},
// avocado.txt, cards.txt}, expands sub, focuses the tree, and returns the
// app with a name→node index.
func filterApp(t *testing.T) (*App, map[string]*filetree.Node) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, rel := range []string{"sub/apple.txt", "sub/apricot.txt", "sub/cdrom.txt", "avocado.txt", "cards.txt"} {
		if err := writeFile(filepath.Join(root, rel), "x\n"); err != nil {
			t.Fatalf("seed %s: %v", rel, err)
		}
	}
	a := newTestApp(t, root)
	idx := map[string]*filetree.Node{}
	for _, c := range a.tree.Root.Children {
		idx[c.Name] = c
		if c.IsDir {
			a.tree.Toggle(c)
			for _, g := range c.Children {
				idx[g.Name] = g
			}
		}
	}
	a.focusTree()
	return a, idx
}

// typeTree sends each rune of s through the full key router.
func typeTree(a *App, s string) {
	for _, r := range s {
		pressRune(a, r)
	}
}

// TestTreeFilter_FirstMatchTakesTheCursor pins the core gesture: letters
// build a multi-rune pattern and the cursor lands on the first match in
// display order, re-evaluated per keystroke.
func TestTreeFilter_FirstMatchTakesTheCursor(t *testing.T) {
	a, idx := filterApp(t)
	typeTree(a, "a")
	if a.tree.Selected != idx["apple.txt"] {
		t.Fatalf("'a' → %s, want apple.txt", a.tree.Selected.Name)
	}
	typeTree(a, "v")
	if a.tree.Filter != "av" || a.tree.Selected != idx["avocado.txt"] {
		t.Fatalf("'av' → filter %q cursor %s", a.tree.Filter, a.tree.Selected.Name)
	}
}

// TestTreeFilter_ScopedToActiveFolder pins "the current folder and
// below": with sub active, a root-level avocado.txt is outside the search.
func TestTreeFilter_ScopedToActiveFolder(t *testing.T) {
	a, idx := filterApp(t)
	a.setActiveFolder(idx["sub"].Path)
	typeTree(a, "a")
	got := a.tree.FilterMatches()
	if len(got) != 2 || got[0] != idx["apple.txt"] || got[1] != idx["apricot.txt"] {
		t.Fatalf("scoped matches wrong: %d rows", len(got))
	}
	typeTree(a, "v")
	if a.tree.Selected != idx["apple.txt"] {
		t.Fatal("a miss must leave the cursor where it was")
	}
	if !strings.Contains(a.statusMsg, "No names in sub/ contain") {
		t.Fatalf("miss flash = %q", a.statusMsg)
	}
}

// TestTreeFilter_CollapsedActiveFolderSearchesProject pins the fallback:
// an active folder showing no contents can only ever answer "no
// matches", so the search widens to the whole project instead.
func TestTreeFilter_CollapsedActiveFolderSearchesProject(t *testing.T) {
	a, idx := filterApp(t)
	a.tree.Toggle(idx["sub"]) // fold
	a.setActiveFolder(idx["sub"].Path)
	typeTree(a, "av")
	if a.tree.Selected != idx["avocado.txt"] {
		t.Fatalf("cursor on %s, want avocado.txt", a.tree.Selected.Name)
	}
}

// TestTreeFilter_MatchesMidName pins the substring rule end to end:
// "rom" starts nowhere but sits inside cdrom.txt, and the cursor goes
// there — and its leading 'r' is a letter, not the old Rename key.
func TestTreeFilter_MatchesMidName(t *testing.T) {
	a, idx := filterApp(t)
	typeTree(a, "rom")
	if a.modal != nil {
		t.Fatal("'r' opened a modal")
	}
	if a.tree.Selected != idx["cdrom.txt"] {
		t.Fatalf("'rom' → %s, want cdrom.txt", a.tree.Selected.Name)
	}
}

// TestTreeFilter_MarkKeysExtendARunningPattern pins that Space and *,
// the two keys still bound in the tree, only act as mark keys on an EMPTY
// pattern: mid-search they are just characters of the name being sought.
func TestTreeFilter_MarkKeysExtendARunningPattern(t *testing.T) {
	a, _ := filterApp(t)
	typeTree(a, "c ")
	if a.tree.MarkCount() != 0 {
		t.Fatal("Space inside a pattern ticked a row")
	}
	if a.tree.Filter != "c " {
		t.Fatalf("filter = %q, want %q", a.tree.Filter, "c ")
	}
}

// TestTreeFilter_BackspaceTrimsThenClears pins the way out by Backspace:
// the pattern shrinks (re-jumping to the wider first match) and the last
// rune switches the filter off rather than matching everything.
func TestTreeFilter_BackspaceTrimsThenClears(t *testing.T) {
	a, idx := filterApp(t)
	typeTree(a, "apr")
	if a.tree.Selected != idx["apricot.txt"] {
		t.Fatal("setup: 'apr' should land on apricot.txt")
	}
	pressTreeKey(a, tcell.KeyBackspace2)
	if a.tree.Filter != "ap" || a.tree.Selected != idx["apple.txt"] {
		t.Fatalf("after ⌫: filter %q cursor %s", a.tree.Filter, a.tree.Selected.Name)
	}
	pressTreeKey(a, tcell.KeyBackspace2)
	pressTreeKey(a, tcell.KeyBackspace2)
	if a.tree.Filter != "" {
		t.Fatalf("filter %q should be cleared", a.tree.Filter)
	}
}

// TestTreeFilter_EscAndFocusLossClear pins the other two exits: Esc as a
// side effect (it still arms the leader), and the tree losing the
// keyboard, where nothing could clear it any more.
func TestTreeFilter_EscAndFocusLossClear(t *testing.T) {
	a, _ := filterApp(t)
	typeTree(a, "a")
	pressEsc(a)
	if a.tree.Filter != "" {
		t.Fatal("Esc should clear the filter")
	}
	if !a.treeFocus {
		t.Fatal("Esc should not take the keyboard from the tree")
	}

	a.lastEscape = a.lastEscape.AddDate(0, 0, -1) // let the leader window lapse
	typeTree(a, "a")
	a.treeFocus = false
	a.draw()
	if a.tree.Filter != "" {
		t.Fatal("losing focus should clear the filter")
	}
}

// TestTreeFilter_TabCyclesAndWraps pins the cycle that moved off repeated
// letters (which now extend the pattern): Tab forward, Shift-Tab back,
// both wrapping.
func TestTreeFilter_TabCyclesAndWraps(t *testing.T) {
	a, idx := filterApp(t)
	typeTree(a, "ap") // apple, apricot
	pressTreeKey(a, tcell.KeyTab)
	if a.tree.Selected != idx["apricot.txt"] {
		t.Fatalf("Tab → %s", a.tree.Selected.Name)
	}
	pressTreeKey(a, tcell.KeyTab)
	if a.tree.Selected != idx["apple.txt"] {
		t.Fatalf("Tab should wrap to apple.txt, got %s", a.tree.Selected.Name)
	}
	pressTreeKey(a, tcell.KeyBacktab)
	if a.tree.Selected != idx["apricot.txt"] {
		t.Fatalf("Shift-Tab should wrap back to apricot.txt, got %s", a.tree.Selected.Name)
	}
}

// TestTreeFilter_TabWithoutFilterIsInert pins that Tab means nothing to a
// tree with no pattern — it must not move the cursor or leak anywhere.
func TestTreeFilter_TabWithoutFilterIsInert(t *testing.T) {
	a, idx := filterApp(t)
	a.tree.Selected = idx["cards.txt"]
	pressTreeKey(a, tcell.KeyTab)
	if a.tree.Selected != idx["cards.txt"] {
		t.Fatal("Tab moved the cursor with no filter armed")
	}
}

// TestTreeFilter_ScrollsToOffscreenFirstMatch pins the second half of the
// request: a first match below the viewport is scrolled into view.
func TestTreeFilter_ScrollsToOffscreenFirstMatch(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 80; i++ {
		if err := writeFile(filepath.Join(root, fmt.Sprintf("f%02d.txt", i)), "x\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFile(filepath.Join(root, "zz-last.txt"), "x\n"); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.focusTree()
	a.draw()
	if a.tree.ScrollY != 0 {
		t.Fatal("setup: tree should start at the top")
	}
	typeTree(a, "zz")
	last := a.tree.Selected
	if last == nil || last.Name != "zz-last.txt" {
		t.Fatal("cursor should be on zz-last.txt")
	}
	if a.tree.ScrollY == 0 {
		t.Fatal("tree did not scroll to the off-screen match")
	}
	treeRowY(t, a, last) // fails the test if the row is not on screen
}
